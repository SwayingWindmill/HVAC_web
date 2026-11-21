package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

type publicAccountingConfigRoute struct {
	template       string
	siteID         string
	holidayDate    string
	versionID      string
	internalPath   string
	write          bool
	allowEmptyBody bool
	auditAction    string
	targetID       string
	targetLabel    string
}

type publicCounterAuditRoute struct {
	template string
	siteID   string
}

var accountingConfigBackendMessages = siteRegistryBackendMessages{
	badRequestTitle:         "Accounting config request invalid",
	defaultBadRequestDetail: "The accounting config request is invalid.",
	forbiddenTitle:          "Accounting config action forbidden",
	defaultForbiddenCode:    "BILLING_MANAGE_FORBIDDEN",
	defaultForbiddenDetail:  "The authenticated Principal is not allowed to manage accounting configuration on this Site.",
	notFoundDetail:          "The requested Site or accounting config resource was not found.",
	conflictTitle:           "Accounting config conflict",
	defaultConflictDetail:   "The accounting config mutation conflicts with the current authoritative state.",
	gatewayTimeoutDetail:    "The Core Registry did not respond to the accounting config request before the gateway deadline.",
	upstreamTimeoutDetail:   "The accounting config request timed out.",
}

func matchPublicAccountingConfigRoute(method, path string) (publicAccountingConfigRoute, bool) {
	if !strings.HasPrefix(path, "/api/v1/sites/") {
		return publicAccountingConfigRoute{}, false
	}
	trimmed := strings.TrimPrefix(path, "/api/v1/")
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" {
			return publicAccountingConfigRoute{}, false
		}
	}
	if len(segments) < 4 || segments[0] != "sites" || segments[2] != "accounting" {
		return publicAccountingConfigRoute{}, false
	}
	siteID := segments[1]

	switch {
	case method == http.MethodGet && len(segments) == 4 && segments[3] == "holidays":
		return publicAccountingConfigRoute{
			template:     "/api/v1/sites/{siteId}/accounting/holidays",
			siteID:       siteID,
			internalPath: "/internal/v1/registry/sites/" + siteID + "/accounting/holidays",
		}, true
	case method == http.MethodPut && len(segments) == 5 && segments[3] == "holidays":
		date := segments[4]
		return publicAccountingConfigRoute{
			template:       "/api/v1/sites/{siteId}/accounting/holidays/{date}",
			siteID:         siteID,
			holidayDate:    date,
			internalPath:   "/internal/v1/registry/sites/" + siteID + "/accounting/holidays/" + date,
			write:          true,
			allowEmptyBody: false,
			auditAction:    "SITE_HOLIDAY_UPSERTED",
			targetID:       "site:" + siteID + ":holiday:" + date,
			targetLabel:    "站点节假日配置",
		}, true
	case method == http.MethodDelete && len(segments) == 5 && segments[3] == "holidays":
		date := segments[4]
		return publicAccountingConfigRoute{
			template:       "/api/v1/sites/{siteId}/accounting/holidays/{date}",
			siteID:         siteID,
			holidayDate:    date,
			internalPath:   "/internal/v1/registry/sites/" + siteID + "/accounting/holidays/" + date,
			write:          true,
			allowEmptyBody: true,
			auditAction:    "SITE_HOLIDAY_DELETED",
			targetID:       "site:" + siteID + ":holiday:" + date,
			targetLabel:    "站点节假日配置",
		}, true
	case method == http.MethodGet && len(segments) == 4 && segments[3] == "meter-ratios":
		return publicAccountingConfigRoute{
			template:     "/api/v1/sites/{siteId}/accounting/meter-ratios",
			siteID:       siteID,
			internalPath: "/internal/v1/registry/sites/" + siteID + "/accounting/meter-ratios",
		}, true
	case method == http.MethodPost && len(segments) == 4 && segments[3] == "meter-ratios":
		return publicAccountingConfigRoute{
			template:       "/api/v1/sites/{siteId}/accounting/meter-ratios",
			siteID:         siteID,
			internalPath:   "/internal/v1/registry/sites/" + siteID + "/accounting/meter-ratios",
			write:          true,
			allowEmptyBody: false,
			auditAction:    "METER_RATIO_VERSION_CREATED",
			targetID:       "site:" + siteID + ":meter-ratios",
			targetLabel:    "表计倍率版本",
		}, true
	case method == http.MethodPost && len(segments) == 6 && segments[3] == "meter-ratios" && segments[5] == "retire":
		versionID := segments[4]
		return publicAccountingConfigRoute{
			template:       "/api/v1/sites/{siteId}/accounting/meter-ratios/{versionId}/retire",
			siteID:         siteID,
			versionID:      versionID,
			internalPath:   "/internal/v1/registry/sites/" + siteID + "/accounting/meter-ratios/" + versionID + "/retire",
			write:          true,
			allowEmptyBody: true,
			auditAction:    "METER_RATIO_VERSION_RETIRED",
			targetID:       "site:" + siteID + ":meter-ratio:" + versionID,
			targetLabel:    "表计倍率版本",
		}, true
	default:
		return publicAccountingConfigRoute{}, false
	}
}

