# S2 Centrifugo transport experiment

This is a **throwaway integration experiment** for Issue #49. It validates whether Centrifugo `v6.8.1` can remain a transport component while the platform owner keeps every S2 business responsibility.

It is not a production service, not a durable telemetry owner and **not a production scale certification**.

## Question answered

Can the platform use Centrifugo transport for browser connections, multiplexed subscriptions, short-window recovery and slow-consumer protection without delegating Organization/Site/Device authorization, Snapshot authority, business Revision or fallback decisions?

The experiment's answer is **yes, with bounded responsibility**.

## Responsibility boundary

The platform owner fixture controls:

- authenticated principal and exact channel Scope authorization;
- live permission revocation and audit evidence;
- authoritative Snapshot state;
- monotonic business Revision and duplicate/gap rules;
- the decision to accept transport recovery or fetch a fresh Snapshot.

Centrifugo transport controls:

- browser WebSocket connections;
- channel multiplexing;
- short-window epoch/offset history recovery;
- a bounded per-client queue;
- transport-level Prometheus metrics.

Centrifugo is never queried as the source of current Presence or telemetry truth. Channel names are internal routing identifiers and contain platform IDs only, never ThingsBoard IDs.

## Scenarios executed

The runner proves:

1. a forged connection credential is rejected;
2. the platform subscribe proxy permits an exact Organization/Site/Device channel and rejects a cross-Site channel;
3. the client subscribes before loading Snapshot, buffers concurrent publications, applies only higher business Revisions and ignores a duplicate Revision;
4. a short disconnect is recovered by Centrifugo epoch/offset history;
5. IAM-style revocation triggers server unsubscribe, blocks resubscribe and stops later delivery;
6. a cursor outside the retained window returns no partial history and falls back to an owner Snapshot;
7. a Centrifugo memory-engine restart invalidates transport recovery and falls back to an owner Snapshot;
8. 32 local clients receive one publication as bounded fan-out evidence;
9. a paused slow consumer exceeds the explicit queue limit and is disconnected with server close code `3008`;
10. recovery, proxy, disconnect, node and owner authorization evidence is present without persisting runtime secrets.

## Run

Docker is required.

```bash
npm run s2:centrifugo:check
npm run s2:centrifugo:poc
```

The executable run writes machine-readable evidence to ignored path `out/s2-centrifugo-poc/report.json` and removes its temporary Compose project by default.

Use `node scripts/run-s2-centrifugo-poc.mjs --cleanup=false --project=<name>` only for local diagnosis, then remove the retained containers explicitly.

## Limits

- The POC uses a dedicated digest-pinned Redis engine so bounded history survives Centrifugo restarts; Redis remains non-authoritative and any overflow, epoch reset, gap, or uncertain recovery falls back to the platform Snapshot owner.
- The 32-client fan-out proves only that the protocol boundary works locally; production scale is not certified.
- The locked `v6.8.1` metrics do not expose a dedicated server-unsubscribe counter. Revocation evidence therefore remains owner-audited and is correlated with the unsubscribe push, denied resubscribe and absence of subsequent delivery.
- The experiment does not choose the production broker engine, retention period, connection target, publication rate or rollout SLO. Those belong to service ownership and release-gate decisions.
- It does not define the public REST or browser contract. It proves the transport seam that those contracts may use.
