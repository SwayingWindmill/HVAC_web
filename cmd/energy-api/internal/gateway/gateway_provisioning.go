package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

const maxProvisioningRequestBodyBytes = 256 << 10

type gatewayProvisioningRoute struct {
	template     string
	upstreamPath string
	action       registryauth.Action
	writeOp      bool
	forwardQuery bool
	forwardBody  bool
	auditAction  string
	targetLabel  string
}

func matchGatewayProvisioningRoute(method, rawPath string) (gatewayProvisioningRoute, bool) {
	parts := strings.Split(rawPath, "/")
	if len(parts) < 5 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "provisioning" || parts[4] != "batches" {
		return gatewayProvisioningRoute{}, false
	}
	switch len(parts) {
	case 5:
		switch method {
		case http.MethodGet:
			return gatewayProvisioningRoute{
				template:     "/api/v1/provisioning/batches",
				upstreamPath: "/internal/v1/provisioning/batches",
				action:       registryauth.ActionDeviceRead,
				forwardQuery: true,
			}, true
		case http.MethodPost:
			return gatewayProvisioningRoute{
				template:     "/api/v1/provisioning/batches",
				upstreamPath: "/internal/v1/provisioning/batches",
				action:       registryauth.ActionDeviceWrite,
				writeOp:      true,
				forwardBody:  true,
				auditAction:  "JITP_BATCH_CREATED",
				targetLabel:  "JITP 预注册批次",
			}, true
		}
	case 6:
		if isValidGatewayPathSegment(parts[5]) && method == http.MethodGet {
			return gatewayProvisioningRoute{
				template:     "/api/v1/provisioning/batches/{batchId}",
				upstreamPath: "/internal/v1/provisioning/batches/" + url.PathEscape(parts[5]),
				action:       registryauth.ActionDeviceRead,
			}, true
		}
	case 7:
		if isValidGatewayPathSegment(parts[5]) && method == http.MethodPost {
			escaped := url.PathEscape(parts[5])
			switch parts[6] {
			case "devices":
				return gatewayProvisioningRoute{
					template:     "/api/v1/provisioning/batches/{batchId}/devices",
					upstreamPath: "/internal/v1/provisioning/batches/" + escaped + "/devices",
					action:       registryauth.ActionDeviceWrite,
					writeOp:      true,
					forwardBody:  true,
					auditAction:  "JITP_BATCH_DEVICES_REGISTERED",
					targetLabel:  "JITP 批次设备登记",
				}, true
			case "revoke":
				return gatewayProvisioningRoute{
					template:     "/api/v1/provisioning/batches/{batchId}/revoke",
					upstreamPath: "/internal/v1/provisioning/batches/" + escaped + "/revoke",
					action:       registryauth.ActionDeviceWrite,
					writeOp:      true,
					auditAction:  "JITP_BATCH_REVOKED",
					targetLabel:  "JITP 批次吊销",
				}, true
			}
		}
	}
	return gatewayProvisioningRoute{}, false
}

func matchGatewayProvisioningPathTemplate(path string) (string, bool) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if route, ok := matchGatewayProvisioningRoute(method, path); ok {
			return route.template, true
		}
	}
	return "", false
}

