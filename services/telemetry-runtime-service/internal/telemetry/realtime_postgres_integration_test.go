package telemetry

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

func TestPostgresRealtimeOwnerRelayCurrentScopeAndRevocation(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetDeviceAState(t, admin)
	if _, err := admin.Exec(ctx, `DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE subscription_id IN (SELECT subscription_id FROM telemetry_runtime.telemetry_subscriptions WHERE device_id = $1::uuid)`, deviceA); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `DELETE FROM telemetry_runtime.recovery_cursors WHERE subscription_id IN (SELECT subscription_id FROM telemetry_runtime.telemetry_subscriptions WHERE device_id = $1::uuid)`, deviceA); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `DELETE FROM telemetry_runtime.telemetry_subscriptions WHERE device_id = $1::uuid`, deviceA); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	if _, err := admin.Exec(ctx, `UPDATE telemetry_runtime.iam_scope_projections SET valid_until = $2, revoked_at = NULL, updated_at = $1 WHERE principal_id = $3::uuid AND device_id = $4::uuid AND action = 'SUBSCRIBE'`, now, now.Add(time.Hour), realtimeTestPrincipal, deviceA); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `UPDATE telemetry_runtime.iam_scope_projections SET valid_until = '2026-07-24T00:00:00Z', revoked_at = NULL, updated_at = '2026-07-23T00:00:00Z' WHERE principal_id = $1::uuid AND device_id = $2::uuid AND action = 'SUBSCRIBE'`, realtimeTestPrincipal, deviceA)
	})

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	transport := &RecordingRealtimeTransport{}
	service, err := NewRealtimeService(RealtimeConfig{
		Repository: store, Transport: transport,
		PublicEndpoint:         "wss://realtime.example.test/connection/websocket",
		CapabilityHMACKey:      []byte(strings.Repeat("c", 32)),
		ConnectionTokenHMACKey: []byte(strings.Repeat("t", 32)),
		Now:                    func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	access := AccessContext{
		PrincipalID: realtimeTestPrincipal, Subject: "subject-a", SubjectIssuer: "https://issuer.example.test",
		SessionID: "session-a", TenantID: orgA, PolicyRevision: "telemetry-access:3",
	}
	bootstrap, err := service.Bootstrap(ctx, access, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "postgres-zone", DeviceId: deviceA, Keys: []telemetryapi.TelemetryKey{"zone.temperature"}},
	}})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if len(bootstrap.Subscriptions) != 1 {
		t.Fatalf("subscriptions=%d", len(bootstrap.Subscriptions))
	}
	if _, err := service.AuthorizeSubscribe(ctx, realtimeTestPrincipal, string(bootstrap.Subscriptions[0].Channel)); err != nil {
		t.Fatalf("current owner scope rejected: %v", err)
	}

	commit, err := store.EvaluateAndRead(ctx, telemetryauth.Target{DeviceID: deviceA, Keys: []string{"zone.temperature"}}, now)
	if err != nil || !commit.StateChanged {
		t.Fatalf("snapshot commit=%+v err=%v", commit, err)
	}
	published, err := service.RelayOnce(ctx, 10)
	if err != nil || published != 1 || len(transport.Publications) != 1 {
		t.Fatalf("relay published=%d transport=%d err=%v", published, len(transport.Publications), err)
	}
	publication := transport.Publications[0].Publication
	if publication.EventId == "" || publication.Revision != commit.Snapshot.BusinessRevision || publication.PreviousRevision != telemetryapi.BusinessRevision(commit.PreviousRevision) {
		t.Fatalf("publication did not reuse authoritative revision: %+v", publication)
	}

	if _, err := admin.Exec(ctx, `UPDATE telemetry_runtime.iam_scope_projections SET revoked_at = $1, updated_at = $1 WHERE principal_id = $2::uuid AND device_id = $3::uuid AND action = 'SUBSCRIBE'`, now.Add(time.Second), realtimeTestPrincipal, deviceA); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthorizeSubscribe(ctx, realtimeTestPrincipal, string(bootstrap.Subscriptions[0].Channel)); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("revoked IAM projection remained subscribable: %v", err)
	}
	revoked, err := service.Revoke(ctx, realtimeTestPrincipal, deviceA)
	if err != nil || revoked != 1 || len(transport.Unsubscribes) != 1 {
		t.Fatalf("revoke count=%d unsubscribes=%d err=%v", revoked, len(transport.Unsubscribes), err)
	}
	if _, err := service.AuthorizeSubscribe(ctx, realtimeTestPrincipal, string(bootstrap.Subscriptions[0].Channel)); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("owner-revoked channel remained subscribable: %v", err)
	}
}
