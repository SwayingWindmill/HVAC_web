# Alarm Telemetry Evaluation Source Review

Status: CURRENT IMPLEMENTATION EVIDENCE
Issue: #340
Parent: #331
Date: 2026-08-28

## Scope

This review records the source-first decisions used to connect Telemetry-owned canonical Device snapshots to the production Alarm evaluator. It covers only the Telemetry → Alarm evaluation bridge. It does not transfer Telemetry authority into Alarm, add a simulator shortcut, or introduce a new Alarm policy input-binding DSL.

The local implementation keeps the existing Postgres-backed Alarm evaluator and durable claim model as authoritative. Telemetry publishes its already-committed canonical Device snapshot through the existing transactional outbox; Alarm stores only the latest derived per-Device evaluation input required to rebuild SITE evaluation snapshots.

## ThingsBoard

Pinned official baseline: `thingsboard/thingsboard` tag `v4.3.1.3`.

Reviewed source/tests/documentation:

- `AlarmRuleState.java` in the pinned official source;
- official `AlarmRulesTest.java` coverage;
- official ThingsBoard Alarm Rules documentation.

Source-level decision:

- `ADAPT`: telemetry-driven alarm evaluation. A new authoritative telemetry fact may trigger rule evaluation immediately.
- `ADAPT`: duration state survives individual telemetry messages and is reevaluated when the configured duration becomes due.
- `ADAPT`: scheduled/due reevaluation remains separate from receipt of a new telemetry message; HVAC uses its existing durable `ClaimDueEvaluations` / `EvaluateClaim` seam.
- `REJECT`: copying ThingsBoard actor/device-profile/rule-chain architecture into HVAC. The existing Alarm policy/evaluator boundary is already authoritative and materially smaller.
- `REJECT`: treating missing telemetry as predicate `false`. HVAC preserves missing, stale, invalid, unavailable, or untrusted inputs as the existing `INDETERMINATE`/quality-blocker behavior.

Local consequence: the bridge builds `EvaluationSnapshot` inputs with canonical quality, freshness, observed time and evidence, then calls the existing Alarm evaluator instead of implementing a second rule engine.

## OpenEMS

Pinned official baseline: commit `a7efc1c1eacd05f7a0f8eb43f962564ccf66ead6`.

Reviewed source/tests/documentation:

- `io.openems.backend.alerting/.../Handler.java` at the pinned commit;
- official OpenEMS Alerting tests located for the reviewed alerting behavior;
- the corresponding official alerting/backend ownership documentation used during the pre-implementation review.

Source-level decision:

- `ADAPT`: keep acquisition/event handling separate from backend alerting ownership. Telemetry owns acquisition and canonical observation state; Alarm owns platform Alarm evaluation and incidents.
- `REJECT`: Edge or simulator directly creating platform Alarm incidents. The simulator remains only a source of device behavior; canonical Telemetry must be committed before Alarm evaluation.

Local consequence: `telemetry-worker` relays the committed canonical outbox event over mTLS to the Alarm owner. It never writes Alarm tables and the simulator never calls the Alarm API.

## MyEMS

Pinned official baseline: commit `51972b1bb807e47c86feca443b53a560d985adcc`.

Reviewed source/tests/documentation:

- `myems-modbus-tcp/acquisition.py` at the pinned commit;
- official MyEMS data-flow documentation for acquisition and downstream processing.

No usable upstream test corpus was found for the pinned acquisition scope. This review therefore records no fabricated MyEMS test evidence.

Source-level decision:

- `ADAPT`: acquisition persists measurement facts; downstream domain logic derives business facts from those measurements.
- `REJECT`: Telemetry writing Alarm tables directly.
- `REJECT`: MyEMS-style direct database coupling between acquisition and downstream Alarm logic.

Local consequence: Telemetry retains its own outbox and independent Alarm delivery state; Alarm receives a service-to-service mTLS request and persists only its derived latest evaluation-input projection.

## HVAC implementation decisions

### Canonical publication and independent delivery

`telemetry_publication_outbox` remains the single publication intent for a committed Device snapshot. #340 adds Alarm-specific delivery/lease metadata to that row rather than reusing realtime delivery state or creating another business-event payload. Realtime and Alarm can therefore claim, retry and complete independently.

Failures on the Alarm transport remain retryable. There is no fallback value, simulator shortcut, direct Alarm database write, or alternate legacy path.

### Alarm-owned SITE evaluation projection

Alarm persists the latest accepted canonical Device snapshot per Tenant/Site/Device in `alarm_runtime.telemetry_evaluation_input`. A strictly lower business revision cannot replace a newer Device fact. An identical revision/event is an idempotent retry; an identical revision with a different event identity is rejected as inconsistent.

For SITE assignments, Alarm deterministically merges the current Device records into one `EvaluationSnapshot`. The input revision is derived from the sorted `(device, business revision, event)` set, so delivery order does not change rule identity.

Canonical telemetry keys remain device-scoped. SITE evaluation inputs use `deviceId/telemetryKey`. The current tracer combines:

- `<chiller-device-id>/run_state`;
- `<chiller-device-id>/cooling_capacity`;
- `<btu-device-id>/return_water_temperature`.

The accepted tracer policy uses the parent #331 thresholds: running chiller, at least 30% of 1200 kW rated capacity (`>= 360 kW`), return water `<= 10.5 °C` for 300 seconds; clear at return water `>= 11.5 °C` or when the chiller is no longer running.

