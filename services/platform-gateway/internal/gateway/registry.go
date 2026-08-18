package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

const (
	defaultRegistryTimeout           = 2 * time.Second
	defaultRegistryShadowTimeout     = 750 * time.Millisecond
	defaultRegistryAuditTimeout      = 500 * time.Millisecond
	defaultRegistryAuthorizationBody = int64(1 << 20)
	defaultRegistryBodyLimit         = int64(2 << 20)
	defaultRegistryShadowConcurrency = 32
)

var (
	registryCursorPattern   = regexp.MustCompile("^[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+$")
	errRegistryBodyTooLarge = errors.New("Registry response body is too large")
)

type RegistryConfig struct {
	CoreBaseURL         string
	CoreHTTPClient      *http.Client
	CoreTimeout         time.Duration
	ShadowTimeout       time.Duration
	MaxResponseBytes    int64
	MaxShadowConcurrent int
}

type registryController struct {
	coreBaseURL      string
	coreHTTPClient   *http.Client
	coreTimeout      time.Duration
	shadowTimeout    time.Duration
	maxResponseBytes int64
	shadowSlots      chan struct{}
}

type publicRegistryRoute struct {
	template     string
	internalPath string
	action       registryauth.Action
	scopeID      string
	list         bool
}

type registryBackendResult struct {
	owner        string
	status       int
	code         string
	body         []byte
	bodySHA256   string
	semanticHash string
	retryable    bool
}

type registryAuthorization struct {
	coreGrant      string
	allowedSiteIDs map[string]struct{}
	policyRevision string
}

type registryAuthorizationFailure struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

func newRegistryController(config *RegistryConfig) *registryController {
	if config == nil {
		return nil
	}
	resolved := *config
	resolved.CoreBaseURL = strings.TrimRight(strings.TrimSpace(resolved.CoreBaseURL), "/")
	if resolved.CoreHTTPClient == nil {
		resolved.CoreHTTPClient = &http.Client{}
	}
	if resolved.CoreTimeout <= 0 {
		resolved.CoreTimeout = defaultRegistryTimeout
	}
	if resolved.ShadowTimeout <= 0 {
		resolved.ShadowTimeout = defaultRegistryShadowTimeout
	}
	if resolved.MaxResponseBytes <= 0 || resolved.MaxResponseBytes > 16<<20 {
		resolved.MaxResponseBytes = defaultRegistryBodyLimit
	}
	if resolved.MaxShadowConcurrent <= 0 || resolved.MaxShadowConcurrent > 1024 {
		resolved.MaxShadowConcurrent = defaultRegistryShadowConcurrency
	}
	return &registryController{
		coreBaseURL:      resolved.CoreBaseURL,
		coreHTTPClient:   resolved.CoreHTTPClient,
		coreTimeout:      resolved.CoreTimeout,
		shadowTimeout:    resolved.ShadowTimeout,
		maxResponseBytes: resolved.MaxResponseBytes,
		shadowSlots:      make(chan struct{}, resolved.MaxShadowConcurrent),
	}
}

func (h *handler) ListSites(writer http.ResponseWriter, request *http.Request, params platformapi.ListRegistryParams) {
	h.serveRegistry(writer, request, publicRegistryRoute{
		template:     platformapi.ListSitesPath,
		internalPath: "/internal/v1/registry/sites",
		action:       registryauth.ActionSiteList,
		list:         true,
	}, params)
}

func (h *handler) GetSite(writer http.ResponseWriter, request *http.Request, siteID string) {
	h.serveRegistry(writer, request, publicRegistryRoute{
		template:     platformapi.GetSitePathTemplate,
		internalPath: "/internal/v1/registry/sites/" + siteID,
		action:       registryauth.ActionSiteRead,
	}, platformapi.ListRegistryParams{})
}

func (h *handler) ListSiteAsset(writer http.ResponseWriter, request *http.Request, siteID string, params platformapi.ListRegistryParams) {
	h.serveRegistry(writer, request, publicRegistryRoute{
		template:     platformapi.ListSiteAssetPathTemplate,
		internalPath: "/internal/v1/registry/sites/" + siteID + "/assets",
		action:       registryauth.ActionAssetList,
		list:         true,
	}, params)
}

func (h *handler) GetAsset(writer http.ResponseWriter, request *http.Request, assetID string) {
	h.serveRegistry(writer, request, publicRegistryRoute{
		template:     platformapi.GetAssetPathTemplate,
		internalPath: "/internal/v1/registry/assets/" + assetID,
		action:       registryauth.ActionAssetRead,
	}, platformapi.ListRegistryParams{})
}

func (h *handler) ListSiteDevices(writer http.ResponseWriter, request *http.Request, siteID string, params platformapi.ListRegistryParams) {
	h.serveRegistry(writer, request, publicRegistryRoute{
		template:     platformapi.ListSiteDevicesPathTemplate,
		internalPath: "/internal/v1/registry/sites/" + siteID + "/devices",
		action:       registryauth.ActionDeviceList,
		list:         true,
	}, params)
}

