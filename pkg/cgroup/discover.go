package cgroup

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Zara1024/Matrix-Governor/pkg/kernel"
	"k8s.io/klog/v2"
)

// DiscoveredPod 是从 cgroup 树里扫出来的一个 Pod 级分组。
type DiscoveredPod struct {
	RelDir   string // 相对于 cgroup 根的路径
	PodUID   string
	QoSClass string
	Quota    int64 // 内核里真实生效的 quota，-1 表示不限
	Period   int64
}

// DiscoverPods 扫描 cgroup 树，返回所有 Pod 级分组及其真实 CPU 配额。
//
// 这是"账本与内核对齐"的入口。内存里的账本一定会失真：
//   - kubelet 重启会重建 cgroup，配额回到 PodSpec 的值
//   - Pod 重建后 UID 变了，旧记录成了幽灵
//   - 有人手工 echo 改过 cpu.max，内存里毫不知情
//
// 所以每轮压制前先扫一遍内核，比维护一份"我以为的状态"可靠。
// 目前只覆盖 cgroup v2（v1 需要按 cpu 控制器单独遍历，路径形态也不同）。
func DiscoverPods(exec *kernel.Executor) ([]DiscoveredPod, error) {
	if exec.Version() != kernel.VersionV2 {
		return nil, nil
	}

	kubepodsRoot := filepath.Join(exec.RootDir(), "kubepods.slice")
	if fi, err := os.Stat(kubepodsRoot); err != nil || !fi.IsDir() {
		// 节点上还没有任何 Pod，属于正常情况
		return nil, nil
	}

	var out []DiscoveredPod
	err := filepath.Walk(kubepodsRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// 遍历过程中 cgroup 被删除是常态，不能因此中断整轮扫描
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			return nil
		}
		name := info.Name()
		if !isPodSlice(name) {
			return nil
		}

		rel, rerr := filepath.Rel(exec.RootDir(), path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		quota, period, qerr := exec.GetCPUQuota(rel)
		if qerr != nil {
			klog.V(4).Infof("[Discover] 跳过 %s: 读取 cpu.max 失败 %v", rel, qerr)
			return nil
		}

		out = append(out, DiscoveredPod{
			RelDir:   rel,
			PodUID:   extractPodUID(name),
			QoSClass: inferQoS(rel),
			Quota:    quota,
			Period:   period,
		})
		return nil
	})
	return out, err
}

// isPodSlice 判断目录名是不是 Pod 级 slice。
// 形态有两种：
//
//	kubepods-burstable-pod<uid>.slice   （Burstable / BestEffort）
//	kubepods-pod<uid>.slice             （Guaranteed，没有中间 QoS 层）
func isPodSlice(name string) bool {
	if !strings.HasSuffix(name, ".slice") {
		return false
	}
	if !strings.HasPrefix(name, "kubepods") {
		return false
	}
	return strings.Contains(name, "-pod")
}

// extractPodUID 从 slice 名里抠出 Pod UID，并把 systemd 的 "_" 还原成 "-"。
// kubepods-burstable-podaf44b779_41d8_43d5_a0d8_8a7a0b17abcd.slice
// -> af44b779-41d8-43d5-a0d8-8a7a0b17abcd
func extractPodUID(sliceName string) string {
	base := strings.TrimSuffix(sliceName, ".slice")
	idx := strings.Index(base, "-pod")
	if idx < 0 {
		return ""
	}
	uid := base[idx+len("-pod"):]
	return strings.ReplaceAll(uid, "_", "-")
}

// inferQoS 从路径推断 QoS 等级。路径里没有 QoS 中间层的就是 Guaranteed。
func inferQoS(rel string) string {
	switch {
	case strings.Contains(rel, "kubepods-besteffort.slice"):
		return kernel.QoSBestEffort
	case strings.Contains(rel, "kubepods-burstable.slice"):
		return kernel.QoSBurstable
	default:
		return kernel.QoSGuaranteed
	}
}

// SyncFromCgroup 扫描内核并把可压制的 Pod 纳管进来，返回新纳管的数量。
//
// quota <= 0（不限配额）的 Pod 会被跳过：对没有配额上限的容器做"按比例压制"没有意义，
// 0.2 倍的无限还是无限。这类容器要压制只能给绝对值，那属于另一套策略（按容量比例分配），
// 不在当前实现范围内——宁可跳过并记账，也不要算出一个错误的负数。
func (s *Suppressor) SyncFromCgroup(exec *kernel.Executor) int {
	pods, err := DiscoverPods(exec)
	if err != nil {
		klog.Errorf("[Discover] 扫描 cgroup 树失败: %v", err)
		return 0
	}

	added := 0
	for _, p := range pods {
		if p.Quota <= 0 {
			klog.V(4).Infof("[Discover] 跳过 %s: 未设置 CPU 配额上限", p.RelDir)
			continue
		}
		name, ns := splitPodDirName(p.RelDir)
		s.mu.Lock()
		if _, exists := s.trackedPods[podKey(ns, name)]; !exists {
			added++
		}
		s.mu.Unlock()
		s.RegisterPodCgroup(name, ns, p.QoSClass, p.Quota, p.Period, p.RelDir)
	}
	if added > 0 {
		klog.Infof("[Discover] 从内核纳管 %d 个 Pod 分组（总计扫描 %d 个）", added, len(pods))
	}
	return added
}

// splitPodDirName 用 cgroup 路径充当 name/namespace。
// 单机 Agent 拿不到 Pod 元数据时，用 Pod UID 作为稳定标识，避免引入 informer 依赖。
func splitPodDirName(rel string) (name, namespace string) {
	uid := extractPodUID(filepath.Base(rel))
	if uid == "" {
		uid = filepath.Base(rel)
	}
	return uid, "discovered"
}
