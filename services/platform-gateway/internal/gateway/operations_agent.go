package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	PublicOperationsInvestigationsPathTemplate      = "/api/v1/sites/{siteId}/operations/investigations"
	PublicOperationsInvestigationPathTemplate       = "/api/v1/sites/{siteId}/operations/investigations/{investigationId}"
	PublicOperationsEventsPathTemplate              = "/api/v1/sites/{siteId}/operations/investigations/{investigationId}/events"
	PublicOperationsAdvancePathTemplate             = "/api/v1/sites/{siteId}/operations/investigations/{investigationId}:advance"
	PublicOperationsSubmitOperatorInputPathTemplate = "/api/v1/sites/{siteId}/operations/investigations/{investigationId}:submit-operator-input"
	PublicOperationsCancelPathTemplate              = "/api/v1/sites/{siteId}/operations/investigations/{investigationId}:cancel"
	InternalOperationsToolAuthorizationPath         = "/internal/v1/operations/tool-authorization"
	defaultOperationsTimeout                        = 8 * time.Second
	defaultOperationsRequestBytes                   = int64(8 << 10)
	defaultOperationsResponseBytes                  = int64(1 << 20)
	defaultOperationsRatePerMinute                  = 30
)

var errOperationsBodyTooLarge = errors.New("Operations request or response body is too large")

type OperationsAgentConfig struct {
	BaseURL            string
	Audience           string
	WorkloadSPIFFEID   string
	HTTPClient         *http.Client
	Timeout            time.Duration
	MaxRequestBytes    int64
	MaxResponseBytes   int64
	RateLimitPerMinute int
}

type operationsAgentController struct {
	baseURL            string
	audience           string
	workloadSPIFFEID   string
	httpClient         *http.Client
	timeout            time.Duration
	maxRequestBytes    int64
	maxResponseBytes   int64
	rateLimitPerMinute int
	mu                 sync.Mutex
	rateWindows        map[string]operationsRateWindow
}

type operationsRateWindow struct {
	startedAt time.Time
	count     int
}

type publicOperationsRoute struct {
	kind            string
	template        string
	siteID          string
	investigationID string
	internalPath    string
	method          string
	mutation        bool
}

type operationsToolAuthorizationRequest struct {
	InvestigationID string `json:"investigationId"`
	RunID           string `json:"runId"`
	Request         struct {
		RequestID string          `json:"requestId"`
		Tool      string          `json:"tool"`
		Input     json.RawMessage `json:"input"`
	} `json:"request"`
}

type operationsToolAuthorizationResponse struct {
	DelegationGrant string `json:"delegationGrant"`
	PolicyRevision  string `json:"policyRevision"`
}

type operationsSubmitOperatorInputRequest struct {
	SchemaVersion    int         `json:"schemaVersion"`
	RequestID        string      `json:"requestId"`
	ExpectedRevision json.Number `json:"expectedRevision"`
	Values           struct {
		AnalysisScope string          `json:"analysisScope"`
		OperatorNote  json.RawMessage `json:"operatorNote"`
	} `json:"values"`
}

func newOperationsAgentController(config *OperationsAgentConfig) *operationsAgentController {
	controller := &operationsAgentController{
		timeout:            defaultOperationsTimeout,
		maxRequestBytes:    defaultOperationsRequestBytes,
		maxResponseBytes:   defaultOperationsResponseBytes,
		rateLimitPerMinute: defaultOperationsRatePerMinute,
		rateWindows:        map[string]operationsRateWindow{},
	}
	if config == nil {
		return controller
	}
	controller.baseURL = strings.TrimRight(config.BaseURL, "/")
	controller.audience = strings.TrimSpace(config.Audience)
	controller.workloadSPIFFEID = strings.TrimSpace(config.WorkloadSPIFFEID)
	controller.httpClient = config.HTTPClient
	if config.Timeout > 0 {
		controller.timeout = config.Timeout
	}
	if config.MaxRequestBytes > 0 {
		controller.maxRequestBytes = config.MaxRequestBytes
	}
	if config.MaxResponseBytes > 0 {
		controller.maxResponseBytes = config.MaxResponseBytes
	}
	if config.RateLimitPerMinute > 0 {
		controller.rateLimitPerMinute = config.RateLimitPerMinute
	}
	return controller
}

func hasOperationsJSONContentType(request *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

func matchPublicOperationsRoute(path string) (publicOperationsRoute, bool) {
	const prefix = "/api/v1/sites/"
	if !strings.HasPrefix(path, prefix) {
		return publicOperationsRoute{}, false
	}
	remainder := strings.TrimPrefix(path, prefix)
	parts := strings.Split(remainder, "/")
	if len(parts) < 3 || parts[0] == "" || parts[1] != "operations" || parts[2] != "investigations" {
		return publicOperationsRoute{}, false
	}
	siteID, err := url.PathUnescape(parts[0])
	if err != nil || !isLowerUUIDv7(siteID) {
		return publicOperationsRoute{siteID: ""}, true
	}
	internalBase := "/internal/v1/sites/" + url.PathEscape(siteID) + "/operations/investigations"
	if len(parts) == 3 {
		return publicOperationsRoute{kind: "COLLECTION", template: PublicOperationsInvestigationsPathTemplate, siteID: siteID, internalPath: internalBase}, true
	}
	if len(parts) == 5 && parts[3] != "" && parts[4] == "events" {
		investigationID, err := url.PathUnescape(parts[3])
		if err != nil || strings.TrimSpace(investigationID) == "" || len(investigationID) > 256 {
			return publicOperationsRoute{siteID: siteID}, true
		}
		return publicOperationsRoute{
			kind: "STREAM", template: PublicOperationsEventsPathTemplate,
			siteID: siteID, investigationID: investigationID,
			internalPath: internalBase + "/" + url.PathEscape(investigationID) + "/events",
			method:       http.MethodGet, mutation: false,
		}, true
	}
	if len(parts) != 4 || parts[3] == "" {
		return publicOperationsRoute{}, false
	}
	segment := parts[3]
	kind, suffix, method, mutation := "GET", "", http.MethodGet, false
	if strings.HasSuffix(segment, ":advance") {
		kind, suffix, method, mutation = "ADVANCE", ":advance", http.MethodPost, true
		segment = strings.TrimSuffix(segment, suffix)
	} else if strings.HasSuffix(segment, ":submit-operator-input") {
		kind, suffix, method, mutation = "SUBMIT_OPERATOR_INPUT", ":submit-operator-input", http.MethodPost, true
		segment = strings.TrimSuffix(segment, suffix)
	} else if strings.HasSuffix(segment, ":cancel") {
		kind, suffix, method, mutation = "CANCEL", ":cancel", http.MethodPost, true
		segment = strings.TrimSuffix(segment, suffix)
	}
	investigationID, err := url.PathUnescape(segment)
	if err != nil || strings.TrimSpace(investigationID) == "" || len(investigationID) > 256 {
		return publicOperationsRoute{siteID: siteID}, true
	}
	template := PublicOperationsInvestigationPathTemplate
	if kind == "ADVANCE" {
		template = PublicOperationsAdvancePathTemplate
	} else if kind == "SUBMIT_OPERATOR_INPUT" {
		template = PublicOperationsSubmitOperatorInputPathTemplate
	} else if kind == "CANCEL" {
		template = PublicOperationsCancelPathTemplate
	}
	return publicOperationsRoute{kind: kind, template: template, siteID: siteID, investigationID: investigationID, internalPath: internalBase + "/" + url.PathEscape(investigationID) + suffix, method: method, mutation: mutation}, true
}

func dispatchOperationsRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicOperationsRoute) {
	if route.siteID == "" {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Operations Investigation was not found.", false, nil)
		return
	}
	if route.kind == "COLLECTION" {
		switch request.Method {
		case http.MethodGet:
			route.kind, route.method, route.mutation = "LIST", http.MethodGet, false
		case http.MethodPost:
			route.kind, route.method, route.mutation = "START", http.MethodPost, true
		default:
			writer.Header().Set("Allow", "GET, POST")
			writeProblem(writer, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "This Operations Investigation collection accepts GET or POST.", false, nil)
			return
		}
	}
	if route.kind != "START" && route.kind != "LIST" && route.investigationID == "" {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Operations Investigation was not found.", false, nil)
		return
	}
	if request.Method != route.method {
		writeMethodNotAllowedFor(writer, request, route.method)
		return
	}
	h.proxyOperationsInvestigation(writer, request, route)
}

