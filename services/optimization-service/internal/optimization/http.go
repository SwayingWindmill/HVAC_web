package optimization

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) (*HTTPHandler, error) {
	if service == nil {
		return nil, errors.New("optimization service is required")
	}
	return &HTTPHandler{service: service}, nil
}

func (handler *HTTPHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("POST /v1/optimize", handler.handleOptimize)
	return mux
}

func (handler *HTTPHandler) handleOptimize(writer http.ResponseWriter, request *http.Request) {
	reader := http.MaxBytesReader(writer, request.Body, 128*1024)
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var input Request
	if err := decoder.Decode(&input); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	plan, err := handler.service.Optimize(request.Context(), input)
	if err != nil {
		writeJSONError(writer, http.StatusUnprocessableEntity, "optimization_rejected")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(plan)
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