func (h *handler) ListSiteDeviceBindings(writer http.ResponseWriter, request *http.Request, siteID string, params platformapi.ListRegistryParams) {
	h.serveRegistry(writer, request, publicRegistryRoute{
		template:     platformapi.ListSiteDeviceBindingsPathTemplate,
		internalPath: "/internal/v1/registry/sites/" + siteID + "/device-bindings",
		action:       registryauth.ActionDeviceBindingList,
		scopeID:      siteID,
		list:         true,
	}, params)
}

func (h *handler) GetSiteAssetModel(writer http.ResponseWriter, request *http.Request, siteID string) {
	h.serveRegistry(writer, request, publicRegistryRoute{
		template:     platformapi.GetSiteAssetModelPathTemplate,
		internalPath: "/internal/v1/registry/sites/" + siteID + "/asset-model",
		action:       registryauth.ActionAssetModelRead,
		scopeID:      siteID,
	}, platformapi.ListRegistryParams{})
}

func (h *handler) GetDevice(writer http.ResponseWriter, request *http.Request, deviceID string) {
	h.serveRegistry(writer, request, publicRegistryRoute{
		template:     platformapi.GetDevicePathTemplate,
		internalPath: "/internal/v1/registry/devices/" + deviceID,
		action:       registryauth.ActionDeviceRead,
	}, platformapi.ListRegistryParams{})
}

func (h *handler) serveRegistry(writer http.ResponseWriter, request *http.Request, route publicRegistryRoute, params platformapi.ListRegistryParams) {
	decision := routeDecisionFromContext(request.Context())
	if decision.RegistryRevision == 0 || h.registry == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry route is not configured.", true, nil)
		return
	}
	session, ok := routeSessionFromContext(request.Context())
	if !ok {
		resolved, failure := h.identitySession(request)
		if failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return
		}
		session = resolved
	}
	authorization, authorizationFailure := h.authorizeRegistry(request.Context(), session, route.action)
	if authorizationFailure != nil {
		if authorizationFailure.status == http.StatusForbidden || authorizationFailure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
			return
		}
		writeProblem(writer, request, authorizationFailure.status, authorizationFailure.code, authorizationFailure.title, authorizationFailure.detail, authorizationFailure.retryable, nil)
		return
	}

	query := encodeRegistryQuery(params)
	if decision.SelectedOwner != ownershipregistry.OwnerCore {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry route decision is outside the V2 Core-only boundary.", true, nil)
		return
	}
	result := h.executeCoreRegistry(request.Context(), route, query, authorization.coreGrant, decision)
	writer.Header().Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	h.writeRegistryBackendResult(writer, request, result)
}

func (h *handler) authorizeRegistry(ctx context.Context, session bffSession, action registryauth.Action) (registryAuthorization, *registryAuthorizationFailure) {
	presenterSPIFFE := ""
	if h.identity != nil {
		presenterSPIFFE = h.identity.config.ExecutingWorkloadSPIFFE
	}
	return h.authorizeRegistryForPresenter(ctx, session, action, presenterSPIFFE)
}

