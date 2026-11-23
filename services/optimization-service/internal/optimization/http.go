package optimization

import (
	"crypto"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
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
	mux.HandleFunc("GET /v1/sites/{siteId}/optimization/preparations/latest", handler.guard(ReadAction, handler.handleLatestPreparation))
	mux.HandleFunc("GET /v1/sites/{siteId}/optimization/shadow-evaluations/latest", handler.guard(ReadAction, handler.handleLatestShadowEvaluation))
	mux.HandleFunc("GET /v1/sites/{siteId}/optimization/staging-recommendations/latest", handler.guard(ReadAction, handler.handleLatestStagingEvaluation))
	mux.HandleFunc("GET /v1/sites/{siteId}/optimization/governance-summary", handler.guard(ReadAction, handler.handleGovernanceSummary))
	mux.HandleFunc("GET /v1/sites/{siteId}/optimization/canary-bindings", handler.guard(ReadAction, handler.handleListCanaryBindings))
	mux.HandleFunc("PUT /v1/sites/{siteId}/optimization/canary-bindings/{loopKey}", handler.guard(WriteAction, handler.handleUpsertCanaryBinding))
	mux.HandleFunc("DELETE /v1/sites/{siteId}/optimization/canary-bindings/{loopKey}", handler.guard(WriteAction, handler.handleDeleteCanaryBinding))
	mux.HandleFunc("GET /v1/sites/{siteId}/optimization/negative-samples", handler.guard(ReadAction, handler.handleListNegativeSamples))
	mux.HandleFunc("GET /v1/sites/{siteId}/edge-models", handler.guard(ReadAction, handler.handleGetEdgeModelWorkspace))
	mux.HandleFunc("POST /v1/sites/{siteId}/edge-models/artifacts", handler.guard(WriteAction, handler.handleCreateEdgeModelArtifact))
	mux.HandleFunc("POST /v1/sites/{siteId}/edge-models/artifacts/{artifactId}/approve", handler.guard(WriteAction, handler.handleApproveEdgeModelArtifact))
	mux.HandleFunc("POST /v1/sites/{siteId}/edge-models/deployments", handler.guard(WriteAction, handler.handleDeployEdgeModelArtifact))
	mux.HandleFunc("POST /v1/sites/{siteId}/edge-models/deployments/{deploymentId}/rollback", handler.guard(WriteAction, handler.handleRollbackEdgeModelDeployment))
	mux.HandleFunc("POST /v1/sites/{siteId}/edge-models/evaluations", handler.guard(WriteAction, handler.handleRecordEdgeModelEvaluation))
	return mux, nil
}

// HealthHandler answers the container health check on the plain diagnostics port.
func HealthHandler() http.Handler {
	return NewDiagnosticsHandler(nil)
}

