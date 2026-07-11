# 智慧能源 AI Agent 产品与技术设计

> 文档状态：Draft v1.0  
> 适用产品：HVAC 智慧能源平台  
> 目标读者：产品、前端、后端、算法、能源专家、运维负责人、安全与审计人员  
> 基线日期：2026-07-11

## 1. 文档目的

本文定义智慧能源平台 AI Agent 的产品定位、用户体验、系统架构、上下文协议、工具体系、任务状态机、权限审批、审计治理、数据接口和阶段性交付标准。

它不是单纯的聊天界面设计，也不是某个模型供应商的接入说明。本文关注的是：如何让 AI 深入参与资产、遥测、告警、FDD、工单、优化、能耗、成本和系统治理，同时保持结果可解释、动作可授权、过程可审计、执行可验证。

本文是后续 AI 产品设计、接口设计、前后端实现和验收的共同基线。

---

## 2. 执行摘要

AI 运维助手应被定义为智慧能源系统的统一智能操作层，而不是独立问答机器人。

它的核心闭环是：

```text
感知问题
→ 汇总上下文
→ 制定调查计划
→ 调用受控工具
→ 形成证据链
→ 提出建议或动作
→ 人工审批
→ 执行
→ 验证效果
→ 沉淀经验
```

一期优先建设可信只读与调查能力，不直接控制设备。写操作从创建工单、补充工单、提交优化草稿等低风险业务动作开始，逐步引入审批。任何设备启停、设定值修改或策略下发都属于高风险控制能力，必须在真实控制接口、权限模型、双人审批、灰度执行、自动回滚和效果验证全部具备后才能开放。

产品形态由四部分组成：

1. **全局 AI 抽屉**：当前页面内的快速咨询和轻量操作。
2. **AI 运维中心**：复杂调查、任务进度、主动发现、审批和历史报告。
3. **业务页面内联 AI**：在设备、告警、FDD、工单、优化和能耗对象旁提供上下文操作。
4. **主动任务与事件收件箱**：承载 AI 周期巡检、节能机会和风险发现，避免打扰式弹窗。

技术上采用“协调 Agent + 领域工具 + 确定性分析服务”的架构。模型负责理解、规划、解释和组织；业务计算、时序聚合、权限校验、写操作和审计必须由确定性服务完成。

---

## 3. 当前系统基线

### 3.1 已有能力

当前前端已经具备：

- 全局 AI 抽屉与 `/ai` 完整工作台共用会话；
- CopilotKit Runtime 模式和本地 Mock 流式模式；
- 当前路由、页面说明、建筑、用户角色、演示状态和业务摘要上下文；
- 当前角色允许访问的页面列表；
- 权限感知的 `navigate_to_page` 前端工具；
- `open_ai_workspace` 前端工具；
- Mock 模式下的实时遥测摘要问答；
- 明确的只读红线，不暴露设备控制工具。

### 3.2 当前局限

当前实现仍属于“上下文增强聊天”，尚未形成真正的 Agent 系统：

- 上下文只到页面和摘要级，缺少当前对象、筛选、时间窗口、图表钻取和选择状态；
- 没有跨模块调查任务；
- 没有结构化证据、置信度、缺失数据和假设管理；
- 没有工具注册中心和统一工具治理；
- 没有任务状态机、暂停、恢复、审批和效果验证；
- 没有主动巡检和事件收件箱；
- 没有组织知识、SOP、手册和历史案例检索；
- 没有 Agent 级审计、成本、延迟和质量指标；
- Mock 回答仍依赖关键词模板，不能替代生产级推理和工具调用。

### 3.3 兼容原则

后续建设必须保留当前已经验证的体验：

- 全局抽屉与 `/ai` 共用会话和任务状态；
- 页面上下文自动注入；
- 前端不持有模型密钥；
- 模型和 Runtime 可替换；
- 权限由平台角色和 scope 决定；
- 无权限对象不能通过 AI 间接读取；
- 模拟、预测和真实数据必须明确区分。

---

## 4. 产品愿景与边界

### 4.1 产品愿景

让用户不必在多个页面中手工拼接信息，也能完成从“发现异常”到“验证处理结果”的完整运维与能源管理闭环。

目标体验示例：

```text
用户：调查 1 号楼昨晚非营业时段能耗为什么升高。

Agent：
1. 比较过去四个同星期夜间负荷；
2. 识别能耗增量最大的系统和设备；
3. 检查运行日程、告警、FDD 和人工操作；
4. 形成原因、影响和建议。

最终结论：
- AHU-03 比计划晚停机 4.2 小时；
- 夜间平均功率增加 82 kW；
- 额外电量约 344 kWh，费用约 268 元；
- 未发现温度越界或延时运行审批；
- 建议创建检查风阀联锁与日程策略的工单。
```

### 4.2 非目标

以下内容不属于一期目标：

- 让模型直接查询生产数据库；
- 将大段原始时序数据直接塞给模型计算；
- 无审批修改设备参数；
- 无审计自动关闭告警或工单；
- 仅凭语言模型生成节能收益精确数值；
- 使用聊天记录作为未经治理的长期事实库；
- 以一个大模型替代 FDD、优化算法、能耗基线和权限系统；
- 为展示“智能”而制造没有数据依据的主动提醒。

---

## 5. 用户、角色与核心任务

### 5.1 用户角色

| 用户 | 主要目标 | AI 价值 |
|---|---|---|
| 运维值班人员 | 快速判断当前风险、处理告警和工单 | 值班摘要、优先级排序、现场排查步骤、工单草稿 |
| 暖通工程师 | 定位复杂设备问题、验证诊断和策略 | 多源证据调查、趋势分析、根因假设、历史案例 |
| 能源管理人员 | 解释能耗变化、发现节能机会、核算收益 | 能耗增量分解、基线比较、费用与碳影响、报告生成 |
| 策略审批人员 | 评估优化收益、风险和回滚条件 | 审批摘要、证据完整性检查、执行前预演 |
| 研发与系统管理员 | 检查数据质量、规则、权限和审计 | 数据源诊断、规则解释、权限查询、Agent 审计 |
| 管理人员 | 获取高层摘要和绩效趋势 | 日报周报、风险和收益摘要、异常解释 |

