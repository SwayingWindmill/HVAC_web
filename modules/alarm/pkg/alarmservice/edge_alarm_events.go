package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	InternalEdgeAlarmEventsPath      = "/internal/v1/alarms/edge-events"
	maxEdgeAlarmEventRequestBodySize = int64(256 << 10)
)

const (
	EdgeAlarmOutcomeRaised       = "RAISED"
	EdgeAlarmOutcomeCleared      = "CLEARED"
	EdgeAlarmOutcomeUpdated      = "UPDATED"
	EdgeAlarmOutcomeDuplicate    = "DUPLICATE"
	EdgeAlarmOutcomeOutOfOrder   = "IGNORED_OUT_OF_ORDER"
	EdgeAlarmOutcomeUnmappedRule = "IGNORED_UNMAPPED_RULE"
)

var (
	ErrEdgeAlarmEventInvalid = errors.New("edge alarm event payload is invalid")
	digest64HexPattern       = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type EdgeAlarmEventRequest struct {
	SchemaVersion  int             `json:"schemaVersion"`
	MessageID      string          `json:"messageId"`
	TenantID       string          `json:"tenantId"`
	SiteID         string          `json:"siteId"`
	GatewayID      string          `json:"gatewayId"`
	EventType      string          `json:"eventType"`
	SourceType     string          `json:"sourceType"`
	SourceID       string          `json:"sourceId"`
	EventTime      string          `json:"eventTime"`
	Severity       string          `json:"severity"`
	SourceSequence int64           `json:"sourceSequence"`
	ReceivedAt     string          `json:"receivedAt"`
	Data           json.RawMessage `json:"data"`
}

type EdgeAlarmEventResult struct {
	Outcome     string `json:"outcome"`
	RuleID      string `json:"ruleId"`
	EdgeEventID string `json:"edgeEventId"`
	AlarmID     string `json:"alarmId,omitempty"`
	EventID     string `json:"eventId,omitempty"`
}

type EdgeAlarmEventIngestor interface {
	IngestEdgeAlarmEvent(ctx context.Context, req EdgeAlarmEventRequest, now time.Time) (EdgeAlarmEventResult, error)
}

type parsedEdgeAlarmEvent struct {
	TenantID         string
	SiteID           string
	GatewayID        string
	MQTTMessageID    string
	RuleID           string
	EdgeEventID      string
	EventType        string
	OccurredAt       time.Time
	ReceivedAt       time.Time
	Severity         string
	Title            string
	Reason           string
	AlarmType        string
	Offline          bool
	BundleRevision   int64
	EdgeBundleDigest string
	TransitionSeq    *int64
	TriggeredActions []string
}

func normalizeEdgeRuleEventType(raw string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case edgecontrol.EdgeRuleEventRaised, "EDGE_RULE_EDGE_RULE_ALARM_RAISED":
		return edgecontrol.EdgeRuleEventRaised, true
	case edgecontrol.EdgeRuleEventCleared, "EDGE_RULE_EDGE_RULE_ALARM_CLEARED":
		return edgecontrol.EdgeRuleEventCleared, true
	default:
		return "", false
	}
}

func parseEdgeTransitionSeq(edgeEventID, ruleID string) *int64 {
	prefix := "edge-rule:" + ruleID + ":"
	lower := strings.ToLower(strings.TrimSpace(edgeEventID))
	if !strings.HasPrefix(lower, prefix) {
		return nil
	}
	seqText := strings.TrimPrefix(lower, prefix)
	seq, err := strconv.ParseInt(seqText, 10, 64)
	if err != nil || seq < 0 {
		return nil
	}
	return &seq
}

