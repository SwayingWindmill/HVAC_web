package commandservice

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestPostgresConcurrentDispatchClaimHasOneWinner(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()
	created, err := store.Submit(ctx, postgresCommandRequest("dispatch-race", 24))
	if err != nil {
		t.Fatal(err)
	}

	results := make(chan commandmodel.DispatchEnvelope, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for _, worker := range []string{"dispatcher-a", "dispatcher-b"} {
		worker := worker
		go func() {
			defer wait.Done()
			envelope, claimErr := store.ClaimDispatch(ctx, commandTenantA, worker, 10*time.Second)
			if claimErr != nil {
				errorsFound <- claimErr
				return
			}
			results <- envelope
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)

	var winner commandmodel.DispatchEnvelope
	wins := 0
	for result := range results {
		winner = result
		wins++
	}
	noWork := 0
	for claimErr := range errorsFound {
		if errors.Is(claimErr, ErrNoDispatchAvailable) {
			noWork++
			continue
		}
		t.Fatalf("unexpected concurrent claim error: %v", claimErr)
	}
	if wins != 1 || noWork != 1 || winner.CommandID != created.Intent.ID || winner.ExecutionFence != 1 {
		t.Fatalf("claim race wins=%d noWork=%d winner=%#v", wins, noWork, winner)
	}
	acknowledgeAndVerifyPostgres(t, store, admin, now, winner, "synthetic-race", 1)
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "SUCCEEDED", "VERIFIED", 1, false)
}

func TestPostgresPreSendRetryAdvancesFenceAndRejectsOldAttempt(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()
	created, err := store.Submit(ctx, postgresCommandRequest("dispatch-pre-send", 24))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveDispatch(ctx, first, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "SYNTHETIC_PRE_SEND",
		EvidenceID: "synthetic-not-sent-1",
	}); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-b", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if second.ExecutionFence <= first.ExecutionFence || second.AttemptID == first.AttemptID {
		t.Fatalf("fence/attempt did not advance first=%#v second=%#v", first, second)
	}
	if err := store.ResolveDispatch(ctx, first, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true,
		EvidenceID: "late-old-fence", EdgeExecution: postgresEdgeExecution(t, first),
	}); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("old attempt was not fenced: %v", err)
	}
	acknowledgeAndVerifyPostgres(t, store, admin, now, second, "synthetic-retry", 2)
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "SUCCEEDED", "VERIFIED", 2, false)
}

func TestPostgresExpiredPreparedLeaseFreezesOutcomeUnknown(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()
	created, err := store.Submit(ctx, postgresCommandRequest("dispatch-lease-expiry", 24))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(3 * time.Second)
	if _, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-b", 10*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("expired prepared attempt was blindly reassigned: %v", err)
	}
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "OUTCOME_UNKNOWN", "OUTCOME_UNKNOWN", 1, true)
	if err := store.ResolveDispatch(ctx, first, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true,
		EvidenceID: "late-after-expiry", EdgeExecution: postgresEdgeExecution(t, first),
	}); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expired worker result did not observe frozen state: %v", err)
	}
}

func TestPostgresDeviceControlLaneIsSerial(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()
	firstCommand, err := store.Submit(ctx, postgresCommandRequest("dispatch-lane-1", 24))
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := postgresCommandRequest("dispatch-lane-2", 23.5)
	secondCommand, err := store.Submit(ctx, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil || first.CommandID != firstCommand.Intent.ID {
		t.Fatalf("first lane claim=%#v err=%v", first, err)
	}
	if _, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-b", 10*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("second device command bypassed active lane: %v", err)
	}
	acknowledgeAndVerifyPostgres(t, store, admin, now, first, "lane-first", 1)
	second, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-b", 10*time.Second)
	if err != nil || second.CommandID != secondCommand.Intent.ID {
		t.Fatalf("second lane claim=%#v err=%v", second, err)
	}
}

func TestPostgresReportedStateMismatchFreezesOutcomeUnknown(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()
	created, err := store.Submit(ctx, postgresCommandRequest("verification-mismatch", 24))
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-mismatch",
		EdgeExecution: recordAcknowledgedConnectorEvidence(t, store, *now, dispatch),
	}); err != nil {
		t.Fatal(err)
	}
	verification, err := store.ClaimVerification(ctx, commandTenantA, "verifier-a", 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	if err := store.ResolveVerification(ctx, verification, commandmodel.VerificationResult{
		Outcome: commandmodel.VerificationMismatch, EvidenceID: "s2-mismatch",
		Reported: postgresReportedState(verification, 21),
	}); err != nil {
		t.Fatal(err)
	}
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "OUTCOME_UNKNOWN", "OUTCOME_UNKNOWN", 1, true)
	if _, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-b", 10*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("mismatched reported state was retryable: %v", err)
	}
}

