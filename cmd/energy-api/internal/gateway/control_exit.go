package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
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

var controlWriteKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}\z`)

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

func routeRevisionFromRequest(request *http.Request) int64 {
	decision := routeDecisionFromContext(request.Context())
	return decision.RegistryRevision
}
