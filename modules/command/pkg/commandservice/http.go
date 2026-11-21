package commandservice

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	InternalCommandsPath        = "/internal/v1/commands"
	InternalCommandsCancelBatch = "/internal/v1/commands/cancel-pending"
	commandGrantHeader          = "X-Command-Grant"
	commandReadContextHeader    = "X-Command-Read-Context"
	commandControlContextHeader = "X-Command-Control-Context"
	tenantHeader                = "X-Tenant-ID"
	maximumInternalRequestBody  = int64(64 << 10)
)

type HTTPAuthority interface {
	Submit(context.Context, commandmodel.SubmitRequest) (SubmitResult, error)
	Get(context.Context, string, string) (commandmodel.CommandIntent, error)
	List(ctx context.Context, tenantID, siteID string, limit int) ([]commandmodel.CommandIntent, error)
	Approve(context.Context, commandmodel.ApproveRequest) (commandmodel.CommandIntent, error)
	Reconcile(context.Context, commandmodel.ReconcileRequest) (commandmodel.CommandIntent, error)
	Cancel(context.Context, commandmodel.CancelRequest) (commandmodel.CommandIntent, error)
	CancelPending(context.Context, commandmodel.CancelPendingRequest) ([]string, error)
}

type HTTPConfig struct {
	Authority                  HTTPAuthority
	CommandGrantPublicKey      crypto.PublicKey
	CommandGrantIssuer         string
	GatewaySPIFFE              string
	CommandGrantAudience       string
	CommandGrantUseChecker     func(commandauth.GrantClaims) (commandauth.UseStatus, error)
	GatewayDelegationPublicKey crypto.PublicKey
	GatewayReadAudience        string
	Now                        func() time.Time
}

type HTTPHandler struct {
	config HTTPConfig
	now    func() time.Time
}

type internalCreateCommandRequest struct {
	TenantID             string                         `json:"tenantId"`
	SiteID               string                         `json:"siteId"`
	DeviceID             string                         `json:"deviceId"`
	PointID              string                         `json:"pointId"`
	PrincipalID          string                         `json:"principalId"`
	IdempotencyKey       string                         `json:"idempotencyKey"`
	TraceID              string                         `json:"traceId,omitempty"`
	Capability           commandmodel.Capability        `json:"capability"`
	Parameters           commandmodel.CommandParameters `json:"parameters"`
	VerificationPointKey string                         `json:"verificationPointKey"`
	PreconditionMode     commandmodel.PreconditionMode  `json:"preconditionMode,omitempty"`
	BypassReason         string                         `json:"bypassReason,omitempty"`
	PriorityTier         commandmodel.PriorityTier      `json:"priorityTier,omitempty"`
	CommandSource        commandmodel.CommandSource     `json:"commandSource,omitempty"`
	CurrentState         internalCurrentState           `json:"currentState"`
}

type internalCurrentState struct {
	EvaluationAvailability string    `json:"evaluationAvailability"`
	Presence               string    `json:"presence"`
	Readiness              string    `json:"readiness"`
	Quality                string    `json:"quality"`
	BusinessRevision       uint64    `json:"businessRevision"`
	CurrentValue           *float64  `json:"currentValue,omitempty"`
	ObservedAt             time.Time `json:"observedAt"`
}

type internalApproveCommandRequest struct {
	TenantID    string `json:"tenantId"`
	SiteID      string `json:"siteId"`
	DeviceID    string `json:"deviceId"`
	PrincipalID string `json:"principalId"`
}

type internalReconcileCommandRequest struct {
	TenantID    string                         `json:"tenantId"`
	SiteID      string                         `json:"siteId"`
	DeviceID    string                         `json:"deviceId"`
	PrincipalID string                         `json:"principalId"`
	Outcome     commandmodel.ReconciledOutcome `json:"outcome"`
}

type internalCancelCommandRequest struct {
	TenantID    string `json:"tenantId"`
	SiteID      string `json:"siteId"`
	DeviceID    string `json:"deviceId"`
	PrincipalID string `json:"principalId"`
	Reason      string `json:"reason,omitempty"`
}

