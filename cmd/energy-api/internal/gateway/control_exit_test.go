package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestGatewaySiteControlStateAndControlModeExit(t *testing.T) {
	fixture := newCommandGatewayFixture(t)

	// GET control-state returns REMOTE_AUTO by default.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/control-state", nil)
	fixture.authenticate(getReq, false)
	getRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET control-state status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var initial siteControlStateView
	if json.NewDecoder(getRec.Body).Decode(&initial) != nil || initial.ControlMode != siteControlModeRemoteAuto || initial.Revision != 0 {
		t.Fatalf("unexpected initial control state %#v", initial)
	}

	// Operator without schedule.manage cannot change site control mode -> 403.
	fixture.denyScheduleManage.Store(true)
	forbidReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"MANUAL_OVERRIDE","reason":"切到本地手动"}`))
	fixture.authenticate(forbidReq, true)
	forbidReq.Header.Set("Content-Type", "application/json")
	forbidRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(forbidRec, forbidReq)
	if forbidRec.Code != http.StatusForbidden {
		t.Fatalf("operator PUT control-mode status=%d want 403 body=%s", forbidRec.Code, forbidRec.Body.String())
	}
	fixture.denyScheduleManage.Store(false)

	// MANUAL_OVERRIDE cancels pending/queued site commands cleanly.
	fixture.cancelledPendingIDs = []string{fixture.commandID}
	overrideReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"MANUAL_OVERRIDE","reason":"现场手自动切换接管"}`))
	fixture.authenticate(overrideReq, true)
	overrideReq.Header.Set("Content-Type", "application/json")
	overrideRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(overrideRec, overrideReq)
	if overrideRec.Code != http.StatusOK {
		t.Fatalf("PUT MANUAL_OVERRIDE status=%d body=%s", overrideRec.Code, overrideRec.Body.String())
	}
	var overrideResp siteControlModeMutationResponse
	if json.NewDecoder(overrideRec.Body).Decode(&overrideResp) != nil ||
		overrideResp.ControlMode != siteControlModeManualOverride || overrideResp.Revision != 1 ||
		len(overrideResp.CancelledCommandIDs) != 1 || overrideResp.CancelledCommandIDs[0] != fixture.commandID {
		t.Fatalf("unexpected MANUAL_OVERRIDE response %#v", overrideResp)
	}
	if fixture.lastCancelPending == nil || len(fixture.lastCancelPending.DeviceIDs) != 0 {
		t.Fatalf("expected site-wide cancel-pending, got %#v", fixture.lastCancelPending)
	}

	// Subsequent POST /api/v1/commands returns 409 COMMAND_CONTROL_LOCKED without calling telemetry or command.
	cmdReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":24.5}}`))
	fixture.authenticate(cmdReq, true)
	cmdReq.Header.Set("Content-Type", "application/json")
	cmdReq.Header.Set("Idempotency-Key", "cmd-blocked-manual-override")
	cmdRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(cmdRec, cmdReq)
	if cmdRec.Code != http.StatusConflict {
		t.Fatalf("blocked command status=%d want 409 body=%s", cmdRec.Code, cmdRec.Body.String())
	}
	var problem platformapi.ProblemDetails
	if json.NewDecoder(cmdRec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_CONTROL_LOCKED" {
		t.Fatalf("unexpected blocked command problem %#v", problem)
	}
	if fixture.telemetryCalls.Load() != 0 || fixture.identityCalls.Load() != 0 || fixture.commandCalls.Load() != 1 {
		t.Fatalf("expected blocked command not to invoke telemetry/totp/create; telemetry=%d identity=%d command=%d",
			fixture.telemetryCalls.Load(), fixture.identityCalls.Load(), fixture.commandCalls.Load())
	}

	// SAFE_SHUTDOWN_EXIT fails with 409 SAFE_SHUTDOWN_PRECONDITION_FAILED when an active DISPATCHING command exists.
	fixture.dispatching.Store(true)
	blockedExitReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"SAFE_SHUTDOWN_EXIT","reason":"安全停机退出","expectedRevision":1}`))
	fixture.authenticate(blockedExitReq, true)
	blockedExitReq.Header.Set("Content-Type", "application/json")
	blockedExitRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(blockedExitRec, blockedExitReq)
	if blockedExitRec.Code != http.StatusConflict {
		t.Fatalf("SAFE_SHUTDOWN_EXIT while DISPATCHING status=%d want 409 body=%s", blockedExitRec.Code, blockedExitRec.Body.String())
	}
	if json.NewDecoder(blockedExitRec.Body).Decode(&problem) != nil || problem.Code != "SAFE_SHUTDOWN_PRECONDITION_FAILED" {
		t.Fatalf("unexpected precondition problem %#v", problem)
	}

	// Once no active DISPATCHING/OUTCOME_UNKNOWN commands remain, SAFE_SHUTDOWN_EXIT succeeds.
	fixture.dispatching.Store(false)
	exitReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"SAFE_SHUTDOWN_EXIT","reason":"安全停机退出","expectedRevision":1}`))
	fixture.authenticate(exitReq, true)
	exitReq.Header.Set("Content-Type", "application/json")
	exitRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(exitRec, exitReq)
	if exitRec.Code != http.StatusOK {
		t.Fatalf("SAFE_SHUTDOWN_EXIT status=%d want 200 body=%s", exitRec.Code, exitRec.Body.String())
	}
}

