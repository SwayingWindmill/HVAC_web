package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const CommandDispatchContextPath = RegistryPathPrefix + "commands/dispatch-context"

type CommandDispatchInterlock struct {
	DeviceID string `json:"deviceId"`
	Key      string `json:"key"`
	Expected string `json:"expected"`
	Reason   string `json:"reason"`
}

type CommandDispatchContext struct {
	TenantID                string                     `json:"tenantId"`
	SiteID                  string                     `json:"siteId"`
	DeviceID                string                     `json:"deviceId"`
	PointID                 string                     `json:"pointId"`
	AssetID                 string                     `json:"assetId,omitempty"`
	ControlMode             string                     `json:"controlMode"`
	AssetLocks              []AssetControlLockState    `json:"assetLocks"`
	ActiveMaintenanceWindow *ActiveMaintenanceWindow   `json:"activeMaintenanceWindow,omitempty"`
	ActuationMode           string                     `json:"actuationMode,omitempty"`
	FeedbackPointKey        string                     `json:"feedbackPointKey"`
	Interlocks              []CommandDispatchInterlock `json:"interlocks"`
}

type CommandDispatchContextStore interface {
	GetCommandDispatchContext(ctx context.Context, tenantID, siteID, deviceID, pointID string, at time.Time) (CommandDispatchContext, error)
}

type commandPointDispatchMeta struct {
	ActuationMode    string                     `json:"actuationMode"`
	FeedbackPointKey string                     `json:"feedbackPointKey"`
	InterlockChecks  []CommandDispatchInterlock `json:"interlockChecks"`
}

