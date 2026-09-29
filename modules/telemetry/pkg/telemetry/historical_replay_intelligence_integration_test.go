package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	intelligenceTenantID      = "018f1d00-0000-7000-8000-000000000001"
	intelligenceSiteID        = "018f1e00-1000-7000-8000-000000000001"
	intelligenceDeviceID      = "018f1e00-4000-7000-8000-000000000001"
	intelligenceIntegrationID = "018f1e00-6100-7000-8000-000000000001"
	intelligenceDatasetID     = "01990000-3480-7000-8000-000000000001"
	intelligenceExternalID    = "edge-device-owner-a-1"
	intelligenceLoadPointID   = "01990000-3481-7000-8000-000000000001"
	intelligenceEnergyPointID = "01990000-3481-7000-8000-000000000002"
)

func TestHistoricalReplayIntelligenceAcceptancePublishesEventTimeHistory(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	historyURL := os.Getenv("S2_TELEMETRY_HISTORY_DATABASE_URL")
	clickHouseURL := os.Getenv("S2_CLICKHOUSE_HTTP_URL")
	if historyURL == "" || clickHouseURL == "" {
		t.Skip("Historical Replay intelligence acceptance environment is not configured")
	}
	ctx := t.Context()
	adminConfig, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	adminConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	seedHistoricalReplayIntelligenceBindings(t, admin)

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	currentAt := time.Date(2026, 8, 28, 11, 20, 0, 0, time.UTC)
	for _, candidate := range historicalReplayIntelligenceCurrentObservations(currentAt) {
		receipt, acceptErr := store.AcceptObservation(ctx, candidate)
		if acceptErr != nil {
			t.Fatal(acceptErr)
		}
		if receipt.Status != ObservationAccepted || !receipt.StateChanged || receipt.BusinessRevision == 0 {
			t.Fatalf("current Telemetry owner receipt=%#v", receipt)
		}
	}
	before := currentMutationCounts(t, admin)
	receivedAt := time.Date(2026, 8, 28, 11, 30, 0, 0, time.UTC)
	requests := historicalReplayIntelligenceRequests()
	for _, input := range requests {
		candidate, normalizeErr := normalizeHistoricalReplayObservation(input, receivedAt)
		if normalizeErr != nil {
			t.Fatal(normalizeErr)
		}
		receipt, acceptErr := store.AcceptHistoricalObservation(ctx, candidate)
		if acceptErr != nil {
			t.Fatal(acceptErr)
		}
		if receipt.Status != ObservationAccepted && receipt.Status != ObservationOutOfOrder {
			t.Fatalf("Historical Replay receipt=%#v", receipt)
		}
		if receipt.Quality != QualityGood || receipt.BusinessRevision != 0 || receipt.StateChanged {
			t.Fatalf("Historical Replay changed Current truth: %#v", receipt)
		}
	}

	// A restart/retry reuses the stable dataset/device/offset identity. ReceivedAt is
	// truthful for the retry attempt, while canonical history remains exactly once.
	for _, input := range requests {
		candidate, normalizeErr := normalizeHistoricalReplayObservation(input, receivedAt.Add(time.Minute))
		if normalizeErr != nil {
			t.Fatal(normalizeErr)
		}
		receipt, acceptErr := store.AcceptHistoricalObservation(ctx, candidate)
		if acceptErr != nil {
			t.Fatal(acceptErr)
		}
		if receipt.Status != ObservationDuplicate {
			t.Fatalf("Historical Replay retry receipt=%#v", receipt)
		}
	}

	repository, err := OpenHistoryPostgresRepository(ctx, historyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: clickHouseURL, Database: "telemetry_history", Table: "observations",
		Username: os.Getenv("S2_CLICKHOUSE_USERNAME"), Password: os.Getenv("S2_CLICKHOUSE_PASSWORD"),
	})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := NewHistoryRelay(HistoryRelayConfig{
		Repository: repository, Sink: sink, BatchSize: 32,
		LeaseFor: 30 * time.Second, RetryAfter: time.Second, MaxAttempts: 4,
		Now: func() time.Time { return receivedAt.Add(2 * time.Minute) },
	})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := relay.RelayOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if projected != len(requests)+2 {
		t.Fatalf("Telemetry history projected=%d want=%d", projected, len(requests)+2)
	}
	if projectedAgain, relayErr := relay.RelayOnce(ctx); relayErr != nil || projectedAgain != 0 {
		t.Fatalf("Historical Replay second projection=%d err=%v", projectedAgain, relayErr)
	}

	partition := historicalReplayPartition(t)
	actual := clickHouseQuery(t, clickHouseURL, fmt.Sprintf(`
SELECT toString(count()) || '|' || toString(countIf(source_path='HISTORY_REPLAY')) || '|' || toString(countIf(received_at > sampled_at)) || '|' || toString(countIf(acceptance_status='OUT_OF_ORDER'))
FROM telemetry_history.observations
WHERE tenant_id=toUUID('%s') AND site_id=toUUID('%s') AND device_id=toUUID('%s') AND source_partition='%s'
FORMAT TSVRaw
`, intelligenceTenantID, intelligenceSiteID, intelligenceDeviceID, partition))
	if actual != "10|10|10|0" {
		t.Fatalf("Historical Replay history=%q", actual)
	}

	after := currentMutationCounts(t, admin)
	if after != before {
		t.Fatalf("Historical Replay mutated Current counts before=%q after=%q", before, after)
	}

	// Replay owns only Telemetry history. Before downstream owners execute, their
	// stores must still contain no facts for this acceptance lineage.
	downstream := clickHouseQuery(t, clickHouseURL, fmt.Sprintf(`
SELECT
  (SELECT count() FROM analytics.energy_interval_facts WHERE tenant_id=toUUID('%s') AND site_id=toUUID('%s')) || '|' ||
  (SELECT count() FROM analytics.metric_result_facts WHERE tenant_id=toUUID('%s') AND site_id=toUUID('%s')) || '|' ||
  (SELECT count() FROM analytics.forecast_series WHERE tenant_id=toUUID('%s') AND site_id=toUUID('%s'))
FORMAT TSVRaw
`, intelligenceTenantID, intelligenceSiteID, intelligenceTenantID, intelligenceSiteID, intelligenceTenantID, intelligenceSiteID))
	if downstream != "0|0|0" {
		t.Fatalf("Historical Replay wrote downstream owner stores: %s", downstream)
	}
}

