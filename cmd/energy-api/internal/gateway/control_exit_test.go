package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestGatewaySiteControlStateAndControlModeExit(t *testing.T) {
	fixture := newCommandGatewayFixture(t)

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
	fixture.control.IAM.DenyScheduleManage = true
	forbidReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"MANUAL_OVERRIDE","reason":"切到本地手动"}`))
	fixture.authenticate(forbidReq, true)
	forbidReq.Header.Set("Content-Type", "application/json")
	forbidRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(forbidRec, forbidReq)
	if forbidRec.Code != http.StatusForbidden {
		t.Fatalf("operator PUT control-mode status=%d want 403 body=%s", forbidRec.Code, forbidRec.Body.String())
	}
	fixture.control.IAM.DenyScheduleManage = false

	// Seed a pending QUEUED command, then switch to MANUAL_OVERRIDE -> cancels pending command cleanly.
	pendingID := fixture.mustSubmitLowCommand(t, "site-mode-cancel-seed")
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
		!slices.Equal(overrideResp.CancelledCommandIDs, []string{pendingID}) {
		t.Fatalf("unexpected MANUAL_OVERRIDE response %#v", overrideResp)
	}

	// Subsequent non-shutdown POST /api/v1/commands returns 409 COMMAND_CONTROL_LOCKED without calling IAM grant.
	fixture.control.IAM.CommandAuthorizeCalls = 0
	cmdReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":23.5}}`))
	fixture.authenticate(cmdReq, true)
	cmdReq.Header.Set("Content-Type", "application/json")
	cmdReq.Header.Set("Idempotency-Key", "cmd-blocked-manual-override")
	cmdRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(cmdRec, cmdReq)
	var problem platformapi.ProblemDetails
	if cmdRec.Code != http.StatusConflict || json.NewDecoder(cmdRec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_CONTROL_LOCKED" {
		t.Fatalf("expected 409 COMMAND_CONTROL_LOCKED, got %d body=%s", cmdRec.Code, cmdRec.Body.String())
	}
	if fixture.control.IAM.CommandAuthorizeCalls != 0 {
		t.Fatalf("expected 0 IAM command grant calls, got %d", fixture.control.IAM.CommandAuthorizeCalls)
	}

	// Return to REMOTE_AUTO to test SAFE_SHUTDOWN_EXIT precondition when a command is DISPATCHING.
	autoReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"REMOTE_AUTO","reason":"恢复远控","expectedRevision":1}`))
	fixture.authenticate(autoReq, true)
	autoReq.Header.Set("Content-Type", "application/json")
	autoRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(autoRec, autoReq)
	if autoRec.Code != http.StatusOK {
		t.Fatalf("restore REMOTE_AUTO status=%d body=%s", autoRec.Code, autoRec.Body.String())
	}

	_ = fixture.mustSubmitLowCommand(t, "disp-precond-seed")
	env, err := fixture.control.Store.ClaimDispatch(context.Background(), fixture.tenantID, "disp-worker", 30*time.Second)
	if err != nil {
		t.Fatalf("claim dispatch: %v", err)
	}

	blockedExitReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"SAFE_SHUTDOWN_EXIT","reason":"安全停机退出","expectedRevision":2}`))
	fixture.authenticate(blockedExitReq, true)
	blockedExitReq.Header.Set("Content-Type", "application/json")
	blockedExitRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(blockedExitRec, blockedExitReq)
	if blockedExitRec.Code != http.StatusConflict || json.NewDecoder(blockedExitRec.Body).Decode(&problem) != nil || problem.Code != "SAFE_SHUTDOWN_PRECONDITION_FAILED" {
		t.Fatalf("expected 409 SAFE_SHUTDOWN_PRECONDITION_FAILED, got %d body=%s", blockedExitRec.Code, blockedExitRec.Body.String())
	}

	// Resolve active DISPATCHING command -> SAFE_SHUTDOWN_EXIT succeeds.
	_ = fixture.control.Store.ResolveDispatch(context.Background(), env, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "CANCELLED_BEFORE_SEND",
	})
	exitReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"SAFE_SHUTDOWN_EXIT","reason":"安全停机退出","expectedRevision":2}`))
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
	pendingID := fixture.mustSubmitLowCommand(t, "lock-cancel-seed")

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
		!slices.Equal(lockResp.CancelledCommandIDs, []string{pendingID}) {
		t.Fatalf("unexpected control-lock response %#v", lockResp)
	}

	// Commands targeting locked asset are rejected with 409 COMMAND_CONTROL_LOCKED.
	cmdReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":23.5}}`))
	fixture.authenticate(cmdReq, true)
	cmdReq.Header.Set("Content-Type", "application/json")
	cmdReq.Header.Set("Idempotency-Key", "cmd-blocked-asset-lock")
	cmdRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(cmdRec, cmdReq)
	var problem platformapi.ProblemDetails
	if cmdRec.Code != http.StatusConflict || json.NewDecoder(cmdRec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_CONTROL_LOCKED" {
		t.Fatalf("expected 409 COMMAND_CONTROL_LOCKED, got %d body=%s", cmdRec.Code, cmdRec.Body.String())
	}

	// Unlock restores command submission.
	unlockReq := httptest.NewRequest(http.MethodPut, "/api/v1/assets/"+fixture.assetID+"/control-lock", strings.NewReader(`{"lockMode":"UNLOCKED","reason":"维保结束解锁","expectedRevision":1}`))
	fixture.authenticate(unlockReq, true)
	unlockReq.Header.Set("Content-Type", "application/json")
	unlockRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(unlockRec, unlockReq)
	if unlockRec.Code != http.StatusOK {
		t.Fatalf("PUT UNLOCKED status=%d body=%s", unlockRec.Code, unlockRec.Body.String())
	}
	retryReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":23.5}}`))
	fixture.authenticate(retryReq, true)
	retryReq.Header.Set("Content-Type", "application/json")
	retryReq.Header.Set("Idempotency-Key", "cmd-after-unlock")
	retryRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(retryRec, retryReq)
	if retryRec.Code != http.StatusAccepted {
		t.Fatalf("command after unlock status=%d body=%s", retryRec.Code, retryRec.Body.String())
	}
}

