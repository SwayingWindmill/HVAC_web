# Handoff

The current state of the work and what to pick up next. Update it in the same PR that changes
what it says. How to build, test and run lives in `CLAUDE.md` / `AGENTS.md`; what changed for
users lives in `CHANGELOG.md`.

Last updated: 2026-10-12.

## Where the product is

These pages run on real owner data in the local stack (`hvac-local`, https://localhost:8443):

| Area | Page | Data owner |
| --- | --- | --- |
| 总览 | `/overview` | energy series, realtime plant, alarms, FDD, work orders, optimization, tariff |
| 监测 | 实时运行, 设备 | telemetry snapshots/push, Registry asset model, device history |
| 能源 | 能耗分析, 需量分析 | analytics energy series, device-series quarter-hour aggregates, Site tariff |
| 能源 | 负荷预测, 优化建议 | forecast service, optimization service |
| 运维 | 告警, 故障诊断, 工单, 控制指令 | alarm, FDD (scheduled every 15 min), work orders, command governance |
| 平台 | 通知, 集成管理, 系统管理 | notification inbox, gateway credentials, IAM/Registry/rules/audit |

Costs are estimates at the Site's time-of-use tariff and are labelled as such; Settlement
(`services/settlement-service`, a CLI) is still the authority on the bill and has no HTTP surface.

## Design system

- Desktop only. Verify at 1440px; no mobile or responsive work (decision of 2026-10-12).
- Visual language follows three shadcn admin templates. What was adopted, adapted or rejected:
  `docs/architecture/shadcn-admin-visual-language-source-review-2026-10-12.md`. Product-wide
  rules: `DESIGN.md`.
- Building blocks every page uses:
  - `Main`: page padding and rhythm.
  - `PageHeader` + `PageMeta`.
  - `MetricGrid` / `MetricCard`: the one KPI strip.
  - `FactList` / `FactRow`: label–value rows.
  - `DataTable` with `TableEmpty`.
  - `Status` / `StatusBadge`: tinted label with a still dot.
  - `MetricTabsChartCard`.
- Do not add another KPI widget or status pill style; extend these.

## Next up

Roadmap issues, in order:

1. **#481 — breakdown/Sankey.** Energy by system: chiller, pumps and towers.
2. **#483 — reports.** Daily, monthly and export.
3. **#484 — measured savings (M&V).** Needs a baseline model; the overview and optimization pages already say measured savings are not yet available.
4. **#485 — benchmarking.**

Smaller follow-ups:

- Move the FDD low-ΔT threshold from the schedule payload into an owner-held rule definition.
- Polish the device inspector and the work-order sheet in the same visual system.
- The energy cost Metric (`energy_cost`) prices a day at the tariff's time-weighted average
  rate; make Metrics read the Tariff so the optimizer's cost is time-of-use.

## Local environment notes

These are drifts from the seed in the current local database. A `npm run local:reset` followed by `local:up` removes all of them.

- The optimization policy is at v2 (v1 superseded by hand).
- The `forecast_models` horizon was raised to 2880 min by hand.
- The ClickHouse user `fdd_service_reader` was created by hand.
- The tariff name was updated to 两部制分时电价 by hand.

Known quirks:

- **Flaky Go proxy.** `goproxy.cn` sometimes drops module downloads. Run `local:up` with `PHASE1_GO_PROXY="https://goproxy.cn|https://goproxy.io|https://proxy.golang.org,direct"` and re-run on a network error.
- **e2e flake under load.** A full `npm run web:e2e` occasionally times out one spec. Re-run that spec on its own before treating it as a failure.
- **Integration tests only run when a module changes.** Integration scripts run only when a PR touches their module, so a migration that breaks another module's seed shows up later. Example: the Registry baseline seeding FDD findings, fixed in #504.

## Process

- Branch per change and open a PR with the template in the `mattpocock-skills:pr` skill. Merge once CI is green; Claude's own PRs may be merged without asking.
- Verify UI against real data on 8443. Screenshot scripts use `scripts/lib/local-administrator-browser.mjs`.
- Add a `CHANGELOG.md` entry for anything a user would notice, and update this file when the next-up list or the local notes change.
