package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
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
	ToolName       string                   `json:"toolName"`
	Calls          uint32                   `json:"calls"`
	Failed         uint32                   `json:"failed"`
	FailureReasons []agentToolFailureReason `json:"failureReasons"`
}

// Why a step failed, as the code the Agent's record keeps (#624); the page puts it into words.
type agentToolFailureReason struct {
	Code  string `json:"code"`
	Count uint32 `json:"count"`
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
			if tool.ToolName == "" || len(tool.ToolName) > 512 || tool.Failed > tool.Calls || tool.FailureReasons == nil || len(tool.FailureReasons) > 32 {
				return agentUsage{}, false
			}
			var reasons uint32
			for _, reason := range tool.FailureReasons {
				if reason.Code == "" || len(reason.Code) > 64 || reason.Count == 0 {
					return agentUsage{}, false
				}
				reasons += reason.Count
			}
			if reasons > tool.Failed {
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
	serviceDelegation, admitted := h.admitAgentRequest(writer, request, session, route.siteID)
	if !admitted {
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
	h.setAgentUpstreamHeaders(ctx, upstream.Header, request, session, serviceDelegation)
	response, err := h.operations.httpClient.Do(upstream)
	if err != nil {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent unavailable", "The Operations Agent did not answer the usage request.", true, nil)
		return
	}
	defer response.Body.Close()
	raw, err := readBoundedBody(response.Body, h.operations.maxResponseBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_BAD_GATEWAY", "Operations Agent gateway failed", "The assistant usage response was unreadable or oversized.", true, nil)
		return
	}
	if response.StatusCode != http.StatusOK {
		writeAgentFailure(writer, request, response.StatusCode, strings.ToLower(response.Header.Get("Content-Type")), raw)
		return
	}
	usage, valid := decodeAgentUsage(raw)
	if !valid {
		writeProblem(writer, request, http.StatusBadGateway, "OPERATIONS_AGENT_CONTRACT_FAILED", "Operations Agent contract failed", "The Operations Agent returned invalid assistant usage.", true, nil)
		return
	}
	writeJSON(writer, http.StatusOK, usage)
}
