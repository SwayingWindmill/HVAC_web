package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

type upstreamEdgeModelErr struct {
	Error  string `json:"error"`
	Detail string `json:"detail"`
}

type edgeModelDeploySyncPayload struct {
	Deployment            json.RawMessage `json:"deployment"`
	GatewayID             string          `json:"gatewayId,omitempty"`
	DesiredBundles        json.RawMessage `json:"desiredBundles"`
	ShadowSyncStatus      string          `json:"shadowSyncStatus,omitempty"`
	ShadowDesiredRevision *int64          `json:"shadowDesiredRevision,omitempty"`
	ShadowSyncError       string          `json:"shadowSyncError,omitempty"`
}

type edgeModelRollbackSyncPayload struct {
	Rollback              json.RawMessage `json:"rollback"`
	RestoredDeployment    json.RawMessage `json:"restoredDeployment,omitempty"`
	GatewayID             string          `json:"gatewayId,omitempty"`
	DesiredBundles        json.RawMessage `json:"desiredBundles"`
	ShadowSyncStatus      string          `json:"shadowSyncStatus,omitempty"`
	ShadowDesiredRevision *int64          `json:"shadowDesiredRevision,omitempty"`
	ShadowSyncError       string          `json:"shadowSyncError,omitempty"`
}

type edgeModelShadowUpdateBody struct {
	EdgeModels json.RawMessage `json:"edgeModels"`
	Reason     string          `json:"reason"`
}

type edgeModelShadowResponseDesired struct {
	Desired *struct {
		DesiredRevision int64 `json:"desiredRevision"`
	} `json:"desired"`
}

func matchEdgeModelIntelligenceRoute(method, rawPath string) (publicIntelligenceRoute, bool) {
	trimmed, ok := strings.CutPrefix(rawPath, "/api/v1/sites/")
	if !ok || strings.HasSuffix(trimmed, "/") {
		return publicIntelligenceRoute{}, false
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || parts[1] != "edge-models" {
		return publicIntelligenceRoute{}, false
	}
	siteID, err := url.PathUnescape(parts[0])
	if err != nil || siteID == "" {
		return publicIntelligenceRoute{}, false
	}
	switch len(parts) {
	case 2:
		if method == http.MethodGet {
			return publicIntelligenceRoute{
				owner: ownershipregistry.OwnerOptimization, siteID: siteID,
				target: "edge-models-workspace", method: http.MethodGet,
				publicPath: "/api/v1/sites/{siteId}/edge-models",
			}, true
		}
	case 3:
		if method != http.MethodPost {
			return publicIntelligenceRoute{}, false
		}
		switch parts[2] {
		case "artifacts":
			return publicIntelligenceRoute{
				owner: ownershipregistry.OwnerOptimization, siteID: siteID,
				target: "edge-models-artifacts-create", method: http.MethodPost,
				publicPath: "/api/v1/sites/{siteId}/edge-models/artifacts",
			}, true
		case "deployments":
			return publicIntelligenceRoute{
				owner: ownershipregistry.OwnerOptimization, siteID: siteID,
				target: "edge-models-deploy", method: http.MethodPost,
				publicPath: "/api/v1/sites/{siteId}/edge-models/deployments",
			}, true
		case "evaluations":
			return publicIntelligenceRoute{
				owner: ownershipregistry.OwnerOptimization, siteID: siteID,
				target: "edge-models-eval", method: http.MethodPost,
				publicPath: "/api/v1/sites/{siteId}/edge-models/evaluations",
			}, true
		}
	case 5:
		if method != http.MethodPost {
			return publicIntelligenceRoute{}, false
		}
		resID, err := url.PathUnescape(parts[3])
		if err != nil || resID == "" {
			return publicIntelligenceRoute{}, false
		}
		if parts[2] == "artifacts" && parts[4] == "approve" {
			return publicIntelligenceRoute{
				owner: ownershipregistry.OwnerOptimization, siteID: siteID, resourceID: resID,
				target: "edge-models-artifact-approve", method: http.MethodPost,
				publicPath: "/api/v1/sites/{siteId}/edge-models/artifacts/{artifactId}/approve",
			}, true
		}
		if parts[2] == "deployments" && parts[4] == "rollback" {
			return publicIntelligenceRoute{
				owner: ownershipregistry.OwnerOptimization, siteID: siteID, resourceID: resID,
				target: "edge-models-rollback", method: http.MethodPost,
				publicPath: "/api/v1/sites/{siteId}/edge-models/deployments/{deploymentId}/rollback",
			}, true
		}
	}
	return publicIntelligenceRoute{}, false
}

func matchEdgeModelPathTemplate(rawPath string) (string, bool) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if route, ok := matchEdgeModelIntelligenceRoute(method, rawPath); ok {
			return route.publicPath, true
		}
	}
	return "", false
}

