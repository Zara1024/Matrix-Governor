package kernel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultRoot 是 cgroup 文件系统的默认挂载点。
const DefaultRoot = "/sys/fs/cgroup"

// DefaultPeriod 是 CFS 的默认调度周期（微秒），与内核默认值一致。
const DefaultPeriod int64 = 100000

// CgroupMaxSymbol 是 cgroup v2 用来表示"无限"的字符串，读回来不是数字。
const CgroupMaxSymbol = "max"

var (
	// ErrDirNotExist 目标 cgroup 目录不存在（Pod 还没被 kubelet 创建，或已被回收）。
	ErrDirNotExist = errors.New("cgroup 目录不存在")
	// ErrFileNotExist 目标控制文件不存在（当前内核/版本不支持该资源）。
	ErrFileNotExist = errors.New("cgroup 控制文件不存在")
	// ErrInvalidValue 待写入的值不合法。
	ErrInvalidValue = errors.New("cgroup 写入值非法")
)

// Executor 是节点侧唯一的内核写入入口。
//
// 设计原则（对齐 Koordinator 的 resourceexecutor）：
//  1. 写之前先读，值相同就不写。cgroup 写入不是免费的：写 cpuset.cpus 会触发
//     整棵 cpuset 子树重算并遍历所有线程做 sched_setaffinity；高频无脑重写等于自造抖动。
//  2. 三道前置校验（目录存在 / 文件存在 / 值合法），任何一道不过都直接返回可判定的错误，
//     不做"尽力而为"的盲写。
//  3. v1/v2 差异全部收敛在 Resource 与 Path 里，调用方只表达意图。
type Executor struct {
	rootDir string
	version Version
}

// NewExecutor 创建执行器，rootDir 传空串时使用 /sys/fs/cgroup。
// 版本自动探测，也可以在测试里用 NewExecutorWithVersion 注入。
func NewExecutor(rootDir string) *Executor {
	if rootDir == "" {
		rootDir = DefaultRoot
	}
	return &Executor{rootDir: rootDir, version: DetectVersion(rootDir)}
}

// NewExecutorWithVersion 用于测试：显式指定版本，避免依赖宿主机环境。
func NewExecutorWithVersion(rootDir string, v Version) *Executor {
	if rootDir == "" {
		rootDir = DefaultRoot
	}
	return &Executor{rootDir: rootDir, version: v}
}

func (e *Executor) RootDir() string  { return e.rootDir }
func (e *Executor) Version() Version { return e.version }

// DetectVersion 探测 cgroup 版本。
//
// 严格做法是用 statfs 判文件系统 magic（CGROUP2_SUPER_MAGIC = 0x63677270，
// TMPFS_MAGIC = 0x01021994），这里用特征文件探测是为了不引入额外依赖并保持可跨平台测试：
//   - v2 根目录下有 cgroup.controllers
//   - v1 根目录下有各控制器子目录，cpu 子目录里有 cpu.shares
//   - systemd 的 hybrid 模式会把 v2 挂在 /sys/fs/cgroup/unified
func DetectVersion(rootDir string) Version {
	if rootDir == "" {
		rootDir = DefaultRoot
	}
	if fileExists(filepath.Join(rootDir, "cgroup.controllers")) {
		return VersionV2
	}
	if fileExists(filepath.Join(rootDir, "cpu", "cpu.shares")) {
		return VersionV1
	}
	if fileExists(filepath.Join(rootDir, "unified", "cgroup.controllers")) {
		return VersionV2
	}
	return VersionUnknown
}

// DirPath 返回某个 cgroup 目录在文件系统中的绝对路径。
// relDir 是相对于 cgroup 根的相对路径（例如 kubepods.slice/...），v1 下会自动补上控制器子目录。
func (e *Executor) DirPath(relDir string, r Resource) string {
	if e.version == VersionV1 {
		return filepath.Join(e.rootDir, r.Controller(), relDir)
	}
	return filepath.Join(e.rootDir, relDir)
}

// Path 返回某个控制文件的绝对路径。
func (e *Executor) Path(relDir string, r Resource) string {
	name := r.FileName(e.version)
	if name == "" {
		return ""
	}
	return filepath.Join(e.DirPath(relDir, r), name)
}

