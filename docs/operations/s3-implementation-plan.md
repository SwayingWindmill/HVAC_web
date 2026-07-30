# S3 Safe Command Closed Loop implementation plan

Status: implementation started; production control disabled

Machine authority: `deploy/s3/implementation-plan.v1.json`

Accepted architecture: `docs/adr/0006-s3-command-intent-attempt-safety.md`

## Current milestone

S3-01 through S3-08 are complete. The active implementation frontier is S3-09 capacity, crash-point certification and an explicitly approved internal low-risk canary. Production control remains disabled; Dispatcher execution remains Synthetic-only, the Verifier is internal-test-only, and the ThingsBoard CE 4.3.1.3 mapping remains `LOCAL_VERIFIED` and not production eligible.

S3-01 establishes a runnable safety baseline without contacting a real Device or ThingsBoard endpoint. It includes:

- shared Command model;
- in-memory reference Command Service;
- Canonical `SET_TEMPERATURE_SETPOINT` Capability;
- idempotency conflict detection;
- S2 current-state fail-closed validation;
- monotonic per-Device command sequence;
- Attempt lease and execution fence;
- Synthetic Connector modes for Provider acknowledgement, provable pre-send rejection and committed-timeout uncertainty;
- tests proving safe retry only before send, stale-fence rejection and `OUTCOME_UNKNOWN` freeze.

S3-02 replaces the submission path with a PostgreSQL authority baseline:

- `command_runtime` is owned only by Command Service;
- `SERIALIZABLE` submission atomically writes Intent, idempotency, Device sequence, three Transitions, Audit Intent and Dispatch Outbox;
- Organization-scoped RLS is enabled and forced on every tenant table;
- runtime login requires explicit role activation and Organization context;
- concurrent same-key submissions converge to one Intent;
- a failure in any owned write rolls back the entire transaction, including Device sequence allocation;
- Dispatcher database access remains permanently disabled; S3-05 provides a Command Service-owned durable Claim/Resolve interface instead.

S3-03 adds the governed authorization boundary:

- a signed, non-transitive Command Grant is exact to authorization purpose (`COMMAND_SUBMIT` or `COMMAND_APPROVE`), Principal, Organization, Site, Device, Capability, Capability Revision and maximum risk;
- submit Grants cannot be reused as approval Grants, and an approval Grant Principal must equal the recorded Approver;
- Command Grant lifetime is capped at 30 seconds and is bound to IAM policy and emergency-revocation revisions;
- Command Service independently computes the risk snapshot and approval policy;
- Medium risk requires one independent approver and High risk requires two distinct approvers;
- approvals bind Payload Hash, Capability Revision, risk rule revision and a fresh approval authorization;
- every approval authorization in a multi-person chain must remain unexpired at Dispatch Claim time;
- authorization, risk and approval snapshots are immutable PostgreSQL authority records with forced Organization RLS;
- Dispatch Outbox creation is forbidden until the approval threshold is satisfied.

S3-04 adds the disabled public Command API boundary:

- Gateway accepts only Canonical `deviceId`, Capability and parameters; Organization, Site and Principal are never accepted as browser authority;
- POST requires an authenticated BFF Session, exact Origin, CSRF token and scoped Idempotency-Key;
- Gateway resolves the Device and Site through the Registry Owner, then reads the exact S2 `zone.temperature` current-state projection;
- current-state must be `AVAILABLE`, `ONLINE`, `CURRENT`, `FRESH` and `GOOD` before IAM Command authorization is requested;
- IAM signs a non-transitive, purpose-bound Command Grant after exact Principal, Organization, Site, Device, Capability and risk-ceiling authorization;
- Command Service verifies the IAM signature and scope before its PostgreSQL authority transaction;
- GET uses a separate Gateway-signed read context scoped to one Organization and one Command;
- both public routes remain disabled in the active Route Ownership Registry.

S3-05 adds the durable execution boundary:

