package finops

import (
	"sync"
	"time"

	"github.com/mritd/goadmission/pkg/metrics"
)

// Config FinOps 财务成本核算配置
type Config struct {
	CPUCostPerHourRMB   float64 // 每核 CPU 每小时的折算成本 (默认 0.25 元/核时)
	MemCostPerHourGBRMB float64 // 每 GB 内存每小时的折算成本 (默认 0.03 元/GB时)
}

// DefaultConfig 默认云上标准核算配置 (对标阿里云/腾讯云通用型机型折算)
func DefaultConfig() Config {
	return Config{
		CPUCostPerHourRMB:   0.25,
		MemCostPerHourGBRMB: 0.03,
	}
}

// Engine FinOps 降本核算引擎
type Engine struct {
	mu                   sync.RWMutex
	config               Config
	totalSavedCores      float64
	totalSavedMemGB      float64
	totalAccumulatedCost float64
	lastCalculatedTime   time.Time
}

var (
	globalEngine *Engine
	engineOnce   sync.Once
)

// GetGlobalEngine 获取全局单例 FinOps 引擎
func GetGlobalEngine() *Engine {
	engineOnce.Do(func() {
		globalEngine = NewEngine(DefaultConfig())
	})
	return globalEngine
}

// NewEngine 创建 FinOps 核算引擎
func NewEngine(cfg Config) *Engine {
	return &Engine{
		config:             cfg,
		lastCalculatedTime: time.Now(),
	}
}

// RecordNodeOversold 记录单个节点的超卖容量增量
func (e *Engine) RecordNodeOversold(nodeName string, capacityCPU, allocatableCPU float64, capacityMemGB, allocatableMemGB float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	savedCPU := allocatableCPU - capacityCPU
	if savedCPU < 0 {
		savedCPU = 0
	}
	savedMem := allocatableMemGB - capacityMemGB
	if savedMem < 0 {
		savedMem = 0
	}

	now := time.Now()
	elapsedHours := now.Sub(e.lastCalculatedTime).Hours()
	if elapsedHours <= 0 {
		elapsedHours = 5.0 / 3600.0 // 默认按 5 秒步长微积分
	}
	e.lastCalculatedTime = now

	// 实时成本节约微积分 (元)
	hourlySavings := savedCPU*e.config.CPUCostPerHourRMB + savedMem*e.config.MemCostPerHourGBRMB
	costSavings := hourlySavings * elapsedHours
	e.totalAccumulatedCost += costSavings
	e.totalSavedCores = savedCPU
	e.totalSavedMemGB = savedMem

	// 同步上报到 Prometheus FinOps 专属指标大盘
	metrics.RecordFinOpsMetrics(nodeName, savedCPU, savedMem, hourlySavings, e.totalAccumulatedCost)
}

// GetStats 获取当前财务节省汇总快照
func (e *Engine) GetStats() (savedCores, savedMemGB, totalSavingsRMB float64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.totalSavedCores, e.totalSavedMemGB, e.totalAccumulatedCost
}
