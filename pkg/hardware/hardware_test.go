package hardware

import (
	"testing"
)

func TestHardwareComponents(t *testing.T) {
	// 1. 测试 Intel RDT 硬件隔离
	rdt := NewRDTManager(DefaultRDTConfig())
	onlineCLOS := rdt.AssignCLOS("pod-online-mysql", "Guaranteed")
	offlineCLOS := rdt.AssignCLOS("pod-offline-spark", "BestEffort")
	if onlineCLOS != "CLOS_ONLINE" || offlineCLOS != "CLOS_OFFLINE" {
		t.Fatalf("RDT 分组分配异常: online=%s, offline=%s", onlineCLOS, offlineCLOS)
	}
	mask, mba := rdt.GetGroupConfig(offlineCLOS)
	if mask != "0x3" || mba != 20 {
		t.Fatalf("RDT 离线组硬件限额不正确: mask=%s, mba=%d", mask, mba)
	}

	// 2. 测试 NUMA 拓扑绑核分配
	numa := NewNUMAManager()
	nodeID, cpus, err := numa.AllocateNUMANode(2, 4*1024*1024*1024)
	if err != nil || nodeID < 0 || len(cpus) != 2 {
		t.Fatalf("NUMA 分配失败: node=%d, cpus=%v, err=%v", nodeID, cpus, err)
	}

	// 3. 测试 GPU 显存切分与算力借还
	gpu := NewGPUGovernor()
	err = gpu.AllocateVGPU("GPU-0", 20480, true)
	if err != nil {
		t.Fatalf("GPU 分配显存失败: %v", err)
	}
	lent, msg := gpu.ReconcileGPUOvercommit("GPU-0", 0)
	if !lent {
		t.Fatalf("QPS=0 时应出借算力: %s", msg)
	}
	reclaimed, msg := gpu.ReconcileGPUOvercommit("GPU-0", 50)
	if reclaimed {
		t.Fatalf("QPS>0 时应回收算力: %s", msg)
	}
}
