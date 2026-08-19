# ThingsBoard Pattern Adoption Boundary

This project does not reintroduce ThingsBoard as a runtime dependency. The useful ThingsBoard patterns are adopted only when they fit the existing Tenant → Site → Space → Asset → Device → Point model and the current PostgreSQL/ClickHouse/MQTT ownership boundaries.

## Adopted in this increment

| ThingsBoard pattern | Current implementation | Boundary |
| --- | --- | --- |
| Message/Rule Node separates evaluation from side effects | `libs/alarmmodel/rule.go` evaluates a `SIMPLE_THRESHOLD` node and returns `MATCHED`, `NOT_MATCHED`, or `INDETERMINATE` | No arbitrary script, SQL, or network side effect is allowed in a Rule Node |
| Alarm creation carries source and evidence | `RuleNode.BuildPublication` produces a scoped `AlarmPublication` with Point evidence and policy revision; `PublicationStore.Publish` writes the provenance event and current projection in one Tenant-scoped transaction | The publication port is internal; an authenticated durable consumer still has to own delivery from the existing domain event stream |
| Alarm occurrences are coalesced and closed alarms can reopen | `PublishAlarm` increments occurrence evidence idempotently and uses the existing Alarm lifecycle transition for reopen | A non-GOOD or unknown-quality observation cannot clear or open an Alarm |
| Transport owns delivery mechanics, not business truth | Native MQTT command/telemetry adapters own QoS, fences, deduplication and reply validation | MQTT is not the Alarm or Command authority |

## Intentionally not adopted

- Things/Device Profile aliases as a second domain identity. `Point` remains the canonical telemetry and control identity.
- Read-through or fallback reads from ThingsBoard. Current-state and history ownership remain explicit in S2.
- A general-purpose chain that can invoke arbitrary providers. Command capabilities remain allowlisted, snapshotted, fenced and independently verified.
- Generic UI dashboard/widget configuration as a business data authority. The Web app owns presentation; backend Domain Read Models own energy, telemetry, alarm, availability and command facts. If tenant-authored presentation is later justified, it may use a versioned, declarative, first-party-only view definition under the constraints in `thingsboard-dashboard-widget-mobile-adjudication.md`, but it must not admit arbitrary script, external resources or direct RPC.

## Remaining gap before production rule-driven Alarms

The current code has a deterministic Rule Node, Alarm aggregate semantics, and an atomic publication Store boundary, but it does not yet claim a production Rule Engine. The remaining work is deliberately larger than this increment:

1. Persist versioned Rule Definitions and their Tenant/Site/Point bindings.
2. Consume the existing `alarm-rule` domain delivery with a durable lease/retry boundary.
3. Connect the existing `alarm-rule` delivery to `PublicationStore.Publish` through an authenticated internal workload boundary; the transaction and event idempotency are now implemented, but the consumer is not.
4. Add explicit clear/debounce/escalation nodes and tests for missing, stale, out-of-order and recovered telemetry.
5. Add rollout evidence before enabling rule-generated Alarm traffic.

Until those pieces exist, `libs/alarmmodel/rule.go` is a safe domain foundation, not a claim that the project has a ThingsBoard-equivalent Rule Engine.
