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
	Cohorts          []RuntimeCohort
}

type RuntimeCohort struct {
	DispatcherSPIFFE string `json:"dispatcherSpiffe"`
	VerifierSPIFFE   string `json:"verifierSpiffe"`
	OrganizationID   string `json:"organizationId"`
	SiteID           string `json:"siteId"`
	DeviceID         string `json:"deviceId"`
}

type runtimeHTTPHandler struct {
	store       RuntimeStore
	dispatchers map[string]RuntimeCohort
	verifiers   map[string]RuntimeCohort
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
	cohorts, err := normalizedRuntimeCohorts(config)
	if config.Store == nil || err != nil {
		return nil, errors.New("command runtime HTTP security configuration is incomplete")
	}
	dispatchers := make(map[string]RuntimeCohort, len(cohorts))
	verifiers := make(map[string]RuntimeCohort, len(cohorts))
	for _, cohort := range cohorts {
		if _, duplicate := dispatchers[cohort.DispatcherSPIFFE]; duplicate {
			return nil, errors.New("command runtime HTTP dispatcher identity is duplicated")
		}
		if _, duplicate := verifiers[cohort.VerifierSPIFFE]; duplicate {
			return nil, errors.New("command runtime HTTP verifier identity is duplicated")
		}
		dispatchers[cohort.DispatcherSPIFFE] = cohort
		verifiers[cohort.VerifierSPIFFE] = cohort
	}
	return &runtimeHTTPHandler{store: config.Store, dispatchers: dispatchers, verifiers: verifiers}, nil
}

func normalizedRuntimeCohorts(config RuntimeHTTPConfig) ([]RuntimeCohort, error) {
	cohorts := config.Cohorts
	if len(cohorts) == 0 {
		cohorts = []RuntimeCohort{{
			DispatcherSPIFFE: config.DispatcherSPIFFE,
			VerifierSPIFFE:   config.VerifierSPIFFE,
			OrganizationID:   config.OrganizationID,
			SiteID:           config.SiteID,
			DeviceID:         config.DeviceID,
		}}
	}
	if len(cohorts) == 0 || len(cohorts) > 64 {
		return nil, errors.New("runtime cohort count is invalid")
	}
	seenDevices := make(map[string]struct{}, len(cohorts))
	for index := range cohorts {
		cohort := &cohorts[index]
		cohort.DispatcherSPIFFE = strings.TrimSpace(cohort.DispatcherSPIFFE)
		cohort.VerifierSPIFFE = strings.TrimSpace(cohort.VerifierSPIFFE)
		cohort.OrganizationID = strings.TrimSpace(cohort.OrganizationID)
		cohort.SiteID = strings.TrimSpace(cohort.SiteID)
		cohort.DeviceID = strings.TrimSpace(cohort.DeviceID)
		if !validSPIFFE(cohort.DispatcherSPIFFE) || !validSPIFFE(cohort.VerifierSPIFFE) || cohort.DispatcherSPIFFE == cohort.VerifierSPIFFE ||
			!commandmodel.IsUUIDv7(cohort.OrganizationID) || !commandmodel.IsUUIDv7(cohort.SiteID) || !commandmodel.IsUUIDv7(cohort.DeviceID) {
			return nil, errors.New("runtime cohort is invalid")
		}
		deviceKey := cohort.OrganizationID + "\x00" + cohort.SiteID + "\x00" + cohort.DeviceID
		if _, duplicate := seenDevices[deviceKey]; duplicate {
			return nil, errors.New("runtime cohort Device is duplicated")
		}
		seenDevices[deviceKey] = struct{}{}
	}
	return cohorts, nil
}

