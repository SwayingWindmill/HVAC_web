package telemetry

import (
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

func TestDefaultPostgresPoolConfig(t *testing.T) {
	cfg := DefaultPostgresPoolConfig()
	if cfg.MaxConns != 24 {
		t.Fatalf("MaxConns=%d, want 24", cfg.MaxConns)
	}
	if cfg.MinConns != 4 {
		t.Fatalf("MinConns=%d, want 4", cfg.MinConns)
	}
	if cfg.MaxConnLifetime != 30*time.Minute {
		t.Fatalf("MaxConnLifetime=%s, want 30m", cfg.MaxConnLifetime)
	}
	if cfg.MaxConnIdleTime != 5*time.Minute {
		t.Fatalf("MaxConnIdleTime=%s, want 5m", cfg.MaxConnIdleTime)
	}
	if cfg.HealthCheckPeriod != 30*time.Second {
		t.Fatalf("HealthCheckPeriod=%s, want 30s", cfg.HealthCheckPeriod)
	}
}

func TestNormalizePostgresPoolConfig(t *testing.T) {
	t.Run("zero struct receives defaults", func(t *testing.T) {
		got, err := normalizePostgresPoolConfig(PostgresPoolConfig{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != DefaultPostgresPoolConfig() {
			t.Fatalf("got=%+v, want=%+v", got, DefaultPostgresPoolConfig())
		}
	})

	t.Run("small MaxConns clamps defaulted MinConns", func(t *testing.T) {
		got, err := normalizePostgresPoolConfig(PostgresPoolConfig{MaxConns: 2})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.MaxConns != 2 || got.MinConns != 2 {
			t.Fatalf("MaxConns=%d MinConns=%d, want 2/2", got.MaxConns, got.MinConns)
		}
		if got.MaxConnLifetime != 30*time.Minute || got.MaxConnIdleTime != 5*time.Minute || got.HealthCheckPeriod != 30*time.Second {
			t.Fatalf("unexpected duration defaults: %+v", got)
		}
	})

	t.Run("explicit valid settings preserved", func(t *testing.T) {
		want := PostgresPoolConfig{
			MaxConns:          48,
			MinConns:          8,
			MaxConnLifetime:   45 * time.Minute,
			MaxConnIdleTime:   10 * time.Minute,
			HealthCheckPeriod: 15 * time.Second,
		}
		got, err := normalizePostgresPoolConfig(want)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != want {
			t.Fatalf("got=%+v, want=%+v", got, want)
		}
	})

	t.Run("rejects negative settings and min exceeding max", func(t *testing.T) {
		invalid := []struct {
			name string
			cfg  PostgresPoolConfig
		}{
			{name: "negative MaxConns", cfg: PostgresPoolConfig{MaxConns: -1}},
			{name: "negative MinConns", cfg: PostgresPoolConfig{MinConns: -1}},
			{name: "negative MaxConnLifetime", cfg: PostgresPoolConfig{MaxConnLifetime: -time.Second}},
			{name: "negative MaxConnIdleTime", cfg: PostgresPoolConfig{MaxConnIdleTime: -time.Second}},
			{name: "negative HealthCheckPeriod", cfg: PostgresPoolConfig{HealthCheckPeriod: -time.Second}},
			{name: "MinConns > MaxConns", cfg: PostgresPoolConfig{MaxConns: 2, MinConns: 3}},
			{name: "MinConns > default MaxConns", cfg: PostgresPoolConfig{MinConns: 25}},
		}
		for _, tc := range invalid {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := normalizePostgresPoolConfig(tc.cfg); err == nil {
					t.Fatalf("expected error for %+v", tc.cfg)
				}
			})
		}
	})
}

func TestBuildTelemetryPgxPoolConfig(t *testing.T) {
	dsn := "postgres://s2_telemetry_service:secret@127.0.0.1:5432/hvac_s2?sslmode=disable"
	custom := PostgresPoolConfig{
		MaxConns:          32,
		MinConns:          6,
		MaxConnLifetime:   20 * time.Minute,
		MaxConnIdleTime:   2 * time.Minute,
		HealthCheckPeriod: 45 * time.Second,
	}
	pgxCfg, err := buildTelemetryPgxPoolConfig(dsn, custom)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pgxCfg.MaxConns != 32 || pgxCfg.MinConns != 6 ||
		pgxCfg.MaxConnLifetime != 20*time.Minute ||
		pgxCfg.MaxConnIdleTime != 2*time.Minute ||
		pgxCfg.HealthCheckPeriod != 45*time.Second {
		t.Fatalf("unexpected pgxpool config: max=%d min=%d lifetime=%s idle=%s health=%s",
			pgxCfg.MaxConns, pgxCfg.MinConns, pgxCfg.MaxConnLifetime, pgxCfg.MaxConnIdleTime, pgxCfg.HealthCheckPeriod)
	}
	if pgxCfg.AfterConnect == nil {
		t.Fatal("expected AfterConnect role activation hook")
	}

	wrongUserDSN := "postgres://postgres:secret@127.0.0.1:5432/hvac_s2?sslmode=disable"
	if _, err := buildTelemetryPgxPoolConfig(wrongUserDSN, DefaultPostgresPoolConfig()); err == nil || !strings.Contains(err.Error(), "s2_telemetry_service") {
		t.Fatalf("expected s2_telemetry_service identity error, got %v", err)
	}

	if _, err := OpenPostgresStoreWithConfig(t.Context(), dsn, PostgresPoolConfig{MaxConns: -5}); err == nil {
		t.Fatal("expected OpenPostgresStoreWithConfig to reject negative pool config")
	}
}

func TestCollectLatestEvaluationUpdates(t *testing.T) {
	keys, freshness, revisions := collectLatestEvaluationUpdates([]telemetryapi.TelemetryKeyState{
		{Missing: &telemetryapi.TelemetryMissingState{Key: "zone.humidity"}},
		{Present: &telemetryapi.TelemetryPresentState{Key: "zone.temperature", Freshness: "FRESH", PolicyRevision: 3}},
		{Present: &telemetryapi.TelemetryPresentState{Key: "duct.pressure", Freshness: "STALE", PolicyRevision: 7}},
		{Present: &telemetryapi.TelemetryPresentState{Key: "zone.temperature", Freshness: "STALE", PolicyRevision: 4}},
	})
	if len(keys) != 2 || keys[0] != "duct.pressure" || keys[1] != "zone.temperature" {
		t.Fatalf("keys=%v, want [duct.pressure zone.temperature]", keys)
	}
	if len(freshness) != 2 || freshness[0] != "STALE" || freshness[1] != "STALE" {
		t.Fatalf("freshness=%v, want [STALE STALE]", freshness)
	}
	if len(revisions) != 2 || revisions[0] != 7 || revisions[1] != 4 {
		t.Fatalf("revisions=%v, want [7 4]", revisions)
	}

	emptyKeys, emptyFresh, emptyRev := collectLatestEvaluationUpdates([]telemetryapi.TelemetryKeyState{
		{Missing: &telemetryapi.TelemetryMissingState{Key: "zone.humidity"}},
	})
	if len(emptyKeys) != 0 || len(emptyFresh) != 0 || len(emptyRev) != 0 {
		t.Fatalf("expected empty slices for missing-only values, got %v %v %v", emptyKeys, emptyFresh, emptyRev)
	}
}

func TestPersistCurrentStateSQLStructure(t *testing.T) {
	if !strings.Contains(persistLatestEvaluationSQL, "unnest($4::text[], $5::text[], $6::bigint[])") {
		t.Fatalf("persistLatestEvaluationSQL missing unnest clause: %s", persistLatestEvaluationSQL)
	}
	if !strings.Contains(persistLatestEvaluationSQL, "IS DISTINCT FROM ($2, upd.freshness, upd.policy_revision)") {
		t.Fatalf("persistLatestEvaluationSQL missing IS DISTINCT FROM guard: %s", persistLatestEvaluationSQL)
	}
	if !strings.Contains(persistDevicePresenceSQL, "IS DISTINCT FROM") || !strings.Contains(persistDevicePresenceSQL, "evaluated_at") {
		t.Fatalf("persistDevicePresenceSQL missing IS DISTINCT FROM / evaluated_at: %s", persistDevicePresenceSQL)
	}
	if !strings.Contains(persistSnapshotSQL, "IS DISTINCT FROM") || !strings.Contains(persistSnapshotSQL, "snapshot_sha256") || !strings.Contains(persistSnapshotSQL, "evaluated_at") {
		t.Fatalf("persistSnapshotSQL missing IS DISTINCT FROM / snapshot_sha256 / evaluated_at: %s", persistSnapshotSQL)
	}
}
