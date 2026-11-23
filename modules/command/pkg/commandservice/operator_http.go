package commandservice

import (
	"bytes"
	"crypto"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	InternalOperatorPrefix       = "/internal/v1/operator"
	InternalOperatorCommandsPath = InternalOperatorPrefix + "/commands"
)

type OperatorHTTPConfig struct {
	Authority                  *ControlAuthority
	GatewayDelegationPublicKey crypto.PublicKey
	GatewaySPIFFE              string
	GatewayAudience            string
	Now                        func() time.Time
}

type OperatorHTTPHandler struct {
	config OperatorHTTPConfig
	now    func() time.Time
}

func NewOperatorHTTPHandler(config OperatorHTTPConfig) (*OperatorHTTPHandler, error) {
	if config.Authority == nil || config.GatewayDelegationPublicKey == nil {
		return nil, errors.New("operator command HTTP configuration requires ControlAuthority and GatewayDelegationPublicKey")
	}
	if strings.TrimSpace(config.GatewaySPIFFE) == "" {
		config.GatewaySPIFFE = "spiffe://hvac.local/platform-gateway"
	}
	if strings.TrimSpace(config.GatewayAudience) == "" {
		config.GatewayAudience = "command-service"
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &OperatorHTTPHandler{config: config, now: now}, nil
}

func (h *OperatorHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	operator, ctrlErr := h.verifyOperatorContext(request)
	if ctrlErr != nil {
		writeControlProblem(writer, request, ctrlErr)
		return
	}

	path := request.URL.Path
	if path == InternalOperatorCommandsPath {
		switch request.Method {
		case http.MethodPost:
			h.handleCreateCommand(writer, request, operator)
		case http.MethodGet:
			h.handleListCommands(writer, request, operator)
		default:
			writer.Header().Set("Allow", "GET, POST")
			writeControlProblem(writer, request, &ControlError{
				HTTPStatus: http.StatusMethodNotAllowed, Code: "COMMAND_METHOD_NOT_ALLOWED",
				Title: "Method not allowed", Detail: "Unsupported HTTP method for operator commands.",
			})
		}
		return
	}

	if remainder, ok := strings.CutPrefix(path, InternalOperatorCommandsPath+"/"); ok && remainder != "" {
		h.dispatchCommandItem(writer, request, operator, remainder)
		return
	}

	if remainder, ok := strings.CutPrefix(path, InternalOperatorPrefix+"/sites/"); ok {
		segments := strings.Split(remainder, "/")
		if len(segments) == 2 {
			siteID, err := url.PathUnescape(segments[0])
			if err == nil && siteID != "" {
				switch segments[1] {
				case "control-state":
					h.dispatchMethod(writer, request, http.MethodGet, func() { h.handleGetSiteControlState(writer, request, operator, siteID) })
					return
				case "control-mode":
					h.dispatchMethod(writer, request, http.MethodPut, func() { h.handlePutSiteControlMode(writer, request, operator, siteID) })
					return
				case "control-shutdown":
					h.dispatchMethod(writer, request, http.MethodPost, func() { h.handlePostSiteControlShutdown(writer, request, operator, siteID) })
					return
				case "command-batches":
					switch request.Method {
					case http.MethodPost:
						h.handleCreateCommandBatch(writer, request, operator, siteID)
					case http.MethodGet:
						h.handleListCommandBatches(writer, request, operator, siteID)
					default:
						writer.Header().Set("Allow", "GET, POST")
						writeControlProblem(writer, request, &ControlError{
							HTTPStatus: http.StatusMethodNotAllowed, Code: "COMMAND_METHOD_NOT_ALLOWED",
							Title: "Method not allowed", Detail: "Unsupported HTTP method for operator command batches.",
						})
					}
					return
				}
			}
		} else if len(segments) == 3 && segments[1] == "command-batches" {
			siteID, siteErr := url.PathUnescape(segments[0])
			batchID, batchErr := url.PathUnescape(segments[2])
			if siteErr == nil && batchErr == nil && siteID != "" && batchID != "" {
				h.dispatchMethod(writer, request, http.MethodGet, func() { h.handleGetCommandBatch(writer, request, operator, siteID, batchID) })
				return
			}
		} else if len(segments) == 4 && segments[1] == "command-batches" && segments[3] == "cancel" {
			siteID, siteErr := url.PathUnescape(segments[0])
			batchID, batchErr := url.PathUnescape(segments[2])
			if siteErr == nil && batchErr == nil && siteID != "" && batchID != "" {
				h.dispatchMethod(writer, request, http.MethodPost, func() { h.handleCancelCommandBatch(writer, request, operator, siteID, batchID) })
				return
			}
		} else if len(segments) == 4 && segments[1] == "devices" && segments[3] == "priority-lock" {
			siteID, siteErr := url.PathUnescape(segments[0])
			deviceID, devErr := url.PathUnescape(segments[2])
			if siteErr == nil && devErr == nil && siteID != "" && deviceID != "" {
				switch request.Method {
				case http.MethodPut:
					h.handlePutDevicePriorityLock(writer, request, operator, siteID, deviceID)
				case http.MethodDelete:
					h.handleDeleteDevicePriorityLock(writer, request, operator, siteID, deviceID)
				default:
					writer.Header().Set("Allow", "PUT, DELETE")
					writeControlProblem(writer, request, &ControlError{
						HTTPStatus: http.StatusMethodNotAllowed, Code: "COMMAND_METHOD_NOT_ALLOWED",
						Title: "Method not allowed", Detail: "Unsupported HTTP method for this route.",
					})
				}
				return
			}
		} else if len(segments) == 3 && segments[1] == "control-alarms" && segments[2] == "dead-letters" {
			siteID, err := url.PathUnescape(segments[0])
			if err == nil && siteID != "" {
				h.dispatchMethod(writer, request, http.MethodGet, func() { h.handleListControlAlarmDeadLetters(writer, request, operator, siteID) })
				return
			}
		} else if len(segments) == 5 && segments[1] == "control-alarms" && segments[2] == "dead-letters" && segments[4] == "replay" {
			siteID, siteErr := url.PathUnescape(segments[0])
			outboxID, outboxErr := url.PathUnescape(segments[3])
			if siteErr == nil && outboxErr == nil && siteID != "" && outboxID != "" {
				h.dispatchMethod(writer, request, http.MethodPost, func() { h.handleReplayControlAlarmDeadLetter(writer, request, operator, siteID, outboxID) })
				return
			}
		}
	}

	if remainder, ok := strings.CutPrefix(path, InternalOperatorPrefix+"/assets/"); ok {
		segments := strings.Split(remainder, "/")
		if len(segments) == 2 && segments[1] == "control-lock" {
			assetID, err := url.PathUnescape(segments[0])
			if err == nil && assetID != "" {
				h.dispatchMethod(writer, request, http.MethodPut, func() { h.handlePutAssetControlLock(writer, request, operator, assetID) })
				return
			}
		}
	}

	writeControlProblem(writer, request, &ControlError{
		HTTPStatus: http.StatusNotFound, Code: "COMMAND_ROUTE_NOT_FOUND",
		Title: "Route not found", Detail: "The requested operator command route does not exist.",
	})
}

func (h *OperatorHTTPHandler) dispatchMethod(writer http.ResponseWriter, request *http.Request, allowed string, fn func()) {
	if request.Method != allowed {
		writer.Header().Set("Allow", allowed)
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusMethodNotAllowed, Code: "COMMAND_METHOD_NOT_ALLOWED",
			Title: "Method not allowed", Detail: "Unsupported HTTP method for this route.",
		})
		return
	}
	fn()
}