### 5.2 用户任务分级

#### 咨询任务

- 当前最需要关注什么；
- 解释某个设备、告警、诊断或指标；
- 查询某项业务状态；
- 生成摘要或报告。

#### 调查任务

- 调查设备能效下降；
- 调查夜间能耗异常；
- 调查告警风暴；
- 调查工单重复发生；
- 调查优化效果未达预期。

#### 执行任务

- 创建或补充工单；
- 指派处理人；
- 提交优化建议；
- 发起审批；
- 变更日程或规则；
- 受控下发控制策略。

---

## 6. 核心产品原则

### 6.1 证据优先

所有重要结论必须关联证据，至少说明：

- 数据来源；
- 时间范围；
- 对比基准；
- 计算口径；
- 结论置信度；
- 缺失或异常数据。

### 6.2 先读后写

Agent 默认只读。写操作必须通过明确的 Proposed Action 结构展示，不得把自然语言中的模糊意图直接转换为业务写入。

### 6.3 确定性计算优先

以下内容必须由确定性服务计算，而不是由模型心算：

- 能耗、费用、碳排；
- 同比、环比、MTD、WTD、YTD；
- COP 加权；
- 阈值判断；
- SLA；
- 节能收益；
- 时间窗口聚合；
- 权限；
- 风险等级；
- 执行参数校验。

### 6.4 对象可追溯

AI 提到的设备、告警、诊断、工单、优化建议、能耗周期和报告都必须使用平台真实对象 ID，并能打开对应页面。

### 6.5 人在回路

AI 生成的草稿、建议和动作由用户确认。高风险动作需要更高等级审批，不能用一次普通确认替代。

### 6.6 不确定性可见

Agent 必须区分：

- 已确认事实；
- 规则或算法结论；
- 高置信度推断；
- 待验证假设；
- 无法判断；
- 数据缺失。

### 6.7 验证才算闭环

动作执行成功不等于问题解决。Agent 必须在指定观察窗口后检查：

- 告警是否恢复；
- 诊断是否关闭；
- 关键指标是否改善；
- 能耗是否下降；
- 是否出现副作用；
- 是否需要回滚。

---

## 7. 产品形态与信息架构

### 7.1 全局 AI 抽屉

用途：快速咨询、当前页面解释、轻量导航和任务跟进。

必须展示：

- 当前上下文摘要；
- 当前对象；
- 建议问题；
- 会话消息；
- 正在运行的任务；
- 待确认动作；
- 打开完整工作台入口。

抽屉不应承载复杂审批表、长报告和多步骤调查详情，这些应进入 AI 运维中心。

### 7.2 AI 运维中心 `/ai`

建议包含五个一级区域：

```text
今日关注
调查任务
待我审批
主动发现
历史与报告
```

#### 今日关注

- 当前最高风险问题；
- SLA 风险工单；
- 高严重度 FDD；
- 能耗异常；
- 可执行节能机会；
- 数据质量问题。

#### 调查任务

- 任务目标；
- 当前阶段；
- 执行步骤；
- 已用数据；
- 关键发现；
- 待补充信息；
- 最终报告。

#### 待我审批

- 动作内容；
- 对象和影响范围；
- 风险等级；
- 预计收益；
- 前置条件；
- 回滚方案；
- 审批记录。

#### 主动发现

- 紧急风险；
- 待处理问题；
- 节能机会；
- 信息提示。

#### 历史与报告

- 日报、周报、月报；
- 已完成调查；
- 执行与验证结果；
- 用户反馈；
- 历史相似案例。

### 7.3 业务页面内联 AI

| 页面 | 内联能力 |
|---|---|
| Dashboard | 今日三件事、值班摘要、风险解释、管理层摘要 |
| Assets | 设备健康摘要、完整问题链、历史故障、维护建议 |
| FDD | 解释诊断、检查证据、比较根因、生成现场排查步骤 |
| Alarms / Work Orders | 告警聚类、优先级、工单草稿、交接摘要、SLA 风险 |
| Optimize | 收益风险解释、执行前提、审批摘要、验证计划 |
| Energy | 年/月/周/日异常解释、增量分解、设备钻取、预测与基线 |
| Cost | 峰平谷费用、节能收益、ROI、碳影响和管理报告 |
| System | 数据源状态、权限说明、规则解释、审计检索 |

### 7.4 对象级快捷动作

每个对象统一提供以下可选入口：

```text
询问 AI
解释这个问题
调查根因
评估影响
查看证据
生成处理方案
创建工单草稿
验证处理结果
```

入口必须携带标准对象引用和当前页面上下文，避免用户重复输入对象名称和时间范围。

---

## 8. 三个一期标杆场景

### 8.1 今日运维问题总览

#### 用户目标

在 30 秒内知道今天最需要处理的三件事。

#### 输入

- 当前站点；
- 活跃告警；
- 高风险 FDD；
- SLA 风险工单；
- 当前设备健康；
- 当日能耗异常；
- 数据源质量。

#### 输出

每个问题必须包含：

- 问题标题；
- 严重度；
- 影响对象；
- 一句话原因；
- 关键证据；
- 当前状态；
- 建议下一步；
- 深链。

### 8.2 单设备完整问题调查

#### 用户目标

从设备详情直接调查“为什么异常”。

#### 调查步骤

1. 读取资产信息和测点完整率；
2. 获取当前和历史遥测摘要；
3. 查询关联告警；
4. 查询 FDD 诊断和证据；
5. 查询工单和维修历史；
6. 查询优化建议和最近操作；
7. 检索设备手册和 SOP；
8. 形成原因假设与排查顺序。

#### 输出结构

