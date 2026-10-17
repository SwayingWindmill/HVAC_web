# Handoff

The current state of the work and what to pick up next. Update it in the same PR that changes
what it says. How to build, test and run lives in `CLAUDE.md` / `AGENTS.md`; what changed for
users lives in `CHANGELOG.md`.

Last updated: 2026-10-16.

## Where the product is

These pages run on real owner data in the local stack (`hvac-local`, https://localhost:8443):

| Area | Page | Data owner |
| --- | --- | --- |
| 总览 | `/overview` | energy series, realtime plant, alarms, FDD, work orders, optimization, tariff |
| 监测 | 实时运行, 设备 | telemetry snapshots/push, Registry asset model, device history |
| 能源 | 能耗分析, 用电负荷 | analytics energy series, device-series hourly/quarter-hour aggregates, Site tariff; 用电负荷 also reads the Site load forecast |
| 能源 | 能耗分析 › 用电去向 | analytics energy series with `breakdown: "device"`: the total meter (SITE) and each machine's sub-meter (BRANCH); 分项能耗 merged into 能耗分析 on 2026-10-16 |
| 能源 | 优化建议 | forecast service (hourly at :07, read by 用电负荷 and the optimizer), optimization service (hourly at :20, and on demand; spec `docs/specs/scheduled-optimization.md`) |
| 能源 | 节能项目 (`/energy-analysis/savings`) | mv-service: measures, frozen baselines, verified days (ADR 0021) |
| 能源 | 能源报告 (`/reports`) | closed months from report-service snapshots (`REPORT_GENERATE`, 03:00 on the 1st); the running month and unarchived months assembled on demand; yearly view; printable to A4 |
| 能源 | 站点对标 (platform route `/benchmark`) | each authorized site's energy series; sites without analytics access are listed but not compared |
| 运维 | 告警, 故障诊断, 工单, 控制指令 | alarm, FDD (scheduled every 15 min; its rule revisions in `core_registry.fdd_rule_revisions`, spec `docs/specs/fdd-rule-definition.md`), work orders, command governance |
| 平台 | 通知, 集成管理, 系统管理 | notification inbox, gateway credentials, IAM/Registry/rules/audit |
| 平台 | 系统管理 → 用户与角色, 修改密码 | IAM Role Assignments (ADR 0022) + identity directory (`identityadmin`, embedded in energy-api); spec `docs/specs/iam-roles-and-users.md` |
| 全站点 | AI 助手（页头按钮，右侧 420px 面板；能耗分析上的「解释」） | Operations Agent Sessions through the Gateway (`/api/v1/sites/{site}/operations/agent-sessions`, snapshot + SSE); answers only where the service runs with `DEEPSEEK_API_KEY`, otherwise the panel says 未接入 |
| 运行 | 数字孪生 (`/operations/digital-twin`) | Registry plant assets + realtime plant (the same reads as 实时运行); the terminal building is declared per site in `twin-building.ts` (Songyaxuan only) and all terminal units show 未接入 until the Registry holds rooms and terminal devices (spec `docs/specs/digital-twin.md`) |

Costs are estimates at the Site's time-of-use tariff and are labelled as such; Settlement
(`services/settlement-service`, a CLI) is still the authority on the bill and has no HTTP surface.

## Design system

> **Redesign done (2026-10-13 → 10-14).** The approved design is the Claude Design canvas
> https://claude.ai/artifact/LVwBRsavimfmaBHnRYJbdm (pages 定稿, 设计系统, 页面 · 批次 1–5) and
> `docs/design-redesign/` (`DESIGN-SYSTEM.md` + `design-tokens.css`). It is implemented in phases,
> one PR each; existing features stay, missing ones get the new visual language:
>
> 1. Tokens + app shell (top bar, grouped sidebar, page header, Button/Badge/Card/Status) — done (#529).
> 2. 总览 — done (#529). 3. 告警, 工单, 设备 — done; 资产与点位 moves to phase 7 with the rest of 系统管理.
> 4. 实时运行, 故障诊断, 控制指令, 通知中心 — done (the plant system diagram is gone; a 3D view is planned instead).
> 5. 能耗分析 (分项能耗 merged in on 2026-10-16), 用电负荷 (需量分析 and 负荷预测 merged on 2026-10-16), 站点对标 — done. 6. 优化建议, 节能项目, 报告 — done.
> Type scale: `text-sm` is body 13/20 and `text-xs` caption 12/18 (DS §2.2, set in `global.css` `@theme`); use `text-title` (14/20) for panel titles and `text-micro` (11/16) for units and badges.
> 7. 集成管理, 系统管理, shared states, 404 — done. 数据接入 lives in 集成管理 (the data chain); the 设计系统 tab is gone, its token list (`features/system/design-system/design-tokens.ts`) stays for `scripts/test-real-design-tokens.mjs`.
> The 用户与角色 tab is being taken over by the IAM rework (another session, `features/system/users/`); keep `lib/capability-labels.ts` and `lib/role-labels.ts` in step with the IAM catalog.
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
  - `Panel` / `PanelHeader` / `PanelLink` (`blocks/panel`): the one white surface with its 48px title bar. Figures sit in a panel as a divided strip; there is no separate KPI widget any more.
  - `StateCard` (`blocks/state`): empty, failed, forbidden and not-connected states, and the route surfaces.
  - `SeverityMark` (shape-coded severity), `InfoTip` (explanations behind ⓘ instead of grey text).
  - `MetricTabsChartCard` (`chartType="bar"` with a hatched running bucket), `Table` from `components/ui/table`.
  - `Status` / `StatusBadge`: tinted label with a still dot.
- Do not add another figure strip or status pill style; extend these.
- `scripts/test-real-design-tokens.mjs` keeps every pairing listed in `features/system/design-system/design-tokens.ts` at its WCAG level.
- `FactTile`'s `className` styles the value, not the grid cell: to span columns, wrap it in a `<div className="col-span-…">`.
- Unsaved drafts belong to the page that owns them: 系统管理 uses `features/system/unsaved-drafts.tsx`, a router blocker. The protected Site scope no longer tracks drafts (removed with its site-switch confirmation); it only purges resources and revokes request tokens. A Site page that gets a non-modal form should guard it the same way, with its own blocker.

## Next up

0. **Site server (139.199.58.47, hvac.swayingwindmill.online) runs main since 2026-10-14.** Compose project `hvac-platform` in `/opt/hvac-platform`, demo tier with simulators and without the intelligence profile, behind the host nginx on 127.0.0.1:18443. Images are built locally from main and shipped with `docker save | ssh docker load`; `/opt/hvac-platform-tools/deploy.sh` replays the `local:up` steps there. ThingsBoard still runs because it receives a real Node-RED gateway over MQTT 8883 (host nginx stream); its database was dumped to `/opt/backups/thingsboard/`. Next: connect that gateway to the platform and import its ThingsBoard history before stopping ThingsBoard.
   - **Songyaxuan (松雅轩), the first real Site, is live on it (#542, spec `docs/specs/site-gateway-onboarding.md`).** Its Node-RED gateway publishes the platform envelope over mutual TLS on port 8884 (host nginx stream → Mosquitto), alongside its old ThingsBoard branch. The gateway key was generated on the gateway; its certificate expires 2027-01-12. The ThingsBoard history import (dataset `01a14000-7000-7000-8000-000000000001`, window up to 2026-10-14T10:26:30Z) runs as container `tb-history-import`; ThingsBoard is already stopped (restart=no, volumes kept) because the import reads the exported CSVs; when the import finishes, compare counts per device. Still to do on the gateway: move its durable outbox to the platform and delete the ThingsBoard branch (the iotrouter tunnel to Node-RED drops at times). The point mapping is provisional (spec), and the ET1010 is meant for control, which needs platform commands down to Node-RED. Energy (spec section 能耗、电价与智能层): the gateway adds the four meters into「空调总电表（四台机组合计）」, which the energy topology uses as the total; Metrics are bound and backfilled with `scripts/site-metric-recalc.mjs`; the Tariff is a provisional Shenzhen TOU. The server runs the intelligence services (single-lite, without the Operations Agent) and both simulators are stopped at the user's request. Equipment readings follow the site's hand-drawn channel table of 2026-10-15 (spec 设备读数): the gateway's `COPIES` copies temperature channels onto the machines, a room and an outdoor device, mirrored by `from` in the site file; some channels' readings disagree with their place on the table (evaporator in/out may be swapped), to be rechecked on site. Meter 4's energy now reads register 60 less a 76938 kWh baseline (spec 已知问题). Changing one Modbus read node on the gateway stalls the other six on the shared serial port; restart all flows afterwards (spec 已知问题).
     - **Server operations as of 2026-10-15.** Meter 4's history before 2026-10-14 15:03 UTC was rebuilt from integrated power (dataset `…0004`; D's counter runs negative up to 0 at the switch, the total meter carries it); the replaced rows are in ClickHouse `telemetry_history.d4fix_backup_observations` and `analytics.d4fix_backup_energy_facts` — drop them once the energy pages have been checked for a few days. Nightly backups run from `hvac-backup.timer` (#552) into the runtime directory on the same disk; no off-server copy yet. Runtime retention (#553) prunes delivered history events hourly and source observations after 7 days; the first observation prune starts on 2026-10-21 and a `VACUUM FULL telemetry_runtime.source_observations` afterwards returns about 3.5 GB. Alertmanager (#554) runs with the default receiver, so alerts stop there until a mail receiver is configured (runbook 「Alert delivery」); on the server Prometheus reads `runtime/server/config/prometheus.yaml` through `wsl.override.yaml`, which got the same `alerting` block by hand. The gateway has no ThingsBoard output left (only the platform MQTT broker); spare inputs t11/t20 are not sent and INACTIVE (#555). The cooling meter (#561) was added to the server's released topology v1 by a one-off insert with triggers bypassed (pre-launch repair; fresh onboarding builds it into v1); the calculated cooling counter's history up to 2026-10-15 03:33:53Z was replayed as dataset `…0006` so it projects into cooling energy, its replaced observations are in `telemetry_history.coolfix_backup_observations` — drop with the d4fix tables. On 2026-10-16 the gateway function was changed to form one envelope per sample (spec 已知问题): the durable outbox resent every unacknowledged record on each enqueue, so the temperature message's nine envelopes sent the first one eight times and the good rate fell to about 33 %; duplicates are 0 since. The same day both demo sites (central-plant, local-energy-site-b) had all 28 schedules disabled because their stopped simulators made the metric jobs go DEAD every hour; re-enable them with the simulators. Also on 2026-10-16 Songyaxuan got its load forecast chain (onboarding never created one, so 负荷预测 was always empty): the 15-minute load Metric on the total meter's power, a baseline model and the hourly FORECAST_RUN, applied on the server from the onboarding SQL with a week of quarter-hours backfilled by `site-metric-recalc.mjs` (which now handles 15MIN).

   - **Digital twin (#559, #563).** A full-bleed perspective scene in a cool, primary-tinted world (neighbourhood blocks, street trees, fences) built from a procedural asset library (`features/operations/digital-twin/scene/assets/`), with N8AO post-processing and a frame-rate guard that drops it on weak GPUs. The UI is four corner panels (`components/TwinCorners.tsx`), designed on the canvas https://claude.ai/artifact/M9fhveMZf6K6iFBEgif7og after the reference warehouse twin; the scene draws no labels, only selection brackets. Not yet measured on a real GPU at 1440 px: first-load time to an interactive scene and steady CPU while water flows (the in-app preview pane was hidden during development, which throttles rendering). Next (P2): register Songyaxuan's rooms and terminal units so the building comes from the Registry, colour rooms by temperature.

   - **Operations Agent (AI 运维助手), first release (#577, spec `docs/specs/operations-assistant-v1.md`).** Pi (`@earendil-works/pi-agent-core` 1.3.0) is the only runtime (ADR 0014). Read-only Tools: Site, assets, `equipment_list` (devices, machines and active point keys from the asset model), current readings, `telemetry_query_history` (device-history aggregate; Gateway authorizes `telemetry.history.read` and signs a query-bound grant for the Agent), energy series and period comparison (both take `energyType: cooling`), `energy_by_equipment` (Energy Series `breakdown: device`). The system policy is Chinese and carries the Run's start time; findings carry up to four key figures. The Web panel is built on assistant-ui (`docs/architecture/assistant-ui-source-review-2026-10-16.md`) over the Session snapshot and events; 解释 on 能耗分析 prefills a question with the bucket's date and energy. Live on the server since 2026-10-16 (`DEEPSEEK_API_KEY` in `server.runtime.env`, put there by the user; `deploy.sh` now starts the agent; the old LangGraph-era database was dropped and recreated by the migrator). Accepted on Songyaxuan: 解释 on 10/15 answered +762 kWh (+87 %), cooling +78 %, COP 4.4 vs 4.6, the excess at 17–20 h, B 机组 1 → 297 kWh, C 584 → 879, D 287 → 456, in about 30 s over 9 reads. Acceptance found and fixed: the strict Site decoder rejected the Registry's profile fields; hourly points were read as UTC (results now carry `siteTime`); the finding had no evidence id to cite (results now carry `evidenceId`); a six-read batch over the parallel limit failed the Run (excess now waits). Budget per Run: 12 model calls, 24 Tool calls. Still rough: the model sometimes narrates in English between Tool calls (the panel shows only the last turn), opens its answer with a filler sentence, and the answer text repeats the finding card. Not yet: history list, Cube semantic query Tool (#573), alarm/FDD/work-order Tools, session audit, a Pi benchmark, Chinese wording in the finding's physical-execution guard (it matches English phrases only).

   - **Cube is the semantic layer for energy reads (ADR 0023, 2026-10-16, parent #574).** Since #571 (2026-10-17) Cube's only public surface is two views, `site_energy` (the Site total, with electricity, cooling and COP) and `equipment_energy` (sub-meters by `equipment_id`), over internal cubes that fix the accounting scope in their SQL; every member has a Chinese title and description, and the query service reads only the views (`semantic/cube/README.md`, `docs/architecture/cube-views-source-review-2026-10-17.md`). On the Songyaxuan server the Energy Series for closed hour/day/month windows, electricity with breakdown and cooling, came out identical before and after, except the last float digits of one monthly sum, which vary between identical calls anyway. The local stack was down and was not compared. Since #572 (2026-10-17) report-service reads the Site's hourly electricity and cooling through the query service as its own Workload Principal `spiffe://hvac.local/report-service`: IAM checks its Tenant-wide `report-energy-read` role (only `analytics.energy-series.read`, seeded by the foundation bootstrap) and signs it a 30 s Energy grant (`/internal/v1/analytics/workload-decision`), which the query service accepts next to the Gateway's; a Site outside the Tenant is not found. report-service has no ClickHouse account any more; its mTLS identity `internal-pki/report` is issued from the runtime CA by `scripts/issue-workload-identities.mjs` (local:up and the server's `deploy.sh` run it). On the server the Songyaxuan 2026-09 snapshot came out jsonb-identical before and after, and `report_service_reader` was dropped there. Since #575 slice 1 (2026-10-17, `docs/specs/estimated-energy-cost.md`) Cube also prices electricity: the Telemetry Runtime lays each priced Site's import Tariff out as UTC price windows in `analytics.tariff_price_windows` (from Registry's new `import-tariffs` route, `tariff.read` added to the `energy-projection` role), and the `site_energy_cost` view prices every Site-total electricity fact at the window its period ends in; Energy Series electricity points carry `cost` (amount, Tariff periods, unpriced kWh). On the server Songyaxuan 2026-09 came out ¥28,777.85 with the same PEAK/FLAT/VALLEY split as report snapshot v5. Since slice 2 the Web (能耗分析, 总览, 报告; `sumCosts`/`costsByLabel` add up the points' `cost`), report-service's monthly snapshot (status from unpriced kWh: PRICED, NO_TARIFF when nothing was priced, UNPRICED_HOURS) and the Agent's energy Tools (`estimatedCostCNY`, `unpricedKWh`) read that cost; the browser and report-service no longer price, and the pages no longer re-read hours to price them. Not yet switched: the `energy_cost` Metric and M&V (slice 3); the Web still divides cooling by electricity itself. Next: #575 slice 3, then #573 the Agent's semantic query Tool.

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
- Each Energy delta query still aggregates its Points' whole Counter history for the ANCHOR, PREFIX_MAX and NEXT rows of `counter_deltas_from`. Memory stays small (about 40 MiB a window locally), but every window reads about 7 M rows, so a batch costs roughly 0.5 s per window and grows with history. The `has(points, point_id)` filter cannot use the `observations` sort key, which wraps `point_id` in `ifNull`.
- `scripts/local-history-replay.mjs` writes its `--to` instant, but its "window already holds history" check is half-open (`< to`), so two adjacent replays both write the boundary instant (fixed in #534; see the stuck projection below).

## Local environment notes

These are drifts from the seed in the current local database. 

- `infra/telemetry/clickhouse/init/014-tariff-price-windows.sql` (#575) only runs on an empty volume: on an existing ClickHouse apply it with `docker exec -i <clickhouse> clickhouse-client --multiquery < …`, and re-run the foundation bootstrap so `energy-projection` gets `tariff.read`. The server got both on 2026-10-17.

- Since 2026-10-14 the stack runs migration 026 (Role Assignments, ADR 0022): the per-domain permission tables are gone and the local administrator holds the built-in `admin` Role. `runtime/local/identity-reconcile.json` was rewritten to that shape (`roleBindings: [{"roleKey": "admin"}]`, `sourceVersion` 2); the old action-list shape no longer reconciles. A `npm run local:reset` followed by `local:up` removes all of them.

- The stack was rebuilt from the #481 seed on 2026-10-12 22:06 (Site time); live history starts then.
- Both Sites hold replayed meter history, sub-meters included, for 2026-09-15 to 10-06 (Site time). Site A's includes the 8 °C setpoint raise from 09-29 09:00.
- The savings ledger holds the Option B acceptance measure on Site B (CHWP-01, 09-08 to 09-15) and the re-verified Option C measure on Site A (8 °C setpoint, 09-29 09:00 to 10-06).
- Both Sites hold the weather station's replayed outdoor temperature for 2026-08-25 to 10-06 (Site time), replayed with `--weather-only` on 2026-10-13.
- Both Sites now also hold replayed history for 2026-08-25 to 09-14 (Site time). Site B's chilled water pump runs at 45 Hz from 09-08 09:00. Only Site B's `hourly_energy`, `hourly_cooling` and CHWP-01 `device_hourly_energy` were recalculated over it; Site A's hourly and daily Metrics for that window are missing.
- `energy_cost` v1 (average-rate expression) was superseded by hand with v2 (`TARIFF_ENERGY_COST` on `hourly_energy`), with new bindings and the daily cost schedules repointed; a fresh seed starts at v1 = `TARIFF_ENERGY_COST`.
- Hourly and daily Metrics of both Sites for 2026-09-15 to 09-30 (Site time) were recalculated with the counter window-start fix on 2026-10-13: September's `hourly_energy` matches the energy facts (72,696.83 kWh) and `energy_cost` the report snapshot (¥60,258.71). Site A's `hourly_energy` and `hourly_cooling` were also recalculated through 10-05 for the M&V re-verification. Other periods and Metrics still hold values from before the fix.
- The workload identity `internal-pki/historical-replay-runner` was issued from the local CA by the replay script.
- The ClickHouse user `report_service_reader` was created by hand (init scripts only run on an empty volume) and is unused since #572; drop it or reset. September 2026 was snapshot for both Sites by a hand-queued `REPORT_GENERATE` job (16 of 30 days, from the replay).
- `analytics.energy_interval_facts_visible` was replaced by hand with the definition in `009-energy-fact-rebuild-v1.sql` (GROUP BY the logical key with `argMax`, so Site and period filters reach the table index) on 2026-10-13; a reset applies it from the init script.
- `telemetry_history.counter_deltas_from` and `counter_deltas` were replaced by hand from `004-counter-semantics.sql` on 2026-10-13 (bounded delta windows), and only `telemetry-worker` was rebuilt from that branch.
- The 08-25..09-14 replay also wrote its `--to` instant, 2026-09-14 16:00 UTC, on top of the 09-15..10-06 replay's first readings; the replay is fixed in #534. Those 12 Counter observations (datasets `01a03480-0400-7000-8000-00000000000{1,2}`) were deleted on 2026-10-13 14:44 UTC: the pair stopped the Energy projector at history sequence 9539 with a zero-length interval. Hourly Metrics for that Site-time hour (09-15 00:00) were calculated with both readings and still hold those values.
- Energy facts written before the arrival-paging fix (2026-10-12) miss intervals. To fill them in, stop `telemetry-worker`, truncate `analytics.energy_projection_checkpoints`, then start it again. Truncating while it runs does nothing: the batch in flight writes its checkpoint back. The existing-fact check skips intervals that already have a fact.

Known quirks:

- **Host standby stops Docker Desktop.** After Windows Modern Standby (System log Kernel-Power 506/507), WSL may report `docker` missing. Start `D:\Program Files\Docker\Docker\Docker Desktop.exe`; the `hvac-local` containers come back on their own.
- **Slow npm mirror.** Some tarballs take over four minutes from npmmirror; the web image's `npm ci` uses a 15-minute fetch timeout. If registries are unreachable, `PHASE1_BUILD_PROXY=http://host.docker.internal:7897` passes the host proxy to image builds.
- **local:up from a worktree.** A worktree made by Windows git stores a Windows path in `.git`, so set `GIT_DIR`/`GIT_WORK_TREE`, and point `PHASE1_RUNTIME_DIR` and `PHASE1_ENV_FILE` at the main checkout's runtime and env files.
- **Flaky Go proxy.** `goproxy.cn` sometimes drops module downloads. Run `local:up` with `PHASE1_GO_PROXY="https://goproxy.cn|https://goproxy.io|https://proxy.golang.org,direct"` and re-run on a network error.
- **e2e under load.** With the local stack running, the e2e build alone can exceed Playwright's 240 s webServer timeout, and parallel workers time out on pages that never finish loading. Build `dist-e2e` once, start `vite preview --outDir dist-e2e --port 5190` yourself (the config reuses it), and run `npm run web:e2e -- --workers=1`.
- **Integration tests only run when a module changes.** Integration scripts run only when a PR touches their module, so a migration that breaks another module's seed shows up later. Example: the Registry baseline seeding FDD findings, fixed in #504.

## Process

- Branch per change and open a PR with the template in the `mattpocock-skills:pr` skill. Merge once CI is green; Claude's own PRs may be merged without asking.
- Verify UI against real data on 8443. Screenshot scripts use `scripts/lib/local-administrator-browser.mjs`.
- Add a `CHANGELOG.md` entry for anything a user would notice, and update this file when the next-up list or the local notes change.
