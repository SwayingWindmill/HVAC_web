# Surface 06–08 Visual Redesign and Acceptance — 2026-09-17

**Status: IMPLEMENTATION READY / BROWSER REVIEW PASSED / PROMOTED**

This document records the final visual reframe and acceptance evidence for:

- 06 — 设备中心
- 07 — 设备详情
- 08 — 舒适与室内环境

The accepted implementation is based on the current Surface Specifications and the shared shadcn application shell. It does not use the former Ant Design / ProComponents page anatomy as a visual source.

---

# 1. Shared decisions

## 1.1 Shell ownership

Surface identity belongs to the shared `AppHeader`.

Rules accepted for these Surfaces:

- list/workspace Surfaces do not repeat the Surface title inside main content;
- detail Surfaces use the entity name as the main content heading;
- shared Site Context remains in `SurfaceContextBar`;
- Sidebar and AppHeader remain the same shell used by the accepted 03 / 04 Surfaces.

## 1.2 Shared compositions

Only two new cross-Surface compositions were introduced:

- `SurfaceFactStrip`
- `IndependentStateStrip`

They compose existing shadcn primitives and do not create a second component system.

## 1.3 Visual hierarchy

Accepted hierarchy:

1. Surface / entity identity;
2. compact operational facts;
3. one dominant work area;
4. lower-weight evidence, metadata and professional exits.

Rejected hierarchy:

- card walls;
- equal-weight KPI modules;
- repeated titles;
- success-colored normal states;
- duplicate actions in both header and side panels;
- explanatory design prose in operator UI.

---

# 2. Surface 06 — 设备中心

## Primary task

Scan, filter and investigate the current device population.

## Accepted composition

- compact `SurfaceFactStrip` for total / online / attention / data issues;
- one dominant Device Ledger;
- URL-backed filters for hierarchy scope, device type, connection, running and data state;
- explicit reset action;
- compact row-level `当前事项` rather than another summary module;
- lightweight contextual Inspector;
- durable Device Detail as the only full investigation Surface.

## Inspector responsibility

The Inspector is intentionally not a miniature detail page.

It contains only:

- device identity and code;
- running / connection / freshness / quality;
- object context;
- up to four current key values;
- current attention reason;
- one `查看设备详情` exit.

## Accepted table columns

`设备 / 对象与位置 / 运行 / 连接 / 数据 / 关键值 / 当前事项 / 更新`

Normal states use neutral text. Warning / destructive color is reserved for actual attention states.

---

# 3. Surface 07 — 设备详情

## Primary task

Investigate one equipment entity using current facts, evidence, relationships and authoritative professional exits.

## Accepted composition

- entity name is the only main-content heading;
- shared `IndependentStateStrip` for running, connection, freshness and quality;
- one dominant `当前运行` semantic section;
- each current fact preserves its own sampled time and quality state;
- `当前注意事项` uses only the operational projection;
- recent observations and engineering points remain in the primary investigation column;
- relationships, stable metadata and professional exits form the lower-weight side column;
- trend / alarm / work-order actions exist only in `专业出口`, not duplicated in the entity header.

## Explicitly rejected

- synthetic health score;
- 8 equal KPI cards;
- duplicate professional actions;
- interpreting point writability as current control authority;
- ACK-like or status shortcuts that imply verified control success.

---

# 4. Surface 08 — 舒适与室内环境

## Primary task

Inspect current space environmental measurements and their data credibility without fabricating comfort or IAQ conclusions.

## Accepted composition

- Surface title appears once in `AppHeader`;
- capability-boundary Alert appears before interpretation;
- six compact environmental facts remain subordinate to the Zone Ledger;
- Zone Ledger is the dominant workspace;
- URL-backed search, area and data-state filters plus reset;
- tabs expose `待核查`, `热环境`, and visible-but-disabled `空气质量 · 未接入`;
- multi-sensor temperature observations remain separate and are never averaged into a fabricated room value.

## Accepted ledger columns