- Command Dispatcher calls Command Service `ClaimDispatch` and `ResolveDispatch` and owns no `command_runtime` credentials;
- Claim atomically leases one governed outbox record, increments a per-Device execution Fence and creates a `PREPARED` Attempt;
- a Device control lane is serial by `device_command_sequence`;
- only a provable `PRE_SEND_REJECTED` result may be requeued, with a strictly higher Fence;
- a lease that expires without proof of non-send becomes `OUTCOME_UNKNOWN` and freezes the control group;
- stale Worker results are rejected by Attempt, lease owner, Payload Hash and monotonic Fence checks.

S3-06 adds a local-only ThingsBoard provider contract:

- ThingsBoard CE `4.3.1.3` is pinned in an ephemeral loopback-only Docker environment;
- the only mapping is `SET_TEMPERATURE_SETPOINT` to two-way REST server-side RPC method `setTemperatureSetpoint`;
- the Connector persists prepared evidence before network send and records request/response hashes without storing provider credentials;
- a transport failure before request write is `PRE_SEND_REJECTED`; any failure after request write is `REQUEST_COMMITTED` and is never blindly retried;
- an RPC reply is only `ACKNOWLEDGED`; it does not set `Verified=true` or declare business success;
- the local contract captured that the first HTTP Device RPC request ID may be zero;
- the temporary Device, ThingsBoard containers, PostgreSQL volume and local credentials are removed after certification;
- the mapping is `LOCAL_VERIFIED`, `productionEligible=false`, and cannot replace the Synthetic-only production execution gate.

S3-07 separates Provider acknowledgement from business success:

- Connector ACK persists the Attempt as `ACKNOWLEDGED`; Intent remains `DISPATCHING` and no Device success is implied;
- Command Service records ACK time, Connector Evidence ID and a bounded reported-state verification deadline;
- restricted `command-verifier` Workers claim exact Attempt/Fence/Payload verification leases from Command Service and have no `command_runtime` credentials;
- the Verifier reads S2 reported state through an Owner-provided Reader boundary and classifies fresh evidence, mismatch or inconclusive state;
- Command Service independently revalidates Organization, Site, Device, Fence, Payload Hash, Connector Evidence, Business Revision, observation time, Presence, Freshness, Quality and setpoint tolerance;
- only a newer S2 Business Revision observed after ACK and matching the Canonical setpoint may produce `VERIFIED` and `SUCCEEDED`;
- stale, mismatched, unavailable or expired verification becomes `OUTCOME_UNKNOWN` and freezes the SETPOINT control group;
- Connector and Synthetic implementations are forbidden from setting `Verified=true` and bypassing S2 evidence.

S3-08 adds the disabled Command operations experience without new execution authority:

- HVAC Web registers `/commands` and `/commands/{commandId}` for Operations and R&D roles only;
- the public client accepts only Canonical Device ID, `SET_TEMPERATURE_SETPOINT` and `setpointC`; browser Organization, Principal, risk, approval role and provider fields are absent;
- Command Detail returns setpoint, approval threshold, S2 Snapshot Revision and a strictly versioned Timeline with only `PRINCIPAL` or `WORKLOAD` actor classes;
- public approval accepts an empty JSON object; Gateway derives Principal and Approver Role from the authenticated Session, resolves the Device through Registry and requests an exact `COMMAND_APPROVE` Grant;
- Command Service independently reconstructs approval evidence from its authoritative Intent, Payload Hash, Capability Revision and risk snapshot;
- Mock mode supports UX audit with zero ThingsBoard or Device side effects;
- real mode shows an explicit production-disabled state and does not attempt to bypass the disabled Route Ownership Registry.

S3-09 now defines and automates the certification boundary without claiming that the real internal Device canary has occurred:

- the frozen capacity envelope is 100 commands/s for a 60-minute steady-state certification and 1,000 commands/s for a one-minute burst, with at least 30% measured headroom;
- Command acceptance, governance, ready-to-send and status-propagation percentiles are checked against the accepted SLOs;
- the required crash matrix covers PostgreSQL rollback, idempotency races, Dispatcher loss after claim, Consumer Rebalance, PRE_SEND retry, REQUEST_COMMITTED uncertainty, Connector crash, old Fence results, reported-state mismatch/deadline, Audit Intent failure and single-AZ loss;
- deterministic Go and PostgreSQL tests remain the authority for Attempt, Fence, control-lane and request-write classification semantics;
- `deploy/s3/certification-envelope.v1.json` defines all zero-tolerance counters, recovery objectives, bounded canary scope and required evidence;
- `scripts/run-s3-command-certification.mjs --profile=preflight` validates repository configuration only and is never formal release evidence;
- the formal profile requires a repository-SHA-bound target-environment attestation, a `VERIFIED` mapping, S2 current-state production certification, two distinct manual approvers, six to twelve operator-confirmed LOW-risk commands on exactly one internal Device, a four-hour hold and a future-command-only rollback drill;
- even after formal S3-09 certification, the three browser Command routes remain disabled and production traffic remains 0% until a separate rollout decision changes Route Ownership.

