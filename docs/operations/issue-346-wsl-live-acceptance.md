# Issue 346 — isolated WSL live acceptance

Status: COMPLETE for the fault-to-maintenance owner chain. Healthy baseline, physical fault, authoritative Alarm, FDD finding, operator Work Order from the Web UI, and verified recovery were all observed live on the paced rig. Two product defects found by this run are recorded and NOT fixed here: per-point ingest throughput, and authorization failure being classified as a permanent message defect. The newest frontend blocker is recorded below.

## Runtime and data ownership

Primary repository: `E:\Code\HVAC_web`. The existing acceptance worktree is `worktrees/issue-346-wsl-acceptance`, operated through WSL at `/mnt/e/Code/HVAC_web/worktrees/issue-346-wsl-acceptance`. Compose project: `hvac-phase1-local`; public origin: `https://localhost:9443`. The separate `hvac-phase1-dev` stack is not part of this acceptance.

Do not reset database volumes. Previous offline simulator queues remain under the ignored runtime data directory. Each resumed live run uses a separate queue and MQTT subscriber session while retaining measurement sequence continuity. Offline backlog replay is not evidence of current fault onset.

## Reproduced blockers and fixes

- Canonical fresh migrations included superseded `009h`; remove its manifest/list entries, retain historical SQL. The authoritative manifest/list/order checks remain; the arbitrary file-count assertion is removed.
- Acceptance Compose build context was resolved relative to the first Compose file and selected an incomplete Dockerfile. Use the canonical workspace Dockerfile.
- WSL identity redirects must follow the configured public origin. TLS files must be readable by the actual service group (65532); MQTT broker certificate files by its own group.
- Simulator Registry seeds omitted COUNTER decrease semantics. The simulated cumulative meters restart from zero; seed `RESET_TO_ZERO`.
- Local admin initialization granted Device/key scope but omitted the corresponding Site telemetry actions. Add the same current telemetry actions to the local Site binding, retaining IAM authorization.
- MQTT simulator diagnostics previously could not disturb the telemetry-producing Plant. Both simulator executables now reuse the same CHWP disturbance handler. Advance Plant time by actual elapsed time so delayed cycles do not accumulate stale timestamps.
- Alarm Site aggregation treated point keys as globally unique. Qualify inputs as `deviceId/telemetryKey`; retain event/key provenance. FDD requests the device-scoped supply/return temperature keys actually emitted by Registry.
- ClickHouse History SQL aliased both its timestamp expression and formatted output as `snapshot_at`, and formatted `sampled_at` shadowed the raw filter/order column. Use a distinct snapshot cutoff alias and qualified raw timestamp columns. Reproduced real query errors 47 and 43; the corrected real HistoryClient completed successfully.
- Local ClickHouse `telemetry_history` writer was missing. Provision only its actual observations INSERT/SELECT and numeric materialized-view destination INSERT permissions. No production authentication policy was relaxed.

## Focused validation already observed

- Canonical deployment check passes.
- Alarm, FDD and History package checks pass; FDD gateway checks pass. PostgreSQL-dependent tests are not claimed passed when skipped.
- Existing physical disturbance/recovery check now goes through the reused HTTP entry point and passes.
- Real administrator login and Work Order page load succeed at desktop 1440 × 1000.
- MQTT ingest accepts current observations; ClickHouse history stores them; public FDD evaluation returns real evidence-backed findings. Warm-up findings and previous-day windows are not classified as injected-fault evidence.

## Simplification

Removed obsolete fixed-ticket S2 implementation-plan gate and old dependency-version snapshot test, including active wiring. Production dependency audit remains. Removed arbitrary minimum artifact counts from simulator acceptance. No new permanent gate or compatibility fallback was added.

Whole-repository cleanup is not complete: the existing domain-matrix aggregate check still invokes source scanners referencing obsolete `modules/telemetry/internal/telemetry` paths; Windows-only browser profiles/workflows and other historical certification gates remain to be reconciled with current Linux authority.

## Second live run — 2026-09-29 (bounded evidence, paced rig)

