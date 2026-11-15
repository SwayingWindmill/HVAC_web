package commandformal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/mqttconnector"
)

// RunAll executes all three formal verification suites (single-command protocol,
// multi-command device exclusivity, and MQTT reply recovery queue drain) and returns
// the combined report.
func RunAll(hooks FaultHooks) (FullReport, error) {
	suites := []func(FaultHooks) (SuiteReport, error){
		CheckSingleCommandProtocol,
		CheckMultiCommandDeviceExclusivity,
		CheckReplyRecoveryQueueDrain,
	}
	report := FullReport{
		Passed: true,
		Properties: []PropertyID{
			PropertyP1,
			PropertyP2,
			PropertyP3,
			PropertyP4,
			PropertyP5,
			PropertyP6,
		},
		Suites: make([]SuiteReport, 0, len(suites)),
	}
	for _, runSuite := range suites {
		suiteReport, err := runSuite(hooks)
		if err != nil {
			return report, err
		}
		report.Suites = append(report.Suites, suiteReport)
		report.TotalStates += suiteReport.VisitedStates
		report.TotalEdges += suiteReport.Transitions
		if !suiteReport.Passed {
			report.Passed = false
			if report.Counterexample == nil && suiteReport.Counterexample != nil {
				cx := *suiteReport.Counterexample
				report.Counterexample = &cx
			}
		}
	}
	return report, nil
}

type protoStepKind uint8

const (
	stepSubmitLowRisk protoStepKind = iota + 1
	stepSubmitMediumRisk
	stepSubmitHighRisk
	stepSubmitStartPulse
	stepSubmitStopPulse
	stepSubmitStartUnitSequence
	stepSubmitStopUnitSequence
	stepApproveValid
	stepApproveSelfRejected
	stepCancelIntent
	stepPrepareDispatchValid
	stepPrepareDispatchExpiredGrant
	stepPrepareDispatchWhenFrozen
	stepCorrelationPrepare
	stepCorrelationArm
	stepPublishEdge
	stepRecoverCorrelation
	stepResolvePreSendRetry
	stepResolvePreSendTerminal
	stepResolveExecRejected
	stepResolveRequestCommitted
	stepResolveAckValid
	stepResolveAckPulseDualReadback
	stepResolveAckEmergencyDoRelease
	stepResolveAckUnitSequenceAccepted
	stepResolveAckMissingEdge
	stepResolveDispatchStaleFence
	stepPrepareVerification
	stepResolveVerificationValidInWindow
	stepResolveVerificationMismatch
	stepResolveVerificationValidPastDeadline
	stepReconcileApplied
	stepReconcileNotApplied
)

func (k protoStepKind) String() string {
	switch k {
	case stepSubmitLowRisk:
		return "Submit(lowRisk)"
	case stepSubmitMediumRisk:
		return "Submit(mediumRisk)"
	case stepSubmitHighRisk:
		return "Submit(highRisk)"
	case stepSubmitStartPulse:
		return "Submit(start)"
	case stepSubmitStopPulse:
		return "Submit(stop)"
	case stepSubmitStartUnitSequence:
		return "Submit(startUnitSequence)"
	case stepSubmitStopUnitSequence:
		return "Submit(stopUnitSequence)"
	case stepApproveValid:
		return "Approve(validApprover)"
	case stepApproveSelfRejected:
		return "Approve(selfApprover)"
	case stepCancelIntent:
		return "CancelIntent"
	case stepPrepareDispatchValid:
		return "PrepareDispatch(validGrant)"
	case stepPrepareDispatchExpiredGrant:
		return "PrepareDispatch(expiredGrant)"
	case stepPrepareDispatchWhenFrozen:
		return "PrepareDispatch(whileDeviceFrozen)"
	case stepCorrelationPrepare:
		return "Correlation(PREPARED)"
	case stepCorrelationArm:
		return "Correlation(MAY_COMMIT)"
	case stepPublishEdge:
		return "MQTTPublish"
	case stepRecoverCorrelation:
		return "RecoverCorrelation"
	case stepResolvePreSendRetry:
		return "ResolveDispatch(preSendRetry)"
	case stepResolvePreSendTerminal:
		return "ResolveDispatch(preSendTerminal)"
	case stepResolveExecRejected:
		return "ResolveDispatch(execRejected)"
	case stepResolveRequestCommitted:
		return "ResolveDispatch(requestCommitted)"
	case stepResolveAckValid:
		return "ResolveDispatch(ackValidEdge)"
	case stepResolveAckPulseDualReadback:
		return "ResolveDispatch(ackPulseDualReadback)"
	case stepResolveAckEmergencyDoRelease:
		return "ResolveDispatch(ackEmergencyDoRelease)"
	case stepResolveAckUnitSequenceAccepted:
		return "ResolveDispatch(ackUnitSequenceAccepted)"
	case stepResolveAckMissingEdge:
		return "ResolveDispatch(ackWithoutEdge)"
	case stepResolveDispatchStaleFence:
		return "ResolveDispatch(staleFenceOrSettledAttempt)"
	case stepPrepareVerification:
		return "PrepareVerification"
	case stepResolveVerificationValidInWindow:
		return "ResolveVerification(validInWindow)"
	case stepResolveVerificationMismatch:
		return "ResolveVerification(mismatch)"
	case stepResolveVerificationValidPastDeadline:
		return "ResolveVerification(validPastDeadline)"
	case stepReconcileApplied:
		return "Reconcile(APPLIED)"
	case stepReconcileNotApplied:
		return "Reconcile(NOT_APPLIED)"
	default:
		return fmt.Sprintf("step(%d)", k)
	}
}

type protoAckEdgeKind uint8

const (
	ackEdgeDefault protoAckEdgeKind = iota
	ackEdgePulseDual
	ackEdgeEmergencyDoRelease
	ackEdgeUnitSequenceAccepted
)

type protoStateKey struct {
	submitted           bool
	capability          commandmodel.Capability
	intentStatus        commandmodel.IntentStatus
	attemptCount        int
	attemptStatus       commandmodel.AttemptStatus
	ackEdgeKind         protoAckEdgeKind
	corrState           commandmodel.CorrelationState
	publishesAttempt    int
	deviceFrozen        bool
	verificationOpen    bool
	pastDeadline        bool
	staleAttemptUsed    bool
	selfApproveTried    bool
	expiredClaimTry     bool
	missingEdgeTried    bool
	cancelDispatchTried bool
}

type protoNode struct {
	key   protoStateKey
	steps []protoStepKind
}

type protoSim struct {
	now                 time.Time
	service             *commandservice.Service
	commandID           string
	capability          commandmodel.Capability
	targetSetpoint      float64
	ackEdgeKind         protoAckEdgeKind
	dispatchEnv         commandmodel.DispatchEnvelope
	verifyEnv           commandmodel.VerificationEnvelope
	hasVerifyEnv        bool
	corrState           commandmodel.CorrelationState
	publishesAttempt    int
	deviceFrozen        bool
	staleAttemptUsed    bool
	selfApproveTried    bool
	expiredClaimTry     bool
	missingEdgeTried    bool
	cancelDispatchTried bool
	stepCount           int
}

func newProtoSim() *protoSim {
	sim := &protoSim{now: baseModelTime}
	sim.service = commandservice.New(func() time.Time { return sim.now })
	return sim
}

func (s *protoSim) key() protoStateKey {
	if s.commandID == "" {
		return protoStateKey{}
	}
	intent, err := s.service.Get(s.commandID)
	if err != nil {
		return protoStateKey{}
	}
	var attemptStatus commandmodel.AttemptStatus
	if len(intent.Attempts) > 0 {
		attemptStatus = intent.Attempts[len(intent.Attempts)-1].Status
	}
	return protoStateKey{
		submitted:           true,
		capability:          s.capability,
		intentStatus:        intent.Status,
		attemptCount:        len(intent.Attempts),
		attemptStatus:       attemptStatus,
		ackEdgeKind:         s.ackEdgeKind,
		corrState:           s.corrState,
		publishesAttempt:    s.publishesAttempt,
		deviceFrozen:        s.deviceFrozen,
		verificationOpen:    s.hasVerifyEnv,
		pastDeadline:        s.hasVerifyEnv && s.now.After(s.verifyEnv.VerificationDeadline),
		staleAttemptUsed:    s.staleAttemptUsed,
		selfApproveTried:    s.selfApproveTried,
		expiredClaimTry:     s.expiredClaimTry,
		missingEdgeTried:    s.missingEdgeTried,
		cancelDispatchTried: s.cancelDispatchTried,
	}
}

func traceStrings(steps []protoStepKind) []string {
	out := make([]string, len(steps))
	for i, step := range steps {
		out[i] = step.String()
	}
	return out
}

func replayProtoSim(steps []protoStepKind, hooks FaultHooks) (*protoSim, *Counterexample, error) {
	sim := newProtoSim()
	for idx, step := range steps {
		if cx, err := applyProtoStep(sim, step, hooks); err != nil {
			return nil, nil, err
		} else if cx != nil {
			cx.Trace = traceStrings(steps[:idx+1])
			return nil, cx, nil
		}
	}
	return sim, nil, nil
}

