package query

import (
	"crypto"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/telemetry-query-service/internal/analytics"
)

const (
	EnergySeriesPath       = "/internal/v1/analytics/energy-series"
	maximumRequestBodySize = int64(64 << 10)
)

type ServerConfig struct {
	Engine                            analytics.EnergySeriesEngine
	DelegationPublicKey               crypto.PublicKey
	DelegationIssuerSPIFFE            string
	AllowedPresenterSPIFFE            string
	AdditionalAllowedPresenterSPIFFEs []string
	Audience                          string
	Logger                            *slog.Logger
	Observability                     *observability.Runtime
	Now                               func() time.Time
}

type handler struct {
	engine                  analytics.EnergySeriesEngine
	delegationPublicKey     crypto.PublicKey
	delegationIssuerSPIFFE  string
	allowedPresenterSPIFFEs map[string]struct{}
	audience                string
	logger                  *slog.Logger
	observability           *observability.Runtime
	now                     func() time.Time
}

func NewHandler(config ServerConfig) http.Handler {
	primaryPresenter := strings.TrimSpace(config.AllowedPresenterSPIFFE)
	delegationIssuer := strings.TrimSpace(config.DelegationIssuerSPIFFE)
	if delegationIssuer == "" {
		delegationIssuer = primaryPresenter
	}
	if config.Engine == nil || config.DelegationPublicKey == nil || primaryPresenter == "" || delegationIssuer == "" || strings.TrimSpace(config.Audience) == "" {
		panic("Telemetry Query server configuration is incomplete")
	}
	allowedPresenters := map[string]struct{}{primaryPresenter: {}}
	for _, candidate := range config.AdditionalAllowedPresenterSPIFFEs {
		presenter := strings.TrimSpace(candidate)
		if presenter == "" {
			panic("Telemetry Query allowed presenter is empty")
		}
		allowedPresenters[presenter] = struct{}{}
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	telemetry := config.Observability
	if telemetry == nil {
		telemetry = observability.NewRuntime(observability.RuntimeConfig{Service: "telemetry-query-service"})
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &handler{
		engine:                  config.Engine,
		delegationPublicKey:     config.DelegationPublicKey,
		delegationIssuerSPIFFE:  delegationIssuer,
		allowedPresenterSPIFFEs: allowedPresenters,
		audience:                config.Audience,
		logger:                  logger,
		observability:           telemetry,
		now:                     now,
	}
}

func (server *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	started := server.now()
	routeName := "unmatched"
	if request.URL.Path == EnergySeriesPath {
		routeName = EnergySeriesPath
	}
	ctx := server.observability.Tracer.ExtractHTTP(request.Context(), request.Header)
	ctx, span := server.observability.Tracer.Start(ctx, "http.analytics.energy-series", observability.SpanKindServer, map[string]any{
		"http.request.method": request.Method,
		"http.route":          routeName,
	})
	request = request.WithContext(ctx)
	writer.Header().Set("traceparent", observability.Traceparent(ctx))
	tracked := &statusWriter{ResponseWriter: writer}
	writer = tracked
	defer func() {
		status := tracked.status
		if status == 0 {
			status = http.StatusOK
		}
		result := "ok"
		if status >= http.StatusBadRequest {
			result = "error"
			span.SetStatus("error", http.StatusText(status))
		} else {
			span.SetStatus("ok", "")
		}
		span.SetAttributes(map[string]any{"http.response.status_code": status})
		span.End()
		_ = server.observability.Metrics.AddCounter("analytics_query_requests_total", "Analytics product query requests.", map[string]string{"route": routeName, "method": request.Method, "result": result}, 1)
		_ = server.observability.Metrics.ObserveHistogram("analytics_query_request_duration_seconds", "Analytics product query latency.", map[string]string{"route": routeName, "method": request.Method}, server.now().Sub(started).Seconds(), nil)
		server.logger.InfoContext(request.Context(), "analytics_query_request",
			"method", request.Method,
			"path", routeName,
			"status", status,
			"duration_ms", server.now().Sub(started).Milliseconds(),
			"trace_id", observability.TraceID(request.Context()),
		)
	}()

	if request.URL.Path != EnergySeriesPath {
		writeProblem(writer, http.StatusNotFound, "ANALYTICS_ROUTE_NOT_FOUND", "The requested analytics route does not exist.", false)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, http.StatusMethodNotAllowed, "ANALYTICS_METHOD_NOT_ALLOWED", "The energy series route only supports POST.", false)
		return
	}
	if hasForgedIdentityHeader(request.Header) {
		writeProblem(writer, http.StatusBadRequest, "ANALYTICS_FORGED_IDENTITY_HEADER", "Caller-supplied identity or business-scope headers are not accepted.", false)
		return
	}
	peerSPIFFE, ok := verifiedPeerSPIFFE(request)
	_, presenterAllowed := server.allowedPresenterSPIFFEs[peerSPIFFE]
	if !ok || !presenterAllowed {
		writeProblem(writer, http.StatusUnauthorized, "ANALYTICS_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.", false)
		return
	}

	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maximumRequestBodySize))
	decoder.DisallowUnknownFields()
	var query analyticsmodel.EnergySeriesQuery
	if err := decoder.Decode(&query); err != nil || ensureJSONEOF(decoder) != nil {
		writeProblem(writer, http.StatusBadRequest, "ANALYTICS_REQUEST_INVALID", "The energy series request body is invalid.", false)
		return
	}
	if err := query.Validate(); err != nil {
		writeProblem(writer, http.StatusBadRequest, "ANALYTICS_QUERY_INVALID", "The energy series query exceeds the supported product boundary.", false)
		return
	}
	scope, err := query.ScopeDigest()
	if err != nil {
		writeProblem(writer, http.StatusBadRequest, "ANALYTICS_QUERY_INVALID", "The energy series query exceeds the supported product boundary.", false)
		return
	}
	claims, err := identitycontext.VerifyDelegation(server.delegationPublicKey, request.Header.Get("X-Delegation-Grant"))
	if err != nil {
		writeProblem(writer, http.StatusUnauthorized, "ANALYTICS_DELEGATION_INVALID", "The analytics delegation grant is invalid.", false)
		return
	}
	if claims.PrincipalID == "" || claims.ActingOrganizationID != query.OrganizationID || identitycontext.ValidateDelegationFromIssuer(
		claims,
		server.now(),
		server.delegationIssuerSPIFFE,
		peerSPIFFE,
		server.audience,
		analyticsmodel.EnergySeriesAction,
		scope,
	) != nil {
		writeProblem(writer, http.StatusForbidden, "ANALYTICS_DELEGATION_REJECTED", "The analytics delegation grant is not authorized for this query.", false)
		return
	}

	response, err := server.engine.QueryEnergySeries(request.Context(), analytics.CallerContext{
		PrincipalID: claims.PrincipalID, PolicyRevision: claims.PolicyRevision,
	}, query)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "ANALYTICS_ENGINE_UNAVAILABLE", "The semantic query engine could not complete the request.", true)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func verifiedPeerSPIFFE(request *http.Request) (string, bool) {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		return "", false
	}
	certificate := request.TLS.PeerCertificates[0]
	if len(certificate.URIs) != 1 || certificate.URIs[0] == nil || certificate.URIs[0].Scheme != "spiffe" {
		return "", false
	}
	return certificate.URIs[0].String(), true
}

func hasForgedIdentityHeader(header http.Header) bool {
	for actualName, values := range header {
		for _, forbiddenName := range []string{"X-Organization-ID", "X-Site-ID", "X-Principal-ID", "X-Roles", "X-Policy-Revision"} {
			if strings.EqualFold(actualName, forbiddenName) {
				for _, value := range values {
					if strings.TrimSpace(value) != "" {
						return true
					}
				}
			}
		}
	}
	return false
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Write(payload []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(payload)
}

type problemDetails struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Retryable bool   `json:"retryable"`
}

func writeProblem(writer http.ResponseWriter, status int, code, detail string, retryable bool) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(problemDetails{
		Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, Detail: detail, Retryable: retryable,
	})
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}