- 当前现象；
- 已确认事实；
- 根因候选及置信度；
- 支持与反证；
- 缺失数据；
- 现场排查步骤；
- 预计影响；
- 可创建的工单草稿。

### 8.3 能耗异常跨模块调查

#### 用户目标

解释某日、某周或某月能耗为什么变化。

#### 调查步骤

1. 选择正确的时间口径；
2. 与基线、上期和去年同期比较；
3. 分解到建筑、系统和设备；
4. 区分负荷增长与效率下降；
5. 关联天气、日程、入住率或生产计划；
6. 关联告警、FDD、工单和人工操作；
7. 计算费用和碳影响；
8. 输出节能机会与验证方式。

#### 输出要求

不得只说“某设备能耗高”。必须说明增量、占比、时间段、证据来源和可验证动作。

---

## 9. Agent 能力地图

### 9.1 能力分层

| 层级 | 能力 | 示例 |
|---|---|---|
| L0 感知 | 获取当前上下文和对象状态 | 当前设备、页面筛选、告警数量 |
| L1 查询 | 单工具或简单组合查询 | 查询工单状态、读取遥测摘要 |
| L2 分析 | 确定性比较与异常识别 | 同期比较、增量分解、SLA 风险 |
| L3 调查 | 多步骤计划、假设和证据链 | 夜间能耗调查、设备效率调查 |
| L4 建议 | 生成处理方案和 Proposed Action | 工单草稿、优化建议草稿 |
| L5 执行 | 经授权调用写工具 | 创建工单、提交审批 |
| L6 验证 | 观察执行后的业务和指标结果 | 验证告警恢复、节能效果 |

一期应完成 L0–L3，并为 L4–L5 的低风险业务动作准备协议。

### 9.2 协调 Agent 与领域能力

系统采用一个运维协调 Agent 组织多个领域能力：

- 资产与拓扑能力；
- 遥测与运行能力；
- FDD 能力；
- 告警与工单能力；
- 能耗与成本能力；
- 优化能力；
- 知识检索能力；
- 权限、审批和审计能力。

一期不要求每个领域部署独立模型。领域能力首先应实现为受控工具和确定性服务，协调 Agent 负责选择和组合。

---

## 10. 总体技术架构

```text
React Web
├── Global AI Drawer
├── /ai Agent Workspace
├── Inline AI Entrypoints
└── Agent Client SDK
        │ SSE / WebSocket / REST
        ▼
Agent Runtime / Orchestrator
├── Identity & Context Resolver
├── Planner
├── Tool Router
├── Approval Manager
├── Task State Machine
├── Response Composer
└── Audit / Observability
        │
        ▼
Tool Gateway
├── Asset Tools
├── Telemetry & Analytics Tools
├── Alarm / FDD / Work Order Tools
├── Energy / Cost Tools
├── Optimization Tools
├── Knowledge Retrieval Tools
└── Governance Tools
        │
        ▼
Domain Services & Data
├── Asset Service
├── Time-series / Energy Analytics
├── FDD Service
├── Alarm / Work Order Service
├── Optimization Service
├── Document / Vector Search
├── Event Bus
└── Audit Store
```

### 10.1 前端职责

- 收集页面与对象上下文；
- 展示消息、任务、证据、动作和审批；
- 管理流式连接和重连；
- 提供深链和焦点恢复；
- 在执行前展示结构化预览；
- 不持有模型密钥；
- 不直接决定权限；
- 不直接执行高风险写操作。

### 10.2 Agent Runtime 职责

- 用户身份和 scope 解析；
- 上下文归一化；
- 任务规划；
- 工具选择和参数生成；
- 工具权限检查；
- 审批状态管理；
- 任务暂停、恢复和取消；
- 证据与结论组合；
- 模型调用；
- 审计和可观测性。

### 10.3 Tool Gateway 职责

- 统一工具注册；
- 输入 schema 校验；
- 权限和站点范围校验；
- 幂等键；
- 超时和重试；
- 敏感字段脱敏；
- 写操作审批令牌校验；
- 结果结构化；
- 工具级审计。

### 10.4 确定性分析服务职责

- 时序聚合与降采样；
- 能耗口径计算；
- 基线与同期比较；
- 异常检测；
- 设备和系统贡献分解；
- COP 加权；
- 收益和风险计算；
- 数据质量评分。

模型不得替代这些服务。

---

## 11. 统一上下文协议

### 11.1 上下文信封

```ts
export interface AgentContextEnvelope {
  requestId: string;
  sessionId: string;
  taskId?: string;
  user: AgentUserContext;
  application: ApplicationContext;
  scope: AnalysisScope;
  focus?: AgentObjectRef;
  selections?: AgentObjectRef[];
  metrics?: Record<string, number | string | boolean | null>;
  permissions: AgentPermissionSnapshot;
  client: ClientContext;
}
```

### 11.2 用户上下文

```ts
export interface AgentUserContext {
  userId: string;
  displayName: string;
  role: 'demo' | 'ops' | 'rd';
  siteIds: string[];
  scopes: string[];
  locale: 'zh-CN';
  timezone: string;
}

export interface AgentPermissionSnapshot {
  role: 'demo' | 'ops' | 'rd';
  permittedRoutes: string[];
  permittedTools: string[];
  scopes: string[];
  evaluatedAt: string;
}

export interface ClientContext {
  locale: 'zh-CN';
  timezone: string;
  viewport: 'desktop' | 'tablet' | 'mobile';
  appVersion: string;
}
```

### 11.3 应用上下文

```ts
export interface ApplicationContext {
  route: string;
  pageKey: string;
  pageTitle: string;
  query: Record<string, string>;
  buildingId?: string;
  energyType?: string;
  compareMode?: string;
  drawer?: {
    object?: AgentObjectRef;
  };
}
```

### 11.4 分析范围

