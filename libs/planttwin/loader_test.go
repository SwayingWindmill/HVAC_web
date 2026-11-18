package planttwin

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestStaticLoaderIsolationAndErrors(t *testing.T) {
	scope := SiteScope{TenantID: testTenantID, SiteID: testSiteID}
	inputTwin := SiteTwin{
		Scope:    scope,
		Timezone: "Asia/Shanghai",
		Units: []PlantUnit{
			{AssetID: chillerAID, AssetCode: "chiller-a", AssetType: AssetTypeChiller, RatedPowerKW: floatPtr(127.5)},
			{AssetID: chwp1ID, AssetCode: "chwp-1", AssetType: AssetTypeChilledWaterPump},
		},
		HydraulicEdges: []HydraulicEdge{
			{FromAssetID: chwp1ID, ToAssetID: chillerAID, RelationshipRole: RelationshipServes},
		},
		Schedule: OperatingSchedule{
			Slots: []ScheduleSlot{{DayOfWeek: 1, StartMinute: 480, EndMinute: 1080, TargetSupplyTempC: floatPtr(7.0)}},
		},
	}

	loader, err := NewStaticLoader(inputTwin)
	if err != nil {
		t.Fatalf("NewStaticLoader error: %v", err)
	}

	// Mutate caller slice after construction to verify defensive copy.
	inputTwin.Units[0].AssetCode = "mutated"
	*inputTwin.Units[0].RatedPowerKW = 999

	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	loaded, err := loader.LoadSiteTwin(context.Background(), scope, at)
	if err != nil {
		t.Fatalf("LoadSiteTwin error: %v", err)
	}
	if loaded.Units[0].AssetCode != "chiller-a" || *loaded.Units[0].RatedPowerKW != 127.5 {
		t.Fatalf("StaticLoader leaked caller mutation: %+v", loaded.Units[0])
	}
	if loaded.Graph == nil || !loaded.Graph.HasExplicitTopology() || len(loaded.Graph.Upstream(chillerAID)) != 1 {
		t.Fatalf("StaticLoader did not rebuild HydraulicGraph cleanly: %+v", loaded.Graph)
	}
	if !loaded.LoadedAt.Equal(at) {
		t.Fatalf("expected LoadedAt=%s, got %s", at, loaded.LoadedAt)
	}

	missingScope := SiteScope{TenantID: testTenantID, SiteID: "019a0000-0000-7000-8000-000000000999"}
	if _, err := loader.LoadSiteTwin(context.Background(), missingScope, at); !errors.Is(err, ErrSiteNotFound) {
		t.Fatalf("expected ErrSiteNotFound, got %v", err)
	}
	if _, err := loader.LoadSiteTwin(context.Background(), SiteScope{TenantID: "bad", SiteID: testSiteID}, at); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("expected ErrInvalidScope, got %v", err)
	}
}

