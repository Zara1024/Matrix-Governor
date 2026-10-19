echo "=== [TC-PROD-05 步骤 1] 彻底清理残留并混合部署双业务 Pod ==="
kubectl delete deployment test-oversold-workload -n default --now --wait=true --ignore-not-found 2>/dev/null || true
kubectl delete pod prod-mysql-master batch-offline-crawler -n default --now --wait=true --ignore-not-found 2>/dev/null || true

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
echo "=== [TC-PROD-05 步骤 2] 查验双业务 Pod 状态 (必须 100% 1/1 Running) ==="
kubectl get pods -l 'matrix-governor.io/qos-class' -o wide

echo "=== [TC-PROD-05 步骤 3] 触发 Matrix-Governor 五维差异化驱逐自治引擎决策 ==="
kubectl exec -n matrix-system deployment/matrix-governor -- curl -k -s "https://127.0.0.1:8443/api/v1/node/evict?node=matrix-cluster-worker"
echo ""

sleep 2
echo "=== 控制器打印的五维打分与决策日志 ==="
kubectl logs -n matrix-system deployment/matrix-governor --tail 25 | grep -E 'EvictionExecutor'

echo "=== [TC-PROD-05 步骤 4] 查验核心在线业务是否零损持续存活 ==="
kubectl get pods -n default
