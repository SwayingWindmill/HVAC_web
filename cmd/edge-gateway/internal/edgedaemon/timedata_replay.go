package edgedaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	timedataReplaySchemaVersion    = 1
	defaultTimedataReplayBatchSize = 100
	maxReplayPointsPerBatch        = 512
)

// TimedataShedWindow describes an inclusive FileTimedata sequence range [FromSeq, ToSeq]
// that requires priority-ordered gap replay after GradedSpool shedding or capacity rejection.
type TimedataShedWindow struct {
	FromSeq uint64 `json:"fromSeq"`
	ToSeq   uint64 `json:"toSeq"`
}

// TimedataReplayPointMeta resolves MQTT device/point metadata and priority for a FileTimedata address.
type TimedataReplayPointMeta struct {
	DeviceID  string
	PointCode string
	Unit      string
	Priority  edgecontrol.DataPriority
}

// TimedataReplayCursorState persists replay progress and pending shed windows in stateDir/timedata-resend-cursor.json.
type TimedataReplayCursorState struct {
	SchemaVersion  int                                 `json:"schemaVersion"`
	AckedSequence  uint64                              `json:"ackedSequence"`
	ByPriority     map[edgecontrol.DataPriority]uint64 `json:"byPriority,omitempty"`
	PendingWindows []TimedataShedWindow                `json:"pendingWindows,omitempty"`
	UpdatedAt      time.Time                           `json:"updatedAt,omitempty"`
}

// TimedataReplayConfig configures a TimedataReplayManager.
type TimedataReplayConfig struct {
	GatewayID     string
	StateDir      string
	Timedata      *edgecontrol.FileTimedata
	BatchSize     int
	PointMetadata func(address string) TimedataReplayPointMeta
	NextSequence  func() uint64
	Now           func() time.Time
}

// TimedataReplayResult summarizes a single DrainBatch tick.
type TimedataReplayResult struct {
	BatchesReplayed int
	RecordsReplayed int
	ByPriority      map[edgecontrol.DataPriority]int
	HasMore         bool
}

// TimedataReplayManager coordinates priority-ordered FileTimedata gap replay across
// GradedSpool sheds and MQTT reconnects with durable atomic cursor persistence.
type TimedataReplayManager struct {
	mu            sync.Mutex
	drainMu       sync.Mutex
	gatewayID     string
	cursorPath    string
	timedata      *edgecontrol.FileTimedata
	batchSize     int
	pointMetadata func(address string) TimedataReplayPointMeta
	nextSequence  func() uint64
	now           func() time.Time
	state         TimedataReplayCursorState
}

// NewTimedataReplayManager constructs and recovers a TimedataReplayManager.
func NewTimedataReplayManager(cfg TimedataReplayConfig) (*TimedataReplayManager, error) {
	gatewayID := strings.TrimSpace(cfg.GatewayID)
	if gatewayID == "" {
		return nil, errors.New("timedata replay manager requires gatewayId")
	}
	if cfg.Timedata == nil {
		return nil, errors.New("timedata replay manager requires FileTimedata")
	}
	stateDir := strings.TrimSpace(cfg.StateDir)
	if stateDir == "" {
		return nil, errors.New("timedata replay manager requires stateDir")
	}
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return nil, fmt.Errorf("create timedata replay state directory: %w", err)
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultTimedataReplayBatchSize
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	mgr := &TimedataReplayManager{
		gatewayID:     gatewayID,
		cursorPath:    filepath.Join(stateDir, "timedata-resend-cursor.json"),
		timedata:      cfg.Timedata,
		batchSize:     batchSize,
		pointMetadata: cfg.PointMetadata,
		nextSequence:  cfg.NextSequence,
		now:           nowFn,
		state:         defaultTimedataReplayCursorState(),
	}
	if err := mgr.loadCursor(); err != nil {
		return nil, err
	}
	return mgr, nil
}

func defaultTimedataReplayCursorState() TimedataReplayCursorState {
	byPrio := make(map[edgecontrol.DataPriority]uint64, len(telemetryResendOrder))
	for _, p := range telemetryResendOrder {
		byPrio[p] = 0
	}
	return TimedataReplayCursorState{
		SchemaVersion: timedataReplaySchemaVersion,
		ByPriority:    byPrio,
	}
}

