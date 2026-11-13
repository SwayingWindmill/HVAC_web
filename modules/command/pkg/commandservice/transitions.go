package commandservice

import (
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

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
	for _, status := range earlierStatuses {
		if IsBlockingEarlierIntentStatus(status) {
			return false
		}
	}
	return validateExecutionGovernance(intent, now) == nil
}

// DispatchResolutionDecision is the pure state-transition decision for ResolveDispatch.
type DispatchResolutionDecision struct {
	AttemptFinal         commandmodel.AttemptStatus
	IntentFinal          commandmodel.IntentStatus
	Reason               string
	FreezeControlGroup   bool
	RequeueForRetry      bool
	AcknowledgedAt       *time.Time
	VerificationDeadline *time.Time
}

// DecideDispatchResolution computes the command attempt, command intent, retry, and device-freeze
// outcomes for a ConnectorResult returned by Dispatch.
func DecideDispatchResolution(result commandmodel.ConnectorResult, now time.Time) (DispatchResolutionDecision, error) {
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
		deadline := nowUTC.Add(verificationWindow)
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
	switch result.Outcome {
	case commandmodel.VerificationSucceeded, commandmodel.VerificationInconclusive, commandmodel.VerificationMismatch, commandmodel.VerificationTimedOut:
	default:
		return VerificationResolutionDecision{}, ErrUnsupportedResult
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