type internalCancelPendingRequest struct {
	TenantID             string   `json:"tenantId"`
	SiteID               string   `json:"siteId"`
	DeviceIDs            []string `json:"deviceIds,omitempty"`
	PrincipalID          string   `json:"principalId"`
	Reason               string   `json:"reason,omitempty"`
	CausationID          string   `json:"causationId,omitempty"`
	AbortActiveSequences bool     `json:"abortActiveSequences,omitempty"`
}

type CommandTransitionView struct {
	FromStatus *commandmodel.IntentStatus `json:"fromStatus,omitempty"`
	ToStatus   commandmodel.IntentStatus  `json:"toStatus"`
	Reason     string                     `json:"reason"`
	ActorType  string                     `json:"actorType"`
	OccurredAt time.Time                  `json:"occurredAt"`
	Version    uint64                     `json:"version"`
}

type CommandView struct {
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
	PriorityTier          commandmodel.PriorityTier      `json:"priorityTier,omitempty"`
	CommandSource         commandmodel.CommandSource     `json:"commandSource,omitempty"`
	ApprovalCount         int                            `json:"approvalCount"`
	RequiredApprovalCount int                            `json:"requiredApprovalCount"`
	Parameters            commandmodel.CommandParameters `json:"parameters"`
	DeviceCommandSequence uint64                         `json:"deviceCommandSequence"`
	Version               uint64                         `json:"version"`
	SnapshotRevision      uint64                         `json:"snapshotRevision"`
	Transitions           []CommandTransitionView        `json:"transitions"`
	CreatedAt             time.Time                      `json:"createdAt"`
	UpdatedAt             time.Time                      `json:"updatedAt"`
}

type commandProblem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

func NewHTTPHandler(config HTTPConfig) (*HTTPHandler, error) {
	if config.Authority == nil || config.CommandGrantPublicKey == nil || config.GatewayDelegationPublicKey == nil ||
		strings.TrimSpace(config.CommandGrantIssuer) == "" || strings.TrimSpace(config.GatewaySPIFFE) == "" ||
		strings.TrimSpace(config.CommandGrantAudience) == "" || strings.TrimSpace(config.GatewayReadAudience) == "" || config.CommandGrantUseChecker == nil {
		return nil, errors.New("command HTTP security configuration is incomplete")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &HTTPHandler{config: config, now: now}, nil
}

func (h *HTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == InternalCommandsPath {
		switch request.Method {
		case http.MethodPost:
			h.createCommand(writer, request)
		case http.MethodGet:
			h.listCommands(writer, request)
		default:
			writer.Header().Set("Allow", "GET, POST")
			writeCommandProblem(writer, http.StatusMethodNotAllowed, "COMMAND_METHOD_NOT_ALLOWED", false)
		}
		return
	}
	if request.URL.Path == InternalCommandsCancelBatch {
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			writeCommandProblem(writer, http.StatusMethodNotAllowed, "COMMAND_METHOD_NOT_ALLOWED", false)
			return
		}
		h.cancelPendingCommands(writer, request)
		return
	}
	prefix := InternalCommandsPath + "/"
	if strings.HasPrefix(request.URL.Path, prefix) {
		raw := strings.TrimPrefix(request.URL.Path, prefix)
		if strings.HasSuffix(raw, "/reconcile") {
			commandID := strings.TrimSuffix(raw, "/reconcile")
			if commandID == "" || strings.Contains(commandID, "/") {
				writeCommandProblem(writer, http.StatusNotFound, "COMMAND_ROUTE_NOT_FOUND", false)
				return
			}
			if request.Method != http.MethodPost {
				writer.Header().Set("Allow", http.MethodPost)
				writeCommandProblem(writer, http.StatusMethodNotAllowed, "COMMAND_METHOD_NOT_ALLOWED", false)
				return
			}
			h.reconcileCommand(writer, request, commandID)
			return
		}
		if strings.HasSuffix(raw, "/cancel") {
			commandID := strings.TrimSuffix(raw, "/cancel")
			if commandID == "" || strings.Contains(commandID, "/") {
				writeCommandProblem(writer, http.StatusNotFound, "COMMAND_ROUTE_NOT_FOUND", false)
				return
			}
			if request.Method != http.MethodPost {
				writer.Header().Set("Allow", http.MethodPost)
				writeCommandProblem(writer, http.StatusMethodNotAllowed, "COMMAND_METHOD_NOT_ALLOWED", false)
				return
			}
			h.cancelCommand(writer, request, commandID)
			return
		}
		if strings.HasSuffix(raw, "/approve") {
			commandID := strings.TrimSuffix(raw, "/approve")
			if commandID != "" && !strings.Contains(commandID, "/") {
				if request.Method != http.MethodPost {
					writer.Header().Set("Allow", http.MethodPost)
					writeCommandProblem(writer, http.StatusMethodNotAllowed, "COMMAND_METHOD_NOT_ALLOWED", false)
					return
				}
				h.approveCommand(writer, request, commandID)
				return
			}
		}
	}
	if strings.HasPrefix(request.URL.Path, prefix) && !strings.Contains(strings.TrimPrefix(request.URL.Path, prefix), "/") {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writeCommandProblem(writer, http.StatusMethodNotAllowed, "COMMAND_METHOD_NOT_ALLOWED", false)
			return
		}
		h.getCommand(writer, request, strings.TrimPrefix(request.URL.Path, prefix))
		return
	}
	writeCommandProblem(writer, http.StatusNotFound, "COMMAND_ROUTE_NOT_FOUND", false)
}