func (h *handler) serveEdgeModelRoute(writer http.ResponseWriter, request *http.Request, session bffSession, route publicIntelligenceRoute) {
	if h.intelligence.optimizationBaseURL == "" {
		writeRouteWithoutOwner(writer, request)
		return
	}
	if !isLowerUUIDv7(route.siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	if route.target == "edge-models-workspace" {
		allowedSites, ok := h.authorizeIntelligenceSitesWithWriter(writer, request, session)
		if !ok {
			return
		}
		if _, allowed := allowedSites[route.siteID]; !allowed {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
			return
		}
		internalPath := "/v1/sites/" + url.PathEscape(route.siteID) + "/edge-models"
		respRaw, status, callErr := h.executeOwner(request, session, optimizationOwner, h.intelligence.optimizationBaseURL, route.siteID, optimizationReadAction, http.MethodGet, internalPath, nil)
		h.writeEdgeModelResult(writer, request, respRaw, status, callErr)
		return
	}

	if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
		return
	}
	if _, ok := h.authorizeSiteControlExitWrite(writer, request, session, route.siteID, "Schedule manage authorization is required to manage edge AI model bundles."); !ok {
		h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
			Category: auditserver.AggregateTypeRegistryMutation, TargetID: route.siteID, TargetLabel: route.publicPath,
			Action: route.target, Outcome: auditOutcomeDenied,
			ChangeSummary: map[string]any{"code": "CONTROL_EXIT_FORBIDDEN"},
		})
		return
	}
	if route.resourceID != "" && !isLowerUUIDv7(route.resourceID) {
		writeProblem(writer, request, http.StatusBadRequest, "EDGE_MODEL_REQUEST_INVALID", "Edge model request invalid", "The requested resource identifier is invalid.", false, nil)
		return
	}
	rawBody, err := readBoundedBody(request.Body, maximumIntelligenceRequestBytes)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "EDGE_MODEL_REQUEST_INVALID", "Edge model request invalid", "The edge model request body is invalid or too large.", false, nil)
		return
	}

	internalPath := ""
	switch route.target {
	case "edge-models-artifacts-create":
		internalPath = "/v1/sites/" + url.PathEscape(route.siteID) + "/edge-models/artifacts"
	case "edge-models-artifact-approve":
		internalPath = "/v1/sites/" + url.PathEscape(route.siteID) + "/edge-models/artifacts/" + url.PathEscape(route.resourceID) + "/approve"
	case "edge-models-deploy":
		internalPath = "/v1/sites/" + url.PathEscape(route.siteID) + "/edge-models/deployments"
	case "edge-models-rollback":
		internalPath = "/v1/sites/" + url.PathEscape(route.siteID) + "/edge-models/deployments/" + url.PathEscape(route.resourceID) + "/rollback"
	case "edge-models-eval":
		internalPath = "/v1/sites/" + url.PathEscape(route.siteID) + "/edge-models/evaluations"
	}

	respRaw, status, callErr := h.executeOwner(request, session, optimizationOwner, h.intelligence.optimizationBaseURL, route.siteID, optimizationWriteAction, http.MethodPost, internalPath, rawBody)
	if callErr != nil || status < 200 || status >= 300 {
		h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
			Category: auditserver.AggregateTypeRegistryMutation, TargetID: route.siteID, TargetLabel: route.publicPath,
			Action: route.target, Outcome: auditOutcomeRejected,
			ChangeSummary: map[string]any{"upstreamStatus": status},
		})
		h.writeEdgeModelResult(writer, request, respRaw, status, callErr)
		return
	}

	finalBody := respRaw
	switch route.target {
	case "edge-models-deploy":
		var out edgeModelDeploySyncPayload
		if err := json.Unmarshal(respRaw, &out); err == nil {
			out.ShadowSyncStatus, out.ShadowDesiredRevision, out.ShadowSyncError = h.syncEdgeModelsShadow(request.Context(), request, session, out.GatewayID, out.DesiredBundles, "deploy edge model bundle")
			if reencoded, encErr := json.Marshal(out); encErr == nil {
				finalBody = reencoded
			}
		}
	case "edge-models-rollback":
		var out edgeModelRollbackSyncPayload
		if err := json.Unmarshal(respRaw, &out); err == nil {
			out.ShadowSyncStatus, out.ShadowDesiredRevision, out.ShadowSyncError = h.syncEdgeModelsShadow(request.Context(), request, session, out.GatewayID, out.DesiredBundles, "rollback edge model bundle")
			if reencoded, encErr := json.Marshal(out); encErr == nil {
				finalBody = reencoded
			}
		}
	}

	h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
		Category: auditserver.AggregateTypeRegistryMutation, TargetID: route.siteID, TargetLabel: route.publicPath,
		Action: route.target, Outcome: auditOutcomeSucceeded,
	})
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(finalBody)
}

