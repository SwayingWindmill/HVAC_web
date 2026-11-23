package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	resendCursorSchemaVersion = 1
	defaultResendBatchSize    = 100
	resendReplayMetricName    = "hvac_edge_mqtt_replay_batches_total"
	resendReplayMetricHelp    = "Edge store-and-forward telemetry batches replayed after reconnect."
)

// ResendPriorityOrder defines the strict drain sequence used when replaying
// offline FileTimedata records after MQTT reconnect.
var ResendPriorityOrder = []edgecontrol.DataPriority{
	edgecontrol.PriorityVeryHigh,
	edgecontrol.PriorityHigh,
	edgecontrol.PriorityMedium,
	edgecontrol.PriorityLow,
}

// ResendCursorState persists the highest committed Timedata sequence per priority
// so mid-replay crashes resume without advancing past unacknowledged lower-priority records.
type ResendCursorState struct {
	SchemaVersion   int                                   `json:"schemaVersion"`
	ByPriority      map[edgecontrol.DataPriority]uint64   `json:"byPriority"`
	AckedByPriority map[edgecontrol.DataPriority][]uint64 `json:"ackedByPriority,omitempty"`
	UpdatedAt       time.Time                             `json:"updatedAt,omitempty"`
}

// PublishedObservation identifies a Timedata sample confirmed flushed live over MQTT.
type PublishedObservation struct {
	Address    string
	ObservedAt time.Time
}

// TimedataResendPointMeta resolves envelope routing and priority for a channel address.
type TimedataResendPointMeta struct {
	DeviceID  string
	PointCode string
	Unit      string
	Priority  edgecontrol.DataPriority
}

// TimedataResendConfig configures TimedataResendQueue.
type TimedataResendConfig struct {
	GatewayID     string
	Timedata      *edgecontrol.FileTimedata
	CursorPath    string
	BatchSize     int
	Metrics       *observability.Registry
	PointMetadata func(address string) TimedataResendPointMeta
	Publish       func(ctx context.Context, topic string, payload []byte) error
}

// TimedataDrainResult summarizes batches and records replayed during a Drain pass.
type TimedataDrainResult struct {
	BatchesReplayed int
	RecordsReplayed int
	ByPriority      map[edgecontrol.DataPriority]int
}

// TimedataResendQueue replays buffered FileTimedata records across MQTT reconnects
// in strict priority order (VERY_HIGH -> HIGH -> MEDIUM -> LOW) with atomic cursor persistence.
type TimedataResendQueue struct {
	mu            sync.Mutex
	drainMu       sync.Mutex
	gatewayID     string
	timedata      *edgecontrol.FileTimedata
	cursorPath    string
	batchSize     int
	metrics       *observability.Registry
	pointMetadata func(address string) TimedataResendPointMeta
	publish       func(ctx context.Context, topic string, payload []byte) error
	cursor        ResendCursorState
	ackedSeqs     map[uint64]edgecontrol.DataPriority
	onlineSynced  bool
}

// NewTimedataResendQueue constructs and loads a persistent priority resend queue.
func NewTimedataResendQueue(cfg TimedataResendConfig) (*TimedataResendQueue, error) {
	gatewayID := strings.TrimSpace(cfg.GatewayID)
	if gatewayID == "" {
		return nil, errors.New("timedata resend queue gatewayId is required")
	}
	if cfg.Timedata == nil {
		return nil, errors.New("timedata resend queue FileTimedata is required")
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultResendBatchSize
	}
	q := &TimedataResendQueue{
		gatewayID:     gatewayID,
		timedata:      cfg.Timedata,
		cursorPath:    strings.TrimSpace(cfg.CursorPath),
		batchSize:     batchSize,
		metrics:       cfg.Metrics,
		pointMetadata: cfg.PointMetadata,
		publish:       cfg.Publish,
		cursor:        defaultResendCursorState(),
		ackedSeqs:     make(map[uint64]edgecontrol.DataPriority),
	}
	if err := q.loadCursor(); err != nil {
		return nil, err
	}
	return q, nil
}

func defaultResendCursorState() ResendCursorState {
	byPriority := make(map[edgecontrol.DataPriority]uint64, len(ResendPriorityOrder))
	for _, p := range ResendPriorityOrder {
		byPriority[p] = 0
	}
	return ResendCursorState{
		SchemaVersion: resendCursorSchemaVersion,
		ByPriority:    byPriority,
	}
}

