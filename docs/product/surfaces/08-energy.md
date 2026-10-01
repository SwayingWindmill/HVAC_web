# 08 能源分析 — Surface Brief

**Status: DESIGN REVIEW**  
**Route intent:** `/sites/$siteId/energy`

**Review evidence:** `scripts/fixtures/remaining-pages-review` contains the new continuous Energy Analysis design review surface. It is browser-rendered for information architecture and interaction review, but is not yet the production Energy surface or production-data integration.

## User job
Explain where and when energy is used, identify material deviations, and drill from aggregate consumption into operational causes.

## Questions
- How much energy is being used in the selected period?
- Which systems, spaces or equipment account for the change?
- When did abnormal load occur?
- How does actual compare with baseline/budget/previous period when those references exist?
- Which operational events coincide with the deviation?

## Information hierarchy
```text
Time / comparison / energy-type context
Headline consumption + demand facts
Primary load profile
Breakdown by system / space / equipment
Variance / anomaly periods
Related operation, alarm, control and work events
Drill path to device / efficiency / opportunity
```

## Interaction model
- One continuous analysis workspace instead of separate year/month/week/day pages.
- Time grain adapts to selected range.
- Drill-down changes analytical context while preserving selected period/comparison.
- Water, gas, cooling or cost remain unavailable unless authoritative data is connected.
- Baseline and forecast are separate named references, never disguised as actuals.

## Component mapping
ECharts for time series, stacked bars/area and breakdowns; TanStack Table for ranked contributors; shadcn Tabs only for true peer measures; Select/Calendar/Date-range controls for context.

## URL state
Time range, comparison reference, energy type, grouping dimension and selected contributor are durable search state.

## Browser acceptance
- User can trace an aggregate anomaly to a lower-level contributor without resetting context.
- Chart legends, axes and units remain readable at normal laptop width.
- “Actual / baseline / forecast” are visually and semantically distinct.
- No fabricated cost/carbon/savings values.