func (h *handler) authorizeRegistryForPresenter(
	ctx context.Context,
	session bffSession,
	action registryauth.Action,
	presenterSPIFFE string,
) (registryAuthorization, *registryAuthorizationFailure) {
	if h.identity == nil || strings.TrimSpace(presenterSPIFFE) == "" {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM authorization is not configured.", true}
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer:               h.identity.config.ExecutingWorkloadSPIFFE,
		Subject:              session.Principal.Subject,
		SubjectIssuer:        session.Principal.Issuer,
		DisplayName:          session.Principal.DisplayName,
		Email:                session.Principal.Email,
		Roles:                append([]string(nil), session.Principal.Roles...),
		ExecutingService:     h.identity.config.ExecutingWorkloadSPIFFE,
		Audience:             h.identity.config.IAMAudience,
		TenantID:             session.TenantID,
		Actions:              []string{"registry:authorize"},
		Scopes:               []string{"session:" + session.ID},
		PolicyRevision:       h.identity.config.PolicyRevision,
		SessionID:            session.ID,
		IssuedAt:             now.Unix(),
		ExpiresAt:            expiresAt.Unix(),
		TokenID:              randomURLToken(16),
	}
	delegation, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry authorization request could not be signed.", true}
	}
	body, err := json.Marshal(registryauth.DecisionRequest{
		TenantID:       session.TenantID,
		Action:         action,
		GrantPresenter:       presenterSPIFFE,
	})
	if err != nil {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry authorization request could not be encoded.", true}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.identity.config.IAMURL, "/")+"/internal/v1/registry-read/decision", bytes.NewReader(body))
	if err != nil {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry authorization request could not be constructed.", true}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-Delegation-Grant", delegation)
	observability.InjectHTTP(ctx, request.Header)
	response, err := h.identity.config.IAMHTTPClient.Do(request)
	if err != nil {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM authorization is temporarily unavailable.", true}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		status := http.StatusServiceUnavailable
		retryable := true
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			status = http.StatusForbidden
			retryable = false
		}
		return registryAuthorization{}, &registryAuthorizationFailure{status, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM did not authorize the Registry request.", retryable}
	}
	decisionBody, err := readBoundedBody(response.Body, defaultRegistryAuthorizationBody)
	if err != nil {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM returned an oversized or unreadable Registry decision.", true}
	}
	var decision registryauth.DecisionResponse
	decoder := json.NewDecoder(bytes.NewReader(decisionBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decision) != nil || ensureRegistryJSONEOF(decoder) != nil {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM returned an invalid Registry decision.", true}
	}
	if !decision.Decision.Allowed {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusForbidden, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false}
	}
	if decision.Decision.TenantID != session.TenantID || decision.Decision.Subject != session.Principal.Subject || decision.Decision.SubjectIssuer != session.Principal.Issuer || decision.Decision.PrincipalID == "" || decision.Decision.PolicyRevision != h.identity.config.PolicyRevision || !registryauth.IsAllowReason(decision.Decision.ReasonCode) || len(decision.Decision.Actions) != 1 || decision.Decision.Actions[0] != action || decision.DelegationGrant == "" || len(decision.DelegationGrant) > registryauth.MaximumEncodedGrantSize {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM returned a Registry decision outside the authenticated boundary.", true}
	}
	if !structurallyValidRegistryGrant(decision.DelegationGrant) {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM returned a malformed Registry grant.", true}
	}
	allowedSites, err := validateExactSiteRegistryScope(decision.Decision)
	if err != nil {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM returned an invalid exact-Site Registry scope.", true}
	}
	return registryAuthorization{coreGrant: decision.DelegationGrant, allowedSiteIDs: allowedSites, policyRevision: decision.Decision.PolicyRevision}, nil
}

func validateExactSiteRegistryScope(decision registryauth.Decision) (map[string]struct{}, error) {
	allowedSites, err := validatedRegistryIDs(decision.AllowedSiteIDs)
	if err != nil {
		return nil, err
	}
	deniedSites, err := validatedRegistryIDs(decision.DeniedSiteIDs)
	if err != nil {
		return nil, err
	}
	if len(allowedSites) == 0 || len(allowedSites) > 256 {
		return nil, errors.New("Registry decision must contain an exact non-empty Site set")
	}
	if setsOverlap(allowedSites, deniedSites) {
		return nil, errors.New("Registry decision allowed and denied Site scopes overlap")
	}
	return allowedSites, nil
}

