package energy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	RebuildScopeTransitionCorrections = "TRANSITION_CORRECTIONS"
	RebuildReasonLateArrival          = "LATE_ARRIVAL"
	RebuildEventRunStarted            = "RUN_STARTED"
	RebuildEventRunPersistedChunk     = "RUN_PERSISTED_CHUNK"
	RebuildEventRunCompleted          = "RUN_COMPLETED"
	RebuildEventRunFailed             = "RUN_FAILED"
)

type RebuildEvent struct {
	EventID        string     `json:"event_id"`
	RunID          string     `json:"run_id"`
	TenantID       string     `json:"tenant_id"`
	SiteID         string     `json:"site_id"`
	ScopeType      string     `json:"scope_type"`
	MeterBindingID *string    `json:"meter_binding_id,omitempty"`
	WindowStart    *time.Time `json:"window_start,omitempty"`
	WindowEnd      *time.Time `json:"window_end,omitempty"`
	ReasonCode     string     `json:"reason_code"`
	TriggerRef     string     `json:"trigger_ref"`
	EventType      string     `json:"event_type"`
	ChunkCursor    string     `json:"chunk_cursor"`
	Detail         string     `json:"detail"`
	RecordedAt     time.Time  `json:"recorded_at"`
}

func (event RebuildEvent) validate() error {
	for name, value := range map[string]string{
		"event_id": event.EventID, "run_id": event.RunID, "tenant_id": event.TenantID,
		"site_id": event.SiteID, "scope_type": event.ScopeType, "reason_code": event.ReasonCode,
		"trigger_ref": event.TriggerRef, "event_type": event.EventType, "detail": event.Detail,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("rebuild event %s is required", name)
		}
	}
	if event.ScopeType != RebuildScopeTransitionCorrections || event.ReasonCode != RebuildReasonLateArrival {
		return errors.New("rebuild event scope or reason is invalid")
	}
	switch event.EventType {
	case RebuildEventRunStarted, RebuildEventRunPersistedChunk, RebuildEventRunCompleted, RebuildEventRunFailed:
	default:
		return fmt.Errorf("rebuild event type %q is invalid", event.EventType)
	}
	if event.RecordedAt.IsZero() {
		return errors.New("rebuild event recorded_at is required")
	}
	if event.WindowStart != nil && event.WindowEnd != nil && !event.WindowStart.Before(*event.WindowEnd) {
		return errors.New("rebuild event window must be ordered")
	}
	return nil
}

func (event RebuildEvent) ValidateForPersistence() error {
	return event.validate()
}

func newUUIDv7(now time.Time) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate rebuild UUID: %w", err)
	}
	millis := uint64(now.UTC().UnixMilli())
	raw[0] = byte(millis >> 40)
	raw[1] = byte(millis >> 32)
	raw[2] = byte(millis >> 24)
	raw[3] = byte(millis >> 16)
	raw[4] = byte(millis >> 8)
	raw[5] = byte(millis)
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func correctionRevision(delta CounterDelta, binding BindingResolution) (uint64, bool, error) {
	if !delta.ExistingFactFound || delta.ExistingFactMeterBindingID != binding.MeterBindingID {
		return 0, false, nil
	}
	if delta.ExistingFactPreviousObservationID == delta.PreviousObservationID {
		return 0, false, errors.New("counter candidate already matches latest fact predecessor")
	}
	if delta.ExistingFactRevision == ^uint64(0) {
		return 0, false, errors.New("fact revision overflow")
	}
	return delta.ExistingFactRevision + 1, true, nil
}

func rebuildDetail(facts []EnergyIntervalFact, failure error) string {
	logicalKeys := make([]string, 0, len(facts))
	for _, fact := range facts {
		logicalKeys = append(logicalKeys, fact.LogicalKey())
	}
	sort.Strings(logicalKeys)
	payload := map[string]any{"factCount": len(facts), "logicalKeys": logicalKeys}
	if failure != nil {
		payload["failure"] = failure.Error()
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return `{"factCount":0,"detailEncoding":"failed"}`
	}
	return string(encoded)
}