func (m *TimedataReplayManager) loadCursor() error {
	raw, err := os.ReadFile(m.cursorPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read timedata resend cursor: %w", err)
	}
	var loaded TimedataReplayCursorState
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return fmt.Errorf("decode timedata resend cursor: %w", err)
	}
	normalized := defaultTimedataReplayCursorState()
	normalized.AckedSequence = loaded.AckedSequence
	for _, p := range telemetryResendOrder {
		if seq, ok := loaded.ByPriority[p]; ok && seq > normalized.AckedSequence {
			normalized.ByPriority[p] = seq
		} else {
			normalized.ByPriority[p] = normalized.AckedSequence
		}
	}
	normalized.PendingWindows = coalesceShedWindows(loaded.PendingWindows, normalized.AckedSequence)
	normalized.UpdatedAt = loaded.UpdatedAt
	m.mu.Lock()
	m.state = normalized
	m.mu.Unlock()
	return nil
}

// CursorSnapshot returns a thread-safe copy of the persisted replay cursor state.
func (m *TimedataReplayManager) CursorSnapshot() TimedataReplayCursorState {
	if m == nil {
		return defaultTimedataReplayCursorState()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	byPrio := make(map[edgecontrol.DataPriority]uint64, len(telemetryResendOrder))
	for _, p := range telemetryResendOrder {
		byPrio[p] = m.state.ByPriority[p]
	}
	return TimedataReplayCursorState{
		SchemaVersion:  m.state.SchemaVersion,
		AckedSequence:  m.state.AckedSequence,
		ByPriority:     byPrio,
		PendingWindows: append([]TimedataShedWindow(nil), m.state.PendingWindows...),
		UpdatedAt:      m.state.UpdatedAt,
	}
}

// HasPendingReplay reports whether at least one shed gap window awaits FileTimedata replay.
func (m *TimedataReplayManager) HasPendingReplay() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.state.PendingWindows) > 0
}

// MarkShed records that GradedSpool shed or rejected telemetry and opens/extends
// a pending FileTimedata gap window ending at the current FileTimedata sequence.
func (m *TimedataReplayManager) MarkShed() error {
	if m == nil || m.timedata == nil {
		return nil
	}
	latest := m.timedata.LatestSequence()
	if latest == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	fromSeq := m.state.AckedSequence + 1
	if latest < fromSeq {
		return nil
	}
	nextWindows := append(append([]TimedataShedWindow(nil), m.state.PendingWindows...), TimedataShedWindow{
		FromSeq: fromSeq,
		ToSeq:   latest,
	})
	m.state.PendingWindows = coalesceShedWindows(nextWindows, m.state.AckedSequence)
	for _, p := range telemetryResendOrder {
		if m.state.ByPriority[p] < m.state.AckedSequence {
			m.state.ByPriority[p] = m.state.AckedSequence
		}
	}
	m.state.UpdatedAt = m.now().UTC()
	return m.persistCursorLocked()
}

// AdvanceBaseline moves AckedSequence up to FileTimedata.LatestSequence() when no
// pending shed windows remain.
func (m *TimedataReplayManager) AdvanceBaseline() error {
	if m == nil || m.timedata == nil {
		return nil
	}
	latest := m.timedata.LatestSequence()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.state.PendingWindows) > 0 || latest <= m.state.AckedSequence {
		return nil
	}
	m.state.AckedSequence = latest
	for _, p := range telemetryResendOrder {
		if m.state.ByPriority[p] < latest {
			m.state.ByPriority[p] = latest
		}
	}
	m.state.UpdatedAt = m.now().UTC()
	return m.persistCursorLocked()
}

