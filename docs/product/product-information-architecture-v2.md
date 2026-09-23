# 泉来禾智慧能源平台 Product IA v2

**Status: SUPERSEDED / HISTORICAL**  
**Superseded by:** `docs/product/smart-energy-system-page-architecture-v2.md`  
**Date:** 2026-09-13  
**Scope:** historical operator-facing Web planning artifact  
**Rule:** do not use this file as page-design authority. It is retained only as planning history.

## 1. Product organizing principle

The product is organized around the work users must complete, not around backend services, technical acronyms or legacy modules.

Primary closed loop:

```text
Understand current operation
→ detect something worth attention
→ investigate evidence
→ determine cause / impact
→ establish responsibility
→ approve / execute an action
→ verify the physical and energy result
```

A page exists only when it owns a durable user task or a durable investigation context.

## 2. Primary users and jobs

### Operations duty operator
- Know whether the site is stable now.
- See urgent alarms, unowned work and abnormal operation quickly.
- Acknowledge and route work without changing physical truth.

### HVAC engineer
- Understand plant, distribution and terminal operation.
- Compare devices and trends.
- Diagnose abnormal relationships and verify interventions.

### Energy manager
- Understand consumption, demand, cost and efficiency.
- Identify explainable savings opportunities.
- Verify savings after implementation.

### Control / strategy approver
- Review proposed control changes and their constraints.
- Approve high-impact actions.
- Verify command execution and readback.

### Platform administrator
- Manage site structure, rules, integrations, identities and permissions.

## 3. Selected top-level navigation

```text
运营
  总览
  系统运行

运行管理
  设备
  告警
  诊断
  工单

能源优化
  能源
  效率
  节能机会
  优化方案          [Phase 2]
  节能验证          [Phase 2]

自动控制
  控制中心          [Phase 2]
  策略与计划        [Phase 2]
  执行记录          [Phase 2]

分析
  趋势分析          [Phase 2]
  报告              [Phase 2]

系统
  站点配置          [Phase 3]
  规则与通知        [Phase 3]
  数据与集成        [Phase 3]
  用户与权限        [Phase 3]
```

The sidebar should expose only pages that are actually available to the current principal and deployment. Do not show fake enabled modules to complete the menu visually.

## 4. Phase 1 core surfaces

Phase 1 deliberately limits the redesign to ten surfaces that form the daily operating loop.

| # | Surface | Primary job | Durable route intent |
|---|---|---|---|
| 1 | 运营总览 | Decide what deserves attention now | `/sites/$siteId/overview` |
| 2 | 系统运行 | Understand current HVAC system operation | `/sites/$siteId/operations` |
| 3 | 设备中心 | Scan/filter/compare assets in context | `/sites/$siteId/assets` |
| 4 | 设备详情 | Sustain investigation of one device | `/sites/$siteId/assets/$deviceId` |
| 5 | 告警中心 | Triage authoritative alarm facts | `/sites/$siteId/alarms` |
| 6 | 诊断中心 | Investigate why a problem may be occurring | `/sites/$siteId/diagnostics` |
| 7 | 工单中心 | Establish responsibility and track execution | `/sites/$siteId/work-orders` |
| 8 | 能源分析 | Explain where/when energy is used | `/sites/$siteId/energy` |
| 9 | 效率分析 | Explain HVAC system and equipment efficiency | `/sites/$siteId/efficiency` |
| 10 | 节能机会 | Prioritize evidence-backed savings opportunities | `/sites/$siteId/opportunities` |

These are route **intent**, not compatibility requirements. Migration should converge on this model without preserving duplicate old routes in the product design.

## 5. Phase 2 surfaces

### 优化方案
Turns a savings opportunity into an engineering change proposal with expected effects, constraints, risk, approval and execution plan.

Route intent: `/sites/$siteId/optimization-plans` and `/sites/$siteId/optimization-plans/$planId`.

### 节能验证
Answers whether an executed optimization actually saved energy, using baseline, actual, normalization, exclusions and confidence.

Route intent: `/sites/$siteId/verification`.

### 控制中心
Shows current control authority, mode, setpoints, schedules and controllable objects. It is not a generic CRUD page.

Route intent: `/sites/$siteId/control`.

### 策略与计划
Owns schedules, reset strategies, sequencing, demand response and other durable automation policy.

