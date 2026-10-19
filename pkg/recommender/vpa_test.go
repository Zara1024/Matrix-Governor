package recommender

import (
	"testing"
)

func TestVPARecommender(t *testing.T) {
	rec := NewVPARecommender(0.15) // 15% 冗余

	// 模拟开发申请了 4.0 核，8.0 GiB，但历史 10 次采样实际利用率均在 0.5~1.0 核之间
	cpuSamples := []float64{0.5, 0.6, 0.7, 0.8, 0.5, 0.9, 1.0, 0.6, 0.7, 0.8}
	memSamples := []float64{1.0, 1.2, 1.5, 1.8, 1.1, 1.3, 1.4, 1.2, 1.6, 1.7}

	result := rec.RecommendRequest("web-order-api", "prod", 4.0, 8.0, cpuSamples, memSamples)

	// P95 约在 1.0 核，+15% 冗余后约为 1.15 核
	if result.RecommendedCPU > 1.5 || result.RecommendedCPU < 1.0 {
		t.Fatalf("推荐 CPU 规格不合理: %.2f (预期约 1.15核)", result.RecommendedCPU)
	}

	if result.SavedCPURatio <= 0.50 {
		t.Fatalf("原申请 4核，实际使用约 1核，节省比例应当大于 50%%，实际为: %.2f", result.SavedCPURatio)
	}
}
