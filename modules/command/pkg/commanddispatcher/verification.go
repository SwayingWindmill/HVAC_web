package commanddispatcher

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const reportedStateRetryInterval = 100 * time.Millisecond

type DurableVerificationStore interface {
	ClaimVerification(ctx context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error)
	ResolveVerification(ctx context.Context, envelope commandmodel.VerificationEnvelope, result commandmodel.VerificationResult) error
}

type ReportedStateReader interface {
	ReadReportedState(ctx context.Context, envelope commandmodel.VerificationEnvelope) (string, commandmodel.ReportedStateEvidence, error)
}

type ReportedStateVerifier interface {
	Verify(ctx context.Context, envelope commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, error)
}

type AuthoritativeReportedStateVerifier struct {
	reader ReportedStateReader
}

func NewAuthoritativeReportedStateVerifier(reader ReportedStateReader) *AuthoritativeReportedStateVerifier {
	return &AuthoritativeReportedStateVerifier{reader: reader}
}

func (verifier *AuthoritativeReportedStateVerifier) Verify(ctx context.Context, envelope commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, error) {
	if verifier == nil || verifier.reader == nil {
		return commandmodel.VerificationResult{}, errors.New("reported-state reader is not configured")
	}
	var lastMismatch *commandmodel.VerificationResult
	for {
		evidenceID, reported, err := verifier.reader.ReadReportedState(ctx, envelope)
		if err != nil {
			return commandmodel.VerificationResult{}, err
		}
		if strings.TrimSpace(evidenceID) == "" {
			return commandmodel.VerificationResult{}, errors.New("reported-state evidence identifier is required")
		}
		result := commandmodel.VerificationResult{Outcome: commandmodel.VerificationInconclusive, EvidenceID: evidenceID, Reported: reported}
		if reported.TenantID != envelope.TenantID || reported.SiteID != envelope.SiteID || reported.DeviceID != envelope.DeviceID {
			result.FailureCode = "REPORTED_STATE_SCOPE_MISMATCH"
			return result, nil
		}
		authoritative := reported.EvaluationAvailability == "AVAILABLE" && reported.Presence == "ONLINE" && reported.Readiness == "CURRENT" &&
			reported.Freshness == "FRESH" && reported.Quality == "GOOD" && reported.BusinessRevision > envelope.BaselineBusinessRevision &&
			reported.ObservedAt.After(envelope.AcknowledgedAt)
		if authoritative {
			expected, expectedOK := commandmodel.ExpectedVerificationValue(envelope.Capability, envelope.Parameters, envelope.EdgeExecution)
			profile, supported := commandmodel.CapabilityProfileFor(envelope.Capability)
			if !expectedOK || !supported {
				return commandmodel.VerificationResult{}, errors.New("command capability cannot be verified")
			}
			if commandmodel.ScalarMatches(reported.ReportedValue, expected, profile.VerificationTolerance) {
				result.Outcome = commandmodel.VerificationSucceeded
				return result, nil
			}
			result.Outcome = commandmodel.VerificationMismatch
			result.FailureCode = "REPORTED_VALUE_MISMATCH"
			lastMismatch = &result
		}

		remaining := time.Until(envelope.VerificationDeadline)
		if envelope.VerificationDeadline.IsZero() || remaining <= 0 {
			if lastMismatch != nil {
				return *lastMismatch, nil
			}
			result.Outcome = commandmodel.VerificationTimedOut
			result.FailureCode = "REPORTED_STATE_VERIFICATION_TIMED_OUT"
			return result, nil
		}
		waitFor := reportedStateRetryInterval
		if remaining < waitFor {
			waitFor = remaining
		}
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return commandmodel.VerificationResult{}, ctx.Err()
		case <-timer.C:
		}
	}
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

func (worker *DurableVerificationWorker) RunOnce(ctx context.Context, tenantID string) error {
	if worker == nil || worker.store == nil || worker.verifier == nil || strings.TrimSpace(worker.workerID) == "" {
		return errors.New("verification worker is not configured")
	}
	envelope, err := worker.store.ClaimVerification(ctx, tenantID, worker.workerID, worker.leaseFor)
	if err != nil {
		return err
	}
	result, err := worker.verifier.Verify(ctx, envelope)
	if err != nil {
		return err
	}
	return worker.store.ResolveVerification(ctx, envelope, result)
}
