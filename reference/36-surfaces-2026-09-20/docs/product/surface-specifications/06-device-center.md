# 06 设备中心 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `06 设备中心`  
> **Route intent：** `/sites/:siteId/devices`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 Device Center、旧 Assets、旧卡片视图、旧设备列表、旧 Drawer、旧设计稿或旧 Ant/ProComponents 页面。当前代码仅可在实施阶段作为真实 Registry owner、Telemetry owner、Alarm/Diagnosis/Work owner、capability、permission 与 route contract 的候选证据来源。

---

# 1. Primary Job

设备中心的唯一核心任务是：

> **让用户在一个可信、可扫描、可筛选的设备资产清单中快速找到真实设备，理解设备的身份、所属系统和几个互相独立的当前状态，并在需要持续调查时进入设备详情或对应专业页面。**

设备中心是 **trusted asset inventory + operational scan workspace**，不是：

- 设备卡片墙；
- CMMS；
- 资产采购/折旧系统；
- 全点位浏览器；
- 网络扫描器；
- 拓扑页；
- 远程控制台；
- 批量控制页面；
- 告警中心；
- FDD 页面；
- 通用“设备健康度”Dashboard。

用户离开本页前应该已经知道：

1. 当前站点有哪些真实设备；
2. 某设备是什么、在哪里、属于哪个系统；
3. 当前是 Running / Stopped / Standby / Unknown 中的哪种运行事实（如果 owner 提供）；
4. 当前是否在线、数据是否 fresh、质量是否可信；
5. 是否存在当前 Alarm / Finding / Work / Override / Out-of-Service 等独立事项；
6. 下一步应该进入 Device Detail、Trend、System Operations、Alarm、Diagnosis、Work 或 Data Quality 中的哪一个负责页面。

---

# 2. 主要用户

## Primary

### HVAC 值班 / 运行工程师

需要快速从大量设备中找到目标设备、扫描当前运行状态和数据可信度，并继续进入设备详情或趋势。

### 维修 / 维护负责人

需要确认设备身份、位置、当前可用状态以及是否存在相关工单，再进入 Work Order 或 Device Detail。

### 能源 / 诊断工程师

需要从效率、告警、诊断结果反向找到真实设备，并继续查看设备事实。

## Secondary

- 控制工程师：确认设备身份、当前 authority / override 等事实，再进入 Control；
- 数据工程师：筛选 stale / bad-quality / point-coverage 问题，再进入 Data Quality；
- 站点负责人：查看设备群的当前 operational population，但不在此完成管理报表。

## 不作为主要目标用户

- 企业管理层：应使用 Portfolio / Site Overview；
- 采购 / 财务资产人员：本产品不替代 ERP / EAM；
- 网络安全资产管理员：本页可以引用 OT inventory 事实，但不替代安全资产管理平台。

---

# 3. 外部最佳实践依据

## 3.1 ISO 55000 / ISO 55001 — Asset identity 与 lifecycle 必须稳定，不等于实时运行状态

ISO 55000:2024 提供资产管理的原则、术语和生命周期框架；ISO 55001:2024 要求组织系统地管理资产，并在生命周期中平衡 performance、risk 与 expenditure。

来源：

- https://committee.iso.org/sites/tc251/home/projects/published/iso-55000.html
- https://www.iso.org/standard/83054.html

**本页采用：**

- 设备具有稳定 identity 和 lifecycle；
- Registry identity 不随 telemetry 临时断开而消失；
- `Decommissioned`、`Out of Service`、`Offline`、`Stopped` 是不同语义；
- 设备中心支持资产清单和持续调查，但不把完整资产生命周期管理、采购、折旧、备件全部塞入本页。

## 3.2 NIST SP 800-82 Rev. 3 — OT inventory 是基础能力，必须能够准确识别真实设备

NIST SP 800-82 Rev. 3 的 Asset Management 指导强调 OT 环境应维护准确 inventory，并建议记录 unique identifier、设备详情、位置、vendor/model/serial、software/firmware 以及责任角色等信息。

来源：

- https://csrc.nist.gov/pubs/sp/800/82/r3/final
- https://www.nist.gov/publications/guide-operational-technology-ot-security

**本页采用：**

- 设备身份来自权威 Registry / asset inventory；
- human-readable name、type、location、system membership 是扫描的主要信息；
- vendor/model/serial/firmware 可作为 professional columns / detail metadata，但不是默认首屏全部铺开；
- internal UUID 可以作为 join key，但不能作为 primary user label；
- inventory 变化必须由 owner 管理，本页不根据当前 telemetry 自动创造/删除资产。

## 3.3 ASHRAE Standard 223P — Equipment、System、Point 和关系必须显式建模

