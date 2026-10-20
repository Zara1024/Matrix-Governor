package cgroup

import (
	"fmt"
	"sync"
	"time"

	"github.com/Zara1024/Matrix-Governor/pkg/kernel"
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
	CgroupRelDir  string // 相对于 cgroup 根的路径；为空表示只在内存记账、不落盘
	OriginalQuota int64  // 原始 CFS Quota（来自内核实际值，不是简历配置）
	CurrentQuota  int64  // 当前生效 CFS Quota
	PeriodUs      int64  // CFS Period (默认 100000us)
	State         SuppressState
	LastAction    time.Time
	LastWriteErr  string
}

// Suppressor 单机 Cgroup 动态微压制引擎
//
// 两种工作模式：
//   - 无 executor（NewSuppressor）：只做内存记账，用于单元测试与降级运行
//   - 有 executor（NewSuppressorWithExecutor）：每次状态变更真的写进内核 cgroup 文件，
//     写失败不改账本——账本必须反映内核真实状态，否则下一轮判断就是基于错误前提
type Suppressor struct {
	mu            sync.RWMutex
	trackedPods   map[string]*PodCgroupInfo
	watermark     float64
	suppressRatio float64 // 压制比例 (默认 0.20，即压制至 20%)
	exec          *kernel.Executor
	lastErr       error
}

// NewSuppressor 创建内存记账模式的压制引擎（不落盘）。
func NewSuppressor(suppressRatio float64) *Suppressor {
	return newSuppressor(suppressRatio, nil)
}

// NewSuppressorWithExecutor 创建会真实写入内核的压制引擎。
func NewSuppressorWithExecutor(suppressRatio float64, exec *kernel.Executor) *Suppressor {
	return newSuppressor(suppressRatio, exec)
}

func newSuppressor(suppressRatio float64, exec *kernel.Executor) *Suppressor {
	if suppressRatio <= 0 || suppressRatio > 1.0 {
		suppressRatio = 0.20
	}
	return &Suppressor{
		trackedPods:   make(map[string]*PodCgroupInfo),
		suppressRatio: suppressRatio,
		exec:          exec,
	}
}

func podKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// RegisterPod 纳管一个低优任务 Pod（仅记账，不落盘）。
func (s *Suppressor) RegisterPod(name, namespace, qos string, defaultQuota, period int64) {
	s.RegisterPodCgroup(name, namespace, qos, defaultQuota, period, "")
}

// RegisterPodCgroup 纳管一个 Pod 并记录其 cgroup 路径，后续压制会真实写进该目录。
func (s *Suppressor) RegisterPodCgroup(name, namespace, qos string, defaultQuota, period int64, relDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if period <= 0 {
		period = kernel.DefaultPeriod
	}
	key := podKey(namespace, name)
	s.trackedPods[key] = &PodCgroupInfo{
		PodName:       name,
		Namespace:     namespace,
		QoSClass:      qos,
		CgroupRelDir:  relDir,
		OriginalQuota: defaultQuota,
		CurrentQuota:  defaultQuota,
		PeriodUs:      period,
		State:         StateUnsuppressed,
		LastAction:    time.Now(),
	}
}

