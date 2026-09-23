# 32 计量与语义模型 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `32 计量与语义模型`  
> **Route intent：** `/sites/:siteId/model`、`/sites/:siteId/model/:entityId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`ASHRAE 223`、`Brick`、`Project Haystack`、`RDF`、`SHACL`、`SPARQL` 等标准术语保留为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有 Registry 页面、旧设备配置页、旧 Ant/ProComponents 页面、历史 meter tree 或点位管理页面。现有代码只能在实施阶段作为真实 identity / relationship / meter / point / unit / version / permission contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **建立并治理智慧能源系统中“对象是谁、对象之间是什么关系、计量边界是什么、每个点表达什么事实、这些关系在什么时间有效”的权威模型。**

32 是 **Metering Governance + Semantic Model Workspace**，不是：

- 设备中心的另一种列表；
- BACnet Object Browser；
- 点位命名清洗工具；
- 任意 Tag 编辑器；
- 通用 CMDB；
- 数据质量问题列表；
- 拓扑绘图工具；
- “根据设备名自动猜关系”的配置页；
- 全系统万能 Registry CRUD。

用户离开本页前应该能回答：

1. 当前 Site / Building / Space / System 的正式层级是什么；
2. 某个 Asset、Device、Point 分别是谁；
3. Point 描述谁、由谁托管、属于什么 quantity / unit；
4. Equipment / System / Space 之间有哪些权威关系；
5. HVAC / 水 / 电 / 气等真实 flow topology 如何表达；
6. Meter hierarchy 与 allocation hierarchy 是什么；
7. Virtual Meter / Calculated Point 的公式、输入和版本是什么；
8. 某个 relationship / mapping 在什么 Effective Period 有效；
9. 某次 model change 会影响哪些 Energy / EnPI / M&V / Report / Review 结果；
10. 当前模型是否存在 unresolved validation / data-quality issue。

---

# 2. 主要用户

## Primary

- **数据 / 模型治理人员：**维护对象身份、关系、版本与有效期；
- **能源工程师：**维护计量层级、Measurement Boundary、Virtual Meter；
- **BAS / Controls Engineer：**维护设备、Point、Controller、System relationship；
- **平台工程师：**维护 Source Binding、integration identity 和 semantic contract。

## Secondary

- Energy Manager：消费正式 meter / model boundary；
- M&V Engineer：消费 measurement boundary 与 meter lineage；
- FDD / Analytics Engineer：消费 equipment-point-system semantics；
- Facility Manager：复核设备 / 系统 / 空间关系；
- Auditor：追溯 model revision / change / approval。

---

# 3. 外部最佳实践依据

## 3.1 ASHRAE Standard 223P / semantic interoperability

ASHRAE 223P 旨在为建筑系统建立机器可读、可互操作的语义表示，使 analytics、automation、control、commissioning 和 energy optimization 能够基于统一对象和关系模型工作。

本页采用：

- 对象必须有明确 identity；
- relationship 必须显式建模；
- 设备、系统、空间、点位、连接关系不能只依赖名称推断；
- topology 与 geometry 分离；
- effective model change 必须可追溯。

## 3.2 DOE Building Semantic Modeling / interoperability

DOE 的建筑语义建模方向强调：成熟模型需要显式表达 components、properties、relationships、systems、spaces 和 data semantics，避免 naming conventions 成为事实来源。

本页采用：

```text
Naming Convention
≠ Semantic Model
```

## 3.3 Brick Schema

Brick 明确区分 `hasPoint`、`hosts`、`controls`、`feeds`、`hasPart`、`hasLocation` 等关系，并支持设备、系统、空间、点位及连接语义。

本页采用：

```text
hasPoint ≠ hosts ≠ controls ≠ feeds ≠ hasPart ≠ hasLocation
```

同一个 Point 可以由 Controller 托管，同时描述另一个 Equipment；不能用一个通用 `parentId` 表达全部关系。

## 3.4 Project Haystack

Haystack 使用独立 entity identity、tags 和 refs 来表达对象及其关系，并要求应用不要把 ID 自身解释成业务含义。

本页采用：

```text
Display Name ≠ Canonical ID
Source ID ≠ Canonical ID
Tag ≠ Relationship automatically
```

## 3.5 FEMP Metering Best Practices

FEMP Metering Best Practices 强调计量应根据 metering objective、measurement boundary、粒度和分析需求设计；whole-building、system、subsystem、circuit、end-use 等不同粒度服务不同业务目的。

本页采用：

- Meter hierarchy 不能靠名称或能量大小推断；
- Meter placement / hierarchy 与 measurement objective 关联；
- Virtual meter / allocation 必须保留 lineage；
- Meter 本身不等于 Savings / Energy Performance result。

## 3.6 BACnet Object / Unit semantics

BACnet 通过 Object / Property / Units 等机制明确现场对象和值语义。协议对象 identity、Point business identity、Equipment identity 不应被混成一个实体。

---

# 4. 产品语言契约

主界面中文优先：

```text
计量与语义模型
对象
关系
系统层级
空间层级
计量层级
测量边界
实体
设备
控制器
点位
实体关系
能源介质
物理计量表
虚拟计量表
计算点
量纲
单位
数据来源
有效期
模型版本
变更影响
```

可保留行业标准术语：`ASHRAE 223`、`Brick`、`Haystack`、`RDF`、`SHACL`、`SPARQL`、`BACnet`。

默认业务界面不直接暴露 ontology triple editor。

---

# 5. Mandatory Semantic Separation

```text
Name ≠ Identity
Display Name ≠ Canonical ID
Source ID ≠ Canonical ID
Naming Convention ≠ Semantic Model
Tag ≠ Relationship