// DrainBatch replays up to maxRecords from pending shed windows in strict priority
// order (VERY_HIGH -> HIGH -> MEDIUM -> LOW), committing durable cursors only after
// publish succeeds. When no shed windows are pending, it advances the baseline cursor.
func (m *TimedataReplayManager) DrainBatch(
	ctx context.Context,
	maxRecords int,
	publish func(context.Context, string, []byte) error,
) (TimedataReplayResult, error) {
	result := TimedataReplayResult{
		ByPriority: make(map[edgecontrol.DataPriority]int, len(telemetryResendOrder)),
	}
	if m == nil || m.timedata == nil {
		return result, nil
	}

	m.drainMu.Lock()
	defer m.drainMu.Unlock()

	if !m.HasPendingReplay() {
		if err := m.AdvanceBaseline(); err != nil {
			return result, err
		}
		return result, nil
	}
	if publish == nil {
		return result, errors.New("timedata replay publish callback is required")
	}

	remaining := maxRecords
	if remaining <= 0 {
		remaining = m.batchSize
	}
	topic := "hvac/v1/" + m.gatewayID + "/up/telemetry"

	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			result.HasMore = m.HasPendingReplay()
			return result, err
		}
		window, ok := m.currentWindow()
		if !ok {
			break
		}

		for _, tier := range telemetryResendOrder {
			for remaining > 0 {
				if err := ctx.Err(); err != nil {
					result.HasMore = m.HasPendingReplay()
					return result, err
				}
				afterSeq := m.tierCursorForWindow(window, tier)
				if afterSeq >= window.ToSeq {
					break
				}

				limit := remaining
				records, err := m.timedata.QueryAfterSequence(afterSeq, limit, func(rec edgecontrol.TimedataRecord) bool {
					if rec.Sequence > window.ToSeq {
						return false
					}
					return m.resolveMeta(rec.Address).Priority == tier
				})
				if err != nil {
					result.HasMore = m.HasPendingReplay()
					return result, err
				}
				if len(records) == 0 {
					if err := m.commitTierCursor(tier, window.ToSeq); err != nil {
						return result, err
					}
					break
				}

				batches := m.buildReplayBatches(records)
				for _, batch := range batches {
					if len(batch.devices) > 0 {
						wire, err := m.buildReplayEnvelope(batch)
						if err != nil {
							return result, err
						}
						if err := publish(ctx, topic, wire); err != nil {
							result.HasMore = m.HasPendingReplay()
							return result, err
						}
						result.BatchesReplayed++
						result.RecordsReplayed += batch.recordCount
						result.ByPriority[tier] += batch.recordCount
					}
					if err := m.commitTierCursor(tier, batch.maxSequence); err != nil {
						return result, err
					}
				}

				remaining -= len(records)
				if len(records) < limit {
					if err := m.commitTierCursor(tier, window.ToSeq); err != nil {
						return result, err
					}
					break
				}
			}
			if remaining <= 0 {
				break
			}
		}

		if err := m.completeWindowIfExhausted(window.ToSeq); err != nil {
			return result, err
		}
		if remaining <= 0 {
			break
		}
	}

	result.HasMore = m.HasPendingReplay()
	return result, nil
}

func (m *TimedataReplayManager) currentWindow() (TimedataShedWindow, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.state.PendingWindows) == 0 {
		return TimedataShedWindow{}, false
	}
	return m.state.PendingWindows[0], true
}

func (m *TimedataReplayManager) tierCursorForWindow(window TimedataShedWindow, tier edgecontrol.DataPriority) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.state.ByPriority[tier]
	if window.FromSeq > 0 && cur < window.FromSeq-1 {
		cur = window.FromSeq - 1
	}
	return cur
}

func (m *TimedataReplayManager) commitTierCursor(tier edgecontrol.DataPriority, seq uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if seq <= m.state.ByPriority[tier] {
		return nil
	}
	m.state.ByPriority[tier] = seq
	m.state.UpdatedAt = m.now().UTC()
	return m.persistCursorLocked()
}

func (m *TimedataReplayManager) completeWindowIfExhausted(toSeq uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.state.PendingWindows) == 0 || m.state.PendingWindows[0].ToSeq != toSeq {
		return nil
	}
	for _, tier := range telemetryResendOrder {
		if m.state.ByPriority[tier] < toSeq {
			return nil
		}
	}
	if toSeq > m.state.AckedSequence {
		m.state.AckedSequence = toSeq
	}
	m.state.PendingWindows = coalesceShedWindows(m.state.PendingWindows[1:], m.state.AckedSequence)
	m.state.UpdatedAt = m.now().UTC()
	return m.persistCursorLocked()
}

type timedataReplayBatch struct {
	observedAt  time.Time
	maxSequence uint64
	recordCount int
	pointCount  int
	devices     map[string]*mqttUplinkDevice
}

