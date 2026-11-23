package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const testTenantB = "018f1d00-0000-7000-8000-000000000002"

func TestPostgresDeviceTransferCrossSiteAndCrossTenantIsolation(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	suffix := strconv.FormatInt(time.Now().UTC().UnixNano(), 36)

	siteClaimsA := writerIntegrationClaims(registryauth.ActionSiteWrite)
	siteA1, _, err := store.SaveSite(ctx, siteClaimsA, SiteMutation{
		Code: "xfer-site-a1-" + suffix, DisplayName: "Transfer Site A1", Timezone: "Asia/Shanghai", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "xfer-site-a1-" + suffix, Reason: "create source site A1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	siteA2, _, err := store.SaveSite(ctx, siteClaimsA, SiteMutation{
		Code: "xfer-site-a2-" + suffix, DisplayName: "Transfer Site A2", Timezone: "Asia/Shanghai", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "xfer-site-a2-" + suffix, Reason: "create target site A2"},
	})
	if err != nil {
		t.Fatal(err)
	}

	siteClaimsB := writerIntegrationClaims(registryauth.ActionSiteWrite)
	siteClaimsB.TenantID = testTenantB
	siteB1, _, err := store.SaveSite(ctx, siteClaimsB, SiteMutation{
		Code: "xfer-site-b1-" + suffix, DisplayName: "Transfer Site B1", Timezone: "Asia/Shanghai", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "xfer-site-b1-" + suffix, Reason: "create target tenant site B1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	devClaimsA := writerIntegrationClaims(registryauth.ActionDeviceWrite, siteA1.ID, siteA2.ID)
	directDev, _, err := store.SaveDevice(ctx, devClaimsA, DeviceMutation{
		SiteID: siteA1.ID, Code: "xfer-direct-" + suffix, DisplayName: "Direct Meter 01", DeviceType: "POWER_METER",
		Status: "ACTIVE", ConnectivityMode: "DIRECT", Tags: map[string]string{"env": "old-site-only"},
		Meta: MutationMeta{IdempotencyKey: "xfer-direct-create-" + suffix, Reason: "create direct device for cross-site transfer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	extSN := "SN-DIRECT-" + suffix
	hwReplaced, err := store.ReplaceDeviceHardware(ctx, devClaimsA, ReplaceDeviceHardwareRequest{
		SiteID: siteA1.ID, DeviceID: directDev.ID, NewExternalID: extSN,
		NewProtocolAddress: map[string]any{"modbusUnit": 11, "host": "192.168.10.22"},
		Meta:               MutationMeta{ExpectedRevision: directDev.Revision, IdempotencyKey: "xfer-hw-" + suffix, Reason: "assign hardware external id before transfer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	directDev = hwReplaced.Device

	// Active point on source device must block transfer (ErrTransferBlocked).
	ptClaimsA := writerIntegrationClaims(registryauth.ActionPointWrite, siteA1.ID)
	decreaseMode := "RESET_TO_ZERO"
	pt, _, err := store.SavePoint(ctx, ptClaimsA, PointMutation{
		SiteID: siteA1.ID, ReportingDeviceID: directDev.ID, PointCode: "total_kwh", SourceKey: "kwh",
		DisplayName: "Total kWh", PointType: "COUNTER", ValueType: "NUMBER", Unit: "kWh",
		SampleIntervalMS: 1000, PublishIntervalMS: 5000, StaleAfterMS: 30000,
		CounterDecreaseMode: &decreaseMode, SourceMetadata: map[string]any{}, Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "xfer-pt-create-" + suffix, Reason: "create point blocking transfer"},
	})
	if err != nil {
		t.Fatal(err)
	}

	blockedReq := TransferDeviceRequest{
		SourceSiteID: siteA1.ID, DeviceID: directDev.ID, TargetSiteID: siteA2.ID,
		Meta: MutationMeta{ExpectedRevision: directDev.Revision, IdempotencyKey: "xfer-blocked-" + suffix, Reason: "must block while active point exists"},
	}
	if _, err := store.TransferDevice(ctx, devClaimsA, nil, blockedReq); !errors.Is(err, ErrTransferBlocked) {
		t.Fatalf("expected ErrTransferBlocked while point is active, got %v", err)
	}

	// Retire the point to unblock transfer.
	retireClaimsA := writerIntegrationClaims(registryauth.ActionRegistryRetire, siteA1.ID)
	if res, err := store.Retire(ctx, retireClaimsA, RetireRequest{
		SiteID: siteA1.ID, ResourceType: ResourcePoint, ResourceID: pt.ID,
		Meta: MutationMeta{ExpectedRevision: pt.Revision, IdempotencyKey: "xfer-pt-retire-" + suffix, Reason: "retire point before transfer"},
	}); err != nil || res.Status != "COMPLETED" {
		t.Fatalf("retire point failed: res=%#v err=%v", res, err)
	}

	// Same-site transfer rejected.
	if _, err := store.TransferDevice(ctx, devClaimsA, nil, TransferDeviceRequest{
		SourceSiteID: siteA1.ID, DeviceID: directDev.ID, TargetSiteID: siteA1.ID,
		Meta: MutationMeta{ExpectedRevision: directDev.Revision, IdempotencyKey: "xfer-same-site-" + suffix, Reason: "same site invalid"},
	}); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("expected ErrInvalidMutation on same-site transfer, got %v", err)
	}

	// 1. Same-tenant cross-site transfer (Site A1 -> Site A2).
	newCode := "xfer-direct-a2-" + suffix
	newName := "Direct Meter Site A2"
	crossSiteReq := TransferDeviceRequest{
		SourceSiteID: siteA1.ID, DeviceID: directDev.ID, TargetSiteID: siteA2.ID,
		NewCode: &newCode, NewDisplayName: &newName,
		Meta: MutationMeta{ExpectedRevision: directDev.Revision, IdempotencyKey: "xfer-cross-site-" + suffix, Reason: "迁至二号冷站"},
	}
	crossSiteRes, err := store.TransferDevice(ctx, devClaimsA, nil, crossSiteReq)
	if err != nil {
		t.Fatalf("cross-site TransferDevice failed: %v", err)
	}
	if crossSiteRes.Replayed || crossSiteRes.TransferScope != TransferScopeCrossSite ||
		crossSiteRes.SourceDeviceID != directDev.ID || crossSiteRes.TargetDeviceID == directDev.ID ||
		crossSiteRes.SourceDevice.Status != "RETIRED" || crossSiteRes.TargetDevice.Status != "ACTIVE" ||
		crossSiteRes.TargetDevice.SiteID != siteA2.ID || crossSiteRes.TargetDevice.Code != newCode ||
		crossSiteRes.TargetDevice.ConnectivityMode != "DIRECT" || len(crossSiteRes.TargetDevice.Tags) != 0 ||
		crossSiteRes.TargetDevice.ExternalID == nil || *crossSiteRes.TargetDevice.ExternalID != extSN ||
		!crossSiteRes.WipeRequired {
		t.Fatalf("unexpected cross-site transfer result: %#v", crossSiteRes)
	}

	// Idempotent replay succeeds.
	replayed, err := store.TransferDevice(ctx, devClaimsA, nil, crossSiteReq)
	if err != nil || !replayed.Replayed || replayed.TransferID != crossSiteRes.TransferID || replayed.TargetDeviceID != crossSiteRes.TargetDeviceID {
		t.Fatalf("expected idempotent replay, got res=%#v err=%v", replayed, err)
	}

	// Stale revision or re-transfer of retired source fails.
	staleReq := crossSiteReq
	staleReq.Meta.IdempotencyKey = "xfer-retry-new-key-" + suffix
	if _, err := store.TransferDevice(ctx, devClaimsA, nil, staleReq); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("expected ErrInvalidMutation re-transferring retired device, got %v", err)
	}

	// 2. Cross-tenant transfer (Tenant A Site A2 -> Tenant B Site B1).
	movedDev := crossSiteRes.TargetDevice
	targetClaimsB := writerIntegrationClaims(registryauth.ActionDeviceWrite, siteB1.ID)
	targetClaimsB.TenantID = testTenantB

	// Mismatched principal on target claims fails closed.
	wrongPrincipal := targetClaimsB
	wrongPrincipal.PrincipalID = "018f1e00-2000-7000-8000-000000000099"
	targetTenantB := testTenantB
	crossTenantReq := TransferDeviceRequest{
		SourceSiteID: siteA2.ID, DeviceID: movedDev.ID,
		TargetTenantID: &targetTenantB, TargetSiteID: siteB1.ID,
		Meta: MutationMeta{ExpectedRevision: movedDev.Revision, IdempotencyKey: "xfer-cross-tenant-" + suffix, Reason: "跨租户资产过户交接"},
	}
	if _, err := store.TransferDevice(ctx, devClaimsA, &wrongPrincipal, crossTenantReq); !errors.Is(err, ErrTransferForbidden) {
		t.Fatalf("expected ErrTransferForbidden on principal mismatch, got %v", err)
	}
	if _, err := store.TransferDevice(ctx, devClaimsA, nil, crossTenantReq); !errors.Is(err, ErrTransferForbidden) {
		t.Fatalf("expected ErrTransferForbidden when target claims missing, got %v", err)
	}

	crossTenantRes, err := store.TransferDevice(ctx, devClaimsA, &targetClaimsB, crossTenantReq)
	if err != nil {
		t.Fatalf("cross-tenant TransferDevice failed: %v", err)
	}
	if crossTenantRes.TransferScope != TransferScopeCrossTenant ||
		crossTenantRes.SourceTenantID != testTenantA || crossTenantRes.TargetTenantID != testTenantB ||
		crossTenantRes.SourceDeviceID == crossTenantRes.TargetDeviceID ||
		crossTenantRes.TargetDevice.TenantID != testTenantB || crossTenantRes.TargetDevice.SiteID != siteB1.ID ||
		crossTenantRes.TargetDevice.ConnectivityMode != "DIRECT" {
		t.Fatalf("unexpected cross-tenant transfer result: %#v", crossTenantRes)
	}

	// Verify strict historical data boundary isolation under s1_core_runtime RLS:
	// Tenant B CANNOT read Tenant A's retired device, directDev history, or hardware replacement history.
	readClaimsB := integrationClaims(registryauth.ActionDeviceRead)
	readClaimsB.TenantID = testTenantB
	readClaimsB.AllowedSiteIDs = []string{siteB1.ID}
	gotB, err := store.GetDevice(ctx, readClaimsB, crossTenantRes.TargetDeviceID)
	if err != nil || gotB.ID != crossTenantRes.TargetDeviceID {
		t.Fatalf("Tenant B GetDevice target failed: got=%#v err=%v", gotB, err)
	}
	if _, err := store.GetDevice(ctx, readClaimsB, directDev.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Tenant B leaked original source device %s: err=%v", directDev.ID, err)
	}
	if _, err := store.GetDevice(ctx, readClaimsB, movedDev.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Tenant B leaked retired Tenant A device %s: err=%v", movedDev.ID, err)
	}

	// Tenant A CANNOT read Tenant B's new device.
	readClaimsA := writerIntegrationClaims(registryauth.ActionDeviceRead, siteA1.ID, siteA2.ID)
	if _, err := store.GetDevice(ctx, readClaimsA, crossTenantRes.TargetDeviceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Tenant A leaked Tenant B target device %s: err=%v", crossTenantRes.TargetDeviceID, err)
	}
}

func TestPostgresOutboxRelayDeviceTransferredDecommissionsGatewayAndDirectDevice(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	suffix := strconv.FormatInt(time.Now().UTC().UnixNano(), 36)

	var (
		mu             sync.Mutex
		telemetryCalls []RelayTelemetrySyncRequest
		decommCalls    []string
		decommBodies   []RelayGatewayTransferDecommissionRequest
		cancelCalls    []RelayCommandCancelRequest
	)
	telServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		var req RelayGatewayTransferDecommissionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		decommCalls = append(decommCalls, r.URL.Path)
		decommBodies = append(decommBodies, req)
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

	relay, err := NewOutboxRelay(store, OutboxRelayConfig{
		TelemetryBaseURL:    telServer.URL,
		ConnectivityBaseURL: connServer.URL,
		CommandBaseURL:      cmdServer.URL,
		LeaseOwner:          "test-transfer-relay",
	})
	if err != nil {
		t.Fatal(err)
	}

	siteClaims := writerIntegrationClaims(registryauth.ActionSiteWrite)
	site1, _, err := store.SaveSite(ctx, siteClaims, SiteMutation{
		Code: "rel-xfer-1-" + suffix, DisplayName: "Relay Xfer 1", Timezone: "Asia/Shanghai", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "rel-xfer-s1-" + suffix, Reason: "site 1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	site2, _, err := store.SaveSite(ctx, siteClaims, SiteMutation{
		Code: "rel-xfer-2-" + suffix, DisplayName: "Relay Xfer 2", Timezone: "Asia/Shanghai", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "rel-xfer-s2-" + suffix, Reason: "site 2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	devClaims := writerIntegrationClaims(registryauth.ActionDeviceWrite, site1.ID, site2.ID)
	gw, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site1.ID, Code: "rel-xfer-gw-" + suffix, DisplayName: "Relay Xfer GW", DeviceType: "GATEWAY", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "rel-xfer-gw-" + suffix, Reason: "gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	direct, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site1.ID, Code: "rel-xfer-dir-" + suffix, DisplayName: "Relay Xfer Direct", DeviceType: "POWER_METER",
		Status: "ACTIVE", ConnectivityMode: "DIRECT",
		Meta: MutationMeta{IdempotencyKey: "rel-xfer-dir-" + suffix, Reason: "direct device"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	telemetryCalls = nil
	decommCalls = nil
	decommBodies = nil
	cancelCalls = nil
	mu.Unlock()

	gwXfer, err := store.TransferDevice(ctx, devClaims, nil, TransferDeviceRequest{
		SourceSiteID: site1.ID, DeviceID: gw.ID, TargetSiteID: site2.ID,
		Meta: MutationMeta{ExpectedRevision: gw.Revision, IdempotencyKey: "rel-gw-xfer-" + suffix, Reason: "transfer gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dirXfer, err := store.TransferDevice(ctx, devClaims, nil, TransferDeviceRequest{
		SourceSiteID: site1.ID, DeviceID: direct.ID, TargetSiteID: site2.ID,
		Meta: MutationMeta{ExpectedRevision: direct.Revision, IdempotencyKey: "rel-dir-xfer-" + suffix, Reason: "transfer direct device"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	gotDecomm := append([]string(nil), decommCalls...)
	gotBodies := append([]RelayGatewayTransferDecommissionRequest(nil), decommBodies...)
	gotTel := append([]RelayTelemetrySyncRequest(nil), telemetryCalls...)
	gotCancel := append([]RelayCommandCancelRequest(nil), cancelCalls...)
	mu.Unlock()

	if len(gotDecomm) != 2 {
		t.Fatalf("expected 2 Connectivity decommission calls (GATEWAY + DIRECT), got %v", gotDecomm)
	}
	wantPaths := map[string]string{
		"/internal/v1/gateways/" + gw.ID + ":transfer-decommission":     gwXfer.TransferID,
		"/internal/v1/gateways/" + direct.ID + ":transfer-decommission": dirXfer.TransferID,
	}
	for i, path := range gotDecomm {
		expectedXfer, ok := wantPaths[path]
		if !ok {
			t.Fatalf("unexpected Connectivity path %q", path)
		}
		if gotBodies[i].TransferID != expectedXfer || !gotBodies[i].WipeRequired || gotBodies[i].TenantID != testTenantA {
			t.Fatalf("unexpected decommission payload for %s: %+v", path, gotBodies[i])
		}
	}
	if len(gotCancel) != 2 {
		t.Fatalf("expected 2 command cancel calls for transferred source devices, got %d", len(gotCancel))
	}
	if len(gotTel) < 4 {
		t.Fatalf("expected telemetry sync calls for both retired source and new target devices, got %d", len(gotTel))
	}
	// Ensure outbox deliveries for both transfers settled to DELIVERED.
	var pending int
	if err := relay.withTenantWriterTx(ctx, testTenantA, pgx.ReadOnly, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT count(*) FROM core_registry.domain_event_deliveries d
JOIN core_registry.domain_outbox_events e ON e.id = d.event_id
WHERE d.consumer_name = $1 AND e.subject_id IN ($2::uuid, $3::uuid) AND d.status <> 'DELIVERED'
`, outboxRelayConsumerName, gw.ID, direct.ID).Scan(&pending)
	}); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("expected all transfer outbox deliveries to settle, pending=%d", pending)
	}
}