func (h *handler) serveGatewayProvisioning(w http.ResponseWriter, r *http.Request) bool {
	route, ok := matchGatewayProvisioningRoute(r.Method, r.URL.Path)
	if !ok {
		return false
	}
	if routeDecisionFromContext(r.Context()).SelectedOwner != ownershipregistry.OwnerConnectivity || h.connectivity == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "CONNECTIVITY_UNAVAILABLE", "Provisioning unavailable", "Connectivity is not configured.", true, nil)
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
			h.emitDomainAudit(r.Context(), r, session, buildGatewayProvisioningAuditInput(r.URL.Path, route, nil, nil, auditOutcomeForStatus(failureAuth.status), failureAuth.code))
		}
		writeProblem(w, r, failureAuth.status, failureAuth.code, failureAuth.title, failureAuth.detail, failureAuth.retryable, nil)
		return true
	}

	var bodyReader io.Reader
	var forwardBody []byte
	if route.forwardBody && r.Body != nil {
		var err error
		forwardBody, err = readBoundedBody(r.Body, maxProvisioningRequestBodyBytes)
		if err != nil {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayProvisioningAuditInput(r.URL.Path, route, nil, nil, auditOutcomeRejected, "PROVISIONING_REQUEST_INVALID"))
			writeProblem(w, r, http.StatusBadRequest, "PROVISIONING_REQUEST_INVALID", "Provisioning request invalid", "The provisioning payload could not be read.", false, nil)
			return true
		}
		if len(forwardBody) > 0 {
			bodyReader = bytes.NewReader(forwardBody)
		}
	}

	upstreamURL := h.connectivity.BaseURL + route.upstreamPath
	if route.forwardQuery && r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bodyReader)
	if err != nil {
		if route.writeOp {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayProvisioningAuditInput(r.URL.Path, route, forwardBody, nil, auditOutcomeRejected, "PROVISIONING_REQUEST_INVALID"))
		}
		writeProblem(w, r, http.StatusBadRequest, "PROVISIONING_REQUEST_INVALID", "Provisioning request invalid", "The provisioning path is invalid.", false, nil)
		return true
	}
	if len(forwardBody) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("X-Delegation-Grant", auth.coreGrant)
	observability.InjectHTTP(r.Context(), request.Header)

	response, err := h.connectivity.HTTPClient.Do(request)
	if err != nil {
		if route.writeOp {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayProvisioningAuditInput(r.URL.Path, route, forwardBody, nil, auditOutcomeFailed, "CONNECTIVITY_UNAVAILABLE"))
		}
		writeProblem(w, r, http.StatusServiceUnavailable, "CONNECTIVITY_UNAVAILABLE", "Provisioning unavailable", "Connectivity could not be reached.", true, nil)
		return true
	}
	defer drainAndCloseBody(response.Body)
	body, err := readBoundedBody(response.Body, 1<<20)
	if err != nil {
		if route.writeOp {
			h.emitDomainAudit(r.Context(), r, session, buildGatewayProvisioningAuditInput(r.URL.Path, route, forwardBody, nil, auditOutcomeFailed, "CONNECTIVITY_RESPONSE_INVALID"))
		}
		writeProblem(w, r, http.StatusBadGateway, "CONNECTIVITY_RESPONSE_INVALID", "Provisioning unavailable", "Connectivity returned an invalid response.", true, nil)
		return true
	}
	if route.writeOp {
		h.emitDomainAudit(r.Context(), r, session, buildGatewayProvisioningAuditInput(r.URL.Path, route, forwardBody, body, auditOutcomeForStatus(response.StatusCode), problemCodeFromRaw(body)))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
	return true
}

func buildGatewayProvisioningAuditInput(
	path string,
	route gatewayProvisioningRoute,
	reqBody, respBody []byte,
	outcome, reasonCode string,
) domainAuditInput {
	parts := strings.Split(path, "/")
	batchID := ""
	if len(parts) >= 6 && parts[5] != "" {
		batchID = parts[5]
	}
	summary := map[string]any{
		"credentialKind": "JITP_BATCH",
	}
	if reqMap := decodeFlatJSONObject(reqBody); len(reqMap) > 0 {
		if productKey := stringValue(reqMap, "productKey"); productKey != "" {
			summary["productKey"] = productKey
		}
		if siteID := stringValue(reqMap, "siteId"); siteID != "" {
			summary["siteId"] = siteID
		}
	}
	// Never copy productSecret into domain audit records.
	if batchID == "" && len(respBody) > 0 {
		var created struct {
			ID    string `json:"id"`
			Batch *struct {
				ID string `json:"id"`
			} `json:"batch"`
		}
		if json.Unmarshal(respBody, &created) == nil {
			if created.Batch != nil && strings.TrimSpace(created.Batch.ID) != "" {
				batchID = strings.TrimSpace(created.Batch.ID)
			} else if strings.TrimSpace(created.ID) != "" {
				batchID = strings.TrimSpace(created.ID)
			}
		}
	}
	targetID := "provisioning:batch"
	if batchID != "" {
		targetID = "provisioning:batch:" + batchID
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
