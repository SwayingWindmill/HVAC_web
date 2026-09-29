# 09 效率分析 — Surface Brief

**Status: DESIGN REVIEW**  
**Route intent:** `/sites/$siteId/efficiency`

**Review evidence:** `scripts/fixtures/remaining-pages-review` contains the new engineering-focused Efficiency Analysis design review surface. It is browser-rendered for metric hierarchy and interaction review, but is not yet the production Efficiency surface or production-data integration.

## User job
Explain how efficiently the HVAC system and major equipment are operating, distinguish true inefficiency from load/context effects, and identify where engineering investigation should continue.

## Questions
- What is current and historical system efficiency?
- Which subsystem or equipment is driving degradation?
- Is the change explained by load, weather, schedule or operating mode?
- Are key engineering indicators such as COP, kW/RT, ΔT, pump transport efficiency or tower approach within expected context?
- Which periods deserve deeper investigation?

## Information hierarchy
```text
Time / operating-context controls
System efficiency facts
Primary efficiency trend
Subsystem comparison
  plant / distribution / terminal where data exists
Equipment ranking and outliers
Engineering relationships
  load vs efficiency / ΔT / approach / flow-pressure relationships
Related alarms / control changes / opportunities
```

## Interaction model
- Efficiency metrics are shown only when their required inputs are valid and comparable.
- Missing prerequisites produce “unavailable / insufficient data”, not zero.
- Users can select an outlier and open Device Detail, Trend Analysis or Savings Opportunity with the same time window.
- Weather/load normalization is labeled when used; raw and normalized values are not mixed silently.

## Component mapping
ECharts scatter/line/bar for engineering relationships, TanStack Table for equipment ranking, compact facts band for headline metrics, Select/Combobox for system/equipment class filters.

## URL state
Time range, system/subsystem, metric, normalization mode and selected equipment are durable search state.

## Browser acceptance
- The page reads as an engineering analysis tool, not a generic BI dashboard.
- Units and denominator definitions are explicit.
- Outliers can be traced to evidence and device context.
- No “efficiency score” is fabricated from unrelated facts.