func matchPublicCounterAuditRoute(method, path string) (publicCounterAuditRoute, bool) {
	if method != http.MethodGet || !strings.HasPrefix(path, "/api/v1/sites/") {
		return publicCounterAuditRoute{}, false
	}
	trimmed := strings.TrimPrefix(path, "/api/v1/")
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" {
			return publicCounterAuditRoute{}, false
		}
	}
	if len(segments) != 4 || segments[0] != "sites" || segments[2] != "accounting" || segments[3] != "counter-audit" {
		return publicCounterAuditRoute{}, false
	}
	return publicCounterAuditRoute{
		template: "/api/v1/sites/{siteId}/accounting/counter-audit",
		siteID:   segments[1],
	}, true
}

func isValidAccountingCalendarDate(value string) bool {
	if len(value) != 10 {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return false
	}
	return parsed.Format("2006-01-02") == value
}

func dispatchAccountingConfigRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAccountingConfigRoute) {
	decision := routeDecisionFromContext(request.Context())
	if decision.RegistryRevision == 0 || decision.SelectedOwner != ownershipregistry.OwnerCore || h.registry == nil || h.registry.coreBaseURL == "" || h.registry.coreHTTPClient == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The accounting config route is not active for this Session.", true, nil)
		return
	}
	if !isLowerUUIDv7(route.siteID) || (route.versionID != "" && !isLowerUUIDv7(route.versionID)) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site or accounting config resource was not found.", false, nil)
		return
	}
	if route.holidayDate != "" && !isValidAccountingCalendarDate(route.holidayDate) {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Accounting config request invalid", "The holiday date path parameter must be a valid YYYY-MM-DD calendar date.", false, nil)
		return
	}
	if route.write && request.URL.RawQuery != "" {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Accounting config request invalid", "Accounting config mutation routes do not accept query parameters.", false, nil)
		return
	}

	session, ok := h.intelligenceSession(writer, request, route.write)
	if !ok {
		return
	}
	readAuth, readFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if readFailure != nil {
		if readFailure.status == http.StatusForbidden || readFailure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
			return
		}
		writeProblem(writer, request, readFailure.status, readFailure.code, readFailure.title, readFailure.detail, readFailure.retryable, nil)
		return
	}
	if _, visible := readAuth.allowedSiteIDs[route.siteID]; !visible {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}

	grant := readAuth.coreGrant
	var body []byte
	if route.write {
		raw, err := readBoundedBody(request.Body, defaultRegistryBodyLimit)
		if err != nil {
			code, status := "REGISTRY_MUTATION_INVALID", http.StatusBadRequest
			if errors.Is(err, errRegistryBodyTooLarge) {
				code, status = "REGISTRY_MUTATION_TOO_LARGE", http.StatusRequestEntityTooLarge
			}
			writeProblem(writer, request, status, code, "Accounting config request invalid", "The accounting config mutation payload is unreadable or exceeds the supported size.", false, nil)
			return
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			if !route.allowEmptyBody {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Accounting config request invalid", "The accounting config mutation payload is required.", false, nil)
				return
			}
		} else if !json.Valid(trimmed) {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Accounting config request invalid", "The accounting config mutation payload must be valid JSON.", false, nil)
			return
		} else {
			body = trimmed
		}

		writeGrant, ok := h.authorizeSiteRegistryWriteAction(
			writer,
			request,
			session,
			route.siteID,
			registryauth.ActionBillingManage,
			"BILLING_MANAGE_FORBIDDEN",
			"Accounting config action forbidden",
			"The authenticated Principal is not allowed to manage accounting configuration on this Site.",
		)
		if !ok {
			if route.auditAction != "" {
				h.emitAccountingConfigAudit(request, session, route, body, auditOutcomeDenied, "BILLING_MANAGE_FORBIDDEN")
			}
			return
		}
		grant = writeGrant
	}

	status, reasonCode := h.forwardSiteRegistryBackendRequestWithAudit(
		writer,
		request,
		session,
		decision.RegistryRevision,
		route.internalPath,
		route.write,
		grant,
		body,
		accountingConfigBackendMessages,
	)
	if route.write && route.auditAction != "" {
		h.emitAccountingConfigAudit(request, session, route, body, auditOutcomeForStatus(status), reasonCode)
	}
}