```ts
export interface AnalysisScope {
  siteId: string;
  buildingIds?: string[];
  zoneIds?: string[];
  systemTypes?: string[];
  deviceIds?: string[];
  period?: {
    start: string;
    end: string;
    granularity?: 'minute' | 'hour' | 'day' | 'week' | 'month' | 'year';
    timezone: string;
  };
}
```

### 11.5 标准对象引用

```ts
export type AgentObjectType =
  | 'site'
  | 'building'
  | 'zone'
  | 'asset'
  | 'meter'
  | 'alarm'
  | 'diagnosis'
  | 'workOrder'
  | 'optimization'
  | 'energyPeriod'
  | 'costReport'
  | 'dataSource'
  | 'document'
  | 'agentTask';

export interface AgentObjectRef {
  type: AgentObjectType;
  id: string;
  label: string;
  route?: string;
  siteId?: string;
  metadata?: Record<string, string | number | boolean | null>;
}
```

### 11.6 上下文优先级

发生冲突时按以下优先级处理：

```text
用户本轮明确指定
> 已确认任务范围
> 当前焦点对象
> 页面筛选和 URL
> 用户默认站点
> 系统默认值
```

Agent 不得静默扩大用户的站点、建筑、时间或设备范围。扩大范围需要说明并获得确认。

---

## 12. Agent 任务模型与状态机

### 12.1 任务类型

```ts
export type AgentTaskType =
  | 'consultation'
  | 'investigation'
  | 'report'
  | 'proposed_action'
  | 'execution'
  | 'verification'
  | 'scheduled_inspection';

export type AgentTaskStatus =
  | 'draft'
  | 'planning'
  | 'running'
  | 'waiting_for_input'
  | 'waiting_for_approval'
  | 'executing'
  | 'verifying'
  | 'completed'
  | 'failed'
  | 'cancelled';
```

### 12.2 状态机

```text
DRAFT → PLANNING → RUNNING → COMPLETED
                         ├→ WAITING_FOR_INPUT → RUNNING
                         ├→ WAITING_FOR_APPROVAL → EXECUTING → VERIFYING → COMPLETED
                         ├→ FAILED
                         └→ CANCELLED

PLANNING、WAITING_FOR_INPUT、WAITING_FOR_APPROVAL、EXECUTING、VERIFYING
也都可以根据错误或用户操作进入 FAILED / CANCELLED。
```

状态说明：

| 状态 | 含义 |
|---|---|
| DRAFT | 已创建但尚未开始 |
| PLANNING | 正在生成和校验调查计划 |
| RUNNING | 正在调用只读工具和分析 |
| WAITING_FOR_INPUT | 缺少必要范围、参数或用户判断 |
| WAITING_FOR_APPROVAL | 已形成写操作，等待授权 |
| EXECUTING | 正在执行已批准动作 |
| VERIFYING | 正在观察执行结果和副作用 |
| COMPLETED | 已形成最终结论或验证完成 |
| FAILED | 因工具、数据、模型或权限失败 |
| CANCELLED | 用户或系统取消 |

### 12.3 任务数据结构

```ts
export interface AgentTask {
  id: string;
  type: AgentTaskType;
  title: string;
  goal: string;
  status: AgentTaskStatus;
  scope: AnalysisScope;
  focus?: AgentObjectRef;
  plan: AgentPlanStep[];
  findings: AgentFinding[];
  proposedActions: ProposedAction[];
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
  parentTaskId?: string;
  idempotencyKey?: string;
}
```

### 12.4 计划步骤

每个计划步骤必须可观察：

```ts
export interface AgentPlanStep {
  id: string;
  title: string;
  purpose: string;
  status: 'pending' | 'running' | 'completed' | 'skipped' | 'failed';
  toolNames?: string[];
  startedAt?: string;
  completedAt?: string;
  summary?: string;
}
```

复杂调查应向用户展示简化后的计划，但不显示模型私有推理过程。展示内容仅包括目标、步骤、工具类别、发现和失败原因。

---

## 13. 证据、结论与响应协议

### 13.1 证据结构

```ts
export interface AgentEvidence {
  id: string;
  kind: 'telemetry' | 'energy' | 'alarm' | 'diagnosis' | 'workOrder' | 'document' | 'audit' | 'calculation';
  title: string;
  summary: string;
  source: string;
  period?: { start: string; end: string };
  objects?: AgentObjectRef[];
  values?: Record<string, number | string | null>;
  quality: 'good' | 'partial' | 'poor';
  retrievedAt: string;
}
```

### 13.2 发现结构

```ts
export interface AgentFinding {
  id: string;
  statement: string;
  classification: 'fact' | 'algorithm_result' | 'inference' | 'hypothesis' | 'unknown';
  confidence?: number;
  severity?: 'info' | 'warning' | 'critical';
  evidenceIds: string[];
  counterEvidenceIds?: string[];
  missingData?: string[];
}
```

### 13.3 最终响应结构

重要调查结果应按以下顺序呈现：

1. 结论摘要；
2. 影响范围；
3. 关键证据；
4. 根因或候选原因；
5. 不确定性与缺失数据；
6. 建议动作；
7. 验证计划；
8. 相关对象深链。

### 13.4 禁止表达

- 没有数据却说“已确认”；
- 把模拟聚合写成真实计量；
- 把模型推测写成 FDD 结论；
- 把相关性写成因果关系；
- 工具失败后继续生成成功结果；
- 没有权限时暗示用户已经执行动作；
- 没有基线时给出精确节能百分比。

---

## 14. 工具注册中心

### 14.1 工具标准描述

```ts
export interface AgentToolDefinition<Input, Output> {
  name: string;
  version: string;
  description: string;
  domain: string;
  inputSchema: unknown;
  outputSchema: unknown;
  sideEffect: 'none' | 'business_write' | 'control_write';
  requiredAction: string;
  requiredSubject: string;
  approvalPolicy: 'none' | 'confirm' | 'single_approval' | 'dual_approval';
  timeoutMs: number;
  idempotent: boolean;
  auditLevel: 'standard' | 'sensitive' | 'critical';
}
```