ASHRAE Standard 223P 的目标是用 machine-readable semantic model 表达 building system components、relationships 与 associated data，以支持 analytics、automation 与 control。

来源：

- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes
- https://osr.ashrae.org/Online-Comment-Database/ShowDoc2/Table/DocumentAttachments/FileName/4779-223p_PPR2_BACnetCommittee_draft-5_chair_approved.pdf/download/false

**本页采用：**

- Equipment、System membership、containment / relationship 不根据名称字符串猜；
- `AHU-01-SAT` 这种命名不能自动成为语义关系的唯一证据；
- 专业列可显示 parent/system/semantic type/point coverage；
- 关系编辑属于 Registry / Semantic Model 管理，不在设备中心列表中 inline 修改。

## 3.4 DOE Semantic Modeling and Interoperability — 不依赖命名约定推断设备、点位和关系

DOE 指出 building software 普遍受到 point mapping 与不一致命名的影响；semantic model 应以结构化方式显式表示 components、properties 与 relationships，而不是依赖各站点不同的命名规则。

来源：

- https://www.energy.gov/cmei/buildings/semantic-modeling-and-interoperability

**本页采用：**

- Type、System、Location、Point Role 来自明确 metadata；
- 搜索可以搜索名称，但业务判断不能从名称解析类型、位置或关系；
- Device Center 不做 regex-based asset classification；
- metadata 缺失就显示 `未配置` / `未知`，不自行补全。

## 3.5 DOE EMIS Metadata Best Practices — metadata quality 直接影响 visualization 与 analytics

DOE FEMP 指出标准命名与 metadata schema 能提高 EMIS 对运营数据的分析、可视化和价值提取能力，常见问题包括 point naming 不一致、metadata 缺失和 proprietary tagging。

来源：

- https://www.energy.gov/cmei/femp/articles/best-practices-energy-management-information-systems-metadata-schemas
- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities

**本页采用：**

- Device Center 可以暴露 metadata completeness / point coverage 事实；
- 数据模型问题进入 Data Quality / Semantic Model 页面解决；
- 本页不通过 UI heuristic 伪造“完整 metadata”。

## 3.6 ASHRAE BACnet — `IN_ALARM`、`FAULT`、`OVERRIDDEN`、`OUT_OF_SERVICE` 是不同状态

BACnet Status Flags 将 `IN_ALARM`、`FAULT`、`OVERRIDDEN`、`OUT_OF_SERVICE` 分离表达，且这些状态之间的关系并不由协议自动定义。

来源：

- https://data.ashrae.org/bacnet/
- https://www.ashrae.org/technical-resources/technical-faqs/question-51-what-is-bacnet

**本页采用：**

- 不把 Alarm / Fault / Override / Out of Service 合成一个 Health；
- Connectivity、Operating、Freshness、Quality 也保持独立；
- `Offline ≠ Fault`；
- `Stopped ≠ Fault`；
- `Stale ≠ Offline`；
- `Out of Service ≠ Decommissioned`。

## 3.7 NIST OT Asset Management 2026 — inventory / configuration / change visibility 是长期治理能力

NIST NCCoE 2026 OT Asset Management 项目将 automated/manual discovery、inventory management、configuration management 与 change management 作为 OT asset visibility 的核心问题。

来源：

- https://csrc.nist.gov/pubs/pd/2026/06/25/asset-management-as-a-foundation-for-ot-cybersecur/ipd

**本页采用：**

- 设备中心需要稳定 inventory，而不是只显示“最近上报过数据的设备”；
- configuration / firmware / change metadata 可以作为 professional context；
- 这些事实由 inventory/configuration owner 提供，不由浏览器猜测。

## 3.8 Siemens Building X Operations Manager — Equipment visibility 应连接 faults、work orders 与实时数据

Siemens Building X Operations Manager 公开能力包括多站点/设备实时可视、data-point updates、fault investigation 与 work-order integration。

来源：

- https://www.siemens.com/en-us/products/building-x/applications/operations-manager/

**本页采用：**

- Device Center 是设备人口的扫描入口；
- current facts、Alarm、Finding、Work 可以连续下钻；
- 但完整调查仍进入专业 Surface。

## 3.9 W3C — 高密度设备清单默认用 semantic table，不随意升级成 ARIA Grid

W3C APG 指出 ARIA Grid 是 composite widget，需要应用承担复杂键盘焦点管理；普通 tabular information 不应为了“高级”而无必要使用 Grid。

来源：

- https://www.w3.org/WAI/ARIA/apg/patterns/grid/
- https://design-system.w3.org/styles/tables.html

**本页采用：**

