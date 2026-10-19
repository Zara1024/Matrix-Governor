package policy

import (
	"sync"
)

// OversoldPolicy 针对不同节点池的声明式超卖治理策略定义
type OversoldPolicy struct {
	Name                string            `json:"name"`
	NodeSelector        map[string]string `json:"nodeSelector"`        // 匹配目标节点池的 Label 选择器
	TargetUtilization   float64           `json:"targetUtilization"`   // 目标安全利用率 (例如 0.65)
	FuseThreshold       float64           `json:"fuseThreshold"`       // 熔断触发利用率阈值 (例如 0.80)
	MinRatio            float64           `json:"minRatio"`            // 最小超卖比 (通常为 1.0)
	MaxCpuRatio         float64           `json:"maxCpuRatio"`         // CPU 最大超卖上限
	MaxMemRatio         float64           `json:"maxMemRatio"`         // 内存最大超卖上限
	Kp                  float64           `json:"kp"`                  // PID 比例增益
	Ki                  float64           `json:"ki"`                  // PID 积分增益
	Kd                  float64           `json:"kd"`                  // PID 微分增益
	ProtectedNamespaces []string          `json:"protectedNamespaces"` // 免除超卖的命名空间白名单
}

// DefaultPolicy 默认全局基线策略
func DefaultPolicy() *OversoldPolicy {
	return &OversoldPolicy{
		Name:              "global-default-policy",
		NodeSelector:      map[string]string{},
		TargetUtilization: 0.65,
		FuseThreshold:     0.80,
		MinRatio:          1.0,
		MaxCpuRatio:       2.0,
		MaxMemRatio:       1.5,
		Kp:                0.8,
		Ki:                0.15,
		Kd:                0.08,
		ProtectedNamespaces: []string{
			"kube-system",
			"monitoring",
		},
	}
}

// Manager 声明式策略管理器
type Manager struct {
	mu            sync.RWMutex
	policies      []*OversoldPolicy
	defaultPolicy *OversoldPolicy
}

// NewPolicyManager 创建策略管理器
func NewPolicyManager() *Manager {
	return &Manager{
		policies:      make([]*OversoldPolicy, 0),
		defaultPolicy: DefaultPolicy(),
	}
}

// AddPolicy 添加自定义节点池策略
func (m *Manager) AddPolicy(policy *OversoldPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policies = append(m.policies, policy)
}

// MatchPolicyForNode 根据节点的 Labels 匹配最合适的一套超卖治理策略
func (m *Manager) MatchPolicyForNode(nodeLabels map[string]string) *OversoldPolicy {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, policy := range m.policies {
		if len(policy.NodeSelector) == 0 {
			continue
		}
		matched := true
		for k, v := range policy.NodeSelector {
			if nodeLabels[k] != v {
				matched = false
				break
			}
		}
		if matched {
			return policy
		}
	}

	// 无匹配时返回默认基准策略
	return m.defaultPolicy
}
