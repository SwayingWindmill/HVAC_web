package commanddispatcher

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestSyntheticAcknowledgementCompletesOnlyAfterReportedState(t *testing.T) {
	clock := testClock()
	store := commandservice.New(clock)
	submitted, err := store.Submit(testRequest())
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	dispatcher := newSyntheticDurableDispatcher(store, submitted.Intent.ID, syntheticConnector{mode: syntheticVerifiedSuccess}, "dispatcher-a", clock)
	if err := dispatcher.RunOnce(context.Background(), "org-1"); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	acknowledged, err := store.Get(submitted.Intent.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if acknowledged.Status != commandmodel.IntentDispatching || len(acknowledged.Attempts) != 1 || acknowledged.Attempts[0].Status != commandmodel.AttemptAcknowledged {
		t.Fatalf("provider ACK declared success: intent=%s attempts=%#v", acknowledged.Status, acknowledged.Attempts)
	}
	completeSyntheticVerification(t, store, submitted.Intent.ID, clock)
	intent, err := store.Get(submitted.Intent.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if intent.Status != commandmodel.IntentSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s", intent.Status)
	}
	if len(intent.Attempts) != 1 || intent.Attempts[0].Status != commandmodel.AttemptVerified {
		t.Fatalf("expected one verified attempt, got %#v", intent.Attempts)
	}
}

func TestCommittedTimeoutBecomesOutcomeUnknown(t *testing.T) {
	clock := testClock()
	store := commandservice.New(clock)
	submitted, err := store.Submit(testRequest())
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	dispatcher := newSyntheticDurableDispatcher(store, submitted.Intent.ID, syntheticConnector{mode: syntheticCommittedThenTimeout}, "dispatcher-a", clock)
	if err := dispatcher.RunOnce(context.Background(), "org-1"); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	intent, err := store.Get(submitted.Intent.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if intent.Status != commandmodel.IntentOutcomeUnknown {
		t.Fatalf("expected OUTCOME_UNKNOWN, got %s", intent.Status)
	}
}

func TestPreSendFailureCanBeRetriedByAnotherDispatcher(t *testing.T) {
	clock := testClock()
	store := commandservice.New(clock)
	submitted, err := store.Submit(testRequest())
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	first := newSyntheticDurableDispatcher(store, submitted.Intent.ID, syntheticConnector{mode: syntheticPreSendRejected}, "dispatcher-a", clock)
	if err := first.RunOnce(context.Background(), "org-1"); err != nil {
		t.Fatalf("first dispatch failed: %v", err)
	}
	second := newSyntheticDurableDispatcher(store, submitted.Intent.ID, syntheticConnector{mode: syntheticVerifiedSuccess}, "dispatcher-b", clock)
	if err := second.RunOnce(context.Background(), "org-1"); err != nil {
		t.Fatalf("second dispatch failed: %v", err)
	}
	completeSyntheticVerification(t, store, submitted.Intent.ID, clock)
	intent, err := store.Get(submitted.Intent.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if intent.Status != commandmodel.IntentSucceeded || len(intent.Attempts) != 2 {
		t.Fatalf("expected successful second attempt, got status=%s attempts=%d", intent.Status, len(intent.Attempts))
	}
	if intent.Attempts[1].ExecutionFence <= intent.Attempts[0].ExecutionFence {
		t.Fatalf("fence did not advance: %#v", intent.Attempts)
	}
}

func completeSyntheticVerification(t *testing.T, store *commandservice.Service, commandID string, clock func() time.Time) {
	t.Helper()
	envelope, err := store.PrepareVerification(commandID, "verifier-a", clock().Add(30*time.Second))
	if err != nil {
		t.Fatalf("prepare verification failed: %v", err)
	}
	if err := store.ResolveVerification(envelope, commandmodel.VerificationResult{
		Outcome:    commandmodel.VerificationSucceeded,
		EvidenceID: "synthetic:s2-reported-state",
		Reported: commandmodel.ReportedStateEvidence{
			TenantID: envelope.TenantID, SiteID: envelope.SiteID, DeviceID: envelope.DeviceID,
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
			BusinessRevision: envelope.BaselineBusinessRevision + 1, ReportedValue: commandmodel.NumberScalar(envelope.Parameters[commandmodel.ParameterSetpointC]),
			ObservedAt: envelope.AcknowledgedAt.Add(time.Second),
		},
	}); err != nil {
		t.Fatalf("resolve verification failed: %v", err)
	}
}

func testRequest() commandmodel.SubmitRequest {
	return commandmodel.SubmitRequest{
		TenantID:             "org-1",
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
			BusinessRevision:       12,
			CurrentValue:           testNumberPointer(23),
			ObservedAt:             time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC),
		},
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID: "grant-dispatcher-test", PolicyRevision: "command-policy-1",
			Purpose: commandmodel.AuthorizationCommandSubmit, PrincipalID: "principal-1", TenantID: "org-1", SiteID: "site-1", DeviceID: "device-1",
			Capability:  commandmodel.CapabilitySetTemperatureSetpoint,
			MaximumRisk: commandmodel.RiskLow, CapabilityRevision: "capability:set-temperature-setpoint:v1",
			EmergencyRevocationRevision: 1,
			IssuedAt:                    time.Date(2026, 7, 26, 9, 59, 55, 0, time.UTC),
			ExpiresAt:                   time.Date(2026, 7, 26, 10, 0, 25, 0, time.UTC),
		},
	}
}