func (h *OperatorHTTPHandler) dispatchCommandItem(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, remainder string) {
	switch {
	case strings.HasSuffix(remainder, "/approve"):
		commandID := strings.TrimSuffix(remainder, "/approve")
		if commandID != "" && !strings.Contains(commandID, "/") {
			h.dispatchMethod(writer, request, http.MethodPost, func() { h.handleApproveCommand(writer, request, operator, commandID) })
			return
		}
	case strings.HasSuffix(remainder, "/reconcile"):
		commandID := strings.TrimSuffix(remainder, "/reconcile")
		if commandID != "" && !strings.Contains(commandID, "/") {
			h.dispatchMethod(writer, request, http.MethodPost, func() { h.handleReconcileCommand(writer, request, operator, commandID) })
			return
		}
	case strings.HasSuffix(remainder, "/cancel"):
		commandID := strings.TrimSuffix(remainder, "/cancel")
		if commandID != "" && !strings.Contains(commandID, "/") {
			h.dispatchMethod(writer, request, http.MethodPost, func() { h.handleCancelCommand(writer, request, operator, commandID) })
			return
		}
	default:
		if !strings.Contains(remainder, "/") {
			h.dispatchMethod(writer, request, http.MethodGet, func() { h.handleGetCommand(writer, request, operator, remainder) })
			return
		}
	}
	writeControlProblem(writer, request, &ControlError{
		HTTPStatus: http.StatusNotFound, Code: "COMMAND_ROUTE_NOT_FOUND",
		Title: "Route not found", Detail: "The requested operator command route does not exist.",
	})
}

