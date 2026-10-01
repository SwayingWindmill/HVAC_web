# Observability Phase 1 Runbook

## Scope

This phase operationalizes the currently implemented MQTT, S2 Telemetry, Command, and Alarm paths. It intentionally does not invent metrics for capabilities that do not yet exist in the runtime, including Modbus protocol polling, Gateway CPU/memory health, and sample-completeness based on expected Point cadence.

Operational metrics must remain low-cardinality. Tenant, Organization, Site, Gateway, Device, Point, Command, Alarm, request, trace, and message identifiers belong in controlled logs, traces, audit evidence, or database queries rather than Prometheus labels.

## Signal map

```text
EG8200 / Edge
  -> local Store & Forward / broker connection: hvac_edge_mqtt_*
  -> Cloud MQTT adapter: hvac_mqtt_*
  -> S2 ingest: hvac_s2_source_lag_seconds
  -> S2 quality: hvac_s2_data_quality_records_total
  -> S2 realtime: hvac_s2_outbox_* / hvac_s2_publication_*

Command API
  -> hvac_command_http_*
  -> Dispatch: hvac_command_dispatch_results_total
  -> Verification: hvac_command_verifications_total
  -> Verification latency: hvac_command_verification_duration_seconds

Alarm API
  -> hvac_alarm_http_*
```

## Edge MQTT disconnected

1. Check `hvac_edge_mqtt_connected` and connection outcome counters.
2. Check the local Store & Forward queue before restarting the publisher.
3. Verify broker reachability, client certificate validity, and the Gateway-scoped ACL.
4. A Cloud-side MQTT adapter being healthy does not prove the Edge publisher is connected.
5. Do not disable mTLS, QoS 1, or persistent session semantics as a recovery shortcut.

## Edge Store and Forward queue high

1. Check `hvac_edge_mqtt_queue_bytes`, `hvac_edge_mqtt_queue_capacity_bytes`, and `hvac_edge_mqtt_queue_utilization_ratio`.
2. If utilization is rising while the Edge is disconnected, restore network/Broker connectivity first.
3. If the Edge is connected but utilization still rises, check Cloud adapter queue pressure and S2 source lag.
4. `hvac_edge_mqtt_queue_limit_rejections_total > 0` means the configured durability envelope has been exhausted and data is at risk; treat it as an operational page.
5. Queue-capacity increases require capacity evidence and disk-headroom review; they are not a substitute for fixing a blocked delivery path.

## MQTT disconnected

1. Check `hvac_mqtt_connected` and `hvac_mqtt_subscribed`.
2. Check `hvac_mqtt_connections_total{outcome="failed"}` and `hvac_mqtt_disconnections_total`.
3. Verify Mosquitto health, mTLS certificate validity, and ACL configuration.
4. Check the EG8200 persistent publish queue before restarting any component.
5. Do not bypass mTLS or broaden Topic ACLs as a recovery action.

## MQTT queue high

1. Compare `hvac_mqtt_processing_queue_depth` with `hvac_mqtt_processing_queue_capacity`.
2. Check `hvac_mqtt_message_retries_total` for downstream S2 pressure.
3. Check S2 `dependency="mqtt"` source lag and PostgreSQL dependency metrics.
4. If queue growth is caused by downstream latency, restore the dependency before increasing queue capacity.
5. Capacity changes require a new sustained-load verification; they are not an emergency substitute for a blocked consumer.

## MQTT dropped

`outcome="dropped"` is reserved for permanent poison messages such as malformed or unauthorized envelopes. Inspect structured MQTT adapter logs for the bounded error reason and correlate using controlled evidence. Never log full high-frequency telemetry payloads as a normal diagnostic strategy.

## MQTT downstream retry

1. Check S2 availability and source acceptance latency.
2. Check PostgreSQL and history/outbox dependencies.
3. Confirm retries recover without MQTT queue saturation.
4. The adapter intentionally leaves transient deliveries unacknowledged while retrying; do not disable QoS 1 or manual acknowledgement to reduce the alert.

## Data quality

`hvac_s2_data_quality_records_total` records bounded S2 quality and acceptance outcomes. The current phase can report GOOD/SUSPECT/REJECTED rates. Data completeness and sampling-compliance are not claimed yet because expected sampling cadence is not projected into S2 runtime facts.

When good rate falls:

1. Split by quality/outcome, not by Device or Point labels.
2. Check S2 source lag and quarantine rate.
3. Use Audit/controlled database evidence to identify affected Site/Device/Point identities.
4. Check mapping and Point binding revision before treating the issue as transport loss.

## Command verification failure

1. Inspect `hvac_command_verifications_total` by outcome.
2. Distinguish `mismatch`, `timed_out`, and `inconclusive` from API failures.
3. Inspect Command Audit and connector evidence by `command_id` in controlled stores; do not add `command_id` to Prometheus.
4. Follow the chain: authorization -> dispatch -> connector evidence -> device acknowledgement -> reported-state readback -> verification.

## Command verification timeout

1. Check MQTT/connector/device availability.
2. Check reported-state freshness and S2 snapshot age.
3. Compare `hvac_command_verification_duration_seconds` P95/P99 with the command verification deadline policy.
4. A timeout is a control outcome, not merely an HTTP 5xx.

## Command API 5xx

1. Split `hvac_command_http_requests_total` by bounded route.
2. Check PostgreSQL, IAM, and S2 dependencies according to the affected operation.
3. Use W3C trace correlation for cross-service investigation.
4. Do not treat expected 4xx authorization/risk rejection as service failure.

## Alarm API 5xx

1. Split by bounded Alarm route.
2. Check Alarm PostgreSQL access and response-validation errors.
3. Use trace correlation to Platform Gateway and Audit where applicable.

## Alarm mutation rejections

A high 4xx rate can be either user/scope misuse or a policy/client mismatch. Inspect operation distribution (`acknowledge`, `assign`, `suppress`, `close`, etc.) and authorization/audit evidence. Do not page the database team solely for 4xx rejection growth.

## Host disk growth and PostgreSQL WAL

1. Treat `Phase1HostDiskUsageWarning` and `Phase1HostDiskUsageCritical` as capacity incidents, not as reasons to delete database files manually.
2. `Phase1HostDiskWillFillWithin24Hours` is an early warning based on the recent free-space trend. Investigate the fastest-growing durable store before the static 80% threshold is reached.
3. For local PostgreSQL, compare the PostgreSQL data-directory size with `pg_wal`. A large `pg_wal` relative to relation data is a strong signal that WAL recycling is blocked.
4. If `POSTGRES_ARCHIVE_MODE=on`, inspect `pg_stat_archiver` and the archive destination. Repeated archive failures must be fixed at the archive destination; never delete files from `pg_wal` by hand.
5. Development and testing keep PostgreSQL WAL archiving disabled by default. Staging/production enable it only with writable, capacity-managed storage independent from the PostgreSQL data volume.

## Retention and cardinality

The Phase 1 metric catalog is `deploy/observability/phase1-metric-catalog.v1.json`. New labels must be bounded before merge. Raw IDs remain forbidden in Prometheus metrics. High-frequency telemetry payloads are not logged by default.
