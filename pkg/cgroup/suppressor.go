package cgroup

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// SuppressState 压制状态枚举
type SuppressState string

const (
	StateUnsuppressed SuppressState = "Unsuppressed" // 正常运行，不压制
	StateThrottled    SuppressState = "Throttled"    // 警戒区 (75%~80%)：CPU Quota 压制为 20%
	StateFreezed      SuppressState = "Freezed"      // 熔断区 (>80%)：极度限制或冻结
)

// PodCgroupInfo 记录受控 Pod 的 Cgroup 限额与原始配置
type PodCgroupInfo struct {
	PodName       string
	Namespace     string
	QoSClass      string
	OriginalQuota int64 // 原始 CFS Quota
	CurrentQuota  int64 // 当前生效 CFS Quota
	PeriodUs      int64 // CFS Period (默认 100000us)
	State         SuppressState
	LastAction    time.Time
}

// Suppressor 单机 Cgroup 动态微压制引擎
type Suppressor struct {
	mu           sync.RWMutex
	trackedPods  map[string]*PodCgroupInfo
	watermark    float64
	suppressRatio float64 // 压制比例 (默认 0.20，即压制至 20%)
}

// NewSuppressor 创建单机 Cgroup 压制引擎
func NewSuppressor(suppressRatio float64) *Suppressor {
	if suppressRatio <= 0 || suppressRatio > 1.0 {
		suppressRatio = 0.20
	}
	return &Suppressor{
		trackedPods:   make(map[string]*PodCgroupInfo),
		suppressRatio: suppressRatio,
	}
}

// RegisterPod 纳管一个低优任务 Pod
func (s *Suppressor) RegisterPod(name, namespace, qos string, defaultQuota, period int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	s.trackedPods[key] = &PodCgroupInfo{
		PodName:       name,
		Namespace:     namespace,
		QoSClass:      qos,
		OriginalQuota: defaultQuota,
		CurrentQuota:  defaultQuota,
		PeriodUs:      period,
		State:         StateUnsuppressed,
		LastAction:    time.Now(),
	}
}

// ReconcileByWatermark 根据节点当前物理水位执行无损微压制或恢复
func (s *Suppressor) ReconcileByWatermark(nodeCPUUsage float64) map[string]SuppressState {
	s.mu.Lock()
	defer s.mu.Unlock()

	results := make(map[string]SuppressState)

	for key, pod := range s.trackedPods {
		// 核心在线保障：Guaranteed 高保业务绝对免压制
		if pod.QoSClass == "Guaranteed" {
			continue
		}

		if nodeCPUUsage >= 0.75 {
			// 1. 突破 75% 警戒水位：触发 CFS Quota 快速压制
			if pod.State != StateThrottled {
				suppressedQuota := int64(float64(pod.OriginalQuota) * s.suppressRatio)
				if suppressedQuota < 5000 {
					suppressedQuota = 5000 // 最小保底 5ms/100ms
				}
				pod.CurrentQuota = suppressedQuota
				pod.State = StateThrottled
				pod.LastAction = time.Now()
				klog.Warningf("[CgroupSuppressor] 节点利用率达到 %.1f%%，对离线 Pod %s 执行毫秒级 Cgroup CFS 压制 (Quota: %d -> %d)",
					nodeCPUUsage*100, key, pod.OriginalQuota, suppressedQuota)
			}
		} else if nodeCPUUsage < 0.65 {
			// 2. 利用率回落至 65% 安全区以下：平滑解除压制，恢复原有算力
			if pod.State != StateUnsuppressed {
				pod.CurrentQuota = pod.OriginalQuota
				pod.State = StateUnsuppressed
				pod.LastAction = time.Now()
				klog.Infof("[CgroupSuppressor] 节点利用率平稳回落至 %.1f%%，已解除离线 Pod %s 的 Cgroup 压制，配额恢复为 %d",
					nodeCPUUsage*100, key, pod.OriginalQuota)
			}
		}

		results[key] = pod.State
	}

	return results
}

// GetPodState 查询 Pod 的当前配额与压制状态
func (s *Suppressor) GetPodState(namespace, name string) (int64, SuppressState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	pod, exists := s.trackedPods[key]
	if !exists {
		return 0, StateUnsuppressed, false
	}
	return pod.CurrentQuota, pod.State, true
}
