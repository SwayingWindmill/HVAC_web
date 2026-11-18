package planttwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Loader loads a site's semantic plant twin snapshot evaluated at a point in time.
type Loader interface {
	LoadSiteTwin(ctx context.Context, scope SiteScope, at time.Time) (SiteTwin, error)
}

// TxBeginner starts a database transaction. *pgxpool.Pool and *pgx.Conn implement it.
type TxBeginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// PostgresLoader loads SiteTwin snapshots from the core_registry.plant_twin_*_v1
// ADR 0016 read views under tenant and site RLS session GUCs.
type PostgresLoader struct {
	db TxBeginner
}

// NewPostgresLoader creates a PostgresLoader backed by db.
func NewPostgresLoader(db TxBeginner) (*PostgresLoader, error) {
	if db == nil {
		return nil, errors.New("planttwin postgres beginner is required")
	}
	return &PostgresLoader{db: db}, nil
}

func (l *PostgresLoader) beginScopedReadTx(ctx context.Context, scope SiteScope, at time.Time) (pgx.Tx, SiteScope, time.Time, error) {
	if l == nil || l.db == nil {
		return nil, SiteScope{}, time.Time{}, errors.New("planttwin postgres loader is uninitialized")
	}
	if err := scope.Validate(); err != nil {
		return nil, SiteScope{}, time.Time{}, err
	}
	normScope := scope.normalized()
	evalAt := at.UTC()
	if evalAt.IsZero() {
		evalAt = time.Now().UTC()
	}

	tx, err := l.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.ReadCommitted,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, SiteScope{}, time.Time{}, fmt.Errorf("begin planttwin read tx: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.tenant_id', $1, true), set_config('app.authorized_site_ids', $2, true)`,
		normScope.TenantID, "{"+normScope.SiteID+"}",
	); err != nil {
		_ = tx.Rollback(ctx)
		return nil, SiteScope{}, time.Time{}, fmt.Errorf("set planttwin RLS scope: %w", err)
	}
	return tx, normScope, evalAt, nil
}

