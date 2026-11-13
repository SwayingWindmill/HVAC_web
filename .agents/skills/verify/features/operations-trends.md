# Operations trends

The `/operations/trends` route renders multi-point historical telemetry grouped into separate physical-unit chart lanes (`kW`, `°C`, `m³/h`), optional previous-day comparison, point-picker staging, and summary statistics tables.

## Sub-features

- `trends-unit-lanes` groups selected points by physical unit (`功率 / 负荷 (kW)`, `温度 / 温差 (°C)`, `流量 (m³/h)`) with Chinese domain labels only.
- `trends-window-and-compare` syncs `preset=`, `window=`, `granularity=`, `compare=prev`, and `view=table` URL search params.
- `trends-statistics-table` switches via radio `统计表` to show `最新值`, `平均值`, `最小值`, `最大值`, and `覆盖率` (`趋势统计表`).
- `trends-point-picker` stages custom point selections up to the 8-point cap before applying them on `应用`.

## How to get to it (user POV)

- Click `运行` → `历史趋势` in the sidebar, follow `在历史趋势查看` from Operating Schedules, or navigate to `/operations/trends?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/trends.spec.ts`.

- **Switch presets, windows, and comparisons.** Open `/operations/trends?site=${SITE_ID}`, verify default radio `冷站功率与供回水`, switch `时间范围` to `1 小时` (`window=1h`, auto `1 分钟` granularity), toggle `对比昨日同时段` (`compare=prev`), and select `水泵流量与温差`.
- **Verify summary table and 8-point picker.** Switch to `统计表` (`view=table`), assert `趋势统计表` columns and `100%` coverage row, and exercise the custom point picker limit.
- **Proof.** Save stdout and `1440×900` screenshots under `.scratch/verify/<slug>/`.

## Gotchas

- Never expose raw English telemetry keys such as `chiller leaving chilled water temperature` in axis legends or table rows; assert negative visibility in Playwright.
