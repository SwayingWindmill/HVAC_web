package telemetry

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

const (
	InternalDeviceSnapshotPrefix          = "/internal/v1/devices/"
	InternalBatchSnapshotPath             = "/internal/v1/telemetry/observation-snapshots:batchGet"
	InternalRecoveryCheckpointResolvePath = "/internal/v1/telemetry/recovery-cursors:resolve"
	InternalCommandReportedStatePath      = "/internal/v1/commands/reported-state"
	telemetryContextGrantHeader           = "X-Telemetry-Context-Grant"
	telemetryCheckpointResolveAction      = "telemetry:checkpoint:resolve"
	maximumSnapshotRequestSize            = 256 << 10
)

var (
	safeRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	traceIDPattern       = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

type ServerConfig struct {
	Store                          SnapshotStore
	Authorizer                     GrantAuthorizer
	AllowedGatewaySPIFFE           string
	RuntimeAudience                string
	ObservationAcceptor            ObservationAcceptor
	CoverageReporter               CoverageReporter
	MQTTEvidenceAcceptor           MQTTEvidenceAcceptor
	SourceAuthenticator            SourceAuthenticator
	Realtime                       *RealtimeService
	LatestCache                    LatestCache
	AllowedCentrifugoSPIFFE        string
	CentrifugoProxySecret          string
	AllowedIAMSPIFFE               string
	AllowedCommandVerifierSPIFFE   string
	AllowedCommandDispatcherSPIFFE string
	CommandVerifierTenantID        string
	CommandVerifierSiteID          string
	CommandVerifierDeviceID        string
	CommandVerifierDeviceIDs       []string
	Metrics                        *observability.Registry
	Now                            func() time.Time
}

type handler struct {
	store                          SnapshotStore
	authorizer                     GrantAuthorizer
	allowedGatewaySPIFFE           string
	runtimeAudience                string
	observationAcceptor            ObservationAcceptor
	coverageReporter               CoverageReporter
	mqttEvidenceAcceptor           MQTTEvidenceAcceptor
	sourceAuthenticator            SourceAuthenticator
	realtime                       *RealtimeService
	latestCache                    LatestCache
	allowedCentrifugoSPIFFE        string
	centrifugoProxySecret          string
	allowedIAMSPIFFE               string
	allowedCommandVerifierSPIFFE   string
	allowedCommandDispatcherSPIFFE string
	commandVerifierTenantID        string
	commandVerifierSiteID          string
	commandVerifierDeviceID        string
	commandVerifierDeviceIDs       map[string]struct{}
	metrics                        *s2Metrics
	now                            func() time.Time
}

func NewHandler(config ServerConfig) http.Handler {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	commandVerifierDeviceIDs := make(map[string]struct{}, len(config.CommandVerifierDeviceIDs)+1)
	if deviceID := strings.TrimSpace(config.CommandVerifierDeviceID); deviceID != "" {
		commandVerifierDeviceIDs[deviceID] = struct{}{}
	}
	for _, rawDeviceID := range config.CommandVerifierDeviceIDs {
		if deviceID := strings.TrimSpace(rawDeviceID); deviceID != "" {
			commandVerifierDeviceIDs[deviceID] = struct{}{}
		}
	}
	return &handler{
		store: config.Store, authorizer: config.Authorizer,
		allowedGatewaySPIFFE: strings.TrimSpace(config.AllowedGatewaySPIFFE),
		runtimeAudience:      strings.TrimSpace(config.RuntimeAudience),
		observationAcceptor:  config.ObservationAcceptor, coverageReporter: config.CoverageReporter,
		mqttEvidenceAcceptor:           config.MQTTEvidenceAcceptor,
		sourceAuthenticator:            config.SourceAuthenticator,
		realtime:                       config.Realtime,
		latestCache:                    config.LatestCache,
		allowedCentrifugoSPIFFE:        strings.TrimSpace(config.AllowedCentrifugoSPIFFE),
		centrifugoProxySecret:          strings.TrimSpace(config.CentrifugoProxySecret),
		allowedIAMSPIFFE:               strings.TrimSpace(config.AllowedIAMSPIFFE),
		allowedCommandVerifierSPIFFE:   strings.TrimSpace(config.AllowedCommandVerifierSPIFFE),
		allowedCommandDispatcherSPIFFE: strings.TrimSpace(config.AllowedCommandDispatcherSPIFFE),
		commandVerifierTenantID:        strings.TrimSpace(config.CommandVerifierTenantID),
		commandVerifierSiteID:          strings.TrimSpace(config.CommandVerifierSiteID),
		commandVerifierDeviceID:        strings.TrimSpace(config.CommandVerifierDeviceID),
		commandVerifierDeviceIDs:       commandVerifierDeviceIDs,
		metrics:                        newS2Metrics(config.Metrics, now),
		now:                            now,
	}
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	startedAt := h.now().UTC()
	captured := h.metrics.capture(writer)
	defer func() {
		status := captured.status
		if status == 0 {
			status = http.StatusOK
		}
		h.metrics.observeRequest(request.URL.Path, status, h.now().UTC().Sub(startedAt))
	}()
	writer = captured
	if request.URL.Path == InternalSourceObservationPath {
		h.handleSourceObservation(writer, request)
		return
	}
	if request.URL.Path == InternalSourceCoveragePath {
		h.handleSourceCoverage(writer, request)
		return
	}
	if request.URL.Path == InternalMQTTGatewayEvidencePath {
		h.handleMQTTGatewayEvidence(writer, request)
		return
	}
	if request.URL.Path == InternalMQTTPresenceEvidencePath {
		h.handleMQTTPresenceEvidence(writer, request)
		return
	}
	if request.URL.Path == InternalMQTTRuntimeEventPath {
		h.handleMQTTRuntimeEvent(writer, request)
		return
	}
	if request.URL.Path == InternalSubscriptionBootstrapPath {
		h.handleSubscriptionBootstrap(writer, request)
		return
	}
	if request.URL.Path == InternalRecoveryCheckpointResolvePath {
		h.handleRecoveryCheckpointResolve(writer, request)
		return
	}
	if request.URL.Path == InternalRecoveryCheckpointPath {
		h.handleRecoveryCheckpoint(writer, request)
		return
	}
	if request.URL.Path == InternalCentrifugoSubscribePath {
		h.handleCentrifugoSubscribe(writer, request)
		return
	}
	if request.URL.Path == InternalSubscriptionRevokePath {
		h.handleSubscriptionRevoke(writer, request)
		return
	}
	if request.URL.Path == InternalCommandReportedStatePath {
		h.handleCommandReportedState(writer, request)
		return
	}
	if request.URL.Path == InternalBatchSnapshotPath {
		h.handleBatch(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, InternalDeviceSnapshotPrefix) && strings.HasSuffix(request.URL.Path, "/observation-snapshot") {
		h.handleSingle(writer, request)
		return
	}
	writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
}

func (h *handler) handleSingle(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports GET.", false)
		return
	}
	peer, grant, ok := h.authenticate(writer, request)
	if !ok {
		return
	}
	deviceID, ok := parseSinglePath(request.URL.Path)
	if !ok {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
		return
	}
	for name := range request.URL.Query() {
		if name != "key" {
			writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry selection is invalid.", false)
			return
		}
	}
	keys := append([]string(nil), request.URL.Query()["key"]...)
	target := telemetryauth.Target{DeviceID: deviceID, Keys: keys}
	if _, err := telemetryauth.CanonicalTargets([]telemetryauth.Target{target}); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry selection is invalid.", false)
		return
	}
	if _, ok := h.authorize(writer, request, peer, grant, telemetryauth.ActionSnapshotRead, []telemetryauth.Target{target}); !ok {
		return
	}
	commit, err := h.store.EvaluateAndRead(request.Context(), target, h.now().UTC())
	if errors.Is(err, ErrDeviceNotFound) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
		return
	}
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_RUNTIME_UNAVAILABLE", "The authoritative telemetry runtime is temporarily unavailable.", true)
		return
	}
	snapshot, err := h.readLatestSnapshot(request.Context(), commit, target.Keys)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_LATEST_UNAVAILABLE", "The rebuildable telemetry Latest cache is temporarily unavailable.", true)
		return
	}
	h.metrics.observeSnapshot(snapshot)
	writeJSON(writer, http.StatusOK, snapshot)
}

