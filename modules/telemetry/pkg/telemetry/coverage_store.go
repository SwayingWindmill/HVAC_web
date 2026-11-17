package telemetry

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

var ErrInvalidCoverageReport = errors.New("telemetry source coverage report is invalid")

type GatewayCoverageScope struct {
	GatewayID string   `json:"gatewayId"`
	DeviceIDs []string `json:"deviceIds"`
}

type SourceCoverageReport struct {
	SourceID       string                 `json:"sourceId"`
	DeviceIDs      []string               `json:"deviceIds"`
	GatewayScopes  []GatewayCoverageScope `json:"gatewayScopes,omitempty"`
	Available      bool                   `json:"available"`
	ReasonCode     *string                `json:"reasonCode,omitempty"`
	ObservedAt     time.Time              `json:"observedAt"`
	SourceRevision int64                  `json:"sourceRevision"`
}

type CoverageAcceptor interface {
	ReportSourceCoverage(context.Context, SourceCoverageReport) error
}

const maximumCoverageDeviceIDs = 2048

func (store *PostgresStore) ReportSourceCoverage(ctx context.Context, report SourceCoverageReport) error {
	if store == nil || store.pool == nil {
		return errors.New("telemetry runtime store is closed")
	}
	normalized, err := validateAndNormalizeCoverageReport(report)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		err := store.reportSourceCoverageOnce(ctx, normalized)
		if err == nil || !retryableTelemetryTransaction(err) {
			return err
		}
	}
	return errors.New("telemetry source coverage transaction retry budget exhausted")
}

func (store *PostgresStore) reportSourceCoverageOnce(ctx context.Context, report SourceCoverageReport) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin telemetry source coverage transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	targetDeviceIDs := report.DeviceIDs
	if report.Available && len(report.GatewayScopes) > 0 {
		gatewayIDs := make([]string, len(report.GatewayScopes))
		for index, scope := range report.GatewayScopes {
			gatewayIDs[index] = scope.GatewayID
		}
		disconnectedGateways, err := findDisconnectedMQTTGateways(ctx, tx, gatewayIDs)
		if err != nil {
			return err
		}
		if len(disconnectedGateways) > 0 {
			targetDeviceIDs = filterAvailableDevicesByGatewayScopes(report.DeviceIDs, report.GatewayScopes, disconnectedGateways)
			if len(targetDeviceIDs) == 0 {
				return tx.Commit(ctx)
			}
		}
	}
	existingIDs, err := findExistingTelemetryDevices(ctx, tx, targetDeviceIDs)
	if err != nil {
		return err
	}
	if len(existingIDs) == 0 {
		return tx.Commit(ctx)
	}
	for _, deviceID := range existingIDs {
		if err := lockDevice(ctx, tx, deviceID); err != nil {
			return err
		}
	}
	changedIDs, err := applyObservationCoverage(ctx, tx, existingIDs, report.Available, report.ReasonCode, report.ObservedAt, report.SourceRevision)
	if err != nil {
		return err
	}
	for _, deviceID := range changedIDs {
		if _, err := store.evaluateAndPersistDevice(ctx, tx, deviceID, nil, report.ObservedAt); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit telemetry source coverage transaction: %w", err)
	}
	return nil
}