func TestPostgresVerificationLeaseUsesDatabaseTimestampPrecision(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	*now = time.Date(2026, 7, 26, 11, 0, 0, 123456789, time.UTC)
	ctx := t.Context()
	created, err := store.Submit(ctx, postgresCommandRequest("verification-timestamp-precision", 24))
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-timestamp-precision",
		EdgeExecution: recordAcknowledgedConnectorEvidence(t, store, *now, dispatch),
	}); err != nil {
		t.Fatal(err)
	}
	verification, err := store.ClaimVerification(ctx, commandTenantA, "verifier-a", 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if verification.LeaseUntil.Nanosecond()%int(time.Microsecond) != 0 {
		t.Fatalf("verification LeaseUntil was not normalized to PostgreSQL precision: %s", verification.LeaseUntil.Format(time.RFC3339Nano))
	}
	*now = now.Add(time.Second)
	if err := store.ResolveVerification(ctx, verification, commandmodel.VerificationResult{
		Outcome: commandmodel.VerificationSucceeded, EvidenceID: "s2-timestamp-precision",
		Reported: postgresReportedState(verification, postgresVerificationNumber(t, verification)),
	}); err != nil {
		t.Fatalf("verification resolution failed after PostgreSQL timestamp round trip: %v", err)
	}
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "SUCCEEDED", "VERIFIED", 1, false)
}

func TestPostgresExpiredReportedStateVerificationFreezesOutcomeUnknown(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()
	created, err := store.Submit(ctx, postgresCommandRequest("verification-expired", 24))
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-expired",
		EdgeExecution: recordAcknowledgedConnectorEvidence(t, store, *now, dispatch),
	}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(commandmodel.VerificationWindowFor(commandmodel.CapabilitySetTemperatureSetpoint) + time.Second)
	if _, err := store.ClaimVerification(ctx, commandTenantA, "verifier-a", 15*time.Second); !errors.Is(err, ErrVerificationNotAvailable) {
		t.Fatalf("expired verification was leased: %v", err)
	}
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "OUTCOME_UNKNOWN", "OUTCOME_UNKNOWN", 1, true)
}

func TestPostgresConnectorCannotSelfVerify(t *testing.T) {
	store, admin, _, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()
	created, err := store.Submit(ctx, postgresCommandRequest("connector-self-verify", 24))
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	err = store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, Verified: true, EvidenceID: "provider-self-verified",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("connector self-verification was accepted: %v", err)
	}
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "DISPATCHING", "PREPARED", 1, false)
}

func acknowledgeAndVerifyPostgres(t *testing.T, store *PostgresStore, admin *pgxpool.Pool, now *time.Time, dispatch commandmodel.DispatchEnvelope, evidencePrefix string, attempts int) {
	t.Helper()
	ctx := t.Context()
	if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true,
		EvidenceID: evidencePrefix + "-provider-ack", EdgeExecution: recordAcknowledgedConnectorEvidence(t, store, *now, dispatch),
	}); err != nil {
		t.Fatal(err)
	}
	assertDispatchDatabaseState(t, admin, dispatch.CommandID, "DISPATCHING", "ACKNOWLEDGED", attempts, false)
	verification, err := store.ClaimVerification(ctx, dispatch.TenantID, "verifier-a", 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	if err := store.ResolveVerification(ctx, verification, commandmodel.VerificationResult{
		Outcome:    commandmodel.VerificationSucceeded,
		EvidenceID: evidencePrefix + "-s2-reported-state",
		Reported:   postgresReportedState(verification, postgresVerificationNumber(t, verification)),
	}); err != nil {
		t.Fatal(err)
	}
}

func postgresVerificationNumber(t *testing.T, envelope commandmodel.VerificationEnvelope) float64 {
	t.Helper()
	value, ok := commandmodel.ExpectedVerificationValue(envelope.Capability, envelope.Parameters, envelope.EdgeExecution)
	if !ok || value.Number == nil {
		t.Fatalf("verification envelope has no numeric governed target: %#v", envelope)
	}
	return *value.Number
}

