package commandservice

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
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

type stubRuntimeStore struct {
	dispatchErr error
	verifyErr   error
}

func (stub *stubRuntimeStore) ClaimDispatch(context.Context, string, string, time.Duration) (commandmodel.DispatchEnvelope, error) {
	return commandmodel.DispatchEnvelope{}, ErrNoDispatchAvailable
}

func (stub *stubRuntimeStore) ResolveDispatch(context.Context, commandmodel.DispatchEnvelope, commandmodel.ConnectorResult) error {
	return stub.dispatchErr
}

func (stub *stubRuntimeStore) ClaimVerification(context.Context, string, string, time.Duration) (commandmodel.VerificationEnvelope, error) {
	return commandmodel.VerificationEnvelope{}, ErrVerificationNotAvailable
}

func (stub *stubRuntimeStore) ResolveVerification(context.Context, commandmodel.VerificationEnvelope, commandmodel.VerificationResult) error {
	return stub.verifyErr
}

func (stub *stubRuntimeStore) PrepareConnectorEvidence(context.Context, commandmodel.PreparedConnectorEvidence) error {
	return nil
}

func (stub *stubRuntimeStore) CompleteConnectorEvidence(context.Context, commandmodel.CompletedConnectorEvidence) error {
	return nil
}

func (stub *stubRuntimeStore) CancelPending(context.Context, commandmodel.CancelPendingRequest) ([]string, error) {
	return nil, nil
}

