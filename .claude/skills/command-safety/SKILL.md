---
name: command-safety
description: Exhaustively model-check command intent, execution fence, MQTT commit-point, readback verification, device control-group freeze, operator reconciliation, and reply-recovery queue liveness invariants. Use whenever modifying libs/commandmodel, modules/command, connectivity command correlations, or command ADRs.
---

# Command Safety Formal Verification

Run the exhaustive explicit-state model checker (`modules/command/pkg/commandformal`) against real production transition decisions in `commandservice`, `mqttconnector`, and aligned SQL predicates in `postgres_*.go` / `connectivity/store.go`.

- **Base syncCommit**: `71424b6c3a96a1feb95eede9eb20e4525b11d129`

## Quick Run

```bash
npm run command:formal
```

Machine-readable JSON output:

```bash
npm run command:formal -- --json
```

If any invariant or mutation test fails, the runner prints the shortest failing BFS trace (`[Suite/Property] <summary>; trace: step1 -> step2 -> ...`) and exits non-zero.

## Properties Verified (`P1`-`P7`)

| ID | Plain-sentence invariant | Verified production symbols |
| --- | --- | --- |
| **P1** | A command is never dispatched without an unexpired matching authorization grant (and required approvals, while `CANCELLED` intents are terminal and unclaimable), requesters can never approve their own command, and `DISPATCH_SAFETY_EVALUATION_FAILED` stays retryable under `PRE_SEND_ONLY` while `DISPATCH_SAFETY_STATE_*` / `DISPATCH_SAFETY_DEVICE_*` / `DISPATCH_SAFETY_INTERLOCK_*` / route errors end `REJECTED`. | `commandservice.Service.Submit`, `commandservice.Service.Approve`, `commandservice.Service.Cancel`, `commandservice.Service.CancelPending`, `commandservice.Service.PrepareDispatch`, `commandservice.CanClaimQueuedIntent`, `commandmodel.TerminalPreSendRejection` |
| **P2** | After persisting `MAY_COMMIT` (`ArmCommandCorrelation`), MQTT recovery never republishes a physical command and at most one publish occurs per attempt. | `mqttconnector.ResumeActionForCorrelation`, `mqttconnector.CompletedEvidenceFromCorrelation`, `mqttconnector.ReplyBackedResult` |
| **P3** | `OUTCOME_UNKNOWN` freezes the device control group, blocks any later queued command on the same device from claiming dispatch, and clears only via authorized operator reconciliation; cancelling an `AWAITING_APPROVAL`/`QUEUED` command unblocks later queued intents without clearing device freezes; `CancelPending(AbortActiveSequences)` cleanly aborts `ACKNOWLEDGED` unit-sequence and emergency-DO `STOP` commands to `FAILED` without freeze while leaving `PREPARED`, pulse, and setpoint commands untouched; `Service.ResolveDispatch` never mutates an already settled attempt. | `commandservice.BlockingEarlierIntentStatuses`, `commandservice.IsBlockingEarlierIntentStatus`, `commandservice.CanClaimQueuedIntent`, `commandservice.DecideDispatchResolution`, `commandservice.DecideOperatorReconcile`, `commandservice.DecideCommandCancel`, `commandservice.IsActiveAbortableSequence` |
| **P4** | Edge ACK without valid `EdgeExecution` fails closed and never declares `IntentSucceeded` before independent readback verification. | `commandservice.Service.ResolveDispatch`, `commandservice.DecideDispatchResolution`, `libs/commandmodel.EdgeExecutionEvidence.ValidExecuted` |
| **P5** | Post-`VerificationDeadline` telemetry cannot succeed even with `VERIFIED` outcome, and stale-fence, post-abort, or already-settled resolutions return `ErrStaleFence`. | `commandservice.Service.ResolveDispatch`, `commandservice.Service.ResolveVerification`, `commandservice.DecideVerificationResolution` |
| **P6** | Persisted `REPLIED` correlations whose attempts are fenced or missing advance to `RESOLVED`, `DrainTenantReplies` loops beyond `LIMIT 50` so stale rows cannot starve trailing live `EXECUTED` replies, and `EvidenceStore.Complete` errors propagate instead of fabricating `OUTCOME_UNKNOWN`. | `mqttconnector.Connector.DrainTenantReplies`, `mqttconnector.Connector.RecoverTenantReplies`, `mqttconnector.Connector.ResolveCorrelationReply`, `connectivity.Store.RecoverCommandReplies` |
| **P7** | Device priority locks (`PriorityLockBlocking`) and active higher-priority intents (`ActiveHigherPriorityBlocking`) block lower-priority `Submit` (`ErrDevicePriorityLocked`) and `ClaimDispatch` (`CanClaimQueuedIntentWithPriority`), while higher-priority intents preempt or abort queued/active lower-priority peers (`PriorityLockPreempts`, SQL `rejectPriorityLockedQueued`). | `commandmodel.PriorityTier`, `commandmodel.DevicePriorityLock`, `commandservice.PriorityLockBlocking`, `commandservice.ActiveHigherPriorityBlocking`, `commandservice.CanClaimQueuedIntentWithPriority`, `commandservice.Service.AcquireDevicePriorityLock` |

