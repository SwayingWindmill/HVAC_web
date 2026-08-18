package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
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

func formatRevision(value int64) string {
	return strconv.FormatInt(value, 10)
}

type contextKey string

const (
	traceIDContextKey       contextKey = "trace-id"
	requestIDContextKey     contextKey = "request-id"
	traceparentContextKey   contextKey = "traceparent"
	routeDecisionContextKey contextKey = "route-decision"
	routeSessionContextKey  contextKey = "route-session"
)

// Config contains edge-only dependencies. It intentionally has no business
// domain or persistence dependencies.
type Config struct {
	Build         platformapi.BuildInfo
	Logger        *slog.Logger
	Now           func() time.Time
	Identity      *IdentityConfig
	RouteManager  *ownershipregistry.Manager
	RouteAudit    ownershipregistry.AuditSink
	Registry      *RegistryConfig
	Telemetry     *TelemetryConfig
	Command       *CommandConfig
	Alarm         *AlarmConfig
	WorkOrder     *WorkOrderConfig
	Analytics     *AnalyticsConfig
	Operations    *OperationsAgentConfig
	Observability *observability.Runtime
}

type handler struct {
	build         platformapi.BuildInfo
	logger        *slog.Logger
	now           func() time.Time
	identity      *identityController
	routeManager  *ownershipregistry.Manager
	routeAudit    ownershipregistry.AuditSink
	registry      *registryController
	telemetry     *telemetryController
	command       *commandController
	alarm         *alarmController
	workOrder     *workOrderController
	analytics     *analyticsController
	operations    *operationsAgentController
	observability *observability.Runtime
}

