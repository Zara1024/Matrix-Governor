package eviction

import (
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEvictionExecutor_CooldownAndExecution(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	cfg := ExecutorConfig{
		CooldownPeriod:        2 * time.Second, // 测试使用 2 秒冷却期
		MaxEvictionsPerMinute: 1,
	}
	executor := NewExecutor(fakeClient, NewScorer(), cfg)

	nodeName := "worker-node-01"

	// 构造候选 Pod 1: 核心生产库 (得分极低 0.001)
	podProd := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "mysql-core", Namespace: "default"},
		Status:     v1.PodStatus{QOSClass: v1.PodQOSGuaranteed},
	}
	candProd := PodCandidate{Pod: podProd, TotalScore: 0.001}

	// 构造候选 Pod 2: 离线批处理 (得分高 4.30)
	podBatch := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "spark-worker-1", Namespace: "default"},
		Status:     v1.PodStatus{QOSClass: v1.PodQOSBestEffort},
	}
	candBatch := PodCandidate{Pod: podBatch, TotalScore: 4.30, HasPDB: true, DisruptionsLeft: 3}

	ctx := context.TODO()
	// 在 fakeClient 中预置待驱逐 Pod
	_, _ = fakeClient.CoreV1().Pods("default").Create(ctx, podProd, metav1.CreateOptions{})
	_, _ = fakeClient.CoreV1().Pods("default").Create(ctx, podBatch, metav1.CreateOptions{})

	// 1. 首次触发驱逐: 预期命中高分的 spark-worker-1
	evicted, err := executor.ExecuteBestEviction(ctx, nodeName, []PodCandidate{candProd, candBatch})

	if err != nil {
		t.Fatalf("预期首次驱逐成功, 实际失败: %v", err)
	}
	if evicted.Pod.Name != "spark-worker-1" {
		t.Fatalf("预期驱逐目标为 spark-worker-1, 实际为: %s", evicted.Pod.Name)
	}
	t.Logf("首次驱逐验证成功: %s (总分: %.2f)", evicted.Pod.Name, evicted.TotalScore)

	// 2. 紧接着立即尝试第二次驱逐: 预期被 2s 冷却窗口拦截
	_, errCooldown := executor.ExecuteBestEviction(ctx, nodeName, []PodCandidate{candProd, candBatch})
	if errCooldown == nil {
		t.Fatalf("冷却期内预期被拦截, 但未返回错误")
	}
	t.Logf("防抖冷却期成功拦截连续驱逐: %v", errCooldown)

	// 3. 全高保业务免死保护测试
	_, errProtected := executor.ExecuteBestEviction(ctx, "worker-node-02", []PodCandidate{candProd})
	if errProtected == nil {
		t.Fatalf("全高保业务预期触发免死保护, 但未被拒绝")
	}
	t.Logf("全高保业务免死保护生效: %v", errProtected)
}
