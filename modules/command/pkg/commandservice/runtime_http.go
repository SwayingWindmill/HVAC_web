package commandservice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	InternalDispatchClaimPath          = "/internal/v1/dispatches:claim"
	InternalDispatchClaimAnyPath       = "/internal/v1/dispatches:claim-any"
	InternalDispatchResolvePath        = "/internal/v1/dispatches:resolve"
	InternalVerificationClaimPath      = "/internal/v1/verifications:claim"
	InternalVerificationClaimAnyPath   = "/internal/v1/verifications:claim-any"
	InternalVerificationResolvePath    = "/internal/v1/verifications:resolve"
	InternalConnectorPreparePath       = "/internal/v1/connector-evidence:prepare"
	InternalConnectorCompletePath      = "/internal/v1/connector-evidence:complete"
	InternalCommandCancelRetiredPath   = "/internal/v1/commands:cancelRetiredTarget"
	InternalCapabilityProfilesSyncPath = "/internal/v1/capability-profiles:sync"
	InternalDispatchWakePath           = "/internal/v1/command/runtime/dispatches/wake"
	maximumRuntimeRequestBody          = int64(256 << 10)
)

// CapabilityProfileSyncItem represents a template-derived capability profile catalog entry.
type CapabilityProfileSyncItem struct {
	CapabilityName     string  `json:"capabilityName"`
	CapabilityRevision string  `json:"capabilityRevision"`
	Status             string  `json:"status"`
	CanonicalUnit      string  `json:"canonicalUnit"`
	MinimumValue       float64 `json:"minimumValue"`
	MaximumValue       float64 `json:"maximumValue"`
	MaximumDelta       float64 `json:"maximumDelta"`
	RiskLevel          string  `json:"riskLevel"`
	ApprovalPolicy     string  `json:"approvalPolicy"`
	RetryPolicy        string  `json:"retryPolicy"`
	ConnectorKind      string  `json:"connectorKind"`
}

// CapabilityProfileSyncStore persists template-derived capability profiles in command runtime.
type CapabilityProfileSyncStore interface {
	SyncCapabilityProfiles(context.Context, string, time.Time, []CapabilityProfileSyncItem) (int, error)
}

type RuntimeStore interface {
	ClaimDispatch(context.Context, string, string, time.Duration) (commandmodel.DispatchEnvelope, error)
	ResolveDispatch(context.Context, commandmodel.DispatchEnvelope, commandmodel.ConnectorResult) error
	ClaimVerification(context.Context, string, string, time.Duration) (commandmodel.VerificationEnvelope, error)
	ResolveVerification(context.Context, commandmodel.VerificationEnvelope, commandmodel.VerificationResult) error
	PrepareConnectorEvidence(context.Context, commandmodel.PreparedConnectorEvidence) error
	CompleteConnectorEvidence(context.Context, commandmodel.CompletedConnectorEvidence) error
	CancelPending(context.Context, commandmodel.CancelPendingRequest) ([]string, error)
}

// AnyTenantRuntimeStore is an optional RuntimeStore extension supporting cross-tenant
// fair claiming with bounded wakeup wait.
type AnyTenantRuntimeStore interface {
	RuntimeStore
	ClaimAnyDispatch(ctx context.Context, leaseOwner string, leaseFor, waitDuration time.Duration) (commandmodel.DispatchEnvelope, error)
	ClaimAnyVerification(ctx context.Context, leaseOwner string, leaseFor, waitDuration time.Duration) (commandmodel.VerificationEnvelope, error)
}

// DeferredDispatchWaker is an optional RuntimeStore extension that wakes deferred
// QUEUE_IF_OFFLINE dispatches when offline/sleeping devices resume activity.
type DeferredDispatchWaker interface {
	WakeDeferredDispatches(ctx context.Context, tenantID string, deviceIDs []string) (int, error)
}

// RuntimeHTTPConfig names the one dispatcher and the one verifier workload. Each serves
// every Tenant: which Devices may be controlled is decided by the Registry (an active
// writable command Point), checked by Connectivity before anything is sent.
type RuntimeHTTPConfig struct {
	Store                  RuntimeStore
	Metrics                *observability.Registry
	DispatcherSPIFFE       string
	VerifierSPIFFE         string
	CoreRegistrySPIFFEs    []string
	ControlFailureNotifier ControlFailureNotifier
}

type runtimeHTTPHandler struct {
	store               RuntimeStore
	metrics             *observability.Registry
	dispatcher          string
	verifier            string
	coreRegistrySPIFFEs map[string]struct{}
	failureNotifier     ControlFailureNotifier
	failureQueue        controlFailureQueue
}