func (h *handler) handleBatch(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports POST.", false)
		return
	}
	peer, grant, ok := h.authenticate(writer, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumSnapshotRequestSize)
	var input telemetryapi.BatchGetObservationSnapshotsRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || len(input.Requests) == 0 || len(input.Requests) > telemetryauth.MaximumTargets {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry batch request is invalid.", false)
		return
	}
	targets := make([]telemetryauth.Target, len(input.Requests))
	seenRequestIDs := make(map[string]struct{}, len(input.Requests))
	for index, item := range input.Requests {
		if strings.TrimSpace(item.RequestId) == "" {
			writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry batch request is invalid.", false)
			return
		}
		if _, duplicate := seenRequestIDs[item.RequestId]; duplicate {
			writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry batch request is invalid.", false)
			return
		}
		seenRequestIDs[item.RequestId] = struct{}{}
		keys := make([]string, len(item.Keys))
		for keyIndex, key := range item.Keys {
			keys[keyIndex] = string(key)
		}
		targets[index] = telemetryauth.Target{DeviceID: string(item.DeviceId), Keys: keys}
	}
	if _, err := telemetryauth.CanonicalTargets(targets); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry batch request is invalid.", false)
		return
	}
	if _, ok := h.authorize(writer, request, peer, grant, telemetryauth.ActionBatchRead, targets); !ok {
		return
	}
	evaluatedAt := h.now().UTC()
	response := telemetryapi.BatchGetObservationSnapshotsResponse{SchemaVersion: 1, Items: make([]telemetryapi.BatchObservationResult, 0, len(input.Requests))}
	for index, item := range input.Requests {
		commit, err := h.store.EvaluateAndRead(request.Context(), targets[index], evaluatedAt)
		if errors.Is(err, ErrDeviceNotFound) {
			response.Items = append(response.Items, telemetryapi.BatchObservationResult{Failure: &telemetryapi.BatchObservationFailure{
				RequestId: item.RequestId, DeviceId: item.DeviceId, Status: "ERROR",
				Problem: problemDetails(request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false),
			}})
			continue
		}
		if err != nil {
			writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_RUNTIME_UNAVAILABLE", "The authoritative telemetry runtime is temporarily unavailable.", true)
			return
		}
		snapshot, err := h.readLatestSnapshot(request.Context(), commit, targets[index].Keys)
		if err != nil {
			writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_LATEST_UNAVAILABLE", "The rebuildable telemetry Latest cache is temporarily unavailable.", true)
			return
		}
		h.metrics.observeSnapshot(snapshot)
		response.Items = append(response.Items, telemetryapi.BatchObservationResult{Success: &telemetryapi.BatchObservationSuccess{
			RequestId: item.RequestId, DeviceId: item.DeviceId, Status: "OK", Snapshot: snapshot,
		}})
	}
	writeJSON(writer, http.StatusOK, response)
}

