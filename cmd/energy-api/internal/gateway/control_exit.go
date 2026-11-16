package gateway

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

const (
	siteControlModeRemoteAuto       = commandservice.SiteControlModeRemoteAuto
	siteControlModeManualOverride   = commandservice.SiteControlModeManualOverride
	siteControlModeSafeShutdownExit = commandservice.SiteControlModeSafeShutdownExit

	siteControlShutdownModePlantSafe          = commandservice.ShutdownModePlantSafe
	siteControlShutdownModeEmergencyDoRelease = commandservice.ShutdownModeEmergencyDoRelease
	siteControlShutdownModeAbortUnitSequence  = commandservice.ShutdownModeAbortUnitSequence

	assetControlLockUnlocked    = commandservice.AssetControlLockUnlocked
	assetControlLockManual      = commandservice.AssetControlLockManual
	assetControlLockMaintenance = commandservice.AssetControlLockMaintenance

	maximumControlExitRequestBody = int64(8 << 10)
)

type publicControlExitRoute struct {
	template     string
	kind         string
	resourceID   string
	internalPath string
	method       string
}

type assetControlLockView = commandservice.AssetControlLockView
type activeMaintenanceWindowView = commandservice.ActiveMaintenanceWindowView
type siteControlStateView = commandservice.SiteControlStateView
type siteControlModeMutationResponse = commandservice.SiteControlModeResponse
type assetControlLockMutationResponse = commandservice.AssetControlLockResponse
type siteControlModeRequest = commandservice.SiteControlModeInput
type assetControlLockRequest = commandservice.AssetControlLockInput
type siteControlShutdownResponse = commandservice.SiteControlShutdownResponse

type siteControlShutdownRequest struct {
	Mode                      string `json:"mode"`
	Reason                    string `json:"reason"`
	VerificationCode          string `json:"verificationCode"`
	UnitCode                  string `json:"unitCode,omitempty"`
	ConfirmEmergencyDoRelease bool   `json:"confirmEmergencyDoRelease,omitempty"`
	ExpectedRevision          *int64 `json:"expectedRevision,omitempty"`
}

func matchPublicControlExitRoute(path string) (publicControlExitRoute, bool) {
	if remainder, ok := strings.CutPrefix(path, "/api/v1/sites/"); ok && !strings.HasSuffix(remainder, "/") {
		segments := strings.Split(remainder, "/")
		if len(segments) == 2 {
			siteID, err := url.PathUnescape(segments[0])
			if err == nil && siteID != "" {
				switch segments[1] {
				case "control-state":
					return publicControlExitRoute{
						template:     "/api/v1/sites/{siteId}/control-state",
						kind:         "site-control-state",
						resourceID:   siteID,
						internalPath: internalOperatorPrefix + "/sites/" + url.PathEscape(siteID) + "/control-state",
						method:       http.MethodGet,
					}, true
				case "control-mode":
					return publicControlExitRoute{
						template:     "/api/v1/sites/{siteId}/control-mode",
						kind:         "site-control-mode",
						resourceID:   siteID,
						internalPath: internalOperatorPrefix + "/sites/" + url.PathEscape(siteID) + "/control-mode",
						method:       http.MethodPut,
					}, true
				case "control-shutdown":
					return publicControlExitRoute{
						template:     "/api/v1/sites/{siteId}/control-shutdown",
						kind:         "site-control-shutdown",
						resourceID:   siteID,
						internalPath: internalOperatorPrefix + "/sites/" + url.PathEscape(siteID) + "/control-shutdown",
						method:       http.MethodPost,
					}, true
				}
			}
		}
	}
	if remainder, ok := strings.CutPrefix(path, "/api/v1/assets/"); ok && !strings.HasSuffix(remainder, "/") {
		segments := strings.Split(remainder, "/")
		if len(segments) == 2 && segments[1] == "control-lock" {
			assetID, err := url.PathUnescape(segments[0])
			if err == nil && assetID != "" {
				return publicControlExitRoute{
					template:     "/api/v1/assets/{assetId}/control-lock",
					kind:         "asset-control-lock",
					resourceID:   assetID,
					internalPath: internalOperatorPrefix + "/assets/" + url.PathEscape(assetID) + "/control-lock",
					method:       http.MethodPut,
				}, true
			}
		}
	}
	return publicControlExitRoute{}, false
}

func dispatchControlExitRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicControlExitRoute) {
	if request.Method != route.method {
		writeMethodNotAllowedFor(writer, request, route.method)
		return
	}
	requireWrite := route.method != http.MethodGet
	session, ok := h.commandSession(writer, request, requireWrite)
	if !ok {
		return
	}
	if requireWrite && !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
		return
	}
	if !isLowerUUIDv7(route.resourceID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site or Asset was not found.", false, nil)
		return
	}
	if h.command == nil || h.command.baseURL == "" || h.command.httpClient == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "Command unavailable", "Command Service is not configured.", true, nil)
		return
	}
	switch route.kind {
	case "site-control-state":
		h.getSiteControlState(writer, request, session, route)
	case "site-control-mode":
		h.putSiteControlMode(writer, request, session, route)
	case "site-control-shutdown":
		h.postSiteControlShutdown(writer, request, session, route)
	case "asset-control-lock":
		h.putAssetControlLock(writer, request, session, route)
	default:
		writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested public API route does not exist.", false, nil)
	}
}

