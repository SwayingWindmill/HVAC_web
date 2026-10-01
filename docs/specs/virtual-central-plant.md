# Virtual Central Plant Specification

Status: READY FOR TICKETING
Wayfinder map: https://github.com/SwayingWindmill/HVAC_web/issues/320
Source decisions: #321–#330
Prepared: 2026-08-28

## Problem Statement

HVAC_web needs a Virtual Central Plant that can exercise the real platform from physical behavior through Edge control, telemetry, diagnostics, maintenance and frontend presentation without creating a parallel simulator product or synthetic business truth.

The current repository already contains important building blocks: canonical Registry Asset/Device/Point identity, Edge Channel/Capability/Process Image/Controller abstractions, MQTT/IoT ingestion, Telemetry Runtime, Alarm, FDD, Work Order, Energy, Forecast and Optimization domains, plus an EG8200 simulator prototype. However, the prototype is not yet an authoritative implementation contract. It contains simulator-local assumptions, a non-canonical `loadFraction`, simulator `businessRevision` leakage, and an in-memory DeviceAdapter path that cannot prove a real production field-protocol driver.

The platform also has real integration gaps that a credible Virtual Central Plant must expose rather than bypass: there is no production Telemetry-to-Alarm evaluation bridge, no production Telemetry/History-to-FDD worker, no production-neutral Edge host running `edgecontrol`, no concrete production Modbus/TCP Bridge or physical Modbus DeviceAdapter, and no server-owned Forecast/Optimization input-snapshot builders. Work Order source validation/reverse association and an EQUIPMENT source-domain contract drift must also be corrected at their authoritative owners.

The required result is therefore not a demo that looks correct. It is a development and acceptance environment in which the same production controllers, protocol drivers, telemetry ownership, business APIs and frontend surfaces used for real devices can be exercised against a reacting physical plant. Missing production behavior must stay missing until implemented at the correct owner; the simulator must never fill the gap with compatibility fallback, fixtures or direct domain writes.

## Solution

Build a non-production-authoritative Virtual Central Plant that models exogenous conditions, central-plant physical behavior, sensors/actuators and later a real Modbus/TCP field-protocol endpoint while reusing all production owners above the Device/Protocol boundary.

The first live slice runs at 1× wall-clock and models a central plant containing Chiller, chilled-water pump, cooling-water pump, cooling tower, electrical meter, BTU meter and weather station behavior. The Virtual Plant receives an explicit Scenario containing only outside dry-bulb temperature, outside wet-bulb temperature and total central-plant cooling demand. It reacts to governed production control outputs, produces physically related observations with meaningful dynamics, and publishes those observations through the existing Edge/MQTT/Telemetry path.

The first end-to-end acceptance disturbance is a CHWP VFD actuator stuck-high condition. The model must derive increased chilled-water flow and reduced chilled-water delta-T from physical relationships instead of inserting a diagnostic outcome. Production Telemetry then drives a distinct Alarm policy and the existing `CHILLED_WATER_LOW_DELTA_T` FDD rule. An operator creates an ALARM-origin Work Order through the normal frontend. Physical recovery clears the current condition while retaining durable Alarm/FDD/Work Order history.

Historical data generation is a separate Historical Replay runner. It shares Plant/Scenario physics but submits observations to a Telemetry-owned `HISTORY_REPLAY` history-only admission path with historical Sampled At and truthful current Received At. Replay can populate canonical history for Energy/Metric/Forecast preparation without touching current latest telemetry, Presence or Business Revision and without directly writing downstream intelligence stores.

Protocol-level acceptance is a second, stronger seam. The first real protocol tracer is Schneider Electric Altivar Process ATV630 over Embedded Modbus TCP. A production-neutral Edge host, one production Modbus/TCP Bridge and one production ATV630 DeviceAdapter use a released Device Template / Protocol Mapping. A Virtual ATV630 Modbus/TCP slave exposes the exact same released mapping over real TCP and is connected to the reacting CHWP physical model. The production Bridge and DeviceAdapter must run unchanged when the Virtual slave is replaced later by real ATV630 hardware.

The Virtual Central Plant exists only in explicit development/acceptance deployment profiles. Production deployment never starts it and never uses it as a fallback source. Existing production frontend pages remain canonical; no Virtual Dashboard, Virtual Alarm, Virtual FDD or simulator-specific Work Order surface is introduced.

## User Stories

