package commandservice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const (
	InternalDispatchClaimPath       = "/internal/v1/dispatches:claim"
	InternalDispatchResolvePath     = "/internal/v1/dispatches:resolve"
	InternalVerificationClaimPath   = "/internal/v1/verifications:claim"
	InternalVerificationResolvePath = "/internal/v1/verifications:resolve"
	InternalConnectorPreparePath    = "/internal/v1/connector-evidence:prepare"
	InternalConnectorCompletePath   = "/internal/v1/connector-evidence:complete"
	maximumRuntimeRequestBody       = int64(256 << 10)
)

type RuntimeStore interface {
	ClaimDispatchForCohort(context.Context, string, string, string, string, time.Duration) (commandmodel.DispatchEnvelope, error)
	ResolveDispatch(context.Context, commandmodel.DispatchEnvelope, commandmodel.ConnectorResult) error
	ClaimVerificationForCohort(context.Context, string, string, string, string, time.Duration) (commandmodel.VerificationEnvelope, error)
	ResolveVerification(context.Context, commandmodel.VerificationEnvelope, commandmodel.VerificationResult) error
	PrepareConnectorEvidence(context.Context, commandmodel.PreparedConnectorEvidence) error
	CompleteConnectorEvidence(context.Context, commandmodel.CompletedConnectorEvidence) error
}

type RuntimeHTTPConfig struct {
	Store            RuntimeStore
	DispatcherSPIFFE string
	VerifierSPIFFE   string
	OrganizationID   string
	SiteID           string
	DeviceID         string
}

type runtimeHTTPHandler struct {
	store            RuntimeStore
	dispatcherSPIFFE string
	verifierSPIFFE   string
	organizationID   string
	siteID           string
	deviceID         string
}

type runtimeClaimRequest struct {
	LeaseOwner   string `json:"leaseOwner"`
	LeaseSeconds int64  `json:"leaseSeconds"`
}

type runtimeDispatchResolveRequest struct {
	Envelope commandmodel.DispatchEnvelope `json:"envelope"`
	Result   commandmodel.ConnectorResult  `json:"result"`
}

type runtimeVerificationResolveRequest struct {
	Envelope commandmodel.VerificationEnvelope `json:"envelope"`
	Result   commandmodel.VerificationResult   `json:"result"`
}

type runtimeProblem struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

func NewRuntimeHTTPHandler(config RuntimeHTTPConfig) (http.Handler, error) {
	if config.Store == nil || !validSPIFFE(config.DispatcherSPIFFE) || !validSPIFFE(config.VerifierSPIFFE) || config.DispatcherSPIFFE == config.VerifierSPIFFE ||
		!commandmodel.IsUUIDv7(config.OrganizationID) || !commandmodel.IsUUIDv7(config.SiteID) || !commandmodel.IsUUIDv7(config.DeviceID) {
		return nil, errors.New("command runtime HTTP security configuration is incomplete")
	}
	return &runtimeHTTPHandler{
		store:            config.Store,
		dispatcherSPIFFE: strings.TrimSpace(config.DispatcherSPIFFE),
		verifierSPIFFE:   strings.TrimSpace(config.VerifierSPIFFE),
		organizationID:   strings.TrimSpace(config.OrganizationID),
		siteID:           strings.TrimSpace(config.SiteID),
		deviceID:         strings.TrimSpace(config.DeviceID),
	}, nil
}

func (handler *runtimeHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeRuntimeProblem(writer, http.StatusMethodNotAllowed, "COMMAND_RUNTIME_METHOD_NOT_ALLOWED", false)
		return
	}
	expectedSPIFFE := ""
	switch request.URL.Path {
	case InternalDispatchClaimPath, InternalDispatchResolvePath, InternalConnectorPreparePath, InternalConnectorCompletePath:
		expectedSPIFFE = handler.dispatcherSPIFFE
	case InternalVerificationClaimPath, InternalVerificationResolvePath:
		expectedSPIFFE = handler.verifierSPIFFE
	default:
		writeRuntimeProblem(writer, http.StatusNotFound, "COMMAND_RUNTIME_ROUTE_NOT_FOUND", false)
		return
	}
	if peerSPIFFE(request) != expectedSPIFFE {
		writeRuntimeProblem(writer, http.StatusForbidden, "COMMAND_RUNTIME_WORKLOAD_FORBIDDEN", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumRuntimeRequestBody)
	switch request.URL.Path {
	case InternalDispatchClaimPath:
		handler.claimDispatch(writer, request)
	case InternalDispatchResolvePath:
		handler.resolveDispatch(writer, request)
	case InternalVerificationClaimPath:
		handler.claimVerification(writer, request)
	case InternalVerificationResolvePath:
		handler.resolveVerification(writer, request)
	case InternalConnectorPreparePath:
		handler.prepareConnectorEvidence(writer, request)
	case InternalConnectorCompletePath:
		handler.completeConnectorEvidence(writer, request)
	}
}

