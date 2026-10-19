package eviction

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestScorer_5Dimension_Ranking(t *testing.T) {
	scorer := NewScorer()

	// 1. Pod A: 核心数据库/高保服务 (Guaranteed, 无PDB)
	podA := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "mysql-master", Namespace: "prod"},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: "mysql",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("4000m"),
							v1.ResourceMemory: resource.MustParse("8Gi"),
						},
						Limits: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("4000m"),
							v1.ResourceMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
		},
		Status: v1.PodStatus{QOSClass: v1.PodQOSGuaranteed},
	}

	// 2. Pod B: 在线普通 Web 服务 (Burstable, 含 PDB, 允许中断 1 个)
	podB := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-app-1", Namespace: "prod"},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: "web",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("2000m"),
							v1.ResourceMemory: resource.MustParse("2Gi"),
						},
						Limits: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("4000m"),
							v1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
		},
		Status: v1.PodStatus{QOSClass: v1.PodQOSBurstable},
	}
	pdbB := &policyv1beta1.PodDisruptionBudget{
		Status: policyv1beta1.PodDisruptionBudgetStatus{
			DisruptionsAllowed: 1,
		},
	}

	// 3. Pod C: 离线分析任务 (BestEffort, 含 PDB, 允许中断 3 个, 虚配严重)
	podC := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "batch-worker-3", Namespace: "batch"},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: "worker",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU: resource.MustParse("4000m"),
						},
					},
				},
			},
		},
		Status: v1.PodStatus{QOSClass: v1.PodQOSBestEffort},
	}
	pdbC := &policyv1beta1.PodDisruptionBudget{
		Status: policyv1beta1.PodDisruptionBudgetStatus{
			DisruptionsAllowed: 3,
		},
	}

	// 计算积分
	// Pod A: 实际 CPU 3800m (基本用满，无超配)，内存 6GB，无 PDB
	candA := scorer.ScorePod(podA, nil, 3800, 6*1024*1024*1024)

	// Pod B: 实际 CPU 500m (超配比 4x)，内存 1GB，含 PDB (N=1)
	candB := scorer.ScorePod(podB, pdbB, 500, 1024*1024*1024)

	// Pod C: 实际 CPU 100m (超配比 40x)，内存 200MB，含 PDB (N=3)
	candC := scorer.ScorePod(podC, pdbC, 100, 200*1024*1024)

	t.Logf("Candidate A (核心生产): %s", candA.String())
	t.Logf("Candidate B (在线普通): %s", candB.String())
	t.Logf("Candidate C (离线批处理): %s", candC.String())

	// 排序
	ranked := scorer.RankCandidates([]PodCandidate{candA, candB, candC})

	// 验证排位：
	// 期望排序必须是: Pod C (总分最高 ~4.3) -> Pod B (次高 ~2.1) -> Pod A (最低 ~0.0)
	if ranked[0].Pod.Name != "batch-worker-3" {
		t.Fatalf("预期首选驱逐目标为 batch-worker-3, 实际为: %s", ranked[0].Pod.Name)
	}
	if ranked[1].Pod.Name != "web-app-1" {
		t.Fatalf("预期次选驱逐目标为 web-app-1, 实际为: %s", ranked[1].Pod.Name)
	}
	if ranked[2].Pod.Name != "mysql-master" {
		t.Fatalf("预期最低驱逐目标为 mysql-master, 实际为: %s", ranked[2].Pod.Name)
	}

	t.Logf("5维加权驱逐打分排序验证完全通过！")
}