func validateAndNormalizeCoverageReport(report SourceCoverageReport) (SourceCoverageReport, error) {
	sourceID := strings.TrimSpace(report.SourceID)
	if len(sourceID) < 1 || len(sourceID) > 256 {
		return SourceCoverageReport{}, fmt.Errorf("%w: sourceId is invalid", ErrInvalidCoverageReport)
	}
	if len(report.DeviceIDs) < 1 || len(report.DeviceIDs) > maximumCoverageDeviceIDs {
		return SourceCoverageReport{}, fmt.Errorf("%w: deviceIds length is out of bounds", ErrInvalidCoverageReport)
	}
	if report.ObservedAt.IsZero() {
		return SourceCoverageReport{}, fmt.Errorf("%w: observedAt is required", ErrInvalidCoverageReport)
	}
	if report.SourceRevision < 1 {
		return SourceCoverageReport{}, fmt.Errorf("%w: sourceRevision must be positive", ErrInvalidCoverageReport)
	}
	if len(report.GatewayScopes) > maximumCoverageDeviceIDs {
		return SourceCoverageReport{}, fmt.Errorf("%w: gatewayScopes length is out of bounds", ErrInvalidCoverageReport)
	}
	deviceIDs, err := canonicalDeviceIDs(report.DeviceIDs)
	if err != nil {
		return SourceCoverageReport{}, fmt.Errorf("%w: %v", ErrInvalidCoverageReport, err)
	}
	gatewayScopes, err := canonicalGatewayScopes(report.GatewayScopes, deviceIDs)
	if err != nil {
		return SourceCoverageReport{}, err
	}
	var reasonCode *string
	if report.Available {
		if report.ReasonCode != nil && strings.TrimSpace(*report.ReasonCode) != "" {
			return SourceCoverageReport{}, fmt.Errorf("%w: available coverage must not carry reasonCode", ErrInvalidCoverageReport)
		}
	} else {
		if report.ReasonCode == nil || strings.TrimSpace(*report.ReasonCode) == "" {
			return SourceCoverageReport{}, fmt.Errorf("%w: unavailable coverage requires reasonCode", ErrInvalidCoverageReport)
		}
		normalizedReason, err := normalizeCoverageReasonCode(*report.ReasonCode)
		if err != nil {
			return SourceCoverageReport{}, err
		}
		reasonCode = &normalizedReason
	}
	return SourceCoverageReport{
		SourceID:       sourceID,
		DeviceIDs:      deviceIDs,
		GatewayScopes:  gatewayScopes,
		Available:      report.Available,
		ReasonCode:     reasonCode,
		ObservedAt:     report.ObservedAt.UTC(),
		SourceRevision: report.SourceRevision,
	}, nil
}

func canonicalGatewayScopes(raw []GatewayCoverageScope, reportDeviceIDs []string) ([]GatewayCoverageScope, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	deviceSet := make(map[string]struct{}, len(reportDeviceIDs))
	for _, id := range reportDeviceIDs {
		deviceSet[id] = struct{}{}
	}
	byGateway := make(map[string][]string, len(raw))
	gatewayOrder := make([]string, 0, len(raw))
	for _, scope := range raw {
		gatewayID := strings.TrimSpace(scope.GatewayID)
		if len(gatewayID) < 1 || len(gatewayID) > 256 {
			return nil, fmt.Errorf("%w: gatewayScope gatewayId is invalid", ErrInvalidCoverageReport)
		}
		if len(scope.DeviceIDs) < 1 || len(scope.DeviceIDs) > maximumCoverageDeviceIDs {
			return nil, fmt.Errorf("%w: gatewayScope deviceIds length is out of bounds", ErrInvalidCoverageReport)
		}
		scopeDeviceIDs, err := canonicalDeviceIDs(scope.DeviceIDs)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCoverageReport, err)
		}
		for _, id := range scopeDeviceIDs {
			if _, ok := deviceSet[id]; !ok {
				return nil, fmt.Errorf("%w: gatewayScope deviceId %q is absent from deviceIds", ErrInvalidCoverageReport, id)
			}
		}
		if _, seen := byGateway[gatewayID]; !seen {
			gatewayOrder = append(gatewayOrder, gatewayID)
		}
		byGateway[gatewayID] = append(byGateway[gatewayID], scopeDeviceIDs...)
	}
	slices.Sort(gatewayOrder)
	scopes := make([]GatewayCoverageScope, 0, len(gatewayOrder))
	for _, gatewayID := range gatewayOrder {
		merged, err := canonicalDeviceIDs(byGateway[gatewayID])
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCoverageReport, err)
		}
		scopes = append(scopes, GatewayCoverageScope{
			GatewayID: gatewayID,
			DeviceIDs: merged,
		})
	}
	return scopes, nil
}

