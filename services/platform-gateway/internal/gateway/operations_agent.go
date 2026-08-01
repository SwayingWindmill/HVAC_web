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
	PublicOperationsInvestigationsPathTemplate = "/api/v1/sites/{siteId}/operations/investigations"
	PublicOperationsInvestigationPathTemplate  = "/api/v1/sites/{siteId}/operations/investigations/{investigationId}"
	PublicOperationsEventsPathTemplate         = "/api/v1/sites/{siteId}/operations/investigations/{investigationId}/events"
	PublicOperationsAdvancePathTemplate        = "/api/v1/sites/{siteId}/operations/investigations/{investigationId}:advance"
	PublicOperationsCancelPathTemplate         = "/api/v1/sites/{siteId}/operations/investigations/{investigationId}:cancel"
	InternalOperationsToolAuthorizationPath    = "/internal/v1/operations/tool-authorization"
	defaultOperationsTimeout                   = 8 * time.Second
	defaultOperationsRequestBytes              = int64(8 << 10)
	defaultOperationsResponseBytes             = int64(1 << 20)
	defaultOperationsRatePerMinute             = 30
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
		return publicOperationsRoute{kind: "START", template: PublicOperationsInvestigationsPathTemplate, siteID: siteID, internalPath: internalBase, method: http.MethodPost, mutation: true}, true
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
			method: http.MethodGet, mutation: false,
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
	} else if kind == "CANCEL" {
		template = PublicOperationsCancelPathTemplate
	}
	return publicOperationsRoute{kind: kind, template: template, siteID: siteID, investigationID: investigationID, internalPath: internalBase + "/" + url.PathEscape(investigationID) + suffix, method: method, mutation: mutation}, true
}

func dispatchOperationsRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicOperationsRoute) {
	if route.siteID == "" || (route.kind != "START" && route.investigationID == "") {
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
	body := []byte(nil)
	if route.mutation {
		body, err = readOperationsMutationBody(request.Body, request.ContentLength, h.operations.maxRequestBytes)
		if err != nil {
			status, code := http.StatusBadRequest, "REQUEST_INVALID"
			if errors.Is(err, errOperationsBodyTooLarge) {
				status, code = http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE"
			}
			writeProblem(writer, request, status, code, "Request invalid", "The Operations Investigation request body is invalid.", false, nil)
			return
		}
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
		if !strings.HasPrefix(contentType, "application/json") || validateOperationsSnapshot(raw) != nil {
			writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_CONTRACT_FAILED", "Operations Agent contract failed", "The Operations Agent returned an invalid authoritative snapshot.", true, nil)
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

func readOperationsMutationBody(reader io.Reader, contentLength, limit int64) ([]byte, error) {
	if contentLength > limit {
		return nil, errOperationsBodyTooLarge
	}
	raw, err := readBoundedBody(reader, limit)
	if err != nil {
		return nil, errOperationsBodyTooLarge
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || len(value) != 0 || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("invalid Operations mutation body")
	}
	return []byte("{}"), nil
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

func validateOperationsSnapshot(raw []byte) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid Operations snapshot JSON")
	}
	root, ok := value.(map[string]any)
	if !ok || root["schemaVersion"] != json.Number("1") || root["id"] == "" || root["status"] == "" {
		return errors.New("invalid Operations snapshot")
	}
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
	rawInvestigation, err := json.Marshal(investigation)
	if err != nil || validateOperationsSnapshot(rawInvestigation) != nil {
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
