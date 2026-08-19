package gateway

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
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
	authorization, failure := h.authorizeRegistry(request.Context(), session, route.action)
	if failure != nil {
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
			return
		}
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	if route.scopeID != "" {
		if _, allowed := authorization.allowedSiteIDs[route.scopeID]; !allowed {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Registry resource was not found.", false, nil)
			return
		}
	}
	if decision.SelectedOwner != ownershipregistry.OwnerCore {
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
		writeProblem(writer, request, status, code, "Registry mutation invalid", "The Registry mutation payload is unreadable or exceeds the supported size.", false, nil)
		return
	}
	result := h.executeCoreRegistryMutation(request.Context(), route, request.Method, body, authorization.coreGrant, decision)
	writer.Header().Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	h.writeRegistryBackendResult(writer, request, result)
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
