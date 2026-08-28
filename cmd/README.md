# Canonical executable entrypoints

`cmd/` contains the canonical long-running Phase 1 application entrypoints. Process topology is defined here; business/domain ownership is not.

Current entrypoints:

- `energy-api`
- `iot-service`
- `telemetry-worker`
- `metric-worker`
- `scheduler`
- `maintenance-worker`

During RC-03 these command modules intentionally keep module paths under their historical implementation module namespace so Go `internal/` visibility remains unchanged while executable files move to the canonical root. The command modules contain startup/composition code only; domain implementation remains under `services/*` until RC-04 moves it into `modules/*`.

This is a temporary source-layout seam, not a runtime compatibility layer. RC-04 must remove the historical module namespace dependency when the corresponding domain packages move. Do not add new domain logic under `cmd/`.
