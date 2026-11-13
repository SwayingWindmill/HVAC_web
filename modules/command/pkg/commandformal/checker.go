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
	stepApproveValid
	stepApproveSelfRejected
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
	case stepApproveValid:
		return "Approve(validApprover)"
	case stepApproveSelfRejected:
		return "Approve(selfApprover)"
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

type protoStateKey struct {
	submitted        bool
	intentStatus     commandmodel.IntentStatus
	attemptCount     int
	attemptStatus    commandmodel.AttemptStatus
	corrState        commandmodel.CorrelationState
	publishesAttempt int
	deviceFrozen     bool
	verificationOpen bool
	pastDeadline     bool
	staleAttemptUsed bool
	selfApproveTried bool
	expiredClaimTry  bool
	missingEdgeTried bool
}

type protoNode struct {
	key   protoStateKey
	steps []protoStepKind
}

type protoSim struct {
	now              time.Time
	service          *commandservice.Service
	commandID        string
	mediumRisk       bool
	dispatchEnv      commandmodel.DispatchEnvelope
	verifyEnv        commandmodel.VerificationEnvelope
	hasVerifyEnv     bool
	corrState        commandmodel.CorrelationState
	publishesAttempt int
	deviceFrozen     bool
	staleAttemptUsed bool
	selfApproveTried bool
	expiredClaimTry  bool
	missingEdgeTried bool
	stepCount        int
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
		submitted:        true,
		intentStatus:     intent.Status,
		attemptCount:     len(intent.Attempts),
		attemptStatus:    attemptStatus,
		corrState:        s.corrState,
		publishesAttempt: s.publishesAttempt,
		deviceFrozen:     s.deviceFrozen,
		verificationOpen: s.hasVerifyEnv,
		pastDeadline:     s.hasVerifyEnv && s.now.After(s.verifyEnv.VerificationDeadline),
		staleAttemptUsed: s.staleAttemptUsed,
		selfApproveTried: s.selfApproveTried,
		expiredClaimTry:  s.expiredClaimTry,
		missingEdgeTried: s.missingEdgeTried,
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
	case stepSubmitLowRisk, stepSubmitMediumRisk:
		setpoint := 24.0
		if step == stepSubmitMediumRisk {
			setpoint = 25.0
			sim.mediumRisk = true
		}
		req := makeSubmitRequest("formal-req", setpoint, sim.now)
		res, err := sim.service.Submit(req)
		if err != nil {
			return nil, fmt.Errorf("submit failed unexpectedly: %w", err)
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

	case stepPrepareDispatchValid:
		env, err := sim.service.PrepareDispatch(sim.commandID, "dispatcher-a", sim.now.Add(dispatchLeaseWindow))
		if err != nil {
			return nil, fmt.Errorf("prepare dispatch failed unexpectedly: %w", err)
		}
		sim.dispatchEnv = env
		sim.corrState = ""
		sim.publishesAttempt = 0
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

	case stepResolveAckValid:
		target := 24.0
		if sim.mediumRisk {
			target = 25.0
		}
		res := commandmodel.ConnectorResult{
			Phase:         commandmodel.ConnectorAcknowledged,
			Acknowledged:  true,
			Verified:      false,
			EvidenceID:    "ev-ack-" + sim.dispatchEnv.AttemptID,
			EdgeExecution: validEdgeExecution(target),
		}
		if err := sim.service.ResolveDispatch(sim.dispatchEnv, res); err != nil {
			return nil, fmt.Errorf("resolve valid ack failed: %w", err)
		}
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
		target := 24.0
		if sim.mediumRisk {
			target = 25.0
		}
		vRes := commandmodel.VerificationResult{
			Outcome:    commandmodel.VerificationSucceeded,
			EvidenceID: "ev-verify-ok",
			Reported:   validReportedEvidence(target, sim.verifyEnv.AcknowledgedAt.Add(time.Second), sim.verifyEnv.BaselineBusinessRevision+1),
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
			Reported:   validReportedEvidence(21, sim.verifyEnv.AcknowledgedAt.Add(time.Second), sim.verifyEnv.BaselineBusinessRevision+1),
		}
		if err := sim.service.ResolveVerification(sim.verifyEnv, vRes); err != nil {
			return nil, fmt.Errorf("resolve verification mismatch failed: %w", err)
		}
		sim.hasVerifyEnv = false
		sim.deviceFrozen = !hooks.UnfreezeWithoutReconcile
		if !sim.deviceFrozen {
			return &Counterexample{
				Property: PropertyP3,
				Suite:    "SingleCommandProtocol",
				Summary:  "verification mismatch transitioned to OUTCOME_UNKNOWN without freezing control group",
			}, nil
		}

	case stepResolveVerificationValidPastDeadline:
		sim.now = sim.verifyEnv.VerificationDeadline.Add(time.Second)
		target := 24.0
		if sim.mediumRisk {
			target = 25.0
		}
		vRes := commandmodel.VerificationResult{
			Outcome:    commandmodel.VerificationSucceeded,
			EvidenceID: "ev-verify-late",
			Reported:   validReportedEvidence(target, sim.verifyEnv.AcknowledgedAt.Add(time.Second), sim.verifyEnv.BaselineBusinessRevision+1),
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
		sim.deviceFrozen = !hooks.UnfreezeWithoutReconcile
		if intent.Status != commandmodel.IntentOutcomeUnknown || !sim.deviceFrozen {
			return &Counterexample{
				Property: PropertyP3,
				Suite:    "SingleCommandProtocol",
				Summary:  fmt.Sprintf("expired verification did not enter frozen OUTCOME_UNKNOWN: status=%s frozen=%v", intent.Status, sim.deviceFrozen),
			}, nil
		}

	case stepReconcileApplied, stepReconcileNotApplied:
		outcome := commandmodel.ReconciledApplied
		if step == stepReconcileNotApplied {
			outcome = commandmodel.ReconciledNotApplied
		}
		req := commandmodel.ReconcileRequest{
			CommandID:     sim.commandID,
			TenantID:      baseTenantID,
			Outcome:       outcome,
			Authorization: submitAuthorization(reconcilePrincipalID, sim.now, commandmodel.RiskHigh),
		}
		intent := mustIntent(sim)
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

func candidateProtoSteps(sim *protoSim) []protoStepKind {
	if sim.commandID == "" {
		// Distinguish initial unsubmitted state from settled protocol completion.
		if sim.stepCount > 0 {
			return nil
		}
		return []protoStepKind{stepSubmitLowRisk, stepSubmitMediumRisk}
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
		out = append(out, stepApproveValid)

	case commandmodel.IntentQueued:
		if len(intent.Attempts) < maxAttemptsPerCommand {
			out = append(out, stepPrepareDispatchValid)
			if !sim.expiredClaimTry {
				out = append(out, stepPrepareDispatchExpiredGrant)
			}
		}
		out = append(out, stepPrepareDispatchWhenFrozen)

	case commandmodel.IntentDispatching:
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
	c1Status     commandmodel.IntentStatus
	c2Status     commandmodel.IntentStatus
	c1Attempts   uint8
	c2Attempts   uint8
	c1GrantValid bool
	c2GrantValid bool
	deviceFrozen bool
}

type multiCmdNode struct {
	state multiCmdState
	trace []string
}

func makeSyntheticIntent(seq uint64, status commandmodel.IntentStatus, grantValid bool, now time.Time) commandmodel.CommandIntent {
	issuedAt := now
	if !grantValid {
		issuedAt = now.Add(-(grantLifetime + 5*time.Second))
	}
	return commandmodel.CommandIntent{
		ID:                    fmt.Sprintf("cmd-%d", seq),
		TenantID:              baseTenantID,
		SiteID:                baseSiteID,
		DeviceID:              baseDeviceID,
		PointID:               basePointID,
		PrincipalID:           submitPrincipalID,
		Capability:            commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision:    setpointCapabilityRev,
		Risk:                  commandmodel.RiskLow,
		ApprovalPolicy:        commandmodel.ApprovalNone,
		Parameters:            commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24},
		VerificationPointKey:  setpointVerificationKey,
		SnapshotRevision:      17,
		DeviceCommandSequence: seq,
		Status:                status,
		Authorization:         submitAuthorization(submitPrincipalID, issuedAt, commandmodel.RiskHigh),
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
			i1 := makeSyntheticIntent(1, st.c1Status, st.c1GrantValid, now)
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
			i2 := makeSyntheticIntent(2, st.c2Status, st.c2GrantValid, now)
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

		// Resolve dispatching outcomes for cmd 1 & cmd 2 using production DecideDispatchResolution / DecideVerificationResolution.
		for cmdNum := 1; cmdNum <= 2; cmdNum++ {
			status := st.c1Status
			if cmdNum == 2 {
				status = st.c2Status
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
				{label: "AckVerified", res: commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EdgeExecution: validEdgeExecution(24)}},
			}
			for _, outcome := range outcomes {
				decision, err := commandservice.DecideDispatchResolution(outcome.res, now)
				if err != nil {
					return SuiteReport{}, err
				}
				next := st
				targetStatus := decision.IntentFinal
				frozen := decision.FreezeControlGroup
				if outcome.label == "AckVerified" {
					vDec, vErr := commandservice.DecideVerificationResolution(
						makeSyntheticIntent(uint64(cmdNum), commandmodel.IntentDispatching, true, now),
						commandmodel.VerificationEnvelope{
							VerificationPointKey:     setpointVerificationKey,
							BaselineBusinessRevision: 17,
							AcknowledgedAt:           now,
							EdgeExecution:            validEdgeExecution(24),
						},
						commandmodel.VerificationResult{
							Outcome:    commandmodel.VerificationSucceeded,
							EvidenceID: "ev-multi",
							Reported:   validReportedEvidence(24, now.Add(time.Second), 18),
						},
						now.Add(defaultVerificationGrace),
						now.Add(2*time.Second),
					)
					if vErr != nil {
						return SuiteReport{}, vErr
					}
					targetStatus = vDec.IntentFinal
					frozen = vDec.FreezeControlGroup
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
				decision, err := commandservice.DecideOperatorReconcile(
					makeSyntheticIntent(uint64(cmdNum), status, true, now),
					commandmodel.ReconcileRequest{
						CommandID:     fmt.Sprintf("cmd-%d", cmdNum),
						TenantID:      baseTenantID,
						Outcome:       outcome,
						Authorization: submitAuthorization(reconcilePrincipalID, now, commandmodel.RiskHigh),
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
