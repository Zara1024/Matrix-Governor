package recommender

import (
	"math"
	"sort"
	"sync"

	"k8s.io/klog/v2"
)

// ResourceRecommendation 推荐资源规格结果
type ResourceRecommendation struct {
	PodName           string
	Namespace         string
	CurrentRequestCPU float64 // 当前申请的 CPU (核)
	CurrentRequestMem float64 // 当前申请的内存 (GiB)
	RecommendedCPU    float64 // 智能推荐 CPU (核)
	RecommendedMem    float64 // 智能推荐内存 (GiB)
	SavedCPURatio     float64 // 节省比例 (0~1.0)
	Reason            string
}

// VPARecommender 资源智能画像与 Request 推荐引擎 (对标 Crane Recommendation Engine)
type VPARecommender struct {
	mu           sync.RWMutex
	safetyMargin float64 // 安全冗余缓冲比率 (默认 15%，即 P95 利用率 * 1.15)
}

// NewVPARecommender 创建智能推荐引擎
func NewVPARecommender(safetyMargin float64) *VPARecommender {
	if safetyMargin <= 0 {
		safetyMargin = 0.15
	}
	return &VPARecommender{
		safetyMargin: safetyMargin,
	}
}

// RecommendRequest 分析历史时间序列采样样本，使用分位数 (P95) + 动态安全余量计算推荐规格
func (r *VPARecommender) RecommendRequest(name, namespace string, currCPU, currMem float64, cpuSamples, memSamples []float64) *ResourceRecommendation {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(cpuSamples) == 0 || len(memSamples) == 0 {
		return &ResourceRecommendation{
			PodName:           name,
			Namespace:         namespace,
			CurrentRequestCPU: currCPU,
			CurrentRequestMem: currMem,
			RecommendedCPU:    currCPU,
			RecommendedMem:    currMem,
			SavedCPURatio:     0,
			Reason:            "历史样本不足，维持现状",
		}
	}

	// 1. 计算 P95 分位数 (过滤突发偶发单点毛刺)
	sort.Float64s(cpuSamples)
	sort.Float64s(memSamples)

	p95IdxCPU := int(math.Ceil(float64(len(cpuSamples))*0.95)) - 1
	p95IdxMem := int(math.Ceil(float64(len(memSamples))*0.95)) - 1
	if p95IdxCPU < 0 {
		p95IdxCPU = 0
	}
	if p95IdxMem < 0 {
		p95IdxMem = 0
	}

	p95CPU := cpuSamples[p95IdxCPU]
	p95Mem := memSamples[p95IdxMem]

	// 2. 结合安全冗余系数 (Safety Margin: +15%)
	recommendedCPU := p95CPU * (1.0 + r.safetyMargin)
	recommendedMem := p95Mem * (1.0 + r.safetyMargin)

	// 保底不低于 0.1 核 / 0.1 GiB
	if recommendedCPU < 0.1 {
		recommendedCPU = 0.1
	}
	if recommendedMem < 0.1 {
		recommendedMem = 0.1
	}

	savedRatio := (currCPU - recommendedCPU) / currCPU
	if savedRatio < 0 {
		savedRatio = 0
	}

	rec := &ResourceRecommendation{
		PodName:           name,
		Namespace:         namespace,
		CurrentRequestCPU: currCPU,
		CurrentRequestMem: currMem,
		RecommendedCPU:    math.Round(recommendedCPU*100) / 100,
		RecommendedMem:    math.Round(recommendedMem*100) / 100,
		SavedCPURatio:     math.Round(savedRatio*100) / 100,
		Reason:            "基于历史真实利用率 P95 与 15% 安全余量智能收敛规格",
	}

	klog.Infof("[VPARecommender] 为工作负载 %s/%s 生成智能推荐: 原规格=%.2f核/%.2fGiB -> 推荐=%.2f核/%.2fGiB (算力缩减率=%.1f%%)",
		namespace, name, currCPU, currMem, rec.RecommendedCPU, rec.RecommendedMem, rec.SavedCPURatio*100)

	return rec
}
