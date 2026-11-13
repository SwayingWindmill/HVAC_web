# Energy load

The `/energy-analysis/load` route tracks quarter-hour plant electrical load (`当前负荷`, `今日最高`, `本月最高`), yesterday's baseline curve, 24-hour load forecast (`明天最高（预测）`), daily peak history, and peak-period demand billing status.

## Sub-features

- `load-summary-and-curve` keeps an in-progress quarter-hour labeled `当前负荷 ... 进行中` rather than promoting it to peak load, and plots `.recharts-line` + `.recharts-area` curves across metered load and forecast.
- `load-hourly-forecast-tables` lists remaining today hours (`今天剩余 ... 小时逐时预测`) and tomorrow's 24-hour forecast (`明天逐时预测`) with headroom column `距本月最高`.
- `load-monthly-and-demand` switches between `本月每日最高` (`view=days`) and `需量电费` (reporting empty state when peak-period quarter-hours are absent).

## How to get to it (user POV)

- Click `能源` → `用电负荷` in the sidebar, or navigate to `/energy-analysis/load?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/load.spec.ts` (pins clock and `gateway.forecastOrigin` to `demand-quarter-hours.json` `recordedAt`).

- **Verify load KPIs, curves, and forecast tables.** Assert `当前负荷247.2kW`, `今日最高247.2kW`, `本月最高247.2kW`, `负荷曲线` Recharts layers, `明天逐时预测` (24 rows), and switch through `本月每日最高` (`view=days`) and `需量电费` (`暂无数据`).
- **Proof.** Save stdout and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- Always align `gateway.forecastOrigin` with `page.clock` (`demand-quarter-hours.json` `recordedAt`), otherwise forecast points fall out of the "today/tomorrow" window.