func (h *handler) getSiteControlState(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	if len(request.URL.Query()) != 0 {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "Invalid query", "The control state query does not accept parameters.", false, nil)
		return
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodGet, route.internalPath, nil, nil, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var state siteControlStateView
	if json.Unmarshal(rawBody, &state) != nil || state.TenantID != session.TenantID || state.SiteID != route.resourceID {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Site control state."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, state)
}

func (h *handler) putSiteControlMode(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The control mode request must use application/json.", false, nil)
		return
	}
	var input siteControlModeRequest
	if !decodeStrictControlJSON(writer, request, &input, maximumControlExitRequestBody) {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The Site control mode request payload is invalid.", false, nil)
		return
	}
	if input.ControlMode != siteControlModeRemoteAuto && input.ControlMode != siteControlModeManualOverride && input.ControlMode != siteControlModeSafeShutdownExit {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "controlMode must be REMOTE_AUTO, MANUAL_OVERRIDE, or SAFE_SHUTDOWN_EXIT.", false, nil)
		return
	}

	var extraHeaders map[string]string
	if idem := strings.TrimSpace(request.Header.Get("Idempotency-Key")); idem != "" {
		extraHeaders = map[string]string{"Idempotency-Key": idem}
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPut, route.internalPath, input, extraHeaders, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var resp siteControlModeMutationResponse
	if json.Unmarshal(rawBody, &resp) != nil || resp.TenantID != session.TenantID || resp.SiteID != route.resourceID {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Site control mode response."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, resp)
}

func (h *handler) putAssetControlLock(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTROL_EXIT_REQUEST_INVALID", "Control lock request invalid", "The Asset control lock request must use application/json.", false, nil)
		return
	}
	var input assetControlLockRequest
	if !decodeStrictControlJSON(writer, request, &input, maximumControlExitRequestBody) {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control lock request invalid", "The Asset control lock request payload is invalid.", false, nil)
		return
	}
	if input.LockMode != assetControlLockUnlocked && input.LockMode != assetControlLockManual && input.LockMode != assetControlLockMaintenance {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control lock request invalid", "lockMode must be UNLOCKED, MANUAL_LOCK, or MAINTENANCE_LOCK.", false, nil)
		return
	}

	var extraHeaders map[string]string
	if idem := strings.TrimSpace(request.Header.Get("Idempotency-Key")); idem != "" {
		extraHeaders = map[string]string{"Idempotency-Key": idem}
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPut, route.internalPath, input, extraHeaders, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var resp assetControlLockMutationResponse
	if json.Unmarshal(rawBody, &resp) != nil || resp.AssetID != route.resourceID || (resp.TenantID != "" && resp.TenantID != session.TenantID) {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Asset control lock response."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, resp)
}

func (h *handler) postSiteControlShutdown(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The Site control shutdown request must use application/json.", false, nil)
		return
	}
	var input siteControlShutdownRequest
	if !decodeStrictControlJSON(writer, request, &input, maximumControlExitRequestBody) {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The Site control shutdown request payload is invalid.", false, nil)
		return
	}
	switch input.Mode {
	case siteControlShutdownModePlantSafe, siteControlShutdownModeEmergencyDoRelease, siteControlShutdownModeAbortUnitSequence:
	default:
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "mode must be PLANT_SAFE_SHUTDOWN, EMERGENCY_DO_RELEASE, or ABORT_UNIT_SEQUENCE.", false, nil)
		return
	}
	if strings.TrimSpace(input.Reason) == "" {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "A non-empty reason is required.", false, nil)
		return
	}
	if input.Mode == siteControlShutdownModeAbortUnitSequence && strings.TrimSpace(input.UnitCode) == "" {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "unitCode is required when mode is ABORT_UNIT_SEQUENCE.", false, nil)
		return
	}
	if input.Mode == siteControlShutdownModeEmergencyDoRelease && !input.ConfirmEmergencyDoRelease {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "confirmEmergencyDoRelease must be explicitly true for EMERGENCY_DO_RELEASE.", false, nil)
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

	var extraHeaders map[string]string
	if idem := strings.TrimSpace(request.Header.Get("Idempotency-Key")); idem != "" {
		extraHeaders = map[string]string{"Idempotency-Key": idem}
	}
	payload := commandservice.SiteControlShutdownInput{
		Mode:                      input.Mode,
		Reason:                    input.Reason,
		UnitCode:                  input.UnitCode,
		ConfirmEmergencyDoRelease: input.ConfirmEmergencyDoRelease,
		ExpectedRevision:          input.ExpectedRevision,
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPost, route.internalPath, payload, extraHeaders, h.command.maxResponseBytes, extraActions...,
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var resp siteControlShutdownResponse
	if json.Unmarshal(rawBody, &resp) != nil ||
		resp.SiteControlState.TenantID != session.TenantID || resp.SiteControlState.SiteID != route.resourceID {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Site control shutdown response."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, resp)
}

func decodeStrictControlJSON(writer http.ResponseWriter, request *http.Request, target any, maxBytes int64) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	return ensureCommandJSONEOF(decoder) == nil
}