func (store *PostgresStore) GetCommandDispatchContext(
	ctx context.Context,
	tenantID, siteID, deviceID, pointID string,
	at time.Time,
) (CommandDispatchContext, error) {
	if store == nil || store.pool == nil {
		return CommandDispatchContext{}, ErrStoreClosed
	}
	if !validUUIDv7(tenantID) || !validUUIDv7(siteID) || !validUUIDv7(deviceID) || !validUUIDv7(pointID) {
		return CommandDispatchContext{}, ErrInvalidMutation
	}

	result := CommandDispatchContext{
		TenantID:         tenantID,
		SiteID:           siteID,
		DeviceID:         deviceID,
		PointID:          pointID,
		ControlMode:      SiteControlModeRemoteAuto,
		AssetLocks:       []AssetControlLockState{},
		FeedbackPointKey: "run_state",
		Interlocks:       []CommandDispatchInterlock{},
	}

	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return CommandDispatchContext{}, fmt.Errorf("begin Core dispatch context read: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	if _, err := transaction.Exec(ctx, `SET LOCAL ROLE s1_core_runtime`); err != nil {
		return CommandDispatchContext{}, fmt.Errorf("activate Core runtime role: %w", err)
	}
	var configuredTenant, configuredSites string
	if err := transaction.QueryRow(ctx, `
SELECT set_config('app.tenant_id', $1, true),
       set_config('app.authorized_site_ids', $2, true)
`, tenantID, postgresUUIDArray([]string{siteID})).Scan(&configuredTenant, &configuredSites); err != nil {
		return CommandDispatchContext{}, fmt.Errorf("set Core dispatch context RLS: %w", err)
	}

	var timezone string
	err = transaction.QueryRow(ctx, `
SELECT timezone
FROM core_registry.sites
WHERE tenant_id = $1::uuid AND id = $2::uuid
`, tenantID, siteID).Scan(&timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommandDispatchContext{}, ErrNotFound
	}
	if err != nil {
		return CommandDispatchContext{}, fmt.Errorf("query Site for dispatch context: %w", err)
	}

	var controlMode string
	err = transaction.QueryRow(ctx, `
SELECT mode
FROM core_registry.site_control_states
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
`, tenantID, siteID).Scan(&controlMode)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CommandDispatchContext{}, fmt.Errorf("query Site control mode: %w", err)
	}
	if err == nil && strings.TrimSpace(controlMode) != "" {
		result.ControlMode = strings.TrimSpace(controlMode)
	}

	assetLocks, err := querySiteAssetLocks(ctx, transaction, tenantID, siteID)
	if err != nil {
		return CommandDispatchContext{}, err
	}
	result.AssetLocks = assetLocks

	maintWindow, err := queryActiveMaintenanceWindow(ctx, transaction, tenantID, siteID, timezone, at.UTC())
	if err != nil {
		return CommandDispatchContext{}, err
	}
	result.ActiveMaintenanceWindow = maintWindow

	var boundAssetID string
	err = transaction.QueryRow(ctx, `
SELECT asset_id::text
FROM core_registry.point_subject_bindings
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND point_id = $3::uuid
  AND binding_role = 'CONTROLS'
  AND status = 'ACTIVE'
  AND valid_to IS NULL
  AND asset_id IS NOT NULL
ORDER BY asset_id::text ASC
LIMIT 1
`, tenantID, siteID, pointID).Scan(&boundAssetID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = transaction.QueryRow(ctx, `
SELECT asset_id::text
FROM core_registry.device_bindings
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND device_id = $3::uuid
  AND status = 'ACTIVE'
  AND valid_to IS NULL
ORDER BY asset_id::text ASC
LIMIT 1
`, tenantID, siteID, deviceID).Scan(&boundAssetID)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CommandDispatchContext{}, fmt.Errorf("query bound Asset for command dispatch context: %w", err)
	}
	if err == nil {
		result.AssetID = boundAssetID
	}

	var rawMeta []byte
	err = transaction.QueryRow(ctx, `
SELECT COALESCE(source_metadata, '{}'::jsonb)
FROM core_registry.telemetry_points
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND reporting_device_id = $3::uuid
  AND id = $4::uuid
  AND point_type = 'COMMAND'
  AND writable
  AND status = 'ACTIVE'
`, tenantID, siteID, deviceID, pointID).Scan(&rawMeta)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CommandDispatchContext{}, fmt.Errorf("query command Point dispatch metadata: %w", err)
	}
	if err == nil && len(rawMeta) > 0 {
		var meta commandPointDispatchMeta
		if err := json.Unmarshal(rawMeta, &meta); err != nil {
			return CommandDispatchContext{}, fmt.Errorf("decode command Point dispatch metadata: %w", err)
		}
		result.ActuationMode = strings.ToUpper(strings.TrimSpace(meta.ActuationMode))
		if feedbackKey := strings.TrimSpace(meta.FeedbackPointKey); feedbackKey != "" {
			result.FeedbackPointKey = feedbackKey
		}
		for _, check := range meta.InterlockChecks {
			devID := strings.TrimSpace(check.DeviceID)
			key := strings.TrimSpace(check.Key)
			expected := strings.TrimSpace(check.Expected)
			if devID == "" || key == "" || expected == "" {
				return CommandDispatchContext{}, fmt.Errorf("invalid command interlock metadata")
			}
			result.Interlocks = append(result.Interlocks, CommandDispatchInterlock{
				DeviceID: devID,
				Key:      key,
				Expected: expected,
				Reason:   strings.TrimSpace(check.Reason),
			})
		}
	}

	if err := transaction.Commit(ctx); err != nil {
		return CommandDispatchContext{}, fmt.Errorf("commit Core dispatch context read: %w", err)
	}
	return result, nil
}

func (server *server) handleCommandDispatchContext(writer http.ResponseWriter, request *http.Request) int {
	query := request.URL.Query()
	for name, values := range query {
		if (name != "tenantId" && name != "siteId" && name != "deviceId" && name != "pointId") || len(values) != 1 {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The command dispatch context query is invalid.", false)
			return http.StatusBadRequest
		}
	}
	tenantID := strings.TrimSpace(query.Get("tenantId"))
	siteID := strings.TrimSpace(query.Get("siteId"))
	deviceID := strings.TrimSpace(query.Get("deviceId"))
	pointID := strings.TrimSpace(query.Get("pointId"))
	if !validUUIDv7(tenantID) || !validUUIDv7(siteID) || !validUUIDv7(deviceID) || !validUUIDv7(pointID) {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "tenantId, siteId, deviceId, and pointId must all be valid UUIDv7 identifiers.", false)
		return http.StatusBadRequest
	}
	reader, ok := server.store.(CommandDispatchContextStore)
	if !ok || reader == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "The Core Registry dispatch context reader is unavailable.", true)
		return http.StatusServiceUnavailable
	}
	contextView, err := reader.GetCommandDispatchContext(request.Context(), tenantID, siteID, deviceID, pointID, server.now().UTC())
	if err != nil {
		return server.writeStoreError(writer, request, err)
	}
	writeJSON(writer, http.StatusOK, contextView)
	return http.StatusOK
}
