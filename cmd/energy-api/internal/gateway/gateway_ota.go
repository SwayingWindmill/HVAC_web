package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

const (
	maxGatewayOTAJSONRequestBytes  = 64 << 10
	maxGatewayOTAUploadBodyBytes   = (64 << 20) + (256 << 10)
	maxGatewayOTAResponseBodyBytes = 4 << 20
	gatewayOTAUploadTimeout        = 120 * time.Second
)

type gatewayOTARoute struct {
	template     string
	upstreamPath string
	campaignID   string
	action       registryauth.Action
	writeOp      bool
	multipart    bool
	optionalBody bool
	forwardQuery bool
	auditAction  string
	targetLabel  string
}

func matchGatewayOTARoute(method, rawPath string) (gatewayOTARoute, bool) {
	parts := strings.Split(rawPath, "/")
	if len(parts) < 5 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "ota" {
		return gatewayOTARoute{}, false
	}

	switch len(parts) {
	case 5:
		switch parts[4] {
		case "packages":
			switch method {
			case http.MethodGet:
				return gatewayOTARoute{
					template:     "/api/v1/ota/packages",
					upstreamPath: "/internal/v1/ota/packages",
					action:       registryauth.ActionDeviceRead,
					forwardQuery: true,
				}, true
			case http.MethodPost:
				return gatewayOTARoute{
					template:     "/api/v1/ota/packages",
					upstreamPath: "/internal/v1/ota/packages",
					action:       registryauth.ActionDeviceWrite,
					writeOp:      true,
					multipart:    true,
					auditAction:  "OTA_PACKAGE_UPLOADED",
					targetLabel:  "OTA 升级包上传",
				}, true
			}
		case "campaigns":
			switch method {
			case http.MethodGet:
				return gatewayOTARoute{
					template:     "/api/v1/ota/campaigns",
					upstreamPath: "/internal/v1/ota/campaigns",
					action:       registryauth.ActionDeviceRead,
					forwardQuery: true,
				}, true
			case http.MethodPost:
				return gatewayOTARoute{
					template:     "/api/v1/ota/campaigns",
					upstreamPath: "/internal/v1/ota/campaigns",
					action:       registryauth.ActionDeviceWrite,
					writeOp:      true,
					auditAction:  "OTA_CAMPAIGN_CREATED",
					targetLabel:  "OTA 发布任务创建",
				}, true
			}
		}
	case 6:
		if parts[4] == "campaigns" && isValidGatewayPathSegment(parts[5]) && method == http.MethodGet {
			return gatewayOTARoute{
				template:     "/api/v1/ota/campaigns/{campaignId}",
				upstreamPath: "/internal/v1/ota/campaigns/" + url.PathEscape(parts[5]),
				campaignID:   parts[5],
				action:       registryauth.ActionDeviceRead,
			}, true
		}
	case 7:
		if parts[4] == "campaigns" && isValidGatewayPathSegment(parts[5]) && method == http.MethodPost {
			escaped := url.PathEscape(parts[5])
			switch parts[6] {
			case "pause":
				return gatewayOTARoute{
					template:     "/api/v1/ota/campaigns/{campaignId}/pause",
					upstreamPath: "/internal/v1/ota/campaigns/" + escaped + "/pause",
					campaignID:   parts[5],
					action:       registryauth.ActionDeviceWrite,
					writeOp:      true,
					optionalBody: true,
					auditAction:  "OTA_CAMPAIGN_PAUSED",
					targetLabel:  "OTA 发布任务暂停",
				}, true
			case "resume":
				return gatewayOTARoute{
					template:     "/api/v1/ota/campaigns/{campaignId}/resume",
					upstreamPath: "/internal/v1/ota/campaigns/" + escaped + "/resume",
					campaignID:   parts[5],
					action:       registryauth.ActionDeviceWrite,
					writeOp:      true,
					optionalBody: true,
					auditAction:  "OTA_CAMPAIGN_RESUMED",
					targetLabel:  "OTA 发布任务恢复",
				}, true
			case "rollback":
				return gatewayOTARoute{
					template:     "/api/v1/ota/campaigns/{campaignId}/rollback",
					upstreamPath: "/internal/v1/ota/campaigns/" + escaped + "/rollback",
					campaignID:   parts[5],
					action:       registryauth.ActionDeviceWrite,
					writeOp:      true,
					optionalBody: true,
					auditAction:  "OTA_CAMPAIGN_ROLLED_BACK",
					targetLabel:  "OTA 发布任务回滚",
				}, true
			}
		}
	}
	return gatewayOTARoute{}, false
}

func matchGatewayOTAPathTemplate(rawPath string) (string, bool) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if route, ok := matchGatewayOTARoute(method, rawPath); ok {
			return route.template, true
		}
	}
	return "", false
}

