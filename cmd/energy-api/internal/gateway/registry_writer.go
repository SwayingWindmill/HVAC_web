package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

func (h *handler) serveRegistryMutation(writer http.ResponseWriter, request *http.Request, route publicRegistryRoute) {
	decision := routeDecisionFromContext(request.Context())
	if decision.RegistryRevision == 0 || h.registry == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry route is not configured.", true, nil)
		return
	}
	if request.URL.RawQuery != "" {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation invalid", "Registry mutation routes do not accept query parameters.", false, nil)
		return
	}
	session, ok := routeSessionFromContext(request.Context())
	if !ok {
		resolved, failure := h.identitySession(request)
		if failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return
		}
		session = resolved
	}
	if h.identity == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "IDENTITY_NOT_CONFIGURED", "Identity unavailable", "Identity is not configured for this Gateway.", true})
		return
	}
	if stateFailure := h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token")); stateFailure != nil {
		writeIdentityFailure(writer, request, *stateFailure)
		return
	}
	authorization, failure := h.authorizeRegistry(request.Context(), session, route.action)
	if failure != nil {
		if failure.status == http.StatusForbidden && (route.resource == "device-replace-hardware" || route.resource == "device-transfer") {
			h.emitDomainAudit(request.Context(), request, session, buildRegistryAuditInput(route, request, nil, nil, auditOutcomeDenied, failure.code))
			title, detail := "Device hardware replacement forbidden", "The authenticated Principal is not allowed to replace Device hardware."
			if route.resource == "device-transfer" {
				title, detail = "Device transfer forbidden", "The authenticated Principal is not allowed to transfer Devices."
			}
			writeProblem(writer, request, http.StatusForbidden, "AUTHORIZATION_DENIED", title, detail, false, nil)
			return
		}
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			h.emitDomainAudit(request.Context(), request, session, buildRegistryAuditInput(route, request, nil, nil, auditOutcomeDenied, failure.code))
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
			return
		}
		h.emitDomainAudit(request.Context(), request, session, buildRegistryAuditInput(route, request, nil, nil, auditOutcomeForStatus(failure.status), failure.code))
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	if route.scopeID != "" {
		if _, allowed := authorization.allowedSiteIDs[route.scopeID]; !allowed {
			h.emitDomainAudit(request.Context(), request, session, buildRegistryAuditInput(route, request, nil, nil, auditOutcomeDenied, "SITE_SCOPE_FORBIDDEN"))
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
			return
		}
	}
	if decision.SelectedOwner != ownershipregistry.OwnerCore {
		h.emitDomainAudit(request.Context(), request, session, buildRegistryAuditInput(route, request, nil, nil, auditOutcomeFailed, "REGISTRY_UNAVAILABLE"))
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry route decision is outside the Core-only boundary.", true, nil)
		return
	}
	body, err := readBoundedBody(request.Body, defaultRegistryBodyLimit)
	if err != nil {
		code := "REGISTRY_MUTATION_INVALID"
		status := http.StatusBadRequest
		if errors.Is(err, errRegistryBodyTooLarge) {
			code, status = "REGISTRY_MUTATION_TOO_LARGE", http.StatusRequestEntityTooLarge
		}
		h.emitDomainAudit(request.Context(), request, session, buildRegistryAuditInput(route, request, nil, nil, auditOutcomeRejected, code))
		writeProblem(writer, request, status, code, "Registry mutation invalid", "The Registry mutation payload is unreadable or exceeds the supported size.", false, nil)
		return
	}
	var extraHeaders map[string]string
	if route.resource == "device-transfer" {
		headers, ok := h.authorizeDeviceTransferTarget(writer, request, session, route, body, authorization)
		if !ok {
			return
		}
		extraHeaders = headers
	}
	result := h.executeCoreRegistryMutationWithHeaders(request.Context(), route, request.Method, body, authorization.coreGrant, extraHeaders, decision)
	if route.resource == "device-transfer" && (result.status == http.StatusOK || result.status == http.StatusCreated) {
		result = h.enrichTransferredGatewayDecommission(request.Context(), session.TenantID, authorization.principalID, result)
	}
	outcome := registryResultOutcome(result)
	h.emitDomainAudit(request.Context(), request, session, buildRegistryAuditInput(route, request, body, result.body, outcome, result.code))
	writer.Header().Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	h.writeRegistryBackendResult(writer, request, result)
}

