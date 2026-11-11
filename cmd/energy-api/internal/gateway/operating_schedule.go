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
)

type publicOperatingScheduleRoute struct {
	template     string
	siteID       string
	exceptionID  string
	kind         string
	internalPath string
	write        bool
}

func matchPublicOperatingScheduleRoute(method, path string) (publicOperatingScheduleRoute, bool) {
	remainder, ok := strings.CutPrefix(path, "/api/v1/sites/")
	if !ok || strings.HasSuffix(remainder, "/") {
		return publicOperatingScheduleRoute{}, false
	}
	segments := strings.Split(remainder, "/")
	if len(segments) < 2 {
		return publicOperatingScheduleRoute{}, false
	}
	siteID, err := url.PathUnescape(segments[0])
	if err != nil || siteID == "" {
		return publicOperatingScheduleRoute{}, false
	}
	switch {
	case len(segments) == 2 && segments[1] == "operating-schedule":
		switch method {
		case http.MethodGet:
			return publicOperatingScheduleRoute{
				template:     "/api/v1/sites/{siteId}/operating-schedule",
				siteID:       siteID,
				kind:         "schedule-get",
				internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/operating-schedule",
			}, true
		case http.MethodPut:
			return publicOperatingScheduleRoute{
				template:     "/api/v1/sites/{siteId}/operating-schedule",
				siteID:       siteID,
				kind:         "schedule-put",
				internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/operating-schedule",
				write:        true,
			}, true
		}
	case len(segments) == 2 && segments[1] == "operating-exceptions":
		switch method {
		case http.MethodGet:
			return publicOperatingScheduleRoute{
				template:     "/api/v1/sites/{siteId}/operating-exceptions",
				siteID:       siteID,
				kind:         "exceptions-list",
				internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/operating-exceptions",
			}, true
		case http.MethodPost:
			return publicOperatingScheduleRoute{
				template:     "/api/v1/sites/{siteId}/operating-exceptions",
				siteID:       siteID,
				kind:         "exceptions-create",
				internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/operating-exceptions",
				write:        true,
			}, true
		}
	case len(segments) == 4 && segments[1] == "operating-exceptions" && segments[3] == "decide" && method == http.MethodPost:
		exceptionID, err := url.PathUnescape(segments[2])
		if err != nil || exceptionID == "" {
			return publicOperatingScheduleRoute{}, false
		}
		return publicOperatingScheduleRoute{
			template:     "/api/v1/sites/{siteId}/operating-exceptions/{exceptionId}/decide",
			siteID:       siteID,
			exceptionID:  exceptionID,
			kind:         "exceptions-decide",
			internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/operating-exceptions/" + url.PathEscape(exceptionID) + "/decide",
			write:        true,
		}, true
	}
	return publicOperatingScheduleRoute{}, false
}

