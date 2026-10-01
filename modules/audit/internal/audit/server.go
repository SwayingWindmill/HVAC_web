package audit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/operationsauditevent"
)

const SessionAuditPathPrefix = "/internal/v1/audit/session-events/"
const OperationsAuditPath = "/internal/v1/audit/operations-events"
const AuditSearchPath = "/internal/v1/audit/search"

type RecordReader interface {
	GetRecord(context.Context, string, string) (Record, error)
}

type RecordSearcher interface {
	SearchRecords(context.Context, string, SearchFilter) ([]SearchRecord, error)
}

type OperationsEventWriter interface {
	ConsumeOperations(context.Context, []byte, MessageMetadata) (bool, error)
}

type ServerConfig struct {
	Store                           RecordReader
	Searcher                        RecordSearcher
	OperationsWriter                OperationsEventWriter
	AllowedWorkloadSPIFFE           string
	AllowedOperationsProducerSPIFFE string
	MaximumOperationsEventBytes     int64
	Audience                        string
	Logger                          *slog.Logger
	Observability                   *observability.Runtime
	Now                             func() time.Time
}

type server struct {
	store                           RecordReader
	searcher                        RecordSearcher
	operationsWriter                OperationsEventWriter
	allowedWorkloadSPIFFE           string
	allowedOperationsProducerSPIFFE string
	maximumOperationsEventBytes     int64
	audience                        string
	logger                          *slog.Logger
	observability                   *observability.Runtime
	now                             func() time.Time
}

func NewHandler(config ServerConfig) http.Handler {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	audience := config.Audience
	if audience == "" {
		audience = "audit-ledger-service"
	}
	if config.Store == nil || config.AllowedWorkloadSPIFFE == "" {
		panic("audit server configuration is incomplete")
	}
	telemetry := config.Observability
	if telemetry == nil {
		telemetry = observability.NewRuntime(observability.RuntimeConfig{Service: "audit-ledger-service"})
	}
	searcher := config.Searcher
	if searcher == nil {
		if candidate, ok := config.Store.(RecordSearcher); ok {
			searcher = candidate
		}
	}
	maximumOperationsEventBytes := config.MaximumOperationsEventBytes
	if maximumOperationsEventBytes == 0 {
		maximumOperationsEventBytes = 64 * 1024
	}
	if maximumOperationsEventBytes < 1 || maximumOperationsEventBytes > 1024*1024 {
		panic("audit operations event size limit is invalid")
	}
	return &server{
		store:                           config.Store,
		searcher:                        searcher,
		operationsWriter:                config.OperationsWriter,
		allowedWorkloadSPIFFE:           config.AllowedWorkloadSPIFFE,
		allowedOperationsProducerSPIFFE: config.AllowedOperationsProducerSPIFFE,
		maximumOperationsEventBytes:     maximumOperationsEventBytes,
		audience:                        audience,
		logger:                          logger,
		observability:                   telemetry,
		now:                             now,
	}
}