func (h *HTTPHandler) createCommand(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumInternalRequestBody)
	var input internalCreateCommandRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil || !validInternalCreate(input) {
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", false)
		return
	}
	claims, err := commandauth.VerifyGrant(h.config.CommandGrantPublicKey, request.Header.Get(commandGrantHeader))
	if err != nil {
		writeCommandProblem(writer, http.StatusUnauthorized, "COMMAND_GRANT_INVALID", false)
		return
	}
	profile, supported := commandmodel.CapabilityProfileFor(input.Capability)
	if !supported {
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", false)
		return
	}
	validation := commandauth.Validation{
		Now: h.now().UTC(), Issuer: h.config.CommandGrantIssuer, Presenter: h.config.GatewaySPIFFE,
		Audience: h.config.CommandGrantAudience, Purpose: commandmodel.AuthorizationCommandSubmit,
		PrincipalID: input.PrincipalID, TenantID: input.TenantID, SiteID: input.SiteID, DeviceID: input.DeviceID,
		Capability: input.Capability, CapabilityRevision: profile.Revision,
		Risk: commandmodel.RiskLow, UseChecker: h.config.CommandGrantUseChecker,
	}
	if commandauth.ValidateGrant(claims, validation) != nil {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_GRANT_REJECTED", false)
		return
	}
	traceID := commandmodel.NormalizeTraceID(input.TraceID)
	if traceID == "" {
		traceID = traceIDFromHTTPRequest(request)
	}
	result, err := h.config.Authority.Submit(request.Context(), commandmodel.SubmitRequest{
		TenantID: input.TenantID, SiteID: input.SiteID, DeviceID: input.DeviceID, PointID: input.PointID,
		PrincipalID: input.PrincipalID, IdempotencyKey: input.IdempotencyKey, TraceID: traceID,
		Capability: input.Capability, Parameters: cloneParameters(input.Parameters), VerificationPointKey: input.VerificationPointKey,
		PreconditionMode: input.PreconditionMode, BypassReason: input.BypassReason,
		PriorityTier: input.PriorityTier, CommandSource: input.CommandSource,
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: input.CurrentState.EvaluationAvailability,
			Presence:               input.CurrentState.Presence, Readiness: input.CurrentState.Readiness,
			Quality: input.CurrentState.Quality, BusinessRevision: input.CurrentState.BusinessRevision,
			CurrentValue: input.CurrentState.CurrentValue, ObservedAt: input.CurrentState.ObservedAt,
		},
		Authorization: commandauth.Snapshot(claims),
	})
	if err != nil {
		writeAuthorityError(writer, err)
		return
	}
	writer.Header().Set("Location", "/api/v1/commands/"+result.Intent.ID)
	writeCommandJSON(writer, http.StatusAccepted, commandView(result.Intent))
}

