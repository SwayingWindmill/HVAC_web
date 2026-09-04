package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	PublicAgentSessionsPathTemplate           = "/api/v1/sites/{siteId}/operations/agent-sessions"
	PublicAgentSessionPathTemplate            = "/api/v1/sites/{siteId}/operations/agent-sessions/{sessionId}"
	PublicAgentSessionEventsPathTemplate      = "/api/v1/sites/{siteId}/operations/agent-sessions/{sessionId}/events"
	PublicAgentSessionStartRunPathTemplate    = "/api/v1/sites/{siteId}/operations/agent-sessions/{sessionId}:run"
	PublicAgentSessionCancelPathTemplate      = "/api/v1/sites/{siteId}/operations/agent-sessions/{sessionId}:cancel"
	PublicAgentSessionSubmitInputPathTemplate = "/api/v1/sites/{siteId}/operations/agent-sessions/{sessionId}:submit-input"
)

type publicAgentSessionRoute struct {
	kind         string
	template     string
	siteID       string
	sessionID    string
	internalPath string
	method       string
	mutation     bool
}

func matchPublicAgentSessionRoute(path string) (publicAgentSessionRoute, bool) {
	const prefix = "/api/v1/sites/"
	if !strings.HasPrefix(path, prefix) {
		return publicAgentSessionRoute{}, false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 3 || parts[0] == "" || parts[1] != "operations" || parts[2] != "agent-sessions" {
		return publicAgentSessionRoute{}, false
	}
	siteID, err := url.PathUnescape(parts[0])
	if err != nil || !isLowerUUIDv7(siteID) {
		return publicAgentSessionRoute{siteID: ""}, true
	}
	internalBase := "/internal/v1/sites/" + url.PathEscape(siteID) + "/operations/agent-sessions"
	if len(parts) == 3 {
		return publicAgentSessionRoute{
			kind: "COLLECTION", template: PublicAgentSessionsPathTemplate,
			siteID: siteID, internalPath: internalBase,
		}, true
	}
	if len(parts) == 5 && parts[4] == "events" {
		sessionID, err := url.PathUnescape(parts[3])
		if err != nil || strings.TrimSpace(sessionID) == "" || len(sessionID) > 256 {
			return publicAgentSessionRoute{siteID: siteID}, true
		}
		return publicAgentSessionRoute{
			kind: "STREAM", template: PublicAgentSessionEventsPathTemplate,
			siteID: siteID, sessionID: sessionID,
			internalPath: internalBase + "/" + url.PathEscape(sessionID) + "/events",
			method: http.MethodGet,
		}, true
	}
	if len(parts) != 4 || parts[3] == "" {
		return publicAgentSessionRoute{}, false
	}
	segment := parts[3]
	kind, suffix, method, mutation := "GET", "", http.MethodGet, false
	template := PublicAgentSessionPathTemplate
	switch {
	case strings.HasSuffix(segment, ":run"):
		kind, suffix, method, mutation = "START", ":run", http.MethodPost, true
		template = PublicAgentSessionStartRunPathTemplate
	case strings.HasSuffix(segment, ":cancel"):
		kind, suffix, method, mutation = "CANCEL", ":cancel", http.MethodPost, true
		template = PublicAgentSessionCancelPathTemplate
	case strings.HasSuffix(segment, ":submit-input"):
		kind, suffix, method, mutation = "SUBMIT_INPUT", ":submit-input", http.MethodPost, true
		template = PublicAgentSessionSubmitInputPathTemplate
	}
	segment = strings.TrimSuffix(segment, suffix)
	sessionID, err := url.PathUnescape(segment)
	if err != nil || strings.TrimSpace(sessionID) == "" || len(sessionID) > 256 {
		return publicAgentSessionRoute{siteID: siteID}, true
	}
	return publicAgentSessionRoute{
		kind: kind, template: template, siteID: siteID, sessionID: sessionID,
		internalPath: internalBase + "/" + url.PathEscape(sessionID) + suffix,
		method: method, mutation: mutation,
	}, true
}

func dispatchAgentSessionRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAgentSessionRoute) {
	if route.siteID == "" {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Agent Session was not found.", false, nil)
		return
	}
	if route.kind == "COLLECTION" {
		switch request.Method {
		case http.MethodGet:
			route.kind, route.method = "LIST", http.MethodGet
		case http.MethodPost:
			route.kind, route.method, route.mutation = "CREATE", http.MethodPost, true
		default:
			writer.Header().Set("Allow", "GET, POST")
			writeProblem(writer, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "This Agent Session collection accepts GET or POST.", false, nil)
			return
		}
	}
	if route.kind != "CREATE" && route.kind != "LIST" && route.sessionID == "" {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Agent Session was not found.", false, nil)
		return
	}
	if request.Method != route.method {
		writeMethodNotAllowedFor(writer, request, route.method)
		return
	}
	h.proxyAgentSession(writer, request, route)
}

