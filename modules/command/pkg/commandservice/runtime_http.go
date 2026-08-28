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
	"github.com/quanlaihe/hvac-web/libs/observability"
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
	ClaimDispatchForCohort(context.Context, string, string, string, commandmodel.Capability, string, time.Duration) (commandmodel.DispatchEnvelope, error)
	ResolveDispatch(context.Context, commandmodel.DispatchEnvelope, commandmodel.ConnectorResult) error
	ClaimVerificationForCohort(context.Context, string, string, string, commandmodel.Capability, string, time.Duration) (commandmodel.VerificationEnvelope, error)
	ResolveVerification(context.Context, commandmodel.VerificationEnvelope, commandmodel.VerificationResult) error
	PrepareConnectorEvidence(context.Context, commandmodel.PreparedConnectorEvidence) error
	CompleteConnectorEvidence(context.Context, commandmodel.CompletedConnectorEvidence) error
}

type RuntimeHTTPConfig struct {
	Store            RuntimeStore
	Metrics          *observability.Registry
	DispatcherSPIFFE string
	VerifierSPIFFE   string
	TenantID         string
	SiteID           string
	DeviceID         string
	Capability       commandmodel.Capability
	Cohorts          []RuntimeCohort
}

type RuntimeCohort struct {
	DispatcherSPIFFE string                  `json:"dispatcherSpiffe"`
	VerifierSPIFFE   string                  `json:"verifierSpiffe"`
	TenantID         string                  `json:"tenantId"`
	SiteID           string                  `json:"siteId"`
	DeviceID         string                  `json:"deviceId"`
	Capability       commandmodel.Capability `json:"capability"`
}

type runtimeHTTPHandler struct {
	store       RuntimeStore
	metrics     *observability.Registry
	dispatchers map[string][]RuntimeCohort
	verifiers   map[string][]RuntimeCohort
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
	dispatchers := make(map[string][]RuntimeCohort, len(cohorts))
	verifiers := make(map[string][]RuntimeCohort, len(cohorts))
	for _, cohort := range cohorts {
		dispatchers[cohort.DispatcherSPIFFE] = append(dispatchers[cohort.DispatcherSPIFFE], cohort)
		verifiers[cohort.VerifierSPIFFE] = append(verifiers[cohort.VerifierSPIFFE], cohort)
	}
	return &runtimeHTTPHandler{store: config.Store, metrics: config.Metrics, dispatchers: dispatchers, verifiers: verifiers}, nil
}