func (h *HTTPHandler) approveCommand(writer http.ResponseWriter, request *http.Request, commandID string) {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumInternalRequestBody)
	var input internalApproveCommandRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil || !validInternalApproval(commandID, input) {
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_APPROVAL_REQUEST_INVALID", false)
		return
	}
	claims, err := commandauth.VerifyGrant(h.config.CommandGrantPublicKey, request.Header.Get(commandGrantHeader))
	if err != nil {
		writeCommandProblem(writer, http.StatusUnauthorized, "COMMAND_GRANT_INVALID", false)
		return
	}
	intent, err := h.config.Authority.Get(request.Context(), input.TenantID, commandID)
	if err != nil {
		writeAuthorityError(writer, err)
		return
	}
	if _, _, _, supported := commandCapabilityProfile(intent.Capability); intent.SiteID != input.SiteID || intent.DeviceID != input.DeviceID || !supported {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_GRANT_REJECTED", false)
		return
	}
	validation := commandauth.Validation{
		Now: h.now().UTC(), Issuer: h.config.CommandGrantIssuer, Presenter: h.config.GatewaySPIFFE,
		Audience: h.config.CommandGrantAudience, Purpose: commandmodel.AuthorizationCommandApprove,
		PrincipalID: input.PrincipalID, TenantID: input.TenantID, SiteID: input.SiteID, DeviceID: input.DeviceID,
		Capability: intent.Capability, CapabilityRevision: intent.CapabilityRevision,
		Risk: intent.Risk, UseChecker: h.config.CommandGrantUseChecker,
	}
	if commandauth.ValidateGrant(claims, validation) != nil {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_GRANT_REJECTED", false)
		return
	}
	now := h.now().UTC()
	approvalID, err := newUUID(now)
	if err != nil {
		writeCommandProblem(writer, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", true)
		return
	}
	authorization := commandauth.Snapshot(claims)
	approved, err := h.config.Authority.Approve(request.Context(), commandmodel.ApproveRequest{
		TenantID:  input.TenantID,
		CommandID: commandID,
		Approval: commandmodel.ApprovalEvidence{
			ApprovalID: approvalID, ApproverID: input.PrincipalID,
			Policy: intent.ApprovalPolicy, PayloadHash: intent.PayloadHash,
			CapabilityRevision: intent.CapabilityRevision, Risk: intent.Risk,
			RiskRuleRevision: intent.RiskSnapshot.RuleRevision, Authorization: authorization,
			IssuedAt: now, ExpiresAt: authorization.ExpiresAt,
		},
	})
	if err != nil {
		writeAuthorityError(writer, err)
		return
	}
	writeCommandJSON(writer, http.StatusOK, commandView(approved))
}

// reconcileCommand settles an OUTCOME_UNKNOWN command. The reconciler needs the same
// COMMAND_SUBMIT grant for the command's Device and Capability as submitting it (#444).
func (h *HTTPHandler) reconcileCommand(writer http.ResponseWriter, request *http.Request, commandID string) {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumInternalRequestBody)
	var input internalReconcileCommandRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil || len(commandID) > 256 ||
		(input.Outcome != commandmodel.ReconciledApplied && input.Outcome != commandmodel.ReconciledNotApplied) {
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_RECONCILIATION_REQUEST_INVALID", false)
		return
	}
	claims, ok := h.authorizeSubmittedDeviceAction(writer, request, commandID, input.TenantID, input.SiteID, input.DeviceID, input.PrincipalID)
	if !ok {
		return
	}
	reconciled, err := h.config.Authority.Reconcile(request.Context(), commandmodel.ReconcileRequest{
		TenantID: input.TenantID, CommandID: commandID, Outcome: input.Outcome, Authorization: commandauth.Snapshot(claims),
	})
	if err != nil {
		writeAuthorityError(writer, err)
		return
	}
	writeCommandJSON(writer, http.StatusOK, commandView(reconciled))
}

