package kernel

import "testing"

const testUID = "af44b779-41d8-43d5-a0d8-8a7a0b17abcd"

func TestNormalizePodUID(t *testing.T) {
	got := NormalizePodUID(testUID)
	want := "af44b779_41d8_43d5_a0d8_8a7a0b17abcd"
	if got != want {
		t.Fatalf("NormalizePodUID = %q, 期望 %q", got, want)
	}
}

func TestSplitContainerID(t *testing.T) {
	cases := []struct {
		in      string
		runtime string
		id      string
	}{
		{"containerd://abc123", "containerd", "abc123"},
		{"docker://def456", "docker", "def456"},
		{"abc123", "", "abc123"},
		{"", "", ""},
	}
	for _, c := range cases {
		rt, id := SplitContainerID(c.in)
		if rt != c.runtime || id != c.id {
			t.Fatalf("SplitContainerID(%q) = (%q,%q), 期望 (%q,%q)", c.in, rt, id, c.runtime, c.id)
		}
	}
}

func TestContainerScopeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"containerd://abc123", "cri-containerd-abc123.scope"},
		{"docker://def456", "docker-def456.scope"},
		{"cri-o://ghi789", "crio-ghi789.scope"},
		{"abc123", "cri-containerd-abc123.scope"},
	}
	for _, c := range cases {
		if got := ContainerScopeName(c.in); got != c.want {
			t.Fatalf("ContainerScopeName(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestPodSliceRelPath 固化三种 QoS 在两种 cgroup 驱动下的路径形态。
// 这是排查"找不到 Pod 的 cgroup"时最先要看的东西。
func TestPodSliceRelPath(t *testing.T) {
	uid := "af44b779_41d8_43d5_a0d8_8a7a0b17abcd"

	cases := []struct {
		qos    string
		driver Driver
		want   string
	}{
		{QoSGuaranteed, DriverSystemd, "kubepods.slice/kubepods-pod" + uid + ".slice"},
		{QoSBurstable, DriverSystemd, "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" + uid + ".slice"},
		{QoSBestEffort, DriverSystemd, "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod" + uid + ".slice"},
		{QoSGuaranteed, DriverCgroupfs, "kubepods/pod" + uid},
		{QoSBurstable, DriverCgroupfs, "kubepods/burstable/pod" + uid},
		{QoSBestEffort, DriverCgroupfs, "kubepods/besteffort/pod" + uid},
	}
	for _, c := range cases {
		got := PodSliceRelPath(testUID, c.qos, c.driver)
		if got != c.want {
			t.Fatalf("PodSliceRelPath(%s,%s) = %q, 期望 %q", c.qos, c.driver, got, c.want)
		}
	}
}

func TestContainerRelPath(t *testing.T) {
	cases := []struct {
		qos      string
		cid      string
		driver   Driver
		wantTail string
	}{
		{QoSBurstable, "containerd://abc123", DriverSystemd, "/cri-containerd-abc123.scope"},
		{QoSBurstable, "docker://abc123", DriverSystemd, "/docker-abc123.scope"},
		{QoSGuaranteed, "containerd://abc123", DriverSystemd, "/cri-containerd-abc123.scope"},
		{QoSBurstable, "containerd://abc123", DriverCgroupfs, "/abc123"},
	}
	for _, c := range cases {
		got := ContainerRelPath(testUID, c.qos, c.cid, c.driver)
		want := PodSliceRelPath(testUID, c.qos, c.driver) + c.wantTail
		if got != want {
			t.Fatalf("ContainerRelPath(%s,%s,%s) = %q, 期望 %q", c.qos, c.cid, c.driver, got, want)
		}
	}
}

func TestValidateRelPath(t *testing.T) {
	if err := ValidateRelPath("kubepods.slice/kubepods-podx.slice"); err != nil {
		t.Fatalf("合法路径被拒: %v", err)
	}
	if err := ValidateRelPath("../etc"); err == nil {
		t.Fatalf("越界路径未被拒")
	}
	if err := ValidateRelPath("/abs/path"); err == nil {
		t.Fatalf("绝对路径未被拒")
	}
	if err := ValidateRelPath(""); err == nil {
		t.Fatalf("空路径未被拒")
	}
}
