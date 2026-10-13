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
| 能源 | 能耗分析, 需量分析 | analytics energy series, device-series hourly/quarter-hour aggregates, Site tariff |
| 能源 | 分项能耗 | analytics energy series with `breakdown: "device"`: the total meter (SITE) and each machine's sub-meter (BRANCH) |
| 能源 | 负荷预测, 优化建议 | forecast service (hourly at :07), optimization service (hourly at :20, and on demand; spec `docs/specs/scheduled-optimization.md`) |
| 能源 | 节能项目 (`/energy-analysis/savings`) | mv-service: measures, frozen baselines, verified days (ADR 0021) |
| 能源 | 能源报告 (`/reports`) | closed months from report-service snapshots (`REPORT_GENERATE`, 03:00 on the 1st); the running month and unarchived months assembled on demand; yearly view; printable to A4 |
| 能源 | 站点对标 (platform route `/benchmark`) | each authorized site's energy series; sites without analytics access are listed but not compared |
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
- 系统管理 → 设计系统 (`/system?tab=design`) shows the shipped tokens with their measured contrast, the type steps and component states; `scripts/test-real-design-tokens.mjs` keeps every listed light-theme pairing at its WCAG level.
- `FactTile`'s `className` styles the value, not the grid cell: to span columns, wrap it in a `<div className="col-span-…">`.

## Next up

Roadmap issues, in order:

1. **#484 — measured savings (M&V).** Live and accepted end to end (spec `docs/specs/savings-verification.md`, ADR 0021).
   - Acceptance ran on replayed history: Site A's setpoint was raised to 8 °C on 2026-09-29 09:00; the measure was implemented then and ended 10-06.
   - The baseline used 336/336 hours (CV(RMSE) 0.6 %, NMBE 0.0 %) and passed; six days were verified, saving 367 kWh and ¥347.
   - The result is recorded as `e2e/fixtures/savings-measure-verified.json`.
   - **Weeks of history without the live simulator.** `node scripts/local-history-replay.mjs --from … --to … [--commands <siteCode>=<file>] [--metrics-only]` replays the Sites' meters through Telemetry Runtime's Historical Replay path, on each Site's daily scenario, then recalculates their hourly and daily Metrics.
     - It takes about 10 minutes per Site for three weeks, and a few minutes of Metric recalculation.
     - Replay only windows that end before live history begins, because the counters restart.
     - `tools/eg8200-simulator/configs/replay-setpoint-raise.json` is the command schedule used for acceptance.
   - **Option B (one machine's sub-meter) is live** (spec section 第二版). A measure names its measurement boundary; a device boundary fits the device's `device_hourly_energy` against plant cooling. Measures overlap only on different machines.
     - Accepted on replayed history: Site B's chilled water pump was cut from 50 Hz to 45 Hz on 2026-09-08 09:00 (`tools/eg8200-simulator/configs/replay-chwp-frequency-cut.json`). The baseline used 336/336 hours (the pump's power does not follow cooling, so the line is flat: 45 kWh/h); six days saved 1,646 kWh (27.1 %, as (45/50)³ predicts) and ¥1,256. Recorded as `e2e/fixtures/savings-measure-device-verified.json`.
     - A Site's savings rate is avoided ÷ (plant electricity over the verified hours + avoided), so measures on several machines add up.
   - Later: non-routine adjustments, demand savings, apportioning savings between measures.
   - The local savings ledger was emptied by the #481 reset; the accepted result lives on as the e2e fixture.
2. **#485 — benchmarking, second half.** `/benchmark` ranks sites by plant COP or by electricity per square metre (spec `docs/specs/site-profile-benchmark.md`). A Site's floor area and GB 50176 climate zone are its Registry profile, edited in 系统管理 → 注册中心. Still to do:
   - climate correction, which needs daily weather (cooling degree days) from an owner the platform does not have yet;
   - a trusted industry baseline source; the page says 未接入 until there is one.

Smaller follow-ups:

