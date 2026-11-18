package gateway

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

type ConnectivityConfig struct {
	BaseURL    string
	HTTPClient *http.Client
}

type gatewayCredentialRoute struct {
	template     string
	upstreamPath string
	action       registryauth.Action
	forwardBody  bool
}

func isValidGatewayPathSegment(segment string) bool {
	return segment != "" && segment != "." && segment != ".." &&
		!strings.ContainsAny(segment, " \t\r\n?#")
}

func matchGatewayCredentialRoute(method, path string) (gatewayCredentialRoute, bool) {
	parts := strings.Split(path, "/")
	if len(parts) < 6 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "gateways" {
		return gatewayCredentialRoute{}, false
	}
	if !isValidGatewayPathSegment(parts[4]) {
		return gatewayCredentialRoute{}, false
	}
	if len(parts) == 6 {
		switch parts[5] {
		case "credential":
			if method == http.MethodGet {
				return gatewayCredentialRoute{
					template:     "/api/v1/gateways/{gatewayId}/credential",
					upstreamPath: "/internal/v1/gateways/" + url.PathEscape(parts[4]) + "/credential",
					action:       registryauth.ActionDeviceRead,
				}, true
			}
		case "credentials":
			if method == http.MethodGet {
				return gatewayCredentialRoute{
					template:     "/api/v1/gateways/{gatewayId}/credentials",
					upstreamPath: "/internal/v1/gateways/" + url.PathEscape(parts[4]) + "/credentials",
					action:       registryauth.ActionDeviceRead,
				}, true
			}
		case "enrollment-code":
			if method == http.MethodPost {
				return gatewayCredentialRoute{
					template:     "/api/v1/gateways/{gatewayId}/enrollment-code",
					upstreamPath: "/internal/v1/gateways/" + url.PathEscape(parts[4]) + "/enrollment-code",
					action:       registryauth.ActionDeviceWrite,
				}, true
			}
		case "revoke":
			if method == http.MethodPost {
				return gatewayCredentialRoute{
					template:     "/api/v1/gateways/{gatewayId}/revoke",
					upstreamPath: "/internal/v1/gateways/" + url.PathEscape(parts[4]) + "/revoke",
					action:       registryauth.ActionDeviceWrite,
				}, true
			}
		case "rotate":
			if method == http.MethodPost {
				return gatewayCredentialRoute{
					template:     "/api/v1/gateways/{gatewayId}/rotate",
					upstreamPath: "/internal/v1/gateways/" + url.PathEscape(parts[4]) + "/rotate",
					action:       registryauth.ActionDeviceWrite,
				}, true
			}
		}
		return gatewayCredentialRoute{}, false
	}
	if len(parts) == 8 && parts[5] == "credentials" && isValidGatewayPathSegment(parts[6]) && parts[7] == "revoke" && method == http.MethodPost {
		return gatewayCredentialRoute{
			template:     "/api/v1/gateways/{gatewayId}/credentials/{credentialId}/revoke",
			upstreamPath: "/internal/v1/gateways/" + url.PathEscape(parts[4]) + "/credentials/" + url.PathEscape(parts[6]) + "/revoke",
			action:       registryauth.ActionDeviceWrite,
			forwardBody:  true,
		}, true
	}
	return gatewayCredentialRoute{}, false
}

func matchGatewayCredentialPathTemplate(path string) (string, bool) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if route, ok := matchGatewayCredentialRoute(method, path); ok {
			return route.template, true
		}
	}
	return "", false
}

