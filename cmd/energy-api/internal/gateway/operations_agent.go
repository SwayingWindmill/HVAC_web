package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

// The Operations Agent behind the Gateway: its Agent Session routes (operations_agent_sessions.go)
// and the internal route where the Agent exchanges a Session's service delegation for the exact
// owner grant right before each Tool read.

const (
	InternalOperationsToolAuthorizationPath = "/internal/v1/operations/tool-authorization"
	defaultOperationsTimeout                = 8 * time.Second
	defaultOperationsServiceDelegationTTL   = 3 * time.Minute
	maxOperationsServiceDelegationTTL       = 5 * time.Minute
	defaultOperationsRequestBytes           = int64(8 << 10)
	defaultOperationsResponseBytes          = int64(1 << 20)
)

type OperationsAgentConfig struct {
	BaseURL              string
	Audience             string
	WorkloadSPIFFEID     string
	HTTPClient           *http.Client
	Timeout              time.Duration
	ServiceDelegationTTL time.Duration
	MaxRequestBytes      int64
	MaxResponseBytes     int64
	RateLimiter          *limitpolicy.Limiter
}

type operationsAgentController struct {
	baseURL              string
	audience             string
	workloadSPIFFEID     string
	httpClient           *http.Client
	timeout              time.Duration
	serviceDelegationTTL time.Duration
	maxRequestBytes      int64
	maxResponseBytes     int64
	rateLimiter          *limitpolicy.Limiter
}

type operationsToolAuthorizationRequest struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
	Request   struct {
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
		timeout:              defaultOperationsTimeout,
		serviceDelegationTTL: defaultOperationsServiceDelegationTTL,
		maxRequestBytes:      defaultOperationsRequestBytes,
		maxResponseBytes:     defaultOperationsResponseBytes,
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
	if ttl := config.ServiceDelegationTTL.Truncate(time.Second); ttl >= time.Second {
		if ttl > maxOperationsServiceDelegationTTL {
			ttl = maxOperationsServiceDelegationTTL
		}
		controller.serviceDelegationTTL = ttl
	}
	if config.MaxRequestBytes > 0 {
		controller.maxRequestBytes = config.MaxRequestBytes
	}
	if config.MaxResponseBytes > 0 {
		controller.maxResponseBytes = config.MaxResponseBytes
	}
	controller.rateLimiter = config.RateLimiter
	return controller
}