func (handler *runtimeHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeRuntimeProblem(writer, http.StatusMethodNotAllowed, "COMMAND_RUNTIME_METHOD_NOT_ALLOWED", false)
		return
	}
	identity := peerSPIFFE(request)
	var cohort RuntimeCohort
	var allowed bool
	switch request.URL.Path {
	case InternalDispatchClaimPath, InternalDispatchResolvePath, InternalConnectorPreparePath, InternalConnectorCompletePath:
		cohort, allowed = handler.dispatchers[identity]
	case InternalVerificationClaimPath, InternalVerificationResolvePath:
		cohort, allowed = handler.verifiers[identity]
	default:
		writeRuntimeProblem(writer, http.StatusNotFound, "COMMAND_RUNTIME_ROUTE_NOT_FOUND", false)
		return
	}
	if !allowed {
		writeRuntimeProblem(writer, http.StatusForbidden, "COMMAND_RUNTIME_WORKLOAD_FORBIDDEN", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumRuntimeRequestBody)
	switch request.URL.Path {
	case InternalDispatchClaimPath:
		handler.claimDispatch(writer, request, cohort)
	case InternalDispatchResolvePath:
		handler.resolveDispatch(writer, request, cohort)
	case InternalVerificationClaimPath:
		handler.claimVerification(writer, request, cohort)
	case InternalVerificationResolvePath:
		handler.resolveVerification(writer, request, cohort)
	case InternalConnectorPreparePath:
		handler.prepareConnectorEvidence(writer, request, cohort)
	case InternalConnectorCompletePath:
		handler.completeConnectorEvidence(writer, request, cohort)
	}
}

func (handler *runtimeHTTPHandler) claimDispatch(writer http.ResponseWriter, request *http.Request, cohort RuntimeCohort) {
	input, ok := decodeRuntimeClaim(writer, request)
	if !ok {
		return
	}
	envelope, err := handler.store.ClaimDispatchForCohort(request.Context(), cohort.OrganizationID, cohort.SiteID, cohort.DeviceID, input.LeaseOwner, time.Duration(input.LeaseSeconds)*time.Second)
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

func (handler *runtimeHTTPHandler) resolveDispatch(writer http.ResponseWriter, request *http.Request, cohort RuntimeCohort) {
	var input runtimeDispatchResolveRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return
	}
	if !exactRuntimeCohort(cohort, input.Envelope.OrganizationID, input.Envelope.SiteID, input.Envelope.DeviceID) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.ResolveDispatch(request.Context(), input.Envelope, input.Result); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) claimVerification(writer http.ResponseWriter, request *http.Request, cohort RuntimeCohort) {
	input, ok := decodeRuntimeClaim(writer, request)
	if !ok {
		return
	}
	envelope, err := handler.store.ClaimVerificationForCohort(request.Context(), cohort.OrganizationID, cohort.SiteID, cohort.DeviceID, input.LeaseOwner, time.Duration(input.LeaseSeconds)*time.Second)
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

func (handler *runtimeHTTPHandler) resolveVerification(writer http.ResponseWriter, request *http.Request, cohort RuntimeCohort) {
	var input runtimeVerificationResolveRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return
	}
	if !exactRuntimeCohort(cohort, input.Envelope.OrganizationID, input.Envelope.SiteID, input.Envelope.DeviceID) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.ResolveVerification(request.Context(), input.Envelope, input.Result); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) prepareConnectorEvidence(writer http.ResponseWriter, request *http.Request, cohort RuntimeCohort) {
	var evidence commandmodel.PreparedConnectorEvidence
	if !decodeRuntimeJSON(writer, request, &evidence) {
		return
	}
	if !exactRuntimeCohort(cohort, evidence.OrganizationID, evidence.SiteID, evidence.DeviceID) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.PrepareConnectorEvidence(request.Context(), evidence); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) completeConnectorEvidence(writer http.ResponseWriter, request *http.Request, cohort RuntimeCohort) {
	var evidence commandmodel.CompletedConnectorEvidence
	if !decodeRuntimeJSON(writer, request, &evidence) {
		return
	}
	if !exactRuntimeCohort(cohort, evidence.OrganizationID, evidence.SiteID, evidence.DeviceID) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.CompleteConnectorEvidence(request.Context(), evidence); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func exactRuntimeCohort(cohort RuntimeCohort, organizationID, siteID, deviceID string) bool {
	return organizationID == cohort.OrganizationID && siteID == cohort.SiteID && deviceID == cohort.DeviceID
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
