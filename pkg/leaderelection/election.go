package leaderelection

import (
	"context"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
)

// Config 选主配置
type Config struct {
	LeaseLockName      string
	LeaseLockNamespace string
	Identity           string // 当前 Pod 唯一标识 (通常取 Pod Name)
	LeaseDuration      time.Duration
	RenewDeadline      time.Duration
	RetryPeriod        time.Duration
}

// DefaultConfig 默认企业级 Lease 选主配置
func DefaultConfig(namespace string) Config {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "matrix-governor-instance"
	}
	if namespace == "" {
		namespace = "matrix-system"
	}
	return Config{
		LeaseLockName:      "matrix-governor-leader-lease",
		LeaseLockNamespace: namespace,
		Identity:           hostname,
		LeaseDuration:      15 * time.Second,
		RenewDeadline:      10 * time.Second,
		RetryPeriod:        2 * time.Second,
	}
}

// RunLeaderElection 启动基于 K8s Lease 的分布式选主循环
func RunLeaderElection(ctx context.Context, kubeClient kubernetes.Interface, cfg Config, onStartedLeading func(ctx context.Context), onStoppedLeading func()) error {
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.LeaseLockName,
			Namespace: cfg.LeaseLockNamespace,
		},
		Client: kubeClient.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: cfg.Identity,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   cfg.LeaseDuration,
		RenewDeadline:   cfg.RenewDeadline,
		RetryPeriod:     cfg.RetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				klog.Infof("[LeaderElection] 当前实例 (%s) 成功当选为 Leader！启动后台核心治理与驱逐巡检任务！", cfg.Identity)
				if onStartedLeading != nil {
					onStartedLeading(ctx)
				}
			},
			OnStoppedLeading: func() {
				klog.Warningf("[LeaderElection] 当前实例 (%s) 失去 Leader 锁，停止后台核心治理任务！", cfg.Identity)
				if onStoppedLeading != nil {
					onStoppedLeading()
				}
			},
			OnNewLeader: func(identity string) {
				if identity == cfg.Identity {
					return
				}
				klog.Infof("[LeaderElection] 集群选举产生新的 Leader 实例: %s", identity)
			},
		},
	})

	return nil
}
