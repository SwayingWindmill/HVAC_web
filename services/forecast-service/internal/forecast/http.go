package forecast

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type HTTPHandler struct {
	service  *Service
	preparer *Preparer
}

func NewHTTPHandler(service *Service, preparer *Preparer) (*HTTPHandler, error) {
	if service == nil || preparer == nil {
		return nil, errors.New("forecast service and input preparer are required")
	}
	return &HTTPHandler{service: service, preparer: preparer}, nil
}

func (handler *HTTPHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("POST /v1/forecast", handler.handleForecast)
	mux.HandleFunc("GET /v1/sites/{siteId}/forecast/load", handler.handleLatestSiteLoad)
	mux.HandleFunc("GET /v1/sites/{siteId}/forecast/pv", handler.handleLatestPV)
	return mux
}

func (handler *HTTPHandler) handleLatestSiteLoad(writer http.ResponseWriter, request *http.Request) {
	handler.handleLatestForecast(writer, request, "SITE_LOAD")
}

func (handler *HTTPHandler) handleLatestPV(writer http.ResponseWriter, request *http.Request) {
	handler.handleLatestForecast(writer, request, "PV_GENERATION")
}

func (handler *HTTPHandler) handleLatestForecast(writer http.ResponseWriter, request *http.Request, target string) {
	tenantID := request.Header.Get("X-Tenant-ID")
	siteID := request.PathValue("siteId")
	result, err := handler.service.LatestForecast(request.Context(), tenantID, siteID, target)
	if errors.Is(err, ErrForecastNotFound) {
		writeJSONError(writer, http.StatusNotFound, "forecast_not_found")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "forecast_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleForecast(writer http.ResponseWriter, request *http.Request) {
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
	prepared, err := handler.preparer.Prepare(request.Context(), input)
	if errors.Is(err, ErrPreparationUnavailable) {
		writeJSONError(writer, http.StatusServiceUnavailable, "forecast_input_unavailable")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusUnprocessableEntity, "forecast_rejected")
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