func (h *OperatorHTTPHandler) verifyOperatorContext(request *http.Request) (OperatorIdentity, *ControlError) {
	claims, err := identitycontext.VerifyDelegation(h.config.GatewayDelegationPublicKey, request.Header.Get(commandControlContextHeader))
	if err != nil {
		return OperatorIdentity{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "COMMAND_CONTROL_CONTEXT_REJECTED",
			Title: "Control context rejected", Detail: "The Gateway operator control delegation context is invalid.",
		}
	}
	if strings.TrimSpace(claims.TenantID) == "" || strings.TrimSpace(claims.SessionID) == "" ||
		!containsCommandScope(claims.Scopes, "tenant:"+claims.TenantID) || !containsCommandScope(claims.Scopes, "session:"+claims.SessionID) {
		return OperatorIdentity{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "COMMAND_CONTROL_CONTEXT_REJECTED",
			Title: "Control context rejected", Detail: "The operator control context scope is invalid.",
		}
	}
	allowedScopes := []string{"tenant:" + claims.TenantID, "session:" + claims.SessionID, StepUpActionTOTP, StepUpActionSessionMFA}
	if err := identitycontext.ValidateDelegationAnyScope(
		claims, h.now().UTC(), h.config.GatewaySPIFFE, h.config.GatewayAudience,
		OperatorControlContextAction, allowedScopes,
	); err != nil {
		return OperatorIdentity{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "COMMAND_CONTROL_CONTEXT_REJECTED",
			Title: "Control context rejected", Detail: "The operator control context failed delegation validation.",
		}
	}
	if tenant := strings.TrimSpace(request.Header.Get(tenantHeader)); tenant != "" && tenant != claims.TenantID {
		return OperatorIdentity{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "COMMAND_CONTROL_CONTEXT_REJECTED",
			Title: "Control context rejected", Detail: "Operator tenant header conflicts with delegation context.",
		}
	}
	totpVerified := containsCommandScope(claims.Scopes, StepUpActionTOTP)
	sessionMFAVerified := containsCommandScope(claims.Scopes, StepUpActionSessionMFA)
	return OperatorIdentity{
		TenantID:                 claims.TenantID,
		Subject:                  claims.Subject,
		SubjectIssuer:            claims.SubjectIssuer,
		DisplayName:              claims.DisplayName,
		Email:                    claims.Email,
		Roles:                    append([]string(nil), claims.Roles...),
		SessionID:                claims.SessionID,
		TokenID:                  claims.TokenID,
		PolicyRevision:           claims.PolicyRevision,
		IssuedAt:                 time.Unix(claims.IssuedAt, 0).UTC(),
		ExpiresAt:                time.Unix(claims.ExpiresAt, 0).UTC(),
		StepUpTOTPVerified:       totpVerified,
		StepUpSessionMFAVerified: sessionMFAVerified,
	}, nil
}

