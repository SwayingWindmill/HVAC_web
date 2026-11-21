package adapter

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
)

const (
	defaultMaxTrackedSessions     = 65536
	maxSupersededSessionsPerEntry = 16
)

// SessionTransition describes a gateway online/offline lifecycle event or status update.
type SessionTransition struct {
	GatewayID      string
	SessionID      string
	Sequence       int64
	HasSequence    bool
	EventTime      int64
	HasEventTime   bool
	ServerTime     int64
	HasServerTime  bool
	ServerObserved bool
	Online         bool
}

// GatewaySessionSnapshot exposes the current fenced session state for a gateway.
type GatewaySessionSnapshot struct {
	GatewayID         string
	SessionID         string
	LastSequence      int64
	HasSequence       bool
	LastEventTime     int64
	LastTelemetryTime int64
	Online            bool
	HasTransition     bool
}

type gatewaySessionState struct {
	gatewayID            string
	sessionID            string
	lastSequence         int64
	hasSequence          bool
	lastEventTime        int64
	lastTelemetryTime    int64
	lastServerTime       int64
	lastTelemetrySession string
	online               bool
	hasTransition        bool
	superseded           []string
}

func (s *gatewaySessionState) markSuperseded(sessionID string) {
	if sessionID == "" || slices.Contains(s.superseded, sessionID) {
		return
	}
	if len(s.superseded) >= maxSupersededSessionsPerEntry {
		copy(s.superseded, s.superseded[1:])
		s.superseded[len(s.superseded)-1] = sessionID
		return
	}
	s.superseded = append(s.superseded, sessionID)
}

func (s *gatewaySessionState) isSuperseded(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	return slices.Contains(s.superseded, sessionID)
}

// GatewaySessionTracker fences per-gateway online/offline transitions using
// (sessionId, sequence, eventTime) so delayed LWT messages from dead TCP sockets or
// replayed lifecycle events never corrupt newer active gateway sessions.
type GatewaySessionTracker struct {
	mu       sync.Mutex
	gateways boundedTouchMap[*gatewaySessionState]
}

// SessionTrackerOption configures a GatewaySessionTracker.
type SessionTrackerOption func(*GatewaySessionTracker)

// WithSessionTrackerCapacity caps the number of gateways retained in memory.
func WithSessionTrackerCapacity(maxGateways int) SessionTrackerOption {
	return func(t *GatewaySessionTracker) {
		if maxGateways > 0 {
			t.gateways.maxEntries = maxGateways
		}
	}
}

// NewGatewaySessionTracker initializes a thread-safe GatewaySessionTracker.
func NewGatewaySessionTracker(opts ...SessionTrackerOption) *GatewaySessionTracker {
	tracker := &GatewaySessionTracker{
		gateways: newBoundedTouchMap[*gatewaySessionState](defaultMaxTrackedSessions),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(tracker)
		}
	}
	return tracker
}

// ApplyStatus applies a decoded StatusEnvelope transition.
func (t *GatewaySessionTracker) ApplyStatus(status StatusEnvelope) bool {
	return t.ApplyStatusAt(status, 0, false)
}

// ApplyStatusAt applies a decoded StatusEnvelope transition with an optional server clock observation.
func (t *GatewaySessionTracker) ApplyStatusAt(status StatusEnvelope, serverTime int64, hasServerTime bool) bool {
	return t.ApplyTransition(SessionTransition{
		GatewayID:     status.GatewayID,
		SessionID:     status.SessionID,
		Sequence:      status.Sequence,
		HasSequence:   status.HasSequence,
		EventTime:     status.EventTime,
		HasEventTime:  status.HasEventTime,
		ServerTime:    serverTime,
		HasServerTime: hasServerTime,
		Online:        status.Online,
	})
}

