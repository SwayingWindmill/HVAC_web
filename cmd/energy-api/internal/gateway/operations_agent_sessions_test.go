package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