func TestGatewayAssetControlLockCancelsBoundDevicesAndBlocksCommands(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	fixture.cancelledPendingIDs = []string{fixture.commandID}

	// Lock asset in MAINTENANCE_LOCK -> cancels pending commands for bound devices.
	lockReq := httptest.NewRequest(http.MethodPut, "/api/v1/assets/"+fixture.assetID+"/control-lock", strings.NewReader(`{"lockMode":"MAINTENANCE_LOCK","reason":"冷机维保挂牌上锁"}`))
	fixture.authenticate(lockReq, true)
	lockReq.Header.Set("Content-Type", "application/json")
	lockRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(lockRec, lockReq)
	if lockRec.Code != http.StatusOK {
		t.Fatalf("PUT control-lock status=%d body=%s", lockRec.Code, lockRec.Body.String())
	}
	var lockResp assetControlLockMutationResponse
	if json.NewDecoder(lockRec.Body).Decode(&lockResp) != nil ||
		lockResp.LockMode != assetControlLockMaintenance || lockResp.Revision != 1 ||
		len(lockResp.CancelledCommandIDs) != 1 || lockResp.CancelledCommandIDs[0] != fixture.commandID {
		t.Fatalf("unexpected control-lock response %#v", lockResp)
	}
	if fixture.lastCancelPending == nil || len(fixture.lastCancelPending.DeviceIDs) != 1 || fixture.lastCancelPending.DeviceIDs[0] != fixture.deviceID {
		t.Fatalf("expected device-scoped cancel-pending for %s, got %#v", fixture.deviceID, fixture.lastCancelPending)
	}

	// Commands targeting locked asset are rejected with 409 COMMAND_CONTROL_LOCKED.
	cmdReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":24.5}}`))
	fixture.authenticate(cmdReq, true)
	cmdReq.Header.Set("Content-Type", "application/json")
	cmdReq.Header.Set("Idempotency-Key", "cmd-blocked-asset-lock")
	cmdRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(cmdRec, cmdReq)
	if cmdRec.Code != http.StatusConflict {
		t.Fatalf("command on locked asset status=%d want 409 body=%s", cmdRec.Code, cmdRec.Body.String())
	}
	var problem platformapi.ProblemDetails
	if json.NewDecoder(cmdRec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_CONTROL_LOCKED" {
		t.Fatalf("unexpected locked asset problem %#v", problem)
	}

	// Unlock asset -> subsequent command succeeds.
	fixture.lastCancelPending = nil
	unlockReq := httptest.NewRequest(http.MethodPut, "/api/v1/assets/"+fixture.assetID+"/control-lock", strings.NewReader(`{"lockMode":"UNLOCKED","reason":"维保完成解锁","expectedRevision":1}`))
	fixture.authenticate(unlockReq, true)
	unlockReq.Header.Set("Content-Type", "application/json")
	unlockRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(unlockRec, unlockReq)
	if unlockRec.Code != http.StatusOK || fixture.lastCancelPending != nil {
		t.Fatalf("unlock asset failed: status=%d lastCancel=%#v body=%s", unlockRec.Code, fixture.lastCancelPending, unlockRec.Body.String())
	}

	okCmdReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":24.5}}`))
	fixture.authenticate(okCmdReq, true)
	okCmdReq.Header.Set("Content-Type", "application/json")
	okCmdReq.Header.Set("Idempotency-Key", "cmd-after-unlock")
	okCmdRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(okCmdRec, okCmdReq)
	if okCmdRec.Code != http.StatusAccepted {
		t.Fatalf("command after unlock status=%d want 202 body=%s", okCmdRec.Code, okCmdRec.Body.String())
	}
}