// Close drains queued control failure notifications before returning.
func (handler *runtimeHTTPHandler) Close() {
	if handler == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlFailureCloseTimeout)
	defer cancel()
	_ = handler.failureQueue.Close(ctx)
}

type runtimeClaimRequest struct {
	TenantID     string `json:"tenantId"`
	LeaseOwner   string `json:"leaseOwner"`
	LeaseSeconds int64  `json:"leaseSeconds"`
}

type runtimeAnyClaimRequest struct {
	LeaseOwner   string `json:"leaseOwner"`
	LeaseSeconds int64  `json:"leaseSeconds"`
}

type runtimeCancelRetiredRequest struct {
	TenantID    string   `json:"tenantId"`
	SiteID      string   `json:"siteId"`
	DeviceIDs   []string `json:"deviceIds"`
	CausationID string   `json:"causationId"`
}

type runtimeDispatchResolveRequest struct {
	Envelope commandmodel.DispatchEnvelope `json:"envelope"`
	Result   commandmodel.ConnectorResult  `json:"result"`
}

type runtimeDispatchWakeRequest struct {
	TenantID  string   `json:"tenantId"`
	DeviceIDs []string `json:"deviceIds"`
}

type runtimeDispatchWakeResponse struct {
	WokenCount int `json:"wokenCount"`
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
	dispatcher, verifier := strings.TrimSpace(config.DispatcherSPIFFE), strings.TrimSpace(config.VerifierSPIFFE)
	if config.Store == nil || !validSPIFFE(dispatcher) || !validSPIFFE(verifier) || dispatcher == verifier {
		return nil, errors.New("command runtime HTTP security configuration is incomplete")
	}
	coreCandidates := config.CoreRegistrySPIFFEs
	if len(coreCandidates) == 0 {
		coreCandidates = []string{"spiffe://hvac.local/platform-core-service", "spiffe://hvac.local/platform-gateway"}
	}
	coreAllowed := make(map[string]struct{}, len(coreCandidates))
	for _, candidate := range coreCandidates {
		spiffe := strings.TrimSpace(candidate)
		if !validSPIFFE(spiffe) {
			return nil, errors.New("command core registry SPIFFE configuration is invalid")
		}
		coreAllowed[spiffe] = struct{}{}
	}
	return &runtimeHTTPHandler{
		store:               config.Store,
		metrics:             config.Metrics,
		dispatcher:          dispatcher,
		verifier:            verifier,
		coreRegistrySPIFFEs: coreAllowed,
		failureNotifier:     config.ControlFailureNotifier,
		failureQueue:        controlFailureQueue{metrics: config.Metrics},
	}, nil
}

func (handler *runtimeHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeRuntimeProblem(writer, http.StatusMethodNotAllowed, "COMMAND_RUNTIME_METHOD_NOT_ALLOWED", false)
		return
	}
	peer := peerSPIFFE(request)
	if request.URL.Path == InternalCommandCancelRetiredPath || request.URL.Path == InternalCapabilityProfilesSyncPath {
		if _, ok := handler.coreRegistrySPIFFEs[peer]; !ok {
			writeRuntimeProblem(writer, http.StatusForbidden, "COMMAND_RUNTIME_WORKLOAD_FORBIDDEN", false)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maximumRuntimeRequestBody)
		if request.URL.Path == InternalCapabilityProfilesSyncPath {
			handler.syncCapabilityProfiles(writer, request)
			return
		}
		handler.cancelRetiredTarget(writer, request)
		return
	}
	var workload string
	switch request.URL.Path {
	case InternalDispatchClaimPath, InternalDispatchClaimAnyPath, InternalDispatchResolvePath, InternalDispatchWakePath, InternalConnectorPreparePath, InternalConnectorCompletePath:
		workload = handler.dispatcher
	case InternalVerificationClaimPath, InternalVerificationClaimAnyPath, InternalVerificationResolvePath:
		workload = handler.verifier
	default:
		writeRuntimeProblem(writer, http.StatusNotFound, "COMMAND_RUNTIME_ROUTE_NOT_FOUND", false)
		return
	}
	if peer != workload {
		writeRuntimeProblem(writer, http.StatusForbidden, "COMMAND_RUNTIME_WORKLOAD_FORBIDDEN", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumRuntimeRequestBody)
	switch request.URL.Path {
	case InternalDispatchClaimPath:
		handler.claimDispatch(writer, request)
	case InternalDispatchClaimAnyPath:
		handler.claimAnyDispatch(writer, request)
	case InternalDispatchResolvePath:
		handler.resolveDispatch(writer, request)
	case InternalDispatchWakePath:
		handler.wakeDeferredDispatches(writer, request)
	case InternalVerificationClaimPath:
		handler.claimVerification(writer, request)
	case InternalVerificationClaimAnyPath:
		handler.claimAnyVerification(writer, request)
	case InternalVerificationResolvePath:
		handler.resolveVerification(writer, request)
	case InternalConnectorPreparePath:
		handler.prepareConnectorEvidence(writer, request)
	case InternalConnectorCompletePath:
		handler.completeConnectorEvidence(writer, request)
	}
}