func historicalReplayIntelligenceRequests() []historicalReplayObservationRequest {
	unitKW, unitKWh := "kW", "kWh"
	load := func(offset int64, at string, value float64) historicalReplayObservationRequest {
		return historicalReplayObservationRequest{
			IntegrationInstanceID: intelligenceIntegrationID, ReplayDatasetID: intelligenceDatasetID,
			DeviceExternalID: intelligenceExternalID, TelemetryKey: "site.load_kw",
			Value: json.RawMessage(fmt.Sprintf("%g", value)), ValueType: "NUMBER", Unit: &unitKW,
			SampledAt: at, Offset: offset,
		}
	}
	energy := func(offset int64, at string, value float64) historicalReplayObservationRequest {
		return historicalReplayObservationRequest{
			IntegrationInstanceID: intelligenceIntegrationID, ReplayDatasetID: intelligenceDatasetID,
			DeviceExternalID: intelligenceExternalID, TelemetryKey: "grid.import_energy_total",
			Value: json.RawMessage(fmt.Sprintf("%g", value)), ValueType: "NUMBER", Unit: &unitKWh,
			SampledAt: at, Offset: offset,
		}
	}
	return []historicalReplayObservationRequest{
		energy(0, "2026-08-28T10:00:00Z", 100),
		load(1, "2026-08-28T10:05:00Z", 760),
		energy(2, "2026-08-28T10:15:00Z", 110),
		load(3, "2026-08-28T10:20:00Z", 780),
		energy(4, "2026-08-28T10:30:00Z", 125),
		load(5, "2026-08-28T10:35:00Z", 800),
		energy(6, "2026-08-28T10:45:00Z", 140),
		load(7, "2026-08-28T10:50:00Z", 820),
		energy(8, "2026-08-28T11:00:00Z", 160),
		// Delivered last on purpose. Event-time consumers must still place it in
		// the 10:15-10:30 interval rather than treating import order as event time.
		load(9, "2026-08-28T10:25:00Z", 800),
	}
}