- 默认使用 native semantic Table；
- 只有未来真正需要 spreadsheet-like cell navigation / inline edit / cell selection 时才采用 Grid；
- 设备中心 v1 不做 inline edit，因此无需 ARIA Grid；
- 窄屏保持 table semantics，必要时由独立可访问 scroll region 承载横向滚动，而不是把表格强制改成语义模糊的卡片。

---

# 4. Primary Questions

用户进入后按以下顺序回答。

## Q1 — 当前站点有哪些设备？

必须知道：

- 当前 Site；
- 当前可见设备数量；
- 当前 filters / search；
- 是否只看 Active lifecycle assets；
- inventory owner 是否可信可用。

## Q2 — 这台设备是什么、在哪里、属于哪个系统？

需要看到 authoritative：

- Display Name；
- Equipment Type；
- System / Domain；
- Location；
- Business asset code（若存在）。

Vendor / Model / Serial / Firmware 属于 professional metadata，不默认铺在首屏。

## Q3 — 它现在是什么运行与数据状态？

必须分别表达：

- Operating；
- Connectivity / Presence；
- Freshness；
- Quality；
- Lifecycle / Out-of-Service（若存在）。

不能合并成一个 `Health`。

## Q4 — 当前有需要关注的事项吗？

只显示 authoritative summary：

- Alarm；
- Finding / Diagnosis；
- Work；
- Override；
- Data issue。

不在前端计算 generic risk score。

## Q5 — 下一步去哪里？

进入：

- Device Detail；
- Trend Analysis；
- System Operations；
- Alarm Center；
- Diagnosis Center；
- Work Order；
- Data Quality；
- Control Center（有权限时）。

---

# 5. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/devices
```

Durable device detail：

```text
/sites/:siteId/devices/:deviceId
```

设备中心默认不拥有 analytical time range。

推荐 Search Params：

```text
q
system
location
type
operating
connectivity
freshness
quality
alarm
finding
work
lifecycle
sort
page
size
selected
```

`selected` 仅用于可恢复的 Context Inspector selection。

不要把：

- hover row；
- tooltip；
- column resize pixel；
- temporary menu state；
- Sheet open animation state

写入 URL。

原则：

> **Search / filter / sort / page / selected asset 是可恢复设备扫描状态；实时数值本身不是 URL state。**

---

# 6. Entry Contract

## 6.1 从 Site Overview

携带：

- Site；
- 如果来源是具体设备 population issue，可携带 filter；
- Return-to-source。

## 6.2 从 System Operations

携带：

- Site；
- System；
- selected equipment（若已有）；
- 保留 Investigation Trail。

如果只想持续调查一个设备，应优先进入 Device Detail，而不是绕到设备中心。

## 6.3 从 Alarm / Diagnosis / Work

如果来源明确指向一个设备：

- 快速查看可打开 Device Center + selected Inspector；
- 持续调查优先进入 Device Detail。

不要把 Alarm evidence window 强塞进设备中心，因为本页不是 time-series workspace。

## 6.4 从 Data Quality

可携带：

- Site；
- affected asset filter；
- freshness / quality filter；
- source trail。

---

# 7. Exit Contract

## Row selection

```text
单击行
→ 选中
→ 打开 / 更新 Context Inspector
```

## Durable detail

```text
设备名称链接 / 打开详情
→ /sites/:siteId/devices/:deviceId
```

## Professional exits

从 Inspector / row action 明确进入：

- 05 Trend Analysis；
- 04 System Operations；
- 07 Device Detail；
- 09 Alarm Center；
- 10 Diagnosis Center；
- 11/12 Work Order；
- 25 Control Center；
- 31 Data Quality。

保持 compatible Site / Object / Source context。

---

# 8. Responsibility Boundary

设备中心拥有：

- asset population scan；
- search / filter / sort；
- authoritative asset identity presentation；
- independent current-state summary；
- page-level current-value projection；
- Context Inspector；
- durable deep links；
- optional saved view（只有 persistence owner 已存在时）；
- capability-gated export（只有 owner 已存在时）。

设备中心不拥有：

- Registry master-data edit；
- relationship edit；
- full point configuration；
- Alarm ACK / Assign；
- diagnosis conclusion；
- Work Order lifecycle mutation；
- control write；
- strategy publish；
- firmware update；
- OT network discovery；
- asset procurement / depreciation；
- generic health score。

---

# 9. Information Architecture

```text
Page Header
  Site context
  Device population summary

Toolbar
  Search
  System / Type / Operating / Connectivity
  More Filters
  Saved View (if supported)
  Column Settings

Device Ledger / Table
  stable scan order
  explicit independent states

Context Inspector (selected row)
  identity
  independent states
  4–8 key facts
  current Alarm / Finding / Work
  professional exits

