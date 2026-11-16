package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
)

const (
	siteControlModeRemoteAuto       = "REMOTE_AUTO"
	siteControlModeManualOverride   = "MANUAL_OVERRIDE"
	siteControlModeSafeShutdownExit = "SAFE_SHUTDOWN_EXIT"

	siteControlShutdownModePlantSafe          = "PLANT_SAFE_SHUTDOWN"
	siteControlShutdownModeEmergencyDoRelease = "EMERGENCY_DO_RELEASE"
	siteControlShutdownModeAbortUnitSequence  = "ABORT_UNIT_SEQUENCE"

	assetControlLockUnlocked    = "UNLOCKED"
	assetControlLockManual      = "MANUAL_LOCK"
	assetControlLockMaintenance = "MAINTENANCE_LOCK"

	internalCommandsCancelPendingPath = "/internal/v1/commands/cancel-pending"
	maximumControlExitRequestBody     = int64(8 << 10)
)

var controlWriteKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}\z`)

type publicControlExitRoute struct {
	template     string
	kind         string
	resourceID   string
	internalPath string
	method       string
}

type assetControlLockView struct {
	ID             string   `json:"id,omitempty"`
	TenantID       string   `json:"tenantId,omitempty"`
	SiteID         string   `json:"siteId"`
	AssetID        string   `json:"assetId"`
	LockMode       string   `json:"lockMode"`
	Reason         string   `json:"reason"`
	UpdatedBy      *string  `json:"updatedBy"`
	UpdatedAt      *string  `json:"updatedAt"`
	Revision       int64    `json:"revision"`
	BoundDeviceIDs []string `json:"boundDeviceIds,omitempty"`
}

type activeMaintenanceWindowView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Reason    string `json:"reason"`
}

type siteControlStateView struct {
	ID                      *string                      `json:"id,omitempty"`
	TenantID                string                       `json:"tenantId"`
	SiteID                  string                       `json:"siteId"`
	ControlMode             string                       `json:"controlMode"`
	Reason                  string                       `json:"reason"`
	UpdatedBy               *string                      `json:"updatedBy"`
	UpdatedAt               *string                      `json:"updatedAt"`
	Revision                int64                        `json:"revision"`
	AssetLocks              []assetControlLockView       `json:"assetLocks"`
	ActiveMaintenanceWindow *activeMaintenanceWindowView `json:"activeMaintenanceWindow,omitempty"`
}

type siteControlModeMutationResponse struct {
	siteControlStateView
	CancelledCommandIDs []string `json:"cancelledCommandIds"`
}

type assetControlLockMutationResponse struct {
	assetControlLockView
	CancelledCommandIDs []string `json:"cancelledCommandIds"`
}

type controlRegistryMeta struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	IdempotencyKey   string `json:"idempotencyKey"`
	Reason           string `json:"reason"`
}

type siteControlModeRequest struct {
	ControlMode      string               `json:"controlMode"`
	Reason           string               `json:"reason,omitempty"`
	ExpectedRevision *int64               `json:"expectedRevision,omitempty"`
	Meta             *controlRegistryMeta `json:"meta,omitempty"`
}

type siteControlShutdownRequest struct {
	Mode                      string `json:"mode"`
	Reason                    string `json:"reason"`
	VerificationCode          string `json:"verificationCode"`
	UnitCode                  string `json:"unitCode,omitempty"`
	ConfirmEmergencyDoRelease bool   `json:"confirmEmergencyDoRelease,omitempty"`
	ExpectedRevision          *int64 `json:"expectedRevision,omitempty"`
}

type siteControlShutdownResponse struct {
	SiteControlState        siteControlStateView `json:"siteControlState"`
	Mode                    string               `json:"mode"`
	CancelledCommandIDs     []string             `json:"cancelledCommandIds"`
	SubmittedCommandIDs     []string             `json:"submittedCommandIds"`
	DeferredHoldingAssetIds []string             `json:"deferredHoldingAssetIds"`
}

type assetControlLockRequest struct {
	LockMode         string               `json:"lockMode"`
	Reason           string               `json:"reason,omitempty"`
	ExpectedRevision *int64               `json:"expectedRevision,omitempty"`
	Meta             *controlRegistryMeta `json:"meta,omitempty"`
}

type internalCommandCancelPendingRequest struct {
	TenantID             string   `json:"tenantId"`
	SiteID               string   `json:"siteId"`
	DeviceIDs            []string `json:"deviceIds,omitempty"`
	PrincipalID          string   `json:"principalId"`
	Reason               string   `json:"reason,omitempty"`
	CausationID          string   `json:"causationId,omitempty"`
	AbortActiveSequences bool     `json:"abortActiveSequences,omitempty"`
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
						internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/control-state",
						method:       http.MethodGet,
					}, true
				case "control-mode":
					return publicControlExitRoute{
						template:     "/api/v1/sites/{siteId}/control-mode",
						kind:         "site-control-mode",
						resourceID:   siteID,
						internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/control-mode",
						method:       http.MethodPut,
					}, true
				case "control-shutdown":
					return publicControlExitRoute{
						template:     "/api/v1/sites/{siteId}/control-shutdown",
						kind:         "site-control-shutdown",
						resourceID:   siteID,
						internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/control-mode",
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
					internalPath: "/internal/v1/registry/assets/" + url.PathEscape(assetID) + "/control-lock",
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
	if h.registry == nil || h.registry.coreBaseURL == "" || h.registry.coreHTTPClient == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Core Registry is not configured.", true, nil)
		return
	}
	switch route.kind {
	case "site-control-state":
		h.getSiteControlState(writer, request, session, route.resourceID)
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

func (h *handler) getSiteControlState(writer http.ResponseWriter, request *http.Request, session bffSession, siteID string) {
	if len(request.URL.Query()) != 0 {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "Invalid query", "The control state query does not accept parameters.", false, nil)
		return
	}
	state, failure := h.readCoreSiteControlState(request, session, siteID)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if state.ControlMode == siteControlModeSafeShutdownExit {
		h.releaseSafeShutdownHoldingAuxiliariesBestEffort(request, session, siteID, state)
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

	siteID := route.resourceID
	auth, ok := h.authorizeSiteControlExitWrite(writer, request, session, siteID, "The authenticated Principal is not allowed to change Site control mode.")
	if !ok {
		return
	}

	reason, meta, failure := h.resolveControlExitMeta(request, input.Reason, input.ExpectedRevision, input.Meta, func() (int64, *commandFailure) {
		current, errFailure := h.readCoreSiteControlState(request, session, siteID)
		if errFailure != nil {
			return 0, errFailure
		}
		return current.Revision, nil
	})
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}

	if input.ControlMode == siteControlModeSafeShutdownExit {
		if preFailure := h.verifySafeShutdownPreconditions(request, session, siteID); preFailure != nil {
			h.writeCommandFailure(writer, request, *preFailure)
			return
		}
	}

	payload := map[string]any{
		"controlMode": input.ControlMode,
		"reason":      reason,
		"meta":        meta,
	}
	saved, cancelledIDs, ok := h.saveSiteControlModeAndCancelPending(writer, request, session, route, auth, siteID, payload, reason, false)
	if !ok {
		return
	}

	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, siteControlModeMutationResponse{
		siteControlStateView: saved,
		CancelledCommandIDs:  cancelledIDs,
	})
}

func (h *handler) saveSiteControlModeAndCancelPending(
	writer http.ResponseWriter,
	request *http.Request,
	session bffSession,
	route publicControlExitRoute,
	auth registryAuthorization,
	siteID string,
	payload map[string]any,
	reason string,
	abortActiveSequences bool,
) (siteControlStateView, []string, bool) {
	status, raw, coreFailure := h.callCoreControlRegistry(request.Context(), http.MethodPut, route.internalPath, auth.coreGrant, payload, routeRevisionFromRequest(request))
	if coreFailure != nil {
		h.writeCommandFailure(writer, request, *coreFailure)
		return siteControlStateView{}, nil, false
	}
	if status != http.StatusOK && status != http.StatusCreated {
		h.writeCoreControlRegistryError(writer, request, status, raw)
		return siteControlStateView{}, nil, false
	}
	var saved siteControlStateView
	if json.Unmarshal(raw, &saved) != nil || saved.SiteID != siteID {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "Core Registry returned an invalid Site control state.", true, nil)
		return siteControlStateView{}, nil, false
	}
	if saved.AssetLocks == nil {
		saved.AssetLocks = []assetControlLockView{}
	}

	cancelledIDs := []string{}
	if saved.ControlMode != siteControlModeRemoteAuto {
		var causationID string
		if saved.ID != nil {
			causationID = *saved.ID
		}
		cancelled, cancelFailure := h.executeCommandCancelPending(request.Context(), session, siteID, nil, auth.principalID, reason, causationID, abortActiveSequences)
		if cancelFailure != nil {
			h.writeCommandFailure(writer, request, *cancelFailure)
			return siteControlStateView{}, nil, false
		}
		if len(cancelled) > 0 {
			cancelledIDs = cancelled
		}
	}
	return saved, cancelledIDs, true
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

	assetID := route.resourceID
	asset, assetFailure := h.resolveControlAsset(request, session, assetID)
	if assetFailure != nil {
		h.writeCommandFailure(writer, request, *assetFailure)
		return
	}
	auth, ok := h.authorizeSiteControlExitWrite(writer, request, session, asset.SiteID, "The authenticated Principal is not allowed to lock or unlock Assets on this Site.")
	if !ok {
		return
	}

	reason, meta, failure := h.resolveControlExitMeta(request, input.Reason, input.ExpectedRevision, input.Meta, func() (int64, *commandFailure) {
		current, errFailure := h.readCoreSiteControlState(request, session, asset.SiteID)
		if errFailure != nil {
			return 0, errFailure
		}
		for _, item := range current.AssetLocks {
			if item.AssetID == assetID {
				return item.Revision, nil
			}
		}
		return 0, nil
	})
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}

	payload := map[string]any{
		"lockMode": input.LockMode,
		"reason":   reason,
		"meta":     meta,
	}
	status, raw, coreFailure := h.callCoreControlRegistry(request.Context(), http.MethodPut, route.internalPath, auth.coreGrant, payload, routeRevisionFromRequest(request))
	if coreFailure != nil {
		h.writeCommandFailure(writer, request, *coreFailure)
		return
	}
	if status != http.StatusOK && status != http.StatusCreated {
		h.writeCoreControlRegistryError(writer, request, status, raw)
		return
	}
	var saved assetControlLockView
	if json.Unmarshal(raw, &saved) != nil || saved.AssetID != assetID || saved.SiteID != asset.SiteID {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "Core Registry returned an invalid Asset control lock.", true, nil)
		return
	}

	cancelledIDs := []string{}
	if saved.LockMode != assetControlLockUnlocked && len(saved.BoundDeviceIDs) > 0 {
		cancelled, cancelFailure := h.executeCommandCancelPending(request.Context(), session, saved.SiteID, saved.BoundDeviceIDs, auth.principalID, reason, saved.ID, false)
		if cancelFailure != nil {
			h.writeCommandFailure(writer, request, *cancelFailure)
			return
		}
		if len(cancelled) > 0 {
			cancelledIDs = cancelled
		}
	}

	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, assetControlLockMutationResponse{
		assetControlLockView: saved,
		CancelledCommandIDs:  cancelledIDs,
	})
}

func (h *handler) authorizeSiteControlExitWrite(writer http.ResponseWriter, request *http.Request, session bffSession, siteID, detail string) (registryAuthorization, bool) {
	authorization, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionScheduleManage)
	if failure != nil && failure.status != http.StatusForbidden && failure.status != http.StatusNotFound {
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return registryAuthorization{}, false
	}
	if _, allowed := authorization.allowedSiteIDs[siteID]; failure != nil || !allowed {
		writeProblem(writer, request, http.StatusForbidden, "CONTROL_EXIT_FORBIDDEN", "Control exit forbidden", detail, false, nil)
		return registryAuthorization{}, false
	}
	return authorization, true
}

func (h *handler) resolveControlAsset(request *http.Request, session bffSession, assetID string) (platformapi.Asset, *commandFailure) {
	assetAuth, authFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionAssetRead)
	if authFailure != nil {
		failure := commandFailure{status: authFailure.status, code: authFailure.code, title: authFailure.title, detail: authFailure.detail, retryable: authFailure.retryable}
		return platformapi.Asset{}, &failure
	}
	status, raw, callFailure := h.callCoreControlRegistry(request.Context(), http.MethodGet, "/internal/v1/registry/assets/"+url.PathEscape(assetID), assetAuth.coreGrant, nil, routeRevisionFromRequest(request))
	if callFailure != nil {
		return platformapi.Asset{}, callFailure
	}
	if status == http.StatusNotFound || status == http.StatusForbidden {
		failure := commandNotFound()
		return platformapi.Asset{}, &failure
	}
	if status != http.StatusOK {
		failure := commandUnavailable("Registry could not resolve the Asset for control lock.")
		return platformapi.Asset{}, &failure
	}
	var asset platformapi.Asset
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&asset) != nil || ensureCommandJSONEOF(decoder) != nil || asset.ID != assetID || !isLowerUUIDv7(asset.SiteID) {
		failure := commandUnavailable("Registry returned an invalid Asset projection.")
		return platformapi.Asset{}, &failure
	}
	return asset, nil
}

func (h *handler) readCoreSiteControlState(request *http.Request, session bffSession, siteID string) (siteControlStateView, *commandFailure) {
	authorization, authFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if authFailure != nil {
		failure := commandFailure{status: authFailure.status, code: authFailure.code, title: authFailure.title, detail: authFailure.detail, retryable: authFailure.retryable}
		return siteControlStateView{}, &failure
	}
	status, raw, callFailure := h.callCoreControlRegistry(request.Context(), http.MethodGet, "/internal/v1/registry/sites/"+url.PathEscape(siteID)+"/control-state", authorization.coreGrant, nil, routeRevisionFromRequest(request))
	if callFailure != nil {
		return siteControlStateView{}, callFailure
	}
	if status == http.StatusNotFound || status == http.StatusForbidden {
		failure := commandNotFound()
		return siteControlStateView{}, &failure
	}
	if status != http.StatusOK {
		failure := commandUnavailable("Registry could not read the Site control state.")
		return siteControlStateView{}, &failure
	}
	var state siteControlStateView
	if json.Unmarshal(raw, &state) != nil || state.SiteID != siteID ||
		(state.ControlMode != siteControlModeRemoteAuto && state.ControlMode != siteControlModeManualOverride && state.ControlMode != siteControlModeSafeShutdownExit) {
		failure := commandUnavailable("Registry returned an invalid Site control state.")
		return siteControlStateView{}, &failure
	}
	if state.AssetLocks == nil {
		state.AssetLocks = []assetControlLockView{}
	}
	return state, nil
}

func isMaintenancePermittedCapability(capability commandmodel.Capability) bool {
	switch capability {
	case commandmodel.CapabilityStop, commandmodel.CapabilityStopUnitSequence, commandmodel.CapabilityResetFault:
		return true
	default:
		return false
	}
}

func isControlExitStopCapability(capability commandmodel.Capability) bool {
	return capability == commandmodel.CapabilityStop || capability == commandmodel.CapabilityStopUnitSequence
}

func (h *handler) enforceControlLockForCommand(request *http.Request, session bffSession, siteID, assetID, deviceID string, capability commandmodel.Capability) *commandFailure {
	if h.registry == nil || h.registry.coreBaseURL == "" || h.registry.coreHTTPClient == nil {
		failure := commandUnavailable("Core Registry is unavailable for Command lock enforcement.")
		return &failure
	}
	state, failure := h.readCoreSiteControlState(request, session, siteID)
	if failure != nil {
		return failure
	}
	if state.ControlMode != siteControlModeRemoteAuto && !isControlExitStopCapability(capability) {
		locked := commandFailure{
			status:    http.StatusConflict,
			code:      "COMMAND_CONTROL_LOCKED",
			title:     "Site control exit active",
			detail:    "Remote commands are blocked while Site control mode is " + state.ControlMode + ".",
			retryable: false,
		}
		return &locked
	}
	for _, lock := range state.AssetLocks {
		if lock.LockMode != assetControlLockUnlocked && (lock.AssetID == assetID || containsString(lock.BoundDeviceIDs, deviceID)) {
			locked := commandFailure{
				status:    http.StatusConflict,
				code:      "COMMAND_CONTROL_LOCKED",
				title:     "Asset control locked",
				detail:    "Commands targeting this Asset are blocked while control lock is " + lock.LockMode + ".",
				retryable: false,
			}
			return &locked
		}
	}
	if state.ActiveMaintenanceWindow != nil && !isMaintenancePermittedCapability(capability) {
		locked := commandFailure{
			status:    http.StatusConflict,
			code:      "COMMAND_CONTROL_LOCKED",
			title:     "Site maintenance window active",
			detail:    fmt.Sprintf("Only STOP, STOP_UNIT_SEQUENCE, and RESET_FAULT commands are permitted during active maintenance window %q (%s..%s).", state.ActiveMaintenanceWindow.Title, state.ActiveMaintenanceWindow.StartDate, state.ActiveMaintenanceWindow.EndDate),
			retryable: false,
		}
		return &locked
	}
	return nil
}

func (h *handler) verifySafeShutdownPreconditions(request *http.Request, session bffSession, siteID string) *commandFailure {
	if h.command == nil || h.command.baseURL == "" || h.command.httpClient == nil {
		return nil
	}
	views, failure := h.executeCommandList(request, session, siteID, maximumListedCommands)
	if failure != nil {
		return failure
	}
	for _, view := range views {
		if view.Status == commandmodel.IntentDispatching || view.Status == commandmodel.IntentOutcomeUnknown {
			blocked := commandFailure{
				status:    http.StatusConflict,
				code:      "SAFE_SHUTDOWN_PRECONDITION_FAILED",
				title:     "Safe shutdown blocked by active commands",
				detail:    "Wait for dispatching commands to complete or reconcile unknown command outcomes before safe shutdown exit.",
				retryable: false,
			}
			return &blocked
		}
	}
	return nil
}

func (h *handler) resolveControlExitMeta(
	request *http.Request,
	rawReason string,
	expectedRevision *int64,
	providedMeta *controlRegistryMeta,
	fetchCurrentRevision func() (int64, *commandFailure),
) (string, controlRegistryMeta, *commandFailure) {
	reason := strings.TrimSpace(rawReason)
	if providedMeta != nil {
		if reason == "" {
			reason = strings.TrimSpace(providedMeta.Reason)
		}
		metaReason := strings.TrimSpace(providedMeta.Reason)
		if metaReason == "" {
			metaReason = reason
		}
		if reason == "" || utf8.RuneCountInString(reason) > 1000 || utf8.RuneCountInString(metaReason) > 1000 ||
			!controlWriteKeyPattern.MatchString(providedMeta.IdempotencyKey) || providedMeta.ExpectedRevision < 0 {
			invalid := commandFailure{
				status:    http.StatusBadRequest,
				code:      "CONTROL_EXIT_REQUEST_INVALID",
				title:     "Control exit request invalid",
				detail:    "A valid reason, non-negative expectedRevision, and valid idempotencyKey are required.",
				retryable: false,
			}
			return "", controlRegistryMeta{}, &invalid
		}
		return reason, controlRegistryMeta{
			ExpectedRevision: providedMeta.ExpectedRevision,
			IdempotencyKey:   providedMeta.IdempotencyKey,
			Reason:           metaReason,
		}, nil
	}

	if reason == "" || utf8.RuneCountInString(reason) > 1000 {
		invalid := commandFailure{
			status:    http.StatusBadRequest,
			code:      "CONTROL_EXIT_REQUEST_INVALID",
			title:     "Control exit request invalid",
			detail:    "A non-empty reason up to 1000 characters is required.",
			retryable: false,
		}
		return "", controlRegistryMeta{}, &invalid
	}

	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if idempotencyKey != "" && !controlWriteKeyPattern.MatchString(idempotencyKey) {
		invalid := commandFailure{
			status:    http.StatusBadRequest,
			code:      "CONTROL_EXIT_REQUEST_INVALID",
			title:     "Control exit request invalid",
			detail:    "The Idempotency-Key header format is invalid.",
			retryable: false,
		}
		return "", controlRegistryMeta{}, &invalid
	}
	if idempotencyKey == "" {
		idempotencyKey = "ctrl-" + randomURLToken(16)
	}

	var revision int64
	if expectedRevision != nil {
		if *expectedRevision < 0 {
			invalid := commandFailure{
				status:    http.StatusBadRequest,
				code:      "CONTROL_EXIT_REQUEST_INVALID",
				title:     "Control exit request invalid",
				detail:    "expectedRevision must be non-negative.",
				retryable: false,
			}
			return "", controlRegistryMeta{}, &invalid
		}
		revision = *expectedRevision
	} else {
		var revFailure *commandFailure
		revision, revFailure = fetchCurrentRevision()
		if revFailure != nil {
			return "", controlRegistryMeta{}, revFailure
		}
	}

	return reason, controlRegistryMeta{
		ExpectedRevision: revision,
		IdempotencyKey:   idempotencyKey,
		Reason:           reason,
	}, nil
}

func (h *handler) callCoreControlRegistry(ctx context.Context, method, internalPath, grant string, payload any, routeRevision int64) (int, []byte, *commandFailure) {
	if h.registry == nil || h.registry.coreBaseURL == "" || h.registry.coreHTTPClient == nil {
		failure := commandUnavailable("Core Registry is not configured.")
		return 0, nil, &failure
	}
	var bodyReader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			failure := commandUnavailable("The Registry request could not be encoded.")
			return 0, nil, &failure
		}
		bodyReader = bytes.NewReader(encoded)
	}
	reqCtx, cancel := context.WithTimeout(ctx, h.registry.coreTimeout)
	defer cancel()
	upstreamReq, err := http.NewRequestWithContext(reqCtx, method, h.registry.coreBaseURL+internalPath, bodyReader)
	if err != nil {
		failure := commandUnavailable("The Core Registry request could not be constructed.")
		return 0, nil, &failure
	}
	upstreamReq.Header.Set("Accept", "application/json, application/problem+json")
	if payload != nil {
		upstreamReq.Header.Set("Content-Type", "application/json")
	}
	upstreamReq.Header.Set("X-Delegation-Grant", grant)
	upstreamReq.Header.Set("X-Route-Policy-Revision", formatRevision(routeRevision))
	upstreamReq.Header.Set("X-Request-ID", requestIDFromContext(ctx))
	observability.InjectHTTP(ctx, upstreamReq.Header)
	response, err := h.registry.coreHTTPClient.Do(upstreamReq)
	if err != nil {
		failure := commandUnavailable("Core Registry is temporarily unavailable.")
		return 0, nil, &failure
	}
	defer response.Body.Close()
	raw, err := readBoundedBody(response.Body, h.registry.maxResponseBytes)
	if err != nil {
		failure := commandUnavailable("Core Registry response exceeded the supported size.")
		return 0, nil, &failure
	}
	return response.StatusCode, raw, nil
}

func (h *handler) writeCoreControlRegistryError(writer http.ResponseWriter, request *http.Request, status int, raw []byte) {
	var upstream platformapi.ProblemDetails
	if json.Unmarshal(raw, &upstream) == nil && upstream.Code != "" {
		detail := upstream.Detail
		if detail == "" {
			detail = "Core Registry rejected the control state mutation."
		}
		title := upstream.Title
		if title == "" {
			title = http.StatusText(status)
		}
		writeProblem(writer, request, status, upstream.Code, title, detail, upstream.Retryable, nil)
		return
	}
	switch status {
	case http.StatusBadRequest:
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The control state mutation is invalid.", false, nil)
	case http.StatusForbidden:
		writeProblem(writer, request, http.StatusForbidden, "CONTROL_EXIT_FORBIDDEN", "Control exit forbidden", "The authenticated Principal is not allowed to update control state.", false, nil)
	case http.StatusNotFound:
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site or Asset was not found.", false, nil)
	case http.StatusConflict:
		writeProblem(writer, request, http.StatusConflict, "REGISTRY_REVISION_CONFLICT", "Control state conflict", "The expected control state revision is stale or conflicts with another request.", false, nil)
	default:
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "Core Registry is temporarily unavailable.", true, nil)
	}
}

func (h *handler) executeCommandCancelPending(ctx context.Context, session bffSession, siteID string, deviceIDs []string, principalID, reason, causationID string, abortActiveSequences bool) ([]string, *commandFailure) {
	if h.command == nil || h.command.baseURL == "" || h.command.httpClient == nil {
		return []string{}, nil
	}
	if h.identity == nil || h.identity.config.DelegationSigner == nil {
		failure := commandUnavailable("Gateway identity signer is unavailable for control exit command cancellation.")
		return nil, &failure
	}
	controlContext, failure := h.signCommandControlContext(session, siteID, "command:cancel-pending")
	if failure != nil {
		return nil, failure
	}
	body, err := json.Marshal(internalCommandCancelPendingRequest{
		TenantID:             session.TenantID,
		SiteID:               siteID,
		DeviceIDs:            deviceIDs,
		PrincipalID:          principalID,
		Reason:               reason,
		CausationID:          causationID,
		AbortActiveSequences: abortActiveSequences,
	})
	if err != nil {
		failure := commandUnavailable("The pending command cancellation request could not be encoded.")
		return nil, &failure
	}
	reqCtx, cancel := context.WithTimeout(ctx, h.command.timeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(reqCtx, http.MethodPost, h.command.baseURL+internalCommandsCancelPendingPath, bytes.NewReader(body))
	if err != nil {
		failure := commandUnavailable("The pending command cancellation request could not be constructed.")
		return nil, &failure
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json, application/problem+json")
	upstream.Header.Set("X-Command-Control-Context", controlContext)
	upstream.Header.Set("X-Request-ID", requestIDFromContext(ctx))
	observability.InjectHTTP(ctx, upstream.Header)
	response, err := h.command.httpClient.Do(upstream)
	if err != nil {
		failure := commandUnavailable("Command Service cancel-pending is temporarily unavailable.")
		return nil, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := commandBackendFailure(response.StatusCode)
		return nil, &failure
	}
	raw, err := readBoundedBody(response.Body, h.command.maxResponseBytes)
	if err != nil {
		failure := commandUnavailable("Command Service returned an unreadable cancel-pending response.")
		return nil, &failure
	}
	var result struct {
		CancelledCommandIDs []string `json:"cancelledCommandIds"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || ensureCommandJSONEOF(decoder) != nil {
		failure := commandUnavailable("Command Service returned an invalid cancel-pending response.")
		return nil, &failure
	}
	if result.CancelledCommandIDs == nil {
		return []string{}, nil
	}
	return result.CancelledCommandIDs, nil
}

