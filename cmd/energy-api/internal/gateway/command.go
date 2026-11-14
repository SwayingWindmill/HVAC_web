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
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/s2telemetryapi"
	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

const (
	publicCommandsPath          = "/api/v1/commands"
	internalCommandsPath        = "/internal/v1/commands"
	commandDecisionPath         = "/internal/v1/command/decision"
	maximumCommandRequestBody   = int64(16 << 10)
	defaultCommandResponseLimit = int64(256 << 10)
)

type CommandConfig struct {
	BackendBaseURL    string
	BackendHTTPClient *http.Client
	BackendAudience   string
	IAMGrantIssuer    string
	Timeout           time.Duration
	MaxResponseBytes  int64
}

type commandController struct {
	baseURL          string
	httpClient       *http.Client
	backendAudience  string
	iamGrantIssuer   string
	timeout          time.Duration
	maxResponseBytes int64
}

const (
	identityTOTPVerifyPath     = "/internal/v1/mfa/totp/verify"
	identityTOTPVerifyAudience = "identity-service"
	identityTOTPVerifyAction   = "mfa:totp:verify"
)

type createCommandRequest struct {
	AssetID          string                         `json:"assetId"`
	CommandPointID   string                         `json:"commandPointId"`
	Parameters       commandmodel.CommandParameters `json:"parameters"`
	VerificationCode string                         `json:"verificationCode,omitempty"`
}

type preparedCommand struct {
	tenantID             string
	siteID               string
	deviceID             string
	pointID              string
	principalID          string
	idempotencyKey       string
	capability           commandmodel.Capability
	parameters           commandmodel.CommandParameters
	verificationPointKey string
	currentState         commandCurrentState
	grant                string
}

type assetCommandTarget struct {
	asset         platformapi.Asset
	device        platformapi.Device
	point         platformapi.TelemetryPoint
	feedbackPoint platformapi.TelemetryPoint
	capability    commandmodel.Capability
	interlocks    []commandmodel.InterlockCheck
}

type commandCurrentState struct {
	EvaluationAvailability string                   `json:"evaluationAvailability"`
	Presence               string                   `json:"presence"`
	Readiness              string                   `json:"readiness"`
	Quality                string                   `json:"quality"`
	BusinessRevision       uint64                   `json:"businessRevision"`
	CurrentValue           *float64                 `json:"currentValue,omitempty"`
	ReportedValue          commandmodel.ScalarValue `json:"-"`
	ObservedAt             time.Time                `json:"observedAt"`
}

type internalCommandCreate struct {
	TenantID             string                         `json:"tenantId"`
	SiteID               string                         `json:"siteId"`
	DeviceID             string                         `json:"deviceId"`
	PointID              string                         `json:"pointId"`
	PrincipalID          string                         `json:"principalId"`
	IdempotencyKey       string                         `json:"idempotencyKey"`
	Capability           commandmodel.Capability        `json:"capability"`
	Parameters           commandmodel.CommandParameters `json:"parameters"`
	VerificationPointKey string                         `json:"verificationPointKey"`
	CurrentState         commandCurrentState            `json:"currentState"`
}

type internalCommandApproval struct {
	TenantID    string `json:"tenantId"`
	SiteID      string `json:"siteId"`
	DeviceID    string `json:"deviceId"`
	PrincipalID string `json:"principalId"`
}

type internalCommandReconciliation struct {
	TenantID    string                         `json:"tenantId"`
	SiteID      string                         `json:"siteId"`
	DeviceID    string                         `json:"deviceId"`
	PrincipalID string                         `json:"principalId"`
	Outcome     commandmodel.ReconciledOutcome `json:"outcome"`
}

type internalCommandCancel struct {
	TenantID    string `json:"tenantId"`
	SiteID      string `json:"siteId"`
	DeviceID    string `json:"deviceId"`
	PrincipalID string `json:"principalId"`
	Reason      string `json:"reason,omitempty"`
}

type commandTransitionView struct {
	FromStatus *commandmodel.IntentStatus `json:"fromStatus,omitempty"`
	ToStatus   commandmodel.IntentStatus  `json:"toStatus"`
	Reason     string                     `json:"reason"`
	ActorType  string                     `json:"actorType"`
	OccurredAt time.Time                  `json:"occurredAt"`
	Version    uint64                     `json:"version"`
}

type commandView struct {
	SchemaVersion         int                            `json:"schemaVersion"`
	CommandID             string                         `json:"commandId"`
	TenantID              string                         `json:"tenantId"`
	SiteID                string                         `json:"siteId"`
	DeviceID              string                         `json:"deviceId"`
	PointID               string                         `json:"pointId"`
	Capability            commandmodel.Capability        `json:"capability"`
	CapabilityRevision    string                         `json:"capabilityRevision"`
	Status                commandmodel.IntentStatus      `json:"status"`
	Risk                  commandmodel.RiskLevel         `json:"risk"`
	ApprovalPolicy        commandmodel.ApprovalPolicy    `json:"approvalPolicy"`
	ApprovalCount         int                            `json:"approvalCount"`
	RequiredApprovalCount int                            `json:"requiredApprovalCount"`
	Parameters            commandmodel.CommandParameters `json:"parameters"`
	DeviceCommandSequence uint64                         `json:"deviceCommandSequence"`
	Version               uint64                         `json:"version"`
	SnapshotRevision      uint64                         `json:"snapshotRevision"`
	Transitions           []commandTransitionView        `json:"transitions"`
	CreatedAt             time.Time                      `json:"createdAt"`
	UpdatedAt             time.Time                      `json:"updatedAt"`
}

func commandCapabilityProfile(capability commandmodel.Capability) (commandmodel.CapabilityProfile, bool) {
	return commandmodel.CapabilityProfileFor(capability)
}

type commandRouteKind int

const (
	commandRouteCollection commandRouteKind = iota + 1
	commandRouteItem
	commandRouteApproval
	commandRouteReconciliation
	commandRouteCancel
)

func newCommandController(config *CommandConfig) *commandController {
	if config == nil {
		return nil
	}
	resolved := *config
	resolved.BackendBaseURL = strings.TrimRight(strings.TrimSpace(resolved.BackendBaseURL), "/")
	if resolved.BackendHTTPClient == nil {
		resolved.BackendHTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if resolved.BackendAudience == "" {
		resolved.BackendAudience = "command-service"
	}
	if resolved.IAMGrantIssuer == "" {
		resolved.IAMGrantIssuer = "spiffe://hvac.local/iam-service"
	}
	if resolved.Timeout <= 0 || resolved.Timeout > 30*time.Second {
		resolved.Timeout = 10 * time.Second
	}
	if resolved.MaxResponseBytes <= 0 || resolved.MaxResponseBytes > 4<<20 {
		resolved.MaxResponseBytes = defaultCommandResponseLimit
	}
	return &commandController{
		baseURL: resolved.BackendBaseURL, httpClient: resolved.BackendHTTPClient,
		backendAudience: resolved.BackendAudience, iamGrantIssuer: resolved.IAMGrantIssuer,
		timeout:          resolved.Timeout,
		maxResponseBytes: resolved.MaxResponseBytes,
	}
}

func matchPublicCommandRoute(path string) (commandRouteKind, string, bool) {
	if path == publicCommandsPath {
		return commandRouteCollection, "", true
	}
	prefix := publicCommandsPath + "/"
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}
	raw := strings.TrimPrefix(path, prefix)
	if raw == "" {
		return 0, "", false
	}
	kind := commandRouteItem
	if strings.HasSuffix(raw, "/approve") {
		kind = commandRouteApproval
		raw = strings.TrimSuffix(raw, "/approve")
	} else if strings.HasSuffix(raw, "/reconcile") {
		kind = commandRouteReconciliation
		raw = strings.TrimSuffix(raw, "/reconcile")
	} else if strings.HasSuffix(raw, "/cancel") {
		kind = commandRouteCancel
		raw = strings.TrimSuffix(raw, "/cancel")
	}
	if raw == "" || strings.Contains(raw, "/") {
		return 0, "", false
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil || decoded == "" || strings.Contains(decoded, "/") || strings.Contains(decoded, ":") {
		return 0, "", false
	}
	return kind, decoded, true
}

func dispatchCommandRoute(h *handler, writer http.ResponseWriter, request *http.Request, kind commandRouteKind, commandID string) {
	if h.command == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "Command unavailable", "The Command API is not configured.", true, nil)
		return
	}
	switch kind {
	case commandRouteCollection:
		switch request.Method {
		case http.MethodPost:
			h.createCommand(writer, request)
		case http.MethodGet:
			h.listCommands(writer, request)
		default:
			writeMethodNotAllowedFor(writer, request, "GET, POST")
		}
	case commandRouteItem:
		if request.Method != http.MethodGet {
			writeMethodNotAllowedFor(writer, request, http.MethodGet)
			return
		}
		h.getCommand(writer, request, commandID)
	case commandRouteApproval:
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		h.approveCommand(writer, request, commandID)
	case commandRouteReconciliation:
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		h.reconcileCommand(writer, request, commandID)
	case commandRouteCancel:
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		h.cancelCommand(writer, request, commandID)
	default:
		writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested public API route does not exist.", false, nil)
	}
}