func validatedRegistryIDs(values []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !isLowerUUIDv7(value) {
			return nil, errors.New("Registry decision contains an invalid resource identifier")
		}
		if _, duplicate := result[value]; duplicate {
			return nil, errors.New("Registry decision contains a duplicate resource identifier")
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func setsOverlap(left, right map[string]struct{}) bool {
	for value := range left {
		if _, exists := right[value]; exists {
			return true
		}
	}
	return false
}

func (h *handler) executeCoreRegistry(ctx context.Context, route publicRegistryRoute, query, grant string, decision ownershipregistry.Decision) registryBackendResult {
	if h.registry == nil || h.registry.coreBaseURL == "" || h.registry.coreHTTPClient == nil {
		return unavailableRegistryResult(ownershipregistry.OwnerCore, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE")
	}
	requestContext, cancel := context.WithTimeout(ctx, h.registry.coreTimeout)
	defer cancel()
	endpoint := h.registry.coreBaseURL + route.internalPath
	if query != "" {
		endpoint += "?" + query
	}
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint, nil)
	if err != nil {
		return unavailableRegistryResult(ownershipregistry.OwnerCore, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE")
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-Delegation-Grant", grant)
	request.Header.Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	request.Header.Set("X-Request-ID", requestIDFromContext(ctx))
	observability.InjectHTTP(ctx, request.Header)
	response, err := h.registry.coreHTTPClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return unavailableRegistryResult(ownershipregistry.OwnerCore, http.StatusGatewayTimeout, "REGISTRY_TIMEOUT")
		}
		return unavailableRegistryResult(ownershipregistry.OwnerCore, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE")
	}
	defer response.Body.Close()
	return h.decodeRegistryBackendResponse(ownershipregistry.OwnerCore, response, route)
}

func (h *handler) decodeRegistryBackendResponse(owner string, response *http.Response, route publicRegistryRoute) registryBackendResult {
	raw, err := readBoundedBody(response.Body, h.registry.maxResponseBytes)
	if err != nil {
		if errors.Is(err, errRegistryBodyTooLarge) {
			return invalidRegistryResult(owner, "")
		}
		return unavailableRegistryResult(owner, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE")
	}
	result := registryBackendResult{owner: owner, status: response.StatusCode, bodySHA256: sha256Hex(raw)}
	if response.StatusCode == http.StatusOK {
		canonical, err := canonicalRegistrySuccess(route.action, route.scopeID, raw)
		if err != nil {
			return invalidRegistryResult(owner, result.bodySHA256)
		}
		result.body = canonical
		result.semanticHash = sha256Hex(canonical)
		return result
	}
	var problem platformapi.ProblemDetails
	_ = json.Unmarshal(raw, &problem)
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || problem.Code == "AUTHORIZATION_DENIED":
		result.status = http.StatusNotFound
		result.code = "AUTHORIZATION_DENIED"
	case response.StatusCode == http.StatusNotFound:
		result.status = http.StatusNotFound
		result.code = "RESOURCE_NOT_FOUND"
	case response.StatusCode == http.StatusBadRequest && problem.Code == "CURSOR_INVALID":
		result.status = http.StatusBadRequest
		result.code = "CURSOR_INVALID"
	case response.StatusCode == http.StatusConflict && (problem.Code == "MAPPING_INVALID" || problem.Code == "MAPPING_QUARANTINED"):
		result.status = http.StatusConflict
		result.code = problem.Code
	case response.StatusCode == http.StatusGatewayTimeout:
		result.status = http.StatusGatewayTimeout
		result.code = "REGISTRY_TIMEOUT"
		result.retryable = true
	case response.StatusCode >= http.StatusInternalServerError:
		result.status = http.StatusServiceUnavailable
		result.code = "REGISTRY_UNAVAILABLE"
		result.retryable = true
	default:
		result.status = http.StatusServiceUnavailable
		result.code = "REGISTRY_UNAVAILABLE"
	}
	result.semanticHash = sha256Hex([]byte(fmt.Sprintf("%d:%s", result.status, result.code)))
	return result
}

func (h *handler) writeRegistryBackendResult(writer http.ResponseWriter, request *http.Request, result registryBackendResult) {
	if result.status == http.StatusOK {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(append(result.body, '\n'))
		return
	}
	switch result.code {
	case "AUTHORIZATION_DENIED", "RESOURCE_NOT_FOUND":
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
	case "CURSOR_INVALID":
		writeProblem(writer, request, http.StatusBadRequest, result.code, "Cursor invalid", "The Registry page cursor or limit is invalid.", false, nil)
	case "MAPPING_INVALID", "MAPPING_QUARANTINED":
		writeProblem(writer, request, http.StatusConflict, result.code, "Registry mapping unavailable", "The Registry resource mapping is not available for public reads.", false, nil)
	case "REGISTRY_TIMEOUT":
		writeProblem(writer, request, http.StatusGatewayTimeout, result.code, "Registry timeout", "The Registry request did not complete before the Gateway deadline.", true, nil)
	default:
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry is temporarily unavailable.", result.retryable, nil)
	}
}

func parseRegistryListParams(writer http.ResponseWriter, request *http.Request) (platformapi.ListRegistryParams, bool) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "Registry query invalid", "The Registry query string is malformed.", false, nil)
		return platformapi.ListRegistryParams{}, false
	}
	for name, values := range query {
		if (name != "limit" && name != "cursor") || len(values) != 1 {
			writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "Registry query invalid", "The Registry query parameters are invalid.", false, []platformapi.FieldError{{Field: name, Message: "unsupported or duplicate query parameter"}})
			return platformapi.ListRegistryParams{}, false
		}
	}
	params := platformapi.ListRegistryParams{Cursor: query.Get("cursor")}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "Registry query invalid", "The Registry page limit is invalid.", false, []platformapi.FieldError{{Field: "limit", Message: "must be an integer from 1 through 200"}})
			return platformapi.ListRegistryParams{}, false
		}
		params.Limit = &limit
	}
	if params.Cursor != "" && (len(params.Cursor) < 16 || len(params.Cursor) > 4096 || !registryCursorPattern.MatchString(params.Cursor)) {
		writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "Cursor invalid", "The Registry page cursor is invalid.", false, []platformapi.FieldError{{Field: "cursor", Message: "invalid opaque cursor"}})
		return platformapi.ListRegistryParams{}, false
	}
	return params, true
}

