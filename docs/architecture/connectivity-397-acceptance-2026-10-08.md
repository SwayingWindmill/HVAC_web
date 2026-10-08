# #397 Connectivity parent acceptance — 2026-10-08

Implementation base: `2d9744fa73c6b928261beab48484ce0af1104d43`; branch `codex/issue-397-connectivity-acceptance`. Children #401–#407 were already merged at this base. This slice verifies their integrated product and repairs the exposed command/authorization/local-startup gaps.

## Repairs

- Local administrator bootstrap now provisions both Sites' explicit Telemetry scopes and exact IAM Command permissions. Runtime IAM reads current principal, membership and permission facts from PostgreSQL under forced principal/Tenant RLS; both embedded and standalone IAM use the same adapter. Permission authoring is the existing explicit local seed path, including `--authorization-only`, not a runtime bypass. Upserts do not inflate revisions when unchanged.
- Command feedback resolves Registry `feedbackPointKey` on the same Device and accepts its read-only SETTING type. Hardware `feedbackSourceKey` is not a Registry Point code. The writable COMMAND Point and current CONTROLS relationship remain required. The IAM capability validator uses the existing Command model's supported profiles.
- Public Command IDs accept the owner's canonical RFC UUIDs; Registry IDs remain UUIDv7. Owner responses and transitions use the existing public OpenAPI Intent statuses; the obsolete control-status projection is removed.
- `local:up` builds Connectivity with the platform once, then starts Connectivity and simulators without rebuilding platform dependencies. No new permanent gate, compatibility path or dependency was introduced.

Source decisions and pinned official source/tests/docs: [feedback](connectivity-command-feedback-source-review-2026-10-08.md), [Command IAM](connectivity-command-authorization-source-review-2026-10-08.md). ThingsBoard 4.4, OpenEMS 2026.9.0 and MyEMS 6.9.0 were compared; no upstream source was copied.

## Actual local runtime

Commands executed through WSL 2 in `/mnt/e/Code/HVAC_web`, project `hvac-local`. Both Site A/B simulators and the single Connectivity remained running during command acceptance.

Full source-build/local startup passed:

```sh
BUILDX_BUILDER=default PHASE1_GO_BUILD_IMAGE=hvac/issue397-go-cache:1.25.12 npm run local:up
```

Official Go proxy downloads had previously failed with TLS EOF. The temporary local build image is based on `golang:1.25.12-bookworm` and contains only the already checksum-verified Linux Go module download cache. It was not published, and no project Dockerfile, dependency or official image tag was changed. The override selects Docker's builder that can see this local image. Evidence: `out/397-local-up-verified.log`, exit 0; later targeted energy-api rebuild deployed the final public transport correction (`out/397-command-transport-{build,deploy}.log`). This is an idempotent source rebuild of the existing environment, preserving existing data, keys and spool; it does not claim a destructive empty-volume reset or fresh enrollment of both existing Gateways today.

Before repair, Site B's authenticated Snapshot returned 404 despite its registered Device. After repair, A/B return 200 with AVAILABLE, ONLINE, CURRENT and PRESENT/FRESH/GOOD samples. Evidence: `out/397-snapshot-red.log`, `out/397-snapshot-final.log`, `out/local-connectivity/snapshots.json`.

Authenticated browser session submitted the current **7°C chilled-water setpoint**, without changing the requested physical value, through `POST /api/v1/commands`, then polled public GET. Both requests returned 202/QUEUED and completed with provider acknowledgment plus authoritative telemetry readback:

| Site | Command ID | Submitted UTC | Verified UTC | Public result |
| --- | --- | --- | --- | --- |
| A | `8ce69e3e-f701-4f06-b87f-9e39e5bab2e2` | 06:40:10.948114 | 06:40:12.729528 | SUCCEEDED |
| B | `6714a6e5-cda5-468e-8dc9-468b4b0eb522` | 06:40:13.075941 | 06:40:14.773529 | SUCCEEDED |

Both transitions end in `ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED`, independently confirmed in the Command owner's PostgreSQL records. Evidence: `out/397-command-live.json`, `out/397-command-live-final.log`, `out/397-command-owner-final.log`, `out/397-two-sites-running.log`. A was not stopped to make B succeed. This proves setpoint application/readback in the simulated controller, not physical plant temperature convergence or industrial hardware certification.

Earlier public POST returned 503 after the owner had accepted intents because of the UUID/status disagreement. Read-only owner inspection confirmed both earlier intents were REJECTED with `DISPATCH_SAFETY_STATE_NOT_CURRENT` before dispatch. No blind retry of an unknown outcome or safety relaxation was used. After runtime readiness was current, the new intents above succeeded with the unchanged pre-dispatch safety verifier. Rejected intent IDs: `50f5012a-fe6b-47d6-b3df-62ca69a98885`, `5b6533b6-90c3-4245-950f-d8a39781e467`.

