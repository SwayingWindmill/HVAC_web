# 10 诊断中心 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `10 诊断中心`  
> **Route intent：** `/sites/:siteId/diagnostics`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 FDD 页面、旧诊断页、旧 AI 调查页、旧 Ant/ProComponents 页面或旧设计稿。当前代码只可在实施阶段作为真实 Alarm / AFDD Finding / Rule / Model / Evidence / Device / Work / Verification / Permission / Route contract 的候选证据来源。

---

# 1. Primary Job

诊断中心的唯一核心任务是：

> **把“发生了什么”与“为什么发生”严格分层，让工程师围绕一个真实问题查看已验证事实、发布的 Finding、支持与反驳证据、候选 Hypothesis 和下一验证动作，并在有充分证据时将调查推进到 Work、Functional Verification 或最终 Root Cause。**

Diagnosis Center 是 **evidence-led investigation workspace**，不是：

- Alarm Center 的详情页；
- Rule Hit 列表；
- FDD engine 管理页；
- AI Chat 页面；
- “自动根因分析”营销页；
- Work Order 页面；
- Trend Analysis 的替代品；
- Control Center；
- 一个把所有异常合成 Health Score 的 Dashboard。

用户离开本页前应该已经知道：

1. 当前调查的 symptom / problem statement 是什么；
2. 哪些是 verified facts；
3. 哪些是 published finding；
4. 哪些只是 hypothesis；
5. 哪些证据支持 / 反驳这些 hypothesis；
6. 当前 finding / hypothesis 的 confidence 到底意味着什么；
7. evidence window、scope、source 是否完整；
8. 下一验证动作是什么；
9. 是否已经具备创建 Work / 启动 Functional Verification / 形成 Opportunity 的条件；
10. 如果已经确认 Root Cause，谁确认、基于什么证据、何时确认。

---

# 2. 主要用户

## Primary

### HVAC 诊断 / Commissioning 工程师

需要从 alarm、fault、comfort deviation、efficiency deviation 或 operator issue 出发，验证问题、缩小范围、形成可执行判断。

### 站点运行工程师

需要快速看清“事实是什么、FDD 说了什么、下一步应该验证什么”，避免被大量 rule hits 淹没。

### 维修 / 技术负责人

需要在创建 Work 前理解 symptom、evidence、suspected cause 和验证要求。

## Secondary

- Controls engineer：核对 sequence、setpoint、command/readback 和 override 证据；
- Energy engineer：把 persistent fault / performance degradation 转换为节能机会；
- Data engineer：排查 sensor / semantic / historian 问题；
- Alarm administrator：从反复误报/无效 finding 回到 rule/rationalization 改进。

## 不作为主要目标用户

- 值班员只做 ACK/Assign：Alarm Center；
- 维修人员执行工单：Work Order；
- 管理层查看高层 KPI：Site / Management Review；
- 直接修改 FDD model/rule：Rule/Model Administration；
- 直接发控制命令：Control Center。

---

# 3. 外部最佳实践依据

## 3.1 DOE/FEMP — AFDD 是“检测偏离 + 诊断问题类型或位置”

DOE 对 AFDD 的定义是：识别偏离 normal / expected operation 的 faults，并诊断问题的 type 或 location。AFDD 可以发现 equipment/component failures、performance degradation、design issues、controllability issues、operator overrides、incorrect sequences 等。

来源：

- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities

**本页采用：**

- AFDD / FDD 是 Finding 来源之一，不是整个 Diagnosis UX；
- detection 与 diagnosis 分开；
- `fault detected` 不等于 `root cause confirmed`；
- algorithm output 必须保留 method/source/provenance。

## 3.2 DOE EMIS Operations Support — Validate / Diagnose / Triage 是人工 + 系统共同流程

DOE 推荐 EMIS 运营过程：

```text
Identify & Prioritize
→ Validate, Diagnose & Triage
→ Implement Corrective Actions
→ Verify Improvement
→ Monitor, Update & Maintain
```

DOE 明确指出，EMIS 的 analysis / visualization 可以帮助 validate issues 和 determine root causes，但底层系统 survey 可能仍然必要；整改之后还必须 verify improvement。

来源：

- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems
- https://www.energy.gov/sites/default/files/2022-02/best-practices-to-support-EMIS-operation-federal-facilities.pdf

**本页采用：**

- Diagnosis 不是“一次算法给答案”；
- 必须支持 next verification；
- Work Complete 后可回到 Functional Verification；
- root cause 可以反馈回 analytics owner 改善 future diagnostics。

## 3.3 DOE / Better Buildings Energy Information Handbook — AFDD 使用 measured data + setup/system data 产生 actionable information

DOE/Better Buildings 的 FDD 指南明确展示：equipment/system characteristics 与 measured conditions 进入 automated diagnostic tool，再产出 fault reports、possible causes、corrective actions、cost/impact 等 actionable information。

来源：

- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/attachments/energy-information-handbook.pdf

**本页采用：**

- Finding 必须与 source data、setup/model assumptions 关联；
- actionable information 可以辅助决策，但不能抹掉 evidence provenance；
- 缺 context 时应降低 diagnosis claim，而不是让前端补猜。

