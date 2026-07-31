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
	upstream.Header.Set("Accept", "application/json, application/problem+json")
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