func TestGatewaySiteControlStateHidesUnauthorizedSite(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sites/018f3e00-2000-7000-8000-000000000002/control-state", nil)
	fixture.authenticate(req, false)
	rec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unauthorized GET control-state status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestGatewaySiteControlModePreservesMaintenanceWindowView(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	window := &activeMaintenanceWindowView{
		ID:        "019a0000-1111-7000-8000-000000000001",
		Title:     "冷却塔年度清洗与冷机保养窗口",
		StartDate: "2026-10-13",
		EndDate:   "2026-10-16",
		Reason:    "季度维保期间暂停自动寻优与非急停控制",
	}
	fixture.control.Twin.ControlStates[fixture.siteID] = siteControlStateView{
		TenantID: fixture.tenantID, SiteID: fixture.siteID, ControlMode: siteControlModeRemoteAuto,
		AssetLocks: []assetControlLockView{}, ActiveMaintenanceWindow: window,
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/control-state", nil)
	fixture.authenticate(getReq, false)
	getRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(getRec, getReq)
	var state siteControlStateView
	if getRec.Code != http.StatusOK || json.NewDecoder(getRec.Body).Decode(&state) != nil ||
		state.ActiveMaintenanceWindow == nil || *state.ActiveMaintenanceWindow != *window {
		t.Fatalf("GET control-state=%#v body=%s", state, getRec.Body.String())
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"MANUAL_OVERRIDE","reason":"维保期间切为现场手动"}`))
	fixture.authenticate(putReq, true)
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(putRec, putReq)
	var updated siteControlModeMutationResponse
	if putRec.Code != http.StatusOK || json.NewDecoder(putRec.Body).Decode(&updated) != nil ||
		updated.ActiveMaintenanceWindow == nil || *updated.ActiveMaintenanceWindow != *window {
		t.Fatalf("PUT control-mode=%#v body=%s", updated, putRec.Body.String())
	}
}

func TestGatewaySiteMaintenanceWindowBlocksControlBeforeTelemetryAndAllowsSafeActions(t *testing.T) {
	window := &activeMaintenanceWindowView{
		ID:        "019a0000-2222-7000-8000-000000000002",
		Title:     "冷水机组年度大修窗口",
		StartDate: "2026-11-10",
		EndDate:   "2026-11-12",
		Reason:    "冷机解体检修，禁止启动及设定点改动",
	}

	for _, tc := range []struct {
		name       string
		capability commandmodel.Capability
		paramsJSON string
	}{
		{name: "START", capability: commandmodel.CapabilityStart, paramsJSON: `{}`},
		{name: "START_UNIT_SEQUENCE", capability: commandmodel.CapabilityStartUnitSequence, paramsJSON: `{}`},
		{name: "SET_TEMPERATURE_SETPOINT", capability: commandmodel.CapabilitySetTemperatureSetpoint, paramsJSON: `{"setpointC":23.5}`},
	} {
		t.Run("reject_"+tc.name, func(t *testing.T) {
			fixture := newCommandGatewayFixture(t)
			if tc.capability != commandmodel.CapabilitySetTemperatureSetpoint {
				fixture.setCommandPointCapability(tc.capability, "", nil)
			}
			fixture.control.Twin.ControlStates[fixture.siteID] = siteControlStateView{
				TenantID: fixture.tenantID, SiteID: fixture.siteID, ControlMode: siteControlModeRemoteAuto,
				AssetLocks: []assetControlLockView{}, ActiveMaintenanceWindow: window,
			}

			body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":` + tc.paramsJSON + `}`
			req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
			fixture.authenticate(req, true)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "cmd-maint-block-"+strings.ToLower(string(tc.capability)))
			rec := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rec, req)

			var problem platformapi.ProblemDetails
			if rec.Code != http.StatusConflict || json.NewDecoder(rec.Body).Decode(&problem) != nil ||
				problem.Code != "COMMAND_CONTROL_LOCKED" || problem.Title != "Site maintenance window active" {
				t.Fatalf("%s status=%d problem=%#v body=%s", tc.name, rec.Code, problem, rec.Body.String())
			}
			if !strings.Contains(problem.Detail, window.Title) || !strings.Contains(problem.Detail, window.StartDate+".."+window.EndDate) {
				t.Fatalf("expected detail to name window %q and %s..%s, got %q", window.Title, window.StartDate, window.EndDate, problem.Detail)
			}
			if fixture.identityCalls.Load() != 0 || fixture.control.IAM.CommandAuthorizeCalls != 0 {
				t.Fatalf("expected 0 identity/grant calls, got identity=%d grant=%d", fixture.identityCalls.Load(), fixture.control.IAM.CommandAuthorizeCalls)
			}
		})
	}

	for _, tc := range []struct {
		name             string
		capability       commandmodel.Capability
		verificationCode string
	}{
		{name: "STOP", capability: commandmodel.CapabilityStop, verificationCode: "123456"},
		{name: "STOP_UNIT_SEQUENCE", capability: commandmodel.CapabilityStopUnitSequence, verificationCode: "123456"},
		{name: "RESET_FAULT", capability: commandmodel.CapabilityResetFault},
	} {
		t.Run("permit_"+tc.name, func(t *testing.T) {
			fixture := newCommandGatewayFixture(t)
			fixture.setCommandPointCapability(tc.capability, "", nil)
			fixture.control.Twin.ControlStates[fixture.siteID] = siteControlStateView{
				TenantID: fixture.tenantID, SiteID: fixture.siteID, ControlMode: siteControlModeRemoteAuto,
				AssetLocks: []assetControlLockView{}, ActiveMaintenanceWindow: window,
			}

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
		})
	}
}