func (h *handler) readLatestSnapshot(ctx context.Context, commit SnapshotCommit, requestedKeys []string) (telemetryapi.DeviceObservationSnapshot, error) {
	if h.latestCache == nil {
		return commit.Snapshot, nil
	}
	if err := validateLatestCacheSnapshot(commit.FullSnapshot); err != nil {
		return telemetryapi.DeviceObservationSnapshot{}, err
	}
	if _, err := h.latestCache.PutIfNewer(ctx, commit.FullSnapshot); err != nil {
		return telemetryapi.DeviceObservationSnapshot{}, err
	}
	cached, err := h.latestCache.Get(ctx, string(commit.FullSnapshot.TenantId), string(commit.FullSnapshot.SiteId), string(commit.FullSnapshot.DeviceId))
	if err != nil {
		return telemetryapi.DeviceObservationSnapshot{}, err
	}
	if cached.BusinessRevision < commit.FullSnapshot.BusinessRevision {
		return telemetryapi.DeviceObservationSnapshot{}, ErrLatestCacheUnavailable
	}
	return ProjectSnapshot(cached, requestedKeys), nil
}

func (h *handler) handleSubscriptionBootstrap(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports POST.", false)
		return
	}
	peer, grant, ok := h.authenticate(writer, request)
	if !ok {
		return
	}
	if h.realtime == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_REALTIME_UNAVAILABLE", "Telemetry realtime is temporarily unavailable.", true)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumSnapshotRequestSize)
	var input telemetryapi.SubscriptionBootstrapRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SUBSCRIPTION_INVALID", "The telemetry subscription request is invalid.", false)
		return
	}
	targets, err := aggregateSubscriptionTargets(input.Subscriptions)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SUBSCRIPTION_INVALID", "The telemetry subscription request is invalid.", false)
		return
	}
	action := telemetryauth.ActionSubscribe
	for _, subscription := range input.Subscriptions {
		if subscription.RecoveryCursor != nil {
			action = telemetryauth.ActionRecoveryUse
			break
		}
	}
	access, ok := h.authorize(writer, request, peer, grant, action, targets)
	if !ok {
		return
	}
	recoveryStartedAt := h.now().UTC()
	response, err := h.realtime.Bootstrap(request.Context(), access, input)
	if action == telemetryauth.ActionRecoveryUse {
		outcome, reason := "success", "none"
		if err != nil {
			outcome, reason = "rejected", "revision"
			if !errors.Is(err, ErrRecoveryCursorRejected) && !errors.Is(err, ErrSubscriptionConflict) {
				outcome, reason = "unavailable", "dependency"
			}
		}
		h.metrics.observeRecovery(outcome, reason, h.now().UTC().Sub(recoveryStartedAt))
	}
	switch {
	case errors.Is(err, ErrSubscriptionConflict):
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SUBSCRIPTION_INVALID", "The telemetry subscription request is invalid.", false)
	case errors.Is(err, ErrRecoveryCursorRejected):
		writeProblem(writer, request, http.StatusBadRequest, "RECOVERY_CURSOR_INVALID", "The supplied recovery cursor is malformed, expired, or outside the current scope.", false)
	case errors.Is(err, ErrSubscriptionNotFound):
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
	case err != nil:
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_REALTIME_UNAVAILABLE", "Telemetry realtime is temporarily unavailable.", true)
	default:
		writeJSON(writer, http.StatusOK, response)
	}
}

