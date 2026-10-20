package kernel

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Version 表示节点上 cgroup 层级的版本。
//
// cgroup v1 是"一个控制器一棵树"（/sys/fs/cgroup/cpu、/sys/fs/cgroup/memory ...），
// cgroup v2 是"所有控制器同一棵树"（/sys/fs/cgroup），差异体现在三处：
//  1. 目录层级：v1 多一层控制器子目录
//  2. 文件名：cpu.shares -> cpu.weight、cpu.cfs_quota_us -> cpu.max、memory.limit_in_bytes -> memory.max
//  3. 值格式：v2 的 cpu.max 是 "quota period" 两个数写一个文件，内存上限可以用字符串 "max" 表示无限
//
// 这三处差异全部收敛在 Resource 的实现里，上层不感知。
type Version int

const (
	VersionUnknown Version = iota
	VersionV1
	VersionV2
)

func (v Version) String() string {
	switch v {
	case VersionV1:
		return "v1"
	case VersionV2:
		return "v2"
	default:
		return "unknown"
	}
}

// Resource 抽象一个 cgroup 控制文件。
type Resource interface {
	// Type 返回资源的稳定标识，用于日志与比较策略选择（例如 cpuset 需要语义比较）。
	Type() string
	// Controller 返回 v1 下的子系统目录名（cpu / cpuset / memory ...）。v2 忽略该值。
	Controller() string
	// FileName 返回指定版本下的文件名。返回空串表示该版本不支持此资源。
	FileName(v Version) string
	// IsValid 校验待写入的值是否合法。
	IsValid(value string) (bool, string)
}

// simpleResource 是绝大多数资源的通用实现。
type simpleResource struct {
	typ        string
	controller string
	v1Name     string
	v2Name     string
	validate   func(string) (bool, string)
}

func (r *simpleResource) Type() string       { return r.typ }
func (r *simpleResource) Controller() string { return r.controller }

func (r *simpleResource) FileName(v Version) string {
	switch v {
	case VersionV1:
		return r.v1Name
	case VersionV2:
		return r.v2Name
	default:
		return ""
	}
}

func (r *simpleResource) IsValid(value string) (bool, string) {
	if r.validate == nil {
		return true, ""
	}
	return r.validate(value)
}

func newResource(typ, controller, v1Name, v2Name string, validate func(string) (bool, string)) Resource {
	return &simpleResource{typ: typ, controller: controller, v1Name: v1Name, v2Name: v2Name, validate: validate}
}

// 资源清单。v1Name 或 v2Name 为空串表示该版本内核没有这个接口。
var (
	// CPUShares v1 的 cpu.shares 在 v2 改名为 cpu.weight，取值范围也从 2~262144 变成 1~10000。
	CPUShares = newResource("cpu.shares", "cpu", "cpu.shares", "cpu.weight", validateWeight)

	// CPUQuota v1 用 cpu.cfs_quota_us（单值，-1 表示不限），v2 合并进 cpu.max（"quota period"）。
	CPUQuota = newResource("cpu.cfs_quota_us", "cpu", "cpu.cfs_quota_us", "cpu.max", validateQuota)

	// CPUPeriod v1 是独立文件，v2 与 quota 共用一个文件，写入由 Executor.SetCPUQuota 统一处理。
	CPUPeriod = newResource("cpu.cfs_period_us", "cpu", "cpu.cfs_period_us", "cpu.max", validatePeriod)

	// CPUSet 两版同名，但 v1 必须与 cpuset.mems 成对设置，否则 cgroup 拒绝接收任务。
	CPUSet = newResource("cpuset.cpus", "cpuset", "cpuset.cpus", "cpuset.cpus", validateCPUSetValue)

	CPUSetMems = newResource("cpuset.mems", "cpuset", "cpuset.mems", "cpuset.mems", validateCPUListValue)

	// MemoryLimit v1 是 memory.limit_in_bytes，v2 是 memory.max。
	MemoryLimit = newResource("memory.limit_in_bytes", "memory", "memory.limit_in_bytes", "memory.max", validateMemoryValue)

	// MemoryHigh v1 的软限制叫 soft_limit_in_bytes，v2 叫 memory.high。
	// 注意语义不同：v1 的 soft limit 只在内存紧张时作为回收优先级参考，v2 的 high 是硬性节流点（超过就同步回收+限速）。
	MemoryHigh = newResource("memory.soft_limit_in_bytes", "memory", "memory.soft_limit_in_bytes", "memory.high", validateMemoryValue)

	// MemoryLow 只有 v2 有，v1 没有对应物。
	MemoryLow = newResource("memory.low", "memory", "", "memory.low", validateMemoryValue)
)

