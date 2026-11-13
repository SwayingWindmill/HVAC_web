---
name: command-safety
description: Exhaustively model-check command intent, execution fence, MQTT commit-point, readback verification, device control-group freeze, operator reconciliation, and reply-recovery queue liveness invariants. Use whenever modifying libs/commandmodel, modules/command, connectivity command correlations, or command ADRs.
---

# Command Safety Formal Verification

Run the exhaustive explicit-state model checker (`modules/command/pkg/commandformal`) against real production transition decisions in `commandservice`, `mqttconnector`, and aligned SQL predicates in `postgres_*.go` / `connectivity/store.go`.

- **Base syncCommit**: `baf9a73712a41d6aede4b8049831213fffb1e6d6`

## Quick Run

```bash
npm run command:formal
```

Machine-readable JSON output:

```bash
npm run command:formal -- --json
```

If any invariant or mutation test fails, the runner prints the shortest failing BFS trace (`[Suite/Property] <summary>; trace: step1 -> step2 -> ...`) and exits non-zero.

## Properties Verified (`P1`-`P6`)

| ID | Plain-sentence invariant | Verified production symbols |
| --- | --- | --- |
| **P1** | A command is never dispatched without an unexpired matching authorization grant (and required approvals), and requesters can never approve their own command. | `commandservice.Service.Submit`, `commandservice.Service.Approve`, `commandservice.Service.PrepareDispatch`, `commandservice.CanClaimQueuedIntent` |
| **P2** | After persisting `MAY_COMMIT` (`ArmCommandCorrelation`), MQTT recovery never republishes a physical command and at most one publish occurs per attempt. | `mqttconnector.ResumeActionForCorrelation`, `mqttconnector.CompletedEvidenceFromCorrelation`, `mqttconnector.ReplyBackedResult` |
| **P3** | `OUTCOME_UNKNOWN` freezes the device control group, blocks any later queued command on the same device from claiming dispatch, and clears only via authorized operator reconciliation; `Service.ResolveDispatch` never mutates an already settled attempt. | `commandservice.BlockingEarlierIntentStatuses`, `commandservice.IsBlockingEarlierIntentStatus`, `commandservice.CanClaimQueuedIntent`, `commandservice.DecideDispatchResolution`, `commandservice.DecideOperatorReconcile` |
| **P4** | Edge ACK without valid `EdgeExecution` fails closed and never declares `IntentSucceeded` before independent readback verification. | `commandservice.Service.ResolveDispatch`, `commandservice.DecideDispatchResolution`, `libs/commandmodel.EdgeExecutionEvidence.ValidExecuted` |
| **P5** | Post-`VerificationDeadline` telemetry cannot succeed even with `VERIFIED` outcome, and stale-fence or already-settled dispatch resolutions return `ErrStaleFence`. | `commandservice.Service.ResolveDispatch`, `commandservice.Service.ResolveVerification`, `commandservice.DecideVerificationResolution` |
| **P6** | Persisted `REPLIED` correlations whose attempts are fenced or missing advance to `RESOLVED` and cannot accumulate to starve live `EXECUTED` replies behind `LIMIT 50`. | `mqttconnector.Connector.RecoverTenantReplies`, `mqttconnector.Connector.ResolveCorrelationReply`, `connectivity.Store.RecoverCommandReplies` |

## Suites Covered

1. **`CheckSingleCommandProtocol` (`P1`-`P5`)**: Exhaustive BFS over `CommandIntent`, `CommandAttempt`, MQTT `CommandCorrelation`, readback `VerificationEnvelope`, and `ReconcileRequest` driving real `commandservice.Service` and `mqttconnector` helpers.
2. **`CheckMultiCommandDeviceExclusivity` (`P1`, `P3`)**: Exhaustive BFS over two queued commands targeting one `(tenant, device)` pair, proving single-flight dispatch and `OUTCOME_UNKNOWN` freeze containment.
3. **`CheckReplyRecoveryQueueDrain` (`P6`)**: Bounded (`limit=2`) and production-scale (`limit=50`) queue-drain verification over `mqttconnector.RecoverTenantReplies` + `commandservice.Service`.
4. **`TestFormalPropertiesCanFailOnBrokenTransitions`**: Mutation suite injecting single-guard failures (`FaultHooks`) to prove every invariant `P1`-`P6` fails when broken.
5. **`sql_alignment_test.go`**: Verifies `postgres_dispatch.go`, `postgres_verification.go`, `postgres_reconcile.go`, and `connectivity/store.go` SQL predicates and helper calls remain aligned with `transitions.go` and `RecoverTenantReplies(..., 50)`.

## Covered Code Map

| File | Verified symbols & invariants |
| --- | --- |
| `libs/commandmodel/model.go` | Command enums, `CapabilityProfileFor`, `EdgeExecutionEvidence.ValidExecuted`, `CorrelationState` |
| `modules/command/pkg/commandservice/service.go` | `Submit`, `Approve`, `PrepareDispatch`, `ResolveDispatch`, `PrepareVerification`, `ResolveVerification` |
| `modules/command/pkg/commandservice/governance.go` | `evaluateGovernance`, `validateApproval`, `validateExecutionGovernance` |
| `modules/command/pkg/commandservice/transitions.go` | `BlockingEarlierIntentStatuses`, `CanClaimQueuedIntent`, `DecideDispatchResolution`, `DecideVerificationResolution`, `DecideOperatorReconcile` |
| `modules/command/pkg/commandservice/postgres_dispatch.go` | `ClaimDispatch` (`claimDispatchOnce`) SQL blocking statuses & device freeze check, `ResolveDispatch` delegation |
| `modules/command/pkg/commandservice/postgres_verification.go` | `ResolveVerification` delegation to `DecideVerificationResolution` |
| `modules/command/pkg/commandservice/postgres_reconcile.go` | `reconcileOnce` delegation to `DecideOperatorReconcile` |
| `modules/command/pkg/mqttconnector/connector.go` | `ResumeActionForCorrelation`, `RecoverTenantReplies`, `ResolveCorrelationReply`, `ReplyBackedResult` |
| `modules/connectivity/pkg/connectivity/store.go` | `RecoverCommandReplies` (`state = 'REPLIED'`, `ORDER BY replied_at, attempt_id LIMIT $2`) |

## Keeping the Verifier Synchronized

When modifying any covered file, inspect changes since `syncCommit`:

```bash
git diff baf9a73712a41d6aede4b8049831213fffb1e6d6 -- libs/commandmodel/ modules/command/pkg/commandservice/ modules/command/pkg/mqttconnector/connector.go modules/connectivity/pkg/connectivity/store.go
```

If transitions, SQL guards, or recovery batch limits change:

1. Update shared pure helpers in `modules/command/pkg/commandservice/transitions.go` and `modules/command/pkg/commandformal/`.
2. Run `npm run command:formal` and confirm both all suites and `TestFormalPropertiesCanFailOnBrokenTransitions` pass.
3. Update this `syncCommit` SHA and table, and mirror byte-for-byte to `.claude/skills/command-safety/SKILL.md`.
