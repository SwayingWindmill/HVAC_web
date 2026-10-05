# #407 Gateway credential acceptance — 2026-10-05

Implementation base: `30892f50`; branch `codex/gateway-credentials-407`.

## Delivered behavior

Web `/settings/integrations` registers a Registry Gateway and opens its credential Dialog. Operators generate a 43-character cryptographic one-time code valid for 24 hours; only its SHA-256 hash is persisted. Connectivity signs the Gateway-generated CSR for 90 days and returns the broker URL. Renewal verifies the exact presented unexpired credential, retaining older certificates until their original expiry. Revocation is terminal for that Gateway and invalidates every overlapping certificate and outstanding code. Uplink quarantine and command denial use current authoritative state.

The real Web registration flow exposed missing `Writer: store` in embedded Core assembly; corrected it using the existing Registry writer, without adding another write path. Removed fake integration inventory/statistics and routed the delivered workflow through the current integration entry. No compatibility path was added to the broken historical `/system` registry tab.

## Evidence

- Existing PostgreSQL runner `node scripts/run-s1-registry-postgres-tests.mjs`: passed. Covers authorized Site scope, concurrent one-time consumption (one winner), 24-hour expiry, public HTTP rejection of consumed/expired codes, 90-day issuance, old-leaf overlapping renewal, simultaneous renewal/revocation leaving zero ACTIVE credentials, terminal state and durable issuance/renewal/revocation evidence. Existing uplink test uses the actual Revoke operation and proves uplink quarantine plus downstream command rejection.
- `node scripts/run-mqtt-e2e-tests.mjs`: passed after removing manual Gateway credential SQL. Simulator automatically enrolled with code+CSR, connected to the real TLS broker, published resolved observations, restarted from its bundle and recovered queued data after outage. Fixture TLS ingress is explicitly distinct from real nginx certification.
- Real stack `hvac-local`, browser Chromium **1440×900**, real local administrator OIDC session: Web registered a new acceptance Gateway, generated its code, then closing/reopening removed the code. A separate simulator container generated and retained its own key, enrolled through nginx and became ready. No manual certificate signing or credential SQL.
- Against real nginx: consumed code returned HTTP 400; renewal succeeded twice with the original leaf, proving overlap; a forged `X-Gateway-Client-Cert` without mTLS returned 400; Web permanent-revocation confirmation produced REVOKED; subsequent renewal and new-code generation returned 409. Existing Site A/B simulators remained healthy and were not revoked.
- Browser evidence (Git-ignored): `out/credential-browser-1791210013975/{not-enrolled,active,revoke-confirmation,revoked}.png` and `evidence.json`. Screenshots contain no enrollment code or private key. Actual rendering inspected for hierarchy, density, labels and action emphasis.
- Existing Web Playwright suite: `gateway-credentials.spec.ts` passed; protects code removal on close, cancellation before revocation, explicit permanent confirmation, terminal disabling and CSRF propagation.
- The eight explicitly identified temporary acceptance Gateways were revoked and retired through the public APIs after verification. Site A/B identities and private runtime files were left intact.
- Simulator tests: HTTPS-only enrollment (initial RED then GREEN), and rejection of a stored certificate belonging to another configured Gateway (observed RED then GREEN). All simulator Go packages passed.
- IAM tests prove only Core and the configured Connectivity workload may read live Registry grant status; an unrelated verified workload is rejected. Credential HTTP tests reject unverified workload identity and public spoofing of internal identity.
- Generated OpenAPI clients, ownership and deployment checks passed; Web typecheck passed. Narrow Impeccable detector on the two changed business components: zero findings/advisories; detector evidence supplements actual browser review.
- Final existing Registry domain matrix (`contracts,unit`) and Web principal-contract check passed; no new permanent CI gate or package chain was added.

## Two-axis review

Standards: fixed missing revoke-versus-renew evidence, explicit ACTIVE/INACTIVE/RETIRED labels and required Field composition. Clarified audit owner evidence after inspecting central consumers. Spec: fixed unavailable-inventory empty messaging and Site pagination. Neither review identified a remaining implementation blocker at the reviewed point. Final small fixes are validated by the tests above.

## Limits

Connectivity owns append-only lifecycle evidence in its database; this slice does not expose it in central Audit UI. Mosquitto has no CRL and Connectivity receives Gateway topics, not leaf fingerprints: Gateway-wide isolation is proven, selective leaf revocation or disconnection of an existing broker connection at leaf expiry is not. Ordinary certificate overlap therefore retains the documented transport limitation. A revoked identity is replaced with a newly registered Gateway and explicitly reassigned source bindings.

The full source-deploy run encountered an external Go proxy EOF downloading an unrelated worker dependency. Targeted Connectivity, energy-api, nginx and simulator builds succeeded; unrelated worker rebuilds are not claimed as passed. No existing private runtime file permissions or contents were changed, and runtime secrets remain untracked.