var _ platformapi.ServerInterface = (*handler)(nil)
var _ platformapi.RegistryServerInterface = (*handler)(nil)

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
	routeAudit := config.RouteAudit
	if routeAudit == nil {
		routeAudit = ownershipregistry.NewMemoryAuditSink()
	}
	telemetry := config.Observability
	if telemetry == nil {
		telemetry = observability.NewRuntime(observability.RuntimeConfig{Service: serviceName})
	}
	return &handler{
		build:         build,
		logger:        logger,
		now:           now,
		identity:      newIdentityController(config.Identity, now),
		routeManager:  config.RouteManager,
		routeAudit:    routeAudit,
		registry:      newRegistryController(config.Registry),
		telemetry:     newTelemetryController(config.Telemetry),
		command:       newCommandController(config.Command),
		alarm:         newAlarmController(config.Alarm),
		workOrder:     newWorkOrderController(config.WorkOrder),
		analytics:     newAnalyticsController(config.Analytics),
		operations:    newOperationsAgentController(config.Operations),
		observability: telemetry,
	}
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	startedAt := h.now()
	requestID := selectRequestID(request.Header.Get("X-Request-ID"), startedAt)
	ctx := h.observability.Tracer.ExtractHTTP(request.Context(), request.Header)
	ctx, span := h.observability.Tracer.Start(ctx, "http.gateway.request", observability.SpanKindServer, map[string]any{
		"http.request.method": request.Method, "http.route": safeLogPath(request.URL.Path),
	})
	traceID := observability.TraceID(ctx)
	traceparent := observability.Traceparent(ctx)

	writer.Header().Set("X-Request-ID", requestID)
	writer.Header().Set("traceparent", traceparent)

	ctx = context.WithValue(ctx, traceIDContextKey, traceID)
	ctx = context.WithValue(ctx, requestIDContextKey, requestID)
	ctx = context.WithValue(ctx, traceparentContextKey, traceparent)
	request = request.WithContext(ctx)

	recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
	defer func() {
		if recovered := recover(); recovered != nil {
			writeProblem(recorder, request, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "The request could not be completed.", true, nil)
		}
		result := "ok"
		if recorder.status >= http.StatusBadRequest {
			result = "error"
			span.SetStatus("error", http.StatusText(recorder.status))
		} else {
			span.SetStatus("ok", "")
		}
		span.SetAttributes(map[string]any{"http.response.status_code": recorder.status})
		span.End()
		_ = h.observability.Metrics.AddCounter("s0_http_requests_total", "Gateway HTTP requests.", map[string]string{"service": serviceName, "route": safeLogPath(request.URL.Path), "method": request.Method, "result": result}, 1)
		_ = h.observability.Metrics.ObserveHistogram("s0_http_request_duration_seconds", "Gateway HTTP request latency.", map[string]string{"service": serviceName, "route": safeLogPath(request.URL.Path), "method": request.Method}, h.now().Sub(startedAt).Seconds(), nil)
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
	if request.URL.Path == InternalOperationsToolAuthorizationPath {
		h.authorizeOperationsTool(writer, request)
		return
	}
	for _, header := range []string{"X-Principal", "X-Roles", "X-Organization-ID", "X-Site-ID", "X-Admin", "X-Delegation-Grant", "X-Command-Grant", "X-Command-Read-Context", "X-Alarm-Read-Context", "X-Alarm-Write-Context", "X-Work-Order-Read-Context", "X-Work-Order-Write-Context", "X-Acting-Organization-ID", "X-Operations-Registry-Site-Grant", "X-Operations-Registry-Asset-Grant", "X-Operations-Registry-Equipment-Grant", "X-Operations-Energy-Grant"} {
		if request.Header.Get(header) == "" {
			continue
		}
		writeProblem(writer, request, http.StatusBadRequest, "FORGED_IDENTITY_HEADER", "Forged identity header", "Caller-supplied identity headers are not accepted at the public edge.", false, nil)
		return
	}

	if contractOnlyRoute, matches := matchV212ContractOnlyRoute(request.URL.Path); matches {
		// /api/v1/sites is shared by the active GET and shape-pending POST.
		// Only the POST belongs to the contract-only boundary; GET must continue
		// through the normal Registry owner path.
		if contractOnlyRoute.template != "/api/v1/sites" || request.Method == contractOnlyRoute.method {
			writeV212ContractOnly(writer, request, contractOnlyRoute)
			return
		}
	}

	if h.routeManager != nil {
		resolved, ok := h.applyRouteOwnership(writer, request)
		if !ok {
			return
		}
		request = resolved
		if registryRoute, id, matches := matchPublicRegistryRoute(request.URL.Path); matches {
			dispatchRegistryRoute(h, writer, request, registryRoute, id)
			return
		}
		if operationsRoute, matches := matchPublicOperationsRoute(request.URL.Path); matches {
			dispatchOperationsRoute(h, writer, request, operationsRoute)
			return
		}
		if workOrderRoute, matches := matchPublicWorkOrderRoute(request.URL.Path); matches {
			decision := routeDecisionFromContext(request.Context())
			if decision.SelectedOwner != ownershipregistry.OwnerWorkOrder {
				writeProblem(writer, request, http.StatusServiceUnavailable, "WORK_ORDER_UNAVAILABLE", "Work Order unavailable", "The Work Order route is not active for this Session.", true, nil)
				return
			}
			dispatchWorkOrderRoute(h, writer, request, workOrderRoute)
			return
		}
		if alarmRoute, matches := matchPublicAlarmRoute(request.URL.Path); matches {
			decision := routeDecisionFromContext(request.Context())
			if decision.SelectedOwner != ownershipregistry.OwnerAlarm {
				writeProblem(writer, request, http.StatusServiceUnavailable, "ALARM_UNAVAILABLE", "Alarm unavailable", "The Alarm route is not active for this Session.", true, nil)
				return
			}
			dispatchAlarmRoute(h, writer, request, alarmRoute)
			return
		}
	}

	if operationsRoute, matches := matchPublicOperationsRoute(request.URL.Path); matches {
		dispatchOperationsRoute(h, writer, request, operationsRoute)
		return
	}
	if telemetryRoute, deviceID, matches := matchPublicTelemetryRoute(request.URL.Path); matches {
		dispatchTelemetryRoute(h, writer, request, telemetryRoute, deviceID)
		return
	}
	if commandRoute, commandID, matches := matchPublicCommandRoute(request.URL.Path); matches {
		dispatchCommandRoute(h, writer, request, commandRoute, commandID)
		return
	}
	if request.URL.Path == PublicEnergySeriesPath {
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		h.QueryEnergySeries(writer, request)
		return
	}

	switch request.URL.Path {
	case platformapi.GetHealthPath:
		if request.Method != http.MethodGet {
			writeMethodNotAllowedFor(writer, request, http.MethodGet)
			return
		}
		params, ok := parseGetHealthParams(writer, request)
		if !ok {
			return
		}
		h.GetHealth(writer, request, params)
	case platformapi.GetVersionPath:
		if request.Method != http.MethodGet {
			writeMethodNotAllowedFor(writer, request, http.MethodGet)
			return
		}
		h.GetVersion(writer, request)
	case platformapi.GetPlatformStatusPath:
		if request.Method != http.MethodGet {
			writeMethodNotAllowedFor(writer, request, http.MethodGet)
			return
		}
		h.GetPlatformStatus(writer, request)
	case platformapi.BeginLoginPath:
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		params, ok := parseBeginLoginParams(writer, request)
		if !ok {
			return
		}
		h.BeginLogin(writer, request, params)
	case platformapi.CompleteLoginPath:
		if request.Method != http.MethodGet {
			writeMethodNotAllowedFor(writer, request, http.MethodGet)
			return
		}
		params, ok := parseCompleteLoginParams(writer, request)
		if !ok {
			return
		}
		h.CompleteLogin(writer, request, params)
	case platformapi.GetCurrentPrincipalPath:
		if request.Method != http.MethodGet {
			writeMethodNotAllowedFor(writer, request, http.MethodGet)
			return
		}
		h.GetCurrentPrincipal(writer, request)
	case platformapi.LogoutPath:
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		h.Logout(writer, request, platformapi.LogoutParams{CSRFToken: request.Header.Get("X-CSRF-Token")})
	default:
		if messageID, matches := matchSessionAuditPath(request.URL.Path); matches {
			if request.Method != http.MethodGet {
				writeMethodNotAllowedFor(writer, request, http.MethodGet)
				return
			}
			decodedMessageID, err := url.PathUnescape(messageID)
			if err != nil || decodedMessageID == "" {
				writeProblem(writer, request, http.StatusBadRequest, "INVALID_AUDIT_MESSAGE_ID", "Invalid audit message ID", "The audit message identifier is invalid.", false, nil)
				return
			}
			h.GetSessionAuditEvent(writer, request, platformapi.GetSessionAuditEventParams{MessageID: decodedMessageID})
			return
		}
		if sessionID, matches := matchRevokeSessionPath(request.URL.Path); matches {
			if request.Method != http.MethodPost {
				writeMethodNotAllowedFor(writer, request, http.MethodPost)
				return
			}
			decodedSessionID, err := url.PathUnescape(sessionID)
			if err != nil || decodedSessionID == "" {
				writeProblem(writer, request, http.StatusBadRequest, "INVALID_SESSION_ID", "Invalid session ID", "The session identifier is invalid.", false, nil)
				return
			}
			h.RevokeSession(writer, request, platformapi.RevokeSessionParams{SessionID: decodedSessionID, CSRFToken: request.Header.Get("X-CSRF-Token")})
			return
		}
		writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested public API route does not exist.", false, nil)
	}
}

