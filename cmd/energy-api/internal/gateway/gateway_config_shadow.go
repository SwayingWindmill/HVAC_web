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
	"time"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const maxGatewayConfigShadowBodyBytes = 1 << 20

func matchConfigShadowRoute(method, rawPath string) (gatewayID string, writeOp bool, ok bool) {
	parts := strings.Split(rawPath, "/")
	if len(parts) != 6 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "gateways" || parts[5] != "config-shadow" {
		return "", false, false
	}
	gatewayID = parts[4]
	if gatewayID == "" {
		return "", false, false
	}
	switch method {
	case http.MethodGet:
		return gatewayID, false, true
	case http.MethodPut:
		return gatewayID, true, true
	default:
		return "", false, false
	}
}

func matchConfigShadowPathTemplate(rawPath string) (string, bool) {
	parts := strings.Split(rawPath, "/")
	if len(parts) != 6 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "gateways" || parts[4] == "" || parts[5] != "config-shadow" {
		return "", false
	}
	return "/api/v1/gateways/{gatewayId}/config-shadow", true
}

func (h *handler) serveGatewayConfigShadow(w http.ResponseWriter, r *http.Request) bool {
	gatewayID, writeOp, ok := matchConfigShadowRoute(r.Method, r.URL.Path)
	if !ok {
		return false
	}
	if routeDecisionFromContext(r.Context()).SelectedOwner != ownershipregistry.OwnerConnectivity || h.connectivity == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "CONNECTIVITY_UNAVAILABLE", "Gateway config shadow unavailable", "Connectivity is not configured.", true, nil)
		return true
	}

	session, failure := h.identitySession(r)
	if failure != nil {
		writeIdentityFailure(w, r, *failure)
		return true
	}
	action := registryauth.ActionDeviceRead
	rateDimension := limitpolicy.DimensionRest
	if writeOp {
		if failure := h.identity.validateStateChange(r, session, r.Header.Get("X-CSRF-Token")); failure != nil {
			writeIdentityFailure(w, r, *failure)
			return true
		}
		action = registryauth.ActionDeviceWrite
		rateDimension = limitpolicy.DimensionCommandWrite
	}
	if !h.allowRateLimitedTenant(w, r, rateDimension, session.TenantID) {
		return true
	}
	if !validConcentratorPathSegment(gatewayID) {
		writeProblem(w, r, http.StatusBadRequest, "CONFIG_SHADOW_REQUEST_INVALID", "Config shadow request invalid", "The gateway config shadow path is invalid.", false, nil)
		return true
	}

	auth, failureAuth := h.authorizeRegistry(r.Context(), session, action)
	if failureAuth != nil {
		writeProblem(w, r, failureAuth.status, failureAuth.code, failureAuth.title, failureAuth.detail, failureAuth.retryable, nil)
		return true
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(20 * time.Second))

	var reqBody io.Reader
	if writeOp {
		bodyBytes, err := readBoundedBody(r.Body, maxGatewayConfigShadowBodyBytes)
		if err != nil {
			status := http.StatusBadRequest
			code := "CONFIG_SHADOW_REQUEST_INVALID"
			detail := "The config shadow request body could not be read."
			if errors.Is(err, errRegistryBodyTooLarge) {
				status = http.StatusRequestEntityTooLarge
				code = "PAYLOAD_TOO_LARGE"
				detail = "The config shadow request body exceeds the maximum size."
			}
			writeProblem(w, r, status, code, "Config shadow request invalid", detail, false, nil)
			return true
		}
		reqBody = bytes.NewReader(bodyBytes)
	}

	upstreamURL := h.connectivity.BaseURL + "/internal/v1/gateways/" + url.PathEscape(gatewayID) + "/config-shadow"
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, reqBody)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "CONFIG_SHADOW_REQUEST_INVALID", "Config shadow request invalid", "The gateway config shadow path is invalid.", false, nil)
		return true
	}
	if writeOp {
		upstreamReq.Header.Set("Content-Type", "application/json")
	}
	upstreamReq.Header.Set("X-Delegation-Grant", auth.coreGrant)

	response, err := h.connectivity.HTTPClient.Do(upstreamReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeProblem(w, r, http.StatusGatewayTimeout, "CONNECTIVITY_TIMEOUT", "Config shadow request timed out", "Connectivity did not answer in time.", true, nil)
			return true
		}
		writeProblem(w, r, http.StatusServiceUnavailable, "CONNECTIVITY_UNAVAILABLE", "Gateway config shadow unavailable", "Connectivity could not be reached.", true, nil)
		return true
	}
	defer response.Body.Close()

	body, err := readBoundedBody(response.Body, maxGatewayConfigShadowBodyBytes)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "CONNECTIVITY_RESPONSE_INVALID", "Config shadow response invalid", "Connectivity returned an invalid response.", true, nil)
		return true
	}

	if response.StatusCode >= http.StatusBadRequest {
		writeUpstreamConfigShadowProblem(w, r, response.StatusCode, body)
		return true
	}
	if response.StatusCode >= http.StatusMultipleChoices {
		writeProblem(w, r, http.StatusBadGateway, "CONNECTIVITY_RESPONSE_INVALID", "Config shadow response invalid", "Connectivity returned an unexpected redirect.", true, nil)
		return true
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
	return true
}

func writeUpstreamConfigShadowProblem(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	var upstream concentratorUpstreamError
	_ = json.Unmarshal(body, &upstream)
	code := strings.TrimSpace(upstream.Code)
	if code == "" {
		code = "CONFIG_SHADOW_UPSTREAM_ERROR"
	}
	detail := "Gateway config shadow request failed."
	if status >= http.StatusInternalServerError {
		detail = "Gateway config shadow service is unavailable."
	} else if trimmed := strings.TrimSpace(upstream.Message); trimmed != "" {
		detail = trimmed
	}
	title := http.StatusText(status)
	if title == "" {
		title = "Config shadow request failed"
	}
	writeProblem(w, r, status, code, title, detail, status >= http.StatusBadGateway, nil)
}