1. As a commissioning engineer, I want to run a central plant against realistic changing outdoor conditions and cooling load, so that I can validate control behavior before field hardware is available.
2. As a controls engineer, I want the simulated equipment to react to the same production Controller decisions used for real devices, so that simulator-only control branches cannot hide integration defects.
3. As a controls engineer, I want pump, fan, temperature and equipment behavior to have meaningful time constants, so that acceptance reflects dynamic plant response rather than instantaneous value substitution.
4. As a controls engineer, I want commanded values and measured readback to be distinct, so that command success does not falsely imply physical outcome.
5. As an energy engineer, I want cooling demand expressed as `coolingLoadKw`, so that plant demand is independent of the rated capacity of any one chiller.
6. As an energy engineer, I want supply/return temperatures, flow, power and cooling capacity to follow credible energy relationships, so that Energy and FDD calculations receive physically coherent data.
7. As an operator, I want the normal Dashboard and Asset pages to display Virtual Plant telemetry through production APIs, so that acceptance uses the same user experience as a real site.
8. As an operator, I want unavailable production data to remain unavailable, so that the browser never fills gaps with simulator fixtures or local calculations.
9. As an operator, I want faults to appear as real telemetry symptoms rather than simulator-authored alarms, so that Alarm and FDD ownership is tested honestly.
10. As an operator, I want a CHWP actuator stuck-high disturbance to produce excessive chilled-water flow and low delta-T through plant physics, so that I can validate a credible maintenance scenario.
11. As an operator, I want the low-return-temperature Alarm to be created by the production Alarm evaluator only after its configured duration, so that simulator code cannot manufacture incidents.
12. As an operator, I want the existing low-delta-T FDD rule to consume production supply/return evidence, so that diagnostic findings retain canonical evidence provenance.
13. As an operator, I want to create a Work Order from the Alarm using the normal frontend flow, so that maintenance provenance remains ALARM-origin rather than SIMULATOR-origin.
14. As a maintenance engineer, I want FDD evidence linked to the same Alarm and Work Order, so that the maintenance record retains the diagnostic context without changing its source domain.
15. As a maintenance engineer, I want physical recovery to clear the current Alarm/FDD condition without auto-completing the Work Order, so that maintenance workflow remains a human/business decision.
16. As an auditor, I want historical FDD Findings and Alarm Incidents retained after recovery, so that the system preserves what happened rather than rewriting history.
17. As a frontend engineer, I want Virtual Plant data to use existing production contracts, so that I do not maintain alternate simulator schemas.
18. As a frontend engineer, I want any future Acceptance/Virtual Plant badge to come from deployment/runtime metadata rather than Device/Site business fields, so that source context does not contaminate domain contracts.
19. As a Registry administrator, I want the Virtual Plant to reuse canonical Asset, Device, Point and optional Physical Sensor identities, so that simulated acceptance does not create a second asset model.
20. As a Registry administrator, I want protocol mappings to come from released Device Templates, so that register addresses are not duplicated in Scenario or frontend configuration.
21. As an integration engineer, I want a production-neutral Edge host that can load production DeviceAdapters independently of the simulator executable, so that the same Edge runtime can serve real hardware.
22. As an integration engineer, I want Modbus connection lifecycle and transaction behavior owned by a reusable Modbus/TCP Bridge, so that vendor DeviceAdapters do not each rebuild transport loops.
23. As an integration engineer, I want vendor raw register mapping owned by the production DeviceAdapter/Device Template while Controllers consume semantic Channels, so that Controller logic stays vendor-independent.
24. As an integration engineer, I want a real TCP connection between the production Modbus master and Virtual ATV630 slave, so that protocol acceptance does not collapse into direct in-memory method calls.
25. As an integration engineer, I want the same production ATV630 DeviceAdapter to connect to both the Virtual slave and later real ATV630 hardware, so that protocol acceptance proves a production implementation rather than a simulator adapter.
26. As an integration engineer, I want the first ATV630 template limited to the minimum read/write variables required for closed-loop acceptance, so that the first driver is deep and maintainable rather than a speculative 1200-parameter import.
27. As a controls engineer, I want production semantic commands `START`, `STOP`, `RESET_FAULT` and `SET_FREQUENCY` translated by the ATV630 DeviceAdapter into CiA402/DriveCom behavior, so that Controllers never manipulate command words directly.
28. As a controls engineer, I want current `faultCode` derived from active drive state plus the Schneider fault register, so that a retained last fault is not misrepresented as a current fault.
29. As a platform engineer, I want released Device Template revisions immutable, so that a protocol mapping used in production cannot silently change.
30. As a platform engineer, I want real-hardware Vendor Template Certification recorded separately from Template RELEASED state, so that virtual protocol acceptance is never falsely presented as hardware certification.
31. As a platform engineer, I want an incorrect released mapping corrected by a new revision rather than aliases or dual-register probing, so that configuration remains authoritative and understandable.
32. As a developer, I want a small explicit Scenario contract with `STATIC` and `SCENARIO` modes, so that simulator inputs remain easy to review and reproduce.
33. As a developer, I want JSON to be the complete Scenario document format and CSV only a fixed step-series input surface, so that there is one typed domain model rather than two competing contracts.
34. As a developer, I want Scenario steps to be stepwise, non-looping and hold the final value, so that behavior is deterministic and does not invent interpolation semantics.
35. As a developer, I want required physical inputs to fail clearly when missing or invalid, so that the implementation does not silently substitute defaults.
36. As a developer, I want no arbitrary simulation-duration or load-fraction caps, so that validation reflects domain invariants rather than defensive guesses.
37. As a data engineer, I want Historical Replay to preserve historical Sampled At and truthful Received At, so that event time and ingestion time remain distinguishable.
38. As a data engineer, I want Historical Replay to resolve the historically applicable Point binding while using a currently authorized replay integration, so that history uses correct Point revision without fabricating old source authorization.
39. As a data engineer, I want valid historical data to avoid live source-lag STALE classification, so that intentional backfill remains usable without creating a new quality enum.
40. As a data engineer, I want replay idempotency based on stable dataset/partition/offset/event identity, so that retries and restarts do not duplicate history.
41. As a Telemetry owner, I want Historical Replay to persist source observation/history intent without replacing latest, emitting Presence or advancing Business Revision, so that history import never mutates current Device truth.
42. As an analytics engineer, I want replayed history to flow through existing event-time Energy and Metric projections, so that late-data correction behavior is exercised instead of bypassed.
43. As a Forecast engineer, I want forecasts to consume server-owned immutable input snapshots built from authoritative history, so that a simulator cannot directly author forecast inputs or results.
44. As an Optimization engineer, I want optimization to consume server-owned SEALED input snapshots and issue commands through Command Governance, so that a browser or simulator cannot fake the optimization loop.
45. As a deployment engineer, I want the simulator available only in explicit acceptance/development profiles, so that production compose cannot accidentally start it.
46. As a deployment engineer, I want the live acceptance stack to run in the actual WSL single-node deployment shape with real network services, databases and MQTT, so that passing tests correlate with the environment being deployed now.
47. As a deployment engineer, I want protocol-level acceptance to use real TCP sockets between separately running components, so that container/network/configuration defects are visible before hardware testing.
48. As a production operator, I want production deployment to have no simulator fallback path, so that a failed real source stays failed instead of showing fabricated values.
49. As a Go maintainer, I want every Go implementation change to follow the project module's Go version and the installed JetBrains Modern Go Guidelines, so that new code uses current language/stdlib idioms without exceeding the configured toolchain.
50. As a Go maintainer, I want implementation code to be straightforward and owner-oriented instead of defensive compatibility code, so that failure modes are explicit and modules stay deep and understandable.
51. As a reviewer, I want OpenEMS source used as the primary implementation reference for Simulator, Cycle, Bridge, Driver/Channel and ModbusSlave patterns, so that architecture remains grounded in a mature real system.
52. As a reviewer, I want OpenEMS concepts adapted to HVAC_web domain ownership rather than its OSGi/framework infrastructure copied wholesale, so that the repository remains simple and native to its current Go architecture.
53. As a reviewer, I want the first slice tested at a few high-value seams rather than a large speculative matrix, so that testing proves behavior without becoming the architecture.
54. As a product owner, I want implementation completion defined by an observable end-to-end acceptance flow, so that backend-only or frontend-only partial work cannot be called done.