### 14.2 一期只读工具

| 工具 | 作用 | 权限 |
|---|---|---|
| `get_asset` | 获取设备台账和当前状态 | view asset |
| `get_asset_tree` | 获取站点、建筑、区域、系统拓扑 | view assets |
| `get_asset_telemetry_summary` | 获取指定周期遥测摘要 | view asset |
| `get_asset_telemetry_series` | 获取降采样时序 | view asset |
| `get_active_alarms` | 查询活动告警 | view alarms |
| `get_alarm_context` | 获取单告警关联信息 | view alarms |
| `get_fdd_diagnoses` | 查询 FDD 诊断 | view fdd |
| `get_diagnosis_evidence` | 获取诊断证据和规则命中 | view diagnosis |
| `get_work_orders` | 查询工单和 SLA | view workOrder |
| `get_work_order_history` | 查询工单流程和处理记录 | view workOrder |
| `get_optimization_suggestions` | 查询优化建议 | view optimization |
| `get_energy_summary` | 获取周期能耗摘要 | view energy |
| `compare_energy_periods` | 同期、环比和基线比较 | view energy |
| `decompose_energy_delta` | 分解建筑、系统、设备增量 | view energy |
| `get_cost_summary` | 获取费用和峰平谷结构 | view costReport |
| `get_operation_schedule` | 获取日程和营业时间 | view asset |
| `get_data_quality` | 获取计量和测点质量 | view systemConfig |
| `search_knowledge` | 检索手册、SOP 和案例 | view corresponding scope |
| `get_audit_events` | 查询相关人工与系统操作 | view system |

### 14.3 二期低风险写工具

| 工具 | 副作用 | 审批 |
|---|---|---|
| `create_work_order_draft` | 创建未提交草稿 | 用户确认 |
| `create_work_order` | 创建工单 | 用户确认或单人审批 |
| `update_work_order` | 补充说明、优先级或指派 | 用户确认 |
| `transition_work_order` | 流转工单状态 | 用户确认，受角色限制 |
| `create_optimization_draft` | 创建优化草稿 | 用户确认 |
| `submit_optimization` | 提交优化审批 | 单人审批 |
| `add_investigation_note` | 写入调查记录 | 用户确认 |
| `generate_report` | 生成平台报告对象 | 无或用户确认 |

### 14.4 高风险控制工具

以下工具不得进入一期和二期：

- `start_device`；
- `stop_device`；
- `set_temperature_setpoint`；
- `set_pressure_setpoint`；
- `set_frequency`；
- `apply_control_strategy`；
- `change_alarm_threshold`；
- `bulk_schedule_update`。

未来开放时必须具备控制写权限、双人审批、参数上下限、冲突检测、执行窗口、灰度范围、回滚策略和实时验证。

### 14.5 工具调用要求

每次工具调用必须携带：

- `requestId`；
- `taskId`；
- `userId`；
- `siteId`；
- `scopeSnapshot`；
- `idempotencyKey`（写操作）；
- `approvalToken`（需要审批时）；
- `reason`；
- `expectedOutput`。

---

## 15. Proposed Action 与审批

### 15.1 动作结构

```ts
export interface VerificationPlan {
  observationWindow: {
    startOffsetMinutes: number;
    durationMinutes: number;
  };
  successCriteria: string[];
  failureCriteria?: string[];
  metrics: string[];
  rollbackOnFailure: boolean;
}

export interface ProposedAction {
  id: string;
  taskId: string;
  toolName: string;
  title: string;
  description: string;
  targetObjects: AgentObjectRef[];
  parameters: Record<string, unknown>;
  expectedOutcome: string;
  risks: string[];
  prerequisites: string[];
  rollbackPlan?: string;
  verificationPlan: VerificationPlan;
  riskLevel: 'low' | 'medium' | 'high' | 'critical';
  approvalPolicy: 'confirm' | 'single_approval' | 'dual_approval';
  status: 'draft' | 'pending' | 'approved' | 'rejected' | 'executed' | 'failed' | 'rolled_back';
}
```

### 15.2 自治等级

| 等级 | 能力 | 默认策略 |
|---|---|---|
| A0 | 解释和总结 | 直接执行只读工具 |
| A1 | 调查和建议 | 自动执行只读调查，用户可取消 |
| A2 | 低风险业务写入 | 显式确认后执行 |
| A3 | 中高风险业务或配置变更 | 指定角色审批 |
| A4 | 设备控制 | 双人审批、灰度、回滚和验证 |

当前系统目标为 A0–A1；第二阶段逐步开放 A2。

### 15.3 审批界面要求

审批卡必须展示：

- 将执行什么；
- 对哪些对象执行；
- 参数；
- 为什么建议；
- 证据；
- 预期收益；
- 风险和副作用；
- 前置条件；
- 回滚方式；
- 验证窗口；
- 申请人和 Agent 任务；
- 权限检查结果。

普通“确认/取消”弹窗不能用于高风险动作。

---

## 16. 权限模型

### 16.1 权限继承

Agent 的可见范围等于用户在平台中的可见范围，不创建额外的超级权限。

执行顺序：

```text
用户身份认证
→ 站点范围校验
→ 角色权限校验
→ 对象级 scope 校验
→ 工具权限校验
→ 审批策略校验
→ 参数安全校验
→ 执行
```

### 16.2 当前角色映射

| 能力 | Demo | Ops | RD |
|---|---:|---:|---:|
| 查看 Dashboard / BigScreen | 是 | 是 | 是 |
| 使用完整 AI 工作台 | 否 | 视产品策略 | 是 |
| 查询资产、FDD、工单 | 否 | 是 | 是 |
| 查询能耗、成本、优化 | 否 | 否 | 是 |
| 创建工单 | 否 | 是 | 是 |
| 流转工单 | 否 | 是 | 是 |
| 审批/驳回/下发优化 | 否 | 否 | 是 |
| 管理资产和系统配置 | 否 | 否 | 是 |
| 设备控制 | 否 | 否 | 当前不开放 |

