package kernel

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	// 真实 cgroup 文件的读回值总是带尾随换行，比较内容时统一去掉空白
	return strings.TrimSpace(string(b))
}

// TestDetectVersion 用特征文件模拟三种宿主机形态。
func TestDetectVersion(t *testing.T) {
	root := t.TempDir()
	mkFile(t, filepath.Join(root, "cgroup.controllers"), "cpuset cpu io memory pids\n")
	if got := DetectVersion(root); got != VersionV2 {
		t.Fatalf("探测结果 = %v, 期望 v2", got)
	}

	root1 := t.TempDir()
	mkFile(t, filepath.Join(root1, "cpu", "cpu.shares"), "1024\n")
	if got := DetectVersion(root1); got != VersionV1 {
		t.Fatalf("探测结果 = %v, 期望 v1", got)
	}

	empty := t.TempDir()
	if got := DetectVersion(empty); got != VersionUnknown {
		t.Fatalf("空目录探测结果 = %v, 期望 unknown", got)
	}
}

// TestWriteIfDifferentCPUSet 是本文件最重要的一条：
// 内核把 "2,3" 规范化为 "2-3"，第二次写入语义相同的值必须判定为"未变更"，
// 否则 2 秒一次的主循环会持续重写 cpuset.cpus，触发无谓的 sched_setaffinity 遍历。
func TestWriteIfDifferentCPUSet(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "kubepods.slice", "kubepods-podx.slice")
	mkFile(t, filepath.Join(dir, "cpuset.cpus"), "0-3\n")
	mkFile(t, filepath.Join(dir, "cpuset.mems"), "0\n")

	e := NewExecutorWithVersion(root, VersionV2)

	changed, err := e.WriteIfDifferent("kubepods.slice/kubepods-podx.slice", CPUSet, "2-3")
	if err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	if !changed {
		t.Fatalf("首次写入应判定为已变更")
	}
	if got := readFile(t, filepath.Join(dir, "cpuset.cpus")); got != "2-3" {
		t.Fatalf("写入后内容 = %q, 期望 %q", got, "2-3")
	}

	// 语义等价的不同写法：内核不会真的改文件，我们也应判定为未变更
	changed, err = e.WriteIfDifferent("kubepods.slice/kubepods-podx.slice", CPUSet, "2,3")
	if err != nil {
		t.Fatalf("二次写入失败: %v", err)
	}
	if changed {
		t.Fatalf("语义等价的写入不应判定为变更，否则会反复重写 cpuset.cpus")
	}

	// 真正不同的值必须写进去
	changed, err = e.WriteIfDifferent("kubepods.slice/kubepods-podx.slice", CPUSet, "0-1")
	if err != nil {
		t.Fatalf("三次写入失败: %v", err)
	}
	if !changed {
		t.Fatalf("不同值应判定为已变更")
	}
}

// TestSetCPUQuotaV2 验证 v2 下 quota 与 period 合并写进 cpu.max。
func TestSetCPUQuotaV2(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "podx.slice")
	mkFile(t, filepath.Join(dir, "cpu.max"), "max 100000\n")

	e := NewExecutorWithVersion(root, VersionV2)

	changed, err := e.SetCPUQuota("podx.slice", 50000, 100000)
	if err != nil {
		t.Fatalf("设置 quota 失败: %v", err)
	}
	if !changed {
		t.Fatalf("应判定为已变更")
	}
	if got := readFile(t, filepath.Join(dir, "cpu.max")); got != "50000 100000" {
		t.Fatalf("cpu.max = %q, 期望 %q", got, "50000 100000")
	}

	changed, err = e.SetCPUQuota("podx.slice", 50000, 100000)
	if err != nil {
		t.Fatalf("重复设置失败: %v", err)
	}
	if changed {
		t.Fatalf("相同配置重复设置不应判定为变更")
	}

	quota, period, err := e.GetCPUQuota("podx.slice")
	if err != nil {
		t.Fatalf("读取 quota 失败: %v", err)
	}
	if quota != 50000 || period != 100000 {
		t.Fatalf("读取结果 quota=%d period=%d, 期望 50000/100000", quota, period)
	}

	// quota<=0 表示解除限制，v2 写 "max"
	if _, err := e.SetCPUQuota("podx.slice", -1, 100000); err != nil {
		t.Fatalf("解除限制失败: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "cpu.max")); got != "max 100000" {
		t.Fatalf("解除限制后 cpu.max = %q, 期望 %q", got, "max 100000")
	}
	quota, _, err = e.GetCPUQuota("podx.slice")
	if err != nil {
		t.Fatalf("再次读取失败: %v", err)
	}
	if quota != -1 {
		t.Fatalf("解除限制后 quota = %d, 期望 -1", quota)
	}
}