Portfolio ≠ Site ≠ Building ≠ Space
System ≠ Equipment ≠ Asset ≠ Device ≠ Controller ≠ Point

Point ≠ Device
Device ≠ Asset automatically
Controller hosts Point ≠ Point describes Controller

hasPoint ≠ hosts ≠ controls ≠ feeds ≠ hasPart ≠ hasLocation

Topology ≠ Geometry
Connection ≠ Containment
Physical Flow ≠ Organizational Hierarchy

Physical Meter ≠ Virtual Meter
Calculated Point ≠ Measured Point
Meter Hierarchy ≠ Allocation Hierarchy
Measurement Boundary ≠ Meter Tree

Measured ≠ Derived ≠ Allocated ≠ Estimated
Unit ≠ Display Decoration
Quantity ≠ Unit

Current Mapping ≠ Historical Mapping
Model Version ≠ Effective Period
Model Published ≠ Downstream Recomputed
Model Corrected ≠ Historical Result Overwritten
```

---

# 6. Identity Contract

每个治理对象必须有稳定 Canonical Identity。

至少记录：

```text
Canonical ID
Entity Type
Display Name
Source Bindings
External IDs
Lifecycle State
Effective Period
Model Revision
Owner
```

禁止把：

```text
AHU-01
CH-1
MTR_003
```

这种名称直接当作系统业务 identity。

名称可以修改，Identity 不应该因为 rename 改变。

---

# 7. Entity Type Contract

核心 entity types 至少包括：

```text
Portfolio
Site
Building
Floor / Level
Space / Zone
System
Equipment / Asset
Device
Controller
Point
Physical Meter
Virtual Meter
Calculated Point
Connection
Connection Point
Energy Source / Carrier
Measurement Boundary
```

不要求所有客户都有全部类型；owner contract 决定可用 capability。

---

# 8. Portfolio / Site / Building / Space Contract

组织 / 地理 / 空间层级必须显式建模。

示例：

```text
Portfolio
└─ Site
   └─ Building
      └─ Floor
         └─ Space / Zone
```

这表达 **location / containment**，不是 HVAC feeds topology。

禁止：

```text
Building parent = Chiller Plant
```

通过通用 parent 字段同时表达空间与系统关系。

---

# 9. System Hierarchy Contract

System 表达业务/工程系统，例如：

```text
Central Chilled Water System
Air Handling System
Condenser Water System
Electrical Distribution System
Domestic Water System
```

System relationship 与 Equipment containment 分开。

系统可能包含多个 Equipment，但 Equipment 所在空间与 System membership 是不同关系。

---

# 10. Asset / Device / Controller Contract

## Asset / Equipment

表示业务 / 物理资产，例如 Chiller、Pump、AHU、Cooling Tower。

## Device

表示通信 / integration device 或 physical device identity。

## Controller

表示控制器 / automation host。

三者不能自动合并。

例如：

```text
Chiller CH-01
≠ BACnet Device 11001
≠ Plant Controller PLC-01
```

它们可以有关系，但不是同一个 identity。

---

# 11. Point Contract

Point 必须有：

```text
Canonical Point ID
Display Name
Quantity Kind
Unit
Data Nature
Source Binding
Describes Entity
Hosted By
Writable / Command Metadata if authoritative
Effective Period
```

关键关系必须分开：

```text
Point describes CH-01
Point hosted by PLC-01
```

不能因为 point 来自 PLC-01 就显示它“属于 PLC-01 设备业务对象”。

---

# 12. Point Data Nature Contract

至少区分：

```text
Measured
Calculated
Derived
Allocated
Estimated
Command / Setpoint
State
Event
```

例如：

```text
CHWS Temperature
Measured