func applyProtoStep(sim *protoSim, step protoStepKind, hooks FaultHooks) (*Counterexample, error) {
	sim.stepCount++
	switch step {
	case stepSubmitLowRisk, stepSubmitMediumRisk, stepSubmitHighRisk:
		setpoint := 24.0
		switch step {
		case stepSubmitMediumRisk:
			setpoint = 25.0
		case stepSubmitHighRisk:
			setpoint = 26.0
		}
		sim.capability = commandmodel.CapabilitySetTemperatureSetpoint
		sim.targetSetpoint = setpoint
		req := makeSubmitRequest("formal-req", setpoint, sim.now)
		res, err := sim.service.Submit(req)
		if err != nil {
			return nil, fmt.Errorf("submit failed unexpectedly: %w", err)
		}
		if step == stepSubmitHighRisk && (res.Intent.Risk != commandmodel.RiskHigh || res.Intent.ApprovalPolicy != commandmodel.ApprovalNone || res.Intent.Status != commandmodel.IntentQueued) {
			return &Counterexample{
				Property: PropertyP1,
				Suite:    "SingleCommandProtocol",
				Summary:  fmt.Sprintf("high-risk submit did not enter QUEUED with ApprovalNone directly: risk=%s policy=%s status=%s", res.Intent.Risk, res.Intent.ApprovalPolicy, res.Intent.Status),
			}, nil
		}
		sim.commandID = res.Intent.ID

	case stepSubmitStartPulse, stepSubmitStopPulse, stepSubmitStartUnitSequence, stepSubmitStopUnitSequence:
		capability := commandmodel.CapabilityStart
		switch step {
		case stepSubmitStopPulse:
			capability = commandmodel.CapabilityStop
		case stepSubmitStartUnitSequence:
			capability = commandmodel.CapabilityStartUnitSequence
		case stepSubmitStopUnitSequence:
			capability = commandmodel.CapabilityStopUnitSequence
		}
		sim.capability = capability
		req := makeSubmitRequestForCapability("formal-req", capability, sim.now)
		res, err := sim.service.Submit(req)
		if err != nil {
			return nil, fmt.Errorf("submit %s failed unexpectedly: %w", capability, err)
		}
		if res.Intent.Risk != commandmodel.RiskHigh || res.Intent.ApprovalPolicy != commandmodel.ApprovalNone || res.Intent.Status != commandmodel.IntentQueued {
			return &Counterexample{
				Property: PropertyP1,
				Suite:    "SingleCommandProtocol",
				Summary:  fmt.Sprintf("%s submit did not enter QUEUED with ApprovalNone directly: risk=%s policy=%s status=%s", capability, res.Intent.Risk, res.Intent.ApprovalPolicy, res.Intent.Status),
			}, nil
		}
		sim.commandID = res.Intent.ID

	case stepApproveValid:
		approvalID := "apr-valid"
		approval := commandmodel.ApprovalEvidence{
			ApprovalID:         approvalID,
			ApproverID:         approverPrincipalID,
			Policy:             commandmodel.ApprovalSingleApprover,
			PayloadHash:        mustIntent(sim).PayloadHash,
			CapabilityRevision: setpointCapabilityRev,
			Risk:               mustIntent(sim).Risk,
			RiskRuleRevision:   mustIntent(sim).RiskSnapshot.RuleRevision,
			IssuedAt:           sim.now.Add(-time.Second),
			ExpiresAt:          sim.now.Add(grantLifetime),
			Authorization:      approveAuthorization(approverPrincipalID, approvalID, sim.now, commandmodel.RiskHigh),
		}
		if _, err := sim.service.Approve(commandmodel.ApproveRequest{
			CommandID: sim.commandID,
			TenantID:  baseTenantID,
			Approval:  approval,
		}); err != nil {
			return nil, fmt.Errorf("valid approve failed unexpectedly: %w", err)
		}

	case stepApproveSelfRejected:
		sim.selfApproveTried = true
		approval := commandmodel.ApprovalEvidence{
			ApprovalID:         "apr-self",
			ApproverID:         submitPrincipalID,
			Policy:             commandmodel.ApprovalSingleApprover,
			PayloadHash:        mustIntent(sim).PayloadHash,
			CapabilityRevision: setpointCapabilityRev,
			Risk:               mustIntent(sim).Risk,
			RiskRuleRevision:   mustIntent(sim).RiskSnapshot.RuleRevision,
			IssuedAt:           sim.now.Add(-time.Second),
			ExpiresAt:          sim.now.Add(grantLifetime),
			Authorization:      approveAuthorization(submitPrincipalID, "self", sim.now, commandmodel.RiskHigh),
		}
		if _, err := sim.service.Approve(commandmodel.ApproveRequest{
			CommandID: sim.commandID,
			TenantID:  baseTenantID,
			Approval:  approval,
		}); err == nil {
			return &Counterexample{
				Property: PropertyP1,
				Suite:    "SingleCommandProtocol",
				Summary:  "self-approval by requester was accepted",
			}, nil
		}

	case stepCancelIntent:
		intent := mustIntent(sim)
		cancelReq := commandmodel.CancelRequest{
			CommandID:     sim.commandID,
			TenantID:      baseTenantID,
			Reason:        "OPERATOR_CANCELLED",
			Authorization: submitAuthorizationFor(submitPrincipalID, intent.Capability, sim.now, commandmodel.RiskHigh),
		}
		if intent.Status != commandmodel.IntentAwaitingApproval && intent.Status != commandmodel.IntentQueued {
			sim.cancelDispatchTried = true
			if decision, err := commandservice.DecideCommandCancel(intent, cancelReq, sim.now); err == nil {
				return &Counterexample{
					Property: PropertyP3,
					Suite:    "SingleCommandProtocol",
					Summary:  fmt.Sprintf("cancel allowed non-pending status %s -> %s", intent.Status, decision.IntentFinal),
				}, nil
			}
			if _, err := sim.service.Cancel(cancelReq); err == nil {
				return &Counterexample{
					Property: PropertyP3,
					Suite:    "SingleCommandProtocol",
					Summary:  fmt.Sprintf("service.Cancel allowed status %s", intent.Status),
				}, nil
			}
			return nil, nil
		}
		decision, decErr := commandservice.DecideCommandCancel(intent, cancelReq, sim.now)
		if decErr != nil {
			return nil, fmt.Errorf("valid cancel decision failed: %w", decErr)
		}
		cancelled, err := sim.service.Cancel(cancelReq)
		if err != nil {
			return nil, fmt.Errorf("valid cancel failed unexpectedly: %w", err)
		}
		if cancelled.Status != commandmodel.IntentCancelled || decision.IntentFinal != commandmodel.IntentCancelled {
			return nil, fmt.Errorf("cancel did not transition to CANCELLED (intent=%s decision=%s)", cancelled.Status, decision.IntentFinal)
		}
		canClaim := commandservice.CanClaimQueuedIntent(cancelled, sim.deviceFrozen, nil, sim.now)
		_, prepErr := sim.service.PrepareDispatch(sim.commandID, "dispatcher-cancelled", sim.now.Add(dispatchLeaseWindow))
		if hooks.AllowDispatchCancelledIntent {
			canClaim = true
		}
		if canClaim || prepErr == nil {
			return &Counterexample{
				Property: PropertyP1,
				Suite:    "SingleCommandProtocol",
				Summary:  "cancelled intent remained claimable or dispatchable",
			}, nil
		}
		sim.commandID = "" // Cancelled terminal intent cleanly ends this protocol lifecycle.

	case stepPrepareDispatchValid:
		env, err := sim.service.PrepareDispatch(sim.commandID, "dispatcher-a", sim.now.Add(dispatchLeaseWindow))
		if err != nil {
			return nil, fmt.Errorf("prepare dispatch failed unexpectedly: %w", err)
		}
		sim.dispatchEnv = env
		sim.corrState = ""
		sim.publishesAttempt = 0
		sim.ackEdgeKind = ackEdgeDefault
		sim.hasVerifyEnv = false

	case stepPrepareDispatchExpiredGrant:
		sim.expiredClaimTry = true
		savedNow := sim.now
		sim.now = sim.now.Add(grantLifetime + 5*time.Second)
		defer func() {
			sim.now = savedNow
		}()
		intent := mustIntent(sim)
		canClaim := commandservice.CanClaimQueuedIntent(intent, sim.deviceFrozen, nil, sim.now)
		_, prepErr := sim.service.PrepareDispatch(sim.commandID, "dispatcher-expired", sim.now.Add(dispatchLeaseWindow))
		if hooks.BypassExpiredGrantOnClaim {
			canClaim = true
		}
		if canClaim || prepErr == nil {
			return &Counterexample{
				Property: PropertyP1,
				Suite:    "SingleCommandProtocol",
				Summary:  "expired authorization grant allowed queued intent dispatch",
			}, nil
		}

	case stepPrepareDispatchWhenFrozen:
		intent := mustIntent(sim)
		if commandservice.CanClaimQueuedIntent(intent, true, nil, sim.now) {
			return &Counterexample{
				Property: PropertyP3,
				Suite:    "SingleCommandProtocol",
				Summary:  "frozen device control group allowed queued claim",
			}, nil
		}

	case stepCorrelationPrepare:
		sim.corrState = commandmodel.CorrelationPrepared

	case stepCorrelationArm:
		sim.corrState = commandmodel.CorrelationMayCommit

	case stepPublishEdge:
		action := mqttconnector.ResumeActionForCorrelation(sim.corrState)
		if hooks.AllowPostArmRepublish && sim.corrState == commandmodel.CorrelationMayCommit {
			action = mqttconnector.ResumePublish
		}
		if action != mqttconnector.ResumePublish {
			return &Counterexample{
				Property: PropertyP2,
				Suite:    "SingleCommandProtocol",
				Summary:  fmt.Sprintf("correlation state %s allowed publish action %d", sim.corrState, action),
			}, nil
		}
		sim.publishesAttempt++
		if sim.publishesAttempt > 1 {
			return &Counterexample{
				Property: PropertyP2,
				Suite:    "SingleCommandProtocol",
				Summary:  "physical MQTT command published more than once for one attempt",
			}, nil
		}

	case stepRecoverCorrelation:
		action := mqttconnector.ResumeActionForCorrelation(sim.corrState)
		if hooks.AllowPostArmRepublish && sim.corrState == commandmodel.CorrelationMayCommit {
			action = mqttconnector.ResumePublish
		}
		if !checkResumeActionMatchesCorrelation(sim.corrState, action) {
			return &Counterexample{
				Property: PropertyP2,
				Suite:    "SingleCommandProtocol",
				Summary:  fmt.Sprintf("ResumeActionForCorrelation returned unsafe action %d for state %s", action, sim.corrState),
			}, nil
		}
		if sim.corrState == commandmodel.CorrelationMayCommit && action == mqttconnector.ResumeOutcomeUnknown {
			// Recovery after crash at MAY_COMMIT settles REQUEST_COMMITTED -> OUTCOME_UNKNOWN.
			err := sim.service.ResolveDispatch(sim.dispatchEnv, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorRequestCommitted,
				FailureCode: "MQTT_COMMAND_RECOVERED_MAY_COMMIT",
				EvidenceID:  "mqtt-recovered-may-commit",
			})
			if err != nil {
				return nil, fmt.Errorf("resolve may-commit failed: %w", err)
			}
			sim.deviceFrozen = !hooks.UnfreezeWithoutReconcile
			if !sim.deviceFrozen {
				return &Counterexample{
					Property: PropertyP3,
					Suite:    "SingleCommandProtocol",
					Summary:  "OUTCOME_UNKNOWN recovered from MAY_COMMIT failed to freeze device control group",
				}, nil
			}
		}

	case stepResolvePreSendRetry, stepResolvePreSendTerminal:
		code := "MQTT_ROUTE_UNAVAILABLE"
		if step == stepResolvePreSendTerminal {
			code = "COMMAND_CONTROL_DISABLED"
		}
		if err := sim.service.ResolveDispatch(sim.dispatchEnv, commandmodel.ConnectorResult{
			Phase:       commandmodel.ConnectorPreSendRejected,
			FailureCode: code,
			EvidenceID:  "ev-presend",
		}); err != nil {
			return nil, fmt.Errorf("resolve pre-send failed: %w", err)
		}

	case stepResolveExecRejected:
		if err := sim.service.ResolveDispatch(sim.dispatchEnv, commandmodel.ConnectorResult{
			Phase:       commandmodel.ConnectorExecutionRejected,
			FailureCode: "MQTT_EDGE_INTERLOCK_OPEN",
			EvidenceID:  "ev-exec-rejected",
		}); err != nil {
			return nil, fmt.Errorf("resolve exec-rejected failed: %w", err)
		}
		sim.corrState = commandmodel.CorrelationResolved

	case stepResolveRequestCommitted:
		if err := sim.service.ResolveDispatch(sim.dispatchEnv, commandmodel.ConnectorResult{
			Phase:       commandmodel.ConnectorRequestCommitted,
			FailureCode: "MQTT_COMMAND_REPLY_TIMEOUT",
			EvidenceID:  "ev-req-committed",
		}); err != nil {
			return nil, fmt.Errorf("resolve request-committed failed: %w", err)
		}
		sim.deviceFrozen = !hooks.UnfreezeWithoutReconcile
		if !sim.deviceFrozen {
			return &Counterexample{
				Property: PropertyP3,
				Suite:    "SingleCommandProtocol",
				Summary:  "REQUEST_COMMITTED settled OUTCOME_UNKNOWN without freezing device control group",
			}, nil
		}

	case stepResolveAckValid, stepResolveAckPulseDualReadback, stepResolveAckEmergencyDoRelease, stepResolveAckUnitSequenceAccepted:
		edgeKind := ackEdgeDefault
		switch step {
		case stepResolveAckPulseDualReadback:
			edgeKind = ackEdgePulseDual
		case stepResolveAckEmergencyDoRelease:
			edgeKind = ackEdgeEmergencyDoRelease
		case stepResolveAckUnitSequenceAccepted:
			edgeKind = ackEdgeUnitSequenceAccepted
		}
		res := commandmodel.ConnectorResult{
			Phase:         commandmodel.ConnectorAcknowledged,
			Acknowledged:  true,
			Verified:      false,
			EvidenceID:    "ev-ack-" + sim.dispatchEnv.AttemptID,
			EdgeExecution: protoEdgeExecution(sim, edgeKind),
		}
		if err := sim.service.ResolveDispatch(sim.dispatchEnv, res); err != nil {
			return nil, fmt.Errorf("resolve valid ack failed: %w", err)
		}
		sim.ackEdgeKind = edgeKind
		sim.corrState = commandmodel.CorrelationResolved
		intent := mustIntent(sim)
		if intent.Status != commandmodel.IntentDispatching || isIntentTerminal(intent.Status) {
			return &Counterexample{
				Property: PropertyP4,
				Suite:    "SingleCommandProtocol",
				Summary:  fmt.Sprintf("ACK resolved intent prematurely to %s without readback verification", intent.Status),
			}, nil
		}

	case stepResolveAckMissingEdge:
		sim.missingEdgeTried = true
		res := commandmodel.ConnectorResult{
			Phase:        commandmodel.ConnectorAcknowledged,
			Acknowledged: true,
			EvidenceID:   "ev-ack-missing-edge",
		}
		err := sim.service.ResolveDispatch(sim.dispatchEnv, res)
		acknowledgedWithoutEdge := err == nil
		if hooks.AllowAckWithoutEdgeExecution {
			acknowledgedWithoutEdge = true
		}
		if acknowledgedWithoutEdge && (res.EdgeExecution == nil || !res.EdgeExecution.ValidExecuted()) {
			return &Counterexample{
				Property: PropertyP4,
				Suite:    "SingleCommandProtocol",
				Summary:  "ConnectorAcknowledged transitioned without valid EdgeExecution evidence",
			}, nil
		}

	case stepResolveDispatchStaleFence:
		sim.staleAttemptUsed = true
		staleEnv := sim.dispatchEnv
		if staleEnv.CommandID == "" {
			staleEnv.CommandID = sim.commandID
		}
		err := sim.service.ResolveDispatch(staleEnv, commandmodel.ConnectorResult{
			Phase:         commandmodel.ConnectorAcknowledged,
			Acknowledged:  true,
			EvidenceID:    "ev-stale",
			EdgeExecution: validEdgeExecution(24),
		})
		if !errors.Is(err, commandservice.ErrStaleFence) {
			return &Counterexample{
				Property: PropertyP5,
				Suite:    "SingleCommandProtocol",
				Summary:  fmt.Sprintf("ResolveDispatch accepted stale or already-resolved attempt (%v)", err),
			}, nil
		}

	case stepPrepareVerification:
		sim.now = sim.now.Add(time.Second)
		vEnv, err := sim.service.PrepareVerification(sim.commandID, "verifier-a", sim.now.Add(30*time.Second))
		if err != nil {
			return nil, fmt.Errorf("prepare verification failed: %w", err)
		}
		sim.verifyEnv = vEnv
		sim.hasVerifyEnv = true

	case stepResolveVerificationValidInWindow:
		sim.now = sim.now.Add(2 * time.Second)
		vRes := commandmodel.VerificationResult{
			Outcome:    commandmodel.VerificationSucceeded,
			EvidenceID: "ev-verify-ok",
			Reported:   protoReportedEvidence(sim, true),
		}
		if err := sim.service.ResolveVerification(sim.verifyEnv, vRes); err != nil {
			return nil, fmt.Errorf("resolve verification in window failed: %w", err)
		}
		sim.hasVerifyEnv = false
		if mustIntent(sim).Status != commandmodel.IntentSucceeded {
			return nil, fmt.Errorf("valid in-window verification did not succeed")
		}

	case stepResolveVerificationMismatch:
		sim.now = sim.now.Add(2 * time.Second)
		vRes := commandmodel.VerificationResult{
			Outcome:    commandmodel.VerificationMismatch,
			EvidenceID: "ev-verify-mismatch",
			Reported:   protoReportedEvidence(sim, false),
		}
		if err := sim.service.ResolveVerification(sim.verifyEnv, vRes); err != nil {
			return nil, fmt.Errorf("resolve verification mismatch failed: %w", err)
		}
		sim.hasVerifyEnv = false
		if cx := verifyUnconfirmedProtoSettlement(sim, mustIntent(sim), hooks, "verification mismatch"); cx != nil {
			return cx, nil
		}

	case stepResolveVerificationValidPastDeadline:
		sim.now = sim.verifyEnv.VerificationDeadline.Add(time.Second)
		vRes := commandmodel.VerificationResult{
			Outcome:    commandmodel.VerificationSucceeded,
			EvidenceID: "ev-verify-late",
			Reported:   protoReportedEvidence(sim, true),
		}
		if err := sim.service.ResolveVerification(sim.verifyEnv, vRes); err != nil {
			return nil, fmt.Errorf("resolve verification past deadline returned err: %w", err)
		}
		sim.hasVerifyEnv = false
		intent := mustIntent(sim)
		settledStatus := intent.Status
		if hooks.IgnoreVerificationDeadline {
			settledStatus = commandmodel.IntentSucceeded
		}
		if sim.now.After(sim.verifyEnv.VerificationDeadline) && settledStatus == commandmodel.IntentSucceeded {
			return &Counterexample{
				Property: PropertyP5,
				Suite:    "SingleCommandProtocol",
				Summary:  "verification settled IntentSucceeded after VerificationDeadline expired",
			}, nil
		}
		if cx := verifyUnconfirmedProtoSettlement(sim, intent, hooks, "expired verification"); cx != nil {
			return cx, nil
		}

	case stepReconcileApplied, stepReconcileNotApplied:
		outcome := commandmodel.ReconciledApplied
		if step == stepReconcileNotApplied {
			outcome = commandmodel.ReconciledNotApplied
		}
		intent := mustIntent(sim)
		req := commandmodel.ReconcileRequest{
			CommandID:     sim.commandID,
			TenantID:      baseTenantID,
			Outcome:       outcome,
			Authorization: submitAuthorizationFor(reconcilePrincipalID, intent.Capability, sim.now, commandmodel.RiskHigh),
		}
		if intent.Status != commandmodel.IntentOutcomeUnknown {
			decision, err := commandservice.DecideOperatorReconcile(intent, req, sim.now)
			if err == nil {
				return &Counterexample{
					Property: PropertyP3,
					Suite:    "SingleCommandProtocol",
					Summary:  fmt.Sprintf("reconciliation allowed non-OUTCOME_UNKNOWN intent %s -> %s", intent.Status, decision.IntentFinal),
				}, nil
			}
			return nil, nil
		}
		decision, err := commandservice.DecideOperatorReconcile(intent, req, sim.now)
		if err != nil {
			return nil, fmt.Errorf("valid reconcile failed: %w", err)
		}
		if decision.ReleaseControlGroup {
			sim.deviceFrozen = false
		}
		// Reflect reconciliation locally so subsequent BFS transitions observe terminal state.
		if sim.deviceFrozen {
			return &Counterexample{
				Property: PropertyP3,
				Suite:    "SingleCommandProtocol",
				Summary:  "operator reconciliation failed to release device freeze",
			}, nil
		}
		sim.commandID = "" // Settled terminal intent cleanly ends this protocol lifecycle.
	}
	return nil, nil
}

