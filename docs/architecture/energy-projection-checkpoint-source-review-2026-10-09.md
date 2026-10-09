# Energy projection checkpoint source review — 2026-10-09

Scope: #441. Every poll of the energy projector found its work by aggregating all of `analytics.energy_interval_facts` and recomputing `telemetry_history.counter_deltas` window functions over all Counter history. On the local stack at 289k Counter observations and 289k facts the candidate query took 3.4 s and 333 MiB; the fact aggregation alone used 296 MiB, the delta view 108 MiB. ClickHouse is capped at 1.5 GiB, and about one poll in fifteen failed with `MEMORY_LIMIT_EXCEEDED`.

## Decisions agreed before implementation

1. Bound both sides, not only the fact aggregation.
2. Correct late arrivals however late: device reordering, a DEAD batch reconciled days later, Historical Replay.
3. Counter semantics stay with Telemetry: one formula, in a `SQL SECURITY DEFINER` parameterized view; the projector never reads raw observations.
4. Each Point's history before the window is summarized by an anchor (last observation) and per-revision maximum computed in the view. Memory is bounded by Points; time still scans that history once.
5. The cursor is the batch's History Sequence, written by Telemetry into every observation.
6. The checkpoint lives in ClickHouse next to the facts and advances after them.
7. Migrations are edited in place before go-live; the local stack is rebuilt.
8. Each poll projects every arrival's own delta and, for a late arrival, its successor's.

## Pinned sources

### ThingsBoard v4.4 (`6d46786579c8b29caf5102f95ddb133674bed68b`)

- [`schema-entities.sql`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/dao/src/main/resources/sql/schema-entities.sql) `edge_event`: `seq_id INT GENERATED ALWAYS AS IDENTITY`, then `SET CYCLE`; the table is partitioned by `created_time`.
- [`GeneralEdgeEventFetcher.java`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/main/java/org/thingsboard/server/service/edge/rpc/fetch/GeneralEdgeEventFetcher.java): pages events after `seqIdStart` ordered by `seqId`, and narrows partitions with `queueStartTs - misorderingCompensationMillis`; detects a new `seqId` cycle.
- [`PostgresGeneralEdgeEventsDispatcher.java`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/main/java/org/thingsboard/server/service/edge/rpc/processor/PostgresGeneralEdgeEventsDispatcher.java): persists `queueStartTs` and `queueStartSeqId` only in the success callback of a processed page.

**ADOPT:** a database sequence as the read order, and advancing the stored position only after the page is processed (at-least-once, idempotent consumer). **ADAPT:** the sequence is assigned per History batch at claim time, because batches reach ClickHouse one at a time; that makes it the visibility order without ThingsBoard's misordering compensation. The first observation's `event_id`, used as `batch_id`, was rejected as the cursor: a transaction committed late can put a smaller UUIDv7 into a later batch. **REJECT:** `SET CYCLE` and cycle detection; a `bigint NO CYCLE` sequence does not wrap.

### MyEMS v6.9.0 (`b360f5bb4c2be4fd15854057963531b2e8d1bc0a`)

- [`myems-normalization/meter.py`](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-normalization/meter.py): resumes each meter from `MAX(start_datetime_utc)` of its output, and takes the baseline from the latest raw value before that time (`ORDER BY utc_date_time DESC LIMIT 1`).

**ADAPT:** the baseline-before-window idea becomes the ANCHOR row of `counter_deltas_from`. **REJECT:** an event-time cursor; values arriving before it are never processed, which contradicts decision 2.

### ClickHouse v26.3.39.7 (`4277fab4705d62bdd5452b9a284dff140ef47e39`)

- [`docs/en/sql-reference/statements/create/view.md`](https://github.com/ClickHouse/ClickHouse/blob/4277fab4705d62bdd5452b9a284dff140ef47e39/docs/en/sql-reference/statements/create/view.md) "Parameterized View", and [`tests/queries/0_stateless/02428_parameterized_view.sh`](https://github.com/ClickHouse/ClickHouse/blob/4277fab4705d62bdd5452b9a284dff140ef47e39/tests/queries/0_stateless/02428_parameterized_view.sh).

**ADOPT:** parameterized views for the bounded reads. Checked on the local 26.3.39.7 server: they work with `SQL SECURITY DEFINER`; a reader granted only the view cannot read the base table; arguments can be HTTP query parameters, so no value is formatted into SQL text.

## Shape

```text
Telemetry History claim       nextval(telemetry_history_batch_sequence) -> observations.history_sequence
counter_observations          the one Counter filter
counter_deltas_from(points, since)
  RANGE rows                  sampled_at >= since
  ANCHOR row per Point        last observation before since (the first predecessor)
  PREFIX_MAX row per revision maximum before since (INVALID decreases only; never a predecessor)
counter_deltas                counter_deltas_from(points = [], since = epoch), unchanged for Metric/Forecast
counter_arrivals(after)       Counter observations after the cursor, minmax index on history_sequence

projector poll
  checkpoint  -> arrivals (<= batch)  -> deltas of arrivals and their successors
  -> facts (existing-fact lookup bounded by the arrivals' Tenants, Sites, Points, since)
  -> advance checkpoint
```

## Found while implementing

`Writer.AppendRebuildEvent` never set `date_time_input_format=best_effort`, so ClickHouse rejected every rebuild event: any late-arrival correction failed in production at its RUN_STARTED event. The new late-arrival integration test is the first to run that path against ClickHouse.