func (h *handler) emitAccountingConfigAudit(
	request *http.Request,
	session bffSession,
	route publicAccountingConfigRoute,
	body []byte,
	outcome, reasonCode string,
) {
	summary := decodeFlatJSONObject(body)
	if summary == nil && route.holidayDate != "" {
		summary = decodeFlatJSONObject([]byte(fmt.Sprintf(`{"holidayDate":%q}`, route.holidayDate)))
	}
	if summary == nil && route.versionID != "" {
		summary = decodeFlatJSONObject([]byte(fmt.Sprintf(`{"versionId":%q}`, route.versionID)))
	}
	if reasonCode != "" && outcome != auditOutcomeSucceeded {
		if summary == nil {
			summary = decodeFlatJSONObject([]byte("{}"))
		}
		summary["reasonCode"] = reasonCode
	}
	h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
		Category:      auditserver.AggregateTypeRegistryMutation,
		TargetID:      route.targetID,
		TargetLabel:   route.targetLabel,
		Action:        route.auditAction,
		Outcome:       outcome,
		ChangeSummary: summary,
	})
}

func dispatchCounterAuditRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicCounterAuditRoute) {
	decision := routeDecisionFromContext(request.Context())
	if (decision.RegistryRevision != 0 && decision.SelectedOwner != ownershipregistry.OwnerAnalyticsQuery) ||
		h.analytics == nil || h.analytics.queryBaseURL == "" || h.analytics.queryHTTPClient == nil {
		h.writeTelemetryFailure(writer, request, telemetryFailure{
			status:    http.StatusServiceUnavailable,
			code:      "TELEMETRY_COUNTER_AUDIT_UNAVAILABLE",
			title:     "Counter audit unavailable",
			detail:    "Telemetry Query Service is not configured for counter audit queries.",
			retryable: true,
		})
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
	if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionExpensiveQuery, session.TenantID) {
		return
	}
	canonical, scope, parseFailure := parseCounterAuditQueryParams(request.URL.RawQuery, session.TenantID, route.siteID)
	if parseFailure != nil {
		h.writeTelemetryFailure(writer, request, *parseFailure)
		return
	}
	presenterSPIFFE := ""
	if h.identity != nil {
		presenterSPIFFE = h.identity.config.ExecutingWorkloadSPIFFE
	}
	grant, authFailure := h.authorizeAnalyticsForPresenter(
		request.Context(),
		request,
		session,
		analyticsScope{TenantID: session.TenantID, SiteID: route.siteID, Digest: scope},
		presenterSPIFFE,
	)
	if authFailure != nil {
		h.writeAnalyticsFailure(writer, request, *authFailure)
		return
	}
	raw, execFailure := h.executeHistoryQuery(request.Context(), request, canonical, grant, "/internal/v1/telemetry/counter-audit")
	if execFailure != nil {
		h.writeTelemetryFailure(writer, request, *execFailure)
		return
	}
	var response telemetryhistorymodel.CounterAuditResponse
	if decodeStrictTelemetryJSON(raw, &response) != nil || response.ValidateFor(canonical) != nil {
		h.writeTelemetryFailure(writer, request, telemetryFailure{
			status:    http.StatusBadGateway,
			code:      "TELEMETRY_COUNTER_AUDIT_RESPONSE_INVALID",
			title:     "Counter audit response invalid",
			detail:    "Telemetry Query Service returned a counter audit response outside the product contract.",
			retryable: true,
		})
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, response)
}