func (h *handler) authorizeOperationsTool(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowedFor(writer, request, http.MethodPost)
		return
	}
	for _, header := range []string{
		"Cookie",
		"Origin",
		"X-CSRF-Token",
		"X-Principal",
		"X-Roles",
		"X-Organization-ID",
		"X-Site-ID",
		"X-Admin",
		"X-Acting-Organization-ID",
		"X-Command-Grant",
		"X-Command-Read-Context",
		"X-Operations-Registry-Site-Grant",
		"X-Operations-Registry-Equipment-Grant",
		"X-Operations-Energy-Grant",
	} {
		if request.Header.Get(header) != "" {
			writeProblem(writer, request, http.StatusBadRequest, "FORGED_IDENTITY_HEADER", "Forged identity header", "Browser Session and caller-supplied identity headers are not accepted on the internal Tool authorization route.", false, nil)
			return
		}
	}
	if h.operations == nil || h.operations.workloadSPIFFEID == "" || h.identity == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_AUTHORIZATION_UNAVAILABLE", "Operations authorization unavailable", "The Operations authorization boundary is not configured.", true, nil)
		return
	}
	if !hasOperationsJSONContentType(request) {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTENT_TYPE_UNSUPPORTED", "Content type unsupported", "The Tool authorization route requires application/json.", false, nil)
		return
	}
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
		writeProblem(writer, request, http.StatusUnauthorized, "OPERATIONS_WORKLOAD_INVALID", "Operations workload invalid", "A verified Operations Agent workload identity is required.", false, nil)
		return
	}
	certificate := request.TLS.PeerCertificates[0]
	if len(certificate.URIs) != 1 || certificate.URIs[0] == nil || certificate.URIs[0].String() != h.operations.workloadSPIFFEID {
		writeProblem(writer, request, http.StatusUnauthorized, "OPERATIONS_WORKLOAD_INVALID", "Operations workload invalid", "The calling workload is not the configured Operations Agent.", false, nil)
		return
	}
	raw, err := readBoundedBody(request.Body, h.operations.maxRequestBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "Request too large", "The Tool authorization request is too large.", false, nil)
		return
	}
	var input operationsToolAuthorizationRequest
	headerRequestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if decodeStrictOperationsJSON(raw, &input) != nil ||
		strings.TrimSpace(input.InvestigationID) == "" || len(input.InvestigationID) > 256 ||
		strings.TrimSpace(input.RunID) == "" || len(input.RunID) > 256 ||
		strings.TrimSpace(input.Request.RequestID) == "" || len(input.Request.RequestID) > 256 ||
		headerRequestID == "" || len(headerRequestID) > 256 || headerRequestID != input.Request.RequestID {
		writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Tool authorization request is invalid.", false, nil)
		return
	}
	var siteID string
	var registryAction registryauth.Action
	var energyQuery analyticsmodel.EnergySeriesQuery
	switch input.Request.Tool {
	case "registry.getSite":
		var registryInput struct {
			SiteID string `json:"siteId"`
		}
		if decodeStrictOperationsJSON(input.Request.Input, &registryInput) != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Registry Site request is invalid.", false, nil)
			return
		}
		siteID, registryAction = registryInput.SiteID, registryauth.ActionSiteRead
	case "registry.listSiteEquipment":
		var registryInput struct {
			SiteID string `json:"siteId"`
		}
		if decodeStrictOperationsJSON(input.Request.Input, &registryInput) != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Registry Equipment request is invalid.", false, nil)
			return
		}
		siteID, registryAction = registryInput.SiteID, registryauth.ActionEquipmentList
	case "analytics.getEnergySeries":
		if decodeStrictOperationsJSON(input.Request.Input, &energyQuery) != nil || energyQuery.Validate() != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Energy Series request is invalid.", false, nil)
			return
		}
		siteID = energyQuery.SiteID
	default:
		writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_UNSUPPORTED", "Tool unsupported", "The requested Logical Tool is not enabled for the night-energy slice.", false, nil)
		return
	}
	if !isLowerUUIDv7(siteID) {
		writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Tool request Site identity is invalid.", false, nil)
		return
	}
	claims, err := identitycontext.VerifyDelegation(h.identity.config.DelegationSigner.Public(), request.Header.Get("X-Delegation-Grant"))
	if err != nil || claims.ActingOrganizationID == "" || claims.PolicyRevision != h.identity.config.PolicyRevision {
		writeProblem(writer, request, http.StatusUnauthorized, "OPERATIONS_DELEGATION_INVALID", "Operations delegation invalid", "The Operations service delegation is invalid.", false, nil)
		return
	}
	acceptableScopes := []string{"site:" + siteID, "session:" + claims.SessionID}
	if identitycontext.ValidateDelegationAnyScope(claims, h.identity.now(), h.identity.config.ExecutingWorkloadSPIFFE, h.operations.audience, "operations:investigate", acceptableScopes) != nil {
		writeProblem(writer, request, http.StatusForbidden, "OPERATIONS_DELEGATION_REJECTED", "Operations delegation rejected", "The Operations service delegation does not authorize this Site.", false, nil)
		return
	}
	stored, err := h.identity.store.GetSession(request.Context(), claims.SessionID)
	if err != nil || stored.RevokedAt != nil || !h.identity.now().Before(stored.ExpiresAt) || stored.ActingOrganizationID != claims.ActingOrganizationID || stored.Principal.Subject != claims.Subject || stored.Principal.Issuer != claims.SubjectIssuer {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	session := bffSession{Session: stored}
	if input.Request.Tool == "analytics.getEnergySeries" {
		if energyQuery.OrganizationID != stored.ActingOrganizationID || energyQuery.SiteID != siteID {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
			return
		}
		grant, failure := h.authorizeAnalyticsForPresenter(
			request.Context(),
			request,
			session,
			energyQuery,
			h.operations.workloadSPIFFEID,
		)
		if failure != nil {
			if failure.status == http.StatusForbidden {
				writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
			} else {
				h.writeAnalyticsFailure(writer, request, *failure)
			}
			return
		}
		issuedClaims, err := identitycontext.VerifyDelegation(
			h.identity.config.DelegationSigner.Public(),
			grant,
		)
		if err != nil || strings.TrimSpace(issuedClaims.PolicyRevision) == "" {
			writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_AUTHORIZATION_UNAVAILABLE", "Operations authorization unavailable", "The Energy authorization grant could not be verified after issuance.", true, nil)
			return
		}
		writeJSON(writer, http.StatusOK, operationsToolAuthorizationResponse{
			DelegationGrant: grant,
			PolicyRevision:  issuedClaims.PolicyRevision,
		})
		return
	}
	authorization, failure := h.authorizeRegistryForPresenter(
		request.Context(),
		session,
		registryAction,
		h.operations.workloadSPIFFEID,
	)
	if failure != nil || !operationsScopeAllows(authorization.legacyScopes, stored.ActingOrganizationID, siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	writeJSON(writer, http.StatusOK, operationsToolAuthorizationResponse{DelegationGrant: authorization.coreGrant, PolicyRevision: authorization.policyRevision})
}

func (h *handler) proxyOperationsInvestigation(writer http.ResponseWriter, request *http.Request, route publicOperationsRoute) {
	if h.operations == nil || h.operations.baseURL == "" || h.operations.httpClient == nil || h.identity == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_AGENT_UNAVAILABLE", "Operations Agent unavailable", "The Operations Agent is not configured.", true, nil)
		return
	}
	if request.URL.RawQuery != "" {
		writeProblem(writer, request, http.StatusBadRequest, "QUERY_UNSUPPORTED", "Query unsupported", "Operations Investigation routes do not accept query parameters.", false, nil)
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
	if route.mutation && !hasOperationsJSONContentType(request) {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTENT_TYPE_UNSUPPORTED", "Content type unsupported", "Operations Investigation mutations require application/json.", false, nil)
		return
	}
	idempotencyKey := ""
	if route.kind == "SUBMIT_OPERATOR_INPUT" {
		idempotencyKey = strings.TrimSpace(request.Header.Get("Idempotency-Key"))
		if idempotencyKey == "" || len(idempotencyKey) > 256 || strings.ContainsAny(idempotencyKey, "\r\n") {
			writeProblem(writer, request, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency Key required", "Operator Input submission requires a bounded Idempotency-Key header.", false, nil)
			return
		}
	}
	body := []byte(nil)
	var err error
	if route.mutation {
		body, err = readOperationsMutationBody(
			request.Body,
			request.ContentLength,
			h.operations.maxRequestBytes,
			route.kind,
		)
		if err != nil {
			status, code := http.StatusBadRequest, "REQUEST_INVALID"
			if errors.Is(err, errOperationsBodyTooLarge) {
				status, code = http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE"
			}
			writeProblem(writer, request, status, code, "Request invalid", "The Operations Investigation request body is invalid.", false, nil)
			return
		}
	}
	if !h.operations.allow(session.ID, h.now()) {
		writeProblem(writer, request, http.StatusTooManyRequests, "OPERATIONS_RATE_LIMITED", "Operations rate limited", "The Operations Investigation request rate has been exceeded.", true, nil)
		return
	}
	siteAuthorization, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if failure != nil || !operationsScopeAllows(siteAuthorization.legacyScopes, session.ActingOrganizationID, route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	serviceDelegation, err := h.operationsServiceDelegation(session, route.siteID)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_AGENT_UNAVAILABLE", "Operations Agent unavailable", "The Operations Agent delegation could not be created.", true, nil)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.operations.timeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(ctx, route.method, h.operations.baseURL+route.internalPath, bytes.NewReader(body))
	if err != nil {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent gateway failed", "The Operations Agent request could not be created.", true, nil)
		return
	}
	if route.mutation {
		upstream.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		upstream.Header.Set("Idempotency-Key", idempotencyKey)
	}
	requestedPosition := ""
	if route.kind == "STREAM" {
		upstream.Header.Set("Accept", "text/event-stream, application/problem+json")
		requestedPosition = strings.TrimSpace(request.Header.Get("Last-Event-ID"))
		if requestedPosition != "" {
			if len(requestedPosition) > 128 || strings.ContainsAny(requestedPosition, "\r\n") {
				requestedPosition = "invalid"
			}
			upstream.Header.Set("Last-Event-ID", requestedPosition)
		}
	} else {
		upstream.Header.Set("Accept", "application/json, application/problem+json")
	}
	upstream.Header.Set("X-Acting-Organization-ID", session.ActingOrganizationID)
	upstream.Header.Set("X-Delegation-Grant", serviceDelegation)
	upstream.Header.Set(
		"X-Route-Policy-Revision",
		formatRevision(routeDecisionFromContext(request.Context()).RegistryRevision),
	)
	upstream.Header.Set("X-Request-ID", requestIDFromContext(request.Context()))
	upstream.Header.Set("traceparent", traceparentFromContext(request.Context()))
	response, err := h.operations.httpClient.Do(upstream)
	if err != nil {
		status, code := http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, code = http.StatusGatewayTimeout, "OPERATIONS_AGENT_TIMEOUT"
		}
		writeProblem(writer, request, status, code, "Operations Agent unavailable", "The Operations Agent did not complete the request.", true, nil)
		return
	}
	defer response.Body.Close()
	raw, err := readBoundedBody(response.Body, h.operations.maxResponseBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent gateway failed", "The Operations Agent response was unreadable or oversized.", true, nil)
		return
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if route.kind == "STREAM" {
			recovery, recoveryErr := validateOperationsEventStream(raw, response.Header, requestedPosition)
			if !strings.HasPrefix(contentType, "text/event-stream") || recoveryErr != nil {
				writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_CONTRACT_FAILED", "Operations Agent contract failed", "The Operations Agent returned an invalid event stream.", true, nil)
				return
			}
			writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			writer.Header().Set("Cache-Control", "no-store, no-transform")
			writer.Header().Set("X-Accel-Buffering", "no")
			writer.Header().Set("X-Operations-Recovery-Mode", recovery.mode)
			writer.Header().Set("X-Operations-Recovery-Reason", recovery.reason)
			writer.Header().Set("X-Operations-Snapshot-Position", recovery.snapshotPosition)
			writer.Header().Set("X-Operations-Latest-Position", recovery.latestPosition)
			if recovery.replayFromPosition != "" {
				writer.Header().Set("X-Operations-Replay-From", recovery.replayFromPosition)
			}
			writer.WriteHeader(response.StatusCode)
			_, _ = writer.Write(raw)
			return
		}
		contractErr := validateOperationsSnapshot(raw)
		if route.kind == "LIST" {
			contractErr = validateOperationsInvestigationList(raw)
		} else if route.kind == "SUBMIT_OPERATOR_INPUT" {
			contractErr = validateOperationsOperatorInputSubmission(raw)
		}
		if !strings.HasPrefix(contentType, "application/json") || contractErr != nil {
			writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_CONTRACT_FAILED", "Operations Agent contract failed", "The Operations Agent returned an invalid authoritative projection.", true, nil)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(raw)
		return
	}
	if response.StatusCode == http.StatusNotFound {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Operations Investigation was not found.", false, nil)
		return
	}
	if strings.HasPrefix(contentType, "application/problem+json") && response.StatusCode >= 400 && response.StatusCode < 500 {
		writer.Header().Set("Content-Type", "application/problem+json")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(raw)
		return
	}
	writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent gateway failed", "The Operations Agent returned an invalid upstream response.", true, nil)
}

func (c *operationsAgentController) allow(sessionID string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	window := c.rateWindows[sessionID]
	if window.startedAt.IsZero() || now.Sub(window.startedAt) >= time.Minute {
		c.rateWindows[sessionID] = operationsRateWindow{startedAt: now, count: 1}
		return true
	}
	if window.count >= c.rateLimitPerMinute {
		return false
	}
	window.count++
	c.rateWindows[sessionID] = window
	return true
}

func operationsScopeAllows(scopes []string, organizationID, siteID string) bool {
	for _, scope := range scopes {
		if scope == "site:"+siteID || scope == "organization:"+organizationID {
			return true
		}
	}
	return false
}

func (h *handler) operationsServiceDelegation(session bffSession, siteID string) (string, error) {
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	return identitycontext.SignDelegation(h.identity.config.DelegationSigner, identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject,
		SubjectIssuer: session.Principal.Issuer, DisplayName: session.Principal.DisplayName,
		Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.operations.audience,
		ActingOrganizationID: session.ActingOrganizationID, Actions: []string{"operations:investigate"},
		Scopes: []string{"site:" + siteID, "session:" + session.ID}, PolicyRevision: h.identity.config.PolicyRevision,
		SessionID: session.ID, IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	})
}

func readOperationsMutationBody(reader io.Reader, contentLength, limit int64, kind string) ([]byte, error) {
	if contentLength > limit {
		return nil, errOperationsBodyTooLarge
	}
	raw, err := readBoundedBody(reader, limit)
	if err != nil {
		return nil, errOperationsBodyTooLarge
	}
	if kind != "SUBMIT_OPERATOR_INPUT" {
		var value map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil || len(value) != 0 || decoder.Decode(&struct{}{}) != io.EOF {
			return nil, errors.New("invalid Operations mutation body")
		}
		return []byte("{}"), nil
	}

	var value operationsSubmitOperatorInputRequest
	if err := decodeStrictOperationsJSON(raw, &value); err != nil || value.SchemaVersion != 1 {
		return nil, errors.New("invalid Operations Operator Input body")
	}
	if _, ok := operationsBoundedString(value.RequestID, 256); !ok {
		return nil, errors.New("invalid Operations Operator Input Request identity")
	}
	revision, err := strconv.ParseInt(string(value.ExpectedRevision), 10, 64)
	if err != nil || revision < 0 {
		return nil, errors.New("invalid Operations Operator Input revision")
	}
	if value.Values.AnalysisScope != "SITE_ONLY" && value.Values.AnalysisScope != "DEFER" {
		return nil, errors.New("invalid Operations Operator Input scope")
	}
	if len(value.Values.OperatorNote) == 0 {
		return nil, errors.New("missing Operations Operator Input note field")
	}
	var note any
	decoder := json.NewDecoder(bytes.NewReader(value.Values.OperatorNote))
	if err := decoder.Decode(&note); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("invalid Operations Operator Input note")
	}
	if note != nil {
		text, ok := note.(string)
		if !ok || strings.TrimSpace(text) == "" || len(text) > 500 {
			return nil, errors.New("invalid Operations Operator Input note")
		}
	}
	return raw, nil
}

func decodeStrictOperationsJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid Operations JSON payload")
	}
	return nil
}

