package nodemanager

import (
	"testing"

	"github.com/mritd/goadmission/pkg/metrics"
	"github.com/mritd/goadmission/pkg/pid"
)

func TestNodeManager_Lifecycle_And_Fuse(t *testing.T) {
	mockColl := metrics.NewMockCollector()
	pidCfg := pid.DefaultConfig()
	mgr := NewManager(mockColl, pidCfg, 0.80)

	nodeName := "k8s-worker-node-01"

	// 1. 注册纳管
	info := mgr.RegisterNode(nodeName)
	if info.HealthState != StateNormal {
		t.Fatalf("预期初始状态为 Normal, 实际为: %s", info.HealthState)
	}

	// 2. 模拟处于 30% 负载 (安全区)
	mgr.UpdateNodeMetrics(nodeName, 0.30, 0.35)
	cpuRatio, memRatio, isFused := mgr.GetDynamicRatio(nodeName)
	if isFused {
		t.Fatalf("30%% 负载下不应触发熔断")
	}
	t.Logf("30%% 负载下节点 %s 动态超卖比: CPU=%.2f, Mem=%.2f", nodeName, cpuRatio, memRatio)

	// 3. 模拟突发过载到 85% (突破 80% 熔断线)
	mgr.UpdateNodeMetrics(nodeName, 0.85, 0.82)
	cpuRatioFused, memRatioFused, isFusedNow := mgr.GetDynamicRatio(nodeName)
	if !isFusedNow {
		t.Fatalf("85%% 负载下预期触发熔断 (isFused=true), 实际未触发")
	}
	if cpuRatioFused != 1.0 || memRatioFused != 1.0 {
		t.Fatalf("熔断状态下超卖比必须强制重置为 1.0, 实际为: CPU=%.2f, Mem=%.2f", cpuRatioFused, memRatioFused)
	}
	t.Logf("85%% 突发负载下成功熔断！超卖比已紧急归位为 1.0！")

	// 4. 模拟负载回落至 40% (恢复安全区)
	mgr.UpdateNodeMetrics(nodeName, 0.40, 0.40)
	cpuRatioRecover, _, isFusedRecover := mgr.GetDynamicRatio(nodeName)
	if isFusedRecover {
		t.Fatalf("负载回落后预期解除熔断")
	}
	t.Logf("负载回落后状态自愈恢复，超卖比重新平稳调节: CPU=%.2f", cpuRatioRecover)
}
