package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	p0TestTenantID = "0199a000-0000-7000-8000-000000000001"
	p0TestSiteID   = "0199a000-1000-7000-8000-000000000001"
	p0TestGateway  = "0199a000-2000-7000-8000-000000000001"
	p0TestDevice1  = "0199a000-3000-7000-8000-000000000001"
	p0TestDevice2  = "0199a000-3000-7000-8000-000000000002"
	p0TestPoint1A  = "0199a000-4000-7000-8000-000000000001"
	p0TestPoint1B  = "0199a000-4000-7000-8000-000000000002"
	p0TestPoint2A  = "0199a000-4000-7000-8000-000000000003"
	p0TestSourceID = "p0-batch-cov-source"
)

func openP0TelemetryPostgresFixture(t *testing.T) (*PostgresStore, *pgxpool.Pool, func()) {
	t.Helper()
	runtimeURL := os.Getenv("S2_TELEMETRY_TEST_DATABASE_URL")
	adminURL := os.Getenv("S2_TELEMETRY_ADMIN_DATABASE_URL")
	if runtimeURL == "" || adminURL == "" {
		t.Skip("S2 Telemetry PostgreSQL integration environment is not configured")
	}
	ctx := context.Background()
	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatalf("open runtime store: %v", err)
	}
	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		store.Close()
		t.Fatalf("open admin pool: %v", err)
	}
	devices := []string{p0TestGateway, p0TestDevice1, p0TestDevice2}
	cleanup := func() {
		statements := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = ANY($1::uuid[])`, []any{devices}},
			{`DELETE FROM telemetry_runtime.telemetry_history_outbox WHERE payload ->> 'source_id' = $1`, []any{p0TestSourceID}},
			{`DELETE FROM telemetry_runtime.source_delivery_evidence WHERE source_id = $1`, []any{p0TestSourceID}},
			{`DELETE FROM telemetry_runtime.ingest_quarantine WHERE source_id = $1`, []any{p0TestSourceID}},
			{`DELETE FROM telemetry_runtime.presence_signals WHERE device_id = ANY($1::uuid[])`, []any{devices}},
			{`DELETE FROM telemetry_runtime.source_observations WHERE source_id = $1`, []any{p0TestSourceID}},
			{`DELETE FROM telemetry_runtime.source_positions WHERE source_id = $1`, []any{p0TestSourceID}},
			{`DELETE FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = ANY($1::uuid[])`, []any{devices}},
			{`DELETE FROM telemetry_runtime.observation_coverage WHERE device_id = ANY($1::uuid[])`, []any{devices}},
			{`DELETE FROM telemetry_runtime.device_presence WHERE device_id = ANY($1::uuid[])`, []any{devices}},
			{`DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id = ANY($1::uuid[])`, []any{devices}},
			{`DELETE FROM telemetry_runtime.mqtt_runtime_events WHERE gateway_id = $1`, []any{p0TestGateway}},
			{`DELETE FROM telemetry_runtime.freshness_policies WHERE device_id = ANY($1::uuid[])`, []any{devices}},
			{`DELETE FROM telemetry_runtime.presence_policies WHERE device_id = ANY($1::uuid[])`, []any{devices}},
			{`DELETE FROM telemetry_runtime.points WHERE device_id = ANY($1::uuid[])`, []any{devices}},
			{`DELETE FROM telemetry_runtime.devices WHERE device_id = ANY($1::uuid[])`, []any{devices}},
		}
		for _, stmt := range statements {
			if _, execErr := adminPool.Exec(ctx, stmt.sql, stmt.args...); execErr != nil {
				t.Logf("cleanup warning (%s): %v", stmt.sql, execErr)
			}
		}
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		adminPool.Close()
		store.Close()
	})

	seedTime := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	for _, devID := range devices {
		if _, err := adminPool.Exec(ctx, `
INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4)`,
			devID, p0TestTenantID, p0TestSiteID, seedTime); err != nil {
			t.Fatalf("seed device %s: %v", devID, err)
		}
		if _, err := adminPool.Exec(ctx, `
INSERT INTO telemetry_runtime.presence_policies (
  device_id, policy_revision, online_within_seconds, offline_after_seconds,
  coverage_required, accepted_signal_types, max_future_clock_skew_seconds,
  max_source_lag_seconds, updated_at
) VALUES ($1::uuid, 1, 60, 180, true, ARRAY['SOURCE_ACTIVITY', 'EXPLICIT_CONNECT', 'EXPLICIT_DISCONNECT']::text[], 30, 600, $2)`,
			devID, seedTime); err != nil {
			t.Fatalf("seed presence policy %s: %v", devID, err)
		}
	}
	for _, pt := range []struct {
		deviceID string
		key      string
		pointID  string
		unit     string
	}{
		{p0TestDevice1, "zone.temperature", p0TestPoint1A, "Cel"},
		{p0TestDevice1, "zone.humidity", p0TestPoint1B, "%RH"},
		{p0TestDevice2, "zone.temperature", p0TestPoint2A, "Cel"},
	} {
		if _, err := adminPool.Exec(ctx, `
INSERT INTO telemetry_runtime.points (
  device_id, telemetry_key, tenant_id, site_id, point_id,
  point_type, value_type, unit, point_revision, updated_at
) VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, 'TELEMETRY', 'NUMBER', $6, 1, $7)`,
			pt.deviceID, pt.key, p0TestTenantID, p0TestSiteID, pt.pointID, pt.unit, seedTime); err != nil {
			t.Fatalf("seed point %s/%s: %v", pt.deviceID, pt.key, err)
		}
		if _, err := adminPool.Exec(ctx, `