func historicalReplayIntelligenceCurrentObservations(at time.Time) []ObservationCandidate {
	unit := "Cel"
	return []ObservationCandidate{
		{
			IntegrationInstanceID: intelligenceIntegrationID, SourcePath: SourcePathPoll,
			ExternalEntityType: "DEVICE", ExternalID: intelligenceExternalID,
			TelemetryKey: "btu_meter.supply_water_temperature", Value: json.RawMessage(`7`), ValueType: "NUMBER", Unit: &unit,
			SampledAt: at, ReceivedAt: at.Add(time.Second),
			Position: SourcePosition{Partition: "issue-348-current", Offset: 1, EventID: "01990000-3485-7000-8000-000000000001"},
		},
		{
			IntegrationInstanceID: intelligenceIntegrationID, SourcePath: SourcePathPoll,
			ExternalEntityType: "DEVICE", ExternalID: intelligenceExternalID,
			TelemetryKey: "zone.temperature", Value: json.RawMessage(`23`), ValueType: "NUMBER", Unit: &unit,
			SampledAt: at.Add(2 * time.Second), ReceivedAt: at.Add(3 * time.Second),
			Position: SourcePosition{Partition: "issue-348-current", Offset: 2, EventID: "01990000-3485-7000-8000-000000000002"},
		},
	}
}

