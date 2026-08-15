package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HistoryPostgresRepository struct {
	pool       *pgxpool.Pool
	newLeaseID EventIDGenerator
}

func OpenHistoryPostgresRepository(ctx context.Context, databaseURL string) (*HistoryPostgresRepository, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse telemetry history database URL: %w", err)
	}
	if config.ConnConfig.User != "s2_telemetry_history_service" {
		return nil, errors.New("telemetry history database identity must be s2_telemetry_history_service")
	}
	config.MaxConns = 4
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE s2_telemetry_history`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open telemetry history database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping telemetry history database: %w", err)
	}
	return &HistoryPostgresRepository{pool: pool, newLeaseID: newUUIDv7}, nil
}

func NewHistoryPostgresRepository(pool *pgxpool.Pool, generator EventIDGenerator) *HistoryPostgresRepository {
	if generator == nil {
		generator = newUUIDv7
	}
	return &HistoryPostgresRepository{pool: pool, newLeaseID: generator}
}

func (repository *HistoryPostgresRepository) Close() {
	if repository != nil && repository.pool != nil {
		repository.pool.Close()
	}
}

func insertHistoryOutboxIntent(ctx context.Context, tx pgx.Tx, observationID string, candidate ObservationCandidate, decision ObservationDecision, sourcePayloadSHA string) error {
	observation, err := buildHistoryObservation(observationID, candidate, decision, sourcePayloadSHA)
	if err != nil {
		return err
	}
	payload, digest, err := encodeHistoryObservation(observation)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO telemetry_runtime.telemetry_history_outbox (
  event_id, payload, outbox_payload_sha256, delivery_state, available_at,
  attempts, last_error_code, lease_id, leased_until, published_at, created_at
) VALUES ($1::uuid, $2::jsonb, $3, 'PENDING', $4, 0, NULL, NULL, NULL, NULL, $4)
`, observationID, payload, digest, candidate.ReceivedAt)
	if err != nil {
		return fmt.Errorf("persist telemetry history outbox intent: %w", err)
	}
	return nil
}

func buildHistoryObservation(observationID string, candidate ObservationCandidate, decision ObservationDecision, sourcePayloadSHA string) (HistoryObservation, error) {
	observation := HistoryObservation{
		ObservationID: observationID, IntegrationInstanceID: candidate.IntegrationInstanceID,
		SourceEventID: candidate.Position.EventID, SourcePartition: candidate.Position.Partition,
		SourceOffset: candidate.Position.Offset, SourcePath: string(candidate.SourcePath),
		TelemetryKey: candidate.TelemetryKey, ValueType: stringPointer(candidate.ValueType), Unit: copyStringPointer(candidate.Unit),
		SampledAt: candidate.SampledAt.UTC(), ReceivedAt: candidate.ReceivedAt.UTC(),
		AcceptanceStatus: string(decision.Status), Quality: string(decision.Quality),
		QualityReasons: qualityReasonStrings(decision.QualityReasons), PayloadSHA256: sourcePayloadSHA,
	}
	if decision.TenantID != "" {
		observation.TenantID = stringPointer(decision.TenantID)
	}
	if decision.SiteID != "" {
		observation.SiteID = stringPointer(decision.SiteID)
	}
	if decision.DeviceID != "" {
		observation.DeviceID = stringPointer(decision.DeviceID)
	}
	if decision.PointID != "" {
		observation.PointID = stringPointer(decision.PointID)
	}
	if decision.PointType != "" {
		observation.PointType = stringPointer(decision.PointType)
	}
	if decision.PointRevision > 0 {
		revision := decision.PointRevision
		observation.PointRevision = &revision
	}
	if decision.CounterDecreaseMode != "" {
		observation.CounterDecreaseMode = stringPointer(decision.CounterDecreaseMode)
	}
	if decision.CounterRolloverModulus != nil {
		modulus := *decision.CounterRolloverModulus
		observation.CounterRolloverModulus = &modulus
	}
	if decision.SensorID != "" {
		observation.SensorID = stringPointer(decision.SensorID)
	}
	if decision.Status != ObservationAccepted && !(decision.Status == ObservationOutOfOrder && decision.PointID != "") {
		return observation, nil
	}
	compact := json.RawMessage(candidate.Value)
	if len(compact) == 0 || !json.Valid(compact) {
		return HistoryObservation{}, errors.New("canonical telemetry history value is invalid JSON")
	}
	valueJSON := string(compact)
	observation.ValueJSON = &valueJSON
	switch candidate.ValueType {
	case "NUMBER":
		var value float64
		if err := json.Unmarshal(compact, &value); err != nil {
			return HistoryObservation{}, fmt.Errorf("decode telemetry history number: %w", err)
		}
		observation.ValueNumber = &value
	case "STRING":
		var value string
		if err := json.Unmarshal(compact, &value); err != nil {
			return HistoryObservation{}, fmt.Errorf("decode telemetry history string: %w", err)
		}
		observation.ValueString = &value
	case "BOOLEAN":
		var value bool
		if err := json.Unmarshal(compact, &value); err != nil {
			return HistoryObservation{}, fmt.Errorf("decode telemetry history boolean: %w", err)
		}
		encoded := uint8(0)
		if value {
			encoded = 1
		}
		observation.ValueBoolean = &encoded
	case "JSON":
	default:
		return HistoryObservation{}, errors.New("accepted telemetry history value type is invalid")
	}
	return observation, nil
}