func (p *Projector) persistFacts(ctx context.Context, facts []EnergyIntervalFact) (int, error) {
	corrections := make(map[string][]EnergyIntervalFact)
	normal := make([]EnergyIntervalFact, 0, len(facts))
	for _, fact := range facts {
		if fact.FactRevision == 0 {
			normal = append(normal, fact)
			continue
		}
		corrections[fact.TenantID+"|"+fact.SiteID] = append(corrections[fact.TenantID+"|"+fact.SiteID], fact)
	}
	if len(corrections) > 0 && p.rebuildEvents == nil {
		return 0, errors.New("energy rebuild event sink is required for fact corrections")
	}
	projected := 0
	if len(normal) > 0 {
		if err := p.sink.InsertFacts(ctx, normal); err != nil {
			return 0, fmt.Errorf("insert energy facts: %w", err)
		}
		projected += len(normal)
	}

	scopes := make([]string, 0, len(corrections))
	for scope := range corrections {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	for _, scope := range scopes {
		group := corrections[scope]
		runID, err := newUUIDv7(p.now())
		if err != nil {
			return 0, err
		}
		for index := range group {
			group[index].RebuildRunID = &runID
		}
		tenantID, siteID, _ := strings.Cut(scope, "|")
		windowStart, windowEnd := correctionWindow(group)
		triggerRef := correctionTriggerRef(group)
		base := RebuildEvent{
			RunID: runID, TenantID: tenantID, SiteID: siteID,
			ScopeType: RebuildScopeTransitionCorrections, WindowStart: windowStart, WindowEnd: windowEnd,
			ReasonCode: RebuildReasonLateArrival, TriggerRef: triggerRef,
		}
		if err := p.appendRebuildEvent(ctx, base, RebuildEventRunStarted, "", group, nil); err != nil {
			return 0, err
		}

		chunks := make(map[string][]EnergyIntervalFact)
		for _, fact := range group {
			cursor := fact.PeriodEnd.UTC().Format("2006-01")
			chunks[cursor] = append(chunks[cursor], fact)
		}
		cursors := make([]string, 0, len(chunks))
		for cursor := range chunks {
			cursors = append(cursors, cursor)
		}
		sort.Strings(cursors)
		for _, cursor := range cursors {
			chunk := chunks[cursor]
			if err := p.sink.InsertFacts(ctx, chunk); err != nil {
				return projected, p.failRebuild(ctx, base, group, err)
			}
			projected += len(chunk)
			if err := p.appendRebuildEvent(ctx, base, RebuildEventRunPersistedChunk, cursor, chunk, nil); err != nil {
				return projected, err
			}
		}
		if err := p.appendRebuildEvent(ctx, base, RebuildEventRunCompleted, "", group, nil); err != nil {
			return projected, err
		}
	}
	return projected, nil
}

func (p *Projector) appendRebuildEvent(ctx context.Context, base RebuildEvent, eventType, cursor string, facts []EnergyIntervalFact, failure error) error {
	eventID, err := newUUIDv7(p.now())
	if err != nil {
		return err
	}
	base.EventID = eventID
	base.EventType = eventType
	base.ChunkCursor = cursor
	base.Detail = rebuildDetail(facts, failure)
	base.RecordedAt = p.now().UTC()
	if err := base.validate(); err != nil {
		return err
	}
	if err := p.rebuildEvents.AppendRebuildEvent(ctx, base); err != nil {
		return fmt.Errorf("append energy rebuild event %s: %w", eventType, err)
	}
	return nil
}

func (p *Projector) failRebuild(ctx context.Context, base RebuildEvent, facts []EnergyIntervalFact, cause error) error {
	if err := p.appendRebuildEvent(ctx, base, RebuildEventRunFailed, "", facts, cause); err != nil {
		return fmt.Errorf("%w; append energy rebuild failure event: %v", cause, err)
	}
	return fmt.Errorf("energy rebuild failed: %w", cause)
}

func correctionWindow(facts []EnergyIntervalFact) (*time.Time, *time.Time) {
	if len(facts) == 0 {
		return nil, nil
	}
	start, end := facts[0].PeriodStart.UTC(), facts[0].PeriodEnd.UTC()
	for _, fact := range facts[1:] {
		if fact.PeriodStart.Before(start) {
			start = fact.PeriodStart.UTC()
		}
		if fact.PeriodEnd.After(end) {
			end = fact.PeriodEnd.UTC()
		}
	}
	return &start, &end
}

func correctionTriggerRef(facts []EnergyIntervalFact) string {
	ids := make([]string, 0, len(facts))
	for _, fact := range facts {
		ids = append(ids, fact.CurrentObservationID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}
