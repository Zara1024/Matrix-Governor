echo "=== [TC-PROD-05 步骤 1] 混合部署在线 Pod (LS) 与离线批处理 Pod (BE) ==="
cat << 'EOF' | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: prod-mysql-master
  namespace: default
  labels:
    app: prod-mysql
    matrix-governor.io/qos-class: LS
spec:
  nodeName: matrix-cluster-worker
  containers:
    - name: mysql
      image: registry.k8s.io/pause:3.9
      resources:
        requests:
          cpu: "200m"
---
apiVersion: v1
kind: Pod
metadata:
  name: batch-offline-crawler
  namespace: default
  labels:
    app: batch-crawler
    matrix-governor.io/qos-class: BE
spec:
  nodeName: matrix-cluster-worker
  containers:
    - name: crawler
      image: registry.k8s.io/pause:3.9
      resources:
        requests:
          cpu: "200m"
EOF

sleep 3
echo "=== [TC-PROD-05 步骤 2] 查验双业务 Pod 运行状态 ==="
kubectl get pods -l 'matrix-governor.io/qos-class' -o wide

echo "=== [TC-PROD-05 步骤 3] 模拟基于五维权重决策执行对 BE 任务的驱逐 ==="
kubectl delete pod batch-offline-crawler -n default --now

sleep 2
echo "=== [TC-PROD-05 步骤 4] 查验核心在线业务是否零损持续存活 ==="
kubectl get pods -n default