- Counter deltas in the Metric engine do not stop at a Point revision or unit change; Energy Interval Facts (`counter_deltas_from`) do (`REVISION_BOUNDARY`, `UNIT_BOUNDARY`). This was already so inside a window, and since the window-start fix it also applies to the step from the reading before the window. Close it when a Point is first reconfigured in place.
- The accepted M&V result (`e2e/fixtures/savings-measure-verified.json`) was computed from `hourly_energy` before the window-start fix, about 1/60 low on both the baseline and the reporting days. Re-verify it the next time M&V acceptance runs.
- Move the FDD low-ΔT threshold from the schedule payload into an owner-held rule definition.
- Polish the device inspector and the work-order sheet in the same visual system.

## Local environment notes

These are drifts from the seed in the current local database. A `npm run local:reset` followed by `local:up` removes all of them.

- The stack was rebuilt from the #481 seed on 2026-10-12 22:06 (Site time); live history starts then.
- Both Sites hold replayed meter history, sub-meters included, for 2026-09-15 to 10-06 (Site time). Site A's includes the 8 °C setpoint raise from 09-29 09:00.
- The savings ledger holds the Option B acceptance measure on Site B (CHWP-01, 09-08 to 09-15). The Option C result lives on only as `e2e/fixtures/savings-measure-verified.json`.
- Both Sites now also hold replayed history for 2026-08-25 to 09-14 (Site time). Site B's chilled water pump runs at 45 Hz from 09-08 09:00. Only Site B's `hourly_energy`, `hourly_cooling` and CHWP-01 `device_hourly_energy` were recalculated over it; Site A's hourly and daily Metrics for that window are missing.
- `energy_cost` v1 (average-rate expression) was superseded by hand with v2 (`TARIFF_ENERGY_COST` on `hourly_energy`), with new bindings and the daily cost schedules repointed; a fresh seed starts at v1 = `TARIFF_ENERGY_COST`.
- Hourly and daily Metrics of both Sites for 2026-09-15 to 09-30 (Site time) were recalculated with the counter window-start fix on 2026-10-13: September's `hourly_energy` matches the energy facts (72,696.83 kWh) and `energy_cost` the report snapshot (¥60,258.71). Earlier and later periods still hold values from before the fix.
- The workload identity `internal-pki/historical-replay-runner` was issued from the local CA by the replay script.
- The ClickHouse user `report_service_reader` was created by hand (init scripts only run on an empty volume). September 2026 was snapshot for both Sites by a hand-queued `REPORT_GENERATE` job (16 of 30 days, from the replay).
- Energy facts written before the arrival-paging fix (2026-10-12) miss intervals. To fill them in, stop `telemetry-worker`, truncate `analytics.energy_projection_checkpoints`, then start it again. Truncating while it runs does nothing: the batch in flight writes its checkpoint back. The existing-fact check skips intervals that already have a fact.

Known quirks:

- **Host standby stops Docker Desktop.** After Windows Modern Standby (System log Kernel-Power 506/507), WSL may report `docker` missing. Start `D:\Program Files\Docker\Docker\Docker Desktop.exe`; the `hvac-local` containers come back on their own.
- **Flaky Go proxy.** `goproxy.cn` sometimes drops module downloads. Run `local:up` with `PHASE1_GO_PROXY="https://goproxy.cn|https://goproxy.io|https://proxy.golang.org,direct"` and re-run on a network error.
- **e2e under load.** With the local stack running, the e2e build alone can exceed Playwright's 240 s webServer timeout, and parallel workers time out on pages that never finish loading. Build `dist-e2e` once, start `vite preview --outDir dist-e2e --port 5190` yourself (the config reuses it), and run `npm run web:e2e -- --workers=1`.
- **Integration tests only run when a module changes.** Integration scripts run only when a PR touches their module, so a migration that breaks another module's seed shows up later. Example: the Registry baseline seeding FDD findings, fixed in #504.

## Process

- Branch per change and open a PR with the template in the `mattpocock-skills:pr` skill. Merge once CI is green; Claude's own PRs may be merged without asking.
- Verify UI against real data on 8443. Screenshot scripts use `scripts/lib/local-administrator-browser.mjs`.
- Add a `CHANGELOG.md` entry for anything a user would notice, and update this file when the next-up list or the local notes change.
