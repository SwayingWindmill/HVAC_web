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

type HistoricalObservationAcceptor interface {
	AcceptHistoricalObservation(context.Context, ObservationCandidate) (ObservationReceipt, error)
}

type observationEvaluator func(ObservationCandidate, ObservationFacts, time.Time) ObservationDecision

func (store *PostgresStore) AcceptObservation(ctx context.Context, candidate ObservationCandidate) (ObservationReceipt, error) {
	if candidate.SourcePath == SourcePathHistoryReplay {
		return ObservationReceipt{}, errors.New("HISTORY_REPLAY requires the dedicated historical observation method")
	}
	return store.acceptObservation(ctx, candidate, EvaluateObservation)
}

func (store *PostgresStore) AcceptHistoricalObservation(ctx context.Context, candidate ObservationCandidate) (ObservationReceipt, error) {
	if candidate.SourcePath != SourcePathHistoryReplay || candidate.ExternalEntityType != "DEVICE" || candidate.Device != nil || candidate.Point != nil {
		return ObservationReceipt{}, errors.New("historical observation must use server-owned HISTORY_REPLAY Device provenance")
	}
	return store.acceptObservation(ctx, candidate, EvaluateHistoricalObservation)
}

func (store *PostgresStore) acceptObservation(ctx context.Context, candidate ObservationCandidate, evaluate observationEvaluator) (ObservationReceipt, error) {
	if store == nil || store.pool == nil {
		return ObservationReceipt{}, errors.New("telemetry runtime store is closed")
	}
	if err := validateObservationCandidate(candidate); err != nil {
		return ObservationReceipt{}, err
	}
	candidate.SampledAt = candidate.SampledAt.UTC()
	candidate.ReceivedAt = candidate.ReceivedAt.UTC()
	for attempt := 0; attempt < 3; attempt++ {
		receipt, err := store.acceptObservationOnce(ctx, candidate, evaluate)
		if err == nil || !retryableTelemetryTransaction(err) {
			return receipt, err
		}
	}
	return ObservationReceipt{}, errors.New("telemetry observation transaction retry budget exhausted")
}

