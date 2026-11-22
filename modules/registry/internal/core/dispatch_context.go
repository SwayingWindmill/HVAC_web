package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
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
	SafetyBoundary          string                     `json:"safetyBoundary,omitempty"`
	FeedbackPointKey        string                     `json:"feedbackPointKey"`
	Interlocks              []CommandDispatchInterlock `json:"interlocks"`
}

var (
	dispatchPointCodePattern  = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}\z`)
	dispatchCapabilityPattern = regexp.MustCompile(`^[A-Z0-9_]{1,64}\z`)
)

type CommandDispatchContextStore interface {
	GetCommandDispatchContext(ctx context.Context, tenantID, siteID, deviceID, pointID string, at time.Time) (CommandDispatchContext, error)
	ResolveCommandDispatchContext(ctx context.Context, tenantID, siteID, deviceID, pointCode, capability string, at time.Time) (CommandDispatchContext, error)
}

type commandPointDispatchMeta struct {
	ActuationMode    string                     `json:"actuationMode"`
	FeedbackPointKey string                     `json:"feedbackPointKey"`
	InterlockChecks  []CommandDispatchInterlock `json:"interlockChecks"`
}

func (store *PostgresStore) beginDispatchContextTx(ctx context.Context, tenantID, siteID string) (pgx.Tx, error) {
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin Core dispatch context read: %w", err)
	}
	if _, err := transaction.Exec(ctx, `SET LOCAL ROLE s1_core_runtime`); err != nil {
		_ = transaction.Rollback(ctx)
		return nil, fmt.Errorf("activate Core runtime role: %w", err)
	}
	var configuredTenant, configuredSites string
	if err := transaction.QueryRow(ctx, `
SELECT set_config('app.tenant_id', $1, true),
       set_config('app.authorized_site_ids', $2, true)
`, tenantID, postgresUUIDArray([]string{siteID})).Scan(&configuredTenant, &configuredSites); err != nil {
		_ = transaction.Rollback(ctx)
		return nil, fmt.Errorf("set Core dispatch context RLS: %w", err)
	}
	return transaction, nil
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
	transaction, err := store.beginDispatchContextTx(ctx, tenantID, siteID)
	if err != nil {
		return CommandDispatchContext{}, err
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	return loadCommandDispatchContextInTx(ctx, transaction, tenantID, siteID, deviceID, pointID, at)
}

func (store *PostgresStore) ResolveCommandDispatchContext(
	ctx context.Context,
	tenantID, siteID, deviceID, pointCode, capability string,
	at time.Time,
) (CommandDispatchContext, error) {
	if store == nil || store.pool == nil {
		return CommandDispatchContext{}, ErrStoreClosed
	}
	pointCode = strings.TrimSpace(pointCode)
	capability = strings.TrimSpace(capability)
	if !validUUIDv7(tenantID) || !validUUIDv7(siteID) || !validUUIDv7(deviceID) || !dispatchCapabilityPattern.MatchString(capability) {
		return CommandDispatchContext{}, ErrInvalidMutation
	}
	if pointCode != "" && !dispatchPointCodePattern.MatchString(pointCode) {
		return CommandDispatchContext{}, ErrInvalidMutation
	}
	transaction, err := store.beginDispatchContextTx(ctx, tenantID, siteID)
	if err != nil {
		return CommandDispatchContext{}, err
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	rows, err := transaction.Query(ctx, `
SELECT id::text
FROM core_registry.telemetry_points
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND reporting_device_id = $3::uuid
  AND point_type = 'COMMAND'
  AND writable
  AND status = 'ACTIVE'
  AND COALESCE(source_metadata->>'capability', '') = $4
  AND ($5 = '' OR point_code = $5)
