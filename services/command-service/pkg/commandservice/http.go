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
	actingOrganizationHeader   = "X-Acting-Organization-ID"
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
	OrganizationID string                  `json:"organizationId"`
	SiteID         string                  `json:"siteId"`
	DeviceID       string                  `json:"deviceId"`
	PrincipalID    string                  `json:"principalId"`
	IdempotencyKey string                  `json:"idempotencyKey"`
	Capability     commandmodel.Capability `json:"capability"`
	SetpointC      float64                 `json:"setpointC"`
	CurrentState   internalCurrentState    `json:"currentState"`
}

type internalCurrentState struct {
	EvaluationAvailability string    `json:"evaluationAvailability"`
	Presence               string    `json:"presence"`
	Readiness              string    `json:"readiness"`
	Quality                string    `json:"quality"`
	BusinessRevision       uint64    `json:"businessRevision"`
	CurrentTemperatureC    float64   `json:"currentTemperatureC"`
	ObservedAt             time.Time `json:"observedAt"`
}

type internalApproveCommandRequest struct {
	OrganizationID string `json:"organizationId"`
	SiteID         string `json:"siteId"`
	DeviceID       string `json:"deviceId"`
	PrincipalID    string `json:"principalId"`
	ApproverRole   string `json:"approverRole"`
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
	SchemaVersion         int                         `json:"schemaVersion"`
	CommandID             string                      `json:"commandId"`
	OrganizationID        string                      `json:"organizationId"`
	SiteID                string                      `json:"siteId"`
	DeviceID              string                      `json:"deviceId"`
	Capability            commandmodel.Capability     `json:"capability"`
	CapabilityRevision    string                      `json:"capabilityRevision"`
	Status                commandmodel.IntentStatus   `json:"status"`
	Risk                  commandmodel.RiskLevel      `json:"risk"`
	ApprovalPolicy        commandmodel.ApprovalPolicy `json:"approvalPolicy"`
	ApprovalCount         int                         `json:"approvalCount"`
	RequiredApprovalCount int                         `json:"requiredApprovalCount"`
	SetpointC             float64                     `json:"setpointC"`
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
		if strings.HasSuffix(raw, ":approve") && !strings.Contains(strings.TrimSuffix(raw, ":approve"), "/") {
			if request.Method != http.MethodPost {
				writer.Header().Set("Allow", http.MethodPost)
				writeCommandProblem(writer, http.StatusMethodNotAllowed, "COMMAND_METHOD_NOT_ALLOWED", false)
				return
			}
			h.approveCommand(writer, request, strings.TrimSuffix(raw, ":approve"))
			return
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
	validation := commandauth.Validation{
		Now: h.now().UTC(), Issuer: h.config.CommandGrantIssuer, Presenter: h.config.GatewaySPIFFE,
		Audience: h.config.CommandGrantAudience, Purpose: commandmodel.AuthorizationCommandSubmit,
		PrincipalID: input.PrincipalID, OrganizationID: input.OrganizationID, SiteID: input.SiteID, DeviceID: input.DeviceID,
		Capability: input.Capability, CapabilityRevision: setpointCapabilityRevision,
		Risk: commandmodel.RiskLow, UseChecker: h.config.CommandGrantUseChecker,
	}
	if commandauth.ValidateGrant(claims, validation) != nil {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_GRANT_REJECTED", false)
		return
	}
	result, err := h.config.Authority.Submit(request.Context(), commandmodel.SubmitRequest{
		OrganizationID: input.OrganizationID, SiteID: input.SiteID, DeviceID: input.DeviceID,
		PrincipalID: input.PrincipalID, IdempotencyKey: input.IdempotencyKey,
		Capability: input.Capability, SetpointC: input.SetpointC,
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: input.CurrentState.EvaluationAvailability,
			Presence:               input.CurrentState.Presence, Readiness: input.CurrentState.Readiness,
			Quality: input.CurrentState.Quality, BusinessRevision: input.CurrentState.BusinessRevision,
			CurrentTemperatureC: input.CurrentState.CurrentTemperatureC, ObservedAt: input.CurrentState.ObservedAt,
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
	intent, err := h.config.Authority.Get(request.Context(), input.OrganizationID, commandID)
	if err != nil {
		writeAuthorityError(writer, err)
		return
	}
	if intent.SiteID != input.SiteID || intent.DeviceID != input.DeviceID || intent.Capability != commandmodel.CapabilitySetTemperatureSetpoint {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_GRANT_REJECTED", false)
		return
	}
	validation := commandauth.Validation{
		Now: h.now().UTC(), Issuer: h.config.CommandGrantIssuer, Presenter: h.config.GatewaySPIFFE,
		Audience: h.config.CommandGrantAudience, Purpose: commandmodel.AuthorizationCommandApprove,
		PrincipalID: input.PrincipalID, OrganizationID: input.OrganizationID, SiteID: input.SiteID, DeviceID: input.DeviceID,
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
		OrganizationID: input.OrganizationID,
		CommandID:      commandID,
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
	organizationID := strings.TrimSpace(request.Header.Get(actingOrganizationHeader))
	if organizationID == "" || commandID == "" || len(commandID) > 256 {
		writeCommandProblem(writer, http.StatusBadRequest, "COMMAND_READ_CONTEXT_INVALID", false)
		return
	}
	claims, err := identitycontext.VerifyDelegation(h.config.GatewayDelegationPublicKey, request.Header.Get(commandReadContextHeader))
	if err != nil || claims.ActingOrganizationID != organizationID || len(claims.Scopes) != 2 ||
		identitycontext.ValidateDelegationAnyScope(claims, h.now().UTC(), h.config.GatewaySPIFFE, h.config.GatewayReadAudience,
			"command:read", []string{"organization:" + organizationID, "command:" + commandID}) != nil ||
		!containsCommandScope(claims.Scopes, "organization:"+organizationID) || !containsCommandScope(claims.Scopes, "command:"+commandID) {
		writeCommandProblem(writer, http.StatusForbidden, "COMMAND_READ_CONTEXT_REJECTED", false)
		return
	}
	intent, err := h.config.Authority.Get(request.Context(), organizationID, commandID)
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
	for _, value := range []string{input.OrganizationID, input.SiteID, input.DeviceID, input.PrincipalID, input.ApproverRole} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return false
		}
	}
	return true
}

func validInternalCreate(input internalCreateCommandRequest) bool {
	for _, value := range []string{input.OrganizationID, input.SiteID, input.DeviceID, input.PrincipalID, input.IdempotencyKey} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return false
		}
	}
	return input.Capability == commandmodel.CapabilitySetTemperatureSetpoint && input.CurrentState.BusinessRevision > 0 && !input.CurrentState.ObservedAt.IsZero()
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
		SchemaVersion: 1, CommandID: intent.ID, OrganizationID: intent.OrganizationID,
		SiteID: intent.SiteID, DeviceID: intent.DeviceID,
		Capability: intent.Capability, CapabilityRevision: intent.CapabilityRevision,
		Status: intent.Status, Risk: intent.Risk, ApprovalPolicy: intent.ApprovalPolicy,
		ApprovalCount: len(intent.Approvals), RequiredApprovalCount: requiredApprovalCount(intent.ApprovalPolicy),
		SetpointC: intent.SetpointC, DeviceCommandSequence: intent.DeviceCommandSequence,
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
