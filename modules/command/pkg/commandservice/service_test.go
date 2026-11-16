package commandservice

import (
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const setpointCapabilityRevision = "capability:set-temperature-setpoint:v1"

func testFloat64Pointer(value float64) *float64 {
	return &value
}

func TestSubmitIsIdempotentForSamePayload(t *testing.T) {
	service := New(fixedClock())
	request := validRequest()

	first, err := service.Submit(request)
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	second, err := service.Submit(request)
	if err != nil {
		t.Fatalf("second submit failed: %v", err)
	}
	if !second.Replayed {
		t.Fatal("expected replayed result")
	}
	if second.Intent.ID != first.Intent.ID {
		t.Fatalf("idempotent replay created a second command: %s != %s", second.Intent.ID, first.Intent.ID)
	}
	if first.Intent.Status != commandmodel.IntentQueued {
		t.Fatalf("expected queued intent, got %s", first.Intent.Status)
	}
}

func TestSubmitRejectsIdempotencyPayloadConflict(t *testing.T) {
	service := New(fixedClock())
	request := validRequest()
	if _, err := service.Submit(request); err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	request.Parameters[commandmodel.ParameterSetpointC] = 25
	_, err := service.Submit(request)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestSubmitFailsClosedOnUnsafeCurrentState(t *testing.T) {
	service := New(fixedClock())
	request := validRequest()
	request.CurrentState.Presence = "UNKNOWN"
	_, err := service.Submit(request)
	if !errors.Is(err, ErrCurrentStateUnsafe) {
		t.Fatalf("expected unsafe current state, got %v", err)
	}
}

func TestPreSendFailureCanBeRetriedWithHigherFence(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	submitted, err := service.Submit(validRequest())
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	first, err := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-a", clock().Add(time.Minute))
	if err != nil {
		t.Fatalf("first prepare failed: %v", err)
	}
	if err := service.ResolveDispatch(first, commandmodel.ConnectorResult{
		Phase:      commandmodel.ConnectorPreSendRejected,
		EvidenceID: "synthetic-pre-send",
	}); err != nil {
		t.Fatalf("pre-send resolution failed: %v", err)
	}
	second, err := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-b", clock().Add(time.Minute))
	if err != nil {
		t.Fatalf("second prepare failed: %v", err)
	}
	if second.ExecutionFence <= first.ExecutionFence {
		t.Fatalf("expected higher fence: first=%d second=%d", first.ExecutionFence, second.ExecutionFence)
	}
	if err := service.ResolveDispatch(first, commandmodel.ConnectorResult{
		Phase:        commandmodel.ConnectorAcknowledged,
		Acknowledged: true,
		Verified:     true,
	}); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expected stale fence rejection, got %v", err)
	}
}

func TestDisabledControlRejectsCommandWithItsReason(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	submitted, err := service.Submit(validRequest())
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	envelope, err := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-a", clock().Add(time.Minute))
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if err := service.ResolveDispatch(envelope, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "COMMAND_CONTROL_DISABLED",
	}); err != nil {
		t.Fatalf("pre-send resolution failed: %v", err)
	}
	rejected, _ := service.Get(submitted.Intent.ID)
	if last := rejected.Transitions[len(rejected.Transitions)-1]; rejected.Status != commandmodel.IntentRejected || last.Reason != "COMMAND_CONTROL_DISABLED" {
		t.Fatalf("status=%s reason=%s", rejected.Status, last.Reason)
	}
	if _, err := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-b", clock().Add(time.Minute)); err == nil {
		t.Fatal("a rejected command was dispatched again")
	}
}

func TestCommittedWithoutOutcomeFreezesCommand(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	submitted, err := service.Submit(validRequest())
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	envelope, err := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-a", clock().Add(time.Minute))
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if err := service.ResolveDispatch(envelope, commandmodel.ConnectorResult{
		Phase:      commandmodel.ConnectorRequestCommitted,
		EvidenceID: "synthetic-request-committed",
	}); err != nil {
		t.Fatalf("resolution failed: %v", err)
	}
	intent, err := service.Get(submitted.Intent.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if intent.Status != commandmodel.IntentOutcomeUnknown {
		t.Fatalf("expected OUTCOME_UNKNOWN, got %s", intent.Status)
	}
	if _, err := service.PrepareDispatch(intent.ID, "dispatcher-b", clock().Add(time.Minute)); !errors.Is(err, ErrCommandNotDispatchable) {
		t.Fatalf("OUTCOME_UNKNOWN must not be blindly retried, got %v", err)
	}
}

func TestAcknowledgedCommandRequiresReportedStateVerification(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	submitted, err := service.Submit(validRequest())
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-a", clock().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResolveDispatch(dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-1", EdgeExecution: testEdgeExecution(24),
	}); err != nil {
		t.Fatal(err)
	}
	acknowledged, _ := service.Get(submitted.Intent.ID)
	if acknowledged.Status != commandmodel.IntentDispatching || acknowledged.Attempts[0].Status != commandmodel.AttemptAcknowledged {
		t.Fatalf("ACK declared business success: intent=%s attempt=%s", acknowledged.Status, acknowledged.Attempts[0].Status)
	}
	verification, err := service.PrepareVerification(submitted.Intent.ID, "verifier-a", clock().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResolveVerification(verification, commandmodel.VerificationResult{
		Outcome: commandmodel.VerificationSucceeded, EvidenceID: "s2-reported-state-1",
		Reported: commandmodel.ReportedStateEvidence{
			TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
			BusinessRevision: 18, ReportedValue: commandmodel.NumberScalar(24), ObservedAt: clock().Add(time.Second),
		},
	}); err != nil {
		t.Fatal(err)
	}
	verified, _ := service.Get(submitted.Intent.ID)
	if verified.Status != commandmodel.IntentSucceeded || verified.Attempts[0].Status != commandmodel.AttemptVerified {
		t.Fatalf("reported state did not verify command: intent=%s attempt=%s", verified.Status, verified.Attempts[0].Status)
	}
}

