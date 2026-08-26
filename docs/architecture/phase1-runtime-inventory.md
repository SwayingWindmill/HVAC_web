# Phase 1 Runtime Inventory

Status: CURRENT / MACHINE-ALIGNED  
Machine contract: `deploy/platform/phase1/runtime-inventory.v1.json`  
Canonical runtime: `deploy/platform/phase1/compose.yaml`

## 1. Purpose

Phase 1 has one supported deployment truth: `deploy/platform/phase1/compose.yaml` on one Linux server with Docker Compose. Historical S0/S2/S3/S4 deployment assets may remain as certification, migration, fixture or evidence inputs, but they are not alternate supported runtimes.

This document separates **business deployables**, **supporting workloads**, **infrastructure**, **optional capabilities** and **one-shot operations** so a source directory or historical `*-service` name cannot silently become a deployable architecture decision.

## 2. Canonical classification

| Class | Runtime items | Decision |
| --- | --- | --- |
| Default public | `nginx` | Always part of the default topology; serves React and terminates HTTPS/WSS. |
| Default business deployables | `energy-api`, `iot-service`, `telemetry-worker`, `metric-worker` | The four Phase 1 business deployables. |
| Default supporting workloads | `scheduler`, `maintenance` | Long-running application workers, but not new business-domain authorities. |
| Default identity infrastructure | `identity-service` | Authentication/OIDC infrastructure; platform IAM remains authorization authority. |
| Default data/realtime infrastructure | `postgres`, `clickhouse`, `redis`, `mqtt-broker`, `centrifugo` | Internal infrastructure; only MQTT TLS is host-published in addition to Nginx HTTPS. |
| Profiled observability infrastructure | `prometheus`, `node-exporter`, `grafana`, `otel-collector`, `loki`, `tempo` | Selected by deployment tier through one observability profile. |
| Optional intelligence | `forecast-service`, `optimization-service`, `fdd-service` | Enabled only through the `intelligence` profile; never gates the default telemetry/control path. |
| Required startup one-shot | `phase1-schema-preflight` | Runs before long-running application processes and exits. |
| Operator one-shot | migration, identity bootstrap/admin/reconciliation, PostgreSQL backup, ClickHouse backup | Explicit operator lifecycle actions; not long-running deployables. |

## 3. Why `maintenance` remains separate

`maintenance` executes `CERTIFICATE_EXPIRY_SCAN`, `DEAD_WORK_DISPOSITION` and `TENANT_RETIREMENT`. Those operations have cross-domain operational privileges and can touch IAM, Connectivity, Job and retirement state.

It therefore stays a separate **supporting worker** in Phase 1:

- merging it into `scheduler` would violate the rule that Scheduler owns timing/coordination while Domain Workers own execution;
- merging it into `energy-api` would widen the privilege and failure surface of the online API process;
- keeping it separate does **not** create a fifth business deployable because it owns no new product domain/API authority.

RC-03 should give it a canonical executable name such as `cmd/maintenance-worker` while preserving the current runtime behavior.

## 4. Why FDD remains optional

FDD already exists behind the same `intelligence` Compose profile as Forecast and Optimization. The previous backend architecture contract named only Forecast and Optimization, which was documentation/contract drift rather than a reason to delete FDD.

The canonical decision is:

```text
intelligence profile
├─ forecast-service
├─ optimization-service
└─ fdd-service
```

None of the three is required for Registry, Telemetry, Command safety, Alarm, Work Order or default Phase 1 startup.

## 5. One-shot operations are not services

The following may appear under `services:` in Compose because Compose is also used as an operational launcher, but they are not long-running runtime topology:

- `phase1-migrator`
- `phase1-schema-preflight`
- `identity-keygen`
- `identity-mfa-keygen`
- `iam-api-credential-keygen`
- `identity-admin`
- `identity-reconciler`
- `postgres-backup`
- `clickhouse-backup`

This distinction matters when RC-03/RC-04 reorganize source code: one-shot commands should remain explicit operator actions and must not be counted as independent product microservices.

## 6. Historical deployment assets

`deploy/platform/phase1/` is the only current Phase 1 deployment root.

The following roots must not define a second runtime truth:

- `deploy/s0/`
- `deploy/s2/`
- `deploy/s3/`
- `deploy/s4/`
- `infra/durability/`
- `infra/observability/`
- `infra/registry/`
- `infra/telemetry/`
- `infra/command/`
- `infra/alarm/`
- `infra/workorder/`

They may still supply migration SQL, configuration, certification fixtures or historical evidence until RC-06 moves live inputs into stable domain-oriented locations. Their existence is not permission to launch them as alternate production topologies.

## 7. RC-03 handoff

With runtime truth frozen, canonical executable entrypoints should next become:

```text
cmd/
  energy-api/
  iot-service/
  telemetry-worker/
  metric-worker/
  scheduler/
  maintenance-worker/
```

`identity-service` remains separate Identity Infrastructure. Forecast/Optimization/FDD remain deliberately independent optional intelligence workloads. Source modules can then move in RC-04 without changing the runtime inventory.
