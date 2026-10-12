package gateway

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

// A savings route is one of the M&V owner's Site-scoped ledger operations (ADR 0021). Reads need
// site.read; registering and lifecycling a measure needs savings.manage on the Site.
type publicSavingsRoute struct {
	siteID    string
	measureID string
	// internalPath is the mv-service path below /v1/sites/{siteId}/savings.
	internalPath string
	write        bool
}

func matchPublicSavingsRoute(method, path string) (publicSavingsRoute, bool) {
	remainder, ok := strings.CutPrefix(path, "/api/v1/sites/")
	if !ok || strings.HasSuffix(remainder, "/") {
		return publicSavingsRoute{}, false
	}
	segments := strings.Split(remainder, "/")
	if len(segments) < 3 || segments[1] != "savings" {
		return publicSavingsRoute{}, false
	}
	siteID, err := url.PathUnescape(segments[0])
	if err != nil {
		return publicSavingsRoute{}, false
	}
	route := publicSavingsRoute{siteID: siteID}
	switch {
	case len(segments) == 3 && segments[2] == "summary":
		route.internalPath = "/summary"
		return route, method == http.MethodGet
	case len(segments) == 3 && segments[2] == "measures":
		route.internalPath = "/measures"
		route.write = method == http.MethodPost
		return route, method == http.MethodGet || method == http.MethodPost
	case len(segments) == 4 && segments[2] == "measures":
		route.measureID = segments[3]
		route.internalPath = "/measures/" + url.PathEscape(route.measureID)
		return route, method == http.MethodGet
	case len(segments) == 5 && segments[2] == "measures" && (segments[4] == "implement" || segments[4] == "refit" || segments[4] == "end"):
		route.measureID = segments[3]
		route.internalPath = "/measures/" + url.PathEscape(route.measureID) + "/" + segments[4]
		route.write = true
		return route, method == http.MethodPost
	}
	return publicSavingsRoute{}, false
}

func dispatchSavingsRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicSavingsRoute) {
	decision := routeDecisionFromContext(request.Context())
	if decision.SelectedOwner != ownershipregistry.OwnerMV || h.intelligence == nil || h.intelligence.mvBaseURL == "" {
		writeProblem(writer, request, http.StatusServiceUnavailable, "SAVINGS_UNAVAILABLE", "Savings verification unavailable", "The savings verification route is not active for this Session.", true, nil)
		return
	}
	if !isLowerUUIDv7(route.siteID) || (route.measureID != "" && !isLowerUUIDv7(route.measureID)) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested savings measure was not found.", false, nil)
		return
	}
	session, ok := h.intelligenceSession(writer, request, route.write)
	if !ok {
		return
	}
	readable, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := readable[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	headers := map[string]string{"X-Tenant-ID": session.TenantID}
	var body []byte
	if route.write {
		authorization, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSavingsManage)
		if failure != nil && failure.status != http.StatusForbidden && failure.status != http.StatusNotFound {
			writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
			return
		}
		if _, allowed := authorization.allowedSiteIDs[route.siteID]; failure != nil || !allowed {
			writeProblem(writer, request, http.StatusForbidden, "SAVINGS_MANAGE_FORBIDDEN", "Savings management forbidden", "The authenticated Principal is not allowed to manage savings measures on this Site.", false, nil)
			return
		}
		raw, err := readBoundedBody(request.Body, maximumIntelligenceRequestBytes)
		if err != nil || !json.Valid(raw) {
			writeProblem(writer, request, http.StatusBadRequest, "SAVINGS_REQUEST_INVALID", "Savings request invalid", "The savings request body is invalid.", false, nil)
			return
		}
		body = raw
		headers["X-Actor-ID"] = session.Principal.Subject
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/savings" + route.internalPath
	if rawQuery := request.URL.RawQuery; rawQuery != "" && !route.write {
		internalPath += "?" + rawQuery
	}
	method := http.MethodGet
	if route.write {
		method = http.MethodPost
	}
	raw, status, err := h.executeIntelligence(request, method, h.intelligence.mvBaseURL+internalPath, body, headers)
	h.writeSavingsResult(writer, request, raw, status, err)
}

// writeSavingsResult passes the owner's refusals through, since a conflicting reporting period
// or a frozen baseline is something the operator acts on, not an outage.
func (h *handler) writeSavingsResult(writer http.ResponseWriter, request *http.Request, raw []byte, status int, err error) {
	if err == nil && (status == http.StatusBadRequest || status == http.StatusConflict) {
		var refusal struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(raw, &refusal) == nil && strings.HasPrefix(refusal.Code, "savings_") {
			writeProblem(writer, request, status, strings.ToUpper(refusal.Code), "Savings request refused", savingsRefusalDetail[refusal.Code], false, nil)
			return
		}
	}
	h.writeIntelligenceResult(writer, request, raw, status, err)
}

var savingsRefusalDetail = map[string]string{
	"savings_request_invalid":        "The savings request is incomplete or out of range.",
	"savings_measure_state_conflict": "The measure is not in a state that allows this.",
	"savings_reporting_overlap":      "Another measure on this Site reports over the same period.",
	"savings_baseline_locked":        "The baseline is frozen because daily results have been published.",
}