func mustIntent(sim *protoSim) commandmodel.CommandIntent {
	intent, _ := sim.service.Get(sim.commandID)
	return intent
}

func protoEdgeExecution(sim *protoSim, edgeKind protoAckEdgeKind) *commandmodel.EdgeExecutionEvidence {
	switch edgeKind {
	case ackEdgePulseDual:
		return validPulseEdgeExecution("u14", 500)
	case ackEdgeEmergencyDoRelease:
		return validEmergencyDoReleaseEdgeExecution()
	case ackEdgeUnitSequenceAccepted:
		return validUnitSequenceAcceptedEdgeExecution("unit-a")
	}
	switch sim.capability {
	case commandmodel.CapabilityStart, commandmodel.CapabilityStop, commandmodel.CapabilityStartUnitSequence, commandmodel.CapabilityStopUnitSequence:
		return validLevelEdgeExecution("u11", 1)
	default:
		target := 24.0
		if sim.targetSetpoint > 0 {
			target = sim.targetSetpoint
		}
		return validEdgeExecution(target)
	}
}

func protoReportedEvidence(sim *protoSim, match bool) commandmodel.ReportedStateEvidence {
	observedAt := sim.verifyEnv.AcknowledgedAt.Add(time.Second)
	revision := sim.verifyEnv.BaselineBusinessRevision + 1
	switch sim.capability {
	case commandmodel.CapabilityStart, commandmodel.CapabilityStartUnitSequence:
		if match {
			return validReportedTextEvidence("RUNNING", observedAt, revision)
		}
		return validReportedTextEvidence("STOPPED", observedAt, revision)
	case commandmodel.CapabilityStop, commandmodel.CapabilityStopUnitSequence:
		if match {
			return validReportedTextEvidence("STOPPED", observedAt, revision)
		}
		return validReportedTextEvidence("RUNNING", observedAt, revision)
	default:
		target := 24.0
		if sim.targetSetpoint > 0 {
			target = sim.targetSetpoint
		}
		if !match {
			target = 21
		}
		return validReportedEvidence(target, observedAt, revision)
	}
}

