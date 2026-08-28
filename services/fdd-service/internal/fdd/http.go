package fdd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

const maxFDDRequestBytes = int64(256 << 10)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("FDD service is required")
	}
	handler := &HTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("POST /v1/fdd/evaluate/low-delta-t", handler.handleEvaluateLowDeltaT)
	mux.HandleFunc("GET /v1/sites/{siteId}/fdd/findings", handler.handleListFindings)
	mux.HandleFunc("PATCH /v1/sites/{siteId}/fdd/findings/{findingId}/links", handler.handleLinkFinding)
	return mux, nil
}

func (handler *HTTPHandler) handleEvaluateLowDeltaT(writer http.ResponseWriter, request *http.Request) {
	var input EvaluationRequest
	if err := decodeStrictFDDJSON(request.Body, &input); err != nil {
		writeFDDError(writer, http.StatusBadRequest, "fdd_request_invalid")
		return
	}
	result, err := handler.service.EvaluateLowDeltaT(request.Context(), input, request.Header.Get("X-Delegation-Grant"))
	if err != nil {
		writeFDDError(writer, http.StatusUnprocessableEntity, "fdd_evaluation_rejected")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleListFindings(writer http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 200 {
			writeFDDError(writer, http.StatusBadRequest, "fdd_limit_invalid")
			return
		}
		limit = parsed
	}
	findings, err := handler.service.ListFindings(request.Context(), request.Header.Get("X-Tenant-ID"), request.PathValue("siteId"), limit)
	if err != nil {
		writeFDDError(writer, http.StatusBadGateway, "fdd_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(struct {
		Items any `json:"items"`
	}{Items: findings})
}

func (handler *HTTPHandler) handleLinkFinding(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		AlarmID     string `json:"alarmId"`
		WorkOrderID string `json:"workOrderId"`
	}
	if err := decodeStrictFDDJSON(request.Body, &input); err != nil {
		writeFDDError(writer, http.StatusBadRequest, "fdd_link_invalid")
		return
	}
	finding, err := handler.service.LinkFinding(request.Context(), request.Header.Get("X-Tenant-ID"), request.PathValue("siteId"), request.PathValue("findingId"), input.AlarmID, input.WorkOrderID)
	if errors.Is(err, ErrFindingNotFound) {
		writeFDDError(writer, http.StatusNotFound, "fdd_finding_not_found")
		return
	}
	if err != nil {
		writeFDDError(writer, http.StatusUnprocessableEntity, "fdd_link_rejected")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(finding)
}

func decodeStrictFDDJSON(body io.Reader, target any) error {
	raw, err := io.ReadAll(io.LimitReader(body, maxFDDRequestBytes+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > maxFDDRequestBytes {
		return errors.New("FDD request body is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("FDD request contains trailing JSON")
	}
	return nil
}

func writeFDDError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"code": code, "status": status})
}
