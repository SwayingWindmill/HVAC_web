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

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

// The Operations Agent behind the Gateway: its Agent Session routes (operations_agent_sessions.go)
// and the internal route where the Agent exchanges a Session's service delegation for the exact
// owner grant right before each Tool read.

const (
	InternalOperationsToolAuthorizationPath = "/internal/v1/operations/tool-authorization"
	defaultOperationsTimeout                = 8 * time.Second
	defaultOperationsRequestBytes           = int64(8 << 10)
	defaultOperationsResponseBytes          = int64(1 << 20)
)

type OperationsAgentConfig struct {
	BaseURL          string
	Audience         string
	WorkloadSPIFFEID string
	HTTPClient       *http.Client
	Timeout          time.Duration
	MaxRequestBytes  int64
	MaxResponseBytes int64
	RateLimiter      *limitpolicy.Limiter
}

type operationsAgentController struct {
	baseURL          string
	audience         string
	workloadSPIFFEID string
	httpClient       *http.Client
	timeout          time.Duration
	maxRequestBytes  int64
	maxResponseBytes int64
	rateLimiter      *limitpolicy.Limiter
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
		timeout:          defaultOperationsTimeout,
		maxRequestBytes:  defaultOperationsRequestBytes,
		maxResponseBytes: defaultOperationsResponseBytes,
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
	controller.rateLimiter = config.RateLimiter
	return controller
}