func validateAgentSessionMutationBody(raw []byte, kind string) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid Agent Session mutation body")
	}
	switch kind {
	case "CREATE":
		if !operationsExactKeys(value, "message") {
			return errors.New("invalid Agent Session create body")
		}
		message, ok := operationsBoundedString(value["message"], 4000)
		if !ok || strings.TrimSpace(message) == "" {
			return errors.New("invalid Agent Session message")
		}
	case "START":
		if !operationsExactKeys(value, "expectedRevision", "message") {
			return errors.New("invalid Agent Session start body")
		}
		if _, ok := operationsNonnegativeInteger(value["expectedRevision"]); !ok {
			return errors.New("invalid Agent Session revision")
		}
		if _, ok := operationsBoundedString(value["message"], 4000); !ok {
			return errors.New("invalid Agent Session message")
		}
	case "CANCEL":
		if !operationsExactKeys(value, "expectedRevision") {
			return errors.New("invalid Agent Session cancel body")
		}
		if _, ok := operationsNonnegativeInteger(value["expectedRevision"]); !ok {
			return errors.New("invalid Agent Session revision")
		}
	case "SUBMIT_INPUT":
		if !operationsExactKeys(value, "expectedRevision", "requestArtifactId", "value") {
			return errors.New("invalid Agent Session input body")
		}
		if _, ok := operationsNonnegativeInteger(value["expectedRevision"]); !ok {
			return errors.New("invalid Agent Session revision")
		}
		if _, ok := operationsBoundedString(value["requestArtifactId"], 256); !ok {
			return errors.New("invalid Agent Session input request identity")
		}
		if _, ok := operationsBoundedString(value["value"], 4000); !ok {
			return errors.New("invalid Agent Session input value")
		}
	default:
		return errors.New("unsupported Agent Session mutation")
	}
	return nil
}

func validateAgentSessionEvidenceRef(value any) error {
	ref, ok := value.(map[string]any)
	if !ok {
		return errors.New("invalid Agent evidence reference")
	}
	if len(ref) != 4 && len(ref) != 5 {
		return errors.New("invalid Agent evidence reference shape")
	}
	if !operationsAllowedString(ref["owner"], "REGISTRY", "TELEMETRY", "ENERGY", "ALARM", "FDD", "WORK_ORDER", "COMMAND") {
		return errors.New("invalid Agent evidence owner")
	}
	for _, key := range []string{"resourceType", "resourceId", "toolExecutionId"} {
		if _, ok := operationsBoundedString(ref[key], 256); !ok {
			return errors.New("invalid Agent evidence identity")
		}
	}
	if revision, exists := ref["revision"]; exists {
		if _, ok := operationsBoundedString(revision, 256); !ok {
			return errors.New("invalid Agent evidence revision")
		}
	}
	for key := range ref {
		if key != "owner" && key != "resourceType" && key != "resourceId" && key != "revision" && key != "toolExecutionId" {
			return errors.New("Agent evidence reference exposes unknown field")
		}
	}
	return nil
}