func (h *handler) applyRouteOwnership(writer http.ResponseWriter, request *http.Request) (*http.Request, bool) {
	snapshot := h.routeManager.Current()
	decision, err := snapshot.Resolve(request.Method, request.URL.Path, "")
	var session bffSession
	var workloadCaller telemetryCaller
	if errors.Is(err, ownershipregistry.ErrRouteMissing) {
		methods := snapshot.AllowedMethods(request.URL.Path)
		if len(methods) > 0 {
			writer.Header().Set("Allow", strings.Join(methods, ", "))
			writeProblem(writer, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "This route does not support the requested method.", false, nil)
			return request, false
		}
		writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested public API route has no applied owner.", false, nil)
		return request, false
	}
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "ROUTE_OWNERSHIP_INVALID", "Route ownership invalid", "The applied route ownership policy could not select one owner.", true, nil)
		return request, false
	}
	if session.ID == "" && workloadCaller.contextID == "" {
		if _, _, registryRoute := matchPublicRegistryRoute(request.URL.Path); registryRoute {
			resolved, failure := h.identitySession(request)
			if failure != nil {
				writeIdentityFailure(writer, request, *failure)
				return request, false
			}
			session = resolved
		} else if _, _, telemetryRoute := matchPublicTelemetryRoute(request.URL.Path); telemetryRoute {
			if isVerifiedTelemetryWorkloadRequest(request) {
				resolved, failure := h.telemetryWorkloadCaller(request)
				if failure != nil {
					h.writeTelemetryFailure(writer, request, *failure)
					return request, false
				}
				workloadCaller = resolved
			} else if _, cookieErr := request.Cookie(sessionCookieName); cookieErr == nil {
				resolved, failure := h.identitySession(request)
				if failure != nil {
					writeIdentityFailure(writer, request, *failure)
					return request, false
				}
				session = resolved
			}
		}
	}

	writer.Header().Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	if span := observability.SpanFromContext(request.Context()); span != nil {
		span.SetAttributes(map[string]any{
			"route.owner": decision.SelectedOwner, "route.policy.revision": decision.RegistryRevision,
			"route.revision": decision.RouteRevision, "route.compatibility_mode": decision.CompatibilityMode,
		})
	}
	auditRecord := ownershipregistry.AuditRecord{
		EventType:         "ROUTE_DECIDED",
		RouteKey:          decision.RouteKey,
		Method:            request.Method,
		PathTemplate:      decision.PathTemplate,
		SelectedOwner:     decision.SelectedOwner,
		RegistryRevision:  decision.RegistryRevision,
		RouteRevision:     decision.RouteRevision,
		CompatibilityMode: decision.CompatibilityMode,
		ExecutingService:  serviceName,
		CorrelationID:     requestIDFromContext(request.Context()),
		TraceID:           traceIDFromContext(request.Context()),
		OccurredAt:        h.now().UTC(),
	}
	if h.identity != nil {
		auditRecord.ExecutingSPIFFEID = h.identity.config.ExecutingWorkloadSPIFFE
		auditRecord.PolicyRevision = h.identity.config.PolicyRevision
	}
	if session.ID != "" {
		auditRecord.TenantID = session.TenantID
		auditRecord.InitiatingSubject = session.Principal.Subject
		auditRecord.InitiatingIssuer = session.Principal.Issuer
	} else if workloadCaller.contextID != "" {
		auditRecord.TenantID = workloadCaller.tenantID
		auditRecord.InitiatingSubject = workloadCaller.principal.Subject
		auditRecord.InitiatingIssuer = workloadCaller.principal.Issuer
	}
	if err := h.routeAudit.Record(request.Context(), auditRecord); err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "ROUTE_AUDIT_FAILED", "Route audit unavailable", "The route decision could not be recorded before execution.", true, nil)
		return request, false
	}
	ctx := context.WithValue(request.Context(), routeDecisionContextKey, decision)
	if session.ID != "" {
		ctx = context.WithValue(ctx, routeSessionContextKey, session)
	} else if workloadCaller.contextID != "" {
		ctx = context.WithValue(ctx, telemetryCallerContextKey, workloadCaller)
	}
	return request.WithContext(ctx), true
}

