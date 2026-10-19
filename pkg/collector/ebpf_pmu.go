package collector

import (
	"sync"

	"k8s.io/klog/v2"
)

// MicroArchMetrics 微架构硬件性能指标 (PMU + PSI)
type MicroArchMetrics struct {
	CPI            float64 // 每指令时钟周期数 (Cycles Per Instruction，基准通常 0.8~1.2)
	MPKI           float64 // 每千指令 L3 Cache 缺失数 (Misses Per Kilo Instructions)
	CPUPsiAvg10    float64 // CPU 10秒平均压力 (Linux Pressure Stall Information)
	MemPsiAvg10    float64 // Memory 10秒平均压力
	IsInterfered   bool    // 是否被判定为受微架构干扰
}

// PMUCollector eBPF 与 PMU 性能监控收集器
type PMUCollector struct {
	mu            sync.RWMutex
	metricsCache  map[string]*MicroArchMetrics
	baselineCPI   float64 // 基准黄金 CPI (默认 1.0)
	cpiThreshold  float64 // 干扰阈值比率 (默认 1.5倍基准即判定为受干扰)
}

// NewPMUCollector 创建 eBPF/PMU 性能监控收集器
func NewPMUCollector(baselineCPI float64) *PMUCollector {
	if baselineCPI <= 0 {
		baselineCPI = 1.0
	}
	return &PMUCollector{
		metricsCache: make(map[string]*MicroArchMetrics),
		baselineCPI:  baselineCPI,
		cpiThreshold: 1.5,
	}
}

// CollectMetrics 模拟或通过 eBPF 从 Hardware PMU 读取指定 Pod 的纳秒级硬件指标
func (c *PMUCollector) CollectMetrics(podUID string, cpi, mpki, cpuPsi, memPsi float64) *MicroArchMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 判定微架构级静默干扰：如果 CPI 超过基准的 1.5 倍或 PSI 突破 20%
	isInterfered := false
	if cpi >= (c.baselineCPI*c.cpiThreshold) || cpuPsi >= 0.20 {
		isInterfered = true
		klog.Warningf("[eBPF-PMU] 警告: 检测到核心 Pod %s 发生物理微架构干扰！当前 CPI=%.2f (基准=%.2f, 增幅=%.1f%%), CPU-PSI=%.2f%%",
			podUID, cpi, c.baselineCPI, (cpi/c.baselineCPI-1)*100, cpuPsi*100)
	}

	m := &MicroArchMetrics{
		CPI:          cpi,
		MPKI:         mpki,
		CPUPsiAvg10:  cpuPsi,
		MemPsiAvg10:  memPsi,
		IsInterfered: isInterfered,
	}
	c.metricsCache[podUID] = m
	return m
}

// GetMetrics 获取指标快照
func (c *PMUCollector) GetMetrics(podUID string) (*MicroArchMetrics, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.metricsCache[podUID]
	return m, ok
}