func validateAgentRunValue(value any) error {
	run, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(run, "id", "sessionId", "modelRef", "status", "startedAt", "finishedAt", "usage", "failureCode") {
		return errors.New("invalid Agent Run shape")
	}
	if _, ok := operationsBoundedString(run["id"], 256); !ok {
		return errors.New("invalid Agent Run id")
	}
	if _, ok := operationsBoundedString(run["sessionId"], 256); !ok {
		return errors.New("invalid Agent Run Session id")
	}
	modelRef, ok := run["modelRef"].(map[string]any)
	if !ok || !operationsExactKeys(modelRef, "provider", "model") {
		return errors.New("invalid Agent model reference")
	}
	if _, ok := operationsBoundedString(modelRef["provider"], 256); !ok {
		return errors.New("invalid Agent provider identity")
	}
	if _, ok := operationsBoundedString(modelRef["model"], 256); !ok {
		return errors.New("invalid Agent model identity")
	}
	if !operationsAllowedString(run["status"], "RUNNING", "COMPLETED", "FAILED", "CANCELLED") {
		return errors.New("invalid Agent Run status")
	}
	if _, ok := operationsNonnegativeInteger(run["startedAt"]); !ok {
		return errors.New("invalid Agent Run start")
	}
	if run["finishedAt"] != nil {
		if _, ok := operationsNonnegativeInteger(run["finishedAt"]); !ok {
			return errors.New("invalid Agent Run finish")
		}
	}
	usage, ok := run["usage"].(map[string]any)
	if !ok || !operationsExactKeys(usage, "inputTokens", "outputTokens", "modelCalls", "toolCalls") {
		return errors.New("invalid Agent Run usage")
	}
	for _, key := range []string{"inputTokens", "outputTokens", "modelCalls", "toolCalls"} {
		if _, ok := operationsNonnegativeInteger(usage[key]); !ok {
			return errors.New("invalid Agent Run usage value")
		}
	}
	if run["failureCode"] != nil {
		if _, ok := operationsBoundedString(run["failureCode"], 256); !ok {
			return errors.New("invalid Agent Run failure code")
		}
	}
	return nil
}

func validateAgentMessageValue(value any) error {
	message, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(message, "id", "sessionId", "runId", "role", "content", "createdAt") {
		return errors.New("invalid Agent Message shape")
	}
	for _, key := range []string{"id", "sessionId"} {
		if _, ok := operationsBoundedString(message[key], 256); !ok {
			return errors.New("invalid Agent Message identity")
		}
	}
	if message["runId"] != nil {
		if _, ok := operationsBoundedString(message["runId"], 256); !ok {
			return errors.New("invalid Agent Message Run")
		}
	}
	if !operationsAllowedString(message["role"], "OPERATOR", "ASSISTANT") {
		return errors.New("invalid Agent Message role")
	}
	if _, ok := operationsBoundedString(message["content"], 4000); !ok {
		return errors.New("invalid Agent Message content")
	}
	if _, ok := operationsNonnegativeInteger(message["createdAt"]); !ok {
		return errors.New("invalid Agent Message time")
	}
	return nil
}

func validateAgentToolExecutionValue(value any) error {
	execution, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(execution, "id", "sessionId", "runId", "toolName", "argumentsDigest", "status", "startedAt", "finishedAt", "resultSummary", "provenance", "failureCode") {
		return errors.New("invalid Agent Tool execution shape")
	}
	for _, key := range []string{"id", "sessionId", "runId", "toolName", "argumentsDigest"} {
		if _, ok := operationsBoundedString(execution[key], 512); !ok {
			return errors.New("invalid Agent Tool execution identity")
		}
	}
	if !operationsAllowedString(execution["status"], "RUNNING", "COMPLETED", "FAILED", "CANCELLED") {
		return errors.New("invalid Agent Tool execution status")
	}
	if _, ok := operationsNonnegativeInteger(execution["startedAt"]); !ok {
		return errors.New("invalid Agent Tool execution start")
	}
	if execution["finishedAt"] != nil {
		if _, ok := operationsNonnegativeInteger(execution["finishedAt"]); !ok {
			return errors.New("invalid Agent Tool execution finish")
		}
	}
	if execution["resultSummary"] != nil {
		if _, ok := operationsBoundedString(execution["resultSummary"], 4000); !ok {
			return errors.New("invalid Agent Tool result summary")
		}
	}
	if execution["failureCode"] != nil {
		if _, ok := operationsBoundedString(execution["failureCode"], 256); !ok {
			return errors.New("invalid Agent Tool failure code")
		}
	}
	provenance, ok := execution["provenance"].([]any)
	if !ok || len(provenance) > 32 {
		return errors.New("invalid Agent Tool provenance")
	}
	for _, ref := range provenance {
		if err := validateAgentSessionEvidenceRef(ref); err != nil {
			return err
		}
	}
	return nil
}

