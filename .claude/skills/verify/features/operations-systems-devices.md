# Operations systems and devices

The `/operations/systems-devices` route provides card-wall and ledger-table views of all site equipment, observed point counts, a device detail drawer with 24-hour trends, work-order creation, and inline AI `解释`.

## Sub-features

- `devices-wall-and-table` lists chillers, pumps, cooling towers, meters, and `WEATHER-STATION-01` while counting only observed telemetry points (`点位 11/11`, excluding command-only write points) and toggles between card wall and `表格` (`layout=table`).
- `devices-inspector-drawer` opens `[role="dialog"]` (`?inspect=<DEVICE_ID>`), translates raw state enums (`RUNNING`) into Chinese labels (`运行状态`), and renders a 24-hour `.recharts-surface` trend chart.
- `devices-work-order-and-explain` creates equipment-origin work orders (`domain: 'ASSET'`) and sends device questions to `AI 助手` via `解释`.

## How to get to it (user POV)

- Click `运行` → `设备` in the sidebar, click `主要设备` from Realtime, or navigate to `/operations/systems-devices?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/systems-devices.spec.ts`.

- **Inspect ledger and table views.** Navigate to `/operations/systems-devices`, verify URL adopts default `site=`, confirm `CHILLER-01` displays `运行` and `点位 11/11`, and toggle `表格` (`layout=table`).
- **Drive device inspector and work order.** Click `CHILLER-01`, confirm dialog shows `运行状态` (never `RUNNING`), `.recharts-surface` chart, `创建工单` (`sourceReferences` `ASSET`), and `解释` handoff to `AI 助手`.
- **Proof.** Save stdout and `1440×900` card/drawer PNGs under `.scratch/verify/<slug>/`.

## Gotchas

- Command points are write-only and intentionally excluded from observed point completeness (`11/11`); never flag un-sampled write points as missing telemetry.