Plant ΔT
Calculated

Allocated Tenant Energy
Allocated
```

前端不能把它们都画成“传感器点”。

---

# 13. Quantity / Unit Contract

Point 必须尽可能绑定正式 Quantity Kind 和 Unit。

例如：

```text
Supply Air Temperature
Quantity = Temperature
Unit = °C

Supply / Return ΔT
Quantity = Temperature Difference
Unit = Δ°C
```

禁止：

```text
unit missing
→ 根据数值范围猜 °C
```

单位变化必须进入 model revision / binding change，而不是仅仅改变 formatter。

---

# 14. Range / Constraint Contract

如果 source owner 有 authoritative engineering range，可记录：

```text
Engineering Min
Engineering Max
Normal Operating Range
Alarm Limits reference
Physical Capability Range
```

这些范围的 owner 和用途必须明确。

Normal Range 不等于 Alarm Limit。

---

# 15. Relationship Contract

Relationship 是一等对象或一等事实，至少包含：

```text
Relationship Type
Subject
Object
Source / Authority
Effective From
Effective To
Revision
Confidence only if owner-defined
Review State
```

禁止只存：

```text
parentId
```

来表达所有关系。

---

# 16. Relationship Types

最少支持明确语义：

```text
hasPart
isPartOf
hasLocation
locatedIn
hasPoint
isPointOf
hosts
hostedBy
controls
controlledBy
feeds
isFedBy
connectedTo
serves
isServedBy
measures
isMeasuredBy
```

项目实际 relation vocabulary 由 semantic owner 定义。

---

# 17. Tag Contract

Tag 用于 classification / annotation，不自动代表 relationship。

例如：

```text
chiller
water
supply
sensor
```

可以帮助查询 / 分类。

但：

```text
chiller + supply + temp
```

不能自动产生：

```text
Point hasPoint-of CH-01
```

关系必须来自 authority owner。

---

# 18. Naming Convention Boundary

命名规范可以提高可读性和 integration consistency，但不是 semantic truth。

禁止：

```text
CH-01-PWR
→ 自动断言 measures CH-01 electrical power
```

可以提出候选 mapping，但必须进入 Review / Approval，不能直接成为 authoritative model。

---

# 19. Topology Contract

Topology 表达真实连接 / flow / service relationship。

例如 chilled-water topology：

```text
Chiller CH-01
→ CHWS Header
→ Distribution Pump
→ AHU Coil
→ CHWR Header
→ Chiller CH-01
```

需要明确：

```text
Connection
Connection Point
Medium
Direction if authoritative
Source
Effective Period
```

没有权威关系时显示“暂无已验证拓扑”。

---

# 20. Topology ≠ Geometry

```text
Topology
= 谁与谁连接、什么介质如何流动

