package gateway

import (
	"encoding/json"
	"net/http"
)

type v212ContractOnlyRoute struct {
	method      string
	template    string
	placeholder string
}

var v212ContractOnlyRoutes = []v212ContractOnlyRoute{
	{method: http.MethodGet, template: "/api/v1/devices"},
	{method: http.MethodPost, template: "/api/v1/telemetry/latest"},
	{method: http.MethodGet, template: "/api/v1/telemetry/history"},
}

func matchV212ContractOnlyRoute(path string) (v212ContractOnlyRoute, bool) {
	for _, route := range v212ContractOnlyRoutes {
		if route.placeholder == "" {
			if path == route.template {
				return route, true
			}
			continue
		}
		if _, matches := matchSinglePathParameter(path, route.template, route.placeholder); matches {
			return route, true
		}
	}
	return v212ContractOnlyRoute{}, false
}

func writeV212ContractOnly(writer http.ResponseWriter, request *http.Request, route v212ContractOnlyRoute) {
	if request.Method != route.method {
		writer.Header().Set("Allow", route.method)
		writeV212Error(writer, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This route does not support the requested method.", map[string]any{
			"allowedMethod": route.method,
			"route":         route.template,
		})
		return
	}
	writeV212Error(writer, request, http.StatusServiceUnavailable, "CONTRACT_NOT_ACTIVE", "This SE-API-001 V2.1.2 route is contracted but not active until its operation-specific shape is frozen.", map[string]any{
		"contractId": "SE-API-001",
		"version":    "2.1.2",
		"route":      route.template,
		"reason":     "OPERATION_SHAPE_PENDING",
	})
}

func writeV212Error(writer http.ResponseWriter, request *http.Request, status int, code, message string, details any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
		"meta": map[string]any{
			"requestId": requestIDFromContext(request.Context()),
		},
	})
}
