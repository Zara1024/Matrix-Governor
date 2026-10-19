package eviction

import (
	"fmt"
	"math"
	"sort"

	v1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
)

// PodCandidate 包含 Pod 及其计算出的差异化驱逐积分
type PodCandidate struct {
	Pod             *v1.Pod
	HasPDB          bool    // 规则1：是否绑定了 PDB
	DisruptionsLeft int32   // 规则2：当前 PDB 允许的可中断副本数 N
	QoSScore        float64 // 规则3：QoS 服务质量等级评分 (BestEffort 0.2, Burstable 0.1, Guaranteed 0.0)
	OvercommitScore float64 // 规则4：超配比归一化评分 (0 ~ 0.1)
	MemoryScore     float64 // 规则5：内存大小归一化评分 (0 ~ 0.01)
	TotalScore      float64 // 综合驱逐总积分 (总分越高，越优先被选为驱逐候选)
}

// Scorer 5 维加权差异化驱逐打分器
type Scorer struct{}

// NewScorer 创建驱逐打分器实例
func NewScorer() *Scorer {
	return &Scorer{}
}

// ScorePod 根据图片5设计的规则，对单个候选 Pod 计算差异化驱逐积分
// pod: 候选 Pod
// pdb: 匹配的 PodDisruptionBudget (若无匹配则传 nil)
// actualCpuUsageMillicores: 真实采集到的 CPU 毫核占用
// actualMemUsageBytes: 真实采集到的内存占用字节数
func (s *Scorer) ScorePod(pod *v1.Pod, pdb *policyv1beta1.PodDisruptionBudget, actualCpuUsageMillicores int64, actualMemUsageBytes int64) PodCandidate {
	candidate := PodCandidate{
		Pod: pod,
	}

	// 规则 1：Pod 是否含 PDB (+1 分)
	if pdb != nil {
		candidate.HasPDB = true
		candidate.TotalScore += 1.0

		// 规则 2：可驱逐数 N (+N 分)
		// disruptionsAllowed 代表当前还能容忍中断多少个副本
		if pdb.Status.DisruptionsAllowed > 0 {
			candidate.DisruptionsLeft = pdb.Status.DisruptionsAllowed
			candidate.TotalScore += float64(pdb.Status.DisruptionsAllowed)
		}
	}

	// 规则 3：QoS 等级评分 (BestEffort: +0.2分, Burstable: +0.1分, Guaranteed: +0分)
	qosClass := pod.Status.QOSClass
	if qosClass == "" {
		qosClass = computeQoSClass(pod)
	}

	switch qosClass {
	case v1.PodQOSBestEffort:
		candidate.QoSScore = 0.2
	case v1.PodQOSBurstable:
		candidate.QoSScore = 0.1
	case v1.PodQOSGuaranteed:
		candidate.QoSScore = 0.0
	default:
		candidate.QoSScore = 0.1
	}
	candidate.TotalScore += candidate.QoSScore

	// 规则 4：超配比 (0 ~ 0.1分)
	// 超配比 = Request 容量 / 实际使用量。虚标越严重 (比值越高)，得分越接近 0.1
	var requestedCpuMillicores int64 = 0
	for _, container := range pod.Spec.Containers {
		if reqCpu, ok := container.Resources.Requests[v1.ResourceCPU]; ok {
			requestedCpuMillicores += reqCpu.MilliValue()
		}
	}

	if requestedCpuMillicores > 0 && actualCpuUsageMillicores > 0 {
		ratio := float64(requestedCpuMillicores) / float64(actualCpuUsageMillicores)
		// 归一化映射至 [0, 0.1]
		// 假设 ratio=1 为不超配 (0分)，ratio>=5 为极度虚配 (+0.1分)
		overcommitScore := math.Min(0.1, math.Max(0.0, (ratio-1.0)*0.025))
		candidate.OvercommitScore = math.Round(overcommitScore*1000) / 1000
	} else if requestedCpuMillicores > 0 && actualCpuUsageMillicores == 0 {
		// 申请了资源却完全没跑流量，给满额 0.1 分
		candidate.OvercommitScore = 0.1
	}
	candidate.TotalScore += candidate.OvercommitScore

	// 规则 5：内存大小 (0 ~ 0.01分)
	// 占用内存越小，驱逐引起的大页开销越低，优先换取 Pod 数目腾挪
	// 归一化：将内存按比例映射至 0 ~ 0.01 分
	if actualMemUsageBytes > 0 {
		// 以 4GB 为基准，4GB 以下获得 0.005 ~ 0.01 分
		memScore := math.Max(0.0, 0.01-float64(actualMemUsageBytes)/(4*1024*1024*1024)*0.01)
		candidate.MemoryScore = math.Round(memScore*10000) / 10000
	} else {
		candidate.MemoryScore = 0.005
	}
	candidate.TotalScore += candidate.MemoryScore

	candidate.TotalScore = math.Round(candidate.TotalScore*10000) / 10000
	return candidate
}

// RankCandidates 对所有候选 Pod 按积分从高到低排序，排在最前面的是最佳驱逐对象
func (s *Scorer) RankCandidates(candidates []PodCandidate) []PodCandidate {
	sorted := make([]PodCandidate, len(candidates))
	copy(sorted, candidates)

	sort.Slice(sorted, func(i, j int) bool {
		// 总分高的排在前面 (优先驱逐)
		if sorted[i].TotalScore != sorted[j].TotalScore {
			return sorted[i].TotalScore > sorted[j].TotalScore
		}
		// 总分相同时，优先驱逐可中断副本数更多的
		return sorted[i].DisruptionsLeft > sorted[j].DisruptionsLeft
	})
	return sorted
}

// computeQoSClass 辅助计算 Pod 的 QoS 级别
func computeQoSClass(pod *v1.Pod) v1.PodQOSClass {
	requests := v1.ResourceList{}
	limits := v1.ResourceList{}
	zeroQuantity := true

	for _, container := range pod.Spec.Containers {
		for name, quantity := range container.Resources.Requests {
			if !quantity.IsZero() {
				zeroQuantity = false
			}
			requests[name] = quantity
		}
		for name, quantity := range container.Resources.Limits {
			limits[name] = quantity
		}
	}

	if len(requests) == 0 && len(limits) == 0 && zeroQuantity {
		return v1.PodQOSBestEffort
	}

	isGuaranteed := true
	for _, supportedResource := range []v1.ResourceName{v1.ResourceCPU, v1.ResourceMemory} {
		req, hasReq := requests[supportedResource]
		lim, hasLim := limits[supportedResource]
		if !hasReq || !hasLim || req.Cmp(lim) != 0 {
			isGuaranteed = false
			break
		}
	}

	if isGuaranteed {
		return v1.PodQOSGuaranteed
	}
	return v1.PodQOSBurstable
}

// String 打印候选者得分详情
func (c *PodCandidate) String() string {
	return fmt.Sprintf("Pod: %s/%s | 总分: %.4f (PDB:%t, 允许中断数:%d, QoS分:%.2f, 超配分:%.3f, 内存分:%.4f)",
		c.Pod.Namespace, c.Pod.Name, c.TotalScore, c.HasPDB, c.DisruptionsLeft, c.QoSScore, c.OvercommitScore, c.MemoryScore)
}