func (server *server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	startedAt := server.now()
	ctx := server.observability.Tracer.ExtractHTTP(request.Context(), request.Header)
	ctx, span := server.observability.Tracer.Start(ctx, "http.audit.query", observability.SpanKindServer, map[string]any{
		"http.request.method": request.Method, "http.route": safeAuditPath(request.URL.Path),
	})
	request = request.WithContext(ctx)
	writer.Header().Set("traceparent", observability.Traceparent(ctx))
	status := http.StatusOK
	defer func() {
		result := "ok"
		if status >= http.StatusBadRequest {
			result = "error"
			span.SetStatus("error", http.StatusText(status))
		} else {
			span.SetStatus("ok", "")
		}
		span.SetAttributes(map[string]any{"http.response.status_code": status})
		span.End()
		_ = server.observability.Metrics.AddCounter("s0_http_requests_total", "Audit query HTTP requests.", map[string]string{"service": "audit-ledger-service", "route": safeAuditPath(request.URL.Path), "method": request.Method, "result": result}, 1)
		_ = server.observability.Metrics.ObserveHistogram("s0_http_request_duration_seconds", "Audit query HTTP latency.", map[string]string{"service": "audit-ledger-service", "route": safeAuditPath(request.URL.Path), "method": request.Method}, server.now().Sub(startedAt).Seconds(), nil)
		server.logger.InfoContext(request.Context(), "audit_query_request",
			"method", request.Method,
			"path", safeAuditPath(request.URL.Path),
			"status", status,
			"duration_ms", server.now().Sub(startedAt).Milliseconds(),
		)
	}()

	if request.Method == http.MethodPost && request.URL.Path == OperationsAuditPath {
		status = server.serveOperationsIngest(writer, request)
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == AuditSearchPath {
		status = server.serveAuditSearch(writer, request)
		return
	}
	if request.Method != http.MethodGet || !strings.HasPrefix(request.URL.Path, SessionAuditPathPrefix) {
		status = http.StatusNotFound
		writeAuditProblem(writer, status, "AUDIT_ROUTE_NOT_FOUND", "Audit route not found")
		return
	}
	messageID := strings.TrimPrefix(request.URL.Path, SessionAuditPathPrefix)
	if messageID == "" || strings.Contains(messageID, "/") {
		status = http.StatusNotFound
		writeAuditProblem(writer, status, "AUDIT_RECORD_NOT_FOUND", "Audit record not found")
		return
	}
	for _, header := range []string{"X-Principal", "X-Roles", "X-Tenant-ID", "X-Site-ID", "X-Admin"} {
		if request.Header.Get(header) != "" {
			status = http.StatusBadRequest
			writeAuditProblem(writer, status, "AUDIT_FORGED_IDENTITY_HEADER", "Caller identity headers are not accepted")
			return
		}
	}
	certificate, peerSPIFFE, ok := verifiedPeer(request)
	if !ok || peerSPIFFE != server.allowedWorkloadSPIFFE {
		status = http.StatusUnauthorized
		writeAuditProblem(writer, status, "AUDIT_WORKLOAD_IDENTITY_INVALID", "Workload identity is invalid")
		return
	}
	grant := request.Header.Get("X-Delegation-Grant")
	claims, err := identitycontext.VerifyDelegation(certificate.PublicKey, grant)
	if err != nil {
		status = http.StatusUnauthorized
		writeAuditProblem(writer, status, "AUDIT_DELEGATION_INVALID", "Delegation grant is invalid")
		return
	}
	expectedScope := "tenant:" + claims.TenantID
	if err := identitycontext.ValidateDelegation(claims, server.now(), peerSPIFFE, server.audience, "audit:read", expectedScope); err != nil {
		status = http.StatusForbidden
		writeAuditProblem(writer, status, "AUDIT_DELEGATION_REJECTED", "Delegation grant is not authorized for this audit query")
		return
	}
	record, err := server.store.GetRecord(request.Context(), claims.TenantID, messageID)
	if errors.Is(err, ErrRecordNotFound) {
		status = http.StatusNotFound
		writeAuditProblem(writer, status, "AUDIT_RECORD_NOT_FOUND", "Audit record not found")
		return
	}
	if err != nil {
		status = http.StatusServiceUnavailable
		writeAuditProblem(writer, status, "AUDIT_QUERY_UNAVAILABLE", "Audit Ledger is temporarily unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(record)
}

func (server *server) serveAuditSearch(writer http.ResponseWriter, request *http.Request) int {
	if server.searcher == nil {
		writeAuditProblem(writer, http.StatusNotFound, "AUDIT_ROUTE_NOT_FOUND", "Audit route not found")
		return http.StatusNotFound
	}
	for _, header := range []string{"X-Principal", "X-Roles", "X-Tenant-ID", "X-Site-ID", "X-Admin"} {
		if request.Header.Get(header) != "" {
			writeAuditProblem(writer, http.StatusBadRequest, "AUDIT_FORGED_IDENTITY_HEADER", "Caller identity headers are not accepted")
			return http.StatusBadRequest
		}
	}
	certificate, peerSPIFFE, ok := verifiedPeer(request)
	if !ok || peerSPIFFE != server.allowedWorkloadSPIFFE {
		writeAuditProblem(writer, http.StatusUnauthorized, "AUDIT_WORKLOAD_IDENTITY_INVALID", "Workload identity is invalid")
		return http.StatusUnauthorized
	}
	claims, err := identitycontext.VerifyDelegation(certificate.PublicKey, request.Header.Get("X-Delegation-Grant"))
	if err != nil {
		writeAuditProblem(writer, http.StatusUnauthorized, "AUDIT_DELEGATION_INVALID", "Delegation grant is invalid")
		return http.StatusUnauthorized
	}
	if err := identitycontext.ValidateDelegation(claims, server.now(), peerSPIFFE, server.audience, "audit:read", "tenant:"+claims.TenantID); err != nil {
		writeAuditProblem(writer, http.StatusForbidden, "AUDIT_DELEGATION_REJECTED", "Delegation grant is not authorized for this audit query")
		return http.StatusForbidden
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 32<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var filter SearchFilter
	if err := decoder.Decode(&filter); err != nil {
		writeAuditProblem(writer, http.StatusBadRequest, "AUDIT_SEARCH_INVALID", "Audit search filter is invalid")
		return http.StatusBadRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAuditProblem(writer, http.StatusBadRequest, "AUDIT_SEARCH_INVALID", "Audit search filter is invalid")
		return http.StatusBadRequest
	}
	records, err := server.searcher.SearchRecords(request.Context(), claims.TenantID, filter)
	if err != nil {
		writeAuditProblem(writer, http.StatusBadRequest, "AUDIT_SEARCH_INVALID", "Audit search filter is invalid")
		return http.StatusBadRequest
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(struct {
		Items []SearchRecord `json:"items"`
	}{Items: records})
	return http.StatusOK
}

func (server *server) serveOperationsIngest(writer http.ResponseWriter, request *http.Request) int {
	if server.operationsWriter == nil || server.allowedOperationsProducerSPIFFE == "" {
		writeAuditProblem(writer, http.StatusNotFound, "AUDIT_ROUTE_NOT_FOUND", "Audit route not found")
		return http.StatusNotFound
	}
	for _, header := range []string{
		"Authorization", "Cookie", "X-Delegation-Grant", "X-Principal", "X-Roles",
		"X-Tenant-ID", "X-Site-ID", "X-Admin",
	} {
		if request.Header.Get(header) != "" {
			writeAuditProblem(writer, http.StatusBadRequest, "AUDIT_FORGED_IDENTITY_HEADER", "Caller identity headers are not accepted")
			return http.StatusBadRequest
		}
	}
	_, peerSPIFFE, ok := verifiedPeer(request)
	if !ok || peerSPIFFE != server.allowedOperationsProducerSPIFFE {
		writeAuditProblem(writer, http.StatusUnauthorized, "AUDIT_WORKLOAD_IDENTITY_INVALID", "Workload identity is invalid")
		return http.StatusUnauthorized
	}
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") {
		writeAuditProblem(writer, http.StatusUnsupportedMediaType, "AUDIT_CONTENT_TYPE_INVALID", "Operations Audit events require JSON")
		return http.StatusUnsupportedMediaType
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 768 || strings.ContainsAny(idempotencyKey, "\r\n") {
		writeAuditProblem(writer, http.StatusBadRequest, "AUDIT_IDEMPOTENCY_KEY_INVALID", "Operations Audit Idempotency Key is invalid")
		return http.StatusBadRequest
	}
	request.Body = http.MaxBytesReader(writer, request.Body, server.maximumOperationsEventBytes)
	payload, err := io.ReadAll(request.Body)
	if err != nil {
		writeAuditProblem(writer, http.StatusRequestEntityTooLarge, "AUDIT_EVENT_TOO_LARGE", "Operations Audit event is too large")
		return http.StatusRequestEntityTooLarge
	}
	event, err := operationsauditevent.Decode(payload)
	if err != nil || event.EventID != idempotencyKey || event.Actor.ExecutingSPIFFEID != peerSPIFFE {
		writeAuditProblem(writer, http.StatusBadRequest, "AUDIT_EVENT_INVALID", "Operations Audit event is invalid")
		return http.StatusBadRequest
	}
	_, err = server.operationsWriter.ConsumeOperations(request.Context(), payload, MessageMetadata{
		Topic: "operations-http", Partition: 0, Offset: 0, ReceivedAt: server.now().UTC(),
	})
	if errors.Is(err, ErrEnvelopeConflict) {
		writeAuditProblem(writer, http.StatusConflict, "AUDIT_EVENT_CONFLICT", "Operations Audit event identity conflicts with existing content")
		return http.StatusConflict
	}
	if err != nil {
		writeAuditProblem(writer, http.StatusServiceUnavailable, "AUDIT_INGEST_UNAVAILABLE", "Audit Ledger is temporarily unavailable")
		return http.StatusServiceUnavailable
	}
	writer.WriteHeader(http.StatusNoContent)
	return http.StatusNoContent
}

func verifiedPeer(request *http.Request) (*x509CertificateView, string, bool) {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
		return nil, "", false
	}
	certificate := request.TLS.PeerCertificates[0]
	if len(certificate.URIs) != 1 || certificate.URIs[0].Scheme != "spiffe" {
		return nil, "", false
	}
	return &x509CertificateView{PublicKey: certificate.PublicKey}, certificate.URIs[0].String(), true
}

type x509CertificateView struct {
	PublicKey any
}

func safeAuditPath(path string) string {
	if path == OperationsAuditPath {
		return OperationsAuditPath
	}
	if path == AuditSearchPath {
		return AuditSearchPath
	}
	if strings.HasPrefix(path, SessionAuditPathPrefix) {
		return SessionAuditPathPrefix + "{messageId}"
	}
	return "unmatched"
}

func writeAuditProblem(writer http.ResponseWriter, status int, code, detail string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"type":      "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		"title":     http.StatusText(status),
		"status":    status,
		"detail":    detail,
		"code":      code,
		"retryable": status >= 500,
	})
}