func operationsNullableBoundedString(value any, maximum int) (string, bool) {
	if value == nil {
		return "", true
	}
	return operationsBoundedString(value, maximum)
}

func operationsDigest(value any) bool {
	text, ok := value.(string)
	if !ok || len(text) != 71 || !strings.HasPrefix(text, "sha256:") {
		return false
	}
	for _, candidate := range text[7:] {
		if (candidate < '0' || candidate > '9') && (candidate < 'a' || candidate > 'f') {
			return false
		}
	}
	return true
}

func operationsStringArray(value any, minimum, maximum int) ([]string, bool) {
	items, ok := value.([]any)
	if !ok || len(items) < minimum || len(items) > maximum {
		return nil, false
	}
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, candidate := range items {
		identity, valid := operationsBoundedString(candidate, 256)
		if !valid {
			return nil, false
		}
		if _, duplicate := seen[identity]; duplicate {
			return nil, false
		}
		seen[identity] = struct{}{}
		result = append(result, identity)
	}
	return result, true
}

func validateOperationsScopeValue(value any, siteOnly bool) error {
	scope, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(scope, "organizationId", "siteId", "equipmentId", "deviceId") {
		return errors.New("invalid Operations Scope")
	}
	if _, ok := operationsBoundedString(scope["organizationId"], 256); !ok {
		return errors.New("invalid Operations Organization Scope")
	}
	if _, ok := operationsBoundedString(scope["siteId"], 256); !ok {
		return errors.New("invalid Operations Site Scope")
	}
	if siteOnly {
		if scope["equipmentId"] != nil || scope["deviceId"] != nil {
			return errors.New("Operations Investigation Scope must remain Site-only")
		}
		return nil
	}
	if _, ok := operationsNullableBoundedString(scope["equipmentId"], 256); !ok {
		return errors.New("invalid Operations Equipment Scope")
	}
	if _, ok := operationsNullableBoundedString(scope["deviceId"], 256); !ok {
		return errors.New("invalid Operations Device Scope")
	}
	return nil
}

func operationsScopesEqual(left, right map[string]any) bool {
	return left["organizationId"] == right["organizationId"] &&
		left["siteId"] == right["siteId"] &&
		left["equipmentId"] == right["equipmentId"] &&
		left["deviceId"] == right["deviceId"]
}

func validateOperationsRecordBase(record map[string]any, recordType, investigationID string) error {
	if record["schemaVersion"] != json.Number("1") || record["recordType"] != recordType {
		return errors.New("invalid Operations business record identity")
	}
	if _, ok := operationsBoundedString(record["id"], 256); !ok || record["investigationId"] != investigationID {
		return errors.New("Operations business record does not match the Investigation")
	}
	if _, ok := operationsNonnegativeInteger(record["recordedAt"]); !ok {
		return errors.New("invalid Operations business record timestamp")
	}
	return nil
}

func validateOperationsEvidenceQuality(value any) error {
	quality, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(quality, "classification", "valid", "suspect", "invalid") ||
		!operationsAllowedString(quality["classification"], "GOOD", "UNCERTAIN", "BAD", "STALE") {
		return errors.New("invalid Operations Evidence quality")
	}
	for _, key := range []string{"valid", "suspect", "invalid"} {
		if count, ok := operationsNonnegativeInteger(quality[key]); !ok || count > 1_000_000 {
			return errors.New("invalid Operations Evidence quality count")
		}
	}
	return nil
}

func validateOperationsEvidenceSource(value any, organizationID, siteID string) (string, error) {
	source, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(source,
		"owner", "scope", "requestId", "registryRevision", "datasetRevision", "watermark",
		"partial", "quality", "capturedAt", "evaluatedAt", "provenanceDigest",
	) {
		return "", errors.New("invalid Operations Evidence source shape")
	}
	owner, ok := source["owner"].(string)
	if !ok || (owner != "registry" && owner != "telemetry-query-service") || validateOperationsScopeValue(source["scope"], false) != nil {
		return "", errors.New("invalid Operations Evidence source Owner or Scope")
	}
	sourceScope := source["scope"].(map[string]any)
	if sourceScope["organizationId"] != organizationID || sourceScope["siteId"] != siteID {
		return "", errors.New("Operations Evidence source exceeds Investigation Scope")
	}
	requestID, ok := operationsBoundedString(source["requestId"], 256)
	if !ok {
		return "", errors.New("invalid Operations Evidence request identity")
	}
	registryRevision, registryOK := operationsNullableBoundedString(source["registryRevision"], 256)
	datasetRevision, datasetOK := operationsNullableBoundedString(source["datasetRevision"], 256)
	if !registryOK || !datasetOK || (owner == "registry" && registryRevision == "") || (owner == "telemetry-query-service" && datasetRevision == "") {
		return "", errors.New("Operations Evidence source lacks its Owner revision")
	}
	watermark, ok := source["watermark"].(map[string]any)
	if !ok || !operationsExactKeys(watermark, "data", "aggregate") {
		return "", errors.New("invalid Operations Evidence watermark")
	}
	dataWatermark, dataOK := operationsNullableBoundedString(watermark["data"], 256)
	aggregateWatermark, aggregateOK := operationsNullableBoundedString(watermark["aggregate"], 256)
	if !dataOK || !aggregateOK || (owner == "telemetry-query-service" && dataWatermark == "" && aggregateWatermark == "") {
		return "", errors.New("Operations telemetry Evidence lacks a Watermark")
	}
	if _, ok := source["partial"].(bool); !ok || validateOperationsEvidenceQuality(source["quality"]) != nil || !operationsDigest(source["provenanceDigest"]) {
		return "", errors.New("invalid Operations Evidence provenance")
	}
	capturedAt, capturedOK := operationsNonnegativeInteger(source["capturedAt"])
	evaluatedAt, evaluatedOK := operationsNonnegativeInteger(source["evaluatedAt"])
	if !capturedOK || !evaluatedOK || evaluatedAt < capturedAt {
		return "", errors.New("invalid Operations Evidence provenance timing")
	}
	return owner + ":" + requestID, nil
}