func hasOperationsJSONContentType(request *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

var operationsForgedHeaders = []string{
	"Cookie",
	"Origin",
	"X-CSRF-Token",
	"X-Principal",
	"X-Roles",
	"X-Tenant-ID",
	"X-Site-ID",
	"X-Admin",
	"X-Command-Grant",
	"X-Command-Read-Context",
	"X-Command-Control-Context",
	"X-Operations-Registry-Site-Grant",
	"X-Operations-Registry-Asset-Grant",
	"X-Operations-Energy-Grant",
}

type problemDetail struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

func (p *problemDetail) write(writer http.ResponseWriter, request *http.Request) {
	writeProblem(writer, request, p.status, p.code, p.title, p.detail, p.retryable, nil)
}

func invalidToolRequestProblem(detail string) *problemDetail {
	return &problemDetail{
		status: http.StatusBadRequest,
		code:   "OPERATIONS_TOOL_REQUEST_INVALID",
		title:  "Tool request invalid",
		detail: detail,
	}
}

func siteNotFoundProblem() *problemDetail {
	return &problemDetail{
		status: http.StatusNotFound,
		code:   "RESOURCE_NOT_FOUND",
		title:  "Resource not found",
		detail: "The requested Site was not found.",
	}
}

func unavailableOperationsProblem(detail string) *problemDetail {
	return &problemDetail{
		status:    http.StatusServiceUnavailable,
		code:      "OPERATIONS_AUTHORIZATION_UNAVAILABLE",
		title:     "Operations authorization unavailable",
		detail:    detail,
		retryable: true,
	}
}

type operationsToolAuthContext struct {
	handler *handler
	request *http.Request
	session bffSession
	claims  identitycontext.DelegationClaims
	siteID  string
	input   any
}

type operationsToolSpec struct {
	parse     func(raw json.RawMessage) (any, string, *problemDetail)
	authorize func(ctx operationsToolAuthContext) (operationsToolAuthorizationResponse, *problemDetail)
}

type telemetryToolInput struct {
	action       telemetryauth.Action
	target       telemetryauth.Target
	historyQuery *telemetryhistorymodel.DeviceHistoryAggregateQuery
}

// Each logical tool maps to a strict input parser and its owner-scoped grant authorizer.
var operationsToolAuthorizers = map[string]operationsToolSpec{
	"registry.getSite":           registryToolSpec(registryauth.ActionSiteRead, "The Registry Site request is invalid."),
	"registry.listSiteAssets":    registryToolSpec(registryauth.ActionAssetList, "The Registry Asset request is invalid."),
	"registry.getSiteAssetModel": registryToolSpec(registryauth.ActionAssetModelRead, "The Registry asset model request is invalid."),
	"analytics.getEnergySeries": {
		parse: func(raw json.RawMessage) (any, string, *problemDetail) {
			return parseAnalyticsToolInput(raw, energySeriesScope, "The Energy Series request is invalid.")
		},
		authorize: authorizeAnalyticsTool,
	},
	"analytics.semanticQuery": {
		parse: func(raw json.RawMessage) (any, string, *problemDetail) {
			return parseAnalyticsToolInput(raw, semanticQueryScope, "The semantic query asks for members or a range outside the catalog.")
		},
		authorize: authorizeAnalyticsTool,
	},
	"alarm.listAlarms": {
		parse:     parseAlarmListToolInput,
		authorize: authorizeAlarmTool,
	},
	"workorder.listWorkOrders": {
		parse:     parseSiteLimitToolInput("The Work Order list request is invalid."),
		authorize: authorizeWorkOrderTool,
	},
	"fdd.listFindings": {
		parse:     parseSiteLimitToolInput("The FDD findings request is invalid."),
		authorize: authorizeFDDTool,
	},
	"telemetry.current.getDeviceObservationSnapshot": {
		parse:     parseTelemetrySnapshotToolInput,
		authorize: authorizeTelemetryTool,
	},
	"telemetry.history.aggregateDeviceHistory": {
		parse:     parseTelemetryHistoryToolInput,
		authorize: authorizeTelemetryTool,
	},
}

func registryToolSpec(action registryauth.Action, invalidDetail string) operationsToolSpec {
	return operationsToolSpec{
		parse: func(raw json.RawMessage) (any, string, *problemDetail) {
			var input struct {
				SiteID string `json:"siteId"`
			}
			if decodeStrictOperationsJSON(raw, &input) != nil {
				return nil, "", invalidToolRequestProblem(invalidDetail)
			}
			return nil, input.SiteID, nil
		},
		authorize: func(ctx operationsToolAuthContext) (operationsToolAuthorizationResponse, *problemDetail) {
			authorization, failure := ctx.handler.authorizeRegistryForPresenter(
				ctx.request.Context(),
				ctx.session,
				action,
				ctx.handler.operations.workloadSPIFFEID,
			)
			if failure != nil || !registryAuthorizationAllowsSite(authorization, ctx.siteID) {
				return operationsToolAuthorizationResponse{}, siteNotFoundProblem()
			}
			return operationsToolAuthorizationResponse{
				DelegationGrant: authorization.coreGrant,
				PolicyRevision:  authorization.policyRevision,
			}, nil
		},
	}
}

func parseAnalyticsToolInput[Q any](
	raw json.RawMessage,
	toScope func(Q) (analyticsScope, error),
	invalidDetail string,
) (any, string, *problemDetail) {
	var query Q
	if decodeStrictOperationsJSON(raw, &query) != nil {
		return nil, "", invalidToolRequestProblem(invalidDetail)
	}
	scope, err := toScope(query)
	if err != nil {
		return nil, "", invalidToolRequestProblem(invalidDetail)
	}
	return scope, scope.SiteID, nil
}

func parseAlarmListToolInput(raw json.RawMessage) (any, string, *problemDetail) {
	var alarmInput struct {
		SiteID    string `json:"siteId"`
		Condition string `json:"condition"`
		Limit     int    `json:"limit"`
	}
	// Condition and size travel with the request the Agent's reader sends; the owner checks them.
	if decodeStrictOperationsJSON(raw, &alarmInput) != nil {
		return nil, "", invalidToolRequestProblem("The Alarm list request is invalid.")
	}
	return nil, alarmInput.SiteID, nil
}

func parseSiteLimitToolInput(invalidDetail string) func(json.RawMessage) (any, string, *problemDetail) {
	return func(raw json.RawMessage) (any, string, *problemDetail) {
		// Size travels with the request the Agent's reader sends; the owner checks it.
		var listInput struct {
			SiteID string `json:"siteId"`
			Limit  int    `json:"limit"`
		}
		if decodeStrictOperationsJSON(raw, &listInput) != nil {
			return nil, "", invalidToolRequestProblem(invalidDetail)
		}
		return nil, listInput.SiteID, nil
	}
}

func parseTelemetrySnapshotToolInput(raw json.RawMessage) (any, string, *problemDetail) {
	var telemetryInput struct {
		SiteID    string   `json:"siteId"`
		DeviceID  string   `json:"deviceId"`
		PointKeys []string `json:"pointKeys"`
	}
	if decodeStrictOperationsJSON(raw, &telemetryInput) != nil || !isLowerUUIDv7(telemetryInput.SiteID) || !isLowerUUIDv7(telemetryInput.DeviceID) {
		return nil, "", invalidToolRequestProblem("The Telemetry Snapshot request is invalid.")
	}
	target := telemetryauth.Target{DeviceID: telemetryInput.DeviceID, Keys: telemetryInput.PointKeys}
	if _, err := telemetryauth.CanonicalTargets([]telemetryauth.Target{target}); err != nil {
		return nil, "", invalidToolRequestProblem("The Telemetry Snapshot selection is invalid.")
	}
	return telemetryToolInput{action: telemetryauth.ActionSnapshotRead, target: target}, telemetryInput.SiteID, nil
}

func parseTelemetryHistoryToolInput(raw json.RawMessage) (any, string, *problemDetail) {
	var historyQuery telemetryhistorymodel.DeviceHistoryAggregateQuery
	if decodeStrictOperationsJSON(raw, &historyQuery) != nil || historyQuery.Validate() != nil {
		return nil, "", invalidToolRequestProblem("The Device History request is invalid.")
	}
	target := telemetryauth.Target{DeviceID: historyQuery.DeviceID, Keys: historyQuery.Keys}
	return telemetryToolInput{
		action:       telemetryauth.ActionHistoryRead,
		target:       target,
		historyQuery: &historyQuery,
	}, historyQuery.SiteID, nil
}

func authorizeWorkOrderTool(ctx operationsToolAuthContext) (operationsToolAuthorizationResponse, *problemDetail) {
	// The Agent reads the list from the work-order owner itself, with a context naming it (#595).
	if ctx.handler.workOrder == nil {
		return operationsToolAuthorizationResponse{}, unavailableOperationsProblem("The Work Order Service is not configured.")
	}
	route := publicWorkOrderRoute{kind: publicWorkOrderCollection, template: "/api/v1/sites/{siteId}/work-orders", siteID: ctx.siteID, action: workordermodel.ActionList}
	decision, failure := ctx.handler.authorizeWorkOrder(ctx.request, ctx.session, route, nil, nil)
	if failure == nil {
		var grant string
		grant, failure = ctx.handler.signWorkOrderReadContext(ctx.session, route, decision, ctx.session.TenantID, ctx.handler.operations.workloadSPIFFEID)
		if failure == nil {
			return operationsToolAuthorizationResponse{DelegationGrant: grant, PolicyRevision: decision.PolicyRevision}, nil
		}
	}
	if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
		return operationsToolAuthorizationResponse{}, siteNotFoundProblem()
	}
	return operationsToolAuthorizationResponse{}, unavailableOperationsProblem("The Work Order authorization could not be completed.")
}

