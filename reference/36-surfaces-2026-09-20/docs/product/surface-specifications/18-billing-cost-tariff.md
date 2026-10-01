# 18 账单、成本与电价 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `18 账单、成本与电价（Capability-gated）`  
> **Route intent：** `/sites/:siteId/billing`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`TOU`、`kWh`、`kW` 等行业缩写/单位保留，但必须有清楚中文业务语义。  
> **设计输入声明：** 本文件不参考当前项目已有 Cost Dashboard、旧账单页、旧电价页、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 Utility Bill / Account / Meter / Tariff / Finance / Cost Allocation / Permission / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **让用户准确知道“这期账单收了什么、为什么收这么多、账单与真实计量是否一致、当前适用什么电价规则，以及成本由哪些可解释组成部分形成”。**

本 Surface 是 **utility-bill reconciliation + energy-cost analysis workspace**，不是：

- 14「能源分析」的成本 Tab；
- 15「需求、负荷与柔性」的 demand-charge 卡片；
- 财务总账；
- 发票支付系统；
- tariff authoring tool；
- 用 `kWh × 单价` 临时估账单的计算器；
- 自动把 meter discrepancy 当 utility billing error 的 AI 页面；
- 把预测成本冒充已结算成本的 Dashboard。

用户离开本页前应该知道：

1. 当前站点有哪些真实 utility accounts / bills；
2. 某一期账单覆盖哪个 billing period；
3. 用量是 Actual、Estimated、Mixed 还是 Unknown；
4. 账单是否已完成 meter/interval reconciliation；
5. 哪些 line items 构成总成本；
6. 当前使用哪个 tariff/rate version；
7. Energy Charge、Demand Charge、Fixed Charge、Tax/Fee/Credit 分别是多少；
8. Billing Demand 是如何定义的，是否包含 TOU / ratchet / coincident peak；
9. 账单是否存在 missing / duplicate / overlap / suspect / corrected/rebill；
10. 继续应该进入 Demand、Energy、Opportunity、Data Quality 还是 Billing Correction workflow。

---

# 2. 主要用户

## Primary

### 能源经理

审核能源费用、识别异常账单、理解 tariff 与 demand-cost exposure。

### Utility / Cost Analyst

进行 bill validation、meter reconciliation、rate interpretation 和 cost breakdown。

### 站点能源工程师

从异常费用进入 Energy / Demand / Meter / Data Quality 调查。

## Secondary

- Facility Manager：理解为什么某月费用上升；
- 财务/采购：在有 finance integration 时读取付款/争议状态；
- 可持续发展负责人：理解能源成本，但不把成本变化当能源绩效本身；
- Optimization Engineer：识别 TOU / demand-charge related opportunities；
- Data Engineer：处理 missing meter / interval / bill quality issues。

---

# 3. 外部最佳实践依据

## 3.1 DOE/FEMP — Utility Cost 由 Energy、Demand、Fixed 等不同收费项组成

DOE/FEMP 的 Utility Rate Guidance 明确把典型电费拆为：

```text
Energy charges
Demand charges
Fixed charges
```

并进一步区分：

- time-of-use demand charges；
- non-coincident demand charges；
- demand ratchet / look-back；
- system-peak / coincident demand charges；
- standby / departing-load charges；
- minimum-import requirements。

来源：

- https://www.energy.gov/cmei/femp/evaluating-your-utility-rate-options
- https://www.energy.gov/cmei/femp/events/optimizing-utility-costs-1-utility-rate-and-billing-basics

**本页采用：**

- `Site Peak ≠ Billing Demand`；
- demand cost 必须来自 tariff/bill owner；
- fixed、energy、demand、tax/fee/credit 分开表达；
- rate schedule/version 是成本解释的一等事实。

## 3.2 DOE/FEMP EMIS — Advanced Bill Processing 需要 AMI / Bill Reconciliation

DOE/FEMP EMIS 明确指出，advanced utility-bill validation 可以把先进计量基础设施（AMI）总量与 utility bill reported data 进行比较，也可以支持 building / department / tenant cost allocation。

来源：

- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities

**本页采用：**

- Bill 与 Meter 是两个事实来源；
- reconciliation 是正式 workflow；
- discrepancy 不自动说明 utility 错误；
- allocation method 必须有 provenance。

## 3.3 ENERGY STAR Portfolio Manager — Estimated Bill 必须显式保留

ENERGY STAR Portfolio Manager 支持 meter bill entry 的 `Estimated Data Flag`，并允许记录 usage、total cost、demand 与 demand cost。

来源：

- https://portfoliomanager.energystar.gov/pm/glossary
- https://www.energystar.gov/sites/default/files/2024-04/How%20to%20Track%20Electric%20Demand_March%202024%20508C.pdf

**本页采用：**

```text
Actual
Estimated
Mixed
Unknown
```

不能把 Estimated 去掉后仍把总费用当完全实测事实。

## 3.4 ENERGY STAR — Billing Period Overlap / Gap 是需要处理的数据问题

ENERGY STAR 的 utility-data guidance 明确提醒 meter entries 出现 overlaps 或 gaps 时属于数据问题，需要用户修正。

来源：

- https://www.energystar.gov/sites/default/files/2024-04/How%20to%20Get%20utility%20data%20into%20Portfolio%20Manager%20508C.pdf

**本页采用：**

- Billing periods 需要 gap/overlap validation；
- missing bill 与 zero bill 分开；
- duplicate / overlap / gap 由 billing/data owner 判定，不由前端字符串匹配。

## 3.5 FEMP — Bundled / Unbundled Supply 与 Delivery 要分开

DOE/FEMP 说明电力可能是 bundled rate，也可能 supply 与 delivery 分开，由不同 provider 出账或合并出账。

来源：

- https://www.energy.gov/cmei/femp/evaluating-your-utility-rate-options

