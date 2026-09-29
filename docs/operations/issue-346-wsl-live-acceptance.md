# Issue 346 — isolated WSL live acceptance

Status: IN PROGRESS. This record does not certify the full acceptance chain.

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

## Remaining acceptance

Complete the operator-created ALARM/ORIGIN Work Order and FDD association, restore fresh telemetry, and verify authoritative Alarm CLEARED / FDD CLEAR. The first physical fault run below exposed additional blockers; it is not full acceptance.

Energy projection remains disabled by the existing WSL override because the Registry delegation configuration has not been provisioned. Dashboard energy completeness is therefore not certified. Frontend FDD association currently lacks an operator action; API linkage alone must not be reported as frontend coverage.

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