func authorizeFDDTool(ctx operationsToolAuthContext) (operationsToolAuthorizationResponse, *problemDetail) {
	// Findings are read on site.read, as the 诊断 page reads them; the Agent presents the context (#595).
	if ctx.handler.intelligence == nil || ctx.handler.intelligence.fddBaseURL == "" {
		return operationsToolAuthorizationResponse{}, unavailableOperationsProblem("FDD is not configured.")
	}
	authorization, failure := ctx.handler.authorizeRegistry(ctx.request.Context(), ctx.session, registryauth.ActionSiteRead)
	if failure != nil || !registryAuthorizationAllowsSite(authorization, ctx.siteID) {
		if failure == nil || failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			return operationsToolAuthorizationResponse{}, siteNotFoundProblem()
		}
		return operationsToolAuthorizationResponse{}, unavailableOperationsProblem("The Site authorization could not be completed.")
	}
	grant, err := ctx.handler.ownerContext(ctx.session, fddOwner, ctx.siteID, fddReadAction, ctx.handler.operations.workloadSPIFFEID)
	if err != nil {
		return operationsToolAuthorizationResponse{}, unavailableOperationsProblem("The FDD context could not be signed.")
	}
	return operationsToolAuthorizationResponse{DelegationGrant: grant, PolicyRevision: authorization.policyRevision}, nil
}