func TestReportedStateMismatchBecomesOutcomeUnknown(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	submitted, _ := service.Submit(validRequest())
	dispatch, _ := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-a", clock().Add(time.Minute))
	if err := service.ResolveDispatch(dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-2", EdgeExecution: testEdgeExecution(24),
	}); err != nil {
		t.Fatal(err)
	}
	verification, err := service.PrepareVerification(submitted.Intent.ID, "verifier-a", clock().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResolveVerification(verification, commandmodel.VerificationResult{
		Outcome: commandmodel.VerificationMismatch, EvidenceID: "s2-reported-state-mismatch",
		Reported: commandmodel.ReportedStateEvidence{TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1", BusinessRevision: 18, ReportedValue: commandmodel.NumberScalar(21), ObservedAt: clock().Add(time.Second)},
	}); err != nil {
		t.Fatal(err)
	}
	unknown, _ := service.Get(submitted.Intent.ID)
	if unknown.Status != commandmodel.IntentOutcomeUnknown || unknown.Attempts[0].Status != commandmodel.AttemptOutcomeUnknown {
		t.Fatalf("mismatch did not freeze uncertainty: intent=%s attempt=%s", unknown.Status, unknown.Attempts[0].Status)
	}
	if _, err := service.PrepareDispatch(unknown.ID, "dispatcher-b", clock().Add(time.Minute)); !errors.Is(err, ErrCommandNotDispatchable) {
		t.Fatalf("unknown command was retryable: %v", err)
	}
}

func TestConnectorCannotDeclareReportedStateVerified(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	submitted, _ := service.Submit(validRequest())
	dispatch, _ := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-a", clock().Add(time.Minute))
	err := service.ResolveDispatch(dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, Verified: true, EvidenceID: "provider-claimed-verified",
	})
	if !errors.Is(err, ErrUnsupportedResult) {
		t.Fatalf("provider bypassed reported-state verifier: %v", err)
	}
	intent, _ := service.Get(submitted.Intent.ID)
	if intent.Status != commandmodel.IntentDispatching || intent.Attempts[0].Status != commandmodel.AttemptPrepared || intent.Attempts[0].Version != 1 {
		t.Fatalf("rejected provider verification mutated state: intent=%s attempt=%s version=%d", intent.Status, intent.Attempts[0].Status, intent.Attempts[0].Version)
	}
}

func TestVerificationPastDeadlineBecomesOutcomeUnknown(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	service := New(func() time.Time { return now })
	submitted, err := service.Submit(validRequest())
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-a", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResolveDispatch(dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-deadline", EdgeExecution: testEdgeExecution(24),
	}); err != nil {
		t.Fatal(err)
	}
	verification, err := service.PrepareVerification(submitted.Intent.ID, "verifier-a", now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	now = verification.VerificationDeadline.Add(time.Second)
	if err := service.ResolveVerification(verification, commandmodel.VerificationResult{
		Outcome:    commandmodel.VerificationSucceeded,
		EvidenceID: "s2-late-reported-state",
		Reported: commandmodel.ReportedStateEvidence{
			TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
			BusinessRevision: verification.BaselineBusinessRevision + 1, ReportedValue: commandmodel.NumberScalar(24),
			ObservedAt: verification.AcknowledgedAt.Add(time.Second),
		},
	}); err != nil {
		t.Fatal(err)
	}
	intent, _ := service.Get(submitted.Intent.ID)
	last := intent.Transitions[len(intent.Transitions)-1]
	if intent.Status != commandmodel.IntentOutcomeUnknown || intent.Attempts[0].Status != commandmodel.AttemptOutcomeUnknown ||
		last.Reason != "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED" {
		t.Fatalf("post-deadline verification=%s/%s reason=%s", intent.Status, intent.Attempts[0].Status, last.Reason)
	}
}