func validateOperationsEvidenceRecord(value any, investigationID, organizationID, siteID string) (string, error) {
	record, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(record,
		"schemaVersion", "recordType", "id", "investigationId", "recordedAt", "evidenceKind",
		"classification", "statement", "analysisReferenceDigest", "sources",
	) || validateOperationsRecordBase(record, "EVIDENCE", investigationID) != nil {
		return "", errors.New("invalid Operations Evidence record")
	}
	if !operationsAllowedString(record["evidenceKind"], "SITE_ENERGY_SERIES_READY", "SITE_ENERGY_SERIES_READINESS_ASSESSED", "SITE_ENERGY_PERIOD_COMPARISON") ||
		!operationsAllowedString(record["classification"], "FACT", "ALGORITHM_RESULT") {
		return "", errors.New("invalid Operations Evidence classification")
	}
	if _, ok := operationsBoundedString(record["statement"], 4_000); !ok {
		return "", errors.New("invalid Operations Evidence statement")
	}
	if record["analysisReferenceDigest"] != nil && !operationsDigest(record["analysisReferenceDigest"]) {
		return "", errors.New("invalid Operations Evidence analysis digest")
	}
	sources, ok := record["sources"].([]any)
	if !ok || len(sources) == 0 || len(sources) > 8 {
		return "", errors.New("invalid Operations Evidence source count")
	}
	seen := make(map[string]struct{}, len(sources))
	for _, candidate := range sources {
		identity, err := validateOperationsEvidenceSource(candidate, organizationID, siteID)
		if err != nil {
			return "", err
		}
		if _, duplicate := seen[identity]; duplicate {
			return "", errors.New("duplicate Operations Evidence source")
		}
		seen[identity] = struct{}{}
	}
	id, _ := record["id"].(string)
	return id, nil
}

func validateOperationsAnalysisReference(value any, investigationID string) (string, error) {
	record, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(record,
		"schemaVersion", "recordType", "id", "investigationId", "recordedAt", "analysisKind",
		"authority", "algorithmVersion", "policyVersion", "inputEvidenceIds", "parameterDigest",
		"resultDigest", "executedAt", "outcome",
	) || validateOperationsRecordBase(record, "ANALYSIS_REFERENCE", investigationID) != nil {
		return "", errors.New("invalid Operations Analysis Reference")
	}
	if record["analysisKind"] != "SITE_NIGHT_ENERGY_COMPARISON" || record["authority"] != "DETERMINISTIC_ALGORITHM" ||
		!operationsAllowedString(record["outcome"], "SUPPORTED_SITE_FINDING", "UNABLE_TO_CONCLUDE") {
		return "", errors.New("invalid Operations Analysis authority or outcome")
	}
	if _, ok := operationsBoundedString(record["algorithmVersion"], 128); !ok {
		return "", errors.New("invalid Operations Analysis algorithm")
	}
	if _, ok := operationsBoundedString(record["policyVersion"], 128); !ok {
		return "", errors.New("invalid Operations Analysis policy")
	}
	if _, ok := operationsStringArray(record["inputEvidenceIds"], 1, 32); !ok || !operationsDigest(record["parameterDigest"]) || !operationsDigest(record["resultDigest"]) {
		return "", errors.New("invalid Operations Analysis references or digest")
	}
	if _, ok := operationsNonnegativeInteger(record["executedAt"]); !ok {
		return "", errors.New("invalid Operations Analysis timestamp")
	}
	id, _ := record["id"].(string)
	return id, nil
}

func validateOperationsRequiredNextPeriod(value any) error {
	period, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(period, "localDate", "from", "to", "expectedBuckets") {
		return errors.New("invalid Operations required-next period")
	}
	if _, ok := operationsBoundedString(period["localDate"], 32); !ok {
		return errors.New("invalid Operations required-next local date")
	}
	if _, ok := operationsBoundedString(period["from"], 64); !ok {
		return errors.New("invalid Operations required-next period start")
	}
	if _, ok := operationsBoundedString(period["to"], 64); !ok {
		return errors.New("invalid Operations required-next period end")
	}
	buckets, ok := operationsNonnegativeInteger(period["expectedBuckets"])
	if !ok || buckets < 1 || buckets > 48 {
		return errors.New("invalid Operations required-next bucket count")
	}
	return nil
}

func validateOperationsRequiredNext(value any, organizationID, siteID string) error {
	requirement, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(requirement,
		"status", "kind", "owner", "capability", "organizationId", "siteId", "equipmentIds",
		"targetPeriod", "baselinePeriod", "requiredMetadata",
	) || requirement["status"] != "REQUIRED_NEXT" || requirement["organizationId"] != organizationID || requirement["siteId"] != siteID {
		return errors.New("invalid Operations required-next Scope")
	}
	if _, ok := operationsStringArray(requirement["equipmentIds"], 0, 32); !ok ||
		validateOperationsRequiredNextPeriod(requirement["targetPeriod"]) != nil ||
		validateOperationsRequiredNextPeriod(requirement["baselinePeriod"]) != nil {
		return errors.New("invalid Operations required-next Evidence period")
	}
	metadata, ok := requirement["requiredMetadata"].([]any)
	if !ok {
		return errors.New("invalid Operations required-next metadata")
	}
	var expected []string
	switch requirement["kind"] {
	case "EQUIPMENT_ENERGY_BINDINGS":
		if requirement["owner"] != "registry" || requirement["capability"] != "registry.getEquipmentEnergyBindings" {
			return errors.New("invalid Operations Registry required-next capability")
		}
		expected = []string{"BUSINESS_REVISION", "QUALITY", "CAPTURED_AT", "PAYLOAD_DIGEST"}
	case "EQUIPMENT_ENERGY_PERIOD_COMPARISON":
		if requirement["owner"] != "telemetry-query-service" || requirement["capability"] != "analytics.energy.getEquipmentSeries" {
			return errors.New("invalid Operations telemetry required-next capability")
		}
		expected = []string{"DATASET_REVISION", "WATERMARK", "PARTIAL", "QUALITY", "CAPTURED_AT", "PAYLOAD_DIGEST"}
	default:
		return errors.New("invalid Operations required-next kind")
	}
	if len(metadata) != len(expected) {
		return errors.New("invalid Operations required-next metadata count")
	}
	for index, expectedValue := range expected {
		if metadata[index] != expectedValue {
			return errors.New("invalid Operations required-next metadata order")
		}
	}
	return nil
}

func validateOperationsFindingRecord(value any, investigationID, organizationID, siteID string) (string, error) {
	record, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(record,
		"schemaVersion", "recordType", "id", "investigationId", "recordedAt", "findingKind",
		"classification", "statement", "evidenceIds", "analysisReferenceIds", "conclusion",
	) || validateOperationsRecordBase(record, "FINDING", investigationID) != nil || record["classification"] != "INFERENCE" {
		return "", errors.New("invalid Operations Finding")
	}
	if !operationsAllowedString(record["findingKind"], "SITE_NIGHT_ENERGY_INCREASE", "SITE_NIGHT_ENERGY_WITHIN_THRESHOLD", "UNABLE_TO_CONCLUDE") {
		return "", errors.New("invalid Operations Finding kind")
	}
	if _, ok := operationsBoundedString(record["statement"], 4_000); !ok {
		return "", errors.New("invalid Operations Finding statement")
	}
	if _, ok := operationsStringArray(record["evidenceIds"], 0, 32); !ok {
		return "", errors.New("invalid Operations Finding Evidence identities")
	}
	if _, ok := operationsStringArray(record["analysisReferenceIds"], 0, 32); !ok {
		return "", errors.New("invalid Operations Finding Analysis identities")
	}
	conclusion, ok := record["conclusion"].(map[string]any)
	if !ok {
		return "", errors.New("invalid Operations Finding conclusion")
	}
	switch conclusion["status"] {
	case "SUPPORTED":
		if !operationsExactKeys(conclusion, "status", "scope", "organizationId", "siteId") || conclusion["scope"] != "SITE" ||
			conclusion["organizationId"] != organizationID || conclusion["siteId"] != siteID || record["findingKind"] == "UNABLE_TO_CONCLUDE" {
			return "", errors.New("Operations supported Finding exceeds Site authority")
		}
	case "UNABLE_TO_CONCLUDE":
		legacy := operationsExactKeys(conclusion, "status", "scope", "reasonCode", "detail")
		withRequirements := operationsExactKeys(conclusion, "status", "scope", "reasonCode", "detail", "requiredNext")
		if (!legacy && !withRequirements) || !operationsAllowedString(conclusion["scope"], "SITE", "EQUIPMENT") || record["findingKind"] != "UNABLE_TO_CONCLUDE" {
			return "", errors.New("invalid Operations unable-to-conclude Finding")
		}
		if _, ok := operationsBoundedString(conclusion["reasonCode"], 128); !ok {
			return "", errors.New("invalid Operations Finding reason")
		}
		if _, ok := operationsBoundedString(conclusion["detail"], 4_000); !ok {
			return "", errors.New("invalid Operations Finding detail")
		}
		if withRequirements {
			requirements, ok := conclusion["requiredNext"].([]any)
			if !ok || len(requirements) == 0 || len(requirements) > 8 {
				return "", errors.New("invalid Operations required-next count")
			}
			seen := make(map[string]struct{}, len(requirements))
			for _, candidate := range requirements {
				if err := validateOperationsRequiredNext(candidate, organizationID, siteID); err != nil {
					return "", err
				}
				requirement := candidate.(map[string]any)
				identity := requirement["owner"].(string) + ":" + requirement["kind"].(string)
				if _, duplicate := seen[identity]; duplicate {
					return "", errors.New("duplicate Operations required-next Evidence")
				}
				seen[identity] = struct{}{}
			}
		}
	default:
		return "", errors.New("unsupported Operations Finding conclusion")
	}
	id, _ := record["id"].(string)
	return id, nil
}

