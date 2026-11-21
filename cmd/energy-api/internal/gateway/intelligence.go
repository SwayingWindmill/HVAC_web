package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	publicOptimizationRunsPath      = "/api/v1/optimization/runs"
	defaultOptimizationWorkloadID   = "spiffe://hvac.local/optimization-service"
	defaultIntelligenceTimeout      = 8 * time.Second
	maximumIntelligenceRequestBytes = int64(256 << 10)
	maximumIntelligenceResponse     = int64(8 << 20)
)

type IntelligenceConfig struct {
	ForecastBaseURL     string
	FDDBaseURL          string
	MVBaseURL           string
	ReportBaseURL       string
	OptimizationBaseURL string
	// OwnerHTTPClient calls FDD, Forecast, Optimization, M&V and Report over mutual TLS with the
	// Gateway's workload identity (#595, #612, #613).
	OwnerHTTPClient *http.Client
	Timeout         time.Duration
}

type intelligenceController struct {
	forecastBaseURL     string
	fddBaseURL          string
	mvBaseURL           string
	reportBaseURL       string
	optimizationBaseURL string
	ownerHTTPClient     *http.Client
	timeout             time.Duration
}

// intelligenceOwner is an owner that serves only contexts the Gateway signs, each for one action on
// one Tenant and Site (#595, #612, #613); before, they trusted whatever X-Tenant-ID a caller sent.
type intelligenceOwner struct{ audience, header string }

var (
	fddOwner          = intelligenceOwner{audience: "fdd-service", header: "X-FDD-Context"}
	forecastOwner     = intelligenceOwner{audience: "forecast-service", header: "X-Forecast-Context"}
	optimizationOwner = intelligenceOwner{audience: "optimization-service", header: "X-Optimization-Context"}
	mvOwner           = intelligenceOwner{audience: "mv-service", header: "X-MV-Context"}
	reportOwner       = intelligenceOwner{audience: "report-service", header: "X-Report-Context"}
)

const (
	fddReadAction           = "fdd:read"
	fddWriteAction          = "fdd:write"
	forecastReadAction      = "forecast:read"
	optimizationReadAction  = "optimization:read"
	optimizationWriteAction = "optimization:write"
	mvReadAction            = "mv:read"
	mvWriteAction           = "mv:write"
	reportReadAction        = "report:read"
)

type publicIntelligenceRoute struct {
	owner      string
	siteID     string
	findingID  string
	loopKey    string
	target     string
	method     string
	publicPath string
}

type fddLinkRequest struct {
	AlarmID     string `json:"alarmId"`
	WorkOrderID string `json:"workOrderId"`
}

// An operator's low-delta-T evaluation names the loop; FDD's rule in effect says the rest.
type fddLowDeltaTEvaluationRequest struct {
	AssetID  string `json:"assetId"`
	DeviceID string `json:"deviceId"`
}

type fddLowDeltaTEvaluationUpstream struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
	AssetID  string `json:"assetId"`
	DeviceID string `json:"deviceId"`
}

func newIntelligenceController(config *IntelligenceConfig) *intelligenceController {
	if config == nil {
		return nil
	}
	resolved := *config
	resolved.ForecastBaseURL = strings.TrimRight(strings.TrimSpace(resolved.ForecastBaseURL), "/")
	resolved.FDDBaseURL = strings.TrimRight(strings.TrimSpace(resolved.FDDBaseURL), "/")
	resolved.MVBaseURL = strings.TrimRight(strings.TrimSpace(resolved.MVBaseURL), "/")
	resolved.ReportBaseURL = strings.TrimRight(strings.TrimSpace(resolved.ReportBaseURL), "/")
	resolved.OptimizationBaseURL = strings.TrimRight(strings.TrimSpace(resolved.OptimizationBaseURL), "/")
	if resolved.Timeout <= 0 || resolved.Timeout > 30*time.Second {
		resolved.Timeout = defaultIntelligenceTimeout
	}
	return &intelligenceController{
		forecastBaseURL: resolved.ForecastBaseURL, fddBaseURL: resolved.FDDBaseURL, mvBaseURL: resolved.MVBaseURL, reportBaseURL: resolved.ReportBaseURL, optimizationBaseURL: resolved.OptimizationBaseURL,
		ownerHTTPClient: resolved.OwnerHTTPClient, timeout: resolved.Timeout,
	}
}

func matchPublicIntelligenceRoute(method, path string) (publicIntelligenceRoute, bool) {
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/forecast/load", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerForecast, siteID: siteID, target: "load", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/forecast/load"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/forecast/pv", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerForecast, siteID: siteID, target: "pv", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/forecast/pv"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/fdd/findings", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerFDD, siteID: siteID, target: "fdd", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/fdd/findings"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/fdd/rules/low-delta-t", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerFDD, siteID: siteID, target: "fdd-rule", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/fdd/rules/low-delta-t"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/fdd/evaluate/low-delta-t", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerFDD, siteID: siteID, target: "fdd-evaluate", method: http.MethodPost, publicPath: "/api/v1/sites/{siteId}/fdd/evaluate/low-delta-t"}, method == http.MethodPost
	}
	if siteID, findingID, ok := matchFDDLinkPath(path); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerFDD, siteID: siteID, findingID: findingID, target: "fdd-link", method: http.MethodPatch, publicPath: "/api/v1/sites/{siteId}/fdd/findings/{findingId}/links"}, method == http.MethodPatch
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/recommendations/latest", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, siteID: siteID, target: "optimization-latest", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/optimization/recommendations/latest"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/preparations/latest", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, siteID: siteID, target: "optimization-preparation", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/optimization/preparations/latest"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/shadow-evaluations/latest", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, siteID: siteID, target: "optimization-shadow", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/optimization/shadow-evaluations/latest"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/staging-recommendations/latest", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, siteID: siteID, target: "optimization-staging", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/optimization/staging-recommendations/latest"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/governance-summary", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, siteID: siteID, target: "optimization-governance", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/optimization/governance-summary"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/canary-bindings", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, siteID: siteID, target: "optimization-canary-list", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/optimization/canary-bindings"}, method == http.MethodGet
	}
	if siteID, loopKey, ok := matchOptimizationCanaryBindingPath(path); ok {
		switch method {
		case http.MethodPut:
			return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, siteID: siteID, loopKey: loopKey, target: "optimization-canary-upsert", method: http.MethodPut, publicPath: "/api/v1/sites/{siteId}/optimization/canary-bindings/{loopKey}"}, true
		case http.MethodDelete:
			return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, siteID: siteID, loopKey: loopKey, target: "optimization-canary-delete", method: http.MethodDelete, publicPath: "/api/v1/sites/{siteId}/optimization/canary-bindings/{loopKey}"}, true
		default:
			return publicIntelligenceRoute{}, false
		}
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/negative-samples", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, siteID: siteID, target: "optimization-negative-samples", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/optimization/negative-samples"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/auto-control-lease", "{siteId}"); ok {
		switch method {
		case http.MethodGet:
			return publicIntelligenceRoute{owner: ownershipregistry.OwnerCore, siteID: siteID, target: "auto-control-lease", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/optimization/auto-control-lease"}, true
		case http.MethodPut:
			return publicIntelligenceRoute{owner: ownershipregistry.OwnerCore, siteID: siteID, target: "auto-control-lease-write", method: http.MethodPut, publicPath: "/api/v1/sites/{siteId}/optimization/auto-control-lease"}, true
		default:
			return publicIntelligenceRoute{}, false
		}
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/auto-control-lease/revoke", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerCore, siteID: siteID, target: "auto-control-lease-revoke", method: http.MethodPost, publicPath: "/api/v1/sites/{siteId}/optimization/auto-control-lease/revoke"}, method == http.MethodPost
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/execution-monitors", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerCore, siteID: siteID, target: "optimization-execution-monitors", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/optimization/execution-monitors"}, method == http.MethodGet
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/auto-commands", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerCommand, siteID: siteID, target: "optimization-auto-command", method: http.MethodPost, publicPath: "/api/v1/sites/{siteId}/optimization/auto-commands"}, method == http.MethodPost
	}
	if path == publicOptimizationRunsPath {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, method: http.MethodPost, publicPath: publicOptimizationRunsPath}, method == http.MethodPost
	}
	return publicIntelligenceRoute{}, false
}