func (m *TimedataReplayManager) buildReplayBatches(records []edgecontrol.TimedataRecord) []timedataReplayBatch {
	groups := make([]timedataReplayBatch, 0, len(records))
	var current *timedataReplayBatch
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
			obs = m.now().UTC()
		}
		meta := m.resolveMeta(rec.Address)
		val, encodable := valueToTelemetryJSON(rec.Sample.Value)
		pointKey := meta.DeviceID + "\x00" + meta.PointCode

		sameTimestamp := current != nil && current.observedAt.UnixMilli() == obs.UnixMilli()
		_, duplicatePoint := seenPoints[pointKey]
		tooLarge := current != nil && current.pointCount >= maxReplayPointsPerBatch
		if !sameTimestamp || duplicatePoint || tooLarge {
			flush()
			current = &timedataReplayBatch{
				observedAt: obs,
				devices:    make(map[string]*mqttUplinkDevice),
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
		current.pointCount++

		dev := current.devices[meta.DeviceID]
		if dev == nil {
			dev = &mqttUplinkDevice{
				DeviceID:        meta.DeviceID,
				DeviceTimestamp: max(obs.UnixMilli(), 1),
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
		dev.Points = append(dev.Points, mqttUplinkPoint{
			Code:    meta.PointCode,
			Value:   val,
			Quality: quality,
			Unit:    unitPtr,
		})
	}
	flush()
	return groups
}

func (m *TimedataReplayManager) buildReplayEnvelope(batch timedataReplayBatch) ([]byte, error) {
	deviceIDs := make([]string, 0, len(batch.devices))
	for id := range batch.devices {
		deviceIDs = append(deviceIDs, id)
	}
	sort.Strings(deviceIDs)

	devices := make([]mqttUplinkDevice, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		dev := *batch.devices[id]
		sort.Slice(dev.Points, func(i, j int) bool {
			return dev.Points[i].Code < dev.Points[j].Code
		})
		devices = append(devices, dev)
	}

	ts := batch.observedAt
	if ts.IsZero() || ts.UnixMilli() <= 0 {
		ts = m.now().UTC()
	}
	msgID := edgecontrol.DeterministicReplicationUUIDv7(ts, m.gatewayID, "timedata-replay", strconv.FormatUint(batch.maxSequence, 10))
	seq := batch.maxSequence
	if m.nextSequence != nil {
		seq = m.nextSequence()
	}
	return json.Marshal(mqttUplinkTelemetryEnvelope{
		SchemaVersion: "2.0",
		MessageID:     msgID,
		GatewayID:     m.gatewayID,
		Timestamp:     max(ts.UTC().UnixMilli(), 1),
		Sequence:      seq,
		Replay:        true,
		Payload:       mqttUplinkDevicePayload{Devices: devices},
	})
}

func (m *TimedataReplayManager) resolveMeta(address string) TimedataReplayPointMeta {
	var meta TimedataReplayPointMeta
	if m.pointMetadata != nil {
		meta = m.pointMetadata(address)
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
	switch meta.Priority {
	case edgecontrol.PriorityVeryHigh, edgecontrol.PriorityHigh, edgecontrol.PriorityMedium, edgecontrol.PriorityLow:
	default:
		meta.Priority = edgecontrol.PriorityMedium
	}
	return meta
}

func (m *TimedataReplayManager) persistCursorLocked() error {
	encoded, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode timedata resend cursor: %w", err)
	}
	return writeFileAtomic(m.cursorPath, append(encoded, '\n'), 0o600)
}

func coalesceShedWindows(windows []TimedataShedWindow, acked uint64) []TimedataShedWindow {
	filtered := make([]TimedataShedWindow, 0, len(windows))
	for _, w := range windows {
		fromSeq := max(w.FromSeq, acked+1)
		if fromSeq == 0 {
			fromSeq = 1
		}
		if w.ToSeq < fromSeq {
			continue
		}
		filtered = append(filtered, TimedataShedWindow{
			FromSeq: fromSeq,
			ToSeq:   w.ToSeq,
		})
	}
	if len(filtered) <= 1 {
		return filtered
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].FromSeq == filtered[j].FromSeq {
			return filtered[i].ToSeq < filtered[j].ToSeq
		}
		return filtered[i].FromSeq < filtered[j].FromSeq
	})
	merged := make([]TimedataShedWindow, 0, len(filtered))
	cur := filtered[0]
	for _, next := range filtered[1:] {
		if next.FromSeq <= cur.ToSeq+1 {
			if next.ToSeq > cur.ToSeq {
				cur.ToSeq = next.ToSeq
			}
			continue
		}
		merged = append(merged, cur)
		cur = next
	}
	merged = append(merged, cur)
	return merged
}
