# 31 数据质量 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `31 数据质量`  
> **Route intent：** `/sites/:siteId/data-quality`、`/sites/:siteId/data-quality/issues/:issueId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`Coverage`、`Freshness`、`Completeness`、`Validity`、`Synchronization`、`Lineage`、`Recompute` 等仅作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有数据页、旧监控页、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 Data Quality / Source Health / Lineage / Correction / Recomputation contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **告诉用户哪些数据和由这些数据驱动的业务结论值得信、哪些存在问题、问题影响什么、由谁负责、修复后哪些结果需要重新计算或重新评估。**

本 Surface 是 **Data Quality Operations + Business Impact Workspace**，不是：

- “Data Health 92” 仪表盘；
- TSDB / ETL / Kafka 管理台；
- 设备告警中心；
- 网络监控工具；
- 32「计量与语义模型」的替代品；
- 34「集成管理」的替代品；
- 一个“数据不好就填 0 / last value”的兜底层；
- 自动重写历史业务结果的 Correction Engine。

用户必须能够回答：

1. 哪些点、表计、来源、模型当前存在数据问题；
2. 是缺失、过期、错误、不完整、不同步、语义错误，还是来源故障；
3. 问题从什么时候开始，是否仍在持续；
4. 哪些业务计算、报告、EnPI、M&V、效率指标、诊断结论受影响；
5. 影响是 Potential、Confirmed 还是 Unknown；
6. 谁是问题 owner，当前处理状态是什么；
7. 是否已经完成 source correction；
8. 修复后是否需要 downstream recomputation；
9. recomputation 是否已经完成并产生新的 result revision；
10. 历史结果是否仍然保留；
11. 数据是否 Measured / Estimated / Backfilled / Corrected；
12. Source Down 是否只是来源中断，而不是设备物理离线；
13. Stale 是否只是数据陈旧，而不是设备 Fault；
14. Source Health、Data Quality 和 Business Impact 是否被保持为不同事实。

---

# 2. 外部最佳实践依据

## 2.1 DOE / FEMP EMIS Technical Resources

DOE/FEMP 的 EMIS Technical Resources 明确要求在 EMIS 上线后对数据、可视化和 analytics 做 quality checking，并把常见问题列为：missing data / gaps、inaccurate sensor or meter data、erroneous data naming or modeling、insufficient data interval、software bugs、erroneous analytics、dropped communication。

来源：

- https://www.energy.gov/sites/default/files/2021-09/emis-technical-resources.pdf
- https://www.energy.gov/cmei/femp/what-are-energy-management-information-systems

**本页采用：** Data Quality 不只检查“有没有值”，还要覆盖 source、point/meter、semantic/model、analytics output，并把问题进入正式 issue/follow-up workflow。

## 2.2 DOE EMIS Capabilities

DOE/FEMP 明确说明 missing interval 可以被模型或 interpolation backfill，但 backfilled data 应标记为非 metered data。

来源：

- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities

**本页采用：**

```text
Measured ≠ Estimated ≠ Backfilled ≠ Corrected
```

## 2.3 BACnet Status / Reliability

BACnet 对 `IN_ALARM`、`FAULT`、`OVERRIDDEN`、`OUT_OF_SERVICE`、Reliability / communication failure 分别建模。

来源：

- https://bacnet.org/
- https://bacnet.org/wp-content/uploads/sites/4/2022/08/BAC-09-08.pdf
- https://bacnet.org/addenda/

**本页采用：**

```text
Bad Quality ≠ Alarm
Fault ≠ Out of Service
Communication Failure ≠ Equipment Physical Failure
```

## 2.4 NIST Time Synchronization

NIST 的数据质量研究明确指出 accurate clock synchronization 与 time stamping 对 distributed system data quality 很重要，timestamp 应尽量反映数据实际测量/生成时刻。

来源：

- https://www.nist.gov/publications/advancing-factory-wide-data-quality-apc-applications
- https://tsapps.nist.gov/publication/get_pdf.cfm?pub_id=32533

**本页采用：** Event Time 与 Ingest Time 分开；clock drift / timestamp lag / cross-source alignment 是独立质量维度。

## 2.5 DOE Semantic Modeling / ASHRAE 223P

DOE Semantic Modeling 项目强调 building analytics / control 需要明确表示设备、点位、空间与关系，避免依赖名字猜语义；ASHRAE 223P 的目标是正式定义可供 analytics / automation 使用的 machine-readable semantic model。

来源：

- https://www.energy.gov/cmei/buildings/semantic-modeling-and-interoperability
- https://www.energy.gov/cmei/buildings/ashrae-standard-223p
- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**本页采用：** wrong mapping / wrong unit / wrong relationship 是一等问题；31 负责问题与影响，32 负责正式修改 semantic / meter model。

---

# 3. 产品语言契约

主界面中文优先：

```text
数据质量
数据覆盖率
数据新鲜度
数据完整性
有效性
时间同步
来源健康
数据问题
业务影响
问题责任人
修复状态
重新计算
结果版本
```

保留必要专业词：

```text
Coverage
Freshness
Completeness
Validity
Synchronization
Lineage
Backfill
Recompute
```

内部字段如 Topic、Shard、Storage Key、Trace ID 不进入默认业务界面。

---

# 4. Data Quality Domain Vocabulary

## Data Observation

带 value、unit、event time、quality/provenance 的数据事实。

## Data Quality Dimension

对某个数据对象的独立质量维度，例如 coverage、freshness、validity。

## Data Quality Issue

需要调查、修复或影响评估的正式数据问题。

## Source Health

connector / gateway / source 是否能正常提供数据。

## Business Impact

数据问题对业务计算、分析、报告或管理决策产生的影响。

## Correction

对 raw data、mapping、unit、timestamp、metadata 的正式修正。

## Recomputation

Correction 后由 downstream owner 重新计算受影响结果。

## Result Revision

Recomputation 后产生的新权威结果版本，旧版本继续保留。

---

# 5. Mandatory Semantic Separation

```text
Missing ≠ 0
Stale ≠ Offline
Bad Quality ≠ Alarm
Source Down ≠ Device Down
Fresh ≠ Valid
Complete ≠ Accurate
Complete ≠ Good Quality
Coverage ≠ Completeness
Freshness ≠ Synchronization
Validity ≠ Accuracy
Source Health ≠ Data Quality
Data Quality ≠ Business Impact
Measured ≠ Estimated
Estimated ≠ Backfilled
Backfilled ≠ Corrected
Event Time ≠ Ingest Time
Clock Drift ≠ Network Delay
Communication Failure ≠ Equipment Fault
Out of Service ≠ Missing Data automatically
Wrong Unit ≠ Bad Sensor necessarily
Wrong Mapping ≠ Bad Raw Value necessarily
Issue Closed ≠ Source Corrected
Source Corrected ≠ Recomputed
Recomputed ≠ Historical Result Overwritten
Data Corrected ≠ Historical Report Rewritten
```

---

# 6. Information Architecture

```text
Context Header
↓
Business Impact Summary
↓
Data Quality Dimensions
  Coverage
  Freshness
  Completeness
  Validity
  Synchronization
  Source Health