func validateAgentArtifactValue(value any) error {
	artifact, ok := value.(map[string]any)
	if !ok {
		return errors.New("invalid Agent Artifact")
	}
	for _, key := range []string{"id", "sessionId", "runId"} {
		if _, ok := operationsBoundedString(artifact[key], 256); !ok {
			return errors.New("invalid Agent Artifact identity")
		}
	}
	if _, ok := operationsNonnegativeInteger(artifact["createdAt"]); !ok {
		return errors.New("invalid Agent Artifact time")
	}
	kind, ok := artifact["kind"].(string)
	if !ok {
		return errors.New("invalid Agent Artifact kind")
	}
	switch kind {
	case "EVIDENCE_REF":
		if !operationsExactKeys(artifact, "id", "sessionId", "runId", "kind", "reference", "createdAt") {
			return errors.New("invalid evidence Artifact shape")
		}
		return validateAgentSessionEvidenceRef(artifact["reference"])
	case "FINDING":
		if !operationsExactKeys(artifact, "id", "sessionId", "runId", "kind", "finding", "createdAt") {
			return errors.New("invalid finding Artifact shape")
		}
		finding, ok := artifact["finding"].(map[string]any)
		if !ok || !operationsExactKeys(finding, "outcome", "summary", "evidenceRefs", "limitations", "recommendedNext") ||
			!operationsAllowedString(finding["outcome"], "SUPPORTED_FINDING", "UNABLE_TO_CONCLUDE") {
			return errors.New("invalid finding Artifact")
		}
		if _, ok := operationsBoundedString(finding["summary"], 2000); !ok {
			return errors.New("invalid finding summary")
		}
		refs, ok := finding["evidenceRefs"].([]any)
		if !ok || len(refs) > 32 {
			return errors.New("invalid finding evidence")
		}
		for _, ref := range refs {
			if err := validateAgentSessionEvidenceRef(ref); err != nil {
				return err
			}
		}
		for _, key := range []string{"limitations", "recommendedNext"} {
			items, ok := finding[key].([]any)
			if !ok || len(items) > 16 {
				return errors.New("invalid finding list")
			}
			for _, item := range items {
				if _, ok := operationsBoundedString(item, 512); !ok {
					return errors.New("invalid finding list item")
				}
			}
		}
	case "PROPOSAL":
		if !operationsExactKeys(artifact, "id", "sessionId", "runId", "kind", "proposalType", "summary", "createdAt") {
			return errors.New("invalid proposal Artifact shape")
		}
		if _, ok := operationsBoundedString(artifact["proposalType"], 256); !ok {
			return errors.New("invalid proposal type")
		}
		if _, ok := operationsBoundedString(artifact["summary"], 4000); !ok {
			return errors.New("invalid proposal summary")
		}
	case "INPUT_REQUEST":
		if !operationsExactKeys(artifact, "id", "sessionId", "runId", "kind", "request", "createdAt") {
			return errors.New("invalid input request Artifact shape")
		}
		request, ok := artifact["request"].(map[string]any)
		if !ok || !operationsExactKeys(request, "prompt", "response") {
			return errors.New("invalid Agent input request")
		}
		if _, ok := operationsBoundedString(request["prompt"], 4000); !ok {
			return errors.New("invalid Agent input prompt")
		}
		response, ok := request["response"].(map[string]any)
		if !ok {
			return errors.New("invalid Agent input response schema")
		}
		if response["kind"] == "TEXT" {
			if !operationsExactKeys(response, "kind", "maxLength") {
				return errors.New("invalid Agent text input schema")
			}
			length, ok := operationsNonnegativeInteger(response["maxLength"])
			if !ok || length == 0 || length > 4000 {
				return errors.New("invalid Agent text input length")
			}
		} else if response["kind"] == "SINGLE_SELECT" {
			if !operationsExactKeys(response, "kind", "choices") {
				return errors.New("invalid Agent select input schema")
			}
			choices, ok := response["choices"].([]any)
			if !ok || len(choices) == 0 || len(choices) > 32 {
				return errors.New("invalid Agent input choices")
			}
			for _, candidate := range choices {
				choice, ok := candidate.(map[string]any)
				if !ok || !operationsExactKeys(choice, "value", "label") {
					return errors.New("invalid Agent input choice")
				}
				if _, ok := operationsBoundedString(choice["value"], 256); !ok {
					return errors.New("invalid Agent input choice value")
				}
				if _, ok := operationsBoundedString(choice["label"], 256); !ok {
					return errors.New("invalid Agent input choice label")
				}
			}
		} else {
			return errors.New("unsupported Agent input schema")
		}
	case "INPUT_RESPONSE":
		if !operationsExactKeys(artifact, "id", "sessionId", "runId", "kind", "requestArtifactId", "value", "submittedBy", "createdAt") {
			return errors.New("invalid input response Artifact shape")
		}
		for _, key := range []string{"requestArtifactId", "submittedBy"} {
			if _, ok := operationsBoundedString(artifact[key], 256); !ok {
				return errors.New("invalid input response identity")
			}
		}
		if _, ok := operationsBoundedString(artifact["value"], 4000); !ok {
			return errors.New("invalid input response value")
		}
	case "LIMITATION":
		if !operationsExactKeys(artifact, "id", "sessionId", "runId", "kind", "description", "createdAt") {
			return errors.New("invalid limitation Artifact shape")
		}
		if _, ok := operationsBoundedString(artifact["description"], 4000); !ok {
			return errors.New("invalid limitation description")
		}
	default:
		return errors.New("unsupported Agent Artifact kind")
	}
	return nil
}