**本页采用：**

- 一个 Site 可以有多个 utility/provider/account；
- Supply Bill 与 Delivery Bill 可能覆盖同一 energy period；
- 不能把两张账单误判 duplicate；
- cost breakdown 需要保留 provider / service role。

## 3.6 NARUC — Demand Charge 取决于正式测量窗口和费率设计

NARUC rate-design guidance 说明 demand charge 通常依据某一时间段内的最大 demand 或 measurement interval（常见 15/30/60 分钟）计算，且不同 rate structure 的峰值定义不同。

来源：

- https://pubs.naruc.org/pub/19FDF48B-AA57-5160-DBA1-BE2E9C2F7EA0

**本页采用：**

- demand window 必须显式；
- frontend 不从任意 telemetry sample 生成 Billing Demand；
- rate/version/effective period 不可省略。

## 3.7 OpenEI Utility Rate Database — Tariff 是有结构和 Revision 的对象

OpenEI U.S. Utility Rate Database 展示实际 rate schedules 包含：

- fixed charges；
- seasonal energy tiers；
- TOU schedules；
- monthly / TOU demand structures；
- minimums；
- revision history。

来源：

- https://apps.openei.org/USURDB/

**本页采用：**

- Tariff/Rate 是 versioned business object；
- 账单必须绑定实际 applicable rate revision；
- tariff 更新不能重写历史账单的 rate context。

---

# 4. 产品语言契约

中文主界面优先使用：

```text
账单
成本分析
计费周期
实际用量
估算用量
账单总额
能源费
需量费
固定费用
税费及附加
优惠 / 返还
分时电价（TOU）
计费用量
计费需量
电价版本
对账状态
成本分摊
```

避免主界面直接使用：

```text
Bill Status
Reconciliation
Tariff Version
Demand Charge
Ratchet
Cost Allocation
```

必要专业缩写可保留并给中文解释，例如：

```text
分时电价（TOU）
先进计量基础设施（AMI）
```

---

# 5. Domain Vocabulary

## 5.1 Utility Bill / 账单

由 utility / supplier / provider 针对特定 account 与 service period 出具的正式费用事实。

它不是 meter data。

## 5.2 Billing Period / 计费周期

账单对应的服务起止时间。

它不一定等于：

```text
calendar month
analysis period
meter interval period
```

## 5.3 Bill Revision / Rebill

Provider 对既有账单进行更正、重新出具或撤销替代的正式 revision/document relation。

旧账单不可直接删除后失去审计关系。

## 5.4 Actual / Estimated / Mixed

表示账单中 usage/read/charge 的数据性质，由 billing/provider owner 提供。

`Estimated` 不是 `Invalid`，但必须显式。

## 5.5 Tariff / Rate Schedule

决定收费结构与适用规则的正式费率对象。

至少可能包含：

```text
energy rate
demand rate
TOU schedule
season
tier
ratchet
fixed charge
minimum charge
rider/adjustment
export credit
```

## 5.6 Energy Charge / 能源费

基于能源用量（例如 kWh）及适用 rate 计算的收费项。

## 5.7 Demand Charge / 需量费

基于正式 Billing Demand 及 demand-rate rule 形成的收费项。

## 5.8 Fixed Charge / 固定费用

不随当期能源用量直接变化的固定收费项。

## 5.9 Billing Demand / 计费需量

由 tariff/billing owner 根据正式规则确定的可计费 demand。

不是 Site Analysis Peak 的同义词。

## 5.10 Reconciliation / 对账

把 Bill facts 与 Meter / Interval / Tariff facts 按正式规则比较，并记录 variance / exception / resolution 的过程。

## 5.11 Allocation / 成本分摊

将已经发生的费用按照正式 allocation method 分配给 building、department、tenant、cost center、system 等对象。

Allocation 不等于物理 metering。

---

# 6. Mandatory Semantic Separation

以下全部禁止混同：

```text
Bill = Meter
Bill Usage = Interval Meter Total automatically
Billing Period = Calendar Month
Actual = Estimated
Estimated = Invalid
Bill Received = Reconciled
Reconciled = Paid
Paid = Correct
Bill Total = Energy Charge
Site Peak = Billing Demand
Billing Demand = Demand Charge Cost
Average Unit Cost = Tariff Rate
Tariff Rate = Marginal Rate
Current Tariff = Historical Bill Tariff
TOU Window = DR Event
Projected Cost = Billed Cost
Billed Cost = Paid / Settled Cost
Allocation = Measurement
Allocated Cost = Tenant Invoice
Duplicate Bill = Same Billing Period automatically
Missing Bill = Zero Cost
Rebill = Overwrite Old Bill
Meter Variance = Utility Error
Variance = Savings
```

正确关系：

```text
Utility Account / Service
↓
Bill Document + Revision
↓
Bill Line Items
↓
Tariff / Rate Context
↓
Meter / Interval Reconciliation
↓
Cost Analysis
↓
Exception / Correction / Allocation
```

---

# 7. Primary Questions

## Q1 — 本期到底收了多少钱？

显示：

- bill total；
- currency；
- billing period；
- provider/account；
- issue date / due date；
- actual/estimated state；
- revision/correction state。

## Q2 — 钱花在哪里？

分解：

```text
能源费
需量费
固定费用
税费及附加
Rider / Adjustment
Credit / Incentive
Other owner-defined items
```

## Q3 — 为什么需量费这么高？

显示：

- Billing Demand；
- demand interval/window；
- demand rate；
- on-peak/non-coincident/coincident semantics；
- ratchet/look-back if applicable；
- deep-link 到 15。

## Q4 — 账单和计量是否一致？

显示：

- billed usage；
- meter total；
- variance；
- coverage；
- reconciliation method；
- exception reason。

