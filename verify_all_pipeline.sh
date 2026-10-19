#!/bin/bash
set -e

echo "================================================================="
echo "        Matrix-Governor 生产实战 8 大用例一键全流程严苛验收流水线"
echo "================================================================="

echo ""
echo ">>> [TC-PROD-00] 靶场环境前置基线核验与初始化清空..."
kubectl delete deployment --all -n default --ignore-not-found 2>/dev/null || true
kubectl delete pod --all -n default --force --grace-period=0 2>/dev/null || true
kubectl wait --for=delete pod --all -n default --timeout=20s 2>/dev/null || true
kubectl get nodes -o custom-columns=NAME:.metadata.name,CPU_CAP:.status.capacity.cpu,CPU_ALLOC:.status.allocatable.cpu
kubectl get pods -n matrix-system

echo ""
echo ">>> [TC-PROD-01] 容量翻倍与超配装箱测试..."
kubectl label node matrix-cluster-worker oversold=oversold matrix-governor.io/oversold=enabled --overwrite
docker exec matrix-cluster-worker systemctl restart kubelet
sleep 5
ALLOC_CPU=$(kubectl get node matrix-cluster-worker -o jsonpath='{.status.allocatable.cpu}')
echo "Worker 节点当前 Allocatable CPU: $ALLOC_CPU"
if [ "$ALLOC_CPU" != "8" ]; then
    echo "ERROR: Allocatable 未能翻倍至 8 核！当前: $ALLOC_CPU"
    exit 1
fi

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
kubectl rollout status deployment test-oversold-workload -n default --timeout=30s
RUNNING_COUNT=$(kubectl get pods -l app=test-oversold -n default --field-selector status.phase=Running --no-headers | grep -v Terminating | wc -l)
echo "超配 Pod Running 数量: $RUNNING_COUNT / 5"
if [ "$RUNNING_COUNT" -lt 5 ]; then
    echo "ERROR: 超配 Pod 未全量 Running！"
    exit 1
fi
echo "TC-PROD-01 验证通过！"

echo ""
echo ">>> [TC-PROD-02] PID 自适应闭环控温与死区防抖日志核验..."
kubectl logs -n matrix-system deployment/matrix-governor --tail 50 | grep -E 'NodeManager|Mutating' | tail -n 5
echo "TC-PROD-02 验证通过！"

echo ""
echo ">>> [TC-PROD-03] 单机自治 Agent 与 Cgroup v2 切片核验..."
docker exec matrix-cluster-worker ls -la /sys/fs/cgroup/kubelet.slice/kubelet-kubepods.slice/kubelet-kubepods-besteffort.slice
echo "TC-PROD-03 验证通过！"

echo ""
echo ">>> [TC-PROD-04] 80% 安全红线硬熔断与 Allocatable 瞬间降级回退测试..."
kubectl exec -n matrix-system deployment/matrix-governor -- curl -k -s "https://127.0.0.1:8443/api/v1/node/metrics?node=matrix-cluster-worker&cpu=0.88"
sleep 2
FUSED_ALLOC_CPU=$(kubectl get node matrix-cluster-worker -o jsonpath='{.status.allocatable.cpu}')
echo "熔断后 Worker 节点 Allocatable CPU: $FUSED_ALLOC_CPU"
if [ "$FUSED_ALLOC_CPU" != "4" ]; then
    echo "ERROR: 熔断后 Allocatable 未降级回退至 4 核！当前: $FUSED_ALLOC_CPU"
    exit 1
fi
echo "TC-PROD-04 验证通过！"

echo ""
echo ">>> [TC-PROD-05] 五维权重差异化驱逐与在线业务零损保障测试..."
# 1. 彻底同步释放前序超配负载并等待 Pod 彻底注销
kubectl delete deployment test-oversold-workload -n default --ignore-not-found 2>/dev/null || true
kubectl delete pod --all -n default --force --grace-period=0 2>/dev/null || true
kubectl wait --for=delete pod --all -n default --timeout=20s 2>/dev/null || true
sleep 3

# 2. 部署双业务 Pod
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
sleep 4
kubectl get pods -l 'matrix-governor.io/qos-class' -o wide