func (h *handler) signCommandControlContext(session bffSession, siteID, action string) (string, *commandFailure) {
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.command.backendAudience,
		TenantID: session.TenantID, Actions: []string{action},
		Scopes:         []string{"tenant:" + session.TenantID, "site:" + siteID},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	controlContext, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := commandUnavailable("The Command control context could not be signed.")
		return "", &failure
	}
	return controlContext, nil
}

func decodeStrictControlJSON(writer http.ResponseWriter, request *http.Request, target any, maxBytes int64) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false
	}
	return ensureCommandJSONEOF(decoder) == nil
}

func routeRevisionFromRequest(request *http.Request) int64 {
	decision := routeDecisionFromContext(request.Context())
	return decision.RegistryRevision
}

type shutdownControlTarget struct {
	asset         platformapi.Asset
	device        platformapi.Device
	point         platformapi.TelemetryPoint
	feedbackPoint platformapi.TelemetryPoint
	capability    commandmodel.Capability
	actuationMode string
	interlocks    []commandmodel.InterlockCheck
}

func (t shutdownControlTarget) asAssetCommandTarget() assetCommandTarget {
	return assetCommandTarget{
		asset:         t.asset,
		device:        t.device,
		point:         t.point,
		feedbackPoint: t.feedbackPoint,
		capability:    t.capability,
		interlocks:    t.interlocks,
	}
}

