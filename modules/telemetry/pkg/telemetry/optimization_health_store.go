package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// PointHealthInspector evaluates whether site telemetry points and probe offset
// estimates allow optimization runs or automated control dispatches to proceed.
type PointHealthInspector interface {
	InspectOptimizationPointHealth(ctx context.Context, req OptimizationPointHealthRequest) (OptimizationPointHealthResult, error)
}

// OptimizationPointHealthRequest scopes a point health inspection to one Tenant, Site,
// optional twin Device IDs, and telemetry keys required by optimization.
type OptimizationPointHealthRequest struct {
	TenantID      string   `json:"tenantId"`
	SiteID        string   `json:"siteId"`
	DeviceIDs     []string `json:"deviceIds,omitempty"`
	TelemetryKeys []string `json:"telemetryKeys"`
}

// OptimizationLivePoint carries the latest accepted telemetry sample and optional
// device presence, snapshot revision, and point revision metadata.
type OptimizationLivePoint struct {
	DeviceID         string     `json:"deviceId"`
	TelemetryKey     string     `json:"telemetryKey"`
	NumericValue     *float64   `json:"numericValue,omitempty"`
	BoolValue        *bool      `json:"boolValue,omitempty"`
	TextValue        string     `json:"textValue,omitempty"`
	Unit             string     `json:"unit,omitempty"`
	Quality          string     `json:"quality"`
	Freshness        string     `json:"freshness"`
	DevicePresence   string     `json:"devicePresence"`
	BusinessRevision int64      `json:"businessRevision"`
	PointRevision    int64      `json:"pointRevision"`
	SampledAt        *time.Time `json:"sampledAt,omitempty"`
	ReceivedAt       *time.Time `json:"receivedAt,omitempty"`
}

// OptimizationPointHealthResult reports whether inspected optimization points are healthy,
// or names the blocking telemetry key and reason when suppressed, along with matching live points.
type OptimizationPointHealthResult struct {
	Healthy      bool                    `json:"healthy"`
	ReasonCode   string                  `json:"reasonCode,omitempty"`
	TelemetryKey string                  `json:"telemetryKey,omitempty"`
	BlockReason  string                  `json:"blockReason,omitempty"`
	Detail       string                  `json:"detail,omitempty"`
	LivePoints   []OptimizationLivePoint `json:"livePoints,omitempty"`
}

