package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const MaxObservationBatchSize = 512

var (
	uuidV7Pattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	uuidPattern         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
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
	AcceptObservationBatch(context.Context, []ObservationCandidate) ([]ObservationReceipt, error)
}

type UnseenObservationFilter interface {
	FilterUnseenObservations(context.Context, string, []string) (map[string]bool, error)
}

type HistoricalObservationAcceptor interface {
	AcceptHistoricalObservation(context.Context, ObservationCandidate) (ObservationReceipt, error)
	AcceptHistoricalObservationBatch(context.Context, []ObservationCandidate) ([]ObservationReceipt, error)
}

type observationEvaluator func(ObservationCandidate, ObservationFacts, time.Time) ObservationDecision

// FilterUnseenObservations returns the subset of eventIDs not yet recorded in
// telemetry_runtime.source_observations for sourceID so retries do not burn rate limits.
func (store *PostgresStore) FilterUnseenObservations(ctx context.Context, sourceID string, eventIDs []string) (map[string]bool, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("telemetry runtime store is closed")
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || len(eventIDs) == 0 {
		return map[string]bool{}, nil
	}
	unseen := make(map[string]bool, len(eventIDs))
	uniqueIDs := make([]string, 0, len(eventIDs))
	for _, rawID := range eventIDs {
		id := strings.ToLower(strings.TrimSpace(rawID))
		if !uuidV7Pattern.MatchString(id) || unseen[id] {
			continue
		}
		unseen[id] = true
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return map[string]bool{}, nil
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin telemetry unseen observation query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return nil, fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	rows, err := tx.Query(ctx, `
SELECT source_event_id::text
FROM telemetry_runtime.source_observations
WHERE source_id = $1 AND source_event_id = ANY($2::uuid[])
`, sourceID, uniqueIDs)
	if err != nil {
		return nil, fmt.Errorf("query existing telemetry source observations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan existing telemetry source observation: %w", err)
		}
		delete(unseen, strings.ToLower(strings.TrimSpace(id)))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing telemetry source observations: %w", err)
	}
	return unseen, nil
}

func (store *PostgresStore) AcceptObservation(ctx context.Context, candidate ObservationCandidate) (ObservationReceipt, error) {
	receipts, err := store.AcceptObservationBatch(ctx, []ObservationCandidate{candidate})
	if err != nil {
		return ObservationReceipt{}, err
	}
	return receipts[0], nil
}

func (store *PostgresStore) AcceptObservationBatch(ctx context.Context, candidates []ObservationCandidate) ([]ObservationReceipt, error) {
	if len(candidates) == 0 || len(candidates) > MaxObservationBatchSize {
		return nil, errors.New("telemetry observation batch size is invalid")
	}
	for _, candidate := range candidates {
		if candidate.SourcePath == SourcePathHistoryReplay {
			return nil, errors.New("HISTORY_REPLAY requires the dedicated historical observation method")
		}
	}
	return store.acceptObservationBatch(ctx, candidates, EvaluateObservation)
}

func (store *PostgresStore) AcceptHistoricalObservation(ctx context.Context, candidate ObservationCandidate) (ObservationReceipt, error) {
	receipts, err := store.AcceptHistoricalObservationBatch(ctx, []ObservationCandidate{candidate})
	if err != nil {
		return ObservationReceipt{}, err
	}
	return receipts[0], nil
}

func (store *PostgresStore) AcceptHistoricalObservationBatch(ctx context.Context, candidates []ObservationCandidate) ([]ObservationReceipt, error) {
	if len(candidates) == 0 || len(candidates) > MaxObservationBatchSize {
		return nil, errors.New("telemetry historical observation batch size is invalid")
	}
	for _, candidate := range candidates {
		if candidate.SourcePath != SourcePathHistoryReplay || candidate.ExternalEntityType != "DEVICE" || candidate.Device != nil || candidate.Point != nil || candidate.EdgeReplay {
			return nil, errors.New("historical observation must use server-owned HISTORY_REPLAY Device provenance")
		}
	}
	return store.acceptObservationBatch(ctx, candidates, EvaluateHistoricalObservation)
}

func (store *PostgresStore) acceptObservationBatch(ctx context.Context, candidates []ObservationCandidate, evaluate observationEvaluator) ([]ObservationReceipt, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("telemetry runtime store is closed")
	}
	normalized := make([]ObservationCandidate, len(candidates))
	payloadSHAs := make([]string, len(candidates))
	for index, candidate := range candidates {
		if candidate.SourcePath == SourcePathHistoryReplay {
			candidate.ExternalID = strings.ToLower(strings.TrimSpace(candidate.ExternalID))
		}
		if err := validateObservationCandidate(candidate); err != nil {
			return nil, err
		}
		candidate.SampledAt = candidate.SampledAt.UTC()
		candidate.ReceivedAt = candidate.ReceivedAt.UTC()
		candidate.Value = append(json.RawMessage(nil), candidate.Value...)
		payloadSHA, err := observationPayloadSHA(candidate)
		if err != nil {
			return nil, err
		}
		normalized[index] = candidate
		payloadSHAs[index] = payloadSHA
	}
	for attempt := 0; attempt < 3; attempt++ {
		receipts, err := store.acceptObservationBatchOnce(ctx, normalized, payloadSHAs, evaluate)
		if err == nil || !retryableTelemetryTransaction(err) {
			return receipts, err
		}
	}
	return nil, errors.New("telemetry observation batch transaction retry budget exhausted")
}