## Implementation Decisions

1. **Authority boundary.** Virtual Central Plant is a non-production-authoritative physical/environment/protocol simulator. It may create exogenous inputs, physical states and device-level observations. It never directly authors Telemetry current truth, Alarm, FDD, Forecast, Optimization, Notification or Work Order business facts.

2. **Primary source architecture.** OpenEMS remains the primary source reference. Adopt the conceptual separation of Datasource/exogenous inputs, reacting physical simulator, production semantic Channels/Natures, production Controllers, protocol Bridge, vendor Driver and optional `ModbusSlave` endpoint. Adapt these concepts into the existing Go `edgecontrol`, Registry and platform owners. Do not copy OSGi/component-framework machinery.

3. **Cycle semantics.** Preserve the existing Input → Process Image → Controllers → Write cycle. Protocol reads are synchronized before Process Image and writes at the execute-write phase, following the same causality as OpenEMS. Commands affect later readback through physical/protocol behavior rather than mutating the current Process Image.

4. **Runtime clock.** Live simulation runs at 1× wall-clock. All live observed/sample times use the runtime clock. Accelerated time is not a live mode; it belongs only to the separate Historical Replay runner.

5. **Plant model fidelity.** v1 is an engineering-grade behavioral model, not a manufacturer digital twin. It must model useful energy relationships, equipment constraints, dynamic response, startup/shutdown/ramp effects where meaningful, closed-loop command/readback and fault behavior. It does not implement CFD or vendor compressor internals.