// InspectOptimizationPointHealth checks latest_accepted_telemetry health statuses and active
// probe_offset_estimates alarms for the requested Tenant, Site, Device IDs, and telemetry keys,
// and projects matching live points with optional presence, snapshot revision, and point revision.
func (store *PostgresStore) InspectOptimizationPointHealth(ctx context.Context, req OptimizationPointHealthRequest) (OptimizationPointHealthResult, error) {
	if store == nil || store.pool == nil {
		return OptimizationPointHealthResult{}, errors.New("telemetry store pool is not configured")
	}
	tenantID := strings.TrimSpace(req.TenantID)
	siteID := strings.TrimSpace(req.SiteID)
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return OptimizationPointHealthResult{}, errors.New("telemetry optimization point health scope is invalid")
	}

	telemetryKeys := normalizeUniqueStrings(req.TelemetryKeys)
	if len(telemetryKeys) == 0 {
		return OptimizationPointHealthResult{Healthy: true}, nil
	}
	deviceIDs := normalizeUUIDStrings(req.DeviceIDs)

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return OptimizationPointHealthResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return OptimizationPointHealthResult{}, err
	}

	var schemaUsable bool
	if err = tx.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'telemetry_runtime')
   AND has_schema_privilege(current_user, 'telemetry_runtime', 'USAGE')`).Scan(&schemaUsable); err != nil {
		return OptimizationPointHealthResult{}, err
	}
	if !schemaUsable {
		if err = tx.Commit(ctx); err != nil {
			return OptimizationPointHealthResult{}, err
		}
		return OptimizationPointHealthResult{Healthy: true}, nil
	}

	latestReadable, err := telemetryTableReadable(ctx, tx, "telemetry_runtime.latest_accepted_telemetry")
	if err != nil {
		return OptimizationPointHealthResult{}, err
	}
	devicesReadable, err := telemetryTableReadable(ctx, tx, "telemetry_runtime.devices")
	if err != nil {
		return OptimizationPointHealthResult{}, err
	}

	var livePoints []OptimizationLivePoint
	if latestReadable && devicesReadable && (len(req.DeviceIDs) == 0 || len(deviceIDs) > 0) {
		presenceReadable, err := telemetryTableReadable(ctx, tx, "telemetry_runtime.device_presence")
		if err != nil {
			return OptimizationPointHealthResult{}, err
		}
		snapshotsReadable, err := telemetryTableReadable(ctx, tx, "telemetry_runtime.device_observation_snapshots")
		if err != nil {
			return OptimizationPointHealthResult{}, err
		}
		pointsReadable, err := telemetryTableReadable(ctx, tx, "telemetry_runtime.points")
		if err != nil {
			return OptimizationPointHealthResult{}, err
		}

		hasDeviceFilter := len(deviceIDs) > 0
		query := buildOptimizationLivePointsQuery(presenceReadable, snapshotsReadable, pointsReadable, hasDeviceFilter)
		args := []any{tenantID, siteID, telemetryKeys}
		if hasDeviceFilter {
			args = append(args, deviceIDs)
		}
		rows, queryErr := tx.Query(ctx, query, args...)
		if queryErr != nil {
			return OptimizationPointHealthResult{}, queryErr
		}

		var blockedKey, blockedReason string
		for rows.Next() {
			var point OptimizationLivePoint
			var rawValue []byte
			var valueType string
			var sampledAt, receivedAt *time.Time
			var healthStatus string
			var healthReasons []string
			if scanErr := rows.Scan(
				&point.DeviceID,
				&point.TelemetryKey,
				&rawValue,
				&valueType,
				&point.Unit,
				&point.Quality,
				&point.Freshness,
				&point.DevicePresence,
				&point.BusinessRevision,
				&point.PointRevision,
				&sampledAt,
				&receivedAt,
				&healthStatus,
				&healthReasons,
			); scanErr != nil {
				rows.Close()
				return OptimizationPointHealthResult{}, scanErr
			}
			decodeOptimizationLivePointValue(rawValue, valueType, &point)
			if sampledAt != nil && !sampledAt.IsZero() {
				ts := sampledAt.UTC()
				point.SampledAt = &ts
			}
			if receivedAt != nil && !receivedAt.IsZero() {
				ts := receivedAt.UTC()
				point.ReceivedAt = &ts
			}
			livePoints = append(livePoints, point)
			if blockedReason == "" {
				if reason, blocked := telemetryHealthBlockReason(healthStatus, healthReasons); blocked {
					blockedKey, blockedReason = point.TelemetryKey, reason
				}
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return OptimizationPointHealthResult{}, rowsErr
		}
		rows.Close()
		if blockedReason != "" {
			if err = tx.Commit(ctx); err != nil {
				return OptimizationPointHealthResult{}, err
			}
			return OptimizationPointHealthResult{
				Healthy:      false,
				ReasonCode:   "TELEMETRY_POINT_UNHEALTHY",
				TelemetryKey: blockedKey,
				BlockReason:  blockedReason,
				Detail:       fmt.Sprintf("point %s telemetry health (%s) suppresses optimization", blockedKey, blockedReason),
				LivePoints:   livePoints,
			}, nil
		}
	}

	probeOffsetsReadable, err := telemetryTableReadable(ctx, tx, "telemetry_runtime.probe_offset_estimates")
	if err != nil {
		return OptimizationPointHealthResult{}, err
	}
	if probeOffsetsReadable {
		var alarmedKey, alarmKind string
		queryErr := tx.QueryRow(ctx, `
SELECT key, alarm FROM (
  SELECT DISTINCT ON (pair_code)
    CASE WHEN supply_key = ANY($2::text[]) THEN supply_key ELSE return_key END AS key,
    COALESCE(alarm, '') AS alarm
  FROM telemetry_runtime.probe_offset_estimates
  WHERE site_id = $1::uuid
    AND (supply_key = ANY($2::text[]) OR return_key = ANY($2::text[]))
  ORDER BY pair_code, window_ended_at DESC
) latest
WHERE alarm <> ''
ORDER BY key, alarm
LIMIT 1`, siteID, telemetryKeys).Scan(&alarmedKey, &alarmKind)
		if queryErr == nil {
			if err = tx.Commit(ctx); err != nil {
				return OptimizationPointHealthResult{}, err
			}
			return OptimizationPointHealthResult{
				Healthy:      false,
				ReasonCode:   "PROBE_OFFSET_ALARM",
				TelemetryKey: alarmedKey,
				BlockReason:  alarmKind,
				Detail:       fmt.Sprintf("active probe offset alarm (%s) on point %s suppresses optimization", alarmKind, alarmedKey),
				LivePoints:   livePoints,
			}, nil
		}
		if !errors.Is(queryErr, pgx.ErrNoRows) {
			return OptimizationPointHealthResult{}, queryErr
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return OptimizationPointHealthResult{}, err
	}
	return OptimizationPointHealthResult{
		Healthy:    true,
		LivePoints: livePoints,
	}, nil
}

func telemetryTableReadable(ctx context.Context, tx pgx.Tx, table string) (bool, error) {
	var readable bool
	err := tx.QueryRow(ctx, `
