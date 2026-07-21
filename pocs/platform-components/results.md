# Platform component POC results

Evaluation date: 2026-07-21

These results are integration evidence, not production adoption approvals. The experiments remain isolated from accepted slice implementations and introduce no production route or data-owner change.

## Current result

| Component | Executed evidence | Result | Provisional decision |
|---|---|---|---|
| Envoy Gateway `v1.8.0` | Locked installer, restricted Kind cluster, explicit ClusterIP data plane, exact public route, unregistered Legacy rejection, route canary and rollback | Passed on GitHub Ubuntu/Kind | Adopt candidate as the Kubernetes traffic layer only; never replace the Go BFF/Gateway business boundary. |
| Debezium `3.6.0.Final` | PostgreSQL initial snapshot, insert/update/delete capture, table allowlist, connector restart and offset resume through the S0-compatible Redpanda broker | Passed locally | Adopt candidate for one-way Legacy CDC and migration evidence. It must not become a reverse synchronization or business dual-write path. |
| Redpanda Connect `v4.94.0` | Consumed the Debezium topic with `kafka_franz`, applied bounded Bloblang metadata mapping and delivered five uniquely offset events to an HTTP evidence sink | Passed locally with conditions | Do not approve production adoption yet. Keep only as a non-authoritative integration candidate pending license review and replacement of the deprecated `kafka_franz` input before the next major release. |
| Centrifugo `v6.8.1` | Authenticated WebSocket connection, subscription, live publication, one offline publication recovery, retained history and Prometheus metrics | Passed locally with conditions | S2 candidate for connection/publication transport only. A separate S2 experiment must prove platform-owned authorization, Snapshot/Cursor/Revision and scale behavior before adoption. |
| River `v0.35.1` | Domain row and job committed in one PostgreSQL transaction, rollback removed both, duplicate unique insertion yielded one job, restarted worker produced one business effect | Passed locally | Adopt candidate for owner-local background jobs. Do not use for Command retries, device side effects or shared cross-service workflow authority. |

## CI evidence

- Workflow run: `https://github.com/SwayingWindmill/HVAC_web/actions/runs/29802480177`
- Envoy/Kind artifact: `platform-component-pocs-envoy` (`8484251479`)
- Docker component artifact: `platform-component-pocs-docker` (`8484265241`)
- Assets, Envoy/Kind and Docker component jobs all completed successfully.
- The Envoy gate proved exact `/api/v1/status` routing to the Go-edge fixture, `404` for the unregistered Legacy path, deterministic canary routing to the private Legacy fixture and rollback to the Go owner without synthesizing business identity headers.

## Machine-readable local evidence

The local runner generated ignored artifacts under `out/platform-component-pocs/`:

- `docker-summary.json`
- `debezium-redpanda-connect.json`
- `centrifugo.json`
- `river.json`

Verified local invariants:

- Debezium connector state was `RUNNING` before and after restart.
- Captured operations were snapshot/read, create, update and delete.
- Only `public.legacy_registry` was captured; the excluded table was absent.
- Five transformed CDC records retained five unique Kafka source offsets.
- No reverse write and no invented platform Organization, Site, Equipment, Device or platform identifier occurred.
- Centrifugo recovered one publication emitted while the client was offline and retained two history publications.
- River committed one domain row and one unique job, rolled back both test rows/jobs in the rollback case and produced exactly one effect after worker restart.
- Runtime-generated test values were not written to evidence files.

## Adoption requirements

A slice-specific ADR is still required before any component enters a production dependency graph. That ADR must lock the exact version, license conclusion, retained responsibility boundary, security configuration, failure mode, observability, upgrade path and rollback procedure.

The S1 specification PR remains separate and unaccepted. These experiments do not start S1 implementation and do not authorize changes to IAM, Core, Gateway, Legacy or data ownership.