func routeDecisionFromContext(ctx context.Context) ownershipregistry.Decision {
	decision, _ := ctx.Value(routeDecisionContextKey).(ownershipregistry.Decision)
	return decision
}

func routeSessionFromContext(ctx context.Context) (bffSession, bool) {
	session, ok := ctx.Value(routeSessionContextKey).(bffSession)
	return session, ok
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

// GetPlatformStatus returns the normalized representation selected by the applied route policy.
func (h *handler) GetPlatformStatus(writer http.ResponseWriter, request *http.Request) {
	decision := routeDecisionFromContext(request.Context())
	if decision.RegistryRevision == 0 {
		decision = ownershipregistry.Decision{
			RouteKey:          http.MethodGet + " " + platformapi.GetPlatformStatusPath,
			PathTemplate:      platformapi.GetPlatformStatusPath,
			SelectedOwner:     ownershipregistry.OwnerGateway,
			RegistryRevision:  1,
			RouteRevision:     1,
			CompatibilityMode: "native",
		}
	}
	if _, failure := h.identitySession(request); failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	writer.Header().Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	writeJSON(writer, http.StatusOK, platformapi.PlatformStatusResponse{
		Status:              "ok",
		Service:             "platform-status",
		Implementation:      "go",
		Version:             h.build.Version,
		CheckedAt:           h.now().UTC().Format(time.RFC3339Nano),
		RoutePolicyRevision: decision.RegistryRevision,
		RouteRevision:       decision.RouteRevision,
		CompatibilityMode:   "native",
	})
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

func parseBeginLoginParams(writer http.ResponseWriter, request *http.Request) (platformapi.BeginLoginParams, bool) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "MALFORMED_QUERY", "Malformed query", "The query string is not valid URL-encoded data.", false, nil)
		return platformapi.BeginLoginParams{}, false
	}
	for key := range query {
		if key != "returnTo" && key != "login_hint" && key != "assurance" {
			writeProblem(writer, request, http.StatusBadRequest, "INVALID_QUERY_PARAMETER", "Invalid query parameter", "One or more query parameters are not supported.", false, []platformapi.FieldError{{Field: key, Message: "unsupported query parameter"}})
			return platformapi.BeginLoginParams{}, false
		}
	}
	returnTo := query.Get("returnTo")
	if returnTo == "" {
		returnTo = "/system"
	}
	assurance := query.Get("assurance")
	if assurance == "" {
		assurance = "normal"
	}
	if assurance != "normal" && assurance != "high" {
		writeProblem(writer, request, http.StatusBadRequest, "INVALID_QUERY_PARAMETER", "Invalid query parameter", "The assurance query parameter must be normal or high.", false, []platformapi.FieldError{{Field: "assurance", Message: "must be normal or high"}})
		return platformapi.BeginLoginParams{}, false
	}
	return platformapi.BeginLoginParams{ReturnTo: returnTo, LoginHint: query.Get("login_hint"), Assurance: assurance}, true
}

