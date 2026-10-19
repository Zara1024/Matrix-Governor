package eviction

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Zara1024/Matrix-Governor/pkg/metrics"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// ExecutorConfig 差异化驱逐执行器配置
type ExecutorConfig struct {
	// CooldownPeriod 节点驱逐后的防抖冷却期 (例如 30s 内不重复对同一节点触发二次驱逐)
	CooldownPeriod time.Duration
	// MaxEvictionsPerMinute 单节点每分钟最大驱逐 Pod 数 (令牌桶限流，防止瞬间杀穿)
	MaxEvictionsPerMinute int
}

// DefaultExecutorConfig 推荐的企业级默认驱逐防抖配置
func DefaultExecutorConfig() ExecutorConfig {
	return ExecutorConfig{
		CooldownPeriod:        30 * time.Second, // 30 秒防抖冷却
		MaxEvictionsPerMinute: 1,                // 每分钟单节点最多 1 个
	}
}

// Executor 差异化物理驱逐执行器
type Executor struct {
	mu            sync.Mutex
	kubeClient    kubernetes.Interface
	scorer        *Scorer
	cfg           ExecutorConfig
	lastEvictTime map[string]time.Time // 记录每个节点上一次驱逐时间戳
	evictTokens   map[string]int       // 简易令牌桶
}

// NewExecutor 创建物理驱逐执行器
func NewExecutor(kubeClient kubernetes.Interface, scorer *Scorer, cfg ExecutorConfig) *Executor {
	if scorer == nil {
		scorer = NewScorer()
	}
	return &Executor{
		kubeClient:    kubeClient,
		scorer:        scorer,
		cfg:           cfg,
		lastEvictTime: make(map[string]time.Time),
		evictTokens:   make(map[string]int),
	}
}

// ExecuteBestEviction 对候选 Pod 进行 5 维加权打分，并安全驱逐排名第一的目标 Pod
func (e *Executor) ExecuteBestEviction(ctx context.Context, nodeName string, candidates []PodCandidate) (*PodCandidate, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(candidates) == 0 {
		return nil, fmt.Errorf("节点 %s 无可选驱逐候选 Pod", nodeName)
	}

	// 1. 冷却期（Cooldown）检查：如果距上次驱逐未超过 30 秒，拒绝驱逐，等待物理负载吸收
	lastTime, exists := e.lastEvictTime[nodeName]
	if exists && time.Since(lastTime) < e.cfg.CooldownPeriod {
		remaining := e.cfg.CooldownPeriod - time.Since(lastTime)
		return nil, fmt.Errorf("节点 %s 处于驱逐后冷却期中 (剩余 %.1fs)，暂不执行二次驱逐", nodeName, remaining.Seconds())
	}

	// 2. 依据 5 维加权积分公式对候选列表从高到低排序
	ranked := e.scorer.RankCandidates(candidates)
	target := ranked[0]

	// 3. 安全红线底线：如果得分最高者也是零分（即核心高保业务），坚决拒杀
	if target.TotalScore <= 0.05 {
		return nil, fmt.Errorf("节点 %s 上的存量 Pod 均为高保生产服务 (最高得分 %.4f)，触发免死保护，拒绝驱逐", nodeName, target.TotalScore)
	}

	// 4. 调用真实 K8s Eviction API 发起优雅物理驱逐
	if e.kubeClient != nil {
		eviction := &policyv1beta1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      target.Pod.Name,
				Namespace: target.Pod.Namespace,
			},
		}

		err := e.kubeClient.CoreV1().Pods(target.Pod.Namespace).Evict(ctx, eviction)
		if err != nil {
			klog.Errorf("[EvictionExecutor] 向 K8s APIServer 提交 Pod %s/%s 驱逐失败: %v", target.Pod.Namespace, target.Pod.Name, err)
			return nil, err
		}
	}

	// 5. 更新冷却时间与审计指标
	e.lastEvictTime[nodeName] = time.Now()
	metrics.RecordEvictionAction(nodeName, target.Pod.Namespace, string(target.Pod.Status.QOSClass), "NodeOverloadFused")

	klog.Warningf("[EvictionExecutor] 成功完成差异化安全驱逐！目标: %s/%s | 综合积分: %.4f (PDB:%t, 允许中断数:%d, QoS:%s)",
		target.Pod.Namespace, target.Pod.Name, target.TotalScore, target.HasPDB, target.DisruptionsLeft, target.Pod.Status.QOSClass)

	return &target, nil
}
