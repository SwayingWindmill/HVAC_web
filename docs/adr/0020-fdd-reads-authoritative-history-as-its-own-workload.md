# ADR 0020 — FDD reads authoritative telemetry history as its own workload

Status: accepted

Date: 2026-10-12

FDD evaluates rules on a schedule as well as when an operator asks, so it reads the evidence it needs, the latest supply and return water temperatures of a loop's BTU meter, from the Telemetry history owner tables in ClickHouse with its own read-only account (`fdd_service_reader`, `SELECT` on `telemetry_history.observations` only), scoped in the query to the Tenant, Site and Device. Optimization already reads its current-state Telemetry this way (#345). Platform Gateway still decides whether a user may ask for an evaluation of a Site; the evaluation itself no longer carries that user's history delegation, and FDD no longer calls the Telemetry history HTTP owner. We chose this because the history owner accepts only Gateway-signed user delegations, so no unattended evaluation could ever run, and one evidence path for both triggers keeps a scheduled finding and an operator's finding comparable.

## Considered Options

- **Extend ADR 0017 to Telemetry history**: IAM signs history grants for Workload Principals and the history owner accepts them beside Gateway delegations. Rejected for now: it adds a second grant issuer and verification path to the history owner for a single reader whose scope is already one Site's two readings, and Optimization's equivalent read has not needed it.
- **Keep the delegated path for operator runs and add a direct read for scheduled runs.** Rejected: two evidence paths could disagree about the same loop at the same moment.

## Consequences

- `fdd_service_reader` can read every Tenant's observations; the query, not the database, scopes it. A compromised FDD service exposes observation history, which is why the account cannot write and sees no other table.
- A persisting condition is recorded once a day per asset and rule: an evaluation that finds an open finding from the last 24 hours reports the condition as ongoing instead of adding another.
