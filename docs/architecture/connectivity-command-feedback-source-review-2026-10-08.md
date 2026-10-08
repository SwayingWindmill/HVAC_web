# Connectivity command feedback source review — 2026-10-08

Scope: issue #397 acceptance exposed a public Command submission regression: a Registry `COMMAND` Point explicitly references a reported setpoint Point classified as `SETTING`, but the gateway accepts only `STATE` or `TELEMETRY` feedback. This review concerns that classification mismatch, not a redesign of command execution or Connectivity identity.

## Local owner contracts reviewed

- `GLOSSARY.md`: Command Intent is distinct from approval, transport delivery, device acknowledgement, readback and verified physical outcome. Point identity belongs to its reporting Device; Gateway scope comes from Registry.
- `docs/adr/0011-spatial-equipment-sensor-point-model.md` (ADR 0011): command and feedback have distinct authority; control requires an active COMMAND Point with CONTROLS relationship. Its earlier canonical terminology is partly superseded by ADR 0013.
- `docs/adr/0015-gateway-identity-and-single-connectivity.md`: certificate-derived Gateway identity, Registry-derived scope and terminal revocation remain authoritative.
- `scripts/central-plant-spatial-model.mjs`: `settingSources` classifies observed temperature setpoint, frequency, load limit and fan speed as SETTING. Observed Points are `writable: false`; `buildCentralPlantControlPoints` creates separate writable COMMAND Points and explicit `feedbackSourceKey` / `feedbackPointKey` metadata.
- `cmd/energy-api/internal/gateway/command.go`, `resolveAssetCommandTarget`: command target must be active, writable COMMAND, in Asset Tenant/Site scope, with current CONTROLS binding and supported capability revision. Feedback is resolved separately by exact reporting Device and declared source key. Before this repair its type filter excludes SETTING.

## Pinned official source comparison

All source and test links below are immutable commits. Source files were fetched read-only from official repositories on 2026-10-08; upstream tests were inspected, not executed. No upstream source is copied into project code. Website documentation is explicitly supplementary and not version-pinned.

### ThingsBoard v4.4

Commit: `6d46786579c8b29caf5102f95ddb133674bed68b`.