Pagination / result count
```

设备中心不是 card-first 页面。

---

# 10. Default View: Ledger / Table First

默认使用高密度、可排序、可筛选的 Table。

理由：

- 用户任务是 scan / compare / find；
- 设备数量可能从几十到几千；
- 相同字段需要纵向比较；
- W3C table semantics 对阅读和辅助技术更明确；
- Card wall 会重复 label、降低比较效率并显著增加滚动。

## 10.1 Card View Policy

**v1 不提供默认 Card View。**

未来只有在存在经过验证的业务任务，例如照片/空间识别对选择设备有明显价值时，才单独评估 Card View。

禁止为了“看起来现代”维护 Table / Card 两套等价视图。

---

# 11. Default Columns

目标是 **7–9 个业务列**完成主要扫描任务。

推荐默认列：

| 列 | 内容 |
|---|---|
| 设备 | Display Name + Type |
| 系统 / 位置 | System membership + concise location |
| 运行 | Operating state |
| 连接 | Connectivity / Presence |
| 数据 | Freshness + Quality，两个独立 label |
| 关键值 | 设备类型 owner 定义的 1–2 个 primary operational values |
| 当前事项 | Alarm / Finding / Work 的 concise authoritative summary |
| 更新 | latest relevant observation / snapshot timestamp |

`Freshness` 和 `Quality` 虽然可以在同一视觉列中紧凑显示，但语义必须保持独立，不能合成 `Data Health`。

## 11.1 Optional Engineering Columns

Column Settings 可以启用：

- Vendor；
- Model；
- Serial；
- Firmware；
- Asset Code；
- Parent / System relationship；
- Point coverage；
- Lifecycle；
- Override；
- Out of Service；
- Control authority；
- Work owner / due state；
- semantic type / classification。

这些列必须由真实 owner 提供。

---

# 12. Identity Contract

Primary user-facing identity：

```text
Display Name
Equipment Type
System
Location
Business Asset Code (if useful)
```

Internal UUID：

- 允许作为 canonical join / route identity；
- 不作为 primary label；
- 不在默认表格显示；
- engineering metadata 只有有真实需求和权限时才可查看。

## 12.1 Identity Stability

Registry identity 与 telemetry state 解耦。

```text
设备离线
≠
设备从清单消失
```

```text
没有当前 telemetry
≠
设备不存在
```

```text
Decommissioned
≠
Offline
```

---

# 13. Equipment Type / System / Location Contract

Type、System、Location 必须来自 explicit metadata / semantic model。

禁止：

```text
name startsWith "CH" → Chiller
path contains "B1" → Basement
power > 100kW → Chiller
point tag contains "SAT" → AHU
```

如果 metadata 缺失：

```text
Type: 未分类
System: 未关联
Location: 未配置
```

而不是前端猜测。

## 13.1 Relationship Presentation

Inspector / optional columns 可以展示：

- member of system；
- parent equipment；
- served area / domain space（若 semantic owner 提供）；
- related meter / sensor count；
- point coverage。

设备中心只读呈现关系。

关系编辑进入 Semantic Model / Registry 管理页面。

---

# 14. Independent State Semantics

设备中心禁止一个 `Health` / `Healthy-Unhealthy` 总状态覆盖所有事实。

## 14.1 Operating

示例：

```text
Running
Stopped
Standby
Starting
Stopping
Unavailable
Unknown
```

必须由 domain owner 明确定义。

`Stopped` 本身不是异常。

## 14.2 Connectivity / Presence

示例：

```text
Online
Offline
Unknown
```

Connectivity owner 负责语义。

`Stream disconnected` 不能直接等于 device offline。

## 14.3 Freshness

示例：

```text
Fresh
Stale
Unknown
```

Freshness 阈值必须由 telemetry / data owner 的明确 policy 提供。

前端不能写死“5 分钟没数据 = stale”作为通用规则。

## 14.4 Quality

示例：

```text
Good
Suspect
Bad
Unknown
```

如果设备级 quality 是多个 points 的聚合，聚合规则必须由 owner 定义并可追溯。

## 14.5 Alarm

来自 Alarm domain。

可以显示：

- active alarm count；
- highest active severity；
- concise current alarm fact。

`0 active alarms` 只在 Alarm owner 成功确认后显示。

Alarm owner unavailable 时显示 `告警状态暂不可用`，不是 `0`。

## 14.6 Finding / Diagnosis

来自 Diagnosis owner。

Finding 不等于 Alarm，也不等于 proven root cause。

## 14.7 Override

来自 authoritative control / BAS state。

Override 存在是独立事实。

## 14.8 Out of Service

来自 authoritative registry/control/device state。

不能用 Offline 推断 Out of Service。

## 14.9 Work / Maintenance

来自 Work owner。

Work 状态不能改变物理 Operating / Alarm 状态。

---

# 15. Key Current Values

设备列表默认只显示 **1–2 个可快速扫描的 primary operational values**。

例如（仅表示 owner 可定义的 display profile，不是前端规则）：

```text
Chiller → load / power
Pump → speed / power
AHU → SAT / fan state
Meter → demand / energy rate
```

关键值必须来自 **equipment presentation metadata / domain-owned display profile**。

禁止：

- 取“第一个 telemetry point”；
- 根据 point name 猜主要值；
- 每种设备在前端写一大组 if/else heuristic；
- 当前值失败时用历史最后值冒充 current。

如果没有 configured primary value：

> `—`

Inspector / Device Detail 再显示更多 values。

---

# 16. Search Contract

Search 优先匹配：

- Display Name；
- Business Asset Code；
- Type label；
- System；
- Location；
- Vendor / Model（若 owner 支持）。

Search 对用户显示 human-readable result。

Internal UUID 可在有明确专业需求和权限时通过 advanced search 支持，但不作为默认用户心智。

Search 必须 server/query owner 可扩展，不把全站几千设备下载到浏览器后 client-side filter。

---

# 17. Filter Contract

默认只展示 **3–5 个高频 filter**。

推荐：

```text
System
Type
Operating
Connectivity
```

`Search` 不算 filter chip。

More Filters：

```text
Location
Freshness
Quality
Alarm
Finding
Work
Lifecycle
Override
Out of Service
```

所有 active filters 必须有：

- visible active count；
- readable summary / chip；
- Clear All。

禁止打开页面就铺两行 15 个 Select。

---

# 18. Sorting Contract

默认排序应该稳定、可预测，例如：

```text
System → Display Name
```

或站点定义的 canonical asset order。

禁止默认使用不可解释的 `Risk Score` / `Health Score` 排序。

## 18.1 Live Update and Sort Stability

实时状态更新**不能让行在用户眼前持续跳动**。

默认 static sort 下：

- row order 保持；
- cell facts 原位更新。

如果用户显式按动态列排序：

- 排序由 query / explicit refresh 重新计算；
- stream tick 不每次即时重排行。

目标是保持 scan position 和 situational awareness。

---

# 19. Pagination / Scale

设备中心面向几十到数千设备。

默认采用 server-owned pagination / result count。

推荐 Search Params：

```text
page
size
sort
```

原则：

- URL 可恢复；
- filter / sort 变化回到合理 page；
- 不使用 infinite scroll 作为默认资产 inventory 交互；
- 不把全站所有设备和所有 telemetry 一次性下载到浏览器。

页面大小属于产品密度选择，不是“越多越专业”。

---

# 20. Context Inspector

Row selection 打开 Inspector，保持 Ledger context。

Inspector 只承担**快速判断**。

## 20.1 Identity

```text
Display Name
Type
System
Location
Asset Code (if available)
```

## 20.2 Independent States

```text
Operating
Connectivity
Freshness
Quality
Alarm
Finding
Override
Out of Service
Work
```

只显示 owner 实际支持的事实。

## 20.3 Key Current Values

显示约 **4–8 个 domain-relevant facts**。

来源必须是设备 presentation contract / semantic metadata。

## 20.4 Current Attention

简洁显示：

- active alarm；
- active finding；
- open work；
- data issue。

每个 item 都有 owner route。

## 20.5 Professional Exits

最多突出 1–3 个当前最相关动作，例如：

```text
打开设备详情
查看趋势
查看告警
```

其他进入 overflow / secondary links。

## 20.6 Inspector 不承载

- full point list；
- 完整历史；
- 长趋势；
- Registry 编辑；
- 关系编辑；
- 长工单；
- 复杂控制；
- 审批；
- firmware update。

这些进入 Durable Detail / 专业 Surface。

---

# 21. Device Detail Boundary

设备详情 route：

```text
/sites/:siteId/devices/:deviceId
```

设备中心回答：

> **“哪台设备值得打开？”**

设备详情回答：

> **“围绕这一台设备持续调查时，我需要哪些完整事实？”**

如果需要：

- 分享链接；
- 多 section；
- full point list；
- 长历史；
- 多 domain references；
- sequence / control；
- audit trace；

必须进入 Device Detail。

---

# 22. Bulk Action Policy

设备中心 **不默认提供业务 mutation 型 bulk action**。

明确禁止在设备清单中提供：

```text
Bulk Start
Bulk Stop
Bulk Setpoint Write
Bulk Override
Bulk ACK Alarm
Bulk Close Work
```

如果未来真实 owner 提供合法批量管理需求，例如：

- capability-gated export；
- approved metadata administration；
- lifecycle administration；

必须进入专门管理 Surface / Dialog，并经过独立产品审查。

不要为了“表格通常有 checkbox”就加 row selection checkbox。

---

# 23. Compact Summary

Page Header 下可提供 compact population facts，但不能变成 KPI Card Wall。

可选事实（owner 存在时）：

```text
Registered devices
Online / Offline population
Stale-data device count
Devices with active alarms
```

规则：

- 每个数字有明确 owner；
- Alarm owner unavailable 时不能显示 `0`；
- 不制造 `Healthy devices` / `Health %`；
- summary 只帮助理解 filter / population，不成为独立 Dashboard。

---

# 24. Data Authority Contract

## Identity / Type / Location / Relationship / Lifecycle

Owner：Registry / Site Asset Model / Semantic Model。

## Operating State

Owner：Operations / device operational projection。

## Connectivity / Presence

Owner：Realtime presence / connectivity domain。

## Current Values

Owner：Telemetry Snapshot。

## Freshness / Quality

Owner：Telemetry / data-quality contract。

## Alarm Summary

Owner：Alarm domain。

## Finding Summary

Owner：Diagnosis domain。

## Work Summary

Owner：Work Order domain。

## Override / Control Authority

Owner：Control / BAS state contract。

## Firmware / Configuration

Owner：Asset / integration / configuration inventory contract。

Frontend 可以投影这些事实到同一行，但**不能成为这些事实的新 owner**。

---

# 25. Query / Read Model Contract

设备中心需要避免 N+1。

合理模式：

```text
Asset Page Query
  ↓
