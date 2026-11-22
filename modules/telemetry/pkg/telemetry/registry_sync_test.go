package telemetry

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type stubRegistrySyncer struct {
	lastReq RegistrySyncRequest
	result  RegistrySyncResult
	err     error
}

func (s *stubRegistrySyncer) ApplyRegistrySync(_ context.Context, req RegistrySyncRequest) (RegistrySyncResult, error) {
	s.lastReq = req
	return s.result, s.err
}

func registrySyncRequestWithSPIFFE(t *testing.T, method, spiffe, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, InternalRegistrySyncPath, bytes.NewBufferString(body))
	if spiffe != "" {
		uri, err := url.Parse(spiffe)
		if err != nil {
			t.Fatal(err)
		}
		cert := &x509.Certificate{URIs: []*url.URL{uri}}
		req.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
			VerifiedChains:   [][]*x509.Certificate{{cert}},
		}
	}
	return req
}

func TestTelemetryHandlerRegistrySyncAuthAndValidation(t *testing.T) {
	syncer := &stubRegistrySyncer{result: RegistrySyncResult{SyncedDevices: 1, RetiredPoints: 1}}
	h := NewHandler(ServerConfig{RegistrySyncer: syncer})

	tenantID := "0193f200-1000-7000-8000-000000000001"
	siteID := "0193f200-2000-7000-8000-000000000001"
	deviceID := "0193f200-3000-7000-8000-000000000001"
	bodyBytes, err := json.Marshal(RegistrySyncRequest{
		TenantID: tenantID,
		SiteID:   siteID,
		Devices: []RegistryDeviceSync{{
			DeviceID: deviceID,
			Status:   "ACTIVE",
			Points: []RegistryPointSync{
				{PointID: "0193f200-4000-7000-8000-000000000001", TelemetryKey: "chw.supply_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", SampleIntervalSeconds: 60, FreshWithinSeconds: 300, Status: "ACTIVE"},
				{PointID: "0193f200-4000-7000-8000-000000000002", TelemetryKey: "start_cmd", PointType: "COMMAND", ValueType: "BOOLEAN", Status: "ACTIVE"},
			},
		}},
		RetiredPoints: []RegistryRetiredPoint{{DeviceID: deviceID, TelemetryKey: "old.temp"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)

	// Missing mTLS or unauthorized SPIFFE rejected.
	for _, spiffe := range []string{"", "spiffe://hvac.local/command-dispatcher"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, registrySyncRequestWithSPIFFE(t, http.MethodPost, spiffe, body))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("spiffe=%q status=%d, want 401", spiffe, rec.Code)
		}
	}

	// Forged identity header rejected.
	forged := registrySyncRequestWithSPIFFE(t, http.MethodPost, "spiffe://hvac.local/platform-core-service", body)
	forged.Header.Set("X-Organization-ID", tenantID)
	forgedRec := httptest.NewRecorder()
	h.ServeHTTP(forgedRec, forged)
	if forgedRec.Code != http.StatusBadRequest {
		t.Fatalf("forged header status=%d, want 400", forgedRec.Code)
	}

	// Valid core-service request succeeds.
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, registrySyncRequestWithSPIFFE(t, http.MethodPost, "spiffe://hvac.local/platform-core-service", body))
	if okRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", okRec.Code, okRec.Body.String())
	}
	if syncer.lastReq.TenantID != tenantID || len(syncer.lastReq.Devices) != 1 || len(syncer.lastReq.RetiredPoints) != 1 {
		t.Fatalf("unexpected syncer request: %#v", syncer.lastReq)
	}

	// Invalid sleep-cycle PresencePolicy ranges are rejected with 400.
	for _, badPolicy := range []RegistryPresencePolicySync{
		{OnlineWithinSeconds: 0, SleepCycleSeconds: 300, OfflineAfterSeconds: 600},
		{OnlineWithinSeconds: 120, SleepCycleSeconds: 60, OfflineAfterSeconds: 300},
		{OnlineWithinSeconds: 60, SleepCycleSeconds: 300, OfflineAfterSeconds: 300},
	} {
		badBytes, marshalErr := json.Marshal(RegistrySyncRequest{
			TenantID: tenantID,
			SiteID:   siteID,
			Devices: []RegistryDeviceSync{{
				DeviceID:       deviceID,
				Status:         "ACTIVE",
				PresencePolicy: &badPolicy,
			}},
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, registrySyncRequestWithSPIFFE(t, http.MethodPost, "spiffe://hvac.local/platform-core-service", string(badBytes)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid PresencePolicy %+v, got %d", badPolicy, rec.Code)
		}
	}

	// Valid sleep-cycle PresencePolicy passes validation and reaches syncer.
	goodSleepBytes, err := json.Marshal(RegistrySyncRequest{
		TenantID: tenantID,
		SiteID:   siteID,
		Devices: []RegistryDeviceSync{{
			DeviceID:       deviceID,
			Status:         "ACTIVE",
			PresencePolicy: &RegistryPresencePolicySync{OnlineWithinSeconds: 60, SleepCycleSeconds: 900, OfflineAfterSeconds: 1200},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sleepRec := httptest.NewRecorder()
	h.ServeHTTP(sleepRec, registrySyncRequestWithSPIFFE(t, http.MethodPost, "spiffe://hvac.local/platform-core-service", string(goodSleepBytes)))
	if sleepRec.Code != http.StatusOK || syncer.lastReq.Devices[0].PresencePolicy == nil || syncer.lastReq.Devices[0].PresencePolicy.SleepCycleSeconds != 900 {
		t.Fatalf("valid PresencePolicy sync failed: code=%d policy=%#v", sleepRec.Code, syncer.lastReq.Devices[0].PresencePolicy)
	}
}

func TestPostgresApplyRegistrySyncUpsertsPoliciesPreservesQualityAndRetiresDevice(t *testing.T) {
	runtimeURL := os.Getenv("S2_TELEMETRY_TEST_DATABASE_URL")
	adminURL := os.Getenv("S2_TELEMETRY_ADMIN_DATABASE_URL")
	if runtimeURL == "" || adminURL == "" {
		t.Skip("S2_TELEMETRY_TEST_DATABASE_URL and S2_TELEMETRY_ADMIN_DATABASE_URL are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tenantID := "0193f300-1000-7000-8000-000000000001"
	siteID := "0193f300-2000-7000-8000-000000000001"
	deviceID := "0193f300-3000-7000-8000-000000000001"

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, q := range []string{
			`DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.presence_signals WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.observation_coverage WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.freshness_policies WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.presence_policies WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.devices WHERE device_id = $1::uuid`,
		} {
			_, _ = admin.Exec(cleanupCtx, q, deviceID)
		}
	})

	// Initial active sync creates device, default presence policy, coverage, and configured non-command freshness policy.
	res, err := store.ApplyRegistrySync(ctx, RegistrySyncRequest{
		TenantID: tenantID,
		SiteID:   siteID,
		Devices: []RegistryDeviceSync{{
			DeviceID: deviceID,
			Status:   "ACTIVE",
			Points: []RegistryPointSync{
				{PointID: "0193f300-4000-7000-8000-000000000001", TelemetryKey: "chw.supply_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", SampleIntervalSeconds: 60, FreshWithinSeconds: 300, Status: "ACTIVE"},
				{PointID: "0193f300-4000-7000-8000-000000000002", TelemetryKey: "chw.start_cmd", PointType: "COMMAND", ValueType: "BOOLEAN", Status: "ACTIVE"},
			},
		}},
	})
	if err != nil || res.SyncedDevices != 1 {
		t.Fatalf("initial ApplyRegistrySync err=%v res=%#v", err, res)
	}

	// Simulate operator-configured quality_checks & numeric bounds on freshness_policies.
	if _, err := admin.Exec(ctx, `
UPDATE telemetry_runtime.freshness_policies
SET minimum_number = -10, maximum_number = 60, quality_checks = '{"flatlineWindowMinutes":30}'::jsonb
WHERE device_id = $1::uuid AND telemetry_key = 'chw.supply_temp'
`, deviceID); err != nil {
		t.Fatal(err)
	}

	// Identical re-sync must NOT bump policy_revision.
	if _, err := store.ApplyRegistrySync(ctx, RegistrySyncRequest{
		TenantID: tenantID,
		SiteID:   siteID,
		Devices: []RegistryDeviceSync{{
			DeviceID: deviceID,
			Status:   "ACTIVE",
			Points: []RegistryPointSync{
				{PointID: "0193f300-4000-7000-8000-000000000001", TelemetryKey: "chw.supply_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", SampleIntervalSeconds: 60, FreshWithinSeconds: 300, Status: "ACTIVE"},
			},
		}},
	}); err != nil {
		t.Fatalf("identical ApplyRegistrySync: %v", err)
	}
	var unchangedRev int64
	if err := admin.QueryRow(ctx, `SELECT policy_revision FROM telemetry_runtime.freshness_policies WHERE device_id = $1::uuid AND telemetry_key = 'chw.supply_temp'`, deviceID).Scan(&unchangedRev); err != nil {
		t.Fatal(err)
	}
	if unchangedRev != 1 {
		t.Fatalf("expected policy_revision to remain 1 on identical re-sync, got %d", unchangedRev)
	}

	// Re-sync with changed window and second point verifies COMMAND point skipped, custom bounds preserved,
	// policy_revision bumped, and device_observation_snapshots updated immediately.
	if _, err := store.ApplyRegistrySync(ctx, RegistrySyncRequest{
		TenantID: tenantID,
		SiteID:   siteID,
		Devices: []RegistryDeviceSync{{
			DeviceID: deviceID,
			Status:   "ACTIVE",
			Points: []RegistryPointSync{
				{PointID: "0193f300-4000-7000-8000-000000000001", TelemetryKey: "chw.supply_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", SampleIntervalSeconds: 30, FreshWithinSeconds: 180, Status: "ACTIVE"},
				{PointID: "0193f300-4000-7000-8000-000000000003", TelemetryKey: "chw.return_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", SampleIntervalSeconds: 30, FreshWithinSeconds: 180, Status: "ACTIVE"},
			},
		}},
	}); err != nil {
		t.Fatalf("second ApplyRegistrySync: %v", err)
	}

	var minVal, maxVal *float64
	var qc string
	var freshSec, cmdCount int
	var policyRev int64
	if err := admin.QueryRow(ctx, `
SELECT policy_revision, fresh_within_seconds, minimum_number, maximum_number, COALESCE(quality_checks::text, '')
FROM telemetry_runtime.freshness_policies
WHERE device_id = $1::uuid AND telemetry_key = 'chw.supply_temp'
`, deviceID).Scan(&policyRev, &freshSec, &minVal, &maxVal, &qc); err != nil {
		t.Fatal(err)
	}
	if policyRev != 2 || freshSec != 180 || minVal == nil || *minVal != -10 || maxVal == nil || *maxVal != 60 || qc == "" {
		t.Fatalf("custom bounds/quality checks or policy_revision mismatch: rev=%d fresh=%d min=%v max=%v qc=%q", policyRev, freshSec, minVal, maxVal, qc)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.freshness_policies WHERE device_id = $1::uuid AND telemetry_key = 'chw.start_cmd'`, deviceID).Scan(&cmdCount); err != nil || cmdCount != 0 {
		t.Fatalf("COMMAND point unexpectedly synced into freshness_policies (count=%d err=%v)", cmdCount, err)
	}
	var hasReturnBefore bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM jsonb_array_elements(snapshot->'values') AS item WHERE item->>'key' = 'chw.return_temp') FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`, deviceID).Scan(&hasReturnBefore); err != nil || !hasReturnBefore {
		t.Fatalf("expected newly synced point chw.return_temp in snapshot immediately (has=%v err=%v)", hasReturnBefore, err)
	}

	// Retiring a single point removes it from device_observation_snapshots immediately and stops bumping policy_revision on repeat.
	if _, err := store.ApplyRegistrySync(ctx, RegistrySyncRequest{
		TenantID:      tenantID,
		SiteID:        siteID,
		RetiredPoints: []RegistryRetiredPoint{{DeviceID: deviceID, TelemetryKey: "chw.return_temp"}},
	}); err != nil {
		t.Fatalf("retire single point: %v", err)
	}
	var hasReturnAfter bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM jsonb_array_elements(snapshot->'values') AS item WHERE item->>'key' = 'chw.return_temp') FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`, deviceID).Scan(&hasReturnAfter); err != nil || hasReturnAfter {
		t.Fatalf("expected retired point chw.return_temp cleared from snapshot immediately (has=%v err=%v)", hasReturnAfter, err)
	}
	if _, err := store.ApplyRegistrySync(ctx, RegistrySyncRequest{
		TenantID:      tenantID,
		SiteID:        siteID,
		RetiredPoints: []RegistryRetiredPoint{{DeviceID: deviceID, TelemetryKey: "chw.return_temp"}},
	}); err != nil {
		t.Fatalf("repeat retire single point: %v", err)
	}
	var retiredRev int64
	if err := admin.QueryRow(ctx, `SELECT policy_revision FROM telemetry_runtime.freshness_policies WHERE device_id = $1::uuid AND telemetry_key = 'chw.return_temp'`, deviceID).Scan(&retiredRev); err != nil {
		t.Fatal(err)
	}
	if retiredRev != 2 {
		t.Fatalf("expected retired point policy_revision=2 without churn on repeat, got %d", retiredRev)
	}

	// Retiring device sets presence_applicability='NOT_APPLICABLE', configured=false, and persists snapshot.
	if _, err := store.ApplyRegistrySync(ctx, RegistrySyncRequest{
		TenantID: tenantID,
		SiteID:   siteID,
		Devices: []RegistryDeviceSync{{
			DeviceID: deviceID,
			Status:   "RETIRED",
			Points: []RegistryPointSync{
				{PointID: "0193f300-4000-7000-8000-000000000001", TelemetryKey: "chw.supply_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", SampleIntervalSeconds: 30, FreshWithinSeconds: 180, Status: "RETIRED"},
			},
		}},
	}); err != nil {
		t.Fatalf("retire ApplyRegistrySync: %v", err)
	}

	var applicability string
	var configured bool
	if err := admin.QueryRow(ctx, `SELECT d.presence_applicability, f.configured FROM telemetry_runtime.devices d JOIN telemetry_runtime.freshness_policies f ON f.device_id = d.device_id WHERE d.device_id = $1::uuid AND f.telemetry_key = 'chw.supply_temp'`, deviceID).Scan(&applicability, &configured); err != nil {
		t.Fatal(err)
	}
	if applicability != "NOT_APPLICABLE" || configured {
		t.Fatalf("expected NOT_APPLICABLE and configured=false after retire, got app=%q configured=%v", applicability, configured)
	}

	var snapApplicability string
	if err := admin.QueryRow(ctx, `SELECT applicability FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`, deviceID).Scan(&snapApplicability); err != nil {
		t.Fatal(err)
	}
	if snapApplicability != "NOT_APPLICABLE" {
		t.Fatalf("expected device_presence applicability NOT_APPLICABLE after snapshot eval, got %q", snapApplicability)
	}
}

func TestPostgresApplyRegistrySyncSleepCyclePolicy(t *testing.T) {
	runtimeURL := os.Getenv("S2_TELEMETRY_TEST_DATABASE_URL")
	adminURL := os.Getenv("S2_TELEMETRY_ADMIN_DATABASE_URL")
	if runtimeURL == "" || adminURL == "" {
		t.Skip("S2_TELEMETRY_TEST_DATABASE_URL and S2_TELEMETRY_ADMIN_DATABASE_URL are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tenantID := "0193f300-1000-7000-8000-000000000002"
	siteID := "0193f300-2000-7000-8000-000000000002"
	deviceID := "0193f300-3000-7000-8000-000000000002"
	pointID := "0193f300-4000-7000-8000-000000000009"

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, q := range []string{
			`DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.presence_signals WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.observation_coverage WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.freshness_policies WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.presence_policies WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.devices WHERE device_id = $1::uuid`,
		} {
			_, _ = admin.Exec(cleanupCtx, q, deviceID)
		}
	})

	if _, err := store.ApplyRegistrySync(ctx, RegistrySyncRequest{
		TenantID: tenantID,
		SiteID:   siteID,
		Devices: []RegistryDeviceSync{{
			DeviceID:       deviceID,
			Status:         "ACTIVE",
			PresencePolicy: &RegistryPresencePolicySync{OnlineWithinSeconds: 60, SleepCycleSeconds: 900, OfflineAfterSeconds: 1200},
			Points: []RegistryPointSync{
				{PointID: pointID, TelemetryKey: "valve_position", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "%", SampleIntervalSeconds: 900, FreshWithinSeconds: 1800, Status: "ACTIVE"},
			},
		}},
	}); err != nil {
		t.Fatalf("sleep-cycle ApplyRegistrySync: %v", err)
	}

	var sleepSec *int
	var hasExplicitSleep bool
	if err := admin.QueryRow(ctx, `
SELECT sleep_cycle_seconds, ('EXPLICIT_SLEEP' = ANY(accepted_signal_types))
FROM telemetry_runtime.presence_policies
WHERE device_id = $1::uuid`, deviceID).Scan(&sleepSec, &hasExplicitSleep); err != nil {
		t.Fatal(err)
	}
	if sleepSec == nil || *sleepSec != 900 || !hasExplicitSleep {
		t.Fatalf("expected sleep_cycle_seconds=900 and EXPLICIT_SLEEP allowed, got %v / %v", sleepSec, hasExplicitSleep)
	}

	// Clearing PresencePolicy restores sleep_cycle_seconds=NULL and drops EXPLICIT_SLEEP from accepted signals.
	if _, err := store.ApplyRegistrySync(ctx, RegistrySyncRequest{
		TenantID: tenantID,
		SiteID:   siteID,
		Devices: []RegistryDeviceSync{{
			DeviceID: deviceID,
			Status:   "ACTIVE",
			Points: []RegistryPointSync{
				{PointID: pointID, TelemetryKey: "valve_position", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "%", SampleIntervalSeconds: 60, FreshWithinSeconds: 300, Status: "ACTIVE"},
			},
		}},
	}); err != nil {
		t.Fatalf("clear sleep-cycle ApplyRegistrySync: %v", err)
	}

	if err := admin.QueryRow(ctx, `
SELECT sleep_cycle_seconds, ('EXPLICIT_SLEEP' = ANY(accepted_signal_types))
FROM telemetry_runtime.presence_policies
WHERE device_id = $1::uuid`, deviceID).Scan(&sleepSec, &hasExplicitSleep); err != nil {
		t.Fatal(err)
	}
	if sleepSec != nil || hasExplicitSleep {
		t.Fatalf("expected sleep_cycle_seconds=NULL and EXPLICIT_SLEEP removed, got %v / %v", sleepSec, hasExplicitSleep)
	}
}
