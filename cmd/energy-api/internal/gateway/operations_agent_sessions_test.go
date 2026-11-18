package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func agentSessionSnapshotFixture() map[string]any {
	return map[string]any{
		"session": map[string]any{
			"id":                "session-1",
			"tenantId":          "tenant-1",
			"siteId":            "site-1",
			"agentDefinitionId": "operations-investigation.v1",
			"createdBy":         "principal-1",
			"revision":          2,
			"createdAt":         1000,
			"updatedAt":         1100,
			"status":            "COMPLETED",
			"activeRunId":       nil,
		},
		"runs":           []any{},
		"messages":       []any{},
		"toolExecutions": []any{},
		"artifacts":      []any{},
	}
}

func agentSessionEventFixture(eventType string, payload any, runID any) []byte {
	raw, err := json.Marshal(map[string]any{
		"version":   "hvac.agent.event/v1",
		"type":      eventType,
		"sessionId": "session-1",
		"runId":     runID,
		"sequence":  0,
		"at":        1100,
		"payload":   payload,
	})
	if err != nil {
		panic(err)
	}
	return raw
}

func TestMatchPublicAgentSessionRoute(t *testing.T) {
	siteID := "0198d4f0-4a57-7c8a-87b1-1c2f3a4b5c6d"
	tests := []struct {
		path     string
		kind     string
		template string
	}{
		{"/api/v1/sites/" + siteID + "/operations/agent-sessions", "COLLECTION", PublicAgentSessionsPathTemplate},
		{"/api/v1/sites/" + siteID + "/operations/agent-sessions/session-1", "GET", PublicAgentSessionPathTemplate},
		{"/api/v1/sites/" + siteID + "/operations/agent-sessions/session-1/events", "STREAM", PublicAgentSessionEventsPathTemplate},
		{"/api/v1/sites/" + siteID + "/operations/agent-sessions/session-1:run", "START", PublicAgentSessionStartRunPathTemplate},
		{"/api/v1/sites/" + siteID + "/operations/agent-sessions/session-1:cancel", "CANCEL", PublicAgentSessionCancelPathTemplate},
		{"/api/v1/sites/" + siteID + "/operations/agent-sessions/session-1:submit-input", "SUBMIT_INPUT", PublicAgentSessionSubmitInputPathTemplate},
	}
	for _, item := range tests {
		route, matches := matchPublicAgentSessionRoute(item.path)
		if !matches || route.kind != item.kind || route.template != item.template || route.siteID != siteID {
			t.Fatalf("unexpected route for %s: %#v, matches=%v", item.path, route, matches)
		}
	}
}

func TestValidateAgentSessionEventRejectsNonPublicFieldsAndPiEvents(t *testing.T) {
	valid := agentSessionEventFixture("session.snapshot", map[string]any{"snapshot": agentSessionSnapshotFixture()}, nil)
	if _, err := validateAgentSessionEvent("session.snapshot", valid); err != nil {
		t.Fatalf("valid public snapshot rejected: %v", err)
	}

	var withThinking map[string]any
	if err := json.Unmarshal(valid, &withThinking); err != nil {
		t.Fatal(err)
	}
	withThinking["thinking"] = "hidden reasoning"
	rawThinking, _ := json.Marshal(withThinking)
	if _, err := validateAgentSessionEvent("session.snapshot", rawThinking); err == nil {
		t.Fatal("event with hidden reasoning field was accepted")
	}

	run := map[string]any{
		"id":        "run-1",
		"sessionId": "session-1",
		"modelRef": map[string]any{
			"provider": "openai",
			"model":    "gpt-5",
			"apiKey":   "[REDACTED_SECRET]",
		},
		"status":      "RUNNING",
		"startedAt":   1000,
		"finishedAt":  nil,
		"usage":       map[string]any{"inputTokens": 0, "outputTokens": 0, "modelCalls": 0, "toolCalls": 0},
		"failureCode": nil,
	}
	withCredential := agentSessionEventFixture("run.started", map[string]any{"run": run}, "run-1")
	if _, err := validateAgentSessionEvent("run.started", withCredential); err == nil {
		t.Fatal("event with provider credential field was accepted")
	}

	piEvent := agentSessionEventFixture("message_update", map[string]any{}, "run-1")
	if _, err := validateAgentSessionEvent("message_update", piEvent); err == nil {
		t.Fatal("Pi-specific event type was accepted")
	}
}