## Q5 — 现在适用什么电价？

显示：

- utility/provider；
- rate schedule；
- version/revision；
- effective period；
- customer/service class；
- TOU / season / tier / demand rule。

## Q6 — 哪些账单需要处理？

识别：

- missing；
- estimated；
- duplicate/suspect；
- overlap/gap；
- reconciliation variance；
- corrected/rebill；
- disputed if finance/billing owner supports。

## Q7 — 下一步去哪？

进入：

- 14 能源分析；
- 15 需求、负荷与柔性；
- 21 节能机会；
- 31 数据质量；
- durable Bill Correction / Reconciliation workflow。

---

# 8. Capability Gating

本 Surface 只有在站点存在真实 billing / tariff capability 时出现。

典型 capability：

```text
utility-billing.read
utility-billing.reconcile
utility-billing.correct
utility-tariff.read
utility-cost-analysis.read
cost-allocation.read
finance-payment.read   // optional
```

如果只有 meter data，没有 utility bill：

- 14 / 15 正常存在；
- 18 不显示空壳账单页。

如果有 Bills 但没有 tariff model：

- Bills 仍可使用；
- detailed rate explanation / projected rate analysis 不出现。

---

# 9. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/billing
```

两个 peer views：

```text
账单
成本分析
```

推荐 Search Params：

```text
view              // bills | cost
account
provider
from
until
billState
reconciliation
estimated
anomaly
rateVersion
selectedBill
costGroup
```

对于需要 durable correction 的 bill，可采用同一 Surface 下 durable object route：

```text
/sites/:siteId/billing/:billId
```

或者由 Router contract 最终批准的等价 durable route。

禁止同时维护两套 detail route。

---

# 10. Entry Contract

## 从 14 能源分析

携带：

- Site；
- period；
- energy type；
- meter scope；
- source trail。

Analysis period 不自动改写为 Billing Period。

## 从 15 Demand Analysis

携带：

- selected peak；
- period；
- meter scope；
- demand context。

如果有真实 tariff owner，再显示对应 billing-demand / demand-charge context。

## 从 Data Quality

可以携带：

- meter/account；
- gap/overlap/correction issue；
- affected period。

## 从 Opportunity

携带：

- tariff/cost hypothesis；
- relevant bill period；
- source trail。

---

# 11. Exit Contract

主要出口：

```text
账单 / 成本
→ 14 能源分析
→ 15 需求、负荷与柔性
→ 21 节能机会
→ 31 数据质量
```

有对应能力时：

```text
→ Finance / Payment owner
→ Utility dispute/correction owner
```

必须保留：

- Site；
- account；
- billing period；
- bill id/revision；
- tariff version；
- selected cost/demand context。

---

# 12. Responsibility Boundary

本 Surface 拥有：

- Bill ledger projection；
- bill document/revision context；
- line-item breakdown；
- actual/estimated projection；
- reconciliation projection/workflow entry；
- tariff/rate context projection；
- cost-analysis projection；
- cost-allocation projection；
- billing anomaly / correction workflow context；
- drill-down to energy/demand/data-quality。

本 Surface 不拥有：

- raw meter ingestion；
- tariff source-of-truth authoring；
- finance general ledger；
- bank payment execution；
- utility dispute external communication unless separate integration owns it；
- M&V savings calculation；
- frontend-generated bill total；
- frontend-generated billing demand；
- frontend-generated tax/rider logic。

---

# 13. Information Architecture

```text
账单 / 成本上下文
  站点 · Utility Account · Provider · Currency

[账单] [成本分析]

账单视图
  账单工具栏
  ↓
  账单台账
  ↓
  选中账单摘要 / Inspector
  ↓
  Durable Bill Detail / Reconciliation / Correction

成本分析视图
  Period / Account / Tariff Context
  ↓
  实际总成本
  ↓
  成本组成
  ↓
  Energy / Demand / Fixed / Tax-Fee / Credit
  ↓
  TOU / Demand-charge Context
  ↓
  Meter / Cost Contributors
  ↓
  Allocation / Exception / Professional Detail