func parseCounterAuditQueryParams(rawQuery, tenantID, siteID string) (telemetryhistorymodel.CounterAuditQuery, string, *telemetryFailure) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		failure := counterAuditQueryInvalid("The counter audit query string is malformed.")
		return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
	}
	for key, list := range values {
		switch key {
		case "from", "to", "deviceId", "pointId", "anomaliesOnly", "limit":
			if len(list) != 1 {
				failure := counterAuditQueryInvalid(fmt.Sprintf("Counter audit query parameter %q must appear once.", key))
				return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
			}
		default:
			failure := counterAuditQueryInvalid(fmt.Sprintf("Unknown counter audit query parameter %q.", key))
			return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
		}
	}

	fromRaw := strings.TrimSpace(values.Get("from"))
	toRaw := strings.TrimSpace(values.Get("to"))
	if fromRaw == "" || toRaw == "" {
		failure := counterAuditQueryInvalid("Counter audit from and to timestamps are required.")
		return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
	}
	fromTS, err := time.Parse(time.RFC3339Nano, fromRaw)
	if err != nil {
		failure := counterAuditQueryInvalid("Counter audit from timestamp must use RFC3339 format.")
		return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
	}
	toTS, err := time.Parse(time.RFC3339Nano, toRaw)
	if err != nil {
		failure := counterAuditQueryInvalid("Counter audit to timestamp must use RFC3339 format.")
		return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
	}
	_, fromOff := fromTS.Zone()
	_, toOff := toTS.Zone()
	if fromOff != 0 || toOff != 0 {
		failure := counterAuditQueryInvalid("Counter audit timestamps must use UTC.")
		return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
	}

	query := telemetryhistorymodel.CounterAuditQuery{
		TenantID: tenantID,
		SiteID:   siteID,
		From:     fromTS.UTC(),
		To:       toTS.UTC(),
	}
	if values.Has("deviceId") {
		deviceID := strings.TrimSpace(values.Get("deviceId"))
		query.DeviceID = &deviceID
	}
	if values.Has("pointId") {
		pointID := strings.TrimSpace(values.Get("pointId"))
		query.PointID = &pointID
	}
	if values.Has("anomaliesOnly") {
		rawFlag := strings.TrimSpace(values.Get("anomaliesOnly"))
		switch rawFlag {
		case "true":
			flag := true
			query.AnomaliesOnly = &flag
		case "false":
			flag := false
			query.AnomaliesOnly = &flag
		default:
			failure := counterAuditQueryInvalid("Counter audit anomaliesOnly must be true or false.")
			return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
		}
	}
	if values.Has("limit") {
		limitVal, err := strconv.Atoi(strings.TrimSpace(values.Get("limit")))
		if err != nil || limitVal < 1 || limitVal > telemetryhistorymodel.MaximumCounterAuditLimit {
			failure := counterAuditQueryInvalid("Counter audit limit must be an integer between 1 and 500.")
			return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
		}
		query.Limit = limitVal
	}

	canonical, err := query.Canonical()
	if err != nil {
		failure := counterAuditQueryInvalid(err.Error())
		return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
	}
	scope, err := canonical.ScopeDigest()
	if err != nil {
		failure := counterAuditQueryInvalid("Counter audit scope could not be normalized.")
		return telemetryhistorymodel.CounterAuditQuery{}, "", &failure
	}
	return canonical, scope, nil
}

func counterAuditQueryInvalid(detail string) telemetryFailure {
	return telemetryFailure{
		status:    http.StatusBadRequest,
		code:      "COUNTER_AUDIT_QUERY_INVALID",
		title:     "Counter audit query invalid",
		detail:    detail,
		retryable: false,
	}
}