func parseCompleteLoginParams(writer http.ResponseWriter, request *http.Request) (platformapi.CompleteLoginParams, bool) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "MALFORMED_QUERY", "Malformed query", "The query string is not valid URL-encoded data.", false, nil)
		return platformapi.CompleteLoginParams{}, false
	}
	for key := range query {
		if key != "code" && key != "state" && key != "iss" {
			writeProblem(writer, request, http.StatusBadRequest, "INVALID_QUERY_PARAMETER", "Invalid query parameter", "One or more query parameters are not supported.", false, []platformapi.FieldError{{Field: key, Message: "unsupported query parameter"}})
			return platformapi.CompleteLoginParams{}, false
		}
	}
	code, state := query.Get("code"), query.Get("state")
	if code == "" || state == "" {
		writeProblem(writer, request, http.StatusBadRequest, "OIDC_CALLBACK_INVALID", "OIDC callback invalid", "The callback code and state are required.", false, nil)
		return platformapi.CompleteLoginParams{}, false
	}
	return platformapi.CompleteLoginParams{Code: code, State: state, Issuer: query.Get("iss")}, true
}

func writeMethodNotAllowedFor(writer http.ResponseWriter, request *http.Request, allowed string) {
	writer.Header().Set("Allow", allowed)
	writeProblem(writer, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "This route does not support the requested method.", false, nil)
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
	case platformapi.GetHealthPath, platformapi.GetVersionPath, platformapi.GetPlatformStatusPath, platformapi.BeginLoginPath, platformapi.CompleteLoginPath, platformapi.GetCurrentPrincipalPath, platformapi.LogoutPath, InternalOperationsToolAuthorizationPath:
		return path
	default:
		if contractOnlyRoute, matches := matchV212ContractOnlyRoute(path); matches {
			return contractOnlyRoute.template
		}
		if registryRoute, _, matches := matchPublicRegistryRoute(path); matches {
			return registryRoute.template
		}
		if operationsRoute, matches := matchPublicOperationsRoute(path); matches {
			return operationsRoute.template
		}
		if telemetryRoute, _, matches := matchPublicTelemetryRoute(path); matches {
			return telemetryRoute.template
		}
		if _, matches := matchSessionAuditPath(path); matches {
			return platformapi.GetSessionAuditEventPathTemplate
		}
		if _, matches := matchRevokeSessionPath(path); matches {
			return platformapi.RevokeSessionPathTemplate
		}
		return "unmatched"
	}
}

func matchSessionAuditPath(path string) (string, bool) {
	return matchSinglePathParameter(path, platformapi.GetSessionAuditEventPathTemplate, "{messageId}")
}

func matchRevokeSessionPath(path string) (string, bool) {
	return matchSinglePathParameter(path, platformapi.RevokeSessionPathTemplate, "{sessionId}")
}

func matchSinglePathParameter(path, template, placeholder string) (string, bool) {
	parts := strings.Split(template, placeholder)
	if len(parts) != 2 || !strings.HasPrefix(path, parts[0]) || !strings.HasSuffix(path, parts[1]) {
		return "", false
	}
	sessionID := strings.TrimSuffix(strings.TrimPrefix(path, parts[0]), parts[1])
	if sessionID == "" || strings.Contains(sessionID, "/") {
		return "", false
	}
	return sessionID, true
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

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

func traceparentFromContext(ctx context.Context) string {
	traceparent, _ := ctx.Value(traceparentContextKey).(string)
	return traceparent
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