func (h *handler) serveGatewayOTA(w http.ResponseWriter, r *http.Request) bool {
	route, ok := matchGatewayOTARoute(r.Method, r.URL.Path)
	if !ok {
		return false
	}
	if routeDecisionFromContext(r.Context()).SelectedOwner != ownershipregistry.OwnerConnectivity || h.connectivity == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "CONNECTIVITY_UNAVAILABLE", "OTA service unavailable", "Connectivity is not configured.", true, nil)
		return true
	}

	session, failure := h.identitySession(r)
	if failure != nil {
		writeIdentityFailure(w, r, *failure)
		return true
	}

	rateDimension := limitpolicy.DimensionRest
	if route.writeOp {
		if failure := h.identity.validateStateChange(r, session, r.Header.Get("X-CSRF-Token")); failure != nil {
			writeIdentityFailure(w, r, *failure)
			return true
		}
		rateDimension = limitpolicy.DimensionCommandWrite
	}
	if !h.allowRateLimitedTenant(w, r, rateDimension, session.TenantID) {
		return true
	}

	auth, failureAuth := h.authorizeRegistry(r.Context(), session, route.action)
	if failureAuth != nil {
		if route.writeOp {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayOTAAuditInput(route, nil, nil, auditOutcomeForStatus(failureAuth.status), failureAuth.code))
		}
		writeProblem(w, r, failureAuth.status, failureAuth.code, failureAuth.title, failureAuth.detail, failureAuth.retryable, nil)
		return true
	}

	var (
		upstreamBody io.Reader
		auditReqBody []byte
		contentType  string
		httpClient   = h.connectivity.HTTPClient
	)

	if route.multipart {
		mediaType, params, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if parseErr != nil || mediaType != "multipart/form-data" || strings.TrimSpace(params["boundary"]) == "" {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayOTAAuditInput(route, nil, nil, auditOutcomeRejected, "OTA_REQUEST_INVALID"))
			writeProblem(w, r, http.StatusBadRequest, "OTA_REQUEST_INVALID", "OTA upload invalid", "Content-Type must be multipart/form-data.", false, nil)
			return true
		}
		deadline := h.now().Add(gatewayOTAUploadTimeout)
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(deadline)
		_ = rc.SetWriteDeadline(deadline)

		r.Body = http.MaxBytesReader(w, r.Body, maxGatewayOTAUploadBodyBytes)
		upstreamBody = r.Body
		contentType = r.Header.Get("Content-Type")
		cloned := *h.connectivity.HTTPClient
		if cloned.Timeout < gatewayOTAUploadTimeout {
			cloned.Timeout = gatewayOTAUploadTimeout
		}
		httpClient = &cloned
	} else {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(25 * time.Second))
		if route.writeOp && r.Body != nil {
			bodyBytes, err := readBoundedBody(r.Body, maxGatewayOTAJSONRequestBytes)
			if err != nil {
				status := http.StatusBadRequest
				code := "OTA_REQUEST_INVALID"
				detail := "The OTA request body could not be read."
				if errors.Is(err, errRegistryBodyTooLarge) {
					status = http.StatusRequestEntityTooLarge
					code = "PAYLOAD_TOO_LARGE"
					detail = "The OTA request body exceeds the maximum size."
				}
				h.emitDomainAudit(r.Context(), r, session, buildGatewayOTAAuditInput(route, nil, nil, auditOutcomeRejected, code))
				writeProblem(w, r, status, code, "OTA request invalid", detail, false, nil)
				return true
			}
			auditReqBody = bodyBytes
			if len(bodyBytes) > 0 || !route.optionalBody {
				upstreamBody = bytes.NewReader(bodyBytes)
				contentType = "application/json"
			}
		}
	}

	upstreamURL := h.connectivity.BaseURL + route.upstreamPath
	if route.forwardQuery && r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, upstreamBody)
	if err != nil {
		if route.writeOp {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayOTAAuditInput(route, auditReqBody, nil, auditOutcomeRejected, "OTA_REQUEST_INVALID"))
		}
		writeProblem(w, r, http.StatusBadRequest, "OTA_REQUEST_INVALID", "OTA request invalid", "The OTA path is invalid.", false, nil)
		return true
	}
	if contentType != "" {
		upstreamReq.Header.Set("Content-Type", contentType)
	}
	if route.multipart && r.ContentLength > 0 && r.ContentLength <= maxGatewayOTAUploadBodyBytes {
		upstreamReq.ContentLength = r.ContentLength
	}
	upstreamReq.Header.Set("X-Delegation-Grant", auth.coreGrant)
	observability.InjectHTTP(r.Context(), upstreamReq.Header)

	response, err := httpClient.Do(upstreamReq)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			if route.writeOp {
				h.emitDomainAudit(r.Context(), r, session, buildGatewayOTAAuditInput(route, auditReqBody, nil, auditOutcomeRejected, "PAYLOAD_TOO_LARGE"))
			}
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "OTA package too large", "The OTA package upload exceeds the 64 MiB limit.", false, nil)
			return true
		}
		if errors.Is(err, context.DeadlineExceeded) {
			if route.writeOp {
				h.emitDomainAudit(r.Context(), r, session, buildGatewayOTAAuditInput(route, auditReqBody, nil, auditOutcomeFailed, "CONNECTIVITY_TIMEOUT"))
			}
			writeProblem(w, r, http.StatusGatewayTimeout, "CONNECTIVITY_TIMEOUT", "OTA request timed out", "Connectivity did not answer in time.", true, nil)
			return true
		}
		if route.writeOp {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayOTAAuditInput(route, auditReqBody, nil, auditOutcomeFailed, "CONNECTIVITY_UNAVAILABLE"))
		}
		writeProblem(w, r, http.StatusServiceUnavailable, "CONNECTIVITY_UNAVAILABLE", "OTA service unavailable", "Connectivity could not be reached.", true, nil)
		return true
	}
	defer drainAndCloseBody(response.Body)

	body, err := readBoundedBody(response.Body, maxGatewayOTAResponseBodyBytes)
	if err != nil {
		if route.writeOp {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayOTAAuditInput(route, auditReqBody, nil, auditOutcomeFailed, "CONNECTIVITY_RESPONSE_INVALID"))
		}
		writeProblem(w, r, http.StatusBadGateway, "CONNECTIVITY_RESPONSE_INVALID", "OTA response invalid", "Connectivity returned an invalid response.", true, nil)
		return true
	}

	if route.writeOp {
		h.emitDomainAudit(r.Context(), r, session, buildGatewayOTAAuditInput(route, auditReqBody, body, auditOutcomeForStatus(response.StatusCode), problemCodeFromRaw(body)))
	}

	if response.StatusCode >= http.StatusBadRequest {
		writeUpstreamOTAProblem(w, r, response.StatusCode, body)
		return true
	}
	if response.StatusCode >= http.StatusMultipleChoices {
		writeProblem(w, r, http.StatusBadGateway, "CONNECTIVITY_RESPONSE_INVALID", "OTA response invalid", "Connectivity returned an unexpected redirect.", true, nil)
		return true
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
	return true
}