func validateOperationsOperatorInputRequest(
	value any,
	investigationID string,
	scope map[string]any,
	activeRun map[string]any,
) (string, error) {
	request, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(request,
		"schemaVersion", "id", "investigationId", "runId", "scope", "kind", "requestedAt",
		"requestedBy", "policyVersion", "fields",
	) || request["schemaVersion"] != json.Number("1") || request["investigationId"] != investigationID ||
		request["kind"] != "SITE_NIGHT_ENERGY_SCOPE_CONFIRMATION" ||
		request["requestedBy"] != "DETERMINISTIC_POLICY" ||
		request["policyVersion"] != "operator-input-policy/v1" {
		return "", errors.New("invalid Operations Operator Input Request")
	}
	requestID, requestOK := operationsBoundedString(request["id"], 256)
	runID, runOK := operationsBoundedString(request["runId"], 256)
	if !requestOK || !runOK || activeRun["id"] != runID || activeRun["status"] != "WAITING_FOR_OPERATOR_INPUT" {
		return "", errors.New("invalid Operations Operator Input Request identity")
	}
	requestScope, ok := request["scope"].(map[string]any)
	if !ok || validateOperationsScopeValue(requestScope, true) != nil || !operationsScopesEqual(requestScope, scope) {
		return "", errors.New("invalid Operations Operator Input Request Scope")
	}
	if _, ok := operationsNonnegativeInteger(request["requestedAt"]); !ok {
		return "", errors.New("invalid Operations Operator Input Request timestamp")
	}
	fields, ok := request["fields"].([]any)
	if !ok || len(fields) != 2 {
		return "", errors.New("invalid Operations Operator Input fields")
	}
	selectField, ok := fields[0].(map[string]any)
	if !ok || !operationsExactKeys(selectField, "id", "type", "required", "options") ||
		selectField["id"] != "analysisScope" || selectField["type"] != "SINGLE_SELECT" || selectField["required"] != true {
		return "", errors.New("invalid Operations Operator Input select field")
	}
	options, ok := selectField["options"].([]any)
	if !ok || len(options) != 2 || options[0] != "SITE_ONLY" || options[1] != "DEFER" {
		return "", errors.New("invalid Operations Operator Input options")
	}
	textField, ok := fields[1].(map[string]any)
	if !ok || !operationsExactKeys(textField, "id", "type", "required", "maximumLength") ||
		textField["id"] != "operatorNote" || textField["type"] != "SHORT_TEXT" || textField["required"] != false ||
		textField["maximumLength"] != json.Number("500") {
		return "", errors.New("invalid Operations Operator Input text field")
	}
	return requestID, nil
}

type operationsAcceptedOperatorInputIdentity struct {
	recordID       string
	requestID      string
	idempotencyKey string
}

func validateOperationsAcceptedOperatorInputRecord(
	value any,
	investigationID string,
	scope map[string]any,
) (operationsAcceptedOperatorInputIdentity, error) {
	record, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(record,
		"schemaVersion", "recordType", "id", "investigationId", "recordedAt", "requestId", "runId",
		"idempotencyKey", "inputKind", "inputDigest", "scope", "values", "provenance",
	) || validateOperationsRecordBase(record, "OPERATOR_INPUT_ACCEPTED", investigationID) != nil ||
		record["inputKind"] != "SITE_NIGHT_ENERGY_SCOPE_CONFIRMATION" || !operationsDigest(record["inputDigest"]) {
		return operationsAcceptedOperatorInputIdentity{}, errors.New("invalid accepted Operations Operator Input")
	}
	recordID, recordOK := operationsBoundedString(record["id"], 256)
	requestID, requestOK := operationsBoundedString(record["requestId"], 256)
	_, runOK := operationsBoundedString(record["runId"], 256)
	idempotencyKey, idempotencyOK := operationsBoundedString(record["idempotencyKey"], 256)
	if !recordOK || !requestOK || !runOK || !idempotencyOK {
		return operationsAcceptedOperatorInputIdentity{}, errors.New("invalid accepted Operations Operator Input identity")
	}
	recordScope, ok := record["scope"].(map[string]any)
	if !ok || validateOperationsScopeValue(recordScope, true) != nil || !operationsScopesEqual(recordScope, scope) {
		return operationsAcceptedOperatorInputIdentity{}, errors.New("invalid accepted Operations Operator Input Scope")
	}
	values, ok := record["values"].(map[string]any)
	if !ok || !operationsExactKeys(values, "analysisScope", "operatorNote") ||
		!operationsAllowedString(values["analysisScope"], "SITE_ONLY", "DEFER") {
		return operationsAcceptedOperatorInputIdentity{}, errors.New("invalid accepted Operations Operator Input values")
	}
	if values["operatorNote"] != nil {
		if _, ok := operationsBoundedString(values["operatorNote"], 500); !ok {
			return operationsAcceptedOperatorInputIdentity{}, errors.New("invalid accepted Operations Operator Input note")
		}
	}
	provenance, ok := record["provenance"].(map[string]any)
	if !ok || !operationsExactKeys(provenance,
		"actorType", "source", "authorizationDecisionId", "policyRevision", "submittedAt",
	) || provenance["actorType"] != "OPERATOR" || provenance["source"] != "PLATFORM_GATEWAY" {
		return operationsAcceptedOperatorInputIdentity{}, errors.New("invalid accepted Operations Operator Input provenance")
	}
	if _, ok := operationsBoundedString(provenance["authorizationDecisionId"], 256); !ok {
		return operationsAcceptedOperatorInputIdentity{}, errors.New("invalid accepted Operations Operator Input authorization decision")
	}
	if _, ok := operationsBoundedString(provenance["policyRevision"], 256); !ok {
		return operationsAcceptedOperatorInputIdentity{}, errors.New("invalid accepted Operations Operator Input policy revision")
	}
	recordedAt, recordedOK := operationsNonnegativeInteger(record["recordedAt"])
	submittedAt, submittedOK := operationsNonnegativeInteger(provenance["submittedAt"])
	if !recordedOK || !submittedOK || submittedAt != recordedAt {
		return operationsAcceptedOperatorInputIdentity{}, errors.New("invalid accepted Operations Operator Input timestamp")
	}
	return operationsAcceptedOperatorInputIdentity{
		recordID: recordID, requestID: requestID, idempotencyKey: idempotencyKey,
	}, nil
}

func validateOperationsToolReceiptRecord(value any, investigationID string) (string, error) {
	record, ok := value.(map[string]any)
	if !ok || !operationsExactKeys(record,
		"schemaVersion", "recordType", "id", "investigationId", "recordedAt", "logicalTool", "owner",
		"requestId", "attemptId", "runId", "stepId", "startedAt", "completedAt", "resultCategory", "metadata",
	) || validateOperationsRecordBase(record, "TOOL_EXECUTION_RECEIPT", investigationID) != nil {
		return "", errors.New("invalid Operations Tool Receipt")
	}
	tool, toolOK := record["logicalTool"].(string)
	owner, ownerOK := record["owner"].(string)
	expectedOwner := map[string]string{
		"registry.getSite": "registry", "registry.listSiteEquipment": "registry",
		"telemetry.getCurrentSnapshot": "telemetry-query-service", "analytics.getEnergySeries": "telemetry-query-service",
		"commands.getCapabilities": "command-service",
	}[tool]
	if !toolOK || !ownerOK || expectedOwner == "" || owner != expectedOwner ||
		!operationsAllowedString(record["resultCategory"], "SUCCEEDED", "REJECTED", "TIMED_OUT", "FAILED") {
		return "", errors.New("invalid Operations Tool Receipt identity")
	}
	for _, key := range []string{"requestId", "attemptId", "runId"} {
		if _, ok := operationsBoundedString(record[key], 256); !ok {
			return "", errors.New("invalid Operations Tool Receipt execution identity")
		}
	}
	if _, ok := operationsBoundedString(record["stepId"], 128); !ok {
		return "", errors.New("invalid Operations Tool Receipt Step")
	}
	startedAt, startedOK := operationsNonnegativeInteger(record["startedAt"])
	completedAt, completedOK := operationsNonnegativeInteger(record["completedAt"])
	if !startedOK || !completedOK || completedAt < startedAt {
		return "", errors.New("invalid Operations Tool Receipt timing")
	}
	metadata, ok := record["metadata"].(map[string]any)
	if !ok || len(metadata) > 32 {
		return "", errors.New("invalid Operations Tool Receipt metadata")
	}
	for key, candidate := range metadata {
		lower := strings.ToLower(key)
		if strings.TrimSpace(key) == "" || len(key) > 128 || strings.Contains(lower, "secret") || strings.Contains(lower, "token") ||
			strings.Contains(lower, "authorization") || strings.Contains(lower, "cookie") || strings.Contains(lower, "prompt") ||
			strings.Contains(lower, "payload") || strings.Contains(lower, "response") || strings.Contains(lower, "body") || strings.Contains(lower, "raw") {
			return "", errors.New("unsafe Operations Tool Receipt metadata key")
		}
		switch typed := candidate.(type) {
		case nil, bool:
		case string:
			if len(typed) > 512 {
				return "", errors.New("oversized Operations Tool Receipt metadata")
			}
		case json.Number:
			if _, err := strconv.ParseFloat(string(typed), 64); err != nil {
				return "", errors.New("invalid Operations Tool Receipt numeric metadata")
			}
		default:
			return "", errors.New("nested Operations Tool Receipt metadata is forbidden")
		}
	}
	id, _ := record["id"].(string)
	return id, nil
}

