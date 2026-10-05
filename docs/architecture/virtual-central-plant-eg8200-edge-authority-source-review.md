# Virtual Central Plant — EG8200 / Edge Authority Source Review

Status: WAYFINDER RESEARCH EVIDENCE
Wayfinder map: `wayfinder: virtual central plant`
Research ticket: `Research current EG8200 and Edge runtime authority seams`
Reviewed: 2026-08-28

## Question

What are the current authoritative seams between the EG8200 simulator, `DeviceAdapter`, Process Image, Controller/Scheduler, MQTT, IoT ingest and Telemetry Runtime, and which parts of the existing Scenario prototype align or conflict with those seams?

## Primary local source reviewed

- `GLOSSARY.md` — `Device Observation Snapshot`, `Business Revision`, `Source Position`.
- `libs/edgecontrol/driver.go` — production-facing `DeviceAdapter`, `DeviceHost`, poll and write boundaries.
- `libs/edgecontrol/cycle.go` — Process Image, Scheduler, Controller and write phase ordering.
- `tools/eg8200-simulator/internal/simulator/edge_driver.go` — simulated device implementation of `DeviceAdapter`.
- `tools/eg8200-simulator/internal/simulator/edge_runtime.go` — simulator composition of the production Edge runtime.
- `tools/eg8200-simulator/internal/simulator/model.go` and `command.go` — physical Plant state and command reaction.
- `tools/eg8200-simulator/internal/simulator/datasource.go` — current uncommitted Scenario prototype.
- `tools/eg8200-simulator/internal/simulator/measurement.go` — Process-Image-derived sampling/publish scheduler.
- `tools/eg8200-simulator/internal/simulator/mqtt_publisher.go` and `cmd/eg8200-mqtt-publisher/main.go` — MQTT transport and live cycle runner.
- `modules/iot/pkg/adapter/processor.go` — MQTT integration boundary.
- `modules/telemetry/pkg/telemetry/ingest.go` and `ingest_store.go` — canonical Telemetry admission and persistence owner.
- `scripts/central-plant-spatial-model.mjs` and `contracts/registry/central-plant-device-points.v2.json` — generated central-plant Device/Point contract.
- `apps/hvac-web/src/domain/centralPlantTelemetry.ts` — current frontend point presentation.
- Existing upstream adjudication: `docs/architecture/openems-source-review.md` and `docs/architecture/virtual-central-plant-openems-simulator-source-review.md`.

## Authority map

| Layer | Owns | Does not own |
|---|---|---|
| Scenario / Plant Datasource | Exogenous physical inputs for the simulator, such as outdoor dry bulb, wet bulb and cooling-load demand | Device identity, Point identity, Telemetry quality/current truth, Alarm, FDD, Work Order, frontend business state |
| Reacting `Plant` | Coupled thermal/hydraulic/electrical state and simulated sensor/actuator behavior | Production Controller decisions, Cloud Business Revision, authoritative Telemetry state |
| `DeviceAdapter` | Production-facing physical/simulated device boundary: typed Channels, Poll and arbitrated Apply | Business APIs or simulator-specific control policy |
| Channel Runtime / Process Image | Stable per-cycle Edge observation image | Cloud current-state authority or mutable post-write Plant state |
| Scheduler / Controllers / ControlPlan | Deterministic governed control decision and constraints | Direct simulator-only command path |
| Device output write | Application of accepted effective decisions to the owning adapter | Independent Cloud command success verification |
| Measurement Scheduler / MQTT Publisher | Sampling cadence, wire envelope, offline queue and transport evidence | Canonical device/point mapping, accepted quality, current business state |
| IoT MQTT adapter | Topic/envelope validation, Integration/Gateway/child authorization, conversion to an Observation candidate | Telemetry acceptance or Device identity creation |
| Telemetry Runtime | Canonical mapping, validation, source ordering, quality, latest accepted observation, Presence, Device Observation Snapshot and Business Revision | Physical simulation or transport behavior |
| Frontend | Presentation of production API/read-model facts | Simulator-generated alternate truth |

## Source findings

### 1. `DeviceAdapter` is already the correct simulated/physical seam

`libs/edgecontrol/driver.go` explicitly defines `DeviceAdapter` as the production-facing contract shared by physical and simulated drivers. Both expose `Component`, typed `Channels`, `Poll` and arbitrated `Apply`. A future physical driver may delegate transport to a protocol bridge without changing Controller or DeviceHost behavior.

The EG8200 `simulatedDeviceAdapter` already obeys that seam. `Poll` reads the Plant and emits typed `ChannelUpdate`s; `Apply` receives production `Decision`s and translates them into `Plant.ApplyCommand`. MQTT callbacks do not mutate the Plant directly.

**Decision:** keep the Virtual Plant completely below `DeviceAdapter`; no simulator-specific Controller/Scheduler/API branch is justified.

