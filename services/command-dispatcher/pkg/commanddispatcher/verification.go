package commanddispatcher

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const reportedSetpointToleranceC = 0.1

type DurableVerificationStore interface {
	ClaimVerification(ctx context.Context, organizationID, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error)
	ResolveVerification(ctx context.Context, envelope commandmodel.VerificationEnvelope, result commandmodel.VerificationResult) error
}

type ReportedStateReader interface {
	ReadReportedState(ctx context.Context, envelope commandmodel.VerificationEnvelope) (string, commandmodel.ReportedStateEvidence, error)
}

type ReportedStateVerifier interface {
	Verify(ctx context.Context, envelope commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, error)
}

type SetpointReportedStateVerifier struct {
	reader ReportedStateReader
}

func NewSetpointReportedStateVerifier(reader ReportedStateReader) *SetpointReportedStateVerifier {
	return &SetpointReportedStateVerifier{reader: reader}
}

func (verifier *SetpointReportedStateVerifier) Verify(ctx context.Context, envelope commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, error) {
	if verifier == nil || verifier.reader == nil {
		return commandmodel.VerificationResult{}, errors.New("reported-state reader is not configured")
	}
	evidenceID, reported, err := verifier.reader.ReadReportedState(ctx, envelope)
	if err != nil {
		return commandmodel.VerificationResult{}, err
	}
	if strings.TrimSpace(evidenceID) == "" {
		return commandmodel.VerificationResult{}, errors.New("reported-state evidence identifier is required")
	}
	result := commandmodel.VerificationResult{Outcome: commandmodel.VerificationInconclusive, EvidenceID: evidenceID, Reported: reported}
	if reported.OrganizationID != envelope.OrganizationID || reported.SiteID != envelope.SiteID || reported.DeviceID != envelope.DeviceID {
		result.FailureCode = "REPORTED_STATE_SCOPE_MISMATCH"
		return result, nil
	}
	if reported.EvaluationAvailability != "AVAILABLE" || reported.Presence != "ONLINE" || reported.Readiness != "CURRENT" ||
		reported.Freshness != "FRESH" || reported.Quality != "GOOD" || reported.BusinessRevision <= envelope.BaselineBusinessRevision ||
		!reported.ObservedAt.After(envelope.AcknowledgedAt) {
		result.FailureCode = "REPORTED_STATE_INCONCLUSIVE"
		return result, nil
	}
	if math.Abs(reported.ReportedSetpointC-envelope.SetpointC) > reportedSetpointToleranceC {
		result.Outcome = commandmodel.VerificationMismatch
		result.FailureCode = "REPORTED_SETPOINT_MISMATCH"
		return result, nil
	}
	result.Outcome = commandmodel.VerificationSucceeded
	return result, nil
}

type DurableVerificationWorker struct {
	store    DurableVerificationStore
	verifier ReportedStateVerifier
	workerID string
	leaseFor time.Duration
}

func NewDurableVerificationWorker(store DurableVerificationStore, verifier ReportedStateVerifier, workerID string, leaseFor time.Duration) *DurableVerificationWorker {
	if leaseFor <= 0 {
		leaseFor = 15 * time.Second
	}
	return &DurableVerificationWorker{store: store, verifier: verifier, workerID: workerID, leaseFor: leaseFor}
}

func (worker *DurableVerificationWorker) RunOnce(ctx context.Context, organizationID string) error {
	if worker == nil || worker.store == nil || worker.verifier == nil || strings.TrimSpace(worker.workerID) == "" {
		return errors.New("verification worker is not configured")
	}
	envelope, err := worker.store.ClaimVerification(ctx, organizationID, worker.workerID, worker.leaseFor)
	if err != nil {
		return err
	}
	result, err := worker.verifier.Verify(ctx, envelope)
	if err != nil {
		return err
	}
	return worker.store.ResolveVerification(ctx, envelope, result)
}
