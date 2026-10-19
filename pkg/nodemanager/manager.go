package nodemanager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mritd/goadmission/pkg/cgroup"
	"github.com/mritd/goadmission/pkg/finops"
	"github.com/mritd/goadmission/pkg/metrics"
	"github.com/mritd/goadmission/pkg/pid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// NodeHealthState 节点过载健康状态
type NodeHealthState string

const (
	StateNormal   NodeHealthState = "Normal"   // 正常状态 (<65%): PID 正常调节超卖比
	StateWarning  NodeHealthState = "Warning"  // 警戒状态 (65%~75%): 冻结超卖比，禁止进一步超配
	StateCritical NodeHealthState = "Critical" // 危险状态 (75%~80%): 快速压制超卖比
	StateFused    NodeHealthState = "Fused"    // 熔断状态 (>80%): 强制将超卖比置为 1.0，并触发差异化驱逐保护
)

// NodeStatusInfo 节点运行时的动态健康与超卖快照
type NodeStatusInfo struct {
	NodeName        string
	CurrentCPUUtil  float64
	CurrentMemUtil  float64
	DynamicCPURatio float64
	DynamicMemRatio float64
	HealthState     NodeHealthState
	LastUpdateTime  time.Time
	FusedUntil      time.Time
	PIDController   *pid.Controller
}

// Manager 节点动态超卖比与安全保护协调管理器
type Manager struct {
	mu            sync.RWMutex
	nodes         map[string]*NodeStatusInfo
	collector     metrics.Collector
	pidConfig     pid.Config
	fuseThreshold float64 // 熔断利用率阈值 (默认 0.80，即 80%)
	fuseCooldown  time.Duration // 熔断冷却保持时间 (默认 45 秒)
	suppressor    *cgroup.Suppressor
	stopCh        chan struct{}
	kubeClient    kubernetes.Interface
}

// GlobalManager 全局单例管理器
var (
	globalManager *Manager
	once          sync.Once
)

// GetGlobalManager 获取全局单例管理器
func GetGlobalManager() *Manager {
	once.Do(func() {
		globalManager = NewManager(metrics.NewMockCollector(), pid.DefaultConfig(), 0.80)
	})
	return globalManager
}

// NewManager 创建一个 NodeManager
func NewManager(collector metrics.Collector, pidCfg pid.Config, fuseThreshold float64) *Manager {
	if fuseThreshold <= 0 {
		fuseThreshold = 0.80 // 默认 80% 安全熔断线
	}
	return &Manager{
		nodes:         make(map[string]*NodeStatusInfo),
		collector:     collector,
		pidConfig:     pidCfg,
		fuseThreshold: fuseThreshold,
		fuseCooldown:  45 * time.Second,
		suppressor:    cgroup.NewSuppressor(0.20),
		stopCh:        make(chan struct{}),
	}
}

// RegisterNode 注册或初始化一个需要超卖管控的节点
func (m *Manager) RegisterNode(nodeName string) *NodeStatusInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	if info, exists := m.nodes[nodeName]; exists {
		return info
	}

	info := &NodeStatusInfo{
		NodeName:        nodeName,
		CurrentCPUUtil:  0.30,
		CurrentMemUtil:  0.30,
		DynamicCPURatio: m.pidConfig.MaxRatio,
		DynamicMemRatio: 1.50,
		HealthState:     StateNormal,
		LastUpdateTime:  time.Now(),
		PIDController:   pid.NewController(m.pidConfig),
	}
	m.nodes[nodeName] = info
	klog.Infof("[Matrix-Governor.NodeManager] 成功注册纳管节点: %s (初始超卖比: %.2f)", nodeName, info.DynamicCPURatio)
	return info
}