func postgresEdgeExecution(t *testing.T, envelope commandmodel.DispatchEnvelope) *commandmodel.EdgeExecutionEvidence {
	t.Helper()
	value, ok := commandmodel.ParameterValue(envelope.Capability, envelope.Parameters)
	if !ok {
		t.Fatalf("dispatch envelope has no numeric parameter: %#v", envelope)
	}
	requested := commandmodel.NumberScalar(value)
	return &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: &requested, Applied: &requested,
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}
}

func postgresReportedState(envelope commandmodel.VerificationEnvelope, setpointC float64) commandmodel.ReportedStateEvidence {
	return commandmodel.ReportedStateEvidence{
		TenantID: envelope.TenantID, SiteID: envelope.SiteID, DeviceID: envelope.DeviceID,
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: envelope.BaselineBusinessRevision + 1, ReportedValue: commandmodel.NumberScalar(setpointC),
		ObservedAt: envelope.AcknowledgedAt.Add(time.Second),
	}
}

func postgresDispatchFixture(t *testing.T) (*PostgresStore, *pgxpool.Pool, *time.Time, func()) {
	t.Helper()
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	resetCommandFixture(t, admin)
	opened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)
	store := NewPostgresStore(opened.pool, func() time.Time { return now }, nil)
	return store, admin, &now, func() {
		opened.Close()
		admin.Close()
	}
}

func assertDispatchDatabaseState(t *testing.T, admin *pgxpool.Pool, commandID, intentStatus, attemptStatus string, attempts int, frozen bool) {
	t.Helper()
	ctx := t.Context()
	var actualIntentStatus, latestAttemptStatus string
	var attemptCount int
	var frozenState bool
	if err := admin.QueryRow(ctx, `
SELECT i.status,
       COALESCE((SELECT a.status FROM command_runtime.command_attempts a WHERE a.command_id = i.command_id ORDER BY a.attempt_number DESC LIMIT 1), ''),
       (SELECT count(*) FROM command_runtime.command_attempts a WHERE a.command_id = i.command_id),
       d.frozen_control_groups ? 'SETPOINT'
FROM command_runtime.command_intents i
JOIN command_runtime.device_control_state d
  ON d.tenant_id = i.tenant_id AND d.device_id = i.device_id
WHERE i.command_id = $1::uuid
`, commandID).Scan(&actualIntentStatus, &latestAttemptStatus, &attemptCount, &frozenState); err != nil {
		t.Fatal(err)
	}
	if actualIntentStatus != intentStatus || latestAttemptStatus != attemptStatus || attemptCount != attempts || frozenState != frozen {
		t.Fatalf("dispatch state intent=%s attempt=%s attempts=%d frozen=%v", actualIntentStatus, latestAttemptStatus, attemptCount, frozenState)
	}
}

// recordAcknowledgedConnectorEvidence records what the connector persists before an
// acknowledged dispatch is resolved: verification is only claimable for an attempt with
// durable ACKNOWLEDGED connector evidence and executed Edge evidence.
func recordAcknowledgedConnectorEvidence(t *testing.T, store *PostgresStore, at time.Time, dispatch commandmodel.DispatchEnvelope) *commandmodel.EdgeExecutionEvidence {
	t.Helper()
	prepared := commandmodel.PreparedConnectorEvidence{
		AttemptID: dispatch.AttemptID, CommandID: dispatch.CommandID,
		TenantID: dispatch.TenantID, SiteID: dispatch.SiteID, DeviceID: dispatch.DeviceID,
		ExternalDeviceID: "CHILLER-01", ExecutionFence: dispatch.ExecutionFence, PayloadHash: dispatch.PayloadHash,
		MappingRevision: "mqtt-command-v1", BindingRevision: "registry-source-key:1",
		ProviderEndpoint: "hvac/v1/018f3e00-4000-7000-8000-000000000100/down/command", ProviderMethod: "setTemperatureSetpoint",
		RequestSHA256: strings.Repeat("a", 64), PreparedAt: at,
	}
	if err := store.PrepareConnectorEvidence(t.Context(), prepared); err != nil {
		t.Fatalf("prepare connector evidence: %v", err)
	}
	edge := postgresEdgeExecution(t, dispatch)
	if err := store.CompleteConnectorEvidence(t.Context(), commandmodel.CompletedConnectorEvidence{
		PreparedConnectorEvidence: prepared, RequestWritten: true, ConnectorPhase: commandmodel.ConnectorAcknowledged,
		ProviderStatusCode: 200, ResponseSHA256: strings.Repeat("b", 64), EdgeExecution: edge, CompletedAt: at,
	}); err != nil {
		t.Fatalf("complete connector evidence: %v", err)
	}
	return edge
}