type shutdownTopology struct {
	chillers               []platformapi.Asset
	auxiliaries            []platformapi.Asset
	chillersByCode         map[string]platformapi.Asset
	lockedAssets           map[string]bool
	boundDevicesByAsset    map[string][]string
	controlsAssetsByPoint  map[string][]string
	regularStopByAsset     map[string]shutdownControlTarget
	abortSequenceByAsset   map[string]shutdownControlTarget
	statusCheckByAsset     map[string]shutdownControlTarget
	servedChillersByAux    map[string][]string
	emergencyReleaseTarget *shutdownControlTarget
}

func (h *handler) currentGatewayTime() time.Time {
	if h.identity != nil && h.identity.now != nil {
		return h.identity.now().UTC()
	}
	return time.Now().UTC()
}

func isShutdownHoldingAuxiliaryType(assetType string) bool {
	switch strings.ToUpper(strings.TrimSpace(assetType)) {
	case "CHILLED_WATER_PUMP", "COOLING_WATER_PUMP", "COOLING_TOWER", "PUMP":
		return true
	default:
		return false
	}
}

func appendUniqueShutdownString(items []string, candidate string) []string {
	if candidate == "" || containsString(items, candidate) {
		return items
	}
	return append(items, candidate)
}

func sortAssetsDeterministically(items []platformapi.Asset) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Code != items[j].Code {
			return items[i].Code < items[j].Code
		}
		return items[i].ID < items[j].ID
	})
}

