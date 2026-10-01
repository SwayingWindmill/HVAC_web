# Virtual Central Plant — Unified Frontend/Backend Contract Source Review

Status: WAYFINDER RESEARCH EVIDENCE
Wayfinder map: `wayfinder: virtual central plant`
Research ticket: `Research unified frontend/backend contract for Virtual Plant`
Reviewed: 2026-08-28

## Question

Which existing backend APIs and frontend presentation models should represent Virtual Plant data, Alarm/FDD/Work Order state and the optional Acceptance source badge, and where would any proposed simulator-specific frontend or backend branch violate the single authoritative contract?

## Primary local sources reviewed

- `apps/hvac-web/src/api/generated/platformGateway.gen.ts`
- `apps/hvac-web/src/api/telemetry-current.ts`
- `apps/hvac-web/src/domain/centralPlantTelemetry.ts`
- `apps/hvac-web/src/features/fdd/capability.ts`
- `apps/hvac-web/src/real/RealProductPages.tsx`
- `apps/hvac-web/src/real/RealAlarms.tsx`
- `apps/hvac-web/src/real/RealWorkOrders.tsx`
- `apps/hvac-web/src/api/work-orders.ts`
- `cmd/energy-api/internal/gateway/intelligence.go`
- `cmd/energy-api/internal/gateway/work_order_mutation.go`
- `contracts/registry/central-plant-device-points.v2.json`
- `contracts/http/s5-work-order-public.openapi.json`
- `libs/workordermodel/model.go`
- prior Wayfinder evidence for OpenEMS, Edge authority and fault-to-maintenance ownership.

## Existing vertical slices are already the correct UI contract

### Telemetry / equipment state

The frontend reads production `DeviceObservationSnapshot` contracts through generated clients. `telemetry-current.ts` explicitly validates Device/Tenant/Site scope and rejects unsupported envelopes. The frontend point catalogue only maps canonical telemetry keys to presentation labels/units; it is not an alternate source of state.

**Decision:** Virtual Plant telemetry must appear through the same Device/Point contracts and `DeviceObservationSnapshot` read/live paths as physical equipment. Do not add `virtualValue`, `simulatedTelemetry`, mock fallback or a parallel simulator telemetry API.

### Alarm

Alarm is already a separate production owner with its own read/lifecycle API. The frontend routes from Alarm to Work Order creation using the authoritative Alarm ID.

**Decision:** a simulated physical fault that eventually creates an Alarm must render on the existing Alarm pages and use the existing Alarm API. No simulator Alarm page, status field or browser-generated Alarm is allowed.

### FDD

`FDD_READ_MODEL_BOUNDARY` explicitly declares `authority: fdd-service` and `fallback: none`. The FDD page describes Findings as evidence-backed facts distinct from Alarm and Work Order, and displays rule/model revision plus explicit links.

**Decision:** Virtual Plant-induced FDD results use the same FDD Finding read model. No client-side FDD calculation and no simulator-specific Finding shape.

### Work Order

The frontend already supports the correct first-slice flow: an Alarm detail navigates to Work Orders with `sourceAlarm`, and Work Order create submits an `ALARM` origin source reference. Work Order service remains the lifecycle owner.

**Decision:** the Virtual Plant does not need a separate maintenance UI or Work Order API. It only needs production Alarm/FDD facts to exist so the normal maintenance workflow has meaningful evidence.

## Contract conflicts that must be fixed together

### 1. Source-provided `*.business_revision` is not Business Revision

Prior authority research found the central-plant Registry contract and frontend point catalogue expose simulator-local equipment counters as telemetry points named `*.business_revision`, while `CONTEXT.md` defines Business Revision as the Telemetry-owned Device Observation Snapshot revision.

**Unified correction:** remove/rename the source telemetry points and frontend presentation together. The authoritative Business Revision must come from the Telemetry read model/API only. Do not retain aliases or interpret the old device point differently in frontend code.

### 2. Work Order `EQUIPMENT` source-domain drift

The frontend Zod schema and public OpenAPI admit `EQUIPMENT`, while the authoritative Go `workordermodel` currently omits `SourceEquipment`. The baseline gate itself expects the Go model to contain it.

**Unified correction:** make the authoritative domain vocabulary consistent across Go model, OpenAPI/generated frontend schema and validation in one change. Do not map `EQUIPMENT` to `ASSET`, drop it only on one side, or add compatibility parsing.

### 3. Work Order source provenance must be owner-resolved

A browser can submit an Alarm-origin Work Order, but current create validation proves only UUID shape rather than owner existence/scope. Frontend validation cannot establish authority.

**Unified correction:** source resolution belongs in the backend/Gateway/owner boundary. UI may provide navigation and friendly errors but must not become the source-verification authority.