### Live telemetry was 38 minutes behind and drifting

The first run ended with every observation accepted but stamped ~38 minutes before
wall clock, all Devices OFFLINE and Alarm evaluation INDETERMINATE on STALE_INPUT.
The messages being processed carried UUIDv7 identities minted at the same instant as
their `observed_at`, so this was not a clock defect: the broker was replaying a
persisted backlog into a consumer that could not keep up. Two consecutive samples of
`max(observed_at)` advanced 91.7s of business time over 113s of wall clock, so the
offset was widening rather than draining. `mosquitto.db` had grown to 24.6 MB with
the MQTT adapter configured for a durable session (`sessionExpirySeconds: 86400`),
which is the backlog's home; the simulator's disk queue was empty.

Measured production and consumption over the same 120s window: 68 MQTT messages
published, 48 processed.

### The ingest path commits one serializable transaction per point

`Processor.processTelemetry` called `AcceptObservation` once per point, serially, over
mTLS. A 36–44 point message therefore cost 36–44 HTTP round trips and 36–44
`SERIALIZABLE` transactions, each taking advisory locks and several inserts. That caps
one Gateway near one point round trip: about 12 points/s, while the seeded Registry
contract (`publishInterval` 2s, `staleAfter` 10s) demands roughly 28 points/s.

Two concurrency fixes were implemented, measured and rejected:

- Accepting independent points concurrently (8-way) produced **723 PostgreSQL
  deadlocks** between `device_observation_snapshots ... FOR UPDATE` and
  `latest_accepted_telemetry` updates, and 49 messages reached the dead-letter path.
- Restricting concurrency to one Device at a time (points of a Device share its
  transaction rows, so those run in order) removed the deadlocks but left **1,562
  `could not serialize access due to read/write dependencies among transactions`**
  errors. Under `SERIALIZABLE`, concurrent inserts into shared indexes conflict, and
  a 36-point message cannot survive three retries per point. Both changes were
  reverted; the adapter is back to serial point acceptance.

The durable fix is one transaction per message (a batch accept path on the telemetry
runtime owner) rather than client-side concurrency. That is an owner-side change and
was deliberately not attempted inside this acceptance.

### Rig cadence aligned with the capacity actually available

`Config.Interval()` takes the minimum of the top-level `publishInterval` and every
point `sampleInterval`, so the publisher tick was pinned at 1s by the seeded 1s
sample intervals; raising only the top-level value changed nothing. The rig now
publishes the two Devices the Alarm/FDD chain needs (CHILLER-01, BTU-METER-01) every
30s with a 5m staleness window, holds the other Devices at a 10m cadence, and the
local Registry freshness contract and runtime freshness policy were widened to match
(60s stale / 120s fresh). This is an acceptance-rig setting, not a product default;
it is also the concrete evidence that the seeded 2s/10s contract is unreachable by
the deployed ingest path.

After that: producer and consumer both ran at 4 messages/120s, business time stayed
within one 30s cadence of wall clock, and parked, dead, serialization-failure and
deadlock counters stayed at zero with all seven Devices ONLINE.

### Owner paths verified live

