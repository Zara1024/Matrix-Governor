package scheduler

import (
	"testing"
	"time"
)

func TestGangScheduler(t *testing.T) {
	gs := NewGangScheduler()

	// 注册一个需要 3 个副本协同的大数据 Spark 任务组
	gs.RegisterGangGroup("spark-pi-job", 3, 500*time.Millisecond)

	// 1. 第 1 个 Pod 到达：未集齐，拒绝单独调度
	permitted, msg := gs.PermitPod("spark-pi-job", "spark-pod-1")
	if permitted {
		t.Fatalf("成员未集齐时应当挂起等待: %s", msg)
	}

	// 2. 第 2 个 Pod 到达：未集齐，继续挂起
	permitted, msg = gs.PermitPod("spark-pi-job", "spark-pod-2")
	if permitted {
		t.Fatalf("成员未集齐时应当挂起等待: %s", msg)
	}

	// 3. 第 3 个 Pod 到达：成员集齐 (3/3)，满足全进全出，触发全量调度
	permitted, msg = gs.PermitPod("spark-pi-job", "spark-pod-3")
	if !permitted {
		t.Fatalf("成员集齐时应准许全量调度: %s", msg)
	}

	// 4. 超时测试
	gs.RegisterGangGroup("spark-timeout-job", 2, 20*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	permitted, msg = gs.PermitPod("spark-timeout-job", "spark-timeout-pod-1")
	if permitted {
		t.Fatalf("超时后应拒绝调度并触发回滚: %s", msg)
	}
}