func lastTransitionReason(intent commandmodel.CommandIntent) string {
	if len(intent.Transitions) == 0 {
		return ""
	}
	return intent.Transitions[len(intent.Transitions)-1].Reason
}

func protoEdgeEligibleWithoutPowerConfirm(capability commandmodel.Capability, edgeKind protoAckEdgeKind) bool {
	switch capability {
	case commandmodel.CapabilityStart:
		return edgeKind == ackEdgePulseDual
	case commandmodel.CapabilityStop:
		return edgeKind == ackEdgePulseDual || edgeKind == ackEdgeEmergencyDoRelease
	case commandmodel.CapabilityStartUnitSequence, commandmodel.CapabilityStopUnitSequence:
		return edgeKind == ackEdgeUnitSequenceAccepted
	default:
		return false
	}
}

func verifyUnconfirmedProtoSettlement(sim *protoSim, intent commandmodel.CommandIntent, hooks FaultHooks, contextLabel string) *Counterexample {
	if protoEdgeEligibleWithoutPowerConfirm(intent.Capability, sim.ackEdgeKind) {
		reason := lastTransitionReason(intent)
		if intent.Status != commandmodel.IntentFailed || sim.deviceFrozen || reason != commandmodel.ReasonPulseDeliveredPowerUnconfirmed {
			return &Counterexample{
				Property: PropertyP3,
				Suite:    "SingleCommandProtocol",
				Summary:  fmt.Sprintf("%s on %s with edgeKind=%d must fail without freeze (%s/%s frozen=%v)", contextLabel, intent.Capability, sim.ackEdgeKind, intent.Status, reason, sim.deviceFrozen),
			}
		}
		followup := makeSyntheticIntentForCapability(2, commandmodel.CapabilityStop, commandmodel.IntentQueued, true, sim.now)
		if !commandservice.CanClaimQueuedIntent(followup, sim.deviceFrozen, []commandmodel.IntentStatus{intent.Status}, sim.now) {
			return &Counterexample{
				Property: PropertyP3,
				Suite:    "SingleCommandProtocol",
				Summary:  fmt.Sprintf("follow-up START/STOP deadlocked after %s failed without freeze", intent.Capability),
			}
		}
		return nil
	}
	sim.deviceFrozen = !hooks.UnfreezeWithoutReconcile
	if intent.Status != commandmodel.IntentOutcomeUnknown || !sim.deviceFrozen {
		return &Counterexample{
			Property: PropertyP3,
			Suite:    "SingleCommandProtocol",
			Summary:  fmt.Sprintf("%s did not enter frozen OUTCOME_UNKNOWN: capability=%s status=%s frozen=%v", contextLabel, intent.Capability, intent.Status, sim.deviceFrozen),
		}
	}
	return nil
}

func candidateProtoSteps(sim *protoSim) []protoStepKind {
	if sim.commandID == "" {
		// Distinguish initial unsubmitted state from settled protocol completion.
		if sim.stepCount > 0 {
			return nil
		}
		return []protoStepKind{
			stepSubmitLowRisk,
			stepSubmitMediumRisk,
			stepSubmitHighRisk,
			stepSubmitStartPulse,
			stepSubmitStopPulse,
			stepSubmitStartUnitSequence,
			stepSubmitStopUnitSequence,
		}
	}
	intent := mustIntent(sim)
	var latestAttemptStatus commandmodel.AttemptStatus
	if len(intent.Attempts) > 0 {
		latestAttemptStatus = intent.Attempts[len(intent.Attempts)-1].Status
	}
	out := make([]protoStepKind, 0, 8)
	switch intent.Status {
	case commandmodel.IntentAwaitingApproval:
		if !sim.selfApproveTried {
			out = append(out, stepApproveSelfRejected)
		}
		out = append(out, stepApproveValid, stepCancelIntent)

	case commandmodel.IntentQueued:
		if len(intent.Attempts) < maxAttemptsPerCommand {
			out = append(out, stepPrepareDispatchValid)
			if !sim.expiredClaimTry {
				out = append(out, stepPrepareDispatchExpiredGrant)
			}
		}
		out = append(out, stepPrepareDispatchWhenFrozen, stepCancelIntent)

	case commandmodel.IntentDispatching:
		if !sim.cancelDispatchTried {
			out = append(out, stepCancelIntent)
		}
		switch latestAttemptStatus {
		case commandmodel.AttemptPrepared:
			switch sim.corrState {
			case "":
				out = append(out, stepCorrelationPrepare)
			case commandmodel.CorrelationPrepared:
				if sim.publishesAttempt == 0 {
					out = append(out, stepCorrelationArm, stepPublishEdge)
				}
				out = append(out, stepResolvePreSendRetry, stepResolvePreSendTerminal)
				if !sim.missingEdgeTried {
					out = append(out, stepResolveAckMissingEdge)
				}
			case commandmodel.CorrelationMayCommit:
				out = append(out,
					stepRecoverCorrelation,
					stepResolveExecRejected,
					stepResolveRequestCommitted,
					stepResolveAckValid,
				)
				if sim.capability == commandmodel.CapabilityStart || sim.capability == commandmodel.CapabilityStop ||
					sim.capability == commandmodel.CapabilityStartUnitSequence || sim.capability == commandmodel.CapabilityStopUnitSequence {
					out = append(out, stepResolveAckPulseDualReadback, stepResolveAckUnitSequenceAccepted)
				}
				if sim.capability == commandmodel.CapabilityStart || sim.capability == commandmodel.CapabilityStop {
					out = append(out, stepResolveAckEmergencyDoRelease)
				}
			}
		case commandmodel.AttemptAcknowledged:
			if !sim.staleAttemptUsed {
				out = append(out, stepResolveDispatchStaleFence)
			}
			if !sim.hasVerifyEnv {
				out = append(out, stepPrepareVerification)
			} else {
				out = append(out,
					stepResolveVerificationValidInWindow,
					stepResolveVerificationMismatch,
					stepResolveVerificationValidPastDeadline,
				)
			}
		}

	case commandmodel.IntentOutcomeUnknown:
		if !sim.staleAttemptUsed {
			out = append(out, stepResolveDispatchStaleFence)
		}
		out = append(out, stepReconcileApplied, stepReconcileNotApplied)
	}
	return out
}

