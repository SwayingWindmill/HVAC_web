# S4 Alarm read canary promotion

## Purpose

This gate controls the only reviewed adjacent promotion from `S4-R1-internal-read-only` at 1% to `S4-R2-site-canary` at 5% for the Alarm list and detail routes. Certification creates evidence only. It never edits the Route Ownership Registry or changes traffic by itself.

## Repository preflight

Run:

```text
npm run s4:alarm:promotion:preflight
npm run s4:alarm:promotion:test
```

Preflight must prove:

- list and detail remain one `s4-alarm-read-v1` cohort at 1%;
- both routes remain Alarm-owned, Gateway-ingressed and fallback-free;
- all seven public lifecycle POST routes remain disabled at `S4-R0-contract-only`;
- the target is exactly `S4-R2-site-canary` at 5%, with adjacent route revisions;
- no preflight result claims formal eligibility.

## Formal attestation

The target environment supplies a JSON attestation bound to the exact repository SHA and workflow run. Run:

```text
node scripts/run-s4-alarm-read-promotion-certification.mjs --profile=formal --attestation=<attestation.json> --output-dir=<evidence-directory>
node scripts/verify-s4-alarm-read-promotion-certification.mjs --directory=<evidence-directory>
```

The formal gate requires:

- at least 24 hours at the reviewed 1% source phase;
- at least two Organizations, three Sites, 1,000 list reads and 200 detail reads;
- availability of at least 99.9%, p95 no greater than 500 ms, p99 no greater than 1,500 ms and 5xx rate no greater than 0.1%;
- zero cross-scope responses, authorization mismatches, stale-policy accepts, response-scope mismatches, fallback selections, local-seam reads, Demo contamination and unaudited route decisions;
- one adjacent route plan promoting list and detail together to revision 3 and 5%;
- a rollback drill restoring 1% with revision 4, a decision within five minutes and route rollback within fifteen minutes;
- two distinct manual approvers after source hold completion and rollback.

Synthetic, incomplete, SHA-mismatched or tampered evidence fails closed. The committed fixture is marked `testFixture: true`; normal formal runs and offline verification reject it, and test-only verification never marks its bundle as promotion-eligible.

## Evidence bundle

The formal runner produces the files declared by `deploy/s4/alarm-read-promotion-envelope.v1.json`, including source, SLO, security, route plan, rollback and approval reports, an eligibility attestation, an in-toto statement and `SHA256SUMS`. The offline verifier rejects missing, unexpected or digest-mismatched evidence.

## Promotion and rollback

Accepted formal evidence authorizes a separate reviewed registry commit. That commit may advance both GET routes together to `S4-R2-site-canary`, 5%, revision 3. It must not enable lifecycle POST routes or add a fallback owner.

Rollback is a new registry revision. It restores both GET routes together to `S4-R1-internal-read-only`, 1%, revision 4. Completed responses remain historical facts; rollback changes only future route decisions.

## Explicit exclusions

This gate does not authorize lifecycle writes, suppression expiry, notifications, correlation policy, Work Order linkage or any alternate Alarm owner.