```

不是 Billing KPI Card Wall。

---

# 14. Bill Identity Contract

一张账单至少应有：

```text
Bill ID / provider document reference
Utility / Provider
Account
Service role
Service address / Site mapping
Billing period start/end
Issue date
Due date if available
Currency
Total amount
Revision / superseded relation
Source / ingestion method
```

内部 UUID 不作为主要 UI 标识。

优先显示 provider invoice/account reference。

---

# 15. Account / Service Contract

一个站点可能有多个：

- utility；
- provider；
- service account；
- meter；
- commodity；
- supply/delivery relationship。

必须明确：

```text
Provider
Account
Commodity
Service role
Meter/service mapping
Effective period
```

不能假设：

```text
1 site = 1 electric account
```

---

# 16. Bundled / Unbundled Contract

必须支持：

```text
Bundled
Supply + Delivery in one provider/bill
```

以及：

```text
Unbundled
Supply provider
+
Delivery utility
```

两张账单可能覆盖同一 kWh 和同一 billing period，但不是 duplicate。

Duplicate 判断必须考虑 provider/service role/account/document identity。

---

# 17. Billing Period Contract

Billing Period 是 provider-defined service period。

页面必须显示真实起止日期，例如：

```text
2026-08-18 → 2026-09-16
30 天
```

而不是强行显示：

```text
2026 年 9 月
```

Calendar-month grouping 可以作为分析维度，但不能改写 bill facts。

---

# 18. Actual / Estimated / Mixed Contract

账单数据性质至少区分：

```text
Actual
Estimated
Mixed
Unknown
```

可以进一步区分：

```text
Usage estimated
Demand estimated
Charge estimated
Provider-adjusted
```

前提是 owner 提供。

规则：

- Estimated 仍然是正式 bill fact；
- Estimated 不显示成 Actual；
- 后续 actual/rebill 不能覆盖历史 estimated document；
- corrected relation必须保留。

---

# 19. Bill State Dimensions

不要只做一个 `Bill Status` enum 压缩全部状态。

至少分开：

## Document State

```text
Current
Superseded by corrected bill
Voided
Unknown
```

## Validation / Reconciliation State

```text
Not Reviewed
Reconciling
Reconciled
Exception
Insufficient Data
Unknown
```

## Payment / Settlement State（只有 finance capability 时）

```text
Open
Approved for Payment
Paid
Disputed
Unknown
```

所以：

```text
Paid
≠ Reconciled
```

完全可能出现：

```text
Payment: Paid
Reconciliation: Exception
```

---

# 20. Bill Ledger Contract

默认核心字段：

```text
计费周期
Provider / Account
能源类型
实际/估算
用量
计费需量（适用时）
账单总额
对账状态
异常 / 更正状态
到期日 / 支付状态（capability-gated）
```

推荐约 8–10 个核心业务列。

不要把：

- internal id；
- ingestion job；
- raw parse state；
- source file hash；

放在主 ledger。

---

# 21. Bill Line Item Contract

Bill line item 保留 provider semantics。

常见分类：

```text
Energy Charge
Demand Charge
Fixed / Customer Charge
Fuel / Power Cost Adjustment
Transmission / Distribution
Rider / Surcharge
Tax / Fee
Credit / Incentive
Export / Net-metering Credit
Late / Finance Charge
Other provider-defined item
```

UI 可以做 canonical category projection，但必须保留原始 line description/provenance 在专业详情。

Frontend 不能通过关键词硬匹配：

```text
contains("demand") → Demand Charge
```

来创建 authoritative category。

---

# 22. Cost Composition Contract

成本分析默认回答：

> 总成本由什么构成？

推荐展示：

```text
总账单成本
├─ 能源费
├─ 需量费
├─ 固定费用
├─ 税费及附加
├─ Rider / Adjustment
├─ Credit
└─ 其他
```

每一类都来自 line-item owner/category mapping。

不要把税费、固定费隐藏后只展示“能源成本”。

---

# 23. Tariff / Rate Version Contract

Tariff 必须 versioned。

至少需要：

```text
Provider
Rate schedule name
Customer/service class
Version / revision
Effective from/until
Commodity
Currency
Source
```

历史 Bill 必须绑定当时实际适用版本。

如果当前 tariff 已更新：

```text
September Bill → Rate v7
October Current Rate → v8
```

不能用 v8 回算 September 后说历史账单“应该是另一个金额”，除非正式 what-if/rate-analysis owner 明确执行。

---

# 24. TOU Contract

分时电价（TOU）至少包含：

```text
Season
Day type
Time window
Rate period name
Energy rate
Demand rate if applicable
Effective period
```

UI 可以显示：

```text
高峰
平段
低谷
```

或 utility 原始名称的中文映射。

TOU Window：

```text
≠ DR Event
```

TOU 是 tariff schedule；DR Event 是事件/程序对象。

---

# 25. Energy Charge Contract

Energy Charge 必须来自：

- bill line items；或
- authoritative tariff-cost analytics owner。

不能前端：

```text
usage kWh × displayed rate
```

然后宣称与 bill total 等价，因为可能还存在：

- tier；
- TOU；
- seasonal pricing；
- adjustments；
- minimums；
- riders；
- taxes；
- credits。

---

# 26. Billing Demand Contract

计费需量至少需要：

```text
Value
Unit
Demand interval/window
Demand type
Applicable TOU/season
Measurement period
Tariff version
Source
```

Demand type 可能是：

```text
Non-coincident
On-peak
Part-peak
Contract demand
Ratchet demand
Coincident / system-peak contribution
Other provider-defined
```

Frontend 不将 15 的 Site Peak 自动映射为 Billing Demand。

---

# 27. Demand Charge Contract

需量费至少需要：

```text
Billing Demand
Rate / tier
Charge amount
Rate period
Tariff revision
Calculation owner
```

如果 bill 只有最终 line amount、没有完整 rate explanation：

- 展示实际 billed charge；
- rate explanation 标 unavailable；
- 不能前端逆推并当 authoritative rate。

---

# 28. Demand Ratchet / Look-back Contract

当 tariff 有 ratchet/look-back：

显示：

```text
Current period peak
Historical reference peak
Look-back window
Ratchet percentage/rule
Resulting billing demand
Tariff version
```

例如：

```text
当前站点峰值：812 kW
过去 11 个月最高：1,120 kW
Ratchet：80%
计费需量：896 kW
```

这些值必须来自 tariff/billing owner。

Frontend 不自己实现 ratchet engine。

---

# 29. Coincident / System Peak Billing Contract

如果存在 system/coincident peak charge：

显示：

- external system peak event/window；
- site demand during the relevant event；
- billing rule；
- contribution / charge result；
- settlement/revision state。

禁止：

```text
site local max
→ system coincident peak
```

---

# 30. Fixed / Tax / Fee / Rider Contract

固定费、税费、附加、rider 等必须作为真实 cost facts 保存。

页面不能为了“能源管理更简单”忽略它们后重新定义：

```text
Total Cost = Energy + Demand
```

如果实际账单还有其他费用，这个等式就是错误的。

---

# 31. Credit / Incentive / Export Contract

可能包括：

```text
Utility credit
DR credit
Efficiency incentive
Export credit
Net metering credit
Other adjustment
```

必须保持 source/program semantics。

例如：

```text
DR Credit
≠ Energy Savings
```

```text
Export Credit
≠ Negative Building Load
```

---

# 32. Bill vs Meter Reconciliation Contract

Reconciliation 至少比较：

```text
Bill usage
Meter/AMI total
Billing period alignment
Commodity/unit
Coverage
Estimated/corrected intervals
Meter replacement/reset events
```

输出可以包括：

```text
Bill: 769,500 kWh
Meter total: 762,840 kWh
Variance: +6,660 kWh (+0.87%)
Coverage: 100%
Status: Review Required
```

但 status/tolerance 来自 reconciliation owner。

Frontend 不硬编码：

```text
variance > 2% → bad bill
```

---

# 33. Reconciliation Outcome Contract

建议状态：

```text
Matched / Within Tolerance
Variance Requires Review
Incomplete Meter Coverage
Estimated Bill
Estimated Meter Data
Billing Period Misalignment
Meter Mapping Issue
Corrected / Rebill Pending
Resolved
Unknown
```

具体 enum 由 owner 定义。

关键是：

```text
Variance
≠ Utility Error automatically
```

原因可能是：

- estimated meter read；
- interval missing；
- billing-period mismatch；
- meter multiplier；
- meter replacement；
- provider correction；
- unmodeled service account；
- unit conversion；
- tariff adjustment；
- utility billing issue。

---

# 34. Reconciliation Method / Tolerance

必须可查看：

```text
Method
Tolerance
Source meters
Coverage rule
Unit conversion
Period alignment
Revision
Owner
```

Tolerance 是 business/analytics rule，不是 UI 常量。

---

# 35. Missing Bill Contract

Missing Bill 必须依赖：

- expected billing cadence；
- active account；
- provider relationship；
- grace period / ingestion expectation。

Frontend 不因为：

```text
last bill > 31 days ago
```

就直接创建 authoritative `Missing Bill`。

正确 owner 如果判定 missing，则页面显示：

```text
预计账单：2026-09
状态：尚未收到
预计到达：根据 provider cadence
```

而不是 `0 元`。

---

# 36. Duplicate / Overlap / Gap Contract

## Duplicate

必须由 document/account/provider/period/revision 等规则识别。

不能：

```text
same period + same amount → duplicate
```

## Overlap / Gap

明确显示：

```text
2026-07-18 → 2026-08-17
2026-08-16 → 2026-09-15
Overlap: 2 days
```

或：

```text
Gap: 3 days
```

但是否错误、是否 provider 正常周期，由 owner解释。

---

# 37. Corrected Bill / Rebill Contract

Rebill 必须保留 immutable relation：

```text
Bill A v1
↓ superseded by
Bill A v2
```

历史 v1 仍可审计。

主成本视图默认使用 current authoritative revision，但用户可以查看 revision history。

禁止：

```text
receive corrected bill
→ delete old bill
```

---

# 38. Bill Correction Workflow

Bill correction 是 durable workflow，不放在小 Dialog。

至少包含：

```text
Problem statement
Affected bill/revision
Affected account/period
Evidence
Meter reconciliation
Tariff context
Proposed correction / dispute
Owner
Status
Resolution
Audit trail
```

可能 workflow：

```text
Needs Review
↓
Investigating
↓
Awaiting Utility / Provider
↓
Corrected Bill Received
↓
Reconciled
↓
Resolved
```

具体 lifecycle 由 owner。

---

# 39. Projected / Billed / Settled Cost Contract

必须区分：

```text
Projected Current-cycle Cost
Billed Cost
Approved/Booked Cost
Paid/Settled Cost
```

只有对应 owner capability 存在时显示。

例如：

```text
本周期预测：$82,400
```

必须明确：

```text
预测
截至日期
模型/费率版本
数据覆盖
```

不能显示成 `当前账单 $82,400`。

---

# 40. Average Unit Cost Contract

可以显示：

```text
平均综合电价 = total billed cost / billed energy
```

如果 owner 定义。

但必须明确：

```text
平均综合电价
≠ Tariff Rate
≠ Marginal Energy Rate
```

例如 Total Cost 包含 demand/fixed/tax 后，`$/kWh` 只是 blended metric。

---

# 41. Cost Analysis Workspace

Cost Analysis 主要回答：

> 为什么成本变化？

建议结构：

```text
实际成本趋势
↓
成本组成
↓
能源 / 需量 / 固定 / 税费 / Credits
↓
Billing Demand / TOU Context
↓
与用量/需求变化对应
↓
成本贡献 / 分摊
↓
异常账单 / tariff change
```

Difference 不自动解释成 savings。

---

# 42. Cost Comparison Contract

允许比较：

```text
Current bill vs previous comparable bill
Current period vs same period last year
Cost component trend
Average blended cost trend
Demand-charge trend
```

比较必须说明：

- period lengths；
- provider/account；
- tariff version；
- estimated/actual state；
- currency。

如果 tariff change：

需要明确标注：

```text
2026-07-01 起 Rate v8 生效
```

不能把 rate change 带来的成本变化自动说成能源绩效变化。

---

# 43. Cost Contributor Contract

Cost contributor 可以按：

- account；
- provider；
- cost category；
- building / department / tenant；
- meter/submeter；
- owner-defined cost center。

必须标：

```text
Billed
Measured allocation
Rule-based allocation
Estimated allocation
Unallocated remainder
```

---

# 44. Cost Allocation Contract

Allocation method 至少有：

```text
Target scope
Source cost
Method
Driver
Effective period
Version
Owner
Coverage
Remainder handling
```

方法可能是：

- submeter-measured；
- usage-share；
- area-based；
- occupancy-based；
- contractual percentage；
- hybrid approved method。

但 UI 必须保持：

```text
Allocated Cost
≠ Measured Energy Cost
```

除非 method 就是直接 submeter billing。

---

# 45. Allocation Coverage / Remainder

如果总账单 $100,000，只能分配 $86,000：

必须显示：

```text
已分摊：$86,000
未分摊：$14,000
覆盖率：86%
```

禁止把已分配对象重新归一化为 100%。

---

# 46. Meter / Bill / Allocation Lineage

专业层需要能够回答：

```text
这笔费用来自哪张 bill？
对应哪个 account？
使用哪个 tariff version？
对应哪些 meter？
经过什么 allocation method？
最终分配给谁？
```

Lineage 不显示成复杂图优先。

默认结构化列表/表格；真实关系复杂且图能帮助理解时才用 graph。

---

# 47. Energy / Demand Boundary

14 Energy Analysis 回答：

> 用了多少能源？

15 Demand Analysis 回答：

> 什么时候峰值、计量需求是什么？

18 Billing 回答：

> Utility 实际收了多少钱，按什么规则收？

因此：

```text
Energy variance
≠ Cost variance
```

```text
Demand peak reduction
≠ Demand-charge savings automatically
```

因为 tariff/rate/ratchet 可能不同。

---

# 48. Opportunity Boundary

Cost anomaly / tariff exposure 可以产生 Opportunity candidate，例如：

- demand ratchet exposure；
- high on-peak usage；
- unfavorable rate schedule candidate；
- repeated estimated bills；
- meter/bill mismatch；
- avoidable late/penalty charges if product scope allows。

但 Opportunity owner 负责：

- proposed measure；
- feasibility；
- expected benefit；
- constraints；
- confidence。

18 不自动把“成本高”变成节能机会。

---

# 49. M&V Boundary

账单变化不能直接成为 Verified Savings。

例如：

```text
去年 $100k
今年 $80k
```

不能自动：

```text
Savings = $20k
```

原因可能是：

- tariff change；
- weather/load；
- estimated bill；
- billing-period difference；
- tax/rider change；
- demand ratchet；
- service change；
- project savings。

正式 savings 进入 Surface 24 M&V。

---

# 50. Currency Contract

每个 bill/cost fact 必须带 currency。

一个 Site 常规情况下使用本地 billing currency，但产品不能假设全系统只有一种 currency。

跨 currency portfolio aggregation 需要正式 FX/consolidation owner。

Frontend 不自行：

```text
USD + EUR
```

也不使用实时网络汇率临时合并账单。

---

# 51. Data Quality Contract

账单/成本至少区分：

```text
Actual
Estimated
Mixed
Missing
Duplicate / Suspect
Overlap
Gap
Corrected
Superseded
Unreconciled
Reconciliation Exception
Tariff Unknown
Meter Coverage Incomplete
Unknown
```

不能压成一个黑盒：

```text
Billing Health 87/100
```

---

# 52. Data Authority Contract

## Bill document / line items

Owner：Utility Billing / Provider Integration。

## Meter / interval facts

Owner：Meter / Historian。

## Tariff / rate schedule

Owner：Tariff / Utility Rate domain。

## Billing Demand

Owner：Billing/Tariff domain。

## Reconciliation

Owner：Billing Reconciliation / Energy Analytics domain。

## Payment / settlement

Owner：Finance domain（capability-gated）。

## Cost Allocation

Owner：Cost Allocation domain。

## Opportunity

Owner：Opportunity domain。

Frontend 只做 bill/cost-centered projection 和 authorized workflow mutation。

---

# 53. Query / Read Model Contract

推荐：

```text
Billing Projection
  + accounts/providers
  + bill ledger
  + current revision
  + actual/estimated state
  + bill totals
  + demand facts
  + reconciliation state
  + anomaly/correction state
  + tariff context
