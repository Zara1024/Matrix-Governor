package adfunc

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/Zara1024/Matrix-Governor/pkg/nodemanager"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

func init() {
	register(AdmissionFunc{
		Type: AdmissionTypeMutating,
		Path: "/oversold",
		Func: oversold,
	})
}

// oversold 处理来自 kubelet 上报 nodes/status 的 Mutating 请求
func oversold(request *admissionv1.AdmissionRequest) (*admissionv1.AdmissionResponse, error) {
	// 仅拦截 Node 资源类型
	switch request.Kind.Kind {
	case "Node":
		node := v1.Node{}
		if err := json.Unmarshal(request.Object.Raw, &node); err != nil {
			errMsg := fmt.Sprintf("[Matrix-Governor.Mutating] /oversold: 反序列化 Node 对象失败: %v", err)

			klog.Error(errMsg)
			return &admissionv1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Code:    http.StatusBadRequest,
					Message: errMsg,
				},
			}, nil
		}

		// 检查节点是否配置了超卖标签白名单
		// 支持 "oversold": "oversold" / "true" 或 "matrix-governor.io/oversold": "enabled"
		isOversoldEnabled := false
		if val, ok := node.Labels["oversold"]; ok && (val == "oversold" || val == "true") {
			isOversoldEnabled = true
		}
		if val, ok := node.Labels["matrix-governor.io/oversold"]; ok && val == "enabled" {
			isOversoldEnabled = true
		}

		if !isOversoldEnabled {
			return &admissionv1.AdmissionResponse{
				Allowed: true,
				Result: &metav1.Status{
					Code:    http.StatusOK,
					Message: "该节点未开启 Matrix-Governor 超卖治理，原样放行",
				},
			}, nil
		}

		nodeName := node.Name
		if nodeName == "" {
			nodeName = request.Name
		}

		// 核心升级：从 NodeManager 中获取经由 PID 算法闭环自适应计算出的实时超卖比
		cpuRatio, memRatio, isFused := nodemanager.GetGlobalManager().GetDynamicRatio(nodeName)

		if isFused {
			klog.Warningf("[Matrix-Governor.Mutating] 节点 %s 处于安全熔断期！强制将 Allocatable 重置为原生 Capacity！", nodeName)
		} else {
			klog.Infof("[Matrix-Governor.Mutating] 节点 %s 准入拦截通过，当前生效动态超卖比: [CPU: x%.2f, Mem: x%.2f]",
				nodeName, cpuRatio, memRatio)
		}

		// 计算经过动态放大的 Allocatable 资源
		patchedCpu := calculateDynamicCPU(node.Status.Capacity.Cpu(), cpuRatio)
		patchedMem := calculateDynamicMemory(node.Status.Capacity.Memory(), memRatio)

		patches := []Patch{
			{
				Option: PatchOptionReplace,
				Path:   "/status/allocatable/cpu",
				Value:  patchedCpu,
			},
			{
				Option: PatchOptionReplace,
				Path:   "/status/allocatable/memory",
				Value:  patchedMem,
			},
		}

		// 如果定义了 Pod 数量超配比例 (如通过 label 指定)
		if overPodsVal, ok := node.Labels["overpods"]; ok && overPodsVal != "" {
			patches = append(patches, Patch{
				Option: PatchOptionReplace,
				Path:   "/status/allocatable/pods",
				Value:  overpods(Quantitytostring(node.Status.Capacity.Pods()), overPodsVal),
			})
		}

		// 企业级双资源模型支持：如果开启了 Batch 扩展资源模式，额外注册 kubernetes.io/batch-cpu
		if node.Labels["matrix-governor.io/resource-model"] == "batch" || node.Labels["batch-resources"] == "true" {
			batchRatio := cpuRatio - 1.0
			// 生产级高可用保底：开启 Batch 双轨模型时，以 1.0x 物理等额容量(4核)作为基线注册，确保离线作业调度器稳定可达
			if batchRatio < 1.0 {
				batchRatio = 1.0
			}
			batchMemRatio := memRatio - 1.0
			if batchMemRatio < 0.5 {
				batchMemRatio = 0.5
			}
			batchCPUVal := calculateDynamicCPU(node.Status.Capacity.Cpu(), batchRatio)
			batchMemVal := calculateDynamicMemory(node.Status.Capacity.Memory(), batchMemRatio)
			patches = append(patches,
				Patch{
					Option: PatchOptionAdd,
					Path:   "/status/allocatable/kubernetes.io~1batch-cpu",
					Value:  batchCPUVal,
				},
				Patch{
					Option: PatchOptionAdd,
					Path:   "/status/allocatable/kubernetes.io~1batch-memory",
					Value:  batchMemVal,
				},
			)
		}

		patchBytes, err := json.Marshal(patches)
		if err != nil {
			errMsg := fmt.Sprintf("[Matrix-Governor.Mutating] /oversold: 序列化 Patch 失败: %v", err)

			logger.Error(errMsg)
			return &admissionv1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Code:    http.StatusInternalServerError,
					Message: errMsg,
				},
			}, nil
		}

		klog.V(2).Infof("[Matrix-Governor.Mutating] 节点 %s 生成的 JSONPatch: %s", nodeName, string(patchBytes))

		return &admissionv1.AdmissionResponse{
			Allowed:   true,
			Patch:     patchBytes,
			PatchType: JSONPatch(),
			Result: &metav1.Status{
				Code:    http.StatusOK,
				Message: fmt.Sprintf("Matrix-Governor 动态超卖注入成功 (CPU: x%.2f, Mem: x%.2f)", cpuRatio, memRatio),
			},
		}, nil

	default:
		errMsg := fmt.Sprintf("[Matrix-Governor.Mutating] /oversold: 收到非 Node 资源请求: %s", request.Kind.Kind)
		logger.Error(errMsg)
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Code:    http.StatusForbidden,
				Message: errMsg,
			},
		}, nil
	}
}

