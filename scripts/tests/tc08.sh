echo "=== [TC-PROD-08 步骤 1] 获取 Matrix-Governor 内部 ClusterIP ==="
SVC_IP=$(kubectl get svc -n matrix-system matrix-governor-svc -o jsonpath='{.spec.clusterIP}')
echo "Matrix-Governor Service IP: $SVC_IP"

echo ""
echo "=== [TC-PROD-08 步骤 2] 向 /metrics 端点抓取 FinOps 与混部超卖核心指标 ==="
docker exec matrix-cluster-control-plane curl -k -s https://$SVC_IP/metrics | grep -E '^matrix_governor_'
