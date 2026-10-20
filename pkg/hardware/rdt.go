package hardware

import (
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

// DefaultResctrlRoot 是 Linux 内核 resctrl 伪文件系统的挂载点。
const DefaultResctrlRoot = "/sys/fs/resctrl"

// CLOS 分组名。Intel RDT 的 CLOS（Class of Service）就是 resctrl 下的一级目录。
const (
	CLOSOnline  = "CLOS_ONLINE"
	CLOSOffline = "CLOS_OFFLINE"
)

// RDTConfig Intel RDT (Resource Director Technology) 硬件隔离配置
type RDTConfig struct {
	EnableCAT      bool   // 是否启用 L3 Cache 硬件隔离 (Cache Allocation Technology)
	EnableMBA      bool   // 是否启用内存带宽硬件隔离 (Memory Bandwidth Allocation)
	L3OfflineCBM   string // 离线任务分配的 L3 Cache 位掩码（回退值，真机优先按 cbm_mask 计算）
	MBAOfflineRate int    // 离线任务内存带宽百分比限制
}

// DefaultRDTConfig 默认工业级 RDT 安全配置
func DefaultRDTConfig() RDTConfig {
	return RDTConfig{
		EnableCAT:      true,
		EnableMBA:      true,
		L3OfflineCBM:   "0x3",
		MBAOfflineRate: 20,
	}
}

// RDTManager Intel RDT 硬件三级缓存与内存带宽调度管理器（基于 /sys/fs/resctrl）
//
// resctrl 的三个关键文件：
//
//	<root>/info/L3/cbm_mask    该平台 L3 掩码上限（全 1 表示可用全部 way），必须读，不能硬编码
//	<root>/<group>/schemata    L3 与 MB 的配额，整组覆盖写，L3 用十六进制、MB 用十进制
//	<root>/<group>/tasks       把线程放进来，写的是 TID（不是 PID）
type RDTManager struct {
	mu     sync.RWMutex
	config RDTConfig
	groups map[string]string // podUID -> CLOS 组名

	root      string
	supported bool
	reason    string
	l3Ways    int   // L3 总 way 数，由 cbm_mask 的置位数推出
	cacheIDs  []int // L3 cache id（≈ socket id），2 路机器是 [0,1]
	hasMB     bool
}

// NewRDTManager 创建 RDT 管理器，自动探测本机 resctrl 能力。
func NewRDTManager(cfg RDTConfig) *RDTManager {
	return NewRDTManagerAt(DefaultResctrlRoot, cfg)
}

// NewRDTManagerAt 指定 resctrl 根目录，便于单元测试与容器内自定义挂载。
func NewRDTManagerAt(root string, cfg RDTConfig) *RDTManager {
	m := &RDTManager{
		config: cfg,
		groups: make(map[string]string),
		root:   root,
	}
	m.detect()
	return m
}

// detect 探测 resctrl 是否可用。
//
// 判断依据是 info/L3/cbm_mask 是否存在，而不是"目录在不在"：
// 内核只在 CPU 真的支持 CAT 且 rdt 参数启用时才导出这个文件。
// 前提条件是 CPU 具备 cat_l3 与 mba 两个 flag（Xeon Scalable 及以后），
// 消费级酷睿和绝大多数云上共享实例都没有。
func (m *RDTManager) detect() {
	cbmPath := filepath.Join(m.root, "info", "L3", "cbm_mask")
	raw, err := os.ReadFile(cbmPath)
	if err != nil {
		m.supported = false
		m.reason = "未找到 " + cbmPath + "（CPU 不支持 CAT，或未挂载 resctrl）"
		return
	}
	mask := strings.TrimSpace(string(raw))
	v, perr := strconv.ParseUint(strings.TrimPrefix(mask, "0x"), 16, 64)
	if perr != nil || v == 0 {
		m.supported = false
		m.reason = "cbm_mask 内容非法: " + mask
		return
	}
	m.l3Ways = bits.OnesCount64(v)
	m.supported = true

	if fi, err := os.Stat(filepath.Join(m.root, "info", "MB")); err == nil && fi.IsDir() {
		m.hasMB = true
	}
	m.cacheIDs = m.readCacheIDs()
}

// readCacheIDs 从根 schemata 里解析出 L3 cache id 列表。
// 单路机器只有 0，双路机器是 0 和 1，格式形如 "L3:0=fffff;1=fffff"。
func (m *RDTManager) readCacheIDs() []int {
	b, err := os.ReadFile(filepath.Join(m.root, "schemata"))
	if err != nil {
		return []int{0}
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "L3:") {
			continue
		}
		var ids []int
		for _, seg := range strings.Split(strings.TrimPrefix(line, "L3:"), ";") {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			kv := strings.SplitN(seg, "=", 2)
			if len(kv) != 2 {
				continue
			}
			if id, err := strconv.Atoi(strings.TrimSpace(kv[0])); err == nil {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			return ids
		}
	}
	return []int{0}
}

func (m *RDTManager) Supported() bool { return m.supported }

// UnsupportedReason 返回不可用原因，方便在 Agent 启动日志里说清楚为什么降级。
func (m *RDTManager) UnsupportedReason() string { return m.reason }

// L3Ways 返回本平台 L3 的 way 总数。
func (m *RDTManager) L3Ways() int { return m.l3Ways }

// maskForPercent 按百分比算出连续的 L3 位掩码。
//
// 为什么必须是连续位：x86 的 CAT 要求 CBM 的置位必须连续，0x5（101）这种
// 中间有空洞的掩码会被内核直接拒绝。
func (m *RDTManager) maskForPercent(pct int) string {
	if m.l3Ways <= 0 {
		return m.config.L3OfflineCBM
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	n := (m.l3Ways*pct + 99) / 100 // 向上取整，至少给 1 个 way
	if n < 1 {
		n = 1
	}
	if n > m.l3Ways {
		n = m.l3Ways
	}
	var mask uint64 = (uint64(1) << uint(n)) - 1
	return fmt.Sprintf("0x%x", mask)
}

// buildSchemata 拼出整组 schemata 内容。
//
// 格式细节（踩过才知道）：
//   - L3 用十六进制，MB 用十进制
//   - 每个 cache id 一段，用 ";" 分隔
//   - 每行必须以换行结尾
//   - 一次写入必须把要改的资源全带上，schemata 是"整组覆盖"语义
func (m *RDTManager) buildSchemata(l3Mask string, mbPct int) string {
	ids := m.cacheIDs
	if len(ids) == 0 {
		ids = []int{0}
	}

	var sb strings.Builder
	if m.config.EnableCAT {
		sb.WriteString("L3:")
		for i, id := range ids {
			if i > 0 {
				sb.WriteString(";")
			}
			sb.WriteString(strconv.Itoa(id))
			sb.WriteString("=")
			sb.WriteString(strings.TrimPrefix(l3Mask, "0x"))
		}
		sb.WriteString("\n")
	}
	if m.config.EnableMBA && m.hasMB {
		sb.WriteString("MB:")
		for i, id := range ids {
			if i > 0 {
				sb.WriteString(";")
			}
			sb.WriteString(strconv.Itoa(id))
			sb.WriteString("=")
			sb.WriteString(strconv.Itoa(mbPct))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ApplySchemata 创建（或复用）CLOS 组并写入配额。
// 本机不支持 resctrl 时直接返回 ErrRDTUnsupported，由调用方决定是否降级。
func (m *RDTManager) ApplySchemata(group, l3Mask string, mbPct int) error {
	if !m.supported {
		return fmt.Errorf("resctrl 不可用: %s", m.reason)
	}
	dir := filepath.Join(m.root, group)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("创建 CLOS 组 %s 失败: %w", group, err)
		}
	}

	content := m.buildSchemata(l3Mask, mbPct)
	if content == "" {
		return nil
	}
	fp := filepath.Join(dir, "schemata")

	// 读回比较，避免无意义重写：写 schemata 会让该 CLOS 下所有 CPU 的
	// IA32_L3_MASK_n MSR 重新编程，代价不低。
	if cur, err := os.ReadFile(fp); err == nil && normalizeSchemata(string(cur)) == normalizeSchemata(content) {
		return nil
	}
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", fp, err)
	}
	klog.Infof("[Intel-RDT] CLOS %s 已生效: %s", group, strings.ReplaceAll(content, "\n", " "))
	return nil
}

// AssignPID 把进程放进指定 CLOS 组。
//
// 写的是 tasks 而不是 cgroup.procs：resctrl 的 tasks 接收 TID（线程 id），
// 只写主线程 PID 的话，进程里其他线程仍然跑在默认组里，隔离等于没做。
func (m *RDTManager) AssignPID(group string, pid int) error {
	if !m.supported {
		return fmt.Errorf("resctrl 不可用: %s", m.reason)
	}
	fp := filepath.Join(m.root, group, "tasks")
	if err := os.WriteFile(fp, []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("把进程 %d 移入 %s 失败: %w", pid, group, err)
	}
	return nil
}

// AssignCLOS 为容器分配 RDT 服务等级（CLOS）。
//
// 在线业务 (Guaranteed/LSR) -> 独占 L3 与 100% 内存带宽
// 离线任务 (BestEffort/BE)   -> 压制至专属限速组
func (m *RDTManager) AssignCLOS(podUID, qosClass string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	targetGroup := CLOSOnline
	if qosClass == "BestEffort" || qosClass == "BE" {
		targetGroup = CLOSOffline
	}
	m.groups[podUID] = targetGroup

	mask, mba := m.GetGroupConfig(targetGroup)
	klog.Infof("[Intel-RDT] Pod %s 分配等级 %s (CAT-Mask: %s, MBA-Rate: %d%%, resctrl可用: %v)",
		podUID, targetGroup, mask, mba, m.supported)
	return targetGroup
}

// GetGroupConfig 获取指定 RDT 分组的硬件配额。
// 支持 resctrl 时按本平台 cbm_mask 实时计算；否则回退到配置里的保守值。
func (m *RDTManager) GetGroupConfig(groupName string) (string, int) {
	if groupName == CLOSOffline {
		if m.supported && m.config.EnableCAT {
			return m.maskForPercent(m.config.MBAOfflineRate), m.config.MBAOfflineRate
		}
		return m.config.L3OfflineCBM, m.config.MBAOfflineRate
	}
	if m.supported && m.config.EnableCAT {
		return m.maskForPercent(100), 100
	}
	return "0x7ff", 100
}

// ApplyForPod 一步完成"分配组 + 写 schemata + 放进程"，是 Agent 实际调用的入口。
// 任何一步失败都返回错误，由 Agent 记录并继续（RDT 属于增强项，不该阻塞主流程）。
func (m *RDTManager) ApplyForPod(podUID, qosClass string, pid int) (string, error) {
	if !m.supported {
		return "", fmt.Errorf("resctrl 不可用: %s", m.reason)
	}
	group := m.AssignCLOS(podUID, qosClass)
	mask, mba := m.GetGroupConfig(group)
	if err := m.ApplySchemata(group, mask, mba); err != nil {
		return group, err
	}
	if pid > 0 {
		if err := m.AssignPID(group, pid); err != nil {
			return group, err
		}
	}
	return group, nil
}

// normalizeSchemata 去掉行尾空白与空行，用于比较内容是否等价。
func normalizeSchemata(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, ";")
}