func (h *HTTPHandler) cancelCommand(writer http.ResponseWriter, request *http.Request, commandID string) {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumInternalRequestBody)
	var input internalCancelCommandRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil || len(commandID) > 256 {
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", false)
		return
	}
	claims, ok := h.authorizeSubmittedDeviceAction(writer, request, commandID, input.TenantID, input.SiteID, input.DeviceID, input.PrincipalID)
	if !ok {
		return
	}
	cancelled, err := h.config.Authority.Cancel(request.Context(), commandmodel.CancelRequest{
		TenantID: input.TenantID, CommandID: commandID, Reason: input.Reason, Authorization: commandauth.Snapshot(claims),
	})
	if err != nil {
		writeAuthorityError(writer, err)
		return
	}
	writeCommandJSON(writer, http.StatusOK, commandView(cancelled))
}

func (h *HTTPHandler) cancelPendingCommands(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumInternalRequestBody)
	var input internalCancelPendingRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil ||
		input.TenantID == "" || input.SiteID == "" || input.PrincipalID == "" {
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", false)
		return
	}
	claims, err := identitycontext.VerifyDelegation(h.config.GatewayDelegationPublicKey, request.Header.Get(commandControlContextHeader))
	if err != nil || claims.TenantID != input.TenantID || len(claims.Scopes) != 2 ||
		identitycontext.ValidateDelegationAnyScope(claims, h.now().UTC(), h.config.GatewaySPIFFE, h.config.GatewayReadAudience,
			"command:cancel-pending", []string{"tenant:" + input.TenantID, "site:" + input.SiteID}) != nil ||
		!containsCommandScope(claims.Scopes, "tenant:"+input.TenantID) || !containsCommandScope(claims.Scopes, "site:"+input.SiteID) {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_CONTROL_CONTEXT_REJECTED", false)
		return
	}
	cancelledIDs, err := h.config.Authority.CancelPending(request.Context(), commandmodel.CancelPendingRequest{
		TenantID:             input.TenantID,
		SiteID:               input.SiteID,
		DeviceIDs:            input.DeviceIDs,
		PrincipalID:          input.PrincipalID,
		Reason:               input.Reason,
		CausationID:          input.CausationID,
		AbortActiveSequences: input.AbortActiveSequences,
	})
	if err != nil {
		writeAuthorityError(writer, err)
		return
	}
	writeCommandJSON(writer, http.StatusOK, map[string]any{"cancelledCommandIds": cancelledIDs})
}

func (h *HTTPHandler) authorizeSubmittedDeviceAction(writer http.ResponseWriter, request *http.Request, commandID, tenantID, siteID, deviceID, principalID string) (commandauth.GrantClaims, bool) {
	claims, err := commandauth.VerifyGrant(h.config.CommandGrantPublicKey, request.Header.Get(commandGrantHeader))
	if err != nil {
		writeCommandProblem(writer, http.StatusUnauthorized, "COMMAND_GRANT_INVALID", false)
		return commandauth.GrantClaims{}, false
	}
	intent, err := h.config.Authority.Get(request.Context(), tenantID, commandID)
	if err != nil {
		writeAuthorityError(writer, err)
		return commandauth.GrantClaims{}, false
	}
	// The grant is for the Capability's current revision, as the Gateway requests it.
	currentRevision, _, _, supported := commandCapabilityProfile(intent.Capability)
	if intent.SiteID != siteID || intent.DeviceID != deviceID || !supported {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_GRANT_REJECTED", false)
		return commandauth.GrantClaims{}, false
	}
	validation := commandauth.Validation{
		Now: h.now().UTC(), Issuer: h.config.CommandGrantIssuer, Presenter: h.config.GatewaySPIFFE,
		Audience: h.config.CommandGrantAudience, Purpose: commandmodel.AuthorizationCommandSubmit,
		PrincipalID: principalID, TenantID: tenantID, SiteID: siteID, DeviceID: deviceID,
		Capability: intent.Capability, CapabilityRevision: currentRevision,
		Risk: intent.Risk, UseChecker: h.config.CommandGrantUseChecker,
	}
	if commandauth.ValidateGrant(claims, validation) != nil {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_GRANT_REJECTED", false)
		return commandauth.GrantClaims{}, false
	}
	return claims, true
}

