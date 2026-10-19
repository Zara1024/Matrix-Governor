echo "=== [TC-PROD-02 步骤 1] 查看中央控制器的准入拦截与 PID 自适应计算日志 ==="
kubectl logs -n matrix-system deployment/matrix-governor --tail 50 | grep -E 'NodeManager|Mutating' | tail -n 15