// LoadSiteTwin queries all 10 ADR 0016 plant twin views inside one ReadCommitted
// read-only transaction and builds the SiteTwin and its HydraulicGraph.
func (l *PostgresLoader) LoadSiteTwin(ctx context.Context, scope SiteScope, at time.Time) (SiteTwin, error) {
	tx, normScope, evalAt, err := l.beginScopedReadTx(ctx, scope, at)
	if err != nil {
		return SiteTwin{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	twin := SiteTwin{
		Scope:    normScope,
		LoadedAt: evalAt,
	}

	err = tx.QueryRow(ctx, `
SELECT timezone, COALESCE(site_control_mode, 'REMOTE_AUTO'), COALESCE(site_control_reason, '')
FROM core_registry.plant_twin_sites_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid`,
		normScope.TenantID, normScope.SiteID,
	).Scan(&twin.Timezone, &twin.SiteControlMode, &twin.SiteControlReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return SiteTwin{}, fmt.Errorf("%w: tenant=%s site=%s", ErrSiteNotFound, normScope.TenantID, normScope.SiteID)
	}
	if err != nil {
		return SiteTwin{}, fmt.Errorf("load plant_twin_sites_v1: %w", err)
	}
	twin.Timezone = strings.TrimSpace(twin.Timezone)
	twin.SiteControlMode = strings.TrimSpace(twin.SiteControlMode)
	twin.SiteControlReason = strings.TrimSpace(twin.SiteControlReason)
	if twin.SiteControlMode == "" {
		twin.SiteControlMode = ControlModeRemoteAuto
	}

	units, err := loadTwinUnits(ctx, tx, normScope)
	if err != nil {
		return SiteTwin{}, err
	}
	locks, err := loadTwinAssetLocks(ctx, tx, normScope)
	if err != nil {
		return SiteTwin{}, err
	}
	for i := range units {
		if lock, ok := locks[normalizeID(units[i].AssetID)]; ok {
			units[i].LockMode = lock.mode
			units[i].LockReason = lock.reason
		}
		if units[i].LockMode == "" {
			units[i].LockMode = LockModeUnlocked
		}
	}
	twin.Units = units

	if twin.Devices, err = loadTwinDevices(ctx, tx, normScope); err != nil {
		return SiteTwin{}, err
	}
	if twin.DeviceBindings, err = loadTwinDeviceBindings(ctx, tx, normScope, evalAt); err != nil {
		return SiteTwin{}, err
	}
	if twin.PointBindings, err = loadTwinPointBindings(ctx, tx, normScope, evalAt); err != nil {
		return SiteTwin{}, err
	}
	if twin.HydraulicEdges, err = loadTwinHydraulicEdges(ctx, tx, normScope, evalAt); err != nil {
		return SiteTwin{}, err
	}
	if twin.Schedule, err = loadTwinSchedule(ctx, tx, normScope, twin.Timezone); err != nil {
		return SiteTwin{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return SiteTwin{}, fmt.Errorf("commit planttwin read tx: %w", err)
	}

	return NewSiteTwin(twin)
}

type assetLockRecord struct {
	mode   string
	reason string
}

func loadTwinUnits(ctx context.Context, tx pgx.Tx, scope SiteScope) ([]PlantUnit, error) {
	rows, err := tx.Query(ctx, `
SELECT asset_id::text, asset_code, asset_name, asset_type,
       rated_power_kw, rated_capacity_kw, COALESCE(feeder_auxiliary_power_kw, 0),
       COALESCE(water_loop, ''), COALESCE(branch_code, '')
FROM core_registry.plant_twin_units_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY asset_code, asset_id`, scope.TenantID, scope.SiteID)
	if err != nil {
		return nil, fmt.Errorf("load plant_twin_units_v1: %w", err)
	}
	defer rows.Close()

	var units []PlantUnit
	for rows.Next() {
		var u PlantUnit
		var rawLoop string
		if err := rows.Scan(
			&u.AssetID, &u.AssetCode, &u.AssetName, &u.AssetType,
			&u.RatedPowerKW, &u.RatedCapacityKW, &u.FeederAuxiliaryPowerKW,
			&rawLoop, &u.BranchCode,
		); err != nil {
			return nil, fmt.Errorf("scan plant_twin_units_v1: %w", err)
		}
		if cleanLoop := strings.TrimSpace(rawLoop); cleanLoop != "" {
			parsedLoop, loopErr := ParseWaterLoop(cleanLoop)
			if loopErr != nil {
				return nil, fmt.Errorf("plant_twin_units_v1 asset %s: %w", u.AssetID, loopErr)
			}
			u.WaterLoop = parsedLoop
		}
		u.BranchCode = strings.TrimSpace(u.BranchCode)
		u.LockMode = LockModeUnlocked
		units = append(units, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plant_twin_units_v1: %w", err)
	}
	return units, nil
}

func loadTwinAssetLocks(ctx context.Context, tx pgx.Tx, scope SiteScope) (map[string]assetLockRecord, error) {
	rows, err := tx.Query(ctx, `
SELECT asset_id::text, lock_mode, COALESCE(reason, '')
FROM core_registry.plant_twin_asset_locks_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY asset_id`, scope.TenantID, scope.SiteID)
	if err != nil {
		return nil, fmt.Errorf("load plant_twin_asset_locks_v1: %w", err)
	}
	defer rows.Close()

	locks := make(map[string]assetLockRecord)
	for rows.Next() {
		var assetID, mode, reason string
		if err := rows.Scan(&assetID, &mode, &reason); err != nil {
			return nil, fmt.Errorf("scan plant_twin_asset_locks_v1: %w", err)
		}
		locks[normalizeID(assetID)] = assetLockRecord{
			mode:   strings.TrimSpace(mode),
			reason: strings.TrimSpace(reason),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plant_twin_asset_locks_v1: %w", err)
	}
	return locks, nil
}

func loadTwinDevices(ctx context.Context, tx pgx.Tx, scope SiteScope) ([]TwinDevice, error) {
	rows, err := tx.Query(ctx, `
SELECT device_id::text, device_code, device_type, COALESCE(source_key, '')
FROM core_registry.plant_twin_devices_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY device_code, device_id`, scope.TenantID, scope.SiteID)
	if err != nil {
		return nil, fmt.Errorf("load plant_twin_devices_v1: %w", err)
	}
	defer rows.Close()

	var devices []TwinDevice
	for rows.Next() {
		var d TwinDevice
		if err := rows.Scan(&d.DeviceID, &d.DeviceCode, &d.DeviceType, &d.SourceKey); err != nil {
			return nil, fmt.Errorf("scan plant_twin_devices_v1: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plant_twin_devices_v1: %w", err)
	}
	return devices, nil
}

func loadTwinDeviceBindings(ctx context.Context, tx pgx.Tx, scope SiteScope, evalAt time.Time) ([]DeviceBinding, error) {
	rows, err := tx.Query(ctx, `
SELECT asset_id::text, device_id::text, binding_role
FROM core_registry.plant_twin_device_bindings_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
  AND valid_from <= $3 AND (valid_to IS NULL OR valid_to > $3)
ORDER BY asset_id, binding_role, device_id`, scope.TenantID, scope.SiteID, evalAt)
	if err != nil {
		return nil, fmt.Errorf("load plant_twin_device_bindings_v1: %w", err)
	}
	defer rows.Close()

	var bindings []DeviceBinding
	for rows.Next() {
		var b DeviceBinding
		if err := rows.Scan(&b.AssetID, &b.DeviceID, &b.BindingRole); err != nil {
			return nil, fmt.Errorf("scan plant_twin_device_bindings_v1: %w", err)
		}
		bindings = append(bindings, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plant_twin_device_bindings_v1: %w", err)
	}
	return bindings, nil
}

func loadTwinPointBindings(ctx context.Context, tx pgx.Tx, scope SiteScope, evalAt time.Time) ([]PointBinding, error) {
	rows, err := tx.Query(ctx, `
SELECT point_id::text, reporting_device_id::text, device_code, COALESCE(device_source_key, ''),
       point_code, wire_source_key, point_type, COALESCE(unit, ''), writable,
       COALESCE(source_metadata, '{}'::jsonb), COALESCE(subject_type, ''),
       COALESCE(space_id::text, ''), COALESCE(asset_id::text, ''),
       COALESCE(binding_role, ''), COALESCE(semantic_role, '')
FROM core_registry.plant_twin_point_roles_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
  AND valid_from <= $3 AND (valid_to IS NULL OR valid_to > $3)
ORDER BY device_code, point_code, point_id`, scope.TenantID, scope.SiteID, evalAt)
	if err != nil {
		return nil, fmt.Errorf("load plant_twin_point_roles_v1: %w", err)
	}
	defer rows.Close()

	var bindings []PointBinding
	for rows.Next() {
		var pb PointBinding
		var rawMeta []byte
		var rawRole string
		if err := rows.Scan(
			&pb.PointID, &pb.DeviceID, &pb.DeviceCode, &pb.DeviceSourceKey,
			&pb.PointCode, &pb.WireSourceKey, &pb.PointType, &pb.Unit, &pb.Writable,
			&rawMeta, &pb.SubjectType, &pb.SpaceID, &pb.AssetID,
			&pb.BindingRole, &rawRole,
		); err != nil {
			return nil, fmt.Errorf("scan plant_twin_point_roles_v1: %w", err)
		}
		if len(rawMeta) > 0 {
			pb.SourceMetadata = json.RawMessage(append([]byte(nil), rawMeta...))
		}
		if strings.TrimSpace(rawRole) != "" {
			parsed, parseErr := ParseSemanticRole(rawRole)
			if parseErr != nil {
				return nil, fmt.Errorf("plant_twin_point_roles_v1 point %s: %w", pb.PointID, parseErr)
			}
			pb.SemanticRole = parsed
		}
		pb.IsCopied = IsCopiedMetadata(pb.SourceMetadata)
		bindings = append(bindings, pb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plant_twin_point_roles_v1: %w", err)
	}
	return bindings, nil
}

func loadTwinHydraulicEdges(ctx context.Context, tx pgx.Tx, scope SiteScope, evalAt time.Time) ([]HydraulicEdge, error) {
	rows, err := tx.Query(ctx, `
SELECT from_asset_id::text, to_asset_id::text, relationship_role
FROM core_registry.plant_twin_hydraulic_edges_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
  AND valid_from <= $3 AND (valid_to IS NULL OR valid_to > $3)
ORDER BY from_asset_id, to_asset_id, relationship_role`, scope.TenantID, scope.SiteID, evalAt)
	if err != nil {
		return nil, fmt.Errorf("load plant_twin_hydraulic_edges_v1: %w", err)
	}
	defer rows.Close()

	var edges []HydraulicEdge
	for rows.Next() {
		var e HydraulicEdge
		if err := rows.Scan(&e.FromAssetID, &e.ToAssetID, &e.RelationshipRole); err != nil {
			return nil, fmt.Errorf("scan plant_twin_hydraulic_edges_v1: %w", err)
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plant_twin_hydraulic_edges_v1: %w", err)
	}
	return edges, nil
}

func loadTwinSchedule(ctx context.Context, tx pgx.Tx, scope SiteScope, siteTimezone string) (OperatingSchedule, error) {
	sched := OperatingSchedule{
		Timezone: siteTimezone,
	}

	err := tx.QueryRow(ctx, `
SELECT schedule_id::text, pre_cool_lead_minutes, coast_down_lag_minutes, revision
FROM core_registry.plant_twin_schedules_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY schedule_id
LIMIT 1`, scope.TenantID, scope.SiteID).Scan(
		&sched.ScheduleID, &sched.PreCoolLeadMinutes, &sched.CoastDownLagMinutes, &sched.Revision,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return OperatingSchedule{}, fmt.Errorf("load plant_twin_schedules_v1: %w", err)
	}

	slotRows, err := tx.Query(ctx, `
SELECT schedule_id::text, schedule_revision, day_of_week, start_minute, end_minute, target_supply_temp_c
FROM core_registry.plant_twin_schedule_slots_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY day_of_week, start_minute, end_minute`, scope.TenantID, scope.SiteID)
	if err != nil {
		return OperatingSchedule{}, fmt.Errorf("load plant_twin_schedule_slots_v1: %w", err)
	}
	defer slotRows.Close()

	for slotRows.Next() {
		var rowSchedID string
		var rowRev int64
		var slot ScheduleSlot
		if err := slotRows.Scan(
			&rowSchedID, &rowRev, &slot.DayOfWeek, &slot.StartMinute, &slot.EndMinute, &slot.TargetSupplyTempC,
		); err != nil {
			return OperatingSchedule{}, fmt.Errorf("scan plant_twin_schedule_slots_v1: %w", err)
		}
		if sched.ScheduleID == "" || !strings.EqualFold(strings.TrimSpace(rowSchedID), strings.TrimSpace(sched.ScheduleID)) || rowRev != sched.Revision {
			continue
		}
		sched.Slots = append(sched.Slots, slot)
	}
	if err := slotRows.Err(); err != nil {
		return OperatingSchedule{}, fmt.Errorf("iterate plant_twin_schedule_slots_v1: %w", err)
	}

	excRows, err := tx.Query(ctx, `
SELECT exception_id::text, COALESCE(title, ''), kind, start_date::text, end_date::text,
       COALESCE(replacement_slots, '[]'::jsonb), COALESCE(reason, '')
FROM core_registry.plant_twin_schedule_exceptions_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY start_date, exception_id`, scope.TenantID, scope.SiteID)
	if err != nil {
		return OperatingSchedule{}, fmt.Errorf("load plant_twin_schedule_exceptions_v1: %w", err)
	}
	defer excRows.Close()

	for excRows.Next() {
		var exc ScheduleException
		var startDate, endDate flexibleDate
		var rawSlots []byte
		if err := excRows.Scan(
			&exc.ID, &exc.Title, &exc.Kind, &startDate, &endDate, &rawSlots, &exc.Reason,
		); err != nil {
			return OperatingSchedule{}, fmt.Errorf("scan plant_twin_schedule_exceptions_v1: %w", err)
		}
		exc.StartDate = string(startDate)
		exc.EndDate = string(endDate)
		if len(rawSlots) > 0 {
			if err := json.Unmarshal(rawSlots, &exc.ReplacementSlots); err != nil {
				return OperatingSchedule{}, fmt.Errorf("decode schedule exception %s replacement_slots: %w", exc.ID, err)
			}
		}
		sched.Exceptions = append(sched.Exceptions, exc)
	}
	if err := excRows.Err(); err != nil {
		return OperatingSchedule{}, fmt.Errorf("iterate plant_twin_schedule_exceptions_v1: %w", err)
	}

	return sched, nil
}

// PlantAssetRelationship describes an active asset relationship enriched with loop and branch metadata.
type PlantAssetRelationship struct {
	RelationshipID   string     `json:"relationshipId"`
	FromAssetID      string     `json:"fromAssetId"`
	FromAssetCode    string     `json:"fromAssetCode"`
	FromAssetName    string     `json:"fromAssetName"`
	FromAssetType    string     `json:"fromAssetType"`
	FromWaterLoop    WaterLoop  `json:"fromWaterLoop,omitempty"`
	FromBranchCode   string     `json:"fromBranchCode,omitempty"`
	ToAssetID        string     `json:"toAssetId"`
	ToAssetCode      string     `json:"toAssetCode"`
	ToAssetName      string     `json:"toAssetName"`
	ToAssetType      string     `json:"toAssetType"`
	ToWaterLoop      WaterLoop  `json:"toWaterLoop,omitempty"`
	ToBranchCode     string     `json:"toBranchCode,omitempty"`
	RelationshipRole string     `json:"relationshipRole"`
	ValidFrom        time.Time  `json:"validFrom"`
	ValidTo          *time.Time `json:"validTo,omitempty"`
}

// LoadAssetRelationships queries core_registry.plant_twin_asset_relationships_v1 inside a scoped read transaction.
func (l *PostgresLoader) LoadAssetRelationships(ctx context.Context, scope SiteScope, evalAt time.Time) ([]PlantAssetRelationship, error) {
	tx, normScope, resolvedAt, err := l.beginScopedReadTx(ctx, scope, evalAt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
SELECT relationship_id::text,
       from_asset_id::text, from_asset_code, from_asset_name, from_asset_type,
       COALESCE(from_water_loop, ''), COALESCE(from_branch_code, ''),
       to_asset_id::text, to_asset_code, to_asset_name, to_asset_type,
       COALESCE(to_water_loop, ''), COALESCE(to_branch_code, ''),
       relationship_role, valid_from, valid_to
FROM core_registry.plant_twin_asset_relationships_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
  AND valid_from <= $3 AND (valid_to IS NULL OR valid_to > $3)
ORDER BY relationship_role, from_asset_code, to_asset_code, relationship_id`, normScope.TenantID, normScope.SiteID, resolvedAt)
	if err != nil {
		return nil, fmt.Errorf("load plant_twin_asset_relationships_v1: %w", err)
	}
	defer rows.Close()

	var items []PlantAssetRelationship
	for rows.Next() {
		var item PlantAssetRelationship
		var fromLoop, toLoop string
		if err := rows.Scan(
			&item.RelationshipID,
			&item.FromAssetID, &item.FromAssetCode, &item.FromAssetName, &item.FromAssetType,
			&fromLoop, &item.FromBranchCode,
			&item.ToAssetID, &item.ToAssetCode, &item.ToAssetName, &item.ToAssetType,
			&toLoop, &item.ToBranchCode,
			&item.RelationshipRole, &item.ValidFrom, &item.ValidTo,
		); err != nil {
			return nil, fmt.Errorf("scan plant_twin_asset_relationships_v1: %w", err)
		}
		item.FromWaterLoop = WaterLoop(strings.TrimSpace(fromLoop))
		item.ToWaterLoop = WaterLoop(strings.TrimSpace(toLoop))
		item.FromBranchCode = strings.TrimSpace(item.FromBranchCode)
		item.ToBranchCode = strings.TrimSpace(item.ToBranchCode)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plant_twin_asset_relationships_v1: %w", err)
	}
	return items, nil
}

type flexibleDate string

func (d *flexibleDate) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = ""
		return nil
	case string:
		*d = flexibleDate(trimISODate(v))
		return nil
	case []byte:
		*d = flexibleDate(trimISODate(string(v)))
		return nil
	case time.Time:
		*d = flexibleDate(v.Format("2006-01-02"))
		return nil
	default:
		return fmt.Errorf("unsupported date scan source %T", src)
	}
}

func trimISODate(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
