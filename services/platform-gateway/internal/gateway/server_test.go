package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/services/platform-gateway/internal/gateway"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

var fixedTime = time.Date(2026, time.July, 19, 2, 30, 0, 123000000, time.UTC)

func TestGatewaySuccessUsesGeneratedContract(t *testing.T) {
	server := httptest.NewServer(newTestHandler(io.Discard))
	t.Cleanup(server.Close)

	client, err := platformapi.NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("create generated client: %v", err)
	}
	includeBuild := true
	health, err := client.GetHealth(context.Background(), &platformapi.GetHealthParams{IncludeBuild: &includeBuild})
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	if health.StatusCode != http.StatusOK || health.Data == nil {
		t.Fatalf("unexpected health response: %#v", health)
	}
	if health.Data.Status != "ok" || health.Data.Service != "platform-gateway" {
		t.Fatalf("unexpected health payload: %#v", health.Data)
	}
	if health.Data.CheckedAt != fixedTime.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected checkedAt: %q", health.Data.CheckedAt)
	}
	if health.Data.Build == nil || health.Data.Build.Version != "0.1.0-test" {
		t.Fatalf("expected build information: %#v", health.Data.Build)
	}
	assertCorrelationHeaders(t, health.RequestID, health.Traceparent)

	version, err := client.GetVersion(context.Background())
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if version.StatusCode != http.StatusOK || version.Data == nil || version.Data.Commit != "test-commit" {
		t.Fatalf("unexpected version response: %#v", version)
	}

	rawResponse, err := server.Client().Get(server.URL + platformapi.GetHealthPath)
	if err != nil {
		t.Fatalf("get raw health: %v", err)
	}
	defer rawResponse.Body.Close()
	var rawPayload map[string]any
	if err := json.NewDecoder(rawResponse.Body).Decode(&rawPayload); err != nil {
		t.Fatalf("decode raw health: %v", err)
	}
	for _, forbidden := range []string{"success", "data", "message"} {
		if _, exists := rawPayload[forbidden]; exists {
			t.Fatalf("global response envelope field %q must not be present", forbidden)
		}
	}
}

func TestGatewayReturnsStableProblemDetails(t *testing.T) {
	server := httptest.NewServer(newTestHandler(io.Discard))
	t.Cleanup(server.Close)

	testCases := []struct {
		name       string
		method     string
		path       string
		status     int
		code       string
		allow      string
		fieldError string
	}{
		{name: "method not allowed", method: http.MethodPost, path: platformapi.GetHealthPath, status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED", allow: http.MethodGet},
		{name: "unknown route", method: http.MethodGet, path: "/api/v1/unknown", status: http.StatusNotFound, code: "ROUTE_NOT_FOUND"},
		{name: "invalid boolean", method: http.MethodGet, path: platformapi.GetHealthPath + "?includeBuild=yes", status: http.StatusBadRequest, code: "INVALID_QUERY_PARAMETER", fieldError: "includeBuild"},
		{name: "unknown parameter", method: http.MethodGet, path: platformapi.GetHealthPath + "?secret=hidden", status: http.StatusBadRequest, code: "INVALID_QUERY_PARAMETER", fieldError: "secret"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := http.NewRequest(testCase.method, server.URL+testCase.path, strings.NewReader("seeded-sensitive-body"))
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatalf("call gateway: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != testCase.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, testCase.status)
			}
			if contentType := response.Header.Get("Content-Type"); contentType != "application/problem+json" {
				t.Fatalf("content type = %q", contentType)
			}
			if testCase.allow != "" && response.Header.Get("Allow") != testCase.allow {
				t.Fatalf("Allow = %q, want %q", response.Header.Get("Allow"), testCase.allow)
			}

			var problem platformapi.ProblemDetails
			if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem.Code != testCase.code || problem.Status != testCase.status || problem.Retryable {
				t.Fatalf("unexpected problem: %#v", problem)
			}
			if len(problem.TraceID) != 32 || problem.Instance == "" || problem.Detail == "" {
				t.Fatalf("incomplete problem: %#v", problem)
			}
			if testCase.fieldError != "" {
				if len(problem.FieldErrors) != 1 || problem.FieldErrors[0].Field != testCase.fieldError {
					t.Fatalf("unexpected field errors: %#v", problem.FieldErrors)
				}
			}
		})
	}
}