func recordAcknowledgedTextConnectorEvidence(t *testing.T, store *PostgresStore, at time.Time, dispatch commandmodel.DispatchEnvelope, edge *commandmodel.EdgeExecutionEvidence) *commandmodel.EdgeExecutionEvidence {
	t.Helper()
	prepared := commandmodel.PreparedConnectorEvidence{
		AttemptID: dispatch.AttemptID, CommandID: dispatch.CommandID,
		TenantID: dispatch.TenantID, SiteID: dispatch.SiteID, DeviceID: dispatch.DeviceID,
		ExternalDeviceID: "CHILLER-01", ExecutionFence: dispatch.ExecutionFence, PayloadHash: dispatch.PayloadHash,
		MappingRevision: "mqtt-command-v1", BindingRevision: "registry-source-key:1",
		ProviderEndpoint: "hvac/v1/018f3e00-4000-7000-8000-000000000100/down/command", ProviderMethod: string(dispatch.Capability),
		RequestSHA256: strings.Repeat("a", 64), PreparedAt: at,
	}
	if err := store.PrepareConnectorEvidence(t.Context(), prepared); err != nil {
		t.Fatalf("prepare connector evidence: %v", err)
	}
	if err := store.CompleteConnectorEvidence(t.Context(), commandmodel.CompletedConnectorEvidence{
		PreparedConnectorEvidence: prepared, RequestWritten: true, ConnectorPhase: commandmodel.ConnectorAcknowledged,
		ProviderStatusCode: 200, ResponseSHA256: strings.Repeat("b", 64), EdgeExecution: edge, CompletedAt: at,
	}); err != nil {
		t.Fatalf("complete connector evidence: %v", err)
	}
	return edge
}

func postgresStartStopCommandRequest(idempotencyKey string, capability commandmodel.Capability, at time.Time) commandmodel.SubmitRequest {
	profile, _ := commandmodel.CapabilityProfileFor(capability)
	return commandmodel.SubmitRequest{
		TenantID: commandTenantA, SiteID: commandSiteA, DeviceID: commandDeviceA, PointID: commandPointA,
		PrincipalID: commandPrincipalA, IdempotencyKey: idempotencyKey, Capability: capability,
		Parameters: commandmodel.CommandParameters{}, VerificationPointKey: "equipment.run_state",
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Quality: "GOOD",
			BusinessRevision: 21, ObservedAt: at,
		},
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID: "018f3e00-9000-7000-8000-000000000002", PolicyRevision: "command-policy-1",
			Purpose: commandmodel.AuthorizationCommandSubmit, PrincipalID: commandPrincipalA,
			TenantID: commandTenantA, SiteID: commandSiteA, DeviceID: commandDeviceA,
			Capability: capability, MaximumRisk: commandmodel.RiskHigh, CapabilityRevision: profile.Revision,
			EmergencyRevocationRevision: 1,
			IssuedAt:                    at.Add(-5 * time.Second),
			ExpiresAt:                   at.Add(25 * time.Second),
		},
	}
}

