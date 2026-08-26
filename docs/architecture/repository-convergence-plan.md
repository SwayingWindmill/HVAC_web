# HVAC Web Repository Convergence Plan

Status: PROPOSED / implementation-ready sequencing  
Date: 2026-08-26  
Scope: repository structure, deployment truth, governance simplification, repository noise/volume  
Authority: `AGENTS.md`, `contracts/architecture/backend-architecture.v2.json`, `docs/architecture/phase1-overall-architecture.md`, `docs/architecture/backend-architecture-v2-conformance.md`, `docs/architecture/deployment-architecture-v1.md`

## 1. Problem statement

The repository has already converged its Phase 1 runtime architecture, but the source tree, historical deployment assets, developer commands, and local working artifacts still expose several older shapes at the same time.

The current mismatch is structural rather than cosmetic:

```text
Logical domain / owner
        !=
source directory
        !=
deployable process
        !=
container / deployment topology
```

The authoritative Phase 1 runtime shape is already documented as:

```text
energy-api
+ iot-service
+ telemetry-worker
+ metric-worker
+ scheduler

selective / on-demand:
forecast, optimization, FDD and other explicitly enabled workloads
```

However, the source tree still presents many historical `services/*-service` directories as independent Go modules, `go.work` registers those historical modules directly, Compose builds the converged processes from those old paths, and `package.json` / CI tooling still exposes many S0/S1/S2/S3/S4/S5-era commands.

At the same time, repository-local `.worktrees/`, `.clones/`, `.scratch/`, `out/`, and a heavily expanded `.ai-bridge/` materially distort code inventory, search, dependency analysis, IDE indexing, file watching, and agent reasoning. A workspace inspection currently sees roughly 19k files and is truncated before it can fully analyze the actual product source; related-test discovery is already polluted by worktree and OpenEMS-clone matches.

This plan therefore takes priority over new Energy/FDD/Optimization product expansion. The purpose is to make the repository tell one truthful story before adding more product surface.

### 1.1 Measured RC-00 baseline

Workspace inspection on 2026-08-26 measured roughly **19,055 files** before source analysis was truncated at its inventory/analysis limits. The dominant non-product surfaces were:

| Surface | Files observed |
| --- | ---: |
| `.worktrees/` | 10,094 |
| `.clones/` | 8,293 |
| `.ai-bridge/` | 175 |
| `.scratch/` | 40 |
| actual `apps/` source tree | 209 |

The analyzer continued to report OpenEMS clone and linked-worktree entrypoints/tests even after codegraph ignore configuration was tightened. Therefore RC-01 requires physical relocation/deletion of these local copies; ignore configuration alone is not accepted as completion evidence.

## 2. Goals

1. Make the target Phase 1 deployment shape obvious from the source tree.
2. Separate logical domain ownership from executable/deployable entrypoints.
3. Retire historical deployment shapes instead of carrying compatibility containers or duplicate runtime paths.
4. Reduce repository-local file volume and search/index noise substantially.
5. Replace ticket/stage-oriented permanent governance with domain/capability-oriented governance.
6. Keep each migration step buildable and reviewable; avoid a big-bang directory rewrite.
7. Preserve existing business behavior, public contracts, data ownership, security boundaries, and current unrelated Energy work.

## 3. Non-goals

This convergence program does **not**:

- add FDD, Optimization, Cost, Carbon, Baseline, or other new product features;
- change Energy Fact semantics or the current Energy rebuild implementation;
- change public HTTP/MQTT business contracts merely to fit new folders;
- implement the future Edge Control Plane;
- introduce Kubernetes, Kafka, a service mesh, Nx, Turborepo, Bazel, or another orchestration framework;
- create compatibility shims between old and new source paths;
- rewrite all Go modules into one module in a single change;
- preserve historical deployment entrypoints merely because scripts still reference them;
- keep generated evidence or temporary agent scripts in the repository as a substitute for source control history or CI artifacts.

## 4. Governing principles

### 4.1 One deployment truth

