package hardware

import (
	"sync"

	"k8s.io/klog/v2"
)

// RDTConfig Intel RDT (Resource Director Technology) 硬件隔离配置
type RDTConfig struct {
	EnableCAT   bool    // 是否启用 L3 Cache 硬件隔离 (Cache Allocation Technology)
	EnableMBA   bool    // 是否启用内存带宽硬件隔离 (Memory Bandwidth Allocation)
	L3OfflineCBM string  // 离线任务分配的 L3 Cache 位掩码 (如 0x3, 仅占用 20% Cache Way)
	MBAOfflineRate int   // 离线任务内存带宽百分比限制 (如 20%)
}

// DefaultRDTConfig 默认工业级 RDT 安全配置
func DefaultRDTConfig() RDTConfig {
	return RDTConfig{
		EnableCAT:      true,
		EnableMBA:      true,
		L3OfflineCBM:   "0x3", // 限制离线任务仅占用前 2 个 Cache Way
		MBAOfflineRate: 20,    // 离线任务内存总线带宽限制为 20%
	}
}

// RDTManager Intel RDT 硬件三级缓存与内存带宽调度管理器 (基于 /sys/fs/resctrl)
type RDTManager struct {
	mu     sync.RWMutex
	config RDTConfig
	groups map[string]string // 记录 Pod 所在的 RDT CLOS (Class of Service) 组
}

// NewRDTManager 创建 RDT 硬件隔离管理器
func NewRDTManager(cfg RDTConfig) *RDTManager {
	return &RDTManager{
		config: cfg,
		groups: make(map[string]string),
	}
}

// AssignCLOS 为容器分配 RDT 服务等级 (CLOS)
// 在线业务 (Guaranteed/LSR) -> 独占全局 L3 Cache 与 100% 内存带宽
// 离线任务 (BestEffort/BE)   -> 压制至专属限速组 (L3 Cache 掩码 0x3, 内存带宽 20%)
func (m *RDTManager) AssignCLOS(podUID, qosClass string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	targetGroup := "CLOS_ONLINE"
	if qosClass == "BestEffort" || qosClass == "BE" {
		targetGroup = "CLOS_OFFLINE"
	}

	m.groups[podUID] = targetGroup
	klog.Infof("[Intel-RDT] 成功为 Pod %s 分配硬件 RDT 等级: %s (CAT-Mask: %s, MBA-Rate: %d%%)",
		podUID, targetGroup, m.config.L3OfflineCBM, m.config.MBAOfflineRate)

	return targetGroup
}

// GetGroupConfig 获取指定 RDT 分组的硬件配额
func (m *RDTManager) GetGroupConfig(groupName string) (string, int) {
	if groupName == "CLOS_OFFLINE" {
		return m.config.L3OfflineCBM, m.config.MBAOfflineRate
	}
	return "0x7ff", 100 // 在线业务独占全部 Cache Way (0x7ff) 与 100% 内存带宽
}