func TestResolveDispatchRejectsAlreadySettledAttemptWithActiveFence(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	submitted, err := service.Submit(validRequest())
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-a", clock().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResolveDispatch(dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorRequestCommitted, EvidenceID: "req-committed",
	}); err != nil {
		t.Fatal(err)
	}
	err = service.ResolveDispatch(dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "late-ack", EdgeExecution: testEdgeExecution(24),
	})
	if !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expected ErrStaleFence on settled attempt, got %v", err)
	}
	intent, _ := service.Get(submitted.Intent.ID)
	if intent.Status != commandmodel.IntentOutcomeUnknown || intent.Attempts[0].Status != commandmodel.AttemptOutcomeUnknown {
		t.Fatalf("settled attempt mutated on duplicate resolution: intent=%s attempt=%s", intent.Status, intent.Attempts[0].Status)
	}
}

func validRequest() commandmodel.SubmitRequest {
	return commandmodel.SubmitRequest{
		TenantID:             "tenant-1",
		SiteID:               "site-1",
		DeviceID:             "device-1",
		PointID:              "point-1",
		PrincipalID:          "principal-1",
		IdempotencyKey:       "request-1",
		Capability:           commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24},
		VerificationPointKey: "zone.temperature_setpoint",
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE",
			Presence:               "ONLINE",
			Readiness:              "CURRENT",
			Quality:                "GOOD",
			BusinessRevision:       17,
			CurrentValue:           testFloat64Pointer(23),
			ObservedAt:             time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC),
		},
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID: "grant-1", PolicyRevision: "command-policy-1", Purpose: commandmodel.AuthorizationCommandSubmit,
			PrincipalID: "principal-1",
			TenantID:    "tenant-1", SiteID: "site-1", DeviceID: "device-1",
			Capability:  commandmodel.CapabilitySetTemperatureSetpoint,
			MaximumRisk: commandmodel.RiskHigh, CapabilityRevision: setpointCapabilityRevision,
			EmergencyRevocationRevision: 1,
			IssuedAt:                    time.Date(2026, 7, 26, 8, 59, 55, 0, time.UTC),
			ExpiresAt:                   time.Date(2026, 7, 26, 9, 0, 25, 0, time.UTC),
		},
	}
}