func TestForwardAgentSessionEventStreamStartsWithValidatedDurableSnapshot(t *testing.T) {
	snapshot := agentSessionEventFixture("session.snapshot", map[string]any{"snapshot": agentSessionSnapshotFixture()}, nil)
	stream := "event: session.snapshot\ndata: " + string(snapshot) + "\n\n"
	recorder := httptest.NewRecorder()
	result, err := forwardAgentSessionEventStream(recorder, strings.NewReader(stream))
	if err != nil {
		t.Fatalf("valid stream rejected: %v", err)
	}
	if !result.started {
		t.Fatal("stream did not start")
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("unexpected content type %q", got)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "\"version\":\"hvac.agent.event/v1\"") {
		t.Fatalf("sanitized public event was not forwarded: %s", recorder.Body.String())
	}

	invalidFirst := agentSessionEventFixture("assistant.delta", map[string]any{"messageId": "message-1", "delta": "hello"}, "run-1")
	invalidRecorder := httptest.NewRecorder()
	invalidResult, invalidErr := forwardAgentSessionEventStream(
		invalidRecorder,
		strings.NewReader("event: assistant.delta\ndata: "+string(invalidFirst)+"\n\n"),
	)
	if invalidErr == nil || invalidResult.started {
		t.Fatal("stream not starting with durable snapshot was accepted")
	}
}

func TestValidateAgentSessionMutationRejectsSynthesizedOperatorIdentity(t *testing.T) {
	valid := []byte(`{"expectedRevision":2,"requestArtifactId":"request-1","value":"weekday"}`)
	if err := validateAgentSessionMutationBody(valid, "SUBMIT_INPUT"); err != nil {
		t.Fatalf("valid Operator Input rejected: %v", err)
	}
	forged := []byte(`{"expectedRevision":2,"requestArtifactId":"request-1","value":"weekday","submittedBy":"forged"}`)
	if err := validateAgentSessionMutationBody(forged, "SUBMIT_INPUT"); err == nil {
		t.Fatal("forged submittedBy field was accepted")
	}
}

func TestSanitizeAgentSessionProblemRejectsProviderOrCredentialFields(t *testing.T) {
	valid := []byte(`{"type":"urn:hvac:operations-agent:run_stale","title":"Agent Session conflict","status":409,"code":"RUN_STALE","detail":"Session revision changed."}`)
	sanitized, err := sanitizeAgentSessionProblem(valid, http.StatusConflict)
	if err != nil {
		t.Fatalf("valid Agent Session problem rejected: %v", err)
	}
	if string(sanitized) == "" || strings.Contains(string(sanitized), "providerMessage") {
		t.Fatalf("valid Agent Session problem was not safely re-encoded: %s", sanitized)
	}

	withProviderMessage := []byte(`{"type":"urn:hvac:operations-agent:run_stale","title":"Agent Session conflict","status":409,"code":"RUN_STALE","detail":"Session revision changed.","providerMessage":"hidden provider detail"}`)
	if _, err := sanitizeAgentSessionProblem(withProviderMessage, http.StatusConflict); err == nil {
		t.Fatal("Agent Session problem with provider field was accepted")
	}
	withCredential := []byte(`{"type":"urn:hvac:operations-agent:run_stale","title":"Agent Session conflict","status":409,"code":"RUN_STALE","detail":"Session revision changed.","apiKey":"[REDACTED_SECRET]"}`)
	if _, err := sanitizeAgentSessionProblem(withCredential, http.StatusConflict); err == nil {
		t.Fatal("Agent Session problem with credential-shaped field was accepted")
	}
	if _, err := sanitizeAgentSessionProblem(valid, http.StatusBadRequest); err == nil {
		t.Fatal("Agent Session problem with mismatched HTTP status was accepted")
	}
}

func TestAgentSessionEventStreamOutlivesTheServerWriteTimeout(t *testing.T) {
	snapshot := "event: session.snapshot\ndata: " + string(agentSessionEventFixture("session.snapshot", map[string]any{"snapshot": agentSessionSnapshotFixture()}, nil)) + "\n\n"
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstream, feed := io.Pipe()
		go func() {
			_, _ = feed.Write([]byte(snapshot))
			time.Sleep(300 * time.Millisecond)
			_, _ = feed.Write([]byte(snapshot))
			_ = feed.Close()
		}()
		_, _ = forwardAgentSessionEventStream(&statusRecorder{ResponseWriter: writer, status: http.StatusOK}, upstream)
	}))
	server.Config.WriteTimeout = 100 * time.Millisecond
	server.Start()
	defer server.Close()

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("stream was cut off: %v", err)
	}
	if got := strings.Count(string(body), "event: session.snapshot"); got != 2 {
		t.Fatalf("expected both events after the write timeout, got %d", got)
	}
}

