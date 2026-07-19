package iam

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const CurrentPrincipalPath = "/internal/v1/principal/current"

type Config struct {
	AllowedWorkloadSPIFFE string
	Audience              string
	Logger                *slog.Logger
	Now                   func() time.Time
}

type handler struct {
	allowedWorkloadSPIFFE string
	audience              string
	logger                *slog.Logger
	now                   func() time.Time
}

func NewHandler(config Config) http.Handler {
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &handler{
		allowedWorkloadSPIFFE: config.AllowedWorkloadSPIFFE,
		audience:              config.Audience,
		logger:                logger,
		now:                   now,
	}
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	started := h.now()
	status := http.StatusOK
	defer func() {
		h.logger.InfoContext(request.Context(), "iam_request",
			"method", request.Method,
			"path", safePath(request.URL.Path),
			"status", status,
			"duration_ms", h.now().Sub(started).Milliseconds(),
		)
	}()

	if request.URL.Path != CurrentPrincipalPath {
		status = http.StatusNotFound
		writeProblem(writer, status, "IAM_ROUTE_NOT_FOUND", "The requested IAM route does not exist.")
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		status = http.StatusMethodNotAllowed
		writeProblem(writer, status, "IAM_METHOD_NOT_ALLOWED", "This IAM route only supports POST.")
		return
	}
	for _, header := range []string{"X-Principal", "X-Roles", "X-Organization-ID", "X-Site-ID", "X-Admin"} {
		if request.Header.Get(header) != "" {
			status = http.StatusBadRequest
			writeProblem(writer, status, "IAM_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.")
			return
		}
	}

	peerCertificate, spiffeID, ok := peerIdentity(request)
	if !ok || spiffeID != h.allowedWorkloadSPIFFE {
		status = http.StatusUnauthorized
		writeProblem(writer, status, "IAM_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.")
		return
	}
	grant := request.Header.Get("X-Delegation-Grant")
	claims, err := identitycontext.VerifyDelegation(peerCertificate.PublicKey, grant)
	if err != nil {
		status = http.StatusUnauthorized
		writeProblem(writer, status, "IAM_DELEGATION_INVALID", "The delegated identity context is invalid.")
		return
	}
	scope := "session:" + claims.SessionID
	if err := identitycontext.ValidateDelegation(claims, h.now(), spiffeID, h.audience, "principal:read", scope); err != nil {
		status = http.StatusForbidden
		writeProblem(writer, status, "IAM_DELEGATION_REJECTED", "The delegated identity context is not authorized for this operation.")
		return
	}

	response := identitycontext.InternalPrincipalResponse{
		Principal: identitycontext.UserPrincipal{
			Subject:     claims.Subject,
			Issuer:      claims.SubjectIssuer,
			DisplayName: claims.DisplayName,
			Email:       claims.Email,
			Roles:       append([]string(nil), claims.Roles...),
		},
		Context: identitycontext.PrincipalContext{
			InitiatingPrincipal: identitycontext.UserPrincipal{
				Subject:     claims.Subject,
				Issuer:      claims.SubjectIssuer,
				DisplayName: claims.DisplayName,
				Email:       claims.Email,
				Roles:       append([]string(nil), claims.Roles...),
			},
			ExecutingServicePrincipal: identitycontext.ServicePrincipal{
				Service:  "platform-gateway",
				SPIFFEID: spiffeID,
			},
			ActingOrganizationID: claims.ActingOrganizationID,
			Audience:             claims.Audience,
			PolicyRevision:       claims.PolicyRevision,
			DelegationExpiresAt:  time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339),
		},
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(response)
}

func peerIdentity(request *http.Request) (*x509CertificateView, string, bool) {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
		return nil, "", false
	}
	certificate := request.TLS.PeerCertificates[0]
	if len(certificate.URIs) != 1 || certificate.URIs[0] == nil || !strings.HasPrefix(certificate.URIs[0].String(), "spiffe://") {
		return nil, "", false
	}
	return &x509CertificateView{PublicKey: certificate.PublicKey}, certificate.URIs[0].String(), true
}

type x509CertificateView struct {
	PublicKey any
}

func safePath(path string) string {
	if path == CurrentPrincipalPath {
		return path
	}
	return "unmatched"
}

func writeProblem(writer http.ResponseWriter, status int, code, detail string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"type":      "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		"title":     http.StatusText(status),
		"status":    status,
		"detail":    detail,
		"code":      code,
		"retryable": false,
	})
}