func TestPostgresLoaderQueriesAllViewsAndMapsResult(t *testing.T) {
	if _, err := NewPostgresLoader(nil); err == nil {
		t.Fatal("expected NewPostgresLoader(nil) to fail")
	}

	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	tx := newFakePlantTwinTx(at)
	beginner := &fakeTxBeginner{tx: tx}
	loader, err := NewPostgresLoader(beginner)
	if err != nil {
		t.Fatalf("NewPostgresLoader error: %v", err)
	}

	scope := SiteScope{TenantID: testTenantID, SiteID: testSiteID}
	twin, err := loader.LoadSiteTwin(context.Background(), scope, at)
	if err != nil {
		t.Fatalf("LoadSiteTwin error: %v", err)
	}

	if beginner.lastOpts.IsoLevel != pgx.ReadCommitted || beginner.lastOpts.AccessMode != pgx.ReadOnly {
		t.Fatalf("expected ReadCommitted ReadOnly tx, got %+v", beginner.lastOpts)
	}
	if !tx.committed {
		t.Fatal("expected transaction to be committed")
	}
	if len(tx.execCalls) != 1 || !strings.Contains(tx.execCalls[0].sql, "set_config('app.tenant_id'") ||
		!strings.Contains(tx.execCalls[0].sql, "set_config('app.authorized_site_ids'") {
		t.Fatalf("expected RLS set_config exec call, got %+v", tx.execCalls)
	}

	expectedViews := []string{
		"core_registry.plant_twin_sites_v1",
		"core_registry.plant_twin_units_v1",
		"core_registry.plant_twin_asset_locks_v1",
		"core_registry.plant_twin_devices_v1",
		"core_registry.plant_twin_device_bindings_v1",
		"core_registry.plant_twin_point_roles_v1",
		"core_registry.plant_twin_hydraulic_edges_v1",
		"core_registry.plant_twin_schedules_v1",
		"core_registry.plant_twin_schedule_slots_v1",
		"core_registry.plant_twin_schedule_exceptions_v1",
	}
	for _, view := range expectedViews {
		if !tx.sawView(view) {
			t.Fatalf("expected query against %s, seen=%v", view, tx.queriedSQL)
		}
	}

	if twin.Timezone != "Asia/Shanghai" || twin.SiteControlMode != ControlModeManualOverride || twin.SiteControlReason != "现场调试" {
		t.Fatalf("unexpected site metadata: %+v", twin)
	}
	chillerA, ok := twin.UnitByID(chillerAID)
	if !ok || chillerA.LockMode != LockModeManualLock || chillerA.LockReason != "检修锁定" || *chillerA.RatedPowerKW != 127.5 || chillerA.WaterLoop != WaterLoopChilledWater || chillerA.BranchCode != "BRANCH-A" {
		t.Fatalf("unexpected chiller-a unit/lock: %+v ok=%v", chillerA, ok)
	}
	chwp1, ok := twin.UnitByID(chwp1ID)
	if !ok || chwp1.LockMode != LockModeUnlocked {
		t.Fatalf("expected chwp-1 default UNLOCKED, got %+v ok=%v", chwp1, ok)
	}
	if pt, ok := twin.ResolveAssetPoint(chillerAID, RoleActivePower); !ok || pt.PointID != "pt-power-a" || pt.IsCopied {
		t.Fatalf("expected primary active power point, got %+v ok=%v", pt, ok)
	}
	if pt, ok := twin.ResolveAssetPoint(chillerAID, RoleCHWLeavingTemp); !ok || !pt.IsCopied {
		t.Fatalf("expected copied CHW leaving temp point derived from source_metadata, got %+v ok=%v", pt, ok)
	}
	if !twin.Graph.HasExplicitTopology() || len(twin.Graph.CoolingLoopsForChiller(chillerAID)) != 1 {
		t.Fatalf("expected hydraulic graph built with CHW loop, got %+v", twin.Graph)
	}
	if len(twin.Schedule.Slots) != 1 || twin.Schedule.Slots[0].DayOfWeek != 1 {
		t.Fatalf("expected revision-2 slot only, got %+v", twin.Schedule.Slots)
	}
	if len(twin.Schedule.Exceptions) != 1 || twin.Schedule.Exceptions[0].StartDate != "2026-11-20" || len(twin.Schedule.Exceptions[0].ReplacementSlots) != 1 {
		t.Fatalf("unexpected schedule exceptions: %+v", twin.Schedule.Exceptions)
	}

	rels, err := loader.LoadAssetRelationships(context.Background(), SiteScope{TenantID: testTenantID, SiteID: testSiteID}, at)
	if err != nil {
		t.Fatalf("LoadAssetRelationships error: %v", err)
	}
	if !tx.sawView("core_registry.plant_twin_asset_relationships_v1") {
		t.Fatalf("expected query against plant_twin_asset_relationships_v1, seen=%v", tx.queriedSQL)
	}
	if len(rels) != 1 || rels[0].RelationshipRole != RelationshipIsolates || rels[0].FromWaterLoop != WaterLoopChilledWater || rels[0].ToBranchCode != "BRANCH-A" {
		t.Fatalf("unexpected LoadAssetRelationships result: %+v", rels)
	}
}