func resolveSiteControlTopology(model platformapi.SiteAssetModel, now time.Time) shutdownTopology {
	topo := shutdownTopology{
		chillersByCode:        map[string]platformapi.Asset{},
		lockedAssets:          map[string]bool{},
		boundDevicesByAsset:   map[string][]string{},
		controlsAssetsByPoint: map[string][]string{},
		regularStopByAsset:    map[string]shutdownControlTarget{},
		abortSequenceByAsset:  map[string]shutdownControlTarget{},
		statusCheckByAsset:    map[string]shutdownControlTarget{},
		servedChillersByAux:   map[string][]string{},
	}
	assetsByID := make(map[string]platformapi.Asset, len(model.Assets))
	for _, asset := range model.Assets {
		if !strings.EqualFold(asset.Status, "ACTIVE") {
			continue
		}
		assetsByID[asset.ID] = asset
		switch strings.ToUpper(strings.TrimSpace(asset.AssetType)) {
		case "CHILLER":
			topo.chillers = append(topo.chillers, asset)
			if code := strings.ToLower(strings.TrimSpace(asset.Code)); code != "" {
				topo.chillersByCode[code] = asset
			}
		default:
			if isShutdownHoldingAuxiliaryType(asset.AssetType) {
				topo.auxiliaries = append(topo.auxiliaries, asset)
			}
		}
	}
	sortAssetsDeterministically(topo.chillers)
	sortAssetsDeterministically(topo.auxiliaries)

	devicesByID := indexActiveDevicesByID(model)
	points, pointsByID, feedbackByDeviceKey := indexActivePoints(model)
	controlsByPoint, controlsSite, deviceToChillers := topo.indexRelationships(model, assetsByID, pointsByID, now)
	topo.controlsAssetsByPoint = controlsByPoint
	topo.resolveControlTargets(model, assetsByID, devicesByID, points, feedbackByDeviceKey, controlsByPoint, controlsSite, deviceToChillers)
	topo.assignFallbackRunStateTargets(assetsByID, devicesByID, feedbackByDeviceKey)
	for auxID, chIDs := range topo.servedChillersByAux {
		sort.Strings(chIDs)
		topo.servedChillersByAux[auxID] = chIDs
	}
	return topo
}

func buildShutdownTopology(model platformapi.SiteAssetModel, state siteControlStateView, now time.Time) shutdownTopology {
	topo := resolveSiteControlTopology(model, now)
	topo.indexAssetLocks(state.AssetLocks)
	return topo
}

func resolveAbortTargetForCommand(topo shutdownTopology, pointID, deviceID string) (shutdownControlTarget, bool) {
	for _, assetID := range topo.controlsAssetsByPoint[pointID] {
		if target, ok := topo.abortSequenceByAsset[assetID]; ok {
			return target, true
		}
	}
	for _, target := range topo.abortSequenceByAsset {
		if target.device.ID == deviceID {
			return target, true
		}
	}
	return shutdownControlTarget{}, false
}

func indexActiveDevicesByID(model platformapi.SiteAssetModel) map[string]platformapi.Device {
	devicesByID := make(map[string]platformapi.Device, len(model.Devices))
	for _, device := range model.Devices {
		if strings.EqualFold(device.Status, "ACTIVE") && device.TenantID == model.TenantID && device.SiteID == model.SiteID {
			devicesByID[device.ID] = device
		}
	}
	return devicesByID
}

