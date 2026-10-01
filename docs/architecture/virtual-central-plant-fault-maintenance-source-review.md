# Virtual Central Plant — Fault-to-Maintenance Source Review

Status: WAYFINDER RESEARCH EVIDENCE
Wayfinder map: `wayfinder: virtual central plant`
Research ticket: `Research authoritative fault-to-maintenance evidence chain`
Reviewed: 2026-08-28

## Question

What is the current production path from Telemetry observations through Alarm evaluation and FDD findings into Work Order source provenance, which owner creates each fact, and what concrete gaps prevent one simulated physical fault from reaching maintenance without simulator-private writes?

## Primary local sources reviewed

- `modules/telemetry/pkg/telemetry/ingest.go`
- `modules/telemetry/pkg/telemetry/ingest_store.go`
- `modules/alarm/pkg/alarmservice/evaluator.go`
- `modules/alarm/pkg/alarmservice/evaluator_postgres.go`
- `modules/alarm/pkg/alarmservice/store.go`
- `modules/alarm/pkg/alarmservice/http.go`
- `libs/alarmmodel/model.go`
- `services/fdd-service/internal/fdd/model.go`
- `services/fdd-service/internal/fdd/service.go`
- `services/fdd-service/internal/fdd/http.go`
- `libs/intelligencemodel/model.go`
- `libs/workordermodel/model.go`
- `modules/workorder/pkg/workorderservice/http.go`
- `cmd/energy-api/internal/gateway/work_order_mutation.go`
- `apps/hvac-web/src/real/RealWorkOrders.tsx`
- `apps/hvac-web/src/api/work-orders.ts`
- `contracts/http/s5-work-order-public.openapi.json`
- `scripts/check-s5-work-order-baseline.mjs`

## Authority map

| Fact | Authoritative owner | Evidence/input it consumes | What must not create it |
|---|---|---|---|
| Accepted/current telemetry | Telemetry Runtime | authenticated integration observation + canonical Device/Point binding + quality/order policy | Simulator, MQTT publisher, browser |
| Alarm evaluation state | Alarm evaluator | an `EvaluationSnapshot` containing input revision, quality, timestamps and evidence refs | Simulator or frontend |
| Alarm incident | Alarm Service | a publish/clear effect from the Alarm evaluator or another explicit Alarm owner path | Simulator, FDD, Work Order |
| FDD Finding | FDD Service | explicit evidence values and immutable evidence IDs over an evaluation window | Simulator, Alarm, browser |
| Work Order | Work Order Service | exactly one authoritative origin source plus optional related sources | Simulator, Telemetry, FDD |
| Work Order source provenance | Work Order Service | `SourceReference` values supplied at create time | Simulator-specific source domain |
| Cross-domain association | owning domain of the association | existing authoritative resource IDs | source simulator state |

## Findings

### 1. Telemetry remains the first Cloud authority

The MQTT/IoT path ends at `Telemetry Runtime.AcceptObservation`. Telemetry owns canonical binding, duplicate/out-of-order handling, type/unit/freshness validation, quality, source position, latest accepted telemetry, history intent, Presence evidence and Device Observation Snapshot reevaluation.

A physical or simulated fault becomes business-relevant only after its sensor/state symptoms pass this admission boundary. Nothing downstream should consume `Plant.Snapshot`, raw MQTT payloads or simulator state directly.

**Decision:** the Virtual Plant only needs to create realistic physical/measurement symptoms. It never writes Alarm, FDD or Work Order facts.

### 2. Alarm evaluation is a real owner, but its Telemetry bridge is not wired at runtime

The Alarm module already has a substantial durable evaluator:

- `AlarmPolicyRevision` owns rule revision, alarm type, source reference, severity, schedule, freshness, trigger mode, raise and clear predicates.
- `EvaluationSnapshot` carries subject scope, input revision, `AsOf`, typed inputs, quality and evidence references.
- `EvaluatePolicy` handles duration/repeating/no-data/stale/hysteresis behavior and emits `PUBLISH`, `CLEAR` or `NONE`.
- `EvaluateClaim` persists evaluation state and publishes/clears the Alarm in the same Alarm-owned transaction.
- Alarm publication copies the input evidence into the incident and records the rule revision and workload actor `alarm-evaluator`.

However, repository-wide references show `EvaluateAssignedSnapshot`, `ClaimDueEvaluations` and `EvaluateClaim` are currently exercised only by Alarm tests. There is no production worker or subscriber that turns Telemetry-owned snapshots/events into Alarm `EvaluationSnapshot`s or regularly claims due reevaluations.

