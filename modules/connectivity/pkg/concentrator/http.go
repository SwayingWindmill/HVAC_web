package concentrator

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

func NewHTTPHandler(service *Service, authorize connectivity.CredentialAuthorizer) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /internal/v1/gateways/{gatewayId}/concentrators/{unitId}/status", func(w http.ResponseWriter, r *http.Request) {
		claims, ok := authorizeHTTP(w, r, authorize, registryauth.ActionDeviceRead)
		if !ok {
			return
		}
		unitID, err := ParseUnitID(r.PathValue("unitId"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		resp, err := service.Status(r.Context(), claims, r.PathValue("gatewayId"), unitID)
		writeJSONResult(w, resp, err)
	})

	mux.HandleFunc("PUT /internal/v1/gateways/{gatewayId}/concentrators/{unitId}/params", func(w http.ResponseWriter, r *http.Request) {
		claims, ok := authorizeHTTP(w, r, authorize, registryauth.ActionDeviceWrite)
		if !ok {
			return
		}
		unitID, err := ParseUnitID(r.PathValue("unitId"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		var input ParamsUpdateRequest
		if err := decodeStrictJSON(w, r, &input, 32<<10); err != nil {
			writeServiceError(w, err)
			return
		}
		resp, err := service.UpdateParams(r.Context(), claims, r.PathValue("gatewayId"), unitID, input)
		writeJSONResult(w, resp, err)
	})

	mux.HandleFunc("GET /internal/v1/gateways/{gatewayId}/concentrators/{unitId}/eids", func(w http.ResponseWriter, r *http.Request) {
		claims, ok := authorizeHTTP(w, r, authorize, registryauth.ActionDeviceRead)
		if !ok {
			return
		}
		unitID, err := ParseUnitID(r.PathValue("unitId"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		resp, err := service.EIDs(r.Context(), claims, r.PathValue("gatewayId"), unitID)
		writeJSONResult(w, resp, err)
	})

	mux.HandleFunc("PUT /internal/v1/gateways/{gatewayId}/concentrators/{unitId}/eids", func(w http.ResponseWriter, r *http.Request) {
		claims, ok := authorizeHTTP(w, r, authorize, registryauth.ActionDeviceWrite)
		if !ok {
			return
		}
		unitID, err := ParseUnitID(r.PathValue("unitId"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		var input EIDUpdateRequest
		if err := decodeStrictJSON(w, r, &input, 128<<10); err != nil {
			writeServiceError(w, err)
			return
		}
		resp, err := service.UpdateEIDs(r.Context(), claims, r.PathValue("gatewayId"), unitID, input)
		writeJSONResult(w, resp, err)
	})

	return mux
}

func authorizeHTTP(w http.ResponseWriter, r *http.Request, authorize connectivity.CredentialAuthorizer, action registryauth.Action) (registryauth.GrantClaims, bool) {
	if authorize == nil {
		writeErrorJSON(w, http.StatusForbidden, "CREDENTIAL_FORBIDDEN", "concentrator authorizer unavailable")
		return registryauth.GrantClaims{}, false
	}
	claims, err := authorize(r, action)
	if err != nil {
		writeServiceError(w, err)
		return registryauth.GrantClaims{}, false
	}
	return claims, true
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrInvalidRequest
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}

func writeJSONResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func writeServiceError(w http.ResponseWriter, err error) {
	status, code := MapError(err)
	writeErrorJSON(w, status, code, err.Error())
}

func writeErrorJSON(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
}

func MapError(err error) (int, string) {
	switch {
	case errors.Is(err, ErrProtectedUID):
		return http.StatusConflict, "CONCENTRATOR_UID_PROTECTED"
	case errors.Is(err, ErrDuplicateEID):
		return http.StatusUnprocessableEntity, "CONCENTRATOR_EID_DUPLICATE"
	case errors.Is(err, ErrEIDSlotsInvalid):
		return http.StatusUnprocessableEntity, "CONCENTRATOR_EIDS_INVALID"
	case errors.Is(err, ErrParamsInvalid):
		return http.StatusUnprocessableEntity, "CONCENTRATOR_PARAMS_INVALID"
	case errors.Is(err, ErrReasonRequired):
		return http.StatusUnprocessableEntity, "CONCENTRATOR_REASON_REQUIRED"
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, connectivity.ErrEnrollmentRejected):
		return http.StatusBadRequest, "CONCENTRATOR_REQUEST_INVALID"
	case errors.Is(err, connectivity.ErrCredentialForbidden):
		return http.StatusForbidden, "CREDENTIAL_FORBIDDEN"
	case errors.Is(err, connectivity.ErrNotFound):
		return http.StatusNotFound, "GATEWAY_NOT_FOUND"
	case errors.Is(err, connectivity.ErrCredentialRevoked):
		return http.StatusConflict, "GATEWAY_REVOKED"
	case errors.Is(err, ErrRPCTimeout):
		return http.StatusGatewayTimeout, "CONCENTRATOR_RPC_TIMEOUT"
	case errors.Is(err, ErrTransportUnavailable):
		return http.StatusServiceUnavailable, "CONCENTRATOR_TRANSPORT_UNAVAILABLE"
	case errors.Is(err, ErrEdgeRejected), errors.Is(err, ErrEdgeFailed):
		return http.StatusBadGateway, "CONCENTRATOR_EDGE_FAILED"
	default:
		return http.StatusInternalServerError, "CONCENTRATOR_SERVICE_UNAVAILABLE"
	}
}
