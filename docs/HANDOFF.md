# Handoff

The current state of the work and what to pick up next. Update it in the same PR that changes
what it says. How to build, test and run lives in `CLAUDE.md` / `AGENTS.md`; what changed for
users lives in `CHANGELOG.md`.

Last updated: 2026-10-13.

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
| 运维 | 告警, 故障诊断, 工单, 控制指令 | alarm, FDD (scheduled every 15 min; its rule revisions in `core_registry.fdd_rule_revisions`, spec `docs/specs/fdd-rule-definition.md`), work orders, command governance |
| 平台 | 通知, 集成管理, 系统管理 | notification inbox, gateway credentials, IAM/Registry/rules/audit |

Costs are estimates at the Site's time-of-use tariff and are labelled as such; Settlement
(`services/settlement-service`, a CLI) is still the authority on the bill and has no HTTP surface.

## Design system

> **Redesign in progress (2026-10-13 →).** The approved design is the Claude Design canvas
> https://claude.ai/artifact/LVwBRsavimfmaBHnRYJbdm (pages 定稿, 设计系统, 页面 · 批次 1–5) and
> `docs/design-redesign/` (`DESIGN-SYSTEM.md` + `design-tokens.css`). It is implemented in phases,
> one PR each; existing features stay, missing ones get the new visual language:
>
> 1. Tokens + app shell (top bar, grouped sidebar, page header, Button/Badge/Card/Status) — done (#529).
> 2. 总览 — done (#529). 3. 告警, 工单, 设备 — done; 资产与点位 moves to phase 7 with the rest of 系统管理.
> 4. 实时运行, 故障诊断, 控制指令, 通知中心.
> 5. 能耗分析, 分项能耗, 需量分析, 负荷预测, 站点对标. 6. 优化建议, 节能项目, 报告.
> 7. 集成管理, 系统管理, shared states.
>
> Visual checks against recorded data: run a Vite dev server on 5190 (`npm -w @hvac/web run dev -- --host localhost --port 5190`); the e2e config reuses it, so `npm run web:e2e -- e2e/<spec>` runs in seconds and a throwaway spec can screenshot pages with fixtures.
>
> Rules that come with it: each fact has one home page (table in `DESIGN-SYSTEM.md` §1); plain
> operator wording (same section); charts and tables together on analysis pages only when the
> table adds dimensions. Until a page's phase lands, the notes below still describe it.

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
- Unsaved drafts belong to the page that owns them: 系统管理 uses `features/system/unsaved-drafts.tsx`, a router blocker. The protected Site scope no longer tracks drafts (removed with its site-switch confirmation); it only purges resources and revokes request tokens. A Site page that gets a non-modal form should guard it the same way, with its own blocker.

## Next up

Roadmap issues, in order:

1. **#484 — measured savings (M&V).** Live and accepted end to end (spec `docs/specs/savings-verification.md`, ADR 0021).
   - Acceptance ran on replayed history: Site A's setpoint was raised to 8 °C on 2026-09-29 09:00; the measure was implemented then and ended 10-06.
   - The baseline used 336/336 hours (CV(RMSE) 0.6 %, NMBE 0.0 %) and passed; six days were verified, saving 373 kWh and ¥353 (re-verified on 2026-10-13 on hourly Metrics recalculated with the counter window-start fix; before it, 367 kWh and ¥347 with the same 1.5 % rate).
   - The result is recorded as `e2e/fixtures/savings-measure-verified.json`.
   - **Weeks of history without the live simulator.** `node scripts/local-history-replay.mjs --from … --to … [--commands <siteCode>=<file>] [--metrics-only | --weather-only]` replays the Sites' meters and weather station temperature through Telemetry Runtime's Historical Replay path, on each Site's daily scenario, then recalculates their hourly and daily Metrics. `--weather-only` adds the temperature alone to a window that already holds the meters.
     - It takes about 10 minutes per Site for three weeks, and a few minutes of Metric recalculation.
     - Replay only windows that end before live history begins, because the counters restart.
     - `tools/eg8200-simulator/configs/replay-setpoint-raise.json` is the command schedule used for acceptance.
   - **Option B (one machine's sub-meter) is live** (spec section 第二版). A measure names its measurement boundary; a device boundary fits the device's `device_hourly_energy` against plant cooling. Measures overlap only on different machines.
     - Accepted on replayed history: Site B's chilled water pump was cut from 50 Hz to 45 Hz on 2026-09-08 09:00 (`tools/eg8200-simulator/configs/replay-chwp-frequency-cut.json`). The baseline used 336/336 hours (the pump's power does not follow cooling, so the line is flat: 45 kWh/h); six days saved 1,646 kWh (27.1 %, as (45/50)³ predicts) and ¥1,256. Recorded as `e2e/fixtures/savings-measure-device-verified.json`.
     - A Site's savings rate is avoided ÷ (plant electricity over the verified hours + avoided), so measures on several machines add up.
   - Later: non-routine adjustments, demand savings, apportioning savings between measures.
2. **#485 — benchmarking, second half.** `/benchmark` ranks sites by plant COP, by electricity per square metre, or by electricity per square metre per cooling degree day (spec `docs/specs/site-profile-benchmark.md`, 第二版). A Site's floor area and GB 50176 climate zone are its Registry profile, edited in 系统管理 → 注册中心.
   - Degree days (CDD26) come from each Site's own weather station: the page reads its daily mean dry-bulb temperature (Device History aggregate, `DAY`) and sums the degrees above 26 °C. Move it into a daily Metric once reports or M&V need it too.
   - Still to do: a trusted industry baseline source; the page says 未接入 until there is one.

Smaller follow-ups:

- To change the FDD low-delta-T threshold or window, publish the next revision (never update one): `node scripts/publish-fdd-rule-revision.mjs --tenant <uuid> --minimum-delta-tc <K> --window-minutes <n> [--effective-from <instant>]`. Findings before 2026-10-13 in the local database cite the old string `chilled-water-low-delta-t/v1`; a reset clears them.
- Counter deltas in the Metric engine do not stop at a Point revision or unit change; Energy Interval Facts (`counter_deltas_from`) do (`REVISION_BOUNDARY`, `UNIT_BOUNDARY`). This was already so inside a window, and since the window-start fix it also applies to the step from the reading before the window. Close it when a Point is first reconfigured in place.
- Energy-series cost now follows the window, about 0.5 KB of ClickHouse memory per fact read. Live meters write a fact every few seconds (~24,000 per meter per day), so a month on one Site takes ~150 MiB and a year would not fit under the 1.5 GiB cap. Before long windows get used on live data, serve day and month granularity from an hourly rollup instead of raw facts.
- Polish the device inspector and the work-order sheet in the same visual system.

## Local environment notes

These are drifts from the seed in the current local database. A `npm run local:reset` followed by `local:up` removes all of them.

- The stack was rebuilt from the #481 seed on 2026-10-12 22:06 (Site time); live history starts then.
- Both Sites hold replayed meter history, sub-meters included, for 2026-09-15 to 10-06 (Site time). Site A's includes the 8 °C setpoint raise from 09-29 09:00.
- The savings ledger holds the Option B acceptance measure on Site B (CHWP-01, 09-08 to 09-15) and the re-verified Option C measure on Site A (8 °C setpoint, 09-29 09:00 to 10-06).
- Both Sites hold the weather station's replayed outdoor temperature for 2026-08-25 to 10-06 (Site time), replayed with `--weather-only` on 2026-10-13.
- Both Sites now also hold replayed history for 2026-08-25 to 09-14 (Site time). Site B's chilled water pump runs at 45 Hz from 09-08 09:00. Only Site B's `hourly_energy`, `hourly_cooling` and CHWP-01 `device_hourly_energy` were recalculated over it; Site A's hourly and daily Metrics for that window are missing.
- `energy_cost` v1 (average-rate expression) was superseded by hand with v2 (`TARIFF_ENERGY_COST` on `hourly_energy`), with new bindings and the daily cost schedules repointed; a fresh seed starts at v1 = `TARIFF_ENERGY_COST`.
- Hourly and daily Metrics of both Sites for 2026-09-15 to 09-30 (Site time) were recalculated with the counter window-start fix on 2026-10-13: September's `hourly_energy` matches the energy facts (72,696.83 kWh) and `energy_cost` the report snapshot (¥60,258.71). Site A's `hourly_energy` and `hourly_cooling` were also recalculated through 10-05 for the M&V re-verification. Other periods and Metrics still hold values from before the fix.
- The workload identity `internal-pki/historical-replay-runner` was issued from the local CA by the replay script.
- The ClickHouse user `report_service_reader` was created by hand (init scripts only run on an empty volume). September 2026 was snapshot for both Sites by a hand-queued `REPORT_GENERATE` job (16 of 30 days, from the replay).
- `analytics.energy_interval_facts_visible` was replaced by hand with the definition in `009-energy-fact-rebuild-v1.sql` (GROUP BY the logical key with `argMax`, so Site and period filters reach the table index) on 2026-10-13; a reset applies it from the init script.
- **The energy projector is stuck (seen 2026-10-13).** Its counter-delta query uses ~800 MiB per batch and every retry hits ClickHouse's 1.5 GiB cap; the projection checkpoint has not moved since 08:21 UTC, so no new energy facts are written, and the retries crowd out other ClickHouse readers. Not fixed yet; the suspect is a batch whose `since` falls back into replayed history.
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
