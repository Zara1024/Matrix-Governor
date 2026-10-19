package metrics

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// OversoldRatioGauge 当前生效的节点动态超卖比 (CPU / 内存)
	OversoldRatioGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "matrix_governor",
			Subsystem: "node",
			Name:      "oversold_ratio",
			Help:      "Current dynamic oversold ratio applied to node allocatable (e.g. 1.0 to 2.0)",
		},
		[]string{"node", "resource"},
	)

	// NodeUtilizationGauge 节点真实物理使用率 (0.0 ~ 1.0)
	NodeUtilizationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "matrix_governor",
			Subsystem: "node",
			Name:      "actual_utilization",
			Help:      "Actual physical node resource utilization from Prometheus or cAdvisor (0.0 to 1.0)",
		},
		[]string{"node", "resource"},
	)

	// NodeStateGauge 节点运行健康状态 (0=Normal, 1=Warning, 2=Critical, 3=Fused)
	NodeStateGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "matrix_governor",
			Subsystem: "node",
			Name:      "health_state",
			Help:      "Current health state code of node (0:Normal, 1:Warning, 2:Critical, 3:Fused)",
		},
		[]string{"node"},
	)

	// EvictionTotalCounter 累计执行的差异化驱逐动作计数器
	EvictionTotalCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "matrix_governor",
			Subsystem: "eviction",
			Name:      "actions_total",
			Help:      "Total count of pods evicted by Matrix-Governor protection controller",
		},
		[]string{"node", "namespace", "qos", "reason"},
	)

	// PIDTermsGauge PID 控制器各项分量 (P, I, D) 与误差
	PIDTermsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "matrix_governor",
			Subsystem: "pid",
			Name:      "term_value",
			Help:      "Internal term values and error of PID controller for each node",
		},
		[]string{"node", "term"},
	)

	// FinOpsSavedCPUCoresGauge 当前动态超卖为集群额外创造的虚拟 CPU 核数
	FinOpsSavedCPUCoresGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "matrix_governor",
			Subsystem: "finops",
			Name:      "saved_cpu_cores",
			Help:      "Total virtual CPU cores gained via dynamic oversold",
		},
		[]string{"node"},
	)

	// FinOpsSavedMemGBGauge 当前动态超卖为集群额外创造的虚拟内存 (GB)
	FinOpsSavedMemGBGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "matrix_governor",
			Subsystem: "finops",
			Name:      "saved_memory_gb",
			Help:      "Total virtual Memory in GB gained via dynamic oversold",
		},
		[]string{"node"},
	)

	// FinOpsHourlySavingsGauge 预计当前每小时节省资金 (RMB)
	FinOpsHourlySavingsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "matrix_governor",
			Subsystem: "finops",
			Name:      "hourly_savings_rmb",
			Help:      "Estimated hourly cost savings in RMB based on cloud pricing",
		},
		[]string{"node"},
	)

	// FinOpsTotalSavingsGauge 累计节省资金总额 (RMB)
	FinOpsTotalSavingsGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "matrix_governor",
			Subsystem: "finops",
			Name:      "total_accumulated_savings_rmb",
			Help:      "Cumulative cost savings in RMB since Matrix-Governor started",
		},
	)

	initMetricsOnce sync.Once
)

// InitMetrics 注册所有 Prometheus 监控指标
func InitMetrics() {
	initMetricsOnce.Do(func() {
		prometheus.MustRegister(OversoldRatioGauge)
		prometheus.MustRegister(NodeUtilizationGauge)
		prometheus.MustRegister(NodeStateGauge)
		prometheus.MustRegister(EvictionTotalCounter)
		prometheus.MustRegister(PIDTermsGauge)
		prometheus.MustRegister(FinOpsSavedCPUCoresGauge)
		prometheus.MustRegister(FinOpsSavedMemGBGauge)
		prometheus.MustRegister(FinOpsHourlySavingsGauge)
		prometheus.MustRegister(FinOpsTotalSavingsGauge)
	})
}

// MetricsHandler 返回用于暴露 /metrics 端点的 HTTP Handler
func MetricsHandler() http.Handler {
	InitMetrics()
	return promhttp.Handler()
}

// RecordNodeStatus 更新指定节点的 Prometheus 指标
func RecordNodeStatus(nodeName string, cpuRatio, memRatio float64, cpuUtil, memUtil float64, stateCode float64) {
	InitMetrics()
	OversoldRatioGauge.WithLabelValues(nodeName, "cpu").Set(cpuRatio)
	OversoldRatioGauge.WithLabelValues(nodeName, "memory").Set(memRatio)
	NodeUtilizationGauge.WithLabelValues(nodeName, "cpu").Set(cpuUtil)
	NodeUtilizationGauge.WithLabelValues(nodeName, "memory").Set(memUtil)
	NodeStateGauge.WithLabelValues(nodeName).Set(stateCode)
}

// RecordEvictionAction 记录一次驱逐事件指标
func RecordEvictionAction(nodeName, namespace, qos, reason string) {
	InitMetrics()
	EvictionTotalCounter.WithLabelValues(nodeName, namespace, qos, reason).Inc()
}

// RecordFinOpsMetrics 更新 FinOps 财务降本指标
func RecordFinOpsMetrics(nodeName string, savedCores, savedMemGB, hourlySavings, totalSavings float64) {
	InitMetrics()
	FinOpsSavedCPUCoresGauge.WithLabelValues(nodeName).Set(savedCores)
	FinOpsSavedMemGBGauge.WithLabelValues(nodeName).Set(savedMemGB)
	FinOpsHourlySavingsGauge.WithLabelValues(nodeName).Set(hourlySavings)
	FinOpsTotalSavingsGauge.Set(totalSavings)
}

