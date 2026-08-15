package sessionstore_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/sessionevent"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

func TestPostgresSessionMutationCommitsStateAuditAndOutboxAtomically(t *testing.T) {
	harness := newPostgresHarness(t)
	harness.reset(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ids := sequenceIDs("audit-create-01", "audit-revoke-01")
	store, err := sessionstore.OpenPostgres(context.Background(), harness.gatewayDSN, sessionstore.PostgresConfig{IDGenerator: ids})
	if err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateSession(context.Background(), fixtureSession(now), fixtureMutation(now, "SESSION_CREATED"))
	if err != nil {
		t.Fatal(err)
	}
	if created.AggregateVersion != 1 || created.LastAuditMessageID != "audit-create-01" {
		t.Fatalf("unexpected created session: %#v", created)
	}
	harness.assertCounts(t, 1, 1, 1)

	store.Close()
	store, err = sessionstore.OpenPostgres(context.Background(), harness.gatewayDSN, sessionstore.PostgresConfig{IDGenerator: ids})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reloaded, err := store.GetSession(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.LastAuditMessageID != created.LastAuditMessageID {
		t.Fatalf("session did not survive restart: %#v", reloaded)
	}

	revoked, err := store.RevokeSession(context.Background(), created.ID, fixtureMutation(now.Add(time.Second), "SESSION_REVOKED"))
	if err != nil {
		t.Fatal(err)
	}
	if revoked.AggregateVersion != 2 || revoked.LastAuditMessageID != "audit-revoke-01" || revoked.RevokedAt == nil {
		t.Fatalf("unexpected revoked session: %#v", revoked)
	}
	harness.assertCounts(t, 1, 2, 2)

	rows, err := harness.admin.Query(context.Background(), `SELECT payload, partition_key, aggregate_id FROM gateway.outbox ORDER BY aggregate_version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	expectedAggregateID := sessionevent.AuditAggregateID(created.ID)
	var intentAggregateID string
	if err := harness.admin.QueryRow(context.Background(), `SELECT session_aggregate_id FROM gateway.audit_intents WHERE message_id = $1`, created.LastAuditMessageID).Scan(&intentAggregateID); err != nil {
		t.Fatal(err)
	}
	if intentAggregateID != expectedAggregateID {
		t.Fatalf("Audit Intent exposed a non-redacted Session aggregate: %q", intentAggregateID)
	}
	for rows.Next() {
		var payload []byte
		var partitionKey, aggregateID string
		if err := rows.Scan(&payload, &partitionKey, &aggregateID); err != nil {
			t.Fatal(err)
		}
		if aggregateID != expectedAggregateID || partitionKey != sessionevent.AggregateType+":"+expectedAggregateID {
			t.Fatalf("Outbox used a non-redacted Session aggregate: aggregate=%q partition=%q", aggregateID, partitionKey)
		}
		for _, forbidden := range [][]byte{[]byte(created.ID), []byte("access_token"), []byte("refresh_token"), []byte("id_token"), []byte("Bearer "), []byte("X-Delegation-Grant"), []byte("seeded-provider-secret")} {
			if bytes.Contains(payload, forbidden) {
				t.Fatalf("Outbox protobuf leaked %q", forbidden)
			}
		}
	}
}

func TestPostgresSessionMutationRollsBackAtEveryPreCommitFailurePoint(t *testing.T) {
	harness := newPostgresHarness(t)
	for _, point := range []sessionstore.FailurePoint{sessionstore.FailureAfterStateWrite, sessionstore.FailureAfterAuditIntent, sessionstore.FailureBeforeCommit} {
		t.Run(string(point), func(t *testing.T) {
			harness.reset(t)
			store, err := sessionstore.OpenPostgres(context.Background(), harness.gatewayDSN, sessionstore.PostgresConfig{
				IDGenerator: func() string { return "audit-failure-" + string(point) },
				FailureInjector: func(candidate sessionstore.FailurePoint) error {
					if candidate == point {
						return errors.New("seeded precommit failure")
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			_, err = store.CreateSession(context.Background(), fixtureSession(time.Now().UTC()), fixtureMutation(time.Now().UTC(), "SESSION_CREATED"))
			if err == nil {
				t.Fatal("expected injected transaction failure")
			}
			harness.assertCounts(t, 0, 0, 0)
		})
	}
}

func TestAuditIntentInsertFailureFailsClosed(t *testing.T) {
	harness := newPostgresHarness(t)
	harness.reset(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	first, err := sessionstore.OpenPostgres(context.Background(), harness.gatewayDSN, sessionstore.PostgresConfig{IDGenerator: func() string { return "duplicate-audit-message" }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.CreateSession(context.Background(), fixtureSession(now), fixtureMutation(now, "SESSION_CREATED")); err != nil {
		t.Fatal(err)
	}
	first.Close()

	secondSession := fixtureSession(now.Add(time.Second))
	secondSession.ID = "session-02"
	second, err := sessionstore.OpenPostgres(context.Background(), harness.gatewayDSN, sessionstore.PostgresConfig{IDGenerator: func() string { return "duplicate-audit-message" }})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.CreateSession(context.Background(), secondSession, fixtureMutation(now.Add(time.Second), "SESSION_CREATED")); err == nil {
		t.Fatal("expected Audit Intent primary key failure")
	}
	harness.assertCounts(t, 1, 1, 1)
	if _, err := second.GetSession(context.Background(), secondSession.ID); !errors.Is(err, sessionstore.ErrSessionNotFound) {
		t.Fatalf("session became externally valid without Audit Intent: %v", err)
	}
}

func TestRuntimeRolesCannotWriteOtherServiceSchemas(t *testing.T) {
	harness := newPostgresHarness(t)
	if _, err := harness.gateway.Exec(context.Background(), `INSERT INTO audit_ledger.tenant_heads (tenant_id,last_record_hash,updated_at) VALUES ('tenant-forged',$1,clock_timestamp())`, stringOf('0', 64)); err == nil {
		t.Fatal("gateway_runtime wrote audit_ledger schema")
	}
	relay, err := pgxpool.New(context.Background(), requiredEnv(t, "S0_RELAY_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	if _, err := relay.Exec(context.Background(), `UPDATE gateway.sessions SET display_name='forged' WHERE session_id='session-01'`); err == nil {
		t.Fatal("gateway_relay_runtime wrote Session state")
	}
}

type postgresHarness struct {
	gatewayDSN string
	admin      *pgxpool.Pool
	gateway    *pgxpool.Pool
}

func newPostgresHarness(t *testing.T) postgresHarness {
	t.Helper()
	gatewayDSN := requiredEnv(t, "S0_GATEWAY_DATABASE_URL")
	admin, err := pgxpool.New(context.Background(), requiredEnv(t, "S0_ADMIN_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := pgxpool.New(context.Background(), gatewayDSN)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	t.Cleanup(gateway.Close)
	return postgresHarness{gatewayDSN: gatewayDSN, admin: admin, gateway: gateway}
}

func (h postgresHarness) reset(t *testing.T) {
	t.Helper()
	_, err := h.admin.Exec(context.Background(), `TRUNCATE gateway.outbox, gateway.audit_intents, gateway.sessions RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func (h postgresHarness) assertCounts(t *testing.T, sessions, intents, outbox int) {
	t.Helper()
	var actualSessions, actualIntents, actualOutbox int
	if err := h.admin.QueryRow(context.Background(), `SELECT count(*) FROM gateway.sessions`).Scan(&actualSessions); err != nil {
		t.Fatal(err)
	}
	if err := h.admin.QueryRow(context.Background(), `SELECT count(*) FROM gateway.audit_intents`).Scan(&actualIntents); err != nil {
		t.Fatal(err)
	}
	if err := h.admin.QueryRow(context.Background(), `SELECT count(*) FROM gateway.outbox`).Scan(&actualOutbox); err != nil {
		t.Fatal(err)
	}
	if actualSessions != sessions || actualIntents != intents || actualOutbox != outbox {
		t.Fatalf("counts session=%d/%d intent=%d/%d outbox=%d/%d", actualSessions, sessions, actualIntents, intents, actualOutbox, outbox)
	}
}

func fixtureSession(now time.Time) sessionstore.Session {
	return sessionstore.Session{
		ID:                       "session-01",
		Principal:                identitycontext.UserPrincipal{Subject: "fixture-user", Issuer: "https://issuer.example.test", DisplayName: "Fixture User", Email: "fixture@example.test", Roles: []string{"operator", "audit-reader"}},
		TenantID:                 "tenant-01",
		CSRFTokenCiphertext:      []byte("encrypted-csrf"),
		ProviderTokensCiphertext: []byte("seeded-provider-secret-that-must-not-enter-audit"),
		ExpiresAt:                now.Add(time.Hour),
	}
}

func fixtureMutation(now time.Time, action string) sessionstore.MutationContext {
	return sessionstore.MutationContext{
		Action: action, Result: "SUCCEEDED", PolicyRevision: "policy-v1",
		CorrelationID: "request-01", TraceID: "0123456789abcdef0123456789abcdef",
		Traceparent:      "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	}
}

func sequenceIDs(values ...string) sessionstore.IDGenerator {
	index := 0
	return func() string {
		if index >= len(values) {
			panic("ID sequence exhausted")
		}
		value := values[index]
		index++
		return value
	}
}

func requiredEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Skipf("%s is not configured", name)
	}
	return value
}

func stringOf(character rune, count int) string {
	return strings.Repeat(string(character), count)
}
