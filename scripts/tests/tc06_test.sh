echo "=== [TC-PROD-06 步骤 1] 部署 5 副本在线核心微服务并配置 PDB 防御规则 ==="
cat << 'EOF' | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-oversold-workload
  namespace: default
spec:
  replicas: 5
  selector:
    matchLabels:
      app: test-oversold
  template:
    metadata:
      labels:
        app: test-oversold
    spec:
      nodeName: matrix-cluster-worker
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.9
          resources:
            requests:
              cpu: "100m"
              memory: "64Mi"
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: test-oversold-workload-pdb
  namespace: default
spec:
  minAvailable: 4
  selector:
    matchLabels:
      app: test-oversold
EOF

echo "=== 等待 5 副本全部平稳启动 Ready ==="
kubectl rollout status deployment test-oversold-workload -n default --timeout=30s

echo "=== [TC-PROD-06 步骤 2] 查验 PDB 生效状态与允许中断数 (必须 ALLOWED DISRUPTIONS: 1) ==="
kubectl get pdb test-oversold-workload-pdb -n default
