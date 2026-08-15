package iam_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

func TestPostgresTelemetryRevocationPoll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := iam.OpenPostgresTelemetryGrantStore(ctx, requiredIAMPostgresEnv(t, "S2_IAM_GRANT_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	admin, err := pgxpool.New(ctx, requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var before int64
	if err := admin.QueryRow(ctx, `SELECT COALESCE(max(sequence), 0) FROM iam.telemetry_revocation_facts`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE iam.telemetry_key_bindings SET telemetry_key='fan.velocity', status='SUSPENDED', revision=revision+1, updated_at=clock_timestamp() WHERE id='018f1e00-2700-7000-8000-000000000003'::uuid`); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `UPDATE iam.telemetry_key_bindings SET telemetry_key='fan.speed', status='ACTIVE', revision=revision+1, updated_at=clock_timestamp() WHERE id='018f1e00-2700-7000-8000-000000000003'::uuid`)
	facts, err := store.PollRevocations(ctx, postgresActingOrganizationID, before, 10)
	if err != nil || len(facts) != 1 || facts[0].SourceType != "KEY_PERMISSION" || facts[0].TelemetryKey != "fan.speed" {
		t.Fatalf("facts=%#v err=%v", facts, err)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, facts[0].OccurredAt)
	if err != nil || time.Since(occurredAt) > 10*time.Second {
		t.Fatalf("occurredAt=%q err=%v", facts[0].OccurredAt, err)
	}
}

func TestPostgresTelemetryGrantSingleUse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := iam.OpenPostgresTelemetryGrantStore(ctx, requiredIAMPostgresEnv(t, "S2_IAM_GRANT_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	admin, err := pgxpool.New(ctx, requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	now := time.Now().UTC()
	digest, err := telemetryauth.ScopeDigest(telemetryauth.ActionSnapshotRead, postgresActingOrganizationID, []telemetryauth.Target{{DeviceID: "018f1e00-4000-7000-8000-000000000001", Keys: []string{"zone.temperature"}}})
	if err != nil {
		t.Fatal(err)
	}
	identifier := "use-" + strconv.FormatInt(now.UnixNano(), 10)
	claims := telemetryauth.GrantClaims{TokenID: identifier, TenantID: "018f1d00-0000-7000-8000-000000000001", PrincipalID: postgresDelegatedPrincipalID, ActingOrganizationID: postgresActingOrganizationID, ScopeDigest: digest, PolicyRevision: "telemetry-access:2", ExpiresAt: now.Add(30 * time.Second).Unix()}
	defer admin.Exec(context.Background(), `DELETE FROM iam.telemetry_grant_uses WHERE token_id = $1`, identifier)
	first, err := store.ConsumeGrant(ctx, claims, now)
	if err != nil || first.Replayed || first.Revoked {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := store.ConsumeGrant(ctx, claims, now.Add(time.Millisecond))
	if err != nil || !second.Replayed {
		t.Fatalf("second=%#v err=%v", second, err)
	}
}
