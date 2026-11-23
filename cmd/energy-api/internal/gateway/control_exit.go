package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
	"github.com/quanlaihe/hvac-web/modules/iam/pkg/iamserver"
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

var controlWriteKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}\z`)

type publicControlExitRoute struct {
	template       string
	kind           string
	resourceID     string
	secondaryID    string
	internalPath   string
	method         string
	allowedMethods []string
}

type assetControlLockView = commandservice.AssetControlLockView
type activeMaintenanceWindowView = commandservice.ActiveMaintenanceWindowView
type devicePriorityLockView = commandservice.DevicePriorityLockView
type devicePriorityLockInput = commandservice.DevicePriorityLockInput
type devicePriorityLockReleaseResponse = commandservice.DevicePriorityLockReleaseResponse
type controlAlarmDeadLetterView = commandservice.ControlAlarmDeadLetterView
type controlAlarmDeadLetterReplayResponse = commandservice.ControlAlarmDeadLetterReplayResponse
type siteControlStateView = commandservice.SiteControlStateView
type siteControlModeMutationResponse = commandservice.SiteControlModeResponse
type assetControlLockMutationResponse = commandservice.AssetControlLockResponse
type controlRegistryMeta = commandservice.ControlMutationMeta
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
				case "command-batches":
					return publicControlExitRoute{
						template:       "/api/v1/sites/{siteId}/command-batches",
						kind:           "site-command-batches",
						resourceID:     siteID,
						internalPath:   internalOperatorPrefix + "/sites/" + url.PathEscape(siteID) + "/command-batches",
						allowedMethods: []string{http.MethodGet, http.MethodPost},
					}, true
				}
			}
		} else if len(segments) == 3 && segments[1] == "command-batches" {
			siteID, siteErr := url.PathUnescape(segments[0])
			batchID, batchErr := url.PathUnescape(segments[2])
			if siteErr == nil && batchErr == nil && siteID != "" && batchID != "" {
				return publicControlExitRoute{
					template:     "/api/v1/sites/{siteId}/command-batches/{batchId}",
					kind:         "site-command-batch",
					resourceID:   siteID,
					secondaryID:  batchID,
					internalPath: internalOperatorPrefix + "/sites/" + url.PathEscape(siteID) + "/command-batches/" + url.PathEscape(batchID),
					method:       http.MethodGet,
				}, true
			}
		} else if len(segments) == 4 && segments[1] == "command-batches" && segments[3] == "cancel" {
			siteID, siteErr := url.PathUnescape(segments[0])
			batchID, batchErr := url.PathUnescape(segments[2])
			if siteErr == nil && batchErr == nil && siteID != "" && batchID != "" {
				return publicControlExitRoute{
					template:     "/api/v1/sites/{siteId}/command-batches/{batchId}/cancel",
					kind:         "site-command-batch-cancel",
					resourceID:   siteID,
					secondaryID:  batchID,
					internalPath: internalOperatorPrefix + "/sites/" + url.PathEscape(siteID) + "/command-batches/" + url.PathEscape(batchID) + "/cancel",
					method:       http.MethodPost,
				}, true
			}
		} else if len(segments) == 4 && segments[1] == "devices" && segments[3] == "priority-lock" {
			siteID, siteErr := url.PathUnescape(segments[0])
			deviceID, devErr := url.PathUnescape(segments[2])
			if siteErr == nil && devErr == nil && siteID != "" && deviceID != "" {
				return publicControlExitRoute{
					template:       "/api/v1/sites/{siteId}/devices/{deviceId}/priority-lock",
					kind:           "site-device-priority-lock",
					resourceID:     siteID,
					secondaryID:    deviceID,
					internalPath:   internalOperatorPrefix + "/sites/" + url.PathEscape(siteID) + "/devices/" + url.PathEscape(deviceID) + "/priority-lock",
					allowedMethods: []string{http.MethodPut, http.MethodDelete},
				}, true
			}
		} else if len(segments) == 3 && segments[1] == "control-alarms" && segments[2] == "dead-letters" {
			siteID, siteErr := url.PathUnescape(segments[0])
			if siteErr == nil && siteID != "" {
				return publicControlExitRoute{
					template:     "/api/v1/sites/{siteId}/control-alarms/dead-letters",
					kind:         "site-control-alarm-dead-letters",
					resourceID:   siteID,
					internalPath: internalOperatorPrefix + "/sites/" + url.PathEscape(siteID) + "/control-alarms/dead-letters",
					method:       http.MethodGet,
				}, true
			}
		} else if len(segments) == 5 && segments[1] == "control-alarms" && segments[2] == "dead-letters" && segments[4] == "replay" {
			siteID, siteErr := url.PathUnescape(segments[0])
			outboxID, outErr := url.PathUnescape(segments[3])
			if siteErr == nil && outErr == nil && siteID != "" && outboxID != "" {
				return publicControlExitRoute{
					template:     "/api/v1/sites/{siteId}/control-alarms/dead-letters/{outboxId}/replay",
					kind:         "site-control-alarm-dead-letter-replay",
					resourceID:   siteID,
					secondaryID:  outboxID,
					internalPath: internalOperatorPrefix + "/sites/" + url.PathEscape(siteID) + "/control-alarms/dead-letters/" + url.PathEscape(outboxID) + "/replay",
					method:       http.MethodPost,
				}, true
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

func controlExitMethodAllowed(route publicControlExitRoute, method string) (string, bool) {
	if len(route.allowedMethods) > 0 {
		for _, candidate := range route.allowedMethods {
			if method == candidate {
				return "", true
			}
		}
		return strings.Join(route.allowedMethods, ", "), false
	}
	if method == route.method {
		return "", true
	}
	return route.method, false
}

func dispatchControlExitRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicControlExitRoute) {
	if allowHeader, ok := controlExitMethodAllowed(route, request.Method); !ok {
		writeMethodNotAllowedFor(writer, request, allowHeader)
		return
	}
	requireWrite := request.Method != http.MethodGet
	session, ok := h.commandSession(writer, request, requireWrite)
	if !ok {
		return
	}
	if requireWrite && !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
		return
	}
	validSecondaryID := route.secondaryID == "" || isLowerUUIDv7(route.secondaryID)
	if (route.kind == "site-command-batch" || route.kind == "site-command-batch-cancel") && route.secondaryID != "" {
		validSecondaryID = isLowerUUID(route.secondaryID)
	}
	if !isLowerUUIDv7(route.resourceID) || !validSecondaryID {
		if requireWrite {
			targetID, targetLabel, action := controlExitRouteAuditMeta(route, request.Method)
			h.emitControlExitAudit(request, session, targetID, targetLabel, action, auditOutcomeRejected, map[string]any{"reasonCode": "RESOURCE_NOT_FOUND"})
		}
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site, Device, or Control Alarm dead-letter entry was not found.", false, nil)
		return
	}
	if h.command == nil || h.command.baseURL == "" || h.command.httpClient == nil {
		if requireWrite {
			targetID, targetLabel, action := controlExitRouteAuditMeta(route, request.Method)
			h.emitControlExitAudit(request, session, targetID, targetLabel, action, auditOutcomeFailed, map[string]any{"reasonCode": "COMMAND_UNAVAILABLE"})
		}
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
	case "site-device-priority-lock":
		if request.Method == http.MethodDelete {
			h.deleteDevicePriorityLock(writer, request, session, route)
			return
		}
		h.putDevicePriorityLock(writer, request, session, route)
	case "site-control-alarm-dead-letters":
		h.listControlAlarmDeadLetters(writer, request, session, route)
	case "site-control-alarm-dead-letter-replay":
		h.replayControlAlarmDeadLetter(writer, request, session, route)
	case "site-command-batches":
		if request.Method == http.MethodPost {
			h.createSiteCommandBatch(writer, request, session, route)
			return
		}
		h.listSiteCommandBatches(writer, request, session, route)
	case "site-command-batch":
		h.getSiteCommandBatch(writer, request, session, route)
	case "site-command-batch-cancel":
		h.cancelSiteCommandBatch(writer, request, session, route)
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
	h.populateSiteControlStateNames(request.Context(), session, "", &state)
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, state)
}

func (h *handler) putSiteControlMode(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	targetID := "site:" + route.resourceID
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		h.emitControlExitAudit(request, session, targetID, "站点控制模式", "SITE_CONTROL_MODE_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The control mode request must use application/json.", false, nil)
		return
	}
	var input siteControlModeRequest
	if !decodeStrictControlJSON(writer, request, &input, maximumControlExitRequestBody) {
		h.emitControlExitAudit(request, session, targetID, "站点控制模式", "SITE_CONTROL_MODE_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The Site control mode request payload is invalid.", false, nil)
		return
	}
	if input.ControlMode != siteControlModeRemoteAuto && input.ControlMode != siteControlModeManualOverride && input.ControlMode != siteControlModeSafeShutdownExit {
		h.emitControlExitAudit(request, session, targetID, "站点控制模式", "SITE_CONTROL_MODE_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "controlMode must be REMOTE_AUTO, MANUAL_OVERRIDE, or SAFE_SHUTDOWN_EXIT.", false, nil)
		return
	}
	targetLabel := siteControlModeTargetLabel(input.ControlMode)

	var extraHeaders map[string]string
	if idem := strings.TrimSpace(request.Header.Get("Idempotency-Key")); idem != "" {
		extraHeaders = map[string]string{"Idempotency-Key": idem}
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPut, route.internalPath, input, extraHeaders, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "SITE_CONTROL_MODE_UPDATED", auditOutcomeForStatus(failure.status), map[string]any{"afterMode": input.ControlMode, "reasonCode": failure.code})
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "SITE_CONTROL_MODE_UPDATED", auditOutcomeForStatus(status), map[string]any{"afterMode": input.ControlMode, "reasonCode": problemCodeFromRaw(rawBody)})
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var resp siteControlModeMutationResponse
	if json.Unmarshal(rawBody, &resp) != nil || resp.TenantID != session.TenantID || resp.SiteID != route.resourceID {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "SITE_CONTROL_MODE_UPDATED", auditOutcomeFailed, map[string]any{"afterMode": input.ControlMode, "reasonCode": "COMMAND_RESPONSE_INVALID"})
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Site control mode response."))
		return
	}
	h.emitControlExitAudit(request, session, targetID, siteControlModeTargetLabel(resp.ControlMode), "SITE_CONTROL_MODE_UPDATED", auditOutcomeSucceeded, map[string]any{
		"afterMode":      resp.ControlMode,
		"reason":         resp.Reason,
		"revision":       resp.Revision,
		"cancelledCount": len(resp.CancelledCommandIDs),
	})
	h.populateSiteControlStateNames(request.Context(), session, derefOptionalString(resp.UpdatedBy), &resp.SiteControlStateView)
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, resp)
}

func (h *handler) putAssetControlLock(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	targetID := "asset:" + route.resourceID
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		h.emitControlExitAudit(request, session, targetID, "设备控制锁定", "ASSET_CONTROL_LOCK_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTROL_EXIT_REQUEST_INVALID", "Control lock request invalid", "The Asset control lock request must use application/json.", false, nil)
		return
	}
	var input assetControlLockRequest
	if !decodeStrictControlJSON(writer, request, &input, maximumControlExitRequestBody) {
		h.emitControlExitAudit(request, session, targetID, "设备控制锁定", "ASSET_CONTROL_LOCK_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control lock request invalid", "The Asset control lock request payload is invalid.", false, nil)
		return
	}
	if input.LockMode != assetControlLockUnlocked && input.LockMode != assetControlLockManual && input.LockMode != assetControlLockMaintenance {
		h.emitControlExitAudit(request, session, targetID, "设备控制锁定", "ASSET_CONTROL_LOCK_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control lock request invalid", "lockMode must be UNLOCKED, MANUAL_LOCK, or MAINTENANCE_LOCK.", false, nil)
		return
	}
	targetLabel := assetControlLockTargetLabel(input.LockMode)

	var extraHeaders map[string]string
	if idem := strings.TrimSpace(request.Header.Get("Idempotency-Key")); idem != "" {
		extraHeaders = map[string]string{"Idempotency-Key": idem}
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPut, route.internalPath, input, extraHeaders, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "ASSET_CONTROL_LOCK_UPDATED", auditOutcomeForStatus(failure.status), map[string]any{"afterLockMode": input.LockMode, "reasonCode": failure.code})
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "ASSET_CONTROL_LOCK_UPDATED", auditOutcomeForStatus(status), map[string]any{"afterLockMode": input.LockMode, "reasonCode": problemCodeFromRaw(rawBody)})
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var resp assetControlLockMutationResponse
	if json.Unmarshal(rawBody, &resp) != nil || resp.AssetID != route.resourceID || (resp.TenantID != "" && resp.TenantID != session.TenantID) {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "ASSET_CONTROL_LOCK_UPDATED", auditOutcomeFailed, map[string]any{"afterLockMode": input.LockMode, "reasonCode": "COMMAND_RESPONSE_INVALID"})
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Asset control lock response."))
		return
	}
	h.emitControlExitAudit(request, session, targetID, assetControlLockTargetLabel(resp.LockMode), "ASSET_CONTROL_LOCK_UPDATED", auditOutcomeSucceeded, map[string]any{
		"afterLockMode":  resp.LockMode,
		"reason":         resp.Reason,
		"revision":       resp.Revision,
		"cancelledCount": len(resp.CancelledCommandIDs),
	})
	h.populateAssetControlLockName(request.Context(), session, derefOptionalString(resp.UpdatedBy), &resp.AssetControlLockView)
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, resp)
}

func (h *handler) postSiteControlShutdown(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	targetID := "site:" + route.resourceID
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		h.emitControlExitAudit(request, session, targetID, "紧急停机控制", "SITE_CONTROL_SHUTDOWN", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The Site control shutdown request must use application/json.", false, nil)
		return
	}
	var input siteControlShutdownRequest
	if !decodeStrictControlJSON(writer, request, &input, maximumControlExitRequestBody) {
		h.emitControlExitAudit(request, session, targetID, "紧急停机控制", "SITE_CONTROL_SHUTDOWN", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "The Site control shutdown request payload is invalid.", false, nil)
		return
	}
	switch input.Mode {
	case siteControlShutdownModePlantSafe, siteControlShutdownModeEmergencyDoRelease, siteControlShutdownModeAbortUnitSequence:
	default:
		h.emitControlExitAudit(request, session, targetID, "紧急停机控制", "SITE_CONTROL_SHUTDOWN", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "mode must be PLANT_SAFE_SHUTDOWN, EMERGENCY_DO_RELEASE, or ABORT_UNIT_SEQUENCE.", false, nil)
		return
	}
	targetLabel := siteControlShutdownTargetLabel(input.Mode)
	if strings.TrimSpace(input.Reason) == "" {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "SITE_CONTROL_SHUTDOWN", auditOutcomeRejected, map[string]any{"mode": input.Mode, "reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "A non-empty reason is required.", false, nil)
		return
	}
	if input.Mode == siteControlShutdownModeAbortUnitSequence && strings.TrimSpace(input.UnitCode) == "" {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "SITE_CONTROL_SHUTDOWN", auditOutcomeRejected, map[string]any{"mode": input.Mode, "reason": input.Reason, "reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "unitCode is required when mode is ABORT_UNIT_SEQUENCE.", false, nil)
		return
	}
	if input.Mode == siteControlShutdownModeEmergencyDoRelease && !input.ConfirmEmergencyDoRelease {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "SITE_CONTROL_SHUTDOWN", auditOutcomeRejected, map[string]any{"mode": input.Mode, "reason": input.Reason, "reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control exit request invalid", "confirmEmergencyDoRelease must be explicitly true for EMERGENCY_DO_RELEASE.", false, nil)
		return
	}

	var extraActions []string
	if strings.TrimSpace(input.VerificationCode) != "" {
		if stepUpFailure := h.verifySubmitStepUpTOTP(request, session, input.VerificationCode); stepUpFailure != nil {
			h.emitControlExitAudit(request, session, targetID, targetLabel, "SITE_CONTROL_SHUTDOWN", auditOutcomeDenied, map[string]any{"mode": input.Mode, "reason": input.Reason, "stepUpRequired": true, "reasonCode": stepUpFailure.code})
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
		h.emitControlExitAudit(request, session, targetID, targetLabel, "SITE_CONTROL_SHUTDOWN", auditOutcomeForStatus(failure.status), map[string]any{"mode": input.Mode, "reason": input.Reason, "reasonCode": failure.code})
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "SITE_CONTROL_SHUTDOWN", auditOutcomeForStatus(status), map[string]any{"mode": input.Mode, "reason": input.Reason, "reasonCode": problemCodeFromRaw(rawBody)})
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var resp siteControlShutdownResponse
	if json.Unmarshal(rawBody, &resp) != nil ||
		resp.SiteControlState.TenantID != session.TenantID || resp.SiteControlState.SiteID != route.resourceID {
		h.emitControlExitAudit(request, session, targetID, targetLabel, "SITE_CONTROL_SHUTDOWN", auditOutcomeFailed, map[string]any{"mode": input.Mode, "reason": input.Reason, "unitCode": input.UnitCode, "reasonCode": "COMMAND_RESPONSE_INVALID"})
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Site control shutdown response."))
		return
	}
	h.emitControlExitAudit(request, session, targetID, siteControlShutdownTargetLabel(resp.Mode), "SITE_CONTROL_SHUTDOWN", auditOutcomeSucceeded, map[string]any{
		"mode":           resp.Mode,
		"afterMode":      resp.SiteControlState.ControlMode,
		"reason":         input.Reason,
		"unitCode":       input.UnitCode,
		"cancelledCount": len(resp.CancelledCommandIDs),
		"submittedCount": len(resp.SubmittedCommandIDs),
	})
	h.populateSiteControlStateNames(request.Context(), session, derefOptionalString(resp.SiteControlState.UpdatedBy), &resp.SiteControlState)
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, resp)
}

func (h *handler) putDevicePriorityLock(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	targetID := "device:" + route.secondaryID
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTROL_EXIT_REQUEST_INVALID", "Device priority lock invalid", "The Device priority lock request must use application/json.", false, nil)
		return
	}
	var input devicePriorityLockInput
	if !decodeStrictControlJSON(writer, request, &input, maximumControlExitRequestBody) {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Device priority lock invalid", "The Device priority lock request payload is invalid.", false, nil)
		return
	}
	switch commandmodel.PriorityTier(input.LockedPriority) {
	case commandmodel.PriorityManualOverride, commandmodel.PriorityProtectionRule, commandmodel.PrioritySafetyInterlock:
	default:
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Device priority lock invalid", "lockedPriority must be MANUAL_OVERRIDE, PROTECTION_RULE, or SAFETY_INTERLOCK.", false, nil)
		return
	}
	if input.DurationMinutes != nil && (*input.DurationMinutes < 5 || *input.DurationMinutes > 1440) {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Device priority lock invalid", "durationMinutes must be between 5 and 1440 minutes.", false, nil)
		return
	}
	if strings.TrimSpace(input.Reason) == "" || utf8.RuneCountInString(strings.TrimSpace(input.Reason)) > 1000 {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_UPDATED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Device priority lock invalid", "A non-empty reason up to 1000 characters is required.", false, nil)
		return
	}

	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPut, route.internalPath, input, nil, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_UPDATED", auditOutcomeForStatus(failure.status), map[string]any{"siteId": route.resourceID, "lockedPriority": input.LockedPriority, "reasonCode": failure.code})
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_UPDATED", auditOutcomeForStatus(status), map[string]any{"siteId": route.resourceID, "lockedPriority": input.LockedPriority, "reasonCode": problemCodeFromRaw(rawBody)})
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var resp devicePriorityLockView
	if json.Unmarshal(rawBody, &resp) != nil || resp.DeviceID != route.secondaryID {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_UPDATED", auditOutcomeFailed, map[string]any{"siteId": route.resourceID, "reasonCode": "COMMAND_RESPONSE_INVALID"})
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Device priority lock response."))
		return
	}
	h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_UPDATED", auditOutcomeSucceeded, map[string]any{
		"siteId":         route.resourceID,
		"lockedPriority": resp.LockedPriority,
		"lockedUntil":    resp.LockedUntil,
		"reason":         resp.Reason,
	})
	if strings.TrimSpace(resp.LockedBy) != "" {
		names := h.lookupTenantPrincipalNames(request.Context(), session, resp.LockedBy, []string{resp.LockedBy})
		resp.LockedByName = resolvedStringName(resp.LockedBy, names)
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, resp)
}

func (h *handler) deleteDevicePriorityLock(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	targetID := "device:" + route.secondaryID
	var input struct {
		Reason string `json:"reason"`
	}
	hasQuery := len(request.URL.Query()) > 0
	if hasQuery {
		for key, values := range request.URL.Query() {
			if key != "reason" || len(values) != 1 {
				h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_RELEASED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
				writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Device priority lock release invalid", "Only a single reason query parameter is accepted.", false, nil)
				return
			}
		}
		input.Reason = request.URL.Query().Get("reason")
	} else if request.Body != nil && request.ContentLength != 0 {
		if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "" && mediaType != "application/json" {
			h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_RELEASED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
			writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTROL_EXIT_REQUEST_INVALID", "Device priority lock release invalid", "The release request body must use application/json.", false, nil)
			return
		}
		if !decodeStrictControlJSON(writer, request, &input, maximumControlExitRequestBody) {
			h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_RELEASED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
			writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Device priority lock release invalid", "The release request payload is invalid.", false, nil)
			return
		}
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > 1000 {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_RELEASED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Device priority lock release invalid", "A non-empty reason up to 1000 characters is required.", false, nil)
		return
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodDelete, route.internalPath, map[string]string{"reason": reason}, nil, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_RELEASED", auditOutcomeForStatus(failure.status), map[string]any{"siteId": route.resourceID, "reasonCode": failure.code})
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_RELEASED", auditOutcomeForStatus(status), map[string]any{"siteId": route.resourceID, "reasonCode": problemCodeFromRaw(rawBody)})
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var resp devicePriorityLockReleaseResponse
	if json.Unmarshal(rawBody, &resp) != nil || resp.DeviceID != route.secondaryID {
		h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_RELEASED", auditOutcomeFailed, map[string]any{"siteId": route.resourceID, "reasonCode": "COMMAND_RESPONSE_INVALID"})
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Device priority lock release response."))
		return
	}
	h.emitControlExitAudit(request, session, targetID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_RELEASED", auditOutcomeSucceeded, map[string]any{
		"siteId":   route.resourceID,
		"released": resp.Released,
		"reason":   reason,
	})
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, resp)
}

func (h *handler) listControlAlarmDeadLetters(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "limit" || len(values) != 1 {
			writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Invalid query", "Only a single limit query parameter is supported.", false, nil)
			return
		}
	}
	internalPath := route.internalPath
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > commandservice.MaximumControlAlarmDeadLetters {
			writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Invalid query", "limit must be between 1 and 50.", false, nil)
			return
		}
		internalPath += "?limit=" + strconv.Itoa(limit)
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodGet, internalPath, nil, nil, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var payload struct {
		Data []controlAlarmDeadLetterView `json:"data"`
	}
	if json.Unmarshal(rawBody, &payload) != nil {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Control Alarm dead-letter list."))
		return
	}
	if payload.Data == nil {
		payload.Data = []controlAlarmDeadLetterView{}
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": payload.Data,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func (h *handler) replayControlAlarmDeadLetter(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	targetID := "outbox:" + route.secondaryID
	if len(request.URL.Query()) != 0 {
		h.emitControlExitAudit(request, session, targetID, "控制告警死信重投", "CONTROL_ALARM_DEAD_LETTER_REPLAYED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control alarm replay invalid", "Control alarm replay does not accept query parameters.", false, nil)
		return
	}
	if request.Body != nil && request.ContentLength != 0 {
		if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "" && mediaType != "application/json" {
			h.emitControlExitAudit(request, session, targetID, "控制告警死信重投", "CONTROL_ALARM_DEAD_LETTER_REPLAYED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
			writeProblem(writer, request, http.StatusUnsupportedMediaType, "CONTROL_EXIT_REQUEST_INVALID", "Control alarm replay invalid", "The Control alarm replay request body must use application/json.", false, nil)
			return
		}
		var empty struct{}
		if !decodeStrictControlJSON(writer, request, &empty, maximumControlExitRequestBody) {
			h.emitControlExitAudit(request, session, targetID, "控制告警死信重投", "CONTROL_ALARM_DEAD_LETTER_REPLAYED", auditOutcomeRejected, map[string]any{"reasonCode": "CONTROL_EXIT_REQUEST_INVALID"})
			writeProblem(writer, request, http.StatusBadRequest, "CONTROL_EXIT_REQUEST_INVALID", "Control alarm replay invalid", "The Control alarm replay request body must be empty or {}.", false, nil)
			return
		}
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPost, route.internalPath, struct{}{}, nil, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.emitControlExitAudit(request, session, targetID, "控制告警死信重投", "CONTROL_ALARM_DEAD_LETTER_REPLAYED", auditOutcomeForStatus(failure.status), map[string]any{"siteId": route.resourceID, "reasonCode": failure.code})
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.emitControlExitAudit(request, session, targetID, "控制告警死信重投", "CONTROL_ALARM_DEAD_LETTER_REPLAYED", auditOutcomeForStatus(status), map[string]any{"siteId": route.resourceID, "reasonCode": problemCodeFromRaw(rawBody)})
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var resp controlAlarmDeadLetterReplayResponse
	if json.Unmarshal(rawBody, &resp) != nil || resp.OutboxID != route.secondaryID {
		h.emitControlExitAudit(request, session, targetID, "控制告警死信重投", "CONTROL_ALARM_DEAD_LETTER_REPLAYED", auditOutcomeFailed, map[string]any{"siteId": route.resourceID, "reasonCode": "COMMAND_RESPONSE_INVALID"})
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Control Alarm replay response."))
		return
	}
	h.emitControlExitAudit(request, session, targetID, "控制告警死信重投", "CONTROL_ALARM_DEAD_LETTER_REPLAYED", auditOutcomeSucceeded, map[string]any{
		"siteId":   route.resourceID,
		"replayed": resp.Replayed,
	})
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, resp)
}

func (h *handler) emitControlExitAudit(
	request *http.Request,
	session bffSession,
	targetID, targetLabel, action, outcome string,
	summary map[string]any,
) {
	h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
		Category:      auditserver.AggregateTypeControlLock,
		TargetID:      targetID,
		TargetLabel:   targetLabel,
		Action:        action,
		Outcome:       outcome,
		ChangeSummary: summary,
	})
}

func controlExitRouteAuditMeta(route publicControlExitRoute, method string) (string, string, string) {
	switch route.kind {
	case "asset-control-lock":
		return "asset:" + route.resourceID, "设备控制锁定", "ASSET_CONTROL_LOCK_UPDATED"
	case "site-control-shutdown":
		return "site:" + route.resourceID, "紧急停机控制", "SITE_CONTROL_SHUTDOWN"
	case "site-device-priority-lock":
		if method == http.MethodDelete {
			return "device:" + route.secondaryID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_RELEASED"
		}
		return "device:" + route.secondaryID, "设备优先级锁定", "DEVICE_PRIORITY_LOCK_UPDATED"
	case "site-control-alarm-dead-letter-replay":
		return "outbox:" + route.secondaryID, "控制告警死信重投", "CONTROL_ALARM_DEAD_LETTER_REPLAYED"
	case "site-command-batches":
		return "site:" + route.resourceID, "批量群控指令", "COMMAND_BATCH_SUBMITTED"
	case "site-command-batch-cancel":
		return "command-batch:" + route.secondaryID, "批量群控指令", "COMMAND_BATCH_CANCELLED"
	default:
		return "site:" + route.resourceID, "站点控制模式", "SITE_CONTROL_MODE_UPDATED"
	}
}

func siteControlModeTargetLabel(mode string) string {
	switch mode {
	case siteControlModeRemoteAuto:
		return "站点控制模式（远程自动）"
	case siteControlModeManualOverride:
		return "站点控制模式（本地手动）"
	case siteControlModeSafeShutdownExit:
		return "站点控制模式（安全退出停机）"
	default:
		return "站点控制模式"
	}
}

func assetControlLockTargetLabel(lockMode string) string {
	switch lockMode {
	case assetControlLockUnlocked:
		return "设备控制锁定（已解锁）"
	case assetControlLockManual:
		return "设备控制锁定（手动锁定）"
	case assetControlLockMaintenance:
		return "设备控制锁定（维保锁定）"
	default:
		return "设备控制锁定"
	}
}

func siteControlShutdownTargetLabel(mode string) string {
	switch mode {
	case siteControlShutdownModePlantSafe:
		return "紧急停机控制（全站安全停机）"
	case siteControlShutdownModeEmergencyDoRelease:
		return "紧急停机控制（紧急直出释放）"
	case siteControlShutdownModeAbortUnitSequence:
		return "紧急停机控制（中止机组序列）"
	default:
		return "紧急停机控制"
	}
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
	defer drainAndCloseBody(response.Body)
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

func routeRevisionFromRequest(request *http.Request) int64 {
	decision := routeDecisionFromContext(request.Context())
	return decision.RegistryRevision
}

func derefOptionalString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (h *handler) populateSiteControlStateNames(ctx context.Context, session bffSession, actorPrincipalID string, state *siteControlStateView) {
	if state == nil {
		return
	}
	ids := make([]string, 0, 1+len(state.AssetLocks)+len(state.DevicePriorityLocks))
	if state.UpdatedBy != nil && strings.TrimSpace(*state.UpdatedBy) != "" {
		ids = append(ids, strings.TrimSpace(*state.UpdatedBy))
	}
	for _, lock := range state.AssetLocks {
		if lock.UpdatedBy != nil && strings.TrimSpace(*lock.UpdatedBy) != "" {
			ids = append(ids, strings.TrimSpace(*lock.UpdatedBy))
		}
	}
	for _, lock := range state.DevicePriorityLocks {
		if strings.TrimSpace(lock.LockedBy) != "" {
			ids = append(ids, strings.TrimSpace(lock.LockedBy))
		}
	}
	names := h.lookupTenantPrincipalNames(ctx, session, actorPrincipalID, ids)
	state.UpdatedByName = resolvedPrincipalName(state.UpdatedBy, names)
	for i := range state.AssetLocks {
		state.AssetLocks[i].UpdatedByName = resolvedPrincipalName(state.AssetLocks[i].UpdatedBy, names)
	}
	for i := range state.DevicePriorityLocks {
		state.DevicePriorityLocks[i].LockedByName = resolvedStringName(state.DevicePriorityLocks[i].LockedBy, names)
	}
}

func resolvedStringName(rawID string, names map[string]string) *string {
	trimmed := strings.TrimSpace(rawID)
	if trimmed == "" {
		return nil
	}
	return resolvedPrincipalName(&trimmed, names)
}

func (h *handler) populateAssetControlLockName(ctx context.Context, session bffSession, actorPrincipalID string, lock *assetControlLockView) {
	if lock == nil || lock.UpdatedBy == nil || strings.TrimSpace(*lock.UpdatedBy) == "" {
		return
	}
	names := h.lookupTenantPrincipalNames(ctx, session, actorPrincipalID, []string{strings.TrimSpace(*lock.UpdatedBy)})
	lock.UpdatedByName = resolvedPrincipalName(lock.UpdatedBy, names)
}

func resolvedPrincipalName(updatedBy *string, names map[string]string) *string {
	if updatedBy == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*updatedBy)
	if trimmed == "" {
		return nil
	}
	if name, ok := names[trimmed]; ok && strings.TrimSpace(name) != "" {
		copyName := name
		return &copyName
	}
	return nil
}

func (h *handler) lookupTenantPrincipalNames(ctx context.Context, session bffSession, actorPrincipalID string, principalIDs []string) map[string]string {
	unique := make([]string, 0, len(principalIDs))
	seen := make(map[string]struct{}, len(principalIDs))
	for _, id := range principalIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		unique = append(unique, trimmed)
	}
	if len(unique) == 0 {
		return nil
	}
	if h == nil || h.identity == nil || h.identity.config.IAMHTTPClient == nil ||
		strings.TrimSpace(h.identity.config.IAMURL) == "" || h.identity.config.DelegationSigner == nil {
		if actorPrincipalID != "" && !strings.HasPrefix(actorPrincipalID, "spiffe://") && strings.TrimSpace(session.Principal.DisplayName) != "" {
			if _, ok := seen[actorPrincipalID]; ok {
				return map[string]string{actorPrincipalID: session.Principal.DisplayName}
			}
		}
		return nil
	}

	now := h.identity.now().UTC()
	expiry := now.Add(h.identity.config.DelegationTTL)
	if !session.ExpiresAt.IsZero() && expiry.After(session.ExpiresAt) {
		expiry = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer:           h.identity.config.ExecutingWorkloadSPIFFE,
		Subject:          session.Principal.Subject,
		SubjectIssuer:    session.Principal.Issuer,
		DisplayName:      session.Principal.DisplayName,
		Email:            session.Principal.Email,
		Roles:            append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience:         h.identity.config.IAMAudience,
		TenantID:         session.TenantID,
		Actions:          []string{"iam:principal:read"},
		Scopes:           []string{"session:" + session.ID},
		PolicyRevision:   h.identity.config.PolicyRevision,
		SessionID:        session.ID,
		IssuedAt:         now.Unix(),
		ExpiresAt:        expiry.Unix(),
		TokenID:          randomURLToken(16),
	}
	grant, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		return nil
	}
	payload, err := json.Marshal(iamserver.TenantPrincipalNamesRequest{
		TenantID:     session.TenantID,
		PrincipalIDs: unique,
	})
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.identity.config.IAMURL, "/")+iamserver.PrincipalNamesPath, bytes.NewReader(payload))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, application/problem+json")
	req.Header.Set("X-Delegation-Grant", grant)
	observability.InjectHTTP(ctx, req.Header)
	resp, err := h.identity.config.IAMHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer drainAndCloseBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var body iamserver.TenantPrincipalNamesResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if decoder.Decode(&body) != nil {
		return nil
	}
	return body.Names
}