// IsOptimizationAutoCommandPath reports whether path targets the workload-only automated
// optimization Command endpoint served on the Gateway's internal mTLS listener.
func IsOptimizationAutoCommandPath(path string) bool {
	_, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/auto-commands", "{siteId}")
	return ok
}

func matchFDDLinkPath(path string) (string, string, bool) {
	remainder, ok := strings.CutPrefix(path, "/api/v1/sites/")
	if !ok || strings.HasSuffix(remainder, "/") {
		return "", "", false
	}
	segments := strings.Split(remainder, "/")
	if len(segments) != 5 || segments[1] != "fdd" || segments[2] != "findings" || segments[4] != "links" {
		return "", "", false
	}
	siteID, err := url.PathUnescape(segments[0])
	if err != nil || !isLowerUUIDv7(siteID) {
		return "", "", false
	}
	findingID, err := url.PathUnescape(segments[3])
	if err != nil || !isLowerUUIDv7(findingID) {
		return "", "", false
	}
	return siteID, findingID, true
}

func matchOptimizationCanaryBindingPath(path string) (string, string, bool) {
	remainder, ok := strings.CutPrefix(path, "/api/v1/sites/")
	if !ok || strings.HasSuffix(remainder, "/") {
		return "", "", false
	}
	segments := strings.Split(remainder, "/")
	if len(segments) != 4 || segments[1] != "optimization" || segments[2] != "canary-bindings" {
		return "", "", false
	}
	siteID, err := url.PathUnescape(segments[0])
	if err != nil || siteID == "" {
		return "", "", false
	}
	loopKey, err := url.PathUnescape(segments[3])
	if err != nil || strings.TrimSpace(loopKey) == "" {
		return "", "", false
	}
	return siteID, loopKey, true
}

func dispatchIntelligenceRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicIntelligenceRoute) {
	decision := routeDecisionFromContext(request.Context())
	if decision.SelectedOwner != route.owner {
		writeProblem(writer, request, http.StatusServiceUnavailable, "INTELLIGENCE_UNAVAILABLE", "Intelligence unavailable", "The Intelligence route is not active for this Session.", true, nil)
		return
	}
	if route.target == "optimization-auto-command" {
		h.serveOptimizationAutoCommand(writer, request, route)
		return
	}
	if route.owner == ownershipregistry.OwnerCore {
		session, ok := h.intelligenceSession(writer, request, route.method != http.MethodGet)
		if !ok {
			return
		}
		switch route.target {
		case "auto-control-lease":
			h.serveAutoControlLeaseRead(writer, request, session, route)
		case "auto-control-lease-write":
			h.serveAutoControlLeaseArm(writer, request, session, route)
		case "auto-control-lease-revoke":
			h.serveAutoControlLeaseRevoke(writer, request, session, route)
		case "optimization-execution-monitors":
			h.serveOptimizationExecutionMonitorsRead(writer, request, session, route)
		}
		return
	}
	if h.intelligence == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "INTELLIGENCE_UNAVAILABLE", "Intelligence unavailable", "The Intelligence route is not active for this Session.", true, nil)
		return
	}
	session, ok := h.intelligenceSession(writer, request, route.method != http.MethodGet)
	if !ok {
		return
	}
	if route.owner == ownershipregistry.OwnerForecast {
		h.serveForecast(writer, request, session, route)
		return
	}
	if route.owner == ownershipregistry.OwnerFDD {
		if route.target == "fdd-evaluate" {
			h.serveFDDEvaluation(writer, request, session, route)
			return
		}
		if route.target == "fdd-link" {
			h.serveFDDLink(writer, request, session, route)
			return
		}
		h.serveFDDRead(writer, request, session, route)
		return
	}
	if route.target == "optimization-latest" {
		h.serveLatestOptimizationRead(writer, request, session, route, "recommendations")
		return
	}
	if route.target == "optimization-preparation" {
		h.serveLatestOptimizationRead(writer, request, session, route, "preparations")
		return
	}
	if route.target == "optimization-shadow" {
		h.serveLatestOptimizationRead(writer, request, session, route, "shadow-evaluations")
		return
	}
	if route.target == "optimization-staging" {
		h.serveLatestOptimizationRead(writer, request, session, route, "staging-recommendations")
		return
	}
	if route.target == "optimization-governance" {
		h.serveOptimizationGovernanceSummaryRead(writer, request, session, route)
		return
	}
	if route.target == "optimization-canary-list" {
		h.serveOptimizationCanaryList(writer, request, session, route)
		return
	}
	if route.target == "optimization-canary-upsert" {
		h.serveOptimizationCanaryUpsert(writer, request, session, route)
		return
	}
	if route.target == "optimization-canary-delete" {
		h.serveOptimizationCanaryDelete(writer, request, session, route)
		return
	}
	if route.target == "optimization-negative-samples" {
		h.serveOptimizationNegativeSamplesRead(writer, request, session, route)
		return
	}
	h.createOptimizationRun(writer, request, session)
}

