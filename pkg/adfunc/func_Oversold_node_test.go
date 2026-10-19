package adfunc

import (
	"encoding/json"
	"testing"

	"github.com/mritd/goadmission/pkg/nodemanager"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestOversoldWebhook_MutatingPatch(t *testing.T) {
	nodeName := "worker-test-01"

	// 1. 设置节点在 NodeManager 中的动态超卖比为 1.5 倍
	mgr := nodemanager.GetGlobalManager()
	mgr.RegisterNode(nodeName)
	// 调整利用率为 30%，使超卖比放大
	mgr.UpdateNodeMetrics(nodeName, 0.30, 0.30)

	// 2. 构造 Node 对象 (8核 16Gi)
	nodeObj := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				"oversold": "true",
			},
		},
		Status: v1.NodeStatus{
			Capacity: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("8"),
				v1.ResourceMemory: resource.MustParse("16Gi"),
				v1.ResourcePods:   resource.MustParse("110"),
			},
			Allocatable: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("8"),
				v1.ResourceMemory: resource.MustParse("16Gi"),
				v1.ResourcePods:   resource.MustParse("110"),
			},
		},
	}

	rawNode, err := json.Marshal(nodeObj)
	if err != nil {
		t.Fatalf("序列化 Node 失败: %v", err)
	}

	// 3. 构造 AdmissionRequest
	req := &admissionv1.AdmissionRequest{
		Name: nodeName,
		Kind: metav1.GroupVersionKind{
			Group:   "",
			Version: "v1",
			Kind:    "Node",
		},
		Object: runtime.RawExtension{
			Raw: rawNode,
		},
	}

	// 4. 调用 oversold Webhook 逻辑
	resp, err := oversold(req)
	if err != nil {
		t.Fatalf("Webhook 执行报错: %v", err)
	}

	if !resp.Allowed {
		t.Fatalf("预期准入通过 Allowed=true, 实际为: false, 消息: %v", resp.Result.Message)
	}

	if len(resp.Patch) == 0 {
		t.Fatalf("预期返回生成的 JSONPatch, 实际为空")
	}

	t.Logf("生成的 JSONPatch 内容: %s", string(resp.Patch))

	var patches []Patch
	if err := json.Unmarshal(resp.Patch, &patches); err != nil {
		t.Fatalf("解析 Patch 失败: %v", err)
	}

	if len(patches) < 2 {
		t.Fatalf("预期至少包含 CPU 与 Memory 2 个 Patch 项, 实际为: %d", len(patches))
	}

	for _, p := range patches {
		t.Logf("Patch 路径: %s -> 替换值: %v", p.Path, p.Value)
	}
}