func TestGatewayReturnsProblemForMalformedQuery(t *testing.T) {
	handler := newTestHandler(io.Discard)
	request := httptest.NewRequest(http.MethodGet, platformapi.GetHealthPath, nil)
	request.URL.RawQuery = "includeBuild=%zz"
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	var problem platformapi.ProblemDetails
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != "MALFORMED_QUERY" || problem.TraceID == "" {
		t.Fatalf("unexpected problem: %#v", problem)
	}
}

func TestGatewayContinuesW3CTraceAndStableRequestID(t *testing.T) {
	server := httptest.NewServer(newTestHandler(io.Discard))
	t.Cleanup(server.Close)

	const incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const incomingSpanID = "00f067aa0ba902b7"
	const incomingTraceparent = "00-" + incomingTraceID + "-" + incomingSpanID + "-01"
	const requestID = "browser-request-01"

	request, err := http.NewRequest(http.MethodGet, server.URL+platformapi.GetVersionPath, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("traceparent", incomingTraceparent)
	request.Header.Set("X-Request-ID", requestID)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("call gateway: %v", err)
	}
	defer response.Body.Close()

	if response.Header.Get("X-Request-ID") != requestID {
		t.Fatalf("request id was not preserved: %q", response.Header.Get("X-Request-ID"))
	}
	outgoingTraceparent := response.Header.Get("traceparent")
	parts := strings.Split(outgoingTraceparent, "-")
	if len(parts) != 4 || parts[1] != incomingTraceID || parts[2] == incomingSpanID || parts[3] != "01" {
		t.Fatalf("unexpected trace continuation: %q", outgoingTraceparent)
	}
}

func TestStructuredLogsExcludeCredentialsCookiesAndBodies(t *testing.T) {
	var logs bytes.Buffer
	server := httptest.NewServer(newTestHandler(&logs))
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/seeded-sensitive-path-secret", strings.NewReader("seeded-sensitive-body"))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer seeded-sensitive-token")
	request.Header.Set("Cookie", "session=seeded-sensitive-cookie")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("call gateway: %v", err)
	}
	response.Body.Close()

	logOutput := logs.String()
	for _, forbidden := range []string{"seeded-sensitive-token", "seeded-sensitive-cookie", "seeded-sensitive-body", "seeded-sensitive-path-secret", "Authorization", "Cookie"} {
		if strings.Contains(logOutput, forbidden) {
			t.Fatalf("structured log leaked %q: %s", forbidden, logOutput)
		}
	}
	for _, required := range []string{`"method":"POST"`, `"path":"unmatched"`, `"status":404`, `"trace_id"`} {
		if !strings.Contains(logOutput, required) {
			t.Fatalf("structured log missing %q: %s", required, logOutput)
		}
	}
}

func newTestHandler(logOutput io.Writer) http.Handler {
	return gateway.NewHandler(gateway.Config{
		Logger: slog.New(slog.NewJSONHandler(logOutput, nil)),
		Now:    func() time.Time { return fixedTime },
		Build: platformapi.BuildInfo{
			Service: "platform-gateway",
			Version: "0.1.0-test",
			Commit:  "test-commit",
			BuiltAt: "2026-07-19T02:00:00Z",
		},
	})
}

func assertCorrelationHeaders(t *testing.T, requestID, traceparent string) {
	t.Helper()
	if len(requestID) != 32 {
		t.Fatalf("request id = %q", requestID)
	}
	parts := strings.Split(traceparent, "-")
	if len(parts) != 4 || len(parts[1]) != 32 || len(parts[2]) != 16 {
		t.Fatalf("traceparent = %q", traceparent)
	}
}
