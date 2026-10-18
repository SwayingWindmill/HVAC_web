package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const PublicAgentUsagePathTemplate = "/api/v1/sites/{siteId}/operations/agent-usage"

// agentUsage is the public shape of the assistant's usage (#597): figures and Tool names only.
// Decoding into it strictly keeps anything else the Agent might send away from the browser.
type agentUsage struct {
	SchemaVersion int             `json:"schemaVersion"`
	Timezone      string          `json:"timezone"`
	Days          []agentUsageDay `json:"days"`
}

type agentUsageDay struct {
	Date              string           `json:"date"`
	Completed         uint32           `json:"completed"`
	Failed            uint32           `json:"failed"`
	Cancelled         uint32           `json:"cancelled"`
	AverageDurationMs *uint64          `json:"averageDurationMs"`
	InputTokens       uint64           `json:"inputTokens"`
	OutputTokens      uint64           `json:"outputTokens"`
	Tools             []agentToolUsage `json:"tools"`
}

type agentToolUsage struct {
	ToolName string `json:"toolName"`
	Calls    uint32 `json:"calls"`
	Failed   uint32 `json:"failed"`
}

var agentUsageDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func decodeAgentUsage(raw []byte) (agentUsage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var usage agentUsage
	if decoder.Decode(&usage) != nil || decoder.Decode(&struct{}{}) != io.EOF || usage.SchemaVersion != 1 || len(usage.Days) != 14 {
		return agentUsage{}, false
	}
	for _, day := range usage.Days {
		if !agentUsageDate.MatchString(day.Date) || day.Tools == nil || len(day.Tools) > 64 {
			return agentUsage{}, false
		}
		for _, tool := range day.Tools {
			if tool.ToolName == "" || len(tool.ToolName) > 512 || tool.Failed > tool.Calls {
				return agentUsage{}, false
			}
		}
	}
	return usage, true
}

// proxyAgentUsage answers the usage view for whoever may read the Tenant's records, of a Site they
// may read, before the Agent is asked.
func (h *handler) proxyAgentUsage(writer http.ResponseWriter, request *http.Request, route publicAgentSessionRoute) {
	if h.operations == nil || h.operations.baseURL == "" || h.operations.httpClient == nil || h.identity == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_AGENT_UNAVAILABLE", "Operations Agent unavailable", "The Operations Agent is not configured.", true, nil)
		return
	}
	query := request.URL.Query()
	timezone := query.Get("timezone")
	if len(query) != 1 || len(query["timezone"]) != 1 || timezone == "" || len(timezone) > 64 {
		writeProblem(writer, request, http.StatusBadRequest, "REQUEST_INVALID", "Request invalid", "Assistant usage takes only the Site time zone, as timezone.", false, nil)
		return
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "REQUEST_INVALID", "Request invalid", "The time zone is not a known IANA time zone.", false, nil)
		return
	}
	session, ok := h.commandSession(writer, request, false)
	if !ok {
		return
	}
	principal, failure := h.identity.fetchPrincipal(request.Context(), session)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !principal.Authorization.Has(identitycontext.CapabilityAuditRead) {
		writeProblem(writer, request, http.StatusForbidden, "OPERATIONS_USAGE_FORBIDDEN", "Assistant usage forbidden", "Reading the assistant's usage requires audit.read.", false, nil)
		return
	}
	if h.operations.rateLimiter == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_LIMIT_UNAVAILABLE", "Operations limit unavailable", "The Agent Session limit policy could not be evaluated.", true, nil)
		return
	}
	if decision := h.operations.rateLimiter.Allow(request.Context(), limitpolicy.DimensionOperationsAgent, session.ID); !decision.Allowed {
		if decision.Reason == "counter-unavailable" {
			writeProblem(writer, request, http.StatusServiceUnavailable, "OPERATIONS_LIMIT_UNAVAILABLE", "Operations limit unavailable", "The Agent Session limit policy could not be evaluated.", true, nil)
			return
		}
		writeProblem(writer, request, http.StatusTooManyRequests, "OPERATIONS_RATE_LIMITED", "Operations rate limited", "The Agent Session request rate has been exceeded.", true, nil)
		return
	}
	siteAuthorization, siteFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if siteFailure != nil || !registryAuthorizationAllowsSite(siteAuthorization, route.siteID) {
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
	upstream, err := http.NewRequestWithContext(ctx, http.MethodGet, h.operations.baseURL+route.internalPath+"?timezone="+url.QueryEscape(timezone), nil)
	if err != nil {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent gateway failed", "The assistant usage request could not be created.", true, nil)
		return
	}
	upstream.Header.Set("Accept", "application/json, application/problem+json")
	upstream.Header.Set("X-Tenant-ID", session.TenantID)
	upstream.Header.Set("X-Delegation-Grant", serviceDelegation)
	upstream.Header.Set("X-Route-Policy-Revision", formatRevision(routeDecisionFromContext(request.Context()).RegistryRevision))
	upstream.Header.Set("X-Request-ID", requestIDFromContext(request.Context()))
	injectOperationsTrace(ctx, upstream.Header)
	response, err := h.operations.httpClient.Do(upstream)
	if err != nil {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent unavailable", "The Operations Agent did not answer the usage request.", true, nil)
		return
	}
	defer response.Body.Close()
	raw, err := readBoundedBody(response.Body, 256<<10)
	if err != nil || response.StatusCode != http.StatusOK {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent unavailable", "The Operations Agent did not answer the usage request.", true, nil)
		return
	}
	usage, valid := decodeAgentUsage(raw)
	if !valid {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_CONTRACT_FAILED", "Operations Agent contract failed", "The Operations Agent returned invalid assistant usage.", true, nil)
		return
	}
	writeJSON(writer, http.StatusOK, usage)
}
