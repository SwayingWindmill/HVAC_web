package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

type publicAccountingRecalcRoute struct {
	template     string
	siteID       string
	kind         string
	internalPath string
	write        bool
}

var accountingRecalcBackendMessages = siteRegistryBackendMessages{
	badRequestTitle:         "Accounting recalculation request invalid",
	defaultBadRequestDetail: "The accounting recalculation request is invalid.",
	forbiddenTitle:          "Accounting recalculation action forbidden",
	defaultForbiddenCode:    "ACCOUNTING_RECALC_FORBIDDEN",
	defaultForbiddenDetail:  "The authenticated Principal is not allowed to trigger accounting recalculation on this Site.",
	notFoundDetail:          "The requested Site was not found.",
	conflictTitle:           "Accounting recalculation conflict",
	defaultConflictDetail:   "The accounting recalculation request conflicts with the current authoritative state.",
	gatewayTimeoutDetail:    "The accounting recalculation request did not complete before the Gateway deadline.",
	upstreamTimeoutDetail:   "The accounting recalculation request timed out.",
}

func matchPublicAccountingRecalcRoute(method, path string) (publicAccountingRecalcRoute, bool) {
	remainder, ok := strings.CutPrefix(path, "/api/v1/sites/")
	if !ok || strings.HasSuffix(remainder, "/") {
		return publicAccountingRecalcRoute{}, false
	}
	segments := strings.Split(remainder, "/")
	if len(segments) != 3 || segments[1] != "accounting" {
		return publicAccountingRecalcRoute{}, false
	}
	siteID, err := url.PathUnescape(segments[0])
	if err != nil || siteID == "" {
		return publicAccountingRecalcRoute{}, false
	}
	switch {
	case segments[2] == "recalculate" && method == http.MethodPost:
		return publicAccountingRecalcRoute{
			template:     "/api/v1/sites/{siteId}/accounting/recalculate",
			siteID:       siteID,
			kind:         "recalculate",
			internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/accounting/recalculate",
			write:        true,
		}, true
	case segments[2] == "recalculate-jobs" && method == http.MethodGet:
		return publicAccountingRecalcRoute{
			template:     "/api/v1/sites/{siteId}/accounting/recalculate-jobs",
			siteID:       siteID,
			kind:         "recalculate-jobs",
			internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/accounting/recalculate-jobs",
		}, true
	default:
		return publicAccountingRecalcRoute{}, false
	}
}

func dispatchAccountingRecalcRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAccountingRecalcRoute) {
	decision := routeDecisionFromContext(request.Context())
	if decision.RegistryRevision == 0 || decision.SelectedOwner != ownershipregistry.OwnerCore || h.registry == nil || h.registry.coreBaseURL == "" || h.registry.coreHTTPClient == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The accounting recalculation route is not active for this Session.", true, nil)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	if route.write && request.URL.RawQuery != "" {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation invalid", "Accounting recalculation mutation routes do not accept query parameters.", false, nil)
		return
	}

	session, ok := h.intelligenceSession(writer, request, route.write)
	if !ok {
		return
	}
	readAuth, readFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if readFailure != nil {
		if readFailure.status == http.StatusForbidden || readFailure.status == http.StatusNotFound {
			if route.write {
				h.emitAccountingRecalcAudit(request, session, route, nil, auditOutcomeDenied, "SITE_SCOPE_FORBIDDEN")
			}
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
			return
		}
		if route.write {
			h.emitAccountingRecalcAudit(request, session, route, nil, auditOutcomeForStatus(readFailure.status), readFailure.code)
		}
		writeProblem(writer, request, readFailure.status, readFailure.code, readFailure.title, readFailure.detail, readFailure.retryable, nil)
		return
	}
	if _, visible := readAuth.allowedSiteIDs[route.siteID]; !visible {
		if route.write {
			h.emitAccountingRecalcAudit(request, session, route, nil, auditOutcomeDenied, "SITE_SCOPE_FORBIDDEN")
		}
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
			h.emitAccountingRecalcAudit(request, session, route, nil, auditOutcomeRejected, code)
			writeProblem(writer, request, status, code, "Registry mutation invalid", "The accounting recalculation payload is unreadable or exceeds the supported size.", false, nil)
			return
		}
		if !json.Valid(raw) {
			h.emitAccountingRecalcAudit(request, session, route, nil, auditOutcomeRejected, "REGISTRY_MUTATION_INVALID")
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation invalid", "The accounting recalculation payload must be valid JSON.", false, nil)
			return
		}
		body = raw

		writeGrant, authOK := h.authorizeAccountingRecalcWrite(writer, request, session, route, body)
		if !authOK {
			return
		}
		grant = writeGrant
	}

	statusCode, respCode := h.forwardAccountingRecalcRequest(
		writer,
		request,
		session,
		decision.RegistryRevision,
		route,
		grant,
		body,
	)
	if route.write {
		h.emitAccountingRecalcAudit(request, session, route, body, auditOutcomeForStatus(statusCode), respCode)
	}
}

