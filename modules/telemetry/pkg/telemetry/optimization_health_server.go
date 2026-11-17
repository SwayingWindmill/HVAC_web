package telemetry

import (
	"encoding/json"
	"mime"
	"net/http"
	"strings"
)

const (
	// DefaultOptimizationServiceSPIFFE is the default workload identity trusted on
	// the internal optimization point-health endpoint.
	DefaultOptimizationServiceSPIFFE     = "spiffe://hvac.local/optimization-service"
	maximumOptimizationHealthRequestSize = 64 << 10
	maximumOptimizationHealthScopeItems  = 256
)

func (h *handler) handleOptimizationPointHealth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports POST.", false)
		return
	}
	if hasForgedIdentityHeader(request.Header) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.", false)
		return
	}
	peer, verified := verifiedPeerSPIFFE(request)
	if !verified || peer == "" || peer != h.allowedOptimizationServiceSPIFFE {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted for optimization point health inspection.", false)
		return
	}
	if h.pointHealthInspector == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_OPTIMIZATION_HEALTH_UNAVAILABLE", "Optimization point health inspection is not configured.", true)
		return
	}
	req, ok := decodeOptimizationPointHealthRequest(writer, request)
	if !ok {
		return
	}
	result, err := h.pointHealthInspector.InspectOptimizationPointHealth(request.Context(), req)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_OPTIMIZATION_HEALTH_UNAVAILABLE", "Optimization point health inspection is temporarily unavailable.", true)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func decodeOptimizationPointHealthRequest(writer http.ResponseWriter, request *http.Request) (OptimizationPointHealthRequest, bool) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(request.Header.Get("Content-Type")))
	if err != nil || mediaType != "application/json" {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_OPTIMIZATION_HEALTH_REQUEST_INVALID", "The optimization point health request must use application/json.", false)
		return OptimizationPointHealthRequest{}, false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumOptimizationHealthRequestSize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var req OptimizationPointHealthRequest
	if err := decoder.Decode(&req); err != nil || ensureJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_OPTIMIZATION_HEALTH_REQUEST_INVALID", "The optimization point health request is malformed.", false)
		return OptimizationPointHealthRequest{}, false
	}
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.SiteID = strings.TrimSpace(req.SiteID)
	if !uuidPattern.MatchString(req.TenantID) || !uuidPattern.MatchString(req.SiteID) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_OPTIMIZATION_HEALTH_REQUEST_INVALID", "The optimization point health tenantId and siteId must be UUIDs.", false)
		return OptimizationPointHealthRequest{}, false
	}
	if len(req.DeviceIDs) > maximumOptimizationHealthScopeItems || len(req.TelemetryKeys) > maximumOptimizationHealthScopeItems {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_OPTIMIZATION_HEALTH_REQUEST_INVALID", "The optimization point health request exceeds maximum scope size.", false)
		return OptimizationPointHealthRequest{}, false
	}
	cleanDevices := make([]string, 0, len(req.DeviceIDs))
	for _, raw := range req.DeviceIDs {
		clean := strings.TrimSpace(raw)
		if !uuidPattern.MatchString(clean) {
			writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_OPTIMIZATION_HEALTH_REQUEST_INVALID", "Every deviceId in optimization point health requests must be a UUID.", false)
			return OptimizationPointHealthRequest{}, false
		}
		cleanDevices = append(cleanDevices, clean)
	}
	cleanKeys := make([]string, 0, len(req.TelemetryKeys))
	for _, raw := range req.TelemetryKeys {
		clean := strings.TrimSpace(raw)
		if clean == "" || len(clean) > 256 {
			writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_OPTIMIZATION_HEALTH_REQUEST_INVALID", "Every telemetryKey in optimization point health requests must be non-empty.", false)
			return OptimizationPointHealthRequest{}, false
		}
		cleanKeys = append(cleanKeys, clean)
	}
	req.DeviceIDs = cleanDevices
	req.TelemetryKeys = cleanKeys
	return req, true
}