## 3.4 NIST FDD — Fault detection 的正确性依赖 operating context、features 与 steady-state / transient 条件

NIST 长期研究 HVAC FDD，包含 regulation fault detection、rule-based diagnosis、steady-state detection、fault intensity、performance degradation 等。NIST 的研究表明不同 operating conditions / transients 会影响 FDD 判断。

来源：

- https://www.nist.gov/programs-projects/fault-detection-and-diagnostics-air-conditioners-and-heat-pumps
- https://www.nist.gov/publications/automatically-detecting-faulty-regulation-hvac-controls
- https://www.nist.gov/publications/design-steady-state-detector-fault-detection-and-diagnosis-residential-air-conditioner

**本页采用：**

- model/rule applicability 与 operating context 必须可查看；
- startup/transient 不应被 UI 隐藏；
- confidence / diagnosis 不可脱离 model applicability 解释；
- evidence gap / wrong mode / bad sensor 可能使 finding inconclusive。

## 3.5 ASHRAE Guideline 36-2024 — FDD 与 sequence / functional test 相连

Guideline 36 的目标包括高效 HVAC、control stability、real-time FDD，并定义 functional tests 用于确认 sequence implementation。

来源：

- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**本页采用：**

- sequence / mode / setpoint / reset / interlock 是诊断一等 evidence；
- incorrect sequence 可以成为 Finding；
- 诊断后的最终确认常需要 Functional Verification；
- FDD result 不能代替 functional test。

## 3.6 DOE / LBNL FDD benchmarking — 某些 FDD 可以 isolate root causes，但不是所有结果都能

DOE/LBNL 关于 FDD test datasets / prioritization 的资料明确指出，FDD algorithms 使用 building operational data 识别 fault，有些算法能进一步 isolate root cause；行业仍在研究 accuracy / benchmark / prioritization。

来源：

- https://www.energy.gov/cmei/buildings/articles/fault-detection-and-diagnostics-test-datasets-and-prioritization-methods

**本页采用：**

- UI 不能默认把所有 Finding 当 root cause；
- capability 要说明算法输出层级：detection / isolation / diagnosis；
- model accuracy/confidence 的语义必须来自 model owner。

## 3.7 Better Buildings — FDD 价值依赖把 insights 变成 corrective action

Better Buildings 明确强调，要最大化 FDD 价值，需要将大量 analytics recommendations 转换成有效 corrective actions，并与 work-order workflow 集成。

来源：

- https://betterbuildingssolutioncenter.energy.gov/webinars/turning-insights-action-bridging-building-data-analytics-and-work-order-systems
- https://betterbuildingssolutioncenter.energy.gov/smart-energy-analytics-additional-resources

**本页采用：**

- Finding 必须有 clear next action / next verification；
- Work handoff 是正式产品路径；
- 不让 Finding 队列无限增长成“没人处理的洞察仓库”。

## 3.8 NIST AI RMF — AI 辅助必须可解释、可审计、受限于用途与知识边界

NIST AI RMF 强调 AI system 的 valid/reliable、accountable/transparent、explainable/interpretable，并要求 output 在其使用 context 中被解释。

来源：

- https://www.nist.gov/itl/ai-risk-management-framework
- https://airc.nist.gov/airmf-resources/airmf/3-sec-characteristics/
- https://airc.nist.gov/airmf-resources/airmf/5-sec-core/

**本页采用：**

- AI 只能草拟 / 辅助 hypothesis、summary、next questions；
- AI output 必须明确来源；
- AI 不能覆盖 authoritative Finding；
- AI confidence 不自动等于 engineering confidence；
- 超出 model knowledge / context 时必须允许“不确定/无法判断”。

## 3.9 Siemens / Schneider — 成熟产品将 AI/FDD 放在 fault investigation 中，而不是独立“AI 中心”

Siemens Building X Operations Manager 的 Fault Triage Agent 用于帮助 operator assess faults、理解 likely causes、聚焦重要问题；Schneider Building Advisor 的 Asset Health aFDD 也提供 possible root causes 与 recommended corrective actions。

来源：

- https://www.siemens.com/en-us/products/building-x/applications/operations-manager/
- https://sqa.ecostruxure-building-help.se.com/ba/Topics/Show.castle?id=13413&locale=en-US&productversion=7.0

**本页采用：**

- AI / FDD 被嵌入 Investigation workflow；
- `possible root cause` 文字必须与 `verified root cause` 区分；
- 推荐动作不能伪装成已执行动作。

---

# 4. Diagnostic Vocabulary

这是本页最重要的语义层。

## 4.1 Fact

来自 authoritative owner 的可验证事实，例如：

```text
CHWS = 7.1°C at 09:42:18
CHWR = 12.4°C at 09:42:17
CH-02 running
Valve position = 100%
Alarm A-103 active since 09:17
```

Fact 必须有：

- source；
- timestamp / time range；
- unit；
- quality；
- provenance。

## 4.2 Symptom

对问题现象的业务描述：

```text
冷冻水温差持续偏低
AHU 送风温度高于目标
Zone Z-1203 持续过热
```

Symptom 可以由 Alarm、Operator、Analytics 或 Energy deviation 触发。

Symptom 不是原因。

## 4.3 Finding

由 rule / model / deterministic analytics / human investigation 发布的结构化诊断结果。

