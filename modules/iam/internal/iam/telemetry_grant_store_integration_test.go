package iam_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

// The S1 runner seeds the delegated user as registry-reader on Site 1 with this binding.
const postgresDelegatedSiteBindingID = "018f1e00-2200-7000-8000-000000000012"

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
	// Suspending the delegated user's Site assignment revokes its live telemetry.
	const setStatus = `UPDATE iam.role_bindings SET status=$2, revision=revision+1, updated_at=clock_timestamp() WHERE id=$1::uuid`
	if _, err := admin.Exec(ctx, setStatus, postgresDelegatedSiteBindingID, "SUSPENDED"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), setStatus, postgresDelegatedSiteBindingID, "ACTIVE")
	facts, err := store.PollRevocations(ctx, postgresTenantAID, before, 10)
	if err != nil || len(facts) != 1 || facts[0].SourceType != "ROLE_BINDING" || facts[0].PrincipalID != postgresDelegatedPrincipalID {
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
	digest, err := telemetryauth.ScopeDigest(telemetryauth.ActionSnapshotRead, postgresTenantAID, []telemetryauth.Target{{DeviceID: "018f1e00-4000-7000-8000-000000000001", Keys: []string{"zone.temperature"}}})
	if err != nil {
		t.Fatal(err)
	}
	identifier := "use-" + strconv.FormatInt(now.UnixNano(), 10)
	claims := telemetryauth.GrantClaims{TokenID: identifier, TenantID: postgresTenantAID, PrincipalID: postgresDelegatedPrincipalID, ScopeDigest: digest, PolicyRevision: "telemetry-access:2/iam:1", ExpiresAt: now.Add(30 * time.Second).Unix()}
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
