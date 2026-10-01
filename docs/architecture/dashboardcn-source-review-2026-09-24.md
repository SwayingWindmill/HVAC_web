# dashboardcn Source Review — 2026-09-24

> **Status:** SELECTED / ANALYTICS-COMPONENT SOURCE
> **Scope:** `apps/hvac-web`
> **Role:** optional shadcn-compatible dashboard/data-visualization source; not a primitive framework, product template, or operational-ledger owner

## 1. Decision

dashboardcn is approved as a **copy-and-own analytics component / composed dashboard-pattern source** above shadcn/ui.

It extends the existing source model without changing ownership:

```text
shadcn/ui
  = primitive / token / accessibility authority

tablecn + project TanStack Table v9 layer
  = operational ledger / DataTable authority

ReUI + Kibo UI + Dice UI
  = advanced application interaction sources

dashboardcn
  = KPI / metric / dashboard visualization / compact analytics composition source

Shadcnblocks
  = broader application blocks / composed page-section source

project domain components and blocks
  = HVAC / energy semantics and stable product compositions
```

dashboardcn does **not** become a second visual system. The project keeps the current `radix-nova` shadcn base, project chart tokens, tablecn ledger grammar, and existing Recharts/ECharts capability boundary.

## 2. Sources reviewed

Reviewed on 2026-09-24:

- https://dashboardcn.com/docs
- https://dashboardcn.com/docs/installation
- https://dashboardcn.com/docs/components
- https://dashboardcn.com/docs/blocks
- https://dashboardcn.com/docs/components/heatmap-chart
- https://dashboardcn.com/docs/components/sankey-chart
- https://dashboardcn.com/docs/components/timeline
- https://github.com/NoahGdev/dashboardcn
- https://ui.shadcn.com/docs/directory
- https://ui.shadcn.com/docs/registry/registry-index
- https://ui.shadcn.com/r/registries.json
- current registry payloads inspected through project shadcn CLI for:
  - `@dashboardcn/kpi-card`
  - `@dashboardcn/heatmap-chart`
  - `@dashboardcn/sankey-chart`
  - `@dashboardcn/timeline`

The official shadcn Registry Directory currently lists:

```text
@dashboardcn
https://dashboardcn.com/r/{name}.json
```

The project CLI verified the namespace successfully with shadcn `4.21.0`:

```bash
npm run ui:shadcn -- search @dashboardcn --limit 8
```

Result on 2026-09-24: 54 registry items were discoverable and the first page included format helpers, Sparkline, KPI Card, Bar List, Distribution Bar, Funnel Chart, Activity Heatmap and Trend Chart.

No dashboardcn production component is copied by this review alone. Before a concrete production adoption, inspect and record the exact selected upstream source/tag/commit or immutable source snapshot as required by the repository source-first rule.

## 3. Compatibility evidence

dashboardcn documents and exposes the same foundations already selected by this project:

- Tailwind CSS v4;
- shadcn/ui theme variables and primitives;
- Recharts through shadcn Chart for ordinary dashboard charts;
- TanStack Table v9 for its Data Table;
- copy-and-own registry distribution rather than a runtime component package;
- compatibility with the shadcn primitive flavor already selected by the target project.

The inspected registry payloads also showed direct use of project-compatible primitives such as `Card`, `Tooltip`, shadcn Chart composition, Lucide and Recharts.

This is a good technical fit for **ordinary application analytics**, but source compatibility does not override product semantics or the project's existing ownership boundaries.

## 4. ADOPT

Prefer dashboardcn as a candidate source when a dashboard/analytics task needs one of these patterns and the project does not already have an equivalent:

- Sparkline;
- Metric Value / numeric formatting presentation;
- KPI Card when the metric, comparison and trend are all authoritative;
- Trend / Bar / Composed chart composition;
- Heatmap for two-dimensional intensity data such as weekday × hour;
- Radial Gauge / Segmented Meter when the measured quantity genuinely maps to a bounded range or engineering threshold;
- Sankey for real flow/allocation relationships;
- Timeline for chronological audit/activity evidence;
- compact ranked/distribution patterns;
- composed analytics cards when their information hierarchy matches the Surface Specification.