例如：

```text
Finding: CHW ΔT below expected range while load > 60%
```

Finding 必须明确：

- source type；
- source identity/revision；
- evaluation window；
- affected scope；
- result/condition；
- evidence references；
- confidence（若 source owner真的提供）；
- published at。

Finding 不自动等于 Root Cause。

## 4.4 Hypothesis

等待验证的原因假设。

例如：

```text
Hypothesis A: bypass valve leakage
Hypothesis B: excessive secondary flow
Hypothesis C: sensor bias
```

每个 Hypothesis 必须有：

- source：human / rule suggestion / AI draft；
- supporting evidence；
- contradicting evidence；
- required verification；
- status。

## 4.5 Root Cause

只有在正式 investigation / verification owner 确认后才能存在。

Root Cause 至少需要：

- confirmed by / process owner；
- confirmation timestamp；
- evidence set；
- verification method；
- related corrective action；
- revision / audit。

如果这些 owner contract 不存在，产品就不显示“Root Cause”，只显示 Finding / Hypothesis。

---

# 5. Mandatory Semantic Separation

以下全部禁止：

```text
Rule hit = Finding
Finding = Root Cause
Correlation = Causality
Alarm = Diagnosis
AI suggestion = Root Cause
High confidence = Root Cause confirmed
Work created = Diagnosis confirmed
Work completed = Root Cause verified
Same-time events = causal relation
Sensor anomaly = equipment fault
```

正确层级：

```text
Facts
↓
Symptom
↓
Finding
↓
Hypotheses
↓
Verification
↓
Confirmed Root Cause (when authoritative)
```

任何层级都可以因为新证据而变成：

```text
Supported
Contradicted
Inconclusive
Superseded
```

---

# 6. Primary Questions

## Q1 — 问题到底是什么？

显示：

- symptom；
- affected object/system/zone；
- first observed；
- current state；
- source / trigger。

## Q2 — 哪些事实已经确定？

必须先展示 Verified Facts，而不是先展示“AI 原因”。

## Q3 — Analytics / FDD 实际发现了什么？

显示 published Finding，并明确：

- model/rule source；
- evaluation window；
- applicability；
- evidence；
- output type。

## Q4 — 现在有哪些可能原因？

Hypothesis 列表按 evidence status，而不是“AI 排名”默认排序。

## Q5 — 哪些证据支持 / 反驳这些假设？

每个 Hypothesis 都必须能看到 supporting 与 contradicting evidence。

## Q6 — 下一步怎么把不确定性缩小？

必须有 `Next Verification`：

- 查看趋势；
- 检查特定 point；
- 验证 sensor；
- 检查 sequence；
- 现场检查；
- Functional Verification；
- 创建 Work。

## Q7 — 什么时候可以结束调查？

调查结束条件由 investigation owner 定义，例如：

```text
Root cause confirmed
or
Issue invalidated
or
Insufficient evidence / deferred
```

不能通过“用户点 Close”把事实强行结束。

---

# 7. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/diagnostics
```

推荐 Search Params：

```text
view            // open | resolved | all
q
sourceType
system
asset
severity
status
confidenceBand
from
to
selected
hypothesis
owner
sort
page
size
```

如果从 Alarm / Trend / Device / Comfort 进入，`selected` 与 source context 应能恢复调查。

不进入 URL：

- local disclosure open state；
- transient chart cursor；
- hover；
- unsaved hypothesis draft；
- AI chat transient token stream。

---

# 8. Entry Contract

## 8.1 从 Alarm Center

携带：

```text
Alarm occurrence
Asset/System/Zone
Activation/recovery window
Source series
Rationalized guidance as guidance
Return-to-source
```

Alarm probable-cause guidance 不自动升级为 Finding/Root Cause。

## 8.2 从 System Operations

携带：

- current system/object；
- mode/stage；
- relevant alarms/findings；
- current evidence window。

## 8.3 从 Device Detail

携带：

- stable asset；
- current states；
- selected point/metric；
- recent evidence；
- source trail。

## 8.4 从 Trend Analysis

携带：

- selected object；
- exact time window；
- selected series；
- relevant event markers；
- user-selected evidence segment。

## 8.5 从 Comfort / Energy / Efficiency

携带：

- affected zone/system；
- metric/deviation；
- time range；
- comparison / target context；
- source trail。

---

# 9. Exit Contract

主要出口：

```text
Diagnosis
→ Trend Analysis
→ Device Detail
→ System Operations
→ Work Order
→ Functional Verification
→ Data Quality
→ Energy / Efficiency
→ Savings Opportunity (when qualified)
```

Admin/engineering capability 下可进入：

```text
Rule / Model metadata
Semantic Model
Alarm Definition
```

保持：

- Site；
- Investigation；
- Asset/System/Zone；
- Evidence Window；
- Finding；
- Hypothesis；
- Source trail。

---

# 10. Responsibility Boundary

Diagnosis Center 拥有：

- investigation queue；
- verified facts projection；
- published Finding projection；
- Hypothesis management（如果 investigation owner存在）；
- supporting / contradicting evidence linkage；
- next verification planning；
- investigation timeline；
- Root Cause confirmation projection（仅在 authoritative owner 存在时）；
- handoff to Work / Verification / Opportunity。

Diagnosis Center 不拥有：

- alarm ACK/assignment；
- raw alarm lifecycle；
- work execution；
- control command；
- rule/model editing；
- telemetry generation；
- historian aggregation；
- AI model training；
- generic chat history；
- savings verification；
- frontend-created root cause。

---

# 11. Information Architecture

```text
Context Header
  Site · Investigation source · evidence window · timezone