func (h *handler) serveGatewayCredentials(w http.ResponseWriter, r *http.Request) bool {
	route, ok := matchGatewayCredentialRoute(r.Method, r.URL.Path)
	if !ok {
		return false
	}
	if routeDecisionFromContext(r.Context()).SelectedOwner != ownershipregistry.OwnerConnectivity || h.connectivity == nil {
		writeProblem(w, r, 503, "CONNECTIVITY_UNAVAILABLE", "Gateway credentials unavailable", "Connectivity is not configured.", true, nil)
		return true
	}
	session, failure := h.identitySession(r)
	if failure != nil {
		writeIdentityFailure(w, r, *failure)
		return true
	}
	if r.Method == http.MethodPost {
		if failure := h.identity.validateStateChange(r, session, r.Header.Get("X-CSRF-Token")); failure != nil {
			writeIdentityFailure(w, r, *failure)
			return true
		}
	}
	auth, failureAuth := h.authorizeRegistry(r.Context(), session, route.action)
	if failureAuth != nil {
		if r.Method == http.MethodPost {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayCredentialAuditInput(r.URL.Path, route, nil, nil, auditOutcomeForStatus(failureAuth.status), failureAuth.code))
		}
		writeProblem(w, r, failureAuth.status, failureAuth.code, failureAuth.title, failureAuth.detail, failureAuth.retryable, nil)
		return true
	}
	var bodyReader io.Reader
	var forwardBody []byte
	if route.forwardBody && r.Body != nil {
		var err error
		forwardBody, err = readBoundedBody(r.Body, 32<<10)
		if err != nil {
			if r.Method == http.MethodPost {
				h.emitDomainAudit(r.Context(), r, session, buildGatewayCredentialAuditInput(r.URL.Path, route, nil, nil, auditOutcomeRejected, "GATEWAY_REQUEST_INVALID"))
			}
			writeProblem(w, r, 400, "GATEWAY_REQUEST_INVALID", "Gateway request invalid", "The Gateway request payload is invalid.", false, nil)
			return true
		}
		if len(forwardBody) > 0 {
			bodyReader = bytes.NewReader(forwardBody)
		}
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, h.connectivity.BaseURL+route.upstreamPath, bodyReader)
	if err != nil {
		if r.Method == http.MethodPost {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayCredentialAuditInput(r.URL.Path, route, forwardBody, nil, auditOutcomeRejected, "GATEWAY_REQUEST_INVALID"))
		}
		writeProblem(w, r, 400, "GATEWAY_REQUEST_INVALID", "Gateway request invalid", "The Gateway path is invalid.", false, nil)
		return true
	}
	if len(forwardBody) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("X-Delegation-Grant", auth.coreGrant)
	response, err := h.connectivity.HTTPClient.Do(request)
	if err != nil {
		if r.Method == http.MethodPost {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayCredentialAuditInput(r.URL.Path, route, forwardBody, nil, auditOutcomeFailed, "CONNECTIVITY_UNAVAILABLE"))
		}
		writeProblem(w, r, 503, "CONNECTIVITY_UNAVAILABLE", "Gateway credentials unavailable", "Connectivity could not be reached.", true, nil)
		return true
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, 64<<10)
	if err != nil {
		if r.Method == http.MethodPost {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayCredentialAuditInput(r.URL.Path, route, forwardBody, nil, auditOutcomeFailed, "CONNECTIVITY_RESPONSE_INVALID"))
		}
		writeProblem(w, r, 502, "CONNECTIVITY_RESPONSE_INVALID", "Gateway credentials unavailable", "Connectivity returned an invalid response.", true, nil)
		return true
	}
	if r.Method == http.MethodPost {
		h.emitDomainAudit(r.Context(), r, session, buildGatewayCredentialAuditInput(r.URL.Path, route, forwardBody, body, auditOutcomeForStatus(response.StatusCode), problemCodeFromRaw(body)))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
	return true
}

func buildGatewayCredentialAuditInput(
	path string,
	route gatewayCredentialRoute,
	reqBody, respBody []byte,
	outcome, reasonCode string,
) domainAuditInput {
	parts := strings.Split(path, "/")
	gatewayID := ""
	credentialID := ""
	if len(parts) >= 5 {
		gatewayID = parts[4]
	}
	if len(parts) >= 7 {
		credentialID = parts[6]
	}
	targetID := "gateway:" + gatewayID
	if credentialID != "" {
		targetID += ":" + credentialID
	}
	action := "GATEWAY_CREDENTIAL_MUTATED"
	targetLabel := "网关接入凭证"
	credentialKind := "CREDENTIAL"
	switch route.template {
	case "/api/v1/gateways/{gatewayId}/enrollment-code":
		action = "GATEWAY_ENROLLMENT_CODE_ISSUED"
		targetLabel = "网关接入授权码"
		credentialKind = "ENROLLMENT_CODE"
	case "/api/v1/gateways/{gatewayId}/revoke":
		action = "GATEWAY_CREDENTIAL_REVOKED"
		targetLabel = "网关接入凭证"
	case "/api/v1/gateways/{gatewayId}/rotate":
		action = "GATEWAY_CREDENTIAL_ROTATED"
		targetLabel = "网关证书轮换"
		credentialKind = "CERTIFICATE"
	case "/api/v1/gateways/{gatewayId}/credentials/{credentialId}/revoke":
		action = "GATEWAY_CERTIFICATE_REVOKED"
		targetLabel = "网关客户端证书"
		credentialKind = "CERTIFICATE"
	}
	summary := map[string]any{
		"credentialKind": credentialKind,
	}
	if reqMap := decodeFlatJSONObject(reqBody); len(reqMap) > 0 {
		if reason := stringValue(reqMap, "reason"); reason != "" {
			summary["reason"] = reason
		}
	}
	if respMap := decodeFlatJSONObject(respBody); len(respMap) > 0 {
		if expiresAt := stringValue(respMap, "expiresAt"); expiresAt != "" {
			summary["expiresAt"] = expiresAt
		}
	}
	if outcome != auditOutcomeSucceeded && reasonCode != "" {
		summary["reasonCode"] = reasonCode
	}
	return domainAuditInput{
		Category:      auditserver.AggregateTypeGatewayCredential,
		TargetID:      targetID,
		TargetLabel:   targetLabel,
		Action:        action,
		Outcome:       outcome,
		ChangeSummary: summary,
	}
}