// ApplyTransition evaluates and applies tx against the gateway's active session state.
// It returns true when the transition advances or matches valid session ordering, or false
// when the event is stale, duplicated/out-of-order, or belongs to a superseded session.
func (t *GatewaySessionTracker) ApplyTransition(tx SessionTransition) bool {
	if t == nil {
		return true
	}
	gw := strings.TrimSpace(tx.GatewayID)
	if gw == "" {
		return false
	}
	sid := strings.TrimSpace(tx.SessionID)

	t.mu.Lock()
	defer t.mu.Unlock()

	cur := t.getOrCreateLocked(gw)

	if !cur.hasTransition {
		if !tx.Online {
			if sid != "" && cur.lastTelemetrySession != "" && sid != cur.lastTelemetrySession {
				return false
			}
			if tx.ServerObserved {
				if tx.HasEventTime && cur.lastServerTime > 0 && tx.EventTime < cur.lastServerTime {
					return false
				}
				if cur.lastTelemetrySession != "" && sid == "" && tx.HasEventTime && cur.lastServerTime > 0 && tx.EventTime <= cur.lastServerTime {
					return false
				}
			} else {
				if tx.HasEventTime && cur.lastTelemetryTime > 0 && tx.EventTime < cur.lastTelemetryTime {
					return false
				}
				if cur.lastTelemetrySession != "" && sid == "" && tx.HasEventTime && tx.EventTime <= cur.lastTelemetryTime {
					return false
				}
			}
		} else {
			if sid != "" && cur.lastTelemetrySession != "" && sid != cur.lastTelemetrySession &&
				tx.HasEventTime && cur.lastTelemetryTime > 0 && tx.EventTime < cur.lastTelemetryTime {
				return false
			}
		}
		targetSID := sid
		if targetSID == "" && tx.Online {
			targetSID = cur.lastTelemetrySession
		}
		return applyAcceptedTransitionLocked(cur, tx, targetSID, true)
	}

	switch {
	case sid != "" && cur.sessionID != "":
		if sid == cur.sessionID {
			if cur.hasSequence && tx.HasSequence && tx.Sequence <= cur.lastSequence {
				return false
			}
			if !tx.HasSequence && tx.HasEventTime && cur.lastEventTime > 0 && tx.EventTime < cur.lastEventTime {
				return false
			}
			return applyAcceptedTransitionLocked(cur, tx, sid, false)
		}

		if cur.isSuperseded(sid) || !tx.Online {
			return false
		}
		if tx.HasEventTime && cur.lastEventTime > 0 && tx.EventTime < cur.lastEventTime {
			return false
		}
		if cur.lastTelemetrySession != "" && cur.lastTelemetrySession != sid &&
			tx.HasEventTime && cur.lastTelemetryTime > 0 && tx.EventTime < cur.lastTelemetryTime {
			return false
		}
		return applyAcceptedTransitionLocked(cur, tx, sid, true)

	case sid != "" && cur.sessionID == "":
		if cur.isSuperseded(sid) {
			return false
		}
		if tx.HasEventTime {
			if cur.lastEventTime > 0 && tx.EventTime < cur.lastEventTime {
				return false
			}
			if !tx.Online && cur.lastTelemetryTime > 0 && tx.EventTime < cur.lastTelemetryTime {
				return false
			}
		}
		return applyAcceptedTransitionLocked(cur, tx, sid, true)

	case sid == "" && cur.sessionID != "":
		if tx.ServerObserved {
			if !tx.Online && cur.online {
				if !tx.HasEventTime || (cur.lastServerTime > 0 && tx.EventTime <= cur.lastServerTime) {
					return false
				}
			}
			if tx.HasEventTime && cur.lastServerTime > 0 && tx.EventTime < cur.lastServerTime {
				return false
			}
			return applyAcceptedTransitionLocked(cur, tx, "", false)
		}
		if !tx.Online && cur.online {
			if !tx.HasEventTime || (cur.lastEventTime > 0 && tx.EventTime <= cur.lastEventTime) {
				return false
			}
			if cur.lastTelemetryTime > 0 && tx.EventTime < cur.lastTelemetryTime {
				return false
			}
		}
		if tx.HasEventTime && cur.lastEventTime > 0 && tx.EventTime < cur.lastEventTime {
			return false
		}
		return applyAcceptedTransitionLocked(cur, tx, "", false)

	default:
		// Legacy path: both incoming and active session omit sessionId.
		if tx.ServerObserved {
			if tx.HasEventTime && cur.lastServerTime > 0 && tx.EventTime < cur.lastServerTime {
				return false
			}
			return applyAcceptedTransitionLocked(cur, tx, "", false)
		}
		if cur.hasSequence && tx.HasSequence && tx.Sequence <= cur.lastSequence {
			return false
		}
		if tx.HasEventTime && cur.lastEventTime > 0 && tx.EventTime < cur.lastEventTime {
			return false
		}
		if !tx.Online && tx.HasEventTime && cur.lastTelemetryTime > 0 && tx.EventTime < cur.lastTelemetryTime {
			return false
		}
		return applyAcceptedTransitionLocked(cur, tx, "", false)
	}
}