需要注意：当前代码中 `ops` 角色没有 `/ai` 页面查看权限。若未来要让运维人员使用 AI，必须在产品评审后调整 `permissions.ts`，不能由 Agent 绕过路由权限。

### 16.3 防止间接越权

- 无权限页面的数据不得出现在模型上下文；
- 搜索知识时必须按站点和文档 ACL 过滤；
- Agent 不得通过汇总结果泄露被禁止对象的名称和数量；
- 工具结果在返回模型前再次执行字段级脱敏；
- 深链生成前检查 `canViewPath` 和对象 scope；
- 历史会话在用户权限变化后重新校验。

---

## 17. 数据与知识体系

### 17.1 时序与计量数据

Agent 不直接消费全量原始时序。分析服务应提供：

- 最新值；
- 周期统计；
- 最大、最小、平均、P95；
- 运行时长；
- 启停次数；
- 趋势和变化点；
- 缺测率；
- 同期和基线差异；
- 设备和系统贡献。

### 17.2 业务关系图

应建立可查询的关系：

```text
站点 → 建筑 → 分区 → 系统 → 设备 → 测点
设备 ↔ 告警 ↔ FDD ↔ 工单 ↔ 优化建议
设备/建筑 ↔ 能耗周期 ↔ 成本 ↔ 报告
对象 ↔ 操作记录 ↔ 文档 ↔ 历史案例
```

一期可通过关系查询服务实现，不要求立刻引入图数据库。

### 17.3 知识库

知识来源包括：

- 设备手册；
- 设计和调试文档；
- 运维 SOP；
- 控制逻辑说明；
- 告警和 FDD 规则说明；
- 厂商技术通告；
- 历史维修记录；
- 企业能源管理制度；
- 已验证的调查报告。

每个文档片段必须保存：

- 文档 ID 和版本；
- 标题；
- 来源；
- 生效时间；
- 适用设备或站点；
- ACL；
- 引用位置；
- 索引时间。

### 17.4 长期记忆

长期记忆只保存结构化、经过确认的组织知识，例如：

- 某设备已知缺陷；
- 某站点特殊日程；
- 经确认的故障原因；
- 工单完成后的验证结果；
- 已批准的操作偏好；
- 已验证的优化策略。

普通聊天文本不能自动成为长期事实。写入长期记忆需要来源、确认人、有效期和适用范围。

---

## 18. API 与事件协议

### 18.1 建议端点

```text
POST   /api/v1/agent/sessions
GET    /api/v1/agent/sessions/:sessionId
POST   /api/v1/agent/sessions/:sessionId/messages

POST   /api/v1/agent/tasks
GET    /api/v1/agent/tasks
GET    /api/v1/agent/tasks/:taskId
POST   /api/v1/agent/tasks/:taskId/cancel
POST   /api/v1/agent/tasks/:taskId/resume
GET    /api/v1/agent/tasks/:taskId/events

POST   /api/v1/agent/actions/:actionId/approve
POST   /api/v1/agent/actions/:actionId/reject

GET    /api/v1/agent/inbox
POST   /api/v1/agent/inbox/:itemId/acknowledge
POST   /api/v1/agent/inbox/:itemId/dismiss

GET    /api/v1/agent/reports
GET    /api/v1/agent/reports/:reportId
```

### 18.2 流式事件

SSE 或 WebSocket 事件建议统一为：

```ts
export type AgentStreamEvent =
  | { type: 'message.delta'; content: string }
  | { type: 'message.completed'; messageId: string }
  | { type: 'task.status'; status: AgentTaskStatus }
  | { type: 'plan.updated'; steps: AgentPlanStep[] }
  | { type: 'tool.started'; executionId: string; toolName: string }
  | { type: 'tool.completed'; executionId: string; summary: string }
  | { type: 'tool.failed'; executionId: string; errorCode: string }
  | { type: 'finding.created'; finding: AgentFinding }
  | { type: 'action.proposed'; action: ProposedAction }
  | { type: 'approval.required'; actionId: string }
  | { type: 'verification.updated'; summary: string }
  | { type: 'error'; code: string; message: string };
```

### 18.3 幂等与恢复

- 每个消息提交携带 `clientMessageId`；
- 每个写工具携带 `idempotencyKey`；
- 流式事件携带递增 `eventId`；
- 前端断线后使用 `Last-Event-ID` 恢复；
- 任务状态以后端为准；
- 浏览器刷新后可恢复任务和待审批动作；
- 同一任务不能因重试重复创建工单或优化建议。

---

## 19. 前端设计要求

### 19.1 会话与任务分离

会话是用户交流容器，任务是可执行和可追踪的工作单元。一个会话可以包含多个任务，一个任务可跨多个会话继续。

### 19.2 消息类型

前端至少支持：

- 普通文本；
- 结构化摘要；
- 对象引用；
- 证据卡；
- 调查计划；
- 任务进度；
- Proposed Action；
- 审批结果；
- 验证结果；
- 错误和降级提示。

### 19.3 上下文显示

输入框上方应显示可移除的上下文标签：

```text
总部园区
1 号楼
冷水机组 CH-02
2026-07-10 00:00–24:00
日度能耗分析
```

用户必须能查看 Agent 实际使用的范围，防止“当前页面”和“任务范围”不一致。

### 19.4 任务进度

复杂调查不应用持续滚动文本代替状态。应显示：

- 当前步骤；
- 已完成步骤；
- 正在调用的数据类别；
- 已发现的问题；
- 是否等待用户或审批；
- 取消和继续入口。

### 19.5 证据卡

证据卡应包含：

- 标题；
- 数值或结论；
- 时间范围；
- 数据质量；
- 来源；
- 关联对象；
- 打开原页面入口。

### 19.6 空状态和失败状态