// ParseEdgeAlarmEventRequest validates and normalizes a relayed MQTT edge rule event,
// accepting both canonical and legacy double-prefixed edge event envelopes safely.
func ParseEdgeAlarmEventRequest(req EdgeAlarmEventRequest, now time.Time) (parsedEdgeAlarmEvent, error) {
	if req.SchemaVersion != 1 {
		return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
	}
	tenantID := strings.ToLower(strings.TrimSpace(req.TenantID))
	siteID := strings.ToLower(strings.TrimSpace(req.SiteID))
	gatewayID := strings.TrimSpace(req.GatewayID)
	mqttMessageID := strings.ToLower(strings.TrimSpace(req.MessageID))
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) || !boundedText(gatewayID, 128) || req.SourceSequence < 0 {
		return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
	}
	if mqttMessageID != "" && !alarmmodel.IsUUIDv7(mqttMessageID) {
		return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
	}
	envelopeEventTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.EventTime))
	if err != nil {
		return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
	}
	receivedAt := now.UTC()
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	if rawReceived := strings.TrimSpace(req.ReceivedAt); rawReceived != "" {
		parsedReceived, err := time.Parse(time.RFC3339Nano, rawReceived)
		if err != nil {
			return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
		}
		receivedAt = parsedReceived.UTC()
	}
	if len(req.Data) == 0 || !json.Valid(req.Data) {
		return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
	}

	var data struct {
		EventID          string   `json:"eventId"`
		EventType        string   `json:"eventType"`
		RuleID           string   `json:"ruleId"`
		AlarmType        string   `json:"alarmType"`
		Title            string   `json:"title"`
		Severity         string   `json:"severity"`
		OccurredAt       string   `json:"occurredAt"`
		BundleRevision   int64    `json:"bundleRevision"`
		EdgeBundleDigest string   `json:"edgeBundleDigest"`
		Offline          bool     `json:"offline"`
		Reason           string   `json:"reason"`
		TriggeredActions []string `json:"triggeredActions"`
	}
	if err := json.Unmarshal(req.Data, &data); err != nil {
		return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
	}

	eventType, ok := normalizeEdgeRuleEventType(req.EventType)
	if !ok {
		eventType, ok = normalizeEdgeRuleEventType(data.EventType)
		if !ok {
			return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
		}
	}

	ruleID := strings.ToLower(strings.TrimSpace(data.RuleID))
	if ruleID == "" {
		ruleID = strings.ToLower(strings.TrimSpace(req.SourceID))
	}
	if !alarmmodel.IsUUIDv7(ruleID) {
		return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
	}

	edgeEventID := strings.TrimSpace(data.EventID)
	if edgeEventID == "" {
		if mqttMessageID == "" {
			return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
		}
		edgeEventID = "mqtt:" + mqttMessageID
	}
	if !boundedText(edgeEventID, 256) {
		return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
	}

	edgeOccurredAt := envelopeEventTime.UTC()
	if rawOccurred := strings.TrimSpace(data.OccurredAt); rawOccurred != "" {
		parsedOccurred, err := time.Parse(time.RFC3339Nano, rawOccurred)
		if err != nil {
			return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
		}
		edgeOccurredAt = parsedOccurred.UTC()
	}

	if data.BundleRevision < 0 {
		return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
	}
	edgeDigest := strings.ToLower(strings.TrimSpace(data.EdgeBundleDigest))
	if edgeDigest != "" && !digest64HexPattern.MatchString(edgeDigest) {
		return parsedEdgeAlarmEvent{}, ErrEdgeAlarmEventInvalid
	}

	severity := strings.TrimSpace(data.Severity)
	if severity == "" {
		severity = strings.TrimSpace(req.Severity)
	}

	return parsedEdgeAlarmEvent{
		TenantID:         tenantID,
		SiteID:           siteID,
		GatewayID:        gatewayID,
		MQTTMessageID:    mqttMessageID,
		RuleID:           ruleID,
		EdgeEventID:      edgeEventID,
		EventType:        eventType,
		OccurredAt:       edgeOccurredAt,
		ReceivedAt:       receivedAt,
		Severity:         severity,
		Title:            strings.TrimSpace(data.Title),
		Reason:           strings.TrimSpace(data.Reason),
		AlarmType:        strings.TrimSpace(data.AlarmType),
		Offline:          data.Offline,
		BundleRevision:   data.BundleRevision,
		EdgeBundleDigest: edgeDigest,
		TransitionSeq:    parseEdgeTransitionSeq(edgeEventID, ruleID),
		TriggeredActions: data.TriggeredActions,
	}, nil
}

