package report

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// report-service serves only the Gateway, with a context it signed to read the Site after deciding
// the person may (site.read) (#613). Before, any caller on the network was trusted with whatever
// X-Tenant-ID it sent.
const (
	ContextHeader          = "X-Report-Context"
	ReadAction             = "report:read"
	Audience               = "report-service"
	DefaultGatewaySPIFFEID = "spiffe://hvac.local/platform-gateway"
)

var monthPattern = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

type AuthConfig struct {
	GatewayPublicKey crypto.PublicKey
	GatewaySPIFFE    string
	Now              func() time.Time
}

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service, auth AuthConfig) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("report service is required")
	}
	gateway := strings.TrimSpace(auth.GatewaySPIFFE)
	verifier := identitycontext.SiteContextVerifier{PublicKey: auth.GatewayPublicKey, Issuer: gateway, Audience: Audience, Header: ContextHeader, Now: auth.Now}
	if err := verifier.Validate(); err != nil {
		return nil, err
	}
	read := func(next http.HandlerFunc) http.HandlerFunc {
		return func(writer http.ResponseWriter, request *http.Request) {
			site, err := verifier.Verify(request, ReadAction, gateway)
			if errors.Is(err, identitycontext.ErrSiteContextWorkload) {
				writeProblem(writer, http.StatusForbidden, "REPORT_WORKLOAD_FORBIDDEN")
				return
			}
			if err != nil {
				writeProblem(writer, http.StatusForbidden, "REPORT_CONTEXT_INVALID")
				return
			}
			next(writer, request.WithContext(context.WithValue(request.Context(), siteKey{}, site)))
		}
	}
	handler := &HTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sites/{siteId}/reports/monthly/{month}", read(handler.month))
	mux.HandleFunc("GET /v1/sites/{siteId}/reports/monthly", read(handler.year))
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
		_, _ = writer.Write([]byte("# HELP hvac_service_up 1 when this diagnostics server is serving.\n# TYPE hvac_service_up gauge\nhvac_service_up{service=\"report-service\"} 1\n"))
	})
	return mux
}

type siteKey struct{}

// siteOf is the Tenant and Site the Gateway's context named; handlers never read them from a header.
func siteOf(request *http.Request) identitycontext.SiteContext {
	site, _ := request.Context().Value(siteKey{}).(identitycontext.SiteContext)
	return site
}

func (handler *HTTPHandler) month(writer http.ResponseWriter, request *http.Request) {
	site, month := siteOf(request), request.PathValue("month")
	if !monthPattern.MatchString(month) {
		writeProblem(writer, http.StatusBadRequest, "REPORT_REQUEST_INVALID")
		return
	}
	stored, err := handler.service.Latest(request.Context(), site.TenantID, site.SiteID, month)
	if errors.Is(err, ErrSnapshotNotFound) {
		writeProblem(writer, http.StatusNotFound, "REPORT_SNAPSHOT_NOT_FOUND")
		return
	}
	if err != nil {
		log.Printf("read report snapshot: %v", err)
		writeProblem(writer, http.StatusServiceUnavailable, "REPORT_UNAVAILABLE")
		return
	}
	writeJSON(writer, stored)
}

func (handler *HTTPHandler) year(writer http.ResponseWriter, request *http.Request) {
	site := siteOf(request)
	year, err := strconv.Atoi(request.URL.Query().Get("year"))
	if err != nil || year < 2000 || year > 9999 {
		writeProblem(writer, http.StatusBadRequest, "REPORT_REQUEST_INVALID")
		return
	}
	snapshots, err := handler.service.Year(request.Context(), site.TenantID, site.SiteID, year)
	if err != nil {
		log.Printf("read report snapshots: %v", err)
		writeProblem(writer, http.StatusServiceUnavailable, "REPORT_UNAVAILABLE")
		return
	}
	writeJSON(writer, map[string]any{"items": snapshots})
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(writer).Encode(value)
}

func writeProblem(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"status": status, "code": code})
}
