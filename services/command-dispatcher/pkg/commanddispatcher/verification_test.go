package commanddispatcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestSetpointVerifierRequiresNewFreshReportedState(t *testing.T) {
	envelope := verificationEnvelope()
	reader := &verificationReader{evidenceID: "s2-evidence-1", reported: validReportedState(envelope)}
	result, err := NewSetpointReportedStateVerifier(reader).Verify(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != commandmodel.VerificationSucceeded || result.EvidenceID != "s2-evidence-1" {
		t.Fatalf("unexpected verification result %#v", result)
	}

	reader.reported.BusinessRevision = envelope.BaselineBusinessRevision
	result, err = NewSetpointReportedStateVerifier(reader).Verify(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != commandmodel.VerificationInconclusive || result.FailureCode != "REPORTED_STATE_INCONCLUSIVE" {
		t.Fatalf("stale business revision was accepted: %#v", result)
	}
}

func TestSetpointVerifierClassifiesMismatch(t *testing.T) {
	envelope := verificationEnvelope()
	reported := validReportedState(envelope)
	reported.ReportedSetpointC = envelope.SetpointC - 1
	result, err := NewSetpointReportedStateVerifier(&verificationReader{evidenceID: "s2-evidence-2", reported: reported}).Verify(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != commandmodel.VerificationMismatch || result.FailureCode != "REPORTED_SETPOINT_MISMATCH" {
		t.Fatalf("mismatch classification=%#v", result)
	}
}

func TestDurableVerificationWorkerPreservesClaimBoundary(t *testing.T) {
	envelope := verificationEnvelope()
	store := &verificationStore{envelope: envelope}
	verifier := &verificationResultVerifier{result: commandmodel.VerificationResult{
		Outcome: commandmodel.VerificationSucceeded, EvidenceID: "s2-evidence-3", Reported: validReportedState(envelope),
	}}
	worker := NewDurableVerificationWorker(store, verifier, "verifier-a", 10*time.Second)
	if err := worker.RunOnce(context.Background(), envelope.OrganizationID); err != nil {
		t.Fatal(err)
	}
	if store.claimOrganization != envelope.OrganizationID || store.claimOwner != "verifier-a" || store.claimLease != 10*time.Second {
		t.Fatalf("claim boundary drifted: org=%s owner=%s lease=%s", store.claimOrganization, store.claimOwner, store.claimLease)
	}
	if store.resolvedEnvelope.AttemptID != envelope.AttemptID || store.resolvedResult.EvidenceID != "s2-evidence-3" {
		t.Fatalf("verification resolution drifted: envelope=%#v result=%#v", store.resolvedEnvelope, store.resolvedResult)
	}
}

func TestDurableVerificationWorkerDoesNotResolveReadFailure(t *testing.T) {
	envelope := verificationEnvelope()
	store := &verificationStore{envelope: envelope}
	worker := NewDurableVerificationWorker(store, &verificationResultVerifier{err: errors.New("s2 unavailable")}, "verifier-a", 10*time.Second)
	if err := worker.RunOnce(context.Background(), envelope.OrganizationID); err == nil {
		t.Fatal("expected S2 read failure")
	}
	if store.resolveCalls != 0 {
		t.Fatal("read failure was converted into a fabricated verification result")
	}
}

type verificationReader struct {
	evidenceID string
	reported   commandmodel.ReportedStateEvidence
	err        error
}

func (reader *verificationReader) ReadReportedState(context.Context, commandmodel.VerificationEnvelope) (string, commandmodel.ReportedStateEvidence, error) {
	return reader.evidenceID, reader.reported, reader.err
}

type verificationResultVerifier struct {
	result commandmodel.VerificationResult
	err    error
}

func (verifier *verificationResultVerifier) Verify(context.Context, commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, error) {
	return verifier.result, verifier.err
}

type verificationStore struct {
	envelope          commandmodel.VerificationEnvelope
	claimOrganization string
	claimOwner        string
	claimLease        time.Duration
	resolvedEnvelope  commandmodel.VerificationEnvelope
	resolvedResult    commandmodel.VerificationResult
	resolveCalls      int
}

func (store *verificationStore) ClaimVerification(_ context.Context, organizationID, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error) {
	store.claimOrganization = organizationID
	store.claimOwner = leaseOwner
	store.claimLease = leaseFor
	return store.envelope, nil
}

func (store *verificationStore) ResolveVerification(_ context.Context, envelope commandmodel.VerificationEnvelope, result commandmodel.VerificationResult) error {
	store.resolveCalls++
	store.resolvedEnvelope = envelope
	store.resolvedResult = result
	return nil
}

func verificationEnvelope() commandmodel.VerificationEnvelope {
	acknowledgedAt := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	return commandmodel.VerificationEnvelope{
		CommandID: "command-1", AttemptID: "attempt-1", OrganizationID: "org-1", SiteID: "site-1", DeviceID: "device-1",
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, CapabilityRevision: "capability:set-temperature-setpoint:v1",
		SetpointC: 24, PayloadHash: "payload-hash", ExecutionFence: 7, BaselineBusinessRevision: 17,
		AcknowledgedAt: acknowledgedAt, VerificationDeadline: acknowledgedAt.Add(2 * time.Minute),
		LeaseOwner: "verifier-a", LeaseUntil: acknowledgedAt.Add(10 * time.Second), ConnectorEvidenceID: "provider-ack-1",
	}
}

func validReportedState(envelope commandmodel.VerificationEnvelope) commandmodel.ReportedStateEvidence {
	return commandmodel.ReportedStateEvidence{
		OrganizationID: envelope.OrganizationID, SiteID: envelope.SiteID, DeviceID: envelope.DeviceID,
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: envelope.BaselineBusinessRevision + 1, ReportedSetpointC: envelope.SetpointC,
		ObservedAt: envelope.AcknowledgedAt.Add(time.Second),
	}
}
