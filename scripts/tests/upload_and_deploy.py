import paramiko

print("开始连接远程主机 REDACTED_HOST...")
ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect("REDACTED_HOST", port=22, username="root", password="REDACTED_PASSWORD")

sftp = ssh.open_sftp()
print("正在上传最新的 matrix-governor-linux (带熔断保持期与即时API回退机制)...")
sftp.put("Matrix-Governor/matrix-governor-linux", "/root/matrix-governor/matrix-governor-linux")
sftp.close()

cmd = """
set -e
cd /root/matrix-governor
cp -f matrix-governor-linux matrix-governor
chmod +x matrix-governor
docker build -t matrix-governor:v2.0.7 .
kind load docker-image matrix-governor:v2.0.7 --name matrix-cluster
kubectl set image deployment/matrix-governor governor=matrix-governor:v2.0.7 -n matrix-system
kubectl rollout status deployment/matrix-governor -n matrix-system --timeout=60s
kubectl get pods -n matrix-system
"""

print("正在构建并滚动更新 deployment/matrix-governor...")
stdin, stdout, stderr = ssh.exec_command(cmd)
print("=== BUILD & ROLLOUT OUTPUT ===")
print(stdout.read().decode('utf-8', errors='replace'))
err = stderr.read().decode('utf-8', errors='replace')
if err:
    print("=== STDERR ===")
    print(err)

ssh.close()
print("部署更新完成！")
