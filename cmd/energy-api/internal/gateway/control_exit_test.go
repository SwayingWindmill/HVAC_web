package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/s2telemetryapi"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
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
		fixture.commandCapability = commandmodel.CapabilityResetFault
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

func TestGatewaySiteControlExitAllowsStopWhileBlockingStartAndSet(t *testing.T) {
	modes := []string{siteControlModeManualOverride, siteControlModeSafeShutdownExit}
	allowed := []commandmodel.Capability{
		commandmodel.CapabilityStop,
		commandmodel.CapabilityStopUnitSequence,
	}
	blocked := []struct {
		capability commandmodel.Capability
		paramsJSON string
	}{
		{capability: commandmodel.CapabilityStart, paramsJSON: `{}`},
		{capability: commandmodel.CapabilityStartUnitSequence, paramsJSON: `{}`},
		{capability: commandmodel.CapabilityResetFault, paramsJSON: `{}`},
		{capability: commandmodel.CapabilitySetTemperatureSetpoint, paramsJSON: `{"setpointC":24.5}`},
	}

	for _, mode := range modes {
		for _, cap := range allowed {
			t.Run(mode+"/allows_"+string(cap), func(t *testing.T) {
				fixture := newCommandGatewayFixture(t)
				fixture.siteControlMode = mode
				fixture.commandCapability = cap

				body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"verificationCode":"123456"}`
				req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
				fixture.authenticate(req, true)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "cmd-exit-allow-"+strings.ToLower(mode)+"-"+strings.ToLower(string(cap)))
				rec := httptest.NewRecorder()
				fixture.handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusAccepted || fixture.commandCalls.Load() != 1 {
					t.Fatalf("mode=%s cap=%s status=%d commands=%d want 202 body=%s",
						mode, cap, rec.Code, fixture.commandCalls.Load(), rec.Body.String())
				}
			})
		}

		for _, tc := range blocked {
			t.Run(mode+"/blocks_"+string(tc.capability), func(t *testing.T) {
				fixture := newCommandGatewayFixture(t)
				fixture.siteControlMode = mode
				fixture.commandCapability = tc.capability

				body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":` + tc.paramsJSON + `,"verificationCode":"123456"}`
				req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
				fixture.authenticate(req, true)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "cmd-exit-block-"+strings.ToLower(mode)+"-"+strings.ToLower(string(tc.capability)))
				rec := httptest.NewRecorder()
				fixture.handler.ServeHTTP(rec, req)

				var problem platformapi.ProblemDetails
				if rec.Code != http.StatusConflict || json.NewDecoder(rec.Body).Decode(&problem) != nil ||
					problem.Code != "COMMAND_CONTROL_LOCKED" {
					t.Fatalf("mode=%s cap=%s status=%d want 409 COMMAND_CONTROL_LOCKED problem=%#v",
						mode, tc.capability, rec.Code, problem)
				}
				if fixture.telemetryCalls.Load() != 0 || fixture.identityCalls.Load() != 0 || fixture.commandCalls.Load() != 0 {
					t.Fatalf("mode=%s cap=%s expected block before telemetry/totp/command, calls telemetry=%d identity=%d command=%d",
						mode, tc.capability, fixture.telemetryCalls.Load(), fixture.identityCalls.Load(), fixture.commandCalls.Load())
				}
			})
		}
	}
}

func TestGatewayManualOverrideStopStillEnforcesHydraulicInterlock(t *testing.T) {
	f := newShutdownPlantFixture(t)
	f.siteControlMode = siteControlModeManualOverride
	f.runStates[f.chillerADevID] = "RUNNING"

	blocked := f.submitDirectCommand(f.chwp1AssetID, f.chwp1StopPointID, "123456", "manual-stop-pump-while-chiller-running")
	var problem platformapi.ProblemDetails
	if blocked.Code != http.StatusConflict || json.NewDecoder(blocked.Body).Decode(&problem) != nil ||
		problem.Code != "COMMAND_INTERLOCK_BLOCKED" || len(f.dispatched) != 0 {
		t.Fatalf("expected 409 COMMAND_INTERLOCK_BLOCKED with 0 dispatches, status=%d problem=%#v dispatched=%v",
			blocked.Code, problem, f.dispatched)
	}

	f.runStates[f.chillerADevID] = "STOPPED"
	allowed := f.submitDirectCommand(f.chwp1AssetID, f.chwp1StopPointID, "123456", "manual-stop-pump-after-chiller-stopped")
	if allowed.Code != http.StatusAccepted || len(f.dispatched) != 1 || f.dispatched[0].PointID != f.chwp1StopPointID {
		t.Fatalf("expected 202 release after chiller stopped, status=%d body=%s dispatched=%#v",
			allowed.Code, allowed.Body.String(), f.dispatched)
	}
}