func matchPublicRegistryRoute(path string) (publicRegistryRoute, string, bool) {
	if path == platformapi.ListSitesPath {
		return publicRegistryRoute{template: platformapi.ListSitesPath, internalPath: "/internal/v1/registry/sites", action: registryauth.ActionSiteList, list: true}, "", true
	}
	patterns := []struct {
		template string
		marker   string
		action   registryauth.Action
		internal func(string) string
		list     bool
	}{
		{platformapi.GetSitePathTemplate, "{siteId}", registryauth.ActionSiteRead, func(id string) string { return "/internal/v1/registry/sites/" + id }, false},
		{platformapi.ListSiteAssetPathTemplate, "{siteId}", registryauth.ActionAssetList, func(id string) string { return "/internal/v1/registry/sites/" + id + "/assets" }, true},
		{platformapi.GetAssetPathTemplate, "{assetId}", registryauth.ActionAssetRead, func(id string) string { return "/internal/v1/registry/assets/" + id }, false},
		{platformapi.ListSiteDevicesPathTemplate, "{siteId}", registryauth.ActionDeviceList, func(id string) string { return "/internal/v1/registry/sites/" + id + "/devices" }, true},
		{platformapi.ListSiteDeviceBindingsPathTemplate, "{siteId}", registryauth.ActionDeviceBindingList, func(id string) string { return "/internal/v1/registry/sites/" + id + "/device-bindings" }, true},
		{platformapi.GetSiteAssetModelPathTemplate, "{siteId}", registryauth.ActionAssetModelRead, func(id string) string { return "/internal/v1/registry/sites/" + id + "/asset-model" }, false},
		{platformapi.GetDevicePathTemplate, "{deviceId}", registryauth.ActionDeviceRead, func(id string) string { return "/internal/v1/registry/devices/" + id }, false},
	}
	for _, pattern := range patterns {
		id, ok := matchSinglePathParameter(path, pattern.template, pattern.marker)
		if !ok {
			continue
		}
		return publicRegistryRoute{template: pattern.template, internalPath: pattern.internal(id), action: pattern.action, scopeID: id, list: pattern.list}, id, true
	}
	return publicRegistryRoute{}, "", false
}

func dispatchRegistryRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicRegistryRoute, id string) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowedFor(writer, request, http.MethodGet)
		return
	}
	if id != "" && !isLowerUUIDv7(id) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
		return
	}
	params := platformapi.ListRegistryParams{}
	if route.list {
		var ok bool
		params, ok = parseRegistryListParams(writer, request)
		if !ok {
			return
		}
	} else if request.URL.RawQuery != "" {
		writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "Registry query invalid", "Detail Registry routes do not accept query parameters.", false, nil)
		return
	}
	switch route.action {
	case registryauth.ActionSiteList:
		h.ListSites(writer, request, params)
	case registryauth.ActionSiteRead:
		h.GetSite(writer, request, id)
	case registryauth.ActionAssetList:
		h.ListSiteAsset(writer, request, id, params)
	case registryauth.ActionAssetRead:
		h.GetAsset(writer, request, id)
	case registryauth.ActionDeviceList:
		h.ListSiteDevices(writer, request, id, params)
	case registryauth.ActionDeviceBindingList:
		h.ListSiteDeviceBindings(writer, request, id, params)
	case registryauth.ActionAssetModelRead:
		h.GetSiteAssetModel(writer, request, id)
	case registryauth.ActionDeviceRead:
		h.GetDevice(writer, request, id)
	default:
		writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested public API route does not exist.", false, nil)
	}
}

func canonicalRegistrySuccess(action registryauth.Action, scopeID string, raw []byte) ([]byte, error) {
	switch action {
	case registryauth.ActionSiteList:
		return decodeCanonical[platformapi.SiteCollection](raw, validateSiteCollection)
	case registryauth.ActionSiteRead:
		return decodeCanonical[platformapi.Site](raw, validateSite)
	case registryauth.ActionAssetList:
		return decodeCanonical[platformapi.AssetCollection](raw, validateAssetCollection)
	case registryauth.ActionAssetRead:
		return decodeCanonical[platformapi.Asset](raw, validateAsset)
	case registryauth.ActionDeviceList:
		return decodeCanonical[platformapi.DeviceCollection](raw, validateDeviceCollection)
	case registryauth.ActionDeviceBindingList:
		return decodeCanonical[platformapi.DeviceBindingCollection](raw, func(value platformapi.DeviceBindingCollection) error {
			return validateDeviceBindingCollection(value, scopeID)
		})
	case registryauth.ActionAssetModelRead:
		return decodeCanonical[platformapi.SiteAssetModel](raw, func(value platformapi.SiteAssetModel) error {
			return validateSiteAssetModel(value, scopeID)
		})
	case registryauth.ActionDeviceRead:
		return decodeCanonical[platformapi.Device](raw, validateDevice)
	default:
		return nil, errors.New("unsupported Registry action")
	}
}