func (h *HTTPHandler) getCommand(writer http.ResponseWriter, request *http.Request, commandID string) {
	tenantID := strings.TrimSpace(request.Header.Get(tenantHeader))
	if tenantID == "" || commandID == "" || len(commandID) > 256 {
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_READ_CONTEXT_INVALID", false)
		return
	}
	claims, err := identitycontext.VerifyDelegation(h.config.GatewayDelegationPublicKey, request.Header.Get(commandReadContextHeader))
	if err != nil || claims.TenantID != tenantID || len(claims.Scopes) != 2 ||
		identitycontext.ValidateDelegationAnyScope(claims, h.now().UTC(), h.config.GatewaySPIFFE, h.config.GatewayReadAudience,
			"command:read", []string{"tenant:" + tenantID, "command:" + commandID}) != nil ||
		!containsCommandScope(claims.Scopes, "tenant:"+tenantID) || !containsCommandScope(claims.Scopes, "command:"+commandID) {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_READ_CONTEXT_REJECTED", false)
		return
	}
	intent, err := h.config.Authority.Get(request.Context(), tenantID, commandID)
	if err != nil {
		writeAuthorityError(writer, err)
		return
	}
	writeCommandJSON(writer, http.StatusOK, commandView(intent))
}

// MaximumListedCommands bounds one page of a Site's Commands, newest first.
const MaximumListedCommands = 100

// listCommands returns a Site's newest Commands to a Gateway read context scoped to that Site.
func (h *HTTPHandler) listCommands(writer http.ResponseWriter, request *http.Request) {
	tenantID := strings.TrimSpace(request.Header.Get(tenantHeader))
	siteID := request.URL.Query().Get("siteId")
	limit, err := strconv.Atoi(request.URL.Query().Get("limit"))
	if tenantID == "" || siteID == "" || len(siteID) > 256 || err != nil || limit < 1 || limit > MaximumListedCommands {
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_LIST_REQUEST_INVALID", false)
		return
	}
	claims, err := identitycontext.VerifyDelegation(h.config.GatewayDelegationPublicKey, request.Header.Get(commandReadContextHeader))
	if err != nil || claims.TenantID != tenantID || len(claims.Scopes) != 2 ||
		identitycontext.ValidateDelegationAnyScope(claims, h.now().UTC(), h.config.GatewaySPIFFE, h.config.GatewayReadAudience,
			"command:read", []string{"tenant:" + tenantID, "site:" + siteID}) != nil ||
		!containsCommandScope(claims.Scopes, "tenant:"+tenantID) || !containsCommandScope(claims.Scopes, "site:"+siteID) {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_READ_CONTEXT_REJECTED", false)
		return
	}
	intents, err := h.config.Authority.List(request.Context(), tenantID, siteID, limit)
	if err != nil {
		writeAuthorityError(writer, err)
		return
	}
	views := make([]CommandView, 0, len(intents))
	for _, intent := range intents {
		views = append(views, commandView(intent))
	}
	writeCommandJSON(writer, http.StatusOK, struct {
		Data []CommandView `json:"data"`
	}{Data: views})
}

func validInternalApproval(commandID string, input internalApproveCommandRequest) bool {
	if strings.TrimSpace(commandID) == "" || len(commandID) > 256 {
		return false
	}
	for _, value := range []string{input.TenantID, input.SiteID, input.DeviceID, input.PrincipalID} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return false
		}
	}
	return true
}