// GetDynamicRatio 供 Webhook 拦截器调用：获取指定节点当前的 CPU 与内存动态超卖比
// 返回 (cpuRatio, memRatio, isFused)
func (m *Manager) GetDynamicRatio(nodeName string) (float64, float64, bool) {
	m.mu.RLock()
	info, exists := m.nodes[nodeName]
	m.mu.RUnlock()

	if !exists {
		// 未显式注册时，先自动以默认配置纳管
		info = m.RegisterNode(nodeName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if info.HealthState == StateFused || time.Now().Before(info.FusedUntil) {
		// 熔断状态或冷却保护期下，强制不超卖 (Ratio = 1.0)
		return 1.0, 1.0, true
	}
	return info.DynamicCPURatio, info.DynamicMemRatio, false
}

// GetKubeClient 获取内部已绑定的 K8s API 客户端
func (m *Manager) GetKubeClient() kubernetes.Interface {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.kubeClient
}

// patchNodeAllocatableImmediate 触发硬熔断时立即通过 K8s API 将 Allocatable 强行刷新回物理 Capacity (零延迟回退)
func (m *Manager) patchNodeAllocatableImmediate(nodeName string) {
	if m.kubeClient == nil {
		return
	}
	go func() {
		node, err := m.kubeClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("[Matrix-Governor] 熔断立即回退获取节点 %s 失败: %v", nodeName, err)
			return
		}
		patchData := []byte(fmt.Sprintf(`{"status":{"allocatable":{"cpu":"%s","memory":"%s"}}}`,
			node.Status.Capacity.Cpu().String(),
			node.Status.Capacity.Memory().String()))
		_, err = m.kubeClient.CoreV1().Nodes().PatchStatus(context.Background(), nodeName, patchData)
		if err != nil {
			klog.Errorf("[Matrix-Governor] 熔断即时 PatchStatus 失败: %v", err)
		} else {
			klog.Warningf("[Matrix-Governor.Safety] 安全底线硬熔断触发成功: 已通过 API 零延迟将节点 %s Allocatable 强制降级回原生物理极限 [CPU: %s, Mem: %s]！",
				nodeName, node.Status.Capacity.Cpu().String(), node.Status.Capacity.Memory().String())
		}
	}()
}

// UpdateNodeMetrics 手动或周期性更新节点的物理指标并驱动 PID 迭代
func (m *Manager) UpdateNodeMetrics(nodeName string, cpuUsage, memUsage float64) *NodeStatusInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, exists := m.nodes[nodeName]
	if !exists {
		info = &NodeStatusInfo{
			NodeName:        nodeName,
			PIDController:   pid.NewController(m.pidConfig),
		}
		m.nodes[nodeName] = info
	}

	info.CurrentCPUUtil = cpuUsage
	info.CurrentMemUtil = memUsage
	info.LastUpdateTime = time.Now()

	// 状态机流转与安全熔断判断
	inCooldown := time.Now().Before(info.FusedUntil)

	if cpuUsage >= m.fuseThreshold || memUsage >= m.fuseThreshold {
		// 1. 突破 80% 触发紧急硬熔断
		info.HealthState = StateFused
		info.DynamicCPURatio = 1.0
		info.DynamicMemRatio = 1.0
		info.FusedUntil = time.Now().Add(m.fuseCooldown)
		info.PIDController.Reset()
		klog.Warningf("[Matrix-Governor.NodeManager] 警告: 节点 %s 物理利用率达到极限红线 (CPU:%.2f%%, Mem:%.2f%%)！触发安全熔断！进入 %v 保护期，超卖比强制重置为 1.0",
			nodeName, cpuUsage*100, memUsage*100, m.fuseCooldown)
		m.patchNodeAllocatableImmediate(nodeName)
	} else if inCooldown {
		// 2. 处于熔断冷却保持期内，继续锁死 1.0 底线，避免抖动回弹
		info.HealthState = StateFused
		info.DynamicCPURatio = 1.0
		info.DynamicMemRatio = 1.0
		klog.Infof("[Matrix-Governor.NodeManager] 节点 %s 处于安全熔断保持期 (剩余 %.0f 秒)，继续保持 1.0 原生容量",
			nodeName, time.Until(info.FusedUntil).Seconds())
	} else if cpuUsage >= 0.75 || memUsage >= 0.75 {
		// 3. 危险区 (75%~80%)：快速压制超卖比
		info.HealthState = StateCritical
		info.DynamicCPURatio = info.PIDController.Update(cpuUsage)
		info.DynamicMemRatio = m.pidConfig.MinRatio
	} else if cpuUsage >= 0.65 || memUsage >= 0.65 {
		// 4. 警戒区 (65%~75%)：保持当前比率，不继续抬升
		info.HealthState = StateWarning
		info.DynamicCPURatio = info.PIDController.Update(cpuUsage)
		info.DynamicMemRatio = info.PIDController.GetCurrentRatio()
	} else {
		// 5. 正常安全区 (<65%)：由 PID 自适应探索最优超卖比
		info.HealthState = StateNormal
		info.DynamicCPURatio = info.PIDController.Update(cpuUsage)
		// 内存超卖需更为保守，设定为 CPU 超卖比增量的一半
		memRatioDelta := (info.DynamicCPURatio - 1.0) * 0.5
		info.DynamicMemRatio = 1.0 + memRatioDelta
	}

	var stateCode float64 = 0
	switch info.HealthState {
	case StateWarning:
		stateCode = 1
	case StateCritical:
		stateCode = 2
	case StateFused:
		stateCode = 3
	}
	metrics.RecordNodeStatus(nodeName, info.DynamicCPURatio, info.DynamicMemRatio, cpuUsage, memUsage, stateCode)

	// 联动 FinOps 财务核算引擎，实时计算超卖核数与节约金额
	finops.GetGlobalEngine().RecordNodeOversold(nodeName, 4.0, 4.0*info.DynamicCPURatio, 7.3, 7.3*info.DynamicMemRatio)

	// 驱动单机 Cgroup 毫秒级微压制，在驱逐前提供无损缓冲
	if m.suppressor != nil {
		m.suppressor.ReconcileByWatermark(cpuUsage)
	}

	return info
}

