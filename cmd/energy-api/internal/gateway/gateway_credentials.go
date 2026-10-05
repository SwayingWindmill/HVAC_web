package gateway

import (
	"net/http"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type ConnectivityConfig struct {
	BaseURL    string
	HTTPClient *http.Client
}

func (h *handler) serveGatewayCredentials(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 6 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "gateways" {
		return false
	}
	operation := parts[5]
	action := registryauth.ActionDeviceWrite
	if operation == "credential" && r.Method == http.MethodGet {
		action = registryauth.ActionDeviceRead
	} else if r.Method != http.MethodPost || (operation != "enrollment-code" && operation != "revoke") {
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
	auth, failureAuth := h.authorizeRegistry(r.Context(), session, action)
	if failureAuth != nil {
		writeProblem(w, r, failureAuth.status, failureAuth.code, failureAuth.title, failureAuth.detail, failureAuth.retryable, nil)
		return true
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, h.connectivity.BaseURL+"/internal/v1/gateways/"+parts[4]+"/"+operation, nil)
	if err != nil {
		writeProblem(w, r, 400, "GATEWAY_REQUEST_INVALID", "Gateway request invalid", "The Gateway path is invalid.", false, nil)
		return true
	}
	request.Header.Set("X-Delegation-Grant", auth.coreGrant)
	response, err := h.connectivity.HTTPClient.Do(request)
	if err != nil {
		writeProblem(w, r, 503, "CONNECTIVITY_UNAVAILABLE", "Gateway credentials unavailable", "Connectivity could not be reached.", true, nil)
		return true
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, 32<<10)
	if err != nil {
		writeProblem(w, r, 502, "CONNECTIVITY_RESPONSE_INVALID", "Gateway credentials unavailable", "Connectivity returned an invalid response.", true, nil)
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
	return true
}