| Observation | Recorded result |
| --- | --- |
| Healthy baseline | CHILLER-01 RUNNING, 849 kW; return 13.202 °C / supply 7.669 °C; delta-T 5.533 °C; quality GOOD, freshness FRESH; Alarm state NOT_MATCHED |
| Owner FDD at baseline | HTTP 201, CLEAR, deltaTC 5.533 |
| Physical injection | 06:15:42.669Z, shared MQTT Plant disturbance endpoint HTTP 204 |
| Fault symptoms | 863.988 kW; return 10.398 °C / supply 7.021 °C; delta-T 3.377 °C; quality GOOD, freshness FRESH |
| Alarm duration condition | candidate from 06:20:46, unchanged 300s |
| Authoritative incident | `01a0ebd7-4bc8-7291-b4e3-2925b6637d10`, ACTIVE, occurrenceCount 1 |
| Public Alarm read | **HTTP 200** (the pre-fix 503 oversized response is gone) |
| Owner FDD over fault window | HTTP 201, FINDING `CHILLED_WATER_LOW_DELTA_T`, deltaTC 3.377, finding `01a0ebd7-af49-7e56-81cb-2a915886d1af`, confidence 0.6623 |
| Operator Work Order (Web UI) | The Alarm workbench opened the incident and created the work order through the branch UI: HTTP 201, `workOrderId 01a0ec0e-b226-7e80-9f94-b8f61eaee0c7`, `sourceReferences [{domain: ALARM, relationship: ORIGIN, resourceId: 01a0ebd7-4bc8-7291-b4e3-2925b6637d10}]` |
| FDD finding linked | PATCH `/sites/{siteId}/fdd/findings/{findingId}/links` accepted with the Alarm and Work Order identity |
| Physical recovery | Disturbance removed 07:26Z, HTTP 204 |
| Business recovery | Alarm incident CLEARED, evaluation NOT_MATCHED with no quality blocker; delta-T 5.628 °C; owner FDD HTTP 201 CLEAR; public Alarm read HTTP 200 |

### A lapsed connectivity session silently quarantined every valid message

Ingest stopped at 07:15:08Z and every later message was logged as
`outcome=quarantined, attempts=1`. The payload was not at fault: capturing a live
message from the broker and decoding it locally produced 17 accepted points, and
authorization data (IntegrationInstance, GatewayChildBindings, credential) was
present and ACTIVE. The cause was `connectivity.sessions`: the seeded MQTT Gateway
session is created with a 24h lifetime, and `expires_at` was
`2026-09-29 07:15:32Z` — the first quarantine followed five seconds later. A sweeper
closed it with `CREDENTIAL_EXPIRED` at 07:16:29Z.

`Processor.Process` wraps **any** `AuthorizeGateway`/`AuthorizeGatewayChild` error in
`permanentMessage`, so a lapsed credential was classified as a defective message and
every valid observation was terminally acked as quarantined. Telemetry was silently
lost while the adapter reported message-level defects, and nothing surfaced the
credential as the cause. Renewing the session (`status = ACTIVE`, `expires_at` moved
forward, close fields cleared) resumed ingest immediately with 17 points accepted per
message.

This is a product defect independent of the acceptance: an infrastructure or
credential failure must not be reported as a permanent message defect. It belongs in
its own issue and is not fixed here.

### Bounded Alarm evidence projection

`Alarm.Evidence` was append-only and `Alarm` is a jsonb column read by every list
query; one legacy incident held 54,384 references (~6 MB of JSON) inside a 2 MiB
gateway response bound. `maximumEvidenceReferences = 32` now bounds the projection on
create, occurrence and clear, keeping the newest facts while `occurrenceCount` stays
authoritative. The legacy incident was repaired to the same bound through SQL
(921,450 bytes of jsonb to 849 bytes); its 1,236-entry lifecycle timeline was left
intact because `validateTimeline` requires contiguous versions from 1.

Residual risk recorded: a very long-lived incident grows its timeline without bound
(638 KB at 1,236 entries), so the list read is bounded only by the gateway limit.
Compacting the timeline needs an explicit elided-range representation and is not done.

### Stale gates

Eleven source-scanning gates still opened `modules/telemetry/internal/telemetry` and
`modules/iot/internal/*`, which moved to `modules/*/pkg/`; they failed with ENOENT
instead of protecting anything. They now scan the current paths and pass, except that
`check-s2-telemetry-ownership.mjs` also asserted `decisionRevision === 3` and
`activationStatus === 'v2-convergence'`, which the v2.1.2 alignment had already moved
past, and re-asserted GLOSSARY.md vocabulary. Those snapshot and documentation
assertions were removed; the storage-authority, ingest-source, semantics and
source-code assertions remain.

### Current frontend blocker (RESOLVED — see run 3)

