package protector

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestAutoPDBController_EnsurePDB(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	ctrl := NewAutoPDBController(fakeClient)

	ctx := context.TODO()
	twoReplicas := int32(2)
	oneReplica := int32(1)

	// 1. 创建待纳管的 2 副本 Deployment (无 PDB)
	deploy1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web-frontend", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &twoReplicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "web-frontend"},
			},
		},
	}
	// 2. 创建单副本应用 (不应注入)
	deploy2 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "singleton-job", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &oneReplica,
		},
	}

	_, _ = fakeClient.AppsV1().Deployments("default").Create(ctx, deploy1, metav1.CreateOptions{})
	_, _ = fakeClient.AppsV1().Deployments("default").Create(ctx, deploy2, metav1.CreateOptions{})

	// 执行巡检注入
	err := ctrl.EnsurePDBForDeployments(ctx, "default")
	if err != nil {
		t.Fatalf("EnsurePDBForDeployments 报错: %v", err)
	}

	// 验证结果
	pdbList, err := fakeClient.PolicyV1beta1().PodDisruptionBudgets("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("查询 PDB 失败: %v", err)
	}

	if len(pdbList.Items) != 1 {
		t.Fatalf("预期只为 web-frontend 创建 1 个 PDB, 实际创建数: %d", len(pdbList.Items))
	}

	createdPDB := pdbList.Items[0]
	if createdPDB.Name != "web-frontend-auto-pdb" {
		t.Fatalf("预期 PDB 名称为 web-frontend-auto-pdb, 实际为: %s", createdPDB.Name)
	}
	t.Logf("成功验证 Auto-PDB 自动注入: %s (MaxUnavailable: %v)", createdPDB.Name, createdPDB.Spec.MaxUnavailable.String())
}