type syntheticMode string

const (
	syntheticVerifiedSuccess      syntheticMode = "VERIFIED_SUCCESS"
	syntheticPreSendRejected      syntheticMode = "PRE_SEND_REJECTED"
	syntheticCommittedThenTimeout syntheticMode = "COMMITTED_THEN_TIMEOUT"
)

type syntheticConnector struct {
	mode syntheticMode
}

func (connector syntheticConnector) Execute(_ context.Context, _ commandmodel.DispatchEnvelope) (commandmodel.ConnectorResult, error) {
	switch connector.mode {
	case syntheticVerifiedSuccess:
		requested := commandmodel.NumberScalar(24)
		return commandmodel.ConnectorResult{
			Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "synthetic:provider-acknowledged",
			EdgeExecution: &commandmodel.EdgeExecutionEvidence{Requested: requested, Effective: &requested, Applied: &requested, WinnerControllerID: "cloud-command-intent", Cycle: 1},
		}, nil
	case syntheticPreSendRejected:
		return commandmodel.ConnectorResult{Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "SYNTHETIC_PRE_SEND_REJECTION", EvidenceID: "synthetic:pre-send-rejected"}, nil
	case syntheticCommittedThenTimeout:
		return commandmodel.ConnectorResult{Phase: commandmodel.ConnectorRequestCommitted, FailureCode: "SYNTHETIC_ACK_TIMEOUT", EvidenceID: "synthetic:request-committed-timeout"}, nil
	default:
		return commandmodel.ConnectorResult{}, nil
	}
}

func testNumberPointer(value float64) *float64 {
	return &value
}

func testClock() func() time.Time {
	return func() time.Time {
		return time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	}
}

// Exercise the production durable seam with the real Command Service's
// acknowledgement, verification and fencing behavior behind a test adapter.
type syntheticDurableStore struct {
	service   *commandservice.Service
	commandID string
	now       func() time.Time
}

func (s syntheticDurableStore) ClaimDispatch(_ context.Context, _ string, owner string, lease time.Duration) (commandmodel.DispatchEnvelope, error) {
	return s.service.PrepareDispatch(s.commandID, owner, s.now().Add(lease))
}

func (s syntheticDurableStore) ResolveDispatch(_ context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	return s.service.ResolveDispatch(envelope, result)
}

type syntheticSafeDispatch struct{}

func (syntheticSafeDispatch) VerifyBeforeDispatch(context.Context, commandmodel.DispatchEnvelope) (DispatchSafetyResult, error) {
	return DispatchSafetyResult{Safe: true}, nil
}

func newSyntheticDurableDispatcher(service *commandservice.Service, commandID string, connector Connector, workerID string, now func() time.Time) *DurableDispatcher {
	return NewDurable(syntheticDurableStore{service, commandID, now}, syntheticSafeDispatch{}, connector, workerID, 30*time.Second)
}