The acceptance stack originally served this branch's own Web build because the newest
frontend (`e45cd159`) required `capabilitySetVersion: 12` while this branch served 11 and
had no `alarm.assign`. That mismatch is resolved: the feature branch now publishes v12
with the assignment capability, and the acceptance branch has merged it while keeping
009h out of the canonical allowlist.

Recorded history: before the merge, the acceptance stack served this branch's own Web
build because the newest frontend could not bootstrap against this backend — its
generated client required `capabilitySetVersion: z.literal(12)` while
`libs/identitycontext.CapabilitySetVersion` was 11 and the effective principal carried
27 of the 33 newer capabilities, so the strict schema rejected the payload and the UI
reported PRINCIPAL UNAVAILABLE with no failing request. Merging the feature branch at
that time was also blocked because it re-added
`infra/registry/postgres/init/009h-data-execution-runtimes.sql` to the canonical
migration manifest. Both conditions were removed before run 3.

### Run 3 — the chain re-proven on the merged stack with the current frontend

The blocker below was resolved by moving the acceptance onto the current product branch:
the feature branch removed the superseded 009h migration (015 only creates the
role-binding revocation trigger over objects 006 and 009 already create, so it does not
duplicate), published capability set v12 with `alarm.assign` together with the IAM
authorization path, catalog revision 4 and the gateway operation, and tracked the
operational scripts its own package.json already called. The acceptance branch then
merged that branch (conflicts resolved in favour of the branch's template, deployment
check and regenerated clients, keeping the 009h guard; the Ant-era Alarm page deleted
because the Issues workspace replaced it) and applied the new migrations
(`schema=8675ccacee093ae0005d7103ad578f809f311a2012dc05681c1c5ad4585f9e39`).

The stack now serves the current shadcn 10-workspace frontend against the current
backend: `capabilitySetVersion = 12`, the principal bootstraps, and
`/sites/{siteId}/issues` renders the Alarm workbench. The whole chain was then re-run
and observed live:

| Step | Recorded result |
| --- | --- |
| Healthy baseline | delta-T 5.628 °C; owner FDD 201 CLEAR; Alarm NOT_MATCHED |
| Physical injection | 08:20:07Z, HTTP 204 |
| Fault | delta-T 3.377 °C; quality GOOD, freshness FRESH |
| Authoritative Alarm | ACTIVE after the unchanged 300s condition; incident `01a0ec45-7ab5-7e9c-aa96-927ee5e050f2`; public read HTTP 200 |
| Owner FDD | 201 FINDING `01a0ec48-f931-702a-bd4c-36678c6aec01`, deltaTC 3.377 |
| Operator Work Order (current frontend) | The Issues workspace opened the incident and the 告警转工单 dialog created the work order: HTTP 201, `workOrderId 01a0ec4a-e550-70ff-ae76-fed6d6e729cd`, `sourceReferences [{domain: ALARM, relationship: ORIGIN, resourceId: 01a0ec45-7ab5-7e9c-aa96-927ee5e050f2}]` |
| FDD finding linked | PATCH 200 with the Alarm and Work Order identity |
| Recovery | disturbance removed 08:32:2xZ HTTP 204; incident CLEARED; evaluation NOT_MATCHED; FDD 201 CLEAR at delta-T 5.447 °C |

## Closing the three acceptance gaps

### Repeatable live rig

`deploy/acceptance/phase1-simulator-rig.v1.json` is now the single reviewed source for the
plant cadence, the Registry publish/staleness contract, the runtime freshness window, the
MQTT session lifetime and the broker queue requirement. The plant configuration is
generated from it (`acceptance:phase1:rig:config`), the central-plant seed applies the
Registry cadence and freshness and creates a session that cannot outlive the profile
window (`acceptance:phase1:rig:seed`), and `acceptance:phase1:rig:check` fails when a
running stack drifts from the profile. Reproducing the rig from the profile alone was
verified: Registry/connectivity/runtime contracts reseeded, simulator configuration
regenerated, business time within one cadence of wall clock and no adapter terminal
outcomes. The previous run depended on hand-edited runtime state and a 24h session that
expired mid-run, which made the adapter quarantine every valid message.

