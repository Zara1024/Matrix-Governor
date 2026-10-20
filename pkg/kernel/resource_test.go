package kernel

import (
	"reflect"
	"testing"
)

func TestParseCPUList(t *testing.T) {
	cases := []struct {
		in      string
		want    []int
		wantErr bool
	}{
		{"0-3", []int{0, 1, 2, 3}, false},
		{"2,3", []int{2, 3}, false},
		{"0-1,3", []int{0, 1, 3}, false},
		{" 0-1 , 3 ", []int{0, 1, 3}, false},
		{"5", []int{5}, false},
		{"", nil, false},
		{"3-1", nil, true},
		{"a-b", nil, true},
		{"1,,2", nil, true},
	}
	for _, c := range cases {
		got, err := ParseCPUList(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("ParseCPUList(%q) 期望报错，实际成功", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseCPUList(%q) 意外报错: %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("ParseCPUList(%q) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

func TestFormatCPUList(t *testing.T) {
	cases := []struct {
		in   []int
		want string
	}{
		{[]int{2, 3}, "2-3"},
		{[]int{3, 2}, "2-3"},
		{[]int{0, 1, 3}, "0-1,3"},
		{[]int{5}, "5"},
		{[]int{}, ""},
	}
	for _, c := range cases {
		if got := FormatCPUList(c.in); got != c.want {
			t.Fatalf("FormatCPUList(%v) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestCPUSetEqual 覆盖最关键的一条：内核会把 "2,3" 压缩成 "2-3"，
// 如果按字符串比较就会判定"不一致"，导致每轮循环都重写 cpuset.cpus。
func TestCPUSetEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2,3", "2-3", true},
		{"0-3", "0,1,2,3", true},
		{"0-1,3", "0,1,3", true},
		{"2,3", "2,4", false},
		{"0-3", "0-2", false},
		{"", "", true},
	}
	for _, c := range cases {
		if got := CPUSetEqual(c.a, c.b); got != c.want {
			t.Fatalf("CPUSetEqual(%q,%q) = %v, 期望 %v", c.a, c.b, got, c.want)
		}
	}
}

// TestResourceFileName 固化 v1/v2 的文件名映射，这是整个适配层的核心知识。
func TestResourceFileName(t *testing.T) {
	cases := []struct {
		r      Resource
		v1, v2 string
	}{
		{CPUShares, "cpu.shares", "cpu.weight"},
		{CPUQuota, "cpu.cfs_quota_us", "cpu.max"},
		{CPUPeriod, "cpu.cfs_period_us", "cpu.max"},
		{CPUSet, "cpuset.cpus", "cpuset.cpus"},
		{CPUSetMems, "cpuset.mems", "cpuset.mems"},
		{MemoryLimit, "memory.limit_in_bytes", "memory.max"},
		{MemoryHigh, "memory.soft_limit_in_bytes", "memory.high"},
		{MemoryLow, "", "memory.low"},
	}
	for _, c := range cases {
		if got := c.r.FileName(VersionV1); got != c.v1 {
			t.Fatalf("%s v1 文件名 = %q, 期望 %q", c.r.Type(), got, c.v1)
		}
		if got := c.r.FileName(VersionV2); got != c.v2 {
			t.Fatalf("%s v2 文件名 = %q, 期望 %q", c.r.Type(), got, c.v2)
		}
	}
}

func TestResourceIsValid(t *testing.T) {
	type tc struct {
		r     Resource
		value string
		want  bool
	}
	cases := []tc{
		{CPUShares, "1024", true},
		{CPUShares, "0", false},
		{CPUQuota, "50000", true},
		{CPUQuota, "-1", true},
		{CPUQuota, "max", true},
		{CPUQuota, "50000 100000", true},
		{CPUQuota, "0", false},
		{CPUPeriod, "100000", true},
		{CPUPeriod, "100", false},
		{CPUPeriod, "2000000", false},
		{CPUSet, "0-3", true},
		{CPUSet, "2,3", true},
		{CPUSet, "3-1", false},
		{MemoryLimit, "1073741824", true},
		{MemoryLimit, "max", true},
		{MemoryLimit, "-1", true},
		{MemoryHigh, "512Mi", false},
	}
	for _, c := range cases {
		got, msg := c.r.IsValid(c.value)
		if got != c.want {
			t.Fatalf("%s.IsValid(%q) = %v (%s), 期望 %v", c.r.Type(), c.value, got, msg, c.want)
		}
	}
}