6. **Equipment modeling.** Model Chiller, chilled-water pump, cooling-water pump, Cooling Tower, Meter, BTU Meter and Weather Station as concrete equipment behaviors. Do not introduce a generic equipment plugin framework until a second real use case demonstrates a reusable abstraction.

7. **Scenario contract.** v1 has explicit `STATIC` and `SCENARIO` modes. The exact required exogenous inputs are ambient dry-bulb temperature, ambient wet-bulb temperature and total central-plant cooling load in kW. The former `loadFraction` prototype concept is removed rather than supported as an alias. Occupancy is not a central-plant input in v1.

8. **Scenario progression.** SCENARIO uses strictly ordered step records beginning at zero. Values are held until the next step; there is no interpolation, automatic loop or implicit reset. After the final step, the last input set remains active.

9. **Scenario formats.** JSON is the complete canonical Scenario document. CSV is only a fixed SCENARIO step-series parser into the same typed model. CSV does not support aliases, metadata extensions, dynamic columns or plugins. Required inputs have no silent defaults.

10. **Scenario validation.** Enforce only real invariants: finite numeric values, wet bulb not greater than dry bulb, cooling load not negative, valid mode shape, ordered offsets. Do not add arbitrary maximum duration, arbitrary load caps or historical compatibility forms.

11. **Fault injection.** Disturbances belong to physical device/measurement behavior. They must change physics, actuator effectiveness or measurement behavior rather than write Alarm/FDD outcomes. Faults and environmental/load changes remain one Scenario concept, not separate generic frameworks.

12. **First maintenance tracer.** The first physical disturbance is CHWP-01/VFD actuator stuck-high. Actual pump frequency remains high despite a lower governed target; flow rises and the plant derives low delta-T through physical energy relationships.

13. **First FDD tracer.** Reuse the existing `CHILLED_WATER_LOW_DELTA_T` rule revision with a 5 °C minimum delta-T for the acceptance policy. Its authoritative evidence is production BTU supply- and return-water temperature evidence.

14. **First Alarm tracer.** The first Alarm is a directly observable operational symptom, not a duplicate FDD calculation: low return-water temperature under meaningful chiller load. For the acceptance policy revision, raise when the chiller is running, cooling capacity is at least 30% rated and BTU return-water temperature is at or below 10.5 °C for five minutes; clear at or above 11.5 °C or when the chiller is not running. These thresholds belong to a versioned policy, not universal code constants.

15. **Minimum business evidence.** Acceptance requires production Points for BTU supply temperature, return temperature and flow; CHWP actual frequency and flow; and Chiller cooling capacity and run state. No `simulatorFault`, `faultInjected`, `expectedDiagnosis`, synthetic delta-T or other acceptance-only business fields are added.

16. **Alarm/FDD runtime bridges.** Implement the missing production path from Telemetry-owned snapshots/events into Alarm evaluation and from Telemetry/History into FDD evaluation. These bridges live at the respective owner boundaries and cannot be replaced with simulator direct calls.

17. **Work Order provenance.** The operator creates the maintenance Work Order from the authoritative Alarm Incident with source domain `ALARM`. There is no `SIMULATOR` or `FDD` Work Order source. Implement authoritative source resolution/tenant/site validation and formal reverse associations rather than accepting shape-only UUID references.