The merge also left the acceptance matrix naming npm scripts the converging product branch
had consolidated, and its static check had been failing on both branches for a requirement
that referenced a gate which never existed. `npm run acceptance:phase1:check` passes again
(requirements=29, gates=18).

### Operator association of an FDD finding with a Work Order

The Work Order detail now lists the Site's FDD findings that are not yet associated with a
Work Order and lets the operator associate one with the Work Order being handled. Verified
live: the action issued `PATCH /api/v1/sites/{siteId}/fdd/findings/{findingId}/links` with
HTTP 200 and the finding then carried `alarmId=01a0ec45-7ab5-7e9c-aa96-927ee5e050f2` and
`workOrderId=01a0ec79-cce5-7ffe-8dd3-6394743bc842`.

The first implementation looked the findings up by the origin Alarm and could never have
worked: a finding only carries `alarmId` *after* an association is recorded, so listing by
that Alarm returns exactly the findings that are already associated.

### Energy projection: the projector reads Registry as its own Workload Principal

Root cause (2026-09-29): the projector only had a static Registry grant input, and a Registry
grant lives at most thirty seconds (`registryauth.MaximumGrantLifetime`); IAM issued grants
only from a user session, which an unattended workload never has. ADR 0017 resolves it: the
projector is its own Workload Principal (issuer `spiffe://hvac.local`, subject its SPIFFE
ID) with an ordinary Tenant membership and an `energy-projection` role limited to
`meter-binding.resolve`. It asks IAM over mTLS at
`POST /internal/v1/registry/workload-decision` for one grant per Tenant and renews it with
less than ten seconds left. In Phase 1 the projector runs inside the Telemetry Runtime, so the
principal is `spiffe://hvac.local/telemetry-runtime-service`, seeded by `local:up`.

Two further gaps appeared once grants worked:

- The plant had no meters. `local:up` now registers both COUNTER points as PRIMARY meters on
  an active energy topology: METER-HVAC-TOTAL `energyKwh` as electricity and BTU-METER-01
  `accumulatedCoolingEnergyKwh` as cooling.
- Projection accepted only electricity, so the first BTU delta stopped every batch. Core,
  the projector and the ClickHouse writer now accept a PRIMARY counter of any energy type;
  Energy Series and the Dashboard still query electricity only.

Live evidence, 2026-10-09 (UTC), isolated `hvac-local` stack built from this branch:

| Observation | Result |
| --- | --- |
| IAM workload decision | `POST /internal/v1/registry/workload-decision` HTTP 200 every ~20 s (30 s grant, renewed with <10 s left) |
| Core meter-binding resolve | HTTP 200 for every resolution (12,621 in the first five minutes of backfill) |
| Energy facts | `analytics.energy_interval_facts` filled from the full counter history, electricity and cooling in equal counts |
| Dashboard local-day energy | `GET /api/v1/sites/{siteId}/dashboard-summary` HTTP 200, `siteLocalDayEnergy` PARTIAL 833.92 kWh from `ANALYTICS_ENERGY`; the overview at 1440 px shows 实际用电 0.8 MWh (11:17 Site-local) |

Environment events during the run, not product defects:

- WSL crashed three times on Windows low virtual memory (event 2004, `vmmemWSL` at
  13–14.5 GB) while building or starting the full stack; capping WSL at 12 GB in
  `.wslconfig` stopped it.
- One crash left history batch `01a11e87-…` DEAD after twelve failed ClickHouse inserts,
  which, as designed, blocks all later history. None of its 84 observations were in
  `telemetry_history.observations`, so it was reconciled by the documented manual recovery:
  telemetry-worker stopped, batch set back to PENDING with unchanged rows and token, worker
  restarted; it published and history resumed.
- The EG8200 simulators had buffered telemetry during the outage and replayed it at about
  four times wall-clock speed, so "today" only filled once the replay passed the Site-local
  midnight.

Open: the projector's candidate query aggregates the whole facts table on every poll and
now intermittently exceeds the ClickHouse 1.5 GiB memory limit (`Code: 241`,
`AggregatingTransform`); projection retries and advances, but the cost grows with history.

