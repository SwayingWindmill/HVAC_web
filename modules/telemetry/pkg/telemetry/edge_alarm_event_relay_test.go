package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

type edgeAlarmRelayRepositoryStub struct {
	pending         []PendingEdgeAlarmEvent
	deliveredID     string
	retryID         string
	retryAt         time.Time
	retryCode       string
	deadLetterID    string
	deadLetterCode  string
	pendingCount    int64
	deadLetterCount int64
}

func (s *edgeAlarmRelayRepositoryStub) ClaimPendingEdgeAlarmEvents(context.Context, string, int, time.Time, time.Duration) ([]PendingEdgeAlarmEvent, error) {
	return s.pending, nil
}
func (s *edgeAlarmRelayRepositoryStub) MarkEdgeAlarmEventDelivered(_ context.Context, messageID, _ string, _ time.Time) error {
	s.deliveredID = messageID
	return nil
}
func (s *edgeAlarmRelayRepositoryStub) MarkEdgeAlarmEventRetry(_ context.Context, messageID, _ string, availableAt time.Time, errorCode string) error {
	s.retryID = messageID
	s.retryAt = availableAt
	s.retryCode = errorCode
	return nil
}
func (s *edgeAlarmRelayRepositoryStub) MarkEdgeAlarmEventDeadLetter(_ context.Context, messageID, _ string, _ time.Time, errorCode string) error {
	s.deadLetterID = messageID
	s.deadLetterCode = errorCode
	return nil
}
func (s *edgeAlarmRelayRepositoryStub) CountEdgeAlarmEventOutboxStates(context.Context) (int64, int64, error) {
	return s.pendingCount, s.deadLetterCount, nil
}

type edgeAlarmTransportStub struct {
	calls  int
	events []PendingEdgeAlarmEvent
	err    error
}

func (s *edgeAlarmTransportStub) DeliverEdgeAlarmEvent(_ context.Context, ev PendingEdgeAlarmEvent) error {
	s.calls++
	s.events = append(s.events, ev)
	if s.err != nil {
		return s.err
	}
	return nil
}

func samplePendingEdgeAlarmEvent(now time.Time) PendingEdgeAlarmEvent {
	return PendingEdgeAlarmEvent{
		MessageID:      "01950000-0000-7000-8000-000000000010",
		TenantID:       "01950000-0000-7000-8000-000000000001",
		SiteID:         "01950000-0000-7000-8000-000000000002",
		GatewayID:      "01950000-0000-7000-8000-000000000003",
		EventType:      "EDGE_RULE_ALARM_RAISED",
		SourceType:     "EDGE_RULE",
		SourceID:       "01950000-0000-7000-8000-000000000004",
		EventTime:      now.Add(-2 * time.Minute),
		Severity:       "CRITICAL",
		SourceSequence: 7,
		Data:           json.RawMessage(`{"eventId":"edge-rule:01950000-0000-7000-8000-000000000004:1"}`),
		ReceivedAt:     now,
	}
}

func TestIsEdgeRuleAlarmEventType(t *testing.T) {
	for _, evType := range []string{"EDGE_RULE_ALARM_RAISED", "EDGE_RULE_ALARM_CLEARED", "EDGE_RULE_EDGE_RULE_ALARM_RAISED", "EDGE_RULE_EDGE_RULE_ALARM_CLEARED"} {
		if !IsEdgeRuleAlarmEventType(evType) {
			t.Fatalf("expected IsEdgeRuleAlarmEventType(%q) == true", evType)
		}
	}
	for _, nonEdge := range []string{"GATEWAY_CONNECTED", "LWT", "DEVICE_OFFLINE", ""} {
		if IsEdgeRuleAlarmEventType(nonEdge) {
			t.Fatalf("expected IsEdgeRuleAlarmEventType(%q) == false", nonEdge)
		}
	}
}