// CheckSingleCommandProtocol performs an exhaustive BFS over all reachable states of a
// single CommandIntent, its dispatch attempt, MQTT correlation, readback verification, and reconciliation.
func CheckSingleCommandProtocol(hooks FaultHooks) (SuiteReport, error) {
	initial := protoNode{key: protoStateKey{}}
	queue := []protoNode{initial}
	visited := map[protoStateKey]bool{initial.key: true}
	transitions := 0

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		sim, cx, err := replayProtoSim(node.steps, hooks)
		if err != nil {
			return SuiteReport{}, err
		}
		if cx != nil {
			return SuiteReport{
				Suite:          "SingleCommandProtocol",
				VisitedStates:  len(visited),
				Transitions:    transitions,
				Passed:         false,
				Counterexample: cx,
			}, nil
		}

		for _, nextStep := range candidateProtoSteps(sim) {
			nextSteps := make([]protoStepKind, len(node.steps)+1)
			copy(nextSteps, node.steps)
			nextSteps[len(node.steps)] = nextStep
			transitions++

			nextSim, nextCX, err := replayProtoSim(nextSteps, hooks)
			if err != nil {
				return SuiteReport{}, err
			}
			if nextCX != nil {
				return SuiteReport{
					Suite:          "SingleCommandProtocol",
					VisitedStates:  len(visited),
					Transitions:    transitions,
					Passed:         false,
					Counterexample: nextCX,
				}, nil
			}
			nextKey := nextSim.key()
			if !visited[nextKey] {
				visited[nextKey] = true
				queue = append(queue, protoNode{key: nextKey, steps: nextSteps})
			}
		}
	}

	return SuiteReport{
		Suite:         "SingleCommandProtocol",
		VisitedStates: len(visited),
		Transitions:   transitions,
		Passed:        true,
	}, nil
}

type multiCmdState struct {
	c1Status           commandmodel.IntentStatus
	c2Status           commandmodel.IntentStatus
	c1Attempts         uint8
	c2Attempts         uint8
	c1GrantValid       bool
	c2GrantValid       bool
	c1PulseUnconfirmed bool
	deviceFrozen       bool
}

type multiCmdNode struct {
	state multiCmdState
	trace []string
}

func makeSyntheticIntent(seq uint64, status commandmodel.IntentStatus, grantValid bool, now time.Time) commandmodel.CommandIntent {
	return makeSyntheticIntentForCapability(seq, commandmodel.CapabilitySetTemperatureSetpoint, status, grantValid, now)
}

func makeSyntheticIntentForCapability(seq uint64, capability commandmodel.Capability, status commandmodel.IntentStatus, grantValid bool, now time.Time) commandmodel.CommandIntent {
	issuedAt := now
	if !grantValid {
		issuedAt = now.Add(-(grantLifetime + 5*time.Second))
	}
	profile, ok := commandmodel.CapabilityProfileFor(capability)
	revision := setpointCapabilityRev
	var params commandmodel.CommandParameters
	if ok {
		revision = profile.Revision
		if profile.ParameterKey == commandmodel.ParameterSetpointC {
			params = commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24}
		}
	}
	return commandmodel.CommandIntent{
		ID:                    fmt.Sprintf("cmd-%d", seq),
		TenantID:              baseTenantID,
		SiteID:                baseSiteID,
		DeviceID:              baseDeviceID,
		PointID:               basePointID,
		PrincipalID:           submitPrincipalID,
		Capability:            capability,
		CapabilityRevision:    revision,
		Risk:                  commandmodel.RiskHigh,
		ApprovalPolicy:        commandmodel.ApprovalNone,
		Parameters:            params,
		VerificationPointKey:  setpointVerificationKey,
		SnapshotRevision:      17,
		DeviceCommandSequence: seq,
		Status:                status,
		Authorization:         submitAuthorizationFor(submitPrincipalID, capability, issuedAt, commandmodel.RiskHigh),
	}
}

func assertMultiCmdInvariants(state multiCmdState, trace []string) *Counterexample {
	if (!state.c1GrantValid && state.c1Status == commandmodel.IntentDispatching) || (!state.c2GrantValid && state.c2Status == commandmodel.IntentDispatching) {
		return &Counterexample{
			Property: PropertyP1,
			Suite:    "MultiCommandDeviceExclusivity",
			Summary:  "a command entered DISPATCHING after its authorization grant expired",
			Trace:    trace,
		}
	}
	if state.c1Status == commandmodel.IntentDispatching && state.c2Status == commandmodel.IntentDispatching {
		return &Counterexample{
			Property: PropertyP3,
			Suite:    "MultiCommandDeviceExclusivity",
			Summary:  "two commands were concurrently DISPATCHING on the same device",
			Trace:    trace,
		}
	}
	if commandservice.IsBlockingEarlierIntentStatus(state.c1Status) && state.c2Status == commandmodel.IntentDispatching {
		return &Counterexample{
			Property: PropertyP3,
			Suite:    "MultiCommandDeviceExclusivity",
			Summary:  fmt.Sprintf("cmd-2 entered DISPATCHING while earlier cmd-1 was still %s", state.c1Status),
			Trace:    trace,
		}
	}
	if state.deviceFrozen && (state.c1Status == commandmodel.IntentDispatching || state.c2Status == commandmodel.IntentDispatching) {
		return &Counterexample{
			Property: PropertyP3,
			Suite:    "MultiCommandDeviceExclusivity",
			Summary:  "a command entered DISPATCHING while the device control group was frozen",
			Trace:    trace,
		}
	}
	if (state.c1Status == commandmodel.IntentOutcomeUnknown || state.c2Status == commandmodel.IntentOutcomeUnknown) && !state.deviceFrozen {
		return &Counterexample{
			Property: PropertyP3,
			Suite:    "MultiCommandDeviceExclusivity",
			Summary:  "an intent settled OUTCOME_UNKNOWN without freezing the device control group",
			Trace:    trace,
		}
	}
	return nil
}