Compact Investigation Context
  Open investigations · New findings · Needs verification · Data issues

Investigation Queue
  Symptom
  Scope
  Finding source
  Investigation status
  Evidence freshness
  Owner
  Next verification

Selected Investigation Workspace
  Problem Statement
  Verified Facts
  Published Finding
  Hypotheses
  Supporting / Contradicting Evidence
  Impact / Related Objects
  Next Verification
  Investigation Timeline

Professional Detail
  Rule / Model metadata
  Confidence semantics
  Raw evidence references
  Data quality / applicability

Actions / Exits
  Trend · Device · Work · Functional Verification · Data Quality · Opportunity
```

默认不是 8 张诊断 KPI cards，也不是“Root Cause Top 5”排行榜。

---

# 12. Investigation Queue

默认扫描单位是 **Investigation / Diagnostic Issue**，不是 raw rule hit。

推荐核心列：

| 列 | 内容 |
|---|---|
| Problem | human-readable symptom |
| Scope | system / device / zone |
| Finding | published result summary |
| Source | rule/model/human/AI-assist label |
| Status | new / investigating / verification / resolved |
| Evidence | current / stale / incomplete |
| Owner | investigation owner |
| Next step | next verification |
| Updated | latest meaningful update |

## 12.1 Deduplication

如果多个 rule hits 属于同一 authoritative investigation grouping，可以聚合。

禁止：

- 根据 message 文本相似度 merge；
- 根据同一设备就自动合并；
- 根据时间相近自动合并；
- 丢失原始 Finding identity。

## 12.2 Queue prioritization

可依据 owner facts：

- safety/criticality；
- active alarm relationship；
- impact；
- persistence；
- repeat；
- owner-defined energy/cost impact；
- verification overdue；
- data confidence。

不使用隐藏 AI risk score。

---

# 13. Problem Statement / Symptom

Selected Workspace 顶部首先回答：

> **正在调查什么？**

例如：

```text
CH-02 冷冻水温差在高负荷时持续低于运行目标
```

同时显示：

- first observed；
- current state；
- trigger source；
- affected scope；
- evidence window；
- source trail。

不要一上来显示：

> Root cause: bypass valve leakage

除非该 Root Cause 已经被 authoritative investigation owner 确认。

---

# 14. Verified Facts

这是 Selected Workspace 的第一专业层。

每条 Fact 结构：

```text
Fact statement
Source
Time / window
Value / state
Quality
Provenance
```

示例：

```text
CH-02 running continuously from 08:12–10:03
CHWS 7.1–7.4°C, GOOD quality
CHWR 10.8–12.4°C
Bypass valve command 0%, readback unavailable
Alarm A-103 active at 09:17
```

## 14.1 Fact categories

- telemetry fact；
- state/mode fact；
- alarm fact；
- control execution fact；
- work fact；
- semantic relation fact；
- data-quality fact。

不要把 inference 放进 Verified Facts。

---

# 15. Published Finding

Finding 是 domain output，不是 UI 自己总结。

显示：

```text
Finding title
Finding result
Source type
Rule/model name + revision
Evaluation window
Affected scope
Confidence (if provided)
Evidence references
Published at
```

## 15.1 Finding source types

例如：

```text
Deterministic rule
Physics/model-based FDD
Statistical/anomaly model
Machine-learning model
Human-published finding
AI-assisted draft (not authoritative by default)
```

具体枚举由 Diagnosis domain 定义。

## 15.2 Rule Hit != Finding

Rule execution internal hit 只有在 Diagnosis owner 发布为 Finding 时，才进入用户层。

Frontend 不读取 raw rule engine logs 自己生成 Finding。

---

# 16. Hypothesis Contract

Hypothesis 是等待验证的候选解释。

状态可包括：

```text
Proposed
Under Test
Supported
Contradicted
Inconclusive
Superseded
Confirmed as Root Cause (only via owner workflow)
```

每个 Hypothesis 显示：

- statement；
- source；
- created by / time；
- supporting evidence；
- contradicting evidence；
- unresolved questions；
- next verification；
- status history。

## 16.1 AI-drafted Hypothesis

必须清楚标记：

> `AI draft — requires engineering verification`

AI 可以：

- summarize facts；
- propose hypotheses；
- suggest next questions；
- identify evidence gaps。

AI 不可以：

- overwrite Finding；
- mark Root Cause confirmed；
- invent telemetry/evidence；
- fabricate confidence；
- auto-create Work without explicit user action。

---

# 17. Supporting / Contradicting Evidence

任何 Hypothesis 都必须允许同时存在两类证据。

## Supporting

例如：

```text
High secondary flow coincides with low ΔT
Valve readback remains > 40% when command = 0%
Issue disappears after valve isolation
```

## Contradicting

例如：

```text
Flow meter quality suspect
Valve readback unavailable
ΔT normal during equivalent load on previous day
```

UI 不应该只展示 supporting evidence，因为这会强化 confirmation bias。

## 17.1 Evidence link

每条 evidence link 必须有：

- owner；
- time/window；
- source object；
- quality；
- relation type；
- deep-link。

---

# 18. Confidence Semantics

`Confidence` 必须带 source-specific definition。

可能存在：

```text
Model confidence
Rule certainty / deterministic match
Data coverage confidence
Investigation confidence
Human assessment confidence
```

这些不是同一个数。

## 18.1 Forbidden

禁止：

```text
Model confidence 93% = 93% root-cause probability
Rule match = 100% confidence
More evidence count = higher confidence
AI token probability = engineering confidence
```

## 18.2 Display rule

显示 confidence 时同时显示：

- whose confidence；
- what output it applies to；
- method / calibration if owner exposes；
- evaluation context；
- limitations / applicability。

如果 owner 没有定义 confidence，就不显示百分比。

---

# 19. Applicability / Operating Context

Finding / Model 必须能表达适用条件，例如：

- equipment type；
- operating mode；
- steady-state requirement；
- minimum load；
- sensor availability；
- sequence version；
- weather / occupancy conditions；
- model version。

如果条件不满足：

> `Finding applicability not satisfied`

或

> `Evaluation inconclusive`

不能继续把旧结果当当前事实。

---

# 20. Evidence Window Contract

每个 investigation 必须有明确时间语义：

```text
Source event window
Finding evaluation window
Selected investigation window
Verification window
```

这些可以不同，但 UI 必须标清。

从 Alarm 进入时保留 alarm occurrence window；从 Trend 进入时保留 user-selected range。

不自动重置为 Today。

---

# 21. Evidence Workspace

Selected Investigation 中提供小型 evidence preview：

```text
1–4 relevant series
+ event lanes
+ target/setpoint/expected range
+ quality gaps
```

遵循 05 Trend Analysis：

- same-unit overlay；
- different unit families → aligned small multiples；
- no default dual-Y-axis；
- missing → gap；
- stale/bad quality 明确。

完整分析进入 Trend。

---

# 22. Sequence / Control Evidence

对于 controls-related diagnosis，可显示：

```text
Operating mode
Stage
Active schedule
Setpoint
Reset target
Command
ACK/execution result
Readback
Override
Interlock state
```

必须保持：

```text
Intent ≠ Attempt ≠ ACK ≠ Readback ≠ Verified
```

如果 sequence requirement owner 存在，可比较：

```text
Expected sequence
vs
Observed sequence
```

但不在前端解析自由文本 sequence 自己得出 fault。

---

# 23. Sensor / Data Quality as Diagnostic Evidence

Data quality 问题本身可以解释“为什么无法诊断”，但不能自动成为 equipment root cause。

显示：

- missing；
- stale；
- suspect；
- bad quality；
- calibration issue；
- semantic mapping missing；
- clock/timestamp issue；
- historian coverage。

例如：

```text
Hypothesis: CHWR sensor bias
Status: Under Test
Evidence: sensor fails cross-check against redundant measurement
```

这需要 Data Quality / verification owner 支持。

---

# 24. Impact Contract

Impact 可以包括：

- affected equipment；
- affected zones；
- comfort/IAQ impact；
- alarm impact；
- runtime / reliability impact；
- energy / demand impact；
- cost impact。

Energy/cost impact 只有 analytics owner 提供时显示。

禁止 frontend：

- 用额定功率 × duration 粗算后标成 verified savings/cost；
- 把 potential impact 写成 actual loss；
- 把 estimated impact 当 priority truth。

---

# 25. Next Verification

每个 unresolved investigation 必须有明确 next verification，避免“只看 Finding 不行动”。

示例：

```text
Compare CH-02 valve command/readback for 2 hours
Verify CHWR sensor against reference instrument
Run Guideline-36 functional test for SAT reset
Inspect bypass valve physically
Check pump differential-pressure reset logic
```

每个 verification item 包含：

- objective；
- required evidence；
- responsible role/team；
- target window；
- destination workflow。

复杂 verification 进入 Functional Verification Surface。

---

# 26. Root Cause Confirmation Contract

Root Cause 不是普通状态选择框。

确认前至少要求 authoritative owner 定义：

- hypothesis identity；
- evidence set；
- verification result；
- confirmer；
- confirmed at；
- confidence / certainty semantics if used；
- corrective action relationship；
- audit trail。

UI 可以显示：

```text
Root Cause Confirmed
By: Li Wei
Method: Valve isolation + readback verification
Confirmed: 2026-09-14 14:32
```

如果 owner 没有 Root Cause confirmation capability，就永远不要显示假“Confirmed Root Cause”。

---

# 27. Resolution Contract

Investigation resolution 与物理恢复分开。

可能 resolution：

```text
Root Cause Confirmed
Issue Invalidated
Duplicate / Merged by owner
Insufficient Evidence
Deferred
Superseded
```

Alarm physical state、Work state、Verification state 保持独立。

不能：

```text
Diagnosis Resolved → clear Alarm
Work Complete → close Diagnosis automatically
Alarm Cleared → Root Cause confirmed
```

---

# 28. Investigation Timeline

Timeline 只记录有业务意义的事件：

- source alarm/finding created；
- investigation created；
- fact/evidence attached；
- hypothesis proposed/changed；
- verification requested/result；
- Work linked；
- Root Cause confirmed；
- resolved/reopened by owner process。

不把每次 UI view / hover / auto-refresh 写成 timeline event。

---

# 29. Diagnosis → Work Contract

创建 Work 时 handoff：

```text
Problem statement
Affected object
Published Finding
Selected hypothesis (as hypothesis)
Verified facts
Evidence links
Next verification / corrective recommendation
Priority/impact context
```

Work Order 不应收到“AI Root Cause”作为已验证事实，除非 Root Cause owner 已确认。

Work 完成后可以回到 Functional Verification / Diagnosis 更新 evidence。

---

# 30. Diagnosis → Functional Verification

这是确认 sequence / corrective action / control behavior 的正式出口。

携带：

- investigation；
- hypothesis；
- expected behavior；
- required test conditions；
- selected points；
- evidence window；
- related Work/Strategy。

Functional Verification 结果回来后：

- support hypothesis；
- contradict hypothesis；
- confirm correction；
- reveal new issue。

不能自动把 test pass 转成 Root Cause confirmed，除非 owner workflow 明确。

---

# 31. Diagnosis → Savings Opportunity

只有满足以下条件时才出现：

- issue/finding 与 energy performance 有明确关系；
- impact owner 提供 potential savings / loss evidence；
- issue 不只是 data-quality error；
- recommendation 有明确 action boundary。

Opportunity 仍是后续 engineering/economic workflow，不在 Diagnosis 直接宣称 savings。

---

# 32. Rule / Model Metadata Boundary

专业层可以显示：

```text
Rule/model name
Version/revision
Method type
Input variables
Applicability conditions
Evaluation interval
Last deployment/update
Owner
Known limitations
```

但不在 Diagnosis Center 直接修改：

- rule threshold；
- model parameters；
- training data；
- priority logic；
- production deployment。

这些进入 Rule/Model Administration + change control。

---

# 33. Data Authority Contract

## Alarm facts

Owner：Alarm domain。

## Telemetry / Historian facts

Owner：Telemetry / Historian。

## System / Device / Zone relation

Owner：Semantic Model / Registry。

## Finding

Owner：Diagnosis / FDD domain。

## Rule / Model metadata

Owner：Rule / Model registry。

## Hypothesis

Owner：Investigation domain。

## Root Cause

Owner：Investigation / Verification domain。

## Work

Owner：Work Order domain。

## Functional Verification

Owner：Verification domain。

## Energy / Cost impact

Owner：Energy / Opportunity analytics。

## AI draft

Owner：AI/Copilot domain，必须标注非 authoritative。

Frontend 只做 investigation-centered projection。

---

# 34. Query / Read Model Contract

推荐 Selected Investigation read model：

```text
Investigation
  + problem statement
  + verified facts
  + current findings
  + hypotheses
  + evidence links
  + related alarms/work/verification
  + source/model metadata summary
  + next verification