18. **Recovery semantics.** Removing the physical disturbance lets measured frequency/flow/temperature recover through plant dynamics. Alarm condition becomes CLEARED and a later FDD evaluation is CLEAR. Historical Finding/Incident remains. Work Order completion remains a maintenance workflow action and is never automatic simulator behavior.

19. **Registry authority.** Reuse canonical Asset, Device, Point and optional Physical Sensor identity. Point remains owned by Device. Equipment Assets are Measured Subjects when appropriate. Simulator configuration must not create an alternate Device/Point identity layer.

20. **Remove simulator Business Revision leakage.** Any simulator-local physical revision counter must not be published as canonical `*.business_revision` Points or frontend business revision. Platform Business Revision remains exclusively Telemetry Runtime Device Observation Snapshot authority.

21. **Frontend contract.** Existing Dashboard, Registry/Assets, Alarm, FDD, Energy and Work Order production APIs/pages remain canonical. Fix backend-owner and frontend contracts together when drift exists. No simulator-specific business API, capability, navigation tree or data fallback is introduced.

22. **Acceptance badge.** No new badge API is required in the first slice. If operational UX later requires explicit Virtual Plant/Acceptance source context, expose one tiny deployment/runtime metadata contract in shared shell UI; do not add `isSimulated` fields to Site/Device/Telemetry/Alarm/FDD/Work Order.

23. **Production-neutral Edge host.** Extract/provide a deployable Edge host around the existing Runtime, ComponentRegistry, DeviceHost, IntentStore, Scheduler, Cycle and Timedata concepts without Plant/Scenario dependency. Existing simulator bootstrap is implementation evidence, not the production host contract.

24. **MQTT remains Edge-to-Cloud transport.** Keep MQTT/TLS and existing IoT/Telemetry authority as the canonical Edge-to-Cloud path. OpenEMS Backend WebSocket is not adopted. The in-memory Virtual Plant DeviceAdapter path is retained for fast physics/controller/business acceptance but is never a production fallback.

25. **Modbus Bridge.** Implement one concrete production Modbus/TCP Bridge responsible for TCP master lifecycle, bounded protocol timeout/retry, Modbus transactions/read tasks/write tasks and Cycle synchronization. Do not build a generic multi-protocol framework in advance of a second protocol.

26. **Protocol mapping authority.** Registry released Device Template/Point Template/Protocol Mapping/Command Definition data is the authoritative vendor mapping vocabulary. Simulator Scenario and frontend never own register addresses, raw types, scale, byte order, word order or function code.

27. **First Modbus device.** The first protocol tracer is Schneider Electric Altivar Process ATV630 used as the communicating Device Endpoint for CHWP-01. CHWP-01 remains the maintainable physical Asset.

28. **ATV630 source references.** The first Device Template revision is pinned to Schneider Embedded Ethernet Manual `EAV64327` version 03 and Communication Parameters `EAV64332` version 4.6 dated 2026-05-01. These are immutable release references for v1.

29. **ATV630 profile.** v1 supports Embedded Modbus TCP with CiA402/DriveCom, command and frequency reference not separated. It does not auto-detect or fallback to I/O Profile.

30. **ATV630 minimal raw map.** v1 maps only ETA logical address 3201, RFR 3202, LFT 7121, CMD 8501 and LFR 8502, with exact datatype/scale/unit/order/access/function semantics taken from the pinned Schneider documents. Optional power/current/torque/PID/thermal diagnostics are deferred.

31. **ATV630 semantic map.** Production DeviceAdapter projects ETA into run/fault-active state, RFR into actual frequency, CMD from START/STOP/RESET_FAULT semantic commands and LFR from SET_FREQUENCY. `faultCode` is exposed from LFT only while ETA indicates an active fault; retained historical last fault is not current state.

32. **Virtual ATV630 slave.** Implement a real Modbus/TCP server endpoint that exposes the exact released ATV630 mapping and connects its read/write semantics to the reacting CHWP physical model. It has no knowledge of Telemetry, Alarm, FDD or Work Order.

33. **Template release vs hardware certification.** `RELEASED` means an immutable protocol contract is platform-approved and proven with the production Bridge/DeviceAdapter plus Virtual slave over real TCP. Vendor Template Certification is separate evidence produced later from real ATV630 lab validation. Hardware absence is never represented as certification.

