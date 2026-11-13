# Site benchmark

The `/benchmark` route compares all authorized sites across plant COP, electricity intensity per floor area (`kWh/m²`, `按单位面积用电`), and cooling degree-day intensity (`CDD26`, `kWh/m²·℃·d`, `按度日修正用电`) while listing permission-denied sites outside rankings.

## Sub-features

- `benchmark-cop-and-access` computes aggregate and per-site COP (`3.48COP`) on each site's timezone, prompts when `< 2` comparable sites exist, and isolates `analyticsDenied` sites (`没有该站点的能耗分析权限`).
- `benchmark-floor-area-intensity` ranks sites by `kWh/m²` (`rank=intensity`) using GB 50176 climate zone (`夏热冬冷地区`) and registered floor area (`32,000 m²`).
- `benchmark-climate-cdd` adjusts intensity by `CDD26` (`rank=climate`) from each site's weather station daily outdoor mean temperature (`ambient_dry_bulb_temperature`).

## How to get to it (user POV)

- Click `能源` → `站点对标` in the sidebar, or open `/benchmark`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/benchmark.spec.ts`.

- **Switch ranking tabs and verify denied-site isolation.** Open `/benchmark`, assert `对标概况`, `站点排名`, and `更多对比项` (`1 / 1 个站点登记了建筑面积`, `1 / 1 个站点有气象站`), click `按单位面积用电` (`rank=intensity`) and `按度日修正用电` (`rank=climate`), and toggle `gateway.analyticsDenied = true` verifying explicit permission refusal text.
- **Proof.** Capture pass output and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- `CDD26` test sets `gateway.energyByDay = true` so multi-day `energy-series` points align date-for-date with `outdoor-days.json`.
