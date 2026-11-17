package packettap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const (
	DefaultMaxPackets         = 100
	DefaultCaptureDuration    = 300 * time.Second
	MinCaptureDurationSeconds = 30
	MaxCaptureDurationSeconds = 900
	MaxActiveCaptureSessions  = 32
	MaxPayloadPreviewBytes    = 4096

	retainedCaptureDuration = 5 * time.Minute
	maxTopicBytes           = 512
	maxTraceIDBytes         = 64

	DirectionUplink   = "UPLINK"
	DirectionDownlink = "DOWNLINK"

	ChannelTelemetry    = "TELEMETRY"
	ChannelEvent        = "EVENT"
	ChannelCommand      = "COMMAND"
	ChannelCommandReply = "COMMAND_REPLY"
	ChannelConcentrator = "CONCENTRATOR"
	ChannelQuarantine   = "QUARANTINE"
)

var (
	ErrCaptureCapacityExceeded = errors.New("packet capture active session limit exceeded")
	ErrInvalidCaptureGateway   = errors.New("invalid gateway id for packet capture")
	ErrInvalidCaptureDuration  = errors.New("packet capture durationSeconds must be between 30 and 900")
)

type CapturedPacket struct {
	Sequence         int64     `json:"sequence"`
	CapturedAt       time.Time `json:"capturedAt"`
	Direction        string    `json:"direction"`
	Channel          string    `json:"channel"`
	Topic            string    `json:"topic"`
	TraceID          string    `json:"traceId,omitempty"`
	PayloadBytes     int       `json:"payloadBytes"`
	PayloadSHA256    string    `json:"payloadSha256"`
	PayloadPreview   string    `json:"payloadPreview"`
	PayloadEncoding  string    `json:"payloadEncoding"`
	PayloadTruncated bool      `json:"payloadTruncated"`
}

type Snapshot struct {
	GatewayID       string           `json:"gatewayId"`
	Active          bool             `json:"active"`
	StartedAt       *time.Time       `json:"startedAt,omitempty"`
	ExpiresAt       *time.Time       `json:"expiresAt,omitempty"`
	DurationSeconds *int             `json:"durationSeconds,omitempty"`
	MaxPackets      int              `json:"maxPackets"`
	DroppedCount    int              `json:"droppedCount"`
	Items           []CapturedPacket `json:"items"`
}

type ManagerOption func(*Manager)

