package metrics

import (
	"sync"
)

// NodeMetrics 节点当前物理使用率数据
type NodeMetrics struct {
	CPUUsageRatio float64 // CPU 真实使用率 (0.0 ~ 1.0)
	MemUsageRatio float64 // 内存真实使用率 (0.0 ~ 1.0)
}

// Collector 节点利用率指标收集接口
type Collector interface {
	GetNodeMetrics(nodeName string) (NodeMetrics, error)
}

// MockCollector 模拟指标收集器 (用于单元测试、本地演示及离线沙箱环境)
type MockCollector struct {
	mu      sync.RWMutex
	metrics map[string]NodeMetrics
}

// NewMockCollector 创建模拟采集器
func NewMockCollector() *MockCollector {
	return &MockCollector{
		metrics: make(map[string]NodeMetrics),
	}
}

// SetNodeMetrics 设置指定节点的模拟指标
func (m *MockCollector) SetNodeMetrics(nodeName string, cpuUsage, memUsage float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics[nodeName] = NodeMetrics{
		CPUUsageRatio: cpuUsage,
		MemUsageRatio: memUsage,
	}
}

// GetNodeMetrics 获取指定节点的物理指标
func (m *MockCollector) GetNodeMetrics(nodeName string) (NodeMetrics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if val, ok := m.metrics[nodeName]; ok {
		return val, nil
	}
	// 默认未设置时返回安全的基线基准值 30%
	return NodeMetrics{CPUUsageRatio: 0.30, MemUsageRatio: 0.35}, nil
}
