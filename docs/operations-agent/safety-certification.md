# Operations Agent negative authorization and recovery certification

## Purpose

Map 5.5 provides one fail-closed certification command for the Operations Agent safety boundary. It
proves negative authorization, exact retry, restart, concurrency and stream-recovery behavior through
public, service, PostgreSQL, Audit Ledger and browser seams.

This is repository and local integration evidence. It does not enable production traffic, certify
capacity or replace a release-candidate rollout decision.

## Commands

```text
npm run operations-agent:safety-certification
npm run operations-agent:safety-certification:verify
```

The first command executes the real gates and writes evidence under:

```text
out/operations-agent-safety-certification/
  certification.json
  SHA256SUMS
  logs/
  supporting/
```

The verifier reads the directory offline, validates the versioned report and checks every declared
supporting artifact against `SHA256SUMS`.

## Versioned scenarios

The report version is `operations-agent-safety-certification/v1`. It contains five mandatory
scenarios:

1. `authorization-negative-complete-boundary`
   - wrong Organization or Site remains nondiscoverable;
   - wrong mTLS presenter, expired grant, stale policy and revoked grant fail closed;
   - expired delegation and Owner-grant Scope drift fail before Owner work.
2. `retry-exactly-once-durable-outcomes`
   - retryable Gateway interruption and duplicate AG-UI delivery do not duplicate durable records;
   - repeated Operator Input, business effect and Audit delivery reuse their exact identities.
3. `restart-authoritative-state-recovery`
   - Runtime Checkpoint recovery and Checkpoint loss rebuild from durable Investigation state;
   - rollback prevents partial pre-commit state;
   - post-commit restart and terminal reload preserve exactly one authoritative result.
4. `concurrency-single-writer-authority`
   - stale Revision, stale or expired Lease and repository CAS conflict are typed failures;
   - simultaneous Operator Input acceptance produces one commit, one conflict and an inert exact retry;
   - PostgreSQL budget operations remain serialized.
5. `stream-recovery-authoritative-rebuild`
   - valid positions replay only the missing committed suffix;
   - unknown, future, expired, out-of-range and partial-Tool positions return a complete authoritative
     snapshot;
   - the browser stores only an opaque `revision:sequence` cursor scoped to Organization, Site and
     Investigation, and removes it on terminal or protected-Site purge.

## Required invariant evidence

Every scenario records an explicit status and bounded evidence references for:

- tenant isolation;
- idempotency;
- restart safety;
- bounded failure outcomes.

An invariant may be `NOT_APPLICABLE` for one scenario, but every invariant must be proven by at least
one scenario in the aggregate report. Missing gates, missing test markers, missing browser assertions,
failed supporting evidence or any production-traffic claim makes the report fail.

## Gate ownership

The certification runs seven stable gates:

- Operations Gateway contract validation;
- verbose IAM, grant, Owner and Gateway authorization-negative Go tests;
- the complete Operations Agent service suite;
- Operations Agent PostgreSQL integration;
- Durable Audit Ledger PostgreSQL integration;
- Operations Workspace unit tests;
- the real-browser Operations reconnect audit.

The runner captures stdout and stderr for every gate rather than trusting only an exit code. Scenario
requirements reference fixed test or browser assertion names, so deleting one required negative case
fails report construction even when unrelated tests remain green.

## Browser recovery storage

`sessionStorage` contains only an opaque bounded recovery position. The storage key includes encoded
Organization, Site and Investigation identity. Invalid values are discarded. No snapshot, Evidence,
Finding, Tool payload, Operator note, authorization data or secret is stored. Terminal Investigations
clear their exact key. Route teardown, Site switch and logout purge all keys for the protected Site.

The browser cursor is a transport optimization only. Every reconnect is reauthorized and begins with
an authoritative committed snapshot. Browser storage never becomes a state owner.

## PostgreSQL evidence

`out/operations-agent/postgres-persistence.json` identifies Ticket #210 and records coverage for:

- Runtime Checkpoint recovery and independent disposal;
- typed business-record persistence and rollback;
- durable resource-budget state and concurrent serialization;
- Audit outbox exact delivery;
- Operator Input exact retry;
- Map 5.5 safety certification.

The certification copies this file and the browser evidence into its supporting directory and binds
both with SHA-256 checksums.