func applyAcceptedTransitionLocked(cur *gatewaySessionState, tx SessionTransition, newSessionID string, resetSeq bool) bool {
	cur.hasTransition = true
	if newSessionID != "" {
		if cur.sessionID != "" && cur.sessionID != newSessionID {
			cur.markSuperseded(cur.sessionID)
		}
		cur.sessionID = newSessionID
	} else if !tx.Online && cur.sessionID != "" {
		cur.markSuperseded(cur.sessionID)
		cur.sessionID = ""
	}
	cur.online = tx.Online
	if resetSeq {
		cur.hasSequence = tx.HasSequence
		if tx.HasSequence {
			cur.lastSequence = tx.Sequence
		} else {
			cur.lastSequence = 0
		}
	} else if tx.HasSequence {
		cur.hasSequence = true
		cur.lastSequence = tx.Sequence
	}
	if tx.HasEventTime && !tx.ServerObserved && tx.EventTime > cur.lastEventTime {
		cur.lastEventTime = tx.EventTime
	}
	serverTime := tx.ServerTime
	hasServerTime := tx.HasServerTime
	if !hasServerTime && tx.HasEventTime {
		serverTime = tx.EventTime
		hasServerTime = true
	}
	if hasServerTime && serverTime > cur.lastServerTime {
		cur.lastServerTime = serverTime
	}
	return true
}

// ObserveTelemetry advances gateway telemetry recency and optional sessionId awareness
// without modifying event/status sequence numbering.
func (t *GatewaySessionTracker) ObserveTelemetry(gatewayID, sessionID string, eventTime int64) {
	t.ObserveTelemetryAt(gatewayID, sessionID, eventTime, 0, false)
}

// ObserveTelemetryAt advances gateway telemetry recency plus server receive time so
// server-observed disconnect notices never race against gateway RTC clock skew.
func (t *GatewaySessionTracker) ObserveTelemetryAt(gatewayID, sessionID string, eventTime, serverTime int64, hasServerTime bool) {
	if t == nil {
		return
	}
	gw := strings.TrimSpace(gatewayID)
	if gw == "" {
		return
	}
	sid := strings.TrimSpace(sessionID)

	t.mu.Lock()
	defer t.mu.Unlock()

	cur := t.getOrCreateLocked(gw)
	if eventTime > cur.lastTelemetryTime {
		cur.lastTelemetryTime = eventTime
	}
	if hasServerTime && serverTime > cur.lastServerTime {
		cur.lastServerTime = serverTime
	}
	if sid == "" || cur.isSuperseded(sid) {
		return
	}
	if cur.sessionID == "" {
		if !cur.hasTransition || eventTime >= cur.lastEventTime {
			cur.sessionID = sid
			cur.lastTelemetrySession = sid
			if eventTime >= cur.lastEventTime {
				cur.online = true
			}
		}
		return
	}
	if sid != cur.sessionID && eventTime >= cur.lastEventTime && eventTime >= cur.lastTelemetryTime {
		cur.markSuperseded(cur.sessionID)
		cur.sessionID = sid
		cur.hasSequence = false
		cur.lastSequence = 0
		cur.online = true
	}
	cur.lastTelemetrySession = sid
}

// Snapshot returns the current tracked state for gatewayID, if any.
func (t *GatewaySessionTracker) Snapshot(gatewayID string) (GatewaySessionSnapshot, bool) {
	if t == nil {
		return GatewaySessionSnapshot{}, false
	}
	gw := strings.TrimSpace(gatewayID)
	t.mu.Lock()
	defer t.mu.Unlock()

	cur, ok := t.gateways.get(gw)
	if !ok {
		return GatewaySessionSnapshot{}, false
	}
	return GatewaySessionSnapshot{
		GatewayID:         cur.gatewayID,
		SessionID:         cur.sessionID,
		LastSequence:      cur.lastSequence,
		HasSequence:       cur.hasSequence,
		LastEventTime:     cur.lastEventTime,
		LastTelemetryTime: cur.lastTelemetryTime,
		Online:            cur.online,
		HasTransition:     cur.hasTransition,
	}, true
}

func (t *GatewaySessionTracker) getOrCreateLocked(gatewayID string) *gatewaySessionState {
	return t.gateways.getOrCreate(gatewayID, func() *gatewaySessionState {
		return &gatewaySessionState{gatewayID: gatewayID}
	})
}

func extractEventSessionID(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var payload struct {
		SessionID string `json:"sessionId"`
		SID       string `json:"sid"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	if sid := strings.TrimSpace(payload.SessionID); sid != "" {
		return sid
	}
	return strings.TrimSpace(payload.SID)
}