func decodeCanonical[T any](raw []byte, validate func(T) error) ([]byte, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || ensureRegistryJSONEOF(decoder) != nil {
		return nil, errors.New("invalid Registry response")
	}
	if err := validate(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func validateSiteCollection(value platformapi.SiteCollection) error {
	for _, item := range value.Items {
		if err := validateSite(item); err != nil {
			return err
		}
	}
	return validateNextCursor(value.NextCursor)
}

func validateAssetCollection(value platformapi.AssetCollection) error {
	for _, item := range value.Items {
		if err := validateAsset(item); err != nil {
			return err
		}
	}
	return validateNextCursor(value.NextCursor)
}

func validateDeviceCollection(value platformapi.DeviceCollection) error {
	for _, item := range value.Items {
		if err := validateDevice(item); err != nil {
			return err
		}
	}
	return validateNextCursor(value.NextCursor)
}

func validateDeviceBindingCollection(value platformapi.DeviceBindingCollection, expectedSiteID string) error {
	if !isLowerUUIDv7(expectedSiteID) {
		return errors.New("invalid DeviceBinding request scope")
	}
	for _, item := range value.Items {
		if err := validateDeviceBinding(item); err != nil {
			return err
		}
		if item.SiteID != expectedSiteID {
			return errors.New("DeviceBinding response escaped requested Site scope")
		}
	}
	return validateNextCursor(value.NextCursor)
}

func (h *handler) resolveAuthoritativeSiteForDomain(request *http.Request, session bffSession, siteID string) (platformapi.Site, error) {
	if request == nil || h.registry == nil || strings.TrimSpace(h.registry.coreBaseURL) == "" || h.registry.coreHTTPClient == nil || !isLowerUUIDv7(siteID) {
		return platformapi.Site{}, errors.New("authoritative Registry Site lookup is unavailable")
	}
	authorization, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if failure != nil {
		return platformapi.Site{}, errors.New("Registry Site authorization failed")
	}
	publicPath := strings.Replace(platformapi.GetSitePathTemplate, "{siteId}", url.PathEscape(siteID), 1)
	route, _, matches := matchPublicRegistryRoute(publicPath)
	if !matches {
		return platformapi.Site{}, errors.New("Registry Site route is unavailable")
	}
	outer := routeDecisionFromContext(request.Context())
	decision := ownershipregistry.Decision{
		RouteKey:          http.MethodGet + " " + route.template,
		PathTemplate:      route.template,
		SelectedOwner:     ownershipregistry.OwnerCore,
		RegistryRevision:  outer.RegistryRevision,
		RouteRevision:     1,
		CompatibilityMode: "native",
	}
	if h.routeManager != nil {
		resolved, err := h.routeManager.Current().Resolve(http.MethodGet, publicPath, session.TenantID)
		if err != nil || resolved.SelectedOwner != ownershipregistry.OwnerCore {
			return platformapi.Site{}, errors.New("Registry Site route ownership is unavailable")
		}
		decision = resolved
	}
	result := h.executeCoreRegistry(request.Context(), route, "", authorization.coreGrant, decision)
	if result.status != http.StatusOK {
		return platformapi.Site{}, errors.New("Registry Site lookup failed")
	}
	var site platformapi.Site
	decoder := json.NewDecoder(bytes.NewReader(result.body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&site) != nil || ensureRegistryJSONEOF(decoder) != nil || validateSite(site) != nil || site.ID != siteID || !isLowerUUIDv7(site.TenantID) {
		return platformapi.Site{}, errors.New("Registry returned an invalid authoritative Site")
	}
	return site, nil
}

func validateSiteAssetModel(value platformapi.SiteAssetModel, expectedSiteID string) error {
	if value.SchemaVersion != 2 || !isLowerUUIDv7(value.TenantID) || value.SiteID != expectedSiteID || !isLowerUUIDv7(expectedSiteID) {
		return errors.New("invalid Site asset model scope")
	}
	identities := map[string]string{expectedSiteID: "SITE"}
	for _, item := range value.Spaces {
		if err := validateSpace(item, expectedSiteID); err != nil {
			return err
		}
		if item.TenantID != value.TenantID {
			return errors.New("Space escaped Site asset model Tenant scope")
		}
		if _, duplicate := identities[item.ID]; duplicate {
			return errors.New("duplicate Site asset model identity")
		}
		identities[item.ID] = "SPACE"
	}
	for _, item := range value.Assets {
		if err := validateAsset(item); err != nil || item.SiteID != expectedSiteID || item.TenantID != value.TenantID {
			return errors.New("invalid Site asset model Asset")
		}
		if _, duplicate := identities[item.ID]; duplicate {
			return errors.New("duplicate Site asset model identity")
		}
		identities[item.ID] = "ASSET"
	}
	for _, item := range value.Devices {
		if err := validateDevice(item); err != nil || item.SiteID != expectedSiteID || item.TenantID != value.TenantID {
			return errors.New("invalid Site asset model Device")
		}
		if _, duplicate := identities[item.ID]; duplicate {
			return errors.New("duplicate Site asset model identity")
		}
		identities[item.ID] = "DEVICE"
	}
	for _, item := range value.Sensors {
		if err := validateSensor(item, expectedSiteID); err != nil {
			return err
		}
		if item.TenantID != value.TenantID {
			return errors.New("Sensor escaped Site asset model Tenant scope")
		}
		if _, duplicate := identities[item.ID]; duplicate {
			return errors.New("duplicate Site asset model identity")
		}
		identities[item.ID] = "SENSOR"
	}
	for _, item := range value.TelemetryPoints {
		if err := validateTelemetryPoint(item, expectedSiteID); err != nil {
			return err
		}
		if item.TenantID != value.TenantID {
			return errors.New("Telemetry Point escaped Site asset model Tenant scope")
		}
		if _, duplicate := identities[item.ID]; duplicate {
			return errors.New("duplicate Site asset model identity")
		}
		if identities[item.ReportingDeviceID] != "DEVICE" {
			return errors.New("Telemetry Point references a Device outside the Site asset model")
		}
		if item.SensorID != nil && identities[*item.SensorID] != "SENSOR" {
			return errors.New("Telemetry Point references a Sensor outside the Site asset model")
		}
		identities[item.ID] = "POINT"
	}
	for _, area := range value.Spaces {
		if area.ParentSpaceID != nil && identities[*area.ParentSpaceID] != "SPACE" {
			return errors.New("Space parent escaped the Site asset model")
		}
	}
	for _, relationship := range value.Relationships {
		if err := validateAssetRelationship(relationship, expectedSiteID); err != nil {
			return err
		}
		if relationship.TenantID != value.TenantID {
			return errors.New("Asset relationship escaped Site asset model Tenant scope")
		}
		if identities[relationship.FromID] != relationship.FromType || identities[relationship.ToID] != relationship.ToType {
			return errors.New("Asset relationship references an unknown or mismatched identity")
		}
	}
	if value.Counts.Spaces != len(value.Spaces) || value.Counts.Assets != len(value.Assets) || value.Counts.DeviceEndpoints != len(value.Devices) || value.Counts.PhysicalSensors != len(value.Sensors) || value.Counts.Points != len(value.TelemetryPoints) {
		return errors.New("Site asset model counts do not match the payload")
	}
	return nil
}

func validateSpace(value platformapi.Space, expectedSiteID string) error {
	if !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || value.SiteID != expectedSiteID || !validRegistryString(value.Code, 64) || !validRegistryString(value.DisplayName, 256) || !oneOf(value.SpaceType, "CAMPUS", "BUILDING", "FLOOR", "ZONE", "ROOM", "PLANT_ROOM", "ROOFTOP", "OUTDOOR", "TENANT_SPACE", "OTHER") || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED") || value.Revision < 1 || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt) {
		return errors.New("invalid Space response")
	}
	if value.ParentSpaceID != nil && !isLowerUUIDv7(*value.ParentSpaceID) {
		return errors.New("invalid Space parent")
	}
	return nil
}

func validateSensor(value platformapi.Sensor, expectedSiteID string) error {
	if !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || value.SiteID != expectedSiteID || !validRegistryString(value.Code, 256) || !validRegistryString(value.DisplayName, 256) || !validRegistryString(value.SensorType, 128) || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED") || value.Revision < 1 || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt) {
		return errors.New("invalid Sensor response")
	}
	if value.CalibrationDueAt != nil && !validRegistryInstant(*value.CalibrationDueAt) {
		return errors.New("invalid Sensor calibration due time")
	}
	return nil
}

func validateTelemetryPoint(value platformapi.TelemetryPoint, expectedSiteID string) error {
	if !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || value.SiteID != expectedSiteID || !isLowerUUIDv7(value.ReportingDeviceID) || !validPointCode(value.PointCode) || !validRegistryString(value.SourceKey, 128) || !validRegistryString(value.DisplayName, 256) || !oneOf(value.PointType, "TELEMETRY", "COUNTER", "STATE", "SETTING", "COMMAND") || !oneOf(value.ValueType, "BOOLEAN", "NUMBER", "STRING", "JSON") || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED") || value.Revision < 1 || value.SampleIntervalMS < 100 || value.PublishIntervalMS < value.SampleIntervalMS || value.StaleAfterMS < value.PublishIntervalMS || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt) {
		return errors.New("invalid Telemetry Point response")
	}
	if value.SensorID != nil && !isLowerUUIDv7(*value.SensorID) {
		return errors.New("invalid Telemetry Point Sensor")
	}
	if value.PointType == "COMMAND" && !value.Writable {
		return errors.New("invalid Telemetry Point authority")
	}
	if value.PointType != "COMMAND" && value.PointType != "SETTING" && value.Writable {
		return errors.New("invalid Telemetry Point authority")
	}
	return nil
}

