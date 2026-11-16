package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestOutboxRelayHelpers(t *testing.T) {
	if _, err := NewOutboxRelay(nil, OutboxRelayConfig{}); err == nil {
		t.Fatal("expected error when PostgresStore is nil")
	}
	siteID := "018f2e00-1000-7000-8000-000000000001"
	deviceID := "018f2e00-3000-7000-8000-000000000001"
	gatewayID := "018f2e00-3000-7000-8000-000000000002"
	validMeta := MutationMeta{IdempotencyKey: "rebind-validation-key-1", Reason: "validate rebind request"}
	effectiveAt := "2026-11-16T10:00:00Z"

	if err := (RebindRequest{
		SiteID: siteID, Kind: BindingGatewaySourceKey, SourceID: deviceID, TargetID: gatewayID,
		Role: "chiller-1", EffectiveAt: effectiveAt, Meta: validMeta,
	}).Validate(); err != nil {
		t.Fatalf("valid GATEWAY_SOURCE_KEY rebind rejected: %v", err)
	}
	if err := (RebindRequest{
		SiteID: siteID, Kind: BindingGatewaySourceKey, SourceID: deviceID, TargetID: "",
		Role: "chiller-1", EffectiveAt: effectiveAt, CloseOnly: true, Meta: validMeta,
	}).Validate(); err != nil {
		t.Fatalf("valid CloseOnly empty TargetID rejected: %v", err)
	}
	if err := (RebindRequest{
		SiteID: siteID, Kind: BindingGatewaySourceKey, SourceID: deviceID, TargetID: "",
		Role: "chiller-1", EffectiveAt: effectiveAt, CloseOnly: false, Meta: validMeta,
	}).Validate(); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("non-CloseOnly empty TargetID err=%v, want ErrInvalidMutation", err)
	}
	if err := (RebindRequest{
		SiteID: siteID, Kind: BindingGatewaySourceKey, SourceID: deviceID, TargetID: gatewayID,
		Role: "bad source key!", EffectiveAt: effectiveAt, Meta: validMeta,
	}).Validate(); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("invalid sourceKey err=%v, want ErrInvalidMutation", err)
	}
	if got := millisToPositiveSeconds(60000, 60); got != 60 {
		t.Fatalf("millisToPositiveSeconds(60000)=%d, want 60", got)
	}
	if got := millisToPositiveSeconds(500, 60); got != 1 {
		t.Fatalf("millisToPositiveSeconds(500)=%d, want 1", got)
	}
	if got := millisToPositiveSeconds(0, 45); got != 45 {
		t.Fatalf("millisToPositiveSeconds(0)=%d, want 45", got)
	}
	if got := relayRetryBackoff(1); got != time.Second {
		t.Fatalf("attempt 1 backoff=%v, want 1s", got)
	}
	if got := relayRetryBackoff(3); got != 4*time.Second {
		t.Fatalf("attempt 3 backoff=%v, want 4s", got)
	}
	if got := relayRetryBackoff(20); got != time.Minute {
		t.Fatalf("attempt 20 backoff=%v, want capped 1m", got)
	}
}