// CheckMultiCommandDeviceExclusivity verifies single-flight device serialization, blocking
// earlier-intent ordering, and freeze containment across two queued intents on one Device.
func CheckMultiCommandDeviceExclusivity(hooks FaultHooks) (SuiteReport, error) {
	initial := multiCmdState{
		c1Status:     commandmodel.IntentQueued,
		c2Status:     commandmodel.IntentQueued,
		c1GrantValid: true,
		c2GrantValid: true,
	}
	queue := []multiCmdNode{{state: initial}}
	visited := map[multiCmdState]bool{initial: true}
	transitions := 0
	now := baseModelTime
	sawPulseUnconfirmedThenNextSucceeded := false

	enqueue := func(next multiCmdState, action string, parentTrace []string) (*Counterexample, error) {
		transitions++
		trace := make([]string, len(parentTrace)+1)
		copy(trace, parentTrace)
		trace[len(parentTrace)] = action
		if cx := assertMultiCmdInvariants(next, trace); cx != nil {
			return cx, nil
		}
		if !visited[next] {
			visited[next] = true
			queue = append(queue, multiCmdNode{state: next, trace: trace})
		}
		return nil, nil
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		st := cur.state

		// Try claiming cmd 1 (no earlier commands).
		if st.c1Status == commandmodel.IntentQueued && st.c1Attempts < maxAttemptsPerCommand {
			i1 := makeSyntheticIntentForCapability(1, commandmodel.CapabilityStart, st.c1Status, st.c1GrantValid, now)
			canClaim := commandservice.CanClaimQueuedIntent(i1, st.deviceFrozen, nil, now)
			if hooks.BypassExpiredGrantOnClaim && !st.c1GrantValid {
				canClaim = true
			}
			if canClaim {
				next := st
				next.c1Status = commandmodel.IntentDispatching
				next.c1Attempts++
				if cx, err := enqueue(next, "Claim(cmd-1)", cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return SuiteReport{Suite: "MultiCommandDeviceExclusivity", VisitedStates: len(visited), Transitions: transitions, Passed: false, Counterexample: cx}, nil
				}
			}
		}

		// Try claiming cmd 2 (gated by cmd 1 status).
		if st.c2Status == commandmodel.IntentQueued && st.c2Attempts < maxAttemptsPerCommand {
			i2 := makeSyntheticIntentForCapability(2, commandmodel.CapabilityStop, st.c2Status, st.c2GrantValid, now)
			if commandservice.CanClaimQueuedIntent(i2, st.deviceFrozen, []commandmodel.IntentStatus{st.c1Status}, now) {
				next := st
				next.c2Status = commandmodel.IntentDispatching
				next.c2Attempts++
				if cx, err := enqueue(next, "Claim(cmd-2)", cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return SuiteReport{Suite: "MultiCommandDeviceExclusivity", VisitedStates: len(visited), Transitions: transitions, Passed: false, Counterexample: cx}, nil
				}
			}
		}

		// Grant expiration events.
		if st.c1GrantValid && st.c1Status == commandmodel.IntentQueued {
			next := st
			next.c1GrantValid = false
			if cx, err := enqueue(next, "ExpireGrant(cmd-1)", cur.trace); err != nil {
				return SuiteReport{}, err
			} else if cx != nil {
				return SuiteReport{Suite: "MultiCommandDeviceExclusivity", VisitedStates: len(visited), Transitions: transitions, Passed: false, Counterexample: cx}, nil
			}
		}

		// Cancel pending/queued commands before dispatch and confirm cancellation unblocks later queued intents.
		for cmdNum := 1; cmdNum <= 2; cmdNum++ {
			status := st.c1Status
			grantValid := st.c1GrantValid
			if cmdNum == 2 {
				status = st.c2Status
				grantValid = st.c2GrantValid
			}
			if status == commandmodel.IntentCancelled {
				i := makeSyntheticIntent(uint64(cmdNum), status, grantValid, now)
				canClaim := commandservice.CanClaimQueuedIntent(i, st.deviceFrozen, nil, now)
				if hooks.AllowDispatchCancelledIntent {
					canClaim = true
				}
				if canClaim {
					trace := append(append([]string(nil), cur.trace...), fmt.Sprintf("ClaimCancelled(cmd-%d)", cmdNum))
					return SuiteReport{
						Suite:         "MultiCommandDeviceExclusivity",
						VisitedStates: len(visited),
						Transitions:   transitions,
						Passed:        false,
						Counterexample: &Counterexample{
							Property: PropertyP1,
							Suite:    "MultiCommandDeviceExclusivity",
							Summary:  fmt.Sprintf("cancelled cmd-%d remained claimable for dispatch", cmdNum),
							Trace:    trace,
						},
					}, nil
				}
				continue
			}
			if !grantValid || (status != commandmodel.IntentAwaitingApproval && status != commandmodel.IntentQueued) {
				continue
			}
			cancelCap := commandmodel.CapabilityStart
			if cmdNum == 2 {
				cancelCap = commandmodel.CapabilityStop
			}
			decision, err := commandservice.DecideCommandCancel(
				makeSyntheticIntentForCapability(uint64(cmdNum), cancelCap, status, grantValid, now),
				commandmodel.CancelRequest{
					CommandID:     fmt.Sprintf("cmd-%d", cmdNum),
					TenantID:      baseTenantID,
					Reason:        "OPERATOR_CANCELLED",
					Authorization: submitAuthorizationFor(submitPrincipalID, cancelCap, now, commandmodel.RiskHigh),
				},
				now,
			)
			if err != nil {
				return SuiteReport{}, err
			}
			next := st
			if cmdNum == 1 {
				next.c1Status = decision.IntentFinal
			} else {
				next.c2Status = decision.IntentFinal
			}
			if cx, err := enqueue(next, fmt.Sprintf("Cancel(cmd-%d)", cmdNum), cur.trace); err != nil {
				return SuiteReport{}, err
			} else if cx != nil {
				return SuiteReport{Suite: "MultiCommandDeviceExclusivity", VisitedStates: len(visited), Transitions: transitions, Passed: false, Counterexample: cx}, nil
			}
		}

		// Resolve dispatching outcomes for cmd 1 & cmd 2 using production DecideDispatchResolution / DecideVerificationResolution.
		for cmdNum := 1; cmdNum <= 2; cmdNum++ {
			status := st.c1Status
			cmdCap := commandmodel.CapabilityStart
			seqCap := commandmodel.CapabilityStartUnitSequence
			expectedReported := "RUNNING"
			if cmdNum == 2 {
				status = st.c2Status
				cmdCap = commandmodel.CapabilityStop
				seqCap = commandmodel.CapabilityStopUnitSequence
				expectedReported = "STOPPED"
			}
			if status != commandmodel.IntentDispatching {
				continue
			}
			outcomes := []struct {
				label string
				res   commandmodel.ConnectorResult
			}{
				{label: "PreSendRetry", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "MQTT_ROUTE_UNAVAILABLE"}},
				{label: "PreSendTerminal", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "COMMAND_CONTROL_DISABLED"}},
				{label: "ExecRejected", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorExecutionRejected, FailureCode: "MQTT_EDGE_INTERLOCK_OPEN"}},
				{label: "RequestCommitted", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorRequestCommitted, FailureCode: "MQTT_COMMAND_REPLY_TIMEOUT"}},
				{label: "AckVerified", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validPulseEdgeExecution("u14", 500)}},
				{label: "AckPulseExpired", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validPulseEdgeExecution("u14", 500)}},
				{label: "AckPulseLateVerified", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validPulseEdgeExecution("u14", 500)}},
				{label: "AckEmergencyDoExpired", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validEmergencyDoReleaseEdgeExecution()}},
				{label: "AckEmergencyDoLateVerified", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validEmergencyDoReleaseEdgeExecution()}},
				{label: "AckStartEmergencyDoMismatchExpired", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validEmergencyDoReleaseEdgeExecution()}},
				{label: "AckUnitSequenceAcceptedExpired", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validUnitSequenceAcceptedEdgeExecution("unit-a")}},
				{label: "AckUnitSequenceAcceptedLateVerified", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validUnitSequenceAcceptedEdgeExecution("unit-a")}},
				{label: "AckLevelExpired", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validLevelEdgeExecution("u11", 1)}},
				{label: "AckUnitSequencePulseExpired", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validPulseEdgeExecution("u14", 500)}},
			}
			for _, outcome := range outcomes {
				evalCap := cmdCap
				switch outcome.label {
				case "AckEmergencyDoExpired", "AckEmergencyDoLateVerified":
					if cmdCap != commandmodel.CapabilityStop {
						continue
					}
				case "AckStartEmergencyDoMismatchExpired":
					if cmdCap != commandmodel.CapabilityStart {
						continue
					}
				case "AckUnitSequencePulseExpired", "AckUnitSequenceAcceptedExpired", "AckUnitSequenceAcceptedLateVerified":
					evalCap = seqCap
				}
				decision, err := commandservice.DecideDispatchResolution(evalCap, outcome.res, now)
				if err != nil {
					return SuiteReport{}, err
				}
				next := st
				targetStatus := decision.IntentFinal
				frozen := decision.FreezeControlGroup
				switch outcome.label {
				case "AckVerified":
					vDec, vErr := commandservice.DecideVerificationResolutionWithEdge(
						makeSyntheticIntentForCapability(uint64(cmdNum), evalCap, commandmodel.IntentDispatching, true, now),
						commandmodel.VerificationEnvelope{
							VerificationPointKey:     setpointVerificationKey,
							BaselineBusinessRevision: 17,
							AcknowledgedAt:           now,
							EdgeExecution:            outcome.res.EdgeExecution,
						},
						outcome.res.EdgeExecution,
						commandmodel.VerificationResult{
							Outcome:    commandmodel.VerificationSucceeded,
							EvidenceID: "ev-multi",
							Reported:   validReportedTextEvidence(expectedReported, now.Add(time.Second), 18),
						},
						now.Add(commandmodel.VerificationWindowFor(evalCap)),
						now.Add(2*time.Second),
					)
					if vErr != nil {
						return SuiteReport{}, vErr
					}
					targetStatus = vDec.IntentFinal
					frozen = vDec.FreezeControlGroup
				case "AckPulseExpired", "AckPulseLateVerified",
					"AckEmergencyDoExpired", "AckEmergencyDoLateVerified",
					"AckStartEmergencyDoMismatchExpired",
					"AckUnitSequenceAcceptedExpired", "AckUnitSequenceAcceptedLateVerified",
					"AckLevelExpired", "AckUnitSequencePulseExpired":
					deadline := now.Add(commandmodel.VerificationWindowFor(evalCap))
					pastDeadline := deadline.Add(time.Second)
					vRes := commandmodel.VerificationResult{
						Outcome:    commandmodel.VerificationTimedOut,
						EvidenceID: "ev-multi-expired",
					}
					lateVerified := outcome.label == "AckPulseLateVerified" ||
						outcome.label == "AckEmergencyDoLateVerified" ||
						outcome.label == "AckUnitSequenceAcceptedLateVerified"
					if lateVerified {
						vRes = commandmodel.VerificationResult{
							Outcome:    commandmodel.VerificationSucceeded,
							EvidenceID: "ev-multi-late-verified",
							Reported:   validReportedTextEvidence(expectedReported, now.Add(time.Second), 18),
						}
					}
					vDec, vErr := commandservice.DecideVerificationResolutionWithEdge(
						makeSyntheticIntentForCapability(uint64(cmdNum), evalCap, commandmodel.IntentDispatching, true, now),
						commandmodel.VerificationEnvelope{
							VerificationPointKey:     setpointVerificationKey,
							BaselineBusinessRevision: 17,
							AcknowledgedAt:           now,
							EdgeExecution:            outcome.res.EdgeExecution,
						},
						outcome.res.EdgeExecution,
						vRes,
						deadline,
						pastDeadline,
					)
					if vErr != nil {
						return SuiteReport{}, vErr
					}
					if lateVerified && (hooks.IgnoreVerificationDeadline || vDec.IntentFinal == commandmodel.IntentSucceeded) {
						trace := append(append([]string(nil), cur.trace...), fmt.Sprintf("Resolve(cmd-%d,%s)", cmdNum, outcome.label))
						return SuiteReport{
							Suite:         "MultiCommandDeviceExclusivity",
							VisitedStates: len(visited),
							Transitions:   transitions,
							Passed:        false,
							Counterexample: &Counterexample{
								Property: PropertyP5,
								Suite:    "MultiCommandDeviceExclusivity",
								Summary:  "verification settled SUCCEEDED after 10m VerificationDeadline expired",
								Trace:    trace,
							},
						}, nil
					}
					targetStatus = vDec.IntentFinal
					frozen = vDec.FreezeControlGroup
					unconfirmedNoFreeze := outcome.label == "AckPulseExpired" ||
						outcome.label == "AckPulseLateVerified" ||
						outcome.label == "AckEmergencyDoExpired" ||
						outcome.label == "AckEmergencyDoLateVerified" ||
						outcome.label == "AckUnitSequenceAcceptedExpired" ||
						outcome.label == "AckUnitSequenceAcceptedLateVerified"
					if unconfirmedNoFreeze {
						if targetStatus != commandmodel.IntentFailed || vDec.Reason != commandmodel.ReasonPulseDeliveredPowerUnconfirmed || frozen {
							trace := append(append([]string(nil), cur.trace...), fmt.Sprintf("Resolve(cmd-%d,%s)", cmdNum, outcome.label))
							return SuiteReport{
								Suite:         "MultiCommandDeviceExclusivity",
								VisitedStates: len(visited),
								Transitions:   transitions,
								Passed:        false,
								Counterexample: &Counterexample{
									Property: PropertyP3,
									Suite:    "MultiCommandDeviceExclusivity",
									Summary:  fmt.Sprintf("edge-confirmed unverified resolution violated contract (%s): status=%s reason=%s frozen=%v", outcome.label, targetStatus, vDec.Reason, frozen),
									Trace:    trace,
								},
							}, nil
						}
						if cmdNum == 1 {
							next.c1PulseUnconfirmed = true
						}
					} else if targetStatus != commandmodel.IntentOutcomeUnknown || !frozen {
						trace := append(append([]string(nil), cur.trace...), fmt.Sprintf("Resolve(cmd-%d,%s)", cmdNum, outcome.label))
						return SuiteReport{
							Suite:         "MultiCommandDeviceExclusivity",
							VisitedStates: len(visited),
							Transitions:   transitions,
							Passed:        false,
							Counterexample: &Counterexample{
								Property: PropertyP3,
								Suite:    "MultiCommandDeviceExclusivity",
								Summary:  fmt.Sprintf("non-eligible unverified resolution (%s) failed to freeze OUTCOME_UNKNOWN: status=%s frozen=%v", outcome.label, targetStatus, frozen),
								Trace:    trace,
							},
						}, nil
					}
				}
				if hooks.UnfreezeWithoutReconcile && targetStatus == commandmodel.IntentOutcomeUnknown {
					frozen = false
				}
				if cmdNum == 1 {
					next.c1Status = targetStatus
				} else {
					next.c2Status = targetStatus
				}
				next.deviceFrozen = st.deviceFrozen || frozen
				if st.c1PulseUnconfirmed && cmdNum == 2 && outcome.label == "AckVerified" && targetStatus == commandmodel.IntentSucceeded && !next.deviceFrozen {
					sawPulseUnconfirmedThenNextSucceeded = true
				}
				if cx, err := enqueue(next, fmt.Sprintf("Resolve(cmd-%d,%s)", cmdNum, outcome.label), cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return SuiteReport{Suite: "MultiCommandDeviceExclusivity", VisitedStates: len(visited), Transitions: transitions, Passed: false, Counterexample: cx}, nil
				}
			}
		}

		// Operator reconciliation for OUTCOME_UNKNOWN.
		for cmdNum := 1; cmdNum <= 2; cmdNum++ {
			status := st.c1Status
			if cmdNum == 2 {
				status = st.c2Status
			}
			if status != commandmodel.IntentOutcomeUnknown {
				continue
			}
			for _, outcome := range []commandmodel.ReconciledOutcome{commandmodel.ReconciledApplied, commandmodel.ReconciledNotApplied} {
				reconcileCap := commandmodel.CapabilityStart
				if cmdNum == 2 {
					reconcileCap = commandmodel.CapabilityStop
				}
				decision, err := commandservice.DecideOperatorReconcile(
					makeSyntheticIntentForCapability(uint64(cmdNum), reconcileCap, status, true, now),
					commandmodel.ReconcileRequest{
						CommandID:     fmt.Sprintf("cmd-%d", cmdNum),
						TenantID:      baseTenantID,
						Outcome:       outcome,
						Authorization: submitAuthorizationFor(reconcilePrincipalID, reconcileCap, now, commandmodel.RiskHigh),
					},
					now,
				)
				if err != nil {
					return SuiteReport{}, err
				}
				next := st
				if cmdNum == 1 {
					next.c1Status = decision.IntentFinal
				} else {
					next.c2Status = decision.IntentFinal
				}
				if decision.ReleaseControlGroup {
					next.deviceFrozen = next.c1Status == commandmodel.IntentOutcomeUnknown || next.c2Status == commandmodel.IntentOutcomeUnknown
				}
				if cx, err := enqueue(next, fmt.Sprintf("Reconcile(cmd-%d,%s)", cmdNum, outcome), cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return SuiteReport{Suite: "MultiCommandDeviceExclusivity", VisitedStates: len(visited), Transitions: transitions, Passed: false, Counterexample: cx}, nil
				}
			}
		}
	}

	if !sawPulseUnconfirmedThenNextSucceeded {
		return SuiteReport{
			Suite:         "MultiCommandDeviceExclusivity",
			VisitedStates: len(visited),
			Transitions:   transitions,
			Passed:        false,
			Counterexample: &Counterexample{
				Property: PropertyP3,
				Suite:    "MultiCommandDeviceExclusivity",
				Summary:  "pulse power-unconfirmed FAILED outcome failed to allow subsequent START/STOP command completion",
			},
		}, nil
	}

	return SuiteReport{
		Suite:         "MultiCommandDeviceExclusivity",
		VisitedStates: len(visited),
		Transitions:   transitions,
		Passed:        true,
	}, nil
}

