// Command nodecheck 是节点内核能力的自检工具。
//
// 它做三件事：
//  1. 打印节点环境（cgroup 版本、CPU 集合、可用控制器）
//  2. self 模式：在真实 cgroup 树上建一个测试分组，写入 cpuset / cpu.max，
//     把真实进程移进去，再从 /proc/<pid>/status 读回 Cpus_allowed_list 验证绑核真的生效
//  3. pod 模式：按 kubelet 的命名规则拼出 Pod/容器的 cgroup 路径并真实创建，
//     验证路径拼接逻辑在真机上成立（不需要装 k8s）
//
// 用法：
//
//	nodecheck -mode all
//	nodecheck -mode self -root /sys/fs/cgroup
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Zara1024/Matrix-Governor/pkg/cgroup"
	"github.com/Zara1024/Matrix-Governor/pkg/kernel"
)

const (
	testSlice   = "matrix-nodecheck.slice"
	fakePodUID  = "11111111-2222-3333-4444-555555555555"
	fakeContID  = "containerd://0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	fakeSandbox = "containerd://fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

type reporter struct {
	pass int
	fail int
}

func (r *reporter) ok(name, detail string) {
	r.pass++
	fmt.Printf("  [PASS] %-42s %s\n", name, detail)
}

func (r *reporter) bad(name, detail string) {
	r.fail++
	fmt.Printf("  [FAIL] %-42s %s\n", name, detail)
}

func (r *reporter) check(name string, ok bool, detail string) {
	if ok {
		r.ok(name, detail)
	} else {
		r.bad(name, detail)
	}
}

func main() {
	root := flag.String("root", kernel.DefaultRoot, "cgroup 挂载点")
	mode := flag.String("mode", "all", "运行模式: self | pod | all")
	flag.Parse()

	e := kernel.NewExecutor(*root)
	rep := &reporter{}

	fmt.Println("========== 节点环境 ==========")
	fmt.Printf("  cgroup 根目录 : %s\n", e.RootDir())
	fmt.Printf("  cgroup 版本   : %s\n", e.Version())
	fmt.Printf("  CPU 核数      : %d\n", runtime.NumCPU())
	fmt.Printf("  CPU 集合      : %s\n", readTrimmed(e.RootDir()+"/cpuset.cpus.effective"))
	fmt.Printf("  可用控制器    : %s\n", readTrimmed(e.RootDir()+"/cgroup.controllers"))
	fmt.Printf("  已下发控制器  : %s\n", readTrimmed(e.RootDir()+"/cgroup.subtree_control"))
	fmt.Println()

	if e.Version() == kernel.VersionUnknown {
		fmt.Println("无法识别 cgroup 版本，退出。")
		os.Exit(2)
	}

	if *mode == "self" || *mode == "all" {
		runSelfCheck(e, rep)
	}
	if *mode == "pod" || *mode == "all" {
		runPodPathCheck(e, rep)
	}
	if *mode == "suppressor" || *mode == "all" {
		runSuppressorCheck(e, rep)
	}

	fmt.Println()
	fmt.Printf("========== 结果: %d 通过 / %d 失败 ==========\n", rep.pass, rep.fail)
	if rep.fail > 0 {
		os.Exit(1)
	}
}

// runSelfCheck 在真实 cgroup 树上验证写入链路。
func runSelfCheck(e *kernel.Executor, rep *reporter) {
	fmt.Println("========== self: 真实 cgroup 写入验证 ==========")

	if e.Version() != kernel.VersionV2 {
		fmt.Println("  当前非 cgroup v2，跳过真实写入验证（v1 需要按控制器分别建目录，本工具暂只覆盖 v2）")
		return
	}

	// 0. 确保根目录下发所需控制器（systemd 节点通常已开好，这里做幂等补齐）
	for _, c := range []string{"cpuset", "cpu", "memory"} {
		_ = os.WriteFile(e.RootDir()+"/cgroup.subtree_control", []byte("+"+c), 0644)
	}

	// 清掉上一次运行可能残留的分组，否则 cpuset 已经是目标值，
	// "是否发生写入" 会被判定为 false，误判成失败
	cleanup(e, testSlice)

	// 1. 建测试分组
	if err := e.EnsureDir(testSlice, "cpuset", "cpu", "memory"); err != nil {
		rep.bad("创建测试分组", err.Error())
		return
	}
	defer cleanup(e, testSlice)

	// 2. 选一组独占核：取后半段，保证与系统负载错开
	cores := pickCores(runtime.NumCPU())
	if len(cores) == 0 {
		rep.bad("选择独占核", "没有可用 CPU")
		return
	}
	mems, err := e.Read("", kernel.CPUSetMems)
	if err != nil || strings.TrimSpace(mems) == "" {
		mems = "0"
	}

	changed, err := e.SetCPUSet(testSlice, cores, parseList(mems))
	if err != nil {
		rep.bad("写入 cpuset.cpus", err.Error())
		return
	}
	rep.check("写入 cpuset.cpus", changed, fmt.Sprintf("目标=%s", kernel.FormatCPUList(cores)))

	// 3. 读回确认内核接受了这个值
	got, err := e.GetCPUSet(testSlice)
	if err != nil {
		rep.bad("读回 cpuset.cpus", err.Error())
		return
	}
	rep.check("读回 cpuset.cpus", kernel.FormatCPUList(got) == kernel.FormatCPUList(cores),
		fmt.Sprintf("读回=%s", kernel.FormatCPUList(got)))

	// 4. 起一个真实进程并移入该分组
	pid, err := spawnSleep(60)
	if err != nil {
		rep.bad("启动测试进程", err.Error())
		return
	}
	defer killPID(pid)

	if err := e.MovePID(testSlice, pid); err != nil {
		rep.bad("移入 cgroup.procs", err.Error())
		return
	}
	allowed, err := readAllowedCPUs(pid)
	if err != nil {
		rep.bad("读取 Cpus_allowed_list", err.Error())
		return
	}
	want := kernel.FormatCPUList(cores)
	rep.check("绑核真实生效(/proc/pid/status)", kernel.CPUSetEqual(allowed, want),
		fmt.Sprintf("Cpus_allowed_list=%s 期望=%s", allowed, want))

	// 5. 设置 CPU 带宽上限并读回
	if _, err := e.SetCPUQuota(testSlice, 20000, kernel.DefaultPeriod); err != nil {
		rep.bad("写入 cpu.max", err.Error())
		return
	}
	raw, err := e.Read(testSlice, kernel.CPUQuota)
	if err != nil {
		rep.bad("读回 cpu.max", err.Error())
		return
	}
	rep.check("写入 cpu.max", raw == "20000 100000", fmt.Sprintf("cpu.max=%q", raw))

	// 6. write-if-different：再写一次相同的值，必须判定为未变更
	changed, err = e.SetCPUQuota(testSlice, 20000, kernel.DefaultPeriod)
	rep.check("幂等写入(第二次不落盘)", err == nil && !changed, fmt.Sprintf("changed=%v err=%v", changed, err))

	// 7. cpuset 语义比较：写成 "2,3" 形式，若内核规范化为 "2-3" 则不应触发重写
	alt := joinByComma(cores)
	changed, err = e.WriteIfDifferent(testSlice, kernel.CPUSet, alt)
	rep.check("cpuset 语义比较", err == nil && !changed,
		fmt.Sprintf("写入 %q changed=%v（内核已规范化为 %q）", alt, changed, rawCPUSet(e, testSlice)))

	// 8. 真正制造 CPU 压力，看内核是否真的节流
	nrThrottled := throttleCount(e, testSlice)
	burner, err := spawnBusy()
	if err == nil {
		_ = e.MovePID(testSlice, burner)
		time.Sleep(1500 * time.Millisecond)
		killPID(burner)
		after := throttleCount(e, testSlice)
		rep.check("CPU 压力下内核节流", after > nrThrottled,
			fmt.Sprintf("nr_throttled %d -> %d", nrThrottled, after))
	}
}

// runPodPathCheck 按 kubelet 的命名规则真实建出 Pod/容器 cgroup 路径。
func runPodPathCheck(e *kernel.Executor, rep *reporter) {
	fmt.Println()
	fmt.Println("========== pod: kubelet 路径拼接验证 ==========")

	if e.Version() != kernel.VersionV2 {
		fmt.Println("  跳过：仅覆盖 cgroup v2")
		return
	}

	podRel := kernel.PodSliceRelPath(fakePodUID, kernel.QoSBurstable, kernel.DriverSystemd)
	contRel := kernel.ContainerRelPath(fakePodUID, kernel.QoSBurstable, fakeContID, kernel.DriverSystemd)
	sbxRel := kernel.PodSandboxRelPath(fakePodUID, kernel.QoSBurstable, fakeSandbox, kernel.DriverSystemd)

	fmt.Printf("  Pod   路径: %s\n", podRel)
	fmt.Printf("  容器  路径: %s\n", contRel)
	fmt.Printf("  沙箱  路径: %s\n", sbxRel)

	rep.check("Pod UID 短横线转下划线", strings.Contains(podRel, "11111111_2222_3333_4444_555555555555"),
		podRel)
	rep.check("Burstable 中间 QoS 层", strings.Contains(podRel, "kubepods-burstable.slice/"), podRel)
	rep.check("容器 scope 命名", strings.HasSuffix(contRel, kernel.ContainerScopeName(fakeContID)), contRel)

	if err := e.EnsureDir(contRel, "cpuset", "cpu", "memory"); err != nil {
		rep.bad("按拼接路径创建 cgroup", err.Error())
		return
	}
	defer cleanup(e, contRel)

	cores := pickCores(runtime.NumCPU())
	mems, _ := e.Read("", kernel.CPUSetMems)
	if strings.TrimSpace(mems) == "" {
		mems = "0"
	}
	changed, err := e.SetCPUSet(contRel, cores, parseList(mems))
	rep.check("在容器 cgroup 写 cpuset", err == nil && changed,
		fmt.Sprintf("目标=%s err=%v", kernel.FormatCPUList(cores), err))

	// 清理：容器 scope -> pod slice -> QoS slice -> kubepods.slice
	cleanup(e, kernel.PodSliceRelPath(fakePodUID, kernel.QoSBurstable, kernel.DriverSystemd))
	cleanup(e, "kubepods.slice/kubepods-burstable.slice")
	cleanup(e, "kubepods.slice")
}

// runSuppressorCheck 验证"水位 -> 压制决策 -> 真实写入内核"的完整链路。
// 造两个 Pod slice（BestEffort 与 Guaranteed），驱动三档水位，
// 每一步都从磁盘读回 cpu.max，确认该变的变了、不该动的没动。
func runSuppressorCheck(e *kernel.Executor, rep *reporter) {
	fmt.Println()
	fmt.Println("========== suppressor: 水位驱动内核写入 全链路 ==========")
	if e.Version() != kernel.VersionV2 {
		fmt.Println("  跳过：仅覆盖 cgroup v2")
		return
	}

	const (
		beUID = "aaaaaaaa-1111-2222-3333-444444444444"
		gaUID = "bbbbbbbb-1111-2222-3333-444444444444"
	)
	beRel := kernel.PodSliceRelPath(beUID, kernel.QoSBestEffort, kernel.DriverSystemd)
	gaRel := kernel.PodSliceRelPath(gaUID, kernel.QoSGuaranteed, kernel.DriverSystemd)

	// 清掉上一次运行的残留，避免把"已被压制过的配额"当成原始配额
	cleanup(e, beRel)
	cleanup(e, gaRel)

	if err := e.EnsureDir(beRel, "cpuset", "cpu", "memory"); err != nil {
		rep.bad("创建 BestEffort Pod slice", err.Error())
		return
	}
	if err := e.EnsureDir(gaRel, "cpuset", "cpu", "memory"); err != nil {
		rep.bad("创建 Guaranteed Pod slice", err.Error())
		return
	}
	defer func() {
		cleanup(e, beRel)
		cleanup(e, gaRel)
		cleanup(e, "kubepods.slice/kubepods-besteffort.slice")
		cleanup(e, "kubepods.slice")
	}()

	if _, err := e.SetCPUQuota(beRel, 50000, kernel.DefaultPeriod); err != nil {
		rep.bad("初始化 BestEffort 配额", err.Error())
		return
	}
	if _, err := e.SetCPUQuota(gaRel, 100000, kernel.DefaultPeriod); err != nil {
		rep.bad("初始化 Guaranteed 配额", err.Error())
		return
	}

	sup := cgroup.NewSuppressorWithExecutor(0.20, e)
	n := sup.SyncFromCgroup(e)
	rep.check("从内核发现 Pod 分组", n >= 2, fmt.Sprintf("纳管 %d 个", n))

	readQuota := func(rel string) string {
		q, _, err := e.GetCPUQuota(rel)
		if err != nil {
			return "err:" + err.Error()
		}
		return strconv.FormatInt(q, 10)
	}

	sup.ReconcileByWatermark(0.30)
	rep.check("水位 30% 不压制", readQuota(beRel) == "50000", "quota="+readQuota(beRel))

	sup.ReconcileByWatermark(0.80)
	rep.check("水位 80% 压制到 20%", readQuota(beRel) == "10000", "quota="+readQuota(beRel))
	rep.check("Guaranteed 免压制", readQuota(gaRel) == "100000", "quota="+readQuota(gaRel))

	sup.ReconcileByWatermark(0.50)
	rep.check("水位 50% 恢复原配额", readQuota(beRel) == "50000", "quota="+readQuota(beRel))

	// 退出还原：模拟 Agent 收到 SIGTERM
	sup.ReconcileByWatermark(0.90)
	sup.RestoreAll()
	rep.check("退出前 RestoreAll 还原", readQuota(beRel) == "50000", "quota="+readQuota(beRel))
}

// ---------- 辅助 ----------

func pickCores(n int) []int {
	if n <= 1 {
		return []int{0}
	}
	half := n / 2
	if half < 1 {
		half = 1
	}
	out := make([]int, 0, n-half)
	for i := half; i < n; i++ {
		out = append(out, i)
	}
	return out
}

func joinByComma(cpus []int) string {
	parts := make([]string, 0, len(cpus))
	for _, c := range cpus {
		parts = append(parts, strconv.Itoa(c))
	}
	return strings.Join(parts, ",")
}

func parseList(s string) []int {
	cpus, err := kernel.ParseCPUList(s)
	if err != nil {
		return nil
	}
	return cpus
}

func rawCPUSet(e *kernel.Executor, rel string) string {
	s, _ := e.Read(rel, kernel.CPUSet)
	return s
}

func readTrimmed(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return "-"
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "(空)"
	}
	return s
}

