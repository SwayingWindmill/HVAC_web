package commanddispatcher

import (
	"context"
	"errors"
	"log/slog"
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
	VerifyOnce(ctx context.Context, envelope commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, error)
}

type AuthoritativeReportedStateVerifier struct {
	reader ReportedStateReader
	now    func() time.Time
}

func NewAuthoritativeReportedStateVerifier(reader ReportedStateReader) *AuthoritativeReportedStateVerifier {
	return &AuthoritativeReportedStateVerifier{reader: reader, now: time.Now}
}

func (verifier *AuthoritativeReportedStateVerifier) currentTime() time.Time {
	if verifier != nil && verifier.now != nil {
		return verifier.now()
	}
	return time.Now()
}

func (verifier *AuthoritativeReportedStateVerifier) remaining(envelope commandmodel.VerificationEnvelope) time.Duration {
	if envelope.VerificationDeadline.IsZero() {
		return 0
	}
	return envelope.VerificationDeadline.Sub(verifier.currentTime())
}

func (verifier *AuthoritativeReportedStateVerifier) evaluateReportedState(ctx context.Context, envelope commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, bool, error) {
	if verifier == nil || verifier.reader == nil {
		return commandmodel.VerificationResult{}, false, errors.New("reported-state reader is not configured")
	}
	ctx, _ = withCommandTraceContext(ctx, envelope.TraceID)
	evidenceID, reported, err := verifier.reader.ReadReportedState(ctx, envelope)
	if err != nil {
		return commandmodel.VerificationResult{}, false, err
	}
	if strings.TrimSpace(evidenceID) == "" {
		return commandmodel.VerificationResult{}, false, errors.New("reported-state evidence identifier is required")
	}
	result := commandmodel.VerificationResult{Outcome: commandmodel.VerificationInconclusive, EvidenceID: evidenceID, Reported: reported}
	if reported.TenantID != envelope.TenantID || reported.SiteID != envelope.SiteID || reported.DeviceID != envelope.DeviceID {
		result.FailureCode = "REPORTED_STATE_SCOPE_MISMATCH"
		return result, true, nil
	}
	authoritative := reported.EvaluationAvailability == "AVAILABLE" && reported.Presence == "ONLINE" && reported.Readiness == "CURRENT" &&
		reported.Freshness == "FRESH" && reported.Quality == "GOOD" && reported.BusinessRevision > envelope.BaselineBusinessRevision &&
		reported.ObservedAt.After(envelope.AcknowledgedAt)
	if authoritative {
		expected, expectedOK := commandmodel.ExpectedVerificationValue(envelope.Capability, envelope.Parameters, envelope.EdgeExecution)
		profile, supported := commandmodel.CapabilityProfileFor(envelope.Capability)
		if !expectedOK || !supported {
			return commandmodel.VerificationResult{}, false, errors.New("command capability cannot be verified")
		}
		if commandmodel.ScalarMatches(reported.ReportedValue, expected, profile.VerificationTolerance) {
			result.Outcome = commandmodel.VerificationSucceeded
			return result, true, nil
		}
		result.Outcome = commandmodel.VerificationMismatch
		result.FailureCode = "REPORTED_VALUE_MISMATCH"
	}
	return result, false, nil
}

// VerifyOnce checks reported state once without sleeping, returning VerificationInconclusive
// immediately when telemetry has not yet converged before VerificationDeadline.
func (verifier *AuthoritativeReportedStateVerifier) VerifyOnce(ctx context.Context, envelope commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, error) {
	result, terminal, err := verifier.evaluateReportedState(ctx, envelope)
	if err != nil || terminal {
		return result, err
	}
	remaining := verifier.remaining(envelope)
	if envelope.VerificationDeadline.IsZero() || remaining <= 0 {
		if result.Outcome == commandmodel.VerificationMismatch {
			return result, nil
		}
		result.Outcome = commandmodel.VerificationTimedOut
		result.FailureCode = "REPORTED_STATE_VERIFICATION_TIMED_OUT"
		return result, nil
	}
	result.Outcome = commandmodel.VerificationInconclusive
	result.FailureCode = ""
	return result, nil
}

func (verifier *AuthoritativeReportedStateVerifier) Verify(ctx context.Context, envelope commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, error) {
	if verifier == nil || verifier.reader == nil {
		return commandmodel.VerificationResult{}, errors.New("reported-state reader is not configured")
	}
	var lastMismatch *commandmodel.VerificationResult
	for {
		result, terminal, err := verifier.evaluateReportedState(ctx, envelope)
		if err != nil || terminal {
			return result, err
		}
		if result.Outcome == commandmodel.VerificationMismatch {
			mismatch := result
			lastMismatch = &mismatch
		}

		remaining := verifier.remaining(envelope)
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
	now      func() time.Time
}

func NewDurableVerificationWorker(store DurableVerificationStore, verifier ReportedStateVerifier, workerID string, leaseFor time.Duration) *DurableVerificationWorker {
	if leaseFor <= 0 {
		leaseFor = 5 * time.Second
	}
	return &DurableVerificationWorker{store: store, verifier: verifier, workerID: workerID, leaseFor: leaseFor, now: time.Now}
}

func (worker *DurableVerificationWorker) currentTime() time.Time {
	if worker != nil && worker.now != nil {
		return worker.now()
	}
	return time.Now()
}

func (worker *DurableVerificationWorker) RunOnce(ctx context.Context, tenantID string) error {
	if worker == nil || worker.store == nil || worker.verifier == nil || strings.TrimSpace(worker.workerID) == "" {
		return errors.New("verification worker is not configured")
	}
	envelope, err := worker.store.ClaimVerification(ctx, tenantID, worker.workerID, worker.leaseFor)
	if err != nil {
		return err
	}
	verifyCtx, traceID := withCommandTraceContext(ctx, envelope.TraceID)
	result, err := worker.verifier.VerifyOnce(verifyCtx, envelope)
	if err != nil {
		slog.WarnContext(verifyCtx, "command_verification_read_failed", "command_id", envelope.CommandID, "attempt_id", envelope.AttemptID, "execution_fence", envelope.ExecutionFence, "trace_id", traceID, "error", err.Error())
		return err
	}
	if result.Outcome == commandmodel.VerificationInconclusive &&
		result.FailureCode != "REPORTED_STATE_SCOPE_MISMATCH" &&
		!envelope.VerificationDeadline.IsZero() &&
		worker.currentTime().Before(envelope.VerificationDeadline) {
		slog.InfoContext(verifyCtx, "command_verification_deferred", "command_id", envelope.CommandID, "attempt_id", envelope.AttemptID, "execution_fence", envelope.ExecutionFence, "trace_id", traceID, "outcome", string(result.Outcome))
		return nil
	}
	if err := worker.store.ResolveVerification(verifyCtx, envelope, result); err != nil {
		slog.WarnContext(verifyCtx, "command_verification_resolve_failed", "command_id", envelope.CommandID, "attempt_id", envelope.AttemptID, "execution_fence", envelope.ExecutionFence, "trace_id", traceID, "outcome", string(result.Outcome), "failure_code", result.FailureCode, "error", err.Error())
		return err
	}
	slog.InfoContext(verifyCtx, "command_verification_resolved", "command_id", envelope.CommandID, "attempt_id", envelope.AttemptID, "execution_fence", envelope.ExecutionFence, "trace_id", traceID, "outcome", string(result.Outcome), "failure_code", result.FailureCode)
	return nil
}