```

Cost Analysis Projection：

```text
Cost Analysis
  + total billed cost
  + category breakdown
  + cost trend
  + demand-charge context
  + TOU context
  + rate version changes
  + allocation summary
  + quality/reconciliation context
```

禁止：

```text
100 bills
→ 100 tariff requests
→ 100 meter requests
→ 100 reconciliation requests
→ 100 allocation requests
```

缺 projection/batch contract 时修 domain，不在前端 fan-out。

---

# 54. Mutation / Reconciliation Contract

允许的 mutation 可能包括：

- start reconciliation；
- mark/review exception；
- attach evidence；
- propose correction；
- resolve exception；
- select authoritative rebill revision；
- manage allocation rule if authorized；
- annotate/dispute if domain supports。

所有 mutation：

1. 显式用户动作；
2. 权限校验；
3. authoritative revision/state validation；
4. server-confirmed result；
5. audit trail。

不能 local-only 把 bill 改成 `Reconciled`。

---

# 55. Concurrency / Revision Contract

Bill reconciliation / correction / allocation rule 都可能多人操作。

要求：

- authoritative revision/version；
- conflict detection；
- refetch current state；
- user reconfirm；
- no silent last-write-wins。

Corrected Bill / Tariff Version 也必须 immutable/versioned。

---

# 56. Loading / Empty / Partial / Error

## No Billing Capability

整个 Surface 不出现。

不是显示：

> `暂无账单`

来占一个空菜单。

## Capability exists, no bills yet

> `当前站点尚未收到可用账单。`

与 unavailable 分开。

## Billing service unavailable

> `账单数据暂不可用。`

不能显示 `本期费用 0`。

## Tariff unavailable

Bills 仍显示实际费用；rate explanation 标：

> `电价规则暂不可用。`

不能用 generic rate fallback。

## Meter reconciliation unavailable

Bill facts 仍显示；reconciliation 标 unavailable。

不能显示 `已匹配`。

## Finance unavailable

不显示支付状态。

不能显示 `未支付`。

---

# 57. Permission / Capability Gating

示例：

```text
billing.read
billing.reconcile
billing.correct
billing.export
tariff.read
cost-analysis.read
cost-allocation.read
cost-allocation.manage
finance-payment.read
```

无权限动作不显示。

UI hiding 不替代 server authorization。

---

# 58. Export Contract

Bill export 至少保留：

```text
Site
Provider
Account
Bill reference
Revision
Billing period
Issue/due date
Currency
Usage
Actual/estimated flags
Demand
Line items
Total cost
Tariff version
Reconciliation state
Source/provenance
Export timestamp
```

Cost-analysis export 保留：

- period；
- accounts；
- currencies；
- cost categories；
- rate versions；
- allocation method/version；
- quality flags。

不能把 projected cost export 成 billed actual。

---

# 59. Visual / UX Contract

默认视觉层级：

```text
账单 / 成本上下文
↓
需要注意的账单
↓
账单台账 或 成本趋势
↓
费用组成
↓
需量 / TOU / Tariff Context
↓
Reconciliation / Allocation
↓
Professional Detail
```

禁止：

- `Billing Health 93`；
- 一排十几张财务 KPI 卡；
- 把 Estimated 隐藏在 tooltip；
- 用红色表示所有高金额账单；
- 把 Actual/Projected 混成同一曲线；
- 只展示总额不展示费用构成；
- 把 internal ingestion status 暴露给业务用户。

---

# 60. Bills View Contract

Bills View 是 ledger-first。

默认排序优先支持业务注意事项：

```text
Reconciliation Exception
Corrected/Rebill requiring review
Estimated bill
Missing expected bill
Duplicate/Suspect
Due/Disputed（如 finance capability）
Then newest billing period
```

不是单纯按 `created_at desc`。

选中 Bill 后 Inspector 只做：

- identity；
- period；
- amount；
- actual/estimated；
- cost composition；
- reconciliation；
- tariff version；
- 1–3 个 next actions。

复杂 correction 进入 durable detail/workflow。

---

# 61. Cost Analysis View Contract

Cost Analysis 是 analytical workspace，不是 Bill Ledger 的图形版。

结构：

```text
Period / Account / Provider / Commodity
↓
Billed Cost + Comparison
↓
Cost Composition
↓
Cost Trend
↓
Demand Charge / TOU Context
↓
Rate-version changes
↓
Allocation / Contributors
↓
Exceptions / Investigation exits
```

默认一个主要成本问题，不堆多个同权重图。

---

# 62. Chart Contract

## Cost Trend

优先：

- total billed cost；
- one named comparison；
- tariff version markers；
- actual/estimated markers。

## Cost Composition

优先 stacked bars / ranked bars；保留 exact table。

## Demand Charge Trend

显示：

- Billing Demand；
- Demand Cost；
- tariff context；

不同单位采用 aligned small multiples，不默认双 Y 轴。

## TOU Cost

可用：

- period breakdown；
- day/time heatmap if owner provides interval-rated cost；

但 billed line items 与 analytical interval allocation必须标明来源差异。

---

# 63. ECharts Boundary

ECharts 可以负责：

- cost trend；
- category breakdown；
- demand-charge trend；
- TOU visualization；
- comparison；
- tariff-version markers。

ECharts 不负责：

- tariff calculation；
- tax/rider calculation；
- billing-demand calculation；
- reconciliation result；
- duplicate detection；
- allocation rules；
- bill correction logic。

---

# 64. Component Mapping

```text
账单/成本 peer views       → Tabs
账单工具栏                 → Search + Select + Popover filters
账单台账                   → shadcn Table + TanStack Table
账单 Inspector             → Sheet / side inspector
账单 durable detail        → Route-owned page
费用组成                   → ECharts + semantic Table
成本趋势                   → dedicated ECharts
对账状态                   → Badge + explanatory text
Tariff / Rate 详情         → definition list / Collapsible
更正/争议 workflow         → durable route + Dialog only for bounded confirm
Allocation                 → semantic Table
```

不使用 generic finance dashboard framework。

---

# 65. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 账单、成本与电价 · 中央冷站                              货币：USD           │
│ Utility：APS · Account 4821••••                       [账单] [成本分析]       │
├──────────────────────────────────────────────────────────────────────────────┤
│ 需要关注：2 张账单   本期实际账单：$81,694   估算数据：1 期                 │
├──────────────────────────────────────────────────────────────────────────────┤
│ 计费周期          用量       计费需量    总额       数据性质   对账状态       │
│ 8/18–9/16       769.5 MWh   2,089 kW   $81,694    实际       需复核         │
│ 7/18–8/17       731.0 MWh   1,985 kW   $77,489    实际       已对账         │
│ 6/18–7/17       692.7 MWh   1,881 kW   $73,417    估算       已记录         │
├──────────────────────────────────────────┬───────────────────────────────────┤
│ 选中账单：8/18–9/16                     │ 费用组成                          │
│ Provider：APS                            │ 能源费      $45,955               │
│ 电价：Large General Service v8           │ 需量费      $29,576               │
│ 实际/估算：实际                          │ 固定/附加    $  819                │
│ 对账差异：+0.87% · 需复核                │ 税费         $5,344                │
│ [查看账单详情] [开始对账]                │                                   │
├──────────────────────────────────────────┴───────────────────────────────────┤
│ 计费需量                                                                      │
│ 本期站点峰值：2,030 kW   计费需量：2,089.5 kW   两者不同                    │
│ 原因：On-peak demand rule · Rate v8                         [需求分析]        │
├──────────────────────────────────────────────────────────────────────────────┤
│ [能源分析] [需求、负荷与柔性] [节能机会] [数据质量]                          │
└──────────────────────────────────────────────────────────────────────────────┘
```