func (handler *runtimeHTTPHandler) wakeDeferredDispatches(writer http.ResponseWriter, request *http.Request) {
	var input runtimeDispatchWakeRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return
	}
	tenantID := strings.TrimSpace(input.TenantID)
	if !commandmodel.IsUUIDv7(tenantID) || len(input.DeviceIDs) == 0 || len(input.DeviceIDs) > 500 {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	deviceIDs := make([]string, 0, len(input.DeviceIDs))
	seen := make(map[string]struct{}, len(input.DeviceIDs))
	for _, rawID := range input.DeviceIDs {
		trimmed := strings.TrimSpace(rawID)
		if !commandmodel.IsUUIDv7(trimmed) {
			writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
			return
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		deviceIDs = append(deviceIDs, trimmed)
	}
	waker, ok := handler.store.(DeferredDispatchWaker)
	if !ok {
		writeRuntimeJSON(writer, http.StatusOK, runtimeDispatchWakeResponse{WokenCount: 0})
		return
	}
	wokenCount, err := waker.WakeDeferredDispatches(request.Context(), tenantID, deviceIDs)
	if err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writeRuntimeJSON(writer, http.StatusOK, runtimeDispatchWakeResponse{WokenCount: wokenCount})
}

func (handler *runtimeHTTPHandler) cancelRetiredTarget(writer http.ResponseWriter, request *http.Request) {
	var input runtimeCancelRetiredRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return
	}
	tenantID := strings.TrimSpace(input.TenantID)
	siteID := strings.TrimSpace(input.SiteID)
	causationID := strings.TrimSpace(input.CausationID)
	deviceIDs := make([]string, 0, len(input.DeviceIDs))
	for _, id := range input.DeviceIDs {
		trimmed := strings.TrimSpace(id)
		if !commandmodel.IsUUIDv7(trimmed) {
			writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
			return
		}
		deviceIDs = append(deviceIDs, trimmed)
	}
	if !commandmodel.IsUUIDv7(tenantID) || !commandmodel.IsUUIDv7(siteID) || len(deviceIDs) == 0 {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	if causationID == "" {
		causationID = "registry-retirement"
	}
	cancelledIDs, err := handler.store.CancelPending(request.Context(), commandmodel.CancelPendingRequest{
		TenantID:    tenantID,
		SiteID:      siteID,
		DeviceIDs:   deviceIDs,
		PrincipalID: tenantID,
		Reason:      "TARGET_DEVICE_RETIRED",
		CausationID: causationID,
	})
	if err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	if cancelledIDs == nil {
		cancelledIDs = []string{}
	}
	writeRuntimeJSON(writer, http.StatusOK, map[string]any{"cancelledCommandIds": cancelledIDs})
}

type runtimeSyncCapabilityProfilesRequest struct {
	TenantID       string                      `json:"tenantId"`
	SourceSyncedAt *time.Time                  `json:"sourceSyncedAt,omitempty"`
	Profiles       []CapabilityProfileSyncItem `json:"profiles"`
}

func (handler *runtimeHTTPHandler) syncCapabilityProfiles(writer http.ResponseWriter, request *http.Request) {
	var input runtimeSyncCapabilityProfilesRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return
	}
	tenantID := strings.TrimSpace(input.TenantID)
	if !commandmodel.IsUUIDv7(tenantID) || len(input.Profiles) == 0 {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return
	}
	for _, profile := range input.Profiles {
		if !validCapabilityProfileSyncItem(profile) {
			writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
			return
		}
	}
	syncer, ok := handler.store.(CapabilityProfileSyncStore)
	if !ok {
		writeRuntimeProblem(writer, http.StatusNotImplemented, "COMMAND_CAPABILITY_SYNC_UNSUPPORTED", false)
		return
	}
	var sourceSyncedAt time.Time
	if input.SourceSyncedAt != nil && !input.SourceSyncedAt.IsZero() {
		sourceSyncedAt = input.SourceSyncedAt.UTC()
	}
	syncedCount, err := syncer.SyncCapabilityProfiles(request.Context(), tenantID, sourceSyncedAt, input.Profiles)
	if err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writeRuntimeJSON(writer, http.StatusOK, map[string]any{"syncedCount": syncedCount})
}