func (h *handler) syncEdgeModelsShadow(ctx context.Context, request *http.Request, session bffSession, gatewayID string, desiredBundles json.RawMessage, reason string) (string, *int64, string) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || h.connectivity == nil {
		return "SKIPPED", nil, ""
	}
	auth, failureAuth := h.authorizeRegistry(ctx, session, registryauth.ActionDeviceWrite)
	if failureAuth != nil {
		return "FAILED", nil, failureAuth.code
	}
	bundles := desiredBundles
	if len(bytes.TrimSpace(bundles)) == 0 || bytes.Equal(bytes.TrimSpace(bundles), []byte("null")) {
		bundles = json.RawMessage("[]")
	}
	bodyBytes, err := json.Marshal(edgeModelShadowUpdateBody{
		EdgeModels: bundles,
		Reason:     reason,
	})
	if err != nil {
		return "FAILED", nil, "EDGE_MODEL_SHADOW_ENCODE_FAILED"
	}
	upstreamURL := h.connectivity.BaseURL + "/internal/v1/gateways/" + url.PathEscape(gwID) + "/config-shadow"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "FAILED", nil, "EDGE_MODEL_SHADOW_URL_INVALID"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Delegation-Grant", auth.coreGrant)
	observability.InjectHTTP(request.Context(), req.Header)
	resp, err := h.connectivity.HTTPClient.Do(req)
	if err != nil {
		return "FAILED", nil, "CONNECTIVITY_UNAVAILABLE"
	}
	defer drainAndCloseBody(resp.Body)
	respRaw, err := readBoundedBody(resp.Body, maxGatewayConfigShadowResponseBodyBytes)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "FAILED", nil, "CONNECTIVITY_SHADOW_REJECTED"
	}
	var parsed edgeModelShadowResponseDesired
	if json.Unmarshal(respRaw, &parsed) == nil && parsed.Desired != nil {
		rev := parsed.Desired.DesiredRevision
		return "SYNCED", &rev, ""
	}
	return "SYNCED", nil, ""
}

func (h *handler) writeEdgeModelResult(writer http.ResponseWriter, request *http.Request, raw []byte, status int, callErr error) {
	if callErr != nil {
		writeProblem(writer, request, http.StatusBadGateway, "INTELLIGENCE_UPSTREAM_UNAVAILABLE", "Intelligence unavailable", "The Optimization owner is temporarily unavailable.", true, nil)
		return
	}
	if status >= 200 && status < 300 {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write(raw)
		return
	}
	var upErr upstreamEdgeModelErr
	_ = json.Unmarshal(raw, &upErr)
	detail := strings.TrimSpace(upErr.Detail)
	code := strings.ToUpper(strings.TrimSpace(upErr.Error))
	switch status {
	case http.StatusBadRequest:
		if detail == "" {
			detail = "The edge model request payload is invalid."
		}
		writeProblem(writer, request, http.StatusBadRequest, "EDGE_MODEL_REQUEST_INVALID", "Edge model request invalid", detail, false, nil)
	case http.StatusNotFound:
		if detail == "" {
			detail = "The requested edge model record or site was not found."
		}
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", detail, false, nil)
	case http.StatusConflict:
		pCode := "EDGE_MODEL_CONFLICT"
		switch code {
		case "EDGE_MODEL_APPROVAL_REQUIRED":
			pCode = "EDGE_MODEL_APPROVAL_REQUIRED"
		case "EDGE_MODEL_ROLLOUT_CONFLICT":
			pCode = "EDGE_MODEL_ROLLOUT_CONFLICT"
		}
		if detail == "" {
			detail = "Edge model workflow state conflict."
		}
		writeProblem(writer, request, http.StatusConflict, pCode, "Edge model workflow conflict", detail, false, nil)
	case http.StatusUnprocessableEntity:
		if detail == "" {
			detail = "Edge model hyperparameters violate safety or physical bounds."
		}
		writeProblem(writer, request, http.StatusUnprocessableEntity, "EDGE_MODEL_VALIDATION_FAILED", "Edge model validation failed", detail, false, nil)
	default:
		_ = errors.New("edge model upstream failure")
		writeProblem(writer, request, http.StatusBadGateway, "INTELLIGENCE_UPSTREAM_REJECTED", "Edge model request rejected", "The Optimization owner rejected the request.", status >= 500, nil)
	}
}