func TestPostgresLoaderSiteNotFoundAndInvalidRole(t *testing.T) {
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	missingTx := newFakePlantTwinTx(at)
	missingTx.siteNotFound = true
	loader, err := NewPostgresLoader(&fakeTxBeginner{tx: missingTx})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.LoadSiteTwin(context.Background(), SiteScope{TenantID: testTenantID, SiteID: testSiteID}, at); !errors.Is(err, ErrSiteNotFound) {
		t.Fatalf("expected ErrSiteNotFound, got %v", err)
	}

	badRoleTx := newFakePlantTwinTx(at)
	badRoleTx.pointRoleOverride = "BOGUS_ROLE"
	badLoader, err := NewPostgresLoader(&fakeTxBeginner{tx: badRoleTx})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := badLoader.LoadSiteTwin(context.Background(), SiteScope{TenantID: testTenantID, SiteID: testSiteID}, at); err == nil {
		t.Fatal("expected error for invalid semantic_role from view")
	}
}

type fakeTxBeginner struct {
	tx       *fakePlantTwinTx
	lastOpts pgx.TxOptions
}

func (b *fakeTxBeginner) BeginTx(_ context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	b.lastOpts = opts
	return b.tx, nil
}

type execCall struct {
	sql  string
	args []any
}

type fakePlantTwinTx struct {
	evalAt            time.Time
	siteNotFound      bool
	pointRoleOverride string
	committed         bool
	rolledBack        bool
	execCalls         []execCall
	queriedSQL        []string
}

func newFakePlantTwinTx(evalAt time.Time) *fakePlantTwinTx {
	return &fakePlantTwinTx{evalAt: evalAt}
}

func (tx *fakePlantTwinTx) sawView(view string) bool {
	for _, sql := range tx.queriedSQL {
		if strings.Contains(sql, view) {
			return true
		}
	}
	return false
}