func validCapabilityProfileSyncItem(profile CapabilityProfileSyncItem) bool {
	if strings.TrimSpace(profile.CapabilityName) == "" || strings.TrimSpace(profile.CapabilityName) != profile.CapabilityName || len(profile.CapabilityName) > 128 {
		return false
	}
	if strings.TrimSpace(profile.CapabilityRevision) == "" || strings.TrimSpace(profile.CapabilityRevision) != profile.CapabilityRevision || len(profile.CapabilityRevision) > 128 {
		return false
	}
	switch profile.Status {
	case "DRAFT", "VERIFIED":
	default:
		return false
	}
	if strings.TrimSpace(profile.CanonicalUnit) == "" || strings.TrimSpace(profile.CanonicalUnit) != profile.CanonicalUnit || len(profile.CanonicalUnit) > 64 {
		return false
	}
	if math.IsNaN(profile.MinimumValue) || math.IsInf(profile.MinimumValue, 0) ||
		math.IsNaN(profile.MaximumValue) || math.IsInf(profile.MaximumValue, 0) ||
		profile.MaximumValue < profile.MinimumValue ||
		math.IsNaN(profile.MaximumDelta) || math.IsInf(profile.MaximumDelta, 0) || profile.MaximumDelta < 0 {
		return false
	}
	switch commandmodel.RiskLevel(profile.RiskLevel) {
	case commandmodel.RiskLow, commandmodel.RiskMedium, commandmodel.RiskHigh, commandmodel.RiskCritical:
	default:
		return false
	}
	switch commandmodel.ApprovalPolicy(profile.ApprovalPolicy) {
	case commandmodel.ApprovalNone, commandmodel.ApprovalSingleApprover, commandmodel.ApprovalTwoPerson:
	default:
		return false
	}
	if profile.RetryPolicy != string(commandmodel.RetryPreSendOnly) {
		return false
	}
	if strings.TrimSpace(profile.ConnectorKind) == "" || strings.TrimSpace(profile.ConnectorKind) != profile.ConnectorKind || len(profile.ConnectorKind) > 64 {
		return false
	}
	return true
}