INSERT INTO telemetry_runtime.freshness_policies (
  device_id, telemetry_key, policy_revision, fresh_within_seconds, configured,
  expected_sample_interval_seconds, value_type, expected_unit, minimum_number, maximum_number, updated_at
) VALUES ($1::uuid, $2, 1, 300, true, 60, 'NUMBER', $3, -50, 100, $4)`,
			pt.deviceID, pt.key, pt.unit, seedTime); err != nil {
			t.Fatalf("seed freshness policy %s/%s: %v", pt.deviceID, pt.key, err)
		}
	}
	return store, adminPool, cleanup
}

func TestPostgresBatchIngestEvaluatesSnapshotOncePerDirtyDevice(t *testing.T) {
	store, adminPool, _ := openP0TelemetryPostgresFixture(t)
	ctx := context.Background()
	sampledAt := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	receivedAt := time.Date(2026, 11, 16, 10, 0, 2, 0, time.UTC)
	cel := "Cel"
	rh := "%RH"

	event1A := "0199a000-5000-7000-8000-000000000001"
	event1B := "0199a000-5000-7000-8000-000000000002"
	event2A := "0199a000-5000-7000-8000-000000000003"
	eventQ := "0199a000-5000-7000-8000-000000000004"

	candidates := []ObservationCandidate{
		{
			SourceID: p0TestSourceID, SourcePath: SourcePathPush,
			Device:             &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestDevice1},
			Point:              &ResolvedPoint{PointID: p0TestPoint1A, PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &cel, PointRevision: 1},
			ExternalEntityType: "DEVICE", ExternalID: "DEV-1", TelemetryKey: "zone.temperature",
			Value: json.RawMessage(`22.4`), ValueType: "NUMBER", Unit: &cel,
			SampledAt: sampledAt, ReceivedAt: receivedAt,
			Position: SourcePosition{Partition: "p0-batch-cov-1-temp", Offset: 1, EventID: event1A},
		},
		{
			SourceID: p0TestSourceID, SourcePath: SourcePathPush,
			Device:             &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestDevice1},
			Point:              &ResolvedPoint{PointID: p0TestPoint1B, PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &rh, PointRevision: 1},
			ExternalEntityType: "DEVICE", ExternalID: "DEV-1", TelemetryKey: "zone.humidity",
			Value: json.RawMessage(`48.5`), ValueType: "NUMBER", Unit: &rh,
			SampledAt: sampledAt, ReceivedAt: receivedAt.Add(time.Millisecond),
			Position: SourcePosition{Partition: "p0-batch-cov-1-hum", Offset: 1, EventID: event1B},
		},
		{
			SourceID: p0TestSourceID, SourcePath: SourcePathPush,
			Device:             &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestDevice2},
			Point:              &ResolvedPoint{PointID: p0TestPoint2A, PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &cel, PointRevision: 1},
			ExternalEntityType: "DEVICE", ExternalID: "DEV-2", TelemetryKey: "zone.temperature",
			Value: json.RawMessage(`19.8`), ValueType: "NUMBER", Unit: &cel,
			SampledAt: sampledAt, ReceivedAt: receivedAt,
			Position: SourcePosition{Partition: "p0-batch-cov-2-temp", Offset: 1, EventID: event2A},
		},
		{
			SourceID: p0TestSourceID, SourcePath: SourcePathPush,
			ExternalEntityType: "DEVICE", ExternalID: "UNREGISTERED-DEV", TelemetryKey: "zone.temperature",
			Value: json.RawMessage(`12.0`), ValueType: "NUMBER", Unit: &cel,
			SampledAt: sampledAt, ReceivedAt: receivedAt,
			Position: SourcePosition{Partition: "p0-batch-cov-q", Offset: 1, EventID: eventQ},
		},
	}

	receipts, err := store.AcceptObservationBatch(ctx, candidates)
	if err != nil {
		t.Fatalf("AcceptObservationBatch failed: %v", err)
	}
	if len(receipts) != 4 {
		t.Fatalf("receipt count = %d, want 4", len(receipts))
	}
	if receipts[0].Status != ObservationAccepted || receipts[1].Status != ObservationAccepted || receipts[2].Status != ObservationAccepted || receipts[3].Status != ObservationQuarantined {
		t.Fatalf("unexpected receipt statuses: %+v", receipts)
	}
	if receipts[3].DeviceID != "" {
		t.Fatalf("quarantined receipt leaked deviceID %q", receipts[3].DeviceID)
	}

	var dev1Rev int64
	if err := adminPool.QueryRow(ctx, `
SELECT business_revision FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`,
		p0TestDevice1).Scan(&dev1Rev); err != nil {
		t.Fatalf("read device1 snapshot revision: %v", err)
	}
	if dev1Rev != 1 {
		t.Fatalf("device1 business_revision = %d, want 1 (single snapshot evaluation for 2 points)", dev1Rev)
	}

	var outboxCount int
	if err := adminPool.QueryRow(ctx, `
SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = $1::uuid`,
		p0TestDevice1).Scan(&outboxCount); err != nil {
		t.Fatalf("count publication outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("device1 publication outbox count = %d, want 1", outboxCount)
	}

	unseenEvent := "0199a000-5000-7000-8000-000000009999"
	unseen, err := store.FilterUnseenObservations(ctx, p0TestSourceID, []string{event1A, event1B, unseenEvent})
	if err != nil {
		t.Fatalf("FilterUnseenObservations failed: %v", err)
	}
	if len(unseen) != 1 || !unseen[unseenEvent] || unseen[event1A] || unseen[event1B] {
		t.Fatalf("unexpected FilterUnseenObservations result: %+v", unseen)
	}

	dupReceipts, err := store.AcceptObservationBatch(ctx, candidates[:2])
	if err != nil {
		t.Fatalf("duplicate batch failed: %v", err)
	}
	if dupReceipts[0].Status != ObservationDuplicate || dupReceipts[1].Status != ObservationDuplicate {
		t.Fatalf("expected DUPLICATE receipts on redelivery, got %+v", dupReceipts)
	}

	limiter := limitpolicy.NewLimiter(limitpolicy.NewMemoryCounter(64), &limitpolicy.Policy{
		Version: 1,
		Limits:  []limitpolicy.Limit{{Dimension: limitpolicy.DimensionTelemetryIngest, Window: time.Hour, Burst: 1, FailClosed: true}},
	})
	h := NewHandler(ServerConfig{
		ObservationAcceptor: store,
		AllowedSourceSPIFFE: testBatchSourceSPIFFE,
		RateLimiter:         limiter,
		Metrics:             observability.NewRegistry(),
		Now:                 func() time.Time { return receivedAt.Add(time.Second) },
	})
	makeHTTPItem := func(partition string, offset int64, eventID string) map[string]any {
		return map[string]any{
			"sourceId":           p0TestSourceID,
			"sourcePath":         "PUSH",
			"externalEntityType": "DEVICE",
			"externalId":         "DEV-1",
			"device": map[string]any{
				"tenantId": p0TestTenantID,
				"siteId":   p0TestSiteID,
				"deviceId": p0TestDevice1,
			},
			"point": map[string]any{
				"pointId":       p0TestPoint1A,
				"pointType":     "TELEMETRY",
				"valueType":     "NUMBER",
				"unit":          cel,
				"pointRevision": 1,
			},
			"telemetryKey": "zone.temperature",
			"value":        22.5,
			"valueType":    "NUMBER",
			"unit":         cel,
			"wireQuality":  0,
			"sampledAt":    sampledAt.Format(time.RFC3339),
			"sourcePosition": map[string]any{
				"partition": partition,
				"offset":    offset,
				"eventId":   eventID,
			},
		}
	}
	// Consume single burst token with an unseen event, then prove unseen triggers 429 while already-seen batch still returns 200 DUPLICATE.
	unseenRec1 := performSPIFFEPost(t, h, InternalSourceObservationBatchPath, map[string]any{
		"observations": []any{makeHTTPItem("p0-batch-cov-1-temp", 2, "0199a000-5000-7000-8000-000000009998")},
	})
	if unseenRec1.Code != http.StatusOK {
		t.Fatalf("first unseen batch status = %d (%s), want 200", unseenRec1.Code, unseenRec1.Body.String())
	}
	unseenRec429 := performSPIFFEPost(t, h, InternalSourceObservationBatchPath, map[string]any{
		"observations": []any{makeHTTPItem("p0-batch-cov-1-temp", 3, "0199a000-5000-7000-8000-000000009997")},
	})
	if unseenRec429.Code != http.StatusTooManyRequests {
		t.Fatalf("exhausted unseen batch status = %d, want 429", unseenRec429.Code)
	}
	seenHTTP := performSPIFFEPost(t, h, InternalSourceObservationBatchPath, map[string]any{
		"observations": []any{
			makeHTTPItem("p0-batch-cov-1-temp", 1, event1A),
			makeHTTPItem("p0-batch-cov-1-hum", 1, event1B),
		},
	})
	if seenHTTP.Code != http.StatusOK {
		t.Fatalf("already-seen :acceptBatch retry status = %d (%s), want 200", seenHTTP.Code, seenHTTP.Body.String())
	}
	var seenResp sourceObservationBatchResponse
	if err := json.Unmarshal(seenHTTP.Body.Bytes(), &seenResp); err != nil {
		t.Fatalf("decode seenHTTP response: %v", err)
	}
	if len(seenResp.Receipts) != 2 || seenResp.Receipts[0].Status != ObservationDuplicate || seenResp.Receipts[1].Status != ObservationDuplicate {
		t.Fatalf("expected DUPLICATE receipts for retry while quota exhausted, got %+v", seenResp.Receipts)
	}
}

func TestPostgresRuntimeEventAndCoverageLoopRespectsADR0002(t *testing.T) {
	store, adminPool, _ := openP0TelemetryPostgresFixture(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 16, 10, 5, 0, 0, time.UTC)

	// Step 1: Connect event makes gateway ONLINE and child coverage available.
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-6000-7000-8000-000000000001", Sequence: 1,
		EventType: "GATEWAY_CONNECTED", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: t0, Severity: "INFO", Data: json.RawMessage(`{}`), ReceivedAt: t0.Add(time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("AcceptRuntimeEvent GATEWAY_CONNECTED: %v", err)
	}

	var childAvailable bool
	var childRev int64
	if err := adminPool.QueryRow(ctx, `
SELECT available, source_revision FROM telemetry_runtime.observation_coverage WHERE device_id = $1::uuid`,
		p0TestDevice1).Scan(&childAvailable, &childRev); err != nil {
		t.Fatalf("read initial coverage: %v", err)
	}
	if !childAvailable {
		t.Fatal("child coverage should be available after GATEWAY_CONNECTED")
	}

	// Idempotent re-report with available=true must NOT bump source_revision or snapshot revision.
	if err := store.ReportSourceCoverage(ctx, SourceCoverageReport{
		SourceID: "connectivity-1", DeviceIDs: []string{p0TestDevice1},
		Available: true, ObservedAt: t0.Add(60 * time.Second), SourceRevision: childRev,
	}); err != nil {
		t.Fatalf("idempotent ReportSourceCoverage: %v", err)
	}
	var afterIdempotentRev int64
	if err := adminPool.QueryRow(ctx, `
