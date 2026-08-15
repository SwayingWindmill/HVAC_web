package commandservice

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	InternalCommandsPath       = "/internal/v1/commands"
	commandGrantHeader         = "X-Command-Grant"
	commandReadContextHeader   = "X-Command-Read-Context"
	tenantHeader               = "X-Tenant-ID"
	maximumInternalRequestBody = int64(64 << 10)
)

type HTTPAuthority interface {
	Submit(context.Context, commandmodel.SubmitRequest) (SubmitResult, error)
	Get(context.Context, string, string) (commandmodel.CommandIntent, error)
	Approve(context.Context, commandmodel.ApproveRequest) (commandmodel.CommandIntent, error)
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
	TenantID       string                         `json:"tenantId"`
	SiteID         string                         `json:"siteId"`
	DeviceID       string                         `json:"deviceId"`
	PointID        string                         `json:"pointId"`
	PrincipalID    string                         `json:"principalId"`
	IdempotencyKey string                         `json:"idempotencyKey"`
	Capability           commandmodel.Capability        `json:"capability"`
	Parameters           commandmodel.CommandParameters `json:"parameters"`
	VerificationPointKey string                         `json:"verificationPointKey"`
	CurrentState         internalCurrentState           `json:"currentState"`
}

type internalCurrentState struct {
	EvaluationAvailability string     `json:"evaluationAvailability"`
	Presence               string     `json:"presence"`
	Readiness              string     `json:"readiness"`
	Quality                string     `json:"quality"`
	BusinessRevision       uint64     `json:"businessRevision"`
	CurrentValue           *float64   `json:"currentValue,omitempty"`
	ObservedAt             time.Time  `json:"observedAt"`
}

type internalApproveCommandRequest struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
	DeviceID       string `json:"deviceId"`
	PrincipalID    string `json:"principalId"`
	ApproverRole   string `json:"approverRole"`
}

type ControlStatus string

const (
	ControlCreated         ControlStatus = "CREATED"
	ControlValidating      ControlStatus = "VALIDATING"
	ControlApprovalPending ControlStatus = "APPROVAL_PENDING"
	ControlApproved        ControlStatus = "APPROVED"
	ControlSent            ControlStatus = "SENT"
	ControlAcked           ControlStatus = "ACKED"
	ControlExecuting       ControlStatus = "EXECUTING"
	ControlVerified        ControlStatus = "VERIFIED"
	ControlFailed          ControlStatus = "FAILED"
	ControlRejected        ControlStatus = "REJECTED"
	ControlExpired         ControlStatus = "EXPIRED"
	ControlUnknown         ControlStatus = "UNKNOWN"
	ControlCancelled       ControlStatus = "CANCELLED"
)

type CommandTransitionView struct {
	FromStatus *ControlStatus `json:"fromStatus,omitempty"`
	ToStatus   ControlStatus  `json:"toStatus"`
	Reason     string                     `json:"reason"`
	ActorType  string                     `json:"actorType"`
	OccurredAt time.Time                  `json:"occurredAt"`
	Version    uint64                     `json:"version"`
}