// acceptObservationBatchOnce runs at READ COMMITTED: it acquires all source partition,
// source event, and Device advisory locks in one globally sorted query to prevent
// deadlocks with overlapping batches and concurrent EvaluateAndRead calls.
func (store *PostgresStore) acceptObservationBatchOnce(ctx context.Context, candidates []ObservationCandidate, payloadSHAs []string, evaluate observationEvaluator) ([]ObservationReceipt, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin telemetry observation batch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return nil, fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	if err := acquireBatchAdvisoryLocks(ctx, tx, collectBatchLockKeys(candidates)); err != nil {
		return nil, err
	}
	state, err := prefetchBatchFacts(ctx, tx, candidates)
	if err != nil {
		return nil, err
	}
	receipts, plan, err := evaluateBatchCandidates(candidates, payloadSHAs, state, evaluate, store.newEventID)
	if err != nil {
		return nil, err
	}
	if err := persistBatchWritePlan(ctx, tx, plan, receipts); err != nil {
		return nil, err
	}
	for _, deviceID := range plan.reevalDeviceOrder {
		commit, err := store.evaluateAndPersistDevice(ctx, tx, deviceID, nil, plan.maxReceivedAtByDevice[deviceID])
		if err != nil {
			return nil, err
		}
		for _, receiptIndex := range plan.reevalReceiptIndices[deviceID] {
			receipts[receiptIndex].BusinessRevision = int64(commit.Snapshot.BusinessRevision)
			receipts[receiptIndex].StateChanged = commit.StateChanged
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit telemetry observation batch transaction: %w", err)
	}
	for index := range receipts {
		if receipts[index].Status == ObservationQuarantined {
			receipts[index].DeviceID = ""
		}
	}
	return receipts, nil
}

type sourceEventKey struct {
	sourceID string
	eventID  string
}

type sourcePartitionKey struct {
	sourceID  string
	partition string
}

type deviceKey struct {
	deviceID string
	key      string
}

type loadedPolicy struct {
	policy             ObservationPolicy
	freshWithinSeconds int
}

type latestPointState struct {
	value              json.RawMessage
	quality            string
	sampledAt          time.Time
	unchangedSince     *time.Time
	healthStatus       *string
	healthAt           *time.Time
	freshWithinSeconds *int
}

type batchWorkingState struct {
	seenEvents    map[sourceEventKey]string
	positions     map[sourcePartitionKey]SourcePositionHead
	knownDevices  map[string]ResolvedDevice
	knownPoints   map[deviceKey]ResolvedPoint
	policies      map[deviceKey]loadedPolicy
	latestByPoint map[deviceKey]latestPointState
}

type deviceInsertRow struct {
	device     ResolvedDevice
	receivedAt time.Time
}

type pointUpsertRow struct {
	device     ResolvedDevice
	key        string
	point      ResolvedPoint
	receivedAt time.Time
}

type sourcePositionUpsertRow struct {
	candidate ObservationCandidate
}

type sourceObservationInsertRow struct {
	observationID string
	candidate     ObservationCandidate
	decision      ObservationDecision
	payloadSHA    string
}

type historyOutboxInsertRow struct {
	observationID string
	payload       []byte
	digest        string
	receivedAt    time.Time
}

type quarantineInsertRow struct {
	quarantineID string
	candidate    ObservationCandidate
	decision     ObservationDecision
	evidence     []byte
}

type latestUpdateOp struct {
	replaceLatest bool
	candidate     ObservationCandidate
	decision      ObservationDecision
}

type presenceSignalInsertRow struct {
	signalID  string
	candidate ObservationCandidate
	decision  ObservationDecision
}

type deliveryEvidenceInsertRow struct {
	receiptIndex int
	evidenceID   string
	candidate    ObservationCandidate
	decision     ObservationDecision
	payloadSHA   string
}

type batchWritePlan struct {
	devices               []deviceInsertRow
	points                []pointUpsertRow
	positions             []sourcePositionUpsertRow
	observations          []sourceObservationInsertRow
	historyOutbox         []historyOutboxInsertRow
	quarantines           []quarantineInsertRow
	latestUpdates         []latestUpdateOp
	presenceSignals       []presenceSignalInsertRow
	deliveryEvidences     []deliveryEvidenceInsertRow
	reevalDeviceOrder     []string
	maxReceivedAtByDevice map[string]time.Time
	reevalReceiptIndices  map[string][]int
}

func collectBatchLockKeys(candidates []ObservationCandidate) []string {
	seen := make(map[string]struct{}, len(candidates)*3)
	keys := make([]string, 0, len(candidates)*3)
	add := func(key string) {
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, candidate := range candidates {
		add(candidate.SourceID + ":partition:" + candidate.Position.Partition)
		add(candidate.SourceID + ":event:" + candidate.Position.EventID)
		if candidate.SourcePath == SourcePathHistoryReplay {
			if candidate.ExternalID != "" {
				add("telemetry-device:" + candidate.ExternalID)
			}
		} else if candidate.Device != nil {
			add("telemetry-device:" + candidate.Device.DeviceID)
		}
	}
	return keys
}

func acquireBatchAdvisoryLocks(ctx context.Context, tx pgx.Tx, lockKeys []string) error {
	if len(lockKeys) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
SELECT pg_advisory_xact_lock(lock_id)
FROM (
  SELECT DISTINCT hashtextextended(k, 0) AS lock_id
  FROM unnest($1::text[]) AS t(k)
  ORDER BY lock_id
) AS ordered_locks
`, lockKeys); err != nil {
		return fmt.Errorf("acquire telemetry batch advisory locks: %w", err)
	}
	return nil
}

func prefetchBatchFacts(ctx context.Context, tx pgx.Tx, candidates []ObservationCandidate) (*batchWorkingState, error) {
	state := &batchWorkingState{
		seenEvents:    make(map[sourceEventKey]string, len(candidates)),
		positions:     make(map[sourcePartitionKey]SourcePositionHead, len(candidates)),
		knownDevices:  make(map[string]ResolvedDevice, len(candidates)),
		knownPoints:   make(map[deviceKey]ResolvedPoint),
		policies:      make(map[deviceKey]loadedPolicy, len(candidates)),
		latestByPoint: make(map[deviceKey]latestPointState, len(candidates)),
	}

	seenEventPairs := make(map[sourceEventKey]struct{}, len(candidates))
	eventSourceIDs := make([]string, 0, len(candidates))
	eventIDs := make([]string, 0, len(candidates))

	seenPartitions := make(map[sourcePartitionKey]struct{}, len(candidates))
	partitions := make([]sourcePartitionKey, 0, len(candidates))

	seenDevices := make(map[string]struct{}, len(candidates))
	deviceIDs := make([]string, 0, len(candidates))

	seenReplayPoints := make(map[deviceKey]struct{})
	replayPointDeviceIDs := make([]string, 0)
	replayPointKeys := make([]string, 0)

	seenBatchPoints := make(map[deviceKey]struct{}, len(candidates))
	batchPointDeviceIDs := make([]string, 0, len(candidates))
	batchPointKeys := make([]string, 0, len(candidates))

	for _, candidate := range candidates {
		evKey := sourceEventKey{sourceID: candidate.SourceID, eventID: candidate.Position.EventID}
		if _, ok := seenEventPairs[evKey]; !ok {
			seenEventPairs[evKey] = struct{}{}
			eventSourceIDs = append(eventSourceIDs, evKey.sourceID)
			eventIDs = append(eventIDs, evKey.eventID)
		}
		partKey := sourcePartitionKey{sourceID: candidate.SourceID, partition: candidate.Position.Partition}
		if _, ok := seenPartitions[partKey]; !ok {
			seenPartitions[partKey] = struct{}{}
			partitions = append(partitions, partKey)
		}
		targetDeviceID := ""
		if candidate.SourcePath == SourcePathHistoryReplay {
			targetDeviceID = candidate.ExternalID
		} else if candidate.Device != nil {
			targetDeviceID = candidate.Device.DeviceID
		}
		if !uuidPattern.MatchString(targetDeviceID) {
			continue
		}
		if _, ok := seenDevices[targetDeviceID]; !ok {
			seenDevices[targetDeviceID] = struct{}{}
			deviceIDs = append(deviceIDs, targetDeviceID)
		}
		ptKey := deviceKey{deviceID: targetDeviceID, key: candidate.TelemetryKey}
		if candidate.SourcePath == SourcePathHistoryReplay {
			if _, ok := seenReplayPoints[ptKey]; !ok {
				seenReplayPoints[ptKey] = struct{}{}
				replayPointDeviceIDs = append(replayPointDeviceIDs, ptKey.deviceID)
				replayPointKeys = append(replayPointKeys, ptKey.key)
			}
		}
		if _, ok := seenBatchPoints[ptKey]; !ok {
			seenBatchPoints[ptKey] = struct{}{}
			batchPointDeviceIDs = append(batchPointDeviceIDs, ptKey.deviceID)
			batchPointKeys = append(batchPointKeys, ptKey.key)
		}
	}

	slices.SortFunc(partitions, func(left, right sourcePartitionKey) int {
		if cmp := strings.Compare(left.sourceID, right.sourceID); cmp != 0 {
			return cmp
		}
		return strings.Compare(left.partition, right.partition)
	})
	posSourceIDs := make([]string, len(partitions))
	posPartitions := make([]string, len(partitions))
	for index, part := range partitions {
		posSourceIDs[index] = part.sourceID
		posPartitions[index] = part.partition
	}

	batch := &pgx.Batch{}
	batch.Queue(`
SELECT so.source_id, so.source_event_id::text, so.observation_id::text
FROM telemetry_runtime.source_observations so
JOIN unnest($1::text[], $2::uuid[]) AS req(source_id, source_event_id)
  ON so.source_id = req.source_id AND so.source_event_id = req.source_event_id
`, eventSourceIDs, eventIDs).Query(func(rows pgx.Rows) error {
		for rows.Next() {
			var sourceID, eventID, observationID string
			if err := rows.Scan(&sourceID, &eventID, &observationID); err != nil {
				return fmt.Errorf("scan telemetry source event receipt: %w", err)
			}
			state.seenEvents[sourceEventKey{sourceID: sourceID, eventID: eventID}] = observationID
		}
		return rows.Err()
	})

	batch.Queue(`
SELECT sp.source_id, sp.source_partition, sp.source_offset, sp.source_event_id::text
FROM telemetry_runtime.source_positions sp
JOIN unnest($1::text[], $2::text[]) AS req(source_id, source_partition)
  ON sp.source_id = req.source_id AND sp.source_partition = req.source_partition
ORDER BY sp.source_id, sp.source_partition
FOR UPDATE OF sp
`, posSourceIDs, posPartitions).Query(func(rows pgx.Rows) error {
		for rows.Next() {
			var sourceID, partition string
			var head SourcePositionHead
			if err := rows.Scan(&sourceID, &partition, &head.Offset, &head.EventID); err != nil {
				return fmt.Errorf("scan telemetry source position: %w", err)
			}
			state.positions[sourcePartitionKey{sourceID: sourceID, partition: partition}] = head
		}
		return rows.Err()
	})

	if len(deviceIDs) > 0 {
		batch.Queue(`
SELECT tenant_id::text, site_id::text, device_id::text
FROM telemetry_runtime.devices
WHERE device_id = ANY($1::uuid[])
`, deviceIDs).Query(func(rows pgx.Rows) error {
			for rows.Next() {
				var known ResolvedDevice
				if err := rows.Scan(&known.TenantID, &known.SiteID, &known.DeviceID); err != nil {
					return fmt.Errorf("scan telemetry Device identity: %w", err)
				}
				state.knownDevices[known.DeviceID] = known
			}
			return rows.Err()
		})
	}

	if len(replayPointDeviceIDs) > 0 {
		batch.Queue(`
SELECT p.device_id::text, p.telemetry_key, p.point_id::text, p.sensor_id::text, p.point_type,
       p.value_type, p.unit, p.counter_decrease_mode, p.counter_rollover_modulus, p.point_revision
FROM telemetry_runtime.points p
JOIN unnest($1::uuid[], $2::text[]) AS req(device_id, telemetry_key)
  ON p.device_id = req.device_id AND p.telemetry_key = req.telemetry_key
`, replayPointDeviceIDs, replayPointKeys).Query(func(rows pgx.Rows) error {
			for rows.Next() {
				var deviceID, key string
				var point ResolvedPoint
				if err := rows.Scan(
					&deviceID, &key, &point.PointID, &point.SensorID, &point.PointType, &point.ValueType,
					&point.Unit, &point.CounterDecreaseMode, &point.CounterRolloverModulus, &point.PointRevision,
				); err != nil {
					return fmt.Errorf("scan telemetry Point identity: %w", err)
				}
				state.knownPoints[deviceKey{deviceID: deviceID, key: key}] = point
			}
			return rows.Err()
		})
	}

	if len(batchPointDeviceIDs) > 0 {
		batch.Queue(`
SELECT f.device_id::text, f.telemetry_key,
       f.policy_revision, p.policy_revision, f.value_type, f.expected_unit,
       f.minimum_number, f.maximum_number, f.quality_checks,
       p.max_future_clock_skew_seconds, p.max_source_lag_seconds,
       f.fresh_within_seconds
FROM telemetry_runtime.freshness_policies f
JOIN telemetry_runtime.presence_policies p USING (device_id)
JOIN unnest($1::uuid[], $2::text[]) AS req(device_id, telemetry_key)
  ON f.device_id = req.device_id AND f.telemetry_key = req.telemetry_key
WHERE f.configured
`, batchPointDeviceIDs, batchPointKeys).Query(func(rows pgx.Rows) error {
			for rows.Next() {
				var deviceID, key string
				var policy ObservationPolicy
				var futureSeconds, lagSeconds, freshWithinSeconds int
				var checks []byte
				if err := rows.Scan(
					&deviceID, &key, &policy.Revision, &policy.PresencePolicyRevision, &policy.ValueType,
					&policy.Unit, &policy.MinimumNumber, &policy.MaximumNumber, &checks,
					&futureSeconds, &lagSeconds, &freshWithinSeconds,
				); err != nil {
					return fmt.Errorf("scan telemetry observation policy: %w", err)
				}
				policy.MaxFutureClockSkew = time.Duration(futureSeconds) * time.Second
				policy.MaxSourceLag = time.Duration(lagSeconds) * time.Second
				parsed, err := parseQualityChecks(checks)
				if err != nil {
					return fmt.Errorf("parse telemetry point quality checks: %w", err)
				}
				policy.QualityChecks = parsed
				state.policies[deviceKey{deviceID: deviceID, key: key}] = loadedPolicy{
					policy:             policy,
					freshWithinSeconds: freshWithinSeconds,
				}
			}
			return rows.Err()
		})

		queueLatestPointLookup(batch, batchPointDeviceIDs, batchPointKeys, state.latestByPoint)
	}

	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return nil, fmt.Errorf("prefetch telemetry batch facts: %w", err)
	}

	missingPeerKeys := collectMissingQualityPeers(state.policies, seenBatchPoints)
	if len(missingPeerKeys) > 0 {
		peerDeviceIDs := make([]string, len(missingPeerKeys))
		peerKeys := make([]string, len(missingPeerKeys))
		for index, peer := range missingPeerKeys {
			peerDeviceIDs[index] = peer.deviceID
			peerKeys[index] = peer.key
		}
		peerBatch := &pgx.Batch{}
		queueLatestPointLookup(peerBatch, peerDeviceIDs, peerKeys, state.latestByPoint)
		if err := tx.SendBatch(ctx, peerBatch).Close(); err != nil {
			return nil, fmt.Errorf("prefetch telemetry quality peer facts: %w", err)
		}
	}

	return state, nil
}

func queueLatestPointLookup(batch *pgx.Batch, deviceIDs, keys []string, target map[deviceKey]latestPointState) {
	batch.Queue(`
SELECT l.device_id::text, l.telemetry_key,
       l.value, l.quality, l.sampled_at, l.unchanged_since,
       l.health_status, l.health_at, fp.fresh_within_seconds
FROM telemetry_runtime.latest_accepted_telemetry l
LEFT JOIN telemetry_runtime.freshness_policies fp
  ON fp.device_id = l.device_id AND fp.telemetry_key = l.telemetry_key AND fp.configured
JOIN unnest($1::uuid[], $2::text[]) AS req(device_id, telemetry_key)
  ON l.device_id = req.device_id AND l.telemetry_key = req.telemetry_key
`, deviceIDs, keys).Query(func(rows pgx.Rows) error {
		for rows.Next() {
			var deviceID, key, quality string
			var rawValue []byte
			var sampledAt time.Time
			var unchangedSince, healthAt *time.Time
			var healthStatus *string
			var freshWithinSeconds *int
			if err := rows.Scan(
				&deviceID, &key, &rawValue, &quality, &sampledAt, &unchangedSince,
				&healthStatus, &healthAt, &freshWithinSeconds,
			); err != nil {
				return fmt.Errorf("scan latest telemetry point fact: %w", err)
			}
			entry := latestPointState{
				value:              append(json.RawMessage(nil), rawValue...),
				quality:            quality,
				sampledAt:          sampledAt.UTC(),
				healthStatus:       cloneString(healthStatus),
				freshWithinSeconds: cloneIntPointer(freshWithinSeconds),
			}
			if unchangedSince != nil && !unchangedSince.IsZero() {
				since := unchangedSince.UTC()
				entry.unchangedSince = &since
			}
			if healthAt != nil && !healthAt.IsZero() {
				recorded := healthAt.UTC()
				entry.healthAt = &recorded
			}
			target[deviceKey{deviceID: deviceID, key: key}] = entry
		}
		return rows.Err()
	})
}

func collectMissingQualityPeers(policies map[deviceKey]loadedPolicy, alreadyQueried map[deviceKey]struct{}) []deviceKey {
	missingSet := make(map[deviceKey]struct{})
	add := func(deviceID, key string) {
		if key == "" || !uuidPattern.MatchString(deviceID) {
			return
		}
		ref := deviceKey{deviceID: deviceID, key: key}
		if _, ok := alreadyQueried[ref]; ok {
			return
		}
		missingSet[ref] = struct{}{}
	}
	for _, loaded := range policies {
		checks := loaded.policy.QualityChecks
		if checks == nil {
			continue
		}
		for _, expect := range checks.Running {
			add(expect.DeviceID, expect.Key)
		}
		for _, input := range checks.Inputs {
			add(input.DeviceID, input.Key)
		}
		for _, ref := range collectPhysicsPeerRefs(checks) {
			add(ref.deviceID, ref.key)
		}
	}
	if len(missingSet) == 0 {
		return nil
	}
	missing := make([]deviceKey, 0, len(missingSet))
	for ref := range missingSet {
		missing = append(missing, ref)
	}
	slices.SortFunc(missing, compareDeviceKeys)
	return missing
}

func collectPhysicsPeerRefs(checks *QualityChecks) []deviceKey {
	seen := make(map[deviceKey]struct{})
	refs := make([]deviceKey, 0)
	add := func(deviceID, key string) {
		if deviceID == "" || key == "" {
			return
		}
		ref := deviceKey{deviceID: deviceID, key: key}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	for _, rule := range checks.Physics {
		for _, peer := range rule.Peers {
			add(peer.DeviceID, peer.Key)
		}
		for _, expect := range rule.Running {
			add(expect.DeviceID, expect.Key)
		}
	}
	if checks.Uncertainty != nil {
		add(checks.Uncertainty.Supply.DeviceID, checks.Uncertainty.Supply.Key)
		add(checks.Uncertainty.Return.DeviceID, checks.Uncertainty.Return.Key)
		if checks.Uncertainty.Flow != nil {
			add(checks.Uncertainty.Flow.DeviceID, checks.Uncertainty.Flow.Key)
		}
		if checks.Uncertainty.Power != nil {
			add(checks.Uncertainty.Power.DeviceID, checks.Uncertainty.Power.Key)
		}
	}
	return refs
}

func evaluateBatchCandidates(
	candidates []ObservationCandidate,
	payloadSHAs []string,
	state *batchWorkingState,
	evaluate observationEvaluator,
	newEventID EventIDGenerator,
) ([]ObservationReceipt, *batchWritePlan, error) {
	receipts := make([]ObservationReceipt, len(candidates))
	plan := &batchWritePlan{
		maxReceivedAtByDevice: make(map[string]time.Time),
		reevalReceiptIndices:  make(map[string][]int),
	}
	deviceIndex := make(map[string]int)
	pointIndex := make(map[deviceKey]int)
	positionIndex := make(map[sourcePartitionKey]int)

	for index, candidate := range candidates {
		facts, existingID := assembleObservationFacts(candidate, state)
		candidateEvaluator := evaluate
		if candidate.EdgeReplay && candidate.SourcePath != SourcePathHistoryReplay {
			candidateEvaluator = EvaluateHistoricalObservation
		}
		decision := candidateEvaluator(candidate, facts, candidate.ReceivedAt)

		if !decision.AdvancePosition {
			evidenceID, err := newEventID(candidate.ReceivedAt)
			if err != nil {
				return nil, nil, fmt.Errorf("generate telemetry delivery evidence ID: %w", err)
			}
			plan.deliveryEvidences = append(plan.deliveryEvidences, deliveryEvidenceInsertRow{
				receiptIndex: index,
				evidenceID:   evidenceID,
				candidate:    candidate,
				decision:     decision,
				payloadSHA:   payloadSHAs[index],
			})
			receipts[index] = ObservationReceipt{
				ObservationID:  existingID,
				EvidenceID:     evidenceID,
				Status:         decision.Status,
				Quality:        decision.Quality,
				QualityReasons: decision.QualityReasons,
			}
			continue
		}

		observationID, err := newEventID(candidate.ReceivedAt)
		if err != nil {
			return nil, nil, fmt.Errorf("generate telemetry observation ID: %w", err)
		}
		historyPayload, historyDigest, err := encodeHistoryOutboxPayload(observationID, candidate, decision, payloadSHAs[index])
		if err != nil {
			return nil, nil, err
		}

		if decision.QuarantineReason != "" {
			quarantineID, err := newEventID(candidate.ReceivedAt)
			if err != nil {
				return nil, nil, fmt.Errorf("generate telemetry quarantine ID: %w", err)
			}
			evidence, err := json.Marshal(map[string]any{
				"schemaVersion":   1,
				"sourcePath":      string(candidate.SourcePath),
				"sourcePartition": candidate.Position.Partition,
				"sourceOffset":    candidate.Position.Offset,
				"sourceEventId":   candidate.Position.EventID,
				"payloadSha256":   payloadSHAs[index],
			})
			if err != nil {
				return nil, nil, fmt.Errorf("encode telemetry quarantine evidence: %w", err)
			}
			plan.quarantines = append(plan.quarantines, quarantineInsertRow{
				quarantineID: quarantineID,
				candidate:    candidate,
				decision:     decision,
				evidence:     evidence,
			})
		}

		if decision.EmitPresenceSignal {
			signalID, err := newEventID(candidate.ReceivedAt)
			if err != nil {
				return nil, nil, fmt.Errorf("generate telemetry Presence Signal ID: %w", err)
			}
			plan.presenceSignals = append(plan.presenceSignals, presenceSignalInsertRow{
				signalID:  signalID,
				candidate: candidate,
				decision:  decision,
			})
		}

		recordInMemoryAndPlanIdentity(candidate, decision, state, plan, deviceIndex, pointIndex)

		partKey := sourcePartitionKey{sourceID: candidate.SourceID, partition: candidate.Position.Partition}
		state.seenEvents[sourceEventKey{sourceID: candidate.SourceID, eventID: candidate.Position.EventID}] = observationID
		state.positions[partKey] = SourcePositionHead{Offset: candidate.Position.Offset, EventID: candidate.Position.EventID}
		if posIdx, exists := positionIndex[partKey]; exists {
			if candidate.Position.Offset > plan.positions[posIdx].candidate.Position.Offset {
				plan.positions[posIdx] = sourcePositionUpsertRow{candidate: candidate}
			}
		} else {
			positionIndex[partKey] = len(plan.positions)
			plan.positions = append(plan.positions, sourcePositionUpsertRow{candidate: candidate})
		}

		plan.observations = append(plan.observations, sourceObservationInsertRow{
			observationID: observationID,
			candidate:     candidate,
			decision:      decision,
			payloadSHA:    payloadSHAs[index],
		})
		plan.historyOutbox = append(plan.historyOutbox, historyOutboxInsertRow{
			observationID: observationID,
			payload:       historyPayload,
			digest:        historyDigest,
			receivedAt:    candidate.ReceivedAt,
		})

		updateLatestWorkingState(candidate, decision, state, plan)

		if decision.ReevaluateSnapshot {
			if prevMax, seen := plan.maxReceivedAtByDevice[decision.DeviceID]; !seen || candidate.ReceivedAt.After(prevMax) {
				plan.maxReceivedAtByDevice[decision.DeviceID] = candidate.ReceivedAt
			}
			if _, seen := plan.reevalReceiptIndices[decision.DeviceID]; !seen {
				plan.reevalDeviceOrder = append(plan.reevalDeviceOrder, decision.DeviceID)
			}
			plan.reevalReceiptIndices[decision.DeviceID] = append(plan.reevalReceiptIndices[decision.DeviceID], index)
		}

		receipts[index] = ObservationReceipt{
			ObservationID:    observationID,
			Status:           decision.Status,
			Quality:          decision.Quality,
			QualityReasons:   decision.QualityReasons,
			QuarantineReason: decision.QuarantineReason,
			DeviceID:         decision.DeviceID,
			PositionAdvanced: true,
		}
	}

	slices.Sort(plan.reevalDeviceOrder)
	return receipts, plan, nil
}

func assembleObservationFacts(candidate ObservationCandidate, state *batchWorkingState) (ObservationFacts, string) {
	facts := ObservationFacts{}
	existingID, seen := state.seenEvents[sourceEventKey{sourceID: candidate.SourceID, eventID: candidate.Position.EventID}]
	if seen {
		facts.EventAlreadySeen = true
	}
	if head, ok := state.positions[sourcePartitionKey{sourceID: candidate.SourceID, partition: candidate.Position.Partition}]; ok {
		headCopy := head
		facts.CurrentPosition = &headCopy
	}

	if candidate.SourcePath != SourcePathHistoryReplay {
		if candidate.Device == nil {
			return facts, existingID
		}
		facts.Device = candidate.Device
		facts.Point = candidate.Point
		if known, ok := state.knownDevices[candidate.Device.DeviceID]; ok && known != *candidate.Device {
			facts.DeviceConflict = true
		}
	} else {
		known, ok := state.knownDevices[candidate.ExternalID]
		if !ok {
			return facts, existingID
		}
		knownCopy := known
		facts.Device = &knownCopy
		if point, ok := state.knownPoints[deviceKey{deviceID: known.DeviceID, key: candidate.TelemetryKey}]; ok {
			pointCopy := point
			facts.Point = &pointCopy
		}
	}

	if facts.Device == nil || facts.DeviceConflict {
		return facts, existingID
	}

	deviceID := facts.Device.DeviceID
	pointKey := deviceKey{deviceID: deviceID, key: candidate.TelemetryKey}
	if loaded, ok := state.policies[pointKey]; ok {
		policyCopy := loaded.policy
		facts.Policy = &policyCopy
	}
	if latest, ok := state.latestByPoint[pointKey]; ok {
		sampledAt := latest.sampledAt.UTC()
		facts.LatestSampledAt = &sampledAt
		priorSample := PriorSample{Value: append(json.RawMessage(nil), latest.value...), SampledAt: sampledAt}
		if latest.unchangedSince != nil && !latest.unchangedSince.IsZero() {
			priorSample.UnchangedSince = latest.unchangedSince.UTC()
		}
		facts.PreviousSample = &priorSample
		if number, ok := numericJSON(latest.value); ok {
			prior := PriorNumeric{Value: number, SampledAt: sampledAt, UnchangedSince: priorSample.UnchangedSince}
			facts.Previous = &prior
		}
	}
	if facts.Policy != nil && facts.Policy.QualityChecks != nil {
		checks := facts.Policy.QualityChecks
		facts.Running, facts.RunningSince, facts.InputsUntrusted = assembleQualityContext(state.latestByPoint, checks, candidate.SampledAt)
		if len(checks.Physics) > 0 || checks.Uncertainty != nil {
			facts.Peers = assemblePhysicsPeers(state.latestByPoint, checks)
		}
	}
	return facts, existingID
}

func assembleQualityContext(latestByPoint map[deviceKey]latestPointState, checks *QualityChecks, at time.Time) (running bool, runningSince *time.Time, inputsUntrusted bool) {
	var latestSince time.Time
	for _, expect := range checks.Running {
		peer, ok := latestByPoint[deviceKey{deviceID: expect.DeviceID, key: expect.Key}]
		if !ok || peer.quality != string(QualityGood) || !jsonEquals(peer.value, expect.Equals) {
			continue
		}
		if peer.freshWithinSeconds != nil && *peer.freshWithinSeconds > 0 {
			staleAfter := time.Duration(*peer.freshWithinSeconds) * time.Second
			delta := at.Sub(peer.sampledAt.UTC())
			if delta > staleAfter || delta < -staleAfter {
				continue
			}
		}
		running = true
		transition := peer.sampledAt.UTC()
		if peer.unchangedSince != nil && !peer.unchangedSince.IsZero() {
			transition = peer.unchangedSince.UTC()
		}
		if latestSince.IsZero() || transition.After(latestSince) {
			latestSince = transition
		}
	}
	if running && !latestSince.IsZero() {
		runningSince = &latestSince
	}
	for _, input := range checks.Inputs {
		peer, ok := latestByPoint[deviceKey{deviceID: input.DeviceID, key: input.Key}]
		if !ok {
			continue
		}
		if peer.quality != string(QualityGood) || (peer.healthStatus != nil && (*peer.healthStatus == HealthBad || *peer.healthStatus == HealthSuspect)) {
			inputsUntrusted = true
			break
		}
	}
	return running, runningSince, inputsUntrusted
}

func assemblePhysicsPeers(latestByPoint map[deviceKey]latestPointState, checks *QualityChecks) map[string]PeerSample {
	refs := collectPhysicsPeerRefs(checks)
	peers := make(map[string]PeerSample, len(refs))
	for _, ref := range refs {
		peer, ok := latestByPoint[ref]
		if !ok {
			continue
		}
		sample := PeerSample{
			Text:      jsonText(peer.value),
			Good:      peer.quality == string(QualityGood),
			SampledAt: peer.sampledAt.UTC(),
		}
		if peer.unchangedSince != nil {
			since := peer.unchangedSince.UTC()
			sample.UnchangedSince = &since
		}
		if peer.freshWithinSeconds != nil && *peer.freshWithinSeconds > 0 {
			sample.StaleAfter = time.Duration(*peer.freshWithinSeconds) * time.Second
		}
		if number, ok := numericJSON(peer.value); ok {
			sample.Number = &number
		}
		peers[ref.deviceID+"/"+ref.key] = sample
	}
	return peers
}

func recordInMemoryAndPlanIdentity(
	candidate ObservationCandidate,
	decision ObservationDecision,
	state *batchWorkingState,
	plan *batchWritePlan,
	deviceIndex map[string]int,
	pointIndex map[deviceKey]int,
) {
	if candidate.Device == nil || decision.DeviceID == "" {
		return
	}
	device := *candidate.Device
	_, knownDevice := state.knownDevices[device.DeviceID]
	if !knownDevice {
		state.knownDevices[device.DeviceID] = device
	}
	if !candidate.EdgeReplay || !knownDevice {
		if _, queued := deviceIndex[device.DeviceID]; !queued {
			deviceIndex[device.DeviceID] = len(plan.devices)
			plan.devices = append(plan.devices, deviceInsertRow{device: device, receivedAt: candidate.ReceivedAt})
		}
	}
	if candidate.EdgeReplay || candidate.Point == nil || decision.PointID == "" {
		return
	}
	point := *candidate.Point
	ptKey := deviceKey{deviceID: device.DeviceID, key: candidate.TelemetryKey}
	state.knownPoints[ptKey] = point
	row := pointUpsertRow{device: device, key: candidate.TelemetryKey, point: point, receivedAt: candidate.ReceivedAt}
	if idx, queued := pointIndex[ptKey]; queued {
		plan.points[idx] = row
	} else {
		pointIndex[ptKey] = len(plan.points)
		plan.points = append(plan.points, row)
	}
}

func updateLatestWorkingState(candidate ObservationCandidate, decision ObservationDecision, state *batchWorkingState, plan *batchWritePlan) {
	if decision.ReplaceLatest {
		pointKey := deviceKey{deviceID: decision.DeviceID, key: candidate.TelemetryKey}
		var freshWithin *int
		if loaded, ok := state.policies[pointKey]; ok && loaded.freshWithinSeconds > 0 {
			fw := loaded.freshWithinSeconds
			freshWithin = &fw
		} else if existing, ok := state.latestByPoint[pointKey]; ok {
			freshWithin = cloneIntPointer(existing.freshWithinSeconds)
		}
		var unchangedPtr *time.Time
		if !decision.UnchangedSince.IsZero() {
			unchanged := decision.UnchangedSince.UTC()
			unchangedPtr = &unchanged
		}
		var healthStatusPtr *string
		if decision.HealthStatus != "" {
			status := decision.HealthStatus
			healthStatusPtr = &status
		}
		receivedAt := candidate.ReceivedAt.UTC()
		state.latestByPoint[pointKey] = latestPointState{
			value:              append(json.RawMessage(nil), candidate.Value...),
			quality:            string(decision.Quality),
			sampledAt:          candidate.SampledAt.UTC(),
			unchangedSince:     unchangedPtr,
			healthStatus:       healthStatusPtr,
			healthAt:           &receivedAt,
			freshWithinSeconds: freshWithin,
		}
		plan.latestUpdates = append(plan.latestUpdates, latestUpdateOp{
			replaceLatest: true,
			candidate:     candidate,
			decision:      decision,
		})
		return
	}
	if decision.RecordHealth {
		pointKey := deviceKey{deviceID: decision.DeviceID, key: candidate.TelemetryKey}
		if existing, ok := state.latestByPoint[pointKey]; ok {
			if existing.healthAt == nil || !candidate.SampledAt.Before(*existing.healthAt) {
				status := decision.HealthStatus
				sampledAt := candidate.SampledAt.UTC()
				existing.healthStatus = &status
				existing.healthAt = &sampledAt
				state.latestByPoint[pointKey] = existing
			}
		}
		plan.latestUpdates = append(plan.latestUpdates, latestUpdateOp{
			replaceLatest: false,
			candidate:     candidate,
			decision:      decision,
		})
	}
}

func persistBatchWritePlan(ctx context.Context, tx pgx.Tx, plan *batchWritePlan, receipts []ObservationReceipt) error {
	batch := &pgx.Batch{}

	queueMultiRowInsert(
		batch,
		`INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, updated_at) VALUES `,
		` ON CONFLICT (device_id) DO NOTHING`,
		len(plan.devices),
		4,
		func(base int) string {
			return fmt.Sprintf("($%d::uuid, $%d::uuid, $%d::uuid, $%d)", base+1, base+2, base+3, base+4)
		},
		func(args []any, index int) []any {
			row := plan.devices[index]
			return append(args, row.device.DeviceID, row.device.TenantID, row.device.SiteID, row.receivedAt)
		},
	)

	queueMultiRowInsert(
		batch,
		`INSERT INTO telemetry_runtime.points (
  device_id, telemetry_key, tenant_id, site_id, point_id, sensor_id, point_type, value_type, unit,
  counter_decrease_mode, counter_rollover_modulus, point_revision, updated_at
) VALUES `,
		` ON CONFLICT (device_id, telemetry_key) DO UPDATE SET
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
  IS DISTINCT FROM (EXCLUDED.point_id, EXCLUDED.point_revision)`,
		len(plan.points),
		13,
		func(base int) string {
			return fmt.Sprintf(
				"($%d::uuid, $%d, $%d::uuid, $%d::uuid, $%d::uuid, $%d::uuid, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11, base+12, base+13,
			)
		},
		func(args []any, index int) []any {
			row := plan.points[index]
			return append(args,
				row.device.DeviceID, row.key, row.device.TenantID, row.device.SiteID,
				row.point.PointID, row.point.SensorID, row.point.PointType, row.point.ValueType,
				row.point.Unit, row.point.CounterDecreaseMode, row.point.CounterRolloverModulus,
				row.point.PointRevision, row.receivedAt,
			)
		},
	)

	queueMultiRowInsert(
		batch,
		`INSERT INTO telemetry_runtime.source_positions (
  source_id, source_partition, source_offset, source_event_id,
  observed_at, updated_at
) VALUES `,
		` ON CONFLICT (source_id, source_partition) DO UPDATE SET
  source_offset = EXCLUDED.source_offset,
  source_event_id = EXCLUDED.source_event_id,
  observed_at = EXCLUDED.observed_at,
  updated_at = EXCLUDED.updated_at
WHERE telemetry_runtime.source_positions.source_offset < EXCLUDED.source_offset`,
		len(plan.positions),
		6,
		func(base int) string {
			return fmt.Sprintf("($%d, $%d, $%d, $%d::uuid, $%d, $%d)", base+1, base+2, base+3, base+4, base+5, base+6)
		},
		func(args []any, index int) []any {
			candidate := plan.positions[index].candidate
			return append(args,
				candidate.SourceID, candidate.Position.Partition, candidate.Position.Offset,
				candidate.Position.EventID, candidate.SampledAt, candidate.ReceivedAt,
			)
		},
	)

	queueMultiRowInsert(
		batch,
		`INSERT INTO telemetry_runtime.source_observations (
  observation_id, tenant_id, source_id, source_event_id, source_partition,
  source_offset, source_path, device_id, point_id, sensor_id, telemetry_key, value, value_type, unit,
  sampled_at, received_at, acceptance_status, quality, quality_reasons,
  payload_sha256, created_at
) VALUES `,
		"",
		len(plan.observations),
		20,
		func(base int) string {
			return fmt.Sprintf(
				"($%d::uuid, $%d::uuid, $%d, $%d::uuid, $%d, $%d, $%d, $%d::uuid, $%d::uuid, $%d::uuid, $%d, $%d::jsonb, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10,
				base+11, base+12, base+13, base+14, base+15, base+16, base+17, base+18, base+19, base+20, base+16,
			)
		},
		func(args []any, index int) []any {
			row := plan.observations[index]
			candidate, decision := row.candidate, row.decision
			var value any
			if decision.Status == ObservationAccepted || (decision.Status == ObservationOutOfOrder && decision.PointID != "") {
				value = []byte(candidate.Value)
			}
			return append(args,
				row.observationID, nullableStringArg(decision.TenantID), candidate.SourceID,
				candidate.Position.EventID, candidate.Position.Partition, candidate.Position.Offset,
				string(candidate.SourcePath), nullableStringArg(decision.DeviceID),
				nullableStringArg(decision.PointID), nullableStringArg(decision.SensorID),
				candidate.TelemetryKey, value, candidate.ValueType, candidate.Unit,
				candidate.SampledAt, candidate.ReceivedAt, string(decision.Status),
				string(decision.Quality), qualityReasonStrings(decision.QualityReasons), row.payloadSHA,
			)
		},
	)

	queueMultiRowInsert(
		batch,
		`INSERT INTO telemetry_runtime.telemetry_history_outbox (
  event_id, payload, outbox_payload_sha256, delivery_state, available_at,
  attempts, last_error_code, lease_id, leased_until, published_at, created_at
) VALUES `,
		"",
		len(plan.historyOutbox),
		4,
		func(base int) string {
			return fmt.Sprintf("($%d::uuid, $%d::jsonb, $%d, 'PENDING', $%d, 0, NULL, NULL, NULL, NULL, $%d)", base+1, base+2, base+3, base+4, base+4)
		},
		func(args []any, index int) []any {
			row := plan.historyOutbox[index]
			return append(args, row.observationID, row.payload, row.digest, row.receivedAt)
		},
	)

	queueMultiRowInsert(
		batch,
		`INSERT INTO telemetry_runtime.ingest_quarantine (
  quarantine_id, tenant_id, source_id, external_entity_type, external_id,
  device_id, telemetry_key, reason_code, evidence, detected_at, resolved_at, resolution
) VALUES `,
		"",
		len(plan.quarantines),
		10,
		func(base int) string {
			return fmt.Sprintf(
				"($%d::uuid, $%d::uuid, $%d, $%d, $%d, $%d::uuid, $%d, $%d, $%d::jsonb, $%d, NULL, NULL)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10,
			)
		},
		func(args []any, index int) []any {
			row := plan.quarantines[index]
			return append(args,
				row.quarantineID, nullableStringArg(row.decision.TenantID), row.candidate.SourceID,
				row.candidate.ExternalEntityType, row.candidate.ExternalID,
				nullableStringArg(row.decision.DeviceID), row.candidate.TelemetryKey,
				string(row.decision.QuarantineReason), row.evidence, row.candidate.ReceivedAt,
			)
		},
	)

	for _, updateOp := range plan.latestUpdates {
		if updateOp.replaceLatest {
			queueUpsertLatestObservation(batch, updateOp.candidate, updateOp.decision)
		} else {
			queueUpdatePointHealth(batch, updateOp.candidate, updateOp.decision)
		}
	}

	queueMultiRowInsert(
		batch,
		`INSERT INTO telemetry_runtime.presence_signals (
  signal_id, device_id, signal_type, observed_at, received_at, accepted,
  policy_revision, source_event_id, created_at
) VALUES `,
		"",
		len(plan.presenceSignals),
		6,
		func(base int) string {
			return fmt.Sprintf("($%d::uuid, $%d::uuid, 'SOURCE_ACTIVITY', $%d, $%d, true, $%d, $%d::uuid, $%d)", base+1, base+2, base+3, base+4, base+5, base+6, base+4)
		},
		func(args []any, index int) []any {
			row := plan.presenceSignals[index]
			return append(args,
				row.signalID, row.decision.DeviceID, row.candidate.SampledAt,
				row.candidate.ReceivedAt, row.decision.PresencePolicyRevision, row.candidate.Position.EventID,
			)
		},
	)

	for _, evidence := range plan.deliveryEvidences {
		queueSourceDeliveryEvidence(batch, evidence, &receipts[evidence.receiptIndex].EvidenceID)
	}

	if batch.Len() == 0 {
		return nil
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("persist telemetry observation batch: %w", err)
	}
	return nil
}

func queueMultiRowInsert(
	batch *pgx.Batch,
	prefix, suffix string,
	count, argsPerRow int,
	formatRow func(base int) string,
	appendArgs func(args []any, index int) []any,
) {
	if count == 0 {
		return
	}
	args := make([]any, 0, count*argsPerRow)
	var sql strings.Builder
	sql.WriteString(prefix)
	for index := 0; index < count; index++ {
		if index > 0 {
			sql.WriteString(", ")
		}
		sql.WriteString(formatRow(index * argsPerRow))
		args = appendArgs(args, index)
	}
	sql.WriteString(suffix)
	batch.Queue(sql.String(), args...)
}

func queueSourceDeliveryEvidence(batch *pgx.Batch, row deliveryEvidenceInsertRow, persistedID *string) {
	reason := QualityReasonOutOfOrder
	if len(row.decision.QualityReasons) > 0 {
		reason = row.decision.QualityReasons[0]
	}
	batch.Queue(`
INSERT INTO telemetry_runtime.source_delivery_evidence (
  evidence_id, tenant_id, source_id, source_event_id, source_partition,
  source_offset, source_path, delivery_status, quality_reason, payload_sha256, detected_at
) VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (
  source_id, source_event_id, source_partition, source_offset,
  delivery_status, quality_reason, payload_sha256
) DO UPDATE SET detected_at = LEAST(telemetry_runtime.source_delivery_evidence.detected_at, EXCLUDED.detected_at)
RETURNING evidence_id::text
`, row.evidenceID, nullableStringArg(row.decision.TenantID), row.candidate.SourceID,
		row.candidate.Position.EventID, row.candidate.Position.Partition, row.candidate.Position.Offset,
		string(row.candidate.SourcePath), string(row.decision.Status), string(reason),
		row.payloadSHA, row.candidate.ReceivedAt,
	).QueryRow(func(scanned pgx.Row) error {
		return scanned.Scan(persistedID)
	})
}

func queueUpsertLatestObservation(batch *pgx.Batch, candidate ObservationCandidate, decision ObservationDecision) {
	var unchanged any
	if !decision.UnchangedSince.IsZero() {
		unchanged = decision.UnchangedSince.UTC()
	}
	batch.Queue(`
INSERT INTO telemetry_runtime.latest_accepted_telemetry (
  device_id, telemetry_key, business_revision, value, value_type, unit, sampled_at,
  received_at, freshness, quality, quality_reasons, policy_revision, updated_at,
  unchanged_since, health_status, health_score, health_reasons, health_at, measurement
) VALUES ($1::uuid, $2, 1, $3::jsonb, $4, $5, $6, $7, 'FRESH', $8, $9, $10, $7, $11, $12, $13, $14, $7, $15::jsonb)
ON CONFLICT (device_id, telemetry_key) DO UPDATE SET
  value = EXCLUDED.value,
  value_type = EXCLUDED.value_type,
  unit = EXCLUDED.unit,
  sampled_at = EXCLUDED.sampled_at,
  received_at = EXCLUDED.received_at,
  quality = EXCLUDED.quality,
  quality_reasons = EXCLUDED.quality_reasons,
  policy_revision = EXCLUDED.policy_revision,
  updated_at = EXCLUDED.updated_at,
  unchanged_since = EXCLUDED.unchanged_since,
  health_status = EXCLUDED.health_status,
  health_score = EXCLUDED.health_score,
  health_reasons = EXCLUDED.health_reasons,
  health_at = EXCLUDED.health_at,
  measurement = EXCLUDED.measurement
WHERE telemetry_runtime.latest_accepted_telemetry.sampled_at <= EXCLUDED.sampled_at
`, decision.DeviceID, candidate.TelemetryKey, []byte(candidate.Value), candidate.ValueType, candidate.Unit,
		candidate.SampledAt, candidate.ReceivedAt, string(decision.Quality), qualityReasonStrings(decision.QualityReasons), decision.PolicyRevision,
		unchanged, decision.HealthStatus, decision.HealthScore, qualityReasonStrings(decision.HealthReasons), measurementArg(decision.Measurement))
}

// queueUpdatePointHealth records a range rejection against the point without replacing
// the last trusted value. The displayed number stays; its health becomes BAD.
func queueUpdatePointHealth(batch *pgx.Batch, candidate ObservationCandidate, decision ObservationDecision) {
	batch.Queue(`
UPDATE telemetry_runtime.latest_accepted_telemetry
SET health_status = $3, health_score = $4, health_reasons = $5, health_at = $6
WHERE device_id = $1::uuid AND telemetry_key = $2
  AND (health_at IS NULL OR health_at <= $6)
`, decision.DeviceID, candidate.TelemetryKey, decision.HealthStatus, decision.HealthScore, qualityReasonStrings(decision.HealthReasons), candidate.SampledAt.UTC())
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

func numericJSON(raw []byte) (float64, bool) {
	kind, number, ok := observationValueType(raw)
	return number, ok && kind == "NUMBER"
}

func measurementArg(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func nullableStringArg(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func compareDeviceKeys(left, right deviceKey) int {
	if cmp := strings.Compare(left.deviceID, right.deviceID); cmp != 0 {
		return cmp
	}
	return strings.Compare(left.key, right.key)
}

func jsonText(raw []byte) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

func jsonEquals(raw []byte, want string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return typed == want
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64) == want
	case bool:
		if want == "true" {
			return typed
		}
		if want == "false" {
			return !typed
		}
	}
	return false
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

var (
	_ ObservationAcceptor           = (*PostgresStore)(nil)
	_ HistoricalObservationAcceptor = (*PostgresStore)(nil)
)