`空间 / 所属区域 / 温度 / 湿度 / 占用上下文 / 运营目标 / IAQ / 关联设备 / 数据状态 / 最近更新`

## Capability boundary

When occupancy context, comfort target policy or IAQ measurements are unavailable, the UI states that explicitly. Temperature / humidity observations are not promoted into ASHRAE 55 / 62.1 compliance or health claims.

---

# 5. Browser and visual acceptance evidence

## Surface 06 / 07

`scripts/run-assets-workspace-browser-review.mjs` passed after the final implementation.

Desktop evidence at 1672×941:

- shared AppShell geometry: Sidebar 240 px, Header 56 px;
- Surface 06 context begins at 88 px;
- facts occupy 77 px and end at 221 px;
- Device Ledger begins at 241 px;
- 15 scan rows remain visible in the working ledger;
- five shadcn Select filters + one InputGroup;
- one primary Card on Surface 06;
- zero Ant runtime DOM;
- no page-level horizontal overflow.

Surface 07 desktop:

- entity identity starts at 144 px and ends at 299 px;
- primary investigation workspace begins at 319 px;
- one H1;
- engineering catalogue remains four registered points in the fixture;
- zero Ant runtime DOM;
- no page-level horizontal overflow.

Surface 07 768×900 responsive evidence was added during final review:

- no horizontal overflow;
- state strip remains inside the first viewport;
- investigation workspace starts at 419 px;
- primary and side columns both resolve to 472 px and stack vertically without overlap;
- main investigation column precedes relationship / metadata / professional exits;
- one H1 and zero Ant runtime DOM.

Surface 06 at 768×900:

- facts collapse to 152 px;
- Device Ledger begins at 360 px after resetting scroll to the page top;
- table retains 15 rows and no page-level horizontal overflow.

Captured evidence:

- `out/assets-workspace-review/assets-ledger-desktop.png`
- `out/assets-workspace-review/assets-inspector-desktop.png`
- `out/assets-workspace-review/asset-device-detail-desktop.png`
- `out/assets-workspace-review/asset-device-detail-evidence-desktop.png`
- `out/assets-workspace-review/asset-device-detail-narrow.png`
- `out/assets-workspace-review/assets-ledger-narrow.png`

## Surface 08

`scripts/run-comfort-browser-review.mjs` passed after the final implementation.

Desktop evidence at 1672×941:

- shared AppShell geometry: Sidebar 240 px, Header 56 px;
- exactly one Surface H1 and it belongs to the AppHeader identity;
- environmental fact strip begins at 232 px and is 77 px high;
- Zone Ledger begins at 329 px;
- two shadcn Select filters + one InputGroup + one Tabs control;
- one primary Card;
- eight fixture spaces remain visible;
- zero Ant runtime DOM;
- no page-level horizontal overflow.

At 768×900:

- Zone Ledger begins at 536 px;
- eight rows remain present;
- no page-level horizontal overflow;
- zero Ant runtime DOM.

The IAQ capability is visible but disabled when the data contract is unavailable.

Captured evidence:

- `out/comfort-review/comfort-ledger-desktop.png`
- `out/comfort-review/comfort-inspector-desktop.png`
- `out/comfort-review/comfort-iaq-disabled.png`
- `out/comfort-review/comfort-ledger-narrow.png`

---

# 6. Final gates

The accepted implementation passed:

- `npm run typecheck:web`
- `npm run assets:browser:review`
- `npm run comfort:browser:review`
- `npm run web:design:changed` with 0 findings / 0 advisory
- `npm run build`

The production build transformed 3980 modules and completed successfully.

---

# 7. Promotion decision

Surface 06, 07 and 08 are promoted into `WIREFRAME_READY_SURFACE_IDS` on 2026-09-17.

Surface 07 remains `navigation: hidden`, so promotion does not create a duplicate Sidebar entry. It becomes available only through its entity route.

The accepted READY set now includes:

`03, 04, 06, 07, 08, 09, 14, 25`.