func authorizeAlarmTool(ctx operationsToolAuthContext) (operationsToolAuthorizationResponse, *problemDetail) {
	// The Agent reads the list from the alarm owner itself, with a context naming it (#594).
	if ctx.handler.alarm == nil {
		return operationsToolAuthorizationResponse{}, unavailableOperationsProblem("The Alarm Service is not configured.")
	}
	route := publicAlarmRoute{template: "/api/v1/alarms", action: alarmmodel.ActionRead, siteID: ctx.siteID}
	caller := ownerCaller{session: ctx.session, presenter: ctx.handler.operations.workloadSPIFFEID}
	grant, decision, failure := grantOwner(ctx.request.Context(), ctx.handler, caller, ctx.handler.alarmOwner(), string(route.action), route.scope(ctx.session.TenantID))
	if failure == nil {
		return operationsToolAuthorizationResponse{DelegationGrant: grant, PolicyRevision: decision.policyRevision}, nil
	}
	if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
		return operationsToolAuthorizationResponse{}, siteNotFoundProblem()
	}
	return operationsToolAuthorizationResponse{}, unavailableOperationsProblem("The Alarm authorization could not be completed.")
}

func authorizeTelemetryTool(ctx operationsToolAuthContext) (operationsToolAuthorizationResponse, *problemDetail) {
	parsed := ctx.input.(telemetryToolInput)
	caller := telemetryCaller{
		principal: ctx.session.Principal,
		tenantID:  ctx.session.TenantID,
		contextID: ctx.session.ID,
		expiresAt: ctx.session.ExpiresAt,
	}
	authorization, failure := ctx.handler.authorizeTelemetryForPresenter(
		ctx.request.Context(), ctx.request, caller, parsed.action,
		[]telemetryauth.Target{parsed.target}, ctx.handler.operations.workloadSPIFFEID,
	)
	if failure != nil {
		if failure.status == http.StatusNotFound || failure.status == http.StatusForbidden {
			return operationsToolAuthorizationResponse{}, &problemDetail{
				status: http.StatusNotFound,
				code:   "RESOURCE_NOT_FOUND",
				title:  "Resource not found",
				detail: "The requested telemetry resource was not found.",
			}
		}
		return operationsToolAuthorizationResponse{}, &problemDetail{
			status:    failure.status,
			code:      failure.code,
			title:     failure.title,
			detail:    failure.detail,
			retryable: failure.retryable,
		}
	}
	if len(authorization.targets) != 1 || authorization.targets[0].TenantID != ctx.claims.TenantID || authorization.targets[0].SiteID != ctx.siteID || authorization.targets[0].DeviceID != parsed.target.DeviceID {
		return operationsToolAuthorizationResponse{}, &problemDetail{
			status: http.StatusNotFound,
			code:   "RESOURCE_NOT_FOUND",
			title:  "Resource not found",
			detail: "The requested telemetry resource was not found.",
		}
	}
	grant := authorization.grant
	if parsed.action == telemetryauth.ActionHistoryRead {
		// History is answered by the Telemetry Query Service, which accepts a Gateway-signed
		// grant bound to the exact aggregate query and presented by the Agent.
		if parsed.historyQuery.TenantID != ctx.claims.TenantID {
			return operationsToolAuthorizationResponse{}, &problemDetail{
				status: http.StatusNotFound,
				code:   "RESOURCE_NOT_FOUND",
				title:  "Resource not found",
				detail: "The requested telemetry resource was not found.",
			}
		}
		signed, failure := ctx.handler.signHistoryQueryGrant(caller, authorization, ctx.claims.TenantID, telemetryhistorymodel.DeviceHistoryAggregateAction, parsed.historyQuery.ScopeDigest, ctx.handler.operations.workloadSPIFFEID)
		if failure != nil {
			return operationsToolAuthorizationResponse{}, &problemDetail{
				status:    failure.status,
				code:      failure.code,
				title:     failure.title,
				detail:    failure.detail,
				retryable: failure.retryable,
			}
		}
		grant = signed
	}
	return operationsToolAuthorizationResponse{DelegationGrant: grant, PolicyRevision: authorization.policyRevision}, nil
}

