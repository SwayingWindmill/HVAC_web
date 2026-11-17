package connectivity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/packettap"
)

const maxDiagnosticsRequestBodyBytes = 32 << 10

type DiagnosticsStore interface {
	ListGatewayQuarantineForOperator(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, limit int) ([]GatewayQuarantineRecord, error)
	CheckGatewayOperatorScope(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) error
}

func NewDiagnosticsHandler(store DiagnosticsStore, tap *packettap.Manager, authorize CredentialAuthorizer) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /internal/v1/gateways/{gatewayId}/quarantine", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeDiagnosticsErrorJSON(w, http.StatusServiceUnavailable, "DIAGNOSTICS_UNAVAILABLE", "gateway diagnostics store unavailable")
			return
		}
		claims, ok := authorizeDiagnosticsHTTP(w, r, authorize, registryauth.ActionDeviceRead)
		if !ok {
			return
		}
		limit := defaultQuarantineListLimit
		if r.URL.Query().Has("limit") {
			rawLimit := strings.TrimSpace(r.URL.Query().Get("limit"))
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil || parsed < 1 || parsed > maxQuarantineListLimit {
				writeDiagnosticsErrorJSON(w, http.StatusBadRequest, "DIAGNOSTICS_REQUEST_INVALID", "limit must be an integer between 1 and 50")
				return
			}
			limit = parsed
		}
		items, err := store.ListGatewayQuarantineForOperator(r.Context(), claims, r.PathValue("gatewayId"), limit)
		if err != nil {
			writeDiagnosticsError(w, err)
			return
		}
		if items == nil {
			items = []GatewayQuarantineRecord{}
		}
		writeDiagnosticsJSONResult(w, map[string]any{"items": items}, nil)
	})

	mux.HandleFunc("GET /internal/v1/gateways/{gatewayId}/packet-capture", func(w http.ResponseWriter, r *http.Request) {
		if store == nil || tap == nil {
			writeDiagnosticsErrorJSON(w, http.StatusServiceUnavailable, "DIAGNOSTICS_UNAVAILABLE", "gateway packet capture service unavailable")
			return
		}
		claims, ok := authorizeDiagnosticsHTTP(w, r, authorize, registryauth.ActionDeviceRead)
		if !ok {
			return
		}
		gatewayID := r.PathValue("gatewayId")
		if err := store.CheckGatewayOperatorScope(r.Context(), claims, gatewayID); err != nil {
			writeDiagnosticsError(w, err)
			return
		}
		writeDiagnosticsJSONResult(w, tap.Snapshot(gatewayID), nil)
	})

	mux.HandleFunc("POST /internal/v1/gateways/{gatewayId}/packet-capture/start", func(w http.ResponseWriter, r *http.Request) {
		if store == nil || tap == nil {
			writeDiagnosticsErrorJSON(w, http.StatusServiceUnavailable, "DIAGNOSTICS_UNAVAILABLE", "gateway packet capture service unavailable")
			return
		}
		claims, ok := authorizeDiagnosticsHTTP(w, r, authorize, registryauth.ActionDeviceWrite)
		if !ok {
			return
		}
		var input struct {
			DurationSeconds *int `json:"durationSeconds"`
		}
		if err := decodeDiagnosticsOptionalJSON(w, r, &input, maxDiagnosticsRequestBodyBytes); err != nil {
			writeDiagnosticsErrorJSON(w, http.StatusBadRequest, "DIAGNOSTICS_REQUEST_INVALID", "invalid packet capture start request")
			return
		}
		durationSeconds := 0
		if input.DurationSeconds != nil {
			if *input.DurationSeconds < packettap.MinCaptureDurationSeconds || *input.DurationSeconds > packettap.MaxCaptureDurationSeconds {
				writeDiagnosticsErrorJSON(w, http.StatusBadRequest, "DIAGNOSTICS_REQUEST_INVALID", "durationSeconds must be between 30 and 900")
				return
			}
			durationSeconds = *input.DurationSeconds
		}
		gatewayID := r.PathValue("gatewayId")
		if err := store.CheckGatewayOperatorScope(r.Context(), claims, gatewayID); err != nil {
			writeDiagnosticsError(w, err)
			return
		}
		snap, err := tap.Start(gatewayID, durationSeconds)
		writeDiagnosticsJSONResult(w, snap, err)
	})

	mux.HandleFunc("POST /internal/v1/gateways/{gatewayId}/packet-capture/stop", func(w http.ResponseWriter, r *http.Request) {
		if store == nil || tap == nil {
			writeDiagnosticsErrorJSON(w, http.StatusServiceUnavailable, "DIAGNOSTICS_UNAVAILABLE", "gateway packet capture service unavailable")
			return
		}
		claims, ok := authorizeDiagnosticsHTTP(w, r, authorize, registryauth.ActionDeviceWrite)
		if !ok {
			return
		}
		gatewayID := r.PathValue("gatewayId")
		if err := store.CheckGatewayOperatorScope(r.Context(), claims, gatewayID); err != nil {
			writeDiagnosticsError(w, err)
			return
		}
		writeDiagnosticsJSONResult(w, tap.Stop(gatewayID), nil)
	})

	return mux
}

func authorizeDiagnosticsHTTP(w http.ResponseWriter, r *http.Request, authorize CredentialAuthorizer, action registryauth.Action) (registryauth.GrantClaims, bool) {
	if authorize == nil {
		writeDiagnosticsErrorJSON(w, http.StatusForbidden, "CREDENTIAL_FORBIDDEN", "gateway diagnostics authorizer unavailable")
		return registryauth.GrantClaims{}, false
	}
	claims, err := authorize(r, action)
	if err != nil {
		writeDiagnosticsError(w, err)
		return registryauth.GrantClaims{}, false
	}
	return claims, true
}

func decodeDiagnosticsOptionalJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if r.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
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

func writeDiagnosticsJSONResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeDiagnosticsError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func writeDiagnosticsError(w http.ResponseWriter, err error) {
	status, code := mapDiagnosticsError(err)
	message := err.Error()
	if status >= http.StatusInternalServerError {
		message = "gateway diagnostics service unavailable"
	}
	writeDiagnosticsErrorJSON(w, status, code, message)
}

func writeDiagnosticsErrorJSON(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
}

func mapDiagnosticsError(err error) (int, string) {
	switch {
	case errors.Is(err, ErrEnrollmentRejected),
		errors.Is(err, packettap.ErrInvalidCaptureGateway),
		errors.Is(err, packettap.ErrInvalidCaptureDuration):
		return http.StatusBadRequest, "DIAGNOSTICS_REQUEST_INVALID"
	case errors.Is(err, ErrCredentialForbidden):
		return http.StatusForbidden, "CREDENTIAL_FORBIDDEN"
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, "GATEWAY_NOT_FOUND"
	case errors.Is(err, packettap.ErrCaptureCapacityExceeded):
		return http.StatusTooManyRequests, "PACKET_CAPTURE_CAPACITY_EXCEEDED"
	default:
		return http.StatusInternalServerError, "DIAGNOSTICS_SERVICE_UNAVAILABLE"
	}
}
