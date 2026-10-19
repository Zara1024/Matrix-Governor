echo "=== [TC-PROD-07 步骤 1] 为靶场节点追加 Batch 扩展资源模型标签 ==="
kubectl label node matrix-cluster-worker matrix-governor.io/resource-model=batch --overwrite

echo "=== [TC-PROD-07 步骤 2] 刷新状态上报，触发 Webhook 注入扩展资源 ==="
docker exec matrix-cluster-worker systemctl restart kubelet
sleep 5

echo "=== [TC-PROD-07 步骤 3] 查验节点 Allocatable 中注册的 batch-cpu 与 batch-memory ==="
kubectl describe node matrix-cluster-worker | grep -E 'batch-cpu|batch-memory'