func NewEdgeAlarmEventHandler(ingestor EdgeAlarmEventIngestor) (http.Handler, error) {
	if ingestor == nil {
		return nil, errors.New("edge alarm event handler requires an ingestor")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != InternalEdgeAlarmEventsPath {
			writeControlEventProblem(writer, http.StatusNotFound, "ALARM_EDGE_EVENT_ROUTE_NOT_FOUND", false)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxEdgeAlarmEventRequestBodySize)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var req EdgeAlarmEventRequest
		if err := decoder.Decode(&req); err != nil {
			writeControlEventProblem(writer, http.StatusBadRequest, "ALARM_EDGE_EVENT_PAYLOAD_INVALID", false)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			writeControlEventProblem(writer, http.StatusBadRequest, "ALARM_EDGE_EVENT_PAYLOAD_INVALID", false)
			return
		}
		if req.SchemaVersion != 1 {
			writeControlEventProblem(writer, http.StatusBadRequest, "ALARM_EDGE_EVENT_SCHEMA_UNSUPPORTED", false)
			return
		}
		ctx := identitycontext.WithTenantID(request.Context(), req.TenantID)
		result, err := ingestor.IngestEdgeAlarmEvent(ctx, req, time.Now().UTC())
		if err != nil {
			if errors.Is(err, ErrEdgeAlarmEventInvalid) || errors.Is(err, alarmmodel.ErrInvalidOperation) {
				writeControlEventProblem(writer, http.StatusBadRequest, "ALARM_EDGE_EVENT_PAYLOAD_INVALID", false)
				return
			}
			writeControlEventProblem(writer, http.StatusServiceUnavailable, "ALARM_EDGE_EVENT_UNAVAILABLE", true)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(writer).Encode(result)
	}), nil
}

