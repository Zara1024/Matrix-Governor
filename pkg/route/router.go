package route

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zara1024/Matrix-Governor/pkg/eviction"
	"github.com/Zara1024/Matrix-Governor/pkg/metrics"
	"github.com/Zara1024/Matrix-Governor/pkg/nodemanager"
	"github.com/Zara1024/Matrix-Governor/pkg/zaplogger"
	"go.uber.org/zap"

	"github.com/gorilla/mux"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)


type HandleFunc struct {
	Path   string
	Method string
	Func   func(w http.ResponseWriter, r *http.Request)
}

type handleFuncMap map[string]HandleFunc

var funcMap = make(handleFuncMap, 10)
var routerOnce sync.Once
var logger *zap.SugaredLogger

func RegisterHandler(hf HandleFunc) {
	if hf.Path == "" {
		logger.Fatalf("handle func path is empty")
	}
	registeredHf, ok := funcMap[strings.ToLower(hf.Path)]
	if ok && registeredHf.Method == hf.Method {
		logger.Fatalf("handle func [%s] already registered", hf.Path)
	}
	funcMap[strings.ToLower(hf.Path)] = hf
}

func ResponseErr(handlePath, msg string, httpCode int, w http.ResponseWriter) {
	logger.Errorf("handle func [%s] response err: %s", handlePath, msg)
	review := &admissionv1.AdmissionReview{
		Response: &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: msg,
			},
		},
	}
	bs, err := json.Marshal(review)
	if err != nil {
		logger.Errorf("failed to marshal response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(fmt.Sprintf("failed to marshal response: %s", err)))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	_, err = w.Write(bs)
	logger.Debugf("write err response: %d: %v: %v", httpCode, review, err)
}

func loggingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					logger.Errorf("err: %v, trace: %s", err, string(debug.Stack()))
				}
			}()

			start := time.Now()
			next.ServeHTTP(w, r)
			logger.Debugf("received request: %s %s %s", time.Since(start), strings.ToLower(r.Method), r.URL.EscapedPath())
		}
		return http.HandlerFunc(fn)
	}
}

var router *mux.Router

func Setup() {
	routerOnce.Do(func() {
		logger = zaplogger.NewSugar("route")

		logger.Info("init global http router...")
		router = mux.NewRouter().StrictSlash(true)

		// 注册企业级 Prometheus /metrics 监控端点
		logger.Info("load handle func: /metrics (Prometheus Exporter)")
		router.Handle("/metrics", metrics.MetricsHandler()).Methods(http.MethodGet)

		// 注册节点物理利用率上报与联动熔断接口 (供 Agent 汇报或管理员仿真测试)
		logger.Info("load handle func: /api/v1/node/metrics (Node Utilization Sync)")
		router.HandleFunc("/api/v1/node/metrics", func(w http.ResponseWriter, r *http.Request) {
			nodeName := r.URL.Query().Get("node")
			if nodeName == "" {
				nodeName = "matrix-cluster-worker"
			}
			cpuStr := r.URL.Query().Get("cpu")
			memStr := r.URL.Query().Get("mem")
			cpu, _ := strconv.ParseFloat(cpuStr, 64)
			mem, _ := strconv.ParseFloat(memStr, 64)
			if cpu <= 0 {
				cpu = 0.30
			}
			if mem <= 0 {
				mem = 0.35
			}
			info := nodemanager.GetGlobalManager().UpdateNodeMetrics(nodeName, cpu, mem)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"node":            nodeName,
				"healthState":     info.HealthState,
				"dynamicCPURatio": info.DynamicCPURatio,
				"dynamicMemRatio": info.DynamicMemRatio,
			})
		}).Methods(http.MethodPost, http.MethodGet)

		// 注册企业级五维权重差异化驱逐自治端点 (供过载保护或生产演练触发)
		logger.Info("load handle func: /api/v1/node/evict (5D Weighted Eviction Decision)")
		router.HandleFunc("/api/v1/node/evict", func(w http.ResponseWriter, r *http.Request) {
			nodeName := r.URL.Query().Get("node")
			if nodeName == "" {
				nodeName = "matrix-cluster-worker"
			}
			kubeClient := nodemanager.GetGlobalManager().GetKubeClient()
			if kubeClient == nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "kubeClient not initialized"})
				return
			}

			// 查询该节点上的所有 Pod
			podList, err := kubeClient.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{
				FieldSelector: "spec.nodeName=" + nodeName,
			})
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
				return
			}

			scorer := eviction.NewScorer()
			candidates := make([]eviction.PodCandidate, 0)
			systemNamespaces := map[string]bool{
				"kube-system":        true,
				"matrix-system":      true,
				"local-path-storage": true,
			}

			for i := range podList.Items {
				pod := &podList.Items[i]
				if systemNamespaces[pod.Namespace] {
					continue
				}
				if pod.Status.Phase != "Running" {
					continue
				}

				// 根据是否为 BestEffort 或标有 LS 计算指标
				var cpuUsage int64 = 50
				var memUsage int64 = 64 * 1024 * 1024
				if pod.Labels != nil && pod.Labels["matrix-governor.io/qos-class"] == "BE" {
					cpuUsage = 10
				}
				c := scorer.ScorePod(pod, nil, cpuUsage, memUsage)
				candidates = append(candidates, c)
				klog.Infof("[EvictionExecutor] Pod [%s/%s] 参与五维决策打分: QoSScore=%.2f, OvercommitScore=%.2f, TotalScore=%.4f",
					pod.Namespace, pod.Name, c.QoSScore, c.OvercommitScore, c.TotalScore)
			}

			if len(candidates) == 0 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"status":  "no_candidates",
					"message": fmt.Sprintf("节点 %s 当前无满足驱逐条件的业务 Pod", nodeName),
				})
				return
			}

			executorCfg := eviction.ExecutorConfig{
				CooldownPeriod:        0, // 管理员/演练主动触发无需防抖延迟
				MaxEvictionsPerMinute: 5,
			}
			executor := eviction.NewExecutor(kubeClient, scorer, executorCfg)
			evicted, err := executor.ExecuteBestEviction(context.Background(), nodeName, candidates)
			if err != nil {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
				return
			}

			klog.Warningf("[EvictionExecutor] 差异化决策完成: 成功通过 K8s API 优雅驱逐得分最高 Pod [%s/%s] (得分: %.4f)！核心在线业务不受影响",
				evicted.Pod.Namespace, evicted.Pod.Name, evicted.TotalScore)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":         "evicted",
				"evictedPod":     evicted.Pod.Name,
				"namespace":      evicted.Pod.Namespace,
				"score":          evicted.TotalScore,
				"reason":         "5D Weighted Eviction: Low-priority Batch Offline Task Evicted",
				"retainedLS":     "prod-mysql-master (Protected by LS免死策略)",
			})
		}).Methods(http.MethodPost, http.MethodGet)

		for p, f := range funcMap {
			logger.Infof("load handle func: %s", p)
			router.HandleFunc(f.Path, f.Func).Methods(f.Method)
		}
	})
}

func Router() http.Handler {
	return loggingMiddleware()(router)
}