func authorizeAnalyticsTool(ctx operationsToolAuthContext) (operationsToolAuthorizationResponse, *problemDetail) {
	scope := ctx.input.(analyticsScope)
	grant, failure := ctx.handler.authorizeAnalyticsForPresenter(
		ctx.request.Context(),
		ctx.request,
		ctx.session,
		scope,
		ctx.handler.operations.workloadSPIFFEID,
	)
	if failure != nil {
		if failure.status == http.StatusForbidden {
			return operationsToolAuthorizationResponse{}, siteNotFoundProblem()
		}
		return operationsToolAuthorizationResponse{}, &problemDetail{
			status:    failure.status,
			code:      failure.code,
			title:     failure.title,
			detail:    failure.detail,
			retryable: failure.retryable,
		}
	}
	issuedClaims, err := identitycontext.VerifyDelegation(
		ctx.handler.identity.config.DelegationSigner.Public(),
		grant,
	)
	if err != nil || strings.TrimSpace(issuedClaims.PolicyRevision) == "" {
		return operationsToolAuthorizationResponse{}, unavailableOperationsProblem("The Energy authorization grant could not be verified after issuance.")
	}
	return operationsToolAuthorizationResponse{
		DelegationGrant: grant,
		PolicyRevision:  issuedClaims.PolicyRevision,
	}, nil
}