func TestServiceCancelAllowsAwaitingApprovalAndQueuedAndRejectsLaterStates(t *testing.T) {
	service := New(fixedClock())

	// AWAITING_APPROVAL (medium risk delta 1.5C) -> CANCELLED.
	mediumReq := validRequest()
	mediumReq.IdempotencyKey = "cancel-awaiting-key"
	mediumReq.Parameters = commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24.5}
	mediumReq.Authorization.MaximumRisk = commandmodel.RiskMedium
	mediumRes, err := service.Submit(mediumReq)
	if err != nil || mediumRes.Intent.Status != commandmodel.IntentAwaitingApproval {
		t.Fatalf("submit medium failed: res=%#v err=%v", mediumRes, err)
	}
	cancelledAwaiting, err := service.Cancel(commandmodel.CancelRequest{
		TenantID:      mediumReq.TenantID,
		CommandID:     mediumRes.Intent.ID,
		Reason:        "OPERATOR_CANCELLED",
		Authorization: mediumReq.Authorization,
	})
	if err != nil || cancelledAwaiting.Status != commandmodel.IntentCancelled {
		t.Fatalf("cancel awaiting failed: intent=%#v err=%v", cancelledAwaiting, err)
	}

	// QUEUED -> CANCELLED, and DISPATCHING -> ErrCancelInvalid.
	queuedReq := validRequest()
	queuedReq.IdempotencyKey = "cancel-queued-key"
	queuedRes, err := service.Submit(queuedReq)
	if err != nil || queuedRes.Intent.Status != commandmodel.IntentQueued {
		t.Fatalf("submit queued failed: res=%#v err=%v", queuedRes, err)
	}
	cancelledQueued, err := service.Cancel(commandmodel.CancelRequest{
		TenantID:      queuedReq.TenantID,
		CommandID:     queuedRes.Intent.ID,
		Authorization: queuedReq.Authorization,
	})
	if err != nil || cancelledQueued.Status != commandmodel.IntentCancelled {
		t.Fatalf("cancel queued failed: intent=%#v err=%v", cancelledQueued, err)
	}
	// Terminal CANCELLED cannot be cancelled again or dispatched.
	if _, err := service.Cancel(commandmodel.CancelRequest{
		TenantID:      queuedReq.TenantID,
		CommandID:     queuedRes.Intent.ID,
		Authorization: queuedReq.Authorization,
	}); !errors.Is(err, ErrCancelInvalid) {
		t.Fatalf("expected ErrCancelInvalid on repeat cancel, got %v", err)
	}

	// Dispatched command rejects cancel.
	dispReq := validRequest()
	dispReq.IdempotencyKey = "cancel-dispatching-key"
	dispRes, err := service.Submit(dispReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareDispatch(dispRes.Intent.ID, "disp-1", fixedClock()().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Cancel(commandmodel.CancelRequest{
		TenantID:      dispReq.TenantID,
		CommandID:     dispRes.Intent.ID,
		Authorization: dispReq.Authorization,
	}); !errors.Is(err, ErrCancelInvalid) {
		t.Fatalf("expected ErrCancelInvalid when DISPATCHING, got %v", err)
	}
}

func TestDecideDispatchResolutionUsesCapabilityVerificationWindow(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	ack := commandmodel.ConnectorResult{
		Phase:         commandmodel.ConnectorAcknowledged,
		Acknowledged:  true,
		EvidenceID:    "ev-1",
		EdgeExecution: testEdgeExecution(1),
	}

	seqDecision, err := DecideDispatchResolution(commandmodel.CapabilityStartUnitSequence, ack, now)
	if err != nil {
		t.Fatal(err)
	}
	if seqDecision.VerificationDeadline == nil || seqDecision.VerificationDeadline.Sub(now) != 10*time.Minute {
		t.Fatalf("unit-sequence deadline=%v want %s", seqDecision.VerificationDeadline, 10*time.Minute)
	}

	setpointDecision, err := DecideDispatchResolution(commandmodel.CapabilitySetTemperatureSetpoint, ack, now)
	if err != nil {
		t.Fatal(err)
	}
	if setpointDecision.VerificationDeadline == nil || setpointDecision.VerificationDeadline.Sub(now) != 2*time.Minute {
		t.Fatalf("setpoint deadline=%v want %s", setpointDecision.VerificationDeadline, 2*time.Minute)
	}
}

func fixedClock() func() time.Time {
	return func() time.Time {
		return time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	}
}

func TestDecideVerificationResolutionPulseDualReadback(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(10 * time.Minute)
	pulseText := "u14:pulse(500ms)->0"
	stuckPulseText := "u14:pulse(500ms)->1"
	levelText := "u11:1"
	requested := commandmodel.TextScalar("START")

	pulseDual := &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: &commandmodel.ScalarValue{Text: &pulseText}, Applied: &commandmodel.ScalarValue{Text: &pulseText},
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}
	pulseStuck := &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: &commandmodel.ScalarValue{Text: &stuckPulseText}, Applied: &commandmodel.ScalarValue{Text: &stuckPulseText},
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}
	levelCoil := &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: &commandmodel.ScalarValue{Text: &levelText}, Applied: &commandmodel.ScalarValue{Text: &levelText},
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}

	makeEnvelope := func(cap commandmodel.Capability, edge *commandmodel.EdgeExecutionEvidence) (commandmodel.CommandIntent, commandmodel.VerificationEnvelope) {
		intent := commandmodel.CommandIntent{
			ID: "cmd-1", TenantID: "t-1", SiteID: "s-1", DeviceID: "d-1", PointID: "p-1",
			Capability: cap, VerificationPointKey: "equipment.run_state", SnapshotRevision: 10,
		}
		env := commandmodel.VerificationEnvelope{
			CommandID: "cmd-1", AttemptID: "att-1", TenantID: "t-1", SiteID: "s-1", DeviceID: "d-1", PointID: "p-1",
			Capability: cap, VerificationPointKey: "equipment.run_state", BaselineBusinessRevision: 10,
			AcknowledgedAt: now, VerificationDeadline: deadline, EdgeExecution: edge,
		}
		return intent, env
	}

	tests := []struct {
		name        string
		capability  commandmodel.Capability
		edge        *commandmodel.EdgeExecutionEvidence
		result      commandmodel.VerificationResult
		at          time.Time
		wantAttempt commandmodel.AttemptStatus
		wantIntent  commandmodel.IntentStatus
		wantReason  string
		wantFreeze  bool
		wantVerify  bool
	}{
		{
			name:        "pulse START timed out fails without freeze",
			capability:  commandmodel.CapabilityStart,
			edge:        pulseDual,
			result:      commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut, EvidenceID: "ev-timeout"},
			at:          deadline.Add(time.Second),
			wantAttempt: commandmodel.AttemptFailed,
			wantIntent:  commandmodel.IntentFailed,
			wantReason:  commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
			wantFreeze:  false,
		},
		{
			name:        "pulse STOP mismatch fails without freeze",
			capability:  commandmodel.CapabilityStop,
			edge:        pulseDual,
			result:      commandmodel.VerificationResult{Outcome: commandmodel.VerificationMismatch, EvidenceID: "ev-mismatch"},
			at:          now.Add(time.Minute),
			wantAttempt: commandmodel.AttemptFailed,
			wantIntent:  commandmodel.IntentFailed,
			wantReason:  commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
			wantFreeze:  false,
		},
		{
			name:        "pulse START late verified past deadline still fails without freeze",
			capability:  commandmodel.CapabilityStart,
			edge:        pulseDual,
			result:      commandmodel.VerificationResult{Outcome: commandmodel.VerificationSucceeded, EvidenceID: "ev-late", Reported: commandmodel.ReportedStateEvidence{TenantID: "t-1", SiteID: "s-1", DeviceID: "d-1", EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD", BusinessRevision: 11, ReportedValue: commandmodel.TextScalar("RUNNING"), ObservedAt: now.Add(time.Second)}},
			at:          deadline.Add(time.Second),
			wantAttempt: commandmodel.AttemptFailed,
			wantIntent:  commandmodel.IntentFailed,
			wantReason:  commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
			wantFreeze:  false,
		},
		{
			name:        "pulse START in window succeeds",
			capability:  commandmodel.CapabilityStart,
			edge:        pulseDual,
			result:      commandmodel.VerificationResult{Outcome: commandmodel.VerificationSucceeded, EvidenceID: "ev-ok", Reported: commandmodel.ReportedStateEvidence{TenantID: "t-1", SiteID: "s-1", DeviceID: "d-1", EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD", BusinessRevision: 11, ReportedValue: commandmodel.TextScalar("RUNNING"), ObservedAt: now.Add(time.Second)}},
			at:          now.Add(10 * time.Second),
			wantAttempt: commandmodel.AttemptVerified,
			wantIntent:  commandmodel.IntentSucceeded,
			wantReason:  "ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED",
			wantFreeze:  false,
			wantVerify:  true,
		},
		{
			name:        "level START timed out freezes OUTCOME_UNKNOWN",
			capability:  commandmodel.CapabilityStart,
			edge:        levelCoil,
			result:      commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut, EvidenceID: "ev-level-timeout"},
			at:          deadline.Add(time.Second),
			wantAttempt: commandmodel.AttemptOutcomeUnknown,
			wantIntent:  commandmodel.IntentOutcomeUnknown,
			wantReason:  "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED",
			wantFreeze:  true,
		},
		{
			name:        "pulse stuck high freezes OUTCOME_UNKNOWN",
			capability:  commandmodel.CapabilityStart,
			edge:        pulseStuck,
			result:      commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut, EvidenceID: "ev-stuck"},
			at:          deadline.Add(time.Second),
			wantAttempt: commandmodel.AttemptOutcomeUnknown,
			wantIntent:  commandmodel.IntentOutcomeUnknown,
			wantReason:  "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED",
			wantFreeze:  true,
		},
		{
			name:        "missing edge freezes OUTCOME_UNKNOWN",
			capability:  commandmodel.CapabilityStart,
			edge:        nil,
			result:      commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut, EvidenceID: "ev-nil-edge"},
			at:          deadline.Add(time.Second),
			wantAttempt: commandmodel.AttemptOutcomeUnknown,
			wantIntent:  commandmodel.IntentOutcomeUnknown,
			wantReason:  "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED",
			wantFreeze:  true,
		},
		{
			name:        "unit sequence timed out freezes OUTCOME_UNKNOWN even with pulse readback",
			capability:  commandmodel.CapabilityStartUnitSequence,
			edge:        pulseDual,
			result:      commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut, EvidenceID: "ev-seq"},
			at:          deadline.Add(time.Second),
			wantAttempt: commandmodel.AttemptOutcomeUnknown,
			wantIntent:  commandmodel.IntentOutcomeUnknown,
			wantReason:  "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED",
			wantFreeze:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			intent, env := makeEnvelope(tc.capability, tc.edge)
			decision, err := DecideVerificationResolution(intent, env, tc.result, deadline, tc.at)
			if err != nil {
				t.Fatalf("DecideVerificationResolution error: %v", err)
			}
			if decision.AttemptFinal != tc.wantAttempt || decision.IntentFinal != tc.wantIntent ||
				decision.Reason != tc.wantReason || decision.FreezeControlGroup != tc.wantFreeze ||
				decision.Verified != tc.wantVerify {
				t.Fatalf("got %+v, want attempt=%s intent=%s reason=%s freeze=%v verified=%v",
					decision, tc.wantAttempt, tc.wantIntent, tc.wantReason, tc.wantFreeze, tc.wantVerify)
			}
		})
	}
}