## Remaining acceptance

The fault-to-maintenance chain is complete and was observed end to end on the merged
stack with the current frontend (run 3). What remains open is outside this chain:

- The Work Orders ledger does not display existing Work Orders: the owner API returns them
  (`GET /api/v1/sites/{siteId}/work-orders?limit=10` → 200 with the Work Order) while the
  ledger and its summary counts render empty, so an operator cannot reach an existing Work
  Order from the list. This predates the association work — the run-3 screenshot shows the
  same empty ledger — and needs its own fix.

## Live run — 2026-09-29 (UTC)

| Observation | Recorded result |
| --- | --- |
| Healthy baseline | CHWP 30 Hz; delta-T 5.31 C; public FDD HTTP 201, CLEAR |
| Physical injection | 00:48:37.427345; shared MQTT Plant disturbance endpoint HTTP 204 |
| Fault symptoms | CHWP 50 Hz; delta-T 3.373 C; telemetry quality GOOD |
| FDD owner | HTTP 201; CHILLED_WATER_LOW_DELTA_T; finding `01a0eaa7-8d9a-7de2-982d-a28e6086a93e`; window 00:53:33.899194–00:54:03.899194 |
| Alarm candidate | 00:55:35.957334; existing 300-second condition unchanged |
| Authoritative incident | `01a0eaad-8725-7875-aa83-71a8da665fce`, first occurred 01:00:36.005564; ACTIVE verified in owner storage |
| Public alarm read | Failed: repeated telemetry publications grew one incident to 1,235 occurrences / 54,340 evidence references, exceeding the public response bound |
| Physical recovery | Disturbance removed via HTTP 204; later CHWP 30 Hz, supply 7 C / return 12.628 C |
| Business recovery | NOT CERTIFIED. Telemetry lag exceeded freshness; Alarm remained INDETERMINATE with STALE_INPUT, preserving the active incident; later public FDD request returned 502 |
| Work Order | NOT CREATED: public alarm read failed before the browser could open its creation action |

Local raw responses, observations and screenshots are under ignored `out/issue-346-live-evidence/`; login state and credentials are excluded from version control. A file containing an error response is failure evidence, not a passing assertion.

### Additional live fixes

- Registry's response and the formal OpenAPI already contain nullable `counterDecreaseMode` and `counterRolloverModulus`, but the generated Go DTO template omitted them. The strict decoder rejected valid assets. Restore these fields in the template and regenerate; the existing point contract check reproduced failure before the fix and passes afterward. Live page revalidation is pending runtime recovery.
- An active evaluator incident no longer republishes on every matching telemetry revision under the same fixed policy. Changed policy revisions retain publication semantics. The existing active-state test reproduces the old repeated publication and passes after the change. See the architecture source-review record for upstream comparison and HVAC-specific justification. Pre-fix historical oversized responses still need a proper read projection; no history was deleted.
- PostgreSQL reached its 1.5 GiB memory ceiling during this run. The isolated runtime limit was raised to 3 GiB; WAL archive directory ownership was corrected to the container's PostgreSQL user. These are local runtime changes, not new production defaults.

### Runtime checkpoint

The updated API image `hvac/energy-api:issue-346` built successfully. Its deployment exposed PostgreSQL connection timeouts. Acquisition was stopped after physical fault removal. Docker then failed to restart PostgreSQL (`did not receive an exit event`); subsequent exec attempts failed at `setns`, while inspect still reported the old process as running. This is not a healthy database despite the stale health label.

The simulator, IoT service and telemetry worker are stopped; API is restarting against the unavailable database. All data and queues remain. Restoring Docker Desktop affects the separate development stack and requires coordinating that interruption. Do not reset volumes or declare this run complete.

Final focused results: Alarm package passes; current Registry point / asset-model contract checks pass; generated contract check passes; Phase 1 deployment check passes; whitespace check passes with the repository's CRLF convention. No full-suite or full-business acceptance claim is made.