The public/internal Alarm HTTP handler also exposes reads and lifecycle mutations, not a generic public rule-evaluation write endpoint.

**Gap A — Telemetry → Alarm evaluator bridge is missing.**

This is the first missing production bridge for Virtual Plant acceptance. The correct future seam is a small Alarm-owned ingestion/evaluator worker that consumes authoritative Telemetry read/event facts and builds `EvaluationSnapshot`; it is not a Simulator callback and does not justify a second Alarm API.

### 3. FDD owns Findings, but current Low-Delta-T evaluation is caller-driven

The FDD Service currently implements one concrete rule, `CHILLED_WATER_LOW_DELTA_T`.

`EvaluateLowDeltaT` requires:

- tenant/site/asset scope,
- evaluation window,
- rule revision,
- optional model deployment revision,
- minimum ΔT,
- supply/return temperature evidence carrying `EvidenceID`, signal, timestamp, value and unit.

When the rule matches, FDD persists an `FDDFinding` with evidence IDs, rule/model revision and confidence. This is a correct evidence-oriented owner boundary.

But the only runtime entry is `POST /v1/fdd/evaluate/low-delta-t`, and repository-wide callers are the FDD HTTP handler and tests. No production worker currently queries/consumes Telemetry history, assembles the evidence window and invokes this evaluation automatically.

The Gateway publicly exposes only the FDD findings read route. It does not expose evaluation or link mutation to the browser.

**Gap B — Telemetry/History → FDD evaluation bridge is missing.**

The correct implementation should be an FDD-owned scheduled/event-driven evaluator using authoritative Telemetry/History evidence. Simulator code must not call FDD directly.

### 4. FDD links are associations, not Work Order source authority

`FDDFinding` may store `AlarmID` and `WorkOrderID`, and FDD exposes an internal link mutation that refuses relinking to a different Alarm/Work Order once a link exists.

This does not make FDD the owner of either resource. It is a cross-domain association from the Finding to already-existing authoritative resources.

The Work Order model deliberately does **not** admit `TELEMETRY` or `FDD` as source domains. The baseline gate explicitly asserts that neither is Work Order authority.

**Decision:** do not introduce `SIMULATOR` or `FDD` as Work Order source domains for this effort. A maintenance action should originate from an authoritative Alarm or Investigation, with FDD linked as supporting diagnostic evidence/association where appropriate.

### 5. Work Order already supports the desired Alarm-origin user flow

Work Order creation requires `SourceReferences` and enforces exactly one `ORIGIN`. `ALARM` and `INVESTIGATION` are intended authoritative source domains.

The production frontend already implements the practical flow:

```text
Alarm detail / sourceAlarm navigation
    -> Work Order create
    -> sourceReferences = [{ domain: ALARM, resourceId: alarmId, relationship: ORIGIN }]
```

So the first Virtual Plant acceptance does not need automatic Work Order creation. A user can observe the production Alarm/FDD evidence and create a real Work Order whose origin is the Alarm.

**Decision:** first-slice maintenance completion should prove an operator-created Alarm-origin Work Order. Automatic Alarm/FDD-to-Work-Order policy is a separate product decision, not Simulator behavior.

### 6. Work Order source provenance is not yet authoritatively resolved

`workordermodel.Validate` requires UUIDv7 identities for non-MANUAL/non-EXTERNAL sources, but it does not establish that an `ALARM` or `INVESTIGATION` source actually exists, belongs to the same tenant/site, or is visible to the creating principal.

The public Gateway create path performs the same model validation and forwards the source references. It does not resolve the referenced Alarm/Investigation before creation.

Alarm Service already has an internal scope-resolution route for an Alarm ID, so an authoritative existence/scope check has a natural owner seam; it is simply not used by Work Order create today.

**Gap C — Work Order authoritative source resolution is missing.**

For the final fault-to-maintenance path, an Alarm-origin Work Order should not accept an arbitrary syntactically valid UUID. Source resolution should happen through the owning domain and fail closed; no browser-side check or compatibility fallback should substitute for it.

### 7. Reverse links are incomplete and should remain secondary to source authority

Alarm's model supports `WORK_ORDER` links, but current Alarm lifecycle HTTP/store mutation surface does not expose a dedicated operation to append a Work Order link after Work Order creation.

FDD can link itself to a Work Order internally, but the public Gateway exposes only FDD reads.

Therefore a Work Order can authoritatively reference an Alarm today, while reverse navigation from Alarm/FDD to the Work Order is not fully wired as a production cross-domain flow.

