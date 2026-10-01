package commandservice

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const (
	commandTenantA    = "018f3d00-0000-7000-8000-000000000001"
	commandTenantB    = "018f3d00-0000-7000-8000-000000000002"
	commandSiteA      = "018f3e00-1000-7000-8000-000000000001"
	commandDeviceA    = "018f3e00-3000-7000-8000-000000000001"
	commandPointA     = "018f3e00-4000-7000-8000-000000000001"
	commandPrincipalA = "018f3e00-5000-7000-8000-000000000001"
)

func TestPostgresSubmissionIsAtomicIdempotentAndTenantScoped(t *testing.T) {
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetCommandFixture(t, admin)

	opened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	store := NewPostgresStore(opened.pool, fixedPostgresClock(), nil)

	request := postgresCommandRequest("postgres-submit-1", 24)
	created, err := store.Submit(ctx, request)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if created.Replayed || created.Intent.Status != commandmodel.IntentQueued || created.Intent.Version != 3 || created.Intent.PointID != commandPointA {
		t.Fatalf("created intent=%#v", created)
	}
	if created.Intent.DeviceCommandSequence != 1 || len(created.Intent.Transitions) != 3 {
		t.Fatalf("sequence/transitions=%d/%d", created.Intent.DeviceCommandSequence, len(created.Intent.Transitions))
	}

	evidence, err := store.SubmissionEvidence(ctx, commandTenantA, created.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if evidence != (SubmissionEvidence{IntentCount: 1, IdempotencyCount: 1, AuthorizationCount: 1, RiskCount: 1, ApprovalCount: 0, TransitionCount: 3, AuditIntentCount: 1, OutboxCount: 1}) {
		t.Fatalf("atomic evidence=%#v", evidence)
	}

	replayed, err := store.Submit(ctx, request)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if !replayed.Replayed || replayed.Intent.ID != created.Intent.ID || replayed.Intent.PointID != commandPointA {
		t.Fatalf("idempotent replay=%#v", replayed)
	}

	pointDrift := request
	pointDrift.PointID = "018f3e00-4000-7000-8000-000000000002"
	if _, err := store.Submit(ctx, pointDrift); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected point identity idempotency conflict, got %v", err)
	}

	request.Parameters[commandmodel.ParameterSetpointC] = 25
	if _, err := store.Submit(ctx, request); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}

	second, err := store.Submit(ctx, postgresCommandRequest("postgres-submit-2", 23.5))
	if err != nil {
		t.Fatalf("second submit failed: %v", err)
	}
	if second.Intent.DeviceCommandSequence != 2 {
		t.Fatalf("expected monotonic device sequence 2, got %d", second.Intent.DeviceCommandSequence)
	}

	if _, err := store.Get(ctx, commandTenantB, created.Intent.ID); !errors.Is(err, ErrCommandNotFound) {
		t.Fatalf("cross-Organization read must be invisible, got %v", err)
	}
	readBack, err := store.Get(ctx, commandTenantA, created.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readBack.PayloadHash != created.Intent.PayloadHash || readBack.PointID != commandPointA || len(readBack.Transitions) != 3 {
		t.Fatalf("read back drifted: %#v", readBack)
	}
}

func TestPostgresConcurrentIdempotencyConvergesToOneIntent(t *testing.T) {
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetCommandFixture(t, admin)

	opened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	store := NewPostgresStore(opened.pool, fixedPostgresClock(), nil)

	request := postgresCommandRequest("postgres-concurrent", 24)
	results := make(chan SubmitResult, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			result, submitErr := store.Submit(ctx, request)
			if submitErr != nil {
				errorsFound <- submitErr
				return
			}
			results <- result
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for submitErr := range errorsFound {
		t.Fatalf("concurrent submit failed: %v", submitErr)
	}
	var commandID string
	count := 0
	for result := range results {
		count++
		if commandID == "" {
			commandID = result.Intent.ID
		} else if result.Intent.ID != commandID {
			t.Fatalf("concurrent idempotency produced multiple commands: %s and %s", commandID, result.Intent.ID)
		}
	}
	if count != 2 {
		t.Fatalf("expected two successful responses, got %d", count)
	}
	var intentCount, idempotencyCount int
	if err := admin.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM command_runtime.command_intents WHERE idempotency_key = 'postgres-concurrent'),
  (SELECT count(*) FROM command_runtime.command_idempotency WHERE idempotency_key = 'postgres-concurrent')
`).Scan(&intentCount, &idempotencyCount); err != nil {
		t.Fatal(err)
	}
	if intentCount != 1 || idempotencyCount != 1 {
		t.Fatalf("concurrent idempotency counts intent=%d key=%d", intentCount, idempotencyCount)
	}
}

func TestPostgresSubmissionRollsBackEveryOwnedWrite(t *testing.T) {
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetCommandFixture(t, admin)

	opened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	store := NewPostgresStore(opened.pool, fixedPostgresClock(), nil)

	ids := []string{
		"018f3e00-7000-7000-8000-000000000001",
		"018f3e00-7100-7000-8000-000000000001",
		"018f3e00-7100-7000-8000-000000000001",
		"018f3e00-7100-7000-8000-000000000003",
		"018f3e00-7200-7000-8000-000000000001",
		"018f3e00-7300-7000-8000-000000000001",
	}
	index := 0
	failing := NewPostgresStore(store.pool, fixedPostgresClock(), func(time.Time) (string, error) {
		value := ids[index]
		index++
		return value, nil
	})
	request := postgresCommandRequest("postgres-rollback", 24)
	if _, err := failing.Submit(ctx, request); err == nil {
		t.Fatal("duplicate transition identifier unexpectedly committed")
	}

	var intentCount, idempotencyCount, transitionCount, auditCount, outboxCount, deviceStateCount int
	if err := admin.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM command_runtime.command_intents WHERE idempotency_key = 'postgres-rollback'),
  (SELECT count(*) FROM command_runtime.command_idempotency WHERE idempotency_key = 'postgres-rollback'),
  (SELECT count(*) FROM command_runtime.command_transitions WHERE command_id = '018f3e00-7000-7000-8000-000000000001'::uuid),
  (SELECT count(*) FROM command_runtime.command_audit_intents WHERE command_id = '018f3e00-7000-7000-8000-000000000001'::uuid),
  (SELECT count(*) FROM command_runtime.command_dispatch_outbox WHERE command_id = '018f3e00-7000-7000-8000-000000000001'::uuid),
  (SELECT count(*) FROM command_runtime.device_control_state WHERE tenant_id = $1::uuid AND device_id = $2::uuid)
`, commandTenantA, commandDeviceA).Scan(&intentCount, &idempotencyCount, &transitionCount, &auditCount, &outboxCount, &deviceStateCount); err != nil {
		t.Fatal(err)
	}
	if intentCount != 0 || idempotencyCount != 0 || transitionCount != 0 || auditCount != 0 || outboxCount != 0 || deviceStateCount != 0 {
		t.Fatalf("partial transaction survived: intent=%d idempotency=%d transition=%d audit=%d outbox=%d device=%d",
			intentCount, idempotencyCount, transitionCount, auditCount, outboxCount, deviceStateCount)
	}
}