Agent 必须明确区分：

- 没有异常；
- 没有数据；
- 没有权限；
- 工具失败；
- 数据质量不足；
- 模型超时；
- 任务被取消；
- 需要用户补充范围。

---

## 20. 主动 Agent 与事件收件箱

### 20.1 主动任务来源

- 固定周期巡检；
- FDD 高风险事件；
- 告警聚类；
- 能耗基线偏差；
- SLA 风险；
- 优化验证失败；
- 数据源或测点质量下降。

### 20.2 主动事件准入

主动事件必须满足：

- 有明确触发条件；
- 有证据；
- 有影响范围；
- 有去重键；
- 有冷却时间；
- 有优先级；
- 有下一步动作；
- 能被忽略、静默或关闭。

### 20.3 防止 AI 告警风暴

- 同一根因合并为一个事件；
- 伴随告警作为证据，不重复提醒；
- 同一对象在冷却期内只更新原事件；
- 低优先级节能机会进入收件箱，不弹窗；
- 用户可按站点、类型和严重度配置订阅；
- 主动任务数量和噪声率纳入质量指标。

---

## 21. 审计、可观测性与安全

### 21.1 Agent 审计记录

每次任务至少记录：

- 用户原始请求；
- 标准化目标和范围；
- Agent 计划；
- 模型和策略版本；
- 工具调用参数摘要；
- 工具返回摘要和数据版本；
- 证据和结论；
- Proposed Action；
- 审批人和审批时间；
- 实际执行结果；
- 验证结果；
- 用户反馈；
- 错误和重试。

### 21.2 可观测指标

#### 可靠性

- 任务成功率；
- 工具调用成功率；
- 流式连接中断率；
- 恢复成功率；
- 写操作幂等冲突数。

#### 性能

- 首 token 延迟；
- 首个有效发现时间；
- 任务完成时间；
- 工具延迟；
- 模型 token 和成本。

#### 质量

- 有证据结论比例；
- 错误对象引用率；
- 用户纠正率；
- 建议采纳率；
- 调查复用率；
- 主动事件噪声率。

#### 安全

- 越权工具调用拦截数；
- 无审批写操作数；
- 高风险参数拒绝数；
- 未验证执行数；
- 审计缺失数。

### 21.3 敏感信息

- 模型密钥只在服务端；
- 工具结果按角色和字段脱敏；
- Prompt 和日志不得包含密码、Token 和密钥；
- 文档检索必须执行 ACL；
- 模型供应商的数据保留策略需要经过安全评审；
- 生产环境不得把完整原始遥测、个人信息和敏感配置发送给未批准的外部模型。

---

## 22. 失败、降级与回退

### 22.1 模型不可用

降级为：

- 确定性摘要；
- 预定义查询；
- 对象深链；
- 任务失败说明；
- 不执行任何写操作。

### 22.2 单个工具失败

Agent 应：

1. 标记失败步骤；
2. 判断是否可用替代工具；
3. 降低结论置信度；
4. 明确缺失的数据；
5. 禁止基于缺失结果执行动作。

### 22.3 数据质量不足

输出应包含数据质量评分和建议修复的数据源，不得生成精确结论。

### 22.4 权限不足

说明当前权限限制，并提供用户有权访问的替代路径。不得泄露被限制对象的内容。

### 22.5 写操作失败

- 返回真实错误码；
- 检查是否部分成功；
- 不自动重复非幂等动作；
- 提供回滚或人工处理建议；
- 记录完整审计。

---

## 23. 评估体系

### 23.1 离线评估集

建立智慧能源场景测试集，至少包括：

- 正常运行；
- 单设备 COP 下降；
- 夜间能耗异常；
- 告警风暴；
- 测点缺失；
- 无权限对象；
- 模拟数据；
- 工具超时；
- 相互冲突的证据；
- 用户要求执行高风险动作。

每个样例定义：

- 正确范围；
- 必须调用的工具；
- 禁止调用的工具；
- 关键事实；
- 可接受结论；
- 必须披露的不确定性；
- 权限和审批要求。

### 23.2 在线产品指标

| 目标 | 指标 |
|---|---|
| 提高定位效率 | 平均问题定位时间、首次有效发现时间 |
| 提高运维闭环 | 工单创建时间、首次处理成功率、重复故障率 |
| 提升能源绩效 | 已验证节能量、费用节省、非营业能耗下降 |
| 保证可信度 | 有证据回答比例、人工纠正率、错误引用率 |
| 控制风险 | 越权数、无审批写入数、未验证执行比例 |

“对话次数”和“消息数量”只能作为使用量指标，不能作为价值指标。

---

## 24. 分阶段实施计划

### 阶段 0：协议和基础设施

目标：建立 Agent 可持续演进的基础，不急于增加表面功能。

交付：

- 上下文协议；
- 对象引用协议；
- 任务和流式事件协议；
- 工具注册规范；
- Agent 审计模型；
- Runtime 接口骨架；
- 前端 Agent Client；
- 权限快照和站点范围校验。

验收：

- 刷新后可恢复会话和任务；
- 所有对象引用可深链；
- 所有工具调用有审计；
- 无权限工具无法注册或执行。

### 阶段 1：可信只读助手

目标：让 AI 真正理解平台并给出有证据的回答。

交付：

- 今日运维问题总览；
- 单设备完整问题摘要；
- 能耗周期解释；
- 资产、告警、FDD、工单、优化、能耗、成本只读工具；
- 证据卡和数据质量；
- 知识检索一期；
- `/ai` 任务列表和报告详情。

验收：

- 重要结论 100% 关联证据；
- 引用对象 100% 使用真实 ID；
- 无权限数据泄露为 0；
- 模拟数据明确标识；
- 工具失败不会生成成功结论。

### 阶段 2：调查型 Agent

目标：完成跨模块、多步骤调查。

交付：

