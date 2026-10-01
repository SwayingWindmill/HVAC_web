# 05 告警与诊断 Workspace — Current Implementation Brief

**Status: IMPLEMENTED / BROWSER-REVIEWED**
**Primary route:** `/sites/$siteId/issues`

## 1. Responsibility

“告警与诊断”不是两个平级页面，也不是两个 Tab。主任务是一条连续的问题处理链：

```text
Alarm condition
→ handling state
→ affected object / current context
→ published Finding / evidence
→ hypothesis / root-cause status
→ next verification
→ Work / Device / Operations handoff
```

领域模型仍严格分离：Alarm 是权威异常条件与处置状态；Finding 是 rule/model/human investigation 发布的结构化诊断结果。ACK ≠ cleared，Finding ≠ confirmed Root Cause，confidence ≠ root-cause probability，Work complete ≠ issue verified。

## 2. 36 Surface 设计继承

Surface 09 提供主工作区骨架：active-first triage、告警负荷事实、当前活动/历史/已搁置/告警绩效、本体 Ledger、统一 Detail Sheet、ACK/Assign/Shelve。

Surface 10 不再作为平级 Tab，而是进入每条 Alarm 的 investigation context，保留 Published Finding、source、evaluation window、evidence、quality blocker、Root Cause status、Next Verification 和 Work/Device/Operations handoff。没有 Finding 时显示“待诊断”，不能生成假根因。

## 3. Current composition

```text
Shell: 告警与诊断
├─ Site / timezone / refresh
├─ FactStrip
│  ├─ 活动告警
│  ├─ 未确认
│  ├─ 未指派
│  └─ 已搁置
├─ Alarm-local views
│  ├─ 当前活动
│  ├─ 历史
│  ├─ 已搁置
│  └─ 告警绩效
├─ standalone Issue Ledger
│  ├─ 等级
│  ├─ 告警 / 来源
│  ├─ 物理状态
│  ├─ 确认
│  ├─ 诊断
│  ├─ 负责人
│  └─ lifecycle facts
└─ Selected Issue Detail Sheet
   ├─ Alarm facts
   ├─ handling state
   ├─ integrated Diagnosis
   │  ├─ Finding / evidence
   │  ├─ quality blocker
   │  ├─ root-cause status
   │  └─ next verification
   ├─ evidence / handling history
   ├─ ACK / Assign
   └─ Operations / Device / Work exits
```

默认不自动打开详情。用户选择 Alarm 后打开右侧 Detail Sheet；desktop 为 non-modal/no-overlay，narrow 为 modal/overlay。

## 4. Route / URL ownership

Canonical route：

```text
/sites/:siteId/issues
```

Alarm-local state：

```text
alarmView=active|history|suppressed|performance
q
severity
ack
owner
sourceType
selected
source
device
deviceId
```

`selected` 是当前 Alarm occurrence identity。Diagnosis 通过 Alarm ↔ Finding 显式关系加载，不再使用 `view=diagnostics`。旧 `view=alarms|diagnostics`、`/alarms`、`/diagnostics` 均不再是 runtime contract。

## 5. Frontend-review contract

Frontend review 必须提供互相关联的 Alarm + Finding 数据，不允许 Shell 使用 review data，而 Alarm/FDD 继续请求真实后端。Review 至少覆盖 active/unacknowledged、acknowledged/assigned、published diagnosis、evidence-quality blocker、cleared history，以及 Device→Asset→Space 的 registry relationship。

## 6. Visual and semantic rules

- 不显示顶层“告警 / 诊断”Tabs；
- 不重复页面标题；
- FactStrip 不回退成 KPI Card wall；
- Ledger 不套额外 Card，并直接展示 `已诊断 / 证据受限 / 待诊断`；
- desktop / narrow 使用同一 Detail Sheet 内容；desktop non-modal，narrow modal；
- 禁止无 owner/证据的“智能排查建议”；
- Finding 评分只能解释为 source result/confidence，不能写成根因概率；
- 无 authoritative root-cause owner 时显示“尚未确认根因”。

必须保持：

```text
Alarm ≠ Finding
Finding ≠ Root Cause
ACK ≠ Clear
Cleared ≠ Verified stable
Correlation ≠ Causality
AI suggestion ≠ Root Cause
Work complete ≠ Root Cause verified
```

## 7. Acceptance

1. Sidebar 恰好 10 个 Workspace，active 为告警与诊断；
2. 页面不存在顶层告警/诊断 Tabs；
3. frontend-review 不停留在加载状态；
4. Ledger 有真实 Alarm 行并包含诊断列；
5. Detail Sheet 同时出现 Alarm facts 与 Diagnosis；
6. Diagnosis 含 Finding/evidence/root-cause status/next verification；
7. 无 Finding 时显示待诊断；
8. desktop/narrow 默认均不自动打开详情；选择 Alarm 后打开 Sheet，desktop 不压缩 Ledger；
9. desktop/narrow 无 page-level horizontal overflow；
10. build/typecheck/lint/design/browser review 全通过。