主界面中文；utility/rate proper noun 保留官方名称。

---

# 66. Accessibility

必须：

- Actual / Estimated 不只靠颜色；
- reconciliation status 有文本 reason；
- cost composition 有 Table alternative；
- currency/unit 始终可读；
- corrected/superseded relation 对读屏可理解；
- ledger 有 semantic headers；
- exact chart values keyboard 可达；
- TOU windows 不只靠背景色；
- around 768px 不依赖 hover。

---

# 67. Responsive Behavior

## 1440–1720 px

Bills 首屏必须看到：

- provider/account；
- billing period；
- actual/estimated；
- usage/demand；
- total cost；
- reconciliation/anomaly；
- selected bill inspector。

Cost Analysis 首屏必须看到：

- period/context；
- total billed cost；
- major cost composition；
- cost trend；
- demand-charge / tariff context。

## 1024–1439 px

- Inspector 可变 Sheet；
- cost composition 和 trend 上下排列；
- table 保持业务列优先。

## Around 768 px

仍必须能：

- 找到账单；
- 查看 period / amount / actual-estimated；
- 查看 reconciliation；
- 查看费用组成；
- 查看 tariff / billing demand；
- 进入 durable correction / Energy / Demand / Data Quality。

Table 自身横向 scroll；页面整体不横向溢出。

