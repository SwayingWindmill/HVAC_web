# 规则引擎表达式、滑窗斜率、多级模板与联动拓扑选型评审（2026-11-17）

围绕规则引擎与场景告警 P2-1 演进，对照仓库既有参照实现及流式/规则引擎标杆项目源码与设计模型，沉淀结构化表达式引擎、有状态斜率窗口、多级模板覆盖、有向无环拓扑防环及无副作用试算的采纳、改造与拒绝决定。

## 一、参考项目与外部标杆基线

1. **ThingsBoard `v4.4`**（commit `6d46786579c8b29caf5102f95ddb133674bed68b`，见 [thingsboard-v4.4.md](./references/thingsboard-v4.4.md)；主体采用 BUSL-1.1 协议，仅作架构与概念参照，不直接复用代码）：
   - 通过 `CalculatedFieldType.ALARM` 把规则直接挂到实体并以声明式配置替代自由脚本；
   - 以 `maxRuleNodeExecutionsPerMessage` 限制单条消息在规则链里的最大跳数防止环路死循环。
2. **LF Edge eKuiper `v2.0.0`**：在流式规则算子中为时序信号保留有界内存采样窗口（滑动计数窗与时间窗），在多点连续抵达时计算变化率与斜率，避免仅凭单点毛刺误报或丢失趋势。
3. **RuleGo `v0.26.0`**：采用轻量化内存有向无环图（DAG）执行编排，在加载发布前静态检查环路并约束最大跨节点跳数。
4. **Prometheus Alertmanager `v0.27.0`**：区分规则判定状态与路由通知抑制关系，通过持久化的 `hold` 持续期与静默抑制树减少高频抖动。
5. **`expr-lang/expr` `v1.16.9` 与 `cel-go` `v0.22.1`**：强调非图灵完备、静态类型约束、无副作用确定性求值与 AST 节点预算上限，严禁服务端通过动态脚本解释器或 `eval` 执行不可控外部代码。

## 二、方案对比与设计决定

### 1. 规则种类区分与确定性表达式求值（`RuleKind` 与 `ConditionExpression`）

- **采纳**：在 `AlarmPolicyRevision` 增加可选 `RuleKind`（`THRESHOLD` / `SLOPE` / `EXPRESSION`）及 `EffectiveRuleKind()`，新增封闭语法、零外部解释器依赖的递归下降布尔算术表达式求值器（`modules/alarm/pkg/alarmservice/expression.go`）。
- **改造**：结合暖通点位三态质量体系（可信 / 不可信 / 缺失），实现 Kleene 三值短路逻辑（`true || INDETERMINATE` 判定 `MATCHED`，`false && INDETERMINATE` 判定 `NOT_MATCHED`，其余阻断归类并保留 `qualityBlocker`）；将除零（`|divisor| < 1e-12`）收敛为 `INDETERMINATE`（`INVALID_INPUT:DIV_BY_ZERO`），AST 上限固定为 `MaxExpressionASTNodes = 64`、`MaxExpressionDepth = 16`，禁止循环、递归、网络 I/O 与自定义函数，仅开放 `abs`、`min`、`max` 与基本四则比较逻辑运算；新增字段全部标注 `omitempty`，保持既有多版策略规范化 SHA-256 `Digest` 字节一致。
- **拒绝**：直接嵌入 JavaScript 引擎、Go 源码反射求值或无约束通用表达式库，避免在单进程内引入内存爆炸、超时停顿及安全逃逸面。

### 2. 跨样本斜率突变窗与持久化状态（`ConditionSlope`）

- **采纳**：借鉴 eKuiper 滑动采样窗口思路，在 `AlarmPolicyRevision` 条件树支持 `SLOPE`（`Input`、`Operator`、`Delta`、`WindowSeconds` 范围为 `1..86400`），并在 `AlarmEvaluationState` 中维护有界有序采样数组 `SampleWindow`（`MaxSlopeSamplesPerRule = 32`）。
- **改造**：为满足多副本部署与进程重启后斜率连续性，新增幂等迁移 `modules/alarm/migrations/alarm-evaluation-slope-window.sql`，在 `alarm_runtime.alarm_evaluation_state` 增加 `sample_window jsonb` 列随规则评估事务原子读写，仅当窗口 `[asOf - windowSeconds, asOf]` 内存在至少两条时间严格递增的可信样本时才做差值判断；首点或不可信样本不触发告警，防止传感器瞬断或刚上线时误报。

### 3. 多级模板复用与场站覆写（`alarmrules`）

- **采纳**：在 `modules/alarm/pkg/alarmrules/rules.go` 支持 `templates` 基准规则定义（支持 `{device}` 占位符与共享默认值）及规则项按 `template` 引用继承、按字段覆写阈值/持续时间/严重度，编译期自动填充 `TemplateKey` 与已排序 `OverriddenFields`。
- **改造**：保持现有 `deploy/sites/songyaxuan.alarm-rules.json` 文件格式完全兼容且零改动，覆写结果在编译进不可变 `AlarmPolicyRevision` 时完成校验与摘要密封，不把运行时模板查找留在热评估路径上。

### 4. 联动拓扑环路检测、最大跳数与零副作用在线试算（`rulegraph` 与 `:simulate`）

- **采纳**：新增独立纯 Go 包 `modules/alarm/pkg/rulegraph`，在发布与模拟前构建规则依赖有向图，同时检查显式 `dependsOnRuleIds` 边与跨设备「写设备 -> 读设备」隐式因果边，输出明确环路路径（`ErrRuleCycleDetected`）并强制最大联动深度 `MaxLinkageHops = 3`（`ErrMaxLinkageHopsExceeded`）。
- **改造**：在平台网关新增 `POST /api/v1/alarm-rules/simulate`，直接复用线上的 `EvaluatePolicy` 与 `rulegraph.ValidateDAG`，对传入的 `2..32` 步时序阶跃/恢复/斜率样本在内存里演练评估过程，不写任何告警表、评估历史表或通知发件箱，也不改变运行期 `alarm_evaluation_state`。