type modelTransportState struct {
	mu           sync.Mutex
	correlations map[string]commandmodel.CommandCorrelation
}

func newModelTransportState() *modelTransportState {
	return &modelTransportState{correlations: make(map[string]commandmodel.CommandCorrelation)}
}

func corrMapKey(tenantID, attemptID string, fence uint64) string {
	return fmt.Sprintf("%s|%s|%d", tenantID, attemptID, fence)
}

func (m *modelTransportState) Put(corr commandmodel.CommandCorrelation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.correlations[corrMapKey(corr.Envelope.TenantID, corr.Envelope.AttemptID, corr.Envelope.ExecutionFence)] = corr
}

func (m *modelTransportState) ResolveCommandRoute(_ context.Context, _ commandmodel.DispatchEnvelope) (commandmodel.DeviceRoute, error) {
	return commandmodel.DeviceRoute{}, nil
}

func (m *modelTransportState) GatewayTenant(_ context.Context, _ string) (string, error) {
	return baseTenantID, nil
}

func (m *modelTransportState) Tenants(_ context.Context) ([]string, error) {
	return []string{baseTenantID}, nil
}

func (m *modelTransportState) PrepareCommandCorrelation(_ context.Context, c commandmodel.CommandCorrelation) (commandmodel.CommandCorrelation, error) {
	return c, nil
}

func (m *modelTransportState) ArmCommandCorrelation(_ context.Context, _, _ string, _ uint64, _ time.Time) error {
	return nil
}

func (m *modelTransportState) RecordCommandReply(_ context.Context, _, _, _ string, _ uint64, _, _ string, _ time.Time, _ string, _ *commandmodel.EdgeExecutionEvidence, _ time.Time) (commandmodel.CommandCorrelation, error) {
	return commandmodel.CommandCorrelation{}, nil
}

