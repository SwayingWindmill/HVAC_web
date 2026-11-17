package connectivity

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const maxConfigShadowRequestBodyBytes = 1 << 20

type GatewayPeerVerifier interface {
	VerifyActiveCredential(ctx context.Context, peer *x509.Certificate) (string, error)
}

type ConfigShadowStore interface {
	GetGatewayConfigShadowForOperator(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (GatewayConfigShadow, error)
	UpdateGatewayDesiredConfig(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, input UpdateDesiredConfigInput) (GatewayConfigShadow, error)
	PullGatewayDesiredConfig(ctx context.Context, gatewayID string, sinceRevision int64) (GatewayShadowPullResponse, error)
	ReportGatewayAppliedConfig(ctx context.Context, gatewayID string, input GatewayShadowReportInput) (GatewayConfigShadow, error)
}

func NewConfigShadowHandler(
	store ConfigShadowStore,
	credentials GatewayPeerVerifier,
	authorize CredentialAuthorizer,
	onDesiredUpdated func(gatewayID string),
) http.Handler {
	return NewConfigShadowHandlerWithPresence(store, credentials, authorize, onDesiredUpdated, nil)
}

func NewConfigShadowHandlerWithPresence(
	store ConfigShadowStore,
	credentials GatewayPeerVerifier,
	authorize CredentialAuthorizer,
	onDesiredUpdated func(gatewayID string),
	onGatewaySeen func(gatewayID string),
) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /internal/v1/gateways/{gatewayId}/config-shadow", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeShadowErrorJSON(w, http.StatusServiceUnavailable, "CONFIG_SHADOW_UNAVAILABLE", "config shadow store unavailable")
			return
		}
		claims, ok := authorizeShadowHTTP(w, r, authorize, registryauth.ActionDeviceRead)
		if !ok {
			return
		}
		shadow, err := store.GetGatewayConfigShadowForOperator(r.Context(), claims, r.PathValue("gatewayId"))
		writeShadowJSONResult(w, shadow, err)
	})

	mux.HandleFunc("PUT /internal/v1/gateways/{gatewayId}/config-shadow", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeShadowErrorJSON(w, http.StatusServiceUnavailable, "CONFIG_SHADOW_UNAVAILABLE", "config shadow store unavailable")
			return
		}
		claims, ok := authorizeShadowHTTP(w, r, authorize, registryauth.ActionDeviceWrite)
		if !ok {
			return
		}
		var input UpdateDesiredConfigInput
		if err := decodeShadowStrictJSON(w, r, &input, maxConfigShadowRequestBodyBytes); err != nil {
			writeShadowErrorJSON(w, http.StatusBadRequest, "CONFIG_SHADOW_REQUEST_INVALID", "invalid config shadow update request")
			return
		}
		gatewayID := r.PathValue("gatewayId")
		shadow, err := store.UpdateGatewayDesiredConfig(r.Context(), claims, gatewayID, input)
		if err == nil && onDesiredUpdated != nil {
			onDesiredUpdated(gatewayID)
		}
		writeShadowJSONResult(w, shadow, err)
	})

	mux.HandleFunc("GET /gateway/v1/config-shadow", func(w http.ResponseWriter, r *http.Request) {
		if store == nil || credentials == nil {
			writeShadowErrorJSON(w, http.StatusServiceUnavailable, "CONFIG_SHADOW_UNAVAILABLE", "config shadow service unavailable")
			return
		}
		peer, err := ParseGatewayPeerCert(r)
		if err != nil {
			writeShadowError(w, err)
			return
		}
		gatewayID, err := credentials.VerifyActiveCredential(r.Context(), peer)
		if err != nil {
			writeShadowError(w, err)
			return
		}
		if onGatewaySeen != nil {
			onGatewaySeen(gatewayID)
		}
		var sinceRevision int64
		if raw := strings.TrimSpace(r.URL.Query().Get("sinceRevision")); raw != "" {
			parsed, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || parsed < 0 {
				writeShadowErrorJSON(w, http.StatusBadRequest, "CONFIG_SHADOW_REQUEST_INVALID", "sinceRevision must be a non-negative integer")
				return
			}
			sinceRevision = parsed
		}
		resp, err := store.PullGatewayDesiredConfig(r.Context(), gatewayID, sinceRevision)
		writeShadowJSONResult(w, resp, err)
	})

	mux.HandleFunc("POST /gateway/v1/config-shadow/report", func(w http.ResponseWriter, r *http.Request) {
		if store == nil || credentials == nil {
			writeShadowErrorJSON(w, http.StatusServiceUnavailable, "CONFIG_SHADOW_UNAVAILABLE", "config shadow service unavailable")
			return
		}
		peer, err := ParseGatewayPeerCert(r)
		if err != nil {
			writeShadowError(w, err)
			return
		}
		gatewayID, err := credentials.VerifyActiveCredential(r.Context(), peer)
		if err != nil {
			writeShadowError(w, err)
			return
		}
		if onGatewaySeen != nil {
			onGatewaySeen(gatewayID)
		}
		var input GatewayShadowReportInput
		if err := decodeShadowStrictJSON(w, r, &input, maxConfigShadowRequestBodyBytes); err != nil {
			writeShadowErrorJSON(w, http.StatusBadRequest, "CONFIG_SHADOW_REQUEST_INVALID", "invalid config shadow report request")
			return
		}
		shadow, err := store.ReportGatewayAppliedConfig(r.Context(), gatewayID, input)
		writeShadowJSONResult(w, shadow, err)
	})

	return mux
}

func authorizeShadowHTTP(w http.ResponseWriter, r *http.Request, authorize CredentialAuthorizer, action registryauth.Action) (registryauth.GrantClaims, bool) {
	if authorize == nil {
		writeShadowErrorJSON(w, http.StatusForbidden, "CREDENTIAL_FORBIDDEN", "config shadow authorizer unavailable")
		return registryauth.GrantClaims{}, false
	}
	claims, err := authorize(r, action)
	if err != nil {
		writeShadowError(w, err)
		return registryauth.GrantClaims{}, false
	}
	return claims, true
}

func decodeShadowStrictJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON content")
	}
	return nil
}

func writeShadowJSONResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeShadowError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func writeShadowError(w http.ResponseWriter, err error) {
	status, code := mapConfigShadowError(err)
	message := err.Error()
	if status >= http.StatusInternalServerError {
		message = "gateway config shadow service unavailable"
	}
	writeShadowErrorJSON(w, status, code, message)
}

func writeShadowErrorJSON(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
}

func mapConfigShadowError(err error) (int, string) {
	switch {
	case errors.Is(err, ErrConfigShadowConflict):
		return http.StatusConflict, "CONFIG_SHADOW_REVISION_CONFLICT"
	case errors.Is(err, ErrConfigShadowInvalid):
		return http.StatusUnprocessableEntity, "CONFIG_SHADOW_INVALID"
	case errors.Is(err, ErrEnrollmentRejected):
		return http.StatusBadRequest, "ENROLLMENT_REJECTED"
	case errors.Is(err, ErrCredentialForbidden):
		return http.StatusForbidden, "CREDENTIAL_FORBIDDEN"
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, "GATEWAY_NOT_FOUND"
	case errors.Is(err, ErrCredentialRevoked):
		return http.StatusConflict, "GATEWAY_REVOKED"
	default:
		return http.StatusInternalServerError, "CONFIG_SHADOW_SERVICE_UNAVAILABLE"
	}
}
