package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

type CoverageReport struct {
	IntegrationInstanceID string
	ExternalEntityType    string
	ExternalID            string
	Available             bool
	ContinuousSince       *time.Time
	Reason                telemetryapi.AvailabilityReasonCode
	SourceRevision        int64
	ReportedAt            time.Time
}

type CoverageReceipt struct {
	Status           string           `json:"status"`
	EvidenceID       string           `json:"evidenceId,omitempty"`
	QuarantineReason QuarantineReason `json:"quarantineReason,omitempty"`
	DeviceID         string           `json:"deviceId,omitempty"`
	BusinessRevision int64            `json:"businessRevision,omitempty"`
	StateChanged     bool             `json:"stateChanged"`
}

type CoverageReporter interface {
	ReportCoverage(context.Context, CoverageReport) (CoverageReceipt, error)
}

func (store *PostgresStore) ReportCoverage(ctx context.Context, report CoverageReport) (CoverageReceipt, error) {
	if store == nil || store.pool == nil {
		return CoverageReceipt{}, errors.New("telemetry runtime store is closed")
	}
	if err := validateCoverageReport(report); err != nil {
		return CoverageReceipt{}, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		receipt, err := store.reportCoverageOnce(ctx, report)
		if err == nil || !retryableTelemetryTransaction(err) {
			return receipt, err
		}
	}
	return CoverageReceipt{}, errors.New("telemetry coverage transaction retry budget exhausted")
}