type checkpointScopeResponse struct {
	Targets []telemetryauth.Target `json:"targets"`
}

func (h *handler) handleRecoveryCheckpointResolve(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports POST.", false)
		return
	}
	identity, ok := h.checkpointIdentity(writer, request)
	if !ok {
		return
	}
	if h.realtime == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_REALTIME_UNAVAILABLE", "Telemetry realtime is temporarily unavailable.", true)
		return
	}
	input, ok := decodeRecoveryCheckpointRequest(writer, request)
	if !ok {
		return
	}
	targets, err := h.realtime.CheckpointTargetsForIdentity(request.Context(), identity, input)
	if errors.Is(err, ErrRecoveryCursorRejected) || errors.Is(err, ErrSubscriptionNotFound) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
		return
	}
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_CHECKPOINT_INVALID", "The telemetry recovery checkpoint is invalid.", false)
		return
	}
	writeJSON(writer, http.StatusOK, checkpointScopeResponse{Targets: targets})
}

func (h *handler) handleRecoveryCheckpoint(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports POST.", false)
		return
	}
	identity, ok := h.checkpointIdentity(writer, request)
	if !ok {
		return
	}
	peer, grant, ok := h.authenticate(writer, request)
	if !ok {
		return
	}
	if h.realtime == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_REALTIME_UNAVAILABLE", "Telemetry realtime is temporarily unavailable.", true)
		return
	}
	input, ok := decodeRecoveryCheckpointRequest(writer, request)
	if !ok {
		return
	}
	targets, err := h.realtime.CheckpointTargetsForIdentity(request.Context(), identity, input)
	if errors.Is(err, ErrRecoveryCursorRejected) || errors.Is(err, ErrSubscriptionNotFound) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
		return
	}
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_CHECKPOINT_INVALID", "The telemetry recovery checkpoint is invalid.", false)
		return
	}
	access, ok := h.authorize(writer, request, peer, grant, telemetryauth.ActionRecoveryCheckpoint, targets)
	if !ok {
		return
	}
	if access.Subject != identity.Subject || access.SubjectIssuer != identity.SubjectIssuer || access.SessionID != identity.SessionID || access.TenantID != identity.TenantID {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
		return
	}
	response, err := h.realtime.Checkpoint(request.Context(), access, input)
	switch {
	case errors.Is(err, ErrSubscriptionConflict):
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_CHECKPOINT_INVALID", "The telemetry recovery checkpoint is invalid.", false)
	case errors.Is(err, ErrRecoveryCursorRejected), errors.Is(err, ErrSubscriptionNotFound):
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
	case err != nil:
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_REALTIME_UNAVAILABLE", "Telemetry realtime is temporarily unavailable.", true)
	default:
		writeJSON(writer, http.StatusOK, response)
	}
}

