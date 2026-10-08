# Connectivity command authorization source review — 2026-10-08

Scope: #397 stories 23/26 require real authorized command delivery for two Sites. Live acceptance exposed that both embedded and standalone IAM leave `CommandAuthorizationStore` unset. `modules/iam/internal/iam/server.go` then installs the deny-all store. This is a missing production owner adapter, not justification for weakening authorization.

## Local evidence

Reviewed `GLOSSARY.md`, ADR 0011 and ADR 0015; command authority, controller feedback and Gateway identity remain separate. Also reviewed:

- `modules/iam/internal/iam/command_authorization.go`: already defines exact `CommandPermission` facts: Tenant, Site, Device, capability, capability revision, submit/approve purpose, maximum risk, effect, status and effective interval. The evaluator requires active principal/membership and lets an exact DENY override ALLOW.
- `modules/iam/internal/iam/command_server_test.go`: protects separate submission/approval permissions and deny precedence, but supplies in-memory facts. It cannot prove production PostgreSQL wiring.
- `modules/iam/internal/iam/postgres_authorization.go` and `postgres_telemetry_authorization.go`: existing shared pgx pool, principal identity resolver, transaction-scoped principal/Tenant RLS context, repeatable-read fact loading and policy revision helpers.
- `modules/iam/internal/iam/postgres_work_order_authorization.go`, `infra/registry/postgres/init/008-s5-work-order-authorization.sql`: an established domain permission table with runtime SELECT, principal/Tenant forced RLS, effective intervals and persisted lifecycle/revision.
- Registry baseline migration, `003-iam-runtime-identity-resolution.sql`, `006-s2-telemetry-authorization.sql`, `009-s04-tenant-iam-admin.sql`: existing principals, memberships, role/site/telemetry bindings and Tenant authorization revisions. These express identity, read access and selected telemetry scope; none inspected encodes the full exact CommandPermission tuple.
- `libs/commandauth/decision.go`, `grant.go`: signed grants preserve exact scope, capability revision, purpose, maximum risk and revocation revision. Invalid risk values are not legitimate permission facts.
- `cmd/energy-api/embedded_energy.go` and `modules/command/cmd/command-owner/main.go`: Command grant consumption currently compares policy/revocation revisions with configured `COMMAND_POLICY_REVISION` / `COMMAND_EMERGENCY_REVOCATION_REVISION`.
- `scripts/run-s1-registry-postgres-tests.mjs`: existing real PostgreSQL acceptance runner; extend its IAM tests/migration selection instead of inventing a new certification gate.

## Pinned official comparison

Official source/tests were fetched read-only and inspected on 2026-10-08; upstream tests were not run. Source pins match the platform's existing reference review. No source is copied. This is a narrow comparison of authorization ownership, not a claim that upstreams implement HVAC risk permissions.

### ThingsBoard v4.4 — `6d46786579c8b29caf5102f95ddb133674bed68b`

