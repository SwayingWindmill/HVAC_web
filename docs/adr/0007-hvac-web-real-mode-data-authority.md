# ADR 0007 — HVAC Web Real Mode data authority

## Status

Accepted.

## Context

HVAC Web contains a mixture of authoritative S1–S3 platform data, telemetry-derived values and deterministic demonstration fixtures. Several pages currently combine those sources without a sufficiently strong product boundary. Once a user selects Real Mode, silently substituting a fixture value for an unavailable backend capability can make fabricated operational state appear authoritative.

The platform already treats Registry, Telemetry and Command resources as owner-authored facts exposed through Platform Gateway contracts. Their clients fail closed rather than falling back to Legacy, ThingsBoard-direct or Mock data.

## Decision

HVAC Web has two explicitly separate operating modes:

- **Real Mode** displays only authoritative platform API data or values derived through an explicitly defined read model from authoritative inputs.
- **Demo Mode** may display deterministic fixture data, but it is visibly identified as non-authoritative.

Real Mode must never fall back to Mock, Legacy or fabricated values when an API is missing, unavailable, unauthorized or returns incomplete coverage. The affected page or section must instead expose an explicit unavailable, not-yet-integrated, unauthorized or degraded state.

A page must not mix Demo values into otherwise real operational totals, KPIs, statuses, timelines or actions. Actions performed in Demo Mode must not be represented as persisted platform state.

## Consequences

- Existing mixed pages require a section-by-section source audit before migration.
- Pages whose backend domain does not yet exist cannot be made “real” by replacing local state with ad hoc Gateway storage.
- Derived values must declare their source inputs, time range, Site timezone, freshness and partial-coverage behavior.
- Demo Mode remains useful for product demonstrations but must be a separate, visibly non-authoritative experience.
- Real Mode integration tests must fail if fixture modules or Mock stores contribute to rendered business facts.
