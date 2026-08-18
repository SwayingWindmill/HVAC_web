package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

var ErrDeviceNotFound = errors.New("telemetry device not found")

type SnapshotCommit struct {
	Snapshot         telemetryapi.DeviceObservationSnapshot
	FullSnapshot     telemetryapi.DeviceObservationSnapshot
	StateChanged     bool
	PreviousRevision int64
}

type SnapshotStore interface {
	EvaluateAndRead(context.Context, telemetryauth.Target, time.Time) (SnapshotCommit, error)
}

type EventIDGenerator func(time.Time) (string, error)

type PostgresStore struct {
	pool       *pgxpool.Pool
	newEventID EventIDGenerator
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse telemetry runtime database URL: %w", err)
	}
	if config.ConnConfig.User != "s2_telemetry_service" {
		return nil, errors.New("telemetry runtime database identity must be s2_telemetry_service")
	}
	config.MaxConns = 16
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE s2_telemetry_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open telemetry runtime database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping telemetry runtime database: %w", err)
	}
	return &PostgresStore{pool: pool, newEventID: newUUIDv7}, nil
}

func NewPostgresStore(pool *pgxpool.Pool, generator EventIDGenerator) *PostgresStore {
	if generator == nil {
		generator = newUUIDv7
	}
	return &PostgresStore{pool: pool, newEventID: generator}
}

func (store *PostgresStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresStore) Ping(ctx context.Context) error {
	return store.pool.Ping(ctx)
}

