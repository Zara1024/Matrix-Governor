echo "=== [TC-PROD-03 步骤 1] 查看运行在靶场节点上的 matrix-agent 实时巡检状态 ==="
AGENT_POD=$(kubectl get pods -n matrix-system -l app=matrix-agent --field-selector spec.nodeName=matrix-cluster-worker -o jsonpath='{.items[0].metadata.name}')
kubectl logs -n matrix-system $AGENT_POD --tail 20

echo ""
echo "=== [TC-PROD-03 步骤 2] 查看靶场节点内核 Cgroup CPU 配额管理接口 (Cgroup v2 架构) ==="
docker exec matrix-cluster-worker ls -la /sys/fs/cgroup/kubelet.slice/kubelet-kubepods.slice/