func TestControlFailureNotifierCoversUnknownOutcomeAndUnconfirmedPowerNonBlocking(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	svc := New(func() time.Time { return now })

	events := make(chan ControlFailureEvent, 4)
	releaseBlocker := make(chan struct{})
	svc.SetControlFailureNotifier(func(_ context.Context, event ControlFailureEvent) {
		<-releaseBlocker
		events <- event
	})

	req := validRequest()
	req.IdempotencyKey = "notify-unknown-1"
	sub, err := svc.Submit(req)
	if err != nil {
		t.Fatal(err)
	}
	env, err := svc.PrepareDispatch(sub.Intent.ID, "disp-1", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ResolveDispatch(env, commandmodel.ConnectorResult{
		Phase:       commandmodel.ConnectorRequestCommitted,
		FailureCode: "MQTT_TIMEOUT",
		EvidenceID:  "ev-commit-timeout",
	}); err != nil {
		t.Fatal(err)
	}

	reqPulse := validRequest()
	reqPulse.IdempotencyKey = "notify-pulse-1"
	reqPulse.DeviceID = "device-pulse"
	reqPulse.Authorization.DeviceID = "device-pulse"
	reqPulse.Capability = commandmodel.CapabilityStart
	reqPulse.Authorization.Capability = commandmodel.CapabilityStart
	profile, ok := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStart)
	if !ok {
		t.Fatal("missing START capability profile")
	}
	reqPulse.Authorization.CapabilityRevision = profile.Revision
	reqPulse.Parameters = commandmodel.CommandParameters{}
	reqPulse.VerificationPointKey = "chiller.run_state"
	subPulse, err := svc.Submit(reqPulse)
	if err != nil {
		t.Fatal(err)
	}
	envPulse, err := svc.PrepareDispatch(subPulse.Intent.ID, "disp-1", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	pulseEdge := &commandmodel.EdgeExecutionEvidence{
		Requested:          commandmodel.TextScalar("START"),
		Effective:          func() *commandmodel.ScalarValue { v := commandmodel.TextScalar("ch1:pulse(500ms)->0"); return &v }(),
		Applied:            func() *commandmodel.ScalarValue { v := commandmodel.TextScalar("ch1:pulse(500ms)->0"); return &v }(),
		WinnerControllerID: "edge",
		Cycle:              1,
	}
	if err := svc.ResolveDispatch(envPulse, commandmodel.ConnectorResult{
		Phase:         commandmodel.ConnectorAcknowledged,
		Acknowledged:  true,
		EvidenceID:    "ev-pulse-ack",
		EdgeExecution: pulseEdge,
	}); err != nil {
		t.Fatal(err)
	}
	vEnv, err := svc.PrepareVerification(subPulse.Intent.ID, "ver-1", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ResolveVerification(vEnv, commandmodel.VerificationResult{
		Outcome:     commandmodel.VerificationTimedOut,
		EvidenceID:  "ev-pulse-timeout",
		FailureCode: "POWER_STILL_BELOW_THRESHOLD",
	}); err != nil {
		t.Fatal(err)
	}

	close(releaseBlocker)
	seen := map[string]ControlFailureEvent{}
	for i := 0; i < 2; i++ {
		select {
		case evt := <-events:
			seen[evt.CommandID] = evt
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for async failure event %d", i+1)
		}
	}
	if got := seen[sub.Intent.ID]; got.Kind != string(commandmodel.IntentOutcomeUnknown) || got.ReasonCode != "REQUEST_COMMITTED_OUTCOME_UNKNOWN" {
		t.Fatalf("unexpected OUTCOME_UNKNOWN event: %+v", got)
	}
	if got := seen[subPulse.Intent.ID]; got.Kind != commandmodel.ReasonPulseDeliveredPowerUnconfirmed || got.ReasonCode != commandmodel.ReasonPulseDeliveredPowerUnconfirmed {
		t.Fatalf("unexpected unconfirmed-power event: %+v", got)
	}

	// Verify runtime HTTP handler triggers notifier cleanly on resolveDispatch/resolveVerification.
	httpEvents := make(chan ControlFailureEvent, 4)
	runtimeHandler, err := NewRuntimeHTTPHandler(RuntimeHTTPConfig{
		Store:            &stubRuntimeStore{},
		DispatcherSPIFFE: "spiffe://hvac.local/command-dispatcher",
		VerifierSPIFFE:   "spiffe://hvac.local/command-verifier",
		ControlFailureNotifier: func(_ context.Context, event ControlFailureEvent) {
			httpEvents <- event
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	postRuntime := func(path, body, spiffe string) int {
		t.Helper()
		uri, parseErr := url.Parse(spiffe)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
		rec := httptest.NewRecorder()
		runtimeHandler.ServeHTTP(rec, req)
		return rec.Code
	}
	dispatchBody, err := json.Marshal(runtimeDispatchResolveRequest{
		Envelope: commandmodel.DispatchEnvelope{
			CommandID:  "cmd-http-1",
			TenantID:   "t1",
			SiteID:     "s1",
			DeviceID:   "d1",
			PointID:    "p1",
			Capability: commandmodel.CapabilityStart,
		},
		Result: commandmodel.ConnectorResult{
			Phase:       commandmodel.ConnectorRequestCommitted,
			FailureCode: "MQTT_IO",
			EvidenceID:  "ev1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code := postRuntime(InternalDispatchResolvePath, string(dispatchBody), "spiffe://hvac.local/command-dispatcher"); code != http.StatusNoContent {
		t.Fatalf("resolveDispatch HTTP code=%d", code)
	}
	select {
	case evt := <-httpEvents:
		if evt.CommandID != "cmd-http-1" || evt.Kind != "OUTCOME_UNKNOWN" {
			t.Fatalf("unexpected HTTP dispatch event: %+v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime HTTP dispatch failure event")
	}

	verifyBody, err := json.Marshal(runtimeVerificationResolveRequest{
		Envelope: commandmodel.VerificationEnvelope{
			CommandID:     "cmd-http-2",
			TenantID:      "t1",
			SiteID:        "s1",
			DeviceID:      "d1",
			PointID:       "p1",
			Capability:    commandmodel.CapabilityStart,
			EdgeExecution: pulseEdge,
		},
		Result: commandmodel.VerificationResult{
			Outcome:     commandmodel.VerificationTimedOut,
			EvidenceID:  "ev2",
			FailureCode: "POWER_LOW",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code := postRuntime(InternalVerificationResolvePath, string(verifyBody), "spiffe://hvac.local/command-verifier"); code != http.StatusNoContent {
		t.Fatalf("resolveVerification HTTP code=%d", code)
	}
	select {
	case evt := <-httpEvents:
		if evt.CommandID != "cmd-http-2" || evt.Kind != commandmodel.ReasonPulseDeliveredPowerUnconfirmed {
			t.Fatalf("unexpected HTTP verification event: %+v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime HTTP verification failure event")
	}
}

func TestRuntimeHTTPVerificationSucceededPastDeadlineEmitsControlFailure(t *testing.T) {
	httpEvents := make(chan ControlFailureEvent, 4)
	runtimeHandler, err := NewRuntimeHTTPHandler(RuntimeHTTPConfig{
		Store:            &stubRuntimeStore{},
		DispatcherSPIFFE: "spiffe://hvac.local/command-dispatcher",
		VerifierSPIFFE:   "spiffe://hvac.local/command-verifier",
		ControlFailureNotifier: func(_ context.Context, event ControlFailureEvent) {
			httpEvents <- event
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	postVerification := func(env commandmodel.VerificationEnvelope, reported commandmodel.ReportedStateEvidence) {
		t.Helper()
		body, marshalErr := json.Marshal(runtimeVerificationResolveRequest{
			Envelope: env,
			Result: commandmodel.VerificationResult{
				Outcome:    commandmodel.VerificationSucceeded,
				EvidenceID: "ev-verify-succeeded",
				Reported:   reported,
			},
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		uri, parseErr := url.Parse("spiffe://hvac.local/command-verifier")
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		req := httptest.NewRequest(http.MethodPost, InternalVerificationResolvePath, strings.NewReader(string(body)))
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
		rec := httptest.NewRecorder()
		runtimeHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("resolveVerification HTTP code=%d body=%s", rec.Code, rec.Body.String())
		}
	}

	now := time.Now().UTC()
	setpoint := 24.0
	setpointScalar := commandmodel.NumberScalar(setpoint)
	setpointEdge := &commandmodel.EdgeExecutionEvidence{
		Requested:          setpointScalar,
		Effective:          &setpointScalar,
		Applied:            &setpointScalar,
		WinnerControllerID: "cloud-command-intent",
		Cycle:              1,
	}
	reportedSetpoint := commandmodel.ReportedStateEvidence{
		TenantID:               "t1",
		SiteID:                 "s1",
		DeviceID:               "d1",
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Freshness:              "FRESH",
		Quality:                "GOOD",
		BusinessRevision:       11,
		ReportedValue:          commandmodel.NumberScalar(setpoint),
		ObservedAt:             now,
	}

	// 1. On-time VerificationSucceeded with valid reported state must NOT emit failure alarm.
	postVerification(commandmodel.VerificationEnvelope{
		CommandID:                "cmd-ontime",
		TenantID:                 "t1",
		SiteID:                   "s1",
		DeviceID:                 "d1",
		PointID:                  "p1",
		Capability:               commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters:               commandmodel.CommandParameters{commandmodel.ParameterSetpointC: setpoint},
		VerificationPointKey:     "temp_setpoint",
		BaselineBusinessRevision: 10,
		AcknowledgedAt:           now.Add(-5 * time.Second),
		VerificationDeadline:     now.Add(time.Minute),
		EdgeExecution:            setpointEdge,
	}, reportedSetpoint)
	select {
	case unexpected := <-httpEvents:
		t.Fatalf("unexpected failure notification for on-time verified command: %+v", unexpected)
	case <-time.After(100 * time.Millisecond):
	}

	// 2. Late VerificationSucceeded past VerificationDeadline for setpoint must emit OUTCOME_UNKNOWN.
	postVerification(commandmodel.VerificationEnvelope{
		CommandID:                "cmd-late-level",
		TenantID:                 "t1",
		SiteID:                   "s1",
		DeviceID:                 "d1",
		PointID:                  "p1",
		Capability:               commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters:               commandmodel.CommandParameters{commandmodel.ParameterSetpointC: setpoint},
		VerificationPointKey:     "temp_setpoint",
		BaselineBusinessRevision: 10,
		AcknowledgedAt:           now.Add(-2 * time.Minute),
		VerificationDeadline:     now.Add(-time.Second),
		EdgeExecution:            setpointEdge,
	}, reportedSetpoint)
	select {
	case evt := <-httpEvents:
		if evt.CommandID != "cmd-late-level" || evt.Kind != string(commandmodel.IntentOutcomeUnknown) || evt.ReasonCode != "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED" {
			t.Fatalf("unexpected late level failure event: %+v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for late VerificationSucceeded failure event")
	}

	// 3. Late VerificationSucceeded past VerificationDeadline for pulse START must emit PULSE_DELIVERED_POWER_UNCONFIRMED.
	pulseText := "u14:pulse(500ms)->0"
	pulseEdge := &commandmodel.EdgeExecutionEvidence{
		Requested:          commandmodel.TextScalar("START"),
		Effective:          &commandmodel.ScalarValue{Text: &pulseText},
		Applied:            &commandmodel.ScalarValue{Text: &pulseText},
		WinnerControllerID: "cloud-command-intent",
		Cycle:              1,
	}
	postVerification(commandmodel.VerificationEnvelope{
		CommandID:                "cmd-late-pulse",
		TenantID:                 "t1",
		SiteID:                   "s1",
		DeviceID:                 "d1",
		PointID:                  "p1",
		Capability:               commandmodel.CapabilityStart,
		VerificationPointKey:     "run_state",
		BaselineBusinessRevision: 10,
		AcknowledgedAt:           now.Add(-2 * time.Minute),
		VerificationDeadline:     now.Add(-time.Second),
		EdgeExecution:            pulseEdge,
	}, commandmodel.ReportedStateEvidence{
		TenantID:               "t1",
		SiteID:                 "s1",
		DeviceID:               "d1",
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Freshness:              "FRESH",
		Quality:                "GOOD",
		BusinessRevision:       11,
		ReportedValue:          commandmodel.TextScalar("RUNNING"),
		ObservedAt:             now,
	})
	select {
	case evt := <-httpEvents:
		if evt.CommandID != "cmd-late-pulse" || evt.Kind != commandmodel.ReasonPulseDeliveredPowerUnconfirmed || evt.ReasonCode != commandmodel.ReasonPulseDeliveredPowerUnconfirmed {
			t.Fatalf("unexpected late pulse failure event: %+v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for late pulse VerificationSucceeded failure event")
	}
}

func TestSubmitIdempotentReplayIgnoresTraceID(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	defer service.Close()

	req1 := validRequest()
	req1.TraceID = " 4BF92F3577B34DA6A3CE929D0E0E4736 "
	first, err := service.Submit(req1)
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	wantTraceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	if first.Intent.TraceID != wantTraceID {
		t.Fatalf("first.Intent.TraceID=%q, want %q", first.Intent.TraceID, wantTraceID)
	}

	req2 := validRequest()
	req2.TraceID = "fedcba9876543210fedcba9876543210"
	second, err := service.Submit(req2)
	if err != nil {
		t.Fatalf("idempotent retry with new TraceID failed: %v", err)
	}
	if !second.Replayed || second.Intent.ID != first.Intent.ID || second.Intent.TraceID != wantTraceID {
		t.Fatalf("unexpected replay result: replayed=%v id=%q trace=%q", second.Replayed, second.Intent.ID, second.Intent.TraceID)
	}

	env, err := service.PrepareDispatch(first.Intent.ID, "dispatcher-a", clock().Add(time.Minute))
	if err != nil {
		t.Fatalf("PrepareDispatch failed: %v", err)
	}
	if env.TraceID != wantTraceID {
		t.Fatalf("DispatchEnvelope.TraceID=%q, want %q", env.TraceID, wantTraceID)
	}
}

func TestOperatorHTTPHandlerPropagatesTraceparentToDispatchEnvelope(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, _ := seedPlantHarness(t, now)
	defer h.Authority.Close()
	defer h.Store.Close()

	claims := identitycontext.DelegationClaims{
		Issuer: "spiffe://hvac.local/platform-gateway", Subject: "op-trace", SubjectIssuer: "https://issuer.test",
		ExecutingService: "spiffe://hvac.local/platform-gateway", Audience: "command-service",
		TenantID: caTenantID, Actions: []string{OperatorControlContextAction},
		Scopes: []string{"tenant:" + caTenantID, "session:sess-trace"}, PolicyRevision: "policy-v1",
		SessionID: "sess-trace", IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(),
		TokenID: "ctx-trace-1",
	}
	signedCtx, err := identitycontext.SignDelegation(h.GatewaySigner, claims)
	if err != nil {
		t.Fatal(err)
	}

	const wantTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	body := `{"assetId":"` + caChillerID + `","commandPointId":"` + caSetpointPt + `","parameters":{"setpointC":23.5}}`
	req := httptest.NewRequest(http.MethodPost, InternalOperatorCommandsPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "http-trace-1")
	req.Header.Set("X-Command-Control-Context", signedCtx)
	req.Header.Set("traceparent", "00-"+wantTraceID+"-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("operator create status=%d body=%s", rec.Code, rec.Body.String())
	}

	env, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "dispatcher-trace", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimDispatch failed: %v", err)
	}
	if env.TraceID != wantTraceID {
		t.Fatalf("DispatchEnvelope.TraceID=%q, want %q", env.TraceID, wantTraceID)
	}
}

func TestControlFailureQueueRecoversPanicsAndDrainsOnClose(t *testing.T) {
	var queue controlFailureQueue
	workerStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	var mu sync.Mutex
	delivered := make([]string, 0, controlFailureQueueCapacity+1)
	panicked := false

	blockingNotifier := func(_ context.Context, event ControlFailureEvent) {
		switch event.CommandID {
		case "cmd-gate":
			close(workerStarted)
			<-releaseFirst
			mu.Lock()
			delivered = append(delivered, event.CommandID)
			mu.Unlock()
		case "cmd-panic":
			mu.Lock()
			panicked = true
			mu.Unlock()
			panic("simulated notifier panic")
		default:
			mu.Lock()
			delivered = append(delivered, event.CommandID)
			mu.Unlock()
		}
	}

	queue.enqueue(blockingNotifier, ControlFailureEvent{CommandID: "cmd-gate"})
	<-workerStarted

	// Fill all 256 buffer slots (including a panicking item) plus overflow to verify bounded drop.
	queue.enqueue(blockingNotifier, ControlFailureEvent{CommandID: "cmd-panic"})
	for i := 1; i < controlFailureQueueCapacity+10; i++ {
		queue.enqueue(blockingNotifier, ControlFailureEvent{CommandID: "cmd-buf"})
	}
	close(releaseFirst)

	if err := queue.Close(context.Background()); err != nil {
		t.Fatalf("queue.Close returned error: %v", err)
	}
	mu.Lock()
	gotPanicked := panicked
	gotDelivered := len(delivered)
	mu.Unlock()
	if !gotPanicked {
		t.Fatal("expected panicking notifier to execute and recover")
	}
	if wantDelivered := controlFailureQueueCapacity; gotDelivered != wantDelivered {
		t.Fatalf("delivered=%d want=%d (gate + 255 buffered non-panicking items; overflow dropped)", gotDelivered, wantDelivered)
	}

	// Verify Service.Close drains queued events after panic recovery cleanly.
	clock := fixedClock()
	service := New(clock)
	var svcMu sync.Mutex
	var svcSeen []string
	var svcCalls int
	svcGate := make(chan struct{})
	service.SetControlFailureNotifier(func(_ context.Context, event ControlFailureEvent) {
		svcMu.Lock()
		svcCalls++
		call := svcCalls
		svcMu.Unlock()
		if call == 1 {
			<-svcGate
			panic("first service failure notifier panicked")
		}
		time.Sleep(15 * time.Millisecond)
		svcMu.Lock()
		svcSeen = append(svcSeen, event.CommandID)
		svcMu.Unlock()
	})

	submitAndCommit := func(key, device string) string {
		req := validRequest()
		req.IdempotencyKey = key
		req.DeviceID = device
		req.Authorization.DeviceID = device
		sub, err := service.Submit(req)
		if err != nil {
			t.Fatalf("submit %s: %v", key, err)
		}
		env, err := service.PrepareDispatch(sub.Intent.ID, "dispatcher-a", clock().Add(time.Minute))
		if err != nil {
			t.Fatalf("prepare %s: %v", key, err)
		}
		if err := service.ResolveDispatch(env, commandmodel.ConnectorResult{
			Phase: commandmodel.ConnectorRequestCommitted, FailureCode: "MQTT_TIMEOUT", EvidenceID: "ev-" + key,
		}); err != nil {
			t.Fatalf("resolve %s: %v", key, err)
		}
		return sub.Intent.ID
	}

	_ = submitAndCommit("q-1", "dev-q-1")
	secondID := submitAndCommit("q-2", "dev-q-2")
	close(svcGate)
	service.Close()

	svcMu.Lock()
	defer svcMu.Unlock()
	if len(svcSeen) != 1 || svcSeen[0] != secondID {
		t.Fatalf("expected Service.Close to drain %q after panic recovery, got %v", secondID, svcSeen)
	}
}
