# Gateway credentials: pinned source review for #407

Date: 2026-10-05. Scope: enrollment code + CSR, 90-day certificate, mTLS renewal, Gateway revocation, audit. Authorities: [issue #407](https://github.com/SwayingWindmill/HVAC_web/issues/407), `GLOSSARY.md`, ADR 0015 and ADR 0016. This is a source review, not implementation or runtime certification. Upstream tests were read, not executed.

## Verified reference pins and provenance

Fresh read-only shallow clones under `/tmp/hvac-credential-refs/` were checked with `git rev-parse HEAD`:

| Project | Official tag | Full commit |
| --- | --- | --- |
| ThingsBoard | `v4.4` | `6d46786579c8b29caf5102f95ddb133674bed68b` |
| OpenEMS | `2026.9.0` | `14f0dedf5fe279845281bafaf0e48ea0ab51333a` |
| MyEMS | `v6.9.0` | `b360f5bb4c2be4fd15854057963531b2e8d1bc0a` |

No upstream implementation was copied. ThingsBoard's root [LICENSE](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/LICENSE) is BUSL-1.1 and files have mixed license headers; use as design evidence only. OpenEMS [README license section](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/README.md#license) distinguishes EPL-2.0 and AGPL-3.0 components. MyEMS's [LICENSE](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/LICENSE) is MIT. These observations do not authorize copying without a file-specific review.

## ThingsBoard: provisioned identity and concrete certificate validation

Reviewed official source:

- [DeviceProvisionServiceImpl.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/main/java/org/thingsboard/server/service/device/DeviceProvisionServiceImpl.java): `provisionDevice`, `processProvision`, `provisionDeviceViaX509Chain`, `notify`, `logAction`.
- [MqttTransportHandler.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/common/transport/mqtt/src/main/java/org/thingsboard/server/transport/mqtt/MqttTransportHandler.java): `processConnect`, `processX509CertConnect`.
- [DeviceProvisionServiceTest.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/test/java/org/thingsboard/server/service/device/provision/DeviceProvisionServiceTest.java): existing-device X509 credential update, missing-device rejection when creation disabled, CN extraction.
- [MqttProvisionJsonDeviceTest.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/test/java/org/thingsboard/server/transport/mqtt/mqttv3/provision/MqttProvisionJsonDeviceTest.java): disabled provision, pre-provisioned device, new-device certificate provision, bad provision key.
- Current official [MQTT provisioning documentation](https://thingsboard.io/docs/reference/mqtt-api/provisioning/) and [provisioning strategy documentation](https://thingsboard.io/docs/paas/user-guide/provisioning/), consulted 2026-10-05. These live docs supplement, rather than replace, the pinned source.

Actual behavior: pre-provisioned mode checks profile secret and device membership, then checks and writes a server-side `provisionState` attribute. X509 chain mode derives a device name from CN, looks it up within the profile's Tenant, and can update its registered certificate. MQTT certificate authentication checks certificate validity (unless explicitly configured to skip it), hashes the actual certificate and requests validation of that credential. Provision success/failure is audited and emitted to the rule engine.

**ADOPT**: identity comes from authenticated credentials; credentials are resolved to an owner/Tenant before domain work. Certificate validity and exact credential identity matter, not only CN. Keep explicit provision failure and lifecycle audit evidence.

**ADAPT**: only already-registered Gateway Devices may enroll. Replace profile-shared provision secrets with per-Gateway, expiring, one-time codes; CSR signing belongs to Connectivity's CA. Enforce one-time consumption and issue/audit persistence atomically in PostgreSQL. The source's read-attribute/write-attribute sequence is not evidence of an atomic consumption guarantee under concurrent requests.

**REJECT**: automatic Device creation, CN regex-derived business names, shared fleet provisioning secrets, and importing the embedded transport/queue architecture. These conflict with Registry ownership, ADR 0015, and #407. ThingsBoard's reviewed provisioning registers supplied certificates; it is not evidence for a CA service issuing 90-day certificates from a one-time code and CSR. Its X509 update replaces the stored credential; #407 explicitly requires overlap until the older certificate expires.

## OpenEMS: credential-to-Edge mapping at connection establishment

Reviewed official source and repository documentation:

- [WebsocketServer.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.backend.edge.application/src/io/openems/backend/edge/server/WebsocketServer.java): handshake obtains API key, authenticates it, rejects missing mapping, and attaches resolved Edge id to connection state.
- [Cache.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.backend.edge.application/src/io/openems/backend/edge/application/Cache.java): API-key mapping supplied by metadata; blank/missing key fails.
- [ControllerApiBackendImpl.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.controller.api.backend/src/io/openems/edge/controller/api/backend/ControllerApiBackendImpl.java): API key sent as an HTTP header.
- [metadata.adoc](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/doc/modules/ROOT/pages/backend/metadata.adoc), [architecture.adoc](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/doc/modules/ROOT/pages/backend/architecture.adoc), and [controller README](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.controller.api.backend/readme.adoc).
- Inspected nearby tests: [MetadataUtilsTest.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.backend.common/test/io/openems/backend/common/metadata/MetadataUtilsTest.java) covers Edge listing/pagination; [CredentialsTest.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.backend.metadata.odoo/test/io/openems/backend/metadata/odoo/postgres/CredentialsTest.java) covers Odoo configuration conversion. Neither proves certificate enrollment, renewal, or immediate revocation. The inspected Edge application/manager areas did not supply such tests.

**ADOPT** the owner-authenticated mapping from transport credential to immutable Gateway identity. **ADAPT** it using Registry's authoritative read view and X509 instead of API-key metadata cache. **REJECT** adding another identity cache/projection or API-key transport to #407. In these reviewed areas, OpenEMS is not a reference implementation of code/CSR enrollment or 90-day certificate renewal. A connection-time mapping alone also does not prove immediate isolation of an already-established connection after credential changes.

## MyEMS: static Gateway token and shared database collection

Reviewed:

- [core/gateway.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/core/gateway.py): Gateway CRUD creates UUID token, stores it and returns it in Gateway responses.
- [modbus main.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-modbus-tcp/main.py): collector selects data sources through Gateway id/token SQL predicates; configuration is loaded at startup.
- [gateway.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-modbus-tcp/gateway.py): heartbeat updates shared database Gateway timestamp.
- [service README](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-modbus-tcp/README.md), [API test_mqtt.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/test_mqtt.py): manual MQTT publish script uses configured username/password; this is not a credential lifecycle regression suite. No enrollment/renewal/revocation test was found in the inspected Gateway/collector paths.

**ADOPT** the explicit registered Gateway as configuration/collection scope. **REJECT** persistent readable Gateway tokens, shared-database collector authentication, and manual credential installation for this product. The reviewed MyEMS paths do not implement #407's certificate lifecycle; absence is bounded to those paths, not a claim about every integration or commercial extension.

## Local implementation decisions and security limits

1. **Code + CSR**: keep code plaintext only in the initial operator response, never persist or audit it. Persist a cryptographic hash, 24-hour expiry and consumed state. Generate randomness with Go `crypto/rand`; parse/sign with existing standard `crypto/x509`, not a new CA framework. Validate the CSR signature as proof of possession, accept supported keys, and construct the issued leaf subject/extensions on the server: CN = registered Gateway id, client authentication only, never CA privileges or arbitrary caller-supplied SAN/subject extensions. This is a product-specific adaptation, not copied platform code.
2. **Atomicity**: serialize enrollment, renewal and revocation for the same Gateway. Committing issuance must commit credential state and audit together; consumption races must yield one successful issuance. Failed CSR validation/signing must not leave a consumed code without its credential. Revocation must also invalidate outstanding codes so a pre-revocation code cannot restore access afterward.
3. **Renewal**: validate the mTLS chain, current validity, Gateway CN and the exact presented certificate fingerprint against an ACTIVE stored credential. Trust only nginx-authenticated certificate context on a protected backend boundary; never accept a spoofable public certificate header. Store the new credential alongside the old one until its original expiry, as #407 requires. Checking only that the Gateway has some active certificate lets a revoked leaf renew using another leaf's status.
4. **Gateway-wide revocation**: `modules/connectivity/pkg/connectivity/uplink.go` and `command_route.go` currently authorize `EXISTS(any ACTIVE unexpired credential)` for a Gateway. Revoking only the newest credential therefore fails when an older renewed certificate remains active. With the accepted Mosquitto/no-CRL design, revoke **all** active Gateway credentials atomically, invalidate outstanding enrollment codes, and make renewal reject those leaf fingerprints. Uplink/command enforcement must reread authoritative state rather than retain an acceptance cache. Cover overlapping certificates and revoke-versus-renew concurrency in meaningful tests.
5. **Terminal Gateway revocation**: the selected minimal implementation treats revocation as terminal for the registered Gateway identity: both generating a fresh code and renewing are rejected afterward. Recover a compromised Gateway using a newly registered Gateway id and explicitly reassign its source bindings. Document this product meaning in ADR 0015 and the operator UI; #407 does not require restoring a revoked identity. One enrollment-state row per Gateway, locked across code generation/enrollment/renewal/revocation, is sufficient; a separate revocation subsystem is unnecessary. This decision closes the old-leaf reactivation issue described below.
6. **Transport limitation**: Connectivity receives MQTT topic/payload from Mosquitto, not the publishing client's certificate fingerprint. Consequently this design enforces Gateway isolation, not selective revocation of an individual MQTT leaf. If a new certificate were later issued for a revoked Gateway, the `any ACTIVE` predicate would again accept messages from a holder of an older still-broker-valid certificate, including command confidentiality through existing subscriptions; terminal revocation forbids that recovery path. During ordinary overlapping renewal, an older established MQTT connection is not proven to be disconnected at leaf expiry by the reviewed application logic. Do not claim per-leaf MQTT expiry/revocation or broker disconnect. Such guarantees require a separately reviewed transport identity/authentication design; silently adding a broker plugin is not authorized by ADR 0015.
7. **Auditing**: emit Gateway identity, actor/operation, time, outcome and credential lifecycle evidence without plaintext codes, private keys, request authorization headers or full CSR payloads. Prefer existing audit persistence/outbox contract; do not create a second audit pipeline or CI gate.

## Result

### Reviewed local Audit boundary

Reviewed `modules/audit/internal/audit/store.go` and `consumer.go`, and the existing `libs/sessionevent` and `libs/operationsauditevent` contracts. Central Audit consumes Session and Operations business events, not a generic credential event. **ADAPT** their transactional persistence principle: `connectivity.gateway_credential_audit` is append-only Connectivity owner evidence committed with successful code generation, issuance, renewal or revocation. Actor, Gateway and time identify the operation; a committed row means success. Runtime cannot update/delete rows. It stores no code or key and creates no second delivery pipeline. Evidence is queryable in the owner database, **not exposed in the central Audit UI**. A central publication contract is not invented for #407.

### Real Web entry

Browser review found `/system?tab=registry` unusable because a platform route registers protected Site drafts. #407 uses `/settings/integrations`: a real Registry Gateway ledger, authorized Site selection, registration and credential Dialog. Fake Connector inventory/statistics are removed. Preserve accepted ledger/Dialog grammar while applying ADR 0015's deletion of integration instances. Site selection is Router search state, registration is RHF/Zod, APIs use the generated client, and credentials are Query-owned. The initial one-time code is an explicit #407 exception; private keys and internal IDs stay absent from operator UI.

Proceed with #407 using existing PostgreSQL + Go X509 + Mosquitto/nginx boundaries and exact leaf validation for HTTP renewal. The three reviewed platforms justify transport-derived identity, owner mapping and explicit lifecycle evidence, while the one-time code, local CA lifetime, overlap and Gateway isolation are documented HVAC adaptations. Record the MQTT recovery limit alongside acceptance evidence. No new dependency, copied upstream code, or live platform execution was needed for this review.