```

Queue 使用 server-side query/filter/pagination。

禁止：

```text
100 findings
→ 100 registry requests
→ 100 alarm requests
→ 100 historian requests
→ 100 model metadata requests
```

如果缺 page read model / batch projection，修 domain contract，不在前端做长期 N+1 workaround。

---

# 35. Realtime Contract

Diagnosis 不是高频 flashing HMI，但支持 meaningful updates：

```text
New finding
Finding superseded
New evidence
Hypothesis update
Verification result
Work link/update
Root cause confirmation
```

Realtime update：

- 不抢 focus；
- 不关闭 Inspector/workspace；
- 不自动替换用户正在阅读的 hypothesis；
- 不自动标 Root Cause；
- selected investigation 即使状态变化也保持当前 context；
- stream disconnect 不解释为 finding resolved。

Reconnect：

- refetch authoritative investigation Snapshot；
- reconcile revisions；
- 再恢复 incremental updates。

---

# 36. Mutation Contract

允许的 investigation mutation 必须由 owner capability 定义，例如：

- assign investigation；
- add human note；
- propose hypothesis；
- change hypothesis status；
- request verification；
- link Work；
- resolve investigation；
- confirm Root Cause（高权限/受控）。

所有 mutation：

1. explicit user action；
2. owner-required fields；
3. authoritative mutation；
4. server-confirmed result；
5. audit trail。

禁止 local-only state 冒充 domain fact。

---

# 37. Loading / Empty / Partial / Error

## No open investigations

owner 成功返回 empty：

> `当前没有待调查问题。`

可以提供 Resolved / History 入口。

## Diagnosis service unavailable

> `诊断结果暂不可用。`

不能显示 `0 findings`。

## Telemetry unavailable

已发布 Finding 仍可显示，但 evidence 明确 unavailable/stale。

不能把旧 sample 冒充 current fact。

## Model metadata unavailable

Finding 仍可显示 result/source identity；专业 metadata 标 unavailable。

不能猜 rule/version。

## Hypothesis owner unavailable

Finding 仍可查看，但 Hypothesis/Root Cause workflow 不伪造。

## Evidence incomplete

显示：

> `证据不足，当前结论为 Inconclusive`

而不是把 confidence 提高或自动选一个原因。

---

# 38. Permission / Capability Gating

示例：

- `diagnosis.read` → queue/workspace；
- `diagnosis.investigate` → hypothesis / notes；
- `diagnosis.rootcause.confirm` → Root Cause confirmation；
- `model.metadata.read` → rule/model detail；
- `work.create` → Work handoff；
- `verification.create` → Functional Verification；
- `opportunity.create` → Savings Opportunity；
- `ai.assist` → AI draft features。

没有权限的高风险动作默认不显示。

---

# 39. AI / Copilot Contract

AI 是横向辅助，不是 Diagnosis source-of-truth。

可以：

```text
Summarize verified facts
Suggest candidate hypotheses
Suggest missing evidence
Draft investigation note
Draft next verification questions
Explain rule/model output in user language
```

必须：

- 标记 AI-generated；
- 引用当前 investigation 的 authoritative evidence；
- 保留用户确认步骤；
- 表达 uncertainty / limitations；
- 不覆盖 authoritative field。

禁止：

```text
AI says root cause → mark Confirmed
AI invents missing sensor values
AI creates fake confidence percentage
AI silently changes Finding
AI directly closes Work/Alarm
```

---

# 40. Search / Filter Contract

Primary filters：

```text
Search
Status
System / Asset
Finding source
Owner
```

Secondary：

- evidence quality；
- confidence band（owner-defined only）；
- related alarm；
- verification required；
- repeat/bad actor；
- time range。

Search human-readable：

- problem statement；
- asset/system；
- finding title；
- business code。

内部 UUID 不做主视觉。

---

# 41. Visual / HMI Contract

诊断页视觉强调“证据层级”，不是严重程度颜色墙。

推荐层级：

```text
Verified Facts           neutral/high-trust
Published Finding        distinct authoritative result
Hypothesis               clearly provisional
Root Cause Confirmed     explicit confirmed state
Contradicting Evidence   equally visible
```

颜色不能让 Hypothesis 看起来比 Fact 更“确定”。

不使用：

- AI 紫色 glow 当可信度；
- 红色越深=越可能根因；
- 3D fault graph 装饰；
- 旋转 AI icon；
- 持续 animation。

---

# 42. Component Mapping

```text
Page header                  → application layout
Investigation queue          → shadcn Table + TanStack Table
Search / filters             → InputGroup / Select / Popover
Status / source              → Badge + semantic text
Selected workspace           → stable main pane / responsive Sheet only on narrow viewport
Verified facts               → semantic list / definition list
Published finding            → structured result section
Hypotheses                   → semantic cards/list with evidence states, not decorative cards
Evidence preview             → dedicated ECharts feature
Evidence lists               → semantic lists
Investigation timeline       → ordered timeline/list
Rule/model metadata          → Collapsible / definition list
AI draft                     → clearly labeled assistant block
Mutations                    → Dialog / Fields when appropriate
Professional exits           → Button / Link
```

避免：

- finding card wall；
- root-cause percentage donuts；
- giant AI chat replacing investigation workspace；
- nested Drawer；
- 20 个等权重信息卡片。

---

# 43. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 诊断中心 · Phoenix Central Plant                Evidence 08:45–10:00        │
│ 来源：CH-02 Low ΔT Alarm                                                  │
├──────────────────────────────────────────────────────────────────────────────┤
│ Open 12   New findings 3   Needs verification 4   Data issues 2            │
├─────────────────────────────────────┬────────────────────────────────────────┤
│ Problem / Scope        Status Next │ CH-02 · 冷冻水温差持续偏低             │
│ CH-02 Low ΔT           Investigate │ Symptom: 高负荷时 ΔT 低于运行目标       │
│ AHU-03 SAT deviation   Verify      │                                        │
│ Z-1203 overheating     Investigate │ 已验证事实                             │
│ ...                                 │ • CH-02 running 08:12–10:03            │
│                                     │ • CHWS 7.1–7.4°C GOOD                  │
│                                     │ • CHWR 10.8–12.4°C                     │
│                                     │                                        │
│                                     │ Published Finding                      │
│                                     │ ΔT below expected range at load >60%   │
│                                     │ Rule v3.2 · confidence: owner-defined  │
│                                     │                                        │
│                                     │ Hypotheses                             │
│                                     │ A Bypass leakage · Under Test           │
│                                     │   + flow remains high                   │
│                                     │   − valve readback unavailable          │
│                                     │ B Sensor bias · Proposed                │
│                                     │                                        │
│                                     │ Next verification                       │
│                                     │ Verify bypass valve readback/isolation   │
│                                     │                                        │
│                                     │ [趋势] [设备] [创建工单] [功能验证]      │
└─────────────────────────────────────┴────────────────────────────────────────┘
```