ORDER BY id ASC
LIMIT 2
`, tenantID, siteID, deviceID, capability, pointCode)
	if err != nil {
		return CommandDispatchContext{}, fmt.Errorf("resolve command Point for dispatch context: %w", err)
	}
	var pointIDs []string
	for rows.Next() {
		var pointID string
		if scanErr := rows.Scan(&pointID); scanErr != nil {
			rows.Close()
			return CommandDispatchContext{}, fmt.Errorf("scan resolved command Point ID: %w", scanErr)
		}
		pointIDs = append(pointIDs, pointID)
	}
	rows.Close()
	if rowsErr := rows.Err(); rowsErr != nil {
		return CommandDispatchContext{}, fmt.Errorf("iterate resolved command Point IDs: %w", rowsErr)
	}
	if len(pointIDs) == 0 {
		return CommandDispatchContext{}, ErrNotFound
	}
	if len(pointIDs) > 1 {
		return CommandDispatchContext{}, ErrBindingConflict
	}
	return loadCommandDispatchContextInTx(ctx, transaction, tenantID, siteID, deviceID, pointIDs[0], at)
}

func loadCommandDispatchContextInTx(
	ctx context.Context,
	transaction pgx.Tx,
	tenantID, siteID, deviceID, pointID string,
	at time.Time,
) (CommandDispatchContext, error) {
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

	var timezone string
	err := transaction.QueryRow(ctx, `
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

	var pointCode string
	var rawMeta []byte
	err = transaction.QueryRow(ctx, `
SELECT point_code, COALESCE(source_metadata, '{}'::jsonb)
FROM core_registry.telemetry_points
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND reporting_device_id = $3::uuid
  AND id = $4::uuid
  AND point_type = 'COMMAND'
  AND writable
  AND status = 'ACTIVE'
`, tenantID, siteID, deviceID, pointID).Scan(&pointCode, &rawMeta)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CommandDispatchContext{}, fmt.Errorf("query command Point dispatch metadata: %w", err)
	}
	if err == nil && len(rawMeta) > 0 {
		var meta commandPointDispatchMeta
		if err := json.Unmarshal(rawMeta, &meta); err != nil {
			return CommandDispatchContext{}, fmt.Errorf("decode command Point dispatch metadata: %w", err)
		}
		var rawMetaMap map[string]any
		if err := json.Unmarshal(rawMeta, &rawMetaMap); err == nil {
			if _, protected := edgecontrol.ClassifyHardRealtimeProtection(pointCode, "", rawMetaMap); protected {
				result.SafetyBoundary = string(edgecontrol.SafetyBoundaryHardRealtime)
			}
		} else if _, protected := edgecontrol.ClassifyHardRealtimeProtection(pointCode, "", nil); protected {
			result.SafetyBoundary = string(edgecontrol.SafetyBoundaryHardRealtime)
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
		switch name {
		case "tenantId", "siteId", "deviceId", "pointId", "pointCode", "capability":
		default:
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The command dispatch context query is invalid.", false)
			return http.StatusBadRequest
		}
		if len(values) != 1 {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The command dispatch context query is invalid.", false)
			return http.StatusBadRequest
		}
	}
	tenantID := strings.TrimSpace(query.Get("tenantId"))
	siteID := strings.TrimSpace(query.Get("siteId"))
	deviceID := strings.TrimSpace(query.Get("deviceId"))
	pointID := strings.TrimSpace(query.Get("pointId"))
	pointCode := strings.TrimSpace(query.Get("pointCode"))
	capability := strings.TrimSpace(query.Get("capability"))
	if !validUUIDv7(tenantID) || !validUUIDv7(siteID) || !validUUIDv7(deviceID) {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "tenantId, siteId, and deviceId must all be valid UUIDv7 identifiers.", false)
		return http.StatusBadRequest
	}
	reader, ok := server.store.(CommandDispatchContextStore)
	if !ok || reader == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "The Core Registry dispatch context reader is unavailable.", true)
		return http.StatusServiceUnavailable
	}

	var (
		contextView CommandDispatchContext
		err         error
	)
	if pointID != "" {
		if pointCode != "" || capability != "" || !validUUIDv7(pointID) {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "pointId cannot be combined with pointCode or capability and must be a valid UUIDv7 identifier.", false)
			return http.StatusBadRequest
		}
		contextView, err = reader.GetCommandDispatchContext(request.Context(), tenantID, siteID, deviceID, pointID, server.now().UTC())
	} else {
		if !dispatchCapabilityPattern.MatchString(capability) || (pointCode != "" && !dispatchPointCodePattern.MatchString(pointCode)) {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "Either pointId or a valid capability (with optional pointCode) is required.", false)
			return http.StatusBadRequest
		}
		contextView, err = reader.ResolveCommandDispatchContext(request.Context(), tenantID, siteID, deviceID, pointCode, capability, server.now().UTC())
	}
	if err != nil {
		if errors.Is(err, ErrInvalidMutation) {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The command dispatch context query is invalid.", false)
			return http.StatusBadRequest
		}
		if errors.Is(err, ErrBindingConflict) {
			writeProblem(writer, request, http.StatusConflict, "REGISTRY_POINT_AMBIGUOUS", "Multiple active writable command points matched the requested capability.", false)
			return http.StatusConflict
		}
		return server.writeStoreError(writer, request, err)
	}
	writeJSON(writer, http.StatusOK, contextView)
	return http.StatusOK
}
