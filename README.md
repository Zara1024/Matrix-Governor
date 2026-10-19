# Matrix-Governor

<p align="center">
  <a href="https://github.com/Zara1024/Matrix-Governor">
    <img src="docs/assets/logo.svg" alt="Matrix-Governor Logo" width="460"/>
  </a>
</p>

<p align="center">
  <b>企业级 Kubernetes 自适应动态超卖、分级保障与 FinOps 降本增效全栈治理系统</b><br>
  <i>Enterprise Kubernetes Dynamic Overselling, Mixed-Workload QoS Governance & FinOps Engine</i>
</p>

<p align="center">
  <a href="https://github.com/Zara1024/Matrix-Governor/releases/tag/v0.0.1"><img src="https://img.shields.io/badge/Release-v0.0.1-brightgreen.svg?style=flat-square" alt="Release v0.0.1"></a>
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.20%2B-00ADD8.svg?style=flat-square&logo=go" alt="Go Version"></a>
  <a href="https://kubernetes.io"><img src="https://img.shields.io/badge/Kubernetes-v1.20%2B-326CE5.svg?style=flat-square&logo=kubernetes" alt="Kubernetes"></a>
  <a href="https://www.kernel.org/"><img src="https://img.shields.io/badge/Linux%20Kernel-5.4%2B-FCC624.svg?style=flat-square&logo=linux" alt="Kernel"></a>
  <a href="https://en.wikipedia.org/wiki/Cgroups"><img src="https://img.shields.io/badge/Cgroup-v2%20Native-orange.svg?style=flat-square" alt="Cgroup v2"></a>
  <a href="https://github.com/Zara1024/Matrix-Governor/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg?style=flat-square" alt="License"></a>
  <a href="https://github.com/Zara1024/Matrix-Governor/pulls"><img src="https://img.shields.io/badge/PRs-welcome-brightgreen.svg?style=flat-square" alt="PRs Welcome"></a>
</p>

---

## 📑 目录 (Table of Contents)