func validateWeight(v string) (bool, string) {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return false, "不是整数"
	}
	if n <= 0 {
		return false, "权重必须大于 0"
	}
	return true, ""
}

func validateQuota(v string) (bool, string) {
	v = strings.TrimSpace(v)
	if v == "max" {
		return true, ""
	}
	// v2 的 cpu.max 是 "quota period" 两段
	fields := strings.Fields(v)
	if len(fields) == 2 {
		if _, err := strconv.ParseInt(fields[0], 10, 64); err != nil && fields[0] != "max" {
			return false, "quota 段不是整数"
		}
		if _, err := strconv.ParseInt(fields[1], 10, 64); err != nil {
			return false, "period 段不是整数"
		}
		return true, ""
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return false, "不是整数"
	}
	if n == -1 || n > 0 {
		return true, ""
	}
	return false, "quota 只能是 -1（不限）或正整数"
}

func validatePeriod(v string) (bool, string) {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return false, "不是整数"
	}
	// 内核限制：1000us <= period <= 1000000us
	if n < 1000 || n > 1000000 {
		return false, "period 必须在 1000~1000000 微秒之间"
	}
	return true, ""
}

func validateMemoryValue(v string) (bool, string) {
	v = strings.TrimSpace(v)
	if v == "max" || v == "-1" {
		return true, ""
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return false, "不是整数"
	}
	if n < 0 {
		return false, "内存值不能为负"
	}
	return true, ""
}

func validateCPUSetValue(v string) (bool, string) {
	if strings.TrimSpace(v) == "" {
		// 写空值是合法的：表示把该 cgroup 的 CPU 集合清空，但随后必须先设置才能接收任务。
		return true, ""
	}
	_, err := ParseCPUList(v)
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

func validateCPUListValue(v string) (bool, string) {
	return validateCPUSetValue(v)
}

// ParseCPUList 解析内核的 CPU 列表格式，例如 "0-3,8,10-11"。
//
// 内核读回来的格式是"压缩"的（写 2,3 读回来是 2-3），所以比较 cpuset 时必须
// 先解析成集合再比，不能直接比字符串。
func ParseCPUList(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("CPU 列表含空段: %q", s)
		}
		if dash := strings.Index(part, "-"); dash >= 0 {
			lo, err := strconv.Atoi(strings.TrimSpace(part[:dash]))
			if err != nil {
				return nil, fmt.Errorf("CPU 列表区间左值非法: %q", part)
			}
			hi, err := strconv.Atoi(strings.TrimSpace(part[dash+1:]))
			if err != nil {
				return nil, fmt.Errorf("CPU 列表区间右值非法: %q", part)
			}
			if hi < lo {
				return nil, fmt.Errorf("CPU 列表区间倒置: %q", part)
			}
			if hi-lo > 4096 {
				return nil, fmt.Errorf("CPU 列表区间过大: %q", part)
			}
			for c := lo; c <= hi; c++ {
				out = append(out, c)
			}
			continue
		}
		c, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("CPU 编号非法: %q", part)
		}
		out = append(out, c)
	}
	return out, nil
}

// FormatCPUList 把 CPU 编号列表压缩成内核格式，例如 [2,3,5] -> "2-3,5"。
func FormatCPUList(cpus []int) string {
	if len(cpus) == 0 {
		return ""
	}
	sorted := make([]int, len(cpus))
	copy(sorted, cpus)
	sort.Ints(sorted)

	var sb strings.Builder
	start, prev := sorted[0], sorted[0]
	flush := func() {
		if sb.Len() > 0 {
			sb.WriteByte(',')
		}
		if start == prev {
			sb.WriteString(strconv.Itoa(start))
		} else {
			sb.WriteString(strconv.Itoa(start))
			sb.WriteByte('-')
			sb.WriteString(strconv.Itoa(prev))
		}
	}
	for _, c := range sorted[1:] {
		if c == prev+1 {
			prev = c
			continue
		}
		flush()
		start, prev = c, c
	}
	flush()
	return sb.String()
}

// CPUSetEqual 语义比较两个 CPU 列表，避免 "2,3" 与 "2-3" 被判为不同而反复重写。
func CPUSetEqual(a, b string) bool {
	if strings.TrimSpace(a) == strings.TrimSpace(b) {
		return true
	}
	sa, err1 := ParseCPUList(a)
	sb, err2 := ParseCPUList(b)
	if err1 != nil || err2 != nil {
		return false
	}
	if len(sa) != len(sb) {
		return false
	}
	sort.Ints(sa)
	sort.Ints(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}
