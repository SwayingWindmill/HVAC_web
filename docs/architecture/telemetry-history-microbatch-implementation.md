# Cloud history microbatch implementation

Scope: PostgreSQL history outbox → ClickHouse observations → numeric hourly aggregate. Edge is excluded. This implements the first cloud data-path improvement from the centralized energy architecture review; it is not a claim that the entire platform is production-ready for hundreds of sites.

## Delivered behavior

- One persistent unresolved batch per outbox. Claim transactions use the existing PostgreSQL advisory-lock pattern; only actual selected members determine batch identity. New arrivals cannot change an existing batch.
- The first claim also takes the batch's History Sequence from a `NO CYCLE` PostgreSQL sequence, and every row of the batch is written with it as `history_sequence`. A retry keeps it, so the request body and its deduplication token are unchanged. Because batches reach ClickHouse one at a time, the sequence is visibility order; the energy projection uses it as its checkpoint (#441).
- First claim caps both rows (configured, maximum 4,096) and exact encoded JSONEachRow bytes (8 MiB). A retry keeps the entire persisted batch even if the configured row limit shrinks.
- One synchronous HTTP INSERT per batch. Observations are sorted by identity and the encoded body SHA-256 is the deduplication token. Parser/block settings are explicit. The writer propagates materialized-view errors and enables dependent-view deduplication.
- PostgreSQL is acknowledged only after synchronous INSERT succeeds. Retry delay is measured after the failed attempt; an expired attempt still counts toward the attempt limit even if its process could not persist a retry.
- A DEAD batch blocks further projection. New telemetry can still enter the authoritative PostgreSQL outbox. DEAD requires reconciliation of both raw and aggregate destinations; automatically clearing the batch or replaying its rows with new tokens is forbidden.
- Both executable entry points use the same loop: drain immediately after successful work; wait only when idle or after a failure. A pass deadline is at most half its lease and at most 15 seconds; the default HTTP client timeout is 10 seconds.

## Validation and interpretation

The existing `s2:history:integration` task owns real PostgreSQL/ClickHouse fault checks. It uses isolated containers and disposable test volumes, and rejects skipped tests. It covers success followed by missing PostgreSQL acknowledgement, concurrent claimers, delayed retry, changed row limit, expired lease, stale acknowledgement, exhaustion without a Retry write, mixed partitions, a partially committed materialized-view failure, and overlapping identical retries.

Focused Go tests cover stable request bytes/token despite changed input order, entire-batch validation before I/O, byte limits, and immediate draining. Telemetry module and worker builds/tests are also run. The source review lists the exact upstream files and decisions: [ClickHouse source review](telemetry-history-microbatch-source-review.md).

The controlled transport benchmark uses 256 observations and a local HTTP stub with a 1 ms per-request delay. Before this change it measured 256 requests/batch and approximately 28.17 ms/batch; the first microbatch run measured 1 request/batch and approximately 3.20 ms/batch. These numbers demonstrate transport-overhead reduction, **not** ClickHouse capacity or an end-to-end station throughput guarantee. Site sizing still needs actual point counts, sampling periods and production-like load evidence.

## Deployment and recovery boundary

`batch_id` is part of the canonical outbox schema in `004-s2-telemetry-history-outbox.sql`; the temporary `006` compatibility path was removed. This is a schema-baseline change tested with freshly initialized databases. Existing initialized databases do not acquire the column from `CREATE TABLE IF NOT EXISTS`, and the checksum-based migrator will reject a changed previously applied file. This change does not silently upgrade or erase an existing deployment. A live database transition requires a separately reviewed change window; do not run the new writer against the old schema or re-batch rows already sent by the old per-row writer.

Native ClickHouse deduplication has a finite window. Default raw and hourly tables retain 100,000 IDs. The single unresolved batch prevents normal new traffic from evicting its IDs before acknowledgement. It does not provide unbounded exactly-once delivery or server-side fencing of an arbitrarily delayed HTTP request. Extra writers, table recreation, partition removal, loss of deduplication logs or changed block settings invalidate the retry contract. An uncertain old request must be stopped/reconciled before manual recovery or maintenance resumes new work; checking raw rows alone does not prove aggregate completeness.

The delayed-old-request limitation also applies to automatic recovery: if an old request completes only after its successful retry and enough subsequent batches to evict its token, duplication is still possible. The tests cover overlapping retries within the retained deduplication window, not arbitrarily delayed writes beyond it. Client deadlines and ClickHouse's cooperative execution-time checks do not provide server-side fencing. Manual recovery must pause publishers, confirm relevant server executions have ended, and reconcile both destinations before resuming.

Run results are written by the existing task to `out/s2-history/clickhouse-integration.json`. No additional permanent CI gate or package dependency was introduced.

## Verification record

- 2026-09-08, 03:45 UTC: the real PostgreSQL/ClickHouse task passed all three tests (`TestPostgresOutboxProjectsClickHouseHistoryDeduplicatesRetry`, `TestHistoryMicrobatchLeaseRetryAndDeadBoundary`, `TestHistoryMicrobatchMixedPartitionsRecoversMaterializedView`). Independent assertions were outbox `PUBLISHED|2|true`, raw `1|1|24.75`, and hourly `1|24.75`.
- 2026-09-09: the full telemetry module plus combined worker tests/builds passed with `GOMAXPROCS=2` and `-p=1`; the existing history architecture check passed. The two review axes confirmed their reported batch-identity, index and retry-exhaustion defects were fixed. The finite-window limitation remains explicit above.
- The September 9 database rerun could not start: Docker Desktop failed to initialize its inference-manager listener because its local `dockerInference` socket could not be accessed. The latest JSON report therefore records an environment failure and does not replace the prior successful fault-test result. No Docker reset or existing database replacement was performed.