func (store *PostgresStore) IngestEdgeAlarmEvent(ctx context.Context, req EdgeAlarmEventRequest, now time.Time) (EdgeAlarmEventResult, error) {
	if store == nil || store.pool == nil {
		return EdgeAlarmEventResult{}, ErrUnavailable
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	parsed, err := ParseEdgeAlarmEventRequest(req, now)
	if err != nil {
		return EdgeAlarmEventResult{}, err
	}

	tenantCtx := identitycontext.WithTenantID(ctx, parsed.TenantID)
	tx, err := store.beginTenantTransaction(tenantCtx, parsed.TenantID, false)
	if err != nil {
		return EdgeAlarmEventResult{}, err
	}
	defer func() { _ = tx.Rollback(tenantCtx) }()

	if err := lockEvaluationAssignment(tenantCtx, tx, parsed.TenantID, parsed.RuleID); err != nil {
		return EdgeAlarmEventResult{}, err
	}

	// 1. Idempotent dedup check against receipt ledger.
	if existing, found, err := findExistingEdgeEventReceiptInTx(tenantCtx, tx, parsed); err != nil {
		return EdgeAlarmEventResult{}, err
	} else if found {
		return EdgeAlarmEventResult{
			Outcome:     EdgeAlarmOutcomeDuplicate,
			RuleID:      parsed.RuleID,
			EdgeEventID: parsed.EdgeEventID,
			AlarmID:     existing.AlarmID,
			EventID:     existing.EventID,
		}, nil
	}

	baseMetadata := buildEdgeReceiptMetadata(parsed)
	metadataJSON, err := json.Marshal(baseMetadata)
	if err != nil {
		return EdgeAlarmEventResult{}, fmt.Errorf("encode edge alarm receipt metadata: %w", err)
	}

	// 2. Load assigned policy; gracefully audit unmapped or retired rules.
	assigned, err := loadAssignedAlarmPolicy(tenantCtx, tx, parsed.TenantID, parsed.SiteID, parsed.RuleID)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrRuleRetired) {
		if recErr := store.insertEdgeReceiptInTx(tenantCtx, tx, parsed, EdgeAlarmOutcomeUnmappedRule, "", "", metadataJSON, now); recErr != nil {
			return EdgeAlarmEventResult{}, recErr
		}
		if commitErr := tx.Commit(tenantCtx); commitErr != nil {
			return EdgeAlarmEventResult{}, fmt.Errorf("commit unmapped edge event receipt: %w", commitErr)
		}
		return EdgeAlarmEventResult{
			Outcome:     EdgeAlarmOutcomeUnmappedRule,
			RuleID:      parsed.RuleID,
			EdgeEventID: parsed.EdgeEventID,
		}, nil
	}
	if err != nil {
		return EdgeAlarmEventResult{}, err
	}

	// 3. Monotonic cursor ordering guard against out-of-order offline burst replay.
	outOfOrder, err := isEdgeEventOutOfOrderInTx(tenantCtx, tx, parsed)
	if err != nil {
		return EdgeAlarmEventResult{}, err
	}
	if outOfOrder {
		if recErr := store.insertEdgeReceiptInTx(tenantCtx, tx, parsed, EdgeAlarmOutcomeOutOfOrder, "", "", metadataJSON, now); recErr != nil {
			return EdgeAlarmEventResult{}, recErr
		}
		if commitErr := tx.Commit(tenantCtx); commitErr != nil {
			return EdgeAlarmEventResult{}, fmt.Errorf("commit out-of-order edge event receipt: %w", commitErr)
		}
		return EdgeAlarmEventResult{
			Outcome:     EdgeAlarmOutcomeOutOfOrder,
			RuleID:      parsed.RuleID,
			EdgeEventID: parsed.EdgeEventID,
		}, nil
	}

	// 4. Shared fingerprint and current evaluation / alarm state.
	subjectDeviceID, subjectPointID := edgePolicySubjectRefs(assigned)
	fingerprint, err := alarmmodel.Fingerprint(
		parsed.TenantID, parsed.SiteID,
		assigned.Policy.SourceType, assigned.Policy.SourceReference, assigned.Policy.AlarmType,
		subjectDeviceID, subjectPointID,
	)
	if err != nil {
		return EdgeAlarmEventResult{}, err
	}
	persisted, hasPersisted, err := loadPersistedEvaluation(tenantCtx, tx, parsed.TenantID, parsed.SiteID, parsed.RuleID, true)
	if err != nil {
		return EdgeAlarmEventResult{}, err
	}
	current, activeErr := getActiveByFingerprint(tenantCtx, tx, parsed.TenantID, parsed.SiteID, fingerprint, true)
	hasActive := activeErr == nil
	if activeErr != nil && !errors.Is(activeErr, ErrNotFound) {
		return EdgeAlarmEventResult{}, activeErr
	}

	// 5. Project transition into events + alarm_current + timeline + outbox.
	var outcome, resultAlarmID, resultEventID string
	var activeChanged bool
	var nextStatus EvaluationStatus
	var nextEffect EvaluationEffect
	var nextActiveAlarmID, nextActiveCorrelationID string
	var nextCandidateSince *string
	var nextRepeatCount uint64
	var nextWindow []TimedSample
	if hasPersisted {
		nextWindow = persisted.State.SampleWindow
	}

	evidence := []alarmmodel.EvidenceReference{{
		Kind:       "edge-rule-event",
		Reference:  parsed.EdgeEventID,
		CapturedAt: parsed.OccurredAt.Format(time.RFC3339Nano),
	}}
	ruleRev := fmt.Sprintf("%s#%d", assigned.Policy.PolicyID, assigned.Policy.Revision)
	actorID := truncateBounded("edge-gateway:"+parsed.GatewayID, 256)
	correlationID := truncateBounded(parsed.EdgeEventID, 256)

	if parsed.EventType == edgecontrol.EdgeRuleEventRaised {
		severity := normalizeEdgeAlarmSeverity(parsed.Severity, assigned.Policy.Severity)
		title := firstNonEmptyEdgeString(parsed.Title, assigned.Policy.Title, assigned.Policy.AlarmType)
		summary := firstNonEmptyEdgeString(parsed.Reason, assigned.Policy.Summary, title)
		effectiveAt := parsed.OccurredAt
		if hasActive {
			effectiveAt = clampMonotonicIncidentTime(effectiveAt, current)
			eventUUID := dereference(current.EventID)
			if eventUUID != "" {
				if _, err := tx.Exec(tenantCtx, `
UPDATE alarm_runtime.events
SET metadata = metadata || $4::jsonb
WHERE tenant_id = $1 AND site_id = $2 AND event_id = $3`,
					parsed.TenantID, parsed.SiteID, eventUUID, metadataJSON,
				); err != nil {
					return EdgeAlarmEventResult{}, fmt.Errorf("update edge alarm event metadata: %w", err)
				}
			}
			updated, err := store.publishInTransaction(tenantCtx, tx, parsed.TenantID, parsed.SiteID, Publication{
				DeviceID:        subjectDeviceID,
				EventID:         current.EventID,
				PointID:         subjectPointID,
				AlarmType:       assigned.Policy.AlarmType,
				SourceType:      assigned.Policy.SourceType,
				SourceReference: assigned.Policy.SourceReference,
				RuleRevision:    ruleRev,
				Title:           truncateBounded(title, 256),
				Summary:         truncateBounded(summary, 2048),
				Severity:        severity,
				OccurredAt:      effectiveAt.Format(time.RFC3339Nano),
				Evidence:        evidence,
				ActorType:       "WORKLOAD",
				ActorID:         actorID,
				CorrelationID:   correlationID,
			})
			if err != nil {
				return EdgeAlarmEventResult{}, err
			}
			outcome = EdgeAlarmOutcomeUpdated
			resultAlarmID = updated.AlarmID
			resultEventID = eventUUID
			nextStatus = EvaluationMatched
			nextEffect = EvaluationEffectNone
			nextActiveAlarmID = updated.AlarmID
			nextActiveCorrelationID = updated.IncidentCorrelationID
		} else {
			eventUUID, err := store.nextID(effectiveAt)
			if err != nil {
				return EdgeAlarmEventResult{}, err
			}
			eventTypeLabel := firstNonEmptyEdgeString(parsed.AlarmType, assigned.Policy.AlarmType)
			if _, err := tx.Exec(tenantCtx, `
INSERT INTO alarm_runtime.events (
	event_id, tenant_id, site_id, device_id, point_id, event_type, severity, message, start_time, end_time, status, metadata
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULL,'ACTIVE',$10)`,
				eventUUID, parsed.TenantID, parsed.SiteID, subjectDeviceID, subjectPointID,
				truncateBounded(eventTypeLabel, 128), string(severity), truncateBounded(summary, 2048),
				effectiveAt, metadataJSON,
			); err != nil {
				return EdgeAlarmEventResult{}, fmt.Errorf("insert edge alarm event: %w", err)
			}
			incident, err := store.publishInTransaction(tenantCtx, tx, parsed.TenantID, parsed.SiteID, Publication{
				DeviceID:        subjectDeviceID,
				EventID:         &eventUUID,
				PointID:         subjectPointID,
				AlarmType:       assigned.Policy.AlarmType,
				SourceType:      assigned.Policy.SourceType,
				SourceReference: assigned.Policy.SourceReference,
				RuleRevision:    ruleRev,
				Title:           truncateBounded(title, 256),
				Summary:         truncateBounded(summary, 2048),
				Severity:        severity,
				OccurredAt:      effectiveAt.Format(time.RFC3339Nano),
				Evidence:        evidence,
				ActorType:       "WORKLOAD",
				ActorID:         actorID,
				CorrelationID:   correlationID,
			})
			if err != nil {
				return EdgeAlarmEventResult{}, err
			}
			if err := store.enqueueRuleActionsWithFilterInTx(
				tenantCtx, tx, parsed.TenantID, parsed.SiteID, incident, assigned, ActionTriggerRaise, effectiveAt, false,
			); err != nil {
				return EdgeAlarmEventResult{}, err
			}
			outcome = EdgeAlarmOutcomeRaised
			resultAlarmID = incident.AlarmID
			resultEventID = eventUUID
			activeChanged = true
			nextStatus = EvaluationMatched
			nextEffect = EvaluationEffectPublish
			nextActiveAlarmID = incident.AlarmID
			nextActiveCorrelationID = incident.IncidentCorrelationID
		}
	} else {
		clearMetaPatch, err := json.Marshal(map[string]any{
			"clearSource":           "EDGE_RULE",
			"clearGatewayId":        parsed.GatewayID,
			"clearEdgeEventId":      parsed.EdgeEventID,
			"clearOffline":          parsed.Offline,
			"clearReason":           parsed.Reason,
			"clearOccurredAt":       parsed.OccurredAt.Format(time.RFC3339Nano),
			"clearTriggeredActions": parsed.TriggeredActions,
		})
		if err != nil {
			return EdgeAlarmEventResult{}, fmt.Errorf("encode edge alarm clear metadata: %w", err)
		}
		if hasActive {
			effectiveAt := clampMonotonicIncidentTime(parsed.OccurredAt, current)
			cleared, err := store.clearInTransaction(tenantCtx, tx, parsed.TenantID, parsed.SiteID, Recovery{
				Fingerprint:           fingerprint,
				IncidentCorrelationID: current.IncidentCorrelationID,
				OccurredAt:            effectiveAt.Format(time.RFC3339Nano),
				Reason:                truncateBounded(firstNonEmptyEdgeString(parsed.Reason, "边缘规则恢复"), 1024),
				Evidence:              evidence,
				RuleRevision:          ruleRev,
				ActorType:             "WORKLOAD",
				ActorID:               actorID,
				CorrelationID:         correlationID,
			})
			if err != nil {
				return EdgeAlarmEventResult{}, err
			}
			eventUUID := dereference(cleared.EventID)
			if eventUUID != "" {
				if _, err := tx.Exec(tenantCtx, `
UPDATE alarm_runtime.events
SET end_time = GREATEST(start_time, $4),
    status = 'CLEARED',
    metadata = metadata || $5::jsonb
WHERE tenant_id = $1 AND site_id = $2 AND event_id = $3`,
					parsed.TenantID, parsed.SiteID, eventUUID, parsed.OccurredAt, clearMetaPatch,
				); err != nil {
					return EdgeAlarmEventResult{}, fmt.Errorf("close edge alarm event: %w", err)
				}
			}
			if err := store.enqueueRuleActionsWithFilterInTx(
				tenantCtx, tx, parsed.TenantID, parsed.SiteID, cleared, assigned, ActionTriggerClear, effectiveAt, false,
			); err != nil {
				return EdgeAlarmEventResult{}, err
			}
			outcome = EdgeAlarmOutcomeCleared
			resultAlarmID = cleared.AlarmID
			resultEventID = eventUUID
			activeChanged = true
			nextStatus = EvaluationNotMatched
			nextEffect = EvaluationEffectClear
		} else {
			outcome = EdgeAlarmOutcomeCleared
			nextStatus = EvaluationNotMatched
			nextEffect = EvaluationEffectNone
		}
	}

	// 6. Persist evaluation state and audit event with valid EvaluationSnapshot.
	inputs := map[string]InputFact{}
	if hasPersisted && persisted.Snapshot.Inputs != nil {
		inputs = persisted.Snapshot.Inputs
	}
	evalSnapshot := EvaluationSnapshot{
		TenantID:      parsed.TenantID,
		SiteID:        parsed.SiteID,
		SubjectType:   assigned.SubjectType,
		SubjectID:     assigned.SubjectID,
		DeviceID:      subjectDeviceID,
		EventID:       optionalUUIDPtr(resultEventID),
		PointID:       subjectPointID,
		InputRevision: truncateBounded("edge-event:"+parsed.EdgeEventID, 256),
		AsOf:          parsed.OccurredAt.Format(time.RFC3339Nano),
		Inputs:        inputs,
		CorrelationID: correlationID,
		EdgeProvenance: &EdgeEvaluationProvenance{
			GatewayID:        parsed.GatewayID,
			EdgeEventID:      parsed.EdgeEventID,
			EventType:        parsed.EventType,
			OccurredAt:       parsed.OccurredAt.Format(time.RFC3339Nano),
			Offline:          parsed.Offline,
			BundleRevision:   parsed.BundleRevision,
			EdgeBundleDigest: parsed.EdgeBundleDigest,
		},
	}
	if err := evalSnapshot.Validate(); err != nil {
		return EdgeAlarmEventResult{}, err
	}
	nextVersion := uint64(1)
	if hasPersisted {
		nextVersion = persisted.State.Version + 1
	}
	decision := EvaluationDecision{
		Status:      nextStatus,
		Effect:      nextEffect,
		Fingerprint: fingerprint,
		State: AlarmEvaluationState{
			PolicyRevisionID:            assigned.Policy.PolicyRevisionID,
			Status:                      nextStatus,
			CandidateSince:              nextCandidateSince,
			RepeatCount:                 nextRepeatCount,
			LastInputRevision:           evalSnapshot.InputRevision,
			ActiveAlarmID:               nextActiveAlarmID,
			ActiveIncidentCorrelationID: nextActiveCorrelationID,
			SampleWindow:                nextWindow,
			LastEvaluatedAt:             now.Format(time.RFC3339Nano),
			Version:                     nextVersion,
		},
	}
	if err := persistEvaluation(tenantCtx, tx, assigned, decision, evalSnapshot, persisted); err != nil {
		return EdgeAlarmEventResult{}, err
	}

	// 7. Advance projection cursor and write immutable receipt.
	if _, err := tx.Exec(tenantCtx, `
INSERT INTO alarm_runtime.edge_rule_projection_cursor (
	tenant_id, site_id, rule_id, gateway_id, last_occurred_at, last_transition_seq,
	last_event_type, last_edge_event_id, last_bundle_revision, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (tenant_id, site_id, rule_id) DO UPDATE SET
	gateway_id = EXCLUDED.gateway_id,
	last_occurred_at = EXCLUDED.last_occurred_at,
	last_transition_seq = EXCLUDED.last_transition_seq,
	last_event_type = EXCLUDED.last_event_type,
	last_edge_event_id = EXCLUDED.last_edge_event_id,
	last_bundle_revision = EXCLUDED.last_bundle_revision,
	updated_at = EXCLUDED.updated_at`,
		parsed.TenantID, parsed.SiteID, parsed.RuleID, parsed.GatewayID, parsed.OccurredAt, parsed.TransitionSeq,
		parsed.EventType, parsed.EdgeEventID, parsed.BundleRevision, now,
	); err != nil {
		return EdgeAlarmEventResult{}, fmt.Errorf("upsert edge rule projection cursor: %w", err)
	}
	if err := store.insertEdgeReceiptInTx(tenantCtx, tx, parsed, outcome, resultAlarmID, resultEventID, metadataJSON, now); err != nil {
		return EdgeAlarmEventResult{}, err
	}
	if err := tx.Commit(tenantCtx); err != nil {
		return EdgeAlarmEventResult{}, fmt.Errorf("commit edge alarm event projection: %w", err)
	}

	// 8. Cascade dependent site rules when an incident transitioned.
	if activeChanged {
		if _, cascadeErr := store.cascadeDownstreamSiteRules(tenantCtx, parsed.TenantID, parsed.SiteID, map[string]bool{parsed.RuleID: true}, now); cascadeErr != nil {
			return EdgeAlarmEventResult{}, cascadeErr
		}
	}

	return EdgeAlarmEventResult{
		Outcome:     outcome,
		RuleID:      parsed.RuleID,
		EdgeEventID: parsed.EdgeEventID,
		AlarmID:     resultAlarmID,
		EventID:     resultEventID,
	}, nil
}