34. **Template correction.** If real hardware proves a released mapping wrong, release a corrected new immutable revision and migrate assignment explicitly. Do not add address aliases, dual-register probing, old-register fallback or mutation of the released revision.

35. **Historical Replay admission.** Add an internal Telemetry-owner history-only admission with server-fixed `HISTORY_REPLAY` provenance and an explicit historical-observation owner method. It is not the live ingestion method with a magic flag and does not reuse MQTT envelope `replay` as its contract.

36. **Historical Replay time/identity.** Received At is the real import time. Sampled At is the controlled historical event time. Current replay workload/integration authorization is checked at import time; Point binding/revision is resolved at Sampled At. v1 accepts Device-owned Points only.

37. **Historical Replay quality.** Reuse normal type/unit/range/wire-trust/future-clock/mapping validation. Skip only live source-lag freshness because historical age is intentional. Do not add `BACKFILLED` or `SIMULATED` quality values solely for provenance.

38. **Historical Replay side effects.** Allow source-position advancement, source-observation persistence, canonical Device/Point resolution and history outbox persistence. Forbid ReplaceLatest, Presence emission, Device Snapshot reevaluation, Business Revision advancement and any current runtime-state mutation, even when no current latest value exists.

39. **Historical Replay idempotency.** One stable UUIDv7 replay dataset identity survives retries/restarts. Partition is scoped by replay dataset and Device external identity; monotonically increasing offset defines canonical record order; event identity is deterministic from stable replay identity/partition/offset and satisfies existing UUIDv7 requirements. Do not create a second replay-offset framework.

40. **Historical Replay transport.** v1 is one observation per internal admission request/transaction. The runner owns bounded concurrency. Batch ingestion is deferred until measured workload demonstrates need and may not alter single-observation semantics.

41. **Analytics/intelligence boundary.** Historical Replay terminates at Telemetry history. Existing event-time Energy/Metric projections consume history. Forecast and Optimization require server-owned immutable input-snapshot/job builders; the simulator/replay runner never writes their tables or calls workers with self-built authoritative snapshots.

42. **Optimization command loop.** Optimization consumes formal Forecast/Baseline/Topology/Constraint/current-state inputs and issues governed Commands through Command Governance/Edge. The plant reacts physically and telemetry provides readback. There is no browser fake optimization loop.

43. **Deployment boundary.** Virtual Central Plant and Historical Replay are explicit development/acceptance deployment capabilities. Production deployment does not start them, does not depend on their data and has no automatic fallback to them.

44. **Real-environment acceptance.** The current deployment target is single-node WSL. Completion must be demonstrated with the actual WSL deployment stack, real service processes/containers, real PostgreSQL/ClickHouse/MQTT paths and real network connections. Protocol acceptance uses actual TCP sockets between production Modbus master/driver and Virtual slave. In-memory tests alone cannot establish completion.

45. **No defensive compatibility.** When an authoritative contract is wrong, change that contract and all consumers together. Do not support old/new field aliases, old/new paths, null-shape fallbacks, alternate protocol profiles, fabricated defaults or silent simulator fallback.

46. **Complexity rule.** Add an abstraction only when it serves a confirmed current use case and either removes concrete duplication or clarifies an owner boundary. No general plugin architecture, protocol framework, scenario scripting engine or large compatibility layer is part of this spec.

47. **Go implementation standard.** The relevant Go modules currently target Go 1.25.12. Before editing any Go file, implementation agents must run the locally installed JetBrains Modern Go Guidelines tool for the target file/module, read the complete applicable `list` output, and use language/stdlib idioms available up to that module's declared Go version. A relevant guideline may be skipped only when it would not compile, would change required behavior or does not apply; use the guideline's `explain` command when needed. Do not adopt a newer Go feature than the target module declares.

48. **Go design style.** Prefer small owner-focused packages, explicit domain types, standard library facilities and narrow interfaces defined at the consumer boundary. Avoid Java/OpenEMS framework translation, unnecessary factories, blanket interfaces, `any`-heavy generic configuration, defensive nil/default behavior and speculative extension points. Errors must carry enough context for operations but must not convert invalid authoritative state into fallback behavior.

49. **OpenEMS source checkpoint.** Implementation planning and review must re-check the pinned/current OpenEMS source for the specific seam being implemented, especially Simulator reacting behavior, shared production Channel/Nature contracts, Cycle timing, Modbus Bridge transaction lifecycle, real vendor Modbus protocol declaration and ModbusSlave tables. Divergence is allowed only when HVAC_web domain ownership requires it and should be stated in the ticket/PR.

