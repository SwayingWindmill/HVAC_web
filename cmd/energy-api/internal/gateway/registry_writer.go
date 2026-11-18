package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
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
	result := h.executeCoreRegistryMutation(request.Context(), route, request.Method, body, authorization.coreGrant, decision)
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
	targetLabel := firstNonEmpty(
		stringValue(respMap, "displayName"),
		stringValue(reqMap, "displayName"),
		stringValue(respMap, "name"),
		stringValue(reqMap, "name"),
		stringValue(respMap, "templateKey"),
		stringValue(reqMap, "templateKey"),
		stringValue(respMap, "code"),
		stringValue(reqMap, "code"),
		stringValue(reqMap, "pointCode"),
		stringValue(reqMap, "namespace"),
		defaultLabel,
	)
	targetID := firstNonEmpty(
		stringValue(respMap, "id"),
		stringValue(respMap, "bindingId"),
		stringValue(respMap, "resourceId"),
		stringValue(respMap, "sagaId"),
		stringValue(reqMap, "id"),
		stringValue(reqMap, "resourceId"),
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

func (h *handler) executeCoreRegistryMutation(ctx context.Context, route publicRegistryRoute, method string, body []byte, grant string, decision ownershipregistry.Decision) registryBackendResult {
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
	observability.InjectHTTP(ctx, request.Header)
	response, err := h.registry.coreHTTPClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return unavailableRegistryResult(ownershipregistry.OwnerCore, http.StatusGatewayTimeout, "REGISTRY_TIMEOUT")
		}
		return unavailableRegistryResult(ownershipregistry.OwnerCore, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE")
	}
	defer response.Body.Close()
	return h.decodeRegistryBackendResponse(ownershipregistry.OwnerCore, response, route)
}