- [DefaultTbRuleEngineRpcService.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/main/java/org/thingsboard/server/service/rpc/DefaultTbRuleEngineRpcService.java): associates response consumers with request identities, ignores unknown/stale responses and separately reports timeout. This establishes request/response distinction, not an HVAC Point taxonomy.
- [AbstractMqttServerSideRpcIntegrationTest.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/test/java/org/thingsboard/server/transport/mqtt/mqttv3/rpc/AbstractMqttServerSideRpcIntegrationTest.java): `processJsonTwoWayRpcTest` sends a setGpio request, verifies the delivered request and separately asserts device response payload. These tests establish transport RPC behavior, not measured physical success.
- [Official Command & Control documentation](https://thingsboard.io/docs/user-guide/command-and-control/), inspected 2026-10-08: describes different confirmation semantics for one-way and two-way RPC. The website may describe versions newer than this pin; the pinned source owns claims about v4.4.
- Additional bounded security comparison: [CommandFeedbackHandler.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/main/java/org/thingsboard/server/service/agent/CommandFeedbackHandler.java) checks event ownership before applying agent feedback; [CommandFeedbackHandlerTest.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/test/java/org/thingsboard/server/service/agent/CommandFeedbackHandlerTest.java) rejects another agent/tenant and stale step. This is an agent-event mechanism, **not device readback**, and is not adopted as a device-command implementation.

**ADOPT:** preserve desired request, reply and observed result as separate evidence; apply evidence only to its authorized identity. **ADAPT:** HVAC uses an explicit separate reported Point, including SETTING, for readback. Nothing in these reviewed RPC mechanisms prescribes excluding that local type. **REJECT:** treating a published command or generic RPC response as verified HVAC physical outcome.

### OpenEMS 2026.9.0

Commit: `14f0dedf5fe279845281bafaf0e48ea0ab51333a`.

- [WriteChannel.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.common/src/io/openems/edge/common/channel/WriteChannel.java): pending desired writes have `getNextWriteValue`, set callbacks and reset semantics. These are distinct from a Channel's observed value.
- [IntegerReadChannel.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.common/src/io/openems/edge/common/channel/IntegerReadChannel.java): exposes the typed read Channel role separately.
- [coreconcepts.adoc](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/doc/modules/ROOT/pages/coreconcepts.adoc): Channel metadata explicitly distinguishes access mode from value type/unit; components supply read values and trigger write actions.
- [SendChannelValuesWorkerTest.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.controller.api.backend/test/io/openems/edge/controller/api/backend/SendChannelValuesWorkerTest.java): observed values are placed in the process image and tested for backend aggregation, including null and latest-value cases. These tests do not establish a remote command verification policy.

**ADOPT:** distinguish desired write from observed controller value. **ADAPT:** retain separate HVAC COMMAND and read-only SETTING identities instead of adopting OpenEMS Channel APIs or permitting a reported setpoint to become writable. The reported setting is useful readback precisely because it describes the controller's current setpoint. **REJECT:** pending desired value as evidence that the controller accepted or applied a command.

### MyEMS v6.9.0

Commit: `b360f5bb4c2be4fd15854057963531b2e8d1bc0a`.

- [myems-api/core/command.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/core/command.py), `CommandSend.on_put`: authorizes an administrator, stores requested set_value, publishes MQTT and returns success without checking controller readback in this handler.
- [myems_system_db.sql](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/database/install/myems_system_db.sql): separate `tbl_commands`, `tbl_points` and `tbl_points_set_values` records; their existence alone does not establish a verification workflow.
- [test_mqtt.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/test_mqtt.py): manual publish procedure, not an asserted command-to-readback test. Pinned tree search found no command-specific test file; this is a bounded test-discovery finding, not proof that no relevant test exists anywhere.
- [README.md](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/README.md): distinguishes optional enterprise equipment-control capabilities. No enterprise implementation is claimed to have been reviewed.

**REJECT:** publish-and-return-success semantics for HVAC control. No mechanism from this handler justifies blocking a read-only reported SETTING as feedback or replacing current HVAC verified outcome semantics.

## Decision and acceptance boundary

**ADAPT — accept SETTING on the feedback side only.** The smallest correction adds the existing current Registry type to the feedback allowlist. Retain STATE and TELEMETRY for existing valid feedback contracts. Do not change the observed fixture into a writable COMMAND Point, add compatibility paths, or redefine Gateway identity.

The separately selected writable COMMAND Point, current CONTROLS relationship, capability revision, exact reporting Device/Point code, tenant/site checks, IAM grant, telemetry quality/freshness and final command verification remain mandatory. Accepting SETTING for readback does not authorize setting it directly and does not bypass any approval or physical safety rule. Reading a current setpoint also does not prove the resulting equipment temperature has physically converged.

The local simulator contract (`tools/eg8200-simulator/internal/simulator/asset_model.go`, `validateCommandContract`) requires `feedbackPointKey`, the platform Point code. `feedbackSourceKey` is a hardware field and differs from the Registry source key in the actual plant. Resolve the explicit `feedbackPointKey` on the same Device, then use that Point code for both current-state Snapshot and verification. Do not add a source-key fallback.

Required regression evidence: an authorized command with its explicit reported SETTING reference reaches the Command owner; attempts to submit the reported SETTING itself remain rejected. Live A/B Gateway dispatch and accepted telemetry readback must still be demonstrated by #397 acceptance; this source review alone is not evidence that they work.

## Public transport contract correction

Live acceptance also exposed two existing owner/BFF disagreements. `contracts/http/s3-command-public.openapi.json` defines the Intent status vocabulary, while `modules/command/pkg/commandservice/http.go` projected it into a separate control vocabulary (QUEUED became APPROVED). Remove that projection and return the authoritative Intent statuses, including transitions. The Command owner generates ordinary RFC UUIDs (currently UUIDv4); the public Command ID contract does not require UUIDv7. Accept canonical lowercase RFC UUID Command IDs at the BFF, retaining UUIDv7 requirements for Registry identities. These corrections follow existing owner/OpenAPI contracts, introduce no new upstream mechanism, and preserve all authorization and physical verification checks. Public POST regression tests fail before and pass after these corrections.

Actual A/B public POST and GET now prove `QUEUED → DISPATCHING → SUCCEEDED`, with `ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED`, while both simulators run. See [#397 acceptance](connectivity-397-acceptance-2026-10-08.md) for IDs, timestamps and evidence limits.
