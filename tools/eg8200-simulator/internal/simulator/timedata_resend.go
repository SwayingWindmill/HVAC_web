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
	SchemaVersion int                                 `json:"schemaVersion"`
	ByPriority    map[edgecontrol.DataPriority]uint64 `json:"byPriority"`
	UpdatedAt     time.Time                           `json:"updatedAt,omitempty"`
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
	normalized.UpdatedAt = state.UpdatedAt
	q.mu.Lock()
	q.cursor = normalized
	q.mu.Unlock()
	return nil
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
	q.cursor.UpdatedAt = time.Now().UTC()
	return q.persistCursorLocked()
}

// Drain replays all buffered FileTimedata records ahead of their per-priority cursors,
// strictly exhausting VERY_HIGH before HIGH, HIGH before MEDIUM, and MEDIUM before LOW.
// Cursor updates are committed atomically only after each published batch succeeds.
func (q *TimedataResendQueue) Drain(ctx context.Context) (TimedataDrainResult, error) {
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

	topic := "hvac/v1/" + q.gatewayID + "/up/telemetry"
	for _, priority := range ResendPriorityOrder {
		for {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			afterSeq := q.cursorForPriority(priority)
			records, err := q.timedata.QueryAfterSequence(afterSeq, q.batchSize, func(rec edgecontrol.TimedataRecord) bool {
				return q.resolveMeta(rec.Address).Priority == priority
			})
			if err != nil {
				return result, err
			}
			if len(records) == 0 {
				break
			}

			batches := q.buildReplayBatches(records)
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
	}
	return out
}
