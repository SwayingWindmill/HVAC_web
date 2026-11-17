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

const (
	maxGatewayDiagnosticsRequestBodyBytes  = 64 << 10
	maxGatewayDiagnosticsResponseBodyBytes = 1 << 20
)

type gatewayDiagnosticsRoute struct {
	template     string
	gatewayID    string
	upstreamPath string
	writeOp      bool
	forwardBody  bool
	forwardQuery bool
}

func matchGatewayDiagnosticsRoute(method, rawPath string) (gatewayDiagnosticsRoute, bool) {
	parts := strings.Split(rawPath, "/")
	if len(parts) < 6 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "gateways" || parts[4] == "" {
		return gatewayDiagnosticsRoute{}, false
	}
	gatewayID := parts[4]
	escapedGatewayID := url.PathEscape(gatewayID)

	if len(parts) == 6 {
		switch parts[5] {
		case "quarantine":
			if method == http.MethodGet {
				return gatewayDiagnosticsRoute{
					template:     "/api/v1/gateways/{gatewayId}/quarantine",
					gatewayID:    gatewayID,
					upstreamPath: "/internal/v1/gateways/" + escapedGatewayID + "/quarantine",
					forwardQuery: true,
				}, true
			}
		case "packet-capture":
			if method == http.MethodGet {
				return gatewayDiagnosticsRoute{
					template:     "/api/v1/gateways/{gatewayId}/packet-capture",
					gatewayID:    gatewayID,
					upstreamPath: "/internal/v1/gateways/" + escapedGatewayID + "/packet-capture",
				}, true
			}
		}
		return gatewayDiagnosticsRoute{}, false
	}

	if len(parts) == 7 && parts[5] == "packet-capture" && method == http.MethodPost {
		switch parts[6] {
		case "start":
			return gatewayDiagnosticsRoute{
				template:     "/api/v1/gateways/{gatewayId}/packet-capture/start",
				gatewayID:    gatewayID,
				upstreamPath: "/internal/v1/gateways/" + escapedGatewayID + "/packet-capture/start",
				writeOp:      true,
				forwardBody:  true,
			}, true
		case "stop":
			return gatewayDiagnosticsRoute{
				template:     "/api/v1/gateways/{gatewayId}/packet-capture/stop",
				gatewayID:    gatewayID,
				upstreamPath: "/internal/v1/gateways/" + escapedGatewayID + "/packet-capture/stop",
				writeOp:      true,
			}, true
		}
	}
	return gatewayDiagnosticsRoute{}, false
}

func matchGatewayDiagnosticsPathTemplate(rawPath string) (string, bool) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if route, ok := matchGatewayDiagnosticsRoute(method, rawPath); ok {
			return route.template, true
		}
	}
	return "", false
}

func (h *handler) serveGatewayDiagnostics(w http.ResponseWriter, r *http.Request) bool {
	route, ok := matchGatewayDiagnosticsRoute(r.Method, r.URL.Path)
	if !ok {
		return false
	}
	if routeDecisionFromContext(r.Context()).SelectedOwner != ownershipregistry.OwnerConnectivity || h.connectivity == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "CONNECTIVITY_UNAVAILABLE", "Gateway diagnostics unavailable", "Connectivity is not configured.", true, nil)
		return true
	}

	session, failure := h.identitySession(r)
	if failure != nil {
		writeIdentityFailure(w, r, *failure)
		return true
	}

	action := registryauth.ActionDeviceRead
	rateDimension := limitpolicy.DimensionRest
	if route.writeOp {
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
	if !validConcentratorPathSegment(route.gatewayID) {
		writeProblem(w, r, http.StatusBadRequest, "GATEWAY_DIAGNOSTICS_REQUEST_INVALID", "Gateway diagnostics request invalid", "The gateway diagnostics path is invalid.", false, nil)
		return true
	}

	auth, failureAuth := h.authorizeRegistry(r.Context(), session, action)
	if failureAuth != nil {
		writeProblem(w, r, failureAuth.status, failureAuth.code, failureAuth.title, failureAuth.detail, failureAuth.retryable, nil)
		return true
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(20 * time.Second))

	var reqBody io.Reader
	var hasBody bool
	if route.forwardBody && r.Body != nil {
		bodyBytes, err := readBoundedBody(r.Body, maxGatewayDiagnosticsRequestBodyBytes)
		if err != nil {
			status := http.StatusBadRequest
			code := "GATEWAY_DIAGNOSTICS_REQUEST_INVALID"
			detail := "The gateway diagnostics request body could not be read."
			if errors.Is(err, errRegistryBodyTooLarge) {
				status = http.StatusRequestEntityTooLarge
				code = "PAYLOAD_TOO_LARGE"
				detail = "The gateway diagnostics request body exceeds the maximum size."
			}
			writeProblem(w, r, status, code, "Gateway diagnostics request invalid", detail, false, nil)
			return true
		}
		if len(bodyBytes) > 0 {
			reqBody = bytes.NewReader(bodyBytes)
			hasBody = true
		}
	}

	upstreamURL := h.connectivity.BaseURL + route.upstreamPath
	if route.forwardQuery && r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, reqBody)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "GATEWAY_DIAGNOSTICS_REQUEST_INVALID", "Gateway diagnostics request invalid", "The gateway diagnostics path is invalid.", false, nil)
		return true
	}
	if hasBody {
		upstreamReq.Header.Set("Content-Type", "application/json")
	}
	upstreamReq.Header.Set("X-Delegation-Grant", auth.coreGrant)

	response, err := h.connectivity.HTTPClient.Do(upstreamReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeProblem(w, r, http.StatusGatewayTimeout, "CONNECTIVITY_TIMEOUT", "Gateway diagnostics request timed out", "Connectivity did not answer in time.", true, nil)
			return true
		}
		writeProblem(w, r, http.StatusServiceUnavailable, "CONNECTIVITY_UNAVAILABLE", "Gateway diagnostics unavailable", "Connectivity could not be reached.", true, nil)
		return true
	}
	defer response.Body.Close()

	body, err := readBoundedBody(response.Body, maxGatewayDiagnosticsResponseBodyBytes)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "CONNECTIVITY_RESPONSE_INVALID", "Gateway diagnostics response invalid", "Connectivity returned an invalid response.", true, nil)
		return true
	}

	if response.StatusCode >= http.StatusBadRequest {
		writeUpstreamDiagnosticsProblem(w, r, response.StatusCode, body)
		return true
	}
	if response.StatusCode >= http.StatusMultipleChoices {
		writeProblem(w, r, http.StatusBadGateway, "CONNECTIVITY_RESPONSE_INVALID", "Gateway diagnostics response invalid", "Connectivity returned an unexpected redirect.", true, nil)
		return true
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
	return true
}

func writeUpstreamDiagnosticsProblem(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	var upstream concentratorUpstreamError
	_ = json.Unmarshal(body, &upstream)
	code := strings.TrimSpace(upstream.Code)
	if code == "" {
		code = "GATEWAY_DIAGNOSTICS_UPSTREAM_ERROR"
	}
	detail := "Gateway diagnostics request failed."
	if status >= http.StatusInternalServerError {
		detail = "Gateway diagnostics service is unavailable."
	} else if trimmed := strings.TrimSpace(upstream.Message); trimmed != "" {
		detail = trimmed
	}
	title := http.StatusText(status)
	if title == "" {
		title = "Gateway diagnostics request failed"
	}
	writeProblem(w, r, status, code, title, detail, status >= http.StatusBadGateway, nil)
}