func decodeRecoveryCheckpointRequest(writer http.ResponseWriter, request *http.Request) (telemetryapi.RecoveryCursorCheckpointRequest, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumSnapshotRequestSize)
	var input telemetryapi.RecoveryCursorCheckpointRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_CHECKPOINT_INVALID", "The telemetry recovery checkpoint is invalid.", false)
		return telemetryapi.RecoveryCursorCheckpointRequest{}, false
	}
	return input, true
}

func (h *handler) checkpointIdentity(writer http.ResponseWriter, request *http.Request) (CheckpointIdentity, bool) {
	if hasForgedIdentityHeader(request.Header) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.", false)
		return CheckpointIdentity{}, false
	}
	peer, ok := verifiedPeerSPIFFE(request)
	if !ok || h.allowedGatewaySPIFFE == "" || peer != h.allowedGatewaySPIFFE || h.runtimeAudience == "" || request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.", false)
		return CheckpointIdentity{}, false
	}
	values := request.Header.Values(telemetryContextGrantHeader)
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_CONTEXT_GRANT_REQUIRED", "A bounded Telemetry context grant is required.", false)
		return CheckpointIdentity{}, false
	}
	claims, err := identitycontext.VerifyDelegation(request.TLS.PeerCertificates[0].PublicKey, strings.TrimSpace(values[0]))
	if err != nil || !uuidV7Pattern.MatchString(claims.TenantID) {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_CONTEXT_GRANT_INVALID", "The Telemetry context grant is invalid.", false)
		return CheckpointIdentity{}, false
	}
	expectedScope := "session:" + claims.SessionID
	if strings.HasPrefix(claims.Subject, "spiffe://") {
		expectedScope = "workload:" + claims.Subject
	}
	if err := identitycontext.ValidateDelegation(claims, h.now().UTC(), peer, h.runtimeAudience, telemetryCheckpointResolveAction, expectedScope); err != nil {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_CONTEXT_GRANT_INVALID", "The Telemetry context grant is invalid.", false)
		return CheckpointIdentity{}, false
	}
	return CheckpointIdentity{
		Subject: claims.Subject, SubjectIssuer: claims.SubjectIssuer,
		SessionID: claims.SessionID, TenantID: claims.TenantID,
	}, true
}