func TestEdgeAlarmEventRelaySuccessRetryAndDeadLetterMetrics(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	reg := observability.NewRegistry()
	ev := samplePendingEdgeAlarmEvent(now)

	repo := &edgeAlarmRelayRepositoryStub{pending: []PendingEdgeAlarmEvent{ev}}
	tr := &edgeAlarmTransportStub{}
	relay, err := NewEdgeAlarmEventRelay(EdgeAlarmEventRelayConfig{
		Repository: repo, Transport: tr, Metrics: reg, WorkerID: "w1", LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := relay.RelayOnce(context.Background(), 10)
	if err != nil || n != 1 || repo.deliveredID != ev.MessageID || tr.calls != 1 {
		t.Fatalf("unexpected success outcome: n=%d err=%v repo=%+v", n, err, repo)
	}

	retryRepo := &edgeAlarmRelayRepositoryStub{pending: []PendingEdgeAlarmEvent{ev}, pendingCount: 1}
	retryTr := &edgeAlarmTransportStub{err: errors.New("alarm service down")}
	retryRelay, err := NewEdgeAlarmEventRelay(EdgeAlarmEventRelayConfig{
		Repository: retryRepo, Transport: retryTr, Metrics: reg, WorkerID: "w2", LeaseDuration: 30 * time.Second, RetryDelay: 4 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := retryRelay.RelayOnce(context.Background(), 10); err == nil || n != 0 || retryRepo.retryID != ev.MessageID || retryRepo.retryAt != now.Add(4*time.Second) || retryRepo.retryCode != "EDGE_ALARM_EVENT_UNAVAILABLE" {
		t.Fatalf("unexpected retry state: n=%d err=%v repo=%+v", n, err, retryRepo)
	}

	permRepo := &edgeAlarmRelayRepositoryStub{pending: []PendingEdgeAlarmEvent{ev}, deadLetterCount: 1}
	permTr := &edgeAlarmTransportStub{err: ErrPermanentEdgeAlarmEventFailure}
	permRelay, err := NewEdgeAlarmEventRelay(EdgeAlarmEventRelayConfig{
		Repository: permRepo, Transport: permTr, Metrics: reg, WorkerID: "w3", LeaseDuration: 30 * time.Second, RetryDelay: 4 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := permRelay.RelayOnce(context.Background(), 10); !errors.Is(err, ErrPermanentEdgeAlarmEventFailure) || n != 0 || permRepo.deadLetterID != ev.MessageID || permRepo.deadLetterCode != "EDGE_ALARM_EVENT_PERMANENT_FAILURE" {
		t.Fatalf("unexpected permanent DLQ state: n=%d err=%v repo=%+v", n, err, permRepo)
	}

	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	m := rec.Body.String()
	if !strings.Contains(m, `hvac_edge_alarm_event_dead_letter_total{reason_family="dependency"} 1`) || !strings.Contains(m, `hvac_edge_alarm_event_outbox_depth{state="dead_letter"} 1`) {
		t.Fatalf("missing edge alarm metrics:\n%s", m)
	}
}

func TestHTTPEdgeAlarmEventTransportClassifiesStatuses(t *testing.T) {
	var statusCode int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != InternalEdgeAlarmEventsPath {
			t.Fatalf("path=%q want %q", r.URL.Path, InternalEdgeAlarmEventsPath)
		}
		w.WriteHeader(statusCode)
	}))
	defer server.Close()

	tr, err := NewHTTPEdgeAlarmEventTransport(HTTPEdgeAlarmEventTransportConfig{
		Endpoint: server.URL + InternalEdgeAlarmEventsPath, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := samplePendingEdgeAlarmEvent(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC))

	for _, perm := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound} {
		statusCode = perm
		if err := tr.DeliverEdgeAlarmEvent(context.Background(), ev); !errors.Is(err, ErrPermanentEdgeAlarmEventFailure) {
			t.Fatalf("status %d expected ErrPermanentEdgeAlarmEventFailure, got %v", perm, err)
		}
	}
	for _, retry := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusInternalServerError} {
		statusCode = retry
		if err := tr.DeliverEdgeAlarmEvent(context.Background(), ev); err == nil || errors.Is(err, ErrPermanentEdgeAlarmEventFailure) {
			t.Fatalf("status %d expected retryable error, got %v", retry, err)
		}
	}
	statusCode = http.StatusOK
	if err := tr.DeliverEdgeAlarmEvent(context.Background(), ev); err != nil {
		t.Fatalf("HTTP 200 failed: %v", err)
	}
}
