package hardware

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeResctrl 造一棵假的 resctrl 目录树，用来验证 schemata 序列化逻辑。
func fakeResctrl(t *testing.T, cbmMask, rootSchemata string, withMB bool) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("建目录失败: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
	}
	write("info/L3/cbm_mask", cbmMask)
	if withMB {
		if err := os.MkdirAll(filepath.Join(root, "info", "MB"), 0755); err != nil {
			t.Fatalf("建 MB 目录失败: %v", err)
		}
	}
	write("schemata", rootSchemata)
	return root
}

// TestRDTUnsupportedOnPlainHost 验证没有 resctrl 的机器会优雅降级而不是 panic。
// 这正是 AMD 机器和绝大多数云主机的真实情况。
func TestRDTUnsupportedOnPlainHost(t *testing.T) {
	m := NewRDTManagerAt(t.TempDir(), DefaultRDTConfig())
	if m.Supported() {
		t.Fatalf("空目录不应判定为支持 resctrl")
	}
	if m.UnsupportedReason() == "" {
		t.Fatalf("应给出不可用原因")
	}
	if err := m.ApplySchemata(CLOSOffline, "0x3", 20); err == nil {
		t.Fatalf("不支持时应返回错误")
	}
	mask, mba := m.GetGroupConfig(CLOSOffline)
	if mask != "0x3" || mba != 20 {
		t.Fatalf("降级后应回退到配置值，实际 mask=%s mba=%d", mask, mba)
	}
}

// TestRDTMaskForPercent 验证按 way 数计算连续掩码。
// 20 way 平台给 20% 应该是 4 个 way -> 0xf，而不是硬编码的 0x3。
func TestRDTMaskForPercent(t *testing.T) {
	root := fakeResctrl(t, "fffff\n", "L3:0=fffff\n", true)
	m := NewRDTManagerAt(root, DefaultRDTConfig())

	if !m.Supported() {
		t.Fatalf("应判定为支持 resctrl")
	}
	if m.L3Ways() != 20 {
		t.Fatalf("L3 way 数 = %d, 期望 20", m.L3Ways())
	}
	cases := []struct {
		pct  int
		want string
	}{
		{100, "0xfffff"},
		{20, "0xf"},
		{50, "0x3ff"},
		{1, "0x1"},
	}
	for _, c := range cases {
		if got := m.maskForPercent(c.pct); got != c.want {
			t.Fatalf("maskForPercent(%d) = %s, 期望 %s", c.pct, got, c.want)
		}
	}
}

// TestApplySchemata 验证 schemata 的格式：L3 十六进制、MB 十进制、分号分隔、换行结尾。
func TestApplySchemata(t *testing.T) {
	root := fakeResctrl(t, "fffff\n", "L3:0=fffff\nMB:0=100\n", true)
	m := NewRDTManagerAt(root, DefaultRDTConfig())

	if err := m.ApplySchemata(CLOSOffline, "0xf", 20); err != nil {
		t.Fatalf("ApplySchemata 失败: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, CLOSOffline, "schemata"))
	if err != nil {
		t.Fatalf("读取 schemata 失败: %v", err)
	}
	want := "L3:0=f\nMB:0=20\n"
	if string(b) != want {
		t.Fatalf("schemata = %q, 期望 %q", string(b), want)
	}

	// 重复写入相同配额不应报错（内部会先读回比较）
	if err := m.ApplySchemata(CLOSOffline, "0xf", 20); err != nil {
		t.Fatalf("重复 ApplySchemata 失败: %v", err)
	}
}

// TestApplySchemataDualSocket 验证双路机器要写两个 cache id。
func TestApplySchemataDualSocket(t *testing.T) {
	root := fakeResctrl(t, "fffff\n", "L3:0=fffff;1=fffff\nMB:0=100;1=100\n", true)
	m := NewRDTManagerAt(root, DefaultRDTConfig())

	if err := m.ApplySchemata(CLOSOffline, "0xf", 20); err != nil {
		t.Fatalf("ApplySchemata 失败: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(root, CLOSOffline, "schemata"))
	want := "L3:0=f;1=f\nMB:0=20;1=20\n"
	if string(b) != want {
		t.Fatalf("schemata = %q, 期望 %q", string(b), want)
	}
}

// TestAssignPID 验证进程通过 tasks 文件入组（写的是 TID 不是 PID）。
func TestAssignPID(t *testing.T) {
	root := fakeResctrl(t, "fffff\n", "L3:0=fffff\nMB:0=100\n", true)
	m := NewRDTManagerAt(root, DefaultRDTConfig())

	if err := m.ApplySchemata(CLOSOffline, "0xf", 20); err != nil {
		t.Fatalf("ApplySchemata 失败: %v", err)
	}
	if err := m.AssignPID(CLOSOffline, 4321); err != nil {
		t.Fatalf("AssignPID 失败: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, CLOSOffline, "tasks"))
	if err != nil {
		t.Fatalf("读取 tasks 失败: %v", err)
	}
	if string(b) != "4321" {
		t.Fatalf("tasks = %q, 期望 4321", string(b))
	}
}

// TestApplyForPod 串起"分配组 -> 写 schemata -> 放进程"的完整链路。
func TestApplyForPod(t *testing.T) {
	root := fakeResctrl(t, "fffff\n", "L3:0=fffff\nMB:0=100\n", true)
	m := NewRDTManagerAt(root, DefaultRDTConfig())

	group, err := m.ApplyForPod("pod-uid-1", "BestEffort", 99)
	if err != nil {
		t.Fatalf("ApplyForPod 失败: %v", err)
	}
	if group != CLOSOffline {
		t.Fatalf("BestEffort 应进 %s，实际 %s", CLOSOffline, group)
	}
	if _, err := os.Stat(filepath.Join(root, CLOSOffline, "tasks")); err != nil {
		t.Fatalf("tasks 未生成: %v", err)
	}

	group, err = m.ApplyForPod("pod-uid-2", "Guaranteed", 100)
	if err != nil {
		t.Fatalf("ApplyForPod 失败: %v", err)
	}
	if group != CLOSOnline {
		t.Fatalf("Guaranteed 应进 %s，实际 %s", CLOSOnline, group)
	}
}