func normalizedRuntimeCohorts(config RuntimeHTTPConfig) ([]RuntimeCohort, error) {
	cohorts := config.Cohorts
	if len(cohorts) == 0 {
		cohorts = []RuntimeCohort{{
			DispatcherSPIFFE: config.DispatcherSPIFFE,
			VerifierSPIFFE:   config.VerifierSPIFFE,
			TenantID:         config.TenantID,
			SiteID:           config.SiteID,
			DeviceID:         config.DeviceID,
			Capability:       config.Capability,
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
		cohort.TenantID = strings.TrimSpace(cohort.TenantID)
		cohort.SiteID = strings.TrimSpace(cohort.SiteID)
		cohort.DeviceID = strings.TrimSpace(cohort.DeviceID)
		profile, capabilitySupported := commandmodel.CapabilityProfileFor(cohort.Capability)
		if !validSPIFFE(cohort.DispatcherSPIFFE) || !validSPIFFE(cohort.VerifierSPIFFE) || cohort.DispatcherSPIFFE == cohort.VerifierSPIFFE ||
			!commandmodel.IsUUIDv7(cohort.TenantID) || !commandmodel.IsUUIDv7(cohort.SiteID) || !commandmodel.IsUUIDv7(cohort.DeviceID) ||
			!capabilitySupported || strings.TrimSpace(profile.Revision) == "" {
			return nil, errors.New("runtime cohort is invalid")
		}
		deviceKey := cohort.TenantID + "\x00" + cohort.SiteID + "\x00" + cohort.DeviceID + "\x00" + string(cohort.Capability)
		if _, duplicate := seenDevices[deviceKey]; duplicate {
			return nil, errors.New("runtime cohort Device capability is duplicated")
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
	var cohorts []RuntimeCohort
	switch request.URL.Path {
	case InternalDispatchClaimPath, InternalDispatchResolvePath, InternalConnectorPreparePath, InternalConnectorCompletePath:
		cohorts = handler.dispatchers[identity]
	case InternalVerificationClaimPath, InternalVerificationResolvePath:
		cohorts = handler.verifiers[identity]
	default:
		writeRuntimeProblem(writer, http.StatusNotFound, "COMMAND_RUNTIME_ROUTE_NOT_FOUND", false)
		return
	}
	if len(cohorts) == 0 {
		writeRuntimeProblem(writer, http.StatusForbidden, "COMMAND_RUNTIME_WORKLOAD_FORBIDDEN", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumRuntimeRequestBody)
	switch request.URL.Path {
	case InternalDispatchClaimPath:
		handler.claimDispatch(writer, request, cohorts)
	case InternalDispatchResolvePath:
		handler.resolveDispatch(writer, request, cohorts)
	case InternalVerificationClaimPath:
		handler.claimVerification(writer, request, cohorts)
	case InternalVerificationResolvePath:
		handler.resolveVerification(writer, request, cohorts)
	case InternalConnectorPreparePath:
		handler.prepareConnectorEvidence(writer, request, cohorts)
	case InternalConnectorCompletePath:
		handler.completeConnectorEvidence(writer, request, cohorts)
	}
}

func (handler *runtimeHTTPHandler) claimDispatch(writer http.ResponseWriter, request *http.Request, cohorts []RuntimeCohort) {
	input, ok := decodeRuntimeClaim(writer, request)
	if !ok {
		return
	}
	for _, cohort := range cohorts {
		envelope, err := handler.store.ClaimDispatchForCohort(request.Context(), cohort.TenantID, cohort.SiteID, cohort.DeviceID, cohort.Capability, input.LeaseOwner, time.Duration(input.LeaseSeconds)*time.Second)
		if errors.Is(err, ErrNoDispatchAvailable) {
			continue
		}
		if err != nil {
			writeRuntimeStoreError(writer, err)
			return
		}
		writeRuntimeJSON(writer, http.StatusOK, envelope)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) resolveDispatch(writer http.ResponseWriter, request *http.Request, cohorts []RuntimeCohort) {
	var input runtimeDispatchResolveRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return
	}
	if !anyRuntimeCommandCohort(cohorts, input.Envelope.TenantID, input.Envelope.SiteID, input.Envelope.DeviceID, input.Envelope.Capability) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.ResolveDispatch(request.Context(), input.Envelope, input.Result); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	if handler.metrics != nil {
		outcome := strings.ToLower(strings.TrimSpace(string(input.Result.Phase)))
		if outcome == "" {
			outcome = "unknown"
		}
		_ = handler.metrics.AddCounter("hvac_command_dispatch_results_total", "Command dispatch results by connector phase.", map[string]string{"outcome": outcome}, 1)
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) claimVerification(writer http.ResponseWriter, request *http.Request, cohorts []RuntimeCohort) {
	input, ok := decodeRuntimeClaim(writer, request)
	if !ok {
		return
	}
	for _, cohort := range cohorts {
		envelope, err := handler.store.ClaimVerificationForCohort(request.Context(), cohort.TenantID, cohort.SiteID, cohort.DeviceID, cohort.Capability, input.LeaseOwner, time.Duration(input.LeaseSeconds)*time.Second)
		if errors.Is(err, ErrVerificationNotAvailable) {
			continue
		}
		if err != nil {
			writeRuntimeStoreError(writer, err)
			return
		}
		writeRuntimeJSON(writer, http.StatusOK, envelope)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) resolveVerification(writer http.ResponseWriter, request *http.Request, cohorts []RuntimeCohort) {
	var input runtimeVerificationResolveRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return
	}
	if !anyRuntimeCommandCohort(cohorts, input.Envelope.TenantID, input.Envelope.SiteID, input.Envelope.DeviceID, input.Envelope.Capability) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.ResolveVerification(request.Context(), input.Envelope, input.Result); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	if handler.metrics != nil {
		outcome := strings.ToLower(strings.TrimSpace(string(input.Result.Outcome)))
		if outcome == "" {
			outcome = "unknown"
		}
		_ = handler.metrics.AddCounter("hvac_command_verifications_total", "Command verification results by final outcome.", map[string]string{"outcome": outcome}, 1)
		if !input.Envelope.AcknowledgedAt.IsZero() {
			duration := time.Since(input.Envelope.AcknowledgedAt).Seconds()
			if duration < 0 {
				duration = 0
			}
			_ = handler.metrics.ObserveHistogram("hvac_command_verification_duration_seconds", "Acknowledgement-to-verification completion duration.", map[string]string{"outcome": outcome}, duration, nil)
		}
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) prepareConnectorEvidence(writer http.ResponseWriter, request *http.Request, cohorts []RuntimeCohort) {
	var evidence commandmodel.PreparedConnectorEvidence
	if !decodeRuntimeJSON(writer, request, &evidence) {
		return
	}
	if !anyRuntimeCohort(cohorts, evidence.TenantID, evidence.SiteID, evidence.DeviceID) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.PrepareConnectorEvidence(request.Context(), evidence); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *runtimeHTTPHandler) completeConnectorEvidence(writer http.ResponseWriter, request *http.Request, cohorts []RuntimeCohort) {
	var evidence commandmodel.CompletedConnectorEvidence
	if !decodeRuntimeJSON(writer, request, &evidence) {
		return
	}
	if !anyRuntimeCohort(cohorts, evidence.TenantID, evidence.SiteID, evidence.DeviceID) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if err := handler.store.CompleteConnectorEvidence(request.Context(), evidence); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func exactRuntimeCohort(cohort RuntimeCohort, tenantID, siteID, deviceID string) bool {
	return tenantID == cohort.TenantID && siteID == cohort.SiteID && deviceID == cohort.DeviceID
}

func exactRuntimeCommandCohort(cohort RuntimeCohort, tenantID, siteID, deviceID string, capability commandmodel.Capability) bool {
	return exactRuntimeCohort(cohort, tenantID, siteID, deviceID) && capability == cohort.Capability
}

func anyRuntimeCohort(cohorts []RuntimeCohort, tenantID, siteID, deviceID string) bool {
	for _, cohort := range cohorts {
		if exactRuntimeCohort(cohort, tenantID, siteID, deviceID) {
			return true
		}
	}
	return false
}

func anyRuntimeCommandCohort(cohorts []RuntimeCohort, tenantID, siteID, deviceID string, capability commandmodel.Capability) bool {
	for _, cohort := range cohorts {
		if exactRuntimeCommandCohort(cohort, tenantID, siteID, deviceID, capability) {
			return true
		}
	}
	return false
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
