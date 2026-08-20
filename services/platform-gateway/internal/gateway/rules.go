package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
	"github.com/quanlaihe/hvac-web/services/rule-runtime-service/pkg/rulemanagement"
)

const maximumRuleRequestBody = 1 << 20

type publicRuleRoute struct {
	template  string
	operation string
	bindingID string
	method    string
}

func matchPublicRuleRoute(method, path string) (publicRuleRoute, bool) {
	static := map[string]publicRuleRoute{
		"GET /api/v1/rules/catalog":      {template: "/api/v1/rules/catalog", operation: "catalog", method: http.MethodGet},
		"GET /api/v1/rules/revisions":    {template: "/api/v1/rules/revisions", operation: "revisions", method: http.MethodGet},
		"POST /api/v1/rules/validate":    {template: "/api/v1/rules/validate", operation: "validate", method: http.MethodPost},
		"POST /api/v1/rules/simulate":    {template: "/api/v1/rules/simulate", operation: "simulate", method: http.MethodPost},
		"POST /api/v1/rules/releases":    {template: "/api/v1/rules/releases", operation: "release", method: http.MethodPost},
		"GET /api/v1/rules/bindings":     {template: "/api/v1/rules/bindings", operation: "bindings", method: http.MethodGet},
		"POST /api/v1/rules/assignments": {template: "/api/v1/rules/assignments", operation: "assign", method: http.MethodPost},
		"GET /api/v1/rules/executions":   {template: "/api/v1/rules/executions", operation: "evidence", method: http.MethodGet},
	}
	if route, ok := static[method+" "+path]; ok {
		return route, true
	}
	const prefix = "/api/v1/rules/bindings/"
	const suffix = "/retire"
	if method == http.MethodPost && strings.HasPrefix(path, prefix) && strings.HasSuffix(path, suffix) {
		raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
		bindingID, err := url.PathUnescape(raw)
		if err == nil && isLowerUUIDv7(bindingID) && !strings.Contains(raw, "/") {
			return publicRuleRoute{template: "/api/v1/rules/bindings/{bindingId}/retire", operation: "retire", bindingID: bindingID, method: http.MethodPost}, true
		}
	}
	return publicRuleRoute{}, false
}

func dispatchRuleRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicRuleRoute) {
	decision := routeDecisionFromContext(request.Context())
	if decision.SelectedOwner != ownershipregistry.OwnerRuleRuntime {
		writeProblem(writer, request, http.StatusServiceUnavailable, "RULE_RUNTIME_UNAVAILABLE", "Rule Runtime unavailable", "The Rule management route is not owned by the active Rule Runtime.", true, nil)
		return
	}
	if h.ruleManagement == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "RULE_RUNTIME_UNAVAILABLE", "Rule Runtime unavailable", "Rule management is not configured for this deployment.", true, nil)
		return
	}
	session, ok := routeSessionFromContext(request.Context())
	if !ok {
		var failure *identityFailure
		session, failure = h.identitySession(request)
		if failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return
		}
	}
	principal, failure := h.identity.fetchPrincipal(request.Context(), session)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !principal.Authorization.Has(identitycontext.CapabilityRuleManage) {
		writeProblem(writer, request, http.StatusForbidden, "RULE_MANAGEMENT_FORBIDDEN", "Rule management forbidden", "The authenticated Principal is not allowed to manage Rules.", false, nil)
		return
	}
	if route.method == http.MethodPost {
		if failure := h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token")); failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return
		}
	}

	switch route.operation {
	case "catalog":
		if len(request.URL.Query()) != 0 {
			writeRuleInvalid(writer, request, "Catalog does not accept query parameters.")
			return
		}
		writeJSON(writer, http.StatusOK, h.ruleManagement.Catalog())
	case "revisions":
		if !ruleQueryKeys(request.URL.Query(), "ruleId") {
			writeRuleInvalid(writer, request, "Rule revision query is invalid.")
			return
		}
		items, err := h.ruleManagement.ListRevisions(request.Context(), session.TenantID, request.URL.Query().Get("ruleId"))
		if err != nil {
			writeRuleFailure(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	case "validate":
		var draft rulemanagement.Draft
		if !decodeRuleRequest(writer, request, &draft) {
			return
		}
		writeJSON(writer, http.StatusOK, h.ruleManagement.Validate(session.TenantID, draft))
	case "simulate":
		var input rulemanagement.SimulationRequest
		if !decodeRuleRequest(writer, request, &input) {
			return
		}
		if input.Event.SiteID != "" && !h.checkRuleSiteVisibility(writer, request, session, input.Event.SiteID) {
			return
		}
		result, err := h.ruleManagement.Simulate(request.Context(), session.TenantID, input)
		if err != nil {
			writeRuleFailure(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case "release":
		var draft rulemanagement.Draft
		if !decodeRuleRequest(writer, request, &draft) {
			return
		}
		revision, err := h.ruleManagement.Release(request.Context(), session.TenantID, draft)
		if err != nil {
			writeRuleFailure(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusCreated, revision)
	case "bindings":
		if !ruleQueryKeys(request.URL.Query(), "siteId") {
			writeRuleInvalid(writer, request, "Rule binding query is invalid.")
			return
		}
		siteID := strings.TrimSpace(request.URL.Query().Get("siteId"))
		if siteID == "" {
			writeRuleInvalid(writer, request, "Rule binding queries require siteId.")
			return
		}
		if !h.checkRuleSiteVisibility(writer, request, session, siteID) {
			return
		}
		items, err := h.ruleManagement.ListBindings(request.Context(), session.TenantID, siteID)
		if err != nil {
			writeRuleFailure(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	case "assign":
		var input rulemanagement.AssignmentRequest
		if !decodeRuleRequest(writer, request, &input) {
			return
		}
		if !h.checkRuleSiteVisibility(writer, request, session, input.SiteID) {
			return
		}
		binding, err := h.ruleManagement.Assign(request.Context(), session.TenantID, input)
		if err != nil {
			writeRuleFailure(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusCreated, binding)
	case "retire":
		var input rulemanagement.RetirementRequest
		if !decodeRuleRequest(writer, request, &input) {
			return
		}
		if !h.checkRuleSiteVisibility(writer, request, session, input.SiteID) {
			return
		}
		binding, err := h.ruleManagement.Retire(request.Context(), session.TenantID, route.bindingID, input)
		if err != nil {
			writeRuleFailure(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, binding)
	case "evidence":
		if !ruleQueryKeys(request.URL.Query(), "siteId", "limit") {
			writeRuleInvalid(writer, request, "Rule execution evidence query is invalid.")
			return
		}
		siteID := strings.TrimSpace(request.URL.Query().Get("siteId"))
		if siteID == "" {
			writeRuleInvalid(writer, request, "Rule execution evidence queries require siteId.")
			return
		}
		if !h.checkRuleSiteVisibility(writer, request, session, siteID) {
			return
		}
		limit := 50
		if raw := request.URL.Query().Get("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				writeRuleInvalid(writer, request, "Rule evidence limit is invalid.")
				return
			}
			limit = value
		}
		items, err := h.ruleManagement.Evidence(request.Context(), session.TenantID, siteID, limit)
		if err != nil {
			writeRuleFailure(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	}
}

func (h *handler) checkRuleSiteVisibility(writer http.ResponseWriter, request *http.Request, session bffSession, siteID string) bool {
	if !isLowerUUIDv7(siteID) {
		writeRuleSiteNotFound(writer, request)
		return false
	}
	if h.registry == nil || h.registry.coreHTTPClient == nil || strings.TrimSpace(h.registry.coreBaseURL) == "" {
		writeProblem(writer, request, http.StatusServiceUnavailable, "RULE_RUNTIME_UNAVAILABLE", "Rule Runtime unavailable", "Authoritative Site visibility is unavailable.", true, nil)
		return false
	}
	authorization, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if failure != nil {
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			writeRuleSiteNotFound(writer, request)
		} else {
			writeProblem(writer, request, http.StatusServiceUnavailable, "RULE_RUNTIME_UNAVAILABLE", "Rule Runtime unavailable", "Authoritative Site authorization is temporarily unavailable.", true, nil)
		}
		return false
	}
	if _, allowed := authorization.allowedSiteIDs[siteID]; !allowed {
		writeRuleSiteNotFound(writer, request)
		return false
	}
	route := publicRegistryRoute{template: "/api/v1/sites/{siteId}", internalPath: "/internal/v1/registry/sites/" + siteID, resource: "sites", action: registryauth.ActionSiteRead, scopeID: siteID}
	result := h.executeCoreRegistry(request.Context(), route, "", authorization.coreGrant, routeDecisionFromContext(request.Context()))
	if result.status == http.StatusNotFound {
		writeRuleSiteNotFound(writer, request)
		return false
	}
	if result.status != http.StatusOK {
		writeProblem(writer, request, http.StatusServiceUnavailable, "RULE_RUNTIME_UNAVAILABLE", "Rule Runtime unavailable", "Authoritative Site lookup is temporarily unavailable.", true, nil)
		return false
	}
	var site platformapi.Site
	if json.Unmarshal(result.body, &site) != nil || validateSite(site) != nil || site.ID != siteID || site.TenantID != session.TenantID {
		writeProblem(writer, request, http.StatusServiceUnavailable, "RULE_RUNTIME_UNAVAILABLE", "Rule Runtime unavailable", "Authoritative Site lookup returned an invalid projection.", true, nil)
		return false
	}
	return true
}

func decodeRuleRequest(writer http.ResponseWriter, request *http.Request, output any) bool {
	if len(request.URL.Query()) != 0 {
		writeProblem(writer, request, http.StatusBadRequest, "RULE_REQUEST_INVALID", "Rule request invalid", "Rule mutations do not accept query parameters.", false, nil)
		return false
	}
	if strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "RULE_REQUEST_INVALID", "Rule request invalid", "Rule mutations require application/json.", false, nil)
		return false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumRuleRequestBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		writeRuleInvalid(writer, request, "Rule request body is invalid.")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeRuleInvalid(writer, request, "Rule request body contains trailing data.")
		return false
	}
	return true
}

func ruleQueryKeys(query url.Values, allowed ...string) bool {
	keys := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		keys[key] = struct{}{}
	}
	for key, values := range query {
		if _, ok := keys[key]; !ok || len(values) != 1 {
			return false
		}
	}
	return true
}

func writeRuleFailure(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, rulemanagement.ErrNotFound):
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Rule resource is not visible.", false, nil)
	case errors.Is(err, rulemanagement.ErrConflict):
		writeProblem(writer, request, http.StatusConflict, "RULE_REVISION_CONFLICT", "Rule revision conflict", "The Rule lifecycle changed before this operation could commit.", false, nil)
	case errors.Is(err, rulemanagement.ErrInvalidIdentity):
		writeRuleInvalid(writer, request, "A Rule resource identity is invalid.")
	case errors.Is(err, rulemanagement.ErrValidation):
		writeProblem(writer, request, http.StatusUnprocessableEntity, "RULE_VALIDATION_FAILED", "Rule validation failed", err.Error(), false, nil)
	default:
		writeProblem(writer, request, http.StatusServiceUnavailable, "RULE_RUNTIME_UNAVAILABLE", "Rule Runtime unavailable", "The Rule Runtime owner could not complete the request.", true, nil)
	}
}

func writeRuleInvalid(writer http.ResponseWriter, request *http.Request, detail string) {
	writeProblem(writer, request, http.StatusBadRequest, "RULE_REQUEST_INVALID", "Rule request invalid", detail, false, nil)
}

func writeRuleSiteNotFound(writer http.ResponseWriter, request *http.Request) {
	writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site is not visible.", false, nil)
}
