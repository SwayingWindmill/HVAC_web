package report

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
)

// The Gateway reaches report-service on the private network after deciding the caller may read
// the Site (site.read); it names the Tenant.
const headerTenant = "X-Tenant-ID"

var (
	monthPattern = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)
	uuidPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("report service is required")
	}
	handler := &HTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /v1/sites/{siteId}/reports/monthly/{month}", handler.month)
	mux.HandleFunc("GET /v1/sites/{siteId}/reports/monthly", handler.year)
	return mux, nil
}

func (handler *HTTPHandler) month(writer http.ResponseWriter, request *http.Request) {
	tenantID, siteID, month := request.Header.Get(headerTenant), request.PathValue("siteId"), request.PathValue("month")
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || !monthPattern.MatchString(month) {
		writeProblem(writer, http.StatusBadRequest, "REPORT_REQUEST_INVALID")
		return
	}
	stored, err := handler.service.Latest(request.Context(), tenantID, siteID, month)
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
	tenantID, siteID := request.Header.Get(headerTenant), request.PathValue("siteId")
	year, err := strconv.Atoi(request.URL.Query().Get("year"))
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || err != nil || year < 2000 || year > 9999 {
		writeProblem(writer, http.StatusBadRequest, "REPORT_REQUEST_INVALID")
		return
	}
	snapshots, err := handler.service.Year(request.Context(), tenantID, siteID, year)
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