func validInternalCreate(input internalCreateCommandRequest) bool {
	for _, value := range []string{input.TenantID, input.SiteID, input.DeviceID, input.PointID, input.PrincipalID, input.IdempotencyKey, input.VerificationPointKey} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return false
		}
	}
	profile, supported := commandmodel.CapabilityProfileFor(input.Capability)
	if !supported || !commandmodel.ParametersValid(input.Capability, input.Parameters) {
		return false
	}
	mode := input.PreconditionMode
	if mode == "" {
		mode = commandmodel.PreconditionRequireCurrentState
	}
	switch mode {
	case commandmodel.PreconditionEmergencyBypass:
		return commandmodel.AllowsEmergencyPreconditionBypass(
			input.Capability,
			input.PriorityTier,
			input.CommandSource,
			input.IdempotencyKey,
			input.BypassReason,
		)
	case commandmodel.PreconditionRequireCurrentState:
		if input.CurrentState.BusinessRevision == 0 || input.CurrentState.ObservedAt.IsZero() {
			return false
		}
	default:
		return false
	}
	if profile.ParameterKey == "" {
		return true
	}

	return input.CurrentState.CurrentValue != nil
}

func commandView(intent commandmodel.CommandIntent) CommandView {
	transitions := make([]CommandTransitionView, 0, len(intent.Transitions))
	for _, transition := range intent.Transitions {
		var from *commandmodel.IntentStatus
		if transition.From != "" {
			value := transition.From
			from = &value
		}
		transitions = append(transitions, CommandTransitionView{
			FromStatus: from, ToStatus: transition.To, Reason: transition.Reason,
			ActorType: actorType(transition.Actor, intent.PrincipalID), OccurredAt: transition.At.UTC(), Version: transition.Version,
		})
	}
	return CommandView{
		SchemaVersion: 1, CommandID: intent.ID, TenantID: intent.TenantID,
		SiteID: intent.SiteID, DeviceID: intent.DeviceID, PointID: intent.PointID,
		Capability: intent.Capability, CapabilityRevision: intent.CapabilityRevision,
		Status: intent.Status, Risk: intent.Risk, ApprovalPolicy: intent.ApprovalPolicy,
		PriorityTier: intent.PriorityTier, CommandSource: intent.CommandSource,
		ApprovalCount: len(intent.Approvals), RequiredApprovalCount: requiredApprovalCount(intent.ApprovalPolicy),
		Parameters: cloneParameters(intent.Parameters), DeviceCommandSequence: intent.DeviceCommandSequence,
		Version: intent.Version, SnapshotRevision: intent.SnapshotRevision, Transitions: transitions,
		CreatedAt: intent.CreatedAt.UTC(), UpdatedAt: intent.UpdatedAt.UTC(),
	}
}

func writeAuthorityError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCommandNotFound):
		writeCommandProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", false)
	case errors.Is(err, ErrAuthorizationDenied):
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_AUTHORIZATION_DENIED", false)
	case errors.Is(err, ErrReconciliationInvalid):
		writeCommandProblem(writer, http.StatusConflict, "COMMAND_RECONCILIATION_INVALID", false)
	case errors.Is(err, ErrCancelInvalid):
		writeCommandProblem(writer, http.StatusConflict, "COMMAND_CANCEL_INVALID", false)
	case errors.Is(err, ErrApprovalInvalid), errors.Is(err, ErrApprovalRequired):
		writeCommandProblem(writer, http.StatusConflict, "COMMAND_APPROVAL_INVALID", false)
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrCapabilityDenied), errors.Is(err, ErrCurrentStateUnsafe):
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", false)
	case errors.Is(err, ErrIdempotencyConflict):
		writeCommandProblem(writer, http.StatusConflict, "COMMAND_IDEMPOTENCY_CONFLICT", false)
	case errors.Is(err, ErrDevicePriorityLocked):
		writeCommandProblem(writer, http.StatusConflict, "DEVICE_PRIORITY_LOCKED", false)
	default:
		writeCommandProblem(writer, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", true)
	}
}

func writeCommandProblem(writer http.ResponseWriter, status int, code string, retryable bool) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(commandProblem{
		Type:  "urn:hvac:problem:" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		Title: http.StatusText(status), Status: status, Code: code, Retryable: retryable,
	})
}

func writeCommandJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
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

func containsCommandScope(scopes []string, expected string) bool {
	for _, scope := range scopes {
		if scope == expected {
			return true
		}
	}
	return false
}