func (q *TimedataResendQueue) loadCursor() error {
	if q == nil || q.cursorPath == "" {
		return nil
	}
	raw, err := os.ReadFile(q.cursorPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read resend cursor: %w", err)
	}
	var state ResendCursorState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("decode resend cursor: %w", err)
	}
	normalized := defaultResendCursorState()
	for _, p := range ResendPriorityOrder {
		if seq, ok := state.ByPriority[p]; ok {
			normalized.ByPriority[p] = seq
		}
	}
	acked := make(map[uint64]edgecontrol.DataPriority)
	for _, p := range ResendPriorityOrder {
		for _, seq := range state.AckedByPriority[p] {
			if seq > normalized.ByPriority[p] {
				acked[seq] = p
			}
		}
	}
	normalized.AckedByPriority = encodeAckedByPriority(acked)
	normalized.UpdatedAt = state.UpdatedAt
	q.mu.Lock()
	q.cursor = normalized
	q.ackedSeqs = acked
	q.mu.Unlock()
	return nil
}

// IsSynced reports whether the queue completed its reconnect replay drain and is
// caught up with the live session.
func (q *TimedataResendQueue) IsSynced() bool {
	if q == nil {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.onlineSynced
}

// CursorSnapshot returns a thread-safe copy of the persisted per-priority sequence cursors.
func (q *TimedataResendQueue) CursorSnapshot() ResendCursorState {
	if q == nil {
		return defaultResendCursorState()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return cloneResendCursorState(q.cursor)
}

// SetOnlineSynced marks whether the live MQTT session is currently online with no
// remaining offline backlog so live online publishes can advance the baseline cursor.
func (q *TimedataResendQueue) SetOnlineSynced(synced bool) {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.onlineSynced = synced
	q.mu.Unlock()
}

// LatestTimedataSequence returns the highest sequence number currently stored in Timedata.
func (q *TimedataResendQueue) LatestTimedataSequence() uint64 {
	if q == nil || q.timedata == nil {
		return 0
	}
	return q.timedata.LatestSequence()
}

// SyncOnlineBaseline advances all priority cursors to LatestSequence when live telemetry
// has been cleanly admitted while online. Calling while offline is a no-op.
func (q *TimedataResendQueue) SyncOnlineBaseline() error {
	if q == nil || q.timedata == nil {
		return nil
	}
	latest := q.timedata.LatestSequence()
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.onlineSynced {
		return nil
	}
	changed := false
	for _, p := range ResendPriorityOrder {
		if latest > q.cursor.ByPriority[p] {
			q.cursor.ByPriority[p] = latest
			changed = true
		}
	}
	if !changed {
		return nil
	}
	q.pruneAckedLocked()
	q.cursor.UpdatedAt = time.Now().UTC()
	return q.persistCursorLocked()
}

// MarkPublishedObservations records sequences whose live MQTT envelopes were flushed
// and advances each priority cursor contiguously without skipping unsent records.
func (q *TimedataResendQueue) MarkPublishedObservations(obs []PublishedObservation) error {
	if q == nil || q.timedata == nil || len(obs) == 0 {
		return nil
	}

	q.drainMu.Lock()
	defer q.drainMu.Unlock()

	q.mu.Lock()
	curSnapshot := make(map[edgecontrol.DataPriority]uint64, len(ResendPriorityOrder))
	for _, p := range ResendPriorityOrder {
		curSnapshot[p] = q.cursor.ByPriority[p]
	}
	q.mu.Unlock()

	touchedPriorities := make(map[edgecontrol.DataPriority]struct{})
	newlyAcked := make(map[uint64]edgecontrol.DataPriority, len(obs))
	for _, item := range obs {
		addr := strings.TrimSpace(item.Address)
		if addr == "" || item.ObservedAt.IsZero() {
			continue
		}
		priority := q.resolveMeta(addr).Priority
		afterSeq := curSnapshot[priority]
		seq, ok := q.timedata.LookupSequence(addr, item.ObservedAt, afterSeq)
		if !ok || seq <= afterSeq {
			continue
		}
		newlyAcked[seq] = priority
		touchedPriorities[priority] = struct{}{}
	}
	if len(newlyAcked) == 0 {
		return nil
	}

	q.mu.Lock()
	addedAck := false
	for seq, prio := range newlyAcked {
		if seq > q.cursor.ByPriority[prio] {
			if _, exists := q.ackedSeqs[seq]; !exists {
				q.ackedSeqs[seq] = prio
				addedAck = true
			}
		}
	}
	ackedSnapshot := make(map[uint64]struct{}, len(q.ackedSeqs))
	for seq := range q.ackedSeqs {
		ackedSnapshot[seq] = struct{}{}
	}
	for p := range touchedPriorities {
		curSnapshot[p] = q.cursor.ByPriority[p]
	}
	q.mu.Unlock()

	advanced := make(map[edgecontrol.DataPriority]uint64)
	for _, p := range ResendPriorityOrder {
		if _, touched := touchedPriorities[p]; !touched {
			continue
		}
		cur := curSnapshot[p]
		for {
			recs, err := q.timedata.QueryAfterSequence(cur, q.batchSize, func(rec edgecontrol.TimedataRecord) bool {
				return q.resolveMeta(rec.Address).Priority == p
			})
			if err != nil {
				return err
			}
			if len(recs) == 0 {
				break
			}
			progressed := false
			stoppedOnUnacked := false
			for _, rec := range recs {
				if _, ok := ackedSnapshot[rec.Sequence]; !ok {
					stoppedOnUnacked = true
					break
				}
				cur = rec.Sequence
				progressed = true
			}
			if stoppedOnUnacked || !progressed {
				break
			}
		}
		if cur > curSnapshot[p] {
			advanced[p] = cur
		}
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	cursorChanged := false
	for p, seq := range advanced {
		if seq > q.cursor.ByPriority[p] {
			q.cursor.ByPriority[p] = seq
			cursorChanged = true
		}
	}
	if !addedAck && !cursorChanged {
		return nil
	}
	q.pruneAckedLocked()
	q.cursor.UpdatedAt = time.Now().UTC()
	return q.persistCursorLocked()
}

// Drain replays all buffered FileTimedata records ahead of their per-priority cursors,
// strictly exhausting VERY_HIGH before HIGH, HIGH before MEDIUM, and MEDIUM before LOW.
// Cursor updates are committed atomically only after each published batch succeeds.
func (q *TimedataResendQueue) Drain(ctx context.Context) (TimedataDrainResult, error) {
	return q.drainUpTo(ctx, 0, false)
}

// DrainUpTo replays buffered FileTimedata records up to maxSequence so reconnect catch-up
// never steals live samples recorded after the connection came up.
func (q *TimedataResendQueue) DrainUpTo(ctx context.Context, maxSequence uint64) (TimedataDrainResult, error) {
	return q.drainUpTo(ctx, maxSequence, true)
}

func (q *TimedataResendQueue) drainUpTo(ctx context.Context, maxSequence uint64, bounded bool) (TimedataDrainResult, error) {
	result := TimedataDrainResult{
		ByPriority: make(map[edgecontrol.DataPriority]int, len(ResendPriorityOrder)),
	}
	if q == nil || q.timedata == nil {
		return result, nil
	}
	if q.publish == nil {
		return result, errors.New("timedata resend queue publish callback is required")
	}

	q.drainMu.Lock()
	defer q.drainMu.Unlock()

	if bounded && maxSequence == 0 {
		q.SetOnlineSynced(true)
		return result, nil
	}

	topic := "hvac/v1/" + q.gatewayID + "/up/telemetry"
	for _, priority := range ResendPriorityOrder {
		for {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			afterSeq := q.cursorForPriority(priority)
			records, err := q.timedata.QueryAfterSequence(afterSeq, q.batchSize, func(rec edgecontrol.TimedataRecord) bool {
				if bounded && rec.Sequence > maxSequence {
					return false
				}
				return q.resolveMeta(rec.Address).Priority == priority
			})
			if err != nil {
				return result, err
			}
			if len(records) == 0 {
				break
			}

			var pageMaxSeq uint64
			unacked := make([]edgecontrol.TimedataRecord, 0, len(records))
			for _, rec := range records {
				if rec.Sequence > pageMaxSeq {
					pageMaxSeq = rec.Sequence
				}
				if !q.isSequenceAcked(rec.Sequence) {
					unacked = append(unacked, rec)
				}
			}
			if len(unacked) == 0 {
				if err := q.commitCursor(priority, pageMaxSeq); err != nil {
					return result, err
				}
				continue
			}

			batches := q.buildReplayBatches(unacked)
			for _, batch := range batches {
				if len(batch.devices) > 0 {
					envelope, err := q.buildReplayEnvelope(batch)
					if err != nil {
						return result, err
					}
					payload, err := json.Marshal(envelope)
					if err != nil {
						return result, fmt.Errorf("encode MQTT replay envelope: %w", err)
					}
					if err := q.publish(ctx, topic, payload); err != nil {
						return result, err
					}
					if q.metrics != nil {
						_ = q.metrics.AddCounter(
							resendReplayMetricName,
							resendReplayMetricHelp,
							map[string]string{"priority": string(priority)},
							1,
						)
					}
					result.BatchesReplayed++
					result.RecordsReplayed += batch.recordCount
					result.ByPriority[priority]++
				}
				if err := q.commitCursor(priority, batch.maxSequence); err != nil {
					return result, err
				}
			}
			if err := q.commitCursor(priority, pageMaxSeq); err != nil {
				return result, err
			}
		}
	}

	q.SetOnlineSynced(true)
	return result, nil
}

type replayBatchGroup struct {
	observedAt  time.Time
	maxSequence uint64
	recordCount int
	devices     map[string]*mqttEnvelopeDevice
}

func (q *TimedataResendQueue) buildReplayBatches(records []edgecontrol.TimedataRecord) []replayBatchGroup {
	groups := make([]replayBatchGroup, 0, len(records))
	var current *replayBatchGroup
	var seenPoints map[string]struct{}

	flush := func() {
		if current != nil {
			groups = append(groups, *current)
			current = nil
			seenPoints = nil
		}
	}

	for _, rec := range records {
		obs := rec.Sample.ObservedAt.UTC()
		if obs.IsZero() {
			obs = time.Now().UTC()
		}
		meta := q.resolveMeta(rec.Address)
		val, encodable := edgeTelemetryValue(rec.Sample.Value)
		pointKey := meta.DeviceID + "\x00" + meta.PointCode

		sameTimestamp := current != nil && current.observedAt.UnixMilli() == obs.UnixMilli()
		_, duplicatePoint := seenPoints[pointKey]
		if !sameTimestamp || duplicatePoint {
			flush()
			current = &replayBatchGroup{
				observedAt: obs,
				devices:    make(map[string]*mqttEnvelopeDevice),
			}
			seenPoints = make(map[string]struct{})
		}

		if rec.Sequence > current.maxSequence {
			current.maxSequence = rec.Sequence
		}
		current.recordCount++
		if !encodable || meta.DeviceID == "" || meta.PointCode == "" {
			continue
		}
		seenPoints[pointKey] = struct{}{}

		dev := current.devices[meta.DeviceID]
		if dev == nil {
			dev = &mqttEnvelopeDevice{
				DeviceID:        meta.DeviceID,
				DeviceTimestamp: obs.UnixMilli(),
			}
			current.devices[meta.DeviceID] = dev
		}
		quality := uint8(0)
		if rec.Sample.Quality != edgecontrol.QualityGood {
			quality = 1
		}
		var unitPtr *string
		if trimmed := strings.TrimSpace(meta.Unit); trimmed != "" {
			unitPtr = &trimmed
		}
		dev.Points = append(dev.Points, mqttEnvelopePoint{
			Code:    meta.PointCode,
			Value:   val,
			Quality: quality,
			Unit:    unitPtr,
		})
	}
	flush()
	return groups
}

func (q *TimedataResendQueue) buildReplayEnvelope(batch replayBatchGroup) (mqttTelemetryEnvelope, error) {
	deviceIDs := make([]string, 0, len(batch.devices))
	for id := range batch.devices {
		deviceIDs = append(deviceIDs, id)
	}
	sort.Strings(deviceIDs)

	devices := make([]mqttEnvelopeDevice, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		dev := *batch.devices[id]
		sort.Slice(dev.Points, func(i, j int) bool {
			return dev.Points[i].Code < dev.Points[j].Code
		})
		devices = append(devices, dev)
	}

	ts := batch.observedAt
	if ts.IsZero() || ts.UnixMilli() <= 0 {
		ts = time.Now().UTC()
	}
	messageID, err := newUUIDV7(ts)
	if err != nil {
		return mqttTelemetryEnvelope{}, err
	}
	return mqttTelemetryEnvelope{
		SchemaVersion: mqttTelemetryEnvelopeSchemaVersion,
		MessageID:     messageID,
		GatewayID:     q.gatewayID,
		Timestamp:     batch.observedAt.UTC().UnixMilli(),
		Sequence:      batch.maxSequence,
		Replay:        true,
		Payload:       mqttTelemetryPayload{Devices: devices},
	}, nil
}

func (q *TimedataResendQueue) resolveMeta(address string) TimedataResendPointMeta {
	var meta TimedataResendPointMeta
	if q.pointMetadata != nil {
		meta = q.pointMetadata(address)
	}
	if meta.DeviceID == "" || meta.PointCode == "" {
		if dev, ch, ok := strings.Cut(address, "/"); ok {
			if meta.DeviceID == "" {
				meta.DeviceID = dev
			}
			if meta.PointCode == "" {
				meta.PointCode = ch
			}
		}
	}
	meta.Priority = normalizeResendPriority(meta.Priority)
	return meta
}

func normalizeResendPriority(p edgecontrol.DataPriority) edgecontrol.DataPriority {
	switch p {
	case edgecontrol.PriorityVeryHigh, edgecontrol.PriorityHigh, edgecontrol.PriorityMedium, edgecontrol.PriorityLow:
		return p
	default:
		return edgecontrol.PriorityMedium
	}
}

func (q *TimedataResendQueue) isSequenceAcked(seq uint64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.ackedSeqs[seq]
	return ok
}

func (q *TimedataResendQueue) pruneAckedLocked() {
	for seq, prio := range q.ackedSeqs {
		if seq <= q.cursor.ByPriority[prio] {
			delete(q.ackedSeqs, seq)
		}
	}
	q.cursor.AckedByPriority = encodeAckedByPriority(q.ackedSeqs)
}

func encodeAckedByPriority(acked map[uint64]edgecontrol.DataPriority) map[edgecontrol.DataPriority][]uint64 {
	if len(acked) == 0 {
		return nil
	}
	out := make(map[edgecontrol.DataPriority][]uint64)
	for seq, prio := range acked {
		out[prio] = append(out[prio], seq)
	}
	for prio, seqs := range out {
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
		out[prio] = seqs
	}
	return out
}

func (q *TimedataResendQueue) cursorForPriority(priority edgecontrol.DataPriority) uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.cursor.ByPriority[priority]
}

func (q *TimedataResendQueue) commitCursor(priority edgecontrol.DataPriority, sequence uint64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if sequence <= q.cursor.ByPriority[priority] {
		return nil
	}
	q.cursor.ByPriority[priority] = sequence
	q.pruneAckedLocked()
	q.cursor.UpdatedAt = time.Now().UTC()
	return q.persistCursorLocked()
}

func (q *TimedataResendQueue) persistCursorLocked() error {
	if q.cursorPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(q.cursorPath), 0o700); err != nil {
		return fmt.Errorf("create resend cursor directory: %w", err)
	}
	return writeResendCursorAtomic(q.cursorPath, q.cursor)
}

func writeResendCursorAtomic(path string, state ResendCursorState) error {
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode resend cursor: %w", err)
	}
	payload = append(payload, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return fmt.Errorf("write temporary resend cursor: %w", err)
	}
	file, err := os.OpenFile(tmpPath, os.O_RDWR, 0o600)
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("open temporary resend cursor: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync temporary resend cursor: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temporary resend cursor: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit resend cursor: %w", err)
	}
	return nil
}

