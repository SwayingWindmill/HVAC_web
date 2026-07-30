# CI gate governance

## Purpose

Keep the repository's security, tenant-isolation, data-integrity and release assertions while reducing duplicated orchestration and unrelated pull-request checks.

The governing rule is: preserve durable assertions, but run them at the cheapest layer that still catches the risk before it matters.

## Baseline

Measured from `main` at `75a418a` on 2026-07-30:

- 41 GitHub Actions workflow files
- 76 declared jobs before matrix expansion
- roughly 545 workflow steps
- 72 `npm ci` steps
- all 41 workflows watch `package.json`
- 39 workflows watch `package-lock.json`
- 161 tracked JavaScript files, of which 138 are reachable from CI

The main sources of excess are broad root-file triggers, permanent Ticket workflows, repeated toolchain setup and release certification mixed into ordinary pull requests.

## Gate layers

| Layer | Purpose | Typical checks | Trigger |
|---|---|---|---|
| Ticket/local | Fast implementation feedback | focused unit, contract, compile, one database or browser smoke | developer command |
| Pull request | Protect the affected production boundary | static checks, affected unit/integration, narrow browser coverage | path-filtered PR |
| Main/nightly | Detect cross-domain and compatibility regressions | full browser, multi-database, Docker topology, broad security suites | affected `main`, schedule or dispatch |
| Release | Prove a release candidate | production images, SBOM, signatures, provenance, capacity, Kind, cutover and rollback | tag, release candidate or dispatch |

## Stable capability suites

Ticket identifiers are historical evidence, not permanent CI architecture. Durable checks should converge on stable capability names.

| Current family | Target capability suite |
|---|---|
| retired numbered `rms-*` and `rms-ticket-*` | `RMS Web Build`, `RMS Web Auth`, `RMS Web Routing`, `RMS Web Browser`, `RMS Web Certification` |
| `s0-*` | `platform-contracts`, `platform-auth`, `platform-durability`, `platform-security`, `platform-release` |
| retired `s1-ticket-*` | `S1 Registry Core`, `S1 IAM Provider POC`, `S1 Registry Migration`, `S1 Registry Routing`, `S1 Registry Web` |
| S2 telemetry workflows | `telemetry-baseline`, `iam-authorization`, `telemetry-runtime-snapshot`, `telemetry-ingest`, `gateway-snapshot`, `realtime-backend`, `telemetry-live-client`, `shadow-routing`, `hvac-web-presence`, `security-observability`, `telemetry-release`, `telemetry-cutover` |
| retired `s3-ticket-*` | `S3 Command Safety`, `S3 Command Authority`, `S3 Command API`, `S3 ThingsBoard Contract`, `S3 Command UX`, `S3 Command Certification` |

## Migration order

### Phase 1: governance and release separation

- Codify the gate rules in `AGENTS.md`.
- Keep release-specific PR validation lightweight.
- Run formal evidence, images, capacity and rollout jobs only on affected `main`, a tag or explicit dispatch.
- Keep Realtime PostgreSQL durability evidence inside the stable `realtime-backend` capability: `s2:realtime:postgres` replaces the generic baseline fixture and writes `out/s2-realtime-backend/realtime-postgres.json` without adding a second database job.

### Initial implementation

The first migration slice separates S2 release-asset validation from full release certification:

| Field | Value |
|---|---|
| Risk prevented | release manifests, evidence builders or capacity-attestation tests drift before merge |
| Owner | telemetry release |
| PR gate | `S2 Telemetry Release Assets / verify` |
| Trigger | S2 release workflows, deployment assets, named release scripts, `package.json` and `package-lock.json` |
| Execution layer | pull request |
| Expected runtime | under 5 minutes on a warm cache |
| Evidence | release-asset, release-evidence and capacity test output |
| Retirement | merge into the future stable `pr / contracts` or `telemetry-release-assets` aggregate |

