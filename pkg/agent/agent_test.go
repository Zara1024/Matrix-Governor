package agent

import (
	"context"
	"testing"
	"time"
)

func TestMatrixAgent(t *testing.T) {
	cfg := Config{
		NodeName:            "worker-test-node",
		SyncInterval:        50 * time.Millisecond,
		CPUWarningThreshold: 0.75,
		EnableRDT:           true,
		EnableKernelQoS:     true,
	}

	ag := NewMatrixAgent(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ag.Start(ctx)
	ag.HandlePodCreated("pod-test-1", "Guaranteed", 4*1024*1024*1024)
	ag.HandlePodCreated("pod-test-2", "BestEffort", 4*1024*1024*1024)

	time.Sleep(100 * time.Millisecond)
	ag.Stop()
}