`deploy/platform/phase1` is the canonical Phase 1 deployment surface. Historical deployment assets may remain only when they are still a referenced input to the canonical topology; otherwise they must be migrated, archived as documentation-only evidence, or deleted.

### 4.2 Logical owner is not a process

IAM, Registry, Telemetry Query, Audit, Alarm, Work Order, and Command remain independent logical owners even when physically hosted in `energy-api`. Ownership, schema, authorization, outbox, and contract boundaries remain intact.

### 4.3 Executables must be visually distinct from domain modules

The target source vocabulary is:

```text
cmd/       executable / deployable entrypoints
modules/   logical business/domain modules
libs/      reusable technical or narrow cross-domain libraries
apps/      user-facing application source
```

A folder ending in `-service` must not be the permanent way to represent a logical domain that no longer deploys as an independent service.

### 4.4 History belongs in Git and reviewed documents, not permanent runtime paths

S0/S1/S2/S3/S4/S5 and ticket/PR identifiers are valid historical evidence but are not the permanent developer interface, package-script taxonomy, deployment taxonomy, or module taxonomy.

### 4.5 Ignore rules are not noise governance

`.gitignore` prevents accidental commits; it does not reliably prevent IDEs, language servers, repository analyzers, or agents from walking those directories. Large cloned repositories and linked worktrees must live outside the product repository root.

### 4.6 Prefer deletion to compatibility

After an old entrypoint has no supported runtime/developer/CI consumer, delete it. Do not keep forwarding binaries, compatibility Compose services, duplicate package scripts, or old aliases indefinitely.

## 5. Target repository shape

Target end-state:

```text
HVAC_web/
├── apps/
│   └── hvac-web/
├── cmd/
│   ├── energy-api/
│   ├── iot-service/
│   ├── telemetry-worker/
│   ├── metric-worker/
│   └── scheduler/
├── modules/
│   ├── iam/
│   ├── registry/
│   ├── telemetry/
│   ├── energy/
│   ├── audit/
│   ├── alarm/
│   ├── workorder/
│   ├── command/
│   ├── metric/
│   ├── settlement/
│   ├── notification/
│   ├── rules/
│   └── intelligence/
├── services/
│   └── <only workloads that are intentionally independently deployable>
├── libs/
├── contracts/
├── infra/
├── deploy/
├── semantic/
├── scripts/
├── tests/
├── benchmarks/
├── pocs/
├── docs/
├── AGENTS.md
├── README.md
├── go.work
├── package.json
└── ...root configuration files only
```

Notes:

- `cmd/` represents process topology, not domain ownership.
- `modules/` represents logical owner/code boundaries.
- `services/` becomes optional and small. It is reserved only for intentionally independent workloads such as selective intelligence or a non-Go service where keeping a self-contained package is materially clearer. It must no longer be the generic backend root.
- Moving domain packages into `modules/` happens after entrypoint seams exist; no big-bang move is required.

## 6. Canonical process-to-module mapping

| Target deployable | Logical responsibilities hosted in Phase 1 | Current primary source surfaces | Target action |
| --- | --- | --- | --- |
| `energy-api` | Gateway/BFF, Realtime public boundary, IAM, Registry, Telemetry Query, Audit, Alarm, Work Order, Command | `platform-gateway`, `iam-service`, `platform-core-service`, `telemetry-query-service`, `audit-ledger-service`, `alarm-service`, `work-order-service`, `command-service` | MERGE entrypoints into `cmd/energy-api`; MOVE owner packages to `modules/*` incrementally |
| `iot-service` | MQTT receive, command dispatch, reported-state verification | `mqtt-telemetry-adapter`, `command-dispatcher`, verification code in current command/IoT runtime | MERGE entrypoints into `cmd/iot-service`; MOVE reusable owner/runtime packages to modules |
| `telemetry-worker` | ingest/latest/realtime data plane, history projection, Energy analytics projection | `telemetry-runtime-service`, `analytics-read-model-projector`, `telemetry-shadow-comparator` where still required | MERGE active worker entrypoints; DELETE shadow/cutover-only paths after proof of retirement |
| `metric-worker` | METRIC job execution and reconcile | `metric-engine-service` | MOVE executable to `cmd/metric-worker`; MOVE domain code to `modules/metric` |
| `scheduler` | cross-domain durable job coordination only | `scheduler-service` | MOVE executable to `cmd/scheduler`; keep business execution outside scheduler |
| selective intelligence | FDD / Forecast / Optimization where explicitly enabled | `fdd-service`, `forecast-service`, `optimization-service` | KEEP independently deployable for now; normalize naming only after core convergence |
| supporting independent workloads | Settlement, Notification, outbound delivery, rule runtime, Operations Agent when deployment contract still calls for independent workload | corresponding current directories | KEEP temporarily; review individually after core convergence rather than forcing them into `energy-api` |