func (h *handler) createCommand(writer http.ResponseWriter, request *http.Request) {
	session, ok := h.commandSession(writer, request, true)
	if !ok {
		return
	}
	if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "COMMAND_REQUEST_INVALID", "Command request invalid", "The Command request must use application/json.", false, nil)
		return
	}
	idempotencyKey := request.Header.Get("Idempotency-Key")
	if len(idempotencyKey) < 8 || !requestIDPattern.MatchString(idempotencyKey) {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command request invalid", "A valid Idempotency-Key is required.", false, nil)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumCommandRequestBody)
	var input createCommandRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil ||
		!isLowerUUIDv7(input.AssetID) || !isLowerUUIDv7(input.CommandPointID) {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command request invalid", "The Command request is invalid.", false, nil)
		return
	}
	target, failure := h.resolveAssetCommandTarget(request, session, input.AssetID, input.CommandPointID)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if !validCommandParameters(target.capability, target.point.SourceMetadata, input.Parameters) {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command request invalid", "The Asset capability parameters are invalid.", false, nil)
		return
	}
	if lockFailure := h.enforceControlLockForCommand(request, session, target.device.SiteID, target.asset.ID, target.device.ID); lockFailure != nil {
		h.writeCommandFailure(writer, request, *lockFailure)
		return
	}
	currentState, failure := h.readCommandCurrentState(request, session, target.device, target.feedbackPoint.PointCode)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if interlockFailure := h.enforceCommandInterlocks(request, session, target); interlockFailure != nil {
		h.writeCommandFailure(writer, request, *interlockFailure)
		return
	}
	if risk, _, ok := commandmodel.EvaluateRiskLevel(target.capability, input.Parameters, currentState.CurrentValue); ok && (risk == commandmodel.RiskHigh || risk == commandmodel.RiskCritical) {
		if stepUpFailure := h.verifySubmitStepUpTOTP(request, session, input.VerificationCode); stepUpFailure != nil {
			h.writeCommandFailure(writer, request, *stepUpFailure)
			return
		}
	}
	principalID, grant, failure := h.authorizeCommand(request, session, target.device, target.capability, commandmodel.AuthorizationCommandSubmit)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	prepared := preparedCommand{
		tenantID: target.device.TenantID, siteID: target.device.SiteID, deviceID: target.device.ID, pointID: target.point.ID,
		principalID: principalID, idempotencyKey: idempotencyKey, capability: target.capability,
		parameters: cloneCommandParameters(input.Parameters), verificationPointKey: target.feedbackPoint.PointCode,
		currentState: currentState, grant: grant,
	}
	view, status, location, failure := h.executeCommandCreate(request.Context(), prepared)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	if location != "" {
		writer.Header().Set("Location", location)
	}
	writeJSON(writer, status, view)
}

func (h *handler) getCommand(writer http.ResponseWriter, request *http.Request, commandID string) {
	session, ok := h.commandSession(writer, request, false)
	if !ok {
		return
	}
	if !isLowerUUID(commandID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Command was not found.", false, nil)
		return
	}
	view, failure := h.executeCommandRead(request, session, commandID)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, view)
}

const (
	defaultListedCommands = 50
	maximumListedCommands = 100
)

// listCommands returns a visible Site's newest Commands, read under a context scoped to that Site.
func (h *handler) listCommands(writer http.ResponseWriter, request *http.Request) {
	session, ok := h.commandSession(writer, request, false)
	if !ok {
		return
	}
	query := request.URL.Query()
	siteID := query.Get("siteId")
	limit := defaultListedCommands
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maximumListedCommands {
			writeProblem(writer, request, http.StatusBadRequest, "COMMAND_LIST_REQUEST_INVALID", "Command list request invalid", "limit must be between 1 and 100.", false, nil)
			return
		}
		limit = parsed
	}
	if !isLowerUUIDv7(siteID) {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_LIST_REQUEST_INVALID", "Command list request invalid", "siteId must be a Site UUID.", false, nil)
		return
	}
	visibility := h.siteVisibility(request, session, siteID)
	if visibility.unavailable != "" {
		h.writeCommandFailure(writer, request, commandUnavailable(visibility.unavailable))
		return
	}
	if visibility.hidden {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	views, failure := h.executeCommandList(request, session, siteID, limit)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, struct {
		Data []commandView `json:"data"`
	}{Data: views})
}