Route intent: `/sites/$siteId/strategies`.

### 执行记录
Auditable ledger: requested → approved → sent → acknowledged → readback → verified.

Route intent: `/sites/$siteId/executions`.

### 趋势分析
Engineering analysis workspace for multi-point history, comparison, event overlays and time-window investigation.

Route intent: `/sites/$siteId/trends`.

### 报告
Durable generated operational and energy reports; not a dashboard clone.

Route intent: `/sites/$siteId/reports`.

## 6. Phase 3 administration surfaces

Administration is separated from daily operations because its users, cadence and risk are different.

- Site configuration: spaces, systems, structure, metadata.
- Rules & notifications: alarm rules, notification routing, rule publication.
- Data & integrations: gateways, points, mappings, protocol/integration health, data quality.
- Users & permissions: identities, roles, grants, audit.

Administration does not define operator page composition.

## 7. Cross-surface navigation model

The system should preserve investigation context rather than forcing users to restart on every page.

Examples:

```text
Overview priority item
→ Alarm
→ Diagnosis
→ Work Order
→ Device
→ Trend evidence
```

```text
Energy anomaly
→ Efficiency evidence
→ Device / system operation
→ Savings opportunity
→ Optimization plan
→ Verification
```

```text
System operation
→ Device
→ Alarm / Diagnosis
→ Control proposal
→ Execution readback
→ Operation verification
```

Navigation should carry durable context through URL search params when it is shareable and browser-history meaningful, such as time range, selected system, comparison set, source alarm, source opportunity or selected device.

## 8. Page-class rules

### Ledger pages
Use for objects that users scan, sort, filter and act on repeatedly:
- Assets
- Alarms
- Work orders
- Opportunities
- Execution records

Default pattern: compact page intro → small factual summary when needed → toolbar → TanStack Table → contextual inspector for rapid triage.

### Engineering workspaces
Use when spatial/temporal relationships matter more than rows:
- System operation
- Trends
- Efficiency analysis
- Diagnostics

Default pattern: context controls → primary analysis canvas → evidence/inspector → durable deep links.

### Detail pages
Use for durable, shareable investigation of one identity:
- Device detail
- Work-order detail
- Optimization-plan detail

Do not force these into Drawer/Sheet just because the old product did.

### Decision pages
Use when the user must compare expected effect, risk and constraints:
- Opportunity
- Optimization plan
- Control approval
- Savings verification

The primary hierarchy is evidence → decision → action → verification.

## 9. AI placement

AI is a horizontal capability, not a top-level information architecture category.

AI can appear as:
- global command / copilot entry,
- diagnosis assistant,
- energy interpretation,
- opportunity explanation,
- work-order summarization,
- optimization proposal drafting.

AI outputs must always remain distinguishable from authoritative facts and deterministic calculations.

## 10. Vocabulary decisions

Use operator language, not technical implementation names.

- `FDD` → `诊断` in navigation and page language.
- `Realtime / Monitor` → `系统运行`.
- `Optimize` → `节能机会` or `优化方案` depending on user task.
- `Cost / Forecast / Settlement` are analysis capabilities inside Energy/Reports unless a future product requirement proves they deserve durable independent jobs.
- `AI` is not a page category by default.

## 11. Visual and component direction

The IA does not prescribe visual geometry, but the selected implementation system remains:

- shadcn/ui application primitives,
- shadcn-admin as application-shell reference only,
- Tailwind CSS,
- TanStack Router / Query / Table,
- ECharts for analytical charts,
- X6 only where engineering topology truly requires a graph canvas,
- Lucide icons.

Avoid defaulting to Card walls. Use structure, typography, dividers, table density and meaningful whitespace before adding containers.

## 12. Definition of done for a surface

A surface is not ready for implementation until its brief defines:

1. Primary user job.
2. Questions the page must answer.
3. Facts and owners required.
4. Information hierarchy.
5. Primary and secondary actions.
6. Durable URL/search state.
7. Interaction boundaries: inline, inspector, dialog, route.
8. Empty/loading/error/permission semantics.
9. Component mapping.
10. Browser review criteria.

A surface is not accepted after implementation until it is reviewed in a real browser at desktop and narrow widths with real/fixed certification data and no invented business facts.