SELECT CASE
  WHEN to_regclass($1) IS NOT NULL
  THEN has_table_privilege(current_user, $1, 'SELECT')
  ELSE false
END`, table).Scan(&readable)
	return readable, err
}

func buildOptimizationLivePointsQuery(presenceReadable, snapshotsReadable, pointsReadable, hasDeviceFilter bool) string {
	presenceExpr := "''::text"
	snapshotRevExpr := "COALESCE(lat.business_revision, 0)"
	pointRevExpr := "0::bigint"

	var joins strings.Builder
	if presenceReadable {
		presenceExpr = "COALESCE(dp.current_state, '')"
		joins.WriteString("\nLEFT JOIN telemetry_runtime.device_presence dp ON dp.device_id = lat.device_id")
	}
	if snapshotsReadable {
		snapshotRevExpr = "COALESCE(dos.business_revision, lat.business_revision, 0)"
		joins.WriteString("\nLEFT JOIN telemetry_runtime.device_observation_snapshots dos ON dos.device_id = lat.device_id")
	}
	if pointsReadable {
		pointRevExpr = "COALESCE(p.point_revision, 0)"
		joins.WriteString("\nLEFT JOIN telemetry_runtime.points p ON p.device_id = lat.device_id AND p.telemetry_key = lat.telemetry_key")
	}

	deviceFilter := ""
	if hasDeviceFilter {
		deviceFilter = "\n  AND lat.device_id = ANY($4::uuid[])"
	}

	return fmt.Sprintf(`
SELECT lat.device_id::text,
       lat.telemetry_key,
       lat.value,
       COALESCE(lat.value_type, ''),
       COALESCE(lat.unit, ''),
       COALESCE(lat.quality, ''),
       COALESCE(lat.freshness, ''),
       %s,
       %s,
       %s,
       lat.sampled_at,
       lat.received_at,
       COALESCE(lat.health_status, ''),
       COALESCE(lat.health_reasons, '{}')
FROM telemetry_runtime.latest_accepted_telemetry lat
JOIN telemetry_runtime.devices d ON d.device_id = lat.device_id%s
WHERE d.tenant_id = $1::uuid
  AND d.site_id = $2::uuid
  AND lat.telemetry_key = ANY($3::text[])%s
ORDER BY lat.device_id, lat.telemetry_key`, presenceExpr, snapshotRevExpr, pointRevExpr, joins.String(), deviceFilter)
}

func decodeOptimizationLivePointValue(raw []byte, valueType string, dst *OptimizationLivePoint) {
	if dst == nil {
		return
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return
	}
	switch strings.ToUpper(strings.TrimSpace(valueType)) {
	case "NUMBER":
		var num float64
		if json.Unmarshal(trimmed, &num) == nil && !math.IsNaN(num) && !math.IsInf(num, 0) {
			dst.NumericValue = &num
		}
		return
	case "BOOLEAN":
		var b bool
		if json.Unmarshal(trimmed, &b) == nil {
			dst.BoolValue = &b
		}
		return
	case "STRING":
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			dst.TextValue = s
		}
		return
	}

	if bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false")) {
		var b bool
		if json.Unmarshal(trimmed, &b) == nil {
			dst.BoolValue = &b
			return
		}
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			dst.TextValue = s
			return
		}
	}
	var num float64
	if json.Unmarshal(trimmed, &num) == nil && !math.IsNaN(num) && !math.IsInf(num, 0) {
		dst.NumericValue = &num
		return
	}
	dst.TextValue = string(trimmed)
}

func telemetryHealthBlockReason(healthStatus string, reasons []string) (string, bool) {
	status := strings.ToUpper(strings.TrimSpace(healthStatus))
	if status == "UNHEALTHY" || status == "BAD" {
		return status, true
	}
	for _, reason := range reasons {
		clean := strings.ToUpper(strings.TrimSpace(reason))
		if clean == "PROBE_OFFSET" || clean == "UNHEALTHY" {
			return clean, true
		}
	}
	return "", false
}

func normalizeUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		clean := strings.TrimSpace(raw)
		if clean == "" {
			continue
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func normalizeUUIDStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		clean := strings.TrimSpace(raw)
		if !uuidPattern.MatchString(clean) {
			continue
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}