type CommandView struct {
	SchemaVersion         int                         `json:"schemaVersion"`
	CommandID             string                      `json:"commandId"`
	TenantID              string                      `json:"tenantId"`
	SiteID                string                      `json:"siteId"`
	DeviceID              string                      `json:"deviceId"`
	PointID               string                      `json:"pointId"`
	Capability            commandmodel.Capability     `json:"capability"`
	CapabilityRevision    string                      `json:"capabilityRevision"`
	Status                ControlStatus               `json:"status"`
	Risk                  commandmodel.RiskLevel      `json:"risk"`
	ApprovalPolicy        commandmodel.ApprovalPolicy `json:"approvalPolicy"`
	ApprovalCount         int                         `json:"approvalCount"`
	RequiredApprovalCount int                         `json:"requiredApprovalCount"`
	Parameters            commandmodel.CommandParameters `json:"parameters"`
	DeviceCommandSequence uint64                      `json:"deviceCommandSequence"`
	Version               uint64                      `json:"version"`
	SnapshotRevision      uint64                      `json:"snapshotRevision"`
	Transitions           []CommandTransitionView     `json:"transitions"`
	CreatedAt             time.Time                   `json:"createdAt"`
	UpdatedAt             time.Time                   `json:"updatedAt"`
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
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			writeCommandProblem(writer, http.StatusMethodNotAllowed, "COMMAND_METHOD_NOT_ALLOWED", false)
			return
		}
		h.createCommand(writer, request)
		return
	}
	prefix := InternalCommandsPath + "/"
	if strings.HasPrefix(request.URL.Path, prefix) {
		raw := strings.TrimPrefix(request.URL.Path, prefix)
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
	result, err := h.config.Authority.Submit(request.Context(), commandmodel.SubmitRequest{
		TenantID: input.TenantID, SiteID: input.SiteID, DeviceID: input.DeviceID, PointID: input.PointID,
		PrincipalID: input.PrincipalID, IdempotencyKey: input.IdempotencyKey,
		Capability: input.Capability, Parameters: cloneParameters(input.Parameters), VerificationPointKey: input.VerificationPointKey,
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
			ApprovalID: approvalID, ApproverID: input.PrincipalID, ApproverRole: input.ApproverRole,
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

func validInternalApproval(commandID string, input internalApproveCommandRequest) bool {
	if strings.TrimSpace(commandID) == "" || len(commandID) > 256 {
		return false
	}
	for _, value := range []string{input.TenantID, input.SiteID, input.DeviceID, input.PrincipalID, input.ApproverRole} {
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
	if !supported || input.CurrentState.BusinessRevision == 0 || input.CurrentState.ObservedAt.IsZero() {
		return false
	}
	if profile.ParameterKey == "" {
		return len(input.Parameters) == 0
	}
	value, ok := commandmodel.ParameterValue(input.Capability, input.Parameters)
	return ok && value >= profile.Minimum && value <= profile.Maximum && input.CurrentState.CurrentValue != nil
}

func commandView(intent commandmodel.CommandIntent) CommandView {
	transitions := make([]CommandTransitionView, 0, len(intent.Transitions))
	for _, transition := range intent.Transitions {
		var from *ControlStatus
		if transition.From != "" {
			value := controlStatus(transition.From, "")
			from = &value
		}
		transitions = append(transitions, CommandTransitionView{
			FromStatus: from, ToStatus: controlStatus(transition.To, transition.Reason), Reason: transition.Reason,
			ActorType: actorType(transition.Actor, intent.PrincipalID), OccurredAt: transition.At.UTC(), Version: transition.Version,
		})
	}
	lastReason := ""
	if len(intent.Transitions) > 0 {
		lastReason = intent.Transitions[len(intent.Transitions)-1].Reason
	}
	return CommandView{
		SchemaVersion: 1, CommandID: intent.ID, TenantID: intent.TenantID,
		SiteID: intent.SiteID, DeviceID: intent.DeviceID, PointID: intent.PointID,
		Capability: intent.Capability, CapabilityRevision: intent.CapabilityRevision,
		Status: controlStatus(intent.Status, lastReason), Risk: intent.Risk, ApprovalPolicy: intent.ApprovalPolicy,
		ApprovalCount: len(intent.Approvals), RequiredApprovalCount: requiredApprovalCount(intent.ApprovalPolicy),
		Parameters: cloneParameters(intent.Parameters), DeviceCommandSequence: intent.DeviceCommandSequence,
		Version: intent.Version, SnapshotRevision: intent.SnapshotRevision, Transitions: transitions,
		CreatedAt: intent.CreatedAt.UTC(), UpdatedAt: intent.UpdatedAt.UTC(),
	}
}

func controlStatus(status commandmodel.IntentStatus, reason string) ControlStatus {
	switch status {
	case commandmodel.IntentSubmitted:
		return ControlCreated
	case commandmodel.IntentValidating:
		return ControlValidating
	case commandmodel.IntentAwaitingApproval:
		return ControlApprovalPending
	case commandmodel.IntentApproved, commandmodel.IntentQueued:
		return ControlApproved
	case commandmodel.IntentDispatching:
		if reason == "PROVIDER_ACKNOWLEDGED_AWAITING_REPORTED_STATE" {
			return ControlAcked
		}
		return ControlSent
	case commandmodel.IntentSucceeded:
		return ControlVerified
	case commandmodel.IntentFailed:
		return ControlFailed
	case commandmodel.IntentRejected:
		return ControlRejected
	case commandmodel.IntentExpired:
		return ControlExpired
	case commandmodel.IntentOutcomeUnknown:
		return ControlUnknown
	case commandmodel.IntentCancelled:
		return ControlCancelled
	default:
		return ControlUnknown
	}
}

func writeAuthorityError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCommandNotFound):
		writeCommandProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", false)
	case errors.Is(err, ErrAuthorizationDenied):
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_AUTHORIZATION_DENIED", false)
	case errors.Is(err, ErrApprovalInvalid), errors.Is(err, ErrApprovalRequired):
		writeCommandProblem(writer, http.StatusConflict, "COMMAND_APPROVAL_INVALID", false)
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrCapabilityDenied), errors.Is(err, ErrCurrentStateUnsafe):
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", false)
	case errors.Is(err, ErrIdempotencyConflict):
		writeCommandProblem(writer, http.StatusConflict, "COMMAND_IDEMPOTENCY_CONFLICT", false)
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