func validateAssetRelationship(value platformapi.AssetRelationship, expectedSiteID string) error {
	if !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || value.SiteID != expectedSiteID || !isLowerUUIDv7(value.FromID) || !isLowerUUIDv7(value.ToID) || !oneOf(value.FromType, "ASSET", "DEVICE", "SENSOR", "POINT") || !oneOf(value.ToType, "SITE", "SPACE", "ASSET", "DEVICE", "SENSOR", "POINT") || !validRegistryString(value.Role, 128) || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED") || !validRegistryInstant(value.ValidFrom) || (value.ValidTo != nil && !validRegistryInstant(*value.ValidTo)) || value.Revision < 1 || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt) {
		return errors.New("invalid Asset relationship")
	}
	return nil
}

func validateSite(value platformapi.Site) error {
	if !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || !validRegistryString(value.Code, 128) || !validRegistryString(value.DisplayName, 256) || !validRegistryTimezone(value.Timezone) || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED") || value.Revision < 1 || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt) {
		return errors.New("invalid Site response")
	}
	return nil
}

func validateAsset(value platformapi.Asset) error {
	if !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || !isLowerUUIDv7(value.SiteID) || !validRegistryString(value.Code, 128) || !validRegistryString(value.DisplayName, 256) || !validRegistryString(value.AssetType, 128) || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED") || value.Revision < 1 || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt) {
		return errors.New("invalid Asset response")
	}
	return nil
}

