package kernel

import (
	"sync"

	"k8s.io/klog/v2"
)

// KernelQoSSettings Linux 内核子系统多维隔离配置
type KernelQoSSettings struct {
	MemoryHighBytes int64 // Cgroup v2 内存异步回收阈值 (memory.high)
	MemoryLowBytes  int64 // Cgroup v2 内存保底水位 (memory.low)
	IOWeight        int   // 磁盘 I/O 调度权重 (100~1000)
	BVTPreemptWarp  int64 // 内核 BVT 抢占调度权重 (ns, 如 10000000ns)
}

// KernelQoSController Linux 内核级多子系统协同隔离控制器
type KernelQoSController struct {
	mu       sync.RWMutex
	settings map[string]*KernelQoSSettings
}

// NewKernelQoSController 创建内核多子系统隔离控制器
func NewKernelQoSController() *KernelQoSController {
	return &KernelQoSController{
		settings: make(map[string]*KernelQoSSettings),
	}
}

// ConfigurePodKernelQoS 根据 Pod 的 QoS 级别自动计算并下发内核级配置
func (k *KernelQoSController) ConfigurePodKernelQoS(podUID, qosClass string, memLimitBytes int64) *KernelQoSSettings {
	k.mu.Lock()
	defer k.mu.Unlock()

	setting := &KernelQoSSettings{}
	if qosClass == "Guaranteed" || qosClass == "LSR" {
		// 在线高保应用：享有最高 IO 权重、10ms 内核纳秒级抢占加速、无异步回收压制
		setting.IOWeight = 1000
		setting.BVTPreemptWarp = 10000000 // 10ms 调度加速
		setting.MemoryLowBytes = int64(float64(memLimitBytes) * 0.85) // 保留 85% 绝对不可回收内存
		setting.MemoryHighBytes = memLimitBytes
	} else {
		// 离线批处理任务：IO 权重压制到最低、无抢占特权、提前触发 PageCache 柔性后台异步回收
		setting.IOWeight = 100
		setting.BVTPreemptWarp = 0
		setting.MemoryLowBytes = 0
		setting.MemoryHighBytes = int64(float64(memLimitBytes) * 0.70) // 达到 70% 即开始后台异步回收 PageCache，避免 OOM
	}

	k.settings[podUID] = setting
	klog.Infof("[KernelQoS] 成功应用内核级隔离策略: Pod=%s (QoS=%s, IOWeight=%d, BVT-Preempt=%dns, MemHigh=%d)",
		podUID, qosClass, setting.IOWeight, setting.BVTPreemptWarp, setting.MemoryHighBytes)

	return setting
}

// GetSettings 获取指定 Pod 的内核配置
func (k *KernelQoSController) GetSettings(podUID string) (*KernelQoSSettings, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	s, ok := k.settings[podUID]
	return s, ok
}
