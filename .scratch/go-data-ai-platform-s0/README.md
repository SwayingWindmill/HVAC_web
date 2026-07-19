# S0 implementation tracker

Formal spec: `spec.md`

## Dependency graph

```text
01 Contract-first Gateway bootstrap
└─ 02 Authenticated principal loop
   └─ 03 Durable Session → Audit
      └─ 04 Route/Data Ownership + Legacy proxy
         ├─ 05 End-to-end observability
         └─ 06 Reproducible delivery and supply chain
                  └──────────────┐
05 ──────────────────────────────┤
03 + 04 ─────────────────────────┤
                                 ↓
07 Security, tenant and failure gates
                                 ↓
08 S0 Release Evidence Bundle
```

## Frontier

Ticket 01 is the only initial frontier item and can start immediately. All other tickets remain `ready-for-agent` but must respect their declared blockers.

## Rules

- Work one frontier ticket at a time in a fresh implementation context.
- A completed ticket must remain buildable and demonstrable on its own.
- Do not add Organization, Telemetry, Command, Schedule or AI business scope to S0.
- NestJS remains Legacy Frozen and private behind Gateway.
- Do not mark S0 complete until ticket 08 publishes the Release Evidence Bundle.
