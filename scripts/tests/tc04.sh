echo "=== [TC-PROD-04 步骤 1] 查看压测前的节点超卖放大状态 ==="
kubectl get nodes matrix-cluster-worker -o custom-columns=NAME:.metadata.name,CPU_ALLOCATABLE:.status.allocatable.cpu,MEM_ALLOCATABLE:.status.allocatable.memory

echo ""
echo "=== [TC-PROD-04 步骤 2] 触发 80% 安全红线硬熔断 (方式B 端点直调 + 方式A 物理压测双保底) ==="
docker exec -d matrix-cluster-worker stress-ng --cpu 4 --cpu-load 85% --timeout 30s 2>/dev/null || true
kubectl exec -n matrix-system deployment/matrix-governor -- curl -k -s "https://127.0.0.1:8443/api/v1/node/metrics?node=matrix-cluster-worker&cpu=0.88"
echo ""

sleep 2
echo "=== [TC-PROD-04 步骤 3] 在控制器日志中观察安全熔断防御触发与 API 零延迟回退 ==="
kubectl logs -n matrix-system deployment/matrix-governor --tail 15

echo ""
echo "=== [TC-PROD-04 步骤 4] 实地验证 Node Allocatable 已瞬间降级回退为物理 4 核极限 ==="
kubectl get nodes matrix-cluster-worker -o custom-columns=NAME:.metadata.name,CPU_ALLOCATABLE:.status.allocatable.cpu,MEM_ALLOCATABLE:.status.allocatable.memory

echo ""
echo "=== [TC-PROD-04 步骤 5] 清理压测进程 ==="
docker exec matrix-cluster-worker killall -9 stress-ng 2>/dev/null || true