func validateAgentSessionStateValue(value any) error {
	state, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(state, "session", "runs", "messages", "toolExecutions", "artifacts") {
		return errors.New("invalid Agent Session snapshot shape")
	}
	session, ok := state["session"].(map[string]any)
	if !ok || !operationsExactKeys(session, "id", "tenantId", "siteId", "agentDefinitionId", "createdBy", "revision", "createdAt", "updatedAt", "status", "activeRunId") {
		return errors.New("invalid Agent Session shape")
	}
	for _, key := range []string{"id", "tenantId", "siteId", "agentDefinitionId", "createdBy"} {
		if _, ok := operationsBoundedString(session[key], 256); !ok {
			return errors.New("invalid Agent Session identity")
		}
	}
	if _, ok := operationsNonnegativeInteger(session["revision"]); !ok {
		return errors.New("invalid Agent Session revision")
	}
	if _, ok := operationsNonnegativeInteger(session["createdAt"]); !ok {
		return errors.New("invalid Agent Session createdAt")
	}
	if _, ok := operationsNonnegativeInteger(session["updatedAt"]); !ok {
		return errors.New("invalid Agent Session updatedAt")
	}
	if !operationsAllowedString(session["status"], "ACTIVE", "WAITING_FOR_INPUT", "COMPLETED", "FAILED", "CANCELLED") {
		return errors.New("invalid Agent Session status")
	}
	if session["status"] == "ACTIVE" {
		if _, ok := operationsBoundedString(session["activeRunId"], 256); !ok {
			return errors.New("active Agent Session requires Run identity")
		}
	} else if session["activeRunId"] != nil {
		return errors.New("inactive Agent Session must not expose active Run")
	}

	collections := []struct {
		key      string
		maximum  int
		validate func(any) error
	}{
		{key: "runs", maximum: 64, validate: validateAgentRunValue},
		{key: "messages", maximum: 256, validate: validateAgentMessageValue},
		{key: "toolExecutions", maximum: 256, validate: validateAgentToolExecutionValue},
		{key: "artifacts", maximum: 256, validate: validateAgentArtifactValue},
	}
	for _, collection := range collections {
		items, ok := state[collection.key].([]any)
		if !ok || len(items) > collection.maximum {
			return errors.New("invalid Agent Session collection")
		}
		for _, item := range items {
			if err := collection.validate(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeAgentSessionJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("invalid Agent Session JSON")
	}
	return value, nil
}

func validateAgentSessionSnapshot(raw []byte) error {
	value, err := decodeAgentSessionJSON(raw)
	if err != nil {
		return err
	}
	return validateAgentSessionStateValue(value)
}

func validateAgentSessionList(raw []byte) error {
	value, err := decodeAgentSessionJSON(raw)
	if err != nil {
		return err
	}
	items, ok := value.([]any)
	if !ok || len(items) > 50 {
		return errors.New("invalid Agent Session list")
	}
	for _, item := range items {
		if err := validateAgentSessionStateValue(item); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeAgentSessionProblem(raw []byte, expectedStatus int) ([]byte, error) {
	value, err := decodeAgentSessionJSON(raw)
	if err != nil {
		return nil, err
	}
	problem, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(problem, "type", "title", "status", "code", "detail") {
		return nil, errors.New("invalid Agent Session problem shape")
	}
	if _, ok := operationsBoundedString(problem["type"], 512); !ok {
		return nil, errors.New("invalid Agent Session problem type")
	}
	if _, ok := operationsBoundedString(problem["title"], 256); !ok {
		return nil, errors.New("invalid Agent Session problem title")
	}
	status, ok := operationsNonnegativeInteger(problem["status"])
	if !ok || status != int64(expectedStatus) {
		return nil, errors.New("invalid Agent Session problem status")
	}
	if _, ok := operationsBoundedString(problem["code"], 256); !ok {
		return nil, errors.New("invalid Agent Session problem code")
	}
	if _, ok := operationsBoundedString(problem["detail"], 4000); !ok {
		return nil, errors.New("invalid Agent Session problem detail")
	}
	return json.Marshal(problem)
}

func validateAgentSessionEvent(name string, raw []byte) ([]byte, error) {
	value, err := decodeAgentSessionJSON(raw)
	if err != nil {
		return nil, err
	}
	event, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(event, "version", "type", "sessionId", "runId", "sequence", "at", "payload") {
		return nil, errors.New("invalid Agent event envelope")
	}
	if event["version"] != "hvac.agent.event/v1" || event["type"] != name {
		return nil, errors.New("invalid Agent event version or type")
	}
	if _, ok := operationsBoundedString(event["sessionId"], 256); !ok {
		return nil, errors.New("invalid Agent event Session")
	}
	if _, ok := operationsNonnegativeInteger(event["sequence"]); !ok {
		return nil, errors.New("invalid Agent event sequence")
	}
	if _, ok := operationsNonnegativeInteger(event["at"]); !ok {
		return nil, errors.New("invalid Agent event time")
	}
	payload, ok := event["payload"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid Agent event payload")
	}
	requireRunID := func() error {
		if _, ok := operationsBoundedString(event["runId"], 256); !ok {
			return errors.New("Agent Run event requires Run identity")
		}
		return nil
	}
	switch name {
	case "session.snapshot":
		if !operationsExactKeys(payload, "snapshot") {
			return nil, errors.New("invalid Agent snapshot event")
		}
		if err := validateAgentSessionStateValue(payload["snapshot"]); err != nil {
			return nil, err
		}
	case "run.started", "run.completed", "run.failed":
		if err := requireRunID(); err != nil || !operationsExactKeys(payload, "run") {
			return nil, errors.New("invalid Agent Run event")
		}
		if err := validateAgentRunValue(payload["run"]); err != nil {
			return nil, err
		}
	case "assistant.delta":
		if err := requireRunID(); err != nil || !operationsExactKeys(payload, "messageId", "delta") {
			return nil, errors.New("invalid Agent assistant event")
		}
		if _, ok := operationsBoundedString(payload["messageId"], 256); !ok {
			return nil, errors.New("invalid Agent assistant Message")
		}
		delta, ok := payload["delta"].(string)
		if !ok || delta == "" || len(delta) > 16_384 {
			return nil, errors.New("invalid Agent assistant delta")
		}
	case "tool.started":
		if err := requireRunID(); err != nil || !operationsExactKeys(payload, "toolExecutionId", "toolName") {
			return nil, errors.New("invalid Agent Tool start event")
		}
		if _, ok := operationsBoundedString(payload["toolExecutionId"], 256); !ok {
			return nil, errors.New("invalid Agent Tool execution id")
		}
		if _, ok := operationsBoundedString(payload["toolName"], 256); !ok {
			return nil, errors.New("invalid Agent Tool name")
		}
	case "tool.completed":
		if err := requireRunID(); err != nil || !operationsExactKeys(payload, "toolExecution") {
			return nil, errors.New("invalid Agent Tool completion event")
		}
		if err := validateAgentToolExecutionValue(payload["toolExecution"]); err != nil {
			return nil, err
		}
	case "artifact.created":
		if err := requireRunID(); err != nil || !operationsExactKeys(payload, "artifact") {
			return nil, errors.New("invalid Agent Artifact event")
		}
		if err := validateAgentArtifactValue(payload["artifact"]); err != nil {
			return nil, err
		}
	case "input.required":
		if err := requireRunID(); err != nil || !operationsExactKeys(payload, "artifact") {
			return nil, errors.New("invalid Agent input event")
		}
		artifact, ok := payload["artifact"].(map[string]any)
		if !ok || artifact["kind"] != "INPUT_REQUEST" || validateAgentArtifactValue(artifact) != nil {
			return nil, errors.New("invalid Agent input request Artifact")
		}
	default:
		return nil, errors.New("unsupported Agent event type")
	}
	return json.Marshal(event)
}

type agentSessionStreamForwardResult struct {
	started bool
}

func forwardAgentSessionEventStream(writer http.ResponseWriter, body io.Reader) (agentSessionStreamForwardResult, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	var eventName string
	var data []byte
	started := false
	flush, _ := writer.(http.Flusher)
	writeBlock := func() error {
		if eventName == "" || len(data) == 0 {
			return errors.New("incomplete Agent event stream block")
		}
		sanitized, err := validateAgentSessionEvent(eventName, data)
		if err != nil {
			return err
		}
		if !started {
			if eventName != "session.snapshot" {
				return errors.New("Agent event stream must start from durable Session snapshot")
			}
			writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			writer.Header().Set("Cache-Control", "no-store, no-transform")
			writer.Header().Set("X-Accel-Buffering", "no")
			writer.WriteHeader(http.StatusOK)
			started = true
		}
		if _, err := writer.Write([]byte("event: " + eventName + "\n")); err != nil {
			return err
		}
		if _, err := writer.Write(append(append([]byte("data: "), sanitized...), '\n', '\n')); err != nil {
			return err
		}
		if flush != nil {
			flush.Flush()
		}
		eventName, data = "", nil
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := writeBlock(); err != nil {
				return agentSessionStreamForwardResult{started: started}, err
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event: ") && eventName == "":
			eventName = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: ") && len(data) == 0:
			data = []byte(strings.TrimPrefix(line, "data: "))
		default:
			return agentSessionStreamForwardResult{started: started}, errors.New("unsupported Agent event stream field")
		}
	}
	if err := scanner.Err(); err != nil {
		return agentSessionStreamForwardResult{started: started}, err
	}
	if eventName != "" || len(data) != 0 {
		return agentSessionStreamForwardResult{started: started}, errors.New("incomplete Agent event stream")
	}
	if !started {
		return agentSessionStreamForwardResult{}, errors.New("empty Agent event stream")
	}
	return agentSessionStreamForwardResult{started: true}, nil
}

func (h *handler) proxyAgentSession(writer http.ResponseWriter, request *http.Request, route publicAgentSessionRoute) {
	if h.operations == nil || h.operations.baseURL == "" || h.operations.httpClient == nil || h.identity == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_AGENT_UNAVAILABLE", "Operations Agent unavailable", "The Operations Agent is not configured.", true, nil)
		return
	}
	if request.URL.RawQuery != "" {
		writeProblem(writer, request, http.StatusBadRequest, "QUERY_UNSUPPORTED", "Query unsupported", "Agent Session routes do not accept query parameters.", false, nil)
		return
	}
	if !route.mutation && request.ContentLength != 0 {
		writeProblem(writer, request, http.StatusBadRequest, "REQUEST_INVALID", "Request invalid", "GET requests must not contain a body.", false, nil)
		return
	}
	session, ok := h.commandSession(writer, request, route.mutation)
	if !ok {
		return
	}
	var body []byte
	if route.mutation {
		if !hasOperationsJSONContentType(request) {
			writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTENT_TYPE_UNSUPPORTED", "Content type unsupported", "Agent Session mutations require application/json.", false, nil)
			return
		}
		var err error
		body, err = readBoundedBody(request.Body, h.operations.maxRequestBytes)
		if err != nil || validateAgentSessionMutationBody(body, route.kind) != nil {
			writeProblem(writer, request, http.StatusBadRequest, "REQUEST_INVALID", "Request invalid", "The Agent Session request body is invalid.", false, nil)
			return
		}
	}
	if h.operations.rateLimiter == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_LIMIT_UNAVAILABLE", "Operations limit unavailable", "The Agent Session limit policy could not be evaluated.", true, nil)
		return
	}
	decision := h.operations.rateLimiter.Allow(request.Context(), limitpolicy.DimensionOperationsAgent, session.ID)
	if !decision.Allowed {
		if decision.Reason == "counter-unavailable" {
			writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_LIMIT_UNAVAILABLE", "Operations limit unavailable", "The Agent Session limit policy could not be evaluated.", true, nil)
			return
		}
		writeProblem(writer, request, http.StatusTooManyRequests, "OPERATIONS_RATE_LIMITED", "Operations rate limited", "The Agent Session request rate has been exceeded.", true, nil)
		return
	}
	siteAuthorization, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if failure != nil || !registryAuthorizationAllowsSite(siteAuthorization, route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	serviceDelegation, err := h.operationsServiceDelegation(session, route.siteID)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_AGENT_UNAVAILABLE", "Operations Agent unavailable", "The Operations Agent delegation could not be created.", true, nil)
		return
	}

	ctx := request.Context()
	var cancel context.CancelFunc = func() {}
	if route.kind != "STREAM" {
		ctx, cancel = context.WithTimeout(request.Context(), h.operations.timeout)
	}
	defer cancel()
	upstream, err := http.NewRequestWithContext(ctx, route.method, h.operations.baseURL+route.internalPath, bytes.NewReader(body))
	if err != nil {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent gateway failed", "The Agent Session request could not be created.", true, nil)
		return
	}
	if route.mutation {
		upstream.Header.Set("Content-Type", "application/json")
	}
	if route.kind == "STREAM" {
		upstream.Header.Set("Accept", "text/event-stream, application/problem+json")
	} else {
		upstream.Header.Set("Accept", "application/json, application/problem+json")
	}
	upstream.Header.Set("X-Tenant-ID", session.TenantID)
	upstream.Header.Set("X-Delegation-Grant", serviceDelegation)
	upstream.Header.Set("X-Route-Policy-Revision", formatRevision(routeDecisionFromContext(request.Context()).RegistryRevision))
	upstream.Header.Set("X-Request-ID", requestIDFromContext(request.Context()))
	injectOperationsTrace(ctx, upstream.Header)
	response, err := h.operations.httpClient.Do(upstream)
	if err != nil {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent unavailable", "The Operations Agent did not complete the Agent Session request.", true, nil)
		return
	}
	defer response.Body.Close()
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if route.kind == "STREAM" && response.StatusCode >= 200 && response.StatusCode < 300 {
		if !strings.HasPrefix(contentType, "text/event-stream") {
			writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_CONTRACT_FAILED", "Operations Agent contract failed", "The Operations Agent returned an invalid Agent event stream.", true, nil)
			return
		}
		result, err := forwardAgentSessionEventStream(writer, response.Body)
		if err != nil && !result.started {
			writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_CONTRACT_FAILED", "Operations Agent contract failed", "The Operations Agent returned an invalid Agent event stream.", true, nil)
		}
		return
	}
	raw, err := readBoundedBody(response.Body, h.operations.maxResponseBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent gateway failed", "The Agent Session response was unreadable or oversized.", true, nil)
		return
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		contractErr := validateAgentSessionSnapshot(raw)
		if route.kind == "LIST" {
			contractErr = validateAgentSessionList(raw)
		}
		if !strings.HasPrefix(contentType, "application/json") || contractErr != nil {
			writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_CONTRACT_FAILED", "Operations Agent contract failed", "The Operations Agent returned an invalid Agent Session snapshot.", true, nil)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(raw)
		return
	}
	if response.StatusCode == http.StatusNotFound {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Agent Session was not found.", false, nil)
		return
	}
	if strings.HasPrefix(contentType, "application/problem+json") && response.StatusCode >= 400 && response.StatusCode < 500 {
		sanitized, err := sanitizeAgentSessionProblem(raw, response.StatusCode)
		if err != nil {
			writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_CONTRACT_FAILED", "Operations Agent contract failed", "The Operations Agent returned an invalid Agent Session problem.", true, nil)
			return
		}
		writer.Header().Set("Content-Type", "application/problem+json")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(sanitized)
		return
	}
	writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent gateway failed", "The Operations Agent returned an invalid upstream response.", true, nil)
}