// ReconcileByWatermark 根据节点当前物理水位执行无损微压制或恢复。
// 返回值是每个 Pod 在本轮结束后的状态。
func (s *Suppressor) ReconcileByWatermark(nodeCPUUsage float64) map[string]SuppressState {
	s.mu.Lock()
	defer s.mu.Unlock()

	results := make(map[string]SuppressState, len(s.trackedPods))

	for key, pod := range s.trackedPods {
		// 核心在线保障：Guaranteed 高保业务绝对免压制
		if pod.QoSClass == kernel.QoSGuaranteed {
			results[key] = pod.State
			continue
		}

		switch {
		case nodeCPUUsage >= 0.75:
			// 突破 75% 警戒水位：触发 CFS Quota 快速压制
			if pod.State != StateThrottled {
				suppressedQuota := int64(float64(pod.OriginalQuota) * s.suppressRatio)
				if suppressedQuota < 5000 {
					suppressedQuota = 5000 // 最小保底 5ms/100ms
				}
				if err := s.applyQuota(pod, suppressedQuota); err != nil {
					pod.LastWriteErr = err.Error()
					s.lastErr = err
					klog.Errorf("[CgroupSuppressor] 压制离线 Pod %s 失败，账本保持原值: %v", key, err)
					results[key] = pod.State
					continue
				}
				pod.CurrentQuota = suppressedQuota
				pod.State = StateThrottled
				pod.LastAction = time.Now()
				pod.LastWriteErr = ""
				klog.Warningf("[CgroupSuppressor] 节点利用率 %.1f%%，离线 Pod %s 已压制 (Quota: %d -> %d)",
					nodeCPUUsage*100, key, pod.OriginalQuota, suppressedQuota)
			}
		case nodeCPUUsage < 0.65:
			// 利用率回落至 65% 安全区以下：平滑解除压制，恢复原有算力
			if pod.State != StateUnsuppressed {
				if err := s.applyQuota(pod, pod.OriginalQuota); err != nil {
					pod.LastWriteErr = err.Error()
					s.lastErr = err
					klog.Errorf("[CgroupSuppressor] 恢复离线 Pod %s 失败: %v", key, err)
					results[key] = pod.State
					continue
				}
				pod.CurrentQuota = pod.OriginalQuota
				pod.State = StateUnsuppressed
				pod.LastAction = time.Now()
				pod.LastWriteErr = ""
				klog.Infof("[CgroupSuppressor] 节点利用率回落至 %.1f%%，已解除 Pod %s 的压制，配额恢复为 %d",
					nodeCPUUsage*100, key, pod.OriginalQuota)
			}
		}

		results[key] = pod.State
	}

	return results
}

// applyQuota 把配额真正写进内核。没有 executor 或没有 cgroup 路径时视为成功（纯记账模式）。
func (s *Suppressor) applyQuota(pod *PodCgroupInfo, quota int64) error {
	if s.exec == nil || pod.CgroupRelDir == "" {
		s.lastErr = nil
		return nil
	}
	if err := kernel.ValidateRelPath(pod.CgroupRelDir); err != nil {
		s.lastErr = err
		return err
	}
	_, err := s.exec.SetCPUQuota(pod.CgroupRelDir, quota, pod.PeriodUs)
	s.lastErr = err
	return err
}

// GetPodState 查询 Pod 的当前配额与压制状态
func (s *Suppressor) GetPodState(namespace, name string) (int64, SuppressState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pod, exists := s.trackedPods[podKey(namespace, name)]
	if !exists {
		return 0, StateUnsuppressed, false
	}
	return pod.CurrentQuota, pod.State, true
}

// Snapshot 返回所有受控 Pod 的只读快照，供指标导出使用。
func (s *Suppressor) Snapshot() []PodCgroupInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]PodCgroupInfo, 0, len(s.trackedPods))
	for _, p := range s.trackedPods {
		out = append(out, *p)
	}
	return out
}

// LastError 返回最近一次内核写入错误（成功写入后会被清空）。
func (s *Suppressor) LastError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastErr
}

// RestoreAll 把所有被压制的 Pod 恢复到原始配额。
// Agent 收到 SIGTERM 时必须调用：否则节点重启后 Pod 会一直卡在 20% 配额上，
// 而 kubelet 不会帮你改回来——它认为 cgroup 配置是自己的，只在容器重建时才重算。
func (s *Suppressor) RestoreAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	restored := 0
	for key, pod := range s.trackedPods {
		if pod.State == StateUnsuppressed {
			continue
		}
		if err := s.applyQuota(pod, pod.OriginalQuota); err != nil {
			klog.Errorf("[CgroupSuppressor] 退出前恢复 Pod %s 失败: %v", key, err)
			continue
		}
		pod.CurrentQuota = pod.OriginalQuota
		pod.State = StateUnsuppressed
		restored++
	}
	if restored > 0 {
		klog.Infof("[CgroupSuppressor] 退出前已恢复 %d 个 Pod 的原始配额", restored)
	}
	return restored
}

// Tracked 返回受控 Pod 数量。
func (s *Suppressor) Tracked() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.trackedPods)
}