SELECT source_revision FROM telemetry_runtime.observation_coverage WHERE device_id = $1::uuid`,
		p0TestDevice1).Scan(&afterIdempotentRev); err != nil {
		t.Fatalf("read coverage after idempotent report: %v", err)
	}
	if afterIdempotentRev != childRev {
		t.Fatalf("idempotent coverage report bumped source_revision from %d to %d", childRev, afterIdempotentRev)
	}

	// Step 2: LWT event marks gateway OFFLINE via presence_signals,
	// while child device MUST become UNAVAILABLE (SOURCE_UNAVAILABLE) with ZERO presence_signals!
	tLWT := t0.Add(90 * time.Second)
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-6000-7000-8000-000000000002", Sequence: 2,
		EventType: "LWT", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: tLWT, Severity: "WARNING", Data: json.RawMessage(`{"reason":"UNEXPECTED_DISCONNECT"}`), ReceivedAt: tLWT.Add(time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("AcceptRuntimeEvent LWT: %v", err)
	}

	var gwPresence string
	if err := adminPool.QueryRow(ctx, `
SELECT current_state FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`,
		p0TestGateway).Scan(&gwPresence); err != nil {
		t.Fatalf("read gateway presence: %v", err)
	}
	if gwPresence != "OFFLINE" {
		t.Fatalf("gateway presence after LWT = %q, want OFFLINE", gwPresence)
	}

	var childPresenceSignalCount int
	if err := adminPool.QueryRow(ctx, `
SELECT count(*) FROM telemetry_runtime.presence_signals WHERE device_id = $1::uuid`,
		p0TestDevice1).Scan(&childPresenceSignalCount); err != nil {
		t.Fatalf("count child presence_signals: %v", err)
	}
	if childPresenceSignalCount != 0 {
		t.Fatalf("child device must NEVER receive presence_signals on gateway LWT, got %d", childPresenceSignalCount)
	}

	var snapAvailability string
	var snapPresence, snapReason *string
	if err := adminPool.QueryRow(ctx, `
SELECT snapshot ->> 'evaluationAvailability',
       snapshot ->> 'currentState',
       snapshot -> 'availabilityReasons' ->> 0
FROM telemetry_runtime.device_observation_snapshots
WHERE device_id = $1::uuid`,
		p0TestDevice1).Scan(&snapAvailability, &snapPresence, &snapReason); err != nil {
		t.Fatalf("read child snapshot after LWT: %v", err)
	}
	if snapAvailability != "UNAVAILABLE" || snapReason == nil || *snapReason != "SOURCE_UNAVAILABLE" {
		t.Fatalf("child snapshot availability=(%q, %v), want (UNAVAILABLE, SOURCE_UNAVAILABLE)", snapAvailability, snapReason)
	}
	if snapPresence != nil && *snapPresence == "OFFLINE" {
		t.Fatalf("child snapshot currentState = %q, must NOT become OFFLINE on gateway LWT", *snapPresence)
	}

	var lwtEventCount int
	if err := adminPool.QueryRow(ctx, `
SELECT count(*) FROM telemetry_runtime.mqtt_runtime_events WHERE gateway_id = $1 AND event_type = 'LWT'`,
		p0TestGateway).Scan(&lwtEventCount); err != nil {
		t.Fatalf("count mqtt_runtime_events: %v", err)
	}
	if lwtEventCount != 1 {
		t.Fatalf("mqtt_runtime_events LWT count = %d, want 1", lwtEventCount)
	}

	// Step 3: Reconnect recovers child coverage and gateway presence.
	tReconnect := t0.Add(150 * time.Second)
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-6000-7000-8000-000000000003", Sequence: 3,
		EventType: "GATEWAY_CONNECTED", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: tReconnect, Severity: "INFO", Data: json.RawMessage(`{}`), ReceivedAt: tReconnect.Add(time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestGateway, p0TestDevice1},
	}); err != nil {
		t.Fatalf("AcceptRuntimeEvent reconnect: %v", err)
	}

	// Step 4a: Duplicate message_id redelivery of old LWT must NOT regress child coverage.
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-6000-7000-8000-000000000002", Sequence: 2,
		EventType: "LWT", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: tLWT, Severity: "WARNING", Data: json.RawMessage(`{"reason":"UNEXPECTED_DISCONNECT"}`), ReceivedAt: tReconnect.Add(2 * time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("duplicate LWT redelivery failed: %v", err)
	}

	// Step 4b: Distinct message_id with stale EventTime (< tReconnect) must NOT regress child coverage.
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-6000-7000-8000-000000000004", Sequence: 2,
		EventType: "LWT", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: tLWT, Severity: "WARNING", Data: json.RawMessage(`{"reason":"DELAYED_LWT"}`), ReceivedAt: tReconnect.Add(3 * time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("stale delayed LWT failed: %v", err)
	}

	// Step 4c: Future-skewed EventTime (> 60s ahead of ReceivedAt) records raw evidence without mutating coverage or presence_signals.
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-6000-7000-8000-000000000005", Sequence: 4,
		EventType: "LWT", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: tReconnect.Add(180 * time.Second), Severity: "WARNING", Data: json.RawMessage(`{"reason":"CLOCK_SKEW"}`), ReceivedAt: tReconnect.Add(4 * time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("future-skewed LWT failed: %v", err)
	}

	if err := adminPool.QueryRow(ctx, `
SELECT available FROM telemetry_runtime.observation_coverage WHERE device_id = $1::uuid`,
		p0TestDevice1).Scan(&childAvailable); err != nil {
		t.Fatalf("read child coverage after guards: %v", err)
	}
	if !childAvailable {
		t.Fatal("child coverage regressed after duplicate/stale/future-skewed LWT events")
	}

	var gwSignalsAfter int
	if err := adminPool.QueryRow(ctx, `
SELECT count(*) FROM telemetry_runtime.presence_signals WHERE device_id = $1::uuid`,
		p0TestGateway).Scan(&gwSignalsAfter); err != nil {
		t.Fatalf("count gateway presence signals: %v", err)
	}
	if gwSignalsAfter != 3 {
		t.Fatalf("gateway presence_signals count = %d, want 3 (duplicate, stale delayed, and future-skewed ignored)", gwSignalsAfter)
	}
	var gwPresenceAfter string
	if err := adminPool.QueryRow(ctx, `
