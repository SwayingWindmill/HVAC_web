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

func NewCredentialHandler(service *CredentialService, authorize CredentialAuthorizer) http.Handler {
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
		encoded, err := url.QueryUnescape(r.Header.Get("X-Gateway-Client-Cert"))
		if err != nil {
			finish(w, nil, ErrEnrollmentRejected)
			return
		}
		block, rest := pem.Decode([]byte(encoded))
		if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
			finish(w, nil, ErrEnrollmentRejected)
			return
		}
		peer, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			finish(w, nil, ErrEnrollmentRejected)
			return
		}
		value, err := service.Renew(r.Context(), peer, input.CSR)
		finish(w, value, err)
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
		trusted := false
		if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.VerifiedChains[0]) > 0 {
			for _, uri := range r.TLS.VerifiedChains[0][0].URIs {
				if uri.String() == "spiffe://hvac.local/platform-gateway" {
					trusted = true
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