### 2. Process Image is the Edge-cycle observation authority

`DeviceHost.PollOnce` writes only Channel next-values. `Cycle.RunOnce` then switches the Process Image before running Controllers and performs device writes only after Controller execution. `EdgeControlRuntime.RunCycle` explicitly derives the outbound telemetry snapshot from `cycleResult.Image`, not from a new `Plant.Snapshot()` after the current actuator write.

The live runner currently performs:

```text
Plant.Tick(interval)
  -> DeviceAdapter.Poll
  -> Switch Process Image
  -> Controllers / ControlPlan
  -> DeviceAdapter.Apply
  -> publish Process-Image snapshot
```

This gives the required reacting causality: a command applied in cycle N changes physical state; a later physical tick/poll exposes the resulting state in a later Process Image. The implementation does not need a new Cycle hook merely to copy OpenEMS' event name if this semantic boundary is preserved.

**Decision:** describe and test the causal boundary, not an upstream framework shape. Scenario progression belongs between stable Process Images and must never mutate the current Process Image mid-cycle.

### 3. MQTT and IoT are transport/integration seams, not truth owners

The MQTT publisher serializes the normal Process-Image-derived measurements into the existing `energy/v1/{tenant}/{site}/{gateway}/telemetry` envelope. The live publisher sets `replay=false` and uses the same point codes/unit/quality/timestamps as other MQTT sources.

`modules/iot/pkg/adapter/processor.go` validates the topic and envelope, verifies the active Integration/Gateway/child bindings, and turns each point into an Observation candidate. It does not create unknown child Devices and does not decide whether a value becomes current truth.

**Decision:** Virtual Plant data must continue to enter the Cloud through this normal integration boundary for live acceptance. No `SIMULATOR` ingest mode or direct database path is needed.

### 4. Telemetry Runtime is the authoritative Cloud owner

`EvaluateObservation` and `PostgresStore.AcceptObservation` own the canonical mapping from external source identity/key to Device/Point, source ordering, duplicate/out-of-order handling, type/unit/time validation, quality, quarantine, latest accepted telemetry, Presence evidence, history intent and Device Observation Snapshot reevaluation.

Only after this transaction may an incoming MQTT value become authoritative current business state. The returned `BusinessRevision` comes from the Telemetry-owned Device Observation Snapshot revision.

**Decision:** all downstream Alarm/FDD/Forecast/Optimization/Work Order/frontend behavior must consume Telemetry-owned facts/read models, never Plant or MQTT publisher state.

### 5. Registry Point identity remains outside the Scenario

`central-plant-spatial-model.mjs` derives Device/Point/Sensor/subject configuration from the existing central-plant contract. The current Scenario prototype only changes `PlantConfig.datasource`; it does not invent new Device or Point identities.

**Decision:** Scenario configuration is an acceptance/simulator configuration contract only. It must not become a Registry, Telemetry or frontend business contract.

## Existing Scenario prototype adjudication

### ALIGNED — keep the behavior

1. **Typed exogenous inputs only.** `PlantInputs` currently contains outdoor dry bulb, wet bulb and cooling-load fraction; these are causes, not equipment outcomes.
2. **Datasource stays inside the physical Plant.** `Plant` asks for inputs by elapsed scenario time; downstream DeviceAdapter/Edge/MQTT/Telemetry code is unchanged.
3. **Explicit STATIC and SCENARIO modes.** This is clearer than a hidden fallback and supports deterministic tests plus live acceptance.
4. **Simple stepwise records.** The prototype selects the latest record at or before elapsed time and holds the last record after the scenario ends. No interpolation engine or scripting layer is introduced.
5. **Strict config decoding.** Unknown fields are rejected and schema version is explicit; there is no old-shape compatibility fallback.
6. **Canonical generated central-plant identity remains the source of Device/Point metadata.** Scenario does not own those identities.
7. **Constructor failure is explicit.** Invalid datasource configuration fails Plant startup rather than silently substituting static/default values.
8. **The scenario changes physical results only.** Weather and load influence the reacting thermal/electrical model and become normal device observations through the existing Edge path.

### PROTOTYPE DETAIL — do not promote without a current requirement

1. **Seven-day hard limit.** `PlantDatasourceConfig.Validate` rejects offsets beyond seven days. No current production/acceptance authority requires this limit; it is a defensive arbitrary cap and should not enter the final contract unless a concrete bounded-use requirement justifies it.
2. **Required scenario `name`.** A descriptive scenario name can help diagnostics or selection, but it is not a business identity. The final contract should require it only if the runner actually uses it; it must never become Registry identity or business provenance.
3. **Generic datasource extensibility.** The `PlantDatasource` seam is useful because STATIC and SCENARIO are two real modes, but it should remain a tiny internal seam. Do not add a registry, plugin loader, arbitrary key map, factory hierarchy or compatibility aliases.
4. **Exact file-format surface.** The current JSON shape is a prototype. Whether CSV is also first-slice input and exactly where scenarios live belongs to the now-specifiable Scenario-contract decision, not to this authority research.