func cloneResendCursorState(src ResendCursorState) ResendCursorState {
	out := ResendCursorState{
		SchemaVersion: src.SchemaVersion,
		ByPriority:    make(map[edgecontrol.DataPriority]uint64, len(ResendPriorityOrder)),
		UpdatedAt:     src.UpdatedAt,
	}
	for _, p := range ResendPriorityOrder {
		out.ByPriority[p] = src.ByPriority[p]
		if len(src.AckedByPriority[p]) > 0 {
			if out.AckedByPriority == nil {
				out.AckedByPriority = make(map[edgecontrol.DataPriority][]uint64, len(src.AckedByPriority))
			}
			out.AckedByPriority[p] = append([]uint64(nil), src.AckedByPriority[p]...)
		}
	}
	return out
}

// Purge clears the resend queue's underlying timedata ring, in-memory state, and persisted cursor file.
func (q *TimedataResendQueue) Purge() error {
	if q == nil {
		return nil
	}
	q.drainMu.Lock()
	defer q.drainMu.Unlock()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.timedata != nil {
		if err := q.timedata.Purge(); err != nil {
			return err
		}
	}
	q.cursor = defaultResendCursorState()
	q.ackedSeqs = make(map[uint64]edgecontrol.DataPriority)
	q.onlineSynced = false
	if q.cursorPath != "" {
		for _, target := range []string{q.cursorPath, q.cursorPath + ".tmp"} {
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
