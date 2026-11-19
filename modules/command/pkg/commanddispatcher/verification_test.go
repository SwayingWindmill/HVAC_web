package commanddispatcher

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestSetpointVerifierRequiresNewFreshReportedState(t *testing.T) {
	envelope := verificationEnvelope()
	reader := &verificationReader{evidenceID: "s2-evidence-1", reported: validReportedState(envelope)}
	result, err := NewAuthoritativeReportedStateVerifier(reader).Verify(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != commandmodel.VerificationSucceeded || result.EvidenceID != "s2-evidence-1" {
		t.Fatalf("unexpected verification result %#v", result)
	}

	reader.reported.BusinessRevision = envelope.BaselineBusinessRevision
	result, err = NewAuthoritativeReportedStateVerifier(reader).Verify(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != commandmodel.VerificationTimedOut || result.FailureCode != "REPORTED_STATE_VERIFICATION_TIMED_OUT" {
		t.Fatalf("stale business revision was accepted: %#v", result)
	}
}

func TestSetpointVerifierWaitsForNewReportedStateWithinDeadline(t *testing.T) {
	envelope := verificationEnvelope()
	envelope.AcknowledgedAt = time.Now().UTC().Add(-2 * time.Second)
	envelope.VerificationDeadline = time.Now().UTC().Add(2 * time.Second)
	stale := validReportedState(envelope)
	stale.BusinessRevision = envelope.BaselineBusinessRevision
	reader := &verificationReader{
		evidenceID:       "s2-evidence-retry",
		reportedSequence: []commandmodel.ReportedStateEvidence{stale, validReportedState(envelope)},
	}
	result, err := NewAuthoritativeReportedStateVerifier(reader).Verify(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != commandmodel.VerificationSucceeded || reader.calls < 2 {
		t.Fatalf("verifier did not wait for authoritative reported state: result=%#v calls=%d", result, reader.calls)
	}
}

func TestSetpointVerifierWaitsForSetpointConvergenceWithinDeadline(t *testing.T) {
	envelope := verificationEnvelope()
	envelope.AcknowledgedAt = time.Now().UTC().Add(-2 * time.Second)
	envelope.VerificationDeadline = time.Now().UTC().Add(2 * time.Second)
	mismatch := validReportedState(envelope)
	mismatch.ReportedValue = commandmodel.NumberScalar(envelope.Parameters[commandmodel.ParameterSetpointC] - 1)
	reader := &verificationReader{
		evidenceID:       "s2-evidence-converge",
		reportedSequence: []commandmodel.ReportedStateEvidence{mismatch, validReportedState(envelope)},
	}
	result, err := NewAuthoritativeReportedStateVerifier(reader).Verify(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != commandmodel.VerificationSucceeded || reader.calls < 2 {
		t.Fatalf("verifier did not wait for setpoint convergence: result=%#v calls=%d", result, reader.calls)
	}
}

func TestSetpointVerifierClassifiesMismatch(t *testing.T) {
	envelope := verificationEnvelope()
	reported := validReportedState(envelope)
	reported.ReportedValue = commandmodel.NumberScalar(envelope.Parameters[commandmodel.ParameterSetpointC] - 1)
	result, err := NewAuthoritativeReportedStateVerifier(&verificationReader{evidenceID: "s2-evidence-2", reported: reported}).Verify(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != commandmodel.VerificationMismatch || result.FailureCode != "REPORTED_VALUE_MISMATCH" {
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
	if err := worker.RunOnce(context.Background(), envelope.TenantID); err != nil {
		t.Fatal(err)
	}
	if store.claimOrganization != envelope.TenantID || store.claimOwner != "verifier-a" || store.claimLease != 10*time.Second {
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
	if err := worker.RunOnce(context.Background(), envelope.TenantID); err == nil {
		t.Fatal("expected S2 read failure")
	}
	if store.resolveCalls != 0 {
		t.Fatal("read failure was converted into a fabricated verification result")
	}
}

type verificationReader struct {
	evidenceID       string
	reported         commandmodel.ReportedStateEvidence
	reportedSequence []commandmodel.ReportedStateEvidence
	calls            int
	err              error
}

func (reader *verificationReader) ReadReportedState(context.Context, commandmodel.VerificationEnvelope) (string, commandmodel.ReportedStateEvidence, error) {
	reader.calls++
	if len(reader.reportedSequence) > 0 {
		index := reader.calls - 1
		if index >= len(reader.reportedSequence) {
			index = len(reader.reportedSequence) - 1
		}
		return reader.evidenceID, reader.reportedSequence[index], reader.err
	}
	return reader.evidenceID, reader.reported, reader.err
}

type verificationResultVerifier struct {
	result commandmodel.VerificationResult
	err    error
}

func (verifier *verificationResultVerifier) Verify(context.Context, commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, error) {
	return verifier.result, verifier.err
}

func (verifier *verificationResultVerifier) VerifyOnce(context.Context, commandmodel.VerificationEnvelope) (commandmodel.VerificationResult, error) {
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
		CommandID: "command-1", AttemptID: "attempt-1", TenantID: "org-1", SiteID: "site-1", DeviceID: "device-1",
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, CapabilityRevision: "capability:set-temperature-setpoint:v1",
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24}, VerificationPointKey: "zone.temperature_setpoint",
		PayloadHash: "payload-hash", ExecutionFence: 7, BaselineBusinessRevision: 17,
		AcknowledgedAt: acknowledgedAt, VerificationDeadline: acknowledgedAt.Add(2 * time.Minute),
		LeaseOwner: "verifier-a", LeaseUntil: acknowledgedAt.Add(10 * time.Second), ConnectorEvidenceID: "provider-ack-1",
	}
}

func validReportedState(envelope commandmodel.VerificationEnvelope) commandmodel.ReportedStateEvidence {
	return commandmodel.ReportedStateEvidence{
		TenantID: envelope.TenantID, SiteID: envelope.SiteID, DeviceID: envelope.DeviceID,
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: envelope.BaselineBusinessRevision + 1, ReportedValue: commandmodel.NumberScalar(envelope.Parameters[commandmodel.ParameterSetpointC]),
		ObservedAt: envelope.AcknowledgedAt.Add(time.Second),
	}
}

func TestAuthoritativeVerifierVerifyOnceNonBlocking(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	envelope := verificationEnvelope()
	envelope.AcknowledgedAt = now
	envelope.VerificationDeadline = now.Add(10 * time.Minute)

	stale := validReportedState(envelope)
	stale.BusinessRevision = envelope.BaselineBusinessRevision
	reader := &verificationReader{evidenceID: "s2-once-stale", reported: stale}
	verifier := NewAuthoritativeReportedStateVerifier(reader)
	verifier.now = func() time.Time { return now.Add(time.Second) }

	res, err := verifier.VerifyOnce(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != commandmodel.VerificationInconclusive || res.FailureCode != "" || reader.calls != 1 {
		t.Fatalf("VerifyOnce stale before deadline: res=%#v calls=%d", res, reader.calls)
	}

	mismatch := validReportedState(envelope)
	mismatch.ReportedValue = commandmodel.NumberScalar(21)
	reader.reported = mismatch
	res, err = verifier.VerifyOnce(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != commandmodel.VerificationInconclusive || reader.calls != 2 {
		t.Fatalf("VerifyOnce mismatch before deadline: res=%#v calls=%d", res, reader.calls)
	}

	verifier.now = func() time.Time { return envelope.VerificationDeadline.Add(time.Second) }
	res, err = verifier.VerifyOnce(context.Background(), envelope)
	if err != nil || res.Outcome != commandmodel.VerificationMismatch || res.FailureCode != "REPORTED_VALUE_MISMATCH" {
		t.Fatalf("VerifyOnce mismatch after deadline: res=%#v err=%v", res, err)
	}
	reader.reported = stale
	res, err = verifier.VerifyOnce(context.Background(), envelope)
	if err != nil || res.Outcome != commandmodel.VerificationTimedOut || res.FailureCode != "REPORTED_STATE_VERIFICATION_TIMED_OUT" {
		t.Fatalf("VerifyOnce stale after deadline: res=%#v err=%v", res, err)
	}

	verifier.now = func() time.Time { return now.Add(time.Second) }
	wrongScope := validReportedState(envelope)
	wrongScope.DeviceID = "other-device"
	reader.reported = wrongScope
	res, err = verifier.VerifyOnce(context.Background(), envelope)
	if err != nil || res.Outcome != commandmodel.VerificationInconclusive || res.FailureCode != "REPORTED_STATE_SCOPE_MISMATCH" {
		t.Fatalf("VerifyOnce scope mismatch: res=%#v err=%v", res, err)
	}
}

func TestDurableVerificationWorkerResolvesScopeMismatchBeforeDeadline(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	envelope := verificationEnvelope()
	envelope.AcknowledgedAt = now
	envelope.VerificationDeadline = now.Add(2 * time.Minute)
	store := &verificationStore{envelope: envelope}
	wrongScope := validReportedState(envelope)
	wrongScope.DeviceID = "device-wrong"
	verifier := NewAuthoritativeReportedStateVerifier(&verificationReader{evidenceID: "s2-scope", reported: wrongScope})
	verifier.now = func() time.Time { return now.Add(time.Second) }
	worker := NewDurableVerificationWorker(store, verifier, "verifier-a", 5*time.Second)
	worker.now = verifier.now

	if err := worker.RunOnce(context.Background(), envelope.TenantID); err != nil {
		t.Fatal(err)
	}
	if store.resolveCalls != 1 || store.resolvedResult.FailureCode != "REPORTED_STATE_SCOPE_MISMATCH" {
		t.Fatalf("expected immediate scope-mismatch resolution, calls=%d result=%#v", store.resolveCalls, store.resolvedResult)
	}
}

type queuedVerificationItem struct {
	envelope commandmodel.VerificationEnvelope
	resolved *commandmodel.VerificationResult
}

type multiCommandVerificationStore struct {
	now   func() time.Time
	items []*queuedVerificationItem
}

func (store *multiCommandVerificationStore) ClaimVerification(_ context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error) {
	now := store.now()
	for _, item := range store.items {
		if item.resolved != nil || item.envelope.TenantID != tenantID {
			continue
		}
		if !item.envelope.VerificationDeadline.After(now) {
			continue
		}
		if !item.envelope.LeaseUntil.IsZero() && item.envelope.LeaseUntil.After(now) {
			continue
		}
		leaseUntil := now.Add(leaseFor)
		if item.envelope.VerificationDeadline.Before(leaseUntil) {
			leaseUntil = item.envelope.VerificationDeadline
		}
		item.envelope.LeaseOwner = leaseOwner
		item.envelope.LeaseUntil = leaseUntil
		return item.envelope, nil
	}
	return commandmodel.VerificationEnvelope{}, commandservice.ErrVerificationNotAvailable
}

func (store *multiCommandVerificationStore) ResolveVerification(_ context.Context, envelope commandmodel.VerificationEnvelope, result commandmodel.VerificationResult) error {
	for _, item := range store.items {
		if item.envelope.AttemptID == envelope.AttemptID {
			if item.resolved != nil || item.envelope.LeaseOwner != envelope.LeaseOwner || !item.envelope.LeaseUntil.Equal(envelope.LeaseUntil) {
				return commandservice.ErrStaleFence
			}
			copied := result
			item.resolved = &copied
			return nil
		}
	}
	return commandservice.ErrCommandNotFound
}

type perCommandReportedReader struct {
	byDevice map[string]commandmodel.ReportedStateEvidence
}

func (reader *perCommandReportedReader) ReadReportedState(_ context.Context, envelope commandmodel.VerificationEnvelope) (string, commandmodel.ReportedStateEvidence, error) {
	return "s2-ev-" + envelope.DeviceID, reader.byDevice[envelope.DeviceID], nil
}

func TestDurableVerificationWorkerMultiCommandNonStarvation(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	cmdA := commandmodel.VerificationEnvelope{
		CommandID: "cmd-a", AttemptID: "att-a", TenantID: "org-1", SiteID: "site-1", DeviceID: "device-a",
		Capability: commandmodel.CapabilityStartUnitSequence, CapabilityRevision: "capability:start-unit-sequence:v1",
		Parameters: commandmodel.CommandParameters{}, VerificationPointKey: "equipment.run_state",
		PayloadHash: "hash-a", ExecutionFence: 1, BaselineBusinessRevision: 10,
		AcknowledgedAt: now, VerificationDeadline: now.Add(10 * time.Minute),
		ConnectorEvidenceID: "ack-a",
	}
	cmdB := commandmodel.VerificationEnvelope{
		CommandID: "cmd-b", AttemptID: "att-b", TenantID: "org-1", SiteID: "site-1", DeviceID: "device-b",
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, CapabilityRevision: "capability:set-temperature-setpoint:v1",
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24}, VerificationPointKey: "zone.temperature_setpoint",
		PayloadHash: "hash-b", ExecutionFence: 2, BaselineBusinessRevision: 20,
		AcknowledgedAt: now, VerificationDeadline: now.Add(2 * time.Minute),
		ConnectorEvidenceID: "ack-b",
	}

	now = now.Add(time.Second)
	store := &multiCommandVerificationStore{
		now:   clock,
		items: []*queuedVerificationItem{{envelope: cmdA}, {envelope: cmdB}},
	}
	reader := &perCommandReportedReader{
		byDevice: map[string]commandmodel.ReportedStateEvidence{
			"device-a": {
				TenantID: "org-1", SiteID: "site-1", DeviceID: "device-a",
				EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
				BusinessRevision: 10, ReportedValue: commandmodel.TextScalar("STOPPED"), ObservedAt: cmdA.AcknowledgedAt,
			},
			"device-b": {
				TenantID: "org-1", SiteID: "site-1", DeviceID: "device-b",
				EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
				BusinessRevision: 21, ReportedValue: commandmodel.NumberScalar(24), ObservedAt: now,
			},
		},
	}
	verifier := NewAuthoritativeReportedStateVerifier(reader)
	verifier.now = clock
	worker := NewDurableVerificationWorker(store, verifier, "verifier-a", 5*time.Second)
	worker.now = clock

	if err := worker.RunOnce(context.Background(), "org-1"); err != nil {
		t.Fatalf("tick 1 failed: %v", err)
	}
	if store.items[0].resolved != nil || !store.items[0].envelope.LeaseUntil.Equal(now.Add(5*time.Second)) {
		t.Fatalf("Command A should be paused with 5s lease, item=%#v", store.items[0])
	}

	if err := worker.RunOnce(context.Background(), "org-1"); err != nil {
		t.Fatalf("tick 2 failed: %v", err)
	}
	if store.items[1].resolved == nil || store.items[1].resolved.Outcome != commandmodel.VerificationSucceeded {
		t.Fatalf("Command B was starved or unresolved: %#v", store.items[1].resolved)
	}

	if err := worker.RunOnce(context.Background(), "org-1"); !errors.Is(err, commandservice.ErrVerificationNotAvailable) {
		t.Fatalf("expected ErrVerificationNotAvailable while Command A is leased, got %v", err)
	}

	now = now.Add(5 * time.Second)
	reader.byDevice["device-a"] = commandmodel.ReportedStateEvidence{
		TenantID: "org-1", SiteID: "site-1", DeviceID: "device-a",
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: 11, ReportedValue: commandmodel.TextScalar("RUNNING"), ObservedAt: now,
	}
	if err := worker.RunOnce(context.Background(), "org-1"); err != nil {
		t.Fatalf("tick 4 failed: %v", err)
	}
	if store.items[0].resolved == nil || store.items[0].resolved.Outcome != commandmodel.VerificationSucceeded {
		t.Fatalf("Command A did not settle after lease elapsed: %#v", store.items[0].resolved)
	}
}

type traceRecordingVerificationStore struct {
	envelope        commandmodel.VerificationEnvelope
	resolvedTraceID string
}

func (s *traceRecordingVerificationStore) ClaimVerification(_ context.Context, _, _ string, _ time.Duration) (commandmodel.VerificationEnvelope, error) {
	return s.envelope, nil
}

func (s *traceRecordingVerificationStore) ResolveVerification(ctx context.Context, _ commandmodel.VerificationEnvelope, _ commandmodel.VerificationResult) error {
	s.resolvedTraceID = observability.TraceID(ctx)
	return nil
}

type traceRecordingReportedReader struct {
	readTraceID string
	reported    commandmodel.ReportedStateEvidence
}

func (r *traceRecordingReportedReader) ReadReportedState(ctx context.Context, _ commandmodel.VerificationEnvelope) (string, commandmodel.ReportedStateEvidence, error) {
	r.readTraceID = observability.TraceID(ctx)
	return "ev-trace-1", r.reported, nil
}

func TestDurableVerificationWorkerPropagatesTraceContextAndLogs(t *testing.T) {
	const wantTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(observability.NewJSONLogger(&logs, slog.LevelInfo))
	t.Cleanup(func() { slog.SetDefault(previous) })

	env := verificationEnvelope()
	env.CommandID = "cmd-verify-trace-1"
	env.TraceID = "4BF92F3577B34DA6A3CE929D0E0E4736"
	store := &traceRecordingVerificationStore{envelope: env}
	reader := &traceRecordingReportedReader{reported: validReportedState(env)}
	verifier := NewAuthoritativeReportedStateVerifier(reader)
	worker := NewDurableVerificationWorker(store, verifier, "verifier-a", 15*time.Second)

	if err := worker.RunOnce(context.Background(), env.TenantID); err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}
	if reader.readTraceID != wantTrace || store.resolvedTraceID != wantTrace {
		t.Fatalf("trace mismatch read=%q resolve=%q want=%q", reader.readTraceID, store.resolvedTraceID, wantTrace)
	}
	logOutput := logs.String()
	if !strings.Contains(logOutput, `"command_id":"cmd-verify-trace-1"`) || !strings.Contains(logOutput, `"trace_id":"`+wantTrace+`"`) {
		t.Fatalf("missing structured command_id/trace_id in logs:\n%s", logOutput)
	}
}
