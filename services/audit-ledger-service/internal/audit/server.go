package audit

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const SessionAuditPathPrefix = "/internal/v1/audit/session-events/"

type RecordReader interface {
	GetRecord(context.Context, string, string) (Record, error)
}

type ServerConfig struct {
	Store                 RecordReader
	AllowedWorkloadSPIFFE string
	Audience              string
	Logger                *slog.Logger
	Now                   func() time.Time
}

type server struct {
	store                 RecordReader
	allowedWorkloadSPIFFE string
	audience              string
	logger                *slog.Logger
	now                   func() time.Time
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
	return &server{
		store:                 config.Store,
		allowedWorkloadSPIFFE: config.AllowedWorkloadSPIFFE,
		audience:              audience,
		logger:                logger,
		now:                   now,
	}
}

func (server *server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	startedAt := server.now()
	status := http.StatusOK
	defer func() {
		server.logger.InfoContext(request.Context(), "audit_query_request",
			"method", request.Method,
			"path", safeAuditPath(request.URL.Path),
			"status", status,
			"duration_ms", server.now().Sub(startedAt).Milliseconds(),
		)
	}()

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
	for _, header := range []string{"X-Principal", "X-Roles", "X-Organization-ID", "X-Site-ID", "X-Admin"} {
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
	expectedScope := "organization:" + claims.ActingOrganizationID
	if err := identitycontext.ValidateDelegation(claims, server.now(), peerSPIFFE, server.audience, "audit:read", expectedScope); err != nil {
		status = http.StatusForbidden
		writeAuditProblem(writer, status, "AUDIT_DELEGATION_REJECTED", "Delegation grant is not authorized for this audit query")
		return
	}
	if !containsAuditRole(claims.Roles) {
		status = http.StatusForbidden
		writeAuditProblem(writer, status, "AUDIT_QUERY_FORBIDDEN", "The initiating principal cannot read audit records")
		return
	}
	record, err := server.store.GetRecord(request.Context(), claims.ActingOrganizationID, messageID)
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

func containsAuditRole(roles []string) bool {
	for _, role := range roles {
		if role == "audit-reader" || role == "platform-admin" {
			return true
		}
	}
	return false
}

func safeAuditPath(path string) string {
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
