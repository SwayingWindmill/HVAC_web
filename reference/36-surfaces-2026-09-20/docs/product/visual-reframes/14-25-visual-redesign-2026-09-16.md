# Surface 14 / 25 Visual Reframe — 2026-09-16

**Status: IMPLEMENTATION READY / BROWSER REVIEW PASSED**

This reframe is derived only from the current authority chain:

1. `PRODUCT.md`
2. `docs/product/smart-energy-system-page-architecture-v2.md`
3. `docs/product/global-navigation-context-interaction-contract-v1.md`
4. `docs/product/surface-specifications/14-energy-analysis.md`
5. `docs/product/surface-specifications/25-control-center.md`
6. `docs/design-system/shadcn-redesign-2026-09-13.md`
7. `DESIGN.md`
8. `docs/design-system/shadcn-component-contract.md`
9. `docs/design-system/legacy-ui-quarantine.md`

The current implementations of Surface 14 and 25, old Ant/Pro pages, historical screenshots, the former UI pattern gallery and historical image-generation packages were **not** used as visual references for this reframe.

---

# 1. Shared visual direction

Both pages must feel like one modern shadcn application, not two dashboards assembled from cards.

Shared rules:

- compact route-owned Page Intro; no boxed hero header;
- ordinary desktop content max-width around 1480–1560 px inside the current shell;
- 24 px desktop outer rhythm, 16 px section rhythm, 8–12 px internal rhythm;
- neutral zinc-like surfaces, subtle borders, minimal elevation;
- semantic color only for actual state or risk;
- Card only for a coherent semantic section;
- border-only groups and definition lists are preferred for dense fact clusters;
- one visually dominant task area per page;
- supporting evidence must be visibly secondary to the dominant task;
- no equal-weight module wall;
- no repeated warning message in multiple boxes;
- no design-principle prose in the business UI;
- no raw schema / owner / read-model / internal identifier language in the default layer;
- desktop visual approval must judge composition, hierarchy, density and balance, not only overflow or DOM correctness.

A Surface is not visually complete merely because it passes TypeScript, request discipline, responsive or accessibility gates.

---

# 2. Surface 14 — 能源分析

## 2.1 User question

The default page must help the user answer, in this order:

1. 这个周期实际用了多少能源？
2. 与明确命名的比较周期相比差多少？
3. 差异发生在什么时候？
4. 哪些贡献来源可以被事实支持？
5. 数据覆盖和计量边界是否足以支持结论？
6. 如果要解释“为什么”，下一步去哪里？

The visual hierarchy must therefore be **analysis-first**, not dashboard-first.

