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
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
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
	registryCursorPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)
	errRegistryBodyTooLarge = errors.New("registry response body is too large")
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
	resource     string
	action       registryauth.Action
	altActions   []registryauth.Action
	scopeID      string
	list         bool
	write        bool
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
	principalID    string
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
		resolved.CoreHTTPClient = &http.Client{Timeout: 10 * time.Second}
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

func (h *handler) ListSiteAssets(writer http.ResponseWriter, request *http.Request, siteID string, params platformapi.ListRegistryParams) {
	h.serveRegistry(writer, request, publicRegistryRoute{
		template:     platformapi.ListSiteAssetsPathTemplate,
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
		resource:     "asset-model",
		action:       registryauth.ActionAssetModelRead,
		scopeID:      siteID,
	}, platformapi.ListRegistryParams{})
}

func (h *handler) GetSiteTariff(writer http.ResponseWriter, request *http.Request, siteID string) {
	h.serveRegistry(writer, request, publicRegistryRoute{
		template:     platformapi.GetSiteTariffPathTemplate,
		internalPath: "/internal/v1/registry/sites/" + siteID + "/tariff",
		action:       registryauth.ActionTariffRead,
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
	h.serveRegistryWithQuery(writer, request, route, params, nil)
}

func (h *handler) serveRegistryWithQuery(writer http.ResponseWriter, request *http.Request, route publicRegistryRoute, params platformapi.ListRegistryParams, extraQuery url.Values) {
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
	authorization, authorizationFailure := h.authorizeRegistryCandidates(request.Context(), session, route.action, route.altActions...)
	if authorizationFailure != nil {
		if authorizationFailure.status == http.StatusForbidden || authorizationFailure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
			return
		}
		writeProblem(writer, request, authorizationFailure.status, authorizationFailure.code, authorizationFailure.title, authorizationFailure.detail, authorizationFailure.retryable, nil)
		return
	}
	if route.resource == "asset-model" && route.scopeID != "" {
		if _, allowed := authorization.allowedSiteIDs[route.scopeID]; !allowed {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
			return
		}
	}

	values, _ := url.ParseQuery(encodeRegistryQuery(params))
	for name, list := range extraQuery {
		for _, item := range list {
			values.Add(name, item)
		}
	}
	if route.resource == "space-children" && !values.Has("parentSpaceId") {
		if parentSpaceID := request.URL.Query().Get("parentSpaceId"); parentSpaceID != "" {
			values.Set("parentSpaceId", parentSpaceID)
		}
	}
	query := values.Encode()
	if decision.SelectedOwner != ownershipregistry.OwnerCore {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry route decision is outside the V2 Core-only boundary.", true, nil)
		return
	}
	result := h.executeCoreRegistry(request.Context(), route, query, authorization.coreGrant, decision)
	writer.Header().Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	if route.resource == "asset-model" && result.status == http.StatusOK && request.Method == http.MethodGet {
		h.writeAssetModelResult(writer, request, result)
		return
	}
	h.writeRegistryBackendResult(writer, request, result)
}

func (h *handler) authorizeRegistry(ctx context.Context, session bffSession, action registryauth.Action) (registryAuthorization, *registryAuthorizationFailure) {
	presenterSPIFFE := ""
	if h.identity != nil {
		presenterSPIFFE = h.identity.config.ExecutingWorkloadSPIFFE
	}
	return h.authorizeRegistryForPresenter(ctx, session, action, presenterSPIFFE)
}

func (h *handler) authorizeRegistryCandidates(ctx context.Context, session bffSession, primary registryauth.Action, alts ...registryauth.Action) (registryAuthorization, *registryAuthorizationFailure) {
	auth, failure := h.authorizeRegistry(ctx, session, primary)
	if failure == nil {
		return auth, nil
	}
	if failure.status != http.StatusForbidden {
		return auth, failure
	}
	for _, candidate := range alts {
		if candidate == "" || candidate == primary {
			continue
		}
		altAuth, altFailure := h.authorizeRegistry(ctx, session, candidate)
		if altFailure == nil {
			return altAuth, nil
		}
		if altFailure.status != http.StatusForbidden {
			return altAuth, altFailure
		}
		failure = altFailure
	}
	return registryAuthorization{}, failure
}

func (h *handler) authorizeRegistryForPresenter(
	ctx context.Context,
	session bffSession,
	action registryauth.Action,
	presenterSPIFFE string,
) (registryAuthorization, *registryAuthorizationFailure) {
	return h.authorizeRegistryForTenant(ctx, session, session.TenantID, action, presenterSPIFFE)
}

func (h *handler) authorizeRegistryForTenant(
	ctx context.Context,
	session bffSession,
	targetTenantID string,
	action registryauth.Action,
	presenterSPIFFE string,
) (registryAuthorization, *registryAuthorizationFailure) {
	if h.identity == nil || strings.TrimSpace(presenterSPIFFE) == "" {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM authorization is not configured.", true}
	}
	effectiveTenantID := strings.TrimSpace(targetTenantID)
	if effectiveTenantID == "" {
		effectiveTenantID = session.TenantID
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer:           h.identity.config.ExecutingWorkloadSPIFFE,
		Subject:          session.Principal.Subject,
		SubjectIssuer:    session.Principal.Issuer,
		DisplayName:      session.Principal.DisplayName,
		Email:            session.Principal.Email,
		Roles:            append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience:         h.identity.config.IAMAudience,
		TenantID:         session.TenantID,
		Actions:          []string{"registry:authorize"},
		Scopes:           []string{"session:" + session.ID},
		PolicyRevision:   h.identity.config.PolicyRevision,
		SessionID:        session.ID,
		IssuedAt:         now.Unix(),
		ExpiresAt:        expiresAt.Unix(),
		TokenID:          randomURLToken(16),
	}
	delegation, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry authorization request could not be signed.", true}
	}
	body, err := json.Marshal(registryauth.DecisionRequest{
		TenantID:       effectiveTenantID,
		Action:         action,
		GrantPresenter: presenterSPIFFE,
	})
	if err != nil {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry authorization request could not be encoded.", true}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.identity.config.IAMURL, "/")+"/internal/v1/registry/decision", bytes.NewReader(body))
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
	defer drainAndCloseBody(response.Body)
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
	if decision.Decision.TenantID != effectiveTenantID || decision.Decision.Subject != session.Principal.Subject || decision.Decision.SubjectIssuer != session.Principal.Issuer || decision.Decision.PrincipalID == "" || strings.TrimSpace(decision.Decision.PolicyRevision) == "" || !registryauth.IsAllowReason(decision.Decision.ReasonCode) || len(decision.Decision.Actions) != 1 || decision.Decision.Actions[0] != action || decision.DelegationGrant == "" || len(decision.DelegationGrant) > registryauth.MaximumEncodedGrantSize {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM returned a Registry decision outside the authenticated boundary.", true}
	}
	if !structurallyValidRegistryGrant(decision.DelegationGrant) {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM returned a malformed Registry grant.", true}
	}
	allowedSites, err := validateExactSiteRegistryScope(decision.Decision, action)
	if err != nil {
		return registryAuthorization{}, &registryAuthorizationFailure{http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "IAM returned an invalid exact-Site Registry scope.", true}
	}
	return registryAuthorization{coreGrant: decision.DelegationGrant, principalID: decision.Decision.PrincipalID, allowedSiteIDs: allowedSites, policyRevision: decision.Decision.PolicyRevision}, nil
}

func validateExactSiteRegistryScope(decision registryauth.Decision, action registryauth.Action) (map[string]struct{}, error) {
	allowedSites, err := validatedRegistryIDs(decision.AllowedSiteIDs)
	if err != nil {
		return nil, err
	}
	deniedSites, err := validatedRegistryIDs(decision.DeniedSiteIDs)
	if err != nil {
		return nil, err
	}
	if action.SiteScoped() && (len(allowedSites) == 0 || len(allowedSites) > 256) {
		return nil, errors.New("registry decision must contain an exact non-empty Site set")
	}
	if action.TenantScoped() && len(allowedSites) > 256 {
		return nil, errors.New("registry decision Site set exceeds the supported bound")
	}
	if setsOverlap(allowedSites, deniedSites) {
		return nil, errors.New("registry decision allowed and denied Site scopes overlap")
	}
	return allowedSites, nil
}

func validatedRegistryIDs(values []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !isLowerUUIDv7(value) {
			return nil, errors.New("registry decision contains an invalid resource identifier")
		}
		if _, duplicate := result[value]; duplicate {
			return nil, errors.New("registry decision contains a duplicate resource identifier")
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
	defer drainAndCloseBody(response.Body)
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
	if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusCreated {
		var canonical []byte
		var err error
		if route.write || route.resource == "space-children" || route.resource == "device-points" || route.resource == "total-electricity-sources" {
			canonical, err = canonicalRegistryDocument(raw)
		} else {
			canonical, err = canonicalRegistrySuccessForRoute(route, raw)
		}
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
	case response.StatusCode == http.StatusBadRequest && (problem.Code == "CURSOR_INVALID" || problem.Code == "REGISTRY_MUTATION_INVALID"):
		result.status = http.StatusBadRequest
		result.code = problem.Code
	case response.StatusCode == http.StatusConflict && problem.Code != "":
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

func (h *handler) writeAssetModelResult(writer http.ResponseWriter, request *http.Request, result registryBackendResult) {
	etag := assetModelETag(result.body)
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Cache-Control", "private, no-cache")
	writer.Header().Set("Vary", "Cookie, Authorization")
	if ifNoneMatchMatches(request.Header.Values("If-None-Match"), etag) {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(append(result.body, '\n'))
}

func assetModelETag(canonicalBody []byte) string {
	sum := sha256.New()
	_, _ = sum.Write([]byte("asset-model-v1:"))
	_, _ = sum.Write(canonicalBody)
	return `"` + hex.EncodeToString(sum.Sum(nil)) + `"`
}

func ifNoneMatchMatches(values []string, currentETag string) bool {
	for _, raw := range values {
		if ifNoneMatchHeaderMatches(raw, currentETag) {
			return true
		}
	}
	return false
}

func ifNoneMatchHeaderMatches(raw, currentETag string) bool {
	i := 0
	for i < len(raw) {
		for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == ',') {
			i++
		}
		if i >= len(raw) {
			return false
		}
		if raw[i] == '*' {
			j := i + 1
			for j < len(raw) && (raw[j] == ' ' || raw[j] == '\t') {
				j++
			}
			if j == len(raw) || raw[j] == ',' {
				return true
			}
			i = skipToNextComma(raw, j)
			continue
		}
		if strings.HasPrefix(raw[i:], "W/") {
			i += 2
		}
		if i >= len(raw) || raw[i] != '"' {
			i = skipToNextComma(raw, i)
			continue
		}
		end := strings.IndexByte(raw[i+1:], '"')
		if end < 0 {
			return false
		}
		candidate := raw[i : i+1+end+1]
		i = i + 1 + end + 1
		for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
			i++
		}
		if i < len(raw) && raw[i] != ',' {
			i = skipToNextComma(raw, i)
			continue
		}
		if candidate == currentETag {
			return true
		}
	}
	return false
}

func skipToNextComma(raw string, start int) int {
	inQuotes := false
	for i := start; i < len(raw); i++ {
		switch raw[i] {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if !inQuotes {
				return i + 1
			}
		}
	}
	return len(raw)
}

func (h *handler) writeRegistryBackendResult(writer http.ResponseWriter, request *http.Request, result registryBackendResult) {
	if result.status == http.StatusOK || result.status == http.StatusCreated {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(result.status)
		_, _ = writer.Write(append(result.body, '\n'))
		return
	}
	switch result.code {
	case "AUTHORIZATION_DENIED", "RESOURCE_NOT_FOUND":
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
	case "CURSOR_INVALID":
		writeProblem(writer, request, http.StatusBadRequest, result.code, "Cursor invalid", "The Registry page cursor or limit is invalid.", false, nil)
	case "REGISTRY_MUTATION_INVALID":
		writeProblem(writer, request, http.StatusBadRequest, result.code, "Registry mutation invalid", "The Registry mutation does not satisfy the canonical contract.", false, nil)
	case "MAPPING_INVALID", "MAPPING_QUARANTINED":
		writeProblem(writer, request, http.StatusConflict, result.code, "Registry mapping unavailable", "The Registry resource mapping is not available for public reads.", false, nil)
	case "REGISTRY_REVISION_CONFLICT", "REGISTRY_IDEMPOTENCY_CONFLICT", "REGISTRY_BINDING_CONFLICT", "TEMPLATE_REVISION_IMMUTABLE", "REGISTRY_IMPORT_PLAN_INVALID":
		writeProblem(writer, request, http.StatusConflict, result.code, "Registry mutation conflict", "The Registry mutation conflicts with the current authoritative state.", false, nil)
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

func matchPublicRegistryRoute(method, path string) (publicRegistryRoute, string, bool) {
	if path == "/api/v1/sites" {
		if method == http.MethodGet {
			return publicRegistryRoute{template: "/api/v1/sites", internalPath: "/internal/v1/registry/sites", resource: "sites", action: registryauth.ActionSiteList, list: true}, "", true
		}
		if method == http.MethodPost {
			return publicRegistryRoute{template: "/api/v1/sites", internalPath: "/internal/v1/registry/sites", resource: "site-write", action: registryauth.ActionSiteWrite, write: true}, "", true
		}
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) < 3 || segments[0] != "api" || segments[1] != "v1" {
		return publicRegistryRoute{}, "", false
	}
	segments = segments[2:]
	switch {
	case len(segments) == 2 && segments[0] == "sites":
		siteID := segments[1]
		if method == http.MethodGet {
			return publicRegistryRoute{template: "/api/v1/sites/{siteId}", internalPath: "/internal/v1/registry/sites/" + siteID, resource: "sites", action: registryauth.ActionSiteRead, scopeID: siteID}, siteID, true
		}
		if method == http.MethodPatch {
			return publicRegistryRoute{template: "/api/v1/sites/{siteId}", internalPath: "/internal/v1/registry/sites/" + siteID, resource: "site-write", action: registryauth.ActionSiteWrite, write: true}, siteID, true
		}
	case len(segments) == 1 && segments[0] == "assets" && method == http.MethodGet:
		return publicRegistryRoute{template: platformapi.ListAssetsPath, internalPath: "/internal/v1/registry/assets", resource: "assets", action: registryauth.ActionAssetList, list: true}, "", true
	case len(segments) == 2 && segments[0] == "assets" && method == http.MethodGet:
		id := segments[1]
		return publicRegistryRoute{template: "/api/v1/assets/{assetId}", internalPath: "/internal/v1/registry/assets/" + id, resource: "assets", action: registryauth.ActionAssetRead}, id, true
	case len(segments) == 1 && segments[0] == "devices" && method == http.MethodGet:
		return publicRegistryRoute{template: platformapi.ListDevicesPath, internalPath: "/internal/v1/registry/devices", resource: "devices", action: registryauth.ActionDeviceList, list: true}, "", true
	case len(segments) == 2 && segments[0] == "devices" && method == http.MethodGet:
		id := segments[1]
		return publicRegistryRoute{template: "/api/v1/devices/{deviceId}", internalPath: "/internal/v1/registry/devices/" + id, resource: "devices", action: registryauth.ActionDeviceRead}, id, true
	case len(segments) == 1 && segments[0] == "templates" && method == http.MethodGet:
		return publicRegistryRoute{
			template: platformapi.ListTemplatesPath, internalPath: "/internal/v1/registry/templates", resource: "templates",
			action:     registryauth.ActionDeviceList,
			altActions: []registryauth.Action{registryauth.ActionTemplateManage, registryauth.ActionAssetModelRead},
			list:       true,
		}, "", true
	case len(segments) == 3 && segments[0] == "templates" && segments[1] == "revisions" && method == http.MethodGet:
		id := segments[2]
		return publicRegistryRoute{
			template: platformapi.GetTemplateRevisionPathTemplate, internalPath: "/internal/v1/registry/templates/revisions/" + id, resource: "template-revision",
			action:     registryauth.ActionDeviceList,
			altActions: []registryauth.Action{registryauth.ActionTemplateManage, registryauth.ActionAssetModelRead},
		}, id, true
	case len(segments) == 3 && segments[0] == "devices" && segments[2] == "points" && method == http.MethodGet:
		id := segments[1]
		return publicRegistryRoute{template: "/api/v1/devices/{deviceId}/points", internalPath: "/internal/v1/registry/devices/" + id + "/points", resource: "device-points", action: registryauth.ActionDeviceRead, list: true}, id, true
	case len(segments) == 3 && segments[0] == "sites":
		siteID, collection := segments[1], segments[2]
		switch collection {
		case "assets":
			if method == http.MethodGet {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/assets", internalPath: "/internal/v1/registry/sites/" + siteID + "/assets", resource: "assets", action: registryauth.ActionAssetList, scopeID: siteID, list: true}, siteID, true
			}
			if method == http.MethodPost {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/assets", internalPath: "/internal/v1/registry/sites/" + siteID + "/assets", resource: "asset-write", action: registryauth.ActionAssetWrite, scopeID: siteID, write: true}, siteID, true
			}
		case "devices":
			if method == http.MethodGet {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/devices", internalPath: "/internal/v1/registry/sites/" + siteID + "/devices", resource: "devices", action: registryauth.ActionDeviceList, scopeID: siteID, list: true}, siteID, true
			}
			if method == http.MethodPost {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/devices", internalPath: "/internal/v1/registry/sites/" + siteID + "/devices", resource: "device-write", action: registryauth.ActionDeviceWrite, scopeID: siteID, write: true}, siteID, true
			}
		case "spaces":
			if method == http.MethodPost {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/spaces", internalPath: "/internal/v1/registry/sites/" + siteID + "/spaces", resource: "space-write", action: registryauth.ActionSpaceWrite, scopeID: siteID, write: true}, siteID, true
			}
		case "sensors":
			if method == http.MethodPost {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/sensors", internalPath: "/internal/v1/registry/sites/" + siteID + "/sensors", resource: "sensor-write", action: registryauth.ActionSensorWrite, scopeID: siteID, write: true}, siteID, true
			}
		case "points":
			if method == http.MethodPost {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/points", internalPath: "/internal/v1/registry/sites/" + siteID + "/points", resource: "point-write", action: registryauth.ActionPointWrite, scopeID: siteID, write: true}, siteID, true
			}
		case "device-bindings":
			if method == http.MethodGet {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/device-bindings", internalPath: "/internal/v1/registry/sites/" + siteID + "/device-bindings", resource: "device-bindings", action: registryauth.ActionDeviceBindingList, scopeID: siteID, list: true}, siteID, true
			}
		case "asset-model":
			if method == http.MethodGet {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/asset-model", internalPath: "/internal/v1/registry/sites/" + siteID + "/asset-model", resource: "asset-model", action: registryauth.ActionAssetModelRead, scopeID: siteID}, siteID, true
			}
		case "tariff":
			if method == http.MethodGet {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/tariff", internalPath: "/internal/v1/registry/sites/" + siteID + "/tariff", resource: "tariff", action: registryauth.ActionTariffRead, scopeID: siteID}, siteID, true
			}
		case "template-assignments":
			if method == http.MethodGet {
				return publicRegistryRoute{
					template: platformapi.ListSiteTemplateAssignmentsPathTemplate, internalPath: "/internal/v1/registry/sites/" + siteID + "/template-assignments", resource: "site-template-assignments",
					action:     registryauth.ActionAssetModelRead,
					altActions: []registryauth.Action{registryauth.ActionTemplateManage, registryauth.ActionDeviceList},
					scopeID:    siteID, list: true,
				}, siteID, true
			}
		case "retire":
			if method == http.MethodPost {
				return publicRegistryRoute{template: "/api/v1/sites/{siteId}/retire", internalPath: "/internal/v1/registry/sites/" + siteID + "/retire", resource: "retire", action: registryauth.ActionRegistryRetire, scopeID: siteID, write: true}, siteID, true
			}
		}
	case len(segments) == 4 && segments[0] == "sites":
		siteID := segments[1]
		if segments[2] == "spaces" && segments[3] == "tree" && method == http.MethodGet {
			return publicRegistryRoute{template: "/api/v1/sites/{siteId}/spaces/tree", internalPath: "/internal/v1/registry/sites/" + siteID + "/spaces/tree", resource: "space-children", action: registryauth.ActionAssetModelRead, scopeID: siteID, list: true}, siteID, true
		}
		if segments[2] == "bindings" && segments[3] == "rebind" && method == http.MethodPost {
			return publicRegistryRoute{template: "/api/v1/sites/{siteId}/bindings/rebind", internalPath: "/internal/v1/registry/sites/" + siteID + "/bindings/rebind", resource: "binding-write", action: registryauth.ActionBindingWrite, scopeID: siteID, write: true}, siteID, true
		}
		if segments[2] == "imports" && segments[3] == "dry-run" && method == http.MethodPost {
			return publicRegistryRoute{template: "/api/v1/sites/{siteId}/imports/dry-run", internalPath: "/internal/v1/registry/sites/" + siteID + "/imports/dry-run", resource: "import-plan", action: registryauth.ActionRegistryImport, scopeID: siteID, write: true}, siteID, true
		}
		if segments[2] == "imports" && segments[3] == "commit" && method == http.MethodPost {
			return publicRegistryRoute{template: "/api/v1/sites/{siteId}/imports/commit", internalPath: "/internal/v1/registry/sites/" + siteID + "/imports/commit", resource: "import-commit", action: registryauth.ActionRegistryImport, scopeID: siteID, write: true}, siteID, true
		}
		if method == http.MethodPatch && (segments[2] == "assets" || segments[2] == "devices" || segments[2] == "spaces" || segments[2] == "sensors" || segments[2] == "points") {
			id := segments[3]
			if !isLowerUUIDv7(id) {
				return publicRegistryRoute{}, "", false
			}
			action := map[string]registryauth.Action{"assets": registryauth.ActionAssetWrite, "devices": registryauth.ActionDeviceWrite, "spaces": registryauth.ActionSpaceWrite, "sensors": registryauth.ActionSensorWrite, "points": registryauth.ActionPointWrite}[segments[2]]
			resource := strings.TrimSuffix(segments[2], "s") + "-write"
			return publicRegistryRoute{template: "/api/v1/sites/{siteId}/" + segments[2] + "/{" + strings.TrimSuffix(segments[2], "s") + "Id}", internalPath: "/internal/v1/registry/sites/" + siteID + "/" + segments[2] + "/" + id, resource: resource, action: action, scopeID: siteID, write: true}, siteID, true
		}
	case len(segments) == 5 && segments[0] == "sites":
		siteID := segments[1]
		if segments[2] == "devices" && segments[4] == "replace-hardware" && method == http.MethodPost {
			deviceID := segments[3]
			if !isLowerUUIDv7(deviceID) {
				return publicRegistryRoute{}, "", false
			}
			return publicRegistryRoute{template: "/api/v1/sites/{siteId}/devices/{deviceId}/replace-hardware", internalPath: "/internal/v1/registry/sites/" + siteID + "/devices/" + deviceID + "/replace-hardware", resource: "device-replace-hardware", action: registryauth.ActionDeviceWrite, scopeID: siteID, write: true}, siteID, true
		}
		if segments[2] == "devices" && segments[4] == "transfer" && method == http.MethodPost {
			deviceID := segments[3]
			if !isLowerUUIDv7(deviceID) {
				return publicRegistryRoute{}, "", false
			}
			return publicRegistryRoute{template: "/api/v1/sites/{siteId}/devices/{deviceId}/transfer", internalPath: "/internal/v1/registry/sites/" + siteID + "/devices/" + deviceID + "/transfer", resource: "device-transfer", action: registryauth.ActionDeviceWrite, scopeID: siteID, write: true}, siteID, true
		}
	case len(segments) == 2 && segments[0] == "templates" && method == http.MethodPost:
		if segments[1] == "revisions" {
			return publicRegistryRoute{template: "/api/v1/templates/revisions", internalPath: "/internal/v1/registry/templates/revisions", resource: "template-release", action: registryauth.ActionTemplateManage, write: true}, "", true
		}
		if segments[1] == "assignments" {
			return publicRegistryRoute{template: "/api/v1/templates/assignments", internalPath: "/internal/v1/registry/templates/assignments", resource: "template-assign", action: registryauth.ActionTemplateManage, write: true}, "", true
		}
	}
	return publicRegistryRoute{}, "", false
}

func dispatchRegistryRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicRegistryRoute, id string) {
	if id != "" && !isLowerUUIDv7(id) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
		return
	}
	if route.write {
		h.serveRegistryMutation(writer, request, route)
		return
	}
	params := platformapi.ListRegistryParams{}
	var extraQuery url.Values
	if route.list {
		var ok bool
		params, extraQuery, ok = parseRegistryRouteListQuery(writer, request, route)
		if !ok {
			return
		}
	} else if request.URL.RawQuery != "" {
		writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "Registry query invalid", "Detail Registry routes do not accept query parameters.", false, nil)
		return
	}
	switch route.resource {
	case "sites":
		if route.list {
			h.ListSites(writer, request, params)
		} else {
			h.GetSite(writer, request, id)
		}
	case "assets":
		if route.list {
			h.serveRegistryWithQuery(writer, request, route, params, extraQuery)
		} else {
			h.GetAsset(writer, request, id)
		}
	case "devices":
		if route.list {
			h.serveRegistryWithQuery(writer, request, route, params, extraQuery)
		} else {
			h.GetDevice(writer, request, id)
		}
	case "device-bindings":
		h.ListSiteDeviceBindings(writer, request, id, params)
	case "asset-model":
		h.GetSiteAssetModel(writer, request, id)
	case "tariff":
		h.GetSiteTariff(writer, request, id)
	case "space-children", "device-points", "templates", "template-revision", "site-template-assignments":
		h.serveRegistryWithQuery(writer, request, route, params, extraQuery)
	default:
		writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested public API route does not exist.", false, nil)
	}
}

func canonicalRegistryDocument(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || ensureRegistryJSONEOF(decoder) != nil || value == nil {
		return nil, errors.New("registry response is not a canonical JSON object")
	}
	return json.Marshal(value)
}

func canonicalRegistrySuccessForRoute(route publicRegistryRoute, raw []byte) ([]byte, error) {
	switch route.resource {
	case "templates":
		return decodeCanonical[platformapi.TemplateCollection](raw, validateTemplateCollection)
	case "template-revision":
		return decodeCanonical[platformapi.TemplateRevision](raw, validateTemplateRevision)
	case "site-template-assignments":
		return decodeCanonical[platformapi.TemplateAssignmentCollection](raw, func(value platformapi.TemplateAssignmentCollection) error {
			return validateTemplateAssignmentCollection(value, route.scopeID)
		})
	default:
		return canonicalRegistrySuccess(route.action, route.scopeID, raw)
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
	case registryauth.ActionTariffRead:
		return decodeCanonical[platformapi.SiteTariff](raw, func(value platformapi.SiteTariff) error {
			if value.SiteID != scopeID {
				return errors.New("registry tariff is outside the requested Site")
			}
			return nil
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
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid Registry response: %w", err)
	}
	if err := ensureRegistryJSONEOF(decoder); err != nil {
		return nil, errors.New("invalid Registry response: trailing data")
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
		return platformapi.Site{}, errors.New("registry site authorization failed")
	}
	publicPath := strings.Replace(platformapi.GetSitePathTemplate, "{siteId}", url.PathEscape(siteID), 1)
	route, _, matches := matchPublicRegistryRoute(http.MethodGet, publicPath)
	if !matches {
		return platformapi.Site{}, errors.New("registry site route is unavailable")
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
			return platformapi.Site{}, errors.New("registry site route ownership is unavailable")
		}
		decision = resolved
	}
	result := h.executeCoreRegistry(request.Context(), route, "", authorization.coreGrant, decision)
	if result.status != http.StatusOK {
		return platformapi.Site{}, errors.New("registry site lookup failed")
	}
	var site platformapi.Site
	decoder := json.NewDecoder(bytes.NewReader(result.body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&site) != nil || ensureRegistryJSONEOF(decoder) != nil || validateSite(site) != nil || site.ID != siteID || !isLowerUUIDv7(site.TenantID) {
		return platformapi.Site{}, errors.New("registry returned an invalid authoritative Site")
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
			return errors.New("space escaped Site asset model Tenant scope")
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
			return errors.New("sensor escaped Site asset model Tenant scope")
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
			return errors.New("telemetry point escaped Site asset model Tenant scope")
		}
		if _, duplicate := identities[item.ID]; duplicate {
			return errors.New("duplicate Site asset model identity")
		}
		if identities[item.ReportingDeviceID] != "DEVICE" {
			return errors.New("telemetry point references a Device outside the Site asset model")
		}
		if item.SensorID != nil && identities[*item.SensorID] != "SENSOR" {
			return errors.New("telemetry point references a Sensor outside the Site asset model")
		}
		identities[item.ID] = "POINT"
	}
	for _, area := range value.Spaces {
		if area.ParentSpaceID != nil && identities[*area.ParentSpaceID] != "SPACE" {
			return errors.New("space parent escaped the Site asset model")
		}
	}
	for _, relationship := range value.Relationships {
		if err := validateAssetRelationship(relationship, expectedSiteID); err != nil {
			return err
		}
		if relationship.TenantID != value.TenantID {
			return errors.New("asset relationship escaped Site asset model Tenant scope")
		}
		if identities[relationship.FromID] != relationship.FromType || identities[relationship.ToID] != relationship.ToType {
			return errors.New("asset relationship references an unknown or mismatched identity")
		}
	}
	if value.Counts.Spaces != len(value.Spaces) || value.Counts.Assets != len(value.Assets) || value.Counts.DeviceEndpoints != len(value.Devices) || value.Counts.PhysicalSensors != len(value.Sensors) || value.Counts.Points != len(value.TelemetryPoints) {
		return errors.New("site asset model counts do not match the payload")
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

// siteVisibility is what the Registry says about a Site for a session: hidden when it is absent
// or not the session's to see, unavailable (with the reason) when the Registry could not answer.
type siteVisibility struct {
	hidden      bool
	unavailable string
}

func (h *handler) siteVisibility(request *http.Request, session bffSession, siteID string) siteVisibility {
	if h.registry == nil || h.registry.coreHTTPClient == nil || strings.TrimSpace(h.registry.coreBaseURL) == "" {
		return siteVisibility{unavailable: "Authoritative Site visibility is unavailable."}
	}
	authorization, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if failure != nil {
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			return siteVisibility{hidden: true}
		}
		return siteVisibility{unavailable: "Authoritative Site authorization is temporarily unavailable."}
	}
	if _, allowed := authorization.allowedSiteIDs[siteID]; !allowed {
		return siteVisibility{hidden: true}
	}
	publicPath := strings.Replace(platformapi.GetSitePathTemplate, "{siteId}", url.PathEscape(siteID), 1)
	registryRoute, _, matches := matchPublicRegistryRoute(http.MethodGet, publicPath)
	if !matches {
		return siteVisibility{unavailable: "Authoritative Site route is unavailable."}
	}
	outer := routeDecisionFromContext(request.Context())
	decision := ownershipregistry.Decision{
		RouteKey: http.MethodGet + " " + registryRoute.template, PathTemplate: registryRoute.template,
		SelectedOwner:    ownershipregistry.OwnerCore,
		RegistryRevision: outer.RegistryRevision, RouteRevision: 1, CompatibilityMode: "native",
	}
	if h.routeManager != nil {
		resolved, err := h.routeManager.Current().Resolve(http.MethodGet, publicPath, session.TenantID)
		if err != nil || resolved.SelectedOwner != ownershipregistry.OwnerCore {
			return siteVisibility{unavailable: "Authoritative Site route ownership is unavailable."}
		}
		decision = resolved
	}
	result := h.executeCoreRegistry(request.Context(), registryRoute, "", authorization.coreGrant, decision)
	if result.status == http.StatusNotFound || result.status == http.StatusForbidden {
		return siteVisibility{hidden: true}
	}
	if result.status != http.StatusOK {
		return siteVisibility{unavailable: "Authoritative Site lookup is temporarily unavailable."}
	}
	var site platformapi.Site
	if decodeStrictAlarmJSON(result.body, &site) != nil || validateSite(site) != nil || site.ID != siteID || site.TenantID != session.TenantID {
		return siteVisibility{unavailable: "Authoritative Site lookup returned an invalid projection."}
	}
	return siteVisibility{}
}

func validateSite(value platformapi.Site) error {
	if !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || !validRegistryString(value.Code, 128) || !validRegistryString(value.DisplayName, 256) || !validRegistryTimezone(value.Timezone) || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED") || value.Revision < 1 || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt) ||
		(value.FloorAreaM2 != nil && !(*value.FloorAreaM2 > 0)) ||
		(value.ClimateZone != nil && !oneOf(*value.ClimateZone, "SEVERE_COLD", "COLD", "HOT_SUMMER_COLD_WINTER", "HOT_SUMMER_WARM_WINTER", "MILD")) {
		return errors.New("invalid Site response")
	}
	if value.BuildingPlan != nil {
		if err := validateSiteBuildingPlan(*value.BuildingPlan); err != nil {
			return err
		}
	}
	return nil
}

func validateSiteBuildingPlan(plan platformapi.SiteBuildingPlan) error {
	finitePos := func(v, maxVal float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= maxVal }
	finiteNonNeg := func(v, maxVal float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= maxVal }
	if !validRegistryString(plan.Name, 128) || !finitePos(plan.Width, 1000) || !finitePos(plan.Depth, 1000) || len(plan.Floors) < 1 || len(plan.Floors) > 100 {
		return errors.New("invalid Site buildingPlan dimensions")
	}
	seenLevels := make(map[int]struct{}, len(plan.Floors))
	for _, floor := range plan.Floors {
		if floor.Level < 1 || floor.Level > 200 || !validRegistryString(floor.Label, 128) || !finitePos(floor.Height, 30) || len(floor.Rooms) < 1 || len(floor.Rooms) > 200 {
			return errors.New("invalid Site buildingPlan floor")
		}
		if _, dup := seenLevels[floor.Level]; dup {
			return errors.New("duplicate Site buildingPlan floor level")
		}
		seenLevels[floor.Level] = struct{}{}
		seenRooms := make(map[string]struct{}, len(floor.Rooms))
		for _, room := range floor.Rooms {
			if !validRegistryString(room.ID, 64) || !validRegistryString(room.Name, 128) ||
				!oneOf(room.Kind, "hall", "private", "smallPrivate", "kitchen", "plantRoom", "service", "corridor", "core", "shaft") {
				return errors.New("invalid Site buildingPlan room")
			}
			if _, dup := seenRooms[room.ID]; dup {
				return errors.New("duplicate Site buildingPlan room id")
			}
			seenRooms[room.ID] = struct{}{}
			if !finiteNonNeg(room.X[0], plan.Width) || !finiteNonNeg(room.X[1], plan.Width) || room.X[0] >= room.X[1] ||
				!finiteNonNeg(room.Z[0], plan.Depth) || !finiteNonNeg(room.Z[1], plan.Depth) || room.Z[0] >= room.Z[1] {
				return errors.New("invalid Site buildingPlan room bounds")
			}
		}
	}
	return nil
}

var (
	registryTagKeyPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,63}\z`)
	registryTemplateKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}\z`)
)

func parseRegistryRouteListQuery(writer http.ResponseWriter, request *http.Request, route publicRegistryRoute) (platformapi.ListRegistryParams, url.Values, bool) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "Registry query invalid", "The Registry query string is malformed.", false, nil)
		return platformapi.ListRegistryParams{}, nil, false
	}
	extra := url.Values{}
	baseQuery := url.Values{}
	for name, values := range query {
		if name == "limit" || name == "cursor" {
			baseQuery[name] = values
			continue
		}
		allowed := isAllowedGatewayListParam(route, name)
		if !allowed || (name != "tag" && len(values) != 1) {
			writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "Registry query invalid", "The Registry query parameters are invalid.", false, []platformapi.FieldError{{Field: name, Message: "unsupported or duplicate query parameter"}})
			return platformapi.ListRegistryParams{}, nil, false
		}
		for _, v := range values {
			extra.Add(name, v)
		}
	}
	if !validateGatewayExtraQuery(writer, request, route, extra) {
		return platformapi.ListRegistryParams{}, nil, false
	}
	original := request.URL.RawQuery
	request.URL.RawQuery = baseQuery.Encode()
	params, ok := parseRegistryListParams(writer, request)
	request.URL.RawQuery = original
	if !ok {
		return platformapi.ListRegistryParams{}, nil, false
	}
	return params, extra, true
}

