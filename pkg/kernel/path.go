package kernel

import (
	"fmt"
	"strings"
)

// QoS 等级字符串，与 corev1.PodQOSClass 的取值一致。
const (
	QoSGuaranteed = "Guaranteed"
	QoSBurstable  = "Burstable"
	QoSBestEffort = "BestEffort"
)

// Driver 表示 kubelet 的 cgroup 驱动方式。
//
// 这是新手最容易踩的坑：同一台机器上，kubelet 用 systemd 驱动还是 cgroupfs 驱动，
// Pod 的 cgroup 路径形态完全不同。
//
//	systemd：kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod<uid>.slice/cri-containerd-<cid>.scope
//	cgroupfs：kubepods/burstable/pod<uid>/<cid>
//
// Kubernetes 1.24 之后 systemd 是默认值，kind / k3s / 主流发行版都是 systemd。
type Driver string

const (
	DriverSystemd  Driver = "systemd"
	DriverCgroupfs Driver = "cgroupfs"
)

// NormalizePodUID 把 Pod UID 转成 cgroup 目录里的形态。
//
// systemd 的 slice 命名不允许出现 "-"，所以 kubelet 会把 UID 里的 "-" 全部换成 "_"：
// af44b779-41d8-43d5-a0d8-8a7a0b17abcd -> af44b779_41d8_43d5_a0d8_8a7a0b17abcd
func NormalizePodUID(uid string) string {
	return strings.ReplaceAll(strings.TrimSpace(uid), "-", "_")
}

// SplitContainerID 拆分 CRI 返回的容器 ID，例如 "containerd://abc123" -> ("containerd", "abc123")。
func SplitContainerID(containerID string) (runtime, id string) {
	containerID = strings.TrimSpace(containerID)
	if i := strings.Index(containerID, "://"); i >= 0 {
		return strings.ToLower(containerID[:i]), containerID[i+3:]
	}
	return "", containerID
}

// ContainerScopeName 生成容器在 systemd 驱动下的 scope 名。
//
// 不同运行时的前缀不一样，写错会导致路径不存在——而报错只是 "no such file"，
// 排查起来很费时间：
//
//	containerd -> cri-containerd-<id>.scope
//	docker     -> docker-<id>.scope
//	cri-o      -> crio-<id>.scope
func ContainerScopeName(containerID string) string {
	rt, id := SplitContainerID(containerID)
	switch rt {
	case "docker":
		return "docker-" + id + ".scope"
	case "cri-o", "crio":
		return "crio-" + id + ".scope"
	case "containerd", "":
		return "cri-containerd-" + id + ".scope"
	default:
		// 未知运行时按 containerd 形态兜底，调用方应校验路径是否存在。
		return "cri-" + rt + "-" + id + ".scope"
	}
}

// PodSliceRelPath 返回 Pod 级 cgroup 目录相对于 cgroup 根的路径。
//
// QoS 决定层级，这是第三个坑：只有 Guaranteed 是扁平的一层，
// Burstable 和 BestEffort 中间还夹着一层 QoS slice。
func PodSliceRelPath(podUID, qosClass string, d Driver) string {
	uid := NormalizePodUID(podUID)

	if d == DriverCgroupfs {
		switch qosClass {
		case QoSBurstable:
			return "kubepods/burstable/pod" + uid
		case QoSBestEffort:
			return "kubepods/besteffort/pod" + uid
		default:
			return "kubepods/pod" + uid
		}
	}

	switch qosClass {
	case QoSBurstable:
		return "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" + uid + ".slice"
	case QoSBestEffort:
		return "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod" + uid + ".slice"
	default:
		// Guaranteed 没有中间的 QoS slice 层
		return "kubepods.slice/kubepods-pod" + uid + ".slice"
	}
}

// ContainerRelPath 返回容器级 cgroup 目录相对于 cgroup 根的路径。
//
// 为什么要落到容器级而不是 Pod 级：Kubernetes 的 resources.limits 最终落在容器 cgroup 上，
// Pod 级 cgroup 只是一个聚合视图。改 Pod 级的 cpu.max 不会影响已经在跑的容器。
func ContainerRelPath(podUID, qosClass, containerID string, d Driver) string {
	pod := PodSliceRelPath(podUID, qosClass, d)
	if d == DriverCgroupfs {
		_, id := SplitContainerID(containerID)
		return pod + "/" + id
	}
	return pod + "/" + ContainerScopeName(containerID)
}

// PodSandboxRelPath 返回 Pod 沙箱（pause 容器）的 cgroup 路径。
// 沙箱容器本身也占一份资源，压制/绑核时如果漏掉它，账目会对不上。
// sandboxID 是 pause 容器的 ID（不是 Pod UID），通常来自 PodSandboxStatus。
func PodSandboxRelPath(podUID, qosClass, sandboxID string, d Driver) string {
	pod := PodSliceRelPath(podUID, qosClass, d)
	if d == DriverCgroupfs {
		_, id := SplitContainerID(sandboxID)
		return pod + "/" + id
	}
	return pod + "/" + ContainerScopeName(sandboxID)
}

// ValidateRelPath 做一次防御性校验：路径里不允许出现 ".."，避免拼接被注入成越界路径。
func ValidateRelPath(p string) error {
	if p == "" {
		return fmt.Errorf("cgroup 相对路径为空")
	}
	if strings.Contains(p, "..") {
		return fmt.Errorf("cgroup 相对路径包含非法片段: %q", p)
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("cgroup 相对路径不应以 / 开头: %q", p)
	}
	return nil
}