func (tx *fakePlantTwinTx) Begin(context.Context) (pgx.Tx, error) { return tx, nil }
func (tx *fakePlantTwinTx) Commit(context.Context) error          { tx.committed = true; return nil }
func (tx *fakePlantTwinTx) Rollback(context.Context) error        { tx.rolledBack = true; return nil }
func (tx *fakePlantTwinTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (tx *fakePlantTwinTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (tx *fakePlantTwinTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (tx *fakePlantTwinTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (tx *fakePlantTwinTx) Conn() *pgx.Conn { return nil }

func (tx *fakePlantTwinTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.execCalls = append(tx.execCalls, execCall{sql: sql, args: args})
	return pgconn.CommandTag{}, nil
}

func (tx *fakePlantTwinTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	tx.queriedSQL = append(tx.queriedSQL, sql)
	switch {
	case strings.Contains(sql, "plant_twin_units_v1"):
		return newFakeRows([][]any{
			{chillerAID, "chiller-a", "A 机组", AssetTypeChiller, floatPtr(127.5), floatPtr(572.0), 30.0, string(WaterLoopChilledWater), "BRANCH-A"},
			{chwp1ID, "chwp-1", "1#冷冻泵", AssetTypeChilledWaterPump, floatPtr(22.0), (*float64)(nil), 0.0, string(WaterLoopChilledWater), "BRANCH-A"},
			{chwLoopID, "chw-loop", "冷冻水总管", AssetTypeChilledWaterLoop, (*float64)(nil), (*float64)(nil), 0.0, string(WaterLoopChilledWater), ""},
		}), nil
	case strings.Contains(sql, "plant_twin_asset_locks_v1"):
		return newFakeRows([][]any{
			{chillerAID, LockModeManualLock, "检修锁定"},
		}), nil
	case strings.Contains(sql, "plant_twin_devices_v1"):
		return newFakeRows([][]any{
			{"dev-meter-a", "meter-a", "ELECTRIC_METER", "ammeter-unit4"},
		}), nil
	case strings.Contains(sql, "plant_twin_device_bindings_v1"):
		return newFakeRows([][]any{
			{chillerAID, "dev-meter-a", BindingMeters},
		}), nil
	case strings.Contains(sql, "plant_twin_point_roles_v1"):
		role := string(RoleCHWLeavingTemp)
		if tx.pointRoleOverride != "" {
			role = tx.pointRoleOverride
		}
		return newFakeRows([][]any{
			{"pt-power-a", "dev-meter-a", "meter-a", "ammeter-unit4", "power_total", "power_total", "TELEMETRY", "kW", false, []byte(`{}`), SubjectTypeAsset, "", chillerAID, BindingDescribes, string(RoleActivePower)},
			{"pt-temp-copy", "dev-meter-a", "meter-a", "ammeter-unit4", "chw_leaving", "chw_leaving", "TELEMETRY", "Cel", false, []byte(`{"copied":true}`), SubjectTypeAsset, "", chillerAID, BindingDescribes, role},
		}), nil
	case strings.Contains(sql, "plant_twin_hydraulic_edges_v1"):
		return newFakeRows([][]any{
			{chwp1ID, chillerAID, RelationshipServes},
			{chillerAID, chwLoopID, RelationshipServes},
		}), nil
	case strings.Contains(sql, "plant_twin_asset_relationships_v1"):
		return newFakeRows([][]any{
			{
				"rel-1",
				"019a0000-3000-7000-8000-000000000091", "mv-chw-a", "A支路电动阀", AssetTypeMotorizedValve,
				string(WaterLoopChilledWater), "BRANCH-A",
				chillerAID, "chiller-a", "A 机组", AssetTypeChiller,
				string(WaterLoopChilledWater), "BRANCH-A",
				RelationshipIsolates, time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC), (*time.Time)(nil),
			},
		}), nil
	case strings.Contains(sql, "plant_twin_schedule_slots_v1"):
		return newFakeRows([][]any{
			{"sched-1", int64(2), 1, 8 * 60, 18 * 60, floatPtr(7.0)},
			{"sched-1", int64(1), 2, 8 * 60, 18 * 60, floatPtr(8.0)}, // stale revision ignored
		}), nil
	case strings.Contains(sql, "plant_twin_schedule_exceptions_v1"):
		return newFakeRows([][]any{
			{"exc-1", "加班", ExceptionOvertimeCooling, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC), "2026-11-20", []byte(`[{"startMinute":600,"endMinute":900,"targetSupplyTempC":7.2}]`), "活动保障"},
		}), nil
	default:
		return newFakeRows(nil), nil
	}
}

func (tx *fakePlantTwinTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	tx.queriedSQL = append(tx.queriedSQL, sql)
	switch {
	case strings.Contains(sql, "plant_twin_sites_v1"):
		if tx.siteNotFound {
			return fakeRow{err: pgx.ErrNoRows}
		}
		return fakeRow{cols: []any{"Asia/Shanghai", ControlModeManualOverride, "现场调试"}}
	case strings.Contains(sql, "plant_twin_schedules_v1"):
		return fakeRow{cols: []any{"sched-1", 45, 30, int64(2)}}
	default:
		return fakeRow{err: pgx.ErrNoRows}
	}
}

type fakeRow struct {
	cols []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanFakeValues(r.cols, dest)
}

type fakeRows struct {
	data [][]any
	idx  int
}

func newFakeRows(data [][]any) *fakeRows {
	return &fakeRows{data: data, idx: -1}
}

func (r *fakeRows) Close()                        {}
func (r *fakeRows) Err() error                    { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}
func (r *fakeRows) Next() bool {
	r.idx++
	return r.idx < len(r.data)
}
func (r *fakeRows) Scan(dest ...any) error {
	return scanFakeValues(r.data[r.idx], dest)
}
func (r *fakeRows) Values() ([]any, error) { return r.data[r.idx], nil }
func (r *fakeRows) RawValues() [][]byte    { return nil }
func (r *fakeRows) Conn() *pgx.Conn        { return nil }

func scanFakeValues(vals []any, dest []any) error {
	for i := range dest {
		if scanner, ok := dest[i].(interface{ Scan(any) error }); ok {
			if err := scanner.Scan(vals[i]); err != nil {
				return err
			}
			continue
		}
		dv := reflect.ValueOf(dest[i])
		if dv.Kind() != reflect.Pointer {
			return errors.New("destination must be pointer")
		}
		sv := reflect.ValueOf(vals[i])
		if !sv.IsValid() {
			dv.Elem().Set(reflect.Zero(dv.Elem().Type()))
			continue
		}
		dv.Elem().Set(sv)
	}
	return nil
}