canonical device ids
  ↓
Batch Snapshot / Batch State / Domain Summary projection
```

或使用经过正式架构定义、保留事实 provenance 的 page-level read model。

明确禁止：

```text
200 rows
→ 200 current-value requests
→ 200 alarm requests
→ 200 finding requests
```

如果某 domain 没有 batch / projection 能力，应先修 owner contract，而不是在前端堆并发请求 workaround。

---

# 26. Snapshot / Stream Realtime Contract

设备中心实时模式：

```text
Snapshot = 当前页设备的权威当前状态
Stream = 当前可见 scope 的授权增量变化
```

订阅范围原则：

- 当前 Site；
- 当前 filter/page 或 owner 定义的 page scope；
- selected Inspector object。

不要为了实现简单订阅整个企业所有 telemetry。

Realtime update：

- 更新 cell；
- 更新 Inspector；
- 不抢 focus；
- 不自动改变 row selection；
- 不持续重排行；
- 不把 stream disconnect 解释成 asset offline。

Reconnect：

- 重新获得 authoritative Snapshot；
- 再恢复 Stream；
- 不叠加重复 event / telemetry。

---

# 27. Empty / Loading / Partial / Error

## 27.1 No Assets Registered

> 当前站点尚未登记设备。

有配置权限时可以提供：

> 前往数据与集成 / 语义模型管理

无权限时只提供事实，不展示无效管理入口。

## 27.2 Filter Returns No Result

> 没有设备符合当前筛选条件。

提供 `清除筛选`。

不要显示“暂无设备”造成 inventory 为空的误解。

## 27.3 Registry Unavailable

整个设备 inventory 无法建立：

> 设备清单暂不可用。

不能用 telemetry 中最近出现的 IDs 临时拼一份设备清单。

## 27.4 Telemetry Unavailable, Registry Available

允许显示稳定 identity rows，但：

```text
Operating: 暂不可用（如果依赖 telemetry）
Current Value: 暂不可用
Freshness: 暂不可用
```

不能把它们显示成 `Stopped / 0 / Offline`。

## 27.5 Alarm Owner Unavailable

Alarm cell / inspector 显示：

> 告警状态暂不可用

不能显示 `0 active alarms`。

## 27.6 Partial Domain Availability

不同 domain 的 partial failure 必须局部表达。

不要因为 Alarm unavailable 就把整个 Device Center 变成 error page；也不要因为表格还能显示 identity 就静默吞掉 Alarm error。

---

# 28. Permission / Capability

Capability Gating：

- 无 Alarm read → 不显示 Alarm column / link；
- 无 Diagnosis read → 不显示 Finding；
- 无 Work read → 不显示 Work；
- 无 engineering metadata permission → 隐藏 raw point / firmware / internal metadata；
- 无 Control capability → 不显示 control authority deep link；
- 无 export permission → 不显示 Export。

没有权限的高级能力默认不作为 disabled teaser。

---

# 29. Saved Views

只有在真实 persistence owner 存在时才支持 Saved View。

Saved View 可以保存：

```text
filters
sort
columns
page size
```

不保存：

- current realtime values；
- transient row selection；
- temporary Inspector state。

如果没有 persistence owner，就不要加 localStorage-only 假“Saved View”。

---

# 30. Export

只有有正式 export owner / permission 时提供。

Export 至少包含：

- Site；
- asset identity；
- type；
- system / location；
- lifecycle；
- exported operational state（带 observation timestamp）；
- current values（若 export scope 明确）；
- data timestamp；
- export timestamp。

不要把 stale/current 混在一起又不带 timestamp。

不要把 internal UUID 当作用户唯一可读 identifier；可以作为附加 machine key。

---

# 31. Responsive Behavior

## 1440–1720 px

首屏应看到：

- Site / page title；
- compact population facts；
- Search + primary filters；
- 约 8 个核心 columns；
- 15–25 条可扫描 rows（取决于 density）；
- selected 时的 Inspector。

页面主要视觉面积属于 Ledger，不属于 KPI cards。

## 1024–1439 px

- Toolbar 可 wrap；
- System / Type 等 filter 保持可达；
- Inspector 可以压缩宽度或转为 Sheet；
- secondary columns 进入 Column Settings。

## Around 768 px

核心任务仍必须完成：

- 搜索设备；
- 使用主要 filter；
- 扫描 Name / Operating / Connectivity / Data / Attention；
- 选择 row；
- 打开 Inspector；
- 进入 Device Detail。

策略：

- 保持 Table semantics；
- 优先减少 secondary visible columns；
- 必要时 table 自己成为 labelled horizontal scroll region；
- Inspector 使用 Sheet / full-width contextual surface。

禁止因为屏幕窄就自动改成完全不同信息架构的卡片墙。

---

# 32. Accessibility

必须：

- 使用 semantic table header / row semantics；
- status 不只靠颜色；
- selected row 有明确 focus / selected state；
- row selection 与 name link 是两个可理解的交互；
- keyboard 能完成 search / filter / row select / open detail；
- table overflow region 有 accessible label；
- Inspector / Sheet 打开后遵循 focus management；
- realtime update 不移动 focus；
- Badge 文本能表达状态，不使用只有图标的模糊语义。

ARIA Grid 只有未来真正需要 cell-level interaction 时才允许引入，并必须完整实现 W3C keyboard contract。

---

# 33. No Defensive Programming / No Compatibility Design

明确禁止：

```text
value || 0
unknown → offline
missing operating state → stopped
telemetry missing → device deleted
historian last value → current value
alarm API failed → activeAlarmCount = 0
finding API failed → findingCount = 0
stream disconnected → device offline
metadata missing → parse device name to infer type
location missing → parse asset path string
system missing → infer from point prefix
first telemetry point → primary metric
multiple APIs → first successful result wins
one request per row N+1
old /devices + new /assets dual product routes
old card view compatibility adapter
legacy DeviceDrawer wrapper
universal BaseAssetTable owning domain semantics
```

不要保留旧 Device Center 的视觉、路由或数据 fallback 逻辑。

不要做“新页面失败就退回旧页面”。

正确原则：

> **One fact → one owner. One asset → one canonical identity. Unknown stays unknown. Unavailable stays unavailable.**

---

# 34. Component Mapping

```text
Page / context header        → application layout
Search                       → InputGroup / searchable input
Primary filters              → Select / Popover
More filters                 → Dropdown / Popover composition
Active filters               → Badge / removable filter chips
Column settings              → Dropdown Menu / Checkbox composition
Device ledger                → shadcn Table + TanStack Table
Row status                   → Badge + text / compact semantic state
Row selection                → TanStack row state + accessible highlight
Context Inspector            → responsive aside / Sheet
Pagination                   → Button-based accessible pagination
Engineering metadata         → Collapsible / disclosure
Professional exits           → Button / Link
Export                       → Dropdown Menu / explicit action
```

不建立 `UniversalDataTable` 来拥有业务状态含义。

Table abstraction最多负责纯技术 plumbing；column semantics 保留在 feature domain。

---

# 35. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 设备中心 · 凤凰中央冷站                       已登记 236 台                  │
│ Online 221 · Offline 7 · Stale data 5 · Active alarms 9                   │
├──────────────────────────────────────────────────────────────────────────────┤
│ [搜索设备…] [系统: 全部] [类型: 全部] [运行: 全部] [连接: 全部] [更多 2]  │
│ Active: 质量=Suspect · Alarm=Active                         [列设置]       │
├───────────────────────────────────────────────────────┬──────────────────────┤
│ 设备       系统/位置   运行   连接   数据   关键值   当前事项   更新      │
│ CH-01      冷冻水系统   运行   在线   Fresh  504 kW  —          11:42     │
│ CH-02      冷冻水系统   运行   在线   Good   487 kW  1 告警     11:42     │
│ CHWP-03    冷冻水系统   待机   在线   Stale  —       数据问题   11:31     │
│ CT-02      冷却水系统   运行   在线   Good   42 Hz   1 Finding  11:42     │
│ ...                                                   │                    │
│                                                       │ CH-02              │
│                                                       │ 离心式冷机          │
│                                                       │ 冷冻水系统 · B1     │
│                                                       │                    │
│                                                       │ 运行: Running      │
│                                                       │ 连接: Online       │
│                                                       │ Freshness: Fresh   │
│                                                       │ Quality: Good      │
│                                                       │                    │
│                                                       │ 负荷 73%           │
│                                                       │ 功率 487 kW        │
│                                                       │ CHWS 7.0°C         │
│                                                       │ CHWR 12.2°C        │
│                                                       │                    │
│                                                       │ 1 个当前告警        │
│                                                       │ 1 个相关工单        │
│                                                       │                    │
│                                                       │ [打开详情]          │
│                                                       │ [查看趋势]          │
│                                                       │ [查看告警]          │
├───────────────────────────────────────────────────────┴──────────────────────┤
│ 1–25 / 236                                            [上一页] 1 2 … 10 [下一页]│
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达责任和密度，不是 pixel specification。

---

# 36. Browser Acceptance Criteria

## Inventory / Identity

- Site scope 明确；
- 至少 200-device review fixture 下扫描仍清晰；
- internal UUID 不作为 primary label；
- metadata 缺失显示 `未配置 / 未分类 / 未关联`，不猜；
- Offline device 仍保留在 Registry population 中；
- Decommissioned 与 Offline 可区分。

## Table / Density

- 默认是 Table first；
- 默认约 7–9 个业务列；
- 无默认 Card Wall；
- Row click 更新 Inspector；
- 名称链接进入 durable detail；
- row action 不存在多个不可预测 click target；
- dynamic stream update 不持续重排行。

## Independent States

必须验证：

```text
Operating != Connectivity
Connectivity != Freshness
Freshness != Quality
Alarm != Fault
Alarm != Work
Offline != Fault
Stopped != Fault
OutOfService != Decommissioned
```

不能存在 generic Health badge 替代这些事实。

## Data Truth

- real zero 显示 `0`；
- missing current value 显示 unavailable / `—`；
- stale 不显示为 current truth；
- Alarm owner unavailable 不显示 `0 alarms`；
- Telemetry unavailable 时 identity rows 仍然存在；
- Stream disconnect 不把所有设备改成 Offline。

## Filters / URL

- Search / filters / sort / page 可通过 URL 恢复；
- Clear All 正确；
- filter no-result 与 inventory empty 区分；
- filter 变化不会保留无效 selected Inspector；
- Site change 清除 incompatible selection。

## Inspector

- 快速显示 identity + independent states + 4–8 key facts；
- 不显示 full point list；
- 不承载长历史；
- Device Detail / Trend / Alarm / Diagnosis deep links 正确；
- Source context 保留。

## Performance / Query Shape

- 无一行一个 API 的 N+1 模式；
- current-value / state 使用 batch snapshot / projection；
- 不下载全站全部 telemetry 再前端过滤；
- page/filter query 是 server/query-owner scope。

## Accessibility

- native table semantics；
- 状态不只靠颜色；
- keyboard 可 search / filter / select row / open detail；
- table overflow region 可访问；
- Inspector focus management 正确；
- realtime 不抢 focus。

## Responsive

1440–1720 px：

- Header / Toolbar / Ledger / Inspector 构成单一工作区；
- Ledger 是主要视觉面积；
- 无 page-level horizontal overflow。

Around 768 px：

- Search / primary filter 可用；
- 核心状态列可扫描；
- secondary columns 可收起；
- table 自己可访问地滚动而不是 page 横向溢出；
- Inspector 可用 Sheet；
- 无强制 Card Wall 转换。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 `/devices` compatibility surface；
- 无旧 DeviceDrawer adapter；
- 无 generic Health score；
- 无 runtime/network error in review scenario；
- 无 N+1 row fetch；
- internal IDs 不进入业务事实显示。

---

# 37. Explicit Non-Goals

设备中心不是：

- Procurement / ERP；
- depreciation register；
- CMMS replacement；
- point configuration editor；
- semantic relationship editor；
- BACnet browser；
- network discovery tool；
- firmware deployment console；
- control command center；
- alarm handling center；
- full device history page；
- topology replacement。

---

# 38. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Registry / Asset Model 是 canonical identity owner；
- Type / System / Location 来自 explicit metadata；
- Operating / Connectivity / Freshness / Quality 等状态 owner 明确；
- current values 有 batch snapshot / projection contract；
- Alarm / Finding / Work summary 有 domain-owned contract；
- Table-first 与 no-health-score policy 已接受；
- Inspector / Device Detail 边界已接受；
- 无 N+1 fetch 作为计划实现；
- 旧 Device Center / Card View / `/devices` 路由无设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