func (h *handler) intelligenceSession(writer http.ResponseWriter, request *http.Request, requireCSRF bool) (bffSession, bool) {
	session, ok := routeSessionFromContext(request.Context())
	if !ok {
		var failure *identityFailure
		session, failure = h.identitySession(request)
		if failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return bffSession{}, false
		}
	}
	if !requireCSRF || !session.needsBrowserProof() {
		return session, true
	}
	if h.identity == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "INTELLIGENCE_UNAVAILABLE", "Intelligence unavailable", "Session validation is unavailable.", true, nil)
		return bffSession{}, false
	}
	csrf := request.Header.Get("X-CSRF-Token")
	if csrf == "" || request.Header.Get("Origin") != h.identity.config.PublicOrigin || subtle.ConstantTimeCompare([]byte(csrf), []byte(session.CSRFToken)) != 1 {
		writeProblem(writer, request, http.StatusForbidden, "CSRF_INVALID", "CSRF token invalid", "A valid Session-bound CSRF token and Origin are required.", false, nil)
		return bffSession{}, false
	}
	return session, true
}

func (h *handler) authorizeIntelligenceSitesWithWriter(writer http.ResponseWriter, request *http.Request, session bffSession) (map[string]struct{}, bool) {
	authorization, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if failure != nil {
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site or Intelligence result was not found.", false, nil)
			return nil, false
		}
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return nil, false
	}
	return authorization.allowedSiteIDs, true
}

func (h *handler) serveForecast(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if h.intelligence.forecastBaseURL == "" {
		writeRouteWithoutOwner(writer, request)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested forecast was not found.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested forecast was not found.", false, nil)
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/forecast/" + route.target
	raw, status, err := h.executeOwner(request, session, forecastOwner, h.intelligence.forecastBaseURL, route.siteID, forecastReadAction, http.MethodGet, internalPath, nil)
	h.writeIntelligenceResult(writer, request, raw, status, err)
}

// serveFDDRead reads a Site's FDD findings, or the low-delta-T rule in effect, on site.read.
func (h *handler) serveFDDRead(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if h.intelligence.fddBaseURL == "" {
		writeRouteWithoutOwner(writer, request)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested FDD resource was not found.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested FDD resource was not found.", false, nil)
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/fdd/findings"
	if route.target == "fdd-rule" {
		internalPath = "/v1/sites/" + url.PathEscape(route.siteID) + "/fdd/rules/low-delta-t"
	} else if rawQuery := strings.TrimSpace(request.URL.RawQuery); rawQuery != "" {
		internalPath += "?" + rawQuery
	}
	raw, status, err := h.executeOwner(request, session, fddOwner, h.intelligence.fddBaseURL, route.siteID, fddReadAction, http.MethodGet, internalPath, nil)
	h.writeIntelligenceResult(writer, request, raw, status, err)
}

func (h *handler) serveFDDLink(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if h.intelligence.fddBaseURL == "" {
		writeRouteWithoutOwner(writer, request)
		return
	}
	if !isLowerUUIDv7(route.siteID) || !isLowerUUIDv7(route.findingID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested FDD finding was not found.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested FDD finding was not found.", false, nil)
		return
	}
	raw, err := readBoundedBody(request.Body, maximumIntelligenceRequestBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "FDD_LINK_REQUEST_INVALID", "FDD link request invalid", "The FDD link request is too large or unreadable.", false, nil)
		return
	}
	var input fddLinkRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureIntelligenceJSONEOF(decoder) != nil || input.AlarmID == "" || input.WorkOrderID == "" {
		writeProblem(writer, request, http.StatusBadRequest, "FDD_LINK_REQUEST_INVALID", "FDD link request invalid", "The FDD link request must identify both the Alarm and its Work Order.", false, nil)
		return
	}
	if !isLowerUUIDv7(input.AlarmID) || !isLowerUUIDv7(input.WorkOrderID) {
		writeProblem(writer, request, http.StatusUnprocessableEntity, "FDD_LINK_SOURCE_INVALID", "FDD link source invalid", "The Alarm and Work Order sources must be valid identities.", false, nil)
		return
	}
	scope, failure := h.resolveAlarmScope(request, session, input.AlarmID)
	if failure != nil {
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Alarm source was not found.", false, nil)
			return
		}
		writeProblem(writer, request, http.StatusServiceUnavailable, "FDD_LINK_SOURCE_UNAVAILABLE", "FDD link source unavailable", "The Alarm owner could not resolve the source.", true, nil)
		return
	}
	if scope.TenantID != session.TenantID || scope.SiteID != route.siteID {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Alarm source was not found.", false, nil)
		return
	}
	if failure := h.validateFDDWorkOrder(request, session, route.siteID, input.WorkOrderID, input.AlarmID); failure != nil {
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Work Order source was not found for this Alarm.", false, nil)
			return
		}
		writeProblem(writer, request, http.StatusServiceUnavailable, "FDD_LINK_SOURCE_UNAVAILABLE", "FDD link source unavailable", "The Work Order owner could not resolve the source.", true, nil)
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/fdd/findings/" + url.PathEscape(route.findingID) + "/links"
	responseBody, status, callErr := h.executeOwner(request, session, fddOwner, h.intelligence.fddBaseURL, route.siteID, fddWriteAction, http.MethodPatch, internalPath, raw)
	h.writeIntelligenceResult(writer, request, responseBody, status, callErr)
}

func (h *handler) validateFDDWorkOrder(request *http.Request, session bffSession, siteID, workOrderID, alarmID string) *workOrderFailure {
	if h.workOrder == nil || h.workOrder.operations == nil {
		failure := workOrderUnavailable("The Work Order read service is not configured.")
		return &failure
	}
	route := publicWorkOrderRoute{
		kind: publicWorkOrderDetail, template: "/api/v1/sites/{siteId}/work-orders/{workOrderId}",
		siteID: siteID, workOrderID: workOrderID, action: workorderauth.ActionRead,
	}
	decision, failure := h.authorizeWorkOrder(request, session, route, nil, nil)
	if failure != nil {
		return failure
	}
	site, err := h.resolveAuthoritativeSiteForDomain(request, session, siteID)
	if err != nil || site.TenantID != session.TenantID {
		unavailable := workOrderUnavailable("The authoritative Tenant scope for this Site could not be resolved.")
		return &unavailable
	}
	readContext, failure := h.signWorkOrderReadContext(session, route, decision, site.TenantID, h.identity.config.ExecutingWorkloadSPIFFE)
	if failure != nil {
		return failure
	}
	body, status, failure := h.executeWorkOrderRead(request, route, readContext)
	if failure != nil {
		return failure
	}
	if status == http.StatusNotFound || status == http.StatusForbidden {
		denied := workOrderDenied()
		return &denied
	}
	if status != http.StatusOK {
		unavailable := workOrderUnavailable("Work Order Service could not resolve the requested source.")
		return &unavailable
	}
	var workOrder workordermodel.WorkOrder
	if decodeStrictWorkOrderJSON(body, &workOrder) != nil || workOrder.Validate() != nil || workOrder.TenantID != session.TenantID || workOrder.SiteID != siteID || workOrder.WorkOrderID != workOrderID {
		unavailable := workOrderUnavailable("Work Order Service returned an invalid source projection.")
		return &unavailable
	}
	if workOrderOriginatesFromAlarm(workOrder, alarmID) {
		return nil
	}
	denied := workOrderDenied()
	return &denied
}