## Story coverage

| #397 stories | Authoritative evidence |
| --- | --- |
| 1–9, 32: Web registration, one-time code, 24h expiry, CSR/90-day certificate/broker URL, renewal, revocation, status and audit | [#407 real-browser/nginx acceptance](gateway-credentials-acceptance-2026-10-05.md), inherited merged evidence; today's existing Registry PostgreSQL suite rerun covers lifecycle/concurrency/identity contracts. Today's Chromium 1440×900 credential Dialog also showed ACTIVE with real data. |
| 10–14: dynamic scope, one process, identity isolation, broker ACL and inactive quarantine | Inherited #407 new Gateway enrollment through real nginx without restarting A/B; current Registry/Connectivity public-seam PostgreSQL tests; today's real Mosquitto ACL suite rejects A→B and forbidden direction; MQTT suite proves revoked quarantine. Multi-Tenant scope is covered by isolated owner tests, not a claim of two live local Tenants. |
| 15–20: current Registry mappings, Point metadata, unknown quarantine, fixed Connectivity source and per-Gateway Source Position | Existing Registry-view and Connectivity/Telemetry tests, rerun affected Go and PostgreSQL suites; today's MQTT report verifies resolved identities, registered Points and quarantine; actual A/B Snapshot reads. No stale mapping copy was added. |
| 21–22: backpressure and per-Gateway backlog | Today's real broker/Connectivity MQTT suite: queue peak 2, published 5/processed 5; per-Gateway metrics assertions; transient retry recovery, 4138-byte offline spool and 80 recovered observations. This is bounded exercised pressure/outage evidence, not an unlimited-load durability guarantee. |
| 23–26: all-Site commands, Registry routing/control authority, safety and readback | Actual A/B public POST→GET results above, current PostgreSQL IAM permissions, negative writable-Point/authorization tests and Command PostgreSQL suite. |
| 27: failed Command initialization fails process | MQTT suite `exitsWithoutCommandRuntime: true`, current Connectivity startup tests. |
| 28–29: real enrollment simulator and one-command two-Site local environment | Inherited #407 fresh simulator code+CSR through nginx, today's MQTT auto-enrollment/broker test and full `local:up` source build/startup above. Existing A/B certificate bundles were reused, not manually re-signed. |
| 30–31: public-seam tests, obsolete model deletion | Existing domain/architecture contracts and merged child removal, current public HTTP/MQTT/PG tests; no legacy integration/session/lease/fleet path reintroduced. |

## Final validation

- Existing Registry PostgreSQL runner: PASS (`out/397-iam-owner-postgres-final.json`), including current exact command permission and known-other-principal/Tenant isolation.
- Full affected Go packages: PASS (`out/397-affected-go-complete.log`): commandauth, Connectivity, Command, Telemetry, IAM and energy-api.
- Existing Command PostgreSQL runner: PASS (`out/397-command-postgres-complete.json`), including durable dispatch/readback contracts.
- Real Mosquitto ACL: PASS (`out/issue-397-acl-20261008/acl.json`); own topics allowed, cross-Gateway publish rejected (MQTT reason 135).
- MQTT uplink end-to-end: PASS (`out/issue-397-mqtt-20261008/integration.json`), real broker/Connectivity/Registry/PostgreSQL/simulator with a Telemetry receiver stand-in. Actual local Snapshot/command verification above separately uses production Telemetry.
- Existing platform contracts, Command contracts, deployment checks, `repo:check`, lint and `git diff --check`: PASS. Existing platform architecture baseline limitations are unchanged; a passing gate is not broad architecture certification.
- RED→GREEN at the public seams: reported SETTING/Point code selection, supported cold-water capability, current IAM persistence, UUIDv4 Command ID and public QUEUED status. Read-only SETTING submission remains rejected.

All raw logs, temporary probes/build files, browser screenshots and runtime credentials stay Git-ignored under `out/` or the existing runtime directory. This record retains reproducible commands, bounded claims and non-secret outcome identifiers.

## Two-axis review

Matt code-review skill reviewed the fixed `2d9744fa…380a5d6c` diff in parallel. **Standards: 0 findings**; the earlier unregistered manual-check finding was resolved by retaining only ignored ad hoc evidence, not a new repository gate. **Spec: 0 findings**; the previously missing actual A/B command and full local-startup evidence is now present. The axes were assessed separately against repository standards and all #397 stories. This review metadata is the only post-review change.

## Limits retained

No CRL was added. Gateway-wide revocation/quarantine and command denial are the current credential contract; selective leaf revocation/disconnection is not claimed. A DB permission change affects the next authorization decision; an already issued grant remains bounded by existing TTL/emergency-revision checks. No administrative permission-management UI, broker HA, second Connectivity deployment or batch-Telemetry redesign was added. Existing platform-wide baseline limitations and the #407 certificate-overlap transport limitation remain documented.
