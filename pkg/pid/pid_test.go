package pid

import (
	"testing"
	"time"
)

func TestPIDController_LowUsage_ScaleUp(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TargetUtilization = 0.65 // 目标 65%
	cfg.MinRatio = 1.0
	cfg.MaxRatio = 2.0
	ctrl := NewController(cfg)

	// 模拟长期处于 30% 低利用率
	for i := 0; i < 10; i++ {
		time.Sleep(10 * time.Millisecond)
		ratio := ctrl.Update(0.30)
		t.Logf("周期 %d: 实际利用率 30%%, 动态超卖比调节为: %.2f", i+1, ratio)
	}

	finalRatio := ctrl.GetCurrentRatio()
	if finalRatio <= 1.0 {
		t.Fatalf("预期低利用率下超卖比放大 (>1.0), 实际为: %.2f", finalRatio)
	}
	if finalRatio > 2.0 {
		t.Fatalf("预期超卖比不超过 MaxRatio 2.0, 实际为: %.2f", finalRatio)
	}
}

func TestPIDController_HighUsage_ScaleDown(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TargetUtilization = 0.65
	cfg.MinRatio = 1.0
	cfg.MaxRatio = 2.0
	ctrl := NewController(cfg)

	// 先将其推升至较高超卖比
	ctrl.currentRatio = 1.8

	// 模拟突发高负载 85%
	for i := 0; i < 10; i++ {
		time.Sleep(10 * time.Millisecond)
		ratio := ctrl.Update(0.85)
		t.Logf("周期 %d: 实际利用率 85%%, 动态超卖比压制为: %.2f", i+1, ratio)
	}

	finalRatio := ctrl.GetCurrentRatio()
	if finalRatio > 1.2 {
		t.Fatalf("预期高负载下超卖比快速回退压制 (<=1.2), 实际为: %.2f", finalRatio)
	}
}

func TestPIDController_Deadband(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TargetUtilization = 0.65
	cfg.Deadband = 0.03
	ctrl := NewController(cfg)
	ctrl.currentRatio = 1.5

	// 变化在死区内 (64% 距 65% 仅 1%)
	ratio := ctrl.Update(0.64)
	if ratio != 1.5 {
		t.Fatalf("死区内预期超卖比保持 1.5 不变，实际为: %.2f", ratio)
	}
}