50. **No framework copy.** OpenEMS is reference architecture/source evidence, not a dependency target. OSGi, App Manager destructive lifecycle, generic component factories and unrelated Energy Storage abstractions are not imported solely for similarity.

## Testing Decisions

1. **Testing philosophy.** Tests prove externally meaningful behavior at the highest practical seam. They should verify authoritative inputs/outputs and side effects, not private implementation structure. Do not create broad test matrices or gates merely because branches exist; add tests for real ownership boundaries and failure modes that could produce incorrect platform behavior.

2. **Seam 1 — Plant behavior.** A small deterministic Plant/Scenario test proves that exogenous Scenario plus governed commands/disturbances yields physically coherent equipment state and observations over controlled time. It must cover the first CHWP stuck-high scenario and recovery, including increased actual frequency/flow and physically derived delta-T change. This is the only place where direct Plant model testing is primary.

3. **Seam 2 — production Edge/Protocol.** A real integration test starts the production Modbus/TCP Bridge and production ATV630 DeviceAdapter against the Virtual ATV630 server over an actual TCP socket. It proves poll → raw conversion → semantic Channel → Process Image, Controller/Decision → CiA402 write → Virtual slave/Plant reaction → later independent readback. The same production Driver code must be used for future real hardware tests.

4. **Seam 3 — end-to-end business acceptance.** The highest-value acceptance runs the WSL single-node stack and proves: Summer Scenario → reacting Plant → production Edge cycle → MQTT/IoT → Telemetry → existing Dashboard/Energy reads → CHWP stuck-high disturbance → production Alarm + FDD → existing frontend evidence → operator-created ALARM-origin Work Order → physical recovery → Alarm CLEARED/FDD CLEAR while historical records remain. This acceptance is the definition of the first business slice being done.

5. **Historical Replay tracer.** Add one focused integration tracer that admits controlled historical observations through `HISTORY_REPLAY`, verifies history/event-time downstream visibility, and asserts that current latest, Presence and Business Revision are unchanged. Retry/restart of the same replay identity must not duplicate history.

6. **Alarm/FDD bridges.** Test Telemetry-to-Alarm and Telemetry/History-to-FDD at their public/internal owner seams using actual owner stores, ensuring quality/freshness/evidence and tenant/site scope are respected. Do not unit-test a simulator-to-Alarm shortcut because such a shortcut must not exist.

7. **Work Order provenance.** Test creation of an ALARM-origin Work Order against authoritative source resolution and tenant/site scope, plus formal FDD/Alarm/Work Order association. The acceptance must reject a syntactically valid UUID that does not resolve to an authorized owner fact.

8. **Frontend acceptance.** Validate the existing production pages against real API responses from the acceptance stack. Missing/unauthorized/unavailable values stay unavailable; no browser calculation or simulator fixture fills them. Frontend and backend contract changes must ship in the same vertical slice.

9. **Deployment acceptance.** Simulator-enabled deployment must be explicit and healthy in WSL. Production deployment configuration must not include or depend on the simulator. Real TCP and MQTT connectivity, persistent queues/history and service identities are part of acceptance; replacing them with in-process fakes does not pass the deployment seam.

10. **Vendor certification.** Virtual protocol acceptance is required for RELEASED template v1. Real ATV630 hardware certification is a later hardware acceptance using the exact same immutable revision and production Driver. Its failure produces a new template revision rather than compatibility behavior.

11. **Go verification.** For each Go ticket, run the project's normal focused Go tests for touched modules plus the installed Modern Go Guidelines check/list workflow before implementation. Lint/build/test gates should remain proportional to touched code; no repository-wide over-testing is required for a narrow slice unless the changed owner contract has repository-wide generated consumers.

12. **Prior art.** Reuse current `edgecontrol` Driver/Cycle tests for semantic runtime behavior, EG8200 simulator tests for reacting Plant evidence, Telemetry Postgres integration tests for Observation/history side effects, Alarm evaluator integration tests for durable evaluation, FDD service tests for low-delta-T evidence, and existing local single-node acceptance scripts as patterns. Extend these seams rather than create a parallel test framework.

## Out of Scope

