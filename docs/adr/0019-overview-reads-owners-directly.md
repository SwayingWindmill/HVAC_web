# ADR 0019 — The overview reads each owner directly; there is no Site dashboard summary

Status: accepted (amends ADR 0013 §12)

Date: 2026-10-10

The Site overview reads every fact it shows from that fact's owner. The facts and their owners are:

| Fact | Owner |
| --- | --- |
| Live plant power, cooling and device state | Telemetry Observation Snapshots and their stream |
| Today's HVAC electricity, cooling and Plant Efficiency | `energy-series` |
| Open alarms | Alarm |
| Open work orders | Work Order |
| Equipment and device names | Registry |

The live and energy cards state their data time, and a stale or degraded live reading is dimmed and marked. The Site dashboard summary route (`GET /api/v1/sites/{siteId}/dashboard-summary` and its `/events` stream), its `presentationmodel` library and the `presentation-service` route owner are deleted.

We chose this because the summary carried only day electricity, the device population and an open-alarm count; cost, baseline savings, COP and current power were fixed as not integrated. Everything else on the overview would have needed copies of facts from more owners:

- live plant power and cooling;
- cooling energy and Plant Efficiency;
- running equipment;
- work orders.

Live state streams from Telemetry anyway, so a summary would still have needed a second channel. The facts also age at different rates: a live reading is seconds old, while projected energy lags by minutes. One combined watermark either hides that or marks the whole page partial. Freshness stated on the facts that age tells the operator which number is behind.

## Considered Options

- **Extend the summary to carry every overview fact.** Rejected: it copies five owners (Telemetry, energy, Alarm, Work Order, Registry) into one projection and still needs Telemetry's stream for live values.
- **ThingsBoard dashboards (adopted).** ThingsBoard binds each widget to its own datasources ([`widget.models.ts`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/ui-ngx/src/app/shared/models/widget.models.ts#L974), `WidgetConfig.datasources`, v4.4) rather than to one dashboard projection; the source review records it under S17 revision.

## Consequences

- The overview makes one request per owner and shares query keys with the alarm, work and device pages, so moving between them renders from the shared cache while it refreshes.
- ADR 0013 §12's other rules still hold: presentation owns layout and view state, never business truth.
- A group or multi-Site overview, or a BigScreen, may need one aggregated read. If one does, it gets a projection fed by owner events at that point (ADR 0016).