The full `S2 Telemetry Production Release Certification` workflow no longer runs on pull requests. It runs after affected changes land on `main` or through explicit dispatch, preserving all existing PostgreSQL, ClickHouse, transport, capacity, browser, image, vulnerability, SBOM, provenance, Kind rollout and rollback assertions.

Because ClickHouse history integration previously existed only inside that release workflow, the migration adds the stable `S2 Telemetry History / verify` PR gate. It runs `npm run s2:history` only when telemetry contracts, S2 deployment assets, the telemetry runtime service or the owned history scripts change. This preserves the transactional outbox, retry deduplication, hourly rollup and projector integration coverage without running the full release matrix.

### Phase 2: trigger precision

- Remove broad root-file triggers from workflows that do not consume them.
- Treat `package-lock.json` as a dependency/static/security trigger, not an automatic database/browser/release trigger.
- Treat `go.work` and `go.work.sum` as Go build/test triggers only for workflows that consume the affected modules.
- Replace `scripts/**` with the exact scripts or stable script families owned by each workflow.

The first trigger-precision slice updates `Security 79 React Router 8`. It no longer watches every workflow file or every `scripts/check-s2-*` script. Its path set now names its own workflow and the exact script/import closure reached by `security:ticket-79` and `security:ticket-79:browser`. The existing React Router security test enforces that the two broad patterns do not return and that the precise paths remain present for both pull requests and `main` pushes.

The second trigger-precision slice removes `scripts/**` from `S0 Reproducible Delivery and Supply Chain`. The workflow now lists the script closure reached by its own commands. `scripts/check-s0-delivery-assets.mjs` derives that closure from workflow `npm run` commands, recursive package-script calls, direct script execution and script-to-script references, then fails if the PR path list is missing a dependency, contains an unrelated script or restores the repository-wide wildcard.

The next release-layer slice removes the duplicated S0 `release-evidence-pr` Kind job. Pull requests still run release asset validation and the deterministic rollout model in `verify`; the disposable Kind rollout, immutable image verification and final evidence bundle remain in the tag/manual `release-evidence` certification path. The S0 delivery gate prevents the PR Kind job from returning and verifies that formal certification still owns the rollout assertion.

### Phase 3: capability consolidation

- Merge Ticket workflows into the stable capability suites above.
- Preserve individual assertions and evidence artifacts while deleting duplicate wrappers.
- Keep historical Ticket numbers in release evidence and documentation, not workflow topology.

The RMS topology slice replaces eight numbered workflows with five stable capability suites. `RMS Web Routing` runs the complete Ticket 07 policy-test superset instead of relaunching the cumulative Ticket 03–07 build wrappers, while `RMS Web Browser` runs the two distinct Windows browser audits once with one checkout and dependency installation. Build and certification evidence now use `out/rms-web-build` and `out/rms-web-certification`. Every RMS workflow watches and executes `rms:topology:check`, which rejects restored numbered workflows, Ticket commands and Ticket-scoped evidence paths.

The S2 topology migration replaces the twelve `s2:ticket-01` through `s2:ticket-12` package entry points with commands named after the existing telemetry capabilities. Evidence now uses capability-scoped directories for baseline, IAM, runtime Snapshot, ingest, Gateway Snapshot, realtime, live client, shadow routing, HVAC Web, security/observability and release, while cutover keeps the established completion-evidence directory. Every capability command executes `s2:topology:check` first; the shared gate verifies workflow, command, artifact and evidence-directory mappings, and rejects restored Ticket commands, Ticket-named workflows or numbered evidence paths. Historical Go harness names remain unchanged because they are implementation entry points rather than CI topology or evidence ownership.

The first capability-consolidation slice replaces `S1 Ticket 01 Contract and Ownership` and `S1 Ticket 03 Core Registry Read Service` with the stable `S1 Registry Core` workflow. The new suite keeps the union of contract generation, ownership validation, SQLC POC, Registry baseline, IAM/Core build and security checks, plus one stable PostgreSQL evidence job. Shared Registry changes therefore use one Node/Go setup per capability job instead of launching two Ticket wrappers and two equivalent PostgreSQL baselines. `s1:registry:check` enforces the stable workflow markers and prevents the retired Ticket files and Ticket-scoped evidence paths from returning.