// TestSetCPUQuotaV1 验证 v1 下 quota 与 period 是两个文件，且 period 先写。
func TestSetCPUQuotaV1(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "cpu", "kubepods", "podx")
	mkFile(t, filepath.Join(dir, "cpu.cfs_quota_us"), "-1\n")
	mkFile(t, filepath.Join(dir, "cpu.cfs_period_us"), "100000\n")

	e := NewExecutorWithVersion(root, VersionV1)
	if got := e.DirPath("kubepods/podx", CPUQuota); got != dir {
		t.Fatalf("v1 目录路径 = %q, 期望 %q", got, dir)
	}

	changed, err := e.SetCPUQuota("kubepods/podx", 20000, 100000)
	if err != nil {
		t.Fatalf("v1 设置 quota 失败: %v", err)
	}
	if !changed {
		t.Fatalf("应判定为已变更")
	}
	if got := readFile(t, filepath.Join(dir, "cpu.cfs_quota_us")); got != "20000" {
		t.Fatalf("cpu.cfs_quota_us = %q, 期望 20000", got)
	}
	if got := readFile(t, filepath.Join(dir, "cpu.cfs_period_us")); got != "100000" {
		t.Fatalf("cpu.cfs_period_us = %q, 期望 100000", got)
	}
}

// TestMaxSymbolEquivalence 验证 v2 的 "max" 与 -1 被视为同一含义，
// 否则 memory.max 会陷入"读到 max、写入 -1、再读到 max"的无限重写。
func TestMaxSymbolEquivalence(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "podx.slice")
	mkFile(t, filepath.Join(dir, "memory.max"), "max\n")

	e := NewExecutorWithVersion(root, VersionV2)
	changed, err := e.WriteIfDifferent("podx.slice", MemoryLimit, "-1")
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if changed {
		t.Fatalf("max 与 -1 语义相同，不应判定为变更")
	}
}

// TestWriteIfDifferentErrors 覆盖三道闸的错误分类，调用方需要靠错误类型决定是否重试。
func TestWriteIfDifferentErrors(t *testing.T) {
	root := t.TempDir()
	e := NewExecutorWithVersion(root, VersionV2)

	// 闸 1：目录不存在
	_, err := e.WriteIfDifferent("nope.slice", CPUSet, "0-1")
	if !errors.Is(err, ErrDirNotExist) {
		t.Fatalf("期望 ErrDirNotExist，实际 %v", err)
	}

	// 闸 2：值非法
	mkFile(t, filepath.Join(root, "podx.slice", "cpuset.cpus"), "0-3\n")
	_, err = e.WriteIfDifferent("podx.slice", CPUSet, "3-1")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("期望 ErrInvalidValue，实际 %v", err)
	}

	// 闸 3：文件不存在（v1 没有 memory.low）
	e1 := NewExecutorWithVersion(root, VersionV1)
	mkFile(t, filepath.Join(root, "memory", "podx", "memory.limit_in_bytes"), "1024\n")
	_, err = e1.WriteIfDifferent("podx", MemoryLow, "1024")
	if !errors.Is(err, ErrFileNotExist) {
		t.Fatalf("期望 ErrFileNotExist，实际 %v", err)
	}
}

func TestEnsureDir(t *testing.T) {
	root := t.TempDir()
	mkFile(t, filepath.Join(root, "cgroup.controllers"), "cpuset cpu memory\n")
	mkFile(t, filepath.Join(root, "cgroup.subtree_control"), "\n")

	e := NewExecutorWithVersion(root, VersionV2)
	rel := "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-podx.slice"
	if err := e.EnsureDir(rel, "cpuset", "cpu", "memory"); err != nil {
		t.Fatalf("EnsureDir 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
		t.Fatalf("目录未创建: %v", err)
	}
}
