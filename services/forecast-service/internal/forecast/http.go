package forecast

import (
	"crypto"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// Forecast serves only the Gateway, with a context it signed for the Site (#612). Before, any
// caller on the network was trusted with whatever X-Tenant-ID it sent.
const (
	ContextHeader          = "X-Forecast-Context"
	ReadAction             = "forecast:read"
	Audience               = "forecast-service"
	DefaultGatewaySPIFFEID = "spiffe://hvac.local/platform-gateway"
)

type AuthConfig struct {
	GatewayPublicKey crypto.PublicKey
	GatewaySPIFFE    string
	Now              func() time.Time
}

type HTTPHandler struct {
	service  *Service
	verifier identitycontext.SiteContextVerifier
	gateway  string
}

func NewHTTPHandler(service *Service, auth AuthConfig) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("forecast service is required")
	}
	gateway := strings.TrimSpace(auth.GatewaySPIFFE)
	verifier := identitycontext.SiteContextVerifier{PublicKey: auth.GatewayPublicKey, Issuer: gateway, Audience: Audience, Header: ContextHeader, Now: auth.Now}
	if err := verifier.Validate(); err != nil {
		return nil, err
	}
	handler := &HTTPHandler{service: service, verifier: verifier, gateway: gateway}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sites/{siteId}/forecast/load", handler.guard(func(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
		handler.handleLatestForecast(writer, request, site, "SITE_LOAD")
	}))
	mux.HandleFunc("GET /v1/sites/{siteId}/forecast/pv", handler.guard(func(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
		handler.handleLatestForecast(writer, request, site, "PV_GENERATION")
	}))
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

func (handler *HTTPHandler) guard(next func(http.ResponseWriter, *http.Request, identitycontext.SiteContext)) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		site, err := handler.verifier.Verify(request, ReadAction, handler.gateway)
		if errors.Is(err, identitycontext.ErrSiteContextWorkload) {
			writeJSONError(writer, http.StatusForbidden, "forecast_workload_forbidden")
			return
		}
		if err != nil {
			writeJSONError(writer, http.StatusForbidden, "forecast_context_invalid")
			return
		}
		next(writer, request, site)
	}
}

func (handler *HTTPHandler) handleLatestForecast(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext, target string) {
	result, err := handler.service.LatestForecast(request.Context(), site.TenantID, site.SiteID, target)
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

func writeJSONError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"error": code})
}