SELECT current_state FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`,
		p0TestGateway).Scan(&gwPresenceAfter); err != nil {
		t.Fatalf("read gateway presence after stale/skewed LWT: %v", err)
	}
	if gwPresenceAfter != "ONLINE" {
		t.Fatalf("gateway current_state = %q, want ONLINE (stale earlier LWT must not regress reconnect)", gwPresenceAfter)
	}
}

func TestPostgresRuntimeEventFailureMode1BacklogReplay(t *testing.T) {
	store, adminPool, _ := openP0TelemetryPostgresFixture(t)
	ctx := context.Background()

	t0 := time.Date(2026, 11, 16, 11, 0, 0, 0, time.UTC)
	tTelemetry := t0.Add(10 * time.Second)
	tReconnect := t0.Add(20 * time.Second)
	tAdapterDrain := t0.Add(60 * time.Second)
	s1 := "0199a000-7000-7000-8000-000000000001"
	s2 := "0199a000-7000-7000-8000-000000000002"

	// Session 1 connects at t0 with matching sessionId s1.
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-7100-7000-8000-000000000001", Sequence: 1,
		EventType: "GATEWAY_CONNECTED", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: t0, Severity: "INFO",
		Data: json.RawMessage(`{"sessionId":"` + s1 + `"}`), ReceivedAt: t0.Add(time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("session 1 connect: %v", err)
	}

	// Telemetry arrives during session 1 at t0 + 10s, updating gateway's latest presence_signal.
	if _, err := adminPool.Exec(ctx, `
INSERT INTO telemetry_runtime.presence_signals (
  signal_id, device_id, signal_type, observed_at, received_at, accepted,
  policy_revision, source_event_id, created_at
) VALUES ($1::uuid, $2::uuid, 'SOURCE_ACTIVITY', $3, $3, true, 1, $4::uuid, $3)`,
		"0199a000-7200-7000-8000-000000000001", p0TestGateway, tTelemetry, "0199a000-7200-7000-8000-000000000002"); err != nil {
		t.Fatalf("seed gateway telemetry presence signal: %v", err)
	}

	// Connectivity adapter restarts at t0 + 60s and drains FIFO backlog:
	// first Session 1 LWT (baked at t0, received at t+60s), then Session 2 GATEWAY_CONNECTED (baked at t0+20s, received at t+61s).
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-7100-7000-8000-000000000002", Sequence: 2,
		EventType: "LWT", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: t0, Severity: "WARNING",
		Data: json.RawMessage(`{"reason":"broker_connection_lost","sessionId":"` + s1 + `"}`), ReceivedAt: tAdapterDrain,
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("drain queued session 1 LWT: %v", err)
	}

	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-7100-7000-8000-000000000003", Sequence: 3,
		EventType: "GATEWAY_CONNECTED", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: tReconnect, Severity: "INFO",
		Data: json.RawMessage(`{"sessionId":"` + s2 + `"}`), ReceivedAt: tAdapterDrain.Add(time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("drain queued session 2 connect: %v", err)
	}

	var gwPresence string
	if err := adminPool.QueryRow(ctx, `
SELECT current_state FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`,
		p0TestGateway).Scan(&gwPresence); err != nil {
		t.Fatalf("read gateway presence: %v", err)
	}
	if gwPresence != "ONLINE" {
		t.Fatalf("gateway current_state after backlog replay = %q, want ONLINE", gwPresence)
	}
	var childAvailable bool
	if err := adminPool.QueryRow(ctx, `
