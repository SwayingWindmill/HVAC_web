# S1 sqlc versus direct pgx POC

This disposable module evaluates `sqlc v1.31.1` (MIT) against the accepted S1 Registry list/detail SQL. It is intentionally outside `go.work` and does not add a production dependency.

## Evidence

- Generates typed `pgx/v5` queries for Organization, Site, Equipment and Device reads.
- Keeps tenant/application Scope predicates explicit in every query.
- Uses deterministic keyset ordering by `display_name COLLATE "C", id`.
- Re-generating the package produces the same digest.
- The generated package compiles against the repository's production `pgx/v5 v5.9.2` line.

Run:

```bash
npm run s1:sqlc:poc
```

## Decision

Do **not** adopt sqlc in S1 Ticket 01. The generated query structs are useful, but S1 must also set transaction-local RLS Scope, preserve explicit transaction boundaries and keep the first read slice small. Adding a second production generation pipeline now costs more than the narrow direct-`pgx` query layer it would replace.

The POC remains checked in as reproducible evidence. A later ADR may reconsider sqlc when the Core query set becomes large enough to offset the generator and upgrade surface.