## 2.2 Desktop composition

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Page Intro                                               导出分析   刷新      │
│ 能源分析                                                                    │
│ 华东制造基地 · UTC+08:00 · 当前周期 9/1–9/14                              │
├──────────────────────────────────────────────────────────────────────────────┤
│ Context controls: [月] [日期] [电力] [上一完整周期] [质量]     ← 当前 →     │
├──────────────────────────────────────────────────────────────────────────────┤
│ 124.6 MWh             +8.2 MWh (+6.4%)             覆盖 98.7%              │
│ 实际用能              与上一完整周期差异             日区间 · 数据完整       │
│ Comparison definition / data note as one quiet supporting line              │
├──────────────────────────────────────────────────────────────────────────────┤
│ 区间电量曲线 — PRIMARY ANALYSIS                                              │
│                                                                              │
│                     dominant time-series plot                                │
│                                                                              │
│ [variance highlight]                                  [查看区间数据表]        │
├───────────────────────────────────────────────┬──────────────────────────────┤
│ 贡献来源                                      │ 主要差异窗口                 │
│ compact evidence area                         │ ranked variance list         │
│ unavailable => concise evidence note          │ 3–5 rows                    │
├───────────────────────────────────────────────┴──────────────────────────────┤
│ 计量与数据边界 — compact definition row / collapsible professional detail    │
├──────────────────────────────────────────────────────────────────────────────┤
│ “差异回答何时，不自动回答为什么”                              [进入诊断]      │
└──────────────────────────────────────────────────────────────────────────────┘
```

## 2.3 What changes from the rejected composition

### A. Controls are a toolbar, not a Card

Period / anchor / energy type / comparison / quality belong to one horizontal context-control band. It may have a border or muted background, but it should not read as a standalone dashboard card.

### B. Headline facts are not four equal KPI cards

The primary pair is:

- Actual energy
- Named comparison difference

Coverage and interval granularity are supporting confidence facts. They must not receive the same visual weight as actual + comparison merely for symmetry.

Preferred weight:

```text
Actual  ~ 40%
Comparison ~ 35%
Coverage + resolution ~ 25%
```

### C. The primary chart owns the page

The interval profile must be the largest visual object on the first screen. It is the analytical workspace, not another card among many.

The chart title, period, unit and comparison definition must be visible without reading a separate explanation panel.

### D. Contributor unavailability must not create a giant empty card

If authoritative contributors are unavailable:

- use a compact evidence section;
- explain the missing capability in 1–2 concise lines;
- retain enough space for future ranked contributors without leaving a large empty canvas;
- do not manufacture a donut/pie or allocate by rated power.

### E. Variance windows are the main supporting evidence

When comparison is valid, show 3–5 ranked windows with:

- interval;
- current value;
- comparison value;
- absolute difference;
- percentage only when denominator is valid.

This section should visually connect to the highlighted regions of the primary profile.

### F. Professional metadata becomes a quiet detail band

Meter scope, provenance category, data watermark, aggregate watermark, baseline/normalization availability are important, but they are not the primary visual story.

Use a compact definition-list band or collapsible detail. Do not make a large explanatory card wall.

## 2.4 First-screen requirement at 1440–1720 px

Without scrolling, the user should see:

- page title / context controls;
- actual consumption;
- named comparison difference;
- data coverage;
- most of the primary load profile;
- at least the beginning of variance / contributor evidence.

If a desktop screenshot shows only header + controls + summary and pushes the primary chart below the fold, the composition fails.

## 2.5 Forbidden patterns

- 4–6 equal KPI cards;
- a filter Card that visually competes with the chart;
- oversized empty contributor Card;
- generic “数据健康度” score;
- donut/pie when contributor composition is incomplete;
- repeated explanation of `Difference ≠ Savings` in multiple boxes;
- a long professional-metadata Card that dominates the lower half;
- using current implementation geometry as the redesign starting point.

---

# 3. Surface 25 — 控制中心

## 3.1 User question

The default page must help the operator answer:

1. 当前最需要关注的控制事项是什么？
2. 我正在看哪个受控对象？
3. 它现在真实反馈是什么？
4. 当前由谁/什么来源控制，是否存在覆盖？
5. 为什么现在能或不能执行？
6. 哪些前置事实缺失或阻塞？
7. 最近执行是否存在未知、错配或需要复核？

The page is a **control decision workspace**, not an asset-detail page and not a command form.

## 3.2 Desktop composition

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Page Intro                                                   刷新控制状态    │
│ 控制中心                                                                    │
│ 华东制造基地 · 当前控制上下文                                               │
├──────────────────────────────────────────────────────────────────────────────┤
│ ONE safety / availability strip                                              │
│ 当前控制操作暂不可用 · authoritative preflight facts incomplete             │
├──────────────────────────────┬───────────────────────────────────────────────┤
│ Control target ledger        │ Selected target decision workspace            │
│                              │                                               │
│ search / scope               │ 冷机 1 · 冷冻水出水温度设定                  │
│                              │ [已登记] [当前不可执行]                        │
│ object                       │                                               │
│ current readback             │ Current state / source / override / range     │
│ source                       │ compact fact band                              │
│ availability                 │                                               │
│                              │ Why execution is blocked                      │
│ selected row                 │ preflight fact matrix                         │
│                              │ auth / authority / precondition / interlock   │
│                              │ priority / approval / impact / override       │
│                              │                                               │
│                              │ confirmed registration facts — quiet detail   │
├──────────────────────────────┴───────────────────────────────────────────────┤
│ Priority attention                                   Recent relevant execution│
│ overrides / mismatch / incomplete registration       compact ledger          │
├──────────────────────────────────────────────────────────────────────────────┤
│ [查看设备与建筑]                                      [查看执行记录]          │
└──────────────────────────────────────────────────────────────────────────────┘
```

## 3.3 Visual priority

The right-side decision workspace is dominant. The target ledger is a selector and scan surface, not a second dashboard.

Recommended desktop proportion:

```text
Target ledger: ~34–38%
Decision workspace: ~62–66%
```

The two columns should align at the top, but **must not be forced to equal height**.

## 3.4 One safety message, not repeated warnings

The page-level state “control operations unavailable” appears once as the page safety strip.

Inside the selected-target workspace it may appear once more only as a compact status badge. Do not repeat the same statement in:

- summary KPI;
- detail fact cell;
- preflight section;
- bottom Alert;
- footer note.

The preflight section should explain **which facts are unavailable**, not repeat the page-level conclusion.

## 3.5 Target ledger