func (h *handler) executeCommandList(publicRequest *http.Request, session bffSession, siteID string, limit int) ([]commandView, *commandFailure) {
	if h.identity == nil || h.command == nil || h.command.baseURL == "" || h.command.httpClient == nil {
		failure := commandUnavailable("Command Service is not configured.")
		return nil, &failure
	}
	readContext, failure := h.signCommandReadContext(session, "site:"+siteID)
	if failure != nil {
		return nil, failure
	}
	ctx, cancel := context.WithTimeout(publicRequest.Context(), h.command.timeout)
	defer cancel()
	target := h.command.baseURL + internalCommandsPath + "?" + url.Values{"siteId": {siteID}, "limit": {strconv.Itoa(limit)}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		failure := commandUnavailable("The Command list request could not be constructed.")
		return nil, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-Tenant-ID", session.TenantID)
	request.Header.Set("X-Command-Read-Context", readContext)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), request.Header)
	response, err := h.command.httpClient.Do(request)
	if err != nil {
		failure := commandUnavailable("Command Service is temporarily unavailable.")
		return nil, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := commandBackendFailure(response.StatusCode)
		return nil, &failure
	}
	body, err := readBoundedBody(response.Body, h.command.maxResponseBytes*int64(limit))
	var page struct {
		Data []commandView `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err != nil || decoder.Decode(&page) != nil || ensureCommandJSONEOF(decoder) != nil || page.Data == nil || len(page.Data) > limit {
		failure := commandUnavailable("Command Service returned an invalid Command list.")
		return nil, &failure
	}
	for _, view := range page.Data {
		if !validCommandView(view) || view.TenantID != session.TenantID || view.SiteID != siteID {
			failure := commandUnavailable("Command Service returned an invalid Command projection.")
			return nil, &failure
		}
	}
	return page.Data, nil
}

func (h *handler) approveCommand(writer http.ResponseWriter, request *http.Request, commandID string) {
	session, ok := h.commandSession(writer, request, true)
	if !ok {
		return
	}
	if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
		return
	}
	if !isLowerUUID(commandID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Command was not found.", false, nil)
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "COMMAND_APPROVAL_REQUEST_INVALID", "Command approval invalid", "The Command approval request must use application/json.", false, nil)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 1024)
	var input struct{}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_APPROVAL_REQUEST_INVALID", "Command approval invalid", "The public approval request must be an empty JSON object.", false, nil)
		return
	}
	current, failure := h.executeCommandRead(request, session, commandID)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if current.Status != commandmodel.IntentAwaitingApproval || current.ApprovalCount >= current.RequiredApprovalCount {
		writeProblem(writer, request, http.StatusConflict, "COMMAND_APPROVAL_INVALID", "Command approval invalid", "The Command is not awaiting another approval.", false, nil)
		return
	}
	if commandApprovalRequiresStepUp(current, session, h.now()) {
		writeProblem(writer, request, http.StatusPreconditionRequired, "STEP_UP_REQUIRED", "Security confirmation required", "This high-risk Command approval requires recent multi-factor authentication. Start a high-assurance login and retry the approval.", false, nil)
		return
	}
	device, principalID, grant, failure := h.resolveAndAuthorizeCommandAction(request, session, current, commandmodel.AuthorizationCommandApprove, "approval")
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	approved, failure := h.executeCommandApproval(request.Context(), commandID, internalCommandApproval{
		TenantID: session.TenantID, SiteID: device.SiteID, DeviceID: device.ID,
		PrincipalID: principalID,
	}, grant)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, approved)
}

func (h *handler) cancelCommand(writer http.ResponseWriter, request *http.Request, commandID string) {
	session, ok := h.commandSession(writer, request, true)
	if !ok {
		return
	}
	if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
		return
	}
	if !isLowerUUID(commandID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Command was not found.", false, nil)
		return
	}
	rawBody, err := io.ReadAll(io.LimitReader(request.Body, 1025))
	if err != nil || len(rawBody) > 1024 {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_CANCEL_REQUEST_INVALID", "Command cancel invalid", "The Command cancel request body is invalid.", false, nil)
		return
	}
	var input struct {
		Reason string `json:"reason,omitempty"`
	}
	if len(bytes.TrimSpace(rawBody)) > 0 {
		if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
			writeProblem(writer, request, http.StatusUnsupportedMediaType, "COMMAND_CANCEL_REQUEST_INVALID", "Command cancel invalid", "The Command cancel request must use application/json.", false, nil)
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(rawBody))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil || len(strings.TrimSpace(input.Reason)) > 256 {
			writeProblem(writer, request, http.StatusBadRequest, "COMMAND_CANCEL_REQUEST_INVALID", "Command cancel invalid", "The Command cancel request must be an empty JSON object or contain only a reason string.", false, nil)
			return
		}
	}
	current, failure := h.executeCommandRead(request, session, commandID)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if current.Status != commandmodel.IntentAwaitingApproval && current.Status != commandmodel.IntentQueued {
		writeProblem(writer, request, http.StatusConflict, "COMMAND_CANCEL_INVALID", "Command cannot be cancelled", "Only a pending or queued Command can be cancelled before dispatch begins.", false, nil)
		return
	}
	device, principalID, grant, failure := h.resolveAndAuthorizeCommandAction(request, session, current, commandmodel.AuthorizationCommandSubmit, "cancel")
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	cancelled, failure := h.executeCommandCancel(request.Context(), commandID, internalCommandCancel{
		TenantID: session.TenantID, SiteID: device.SiteID, DeviceID: device.ID, PrincipalID: principalID, Reason: strings.TrimSpace(input.Reason),
	}, grant)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, cancelled)
}

func (h *handler) resolveAndAuthorizeCommandAction(request *http.Request, session bffSession, current commandView, purpose commandmodel.AuthorizationPurpose, label string) (platformapi.Device, string, string, *commandFailure) {
	device, failure := h.resolveCommandDevice(request, session, current.DeviceID)
	if failure != nil {
		return platformapi.Device{}, "", "", failure
	}
	if device.ID != current.DeviceID {
		unavailable := commandUnavailable("Registry returned a Device outside the Command " + label + " boundary.")
		return platformapi.Device{}, "", "", &unavailable
	}
	principalID, grant, failure := h.authorizeCommand(request, session, device, current.Capability, purpose)
	if failure != nil {
		return platformapi.Device{}, "", "", failure
	}
	return device, principalID, grant, nil
}

func commandApprovalRequiresStepUp(current commandView, session bffSession, now time.Time) bool {
	if current.Risk != commandmodel.RiskHigh && current.Risk != commandmodel.RiskCritical {
		return false
	}
	return !freshMFAAssurance(session, now)
}

func freshMFAAssurance(session bffSession, now time.Time) bool {
	if session.AuthenticationACR != authenticationACRMFA || !containsString(session.AuthenticationAMR, "pwd") || !containsString(session.AuthenticationAMR, "otp") || session.AuthenticationTime.IsZero() {
		return false
	}
	age := now.UTC().Sub(session.AuthenticationTime.UTC())
	return age >= 0 && age <= 10*time.Minute
}

func (h *handler) verifySubmitStepUpTOTP(request *http.Request, session bffSession, rawCode string) *commandFailure {
	code := strings.TrimSpace(rawCode)
	if !isSixDigitTOTPCode(code) {
		failure := commandStepUpRequired("High-risk commands require a 6-digit verificationCode from your authenticator app.")
		return &failure
	}
	if h.identity == nil || h.identity.config.DelegationSigner == nil || h.identity.config.OIDCHTTPClient == nil {
		failure := commandUnavailable("Identity verification is unavailable for high-risk commands.")
		return &failure
	}
	grant, err := h.signIdentitySessionGrant(session, identityTOTPVerifyAudience, identityTOTPVerifyAction)
	if err != nil {
		failure := commandUnavailable("Could not sign the identity verification request.")
		return &failure
	}
	status, reply, err := h.identity.backchannel(request, http.MethodPost, identityTOTPVerifyPath, map[string]string{"code": code}, map[string]string{"X-Delegation-Grant": grant})
	if err != nil {
		failure := commandUnavailable("The identity service could not verify the step-up code.")
		return &failure
	}
	if status == http.StatusOK {
		var verified struct {
			Verified bool `json:"verified"`
		}
		if json.Unmarshal(reply, &verified) != nil || !verified.Verified {
			failure := commandUnavailable("The identity service returned an invalid step-up verification response.")
			return &failure
		}
		return nil
	}
	return mapTOTPVerifyError(status, reply)
}

func isSixDigitTOTPCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

func commandStepUpRequired(detail string) commandFailure {
	return commandFailure{
		status:    http.StatusPreconditionRequired,
		code:      "STEP_UP_REQUIRED",
		title:     "Security confirmation required",
		detail:    detail,
		retryable: false,
	}
}

func mapTOTPVerifyError(status int, reply []byte) *commandFailure {
	var refusal struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(reply, &refusal)
	var failure commandFailure
	switch {
	case status == http.StatusBadRequest || (status == http.StatusUnauthorized && refusal.Error == "mfa_code_invalid"):
		failure = commandStepUpRequired("The 6-digit verification code is invalid. Enter the current authenticator code and try again.")
	case status == http.StatusConflict || refusal.Error == "mfa_code_replayed":
		failure = commandStepUpRequired("This 6-digit verification code has already been used. Wait for the next authenticator code and try again.")
	case status == http.StatusPreconditionFailed || refusal.Error == "mfa_not_configured":
		failure = commandStepUpRequired("Authenticator TOTP MFA is not configured on this account. Enroll TOTP before submitting high-risk commands.")
	case status == http.StatusLocked || refusal.Error == "mfa_locked":
		failure = commandStepUpRequired("Authenticator verification is temporarily locked after repeated invalid attempts. Wait before trying again.")
	default:
		failure = commandUnavailable("The identity service could not verify the step-up code.")
	}
	return &failure
}

func (h *handler) commandSession(writer http.ResponseWriter, request *http.Request, requireCSRF bool) (bffSession, bool) {
	session, ok := routeSessionFromContext(request.Context())
	if !ok {
		var failure *identityFailure
		session, failure = h.identitySession(request)
		if failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return bffSession{}, false
		}
	}
	if requireCSRF && session.needsBrowserProof() {
		if h.identity == nil {
			writeProblem(writer, request, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "Command unavailable", "Session validation is unavailable.", true, nil)
			return bffSession{}, false
		}
		csrf := request.Header.Get("X-CSRF-Token")
		if csrf == "" {
			writeProblem(writer, request, http.StatusForbidden, "CSRF_REQUIRED", "CSRF token required", "A CSRF token is required for this Session request.", false, nil)
			return bffSession{}, false
		}
		if request.Header.Get("Origin") != h.identity.config.PublicOrigin || subtle.ConstantTimeCompare([]byte(csrf), []byte(session.CSRFToken)) != 1 {
			writeProblem(writer, request, http.StatusForbidden, "CSRF_INVALID", "CSRF token invalid", "The request Origin or CSRF token is invalid.", false, nil)
			return bffSession{}, false
		}
	}
	return session, true
}

func (h *handler) resolveCommandDevice(request *http.Request, session bffSession, deviceID string) (platformapi.Device, *commandFailure) {
	authorization, authFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionDeviceRead)
	if authFailure != nil {
		failure := commandFailure{status: authFailure.status, code: authFailure.code, title: authFailure.title, detail: authFailure.detail, retryable: authFailure.retryable}
		return platformapi.Device{}, &failure
	}
	route, _, matches := matchPublicRegistryRoute(http.MethodGet, "/api/v1/devices/"+url.PathEscape(deviceID))
	if !matches {
		failure := commandNotFound()
		return platformapi.Device{}, &failure
	}
	decision, decisionFailure := h.commandRegistryDecision(request, session, route, deviceID)
	if decisionFailure != nil {
		return platformapi.Device{}, decisionFailure
	}
	result := h.executeCoreRegistry(request.Context(), route, "", authorization.coreGrant, decision)
	if result.status != http.StatusOK {
		if result.status == http.StatusNotFound || result.status == http.StatusForbidden {
			failure := commandNotFound()
			return platformapi.Device{}, &failure
		}
		failure := commandUnavailable("Registry could not resolve the Command Device.")
		return platformapi.Device{}, &failure
	}
	var device platformapi.Device
	decoder := json.NewDecoder(bytes.NewReader(result.body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&device) != nil || ensureCommandJSONEOF(decoder) != nil || device.ID != deviceID || !isLowerUUIDv7(device.TenantID) || !isLowerUUIDv7(device.SiteID) || !strings.EqualFold(device.Status, "ACTIVE") {
		failure := commandUnavailable("Registry returned an invalid Command Device projection.")
		return platformapi.Device{}, &failure
	}
	return device, nil
}

func (h *handler) resolveAssetCommandTarget(request *http.Request, session bffSession, assetID, commandPointID string) (assetCommandTarget, *commandFailure) {
	assetAuthorization, authFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionAssetRead)
	if authFailure != nil {
		failure := commandFailure{status: authFailure.status, code: authFailure.code, title: authFailure.title, detail: authFailure.detail, retryable: authFailure.retryable}
		return assetCommandTarget{}, &failure
	}
	assetPath := strings.Replace(platformapi.GetAssetPathTemplate, "{assetId}", url.PathEscape(assetID), 1)
	assetRoute, _, matches := matchPublicRegistryRoute(http.MethodGet, assetPath)
	if !matches {
		failure := commandNotFound()
		return assetCommandTarget{}, &failure
	}
	assetDecision, decisionFailure := h.commandRegistryDecisionForPath(request, session, assetRoute, assetPath, "Asset lookup")
	if decisionFailure != nil {
		return assetCommandTarget{}, decisionFailure
	}
	assetResult := h.executeCoreRegistry(request.Context(), assetRoute, "", assetAuthorization.coreGrant, assetDecision)
	if assetResult.status != http.StatusOK {
		if assetResult.status == http.StatusNotFound || assetResult.status == http.StatusForbidden {
			failure := commandNotFound()
			return assetCommandTarget{}, &failure
		}
		failure := commandUnavailable("Registry could not resolve the Asset for this Command.")
		return assetCommandTarget{}, &failure
	}
	var asset platformapi.Asset
	decoder := json.NewDecoder(bytes.NewReader(assetResult.body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&asset) != nil || ensureCommandJSONEOF(decoder) != nil || asset.ID != assetID ||
		!isLowerUUIDv7(asset.TenantID) || !isLowerUUIDv7(asset.SiteID) || !strings.EqualFold(asset.Status, "ACTIVE") {
		failure := commandUnavailable("Registry returned an invalid Asset projection for Command execution.")
		return assetCommandTarget{}, &failure
	}

	assetModelAuthorization, authFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionAssetModelRead)
	if authFailure != nil {
		failure := commandFailure{status: authFailure.status, code: authFailure.code, title: authFailure.title, detail: authFailure.detail, retryable: authFailure.retryable}
		return assetCommandTarget{}, &failure
	}
	assetModelPath := strings.Replace(platformapi.GetSiteAssetModelPathTemplate, "{siteId}", url.PathEscape(asset.SiteID), 1)
	assetModelRoute, _, matches := matchPublicRegistryRoute(http.MethodGet, assetModelPath)
	if !matches {
		failure := commandUnavailable("Registry Asset Model route is unavailable for Command execution.")
		return assetCommandTarget{}, &failure
	}
	assetModelDecision, decisionFailure := h.commandRegistryDecisionForPath(request, session, assetModelRoute, assetModelPath, "Asset Model lookup")
	if decisionFailure != nil {
		return assetCommandTarget{}, decisionFailure
	}
	assetModelResult := h.executeCoreRegistry(request.Context(), assetModelRoute, "", assetModelAuthorization.coreGrant, assetModelDecision)
	if assetModelResult.status != http.StatusOK {
		failure := commandUnavailable("Registry could not resolve the authoritative Asset Model for this Command.")
		return assetCommandTarget{}, &failure
	}
	var assetModel platformapi.SiteAssetModel
	decoder = json.NewDecoder(bytes.NewReader(assetModelResult.body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&assetModel) != nil || ensureCommandJSONEOF(decoder) != nil || validateSiteAssetModel(assetModel, asset.SiteID) != nil || assetModel.TenantID != asset.TenantID {
		failure := commandUnavailable("Registry returned an invalid Asset Model for Command execution.")
		return assetCommandTarget{}, &failure
	}

	var point *platformapi.TelemetryPoint
	for index := range assetModel.TelemetryPoints {
		candidate := &assetModel.TelemetryPoints[index]
		if candidate.ID == commandPointID {
			point = candidate
			break
		}
	}
	if point == nil || point.TenantID != asset.TenantID || point.SiteID != asset.SiteID ||
		!strings.EqualFold(point.Status, "ACTIVE") || point.PointType != "COMMAND" || !point.Writable {
		failure := commandNotFound()
		return assetCommandTarget{}, &failure
	}
	now := time.Now().UTC()
	controlsAsset := false
	for _, relationship := range assetModel.Relationships {
		if relationship.FromType == "POINT" && relationship.FromID == point.ID && relationship.ToType == "ASSET" &&
			relationship.ToID == asset.ID && relationship.Role == "CONTROLS" && commandRelationshipCurrent(relationship, now) {
			controlsAsset = true
			break
		}
	}
	if !controlsAsset {
		failure := commandNotFound()
		return assetCommandTarget{}, &failure
	}

	capabilityName, _ := point.SourceMetadata["capability"].(string)
	capability := commandmodel.Capability(strings.TrimSpace(capabilityName))
	profile, supported := commandCapabilityProfile(capability)
	declaredRevision, _ := point.SourceMetadata["capabilityRevision"].(string)
	feedbackPointKey, _ := point.SourceMetadata["feedbackPointKey"].(string)
	if !supported || strings.TrimSpace(declaredRevision) != profile.Revision || strings.TrimSpace(feedbackPointKey) == "" {
		failure := commandUnavailable("The COMMAND Point does not declare a supported authoritative capability contract.")
		return assetCommandTarget{}, &failure
	}

	var device *platformapi.Device
	for index := range assetModel.Devices {
		candidate := &assetModel.Devices[index]
		if candidate.ID == point.ReportingDeviceID {
			device = candidate
			break
		}
	}
	if device == nil || device.TenantID != asset.TenantID || device.SiteID != asset.SiteID || !strings.EqualFold(device.Status, "ACTIVE") {
		failure := commandUnavailable("The COMMAND Point has no active reporting Device Endpoint.")
		return assetCommandTarget{}, &failure
	}

	var feedbackPoint *platformapi.TelemetryPoint
	for index := range assetModel.TelemetryPoints {
		candidate := &assetModel.TelemetryPoints[index]
		if candidate.ReportingDeviceID == device.ID && candidate.PointCode == feedbackPointKey && strings.EqualFold(candidate.Status, "ACTIVE") {
			feedbackPoint = candidate
			break
		}
	}
	if feedbackPoint == nil || (feedbackPoint.PointType != "STATE" && feedbackPoint.PointType != "TELEMETRY" && feedbackPoint.PointType != "SETTING") {
		failure := commandUnavailable("The COMMAND Point has no active authoritative feedback Point.")
		return assetCommandTarget{}, &failure
	}
	interlocks, interlockFailure := parseCommandInterlockChecks(point.SourceMetadata, asset.TenantID, asset.SiteID, assetModel.Devices, assetModel.TelemetryPoints)
	if interlockFailure != nil {
		return assetCommandTarget{}, interlockFailure
	}
	return assetCommandTarget{
		asset: asset, device: *device, point: *point, feedbackPoint: *feedbackPoint,
		capability: capability, interlocks: interlocks,
	}, nil
}

func parseCommandInterlockChecks(
	sourceMetadata map[string]any,
	tenantID string,
	siteID string,
	devices []platformapi.Device,
	points []platformapi.TelemetryPoint,
) ([]commandmodel.InterlockCheck, *commandFailure) {
	raw, present := sourceMetadata["interlockChecks"]
	if !present || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		failure := commandUnavailable("The COMMAND Point declares an invalid interlock precondition contract.")
		return nil, &failure
	}
	activeDevices := make(map[string]struct{}, len(devices))
	for _, device := range devices {
		if device.TenantID == tenantID && device.SiteID == siteID && strings.EqualFold(device.Status, "ACTIVE") {
			activeDevices[device.ID] = struct{}{}
		}
	}
	activeDevicePoints := make(map[string]struct{}, len(points))
	for _, pt := range points {
		if pt.TenantID == tenantID && pt.SiteID == siteID && strings.EqualFold(pt.Status, "ACTIVE") &&
			(pt.PointType == "STATE" || pt.PointType == "TELEMETRY" || pt.PointType == "SETTING") {
			activeDevicePoints[pt.ReportingDeviceID+"|"+pt.PointCode] = struct{}{}
		}
	}
	checks := make([]commandmodel.InterlockCheck, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			failure := commandUnavailable("The COMMAND Point declares an invalid interlock precondition contract.")
			return nil, &failure
		}
		deviceID, _ := obj["deviceId"].(string)
		deviceID = strings.TrimSpace(deviceID)
		key, _ := obj["key"].(string)
		key = strings.TrimSpace(key)
		expected, _ := obj["expected"].(string)
		expected = strings.TrimSpace(expected)
		if !isLowerUUIDv7(deviceID) || key == "" || (expected != "RUNNING" && expected != "STOPPED") {
			failure := commandUnavailable("The COMMAND Point declares an invalid interlock precondition contract.")
			return nil, &failure
		}
		if _, exists := activeDevices[deviceID]; !exists {
			failure := commandUnavailable("The COMMAND Point references an inactive or missing interlock peer device.")
			return nil, &failure
		}
		if _, exists := activeDevicePoints[deviceID+"|"+key]; !exists {
			failure := commandUnavailable("The COMMAND Point references an inactive or missing interlock peer feedback Point.")
			return nil, &failure
		}
		reason := ""
		if rawReason, hasReason := obj["reason"]; hasReason && rawReason != nil {
			text, isString := rawReason.(string)
			if !isString {
				failure := commandUnavailable("The COMMAND Point declares an invalid interlock precondition contract.")
				return nil, &failure
			}
			reason = strings.TrimSpace(text)
		}
		checks = append(checks, commandmodel.InterlockCheck{
			DeviceID: deviceID,
			Key:      key,
			Expected: expected,
			Reason:   reason,
		})
	}
	return checks, nil
}

func commandRelationshipCurrent(relationship platformapi.AssetRelationship, now time.Time) bool {
	if !strings.EqualFold(relationship.Status, "ACTIVE") {
		return false
	}
	validFrom, err := time.Parse(time.RFC3339Nano, relationship.ValidFrom)
	if err != nil || validFrom.After(now) {
		return false
	}
	if relationship.ValidTo == nil {
		return true
	}
	validTo, err := time.Parse(time.RFC3339Nano, *relationship.ValidTo)
	return err == nil && validTo.After(now)
}

func validCommandParameters(capability commandmodel.Capability, metadata map[string]any, parameters commandmodel.CommandParameters) bool {
	profile, ok := commandmodel.CapabilityProfileFor(capability)
	if !ok || !commandmodel.ParametersValid(capability, parameters) {
		return false
	}
	if profile.ParameterKey == "" {
		return true
	}
	declaredKey, _ := metadata["parameterKey"].(string)
	return declaredKey == profile.ParameterKey
}

func cloneCommandParameters(parameters commandmodel.CommandParameters) commandmodel.CommandParameters {
	cloned := make(commandmodel.CommandParameters, len(parameters))
	for key, value := range parameters {
		cloned[key] = value
	}
	return cloned
}

func (h *handler) commandRegistryDecision(request *http.Request, session bffSession, route publicRegistryRoute, deviceID string) (ownershipregistry.Decision, *commandFailure) {
	return h.commandRegistryDecisionForPath(request, session, route, "/api/v1/devices/"+url.PathEscape(deviceID), "Device lookup")
}

func (h *handler) commandRegistryDecisionForPath(request *http.Request, session bffSession, route publicRegistryRoute, publicPath, purpose string) (ownershipregistry.Decision, *commandFailure) {
	outer := routeDecisionFromContext(request.Context())
	decision := ownershipregistry.Decision{
		RouteKey:          http.MethodGet + " " + route.template,
		PathTemplate:      route.template,
		SelectedOwner:     ownershipregistry.OwnerCore,
		RegistryRevision:  outer.RegistryRevision,
		RouteRevision:     1,
		CompatibilityMode: "native",
	}
	if h.routeManager == nil {
		return decision, nil
	}
	resolved, err := h.routeManager.Current().Resolve(http.MethodGet, publicPath, session.TenantID)
	if err != nil || resolved.SelectedOwner != ownershipregistry.OwnerCore {
		failure := commandUnavailable("Registry route ownership is unavailable for the Command " + purpose + ".")
		return ownershipregistry.Decision{}, &failure
	}
	return resolved, nil
}

func (h *handler) readCommandCurrentState(request *http.Request, session bffSession, device platformapi.Device, feedbackKey string) (commandCurrentState, *commandFailure) {
	feedbackKey = strings.TrimSpace(feedbackKey)
	if feedbackKey == "" {
		failure := commandUnsafe("The requested Asset capability has no feedback Point.")
		return commandCurrentState{}, &failure
	}
	caller := telemetryCaller{principal: session.Principal, tenantID: session.TenantID, contextID: session.ID, expiresAt: session.ExpiresAt}
	target := telemetryauth.Target{DeviceID: device.ID, Keys: []string{feedbackKey}}
	authorization, authFailure := h.authorizeTelemetry(request.Context(), request, caller, telemetryauth.ActionSnapshotRead, []telemetryauth.Target{target})
	if authFailure != nil {
		if authFailure.status == http.StatusNotFound || authFailure.status == http.StatusForbidden {
			failure := commandNotFound()
			return commandCurrentState{}, &failure
		}
		failure := commandUnavailable("Telemetry authorization is unavailable for the Command precondition.")
		return commandCurrentState{}, &failure
	}
	response, telemetryFailure := h.executeTelemetryRuntime(request.Context(), request, http.MethodGet,
		internalTelemetrySinglePrefix+url.PathEscape(device.ID)+"/observation-snapshot", []string{feedbackKey}, nil, authorization.grant)
	if telemetryFailure != nil {
		failure := commandUnavailable("The current Device state is unavailable for control.")
		return commandCurrentState{}, &failure
	}
	var snapshot s2telemetryapi.DeviceObservationSnapshot
	if len(authorization.targets) != 1 || decodeStrictTelemetryJSON(response, &snapshot) != nil || !validateTelemetrySnapshot(snapshot, authorization.targets[0], []string{feedbackKey}) ||
		string(snapshot.TenantId) != device.TenantID || string(snapshot.SiteId) != device.SiteID || len(snapshot.Values) != 1 || snapshot.Values[0].Present == nil {
		failure := commandUnavailable("Telemetry Runtime returned an invalid current-state projection.")
		return commandCurrentState{}, &failure
	}
	present := snapshot.Values[0].Present
	var currentValue *float64
	var reportedValue commandmodel.ScalarValue
	switch present.ValueType {
	case "NUMBER":
		var numeric float64
		if json.Unmarshal(present.Value, &numeric) != nil {
			failure := commandUnsafe("The required feedback value is not a usable numeric observation.")
			return commandCurrentState{}, &failure
		}
		currentValue = &numeric
		reportedValue = commandmodel.NumberScalar(numeric)
	case "STRING":
		var text string
		if json.Unmarshal(present.Value, &text) != nil {
			failure := commandUnsafe("The required feedback value is not a usable string observation.")
			return commandCurrentState{}, &failure
		}
		reportedValue = commandmodel.TextScalar(text)
	case "BOOLEAN":
		var flag bool
		if json.Unmarshal(present.Value, &flag) != nil {
			failure := commandUnsafe("The required feedback value is not a usable boolean observation.")
			return commandCurrentState{}, &failure
		}
		reportedValue = commandmodel.BooleanScalar(flag)
	}
	observedAt, err := time.Parse(time.RFC3339Nano, string(present.SampledAt))
	if err != nil {
		failure := commandUnavailable("Telemetry Runtime returned an invalid observation timestamp.")
		return commandCurrentState{}, &failure
	}
	presence := "UNKNOWN"
	if snapshot.Presence.CurrentState != nil {
		presence = string(*snapshot.Presence.CurrentState)
	}
	state := commandCurrentState{
		EvaluationAvailability: string(snapshot.EvaluationAvailability), Presence: presence,
		Readiness: string(snapshot.TelemetryReadiness), Quality: string(present.Quality),
		BusinessRevision: uint64(snapshot.BusinessRevision), CurrentValue: currentValue, ReportedValue: reportedValue, ObservedAt: observedAt.UTC(),
	}
	if state.EvaluationAvailability != "AVAILABLE" || state.Presence != "ONLINE" || state.Readiness != "CURRENT" || present.Freshness != "FRESH" || state.Quality != "GOOD" {
		failure := commandUnsafe("The current Device state does not satisfy the control preconditions.")
		return commandCurrentState{}, &failure
	}
	return state, nil
}

func (h *handler) enforceCommandInterlocks(request *http.Request, session bffSession, target assetCommandTarget) *commandFailure {
	for _, check := range target.interlocks {
		peerDevice := platformapi.Device{
			ID:       check.DeviceID,
			TenantID: target.asset.TenantID,
			SiteID:   target.asset.SiteID,
			Status:   "ACTIVE",
		}
		peerState, peerFailure := h.readCommandCurrentState(request, session, peerDevice, check.Key)
		if peerFailure != nil {
			if peerFailure.status == http.StatusServiceUnavailable {
				return peerFailure
			}
			detail := "Prerequisite peer device state is unavailable or stale for safe execution."
			if check.Reason != "" {
				detail += " (" + check.Reason + ")"
			}
			failure := commandFailure{
				status:    http.StatusConflict,
				code:      "COMMAND_INTERLOCK_UNVERIFIABLE",
				title:     "Command Interlock Precondition Unavailable",
				detail:    detail,
				retryable: false,
			}
			return &failure
		}
		if !commandmodel.ScalarMatches(peerState.ReportedValue, commandmodel.TextScalar(check.Expected), 0) {
			detail := "Command is blocked by hydraulic interlock preconditions."
			if check.Reason != "" {
				detail += " " + check.Reason
			}
			failure := commandFailure{
				status:    http.StatusConflict,
				code:      "COMMAND_INTERLOCK_BLOCKED",
				title:     "Command Interlock Blocked",
				detail:    detail,
				retryable: false,
			}
			return &failure
		}
	}
	return nil
}

func (h *handler) authorizeCommand(request *http.Request, session bffSession, device platformapi.Device, capability commandmodel.Capability, purpose commandmodel.AuthorizationPurpose) (string, string, *commandFailure) {
	if h.identity == nil || h.command == nil {
		failure := commandUnavailable("Command authorization is not configured.")
		return "", "", &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.identity.config.IAMAudience,
		TenantID: session.TenantID, Actions: []string{"command:authorize"}, Scopes: []string{"session:" + session.ID},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	delegation, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := commandUnavailable("The Command authorization request could not be signed.")
		return "", "", &failure
	}
	profile, supported := commandCapabilityProfile(capability)
	if !supported {
		failure := commandNotFound()
		return "", "", &failure
	}
	input := commandauth.DecisionRequest{
		TenantID: session.TenantID, SiteID: device.SiteID, DeviceID: device.ID,
		Capability: capability, CapabilityRevision: profile.Revision, Purpose: purpose,
	}
	body, _ := json.Marshal(input)
	upstream, err := http.NewRequestWithContext(request.Context(), http.MethodPost, strings.TrimRight(h.identity.config.IAMURL, "/")+commandDecisionPath, bytes.NewReader(body))
	if err != nil {
		failure := commandUnavailable("The Command authorization request could not be constructed.")
		return "", "", &failure
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json, application/problem+json")
	upstream.Header.Set("X-Delegation-Grant", delegation)
	upstream.Header.Set("X-Request-ID", requestIDFromContext(request.Context()))
	observability.InjectHTTP(request.Context(), upstream.Header)
	response, err := h.identity.config.IAMHTTPClient.Do(upstream)
	if err != nil {
		failure := commandUnavailable("IAM Command authorization is temporarily unavailable.")
		return "", "", &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := commandUnavailable("IAM did not return a valid Command authorization decision.")
		return "", "", &failure
	}
	raw, err := readBoundedBody(response.Body, defaultCommandResponseLimit)
	if err != nil {
		failure := commandUnavailable("IAM returned an unreadable Command authorization decision.")
		return "", "", &failure
	}
	var decision commandauth.DecisionResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decision) != nil || ensureCommandJSONEOF(decoder) != nil {
		failure := commandUnavailable("IAM returned an invalid Command authorization decision.")
		return "", "", &failure
	}
	if !decision.Decision.Allowed {
		failure := commandNotFound()
		return "", "", &failure
	}
	if !h.validateCommandDecision(decision, session, device, capability, purpose, now) {
		failure := commandUnavailable("IAM returned a Command decision outside the authenticated boundary.")
		return "", "", &failure
	}
	return decision.Decision.PrincipalID, decision.DelegationGrant, nil
}

func (h *handler) validateCommandDecision(response commandauth.DecisionResponse, session bffSession, device platformapi.Device, capability commandmodel.Capability, purpose commandmodel.AuthorizationPurpose, now time.Time) bool {
	decision := response.Decision
	profile, supported := commandCapabilityProfile(capability)
	if !supported {
		return false
	}
	if decision.PrincipalID == "" || decision.Subject != session.Principal.Subject || decision.SubjectIssuer != session.Principal.Issuer ||
		decision.TenantID != session.TenantID || decision.SiteID != device.SiteID || decision.DeviceID != device.ID ||
		decision.Capability != capability || decision.CapabilityRevision != profile.Revision ||
		decision.Purpose != purpose || decision.MaximumRisk == "" || decision.PolicyRevision == "" || !commandauth.IsAllowReason(decision.ReasonCode) {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, decision.DecidedAt); err != nil {
		return false
	}
	return structurallyValidCommandGrant(response.DelegationGrant, h.command.iamGrantIssuer,
		h.identity.config.ExecutingWorkloadSPIFFE, h.command.backendAudience, decision, now)
}

func structurallyValidCommandGrant(grant, issuer, presenter, audience string, decision commandauth.Decision, now time.Time) bool {
	if grant == "" || len(grant) > commandauth.MaximumEncodedGrantSize {
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
	var claims commandauth.GrantClaims
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&claims) == nil && ensureCommandJSONEOF(decoder) == nil &&
		claims.Version == commandauth.GrantVersion && claims.Issuer == issuer && claims.Presenter == presenter && claims.Audience == audience &&
		claims.GrantID != "" && claims.TokenID != "" && claims.Purpose == decision.Purpose && claims.PrincipalID == decision.PrincipalID &&
		claims.TenantID == decision.TenantID && claims.SiteID == decision.SiteID && claims.DeviceID == decision.DeviceID &&
		claims.Capability == decision.Capability && claims.CapabilityRevision == decision.CapabilityRevision && claims.MaximumRisk == decision.MaximumRisk &&
		claims.PolicyRevision == decision.PolicyRevision && claims.EmergencyRevocationRevision == decision.EmergencyRevocationRevision && !claims.Transitive &&
		claims.IssuedAt <= now.Add(5*time.Second).Unix() && claims.ExpiresAt > now.Unix() && claims.ExpiresAt > claims.IssuedAt &&
		time.Duration(claims.ExpiresAt-claims.IssuedAt)*time.Second <= commandauth.MaximumGrantLifetime
}

func (h *handler) executeCommandCreate(ctx context.Context, prepared preparedCommand) (commandView, int, string, *commandFailure) {
	if h.command.baseURL == "" || h.command.httpClient == nil {
		failure := commandUnavailable("Command Service is not configured.")
		return commandView{}, 0, "", &failure
	}
	body, _ := json.Marshal(internalCommandCreate{
		TenantID: prepared.tenantID, SiteID: prepared.siteID, DeviceID: prepared.deviceID, PointID: prepared.pointID,
		PrincipalID: prepared.principalID, IdempotencyKey: prepared.idempotencyKey,
		Capability: prepared.capability, Parameters: cloneCommandParameters(prepared.parameters), VerificationPointKey: prepared.verificationPointKey,
		CurrentState: prepared.currentState,
	})
	requestContext, cancel := context.WithTimeout(ctx, h.command.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, h.command.baseURL+internalCommandsPath, bytes.NewReader(body))
	if err != nil {
		failure := commandUnavailable("The Command Service request could not be constructed.")
		return commandView{}, 0, "", &failure
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-Command-Grant", prepared.grant)
	request.Header.Set("X-Request-ID", requestIDFromContext(ctx))
	observability.InjectHTTP(ctx, request.Header)
	response, err := h.command.httpClient.Do(request)
	if err != nil {
		failure := commandUnavailable("Command Service is temporarily unavailable.")
		return commandView{}, 0, "", &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		failure := commandBackendFailure(response.StatusCode)
		return commandView{}, 0, "", &failure
	}
	view, ok := h.decodeCommandView(response.Body)
	if !ok || view.TenantID != prepared.tenantID || view.SiteID != prepared.siteID ||
		view.DeviceID != prepared.deviceID || view.PointID != prepared.pointID || view.Capability != prepared.capability {
		failure := commandUnavailable("Command Service returned an invalid accepted Command.")
		return commandView{}, 0, "", &failure
	}
	return view, http.StatusAccepted, response.Header.Get("Location"), nil
}

// reconcileCommand settles an OUTCOME_UNKNOWN Command on the caller's statement of whether it
// took effect. The caller needs control authority for the Device: a COMMAND_SUBMIT grant for
// the Command's Capability (#444).
func (h *handler) reconcileCommand(writer http.ResponseWriter, request *http.Request, commandID string) {
	session, ok := h.commandSession(writer, request, true)
	if !ok {
		return
	}
	if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
		return
	}
	if !isLowerUUID(commandID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Command was not found.", false, nil)
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "COMMAND_RECONCILIATION_REQUEST_INVALID", "Command reconciliation invalid", "The Command reconciliation request must use application/json.", false, nil)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 1024)
	var input struct {
		Outcome commandmodel.ReconciledOutcome `json:"outcome"`
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil ||
		(input.Outcome != commandmodel.ReconciledApplied && input.Outcome != commandmodel.ReconciledNotApplied) {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_RECONCILIATION_REQUEST_INVALID", "Command reconciliation invalid", "The request must state the outcome as APPLIED or NOT_APPLIED and nothing else.", false, nil)
		return
	}
	current, failure := h.executeCommandRead(request, session, commandID)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if current.Status != commandmodel.IntentOutcomeUnknown {
		writeProblem(writer, request, http.StatusConflict, "COMMAND_RECONCILIATION_INVALID", "Command reconciliation invalid", "Only a Command whose outcome is unknown can be reconciled.", false, nil)
		return
	}
	device, principalID, grant, failure := h.resolveAndAuthorizeCommandAction(request, session, current, commandmodel.AuthorizationCommandSubmit, "reconciliation")
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	reconciled, failure := h.executeCommandReconciliation(request.Context(), commandID, internalCommandReconciliation{
		TenantID: session.TenantID, SiteID: device.SiteID, DeviceID: device.ID, PrincipalID: principalID, Outcome: input.Outcome,
	}, grant)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, reconciled)
}

func (h *handler) executeCommandApproval(ctx context.Context, commandID string, input internalCommandApproval, grant string) (commandView, *commandFailure) {
	return h.executeCommandAction(ctx, commandID, "approve", input, input.TenantID, input.SiteID, input.DeviceID, grant)
}

func (h *handler) executeCommandReconciliation(ctx context.Context, commandID string, input internalCommandReconciliation, grant string) (commandView, *commandFailure) {
	return h.executeCommandAction(ctx, commandID, "reconcile", input, input.TenantID, input.SiteID, input.DeviceID, grant)
}

func (h *handler) executeCommandCancel(ctx context.Context, commandID string, input internalCommandCancel, grant string) (commandView, *commandFailure) {
	return h.executeCommandAction(ctx, commandID, "cancel", input, input.TenantID, input.SiteID, input.DeviceID, grant)
}

// executeCommandAction posts a governed action on one Command to Command Service and checks
// that the returned Command is still the one in the caller's scope.
func (h *handler) executeCommandAction(ctx context.Context, commandID, action string, input any, tenantID, siteID, deviceID, grant string) (commandView, *commandFailure) {
	if h.command == nil || h.command.baseURL == "" || h.command.httpClient == nil {
		failure := commandUnavailable("Command Service is not configured.")
		return commandView{}, &failure
	}
	body, err := json.Marshal(input)
	if err != nil {
		failure := commandUnavailable("The Command " + action + " request could not be encoded.")
		return commandView{}, &failure
	}
	requestContext, cancel := context.WithTimeout(ctx, h.command.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost,
		h.command.baseURL+internalCommandsPath+"/"+url.PathEscape(commandID)+"/"+action, bytes.NewReader(body))
	if err != nil {
		failure := commandUnavailable("The Command " + action + " request could not be constructed.")
		return commandView{}, &failure
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-Command-Grant", grant)
	request.Header.Set("X-Request-ID", requestIDFromContext(ctx))
	observability.InjectHTTP(ctx, request.Header)
	response, err := h.command.httpClient.Do(request)
	if err != nil {
		failure := commandUnavailable("Command Service " + action + " is temporarily unavailable.")
		return commandView{}, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := commandActionFailure(action, response.StatusCode)
		return commandView{}, &failure
	}
	view, ok := h.decodeCommandView(response.Body)
	if !ok || view.CommandID != commandID || view.TenantID != tenantID || view.SiteID != siteID || view.DeviceID != deviceID {
		failure := commandUnavailable("Command Service returned an invalid Command after " + action + ".")
		return commandView{}, &failure
	}
	return view, nil
}

func (h *handler) executeCommandRead(publicRequest *http.Request, session bffSession, commandID string) (commandView, *commandFailure) {
	if h.identity == nil || h.command == nil || h.command.baseURL == "" || h.command.httpClient == nil {
		failure := commandUnavailable("Command Service is not configured.")
		return commandView{}, &failure
	}
	readContext, failure := h.signCommandReadContext(session, "command:"+commandID)
	if failure != nil {
		return commandView{}, failure
	}
	ctx, cancel := context.WithTimeout(publicRequest.Context(), h.command.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.command.baseURL+internalCommandsPath+"/"+url.PathEscape(commandID), nil)
	if err != nil {
		failure := commandUnavailable("The Command read request could not be constructed.")
		return commandView{}, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-Tenant-ID", session.TenantID)
	request.Header.Set("X-Command-Read-Context", readContext)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), request.Header)
	response, err := h.command.httpClient.Do(request)
	if err != nil {
		failure := commandUnavailable("Command Service is temporarily unavailable.")
		return commandView{}, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := commandBackendFailure(response.StatusCode)
		return commandView{}, &failure
	}
	view, ok := h.decodeCommandView(response.Body)
	if !ok || view.CommandID != commandID || view.TenantID != session.TenantID {
		failure := commandUnavailable("Command Service returned an invalid Command projection.")
		return commandView{}, &failure
	}
	return view, nil
}

// signCommandReadContext signs a tenant read of Commands, narrowed to one Command or one Site.
func (h *handler) signCommandReadContext(session bffSession, scope string) (string, *commandFailure) {
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.command.backendAudience,
		TenantID: session.TenantID, Actions: []string{"command:read"},
		Scopes:         []string{"tenant:" + session.TenantID, scope},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	readContext, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := commandUnavailable("The Command read context could not be signed.")
		return "", &failure
	}
	return readContext, nil
}

func (h *handler) decodeCommandView(reader io.Reader) (commandView, bool) {
	body, err := readBoundedBody(reader, h.command.maxResponseBytes)
	if err != nil {
		return commandView{}, false
	}
	var view commandView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&view) != nil || ensureCommandJSONEOF(decoder) != nil || !validCommandView(view) {
		return commandView{}, false
	}
	return view, true
}

func validCommandView(view commandView) bool {
	profile, supported := commandCapabilityProfile(view.Capability)
	if !supported || view.SchemaVersion != 1 ||
		!isLowerUUID(view.CommandID) || !isLowerUUIDv7(view.TenantID) || !isLowerUUIDv7(view.SiteID) ||
		!isLowerUUIDv7(view.DeviceID) || !isLowerUUIDv7(view.PointID) || view.CapabilityRevision != profile.Revision || !validCommandIntentStatus(view.Status) || !validCommandRisk(view.Risk) ||
		!validCommandApprovalPolicy(view.ApprovalPolicy, view.ApprovalCount, view.RequiredApprovalCount) ||
		!validCommandParameters(view.Capability, map[string]any{"parameterKey": profile.ParameterKey}, view.Parameters) || view.DeviceCommandSequence == 0 || view.Version == 0 || view.SnapshotRevision == 0 ||
		view.CreatedAt.IsZero() || view.UpdatedAt.IsZero() || view.UpdatedAt.Before(view.CreatedAt) || !validCommandTimeline(view) {
		return false
	}
	return true
}

func validCommandIntentStatus(status commandmodel.IntentStatus) bool {
	switch status {
	case commandmodel.IntentSubmitted, commandmodel.IntentValidating, commandmodel.IntentAwaitingApproval,
		commandmodel.IntentApproved, commandmodel.IntentQueued, commandmodel.IntentDispatching,
		commandmodel.IntentSucceeded, commandmodel.IntentFailed, commandmodel.IntentRejected,
		commandmodel.IntentCancelled, commandmodel.IntentExpired, commandmodel.IntentOutcomeUnknown:
		return true
	default:
		return false
	}
}

func validCommandRisk(risk commandmodel.RiskLevel) bool {
	return risk == commandmodel.RiskLow || risk == commandmodel.RiskMedium || risk == commandmodel.RiskHigh
}

func validCommandApprovalPolicy(policy commandmodel.ApprovalPolicy, approvalCount, requiredCount int) bool {
	expected := -1
	switch policy {
	case commandmodel.ApprovalNone:
		expected = 0
	case commandmodel.ApprovalSingleApprover:
		expected = 1
	case commandmodel.ApprovalTwoPerson:
		expected = 2
	}
	return expected >= 0 && requiredCount == expected && approvalCount >= 0 && approvalCount <= requiredCount
}

func validCommandTimeline(view commandView) bool {
	if len(view.Transitions) == 0 || len(view.Transitions) > 256 {
		return false
	}
	previousVersion := uint64(0)
	for _, transition := range view.Transitions {
		if transition.FromStatus != nil && !validCommandIntentStatus(*transition.FromStatus) {
			return false
		}
		if !validCommandIntentStatus(transition.ToStatus) || strings.TrimSpace(transition.Reason) == "" || len(transition.Reason) > 256 ||
			(transition.ActorType != "PRINCIPAL" && transition.ActorType != "WORKLOAD") || transition.OccurredAt.IsZero() ||
			transition.Version <= previousVersion || transition.Version > view.Version {
			return false
		}
		previousVersion = transition.Version
	}
	last := view.Transitions[len(view.Transitions)-1]
	return last.ToStatus == view.Status && last.Version == view.Version
}

type commandFailure struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

func commandNotFound() commandFailure {
	return commandFailure{status: http.StatusNotFound, code: "RESOURCE_NOT_FOUND", title: "Resource not found", detail: "The requested Command resource was not found.", retryable: false}
}

func commandUnavailable(detail string) commandFailure {
	return commandFailure{status: http.StatusServiceUnavailable, code: "COMMAND_UNAVAILABLE", title: "Command unavailable", detail: detail, retryable: true}
}

func commandUnsafe(detail string) commandFailure {
	return commandFailure{status: http.StatusConflict, code: "COMMAND_CURRENT_STATE_UNSAFE", title: "Current state unsafe", detail: detail, retryable: false}
}

func commandActionFailure(action string, status int) commandFailure {
	if status == http.StatusConflict {
		switch action {
		case "cancel":
			return commandFailure{status: status, code: "COMMAND_CANCEL_INVALID", title: "Command cannot be cancelled", detail: "Only a pending or queued Command can be cancelled before dispatch begins.", retryable: false}
		case "reconcile":
			return commandFailure{status: status, code: "COMMAND_RECONCILIATION_INVALID", title: "Command reconciliation invalid", detail: "Only a Command whose outcome is unknown can be reconciled.", retryable: false}
		case "approve":
			return commandFailure{status: status, code: "COMMAND_APPROVAL_INVALID", title: "Command approval invalid", detail: "The Command is not awaiting another approval.", retryable: false}
		}
	}
	return commandBackendFailure(status)
}

func commandBackendFailure(status int) commandFailure {
	switch status {
	case http.StatusBadRequest:
		return commandFailure{status: status, code: "COMMAND_REQUEST_INVALID", title: "Command request invalid", detail: "Command Service rejected the request.", retryable: false}
	case http.StatusConflict:
		return commandFailure{status: status, code: "COMMAND_IDEMPOTENCY_CONFLICT", title: "Idempotency conflict", detail: "The Idempotency-Key is already bound to another Command payload.", retryable: false}
	case http.StatusNotFound, http.StatusForbidden:
		return commandNotFound()
	default:
		return commandUnavailable("Command Service could not complete the request.")
	}
}

func (h *handler) writeCommandFailure(writer http.ResponseWriter, request *http.Request, failure commandFailure) {
	writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
}

func ensureCommandJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}
