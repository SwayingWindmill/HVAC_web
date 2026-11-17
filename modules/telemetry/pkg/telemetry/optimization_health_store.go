package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// OptimizationPointHealthResult reports whether inspected optimization points are healthy,
// or names the blocking telemetry key and reason when suppressed.
type OptimizationPointHealthResult struct {
	Healthy      bool   `json:"healthy"`
	ReasonCode   string `json:"reasonCode,omitempty"`
	TelemetryKey string `json:"telemetryKey,omitempty"`
	BlockReason  string `json:"blockReason,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// InspectOptimizationPointHealth checks latest_accepted_telemetry health statuses and active
// probe_offset_estimates alarms for the requested Tenant, Site, Device IDs, and telemetry keys.
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

	var latestReadable bool
	if err = tx.QueryRow(ctx, `
SELECT CASE
  WHEN to_regclass('telemetry_runtime.latest_accepted_telemetry') IS NOT NULL
  THEN has_table_privilege(current_user, 'telemetry_runtime.latest_accepted_telemetry', 'SELECT')
  ELSE false
END`).Scan(&latestReadable); err != nil {
		return OptimizationPointHealthResult{}, err
	}
	if latestReadable && len(deviceIDs) > 0 {
		rows, queryErr := tx.Query(ctx, `
SELECT lat.telemetry_key, COALESCE(lat.health_status, ''), COALESCE(lat.health_reasons, '{}')
FROM telemetry_runtime.latest_accepted_telemetry lat
JOIN telemetry_runtime.devices d ON d.device_id = lat.device_id
WHERE d.tenant_id = $1::uuid
  AND d.site_id = $2::uuid
  AND lat.device_id = ANY($3::uuid[])
  AND lat.telemetry_key = ANY($4::text[])
ORDER BY lat.device_id, lat.telemetry_key`, tenantID, siteID, deviceIDs, telemetryKeys)
		if queryErr != nil {
			return OptimizationPointHealthResult{}, queryErr
		}
		var blockedKey, blockedReason string
		for rows.Next() {
			var key, healthStatus string
			var healthReasons []string
			if scanErr := rows.Scan(&key, &healthStatus, &healthReasons); scanErr != nil {
				rows.Close()
				return OptimizationPointHealthResult{}, scanErr
			}
			if reason, blocked := telemetryHealthBlockReason(healthStatus, healthReasons); blocked {
				blockedKey, blockedReason = key, reason
				break
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
			}, nil
		}
	}

	var probeOffsetsReadable bool
	if err = tx.QueryRow(ctx, `
SELECT CASE
  WHEN to_regclass('telemetry_runtime.probe_offset_estimates') IS NOT NULL
  THEN has_table_privilege(current_user, 'telemetry_runtime.probe_offset_estimates', 'SELECT')
  ELSE false
END`).Scan(&probeOffsetsReadable); err != nil {
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
			}, nil
		}
		if !errors.Is(queryErr, pgx.ErrNoRows) {
			return OptimizationPointHealthResult{}, queryErr
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return OptimizationPointHealthResult{}, err
	}
	return OptimizationPointHealthResult{Healthy: true}, nil
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