func seedHistoricalReplayIntelligenceBindings(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	if _, err := admin.Exec(ctx, `
DELETE FROM telemetry_runtime.telemetry_history_outbox WHERE payload ->> 'device_id' = $1;
DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id=$1::uuid;
DELETE FROM telemetry_runtime.presence_signals WHERE device_id=$1::uuid;
DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id=$1::uuid;
DELETE FROM telemetry_runtime.device_presence WHERE device_id=$1::uuid;
DELETE FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id=$1::uuid;
DELETE FROM telemetry_runtime.source_observations WHERE device_id=$1::uuid;
DELETE FROM telemetry_runtime.source_positions WHERE integration_instance_id=$2::uuid;
DELETE FROM telemetry_runtime.freshness_policies WHERE device_id=$1::uuid;
DELETE FROM telemetry_runtime.presence_policies WHERE device_id=$1::uuid;
DELETE FROM telemetry_runtime.registry_point_bindings WHERE device_id=$1::uuid;
DELETE FROM telemetry_runtime.registry_device_bindings WHERE device_id=$1::uuid;
INSERT INTO telemetry_runtime.registry_device_bindings (
  tenant_id,device_id,site_id,integration_instance_id,external_entity_type,external_id,
  binding_status,binding_revision,source_registry_revision,valid_from,valid_to,updated_at
) VALUES ($3::uuid,$1::uuid,$4::uuid,$2::uuid,'DEVICE',$5,'ACTIVE',1,1,'2026-08-01T00:00:00Z',NULL,'2026-08-28T00:00:00Z');
INSERT INTO telemetry_runtime.registry_point_bindings (
  projection_id,tenant_id,site_id,point_id,sensor_id,device_id,telemetry_key,point_type,value_type,unit,binding_status,
  point_revision,source_registry_revision,valid_from,valid_to,updated_at,counter_decrease_mode,counter_rollover_modulus
) VALUES
  ('01990000-3482-7000-8000-000000000001',$3::uuid,$4::uuid,$6::uuid,NULL,$1::uuid,'site.load_kw','TELEMETRY','NUMBER','kW','ACTIVE',1,1,'2026-08-01T00:00:00Z',NULL,'2026-08-28T00:00:00Z',NULL,NULL),
  ('01990000-3482-7000-8000-000000000002',$3::uuid,$4::uuid,$7::uuid,NULL,$1::uuid,'grid.import_energy_total','COUNTER','NUMBER','kWh','ACTIVE',1,1,'2026-08-01T00:00:00Z',NULL,'2026-08-28T00:00:00Z','RESET_TO_ZERO',NULL),
  ('01990000-3482-7000-8000-000000000003',$3::uuid,$4::uuid,'01990000-3481-7000-8000-000000000003',NULL,$1::uuid,'btu_meter.supply_water_temperature','TELEMETRY','NUMBER','Cel','ACTIVE',1,1,'2026-08-01T00:00:00Z',NULL,'2026-08-28T00:00:00Z',NULL,NULL),
  ('01990000-3482-7000-8000-000000000004',$3::uuid,$4::uuid,'01990000-3481-7000-8000-000000000004',NULL,$1::uuid,'zone.temperature','TELEMETRY','NUMBER','Cel','ACTIVE',1,1,'2026-08-01T00:00:00Z',NULL,'2026-08-28T00:00:00Z',NULL,NULL);
INSERT INTO telemetry_runtime.presence_policies (
  device_id,policy_revision,online_within_seconds,offline_after_seconds,coverage_required,
  accepted_signal_types,max_future_clock_skew_seconds,max_source_lag_seconds,updated_at
) VALUES ($1::uuid,1,60,180,true,ARRAY['SOURCE_ACTIVITY']::text[],30,600,'2026-08-28T00:00:00Z');
INSERT INTO telemetry_runtime.freshness_policies (
  device_id,telemetry_key,policy_revision,fresh_within_seconds,configured,expected_sample_interval_seconds,value_type,expected_unit,minimum_number,maximum_number,updated_at
) VALUES
  ($1::uuid,'site.load_kw',1,900,true,900,'NUMBER','kW',0,100000,'2026-08-28T00:00:00Z'),
  ($1::uuid,'grid.import_energy_total',1,900,true,900,'NUMBER','kWh',0,NULL,'2026-08-28T00:00:00Z'),
  ($1::uuid,'btu_meter.supply_water_temperature',1,300,true,60,'NUMBER','Cel',0,30,'2026-08-28T00:00:00Z'),
  ($1::uuid,'zone.temperature',1,300,true,60,'NUMBER','Cel',-20,60,'2026-08-28T00:00:00Z');
`, intelligenceDeviceID, intelligenceIntegrationID, intelligenceTenantID, intelligenceSiteID, intelligenceExternalID, intelligenceLoadPointID, intelligenceEnergyPointID); err != nil {
		t.Fatal(err)
	}
}

func historicalReplayPartition(t *testing.T) string {
	t.Helper()
	candidate, err := normalizeHistoricalReplayObservation(historicalReplayIntelligenceRequests()[0], time.Date(2026, 8, 28, 11, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return candidate.Position.Partition
}

func currentMutationCounts(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	var counts string
	if err := admin.QueryRow(t.Context(), `
SELECT
  (SELECT count(*) FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id=$1::uuid)::text || '|' ||
  (SELECT count(*) FROM telemetry_runtime.device_observation_snapshots WHERE device_id=$1::uuid)::text || '|' ||
  (SELECT count(*) FROM telemetry_runtime.device_presence WHERE device_id=$1::uuid)::text || '|' ||
  (SELECT count(*) FROM telemetry_runtime.presence_signals WHERE device_id=$1::uuid)::text
`, intelligenceDeviceID).Scan(&counts); err != nil {
		t.Fatal(err)
	}
	return counts
}
