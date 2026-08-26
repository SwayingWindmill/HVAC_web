package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/s2telemetryapi"
)

const (
	defaultTelemetryTimeout                = 2 * time.Second
	defaultTelemetryResponseLimit          = int64(2 << 20)
	maximumTelemetryRequestSize            = int64(256 << 10)
	telemetryDecisionBodyLimit             = int64(1 << 20)
	telemetryDecisionPath                  = "/internal/v1/telemetry/decision"
	internalTelemetrySinglePrefix          = "/internal/v1/devices/"
	internalTelemetryBatchPath             = "/internal/v1/telemetry/observation-snapshots:batchGet"
	internalTelemetryBootstrapPath         = "/internal/v1/telemetry/subscriptions:bootstrap"
	internalTelemetryCheckpointResolvePath = "/internal/v1/telemetry/recovery-cursors:resolve"
	internalTelemetryCheckpointPath        = "/internal/v1/telemetry/recovery-cursors:checkpoint"
	telemetryContextGrantHeader            = "X-Telemetry-Context-Grant"
	telemetryCheckpointResolveAction       = "telemetry:checkpoint:resolve"
)

var (
	telemetryClientSubscriptionIDPattern = regexp.MustCompile("^[A-Za-z0-9_.:-]{1,128}$")
	telemetryOpaqueSubscriptionIDPattern = regexp.MustCompile("^[A-Za-z0-9_-]{16,256}$")
	telemetryRecoveryCursorPattern       = regexp.MustCompile("^[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+$")
	telemetryTransportEpochPattern       = regexp.MustCompile("^[A-Za-z0-9_.:-]{1,128}$")
)

type TelemetryConfig struct {
	RuntimeBaseURL    string
	RuntimeHTTPClient *http.Client
	RuntimeAudience   string
	Timeout           time.Duration
	MaxResponseBytes  int64
}

type telemetryController struct {
	runtimeBaseURL    string
	runtimeHTTPClient *http.Client
	runtimeAudience   string
	timeout           time.Duration
	maxResponseBytes  int64
}

type telemetryCaller struct {
	principal      identitycontext.UserPrincipal
	tenantID       string
	contextID      string
	expiresAt      time.Time
	workloadSPIFFE string
}

type telemetryCallerContextKeyType struct{}

var telemetryCallerContextKey telemetryCallerContextKeyType

type publicTelemetryRoute struct {
	template         string
	action           telemetryauth.Action
	batch            bool
	bootstrap        bool
	checkpoint       bool
	history          bool
	historyAggregate bool
}

type telemetryAuthorization struct {
	grant          string
	principalID    string
	policyRevision string
	targets        []telemetryauth.AuthorizedTarget
}

type telemetryFailure struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

func newTelemetryController(config *TelemetryConfig) *telemetryController {
	if config == nil {
		return nil
	}
	resolved := *config
	resolved.RuntimeBaseURL = strings.TrimRight(strings.TrimSpace(resolved.RuntimeBaseURL), "/")
	if resolved.RuntimeHTTPClient == nil {
		resolved.RuntimeHTTPClient = &http.Client{}
	}
	if resolved.RuntimeAudience == "" {
		resolved.RuntimeAudience = "telemetry-runtime-service"
	}
	if resolved.Timeout <= 0 {
		resolved.Timeout = defaultTelemetryTimeout
	}
	if resolved.MaxResponseBytes <= 0 || resolved.MaxResponseBytes > 16<<20 {
		resolved.MaxResponseBytes = defaultTelemetryResponseLimit
	}
	return &telemetryController{
		runtimeBaseURL: resolved.RuntimeBaseURL, runtimeHTTPClient: resolved.RuntimeHTTPClient,
		runtimeAudience: resolved.RuntimeAudience, timeout: resolved.Timeout, maxResponseBytes: resolved.MaxResponseBytes,
	}
}