func isAllowedGatewayListParam(route publicRegistryRoute, name string) bool {
	switch route.resource {
	case "space-children":
		return name == "parentSpaceId"
	case "assets":
		switch name {
		case "status", "assetType", "spaceId", "templateKey", "templateRevisionId", "tag":
			return true
		case "siteId":
			return route.scopeID == ""
		}
	case "devices":
		switch name {
		case "status", "deviceType", "spaceId", "templateKey", "templateRevisionId", "tag":
			return true
		case "siteId":
			return route.scopeID == ""
		}
	case "templates":
		return name == "templateKind"
	case "site-template-assignments":
		return name == "targetType" || name == "targetId"
	}
	return false
}

func validateGatewayExtraQuery(writer http.ResponseWriter, request *http.Request, route publicRegistryRoute, extra url.Values) bool {
	invalidField := func(field, msg string) bool {
		writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "Registry query invalid", "The Registry query parameters are invalid.", false, []platformapi.FieldError{{Field: field, Message: msg}})
		return false
	}
	switch route.resource {
	case "space-children":
		if parentSpaceID := extra.Get("parentSpaceId"); parentSpaceID != "" && !isLowerUUIDv7(parentSpaceID) {
			return invalidField("parentSpaceId", "parentSpaceId must be a UUIDv7")
		}
	case "assets", "devices":
		if siteID := extra.Get("siteId"); siteID != "" && !isLowerUUIDv7(siteID) {
			return invalidField("siteId", "siteId must be a UUIDv7")
		}
		if status := extra.Get("status"); status != "" && !oneOf(status, "ACTIVE", "INACTIVE", "RETIRED") {
			return invalidField("status", "status must be ACTIVE, INACTIVE, or RETIRED")
		}
		typeField := "assetType"
		if route.resource == "devices" {
			typeField = "deviceType"
		}
		if tval := extra.Get(typeField); extra.Has(typeField) && (strings.TrimSpace(tval) == "" || strings.TrimSpace(tval) != tval || len(tval) > 128 || hasControlRune(tval)) {
			return invalidField(typeField, "type filter is invalid")
		}
		if spaceID := extra.Get("spaceId"); spaceID != "" && !isLowerUUIDv7(spaceID) {
			return invalidField("spaceId", "spaceId must be a UUIDv7")
		}
		if templateRevID := extra.Get("templateRevisionId"); templateRevID != "" && !isLowerUUIDv7(templateRevID) {
			return invalidField("templateRevisionId", "templateRevisionId must be a UUIDv7")
		}
		if templateKey := extra.Get("templateKey"); templateKey != "" && !registryTemplateKeyPattern.MatchString(templateKey) {
			return invalidField("templateKey", "templateKey is invalid")
		}
		rawTags := extra["tag"]
		if len(rawTags) > 64 {
			return invalidField("tag", "at most 64 tag filters are allowed")
		}
		seenTagValues := map[string]string{}
		for _, rawTag := range rawTags {
			if key, val, hasValue := strings.Cut(rawTag, ":"); hasValue {
				if !registryTagKeyPattern.MatchString(key) || !validTagValueString(val) {
					return invalidField("tag", "invalid tag key:value filter")
				}
				if prev, exists := seenTagValues[key]; exists && prev != val {
					return invalidField("tag", "conflicting tag filters for key")
				}
				seenTagValues[key] = val
			} else if !registryTagKeyPattern.MatchString(rawTag) {
				return invalidField("tag", "invalid tag key filter")
			}
		}
	case "templates":
		if kind := extra.Get("templateKind"); kind != "" && !oneOf(kind, "ASSET", "DEVICE", "POINT") {
			return invalidField("templateKind", "templateKind must be ASSET, DEVICE, or POINT")
		}
	case "site-template-assignments":
		if tt := extra.Get("targetType"); tt != "" && !oneOf(tt, "ASSET", "DEVICE", "POINT") {
			return invalidField("targetType", "targetType must be ASSET, DEVICE, or POINT")
		}
		if tid := extra.Get("targetId"); tid != "" && !isLowerUUIDv7(tid) {
			return invalidField("targetId", "targetId must be a UUIDv7")
		}
	}
	return true
}