Geometry
= 实际空间几何 / 坐标 / 平面位置
```

没有 geometry 不能假装 CAD；没有 topology 不能根据屏幕布局暗示连接关系。

---

# 21. Medium / Carrier Contract

Connection / Meter / Boundary 可以绑定：

```text
Electricity
Natural Gas
Steam
Chilled Water
Hot Water
Condenser Water
Domestic Water
Compressed Air
Fuel
Other owner-defined carrier
```

Carrier 不允许只通过 unit 推断。

---

# 22. Physical Meter Contract

Physical Meter 至少记录：

```text
Meter ID
Meter Type
Energy / Resource Carrier
Measurement Quantity
Unit
Source Binding
Measurement Boundary
Parent Meter if authoritative
Location
Effective Period
Calibration / Replacement references
```

Meter identity 与 meter point identity 分开。

---

# 23. Meter Hierarchy Contract

Meter Hierarchy 表达 authoritative measurement aggregation / submeter relationship。

例如：

```text
Main Electricity Meter
├─ Chiller Plant Meter
│  ├─ Chillers
│  └─ Pumps
└─ Office Building Meter
```

但这必须来自 meter governance owner。

禁止：

```text
child energy < parent energy
→ infer hierarchy
```

也禁止通过名称前缀自动组树。

---

# 24. Meter Hierarchy ≠ Allocation Hierarchy

Physical Meter hierarchy 是 measurement relation。

Allocation hierarchy 是业务分摊 relation。

例如：

```text
Main Meter 1000 kWh
↓ allocation rule
Tenant A 450 kWh
Tenant B 550 kWh
```

Tenant allocation 不是 physical submeter reading，除非存在真实 submeter。

---

# 25. Measurement Boundary Contract

Measurement Boundary 是正式业务对象，至少定义：

```text
Boundary ID
Purpose
Included Systems
Included Assets
Included Meters
Excluded Loads
Energy / Resource Carriers
Interactive Effects Treatment
Effective Period
Owner
Revision
```

24 M&V、14 Energy、16 Efficiency 等页面消费它，但不自己重新定义。

---

# 26. Measurement Boundary ≠ Meter Tree

同一个 meter hierarchy 可以支持不同 analytical boundaries。

例如：

```text
Plant Electrical Boundary
Central Plant M&V Boundary
Chiller-only Efficiency Boundary
```

可能使用不同 meter subset。

所以不能：

```text
meter descendants
= measurement boundary automatically
```

---

# 27. Virtual Meter Contract

Virtual Meter 是明确的派生计量对象，不是物理 meter。

必须至少记录：

```text
Virtual Meter ID
Purpose
Formula Revision
Inputs
Units
Aggregation / Time Basis
Missing-data Policy
Quality Propagation Rule
Owner
Effective Period
```

例如：

```text
Central Plant Total Electricity
= CH-01 + CH-02 + CHW Pumps + CW Pumps + Towers
```

---

# 28. Virtual Meter ≠ Physical Meter

用户界面必须有明显 Data Nature：

```text
物理计量
虚拟计量
```

禁止用相同 icon / label 让用户误以为 virtual meter 是现场设备。

---

# 29. Calculated Point Contract

Calculated Point 必须记录：

```text
Formula
Inputs
Quantity Kind
Unit
Version
Aggregation / Alignment Rule
Quality Propagation
Owner
Effective Period
```

例如：

```text
Plant ΔT
= CHWR Temperature − CHWS Temperature
```

Calculated Point 不是 Measured Point。

---

# 30. Formula Contract

Formula 必须是受治理的定义，不是散落在 React component 内的临时计算。

至少记录：

```text
Formula ID
Version
Expression / executable reference
Input bindings
Unit contract
Time alignment
Missing-data handling
Quality propagation
Test / validation status
Owner
```

前端只展示，不成为 authoritative calculation engine。

---

# 31. Time Alignment Contract

多输入计算必须明确：

```text
Sampling interval
Aggregation window
Timezone
Alignment method
Event time basis
Late data handling
```

不能：

```text
两个 series 看起来时间接近
→ 直接相减 / 相乘
```

这与 31 Data Quality 的 Synchronization contract 对齐。

---

# 32. Missing-data Contract for Derived Objects

Virtual Meter / Calculated Point 的 missing-data 行为必须由 owner 定义。

禁止通用规则：

```text
missing input → 0
missing input → last known
missing input → previous period
```

可能结果包括：

```text
Missing
Partial
Estimated
Backfilled
Inconclusive
```

由 calculation owner 决定。

---

# 33. Quality Propagation Contract

Derived object 的质量不能自动等于最差输入，也不能自动等于全部输入的平均分。

Quality propagation 必须有 owner-defined rule / result。

前端只呈现 authoritative quality state。

---

# 34. Source Binding Contract

Canonical Entity 与 external source object 之间的 binding 必须明确：

```text
Canonical Entity
Source System
Source Object Type
Source Object ID
Binding Type
Effective Period
Revision
```

例如：

```text
Point P-CHWS-TEMP
↔ BACnet Device 1001 / AI:3
```

Source Binding 变化不应创建新的 Canonical Entity，除非 identity 确实变化。

---

# 35. Source Binding Change Contract

Controller replacement、BACnet object renumbering、sensor replacement 等可能导致 binding change。

历史必须保留：

```text
2026-01 → 2026-08
BACnet 1001 / AI:3