func (h *handler) authorizeAccountingRecalcWrite(
	writer http.ResponseWriter,
	request *http.Request,
	session bffSession,
	route publicAccountingRecalcRoute,
	body []byte,
) (string, bool) {
	for _, candidate := range []registryauth.Action{
		registryauth.ActionBillingManage,
		registryauth.ActionSavingsManage,
	} {
		auth, failure := h.authorizeRegistry(request.Context(), session, candidate)
		if failure != nil {
			if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
				continue
			}
			h.emitAccountingRecalcAudit(request, session, route, body, auditOutcomeForStatus(failure.status), failure.code)
			writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
			return "", false
		}
		if _, allowed := auth.allowedSiteIDs[route.siteID]; allowed {
			return auth.coreGrant, true
		}
	}
	h.emitAccountingRecalcAudit(request, session, route, body, auditOutcomeDenied, "ACCOUNTING_RECALC_FORBIDDEN")
	writeProblem(
		writer,
		request,
		http.StatusForbidden,
		"ACCOUNTING_RECALC_FORBIDDEN",
		"Accounting recalculation action forbidden",
		"The authenticated Principal is not allowed to trigger accounting recalculation on this Site.",
		false,
		nil,
	)
	return "", false
}

func (h *handler) forwardAccountingRecalcRequest(
	writer http.ResponseWriter,
	request *http.Request,
	session bffSession,
	registryRevision int64,
	route publicAccountingRecalcRoute,
	grant string,
	body []byte,
) (int, string) {
	targetURL := h.registry.coreBaseURL + route.internalPath
	if !route.write && request.URL.RawQuery != "" {
		targetURL += "?" + request.URL.RawQuery
	}
	reqCtx, cancel := context.WithTimeout(request.Context(), h.registry.coreTimeout)
	defer cancel()
	var bodyReader io.Reader
	if route.write {
		bodyReader = bytes.NewReader(body)
	}
	upstreamReq, err := http.NewRequestWithContext(reqCtx, request.Method, targetURL, bodyReader)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Core Registry request could not be constructed.", true, nil)
		return http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE"
	}
	upstreamReq.Header.Set("Accept", "application/json, application/problem+json")
	if route.write {
		upstreamReq.Header.Set("Content-Type", "application/json")
		displayName := strings.TrimSpace(session.Principal.DisplayName)
		if displayName == "" {
			displayName = strings.TrimSpace(session.Principal.Subject)
		}
		if displayName != "" {
			upstreamReq.Header.Set("X-Actor-Display-Name", displayName)
		}
	}
	upstreamReq.Header.Set("X-Delegation-Grant", grant)
	upstreamReq.Header.Set("X-Route-Policy-Revision", formatRevision(registryRevision))
	upstreamReq.Header.Set("X-Request-ID", requestIDFromContext(request.Context()))
	observability.InjectHTTP(request.Context(), upstreamReq.Header)

	response, err := h.registry.coreHTTPClient.Do(upstreamReq)
	if err != nil {
		if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			writeProblem(writer, request, http.StatusGatewayTimeout, "REGISTRY_TIMEOUT", "Registry timeout", accountingRecalcBackendMessages.gatewayTimeoutDetail, true, nil)
			return http.StatusGatewayTimeout, "REGISTRY_TIMEOUT"
		}
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Core Registry is temporarily unavailable.", true, nil)
		return http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE"
	}
	defer drainAndCloseBody(response.Body)

	writer.Header().Set("X-Route-Policy-Revision", formatRevision(registryRevision))
	return writeAccountingRecalcBackendResponse(writer, request, response, h.registry.maxResponseBytes)
}

func writeAccountingRecalcBackendResponse(writer http.ResponseWriter, request *http.Request, response *http.Response, maxBytes int64) (int, string) {
	if response.StatusCode == http.StatusAccepted {
		raw, err := readBoundedBody(response.Body, maxBytes)
		if err != nil {
			writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Core Registry response exceeded the supported size.", false, nil)
			return http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE"
		}
		if !json.Valid(raw) {
			writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Core Registry response is malformed.", false, nil)
			return http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE"
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write(append(bytes.TrimSpace(raw), '\n'))
		return http.StatusAccepted, ""
	}
	return writeSiteRegistryBackendResponse(writer, request, response, maxBytes, accountingRecalcBackendMessages)
}

func (h *handler) emitAccountingRecalcAudit(
	request *http.Request,
	session bffSession,
	route publicAccountingRecalcRoute,
	body []byte,
	outcome, reasonCode string,
) {
	summary := decodeFlatJSONObject(body)
	if reasonCode != "" && outcome != auditOutcomeSucceeded {
		if summary == nil {
			summary = decodeFlatJSONObject([]byte("{}"))
		}
		summary["reasonCode"] = reasonCode
	}
	h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
		Category:      auditserver.AggregateTypeRegistryMutation,
		TargetID:      "site:" + route.siteID,
		TargetLabel:   "能耗账务重算任务",
		Action:        "ACCOUNTING_RECALC_REQUESTED",
		Outcome:       outcome,
		ChangeSummary: summary,
	})
}