The default ledger should expose only scan-worthy facts:

- business object / control intent;
- current readback summary;
- current control source when authoritative;
- availability / review state;
- relevant override indicator.

Do not make every row a mini asset-detail record.

Selection is a semantic row/button state and should preserve the user's spatial context.

## 3.6 Selected-target workspace

The target header should answer:

```text
What is this control?
What is the current observed value?
What is the current source / override context?
Can it be executed now?
```

### Current state band

Use a compact fact band, not nested cards:

- current readback;
- sampled time / freshness / quality;
- current mode if authoritative;
- current control source;
- active override summary;
- allowed range / current schedule when authoritative.

Unknown remains visibly `未知` or `暂不可用`.

### Preflight fact matrix

This is the central evidence area for the current read-only state.

Show the eight facts as a disciplined 2×4 or 4×2 matrix depending width:

- 用户授权
- 当前控制权
- 前置条件
- 联锁
- 更高优先级来源
- 审批要求
- 影响范围
- 临时覆盖

Each item contains:

```text
Fact name
Current state
One-line reason / source note when useful
```

No decorative icons on every tile unless they materially improve scanning. Unknown state should be consistent and quiet.

### Confirmed registration facts

Capability, command point, feedback point, device and safe range are supporting technical facts. Place them below the preflight matrix in a compact definition list. They must not compete with current state and execution availability.

## 3.7 Priority attention

The spec prioritizes operational exceptions, not success counts.

The lower supporting area should surface, when present:

1. active override needing review;
2. execution state unknown;
3. readback mismatch;
4. verification failed;
5. expiring override;
6. blocked critical control;
7. incomplete target registration.

This area should be a short ranked list, not KPI cards.

## 3.8 Recent relevant execution

Show only relevant recent executions, with business columns:

- object;
- action;
- requested state/value;
- lifecycle state;
- current readback;
- actor;
- time.

Do not show protocol logs or imply that ACK means verified success.

## 3.9 Forbidden patterns

- repeated “当前不可执行” Alerts;
- four equal headline KPI facts for target count / selected target / feedback / availability;
- treating “已登记” as a success KPI;
- rendering writable/online as controllable;
- start/stop Switch;
- command form before authoritative preflight exists;
- equal-height left/right columns that create empty space;
- asset-detail-field wall that obscures the decision task;
- copying the current ControlCenter JSX structure as the visual starting point.

---

# 4. Visual acceptance gate for the redesign

Surface 14 and 25 were promoted into `WIREFRAME_READY_SURFACE_IDS` on 2026-09-16 after passing all of the following:

## Composition

- one dominant task area is obvious within 2 seconds;
- first-screen information matches the Surface Spec priority;
- no card-wall / module-collage impression;
- supporting sections have visibly lower weight than the primary workspace;
- no awkward equal-height stretching or large empty boxes.

## Design-system fidelity

- current shadcn/ui primitives and Tailwind composition only on the accepted path;
- zero Ant/Pro runtime DOM;
- no legacy PageContainer / StatisticCard / Drawer-Splitter page anatomy;
- neutral surfaces and restrained semantic color;
- Chinese-first content language.

## Content fidelity

- no internal architecture vocabulary in ordinary UI;
- no design-rule prose presented to operators;
- unknown / unavailable states remain explicit;
- no fabricated contributors, control availability, authority, interlock or success state.

## Rendered review

Before READY:

1. render the real page at the approved desktop viewport;
2. compare it against this reframe + the matching current Surface Specification, not against the previous implementation;
3. inspect hierarchy, geometry, density, alignment, whitespace and state emphasis;
4. fix visible defects in one coherent batch;
5. only then run final TypeScript/build/business-state gates and promote the Surface.

## Acceptance evidence — 2026-09-16

- Surface 14: `scripts/run-energy-analysis-browser-review.mjs` passed at 1672×941, 900×900 and 320×900; primary profile begins at 416 px, supporting evidence begins at 909 px, no page overflow and zero Ant DOM.
- Surface 25: `scripts/run-control-center-browser-review.mjs` passed at 1672×941, 900×900 and 320×900; target ledger remains narrower than the decision workspace, the preflight matrix remains the dominant evidence area, no command requests are emitted and zero Ant DOM is rendered.
- Surface 25 final composition moved priority attention and recent execution into one lower supporting band, changed the target status from success-like `完整` to neutral `待确认`, and collapsed registration facts by default.
- Surface 14 review capture now records the default collapsed chart-data state before the accessibility data table is expanded.
- `npm run lint`, `npm run web:design:changed`, and `npm run build` passed after the final implementation changes.
