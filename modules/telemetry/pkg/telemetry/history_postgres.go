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
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
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

func encodeHistoryOutboxPayload(observationID string, candidate ObservationCandidate, decision ObservationDecision, sourcePayloadSHA string) ([]byte, string, error) {
	observation, err := buildHistoryObservation(observationID, candidate, decision, sourcePayloadSHA)
	if err != nil {
		return nil, "", err
	}
	return encodeHistoryObservation(observation)
}

func buildHistoryObservation(observationID string, candidate ObservationCandidate, decision ObservationDecision, sourcePayloadSHA string) (HistoryObservation, error) {
	observation := HistoryObservation{
		ObservationID: observationID, SourceID: candidate.SourceID,
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
	if decision.Status != ObservationAccepted && (decision.Status != ObservationOutOfOrder || decision.PointID == "") {
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

type assignedHistoryBatch struct {
	batchID     string
	leaseID     string
	sequence    uint64
	state       string
	leasedUntil *time.Time
	availableAt time.Time
}

func (repository *HistoryPostgresRepository) retryAttempts() int {
	if repository == nil || repository.pool == nil {
		return 3
	}
	var minConns int32
	if cfg := repository.pool.Config(); cfg != nil {
		minConns = cfg.MinConns
	}
	return retryBudgetAttempts(repository.pool.Stat().TotalConns(), minConns)
}

func (repository *HistoryPostgresRepository) ClaimHistoryBatch(ctx context.Context, limit int, now time.Time, leaseFor time.Duration) (HistoryBatch, error) {
	return repository.ClaimPipelinedHistoryBatch(ctx, limit, now, leaseFor, nil, 1)
}

func (repository *HistoryPostgresRepository) ClaimPipelinedHistoryBatch(ctx context.Context, maxEvents int, now time.Time, leaseFor time.Duration, activeLeaseIDs []string, maxInFlight int) (HistoryBatch, error) {
	if repository == nil || repository.pool == nil {
		return HistoryBatch{}, errors.New("telemetry history repository is closed")
	}
	if maxEvents < 1 || maxEvents > maxHistoryBatchRows || now.IsZero() || leaseFor < time.Second || leaseFor > 10*time.Minute || maxInFlight < 1 {
		return HistoryBatch{}, errors.New("telemetry history claim parameters are invalid")
	}
	for attempt := 0; attempt < repository.retryAttempts(); attempt++ {
		batch, err := repository.claimPipelinedHistoryBatchOnce(ctx, maxEvents, now, leaseFor, activeLeaseIDs, maxInFlight)
		if err == nil || ctx.Err() != nil || !retryableTelemetryTransaction(err) {
			return batch, err
		}
	}
	return HistoryBatch{}, errors.New("telemetry history claim transaction retry budget exhausted")
}

func (repository *HistoryPostgresRepository) claimPipelinedHistoryBatchOnce(ctx context.Context, limit int, now time.Time, leaseFor time.Duration, activeLeaseIDs []string, maxInFlight int) (HistoryBatch, error) {
	nowUTC := now.UTC()
	leaseID, err := repository.newLeaseID(nowUTC)
	if err != nil {
		return HistoryBatch{}, fmt.Errorf("generate telemetry history lease ID: %w", err)
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return HistoryBatch{}, fmt.Errorf("begin telemetry history claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('telemetry_history_batch', 0))`); err != nil {
		return HistoryBatch{}, fmt.Errorf("serialize telemetry history batch claim: %w", err)
	}

	batchRows, err := tx.Query(ctx, `
SELECT batch_id::text,
       COALESCE(MAX(lease_id::text) FILTER (WHERE delivery_state = 'IN_FLIGHT'), '') AS lease_id,
       batch_sequence,
       CASE WHEN bool_or(delivery_state = 'IN_FLIGHT') THEN 'IN_FLIGHT' ELSE 'PENDING' END AS delivery_state,
       MAX(leased_until) FILTER (WHERE delivery_state = 'IN_FLIGHT') AS leased_until,
       MIN(available_at) AS available_at
FROM telemetry_runtime.telemetry_history_outbox
WHERE batch_id IS NOT NULL AND delivery_state IN ('PENDING', 'IN_FLIGHT')
GROUP BY batch_id, batch_sequence
ORDER BY batch_sequence ASC, batch_id ASC
`)
	if err != nil {
		return HistoryBatch{}, fmt.Errorf("inspect telemetry history batch: %w", err)
	}
	var existingBatches []assignedHistoryBatch
	for batchRows.Next() {
		var item assignedHistoryBatch
		if scanErr := batchRows.Scan(&item.batchID, &item.leaseID, &item.sequence, &item.state, &item.leasedUntil, &item.availableAt); scanErr != nil {
			batchRows.Close()
			return HistoryBatch{}, fmt.Errorf("scan telemetry history batch: %w", scanErr)
		}
		existingBatches = append(existingBatches, item)
	}
	if rowsErr := batchRows.Err(); rowsErr != nil {
		batchRows.Close()
		return HistoryBatch{}, fmt.Errorf("iterate telemetry history batches: %w", rowsErr)
	}
	batchRows.Close()

	owned := make(map[string]struct{}, len(activeLeaseIDs))
	for _, id := range activeLeaseIDs {
		if id != "" {
			owned[id] = struct{}{}
		}
	}

	activeOwned := 0
	var batchID string
	var sequence uint64
	newBatch := true
	for _, item := range existingBatches {
		if item.state == "IN_FLIGHT" {
			if item.leaseID != "" {
				if _, isOwned := owned[item.leaseID]; isOwned {
					activeOwned++
					if activeOwned >= maxInFlight {
						if commitErr := tx.Commit(ctx); commitErr != nil {
							return HistoryBatch{}, fmt.Errorf("commit busy telemetry history claim: %w", commitErr)
						}
						return HistoryBatch{}, nil
					}
					continue
				}
			}
			if item.leasedUntil != nil && item.leasedUntil.After(nowUTC) {
				if commitErr := tx.Commit(ctx); commitErr != nil {
					return HistoryBatch{}, fmt.Errorf("commit blocked telemetry history claim: %w", commitErr)
				}
				return HistoryBatch{}, nil
			}
			batchID = item.batchID
			sequence = item.sequence
			newBatch = false
			break
		}
		if item.state == "PENDING" && item.availableAt.After(nowUTC) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return HistoryBatch{}, fmt.Errorf("commit blocked telemetry history claim: %w", commitErr)
			}
			return HistoryBatch{}, nil
		}
		batchID = item.batchID
		sequence = item.sequence
		newBatch = false
		break
	}

	query := `SELECT event_id::text, payload, attempts
FROM telemetry_runtime.telemetry_history_outbox
WHERE batch_id = $1::uuid AND delivery_state IN ('PENDING','IN_FLIGHT')
ORDER BY event_id FOR UPDATE`
	args := []any{batchID}
	if newBatch {
		query = `SELECT event_id::text, payload, attempts
FROM telemetry_runtime.telemetry_history_outbox
WHERE batch_id IS NULL AND delivery_state = 'PENDING' AND available_at <= $1
ORDER BY available_at, event_id LIMIT $2 FOR UPDATE SKIP LOCKED`
		args = []any{nowUTC, limit}
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return HistoryBatch{}, fmt.Errorf("claim telemetry history rows: %w", err)
	}
	observations := make([]HistoryObservation, 0, limit)
	eventIDs := make([]string, 0, limit)
	invalidByCode := make(map[string][]string)
	batchBytes := 0
	maxAttempts := 0
	for rows.Next() {
		var eventID string
		var payload []byte
		var attempts int
		if err := rows.Scan(&eventID, &payload, &attempts); err != nil {
			rows.Close()
			return HistoryBatch{}, fmt.Errorf("scan telemetry history row: %w", err)
		}
		observation, rowBytes, failureCode := validateClaimedHistoryRow(eventID, payload)
		if failureCode != "" {
			invalidByCode[failureCode] = append(invalidByCode[failureCode], eventID)
			continue
		}
		if len(observations) > 0 && batchBytes+rowBytes > maxHistoryBatchBytes {
			break
		}
		batchBytes += rowBytes
		observations = append(observations, observation)
		eventIDs = append(eventIDs, eventID)
		if nextAttempts := attempts + 1; nextAttempts > maxAttempts {
			maxAttempts = nextAttempts
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return HistoryBatch{}, fmt.Errorf("iterate telemetry history rows: %w", err)
	}
	rows.Close()
	for code, invalidIDs := range invalidByCode {
		if _, err := tx.Exec(ctx, `UPDATE telemetry_runtime.telemetry_history_outbox
SET batch_id = NULL, batch_sequence = NULL, delivery_state = 'DEAD',
    lease_id = NULL, leased_until = NULL, published_at = NULL,
    dead_lettered_at = $2, last_error_code = $3
WHERE event_id = ANY($1::uuid[])`, invalidIDs, nowUTC, code); err != nil {
			return HistoryBatch{}, fmt.Errorf("dead-letter invalid telemetry history rows: %w", err)
		}
	}
	if len(observations) == 0 {
		if len(invalidByCode) > 0 {
			if err := tx.Commit(ctx); err != nil {
				return HistoryBatch{}, fmt.Errorf("commit dead-lettered telemetry history claim: %w", err)
			}
		}
		return HistoryBatch{}, nil
	}
	if newBatch {
		batchID = eventIDs[0]
		if err := tx.QueryRow(ctx, `SELECT nextval('telemetry_runtime.telemetry_history_batch_sequence')`).Scan(&sequence); err != nil {
			return HistoryBatch{}, fmt.Errorf("allocate telemetry history sequence: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE telemetry_runtime.telemetry_history_outbox
SET batch_id = $1::uuid, batch_sequence = $5, delivery_state = 'IN_FLIGHT', lease_id = $2::uuid, leased_until = $3,
    attempts = attempts + 1, last_error_code = NULL
WHERE event_id = ANY($4::uuid[])`, batchID, leaseID, nowUTC.Add(leaseFor), eventIDs, sequence); err != nil {
		return HistoryBatch{}, fmt.Errorf("lease telemetry history batch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return HistoryBatch{}, fmt.Errorf("commit telemetry history claim: %w", err)
	}
	return HistoryBatch{LeaseID: leaseID, BatchID: batchID, Sequence: sequence, MaxAttempts: maxAttempts, Observations: observations}, nil
}

func validateClaimedHistoryRow(eventID string, payload []byte) (HistoryObservation, int, string) {
	var observation HistoryObservation
	if err := json.Unmarshal(payload, &observation); err != nil {
		return HistoryObservation{}, 0, historyPayloadInvalidCode
	}
	if observation.ObservationID != eventID || validateHistoryObservation(observation) != nil {
		return HistoryObservation{}, 0, historyPayloadInvalidCode
	}
	encoded, _, err := encodeHistoryObservation(observation)
	if err != nil {
		return HistoryObservation{}, 0, historyPayloadInvalidCode
	}
	rowBytes := len(encoded) + 1
	if rowBytes > maxHistoryBatchBytes {
		return HistoryObservation{}, 0, historyRowTooLargeCode
	}
	return observation, rowBytes, ""
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

func (repository *HistoryPostgresRepository) MarkHistoryEventsPublished(ctx context.Context, leaseID string, eventIDs []string, publishedAt time.Time) error {
	if repository == nil || repository.pool == nil {
		return errors.New("telemetry history repository is closed")
	}
	if len(eventIDs) == 0 {
		return nil
	}
	command, err := repository.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_history_outbox
SET delivery_state = 'PUBLISHED', published_at = $3, last_error_code = NULL,
    lease_id = NULL, leased_until = NULL
WHERE lease_id = $1::uuid AND delivery_state = 'IN_FLIGHT' AND event_id = ANY($2::uuid[])
`, leaseID, eventIDs, publishedAt.UTC())
	if err != nil {
		return fmt.Errorf("mark telemetry history events published: %w", err)
	}
	if command.RowsAffected() == 0 {
		return errors.New("telemetry history lease was not found")
	}
	return nil
}

func (repository *HistoryPostgresRepository) DeadLetterHistoryEvents(ctx context.Context, leaseID string, eventIDs []string, errorCode string, deadLetteredAt time.Time) error {
	if repository == nil || repository.pool == nil {
		return errors.New("telemetry history repository is closed")
	}
	if errorCode == "" || len(errorCode) > 128 {
		return errors.New("telemetry history dead-letter parameters are invalid")
	}
	if len(eventIDs) == 0 {
		return nil
	}
	command, err := repository.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_history_outbox
SET batch_id = NULL, batch_sequence = NULL, delivery_state = 'DEAD',
    lease_id = NULL, leased_until = NULL, published_at = NULL,
    dead_lettered_at = $4, last_error_code = $3
WHERE lease_id = $1::uuid AND delivery_state = 'IN_FLIGHT' AND event_id = ANY($2::uuid[])
`, leaseID, eventIDs, errorCode, deadLetteredAt.UTC())
	if err != nil {
		return fmt.Errorf("dead-letter telemetry history events: %w", err)
	}
	if command.RowsAffected() == 0 {
		return errors.New("telemetry history lease was not found")
	}
	return nil
}

func (repository *HistoryPostgresRepository) RetryHistoryBatch(ctx context.Context, leaseID string, retryAt time.Time, errorCode string) error {
	if repository == nil || repository.pool == nil {
		return errors.New("telemetry history repository is closed")
	}
	if errorCode == "" || len(errorCode) > 128 {
		return errors.New("telemetry history retry parameters are invalid")
	}
	command, err := repository.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_history_outbox
SET delivery_state = 'PENDING', available_at = $2, last_error_code = $3, lease_id = NULL, leased_until = NULL
WHERE lease_id = $1::uuid AND delivery_state = 'IN_FLIGHT'
`, leaseID, retryAt.UTC(), errorCode)
	if err != nil {
		return fmt.Errorf("retry telemetry history batch: %w", err)
	}
	if command.RowsAffected() == 0 {
		return errors.New("telemetry history lease was not found")
	}
	return nil
}

func (repository *HistoryPostgresRepository) ReleaseHistoryBatchLease(ctx context.Context, leaseID string, now time.Time) error {
	if repository == nil || repository.pool == nil {
		return errors.New("telemetry history repository is closed")
	}
	if leaseID == "" || now.IsZero() {
		return errors.New("telemetry history lease release parameters are invalid")
	}
	command, err := repository.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_history_outbox
SET delivery_state = 'PENDING',
    available_at = $2,
    last_error_code = NULL,
    lease_id = NULL,
    leased_until = NULL,
    attempts = GREATEST(attempts - 1, 0)
WHERE lease_id = $1::uuid AND delivery_state = 'IN_FLIGHT'
`, leaseID, now.UTC())
	if err != nil {
		return fmt.Errorf("release telemetry history batch lease: %w", err)
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

func (repository *HistoryPostgresRepository) HistoryOutboxDepths(ctx context.Context) (pending, publishing, deadLetter int, err error) {
	if repository == nil || repository.pool == nil {
		return 0, 0, 0, errors.New("telemetry history repository is closed")
	}
	err = repository.pool.QueryRow(ctx, `
SELECT
  (SELECT count(*)::int FROM telemetry_runtime.telemetry_history_outbox WHERE delivery_state = 'PENDING'),
  (SELECT count(*)::int FROM telemetry_runtime.telemetry_history_outbox WHERE delivery_state = 'IN_FLIGHT'),
  (SELECT count(*)::int FROM telemetry_runtime.telemetry_history_outbox WHERE delivery_state = 'DEAD')
`).Scan(&pending, &publishing, &deadLetter)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("query telemetry history outbox depths: %w", err)
	}
	return pending, publishing, deadLetter, nil
}

var (
	_ HistoryRepository          = (*HistoryPostgresRepository)(nil)
	_ PipelinedHistoryRepository = (*HistoryPostgresRepository)(nil)
	_ HistoryOutboxDepthProvider = (*HistoryPostgresRepository)(nil)
)