func indexActivePoints(model platformapi.SiteAssetModel) ([]platformapi.TelemetryPoint, map[string]platformapi.TelemetryPoint, map[string]platformapi.TelemetryPoint) {
	points := append([]platformapi.TelemetryPoint(nil), model.TelemetryPoints...)
	sort.Slice(points, func(i, j int) bool {
		if points[i].PointCode != points[j].PointCode {
			return points[i].PointCode < points[j].PointCode
		}
		return points[i].ID < points[j].ID
	})
	pointsByID := make(map[string]platformapi.TelemetryPoint, len(points))
	feedbackByDeviceKey := make(map[string]platformapi.TelemetryPoint, len(points))
	for _, point := range points {
		if !strings.EqualFold(point.Status, "ACTIVE") || point.TenantID != model.TenantID || point.SiteID != model.SiteID {
			continue
		}
		pointsByID[point.ID] = point
		if point.PointType == "STATE" || point.PointType == "TELEMETRY" || point.PointType == "SETTING" {
			feedbackByDeviceKey[point.ReportingDeviceID+"\x00"+point.PointCode] = point
		}
	}
	return points, pointsByID, feedbackByDeviceKey
}

func (topo *shutdownTopology) indexRelationships(
	model platformapi.SiteAssetModel,
	assetsByID map[string]platformapi.Asset,
	pointsByID map[string]platformapi.TelemetryPoint,
	now time.Time,
) (map[string][]string, map[string]bool, map[string][]string) {
	controlsByPoint := map[string][]string{}
	controlsSite := map[string]bool{}
	deviceToChillers := map[string][]string{}
	for _, rel := range model.Relationships {
		if !commandRelationshipCurrent(rel, now) {
			continue
		}
		switch {
		case rel.FromType == "POINT" && rel.Role == "CONTROLS":
			if rel.ToType == "SITE" && rel.ToID == model.SiteID {
				controlsSite[rel.FromID] = true
			}
			if rel.ToType == "ASSET" {
				if asset, ok := assetsByID[rel.ToID]; ok {
					controlsByPoint[rel.FromID] = appendUniqueShutdownString(controlsByPoint[rel.FromID], asset.ID)
					if pt, ok := pointsByID[rel.FromID]; ok {
						topo.boundDevicesByAsset[asset.ID] = appendUniqueShutdownString(topo.boundDevicesByAsset[asset.ID], pt.ReportingDeviceID)
						if strings.EqualFold(asset.AssetType, "CHILLER") {
							deviceToChillers[pt.ReportingDeviceID] = appendUniqueShutdownString(deviceToChillers[pt.ReportingDeviceID], asset.ID)
						}
					}
				}
			}
		case rel.FromType == "DEVICE" && rel.ToType == "ASSET":
			if asset, ok := assetsByID[rel.ToID]; ok {
				topo.boundDevicesByAsset[asset.ID] = appendUniqueShutdownString(topo.boundDevicesByAsset[asset.ID], rel.FromID)
				if strings.EqualFold(asset.AssetType, "CHILLER") {
					deviceToChillers[rel.FromID] = appendUniqueShutdownString(deviceToChillers[rel.FromID], asset.ID)
				}
			}
		case rel.FromType == "ASSET" && rel.ToType == "ASSET" && strings.EqualFold(rel.Role, "SERVES"):
			fromAsset, fromOK := assetsByID[rel.FromID]
			toAsset, toOK := assetsByID[rel.ToID]
			if fromOK && toOK && isShutdownHoldingAuxiliaryType(fromAsset.AssetType) && strings.EqualFold(toAsset.AssetType, "CHILLER") {
				topo.servedChillersByAux[fromAsset.ID] = appendUniqueShutdownString(topo.servedChillersByAux[fromAsset.ID], toAsset.ID)
			}
		}
	}
	return controlsByPoint, controlsSite, deviceToChillers
}

func (topo *shutdownTopology) indexAssetLocks(locks []assetControlLockView) {
	for _, lock := range locks {
		if lock.LockMode == assetControlLockUnlocked {
			continue
		}
		topo.lockedAssets[lock.AssetID] = true
		for _, devID := range lock.BoundDeviceIDs {
			topo.boundDevicesByAsset[lock.AssetID] = appendUniqueShutdownString(topo.boundDevicesByAsset[lock.AssetID], devID)
		}
	}
	for assetID, devIDs := range topo.boundDevicesByAsset {
		for _, lock := range locks {
			if lock.LockMode == assetControlLockUnlocked {
				continue
			}
			for _, devID := range devIDs {
				if containsString(lock.BoundDeviceIDs, devID) {
					topo.lockedAssets[assetID] = true
					break
				}
			}
		}
	}
}

func (topo *shutdownTopology) resolveControlTargets(
	model platformapi.SiteAssetModel,
	assetsByID map[string]platformapi.Asset,
	devicesByID map[string]platformapi.Device,
	points []platformapi.TelemetryPoint,
	feedbackByDeviceKey map[string]platformapi.TelemetryPoint,
	controlsByPoint map[string][]string,
	controlsSite map[string]bool,
	deviceToChillers map[string][]string,
) {
	var fallbackEmergencyTarget *shutdownControlTarget
	for _, point := range points {
		if !strings.EqualFold(point.Status, "ACTIVE") || point.PointType != "COMMAND" || !point.Writable ||
			point.TenantID != model.TenantID || point.SiteID != model.SiteID {
			continue
		}
		device, hasDevice := devicesByID[point.ReportingDeviceID]
		if !hasDevice {
			continue
		}
		capName, _ := point.SourceMetadata["capability"].(string)
		capability := commandmodel.Capability(strings.TrimSpace(capName))
		profile, supported := commandCapabilityProfile(capability)
		declaredRev, _ := point.SourceMetadata["capabilityRevision"].(string)
		feedbackKey, _ := point.SourceMetadata["feedbackPointKey"].(string)
		feedbackKey = strings.TrimSpace(feedbackKey)
		if !supported || strings.TrimSpace(declaredRev) != profile.Revision || feedbackKey == "" {
			continue
		}
		feedbackPoint, hasFeedback := feedbackByDeviceKey[device.ID+"\x00"+feedbackKey]
		if !hasFeedback {
			continue
		}
		interlocks, interlockErr := parseCommandInterlockChecks(point.SourceMetadata, model.TenantID, model.SiteID, model.Devices, model.TelemetryPoints)
		if interlockErr != nil {
			continue
		}
		actuationMode, _ := point.SourceMetadata["actuationMode"].(string)
		trimmedMode := strings.ToUpper(strings.TrimSpace(actuationMode))
		baseTarget := shutdownControlTarget{
			device: device, point: point, feedbackPoint: feedbackPoint,
			capability: capability, actuationMode: trimmedMode, interlocks: interlocks,
		}
		if capability == commandmodel.CapabilityStop && trimmedMode == "EMERGENCY_DO_RELEASE" {
			candidate := baseTarget
			if len(controlsByPoint[point.ID]) > 0 {
				candidate.asset = assetsByID[controlsByPoint[point.ID][0]]
			} else {
				candidate.asset = platformapi.Asset{TenantID: model.TenantID, SiteID: model.SiteID}
			}
			if controlsSite[point.ID] && topo.emergencyReleaseTarget == nil {
				topo.emergencyReleaseTarget = &candidate
			} else if fallbackEmergencyTarget == nil {
				fallbackEmergencyTarget = &candidate
			}
			continue
		}
		if (capability == commandmodel.CapabilityStop || capability == commandmodel.CapabilityStopUnitSequence) && trimmedMode == "ABORT_UNIT_SEQUENCE" {
			for _, assetID := range controlsByPoint[point.ID] {
				target := baseTarget
				target.asset = assetsByID[assetID]
				if _, exists := topo.abortSequenceByAsset[assetID]; !exists {
					topo.abortSequenceByAsset[assetID] = target
				}
			}
			continue
		}
		for _, assetID := range controlsByPoint[point.ID] {
			target := baseTarget
			target.asset = assetsByID[assetID]
			if capability == commandmodel.CapabilityStop {
				if _, exists := topo.regularStopByAsset[assetID]; !exists {
					topo.regularStopByAsset[assetID] = target
				}
				topo.statusCheckByAsset[assetID] = target
			} else if _, exists := topo.statusCheckByAsset[assetID]; !exists {
				topo.statusCheckByAsset[assetID] = target
			}
			if (capability == commandmodel.CapabilityStop || capability == commandmodel.CapabilityStopUnitSequence) && isShutdownHoldingAuxiliaryType(target.asset.AssetType) {
				for _, check := range interlocks {
					for _, chID := range deviceToChillers[check.DeviceID] {
						topo.servedChillersByAux[assetID] = appendUniqueShutdownString(topo.servedChillersByAux[assetID], chID)
					}
				}
			}
		}
	}
	if topo.emergencyReleaseTarget == nil {
		topo.emergencyReleaseTarget = fallbackEmergencyTarget
	}
}