// StartAutoSync 启动后台定时同步轮询任务
func (m *Manager) StartAutoSync(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				m.syncAllNodes()
			case <-m.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// Stop 停止后台任务
func (m *Manager) Stop() {
	close(m.stopCh)
}

// syncAllNodes 遍历所有纳管节点同步指标并计算 PID
func (m *Manager) syncAllNodes() {
	m.mu.RLock()
	nodeNames := make([]string, 0, len(m.nodes))
	for name := range m.nodes {
		nodeNames = append(nodeNames, name)
	}
	m.mu.RUnlock()

	for _, name := range nodeNames {
		m.mu.RLock()
		info := m.nodes[name]
		m.mu.RUnlock()

		// 生产级防覆盖保护：若节点处于熔断冷却期，或在过去 20 秒内接收过实时上报，则跳过 Mock 轮询覆盖
		if info != nil {
			if time.Now().Before(info.FusedUntil) {
				continue
			}
			if time.Since(info.LastUpdateTime) < 20*time.Second {
				continue
			}
		}

		metricsData, err := m.collector.GetNodeMetrics(name)
		if err != nil {
			klog.Errorf("[Matrix-Governor] 获取节点 %s 指标失败: %v", name, err)
			continue
		}
		m.UpdateNodeMetrics(name, metricsData.CPUUsageRatio, metricsData.MemUsageRatio)
	}
}

// AutoDiscoverNodes 自动发现集群内所有 Node 并注册纳管
func (m *Manager) AutoDiscoverNodes(kubeClient kubernetes.Interface) {
	if kubeClient == nil {
		return
	}
	m.mu.Lock()
	m.kubeClient = kubeClient
	m.mu.Unlock()

	nodes, err := kubeClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		klog.Errorf("[Matrix-Governor] 自动发现集群节点失败: %v", err)
		return
	}
	for _, n := range nodes.Items {
		m.RegisterNode(n.Name)
	}
	// 立即触发一次全节点指标与 Prometheus 同步
	m.syncAllNodes()
}

