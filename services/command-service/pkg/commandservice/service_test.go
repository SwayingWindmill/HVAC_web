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
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-1",
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
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-2",
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

func validRequest() commandmodel.SubmitRequest {
	return commandmodel.SubmitRequest{
		TenantID:       "tenant-1",
		SiteID:         "site-1",
		DeviceID:       "device-1",
		PointID:        "point-1",
		PrincipalID:    "principal-1",
		IdempotencyKey: "request-1",
		Capability:           commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24},
		VerificationPointKey: "zone.temperature_setpoint",
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE",
			Presence:               "ONLINE",
			Readiness:              "CURRENT",
			Quality:                "GOOD",
			BusinessRevision: 17,
			CurrentValue:     testFloat64Pointer(23),
			ObservedAt:       time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC),
		},
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID: "grant-1", PolicyRevision: "command-policy-1", Purpose: commandmodel.AuthorizationCommandSubmit,
			PrincipalID:    "principal-1",
			TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
			Capability:  commandmodel.CapabilitySetTemperatureSetpoint,
			MaximumRisk: commandmodel.RiskHigh, CapabilityRevision: setpointCapabilityRevision,
			EmergencyRevocationRevision: 1,
			IssuedAt:                    time.Date(2026, 7, 26, 8, 59, 55, 0, time.UTC),
			ExpiresAt:                   time.Date(2026, 7, 26, 9, 0, 25, 0, time.UTC),
		},
	}
}

func fixedClock() func() time.Time {
	return func() time.Time {
		return time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	}
}