## Virtual Plant identity and source badge

The current canonical `Site` contract contains operational identity and lifecycle fields only. It does not include deployment/runtime mode. `/api/v1/platform/status` describes Platform Gateway implementation/compatibility state, not per-Site simulator provenance.

Therefore an `isSimulated`, `virtualPlant`, `scenarioName` or similar field should **not** be added to Site, Device, Point, Telemetry Snapshot, Alarm, FDD Finding or Work Order merely to decorate the UI. Those facts have different owners and would pollute business contracts.

The optional Acceptance badge is a deployment/runtime-context concern. If a later acceptance requirement proves that operators must see it, the smallest valid design is:

- one authoritative deployment/runtime metadata fact from the runtime/deployment owner;
- exposed through one generated Gateway contract;
- consumed only by the shared shell/chrome to render a visible `Virtual Plant / Acceptance` indicator;
- no behavior switch in business pages;
- no fallback from browser environment variables when the backend fact is unavailable.

For the first vertical slice, **no new badge API is required** unless the acceptance UX explicitly requires it. Avoid creating an API solely for decorative context.

## Capability and navigation implications

The current principal capability contract is generated and versioned (`capabilitySetVersion=11`). Virtual Plant is not a new business capability: a user still needs the same `telemetry.*`, `alarm.*`, `work-order.*`, Registry and intelligence permissions.

**Decision:** do not add `virtual-plant.read`, `simulator.read` or similar IAM capabilities just to display production data sourced from the Virtual Plant. Simulator lifecycle/configuration tooling, if later exposed to users, is a separate acceptance/developer control surface and should be designed only when that use case is confirmed.

Likewise, do not add parallel navigation such as `Virtual Dashboard`, `Virtual Alarms`, `Virtual FDD` or `Virtual Work Orders`. Existing Site routes remain canonical.

## Unified contract matrix

| Concern | Backend authority | Frontend representation | Virtual Plant-specific branch? |
|---|---|---|---|
| Site/Asset/Device/Point identity | Registry | existing Site/Asset/Device views | No |
| Current telemetry / Presence / Business Revision | Telemetry Runtime | existing `DeviceObservationSnapshot` clients and central-plant point presentation | No |
| Alarm incident/lifecycle | Alarm Service | existing Alarm pages | No |
| FDD Finding | FDD Service | existing FDD page/read model | No |
| Work Order provenance/lifecycle | Work Order Service | existing Work Order page, Alarm-origin creation flow | No |
| Forecast/Optimization | their production owners | existing intelligence pages | No |
| Acceptance/Virtual Plant indication | deployment/runtime metadata owner, only if needed | shared shell badge | At most one presentation-only metadata seam |
| Scenario configuration/control | simulator/acceptance runner | not a business page in first slice | Separate developer/acceptance control surface only if later required |

## Things the frontend must not infer

The frontend must not infer Virtual Plant state from:

- Device codes or naming conventions;
- presence of known simulator telemetry keys;
- localhost/hostname;
- environment variables that disagree with backend runtime truth;
- absence of physical protocol metadata;
- fixture/demo data;
- Alarm/FDD content.

Such inference would create a second truth source and defensive behavior.

## Things the backend must not expose

The backend must not add simulator-specific variants of production business contracts, including:

- simulated/current dual telemetry fields;
- simulator flags on Alarm/FDD/Work Order business records;
- fallback-to-simulator reads when production data is absent;
- old/new field aliases to preserve prototype shapes;
- browser-only endpoints that bypass the owning domain.

## Resulting vertical acceptance contract

The first live Virtual Plant acceptance can use only existing production business contracts:

```text
Virtual physical symptoms
    -> Registry-bound Device/Point observations
    -> Telemetry DeviceObservationSnapshot
    -> existing Dashboard/Asset telemetry UI
    -> Alarm owner/read model
    -> FDD owner/read model
    -> existing Alarm -> Work Order create flow
    -> Work Order owner/read model
```

If an Acceptance badge is eventually required, it sits beside this path in Shell metadata; it does not participate in the path.

## Decision

The frontend/backend architecture for Virtual Plant is not a new product surface. It is a test/acceptance source feeding existing production owners and their generated contracts.

The final implementation route should therefore:

1. repair known contract drift at the authoritative owner and regenerate/update frontend together;
2. keep all business pages on existing production APIs;
3. add no simulator-specific business fields, routes, fallbacks or IAM capabilities;
4. keep any optional Virtual Plant badge as minimal deployment/runtime metadata rendered by the shared shell, and omit it entirely from the first slice unless it is required for acceptance clarity;
5. validate each implementation ticket as one frontend/backend vertical slice rather than separate backend and frontend adaptation phases.
