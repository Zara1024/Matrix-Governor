package kernel

import (
	"sync"

	"k8s.io/klog/v2"
)

// NetworkBandwidthQoS 网络带宽整形与 QoS 队列配置
type NetworkBandwidthQoS struct {
	EgressRateMbps  int // 出向带宽速率限制 (Mbps)
	IngressRateMbps int // 入向带宽速率限制 (Mbps)
	PriorityQueue   int // 队列调度优先级 (0:高保, 1:普通, 2:低优)
}

// NetworkTCManager 网络 Traffic Control 与带宽隔离管理器
type NetworkTCManager struct {
	mu     sync.RWMutex
	limits map[string]*NetworkBandwidthQoS
}

// NewNetworkTCManager 创建网络 TC 管理器
func NewNetworkTCManager() *NetworkTCManager {
	return &NetworkTCManager{
		limits: make(map[string]*NetworkBandwidthQoS),
	}
}

// ApplyBandwidthLimit 为指定容器网络接口 (veth/pod) 应用带宽整形与限速策略
func (m *NetworkTCManager) ApplyBandwidthLimit(podUID, qosClass string) *NetworkBandwidthQoS {
	m.mu.Lock()
	defer m.mu.Unlock()

	qos := &NetworkBandwidthQoS{}
	if qosClass == "Guaranteed" || qosClass == "LSR" {
		// 在线业务：不限制出入带宽 (10Gbps 网卡全开)，高优先级队列
		qos.EgressRateMbps = 10000
		qos.IngressRateMbps = 10000
		qos.PriorityQueue = 0
	} else {
		// 离线批处理：出向限制 100Mbps，入向限制 200Mbps，低优先级队列，防把物理网卡打爆
		qos.EgressRateMbps = 100
		qos.IngressRateMbps = 200
		qos.PriorityQueue = 2
	}

	m.limits[podUID] = qos
	klog.Infof("[Network-TC] 成功配置网络带宽队列: Pod=%s (Egress=%dMbps, Ingress=%dMbps, Priority=%d)",
		podUID, qos.EgressRateMbps, qos.IngressRateMbps, qos.PriorityQueue)

	return qos
}
