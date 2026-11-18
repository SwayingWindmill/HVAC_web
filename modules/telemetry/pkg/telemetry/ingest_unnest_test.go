package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func testLatestOp(deviceID, key string, replaceLatest bool, sampledAt, receivedAt time.Time, value string, healthStatus string, score int) latestUpdateOp {
	unit := "Cel"
	var measurement []byte
	if replaceLatest {
		measurement = []byte(`{"relative":0.01,"low":23.8,"high":24.2,"lowConfidence":false}`)
	}
	return latestUpdateOp{
		replaceLatest: replaceLatest,
		candidate: ObservationCandidate{
			TelemetryKey: key,
			Value:        json.RawMessage(value),
			ValueType:    "NUMBER",
			Unit:         &unit,
			SampledAt:    sampledAt,
			ReceivedAt:   receivedAt,
		},
		decision: ObservationDecision{
			DeviceID:       deviceID,
			Quality:        QualityGood,
			QualityReasons: []QualityReason{QualityReasonSourceLagExceeded},
			PolicyRevision: 5,
			UnchangedSince: sampledAt.Add(-time.Minute),
			HealthStatus:   healthStatus,
			HealthScore:    score,
			HealthReasons:  []QualityReason{QualityReasonOutOfRange},
			Measurement:    measurement,
		},
	}
}

func TestDeduplicateLatestUpdates(t *testing.T) {
	base := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	dev1 := "018f2e00-3000-7000-8000-000000000001"
	dev2 := "018f2e00-3000-7000-8000-000000000002"

	t.Run("empty input", func(t *testing.T) {
		upserts, healthUpdates := deduplicateLatestUpdates(nil)
		if len(upserts) != 0 || len(healthUpdates) != 0 {
			t.Fatalf("got upserts=%d healthUpdates=%d, want 0/0", len(upserts), len(healthUpdates))
		}
	})

	t.Run("picks greatest sampled_at or later index on tie and sorts deterministically", func(t *testing.T) {
		ops := []latestUpdateOp{
			// dev2 arrives before dev1 to verify sorting by (device_id, telemetry_key).
			testLatestOp(dev2, "zone.temperature", true, base.Add(2*time.Second), base.Add(2*time.Second), `25.0`, HealthHealthy, 100),
			// dev1 zone.temperature: earlier timestamp arrives AFTER greater timestamp -> greater timestamp wins.
			testLatestOp(dev1, "zone.temperature", true, base.Add(10*time.Second), base.Add(10*time.Second), `23.5`, HealthHealthy, 100),
			testLatestOp(dev1, "zone.temperature", true, base.Add(5*time.Second), base.Add(11*time.Second), `21.0`, HealthHealthy, 100),
			// dev1 duct.pressure: tie on sampled_at -> later index wins.
			testLatestOp(dev1, "duct.pressure", true, base.Add(4*time.Second), base.Add(4*time.Second), `100.0`, HealthHealthy, 100),
			testLatestOp(dev1, "duct.pressure", true, base.Add(4*time.Second), base.Add(5*time.Second), `105.0`, HealthHealthy, 100),
			// dev1 duct.pressure subsequent health rejection -> preserved in healthUpdates.
			testLatestOp(dev1, "duct.pressure", false, base.Add(6*time.Second), base.Add(6*time.Second), `999.0`, HealthBad, 0),
			// dev2 zone.humidity health updates: tie on sampled_at -> later index wins.
			testLatestOp(dev2, "zone.humidity", false, base.Add(3*time.Second), base.Add(3*time.Second), `500.0`, HealthBad, 10),
			testLatestOp(dev2, "zone.humidity", false, base.Add(3*time.Second), base.Add(4*time.Second), `600.0`, HealthBad, 0),
		}

		upserts, healthUpdates := deduplicateLatestUpdates(ops)
		if len(upserts) != 3 {
			t.Fatalf("upserts=%d, want 3", len(upserts))
		}
		if upserts[0].decision.DeviceID != dev1 || upserts[0].candidate.TelemetryKey != "duct.pressure" || string(upserts[0].candidate.Value) != `105.0` {
			t.Fatalf("upserts[0]=%+v", upserts[0])
		}
		if upserts[1].decision.DeviceID != dev1 || upserts[1].candidate.TelemetryKey != "zone.temperature" || string(upserts[1].candidate.Value) != `23.5` {
			t.Fatalf("upserts[1]=%+v", upserts[1])
		}
		if upserts[2].decision.DeviceID != dev2 || upserts[2].candidate.TelemetryKey != "zone.temperature" || string(upserts[2].candidate.Value) != `25.0` {
			t.Fatalf("upserts[2]=%+v", upserts[2])
		}

		if len(healthUpdates) != 2 {
			t.Fatalf("healthUpdates=%d, want 2", len(healthUpdates))
		}
		if healthUpdates[0].decision.DeviceID != dev1 || healthUpdates[0].candidate.TelemetryKey != "duct.pressure" {
			t.Fatalf("healthUpdates[0]=%+v", healthUpdates[0])
		}
		if healthUpdates[1].decision.DeviceID != dev2 || healthUpdates[1].candidate.TelemetryKey != "zone.humidity" || healthUpdates[1].decision.HealthScore != 0 {
			t.Fatalf("healthUpdates[1]=%+v", healthUpdates[1])
		}
	})

	t.Run("subsequent upsert supersedes earlier health-only op for same point", func(t *testing.T) {
		ops := []latestUpdateOp{
			testLatestOp(dev1, "zone.temperature", false, base.Add(time.Second), base.Add(time.Second), `500.0`, HealthBad, 0),
			testLatestOp(dev1, "zone.temperature", true, base.Add(2*time.Second), base.Add(2*time.Second), `24.0`, HealthHealthy, 100),
		}
		upserts, healthUpdates := deduplicateLatestUpdates(ops)
		if len(upserts) != 1 || len(healthUpdates) != 0 {
			t.Fatalf("got upserts=%d healthUpdates=%d, want 1/0", len(upserts), len(healthUpdates))
		}
	})

	t.Run("subsequent older-sampled accepted op preserves newer health rejection", func(t *testing.T) {
		ops := []latestUpdateOp{
			testLatestOp(dev1, "zone.temperature", false, base.Add(10*time.Second), base.Add(10*time.Second), `500.0`, HealthBad, 0),
			testLatestOp(dev1, "zone.temperature", true, base.Add(5*time.Second), base.Add(11*time.Second), `22.5`, HealthHealthy, 100),
		}
		upserts, healthUpdates := deduplicateLatestUpdates(ops)
		if len(upserts) != 1 || len(healthUpdates) != 1 {
			t.Fatalf("got upserts=%d healthUpdates=%d, want 1/1", len(upserts), len(healthUpdates))
		}
		if healthUpdates[0].decision.HealthStatus != HealthBad {
			t.Fatalf("healthUpdates[0]=%+v, want BAD", healthUpdates[0])
		}
	})
}

