package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

func TestParseEdgeAlarmEventRequestSupportsCanonicalDoublePrefixedAndFallbacks(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	const (
		tenantID = "0195b000-0000-7000-8000-000000000001"
		siteID   = "0195b000-0000-7000-8000-000000000002"
		ruleID   = "0195b000-0000-7000-8000-000000000003"
		msgID    = "0195b000-0000-7000-8000-000000000004"
	)

	canonical := EdgeAlarmEventRequest{
		SchemaVersion:  1,
		MessageID:      msgID,
		TenantID:       tenantID,
		SiteID:         siteID,
		GatewayID:      "gw-1",
		EventType:      "EDGE_RULE_ALARM_RAISED",
		SourceType:     "EDGE_RULE",
		SourceID:       ruleID,
		EventTime:      "2026-11-22T11:55:00Z",
		Severity:       "CRITICAL",
		SourceSequence: 4,
		ReceivedAt:     "2026-11-22T12:00:00Z",
		Data:           json.RawMessage(`{"eventId":"edge-rule:` + ruleID + `:9","eventType":"EDGE_RULE_ALARM_RAISED","ruleId":"` + ruleID + `","alarmType":"HIGH_TEMP","title":"High Temp","severity":"HIGH","occurredAt":"2026-11-22T11:55:00Z","bundleRevision":3,"edgeBundleDigest":"` + strings.Repeat("a", 64) + `","offline":true,"reason":"t > 45","triggeredActions":["cmd-1"]}`),
	}
	parsed, err := ParseEdgeAlarmEventRequest(canonical, now)
	if err != nil {
		t.Fatalf("ParseEdgeAlarmEventRequest(canonical): %v", err)
	}
	if parsed.EdgeEventID != "edge-rule:"+ruleID+":9" || parsed.TransitionSeq == nil || *parsed.TransitionSeq != 9 || !parsed.Offline || parsed.BundleRevision != 3 {
		t.Fatalf("unexpected canonical parse: %+v", parsed)
	}
	if sev := normalizeEdgeAlarmSeverity(parsed.Severity, alarmmodel.SeverityMinor); sev != alarmmodel.SeverityMajor {
		t.Fatalf("HIGH did not normalize to MAJOR, got %s", sev)
	}

	doublePrefixed := EdgeAlarmEventRequest{
		SchemaVersion:  1,
		MessageID:      msgID,
		TenantID:       tenantID,
		SiteID:         siteID,
		GatewayID:      "gw-1",
		EventType:      "EDGE_RULE_EDGE_RULE_ALARM_CLEARED",
		SourceType:     "RULE",
		SourceID:       ruleID,
		EventTime:      "2026-11-22T11:56:00Z",
		Severity:       "INFO",
		SourceSequence: 5,
		Data:           json.RawMessage(`{"title":"Recovered"}`),
	}
	fallbackParsed, err := ParseEdgeAlarmEventRequest(doublePrefixed, now)
	if err != nil {
		t.Fatalf("ParseEdgeAlarmEventRequest(doublePrefixed): %v", err)
	}
	if fallbackParsed.EventType != "EDGE_RULE_ALARM_CLEARED" || fallbackParsed.RuleID != ruleID || fallbackParsed.EdgeEventID != "mqtt:"+msgID || fallbackParsed.TransitionSeq != nil {
		t.Fatalf("unexpected fallback parse: %+v", fallbackParsed)
	}

	bad := canonical
	bad.Data = json.RawMessage(`{"edgeBundleDigest":"not-hex"}`)
	if _, err := ParseEdgeAlarmEventRequest(bad, now); !errors.Is(err, ErrEdgeAlarmEventInvalid) {
		t.Fatalf("expected ErrEdgeAlarmEventInvalid for bad digest, got %v", err)
	}
}

type stubEdgeIngestor struct {
	calls  int
	last   EdgeAlarmEventRequest
	result EdgeAlarmEventResult
	err    error
}

func (s *stubEdgeIngestor) IngestEdgeAlarmEvent(_ context.Context, req EdgeAlarmEventRequest, _ time.Time) (EdgeAlarmEventResult, error) {
	s.calls++
	s.last = req
	return s.result, s.err
}

func TestEdgeAlarmEventHandlerValidationAndResponses(t *testing.T) {
	stub := &stubEdgeIngestor{result: EdgeAlarmEventResult{Outcome: EdgeAlarmOutcomeRaised, RuleID: "r1", EdgeEventID: "e1", AlarmID: "a1"}}
	h, err := NewEdgeAlarmEventHandler(stub)
	if err != nil {
		t.Fatal(err)
	}

	recBad := httptest.NewRecorder()
	h.ServeHTTP(recBad, httptest.NewRequest(http.MethodPost, InternalEdgeAlarmEventsPath, strings.NewReader(`{"schemaVersion":1,"extra":true}`)))
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d want 400", recBad.Code)
	}

	recWrongPath := httptest.NewRecorder()
	h.ServeHTTP(recWrongPath, httptest.NewRequest(http.MethodPost, "/internal/v1/alarms/other", nil))
	if recWrongPath.Code != http.StatusNotFound {
		t.Fatalf("wrong path status=%d want 404", recWrongPath.Code)
	}

	body := `{"schemaVersion":1,"messageId":"0195b000-0000-7000-8000-000000000004","tenantId":"0195b000-0000-7000-8000-000000000001","siteId":"0195b000-0000-7000-8000-000000000002","gatewayId":"gw-1","eventType":"EDGE_RULE_ALARM_RAISED","sourceType":"EDGE_RULE","sourceId":"0195b000-0000-7000-8000-000000000003","eventTime":"2026-11-22T11:55:00Z","severity":"CRITICAL","sourceSequence":1,"receivedAt":"2026-11-22T11:55:01Z","data":{}}`
	recOK := httptest.NewRecorder()
	h.ServeHTTP(recOK, httptest.NewRequest(http.MethodPost, InternalEdgeAlarmEventsPath, strings.NewReader(body)))
	if recOK.Code != http.StatusOK || stub.calls != 1 {
		t.Fatalf("valid POST status=%d calls=%d body=%s", recOK.Code, stub.calls, recOK.Body.String())
	}
}