// Quantitytostring *resource.Quantity 类型转 string
func Quantitytostring(r *resource.Quantity) string {
	if r == nil {
		return "0"
	}
	return r.String()
}

// calculateDynamicCPU 根据基础容量与动态倍数计算目标 CPU Allocatable
func calculateDynamicCPU(capacity *resource.Quantity, ratio float64) string {
	if capacity == nil || ratio <= 0 {
		return "1"
	}
	// 转为毫核 (millicores) 进行高精度计算，防止浮点截断误差
	baseMilli := capacity.MilliValue()
	targetMilli := int64(math.Round(float64(baseMilli) * ratio))

	// 生成新的标准 Quantity
	newQuantity := resource.NewMilliQuantity(targetMilli, resource.DecimalSI)
	return newQuantity.String()
}

// calculateDynamicMemory 根据基础容量与动态倍数计算目标 Memory Allocatable
func calculateDynamicMemory(capacity *resource.Quantity, ratio float64) string {
	if capacity == nil || ratio <= 0 {
		return "1Gi"
	}
	// 转为字节数 (Bytes) 计算
	baseBytes := capacity.Value()
	targetBytes := int64(math.Round(float64(baseBytes) * ratio))

	// 以 Ki 为基准单位格式化输出
	targetKi := targetBytes / 1024
	return fmt.Sprintf("%dKi", targetKi)
}

// overpods 保留兼容 Pod 数量超售计算
func overpods(pods, multiple string) string {
	a, _ := json.Marshal(pods)
	_ = a

	var base float64 = 110
	if pods != "" {
		fmt.Sscanf(pods, "%f", &base)
	}
	var mult float64 = 1.0
	if multiple != "" {
		fmt.Sscanf(multiple, "%f", &mult)
	}
	res := int(base * mult)
	return fmt.Sprintf("%d", res)
}
