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

type SourceCoverageReport struct {
	SourceID       string    `json:"sourceId"`
	DeviceIDs      []string  `json:"deviceIds"`
	Available      bool      `json:"available"`
	ReasonCode     *string   `json:"reasonCode,omitempty"`
	ObservedAt     time.Time `json:"observedAt"`
	SourceRevision int64     `json:"sourceRevision"`
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
	existingIDs, err := findExistingTelemetryDevices(ctx, tx, report.DeviceIDs)
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
	deviceIDs, err := canonicalDeviceIDs(report.DeviceIDs)
	if err != nil {
		return SourceCoverageReport{}, fmt.Errorf("%w: %v", ErrInvalidCoverageReport, err)
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
		Available:      report.Available,
		ReasonCode:     reasonCode,
		ObservedAt:     report.ObservedAt.UTC(),
		SourceRevision: report.SourceRevision,
	}, nil
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
