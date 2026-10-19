echo "=== [TC-PROD-01 步骤 1] 为靶场节点打上超卖治理标签 ==="
kubectl label node matrix-cluster-worker oversold=oversold matrix-governor.io/oversold=enabled --overwrite

echo "=== [TC-PROD-01 步骤 2] 触发节点容量上报并由 Webhook 拦截注入 ==="
docker exec matrix-cluster-worker systemctl restart kubelet
sleep 4

echo "=== [TC-PROD-01 步骤 3] 查看节点 Allocatable 容量动态放大 ==="
kubectl get node matrix-cluster-worker -o jsonpath='Capacity: {.status.capacity.cpu}, Allocatable: {.status.allocatable.cpu}{"\n"}'

echo "=== [TC-PROD-01 步骤 4] 提交 5 副本 1 核业务负载（总计需 5 核，突破物理 4 核极限） ==="
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
              cpu: "1000m"
              memory: "256Mi"
EOF

sleep 5
echo "=== [TC-PROD-01 步骤 5] 查验 5 个 Pod 调度运行状态 ==="
kubectl get pods -l app=test-oversold -o wide