2026-09 →
BACnet 2001 / AI:7
```

不能覆盖旧 binding 后让历史 telemetry 看起来一直来自新对象。

---

# 36. Effective-dated Model Contract

Relationship / Binding / Formula / Meter Parent / Classification 都必须支持 Effective Period。

例如：

```text
M-02

2026-01 → 2026-08
Parent = Main-01

2026-09 →
Parent = Plant-01
```

Current View 只是历史中的当前 slice。

---

# 37. Model Revision Contract

模型变更必须形成 revision / change set。

至少记录：

```text
Revision ID
Change Set
Reason
Author
Reviewer / Approver
Created At
Effective At
Validation Result
Downstream Impact
```

不能直接修改数据库当前行而没有历史。

---

# 38. Model Version ≠ Effective Period

```text
Revision created 2026-09-14
Effective from 2026-10-01
```

完全合法。

也可能需要 backdated correction，但必须显式进入 change review / impact assessment。

---

# 39. Change Categories

至少区分：

```text
Rename / Display-only
Identity correction
Relationship change
Meter hierarchy change
Source binding change
Unit / Quantity change
Formula change
Boundary change
Topology change
Classification / Tag change
```

不同 Change Category 的 downstream impact 不同。

---

# 40. Rename ≠ Identity Change

修改：

```text
冷机 1#
→ 1 号冷水机组
```

通常是 Rename。

不能因此导致新 Asset ID、历史 telemetry 断裂、work order history 丢失。

---

# 41. Relationship Change Impact

Relationship change 可能影响：

```text
Topology
Device Center
System Operations
Energy aggregation
Efficiency calculation
EnPI / EnB
M&V boundary
Report
Management Review evidence
```

必须由 owner impact service / governance owner 给出正式影响。

前端不自行推断“所有结果都受影响”。

---

# 42. Meter Hierarchy Change Impact

例如：

```text
M-02 parent corrected
Main-01 → Plant-01
```

可能影响：

```text
Energy rollup
Allocation
EnPI
M&V
Carbon
Billing reconciliation
Formal reports
```

正确流程：

```text
Model Revision Published
↓
Impact Assessment
↓
Affected downstream result identified
↓
Owner recomputation / reissue decision
↓
New result revision
```

不是：

```text
model fixed
→ old report silently changes
```

---

# 43. Model Publish Contract

建议生命周期：

```text
Draft
In Review
Approved
Published
Superseded
Retired
```

Published 只表示模型 revision 成为 authoritative model。

```text
Published
≠ Downstream Recomputed
```

---

# 44. Validation Contract

Model Validation 至少可以包括：

```text
Identity uniqueness
Required relationships
Relationship cardinality
Unit compatibility
Quantity / Unit consistency
Dangling references
Cycle checks where forbidden
Meter hierarchy validity
Formula dependency validity
Effective-date overlap
Source binding validity
```

Validation rule 来自 semantic / metering owner，不由 UI 临时硬编码业务事实。

---

# 45. Cardinality / Constraint Contract

某些关系可以有明确 cardinality，例如：

```text
Point describes exactly one governed quantity owner object
Physical Meter has one active source binding at a time
```

但不同 domain 规则不同。

不能把所有 relationship 强行套成 tree。

---

# 46. Graph ≠ Tree

Semantic Model 本质可能是 graph。

例如 AHU 同时：

```text
locatedIn Building-A
partOf AHU System
feeds Zone-101
hasPoint SAT
controlledBy PLC-01
```

因此不能用单一 parent hierarchy 表达完整模型。

---

# 47. Relationship Explorer Contract

UI 可以提供 graph / topology explorer，但必须：

- 只显示 authoritative relationships；
- relation type 可见；
- direction 可见；
- source / revision 可追溯；
- 不通过视觉 proximity 暗示不存在的关系；
- 大 graph 支持 scope / relation filter。

---

# 48. Meter Hierarchy Workspace Contract

Meter Workspace 默认关注：

```text
Meter identity
Carrier
Quantity / Unit
Parent / Children
Boundary membership
Physical / Virtual
Source binding
Coverage / Quality link to 31
Effective period
```

不是树形 CRUD 组件本身。

---

# 49. Measurement Boundary Workspace Contract

用户必须能查看：

```text
Boundary purpose
Included / Excluded
Meters
Systems
Assets
Carrier
Revision
Effective period
Consumers
```

Consumers 可以包括：

```text
14 Energy
16 Efficiency
17 Energy Review
24 M&V
29 Report
```

---

# 50. Semantic Search Contract

搜索可以支持：

```text
name
ID
external source ID
entity type
system
space
relationship
quantity
unit
tag
meter carrier
```

搜索结果不能因为名称相似就自动 merge identity。

---

# 51. Merge / Duplicate Contract

如果发现可能重复对象：

```text
Potential Duplicate
```

只能进入 identity review。

禁止：

```text
same display name
→ merge automatically
```

Merge 必须保留 source identities、historical references、decision / audit。

---

# 52. Split Contract

一个错误合并的 entity 可能需要 split。

Split 必须明确：

```text
new identities
historical relationship allocation
source binding reassignment
telemetry/history impact
work/order/alert references
```

不能只新建两个 row。

---

# 53. 31 Data Quality Handoff

31 负责发现：

```text
wrong meter parent
wrong unit
wrong semantic mapping
missing relationship
invalid source binding
```

32 负责正式修正 model / relationship / binding。

修正后回传：

```text
Model Revision
Effective Period
Affected Objects
Impact Assessment Reference
```

---

# 54. Downstream Recomputation Contract

32 只发出 / 展示 downstream impact 和 recomputation requirement。

真正重算由对应 owner 完成。

例如：

```text
32 Meter hierarchy corrected
↓
17 EnPI recomputation owner
24 M&V recomputation owner
29 Report correction owner
```

不能由 32 前端统一重算所有业务结果。

---

# 55. Historical Integrity Contract

任何 model correction 都不能直接改写：

```text
old M&V result
published report
management review record
approved plan
historical work order
```

需要对应 domain 的 revision / correction / reissue workflow。

---

# 56. Permission / Governance Contract

典型权限：

```text
Read Model
Create Draft Revision
Edit Entity
Edit Relationship
Edit Meter Hierarchy
Edit Formula
Edit Source Binding
Validate Revision
Review Revision
Approve Revision
Publish Revision
Read Audit
```

高影响变更应有明确 reviewer / approver。

---

# 57. Audit Contract

至少审计：

```text
who
when
what changed
before / after
reason
effective date
validation result
review / approval
impact assessment
```

不能只留 `updated_at`。

---

# 58. Data Authority Contract

| Fact | Owner |
|---|---|
| Canonical identity | semantic / registry owner |
| Relationship | semantic model owner |
| Source binding | integration owner |
| Physical meter | metering owner |
| Meter hierarchy | metering owner |
| Measurement boundary | energy / M&V governance owner |
| Virtual meter | analytics / metering owner |
| Formula | calculation owner |
| Data quality | 31 quality owner |
| Telemetry value | historian / telemetry owner |
| Business result | downstream domain owner |

Frontend 只呈现 authoritative model，不重新发明关系。

---

# 59. Query / Read Model Contract

默认列表 / explorer 使用面向 UI 的 authoritative read model。

不允许：

```text
load 1000 entities
→ frontend join all relationships
→ frontend infer topology
```

Graph / hierarchy expansion 应按 scope server-side 查询。

---

# 60. Realtime Boundary

32 是治理页面，不是 realtime HMI。

模型变更采用 Query + explicit invalidation / revision refresh。

Telemetry realtime 不应该驱动 semantic relationship 自动变化。

---

# 61. AI Assistance Boundary

AI 可以：

- 根据 source metadata 建议 candidate mapping；
- 解释 relationship；
- 检查可能缺失的语义；
- 草拟 change rationale；
- 总结 downstream impact；
- 生成 review checklist。

AI 不能：

- 根据名称直接建立 authoritative relationship；
- 自动 merge identity；
- 自动修改 meter hierarchy；
- 自动批准 model revision；
- 自动发布；
- 自动改写历史 result。

Candidate 必须明确标识为 candidate。

---

# 62. No Defensive Programming / No Compatibility Design

明确禁止：

```text
class missing
→ parse name

