# S2 production cutover and Legacy current-state retirement

Status: implementation-ready, production promotion fail-closed

Issue: #71

Historical status: ADR 0005 supersedes this document as the current production rollout authority. The assets below remain reproducible historical evidence for the former Legacy-production assumption; current production promotion follows `docs/operations/go-platform-production-rollout.md` and `deploy/platform/production-rollout.v1.json`.

Machine authorities:

- `deploy/s2/release-gates.v1.json`
- `deploy/s2/cutover-plan.v1.json`
- `deploy/s2/full-cutover-attestation.schema.json`
- `.github/workflows/s2-telemetry-cutover.yml`

## Safety boundary

Merging Ticket 12 does not promote production traffic. The repository keeps `appliedProductionPhase` at `R0-contract-only`. Pull requests execute only a configuration preflight and must report `formalCompletionEligible: false`.

A formal completion run is manual, uses the protected `s2-production` environment and fails closed unless it receives:

1. a previously generated **formal** Ticket 11 release-evidence artifact for the exact repository SHA;
2. the approved R1–R8 cutover attestation;
3. real elapsed phase windows and required sample counts;
4. distinct primary and secondary owner approvals recorded after each hold;
5. a rollback drill within the five-minute decision and fifteen-minute route-revision objectives;
6. seven real days of zero Legacy latest, batch and WebSocket traffic;
7. code and network retirement evidence;
8. final Audit approval and zero values for every S2 completion risk.

The workflow verifies evidence; it does not change route ownership automatically. Production route revisions remain an operator-controlled change through the accepted ownership-registry process.

## Phase execution

The exact phase percentages, minimum holds and sample floors are inherited from `release-gates.v1.json`:

| Phase | S2 traffic | Minimum hold | Additional minimums |
|---|---:|---:|---|
| R1 Dark ingest | 0% | 24 h | S2 writer isolated to `telemetry_runtime` |
| R2 Shadow compare | 0% | 24 h | zero mapping/device mismatch; classified semantic differences |
| R3 Internal canary | 1% | 2 h | 10,000 Snapshot requests; 1,000 subscriptions |
| R4 External canary | 5% | 4 h | 50,000 Snapshot requests; 5,000 subscriptions; 100 recoveries |
| R5 Ramp | 25% | 8 h | all promotion gates |
| R6 Ramp | 50% | 12 h | all promotion gates |
| R7 Primary | 100% | 24 h | Legacy remains route-revision rollback target only |
| R8 Legacy retired | 100% | 7 days | zero Legacy latest/batch/WebSocket traffic and removal approval |

Snapshot and live ownership move together for each cohort revision. There is no request-level Legacy, ThingsBoard or Mock fallback.

## Rollback

Every canary or ramp route revision must support adjacent rollback. The decision begins within five minutes and the route revision is restored within fifteen minutes. The operation disconnects or expires affected live sessions and requires a fresh authoritative Snapshot. Expand-only database migrations remain in place; emergency down-migration is forbidden.

## Legacy retirement boundary

Ticket 12 removes the browser Socket.IO client and direct dependency. Backend retirement is implemented in `SwayingWindmill/hvac-backend` PR #31 at commit `d6743e9bdd5da8df17d941b28f31ee4a73ff66e3`; the root repository binds that change through `deploy/s2/legacy-current-state-retirement.v1.json`, and formal completion must attest the same backend SHA.

The Legacy backend no longer registers:

- `GET /telemetry/devices/:id/latest`;
- `POST /telemetry/latest/batch`;
- `/ws/telemetry`;
- unauthenticated Legacy current-state ingest as a public controller.

The deny-all policy in `deploy/s2/legacy-current-state-retirement.yaml` seals workloads labelled `hvac.surface=legacy-current-state` after traffic reaches zero.

Historical timeseries remains a separate compatibility boundary:

- `GET /telemetry/devices/:id/timeseries`;
- `GET /devices/:id/telemetry`.

Historical data may never satisfy a current Snapshot request, backfill S2 current state, become an availability fallback, or influence Presence.

## Commands

Configuration-only preflight:

```bash
npm run s2:cutover:check
npm run s2:cutover:preflight
npm run test:s2-cutover
npm run s2:ticket-12
```

Formal verification is run by `.github/workflows/s2-telemetry-cutover.yml` with `profile=formal`. The operator supplies `release_run_id` and `cutover_attestation_json`. A passing run uploads `s2-completion-evidence`, including phase reports, approvals, rollback and zero-traffic reports, final ownership state, completion attestation, in-toto statement and `SHA256SUMS`.

## Completion meaning

S2 is formally complete only when `s2-completion-attestation.json` says:

- `formalCompletionEligible: true`;
- `completedPhase: R8-legacy-current-state-retired`;
- all security invariants and completion risks are zero;
- Legacy current-state traffic remaining is zero;
- historical timeseries is retained only as the explicit compatibility boundary;
- Audit approval follows the complete seven-day observation.

A preflight report, a merged pull request, or a synthetic test fixture is not production completion evidence.