func workOrderOriginatesFromAlarm(workOrder workordermodel.WorkOrder, alarmID string) bool {
	for _, reference := range workOrder.SourceReferences {
		if reference.Domain == workordermodel.SourceAlarm && reference.Relationship == workordermodel.RelationshipOrigin && reference.ResourceID == alarmID {
			return true
		}
	}
	return false
}

func (h *handler) serveFDDEvaluation(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if !isLowerUUIDv7(route.siteID) || h.intelligence.fddBaseURL == "" {
		writeProblem(writer, request, http.StatusServiceUnavailable, "FDD_EVALUATION_UNAVAILABLE", "FDD evaluation unavailable", "The production FDD evaluation path is not configured.", true, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	raw, err := readBoundedBody(request.Body, maximumIntelligenceRequestBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "FDD_EVALUATION_REQUEST_INVALID", "FDD evaluation request invalid", "The FDD evaluation request is too large or unreadable.", false, nil)
		return
	}
	var input fddLowDeltaTEvaluationRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureIntelligenceJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "FDD_EVALUATION_REQUEST_INVALID", "FDD evaluation request invalid", "The FDD evaluation request body is invalid.", false, nil)
		return
	}
	// FDD reads the loop evidence itself (ADR 0020); the Gateway only decides the Site.
	upstreamBody, err := json.Marshal(fddLowDeltaTEvaluationUpstream{TenantID: session.TenantID, SiteID: route.siteID, AssetID: input.AssetID, DeviceID: input.DeviceID})
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "FDD_EVALUATION_REQUEST_INVALID", "FDD evaluation request invalid", "The FDD evaluation request could not be encoded.", false, nil)
		return
	}
	responseBody, status, callErr := h.executeOwner(request, session, fddOwner, h.intelligence.fddBaseURL, route.siteID, fddWriteAction, http.MethodPost, "/v1/fdd/evaluate/low-delta-t", upstreamBody)
	h.writeIntelligenceResult(writer, request, responseBody, status, callErr)
}

// serveLatestOptimizationRead reads the Site's latest Optimization recommendation, or how its
// schedule's latest firing went, from the owner.
func (h *handler) serveLatestOptimizationRead(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute, resource string) {
	if h.intelligence.optimizationBaseURL == "" {
		writeRouteWithoutOwner(writer, request)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Optimization result was not found.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Optimization result was not found.", false, nil)
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/optimization/" + resource + "/latest"
	raw, status, err := h.executeOwner(request, session, optimizationOwner, h.intelligence.optimizationBaseURL, route.siteID, optimizationReadAction, http.MethodGet, internalPath, nil)
	h.writeIntelligenceResult(writer, request, raw, status, err)
}

func (h *handler) serveOptimizationGovernanceSummaryRead(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if h.intelligence.optimizationBaseURL == "" {
		writeRouteWithoutOwner(writer, request)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Optimization governance summary was not found.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Optimization governance summary was not found.", false, nil)
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/optimization/governance-summary"
	if rawQuery := strings.TrimSpace(request.URL.RawQuery); rawQuery != "" {
		internalPath += "?" + rawQuery
	}
	raw, status, err := h.executeOwner(request, session, optimizationOwner, h.intelligence.optimizationBaseURL, route.siteID, optimizationReadAction, http.MethodGet, internalPath, nil)
	h.writeIntelligenceResult(writer, request, raw, status, err)
}

type optimizationCanaryUpsertPayload struct {
	ServingRef string `json:"servingRef"`
	Status     string `json:"status"`
}

func (h *handler) serveOptimizationCanaryList(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if h.intelligence.optimizationBaseURL == "" {
		writeRouteWithoutOwner(writer, request)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/optimization/canary-bindings"
	raw, status, err := h.executeOwner(request, session, optimizationOwner, h.intelligence.optimizationBaseURL, route.siteID, optimizationReadAction, http.MethodGet, internalPath, nil)
	h.writeIntelligenceResult(writer, request, raw, status, err)
}

func (h *handler) serveOptimizationCanaryUpsert(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if h.intelligence.optimizationBaseURL == "" {
		writeRouteWithoutOwner(writer, request)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	if _, ok := h.authorizeSiteControlExitWrite(writer, request, session, route.siteID, "Schedule manage authorization is required to manage optimization canary routing."); !ok {
		return
	}
	raw, err := readBoundedBody(request.Body, maximumIntelligenceRequestBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "OPTIMIZATION_CANARY_REQUEST_INVALID", "Canary binding request invalid", "The canary binding request is too large or unreadable.", false, nil)
		return
	}
	var payload optimizationCanaryUpsertPayload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || ensureIntelligenceJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "OPTIMIZATION_CANARY_REQUEST_INVALID", "Canary binding request invalid", "The canary binding request payload is invalid.", false, nil)
		return
	}
	upstreamBody, err := json.Marshal(payload)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "OPTIMIZATION_CANARY_REQUEST_INVALID", "Canary binding request invalid", "The canary binding request could not be encoded.", false, nil)
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/optimization/canary-bindings/" + url.PathEscape(route.loopKey)
	respRaw, status, callErr := h.executeOwner(request, session, optimizationOwner, h.intelligence.optimizationBaseURL, route.siteID, optimizationWriteAction, http.MethodPut, internalPath, upstreamBody)
	if callErr == nil && status == http.StatusUnprocessableEntity {
		writeProblem(writer, request, http.StatusUnprocessableEntity, "OPTIMIZATION_SOLVER_HOST_NOT_ALLOWED", "Solver host not allowed", "External solver host is not in the configured allowlist.", false, nil)
		return
	}
	if callErr == nil && status == http.StatusBadRequest {
		writeProblem(writer, request, http.StatusBadRequest, "OPTIMIZATION_CANARY_REQUEST_INVALID", "Canary binding request invalid", "The canary binding parameters or loop key are invalid.", false, nil)
		return
	}
	h.writeIntelligenceResult(writer, request, respRaw, status, callErr)
}