func (h *handler) authorizeOperationsTool(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowedFor(writer, request, http.MethodPost)
		return
	}
	for _, header := range operationsForgedHeaders {
		if request.Header.Get(header) != "" {
			writeProblem(writer, request, http.StatusBadRequest, "FORGED_IDENTITY_HEADER", "Forged identity header", "Browser Session and caller-supplied identity headers are not accepted on the internal Tool authorization route.", false, nil)
			return
		}
	}
	if h.operations == nil || h.operations.workloadSPIFFEID == "" || h.identity == nil {
		unavailableOperationsProblem("The Operations authorization boundary is not configured.").write(writer, request)
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
		strings.TrimSpace(input.SessionID) == "" || len(input.SessionID) > 256 ||
		strings.TrimSpace(input.RunID) == "" || len(input.RunID) > 256 ||
		strings.TrimSpace(input.Request.RequestID) == "" || len(input.Request.RequestID) > 256 ||
		headerRequestID == "" || len(headerRequestID) > 256 || headerRequestID != input.Request.RequestID {
		invalidToolRequestProblem("The Tool authorization request is invalid.").write(writer, request)
		return
	}
	spec, ok := operationsToolAuthorizers[input.Request.Tool]
	if !ok {
		writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_UNSUPPORTED", "Tool unsupported", "The requested Tool is not enabled for the Operations Agent.", false, nil)
		return
	}
	parsedInput, siteID, problem := spec.parse(input.Request.Input)
	if problem != nil {
		problem.write(writer, request)
		return
	}
	if !isLowerUUIDv7(siteID) {
		invalidToolRequestProblem("The Tool request Site identity is invalid.").write(writer, request)
		return
	}
	claims, err := identitycontext.VerifyDelegation(h.identity.config.DelegationSigner.Public(), request.Header.Get("X-Delegation-Grant"))
	if err != nil || claims.TenantID == "" || claims.PolicyRevision != h.identity.config.PolicyRevision {
		writeProblem(writer, request, http.StatusUnauthorized, "OPERATIONS_DELEGATION_INVALID", "Operations delegation invalid", "The Operations service delegation is invalid.", false, nil)
		return
	}
	acceptableScopes := []string{"site:" + siteID, "session:" + claims.SessionID}
	maxDelegationTTL := h.operations.serviceDelegationTTL
	if maxDelegationTTL <= 0 {
		maxDelegationTTL = defaultOperationsServiceDelegationTTL
	}
	if identitycontext.ValidateDelegationAnyScopeWithMaxTTL(claims, h.identity.now(), h.identity.config.ExecutingWorkloadSPIFFE, h.operations.audience, "operations:investigate", acceptableScopes, maxDelegationTTL) != nil {
		writeProblem(writer, request, http.StatusForbidden, "OPERATIONS_DELEGATION_REJECTED", "Operations delegation rejected", "The Operations service delegation does not authorize this Site.", false, nil)
		return
	}
	stored, err := h.identity.store.GetSession(request.Context(), claims.SessionID)
	if err != nil || stored.RevokedAt != nil || !h.identity.now().Before(stored.ExpiresAt) || stored.TenantID != claims.TenantID || stored.Principal.Subject != claims.Subject || stored.Principal.Issuer != claims.SubjectIssuer {
		siteNotFoundProblem().write(writer, request)
		return
	}
	response, problem := spec.authorize(operationsToolAuthContext{
		handler: h,
		request: request,
		session: bffSession{Session: stored},
		claims:  claims,
		siteID:  siteID,
		input:   parsedInput,
	})
	if problem != nil {
		problem.write(writer, request)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func registryAuthorizationAllowsSite(authorization registryAuthorization, siteID string) bool {
	_, allowed := authorization.allowedSiteIDs[siteID]
	return allowed
}

func (h *handler) operationsServiceDelegation(session bffSession, siteID string) (string, error) {
	ttl := defaultOperationsServiceDelegationTTL
	if h.operations != nil && h.operations.serviceDelegationTTL > 0 {
		ttl = h.operations.serviceDelegationTTL
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(ttl)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	return identitycontext.SignDelegation(h.identity.config.DelegationSigner, identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject,
		SubjectIssuer: session.Principal.Issuer, DisplayName: session.Principal.DisplayName,
		Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.operations.audience,
		TenantID: session.TenantID, Actions: []string{"operations:investigate"},
		Scopes: []string{"site:" + siteID, "session:" + session.ID}, PolicyRevision: h.identity.config.PolicyRevision,
		SessionID: session.ID, IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	})
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

// operationsBoundedString bounds text in characters, as the contracts' maxLength and the Agent
// count it; a byte bound would reject Chinese text a third of the length.
func operationsBoundedString(value any, maximum int) (string, bool) {
	text, ok := value.(string)
	return text, ok && strings.TrimSpace(text) != "" && utf8.RuneCountInString(text) <= maximum
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
