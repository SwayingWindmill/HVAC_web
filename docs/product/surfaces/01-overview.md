# 01 运营总览 — Surface Brief

**Status: READY FOR WIREFRAME / IMPLEMENTATION**  
**Route intent:** `/sites/$siteId/overview`

## User job

In 30–60 seconds, decide whether the site is stable, what deserves attention first, and which workflow to enter next.

## Questions this page must answer

1. Is HVAC operation broadly stable now?
2. What are the highest-priority active problems or unowned tasks?
3. Is current energy/efficiency performance materially off expectation?
4. Are there credible savings opportunities worth reviewing?
5. What changed recently that explains the current state?

## Explicit non-goals

- Not a full telemetry dashboard.
- Not a KPI card wall.
- Not a replacement for Alarm, Work Order, Energy or System Operation.
- Not a place for fabricated “health scores”.
- Not a big-screen visualization.

## Information hierarchy

```text
Page intro + site/time context

Operational status strip
  Active systems | Active critical alarms | Unowned work | Data confidence

Priority work
  ordered list of items requiring attention
  each row: what / impact / age / responsibility / next action

Main evidence row
  System operation summary        Energy & efficiency summary

Savings opportunities
  small ranked list with evidence-backed expected benefit

Recent meaningful changes
  alarm / work-order / control / strategy / data events
```

## Wireframe

```text
┌──────────────────────────────────────────────────────────────────────┐
│ 运营总览                                      [time context] [刷新] │
│ 当前站点需要关注什么，以及下一步进入哪个工作流                     │
├──────────────────────────────────────────────────────────────────────┤
│ 运行系统 4/5 │ 紧急告警 2 │ 未指派工单 3 │ 数据可信度 98%          │
├───────────────────────────────────────┬──────────────────────────────┤
│ 优先处理                              │ 当前运行                      │
│ 1 供水温度持续偏高   CRITICAL  18m    │ 冷源 / 输配 / 末端摘要        │
│   影响: 末端舒适性   [查看告警]       │ 关键异常关系                  │
│ 2 工单 WO-... 临近 SLA ...            │ [进入系统运行]                │
│ 3 ...                                 ├──────────────────────────────┤
│                                       │ 能源与效率                    │
│                                       │ 今日用电 / 峰值 / COP / 基线 │
│                                       │ [进入能源] [进入效率]         │
├───────────────────────────────────────┴──────────────────────────────┤
│ 节能机会                                                            │
│ 冷冻水温度重置 | 预计 92 kWh/日 | 风险低 | [查看机会]               │
├──────────────────────────────────────────────────────────────────────┤
│ 最近变化  · 告警恢复 · 工单指派 · 控制执行 · 数据质量变化           │
└──────────────────────────────────────────────────────────────────────┘
```

## Fact ownership

- System operation: current snapshot/stream owners.
- Alarms: Alarm service authoritative facts.
- Work ownership/SLA: Work Order service.
- Energy and efficiency: energy facts / deterministic calculations.
- Opportunity benefit: optimization/opportunity owner only; never infer in the UI.
- Data confidence: only if an authoritative readiness/quality owner exists. Otherwise omit the metric.

## Primary actions

- Open highest-priority item.
- Enter System Operation.
- Enter Alarm / Work Order / Energy / Efficiency / Opportunity with preserved source context.

No write action belongs directly on Overview unless it is a universally safe, well-scoped action backed by the owning domain. Default is navigation, not mutation.

## Component mapping

- Page intro: plain semantic header, no enclosing Card.
- Status strip: one bordered facts band, not 4–6 independent Cards.
- Priority work: compact list/table-like rows; Badge for severity/state.
- Main evidence: two purposeful Cards or sections, not nested cards.
- Opportunities: compact ranked list.
- Recent changes: chronological list; avoid a decorative Timeline if plain rows scan better.
- Buttons/links: shadcn Button variants; Lucide icons only where useful.

## URL/search state

- `siteId` in route.
- Optional time context only when user changes it; default “now/current business day” should not create noisy search params.
- Deep links carry `source=overview` only if downstream workflow benefits from it; never show source IDs to users.

## States

- Loading: preserve page skeleton hierarchy, not dozens of skeleton cards.
- Partial data: each domain section can state unavailable independently; one failing domain must not turn every fact into “normal”.
- Permission denied: omit or replace only the inaccessible section with a concise permission state.
- Empty priority list: “当前没有需要立即处理的事项” only when authoritative inputs were successfully read.

## Browser acceptance

- At 1440–1720px, the first viewport exposes priority work plus at least one operational/energy evidence section.
- No card wall feeling.
- Priority items visually dominate normal background facts.
- No UUID/trace/internal revision text.
- No fake trend arrows, percentages or scores.
- Narrow width stacks content without converting everything into separate Cards.
