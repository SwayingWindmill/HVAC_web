# S1 HVAC Web Registry Pages

## Scope

Ticket 06 connects the existing HVAC Web System and Assets surfaces to the eight public S1 Registry GET routes through the generated `platformGateway.gen.ts` client.

The slice is read-only. Registry writes, Device online status, telemetry, point quality and control operations remain outside S1.

## Runtime mode

Set:

```text
VITE_API_MODE=real
S0_GATEWAY_ONLY=true
PLATFORM_GATEWAY_PROXY_TARGET=https://<platform-gateway-origin>
```

In real mode:

- Organization and Site navigation is derived only from authorized Gateway responses.
- System displays authoritative Site UUIDv7, owning Organization, IANA timezone, lifecycle and revision.
- Assets keeps Equipment and Device as separate Registry identities.
- Device `status` is shown only as Registry lifecycle. It is never converted into online or telemetry state.
- List pagination uses the opaque `nextCursor` returned by Gateway.
- Detail URLs use platform IDs and preserve the Gateway's indistinguishable `RESOURCE_NOT_FOUND` response.
- Registry failures render typed loading, empty, unavailable, timeout, cursor and invisibility states.
- Local Mock business data is not substituted after a real request failure.
- Browser-supplied `X-Site-Id`, Organization, role, admin or scope headers are not used.

In mock mode, the existing demo surfaces are loaded from isolated lazy chunks. Their local write interactions are not available in real mode.

## Operational checks

Run the complete Registry Web capability gate:

```text
npm run domain:run -- --domain=registry --layers=contracts,unit,browser
```

Individual evidence:

```text
npm run s1:hvac-web:check
npm run s1:hvac-web:build
npm run audit:s1-hvac-web-registry
```

The browser audit writes:

```text
out/s1-registry-web/hvac-web-registry-browser.json
```

The report records the Session-protected Gateway routes used, cursor calls, resource-invisibility assertion and zero counts for Mock asset-tree, telemetry and forged authorization-header usage.

## Failure handling

- `RESOURCE_NOT_FOUND`: display the same safe state for missing and unauthorized resources; do not discover parent or sibling resources.
- `CURSOR_INVALID`: ask the operator to reload the list; do not reuse or reinterpret the cursor.
- `REGISTRY_UNAVAILABLE` / `REGISTRY_TIMEOUT`: display a retry action only when Problem Details marks the failure retryable.
- `MAPPING_INVALID` / `MAPPING_QUARANTINED`: display migration mapping not-ready status; do not fall back to Legacy or Mock data.
- Network/contract failure: fail visibly and preserve the real-mode boundary.

## Rollback

The frontend rollout can be reverted to mock mode only in an explicitly configured non-production demonstration environment. Production rollback is performed at the Gateway ownership layer described in `s1-registry-routing-cutover.md`; the browser must not choose Legacy or Mock as a fallback.
