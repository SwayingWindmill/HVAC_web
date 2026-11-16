package connectivity

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

// CredentialAuthorizer validates the workload-bound, current IAM grant. Scope is
// checked against Registry by CredentialService, not by a caller's Tenant header.
type CredentialAuthorizer func(*http.Request, registryauth.Action) (registryauth.GrantClaims, error)

// ParseGatewayPeerCert parses nginx's URL-escaped PEM client certificate header.
func ParseGatewayPeerCert(r *http.Request) (*x509.Certificate, error) {
	encoded, err := url.QueryUnescape(r.Header.Get("X-Gateway-Client-Cert"))
	if err != nil {
		return nil, ErrEnrollmentRejected
	}
	block, rest := pem.Decode([]byte(encoded))
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		return nil, ErrEnrollmentRejected
	}
	peer, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, ErrEnrollmentRejected
	}
	return peer, nil
}

func NewCredentialHandler(service *CredentialService, authorize CredentialAuthorizer) http.Handler {
	return NewCredentialHandlerWithExtensions(service, authorize, nil, nil)
}

func NewCredentialHandlerWithTunnel(service *CredentialService, authorize CredentialAuthorizer, tunnelHandler http.Handler) http.Handler {
	return NewCredentialHandlerWithExtensions(service, authorize, tunnelHandler, nil)
}

func NewCredentialHandlerWithExtensions(service *CredentialService, authorize CredentialAuthorizer, tunnelHandler, concentratorHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	finish := func(w http.ResponseWriter, value any, err error) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			status, code := http.StatusInternalServerError, "CREDENTIAL_SERVICE_UNAVAILABLE"
			switch {
			case errors.Is(err, ErrEnrollmentRejected):
				status, code = 400, "ENROLLMENT_REJECTED"
			case errors.Is(err, ErrCredentialForbidden):
				status, code = 403, "CREDENTIAL_FORBIDDEN"
			case errors.Is(err, ErrCredentialRevoked):
				status, code = 409, "GATEWAY_REVOKED"
			case errors.Is(err, ErrNotFound):
				status, code = 404, "GATEWAY_NOT_FOUND"
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}
	decode := func(w http.ResponseWriter, r *http.Request, value any) error {
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(value) != nil {
			return ErrEnrollmentRejected
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return ErrEnrollmentRejected
		}
		return nil
	}
	mux.HandleFunc("POST /gateway/v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			EnrollmentCode string `json:"enrollmentCode"`
			CSR            string `json:"csrPem"`
		}
		if err := decode(w, r, &input); err != nil {
			finish(w, nil, err)
			return
		}
		value, err := service.Enroll(r.Context(), input.EnrollmentCode, input.CSR)
		finish(w, value, err)
	})
	mux.HandleFunc("POST /gateway/v1/renew", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			CSR string `json:"csrPem"`
		}
		if err := decode(w, r, &input); err != nil {
			finish(w, nil, err)
			return
		}
		// nginx overwrites this header after client-certificate verification; this
		// listener accepts only the platform-gateway workload certificate.
		peer, err := ParseGatewayPeerCert(r)
		if err != nil {
			finish(w, nil, err)
			return
		}
		value, err := service.Renew(r.Context(), peer, input.CSR)
		finish(w, value, err)
	})
	if tunnelHandler != nil {
		mux.Handle("GET /gateway/v1/tunnel", tunnelHandler)
	}
	if concentratorHandler != nil {
		mux.Handle("/internal/v1/gateways/{gatewayId}/concentrators/", concentratorHandler)
	}
	mux.HandleFunc("POST /internal/v1/gateways/{target}", func(w http.ResponseWriter, r *http.Request) {
		gatewayID, ok := strings.CutSuffix(r.PathValue("target"), ":retire")
		if !ok || strings.TrimSpace(gatewayID) == "" {
			http.NotFound(w, r)
			return
		}
		var input struct {
			TenantID string `json:"tenantId"`
		}
		if err := decode(w, r, &input); err != nil {
			finish(w, nil, err)
			return
		}
		if service == nil {
			finish(w, nil, errors.New("credential service is not configured"))
			return
		}
		if err := service.RevokeRetiredGateway(r.Context(), input.TenantID, gatewayID); err != nil {
			finish(w, nil, err)
			return
		}
		finish(w, map[string]any{"gatewayId": gatewayID, "revoked": true}, nil)
	})
	for _, route := range []struct {
		method, path string
		action       registryauth.Action
	}{
		{"GET", "credential", registryauth.ActionDeviceRead},
		{"POST", "enrollment-code", registryauth.ActionDeviceWrite},
		{"POST", "revoke", registryauth.ActionDeviceWrite},
	} {
		mux.HandleFunc(route.method+" /internal/v1/gateways/{gatewayId}/"+route.path, func(w http.ResponseWriter, r *http.Request) {
			claims, err := authorize(r, route.action)
			if err != nil {
				finish(w, nil, err)
				return
			}
			gateway := r.PathValue("gatewayId")
			switch route.path {
			case "credential":
				value, err := service.Status(r.Context(), claims, gateway)
				finish(w, value, err)
			case "enrollment-code":
				value, err := service.GenerateEnrollmentCode(r.Context(), claims, gateway)
				finish(w, value, err)
			case "revoke":
				err := service.Revoke(r.Context(), claims, gateway)
				finish(w, map[string]string{"status": "REVOKED"}, err)
			}
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isRetireRoute := isGatewayRetireRequest(r)
		trusted := false
		if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.VerifiedChains[0]) > 0 {
			for _, uri := range r.TLS.VerifiedChains[0][0].URIs {
				switch uri.String() {
				case "spiffe://hvac.local/platform-gateway":
					trusted = true
				case "spiffe://hvac.local/platform-core-service":
					if isRetireRoute {
						trusted = true
					}
				}
			}
		}
		if !trusted {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/internal/") && r.Header.Get("X-Gateway-Client-Cert") != "" {
			finish(w, nil, ErrCredentialForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func isGatewayRetireRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/internal/v1/gateways/")
	if !ok || strings.Contains(rest, "/") {
		return false
	}
	gatewayID, ok := strings.CutSuffix(rest, ":retire")
	return ok && strings.TrimSpace(gatewayID) != ""
}