type centrifugoSubscribeRequest struct {
	Client    string          `json:"client"`
	Transport string          `json:"transport"`
	Protocol  string          `json:"protocol"`
	Encoding  string          `json:"encoding"`
	User      string          `json:"user"`
	Channel   string          `json:"channel"`
	Token     string          `json:"token"`
	Data      json.RawMessage `json:"data"`
	Meta      json.RawMessage `json:"meta"`
}

func (h *handler) handleCentrifugoSubscribe(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeJSON(writer, http.StatusOK, map[string]any{"error": map[string]any{"code": 405, "message": "method not allowed"}})
		return
	}
	peer, verified := verifiedPeerSPIFFE(request)
	providedSecret := request.Header.Get("X-Centrifugo-Proxy-Secret")
	if !verified || h.allowedCentrifugoSPIFFE == "" || peer != h.allowedCentrifugoSPIFFE || h.centrifugoProxySecret == "" ||
		len(providedSecret) != len(h.centrifugoProxySecret) || subtle.ConstantTimeCompare([]byte(providedSecret), []byte(h.centrifugoProxySecret)) != 1 || h.realtime == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"error": map[string]any{"code": 403, "message": "scope denied"}})
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumSnapshotRequestSize)
	var input centrifugoSubscribeRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || strings.TrimSpace(input.Token) != "" {
		writeJSON(writer, http.StatusOK, map[string]any{"error": map[string]any{"code": 403, "message": "scope denied"}})
		return
	}
	subscription, err := h.realtime.AuthorizeSubscribe(request.Context(), input.User, input.Channel)
	if err != nil {
		writeJSON(writer, http.StatusOK, map[string]any{"error": map[string]any{"code": 403, "message": "scope denied"}})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"result": map[string]any{"data": map[string]any{
		"authorizationSource": "telemetry-runtime-owner",
		"subscriptionId":      subscription.SubscriptionID,
		"policyRevision":      subscription.PolicyRevision,
	}}})
}

type subscriptionRevokeRequest struct {
	PrincipalID string `json:"principalId"`
	DeviceID    string `json:"deviceId"`
	Reason      string `json:"reason"`
	OccurredAt  string `json:"occurredAt"`
}

func (h *handler) handleSubscriptionRevoke(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports POST.", false)
		return
	}
	if hasForgedIdentityHeader(request.Header) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.", false)
		return
	}
	peer, verified := verifiedPeerSPIFFE(request)
	if !verified || h.allowedIAMSPIFFE == "" || peer != h.allowedIAMSPIFFE || h.realtime == nil {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumSnapshotRequestSize)
	var input subscriptionRevokeRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil ||
		(input.PrincipalID == "" && input.DeviceID == "") || (input.PrincipalID != "" && !uuidV7Pattern.MatchString(input.PrincipalID)) ||
		(input.DeviceID != "" && !uuidV7Pattern.MatchString(input.DeviceID)) || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 128 {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REVOCATION_INVALID", "The telemetry revocation request is invalid.", false)
		return
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, input.OccurredAt)
	if err != nil || occurredAt.After(h.now().UTC().Add(5*time.Second)) || occurredAt.Before(h.now().UTC().Add(-24*time.Hour)) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REVOCATION_INVALID", "The telemetry revocation request is invalid.", false)
		return
	}
	revoked, err := h.realtime.Revoke(request.Context(), input.PrincipalID, input.DeviceID)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_REVOCATION_UNAVAILABLE", "Telemetry revocation is temporarily unavailable.", true)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"schemaVersion": 1, "revokedSubscriptions": revoked, "occurredAt": occurredAt.UTC().Format(time.RFC3339Nano)})
}