func (h *handler) GetDeviceObservationSnapshot(writer http.ResponseWriter, request *http.Request, deviceID string, params s2telemetryapi.GetDeviceObservationSnapshotParams) {
	keys := make([]string, len(params.Keys))
	for index, key := range params.Keys {
		keys[index] = string(key)
	}
	target := telemetryauth.Target{DeviceID: deviceID, Keys: keys}
	caller, ok := h.telemetryCaller(writer, request, false)
	if !ok {
		return
	}
	authorization, failure := h.authorizeTelemetry(request.Context(), request, caller, telemetryauth.ActionSnapshotRead, []telemetryauth.Target{target})
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	response, failure := h.executeTelemetryRuntime(request.Context(), request, http.MethodGet, internalTelemetrySinglePrefix+url.PathEscape(deviceID)+"/observation-snapshot", keys, nil, authorization.grant)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	var snapshot s2telemetryapi.DeviceObservationSnapshot
	if len(authorization.targets) != 1 || decodeStrictTelemetryJSON(response, &snapshot) != nil || !validateTelemetrySnapshot(snapshot, authorization.targets[0]) {
		h.writeTelemetryFailure(writer, request, telemetryUnavailable("Telemetry Runtime returned an invalid Snapshot response."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, snapshot)
}

func (h *handler) BatchGetDeviceObservationSnapshots(writer http.ResponseWriter, request *http.Request, input s2telemetryapi.BatchGetObservationSnapshotsRequest) {
	targets := make([]telemetryauth.Target, len(input.Requests))
	for index, item := range input.Requests {
		keys := make([]string, len(item.Keys))
		for keyIndex, key := range item.Keys {
			keys[keyIndex] = string(key)
		}
		targets[index] = telemetryauth.Target{DeviceID: string(item.DeviceId), Keys: keys}
	}
	caller, ok := h.telemetryCaller(writer, request, true)
	if !ok {
		return
	}
	authorization, failure := h.authorizeTelemetry(request.Context(), request, caller, telemetryauth.ActionBatchRead, targets)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	body, err := json.Marshal(input)
	if err != nil {
		h.writeTelemetryFailure(writer, request, telemetryUnavailable("The telemetry batch request could not be encoded."))
		return
	}
	response, failure := h.executeTelemetryRuntime(request.Context(), request, http.MethodPost, internalTelemetryBatchPath, nil, body, authorization.grant)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	var output s2telemetryapi.BatchGetObservationSnapshotsResponse
	if decodeStrictTelemetryJSON(response, &output) != nil || !validateTelemetryBatchResponse(output, input, authorization.targets) {
		h.writeTelemetryFailure(writer, request, telemetryUnavailable("Telemetry Runtime returned an invalid batch response."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, output)
}

func (h *handler) BootstrapTelemetrySubscriptions(writer http.ResponseWriter, request *http.Request, input s2telemetryapi.SubscriptionBootstrapRequest) {
	targets := subscriptionTargets(input)
	caller, ok := h.telemetryCaller(writer, request, true)
	if !ok {
		return
	}
	action := telemetryauth.ActionSubscribe
	for _, subscription := range input.Subscriptions {
		if subscription.RecoveryCursor != nil {
			action = telemetryauth.ActionRecoveryUse
			break
		}
	}
	authorization, failure := h.authorizeTelemetry(request.Context(), request, caller, action, targets)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	body, err := json.Marshal(input)
	if err != nil {
		h.writeTelemetryFailure(writer, request, telemetryUnavailable("The telemetry subscription request could not be encoded."))
		return
	}
	response, failure := h.executeTelemetryRuntimeWithContext(request.Context(), request, http.MethodPost, internalTelemetryBootstrapPath, nil, body, authorization.grant, "")
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	var output s2telemetryapi.SubscriptionBootstrapResponse
	if decodeStrictTelemetryJSON(response, &output) != nil || !validateSubscriptionBootstrapResponse(output, input, h.now().UTC()) {
		h.writeTelemetryFailure(writer, request, telemetryUnavailable("Telemetry Runtime returned an invalid subscription bootstrap response."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, output)
}

func (h *handler) CheckpointTelemetryRecoveryCursors(writer http.ResponseWriter, request *http.Request, input s2telemetryapi.RecoveryCursorCheckpointRequest) {
	caller, ok := h.telemetryCaller(writer, request, true)
	if !ok {
		return
	}
	body, err := json.Marshal(input)
	if err != nil {
		h.writeTelemetryFailure(writer, request, telemetryUnavailable("The telemetry checkpoint request could not be encoded."))
		return
	}
	contextGrant, failure := h.signTelemetryContextGrant(caller)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	targets, failure := h.resolveTelemetryCheckpointTargets(request.Context(), request, body, contextGrant)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	authorization, failure := h.authorizeTelemetry(request.Context(), request, caller, telemetryauth.ActionRecoveryCheckpoint, targets)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	response, failure := h.executeTelemetryRuntimeWithContext(request.Context(), request, http.MethodPost, internalTelemetryCheckpointPath, nil, body, authorization.grant, contextGrant)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	var output s2telemetryapi.RecoveryCursorCheckpointResponse
	if decodeStrictTelemetryJSON(response, &output) != nil || !validateRecoveryCheckpointResponse(output, input, h.now().UTC()) {
		h.writeTelemetryFailure(writer, request, telemetryUnavailable("Telemetry Runtime returned an invalid recovery checkpoint response."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, output)
}

func subscriptionTargets(input s2telemetryapi.SubscriptionBootstrapRequest) []telemetryauth.Target {
	targets := make([]telemetryauth.Target, len(input.Subscriptions))
	for index, subscription := range input.Subscriptions {
		keys := make([]string, len(subscription.Keys))
		for keyIndex, key := range subscription.Keys {
			keys[keyIndex] = string(key)
		}
		targets[index] = telemetryauth.Target{DeviceID: string(subscription.DeviceId), Keys: keys}
	}
	return targets
}

func (h *handler) telemetryCaller(writer http.ResponseWriter, request *http.Request, requireCSRF bool) (telemetryCaller, bool) {
	if caller, ok := request.Context().Value(telemetryCallerContextKey).(telemetryCaller); ok {
		return caller, true
	}
	if session, ok := routeSessionFromContext(request.Context()); ok {
		return h.telemetrySessionCaller(writer, request, session, requireCSRF)
	}
	if _, err := request.Cookie(sessionCookieName); err == nil {
		session, failure := h.identitySession(request)
		if failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return telemetryCaller{}, false
		}
		return h.telemetrySessionCaller(writer, request, session, requireCSRF)
	}
	caller, failure := h.telemetryWorkloadCaller(request)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return telemetryCaller{}, false
	}
	return caller, true
}

func (h *handler) telemetrySessionCaller(writer http.ResponseWriter, request *http.Request, session bffSession, requireCSRF bool) (telemetryCaller, bool) {
	if requireCSRF {
		if h.identity == nil {
			h.writeTelemetryFailure(writer, request, telemetryUnavailable("Telemetry Session validation is unavailable."))
			return telemetryCaller{}, false
		}
		csrf := request.Header.Get("X-CSRF-Token")
		if csrf == "" {
			h.writeTelemetryFailure(writer, request, telemetryFailure{http.StatusForbidden, "CSRF_REQUIRED", "CSRF token required", "A CSRF token is required for this Session request.", false})
			return telemetryCaller{}, false
		}
		if request.Header.Get("Origin") != h.identity.config.PublicOrigin || subtle.ConstantTimeCompare([]byte(csrf), []byte(session.CSRFToken)) != 1 {
			h.writeTelemetryFailure(writer, request, telemetryFailure{http.StatusForbidden, "CSRF_INVALID", "CSRF token invalid", "The request Origin or CSRF token is invalid.", false})
			return telemetryCaller{}, false
		}
	}
	return telemetryCaller{
		principal: session.Principal, tenantID: session.TenantID,
		contextID: session.ID, expiresAt: session.ExpiresAt,
	}, true
}

func (h *handler) telemetryWorkloadCaller(request *http.Request) (telemetryCaller, *telemetryFailure) {
	spiffeID, subjectIssuer, ok := verifiedTelemetryWorkloadIdentity(request)
	if !ok {
		failure := telemetryFailure{http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication required", "A valid BFF Session or verified workload identity is required.", false}
		return telemetryCaller{}, &failure
	}
	tenantID := request.Header.Get("X-Tenant-ID")
	if !isLowerUUIDv7(tenantID) {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "A verified workload caller must provide one valid Tenant.", false}
		return telemetryCaller{}, &failure
	}
	if h.identity == nil {
		failure := telemetryAuthorizationUnavailable("Telemetry workload authorization is not configured.")
		return telemetryCaller{}, &failure
	}
	now := h.identity.now().UTC()
	return telemetryCaller{
		principal:      identitycontext.UserPrincipal{Subject: spiffeID, Issuer: subjectIssuer, DisplayName: spiffeID},
		tenantID:       tenantID,
		contextID:      "workload:" + requestIDFromContext(request.Context()),
		expiresAt:      now.Add(h.identity.config.DelegationTTL),
		workloadSPIFFE: spiffeID,
	}, nil
}

func verifiedTelemetryWorkloadIdentity(request *http.Request) (string, string, bool) {
	if request == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.VerifiedChains[0]) == 0 {
		return "", "", false
	}
	leaf := request.TLS.VerifiedChains[0][0]
	if len(leaf.URIs) != 1 {
		return "", "", false
	}
	identity := leaf.URIs[0]
	if identity == nil || identity.Scheme != "spiffe" || identity.Host == "" || identity.User != nil || identity.RawQuery != "" || identity.Fragment != "" || identity.Path == "" {
		return "", "", false
	}
	return identity.String(), "spiffe://" + identity.Host, true
}

func isVerifiedTelemetryWorkloadRequest(request *http.Request) bool {
	if _, err := request.Cookie(sessionCookieName); err == nil {
		return false
	}
	if _, _, ok := matchPublicTelemetryRoute(request.URL.Path); !ok {
		return false
	}
	_, _, ok := verifiedTelemetryWorkloadIdentity(request)
	return ok
}

func (h *handler) signTelemetryContextGrant(caller telemetryCaller) (string, *telemetryFailure) {
	if h.identity == nil || h.telemetry == nil || h.telemetry.runtimeAudience == "" {
		failure := telemetryAuthorizationUnavailable("Telemetry context signing is not configured.")
		return "", &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if caller.expiresAt.IsZero() || caller.expiresAt.Before(expiresAt) {
		expiresAt = caller.expiresAt
	}
	if !expiresAt.After(now) {
		failure := telemetryFailure{http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication required", "The authenticated caller context has expired.", false}
		return "", &failure
	}
	callerScope := "session:" + caller.contextID
	if caller.workloadSPIFFE != "" {
		callerScope = "workload:" + caller.workloadSPIFFE
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: caller.principal.Subject, SubjectIssuer: caller.principal.Issuer,
		DisplayName: caller.principal.DisplayName, Email: caller.principal.Email, Roles: append([]string(nil), caller.principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.telemetry.runtimeAudience,
		TenantID: caller.tenantID, Actions: []string{telemetryCheckpointResolveAction}, Scopes: []string{callerScope},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: caller.contextID, IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	grant, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := telemetryAuthorizationUnavailable("The Gateway could not sign the Telemetry context grant.")
		return "", &failure
	}
	return grant, nil
}

func (h *handler) authorizeTelemetry(ctx context.Context, publicRequest *http.Request, caller telemetryCaller, action telemetryauth.Action, targets []telemetryauth.Target) (telemetryAuthorization, *telemetryFailure) {
	if h.identity == nil || h.telemetry == nil {
		failure := telemetryAuthorizationUnavailable("Telemetry authorization is not configured.")
		return telemetryAuthorization{}, &failure
	}
	canonicalTargets, err := telemetryauth.CanonicalTargets(targets)
	if err != nil {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry resource selection is invalid.", false}
		return telemetryAuthorization{}, &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if caller.expiresAt.IsZero() || caller.expiresAt.Before(expiresAt) {
		expiresAt = caller.expiresAt
	}
	if !expiresAt.After(now) {
		failure := telemetryFailure{http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication required", "The authenticated caller context has expired.", false}
		return telemetryAuthorization{}, &failure
	}
	parentTokenID := randomURLToken(16)
	callerScope := "session:" + caller.contextID
	if caller.workloadSPIFFE != "" {
		callerScope = "workload:" + caller.workloadSPIFFE
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: caller.principal.Subject, SubjectIssuer: caller.principal.Issuer,
		DisplayName: caller.principal.DisplayName, Email: caller.principal.Email, Roles: append([]string(nil), caller.principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.identity.config.IAMAudience,
		TenantID: caller.tenantID, Actions: []string{"telemetry:authorize"}, Scopes: []string{callerScope},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: caller.contextID, IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: parentTokenID,
	}
	delegation, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := telemetryAuthorizationUnavailable("The Gateway could not sign the Telemetry authorization request.")
		return telemetryAuthorization{}, &failure
	}
	decisionBody, err := json.Marshal(telemetryauth.DecisionRequest{TenantID: caller.tenantID, Action: action, Targets: targets})
	if err != nil {
		failure := telemetryAuthorizationUnavailable("The Gateway could not encode the Telemetry authorization request.")
		return telemetryAuthorization{}, &failure
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.identity.config.IAMURL, "/")+telemetryDecisionPath, bytes.NewReader(decisionBody))
	if err != nil {
		failure := telemetryAuthorizationUnavailable("The Gateway could not construct the Telemetry authorization request.")
		return telemetryAuthorization{}, &failure
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-Delegation-Grant", delegation)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(ctx, request.Header)
	response, err := h.identity.config.IAMHTTPClient.Do(request)
	if err != nil {
		failure := telemetryAuthorizationUnavailable("IAM authorization is temporarily unavailable.")
		return telemetryAuthorization{}, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := telemetryAuthorizationUnavailable("IAM did not return a valid Telemetry authorization decision.")
		return telemetryAuthorization{}, &failure
	}
	raw, err := readBoundedBody(response.Body, telemetryDecisionBodyLimit)
	if err != nil {
		failure := telemetryAuthorizationUnavailable("IAM returned an oversized or unreadable Telemetry decision.")
		return telemetryAuthorization{}, &failure
	}
	var decision telemetryauth.DecisionResponse
	if decodeStrictTelemetryJSON(raw, &decision) != nil {
		failure := telemetryAuthorizationUnavailable("IAM returned an invalid Telemetry authorization decision.")
		return telemetryAuthorization{}, &failure
	}
	if !decision.Decision.Allowed {
		if decision.Decision.ReasonCode == telemetryauth.ReasonTelemetryKeyInvalid {
			failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_KEY_INVALID", "Telemetry key invalid", "One or more requested telemetry keys are invalid or unavailable to this principal.", false}
			return telemetryAuthorization{}, &failure
		}
		failure := telemetryFailure{http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested telemetry resource was not found.", false}
		return telemetryAuthorization{}, &failure
	}
	expectedDigest, _ := telemetryauth.ScopeDigest(action, caller.tenantID, canonicalTargets)
	if !validateTelemetryDecision(decision.Decision, caller, action, canonicalTargets, expectedDigest) ||
		!validateTelemetryGrantStructure(decision.DelegationGrant, h, publicRequest, caller, action, canonicalTargets, expectedDigest, parentTokenID, decision.Decision.PrincipalID, decision.Decision.PolicyRevision) {
		failure := telemetryAuthorizationUnavailable("IAM returned a Telemetry decision outside the authenticated request boundary.")
		return telemetryAuthorization{}, &failure
	}
	return telemetryAuthorization{
		grant: decision.DelegationGrant, principalID: decision.Decision.PrincipalID, policyRevision: decision.Decision.PolicyRevision,
		targets: append([]telemetryauth.AuthorizedTarget(nil), decision.Decision.Targets...),
	}, nil
}

func validateTelemetryDecision(decision telemetryauth.Decision, caller telemetryCaller, action telemetryauth.Action, expected []telemetryauth.Target, expectedDigest string) bool {
	if decision.PrincipalID == "" || decision.Subject != caller.principal.Subject || decision.SubjectIssuer != caller.principal.Issuer ||
		decision.TenantID != caller.tenantID || decision.Action != action || decision.ScopeDigest != expectedDigest ||
		decision.PolicyRevision == "" || decision.ReasonCode != telemetryauth.ReasonAllowExactScope || len(decision.Targets) != len(expected) {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, decision.DecidedAt); err != nil {
		return false
	}
	for index, target := range expected {
		authorized := decision.Targets[index]
		if authorized.DeviceID != target.DeviceID || !isLowerUUIDv7(authorized.TenantID) || !isLowerUUIDv7(authorized.SiteID) || !slices.Equal(authorized.Keys, target.Keys) {
			return false
		}
	}
	return true
}

func validateTelemetryGrantStructure(grant string, h *handler, publicRequest *http.Request, caller telemetryCaller, action telemetryauth.Action, targets []telemetryauth.Target, digest, parentTokenID, principalID, policyRevision string) bool {
	if len(grant) == 0 || len(grant) > telemetryauth.MaximumEncodedGrantSize {
		return false
	}
	parts := strings.Split(grant, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	if signature, err := base64.RawURLEncoding.DecodeString(parts[1]); err != nil || len(signature) == 0 {
		return false
	}
	var claims telemetryauth.GrantClaims
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&claims) != nil || ensureTelemetryJSONEOF(decoder) != nil {
		return false
	}
	keyCount := 0
	for _, target := range targets {
		keyCount += len(target.Keys)
	}
	now := h.now().UTC()
	return claims.Version == telemetryauth.GrantVersion && claims.Issuer != "" &&
		claims.Presenter == h.identity.config.ExecutingWorkloadSPIFFE && claims.Audience == h.telemetry.runtimeAudience &&
		claims.PrincipalID == principalID && claims.Subject == caller.principal.Subject && claims.SubjectIssuer == caller.principal.Issuer &&
		claims.TenantID == caller.tenantID && claims.Action == action && claims.ScopeDigest == digest &&
		claims.TargetCount == len(targets) && claims.KeyCount == keyCount && claims.PolicyRevision == policyRevision &&
		claims.SessionID == caller.contextID && claims.ParentTokenID == parentTokenID && claims.RequestID == requestIDFromContext(publicRequest.Context()) &&
		claims.TraceID == traceIDFromContext(publicRequest.Context()) && claims.Route == telemetryPublicRoute(action) &&
		claims.TokenID != "" && !claims.Transitive && claims.IssuedAt <= now.Add(5*time.Second).Unix() && claims.ExpiresAt > now.Unix() &&
		claims.ExpiresAt > claims.IssuedAt && time.Duration(claims.ExpiresAt-claims.IssuedAt)*time.Second <= telemetryauth.MaximumGrantLifetime &&
		len(claims.ActorChain) == 1 && claims.ActorChain[0].Service == "platform-gateway" && claims.ActorChain[0].SPIFFEID == h.identity.config.ExecutingWorkloadSPIFFE
}

func telemetryPublicRoute(action telemetryauth.Action) string {
	switch action {
	case telemetryauth.ActionSnapshotRead:
		return s2telemetryapi.GetDeviceObservationSnapshotPathTemplate
	case telemetryauth.ActionBatchRead:
		return s2telemetryapi.BatchGetDeviceObservationSnapshotsPath
	case telemetryauth.ActionSubscribe, telemetryauth.ActionRecoveryUse, telemetryauth.ActionResubscribe:
		return s2telemetryapi.BootstrapTelemetrySubscriptionsPath
	case telemetryauth.ActionRecoveryCheckpoint:
		return s2telemetryapi.CheckpointTelemetryRecoveryCursorsPath
	default:
		return ""
	}
}

func (h *handler) executeTelemetryRuntime(ctx context.Context, publicRequest *http.Request, method, path string, keys []string, body []byte, grant string) ([]byte, *telemetryFailure) {
	return h.executeTelemetryRuntimeWithContext(ctx, publicRequest, method, path, keys, body, grant, "")
}

func (h *handler) executeTelemetryRuntimeWithContext(ctx context.Context, publicRequest *http.Request, method, path string, keys []string, body []byte, grant, contextGrant string) ([]byte, *telemetryFailure) {
	startedAt := h.now().UTC()
	outcome := "failed"
	defer func() { h.observeTelemetryUpstream(path, outcome, h.now().UTC().Sub(startedAt)) }()
	if h.telemetry == nil || h.telemetry.runtimeBaseURL == "" || h.telemetry.runtimeHTTPClient == nil {
		outcome = "unavailable"
		failure := telemetryUnavailable("Telemetry Runtime is not configured.")
		return nil, &failure
	}
	requestContext, cancel := context.WithTimeout(ctx, h.telemetry.timeout)
	defer cancel()
	endpoint := h.telemetry.runtimeBaseURL + path
	if len(keys) > 0 {
		query := url.Values{}
		for _, key := range keys {
			query.Add("key", key)
		}
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(requestContext, method, endpoint, bytes.NewReader(body))
	if err != nil {
		outcome = "unavailable"
		failure := telemetryUnavailable("The Gateway could not construct the Telemetry Runtime request.")
		return nil, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if grant != "" {
		request.Header.Set("Authorization", "Bearer "+grant)
	}
	if contextGrant != "" {
		request.Header.Set(telemetryContextGrantHeader, contextGrant)
	}
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(ctx, request.Header)
	response, err := h.telemetry.runtimeHTTPClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			outcome = "timeout"
			failure := telemetryFailure{http.StatusGatewayTimeout, "TELEMETRY_TIMEOUT", "Telemetry request timed out", "Telemetry Runtime did not respond within the bounded request deadline.", true}
			return nil, &failure
		}
		outcome = "unavailable"
		failure := telemetryUnavailable("Telemetry Runtime is temporarily unavailable.")
		return nil, &failure
	}
	defer response.Body.Close()
	raw, err := readBoundedBody(response.Body, h.telemetry.maxResponseBytes)
	if err != nil {
		outcome = "unavailable"
		failure := telemetryUnavailable("Telemetry Runtime returned an oversized or unreadable response.")
		return nil, &failure
	}
	if response.StatusCode == http.StatusOK {
		outcome = "success"
		return raw, nil
	}
	var problem s2telemetryapi.ProblemDetails
	_ = decodeStrictTelemetryJSON(raw, &problem)
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		outcome = "rejected"
	} else if response.StatusCode == http.StatusGatewayTimeout {
		outcome = "timeout"
	} else {
		outcome = "unavailable"
	}
	switch {
	case response.StatusCode == http.StatusNotFound:
		failure := telemetryFailure{http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested telemetry resource was not found.", false}
		return nil, &failure
	case response.StatusCode == http.StatusBadRequest && problem.Code == "TELEMETRY_KEY_INVALID":
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_KEY_INVALID", "Telemetry key invalid", "One or more requested telemetry keys are invalid.", false}
		return nil, &failure
	case response.StatusCode == http.StatusGatewayTimeout:
		failure := telemetryFailure{http.StatusGatewayTimeout, "TELEMETRY_TIMEOUT", "Telemetry request timed out", "Telemetry Runtime did not complete the request within its deadline.", true}
		return nil, &failure
	default:
		failure := telemetryUnavailable("Telemetry Runtime is temporarily unavailable.")
		return nil, &failure
	}
}

type telemetryCheckpointScopeResponse struct {
	Targets []telemetryauth.Target `json:"targets"`
}

func (h *handler) resolveTelemetryCheckpointTargets(ctx context.Context, publicRequest *http.Request, body []byte, contextGrant string) ([]telemetryauth.Target, *telemetryFailure) {
	raw, failure := h.executeTelemetryRuntimeWithContext(ctx, publicRequest, http.MethodPost, internalTelemetryCheckpointResolvePath, nil, body, "", contextGrant)
	if failure != nil {
		return nil, failure
	}
	var response telemetryCheckpointScopeResponse
	if decodeStrictTelemetryJSON(raw, &response) != nil {
		failure := telemetryUnavailable("Telemetry Runtime returned an invalid checkpoint scope response.")
		return nil, &failure
	}
	canonical, err := telemetryauth.CanonicalTargets(response.Targets)
	if err != nil || !slices.EqualFunc(response.Targets, canonical, func(left, right telemetryauth.Target) bool {
		return left.DeviceID == right.DeviceID && slices.Equal(left.Keys, right.Keys)
	}) {
		failure := telemetryUnavailable("Telemetry Runtime returned a checkpoint scope outside the authenticated boundary.")
		return nil, &failure
	}
	return canonical, nil
}

func validateTelemetrySnapshot(snapshot s2telemetryapi.DeviceObservationSnapshot, target telemetryauth.AuthorizedTarget) bool {
	if snapshot.SchemaVersion != 1 || string(snapshot.TenantId) != target.TenantID || string(snapshot.SiteId) != target.SiteID || string(snapshot.DeviceId) != target.DeviceID || snapshot.BusinessRevision < 1 {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, string(snapshot.EvaluatedAt)); err != nil {
		return false
	}
	if !validEvaluationAvailability(snapshot.EvaluationAvailability) || len(snapshot.AvailabilityReasons) > 16 || !uniqueAvailabilityReasons(snapshot.AvailabilityReasons) ||
		!validTelemetryReadiness(snapshot.TelemetryReadiness) || snapshot.DisplayState == nil || !validDisplayState(*snapshot.DisplayState) || !validPresenceSnapshot(snapshot.Presence) || len(snapshot.Values) != len(target.Keys) {
		return false
	}
	for index, state := range snapshot.Values {
		key := ""
		switch {
		case state.Present != nil && state.Missing == nil:
			if !validPresentState(*state.Present) {
				return false
			}
			key = string(state.Present.Key)
		case state.Missing != nil && state.Present == nil:
			if !validMissingState(*state.Missing) {
				return false
			}
			key = string(state.Missing.Key)
		default:
			return false
		}
		if key != target.Keys[index] {
			return false
		}
	}
	return true
}

func validEvaluationAvailability(value s2telemetryapi.EvaluationAvailability) bool {
	return value == s2telemetryapi.EvaluationAvailabilityAvailable || value == s2telemetryapi.EvaluationAvailabilityUnavailable
}

func validTelemetryReadiness(value s2telemetryapi.TelemetryReadiness) bool {
	switch value {
	case s2telemetryapi.TelemetryReadinessCurrent, s2telemetryapi.TelemetryReadinessDegraded, s2telemetryapi.TelemetryReadinessIncomplete, s2telemetryapi.TelemetryReadinessNotApplicable:
		return true
	default:
		return false
	}
}

func validDisplayState(value s2telemetryapi.DeviceDisplayState) bool {
	switch value {
	case s2telemetryapi.DeviceDisplayStateOnline, s2telemetryapi.DeviceDisplayStateOffline, s2telemetryapi.DeviceDisplayStateStale, s2telemetryapi.DeviceDisplayStateUnknown, s2telemetryapi.DeviceDisplayStateUnavailable:
		return true
	default:
		return false
	}
}

func validPresenceSnapshot(value s2telemetryapi.PresenceSnapshot) bool {
	if value.Applicability != s2telemetryapi.PresenceApplicabilityApplicable && value.Applicability != s2telemetryapi.PresenceApplicabilityNotApplicable {
		return false
	}
	if value.CurrentState != nil && *value.CurrentState != s2telemetryapi.DevicePresenceStateOnline && *value.CurrentState != s2telemetryapi.DevicePresenceStateOffline && *value.CurrentState != s2telemetryapi.DevicePresenceStateUnknown {
		return false
	}
	if value.LastSeenAt != nil {
		if _, err := time.Parse(time.RFC3339Nano, string(*value.LastSeenAt)); err != nil {
			return false
		}
	}
	if value.PolicyRevision != nil && *value.PolicyRevision < 1 {
		return false
	}
	if value.LastKnown != nil {
		if value.LastKnown.PolicyRevision < 1 {
			return false
		}
		if _, err := time.Parse(time.RFC3339Nano, string(value.LastKnown.EvaluatedAt)); err != nil {
			return false
		}
		if value.LastKnown.LastSeenAt != nil {
			if _, err := time.Parse(time.RFC3339Nano, string(*value.LastKnown.LastSeenAt)); err != nil {
				return false
			}
		}
	}
	return true
}

func validTelemetryQuality(value s2telemetryapi.TelemetryQuality) bool {
	switch value {
	case s2telemetryapi.TelemetryQualityGood,
		s2telemetryapi.TelemetryQualityPartial,
		s2telemetryapi.TelemetryQualityEstimated,
		s2telemetryapi.TelemetryQualityManual,
		s2telemetryapi.TelemetryQualityStale,
		s2telemetryapi.TelemetryQualityInvalid:
		return true
	default:
		return false
	}
}

func validPresentState(value s2telemetryapi.TelemetryPresentState) bool {
	if value.State != "PRESENT" || (value.Freshness != "FRESH" && value.Freshness != "STALE") ||
		!validTelemetryQuality(value.Quality) || value.PolicyRevision < 1 ||
		len(value.QualityReasons) > 16 || !uniqueQualityReasons(value.QualityReasons) || !json.Valid(value.Value) {
		return false
	}
	switch value.ValueType {
	case "NUMBER", "STRING", "BOOLEAN", "JSON":
	default:
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, string(value.SampledAt)); err != nil {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, string(value.ReceivedAt)); err != nil {
		return false
	}
	return true
}

func validMissingState(value s2telemetryapi.TelemetryMissingState) bool {
	if value.State != "MISSING" || value.Freshness != "MISSING" {
		return false
	}
	switch value.MissingReason {
	case "NEVER_OBSERVED", "ONLY_REJECTED_CANDIDATES", "POLICY_NOT_CONFIGURED":
	default:
		return false
	}
	return value.PolicyRevision == nil || *value.PolicyRevision >= 1
}

func uniqueAvailabilityReasons(values []s2telemetryapi.AvailabilityReasonCode) bool {
	seen := make(map[s2telemetryapi.AvailabilityReasonCode]struct{}, len(values))
	for _, value := range values {
		switch value {
		case s2telemetryapi.AvailabilityReasonCodeSourceUnavailable, s2telemetryapi.AvailabilityReasonCodeObservationCoverageGap, s2telemetryapi.AvailabilityReasonCodePolicyUnavailable, s2telemetryapi.AvailabilityReasonCodeOwnerDependencyUnavailable:
		default:
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func uniqueQualityReasons(values []s2telemetryapi.QualityReasonCode) bool {
	seen := make(map[s2telemetryapi.QualityReasonCode]struct{}, len(values))
	for _, value := range values {
		switch value {
		case s2telemetryapi.QualityReasonCodeSourceUntrusted, s2telemetryapi.QualityReasonCodeTypeMismatch, s2telemetryapi.QualityReasonCodeUnitMismatch, s2telemetryapi.QualityReasonCodeOutOfRange, s2telemetryapi.QualityReasonCodeClockAhead, s2telemetryapi.QualityReasonCodeClockBehind, s2telemetryapi.QualityReasonCodeSourceLagExceeded, s2telemetryapi.QualityReasonCodeDuplicate, s2telemetryapi.QualityReasonCodeOutOfOrder, s2telemetryapi.QualityReasonCodeReplayed:
		default:
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validateTelemetryBatchResponse(response s2telemetryapi.BatchGetObservationSnapshotsResponse, request s2telemetryapi.BatchGetObservationSnapshotsRequest, targets []telemetryauth.AuthorizedTarget) bool {
	if response.SchemaVersion != 1 || len(response.Items) != len(request.Requests) || len(targets) != len(request.Requests) {
		return false
	}
	for index, result := range response.Items {
		expected := request.Requests[index]
		authorized, ok := authorizedTargetForDevice(targets, string(expected.DeviceId))
		if !ok {
			return false
		}
		switch {
		case result.Success != nil && result.Failure == nil:
			if result.Success.Status != "OK" || result.Success.RequestId != expected.RequestId || result.Success.DeviceId != expected.DeviceId || !validateTelemetrySnapshot(result.Success.Snapshot, authorized) {
				return false
			}
		case result.Failure != nil && result.Success == nil:
			if result.Failure.Status != "ERROR" || result.Failure.RequestId != expected.RequestId || result.Failure.DeviceId != expected.DeviceId ||
				result.Failure.Problem.Status != http.StatusNotFound || result.Failure.Problem.Code != "RESOURCE_NOT_FOUND" || result.Failure.Problem.Retryable {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func authorizedTargetForDevice(targets []telemetryauth.AuthorizedTarget, deviceID string) (telemetryauth.AuthorizedTarget, bool) {
	for _, target := range targets {
		if target.DeviceID == deviceID {
			return target, true
		}
	}
	return telemetryauth.AuthorizedTarget{}, false
}

func validateSubscriptionBootstrapResponse(response s2telemetryapi.SubscriptionBootstrapResponse, request s2telemetryapi.SubscriptionBootstrapRequest, now time.Time) bool {
	if response.SchemaVersion != 1 || response.TransportProtocol != "CENTRIFUGO_JSON_V1" ||
		len(response.ConnectionToken) < 16 || len(response.ConnectionToken) > 8192 || strings.ContainsAny(response.ConnectionToken, " \t\r\n") ||
		response.Limits.MaxSubscriptions != telemetryauth.MaximumTargets || response.Limits.MaxKeysPerSubscription != telemetryauth.MaximumKeysPerTarget ||
		response.Limits.MaxTotalKeySelections != telemetryauth.MaximumTotalKeys || len(response.Subscriptions) != len(request.Subscriptions) {
		return false
	}
	endpoint, err := url.Parse(response.Endpoint)
	if err != nil || endpoint.Scheme != "wss" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return false
	}
	expiresAt, err := time.Parse("2006-01-02T15:04:05.000Z", string(response.ExpiresAt))
	if err != nil || !expiresAt.After(now.Add(-5*time.Second)) || expiresAt.After(now.Add(5*time.Minute+5*time.Second)) {
		return false
	}
	seenSubscriptions := make(map[string]struct{}, len(response.Subscriptions))
	seenChannels := make(map[string]struct{}, len(response.Subscriptions))
	for index, descriptor := range response.Subscriptions {
		expected := request.Subscriptions[index]
		subscriptionID := string(descriptor.SubscriptionId)
		channel := string(descriptor.Channel)
		if descriptor.ClientSubscriptionId != expected.ClientSubscriptionId || descriptor.DeviceId != expected.DeviceId || !slices.Equal(descriptor.Keys, expected.Keys) ||
			!telemetryOpaqueSubscriptionIDPattern.MatchString(subscriptionID) || len(channel) < 16 || len(channel) > 512 || strings.Contains(channel, string(expected.DeviceId)) {
			return false
		}
		if _, duplicate := seenSubscriptions[subscriptionID]; duplicate {
			return false
		}
		seenSubscriptions[subscriptionID] = struct{}{}
		if _, duplicate := seenChannels[channel]; duplicate {
			return false
		}
		seenChannels[channel] = struct{}{}
		if expected.RecoveryCursor == nil {
			if descriptor.RecoveryMode != "SNAPSHOT_THEN_LIVE" || descriptor.TransportPosition != nil || descriptor.RecoveryCursor != nil {
				return false
			}
			continue
		}
		if descriptor.RecoveryMode != "ATTEMPT_RECOVERY" || descriptor.TransportPosition == nil || descriptor.RecoveryCursor == nil ||
			*descriptor.RecoveryCursor != *expected.RecoveryCursor || !validTelemetryTransportPosition(*descriptor.TransportPosition) {
			return false
		}
	}
	return true
}

func validateRecoveryCheckpointResponse(response s2telemetryapi.RecoveryCursorCheckpointResponse, request s2telemetryapi.RecoveryCursorCheckpointRequest, now time.Time) bool {
	if response.SchemaVersion != 1 || len(response.Items) != len(request.Checkpoints) {
		return false
	}
	seenCursors := make(map[string]struct{}, len(response.Items))
	for index, item := range response.Items {
		expected := request.Checkpoints[index]
		cursor := string(item.RecoveryCursor)
		expiresAt, err := time.Parse("2006-01-02T15:04:05.000Z", string(item.ExpiresAt))
		if item.SubscriptionId != expected.SubscriptionId || item.BusinessRevision != expected.BusinessRevision ||
			len(cursor) < 16 || len(cursor) > 4096 || !telemetryRecoveryCursorPattern.MatchString(cursor) ||
			err != nil || !expiresAt.After(now.Add(-5*time.Second)) || expiresAt.After(now.Add(2*time.Minute+5*time.Second)) {
			return false
		}
		if _, duplicate := seenCursors[cursor]; duplicate {
			return false
		}
		seenCursors[cursor] = struct{}{}
	}
	return true
}

func validTelemetryTransportPosition(position s2telemetryapi.TransportPosition) bool {
	return telemetryTransportEpochPattern.MatchString(position.Epoch) && position.Offset >= 0
}

func dispatchTelemetryRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicTelemetryRoute, deviceID string) {
	decision := routeDecisionFromContext(request.Context())
	expectedOwner := ownershipregistry.OwnerTelemetryRuntime
	if route.history || route.historyAggregate {
		expectedOwner = ownershipregistry.OwnerAnalyticsQuery
	}
	if decision.RegistryRevision != 0 && decision.SelectedOwner != expectedOwner {
		h.writeTelemetryFailure(writer, request, telemetryUnavailable("The selected telemetry route owner is unavailable."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	if route.batch {
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		caller, ok := h.telemetryCaller(writer, request, true)
		if !ok {
			return
		}
		request = request.WithContext(context.WithValue(request.Context(), telemetryCallerContextKey, caller))
		input, failure := parseTelemetryBatchRequest(writer, request)
		if failure != nil {
			h.writeTelemetryFailure(writer, request, *failure)
			return
		}
		h.BatchGetDeviceObservationSnapshots(writer, request, input)
		return
	}
	if route.bootstrap {
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		caller, ok := h.telemetryCaller(writer, request, true)
		if !ok {
			return
		}
		request = request.WithContext(context.WithValue(request.Context(), telemetryCallerContextKey, caller))
		input, failure := parseTelemetrySubscriptionRequest(writer, request)
		if failure != nil {
			h.writeTelemetryFailure(writer, request, *failure)
			return
		}
		h.BootstrapTelemetrySubscriptions(writer, request, input)
		return
	}
	if route.checkpoint {
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		caller, ok := h.telemetryCaller(writer, request, true)
		if !ok {
			return
		}
		request = request.WithContext(context.WithValue(request.Context(), telemetryCallerContextKey, caller))
		input, failure := parseTelemetryCheckpointRequest(writer, request)
		if failure != nil {
			h.writeTelemetryFailure(writer, request, *failure)
			return
		}
		h.CheckpointTelemetryRecoveryCursors(writer, request, input)
		return
	}
	if route.history || route.historyAggregate {
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		caller, ok := h.telemetryCaller(writer, request, true)
		if !ok {
			return
		}
		request = request.WithContext(context.WithValue(request.Context(), telemetryCallerContextKey, caller))
		if route.historyAggregate {
			input, failure := parseTelemetryHistoryAggregateRequest(writer, request)
			if failure != nil {
				h.writeTelemetryFailure(writer, request, *failure)
				return
			}
			h.QueryDeviceHistoryAggregate(writer, request, input)
			return
		}
		input, failure := parseTelemetryHistoryRequest(writer, request)
		if failure != nil {
			h.writeTelemetryFailure(writer, request, *failure)
			return
		}
		h.QueryDeviceHistory(writer, request, input)
		return
	}
	if request.Method != http.MethodGet {
		writeMethodNotAllowedFor(writer, request, http.MethodGet)
		return
	}
	params, failure := parseTelemetrySingleParams(request)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	h.GetDeviceObservationSnapshot(writer, request, deviceID, params)
}

func matchPublicTelemetryRoute(path string) (publicTelemetryRoute, string, bool) {
	if path == s2telemetryapi.BatchGetDeviceObservationSnapshotsPath {
		return publicTelemetryRoute{template: s2telemetryapi.BatchGetDeviceObservationSnapshotsPath, action: telemetryauth.ActionBatchRead, batch: true}, "", true
	}
	if path == s2telemetryapi.BootstrapTelemetrySubscriptionsPath {
		return publicTelemetryRoute{template: s2telemetryapi.BootstrapTelemetrySubscriptionsPath, action: telemetryauth.ActionSubscribe, bootstrap: true}, "", true
	}
	if path == s2telemetryapi.CheckpointTelemetryRecoveryCursorsPath {
		return publicTelemetryRoute{template: s2telemetryapi.CheckpointTelemetryRecoveryCursorsPath, action: telemetryauth.ActionRecoveryCheckpoint, checkpoint: true}, "", true
	}
	if path == s2telemetryapi.QueryDeviceHistoryPath {
		return publicTelemetryRoute{template: s2telemetryapi.QueryDeviceHistoryPath, action: telemetryauth.ActionHistoryRead, history: true}, "", true
	}
	if path == s2telemetryapi.QueryDeviceHistoryAggregatePath {
		return publicTelemetryRoute{template: s2telemetryapi.QueryDeviceHistoryAggregatePath, action: telemetryauth.ActionHistoryRead, historyAggregate: true}, "", true
	}
	deviceID, matches := matchSinglePathParameter(path, s2telemetryapi.GetDeviceObservationSnapshotPathTemplate, "{deviceId}")
	if !matches {
		return publicTelemetryRoute{}, "", false
	}
	decoded, err := url.PathUnescape(deviceID)
	if err != nil || decoded == "" || decoded != deviceID {
		return publicTelemetryRoute{}, "", false
	}
	return publicTelemetryRoute{template: s2telemetryapi.GetDeviceObservationSnapshotPathTemplate, action: telemetryauth.ActionSnapshotRead}, decoded, true
}

func parseTelemetrySingleParams(request *http.Request) (s2telemetryapi.GetDeviceObservationSnapshotParams, *telemetryFailure) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry query string is malformed.", false}
		return s2telemetryapi.GetDeviceObservationSnapshotParams{}, &failure
	}
	for name := range query {
		if name != "keys" {
			failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry query contains an unsupported parameter.", false}
			return s2telemetryapi.GetDeviceObservationSnapshotParams{}, &failure
		}
	}
	values, exists := query["keys"]
	if !exists || (len(values) == 1 && values[0] == "") {
		return s2telemetryapi.GetDeviceObservationSnapshotParams{Keys: []s2telemetryapi.TelemetryKey{}}, nil
	}
	if len(values) != 1 {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The keys parameter must be supplied at most once.", false}
		return s2telemetryapi.GetDeviceObservationSnapshotParams{}, &failure
	}
	parts := strings.Split(values[0], ",")
	if len(parts) > telemetryauth.MaximumKeysPerTarget {
		failure := telemetryLimitExceeded("The request exceeds the maximum number of telemetry keys per Device.")
		return s2telemetryapi.GetDeviceObservationSnapshotParams{}, &failure
	}
	keys := make([]s2telemetryapi.TelemetryKey, len(parts))
	plain := make([]string, len(parts))
	for index, value := range parts {
		if value == "" {
			failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry key selection is invalid.", false}
			return s2telemetryapi.GetDeviceObservationSnapshotParams{}, &failure
		}
		keys[index], plain[index] = s2telemetryapi.TelemetryKey(value), value
	}
	if _, err := telemetryauth.CanonicalTargets([]telemetryauth.Target{{DeviceID: "018f2e00-3000-7000-8000-000000000001", Keys: plain}}); err != nil {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry key selection is invalid.", false}
		return s2telemetryapi.GetDeviceObservationSnapshotParams{}, &failure
	}
	return s2telemetryapi.GetDeviceObservationSnapshotParams{Keys: keys}, nil
}

func parseTelemetryBatchRequest(writer http.ResponseWriter, request *http.Request) (s2telemetryapi.BatchGetObservationSnapshotsRequest, *telemetryFailure) {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumTelemetryRequestSize)
	var input s2telemetryapi.BatchGetObservationSnapshotsRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			failure := telemetryLimitExceeded("The telemetry batch request body exceeds the maximum size.")
			return input, &failure
		}
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry batch request is malformed.", false}
		return input, &failure
	}
	if ensureTelemetryJSONEOF(decoder) != nil {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry batch request contains additional JSON data.", false}
		return input, &failure
	}
	if len(input.Requests) == 0 {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "At least one telemetry request is required.", false}
		return input, &failure
	}
	if len(input.Requests) > telemetryauth.MaximumTargets {
		failure := telemetryLimitExceeded("The request exceeds the maximum number of Devices per batch.")
		return input, &failure
	}
	seenRequestIDs := make(map[string]struct{}, len(input.Requests))
	targets := make([]telemetryauth.Target, len(input.Requests))
	totalKeys := 0
	for index, item := range input.Requests {
		if len(item.RequestId) == 0 || len(item.RequestId) > 128 {
			failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "Each batch item requires a bounded requestId.", false}
			return input, &failure
		}
		if _, duplicate := seenRequestIDs[item.RequestId]; duplicate {
			failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "Batch requestId values must be unique.", false}
			return input, &failure
		}
		seenRequestIDs[item.RequestId] = struct{}{}
		if len(item.Keys) > telemetryauth.MaximumKeysPerTarget {
			failure := telemetryLimitExceeded("A batch item exceeds the maximum number of telemetry keys per Device.")
			return input, &failure
		}
		totalKeys += len(item.Keys)
		if totalKeys > telemetryauth.MaximumTotalKeys {
			failure := telemetryLimitExceeded("The batch exceeds the maximum total telemetry key selections.")
			return input, &failure
		}
		keys := make([]string, len(item.Keys))
		for keyIndex, key := range item.Keys {
			keys[keyIndex] = string(key)
		}
		targets[index] = telemetryauth.Target{DeviceID: string(item.DeviceId), Keys: keys}
	}
	if _, err := telemetryauth.CanonicalTargets(targets); err != nil {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry batch resource selection is invalid.", false}
		return input, &failure
	}
	return input, nil
}

func parseTelemetrySubscriptionRequest(writer http.ResponseWriter, request *http.Request) (s2telemetryapi.SubscriptionBootstrapRequest, *telemetryFailure) {
	var input s2telemetryapi.SubscriptionBootstrapRequest
	if request.URL.RawQuery != "" {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry subscription route does not accept query parameters.", false}
		return input, &failure
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumTelemetryRequestSize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			failure := telemetryLimitExceeded("The telemetry subscription request body exceeds the maximum size.")
			return input, &failure
		}
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry subscription request is malformed.", false}
		return input, &failure
	}
	if ensureTelemetryJSONEOF(decoder) != nil || len(input.Subscriptions) == 0 {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "At least one valid telemetry subscription is required.", false}
		return input, &failure
	}
	if len(input.Subscriptions) > telemetryauth.MaximumTargets {
		failure := telemetryLimitExceeded("The request exceeds the maximum number of telemetry subscriptions.")
		return input, &failure
	}
	seenClientIDs := make(map[string]struct{}, len(input.Subscriptions))
	totalKeys := 0
	for _, item := range input.Subscriptions {
		clientID := string(item.ClientSubscriptionId)
		if !telemetryClientSubscriptionIDPattern.MatchString(clientID) {
			failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "Each subscription requires a valid clientSubscriptionId.", false}
			return input, &failure
		}
		if _, duplicate := seenClientIDs[clientID]; duplicate {
			failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "clientSubscriptionId values must be unique.", false}
			return input, &failure
		}
		seenClientIDs[clientID] = struct{}{}
		if len(item.Keys) > telemetryauth.MaximumKeysPerTarget {
			failure := telemetryLimitExceeded("A subscription exceeds the maximum number of telemetry keys per Device.")
			return input, &failure
		}
		totalKeys += len(item.Keys)
		if totalKeys > telemetryauth.MaximumTotalKeys {
			failure := telemetryLimitExceeded("The subscription request exceeds the maximum total telemetry key selections.")
			return input, &failure
		}
		if item.RecoveryCursor != nil {
			cursor := string(*item.RecoveryCursor)
			if len(cursor) < 16 || len(cursor) > 4096 || !telemetryRecoveryCursorPattern.MatchString(cursor) {
				failure := telemetryFailure{http.StatusBadRequest, "RECOVERY_CURSOR_INVALID", "Recovery cursor invalid", "The supplied recovery cursor is malformed or expired.", false}
				return input, &failure
			}
		}
	}
	if _, err := telemetryauth.CanonicalTargets(subscriptionTargets(input)); err != nil {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry subscription scope is invalid.", false}
		return input, &failure
	}
	return input, nil
}

func parseTelemetryCheckpointRequest(writer http.ResponseWriter, request *http.Request) (s2telemetryapi.RecoveryCursorCheckpointRequest, *telemetryFailure) {
	var input s2telemetryapi.RecoveryCursorCheckpointRequest
	if request.URL.RawQuery != "" {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry checkpoint route does not accept query parameters.", false}
		return input, &failure
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumTelemetryRequestSize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			failure := telemetryLimitExceeded("The telemetry checkpoint request body exceeds the maximum size.")
			return input, &failure
		}
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry checkpoint request is malformed.", false}
		return input, &failure
	}
	if ensureTelemetryJSONEOF(decoder) != nil || len(input.Checkpoints) == 0 {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "At least one valid telemetry checkpoint is required.", false}
		return input, &failure
	}
	if len(input.Checkpoints) > telemetryauth.MaximumTargets {
		failure := telemetryLimitExceeded("The request exceeds the maximum number of telemetry checkpoints.")
		return input, &failure
	}
	seen := make(map[string]struct{}, len(input.Checkpoints))
	for _, checkpoint := range input.Checkpoints {
		subscriptionID := string(checkpoint.SubscriptionId)
		if !telemetryOpaqueSubscriptionIDPattern.MatchString(subscriptionID) || checkpoint.BusinessRevision < 1 ||
			!telemetryTransportEpochPattern.MatchString(checkpoint.TransportPosition.Epoch) || checkpoint.TransportPosition.Offset < 0 {
			failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "The telemetry checkpoint contains an invalid subscription, revision, or transport position.", false}
			return input, &failure
		}
		if _, duplicate := seen[subscriptionID]; duplicate {
			failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Telemetry request invalid", "Checkpoint subscriptionId values must be unique.", false}
			return input, &failure
		}
		seen[subscriptionID] = struct{}{}
	}
	return input, nil
}

func parseTelemetryHistoryRequest(writer http.ResponseWriter, request *http.Request) (s2telemetryapi.DeviceHistoryRequest, *telemetryFailure) {
	var input s2telemetryapi.DeviceHistoryRequest
	if request.URL.RawQuery != "" {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_HISTORY_REQUEST_INVALID", "Device History request invalid", "The Device History route does not accept query parameters.", false}
		return input, &failure
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumTelemetryRequestSize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			failure := telemetryLimitExceeded("The Device History request body exceeds the maximum size.")
			return input, &failure
		}
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_HISTORY_REQUEST_INVALID", "Device History request invalid", "The Device History request is malformed.", false}
		return input, &failure
	}
	if ensureTelemetryJSONEOF(decoder) != nil {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_HISTORY_REQUEST_INVALID", "Device History request invalid", "The Device History request contains additional JSON data.", false}
		return input, &failure
	}
	return input, nil
}

func parseTelemetryHistoryAggregateRequest(writer http.ResponseWriter, request *http.Request) (s2telemetryapi.DeviceHistoryAggregateRequest, *telemetryFailure) {
	var input s2telemetryapi.DeviceHistoryAggregateRequest
	if request.URL.RawQuery != "" {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_HISTORY_AGGREGATE_REQUEST_INVALID", "Device History aggregate request invalid", "The Device History aggregate route does not accept query parameters.", false}
		return input, &failure
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumTelemetryRequestSize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			failure := telemetryLimitExceeded("The Device History aggregate request body exceeds the maximum size.")
			return input, &failure
		}
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_HISTORY_AGGREGATE_REQUEST_INVALID", "Device History aggregate request invalid", "The Device History aggregate request is malformed.", false}
		return input, &failure
	}
	if ensureTelemetryJSONEOF(decoder) != nil {
		failure := telemetryFailure{http.StatusBadRequest, "TELEMETRY_HISTORY_AGGREGATE_REQUEST_INVALID", "Device History aggregate request invalid", "The Device History aggregate request contains additional JSON data.", false}
		return input, &failure
	}
	return input, nil
}

func decodeStrictTelemetryJSON(raw []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	return ensureTelemetryJSONEOF(decoder)
}

func ensureTelemetryJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("additional JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (h *handler) writeTelemetryFailure(writer http.ResponseWriter, request *http.Request, failure telemetryFailure) {
	writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
}

func telemetryUnavailable(detail string) telemetryFailure {
	return telemetryFailure{http.StatusServiceUnavailable, "TELEMETRY_UNAVAILABLE", "Telemetry unavailable", detail, true}
}

func telemetryAuthorizationUnavailable(detail string) telemetryFailure {
	return telemetryFailure{http.StatusServiceUnavailable, "TELEMETRY_AUTHORIZATION_UNAVAILABLE", "Telemetry authorization unavailable", detail, true}
}

func telemetryLimitExceeded(detail string) telemetryFailure {
	return telemetryFailure{http.StatusRequestEntityTooLarge, "TELEMETRY_BATCH_LIMIT_EXCEEDED", "Telemetry batch limit exceeded", detail, false}
}

var _ s2telemetryapi.ServerInterface = (*handler)(nil)