The final S1 topology slice renames the remaining Ticket 02, 04, 05 and 06 wrappers to `S1 IAM Provider POC`, `S1 Registry Migration`, `S1 Registry Routing` and `S1 Registry Web`. Five stable capability commands replace Ticket 01–06 package entry points, and evidence now lives under capability-scoped directories. Every S1 workflow watches and executes `s1:topology:check`, which rejects restored Ticket workflows or commands and verifies that S2 IAM continues to consume the stable Registry Core PostgreSQL evidence.

The second capability-consolidation slice replaces S3 Tickets 02, 03, 05 and 07 with `S3 Command Authority`. The suite runs PostgreSQL authority, governance/dispatch and reported-state verification checks once, executes the shared command database integration once, then runs one Go test/vet union and one Lint/Build pass. Its evidence path is stable under `out/s3-command-authority`, and the PostgreSQL authority gate prevents the four retired workflow wrappers or Ticket 02 report path from returning.

The third S3 slice replaces `S3 Ticket 09 Command Certification` with the stable `S3 Command Certification` workflow and `s3:certification:pr` command. Pull requests retain local-profile checks, target-runtime Go test/vet/build coverage, certification contract tests, target-image evidence tests, deterministic preflight and ownership validation. PostgreSQL command authority, TypeScript linting and the product build are no longer repeated because `S3 Command Authority` owns them. Signed images, SBOM, provenance, Trivy secret scanning, Cosign verification and the four-image candidate manifest remain restricted to `s3-v*` tags or manual certification runs. Static gates prevent the retired Ticket workflow/script and Ticket-scoped evidence path from returning.

The final S3 topology slice renames the remaining Ticket 01, 04, 06 and 08 wrappers to `S3 Command Safety`, `S3 Command API`, `S3 ThingsBoard Contract` and `S3 Command UX`. The package entry points are now the six capability commands documented in the S3 implementation plan; obsolete Ticket 01–09 npm commands are removed. Every S3 workflow watches and executes `s3:topology:check`, which verifies stable names, self-trigger paths, capability commands, release-only image publication and capability-scoped evidence paths while rejecting any restored `s3-ticket-*` workflow or command.

### Phase 4: reusable setup

- Centralize checkout, Node, Go, cache and dependency installation in reusable workflows or composite actions.
- Combine checks that can share setup without hiding failures or crossing security boundaries.
- Keep separate jobs where permissions, secrets, service containers or matrix isolation require it.

### Phase 5: enforcement

- Configure a small stable required-check set after workflow names settle.
- Target approximately 15 minutes wall-clock for required PR gates.
- Run broad regression after merge and formal certification only for a release candidate.
- Track flaky checks as defects with an owner and expiry instead of silently retrying or making them optional.

## Required-check target

The eventual branch rule should require stable aggregate checks rather than every historical job:

- `pr / static`
- `pr / contracts`
- `pr / affected-unit`
- `pr / affected-integration`
- `pr / affected-browser` when the change classifier marks browser impact

The classifier itself must fail closed: an unknown path selects the broader affected-domain suite, not no suite.

## Gate acceptance record

Every new or materially changed gate must document:

- risk or regression prevented
- owning domain
- trigger paths
- execution layer
- required or advisory status
- expected runtime
- evidence or artifact produced
- retirement or consolidation condition

A gate without a concrete risk owner and retirement condition should not be added.

## Review checklist

Before merging automation changes:

1. Trace every moved or deleted assertion to its new owner.
2. Verify YAML syntax and the smallest affected npm, Go or script entry points.
3. Confirm PR paths do not trigger unrelated domains.
4. Confirm release-only jobs cannot publish, sign or attest from an ordinary PR.
5. Confirm required check names remain stable or branch rules are updated in the same rollout.
6. Record any intentionally pending main/nightly or release certification.