func registryResultOutcome(result registryBackendResult) string {
	if result.status == http.StatusOK || result.status == http.StatusCreated {
		return auditOutcomeSucceeded
	}
	if result.code == "AUTHORIZATION_DENIED" {
		return auditOutcomeDenied
	}
	return auditOutcomeForStatus(result.status)
}

func buildRegistryAuditInput(
	route publicRegistryRoute,
	request *http.Request,
	reqBody, respBody []byte,
	outcome, reasonCode string,
) domainAuditInput {
	action, defaultLabel := registryAuditMeta(route.resource, request.Method)
	reqMap := decodeFlatJSONObject(reqBody)
	respMap := decodeFlatJSONObject(respBody)
	deviceMap, _ := respMap["device"].(map[string]any)
	targetLabel := firstNonEmpty(
		stringValue(respMap, "displayName"),
		stringValue(deviceMap, "displayName"),
		stringValue(reqMap, "displayName"),
		stringValue(respMap, "name"),
		stringValue(reqMap, "name"),
		stringValue(respMap, "templateKey"),
		stringValue(reqMap, "templateKey"),
		stringValue(respMap, "code"),
		stringValue(deviceMap, "code"),
		stringValue(reqMap, "code"),
		stringValue(reqMap, "pointCode"),
		stringValue(reqMap, "namespace"),
		defaultLabel,
	)
	targetID := firstNonEmpty(
		stringValue(respMap, "transferId"),
		stringValue(respMap, "id"),
		stringValue(respMap, "deviceId"),
		stringValue(respMap, "targetDeviceId"),
		stringValue(respMap, "bindingId"),
		stringValue(respMap, "resourceId"),
		stringValue(respMap, "sagaId"),
		stringValue(reqMap, "id"),
		stringValue(reqMap, "resourceId"),
		routeDeviceID(route),
		route.scopeID,
		route.internalPath,
	)
	summary := map[string]any{
		"resourceKind": route.resource,
		"method":       request.Method,
	}
	if reason := firstNonEmpty(nestedStringValue(reqMap, "meta", "reason"), stringValue(reqMap, "reason")); reason != "" {
		summary["reason"] = reason
	}
	if route.resource == "device-replace-hardware" {
		for _, key := range []string{"replacementId", "previousExternalId", "newExternalId", "previousGatewayDeviceId", "newGatewayDeviceId"} {
			if value := stringValue(respMap, key); value != "" {
				summary[key] = value
			}
		}
	}
	if route.resource == "device-transfer" {
		for _, key := range []string{"transferId", "transferScope", "sourceDeviceId", "targetTenantId", "targetSiteId", "targetDeviceId", "factoryResetId", "factoryResetStatus"} {
			if value := firstNonEmpty(stringValue(respMap, key), stringValue(reqMap, key)); value != "" {
				summary[key] = value
			}
		}
		if revoked, ok := respMap["oldCredentialsRevoked"].(bool); ok {
			summary["oldCredentialsRevoked"] = revoked
		}
	}
	if changed := changedJSONKeys(reqMap); len(changed) > 0 {
		summary["changedFields"] = changed
	}
	if outcome != auditOutcomeSucceeded && reasonCode != "" {
		summary["reasonCode"] = reasonCode
	}
	return domainAuditInput{
		Category:      auditserver.AggregateTypeRegistryMutation,
		TargetID:      targetID,
		TargetLabel:   targetLabel,
		Action:        action,
		Outcome:       outcome,
		ChangeSummary: summary,
	}
}

func routeDeviceID(route publicRegistryRoute) string {
	if route.resource != "device-replace-hardware" && route.resource != "device-transfer" {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(route.internalPath, "/internal/v1/registry/"), "/")
	if len(parts) == 5 && parts[2] == "devices" {
		return parts[3]
	}
	return ""
}