Reuse evaluation for certification tooling considered `tsenart/vegeta` and `fortio/fortio` for target-environment load generation and `Shopify/toxiproxy` for transport fault injection. They are suitable optional runners for an approved environment, but none is added as a runtime dependency: exact Command safety semantics depend on project-specific Attempt, Fence, PostgreSQL and Connector evidence and therefore remain implemented as deterministic domain/integration tests. Formal attestations may record those tools in the environment evidence.

The public routes, production Connector credentials and real production Device control remain blocked by formal S3-09 certification and explicit rollout approval. S3-09 remains `formal-certification-pending`; a configuration preflight or validator fixture must not add it to `completedTickets`.

## Delivery order

| Ticket | Scope | Production side effect |
|---|---|---|
| S3-01 | Ownership, ADR, contracts, state machine and Synthetic tracer bullet | Forbidden |
| S3-02 | PostgreSQL authority, RLS, transitions, audit intent and dispatch outbox | Forbidden |
| S3-03 | IAM Capability authorization, risk and approval snapshots | Forbidden |
| S3-04 | Gateway create/query routes with Session and CSRF | Routes remain canary-disabled |
| S3-05 | Durable device lane, lease, fence and takeover recovery | Synthetic only |
| S3-06 | ThingsBoard control Connector and verified mappings | Internal test integration only |
| S3-07 | ACK, reported-state verification and unknown-outcome resolution | Internal test Devices only |
| S3-08 | HVAC Web command timeline and approval experience | No new execution authority |
| S3-09 | Capacity, crash-point tests and internal low-risk canary | Explicit approved cohort only |

## Verification commands

```bash
npm run s3:command-safety
npm run s3:command-authority
npm run s3:command-api
npm run s3:thingsboard-contract
npm run s3:command-ux
npm run s3:certification:pr
```

`npm run s3:certification:pr` runs the deterministic target-runtime and certification preflight gate only. PostgreSQL command authority, TypeScript linting and the product build remain owned by their stable capability workflows instead of being repeated here. Formal target-environment certification is invoked explicitly with an approved attestation:

```bash
node scripts/run-s3-command-certification.mjs \
  --profile=formal \
  --attestation=path/to/s3-command-attestation.json \
  --output-dir=out/s3-formal-certification

node scripts/verify-s3-command-certification.mjs \
  --directory=out/s3-formal-certification
```

The commands check the machine plan and ownership baseline, run PostgreSQL container certification, then run tests and static analysis for:

- `libs/commandmodel`;
- `services/command-service`;
- `services/command-dispatcher`;
- `services/thingsboard-connector-control`.

## Entry conditions for production provider activation

The `LOCAL_VERIFIED` ThingsBoard mapping cannot become production eligible until:

1. S2 current-state reads are production-certified for the intended internal Device cohort;
2. Command PostgreSQL transactions and RLS pass multi-Organization tests;
3. IAM authorizes exact Organization, Site, Device, Capability and risk;
4. execution lease and fence recovery pass crash-point and stale-worker tests;
5. the Capability mapping is versioned and marked `VERIFIED`;
6. provider credentials exist only in the Connector workload;
7. `REQUEST_COMMITTED` timeout tests prove zero automatic reissue;
8. reported-state verification uses a newer S2 Business Revision and passes deadline, mismatch and unavailable-state failure injection;
9. Audit Intent persistence is fail-closed.

## Explicitly deferred

- historical telemetry platform;
- scheduling and Automation;
- AI recommendations;
- bulk multi-Device control;
- high-risk start/stop operations;
- Persistent RPC;
- real production Device control.
