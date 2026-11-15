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
	"strings"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	siteControlModeRemoteAuto       = "REMOTE_AUTO"
	siteControlModeManualOverride   = "MANUAL_OVERRIDE"
	siteControlModeSafeShutdownExit = "SAFE_SHUTDOWN_EXIT"

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

type assetControlLockRequest struct {
	LockMode         string               `json:"lockMode"`
	Reason           string               `json:"reason,omitempty"`
	ExpectedRevision *int64               `json:"expectedRevision,omitempty"`
	Meta             *controlRegistryMeta `json:"meta,omitempty"`
}

type internalCommandCancelPendingRequest struct {
	TenantID    string   `json:"tenantId"`
	SiteID      string   `json:"siteId"`
	DeviceIDs   []string `json:"deviceIds,omitempty"`
	PrincipalID string   `json:"principalId"`
	Reason      string   `json:"reason,omitempty"`
	CausationID string   `json:"causationId,omitempty"`
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
	status, raw, coreFailure := h.callCoreControlRegistry(request.Context(), http.MethodPut, route.internalPath, auth.coreGrant, payload, routeRevisionFromRequest(request))
	if coreFailure != nil {
		h.writeCommandFailure(writer, request, *coreFailure)
		return
	}
	if status != http.StatusOK && status != http.StatusCreated {
		h.writeCoreControlRegistryError(writer, request, status, raw)
		return
	}
	var saved siteControlStateView
	if json.Unmarshal(raw, &saved) != nil || saved.SiteID != siteID {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "Core Registry returned an invalid Site control state.", true, nil)
		return
	}

	cancelledIDs := []string{}
	if saved.ControlMode != siteControlModeRemoteAuto {
		var causationID string
		if saved.ID != nil {
			causationID = *saved.ID
		}
		cancelled, cancelFailure := h.executeCommandCancelPending(request.Context(), session, siteID, nil, auth.principalID, reason, causationID)
		if cancelFailure != nil {
			h.writeCommandFailure(writer, request, *cancelFailure)
			return
		}
		if len(cancelled) > 0 {
			cancelledIDs = cancelled
		}
	}

	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, siteControlModeMutationResponse{
		siteControlStateView: saved,
		CancelledCommandIDs:  cancelledIDs,
	})
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
		cancelled, cancelFailure := h.executeCommandCancelPending(request.Context(), session, saved.SiteID, saved.BoundDeviceIDs, auth.principalID, reason, saved.ID)
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

func (h *handler) enforceControlLockForCommand(request *http.Request, session bffSession, siteID, assetID, deviceID string, capability commandmodel.Capability) *commandFailure {
	if h.registry == nil || h.registry.coreBaseURL == "" || h.registry.coreHTTPClient == nil {
		failure := commandUnavailable("Core Registry is unavailable for Command lock enforcement.")
		return &failure
	}
	state, failure := h.readCoreSiteControlState(request, session, siteID)
	if failure != nil {
		return failure
	}
	if state.ControlMode != siteControlModeRemoteAuto {
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

func (h *handler) executeCommandCancelPending(ctx context.Context, session bffSession, siteID string, deviceIDs []string, principalID, reason, causationID string) ([]string, *commandFailure) {
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
		TenantID:    session.TenantID,
		SiteID:      siteID,
		DeviceIDs:   deviceIDs,
		PrincipalID: principalID,
		Reason:      reason,
		CausationID: causationID,
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
