# HVAC Web Real Mode Shell Certification

## Purpose

RMS-08 provides one repeatable certification gate for the HVAC Web Real Mode Shell. The gate proves contract identity, generated-client drift protection, relevant IAM and Gateway behavior, Real/Demo build isolation, TypeScript correctness, Site-scoped lifecycle behavior, browser security properties, failure handling, and mobile accessibility.

The generated machine-readable report is written to `out/rms-web-certification/real-shell-certification.json`. Supporting evidence is written beside it and is covered by `out/rms-web-certification/SHA256SUMS`.

## Certification command

Run:

```sh
npm run rms:certify
```

The command records each gate result and fails closed. A final certification report is produced only after all required gates pass.

## Evidence scope

The browser evidence covers login, one/many/zero authorized Sites, invalid or invisible Site URLs, capability denial, Not Integrated routes, protected Site switching, Session expiration, logout, and 390 px mobile states. It records:

- zero browser-supplied Authorization, Organization, Site, role, admin, or scope authority headers;
- zero persisted token, CSRF, Principal, Registry, Telemetry, or Command payloads;
- failure Problem codes and trace IDs with `fixtureFallback: false`;
- zero forbidden Demo/Mock imports and emitted symbols in the Real graph and bundle.

## Rollback

A Real deployment rollback may select only a previously accepted **Real artifact**. It must not route production users to Demo Mode, load Demo chunks, or restore fixture data as a fallback. Session, capability, Site-discovery, and protected-scope failures continue to fail closed during rollback.

The authoritative machine policy is `deploy/rms/real-shell-release-policy.v1.json`.

## Explicit non-claim

RMS-08 certifies only the Real Mode Shell. It does **not** enable, increase, or otherwise change S3 Command production traffic. It does **not** change any formal S3 certification claim. Existing S3 traffic controls, rollout cohorts, and certification evidence remain authoritative and unchanged.