func resolveOperatingExceptionCreateAction(raw []byte) (registryauth.Action, string, error) {
	var envelope struct {
		AutoApprove bool `json:"autoApprove"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", "", err
	}
	if envelope.AutoApprove {
		return registryauth.ActionScheduleManage, "SCHEDULE_MANAGE_FORBIDDEN", nil
	}
	return registryauth.ActionScheduleRequest, "SCHEDULE_REQUEST_FORBIDDEN", nil
}

func resolveOperatingExceptionDecideAction(raw []byte) (registryauth.Action, bool, error) {
	var envelope struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", false, err
	}
	switch envelope.Decision {
	case "APPROVE", "REJECT":
		return registryauth.ActionScheduleManage, false, nil
	case "CANCEL":
		return registryauth.ActionScheduleManage, true, nil
	default:
		return "", false, errors.New("unsupported operating exception decision")
	}
}

func dispatchOperatingScheduleRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicOperatingScheduleRoute) {
	decision := routeDecisionFromContext(request.Context())
	if decision.RegistryRevision == 0 || decision.SelectedOwner != ownershipregistry.OwnerCore || h.registry == nil || h.registry.coreBaseURL == "" || h.registry.coreHTTPClient == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Operating Schedule route is not active for this Session.", true, nil)
		return
	}
	if !isLowerUUIDv7(route.siteID) || (route.exceptionID != "" && !isLowerUUIDv7(route.exceptionID)) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site or Operating Exception was not found.", false, nil)
		return
	}
	if route.write && request.URL.RawQuery != "" {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation invalid", "Operating Schedule mutation routes do not accept query parameters.", false, nil)
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
			writeProblem(writer, request, status, code, "Registry mutation invalid", "The Operating Schedule mutation payload is unreadable or exceeds the supported size.", false, nil)
			return
		}
		if !json.Valid(raw) {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation invalid", "The Operating Schedule mutation payload must be valid JSON.", false, nil)
			return
		}
		body = raw

		switch route.kind {
		case "schedule-put":
			writeGrant, ok := h.authorizeSiteScheduleAction(writer, request, session, route.siteID, registryauth.ActionScheduleManage, "SCHEDULE_MANAGE_FORBIDDEN", "The authenticated Principal is not allowed to manage Operating Schedules on this Site.")
			if !ok {
				return
			}
			grant = writeGrant
		case "exceptions-create":
			action, forbiddenCode, err := resolveOperatingExceptionCreateAction(raw)
			if err != nil {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation invalid", "The Operating Exception request payload is invalid.", false, nil)
				return
			}
			detail := "The authenticated Principal is not allowed to request Operating Exceptions on this Site."
			if action == registryauth.ActionScheduleManage {
				detail = "The authenticated Principal is not allowed to directly approve Operating Exceptions on this Site."
			}
			writeGrant, ok := h.authorizeSiteScheduleAction(writer, request, session, route.siteID, action, forbiddenCode, detail)
			if !ok {
				return
			}
			grant = writeGrant
		case "exceptions-decide":
			primaryAction, allowRequestFallback, err := resolveOperatingExceptionDecideAction(raw)
			if err != nil {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation invalid", "The Operating Exception decision payload is invalid.", false, nil)
				return
			}
			auth, failure := h.authorizeRegistry(request.Context(), session, primaryAction)
			if failure != nil && failure.status != http.StatusForbidden && failure.status != http.StatusNotFound {
				writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
				return
			}
			if _, allowed := auth.allowedSiteIDs[route.siteID]; failure == nil && allowed {
				grant = auth.coreGrant
			} else if allowRequestFallback {
				writeGrant, ok := h.authorizeSiteScheduleAction(writer, request, session, route.siteID, registryauth.ActionScheduleRequest, "SCHEDULE_REQUEST_FORBIDDEN", "The authenticated Principal is not allowed to cancel Operating Exceptions on this Site.")
				if !ok {
					return
				}
				grant = writeGrant
			} else {
				writeProblem(writer, request, http.StatusForbidden, "SCHEDULE_MANAGE_FORBIDDEN", "Operating Schedule management forbidden", "The authenticated Principal is not allowed to approve or reject Operating Exceptions on this Site.", false, nil)
				return
			}
		}
	}

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
		return
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
	upstreamReq.Header.Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	upstreamReq.Header.Set("X-Request-ID", requestIDFromContext(request.Context()))
	observability.InjectHTTP(request.Context(), upstreamReq.Header)

	response, err := h.registry.coreHTTPClient.Do(upstreamReq)
	if err != nil {
		if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			writeProblem(writer, request, http.StatusGatewayTimeout, "REGISTRY_TIMEOUT", "Registry timeout", "The Operating Schedule request did not complete before the Gateway deadline.", true, nil)
			return
		}
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Core Registry is temporarily unavailable.", true, nil)
		return
	}
	defer response.Body.Close()

	writer.Header().Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	writeOperatingScheduleBackendResponse(writer, request, response, h.registry.maxResponseBytes)
}

func (h *handler) authorizeSiteScheduleAction(
	writer http.ResponseWriter,
	request *http.Request,
	session bffSession,
	siteID string,
	action registryauth.Action,
	forbiddenCode string,
	forbiddenDetail string,
) (string, bool) {
	authorization, failure := h.authorizeRegistry(request.Context(), session, action)
	if failure != nil && failure.status != http.StatusForbidden && failure.status != http.StatusNotFound {
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return "", false
	}
	if _, allowed := authorization.allowedSiteIDs[siteID]; failure != nil || !allowed {
		writeProblem(writer, request, http.StatusForbidden, forbiddenCode, "Operating Schedule action forbidden", forbiddenDetail, false, nil)
		return "", false
	}
	return authorization.coreGrant, true
}

func writeOperatingScheduleBackendResponse(writer http.ResponseWriter, request *http.Request, response *http.Response, maxBytes int64) {
	raw, err := readBoundedBody(response.Body, maxBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Core Registry response exceeded the supported size.", false, nil)
		return
	}
	if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusCreated {
		if !json.Valid(raw) {
			writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Core Registry response is malformed.", false, nil)
			return
		}
		if response.Header.Get("X-Idempotent-Replay") == "true" {
			writer.Header().Set("X-Idempotent-Replay", "true")
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(append(bytes.TrimSpace(raw), '\n'))
		return
	}

	var problem struct {
		Code      string `json:"code"`
		Detail    string `json:"detail"`
		Retryable bool   `json:"retryable"`
	}
	_ = json.Unmarshal(raw, &problem)

	switch response.StatusCode {
	case http.StatusBadRequest:
		code := problem.Code
		if code != "REGISTRY_MUTATION_INVALID" && code != "REGISTRY_QUERY_INVALID" {
			code = "REGISTRY_MUTATION_INVALID"
		}
		detail := problem.Detail
		if detail == "" {
			detail = "The Operating Schedule request is invalid."
		}
		writeProblem(writer, request, http.StatusBadRequest, code, "Operating Schedule request invalid", detail, false, nil)
	case http.StatusForbidden:
		code := problem.Code
		if code == "" {
			code = "OPERATING_EXCEPTION_FORBIDDEN"
		}
		detail := problem.Detail
		if detail == "" {
			detail = "The authenticated Principal is not allowed to perform this Operating Exception action."
		}
		writeProblem(writer, request, http.StatusForbidden, code, "Operating Schedule action forbidden", detail, false, nil)
	case http.StatusNotFound:
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site or Operating Exception was not found.", false, nil)
	case http.StatusConflict:
		code := problem.Code
		if code == "" {
			code = "REGISTRY_REVISION_CONFLICT"
		}
		detail := problem.Detail
		if detail == "" {
			detail = "The Operating Schedule mutation conflicts with the current authoritative state."
		}
		writeProblem(writer, request, http.StatusConflict, code, "Operating Schedule conflict", detail, false, nil)
	case http.StatusGatewayTimeout:
		writeProblem(writer, request, http.StatusGatewayTimeout, "REGISTRY_TIMEOUT", "Registry timeout", "The Operating Schedule request timed out.", true, nil)
	default:
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Core Registry is temporarily unavailable.", true, nil)
	}
}
