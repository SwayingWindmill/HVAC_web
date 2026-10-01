# Telemetry history microbatch source review

Date: 2026-09-07. Scope: cloud PostgreSQL history outbox → ClickHouse synchronous insert and dependent hourly materialized view. Edge is excluded. This is a source review and implementation decision record; it is not a load-test or production certification result.

## Fixed upstream

Official repository: https://github.com/ClickHouse/ClickHouse

- Release/tag: `v26.3.12.3-lts`, matching the canonical `clickhouse/clickhouse-server:26.3.12.3` image.
- Annotated tag object: `f118ee7c3b4c1a57dde6a389e5c3e29080f38c5d`.
- Peeled source commit: **`d23c7536b980c34b39c850b08ef23c509f06aaaa`**.
- Verified with official `git ls-remote` and a shallow, filtered checkout under OS temporary storage. No upstream code was copied into product code.

All source/test paths below refer to that immutable commit. Current documentation is explanatory evidence only: its defaults and setting names can evolve, so pinned code and tests own version-specific conclusions.

## Source and tests actually read

| Official file | Observed behavior and decision |
| --- | --- |
| [MergeTreeSink.cpp](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/src/Storages/MergeTree/MergeTreeSink.cpp) | `finishDelayedChunk` commits partition parts independently; `commitPart` checks/records dedup IDs under the parts lock. Conflicting IDs filter matching data. ADOPT native insert deduplication; REJECT treating an entire HTTP request and all MV destinations as one transaction. |
| [MergeTreeDeduplicationLog.cpp](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/src/Storages/MergeTree/MergeTreeDeduplicationLog.cpp) | A bounded map/log stores block IDs. Zero disables it; resize/eviction and partition removal affect remembered IDs. ADAPT application scheduling so unresolved inserts cannot be overtaken by unlimited new inserts. |
| [MergeTreeSettings.cpp](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/src/Storages/MergeTree/MergeTreeSettings.cpp) | `non_replicated_deduplication_window` defaults to zero and represents retained recent IDs, not a wall-clock retry TTL. REJECT claiming a retry is safe merely because it is younger than a chosen duration. |
| [InsertDeduplication.cpp](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/src/Interpreters/InsertDeduplication.cpp) | User tokens take precedence over data hashes; source/view block metadata and partition identity contribute to deduplication. The code supports old/unified/double hash stages. ADAPT stable payload, order and block settings; size window with ample headroom for partitions and hash stages. |
| [DeduplicationTokenTransforms.cpp](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/src/Processors/Transforms/DeduplicationTokenTransforms.cpp) | Source and view block numbers are appended as chunks traverse the pipeline. REJECT changing block boundaries between attempts of a persisted batch. |
| [AsynchronousInsertQueue.cpp](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/src/Interpreters/AsynchronousInsertQueue.cpp) | Queue grouping excludes the token setting, while parsed entries preserve each request's token and row extent. REJECT async per-row requests for this change: server batching may reduce parts but retains one client request per observation. |
| [01781_merge_tree_deduplication.sql](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/tests/queries/0_stateless/01781_merge_tree_deduplication.sql) | Tests small-window eviction, detach/attach, disabling/resizing deduplication and partition removal. ADOPT explicit finite-window and maintenance boundaries. |
| [02124_insert_deduplication_token.sql](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/tests/queries/0_stateless/02124_insert_deduplication_token.sql) | Different data with the same user token is deduplicated. REJECT reusing batch token with different membership or payload. |
| [02124_insert_deduplication_token_multiple_blocks.sh](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/tests/queries/0_stateless/02124_insert_deduplication_token_multiple_blocks.sh) | HTTP test repeats a two-block insert and later appends rows under the same token: earlier blocks deduplicate while new block numbers insert. ADOPT deterministic block settings; stable token alone is insufficient. |
| [02124_insert_deduplication_token_materialized_views.sql](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/tests/queries/0_stateless/02124_insert_deduplication_token_materialized_views.sql) | Injects an MV failure using a partition limit; explicitly enabled dependent-MV deduplication allows retrying all destinations without overcounting successful ones. ADOPT explicit MV deduplication and positive target-table windows. This particular test uses replicated tables. |
| [03008_deduplication_mv_generates_several_blocks_nonreplicated.sh](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/tests/queries/0_stateless/03008_deduplication_mv_generates_several_blocks_nonreplicated.sh) and [03008_deduplication.python](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/tests/queries/0_stateless/03008_deduplication.python) | Nonreplicated MergeTree matrix uses source/destination deduplication independently; generated `throwIf` assertions distinguish repeated source and MV row counts. ADOPT separate destination-table protection, with focused local integration tests rather than copying the matrix. |
| [03717_async_deduplication_with_mv.sql](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/tests/queries/0_stateless/03717_async_deduplication_with_mv.sql) | Tests overlapping asynchronous request sets and count MVs. It proves asynchronous deduplication is a real upstream capability, but does not remove the client request count or bounded-log limitation. |