func (h *handler) authenticate(writer http.ResponseWriter, request *http.Request) (string, string, bool) {
	if hasForgedIdentityHeader(request.Header) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.", false)
		return "", "", false
	}
	peer, ok := verifiedPeerSPIFFE(request)
	if !ok || h.allowedGatewaySPIFFE == "" || peer != h.allowedGatewaySPIFFE {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.", false)
		return "", "", false
	}
	grant, ok := bearerGrant(request.Header.Get("Authorization"))
	if !ok {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_GRANT_REQUIRED", "A Telemetry delegation grant is required.", false)
		return "", "", false
	}
	if h.authorizer == nil || (h.store == nil && h.realtime == nil) {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_RUNTIME_UNAVAILABLE", "The authoritative telemetry runtime is temporarily unavailable.", true)
		return "", "", false
	}
	return peer, grant, true
}

func (h *handler) authorize(writer http.ResponseWriter, request *http.Request, peer, grant string, action telemetryauth.Action, targets []telemetryauth.Target) (AccessContext, bool) {
	access, err := h.authorizer.Authorize(request.Context(), peer, grant, action, targets)
	if errors.Is(err, ErrGrantRejected) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
		return AccessContext{}, false
	}
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_AUTHORIZATION_UNAVAILABLE", "Telemetry authorization is temporarily unavailable.", true)
		return AccessContext{}, false
	}
	return access, true
}

func parseSinglePath(path string) (string, bool) {
	trimmed := strings.TrimPrefix(path, InternalDeviceSnapshotPrefix)
	if trimmed == path || !strings.HasSuffix(trimmed, "/observation-snapshot") {
		return "", false
	}
	deviceID := strings.TrimSuffix(trimmed, "/observation-snapshot")
	if deviceID == "" || strings.Contains(deviceID, "/") {
		return "", false
	}
	return deviceID, true
}

func verifiedPeerSPIFFE(request *http.Request) (string, bool) {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
		return "", false
	}
	certificate := request.TLS.PeerCertificates[0]
	if len(certificate.URIs) != 1 || certificate.URIs[0] == nil || certificate.URIs[0].Scheme != "spiffe" {
		return "", false
	}
	return certificate.URIs[0].String(), true
}

func hasForgedIdentityHeader(header http.Header) bool {
	for name, values := range header {
		nonEmpty := false
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				nonEmpty = true
				break
			}
		}
		if !nonEmpty {
			continue
		}
		lowerName := strings.ToLower(name)
		switch lowerName {
		case "x-principal", "x-roles", "x-role", "x-admin", "x-scope", "x-organization-id", "x-site-id", "x-delegation-grant", "x-integration-instance-id", "x-source-scope":
			return true
		}
		if strings.HasPrefix(lowerName, "x-principal-") || strings.HasPrefix(lowerName, "x-organization-") || strings.HasPrefix(lowerName, "x-site-") || strings.HasPrefix(lowerName, "x-integration-") || strings.HasPrefix(lowerName, "x-source-") {
			return true
		}
	}
	return false
}

func bearerGrant(value string) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return strings.TrimSpace(parts[1]), true
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("additional JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeProblem(writer http.ResponseWriter, request *http.Request, status int, code, detail string, retryable bool) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(problemDetails(request, status, code, detail, retryable))
}

func problemDetails(request *http.Request, status int, code, detail string, retryable bool) telemetryapi.ProblemDetails {
	return telemetryapi.ProblemDetails{
		Type:  "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		Title: http.StatusText(status), Status: status, Detail: detail, Instance: request.URL.Path,
		Code: code, TraceId: problemTraceID(request), Retryable: retryable,
	}
}

func problemTraceID(request *http.Request) string {
	parts := strings.Split(strings.TrimSpace(request.Header.Get("Traceparent")), "-")
	if len(parts) == 4 {
		traceID := strings.ToLower(parts[1])
		if len(parts[0]) == 2 && traceIDPattern.MatchString(traceID) && traceID != strings.Repeat("0", 32) {
			return traceID
		}
	}
	requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if traceIDPattern.MatchString(strings.ToLower(requestID)) {
		return strings.ToLower(requestID)
	}
	seed := request.Method + "|" + request.URL.RequestURI() + "|" + request.RemoteAddr
	if safeRequestIDPattern.MatchString(requestID) {
		seed = requestID
	}
	digest := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(digest[:16])
}
