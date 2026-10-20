from ssh_conf import connect

ssh = connect()

cmd = """
echo "=== 1. 清理 default 命名空间测试负载与残留进程 ==="
kubectl delete deployment test-oversold-workload -n default --ignore-not-found
docker exec matrix-cluster-worker killall -9 stress-ng 2>/dev/null || true

echo "=== 2. 查看当前节点与控制器状态 ==="
kubectl get pods -A
kubectl get nodes matrix-cluster-worker -o custom-columns=NAME:.metadata.name,CPU_ALLOCATABLE:.status.allocatable.cpu,MEM_ALLOCATABLE:.status.allocatable.memory
"""

stdin, stdout, stderr = ssh.exec_command(cmd)
print(stdout.read().decode('utf-8', errors='replace'))
ssh.close()