**Gap D — cross-domain reverse association is incomplete.**

This is useful for UX/provenance but must not be confused with Work Order origin authority. The Work Order's source reference is the authoritative maintenance origin; reverse links are secondary projections/associations.

### 8. A real frontend/backend Work Order source-domain drift exists

The public OpenAPI and frontend schema allow:

- `MANUAL`
- `ALARM`
- `ASSET`
- `EQUIPMENT`
- `INVESTIGATION`
- `EXTERNAL`

The baseline gate also explicitly expects `SourceEquipment` in the Go Work Order model.

But the current `libs/workordermodel/model.go` defines and validates only:

- `MANUAL`
- `ALARM`
- `ASSET`
- `INVESTIGATION`
- `EXTERNAL`

`SourceEquipment` is missing.

**Gap E — frontend/OpenAPI/Go source-domain contract drift.**

This must be corrected at the authoritative contract, together, before final Virtual Plant implementation uses Work Order provenance. Do not add browser fallbacks or silently map EQUIPMENT to ASSET.

## Current production-capable path versus missing bridges

What is already structurally valid:

```text
Virtual Plant physical fault
    -> simulated DeviceAdapter
    -> production Edge Process Image
    -> MQTT / IoT
    -> Telemetry Runtime accepted facts
```

What is conceptually implemented but not yet runtime-connected:

```text
Telemetry authoritative facts
    -X-> Alarm EvaluationSnapshot worker
        -> Alarm evaluator
        -> Alarm incident

Telemetry/History evidence
    -X-> FDD evaluation worker
        -> FDD Finding
```

What is already usable for the first maintenance acceptance after an Alarm exists:

```text
Operator sees Alarm (+ FDD evidence)
    -> Create Work Order with ALARM origin
    -> Work Order Service owns lifecycle
```

What still needs authoritative cross-domain strengthening:

```text
Work Order create
    -X-> resolve/verify ALARM or INVESTIGATION source with owner

Work Order created
    -X-> reverse Alarm/FDD association where product UX needs it
```

## Recommended first end-to-end acceptance shape

This research does **not** choose the exact physical fault yet, but it fixes the ownership pattern the eventual scenario must satisfy:

```text
1. Scenario changes physical condition or sensor behavior only.
2. Reacting Plant produces believable symptoms.
3. Production Edge/MQTT/IoT path delivers those symptoms.
4. Telemetry Runtime accepts them as canonical observations.
5. Alarm-owned evaluator creates a real Alarm from Telemetry evidence.
6. FDD-owned evaluator creates a real Finding from Telemetry/History evidence.
7. UI reads both through production APIs.
8. Operator creates a Work Order with ALARM as the single ORIGIN.
9. Work Order source is resolved against the Alarm owner before acceptance.
10. FDD may link to the Alarm/Work Order as supporting evidence; it does not become the Work Order origin.
```

This gives the Work Order system meaningful simulated data without giving the Simulator any maintenance authority.

## Wayfinder implications

The following fog is now sharp enough for downstream decisions:

1. **Physical acceptance fault and rule coverage.** We can now decide which smallest physical disturbance produces evidence usable by both one Alarm policy and one FDD rule. Low chilled-water ΔT is an existing FDD capability and is a strong candidate, but the final choice should be made as a dedicated decision rather than assumed here.
2. **Unified frontend/backend Virtual Plant contract.** The current source-domain drift and Alarm/FDD/Work Order read/link boundaries are now concrete inputs to that decision.
3. **Implementation gaps are owner bridges, not simulator features.** Telemetry→Alarm and Telemetry/History→FDD workers, plus Work Order source resolution, should become implementation tickets only after the Wayfinder reaches `/to-spec`.

## Decision

The authoritative maintenance route is:

```text
Simulator physical cause
    -> production Telemetry truth
    -> production Alarm/FDD owners
    -> Alarm/Investigation-origin Work Order
```

The Simulator must never insert Alarm/FDD/Work Order records or add a simulator-specific maintenance source domain.

The main blockers are missing production bridges and contract drift, not missing simulator abstractions:

- missing Telemetry-to-Alarm evaluation runtime,
- missing Telemetry/History-to-FDD evaluation runtime,
- missing authoritative Work Order source resolution,
- incomplete reverse Alarm/FDD-to-Work-Order association,
- `EQUIPMENT` source-domain mismatch between OpenAPI/frontend and Go Work Order model.

These should be corrected at their owning boundaries with one frontend/backend contract, without defensive aliases or speculative framework layers.