### Quality, freshness and evidence

A present canonical key becomes an Alarm `InputFact` carrying its value type, quality, sampled time and Telemetry snapshot evidence. Telemetry freshness other than `FRESH` is preserved as `STALE`; unavailable Device evaluation is preserved as untrusted quality. Missing keys remain missing rather than becoming zero/false.

Alarm incident evidence is therefore derived from authoritative Telemetry snapshot/event references, and the existing evaluator continues to decide `INDETERMINATE`, duration, publish and clear outcomes.

### Workload authorization

The internal evaluation endpoint is only routed for the configured Telemetry SPIFFE workload (default `spiffe://hvac.local/telemetry-runtime-service`). Gateway and unauthenticated workloads are rejected from that endpoint. Normal Alarm API routes remain gateway-only.

Both the standalone `alarm-owner` and the default embedded Alarm owner in `energy-api` use the same owner router. Owner-split deployment changes only the destination hostname; the TLS server identity remains `alarm-service`.

## Focused local evidence

The #340 implementation is covered by:

- Alarm unit tests for cross-Device merge, stable input revision, evidence, stale/unavailable → `INDETERMINATE`, and workload route separation;
- Telemetry relay unit tests for success and retry-without-fallback;
- Telemetry Postgres integration proving Alarm and realtime delivery states/leases are independent on the same canonical outbox event;
- Alarm Postgres integration using the real policy assignment/store/evaluator seam, including out-of-order Device revisions, a durable five-minute claim, Alarm publication, and clear hysteresis;
- Phase1 deployment verification confirming the two new production migrations are in the migration allowlist and the canonical runtime topology remains valid.

This evidence supports `ADAPT` decisions above without treating any pre-existing local implementation as the default authority.

## 2026-09-28 live acceptance: device-scoped inputs

Observed in #346: central-plant Registry uses device-scoped point codes (`run_state`, `frequency`, `return_water_temperature`). The Site bridge rejected duplicate keys across devices; its tests had used globally prefixed names that the live ingest path does not emit. FDD also selected those obsolete names.

Re-read official ThingsBoard `v4.3.1.3`, commit `105351615126682762caf849619f0ea02df1faf3`:

- `common/data/src/main/java/org/thingsboard/server/common/data/cf/configuration/Argument.java`: referenced entity identity and referenced key are separate input coordinates.
- `application/src/main/java/org/thingsboard/server/service/cf/ctx/state/alarm/AlarmRuleState.java`: duration uses event/current time and preserves evaluation state.
- `application/src/test/java/org/thingsboard/server/cf/AlarmRulesTest.java`: duration and argument-source change scenarios.
- Official documentation: https://thingsboard.io/docs/user-guide/calculated-fields/ (argument sources and cross-device calculation).

ADOPT entity-plus-key input identity. ADAPT to the existing HVAC Condition.Input string as `deviceId/telemetryKey`, preserving original event/key evidence. REJECT a global point-key uniqueness assumption and any compatibility lookup for old prefixed names. FDD already has an explicit deviceId and now selects that device's canonical `supply_water_temperature` and `return_water_temperature`. Existing device-level IAM authorization remains authoritative. No external source was copied.

OpenEMS reacting source/test at `a7efc1c1eacd05f7a0f8eb43f962564ccf66ead6` was also re-read (the paths in `virtual-central-plant-chwp-stuck-high-source-review.md`): ADOPT elapsed-time physics. The MQTT simulator previously advanced a fixed interval even after delayed ticks; it now advances by actual elapsed time. MyEMS acquisition/business separation remains the applicable comparison recorded there; neither simulator produces Alarm/FDD/Work Order facts.

Verification: the existing Site merge test was first reproduced failing with two real device-scoped `return_water_temperature` inputs, then passed after qualification. Existing freshness/quality tests and Alarm/FDD package tests pass. Live end-to-end acceptance is still pending; this record does not certify it.

### Continuous matching incident (2026-09-29 live regression)

Also reviewed `application/src/main/java/org/thingsboard/server/service/cf/ctx/state/alarm/AlarmCalculatedFieldState.java` at the same pinned ThingsBoard commit, including `calculateAlarmResult`, alongside `AlarmRuleState.doEval` / `evalDuration` and `AlarmRulesTest.testCreateAlarm_durationCondition`.

ThingsBoard updates the existing alarm end timestamp/details for new matching telemetry; its scheduled reevaluation skips unchanged severity. It does not implement HVAC's append-only occurrence evidence collection. HVAC's previous mapping of every matching snapshot to `RecordOccurrence` accumulated 54,340 evidence references within one continuous incident and made the public read exceed its response limit.

- ADAPT: retain the active incident while its condition remains matched; evaluate clear conditions on subsequent observations.
- REJECT: translating every telemetry revision into another business occurrence. HVAC occurrence publication appends immutable evidence and timeline entries, unlike the upstream current-details update; ordinary sampling is not a new occurrence under the current fixed-severity policy.
- RETAIN: a changed policy revision can publish its changed severity/rule evidence against the active incident. A genuine clear followed by a new trigger creates the next incident through the existing owner.

The evaluator still persists the latest input revision, freshness state and next due time. No telemetry is discarded and no historical incident is deleted. Historical oversized responses from the pre-fix run remain a separate read-projection problem; this change does not certify their readability.