SELECT available FROM telemetry_runtime.observation_coverage WHERE device_id = $1::uuid`,
		p0TestDevice1).Scan(&childAvailable); err != nil {
		t.Fatalf("read child coverage: %v", err)
	}
	if !childAvailable {
		t.Fatal("child observation_coverage.available = false after backlog replay, want true")
	}
}

func TestPostgresRuntimeEventFailureMode2StaleSessionLWTAndOwnSessionLWT(t *testing.T) {
	store, adminPool, _ := openP0TelemetryPostgresFixture(t)
	ctx := context.Background()

	t0 := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s1 := "0199a000-8000-7000-8000-000000000001"
	s2 := "0199a000-8000-7000-8000-000000000002"

	// Session 1 connects at t0 with s1, and sends telemetry at t0 + 5s.
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-8100-7000-8000-000000000001", Sequence: 1,
		EventType: "GATEWAY_CONNECTED", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: t0.Add(time.Millisecond), Severity: "INFO",
		Data: json.RawMessage(`{"sessionId":"` + s1 + `"}`), ReceivedAt: t0.Add(time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("session 1 connect: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
INSERT INTO telemetry_runtime.presence_signals (
  signal_id, device_id, signal_type, observed_at, received_at, accepted,
  policy_revision, source_event_id, created_at
) VALUES ($1::uuid, $2::uuid, 'SOURCE_ACTIVITY', $3, $3, true, 1, $4::uuid, $3)`,
		"0199a000-8200-7000-8000-000000000001", p0TestGateway, t0.Add(5*time.Second), "0199a000-8200-7000-8000-000000000002"); err != nil {
		t.Fatalf("seed session 1 telemetry: %v", err)
	}

	// Session 1's own LWT (baked at t0, matching sessionId s1) MUST take effect even though
	// session 1's own GATEWAY_CONNECTED had t0 + 1ms and telemetry had t0 + 5s.
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-8100-7000-8000-000000000002", Sequence: 2,
		EventType: "LWT", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: t0, Severity: "WARNING",
		Data: json.RawMessage(`{"reason":"broker_connection_lost","sessionId":"` + s1 + `"}`), ReceivedAt: t0.Add(15 * time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("session 1 own LWT: %v", err)
	}
	var gwState string
	if err := adminPool.QueryRow(ctx, `
SELECT current_state FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`,
		p0TestGateway).Scan(&gwState); err != nil {
		t.Fatalf("read gateway state after own LWT: %v", err)
	}
	if gwState != "OFFLINE" {
		t.Fatalf("gateway state after session 1 own LWT = %q, want OFFLINE", gwState)
	}
	var childAvailable bool
	if err := adminPool.QueryRow(ctx, `
SELECT available FROM telemetry_runtime.observation_coverage WHERE device_id = $1::uuid`,
		p0TestDevice1).Scan(&childAvailable); err != nil {
		t.Fatalf("read child availability after own LWT: %v", err)
	}
	if childAvailable {
		t.Fatal("child coverage should be false after session 1 own LWT")
	}

	// Now Session 2 connects at t0 + 20s with s2, and THEN a duplicate/late Session 1 LWT
	// (distinct messageId, EventTime=t0, sessionId=s1, ReceivedAt=t0+40s) arrives out of order.
	tReconnect := t0.Add(20 * time.Second)
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-8100-7000-8000-000000000003", Sequence: 3,
		EventType: "GATEWAY_CONNECTED", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: tReconnect, Severity: "INFO",
		Data: json.RawMessage(`{"sessionId":"` + s2 + `"}`), ReceivedAt: tReconnect.Add(time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("session 2 connect: %v", err)
	}

	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: p0TestGateway,
		MessageID: "0199a000-8100-7000-8000-000000000004", Sequence: 2,
		EventType: "LWT", SourceType: "GATEWAY", SourceID: p0TestGateway,
		EventTime: t0, Severity: "WARNING",
		Data: json.RawMessage(`{"reason":"broker_connection_lost","sessionId":"` + s1 + `"}`), ReceivedAt: t0.Add(40 * time.Second),
		Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestGateway},
		CoverageDeviceIDs: []string{p0TestDevice1},
	}); err != nil {
		t.Fatalf("delayed stale session 1 LWT: %v", err)
	}

	if err := adminPool.QueryRow(ctx, `
SELECT current_state FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`,
		p0TestGateway).Scan(&gwState); err != nil {
		t.Fatalf("read gateway state after stale session 1 LWT: %v", err)
	}
	if gwState != "ONLINE" {
		t.Fatalf("gateway state after stale session 1 LWT = %q, want ONLINE", gwState)
	}
	if err := adminPool.QueryRow(ctx, `
SELECT available FROM telemetry_runtime.observation_coverage WHERE device_id = $1::uuid`,
		p0TestDevice1).Scan(&childAvailable); err != nil {
		t.Fatalf("read child availability after stale session 1 LWT: %v", err)
	}
	if !childAvailable {
		t.Fatal("child coverage regressed after stale session 1 LWT")
	}
}

