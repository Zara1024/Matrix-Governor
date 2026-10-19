package kernel

import (
	"testing"
)

func TestKernelComponents(t *testing.T) {
	// 1. 测试内核级 QoS (内存/IO/抢占)
	kqos := NewKernelQoSController()
	onlineSetting := kqos.ConfigurePodKernelQoS("pod-prod-db", "Guaranteed", 8*1024*1024*1024)
	offlineSetting := kqos.ConfigurePodKernelQoS("pod-offline-hadoop", "BestEffort", 8*1024*1024*1024)

	if onlineSetting.IOWeight != 1000 || onlineSetting.BVTPreemptWarp <= 0 {
		t.Fatalf("在线业务内核设置不正确: IO=%d, BVT=%d", onlineSetting.IOWeight, onlineSetting.BVTPreemptWarp)
	}
	if offlineSetting.IOWeight != 100 || offlineSetting.MemoryHighBytes >= 8*1024*1024*1024 {
		t.Fatalf("离线任务内核设置不正确: IO=%d, High=%d", offlineSetting.IOWeight, offlineSetting.MemoryHighBytes)
	}

	// 2. 测试网络 TC 带宽限速
	tc := NewNetworkTCManager()
	onlineNet := tc.ApplyBandwidthLimit("pod-prod-db", "Guaranteed")
	offlineNet := tc.ApplyBandwidthLimit("pod-offline-hadoop", "BestEffort")

	if onlineNet.PriorityQueue != 0 || onlineNet.EgressRateMbps != 10000 {
		t.Fatalf("在线网络队列不正确: %v", onlineNet)
	}
	if offlineNet.PriorityQueue != 2 || offlineNet.EgressRateMbps != 100 {
		t.Fatalf("离线网络限速不正确: %v", offlineNet)
	}
}