func TestPostgresExpiredPulseStartDoesNotFreezeAndUnblocksFollowup(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()

	created, err := store.Submit(ctx, postgresStartStopCommandRequest("pulse-start-expired", commandmodel.CapabilityStart, *now))
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pulseText := "u14:pulse(500ms)->0"
	edge := &commandmodel.EdgeExecutionEvidence{
		Requested: commandmodel.TextScalar("START"),
		Effective: &commandmodel.ScalarValue{Text: &pulseText}, Applied: &commandmodel.ScalarValue{Text: &pulseText},
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}
	if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-pulse",
		EdgeExecution: recordAcknowledgedTextConnectorEvidence(t, store, *now, dispatch, edge),
	}); err != nil {
		t.Fatal(err)
	}

	*now = now.Add(commandmodel.VerificationWindowFor(commandmodel.CapabilityStart) + time.Second)
	if _, err := store.ClaimVerification(ctx, commandTenantA, "verifier-a", 15*time.Second); !errors.Is(err, ErrVerificationNotAvailable) {
		t.Fatalf("expired pulse verification was leased: %v", err)
	}
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "FAILED", "FAILED", 1, false)

	var attemptEvidence, transitionReason string
	if err := admin.QueryRow(ctx, `
SELECT COALESCE(a.verification_evidence_id, ''),
       COALESCE((SELECT t.reason FROM command_runtime.command_transitions t WHERE t.command_id = a.command_id ORDER BY t.command_version DESC LIMIT 1), '')
FROM command_runtime.command_attempts a
WHERE a.command_id = $1::uuid
`, created.Intent.ID).Scan(&attemptEvidence, &transitionReason); err != nil {
		t.Fatal(err)
	}
	if attemptEvidence != commandmodel.ReasonPulseDeliveredPowerUnconfirmed || transitionReason != commandmodel.ReasonPulseDeliveredPowerUnconfirmed {
		t.Fatalf("unexpected evidence/reason: evidence=%q reason=%q", attemptEvidence, transitionReason)
	}

	// Subsequent STOP command on the same device must NOT be blocked by a frozen control group.
	followup, err := store.Submit(ctx, postgresStartStopCommandRequest("pulse-followup-stop", commandmodel.CapabilityStop, *now))
	if err != nil {
		t.Fatal(err)
	}
	nextDispatch, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-b", 10*time.Second)
	if err != nil {
		t.Fatalf("follow-up STOP dispatch was deadlocked after pulse failure: %v", err)
	}
	if nextDispatch.CommandID != followup.Intent.ID {
		t.Fatalf("claimed unexpected command %s want %s", nextDispatch.CommandID, followup.Intent.ID)
	}
}

func TestPostgresExpiredLevelStartStillFreezesControlGroup(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()

	created, err := store.Submit(ctx, postgresStartStopCommandRequest("level-start-expired", commandmodel.CapabilityStart, *now))
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	levelText := "u11:1"
	edge := &commandmodel.EdgeExecutionEvidence{
		Requested: commandmodel.TextScalar("START"),
		Effective: &commandmodel.ScalarValue{Text: &levelText}, Applied: &commandmodel.ScalarValue{Text: &levelText},
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}
	if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-level",
		EdgeExecution: recordAcknowledgedTextConnectorEvidence(t, store, *now, dispatch, edge),
	}); err != nil {
		t.Fatal(err)
	}

	*now = now.Add(commandmodel.VerificationWindowFor(commandmodel.CapabilityStart) + time.Second)
	if _, err := store.ClaimVerification(ctx, commandTenantA, "verifier-a", 15*time.Second); !errors.Is(err, ErrVerificationNotAvailable) {
		t.Fatalf("expired level verification was leased: %v", err)
	}
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "OUTCOME_UNKNOWN", "OUTCOME_UNKNOWN", 1, true)
}

func TestPostgresExpiredEmergencyDoReleaseAndUnitSequenceDoNotFreeze(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		capability commandmodel.Capability
		edgeText   string
	}{
		{
			name:       "emergency DO release STOP expires without freeze",
			key:        "edo-stop-expired",
			capability: commandmodel.CapabilityStop,
			edgeText:   "all-do:0",
		},
		{
			name:       "unit sequence acceptance expires without freeze",
			key:        "unit-seq-start-expired",
			capability: commandmodel.CapabilityStartUnitSequence,
			edgeText:   "chiller-1:sequence-accepted",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, admin, now, cleanup := postgresDispatchFixture(t)
			defer cleanup()
			ctx := t.Context()

			created, err := store.Submit(ctx, postgresStartStopCommandRequest(tc.key, tc.capability, *now))
			if err != nil {
				t.Fatal(err)
			}
			dispatch, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			applied := tc.edgeText
			edge := &commandmodel.EdgeExecutionEvidence{
				Requested: commandmodel.TextScalar(string(tc.capability)),
				Effective: &commandmodel.ScalarValue{Text: &applied}, Applied: &commandmodel.ScalarValue{Text: &applied},
				WinnerControllerID: "cloud-command-intent", Cycle: 1,
			}
			if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
				Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-" + tc.key,
				EdgeExecution: recordAcknowledgedTextConnectorEvidence(t, store, *now, dispatch, edge),
			}); err != nil {
				t.Fatal(err)
			}

			*now = now.Add(commandmodel.VerificationWindowFor(tc.capability) + time.Second)
			if _, err := store.ClaimVerification(ctx, commandTenantA, "verifier-a", 15*time.Second); !errors.Is(err, ErrVerificationNotAvailable) {
				t.Fatalf("expired verification was leased: %v", err)
			}
			assertDispatchDatabaseState(t, admin, created.Intent.ID, "FAILED", "FAILED", 1, false)
		})
	}
}