- [💡 背景与核心痛点 (Motivation & Pain Points)](#-背景与核心痛点-motivation--pain-points)
- [✨ 核心特性 (Key Features)](#-核心特性-key-features)
- [🏛️ 系统架构 (Architecture)](#️-系统架构-architecture)
- [📊 方案深度对比 (Comparison)](#-方案深度对比-comparison)
- [🧪 生产实战场景验证矩阵 (Production Verification)](#-生产实战场景验证矩阵-production-verification)
- [🚀 快速开始 (Quick Start)](#-快速开始-quick-start)
- [📈 财务账本与监控导出 (FinOps & Observability)](#-财务账本与监控导出-finops--observability)
- [🗺️ 版本演进路线 (Roadmap)](#️-版本演进路线-roadmap)
- [🤝 参与贡献 (Contributing)](#-参与贡献-contributing)
- [📄 开源协议 (License)](#-开源协议-license)

---

## 💡 背景与核心痛点 (Motivation & Pain Points)

在企业级生产 Kubernetes 容器集群中，普遍存在**“静态分配率虚高（80%~90%）、物理真实利用率低下（15%~25%）”**的巨大资源浪费。研发人员为保障自身微服务在偶发流量下的绝对稳定性，往往在配置 Pod 的 `resources.requests` 时过度申请，导致高达 **70%** 的物理硬件资源常年处于空转状态。

目前行业主流的混部与超卖方案（如阿里 Koordinator、字节 Katalyst、腾讯 Crane）：
1. **内核定制要求苛刻**：重度依赖特定云厂商深度定制的 Linux 内核特性，导致自建私有云或通用公有云（如 AWS/GCP/阿里云 ACK 标准节点）无法平滑升级部署；
2. **业务改造成本高昂**：通常强制要求业务研发修改现有部署 YAML 申请非标的扩展资源（如 `batch-cpu`），业务团队推行动力严重不足；
3. **架构强耦合风险**：中央调度器或控制器一旦网络分区或宕机，单机节点易丧失管控能力从而引发节点级雪崩。

**Matrix-Governor（矩阵总督）正是为终结这一困局而生**。它提供了一套完全开箱即用、业务零侵入、两层解耦的高可用混部超卖全栈治理体系。

---

## ✨ 核心特性 (Key Features)

### 1. 业务零侵入动态超卖 (Zero-Intrusion Dynamic Overselling)
通过 Mutating Admission Webhook 拦截节点状态上报，根据当前节点实际水位智能动态放大原生 `status.allocatable`（如 4 核物理节点按需放大至 8 核）。业务研发**无需改动任何一行现有 YAML 配置**，调度器即可无缝装箱超配调度；同时兼容大厂扩展资源模型（`kubernetes.io/batch-cpu`）。

### 2. 增量式 PID 闭环控温算法 (Incremental PID Regulation)
引入工业级增量式 PID（比例-积分-微分）闭环控制算法，每 10 秒采样节点真实物理利用率：
- **安全死区防抖 (Deadband)**：设定利用率安全区间（如 50%~65%），在安全带内维持超卖比恒定，避免控制器高频颠簸；
- **自适应探索**：在低负载期平滑向上自适应探索最佳超配比例，在负载爬升期平滑缓释。

### 3. 三道递进式安全防御网 (Multi-Tier Progressive Safety Net)
- **第一道防线（毫秒级 CFS 降频微压制）**：单机自治守护 `matrix-agent` 基于 Cgroup v2 实时监测。当物理负载逼近警戒线（70%）时，毫秒级压缩 BestEffort 离线任务的 CFS Quota，在不杀进程的前提下削峰填谷，保障核心 LS 业务 P99 时延零受损；
- **第二道防线（80% 物理过载瞬时硬熔断）**：若宿主机物理 CPU/内存突破 80% 硬阈值，控制器毫秒级熔断超卖比回退至物理 1.0x，阻断调度器继续下发负载，并锁定 45 秒冷静恢复期；
- **第三道防线（五维权重差异化驱逐仲裁）**：过载持续无法缓解时，触发 Eviction API 差异化驱逐。基于五维加权打分模型（QoS等级 + 资源申请量 + 内存占用 + 运行时长 + 优先级），优先精准收割低优离线作业，高保在线核心享受免死保护。

### 4. 硬件拓扑与内核级 QoS 隔离 (Hardware Topology & Kernel-Level QoS)
- **CPU 拓扑硬核隔离 (cpuset)**：高保 Guaranteed 核心业务享有专属独占 CPU 亲和绑定（如核 2,3），离线批处理禁锢于共享核池，彻底杜绝 Noisy Neighbor（嘈杂邻居）缓存污染；
- **Linux 内核网络流量整形 (Traffic Control)**：针对离线大数据作业穿透挂载内核 TC 令牌桶过滤器（TBF），将离线网络带宽严格压制在阈值以内（如 256Kbps），保障核心 API 网络微秒级通畅；
- **Memory QoS 内存防爆**：在内核层差异化配置 `/proc/<pid>/oom_score_adj`。核心业务赋予 `-997`（免死金牌），离线任务赋予 `1000`（顶格首选牺牲者），杜绝物理内存吃紧时内核 OOM-Killer 误杀核心业务。

### 5. Local Autonomy 单机独立自治 (Decoupled High Availability)
中央控制面（`matrix-governor`）与单机数据面（`matrix-agent`）彻底物理解耦。当中央控制器遭遇版本更新、网络分区或 OOM 崩溃停机时，单机 Agent 依然持续坚守阵地独立履行 CFS 降频自保职责，节点 Allocatable 绝对不抖动，控制器重启后自动无缝握手接管。

### 6. FinOps 实时成本节约核算引擎 (FinOps Cost Analytics)
内置云原生成本核算引擎，将混部超卖额外白嫖出的虚拟 CPU/内存容量，结合主流云厂商按量计费单价模型，实时折算为**每小时节省金额（元/小时）**与**累计节省总额（元）**，向 Prometheus 提供标准的 `/metrics` 监控导出，无缝接入 Grafana 成本大盘。

---

## 🏛️ 系统架构 (Architecture)

Matrix-Governor 采用清晰的“中央智能编排 + 单机数据面自治”两层解耦物理拓扑：

```
                            ┌────────────────────────────────────────┐
                            │      Kubernetes API Server             │
                            └────────────────────┬───────────────────┘
                                                 │
                                 Mutating Webhook / Nodes Status
                                                 │
         ┌───────────────────────────────────────▼───────────────────────────────────────┐
         │              Matrix-Governor Central Controller (Control Plane)               │
         │  ┌──────────────────────┐  ┌──────────────────────┐  ┌──────────────────────┐  │
         │  │ Incremental PID      │  │ Circuit Breaker      │  │ Eviction Arbiter     │  │
         │  │ Oversold Calculator  │  │ (>80% Hard Fallback) │  │ (5-Dim Weight Model) │  │
         │  └──────────────────────┘  └──────────────────────┘  └──────────────────────┘  │
         │  ┌──────────────────────┐  ┌──────────────────────┐  ┌──────────────────────┐  │
         │  │ Auto-PDB Protector   │  │ Dual-Resource Engine │  │ FinOps Billing Engine│  │
         │  │ (Anti-Cascade Crash) │  │ (batch-cpu Injection)│  │ (Hourly Savings RMB) │  │
         │  └──────────────────────┘  └──────────────────────┘  └──────────────────────┘  │
         └───────────────────────────────────────┬───────────────────────────────────────┘
                                                 │
                                  NodeLease / Local Autonomy
                                                 │
       ┌─────────────────────────────────────────┴─────────────────────────────────────────┐
       ▼                                                                                   ▼
┌─────────────────────────────────┐                                 ┌─────────────────────────────────┐
│ Worker Node 1 (Matrix-Target)   │                                 │ Worker Node 2 (Matrix-Target)   │
│ ┌─────────────────────────────┐ │                                 │ ┌─────────────────────────────┐ │
│ │  matrix-agent (DaemonSet)   │ │                                 │ │  matrix-agent (DaemonSet)   │ │
│ │  - Linux Cgroup v2 Manager  │ │                                 │ │  - Linux Cgroup v2 Manager  │ │
│ │  - CFS Quota Micro-Throttler│ │                                 │ │  - CFS Quota Micro-Throttler│ │
│ │  - TC Network Bandwidth TBF │ │                                 │ │  - TC Network Bandwidth TBF │ │
│ │  - Memory QoS (oom_score)   │ │                                 │ │  - Memory QoS (oom_score)   │ │
│ └──────────────┬──────────────┘ │                                 └──────────────┬──────────────────┘
│                │                                                                 │
│   ┌────────────┴────────────┐                                       ┌────────────┴────────────┐
│   ▼                         ▼                                       ▼                         ▼
│ ┌───────────────┐ ┌───────────────┐                               ┌───────────────┐ ┌───────────────┐
│ │ Online (LS)   │ │ Offline (BE)  │                               │ Online (LS)   │ │ Offline (BE)  │
│ │ Guaranteed    │ │ BestEffort    │                               │ Guaranteed    │ │ BestEffort    │
│ │ cpuset: 2,3   │ │ cpuset: 0,1   │                               │ cpuset: 2,3   │ │ cpuset: 0,1   │
│ │ oom_adj: -997 │ │ oom_adj: 1000 │                               │ oom_adj: -997 │ │ oom_adj: 1000 │
│ └───────────────┘ └───────────────┘                               └───────────────┘ └───────────────┘
└─────────────────────────────────┘                                 └─────────────────────────────────┘
```

---

## 📊 方案深度对比 (Comparison)

| 核心维度 | 原生 Kubernetes | 阿里 Koordinator | 腾讯 Crane | 字节 Katalyst | **Matrix-Governor (本项目)** |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **业务 YAML 改造侵入** | 无 | 高（需改用 batch 资源） | 中（需适配推荐器） | 高（需定制混部配置） | **完全零侵入（原生 Allocatable 动态放大）** |
| **Linux 内核依赖** | 通用标准内核 | 依赖阿里 Dragonfly 内核 | 通用内核 | 依赖字节内部优化内核 | **通用标准 Linux 5.4+（Cgroup v2 原生支持）** |
| **算法调节模型** | 静态无调节 | 静态阈值规则 | 预测式缩放 | 动态启发式 | **增量式 PID 闭环控温 + 死区防抖** |
| **安全防御层次** | 仅 OOM 粗暴杀 | 单机压制 + 驱逐 | 建议/告警 | 单机压制 + 驱逐 | **CFS 毫秒微压制 + 80%硬熔断 + 5维权重驱逐** |
| **单机容灾自治 (Autonomy)** | 弱 | 强 | 中 | 强 | **强（两层彻底解耦，中央宕机单机永不雪崩）** |
| **业务防穿透 (Anti-Crash)** | 人工配 PDB | 人工维护 | 无 | 人工维护 | **内置 Auto-PDB 自动注入与强制阻断** |
| **拓扑亲和与网络整形** | CPU Manager | 支持 | 不支持 | 支持 | **Guaranteed 整核独占 + 内核 TC 流量限速** |
| **FinOps 降本账本** | 无 | 第三方配合 | 自带计量 | 无 | **内置按量付费模型，实时折算 RMB/h 与总额** |

---

## 🧪 生产实战场景验证矩阵 (Production Verification)

在企业级云端真实 3 节点集群（Kubernetes v1.27.3，Linux 5.15 内核，Cgroup v2 靶场）上，Matrix-Governor 通过了 14 大核心实战场景的真机严苛测试：

| 用例编号 | 生产实战测试场景 | 核心检验动作 | 实测断言标准 | 测试状态 |
| :---: | :--- | :--- | :--- | :---: |
| **TC-01** | **【容量翻倍】在线业务低峰期物理容量动态放大** | 打标超卖节点并刷新 kubelet，下发 5 个 1 核 Pod | Allocatable 从 4 核翻倍至 8 核，5 副本 100% Running，0 个 OutOfcpu | **✅ PASS** |
| **TC-02** | **【智能控温】增量式 PID 自适应闭环控温实测** | 采集物理真实负载，验证 PID 自适应调优与死区防抖 | 日志清晰呈现增量探索过程，安全死区内超卖比维持恒定无高频抖动 | **✅ PASS** |
| **TC-03** | **【毫秒保命】单机自治 Agent CFS 降频微压制** | 模拟物理负载飙升至 70%，Agent 动态调节 CFS 配额 | BestEffort 离线任务被限制 CPU 周期，在线业务 P99 时延零受损 | **✅ PASS** |
| **TC-04** | **【底线熔断】80% 物理过载硬熔断与瞬间降级回退** | 注入 85% 强 CPU 压力突破警戒线 | 控制器瞬间回退比率为 1.0x，Allocatable 秒级降回物理 4 核极限 | **✅ PASS** |
| **TC-05** | **【分级保命】大促洪峰物理过载时五维权重驱逐** | 混合部署 LS 与 BE，触发高压驱逐接口 | BE 离线任务优先被淘汰，LS 在线核心库 100% 毫发无损存活 | **✅ PASS** |
| **TC-06** | **【业务防穿透】Auto-PDB 自动化高可用防御测试** | 创建 PDB 规则（minAvailable: 4），模拟破坏 | ALLOWED DISRUPTIONS: 1 限制生效，安全阻断过度驱逐 | **✅ PASS** |
| **TC-07** | **【双轨资源】企业级 Batch 离线扩展资源模型** | 打上 `resource-model=batch` 标签 | 节点成功注册 `kubernetes.io/batch-cpu: 4` 与内存扩展配额 | **✅ PASS** |
| **TC-08** | **【财务核算】企业 FinOps 实时成本节约账本导出** | 请求 `/metrics` 监控端点 | 标准 Prometheus 格式输出节省金额（1.11元/h/节点）与虚拟核数（4核） | **✅ PASS** |
| **TC-09** | **【优雅排空】生产节点维护（`kubectl drain`）实测** | 运维维护执行排空命令，超配节点协同 PDB | 离线 Pod 平滑排空，在线业务触发 PDB 拦截保护，杜绝全量停机 | **✅ PASS** |
| **TC-10** | **【拓扑绑核】高优先级核心业务 CPU 独占亲和** | 部署 Guaranteed 业务与 BE 业务，核验 cpuset | 核心业务锁定专属独占核心（CPU 2,3），离线任务禁锢于共享池 | **✅ PASS** |
| **TC-11** | **【网络限速】内核级带宽流量整形（TC）实测** | 挂载 Linux TC 令牌桶过滤器（TBF）至离线容器 | 离线出口带宽被严格钳制在 256Kbit/s，在线核心服务网络延迟零抖动 | **✅ PASS** |
| **TC-12** | **【弹性联动】超卖节点与 HPA 自动水平扩缩容** | 配置 HPA 弹性伸缩，模拟大促突发扩容至 5 副本 | 超卖节点顺畅接纳 4100m CPU 扩容副本，流量退峰后平稳缩容回收 | **✅ PASS** |
| **TC-13** | **【内存防爆】Memory QoS 与内核 OOM 水线隔离** | 穿透容器查看 Linux 内核 `/proc/<pid>/oom_score_adj` | 核心业务获 `-997`（免死保护），离线任务获 `1000`（顶格首选牺牲者） | **✅ PASS** |
| **TC-14** | **【容灾自愈】中央控制器崩溃停机与单机独立自治** | 模拟中央控制器 0 副本宕机，测试单机数据面 | 单机 Agent 持续坚守巡检，节点容量稳定不抖动，控制器重启无缝自愈 | **✅ PASS** |

---

## 🚀 快速开始 (Quick Start)

### 1. 先决条件 (Prerequisites)
- Kubernetes 1.20+（推荐 v1.24 ~ v1.29）
- Linux 内核 5.4+（全面开启 Cgroup v2）
- 集群具备标准 `containerd` 或 `CRI-O` 运行时环境

### 2. 一键安装部署 (Installation)

```bash
# 1. 克隆代码仓库
git clone https://github.com/Zara1024/Matrix-Governor.git
cd Matrix-Governor

# 2. 部署 RBAC 鉴权与命名空间
kubectl apply -f deploy/rbac.yaml
kubectl apply -f clusterrole_evict.yaml

# 3. 部署中央控制器 (matrix-governor)
kubectl apply -f deploy/production/governor-deployment.yaml

# 4. 部署单机自治守护 (matrix-agent DaemonSet)
kubectl apply -f deploy/agent/matrix-agent-ds.yaml

# 5. 核验服务运行状态
kubectl get pods -n matrix-system -o wide
```

*预期回显状态：*
```text
NAME                               READY   STATUS    RESTARTS   AGE   NODE
matrix-agent-xxxxx                 1/1     Running   0          2m    worker-1
matrix-agent-yyyyy                 1/1     Running   0          2m    worker-2
matrix-governor-zzzzz              1/1     Running   0          2m    worker-2
```

### 3. 一键开启节点动态超卖

只需为您希望托管的物理节点打上标签，系统将自动探索并动态注入 Allocatable 容量：

```bash
# 为目标工作节点打上超卖托管标签
kubectl label node <your-worker-node> oversold=oversold matrix-governor.io/oversold=enabled --overwrite

# 刷新节点 Kubelet 触发全量上报与 Webhook 注入
# (公网节点无需重启，系统将在下一次全量状态周期自动注入)
```

查看节点放大效果：
```bash
kubectl get node <your-worker-node> -o custom-columns=NAME:.metadata.name,CAPACITY:.status.capacity.cpu,ALLOCATABLE:.status.allocatable.cpu
```
*预期回显：*
```text
NAME                    CAPACITY   ALLOCATABLE
matrix-cluster-worker   4          8
```
*(物理仅有 4 核算力，调度层即可按 8 核超配额度部署更多工作负载！)*

---

## 📈 财务账本与监控导出 (FinOps & Observability)

Matrix-Governor 内置标准 Prometheus `/metrics` 监控端点，实时导出动态超卖比率与财务节约数据：

```bash
SVC_IP=$(kubectl get svc -n matrix-system matrix-governor-svc -o jsonpath='{.spec.clusterIP}')
curl -k -s https://$SVC_IP:8443/metrics | grep -E '^matrix_governor_'
```

*导出指标规范示例：*
```prometheus
# 每小时为企业节省的云算力资金 (RMB)
matrix_governor_finops_hourly_savings_rmb{node="matrix-cluster-worker"} 1.1095

# 额外为企业创造的等效虚拟 CPU 核数
matrix_governor_finops_saved_cpu_cores{node="matrix-cluster-worker"} 4

# 额外释放的等效虚拟内存容量 (GB)
matrix_governor_finops_saved_memory_gb{node="matrix-cluster-worker"} 3.65

# 当前节点生效的动态超卖放大倍率
matrix_governor_node_oversold_ratio{node="matrix-cluster-worker",resource="cpu"} 2.0
matrix_governor_node_oversold_ratio{node="matrix-cluster-worker",resource="memory"} 1.5
```

---

## 🗺️ 版本演进路线 (Roadmap)

- [x] **v0.0.1 (当前首个开源版本 - 2026 Q4)**
  - [x] 基于 Admission Webhook 的原生 Allocatable 动态拦截与注入；
  - [x] 增量式 PID 闭环控温算法与安全死区防抖引擎；
  - [x] 中央大脑与单机 Agent 两层物理拓扑解耦；
  - [x] Cgroup v2 毫秒级 CFS 降频微压制与 80% 过载硬熔断机制；
  - [x] 五维加权差异化驱逐仲裁与 Auto-PDB 自动防穿透；
  - [x] CPU 拓扑硬绑核（cpuset）、Linux 内核 TC 带宽限速与 Memory QoS；
  - [x] 14 大企业生产级端到端实战验证套件。
- [ ] **v0.1.0 (规划中)**
  - [ ] eBPF 无侵入内核探针，实时采集硬件 CPI、MPKI 性能计数器指标；
  - [ ] 深度融合 Intel RDT（CAT 缓存隔离 + MBA 内存带宽整形）；
  - [ ] 基于历史行为画像的智能 VPA 推荐器联动。
- [ ] **v1.0.0 (企业级生产就绪标准)**
  - [ ] 多集群 Federation 联邦超卖协同控制面；
  - [ ] 大模型（LLM）分布式训练与 GPU 显存算力动态混部切分（vGPU）。

---

## 🤝 参与贡献 (Contributing)

欢迎任何形式的开源贡献！无论是提交 Issue、完善文档、新增场景还是贡献核心代码。

1. **Fork 本仓库** 到您的个人 GitHub；
2. **新建特性分支**：`git checkout -b feat/amazing-feature`；
3. **提交您的修改**：`git commit -m "feat: add amazing feature"`；
4. **推送到远程分支**：`git push origin feat/amazing-feature`；
5. **创建 Pull Request**，我们会以最快速度进行 Code Review！

请参阅我们的 [贡献指南 (CONTRIBUTING.md)](CONTRIBUTING.md) 与 [行为准则 (CODE_OF_CONDUCT.md)](CODE_OF_CONDUCT.md)。

---

## 📄 开源协议 (License)

本项目采用 [Apache 2.0 许可证](LICENSE) 进行开源。欢迎在遵守开源协议的前提下商业应用、二次开发与技术交流。