func TestAgentSessionEventStreamEmitsKeepaliveDuringIdlePause(t *testing.T) {
	snapshot := "event: session.snapshot\ndata: " + string(agentSessionEventFixture("session.snapshot", map[string]any{"snapshot": agentSessionSnapshotFixture()}, nil)) + "\n\n"
	delta := "event: assistant.delta\ndata: " + string(agentSessionEventFixture("assistant.delta", map[string]any{"messageId": "message-1", "delta": "ok"}, "run-1")) + "\n\n"
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstream, feed := io.Pipe()
		go func() {
			_, _ = feed.Write([]byte(snapshot))
			time.Sleep(110 * time.Millisecond)
			_, _ = feed.Write([]byte(delta))
			_ = feed.Close()
		}()
		ctx, cancel := context.WithCancel(request.Context())
		defer cancel()
		_, _ = forwardAgentSessionEventStreamWithConfig(
			ctx,
			cancel,
			&statusRecorder{ResponseWriter: writer, status: http.StatusOK},
			upstream,
			90*time.Millisecond,
			30*time.Millisecond,
		)
	}))
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("stream failed during idle pause: %v", err)
	}
	text := string(body)
	if keepalives := strings.Count(text, ": keepalive\n\n"); keepalives < 2 {
		t.Fatalf("expected >= 2 keepalive comments during idle pause, got %d in %q", keepalives, text)
	}
	if !strings.Contains(text, "event: assistant.delta") {
		t.Fatalf("expected trailing assistant.delta event, got %q", text)
	}
}

type sseDeadlineStallConn struct {
	net.Conn
	stallWrites atomic.Bool
	mu          sync.Mutex
	deadline    time.Time
	closed      chan struct{}
	closeOnce   sync.Once
}

func (c *sseDeadlineStallConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return c.Conn.SetWriteDeadline(t)
}

func (c *sseDeadlineStallConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return c.Conn.SetDeadline(t)
}

func (c *sseDeadlineStallConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
	})
	return c.Conn.Close()
}

func (c *sseDeadlineStallConn) Write(p []byte) (int, error) {
	if !c.stallWrites.Load() {
		return c.Conn.Write(p)
	}
	c.mu.Lock()
	dl := c.deadline
	c.mu.Unlock()
	if dl.IsZero() {
		<-c.closed
		return 0, net.ErrClosed
	}
	wait := time.Until(dl)
	if wait <= 0 {
		return 0, os.ErrDeadlineExceeded
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return 0, os.ErrDeadlineExceeded
	case <-c.closed:
		return 0, net.ErrClosed
	}
}

type sseStallingListener struct {
	net.Listener
	onConn func(*sseDeadlineStallConn)
}

func (l *sseStallingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &sseDeadlineStallConn{
		Conn:   conn,
		closed: make(chan struct{}),
	}
	if l.onConn != nil {
		l.onConn(wrapped)
	}
	return wrapped, nil
}

