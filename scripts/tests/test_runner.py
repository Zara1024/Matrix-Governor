import paramiko

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect("REDACTED_HOST", port=22, username="root", password="REDACTED_PASSWORD")

cmd = """
echo "=== 1. 向控制器端点上报节点过载 (88% CPU) ==="
kubectl exec -n matrix-system deployment/matrix-governor -- curl -k -s "https://127.0.0.1:8443/api/v1/node/metrics?node=matrix-cluster-worker&cpu=0.88"
echo ""

echo "=== 2. 查看控制器最新日志 (确认硬熔断日志) ==="
kubectl logs -n matrix-system deployment/matrix-governor --tail=15

echo "=== 3. 查看节点当前 Allocatable 是否已降级回退到原生物理极限 ==="
kubectl get nodes matrix-cluster-worker -o custom-columns=NAME:.metadata.name,CPU_ALLOCATABLE:.status.allocatable.cpu,MEM_ALLOCATABLE:.status.allocatable.memory
"""

stdin, stdout, stderr = ssh.exec_command(cmd)
print(stdout.read().decode('utf-8', errors='replace'))
print(stderr.read().decode('utf-8', errors='replace'))
ssh.close()
