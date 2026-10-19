package finops

import (
	"testing"
	"time"
)

func TestFinOpsCalculator(t *testing.T) {
	cfg := Config{
		CPUCostPerHourRMB:   0.25,
		MemCostPerHourGBRMB: 0.03,
	}
	engine := NewEngine(cfg)

	// 模拟节点 Capacity: 4 核, 8GB; Allocatable 放大为: 8 核, 12GB (超卖增量: 4 核, 4GB)
	engine.RecordNodeOversold("test-worker-1", 4.0, 8.0, 8.0, 12.0)

	savedCores, savedMemGB, totalCost := engine.GetStats()
	if savedCores != 4.0 {
		t.Fatalf("期望节省核数为 4.0, 实际为: %.2f", savedCores)
	}
	if savedMemGB != 4.0 {
		t.Fatalf("期望节省内存为 4.0 GB, 实际为: %.2f", savedMemGB)
	}
	if totalCost <= 0 {
		t.Fatalf("期望累计节约金额大于 0, 实际为: %.4f", totalCost)
	}

	time.Sleep(10 * time.Millisecond)
	engine.RecordNodeOversold("test-worker-1", 4.0, 8.0, 8.0, 12.0)
	_, _, newTotalCost := engine.GetStats()
	if newTotalCost <= totalCost {
		t.Fatalf("时间推进后累计节省成本应当单调递增, 前: %.4f, 后: %.4f", totalCost, newTotalCost)
	}
}
