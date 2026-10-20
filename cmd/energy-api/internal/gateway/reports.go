package gateway

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
)

// A report route reads the report owner's snapshots of a Site's closed months (#483); it needs
// site.read. A month without a snapshot answers 404 and the page builds it live.
type publicReportRoute struct {
	siteID string
	// internalPath is the report-service path below /v1/sites/{siteId}/reports.
	internalPath string
}

func matchPublicReportRoute(method, path string) (publicReportRoute, bool) {
	remainder, ok := strings.CutPrefix(path, "/api/v1/sites/")
	if !ok || method != http.MethodGet || strings.HasSuffix(remainder, "/") {
		return publicReportRoute{}, false
	}
	segments := strings.Split(remainder, "/")
	if len(segments) < 3 || segments[1] != "reports" || segments[2] != "monthly" {
		return publicReportRoute{}, false
	}
	siteID, err := url.PathUnescape(segments[0])
	if err != nil {
		return publicReportRoute{}, false
	}
	switch len(segments) {
	case 3:
		return publicReportRoute{siteID: siteID, internalPath: "/monthly"}, true
	case 4:
		return publicReportRoute{siteID: siteID, internalPath: "/monthly/" + url.PathEscape(segments[3])}, true
	}
	return publicReportRoute{}, false
}

func dispatchReportRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicReportRoute) {
	decision := routeDecisionFromContext(request.Context())
	if decision.SelectedOwner != ownershipregistry.OwnerReport || h.intelligence == nil || h.intelligence.reportBaseURL == "" {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REPORT_UNAVAILABLE", "Reports unavailable", "The report route is not active for this Session.", true, nil)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	session, ok := h.intelligenceSession(writer, request, false)
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
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/reports" + route.internalPath
	if rawQuery := request.URL.RawQuery; rawQuery != "" {
		internalPath += "?" + rawQuery
	}
	raw, status, err := h.executeOwner(request, session, reportOwner, h.intelligence.reportBaseURL, route.siteID, reportReadAction, http.MethodGet, internalPath, nil)
	if err == nil && status == http.StatusBadRequest {
		writeProblem(writer, request, http.StatusBadRequest, "REPORT_REQUEST_INVALID", "Report request invalid", "The report month or year is invalid.", false, nil)
		return
	}
	h.writeIntelligenceResult(writer, request, raw, status, err)
}