func (store *PostgresStore) reportCoverageOnce(ctx context.Context, report CoverageReport) (CoverageReceipt, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CoverageReceipt{}, fmt.Errorf("begin telemetry coverage transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return CoverageReceipt{}, fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, report.IntegrationInstanceID+":"+report.ExternalEntityType+":"+report.ExternalID+":coverage"); err != nil {
		return CoverageReceipt{}, fmt.Errorf("lock telemetry coverage report: %w", err)
	}
	candidate := ObservationCandidate{
		IntegrationInstanceID: report.IntegrationInstanceID,
		ExternalEntityType:    report.ExternalEntityType,
		ExternalID:            report.ExternalID,
		ReceivedAt:            report.ReportedAt,
	}
	bindings, err := queryRuntimeBindings(ctx, tx, candidate.IntegrationInstanceID, candidate.ExternalEntityType, candidate.ExternalID)
	if err != nil {
		return CoverageReceipt{}, err
	}
	binding, quarantine := resolveRuntimeBinding(candidate, bindings)
	if quarantine != "" {
		evidenceID, err := store.newEventID(report.ReportedAt)
		if err != nil {
			return CoverageReceipt{}, fmt.Errorf("generate telemetry coverage quarantine ID: %w", err)
		}
		evidenceID, err = insertCoverageQuarantine(ctx, tx, evidenceID, report, quarantine)
		if err != nil {
			return CoverageReceipt{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CoverageReceipt{}, fmt.Errorf("commit telemetry coverage quarantine: %w", err)
		}
		return CoverageReceipt{Status: "QUARANTINED", EvidenceID: evidenceID, QuarantineReason: quarantine}, nil
	}

	var currentRevision int64
	err = tx.QueryRow(ctx, `
SELECT source_revision
FROM telemetry_runtime.observation_coverage
WHERE device_id = $1::uuid
FOR UPDATE
`, binding.DeviceID).Scan(&currentRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CoverageReceipt{}, fmt.Errorf("lock telemetry coverage state: %w", err)
	}
	if err == nil && report.SourceRevision <= currentRevision {
		status := "DUPLICATE"
		if report.SourceRevision < currentRevision {
			status = "OUT_OF_ORDER"
		}
		return CoverageReceipt{Status: status, DeviceID: binding.DeviceID}, nil
	}

	var continuousSince any
	var reason any
	if report.Available {
		continuousSince = report.ContinuousSince.UTC()
	} else {
		reason = string(report.Reason)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO telemetry_runtime.observation_coverage (
  device_id, available, continuous_since, reason_code, source_revision, updated_at
) VALUES ($1::uuid, $2, $3, $4, $5, $6)
ON CONFLICT (device_id) DO UPDATE SET
  available = EXCLUDED.available,
  continuous_since = EXCLUDED.continuous_since,
  reason_code = EXCLUDED.reason_code,
  source_revision = EXCLUDED.source_revision,
  updated_at = EXCLUDED.updated_at
`, binding.DeviceID, report.Available, continuousSince, reason, report.SourceRevision, report.ReportedAt)
	if err != nil {
		return CoverageReceipt{}, fmt.Errorf("persist telemetry coverage state: %w", err)
	}
	commit, err := store.evaluateAndPersistDevice(ctx, tx, binding.DeviceID, nil, report.ReportedAt)
	if err != nil {
		return CoverageReceipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CoverageReceipt{}, fmt.Errorf("commit telemetry coverage transaction: %w", err)
	}
	return CoverageReceipt{
		Status: "APPLIED", DeviceID: binding.DeviceID,
		BusinessRevision: int64(commit.Snapshot.BusinessRevision), StateChanged: commit.StateChanged,
	}, nil
}

func insertCoverageQuarantine(ctx context.Context, tx pgx.Tx, evidenceID string, report CoverageReport, quarantine QuarantineReason) (string, error) {
	evidence, err := json.Marshal(map[string]any{
		"schemaVersion":  1,
		"kind":           "OBSERVATION_COVERAGE_REPORT",
		"available":      report.Available,
		"sourceRevision": report.SourceRevision,
		"reason":         report.Reason,
		"reportedAt":     report.ReportedAt.UTC(),
	})
	if err != nil {
		return "", fmt.Errorf("encode telemetry coverage quarantine evidence: %w", err)
	}
	var persistedID string
	err = tx.QueryRow(ctx, `
WITH inserted AS (
  INSERT INTO telemetry_runtime.ingest_quarantine (
    quarantine_id, integration_instance_id, external_entity_type, external_id,
    device_id, telemetry_key, reason_code, evidence, detected_at, resolved_at, resolution
  ) VALUES ($1::uuid, $2::uuid, $3, $4, NULL, NULL, $5, $6::jsonb, $7, NULL, NULL)
  ON CONFLICT DO NOTHING
  RETURNING quarantine_id::text
)
SELECT COALESCE(
  (SELECT quarantine_id FROM inserted),
  (SELECT quarantine_id::text
   FROM telemetry_runtime.ingest_quarantine
   WHERE integration_instance_id = $2::uuid
     AND external_entity_type = $3
     AND external_id = $4
     AND reason_code = $5
     AND evidence ->> 'kind' = 'OBSERVATION_COVERAGE_REPORT'
     AND evidence ->> 'sourceRevision' = $8
     AND resolved_at IS NULL
   ORDER BY detected_at
   LIMIT 1)
)
`, evidenceID, report.IntegrationInstanceID, report.ExternalEntityType, report.ExternalID,
		string(quarantine), evidence, report.ReportedAt, fmt.Sprint(report.SourceRevision)).Scan(&persistedID)
	if err != nil {
		return "", fmt.Errorf("persist telemetry coverage quarantine evidence: %w", err)
	}
	return persistedID, nil
}

func validateCoverageReport(report CoverageReport) error {
	if !uuidV7Pattern.MatchString(report.IntegrationInstanceID) || (report.ExternalEntityType != "DEVICE" && report.ExternalEntityType != "ASSET") || len(report.ExternalID) < 1 || len(report.ExternalID) > 512 {
		return errors.New("telemetry coverage source identity is invalid")
	}
	if report.SourceRevision < 1 || report.ReportedAt.IsZero() {
		return errors.New("telemetry coverage revision is invalid")
	}
	if report.Available {
		if report.ContinuousSince == nil || report.ContinuousSince.IsZero() || report.ContinuousSince.After(report.ReportedAt) || report.Reason != "" {
			return errors.New("available telemetry coverage report is invalid")
		}
		return nil
	}
	if report.ContinuousSince != nil {
		return errors.New("unavailable telemetry coverage report is invalid")
	}
	switch report.Reason {
	case telemetryapi.AvailabilityReasonCodeSourceUnavailable,
		telemetryapi.AvailabilityReasonCodeObservationCoverageGap,
		telemetryapi.AvailabilityReasonCodePolicyUnavailable,
		telemetryapi.AvailabilityReasonCodeOwnerDependencyUnavailable:
		return nil
	default:
		return errors.New("telemetry coverage reason is invalid")
	}
}

func queryRuntimeBindings(ctx context.Context, tx pgx.Tx, integrationInstanceID, externalEntityType, externalID string) ([]RuntimeBinding, error) {
	rows, err := tx.Query(ctx, `
SELECT tenant_id::text, device_id::text, site_id::text,
       integration_instance_id::text, external_entity_type, external_id, binding_status,
       valid_from, valid_to
FROM telemetry_runtime.registry_device_bindings
WHERE integration_instance_id = $1::uuid
  AND external_entity_type = $2
  AND external_id = $3
ORDER BY binding_revision DESC, device_id
LIMIT 4
`, integrationInstanceID, externalEntityType, externalID)
	if err != nil {
		return nil, fmt.Errorf("query telemetry runtime binding: %w", err)
	}
	defer rows.Close()
	bindings := make([]RuntimeBinding, 0, 2)
	for rows.Next() {
		var binding RuntimeBinding
		if err := rows.Scan(
			&binding.TenantID, &binding.DeviceID, &binding.SiteID,
			&binding.IntegrationInstanceID, &binding.ExternalEntityType, &binding.ExternalID, &binding.Status,
			&binding.ValidFrom, &binding.ValidTo,
		); err != nil {
			return nil, fmt.Errorf("scan telemetry runtime binding: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate telemetry runtime bindings: %w", err)
	}
	return bindings, nil
}

func queryRuntimePointBindings(ctx context.Context, tx pgx.Tx, tenantID, deviceID, telemetryKey string, sampledAt time.Time) ([]RuntimePointBinding, error) {
	rows, err := tx.Query(ctx, `
SELECT tenant_id::text, site_id::text,
       point_id::text, sensor_id::text, device_id::text, telemetry_key,
       point_type, value_type, unit, counter_decrease_mode, counter_rollover_modulus,
       binding_status, point_revision, valid_from, valid_to
FROM telemetry_runtime.registry_point_bindings
WHERE tenant_id = $1::uuid
  AND device_id = $2::uuid
  AND telemetry_key = $3
  AND valid_from <= $4
  AND (valid_to IS NULL OR $4 < valid_to)
ORDER BY point_revision DESC, projection_id
LIMIT 4
`, tenantID, deviceID, telemetryKey, sampledAt.UTC())
	if err != nil {
		return nil, fmt.Errorf("query telemetry runtime Point binding: %w", err)
	}
	defer rows.Close()
	bindings := make([]RuntimePointBinding, 0, 2)
	for rows.Next() {
		var binding RuntimePointBinding
		if err := rows.Scan(
			&binding.TenantID, &binding.SiteID,
			&binding.PointID, &binding.SensorID, &binding.DeviceID, &binding.TelemetryKey,
			&binding.PointType, &binding.ValueType, &binding.Unit, &binding.CounterDecreaseMode, &binding.CounterRolloverModulus,
			&binding.Status, &binding.PointRevision, &binding.ValidFrom, &binding.ValidTo,
		); err != nil {
			return nil, fmt.Errorf("scan telemetry runtime Point binding: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate telemetry runtime Point bindings: %w", err)
	}
	return bindings, nil
}

var _ CoverageReporter = (*PostgresStore)(nil)
