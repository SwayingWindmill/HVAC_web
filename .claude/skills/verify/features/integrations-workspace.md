# Integrations workspace

The `/settings/integrations` route hosts the three hardware-facing operator tabs: `现场网关` (gateway data chain and enrollment codes), `电表数据核对` (`?view=meters` valid vs suspect hourly energy reconciliation), and `传感器校准` (`?view=sensors` probe offset suggestions and calibration due dates).

## Sub-features

- `integrations-gateway-chain` displays the 5-stage data chain (`现场设备` → platform), active gateway cards (`EG8200-*`), and enrollment code lifecycle actions.
- `integrations-meter-reconciliation` contrasts valid and suspect energy deltas, toggles chart vs table views, opens the meter drawer, and creates deduplicated inspection work orders.
- `integrations-sensor-calibration` surfaces pump-steady-state probe zero-offset suggestions (marked unapplied), low-ΔT uncertainty bounds, and calibration work orders.

## How to get to it (user POV)

- Click `集成管理` in the sidebar, click the `集成管理 ›` link from System Overview, or follow a link from the top `data-gap-banner`.
- Navigate directly to `/settings/integrations?site=<SITE_ID>` (or with `&view=meters` / `&view=sensors`).

## Driving it with Playwright

Preconditions:

- Operator holds gateway/meter read capabilities and target `<SITE_ID>` has registered meters and snapshot points.

- **Verify default gateway tab and credential lifecycle at 1440px.** Navigate to `/settings/integrations?site=${SITE_ID}` and run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/integrations.spec.ts e2e/gateway-credentials.spec.ts`. Assert heading `集成管理`, selected tab `现场网关`, region label `数据链路` containing `现场设备`, and `document.documentElement.scrollWidth <= window.innerWidth`. In `e2e/gateway-credentials.spec.ts`, open a gateway drawer, generate `一次性接入码`, press `Escape` to confirm the secret input is discarded on close, and verify `停用网关…` opens an explicit confirmation `alertdialog` (`该身份不能恢复`) before calling `永久停用此网关`.
- **Verify hourly meter reconciliation.** Click tab `电表数据核对` (`URL /view=meters/`), confirm `已接入表计`, `有效计量时段`, `可疑差值 +25.50 kWh`, toggle radiogroup `逐时电量核对视图切换`, open the meter drawer, and trigger work-order creation confirming persisted `POST` payload in `gateway.writes`.
- **Verify sensor calibration drawer.** Click tab `传感器校准` (`view=sensors`), confirm `header-chw` offset suggestion and `LOW_CONFIDENCE` uncertainty badges render cleanly.
- **Proof.** Capture test pass log and `1440×900` tab screenshots under `.scratch/verify/<slug>/`.

## Gotchas

- `integrations.spec.ts` injects fresh `evaluatedAt` / `sampledAt` timestamps dynamically via `refreshSnapshotsWithCalibrationSignals`; static fixtures alone appear stale without refreshing timestamps.
- Ensure horizontal overflow check (`scrollWidth > innerWidth`) passes on all three tabs after viewport layout edits.
