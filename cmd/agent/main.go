package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Zara1024/Matrix-Governor/pkg/agent"
	"k8s.io/klog/v2"
)

func main() {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		nodeName, _ = os.Hostname()
	}

	cfg := agent.Config{
		NodeName:            nodeName,
		SyncInterval:        2 * time.Second, // 单机高频 2 秒一次微闭环
		CPUWarningThreshold: 0.75,
		EnableRDT:           true,
		EnableKernelQoS:     true,
	}

	matrixAgent := agent.NewMatrixAgent(cfg)
	matrixAgent.Start(context.Background())

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	klog.Info("正在优雅停止 Matrix-Agent 单机守护进程...")
	matrixAgent.Stop()
}