func validateOperationsRecordArray(value any, maximum int, validate func(any) (string, error)) error {
	items, ok := value.([]any)
	if !ok || len(items) > maximum {
		return errors.New("invalid Operations record array")
	}
	seen := make(map[string]struct{}, len(items))
	for _, candidate := range items {
		identity, err := validate(candidate)
		if err != nil {
			return err
		}
		if _, duplicate := seen[identity]; duplicate {
			return errors.New("duplicate Operations business record identity")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func validateOperationsInvestigationValue(root map[string]any, includeToolReceipts bool) error {
	expected := []string{
		"schemaVersion", "id", "scope", "status", "revision", "createdAt", "activeRun", "outcome",
		"evidence", "analysisReferences", "findings", "operatorInputRequest", "acceptedOperatorInputs",
	}
	if includeToolReceipts {
		expected = append(expected, "toolReceipts")
	}
	if !operationsExactKeys(root, expected...) || root["schemaVersion"] != json.Number("1") {
		return errors.New("invalid Operations Investigation shape")
	}
	investigationID, ok := operationsBoundedString(root["id"], 256)
	if !ok || validateOperationsScopeValue(root["scope"], true) != nil ||
		!operationsAllowedString(root["status"], "DRAFT", "RUNNING", "PAUSED", "WAITING_FOR_OPERATOR_INPUT", "COMPLETED", "FAILED", "CANCELLED") {
		return errors.New("invalid Operations Investigation identity or Scope")
	}
	if _, ok := operationsNonnegativeInteger(root["revision"]); !ok {
		return errors.New("invalid Operations Investigation revision")
	}
	if _, ok := operationsNonnegativeInteger(root["createdAt"]); !ok {
		return errors.New("invalid Operations Investigation creation timestamp")
	}
	var activeRun map[string]any
	if root["activeRun"] != nil {
		run, ok := root["activeRun"].(map[string]any)
		if !ok || !operationsExactKeys(run, "id", "status", "startedAt") ||
			!operationsAllowedString(run["status"], "ACTIVE", "PAUSED", "WAITING_FOR_OPERATOR_INPUT") {
			return errors.New("invalid Operations active Agent Run")
		}
		if _, ok := operationsBoundedString(run["id"], 256); !ok {
			return errors.New("invalid Operations active Agent Run identity")
		}
		if _, ok := operationsNonnegativeInteger(run["startedAt"]); !ok {
			return errors.New("invalid Operations active Agent Run timestamp")
		}
		activeRun = run
	}
	if root["status"] == "WAITING_FOR_OPERATOR_INPUT" {
		if activeRun == nil || activeRun["status"] != "WAITING_FOR_OPERATOR_INPUT" {
			return errors.New("Operations waiting Investigation requires one waiting Agent Run")
		}
	} else if activeRun != nil && activeRun["status"] == "WAITING_FOR_OPERATOR_INPUT" {
		return errors.New("Operations waiting Agent Run requires a waiting Investigation")
	}
	if root["outcome"] != nil && !operationsAllowedString(root["outcome"], "SUPPORTED_SITE_FINDING", "UNABLE_TO_CONCLUDE") {
		return errors.New("invalid Operations Investigation outcome")
	}
	scope := root["scope"].(map[string]any)
	organizationID := scope["organizationId"].(string)
	siteID := scope["siteId"].(string)
	activeRequestID := ""
	if root["operatorInputRequest"] != nil {
		if root["status"] != "WAITING_FOR_OPERATOR_INPUT" || activeRun == nil {
			return errors.New("Operations Operator Input Request requires a waiting Investigation")
		}
		requestID, err := validateOperationsOperatorInputRequest(
			root["operatorInputRequest"], investigationID, scope, activeRun,
		)
		if err != nil {
			return err
		}
		activeRequestID = requestID
	} else if root["status"] == "WAITING_FOR_OPERATOR_INPUT" {
		return errors.New("Operations waiting Investigation is missing its Operator Input Request")
	}
	acceptedInputs, ok := root["acceptedOperatorInputs"].([]any)
	if !ok || len(acceptedInputs) > 32 {
		return errors.New("invalid accepted Operations Operator Input array")
	}
	acceptedRecordIDs := make(map[string]struct{}, len(acceptedInputs))
	acceptedRequestIDs := make(map[string]struct{}, len(acceptedInputs))
	acceptedIdempotencyKeys := make(map[string]struct{}, len(acceptedInputs))
	for _, candidate := range acceptedInputs {
		identity, err := validateOperationsAcceptedOperatorInputRecord(candidate, investigationID, scope)
		if err != nil {
			return err
		}
		if _, duplicate := acceptedRecordIDs[identity.recordID]; duplicate {
			return errors.New("duplicate accepted Operations Operator Input record")
		}
		if _, duplicate := acceptedRequestIDs[identity.requestID]; duplicate {
			return errors.New("duplicate accepted Operations Operator Input Request")
		}
		if _, duplicate := acceptedIdempotencyKeys[identity.idempotencyKey]; duplicate {
			return errors.New("duplicate accepted Operations Operator Input Idempotency Key")
		}
		acceptedRecordIDs[identity.recordID] = struct{}{}
		acceptedRequestIDs[identity.requestID] = struct{}{}
		acceptedIdempotencyKeys[identity.idempotencyKey] = struct{}{}
	}
	if activeRequestID != "" {
		if _, alreadyAccepted := acceptedRequestIDs[activeRequestID]; alreadyAccepted {
			return errors.New("active Operations Operator Input Request was already accepted")
		}
	}
	if err := validateOperationsRecordArray(root["evidence"], 32, func(candidate any) (string, error) {
		return validateOperationsEvidenceRecord(candidate, investigationID, organizationID, siteID)
	}); err != nil {
		return err
	}
	if err := validateOperationsRecordArray(root["analysisReferences"], 32, func(candidate any) (string, error) {
		return validateOperationsAnalysisReference(candidate, investigationID)
	}); err != nil {
		return err
	}
	if err := validateOperationsRecordArray(root["findings"], 32, func(candidate any) (string, error) {
		return validateOperationsFindingRecord(candidate, investigationID, organizationID, siteID)
	}); err != nil {
		return err
	}
	evidenceIDs := make(map[string]struct{})
	for _, candidate := range root["evidence"].([]any) {
		record := candidate.(map[string]any)
		evidenceIDs[record["id"].(string)] = struct{}{}
	}
	analysisIDs := make(map[string]struct{})
	for _, candidate := range root["analysisReferences"].([]any) {
		record := candidate.(map[string]any)
		analysisIDs[record["id"].(string)] = struct{}{}
		for _, evidenceID := range record["inputEvidenceIds"].([]any) {
			if _, exists := evidenceIDs[evidenceID.(string)]; !exists {
				return errors.New("Operations Analysis references unknown Evidence")
			}
		}
	}
	for _, candidate := range root["findings"].([]any) {
		record := candidate.(map[string]any)
		for _, evidenceID := range record["evidenceIds"].([]any) {
			if _, exists := evidenceIDs[evidenceID.(string)]; !exists {
				return errors.New("Operations Finding references unknown Evidence")
			}
		}
		for _, analysisID := range record["analysisReferenceIds"].([]any) {
			if _, exists := analysisIDs[analysisID.(string)]; !exists {
				return errors.New("Operations Finding references unknown Analysis")
			}
		}
	}
	if includeToolReceipts {
		if err := validateOperationsRecordArray(root["toolReceipts"], 64, func(candidate any) (string, error) {
			return validateOperationsToolReceiptRecord(candidate, investigationID)
		}); err != nil {
			return err
		}
	}
	return nil
}

func inspectOperationsSnapshotPayload(value any) error {
	forbidden := map[string]struct{}{"lease": {}, "leaseHistory": {}, "checkpoint": {}, "opaqueState": {}, "runtimeRevision": {}, "providerMessage": {}, "points": {}}
	var inspect func(any) error
	inspect = func(candidate any) error {
		switch typed := candidate.(type) {
		case map[string]any:
			for key, nested := range typed {
				if _, denied := forbidden[key]; denied {
					return errors.New("Operations snapshot exposes internal state")
				}
				if err := inspect(nested); err != nil {
					return err
				}
			}
		case []any:
			for _, nested := range typed {
				if err := inspect(nested); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return inspect(value)
}

func validateOperationsSnapshot(raw []byte) error {
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid Operations snapshot JSON")
	}
	if err := validateOperationsInvestigationValue(root, true); err != nil {
		return err
	}
	return inspectOperationsSnapshotPayload(root)
}

func validateOperationsOperatorInputSubmission(raw []byte) error {
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		!operationsExactKeys(root, "outcome", "investigation") ||
		!operationsAllowedString(root["outcome"], "COMMITTED", "DUPLICATE") {
		return errors.New("invalid Operations Operator Input submission response")
	}
	investigation, ok := root["investigation"].(map[string]any)
	if !ok {
		return errors.New("missing Operations Operator Input Investigation")
	}
	if err := validateOperationsInvestigationValue(investigation, true); err != nil {
		return err
	}
	return inspectOperationsSnapshotPayload(root)
}

func validateOperationsInvestigationList(raw []byte) error {
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !operationsExactKeys(root, "schemaVersion", "investigations") || root["schemaVersion"] != json.Number("1") {
		return errors.New("invalid Operations Investigation list")
	}
	investigations, ok := root["investigations"].([]any)
	if !ok || len(investigations) > 50 {
		return errors.New("invalid Operations Investigation list size")
	}
	var previousCreated int64 = 1<<63 - 1
	previousID := ""
	for index, candidate := range investigations {
		item, ok := candidate.(map[string]any)
		if !ok || !operationsExactKeys(item,
			"schemaVersion", "id", "scope", "status", "revision", "createdAt", "outcome",
			"evidenceCount", "analysisReferenceCount", "findingCount", "toolReceiptCount",
			"acceptedOperatorInputCount",
		) || item["schemaVersion"] != json.Number("1") {
			return errors.New("invalid Operations Investigation summary")
		}
		id, idOK := operationsBoundedString(item["id"], 256)
		scope, scopeOK := item["scope"].(map[string]any)
		if !idOK || !scopeOK || !operationsExactKeys(scope, "organizationId", "siteId", "equipmentId", "deviceId") {
			return errors.New("invalid Operations Investigation summary Scope")
		}
		if _, ok := operationsBoundedString(scope["organizationId"], 256); !ok {
			return errors.New("invalid Operations Investigation Organization")
		}
		if _, ok := operationsBoundedString(scope["siteId"], 256); !ok || scope["equipmentId"] != nil || scope["deviceId"] != nil {
			return errors.New("invalid Operations Investigation Site Scope")
		}
		if !operationsAllowedString(item["status"], "DRAFT", "RUNNING", "PAUSED", "WAITING_FOR_OPERATOR_INPUT", "COMPLETED", "FAILED", "CANCELLED") {
			return errors.New("invalid Operations Investigation status")
		}
		outcome := item["outcome"]
		if outcome != nil && !operationsAllowedString(outcome, "SUPPORTED_SITE_FINDING", "UNABLE_TO_CONCLUDE") {
			return errors.New("invalid Operations Investigation outcome")
		}
		createdAt, createdOK := operationsNonnegativeInteger(item["createdAt"])
		if _, ok := operationsNonnegativeInteger(item["revision"]); !ok || !createdOK {
			return errors.New("invalid Operations Investigation summary revision")
		}
		maximumCounts := map[string]int64{
			"evidenceCount": 32, "analysisReferenceCount": 32, "findingCount": 32,
			"toolReceiptCount": 64, "acceptedOperatorInputCount": 32,
		}
		for countKey, maximum := range maximumCounts {
			count, ok := operationsNonnegativeInteger(item[countKey])
			if !ok || count > maximum {
				return errors.New("invalid Operations Investigation summary count")
			}
		}
		if createdAt > previousCreated || (createdAt == previousCreated && index > 0 && id > previousID) {
			return errors.New("Operations Investigation list order is unstable")
		}
		previousCreated, previousID = createdAt, id
	}
	return nil
}

func operationsExactKeys(value map[string]any, expected ...string) bool {
	if len(value) != len(expected) {
		return false
	}
	for _, key := range expected {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func operationsBoundedString(value any, maximum int) (string, bool) {
	text, ok := value.(string)
	return text, ok && strings.TrimSpace(text) != "" && len(text) <= maximum
}

func operationsAllowedString(value any, allowed ...string) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	for _, candidate := range allowed {
		if text == candidate {
			return true
		}
	}
	return false
}

func operationsNonnegativeInteger(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseInt(string(number), 10, 64)
	return parsed, err == nil && parsed >= 0
}

func validateOperationsToolActivity(activity map[string]any) error {
	if !operationsExactKeys(activity, "recordId", "logicalTool", "owner", "resultCategory", "startedAt", "completedAt") {
		return errors.New("invalid Operations Tool activity shape")
	}
	if _, ok := operationsBoundedString(activity["recordId"], 256); !ok ||
		!operationsAllowedString(activity["logicalTool"], "registry.getSite", "registry.listSiteEquipment", "telemetry.getCurrentSnapshot", "analytics.getEnergySeries", "commands.getCapabilities") ||
		!operationsAllowedString(activity["owner"], "registry", "telemetry-query-service", "command-service") ||
		!operationsAllowedString(activity["resultCategory"], "SUCCEEDED", "REJECTED", "TIMED_OUT", "FAILED") {
		return errors.New("invalid Operations Tool activity identity")
	}
	startedAt, startedOK := operationsNonnegativeInteger(activity["startedAt"])
	completedAt, completedOK := operationsNonnegativeInteger(activity["completedAt"])
	if !startedOK || !completedOK || completedAt < startedAt {
		return errors.New("invalid Operations Tool activity timing")
	}
	return nil
}

func validateOperationsPlan(plan map[string]any) error {
	if !operationsExactKeys(plan, "schemaVersion", "id", "label", "completedSteps", "totalSteps", "progressPercent", "steps") ||
		plan["schemaVersion"] != json.Number("1") || plan["id"] != "site-night-energy-investigation" ||
		plan["label"] != "Site night-energy investigation" || plan["totalSteps"] != json.Number("4") {
		return errors.New("invalid Operations plan projection")
	}
	completedSteps, completedOK := operationsNonnegativeInteger(plan["completedSteps"])
	progressPercent, progressOK := operationsNonnegativeInteger(plan["progressPercent"])
	steps, ok := plan["steps"].([]any)
	if !completedOK || completedSteps > 4 || !progressOK || progressPercent > 100 || !ok || len(steps) != 4 {
		return errors.New("invalid Operations plan progress")
	}
	expectedIDs := [...]string{"READ_SITE_CONTEXT", "READ_ENERGY_SERIES", "ANALYZE", "COMMIT_RESULT"}
	completedCount := int64(0)
	for index, candidate := range steps {
		step, ok := candidate.(map[string]any)
		if !ok || !operationsExactKeys(step, "id", "label", "status") || step["id"] != expectedIDs[index] {
			return errors.New("invalid Operations plan step")
		}
		if _, ok := operationsBoundedString(step["label"], 256); !ok ||
			!operationsAllowedString(step["status"], "PENDING", "IN_PROGRESS", "PAUSED", "COMPLETED", "FAILED", "CANCELLED") {
			return errors.New("invalid Operations plan step value")
		}
		if step["status"] == "COMPLETED" {
			completedCount++
		}
	}
	if completedSteps != completedCount || progressPercent != completedCount*25 {
		return errors.New("inconsistent Operations plan progress")
	}
	return nil
}

func inspectOperationsEventPayload(value any) error {
	forbidden := map[string]struct{}{
		"lease": {}, "leaseHistory": {}, "checkpoint": {}, "opaqueState": {},
		"runtimeRevision": {}, "providerMessage": {}, "points": {}, "rawPrompt": {},
		"toolPayload": {}, "delegationGrant": {}, "authorizationDecision": {},
		"metadata": {}, "attemptId": {},
	}
	var inspect func(any) error
	inspect = func(candidate any) error {
		switch typed := candidate.(type) {
		case map[string]any:
			for key, nested := range typed {
				if _, denied := forbidden[key]; denied {
					return errors.New("Operations event exposes internal state")
				}
				if err := inspect(nested); err != nil {
					return err
				}
			}
		case []any:
			for _, nested := range typed {
				if err := inspect(nested); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return inspect(value)
}

func decodeOperationsEventJSON(raw string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("invalid Operations event JSON")
	}
	if err := inspectOperationsEventPayload(value); err != nil {
		return nil, err
	}
	return value, nil
}

func validateOperationsStateSnapshot(value map[string]any) error {
	if !operationsExactKeys(value, "type", "snapshot") || value["type"] != "STATE_SNAPSHOT" {
		return errors.New("invalid Operations state event")
	}
	snapshot, ok := value["snapshot"].(map[string]any)
	if !ok || !operationsExactKeys(snapshot, "schemaVersion", "investigation", "plan", "toolActivities") ||
		snapshot["schemaVersion"] != "operations-investigation-ui/v1" {
		return errors.New("invalid Operations state snapshot")
	}
	investigation, ok := snapshot["investigation"].(map[string]any)
	if !ok {
		return errors.New("invalid Operations Investigation projection")
	}
	if _, exposesReceipts := investigation["toolReceipts"]; exposesReceipts {
		return errors.New("Operations state exposes Tool Receipts")
	}
	if validateOperationsInvestigationValue(investigation, false) != nil || inspectOperationsSnapshotPayload(investigation) != nil {
		return errors.New("invalid Operations Investigation projection")
	}
	plan, ok := snapshot["plan"].(map[string]any)
	if !ok || validateOperationsPlan(plan) != nil {
		return errors.New("invalid Operations plan projection")
	}
	activities, ok := snapshot["toolActivities"].([]any)
	if !ok || len(activities) > 64 {
		return errors.New("invalid Operations Tool activities")
	}
	for _, candidate := range activities {
		activity, ok := candidate.(map[string]any)
		if !ok || validateOperationsToolActivity(activity) != nil {
			return errors.New("invalid Operations Tool activity")
		}
	}
	return nil
}

func validateOperationsToolArguments(value map[string]any) error {
	if !operationsExactKeys(value, "type", "toolCallId", "delta") || value["type"] != "TOOL_CALL_ARGS" {
		return errors.New("invalid Operations Tool arguments event")
	}
	delta, ok := value["delta"].(string)
	if !ok || len(delta) > 4096 {
		return errors.New("invalid Operations Tool activity payload")
	}
	activity, err := decodeOperationsEventJSON(delta)
	if err != nil || validateOperationsToolActivity(activity) != nil {
		return errors.New("invalid Operations Tool activity payload")
	}
	return nil
}

func validateOperationsEvent(name string, value map[string]any) error {
	if value["type"] != name {
		return errors.New("Operations event name and payload type differ")
	}
	switch name {
	case "RUN_STARTED":
		if !operationsExactKeys(value, "type", "threadId", "runId") {
			return errors.New("invalid Operations run-start event")
		}
		if _, ok := operationsBoundedString(value["threadId"], 256); !ok {
			return errors.New("invalid Operations run thread")
		}
		if _, ok := operationsBoundedString(value["runId"], 512); !ok {
			return errors.New("invalid Operations projection run")
		}
	case "STATE_SNAPSHOT":
		return validateOperationsStateSnapshot(value)
	case "TOOL_CALL_START":
		if !operationsExactKeys(value, "type", "toolCallId", "toolCallName") {
			return errors.New("invalid Operations Tool start event")
		}
		if _, ok := operationsBoundedString(value["toolCallId"], 256); !ok ||
			!operationsAllowedString(value["toolCallName"], "registry.getSite", "registry.listSiteEquipment", "telemetry.getCurrentSnapshot", "analytics.getEnergySeries", "commands.getCapabilities") {
			return errors.New("invalid Operations Tool start value")
		}
	case "TOOL_CALL_ARGS":
		return validateOperationsToolArguments(value)
	case "TOOL_CALL_END":
		if !operationsExactKeys(value, "type", "toolCallId") {
			return errors.New("invalid Operations Tool end event")
		}
		if _, ok := operationsBoundedString(value["toolCallId"], 256); !ok {
			return errors.New("invalid Operations Tool end value")
		}
	case "RUN_FINISHED":
		if !operationsExactKeys(value, "type", "threadId", "runId", "outcome") {
			return errors.New("invalid Operations run-finished event")
		}
		if _, ok := operationsBoundedString(value["threadId"], 256); !ok {
			return errors.New("invalid Operations run thread")
		}
		if _, ok := operationsBoundedString(value["runId"], 512); !ok {
			return errors.New("invalid Operations projection run")
		}
		outcome, ok := value["outcome"].(map[string]any)
		if !ok || !operationsExactKeys(outcome, "type") || outcome["type"] != "success" {
			return errors.New("invalid Operations run outcome")
		}
	default:
		return errors.New("unsupported Operations event")
	}
	return nil
}

type operationsStreamRecovery struct {
	mode               string
	reason             string
	snapshotPosition   string
	latestPosition     string
	replayFromPosition string
	revision           uint64
	latestSequence     uint64
	replaySequence     uint64
}

func operationsReplayBoundary(sequence, latest uint64) bool {
	return sequence == 0 || sequence == 1 || sequence == latest || (sequence >= 4 && sequence < latest && (sequence-4)%3 == 0)
}

func validateOperationsRecoveryHeaders(headers http.Header, requestedPosition string) (operationsStreamRecovery, error) {
	recovery := operationsStreamRecovery{
		mode:               strings.TrimSpace(headers.Get("X-Operations-Recovery-Mode")),
		reason:             strings.TrimSpace(headers.Get("X-Operations-Recovery-Reason")),
		snapshotPosition:   strings.TrimSpace(headers.Get("X-Operations-Snapshot-Position")),
		latestPosition:     strings.TrimSpace(headers.Get("X-Operations-Latest-Position")),
		replayFromPosition: strings.TrimSpace(headers.Get("X-Operations-Replay-From")),
	}
	if recovery.mode != "FULL_SNAPSHOT" && recovery.mode != "RESUME" {
		return operationsStreamRecovery{}, errors.New("invalid Operations recovery mode")
	}
	if recovery.mode == "RESUME" {
		if requestedPosition == "" || recovery.reason != "VALID" || recovery.replayFromPosition != requestedPosition {
			return operationsStreamRecovery{}, errors.New("invalid Operations resume metadata")
		}
	} else {
		switch recovery.reason {
		case "INITIAL":
			if requestedPosition != "" {
				return operationsStreamRecovery{}, errors.New("initial Operations recovery must not follow a requested position")
			}
		case "UNKNOWN", "EXPIRED", "CONFLICT":
			if requestedPosition == "" {
				return operationsStreamRecovery{}, errors.New("fallback Operations recovery requires a requested position")
			}
		default:
			return operationsStreamRecovery{}, errors.New("invalid Operations snapshot recovery metadata")
		}
		if recovery.replayFromPosition != "" {
			return operationsStreamRecovery{}, errors.New("snapshot recovery must not claim a replay position")
		}
	}
	snapshotRevision, snapshotSequence, err := parseOperationsEventID(recovery.snapshotPosition)
	if err != nil || snapshotSequence != 1 {
		return operationsStreamRecovery{}, errors.New("invalid Operations snapshot position")
	}
	latestRevision, latestSequence, err := parseOperationsEventID(recovery.latestPosition)
	if err != nil || latestRevision != snapshotRevision || latestSequence < 2 {
		return operationsStreamRecovery{}, errors.New("invalid Operations latest position")
	}
	recovery.revision = snapshotRevision
	recovery.latestSequence = latestSequence
	if recovery.mode == "RESUME" {
		replayRevision, replaySequence, err := parseOperationsEventID(recovery.replayFromPosition)
		if err != nil || replayRevision != snapshotRevision || replaySequence > latestSequence || !operationsReplayBoundary(replaySequence, latestSequence) {
			return operationsStreamRecovery{}, errors.New("invalid Operations replay position")
		}
		recovery.replaySequence = replaySequence
	}
	return recovery, nil
}

func validateOperationsEventSequence(names []string, values []map[string]any, sequences []uint64, recovery operationsStreamRecovery) error {
	if len(names) < 3 || len(values) != len(names) || len(sequences) != len(names) || names[0] != "RUN_STARTED" || names[1] != "STATE_SNAPSHOT" || names[len(names)-1] != "RUN_FINISHED" {
		return errors.New("Operations event stream lifecycle is invalid")
	}
	if sequences[0] != 0 || sequences[1] != 1 || sequences[len(sequences)-1] != recovery.latestSequence {
		return errors.New("Operations event stream control positions are invalid")
	}
	startThread, _ := values[0]["threadId"].(string)
	startRun, _ := values[0]["runId"].(string)
	finishedThread, _ := values[len(values)-1]["threadId"].(string)
	finishedRun, _ := values[len(values)-1]["runId"].(string)
	if startThread != finishedThread || startRun != finishedRun {
		return errors.New("Operations event stream run identity changed")
	}
	snapshot := values[1]["snapshot"].(map[string]any)
	investigation := snapshot["investigation"].(map[string]any)
	investigationRevision, revisionOK := operationsNonnegativeInteger(investigation["revision"])
	investigationID, idOK := operationsBoundedString(investigation["id"], 256)
	if !revisionOK || uint64(investigationRevision) != recovery.revision || !idOK || investigationID != startThread {
		return errors.New("Operations event stream does not match the authoritative snapshot")
	}
	activities := snapshot["toolActivities"].([]any)
	expectedLatest := uint64(2 + len(activities)*3)
	if recovery.latestSequence != expectedLatest {
		return errors.New("Operations latest position does not match the committed snapshot")
	}
	startActivity := 0
	if recovery.mode == "RESUME" {
		switch {
		case recovery.replaySequence <= 1:
			startActivity = 0
		case recovery.replaySequence == recovery.latestSequence:
			startActivity = len(activities)
		default:
			startActivity = int((recovery.replaySequence-4)/3) + 1
		}
	}
	toolEventCount := len(names) - 3
	if toolEventCount != (len(activities)-startActivity)*3 {
		return errors.New("Operations replay event count does not match the committed snapshot suffix")
	}
	for replayIndex := 0; replayIndex < toolEventCount/3; replayIndex++ {
		activityIndex := startActivity + replayIndex
		activity := activities[activityIndex].(map[string]any)
		streamIndex := 2 + replayIndex*3
		if names[streamIndex] != "TOOL_CALL_START" || names[streamIndex+1] != "TOOL_CALL_ARGS" || names[streamIndex+2] != "TOOL_CALL_END" {
			return errors.New("Operations Tool events are out of order")
		}
		expectedStart := uint64(2 + activityIndex*3)
		if sequences[streamIndex] != expectedStart || sequences[streamIndex+1] != expectedStart+1 || sequences[streamIndex+2] != expectedStart+2 {
			return errors.New("Operations Tool event positions are invalid")
		}
		start := values[streamIndex]
		arguments := values[streamIndex+1]
		end := values[streamIndex+2]
		toolCallID, _ := start["toolCallId"].(string)
		if arguments["toolCallId"] != toolCallID || end["toolCallId"] != toolCallID || activity["recordId"] != toolCallID || start["toolCallName"] != activity["logicalTool"] {
			return errors.New("Operations Tool event identity changed")
		}
		delta, _ := arguments["delta"].(string)
		deltaActivity, err := decodeOperationsEventJSON(delta)
		if err != nil {
			return errors.New("invalid Operations Tool activity payload")
		}
		for _, key := range []string{"recordId", "logicalTool", "owner", "resultCategory", "startedAt", "completedAt"} {
			if deltaActivity[key] != activity[key] {
				return errors.New("Operations Tool event differs from the committed snapshot")
			}
		}
	}
	return nil
}

func canonicalOperationsEventNumber(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, candidate := range value {
		if candidate < '0' || candidate > '9' {
			return false
		}
	}
	return true
}

func parseOperationsEventID(id string) (uint64, uint64, error) {
	revisionText, sequenceText, ok := strings.Cut(id, ":")
	if !ok || strings.Contains(sequenceText, ":") || !canonicalOperationsEventNumber(revisionText) || !canonicalOperationsEventNumber(sequenceText) {
		return 0, 0, errors.New("invalid Operations event identity")
	}
	revision, err := strconv.ParseUint(revisionText, 10, 64)
	if err != nil {
		return 0, 0, errors.New("invalid Operations event revision")
	}
	sequence, err := strconv.ParseUint(sequenceText, 10, 64)
	if err != nil {
		return 0, 0, errors.New("invalid Operations event sequence")
	}
	return revision, sequence, nil
}

func validateOperationsEventStream(raw []byte, headers http.Header, requestedPosition string) (operationsStreamRecovery, error) {
	recovery, err := validateOperationsRecoveryHeaders(headers, requestedPosition)
	if err != nil {
		return operationsStreamRecovery{}, err
	}
	if len(raw) == 0 || !bytes.HasSuffix(raw, []byte("\n\n")) {
		return operationsStreamRecovery{}, errors.New("Operations event stream is incomplete")
	}
	normalized := strings.ReplaceAll(string(raw), "\r\n", "\n")
	blocks := strings.Split(normalized, "\n\n")
	eventNames := make([]string, 0, len(blocks))
	eventValues := make([]map[string]any, 0, len(blocks))
	eventSequences := make([]uint64, 0, len(blocks))
	var streamRevision uint64
	for _, block := range blocks {
		if block == "" {
			continue
		}
		var id, eventName, data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "id: ") && id == "":
				id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: ") && eventName == "":
				eventName = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && data == "":
				data = strings.TrimPrefix(line, "data: ")
			default:
				return operationsStreamRecovery{}, errors.New("invalid Operations event stream field")
			}
		}
		if id == "" || len(id) > 128 || eventName == "" || data == "" {
			return operationsStreamRecovery{}, errors.New("incomplete Operations event stream block")
		}
		revision, sequence, err := parseOperationsEventID(id)
		if err != nil || (len(eventNames) > 0 && revision != streamRevision) {
			return operationsStreamRecovery{}, errors.New("Operations event stream identity is invalid")
		}
		if len(eventSequences) > 0 && sequence <= eventSequences[len(eventSequences)-1] {
			return operationsStreamRecovery{}, errors.New("Operations event stream positions are not ordered")
		}
		if len(eventNames) == 0 {
			streamRevision = revision
		}
		value, err := decodeOperationsEventJSON(data)
		if err != nil || validateOperationsEvent(eventName, value) != nil {
			return operationsStreamRecovery{}, errors.New("invalid Operations event stream payload")
		}
		eventNames = append(eventNames, eventName)
		eventValues = append(eventValues, value)
		eventSequences = append(eventSequences, sequence)
		if len(eventNames) > 256 {
			return operationsStreamRecovery{}, errors.New("Operations event stream has too many events")
		}
	}
	if streamRevision != recovery.revision {
		return operationsStreamRecovery{}, errors.New("Operations recovery metadata does not match stream revision")
	}
	if err := validateOperationsEventSequence(eventNames, eventValues, eventSequences, recovery); err != nil {
		return operationsStreamRecovery{}, err
	}
	return recovery, nil
}