Wireframe 只表达职责、证据层级和交互结构，不是 pixel specification。

---

# 44. Accessibility

必须：

- Investigation Queue 使用 semantic table；
- Fact / Finding / Hypothesis / Root Cause 不只靠颜色区分；
- evidence chart 有 text/data alternative；
- supporting / contradicting evidence 有文本标签；
- confidence 有 accessible definition；
- keyboard 可完成 queue select / evidence link / professional exits；
- realtime update 不抢 focus；
- AI streaming 不高频 announce；
- status change 有适度 accessible notification；
- narrow viewport workspace 使用 accessible Sheet / stack，而不是 hover-only panel。

---

# 45. Responsive Behavior

## 1440–1720 px

首屏看到：

- source/context；
- investigation queue；
- selected problem；
- verified facts；
- published Finding；
- hypotheses / next verification；
- professional exits。

## 1024–1439 px

- Queue 变窄；
- metadata / timeline 下沉；
- evidence preview 仍可读。

## Around 768 px

必须仍可：

- 找到 investigation；
- 看 problem / facts / Finding；
- 查看 Hypothesis supporting/contradicting evidence；
- 看 next verification；
- 进入 Trend / Device / Work / Verification。

策略：

- Queue 与 workspace sequential；
- selected investigation 可通过 Sheet/route-like selection呈现；
- evidence small multiples stack；
- 无 hover-only interaction；
- 无 page-level horizontal overflow。

