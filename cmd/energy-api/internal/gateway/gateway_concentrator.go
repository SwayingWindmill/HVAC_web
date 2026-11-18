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

type concentratorUpstreamError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func matchConcentratorRoute(method, rawPath string) (gatewayID, unitID, operation string, writeOp bool, ok bool) {
	parts := strings.Split(rawPath, "/")
	if len(parts) != 8 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "gateways" || parts[5] != "concentrators" {
		return "", "", "", false, false
	}
	gatewayID, unitID, operation = parts[4], parts[6], parts[7]
	if gatewayID == "" || unitID == "" {
		return "", "", "", false, false
	}
	if method == http.MethodGet && (operation == "status" || operation == "eids") {
		return gatewayID, unitID, operation, false, true
	}
	if method == http.MethodPut && (operation == "params" || operation == "eids") {
		return gatewayID, unitID, operation, true, true
	}
	return "", "", "", false, false
}

func matchConcentratorPathTemplate(rawPath string) (string, bool) {
	parts := strings.Split(rawPath, "/")
	if len(parts) != 8 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "gateways" || parts[5] != "concentrators" || parts[4] == "" || parts[6] == "" {
		return "", false
	}
	switch parts[7] {
	case "status", "params", "eids":
		return "/api/v1/gateways/{gatewayId}/concentrators/{unitId}/" + parts[7], true
	default:
		return "", false
	}
}

func validConcentratorPathSegment(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func (h *handler) serveGatewayConcentrator(w http.ResponseWriter, r *http.Request) bool {
	gatewayID, unitID, operation, writeOp, ok := matchConcentratorRoute(r.Method, r.URL.Path)
	if !ok {
		return false
	}
	if routeDecisionFromContext(r.Context()).SelectedOwner != ownershipregistry.OwnerConnectivity || h.connectivity == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "CONNECTIVITY_UNAVAILABLE", "Concentrator control unavailable", "Connectivity is not configured.", true, nil)
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
	if !validConcentratorPathSegment(gatewayID) || !validConcentratorPathSegment(unitID) {
		writeProblem(w, r, http.StatusBadRequest, "CONCENTRATOR_REQUEST_INVALID", "Concentrator request invalid", "The concentrator path is invalid.", false, nil)
		return true
	}

	auth, failureAuth := h.authorizeRegistry(r.Context(), session, action)
	if failureAuth != nil {
		writeProblem(w, r, failureAuth.status, failureAuth.code, failureAuth.title, failureAuth.detail, failureAuth.retryable, nil)
		return true
	}
	// Concentrator MQTT RPC + timeout budget (18-25s) exceeds energy-api's 10s default HTTP WriteTimeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))

	var reqBody io.Reader
	if writeOp {
		maxBytes := int64(32 << 10)
		if operation == "eids" {
			maxBytes = 128 << 10
		}
		bodyBytes, err := readBoundedBody(r.Body, maxBytes)
		if err != nil {
			status := http.StatusBadRequest
			code := "CONCENTRATOR_REQUEST_INVALID"
			detail := "The concentrator request body could not be read."
			if errors.Is(err, errRegistryBodyTooLarge) {
				status = http.StatusRequestEntityTooLarge
				code = "PAYLOAD_TOO_LARGE"
				detail = "The concentrator request body exceeds the maximum size."
			}
			writeProblem(w, r, status, code, "Concentrator request invalid", detail, false, nil)
			return true
		}
		reqBody = bytes.NewReader(bodyBytes)
	}

	upstreamURL := h.connectivity.BaseURL + "/internal/v1/gateways/" + url.PathEscape(gatewayID) + "/concentrators/" + url.PathEscape(unitID) + "/" + operation
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, reqBody)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "CONCENTRATOR_REQUEST_INVALID", "Concentrator request invalid", "The concentrator path is invalid.", false, nil)
		return true
	}
	if writeOp {
		upstreamReq.Header.Set("Content-Type", "application/json")
	}
	upstreamReq.Header.Set("X-Delegation-Grant", auth.coreGrant)

	response, err := h.connectivity.HTTPClient.Do(upstreamReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeProblem(w, r, http.StatusGatewayTimeout, "CONCENTRATOR_RPC_TIMEOUT", "Concentrator request timed out", "The gateway did not answer before the RPC deadline.", true, nil)
			return true
		}
		writeProblem(w, r, http.StatusServiceUnavailable, "CONNECTIVITY_UNAVAILABLE", "Concentrator control unavailable", "Connectivity could not be reached.", true, nil)
		return true
	}
	defer drainAndCloseBody(response.Body)

	body, err := readBoundedBody(response.Body, 256<<10)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "CONNECTIVITY_RESPONSE_INVALID", "Concentrator response invalid", "Connectivity returned an invalid response.", true, nil)
		return true
	}

	if response.StatusCode >= http.StatusBadRequest {
		writeUpstreamConcentratorProblem(w, r, response.StatusCode, body)
		return true
	}
	if response.StatusCode >= http.StatusMultipleChoices {
		writeProblem(w, r, http.StatusBadGateway, "CONNECTIVITY_RESPONSE_INVALID", "Concentrator response invalid", "Connectivity returned an unexpected redirect.", true, nil)
		return true
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
	return true
}

func writeUpstreamConcentratorProblem(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	var upstream concentratorUpstreamError
	_ = json.Unmarshal(body, &upstream)
	code := strings.TrimSpace(upstream.Code)
	if code == "" {
		code = "CONCENTRATOR_UPSTREAM_ERROR"
	}
	detail := strings.TrimSpace(upstream.Message)
	if detail == "" {
		detail = "Concentrator request failed."
	}
	title := http.StatusText(status)
	if title == "" {
		title = "Concentrator request failed"
	}
	writeProblem(w, r, status, code, title, detail, status >= http.StatusBadGateway, nil)
}