func TestPostgresCoverageHeartbeatDoesNotOverwriteDisconnectedGateway(t *testing.T) {
	store, adminPool, _ := openP0TelemetryPostgresFixture(t)
	ctx := context.Background()

	const gw2 = "0199a000-2000-7000-8000-000000000002"
	sourceIDs := []string{p0TestGateway, gw2}
	cleanupExtra := func() {
		statements := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = $1::uuid`, []any{gw2}},
			{`DELETE FROM telemetry_runtime.telemetry_history_outbox WHERE payload ->> 'source_id' = ANY($1::text[])`, []any{sourceIDs}},
			{`DELETE FROM telemetry_runtime.source_delivery_evidence WHERE source_id = ANY($1::text[])`, []any{sourceIDs}},
			{`DELETE FROM telemetry_runtime.ingest_quarantine WHERE source_id = ANY($1::text[])`, []any{sourceIDs}},
			{`DELETE FROM telemetry_runtime.presence_signals WHERE device_id = $1::uuid`, []any{gw2}},
			{`DELETE FROM telemetry_runtime.source_observations WHERE source_id = ANY($1::text[])`, []any{sourceIDs}},
			{`DELETE FROM telemetry_runtime.source_positions WHERE source_id = ANY($1::text[])`, []any{sourceIDs}},
			{`DELETE FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = $1::uuid`, []any{gw2}},
			{`DELETE FROM telemetry_runtime.observation_coverage WHERE device_id = $1::uuid`, []any{gw2}},
			{`DELETE FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`, []any{gw2}},
			{`DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`, []any{gw2}},
			{`DELETE FROM telemetry_runtime.mqtt_runtime_events WHERE gateway_id = ANY($1::text[])`, []any{sourceIDs}},
			{`DELETE FROM telemetry_runtime.freshness_policies WHERE device_id = $1::uuid`, []any{gw2}},
			{`DELETE FROM telemetry_runtime.presence_policies WHERE device_id = $1::uuid`, []any{gw2}},
			{`DELETE FROM telemetry_runtime.devices WHERE device_id = $1::uuid`, []any{gw2}},
		}
		for _, stmt := range statements {
			if _, execErr := adminPool.Exec(ctx, stmt.sql, stmt.args...); execErr != nil {
				t.Logf("extra cleanup warning (%s): %v", stmt.sql, execErr)
			}
		}
	}
	cleanupExtra()
	t.Cleanup(cleanupExtra)

	seedTime := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	if _, err := adminPool.Exec(ctx, `
INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4)`,
		gw2, p0TestTenantID, p0TestSiteID, seedTime); err != nil {
		t.Fatalf("seed gw2 device: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
INSERT INTO telemetry_runtime.presence_policies (
  device_id, policy_revision, online_within_seconds, offline_after_seconds,
  coverage_required, accepted_signal_types, max_future_clock_skew_seconds,
  max_source_lag_seconds, updated_at
) VALUES ($1::uuid, 1, 60, 180, true, ARRAY['SOURCE_ACTIVITY', 'EXPLICIT_CONNECT', 'EXPLICIT_DISCONNECT']::text[], 30, 600, $2)`,
		gw2, seedTime); err != nil {
		t.Fatalf("seed gw2 presence policy: %v", err)
	}

	assertCoverageAndSnapshot := func(label, deviceID string, wantAvailable bool, wantReason string) {
		t.Helper()
		var available bool
		var reason *string
		if err := adminPool.QueryRow(ctx, `
SELECT available, reason_code
FROM telemetry_runtime.observation_coverage
WHERE device_id = $1::uuid`,
			deviceID).Scan(&available, &reason); err != nil {
			t.Fatalf("%s: read observation_coverage(%s): %v", label, deviceID, err)
		}
		gotReason := ""
		if reason != nil {
			gotReason = *reason
		}
		if available != wantAvailable || gotReason != wantReason {
			t.Fatalf("%s: coverage(%s) = (available=%v, reason=%q), want (available=%v, reason=%q)",
				label, deviceID, available, gotReason, wantAvailable, wantReason)
		}

		var snapAvailability string
		var snapReason *string
		if err := adminPool.QueryRow(ctx, `
SELECT evaluation_availability, availability_reasons[1]
FROM telemetry_runtime.device_observation_snapshots
WHERE device_id = $1::uuid`,
			deviceID).Scan(&snapAvailability, &snapReason); err != nil {
			t.Fatalf("%s: read snapshot(%s): %v", label, deviceID, err)
		}
		wantSnapAvailability := "UNAVAILABLE"
		if wantAvailable {
			wantSnapAvailability = "AVAILABLE"
		}
		gotSnapReason := ""
		if snapReason != nil {
			gotSnapReason = *snapReason
		}
		if snapAvailability != wantSnapAvailability || gotSnapReason != wantReason {
			t.Fatalf("%s: snapshot(%s) = (%q, %q), want (%q, %q)",
				label, deviceID, snapAvailability, gotSnapReason, wantSnapAvailability, wantReason)
		}
	}

	t0 := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	s1GW1 := "0199a000-9000-7000-8000-000000000001"
	s1GW2 := "0199a000-9000-7000-8000-000000000002"
	s2GW1 := "0199a000-9000-7000-8000-000000000003"
	s3GW1 := "0199a000-9000-7000-8000-000000000004"

	emitGatewayEvent := func(gw, msgID string, seq int64, eventType string, eventTime, receivedAt time.Time, severity, data string, coverageIDs []string) {
		t.Helper()
		if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
			TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: gw,
			MessageID: msgID, Sequence: seq,
			EventType: eventType, SourceType: "GATEWAY", SourceID: gw,
			EventTime: eventTime, Severity: severity,
			Data: json.RawMessage(data), ReceivedAt: receivedAt,
			Device:            &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: gw},
			CoverageDeviceIDs: coverageIDs,
		}); err != nil {
			t.Fatalf("%s (%s): %v", eventType, gw, err)
		}
	}

	emitGatewayEvent(p0TestGateway, "0199a000-9100-7000-8000-000000000001", 1, "GATEWAY_CONNECTED", t0.Add(time.Millisecond), t0.Add(time.Second), "INFO", `{"sessionId":"`+s1GW1+`"}`, []string{p0TestDevice1})
	emitGatewayEvent(gw2, "0199a000-9100-7000-8000-000000000002", 1, "GATEWAY_CONNECTED", t0.Add(time.Millisecond), t0.Add(time.Second), "INFO", `{"sessionId":"`+s1GW2+`"}`, []string{p0TestDevice2})
	assertCoverageAndSnapshot("initial-child1", p0TestDevice1, true, "")
	assertCoverageAndSnapshot("initial-child2", p0TestDevice2, true, "")

	gapReason := "OBSERVATION_COVERAGE_GAP"
	if err := store.ReportSourceCoverage(ctx, SourceCoverageReport{
		SourceID: "connectivity-1", DeviceIDs: []string{p0TestDevice2},
		GatewayScopes: []GatewayCoverageScope{{GatewayID: gw2, DeviceIDs: []string{p0TestDevice2}}},
		Available:     false, ReasonCode: &gapReason, ObservedAt: t0.Add(10 * time.Second), SourceRevision: 2,
	}); err != nil {
		t.Fatalf("set child2 gap: %v", err)
	}
	assertCoverageAndSnapshot("child2-gap", p0TestDevice2, false, "OBSERVATION_COVERAGE_GAP")

	emitGatewayEvent(p0TestGateway, "0199a000-9100-7000-8000-000000000003", 2, "LWT", t0, t0.Add(30*time.Second), "WARNING", `{"reason":"broker_connection_lost","sessionId":"`+s1GW1+`"}`, []string{p0TestDevice1})
	assertCoverageAndSnapshot("child1-after-lwt", p0TestDevice1, false, "SOURCE_UNAVAILABLE")

	if err := store.ReportSourceCoverage(ctx, SourceCoverageReport{
		SourceID:  "connectivity-1",
		DeviceIDs: []string{p0TestDevice1, p0TestDevice2},
		GatewayScopes: []GatewayCoverageScope{
			{GatewayID: p0TestGateway, DeviceIDs: []string{p0TestDevice1}},
			{GatewayID: gw2, DeviceIDs: []string{p0TestDevice2}},
		},
		Available: true, ObservedAt: t0.Add(60 * time.Second), SourceRevision: 10,
	}); err != nil {
		t.Fatalf("mixed heartbeat ReportSourceCoverage: %v", err)
	}
	assertCoverageAndSnapshot("child1-spared-by-heartbeat", p0TestDevice1, false, "SOURCE_UNAVAILABLE")
	assertCoverageAndSnapshot("child2-restored-by-heartbeat", p0TestDevice2, true, "")

	if err := store.ReportSourceCoverage(ctx, SourceCoverageReport{
		SourceID:      "connectivity-1",
		DeviceIDs:     []string{p0TestDevice1},
		GatewayScopes: []GatewayCoverageScope{{GatewayID: p0TestGateway, DeviceIDs: []string{p0TestDevice1}}},
		Available:     true, ObservedAt: t0.Add(65 * time.Second), SourceRevision: 11,
	}); err != nil {
		t.Fatalf("disconnected-only heartbeat: %v", err)
	}
	assertCoverageAndSnapshot("child1-still-unavailable", p0TestDevice1, false, "SOURCE_UNAVAILABLE")

	emitGatewayEvent(p0TestGateway, "0199a000-9100-7000-8000-000000000004", 3, "GATEWAY_CONNECTED", t0.Add(90*time.Second), t0.Add(91*time.Second), "INFO", `{"sessionId":"`+s2GW1+`"}`, nil)
	if err := store.ReportSourceCoverage(ctx, SourceCoverageReport{
		SourceID:  "connectivity-1",
		DeviceIDs: []string{p0TestDevice1, p0TestDevice2},
		GatewayScopes: []GatewayCoverageScope{
			{GatewayID: p0TestGateway, DeviceIDs: []string{p0TestDevice1}},
			{GatewayID: gw2, DeviceIDs: []string{p0TestDevice2}},
		},
		Available: true, ObservedAt: t0.Add(95 * time.Second), SourceRevision: 12,
	}); err != nil {
		t.Fatalf("reconnect recovery heartbeat: %v", err)
	}
	assertCoverageAndSnapshot("child1-recovered-after-reconnect", p0TestDevice1, true, "")

	emitGatewayEvent(p0TestGateway, "0199a000-9100-7000-8000-000000000005", 4, "LWT", t0.Add(90*time.Second), t0.Add(110*time.Second), "WARNING", `{"reason":"broker_connection_lost","sessionId":"`+s2GW1+`"}`, []string{p0TestDevice1})
	assertCoverageAndSnapshot("child1-after-second-lwt", p0TestDevice1, false, "SOURCE_UNAVAILABLE")

	cel := "Cel"
	receipts, err := store.AcceptObservationBatch(ctx, []ObservationCandidate{{
		SourceID: p0TestGateway, SourcePath: SourcePathPush,
		Device:             &ResolvedDevice{TenantID: p0TestTenantID, SiteID: p0TestSiteID, DeviceID: p0TestDevice1},
		Point:              &ResolvedPoint{PointID: p0TestPoint1A, PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &cel, PointRevision: 1},
		ExternalEntityType: "DEVICE", ExternalID: "DEV-1", TelemetryKey: "zone.temperature",
		Value: json.RawMessage(`21.5`), ValueType: "NUMBER", Unit: &cel,
		SampledAt: t0.Add(120 * time.Second), ReceivedAt: t0.Add(121 * time.Second),
		Position: SourcePosition{Partition: "p0-cov-live-uplink", Offset: 1, EventID: "0199a000-9200-7000-8000-000000000001"},
	}})
	if err != nil || len(receipts) != 1 || receipts[0].Status != ObservationAccepted {
		t.Fatalf("live uplink receipts=(%+v, err=%v), want 1 ACCEPTED", receipts, err)
	}
	if err := store.ReportSourceCoverage(ctx, SourceCoverageReport{
		SourceID:  "connectivity-1",
		DeviceIDs: []string{p0TestDevice1, p0TestDevice2},
		GatewayScopes: []GatewayCoverageScope{
			{GatewayID: p0TestGateway, DeviceIDs: []string{p0TestDevice1}},
			{GatewayID: gw2, DeviceIDs: []string{p0TestDevice2}},
		},
		Available: true, ObservedAt: t0.Add(125 * time.Second), SourceRevision: 15,
	}); err != nil {
		t.Fatalf("uplink recovery heartbeat: %v", err)
	}
	assertCoverageAndSnapshot("child1-recovered-after-uplink", p0TestDevice1, true, "")

	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: p0TestTenantID, SiteID: p0TestSiteID, GatewayID: gw2,
		MessageID: "0199a000-9100-7000-8000-000000000006", Sequence: 2,
		EventType: "DEVICE_DISCONNECTED", SourceType: "DEVICE", SourceID: p0TestDevice2,
		EventTime: t0.Add(130 * time.Second), Severity: "WARNING",
		Data: json.RawMessage(`{"sessionId":"` + s3GW1 + `"}`), ReceivedAt: t0.Add(131 * time.Second),
	}); err != nil {
		t.Fatalf("child DEVICE_DISCONNECTED: %v", err)
	}
	if err := store.ReportSourceCoverage(ctx, SourceCoverageReport{
		SourceID:      "connectivity-1",
		DeviceIDs:     []string{p0TestDevice2},
		GatewayScopes: []GatewayCoverageScope{{GatewayID: gw2, DeviceIDs: []string{p0TestDevice2}}},
		Available:     false, ReasonCode: &gapReason, ObservedAt: t0.Add(132 * time.Second), SourceRevision: 16,
	}); err != nil {
		t.Fatalf("set child2 gap after child disconnect: %v", err)
	}
	if err := store.ReportSourceCoverage(ctx, SourceCoverageReport{
		SourceID:      "connectivity-1",
		DeviceIDs:     []string{p0TestDevice2},
		GatewayScopes: []GatewayCoverageScope{{GatewayID: gw2, DeviceIDs: []string{p0TestDevice2}}},
		Available:     true, ObservedAt: t0.Add(135 * time.Second), SourceRevision: 17,
	}); err != nil {
		t.Fatalf("recover child2 with child disconnect present: %v", err)
	}
	assertCoverageAndSnapshot("child2-unaffected-by-device-disconnect", p0TestDevice2, true, "")
}