func (handler *runtimeHTTPHandler) claimDispatch(writer http.ResponseWriter, request *http.Request) {
	input, ok := decodeRuntimeClaim(writer, request)
	if !ok {
		return
	}
	envelope, err := handler.store.ClaimDispatch(request.Context(), input.TenantID, input.LeaseOwner, time.Duration(input.LeaseSeconds)*time.Second)
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

func (handler *runtimeHTTPHandler) claimAnyDispatch(writer http.ResponseWriter, request *http.Request) {
	input, waitDuration, ok := decodeRuntimeAnyClaim(writer, request)
	if !ok {
		return
	}
	anyStore, ok := handler.store.(AnyTenantRuntimeStore)
	if !ok {
		writeRuntimeProblem(writer, http.StatusNotImplemented, "COMMAND_RUNTIME_CLAIM_ANY_UNSUPPORTED", false)
		return
	}
	envelope, err := anyStore.ClaimAnyDispatch(request.Context(), input.LeaseOwner, time.Duration(input.LeaseSeconds)*time.Second, waitDuration)
	if errors.Is(err, errTransientSafetyEvaluation) {
		writeRuntimeProblem(writer, http.StatusServiceUnavailable, "COMMAND_SAFETY_UNAVAILABLE", true)
		return
	}
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

type controlFailureNotifyingStore interface {
	hasControlFailureNotifier() bool
}

func (handler *runtimeHTTPHandler) shouldNotifyFromHTTP() bool {
	if handler == nil || handler.failureNotifier == nil {
		return false
	}
	if store, ok := handler.store.(controlFailureNotifyingStore); ok && store.hasControlFailureNotifier() {
		return false
	}
	return true
}

func (handler *runtimeHTTPHandler) resolveDispatch(writer http.ResponseWriter, request *http.Request) {
	var input runtimeDispatchResolveRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return
	}
	if err := handler.store.ResolveDispatch(request.Context(), input.Envelope, input.Result); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	if handler.shouldNotifyFromHTTP() {
		now := time.Now().UTC()
		if decision, decErr := DecideDispatchResolution(input.Envelope.Capability, input.Result, now); decErr == nil {
			intent := commandmodel.CommandIntent{
				ID:         input.Envelope.CommandID,
				TenantID:   input.Envelope.TenantID,
				SiteID:     input.Envelope.SiteID,
				DeviceID:   input.Envelope.DeviceID,
				PointID:    input.Envelope.PointID,
				Capability: input.Envelope.Capability,
				Parameters: input.Envelope.Parameters,
			}
			if event, notify := controlFailureEventFromDecision(intent, decision.IntentFinal, decision.Reason, input.Result.FailureCode, now); notify {
				handler.failureQueue.enqueue(handler.failureNotifier, event)
			}
		}
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

func (handler *runtimeHTTPHandler) claimVerification(writer http.ResponseWriter, request *http.Request) {
	input, ok := decodeRuntimeClaim(writer, request)
	if !ok {
		return
	}
	envelope, err := handler.store.ClaimVerification(request.Context(), input.TenantID, input.LeaseOwner, time.Duration(input.LeaseSeconds)*time.Second)
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

func (handler *runtimeHTTPHandler) claimAnyVerification(writer http.ResponseWriter, request *http.Request) {
	input, waitDuration, ok := decodeRuntimeAnyClaim(writer, request)
	if !ok {
		return
	}
	anyStore, ok := handler.store.(AnyTenantRuntimeStore)
	if !ok {
		writeRuntimeProblem(writer, http.StatusNotImplemented, "COMMAND_RUNTIME_CLAIM_ANY_UNSUPPORTED", false)
		return
	}
	envelope, err := anyStore.ClaimAnyVerification(request.Context(), input.LeaseOwner, time.Duration(input.LeaseSeconds)*time.Second, waitDuration)
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
	if err := handler.store.ResolveVerification(request.Context(), input.Envelope, input.Result); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	if handler.shouldNotifyFromHTTP() {
		now := time.Now().UTC()
		intent := commandmodel.CommandIntent{
			ID:         input.Envelope.CommandID,
			TenantID:   input.Envelope.TenantID,
			SiteID:     input.Envelope.SiteID,
			DeviceID:   input.Envelope.DeviceID,
			PointID:    input.Envelope.PointID,
			Capability: input.Envelope.Capability,
			Parameters: input.Envelope.Parameters,
		}
		if decision, decErr := DecideVerificationResolutionWithEdge(intent, input.Envelope, input.Envelope.EdgeExecution, input.Result, input.Envelope.VerificationDeadline, now); decErr == nil {
			if event, notify := controlFailureEventFromDecision(intent, decision.IntentFinal, decision.Reason, input.Result.FailureCode, now); notify {
				handler.failureQueue.enqueue(handler.failureNotifier, event)
			}
		}
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

func (handler *runtimeHTTPHandler) prepareConnectorEvidence(writer http.ResponseWriter, request *http.Request) {
	var evidence commandmodel.PreparedConnectorEvidence
	if !decodeRuntimeJSON(writer, request, &evidence) {
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
	if err := handler.store.CompleteConnectorEvidence(request.Context(), evidence); err != nil {
		writeRuntimeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func decodeRuntimeClaim(writer http.ResponseWriter, request *http.Request) (runtimeClaimRequest, bool) {
	var input runtimeClaimRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return runtimeClaimRequest{}, false
	}
	if !commandmodel.IsUUIDv7(input.TenantID) || strings.TrimSpace(input.LeaseOwner) == "" || input.LeaseSeconds <= 0 || input.LeaseSeconds > 120 {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return runtimeClaimRequest{}, false
	}
	return input, true
}

func decodeRuntimeAnyClaim(writer http.ResponseWriter, request *http.Request) (runtimeAnyClaimRequest, time.Duration, bool) {
	var input runtimeAnyClaimRequest
	if !decodeRuntimeJSON(writer, request, &input) {
		return runtimeAnyClaimRequest{}, 0, false
	}
	if strings.TrimSpace(input.LeaseOwner) == "" || input.LeaseSeconds <= 0 || input.LeaseSeconds > 120 {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return runtimeAnyClaimRequest{}, 0, false
	}
	rawWait := strings.TrimSpace(request.URL.Query().Get("waitMs"))
	if rawWait == "" {
		return input, 0, true
	}
	waitMs, err := strconv.Atoi(rawWait)
	if err != nil || waitMs < 0 || waitMs > int(maxCrossTenantClaimWait/time.Millisecond) {
		writeRuntimeProblem(writer, http.StatusBadRequest, "COMMAND_RUNTIME_REQUEST_INVALID", false)
		return runtimeAnyClaimRequest{}, 0, false
	}
	return input, time.Duration(waitMs) * time.Millisecond, true
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
	case errors.Is(err, ErrCapabilityProfileConflict):
		writeRuntimeProblem(writer, http.StatusConflict, "COMMAND_RUNTIME_CAPABILITY_CONFLICT", false)
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