func (handler *runtimeHTTPHandler) claimDispatch(writer http.ResponseWriter, request *http.Request) {
	input, ok := decodeRuntimeClaim(writer, request)
	if !ok {
		return
	}
	envelope, err := handler.store.ClaimDispatchForCohort(request.Context(), handler.organizationID, handler.siteID, handler.deviceID, input.LeaseOwner, time.Duration(input.LeaseSeconds)*time.Second)
	if errors.Is(err, ErrNoDispatchAvailable) {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writeRuntimeJSON(writer, http.StatusOK, envelope)
}

func (handler *runtimeHTTPHandler) resolveDispatch(writer http.ResponseWriter, request *http.Request) {
	var input runtimeDispatchResolveRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return
	}
	if !handler.exactCohort(input.Envelope.OrganizationID, input.Envelope.SiteID, input.Envelope.DeviceID) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.ResolveDispatch(request.Context(), input.Envelope, input.Result); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) claimVerification(writer http.ResponseWriter, request *http.Request) {
	input, ok := decodeRuntimeClaim(writer, request)
	if !ok {
		return
	}
	envelope, err := handler.store.ClaimVerificationForCohort(request.Context(), handler.organizationID, handler.siteID, handler.deviceID, input.LeaseOwner, time.Duration(input.LeaseSeconds)*time.Second)
	if errors.Is(err, ErrVerificationNotAvailable) {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writeRuntimeJSON(writer, http.StatusOK, envelope)
}

func (handler *runtimeHTTPHandler) resolveVerification(writer http.ResponseWriter, request *http.Request) {
	var input runtimeVerificationResolveRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return
	}
	if !handler.exactCohort(input.Envelope.OrganizationID, input.Envelope.SiteID, input.Envelope.DeviceID) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.ResolveVerification(request.Context(), input.Envelope, input.Result); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) prepareConnectorEvidence(writer http.ResponseWriter, request *http.Request) {
	var evidence commandmodel.PreparedConnectorEvidence
	if !decodeRuntimeJSON(writer, request, &evidence) {
		return
	}
	if !handler.exactCohort(evidence.OrganizationID, evidence.SiteID, evidence.DeviceID) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.PrepareConnectorEvidence(request.Context(), evidence); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) completeConnectorEvidence(writer http.ResponseWriter, request *http.Request) {
	var evidence commandmodel.CompletedConnectorEvidence
	if !decodeRuntimeJSON(writer, request, &evidence) {
		return
	}
	if !handler.exactCohort(evidence.OrganizationID, evidence.SiteID, evidence.DeviceID) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.CompleteConnectorEvidence(request.Context(), evidence); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) exactCohort(organizationID, siteID, deviceID string) bool {
	return handler != nil && organizationID == handler.organizationID && siteID == handler.siteID && deviceID == handler.deviceID
}

func decodeRuntimeClaim(writer http.ResponseWriter, request *http.Request) (runtimeClaimRequest, bool) {
	var input runtimeClaimRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return runtimeClaimRequest{}, false
	}
	if strings.TrimSpace(input.LeaseOwner) == "" || input.LeaseSeconds <= 0 || input.LeaseSeconds > 120 {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return runtimeClaimRequest{}, false
	}
	return input, true
}

func decodeRuntimeJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || ensureRuntimeEOF(decoder) != nil {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return false
	}
	return true
}

func ensureRuntimeEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

func peerSPIFFE(request *http.Request) string {
	if request == nil || request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		return ""
	}
	certificate := request.TLS.PeerCertificates[0]
	if certificate == nil || len(certificate.URIs) != 1 || certificate.URIs[0] == nil {
		return ""
	}
	identity := certificate.URIs[0].String()
	if !validSPIFFE(identity) {
		return ""
	}
	return identity
}

func validSPIFFE(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "spiffe://") && len(value) > len("spiffe://")
}

func writeRuntimeStoreError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
	case errors.Is(err, ErrCommandNotFound):
		writeRuntimeProblem(writer, http.StatusNotFound, "COMMAND_RUNTIME_COMMAND_NOT_FOUND", false)
	case errors.Is(err, ErrStaleFence):
		writeRuntimeProblem(writer, http.StatusConflict, "COMMAND_RUNTIME_STALE_FENCE", false)
	default:
		writeRuntimeProblem(writer, http.StatusServiceUnavailable, "COMMAND_RUNTIME_UNAVAILABLE", true)
	}
}

func writeRuntimeProblem(writer http.ResponseWriter, status int, code string, retryable bool) {
	writeRuntimeJSON(writer, status, runtimeProblem{Code: code, Retryable: retryable})
}

func writeRuntimeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