func TestGatewaySiteControlExitAllowsStopWhileBlockingStartAndSet(t *testing.T) {
	modes := []string{siteControlModeManualOverride, siteControlModeSafeShutdownExit}
	for _, mode := range modes {
		for _, cap := range []commandmodel.Capability{commandmodel.CapabilityStop, commandmodel.CapabilityStopUnitSequence} {
			t.Run(mode+"/allows_"+string(cap), func(t *testing.T) {
				fixture := newCommandGatewayFixture(t)
				fixture.setCommandPointCapability(cap, "", nil)
				fixture.control.Twin.ControlStates[fixture.siteID] = siteControlStateView{
					TenantID: fixture.tenantID, SiteID: fixture.siteID, ControlMode: mode, AssetLocks: []assetControlLockView{},
				}
				body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"verificationCode":"123456"}`
				req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
				fixture.authenticate(req, true)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "cmd-exit-allow-"+strings.ToLower(mode)+"-"+strings.ToLower(string(cap)))
				rec := httptest.NewRecorder()
				fixture.handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusAccepted {
					t.Fatalf("mode=%s cap=%s status=%d want 202 body=%s", mode, cap, rec.Code, rec.Body.String())
				}
			})
		}
		for _, tc := range []struct {
			capability commandmodel.Capability
			paramsJSON string
		}{
			{capability: commandmodel.CapabilityStart, paramsJSON: `{}`},
			{capability: commandmodel.CapabilityStartUnitSequence, paramsJSON: `{}`},
			{capability: commandmodel.CapabilityResetFault, paramsJSON: `{}`},
			{capability: commandmodel.CapabilitySetTemperatureSetpoint, paramsJSON: `{"setpointC":23.5}`},
		} {
			t.Run(mode+"/blocks_"+string(tc.capability), func(t *testing.T) {
				fixture := newCommandGatewayFixture(t)
				if tc.capability != commandmodel.CapabilitySetTemperatureSetpoint {
					fixture.setCommandPointCapability(tc.capability, "", nil)
				}
				fixture.control.Twin.ControlStates[fixture.siteID] = siteControlStateView{
					TenantID: fixture.tenantID, SiteID: fixture.siteID, ControlMode: mode, AssetLocks: []assetControlLockView{},
				}
				body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":` + tc.paramsJSON + `}`
				req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
				fixture.authenticate(req, true)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "cmd-exit-block-"+strings.ToLower(mode)+"-"+strings.ToLower(string(tc.capability)))
				rec := httptest.NewRecorder()
				fixture.handler.ServeHTTP(rec, req)
				var problem platformapi.ProblemDetails
				if rec.Code != http.StatusConflict || json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_CONTROL_LOCKED" {
					t.Fatalf("mode=%s cap=%s status=%d problem=%#v body=%s", mode, tc.capability, rec.Code, problem, rec.Body.String())
				}
				if fixture.control.IAM.CommandAuthorizeCalls != 0 {
					t.Fatalf("expected 0 IAM command grant calls, got %d", fixture.control.IAM.CommandAuthorizeCalls)
				}
			})
		}
	}
}