func registryAuditMeta(resource, _ string) (string, string) {
	switch resource {
	case "site-write":
		return "REGISTRY_SITE_MUTATED", "站点档案"
	case "space-write":
		return "REGISTRY_SPACE_MUTATED", "空间区域"
	case "asset-write":
		return "REGISTRY_ASSET_MUTATED", "设备档案"
	case "device-write":
		return "REGISTRY_DEVICE_MUTATED", "采集设备"
	case "device-replace-hardware":
		return "REGISTRY_DEVICE_HARDWARE_REPLACED", "采集设备硬件"
	case "device-transfer":
		return "REGISTRY_DEVICE_TRANSFERRED", "设备归属过户"
	case "sensor-write":
		return "REGISTRY_SENSOR_MUTATED", "传感器"
	case "point-write":
		return "REGISTRY_POINT_MUTATED", "监控点位"
	case "binding-write":
		return "REGISTRY_BINDING_UPDATED", "设备绑定关系"
	case "template-release":
		return "REGISTRY_TEMPLATE_RELEASED", "点位模板版本"
	case "template-assign":
		return "REGISTRY_TEMPLATE_ASSIGNED", "模板下发配置"
	case "import-plan":
		return "REGISTRY_IMPORT_DRY_RUN", "设备登记预检"
	case "import-commit":
		return "REGISTRY_IMPORT_COMMITTED", "设备批量导入"
	case "retire":
		return "REGISTRY_RESOURCE_RETIRED", "对象退役"
	default:
		return "REGISTRY_MUTATED", "设备登记变更"
	}
}

func decodeFlatJSONObject(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	return obj
}