## 7. Repository KEEP / MOVE / MERGE / DELETE inventory

### 7.1 Root/local working surfaces

| Surface | Decision | Destination / rule | Rationale |
| --- | --- | --- | --- |
| `.worktrees/` | MOVE OUT | sibling root such as `E:\Code\HVAC_web-worktrees\` | Full repository copies dominate file count and corrupt code/search/test discovery. |
| `.clones/` | MOVE OUT | shared reference root such as `E:\Code\references\` | Preserve source-first review, but keep upstream repositories outside the product tree. |
| `.scratch/` | MOVE OUT / DELETE | external scratch root; delete disposable snapshots | Scratch copies are not product source. |
| `out/` | KEEP AS EPHEMERAL, PURGE | only current generated artifacts; no durable history | Must be safely disposable. Old PR/ticket logs, copied source, caches and evidence cannot accumulate indefinitely. |
| `.ai-bridge/` | KEEP, SHRINK | fixed control-plane allowlist only | Current directory contains many one-off scripts/copies/logs that contradict its own README. |
| `.codegraph/` | KEEP LOCAL IF TOOL REQUIRES | generated local index only; never product authority | Small generated tooling state is acceptable if bounded. |
| `.ruff_cache/` and tool caches | DELETE/REGENERATE | normal external/local cache behavior | Generated cache only. |
| `.workbuddy/` | MOVE OUT / DELETE if unused | external tool state | Not product source. |
| root Go tarball | DELETE after provenance check | tool cache outside repo | Toolchain archive does not belong at root. |
| root `platform-gateway` binary/artifact | DELETE after provenance check | generated output outside repo | Root executable conflicts with source/deployment truth. |
| strange root `/` | DELETE after provenance check | n/a | Invalid unexplained root surface. |

### 7.2 `.ai-bridge` allowlist

The bridge should normally contain only:

```text
.ai-bridge/
├── README.md
├── current-plan.md
├── agent-status.md
├── implementation-diff.patch
├── decisions.md
├── open-questions.md
├── execution-log.jsonl
├── handoff-run-state.json
├── codex-status.md       # temporary legacy allowance
└── session-log.jsonl     # temporary legacy allowance
```

One-off `apply-*`, `fix-*`, `inspect-*`, copied `.go` files, job logs, downloaded PR data, temporary tokens, browser blobs, and generated prompts are not permanent bridge contents. Temporary agent work belongs outside the repository or in an OS temp directory and is deleted after the run.

### 7.3 Current `services/` classification

This classification is about **permanent source shape**, not whether the code can be deleted immediately.

| Current directory | Decision | Target |
| --- | --- | --- |
| `alarm-service` | MOVE/MERGE | `modules/alarm` + `cmd/energy-api` composition |
| `analytics-read-model-projector` | MOVE/MERGE | `modules/energy` or telemetry/energy projection package + `cmd/telemetry-worker` |
| `audit-ledger-service` | MOVE/MERGE | `modules/audit` + `cmd/energy-api` |
| `command-dispatcher` | MOVE/MERGE | `modules/command` / IoT runtime + `cmd/iot-service` |
| `command-service` | MOVE/MERGE | `modules/command` + `cmd/energy-api` |
| `fdd-service` | KEEP FOR NOW | independently deployable selective intelligence workload |
| `forecast-service` | KEEP FOR NOW | independently deployable selective intelligence workload |
| `iam-service` | MOVE/MERGE | `modules/iam` + `cmd/energy-api` |
| `identity-service` | KEEP | verified canonical Phase 1 Identity Infrastructure; keep separate from business IAM and preserve OIDC/authentication-only authority |
| `legacy-migration-service` | VERIFY→DELETE | remove after canonical migration/cutover paths prove no runtime dependency |
| `maintenance-service` | MOVE | verified as a separate Phase 1 supporting worker; move executable to `cmd/maintenance-worker` and domain code to `modules/maintenance` without merging its cross-domain privileges into Scheduler or energy-api |
| `metric-engine-service` | MOVE/MERGE | `modules/metric` + `cmd/metric-worker` |
| `mqtt-telemetry-adapter` | MOVE/MERGE | IoT ingress module + `cmd/iot-service` |
| `notification-service` | MOVE/MERGE | canonical Phase 1 keeps Notification embedded in `energy-api`; move domain code to `modules/notification` and delete the standalone executable when no non-canonical consumer remains |
| `oidc-test-provider` | MOVE | `tools/` or test fixture surface; never production service taxonomy |
| `operations-agent-service` | KEEP FOR NOW | independently packaged TypeScript workload; later naming cleanup may move it under `apps/` or dedicated workload root |
| `optimization-service` | KEEP FOR NOW | independently deployable selective intelligence workload |
| `outbound-delivery-service` | KEEP FOR NOW | independent supporting workload pending deployment review |
| `outbox-relay` | VERIFY→MERGE/DELETE | keep only where durable fan-out still requires an independent process; do not preserve a generic relay by inertia |
| `platform-core-service` | MOVE/MERGE | `modules/registry` and related platform foundation modules + `cmd/energy-api` |
| `platform-gateway` | MOVE/MERGE | gateway module + `cmd/energy-api` |
| `rule-runtime-service` | KEEP FOR NOW | independent automation workload pending workload review |
| `scheduler-service` | MOVE | `cmd/scheduler` + narrow coordination module |
| `settlement-service` | KEEP FOR NOW | independent domain workload until Energy/Settlement topology is deliberately revisited |
| `telemetry-query-service` | MOVE/MERGE | telemetry query module + `cmd/energy-api` |
| `telemetry-runtime-service` | MOVE/MERGE | telemetry data-plane module + `cmd/telemetry-worker` |
| `telemetry-shadow-comparator` | VERIFY→DELETE | cutover/shadow diagnostic only unless current canonical topology proves otherwise |
| `work-order-service` | MOVE/MERGE | `modules/workorder` + `cmd/energy-api` |

`VERIFY→...` means the ticket must first search canonical Compose, build scripts, runtime configuration, generated contracts, and tests for active references. If none exist, deletion is the default; no compatibility wrapper is created.

### 7.4 Deployment and infrastructure trees

| Surface | Decision | Rule |
| --- | --- | --- |
| `deploy/platform/phase1/` | KEEP / CANONICAL | only supported Phase 1 runtime topology |
| `deploy/acceptance`, `deploy/observability`, active platform assets | KEEP if consumed by canonical topology | rename only when it removes real ambiguity |
| `deploy/s0`, `deploy/s2`, `deploy/s3`, `deploy/s4`, old RMS/cutover assets | VERIFY→MERGE/DELETE | move still-live inputs under canonical deployment hierarchy; delete retired executable paths; retain only concise historical docs where necessary |
| `infra/durability`, `infra/observability` | KEEP / CURRENT | stable responsibility-oriented split of the former `infra/s0-durable`; durability owns PostgreSQL/Redpanda/Toxiproxy test substrate, observability owns OTel/Prometheus/alerts/dashboards |
| `infra/registry`, `infra/telemetry`, `infra/command`, `infra/alarm`, `infra/workorder`, `infra/identity`, `infra/connectivity`, `infra/operations-agent`, `infra/central-plant-local` | KEEP if current | stable responsibility-oriented infrastructure names; Registry, Telemetry, Command, Alarm and Work Order were renamed from S1/S2/S3/S4/S5 during RC-06 |

Important: stage-named `infra/` directories are not assumed obsolete. Many hold authoritative migrations. They are a naming/convergence problem, not a deletion target until ownership and migration order are preserved.

## 8. Governance target

### 8.1 Developer-facing command surface

The root command interface should become small and stable. Target shape:

```text
npm run dev
npm run build
npm run test
npm run lint
npm run check
npm run domain -- <domain> <task>
```

Optional explicit operational commands may remain when they are genuinely distinct, but historical ticket/stage commands are not part of the primary interface.

### 8.2 Domain task matrix becomes the compatibility breaker, not another compatibility layer

`scripts/domain-task-matrix.mjs` is the existing migration seam. Use it to replace S0-S5 developer entrypoints with semantic capability tasks such as:

```text
identity
registry
telemetry
gateway
energy
command
alarm
work-order
rules
notification
intelligence
deployment
reliability
security
```

During each migration ticket, the matrix may temporarily invoke an old script internally. The ticket is not complete until either:

1. the old script is renamed/merged into the semantic task implementation; or
2. the old script is deleted because the underlying test/check is redundant.

Do not declare convergence merely because an alias hides an S-numbered script.

### 8.3 CI semantics

Permanent CI gates are named by invariant/capability, for example:

```text
repository-governance
architecture-contracts
public-contracts
security-boundaries
registry
authoritative-telemetry
energy-analytics
command-governance
alarm
work-order
real-web
phase1-deployment
```

Ticket/PR/stage identifiers remain in evidence metadata and history, not job topology.

### 8.4 Repository governance checker

`check-repository-governance.ts` must stop deriving repository truth from every child directory of `services/`. After source-topology migration begins, it should validate the target roots and canonical architecture contract instead.

At minimum it should enforce:

- no `.worktrees/`, `.clones/`, or `.scratch/` under the product root in the normal supported layout;
- `.ai-bridge/` contains only its allowlisted control-plane files;
- forbidden large/binary/toolchain artifacts are absent from root;
- `out/` is ignored and treated as disposable output, not an input to builds or authoritative docs;
- new deployable entrypoints exist only in the canonical executable/workload roots;
- README documents the canonical source and deployment topology rather than enumerating historical service folders.

Prefer reading existing `contracts/architecture/backend-architecture.v2.json` for process truth instead of inventing a second large architecture manifest.

## 9. Execution sequence

The work must proceed as a prefactor-first migration. Each ticket is independently reviewable and must leave the main supported path working.

### RC-00 — Freeze convergence baseline and deletion guardrails

**Purpose:** make later deletion/moves safe without changing runtime behavior.

Work:

- publish this plan as the convergence authority;
- inventory active references to historical deployables, stage-named deployment paths, and root-generated surfaces;
- record the current canonical Phase 1 process list from the existing architecture contract;
- define the `.ai-bridge` allowlist and external local-workspace convention;
- explicitly mark the current Energy rebuild files as unrelated dirty work that must be preserved;
- identify any historical artifact that must be retained for legal/security/release evidence before cleanup.

Exit criteria:

- every deletion candidate has an owner/action/confidence;
- no runtime code is moved yet;
- current dirty Energy work is unchanged.

### RC-01 — Remove repository-local volume and indexing noise

**Purpose:** immediately improve IDE/agent/search accuracy before source refactoring.

Work:

- move Git worktrees to a sibling external worktree root;
- move pinned upstream clones to an external references root while preserving version/commit evidence in reviewed docs/lock metadata;
- S0 release evidence and the S1 IAM provider workflow have been rehomed from `.scratch/go-data-ai-platform*` to `docs/evidence/go-data-ai-platform*`; active release/CI consumers no longer depend on `.scratch`;
- move/delete the remaining historical `.scratch` planning snapshots and keep new repository-local scratch unsupported;
- purge old `out/` history, duplicated source, logs and caches; keep only disposable generated-output semantics;
- shrink `.ai-bridge` to its documented allowlist;
- remove generated tool caches and root stray artifacts after provenance verification;
- update local tooling documentation so new worktrees/clones are never created under the product root.

Exit criteria:

- normal repository inventory is dominated by product source rather than linked copies/upstream clones;
- code analysis no longer reports worktree/OpenEMS-clone files as related tests for local source changes;
- root tree is understandable without hiding hundreds of generated files;
- no product behavior changes.

### RC-02 — Make deployment truth singular

**Status:** COMPLETE — 2026-08-26

**Purpose:** ensure one supported Phase 1 execution topology before source moves.

Completion result: `deploy/platform/phase1/compose.yaml` is the single canonical Phase 1 runtime; `deploy/platform/phase1/runtime-inventory.v1.json` classifies every Compose service exactly once; canonical and owner-split Go image builds no longer depend on historical `deploy/s0` Dockerfiles. The four default business deployables remain `energy-api + iot-service + telemetry-worker + metric-worker`; `scheduler + maintenance` are supporting workloads; `identity-service` is Identity Infrastructure; Forecast / Optimization / FDD are optional intelligence. Live `infra/s*` configuration/schema inputs remain intentionally scheduled for RC-06 stable-path migration rather than being duplicated during RC-02.

Work:

- treat `deploy/platform/phase1` as canonical;
- trace all references from canonical Compose/Dockerfiles/scripts into `deploy/s*`, `infra/s*`, legacy target/canary/shadow assets;
- move still-required deployment inputs into semantic/canonical locations;
- delete retired Compose/services/ThingsBoard/legacy/shadow runtime paths once unreferenced;
- keep Stage 1 owner-split as an explicit overlay of the same product version, not a second default architecture;
- update deployment docs to distinguish current Stage 0, optional Stage 1, and historical evidence.

Exit criteria:

- one default Compose path starts the Phase 1 system;
- historical deployment trees are not executable alternate truths;
- target deployable list in docs, architecture contract and Compose agrees.

### RC-03 — Introduce canonical executable entrypoints

**Status:** COMPLETE — 2026-08-26

**Purpose:** decouple process topology from historical service source directories before moving domain code.

Completion result: the six long-running Phase 1 application/supporting workloads now build from canonical root entrypoints under `cmd/`: `energy-api`, `iot-service`, `telemetry-worker`, `metric-worker`, `scheduler`, and `maintenance-worker`. `deploy/platform/phase1/runtime-inventory.v1.json`, canonical Compose `SERVICE_PACKAGE`, `go.work`, and the canonical Go Dockerfile are checked together by `deployment:phase1:check`; historical executable paths under `services/*/cmd/<legacy-name>` have no active repository references. Domain implementations remain in historical modules only as a temporary RC-03 Go `internal/` visibility seam and are scheduled for removal by RC-04.


Work:

- create canonical executable entrypoint locations for `energy-api`, `iot-service`, `telemetry-worker`, `metric-worker`, `scheduler`, and `maintenance-worker`;
- move or compose existing main/startup logic behind those entrypoints without changing domain APIs;
- rewire Docker image builds and canonical Compose to those entrypoints;
- keep owner packages in their current locations temporarily where moving them would enlarge the diff;
- remove old standalone executable entrypoints as soon as the canonical process owns their startup responsibility.

Exit criteria:

- Phase 1 images build from canonical executable entrypoints;
- runtime behavior and owner boundaries remain equivalent;
- old independent commands for embedded owners are absent unless an explicit Stage 1 owner-split requirement still consumes the same code through a deliberate entrypoint.

### RC-04 — Move logical owner code into domain modules

**Status:** COMPLETE — 2026-08-26

**Purpose:** make source layout reflect logical ownership rather than old process boundaries.

Completed first batch: Audit, Alarm and Work Order have moved from historical `services/*-service` module namespaces into `modules/audit`, `modules/alarm` and `modules/workorder`. Their public consumers, `go.work`, owner-split entrypoints, domain tests, migration manifests and domain-owned PostgreSQL migrations/testdata now point only at the new module roots; repository searches for the three historical source roots and Go import paths return zero active matches. `modules/alarm` and `modules/workorder` moved persistence assets with the domain to avoid split authority. Focused Audit durability/security, Alarm S4/S16, Work Order S5, `energy-api`, owner-split, Phase 1 deployment and repository governance gates pass after the moves.

Completion result: IAM, Registry, Command, Telemetry, Metric, Energy, Scheduler, Maintenance and IoT execution code now live under `modules/` with default runtime entrypoints under `cmd/`. Historical Shadow and migration-only code has exited active product/CI paths and is retained only under `pocs/`, `tools/` or `docs/evidence/` where useful. The former `services/platform-gateway` application layer is absorbed directly into `cmd/energy-api/internal/*`; no replacement platform module or compatibility layer was introduced. `services/` is reserved for deliberately independent workloads.

Verification follows the minimum affected-surface rule: owner tests and hosting-process builds plus architecture, deployment and repository invariants. RC-04 adds no directory-placement tests or duplicate stage gates.

Migration order:

1. narrow/easier owners first: Audit, Alarm, Work Order — **COMPLETE**;
2. IAM and Registry/Foundation — **COMPLETE**;
3. Command — **COMPLETE**;
4. Telemetry Query / Telemetry Runtime — **COMPLETE**;
5. Energy projection / Metric — **COMPLETE**;
6. supporting workloads only where the move materially improves truth — **COMPLETE**.

For each owner:

- identify public package boundary and current consumers;
- move owner code to `modules/<owner>` without changing its ownership contract;
- update imports, `go.work`, focused tests and build references;
- delete the historical `services/<owner>-service` directory when empty;
- do not leave import-forwarding compatibility packages.

Exit criteria:

- embedded logical owners are represented under `modules/`, not independent `*-service` roots;
- `go.work` contains intentional module boundaries only;
- `services/` contains only deliberately independent workloads.

### RC-05 — Simplify repository and CI governance

**Status:** COMPLETE — 2026-08-26

**Purpose:** stop paying permanent operational cost for project history.

Work:

- make domain task matrix the canonical semantic task catalog;
- migrate active checks away from S0-S5/ticket naming;
- delete obsolete/redundant check/run/verify scripts;
- merge duplicated Compose/PostgreSQL/browser harness logic into existing shared helpers rather than a new framework;
- shrink root `package.json` to stable developer commands and a small number of explicit operational commands;
- rename permanent CI jobs by capability/invariant;
- update repository governance to validate target topology rather than enumerate `services/` children;
- ratchet tooling-file count/bytes downward after deletions, rather than merely raising the existing JavaScript baseline.

Completed outcomes:

- daily PR selection now uses the affected capability/domain matrix instead of broad unknown suites for migrated domains;
- historical Snapshot, Ticket, stage-only and duplicate main-push workflows were retired to evidence rather than kept as active CI;
- Alarm, Work Order, Registry, Telemetry, Command and RMS daily verification were folded into stable domain/capability paths;
- security/observability behavior checks remain explicit while duplicate umbrella wrappers and Gate-on-Gate assertions were removed;
- long inline package-script exceptions were reduced from eighteen to three explicit certification/release operations (`real-assets:certify`, `s2:telemetry-release`, `s2:telemetry-cutover`);
- RMS Browser remains a real browser workflow while full RMS certification is manual (`workflow_dispatch`) rather than a normal push gate.

Exit criteria:

- a developer can discover the supported workflow from a small root command set;
- no normal development task requires knowing an S0-S5 ticket era;
- duplicated one-off gate scripts have been retired;
- governance code is materially smaller after the migration.

### RC-06 — Rename live stage-based infrastructure and close documentation drift

**Status:** COMPLETE — 2026-08-26

**Purpose:** remove the last historical naming mismatch after runtime/source consumers have already moved.

Work:

- Registry is complete: `infra/s1-registry` was renamed to `infra/registry` after preserving the full migration set and switching Phase 1 migration consumers, active workflows, checkers and architecture references atomically;
- Durability/observability split is complete: the former `infra/s0-durable` was separated into `infra/durability` for PostgreSQL/Redpanda/Toxiproxy and `infra/observability` for OTel/Prometheus/alerts/dashboards;
- migration runners, local topology and Phase 1 Compose references were switched atomically to the responsibility-oriented paths;
- README, architecture, deployment, development and governance documentation now use the responsibility-oriented infrastructure vocabulary;
- stale active architecture/deployment descriptions that treated historical stage paths as current were removed or corrected;
- final repository-wide scans confirm removed stage paths remain only in explicit migration history or retired evidence.

Exit criteria:

- source tree, infrastructure tree, deployment docs, architecture contract and developer commands use the same long-lived vocabulary;
- there is no supported path whose name is a historical delivery stage.

## 10. Verification strategy

This is a refactor/convergence program. Tests should protect externally meaningful seams, not folder placement itself.

Per-ticket minimum verification:

1. **RC-01:** repository governance + inventory/search sanity; no application build required unless a build input moved.
2. **RC-02:** canonical Phase 1 deployment/static topology checks and affected Compose/config tests.
3. **RC-03:** focused build/startup checks for the migrated process plus architecture/deployment consistency checks.
4. **RC-04:** affected owner unit/contract/integration tests and the hosting process build; do not run unrelated broad matrices.
5. **RC-05:** repository-governance tests, domain-task-matrix tests, affected CI/checker tests.
6. **RC-06:** migration/deployment path checks and final architecture/document consistency checks.

Repository-wide broad tests are not a substitute for focused proof and are not required on every mechanical move.

## 11. Acceptance metrics

The convergence program is complete when all of the following are true:

### Repository noise

- `.worktrees/`, `.clones/`, and `.scratch/` are not present under the normal product root.
- `.ai-bridge/` is limited to its documented control-plane files.
- `out/` is disposable and contains no authoritative source, required build input, or long-term project history.
- repository analysis can inspect the complete product source without exhausting its inventory on linked worktrees/upstream clones.
- root contains no unexplained binaries, toolchain archives, or malformed directories.

### Source topology

- canonical executable processes are visible under one executable root.
- embedded logical owners live under domain/module roots rather than looking like independent deployables.
- `services/` contains only intentionally independently deployable workloads, or is removed if no longer useful.
- `go.work` no longer mirrors the historical microservice decomposition by default.

### Deployment truth

- `deploy/platform/phase1` is the single default Phase 1 deployment entrypoint.
- default process topology matches the architecture machine contract.
- no retired ThingsBoard/legacy/shadow/canary path can accidentally become a second production architecture.

### Governance

- root developer commands are semantic and stable.
- domain task matrix contains no permanent dependency on S0-S5 naming after the relevant migration is complete.
- permanent CI jobs are named by capability/invariant, not ticket sequence.
- repository governance validates target topology rather than requiring every historical `services/*` directory to exist in README.
- JavaScript/tooling ratchets decrease as scripts are retired; cleanup does not simply increase accepted baselines.

## 12. Stop conditions / safeguards

Pause a migration ticket if any of the following occurs:

- the proposed move changes a public business contract rather than only source/deployment composition;
- a supposedly historical service is still the only owner of an active durable fact or authorization path;
- Stage 1 owner-split requires a reusable entrypoint that the proposed deletion would remove;
- an `infra/s*` directory contains ordered migrations whose path is embedded in release tooling not yet migrated;
- cleanup would delete evidence required outside Git/CI artifact retention;
- current unrelated Energy rebuild changes would need to be rewritten or reverted.

In those cases, prefactor the dependency first, then resume deletion. Do not add a compatibility layer to force progress.

## 13. Priority after convergence

Only after RC-00 through the core of RC-05 are stable should new product expansion resume. The next product work can then return to Energy rebuild/expansion and FDD on top of a repository whose source topology, deployment topology and governance all describe the same system.