---

# 68. No Defensive Programming / No Compatibility Design

明确禁止：

```text
billing API error → $0
missing bill → $0
bill total missing → sum visible lines in frontend as authoritative total
estimated bill → actual
payment API unavailable → unpaid
reconciliation unavailable → matched
meter unavailable → bill reconciled
bill usage unavailable → interval meter total fallback
meter total unavailable → bill usage fallback
site peak → billing demand
billing demand unavailable → site peak
rate unavailable → generic $/kWh
rate unavailable → previous tariff version silently
current tariff → recalculate historical bill automatically
energy kWh × rate → authoritative bill total
demand kW × displayed rate → authoritative demand charge
tax unavailable → 0 tax
fixed charge unavailable → 0
same period + same amount → duplicate
bill age > 31 days → missing bill
variance > hardcoded percent → utility error
reconciliation variance → savings
corrected bill → delete old bill
rebill → overwrite old revision
actual replaces estimated → rewrite historical document
supply + delivery bills → duplicate
allocation missing → proportional split automatically
allocation incomplete → normalize visible shares to 100%
projected cost → billed cost
billed cost → paid cost
average $/kWh → tariff rate
DR credit → energy savings
bill cost drop → verified savings
currency mismatch → frontend FX conversion
multiple billing APIs → first success wins
one bill → one tariff/meter/reconciliation/allocation request N+1
old Cost Dashboard adapter
old utility bill page fallback
frontend-generated Billing Demand
frontend-generated Tariff calculation
frontend-generated reconciliation status
```