// acceptObservationOnce runs at READ COMMITTED: the source partition, the source event
// and the Device are each serialized by their own lock, taken in that order, so work on
// one Device never waits on or fails because of another.
func (store *PostgresStore) acceptObservationOnce(ctx context.Context, candidate ObservationCandidate, evaluate observationEvaluator) (ObservationReceipt, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ObservationReceipt{}, fmt.Errorf("begin telemetry observation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return ObservationReceipt{}, fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, candidate.SourceID+":partition:"+candidate.Position.Partition); err != nil {
		return ObservationReceipt{}, fmt.Errorf("lock telemetry source partition: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, candidate.SourceID+":event:"+candidate.Position.EventID); err != nil {
		return ObservationReceipt{}, fmt.Errorf("lock telemetry source event: %w", err)
	}

	facts, existingObservationID, err := loadObservationFacts(ctx, tx, candidate)
	if err != nil {
		return ObservationReceipt{}, err
	}
	decision := evaluate(candidate, facts, candidate.ReceivedAt)
	if err := recordResolvedIdentity(ctx, tx, candidate, decision); err != nil {
		return ObservationReceipt{}, err
	}
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
	if err := insertHistoryOutboxIntent(ctx, tx, observationID, candidate, decision, payloadSHA); err != nil {
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
	if len(candidate.SourceID) < 1 || len(candidate.SourceID) > 256 || !uuidV7Pattern.MatchString(candidate.Position.EventID) {
		return errors.New("telemetry source identity is invalid")
	}
	if device := candidate.Device; device != nil && (!uuidV7Pattern.MatchString(device.TenantID) || !uuidV7Pattern.MatchString(device.SiteID) || !uuidV7Pattern.MatchString(device.DeviceID)) {
		return errors.New("telemetry resolved Device is invalid")
	}
	if point := candidate.Point; point != nil && (candidate.Device == nil || !uuidV7Pattern.MatchString(point.PointID) || point.SensorID != nil && !uuidV7Pattern.MatchString(*point.SensorID) || point.PointRevision < 1) {
		return errors.New("telemetry resolved Point is invalid")
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
WHERE source_id = $1 AND source_event_id = $2::uuid
`, candidate.SourceID, candidate.Position.EventID).Scan(&existingID)
	if err == nil {
		facts.EventAlreadySeen = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ObservationFacts{}, "", fmt.Errorf("query telemetry source event receipt: %w", err)
	}

	var head SourcePositionHead
	err = tx.QueryRow(ctx, `
SELECT source_offset, source_event_id::text
FROM telemetry_runtime.source_positions
WHERE source_id = $1 AND source_partition = $2
FOR UPDATE
`, candidate.SourceID, candidate.Position.Partition).Scan(&head.Offset, &head.EventID)
	if err == nil {
		facts.CurrentPosition = &head
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ObservationFacts{}, "", fmt.Errorf("lock telemetry source position: %w", err)
	}

	if err := loadObservationIdentity(ctx, tx, candidate, &facts); err != nil {
		return ObservationFacts{}, "", err
	}
	if facts.Device == nil || facts.DeviceConflict {
		return facts, existingID, nil
	}
	deviceID := facts.Device.DeviceID
	if err := lockDevice(ctx, tx, deviceID); err != nil {
		return ObservationFacts{}, "", err
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
`, deviceID, candidate.TelemetryKey).Scan(
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
`, deviceID, candidate.TelemetryKey).Scan(&latest)
	if err == nil {
		latest = latest.UTC()
		facts.LatestSampledAt = &latest
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ObservationFacts{}, "", fmt.Errorf("query latest telemetry ordering fact: %w", err)
	}
	return facts, existingID, nil
}

// loadObservationIdentity takes a live source's resolved Device and Point as given, unless
// Telemetry already knows that Device under another Tenant or Site. History replay names
// a Device ID only and resolves both from what Telemetry last accepted.
func loadObservationIdentity(ctx context.Context, tx pgx.Tx, candidate ObservationCandidate, facts *ObservationFacts) error {
	deviceID := candidate.ExternalID
	if candidate.SourcePath != SourcePathHistoryReplay {
		if candidate.Device == nil {
			return nil
		}
		deviceID = candidate.Device.DeviceID
	}
	var known ResolvedDevice
	err := tx.QueryRow(ctx, `
SELECT tenant_id::text, site_id::text, device_id::text
FROM telemetry_runtime.devices
WHERE device_id = $1::uuid
`, deviceID).Scan(&known.TenantID, &known.SiteID, &known.DeviceID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("query telemetry Device identity: %w", err)
	}
	if candidate.SourcePath != SourcePathHistoryReplay {
		facts.Device = candidate.Device
		facts.Point = candidate.Point
		facts.DeviceConflict = err == nil && known != *candidate.Device
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	facts.Device = &known
	var point ResolvedPoint
	err = tx.QueryRow(ctx, `
SELECT point_id::text, sensor_id::text, point_type, value_type, unit,
       counter_decrease_mode, counter_rollover_modulus, point_revision
FROM telemetry_runtime.points
WHERE device_id = $1::uuid AND telemetry_key = $2
`, known.DeviceID, candidate.TelemetryKey).Scan(
		&point.PointID, &point.SensorID, &point.PointType, &point.ValueType, &point.Unit,
		&point.CounterDecreaseMode, &point.CounterRolloverModulus, &point.PointRevision,
	)
	if err == nil {
		facts.Point = &point
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("query telemetry Point identity: %w", err)
	}
	return nil
}

// recordResolvedIdentity keeps the Device and Point a live source resolved, so evidence
// can reference the Device and history replay can resolve the Point later.
func recordResolvedIdentity(ctx context.Context, tx pgx.Tx, candidate ObservationCandidate, decision ObservationDecision) error {
	if candidate.Device == nil || decision.DeviceID == "" {
		return nil
	}
	device := candidate.Device
	if _, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4)
ON CONFLICT (device_id) DO NOTHING
`, device.DeviceID, device.TenantID, device.SiteID, candidate.ReceivedAt); err != nil {
		return fmt.Errorf("record telemetry Device identity: %w", err)
	}
	if candidate.Point == nil || decision.PointID == "" {
		return nil
	}
	point := candidate.Point
	if _, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.points (
  device_id, telemetry_key, tenant_id, site_id, point_id, sensor_id, point_type, value_type, unit,
  counter_decrease_mode, counter_rollover_modulus, point_revision, updated_at
) VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (device_id, telemetry_key) DO UPDATE SET
  point_id = EXCLUDED.point_id,
  sensor_id = EXCLUDED.sensor_id,
  point_type = EXCLUDED.point_type,
  value_type = EXCLUDED.value_type,
  unit = EXCLUDED.unit,
  counter_decrease_mode = EXCLUDED.counter_decrease_mode,
  counter_rollover_modulus = EXCLUDED.counter_rollover_modulus,
  point_revision = EXCLUDED.point_revision,
  updated_at = EXCLUDED.updated_at
WHERE (telemetry_runtime.points.point_id, telemetry_runtime.points.point_revision)
  IS DISTINCT FROM (EXCLUDED.point_id, EXCLUDED.point_revision)
`, device.DeviceID, candidate.TelemetryKey, device.TenantID, device.SiteID, point.PointID, point.SensorID,
		point.PointType, point.ValueType, point.Unit, point.CounterDecreaseMode, point.CounterRolloverModulus,
		point.PointRevision, candidate.ReceivedAt); err != nil {
		return fmt.Errorf("record telemetry Point identity: %w", err)
	}
	return nil
}

func advanceSourcePosition(ctx context.Context, tx pgx.Tx, candidate ObservationCandidate) error {
	_, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.source_positions (
  source_id, source_partition, source_offset, source_event_id,
  observed_at, updated_at
) VALUES ($1, $2, $3, $4::uuid, $5, $6)
ON CONFLICT (source_id, source_partition) DO UPDATE SET
  source_offset = EXCLUDED.source_offset,
  source_event_id = EXCLUDED.source_event_id,
  observed_at = EXCLUDED.observed_at,
  updated_at = EXCLUDED.updated_at
WHERE telemetry_runtime.source_positions.source_offset < EXCLUDED.source_offset
`, candidate.SourceID, candidate.Position.Partition, candidate.Position.Offset, candidate.Position.EventID, candidate.SampledAt, candidate.ReceivedAt)
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
	var tenantID any
	if decision.TenantID != "" {
		tenantID = decision.TenantID
	}
	var persistedID string
	err := tx.QueryRow(ctx, `
INSERT INTO telemetry_runtime.source_delivery_evidence (
  evidence_id, tenant_id, source_id, source_event_id, source_partition,
  source_offset, source_path, delivery_status, quality_reason, payload_sha256, detected_at
) VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (
  source_id, source_event_id, source_partition, source_offset,
  delivery_status, quality_reason, payload_sha256
) DO UPDATE SET detected_at = LEAST(telemetry_runtime.source_delivery_evidence.detected_at, EXCLUDED.detected_at)
RETURNING evidence_id::text
`, evidenceID, tenantID, candidate.SourceID, candidate.Position.EventID, candidate.Position.Partition,
		candidate.Position.Offset, string(candidate.SourcePath), string(decision.Status), string(reason), payloadSHA, candidate.ReceivedAt).Scan(&persistedID)
	if err != nil {
		return "", fmt.Errorf("persist telemetry delivery evidence: %w", err)
	}
	return persistedID, nil
}

func insertSourceObservation(ctx context.Context, tx pgx.Tx, observationID string, candidate ObservationCandidate, decision ObservationDecision, payloadSHA string) error {
	var tenantID any
	if decision.TenantID != "" {
		tenantID = decision.TenantID
	}
	var deviceID any
	if decision.DeviceID != "" {
		deviceID = decision.DeviceID
	}
	var pointID any
	if decision.PointID != "" {
		pointID = decision.PointID
	}
	var sensorID any
	if decision.SensorID != "" {
		sensorID = decision.SensorID
	}
	var value any
	if decision.Status == ObservationAccepted || (decision.Status == ObservationOutOfOrder && decision.PointID != "") {
		value = []byte(candidate.Value)
	}
	_, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.source_observations (
  observation_id, tenant_id, source_id, source_event_id, source_partition,
  source_offset, source_path, device_id, point_id, sensor_id, telemetry_key, value, value_type, unit,
  sampled_at, received_at, acceptance_status, quality, quality_reasons,
  payload_sha256, created_at
) VALUES (
  $1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8::uuid, $9::uuid, $10::uuid, $11, $12::jsonb, $13, $14,
  $15, $16, $17, $18, $19, $20, $16
)
`, observationID, tenantID, candidate.SourceID, candidate.Position.EventID, candidate.Position.Partition,
		candidate.Position.Offset, string(candidate.SourcePath), deviceID, pointID, sensorID, candidate.TelemetryKey, value, candidate.ValueType, candidate.Unit,
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
	var tenantID any
	if decision.TenantID != "" {
		tenantID = decision.TenantID
	}
	var deviceID any
	if decision.DeviceID != "" {
		deviceID = decision.DeviceID
	}
	_, err = tx.Exec(ctx, `
INSERT INTO telemetry_runtime.ingest_quarantine (
  quarantine_id, tenant_id, source_id, external_entity_type, external_id,
  device_id, telemetry_key, reason_code, evidence, detected_at, resolved_at, resolution
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7, $8, $9::jsonb, $10, NULL, NULL)
`, quarantineID, tenantID, candidate.SourceID, candidate.ExternalEntityType, candidate.ExternalID,
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
WHERE telemetry_runtime.latest_accepted_telemetry.sampled_at <= EXCLUDED.sampled_at
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
		SourceID           string          `json:"sourceId"`
		SourcePath         SourcePath      `json:"sourcePath"`
		ExternalEntityType string          `json:"externalEntityType"`
		ExternalID         string          `json:"externalId"`
		TelemetryKey       string          `json:"telemetryKey"`
		Value              json.RawMessage `json:"value"`
		ValueType          string          `json:"valueType"`
		Unit               *string         `json:"unit"`
		SampledAt          time.Time       `json:"sampledAt"`
		Position           SourcePosition  `json:"position"`
	}{
		SourceID: candidate.SourceID, SourcePath: candidate.SourcePath,
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