func buildGatewayOTAAuditInput(
	route gatewayOTARoute,
	reqBody, respBody []byte,
	outcome, reasonCode string,
) domainAuditInput {
	targetID := "ota"
	if route.campaignID != "" {
		targetID = "ota-campaign:" + route.campaignID
	}
	summary := map[string]any{}
	if reqMap := decodeFlatJSONObject(reqBody); len(reqMap) > 0 {
		for _, k := range []string{"reason", "name", "packageId"} {
			if val := stringValue(reqMap, k); val != "" {
				summary[k] = val
			}
		}
	}
	if respMap := decodeFlatJSONObject(respBody); len(respMap) > 0 {
		if id := stringValue(respMap, "id"); id != "" {
			if route.multipart {
				targetID = "ota-package:" + id
			} else if route.campaignID == "" {
				targetID = "ota-campaign:" + id
			}
		}
		for _, k := range []string{"version", "component", "targetArch", "sha256", "status"} {
			if val := stringValue(respMap, k); val != "" {
				summary[k] = val
			}
		}
	}
	if outcome != auditOutcomeSucceeded && reasonCode != "" {
		summary["reasonCode"] = reasonCode
	}
	return domainAuditInput{
		Category:      auditserver.AggregateTypeGatewayCredential,
		TargetID:      targetID,
		TargetLabel:   route.targetLabel,
		Action:        route.auditAction,
		Outcome:       outcome,
		ChangeSummary: summary,
	}
}

func writeUpstreamOTAProblem(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	var upstream concentratorUpstreamError
	_ = json.Unmarshal(body, &upstream)
	code := strings.TrimSpace(upstream.Code)
	if code == "" {
		code = "OTA_UPSTREAM_ERROR"
	}
	detail := "OTA request failed."
	if status >= http.StatusInternalServerError {
		detail = "OTA service is unavailable."
	} else if trimmed := strings.TrimSpace(upstream.Message); trimmed != "" {
		detail = trimmed
	}
	title := http.StatusText(status)
	if title == "" {
		title = "OTA request failed"
	}
	writeProblem(w, r, status, code, title, detail, status >= http.StatusBadGateway, nil)
}