func TestGatewayManualOverrideStopStillEnforcesHydraulicInterlock(t *testing.T) {
	f := newShutdownPlantFixture(t)
	f.control.Twin.ControlStates[f.siteID] = siteControlStateView{
		TenantID: f.tenantID, SiteID: f.siteID, ControlMode: siteControlModeManualOverride, AssetLocks: []assetControlLockView{},
	}
	f.setRunState(f.chillerADevID, "RUNNING", 21)

	blocked := f.submitDirectCommand(f.chwp1AssetID, f.chwp1StopPointID, "123456", "manual-stop-pump-while-chiller-running")
	var problem platformapi.ProblemDetails
	if blocked.Code != http.StatusConflict || json.NewDecoder(blocked.Body).Decode(&problem) != nil ||
		problem.Code != "COMMAND_INTERLOCK_BLOCKED" || f.control.IAM.CommandAuthorizeCalls != 0 {
		t.Fatalf("expected 409 COMMAND_INTERLOCK_BLOCKED with 0 grants, status=%d grants=%d problem=%#v",
			blocked.Code, f.control.IAM.CommandAuthorizeCalls, problem)
	}

	f.setRunState(f.chillerADevID, "STOPPED", 22)
	allowed := f.submitDirectCommand(f.chwp1AssetID, f.chwp1StopPointID, "123456", "manual-stop-pump-after-chiller-stopped")
	if allowed.Code != http.StatusAccepted {
		t.Fatalf("expected 202 release after chiller stopped, status=%d body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestGatewaySiteControlShutdown(t *testing.T) {
	t.Run("forbidden_for_operator_before_step_up", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			body string
		}{
			{name: "without_code", body: `{"mode":"PLANT_SAFE_SHUTDOWN","reason":"全站停机"}`},
			{name: "with_verified_code", body: `{"mode":"PLANT_SAFE_SHUTDOWN","reason":"全站停机","verificationCode":"123456"}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newShutdownPlantFixture(t)
				f.control.IAM.DenyScheduleManage = true
				rec := f.postShutdown(tc.body)
				var problem platformapi.ProblemDetails
				if rec.Code != http.StatusForbidden || json.NewDecoder(rec.Body).Decode(&problem) != nil ||
					problem.Code != "CONTROL_EXIT_FORBIDDEN" {
					t.Fatalf("status=%d want 403 CONTROL_EXIT_FORBIDDEN problem=%#v body=%s", rec.Code, problem, rec.Body.String())
				}
			})
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
		f.setRunState(f.chillerADevID, "RUNNING", 30)
		f.setRunState(f.chillerBDevID, "STOPPED", 31)
		pendingID := f.mustSeedQueuedCommand(t, f.chillerADevID, f.chillerAStopPointID, "seed-plant-safe-cancel")

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
			resp.SiteControlState.Revision != 1 ||
			!slices.Equal(resp.CancelledCommandIDs, []string{pendingID}) {
			t.Fatalf("unexpected shutdown state %#v", resp)
		}
		dispatchedPoints := f.submittedPointIDs(t, resp.SubmittedCommandIDs)
		if !slices.Contains(dispatchedPoints, f.chillerAStopPointID) ||
			!slices.Contains(dispatchedPoints, f.tower1StopPointID) ||
			slices.Contains(dispatchedPoints, f.chillerBStopPointID) ||
			slices.Contains(dispatchedPoints, f.chwp1StopPointID) ||
			slices.Contains(dispatchedPoints, f.chwp2StopPointID) ||
			len(resp.SubmittedCommandIDs) != 2 {
			t.Fatalf("unexpected submitted points=%v ids=%v", dispatchedPoints, resp.SubmittedCommandIDs)
		}
		if !slices.Contains(resp.DeferredHoldingAssetIds, f.chwp1AssetID) ||
			!slices.Contains(resp.DeferredHoldingAssetIds, f.chwp2AssetID) ||
			slices.Contains(resp.DeferredHoldingAssetIds, f.tower1AssetID) {
			t.Fatalf("unexpected deferredHoldingAssetIds=%v", resp.DeferredHoldingAssetIds)
		}
	})

	t.Run("abort_unit_sequence_stops_chiller_only_while_chiller_running", func(t *testing.T) {
		f := newShutdownPlantFixture(t)
		f.setRunState(f.chillerADevID, "RUNNING", 30)
		pendingUnitID := f.mustSeedQueuedCommand(t, f.chwp1DevID, f.chwp1StopPointID, "seed-abort-cancel")

		rec := f.postShutdown(`{"mode":"ABORT_UNIT_SEQUENCE","unitCode":"chiller-a","reason":"中止A组顺控","verificationCode":"123456"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp siteControlShutdownResponse
		if json.NewDecoder(rec.Body).Decode(&resp) != nil {
			t.Fatalf("decode failed: %s", rec.Body.String())
		}
		if resp.SiteControlState.ControlMode != siteControlModeRemoteAuto ||
			!slices.Equal(resp.CancelledCommandIDs, []string{pendingUnitID}) {
			t.Fatalf("unexpected abort response %#v", resp)
		}
		dispatchedPoints := f.submittedPointIDs(t, resp.SubmittedCommandIDs)
		if !slices.Equal(dispatchedPoints, []string{f.chillerAStopPointID}) || len(resp.SubmittedCommandIDs) != 1 {
			t.Fatalf("expected only chiller-a STOP dispatched, got points=%v", dispatchedPoints)
		}
		if !slices.Contains(resp.DeferredHoldingAssetIds, f.chwp1AssetID) ||
			!slices.Contains(resp.DeferredHoldingAssetIds, f.chwp2AssetID) {
			t.Fatalf("expected chwp-1 and chwp-2 deferred while chiller-a running, got %v", resp.DeferredHoldingAssetIds)
		}
	})

	t.Run("abort_unit_sequence_after_chiller_stopped_releases_safe_pump_and_handles_locks", func(t *testing.T) {
		for _, unitCode := range []string{"chiller-a", "unit-a"} {
			t.Run(unitCode, func(t *testing.T) {
				f := newShutdownPlantFixture(t)
				f.setRunState(f.chillerADevID, "STOPPED", 30)
				f.setRunState(f.chillerBDevID, "RUNNING", 31)

				body := fmt.Sprintf(`{"mode":"ABORT_UNIT_SEQUENCE","unitCode":%q,"reason":"释放A组闲置水泵保持通道","verificationCode":"123456"}`, unitCode)
				rec := f.postShutdown(body)
				if rec.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
				}
				var resp siteControlShutdownResponse
				if json.NewDecoder(rec.Body).Decode(&resp) != nil {
					t.Fatalf("decode failed: %s", rec.Body.String())
				}
				dispatchedPoints := f.submittedPointIDs(t, resp.SubmittedCommandIDs)
				if !slices.Contains(dispatchedPoints, f.chillerAStopPointID) ||
					!slices.Contains(dispatchedPoints, f.chwp1StopPointID) ||
					len(resp.SubmittedCommandIDs) != 2 {
					t.Fatalf("expected chiller-a and chwp-1 STOP, got points=%v", dispatchedPoints)
				}
				if !slices.Equal(resp.DeferredHoldingAssetIds, []string{f.chwp2AssetID}) {
					t.Fatalf("expected shared chwp-2 deferred while chiller-b running, got %v", resp.DeferredHoldingAssetIds)
				}
			})
		}

		// Unlocked chiller still receives STOP while locked auxiliary is deferred.
		fAuxLocked := newShutdownPlantFixture(t)
		fAuxLocked.setRunState(fAuxLocked.chillerBDevID, "STOPPED", 30)
		fAuxLocked.control.Twin.ControlStates[fAuxLocked.siteID] = siteControlStateView{
			TenantID: fAuxLocked.tenantID, SiteID: fAuxLocked.siteID, ControlMode: siteControlModeRemoteAuto,
			AssetLocks: []assetControlLockView{{AssetID: fAuxLocked.tower1AssetID, SiteID: fAuxLocked.siteID, LockMode: assetControlLockManual}},
		}
		recAuxLocked := fAuxLocked.postShutdown(`{"mode":"ABORT_UNIT_SEQUENCE","unitCode":"chiller-b","reason":"B组停机收尾","verificationCode":"123456"}`)
		if recAuxLocked.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recAuxLocked.Code, recAuxLocked.Body.String())
		}
		var respAuxLocked siteControlShutdownResponse
		if json.NewDecoder(recAuxLocked.Body).Decode(&respAuxLocked) != nil {
			t.Fatalf("decode failed: %s", recAuxLocked.Body.String())
		}
		if !slices.Equal(fAuxLocked.submittedPointIDs(t, respAuxLocked.SubmittedCommandIDs), []string{fAuxLocked.chillerBStopPointID}) ||
			!slices.Contains(respAuxLocked.DeferredHoldingAssetIds, fAuxLocked.tower1AssetID) {
			t.Fatalf("expected chiller-b STOP and locked tower-1 deferred, got %#v", respAuxLocked)
		}

		// When all target assets are locked and nothing was cancelled or submitted, return 409 CONTROL_SHUTDOWN_LOCKED.
		fAllLocked := newShutdownPlantFixture(t)
		fAllLocked.setRunState(fAllLocked.chillerBDevID, "STOPPED", 30)
		fAllLocked.control.Twin.ControlStates[fAllLocked.siteID] = siteControlStateView{
			TenantID: fAllLocked.tenantID, SiteID: fAllLocked.siteID, ControlMode: siteControlModeRemoteAuto,
			AssetLocks: []assetControlLockView{
				{AssetID: fAllLocked.chillerBAssetID, SiteID: fAllLocked.siteID, LockMode: assetControlLockMaintenance},
				{AssetID: fAllLocked.tower1AssetID, SiteID: fAllLocked.siteID, LockMode: assetControlLockManual},
			},
		}
		recAllLocked := fAllLocked.postShutdown(`{"mode":"ABORT_UNIT_SEQUENCE","unitCode":"unit-b","reason":"全锁拦截验证","verificationCode":"123456"}`)
		var lockedProblem platformapi.ProblemDetails
		if recAllLocked.Code != http.StatusConflict || json.NewDecoder(recAllLocked.Body).Decode(&lockedProblem) != nil ||
			lockedProblem.Code != "CONTROL_SHUTDOWN_LOCKED" {
			t.Fatalf("status=%d want 409 CONTROL_SHUTDOWN_LOCKED problem=%#v body=%s", recAllLocked.Code, lockedProblem, recAllLocked.Body.String())
		}
	})

	t.Run("emergency_do_release_requires_point_reconciles_unknown_and_tolerates_degraded_telemetry", func(t *testing.T) {
		fConfigured := newShutdownPlantFixtureWithEmergency(t, true)
		pendingID := fConfigured.mustSeedQueuedCommand(t, fConfigured.chillerADevID, fConfigured.chillerAStopPointID, "seed-emerg-cancel")
		rec := fConfigured.postShutdown(`{"mode":"EMERGENCY_DO_RELEASE","confirmEmergencyDoRelease":true,"reason":"紧急全断DO通道","verificationCode":"123456"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp siteControlShutdownResponse
		if json.NewDecoder(rec.Body).Decode(&resp) != nil {
			t.Fatalf("decode failed: %s", rec.Body.String())
		}
		if resp.SiteControlState.ControlMode != siteControlModeSafeShutdownExit ||
			!slices.Equal(resp.CancelledCommandIDs, []string{pendingID}) ||
			!slices.Equal(fConfigured.submittedPointIDs(t, resp.SubmittedCommandIDs), []string{fConfigured.emergencyPointID}) ||
			len(resp.DeferredHoldingAssetIds) != 0 {
			t.Fatalf("unexpected configured emergency response=%#v", resp)
		}

		// Reconciles OUTCOME_UNKNOWN command on emergency DO device and proceeds under STALE or UNAVAILABLE telemetry with explicit EMERGENCY_BYPASS.
		for _, mode := range []string{"STALE", "UNAVAILABLE"} {
			t.Run("degraded_"+strings.ToLower(mode), func(t *testing.T) {
				fDegraded := newShutdownPlantFixtureWithEmergency(t, true)
				if mode == "STALE" {
					fDegraded.control.Live.Set(fDegraded.emergencyDevID, "run_state", &commandmodel.LivePointObservation{
						EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "STALE", Quality: "STALE",
						BusinessRevision: 10, ObservedAt: fDegraded.now,
					}, "s2:sha256:"+strings.Repeat("5", 64))
				} else {
					fDegraded.control.Live.SetError(fDegraded.emergencyDevID, "run_state", &commandservice.ControlError{
						HTTPStatus: http.StatusServiceUnavailable, Code: "TELEMETRY_UNAVAILABLE", Title: "Telemetry unavailable", Detail: "offline", Retryable: true,
					})
				}

				// Create and leave an OUTCOME_UNKNOWN command on emergencyDevID before restoring degraded live state.
				fDegraded.control.Live.Set(fDegraded.emergencyDevID, "run_state", healthyShutdownObs(fDegraded.now, "RUNNING", 15), "s2:sha256:"+strings.Repeat("6", 64))
				unknownID := fDegraded.mustSeedQueuedCommand(t, fDegraded.emergencyDevID, fDegraded.emergencyPointID, "seed-unknown-emerg")
				env, claimErr := fDegraded.control.Store.ClaimDispatch(context.Background(), fDegraded.tenantID, "worker-1", 30*time.Second)
				if claimErr != nil {
					t.Fatalf("claim unknown cmd: %v", claimErr)
				}
				_ = fDegraded.control.Store.ResolveDispatch(context.Background(), env, commandmodel.ConnectorResult{
					Phase: commandmodel.ConnectorRequestCommitted, FailureCode: "ACK_TIMEOUT", EvidenceID: "ev-timeout",
				})
				if mode == "STALE" {
					fDegraded.control.Live.Set(fDegraded.emergencyDevID, "run_state", &commandmodel.LivePointObservation{
						EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "STALE", Quality: "STALE",
						BusinessRevision: 16, ObservedAt: fDegraded.now,
					}, "s2:sha256:"+strings.Repeat("7", 64))
				} else {
					fDegraded.control.Live.SetError(fDegraded.emergencyDevID, "run_state", &commandservice.ControlError{
						HTTPStatus: http.StatusServiceUnavailable, Code: "TELEMETRY_UNAVAILABLE", Title: "Telemetry unavailable", Detail: "offline", Retryable: true,
					})
				}

				recDeg := fDegraded.postShutdown(`{"mode":"EMERGENCY_DO_RELEASE","confirmEmergencyDoRelease":true,"reason":"断采故障下仍必达急停","verificationCode":"123456"}`)
				if recDeg.Code != http.StatusOK {
					t.Fatalf("%s status=%d want 200 body=%s", mode, recDeg.Code, recDeg.Body.String())
				}
				var degResp siteControlShutdownResponse
				if json.NewDecoder(recDeg.Body).Decode(&degResp) != nil || len(degResp.SubmittedCommandIDs) != 1 {
					t.Fatalf("unexpected response %s", recDeg.Body.String())
				}
				reconciledUnknown, _ := fDegraded.control.Store.Get(context.Background(), fDegraded.tenantID, unknownID)
				if reconciledUnknown.Status != commandmodel.IntentFailed {
					t.Fatalf("expected OUTCOME_UNKNOWN command reconciled to FAILED, got %s", reconciledUnknown.Status)
				}
				emergIntent, _ := fDegraded.control.Store.Get(context.Background(), fDegraded.tenantID, degResp.SubmittedCommandIDs[0])
				if emergIntent.PreconditionMode != commandmodel.PreconditionEmergencyBypass {
					t.Fatalf("expected PreconditionEmergencyBypass under %s telemetry, got %s", mode, emergIntent.PreconditionMode)
				}
				// Verify ClaimDispatch allows EMERGENCY_BYPASS command through under degraded telemetry!
				claimedEmerg, err := fDegraded.control.Authority.ClaimDispatch(context.Background(), fDegraded.tenantID, "worker-2", 30*time.Second)
				if err != nil || claimedEmerg.CommandID != emergIntent.ID {
					t.Fatalf("expected pre-dispatch gate to permit EMERGENCY_BYPASS release, got env=%+v err=%v", claimedEmerg, err)
				}
			})
		}

		// Missing EMERGENCY_DO_RELEASE point returns 409 EMERGENCY_DO_RELEASE_UNAVAILABLE without changing controlMode.
		fAbsent := newShutdownPlantFixture(t)
		recAbsent := fAbsent.postShutdown(`{"mode":"EMERGENCY_DO_RELEASE","confirmEmergencyDoRelease":true,"reason":"未配置急停点拦截","verificationCode":"123456"}`)
		var absentProblem platformapi.ProblemDetails
		if recAbsent.Code != http.StatusConflict || json.NewDecoder(recAbsent.Body).Decode(&absentProblem) != nil ||
			absentProblem.Code != "EMERGENCY_DO_RELEASE_UNAVAILABLE" {
			t.Fatalf("absent status=%d want 409 EMERGENCY_DO_RELEASE_UNAVAILABLE problem=%#v body=%s", recAbsent.Code, absentProblem, recAbsent.Body.String())
		}
		if fAbsent.control.Twin.ControlStates[fAbsent.siteID].ControlMode == siteControlModeSafeShutdownExit {
			t.Fatalf("expected controlMode unchanged when emergency point absent")
		}
	})
}

type shutdownPlantFixture struct {
	*commandGatewayFixture
	chillerAAssetID     string
	chillerBAssetID     string
	chwp1AssetID        string
	chwp2AssetID        string
	tower1AssetID       string
	chillerADevID       string
	chillerBDevID       string
	chwp1DevID          string
	chwp2DevID          string
	tower1DevID         string
	emergencyDevID      string
	chillerAStopPointID string
	chillerBStopPointID string
	chwp1StopPointID    string
	chwp2StopPointID    string
	tower1StopPointID   string
	emergencyPointID    string
}

func newShutdownPlantFixture(t *testing.T) *shutdownPlantFixture {
	t.Helper()
	return newShutdownPlantFixtureWithEmergency(t, false)
}

func newShutdownPlantFixtureWithEmergency(t *testing.T, includeEmergencyPoint bool) *shutdownPlantFixture {
	t.Helper()
	base := newCommandGatewayFixture(t)
	stopProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStop)
	ts := base.now.Format(time.RFC3339)

	f := &shutdownPlantFixture{
		commandGatewayFixture: base,
		chillerAAssetID:       "019a8000-1000-7000-8000-000000000001",
		chillerBAssetID:       "019a8000-1000-7000-8000-000000000002",
		chwp1AssetID:          "019a8000-1000-7000-8000-000000000003",
		chwp2AssetID:          "019a8000-1000-7000-8000-000000000004",
		tower1AssetID:         "019a8000-1000-7000-8000-000000000005",
		chillerADevID:         "019a8000-2000-7000-8000-000000000001",
		chillerBDevID:         "019a8000-2000-7000-8000-000000000002",
		chwp1DevID:            "019a8000-2000-7000-8000-000000000003",
		chwp2DevID:            "019a8000-2000-7000-8000-000000000004",
		tower1DevID:           "019a8000-2000-7000-8000-000000000005",
		emergencyDevID:        "019a8000-2000-7000-8000-000000000006",
		chillerAStopPointID:   "019a8000-4000-7000-8000-000000000001",
		chillerBStopPointID:   "019a8000-4000-7000-8000-000000000002",
		chwp1StopPointID:      "019a8000-4000-7000-8000-000000000003",
		chwp2StopPointID:      "019a8000-4000-7000-8000-000000000004",
		tower1StopPointID:     "019a8000-4000-7000-8000-000000000005",
		emergencyPointID:      "019a8000-4000-7000-8000-000000000006",
	}

	assets := []commandservice.AssetDTO{
		{ID: f.chillerAAssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chiller-a", DisplayName: "1号离心机", AssetType: "CHILLER", Status: "ACTIVE"},
		{ID: f.chillerBAssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chiller-b", DisplayName: "2号离心机", AssetType: "CHILLER", Status: "ACTIVE"},
		{ID: f.chwp1AssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chwp-1", DisplayName: "1号冷冻泵", AssetType: "CHILLED_WATER_PUMP", Status: "ACTIVE"},
		{ID: f.chwp2AssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "chwp-2", DisplayName: "2号冷冻泵", AssetType: "CHILLED_WATER_PUMP", Status: "ACTIVE"},
		{ID: f.tower1AssetID, TenantID: f.tenantID, SiteID: f.siteID, Code: "tower-1", DisplayName: "1号冷却塔", AssetType: "COOLING_TOWER", Status: "ACTIVE"},
	}
	for _, a := range assets {
		f.control.Twin.Assets[a.ID] = a
	}
	f.control.Twin.BoundDevices[f.chillerAAssetID] = []string{f.chillerADevID}
	f.control.Twin.BoundDevices[f.chillerBAssetID] = []string{f.chillerBDevID}
	f.control.Twin.BoundDevices[f.chwp1AssetID] = []string{f.chwp1DevID}
	f.control.Twin.BoundDevices[f.chwp2AssetID] = []string{f.chwp2DevID}
	f.control.Twin.BoundDevices[f.tower1AssetID] = []string{f.tower1DevID}

	devices := []commandservice.DeviceDTO{
		{ID: f.chillerADevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chiller-a", Status: "ACTIVE"},
		{ID: f.chillerBDevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chiller-b", Status: "ACTIVE"},
		{ID: f.chwp1DevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chwp-1", Status: "ACTIVE"},
		{ID: f.chwp2DevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-chwp-2", Status: "ACTIVE"},
		{ID: f.tower1DevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-tower-1", Status: "ACTIVE"},
	}
	points := []commandservice.TelemetryPointDTO{
		{ID: "019a8000-3000-7000-8000-000000000001", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerADevID, PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE"},
		{ID: "019a8000-3000-7000-8000-000000000002", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerBDevID, PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE"},
		{ID: "019a8000-3000-7000-8000-000000000003", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chwp1DevID, PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE"},
		{ID: "019a8000-3000-7000-8000-000000000004", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chwp2DevID, PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE"},
		{ID: "019a8000-3000-7000-8000-000000000005", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.tower1DevID, PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE"},
		{ID: f.chillerAStopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerADevID, PointCode: "stop_chiller_a", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE", SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state"}},
		{ID: f.chillerBStopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chillerBDevID, PointCode: "stop_chiller_b", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE", SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state"}},
		{
			ID: f.chwp1StopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chwp1DevID, PointCode: "stop_chwp_1", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE",
			SourceMetadata: map[string]any{
				"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state",
				"interlockChecks": []any{map[string]any{"deviceId": f.chillerADevID, "key": "run_state", "expected": "STOPPED", "reason": "chiller-a must stop before stopping chwp-1"}},
			},
		},
		{
			ID: f.chwp2StopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.chwp2DevID, PointCode: "stop_chwp_2", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE",
			SourceMetadata: map[string]any{
				"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state",
				"interlockChecks": []any{
					map[string]any{"deviceId": f.chillerADevID, "key": "run_state", "expected": "STOPPED", "reason": "chiller-a must stop before stopping chwp-2"},
					map[string]any{"deviceId": f.chillerBDevID, "key": "run_state", "expected": "STOPPED", "reason": "chiller-b must stop before stopping chwp-2"},
				},
			},
		},
		{
			ID: f.tower1StopPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.tower1DevID, PointCode: "stop_tower_1", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE",
			SourceMetadata: map[string]any{
				"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state",
				"interlockChecks": []any{map[string]any{"deviceId": f.chillerBDevID, "key": "run_state", "expected": "STOPPED", "reason": "chiller-b must stop before stopping tower-1"}},
			},
		},
	}
	relationships := []commandservice.AssetRelationshipDTO{
		{ID: "019a8000-9100-7000-8000-000000000001", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerAStopPointID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
		{ID: "019a8000-9100-7000-8000-000000000002", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chillerBStopPointID, ToType: "ASSET", ToID: f.chillerBAssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
		{ID: "019a8000-9100-7000-8000-000000000003", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chwp1StopPointID, ToType: "ASSET", ToID: f.chwp1AssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
		{ID: "019a8000-9100-7000-8000-000000000004", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.chwp2StopPointID, ToType: "ASSET", ToID: f.chwp2AssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
		{ID: "019a8000-9100-7000-8000-000000000005", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.tower1StopPointID, ToType: "ASSET", ToID: f.tower1AssetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
		{ID: "019a8000-9200-7000-8000-000000000001", TenantID: f.tenantID, SiteID: f.siteID, FromType: "ASSET", FromID: f.chwp1AssetID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
		{ID: "019a8000-9200-7000-8000-000000000002", TenantID: f.tenantID, SiteID: f.siteID, FromType: "ASSET", FromID: f.chwp2AssetID, ToType: "ASSET", ToID: f.chillerAAssetID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
		{ID: "019a8000-9200-7000-8000-000000000003", TenantID: f.tenantID, SiteID: f.siteID, FromType: "ASSET", FromID: f.chwp2AssetID, ToType: "ASSET", ToID: f.chillerBAssetID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
		{ID: "019a8000-9200-7000-8000-000000000004", TenantID: f.tenantID, SiteID: f.siteID, FromType: "ASSET", FromID: f.tower1AssetID, ToType: "ASSET", ToID: f.chillerBAssetID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
	}
	if includeEmergencyPoint {
		devices = append(devices, commandservice.DeviceDTO{ID: f.emergencyDevID, TenantID: f.tenantID, SiteID: f.siteID, Code: "ctrl-emergency-do", Status: "ACTIVE"})
		points = append(points,
			commandservice.TelemetryPointDTO{ID: "019a8000-3000-7000-8000-000000000006", TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.emergencyDevID, PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE"},
			commandservice.TelemetryPointDTO{
				ID: f.emergencyPointID, TenantID: f.tenantID, SiteID: f.siteID, ReportingDeviceID: f.emergencyDevID,
				PointCode: "emergency_do_release", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE",
				SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProfile.Revision, "actuationMode": "EMERGENCY_DO_RELEASE", "feedbackPointKey": "run_state"},
			},
		)
		relationships = append(relationships, commandservice.AssetRelationshipDTO{
			ID: "019a8000-9100-7000-8000-000000000006", TenantID: f.tenantID, SiteID: f.siteID, FromType: "POINT", FromID: f.emergencyPointID, ToType: "SITE", ToID: f.siteID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts,
		})
	}
	f.control.Twin.Models[f.siteID] = commandservice.SiteAssetModelDTO{
		SchemaVersion: 2, TenantID: f.tenantID, SiteID: f.siteID,
		Assets: assets, Devices: devices, TelemetryPoints: points, Relationships: relationships,
	}
	for i, devID := range []string{f.chillerADevID, f.chillerBDevID, f.chwp1DevID, f.chwp2DevID, f.tower1DevID, f.emergencyDevID} {
		f.setRunState(devID, "RUNNING", uint64(10+i))
	}
	return f
}

func healthyShutdownObs(now time.Time, value string, rev uint64) *commandmodel.LivePointObservation {
	return &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: rev, ReportedValue: commandmodel.TextScalar(value), ObservedAt: now,
	}
}

func (f *shutdownPlantFixture) setRunState(deviceID, state string, rev uint64) {
	f.control.Live.Set(deviceID, "run_state", healthyShutdownObs(f.now, state, rev), "s2:sha256:"+strings.Repeat("3", 64))
}

func (f *shutdownPlantFixture) mustSeedQueuedCommand(t *testing.T, deviceID, pointID, idemKey string) string {
	t.Helper()
	_, claims, err := f.control.IAM.AuthorizeCommand(
		context.Background(),
		commandservice.OperatorIdentity{TenantID: f.tenantID, Subject: "seed-op"},
		commandmodel.AuthorizationCommandSubmit, f.siteID, deviceID,
		commandmodel.CapabilityStop, "capability:stop:v1",
	)
	if err != nil {
		t.Fatalf("authorize seed command: %v", err)
	}
	auth := commandauth.Snapshot(claims)
	res, err := f.control.Store.Submit(context.Background(), commandmodel.SubmitRequest{
		TenantID: f.tenantID, SiteID: f.siteID, DeviceID: deviceID, PointID: pointID,
		PrincipalID: claims.PrincipalID, IdempotencyKey: idemKey, Capability: commandmodel.CapabilityStop,
		Parameters: commandmodel.CommandParameters{}, VerificationPointKey: "run_state",
		PreconditionMode: commandmodel.PreconditionRequireCurrentState,
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Quality: "GOOD",
			BusinessRevision: 10, ObservedAt: f.now,
		},
		Authorization: auth,
	})
	if err != nil {
		t.Fatalf("submit seed command: %v", err)
	}
	return res.Intent.ID
}

func (f *shutdownPlantFixture) postShutdown(body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+f.siteID+"/control-shutdown", strings.NewReader(body))
	f.authenticate(req, true)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *shutdownPlantFixture) submitDirectCommand(assetID, pointID, totpCode, idemKey string) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"assetId":%q,"commandPointId":%q,"parameters":{},"verificationCode":%q}`, assetID, pointID, totpCode)
	req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	f.authenticate(req, true)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idemKey)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *shutdownPlantFixture) submittedPointIDs(t *testing.T, commandIDs []string) []string {
	t.Helper()
	ids := make([]string, 0, len(commandIDs))
	for _, id := range commandIDs {
		intent, err := f.control.Store.Get(context.Background(), f.tenantID, id)
		if err != nil {
			t.Fatalf("get submitted command %s: %v", id, err)
		}
		ids = append(ids, intent.PointID)
	}
	return ids
}

var _ = bytes.NewReader