func (store *PostgresStore) EvaluateAndRead(ctx context.Context, target telemetryauth.Target, evaluatedAt time.Time) (SnapshotCommit, error) {
	if store == nil || store.pool == nil {
		return SnapshotCommit{}, errors.New("telemetry runtime store is closed")
	}
	if evaluatedAt.IsZero() {
		return SnapshotCommit{}, errors.New("telemetry evaluation time is required")
	}
	if _, err := telemetryauth.CanonicalTargets([]telemetryauth.Target{target}); err != nil {
		return SnapshotCommit{}, fmt.Errorf("validate telemetry snapshot target: %w", err)
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SnapshotCommit{}, fmt.Errorf("begin telemetry snapshot transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return SnapshotCommit{}, fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}

	commit, err := store.evaluateAndPersistDevice(ctx, tx, target.DeviceID, target.Keys, evaluatedAt)
	if err != nil {
		return SnapshotCommit{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SnapshotCommit{}, fmt.Errorf("commit telemetry snapshot transaction: %w", err)
	}
	return commit, nil
}

func (store *PostgresStore) evaluateAndPersistDevice(ctx context.Context, tx pgx.Tx, deviceID string, requestedKeys []string, evaluatedAt time.Time) (SnapshotCommit, error) {
	previousRevision, previousDigest, previousSnapshot, err := lockCurrentSnapshot(ctx, tx, deviceID)
	if err != nil {
		return SnapshotCommit{}, err
	}
	facts, err := loadDeviceFacts(ctx, tx, deviceID)
	if err != nil {
		return SnapshotCommit{}, err
	}
	candidateRevision := previousRevision
	if candidateRevision < 1 {
		candidateRevision = 1
	}
	evaluation, err := EvaluateCanonical(facts, candidateRevision, evaluatedAt)
	if err != nil {
		return SnapshotCommit{}, fmt.Errorf("evaluate telemetry snapshot: %w", err)
	}
	stateChanged := previousRevision == 0 || evaluation.StateDigest != previousDigest
	if stateChanged && previousRevision > 0 {
		candidateRevision = previousRevision + 1
		evaluation, err = EvaluateCanonical(facts, candidateRevision, evaluatedAt)
		if err != nil {
			return SnapshotCommit{}, fmt.Errorf("re-evaluate telemetry snapshot revision: %w", err)
		}
	}
	if err := persistCurrentState(ctx, tx, evaluation, evaluatedAt); err != nil {
		return SnapshotCommit{}, err
	}
	if stateChanged {
		changedKeys, err := changedTelemetryKeys(previousSnapshot, evaluation.Snapshot)
		if err != nil {
			return SnapshotCommit{}, err
		}
		if err := store.insertOutboxIntent(ctx, tx, evaluation.Snapshot, previousRevision, changedKeys, evaluatedAt); err != nil {
			return SnapshotCommit{}, err
		}
	}
	return SnapshotCommit{
		Snapshot: ProjectSnapshot(evaluation.Snapshot, requestedKeys), FullSnapshot: evaluation.Snapshot,
		StateChanged: stateChanged, PreviousRevision: previousRevision,
	}, nil
}

func lockCurrentSnapshot(ctx context.Context, tx pgx.Tx, deviceID string) (int64, string, *telemetryapi.DeviceObservationSnapshot, error) {
	var revision int64
	var digest string
	var snapshotJSON []byte
	err := tx.QueryRow(ctx, `
SELECT business_revision, state_sha256, snapshot
FROM telemetry_runtime.device_observation_snapshots
WHERE device_id = $1::uuid
FOR UPDATE
`, deviceID).Scan(&revision, &digest, &snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", nil, nil
	}
	if err != nil {
		return 0, "", nil, fmt.Errorf("lock current telemetry snapshot: %w", err)
	}
	var snapshot telemetryapi.DeviceObservationSnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		return 0, "", nil, fmt.Errorf("decode current telemetry snapshot: %w", err)
	}
	return revision, digest, &snapshot, nil
}

func loadDeviceFacts(ctx context.Context, tx pgx.Tx, deviceID string) (DeviceFacts, error) {
	facts := DeviceFacts{FreshnessPolicies: map[string]FreshnessPolicy{}, Latest: map[string]LatestObservation{}, RejectedKeys: map[string]bool{}}
	var applicability string
	err := tx.QueryRow(ctx, `
SELECT device_id::text, tenant_id::text, site_id::text, presence_applicability
FROM telemetry_runtime.registry_device_bindings
WHERE device_id = $1::uuid AND binding_status = 'ACTIVE' AND valid_to IS NULL
`, deviceID).Scan(&facts.DeviceID, &facts.TenantID, &facts.SiteID, &applicability)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeviceFacts{}, ErrDeviceNotFound
	}
	if err != nil {
		return DeviceFacts{}, fmt.Errorf("load telemetry device binding: %w", err)
	}
	facts.Applicability = telemetryapi.PresenceApplicability(applicability)

	var onlineWithin, offlineAfter int
	var policyRevision int64
	var acceptedSignalTypes []string
	err = tx.QueryRow(ctx, `
SELECT policy_revision, online_within_seconds, offline_after_seconds, accepted_signal_types
FROM telemetry_runtime.presence_policies
WHERE device_id = $1::uuid
`, deviceID).Scan(&policyRevision, &onlineWithin, &offlineAfter, &acceptedSignalTypes)
	if err == nil {
		facts.PresencePolicy = &PresencePolicy{
			Revision: policyRevision, OnlineWithin: time.Duration(onlineWithin) * time.Second,
			OfflineAfter: time.Duration(offlineAfter) * time.Second, AcceptedSignalTypes: append([]string(nil), acceptedSignalTypes...),
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return DeviceFacts{}, fmt.Errorf("load telemetry presence policy: %w", err)
	}

	var coverageReason *string
	err = tx.QueryRow(ctx, `
SELECT available, continuous_since, reason_code
FROM telemetry_runtime.observation_coverage
WHERE device_id = $1::uuid
`, deviceID).Scan(&facts.Coverage.Available, &facts.Coverage.ContinuousSince, &coverageReason)
	if errors.Is(err, pgx.ErrNoRows) {
		facts.Coverage = Coverage{Available: false, Reason: telemetryapi.AvailabilityReasonCodeObservationCoverageGap}
	} else if err != nil {
		return DeviceFacts{}, fmt.Errorf("load telemetry observation coverage: %w", err)
	} else if coverageReason != nil {
		facts.Coverage.Reason = telemetryapi.AvailabilityReasonCode(*coverageReason)
	}

	rows, err := tx.Query(ctx, `
SELECT signal_type, observed_at
FROM telemetry_runtime.presence_signals
WHERE device_id = $1::uuid AND accepted
ORDER BY observed_at, signal_id
`, deviceID)
	if err != nil {
		return DeviceFacts{}, fmt.Errorf("query telemetry presence signals: %w", err)
	}
	for rows.Next() {
		var signal PresenceSignal
		if err := rows.Scan(&signal.Type, &signal.ObservedAt); err != nil {
			rows.Close()
			return DeviceFacts{}, fmt.Errorf("scan telemetry presence signal: %w", err)
		}
		facts.PresenceSignals = append(facts.PresenceSignals, signal)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return DeviceFacts{}, fmt.Errorf("iterate telemetry presence signals: %w", err)
	}
	rows.Close()

	var lastKnownJSON []byte
	err = tx.QueryRow(ctx, `SELECT last_known FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`, deviceID).Scan(&lastKnownJSON)
	if err == nil && len(lastKnownJSON) > 0 {
		var lastKnown telemetryapi.LastKnownPresence
		if err := json.Unmarshal(lastKnownJSON, &lastKnown); err != nil {
			return DeviceFacts{}, fmt.Errorf("decode last-known presence: %w", err)
		}
		facts.LastKnownPresence = &lastKnown
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return DeviceFacts{}, fmt.Errorf("load last-known presence: %w", err)
	}

	rows, err = tx.Query(ctx, `
SELECT telemetry_key, policy_revision, fresh_within_seconds, configured
FROM telemetry_runtime.freshness_policies
WHERE device_id = $1::uuid
ORDER BY telemetry_key
`, deviceID)
	if err != nil {
		return DeviceFacts{}, fmt.Errorf("query telemetry freshness policies: %w", err)
	}
	for rows.Next() {
		var key string
		var revision int64
		var seconds int
		var configured bool
		if err := rows.Scan(&key, &revision, &seconds, &configured); err != nil {
			rows.Close()
			return DeviceFacts{}, fmt.Errorf("scan telemetry freshness policy: %w", err)
		}
		facts.FreshnessPolicies[key] = FreshnessPolicy{Revision: revision, FreshFor: time.Duration(seconds) * time.Second, Configured: configured}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return DeviceFacts{}, fmt.Errorf("iterate telemetry freshness policies: %w", err)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
SELECT telemetry_key, value, value_type, unit, sampled_at, received_at, quality, quality_reasons
FROM telemetry_runtime.latest_accepted_telemetry
WHERE device_id = $1::uuid
ORDER BY telemetry_key
`, deviceID)
	if err != nil {
		return DeviceFacts{}, fmt.Errorf("query latest accepted telemetry: %w", err)
	}
	for rows.Next() {
		var key string
		var value []byte
		var observation LatestObservation
		var quality string
		var reasons []string
		if err := rows.Scan(&key, &value, &observation.ValueType, &observation.Unit, &observation.SampledAt, &observation.ReceivedAt, &quality, &reasons); err != nil {
			rows.Close()
			return DeviceFacts{}, fmt.Errorf("scan latest accepted telemetry: %w", err)
		}
		observation.Value = append(json.RawMessage(nil), value...)
		observation.Quality = telemetryapi.TelemetryQuality(quality)
		for _, reason := range reasons {
			observation.QualityReasons = append(observation.QualityReasons, telemetryapi.QualityReasonCode(reason))
		}
		facts.Latest[key] = observation
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return DeviceFacts{}, fmt.Errorf("iterate latest accepted telemetry: %w", err)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
SELECT DISTINCT telemetry_key
FROM telemetry_runtime.source_observations
WHERE device_id = $1::uuid AND acceptance_status = 'REJECTED'
ORDER BY telemetry_key
`, deviceID)
	if err != nil {
		return DeviceFacts{}, fmt.Errorf("query rejected telemetry candidates: %w", err)
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return DeviceFacts{}, fmt.Errorf("scan rejected telemetry candidate: %w", err)
		}
		facts.RejectedKeys[key] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return DeviceFacts{}, fmt.Errorf("iterate rejected telemetry candidates: %w", err)
	}
	rows.Close()
	return facts, nil
}

func persistCurrentState(ctx context.Context, tx pgx.Tx, evaluation CanonicalEvaluation, evaluatedAt time.Time) error {
	snapshot := evaluation.Snapshot
	presence := snapshot.Presence
	lastKnown, err := nullableJSON(presence.LastKnown)
	if err != nil {
		return fmt.Errorf("encode last-known presence: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO telemetry_runtime.device_presence (
  device_id, business_revision, applicability, current_state, last_seen_at,
  evaluated_at, policy_revision, last_known, updated_at
) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::jsonb, $6)
ON CONFLICT (device_id) DO UPDATE SET
  business_revision = EXCLUDED.business_revision,
  applicability = EXCLUDED.applicability,
  current_state = EXCLUDED.current_state,
  last_seen_at = EXCLUDED.last_seen_at,
  evaluated_at = EXCLUDED.evaluated_at,
  policy_revision = EXCLUDED.policy_revision,
  last_known = EXCLUDED.last_known,
  updated_at = EXCLUDED.updated_at
`, string(snapshot.DeviceId), int64(snapshot.BusinessRevision), string(presence.Applicability), nullablePresence(presence.CurrentState), nullableInstant(presence.LastSeenAt), evaluatedAt, nullablePolicyRevision(presence.PolicyRevision), lastKnown)
	if err != nil {
		return fmt.Errorf("persist current telemetry presence: %w", err)
	}

	for _, value := range snapshot.Values {
		if value.Present == nil {
			continue
		}
		_, err := tx.Exec(ctx, `
UPDATE telemetry_runtime.latest_accepted_telemetry
SET business_revision = $3, freshness = $4, policy_revision = $5, updated_at = $6
WHERE device_id = $1::uuid AND telemetry_key = $2
`, string(snapshot.DeviceId), string(value.Present.Key), int64(snapshot.BusinessRevision), value.Present.Freshness, int64(value.Present.PolicyRevision), evaluatedAt)
		if err != nil {
			return fmt.Errorf("persist latest telemetry evaluation: %w", err)
		}
	}

	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode current telemetry snapshot: %w", err)
	}
	snapshotHash := sha256.Sum256(snapshotJSON)
	_, err = tx.Exec(ctx, `
INSERT INTO telemetry_runtime.device_observation_snapshots (
  device_id, business_revision, evaluated_at, evaluation_availability,
  availability_reasons, telemetry_readiness, display_state, snapshot,
  snapshot_sha256, state_sha256, updated_at
) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $3)
ON CONFLICT (device_id) DO UPDATE SET
  business_revision = EXCLUDED.business_revision,
  evaluated_at = EXCLUDED.evaluated_at,
  evaluation_availability = EXCLUDED.evaluation_availability,
  availability_reasons = EXCLUDED.availability_reasons,
  telemetry_readiness = EXCLUDED.telemetry_readiness,
  display_state = EXCLUDED.display_state,
  snapshot = EXCLUDED.snapshot,
  snapshot_sha256 = EXCLUDED.snapshot_sha256,
  state_sha256 = EXCLUDED.state_sha256,
  updated_at = EXCLUDED.updated_at
`, string(snapshot.DeviceId), int64(snapshot.BusinessRevision), evaluatedAt, string(snapshot.EvaluationAvailability), availabilityStrings(snapshot.AvailabilityReasons), string(snapshot.TelemetryReadiness), nullableDisplay(snapshot.DisplayState), snapshotJSON, hex.EncodeToString(snapshotHash[:]), evaluation.StateDigest)
	if err != nil {
		return fmt.Errorf("persist current telemetry snapshot: %w", err)
	}
	return nil
}

func (store *PostgresStore) insertOutboxIntent(ctx context.Context, tx pgx.Tx, snapshot telemetryapi.DeviceObservationSnapshot, previousRevision int64, changedKeys []string, createdAt time.Time) error {
	eventID, err := store.newEventID(createdAt)
	if err != nil {
		return fmt.Errorf("generate telemetry outbox event ID: %w", err)
	}
	payload := map[string]any{
		"schemaVersion":    1,
		"kind":             "DEVICE_OBSERVATION_SNAPSHOT_COMMITTED",
		"eventId":          eventID,
		"deviceId":         snapshot.DeviceId,
		"previousRevision": previousRevision,
		"revision":         snapshot.BusinessRevision,
		"evaluatedAt":      snapshot.EvaluatedAt,
		"changedKeys":      append([]string(nil), changedKeys...),
		"snapshot":         snapshot,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode telemetry outbox intent: %w", err)
	}
	digest := sha256.Sum256(encoded)
	_, err = tx.Exec(ctx, `
INSERT INTO telemetry_runtime.telemetry_publication_outbox (
  event_id, device_id, business_revision, subscription_id, event_family,
  payload, payload_sha256, delivery_state, available_at, attempts,
  last_error_code, published_at, created_at
) VALUES ($1::uuid, $2::uuid, $3, NULL, 'hvac.telemetry.device-snapshot.v1', $4::jsonb, $5, 'PENDING', $6, 0, NULL, NULL, $6)
`, eventID, string(snapshot.DeviceId), int64(snapshot.BusinessRevision), encoded, hex.EncodeToString(digest[:]), createdAt)
	if err != nil {
		return fmt.Errorf("persist telemetry outbox intent: %w", err)
	}
	return nil
}

func changedTelemetryKeys(previous *telemetryapi.DeviceObservationSnapshot, current telemetryapi.DeviceObservationSnapshot) ([]string, error) {
	currentStates := make(map[string][]byte, len(current.Values))
	for _, value := range current.Values {
		key := telemetryStateKey(value)
		if key == "" {
			return nil, errors.New("current telemetry snapshot contains an invalid key state")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode current telemetry key state: %w", err)
		}
		currentStates[key] = encoded
	}
	if previous == nil {
		keys := make([]string, 0, len(currentStates))
		for _, value := range current.Values {
			keys = append(keys, telemetryStateKey(value))
		}
		return keys, nil
	}
	previousStates := make(map[string][]byte, len(previous.Values))
	for _, value := range previous.Values {
		key := telemetryStateKey(value)
		if key == "" {
			return nil, errors.New("previous telemetry snapshot contains an invalid key state")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode previous telemetry key state: %w", err)
		}
		previousStates[key] = encoded
	}
	changed := make([]string, 0)
	for _, value := range current.Values {
		key := telemetryStateKey(value)
		if string(previousStates[key]) != string(currentStates[key]) {
			changed = append(changed, key)
		}
	}
	for _, value := range previous.Values {
		key := telemetryStateKey(value)
		if _, exists := currentStates[key]; !exists {
			changed = append(changed, key)
		}
	}
	return changed, nil
}

func nullableJSON(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}
func nullablePresence(value *telemetryapi.DevicePresenceState) any {
	if value == nil {
		return nil
	}
	return string(*value)
}
func nullableInstant(value *telemetryapi.Instant) any {
	if value == nil {
		return nil
	}
	return string(*value)
}
func nullablePolicyRevision(value *telemetryapi.PolicyRevision) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}
func nullableDisplay(value *telemetryapi.DeviceDisplayState) any {
	if value == nil {
		return nil
	}
	return string(*value)
}
func availabilityStrings(values []telemetryapi.AvailabilityReasonCode) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func newUUIDv7(now time.Time) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	milliseconds := now.UTC().UnixMilli()
	value[0] = byte(milliseconds >> 40)
	value[1] = byte(milliseconds >> 32)
	value[2] = byte(milliseconds >> 24)
	value[3] = byte(milliseconds >> 16)
	value[4] = byte(milliseconds >> 8)
	value[5] = byte(milliseconds)
	value[6] = value[6]&0x0f | 0x70
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

var _ SnapshotStore = (*PostgresStore)(nil)