// Read 读取控制文件内容（去掉首尾空白与换行）。
func (e *Executor) Read(relDir string, r Resource) (string, error) {
	fp := e.Path(relDir, r)
	if fp == "" {
		return "", fmt.Errorf("%w: %s 在 cgroup %s 下没有对应文件", ErrFileNotExist, r.Type(), e.version)
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrFileNotExist, fp)
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// ReadInt 读取整数值，兼容 v2 的 "max"（返回 -1 表示无限）。
func (e *Executor) ReadInt(relDir string, r Resource) (int64, error) {
	s, err := e.Read(relDir, r)
	if err != nil {
		return 0, err
	}
	if s == CgroupMaxSymbol || s == "-1" {
		return -1, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("解析 %s 的值 %q 失败: %w", r.Type(), s, err)
	}
	return n, nil
}

// WriteIfDifferent 是核心写入原语：值相同则不写，返回是否真的发生了写入。
func (e *Executor) WriteIfDifferent(relDir string, r Resource, value string) (bool, error) {
	value = strings.TrimSpace(value)

	fp := e.Path(relDir, r)
	if fp == "" {
		return false, fmt.Errorf("%w: %s 在 cgroup %s 下没有对应文件", ErrFileNotExist, r.Type(), e.version)
	}

	// 闸 1：目录必须在
	dir := filepath.Dir(fp)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return false, fmt.Errorf("%w: %s", ErrDirNotExist, dir)
	}

	// 闸 2：值必须合法
	if ok, msg := r.IsValid(value); !ok {
		return false, fmt.Errorf("%w: %s=%q (%s)", ErrInvalidValue, r.Type(), value, msg)
	}

	// 闸 3：文件必须在（即当前内核真的支持这个接口）
	if !fileExists(fp) {
		return false, fmt.Errorf("%w: %s", ErrFileNotExist, fp)
	}

	// 先读后比
	current, err := e.Read(relDir, r)
	if err == nil && e.valuesEqual(r, current, value) {
		return false, nil
	}

	if err := os.WriteFile(fp, []byte(value), 0644); err != nil {
		return false, fmt.Errorf("写入 %s 失败: %w", fp, err)
	}
	return true, nil
}

// valuesEqual 判断内核当前值与期望值是否等价。
// 两个特例必须处理，否则会导致每轮都重写：
//  1. cpuset 的字符串表示会被内核压缩（"2,3" -> "2-3"），必须按集合比较
//  2. v2 的 "max" 与 MaxInt64 / -1 表示同一个含义
func (e *Executor) valuesEqual(r Resource, current, desired string) bool {
	if current == desired {
		return true
	}
	switch r.Type() {
	case CPUSet.Type(), CPUSetMems.Type():
		return CPUSetEqual(current, desired)
	}
	if desired == CgroupMaxSymbol && (current == "-1" || current == "9223372036854771712") {
		return true
	}
	if current == CgroupMaxSymbol && (desired == "-1" || desired == "9223372036854771712") {
		return true
	}
	return false
}

// SetCPUQuota 设置 CPU 带宽上限，屏蔽 v1/v2 的接口差异。
//
// quota <= 0 表示不限：
//   - v1 写 -1 到 cpu.cfs_quota_us
//   - v2 写 "max" 到 cpu.max
//
// v1 下必须先写 period 再写 quota：period 变化会让内核对已生效的 quota 做一次重新校准。
func (e *Executor) SetCPUQuota(relDir string, quota, period int64) (bool, error) {
	if period <= 0 {
		period = DefaultPeriod
	}

	if e.version == VersionV2 {
		// 必须把 period 一起写进去：内核虽然接受只写 "max"（此时 period 取默认 100000），
		// 但那样会静默覆盖掉调用方显式配置的 period。
		value := fmt.Sprintf("%s %d", CgroupMaxSymbol, period)
		if quota > 0 {
			value = fmt.Sprintf("%d %d", quota, period)
		}
		return e.WriteIfDifferent(relDir, CPUQuota, value)
	}

	changedPeriod, err := e.WriteIfDifferent(relDir, CPUPeriod, strconv.FormatInt(period, 10))
	if err != nil {
		return changedPeriod, err
	}
	q := quota
	if q <= 0 {
		q = -1
	}
	changedQuota, err := e.WriteIfDifferent(relDir, CPUQuota, strconv.FormatInt(q, 10))
	return changedPeriod || changedQuota, err
}