func TestPostgresOutboxRelayDispatchesSyncRetireCancelAndRetriesFailures(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)

	var (
		mu             sync.Mutex
		telemetryCalls []RelayTelemetrySyncRequest
		retireCalls    []string
		cancelCalls    []RelayCommandCancelRequest
		failFirstSync  atomic.Bool
	)
	failFirstSync.Store(true)

	telServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != outboxTelemetrySyncPath {
			http.NotFound(w, r)
			return
		}
		if failFirstSync.Swap(false) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("transient telemetry unavailable"))
			return
		}
		var req RelayTelemetrySyncRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		telemetryCalls = append(telemetryCalls, req)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer telServer.Close()

	connServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		retireCalls = append(retireCalls, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer connServer.Close()

	cmdServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != outboxCommandCancelPath {
			http.NotFound(w, r)
			return
		}
		var req RelayCommandCancelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		cancelCalls = append(cancelCalls, req)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cancelledCommandIds":[]}`))
	}))
	defer cmdServer.Close()

	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	relay, err := NewOutboxRelay(store, OutboxRelayConfig{
		TelemetryBaseURL:    telServer.URL,
		ConnectivityBaseURL: connServer.URL,
		CommandBaseURL:      cmdServer.URL,
		LeaseOwner:          "test-outbox-relay",
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	site, _, err := store.SaveSite(ctx, writerIntegrationClaims(registryauth.ActionSiteWrite), SiteMutation{
		Code: "relay-site-01", DisplayName: "Outbox Relay Site", Timezone: "Asia/Shanghai", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "relay-site-create-01", Reason: "create site for outbox relay test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	devClaims := writerIntegrationClaims(registryauth.ActionDeviceWrite, site.ID)
	gateway, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site.ID, Code: "relay-gw-01", DisplayName: "Relay GW", DeviceType: "GATEWAY", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "relay-gw-create-01", Reason: "create gateway for outbox relay test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chiller, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site.ID, Code: "relay-chiller-01", DisplayName: "Relay Chiller", DeviceType: "CHILLER", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "relay-chiller-create-01", Reason: "create chiller for outbox relay test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ptClaims := writerIntegrationClaims(registryauth.ActionPointWrite, site.ID)
	point, _, err := store.SavePoint(ctx, ptClaims, PointMutation{
		SiteID: site.ID, ReportingDeviceID: chiller.ID, PointCode: "chw_supply_temp", SourceKey: "temp-1",
		DisplayName: "CHW Supply Temp", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel",
		SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 300000,
		SourceMetadata: map[string]any{}, Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "relay-pt-create-01", Reason: "create telemetry point for outbox relay test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Drain initial creation outbox events; first telemetry sync fails with 503, forcing retry scheduling.
	if processed, err := relay.ProcessOnce(ctx); err != nil || processed < 3 {
		t.Fatalf("first ProcessOnce processed=%d err=%v", processed, err)
	}
	// Advance simulated clock past 1s backoff so failed delivery retries cleanly.
	now = now.Add(5 * time.Second)
	if _, err := relay.ProcessOnce(ctx); err != nil {
		t.Fatalf("retry ProcessOnce err=%v", err)
	}

	// Retire the gateway and verify Connectivity + Command + Telemetry are all invoked.
	retireClaims := writerIntegrationClaims(registryauth.ActionRegistryRetire, site.ID)
	retiredGW, err := store.Retire(ctx, retireClaims, RetireRequest{
		SiteID: site.ID, ResourceType: ResourceDevice, ResourceID: gateway.ID,
		Meta: MutationMeta{ExpectedRevision: gateway.Revision, IdempotencyKey: "relay-gw-retire-01", Reason: "retire gateway and relay downstream"},
	})
	if err != nil || retiredGW.Status != "COMPLETED" {
		t.Fatalf("retire gateway res=%#v err=%v", retiredGW, err)
	}
	now = now.Add(5 * time.Second)
	if _, err := relay.ProcessOnce(ctx); err != nil {
		t.Fatalf("retire ProcessOnce err=%v", err)
	}

	mu.Lock()
	gotTel := append([]RelayTelemetrySyncRequest(nil), telemetryCalls...)
	gotRetire := append([]string(nil), retireCalls...)
	gotCancel := append([]RelayCommandCancelRequest(nil), cancelCalls...)
	mu.Unlock()

	if len(gotTel) < 3 {
		t.Fatalf("expected >= 3 telemetry sync calls after retry + retire, got %d", len(gotTel))
	}
	expectedRetirePath := "/internal/v1/gateways/" + gateway.ID + ":retire"
	if len(gotRetire) != 1 || gotRetire[0] != expectedRetirePath {
		t.Fatalf("expected single connectivity retire call to %q, got %v", expectedRetirePath, gotRetire)
	}
	if len(gotCancel) != 1 || len(gotCancel[0].DeviceIDs) != 1 || gotCancel[0].DeviceIDs[0] != gateway.ID {
		t.Fatalf("expected command cancel call for gateway %s, got %#v", gateway.ID, gotCancel)
	}
	// Ensure our delivery rows settled to DELIVERED for point/gateway events.
	var undelivered int
	if err := relay.withTenantWriterTx(ctx, site.TenantID, pgx.ReadOnly, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT count(*)
FROM core_registry.domain_event_deliveries d
JOIN core_registry.domain_outbox_events e ON e.id = d.event_id
WHERE d.consumer_name = $1
  AND e.subject_id IN ($2::uuid, $3::uuid, $4::uuid)
  AND d.status <> 'DELIVERED'
`, outboxRelayConsumerName, gateway.ID, chiller.ID, point.ID).Scan(&undelivered)
	}); err != nil {
		t.Fatal(err)
	}
	if undelivered != 0 {
		t.Fatalf("expected 0 undelivered rows for relay test subjects, got %d", undelivered)
	}
}
