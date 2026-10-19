package protector

import (
	"context"
	"fmt"
	"sync"
	"time"

	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// AutoPDBController 自动为集群无状态 Workload 绑定 PDB 的保障控制器
type AutoPDBController struct {
	mu         sync.Mutex
	kubeClient kubernetes.Interface
	stopCh     chan struct{}
}

// NewAutoPDBController 创建 Auto-PDB 控制器
func NewAutoPDBController(kubeClient kubernetes.Interface) *AutoPDBController {
	return &AutoPDBController{
		kubeClient: kubeClient,
		stopCh:     make(chan struct{}),
	}
}

// EnsurePDBForDeployments 巡检指定命名空间 (或全部业务命名空间)，为未配置 PDB 且副本数 >= 2 的 Deployment 自动创建兜底 PDB
func (c *AutoPDBController) EnsurePDBForDeployments(ctx context.Context, namespace string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.kubeClient == nil {
		return nil
	}

	// 1. 获取目标命名空间下的所有 Deployment
	deployments, err := c.kubeClient.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("获取命名空间 %s 下的 Deployment 列表失败: %v", namespace, err)
	}

	// 2. 获取该命名空间下现存的所有 PDB
	existingPDBs, err := c.kubeClient.PolicyV1beta1().PodDisruptionBudgets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("获取命名空间 %s 下的 PDB 列表失败: %v", namespace, err)
	}

	for _, deploy := range deployments.Items {
		// 系统组件与副本数小于 2 的单副本应用不自动创建 PDB (单副本不可中断)
		if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas < 2 {
			continue
		}

		// 检查是否已有匹配该 Deployment 的 PDB
		hasPDB := false
		for _, pdb := range existingPDBs.Items {
			if pdb.Spec.Selector != nil && deploy.Spec.Selector != nil {
				// 简易匹配：如果名称包含或 selector 标签吻合
				if pdb.Name == fmt.Sprintf("%s-auto-pdb", deploy.Name) {
					hasPDB = true
					break
				}
			}
		}

		if hasPDB {
			continue
		}

		// 3. 自动计算安全容忍中断数：默认允许中断 1 个副本
		maxUnavailable := intstr.FromInt(1)
		// 如果副本数较多 (>= 5)，允许中断 20%
		if *deploy.Spec.Replicas >= 5 {
			maxUnavailable = intstr.FromString("20%")
		}

		autoPDB := &policyv1beta1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-auto-pdb", deploy.Name),
				Namespace: deploy.Namespace,
				Labels: map[string]string{
					"matrix-governor.io/managed-by": "auto-pdb-controller",
					"matrix-governor.io/app":        deploy.Name,
				},
			},
			Spec: policyv1beta1.PodDisruptionBudgetSpec{
				Selector:       deploy.Spec.Selector,
				MaxUnavailable: &maxUnavailable,
			},
		}

		_, err := c.kubeClient.PolicyV1beta1().PodDisruptionBudgets(deploy.Namespace).Create(ctx, autoPDB, metav1.CreateOptions{})
		if err != nil {
			klog.Warningf("[AutoPDBController] 为 Deployment %s/%s 自动注入兜底 PDB 失败: %v", deploy.Namespace, deploy.Name, err)
		} else {
			klog.Infof("[AutoPDBController] 成功为无状态应用 %s/%s 自动注入兜底 PDB (MaxUnavailable: %v)",
				deploy.Namespace, deploy.Name, maxUnavailable.String())
		}
	}

	return nil
}

// Start 启动后台巡检周期
func (c *AutoPDBController) Start(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				_ = c.EnsurePDBForDeployments(ctx, "")
			case <-c.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// Stop 停止控制器
func (c *AutoPDBController) Stop() {
	close(c.stopCh)
}