Also inspected `03008_deduplication_wrong_mv.sql`; its schema-change scenario does not prove retry recovery and is not used as the authority for that claim.

Official documentation read: [deduplicating insert retries](https://clickhouse.com/docs/concepts/features/operations/insert/deduplicating-inserts-on-retries) and [asynchronous inserts](https://clickhouse.com/docs/concepts/features/operations/insert/asyncinserts). Their documented uncertain outcomes and bounded deduplication align with the pinned sources. Do not copy current documentation's evolving defaults into this version without checking them.

## Local comparison and selected ADAPT

The incumbent path claims arbitrary currently available outbox rows under a fresh lease and sends synchronous per-observation INSERTs using observation identity. Replacing that loop with a hash of each newly claimed set is unsafe: lease expiry, new arrivals or changed limits can reshape a retry after some original data reached ClickHouse. Plain MergeTree and incremental `count`/`avg` states do not remove such duplicates during merges.

The implementation owner selected a **single unresolved persisted batch channel** as the smallest bounded solution:

1. Serialize batch selection with the existing PostgreSQL transaction advisory-lock pattern.
2. Atomically assign immutable `batch_id` to newly selected rows. Do not form a second batch while one remains unconfirmed. A lease identifies an attempt, not batch membership.
3. After expiry, reclaim the entire same batch; configuration changes must not split it. Sort by stable event identity and send identical deterministic payload. Use its SHA-256 digest as the insert token. Payload changes under an old batch are an error, not an implicit repair.
4. Bound the batch to at most 4,096 rows, also cap bytes, and keep parser/block settings deterministic and parsing single-threaded. Use synchronous INSERT explicitly. The selected window of 100,000 IDs on both raw and hourly destination tables provides headroom for this single bounded group, including partitions and hash-stage expansion; do not generalize this to arbitrary extra MVs or writers.
5. Enable dependent-MV deduplication explicitly and propagate MV errors. Do not mark PostgreSQL rows published until the whole synchronous request succeeds. A failure may leave data in some partitions/destinations, so retry the same complete batch.
6. Keep the channel blocked while retries remain unresolved. Exhaustion becomes DEAD/manual reconciliation, not automatic release followed by blind replay. This deliberately trades availability during an uncertain write for preservation of meter/history aggregate correctness.

This is an HVAC/domain adaptation rather than upstream queue source copying: PostgreSQL is already the authoritative outbox; no new broker, native client or external batch ledger is required. The persistence and delivery tests must prove the single-channel invariant across multiple worker processes, rather than relying on the current replica count.

## Limits that remain part of the contract

Supplemental timeout review at the same pinned commit: `src/Server/HTTPHandler.cpp` only installs the client-disconnect cancellation callback for `readonly > 0`; `cancel_http_readonly_queries_on_client_close` does not fence INSERT. `src/Interpreters/executeQuery.cpp`, `src/Processors/Executors/PipelineExecutor.cpp`, and `src/Core/Settings.cpp` show that INSERT can use cooperative execution-time checks, but checks happen at selected processing points and actual execution can exceed the configured limit. REJECT treating `max_execution_time` or a disconnected HTTP client as proof that an old write can no longer arrive or complete. No timeout setting is added as a substitute for fencing.

- This is **not unbounded exactly-once delivery**. Native deduplication memory is finite. Table recreation, dedup-log loss/disablement, partition removal, incompatible token/block settings, or an additional writer bypassing the channel invalidate the retry proof.
- A lease is not fencing at the HTTP server. A stalled attempt may complete after another lease is acquired. Both attempts must retain identical membership/token/settings; native table dedup handles their collision while IDs remain remembered. Do not let a stale attempt publish or release a different lease.
- When a timed-out request might still execute, no unknown old request may be blindly replayed after the system has resumed enough new work to evict its IDs. Server request duration/cancellation assumptions and operational reconciliation must be explicit. A client timeout alone does not prove that server execution stopped.
- Manual recovery from DEAD requires checking both raw observations and aggregate destinations. Merely finding raw rows is insufficient because a materialized view may have failed. Do not reactivate DEAD automatically or compute a new token for its old observations.
- An unordered raw existence query followed by INSERT is not a substitute for deduplication: it races with an earlier still-running request and does not establish MV completeness.
- ReplacingMergeTree alone would not repair already-added `count`/`avg` MV states. Changing the entire history read/aggregate model is a separate decision, not a prerequisite hidden inside this performance change.
- Before acceptance, execute focused real ClickHouse tests for repeated mixed-partition batches, success followed by failed PostgreSQL acknowledgement, overlapping attempts, and an MV partial failure. Tests with an HTTP stub can prove request reduction and stable payload, but cannot establish database/MV recovery behavior.

Energy-platform references remain covered by the existing [centralized energy source review](centralized-energy-source-review-2026-09-07.md); this supplemental record settles the ClickHouse mechanism previously marked unverified. It does not claim new ThingsBoard/OpenEMS/MyEMS source inspection by this bounded research task.