↓
Issue Ledger
                         → Issue Inspector
↓
Selected Issue
  Identity
  Evidence
  Affected Data Objects
  Business Impact
  Lineage
  Owner / Status
  Correction
  Recomputation
↓
Downstream Impact Graph
↓
Correction / Recomputation History
↓
Audit
```

默认是 **impact-first + issue-ledger workspace**，不是 Data Platform Dashboard。

---

# 7. Route / URL State Contract

Canonical routes：

```text
/sites/:siteId/data-quality
/sites/:siteId/data-quality/issues/:issueId
```

Search Params：

```text
scope
source
objectType
qualityDimension
status
impact
owner
businessDomain
selectedIssue
```

Issue ID 是 durable/shareable context。

---

# 8. Capability Gating

只有真实 capability 存在时才显示：

```text
Meter Quality
BAS Point Quality
Billing Data Quality
Carbon Data Quality
M&V Data Quality
Semantic Model Quality
Integration Source Health
```

没有 Billing capability 时，不显示“Billing Quality = 100%”。

---

# 9. Quality Dimension Contract

至少独立表达：

```text
Coverage
Freshness
Completeness
Validity
Accuracy / Plausibility（owner-defined）
Synchronization
Source Health
Semantic Integrity
Unit / Scale Integrity
Lineage Integrity
```

不要压成单一 `healthScore`。

---

# 10. Coverage Contract

Coverage 必须知道 expected cadence / window / timezone / expected count-or-duration / observed count-or-duration / missing intervals / method owner。

如果 expected cadence 不明确：

```text
Coverage = Unknown
```

不能“收到 100 个 sample 就是 100% coverage”。

---

# 11. Freshness / Stale Contract

Freshness 基于：

```text
Event Time
Evaluation Time
Owner-defined cadence / threshold
```

```text
Stale ≠ Offline
```

可以出现：

```text
Source connected
Device online
Point stale
```

---

# 12. Completeness Contract

Completeness 面向业务计算所需输入，而不是单点是否存在。

例如 COP 需要：

```text
Thermal Load
Input Power
Timestamp Alignment
Quality
```

缺一项即可使 calculation completeness 不满足，即使其他点 coverage 很高。

---

# 13. Validity / Plausibility Contract

Validity 可考虑：

```text
type
unit
range
mode applicability
quality flag
```

但：

```text
Plausible ≠ Accurate
```

22°C 看起来合理，不代表传感器没有 calibration bias。

---

# 14. Synchronization Contract

同步质量关注：

```text
Clock Drift
Timestamp Offset
Cross-source Alignment
Sampling Alignment
Event Ordering
```

不同点都“Fresh”不代表可以同时参与 load / ΔT / COP 计算。

---

# 15. Event Time vs Ingest Time Contract

必须保留 Event Time、Ingest Time 与 Observed Delay。若 source 没有可信 Event Time，则 Timestamp Quality 必须显示 `Unknown / Limited`，不能静默用 ingest time 冒充 measurement time。

---

# 16. Source Health Contract

建议状态：`Connected / Degraded / Disconnected / Authentication Error / Rate Limited / Sync Lag / Partial Failure / Unknown`。

```text
Source Down ≠ Device Down
```

---

# 17. BACnet Quality Mapping Contract

如果来源是 BACnet，可消费 Reliability、Status_Flags、Out_Of_Service、Communication Failure，但保持原始语义：

```text
FAULT ≠ IN_ALARM
OUT_OF_SERVICE ≠ Missing automatically
```

31 不重新发明 BACnet 状态。

---

# 18. Estimated / Backfilled Contract

数据性质至少区分 `Measured / Estimated / Backfilled / Corrected / Allocated / Derived / Missing / Unknown`。Backfill 必须带 Method、Model Version、Owner、Window，并明确标识为非实测。

---

# 19. Missing Data Contract

```text
Missing ≠ 0
```

缺失值显示 gap / 缺失，不自动 forward-fill、last value 或 previous period。

---

# 20. Unit / Scale Contract

常见问题包括 °C/°F、Pa/kPa、kW/W、m³/h/L/s、CT/PT multiplier 和 sign convention。unit / scale 不确定时标记 Unknown / Suspect，不根据数值范围猜 unit。

---

# 21. Semantic Integrity Contract

一等问题包括 Wrong asset mapping、Wrong point role、Wrong meter parent、Wrong system relationship、Wrong served space、Wrong energy source、Missing semantic tag、Ambiguous relationship。

31 负责发现/影响/issue；32 负责正式修改 semantic / meter model。

```text
Naming ≠ Semantics
```

---

# 22. Lineage Contract

重要数据事实应可追溯：`Physical/Source Object → Connector → Normalization → Mapping → Quality Processing → Derived Calculation → Business Metric → Report/Decision Consumer`。Lineage 来自 authoritative model，不靠 timestamp/name 猜。

---

# 23. Business Impact Contract

建议区分 `No Known Impact / Potential Impact / Confirmed Impact / Unknown`，并列出受影响 Metric、Finding、Opportunity、EnPI、M&V、Report、Management Review Input、Control Decision Input。

---

# 24. Downstream Impact Graph Contract

示例：

```text
CHW Flow Meter M-02
↓
Cooling Load
↓
Plant kW/RT
↓
16 Efficiency
↓
17 EnPI
↓
24 M&V MV-12
↓
29 Report rev2
↓
30 Review Pack rev3
```

图只显示 authoritative lineage。

---

# 25. Issue Contract

每个 Data Quality Issue 至少包括 Issue ID、Issue Type、Affected Objects、Quality Dimension、Detected At、Effective Start、Current State、Evidence、Owner、Business Impact、Root Cause State、Correction State、Recompute State。

---

# 26. Issue Lifecycle Contract

建议：`Detected → Triaged → Assigned → Investigating → Correction Planned → Correction In Progress → Source Corrected → Recompute Required → Recomputing → Resolved → Closed`，另有 `False Positive`。

```text
Source Corrected ≠ Recomputed
Issue Closed ≠ Downstream History Rewritten
```

---

# 27. Data Quality Finding vs Root Cause

```text
Finding ≠ Root Cause
```

例如 Flow meter flatline 是 finding；sensor failure、mapping、network、controller freeze 才可能是 hypotheses/root cause。31 不把 pattern 自动升级成根因。

---

# 28. Owner Contract

Issue owner 可来自 Controls、Metering、Integration、Data Platform、Energy Analytics、Vendor、Facility。Owner 由治理模型给出，前端不按 source type 猜。

---

# 29. Correction Contract

Correction 至少记录 Correction ID、Issue ID、Affected Object/Window、Correction Type、Before/After、Method、Performed By/At、Reason、Approval（若需要）。Correction type 可包括 raw data correction、backfill、unit/scale、timestamp、semantic remap、meter relation、source config、quality flag correction。

---

# 30. Correction ≠ History Erasure

如果系统支持 revisioned data，应保留 Original Observation + Correction Revision；若 source 只能提供 corrected value，也至少保留 correction event / audit。修好以后不能让历史看起来“从来没出过问题”。

---

# 31. Recomputation Contract

Correction 后由 downstream owner 判断 `No Recompute Required / Recompute Required / Recompute Scheduled / Recomputing / Recomputed / Recompute Failed / Inconclusive / Unknown`。

```text
Source Corrected ≠ Recomputed
```

---

# 32. Recomputation ≠ Historical Overwrite

重新计算必须产生新的 Result Revision，例如 `MV-12 rev1 → data correction → MV-12 rev2`，rev1 保留历史。

```text
Recomputed ≠ Historical Result Overwritten
```

---

# 33. Report / Review Impact Contract

如果 correction 影响 29 Report / 30 Review Pack，31 只产生 Impact Signal / Reference。Report correction/reissue 进入 29，Management Review follow-up 进入 30，禁止自动修改正式报告或管理决策。

---

# 34. M&V Impact Contract

数据问题影响 M&V 时产生 `M&V Impact Assessment Required`，但 24 才是 recomputation/result revision owner；31 不自行重算 Verified Savings。


# 35. Control Safety Boundary

Data Quality 可以成为 Control Availability / Guardrail / Fail-safe 的输入，但 31 不自行执行控制。

```text
Bad Data ≠ Frontend automatically disable control
```

25/26/27 的 Control/Strategy owner 决定 block、suspend、degraded、fail-safe。

---

# 36. Alarm Boundary

```text
Bad Quality ≠ Alarm automatically
```

如果需要 Data Quality Alarm，由 33 Rule / Alarm Definition 正式定义，09 处理 Alarm occurrence，31 处理 Data Quality Issue。

---

# 37. Integration / Semantic Boundaries

32 正式拥有 meter hierarchy、units/ranges、semantic relationships、virtual meter、effective date。

34 正式拥有 connector、auth、sync configuration、mapping deployment、connection test。

31 只负责 quality issue、business impact、correction/recompute tracking，不成为万能配置页。

---

# 38. Issue Ledger Contract

默认列：问题、影响对象、质量维度、开始时间、持续时长、业务影响、Owner、状态、Correction、Recompute。

---

# 39. Default Prioritization Contract

优先显示：Confirmed high business impact、M&V/EnPI/official report impacted、Control/safety decision input impacted、critical meter/source issue、long-running missing/stale issue、unowned issue、correction done but recompute pending、recompute failed，而不是纯 `detectedAt DESC`。

---

# 40. Issue Inspector Contract

快速显示 Issue identity、Affected source/object、Quality dimension、Evidence、Current state、Business impact、Owner、Correction status、Recompute status、Next action。

---

# 41. Evidence Contract

Evidence 可以包括 gap timeline、flatline、out-of-range、timestamp drift、quality flag history、source disconnect、unit mismatch、mapping mismatch、reference comparison，并必须带 source/window/method。

```text
Flatline ≠ Sensor Failed
Out-of-range ≠ Root Cause
```

---

# 42. Time-series Rendering Contract

遵守 05 Trend Analysis：

```text
Missing → gap
Estimated / Backfilled → explicit
Suspect → explicit
Corrected revision → annotation
```

禁止用连线穿过 missing gap。

---

# 43. Quality Rule Version Contract

quality rule/method 应有 Version、Effective Period、Scope、Owner。规则升级不改写历史 issue 的判定上下文。

---

# 44. Historical Integrity Contract

至少保留 Issue Detection、Evidence、Rule Version、Correction、Recompute、Result Revision、Impact Decision。修复后不能把历史问题从记录中抹掉。

---

# 45. Query / Read Model Contract

Issue Ledger 使用服务端 summary read model。禁止 `100 issues → 100 source queries → 100 lineage queries → 100 impact queries → 100 recompute queries`。heavy evidence / lineage 按需加载。

---

# 46. Snapshot + Event Stream Contract

实时 quality view 使用 `Snapshot + Quality/Source Event Stream`。

```text
Stream Disconnect ≠ Source Down
Stream Disconnect ≠ Device Offline
Stream Disconnect ≠ Issue Resolved
```

Reconnect 后以 authoritative snapshot 对账。

---

# 47. Late / Out-of-order Data Contract

Late data 需要区分 Event Time、Arrival Time、Data Cutoff；Out-of-order arrival 不代表业务事件顺序。如果 late data 已超过 29 Report Data Cutoff，应产生 Impact Assessment Required，而不是静默修改旧报告。

---

# 48. Security Boundary Contract

Backend 验证 Principal、Site Scope、Issue Read、Issue Triage、Correction Permission、Recompute Request Permission、Sensitive Source Permission、Audit Permission。能看 Data Quality 不等于能改历史数据。

---

# 49. Audit Contract

至少审计 issue detected/assigned/state changed、correction proposed/applied/approved/rejected、recompute requested/completed/failed、result revision created、impact assessment changed、issue closed、false-positive decision。每条包括 Who / When / Before-After / Reason / Evidence / Reference。

---

# 50. AI Assistance Boundary

AI 可以总结 evidence、整理 hypotheses、解释 lineage、提示缺失证据、草拟 correction note、总结 downstream impact。

AI 不能：

```text
Missing → 0
自动 authoritative backfill
自行修改 semantic/meter mapping
自行确认 root cause
自行关闭 issue
自行宣布 recomputation valid
自行重写 historical M&V/report
自行把 suspect 改成 good
```

AI 输出默认 Draft / Review Required。

---

# 51. Browser Acceptance Criteria

## Semantics

- Missing 不显示 0；
- Stale 不显示 Offline；
- Bad Quality 不显示 Alarm；
- Source Down 不显示 Device Down；
- Measured / Estimated / Backfilled / Corrected 可区分；
- Event Time / Ingest Time 可追溯。

## Dimensions

- Coverage / Freshness / Completeness / Validity / Synchronization 分离；
- Source Health / Data Quality / Business Impact 分离；
- 不以黑盒 Data Health 总分替代专业事实。

## Impact

- 关键 issue 有 authoritative downstream impact；
- M&V / EnPI / Report / Review impact 可追溯；
- correction 不自动修改历史业务结果。

## Correction / Recomputation

- Source Corrected 不等于 Recomputed；
- Recomputed 不覆盖旧 result revision；
- recompute failure 明确；
- report/review impact 进入对应 owner workflow。

## Accessibility / Responsive

- semantic table / headings；
- 状态不只靠颜色；
- chart gap 可感知；
- keyboard 可操作；
- 768px 保留 Issue、Impact、Owner、Correction、Recompute；
- 无 page-level 横向 overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无前端 authoritative quality rule；
- 无前端 authoritative recomputation；
- 无 old Data Health fallback / legacy quality adapter；
- 无 N+1 issue queries；
- review scenario 无 runtime/network error。

---

# 52. No Defensive Programming / No Compatibility Design

明确禁止：

```text
data quality API error → []
missing → 0
missing → previous value
missing → previous period
stale → offline
bad quality → alarm
source down → device offline
fresh → valid
complete → accurate
coverage 100% → good quality
estimated → measured
backfilled → measured
corrected → original measured
event time missing → ingest time silently
clock drift unknown → synchronized
unit missing → guess from value range
semantic mapping missing → infer from point name
range missing → hardcoded global range
quality threshold missing → global default
flatline → sensor failed
communication failure → equipment fault
BACnet out-of-service → missing data
source corrected → issue resolved
source corrected → all downstream recomputed
issue closed → impacted M&V corrected
recompute → overwrite old result
recompute complete → authoritative result approved
source correction → rewrite published report
source correction → rewrite management review history
same timestamp → infer lineage / causation
multiple data-quality APIs → first success wins
old Data Health Dashboard fallback
legacy quality compatibility adapter
```

正式原则：

> **一个数据事实必须保留来源、时间、质量与数据性质；一个 Data Quality Issue 必须有明确 owner 和业务影响。Missing 不是 0，Stale 不是 Offline，Bad Quality 不是 Alarm，Source Down 不是 Device Down。Source Correction、Recomputation、Result Revision 与 Historical Record 是不同事实；Unknown 保持 Unknown。**

---

# 53. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 数据质量 · 中央园区                                      [问题视图] [来源视图]│
├──────────────────────────────────────────────────────────────────────────────┤
│ 高业务影响 4   影响 M&V 1   影响正式报告 2   未分派 3   待重算 5             │
├──────────────────────────────────────────────────────────────────────────────┤
│ 数据问题             影响对象      维度       业务影响        Owner   状态    │
│ CHW Flow M-02 缺口   冷量计        Coverage   M&V / EnPI      Data    修复中  │
│ SAT-03 数据陈旧      AHU-03        Freshness  运行判断         Controls 调查中│
│ Main Meter 单位异常  主电表        Unit       Energy / Carbon Metering 待处理 │
│ BAS 时间漂移         BAS-GW-02     Sync       趋势 / FDD       OT      已分派  │
├───────────────────────────────────────────────┬──────────────────────────────┤
│ 选中：CHW Flow M-02 缺口                     │ 快速判断                     │
│ 开始：09/11 02:14                             │ Coverage   71%              │
│ Source：BAS-GW-01                             │ Freshness  正常              │
│ 数据性质：Measured + Missing                  │ Source     Connected         │
│                                               │ 影响        Confirmed        │
├──────────────────────────────────────────────────────────────────────────────┤
│ 下游影响                                                                     │
│ CHW Flow M-02 → Cooling Load → kW/RT → EnPI v4 → MV-12 → 月报 rev2          │
├──────────────────────────────────────────────────────────────────────────────┤
│ 修复：Source correction 进行中 · Backfill 若使用必须标记非实测              │
│ 重算：Cooling Load / EnPI / MV-12 Required · Report impact review required  │
├──────────────────────────────────────────────────────────────────────────────┤
│ 原始结果不会因修复自动覆盖；重算必须产生新的 Result Revision                 │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

# 54. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：Coverage / Freshness / Completeness / Validity / Synchronization 分离；Source Health / Data Quality / Business Impact 分离；Missing / Stale / Offline / Alarm 语义明确；Measured / Estimated / Backfilled / Corrected 分离；Event Time / Ingest Time 明确；BACnet quality/status 语义保留；Unit / Scale / Semantic issue 明确；Lineage / Downstream Impact 明确；Issue lifecycle 明确；Correction / Recomputation 分离；Recomputation / Result Revision / Historical Integrity 明确；29/30/24/17 边界明确；32 Semantic / Meter、34 Integration 边界明确；Security / Audit、AI Boundary、No Defensive Programming、中文产品语言和 Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**

