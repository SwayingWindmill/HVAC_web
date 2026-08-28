package optimization

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type HTTPHandler struct {
	service  *Service
	preparer *Preparer
}

func NewHTTPHandler(service *Service, preparer *Preparer) (*HTTPHandler, error) {
	if service == nil || preparer == nil {
		return nil, errors.New("optimization service and input preparer are required")
	}
	return &HTTPHandler{service: service, preparer: preparer}, nil
}

func (handler *HTTPHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("POST /v1/optimize", handler.handleOptimize)
	mux.HandleFunc("GET /v1/sites/{siteId}/optimization/runs/{runId}", handler.handleGetRecommendation)
	mux.HandleFunc("GET /v1/sites/{siteId}/optimization/recommendations/latest", handler.handleLatestRecommendation)
	mux.HandleFunc("GET /v1/optimization/runs/{runId}", handler.handleGetRecommendationForAuthorizedSites)
	return mux
}

func (handler *HTTPHandler) handleLatestRecommendation(writer http.ResponseWriter, request *http.Request) {
	result, err := handler.service.LatestRecommendation(request.Context(), request.Header.Get("X-Tenant-ID"), request.PathValue("siteId"))
	if errors.Is(err, ErrOptimizationNotFound) {
		writeJSONError(writer, http.StatusNotFound, "optimization_not_found")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "optimization_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleGetRecommendationForAuthorizedSites(writer http.ResponseWriter, request *http.Request) {
	allowedSiteIDs := splitAuthorizedSiteIDs(request.Header.Get("X-Authorized-Site-IDs"))
	result, err := handler.service.GetRecommendationForSites(request.Context(), request.Header.Get("X-Tenant-ID"), allowedSiteIDs, request.PathValue("runId"))
	if errors.Is(err, ErrOptimizationNotFound) {
		writeJSONError(writer, http.StatusNotFound, "optimization_not_found")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "optimization_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func splitAuthorizedSiteIDs(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func (handler *HTTPHandler) handleGetRecommendation(writer http.ResponseWriter, request *http.Request) {
	result, err := handler.service.GetRecommendation(request.Context(), request.Header.Get("X-Tenant-ID"), request.PathValue("siteId"), request.PathValue("runId"))
	if errors.Is(err, ErrOptimizationNotFound) {
		writeJSONError(writer, http.StatusNotFound, "optimization_not_found")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "optimization_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleOptimize(writer http.ResponseWriter, request *http.Request) {
	reader := http.MaxBytesReader(writer, request.Body, 64*1024)
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var input PreparationRequest
	if err := decoder.Decode(&input); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	input.TenantID = request.Header.Get("X-Tenant-ID")
	input.SubjectType = "SITE"
	input.SubjectID = input.SiteID
	prepared, err := handler.preparer.Prepare(request.Context(), input)
	if errors.Is(err, ErrPreparationUnavailable) {
		writeJSONError(writer, http.StatusServiceUnavailable, "optimization_input_unavailable")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusUnprocessableEntity, "optimization_rejected")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(writer).Encode(prepared)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	}
	return errors.New("trailing JSON")
}

func writeJSONError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"error": code})
}
