package hardware

import (
	"fmt"
	"sync"
)

// NUMANode 代表一个物理 Socket 对应的 NUMA 节点
type NUMANode struct {
	ID        int
	CPUs      []int
	TotalMem  int64 // 字节
	Allocated int64
}

// NUMAManager NUMA 拓扑感知与跨 Socket 内存总线延迟消除管理器
type NUMAManager struct {
	mu    sync.RWMutex
	nodes map[int]*NUMANode
}

// NewNUMAManager 初始化 NUMA 拓扑感知管理器 (默认探测或模拟双路双 Socket 服务器)
func NewNUMAManager() *NUMAManager {
	m := &NUMAManager{
		nodes: make(map[int]*NUMANode),
	}
	// 默认初始化 2 个 NUMA 节点 (典型 2路服务器拓扑)
	m.nodes[0] = &NUMANode{
		ID:       0,
		CPUs:     []int{0, 1, 2, 3},
		TotalMem: 16 * 1024 * 1024 * 1024, // 16GB
	}
	m.nodes[1] = &NUMANode{
		ID:       1,
		CPUs:     []int{4, 5, 6, 7},
		TotalMem: 16 * 1024 * 1024 * 1024, // 16GB
	}
	return m
}

// AllocateNUMANode 为延迟敏感型在线应用寻找亲和性最高的单一 NUMA 节点，杜绝跨 Socket 延迟
func (m *NUMAManager) AllocateNUMANode(coresNeeded int, memNeeded int64) (int, []int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, node := range m.nodes {
		if len(node.CPUs) >= coresNeeded && (node.TotalMem-node.Allocated) >= memNeeded {
			node.Allocated += memNeeded
			assignedCPUs := node.CPUs[:coresNeeded]
			return id, assignedCPUs, nil
		}
	}
	return -1, nil, fmt.Errorf("无满足条件的单一 NUMA 节点，无法提供完全无损跨 Socket 隔离")
}