func TestPersistBatchWritePlanLatestUpdatesUsesSingleUNNESTQueries(t *testing.T) {
	base := time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC)
	dev1 := "018f2e00-3000-7000-8000-000000000001"
	dev2 := "018f2e00-3000-7000-8000-000000000002"

	opWithNulls := latestUpdateOp{
		replaceLatest: true,
		candidate: ObservationCandidate{
			TelemetryKey: "zone.status",
			Value:        json.RawMessage(`"RUNNING"`),
			ValueType:    "STRING",
			Unit:         nil,
			SampledAt:    base,
			ReceivedAt:   base,
		},
		decision: ObservationDecision{
			DeviceID:       dev1,
			Quality:        QualityGood,
			QualityReasons: nil,
			PolicyRevision: 2,
			HealthStatus:   HealthHealthy,
			HealthScore:    100,
			HealthReasons:  nil,
			Measurement:    nil,
		},
	}

	plan := &batchWritePlan{
		latestUpdates: []latestUpdateOp{
			testLatestOp(dev1, "zone.temperature", true, base, base, `22.0`, HealthHealthy, 100),
			testLatestOp(dev1, "zone.temperature", true, base.Add(time.Second), base.Add(time.Second), `23.0`, HealthHealthy, 100),
			testLatestOp(dev1, "zone.temperature", false, base.Add(2*time.Second), base.Add(2*time.Second), `500.0`, HealthBad, 0),
			opWithNulls,
			testLatestOp(dev2, "duct.pressure", true, base, base, `150.0`, HealthHealthy, 100),
			testLatestOp(dev2, "zone.humidity", false, base, base, `999.0`, HealthBad, 0),
		},
	}

	batch := buildBatchWritePlan(plan, nil)
	if batch.Len() != 2 {
		t.Fatalf("batch.Len()=%d, want 2 (one UNNEST upsert and one UNNEST health update)", batch.Len())
	}

	upsertQuery := batch.QueuedQueries[0]
	if !strings.Contains(upsertQuery.SQL, "FROM unnest(") ||
		!strings.Contains(upsertQuery.SQL, "lat.sampled_at <= EXCLUDED.sampled_at") ||
		!strings.Contains(upsertQuery.SQL, "IS DISTINCT FROM") {
		t.Fatalf("unexpected upsert SQL: %s", upsertQuery.SQL)
	}
	if len(upsertQuery.Arguments) != 15 {
		t.Fatalf("upsert args=%d, want 15", len(upsertQuery.Arguments))
	}
	assertPgxArrayArgEncodes(t, upsertQuery.Arguments, []uint32{
		pgtype.UUIDArrayOID, pgtype.TextArrayOID, pgtype.TextArrayOID, pgtype.TextArrayOID, pgtype.TextArrayOID,
		pgtype.TimestamptzArrayOID, pgtype.TimestamptzArrayOID, pgtype.TextArrayOID, pgtype.TextArrayOID, pgtype.Int8ArrayOID,
		pgtype.TimestamptzArrayOID, pgtype.TextArrayOID, pgtype.Int2ArrayOID, pgtype.TextArrayOID, pgtype.TextArrayOID,
	}, 3)

	healthQuery := batch.QueuedQueries[1]
	if !strings.Contains(healthQuery.SQL, "FROM unnest(") ||
		!strings.Contains(healthQuery.SQL, "lat.health_at IS NULL OR lat.health_at <= upd.health_at") ||
		!strings.Contains(healthQuery.SQL, "IS DISTINCT FROM") {
		t.Fatalf("unexpected health SQL: %s", healthQuery.SQL)
	}
	if len(healthQuery.Arguments) != 6 {
		t.Fatalf("health args=%d, want 6", len(healthQuery.Arguments))
	}
	assertPgxArrayArgEncodes(t, healthQuery.Arguments, []uint32{
		pgtype.UUIDArrayOID, pgtype.TextArrayOID, pgtype.TextArrayOID,
		pgtype.Int2ArrayOID, pgtype.TextArrayOID, pgtype.TimestamptzArrayOID,
	}, 2)
}