func filterAvailableDevicesByGatewayScopes(
	deviceIDs []string,
	gatewayScopes []GatewayCoverageScope,
	disconnectedGateways map[string]bool,
) []string {
	scoped := make(map[string]struct{}, len(deviceIDs))
	connected := make(map[string]struct{}, len(deviceIDs))
	for _, scope := range gatewayScopes {
		isDisconnected := disconnectedGateways[scope.GatewayID]
		for _, id := range scope.DeviceIDs {
			scoped[id] = struct{}{}
			if !isDisconnected {
				connected[id] = struct{}{}
			}
		}
	}
	filtered := make([]string, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		if _, isScoped := scoped[id]; !isScoped {
			filtered = append(filtered, id)
			continue
		}
		if _, isConnected := connected[id]; isConnected {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

func findDisconnectedMQTTGateways(ctx context.Context, tx pgx.Tx, gatewayIDs []string) (map[string]bool, error) {
	if len(gatewayIDs) == 0 {
		return nil, nil
	}
	futureSkewSeconds := int64(maximumRuntimeEventFutureSkew / time.Second)
	rows, err := tx.Query(ctx, `
SELECT DISTINCT d.gateway_id
FROM telemetry_runtime.mqtt_runtime_events d
WHERE d.gateway_id = ANY($1::text[])
  AND UPPER(TRIM(d.event_type)) = ANY($2::text[])
  AND (UPPER(TRIM(d.source_type)) = 'GATEWAY' OR UPPER(TRIM(d.event_type)) IN ('GATEWAY_DISCONNECTED', 'LWT', 'LAST_WILL'))
  AND d.event_time <= d.received_at + ($4::bigint * interval '1 second')
  AND NOT EXISTS (
    SELECT 1
    FROM telemetry_runtime.mqtt_runtime_events c
    WHERE c.tenant_id = d.tenant_id
      AND c.site_id = d.site_id
      AND c.gateway_id = d.gateway_id
      AND UPPER(TRIM(c.source_type)) = UPPER(TRIM(d.source_type))
      AND TRIM(c.source_id) = TRIM(d.source_id)
      AND c.message_id <> d.message_id
      AND UPPER(TRIM(c.event_type)) = ANY($3::text[])
      AND c.event_time <= c.received_at + ($4::bigint * interval '1 second')
      AND (
        (
          c.event_time > d.event_time
          AND (
            COALESCE(d.data ->> 'sessionId', '') = ''
            OR COALESCE(c.data ->> 'sessionId', '') <> COALESCE(d.data ->> 'sessionId', '')
          )
        )
        OR (
          UPPER(TRIM(d.event_type)) IN ('LWT', 'LAST_WILL')
          AND COALESCE(d.data ->> 'sessionId', '') <> ''
          AND COALESCE(c.data ->> 'sessionId', '') <> ''
          AND COALESCE(c.data ->> 'sessionId', '') <> COALESCE(d.data ->> 'sessionId', '')
          AND c.event_time >= d.event_time
        )
      )
  )
  AND NOT EXISTS (
    SELECT 1
    FROM telemetry_runtime.source_observations so
    WHERE so.source_id = d.gateway_id
      AND so.acceptance_status = 'ACCEPTED'
      AND so.received_at > d.received_at
  )
`, gatewayIDs, lifecycleDisconnectEventTypes, lifecycleConnectEventTypes, futureSkewSeconds)
	if err != nil {
		return nil, fmt.Errorf("query disconnected MQTT gateways: %w", err)
	}
	defer rows.Close()
	disconnected := make(map[string]bool)
	for rows.Next() {
		var gatewayID string
		if err := rows.Scan(&gatewayID); err != nil {
			return nil, fmt.Errorf("scan disconnected MQTT gateway: %w", err)
		}
		disconnected[gatewayID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate disconnected MQTT gateways: %w", err)
	}
	return disconnected, nil
}

// normalizeCoverageReasonCode maps gateway connectivity loss reasons onto the four
// AvailabilityReasonCode values permitted by the database constraint and OpenAPI contract.
func normalizeCoverageReasonCode(raw string) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	switch code {
	case string(telemetryapi.AvailabilityReasonCodeSourceUnavailable),
		"GATEWAY_DISCONNECTED",
		"GATEWAY_CREDENTIAL_REVOKED":
		return string(telemetryapi.AvailabilityReasonCodeSourceUnavailable), nil
	case string(telemetryapi.AvailabilityReasonCodeObservationCoverageGap),
		string(telemetryapi.AvailabilityReasonCodePolicyUnavailable),
		string(telemetryapi.AvailabilityReasonCodeOwnerDependencyUnavailable):
		return code, nil
	default:
		return "", fmt.Errorf("%w: unsupported reasonCode %q", ErrInvalidCoverageReport, raw)
	}
}

func canonicalDeviceIDs(raw []string) ([]string, error) {
	seen := make(map[string]struct{}, len(raw))
	ids := make([]string, 0, len(raw))
	for _, entry := range raw {
		id := strings.ToLower(strings.TrimSpace(entry))
		if !uuidV7Pattern.MatchString(id) {
			return nil, fmt.Errorf("deviceId %q must be UUIDv7", entry)
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

func findExistingTelemetryDevices(ctx context.Context, tx pgx.Tx, deviceIDs []string) ([]string, error) {
	if len(deviceIDs) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
SELECT device_id::text
FROM telemetry_runtime.devices
WHERE device_id = ANY($1::uuid[])
ORDER BY device_id
`, deviceIDs)
	if err != nil {
		return nil, fmt.Errorf("query existing telemetry devices: %w", err)
	}
	defer rows.Close()
	existing := make([]string, 0, len(deviceIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan existing telemetry device: %w", err)
		}
		existing = append(existing, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing telemetry devices: %w", err)
	}
	return existing, nil
}

func applyObservationCoverage(
	ctx context.Context,
	tx pgx.Tx,
	existingLockedDeviceIDs []string,
	available bool,
	reasonCode *string,
	observedAt time.Time,
	sourceRevision int64,
) ([]string, error) {
	if len(existingLockedDeviceIDs) == 0 {
		return nil, nil
	}
	if sourceRevision < 1 {
		sourceRevision = 1
	}
	observedAt = observedAt.UTC()
	var continuousSince any
	if available {
		continuousSince = observedAt
	}
	changed := make([]string, 0, len(existingLockedDeviceIDs))
	for _, deviceID := range existingLockedDeviceIDs {
		var updatedID string
		err := tx.QueryRow(ctx, `
INSERT INTO telemetry_runtime.observation_coverage (
  device_id, available, continuous_since, reason_code, source_revision, updated_at
) VALUES ($1::uuid, $2, $3, $4, $5, $6)
ON CONFLICT (device_id) DO UPDATE SET
  available = EXCLUDED.available,
  continuous_since = CASE
    WHEN EXCLUDED.available THEN COALESCE(telemetry_runtime.observation_coverage.continuous_since, EXCLUDED.continuous_since)
    ELSE NULL
  END,
  reason_code = EXCLUDED.reason_code,
  source_revision = GREATEST(telemetry_runtime.observation_coverage.source_revision + 1, EXCLUDED.source_revision),
  updated_at = EXCLUDED.updated_at
WHERE telemetry_runtime.observation_coverage.updated_at <= EXCLUDED.updated_at
  AND (telemetry_runtime.observation_coverage.available, telemetry_runtime.observation_coverage.reason_code)
  IS DISTINCT FROM (EXCLUDED.available, EXCLUDED.reason_code)
RETURNING device_id::text
`, deviceID, available, continuousSince, reasonCode, sourceRevision, observedAt).Scan(&updatedID)
		if err == nil {
			changed = append(changed, updatedID)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("upsert telemetry observation coverage: %w", err)
		}
	}
	return changed, nil
}

var _ CoverageAcceptor = (*PostgresStore)(nil)