- 调查计划和状态机；
- 假设、证据和反证；
- 夜间能耗调查；
- 设备效率下降调查；
- 告警聚类和根因链；
- 调查报告；
- 暂停、恢复、取消和重试。

验收：

- 三个标杆场景端到端通过；
- 调查范围可见且可修改；
- 缺失数据和不确定性明确；
- 任务可刷新恢复；
- 浏览器断线后不重复调用写工具。

### 阶段 3：受控业务执行

目标：推动业务闭环，但不控制设备。

交付：

- 工单草稿和创建；
- 工单补充、指派和流转；
- 优化草稿和提交审批；
- Proposed Action；
- 审批中心；
- 执行回执；
- 验证任务。

验收：

- 所有写操作有用户确认；
- 权限和审批矩阵生效；
- 幂等性通过；
- 执行结果可追溯；
- 关闭问题前必须完成验证。

### 阶段 4：主动运维

目标：AI 持续发现高价值问题。

交付：

- 周期巡检；
- 主动事件收件箱；
- 去重、合并、冷却和订阅；
- 自动日报周报；
- 节能机会和风险排序；
- 用户反馈闭环。

验收：

- 主动事件有证据和去重键；
- 噪声率达到产品目标；
- 用户可以静默和关闭；
- 不产生新的告警风暴。

### 阶段 5：受控优化与控制

目标：在严格治理下参与设备运行策略。

前置条件：

- 真实控制接口；
- 完整 RBAC 与对象 scope；
- 双人审批；
- 沙箱或数字孪生验证；
- 参数安全边界；
- 灰度执行；
- 实时监控；
- 自动回滚；
- 效果核算。

没有满足全部前置条件，不得开放设备控制工具。

---

## 25. 建议的代码与服务边界

### 25.1 前端建议目录

```text
src/agent/
├── client/
│   ├── AgentClient.ts
│   ├── stream.ts
│   └── types.ts
├── context/
│   ├── ApplicationContextProvider.tsx
│   ├── objectRefs.ts
│   └── scope.ts
├── session/
│   ├── store.ts
│   └── persistence.ts
├── tasks/
│   ├── TaskPanel.tsx
│   ├── TaskTimeline.tsx
│   └── TaskStatus.tsx
├── evidence/
│   ├── EvidenceCard.tsx
│   └── FindingCard.tsx
├── actions/
│   ├── ProposedActionCard.tsx
│   └── ApprovalPanel.tsx
├── inline/
│   └── AskAgentButton.tsx
└── workspace/
    └── AgentWorkspace.tsx
```

当前 `src/ai` 可以逐步迁移或重命名为 `src/agent`，但不应一次性重写已经稳定的共享会话和 UI。

### 25.2 后端建议模块

```text
agent/
├── agent.controller
├── agent.gateway
├── session.service
├── task.service
├── orchestrator.service
├── planner.service
├── tool-registry.service
├── approval.service
├── verification.service
├── audit.service
├── context-resolver.service
└── providers/
```

工具本身应留在各领域服务中，通过 Tool Gateway 暴露，避免 Agent 模块复制业务逻辑。

---

## 26. 第一阶段详细交付清单

### 26.1 产品

- `/ai` 今日关注页；
- 任务列表和任务详情；
- 全局抽屉当前对象上下文；
- 设备、FDD、工单、能耗页面“询问 AI”入口；
- 证据卡、对象卡和数据质量提示；
- 三个标杆场景的交互原型；
- 无数据、无权限、失败和降级状态。

### 26.2 前端

- `AgentContextEnvelope`；
- `AgentObjectRef`；
- Agent Client 与 SSE 恢复；
- 会话/任务共享 Store；
- 页面焦点对象注册 API；
- 消息、任务、证据和动作渲染器；
- URL 深链；
- 角色权限适配；
- 浏览器审计覆盖。

### 26.3 后端

- Session、Task、Message 数据模型；
- Runtime 流式端点；
- 身份和 scope 解析；
- 工具注册中心；
- 只读工具一期；
- Agent 审计；
- 模型 Provider 抽象；
- 工具超时和重试；
- Mock 和真实实现切换。

### 26.4 数据与算法

- 遥测摘要接口；
- 能耗同期比较；
- 能耗增量分解；
- 数据质量评分；
- FDD 证据接口；
- 告警关联；
- 设备历史问题链；
- 文档索引和 ACL。

### 26.5 测试

- 权限越权测试；
- 工具 schema 测试；
- 流式断线恢复；
- 幂等测试；
- 模型不可用降级；
- 数据缺失；
- 模拟数据标识；
- 三个标杆场景；
- Prompt Injection 与工具注入测试；
- 审计完整性测试。

---

## 27. 开放决策

以下问题需要在实施前由产品、技术和安全共同确认：

1. `ops` 角色是否开放 `/ai`，以及开放到何种范围；
2. Agent Runtime 继续使用 CopilotKit，还是增加自有 Orchestrator 层；
3. 任务、消息和审计的持久化数据库；
4. 知识检索使用现有搜索、向量库还是混合检索；
5. 模型供应商、数据保留和区域合规；
6. 主动任务运行方式和事件总线；
7. 能耗基线、天气和入住率数据来源；
8. 二期允许的第一批写工具；
9. 审批人来源和双人审批规则；
10. Agent 生成报告的正式归档格式。

---

## 28. 最终验收原则

AI Agent 只有同时满足以下条件，才算真正融入智慧能源系统：

- 能理解当前页面和真实业务对象；
- 能跨资产、遥测、告警、FDD、工单、优化和能耗调查；
- 结论有证据、口径和不确定性；
- 用户权限始终生效；
- 写操作经过明确审批；
- 每次动作可审计；
- 执行后有验证；
- 模型不可用时系统仍可安全降级；
- 不制造没有依据的精确数字；
- 不把聊天热闹程度当作产品价值。

最终目标不是让用户“多和 AI 对话”，而是让问题更快被发现、原因更快被定位、动作更安全地执行、能源效果被真实验证。
