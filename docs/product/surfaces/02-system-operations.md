# 02 系统运行 — Surface Brief

**Status: READY FOR WIREFRAME**  
**Route intent:** `/sites/$siteId/operations`

## User job
Understand how the HVAC system is operating now, identify abnormal equipment/parameters/relationships, and move into evidence or action without losing context.

## Questions
- Which systems are running and in what mode?
- What equipment is active, stopped, unavailable or unknown?
- Which current parameters are abnormal or inconsistent?
- Where is the problem located in the physical/functional chain?
- What evidence should be investigated next?

## Information hierarchy
```text
Context: site / system / time-now
System selector: 冷源 | 输配 | 末端
Primary engineering canvas
  topology / grouped equipment / current key facts
Context Inspector
  selected object identity
  runtime / connectivity / freshness / current values
  alarms / diagnosis / related work
Supporting evidence
  key trends / recent events / efficiency context
```

## Interaction model
- Selecting equipment changes the Inspector; it does not navigate immediately.
- Durable investigation uses Device Detail or Trend Analysis routes.
- Topology is used only when relationships materially help the task; do not draw decorative process diagrams.
- Never invent flow allocation, causal arrows or running relationships from missing data.

## Component mapping
- Tabs for system mode when peers are mutually exclusive.
- X6 only for genuine fixed engineering topology requiring zoom/pan/selection.
- Plain grouped sections/Table when a graph adds no task value.
- Context Inspector as same-page aside on desktop; responsive stacked detail on narrow widths.
- Badge only for real semantic state.

## URL state
`system`, selected object and meaningful sub-view may live in search params when shareable. Hover/temporary UI state stays local.

## Browser acceptance
- Current system state is understandable without opening a second page.
- Independent runtime/connectivity/freshness facts are not merged into “health”.
- Inspector preserves list/canvas spatial memory.
- No fake animation implying flow or command success.
