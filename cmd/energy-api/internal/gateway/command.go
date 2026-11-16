package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

const (
	publicCommandsPath           = "/api/v1/commands"
	internalOperatorPrefix       = commandservice.InternalOperatorPrefix
	internalOperatorCommandsPath = commandservice.InternalOperatorCommandsPath
	commandControlContextHeader  = "X-Command-Control-Context"
	defaultCommandResponseLimit  = int64(256 << 10)
	maximumCommandRequestBody    = int64(64 << 10)

	identityTOTPVerifyPath     = "/internal/v1/mfa/totp/verify"
	identityTOTPVerifyAudience = "identity-service"
	identityTOTPVerifyAction   = "mfa:totp:verify"

	defaultListedCommands = 50
	maximumListedCommands = 100
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

type createCommandRequest struct {
	AssetID          string                         `json:"assetId"`
	CommandPointID   string                         `json:"commandPointId"`
	Parameters       commandmodel.CommandParameters `json:"parameters"`
	VerificationCode string                         `json:"verificationCode,omitempty"`
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
	return kind, raw, true
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

	var extraActions []string
	if strings.TrimSpace(input.VerificationCode) != "" {
		if stepUpFailure := h.verifySubmitStepUpTOTP(request, session, input.VerificationCode); stepUpFailure != nil {
			h.writeCommandFailure(writer, request, *stepUpFailure)
			return
		}
		extraActions = append(extraActions, commandservice.StepUpActionTOTP)
	}

	payload := commandservice.SubmitOperatorCommandInput{
		AssetID:        input.AssetID,
		CommandPointID: input.CommandPointID,
		Parameters:     input.Parameters,
		IdempotencyKey: idempotencyKey,
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPost, internalOperatorCommandsPath, payload,
		map[string]string{"Idempotency-Key": idempotencyKey}, h.command.maxResponseBytes, extraActions...,
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	view, ok := decodeAndValidateCommandView(rawBody)
	if !ok || view.TenantID != session.TenantID {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Command projection."))
		return
	}
	writer.Header().Set("Location", "/api/v1/commands/"+view.CommandID)
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusAccepted, view)
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
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodGet, internalOperatorCommandsPath+"/"+url.PathEscape(commandID), nil,
		nil, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	view, ok := decodeAndValidateCommandView(rawBody)
	if !ok || view.CommandID != commandID || view.TenantID != session.TenantID {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Command projection."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, view)
}

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
	target := internalOperatorCommandsPath + "?" + url.Values{"siteId": {siteID}, "limit": {strconv.Itoa(limit)}}.Encode()
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodGet, target, nil, nil, h.command.maxResponseBytes*int64(limit),
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var page struct {
		Data []commandView `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&page) != nil || ensureCommandJSONEOF(decoder) != nil || page.Data == nil || len(page.Data) > limit {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Command list."))
		return
	}
	for _, view := range page.Data {
		if !validCommandView(view) || view.TenantID != session.TenantID || view.SiteID != siteID {
			h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Command projection."))
			return
		}
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, struct {
		Data []commandView `json:"data"`
	}{Data: page.Data})
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
	var extraActions []string
	if freshMFAAssurance(session, h.now()) {
		extraActions = append(extraActions, commandservice.StepUpActionSessionMFA)
	}
	h.forwardCommandItemMutation(writer, request, session, commandID, "approve", struct{}{}, extraActions...)
}

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
	h.forwardCommandItemMutation(writer, request, session, commandID, "reconcile", input)
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
		decoder := json.NewDecoder(bytes.NewReader(rawBody))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil {
			writeProblem(writer, request, http.StatusBadRequest, "COMMAND_CANCEL_REQUEST_INVALID", "Command cancel invalid", "The Command cancel request body is invalid.", false, nil)
			return
		}
		if utf8.RuneCountInString(strings.TrimSpace(input.Reason)) > 256 {
			writeProblem(writer, request, http.StatusBadRequest, "COMMAND_CANCEL_REQUEST_INVALID", "Command cancel invalid", "reason must be at most 256 characters.", false, nil)
			return
		}
	}
	h.forwardCommandItemMutation(writer, request, session, commandID, "cancel", input)
}

func (h *handler) forwardCommandItemMutation(
	writer http.ResponseWriter,
	request *http.Request,
	session bffSession,
	commandID, action string,
	payload any,
	extraActions ...string,
) {
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPost,
		internalOperatorCommandsPath+"/"+url.PathEscape(commandID)+"/"+action,
		payload, nil, h.command.maxResponseBytes, extraActions...,
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeOperatorProblem(writer, request, status, rawBody, action)
		return
	}
	view, ok := decodeAndValidateCommandView(rawBody)
	if !ok || view.CommandID != commandID || view.TenantID != session.TenantID {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Command projection."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, view)
}

func (h *handler) callOperatorCommandBackend(
	publicRequest *http.Request,
	session bffSession,
	method, internalPath string,
	payload any,
	extraHeaders map[string]string,
	maxResponseBytes int64,
	extraActions ...string,
) (int, []byte, *commandFailure) {
	if h.identity == nil || h.identity.config.DelegationSigner == nil ||
		h.command == nil || h.command.baseURL == "" || h.command.httpClient == nil {
		failure := commandUnavailable("Command Service is not configured.")
		return 0, nil, &failure
	}
	delegation, failure := h.signCommandControlContext(session, extraActions...)
	if failure != nil {
		return 0, nil, failure
	}
	var bodyReader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			failure := commandUnavailable("The Command request payload could not be encoded.")
			return 0, nil, &failure
		}
		bodyReader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(publicRequest.Context(), h.command.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, h.command.baseURL+internalPath, bodyReader)
	if err != nil {
		failure := commandUnavailable("The Command request could not be constructed.")
		return 0, nil, &failure
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-Tenant-ID", session.TenantID)
	request.Header.Set(commandControlContextHeader, delegation)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	for key, val := range extraHeaders {
		if strings.TrimSpace(val) != "" {
			request.Header.Set(key, val)
		}
	}
	observability.InjectHTTP(publicRequest.Context(), request.Header)

	response, err := h.command.httpClient.Do(request)
	if err != nil {
		failure := commandUnavailable("Command Service is temporarily unavailable.")
		return 0, nil, &failure
	}
	defer response.Body.Close()

	if maxResponseBytes <= 0 {
		maxResponseBytes = h.command.maxResponseBytes
	}
	body, err := readBoundedBody(response.Body, maxResponseBytes)
	if err != nil {
		failure := commandUnavailable("Command Service response exceeded size limit.")
		return 0, nil, &failure
	}
	return response.StatusCode, body, nil
}

func (h *handler) signCommandControlContext(session bffSession, extraActions ...string) (string, *commandFailure) {
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	scopes := make([]string, 0, 2+len(extraActions))
	scopes = append(scopes, "tenant:"+session.TenantID, "session:"+session.ID)
	scopes = append(scopes, extraActions...)
	claims := identitycontext.DelegationClaims{
		Issuer:           h.identity.config.ExecutingWorkloadSPIFFE,
		Subject:          session.Principal.Subject,
		SubjectIssuer:    session.Principal.Issuer,
		DisplayName:      session.Principal.DisplayName,
		Email:            session.Principal.Email,
		Roles:            append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience:         h.command.backendAudience,
		TenantID:         session.TenantID,
		Actions:          []string{commandservice.OperatorControlContextAction},
		Scopes:           scopes,
		PolicyRevision:   h.identity.config.PolicyRevision,
		SessionID:        session.ID,
		IssuedAt:         now.Unix(),
		ExpiresAt:        expiresAt.Unix(),
		TokenID:          randomURLToken(16),
	}
	signed, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := commandUnavailable("The Command control context could not be signed.")
		return "", &failure
	}
	return signed, nil
}

func (h *handler) writeOperatorProblem(writer http.ResponseWriter, request *http.Request, status int, rawBody []byte, action string) {
	var upstream platformapi.ProblemDetails
	if json.Unmarshal(rawBody, &upstream) == nil && upstream.Code != "" && status >= http.StatusBadRequest && status < 600 {
		if upstream.Title == "" {
			upstream.Title = http.StatusText(status)
		}
		if upstream.Type == "" {
			upstream.Type = "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(upstream.Code, "_", "-"))
		}
		upstream.Status = status
		upstream.Instance = request.URL.Path
		writer.Header().Set("Content-Type", "application/problem+json")
		writer.Header().Set("Cache-Control", "private, no-store")
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(upstream)
		return
	}
	if action != "" {
		h.writeCommandFailure(writer, request, commandActionFailure(action, status))
		return
	}
	h.writeCommandFailure(writer, request, commandBackendFailure(status))
}

func commandApprovalRequiresStepUp(current commandView, session bffSession, now time.Time) bool {
	if current.Risk != commandmodel.RiskHigh && current.Risk != commandmodel.RiskCritical {
		return false
	}
	return !freshMFAAssurance(session, now)
}

func freshMFAAssurance(session bffSession, now time.Time) bool {
	if session.AuthenticationACR != authenticationACRMFA || len(session.AuthenticationAMR) == 0 || session.AuthenticationTime.IsZero() {
		return false
	}
	age := now.Sub(session.AuthenticationTime)
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
		if request.Header.Get("Origin") != h.identity.config.PublicOrigin || subtleConstantTimeEquals(csrf, session.CSRFToken) != 1 {
			writeProblem(writer, request, http.StatusForbidden, "CSRF_INVALID", "CSRF token invalid", "The request Origin or CSRF token is invalid.", false, nil)
			return bffSession{}, false
		}
	}
	return session, true
}

func subtleConstantTimeEquals(a, b string) int {
	if len(a) != len(b) {
		return 0
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	if diff == 0 {
		return 1
	}
	return 0
}

func decodeAndValidateCommandView(body []byte) (commandView, bool) {
	var view commandView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&view) != nil || ensureCommandJSONEOF(decoder) != nil || !validCommandView(view) {
		return commandView{}, false
	}
	return view, true
}

func validCommandView(view commandView) bool {
	profile, supported := commandmodel.CapabilityProfileFor(view.Capability)
	if !supported || view.SchemaVersion != 1 ||
		!isLowerUUID(view.CommandID) || !isLowerUUIDv7(view.TenantID) || !isLowerUUIDv7(view.SiteID) ||
		!isLowerUUIDv7(view.DeviceID) || !isLowerUUIDv7(view.PointID) || view.CapabilityRevision != profile.Revision ||
		!validCommandIntentStatus(view.Status) || !validCommandRisk(view.Risk) ||
		!validCommandApprovalPolicy(view.ApprovalPolicy, view.ApprovalCount, view.RequiredApprovalCount) ||
		view.DeviceCommandSequence == 0 || view.Version == 0 ||
		view.CreatedAt.IsZero() || view.UpdatedAt.IsZero() || view.UpdatedAt.Before(view.CreatedAt) ||
		!validCommandTimeline(view) {
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
	return risk == commandmodel.RiskLow || risk == commandmodel.RiskMedium || risk == commandmodel.RiskHigh || risk == commandmodel.RiskCritical
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
