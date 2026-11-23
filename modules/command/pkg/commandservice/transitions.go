package commandservice

import (
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

var ErrDevicePriorityLocked = errors.New("device is locked at a higher command priority")

var blockingEarlierIntentStatuses = []commandmodel.IntentStatus{
	commandmodel.IntentSubmitted,
	commandmodel.IntentValidating,
	commandmodel.IntentAwaitingApproval,
	commandmodel.IntentApproved,
	commandmodel.IntentQueued,
	commandmodel.IntentDispatching,
	commandmodel.IntentOutcomeUnknown,
}

// BlockingEarlierIntentStatuses returns the Intent statuses that block dispatching
// any later CommandIntent on the same Device.
func BlockingEarlierIntentStatuses() []commandmodel.IntentStatus {
	return append([]commandmodel.IntentStatus(nil), blockingEarlierIntentStatuses...)
}

// IsBlockingEarlierIntentStatus reports whether an earlier CommandIntent in this status
// prevents ClaimDispatch (claimDispatchOnce) from dispatching a later command on the same Device.
func IsBlockingEarlierIntentStatus(status commandmodel.IntentStatus) bool {
	for _, candidate := range blockingEarlierIntentStatuses {
		if status == candidate {
			return true
		}
	}
	return false
}

// CanClaimQueuedIntent evaluates the domain pre-conditions matching PostgresStore.ClaimDispatch (claimDispatchOnce):
// the Intent is QUEUED, its control group is not frozen, no earlier command on the Device is
// active or OUTCOME_UNKNOWN, and its governance grants are valid at now.
func CanClaimQueuedIntent(intent commandmodel.CommandIntent, controlGroupFrozen bool, earlierStatuses []commandmodel.IntentStatus, now time.Time) bool {
	if intent.Status != commandmodel.IntentQueued || controlGroupFrozen {
		return false
	}
	nowUTC := now.UTC()
	if intent.ExpiresAt != nil && !nowUTC.Before(intent.ExpiresAt.UTC()) {
		return false
	}
	if intent.NextDispatchAfter != nil && intent.NextDispatchAfter.After(nowUTC) {
		return false
	}
	for _, status := range earlierStatuses {
		if IsBlockingEarlierIntentStatus(status) {
			return false
		}
	}
	return validateExecutionGovernance(intent, nowUTC) == nil
}

// QueuedPeerState describes another active intent on the same Device for priority-aware claim arbitration.
type QueuedPeerState struct {
	Status                commandmodel.IntentStatus
	PriorityTier          commandmodel.PriorityTier
	DeviceCommandSequence uint64
}

// PriorityLockBlocking reports whether an active device priority lock blocks commands of priority tier at now.
func PriorityLockBlocking(priority commandmodel.PriorityTier, lock *commandmodel.DevicePriorityLock, now time.Time) bool {
	if lock == nil || !lock.LockedUntil.After(now) {
		return false
	}
	return commandmodel.PriorityRank(priority) < commandmodel.PriorityRank(lock.LockedPriority)
}

// ActiveHigherPriorityBlocking reports whether any active non-terminal intent on the same Device
// holds a strictly higher priority rank than the incoming command priority.
func ActiveHigherPriorityBlocking(priority commandmodel.PriorityTier, activeStatuses []commandmodel.IntentPriorityState) bool {
	incomingRank := commandmodel.PriorityRank(priority)
	for _, active := range activeStatuses {
		if !IsBlockingEarlierIntentStatus(active.Status) {
			continue
		}
		if commandmodel.PriorityRank(active.PriorityTier) > incomingRank {
			return true
		}
	}
	return false
}

// ShouldPreemptOnSafetyInterlock reports whether an incoming SAFETY_INTERLOCK command preempts
// an existing AWAITING_APPROVAL or QUEUED lower-priority command on the same Device.
func ShouldPreemptOnSafetyInterlock(incoming commandmodel.PriorityTier, existingStatus commandmodel.IntentStatus, existingPriority commandmodel.PriorityTier) bool {
	if incoming != commandmodel.PrioritySafetyInterlock {
		return false
	}
	if existingStatus != commandmodel.IntentAwaitingApproval && existingStatus != commandmodel.IntentQueued {
		return false
	}
	return commandmodel.PriorityRank(existingPriority) < commandmodel.PriorityRank(incoming)
}

// CanClaimQueuedIntentWithPriority evaluates queued claim eligibility under active device priority locks
// and priority-ranked peer ordering while preserving wire serialization on DISPATCHING/OUTCOME_UNKNOWN.
func CanClaimQueuedIntentWithPriority(
	intent commandmodel.CommandIntent,
	controlGroupFrozen bool,
	lock *commandmodel.DevicePriorityLock,
	peers []QueuedPeerState,
	now time.Time,
) bool {
	if intent.Status != commandmodel.IntentQueued || controlGroupFrozen {
		return false
	}
	nowUTC := now.UTC()
	if intent.ExpiresAt != nil && !nowUTC.Before(intent.ExpiresAt.UTC()) {
		return false
	}
	if intent.NextDispatchAfter != nil && intent.NextDispatchAfter.After(nowUTC) {
		return false
	}
	if PriorityLockBlocking(intent.PriorityTier, lock, nowUTC) {
		return false
	}
	intentRank := commandmodel.PriorityRank(intent.PriorityTier)
	for _, peer := range peers {
		if !IsBlockingEarlierIntentStatus(peer.Status) {
			continue
		}
		if peer.Status == commandmodel.IntentDispatching || peer.Status == commandmodel.IntentOutcomeUnknown {
			return false
		}
		peerRank := commandmodel.PriorityRank(peer.PriorityTier)
		if peerRank > intentRank {
			return false
		}
		if peer.DeviceCommandSequence < intent.DeviceCommandSequence && peerRank >= intentRank {
			return false
		}
	}
	return validateExecutionGovernance(intent, nowUTC) == nil
}

// DispatchResolutionDecision is the pure state-transition decision for ResolveDispatch.
type DispatchResolutionDecision struct {
	AttemptFinal         commandmodel.AttemptStatus
	IntentFinal          commandmodel.IntentStatus
	Reason               string
	FreezeControlGroup   bool
	RequeueForRetry      bool
	NextDispatchAfter    *time.Time
	AcknowledgedAt       *time.Time
	VerificationDeadline *time.Time
}

// DecideDispatchResolutionForIntent extends DecideDispatchResolution with TTL expiration and
// bounded offline-queue deferrals when deliveryMode is QUEUE_IF_OFFLINE.
func DecideDispatchResolutionForIntent(intent commandmodel.CommandIntent, result commandmodel.ConnectorResult, attemptNumber int, now time.Time) (DispatchResolutionDecision, error) {
	nowUTC := now.UTC()
	switch result.Phase {
	case commandmodel.ConnectorPreSendRejected:
		code := strings.TrimSpace(result.FailureCode)
		if commandmodel.IsTTLExpiredFailureCode(code) || (intent.ExpiresAt != nil && !nowUTC.Before(intent.ExpiresAt.UTC())) {
			reason := code
			if !commandmodel.IsTTLExpiredFailureCode(reason) {
				reason = "COMMAND_TTL_EXPIRED"
			}
			return DispatchResolutionDecision{
				AttemptFinal: commandmodel.AttemptNotSent,
				IntentFinal:  commandmodel.IntentExpired,
				Reason:       reason,
			}, nil
		}
		if commandmodel.NormalizeDeliveryMode(intent.DeliveryMode) == commandmodel.DeliveryModeQueueIfOffline &&
			commandmodel.IsDeferrableOfflinePreSendCode(code) {
			nextAt := nowUTC.Add(commandmodel.OfflineDeferBackoff(attemptNumber))
			if intent.ExpiresAt != nil && nextAt.After(intent.ExpiresAt.UTC()) {
				nextAt = intent.ExpiresAt.UTC()
			}
			return DispatchResolutionDecision{
				AttemptFinal:      commandmodel.AttemptNotSent,
				IntentFinal:       commandmodel.IntentQueued,
				Reason:            "OFFLINE_DEFERRED_" + code,
				RequeueForRetry:   true,
				NextDispatchAfter: &nextAt,
			}, nil
		}
		return DecideDispatchResolution(intent.Capability, result, nowUTC)
	case commandmodel.ConnectorExecutionRejected:
		code := strings.TrimSpace(result.FailureCode)
		if commandmodel.IsTTLExpiredFailureCode(code) {
			return DispatchResolutionDecision{
				AttemptFinal: commandmodel.AttemptFailed,
				IntentFinal:  commandmodel.IntentExpired,
				Reason:       code,
			}, nil
		}
		return DecideDispatchResolution(intent.Capability, result, nowUTC)
	default:
		return DecideDispatchResolution(intent.Capability, result, nowUTC)
	}
}

// DecideDispatchResolution computes the command attempt, command intent, retry, and device-freeze
// outcomes for a ConnectorResult returned by Dispatch.
func DecideDispatchResolution(capability commandmodel.Capability, result commandmodel.ConnectorResult, now time.Time) (DispatchResolutionDecision, error) {
	nowUTC := now.UTC()
	switch result.Phase {
	case commandmodel.ConnectorPreSendRejected:
		if failureCode := strings.TrimSpace(result.FailureCode); commandmodel.TerminalPreSendRejection(failureCode) {
			return DispatchResolutionDecision{
				AttemptFinal: commandmodel.AttemptNotSent,
				IntentFinal:  commandmodel.IntentRejected,
				Reason:       failureCode,
			}, nil
		}
		return DispatchResolutionDecision{
			AttemptFinal:    commandmodel.AttemptNotSent,
			IntentFinal:     commandmodel.IntentQueued,
			Reason:          "PROVABLY_NOT_SENT_REQUEUE",
			RequeueForRetry: true,
		}, nil
	case commandmodel.ConnectorExecutionRejected:
		reason := strings.TrimSpace(result.FailureCode)
		if reason == "" {
			reason = "EDGE_EXECUTION_REJECTED"
		}
		return DispatchResolutionDecision{
			AttemptFinal: commandmodel.AttemptFailed,
			IntentFinal:  commandmodel.IntentFailed,
			Reason:       reason,
		}, nil
	case commandmodel.ConnectorRequestCommitted:
		return DispatchResolutionDecision{
			AttemptFinal:       commandmodel.AttemptOutcomeUnknown,
			IntentFinal:        commandmodel.IntentOutcomeUnknown,
			Reason:             "REQUEST_COMMITTED_OUTCOME_UNKNOWN",
			FreezeControlGroup: true,
		}, nil
	case commandmodel.ConnectorAcknowledged:
		ackAt := nowUTC
		deadline := nowUTC.Add(commandmodel.VerificationWindowFor(capability))
		return DispatchResolutionDecision{
			AttemptFinal:         commandmodel.AttemptAcknowledged,
			IntentFinal:          commandmodel.IntentDispatching,
			Reason:               "PROVIDER_ACKNOWLEDGED_AWAITING_REPORTED_STATE",
			AcknowledgedAt:       &ackAt,
			VerificationDeadline: &deadline,
		}, nil
	default:
		return DispatchResolutionDecision{}, ErrUnsupportedResult
	}
}

// VerificationResolutionDecision is the pure state-transition decision for ResolveVerification.
type VerificationResolutionDecision struct {
	Verified           bool
	AttemptFinal       commandmodel.AttemptStatus
	IntentFinal        commandmodel.IntentStatus
	Reason             string
	FreezeControlGroup bool
}

// DecideVerificationResolution enforces the readback verification deadline and reported-state
// quality/freshness/value checks before settling an acknowledged dispatch attempt.
func DecideVerificationResolution(intent commandmodel.CommandIntent, envelope commandmodel.VerificationEnvelope, result commandmodel.VerificationResult, verificationDeadline time.Time, now time.Time) (VerificationResolutionDecision, error) {
	return DecideVerificationResolutionWithEdge(intent, envelope, envelope.EdgeExecution, result, verificationDeadline, now)
}

// DecideVerificationResolutionWithEdge evaluates verification resolution using explicit
// edge execution evidence so live verification and expired-attempt reconciliation share one rule.
func DecideVerificationResolutionWithEdge(
	intent commandmodel.CommandIntent,
	envelope commandmodel.VerificationEnvelope,
	edgeExecution *commandmodel.EdgeExecutionEvidence,
	result commandmodel.VerificationResult,
	verificationDeadline time.Time,
	now time.Time,
) (VerificationResolutionDecision, error) {
	switch result.Outcome {
	case commandmodel.VerificationSucceeded, commandmodel.VerificationInconclusive, commandmodel.VerificationMismatch, commandmodel.VerificationTimedOut:
	default:
		return VerificationResolutionDecision{}, ErrUnsupportedResult
	}
	if envelope.EdgeExecution == nil && edgeExecution != nil {
		envelope.EdgeExecution = edgeExecution
	}
	nowUTC := now.UTC()
	if result.Outcome == commandmodel.VerificationSucceeded && !nowUTC.After(verificationDeadline) && validReportedState(intent, envelope, result.Reported) {
		return VerificationResolutionDecision{
			Verified:     true,
			AttemptFinal: commandmodel.AttemptVerified,
			IntentFinal:  commandmodel.IntentSucceeded,
			Reason:       "ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED",
		}, nil
	}
	if isPulseCapabilityWithoutPowerConfirmation(intent.Capability, edgeExecution) {
		return VerificationResolutionDecision{
			AttemptFinal:       commandmodel.AttemptFailed,
			IntentFinal:        commandmodel.IntentFailed,
			Reason:             commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
			FreezeControlGroup: false,
		}, nil
	}
	reason := "REPORTED_STATE_VERIFICATION_NOT_PROVEN"
	if nowUTC.After(verificationDeadline) || result.Outcome == commandmodel.VerificationTimedOut {
		reason = "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED"
	}
	return VerificationResolutionDecision{
		AttemptFinal:       commandmodel.AttemptOutcomeUnknown,
		IntentFinal:        commandmodel.IntentOutcomeUnknown,
		Reason:             reason,
		FreezeControlGroup: true,
	}, nil
}

func isPulseCapabilityWithoutPowerConfirmation(capability commandmodel.Capability, edgeExecution *commandmodel.EdgeExecutionEvidence) bool {
	if edgeExecution == nil {
		return false
	}
	switch capability {
	case commandmodel.CapabilityStart:
		return edgeExecution.ValidPulseDualReadback()
	case commandmodel.CapabilityStop:
		return edgeExecution.ValidPulseDualReadback() || edgeExecution.ValidEmergencyDoReleaseReadback()
	case commandmodel.CapabilityStartUnitSequence, commandmodel.CapabilityStopUnitSequence:
		return edgeExecution.ValidUnitSequenceAcceptance()
	default:
		return false
	}
}

// IsActiveAbortableSequence reports whether an already-ACKNOWLEDGED dispatching command
// represents an in-flight unit sequence or emergency DO release that can be cleanly aborted
// to FAILED without freezing the device control group when cancelling pending commands.
func IsActiveAbortableSequence(capability commandmodel.Capability, edgeExecution *commandmodel.EdgeExecutionEvidence) bool {
	switch capability {
	case commandmodel.CapabilityStartUnitSequence, commandmodel.CapabilityStopUnitSequence:
		return true
	case commandmodel.CapabilityStop:
		return edgeExecution != nil && edgeExecution.ValidEmergencyDoReleaseReadback()
	default:
		return false
	}
}

// ReconcileDecision is the pure operator reconciliation transition for an OUTCOME_UNKNOWN command.
type ReconcileDecision struct {
	IntentFinal         commandmodel.IntentStatus
	Reason              string
	ReleaseControlGroup bool
}

// DecideOperatorReconcile validates that the Intent is OUTCOME_UNKNOWN and the operator holds
// a valid current-revision COMMAND_SUBMIT grant before settling to SUCCEEDED or FAILED.
func DecideOperatorReconcile(intent commandmodel.CommandIntent, request commandmodel.ReconcileRequest, now time.Time) (ReconcileDecision, error) {
	final, reason := commandmodel.IntentSucceeded, "OPERATOR_RECONCILED_APPLIED"
	switch request.Outcome {
	case commandmodel.ReconciledApplied:
	case commandmodel.ReconciledNotApplied:
		final, reason = commandmodel.IntentFailed, "OPERATOR_RECONCILED_NOT_APPLIED"
	default:
		return ReconcileDecision{}, ErrReconciliationInvalid
	}
	if intent.Status != commandmodel.IntentOutcomeUnknown {
		return ReconcileDecision{}, ErrReconciliationInvalid
	}
	currentRevision, _, _, _ := commandCapabilityProfile(intent.Capability)
	if err := validateAuthorizationScope(request.Authorization, commandmodel.AuthorizationCommandSubmit,
		request.Authorization.PrincipalID, intent.TenantID, intent.SiteID, intent.DeviceID,
		intent.Capability, currentRevision, intent.Risk, now.UTC()); err != nil {
		return ReconcileDecision{}, err
	}
	return ReconcileDecision{
		IntentFinal:         final,
		Reason:              reason,
		ReleaseControlGroup: true,
	}, nil
}

// ErrCancelInvalid reports an attempt to cancel a command whose lifecycle state already entered
// dispatch or settled, or a malformed cancel request.
var ErrCancelInvalid = errors.New("command cannot be cancelled in its current state")

// CancelDecision is the deterministic state decision produced when cancelling a command intent.
type CancelDecision struct {
	IntentFinal     commandmodel.IntentStatus
	Reason          string
	RetireOutboxRow bool
}

// DecideCommandCancel validates that the Intent is AWAITING_APPROVAL or QUEUED and the caller
// holds a valid current-revision COMMAND_SUBMIT grant before settling to CANCELLED.
func DecideCommandCancel(intent commandmodel.CommandIntent, request commandmodel.CancelRequest, now time.Time) (CancelDecision, error) {
	if intent.Status != commandmodel.IntentAwaitingApproval && intent.Status != commandmodel.IntentQueued {
		return CancelDecision{}, ErrCancelInvalid
	}
	currentRevision, _, _, _ := commandCapabilityProfile(intent.Capability)
	if err := validateAuthorizationScope(request.Authorization, commandmodel.AuthorizationCommandSubmit,
		request.Authorization.PrincipalID, intent.TenantID, intent.SiteID, intent.DeviceID,
		intent.Capability, currentRevision, intent.Risk, now.UTC()); err != nil {
		return CancelDecision{}, err
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" {
		reason = "OPERATOR_CANCELLED"
	}
	return CancelDecision{
		IntentFinal:     commandmodel.IntentCancelled,
		Reason:          reason,
		RetireOutboxRow: true,
	}, nil
}
