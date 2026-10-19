package cgroup

import (
	"testing"
)

func TestCgroupSuppressor(t *testing.T) {
	sup := NewSuppressor(0.20) // 压制至 20%

	// 注册高保核心 Pod (Guaranteed) 和离线批处理 Pod (BestEffort)
	sup.RegisterPod("mysql-master", "prod", "Guaranteed", 200000, 100000)
	sup.RegisterPod("spark-worker", "batch", "BestEffort", 100000, 100000)

	// 1. 水位低 (30%)：均不压制
	results := sup.ReconcileByWatermark(0.30)
	if q, state, _ := sup.GetPodState("batch", "spark-worker"); state != StateUnsuppressed || q != 100000 {
		t.Fatalf("正常状态下离线 Pod 不应被压制，当前状态: %s, quota: %d", state, q)
	}

	// 2. 突发冲高至 78% (突破 75% 警戒区)：离线 Pod 应被秒级压制至 20%
	results = sup.ReconcileByWatermark(0.78)
	if results["batch/spark-worker"] != StateThrottled {
		t.Fatalf("高水位下离线 Pod 应当被标记为 Throttled，实际为: %s", results["batch/spark-worker"])
	}
	quota, state, _ := sup.GetPodState("batch", "spark-worker")
	if state != StateThrottled || quota != 20000 {
		t.Fatalf("离线 Pod 配额应压制为 20000, 实际为: %d", quota)
	}

	// 高保 Pod 绝对免压制
	if _, state, found := sup.GetPodState("prod", "mysql-master"); found && state != StateUnsuppressed {
		t.Fatalf("Guaranteed 核心业务绝不能被压制！")
	}

	// 3. 水位恢复至 50%：离线 Pod 配额平滑恢复
	_ = sup.ReconcileByWatermark(0.50)
	quotaRecovered, stateRecovered, _ := sup.GetPodState("batch", "spark-worker")
	if stateRecovered != StateUnsuppressed || quotaRecovered != 100000 {
		t.Fatalf("水位回落后离线 Pod 应恢复原始配额 100000，实际为: %d, 状态: %s", quotaRecovered, stateRecovered)
	}
}
