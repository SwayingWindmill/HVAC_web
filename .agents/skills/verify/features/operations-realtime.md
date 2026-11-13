# Operations realtime

The `/operations/realtime` route presents the plant's live COP, total power, instantaneous cooling load, water loop/ambient readings, running equipment counts, and quarter-hour realtime trend curves.

## Sub-features

- `realtime-plant-metrics` displays `冷站运行` tabs (`冷站 COP`, `冷站总功率`, instantaneous cooling), active equipment counts (`主要设备 4 / 4 运行`), and the `水系统与室内外环境` table.
- `realtime-stream-honesty` renders `[data-testid="real-realtime-status"]` (`未订阅` + `每 30 秒刷新`) when no WebSocket grant is present rather than faking a stream connection.
- `realtime-gap-preserving-curve` syncs `metric=` and `window=` search params and breaks missing quarter-hour buckets instead of interpolating across telemetry outages.

## How to get to it (user POV)

- Select `运行` → `实时运行` in the sidebar, or open `/operations/realtime?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/realtime.spec.ts`.

- **Inspect live KPIs and honest polling status.** On `/operations/realtime?site=${SITE_ID}`, verify `冷站 COP` tab is selected by default, `水系统与室内外环境` shows `24.1 °C`, `每 30 秒刷新` is visible, and `[data-testid="real-realtime-status"]` has `data-realtime-state="idle"` (`未订阅`).
- **Verify URL state and device drilldown.** Click `冷站总功率` (`metric=power`) and radio `24 小时` (`window=24h`), click `主要设备` link to `/operations/systems-devices`, and test outage gaps via `gateway.aggregateHandler`.
- **Proof.** Capture test stdout and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- `realtime.spec.ts` verifies outage discontinuities on `QUARTER_HOUR` `active_power` buckets by injecting missing indices `[0..3, 8..11]` via `gateway.aggregateHandler`.