func TestAgentSessionEventStreamWriteDeadlineCancelsUpstream(t *testing.T) {
	snapshot := "event: session.snapshot\ndata: " + string(agentSessionEventFixture("session.snapshot", map[string]any{"snapshot": agentSessionSnapshotFixture()}, nil)) + "\n\n"
	upstreamCanceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, snapshot)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
		close(upstreamCanceled)
	}))
	defer upstream.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Pointer[sseDeadlineStallConn]
	wrappedLn := &sseStallingListener{
		Listener: ln,
		onConn: func(c *sseDeadlineStallConn) {
			accepted.Store(c)
		},
	}

	streamErrCh := make(chan error, 1)
	gatewaySrv := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			ctx, cancel := context.WithCancel(request.Context())
			defer cancel()
			upReq, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL, nil)
			if reqErr != nil {
				streamErrCh <- reqErr
				return
			}
			upResp, doErr := upstream.Client().Do(upReq)
			if doErr != nil {
				streamErrCh <- doErr
				return
			}
			defer upResp.Body.Close()
			_, fwdErr := forwardAgentSessionEventStreamWithConfig(
				ctx,
				cancel,
				&statusRecorder{ResponseWriter: writer, status: http.StatusOK},
				upResp.Body,
				45*time.Millisecond,
				25*time.Millisecond,
			)
			streamErrCh <- fwdErr
		}),
	}
	go func() { _ = gatewaySrv.Serve(wrappedLn) }()
	defer func() { _ = gatewaySrv.Close() }()

	clientConn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clientConn.Close() }()
	if _, err := io.WriteString(clientConn, "GET /events HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 4096)
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := clientConn.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "200 OK") {
		t.Fatalf("expected initial SSE response headers/snapshot, got n=%d err=%v body=%q", n, err, string(buf[:n]))
	}
	if srvConn := accepted.Load(); srvConn != nil {
		srvConn.stallWrites.Store(true)
	}

	select {
	case fwdErr := <-streamErrCh:
		if !errors.Is(fwdErr, os.ErrDeadlineExceeded) {
			t.Fatalf("expected os.ErrDeadlineExceeded on stalled SSE write, got %v", fwdErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled SSE stream did not fail within write deadline")
	}

	select {
	case <-upstreamCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream SSE request context was not canceled after client write timeout")
	}
}

func TestValidateAgentSessionEventAcceptsFindingKeyFiguresAndRejectsExtraFields(t *testing.T) {
	finding := func(figure map[string]any) []byte {
		return agentSessionEventFixture("artifact.created", map[string]any{"artifact": map[string]any{
			"id": "artifact-1", "sessionId": "session-1", "runId": "run-1", "kind": "FINDING", "createdAt": 1000,
			"finding": map[string]any{
				"outcome": "UNABLE_TO_CONCLUDE", "evidenceRefs": []any{},
				"keyFigures": []any{figure}, "limitations": []any{}, "recommendedNext": []any{},
			},
		}}, "run-1")
	}
	if _, err := validateAgentSessionEvent("artifact.created", finding(map[string]any{"label": "多用电", "value": "+762", "unit": "kWh"})); err != nil {
		t.Fatalf("finding with key figures rejected: %v", err)
	}
	if _, err := validateAgentSessionEvent("artifact.created", finding(map[string]any{"label": "多用电", "value": "+762", "pointId": "x"})); err == nil {
		t.Fatal("key figure with a non-public field was accepted")
	}
}

// The contract bounds strings in characters, as the Agent and the Web count them: a 14-character
// Chinese key figure label is 33 UTF-8 bytes and must not make the whole Session unreadable.
func TestValidateAgentSessionSnapshotBoundsChineseTextInCharacters(t *testing.T) {
	snapshot := func(label string) []byte {
		raw, err := json.Marshal(map[string]any{
			"session": map[string]any{
				"id": "session-1", "tenantId": "tenant-1", "siteId": "site-1", "agentDefinitionId": "operations-investigation.v1",
				"createdBy": "principal-1", "revision": 1, "createdAt": 1000, "updatedAt": 2000, "status": "COMPLETED", "activeRunId": nil,
			},
			"runs": []any{}, "messages": []any{}, "toolExecutions": []any{},
			"artifacts": []any{map[string]any{
				"id": "artifact-1", "sessionId": "session-1", "runId": "run-1", "kind": "FINDING", "createdAt": 2000,
				"finding": map[string]any{
					"outcome": "SUPPORTED_FINDING", "evidenceRefs": []any{},
					"keyFigures": []any{map[string]any{"unit": "kWh", "label": label, "value": "1768"}}, "limitations": []any{}, "recommendedNext": []any{},
				},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if err := validateAgentSessionSnapshot(snapshot("10/13 用电量（七日最高）")); err != nil {
		t.Fatalf("a 14-character label was rejected: %v", err)
	}
	if err := validateAgentSessionSnapshot(snapshot(strings.Repeat("用", 33))); err == nil {
		t.Fatal("a 33-character label was accepted")
	}
}

// #640: a finding may carry a chart drawn from one read; the gateway passes it through only in
// the public shape, one value per category.
func TestValidateAgentSessionEventAcceptsAFindingChartInThePublicShape(t *testing.T) {
	finding := func(chart map[string]any) []byte {
		return agentSessionEventFixture("artifact.created", map[string]any{"artifact": map[string]any{
			"id": "artifact-1", "sessionId": "session-1", "runId": "run-1", "kind": "FINDING", "createdAt": 1000,
			"finding": map[string]any{
				"outcome": "SUPPORTED_FINDING", "evidenceRefs": []any{}, "limitations": []any{}, "recommendedNext": []any{},
				"chart": chart,
			},
		}}, "run-1")
	}
	chart := func(change func(map[string]any)) map[string]any {
		value := map[string]any{
			"kind": "LINE", "title": "10月15日每小时用电", "unit": "kWh", "categories": []any{"08:00", "09:00"},
			"series": []any{map[string]any{"name": "用电", "values": []any{120.0, nil}}},
		}
		change(value)
		return value
	}
	if _, err := validateAgentSessionEvent("artifact.created", finding(chart(func(map[string]any) {}))); err != nil {
		t.Fatalf("a finding chart was rejected: %v", err)
	}
	for name, change := range map[string]func(map[string]any){
		"unknown kind": func(value map[string]any) { value["kind"] = "PIE" },
		"extra field":  func(value map[string]any) { value["pointId"] = "x" },
		"value count": func(value map[string]any) {
			value["series"] = []any{map[string]any{"name": "用电", "values": []any{120.0}}}
		},
		"text value": func(value map[string]any) {
			value["series"] = []any{map[string]any{"name": "用电", "values": []any{"120", 1.0}}}
		},
		"no categories": func(value map[string]any) { value["categories"] = []any{} },
		"five series": func(value map[string]any) {
			one := map[string]any{"name": "用电", "values": []any{1.0, 2.0}}
			value["series"] = []any{one, one, one, one, one}
		},
	} {
		if _, err := validateAgentSessionEvent("artifact.created", finding(chart(change))); err == nil {
			t.Fatalf("a chart with %s was accepted", name)
		}
	}
}
