package fdd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const maxFDDRequestBytes = int64(256 << 10)

type HTTPHandler struct{ service *Service }

// NewHTTPHandler serves FDD to the Gateway, and the Site's findings list to the reader workload,
// each under a context the Gateway signed (#595).
func NewHTTPHandler(service *Service, auth AuthConfig) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("FDD service is required")
	}
	guard, err := newAuthorizer(auth)
	if err != nil {
		return nil, err
	}
	handler := &HTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/fdd/evaluate/low-delta-t", guard.guard(WriteAction, false, handler.handleEvaluateLowDeltaT))
	mux.HandleFunc("GET /v1/sites/{siteId}/fdd/rules/low-delta-t", guard.guard(ReadAction, false, ignoreClaims(handler.handleCurrentRule)))
	mux.HandleFunc("GET /v1/sites/{siteId}/fdd/findings", guard.guard(ReadAction, true, ignoreClaims(handler.handleListFindings)))
	mux.HandleFunc("PATCH /v1/sites/{siteId}/fdd/findings/{findingId}/links", guard.guard(WriteAction, false, ignoreClaims(handler.handleLinkFinding)))
	return mux, nil
}

// HealthHandler answers the container health check on the plain diagnostics port.
func HealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}

func ignoreClaims(next http.HandlerFunc) func(http.ResponseWriter, *http.Request, identitycontext.DelegationClaims) {
	return func(writer http.ResponseWriter, request *http.Request, _ identitycontext.DelegationClaims) {
		next(writer, request)
	}
}

// handleEvaluateLowDeltaT diagnoses a loop now, over the window of the rule in effect, for the
// Tenant and Site the context covers.
func (handler *HTTPHandler) handleEvaluateLowDeltaT(writer http.ResponseWriter, request *http.Request, claims identitycontext.DelegationClaims) {
	var input struct {
		TenantID string `json:"tenantId"`
		SiteID   string `json:"siteId"`
		AssetID  string `json:"assetId"`
		DeviceID string `json:"deviceId"`
	}
	if err := decodeStrictFDDJSON(request.Body, &input); err != nil {
		writeFDDError(writer, http.StatusBadRequest, "fdd_request_invalid")
		return
	}
	if input.TenantID != claims.TenantID || !coversSite(claims, input.SiteID) {
		writeFDDError(writer, http.StatusForbidden, "fdd_context_invalid")
		return
	}
	result, err := handler.service.EvaluateLowDeltaT(request.Context(), EvaluationRequest{
		TenantID: input.TenantID, SiteID: input.SiteID, AssetID: input.AssetID, DeviceID: input.DeviceID, At: handler.service.now().UTC(),
	})
	if errors.Is(err, ErrRuleNotFound) {
		writeFDDError(writer, http.StatusConflict, "fdd_rule_not_found")
		return
	}
	if err != nil {
		writeFDDError(writer, http.StatusUnprocessableEntity, "fdd_evaluation_rejected")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleCurrentRule(writer http.ResponseWriter, request *http.Request) {
	rule, err := handler.service.CurrentRule(request.Context(), tenantFrom(request.Context()), request.PathValue("siteId"))
	if errors.Is(err, ErrRuleNotFound) {
		writeFDDError(writer, http.StatusNotFound, "fdd_rule_not_found")
		return
	}
	if err != nil {
		writeFDDError(writer, http.StatusBadGateway, "fdd_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(rule)
}

func (handler *HTTPHandler) handleListFindings(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	for key, values := range query {
		if len(values) != 1 {
			writeFDDError(writer, http.StatusBadRequest, "fdd_filter_invalid")
			return
		}
		switch key {
		case "limit", "alarmId", "workOrderId":
		default:
			writeFDDError(writer, http.StatusBadRequest, "fdd_filter_invalid")
			return
		}
	}
	filter := FindingFilter{AlarmID: query.Get("alarmId"), WorkOrderID: query.Get("workOrderId"), Limit: 50}
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 200 {
			writeFDDError(writer, http.StatusBadRequest, "fdd_limit_invalid")
			return
		}
		filter.Limit = parsed
	}
	findings, err := handler.service.ListFindings(request.Context(), tenantFrom(request.Context()), request.PathValue("siteId"), filter)
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
	finding, err := handler.service.LinkFinding(request.Context(), tenantFrom(request.Context()), request.PathValue("siteId"), request.PathValue("findingId"), input.AlarmID, input.WorkOrderID)
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