// GetCPUQuota 读取当前 CPU 带宽配置，quota 返回 -1 表示不限。
func (e *Executor) GetCPUQuota(relDir string) (quota int64, period int64, err error) {
	period = DefaultPeriod

	if e.version == VersionV2 {
		s, rerr := e.Read(relDir, CPUQuota)
		if rerr != nil {
			return 0, 0, rerr
		}
		fields := strings.Fields(s)
		if len(fields) == 0 {
			return 0, 0, fmt.Errorf("cpu.max 内容为空")
		}
		if fields[0] == CgroupMaxSymbol {
			quota = -1
		} else {
			quota, err = strconv.ParseInt(fields[0], 10, 64)
			if err != nil {
				return 0, 0, err
			}
		}
		if len(fields) > 1 {
			period, err = strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, 0, err
			}
		}
		return quota, period, nil
	}

	quota, err = e.ReadInt(relDir, CPUQuota)
	if err != nil {
		return 0, 0, err
	}
	if p, perr := e.ReadInt(relDir, CPUPeriod); perr == nil && p > 0 {
		period = p
	}
	return quota, period, nil
}

// SetCPUSet 设置独占 CPU 集合。
//
// v1 下 cpuset.cpus 与 cpuset.mems 必须成对设置：只设 cpus 不设 mems 时，
// 该 cgroup 的 cpuset.mems 为空，内核会拒绝把任务移进来（write cgroup.procs 报 ENOSPC）。
func (e *Executor) SetCPUSet(relDir string, cpus []int, mems []int) (bool, error) {
	changed, err := e.WriteIfDifferent(relDir, CPUSet, FormatCPUList(cpus))
	if err != nil {
		return changed, err
	}
	if len(mems) > 0 {
		changedMems, merr := e.WriteIfDifferent(relDir, CPUSetMems, FormatCPUList(mems))
		if merr != nil {
			return changed, merr
		}
		changed = changed || changedMems
	}
	return changed, nil
}

// GetCPUSet 读取当前生效的 CPU 集合。
func (e *Executor) GetCPUSet(relDir string) ([]int, error) {
	s, err := e.Read(relDir, CPUSet)
	if err != nil {
		return nil, err
	}
	return ParseCPUList(s)
}

// EnsureDir 逐级创建 cgroup 目录，并按需在父级开启控制器下发。
//
// cgroup v2 有个硬约束：控制器必须在父级的 cgroup.subtree_control 里启用，
// 子级才会出现对应的控制文件。systemd 管理的节点上，kubepods.slice 这几层
// 是 kubelet 建的、控制器已经开好；这里主要给测试和自检场景用。
func (e *Executor) EnsureDir(relDir string, controllers ...string) error {
	if relDir == "" {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(relDir), "/")
	cur := ""
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		parent := cur
		if cur == "" {
			cur = p
		} else {
			cur = cur + "/" + p
		}
		abs := e.DirPath(cur, CPUSet)
		if err := os.MkdirAll(abs, 0755); err != nil {
			return fmt.Errorf("创建 cgroup 目录 %s 失败: %w", abs, err)
		}
		if e.version == VersionV2 && len(controllers) > 0 {
			if err := e.enableControllers(parent, controllers); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Executor) enableControllers(relDir string, controllers []string) error {
	if e.version != VersionV2 {
		return nil
	}
	fp := filepath.Join(e.DirPath(relDir, CPUSet), "cgroup.subtree_control")
	if !fileExists(fp) {
		return nil
	}
	for _, c := range controllers {
		// 写入 "+cpu" 表示启用；重复启用是幂等的，内核不报错。
		if err := os.WriteFile(fp, []byte("+"+c), 0644); err != nil {
			// 已被父级限制、或该控制器不在 cgroup.controllers 里时会失败，属于可降级情形。
			if os.IsPermission(err) {
				return fmt.Errorf("在 %s 启用控制器 %s 失败: %w", fp, c, err)
			}
		}
	}
	return nil
}

// MovePID 把进程移入 cgroup。
// 必须在 cpuset 设置完成之后调用，否则空 cpuset 的 cgroup 会拒绝接收任务。
func (e *Executor) MovePID(relDir string, pid int) error {
	fp := filepath.Join(e.DirPath(relDir, CPUSet), "cgroup.procs")
	if !fileExists(fp) {
		return fmt.Errorf("%w: %s", ErrFileNotExist, fp)
	}
	return os.WriteFile(fp, []byte(strconv.Itoa(pid)), 0644)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
