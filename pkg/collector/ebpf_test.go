package collector

import (
	"testing"
)

func TestPMUCollector(t *testing.T) {
	col := NewPMUCollector(1.0) // 基准黄金 CPI 为 1.0

	// 1. 正常低负载：CPI=1.05, PSI=0.02 -> 未受干扰
	m1 := col.CollectMetrics("pod-payment-svc", 1.05, 5.0, 0.02, 0.01)
	if m1.IsInterfered {
		t.Fatalf("正常状态下不应被判定为干扰: CPI=%.2f", m1.CPI)
	}

	// 2. 突发受离线邻居挤压 Cache：CPI 飙升至 1.80 (超出基准 1.5倍) -> 判定微架构静默干扰
	m2 := col.CollectMetrics("pod-payment-svc", 1.80, 25.0, 0.15, 0.05)
	if !m2.IsInterfered {
		t.Fatalf("CPI 飙升至 1.8 时应当被判定为受到微架构干扰！")
	}

	// 3. PSI 突破 25% -> 触发干扰判定
	m3 := col.CollectMetrics("pod-payment-svc", 1.20, 8.0, 0.25, 0.10)
	if !m3.IsInterfered {
		t.Fatalf("CPU PSI 达到 25%% 时应当被判定为受到微架构干扰！")
	}
}