### CONFLICT — must be corrected before the final implementation route

#### Simulator `businessRevision` conflicts with the platform domain language

`GLOSSARY.md` defines **Business Revision** as the monotonic owner-authored revision of one Telemetry `Device Observation Snapshot`, advancing only when committed current runtime state changes.

The older central-plant simulator independently increments per-equipment `revision` fields, returns them as `CommandResult.BusinessRevision`, emits them from Plant snapshots as `businessRevision`, registers them as telemetry points such as `chiller.business_revision`, and the frontend displays those source points as “业务版本”. These values are not Telemetry Business Revisions: they are local simulated equipment-state counters.

This is not merely a naming nit. It creates two different facts with the same ubiquitous-language term and lets a source-provided integer appear beside the actual Telemetry-owned snapshot revision.

**Decision:**

- `Business Revision` remains exclusively owned by Telemetry Runtime as defined in `GLOSSARY.md`.
- Virtual Plant internal state counters must not be called `BusinessRevision`.
- If an internal counter is still useful, call it a simulator/physical/device state revision inside the simulator.
- Unless a real device protocol exposes an equivalent register with independent operational meaning, do not publish that internal counter as a canonical central-plant telemetry Point.
- The Registry point contract and frontend point catalogue must eventually remove/rename the current `*.business_revision` source points together; no compatibility alias is required. The authoritative Telemetry snapshot revision is supplied by the production read model/API, not by a device telemetry key.

This correction directly supports the standing frontend/backend unification rule: both sides use one Business Revision contract from the same owner.

## Authoritative live flow

```text
Scenario / external conditions
        |
        v
small typed Plant datasource
        |
        v
Reacting coupled Plant
        |
        v
simulated DeviceAdapter.Poll
        |
        v
Channel.nextValue
        |
        v
immutable Edge Process Image
        |
        +--> production Scheduler / Controllers / ControlPlan
        |                     |
        |                     v
        |              DeviceAdapter.Apply
        |                     |
        |                     v
        |                Plant command
        |
        v
Process-Image-derived Measurement
        |
        v
existing MQTT envelope / spool
        |
        v
IoT adapter: auth + wire-to-candidate only
        |
        v
Telemetry Runtime AcceptObservation
        |
        +--> canonical Device/Point binding
        +--> ordering / quality / quarantine
        +--> latest accepted telemetry / history
        +--> Presence / Device Observation Snapshot
        +--> canonical Business Revision
        |
        v
production business APIs / Alarm / FDD / frontend
```

## Authoritative command and readback flow

```text
Cloud governed command
  -> existing MQTT command downlink
  -> EdgeControlRuntime.SubmitCommand
  -> IntentStore
  -> production Controllers / Arbiter / constraints
  -> DeviceOutputWriter
  -> simulated DeviceAdapter.Apply
  -> Plant.ApplyCommand
  -> later Plant.Tick / Poll / Process Image
  -> MQTT uplink
  -> Telemetry Runtime
  -> independent Cloud readback / command verification
```

A simulator-specific immediate success/readback path would violate this ownership chain.

## Fog cleared by this research

Together with `Research OpenEMS simulator seams for HVAC adoption`, the local authority boundary is now clear enough to phrase the exact Scenario-contract decision precisely:

- the contract is simulator/acceptance-only;
- it sits below `DeviceAdapter`;
- it contains only typed exogenous inputs and scenario timing;
- it may provide explicit STATIC and stepwise SCENARIO modes;
- it must not define Device/Point identities, business results, Cloud revisions or frontend state;
- it must not carry compatibility aliases/default fallbacks or arbitrary defensive limits.

The exact minimal v1 configuration/file surface is a design decision and should graduate from the map's fog into its own Wayfinder decision ticket rather than being silently frozen from the prototype.

## Decision

The current platform already has the right authority chain. The Virtual Central Plant should **reuse it unchanged**:

- Plant/Datasource owns only physical causes and physical state;
- `DeviceAdapter` is the simulated/real device seam;
- Process Image is the stable Edge-cycle observation boundary;
- production Scheduler/Controllers own governed control;
- MQTT/IoT own transport and source adaptation;
- Telemetry Runtime owns canonical Cloud observation truth and Business Revision;
- frontend and downstream business domains consume production owner facts.

The Scenario prototype is directionally aligned and should remain evidence, but its arbitrary seven-day cap, descriptive-name requirement and exact JSON surface have no incumbency preference. The existing simulator/device `businessRevision` point is an actual authority/ubiquitous-language conflict and must not survive into the final Virtual Plant contract.