func TestGatewaySiteControlShutdown(t *testing.T) {
	t.Run("forbidden_for_operator", func(t *testing.T) {
		f := newShutdownPlantFixture(t)
		f.denyScheduleManage.Store(true)

		rec := f.postShutdown(`{"mode":"PLANT_SAFE_SHUTDOWN","reason":"全站停机","verificationCode":"123456"}`)
		var problem platformapi.ProblemDetails
		if rec.Code != http.StatusForbidden || json.NewDecoder(rec.Body).Decode(&problem) != nil ||
			problem.Code != "CONTROL_EXIT_FORBIDDEN" {
			t.Fatalf("status=%d want 403 CONTROL_EXIT_FORBIDDEN problem=%#v body=%s", rec.Code, problem, rec.Body.String())
		}
	})

	t.Run("requires_valid_totp_step_up", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			code string
		}{
			{name: "missing_code", code: ""},
			{name: "wrong_code", code: "000000"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newShutdownPlantFixture(t)
				body := fmt.Sprintf(`{"mode":"PLANT_SAFE_SHUTDOWN","reason":"全站停机","verificationCode":%q}`, tc.code)
				rec := f.postShutdown(body)
				var problem platformapi.ProblemDetails
				if rec.Code != http.StatusPreconditionRequired || json.NewDecoder(rec.Body).Decode(&problem) != nil ||
					problem.Code != "STEP_UP_REQUIRED" {
					t.Fatalf("status=%d want 428 STEP_UP_REQUIRED problem=%#v body=%s", rec.Code, problem, rec.Body.String())
				}
			})
		}
	})

	t.Run("validates_mode_reason_unit_and_emergency_confirmation", func(t *testing.T) {
		cases := []struct {
			name string
			body string
		}{
			{name: "invalid_mode", body: `{"mode":"UNKNOWN","reason":"x","verificationCode":"123456"}`},
			{name: "blank_reason", body: `{"mode":"PLANT_SAFE_SHUTDOWN","reason":"   ","verificationCode":"123456"}`},
			{name: "abort_missing_unit_code", body: `{"mode":"ABORT_UNIT_SEQUENCE","reason":"中止顺控","verificationCode":"123456"}`},
			{name: "abort_unknown_unit_code", body: `{"mode":"ABORT_UNIT_SEQUENCE","unitCode":"chiller-z","reason":"中止顺控","verificationCode":"123456"}`},
			{name: "emergency_missing_confirm", body: `{"mode":"EMERGENCY_DO_RELEASE","reason":"紧急全断","verificationCode":"123456"}`},
			{name: "emergency_false_confirm", body: `{"mode":"EMERGENCY_DO_RELEASE","confirmEmergencyDoRelease":false,"reason":"紧急全断","verificationCode":"123456"}`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				f := newShutdownPlantFixture(t)
				rec := f.postShutdown(tc.body)
				var problem platformapi.ProblemDetails
				if rec.Code != http.StatusBadRequest || json.NewDecoder(rec.Body).Decode(&problem) != nil ||
					problem.Code != "CONTROL_EXIT_REQUEST_INVALID" {
					t.Fatalf("%s status=%d want 400 CONTROL_EXIT_REQUEST_INVALID problem=%#v body=%s",
						tc.name, rec.Code, problem, rec.Body.String())
				}
			})
		}
	})

	t.Run("plant_safe_shutdown_stops_running_chillers_and_defers_active_pumps", func(t *testing.T) {
		f := newShutdownPlantFixture(t)
		f.runStates[f.chillerADevID] = "RUNNING"
		f.runStates[f.chillerBDevID] = "STOPPED"
		f.cancelledPendingIDs = []string{"cmd-pending-site-1"}

		rec := f.postShutdown(`{"mode":"PLANT_SAFE_SHUTDOWN","reason":"闭馆前全站安全停机","verificationCode":"123456"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
		}
		var resp siteControlShutdownResponse
		if json.NewDecoder(rec.Body).Decode(&resp) != nil {
			t.Fatalf("failed to decode response: %s", rec.Body.String())
		}
		if resp.Mode != siteControlShutdownModePlantSafe ||
			resp.SiteControlState.ControlMode != siteControlModeSafeShutdownExit ||
			resp.SiteControlState.Revision != 1 {
			t.Fatalf("unexpected shutdown state %#v", resp)
		}
		if f.lastCancelPending == nil || len(f.lastCancelPending.DeviceIDs) != 0 ||
			!f.lastCancelPending.AbortActiveSequences ||
			!slices.Equal(resp.CancelledCommandIDs, []string{"cmd-pending-site-1"}) {
			t.Fatalf("expected site-wide cancel-pending with abortActiveSequences, got cancel=%#v resp=%#v", f.lastCancelPending, resp.CancelledCommandIDs)
		}

		// Dispatches STOP for running chiller-a and safe tower-1 (chiller-b is already STOPPED),
		// never stops already-stopped chiller-b, and never cuts pumps serving running chiller-a.
		dispatchedPoints := f.dispatchedPointIDs()
		if !slices.Contains(dispatchedPoints, f.chillerAStopPointID) ||
			!slices.Contains(dispatchedPoints, f.tower1StopPointID) ||
			slices.Contains(dispatchedPoints, f.chillerBStopPointID) ||
			slices.Contains(dispatchedPoints, f.chwp1StopPointID) ||
			slices.Contains(dispatchedPoints, f.chwp2StopPointID) {
			t.Fatalf("unexpected dispatched points %v", dispatchedPoints)
		}
		if len(resp.SubmittedCommandIDs) != 2 {
			t.Fatalf("submittedCommandIds=%v want 2 commands", resp.SubmittedCommandIDs)
		}
		if !slices.Contains(resp.DeferredHoldingAssetIds, f.chwp1AssetID) ||
			!slices.Contains(resp.DeferredHoldingAssetIds, f.chwp2AssetID) ||
			slices.Contains(resp.DeferredHoldingAssetIds, f.tower1AssetID) {
			t.Fatalf("deferredHoldingAssetIds=%v want chwp-1 and chwp-2 deferred only", resp.DeferredHoldingAssetIds)
		}
	})

	t.Run("plant_safe_shutdown_with_stale_chiller_telemetry_still_stops_chiller_and_defers_pumps", func(t *testing.T) {
		f := newShutdownPlantFixture(t)
		f.runStates[f.chillerADevID] = "RUNNING"
		f.telemetryMode[f.chillerADevID] = "STALE"
		f.runStates[f.chillerBDevID] = "STOPPED"

		rec := f.postShutdown(`{"mode":"PLANT_SAFE_SHUTDOWN","reason":"遥测陈旧下安全停冷保泵","verificationCode":"123456"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
		}
		var resp siteControlShutdownResponse
		if json.NewDecoder(rec.Body).Decode(&resp) != nil {
			t.Fatalf("decode failed: %s", rec.Body.String())
		}
		dispatchedPoints := f.dispatchedPointIDs()
		if !slices.Contains(dispatchedPoints, f.chillerAStopPointID) ||
			!slices.Contains(dispatchedPoints, f.tower1StopPointID) ||
			slices.Contains(dispatchedPoints, f.chwp1StopPointID) ||
			slices.Contains(dispatchedPoints, f.chwp2StopPointID) {
			t.Fatalf("unexpected dispatched points under stale chiller telemetry: %v", dispatchedPoints)
		}
		if !slices.Contains(resp.DeferredHoldingAssetIds, f.chwp1AssetID) ||
			!slices.Contains(resp.DeferredHoldingAssetIds, f.chwp2AssetID) {
			t.Fatalf("expected pumps deferred when chiller telemetry stale, got %v", resp.DeferredHoldingAssetIds)
		}
	})

	t.Run("abort_unit_sequence_while_chiller_running_stops_chiller_and_defers_pumps", func(t *testing.T) {
		f := newShutdownPlantFixture(t)
		f.siteControlMode = siteControlModeRemoteAuto
		f.runStates[f.chillerADevID] = "RUNNING"
		f.cancelledPendingIDs = []string{"cmd-unit-a-1"}

		rec := f.postShutdown(`{"mode":"ABORT_UNIT_SEQUENCE","unitCode":"chiller-a","reason":"中止A组顺控","verificationCode":"123456"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp siteControlShutdownResponse
		if json.NewDecoder(rec.Body).Decode(&resp) != nil {
			t.Fatalf("decode failed: %s", rec.Body.String())
		}
		if resp.SiteControlState.ControlMode != siteControlModeRemoteAuto || f.controlModeUpdates.Load() != 0 {
			t.Fatalf("expected ABORT_UNIT_SEQUENCE to preserve site control mode, got %#v updates=%d", resp.SiteControlState, f.controlModeUpdates.Load())
		}
		if f.lastCancelPending == nil ||
			!f.lastCancelPending.AbortActiveSequences ||
			!slices.Contains(f.lastCancelPending.DeviceIDs, f.chillerADevID) ||
			!slices.Contains(f.lastCancelPending.DeviceIDs, f.chwp1DevID) ||
			!slices.Contains(f.lastCancelPending.DeviceIDs, f.chwp2DevID) ||
			slices.Contains(f.lastCancelPending.DeviceIDs, f.chillerBDevID) ||
			slices.Contains(f.lastCancelPending.DeviceIDs, f.tower1DevID) {
			t.Fatalf("unexpected scoped cancel deviceIds=%#v", f.lastCancelPending)
		}
		dispatchedPoints := f.dispatchedPointIDs()
		if !slices.Equal(dispatchedPoints, []string{f.chillerAStopPointID}) || len(resp.SubmittedCommandIDs) != 1 {
			t.Fatalf("expected only chiller-a STOP dispatched, got points=%v ids=%v", dispatchedPoints, resp.SubmittedCommandIDs)
		}
		if !slices.Contains(resp.DeferredHoldingAssetIds, f.chwp1AssetID) ||
			!slices.Contains(resp.DeferredHoldingAssetIds, f.chwp2AssetID) {
			t.Fatalf("expected chwp-1 and chwp-2 deferred while chiller-a running, got %v", resp.DeferredHoldingAssetIds)
		}
	})

	t.Run("abort_unit_sequence_after_chiller_stopped_stops_chiller_releases_safe_pump_and_handles_locks", func(t *testing.T) {
		for _, unitCode := range []string{"chiller-a", "unit-a"} {
			t.Run(unitCode, func(t *testing.T) {
				f := newShutdownPlantFixture(t)
				f.runStates[f.chillerADevID] = "STOPPED"
				f.runStates[f.chillerBDevID] = "RUNNING"

				body := fmt.Sprintf(`{"mode":"ABORT_UNIT_SEQUENCE","unitCode":%q,"reason":"释放A组闲置水泵保持通道","verificationCode":"123456"}`, unitCode)
				rec := f.postShutdown(body)
				if rec.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
				}
				var resp siteControlShutdownResponse
				if json.NewDecoder(rec.Body).Decode(&resp) != nil {
					t.Fatalf("decode failed: %s", rec.Body.String())
				}
				if f.lastCancelPending == nil || !f.lastCancelPending.AbortActiveSequences {
					t.Fatalf("expected abortActiveSequences=true, got %#v", f.lastCancelPending)
				}
				dispatchedPoints := f.dispatchedPointIDs()
				if !slices.Contains(dispatchedPoints, f.chillerAStopPointID) ||
					!slices.Contains(dispatchedPoints, f.chwp1StopPointID) ||
					len(resp.SubmittedCommandIDs) != 2 {
					t.Fatalf("expected chiller-a and chwp-1 STOP, got points=%v ids=%v", dispatchedPoints, resp.SubmittedCommandIDs)
				}
				if !slices.Equal(resp.DeferredHoldingAssetIds, []string{f.chwp2AssetID}) {
					t.Fatalf("expected shared chwp-2 deferred while chiller-b running, got %v", resp.DeferredHoldingAssetIds)
				}
			})
		}

		// Unlocked chiller still receives STOP while locked auxiliary is deferred.
		fAuxLocked := newShutdownPlantFixture(t)
		fAuxLocked.runStates[fAuxLocked.chillerBDevID] = "STOPPED"
		fAuxLocked.assetLocks[fAuxLocked.tower1AssetID] = assetControlLockManual
		recAuxLocked := fAuxLocked.postShutdown(`{"mode":"ABORT_UNIT_SEQUENCE","unitCode":"chiller-b","reason":"B组停机收尾","verificationCode":"123456"}`)
		if recAuxLocked.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recAuxLocked.Code, recAuxLocked.Body.String())
		}
		var respAuxLocked siteControlShutdownResponse
		if json.NewDecoder(recAuxLocked.Body).Decode(&respAuxLocked) != nil {
			t.Fatalf("decode failed: %s", recAuxLocked.Body.String())
		}
		if !slices.Equal(fAuxLocked.dispatchedPointIDs(), []string{fAuxLocked.chillerBStopPointID}) ||
			!slices.Contains(respAuxLocked.DeferredHoldingAssetIds, fAuxLocked.tower1AssetID) {
			t.Fatalf("expected chiller-b STOP and locked tower-1 deferred, got points=%v resp=%#v", fAuxLocked.dispatchedPointIDs(), respAuxLocked)
		}

		// When all target assets are locked and nothing was cancelled or submitted, return 409 CONTROL_SHUTDOWN_LOCKED.
		fAllLocked := newShutdownPlantFixture(t)
		fAllLocked.runStates[fAllLocked.chillerBDevID] = "STOPPED"
		fAllLocked.assetLocks[fAllLocked.chillerBAssetID] = assetControlLockMaintenance
		fAllLocked.assetLocks[fAllLocked.tower1AssetID] = assetControlLockManual
		recAllLocked := fAllLocked.postShutdown(`{"mode":"ABORT_UNIT_SEQUENCE","unitCode":"unit-b","reason":"全锁拦截验证","verificationCode":"123456"}`)
		var lockedProblem platformapi.ProblemDetails
		if recAllLocked.Code != http.StatusConflict || json.NewDecoder(recAllLocked.Body).Decode(&lockedProblem) != nil ||
			lockedProblem.Code != "CONTROL_SHUTDOWN_LOCKED" {
			t.Fatalf("status=%d want 409 CONTROL_SHUTDOWN_LOCKED problem=%#v body=%s", recAllLocked.Code, lockedProblem, recAllLocked.Body.String())
		}
	})

	t.Run("emergency_do_release_requires_point_reconciles_unknown_and_tolerates_degraded_telemetry", func(t *testing.T) {
		fConfigured := newShutdownPlantFixture(t)
		fConfigured.includeEmergencyPoint = true
		fConfigured.cancelledPendingIDs = []string{"cmd-pending-emerg"}
		rec := fConfigured.postShutdown(`{"mode":"EMERGENCY_DO_RELEASE","confirmEmergencyDoRelease":true,"reason":"紧急全断DO通道","verificationCode":"123456"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp siteControlShutdownResponse
		if json.NewDecoder(rec.Body).Decode(&resp) != nil {
			t.Fatalf("decode failed: %s", rec.Body.String())
		}
		if resp.SiteControlState.ControlMode != siteControlModeSafeShutdownExit ||
			fConfigured.lastCancelPending == nil || !fConfigured.lastCancelPending.AbortActiveSequences ||
			!slices.Equal(resp.CancelledCommandIDs, []string{"cmd-pending-emerg"}) ||
			!slices.Equal(fConfigured.dispatchedPointIDs(), []string{fConfigured.emergencyPointID}) ||
			len(resp.SubmittedCommandIDs) != 1 ||
			len(resp.DeferredHoldingAssetIds) != 0 {
			t.Fatalf("unexpected configured emergency response=%#v points=%v cancel=%#v", resp, fConfigured.dispatchedPointIDs(), fConfigured.lastCancelPending)
		}

		// Reconciles OUTCOME_UNKNOWN command on emergency DO device and proceeds even when telemetry returns 409 or 503.
		for _, mode := range []string{"STALE", "UNAVAILABLE"} {
			t.Run("degraded_"+strings.ToLower(mode), func(t *testing.T) {
				fDegraded := newShutdownPlantFixture(t)
				fDegraded.includeEmergencyPoint = true
				fDegraded.telemetryMode[fDegraded.emergencyDevID] = mode
				unknownCmdID := "019a8000-dddd-7000-8000-000000000099"
				stopProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStop)
				ts := time.Date(2026, 11, 15, 15, 58, 0, 0, time.UTC)
				fDegraded.listedCommands = []commandView{{
					SchemaVersion: 1, CommandID: unknownCmdID, TenantID: fDegraded.tenantID, SiteID: fDegraded.siteID,
					DeviceID: fDegraded.emergencyDevID, PointID: fDegraded.emergencyPointID, Capability: commandmodel.CapabilityStop,
					CapabilityRevision: stopProfile.Revision, Status: commandmodel.IntentOutcomeUnknown,
					Risk: commandmodel.RiskHigh, ApprovalPolicy: commandmodel.ApprovalNone,
					Parameters: commandmodel.CommandParameters{}, DeviceCommandSequence: 99, Version: 5, SnapshotRevision: 1,
					CreatedAt: ts, UpdatedAt: ts.Add(time.Minute),
					Transitions: []commandTransitionView{
						{ToStatus: commandmodel.IntentSubmitted, Reason: "COMMAND_SUBMITTED", ActorType: "PRINCIPAL", OccurredAt: ts, Version: 1},
						{FromStatus: commandStatusPointer(commandmodel.IntentSubmitted), ToStatus: commandmodel.IntentValidating, Reason: "COMMAND_VALIDATING", ActorType: "WORKLOAD", OccurredAt: ts.Add(10 * time.Second), Version: 2},
						{FromStatus: commandStatusPointer(commandmodel.IntentValidating), ToStatus: commandmodel.IntentQueued, Reason: "COMMAND_QUEUED", ActorType: "WORKLOAD", OccurredAt: ts.Add(20 * time.Second), Version: 3},
						{FromStatus: commandStatusPointer(commandmodel.IntentQueued), ToStatus: commandmodel.IntentDispatching, Reason: "DISPATCH_ATTEMPT_PREPARED", ActorType: "WORKLOAD", OccurredAt: ts.Add(30 * time.Second), Version: 4},
						{FromStatus: commandStatusPointer(commandmodel.IntentDispatching), ToStatus: commandmodel.IntentOutcomeUnknown, Reason: "LEASE_EXPIRED_WITHOUT_SEND_PROOF", ActorType: "WORKLOAD", OccurredAt: ts.Add(time.Minute), Version: 5},
					},
				}}

				recDeg := fDegraded.postShutdown(`{"mode":"EMERGENCY_DO_RELEASE","confirmEmergencyDoRelease":true,"reason":"断采故障下仍必达急停","verificationCode":"123456"}`)
				if recDeg.Code != http.StatusOK {
					t.Fatalf("%s status=%d want 200 body=%s", mode, recDeg.Code, recDeg.Body.String())
				}
				if len(fDegraded.reconciledInputs) != 1 || fDegraded.reconciledInputs[0].Outcome != commandmodel.ReconciledNotApplied {
					t.Fatalf("expected NOT_APPLIED reconciliation for OUTCOME_UNKNOWN, got %#v", fDegraded.reconciledInputs)
				}
				if !slices.Equal(fDegraded.dispatchedPointIDs(), []string{fDegraded.emergencyPointID}) {
					t.Fatalf("expected emergency STOP dispatched under degraded %s telemetry, got %v", mode, fDegraded.dispatchedPointIDs())
				}
			})
		}

		// Missing EMERGENCY_DO_RELEASE point returns 409 EMERGENCY_DO_RELEASE_UNAVAILABLE without changing controlMode.
		fAbsent := newShutdownPlantFixture(t)
		fAbsent.includeEmergencyPoint = false
		recAbsent := fAbsent.postShutdown(`{"mode":"EMERGENCY_DO_RELEASE","confirmEmergencyDoRelease":true,"reason":"未配置急停点拦截","verificationCode":"123456"}`)
		var absentProblem platformapi.ProblemDetails
		if recAbsent.Code != http.StatusConflict || json.NewDecoder(recAbsent.Body).Decode(&absentProblem) != nil ||
			absentProblem.Code != "EMERGENCY_DO_RELEASE_UNAVAILABLE" {
			t.Fatalf("absent status=%d want 409 EMERGENCY_DO_RELEASE_UNAVAILABLE problem=%#v body=%s", recAbsent.Code, absentProblem, recAbsent.Body.String())
		}
		if fAbsent.controlModeUpdates.Load() != 0 || fAbsent.siteControlMode != siteControlModeRemoteAuto || len(fAbsent.dispatched) != 0 {
			t.Fatalf("expected no control-mode mutation or command dispatch when emergency point absent, updates=%d mode=%s dispatched=%d",
				fAbsent.controlModeUpdates.Load(), fAbsent.siteControlMode, len(fAbsent.dispatched))
		}
	})

	t.Run("plant_safe_shutdown_with_503_telemetry_outage_degrades_stops_chillers_and_respects_locks", func(t *testing.T) {
		f := newShutdownPlantFixture(t)
		f.telemetryMode[f.chillerADevID] = "UNAVAILABLE"
		f.telemetryMode[f.chillerBDevID] = "UNAVAILABLE"
		f.assetLocks[f.chillerBAssetID] = assetControlLockManual

		rec := f.postShutdown(`{"mode":"PLANT_SAFE_SHUTDOWN","reason":"遥测503断采安全停机","verificationCode":"123456"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected degraded 200 OK on 503 telemetry outage, got %d body=%s", rec.Code, rec.Body.String())
		}
		var resp siteControlShutdownResponse
		if json.NewDecoder(rec.Body).Decode(&resp) != nil {
			t.Fatalf("decode failed: %s", rec.Body.String())
		}
		if resp.SiteControlState.ControlMode != siteControlModeSafeShutdownExit {
			t.Fatalf("expected SAFE_SHUTDOWN_EXIT mode, got %s", resp.SiteControlState.ControlMode)
		}
		dispatched := f.dispatchedPointIDs()
		if !slices.Contains(dispatched, f.chillerAStopPointID) || slices.Contains(dispatched, f.chillerBStopPointID) {
			t.Fatalf("expected unlocked chiller-a STOP and locked chiller-b skipped, got %v", dispatched)
		}
		if !slices.Contains(resp.DeferredHoldingAssetIds, f.chwp1AssetID) ||
			!slices.Contains(resp.DeferredHoldingAssetIds, f.chwp2AssetID) ||
			!slices.Contains(resp.DeferredHoldingAssetIds, f.tower1AssetID) {
			t.Fatalf("expected holding auxiliaries deferred during telemetry outage, got %v", resp.DeferredHoldingAssetIds)
		}
	})

	t.Run("explicit_abort_unit_sequence_dispatches_edge_abort_without_pulsing_idle_chiller", func(t *testing.T) {
		f := newShutdownPlantFixture(t)
		f.includeAbortPoint = true
		f.runStates[f.chillerADevID] = "STOPPED"
		f.runStates[f.chillerBDevID] = "RUNNING"

		rec := f.postShutdown(`{"mode":"ABORT_UNIT_SEQUENCE","unitCode":"chiller-a","reason":"中止A组顺控计时器","verificationCode":"123456"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		dispatched := f.dispatchedPointIDs()
		if !slices.Contains(dispatched, f.chillerAAbortPointID) {
			t.Fatalf("expected explicit ABORT_UNIT_SEQUENCE point dispatched, got %v", dispatched)
		}
		if slices.Contains(dispatched, f.chillerAStopPointID) {
			t.Fatalf("must not pulse regular STOP on idle STOPPED chiller when explicit abort point exists, got %v", dispatched)
		}

		// Also verify cancelling a DISPATCHING START_UNIT_SEQUENCE aborts active sequence and dispatches explicit edge abort point.
		fCancel := newShutdownPlantFixture(t)
		fCancel.includeAbortPoint = true
		activeSeqID := "019a8000-dddd-7000-8000-000000000001"
		fCancel.listedCommands = []commandView{{
			SchemaVersion: 1, CommandID: activeSeqID, TenantID: fCancel.tenantID, SiteID: fCancel.siteID,
			DeviceID: fCancel.chillerADevID, PointID: fCancel.chillerAStartSeqPointID,
			Capability: commandmodel.CapabilityStartUnitSequence, CapabilityRevision: "capability:start-unit-sequence:v1",
			Status: commandmodel.IntentDispatching, Risk: commandmodel.RiskHigh,
			ApprovalPolicy: commandmodel.ApprovalNone, Parameters: commandmodel.CommandParameters{},
			DeviceCommandSequence: 1, Version: 4, SnapshotRevision: 1,
			CreatedAt: fCancel.currentTime.Add(-3 * time.Second), UpdatedAt: fCancel.currentTime,
			Transitions: []commandTransitionView{
				{ToStatus: commandmodel.IntentSubmitted, Reason: "COMMAND_SUBMITTED", ActorType: "PRINCIPAL", OccurredAt: fCancel.currentTime.Add(-3 * time.Second), Version: 1},
				{FromStatus: commandStatusPointer(commandmodel.IntentSubmitted), ToStatus: commandmodel.IntentValidating, Reason: "COMMAND_VALIDATING", ActorType: "WORKLOAD", OccurredAt: fCancel.currentTime.Add(-2 * time.Second), Version: 2},
				{FromStatus: commandStatusPointer(commandmodel.IntentValidating), ToStatus: commandmodel.IntentQueued, Reason: "COMMAND_QUEUED", ActorType: "WORKLOAD", OccurredAt: fCancel.currentTime.Add(-time.Second), Version: 3},
				{FromStatus: commandStatusPointer(commandmodel.IntentQueued), ToStatus: commandmodel.IntentDispatching, Reason: "DISPATCH_CLAIMED", ActorType: "WORKLOAD", OccurredAt: fCancel.currentTime, Version: 4},
			},
		}}
		fCancel.cancelledPendingIDs = []string{activeSeqID}
		cancelReq := httptest.NewRequest(http.MethodPost, "/api/v1/commands/"+activeSeqID+"/cancel", strings.NewReader(`{"reason":"操作员取消顺控"}`))
		fCancel.authenticate(cancelReq)
		cancelRec := httptest.NewRecorder()
		fCancel.handler.ServeHTTP(cancelRec, cancelReq)
		if cancelRec.Code != http.StatusOK {
			t.Fatalf("cancel DISPATCHING sequence status=%d body=%s", cancelRec.Code, cancelRec.Body.String())
		}
		if fCancel.lastCancelPending == nil || !fCancel.lastCancelPending.AbortActiveSequences {
			t.Fatalf("expected AbortActiveSequences=true on sequence cancel, got %#v", fCancel.lastCancelPending)
		}
		if !slices.Contains(fCancel.dispatchedPointIDs(), fCancel.chillerAAbortPointID) {
			t.Fatalf("expected explicit edge ABORT_UNIT_SEQUENCE dispatch on cancel, got %v", fCancel.dispatchedPointIDs())
		}
	})

	t.Run("safe_shutdown_control_state_auto_releases_aux_after_coast_down_and_never_blocks_offline", func(t *testing.T) {
		f := newShutdownPlantFixture(t)
		f.siteControlMode = siteControlModeSafeShutdownExit
		enteredRecently := f.currentTime.Add(-10 * time.Second).Format(time.RFC3339Nano)
		f.controlStateUpdatedAt = &enteredRecently
		f.runStates[f.chillerADevID] = "STOPPED"
		f.runStates[f.chillerBDevID] = "STOPPED"
		f.runStates[f.chwp1DevID] = "RUNNING"
		f.runStates[f.tower1DevID] = "RUNNING"
		f.assetLocks[f.tower1AssetID] = assetControlLockManual

		// Coast-down < 60s: GET control-state must not release pumps yet.
		getEarly := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+f.siteID+"/control-state", nil)
		f.authenticate(getEarly)
		recEarly := httptest.NewRecorder()
		f.handler.ServeHTTP(recEarly, getEarly)
		if recEarly.Code != http.StatusOK {
			t.Fatalf("early GET control-state status=%d body=%s", recEarly.Code, recEarly.Body.String())
		}
		if len(f.dispatched) != 0 {
			t.Fatalf("expected 0 aux STOPs before 60s coast-down, got %v", f.dispatchedPointIDs())
		}

		// After >= 60s coast-down: GET control-state auto-releases unlocked chwp-1 while preserving locked tower-1.
		f.currentTime = f.currentTime.Add(65 * time.Second)
		getReady := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+f.siteID+"/control-state", nil)
		f.authenticate(getReady)
		recReady := httptest.NewRecorder()
		f.handler.ServeHTTP(recReady, getReady)
		if recReady.Code != http.StatusOK {
			t.Fatalf("ready GET control-state status=%d body=%s", recReady.Code, recReady.Body.String())
		}
		dispatched := f.dispatchedPointIDs()
		if !slices.Contains(dispatched, f.chwp1StopPointID) || slices.Contains(dispatched, f.tower1StopPointID) {
			t.Fatalf("expected unlocked chwp-1 auto-released and locked tower-1 untouched, got %v", dispatched)
		}

		// Telemetry outage must never fail GET /control-state.
		f.telemetryMode[f.chillerADevID] = "UNAVAILABLE"
		f.telemetryMode[f.chwp1DevID] = "UNAVAILABLE"
		getOffline := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+f.siteID+"/control-state", nil)
		f.authenticate(getOffline)
		recOffline := httptest.NewRecorder()
		f.handler.ServeHTTP(recOffline, getOffline)
		if recOffline.Code != http.StatusOK {
			t.Fatalf("expected non-blocking 200 OK during offline telemetry, got %d", recOffline.Code)
		}
	})

	t.Run("emergency_do_release_emits_critical_operational_alarm", func(t *testing.T) {
		f := newShutdownPlantFixture(t)
		f.includeEmergencyPoint = true
		rec := f.postShutdown(`{"mode":"EMERGENCY_DO_RELEASE","confirmEmergencyDoRelease":true,"reason":"配电柜急停演练","verificationCode":"123456"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			list, err := f.alarmStore.List(context.Background(), f.tenantID, f.siteID, alarmservice.Filter{Limit: 10})
			if err == nil && len(list.Items) > 0 {
				alarm := list.Items[0]
				if alarm.SourceType != alarmmodel.SourceExternal ||
					alarm.AlarmType != "EMERGENCY_DO_RELEASE" ||
					alarm.CurrentSeverity != alarmmodel.SeverityCritical {
					t.Fatalf("unexpected EMERGENCY_DO_RELEASE alarm: %#v", alarm)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for EMERGENCY_DO_RELEASE operational alarm")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

type shutdownPlantFixture struct {
	handler                 *handler
	sessionID               string
	tenantID                string
	siteID                  string
	principalID             string
	chillerAAssetID         string
	chillerBAssetID         string
	chwp1AssetID            string
	chwp2AssetID            string
	tower1AssetID           string
	chillerADevID           string
	chillerBDevID           string
	chwp1DevID              string
	chwp2DevID              string
	tower1DevID             string
	emergencyDevID          string
	chillerARunPointID      string
	chillerBRunPointID      string
	chwp1RunPointID         string
	chwp2RunPointID         string
	tower1RunPointID        string
	emergencyRunPointID     string
	chillerAStopPointID     string
	chillerBStopPointID     string
	chwp1StopPointID        string
	chwp2StopPointID        string
	tower1StopPointID       string
	emergencyPointID        string
	chillerAAbortPointID    string
	chillerAStartSeqPointID string
	includeEmergencyPoint   bool
	includeAbortPoint       bool
	currentTime             time.Time
	controlStateUpdatedAt   *string
	alarmStore              *alarmservice.MemoryStore
	denyScheduleManage      atomic.Bool
	siteControlMode         string
	siteControlRevision     int64
	assetLocks              map[string]string
	runStates               map[string]string
	telemetryMode           map[string]string
	listedCommands          []commandView
	reconciledInputs        []internalCommandReconciliation
	cancelledPendingIDs     []string
	lastCancelPending       *internalCommandCancelPendingRequest
	dispatched              []internalCommandCreate
	controlModeUpdates      atomic.Int32
	commandSeq              atomic.Int32
}

func newShutdownPlantFixture(t *testing.T) *shutdownPlantFixture {
	t.Helper()
	now := time.Date(2026, 11, 15, 16, 0, 0, 0, time.UTC)
	gatewaySigner := commandTestSigner(t)
	iamSigner := commandTestSigner(t)
	alarmStore, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}

	f := &shutdownPlantFixture{
		tenantID:                "019a8000-1000-7000-8000-000000000001",
		siteID:                  "019a8000-2000-7000-8000-000000000001",
		principalID:             "019a8000-5000-7000-8000-000000000001",
		chillerAAssetID:         "019a8000-6000-7000-8000-000000000001",
		chillerBAssetID:         "019a8000-6000-7000-8000-000000000002",
		chwp1AssetID:            "019a8000-6000-7000-8000-000000000003",
		chwp2AssetID:            "019a8000-6000-7000-8000-000000000004",
		tower1AssetID:           "019a8000-6000-7000-8000-000000000005",
		chillerADevID:           "019a8000-3000-7000-8000-000000000001",
		chillerBDevID:           "019a8000-3000-7000-8000-000000000002",
		chwp1DevID:              "019a8000-3000-7000-8000-000000000003",
		chwp2DevID:              "019a8000-3000-7000-8000-000000000004",
		tower1DevID:             "019a8000-3000-7000-8000-000000000005",
		emergencyDevID:          "019a8000-3000-7000-8000-000000000006",
		chillerARunPointID:      "019a8000-7000-7000-8000-000000000001",
		chillerBRunPointID:      "019a8000-7000-7000-8000-000000000002",
		chwp1RunPointID:         "019a8000-7000-7000-8000-000000000003",
		chwp2RunPointID:         "019a8000-7000-7000-8000-000000000004",
		tower1RunPointID:        "019a8000-7000-7000-8000-000000000005",
		emergencyRunPointID:     "019a8000-7000-7000-8000-000000000006",
		chillerAStopPointID:     "019a8000-7100-7000-8000-000000000001",
		chillerBStopPointID:     "019a8000-7100-7000-8000-000000000002",
		chwp1StopPointID:        "019a8000-7100-7000-8000-000000000003",
		chwp2StopPointID:        "019a8000-7100-7000-8000-000000000004",
		tower1StopPointID:       "019a8000-7100-7000-8000-000000000005",
		emergencyPointID:        "019a8000-7100-7000-8000-000000000006",
		chillerAAbortPointID:    "019a8000-7100-7000-8000-000000000007",
		chillerAStartSeqPointID: "019a8000-7100-7000-8000-000000000008",
		currentTime:             now,
		alarmStore:              alarmStore,
		siteControlMode:         siteControlModeRemoteAuto,
		assetLocks:              map[string]string{},
		telemetryMode:           map[string]string{},
		runStates: map[string]string{
			"019a8000-3000-7000-8000-000000000001": "RUNNING",
			"019a8000-3000-7000-8000-000000000002": "RUNNING",
			"019a8000-3000-7000-8000-000000000003": "RUNNING",
			"019a8000-3000-7000-8000-000000000004": "RUNNING",
			"019a8000-3000-7000-8000-000000000005": "RUNNING",
			"019a8000-3000-7000-8000-000000000006": "RUNNING",
		},
	}

	iamClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		parent, err := identitycontext.VerifyDelegation(&gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("invalid gateway delegation: %v", err)
		}
		switch request.URL.Path {
		case "/internal/v1/principal/current":
			principal := identitycontext.UserPrincipal{Subject: parent.Subject, Issuer: parent.SubjectIssuer, Roles: []string{"site-manager"}}
			return telemetryJSONResponse(http.StatusOK, identitycontext.InternalPrincipalResponse{
				PrincipalID: f.principalID, Principal: principal,
				Context: identitycontext.PrincipalContext{
					InitiatingPrincipal: principal, ExecutingServicePrincipal: identitycontext.ServicePrincipal{Service: "platform-gateway", SPIFFEID: parent.ExecutingService},
					TenantID: parent.TenantID, Audience: parent.Audience, PolicyRevision: parent.PolicyRevision,
					DelegationExpiresAt: time.Unix(parent.ExpiresAt, 0).UTC().Format(time.RFC3339),
				},
				Authorization: identitycontext.EffectiveAuthorization{
					CapabilitySetVersion: identitycontext.CapabilitySetVersion, PolicyRevision: "registry-read:1/iam:1",
					Capabilities: []identitycontext.Capability{identitycontext.CapabilitySiteRead},
				},
			}), nil
		case "/internal/v1/registry/decision":
			var input registryauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				t.Fatal("invalid Registry decision request")
			}
			if input.Action == registryauth.ActionScheduleManage && f.denyScheduleManage.Load() {
				return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{
					Decision: registryauth.Decision{
						Allowed: false, PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
						TenantID: f.tenantID, Actions: []registryauth.Action{input.Action},
						PolicyRevision: "registry-read:1/iam:1", ReasonCode: registryauth.ReasonDenyActionNotGranted,
						DecidedAt: now.Format(time.RFC3339Nano),
					},
				}), nil
			}
			decision := registryauth.Decision{
				Allowed: true, PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, AllowedSiteIDs: []string{f.siteID},
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
			}
			claims := registryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
				PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: f.tenantID,
				AllowedSiteIDs: []string{f.siteID}, Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				IssuedAt: f.currentTime.Unix(), ExpiresAt: f.currentTime.Add(30 * time.Second).Unix(),
			}
			return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims)}), nil
		case telemetryDecisionPath:
			var input telemetryauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil || len(input.Targets) != 1 {
				t.Fatal("invalid Telemetry decision request")
			}
			target := input.Targets[0]
			canonical, _ := telemetryauth.CanonicalTargets(input.Targets)
			digest, _ := telemetryauth.ScopeDigest(input.Action, input.TenantID, canonical)
			decision := telemetryauth.Decision{
				Allowed: true, PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, Action: input.Action, ScopeDigest: digest,
				PolicyRevision: "identity-policy-1", ReasonCode: telemetryauth.ReasonAllowExactScope,
				DecidedAt: f.currentTime.Format(time.RFC3339Nano), Targets: []telemetryauth.AuthorizedTarget{{
					TenantID: f.tenantID, SiteID: f.siteID, DeviceID: target.DeviceID, Keys: target.Keys,
				}},
			}
			claims := telemetryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "telemetry-runtime-service",
				PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, ActorChain: []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: "spiffe://hvac.local/platform-gateway"}},
				Action: input.Action, ScopeDigest: digest, TargetCount: 1, KeyCount: len(target.Keys), PolicyRevision: "identity-policy-1",
				SessionID: parent.SessionID, ParentTokenID: parent.TokenID, RequestID: request.Header.Get("X-Request-ID"),
				TraceID: traceIDFromTraceparent(request.Header.Get("Traceparent")), Route: telemetryPublicRoute(input.Action),
				IssuedAt: f.currentTime.Unix(), ExpiresAt: f.currentTime.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
			}
			return telemetryJSONResponse(http.StatusOK, telemetryauth.DecisionResponse{Decision: decision, DelegationGrant: unsignedTelemetryGrant(claims)}), nil
		case commandDecisionPath:
			var input commandauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				t.Fatal("invalid Command decision request")
			}
			profile, _ := commandmodel.CapabilityProfileFor(input.Capability)
			decision := commandauth.Decision{
				Allowed: true, PrincipalID: f.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: f.tenantID, SiteID: f.siteID, DeviceID: input.DeviceID,
				Capability: input.Capability, CapabilityRevision: profile.Revision,
				Purpose: input.Purpose, MaximumRisk: commandmodel.RiskHigh,
				PolicyRevision: "identity-policy-1", EmergencyRevocationRevision: 3,
				ReasonCode: commandauth.ReasonAllowExactCapability, DecidedAt: f.currentTime.Format(time.RFC3339Nano),
			}
			grantID := randomURLToken(16)
			grant, err := commandauth.SignGrant(iamSigner, commandauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "command-service",
				GrantID: grantID, Purpose: decision.Purpose, PrincipalID: f.principalID,
				TenantID: f.tenantID, SiteID: f.siteID, DeviceID: input.DeviceID,
				Capability: decision.Capability, MaximumRisk: decision.MaximumRisk, CapabilityRevision: decision.CapabilityRevision,
				PolicyRevision: decision.PolicyRevision, EmergencyRevocationRevision: 3,
				IssuedAt: f.currentTime.Unix(), ExpiresAt: f.currentTime.Add(25 * time.Second).Unix(), TokenID: grantID,
			})
			if err != nil {
				t.Fatal(err)
			}
			return telemetryJSONResponse(http.StatusOK, commandauth.DecisionResponse{Decision: decision, DelegationGrant: grant}), nil
		default:
			t.Fatalf("unexpected IAM path %s", request.URL.Path)
			return nil, io.EOF
		}
	})}

	identityClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != identityTOTPVerifyPath {
			t.Fatalf("unexpected OIDC/Identity path %s", request.URL.Path)
		}
		var payload struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || payload.Code != "123456" {
			return telemetryJSONResponse(http.StatusUnauthorized, map[string]any{"error": "mfa_code_invalid"}), nil
		}
		return telemetryJSONResponse(http.StatusOK, map[string]any{"verified": true, "method": "TOTP"}), nil
	})}

	registryClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		ts := now.Format("2006-01-02T15:04:05.000Z")
		switch request.URL.Path {
		case "/internal/v1/registry/sites/" + f.siteID + "/control-state":
			return telemetryJSONResponse(http.StatusOK, f.buildControlStateView(ts)), nil
		case "/internal/v1/registry/sites/" + f.siteID + "/control-mode":
			var body struct {
				ControlMode string `json:"controlMode"`
			}
			if json.NewDecoder(request.Body).Decode(&body) != nil || body.ControlMode == "" {
				t.Fatal("invalid control-mode payload")
			}
			f.controlModeUpdates.Add(1)
			f.siteControlMode = body.ControlMode
			f.siteControlRevision++
			return telemetryJSONResponse(http.StatusOK, f.buildControlStateView(ts)), nil
		case "/internal/v1/registry/sites/" + f.siteID + "/asset-model":
			return telemetryJSONResponse(http.StatusOK, f.buildAssetModel(ts)), nil
		default:
			model := f.buildAssetModel(ts)
			for _, asset := range model.Assets {
				if request.URL.Path == "/internal/v1/registry/assets/"+asset.ID {
					return telemetryJSONResponse(http.StatusOK, asset), nil
				}
			}
			for _, device := range model.Devices {
				if request.URL.Path == "/internal/v1/registry/devices/"+device.ID {
					return telemetryJSONResponse(http.StatusOK, device), nil
				}
			}
			t.Fatalf("unexpected Registry request %s", request.URL.Path)
			return nil, io.EOF
		}
	})}

	telemetryClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
		deviceID := parts[len(parts)-2]
		if f.telemetryMode[deviceID] == "UNAVAILABLE" {
			return telemetryJSONResponse(http.StatusServiceUnavailable, map[string]any{"code": "TELEMETRY_UNAVAILABLE"}), nil
		}
		val := f.runStates[deviceID]
		if val == "" {
			val = "STOPPED"
		}
		rawVal, _ := json.Marshal(val)
		state := s2telemetryapi.DevicePresenceStateOnline
		display := s2telemetryapi.DeviceDisplayStateOnline
		instant := s2telemetryapi.Instant(f.currentTime.Add(-time.Second).Format(time.RFC3339Nano))
		policy := s2telemetryapi.PolicyRevision(5)
		readiness := s2telemetryapi.TelemetryReadinessCurrent
		freshness := "FRESH"
		if f.telemetryMode[deviceID] == "STALE" {
			readiness = s2telemetryapi.TelemetryReadinessDegraded
			freshness = "STALE"
		}
		snapshot := s2telemetryapi.DeviceObservationSnapshot{
			SchemaVersion: 1, TenantId: s2telemetryapi.UUIDv7(f.tenantID), SiteId: s2telemetryapi.UUIDv7(f.siteID), DeviceId: s2telemetryapi.UUIDv7(deviceID),
			BusinessRevision: 17, EvaluatedAt: s2telemetryapi.Instant(f.currentTime.Format(time.RFC3339Nano)),
			EvaluationAvailability: s2telemetryapi.EvaluationAvailabilityAvailable, AvailabilityReasons: []s2telemetryapi.AvailabilityReasonCode{},
			Presence:           s2telemetryapi.PresenceSnapshot{Applicability: s2telemetryapi.PresenceApplicabilityApplicable, CurrentState: &state, LastSeenAt: &instant, PolicyRevision: &policy},
			TelemetryReadiness: readiness, DisplayState: &display,
			Values: []s2telemetryapi.TelemetryKeyState{{Present: &s2telemetryapi.TelemetryPresentState{
				Key: s2telemetryapi.TelemetryKey("run_state"), State: "PRESENT", Value: json.RawMessage(rawVal), ValueType: "STRING",
				SampledAt: instant, ReceivedAt: instant, Freshness: freshness, Quality: s2telemetryapi.TelemetryQualityGood,
				QualityReasons: []s2telemetryapi.QualityReasonCode{}, PolicyRevision: policy,
			}}},
		}
		return telemetryJSONResponse(http.StatusOK, snapshot), nil
	})}

	commandClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == internalCommandsCancelPendingPath && request.Method == http.MethodPost {
			var cancelReq internalCommandCancelPendingRequest
			if json.NewDecoder(request.Body).Decode(&cancelReq) != nil {
				t.Fatal("invalid cancel-pending payload")
			}
			f.lastCancelPending = &cancelReq
			cancelled := f.cancelledPendingIDs
			if cancelled == nil {
				cancelled = []string{}
			}
			return telemetryJSONResponse(http.StatusOK, map[string]any{"cancelledCommandIds": cancelled}), nil
		}
		if strings.HasSuffix(request.URL.Path, "/reconcile") && request.Method == http.MethodPost {
			var input internalCommandReconciliation
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				t.Fatal("invalid reconcile payload")
			}
			f.reconciledInputs = append(f.reconciledInputs, input)
			for _, existing := range f.listedCommands {
				if strings.HasSuffix(request.URL.Path, "/"+existing.CommandID+"/reconcile") {
					reconciled := existing
					reconciled.Status = commandmodel.IntentFailed
					reconciled.Version++
					reconciled.UpdatedAt = now
					reconciled.Transitions = append(append([]commandTransitionView(nil), existing.Transitions...), commandTransitionView{
						FromStatus: commandStatusPointer(commandmodel.IntentOutcomeUnknown), ToStatus: commandmodel.IntentFailed,
						Reason: "OPERATOR_RECONCILED_NOT_APPLIED", ActorType: "PRINCIPAL", OccurredAt: now, Version: reconciled.Version,
					})
					return telemetryJSONResponse(http.StatusOK, reconciled), nil
				}
			}
			return telemetryJSONResponse(http.StatusNotFound, map[string]any{"code": "RESOURCE_NOT_FOUND"}), nil
		}
		if request.URL.Path == internalCommandsPath && request.Method == http.MethodPost {
			var input internalCommandCreate
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				t.Fatal("invalid internal command create")
			}
			f.dispatched = append(f.dispatched, input)
			seq := f.commandSeq.Add(1)
			cmdID := fmt.Sprintf("019a8000-cccc-7000-8000-%012d", seq)
			profile, _ := commandmodel.CapabilityProfileFor(input.Capability)
			params := input.Parameters
			if params == nil {
				params = commandmodel.CommandParameters{}
			}
			return telemetryJSONResponse(http.StatusAccepted, commandView{
				SchemaVersion: 1, CommandID: cmdID, TenantID: f.tenantID, SiteID: f.siteID,
				DeviceID: input.DeviceID, PointID: input.PointID, Capability: input.Capability,
				CapabilityRevision: profile.Revision, Status: commandmodel.IntentQueued,
				Risk: commandmodel.RiskHigh, ApprovalPolicy: commandmodel.ApprovalNone,
				Parameters: params, DeviceCommandSequence: uint64(seq), Version: 3, SnapshotRevision: 1,
				CreatedAt: now.Add(-2 * time.Second), UpdatedAt: now,
				Transitions: []commandTransitionView{
					{ToStatus: commandmodel.IntentSubmitted, Reason: "COMMAND_SUBMITTED", ActorType: "PRINCIPAL", OccurredAt: now.Add(-2 * time.Second), Version: 1},
					{FromStatus: commandStatusPointer(commandmodel.IntentSubmitted), ToStatus: commandmodel.IntentValidating, Reason: "COMMAND_VALIDATING", ActorType: "WORKLOAD", OccurredAt: now.Add(-time.Second), Version: 2},
					{FromStatus: commandStatusPointer(commandmodel.IntentValidating), ToStatus: commandmodel.IntentQueued, Reason: "COMMAND_QUEUED", ActorType: "WORKLOAD", OccurredAt: now, Version: 3},
				},
			}), nil
		}
		if request.URL.Path == internalCommandsPath && request.Method == http.MethodGet {
			listed := f.listedCommands
			if listed == nil {
				listed = []commandView{}
			}
			return telemetryJSONResponse(http.StatusOK, map[string][]commandView{"data": listed}), nil
		}
		if strings.HasPrefix(request.URL.Path, internalCommandsPath+"/") && request.Method == http.MethodGet {
			for _, existing := range f.listedCommands {
				if request.URL.Path == internalCommandsPath+"/"+existing.CommandID {
					view := existing
					if f.lastCancelPending != nil && f.lastCancelPending.AbortActiveSequences && slices.Contains(f.cancelledPendingIDs, existing.CommandID) && existing.Status == commandmodel.IntentDispatching {
						view.Status = commandmodel.IntentFailed
						view.Version++
						view.UpdatedAt = f.currentTime
						view.Transitions = append(append([]commandTransitionView(nil), existing.Transitions...), commandTransitionView{
							FromStatus: commandStatusPointer(commandmodel.IntentDispatching), ToStatus: commandmodel.IntentFailed,
							Reason: "ACTIVE_SEQUENCE_ABORTED", ActorType: "PRINCIPAL", OccurredAt: f.currentTime, Version: view.Version,
						})
					}
					return telemetryJSONResponse(http.StatusOK, view), nil
				}
			}
			return telemetryJSONResponse(http.StatusNotFound, map[string]any{"code": "RESOURCE_NOT_FOUND"}), nil
		}
		t.Fatalf("unexpected command backend call %s %s", request.Method, request.URL.Path)
		return nil, io.EOF
	})}

	store := sessionstore.NewMemoryStore()
	configured := NewHandler(Config{
		Now: func() time.Time { return f.currentTime },
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1", DelegationSigner: gatewaySigner,
			TokenEncryptionKey: make([]byte, 32), SessionStore: store, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
			IAMHTTPClient: iamClient, OIDCHTTPClient: identityClient,
		},
		Registry:  &RegistryConfig{CoreBaseURL: "https://registry.example.test", CoreHTTPClient: registryClient, CoreTimeout: time.Second},
		Telemetry: &TelemetryConfig{RuntimeBaseURL: "https://telemetry.example.test", RuntimeHTTPClient: telemetryClient, RuntimeAudience: "telemetry-runtime-service", Timeout: time.Second},
		Command:   &CommandConfig{BackendBaseURL: "https://command.example.test", BackendHTTPClient: commandClient, BackendAudience: "command-service", IAMGrantIssuer: "spiffe://hvac.local/iam-service", Timeout: time.Second},
		Alarm:     &AlarmConfig{Store: alarmStore, BackendAudience: "alarm-service", Timeout: time.Second},
	}).(*handler)
	f.handler = configured

	csrfCiphertext, err := configured.identity.encryptBytes([]byte(commandTestCSRFValue()))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{
		ID: "shutdown-plant-session", Client: sessionstore.ClientBrowser,
		Principal: identitycontext.UserPrincipal{Subject: "plant-manager", Issuer: "https://issuer.example.test", Roles: []string{"site-manager"}},
		TenantID:  f.tenantID, CSRFTokenCiphertext: csrfCiphertext, ExpiresAt: now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1",
		CorrelationID: "shutdown-fixture", TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.sessionID = created.ID
	return f
}

func (f *shutdownPlantFixture) buildControlStateView(ts string) siteControlStateView {
	locks := make([]assetControlLockView, 0, len(f.assetLocks))
	for assetID, lockMode := range f.assetLocks {
		locks = append(locks, assetControlLockView{
			ID:        "019a8000-9900-7000-8000-" + assetID[len(assetID)-12:],
			TenantID:  f.tenantID,
			SiteID:    f.siteID,
			AssetID:   assetID,
			LockMode:  lockMode,
			Reason:    "locked in fixture",
			UpdatedBy: &f.principalID,
			UpdatedAt: &ts,
			Revision:  1,
		})
	}
	return siteControlStateView{
		TenantID:    f.tenantID,
		SiteID:      f.siteID,
		ControlMode: f.siteControlMode,
		UpdatedAt:   f.controlStateUpdatedAt,
		Revision:    f.siteControlRevision,
		AssetLocks:  locks,
	}
}

func (f *shutdownPlantFixture) buildAssetModel(ts string) platformapi.SiteAssetModel {
	stopProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStop)
	assets := []platformapi.Asset{
		{ID: f.chillerAAssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chiller-a", DisplayName: "Chiller A", AssetType: "CHILLER", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chillerBAssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chiller-b", DisplayName: "Chiller B", AssetType: "CHILLER", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chwp1AssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chwp-1", DisplayName: "CHWP 1", AssetType: "CHILLED_WATER_PUMP", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chwp2AssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chwp-2", DisplayName: "CHWP 2", AssetType: "CHILLED_WATER_PUMP", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.tower1AssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "tower-1", DisplayName: "Tower 1", AssetType: "COOLING_TOWER", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
	}
	devices := []platformapi.Device{
		{ID: f.chillerADevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chiller-a", DisplayName: "Ctrl Chiller A", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chillerBDevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chiller-b", DisplayName: "Ctrl Chiller B", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chwp1DevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chwp-1", DisplayName: "Ctrl CHWP 1", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chwp2DevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chwp-2", DisplayName: "Ctrl CHWP 2", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.tower1DevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-tower-1", DisplayName: "Ctrl Tower 1", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
	}
	points := []platformapi.TelemetryPoint{
		{ID: f.chillerARunPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerADevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "Chiller A run", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chillerBRunPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerBDevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "Chiller B run", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chwp1RunPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chwp1DevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "CHWP 1 run", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chwp2RunPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chwp2DevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "CHWP 2 run", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.tower1RunPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.tower1DevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "Tower 1 run", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chillerAStopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerADevID, PointCode: "stop_chiller_a", SourceKey: "stop_chiller_a", DisplayName: "Stop Chiller A", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: f.chillerBStopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerBDevID, PointCode: "stop_chiller_b", SourceKey: "stop_chiller_b", DisplayName: "Stop Chiller B", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{
			ID: f.chwp1StopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chwp1DevID, PointCode: "stop_chwp_1", SourceKey: "stop_chwp_1", DisplayName: "Stop CHWP 1", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000,
			SourceMetadata: map[string]any{
				"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state",
				"interlockChecks": []any{map[string]any{"deviceId": f.chillerADevID, "key": "run_state", "expected": "STOPPED", "reason": "chiller-a must stop before stopping chwp-1"}},
			},
			Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts,
		},
		{
			ID: f.chwp2StopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chwp2DevID, PointCode: "stop_chwp_2", SourceKey: "stop_chwp_2", DisplayName: "Stop CHWP 2", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000,
			SourceMetadata: map[string]any{
				"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state",
				"interlockChecks": []any{
					map[string]any{"deviceId": f.chillerADevID, "key": "run_state", "expected": "STOPPED", "reason": "chiller-a must stop before stopping chwp-2"},
					map[string]any{"deviceId": f.chillerBDevID, "key": "run_state", "expected": "STOPPED", "reason": "chiller-b must stop before stopping chwp-2"},
				},
			},
			Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts,
		},
		{
			ID: f.tower1StopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.tower1DevID, PointCode: "stop_tower_1", SourceKey: "stop_tower_1", DisplayName: "Stop Tower 1", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000,
			SourceMetadata: map[string]any{
				"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state",
				"interlockChecks": []any{map[string]any{"deviceId": f.chillerBDevID, "key": "run_state", "expected": "STOPPED", "reason": "chiller-b must stop before stopping tower-1"}},
			},
			Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts,
		},
	}
	relationships := []platformapi.AssetRelationship{
		{ID: "019a8000-9100-7000-8000-000000000001", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerAStopPointID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: "019a8000-9100-7000-8000-000000000002", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerBStopPointID, ToType: "ASSET", ToID: f.chillerBAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: "019a8000-9100-7000-8000-000000000003", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chwp1StopPointID, ToType: "ASSET", ToID: f.chwp1AssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: "019a8000-9100-7000-8000-000000000004", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chwp2StopPointID, ToType: "ASSET", ToID: f.chwp2AssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: "019a8000-9100-7000-8000-000000000005", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.tower1StopPointID, ToType: "ASSET", ToID: f.tower1AssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: "019a8000-9200-7000-8000-000000000001", TenantID: f.tenantID, SiteID: f.siteID, FromType: "ASSET", FromID: f.chwp1AssetID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: "019a8000-9200-7000-8000-000000000002", TenantID: f.tenantID, SiteID: f.siteID, FromType: "ASSET", FromID: f.chwp2AssetID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: "019a8000-9200-7000-8000-000000000003", TenantID: f.tenantID, SiteID: f.siteID, FromType: "ASSET", FromID: f.chwp2AssetID, ToType: "ASSET", ToID: f.chillerBAssetID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		{ID: "019a8000-9200-7000-8000-000000000004", TenantID: f.tenantID, SiteID: f.siteID, FromType: "ASSET", FromID: f.tower1AssetID, ToType: "ASSET", ToID: f.chillerBAssetID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
	}
	if f.includeEmergencyPoint {
		devices = append(devices, platformapi.Device{ID: f.emergencyDevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-emergency-do", DisplayName: "Emergency DO Relay", DeviceType: "EDGE_GATEWAY", Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts})
		points = append(points,
			platformapi.TelemetryPoint{ID: f.emergencyRunPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.emergencyDevID, PointCode: "run_state", SourceKey: "run_state", DisplayName: "Emergency relay state", PointType: "STATE", ValueType: "STRING", Writable: false, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			platformapi.TelemetryPoint{ID: f.emergencyPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.emergencyDevID, PointCode: "emergency_do_release", SourceKey: "emergency_do_release", DisplayName: "Emergency DO Release", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProfile.Revision, "actuationMode": "EMERGENCY_DO_RELEASE", "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		)
		relationships = append(relationships, platformapi.AssetRelationship{ID: "019a8000-9100-7000-8000-000000000006", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.emergencyPointID, ToType: "SITE", ToID: f.siteID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts})
	}
	if f.includeAbortPoint {
		startSeqProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStartUnitSequence)
		stopSeqProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStopUnitSequence)
		points = append(points,
			platformapi.TelemetryPoint{ID: f.chillerAAbortPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerADevID, PointCode: "abort_unit_sequence", SourceKey: "abort_unit_sequence", DisplayName: "Abort Unit A Sequence", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "STOP_UNIT_SEQUENCE", "capabilityRevision": stopSeqProfile.Revision, "actuationMode": "ABORT_UNIT_SEQUENCE", "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			platformapi.TelemetryPoint{ID: f.chillerAStartSeqPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerADevID, PointCode: "start_unit_sequence", SourceKey: "start_unit_sequence", DisplayName: "Start Unit A Sequence", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, SampleIntervalMS: 60000, PublishIntervalMS: 60000, StaleAfterMS: 180000, SourceMetadata: map[string]any{"capability": "START_UNIT_SEQUENCE", "capabilityRevision": startSeqProfile.Revision, "actuationMode": "UNIT_SEQUENCE", "feedbackPointKey": "run_state"}, Status: "ACTIVE", Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		)
		relationships = append(relationships,
			platformapi.AssetRelationship{ID: "019a8000-9100-7000-8000-000000000007", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerAAbortPointID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
			platformapi.AssetRelationship{ID: "019a8000-9100-7000-8000-000000000008", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerAStartSeqPointID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts, Revision: 1, CreatedAt: ts, UpdatedAt: ts},
		)
	}
	return platformapi.SiteAssetModel{
		SchemaVersion: 2, TenantID: f.tenantID, SiteID: f.siteID,
		Spaces: []platformapi.Space{}, Assets: assets, Devices: devices, Sensors: []platformapi.Sensor{}, TelemetryPoints: points, Relationships: relationships,
		Counts: platformapi.AssetModelCounts{Assets: len(assets), DeviceEndpoints: len(devices), Points: len(points)},
	}
}

func (f *shutdownPlantFixture) authenticate(request *http.Request) {
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: f.sessionID})
	request.Header.Set("Origin", "https://web.example.test")
	request.Header.Set("X-CSRF-Token", commandTestCSRFValue())
	request.Header.Set("Content-Type", "application/json")
}

func (f *shutdownPlantFixture) postShutdown(body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+f.siteID+"/control-shutdown", strings.NewReader(body))
	f.authenticate(req)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *shutdownPlantFixture) submitDirectCommand(assetID, pointID, totpCode, idemKey string) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"assetId":%q,"commandPointId":%q,"parameters":{},"verificationCode":%q}`, assetID, pointID, totpCode)
	req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	f.authenticate(req)
	req.Header.Set("Idempotency-Key", idemKey)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *shutdownPlantFixture) dispatchedPointIDs() []string {
	ids := make([]string, 0, len(f.dispatched))
	for _, cmd := range f.dispatched {
		ids = append(ids, cmd.PointID)
	}
	return ids
}