func traceIDFromHTTPRequest(request *http.Request) string {
	if request == nil {
		return ""
	}
	if traceID := commandmodel.NormalizeTraceID(observability.TraceID(request.Context())); traceID != "" {
		return traceID
	}
	spanCtx, ok := observability.ParseTraceparent(request.Header.Get("traceparent"), "")
	if !ok {
		return ""
	}
	return commandmodel.NormalizeTraceID(spanCtx.TraceID)
}

func commandItemObservabilityRoute(remainder string) string {
	switch {
	case strings.HasSuffix(remainder, "/approve"):
		commandID := strings.TrimSuffix(remainder, "/approve")
		if commandID != "" && !strings.Contains(commandID, "/") {
			return "commands.approve"
		}
	case strings.HasSuffix(remainder, "/reconcile"):
		commandID := strings.TrimSuffix(remainder, "/reconcile")
		if commandID != "" && !strings.Contains(commandID, "/") {
			return "commands.reconcile"
		}
	case strings.HasSuffix(remainder, "/cancel"):
		commandID := strings.TrimSuffix(remainder, "/cancel")
		if commandID != "" && !strings.Contains(commandID, "/") {
			return "commands.cancel"
		}
	default:
		if !strings.Contains(remainder, "/") {
			return "commands.get"
		}
	}
	return "unknown"
}

// ObservabilityRoute maps Command Service internal HTTP endpoints onto the bounded route label catalog.
func ObservabilityRoute(request *http.Request) string {
	if request == nil {
		return "unknown"
	}
	path := request.URL.Path
	switch path {
	case InternalCommandsPath, InternalOperatorCommandsPath:
		if request.Method == http.MethodGet {
			return "commands.get"
		}
		if request.Method == http.MethodPost {
			return "commands.create"
		}
		return "unknown"
	case InternalCommandsCancelBatch, InternalCommandCancelRetiredPath:
		return "commands.cancel"
	case InternalDispatchClaimPath:
		return "dispatch.claim"
	case InternalDispatchClaimAnyPath:
		return "dispatch.claim-any"
	case InternalDispatchResolvePath:
		return "dispatch.resolve"
	case InternalVerificationClaimPath:
		return "verification.claim"
	case InternalVerificationClaimAnyPath:
		return "verification.claim-any"
	case InternalVerificationResolvePath:
		return "verification.resolve"
	case InternalConnectorPreparePath:
		return "connector.prepare"
	case InternalConnectorCompletePath:
		return "connector.complete"
	case InternalCapabilityProfilesSyncPath:
		return "capability.sync"
	}
	if remainder, ok := strings.CutPrefix(path, InternalCommandsPath+"/"); ok && remainder != "" {
		return commandItemObservabilityRoute(remainder)
	}
	if remainder, ok := strings.CutPrefix(path, InternalOperatorCommandsPath+"/"); ok && remainder != "" {
		return commandItemObservabilityRoute(remainder)
	}
	if remainder, ok := strings.CutPrefix(path, InternalOperatorPrefix+"/sites/"); ok {
		segments := strings.Split(remainder, "/")
		if len(segments) == 2 && segments[0] != "" {
			switch segments[1] {
			case "control-state":
				return "control.state"
			case "control-mode":
				return "control.mode"
			case "control-shutdown":
				return "control.shutdown"
			}
		} else if len(segments) == 4 && segments[0] != "" && segments[1] == "devices" && segments[2] != "" && segments[3] == "priority-lock" {
			return "control.lock"
		} else if len(segments) == 3 && segments[0] != "" && segments[1] == "control-alarms" && segments[2] == "dead-letters" {
			return "control.state"
		} else if len(segments) == 5 && segments[0] != "" && segments[1] == "control-alarms" && segments[2] == "dead-letters" && segments[3] != "" && segments[4] == "replay" {
			return "control.state"
		}
	}
	if remainder, ok := strings.CutPrefix(path, InternalOperatorPrefix+"/assets/"); ok {
		segments := strings.Split(remainder, "/")
		if len(segments) == 2 && segments[0] != "" && segments[1] == "control-lock" {
			return "control.lock"
		}
	}
	return "unknown"
}
