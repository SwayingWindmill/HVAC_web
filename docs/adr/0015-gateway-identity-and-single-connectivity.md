# ADR 0015 — Gateway identity is its certificate; one Connectivity serves every Gateway

Status: accepted

Date: 2026-10-02

Reference review: `docs/architecture/references/thingsboard-v4.4.md` §2, `openems-2026.9.0.md` §5, `myems-v6.9.0.md` §2

## Context

Device ingress ran one `iot-service` per Gateway: the connectivity store was pinned to one Tenant, the process to one integration instance, and the broker ACL file named Tenant and Site UUIDs per Gateway. Adding a second Site meant editing six files and restarting three processes, and Device identity lived in seven unsynchronised places. Topics carried Tenant, Site and Gateway, which the adapter then had to cross-check against configuration.

## Decision

- A Gateway's identity on the wire is its Gateway Credential: an X.509 certificate whose subject CN is the Gateway's Registry Device id. Tenant and Site are looked up from Registry; messages never carry them.
- Topics carry only the Gateway id: `hvac/v1/{gatewayId}/up/{telemetry|event|reply}` and `hvac/v1/{gatewayId}/down/command`.
- The broker stays Mosquitto with certificate authentication and a static pattern ACL (`pattern write hvac/v1/%u/up/#`, `pattern read hvac/v1/%u/down/#`). Adding a Gateway changes no broker configuration.
- One Connectivity process serves every Tenant and Gateway. It resolves the Gateway to its Tenant before any Tenant-scoped work.
- Connectivity holds the Gateway CA. An operator issues an Enrollment Code for a registered Gateway; the Gateway exchanges the code and a CSR once for a 90-day certificate and renews with its current certificate.
- Revocation is enforced by Connectivity: messages from a Gateway without an active credential are quarantined and no commands are sent to it. The broker carries no CRL.
- Removed with no replacement: integration instances and transport profiles, connectivity sessions, the command connector ownership lease, per-Gateway static bindings files and environment lists, and the edge fleet runtime (releases, OTA, snapshots). Edge configuration delivery is designed again with the edge runtime.

## Considered options

- **Database-driven broker auth (Mosquitto dynamic-security, go-auth plugin, EMQX webhooks).** Rejected: the certificate already proves identity and a pattern ACL already confines each Gateway to its own topics, so a plugin adds a moving part without adding a guarantee.
- **Embedded Go broker inside Connectivity.** Rejected for now: it would make authorization an in-process call, but trades a mature broker's session and persistence behaviour for our own.
- **Tenant and Site in the topic.** Rejected: the topic becomes a second, forgeable source of scope that has to be cross-checked, and moving a Gateway between Sites would change its topics.

## Consequences

- `contracts/mqtt/*`, the simulator and the telemetry source contract change together; `energy/v1` topics are removed, not kept alongside.
- Telemetry trusts Connectivity as its only MQTT source and receives the resolved Tenant, Site, Device and Point from it (Telemetry lives in its own database and cannot read Registry views, ADR 0016), so per-integration source bindings and Telemetry's Registry projections disappear.
- A stolen Gateway certificate can still hold a broker connection until it expires, but nothing it sends is accepted and it can only reach its own topics.