type existingEdgeReceipt struct {
	Outcome string
	AlarmID string
	EventID string
}

func findExistingEdgeEventReceiptInTx(ctx context.Context, tx pgx.Tx, parsed parsedEdgeAlarmEvent) (existingEdgeReceipt, bool, error) {
	var existing existingEdgeReceipt
	var alarmID, eventID *string
	err := tx.QueryRow(ctx, `
SELECT outcome, alarm_id::text, event_id::text
FROM alarm_runtime.edge_alarm_event_receipt
WHERE tenant_id = $1
  AND site_id = $2
  AND gateway_id = $3
  AND edge_event_id = $4
  AND occurred_at = $5
  AND outcome <> 'DUPLICATE'
LIMIT 1`,
		parsed.TenantID, parsed.SiteID, parsed.GatewayID, parsed.EdgeEventID, parsed.OccurredAt,
	).Scan(&existing.Outcome, &alarmID, &eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return existingEdgeReceipt{}, false, nil
	}
	if err != nil {
		return existingEdgeReceipt{}, false, fmt.Errorf("lookup edge alarm receipt: %w", err)
	}
	if alarmID != nil {
		existing.AlarmID = *alarmID
	}
	if eventID != nil {
		existing.EventID = *eventID
	}
	return existing, true, nil
}