func (h *OperatorHTTPHandler) handleCreateCommand(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity) {
	var input SubmitOperatorCommandInput
	if !decodeStrictOperatorJSON(writer, request, &input, maximumInternalRequestBody) {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "The Command request payload is invalid.",
		})
		return
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		input.IdempotencyKey = strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	}
	input.TraceID = commandmodel.NormalizeTraceID(input.TraceID)
	if input.TraceID == "" {
		input.TraceID = traceIDFromHTTPRequest(request)
	}
	out, err := h.config.Authority.SubmitOperatorCommand(request.Context(), operator, input)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v1/commands/"+out.View.CommandID)
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusAccepted, out.View)
}

func (h *OperatorHTTPHandler) handleGetCommand(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, commandID string) {
	view, err := h.config.Authority.GetOperatorCommand(request.Context(), operator, commandID)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, view)
}

func (h *OperatorHTTPHandler) handleListCommands(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity) {
	query := request.URL.Query()
	siteID := strings.TrimSpace(query.Get("siteId"))
	limit := 50
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > MaximumListedCommands {
			writeControlProblem(writer, request, &ControlError{
				HTTPStatus: http.StatusBadRequest, Code: "COMMAND_LIST_REQUEST_INVALID",
				Title: "Command list request invalid", Detail: "limit must be between 1 and 100.",
			})
			return
		}
		limit = parsed
	}
	views, err := h.config.Authority.ListOperatorCommands(request.Context(), operator, siteID, limit)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, struct {
		Data []CommandView `json:"data"`
	}{Data: views})
}

func (h *OperatorHTTPHandler) handleApproveCommand(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, commandID string) {
	var body struct{}
	if !decodeStrictOperatorJSON(writer, request, &body, 1024) {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_APPROVAL_REQUEST_INVALID",
			Title: "Command approval invalid", Detail: "The approval request must be an empty JSON object.",
		})
		return
	}
	view, err := h.config.Authority.ApproveOperatorCommand(request.Context(), operator, commandID)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, view)
}

func (h *OperatorHTTPHandler) handleReconcileCommand(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, commandID string) {
	var input struct {
		Outcome commandmodel.ReconciledOutcome `json:"outcome"`
	}
	if !decodeStrictOperatorJSON(writer, request, &input, 4096) {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_RECONCILIATION_REQUEST_INVALID",
			Title: "Command reconciliation invalid", Detail: "The Command reconciliation payload is invalid.",
		})
		return
	}
	view, err := h.config.Authority.ReconcileOperatorCommand(request.Context(), operator, commandID, input.Outcome)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, view)
}

func (h *OperatorHTTPHandler) handleCancelCommand(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, commandID string) {
	raw, err := io.ReadAll(io.LimitReader(request.Body, 4097))
	if err != nil || len(raw) > 4096 {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_CANCEL_REQUEST_INVALID",
			Title: "Command cancel invalid", Detail: "The Command cancel request body is invalid.",
		})
		return
	}
	var input struct {
		Reason string `json:"reason,omitempty"`
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || ensureCommandJSONEOF(decoder) != nil {
			writeControlProblem(writer, request, &ControlError{
				HTTPStatus: http.StatusBadRequest, Code: "COMMAND_CANCEL_REQUEST_INVALID",
				Title: "Command cancel invalid", Detail: "The Command cancel request body is invalid.",
			})
			return
		}
	}
	view, err := h.config.Authority.CancelOperatorCommand(request.Context(), operator, commandID, input.Reason)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, view)
}