Likely HVAC/energy use cases include:

```text
Heatmap
→ hourly × weekday load / alarm density / comfort deviation

Sankey
→ energy-flow or allocation relationships when conservation/flow semantics are real

Timeline
→ alarm → diagnosis → action → verification chronology
→ control execution / approval / audit history

Sparkline / Trend
→ ordinary low-density application trends

Gauge / Segmented Meter
→ bounded utilization, threshold bands, or engineering range only

KPI composition
→ authoritative current fact + meaningful comparison + real trend
```

## 5. ADAPT

### 5.1 Project chart boundary still wins

dashboardcn uses Recharts/shadcn Chart and is appropriate for ordinary application analytics.

It does not replace Apache ECharts for:

- high-density HVAC time series;
- linked cursors;
- multi-axis engineering analysis;
- dataZoom / brush;
- large-series interaction;
- other engineering-analysis behavior already assigned to ECharts.

### 5.2 Project visual tokens still win

Adopted sources must use the project's existing shadcn semantic/chart tokens.

Do not copy dashboardcn example colors, gradient choices, business copy, card density or example KPI semantics when they conflict with `DESIGN.md` or the target Surface Specification.

### 5.3 KPI semantics require domain review

The inspected KPI Card includes delta direction and optional Sparkline behavior. Those mechanics are useful, but a percentage increase is not inherently "good" in HVAC.

For example:

- higher energy use may be bad;
- higher COP may be good;
- higher alarm count may be bad;
- a flat metric may still violate a threshold.

Feature code must own domain meaning. Generic dashboardcn direction/color semantics must be adapted or removed when they would imply the wrong operational meaning.

### 5.4 Formatting requires localization review

The inspected `format` helper defaults to `en-US` and USD-oriented examples.

The HVAC product is Chinese-first and engineering-unit-heavy, so production adoption must explicitly handle:

- locale;
- engineering units;
- currency where applicable;
- precision;
- compact-number behavior;
- tooltip/full-value behavior.

Do not silently inherit demo defaults.

## 6. REJECT

Reject dashboardcn for the following responsibilities:

- **ordinary operational Data Table / ledger** — project tablecn grammar + TanStack Table v9 remains authoritative;
- application shell / navigation IA;
- generic Button / Select / Dialog / Sheet primitive replacement;
- engineering-grade high-density time-series analysis already assigned to ECharts;
- page templates that create a generic "KPI cards + charts" dashboard regardless of business task;
- fake trends, opaque health scores, decorative gauges or charts without authoritative data;
- duplicate implementations of existing project/domain components;
- source copied from an unreviewed moving branch without recording the exact selected source for production adoption.

The dashboardcn Data Table may be inspected as comparative source evidence because it is TanStack Table v9-based, but it is **not an alternate project ledger implementation**.

## 7. Relationship with Shadcnblocks

The sources have different preferred roles:

```text
dashboardcn
→ focused dashboard metrics / charts / analytics compositions

Shadcnblocks
→ broader application blocks and page-section composition

ReUI / Kibo UI / Dice UI
→ advanced interaction mechanics

tablecn
→ operational ledger grammar
```

When two sources overlap, select a single implementation based on current task semantics, accessibility, dependency weight, Radix compatibility, source quality and fit with project state ownership.

Do not retain two generic Timeline, Gauge, KPI, Kanban, Gantt or Table implementations for the same project responsibility.

## 8. Adoption workflow

For each dashboardcn production item:

1. identify the exact required capability from the Surface Specification;
2. confirm no existing project/shadcn component already owns it;
3. run `npm run ui:shadcn -- view @dashboardcn/<item>`;
4. inspect every file, npm dependency and shadcn registry dependency;
5. record the exact upstream source/tag/commit or immutable reviewed source;
6. compare overlapping ReUI/Kibo/Dice/Shadcnblocks/project implementations;
7. copy only the selected source and required dependencies;
8. adapt tokens, localization, accessibility and HVAC semantics;
9. remove any competing project implementation rather than adding a compatibility wrapper;
10. run the narrowest relevant design/type/browser checks.

