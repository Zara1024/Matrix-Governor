# Matrix-Governor Technical Architecture Whitepaper
# Matrix-Governor 技术架构白皮书

---

## 1. Project Mission & Design Philosophy / 项目愿景与设计哲学

**Matrix-Governor** 是一个专为 Kubernetes 生态打造的企业级动态超卖、分级保障与 FinOps 降本增效系统（对标阿里巴巴 Koordinator、字节跳动 Katalyst、腾讯 Crane）。
其核心设计哲学为：
1. **业务零侵入（Non-Invasive）**：业务 Pod 的 YAML 声明无须修改，准入层透明生效；
2. **三道递进防御（3-Tier Progressive Defense）**：
   - 第一道：增量式 PID 闭环控温自适应超卖；
   - 第二道：Cgroup CFS 毫秒级微压制，业务不杀、性能无损；
   - 第三道：80% 突发硬熔断 + 5 维加权差异化驱逐（Auto-PDB 保护在线核心）；
3. **两层解耦架构（Two-Tier Architecture）**：中心控制器（Controller-Manager）负责全局 CRD 与选主，单机守护进程（Matrix-Agent）负责本地毫秒级内核/硬件隔离。

---

## 2. Eight-Layer Full-Stack Architecture / 八层技术全栈架构

| 层次 | 核心模块 | 技术实现与标准 |
| :--- | :--- | :--- |
| **1. 物理硬件与微架构层** | `pkg/hardware` | Intel RDT L3 Cache (CAT) 硬件隔离、MBA 内存带宽限制、NUMA 拓扑亲和性绑核、GPU 虚拟显存切分与算力借还 |
| **2. 操作系统与内核层** | `pkg/kernel` | Cgroup v2 异步 PageCache 柔性回收 (`memory.high`)、磁盘 I/O 调度权重 (`io.weight`)、BVT 纳秒级抢占加速、网络 TC 带宽限速 |
| **3. 性能干扰感知层** | `pkg/collector` | eBPF + PMU 硬件性能计数器采集 (CPI / MPKI)、Linux PSI (`/proc/pressure/*`) 内核压力指标 |
| **4. 单机运行时与隔离层** | `pkg/agent`, `pkg/cgroup` | 解耦的单机 DaemonSet (`matrix-agent`)、Cgroup CFS Quota 毫秒级微压制 |
| **5. 调度器与资源模型层** | `pkg/scheduler` | 双资源模型 (原生 `Allocatable` + `kubernetes.io/batch-cpu`)、Load-Aware 真实负载感知调度、批处理 Gang 协同调度 (Coscheduling) |
| **6. 准入拦截与保护层** | `pkg/adfunc`, `pkg/protector`, `pkg/cert` | Mutating Webhook Allocatable 动态放大、ETCD 零污染自愈、Auto-PDB 自动注入打底、纯 Go 内存自签证书轮转 |
| **7. 控制平面与 CRD 体系** | `pkg/leaderelection`, `deploy/crds` | K8s Lease 分布式热备选主、`OversoldPolicy` (节点池策略)、`NodeSLO` (单机门禁)、`WorkloadQoS` (业务分类) |
| **8. 智能画像与 FinOps 财务层** | `pkg/finops`, `pkg/recommender` | FinOps 实时降本资金核算引擎 (RMB/h)、VPA 智能规格画像与 Request 推荐算法 (P95 + 15% 冗余)、Grafana 大盘 |

---

## 3. Mathematical Foundations / 核心数学模型

### 3.1 增量式 PID 控制器 (Incremental PID Formulation)
$$\Delta u(k) = K_p [e(k) - e(k-1)] + K_i e(k) + K_d [e(k) - 2e(k-1) + e(k-2)]$$
$$u(k) = \text{Clamp}(u(k-1) + \Delta u(k), \text{MinRatio}, \text{MaxRatio})$$

### 3.2 五维差异化驱逐打分公式 (5-Dimensional Eviction Scoring)
$$\text{Score} = \text{Score}_{\text{PDB}} + \text{DisruptionsAllowed} + \text{Score}_{\text{QoS}} + \text{Score}_{\text{Overcommit}} + \text{Score}_{\text{Memory}}$$

### 3.3 FinOps 实时节约金额核算公式 (FinOps Cost Savings)
$$\text{HourlySavingsRMB} = \sum_{i \in \text{Nodes}} \left[ \Delta \text{CPU}_i \times \text{Price}_{\text{CPU}} + \Delta \text{MemGB}_i \times \text{Price}_{\text{Mem}} \right]$$
