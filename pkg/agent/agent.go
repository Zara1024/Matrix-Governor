package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zara1024/Matrix-Governor/pkg/cgroup"
	"github.com/Zara1024/Matrix-Governor/pkg/hardware"
	"github.com/Zara1024/Matrix-Governor/pkg/kernel"
	"k8s.io/klog/v2"
)

// Config 单机 Agent 配置
type Config struct {
	NodeName            string
	SyncInterval        time.Duration
	CPUWarningThreshold float64
	EnableRDT           bool
	EnableKernelQoS     bool
	// CgroupRoot 为空时使用 /sys/fs/cgroup
	CgroupRoot string
	// Driver 是 kubelet 的 cgroup 驱动方式，决定 Pod cgroup 路径形态
	Driver kernel.Driver
}

// MatrixAgent 单机自治守护 Agent (对标 Koord-let / Katalyst-Agent)
type MatrixAgent struct {
	mu         sync.RWMutex
	config     Config
	exec       *kernel.Executor
	suppressor *cgroup.Suppressor
	rdtMgr     *hardware.RDTManager
	kernelQoS  *kernel.KernelQoSController
	stopCh     chan struct{}
	ticks      uint64
}

// NewMatrixAgent 创建单机 Agent
func NewMatrixAgent(cfg Config) *MatrixAgent {
	if cfg.Driver == "" {
		cfg.Driver = kernel.DriverSystemd
	}
	exec := kernel.NewExecutor(cfg.CgroupRoot)
	return &MatrixAgent{
		config:     cfg,
		exec:       exec,
		suppressor: cgroup.NewSuppressorWithExecutor(0.20, exec),
		rdtMgr:     hardware.NewRDTManager(hardware.DefaultRDTConfig()),
		kernelQoS:  kernel.NewKernelQoSController(),
		stopCh:     make(chan struct{}),
	}
}

