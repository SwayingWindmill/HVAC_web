package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"


)

const (
	publicOptimizationRunsPath      = "/api/v1/optimization/runs"
	defaultIntelligenceTimeout      = 8 * time.Second
	maximumIntelligenceRequestBytes = int64(256 << 10)
	maximumIntelligenceResponse     = int64(8 << 20)
	fddSupplyTemperatureKey         = "btu_meter.supply_water_temperature"
	fddReturnTemperatureKey         = "btu_meter.return_water_temperature"
)

type IntelligenceConfig struct {
	ForecastBaseURL     string
	FDDBaseURL          string
	FDDWorkloadSPIFFE   string
	OptimizationBaseURL string
	HTTPClient          *http.Client
	Timeout             time.Duration
}

type intelligenceController struct {
	forecastBaseURL     string
	fddBaseURL          string
	fddWorkloadSPIFFE   string
	optimizationBaseURL string
	httpClient          *http.Client
	timeout             time.Duration
}

type publicIntelligenceRoute struct {
	owner      string
	siteID     string
	findingID  string
	runID      string
	target     string
	method     string
	publicPath string
}

type fddLinkRequest struct {
	AlarmID     string `json:"alarmId"`
	WorkOrderID string `json:"workOrderId"`
}

type fddLowDeltaTEvaluationRequest struct {
	AssetID                   string    `json:"assetId"`
	DeviceID                  string    `json:"deviceId"`
	EvaluationFrom            time.Time `json:"evaluationFrom"`
	EvaluationTo              time.Time `json:"evaluationTo"`
	RuleRevisionID            string    `json:"ruleRevisionId"`
	ModelDeploymentRevisionID string    `json:"modelDeploymentRevisionId,omitempty"`
	MinimumDeltaTC            float64   `json:"minimumDeltaTC"`
}

type fddLowDeltaTEvaluationUpstream struct {
	TenantID                  string    `json:"tenantId"`
	SiteID                    string    `json:"siteId"`
	AssetID                   string    `json:"assetId"`
	DeviceID                  string    `json:"deviceId"`
	EvaluationFrom            time.Time `json:"evaluationFrom"`
	EvaluationTo              time.Time `json:"evaluationTo"`
	RuleRevisionID            string    `json:"ruleRevisionId"`
	ModelDeploymentRevisionID string    `json:"modelDeploymentRevisionId,omitempty"`
	MinimumDeltaTC            float64   `json:"minimumDeltaTC"`
}

func newIntelligenceController(config *IntelligenceConfig) *intelligenceController {
	if config == nil {
		return nil
	}
	resolved := *config
	resolved.ForecastBaseURL = strings.TrimRight(strings.TrimSpace(resolved.ForecastBaseURL), "/")
	resolved.FDDBaseURL = strings.TrimRight(strings.TrimSpace(resolved.FDDBaseURL), "/")
	resolved.FDDWorkloadSPIFFE = strings.TrimSpace(resolved.FDDWorkloadSPIFFE)
	resolved.OptimizationBaseURL = strings.TrimRight(strings.TrimSpace(resolved.OptimizationBaseURL), "/")
	if resolved.HTTPClient == nil {
		resolved.HTTPClient = &http.Client{}
	}
	if resolved.Timeout <= 0 || resolved.Timeout > 30*time.Second {
		resolved.Timeout = defaultIntelligenceTimeout
	}
	return &intelligenceController{
		forecastBaseURL: resolved.ForecastBaseURL, fddBaseURL: resolved.FDDBaseURL, fddWorkloadSPIFFE: resolved.FDDWorkloadSPIFFE, optimizationBaseURL: resolved.OptimizationBaseURL,
		httpClient: resolved.HTTPClient, timeout: resolved.Timeout,
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
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/fdd/evaluate/low-delta-t", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerFDD, siteID: siteID, target: "fdd-evaluate", method: http.MethodPost, publicPath: "/api/v1/sites/{siteId}/fdd/evaluate/low-delta-t"}, method == http.MethodPost
	}
	if siteID, findingID, ok := matchFDDLinkPath(path); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerFDD, siteID: siteID, findingID: findingID, target: "fdd-link", method: http.MethodPatch, publicPath: "/api/v1/sites/{siteId}/fdd/findings/{findingId}/links"}, method == http.MethodPatch
	}
	if siteID, ok := matchSinglePathParameter(path, "/api/v1/sites/{siteId}/optimization/recommendations/latest", "{siteId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, siteID: siteID, target: "optimization-latest", method: http.MethodGet, publicPath: "/api/v1/sites/{siteId}/optimization/recommendations/latest"}, method == http.MethodGet
	}
	if path == publicOptimizationRunsPath {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, method: http.MethodPost, publicPath: publicOptimizationRunsPath}, method == http.MethodPost
	}
	if runID, ok := matchSinglePathParameter(path, "/api/v1/optimization/runs/{runId}", "{runId}"); ok {
		return publicIntelligenceRoute{owner: ownershipregistry.OwnerOptimization, runID: runID, method: http.MethodGet, publicPath: "/api/v1/optimization/runs/{runId}"}, method == http.MethodGet
	}
	return publicIntelligenceRoute{}, false
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

func dispatchIntelligenceRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicIntelligenceRoute) {
	decision := routeDecisionFromContext(request.Context())
	if decision.SelectedOwner != route.owner || h.intelligence == nil {
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
		h.serveFDDFindings(writer, request, session, route)
		return
	}
	if route.target == "optimization-latest" {
		h.serveLatestOptimizationRecommendation(writer, request, session, route)
		return
	}
	if route.method == http.MethodPost {
		h.createOptimizationRun(writer, request, session)
		return
	}
	h.getOptimizationRun(writer, request, session, route.runID)
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
	if !requireCSRF {
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
	if !isLowerUUIDv7(route.siteID) || h.intelligence.forecastBaseURL == "" {
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
	raw, status, err := h.executeIntelligence(request, http.MethodGet, h.intelligence.forecastBaseURL+internalPath, nil, map[string]string{"X-Tenant-ID": session.TenantID})
	h.writeIntelligenceResult(writer, request, raw, status, err)
}

func (h *handler) serveFDDFindings(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if !isLowerUUIDv7(route.siteID) || h.intelligence.fddBaseURL == "" {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested FDD findings were not found.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested FDD findings were not found.", false, nil)
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/fdd/findings"
	if rawQuery := strings.TrimSpace(request.URL.RawQuery); rawQuery != "" {
		internalPath += "?" + rawQuery
	}
	raw, status, err := h.executeIntelligence(request, http.MethodGet, h.intelligence.fddBaseURL+internalPath, nil, map[string]string{"X-Tenant-ID": session.TenantID})
	h.writeIntelligenceResult(writer, request, raw, status, err)
}

func (h *handler) serveFDDLink(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if !isLowerUUIDv7(route.siteID) || !isLowerUUIDv7(route.findingID) || h.intelligence.fddBaseURL == "" {
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
	responseBody, status, callErr := h.executeIntelligence(request, http.MethodPatch, h.intelligence.fddBaseURL+internalPath, raw, map[string]string{"X-Tenant-ID": session.TenantID})
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
	readContext, failure := h.signWorkOrderReadContext(session, route, decision, site.TenantID)
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
	if !isLowerUUIDv7(route.siteID) || h.intelligence.fddBaseURL == "" || h.intelligence.fddWorkloadSPIFFE == "" {
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
	selection := telemetryhistorymodel.DeviceHistoryRequest{
		DeviceID: input.DeviceID,
		Keys:     []string{fddSupplyTemperatureKey, fddReturnTemperatureKey},
		From:     input.EvaluationFrom,
		To:       input.EvaluationTo,
		PageSize: telemetryhistorymodel.MaximumHistoryPageSize,
	}
	if err := selection.Validate(); err != nil {
		writeProblem(writer, request, http.StatusUnprocessableEntity, "FDD_EVALUATION_SCOPE_INVALID", "FDD evaluation scope invalid", "The FDD evaluation history selection is invalid.", false, nil)
		return
	}
	caller := telemetryCaller{
		principal: session.Principal,
		tenantID:  session.TenantID,
		contextID: session.ID,
		expiresAt: session.ExpiresAt,
	}
	authorization, failure := h.authorizeTelemetry(request.Context(), request, caller, telemetryauth.ActionHistoryRead, []telemetryauth.Target{{DeviceID: selection.DeviceID, Keys: selection.Keys}})
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	if len(authorization.targets) != 1 {
		h.writeTelemetryFailure(writer, request, historyUnavailable("IAM returned an incomplete Device History resource scope for FDD evaluation."))
		return
	}
	authorizedTarget := authorization.targets[0]
	canonical, err := selection.Complete(authorizedTarget.TenantID, authorizedTarget.SiteID)
	if err != nil || canonical.TenantID != session.TenantID || canonical.SiteID != route.siteID {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested FDD evaluation scope was not found.", false, nil)
		return
	}
	grant, failure := h.signHistoryQueryGrant(caller, authorization, canonical.TenantID, telemetryhistorymodel.DeviceHistoryAction, canonical.CursorScopeDigest, h.intelligence.fddWorkloadSPIFFE)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	upstreamBody, err := json.Marshal(fddLowDeltaTEvaluationUpstream{
		TenantID:                  session.TenantID,
		SiteID:                    route.siteID,
		AssetID:                   input.AssetID,
		DeviceID:                  input.DeviceID,
		EvaluationFrom:            input.EvaluationFrom,
		EvaluationTo:              input.EvaluationTo,
		RuleRevisionID:            input.RuleRevisionID,
		ModelDeploymentRevisionID: input.ModelDeploymentRevisionID,
		MinimumDeltaTC:            input.MinimumDeltaTC,
	})
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "FDD_EVALUATION_REQUEST_INVALID", "FDD evaluation request invalid", "The FDD evaluation request could not be encoded.", false, nil)
		return
	}
	responseBody, status, callErr := h.executeIntelligence(request, http.MethodPost, h.intelligence.fddBaseURL+"/v1/fdd/evaluate/low-delta-t", upstreamBody, map[string]string{
		"X-Tenant-ID":        session.TenantID,
		"X-Delegation-Grant": grant,
	})
	h.writeIntelligenceResult(writer, request, responseBody, status, callErr)
}

func (h *handler) serveLatestOptimizationRecommendation(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if !isLowerUUIDv7(route.siteID) || h.intelligence.optimizationBaseURL == "" {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Optimization recommendation was not found.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	if _, allowed := allowedSites[route.siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Optimization recommendation was not found.", false, nil)
		return
	}
	internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/optimization/recommendations/latest"
	raw, status, err := h.executeIntelligence(request, http.MethodGet, h.intelligence.optimizationBaseURL+internalPath, nil, map[string]string{"X-Tenant-ID": session.TenantID})
	h.writeIntelligenceResult(writer, request, raw, status, err)
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
	responseBody, status, callErr := h.executeIntelligence(request, http.MethodPost, h.intelligence.optimizationBaseURL+"/v1/optimize", upstreamBody, map[string]string{"X-Tenant-ID": session.TenantID})
	h.writeIntelligenceResult(writer, request, responseBody, status, callErr)
}

func (h *handler) getOptimizationRun(writer http.ResponseWriter, request *http.Request, session bffSession, runID string) {
	if !isLowerUUIDv7(runID) || h.intelligence.optimizationBaseURL == "" {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Optimization run was not found.", false, nil)
		return
	}
	allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
	if !ok {
		return
	}
	siteIDs := make([]string, 0, len(allowedSites))
	for siteID := range allowedSites {
		siteIDs = append(siteIDs, siteID)
	}
	sort.Strings(siteIDs)
	if len(siteIDs) == 0 {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Optimization run was not found.", false, nil)
		return
	}
	internalPath := "/v1/optimization/runs/" + url.PathEscape(runID)
	raw, status, err := h.executeIntelligence(request, http.MethodGet, h.intelligence.optimizationBaseURL+internalPath, nil, map[string]string{
		"X-Tenant-ID": session.TenantID, "X-Authorized-Site-IDs": strings.Join(siteIDs, ","),
	})
	h.writeIntelligenceResult(writer, request, raw, status, err)
}

func (h *handler) executeIntelligence(publicRequest *http.Request, method, endpoint string, body []byte, headers map[string]string) ([]byte, int, error) {
	ctx, cancel := contextWithTimeout(publicRequest.Context(), h.intelligence.timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	upstream, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, 0, err
	}
	upstream.Header.Set("Accept", "application/json")
	if body != nil {
		upstream.Header.Set("Content-Type", "application/json")
	}
	upstream.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), upstream.Header)
	for name, value := range headers {
		upstream.Header.Set(name, value)
	}
	response, err := h.intelligence.httpClient.Do(upstream)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
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
