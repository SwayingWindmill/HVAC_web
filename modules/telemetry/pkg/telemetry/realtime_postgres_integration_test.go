package telemetry

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
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
		SessionID: "session-a", TenantID: tenantA, PolicyRevision: "telemetry-access:3",
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

	if revoked, err := service.Revoke(ctx, realtimeTestPrincipal, deviceA, now.Add(-time.Second)); err != nil || revoked != 0 {
		t.Fatalf("an IAM change before the subscription was authorized revoked it: count=%d err=%v", revoked, err)
	}
	revoked, err := service.Revoke(ctx, realtimeTestPrincipal, deviceA, now)
	if err != nil || revoked != 1 || len(transport.Unsubscribes) != 1 {
		t.Fatalf("revoke count=%d unsubscribes=%d err=%v", revoked, len(transport.Unsubscribes), err)
	}
	if _, err := service.AuthorizeSubscribe(ctx, realtimeTestPrincipal, string(bootstrap.Subscriptions[0].Channel)); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("owner-revoked channel remained subscribable: %v", err)
	}
}

// A page reload or a second tab bootstraps again with the same client subscription id.
func TestPostgresRealtimeBootstrapAgainWithSameClientSubscriptionID(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, statement := range []string{
		`DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE subscription_id IN (SELECT subscription_id FROM telemetry_runtime.telemetry_subscriptions WHERE device_id = $1::uuid)`,
		`DELETE FROM telemetry_runtime.recovery_cursors WHERE subscription_id IN (SELECT subscription_id FROM telemetry_runtime.telemetry_subscriptions WHERE device_id = $1::uuid)`,
		`DELETE FROM telemetry_runtime.telemetry_subscriptions WHERE device_id = $1::uuid`,
	} {
		if _, err := admin.Exec(ctx, statement, deviceA); err != nil {
			t.Fatal(err)
		}
	}

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	service, err := NewRealtimeService(RealtimeConfig{
		Repository: store, Transport: &RecordingRealtimeTransport{},
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
		SessionID: "session-a", TenantID: tenantA, PolicyRevision: "telemetry-access:3",
	}
	request := telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "page-device-a", DeviceId: deviceA, Keys: []telemetryapi.TelemetryKey{"zone.temperature"}},
	}}
	first, err := service.Bootstrap(ctx, access, request)
	if err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	second, err := service.Bootstrap(ctx, access, request)
	if err != nil {
		t.Fatalf("second bootstrap with the same client subscription id: %v", err)
	}
	if len(first.Subscriptions) != 1 || len(second.Subscriptions) != 1 || first.Subscriptions[0].Channel == second.Subscriptions[0].Channel {
		t.Fatalf("bootstraps must create distinct subscriptions: first=%+v second=%+v", first.Subscriptions, second.Subscriptions)
	}
}
