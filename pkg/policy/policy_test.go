package policy

import (
	"testing"
)

func TestPolicyManager_MatchPolicy(t *testing.T) {
	mgr := NewPolicyManager()

	// 添加离线大数据节点池策略 (允许超卖 3 倍)
	batchPolicy := &OversoldPolicy{
		Name: "batch-node-policy",
		NodeSelector: map[string]string{
			"node-role.kubernetes.io/batch": "true",
		},
		TargetUtilization: 0.75,
		MaxCpuRatio:       3.0,
	}
	// 添加核心支付节点池策略 (最高 1.2 倍)
	corePolicy := &OversoldPolicy{
		Name: "core-payment-policy",
		NodeSelector: map[string]string{
			"node-tier": "core-payment",
		},
		TargetUtilization: 0.50,
		MaxCpuRatio:       1.2,
	}

	mgr.AddPolicy(batchPolicy)
	mgr.AddPolicy(corePolicy)

	// 1. 匹配大数据节点
	node1 := map[string]string{"node-role.kubernetes.io/batch": "true", "zone": "az-1"}
	matched1 := mgr.MatchPolicyForNode(node1)
	if matched1.Name != "batch-node-policy" || matched1.MaxCpuRatio != 3.0 {
		t.Fatalf("预期匹配 batch-node-policy, 实际为: %s", matched1.Name)
	}
	t.Logf("成功匹配大数据节点池策略: %s (超卖上限: %.1f)", matched1.Name, matched1.MaxCpuRatio)

	// 2. 匹配核心支付节点
	node2 := map[string]string{"node-tier": "core-payment"}
	matched2 := mgr.MatchPolicyForNode(node2)
	if matched2.Name != "core-payment-policy" || matched2.MaxCpuRatio != 1.2 {
		t.Fatalf("预期匹配 core-payment-policy, 实际为: %s", matched2.Name)
	}
	t.Logf("成功匹配核心支付节点池策略: %s (超卖上限: %.1f)", matched2.Name, matched2.MaxCpuRatio)

	// 3. 匹配普通无特殊标节点 (命中全局默认)
	node3 := map[string]string{"kubernetes.io/hostname": "worker-common"}
	matched3 := mgr.MatchPolicyForNode(node3)
	if matched3.Name != "global-default-policy" {
		t.Fatalf("预期匹配 global-default-policy, 实际为: %s", matched3.Name)
	}
	t.Logf("未打标节点成功回退至默认全局策略: %s", matched3.Name)
}
