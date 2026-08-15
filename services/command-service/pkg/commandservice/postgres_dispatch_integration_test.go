package commandservice

import (
	"errors"
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
			envelope, claimErr := store.ClaimDispatch(ctx, commandOrgA, worker, 10*time.Second)
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
	first, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveDispatch(ctx, first, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "SYNTHETIC_PRE_SEND",
		EvidenceID: "synthetic-not-sent-1",
	}); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-b", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if second.ExecutionFence <= first.ExecutionFence || second.AttemptID == first.AttemptID {
		t.Fatalf("fence/attempt did not advance first=%#v second=%#v", first, second)
	}
	if err := store.ResolveDispatch(ctx, first, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true,
		EvidenceID: "late-old-fence",
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
	first, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-a", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(3 * time.Second)
	if _, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-b", 10*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("expired prepared attempt was blindly reassigned: %v", err)
	}
	assertDispatchDatabaseState(t, admin, created.Intent.ID, "OUTCOME_UNKNOWN", "OUTCOME_UNKNOWN", 1, true)
	if err := store.ResolveDispatch(ctx, first, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true,
		EvidenceID: "late-after-expiry",
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
	first, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-a", 10*time.Second)
	if err != nil || first.CommandID != firstCommand.Intent.ID {
		t.Fatalf("first lane claim=%#v err=%v", first, err)
	}
	if _, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-b", 10*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("second device command bypassed active lane: %v", err)
	}
	acknowledgeAndVerifyPostgres(t, store, admin, now, first, "lane-first", 1)
	second, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-b", 10*time.Second)
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
	dispatch, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-mismatch",
	}); err != nil {
		t.Fatal(err)
	}
	verification, err := store.ClaimVerification(ctx, commandOrgA, "verifier-a", 15*time.Second)
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
	if _, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-b", 10*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
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
	dispatch, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-timestamp-precision",
	}); err != nil {
		t.Fatal(err)
	}
	verification, err := store.ClaimVerification(ctx, commandOrgA, "verifier-a", 15*time.Second)
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
	dispatch, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-a", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveDispatch(ctx, dispatch, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "provider-ack-expired",
	}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(verificationWindow + time.Second)
	if _, err := store.ClaimVerification(ctx, commandOrgA, "verifier-a", 15*time.Second); !errors.Is(err, ErrVerificationNotAvailable) {
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
	dispatch, err := store.ClaimDispatch(ctx, commandOrgA, "dispatcher-a", 10*time.Second)
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
		EvidenceID: evidencePrefix + "-provider-ack",
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
	value, ok := commandmodel.ParameterValue(envelope.Capability, envelope.Parameters)
	if !ok {
		t.Fatalf("verification envelope has no numeric parameter: %#v", envelope)
	}
	return value
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
  ON d.organization_id = i.organization_id AND d.device_id = i.device_id
WHERE i.command_id = $1::uuid
`, commandID).Scan(&actualIntentStatus, &latestAttemptStatus, &attemptCount, &frozenState); err != nil {
		t.Fatal(err)
	}
	if actualIntentStatus != intentStatus || latestAttemptStatus != attemptStatus || attemptCount != attempts || frozenState != frozen {
		t.Fatalf("dispatch state intent=%s attempt=%s attempts=%d frozen=%v", actualIntentStatus, latestAttemptStatus, attemptCount, frozenState)
	}
}
