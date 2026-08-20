package ruleruntime

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const postgresTestTenantID = "0198c1e0-1001-7000-8000-000000000001"
const postgresTestSiteID = "0198c1e0-1002-7000-8000-000000000002"

func TestPostgresStoreRestartsFromPersistedEffectBoundary(t *testing.T) {
	databaseURL := os.Getenv("RULE_RUNTIME_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("RULE_RUNTIME_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool := openTenantPool(t, ctx, databaseURL, postgresTestTenantID)
	store, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	plan := postgresAlarmIntentPlan(t)
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	if err := store.PutReleasedPlan(ctx, plan, now); err != nil {
		t.Fatal(err)
	}
	binding := RuleBinding{
		ID: "0198c1e0-1005-7000-8000-000000000005", TenantID: postgresTestTenantID, SiteID: postgresTestSiteID,
		Revision: 1, RuleRevisionID: plan.Revision.ID, Priority: 10,
	}
	if err := store.AppendBinding(ctx, binding, now); err != nil {
		t.Fatal(err)
	}
	reader := fixedSnapshotReader{value: TypedValue{Type: PortSnapshot, Data: json.RawMessage(`{"quality":"GOOD","value":42}`)}}
	beforeRestart := mustRuntime(t, plan, store, reader, nil, ModeLive)
	event := RuleEventEnvelope{
		EventID: "postgres-event-a", Schema: "telemetry.point.observed.v1", TenantID: postgresTestTenantID, SiteID: postgresTestSiteID,
		SubjectType: "POINT", SubjectID: "0198c1e0-1006-7000-8000-000000000006", OccurredAt: now, ReceivedAt: now,
		Payload: json.RawMessage(`{"value":41}`),
	}
	seed, _, err := beforeRestart.Start(ctx, binding, event, now)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := beforeRestart.Run(ctx, seed.Execution.ExecutionID, "pg-worker-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Execution.Status != ExecutionBlocked || len(blocked.Effects) != 1 {
		t.Fatalf("persisted boundary status=%s effects=%d", blocked.Execution.Status, len(blocked.Effects))
	}
	effectID := blocked.Effects[0].Effect.EffectID
	pool.Close()

	restartedPool := openTenantPool(t, ctx, databaseURL, postgresTestTenantID)
	defer restartedPool.Close()
	restartedStore, _ := NewPostgresStore(restartedPool)
	sink := &recordingEffectSink{}
	restarted := mustRuntime(t, plan, restartedStore, reader, sink, ModeLive)
	afterEffect, err := restarted.DispatchEffects(ctx, seed.Execution.ExecutionID, "pg-worker-b", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.ids) != 1 || sink.ids[0] != effectID || afterEffect.Effects[0].Status != "DELIVERED" {
		t.Fatalf("restart delivery ids=%v effect=%+v", sink.ids, afterEffect.Effects[0])
	}
	completed, err := restarted.Run(ctx, seed.Execution.ExecutionID, "pg-worker-c", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Execution.Status != ExecutionSucceeded {
		t.Fatalf("status=%s want SUCCEEDED", completed.Execution.Status)
	}
	var effectRows int
	if err := restartedPool.QueryRow(ctx, `SELECT count(*) FROM rule_runtime.execution_effects WHERE execution_id=$1`, seed.Execution.ExecutionID).Scan(&effectRows); err != nil {
		t.Fatal(err)
	}
	if effectRows != 1 {
		t.Fatalf("execution_effects rows=%d want 1", effectRows)
	}
}

func openTenantPool(t *testing.T, ctx context.Context, databaseURL, tenantID string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 2
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, `SELECT set_config('app.tenant_id',$1,false)`, tenantID); err != nil {
			return err
		}
		_, err := conn.Exec(ctx, `SET ROLE rule_runtime_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return pool
}

func postgresAlarmIntentPlan(t *testing.T) ExecutionPlan {
	t.Helper()
	rule := alarmIntentRule()
	rule.ID = "0198c1e0-1004-7000-8000-000000000004"
	rule.RuleID = "0198c1e0-1003-7000-8000-000000000003"
	rule.TenantID = postgresTestTenantID
	return mustCompile(t, rule, CoreCatalogV1())
}