- [AbstractRpcController.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/main/java/org/thingsboard/server/controller/AbstractRpcController.java): obtains the current security user and validates `Operation.RPC_CALL` against the target Device before forwarding. Authentication alone is not authorization to send every command.
- [RpcControllerTest.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/test/java/org/thingsboard/server/controller/RpcControllerTest.java): uses Tenant-admin identity and Device-bound RPC persistence/listing/deletion tests. These are not evidence for our capability/purpose/risk matrix.
- [CommandFeedbackHandlerTest.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/test/java/org/thingsboard/server/service/agent/CommandFeedbackHandlerTest.java): rejects another Tenant/agent when applying agent-event feedback. This corroborates exact owner identity enforcement; it is not the Device RPC authorization implementation.
- [Official Command & Control docs](https://thingsboard.io/docs/user-guide/command-and-control/), inspected 2026-10-08: operator-facing controls use the RPC API. This live website is supplementary, not a release-pinned implementation authority.

**ADOPT:** server-side target operation authorization before dispatch. **ADAPT:** retain HVAC's stronger exact Device/capability revision/purpose/risk permission and signed grant rather than substituting a generic RPC role. **REJECT:** browser-visible control, telemetry-read access or authenticated session as implicit command permission.

### OpenEMS 2026.9.0 — `14f0dedf5fe279845281bafaf0e48ea0ab51333a`

- [JsonrpcRoleEndpointGuard.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.common/src/io/openems/edge/common/jsonapi/JsonrpcRoleEndpointGuard.java): endpoint guard reads authenticated User role and asserts the endpoint's minimum role.
- [JsonrpcRoleEndpointGuardTest.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.common/test/io/openems/edge/common/jsonapi/JsonrpcRoleEndpointGuardTest.java): administrator passes an ADMIN guard; installer raises an exception. This is meaningful endpoint permission evidence, not Tenant/Site or physical-risk permission coverage.
- [coreconcepts.adoc](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/doc/modules/ROOT/pages/coreconcepts.adoc): Channel access mode is explicit metadata; read and write operations have different responsibilities. The guard's source documentation describes its required role.

**ADOPT:** explicit write-operation permission and negative authorization tests. **ADAPT:** persistent HVAC IAM facts express narrower scoped authority than an edge-wide role threshold. **REJECT:** adding an OpenEMS runtime/framework or mapping every site reader to a control role.

### MyEMS v6.9.0 — `b360f5bb4c2be4fd15854057963531b2e8d1bc0a`

- [command.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/core/command.py), `CommandSend.on_put`, invokes `admin_control` before publishing.
- [useractivity.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/core/useractivity.py), `admin_control`, checks stored session expiry and an administrator user who is not read-only. It does not implement this project's exact capability/purpose/max-risk contract.
- [test_mqtt.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/test_mqtt.py): manual publish procedure, without assertions protecting command permission scope. Pinned tree inspection found no command-specific test file; that is a bounded discovery result.
- [README.md](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/README.md): distinguishes optional enterprise equipment-control functionality. No enterprise authorization implementation was reviewed.

**ADOPT:** persisted authenticated write authority. **REJECT:** global administrator flag as sufficient HVAC Device/capability/purpose/risk authority and publish-success as physical success.

## Smallest correct owner extension

**ADAPT the existing IAM PostgreSQL permission pattern.** Add a narrow PostgreSQL Command authorization adapter around the existing `PostgresAuthorizationStore`, reusing its pool/helpers. Its factory receives the existing declared Command policy/emergency revision configuration, and it implements `LookupCommandAuthorization` from PostgreSQL facts. Wire the adapter into both embedded and standalone IAM. Reuse principal resolution, active Tenant membership, transaction/RLS context and signed grant machinery. No new service, authorization engine, runtime static grant list, test-only bypass or package is needed.

One new IAM-owned `command_permissions` table is justified because existing read-oriented role/site facts cannot represent all required dimensions without granting unproven authority. Persist principal, Tenant, Site, Device, capability, capability revision, purpose, maximum risk, ALLOW/DENY, status, validity and normal record revision/timestamps. Constrain supported enums and intervals at persistence; use exact tuples rather than nullable scope wildcards. Follow existing UUIDv7 and forced principal/Tenant RLS conventions; runtime receives SELECT, not permission-authoring rights. Resolve Device ownership against current Registry facts so a permission cannot authorize a stale or cross-Site target. Existing Registry CONTROLS and active writable COMMAND validation remains mandatory at the application boundary.

Active membership plus an exact current command permission is the authority; a role/site read grant is not its substitute. If product policy requires generic deny bindings to veto commands, express that requirement explicitly and apply existing effective deny facts; do not silently reinterpret Registry-read denies as a new command policy. Permission authoring must follow IAM's current record/revision conventions. Missing permission remains denied; PostgreSQL errors remain failures, with no fallback to static grants.

**Preserve the existing declared Command policy contract for #397.** Supply the same `COMMAND_POLICY_REVISION` / `COMMAND_EMERGENCY_REVOCATION_REVISION` values to the IAM adapter and Command consumer. These are grant-protocol metadata, not permission-authoring configuration. Do not reuse `registry-read` revision, duplicate the declared revision into an additional IAM policy JSON row, or derive permissions from environment values. DB identity/membership/permission facts remain the sole basis for allowance. Reject invalid adapter revision configuration at composition time.

Bounded revocation claim: changing a permission or membership affects the next PostgreSQL authorization decision. It does not retroactively invalidate an already issued signed grant under this configured-revision contract; such a grant is bounded by its TTL and the existing consumer checks. Rotating the deployment's emergency revision rejects older grants according to the current protocol. Immediate DB-driven grant revocation would require a separate authoritative revision/status lookup and tests; do not claim or introduce that redesign as part of this bounded repair.

Provision only the required real operator/Device/capability/purpose/risk tuples for A/B through an explicit existing owner migration/administration path, with recorded scope. Durable facts used by runtime must exist in PostgreSQL. Acceptance must not secretly insert broad grants outside the reviewed owner contract. Administrative UI for new permission management is not required merely to repair this current bounded command path, but the persisted permission-authoring procedure must be explicit.

## Required product evidence

Reuse the IAM PostgreSQL test runner and current domain task matrix. Small direct tests should prove an actual stored exact permission allows submission; absent/wrong Device, Site, capability revision or purpose denies; submit never authorizes approval; suspended/expired membership or permission denies; exact DENY overrides ALLOW; risk ceiling survives signing and is enforced by Command; runtime cannot read another principal/Tenant's permission; a mismatched declared policy/emergency revision is rejected by existing Command grant validation.

Then demonstrate real authenticated public API command flow for both existing Sites through the production PostgreSQL IAM store, Connectivity and accepted readback. Until that evidence exists, stories 23/26 remain incomplete. This record is a source-grounded implementation recommendation, not proof that the missing adapter has been implemented or that the live command succeeds.