## Suites Covered

1. **`CheckSingleCommandProtocol` (`P1`-`P5`)**: Exhaustive BFS over `CommandIntent`, `CommandAttempt`, MQTT `CommandCorrelation`, readback `VerificationEnvelope`, `CancelPending(AbortActiveSequences)`, `TerminalPreSendRejection`, and `ReconcileRequest` driving real `commandservice.Service` and `mqttconnector` helpers.
2. **`CheckMultiCommandDeviceExclusivity` (`P1`, `P3`)**: Exhaustive BFS over two queued commands targeting one `(tenant, device)` pair, proving single-flight dispatch, pre-send safety retry vs terminal rejection, and `OUTCOME_UNKNOWN` freeze containment.
3. **`CheckReplyRecoveryQueueDrain` (`P6`)**: Bounded (`limit=2`), batch (`limit=50`), and looped multi-batch (`stale=120`, `limit=50`) queue-drain verification over `mqttconnector.RecoverTenantReplies`, `mqttconnector.DrainTenantReplies`, and `commandservice.Service`.
4. **`CheckPriorityArbitrationAndLocks` (`P7`)**: Exhaustive BFS over `PriorityAIOptimization`, `PriorityManualOverride`, and `PrioritySafetyInterlock` intents plus `DevicePriorityLock` preemption and `AbortActiveSequences` driving `commandservice.Service` and `commandservice` pure helpers.
5. **`TestFormalPropertiesCanFailOnBrokenTransitions`**: Mutation suite injecting single-guard failures (`FaultHooks`) to prove every invariant `P1`-`P7` fails when broken.
6. **`sql_alignment_test.go`**: Verifies `postgres.go` (`rejectPriorityLockedQueued`), `postgres_dispatch.go` (`resolveDispatchOnce` `AND o.delivered_at IS NULL` + `expireGovernanceInvalidQueued` outbox retirement), `postgres_verification.go`, `postgres_reconcile.go`, `postgres_cancel.go` (`abortActiveSequences` / `IsActiveAbortableSequence`), `libs/commandmodel/model.go` (`TerminalPreSendRejection`), `connectivity/store.go`, and `mqttconnector/connector.go` (`DrainTenantReplies` + `completeRecoveredReply`) remain aligned with the formal model.

## Covered Code Map

