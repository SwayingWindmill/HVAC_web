package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

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
	if truncated := truncateRelayUTF8(strings.Repeat("中", 200), 512); len(truncated) > 512 || !utf8.ValidString(truncated) {
		t.Fatalf("expected valid UTF-8 truncation <= 512 bytes, got len=%d valid=%v", len(truncated), utf8.ValidString(truncated))
	}
}

func TestPostgresOutboxRelayCopyOnlyPointRetireSaveDeviceRetireAndAttemptCap(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)

	var (
		mu             sync.Mutex
		telemetryCalls []RelayTelemetrySyncRequest
		retireCalls    []string
		cancelCalls    []RelayCommandCancelRequest
		failAlways     atomic.Bool
	)

	telServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failAlways.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(strings.Repeat("错", 250)))
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

	now := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	relay, err := NewOutboxRelay(store, OutboxRelayConfig{
		TelemetryBaseURL:    telServer.URL,
		ConnectivityBaseURL: connServer.URL,
		CommandBaseURL:      cmdServer.URL,
		LeaseOwner:          "test-relay-v2",
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	site, _, err := store.SaveSite(ctx, writerIntegrationClaims(registryauth.ActionSiteWrite), SiteMutation{
		Code: "relay-v2-site", DisplayName: "Relay V2 Site", Timezone: "Asia/Shanghai", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "relay-v2-site-01", Reason: "setup relay v2 test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	devClaims := writerIntegrationClaims(registryauth.ActionDeviceWrite, site.ID)
	copyDev, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site.ID, Code: "relay-copy-dev", DisplayName: "Copy Only Device", DeviceType: "CHILLER", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "relay-v2-copy-dev-01", Reason: "device with copied points"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ptClaims := writerIntegrationClaims(registryauth.ActionPointWrite, site.ID)
	copiedPt, _, err := store.SavePoint(ctx, ptClaims, PointMutation{
		SiteID: site.ID, ReportingDeviceID: copyDev.ID, PointCode: "copied_temp", SourceKey: "copied-temp",
		DisplayName: "Copied Temp", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel",
		SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 300000,
		SourceMetadata: map[string]any{"copied": true}, Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "relay-v2-copy-pt-01", Reason: "copied point must keep NOT_APPLICABLE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.ProcessOnce(ctx); err != nil {
		t.Fatalf("ProcessOnce copy-only: %v", err)
	}

	mu.Lock()
	copyCall := telemetryCalls[len(telemetryCalls)-1]
	telemetryCalls = nil
	mu.Unlock()
	if len(copyCall.Devices) != 1 || copyCall.Devices[0].PresenceApplicability != "NOT_APPLICABLE" {
		t.Fatalf("expected NOT_APPLICABLE for copy-only active device, got %#v", copyCall.Devices)
	}

	// Add a non-copied own TELEMETRY point -> flips to APPLICABLE; then retire it -> RetiredPoints ONLY.
	ownPt, _, err := store.SavePoint(ctx, ptClaims, PointMutation{
		SiteID: site.ID, ReportingDeviceID: copyDev.ID, PointCode: "own_temp", SourceKey: "own-temp",
		DisplayName: "Own Temp", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel",
		SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 300000,
		SourceMetadata: map[string]any{}, Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "relay-v2-own-pt-01", Reason: "own active point makes device APPLICABLE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.ProcessOnce(ctx); err != nil {
		t.Fatalf("ProcessOnce own point: %v", err)
	}
	mu.Lock()
	ownCall := telemetryCalls[len(telemetryCalls)-1]
	telemetryCalls = nil
	mu.Unlock()
	if len(ownCall.Devices) != 1 || ownCall.Devices[0].PresenceApplicability != "APPLICABLE" {
		t.Fatalf("expected APPLICABLE after own active point added, got %#v", ownCall.Devices)
	}

	// Retire ownPt via SavePoint -> deliverPointSync must send it ONLY in RetiredPoints, NOT Devices[0].Points.
	if _, _, err := store.SavePoint(ctx, ptClaims, PointMutation{
		ID: ownPt.ID, SiteID: site.ID, ReportingDeviceID: copyDev.ID, PointCode: "own_temp", SourceKey: "own-temp",
		DisplayName: "Own Temp", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel",
		SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 300000,
		SourceMetadata: map[string]any{}, Status: "RETIRED",
		Meta: MutationMeta{ExpectedRevision: ownPt.Revision, IdempotencyKey: "relay-v2-own-pt-retire-01", Reason: "retire own point"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.ProcessOnce(ctx); err != nil {
		t.Fatalf("ProcessOnce retire point: %v", err)
	}
	mu.Lock()
	retPtCall := telemetryCalls[len(telemetryCalls)-1]
	telemetryCalls = nil
	mu.Unlock()
	if retPtCall.Devices[0].PresenceApplicability != "NOT_APPLICABLE" {
		t.Fatalf("expected NOT_APPLICABLE after own point retired (only copied active point remains), got %#v", retPtCall.Devices[0])
	}
	if len(retPtCall.RetiredPoints) != 1 || retPtCall.RetiredPoints[0].TelemetryKey != "own_temp" {
		t.Fatalf("expected own_temp in RetiredPoints, got %#v", retPtCall.RetiredPoints)
	}
	for _, pt := range retPtCall.Devices[0].Points {
		if pt.PointID == ownPt.ID {
			t.Fatalf("retired point %s must not remain in Devices[0].Points", ownPt.ID)
		}
	}

	// Verify SaveDevice(..., Status: "RETIRED") triggers CommandCancel + ConnectivityRetire for GATEWAY.
	gw, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site.ID, Code: "relay-v2-gw", DisplayName: "Relay V2 GW", DeviceType: "GATEWAY", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "relay-v2-gw-01", Reason: "create gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		ID: gw.ID, SiteID: site.ID, Code: "relay-v2-gw", DisplayName: "Relay V2 GW", DeviceType: "GATEWAY", Status: "RETIRED",
		Meta: MutationMeta{ExpectedRevision: gw.Revision, IdempotencyKey: "relay-v2-gw-retire-save-01", Reason: "retire gateway via SaveDevice"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotRetire := append([]string(nil), retireCalls...)
	gotCancel := append([]RelayCommandCancelRequest(nil), cancelCalls...)
	mu.Unlock()
	if len(gotRetire) != 1 || gotRetire[0] != "/internal/v1/gateways/"+gw.ID+":retire" {
		t.Fatalf("expected SaveDevice RETIRED to invoke Connectivity retire, got %v", gotRetire)
	}
	if len(gotCancel) != 1 || len(gotCancel[0].DeviceIDs) != 1 || gotCancel[0].DeviceIDs[0] != gw.ID {
		t.Fatalf("expected SaveDevice RETIRED to invoke Command cancel, got %#v", gotCancel)
	}

	// Verify delivery retry cap stops at outboxMaxDeliveryAttempts (10) with UTF-8 valid last_error and next_retry_at NULL.
	failAlways.Store(true)
	if _, _, err := store.SavePoint(ctx, ptClaims, PointMutation{
		ID: copiedPt.ID, SiteID: site.ID, ReportingDeviceID: copyDev.ID, PointCode: "copied_temp", SourceKey: "copied-temp",
		DisplayName: "Copied Temp Updated", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel",
		SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 300000,
		SourceMetadata: map[string]any{"copied": true}, Status: "ACTIVE",
		Meta: MutationMeta{ExpectedRevision: copiedPt.Revision, IdempotencyKey: "relay-v2-copy-pt-fail-01", Reason: "exercise max attempts cap"},
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < outboxMaxDeliveryAttempts+2; i++ {
		if _, err := relay.ProcessOnce(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(2 * time.Minute)
	}
	var attempts int
	var lastErr string
	var hasNextRetry bool
	if err := relay.withTenantWriterTx(ctx, site.TenantID, pgx.ReadOnly, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT d.attempt, COALESCE(d.last_error, ''), d.next_retry_at IS NOT NULL
FROM core_registry.domain_event_deliveries d
JOIN core_registry.domain_outbox_events e ON e.id = d.event_id
WHERE d.consumer_name = $1 AND e.subject_id = $2::uuid
ORDER BY e.created_at DESC
LIMIT 1
`, outboxRelayConsumerName, copiedPt.ID).Scan(&attempts, &lastErr, &hasNextRetry)
	}); err != nil {
		t.Fatal(err)
	}
	if attempts != outboxMaxDeliveryAttempts || hasNextRetry || !utf8.ValidString(lastErr) {
		t.Fatalf("expected attempt=%d next_retry_at=NULL valid-utf8 last_error, got attempt=%d hasNextRetry=%v lastErrLen=%d", outboxMaxDeliveryAttempts, attempts, hasNextRetry, len(lastErr))
	}
}
