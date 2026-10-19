package metrics

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// PrometheusCollector 生产级真实 Prometheus 指标采集器
type PrometheusCollector struct {
	mu           sync.RWMutex
	promEndpoint string
	httpClient   *http.Client
	cache        map[string]NodeMetrics
}

// PromQueryResponse Prometheus HTTP API 响应格式
type PromQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"` // [timestamp, "valueString"]
		} `json:"result"`
	} `json:"data"`
}

// NewPrometheusCollector 创建 Prometheus 真实采集器
func NewPrometheusCollector(promEndpoint string) *PrometheusCollector {
	if promEndpoint == "" {
		promEndpoint = "http://prometheus-k8s.monitoring.svc:9090"
	}
	return &PrometheusCollector{
		promEndpoint: promEndpoint,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		cache: make(map[string]NodeMetrics),
	}
}

// GetNodeMetrics 查询指定节点的物理利用率指标 (优先走真实 PromQL，故障时走自愈缓存)
func (p *PrometheusCollector) GetNodeMetrics(nodeName string) (NodeMetrics, error) {
	// 1. 查询 CPU 真实利用率 (过去 2 分钟内的真实活跃计算占比)
	cpuQuery := fmt.Sprintf(`100 - (avg(irate(node_cpu_seconds_total{mode="idle", instance=~".*%s.*"}[2m])) * 100)`, nodeName)
	cpuVal, err := p.querySingleValue(cpuQuery)
	if err != nil {
		klog.V(3).Infof("[PrometheusCollector] 采集节点 %s CPU 指标失败, 启用缓存兜底: %v", nodeName, err)
		return p.getFromCache(nodeName), nil
	}

	// 2. 查询内存真实利用率 (基于实际可用内存计算)
	memQuery := fmt.Sprintf(`(1 - (node_memory_MemAvailable_bytes{instance=~".*%s.*"} / node_memory_MemTotal_bytes{instance=~".*%s.*"})) * 100`, nodeName, nodeName)
	memVal, err := p.querySingleValue(memQuery)
	if err != nil {
		memVal = 30.0 // 默认保底
	}

	result := NodeMetrics{
		CPUUsageRatio: cpuVal / 100.0,
		MemUsageRatio: memVal / 100.0,
	}

	// 存入本地健康缓存
	p.mu.Lock()
	p.cache[nodeName] = result
	p.mu.Unlock()

	return result, nil
}

func (p *PrometheusCollector) querySingleValue(promQL string) (float64, error) {
	u := fmt.Sprintf("%s/api/v1/query?query=%s", p.promEndpoint, url.QueryEscape(promQL))
	resp, err := p.httpClient.Get(u)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus 返回非 200 状态码: %d", resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var promResp PromQueryResponse
	if err := json.Unmarshal(body, &promResp); err != nil {
		return 0, err
	}

	if promResp.Status != "success" || len(promResp.Data.Result) == 0 {
		return 0, fmt.Errorf("无有效查询结果")
	}

	valArr := promResp.Data.Result[0].Value
	if len(valArr) < 2 {
		return 0, fmt.Errorf("prometheus 数据点格式异常")
	}

	valStr, ok := valArr[1].(string)
	if !ok {
		return 0, fmt.Errorf("无法断言 prometheus 数据为 string")
	}

	valFloat, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, err
	}

	return valFloat, nil
}

func (p *PrometheusCollector) getFromCache(nodeName string) NodeMetrics {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if val, ok := p.cache[nodeName]; ok {
		return val
	}
	// 默认基准水位：30%
	return NodeMetrics{CPUUsageRatio: 0.30, MemUsageRatio: 0.35}
}