func (topo *shutdownTopology) assignFallbackRunStateTargets(
	assetsByID map[string]platformapi.Asset,
	devicesByID map[string]platformapi.Device,
	feedbackByDeviceKey map[string]platformapi.TelemetryPoint,
) {
	for assetID, devIDs := range topo.boundDevicesByAsset {
		if _, exists := topo.statusCheckByAsset[assetID]; exists {
			continue
		}
		for _, devID := range devIDs {
			device, hasDevice := devicesByID[devID]
			if !hasDevice {
				continue
			}
			if feedbackPoint, hasFeedback := feedbackByDeviceKey[devID+"\x00run_state"]; hasFeedback {
				topo.statusCheckByAsset[assetID] = shutdownControlTarget{
					asset: assetsByID[assetID], device: device, feedbackPoint: feedbackPoint,
				}
				break
			}
		}
	}
}

func (h *handler) probeAssetRunState(
	request *http.Request,
	session bffSession,
	topo shutdownTopology,
	assetID string,
) (commandCurrentState, bool, *commandFailure) {
	target, ok := topo.statusCheckByAsset[assetID]
	if !ok {
		return commandCurrentState{}, false, nil
	}
	state, failure := h.readCommandCurrentState(request, session, target.device, target.feedbackPoint.PointCode)
	if failure != nil {
		return commandCurrentState{}, false, failure
	}
	return state, commandmodel.ScalarMatches(state.ReportedValue, commandmodel.TextScalar("STOPPED"), 0), nil
}

func buildShutdownCommandIdempotencyKey(idemBase, pointID string) string {
	cleanBase := strings.TrimSpace(idemBase)
	if cleanBase != "" && controlWriteKeyPattern.MatchString(cleanBase) {
		maxBase := 128 - 5 - len(pointID)
		if maxBase > 0 && len(cleanBase) > maxBase {
			cleanBase = cleanBase[:maxBase]
		}
		return "shd-" + cleanBase + "-" + pointID
	}
	return "shd-" + randomURLToken(16)
}

func (h *handler) dispatchShutdownTarget(
	request *http.Request,
	session bffSession,
	target shutdownControlTarget,
	currentState commandCurrentState,
	idemBase string,
) (string, *commandFailure) {
	principalID, grant, failure := h.authorizeCommand(request, session, target.device, target.capability, commandmodel.AuthorizationCommandSubmit)
	if failure != nil {
		return "", failure
	}
	prepared := preparedCommand{
		tenantID: target.device.TenantID, siteID: target.device.SiteID, deviceID: target.device.ID, pointID: target.point.ID,
		principalID: principalID, idempotencyKey: buildShutdownCommandIdempotencyKey(idemBase, target.point.ID),
		capability: target.capability, parameters: commandmodel.CommandParameters{},
		verificationPointKey: target.feedbackPoint.PointCode, currentState: currentState, grant: grant,
	}
	view, _, _, failure := h.executeCommandCreate(request.Context(), prepared)
	if failure != nil {
		return "", failure
	}
	return view.CommandID, nil
}

func (h *handler) degradedShutdownCommandState() commandCurrentState {
	return commandCurrentState{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Quality:                "GOOD",
		BusinessRevision:       1,
		ObservedAt:             h.currentGatewayTime(),
	}
}

func (h *handler) reconcileOutcomeUnknownDeviceCommands(request *http.Request, session bffSession, siteID string, target shutdownControlTarget) {
	if h.command == nil || h.command.baseURL == "" {
		return
	}
	views, listFailure := h.executeCommandList(request, session, siteID, maximumListedCommands)
	if listFailure != nil {
		return
	}
	for _, view := range views {
		if view.DeviceID != target.device.ID || view.Status != commandmodel.IntentOutcomeUnknown {
			continue
		}
		principalID, grant, authFailure := h.authorizeCommand(request, session, target.device, view.Capability, commandmodel.AuthorizationCommandSubmit)
		if authFailure != nil {
			continue
		}
		_, _ = h.executeCommandReconciliation(request.Context(), view.CommandID, internalCommandReconciliation{
			TenantID:    session.TenantID,
			SiteID:      target.device.SiteID,
			DeviceID:    target.device.ID,
			PrincipalID: principalID,
			Outcome:     commandmodel.ReconciledNotApplied,
		}, grant)
	}
}

func resolveShutdownChillerByUnitCode(topo shutdownTopology, unitCode string) (platformapi.Asset, bool) {
	normalized := strings.ToLower(unitCode)
	if chiller, found := topo.chillersByCode[normalized]; found {
		return chiller, true
	}
	if strings.HasPrefix(normalized, "unit-") {
		if chiller, found := topo.chillersByCode["chiller-"+strings.TrimPrefix(normalized, "unit-")]; found {
			return chiller, true
		}
	}
	return platformapi.Asset{}, false
}

func (h *handler) stopUnlockedChillers(
	request *http.Request,
	session bffSession,
	topo shutdownTopology,
	chillers []platformapi.Asset,
	forceStopWhenStopped bool,
	idemBase string,
) ([]string, map[string]bool, *commandFailure) {
	submitted := []string{}
	confirmedStopped := make(map[string]bool, len(chillers))
	for _, chiller := range chillers {
		state, stopped, probeFailure := h.probeAssetRunState(request, session, topo, chiller.ID)
		degradedDispatch := false
		if probeFailure != nil {
			confirmedStopped[chiller.ID] = false
			degradedDispatch = true
			state = h.degradedShutdownCommandState()
		} else {
			confirmedStopped[chiller.ID] = stopped
			if stopped && !forceStopWhenStopped {
				continue
			}
		}
		if topo.lockedAssets[chiller.ID] {
			continue
		}
		stopTarget, hasStop := topo.regularStopByAsset[chiller.ID]
		if !hasStop || (degradedDispatch && len(stopTarget.interlocks) > 0) {
			continue
		}
		if len(stopTarget.interlocks) > 0 {
			if interlockFailure := h.enforceCommandInterlocks(request, session, stopTarget.asAssetCommandTarget()); interlockFailure != nil {
				if interlockFailure.status == http.StatusServiceUnavailable || interlockFailure.status == http.StatusConflict {
					continue
				}
				return nil, nil, interlockFailure
			}
		}
		if state.ObservedAt.IsZero() {
			state = h.degradedShutdownCommandState()
		}
		cmdID, cmdFailure := h.dispatchShutdownTarget(request, session, stopTarget, state, idemBase)
		if cmdFailure != nil {
			if cmdFailure.status == http.StatusConflict {
				continue
			}
			return nil, nil, cmdFailure
		}
		submitted = append(submitted, cmdID)
	}
	return submitted, confirmedStopped, nil
}

func (h *handler) ensureChillersConfirmedStopped(
	request *http.Request,
	session bffSession,
	topo shutdownTopology,
	chillerIDs []string,
	confirmedStopped map[string]bool,
) (bool, *commandFailure) {
	if len(chillerIDs) == 0 {
		return false, nil
	}
	for _, chID := range chillerIDs {
		stopped, checked := confirmedStopped[chID]
		if !checked {
			_, probedStopped, probeFailure := h.probeAssetRunState(request, session, topo, chID)
			if probeFailure != nil {
				confirmedStopped[chID] = false
				return false, nil
			}
			stopped = probedStopped
			confirmedStopped[chID] = stopped
		}
		if !stopped {
			return false, nil
		}
	}
	return true, nil
}