func validateRegistryTags(tags map[string]string) error {
	if len(tags) > 64 {
		return errors.New("registry tags exceed maximum cardinality")
	}
	for key, val := range tags {
		if !registryTagKeyPattern.MatchString(key) || !validTagValueString(val) {
			return errors.New("invalid registry tag entry")
		}
	}
	return nil
}

func validTagValueString(value string) bool {
	if !utf8.ValidString(value) || len(value) > 256 {
		return false
	}
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && trimmed == value && !hasControlRune(value)
}

func hasControlRune(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func validateTemplateCollection(value platformapi.TemplateCollection) error {
	for _, item := range value.Items {
		if !isLowerUUIDv7(item.ID) || !isLowerUUIDv7(item.TenantID) || !registryTemplateKeyPattern.MatchString(item.TemplateKey) || !oneOf(item.TemplateKind, "ASSET", "DEVICE", "POINT") || !validRegistryInstant(item.CreatedAt) {
			return errors.New("invalid Template response")
		}
		if item.LatestRevisionID != "" && !isLowerUUIDv7(item.LatestRevisionID) {
			return errors.New("invalid Template revision identifier")
		}
		if item.ReleasedAt != "" && !validRegistryInstant(item.ReleasedAt) {
			return errors.New("invalid Template release timestamp")
		}
	}
	return validateNextCursor(value.NextCursor)
}

func validateTemplateRevision(value platformapi.TemplateRevision) error {
	if !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || !isLowerUUIDv7(value.TemplateID) || !registryTemplateKeyPattern.MatchString(value.TemplateKey) || !oneOf(value.TemplateKind, "ASSET", "DEVICE", "POINT") || value.RevisionNumber < 1 || value.Status != "RELEASED" || value.Payload == nil || value.ReleaseReferences == nil || !validRegistryInstant(value.ReleasedAt) {
		return errors.New("invalid TemplateRevision response")
	}
	return nil
}

func validateTemplateAssignmentCollection(value platformapi.TemplateAssignmentCollection, expectedSiteID string) error {
	if !isLowerUUIDv7(expectedSiteID) {
		return errors.New("invalid TemplateAssignment scope")
	}
	for _, item := range value.Items {
		if !isLowerUUIDv7(item.ID) || !isLowerUUIDv7(item.TenantID) || item.SiteID != expectedSiteID || !oneOf(item.TargetType, "ASSET", "DEVICE", "POINT") || !isLowerUUIDv7(item.TargetID) || !isLowerUUIDv7(item.TemplateRevisionID) || !validRegistryInstant(item.ValidFrom) || (item.ValidTo != nil && !validRegistryInstant(*item.ValidTo)) || item.Revision < 1 {
			return errors.New("invalid TemplateAssignment response")
		}
	}
	return validateNextCursor(value.NextCursor)
}

func validateAsset(value platformapi.Asset) error {
	if !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || !isLowerUUIDv7(value.SiteID) || !validRegistryString(value.Code, 128) || !validRegistryString(value.DisplayName, 256) || !validRegistryString(value.AssetType, 128) || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED") || value.Revision < 1 || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt) || validateRegistryTags(value.Tags) != nil {
		return errors.New("invalid Asset response")
	}
	return nil
}

func validateDevice(value platformapi.Device) error {
	invalidConnectivityMode := value.ConnectivityMode != "" && !oneOf(value.ConnectivityMode, "GATEWAY_PROXIED", "DIRECT")
	if invalidConnectivityMode || !isLowerUUIDv7(value.ID) || !isLowerUUIDv7(value.TenantID) || !isLowerUUIDv7(value.SiteID) || !validRegistryString(value.Code, 128) || !validRegistryString(value.DisplayName, 256) || !validRegistryString(value.DeviceType, 128) || !oneOf(value.Status, "ACTIVE", "INACTIVE", "RETIRED") || value.Revision < 1 || !validRegistryInstant(value.CreatedAt) || !validRegistryInstant(value.UpdatedAt) || validateRegistryTags(value.Tags) != nil {
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
	return isLowerUUID(value) && value[14] == '7' && strings.ContainsRune("89ab", rune(value[19]))
}

func isLowerUUID(value string) bool {
	if value != strings.ToLower(value) || len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded := make([]byte, 16)
	if _, err := hex.Decode(decoded, []byte(compact)); err != nil {
		return false
	}
	return decoded[8]>>6 == 2
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