func TestGatewaySiteControlStateAndControlModePreserveActiveMaintenanceWindow(t *testing.T) {
	fixture := newCommandGatewayFixture(t)

	// When no maintenance window is active, activeMaintenanceWindow is omitted from JSON.
	emptyGetReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/control-state", nil)
	fixture.authenticate(emptyGetReq, false)
	emptyGetRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(emptyGetRec, emptyGetReq)
	if emptyGetRec.Code != http.StatusOK || strings.Contains(emptyGetRec.Body.String(), `"activeMaintenanceWindow"`) {
		t.Fatalf("expected omitted activeMaintenanceWindow, status=%d body=%s", emptyGetRec.Code, emptyGetRec.Body.String())
	}

	window := &activeMaintenanceWindowView{
		ID:        "01990000-8400-7000-8000-000000000701",
		Title:     "1号冷机季节性清洗维保",
		StartDate: "2026-11-15",
		EndDate:   "2026-11-17",
		Reason:    "冷却水塔与冷凝器例行清洗",
	}
	fixture.activeMaintenanceWindow = window

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/control-state", nil)
	fixture.authenticate(getReq, false)
	getRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET control-state status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var state siteControlStateView
	if err := json.NewDecoder(getRec.Body).Decode(&state); err != nil {
		t.Fatalf("decode GET control-state: %v", err)
	}
	if state.ActiveMaintenanceWindow == nil || *state.ActiveMaintenanceWindow != *window {
		t.Fatalf("GET activeMaintenanceWindow=%#v, want %#v", state.ActiveMaintenanceWindow, window)
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"MANUAL_OVERRIDE","reason":"维保期间切为现场手动"}`))
	fixture.authenticate(putReq, true)
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT control-mode status=%d body=%s", putRec.Code, putRec.Body.String())
	}
	var updated siteControlStateView
	if err := json.NewDecoder(putRec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode PUT control-mode: %v", err)
	}
	if updated.ControlMode != siteControlModeManualOverride || updated.ActiveMaintenanceWindow == nil || *updated.ActiveMaintenanceWindow != *window {
		t.Fatalf("PUT control-mode=%#v, want activeMaintenanceWindow=%#v", updated, window)
	}
}

func TestGatewayMaintenanceWindowGatesCommands(t *testing.T) {
	window := &activeMaintenanceWindowView{
		ID:        "01990000-8400-7000-8000-000000000702",
		Title:     "1号冷机季节性清洗维保",
		StartDate: "2026-11-15",
		EndDate:   "2026-11-17",
		Reason:    "冷却水塔与冷凝器例行清洗",
	}

	blockedCases := []struct {
		name       string
		capability commandmodel.Capability
		paramsJSON string
	}{
		{name: "START", capability: commandmodel.CapabilityStart, paramsJSON: `{}`},
		{name: "START_UNIT_SEQUENCE", capability: commandmodel.CapabilityStartUnitSequence, paramsJSON: `{}`},
		{name: "SET_TEMPERATURE_SETPOINT", capability: commandmodel.CapabilitySetTemperatureSetpoint, paramsJSON: `{"setpointC":24.5}`},
		{name: "SET_CHILLED_WATER_TEMPERATURE_SETPOINT", capability: commandmodel.CapabilitySetChilledWaterTemperatureSetpoint, paramsJSON: `{"setpointC":7.5}`},
		{name: "SET_FREQUENCY", capability: commandmodel.CapabilitySetFrequency, paramsJSON: `{"frequencyHz":42.0}`},
	}

	for _, tc := range blockedCases {
		t.Run("reject_"+tc.name, func(t *testing.T) {
			fixture := newCommandGatewayFixture(t)
			fixture.commandCapability = tc.capability
			fixture.activeMaintenanceWindow = window

			body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":` + tc.paramsJSON + `}`
			req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
			fixture.authenticate(req, true)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "cmd-maint-block-"+strings.ToLower(string(tc.capability)))
			rec := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusConflict {
				t.Fatalf("%s status=%d want 409 body=%s", tc.name, rec.Code, rec.Body.String())
			}
			var problem platformapi.ProblemDetails
			if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem.Code != "COMMAND_CONTROL_LOCKED" || problem.Title != "Site maintenance window active" || problem.Retryable {
				t.Fatalf("unexpected problem %#v", problem)
			}
			if !strings.Contains(problem.Detail, window.Title) || !strings.Contains(problem.Detail, window.StartDate+".."+window.EndDate) {
				t.Fatalf("expected detail to name window %q and %s..%s, got %q", window.Title, window.StartDate, window.EndDate, problem.Detail)
			}
			if fixture.telemetryCalls.Load() != 0 || fixture.identityCalls.Load() != 0 || fixture.commandCalls.Load() != 0 {
				t.Fatalf("expected gate before telemetry/totp/command, calls telemetry=%d identity=%d command=%d", fixture.telemetryCalls.Load(), fixture.identityCalls.Load(), fixture.commandCalls.Load())
			}
		})
	}

	permittedCases := []struct {
		name             string
		capability       commandmodel.Capability
		verificationCode string
	}{
		{name: "STOP", capability: commandmodel.CapabilityStop, verificationCode: "123456"},
		{name: "STOP_UNIT_SEQUENCE", capability: commandmodel.CapabilityStopUnitSequence, verificationCode: "123456"},
		{name: "RESET_FAULT", capability: commandmodel.CapabilityResetFault},
	}

	for _, tc := range permittedCases {
		t.Run("permit_"+tc.name, func(t *testing.T) {
			fixture := newCommandGatewayFixture(t)
			fixture.commandCapability = tc.capability
			fixture.activeMaintenanceWindow = window

			body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{}`
			if tc.verificationCode != "" {
				body += `,"verificationCode":"` + tc.verificationCode + `"`
			}
			body += `}`
			req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
			fixture.authenticate(req, true)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "cmd-maint-allow-"+strings.ToLower(string(tc.capability)))
			rec := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusAccepted {
				t.Fatalf("%s status=%d want 202 body=%s", tc.name, rec.Code, rec.Body.String())
			}
			if fixture.commandCalls.Load() != 1 {
				t.Fatalf("%s expected commandCalls=1, got %d", tc.name, fixture.commandCalls.Load())
			}
		})
	}

	t.Run("site_control_mode_precedes_maintenance_window", func(t *testing.T) {
		fixture := newCommandGatewayFixture(t)
		fixture.commandCapability = commandmodel.CapabilityStop
		fixture.siteControlMode = siteControlModeManualOverride
		fixture.activeMaintenanceWindow = window

		body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"verificationCode":"123456"}`
		req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
		fixture.authenticate(req, true)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "cmd-maint-mode-precedence")
		rec := httptest.NewRecorder()
		fixture.handler.ServeHTTP(rec, req)

		var problem platformapi.ProblemDetails
		if rec.Code != http.StatusConflict || json.NewDecoder(rec.Body).Decode(&problem) != nil ||
			problem.Code != "COMMAND_CONTROL_LOCKED" || problem.Title != "Site control exit active" {
			t.Fatalf("expected site control exit precedence 409, status=%d problem=%#v", rec.Code, problem)
		}
	})

	t.Run("asset_control_lock_precedes_maintenance_window", func(t *testing.T) {
		fixture := newCommandGatewayFixture(t)
		fixture.commandCapability = commandmodel.CapabilityResetFault
		fixture.assetLockMode = assetControlLockMaintenance
		fixture.activeMaintenanceWindow = window

		body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{}}`
		req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
		fixture.authenticate(req, true)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "cmd-maint-asset-precedence")
		rec := httptest.NewRecorder()
		fixture.handler.ServeHTTP(rec, req)

		var problem platformapi.ProblemDetails
		if rec.Code != http.StatusConflict || json.NewDecoder(rec.Body).Decode(&problem) != nil ||
			problem.Code != "COMMAND_CONTROL_LOCKED" || problem.Title != "Asset control locked" {
			t.Fatalf("expected asset control lock precedence 409, status=%d problem=%#v", rec.Code, problem)
		}
	})
}
