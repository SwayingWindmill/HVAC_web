# ADR 0016 — Owners publish read views; consumers keep no copies of owner master data

Status: accepted

Date: 2026-10-02

While every context shares one PostgreSQL database, an owner exposes the master data other contexts need as versioned, read-only SQL views in its own schema (for example Registry's Gateway directory, Device Source Keys and Point bindings), granted to the consuming roles. Consumers query those views instead of keeping projection tables. We chose this because the projections it replaces (`telemetry_runtime.registry_device_bindings`, `registry_point_bindings`, `iam_scope_projections`) had no projector and were filled only by seed scripts, so they silently drifted from Registry; a view is always current and needs no synchronisation code. This is the "call the owner's port" option of ADR 0013 §2: the view is the port, and the owner may change its tables freely as long as the view keeps its contract. When a consumer moves to its own database, that consumer replaces the view with a projection fed by owner events.