func TestServicePrepareVerificationPulseExpiryFailsWithoutBlockingFollowup(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	svc := New(func() time.Time { return now })

	submitStart := validRequest()
	submitStart.IdempotencyKey = "pulse-start-1"
	submitStart.Capability = commandmodel.CapabilityStart
	submitStart.Parameters = commandmodel.CommandParameters{}
	submitStart.VerificationPointKey = "chiller.run_state"
	submitStart.Authorization.Capability = commandmodel.CapabilityStart
	submitStart.Authorization.CapabilityRevision = "capability:start:v1"
	res, err := svc.Submit(submitStart)
	if err != nil {
		t.Fatal(err)
	}
	disp, err := svc.PrepareDispatch(res.Intent.ID, "disp-1", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	pulse := "u14:pulse(500ms)->0"
	if err := svc.ResolveDispatch(disp, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "ev-ack",
		EdgeExecution: &commandmodel.EdgeExecutionEvidence{
			Requested: commandmodel.TextScalar("START"),
			Effective: &commandmodel.ScalarValue{Text: &pulse}, Applied: &commandmodel.ScalarValue{Text: &pulse},
			WinnerControllerID: "edge", Cycle: 1,
		},
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10*time.Minute + time.Second)
	if _, err := svc.PrepareVerification(res.Intent.ID, "ver-1", now.Add(time.Minute)); !errors.Is(err, ErrVerificationNotAvailable) {
		t.Fatalf("expected ErrVerificationNotAvailable, got %v", err)
	}
	settled, err := svc.Get(res.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != commandmodel.IntentFailed {
		t.Fatalf("expected IntentFailed, got %s", settled.Status)
	}
	if len(settled.Transitions) == 0 || settled.Transitions[len(settled.Transitions)-1].Reason != commandmodel.ReasonPulseDeliveredPowerUnconfirmed {
		t.Fatalf("unexpected transition reason: %+v", settled.Transitions)
	}
}

func TestDecideVerificationResolutionEmergencyDoAndUnitSequence(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	deadline := now.Add(10 * time.Minute)
	str := func(v string) *commandmodel.ScalarValue { return &commandmodel.ScalarValue{Text: &v} }
	requested := commandmodel.TextScalar("STOP")

	emergencyDoEdge := &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: str("all-do:0"), Applied: str("all-do:0"),
		WinnerControllerID: "edge-local", Cycle: 1,
	}
	unitSeqEdge := &commandmodel.EdgeExecutionEvidence{
		Requested: commandmodel.TextScalar("START_UNIT_SEQUENCE"), Effective: str("chiller-1:sequence-accepted"), Applied: str("chiller-1:sequence-accepted"),
		WinnerControllerID: "edge-local", Cycle: 1,
	}
	pulseEdge := &commandmodel.EdgeExecutionEvidence{
		Requested: commandmodel.TextScalar("START"), Effective: str("u14:pulse(500ms)->0"), Applied: str("u14:pulse(500ms)->0"),
		WinnerControllerID: "edge-local", Cycle: 1,
	}

	tests := []struct {
		name       string
		capability commandmodel.Capability
		edge       *commandmodel.EdgeExecutionEvidence
		wantIntent commandmodel.IntentStatus
		wantReason string
		wantFreeze bool
	}{
		{
			name:       "emergency DO release STOP expires without freeze",
			capability: commandmodel.CapabilityStop,
			edge:       emergencyDoEdge,
			wantIntent: commandmodel.IntentFailed,
			wantReason: commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
			wantFreeze: false,
		},
		{
			name:       "emergency DO release edge on START still freezes OUTCOME_UNKNOWN",
			capability: commandmodel.CapabilityStart,
			edge:       emergencyDoEdge,
			wantIntent: commandmodel.IntentOutcomeUnknown,
			wantReason: "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED",
			wantFreeze: true,
		},
		{
			name:       "START_UNIT_SEQUENCE accepted expires without freeze",
			capability: commandmodel.CapabilityStartUnitSequence,
			edge:       unitSeqEdge,
			wantIntent: commandmodel.IntentFailed,
			wantReason: commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
			wantFreeze: false,
		},
		{
			name:       "STOP_UNIT_SEQUENCE accepted expires without freeze",
			capability: commandmodel.CapabilityStopUnitSequence,
			edge:       unitSeqEdge,
			wantIntent: commandmodel.IntentFailed,
			wantReason: commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
			wantFreeze: false,
		},
		{
			name:       "unit sequence with pulse readback still freezes OUTCOME_UNKNOWN",
			capability: commandmodel.CapabilityStartUnitSequence,
			edge:       pulseEdge,
			wantIntent: commandmodel.IntentOutcomeUnknown,
			wantReason: "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED",
			wantFreeze: true,
		},
		{
			name:       "START with sequence-accepted still freezes OUTCOME_UNKNOWN",
			capability: commandmodel.CapabilityStart,
			edge:       unitSeqEdge,
			wantIntent: commandmodel.IntentOutcomeUnknown,
			wantReason: "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED",
			wantFreeze: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			intent := commandmodel.CommandIntent{
				ID:                   "cmd-edge-class",
				Capability:           tc.capability,
				VerificationPointKey: "equipment.run_state",
				Status:               commandmodel.IntentDispatching,
			}
			env := commandmodel.VerificationEnvelope{
				VerificationPointKey: "equipment.run_state",
				AcknowledgedAt:       now,
				EdgeExecution:        tc.edge,
			}
			decision, err := DecideVerificationResolutionWithEdge(
				intent, env, tc.edge,
				commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut, EvidenceID: "ev-timeout"},
				deadline, deadline.Add(time.Second),
			)
			if err != nil {
				t.Fatalf("DecideVerificationResolutionWithEdge error: %v", err)
			}
			if decision.IntentFinal != tc.wantIntent || decision.Reason != tc.wantReason || decision.FreezeControlGroup != tc.wantFreeze {
				t.Fatalf("got %+v, want intent=%s reason=%s freeze=%v", decision, tc.wantIntent, tc.wantReason, tc.wantFreeze)
			}
		})
	}
}