// RecoverCommandReplies reproduces connectivity.Store.RecoverCommandReplies SQL predicate & ordering:
// WHERE tenant_id = $1 AND state = 'REPLIED' ORDER BY replied_at ASC, attempt_id ASC LIMIT $2.
func (m *modelTransportState) RecoverCommandReplies(_ context.Context, tenantID string, limit int) ([]commandmodel.CommandCorrelation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]commandmodel.CommandCorrelation, 0, len(m.correlations))
	for _, corr := range m.correlations {
		if corr.Envelope.TenantID == tenantID && corr.State == commandmodel.CorrelationReplied {
			out = append(out, corr)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].RepliedAt.Equal(out[j].RepliedAt) {
			return out[i].RepliedAt.Before(out[j].RepliedAt)
		}
		return out[i].Envelope.AttemptID < out[j].Envelope.AttemptID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *modelTransportState) MarkCommandCorrelationResolved(_ context.Context, tenantID, attemptID string, executionFence uint64, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := corrMapKey(tenantID, attemptID, executionFence)
	corr, ok := m.correlations[key]
	if !ok {
		return nil
	}
	corr.State = commandmodel.CorrelationResolved
	m.correlations[key] = corr
	return nil
}

func (m *modelTransportState) CountReplied(tenantID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, corr := range m.correlations {
		if corr.Envelope.TenantID == tenantID && corr.State == commandmodel.CorrelationReplied {
			count++
		}
	}
	return count
}

type modelEvidenceStore struct{}

func (modelEvidenceStore) Prepare(context.Context, commandmodel.PreparedConnectorEvidence) error {
	return nil
}

func (modelEvidenceStore) Complete(context.Context, commandmodel.CompletedConnectorEvidence) error {
	return nil
}

type serviceLateResultSink struct {
	service            *commandservice.Service
	obscureClosedError bool
}

func (s serviceLateResultSink) ResolveDispatch(_ context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	err := s.service.ResolveDispatch(envelope, result)
	if err != nil && s.obscureClosedError && (errors.Is(err, commandservice.ErrStaleFence) || errors.Is(err, commandservice.ErrCommandNotFound)) {
		return fmt.Errorf("injected unclosed sink error: %v", err)
	}
	return err
}

func runRecoveryBatchScenario(staleCount int, batchLimit int, hooks FaultHooks) (states int, edges int, cx *Counterexample, err error) {
	now := baseModelTime
	svc := commandservice.New(func() time.Time { return now })
	transport := newModelTransportState()
	connector, err := mqttconnector.NewOffline(mqttconnector.Config{
		TransportState: transport,
		EvidenceStore:  modelEvidenceStore{},
		LateResultSink: serviceLateResultSink{
			service:            svc,
			obscureClosedError: hooks.DropStaleReplyRecoveryAsError,
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		return 0, 0, nil, err
	}

	// Seed stale REPLIED correlations covering superseded fences (ErrStaleFence),
	// already-settled OUTCOME_UNKNOWN intents (ErrStaleFence), and unregistered commands (ErrCommandNotFound).
	for i := 0; i < staleCount; i++ {
		if i%3 == 2 {
			transport.Put(buildCommandCorrelation(
				commandmodel.DispatchEnvelope{
					TenantID:       baseTenantID,
					CommandID:      "cmd-missing-" + strconv.Itoa(i),
					AttemptID:      "attempt-missing-" + strconv.Itoa(i),
					ExecutionFence: 1,
				},
				commandmodel.CorrelationReplied,
				"EXECUTED",
				now.Add(time.Duration(i+1)*time.Millisecond),
				validEdgeExecution(24),
			))
			continue
		}
		deviceID := fmt.Sprintf("01900000-0000-7000-8000-00000000%04x", i+10)
		req := makeSubmitRequest("stale-"+strconv.Itoa(i), 24, now)
		req.DeviceID = deviceID
		req.Authorization.DeviceID = deviceID
		sub, subErr := svc.Submit(req)
		if subErr != nil {
			return states, edges, nil, subErr
		}
		env1, prepErr := svc.PrepareDispatch(sub.Intent.ID, "dispatcher-old", now.Add(dispatchLeaseWindow))
		if prepErr != nil {
			return states, edges, nil, prepErr
		}
		if i%3 == 0 {
			if resErr := svc.ResolveDispatch(env1, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorPreSendRejected,
				FailureCode: "MQTT_ROUTE_UNAVAILABLE",
				EvidenceID:  "presend-" + strconv.Itoa(i),
			}); resErr != nil {
				return states, edges, nil, resErr
			}
			if _, prep2Err := svc.PrepareDispatch(sub.Intent.ID, "dispatcher-retry", now.Add(dispatchLeaseWindow)); prep2Err != nil {
				return states, edges, nil, prep2Err
			}
		} else {
			if resErr := svc.ResolveDispatch(env1, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorRequestCommitted,
				FailureCode: "MQTT_COMMAND_REPLY_TIMEOUT",
				EvidenceID:  "timeout-" + strconv.Itoa(i),
			}); resErr != nil {
				return states, edges, nil, resErr
			}
		}
		transport.Put(buildCommandCorrelation(
			env1,
			commandmodel.CorrelationReplied,
			"EXECUTED",
			now.Add(time.Duration(i+1)*time.Millisecond),
			validEdgeExecution(24),
		))
	}

	// Seed 1 trailing live EXECUTED correlation on a fresh Device awaiting dispatch resolution.
	liveReq := makeSubmitRequest("live-cmd", 24, now)
	liveReq.DeviceID = "01900000-0000-7000-8000-00000000f001"
	liveReq.Authorization.DeviceID = liveReq.DeviceID
	liveSub, err := svc.Submit(liveReq)
	if err != nil {
		return states, edges, nil, err
	}
	liveEnv, err := svc.PrepareDispatch(liveSub.Intent.ID, "dispatcher-live", now.Add(dispatchLeaseWindow))
	if err != nil {
		return states, edges, nil, err
	}
	transport.Put(buildCommandCorrelation(
		liveEnv,
		commandmodel.CorrelationReplied,
		"EXECUTED",
		now.Add(time.Duration(staleCount+5)*time.Millisecond),
		validEdgeExecution(24),
	))

	trace := []string{fmt.Sprintf("Seed(stale=%d,live=1,limit=%d)", staleCount, batchLimit)}
	maxRounds := (staleCount / batchLimit) + 3
	for round := 1; round <= maxRounds; round++ {
		beforeReplied := transport.CountReplied(baseTenantID)
		states++
		if beforeReplied == 0 {
			break
		}
		edges++
		trace = append(trace, fmt.Sprintf("RecoverTenantReplies(round=%d,limit=%d)", round, batchLimit))
		recErr := connector.RecoverTenantReplies(context.Background(), baseTenantID, batchLimit)
		if recErr != nil && !hooks.DropStaleReplyRecoveryAsError {
			return states, edges, nil, recErr
		}
		afterReplied := transport.CountReplied(baseTenantID)
		if afterReplied == beforeReplied {
			return states, edges, &Counterexample{
				Property: PropertyP6,
				Suite:    "ReplyRecoveryQueueDrain",
				Summary:  fmt.Sprintf("RecoverTenantReplies made zero progress: %d REPLIED correlations remained stuck and starved live reply %s", afterReplied, liveEnv.AttemptID),
				Trace:    trace,
			}, nil
		}
	}

	if remaining := transport.CountReplied(baseTenantID); remaining > 0 {
		return states, edges, &Counterexample{
			Property: PropertyP6,
			Suite:    "ReplyRecoveryQueueDrain",
			Summary:  fmt.Sprintf("%d REPLIED correlations still undrained after %d recovery rounds", remaining, maxRounds),
			Trace:    trace,
		}, nil
	}
	liveIntent, err := svc.Get(liveSub.Intent.ID)
	if err != nil {
		return states, edges, nil, err
	}
	if len(liveIntent.Attempts) == 0 || liveIntent.Attempts[0].Status != commandmodel.AttemptAcknowledged {
		gotStatus := "<none>"
		if len(liveIntent.Attempts) > 0 {
			gotStatus = string(liveIntent.Attempts[0].Status)
		}
		return states, edges, &Counterexample{
			Property: PropertyP6,
			Suite:    "ReplyRecoveryQueueDrain",
			Summary:  fmt.Sprintf("trailing live EXECUTED command did not advance to ATTEMPT ACKNOWLEDGED (got %s)", gotStatus),
			Trace:    trace,
		}, nil
	}
	return states, edges, nil, nil
}

// CheckReplyRecoveryQueueDrain exhaustively verifies that stale/fenced REPLIED MQTT correlations
// are marked RESOLVED rather than starving live EXECUTED replies behind SQL LIMIT 50.
func CheckReplyRecoveryQueueDrain(hooks FaultHooks) (SuiteReport, error) {
	configs := []struct {
		staleCount int
		batchLimit int
	}{
		{staleCount: 3, batchLimit: 2},
		{staleCount: 50, batchLimit: 50},
	}
	totalStates := 0
	totalEdges := 0
	for _, cfg := range configs {
		s, e, cx, err := runRecoveryBatchScenario(cfg.staleCount, cfg.batchLimit, hooks)
		totalStates += s
		totalEdges += e
		if err != nil {
			return SuiteReport{}, err
		}
		if cx != nil {
			return SuiteReport{
				Suite:          "ReplyRecoveryQueueDrain",
				VisitedStates:  totalStates,
				Transitions:    totalEdges,
				Passed:         false,
				Counterexample: cx,
			}, nil
		}
	}
	return SuiteReport{
		Suite:         "ReplyRecoveryQueueDrain",
		VisitedStates: totalStates,
		Transitions:   totalEdges,
		Passed:        true,
	}, nil
}
