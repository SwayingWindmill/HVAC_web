package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

const (
	serviceName        = "platform-gateway"
	problemTypeBaseURL = "https://api.quanlaihe.com/problems/"
)

var (
	requestIDPattern   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	traceparentPattern = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$`)
	zeroTraceID        = strings.Repeat("0", 32)
	zeroSpanID         = strings.Repeat("0", 16)
)

type contextKey string

const traceIDContextKey contextKey = "trace-id"

// Config contains edge-only dependencies. It intentionally has no business,
// persistence, identity, or integration dependencies.
type Config struct {
	Build  platformapi.BuildInfo
	Logger *slog.Logger
	Now    func() time.Time
}

type handler struct {
	build  platformapi.BuildInfo
	logger *slog.Logger
	now    func() time.Time
}

var _ platformapi.ServerInterface = (*handler)(nil)

// NewHandler creates the public HTTP seam owned by platform-gateway.
func NewHandler(config Config) http.Handler {
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	build := config.Build
	build.Service = serviceName
	return &handler{build: build, logger: logger, now: now}
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	startedAt := h.now()
	requestID := selectRequestID(request.Header.Get("X-Request-ID"), startedAt)
	traceID, traceparent := selectTraceparent(request.Header.Get("traceparent"), startedAt)

	writer.Header().Set("X-Request-ID", requestID)
	writer.Header().Set("traceparent", traceparent)

	ctx := context.WithValue(request.Context(), traceIDContextKey, traceID)
	request = request.WithContext(ctx)

	recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
	defer func() {
		if recovered := recover(); recovered != nil {
			writeProblem(recorder, request, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "The request could not be completed.", true, nil)
		}
		h.logger.InfoContext(
			request.Context(),
			"http_request",
			"service", serviceName,
			"method", request.Method,
			"path", safeLogPath(request.URL.Path),
			"status", recorder.status,
			"duration_ms", h.now().Sub(startedAt).Milliseconds(),
			"request_id", requestID,
			"trace_id", traceID,
		)
	}()

	h.route(recorder, request)
}

func (h *handler) route(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case platformapi.GetHealthPath:
		if request.Method != http.MethodGet {
			writeMethodNotAllowed(writer, request)
			return
		}
		params, ok := parseGetHealthParams(writer, request)
		if !ok {
			return
		}
		h.GetHealth(writer, request, params)
	case platformapi.GetVersionPath:
		if request.Method != http.MethodGet {
			writeMethodNotAllowed(writer, request)
			return
		}
		h.GetVersion(writer, request)
	default:
		writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested public API route does not exist.", false, nil)
	}
}

// GetHealth implements the generated OpenAPI server interface.
func (h *handler) GetHealth(writer http.ResponseWriter, _ *http.Request, params platformapi.GetHealthParams) {
	response := platformapi.HealthResponse{
		Status:    "ok",
		Service:   serviceName,
		CheckedAt: h.now().UTC().Format(time.RFC3339Nano),
	}
	if params.IncludeBuild != nil && *params.IncludeBuild {
		build := h.build
		response.Build = &build
	}
	writeJSON(writer, http.StatusOK, response)
}

// GetVersion implements the generated OpenAPI server interface.
func (h *handler) GetVersion(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, h.build)
}

func parseGetHealthParams(writer http.ResponseWriter, request *http.Request) (platformapi.GetHealthParams, bool) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "MALFORMED_QUERY", "Malformed query", "The query string is not valid URL-encoded data.", false, nil)
		return platformapi.GetHealthParams{}, false
	}

	for key := range query {
		if key != "includeBuild" {
			writeProblem(
				writer,
				request,
				http.StatusBadRequest,
				"INVALID_QUERY_PARAMETER",
				"Invalid query parameter",
				"One or more query parameters are not supported.",
				false,
				[]platformapi.FieldError{{Field: key, Message: "unsupported query parameter"}},
			)
			return platformapi.GetHealthParams{}, false
		}
	}

	values, exists := query["includeBuild"]
	if !exists {
		return platformapi.GetHealthParams{}, true
	}
	if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
		writeProblem(
			writer,
			request,
			http.StatusBadRequest,
			"INVALID_QUERY_PARAMETER",
			"Invalid query parameter",
			"The includeBuild query parameter must be true or false.",
			false,
			[]platformapi.FieldError{{Field: "includeBuild", Message: "must be true or false"}},
		)
		return platformapi.GetHealthParams{}, false
	}
	includeBuild := values[0] == "true"
	return platformapi.GetHealthParams{IncludeBuild: &includeBuild}, true
}

func writeMethodNotAllowed(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Allow", http.MethodGet)
	writeProblem(writer, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "This route only supports GET.", false, nil)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeProblem(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	code string,
	title string,
	detail string,
	retryable bool,
	fieldErrors []platformapi.FieldError,
) {
	problem := platformapi.ProblemDetails{
		Type:        problemTypeBaseURL + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		Title:       title,
		Status:      status,
		Detail:      detail,
		Instance:    request.URL.Path,
		Code:        code,
		TraceID:     traceIDFromContext(request.Context()),
		Retryable:   retryable,
		FieldErrors: fieldErrors,
	}
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(problem)
}

func safeLogPath(path string) string {
	switch path {
	case platformapi.GetHealthPath, platformapi.GetVersionPath:
		return path
	default:
		return "unmatched"
	}
}

func selectRequestID(candidate string, now time.Time) string {
	if requestIDPattern.MatchString(candidate) {
		return candidate
	}
	return randomHex(16, now)
}

func selectTraceparent(candidate string, now time.Time) (string, string) {
	traceID := randomHex(16, now)
	flags := "01"
	if match := traceparentPattern.FindStringSubmatch(candidate); len(match) == 4 && match[1] != zeroTraceID && match[2] != zeroSpanID {
		traceID = match[1]
		flags = match[3]
	}
	spanID := randomHex(8, now)
	if spanID == zeroSpanID {
		spanID = "0000000000000001"
	}
	return traceID, fmt.Sprintf("00-%s-%s-%s", traceID, spanID, flags)
}

func randomHex(byteCount int, now time.Time) string {
	buffer := make([]byte, byteCount)
	if _, err := rand.Read(buffer); err == nil {
		return hex.EncodeToString(buffer)
	}
	fallback := sha256.Sum256([]byte(fmt.Sprintf("%d-%d", now.UnixNano(), byteCount)))
	return hex.EncodeToString(fallback[:byteCount])
}

func traceIDFromContext(ctx context.Context) string {
	traceID, _ := ctx.Value(traceIDContextKey).(string)
	return traceID
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (recorder *statusRecorder) WriteHeader(status int) {
	if recorder.wroteHeader {
		return
	}
	recorder.status = status
	recorder.wroteHeader = true
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(body []byte) (int, error) {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(body)
}