func (h *OperatorHTTPHandler) handleGetSiteControlState(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID string) {
	state, err := h.config.Authority.GetSiteControlState(request.Context(), operator, siteID)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, state)
}

func (h *OperatorHTTPHandler) handlePutSiteControlMode(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID string) {
	var input SiteControlModeInput
	if !decodeStrictOperatorJSON(writer, request, &input, 8<<10) {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control exit request invalid", Detail: "The Site control mode request payload is invalid.",
		})
		return
	}
	input.IdempotencyKey = strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	resp, err := h.config.Authority.PutSiteControlMode(request.Context(), operator, siteID, input)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, resp)
}

func (h *OperatorHTTPHandler) handlePutAssetControlLock(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, assetID string) {
	var input AssetControlLockInput
	if !decodeStrictOperatorJSON(writer, request, &input, 8<<10) {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control lock request invalid", Detail: "The Asset control lock request payload is invalid.",
		})
		return
	}
	input.IdempotencyKey = strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	resp, err := h.config.Authority.PutAssetControlLock(request.Context(), operator, assetID, input)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, resp)
}

func (h *OperatorHTTPHandler) handlePostSiteControlShutdown(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID string) {
	var input SiteControlShutdownInput
	if !decodeStrictOperatorJSON(writer, request, &input, 8<<10) {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control exit request invalid", Detail: "The Site control shutdown request payload is invalid.",
		})
		return
	}
	input.IdempotencyKey = strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	resp, err := h.config.Authority.ExecuteSiteControlShutdown(request.Context(), operator, siteID, input)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, resp)
}

func (h *OperatorHTTPHandler) handlePutDevicePriorityLock(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID, deviceID string) {
	var input DevicePriorityLockInput
	if !decodeStrictOperatorJSON(writer, request, &input, 8<<10) {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Device priority lock invalid", Detail: "The Device priority lock request payload is invalid.",
		})
		return
	}
	view, err := h.config.Authority.PutDevicePriorityLock(request.Context(), operator, siteID, deviceID, input)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, view)
}

func (h *OperatorHTTPHandler) handleDeleteDevicePriorityLock(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID, deviceID string) {
	var input struct {
		Reason string `json:"reason"`
	}
	hasBody, ok := decodeOptionalStrictOperatorJSON(request, &input, 8<<10)
	if !ok {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Device priority lock release invalid", Detail: "The Device priority lock release request body is invalid.",
		})
		return
	}
	if !hasBody {
		input.Reason = request.URL.Query().Get("reason")
	}
	resp, err := h.config.Authority.DeleteDevicePriorityLock(request.Context(), operator, siteID, deviceID, input.Reason)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, resp)
}

func (h *OperatorHTTPHandler) handleListControlAlarmDeadLetters(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID string) {
	limit := MaximumControlAlarmDeadLetters
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > MaximumControlAlarmDeadLetters {
			writeControlProblem(writer, request, &ControlError{
				HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
				Title: "Control alarm list invalid", Detail: "limit must be between 1 and 50.",
			})
			return
		}
		limit = parsed
	}
	views, err := h.config.Authority.ListControlAlarmDeadLetters(request.Context(), operator, siteID, limit)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, struct {
		Data []ControlAlarmDeadLetterView `json:"data"`
	}{Data: views})
}

func (h *OperatorHTTPHandler) handleReplayControlAlarmDeadLetter(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID, outboxID string) {
	var empty struct{}
	if _, ok := decodeOptionalStrictOperatorJSON(request, &empty, 8<<10); !ok {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control alarm replay invalid", Detail: "The Control alarm replay request body must be empty or an empty JSON object.",
		})
		return
	}
	resp, err := h.config.Authority.ReplayControlAlarmDeadLetter(request.Context(), operator, siteID, outboxID)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, resp)
}