不建立：

```text
new Billing unavailable
→ fallback old cost dashboard
```

原则：

> **One bill → one authoritative document lifecycle. Bill, meter, tariff, reconciliation and payment remain separate facts. Estimated is not actual. Site peak is not billing demand. Projected is not billed. Corrected documents preserve history. Unknown stays unknown.**

---

# 69. Browser Acceptance Criteria

## Capability

- 无 billing capability 时菜单/route 不作为正常业务入口显示；
- capability 存在但暂无 bill 与 service unavailable 明确分开。

## Bill truth

- billing period 是真实日期；
- Actual / Estimated / Mixed 明确；
- real zero 与 missing 分开；
- corrected/superseded revision 可追溯；
- supply/delivery 不误判 duplicate。

## Cost

- total / energy / demand / fixed / tax-fee / credits 可解释；
- unknown line item 不被硬归类；
- currency 可见；
- projected / billed / paid 分开。

## Tariff

- rate name/version/effective period 可查；
- current rate 不覆盖 historical bill context；
- TOU/season/demand semantics 可查；
- no generic-rate fallback。

## Demand

- Site Peak 与 Billing Demand 分开；
- demand interval/window 可查；
- ratchet/look-back由 owner提供；
- coincident/system peak不由本地 peak 猜。

## Reconciliation

- bill usage vs meter total 可比较；
- coverage / variance / method 可见；
- tolerance来自 owner；
- variance 不自动叫 utility error；
- unavailable 不显示 matched。

## Allocation

- measured / rule-based / estimated allocation 分开；
- remainder visible；
- allocation不冒充物理计量。

## Boundaries

- Energy → 14；
- Demand/Flexibility → 15；
- Opportunity → 21；
- Data Quality → 31；
- Verified Savings → 24 M&V。

## Responsive / Accessibility

- 1440–1720px 是 coherent billing workspace；
- around 768px 核心任务完整；
- no color-only actual/estimated/reconciliation；
- no hover-only critical values；
- no page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无 old Billing/Cost Dashboard compatibility adapter；
- 无 frontend-generated bill/demand/tariff/reconciliation truth；
- 无 N+1 bill-detail fan-out；
- 无 missing→0；
- review scenario 无 runtime/network error。

---

# 70. Explicit Non-Goals

本页不是：

- ERP / General Ledger；
- payment execution portal；
- bank reconciliation；
- utility tariff authoring studio；
- retail energy procurement platform；
- M&V savings page；
- generic financial BI；
- arbitrary invoice OCR tool；
- tax engine；
- DR settlement engine。

---

# 71. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Billing capability gating 已明确；
- Bills / Cost Analysis peer views 已明确；
- Bill / Meter / Tariff / Payment 已分离；
- Actual / Estimated / Mixed 已明确；
- Billing Period / Revision / Rebill 已明确；
- Cost Component / Tariff Version / TOU 已明确；
- Site Peak / Billing Demand / Demand Charge 已分离；
- Ratchet / Coincident Peak boundary 已明确；
- Reconciliation / tolerance / correction workflow 已明确；
- Missing / Duplicate / Overlap / Gap 已明确；
- Projected / Billed / Paid 已分离；
- Cost Allocation / Remainder 已明确；
- 中文优先产品语言已落实；
- no defensive fallback contract 已接受；
- old Billing / Cost Dashboard 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
