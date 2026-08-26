package commandservice

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestTransportAcknowledgementWithoutEdgeExecutionEvidenceCannotAdvance(t *testing.T) {
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
	err = service.ResolveDispatch(dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "transport-only-ack",
	})
	if !errors.Is(err, ErrUnsupportedResult) {
		t.Fatalf("transport-only ACK error=%v want=%v", err, ErrUnsupportedResult)
	}
	intent, _ := service.Get(submitted.Intent.ID)
	if intent.Status != commandmodel.IntentDispatching || intent.Attempts[0].Status != commandmodel.AttemptPrepared {
		t.Fatalf("transport-only ACK advanced state: intent=%s attempt=%s", intent.Status, intent.Attempts[0].Status)
	}
}

func TestEdgeExecutionRejectionFailsAttemptWithoutChangingApprovalFacts(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	submitted, err := service.Submit(validRequest())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := service.Get(submitted.Intent.ID)
	dispatch, err := service.PrepareDispatch(submitted.Intent.ID, "dispatcher-a", clock().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	requested := commandmodel.NumberScalar(24)
	rejection := &commandmodel.EdgeExecutionEvidence{
		Requested: requested, WinnerControllerID: "safety-interlock", Cycle: 9,
		Constraints: []commandmodel.EdgeConstraintEvidence{{ControllerID: "safety-interlock", Reason: "INTERLOCK_OPEN"}},
	}
	if err := service.ResolveDispatch(dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorExecutionRejected, FailureCode: "MQTT_EDGE_INTERLOCK_OPEN", EvidenceID: "edge-rejection-1", EdgeExecution: rejection,
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := service.Get(submitted.Intent.ID)
	if after.Status != commandmodel.IntentFailed || after.Attempts[0].Status != commandmodel.AttemptFailed {
		t.Fatalf("Edge rejection did not become failed execution: intent=%s attempt=%s", after.Status, after.Attempts[0].Status)
	}
	if after.ApprovalPolicy != before.ApprovalPolicy || !reflect.DeepEqual(after.Approvals, before.Approvals) || !reflect.DeepEqual(after.Authorizations, before.Authorizations) {
		t.Fatal("Edge rejection mutated immutable Cloud approval/authorization facts")
	}
}

func TestIndependentReadbackUsesConstrainedEdgeEffectiveValue(t *testing.T) {
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
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "edge-constrained-ack", EdgeExecution: testEdgeExecution(23),
	}); err != nil {
		t.Fatal(err)
	}
	verification, err := service.PrepareVerification(submitted.Intent.ID, "verifier-a", clock().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResolveVerification(verification, commandmodel.VerificationResult{
		Outcome: commandmodel.VerificationSucceeded, EvidenceID: "s2-effective-readback",
		Reported: commandmodel.ReportedStateEvidence{
			TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
			BusinessRevision: verification.BaselineBusinessRevision + 1, ReportedValue: commandmodel.NumberScalar(23),
			ObservedAt: verification.AcknowledgedAt.Add(time.Second),
		},
	}); err != nil {
		t.Fatal(err)
	}
	intent, _ := service.Get(submitted.Intent.ID)
	if intent.Status != commandmodel.IntentSucceeded || intent.Parameters[commandmodel.ParameterSetpointC] != 24 {
		t.Fatalf("effective readback failed or mutated original intent: status=%s parameters=%v", intent.Status, intent.Parameters)
	}
}

func testEdgeExecution(effective float64) *commandmodel.EdgeExecutionEvidence {
	requested := commandmodel.NumberScalar(24)
	effectiveValue := commandmodel.NumberScalar(effective)
	return &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: &effectiveValue, Applied: &effectiveValue,
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}
}