## 9. Selection boundary

dashboardcn participates in the project's source-selection process; it is not behind the existing project implementation in a fixed reuse ladder.

```text
Surface Spec + business task + accessibility semantics
↓
mature product patterns
↓
compare candidate sources in parallel
  existing project / shadcn / tablecn / domain components
  ReUI / Kibo UI / Dice UI / dashboardcn / Shadcnblocks
  other reviewed mature OSS when necessary
↓
ADOPT / ADAPT / REPLACE / REJECT
↓
one canonical project implementation
```

Existing project components have no grandfathered preference. A weaker or semantically incorrect local implementation should be replaced when a reviewed mature source provides a better task model.

## 10. Adopted production source — Timeline

The Issues / Alarm Diagnosis Workspace adopted `@dashboardcn/timeline` on 2026-09-24.

Reviewed installation path:

```text
registry namespace: @dashboardcn
registry item: timeline
registry URL contract: https://dashboardcn.com/r/{name}.json
project CLI: shadcn 4.21.0
command: npm run ui:shadcn -- add @dashboardcn/timeline --dry-run
installed target: apps/hvac-web/src/components/ui/timeline.tsx
local reviewed SHA-256: f7409c06981886db4a72bf360fd827f35a6c69cf47bd0a53fe776739c8e11d98
```

The dry run proposed exactly one source file and no npm-package dependency or existing primitive overwrite. The copied Timeline remains generic and business-free; Alarm lifecycle semantics stay in the feature.

Selection rationale:

- ADOPT dashboardcn Timeline for immutable chronological Alarm state/handling evidence;
- REJECT ReUI Timeline for this task because its richer step/progress semantics are unnecessary for an audit chronology;
- REJECT a new project-owned timeline because the registry source already supplies the required accessible ordered-list composition;
- do not use the component to imply workflow completion state: Alarm physical condition, ACK, assignment and suppression remain independent business facts.

The source is used by `AlarmCenterWorkbench` only as layout/interaction grammar. Event labels, timestamps, actor/assignee facts and physical-state semantics remain owned by the Alarm contract.

## 11. Adapted production sources — Heatmap Chart and Bar List

The Issues Workspace adopted the interaction grammar of `@dashboardcn/heatmap-chart` and `@dashboardcn/bar-list` on 2026-09-24 for Alarm Performance.

Reviewed source path:

```text
registry namespace: @dashboardcn
items: heatmap-chart, bar-list
registry URL contract: https://dashboardcn.com/r/{name}.json
project CLI: shadcn 4.21.0
review: npm run ui:shadcn -- view @dashboardcn/heatmap-chart
review: npm run ui:shadcn -- view @dashboardcn/bar-list
dry run: npm run ui:shadcn -- add @dashboardcn/heatmap-chart @dashboardcn/bar-list --dry-run
```

The dry run proposed two new UI files, but also proposed overwriting the project's existing `src/lib/format.ts` and `src/components/ui/tooltip.tsx`, plus adding an unnecessary `cn` package dependency. Direct installation was therefore rejected.

Decision: **ADAPT / copy-and-own**.

Project targets:

```text
apps/hvac-web/src/components/ui/heatmap-chart.tsx
apps/hvac-web/src/components/ui/bar-list.tsx
```

Adaptations:

- reuse the reviewed dashboardcn matrix-heatmap and proportional-ranked-list interaction models;
- depend on the project's existing shadcn Tooltip and `@/lib/utils`;
- use project chart tokens rather than introducing a second theme layer;
- use `zh-CN` number formatting and HVAC/Alarm wording;
- remove dashboardcn's generic format dependency;
- do not overwrite any existing primitive;
- keep Alarm thresholds, Flood definitions, recurrence semantics and impact calculations outside the UI component.

Selected use in the Issues Workspace:

- Heatmap Chart → weekday × time Alarm trigger density;
- Bar List → high-frequency contributing equipment and recurring issue classes.

The components are intentionally generic. Business data remains authoritative only when supplied by the Issues performance contract; the components must never infer energy waste, root cause, alarm flood or recurrence from frontend-only heuristics.