func TestQueueLatestBatchNoOpsWhenEmpty(t *testing.T) {
	batch := &pgx.Batch{}
	queueUpsertLatestObservationsBatch(batch, nil)
	queueUpdatePointHealthBatch(batch, nil)
	if batch.Len() != 0 {
		t.Fatalf("batch.Len()=%d, want 0", batch.Len())
	}
}

func assertPgxArrayArgEncodes(t *testing.T, args []any, oids []uint32, wantRows int) {
	t.Helper()
	if len(args) != len(oids) {
		t.Fatalf("len(args)=%d != len(oids)=%d", len(args), len(oids))
	}
	typeMap := pgtype.NewMap()
	eqb := &pgx.ExtendedQueryBuilder{}
	if err := eqb.Build(typeMap, &pgconn.StatementDescription{ParamOIDs: oids}, args); err != nil {
		t.Fatalf("ExtendedQueryBuilder.Build with ParamOIDs failed: %v", err)
	}
	if len(eqb.ParamValues) != len(args) {
		t.Fatalf("encoded params=%d, want %d", len(eqb.ParamValues), len(args))
	}
	for index, arg := range args {
		if _, err := typeMap.Encode(oids[index], pgtype.TextFormatCode, arg, nil); err != nil {
			t.Fatalf("arg[%d] oid=%d text encode failed: %v", index, oids[index], err)
		}
		if oids[index] != pgtype.UUIDArrayOID {
			if _, err := typeMap.Encode(oids[index], pgtype.BinaryFormatCode, arg, nil); err != nil {
				t.Fatalf("arg[%d] oid=%d binary encode failed: %v", index, oids[index], err)
			}
		}
	}
	if deviceIDs, ok := args[0].([]string); !ok || len(deviceIDs) != wantRows {
		t.Fatalf("args[0]=%#v, want %d deviceIDs", args[0], wantRows)
	}
}