| File | Verified symbols & invariants |
| --- | --- |
| `libs/commandmodel/model.go` | Command enums, `PriorityTier`, `CommandSource`, `DevicePriorityLock`, `PriorityRank`, `PriorityLockPreempts`, `CapabilityProfileFor`, `EvaluateRiskLevel`, `EdgeExecutionEvidence.ValidExecuted`, `CorrelationState`, `ErrCommandCorrelationNotFound`, `ErrCommandCorrelationMismatch`, `TerminalPreSendRejection`, `NormalizeTraceID` |
| `modules/command/pkg/commandservice/service.go` | `Submit`, `Approve`, `Cancel`, `CancelPending`, `PrepareDispatch`, `ResolveDispatch`, `PrepareVerification`, `ResolveVerification`, `AcquireDevicePriorityLock`, `ReleaseDevicePriorityLock`, `Close` (`controlFailureQueue` drain) |
| `modules/command/pkg/commandservice/governance.go` | `evaluateGovernance`, `approvalPolicyForRisk`, `validateApproval`, `validateExecutionGovernance` |
| `modules/command/pkg/commandservice/transitions.go` | `BlockingEarlierIntentStatuses`, `CanClaimQueuedIntent`, `CanClaimQueuedIntentWithPriority`, `PriorityLockBlocking`, `ActiveHigherPriorityBlocking`, `DecideCommandCancel`, `DecideDispatchResolution`, `DecideVerificationResolution`, `DecideOperatorReconcile`, `IsActiveAbortableSequence` |
| `modules/command/pkg/commandservice/postgres_dispatch.go` | `ClaimDispatch` (`claimDispatchOnce`) SQL blocking statuses & device freeze check, `ResolveDispatch` (`resolveDispatchOnce` `AND o.delivered_at IS NULL`), `expireGovernanceInvalidQueued`, `DispatchEnvelope.TraceID` projection, transactional `insertControlAlarmOutbox` |
| `modules/command/pkg/commandservice/postgres_cancel.go` | `Cancel` (`cancelOnce`) & `CancelPending` delegation to `DecideCommandCancel` and `IsActiveAbortableSequence`, outbox retirement (`delivered_at = $3, lease_until = NULL`) |
| `modules/command/pkg/commandservice/postgres_verification.go` | `ResolveVerification` delegation to `DecideVerificationResolutionWithEdge`, transactional `insertControlAlarmOutbox` |
| `modules/command/pkg/commandservice/control_alarm_outbox.go` | Durable `command_runtime.control_alarm_outbox` enqueue/claim/deliver/fail/dead-letter transitions & `ControlAlarmRelay` |
| `modules/command/pkg/commandservice/postgres.go` | `PostgresStore`, `Submit`, `Close`, `commitAndNotifyFailures`, `replayIdempotentCommand`, `SyncCapabilityProfiles` |
| `modules/command/pkg/commandservice/shutdown.go` | `ControlAuthority.ExecuteSiteControlShutdown`, `executeAbortUnitSequence`, `releaseOrDeferHoldingAuxiliaries`, `dispatchShutdownTarget`, `reconcileOutcomeUnknownDeviceCommands` |
| `modules/command/pkg/commandservice/postgres_reconcile.go` | `reconcileOnce` delegation to `DecideOperatorReconcile` |
| `modules/command/pkg/mqttconnector/connector.go` | `EnableManualAcknowledgment`, `SubscriptionTopicFilter`, `checkSubackReasons`, sharded `onReply` custody worker pool, `ResumeActionForCorrelation`, `DrainTenantReplies`, `RecoverTenantReplies`, `ResolveCorrelationReply`, `ReplyBackedResult`, `completeRecoveredReply`, `buildCommandEnvelope` `traceId`, `recoverReplies` panic guard |
| `modules/connectivity/pkg/connectivity/store.go` | `RecoverCommandReplies` (`state = 'REPLIED'`, `ORDER BY replied_at, attempt_id LIMIT $2`), `runSerializableTenantOp` (`40001`/`40P01` retry) |

## Keeping the Verifier Synchronized

When modifying any covered file, inspect changes since `syncCommit`:

```bash
git diff 5685f2fe27c98e466aa678c09ca39b29b8255ede -- libs/commandmodel/ modules/command/pkg/commandservice/ modules/command/pkg/mqttconnector/connector.go modules/connectivity/pkg/connectivity/store.go
```

If transitions, SQL guards, or recovery batch limits change:

1. Update shared pure helpers in `modules/command/pkg/commandservice/transitions.go` and `modules/command/pkg/commandformal/`.
2. Run `npm run command:formal` and confirm both all suites and `TestFormalPropertiesCanFailOnBrokenTransitions` pass.
3. Update this `syncCommit` SHA and table, and mirror byte-for-byte to `.claude/skills/command-safety/SKILL.md`.