func (h *handler) serveOptimizationCanaryDelete(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if h.intelligence.optimizationBaseURL == "" {
		writeRouteWithoutOwner(writer, request)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	if _, ok := h.authorizeSiteControlExitWrite(writer, request, session, route.siteID, "Schedule manage authorization is required to manage optimization canary routing."); !ok {
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/optimization/canary-bindings/" + url.PathEscape(route.loopKey)
	respRaw, status, callErr := h.executeOwner(request, session, optimizationOwner, h.intelligence.optimizationBaseURL, route.siteID, optimizationWriteAction, http.MethodDelete, internalPath, nil)
	if callErr == nil && status == http.StatusBadRequest {
		writeProblem(writer, request, http.StatusBadRequest, "OPTIMIZATION_CANARY_REQUEST_INVALID", "Canary binding request invalid", "The canary binding loop key is invalid.", false, nil)
		return
	}
	h.writeIntelligenceResult(writer, request, respRaw, status, callErr)
}

func (h *handler) serveOptimizationNegativeSamplesRead(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if h.intelligence.optimizationBaseURL == "" {
		writeRouteWithoutOwner(writer, request)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/optimization/negative-samples"
	if rawQuery := strings.TrimSpace(request.URL.RawQuery); rawQuery != "" {
		internalPath += "?" + rawQuery
	}
	respRaw, status, callErr := h.executeOwner(request, session, optimizationOwner, h.intelligence.optimizationBaseURL, route.siteID, optimizationReadAction, http.MethodGet, internalPath, nil)
	if callErr == nil && status == http.StatusBadRequest {
		writeProblem(writer, request, http.StatusBadRequest, "OPTIMIZATION_NEGATIVE_SAMPLES_QUERY_INVALID", "Negative samples query invalid", "The negative samples query parameters are invalid.", false, nil)
		return
	}
	h.writeIntelligenceResult(writer, request, respRaw, status, callErr)
}

func (h *handler) createOptimizationRun(writer http.ResponseWriter, request *http.Request, session bffSession) {
	if h.intelligence.optimizationBaseURL == "" {
		writeProblem(writer, request, http.StatusServiceUnavailable, "INTELLIGENCE_UNAVAILABLE", "Intelligence unavailable", "Optimization Service is not configured.", true, nil)
		return
	}
	raw, err := readBoundedBody(request.Body, maximumIntelligenceRequestBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "OPTIMIZATION_REQUEST_INVALID", "Optimization request invalid", "The Optimization request is too large or unreadable.", false, nil)
		return
	}
	var body struct {
		SiteID string `json:"siteId"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || ensureIntelligenceJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "OPTIMIZATION_REQUEST_INVALID", "Optimization request invalid", "The Optimization request body is invalid.", false, nil)
		return
	}
	siteID := body.SiteID
	if !isLowerUUIDv7(siteID) {
		writeProblem(writer, request, http.StatusUnprocessableEntity, "OPTIMIZATION_SCOPE_INVALID", "Optimization scope invalid", "A valid Site is required.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	upstreamBody, err := json.Marshal(body)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "OPTIMIZATION_REQUEST_INVALID", "Optimization request invalid", "The Optimization request could not be encoded.", false, nil)
		return
	}
	responseBody, status, callErr := h.executeOwner(request, session, optimizationOwner, h.intelligence.optimizationBaseURL, siteID, optimizationWriteAction, http.MethodPost, "/v1/optimize", upstreamBody)
	h.writeIntelligenceResult(writer, request, responseBody, status, callErr)
}

// ownerContext signs the context an intelligence owner serves: one action on one Tenant and Site,
// for the workload that presents it — the Gateway for the pages, the Operations Agent for FDD findings.
func (h *handler) ownerContext(session bffSession, owner intelligenceOwner, siteID, action, presenter string) (string, error) {
	if h.identity == nil || h.identity.config.DelegationSigner == nil {
		return "", errors.New("intelligence context signing is not configured")
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	return identitycontext.SignDelegation(h.identity.config.DelegationSigner, identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: presenter, Audience: owner.audience, TenantID: session.TenantID,
		Actions: []string{action}, Scopes: []string{"tenant:" + session.TenantID, "site:" + siteID},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	})
}

// executeOwner calls an intelligence owner for the pages over mutual TLS with a context naming the Gateway.
func (h *handler) executeOwner(publicRequest *http.Request, session bffSession, owner intelligenceOwner, baseURL, siteID, action, method, path string, body []byte) ([]byte, int, error) {
	if h.intelligence.ownerHTTPClient == nil {
		return nil, 0, errors.New("the intelligence owner client is not configured")
	}
	signed, err := h.ownerContext(session, owner, siteID, action, h.identity.config.ExecutingWorkloadSPIFFE)
	if err != nil {
		return nil, 0, err
	}
	ctx, cancel := contextWithTimeout(publicRequest.Context(), h.intelligence.timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	upstream, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		return nil, 0, err
	}
	upstream.Header.Set("Accept", "application/json")
	if body != nil {
		upstream.Header.Set("Content-Type", "application/json")
	}
	upstream.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), upstream.Header)
	upstream.Header.Set(owner.header, signed)
	response, err := h.intelligence.ownerHTTPClient.Do(upstream)
	if err != nil {
		return nil, 0, err
	}
	defer drainAndCloseBody(response.Body)
	raw, err := readBoundedBody(response.Body, maximumIntelligenceResponse)
	if err != nil {
		return nil, response.StatusCode, err
	}
	return raw, response.StatusCode, nil
}

func (h *handler) writeIntelligenceResult(writer http.ResponseWriter, request *http.Request, raw []byte, status int, err error) {
	if err != nil {
		writeProblem(writer, request, http.StatusBadGateway, "INTELLIGENCE_UPSTREAM_UNAVAILABLE", "Intelligence unavailable", "The Intelligence owner is temporarily unavailable.", true, nil)
		return
	}
	if status == http.StatusNotFound {
		// An owner answers a missing result with a JSON error body; a 404 without one comes from its
		// router, so the owner deployed here does not serve the route yet.
		if len(raw) == 0 || !json.Valid(raw) {
			writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The Intelligence owner does not serve this route.", false, nil)
			return
		}
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Intelligence result was not found.", false, nil)
		return
	}
	if status < 200 || status >= 300 {
		writeProblem(writer, request, http.StatusBadGateway, "INTELLIGENCE_UPSTREAM_REJECTED", "Intelligence request rejected", "The Intelligence owner rejected the request.", status >= 500, nil)
		return
	}
	if len(raw) == 0 || !json.Valid(raw) {
		writeProblem(writer, request, http.StatusBadGateway, "INTELLIGENCE_RESPONSE_INVALID", "Intelligence response invalid", "The Intelligence owner returned an invalid JSON response.", true, nil)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(raw)
}

func ensureIntelligenceJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("trailing JSON")
}

func contextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeout)
}

// writeRouteWithoutOwner answers a public read whose owner this Gateway has no address for. It
// is ROUTE_NOT_FOUND, as for a route with no applied owner, so a client never reads it as "this
// Site has no result yet" (RESOURCE_NOT_FOUND).
func writeRouteWithoutOwner(writer http.ResponseWriter, request *http.Request) {
	writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested public API route has no configured owner.", false, nil)
}

type armAutoControlLeasePublicRequest struct {
	ExpiresInHours      int      `json:"expiresInHours,omitempty"`
	ExpiresAt           string   `json:"expiresAt,omitempty"`
	AllowedCapabilities []string `json:"allowedCapabilities"`
	MaxSetpointStepC    *float64 `json:"maxSetpointStepC,omitempty"`
	DeadbandC           *float64 `json:"deadbandC,omitempty"`
	SupplyTempMinC      float64  `json:"supplyTempMinC"`
	SupplyTempMaxC      float64  `json:"supplyTempMaxC"`
	ZoneTempMaxC        *float64 `json:"zoneTempMaxC,omitempty"`
	MinRunningChillers  *int     `json:"minRunningChillers,omitempty"`
	MaxCommandsPerHour  *int     `json:"maxCommandsPerHour,omitempty"`
	ExecutionMode       string   `json:"executionMode,omitempty"`
	Reason              string   `json:"reason"`
	VerificationCode    string   `json:"verificationCode"`
	ExpectedRevision    *int64   `json:"expectedRevision,omitempty"`
}

type armAutoControlLeaseCoreRequest struct {
	ExpiresInHours      int                 `json:"expiresInHours,omitempty"`
	ExpiresAt           string              `json:"expiresAt,omitempty"`
	AllowedCapabilities []string            `json:"allowedCapabilities"`
	MaxSetpointStepC    *float64            `json:"maxSetpointStepC,omitempty"`
	DeadbandC           *float64            `json:"deadbandC,omitempty"`
	SupplyTempMinC      float64             `json:"supplyTempMinC"`
	SupplyTempMaxC      float64             `json:"supplyTempMaxC"`
	ZoneTempMaxC        *float64            `json:"zoneTempMaxC,omitempty"`
	MinRunningChillers  *int                `json:"minRunningChillers,omitempty"`
	MaxCommandsPerHour  *int                `json:"maxCommandsPerHour,omitempty"`
	ExecutionMode       string              `json:"executionMode,omitempty"`
	Reason              string              `json:"reason"`
	Meta                controlRegistryMeta `json:"meta"`
}

type revokeAutoControlLeasePublicRequest struct {
	Reason           string `json:"reason"`
	ExpectedRevision *int64 `json:"expectedRevision,omitempty"`
}

type optimizationAutoCommandRequest struct {
	TenantID             string                         `json:"tenantId"`
	DeviceID             string                         `json:"deviceId"`
	PointID              string                         `json:"pointId"`
	Capability           commandmodel.Capability        `json:"capability"`
	Parameters           commandmodel.CommandParameters `json:"parameters,omitempty"`
	VerificationPointKey string                         `json:"verificationPointKey"`
	IdempotencyKey       string                         `json:"idempotencyKey"`
	IsRollback           bool                           `json:"isRollback,omitempty"`
	PreconditionMode     commandmodel.PreconditionMode  `json:"preconditionMode,omitempty"`
	BypassReason         string                         `json:"bypassReason,omitempty"`
	PriorityTier         commandmodel.PriorityTier      `json:"priorityTier,omitempty"`
	CommandSource        commandmodel.CommandSource     `json:"commandSource,omitempty"`
	CurrentState         commandCurrentState            `json:"currentState"`
	CommandGrant         string                         `json:"commandGrant"`
}

func (h *handler) serveAutoControlLeaseRead(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	auth, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if failure != nil {
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
			return
		}
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	if _, allowed := auth.allowedSiteIDs[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	status, raw, callFailure := h.callCoreControlRegistry(request.Context(), http.MethodGet, "/internal/v1/registry/sites/"+url.PathEscape(route.siteID)+"/auto-control-lease", auth.coreGrant, nil, routeRevisionFromRequest(request))
	if callFailure != nil {
		h.writeCommandFailure(writer, request, *callFailure)
		return
	}
	if status != http.StatusOK {
		h.writeCoreControlRegistryError(writer, request, status, raw)
		return
	}
	h.writeIntelligenceResult(writer, request, raw, status, nil)
}

func (h *handler) serveOptimizationExecutionMonitorsRead(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	auth, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if failure != nil {
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
			return
		}
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	if _, allowed := auth.allowedSiteIDs[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	status, raw, callFailure := h.callCoreControlRegistry(request.Context(), http.MethodGet, "/internal/v1/registry/sites/"+url.PathEscape(route.siteID)+"/optimization-execution-monitors", auth.coreGrant, nil, routeRevisionFromRequest(request))
	if callFailure != nil {
		h.writeCommandFailure(writer, request, *callFailure)
		return
	}
	if status != http.StatusOK {
		h.writeCoreControlRegistryError(writer, request, status, raw)
		return
	}
	h.writeIntelligenceResult(writer, request, raw, status, nil)
}

func (h *handler) serveAutoControlLeaseArm(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	auth, ok := h.authorizeSiteControlExitWrite(writer, request, session, route.siteID, "Schedule manage authorization is required to arm automatic control leases.")
	if !ok {
		return
	}
	raw, err := readBoundedBody(request.Body, maximumIntelligenceRequestBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "AUTO_CONTROL_LEASE_REQUEST_INVALID", "Auto control lease request invalid", "The auto control lease request is too large or unreadable.", false, nil)
		return
	}
	var input armAutoControlLeasePublicRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureIntelligenceJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "AUTO_CONTROL_LEASE_REQUEST_INVALID", "Auto control lease request invalid", "The auto control lease request payload is invalid.", false, nil)
		return
	}
	if totpFailure := h.verifySubmitStepUpTOTP(request, session, input.VerificationCode); totpFailure != nil {
		h.writeCommandFailure(writer, request, *totpFailure)
		return
	}
	reason, meta, metaFailure := h.resolveControlExitMeta(request, input.Reason, input.ExpectedRevision, nil, func() (int64, *commandFailure) {
		readAuth, readFail := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
		if readFail != nil {
			f := commandFailure{status: readFail.status, code: readFail.code, title: readFail.title, detail: readFail.detail, retryable: readFail.retryable}
			return 0, &f
		}
		status, currentRaw, callFail := h.callCoreControlRegistry(request.Context(), http.MethodGet, "/internal/v1/registry/sites/"+url.PathEscape(route.siteID)+"/auto-control-lease", readAuth.coreGrant, nil, routeRevisionFromRequest(request))
		if callFail != nil {
			return 0, callFail
		}
		if status != http.StatusOK {
			return 0, nil
		}
		var state struct {
			ActiveLease *struct {
				Revision int64 `json:"revision"`
			} `json:"activeLease"`
		}
		if json.Unmarshal(currentRaw, &state) == nil && state.ActiveLease != nil {
			return state.ActiveLease.Revision, nil
		}
		return 0, nil
	})
	if metaFailure != nil {
		h.writeCommandFailure(writer, request, *metaFailure)
		return
	}
	payload := armAutoControlLeaseCoreRequest{
		ExpiresInHours:      input.ExpiresInHours,
		ExpiresAt:           input.ExpiresAt,
		AllowedCapabilities: input.AllowedCapabilities,
		MaxSetpointStepC:    input.MaxSetpointStepC,
		DeadbandC:           input.DeadbandC,
		SupplyTempMinC:      input.SupplyTempMinC,
		SupplyTempMaxC:      input.SupplyTempMaxC,
		ZoneTempMaxC:        input.ZoneTempMaxC,
		MinRunningChillers:  input.MinRunningChillers,
		MaxCommandsPerHour:  input.MaxCommandsPerHour,
		ExecutionMode:       input.ExecutionMode,
		Reason:              reason,
		Meta:                meta,
	}
	status, respRaw, callFailure := h.callCoreControlRegistry(request.Context(), http.MethodPut, "/internal/v1/registry/sites/"+url.PathEscape(route.siteID)+"/auto-control-lease", auth.coreGrant, payload, routeRevisionFromRequest(request))
	if callFailure != nil {
		h.writeCommandFailure(writer, request, *callFailure)
		return
	}
	if status != http.StatusOK && status != http.StatusCreated {
		h.writeCoreControlRegistryError(writer, request, status, respRaw)
		return
	}
	h.writeIntelligenceResult(writer, request, respRaw, http.StatusOK, nil)
}

func (h *handler) serveAutoControlLeaseRevoke(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	auth, ok := h.authorizeSiteControlExitWrite(writer, request, session, route.siteID, "Schedule manage authorization is required to revoke automatic control leases.")
	if !ok {
		return
	}
	raw, err := readBoundedBody(request.Body, maximumIntelligenceRequestBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "AUTO_CONTROL_LEASE_REQUEST_INVALID", "Auto control lease request invalid", "The auto control lease revoke request is too large or unreadable.", false, nil)
		return
	}
	var input revokeAutoControlLeasePublicRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureIntelligenceJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "AUTO_CONTROL_LEASE_REQUEST_INVALID", "Auto control lease request invalid", "The auto control lease revoke request payload is invalid.", false, nil)
		return
	}
	reason, meta, metaFailure := h.resolveControlExitMeta(request, input.Reason, input.ExpectedRevision, nil, func() (int64, *commandFailure) {
		return 0, nil
	})
	if metaFailure != nil {
		h.writeCommandFailure(writer, request, *metaFailure)
		return
	}
	payload := map[string]any{
		"reason": reason,
		"meta":   meta,
	}
	status, respRaw, callFailure := h.callCoreControlRegistry(request.Context(), http.MethodPost, "/internal/v1/registry/sites/"+url.PathEscape(route.siteID)+"/auto-control-lease/revoke", auth.coreGrant, payload, routeRevisionFromRequest(request))
	if callFailure != nil {
		h.writeCommandFailure(writer, request, *callFailure)
		return
	}
	if status != http.StatusOK {
		h.writeCoreControlRegistryError(writer, request, status, respRaw)
		return
	}
	h.writeIntelligenceResult(writer, request, respRaw, http.StatusOK, nil)
}

func (h *handler) serveOptimizationAutoCommand(writer http.ResponseWriter, request *http.Request, route publicIntelligenceRoute) {
	if hasSessionCredential(request) || strings.TrimSpace(request.Header.Get("X-Forwarded-For")) != "" || strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")) != "" {
		writeProblem(writer, request, http.StatusForbidden, "OPTIMIZATION_WORKLOAD_FORBIDDEN", "Optimization workload forbidden", "Automated optimization commands cannot be invoked through a user session or edge proxy.", false, nil)
		return
	}
	spiffeID, _, ok := verifiedTelemetryWorkloadIdentity(request)
	if !ok || spiffeID != defaultOptimizationWorkloadID {
		writeProblem(writer, request, http.StatusForbidden, "OPTIMIZATION_WORKLOAD_FORBIDDEN", "Optimization workload forbidden", "A verified optimization-service mutual TLS workload certificate is required.", false, nil)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	if h.command == nil || h.identity == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "Command service unavailable", "Command dispatch is not configured on this Gateway.", true, nil)
		return
	}
	raw, err := readBoundedBody(request.Body, maximumIntelligenceRequestBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command request invalid", "The automated Command request is too large or unreadable.", false, nil)
		return
	}
	var input optimizationAutoCommandRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureIntelligenceJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command request invalid", "The automated Command request body is invalid.", false, nil)
		return
	}
	profile, supported := commandmodel.CapabilityProfileFor(input.Capability)
	if !isLowerUUIDv7(input.TenantID) || !isLowerUUIDv7(input.DeviceID) || !isLowerUUIDv7(input.PointID) ||
		strings.TrimSpace(input.IdempotencyKey) == "" || strings.TrimSpace(input.VerificationPointKey) == "" ||
		!supported || !commandmodel.ParametersValid(input.Capability, input.Parameters) {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command request invalid", "The automated Command target or parameters are invalid.", false, nil)
		return
	}
	if input.IsRollback && !strings.HasPrefix(input.IdempotencyKey, "auto-rollback:") {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command request invalid", "Rollback automated Commands must use an auto-rollback idempotency key.", false, nil)
		return
	}
	precondMode := input.PreconditionMode
	if precondMode == "" {
		precondMode = commandmodel.PreconditionRequireCurrentState
	}
	var priorityTier commandmodel.PriorityTier
	var commandSource commandmodel.CommandSource
	if input.IsRollback {
		if (input.PriorityTier != "" && input.PriorityTier != commandmodel.PriorityProtectionRule) ||
			(input.CommandSource != "" && input.CommandSource != commandmodel.SourceProtectionRule) {
			writeProblem(writer, request, http.StatusForbidden, "COMMAND_PRIORITY_ESCALATION_FORBIDDEN", "Command priority forbidden", "Rollback automated commands must use PROTECTION_RULE priority and source.", false, nil)
			return
		}
		priorityTier = commandmodel.PriorityProtectionRule
		commandSource = commandmodel.SourceProtectionRule
	} else {
		if (input.PriorityTier != "" && input.PriorityTier != commandmodel.PriorityAIOptimization) ||
			(input.CommandSource != "" && input.CommandSource != commandmodel.SourceOptimization) {
			writeProblem(writer, request, http.StatusForbidden, "COMMAND_PRIORITY_ESCALATION_FORBIDDEN", "Command priority forbidden", "Automated optimization commands cannot escalate priority tier or source.", false, nil)
			return
		}
		priorityTier = commandmodel.PriorityAIOptimization
		commandSource = commandmodel.SourceOptimization
	}
	if !input.IsRollback {
		if precondMode == commandmodel.PreconditionEmergencyBypass {
			writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command request invalid", "Normal automated optimization commands cannot use emergency precondition bypass.", false, nil)
			return
		}
		if input.CurrentState.EvaluationAvailability != "AVAILABLE" || input.CurrentState.Presence != "ONLINE" ||
			input.CurrentState.Readiness != "CURRENT" || input.CurrentState.Quality != "GOOD" {
			writeProblem(writer, request, http.StatusConflict, "COMMAND_CURRENT_STATE_UNSAFE", "Current state unsafe", "Live telemetry current-state preconditions are incomplete or unhealthy.", false, nil)
			return
		}
	} else if precondMode == commandmodel.PreconditionEmergencyBypass {
		if strings.TrimSpace(input.BypassReason) == "" {
			writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command request invalid", "Emergency precondition bypass rollback requires a non-empty bypassReason.", false, nil)
			return
		}
	}
	if precondMode != commandmodel.PreconditionEmergencyBypass {
		if input.CurrentState.BusinessRevision == 0 || input.CurrentState.ObservedAt.IsZero() ||
			(profile.ParameterKey != "" && input.CurrentState.CurrentValue == nil) {
			writeProblem(writer, request, http.StatusConflict, "COMMAND_CURRENT_STATE_UNSAFE", "Current state unsafe", "Live telemetry current-state evidence is incomplete.", false, nil)
			return
		}
	}
	claims, ok := parseOptimizationWorkloadCommandGrant(
		input.CommandGrant, h.command.iamGrantIssuer, h.identity.config.ExecutingWorkloadSPIFFE,
		h.command.backendAudience, defaultOptimizationWorkloadID,
		input.TenantID, route.siteID, input.DeviceID, input.Capability, profile.Revision, h.now().UTC(),
	)
	if !ok {
		writeProblem(writer, request, http.StatusForbidden, "COMMAND_AUTHORIZATION_DENIED", "Command authorization denied", "A valid IAM-signed optimization workload Command grant is required.", false, nil)
		return
	}
	prepared := preparedCommand{
		tenantID:             input.TenantID,
		siteID:               route.siteID,
		deviceID:             input.DeviceID,
		pointID:              input.PointID,
		principalID:          claims.PrincipalID,
		idempotencyKey:       input.IdempotencyKey,
		capability:           input.Capability,
		parameters:           input.Parameters,
		verificationPointKey: input.VerificationPointKey,
		priorityTier:         priorityTier,
		commandSource:        commandSource,
		preconditionMode:     precondMode,
		bypassReason:         strings.TrimSpace(input.BypassReason),
		currentState:         input.CurrentState,
		grant:                input.CommandGrant,
	}
	ctx := request.Context()
	if observability.Traceparent(ctx) == "" {
		if rawTP := strings.TrimSpace(request.Header.Get("traceparent")); rawTP != "" {
			var tracer *observability.Tracer
			if h.observability != nil {
				tracer = h.observability.Tracer
			}
			if tracer == nil {
				tracer = observability.NewTracer(serviceName, nil)
			}
			ctx = observability.ContextWithRemoteParent(ctx, tracer, rawTP, request.Header.Get("tracestate"))
		}
	}
	view, status, location, cmdFailure := h.executeCommandCreate(ctx, request, prepared)
	if cmdFailure != nil {
		h.writeCommandFailure(writer, request, *cmdFailure)
		return
	}
	if location != "" {
		writer.Header().Set("Location", location)
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, status, view)
}

func parseOptimizationWorkloadCommandGrant(
	grant string,
	issuer string,
	presenter string,
	audience string,
	workloadSPIFFE string,
	tenantID string,
	siteID string,
	deviceID string,
	capability commandmodel.Capability,
	capabilityRevision string,
	now time.Time,
) (commandauth.GrantClaims, bool) {
	if grant == "" || len(grant) > commandauth.MaximumEncodedGrantSize {
		return commandauth.GrantClaims{}, false
	}
	parts := strings.Split(grant, ".")
	if len(parts) != 2 {
		return commandauth.GrantClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return commandauth.GrantClaims{}, false
	}
	if sig, err := base64.RawURLEncoding.DecodeString(parts[1]); err != nil || len(sig) == 0 {
		return commandauth.GrantClaims{}, false
	}
	var claims commandauth.GrantClaims
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&claims) != nil || ensureIntelligenceJSONEOF(decoder) != nil {
		return commandauth.GrantClaims{}, false
	}
	valid := claims.Version == commandauth.GrantVersion &&
		claims.Issuer == issuer &&
		claims.Presenter == presenter &&
		claims.Audience == audience &&
		claims.WorkloadSPIFFE == workloadSPIFFE &&
		claims.LeaseID != "" &&
		claims.ArmedBy != "" &&
		claims.GrantID != "" &&
		claims.TokenID != "" &&
		claims.PrincipalID != "" &&
		claims.Purpose == commandmodel.AuthorizationCommandSubmit &&
		claims.TenantID == tenantID &&
		claims.SiteID == siteID &&
		claims.DeviceID == deviceID &&
		claims.Capability == capability &&
		claims.CapabilityRevision == capabilityRevision &&
		!claims.Transitive &&
		commandauth.RiskOrdinal(commandmodel.RiskLow) <= commandauth.RiskOrdinal(claims.MaximumRisk) &&
		claims.IssuedAt <= now.Add(5*time.Second).Unix() &&
		claims.ExpiresAt > now.Unix() &&
		claims.ExpiresAt > claims.IssuedAt &&
		time.Duration(claims.ExpiresAt-claims.IssuedAt)*time.Second <= commandauth.MaximumGrantLifetime
	if !valid {
		return commandauth.GrantClaims{}, false
	}
	return claims, true
}