func validateDevice(value platformapi.Device) error {
	if !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || !isLowerUUIDv7(value.SiteID) || !validRegistryString(value.Code, 128) || !validRegistryString(value.DisplayName, 256) || !validRegistryString(value.DeviceType, 128) || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED") || value.Revision < 1 || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt) {
		return errors.New("invalid Device response")
	}
	return nil
}

func validateDeviceBinding(value platformapi.DeviceBinding) error {
	invalidIdentity := !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || !isLowerUUIDv7(value.SiteID) || !isLowerUUIDv7(value.DeviceID) || !isLowerUUIDv7(value.AssetID)
	invalidLifecycle := !validRegistryString(value.BindingRole, 128) || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED")
	invalidValidity := !validRegistryInstant(value.ValidFrom) || (value.ValidTo != nil && !validRegistryInstant(*value.ValidTo))
	invalidRevision := value.Revision < 1 || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt)
	if invalidIdentity || invalidLifecycle || invalidValidity || invalidRevision {
		return errors.New("invalid DeviceBinding response")
	}
	return nil
}

func validRegistryString(value string, maximum int) bool {
	return len(value) >= 1 && len(value) <= maximum
}

func validPointCode(value string) bool {
	if len(value) < 1 || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func validRegistryInstant(value string) bool {
	parsed, err := time.Parse("2006-01-02T15:04:05.000Z", value)
	return err == nil && parsed.UTC().Format("2006-01-02T15:04:05.000Z") == value
}

func validRegistryTimezone(value string) bool {
	if len(value) < 3 || len(value) > 128 {
		return false
	}
	_, err := time.LoadLocation(value)
	return err == nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func validateNextCursor(value *string) error {
	if value == nil {
		return nil
	}
	if len(*value) < 16 || len(*value) > 4096 || !registryCursorPattern.MatchString(*value) {
		return errors.New("invalid next cursor")
	}
	return nil
}

func ensureRegistryJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("additional JSON value is not allowed")
}

func readBoundedBody(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errRegistryBodyTooLarge
	}
	return body, nil
}

func unavailableRegistryResult(owner string, status int, code string) registryBackendResult {
	emptyHash := sha256Hex(nil)
	return registryBackendResult{owner: owner, status: status, code: code, bodySHA256: emptyHash, semanticHash: sha256Hex([]byte(fmt.Sprintf("%d:%s", status, code))), retryable: true}
}

func invalidRegistryResult(owner, bodySHA256 string) registryBackendResult {
	if bodySHA256 == "" {
		bodySHA256 = sha256Hex(nil)
	}
	return registryBackendResult{
		owner:        owner,
		status:       http.StatusServiceUnavailable,
		code:         "REGISTRY_UNAVAILABLE",
		bodySHA256:   bodySHA256,
		semanticHash: sha256Hex([]byte(fmt.Sprintf("%d:%s", http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE"))),
	}
}

func encodeRegistryQuery(params platformapi.ListRegistryParams) string {
	query := url.Values{}
	if params.Limit != nil {
		query.Set("limit", strconv.Itoa(*params.Limit))
	}
	if params.Cursor != "" {
		query.Set("cursor", params.Cursor)
	}
	return query.Encode()
}

func routePublicPath(route publicRegistryRoute) string {
	return strings.Replace(route.internalPath, "/internal/v1/registry", "/api/v1", 1)
}

func structurallyValidRegistryGrant(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := base64.RawURLEncoding.DecodeString(part); err != nil {
			return false
		}
	}
	return true
}

func isLowerUUIDv7(value string) bool {
	if value != strings.ToLower(value) || len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded := make([]byte, 16)
	if _, err := hex.Decode(decoded, []byte(compact)); err != nil {
		return false
	}
	return decoded[6]>>4 == 7 && decoded[8]>>6 == 2
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