func TestPostgresCancelPendingAbortActiveSequences(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()

	submitForDevice := func(key, deviceID string, cap commandmodel.Capability) commandmodel.CommandIntent {
		t.Helper()
		req := postgresStartStopCommandRequest(key, cap, *now)
		req.DeviceID = deviceID
		req.Authorization.DeviceID = deviceID
		res, err := store.Submit(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return res.Intent
	}
	ackEdge := func(dispatch commandmodel.DispatchEnvelope, evidenceID, edgeText string) {
		t.Helper()
		applied := edgeText
		edge := &commandmodel.EdgeExecutionEvidence{
			Requested: commandmodel.TextScalar(string(dispatch.Capability)),
			Effective: &commandmodel.ScalarValue{Text: &applied}, Applied: &commandmodel.ScalarValue{Text: &applied},
			WinnerControllerID: "cloud-command-intent", Cycle: 1,
		}
		if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
			Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: evidenceID,
			EdgeExecution: recordAcknowledgedTextConnectorEvidence(t, store, *now, dispatch, edge),
		}); err != nil {
			t.Fatal(err)
		}
	}

	devQueued := "018f3e00-3000-7000-8000-000000000101"
	devPrepared := "018f3e00-3000-7000-8000-000000000102"
	devSeqStart := "018f3e00-3000-7000-8000-000000000103"
	devEmergencyDo := "018f3e00-3000-7000-8000-000000000104"
	devPulseStop := "018f3e00-3000-7000-8000-000000000105"

	seqStart := submitForDevice("pg-abort-seq", devSeqStart, commandmodel.CapabilityStartUnitSequence)
	dispSeqStart, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ackEdge(dispSeqStart, "ev-pg-seq", "chiller-1:sequence-accepted")
	if _, err := store.ClaimVerification(ctx, commandTenantA, "verifier-a", 15*time.Second); err != nil {
		t.Fatal(err)
	}

	emergencyDo := submitForDevice("pg-abort-edo", devEmergencyDo, commandmodel.CapabilityStop)
	dispEmergencyDo, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ackEdge(dispEmergencyDo, "ev-pg-edo", "all-do:0")

	pulseStop := submitForDevice("pg-keep-pulse", devPulseStop, commandmodel.CapabilityStop)
	dispPulseStop, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ackEdge(dispPulseStop, "ev-pg-pulse", "u14:pulse(500ms)->0")

	preparedSeq := submitForDevice("pg-keep-prepared", devPrepared, commandmodel.CapabilityStartUnitSequence)
	if _, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second); err != nil {
		t.Fatal(err)
	}

	queuedCmd := submitForDevice("pg-cancel-queued", devQueued, commandmodel.CapabilityStart)

	*now = now.Add(time.Second)
	ids, err := store.CancelPending(ctx, commandmodel.CancelPendingRequest{
		TenantID:             commandTenantA,
		SiteID:               commandSiteA,
		PrincipalID:          commandPrincipalA,
		Reason:               "SAFE_SHUTDOWN_EXIT",
		CausationID:          "exit-1",
		AbortActiveSequences: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 cancelled/aborted IDs (%s, %s, %s), got %v", queuedCmd.ID, seqStart.ID, emergencyDo.ID, ids)
	}

	assertDispatchDatabaseState(t, admin, queuedCmd.ID, "CANCELLED", "", 0, false)
	assertDispatchDatabaseState(t, admin, seqStart.ID, "FAILED", "FAILED", 1, false)
	assertDispatchDatabaseState(t, admin, emergencyDo.ID, "FAILED", "FAILED", 1, false)
	assertDispatchDatabaseState(t, admin, pulseStop.ID, "DISPATCHING", "ACKNOWLEDGED", 1, false)
	assertDispatchDatabaseState(t, admin, preparedSeq.ID, "DISPATCHING", "PREPARED", 1, false)

	for _, abortedID := range []string{seqStart.ID, emergencyDo.ID} {
		var reason, auditReason string
		var leaseOwner *string
		if err := admin.QueryRow(ctx, `
SELECT a.verification_lease_owner,
       COALESCE((SELECT t.reason FROM command_runtime.command_transitions t WHERE t.command_id = i.command_id ORDER BY t.command_version DESC LIMIT 1), ''),
       COALESCE((SELECT au.redacted_payload->>'reason' FROM command_runtime.command_audit_intents au WHERE au.command_id = i.command_id AND au.event_kind = 'COMMAND_CANCELLED' ORDER BY au.created_at DESC LIMIT 1), '')
FROM command_runtime.command_intents i
JOIN command_runtime.command_attempts a ON a.command_id = i.command_id
WHERE i.command_id = $1::uuid
`, abortedID).Scan(&leaseOwner, &reason, &auditReason); err != nil {
			t.Fatal(err)
		}
		if leaseOwner != nil || reason != "ACTIVE_SEQUENCE_ABORTED" || auditReason != "ACTIVE_SEQUENCE_ABORTED" {
			t.Fatalf("unexpected aborted state for %s: leaseOwner=%v reason=%q auditReason=%q", abortedID, leaseOwner, reason, auditReason)
		}
	}
}