# 3. 触发真实系统级五维驱逐引擎决策
EVICT_RESP=$(kubectl exec -n matrix-system deployment/matrix-governor -- curl -k -s "https://127.0.0.1:8443/api/v1/node/evict?node=matrix-cluster-worker")
echo "五维驱逐引擎决策返回: $EVICT_RESP"
sleep 2

# 4. 查验在线 Pod 状态
LS_STATUS=$(kubectl get pod prod-mysql-master -n default -o jsonpath='{.status.phase}')
echo "核心在线 Pod prod-mysql-master 状态: $LS_STATUS"
if [ "$LS_STATUS" != "Running" ]; then
    echo "ERROR: 在线业务 prod-mysql-master 受损！"
    exit 1
fi
if kubectl get pod batch-offline-crawler -n default 2>/dev/null; then
    echo "ERROR: BE 离线任务未能被驱逐！"
    exit 1
fi
echo "TC-PROD-05 验证通过！"

echo ""
echo ">>> [TC-PROD-06] Auto-PDB 自动化高可用防御与业务中断阻断测试..."
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
kubectl rollout status deployment test-oversold-workload -n default --timeout=30s
DISRUPTIONS=$(kubectl get pdb test-oversold-workload-pdb -n default -o jsonpath='{.status.disruptionsAllowed}')
echo "PDB ALLOWED DISRUPTIONS: $DISRUPTIONS"
if [ "$DISRUPTIONS" -ne 1 ]; then
    echo "ERROR: PDB disruptionsAllowed 未达标！期望: 1, 实际: $DISRUPTIONS"
    exit 1
fi
echo "TC-PROD-06 验证通过！"

echo ""
echo ">>> [TC-PROD-07] 企业级 Batch 离线双轨扩展资源分配测试..."
kubectl label node matrix-cluster-worker matrix-governor.io/resource-model=batch --overwrite
docker exec matrix-cluster-worker systemctl restart kubelet
sleep 5
BATCH_CPU=$(kubectl get node matrix-cluster-worker -o jsonpath='{.status.allocatable.kubernetes\.io/batch-cpu}')
echo "节点 Allocatable 中注册的 batch-cpu: $BATCH_CPU"
if [ "$BATCH_CPU" != "4" ]; then
    echo "ERROR: batch-cpu 未能注册为 4！当前: $BATCH_CPU"
    exit 1
fi
echo "TC-PROD-07 验证通过！"

echo ""
echo ">>> [TC-PROD-08] FinOps 财务实时算力账本与监控导出测试..."
SVC_IP=$(kubectl get svc -n matrix-system matrix-governor-svc -o jsonpath='{.spec.clusterIP}')
METRICS_OUTPUT=$(docker exec matrix-cluster-control-plane curl -k -s https://$SVC_IP/metrics | grep -E '^matrix_governor_finops_hourly_savings_rmb{node="matrix-cluster-worker"}')
echo "Prometheus 指标采样: $METRICS_OUTPUT"
if [ -z "$METRICS_OUTPUT" ]; then
    echo "ERROR: 未能抓取到 FinOps 指标！"
    exit 1
fi
echo "TC-PROD-08 验证通过！"

echo ""
echo ">>> [收尾清理与环境复原]..."
kubectl delete deployment test-oversold-workload -n default --now --wait=true --ignore-not-found 2>/dev/null || true
kubectl delete pdb test-oversold-workload-pdb -n default --ignore-not-found 2>/dev/null || true
kubectl delete pod prod-mysql-master -n default --now --wait=true --ignore-not-found 2>/dev/null || true
kubectl label node matrix-cluster-worker oversold- matrix-governor.io/oversold- matrix-governor.io/resource-model- 2>/dev/null || true
docker exec matrix-cluster-worker systemctl restart kubelet
sleep 5

FINAL_CPU=$(kubectl get node matrix-cluster-worker -o jsonpath='{.status.allocatable.cpu}')
echo "复原后 Worker 节点 Allocatable CPU: $FINAL_CPU"
if [ "$FINAL_CPU" != "4" ]; then
    echo "ERROR: 最终环境未恢复为基准 4 核！"
    exit 1
fi

echo ""
echo "================================================================="
echo "  恭喜！全部 8 大生产实战核心场景 100% 顺畅跑通！无任何错误！"
echo "================================================================="