func TestServiceCancelPendingAbortActiveSequences(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	svc := New(func() time.Time { return now })

	submitDevice := func(key, deviceID string, cap commandmodel.Capability, setpoint *float64) commandmodel.CommandIntent {
		t.Helper()
		req := validRequest()
		req.IdempotencyKey = key
		req.DeviceID = deviceID
		req.Authorization.DeviceID = deviceID
		req.Capability = cap
		req.Authorization.Capability = cap
		profile, ok := commandmodel.CapabilityProfileFor(cap)
		if !ok {
			t.Fatalf("missing capability profile for %s", cap)
		}
		req.Authorization.CapabilityRevision = profile.Revision
		if setpoint != nil {
			req.Parameters = commandmodel.CommandParameters{commandmodel.ParameterSetpointC: *setpoint}
			req.VerificationPointKey = "zone.temperature_setpoint"
		} else {
			req.Parameters = commandmodel.CommandParameters{}
			req.VerificationPointKey = "chiller.run_state"
		}
		res, err := svc.Submit(req)
		if err != nil {
			t.Fatalf("submit %s: %v", key, err)
		}
		return res.Intent
	}
	ackDevice := func(commandID, evidence string, edgeText string) {
		t.Helper()
		env, err := svc.PrepareDispatch(commandID, "disp-1", now.Add(time.Minute))
		if err != nil {
			t.Fatalf("prepare dispatch %s: %v", commandID, err)
		}
		applied := commandmodel.TextScalar(edgeText)
		if err := svc.ResolveDispatch(env, commandmodel.ConnectorResult{
			Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: evidence,
			EdgeExecution: &commandmodel.EdgeExecutionEvidence{
				Requested: commandmodel.TextScalar(string(env.Capability)),
				Effective: &applied, Applied: &applied,
				WinnerControllerID: "edge", Cycle: 1,
			},
		}); err != nil {
			t.Fatalf("ack dispatch %s: %v", commandID, err)
		}
	}

	queued := submitDevice("k-queued", "device-queued", commandmodel.CapabilityStart, nil)
	preparedSeq := submitDevice("k-prepared-seq", "device-prepared", commandmodel.CapabilityStartUnitSequence, nil)
	if _, err := svc.PrepareDispatch(preparedSeq.ID, "disp-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	ackStartSeq := submitDevice("k-ack-start-seq", "device-seq-start", commandmodel.CapabilityStartUnitSequence, nil)
	ackDevice(ackStartSeq.ID, "ev-seq-start", "chiller-1:sequence-accepted")
	if _, err := svc.PrepareVerification(ackStartSeq.ID, "verifier-1", now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}

	ackStopSeq := submitDevice("k-ack-stop-seq", "device-seq-stop", commandmodel.CapabilityStopUnitSequence, nil)
	ackDevice(ackStopSeq.ID, "ev-seq-stop", "chiller-2:sequence-accepted")

	ackEmergencyDo := submitDevice("k-ack-edo", "device-edo", commandmodel.CapabilityStop, nil)
	ackDevice(ackEmergencyDo.ID, "ev-edo", "all-do:0")

	ackPulseStop := submitDevice("k-ack-pulse-stop", "device-pulse", commandmodel.CapabilityStop, nil)
	ackDevice(ackPulseStop.ID, "ev-pulse", "u14:pulse(500ms)->0")

	sp := 24.0
	ackSetpoint := submitDevice("k-ack-sp", "device-sp", commandmodel.CapabilitySetTemperatureSetpoint, &sp)
	envSP, err := svc.PrepareDispatch(ackSetpoint.ID, "disp-1", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	numApplied := commandmodel.NumberScalar(24.0)
	if err := svc.ResolveDispatch(envSP, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "ev-sp",
		EdgeExecution: &commandmodel.EdgeExecutionEvidence{
			Requested: numApplied, Effective: &numApplied, Applied: &numApplied,
			WinnerControllerID: "edge", Cycle: 1,
		},
	}); err != nil {
		t.Fatal(err)
	}

	// AbortActiveSequences == false MUST only cancel QUEUED, never touch ACKNOWLEDGED sequences.
	idsWithoutAbort, err := svc.CancelPending(commandmodel.CancelPendingRequest{
		TenantID:             "tenant-1",
		SiteID:               "site-1",
		DeviceIDs:            []string{"device-queued", "device-seq-stop"},
		PrincipalID:          "operator-1",
		Reason:               "MANUAL_OVERRIDE",
		AbortActiveSequences: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(idsWithoutAbort) != 1 || idsWithoutAbort[0] != queued.ID {
		t.Fatalf("expected only queued command cancelled without AbortActiveSequences, got %v", idsWithoutAbort)
	}
	if got, _ := svc.Get(ackStopSeq.ID); got.Status != commandmodel.IntentDispatching {
		t.Fatalf("ackStopSeq changed without AbortActiveSequences: %s", got.Status)
	}

	// AbortActiveSequences == true MUST abort ACKNOWLEDGED sequences & emergency DO release,
	// while leaving PREPARED, pulse STOP, and SET_TEMPERATURE_SETPOINT DISPATCHING.
	abortedIDs, err := svc.CancelPending(commandmodel.CancelPendingRequest{
		TenantID:             "tenant-1",
		SiteID:               "site-1",
		PrincipalID:          "operator-1",
		Reason:               "SAFE_SHUTDOWN_EXIT",
		AbortActiveSequences: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(abortedIDs) != 3 {
		t.Fatalf("expected 3 aborted active sequence IDs, got %v", abortedIDs)
	}
	for _, id := range []string{ackStartSeq.ID, ackStopSeq.ID, ackEmergencyDo.ID} {
		got, err := svc.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != commandmodel.IntentFailed {
			t.Fatalf("expected %s IntentFailed, got %s", id, got.Status)
		}
		if got.Attempts[0].Status != commandmodel.AttemptFailed || got.Attempts[0].VerificationLeaseOwner != "" || !got.Attempts[0].VerificationLeaseUntil.IsZero() {
			t.Fatalf("expected attempt FAILED with cleared lease on %s, got %+v", id, got.Attempts[0])
		}
		last := got.Transitions[len(got.Transitions)-1]
		if last.Reason != "ACTIVE_SEQUENCE_ABORTED" {
			t.Fatalf("expected ACTIVE_SEQUENCE_ABORTED transition reason on %s, got %+v", id, last)
		}
	}
	for _, untouchedID := range []string{preparedSeq.ID, ackPulseStop.ID, ackSetpoint.ID} {
		got, err := svc.Get(untouchedID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != commandmodel.IntentDispatching {
			t.Fatalf("command %s must remain DISPATCHING, got %s", untouchedID, got.Status)
		}
	}
}

func TestSubmitEmergencyBypassPrecondition(t *testing.T) {
	service := New(fixedClock())

	stopProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStop)
	req := validRequest()
	req.IdempotencyKey = "bypass-stop-1"
	req.Capability = commandmodel.CapabilityStop
	req.Parameters = commandmodel.CommandParameters{}
	req.VerificationPointKey = "run_state"
	req.Authorization.Capability = commandmodel.CapabilityStop
	req.Authorization.CapabilityRevision = stopProfile.Revision
	req.Authorization.MaximumRisk = commandmodel.RiskCritical
	req.CurrentState = commandmodel.CurrentStateEvidence{}
	req.PreconditionMode = commandmodel.PreconditionEmergencyBypass
	req.BypassReason = "telemetry offline during site emergency shutdown"

	submitted, err := service.Submit(req)
	if err != nil {
		t.Fatalf("expected STOP with EMERGENCY_BYPASS and zero revision to succeed, got %v", err)
	}
	if submitted.Intent.PreconditionMode != commandmodel.PreconditionEmergencyBypass || submitted.Intent.BypassReason == "" {
		t.Fatalf("precondition fields not preserved: %+v", submitted.Intent)
	}
	if submitted.Intent.SnapshotRevision != 0 {
		t.Fatalf("expected SnapshotRevision 0 on bypass, got %d", submitted.Intent.SnapshotRevision)
	}

	normalReq := req
	normalReq.IdempotencyKey = "normal-stop-1"
	normalReq.PreconditionMode = commandmodel.PreconditionRequireCurrentState
	normalReq.BypassReason = ""
	normalReq.CurrentState = validRequest().CurrentState
	normalReq.CurrentState.CurrentValue = nil
	normalSubmitted, err := service.Submit(normalReq)
	if err != nil {
		t.Fatalf("expected normal STOP submit to succeed: %v", err)
	}
	if normalSubmitted.Intent.PayloadHash == submitted.Intent.PayloadHash {
		t.Fatalf("expected bypass payload hash to differ from REQUIRE_CURRENT_STATE hash")
	}

	missingReason := req
	missingReason.IdempotencyKey = "bypass-missing-reason"
	missingReason.BypassReason = "   "
	if _, err := service.Submit(missingReason); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest when bypass reason empty, got %v", err)
	}

	unsupported := validRequest()
	unsupported.IdempotencyKey = "bypass-setpoint"
	unsupported.PreconditionMode = commandmodel.PreconditionEmergencyBypass
	unsupported.BypassReason = "emergency"
	if _, err := service.Submit(unsupported); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("expected ErrCapabilityDenied on non-bypassable setpoint, got %v", err)
	}
}