system missing
→ infer from prefix

point owner missing
→ use host controller

unit missing
→ guess from value range

quantity missing
→ guess from unit

relationship missing
→ infer from correlation

same name
→ same identity

same source ID across systems
→ merge

meter parent missing
→ infer from arithmetic

child energy < parent energy
→ infer submeter

virtual meter missing input
→ 0

formula input missing
→ last value

carrier missing
→ infer from unit

source binding changed
→ overwrite historical mapping

relationship changed
→ overwrite history

model revision
→ rewrite historical results

published model
→ assume downstream recomputed

topology unavailable
→ draw inferred topology

geometry unavailable
→ invent layout as physical map

multiple semantic APIs
→ first success wins

legacy name parser fallback
legacy meter-tree compatibility adapter
legacy Registry relation fallback
```

正式原则：

> **一个业务对象必须有稳定 Identity，一个关系必须有明确 Type / Source / Effective Period，一个计量事实必须有明确 Boundary / Lineage / Data Nature。Name 不是 Identity，Tag 不是 Relationship，Virtual 不是 Physical，Calculated 不是 Measured，Current Mapping 不改写历史，Unknown 保持 Unknown。**

---

# 63. Information Architecture

```text
Context Header
↓
[对象] [关系] [计量] [变更]
↓
Entity Ledger
                       → Entity Inspector