func encodeHistoryObservation(observation HistoryObservation) ([]byte, string, error) {
	payload, err := json.Marshal(observation)
	if err != nil {
		return nil, "", fmt.Errorf("encode telemetry history observation: %w", err)
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func (repository *HistoryPostgresRepository) ClaimHistoryBatch(ctx context.Context, limit int, now time.Time, leaseFor time.Duration) (HistoryBatch, error) {
	if repository == nil || repository.pool == nil {
		return HistoryBatch{}, errors.New("telemetry history repository is closed")
	}
	if limit < 1 || limit > 4096 || now.IsZero() || leaseFor < time.Second || leaseFor > 10*time.Minute {
		return HistoryBatch{}, errors.New("telemetry history claim parameters are invalid")
	}
	leaseID, err := repository.newLeaseID(now.UTC())
	if err != nil {
		return HistoryBatch{}, fmt.Errorf("generate telemetry history lease ID: %w", err)
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return HistoryBatch{}, fmt.Errorf("begin telemetry history claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
WITH candidates AS (
  SELECT event_id
  FROM telemetry_runtime.telemetry_history_outbox
  WHERE (delivery_state = 'PENDING' AND available_at <= $1)
     OR (delivery_state = 'IN_FLIGHT' AND leased_until <= $1)
  ORDER BY COALESCE(leased_until, available_at), event_id
  FOR UPDATE SKIP LOCKED
  LIMIT $2
)
UPDATE telemetry_runtime.telemetry_history_outbox outbox
SET delivery_state = 'IN_FLIGHT', lease_id = $3::uuid, leased_until = $4,
    attempts = outbox.attempts + 1, last_error_code = NULL
FROM candidates
WHERE outbox.event_id = candidates.event_id
RETURNING outbox.event_id::text, outbox.payload, outbox.outbox_payload_sha256
`, now.UTC(), limit, leaseID, now.UTC().Add(leaseFor))
	if err != nil {
		return HistoryBatch{}, fmt.Errorf("claim telemetry history rows: %w", err)
	}
	observations := make([]HistoryObservation, 0, limit)
	for rows.Next() {
		var eventID, expectedDigest string
		var payload []byte
		if err := rows.Scan(&eventID, &payload, &expectedDigest); err != nil {
			rows.Close()
			return HistoryBatch{}, fmt.Errorf("scan telemetry history row: %w", err)
		}
		var observation HistoryObservation
		if err := json.Unmarshal(payload, &observation); err != nil {
			rows.Close()
			return HistoryBatch{}, fmt.Errorf("decode telemetry history row %s: %w", eventID, err)
		}
		_, actualDigest, err := encodeHistoryObservation(observation)
		if err != nil {
			rows.Close()
			return HistoryBatch{}, err
		}
		if observation.ObservationID != eventID || actualDigest != expectedDigest {
			rows.Close()
			return HistoryBatch{}, errors.New("telemetry history outbox payload is inconsistent")
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return HistoryBatch{}, fmt.Errorf("iterate telemetry history rows: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return HistoryBatch{}, fmt.Errorf("commit telemetry history claim: %w", err)
	}
	if len(observations) == 0 {
		return HistoryBatch{}, nil
	}
	return HistoryBatch{LeaseID: leaseID, Observations: observations}, nil
}

func (repository *HistoryPostgresRepository) MarkHistoryBatchPublished(ctx context.Context, leaseID string, publishedAt time.Time) error {
	if repository == nil || repository.pool == nil {
		return errors.New("telemetry history repository is closed")
	}
	command, err := repository.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_history_outbox
SET delivery_state = 'PUBLISHED', published_at = $2, last_error_code = NULL,
    lease_id = NULL, leased_until = NULL
WHERE lease_id = $1::uuid AND delivery_state = 'IN_FLIGHT'
`, leaseID, publishedAt.UTC())
	if err != nil {
		return fmt.Errorf("mark telemetry history batch published: %w", err)
	}
	if command.RowsAffected() == 0 {
		return errors.New("telemetry history lease was not found")
	}
	return nil
}

func (repository *HistoryPostgresRepository) RetryHistoryBatch(ctx context.Context, leaseID string, retryAt time.Time, errorCode string, maxAttempts int) error {
	if repository == nil || repository.pool == nil {
		return errors.New("telemetry history repository is closed")
	}
	if errorCode == "" || len(errorCode) > 128 || maxAttempts < 1 || maxAttempts > 100 {
		return errors.New("telemetry history retry parameters are invalid")
	}
	command, err := repository.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_history_outbox
SET delivery_state = CASE WHEN attempts >= $4 THEN 'DEAD' ELSE 'PENDING' END,
    available_at = $2, last_error_code = $3, lease_id = NULL, leased_until = NULL
WHERE lease_id = $1::uuid AND delivery_state = 'IN_FLIGHT'
`, leaseID, retryAt.UTC(), errorCode, maxAttempts)
	if err != nil {
		return fmt.Errorf("retry telemetry history batch: %w", err)
	}
	if command.RowsAffected() == 0 {
		return errors.New("telemetry history lease was not found")
	}
	return nil
}

func stringPointer(value string) *string {
	copy := value
	return &copy
}

func copyStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	return stringPointer(*value)
}

var _ HistoryRepository = (*HistoryPostgresRepository)(nil)