func TestPostgresSameOwnerSafetyEvalRetryRetiresEveryOutboxRowAndExpiresCleanly(t *testing.T) {
	store, admin, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()

	created, err := store.Submit(ctx, postgresCommandRequest("same-owner-retry", 24))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-same", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveDispatch(ctx, first, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "DISPATCH_SAFETY_EVALUATION_FAILED",
	}); err != nil {
		t.Fatal(err)
	}

	second, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-same", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if second.AttemptID == first.AttemptID || second.ExecutionFence <= first.ExecutionFence {
		t.Fatalf("expected new attempt and fence on same-owner retry: first=%#v second=%#v", first, second)
	}
	acknowledgeAndVerifyPostgres(t, store, admin, now, second, "same-owner", 2)
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "SUCCEEDED", "VERIFIED", 2, false)

	var undelivered int
	if err := admin.QueryRow(ctx, `
SELECT count(*) FROM command_runtime.command_dispatch_outbox
WHERE command_id = $1::uuid AND delivered_at IS NULL
`, created.Intent.ID).Scan(&undelivered); err != nil {
		t.Fatal(err)
	}
	if undelivered != 0 {
		t.Fatalf("expected 0 undelivered outbox rows after same-owner retry, got %d", undelivered)
	}

	// Governance-expired QUEUED command with >1 undelivered outbox rows must expire in one update without 23505.
	dupReq := postgresCommandRequest("dup-outbox-expire", 23.5)
	dupReq.CurrentState.ObservedAt = *now
	dupReq.Authorization.IssuedAt = now.Add(-5 * time.Second)
	dupReq.Authorization.ExpiresAt = now.Add(25 * time.Second)
	dupCmd, err := store.Submit(ctx, dupReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
INSERT INTO command_runtime.command_dispatch_outbox (
  outbox_id, command_id, tenant_id, site_id, device_id, command_version,
  available_at, lease_owner, lease_until, delivered_at, attempt_count, payload, created_at
) VALUES (
  '018f3e00-aaaa-7000-8000-000000000001'::uuid, $1::uuid, $2::uuid, $3::uuid, $4::uuid, 99,
  $5, NULL, NULL, NULL, 0, '{}'::jsonb, $5
)
`, dupCmd.Intent.ID, commandTenantA, commandSiteA, commandDeviceA, *now); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(30 * time.Second)
	if _, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-same", 10*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("expected ErrNoDispatchAvailable after governance expiry, got %v", err)
	}
	assertDispatchDatabaseState(t, admin, dupCmd.Intent.ID, "EXPIRED", "", 0, false)
	if err := admin.QueryRow(ctx, `
SELECT count(*) FROM command_runtime.command_dispatch_outbox
WHERE command_id = $1::uuid AND delivered_at IS NULL
`, dupCmd.Intent.ID).Scan(&undelivered); err != nil {
		t.Fatal(err)
	}
	if undelivered != 0 {
		t.Fatalf("expected all undelivered outbox rows retired on governance expiry, got %d", undelivered)
	}
}