func (h *handler) releaseOrDeferHoldingAuxiliaries(
	request *http.Request,
	session bffSession,
	topo shutdownTopology,
	auxAssets []platformapi.Asset,
	confirmedStopped map[string]bool,
	stopAlreadyStopped bool,
	includeLockedInDeferred bool,
	idemBase string,
) ([]string, []string, *commandFailure) {
	submitted := []string{}
	deferred := []string{}
	for _, aux := range auxAssets {
		if topo.lockedAssets[aux.ID] {
			if includeLockedInDeferred {
				_, auxStopped, probeFailure := h.probeAssetRunState(request, session, topo, aux.ID)
				if probeFailure != nil || !auxStopped {
					deferred = append(deferred, aux.ID)
				}
			}
			continue
		}
		auxState, auxStopped, probeFailure := h.probeAssetRunState(request, session, topo, aux.ID)
		if probeFailure != nil {
			deferred = append(deferred, aux.ID)
			continue
		}
		if auxStopped && !stopAlreadyStopped {
			continue
		}
		stopTarget, hasStop := topo.regularStopByAsset[aux.ID]
		if !hasStop {
			if !auxStopped {
				deferred = append(deferred, aux.ID)
			}
			continue
		}
		allServedStopped, checkFailure := h.ensureChillersConfirmedStopped(request, session, topo, topo.servedChillersByAux[aux.ID], confirmedStopped)
		if checkFailure != nil {
			return nil, nil, checkFailure
		}
		if !allServedStopped {
			if !auxStopped {
				deferred = append(deferred, aux.ID)
			}
			continue
		}
		if interlockFailure := h.enforceCommandInterlocks(request, session, stopTarget.asAssetCommandTarget()); interlockFailure != nil {
			if interlockFailure.status == http.StatusServiceUnavailable || interlockFailure.status == http.StatusConflict {
				deferred = append(deferred, aux.ID)
				continue
			}
			return nil, nil, interlockFailure
		}
		cmdID, cmdFailure := h.dispatchShutdownTarget(request, session, stopTarget, auxState, idemBase)
		if cmdFailure != nil {
			if cmdFailure.status == http.StatusConflict {
				deferred = append(deferred, aux.ID)
				continue
			}
			return nil, nil, cmdFailure
		}
		submitted = append(submitted, cmdID)
	}
	return submitted, deferred, nil
}

// postSiteControlShutdown executes the three-tier site shutdown workflow
// (PLANT_SAFE_SHUTDOWN, EMERGENCY_DO_RELEASE, or ABORT_UNIT_SEQUENCE) with
// schedule.manage authorization, 6-digit TOTP step-up, and chiller-first pump protection.
func (h *handler) postSiteControlShutdown(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The control shutdown request must use application/json.", false, nil)
		return
	}
	var input siteControlShutdownRequest
	if !decodeStrictControlJSON(writer, request, &input, maximumControlExitRequestBody) {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The Site control shutdown request payload is invalid.", false, nil)
		return
	}
	mode := strings.TrimSpace(input.Mode)
	switch mode {
	case siteControlShutdownModePlantSafe, siteControlShutdownModeEmergencyDoRelease, siteControlShutdownModeAbortUnitSequence:
	default:
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "mode must be PLANT_SAFE_SHUTDOWN, EMERGENCY_DO_RELEASE, or ABORT_UNIT_SEQUENCE.", false, nil)
		return
	}
	unitCode := strings.TrimSpace(input.UnitCode)
	if mode == siteControlShutdownModeAbortUnitSequence && unitCode == "" {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "unitCode is required when mode is ABORT_UNIT_SEQUENCE.", false, nil)
		return
	}
	if mode == siteControlShutdownModeEmergencyDoRelease && !input.ConfirmEmergencyDoRelease {
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "confirmEmergencyDoRelease must be explicitly true for EMERGENCY_DO_RELEASE.", false, nil)
		return
	}

	siteID := route.resourceID
	reason, meta, metaFailure := h.resolveControlExitMeta(request, input.Reason, input.ExpectedRevision, nil, func() (int64, *commandFailure) {
		return 0, nil
	})
	if metaFailure != nil {
		h.writeCommandFailure(writer, request, *metaFailure)
		return
	}

	auth, ok := h.authorizeSiteControlExitWrite(writer, request, session, siteID, "The authenticated Principal is not allowed to execute Site control shutdown.")
	if !ok {
		return
	}
	if stepUpFailure := h.verifySubmitStepUpTOTP(request, session, input.VerificationCode); stepUpFailure != nil {
		h.writeCommandFailure(writer, request, *stepUpFailure)
		return
	}

	assetModel, modelFailure := h.loadSiteAssetModel(request, session, siteID)
	if modelFailure != nil {
		h.writeCommandFailure(writer, request, *modelFailure)
		return
	}
	currentState, stateFailure := h.readCoreSiteControlState(request, session, siteID)
	if stateFailure != nil {
		h.writeCommandFailure(writer, request, *stateFailure)
		return
	}
	if input.ExpectedRevision == nil {
		meta.ExpectedRevision = currentState.Revision
	}
	topo := buildShutdownTopology(assetModel, currentState, h.currentGatewayTime())
	idemBase := strings.TrimSpace(request.Header.Get("Idempotency-Key"))

	if mode == siteControlShutdownModeAbortUnitSequence {
		chiller, found := resolveShutdownChillerByUnitCode(topo, unitCode)
		if !found {
			writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", fmt.Sprintf("unitCode %q did not match an active CHILLER asset on this Site.", unitCode), false, nil)
			return
		}
		var unitAux []platformapi.Asset
		deviceIDs := append([]string(nil), topo.boundDevicesByAsset[chiller.ID]...)
		for _, aux := range topo.auxiliaries {
			if containsString(topo.servedChillersByAux[aux.ID], chiller.ID) {
				unitAux = append(unitAux, aux)
				for _, devID := range topo.boundDevicesByAsset[aux.ID] {
					deviceIDs = appendUniqueShutdownString(deviceIDs, devID)
				}
			}
		}
		cancelledIDs := []string{}
		if len(deviceIDs) > 0 {
			sort.Strings(deviceIDs)
			cancelled, cancelFailure := h.executeCommandCancelPending(request.Context(), session, siteID, deviceIDs, auth.principalID, reason, "", true)
			if cancelFailure != nil {
				h.writeCommandFailure(writer, request, *cancelFailure)
				return
			}
			if len(cancelled) > 0 {
				cancelledIDs = cancelled
			}
		}
		submittedIDs := []string{}
		abortTarget, hasAbortTarget := topo.abortSequenceByAsset[chiller.ID]
		if hasAbortTarget && !topo.lockedAssets[chiller.ID] {
			abortState, _, probeErr := h.probeAssetRunState(request, session, topo, chiller.ID)
			if probeErr != nil || abortState.ObservedAt.IsZero() {
				abortState = h.degradedShutdownCommandState()
			}
			abortID, abortFailure := h.dispatchShutdownTarget(request, session, abortTarget, abortState, idemBase)
			if abortFailure != nil && abortFailure.status != http.StatusConflict {
				h.writeCommandFailure(writer, request, *abortFailure)
				return
			}
			if abortFailure == nil {
				submittedIDs = append(submittedIDs, abortID)
			}
		}
		chillerSubmitted, confirmedStopped, chillerFailure := h.stopUnlockedChillers(request, session, topo, []platformapi.Asset{chiller}, !hasAbortTarget, idemBase)
		if chillerFailure != nil {
			h.writeCommandFailure(writer, request, *chillerFailure)
			return
		}
		submittedIDs = append(submittedIDs, chillerSubmitted...)
		auxSubmitted, deferredIDs, auxFailure := h.releaseOrDeferHoldingAuxiliaries(request, session, topo, unitAux, confirmedStopped, true, true, idemBase)
		if auxFailure != nil {
			h.writeCommandFailure(writer, request, *auxFailure)
			return
		}
		submittedIDs = append(submittedIDs, auxSubmitted...)
		if len(cancelledIDs) == 0 && len(submittedIDs) == 0 && (topo.lockedAssets[chiller.ID] || len(deferredIDs) > 0) {
			writeProblem(writer, request, http.StatusConflict, "CONTROL_SHUTDOWN_LOCKED", "Control shutdown locked", "Unable to abort or stop the requested unit sequence because target assets are locked or waiting on active chillers.", false, nil)
			return
		}
		writer.Header().Set("Cache-Control", "private, no-store")
		writeJSON(writer, http.StatusOK, siteControlShutdownResponse{
			SiteControlState:        currentState,
			Mode:                    mode,
			CancelledCommandIDs:     cancelledIDs,
			SubmittedCommandIDs:     submittedIDs,
			DeferredHoldingAssetIds: deferredIDs,
		})
		return
	}

	if mode == siteControlShutdownModeEmergencyDoRelease && topo.emergencyReleaseTarget == nil {
		writeProblem(writer, request, http.StatusConflict, "EMERGENCY_DO_RELEASE_UNAVAILABLE", "Emergency DO release unavailable", "This Site has no active EMERGENCY_DO_RELEASE command point.", false, nil)
		return
	}

	payload := map[string]any{
		"controlMode": siteControlModeSafeShutdownExit,
		"reason":      reason,
		"meta":        meta,
	}
	savedState, cancelledIDs, ok := h.saveSiteControlModeAndCancelPending(writer, request, session, route, auth, siteID, payload, reason, true)
	if !ok {
		return
	}
	topo = buildShutdownTopology(assetModel, savedState, h.currentGatewayTime())

	submittedIDs := []string{}
	deferredIDs := []string{}
	if mode == siteControlShutdownModeEmergencyDoRelease {
		target := *topo.emergencyReleaseTarget
		h.reconcileOutcomeUnknownDeviceCommands(request, session, siteID, target)
		current, stateReadFailure := h.readCommandCurrentState(request, session, target.device, target.feedbackPoint.PointCode)
		if stateReadFailure != nil {
			current = h.degradedShutdownCommandState()
		}
		cmdID, cmdFailure := h.dispatchShutdownTarget(request, session, target, current, idemBase)
		if cmdFailure != nil {
			h.writeCommandFailure(writer, request, *cmdFailure)
			return
		}
		submittedIDs = append(submittedIDs, cmdID)
		alarmEvent := alarmservice.ControlEventRequest{
			SchemaVersion: 1,
			TenantID:      session.TenantID,
			SiteID:        siteID,
			CommandID:     cmdID,
			EventKind:     alarmservice.ControlEventEmergencyDORelease,
			Capability:    string(target.capability),
			ReasonCode:    alarmservice.ControlEventEmergencyDORelease,
			Detail:        reason,
			OccurredAt:    h.currentGatewayTime().UTC().Format(time.RFC3339Nano),
			ActorType:     "PRINCIPAL",
			ActorID:       auth.principalID,
			CorrelationID: cmdID,
		}
		if alarmmodel.IsUUIDv7(target.device.ID) {
			devID := target.device.ID
			alarmEvent.DeviceID = &devID
		}
		if alarmmodel.IsUUIDv7(target.point.ID) {
			ptID := target.point.ID
			alarmEvent.PointID = &ptID
		}
		h.emitControlFailureAlarmAsync(alarmEvent)
	} else {
		chillerSubmitted, confirmedStopped, chillerFailure := h.stopUnlockedChillers(request, session, topo, topo.chillers, false, idemBase)
		if chillerFailure != nil {
			h.writeCommandFailure(writer, request, *chillerFailure)
			return
		}
		submittedIDs = append(submittedIDs, chillerSubmitted...)
		auxSubmitted, auxDeferred, auxFailure := h.releaseOrDeferHoldingAuxiliaries(request, session, topo, topo.auxiliaries, confirmedStopped, false, false, idemBase)
		if auxFailure != nil {
			h.writeCommandFailure(writer, request, *auxFailure)
			return
		}
		submittedIDs = append(submittedIDs, auxSubmitted...)
		deferredIDs = append(deferredIDs, auxDeferred...)
	}

	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, siteControlShutdownResponse{
		SiteControlState:        savedState,
		Mode:                    mode,
		CancelledCommandIDs:     cancelledIDs,
		SubmittedCommandIDs:     submittedIDs,
		DeferredHoldingAssetIds: deferredIDs,
	})
}