func stringValue(obj map[string]any, key string) string {
	if len(obj) == 0 {
		return ""
	}
	value, ok := obj[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func nestedStringValue(obj map[string]any, outer, inner string) string {
	if len(obj) == 0 {
		return ""
	}
	nested, ok := obj[outer].(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(nested, inner)
}

func changedJSONKeys(obj map[string]any) []string {
	if len(obj) == 0 {
		return nil
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		switch key {
		case "expectedRevision", "idempotencyKey", "meta", "siteId", "tenantId", "id":
			continue
		default:
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 12 {
		return keys[:12]
	}
	return keys
}

func (h *handler) executeCoreRegistryMutationWithHeaders(
	ctx context.Context,
	route publicRegistryRoute,
	method string,
	body []byte,
	grant string,
	extraHeaders map[string]string,
	decision ownershipregistry.Decision,
) registryBackendResult {
	if h.registry == nil || h.registry.coreBaseURL == "" || h.registry.coreHTTPClient == nil {
		return unavailableRegistryResult(ownershipregistry.OwnerCore, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE")
	}
	requestContext, cancel := context.WithTimeout(ctx, h.registry.coreTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, method, h.registry.coreBaseURL+route.internalPath, bytes.NewReader(body))
	if err != nil {
		return unavailableRegistryResult(ownershipregistry.OwnerCore, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-Delegation-Grant", grant)
	request.Header.Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	request.Header.Set("X-Request-ID", requestIDFromContext(ctx))
	for k, v := range extraHeaders {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			request.Header.Set(k, v)
		}
	}
	observability.InjectHTTP(ctx, request.Header)
	response, err := h.registry.coreHTTPClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return unavailableRegistryResult(ownershipregistry.OwnerCore, http.StatusGatewayTimeout, "REGISTRY_TIMEOUT")
		}
		return unavailableRegistryResult(ownershipregistry.OwnerCore, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE")
	}
	defer drainAndCloseBody(response.Body)
	return h.decodeRegistryBackendResponse(ownershipregistry.OwnerCore, response, route)
}

func (h *handler) authorizeDeviceTransferTarget(
	writer http.ResponseWriter,
	request *http.Request,
	session bffSession,
	route publicRegistryRoute,
	body []byte,
	sourceAuth registryAuthorization,
) (map[string]string, bool) {
	reqMap := decodeFlatJSONObject(body)
	targetTenantID := stringValue(reqMap, "targetTenantId")
	targetSiteID := stringValue(reqMap, "targetSiteId")
	if targetTenantID != "" && targetTenantID != session.TenantID {
		if !isLowerUUIDv7(targetTenantID) {
			return nil, true
		}
		presenterSPIFFE := ""
		if h.identity != nil {
			presenterSPIFFE = h.identity.config.ExecutingWorkloadSPIFFE
		}
		targetAuth, failure := h.authorizeRegistryForTenant(request.Context(), session, targetTenantID, registryauth.ActionDeviceWrite, presenterSPIFFE)
		if failure != nil {
			outcome := auditOutcomeForStatus(failure.status)
			reasonCode := failure.code
			if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
				outcome = auditOutcomeDenied
				reasonCode = "REGISTRY_TRANSFER_FORBIDDEN"
			}
			h.emitDomainAudit(request.Context(), request, session, buildRegistryAuditInput(route, request, body, nil, outcome, reasonCode))
			if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
				writeProblem(writer, request, http.StatusForbidden, "REGISTRY_TRANSFER_FORBIDDEN", "Cross-tenant device transfer forbidden", "The authenticated Principal is not authorized to transfer a Device into the destination tenant.", false, nil)
				return nil, false
			}
			writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
			return nil, false
		}
		if targetSiteID != "" && isLowerUUIDv7(targetSiteID) {
			if _, allowed := targetAuth.allowedSiteIDs[targetSiteID]; !allowed {
				h.emitDomainAudit(request.Context(), request, session, buildRegistryAuditInput(route, request, body, nil, auditOutcomeDenied, "REGISTRY_TRANSFER_FORBIDDEN"))
				writeProblem(writer, request, http.StatusForbidden, "REGISTRY_TRANSFER_FORBIDDEN", "Cross-tenant device transfer forbidden", "The authenticated Principal is not allowed to transfer a Device into the destination Site.", false, nil)
				return nil, false
			}
		}
		return map[string]string{"X-Target-Delegation-Grant": targetAuth.coreGrant}, true
	}
	if targetSiteID != "" && isLowerUUIDv7(targetSiteID) {
		if _, allowed := sourceAuth.allowedSiteIDs[targetSiteID]; !allowed {
			h.emitDomainAudit(request.Context(), request, session, buildRegistryAuditInput(route, request, body, nil, auditOutcomeDenied, "REGISTRY_TRANSFER_FORBIDDEN"))
			writeProblem(writer, request, http.StatusForbidden, "REGISTRY_TRANSFER_FORBIDDEN", "Device transfer forbidden", "The authenticated Principal is not allowed to transfer a Device to the destination Site.", false, nil)
			return nil, false
		}
	}
	return nil, true
}

func (h *handler) enrichTransferredGatewayDecommission(ctx context.Context, fallbackTenantID, actorID string, result registryBackendResult) registryBackendResult {
	if h.connectivity == nil || strings.TrimSpace(h.connectivity.BaseURL) == "" || h.connectivity.HTTPClient == nil {
		return result
	}
	var resp map[string]any
	if json.Unmarshal(result.body, &resp) != nil || len(resp) == 0 {
		return result
	}
	sourceDevice, _ := resp["sourceDevice"].(map[string]any)
	deviceType := strings.ToUpper(stringValue(sourceDevice, "deviceType"))
	connectivityMode := strings.ToUpper(stringValue(sourceDevice, "connectivityMode"))
	if deviceType != "GATEWAY" && connectivityMode != "DIRECT" {
		return result
	}
	sourceDeviceID := stringValue(resp, "sourceDeviceId")
	transferID := stringValue(resp, "transferId")
	if !isLowerUUIDv7(sourceDeviceID) || !isLowerUUIDv7(transferID) {
		return result
	}
	tenantID := firstNonEmpty(stringValue(resp, "sourceTenantId"), fallbackTenantID)
	wipeRequired := true
	if val, ok := resp["wipeRequired"].(bool); ok {
		wipeRequired = val
	}
	payloadBytes, err := json.Marshal(map[string]any{
		"tenantId":     tenantID,
		"transferId":   transferID,
		"wipeRequired": wipeRequired,
		"reason":       firstNonEmpty(stringValue(resp, "reason"), "ownership_transfer:"+transferID),
		"actorId":      actorID,
	})
	if err != nil {
		return result
	}
	decommCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	endpoint := strings.TrimRight(h.connectivity.BaseURL, "/") + "/internal/v1/gateways/" + url.PathEscape(sourceDeviceID) + ":transfer-decommission"
	req, err := http.NewRequestWithContext(decommCtx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Request-ID", requestIDFromContext(ctx))
	observability.InjectHTTP(decommCtx, req.Header)
	response, err := h.connectivity.HTTPClient.Do(req)
	if err != nil {
		return result
	}
	defer drainAndCloseBody(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result
	}
	decommRaw, err := readBoundedBody(response.Body, 32<<10)
	if err != nil {
		return result
	}
	var decommResp map[string]any
	if json.Unmarshal(decommRaw, &decommResp) != nil {
		return result
	}
	if revoked, ok := decommResp["credentialsRevoked"].(bool); ok {
		resp["oldCredentialsRevoked"] = revoked
	}
	if resetMap, ok := decommResp["factoryReset"].(map[string]any); ok {
		if resetID := stringValue(resetMap, "id"); resetID != "" {
			resp["factoryResetId"] = resetID
		}
		if resetStatus := stringValue(resetMap, "status"); resetStatus != "" {
			resp["factoryResetStatus"] = resetStatus
		}
	}
	enriched, err := json.Marshal(resp)
	if err != nil {
		return result
	}
	result.body = enriched
	return result
}
