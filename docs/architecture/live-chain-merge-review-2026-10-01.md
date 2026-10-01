# Live chain integration review — 2026-10-01

Integrate `issue-346-wsl-acceptance` at `ec315bed` into the primary checkout. Issue 346 is historical acceptance evidence, not an authority for current page architecture or runtime parameters.

## Reviewed conflict decisions

- ADOPT the acceptance simulator configuration, Scenario model, physical response and command/readback tests. Absolute cooling demand and elapsed-time response replace the earlier load-fraction datasource prototype. Remove the unused `datasource.go`; do not retain two scenario mechanisms.
- ADAPT telemetry-worker wiring: add the Alarm evaluation relay and Historical Replay acceptor while retaining current analytics projection, realtime relay and history microbatch `Run` behavior.
- ADAPT the telemetry HTTP handler: add the dedicated Historical Replay route while retaining the current authenticated snapshot-reader SPIFFE allowlist.
- ADAPT history integration tests: retain the current lease-expiry/deduplication regression test and add the test proving Historical Replay does not mutate current measurements, snapshots or presence.
- ADAPT route ownership: preserve current public Alarm assignment and add the accepted FDD evaluation/link routes; registry revision becomes 33.
- REJECT resurrection of superseded CI families. Current domain task matrix and capability classification remain authoritative. Preserve the committed removal of seven obsolete workflow/check files.
- REJECT obsolete browser runtime choices where they conflict with current Linux-authoritative browser resolution.

Reviewed sources: `tools/eg8200-simulator/internal/simulator/{config,model,model_test,scenario,scenario_test,edge_runtime_test,mqtt_command_test,mqtt_command_recovery_test}.go`, `cmd/telemetry-worker/main.go`, `modules/telemetry/pkg/telemetry/{server,history_clickhouse_integration_test,alarm_evaluation_relay,source_server}.go`, `contracts/ownership/route-ownership.v1.json`, and current `scripts/{domain-task-matrix,classify-pr-gates,run-s2-hvac-web-presence-browser-audit}.mjs`. Upstream provenance remains recorded in the physical-model, Alarm, FDD, replay and work-order source-review records integrated with this branch.

## Validation limits

Focused Go package tests passed for Simulator, Telemetry, Gateway, Work Order and FDD; telemetry-worker compiled. Database-dependent tests skipped without configured integration endpoints are not live-chain evidence. Live acceptance is still required after runtime recovery and frontend integration.