↓
Relationship Explorer
↓
Meter Hierarchy
↓
Measurement Boundary
↓
Point / Quantity / Unit / Formula
↓
Source Binding
↓
Version / Effective Date
↓
Validation
↓
Change Review
↓
Downstream Impact
↓
Revision History / Audit
```

不是 ontology authoring IDE。

---

# 64. Route / URL State Contract

```text
/sites/:siteId/model
/sites/:siteId/model/:entityId
```

Search Params 可包括：

```text
view
entityType
relation
system
space
carrier
meterType
revision
asOf
validation
impact
q
```

`asOf` 用于 effective-dated historical view。

---

# 65. Ledger Contract

对象 Ledger 默认列：

```text
名称
类型
所属系统 / 空间摘要
数据 / 计量性质
关键关系
来源
有效期
模型版本
验证状态
```

Meter Ledger 可以专门显示：

```text
计量表
物理 / 虚拟
介质
Quantity / Unit
Parent
Boundary
Source
Effective Period
```

---

# 66. Inspector Contract

快速 Inspector 只显示：

```text
Identity
Entity Type
Display Name
Current Relationships
System / Location
Source Binding
Quantity / Unit
Meter / Formula Nature
Effective Period
Model Revision
Validation / Data Quality references
```

复杂编辑进入 durable detail route。

---

# 67. Change Review Contract

提交 revision review 时必须看：

```text
Change Summary
Changed Entities
Changed Relationships
Changed Meters / Formulas
Effective Date
Validation Result
Downstream Impact
Open Issues
Reviewer / Approver
```

不能只显示 raw JSON diff。

---

# 68. Impact Summary Contract

Impact 可以按 owner-provided domain 展示：

```text
Operations
Energy
Efficiency
EnPI / EnB
M&V
Carbon
Billing
Report
Management Review
Control / Strategy
```

如果 impact owner 无法确定，则显示：

```text
Impact Assessment Required
```

不能显示 No Impact。

---

# 69. Empty / Unknown / Error States

## Empty

合法表示尚无对应模型对象。

## Unknown

例如 relationship owner unavailable。

## Error

API / validation service error 明确展示错误。

禁止：

```text
error → empty
unknown → no relationship
```

---

# 70. Accessibility / Responsive Contract

- relationship type 不只靠线条颜色；
- graph 有 textual relationship list alternative；
- hierarchy 支持键盘展开；
- physical / virtual / calculated 不只靠 icon；
- unit / data nature 有文本；
- 768px 下仍能完成 entity lookup、relationship review、meter hierarchy 和 impact inspection。

---

# 71. Wireframe Intent

```text
┌────────────────────────────────────────────────────────────────────────────┐
│ 计量与语义模型 · 中央园区                    Model rev12 · 当前有效      │
├────────────────────────────────────────────────────────────────────────────┤
│ [对象] [关系] [计量] [变更]                                               │
├────────────────────────────────────────────────────────────────────────────┤
│ 对象                     类型        系统 / 空间        来源       状态     │
│ 1号冷水机组              Equipment   中央冷站          Registry   已验证   │
│ 冷机1号控制器            Controller  中央冷站          BACnet     已验证   │
│ 冷冻水供水温度           Point       CH-01             BACnet     已验证   │
│ 冷站总电耗               VirtualMeter 中央冷站         Analytics  已验证   │
├───────────────────────────────────────────────┬────────────────────────────┤
│ Relationship Explorer                         │ 快速检查                   │
│                                               │ Identity CH-01             │
│ Central Plant                                 │ Type Equipment             │
│   └─ hasPart → CH-01                          │ Location Plant Room        │
│ CH-01                                         │ hasPoint 18                │
│   ├─ hasPoint → CHWS Temp                     │ hosted points 0            │
│   ├─ controlledBy → PLC-01                    │ Source Registry            │
│   └─ feeds → CHWS Header                      │ Effective 2026-01 →        │
│                                               │ Model rev12                │
├────────────────────────────────────────────────────────────────────────────┤
│ Meter Hierarchy                                                            │
│ Main Electricity                                                           │
│ └─ Central Plant                                                           │
│    ├─ Chillers                                                              │
│    ├─ Pumps                                                                 │
│    └─ Towers                                                                │
│                                                                            │
│ Virtual Meter: 冷站总电耗 · Formula v4 · 7 inputs · 当前完整              │
├────────────────────────────────────────────────────────────────────────────┤
│ 待发布变更 rev13                                                           │
│ M-02 Parent: Main-01 → Plant-01                                            │
│ Effective: 2026-10-01                                                      │
│ Validation: Passed                                                         │
│ Impact: Energy / EnPI / M&V / Reports                                      │
│ [查看变更] [提交审批]                                                      │
└────────────────────────────────────────────────────────────────────────────┘
```

---

# 72. Browser Acceptance Criteria

## Identity

- Display Name 与 Canonical ID 分离；
- Rename 不创建新 identity；
- source ID 不冒充 canonical identity；
- potential duplicate 不自动 merge。

## Relationships

- relationship type 明确；
- hasPoint / hosts / controls / feeds / location 等不混合；
- relation source / revision / effective period 可见；
- missing relationship 不由前端推断。

## Point / Unit

- Point data nature 可见；
- measured / calculated / allocated 分开；
- Quantity / Unit 明确；
- missing unit 不猜；
- host 与 describes 分开。

## Metering

- physical / virtual 分开；
- meter hierarchy 来自 authoritative owner；
- allocation 不冒充 physical submetering；
- virtual meter formula / inputs / version 可追溯；
- missing input 不补 0。

## Boundary

- measurement boundary 有 explicit included / excluded scope；
- meter tree 不自动等于 boundary；
- boundary revision / effective period 可追溯。

## Version / History

- current mapping 与 historical mapping 可查看；
- relationship / binding / meter parent 变更 effective-dated；
- published revision 不覆盖旧 revision；
- historical result 不因 model change 静默改写。

## Impact

- high-impact model change 有 downstream impact；
- model publish 不显示 downstream already recomputed；
- recomputation 由 downstream owner 完成。

## Implementation Integrity

- 无前端 name-based semantic inference；
- 无前端 correlation-based topology inference；
- 无 generic `parentId` 代替所有关系；
- 无 legacy meter-tree fallback；
- 无 old Registry compatibility adapter；
- review scenario 无 runtime/network error。

---

# 73. Explicit Non-goals

32 不是：

- generic ontology editor；
- RDF/SPARQL IDE；
- BACnet Object Browser；
- Device Center replacement；
- Data Quality issue center；
- CAD/BIM editor；
- universal Registry CRUD；
- frontend inference engine。

---

# 74. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Identity / Name 分离；
- Asset / Device / Controller / Point 分离；
- Relationship types 明确；
- Tag / Relationship 分离；
- Topology / Geometry 分离；
- Meter Hierarchy / Allocation 分离；
- Physical / Virtual Meter 分离；
- Measured / Calculated / Allocated 分离；
- Quantity / Unit 契约明确；
- Formula / Input / Quality / Missing contract 明确；
- Measurement Boundary 明确；
- Source Binding 明确；
- Model Revision / Effective Date 明确；
- Historical Mapping 保留；
- Change Validation / Review 明确；
- Downstream Impact / Recomputation 明确；
- No Defensive Programming 规则明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**