func isEdgeEventOutOfOrderInTx(ctx context.Context, tx pgx.Tx, parsed parsedEdgeAlarmEvent) (bool, error) {
	var curGatewayID string
	var curOccurredAt time.Time
	var curSeq *int64
	err := tx.QueryRow(ctx, `
SELECT gateway_id, last_occurred_at, last_transition_seq
FROM alarm_runtime.edge_rule_projection_cursor
WHERE tenant_id = $1 AND site_id = $2 AND rule_id = $3
FOR UPDATE`,
		parsed.TenantID, parsed.SiteID, parsed.RuleID,
	).Scan(&curGatewayID, &curOccurredAt, &curSeq)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read edge rule projection cursor: %w", err)
	}
	if parsed.OccurredAt.Before(curOccurredAt.UTC()) {
		return true, nil
	}
	if parsed.OccurredAt.Equal(curOccurredAt.UTC()) &&
		curGatewayID == parsed.GatewayID &&
		parsed.TransitionSeq != nil && curSeq != nil &&
		*parsed.TransitionSeq <= *curSeq {
		return true, nil
	}
	return false, nil
}

func (store *PostgresStore) insertEdgeReceiptInTx(
	ctx context.Context,
	tx pgx.Tx,
	parsed parsedEdgeAlarmEvent,
	outcome, alarmID, eventID string,
	metadataJSON []byte,
	now time.Time,
) error {
	receiptID, err := store.nextID(now)
	if err != nil {
		return fmt.Errorf("generate edge alarm receipt id: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO alarm_runtime.edge_alarm_event_receipt (
	receipt_id, tenant_id, site_id, gateway_id, edge_event_id, mqtt_message_id, rule_id,
	event_type, occurred_at, received_at, offline, bundle_revision, edge_bundle_digest,
	transition_seq, outcome, alarm_id, event_id, metadata, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		receiptID, parsed.TenantID, parsed.SiteID, parsed.GatewayID, parsed.EdgeEventID,
		optionalUUIDPtr(parsed.MQTTMessageID), parsed.RuleID, parsed.EventType,
		parsed.OccurredAt, parsed.ReceivedAt, parsed.Offline, parsed.BundleRevision,
		parsed.EdgeBundleDigest, parsed.TransitionSeq, outcome,
		optionalUUIDPtr(alarmID), optionalUUIDPtr(eventID), metadataJSON, now,
	)
	if err != nil {
		return fmt.Errorf("insert edge alarm receipt: %w", err)
	}
	return nil
}

func buildEdgeReceiptMetadata(parsed parsedEdgeAlarmEvent) map[string]any {
	meta := map[string]any{
		"source":         "EDGE_RULE",
		"gatewayId":      parsed.GatewayID,
		"edgeEventId":    parsed.EdgeEventID,
		"bundleRevision": parsed.BundleRevision,
		"offline":        parsed.Offline,
		"occurredAt":     parsed.OccurredAt.Format(time.RFC3339Nano),
		"receivedAt":     parsed.ReceivedAt.Format(time.RFC3339Nano),
	}
	if parsed.EdgeBundleDigest != "" {
		meta["edgeBundleDigest"] = parsed.EdgeBundleDigest
	}
	if parsed.Reason != "" {
		meta["reason"] = parsed.Reason
	}
	if len(parsed.TriggeredActions) > 0 {
		meta["triggeredActions"] = parsed.TriggeredActions
	}
	if parsed.AlarmType != "" {
		meta["alarmType"] = parsed.AlarmType
	}
	return meta
}

func edgePolicySubjectRefs(assigned assignedAlarmPolicy) (deviceID *string, pointID *string) {
	switch assigned.SubjectType {
	case "DEVICE":
		s := assigned.SubjectID
		return &s, nil
	default:
		return nil, nil
	}
}

func optionalUUIDPtr(raw string) *string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizeEdgeAlarmSeverity(raw string, fallback alarmmodel.Severity) alarmmodel.Severity {
	if sev, ok := normalizeControlEventSeverity(alarmmodel.Severity(raw), fallback); ok && sev != "" {
		return sev
	}
	if sev, ok := normalizeControlEventSeverity(fallback, alarmmodel.SeverityMinor); ok && sev != "" {
		return sev
	}
	return alarmmodel.SeverityMinor
}

func firstNonEmptyEdgeString(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func clampMonotonicIncidentTime(target time.Time, current alarmmodel.Alarm) time.Time {
	clamped := target.UTC()
	for _, existing := range []string{current.UpdatedAt, current.LastOccurredAt} {
		if parsed, err := time.Parse(time.RFC3339Nano, existing); err == nil && parsed.UTC().After(clamped) {
			clamped = parsed.UTC()
		}
	}
	return clamped
}
