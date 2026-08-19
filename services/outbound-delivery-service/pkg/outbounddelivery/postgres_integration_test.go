package outbounddelivery

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresDeliveryLedgerPreservesUnknownOutcomeHistoryAcrossGovernedReplay(t *testing.T) {
	dsn := os.Getenv("OUTBOUND_DELIVERY_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OUTBOUND_DELIVERY_POSTGRES_DSN is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}

	tenantID := "0190f000-0000-7000-8000-000000000001"
	now := time.Date(2026, 8, 19, 5, 0, 0, 0, time.UTC)
	definition, err := store.PutIntegration(ctx, PutIntegrationRequest{Definition: IntegrationDefinition{
		TenantID:         tenantID,
		Name:             "s15-ledger-test",
		AdapterType:      AdapterRESTWebhook,
		DestinationURL:   "https://delivery.example/hook",
		AllowedHosts:     []string{"delivery.example"},
		CredentialRef:    "credential-ref-test",
		Enabled:          true,
		MaxRequestBytes:  64 * 1024,
		MaxResponseBytes: 16 * 1024,
		Timeout:          5 * time.Second,
		MaxConcurrency:   1,
		MaxAttempts:      3,
		RetryDelay:       time.Minute,
		CreatedAt:        now,
	}})
	if err != nil {
		t.Fatal(err)
	}

	request := SubmitIntentRequest{
		TenantID:            tenantID,
		IntegrationID:       definition.ID,
		Purpose:             "alarm.external-delivery",
		PayloadSchema:       "alarm-delivery/v1",
		Payload:             []byte(`{"alarmId":"alarm-1"}`),
		IdempotencyKey:      "alarm-1:created:webhook",
		SourceAggregateType: "Alarm",
		SourceAggregateID:   "alarm-1",
		Classification:      "OPERATIONS",
		CreatedAt:           now.Add(time.Second),
	}
	intent, err := store.SubmitIntent(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	replayedIntent, err := store.SubmitIntent(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if replayedIntent.ID != intent.ID {
		t.Fatalf("idempotent submit returned intent %s, want %s", replayedIntent.ID, intent.ID)
	}
	conflicting := request
	conflicting.Payload = []byte(`{"alarmId":"alarm-2"}`)
	if _, err = store.SubmitIntent(ctx, conflicting); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed payload with same idempotency key error = %v", err)
	}

	claim, err := store.ClaimNext(ctx, Scope{TenantID: tenantID}, "worker-1", now.Add(2*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Attempt.AttemptNo != 1 || claim.Attempt.Outcome != OutcomeMaybeSent {
		t.Fatalf("first claim attempt = %#v", claim.Attempt)
	}
	if err = store.CompleteAttempt(ctx, Scope{TenantID: tenantID}, claim.Attempt.ID, "worker-1", AdapterResult{
		Outcome:   OutcomeMaybeSent,
		ErrorCode: "TRANSPORT_OUTCOME_UNKNOWN",
	}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimNext(ctx, Scope{TenantID: tenantID}, "worker-2", now.Add(10*time.Minute), time.Minute); !errors.Is(err, ErrNothingReady) {
		t.Fatalf("unknown outcome must not be automatically retryable, claim error = %v", err)
	}

	deadLetterID := tenantScalar(t, ctx, pool, tenantID, `SELECT id::text FROM outbound_delivery.dead_letters WHERE intent_id=$1::uuid`, intent.ID)
	approverID := "0190f000-0000-7000-8000-000000000099"
	if _, err = store.ApproveReplay(ctx, ReplayRequest{
		TenantID:            tenantID,
		DeadLetterID:        deadLetterID,
		ApprovedByPrincipal: approverID,
		Reason:              "provider could not confirm prior outcome",
		ApprovedAt:          now.Add(11 * time.Minute),
	}); !errors.Is(err, ErrReplayRiskRequired) {
		t.Fatalf("replay without duplicate-risk acknowledgement error = %v", err)
	}
	if _, err = store.ApproveReplay(ctx, ReplayRequest{
		TenantID:            tenantID,
		DeadLetterID:        deadLetterID,
		ApprovedByPrincipal: approverID,
		Reason:              "provider could not confirm prior outcome",
		AcceptDuplicateRisk: true,
		ApprovedAt:          now.Add(12 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	secondClaim, err := store.ClaimNext(ctx, Scope{TenantID: tenantID}, "worker-2", now.Add(13*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if secondClaim.Attempt.AttemptNo != 2 || secondClaim.Attempt.ID == claim.Attempt.ID {
		t.Fatalf("governed replay must create a new attempt, got %#v", secondClaim.Attempt)
	}
	if err = store.CompleteAttempt(ctx, Scope{TenantID: tenantID}, secondClaim.Attempt.ID, "worker-2", AdapterResult{
		Outcome:           OutcomeDelivered,
		HTTPStatus:        200,
		ProviderRequestID: "provider-request-2",
		ResponseDigest:    PayloadDigest([]byte(`{"ok":true}`)),
	}, now.Add(13*time.Minute+30*time.Second)); err != nil {
		t.Fatal(err)
	}

	if got := tenantCount(t, ctx, pool, tenantID, `SELECT count(*) FROM outbound_delivery.delivery_attempts WHERE intent_id=$1::uuid`, intent.ID); got != 2 {
		t.Fatalf("attempt count = %d, want 2", got)
	}
	if got := tenantCount(t, ctx, pool, tenantID, `SELECT count(*) FROM outbound_delivery.delivery_receipts WHERE intent_id=$1::uuid`, intent.ID); got != 1 {
		t.Fatalf("receipt count = %d, want 1", got)
	}
	if got := tenantCount(t, ctx, pool, tenantID, `SELECT count(*) FROM outbound_delivery.dead_letters WHERE intent_id=$1::uuid`, intent.ID); got != 1 {
		t.Fatalf("dead-letter count = %d, want 1", got)
	}
	if got := tenantCount(t, ctx, pool, tenantID, `SELECT count(*) FROM outbound_delivery.replay_approvals WHERE intent_id=$1::uuid`, intent.ID); got != 1 {
		t.Fatalf("replay approval count = %d, want 1", got)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenantID); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE outbound_delivery.delivery_attempts SET error_code='tampered' WHERE id=$1::uuid`, secondClaim.Attempt.ID); err == nil {
		tx.Rollback(ctx)
		t.Fatal("completed delivery attempt mutation unexpectedly succeeded")
	}
	if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}

	expiringRequest := request
	expiringRequest.IdempotencyKey = "alarm-1:lease-expiry:webhook"
	expiringRequest.Purpose = "alarm.lease-expiry-test"
	expiringRequest.CreatedAt = now.Add(20 * time.Minute)
	expiringIntent, err := store.SubmitIntent(ctx, expiringRequest)
	if err != nil {
		t.Fatal(err)
	}
	expiringClaim, err := store.ClaimNext(ctx, Scope{TenantID: tenantID}, "worker-crashed", now.Add(21*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if expiringClaim.Intent.ID != expiringIntent.ID {
		t.Fatalf("claimed intent %s, want expiring intent %s", expiringClaim.Intent.ID, expiringIntent.ID)
	}
	recovered, err := store.RecoverExpired(ctx, Scope{TenantID: tenantID}, now.Add(23*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered expired attempts = %d, want 1", recovered)
	}
	if _, err = store.ClaimNext(ctx, Scope{TenantID: tenantID}, "worker-after-restart", now.Add(24*time.Minute), time.Minute); !errors.Is(err, ErrNothingReady) {
		t.Fatalf("expired unknown attempt must not be automatically retried, claim error = %v", err)
	}
	if state := tenantScalar(t, ctx, pool, tenantID, `SELECT state FROM outbound_delivery.delivery_intents WHERE id=$1::uuid`, expiringIntent.ID); state != string(IntentOutcomeUnknown) {
		t.Fatalf("expired intent state = %s, want %s", state, IntentOutcomeUnknown)
	}
	if got := tenantCount(t, ctx, pool, tenantID, `SELECT count(*) FROM outbound_delivery.dead_letters WHERE intent_id=$1::uuid AND requires_duplicate_risk_ack=true`, expiringIntent.ID); got != 1 {
		t.Fatalf("expired intent duplicate-risk dead letters = %d, want 1", got)
	}
}

func tenantScalar(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, query string, args ...any) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenantID); err != nil {
		t.Fatal(err)
	}
	var value string
	if err = tx.QueryRow(ctx, query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return value
}

func tenantCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, query string, args ...any) int {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenantID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return count
}