const safeShutdownAuxCoastDown = 60 * time.Second

func (h *handler) releaseSafeShutdownHoldingAuxiliariesBestEffort(
	request *http.Request,
	session bffSession,
	siteID string,
	state siteControlStateView,
) {
	if h == nil || h.command == nil || h.command.baseURL == "" || h.telemetry == nil {
		return
	}
	assetModel, modelFailure := h.loadSiteAssetModel(request, session, siteID)
	if modelFailure != nil {
		return
	}
	now := h.currentGatewayTime()
	topo := buildShutdownTopology(assetModel, state, now)
	if len(topo.auxiliaries) == 0 {
		return
	}
	listedCommands, _ := h.executeCommandList(request, session, siteID, maximumListedCommands)

	var safeShutdownEnteredAt time.Time
	if state.UpdatedAt != nil {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*state.UpdatedAt)); err == nil {
			safeShutdownEnteredAt = parsed.UTC()
		}
	}

	chillerReady := make(map[string]bool, len(topo.chillers))
	isServedChillerCoastDownComplete := func(chID string) bool {
		if ready, ok := chillerReady[chID]; ok {
			return ready
		}
		chState, stopped, probeFailure := h.probeAssetRunState(request, session, topo, chID)
		if probeFailure != nil || !stopped {
			chillerReady[chID] = false
			return false
		}
		latestStopSucceededAt, hasStopSucceeded, hasInFlightStop := latestDeviceStopStatus(topo.boundDevicesByAsset[chID], listedCommands)
		if hasInFlightStop {
			chillerReady[chID] = false
			return false
		}
		if hasStopSucceeded && now.Sub(latestStopSucceededAt) < safeShutdownAuxCoastDown {
			chillerReady[chID] = false
			return false
		}
		ready := (hasStopSucceeded && now.Sub(latestStopSucceededAt) >= safeShutdownAuxCoastDown) ||
			(!chState.ObservedAt.IsZero() && now.Sub(chState.ObservedAt) >= safeShutdownAuxCoastDown) ||
			(!safeShutdownEnteredAt.IsZero() && now.Sub(safeShutdownEnteredAt) >= safeShutdownAuxCoastDown)
		chillerReady[chID] = ready
		return ready
	}

	for _, aux := range topo.auxiliaries {
		if topo.lockedAssets[aux.ID] {
			continue
		}
		stopTarget, hasStop := topo.regularStopByAsset[aux.ID]
		if !hasStop {
			continue
		}
		auxState, auxStopped, probeFailure := h.probeAssetRunState(request, session, topo, aux.ID)
		if probeFailure != nil || auxStopped {
			continue
		}
		auxDevices := append([]string{stopTarget.device.ID}, topo.boundDevicesByAsset[aux.ID]...)
		if auxStopAt, hasAuxSucceeded, hasAuxInFlight := latestDeviceStopStatus(auxDevices, listedCommands); hasAuxInFlight || (hasAuxSucceeded && now.Sub(auxStopAt) < safeShutdownAuxCoastDown) {
			continue
		}
		served := topo.servedChillersByAux[aux.ID]
		if len(served) == 0 {
			continue
		}
		allReady := true
		for _, chID := range served {
			if !isServedChillerCoastDownComplete(chID) {
				allReady = false
				break
			}
		}
		if !allReady {
			continue
		}
		if interlockFailure := h.enforceCommandInterlocks(request, session, stopTarget.asAssetCommandTarget()); interlockFailure != nil {
			continue
		}
		_, _ = h.dispatchShutdownTarget(request, session, stopTarget, auxState, "")
	}
}

func latestDeviceStopStatus(deviceIDs []string, commands []commandView) (time.Time, bool, bool) {
	var latestSucceeded time.Time
	hasSucceeded := false
	hasInFlight := false
	for _, cmd := range commands {
		if !containsString(deviceIDs, cmd.DeviceID) {
			continue
		}
		if cmd.Capability != commandmodel.CapabilityStop && cmd.Capability != commandmodel.CapabilityStopUnitSequence {
			continue
		}
		switch cmd.Status {
		case commandmodel.IntentSubmitted, commandmodel.IntentValidating, commandmodel.IntentAwaitingApproval,
			commandmodel.IntentApproved, commandmodel.IntentQueued, commandmodel.IntentDispatching, commandmodel.IntentOutcomeUnknown:
			hasInFlight = true
		case commandmodel.IntentSucceeded:
			if !hasSucceeded || cmd.UpdatedAt.After(latestSucceeded) {
				latestSucceeded = cmd.UpdatedAt
				hasSucceeded = true
			}
		}
	}
	return latestSucceeded, hasSucceeded, hasInFlight
}