func WithNow(now func() time.Time) ManagerOption {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

type captureSession struct {
	generation      uint64
	gatewayID       string
	active          bool
	startedAt       time.Time
	expiresAt       time.Time
	retainedUntil   time.Time
	durationSeconds int
	maxPackets      int
	droppedCount    int
	nextSeq         int64
	items           []CapturedPacket
}

type Manager struct {
	activeCount    atomic.Int32
	mu             sync.Mutex
	now            func() time.Time
	maxPackets     int
	nextGeneration uint64
	sessions       map[string]*captureSession
}

func NewManager(options ...ManagerOption) *Manager {
	m := &Manager{
		now:        time.Now,
		maxPackets: DefaultMaxPackets,
		sessions:   make(map[string]*captureSession),
	}
	for _, option := range options {
		if option != nil {
			option(m)
		}
	}
	return m
}

func FormatPayloadPreview(raw []byte, maxBytes int) (preview string, encoding string, truncated bool, sha256Hex string) {
	sum := sha256.Sum256(raw)
	sha256Hex = hex.EncodeToString(sum[:])
	if maxBytes <= 0 {
		maxBytes = MaxPayloadPreviewBytes
	}
	if len(raw) == 0 {
		return "", "empty", false, sha256Hex
	}
	if utf8.Valid(raw) && !bytes.ContainsRune(raw, 0) {
		preview = truncateUTF8Bytes(raw, maxBytes)
		truncated = len(raw) > len(preview)
		return preview, "utf8", truncated, sha256Hex
	}
	n := min(len(raw), maxBytes/2)
	if n == 0 && len(raw) > 0 && maxBytes >= 2 {
		n = 1
	}
	hexStr := hex.EncodeToString(raw[:n])
	return hexStr, "hex", len(raw) > n, sha256Hex
}

func truncateUTF8Bytes(raw []byte, maxBytes int) string {
	if maxBytes <= 0 || len(raw) == 0 {
		return ""
	}
	if len(raw) <= maxBytes {
		return string(raw)
	}
	cut := 0
	for cut < len(raw) {
		_, size := utf8.DecodeRune(raw[cut:])
		if cut+size > maxBytes {
			break
		}
		cut += size
	}
	return string(raw[:cut])
}

func (m *Manager) Start(gatewayID string, durationSeconds int) (Snapshot, error) {
	norm := normalizeGatewayID(gatewayID)
	if !commandmodel.IsUUIDv7(norm) {
		return Snapshot{}, ErrInvalidCaptureGateway
	}
	if durationSeconds == 0 {
		durationSeconds = int(DefaultCaptureDuration / time.Second)
	} else if durationSeconds < MinCaptureDurationSeconds || durationSeconds > MaxCaptureDurationSeconds {
		return Snapshot{}, ErrInvalidCaptureDuration
	}
	if m == nil {
		return Snapshot{}, errors.New("packet capture manager is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	now := m.nowTime()
	m.pruneLocked(now)

	existing, exists := m.sessions[norm]
	alreadyActive := exists && existing.active
	if !alreadyActive && m.activeCountLocked() >= MaxActiveCaptureSessions {
		return Snapshot{}, ErrCaptureCapacityExceeded
	}

	m.nextGeneration++
	expiresAt := now.Add(time.Duration(durationSeconds) * time.Second)
	sess := &captureSession{
		generation:      m.nextGeneration,
		gatewayID:       norm,
		active:          true,
		startedAt:       now,
		expiresAt:       expiresAt,
		retainedUntil:   expiresAt.Add(retainedCaptureDuration),
		durationSeconds: durationSeconds,
		maxPackets:      m.maxPackets,
		items:           make([]CapturedPacket, 0, min(m.maxPackets, 16)),
	}
	m.sessions[norm] = sess
	m.recomputeActiveCountLocked()
	return sess.snapshot(), nil
}

func (m *Manager) Stop(gatewayID string) Snapshot {
	norm := normalizeGatewayID(gatewayID)
	if m == nil {
		return emptySnapshot(norm, DefaultMaxPackets)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	now := m.nowTime()
	m.pruneLocked(now)

	sess, exists := m.sessions[norm]
	if !exists {
		return emptySnapshot(norm, m.maxPackets)
	}
	if sess.active {
		sess.active = false
		sess.expiresAt = now
		sess.retainedUntil = now.Add(retainedCaptureDuration)
		m.recomputeActiveCountLocked()
	}
	return sess.snapshot()
}

func (m *Manager) Snapshot(gatewayID string) Snapshot {
	norm := normalizeGatewayID(gatewayID)
	if m == nil {
		return emptySnapshot(norm, DefaultMaxPackets)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	now := m.nowTime()
	m.pruneLocked(now)

	sess, exists := m.sessions[norm]
	if !exists {
		return emptySnapshot(norm, m.maxPackets)
	}
	return sess.snapshot()
}

func (m *Manager) Record(direction, channel, gatewayID, topic, traceID string, payload []byte) {
	if m == nil || m.activeCount.Load() == 0 {
		return
	}
	norm := normalizeGatewayID(gatewayID)
	if norm == "" {
		return
	}
	now := m.nowTime()

	m.mu.Lock()
	if m.sessions == nil {
		m.mu.Unlock()
		return
	}
	sess, exists := m.sessions[norm]
	if !exists || !sess.active {
		m.mu.Unlock()
		return
	}
	if !now.Before(sess.expiresAt) {
		sess.active = false
		sess.retainedUntil = sess.expiresAt.Add(retainedCaptureDuration)
		m.recomputeActiveCountLocked()
		m.mu.Unlock()
		return
	}
	gen := sess.generation
	m.mu.Unlock()

	preview, encoding, truncated, sha256Hex := FormatPayloadPreview(payload, MaxPayloadPreviewBytes)
	dir := sanitizeDirection(direction)
	ch := sanitizeChannel(channel)
	cleanedTopic := sanitizeTopic(topic)
	cleanedTrace := sanitizeTraceID(traceID)

	m.mu.Lock()
	defer m.mu.Unlock()
	sess, exists = m.sessions[norm]
	if !exists || !sess.active || sess.generation != gen {
		return
	}
	now = m.nowTime()
	if !now.Before(sess.expiresAt) {
		sess.active = false
		sess.retainedUntil = sess.expiresAt.Add(retainedCaptureDuration)
		m.recomputeActiveCountLocked()
		return
	}
	sess.nextSeq++
	pkt := CapturedPacket{
		Sequence:         sess.nextSeq,
		CapturedAt:       now,
		Direction:        dir,
		Channel:          ch,
		Topic:            cleanedTopic,
		TraceID:          cleanedTrace,
		PayloadBytes:     len(payload),
		PayloadSHA256:    sha256Hex,
		PayloadPreview:   preview,
		PayloadEncoding:  encoding,
		PayloadTruncated: truncated,
	}
	if len(sess.items) >= sess.maxPackets {
		copy(sess.items, sess.items[1:])
		sess.items[len(sess.items)-1] = pkt
		sess.droppedCount++
		return
	}
	sess.items = append(sess.items, pkt)
}

func (m *Manager) initLocked() {
	if m.sessions == nil {
		m.sessions = make(map[string]*captureSession)
	}
	if m.maxPackets <= 0 {
		m.maxPackets = DefaultMaxPackets
	}
	if m.now == nil {
		m.now = time.Now
	}
}

func (m *Manager) nowTime() time.Time {
	if m != nil && m.now != nil {
		return m.now().UTC()
	}
	return time.Now().UTC()
}

func (m *Manager) pruneLocked(now time.Time) {
	changed := false
	for id, sess := range m.sessions {
		if sess.active && !now.Before(sess.expiresAt) {
			sess.active = false
			sess.retainedUntil = sess.expiresAt.Add(retainedCaptureDuration)
			changed = true
		}
		if !sess.active && !now.Before(sess.retainedUntil) {
			delete(m.sessions, id)
		}
	}
	if changed {
		m.recomputeActiveCountLocked()
	}
}

func (m *Manager) activeCountLocked() int {
	count := 0
	for _, sess := range m.sessions {
		if sess.active {
			count++
		}
	}
	return count
}

func (m *Manager) recomputeActiveCountLocked() {
	m.activeCount.Store(int32(m.activeCountLocked()))
}

func (s *captureSession) snapshot() Snapshot {
	started := s.startedAt.UTC()
	expires := s.expiresAt.UTC()
	dur := s.durationSeconds
	items := slices.Clone(s.items)
	if items == nil {
		items = []CapturedPacket{}
	}
	return Snapshot{
		GatewayID:       s.gatewayID,
		Active:          s.active,
		StartedAt:       &started,
		ExpiresAt:       &expires,
		DurationSeconds: &dur,
		MaxPackets:      s.maxPackets,
		DroppedCount:    s.droppedCount,
		Items:           items,
	}
}

func emptySnapshot(gatewayID string, maxPackets int) Snapshot {
	if maxPackets <= 0 {
		maxPackets = DefaultMaxPackets
	}
	return Snapshot{
		GatewayID:  gatewayID,
		Active:     false,
		MaxPackets: maxPackets,
		Items:      []CapturedPacket{},
	}
}

func normalizeGatewayID(gatewayID string) string {
	return strings.ToLower(strings.TrimSpace(gatewayID))
}

func sanitizeDirection(direction string) string {
	switch strings.ToUpper(strings.TrimSpace(direction)) {
	case DirectionUplink:
		return DirectionUplink
	case DirectionDownlink:
		return DirectionDownlink
	default:
		return DirectionUplink
	}
}

func sanitizeChannel(channel string) string {
	switch strings.ToUpper(strings.TrimSpace(channel)) {
	case ChannelTelemetry:
		return ChannelTelemetry
	case ChannelEvent:
		return ChannelEvent
	case ChannelCommand:
		return ChannelCommand
	case ChannelCommandReply:
		return ChannelCommandReply
	case ChannelConcentrator:
		return ChannelConcentrator
	case ChannelQuarantine:
		return ChannelQuarantine
	default:
		return ChannelTelemetry
	}
}

func sanitizeTopic(topic string) string {
	trimmed := strings.TrimSpace(topic)
	if trimmed == "" {
		return "(unknown)"
	}
	return truncateUTF8Bytes([]byte(trimmed), maxTopicBytes)
}

func sanitizeTraceID(traceID string) string {
	cleaned := strings.NewReplacer("\r", "", "\n", "").Replace(strings.TrimSpace(traceID))
	if cleaned == "" {
		return ""
	}
	return truncateUTF8Bytes([]byte(cleaned), maxTraceIDBytes)
}