// Start 启动单机高频高精度自治控制闭环
func (a *MatrixAgent) Start(ctx context.Context) {
	ticker := time.NewTicker(a.config.SyncInterval)
	klog.Infof("[MatrixAgent] 单机自治保护 Agent 已启动在节点: %s (巡检周期: %v)", a.config.NodeName, a.config.SyncInterval)
	klog.Infof("[MatrixAgent] 内核写入通道: cgroup %s, 驱动 %s, resctrl 可用=%v",
		a.exec.Version(), a.config.Driver, a.rdtMgr.Supported())
	if !a.rdtMgr.Supported() {
		klog.Infof("[MatrixAgent] RDT 降级: %s", a.rdtMgr.UnsupportedReason())
	}

	go func() {
		for {
			select {
			case <-ticker.C:
				a.reconcileLocalNode()
			case <-a.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// Stop 停止 Agent。
// 必须先把被压制的 Pod 恢复到原始配额：kubelet 只在容器重建时才重算 cgroup 配置，
// Agent 退出时如果不还原，Pod 会一直卡在 20% 配额上，且没人会去修正。
func (a *MatrixAgent) Stop() {
	a.suppressor.RestoreAll()
	close(a.stopCh)
}

var (
	lastSampleIdle  uint64
	lastSampleTotal uint64
	sampleMu        sync.Mutex
)

// getLocalCPUUsage 真实读取 Linux 内核 /proc/stat 计算瞬时利用率
func getLocalCPUUsage() float64 {
	sampleMu.Lock()
	defer sampleMu.Unlock()

	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0.30
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 8 {
				return 0.30
			}
			var user, nice, sys, idle, iowait, irq, softirq, steal uint64
			user, _ = strconv.ParseUint(fields[1], 10, 64)
			nice, _ = strconv.ParseUint(fields[2], 10, 64)
			sys, _ = strconv.ParseUint(fields[3], 10, 64)
			idle, _ = strconv.ParseUint(fields[4], 10, 64)
			iowait, _ = strconv.ParseUint(fields[5], 10, 64)
			irq, _ = strconv.ParseUint(fields[6], 10, 64)
			softirq, _ = strconv.ParseUint(fields[7], 10, 64)
			if len(fields) > 8 {
				steal, _ = strconv.ParseUint(fields[8], 10, 64)
			}
			curIdle := idle + iowait
			curTotal := user + nice + sys + idle + iowait + irq + softirq + steal
			if lastSampleTotal == 0 {
				lastSampleIdle = curIdle
				lastSampleTotal = curTotal
				return 0.30
			}
			deltaTotal := curTotal - lastSampleTotal
			deltaIdle := curIdle - lastSampleIdle
			lastSampleIdle = curIdle
			lastSampleTotal = curTotal
			if deltaTotal <= 0 {
				return 0.30
			}
			usage := float64(deltaTotal-deltaIdle) / float64(deltaTotal)
			if usage < 0 {
				usage = 0
			}
			if usage > 1.0 {
				usage = 1.0
			}
			return usage
		}
	}
	return 0.30
}

// reconcileLocalNode 单机毫秒级闭环探测与物理隔离执行
func (a *MatrixAgent) reconcileLocalNode() {
	a.mu.Lock()
	defer a.mu.Unlock()

	currentLocalCPU := getLocalCPUUsage()

	if currentLocalCPU >= 0.80 {
		klog.Warningf("[MatrixAgent] 警告: 节点 %s 物理利用率达到极限 (CPU: %.2f%%)！触发单机硬熔断保护与CFS压制！",
			a.config.NodeName, currentLocalCPU*100)
		// 异步向中央控制器同步熔断状态
		go func(node string, cpu float64) {
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
			client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
			u := fmt.Sprintf("https://matrix-governor-svc.matrix-system.svc:443/api/v1/node/metrics?node=%s&cpu=%.4f&mem=0.35", node, cpu)
			resp, err := client.Post(u, "application/json", nil)
			if err == nil && resp != nil {
				_ = resp.Body.Close()
			}
		}(a.config.NodeName, currentLocalCPU)
	} else if currentLocalCPU >= 0.70 {
		klog.Infof("[MatrixAgent] 节点 %s CPU水位进入警戒区 (当前: %.2f%%)，执行 Cgroup 毫秒级微压制",
			a.config.NodeName, currentLocalCPU*100)
	}

	// 每 15 个巡检周期（默认 30 秒）与内核对齐一次账本：
	// kubelet 重启、Pod 重建、手工改过 cgroup 都会让内存账本失真，
	// 只有重新扫一遍内核才知道真实配额是多少。
	a.ticks++
	if a.ticks%15 == 1 {
		a.suppressor.SyncFromCgroup(a.exec)
	}

	a.suppressor.ReconcileByWatermark(currentLocalCPU)
}

// HandlePodCreated 当单机感知到新容器创建时，执行单机硬件分配与内核 QoS 赋权。
func (a *MatrixAgent) HandlePodCreated(podUID, qosClass string, memLimitBytes int64) {
	a.HandlePodCreatedWithPID(podUID, qosClass, memLimitBytes, 0)
}

// HandlePodCreatedWithPID 是带进程号的版本，能把容器主进程真正放进 RDT CLOS 组。
func (a *MatrixAgent) HandlePodCreatedWithPID(podUID, qosClass string, memLimitBytes int64, pid int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.config.EnableRDT {
		if _, err := a.rdtMgr.ApplyForPod(podUID, qosClass, pid); err != nil {
			// RDT 属于增强项：拿不到 resctrl 就降级，不能阻塞主流程
			klog.V(2).Infof("[MatrixAgent] Pod %s RDT 分配跳过: %v", podUID, err)
		}
	}
	if a.config.EnableKernelQoS {
		a.kernelQoS.ConfigurePodKernelQoS(podUID, qosClass, memLimitBytes)
	}
}
