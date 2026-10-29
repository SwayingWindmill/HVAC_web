package optimization

import (
	"crypto"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// Optimization serves only the Gateway, with a context it signed for the Site (#612). Before, any
// caller on the network was trusted with whatever X-Tenant-ID it sent.
const (
	ContextHeader          = "X-Optimization-Context"
	ReadAction             = "optimization:read"
	WriteAction            = "optimization:write"
	Audience               = "optimization-service"
	DefaultGatewaySPIFFEID = "spiffe://hvac.local/platform-gateway"
)

type AuthConfig struct {
	GatewayPublicKey crypto.PublicKey
	GatewaySPIFFE    string
	Now              func() time.Time
}

type HTTPHandler struct {
	service  *Service
	preparer *Preparer
	verifier identitycontext.SiteContextVerifier
}

func NewHTTPHandler(service *Service, preparer *Preparer, auth AuthConfig) (http.Handler, error) {
	if service == nil || preparer == nil {
		return nil, errors.New("optimization service and input preparer are required")
	}
	gateway := strings.TrimSpace(auth.GatewaySPIFFE)
	verifier := identitycontext.SiteContextVerifier{PublicKey: auth.GatewayPublicKey, Issuer: gateway, Audience: Audience, Header: ContextHeader, Now: auth.Now}
	if err := verifier.Validate(); err != nil {
		return nil, err
	}
	handler := &HTTPHandler{service: service, preparer: preparer, verifier: verifier}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/optimize", handler.guard(WriteAction, handler.handleOptimize))
	mux.HandleFunc("GET /v1/sites/{siteId}/optimization/recommendations/latest", handler.guard(ReadAction, handler.handleLatestRecommendation))
	return mux, nil
}

// HealthHandler answers the container health check on the plain diagnostics port.
func HealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /metrics", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = writer.Write([]byte("# HELP hvac_service_up 1 when this diagnostics server is serving.\n# TYPE hvac_service_up gauge\nhvac_service_up{service=\"optimization-service\"} 1\n"))
	})
	return mux
}

func (handler *HTTPHandler) guard(action string, next func(http.ResponseWriter, *http.Request, identitycontext.SiteContext)) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		site, err := handler.verifier.Verify(request, action, handler.verifier.Issuer)
		if errors.Is(err, identitycontext.ErrSiteContextWorkload) {
			writeJSONError(writer, http.StatusForbidden, "optimization_workload_forbidden")
			return
		}
		if err != nil {
			writeJSONError(writer, http.StatusForbidden, "optimization_context_invalid")
			return
		}
		next(writer, request, site)
	}
}

func (handler *HTTPHandler) handleLatestRecommendation(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	result, err := handler.service.LatestRecommendation(request.Context(), site.TenantID, site.SiteID)
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

// handleOptimize prepares a run for the Tenant and Site the context covers.
func (handler *HTTPHandler) handleOptimize(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
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
	if input.SiteID != site.SiteID {
		writeJSONError(writer, http.StatusForbidden, "optimization_context_invalid")
		return
	}
	input.TenantID = site.TenantID
	input.SubjectType = "SITE"
	input.SubjectID = input.SiteID
	input.Trigger = TriggerManual
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