---

# 46. No Defensive Programming / No Compatibility Design

明确禁止：

```text
rule hit → root cause
alarm exists → finding
finding exists → confirmed root cause
model confidence → root-cause probability
correlation → causality
missing evidence → assume normal
missing evidence → use last known silently
telemetry unavailable → keep old current values as fact
model metadata missing → infer rule/version from name
asset relation missing → infer by point prefix
AI suggestion → authoritative hypothesis without label
AI suggestion → confirmed root cause
work complete → resolve diagnosis automatically
alarm cleared → resolve diagnosis automatically
finding unavailable → rebuild it in frontend from telemetry
multiple diagnosis APIs → first success wins
one finding → one registry/model/evidence request N+1
old FDD page compatibility adapter
old AI investigation fallback
frontend-generated root-cause score
```

不建立旧 `/fdd` 与新 `/diagnostics` 的双产品语义兼容。

不建立“新 Diagnosis unavailable → fallback old FDD result”的链路。

原则：

> **One fact → one owner. One finding → one published source. A hypothesis stays provisional until verified. Correlation is not causality. Unknown stays unknown.**

---

# 47. Browser Acceptance Criteria

## Semantic Integrity

- Fact / Symptom / Finding / Hypothesis / Root Cause 视觉和文字分开；
- Rule Hit 不显示为 Root Cause；
- Finding 不默认显示为 Root Cause；
- AI draft 明确标记；
- correlation 不显示 causal claim；
- Root Cause 只有 authoritative confirmation 才出现。

