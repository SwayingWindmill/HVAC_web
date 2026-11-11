package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type publicElectricityBillingRoute struct {
	template     string
	siteID       string
	billID       string
	kind         string
	internalPath string
	write        bool
}

var electricityBillingBackendMessages = siteRegistryBackendMessages{
	badRequestTitle:         "Electricity billing request invalid",
	defaultBadRequestDetail: "The electricity billing request is invalid.",
	forbiddenTitle:          "Electricity billing action forbidden",
	defaultForbiddenCode:    "BILLING_MANAGE_FORBIDDEN",
	defaultForbiddenDetail:  "The authenticated Principal is not allowed to manage electricity billing on this Site.",
	notFoundDetail:          "The requested Site or utility bill was not found.",
	conflictTitle:           "Electricity billing conflict",
	defaultConflictDetail:   "The electricity billing mutation conflicts with the current authoritative state.",
	gatewayTimeoutDetail:    "The electricity billing request did not complete before the Gateway deadline.",
	upstreamTimeoutDetail:   "The electricity billing request timed out.",
}

func matchPublicElectricityBillingRoute(method, path string) (publicElectricityBillingRoute, bool) {
	remainder, ok := strings.CutPrefix(path, "/api/v1/sites/")
	if !ok || strings.HasSuffix(remainder, "/") {
		return publicElectricityBillingRoute{}, false
	}
	segments := strings.Split(remainder, "/")
	if len(segments) < 2 {
		return publicElectricityBillingRoute{}, false
	}
	siteID, err := url.PathUnescape(segments[0])
	if err != nil || siteID == "" {
		return publicElectricityBillingRoute{}, false
	}
	switch {
	case len(segments) == 2 && segments[1] == "electricity-budgets":
		switch method {
		case http.MethodGet:
			return publicElectricityBillingRoute{
				template:     "/api/v1/sites/{siteId}/electricity-budgets",
				siteID:       siteID,
				kind:         "budgets-list",
				internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/electricity-budgets",
			}, true
		case http.MethodPut:
			return publicElectricityBillingRoute{
				template:     "/api/v1/sites/{siteId}/electricity-budgets",
				siteID:       siteID,
				kind:         "budgets-put",
				internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/electricity-budgets",
				write:        true,
			}, true
		}
	case len(segments) == 2 && segments[1] == "utility-bills":
		switch method {
		case http.MethodGet:
			return publicElectricityBillingRoute{
				template:     "/api/v1/sites/{siteId}/utility-bills",
				siteID:       siteID,
				kind:         "bills-list",
				internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/utility-bills",
			}, true
		case http.MethodPost:
			return publicElectricityBillingRoute{
				template:     "/api/v1/sites/{siteId}/utility-bills",
				siteID:       siteID,
				kind:         "bills-create",
				internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/utility-bills",
				write:        true,
			}, true
		}
	case len(segments) == 3 && segments[1] == "utility-bills" && method == http.MethodPut:
		billID, err := url.PathUnescape(segments[2])
		if err != nil || billID == "" {
			return publicElectricityBillingRoute{}, false
		}
		return publicElectricityBillingRoute{
			template:     "/api/v1/sites/{siteId}/utility-bills/{billId}",
			siteID:       siteID,
			billID:       billID,
			kind:         "bills-update",
			internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/utility-bills/" + url.PathEscape(billID),
			write:        true,
		}, true
	case len(segments) == 4 && segments[1] == "utility-bills" && segments[3] == "confirm" && method == http.MethodPost:
		billID, err := url.PathUnescape(segments[2])
		if err != nil || billID == "" {
			return publicElectricityBillingRoute{}, false
		}
		return publicElectricityBillingRoute{
			template:     "/api/v1/sites/{siteId}/utility-bills/{billId}/confirm",
			siteID:       siteID,
			billID:       billID,
			kind:         "bills-confirm",
			internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/utility-bills/" + url.PathEscape(billID) + "/confirm",
			write:        true,
		}, true
	}
	return publicElectricityBillingRoute{}, false
}

func dispatchElectricityBillingRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicElectricityBillingRoute) {
	decision := routeDecisionFromContext(request.Context())
	if decision.RegistryRevision == 0 || decision.SelectedOwner != ownershipregistry.OwnerCore || h.registry == nil || h.registry.coreBaseURL == "" || h.registry.coreHTTPClient == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The electricity billing route is not active for this Session.", true, nil)
		return
	}
	if !isLowerUUIDv7(route.siteID) || (route.billID != "" && !isLowerUUIDv7(route.billID)) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site or utility bill was not found.", false, nil)
		return
	}
	if route.write && request.URL.RawQuery != "" {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation invalid", "Electricity billing mutation routes do not accept query parameters.", false, nil)
		return
	}

	session, ok := h.intelligenceSession(writer, request, route.write)
	if !ok {
		return
	}
	readAuth, readFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if readFailure != nil {
		if readFailure.status == http.StatusForbidden || readFailure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
			return
		}
		writeProblem(writer, request, readFailure.status, readFailure.code, readFailure.title, readFailure.detail, readFailure.retryable, nil)
		return
	}
	if _, visible := readAuth.allowedSiteIDs[route.siteID]; !visible {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}

	grant := readAuth.coreGrant
	var body []byte
	if route.write {
		raw, err := readBoundedBody(request.Body, defaultRegistryBodyLimit)
		if err != nil {
			code, status := "REGISTRY_MUTATION_INVALID", http.StatusBadRequest
			if errors.Is(err, errRegistryBodyTooLarge) {
				code, status = "REGISTRY_MUTATION_TOO_LARGE", http.StatusRequestEntityTooLarge
			}
			writeProblem(writer, request, status, code, "Registry mutation invalid", "The electricity billing mutation payload is unreadable or exceeds the supported size.", false, nil)
			return
		}
		if !json.Valid(raw) {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation invalid", "The electricity billing mutation payload must be valid JSON.", false, nil)
			return
		}
		body = raw

		writeGrant, ok := h.authorizeSiteRegistryWriteAction(
			writer,
			request,
			session,
			route.siteID,
			registryauth.ActionBillingManage,
			"BILLING_MANAGE_FORBIDDEN",
			"Electricity billing action forbidden",
			"The authenticated Principal is not allowed to manage electricity budgets or utility bills on this Site.",
		)
		if !ok {
			return
		}
		grant = writeGrant
	}

	h.forwardSiteRegistryBackendRequest(
		writer,
		request,
		session,
		decision.RegistryRevision,
		route.internalPath,
		route.write,
		grant,
		body,
		electricityBillingBackendMessages,
	)
}

func writeElectricityBillingBackendResponse(writer http.ResponseWriter, request *http.Request, response *http.Response, maxBytes int64) {
	writeSiteRegistryBackendResponse(writer, request, response, maxBytes, electricityBillingBackendMessages)
}
