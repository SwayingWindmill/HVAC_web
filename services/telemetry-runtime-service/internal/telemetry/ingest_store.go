package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	uuidV7Pattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	telemetryKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
)

type ObservationReceipt struct {
	ObservationID    string             `json:"observationId,omitempty"`
	EvidenceID       string             `json:"evidenceId,omitempty"`
	Status           ObservationStatus  `json:"status"`
	Quality          ObservationQuality `json:"quality"`
	QualityReasons   []QualityReason    `json:"qualityReasons"`
	QuarantineReason QuarantineReason   `json:"quarantineReason,omitempty"`
	DeviceID         string             `json:"deviceId,omitempty"`
	BusinessRevision int64              `json:"businessRevision,omitempty"`
	StateChanged     bool               `json:"stateChanged"`
	PositionAdvanced bool               `json:"positionAdvanced"`
}

type ObservationAcceptor interface {
	AcceptObservation(context.Context, ObservationCandidate) (ObservationReceipt, error)
}

func (store *PostgresStore) AcceptObservation(ctx context.Context, candidate ObservationCandidate) (ObservationReceipt, error) {
	if store == nil || store.pool == nil {
		return ObservationReceipt{}, errors.New("telemetry runtime store is closed")
	}
	if err := validateObservationCandidate(candidate); err != nil {
		return ObservationReceipt{}, err
	}
	candidate.SampledAt = candidate.SampledAt.UTC()
	candidate.ReceivedAt = candidate.ReceivedAt.UTC()
	for attempt := 0; attempt < 3; attempt++ {
		receipt, err := store.acceptObservationOnce(ctx, candidate)
		if err == nil || !retryableTelemetryTransaction(err) {
			return receipt, err
		}
	}
	return ObservationReceipt{}, errors.New("telemetry observation transaction retry budget exhausted")
}