## Evidence

- verified facts 有 source/time/quality；
- Finding 有 evaluation window/source/revision；
- supporting / contradicting evidence 同时可见；
- evidence gaps 明确；
- full Trend deep-link 保留 window/series/source；
- sequence/control evidence 保持 Intent/Attempt/Readback/Verified 分离。

## Confidence

- confidence 显示 owner/meaning；
- no fake percentage；
- model confidence 不写成 root-cause probability；
- owner 不提供 confidence 时不显示数字；
- applicability/limitations 可查看。

## Workflow

- Alarm → Diagnosis 保留 occurrence/evidence context；
- Diagnosis → Work 保留 facts/finding/evidence；
- Diagnosis → Functional Verification 保留 hypothesis/expected behavior；
- Work Complete 不自动 resolve diagnosis；
- Verification result 可 support/contradict hypothesis。

## Partial / Error

- Diagnosis unavailable 不显示 0 findings；
- telemetry unavailable 不伪造 current facts；
- model metadata unavailable 不猜；
- hypothesis owner unavailable 不 fake Root Cause；
- insufficient evidence 显示 Inconclusive。

## Responsive

1440–1720 px：

- Queue + selected workspace 形成一个 coherent investigation workspace；
- 无 card wall；
- 无 page-level horizontal overflow。

Around 768 px：

- Queue / facts / Finding / hypothesis / next verification / exits 可操作；
- evidence 可读；
- 无 hover-only interaction；
- 无 page-level horizontal overflow。

## Accessibility

- semantic table；
- evidence types 不只靠颜色；
- keyboard navigation；
- chart data alternative；
- AI/realtime 不抢 focus。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 FDD page compatibility adapter；
- 无旧 AI investigation fallback；
- 无 frontend-generated root cause；
- 无 frontend-generated Finding；
- 无 one-row-one-request N+1；
- review scenario 无 runtime/network error。

---

# 48. Explicit Non-Goals

本页不是：

- Alarm triage page；
- Work execution page；
- Rule editor；
- Model training UI；
- generic AI chat；
- automatic root-cause oracle；
- M&V savings calculator；
- Control Center；
- raw historian；
- semantic model editor。

---

# 49. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Fact / Symptom / Finding / Hypothesis / Root Cause 语义已接受；
- Finding owner / rule-model provenance contract 明确；
- evidence window 与 supporting/contradicting evidence contract 明确；
- confidence semantics 不再使用 generic percentage；
- AI 只作为 clearly labeled assist；
- Investigation / Hypothesis / Root Cause mutation owner 明确；
- Alarm → Diagnosis → Work / Verification context handoff 明确；
- Root Cause confirmation 是受控 workflow；
- old FDD / old AI pages 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