func hasOperationsJSONContentType(request *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
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
		"X-Tenant-ID",
		"X-Site-ID",
		"X-Admin",
		"X-Command-Grant",
		"X-Command-Read-Context",
		"X-Operations-Registry-Site-Grant",
		"X-Operations-Registry-Asset-Grant",
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
		strings.TrimSpace(input.SessionID) == "" || len(input.SessionID) > 256 ||
		strings.TrimSpace(input.RunID) == "" || len(input.RunID) > 256 ||
		strings.TrimSpace(input.Request.RequestID) == "" || len(input.Request.RequestID) > 256 ||
		headerRequestID == "" || len(headerRequestID) > 256 || headerRequestID != input.Request.RequestID {
		writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Tool authorization request is invalid.", false, nil)
		return
	}
	var siteID string
	var registryAction registryauth.Action
	// analytics is the Energy grant's scope for an Energy Series or a semantic query; nil otherwise.
	var analytics *analyticsScope
	var telemetryTarget telemetryauth.Target
	var historyQuery telemetryhistorymodel.DeviceHistoryAggregateQuery
	alarmList := false
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
	case "registry.listSiteAssets":
		var registryInput struct {
			SiteID string `json:"siteId"`
		}
		if decodeStrictOperationsJSON(input.Request.Input, &registryInput) != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Registry Asset request is invalid.", false, nil)
			return
		}
		siteID, registryAction = registryInput.SiteID, registryauth.ActionAssetList
	case "registry.getSiteAssetModel":
		var registryInput struct {
			SiteID string `json:"siteId"`
		}
		if decodeStrictOperationsJSON(input.Request.Input, &registryInput) != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Registry asset model request is invalid.", false, nil)
			return
		}
		siteID, registryAction = registryInput.SiteID, registryauth.ActionAssetModelRead
	case "analytics.getEnergySeries":
		var energyQuery analyticsmodel.EnergySeriesQuery
		if decodeStrictOperationsJSON(input.Request.Input, &energyQuery) != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Energy Series request is invalid.", false, nil)
			return
		}
		scope, err := energySeriesScope(energyQuery)
		if err != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Energy Series request is invalid.", false, nil)
			return
		}
		siteID, analytics = scope.SiteID, &scope
	case "analytics.semanticQuery":
		var semanticQuery analyticsmodel.SemanticQuery
		if decodeStrictOperationsJSON(input.Request.Input, &semanticQuery) != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The semantic query asks for members or a range outside the catalog.", false, nil)
			return
		}
		scope, err := semanticQueryScope(semanticQuery)
		if err != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The semantic query asks for members or a range outside the catalog.", false, nil)
			return
		}
		siteID, analytics = scope.SiteID, &scope
	case "alarm.listAlarms":
		var alarmInput struct {
			SiteID string `json:"siteId"`
		}
		if decodeStrictOperationsJSON(input.Request.Input, &alarmInput) != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Alarm list request is invalid.", false, nil)
			return
		}
		siteID, alarmList = alarmInput.SiteID, true
	case "telemetry.current.getDeviceObservationSnapshot":
		var telemetryInput struct {
			SiteID    string   `json:"siteId"`
			DeviceID  string   `json:"deviceId"`
			PointKeys []string `json:"pointKeys"`
		}
		if decodeStrictOperationsJSON(input.Request.Input, &telemetryInput) != nil || !isLowerUUIDv7(telemetryInput.SiteID) || !isLowerUUIDv7(telemetryInput.DeviceID) {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Telemetry Snapshot request is invalid.", false, nil)
			return
		}
		telemetryTarget = telemetryauth.Target{DeviceID: telemetryInput.DeviceID, Keys: telemetryInput.PointKeys}
		if _, err := telemetryauth.CanonicalTargets([]telemetryauth.Target{telemetryTarget}); err != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Telemetry Snapshot selection is invalid.", false, nil)
			return
		}
		siteID = telemetryInput.SiteID
	case "telemetry.history.aggregateDeviceHistory":
		if decodeStrictOperationsJSON(input.Request.Input, &historyQuery) != nil || historyQuery.Validate() != nil {
			writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Device History request is invalid.", false, nil)
			return
		}
		telemetryTarget = telemetryauth.Target{DeviceID: historyQuery.DeviceID, Keys: historyQuery.Keys}
		siteID = historyQuery.SiteID
	default:
		writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_UNSUPPORTED", "Tool unsupported", "The requested Tool is not enabled for the Operations Agent.", false, nil)
		return
	}
	if !isLowerUUIDv7(siteID) {
		writeProblem(writer, request, http.StatusBadRequest, "OPERATIONS_TOOL_REQUEST_INVALID", "Tool request invalid", "The Tool request Site identity is invalid.", false, nil)
		return
	}
	claims, err := identitycontext.VerifyDelegation(h.identity.config.DelegationSigner.Public(), request.Header.Get("X-Delegation-Grant"))
	if err != nil || claims.TenantID == "" || claims.PolicyRevision != h.identity.config.PolicyRevision {
		writeProblem(writer, request, http.StatusUnauthorized, "OPERATIONS_DELEGATION_INVALID", "Operations delegation invalid", "The Operations service delegation is invalid.", false, nil)
		return
	}
	acceptableScopes := []string{"site:" + siteID, "session:" + claims.SessionID}
	if identitycontext.ValidateDelegationAnyScope(claims, h.identity.now(), h.identity.config.ExecutingWorkloadSPIFFE, h.operations.audience, "operations:investigate", acceptableScopes) != nil {
		writeProblem(writer, request, http.StatusForbidden, "OPERATIONS_DELEGATION_REJECTED", "Operations delegation rejected", "The Operations service delegation does not authorize this Site.", false, nil)
		return
	}
	stored, err := h.identity.store.GetSession(request.Context(), claims.SessionID)
	if err != nil || stored.RevokedAt != nil || !h.identity.now().Before(stored.ExpiresAt) || stored.TenantID != claims.TenantID || stored.Principal.Subject != claims.Subject || stored.Principal.Issuer != claims.SubjectIssuer {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	session := bffSession{Session: stored}
	if alarmList {
		// The Agent reads the list from the alarm owner itself, with a context naming it (#594).
		if h.alarm == nil {
			writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_AUTHORIZATION_UNAVAILABLE", "Operations authorization unavailable", "The Alarm Service is not configured.", true, nil)
			return
		}
		route := publicAlarmRoute{template: "/api/v1/alarms", action: alarmauth.ActionRead, siteID: siteID}
		decision, failure := h.authorizeAlarm(request, session, route)
		if failure == nil {
			var grant string
			grant, failure = h.signAlarmServiceContext(session, route, decision, h.operations.workloadSPIFFEID)
			if failure == nil {
				writeJSON(writer, http.StatusOK, operationsToolAuthorizationResponse{DelegationGrant: grant, PolicyRevision: decision.PolicyRevision})
				return
			}
		}
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		} else {
			h.writeAlarmFailure(writer, request, *failure)
		}
		return
	}
	if input.Request.Tool == "telemetry.current.getDeviceObservationSnapshot" || input.Request.Tool == "telemetry.history.aggregateDeviceHistory" {
		caller := telemetryCaller{
			principal: session.Principal,
			tenantID:  session.TenantID,
			contextID: session.ID,
			expiresAt: session.ExpiresAt,
		}
		action := telemetryauth.ActionSnapshotRead
		if input.Request.Tool == "telemetry.history.aggregateDeviceHistory" {
			action = telemetryauth.ActionHistoryRead
		}
		authorization, failure := h.authorizeTelemetryForPresenter(
			request.Context(), request, caller, action,
			[]telemetryauth.Target{telemetryTarget}, h.operations.workloadSPIFFEID,
		)
		if failure != nil {
			if failure.status == http.StatusNotFound || failure.status == http.StatusForbidden {
				writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested telemetry resource was not found.", false, nil)
			} else {
				writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
			}
			return
		}
		if len(authorization.targets) != 1 || authorization.targets[0].TenantID != claims.TenantID || authorization.targets[0].SiteID != siteID || authorization.targets[0].DeviceID != telemetryTarget.DeviceID {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested telemetry resource was not found.", false, nil)
			return
		}
		grant := authorization.grant
		if action == telemetryauth.ActionHistoryRead {
			// History is answered by the Telemetry Query Service, which accepts a Gateway-signed
			// grant bound to the exact aggregate query and presented by the Agent.
			if historyQuery.TenantID != claims.TenantID {
				writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested telemetry resource was not found.", false, nil)
				return
			}
			signed, failure := h.signHistoryQueryGrant(caller, authorization, claims.TenantID, telemetryhistorymodel.DeviceHistoryAggregateAction, historyQuery.ScopeDigest, h.operations.workloadSPIFFEID)
			if failure != nil {
				writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
				return
			}
			grant = signed
		}
		writeJSON(writer, http.StatusOK, operationsToolAuthorizationResponse{DelegationGrant: grant, PolicyRevision: authorization.policyRevision})
		return
	}
	if analytics != nil {
		grant, failure := h.authorizeAnalyticsForPresenter(
			request.Context(),
			request,
			session,
			*analytics,
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
	if failure != nil || !registryAuthorizationAllowsSite(authorization, siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	writeJSON(writer, http.StatusOK, operationsToolAuthorizationResponse{DelegationGrant: authorization.coreGrant, PolicyRevision: authorization.policyRevision})
}

func registryAuthorizationAllowsSite(authorization registryAuthorization, siteID string) bool {
	_, allowed := authorization.allowedSiteIDs[siteID]
	return allowed
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