func (store *PostgresStore) acceptObservationOnce(ctx context.Context, candidate ObservationCandidate) (ObservationReceipt, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ObservationReceipt{}, fmt.Errorf("begin telemetry observation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return ObservationReceipt{}, fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, candidate.IntegrationInstanceID+":partition:"+candidate.Position.Partition); err != nil {
		return ObservationReceipt{}, fmt.Errorf("lock telemetry source partition: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, candidate.IntegrationInstanceID+":event:"+candidate.Position.EventID); err != nil {
		return ObservationReceipt{}, fmt.Errorf("lock telemetry source event: %w", err)
	}

	facts, existingObservationID, err := loadObservationFacts(ctx, tx, candidate)
	if err != nil {
		return ObservationReceipt{}, err
	}
	decision := EvaluateObservation(candidate, facts, candidate.ReceivedAt)
	payloadSHA, err := observationPayloadSHA(candidate)
	if err != nil {
		return ObservationReceipt{}, err
	}
	if !decision.AdvancePosition {
		evidenceID, err := store.newEventID(candidate.ReceivedAt)
		if err != nil {
			return ObservationReceipt{}, fmt.Errorf("generate telemetry delivery evidence ID: %w", err)
		}
		evidenceID, err = insertSourceDeliveryEvidence(ctx, tx, evidenceID, candidate, decision, payloadSHA)
		if err != nil {
			return ObservationReceipt{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ObservationReceipt{}, fmt.Errorf("commit telemetry delivery evidence: %w", err)
		}
		return ObservationReceipt{
			ObservationID: existingObservationID, EvidenceID: evidenceID,
			Status: decision.Status, Quality: decision.Quality, QualityReasons: decision.QualityReasons,
		}, nil
	}

	observationID, err := store.newEventID(candidate.ReceivedAt)
	if err != nil {
		return ObservationReceipt{}, fmt.Errorf("generate telemetry observation ID: %w", err)
	}
	if err := advanceSourcePosition(ctx, tx, candidate); err != nil {
		return ObservationReceipt{}, err
	}
	if err := insertSourceObservation(ctx, tx, observationID, candidate, decision, payloadSHA); err != nil {
		return ObservationReceipt{}, err
	}
	if decision.QuarantineReason != "" {
		if err := store.insertQuarantine(ctx, tx, candidate, decision, payloadSHA); err != nil {
			return ObservationReceipt{}, err
		}
	}
	if decision.ReplaceLatest {
		if err := upsertLatestObservation(ctx, tx, candidate, decision); err != nil {
			return ObservationReceipt{}, err
		}
	}
	if decision.EmitPresenceSignal {
		if err := store.insertSourcePresenceSignal(ctx, tx, candidate, decision); err != nil {
			return ObservationReceipt{}, err
		}
	}

	receipt := ObservationReceipt{
		ObservationID: observationID, Status: decision.Status, Quality: decision.Quality,
		QualityReasons: decision.QualityReasons, QuarantineReason: decision.QuarantineReason,
		DeviceID: decision.DeviceID, PositionAdvanced: true,
	}
	if decision.ReevaluateSnapshot {
		commit, err := store.evaluateAndPersistDevice(ctx, tx, decision.DeviceID, nil, candidate.ReceivedAt)
		if err != nil {
			return ObservationReceipt{}, err
		}
		receipt.BusinessRevision = int64(commit.Snapshot.BusinessRevision)
		receipt.StateChanged = commit.StateChanged
	}
	if err := tx.Commit(ctx); err != nil {
		return ObservationReceipt{}, fmt.Errorf("commit telemetry observation transaction: %w", err)
	}
	if receipt.Status == ObservationQuarantined {
		receipt.DeviceID = ""
	}
	return receipt, nil
}

func validateObservationCandidate(candidate ObservationCandidate) error {
	if !uuidV7Pattern.MatchString(candidate.IntegrationInstanceID) || !uuidV7Pattern.MatchString(candidate.Position.EventID) {
		return errors.New("telemetry source identity must be UUIDv7")
	}
	if !candidate.SourcePath.Valid() {
		return errors.New("telemetry source path is invalid")
	}
	if candidate.ExternalEntityType != "DEVICE" && candidate.ExternalEntityType != "ASSET" {
		return errors.New("telemetry external entity type is invalid")
	}
	if len(candidate.ExternalID) < 1 || len(candidate.ExternalID) > 512 || !telemetryKeyPattern.MatchString(candidate.TelemetryKey) {
		return errors.New("telemetry external identity or key is invalid")
	}
	if len(candidate.Position.Partition) < 1 || len(candidate.Position.Partition) > 256 || candidate.Position.Offset < 0 {
		return errors.New("telemetry source position is invalid")
	}
	if candidate.SampledAt.IsZero() || candidate.ReceivedAt.IsZero() || len(candidate.Value) == 0 || len(candidate.Value) > 64<<10 || !json.Valid(candidate.Value) {
		return errors.New("telemetry observation payload is invalid")
	}
	if candidate.ValueType != "NUMBER" && candidate.ValueType != "STRING" && candidate.ValueType != "BOOLEAN" && candidate.ValueType != "JSON" {
		return errors.New("telemetry observation valueType is invalid")
	}
	if candidate.Unit != nil && (len(*candidate.Unit) < 1 || len(*candidate.Unit) > 64) {
		return errors.New("telemetry observation unit is invalid")
	}
	return nil
}

func loadObservationFacts(ctx context.Context, tx pgx.Tx, candidate ObservationCandidate) (ObservationFacts, string, error) {
	facts := ObservationFacts{}
	var existingID string
	err := tx.QueryRow(ctx, `
SELECT observation_id::text
FROM telemetry_runtime.source_observations
WHERE integration_instance_id = $1::uuid AND source_event_id = $2::uuid
`, candidate.IntegrationInstanceID, candidate.Position.EventID).Scan(&existingID)
	if err == nil {
		facts.EventAlreadySeen = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ObservationFacts{}, "", fmt.Errorf("query telemetry source event receipt: %w", err)
	}

	var head SourcePositionHead
	err = tx.QueryRow(ctx, `
SELECT source_offset, source_event_id::text
FROM telemetry_runtime.source_positions
WHERE integration_instance_id = $1::uuid AND source_partition = $2
FOR UPDATE
`, candidate.IntegrationInstanceID, candidate.Position.Partition).Scan(&head.Offset, &head.EventID)
	if err == nil {
		facts.CurrentPosition = &head
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ObservationFacts{}, "", fmt.Errorf("lock telemetry source position: %w", err)
	}

	facts.Bindings, err = queryRuntimeBindings(
		ctx, tx, candidate.IntegrationInstanceID, candidate.ExternalEntityType, candidate.ExternalID,
	)
	if err != nil {
		return ObservationFacts{}, "", err
	}

	binding, quarantine := resolveRuntimeBinding(candidate, facts.Bindings)
	if quarantine != "" {
		return facts, existingID, nil
	}
	var policy ObservationPolicy
	var futureSeconds, lagSeconds int
	err = tx.QueryRow(ctx, `
SELECT f.policy_revision, p.policy_revision, f.value_type, f.expected_unit,
       f.minimum_number, f.maximum_number,
       p.max_future_clock_skew_seconds, p.max_source_lag_seconds
FROM telemetry_runtime.freshness_policies f
JOIN telemetry_runtime.presence_policies p USING (device_id)
WHERE f.device_id = $1::uuid AND f.telemetry_key = $2 AND f.configured
`, binding.DeviceID, candidate.TelemetryKey).Scan(
		&policy.Revision, &policy.PresencePolicyRevision, &policy.ValueType, &policy.Unit,
		&policy.MinimumNumber, &policy.MaximumNumber, &futureSeconds, &lagSeconds,
	)
	if err == nil {
		policy.MaxFutureClockSkew = time.Duration(futureSeconds) * time.Second
		policy.MaxSourceLag = time.Duration(lagSeconds) * time.Second
		facts.Policy = &policy
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ObservationFacts{}, "", fmt.Errorf("query telemetry observation policy: %w", err)
	}
	var latest time.Time
	err = tx.QueryRow(ctx, `
SELECT sampled_at
FROM telemetry_runtime.latest_accepted_telemetry
WHERE device_id = $1::uuid AND telemetry_key = $2
`, binding.DeviceID, candidate.TelemetryKey).Scan(&latest)
	if err == nil {
		latest = latest.UTC()
		facts.LatestSampledAt = &latest
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ObservationFacts{}, "", fmt.Errorf("query latest telemetry ordering fact: %w", err)
	}
	return facts, existingID, nil
}

func advanceSourcePosition(ctx context.Context, tx pgx.Tx, candidate ObservationCandidate) error {
	_, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.source_positions (
  integration_instance_id, source_partition, source_offset, source_event_id,
  observed_at, updated_at
) VALUES ($1::uuid, $2, $3, $4::uuid, $5, $6)
ON CONFLICT (integration_instance_id, source_partition) DO UPDATE SET
  source_offset = EXCLUDED.source_offset,
  source_event_id = EXCLUDED.source_event_id,
  observed_at = EXCLUDED.observed_at,
  updated_at = EXCLUDED.updated_at
WHERE telemetry_runtime.source_positions.source_offset < EXCLUDED.source_offset
`, candidate.IntegrationInstanceID, candidate.Position.Partition, candidate.Position.Offset, candidate.Position.EventID, candidate.SampledAt, candidate.ReceivedAt)
	if err != nil {
		return fmt.Errorf("advance telemetry source position: %w", err)
	}
	return nil
}

func insertSourceDeliveryEvidence(ctx context.Context, tx pgx.Tx, evidenceID string, candidate ObservationCandidate, decision ObservationDecision, payloadSHA string) (string, error) {
	reason := QualityReasonOutOfOrder
	if len(decision.QualityReasons) > 0 {
		reason = decision.QualityReasons[0]
	}
	var persistedID string
	err := tx.QueryRow(ctx, `
INSERT INTO telemetry_runtime.source_delivery_evidence (
  evidence_id, integration_instance_id, source_event_id, source_partition,
  source_offset, source_path, delivery_status, quality_reason, payload_sha256, detected_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (
  integration_instance_id, source_event_id, source_partition, source_offset,
  delivery_status, quality_reason, payload_sha256
) DO UPDATE SET detected_at = LEAST(telemetry_runtime.source_delivery_evidence.detected_at, EXCLUDED.detected_at)
RETURNING evidence_id::text
`, evidenceID, candidate.IntegrationInstanceID, candidate.Position.EventID, candidate.Position.Partition,
		candidate.Position.Offset, string(candidate.SourcePath), string(decision.Status), string(reason), payloadSHA, candidate.ReceivedAt).Scan(&persistedID)
	if err != nil {
		return "", fmt.Errorf("persist telemetry delivery evidence: %w", err)
	}
	return persistedID, nil
}

func insertSourceObservation(ctx context.Context, tx pgx.Tx, observationID string, candidate ObservationCandidate, decision ObservationDecision, payloadSHA string) error {
	var deviceID any
	if decision.DeviceID != "" {
		deviceID = decision.DeviceID
	}
	var value any
	if decision.Status == ObservationAccepted {
		value = []byte(candidate.Value)
	}
	_, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.source_observations (
  observation_id, integration_instance_id, source_event_id, source_partition,
  source_offset, source_path, device_id, telemetry_key, value, value_type, unit,
  sampled_at, received_at, acceptance_status, quality, quality_reasons,
  payload_sha256, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7::uuid, $8, $9::jsonb, $10, $11,
  $12, $13, $14, $15, $16, $17, $13
)
`, observationID, candidate.IntegrationInstanceID, candidate.Position.EventID, candidate.Position.Partition,
		candidate.Position.Offset, string(candidate.SourcePath), deviceID, candidate.TelemetryKey, value, candidate.ValueType, candidate.Unit,
		candidate.SampledAt, candidate.ReceivedAt, string(decision.Status), string(decision.Quality), qualityReasonStrings(decision.QualityReasons), payloadSHA)
	if err != nil {
		return fmt.Errorf("persist telemetry observation evidence: %w", err)
	}
	return nil
}

func (store *PostgresStore) insertQuarantine(ctx context.Context, tx pgx.Tx, candidate ObservationCandidate, decision ObservationDecision, payloadSHA string) error {
	quarantineID, err := store.newEventID(candidate.ReceivedAt)
	if err != nil {
		return fmt.Errorf("generate telemetry quarantine ID: %w", err)
	}
	evidence, err := json.Marshal(map[string]any{
		"schemaVersion":   1,
		"sourcePath":      string(candidate.SourcePath),
		"sourcePartition": candidate.Position.Partition,
		"sourceOffset":    candidate.Position.Offset,
		"sourceEventId":   candidate.Position.EventID,
		"payloadSha256":   payloadSHA,
	})
	if err != nil {
		return fmt.Errorf("encode telemetry quarantine evidence: %w", err)
	}
	var deviceID any
	if decision.DeviceID != "" {
		deviceID = decision.DeviceID
	}
	_, err = tx.Exec(ctx, `
INSERT INTO telemetry_runtime.ingest_quarantine (
  quarantine_id, integration_instance_id, external_entity_type, external_id,
  device_id, telemetry_key, reason_code, evidence, detected_at, resolved_at, resolution
) VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6, $7, $8::jsonb, $9, NULL, NULL)
`, quarantineID, candidate.IntegrationInstanceID, candidate.ExternalEntityType, candidate.ExternalID,
		deviceID, candidate.TelemetryKey, string(decision.QuarantineReason), evidence, candidate.ReceivedAt)
	if err != nil {
		return fmt.Errorf("persist telemetry quarantine evidence: %w", err)
	}
	return nil
}

func upsertLatestObservation(ctx context.Context, tx pgx.Tx, candidate ObservationCandidate, decision ObservationDecision) error {
	_, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.latest_accepted_telemetry (
  device_id, telemetry_key, business_revision, value, value_type, unit, sampled_at,
  received_at, freshness, quality, quality_reasons, policy_revision, updated_at
) VALUES ($1::uuid, $2, 1, $3::jsonb, $4, $5, $6, $7, 'FRESH', $8, $9, $10, $7)
ON CONFLICT (device_id, telemetry_key) DO UPDATE SET
  value = EXCLUDED.value,
  value_type = EXCLUDED.value_type,
  unit = EXCLUDED.unit,
  sampled_at = EXCLUDED.sampled_at,
  received_at = EXCLUDED.received_at,
  quality = EXCLUDED.quality,
  quality_reasons = EXCLUDED.quality_reasons,
  policy_revision = EXCLUDED.policy_revision,
  updated_at = EXCLUDED.updated_at
`, decision.DeviceID, candidate.TelemetryKey, []byte(candidate.Value), candidate.ValueType, candidate.Unit,
		candidate.SampledAt, candidate.ReceivedAt, string(decision.Quality), qualityReasonStrings(decision.QualityReasons), decision.PolicyRevision)
	if err != nil {
		return fmt.Errorf("replace latest accepted telemetry: %w", err)
	}
	return nil
}

func (store *PostgresStore) insertSourcePresenceSignal(ctx context.Context, tx pgx.Tx, candidate ObservationCandidate, decision ObservationDecision) error {
	signalID, err := store.newEventID(candidate.ReceivedAt)
	if err != nil {
		return fmt.Errorf("generate telemetry Presence Signal ID: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO telemetry_runtime.presence_signals (
  signal_id, device_id, signal_type, observed_at, received_at, accepted,
  policy_revision, source_event_id, created_at
) VALUES ($1::uuid, $2::uuid, 'SOURCE_ACTIVITY', $3, $4, true, $5, $6::uuid, $4)
`, signalID, decision.DeviceID, candidate.SampledAt, candidate.ReceivedAt, decision.PresencePolicyRevision, candidate.Position.EventID)
	if err != nil {
		return fmt.Errorf("persist telemetry source Presence Signal: %w", err)
	}
	return nil
}

func observationPayloadSHA(candidate ObservationCandidate) (string, error) {
	encoded, err := json.Marshal(struct {
		IntegrationInstanceID string          `json:"integrationInstanceId"`
		SourcePath            SourcePath      `json:"sourcePath"`
		ExternalEntityType    string          `json:"externalEntityType"`
		ExternalID            string          `json:"externalId"`
		TelemetryKey          string          `json:"telemetryKey"`
		Value                 json.RawMessage `json:"value"`
		ValueType             string          `json:"valueType"`
		Unit                  *string         `json:"unit"`
		SampledAt             time.Time       `json:"sampledAt"`
		Position              SourcePosition  `json:"position"`
	}{
		IntegrationInstanceID: candidate.IntegrationInstanceID, SourcePath: candidate.SourcePath,
		ExternalEntityType: candidate.ExternalEntityType, ExternalID: candidate.ExternalID,
		TelemetryKey: candidate.TelemetryKey, Value: candidate.Value, ValueType: candidate.ValueType,
		Unit: candidate.Unit, SampledAt: candidate.SampledAt, Position: candidate.Position,
	})
	if err != nil {
		return "", fmt.Errorf("encode telemetry observation digest: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func qualityReasonStrings(values []QualityReason) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func retryableTelemetryTransaction(err error) bool {
	if pgconn.SafeToRetry(err) {
		return true
	}
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) {
		return false
	}
	return pgError.Code == "40001" || pgError.Code == "40P01" ||
		strings.HasPrefix(pgError.Code, "08") ||
		pgError.Code == "57P01" || pgError.Code == "57P02" || pgError.Code == "57P03"
}

func safeSourcePath(value string) SourcePath {
	return SourcePath(strings.ToUpper(strings.TrimSpace(value)))
}

var _ ObservationAcceptor = (*PostgresStore)(nil)