// NewDiagnosticsHandler serves /healthz, /metrics (preserving hvac_service_up), and runtime readiness probes.
func NewDiagnosticsHandler(runtime *observability.Runtime) http.Handler {
	metrics := observability.NewRegistry()
	var runtimeHandler http.Handler
	if runtime != nil {
		if runtime.Metrics != nil {
			metrics = runtime.Metrics
		}
		runtimeHandler = runtime.DiagnosticsHandler()
	}
	_ = metrics.SetGauge(
		"hvac_service_up",
		"1 when this diagnostics server is serving.",
		map[string]string{"service": Audience},
		1,
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	if runtimeHandler != nil {
		mux.Handle("/", runtimeHandler)
	} else {
		mux.Handle("GET /metrics", metrics.Handler())
	}
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

// handleLatestPreparation returns the Site's latest schedule firing and readiness status.
func (handler *HTTPHandler) handleLatestPreparation(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	result, err := handler.service.LatestPreparation(request.Context(), site.TenantID, site.SiteID)
	if errors.Is(err, ErrPreparationNotFound) {
		writeJSONError(writer, http.StatusNotFound, "optimization_preparation_not_found")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "optimization_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleLatestShadowEvaluation(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	result, err := handler.service.LatestShadowEvaluation(request.Context(), site.TenantID, site.SiteID)
	if errors.Is(err, ErrShadowEvaluationNotFound) {
		writeJSONError(writer, http.StatusNotFound, "optimization_shadow_not_found")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "optimization_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleLatestStagingEvaluation(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	result, err := handler.service.LatestStagingEvaluation(request.Context(), site.TenantID, site.SiteID)
	if errors.Is(err, ErrStagingEvaluationNotFound) {
		writeJSONError(writer, http.StatusNotFound, "optimization_staging_not_found")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "optimization_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleGovernanceSummary(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	windowDays := DefaultGovernanceWindowDays
	if rawWindow := strings.TrimSpace(request.URL.Query().Get("windowDays")); rawWindow != "" {
		parsed, err := strconv.Atoi(rawWindow)
		if err != nil || parsed <= 0 || parsed > MaxGovernanceWindowDays {
			writeJSONError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		windowDays = parsed
	}
	result, err := handler.service.GovernanceSummary(request.Context(), site.TenantID, site.SiteID, windowDays)
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

func (handler *HTTPHandler) handleListCanaryBindings(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	result, err := handler.service.ListCanaryBindings(request.Context(), site.TenantID, site.SiteID)
	if errors.Is(err, ErrOptimizationNotFound) {
		writeJSONError(writer, http.StatusNotFound, "optimization_not_found")
		return
	}
	if errors.Is(err, ErrCanaryInvalidRequest) {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "optimization_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleUpsertCanaryBinding(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	reader := http.MaxBytesReader(writer, request.Body, 16*1024)
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var input UpsertCanaryBindingRequest
	if err := decoder.Decode(&input); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	loopKey := request.PathValue("loopKey")
	result, err := handler.service.UpsertCanaryBinding(request.Context(), site.TenantID, site.SiteID, loopKey, input)
	if errors.Is(err, ErrOptimizationNotFound) {
		writeJSONError(writer, http.StatusNotFound, "optimization_not_found")
		return
	}
	if errors.Is(err, ErrCanaryHostNotAllowed) {
		writeJSONError(writer, http.StatusUnprocessableEntity, "optimization_solver_host_not_allowed")
		return
	}
	if errors.Is(err, ErrCanaryInvalidRequest) {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "optimization_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleDeleteCanaryBinding(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	loopKey := request.PathValue("loopKey")
	result, err := handler.service.DeleteCanaryBinding(request.Context(), site.TenantID, site.SiteID, loopKey)
	if errors.Is(err, ErrOptimizationNotFound) {
		writeJSONError(writer, http.StatusNotFound, "optimization_not_found")
		return
	}
	if errors.Is(err, ErrCanaryInvalidRequest) {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "optimization_unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleListNegativeSamples(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	q := request.URL.Query()
	windowDays := DefaultGovernanceWindowDays
	if rawWindow := strings.TrimSpace(q.Get("windowDays")); rawWindow != "" {
		parsed, err := strconv.Atoi(rawWindow)
		if err != nil || parsed <= 0 || parsed > MaxGovernanceWindowDays {
			writeJSONError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		windowDays = parsed
	}
	limit := DefaultNegativeSampleLimit
	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 || parsed > MaxNegativeSampleLimit {
			writeJSONError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		limit = parsed
	}
	offset := 0
	if rawOffset := strings.TrimSpace(q.Get("offset")); rawOffset != "" {
		parsed, err := strconv.Atoi(rawOffset)
		if err != nil || parsed < 0 {
			writeJSONError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		offset = parsed
	}
	sampleKind := strings.TrimSpace(q.Get("sampleKind"))
	result, err := handler.service.ListNegativeSamplesPage(request.Context(), site.TenantID, site.SiteID, windowDays, sampleKind, limit, offset)
	if errors.Is(err, ErrOptimizationNotFound) {
		writeJSONError(writer, http.StatusNotFound, "optimization_not_found")
		return
	}
	if errors.Is(err, ErrCanaryInvalidRequest) {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
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