- Manufacturer-grade chiller/compressor digital-twin fidelity, CFD or thermodynamic certification.
- A generic simulation plugin framework, scripting DSL or arbitrary Datasource marketplace.
- Automatic Scenario looping or accelerated live simulation.
- Occupancy/building thermal modeling inside the central-plant v1 Scenario.
- Simulator-specific Registry identity, Telemetry store, Alarm, FDD, Forecast, Optimization, Notification or Work Order domains.
- Simulator-authored Alarm/FDD/Work Order outcomes.
- A separate Virtual Plant business frontend or simulator-specific IAM capability.
- Production fallback to Virtual Plant data, fixtures, old fields, old protocol mappings or browser calculations.
- BACnet or OPC UA protocol simulation before a concrete real integration need exists.
- A generic multi-protocol Bridge framework before a second production protocol requires shared behavior.
- Full ATV630 parameter coverage, optional power/current/torque/PID/thermal telemetry in v1.
- I/O Profile support or automatic ATV630 control-profile detection in v1.
- Real-hardware Vendor Template Certification as a prerequisite for implementing the virtual protocol tracer; certification remains a later hardware gate.
- Batch Historical Replay ingestion in v1.
- Asset-level Historical Replay in v1.
- Direct Replay writes to ClickHouse, Energy, Metrics, Forecast or Optimization.
- Browser-generated Forecast/Optimization inputs or synthetic optimization loops.
- Copying OpenEMS OSGi/App Manager/component infrastructure into Go.
- Broad speculative tests, exhaustive permutation matrices or new release gates unrelated to authoritative behavior.

## Further Notes

### Source hierarchy

OpenEMS is the primary external source reference for simulator, reacting-device, shared production Channel/controller, Cycle and Modbus Bridge/Slave design. ThingsBoard remains a secondary reference for protocol/Gateway boundaries and MyEMS a secondary reference for acquisition/history separation. When these references overlap with an existing HVAC_web Domain Owner, HVAC_web ownership remains authoritative while the upstream implementation pattern is adapted rather than copied.

The OpenEMS `develop` branch checkpoint used by the Wayfinder research was commit `a7efc1c1eacd05f7a0f8eb43f962564ccf66ead6` (2026-08-26). Before implementing a seam, re-check the current source and document any material change. Key upstream references include:

- https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.bridge.modbus/src/io/openems/edge/bridge/modbus/BridgeModbusTcpImpl.java
- https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.simulator/src/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImpl.java
- the OpenEMS simulator Datasource and reacting-simulator source reviewed in the Wayfinder research asset
- real OpenEMS vendor Modbus component protocol declarations for concrete register/task mapping

At the checked source, OpenEMS Modbus TCP Bridge participates in the cycle before Process Image and at execute-write, owns TCP connection/transaction behavior, and the reacting simulator implements production interfaces plus `ModbusSlave`. These are explicit implementation-review anchors, not optional inspiration.

### Modern Go implementation requirement

JetBrains `go-modern-guidelines` is an implementation source of truth for modern Go syntax/stdlib choices: https://github.com/JetBrains/go-modern-guidelines . The installed tool must be invoked against the actual file/module before Go edits; do not rely on remembered examples. Most relevant HVAC_web modules currently declare Go `1.25.12`, but each target file is governed by its own module's `go.mod`/workspace resolution. The implementation must stay within that resolved version.

### Real-environment definition of done

A feature is not complete merely because a Go unit test or in-memory simulator test passes. For the first business slice, the actual single-node WSL deployment must be able to run the simulator-enabled acceptance profile and demonstrate the real MQTT/Telemetry/business/frontend chain. For the protocol slice, the production Modbus Bridge/Driver and Virtual ATV630 endpoint must communicate over a real TCP connection between separately running runtime components. Production deployment must remain simulator-free.

### Existing gaps are implementation work, not permission to bypass owners

The spec intentionally exposes current missing production seams: Telemetry→Alarm, Telemetry/History→FDD, production-neutral Edge runtime host, production Modbus Bridge/DeviceAdapter, Forecast input builder, Optimization input builder, Work Order source resolution/reverse association and existing frontend/backend contract drift. `/to-tickets` should turn these into owner-scoped prerequisites/vertical slices. The simulator must not add shortcuts to avoid implementing them.

### Wayfinder decision assets

The detailed source reviews and decisions produced by #321–#330 remain supporting evidence. This specification is the canonical implementation input; when a prototype conflicts with this spec, the spec wins and the prototype is migrated in one authoritative change rather than preserved through compatibility code.