func TestPostgresRuntimeIdentityRequiresActivation(t *testing.T) {
	runtimeURL, _ := commandPostgresTestURLs(t)
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var count int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM command_runtime.command_intents`).Scan(&count)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("runtime login read without role activation err=%v count=%d", err, count)
	}

	wrongURL := strings.Replace(runtimeURL, "s3_command_service", "postgres", 1)
	if _, err := OpenPostgresStore(ctx, wrongURL); err == nil || !strings.Contains(err.Error(), "s3_command_service") {
		t.Fatalf("wrong database identity err=%v", err)
	}
}

func postgresCommandRequest(idempotencyKey string, setpoint float64) commandmodel.SubmitRequest {
	currentValue := 23.0
	return commandmodel.SubmitRequest{
		TenantID:             commandTenantA,
		SiteID:               commandSiteA,
		DeviceID:             commandDeviceA,
		PointID:              commandPointA,
		PrincipalID:          commandPrincipalA,
		IdempotencyKey:       idempotencyKey,
		Capability:           commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: setpoint},
		VerificationPointKey: "zone.temperature_setpoint",
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE",
			Presence:               "ONLINE",
			Readiness:              "CURRENT",
			Quality:                "GOOD",
			BusinessRevision:       21,
			CurrentValue:           &currentValue,
			ObservedAt:             time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC),
		},
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID: "018f3e00-9000-7000-8000-000000000001", PolicyRevision: "command-policy-1",
			Purpose: commandmodel.AuthorizationCommandSubmit, PrincipalID: commandPrincipalA, TenantID: commandTenantA, SiteID: commandSiteA, DeviceID: commandDeviceA,
			Capability:  commandmodel.CapabilitySetTemperatureSetpoint,
			MaximumRisk: commandmodel.RiskHigh, CapabilityRevision: setpointCapabilityRevision,
			EmergencyRevocationRevision: 1,
			IssuedAt:                    time.Date(2026, 7, 26, 10, 59, 55, 0, time.UTC),
			ExpiresAt:                   time.Date(2026, 7, 26, 11, 0, 25, 0, time.UTC),
		},
	}
}

func resetCommandFixture(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	for _, statement := range []string{
		`DELETE FROM command_runtime.connector_evidence`,
		`DELETE FROM command_runtime.command_grant_uses`,
		`DELETE FROM command_runtime.command_audit_intents`,
		`DELETE FROM command_runtime.command_dispatch_outbox`,
		`DELETE FROM command_runtime.command_transitions`,
		`DELETE FROM command_runtime.command_attempts`,
		`DELETE FROM command_runtime.command_approval_snapshots`,
		`DELETE FROM command_runtime.command_risk_snapshots`,
		`DELETE FROM command_runtime.command_authorization_snapshots`,
		`DELETE FROM command_runtime.command_idempotency`,
		`DELETE FROM command_runtime.command_intents`,
		`DELETE FROM command_runtime.device_control_state`,
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}

func commandPostgresTestURLs(t *testing.T) (string, string) {
	t.Helper()
	runtimeURL := os.Getenv("S3_COMMAND_TEST_DATABASE_URL")
	adminURL := os.Getenv("S3_COMMAND_ADMIN_DATABASE_URL")
	if runtimeURL == "" || adminURL == "" {
		t.Skip("S3 Command PostgreSQL integration environment is not configured")
	}
	return runtimeURL, adminURL
}

func fixedPostgresClock() func() time.Time {
	return func() time.Time {
		return time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)
	}
}