func readAllowedCPUs(pid int) (string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "Cpus_allowed_list:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Cpus_allowed_list:")), nil
		}
	}
	return "", fmt.Errorf("未找到 Cpus_allowed_list")
}

func throttleCount(e *kernel.Executor, rel string) int64 {
	b, err := os.ReadFile(e.DirPath(rel, kernel.CPUQuota) + "/cpu.stat")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "nr_throttled ") {
			n, _ := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "nr_throttled ")), 10, 64)
			return n
		}
	}
	return 0
}

func spawnSleep(sec int) (int, error) {
	cmd := exec.Command("sleep", strconv.Itoa(sec))
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

func spawnBusy() (int, error) {
	cmd := exec.Command("sh", "-c", "while :; do :; done")
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

func killPID(pid int) {
	if pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// cleanup 把残留进程移回根 cgroup 后删除目录，避免污染节点。
func cleanup(e *kernel.Executor, rel string) {
	dir := e.DirPath(rel, kernel.CPUSet)
	if b, err := os.ReadFile(dir + "/cgroup.procs"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			_ = os.WriteFile(e.RootDir()+"/cgroup.procs", []byte(line), 0644)
		}
	}
	// 进程被杀后内核释放 cgroup 归属需要一点时间，立刻 rmdir 会失败（EBUSY）
	time.Sleep(200 * time.Millisecond)
	_ = os.Remove(dir)
}