func (h *OperatorHTTPHandler) handleCreateCommandBatch(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID string) {
	var input SubmitCommandBatchInput
	if !decodeStrictOperatorJSON(writer, request, &input, maximumInternalRequestBody) {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "The Command batch request payload is invalid.",
		})
		return
	}
	input.SiteID = siteID
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		input.IdempotencyKey = strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	}
	input.TraceID = commandmodel.NormalizeTraceID(input.TraceID)
	if input.TraceID == "" {
		input.TraceID = traceIDFromHTTPRequest(request)
	}
	view, err := h.config.Authority.SubmitOperatorCommandBatch(request.Context(), operator, input)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v1/sites/"+url.PathEscape(siteID)+"/command-batches/"+url.PathEscape(view.ID))
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusAccepted, view)
}

func (h *OperatorHTTPHandler) handleListCommandBatches(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID string) {
	limit := 50
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > MaximumListedCommands {
			writeControlProblem(writer, request, &ControlError{
				HTTPStatus: http.StatusBadRequest, Code: "COMMAND_LIST_REQUEST_INVALID",
				Title: "Command batch list request invalid", Detail: "limit must be between 1 and 100.",
			})
			return
		}
		limit = parsed
	}
	view, err := h.config.Authority.ListOperatorCommandBatches(request.Context(), operator, siteID, limit)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, view)
}

func (h *OperatorHTTPHandler) handleGetCommandBatch(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID, batchID string) {
	view, err := h.config.Authority.GetOperatorCommandBatch(request.Context(), operator, siteID, batchID)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, view)
}

func (h *OperatorHTTPHandler) handleCancelCommandBatch(writer http.ResponseWriter, request *http.Request, operator OperatorIdentity, siteID, batchID string) {
	var body CommandBatchCancelInput
	present, ok := decodeOptionalStrictOperatorJSON(request, &body, 4096)
	if !ok {
		writeControlProblem(writer, request, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_CANCEL_REQUEST_INVALID",
			Title: "Command batch cancel invalid", Detail: "The Command batch cancel request payload is invalid.",
		})
		return
	}
	reason := ""
	if present {
		reason = strings.TrimSpace(body.Reason)
	}
	resp, err := h.config.Authority.CancelOperatorCommandBatch(request.Context(), operator, siteID, batchID, reason)
	if err != nil {
		writeControlError(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeCommandJSON(writer, http.StatusOK, resp)
}

func decodeOptionalStrictOperatorJSON(request *http.Request, target any, maxBytes int64) (bool, bool) {
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxBytes+1))
	if err != nil || int64(len(raw)) > maxBytes {
		return false, false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return false, true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || ensureCommandJSONEOF(decoder) != nil {
		return true, false
	}
	return true, true
}

func decodeStrictOperatorJSON(writer http.ResponseWriter, request *http.Request, target any, maxBytes int64) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	return ensureCommandJSONEOF(decoder) == nil
}

func writeControlError(writer http.ResponseWriter, request *http.Request, err error) {
	var ce *ControlError
	if errors.As(err, &ce) && ce != nil {
		writeControlProblem(writer, request, ce)
		return
	}
	converted := controlErrorFromStore(err, "")
	if errors.As(converted, &ce) && ce != nil {
		writeControlProblem(writer, request, ce)
		return
	}
	writeControlProblem(writer, request, unavailableControlError("Command Service is temporarily unavailable."))
}

func writeControlProblem(writer http.ResponseWriter, request *http.Request, ce *ControlError) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.WriteHeader(ce.HTTPStatus)
	title := ce.Title
	if title == "" {
		title = http.StatusText(ce.HTTPStatus)
	}
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"type":      "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(ce.Code, "_", "-")),
		"title":     title,
		"status":    ce.HTTPStatus,
		"detail":    ce.Detail,
		"instance":  request.URL.Path,
		"code":      ce.Code,
		"retryable": ce.Retryable,
	})
}
