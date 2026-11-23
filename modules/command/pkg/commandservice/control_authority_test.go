package commandservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	caTenantID   = "018f3d00-0000-7000-8000-000000000001"
	caSiteID     = "018f3e00-1000-7000-8000-000000000001"
	caChillerID  = "018f3e00-2000-7000-8000-000000000001"
	caPumpID     = "018f3e00-2000-7000-8000-000000000002"
	caChillerDev = "018f3e00-3000-7000-8000-000000000001"
	caPumpDev    = "018f3e00-3000-7000-8000-000000000002"
	caEmergDev   = "018f3e00-3000-7000-8000-000000000003"
	caStartPt    = "018f3e00-4000-7000-8000-000000000001"
	caStopPt     = "018f3e00-4000-7000-8000-000000000002"
	caSetpointPt = "018f3e00-4000-7000-8000-000000000003"
	caPumpStopPt = "018f3e00-4000-7000-8000-000000000004"
	caEmergPt    = "018f3e00-4000-7000-8000-000000000005"
)

func seedPlantHarness(t *testing.T, now time.Time) (*TestControlHarness, OperatorIdentity) {
	t.Helper()
	h, err := NewControlAuthorityForTest(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	stopProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStop)
	startProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStart)
	spProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilitySetTemperatureSetpoint)

	ts := now.Format(time.RFC3339)
	h.IAM.AllowedSites = []string{caSiteID}
	h.Twin.BoundDevices[caChillerID] = []string{caChillerDev}
	h.Twin.BoundDevices[caPumpID] = []string{caPumpDev}

	chillerAsset := AssetDTO{ID: caChillerID, TenantID: caTenantID, SiteID: caSiteID, Code: "chiller-a", DisplayName: "Chiller A", AssetType: "CHILLER", Status: "ACTIVE"}
	pumpAsset := AssetDTO{ID: caPumpID, TenantID: caTenantID, SiteID: caSiteID, Code: "chwp-1", DisplayName: "CHWP 1", AssetType: "CHILLED_WATER_PUMP", Status: "ACTIVE"}
	h.Twin.Assets[caChillerID] = chillerAsset
	h.Twin.Assets[caPumpID] = pumpAsset
	h.Twin.Models[caSiteID] = SiteAssetModelDTO{
		SchemaVersion: 2,
		TenantID:      caTenantID,
		SiteID:        caSiteID,
		Assets:        []AssetDTO{chillerAsset, pumpAsset},
		Devices: []DeviceDTO{
			{ID: caChillerDev, TenantID: caTenantID, SiteID: caSiteID, Code: "ctrl-ch-a", Status: "ACTIVE"},
			{ID: caPumpDev, TenantID: caTenantID, SiteID: caSiteID, Code: "ctrl-pump-1", Status: "ACTIVE"},
			{ID: caEmergDev, TenantID: caTenantID, SiteID: caSiteID, Code: "ctrl-emerg", Status: "ACTIVE"},
		},
		TelemetryPoints: []TelemetryPointDTO{
			{
				ID: "018f3e00-4100-7000-8000-000000000001", TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caChillerDev,
				PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE",
			},
			{
				ID: "018f3e00-4100-7000-8000-000000000002", TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caChillerDev,
				PointCode: "zone_temperature", PointType: "TELEMETRY", ValueType: "NUMBER", Status: "ACTIVE",
			},
			{
				ID: "018f3e00-4100-7000-8000-000000000003", TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caPumpDev,
				PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE",
			},
			{
				ID: "018f3e00-4100-7000-8000-000000000004", TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caEmergDev,
				PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE",
			},
			{
				ID: caStartPt, TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caChillerDev,
				PointCode: "start_chiller", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE",
				SourceMetadata: map[string]any{
					"capability": "START", "capabilityRevision": startProfile.Revision, "feedbackPointKey": "run_state",
					"interlockChecks": []any{map[string]any{"deviceId": caPumpDev, "key": "run_state", "expected": "RUNNING", "reason": "chilled pump must run before starting chiller"}},
				},
			},
			{
				ID: caStopPt, TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caChillerDev,
				PointCode: "stop_chiller", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE",
				SourceMetadata: map[string]any{"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state"},
			},
			{
				ID: caSetpointPt, TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caChillerDev,
				PointCode: "temp_sp", PointType: "COMMAND", ValueType: "NUMBER", Writable: true, Status: "ACTIVE",
				SourceMetadata: map[string]any{"capability": "SET_TEMPERATURE_SETPOINT", "capabilityRevision": spProfile.Revision, "feedbackPointKey": "zone_temperature"},
			},
			{
				ID: caPumpStopPt, TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caPumpDev,
				PointCode: "stop_pump", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE",
				SourceMetadata: map[string]any{
					"capability": "STOP", "capabilityRevision": stopProfile.Revision, "feedbackPointKey": "run_state",
					"interlockChecks": []any{map[string]any{"deviceId": caChillerDev, "key": "run_state", "expected": "STOPPED", "reason": "chiller-a must stop before pump"}},
				},
			},
			{
				ID: caEmergPt, TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caEmergDev,
				PointCode: "emerg_do", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE",
				SourceMetadata: map[string]any{
					"capability": "STOP", "capabilityRevision": stopProfile.Revision, "actuationMode": "EMERGENCY_DO_RELEASE", "feedbackPointKey": "run_state",
				},
			},
		},
		Relationships: []AssetRelationshipDTO{
			{ID: "rel-1", TenantID: caTenantID, SiteID: caSiteID, FromType: "POINT", FromID: caStartPt, ToType: "ASSET", ToID: caChillerID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
			{ID: "rel-2", TenantID: caTenantID, SiteID: caSiteID, FromType: "POINT", FromID: caStopPt, ToType: "ASSET", ToID: caChillerID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
			{ID: "rel-3", TenantID: caTenantID, SiteID: caSiteID, FromType: "POINT", FromID: caSetpointPt, ToType: "ASSET", ToID: caChillerID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
			{ID: "rel-4", TenantID: caTenantID, SiteID: caSiteID, FromType: "POINT", FromID: caPumpStopPt, ToType: "ASSET", ToID: caPumpID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
			{ID: "rel-5", TenantID: caTenantID, SiteID: caSiteID, FromType: "POINT", FromID: caEmergPt, ToType: "SITE", ToID: caSiteID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
			{ID: "rel-6", TenantID: caTenantID, SiteID: caSiteID, FromType: "ASSET", FromID: caPumpID, ToType: "ASSET", ToID: caChillerID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
		},
	}

	h.Live.Set(caChillerDev, "run_state", healthyTextObs(now, "STOPPED", 11), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000001")
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(now, "RUNNING", 12), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000002")
	h.Live.Set(caChillerDev, "zone_temperature", healthyNumObs(now, 23.0, 17), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000003")

	op := OperatorIdentity{
		TenantID: caTenantID, Subject: "op-1", SubjectIssuer: "https://issuer.test",
		Roles: []string{"operator"}, SessionID: "sess-1", TokenID: "tok-1",
		IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	return h, op
}

func healthyTextObs(now time.Time, text string, rev uint64) *commandmodel.LivePointObservation {
	return &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT",
		Freshness: "FRESH", Quality: "GOOD", BusinessRevision: rev,
		ReportedValue: commandmodel.TextScalar(text), ObservedAt: now,
	}
}

func healthyNumObs(now time.Time, num float64, rev uint64) *commandmodel.LivePointObservation {
	return &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT",
		Freshness: "FRESH", Quality: "GOOD", BusinessRevision: rev,
		CurrentValue: &num, ReportedValue: commandmodel.NumberScalar(num), ObservedAt: now,
	}
}

func TestControlAuthorityOrderInvariantLocksBeforeStepUpBeforeGrant(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)

	// 1. Peer interlock blocked -> returns 409 COMMAND_INTERLOCK_BLOCKED BEFORE 428 STEP_UP_REQUIRED and consumes 0 IAM grants.
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(now, "STOPPED", 13), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000004")
	_, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStartPt, IdempotencyKey: "k-interlock-blocked",
	})
	var ce *ControlError
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_INTERLOCK_BLOCKED" {
		t.Fatalf("expected 409 COMMAND_INTERLOCK_BLOCKED before step-up, got %v", err)
	}
	if h.IAM.CommandAuthorizeCalls != 0 {
		t.Fatalf("expected 0 IAM command grant calls, got %d", h.IAM.CommandAuthorizeCalls)
	}

	// Restore pump RUNNING -> now START hits 428 STEP_UP_REQUIRED BEFORE IAM grant minting.
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(now, "RUNNING", 14), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000005")
	_, err = h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStartPt, IdempotencyKey: "k-needs-step-up",
	})
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusPreconditionRequired || ce.Code != "STEP_UP_REQUIRED" {
		t.Fatalf("expected 428 STEP_UP_REQUIRED, got %v", err)
	}
	if h.IAM.CommandAuthorizeCalls != 0 {
		t.Fatalf("428 STEP_UP_REQUIRED must not consume an IAM grant, got %d calls", h.IAM.CommandAuthorizeCalls)
	}

	// Lock the chiller asset -> locked check blocks BEFORE step-up even for START!
	_, lockErr := h.Authority.PutAssetControlLock(context.Background(), op, caChillerID, AssetControlLockInput{
		LockMode: AssetControlLockMaintenance, Reason: "filter maintenance", IdempotencyKey: "lock-1",
	})
	if lockErr != nil {
		t.Fatalf("lock failed: %v", lockErr)
	}
	_, err = h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStartPt, IdempotencyKey: "k-locked-asset",
	})
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_CONTROL_LOCKED" {
		t.Fatalf("expected 409 COMMAND_CONTROL_LOCKED before step-up, got %v", err)
	}
	if h.IAM.CommandAuthorizeCalls != 0 {
		t.Fatalf("locked check must not consume an IAM grant, got %d calls", h.IAM.CommandAuthorizeCalls)
	}

	// Unlock asset & provide step-up -> succeeds and mints/consumes exactly 1 grant.
	_, lockErr = h.Authority.PutAssetControlLock(context.Background(), op, caChillerID, AssetControlLockInput{
		LockMode: AssetControlLockUnlocked, Reason: "done", IdempotencyKey: "unlock-1",
	})
	if lockErr != nil {
		t.Fatalf("unlock failed: %v", lockErr)
	}
	op.StepUpTOTPVerified = true
	out, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStartPt, IdempotencyKey: "k-ok-start",
	})
	if err != nil {
		t.Fatalf("expected verified start command to succeed, got %v", err)
	}
	if out.View.CommandID == "" || h.IAM.CommandAuthorizeCalls != 1 {
		t.Fatalf("unexpected output=%+v grantCalls=%d", out.View, h.IAM.CommandAuthorizeCalls)
	}
}

func TestControlAuthorityShutdownEmergencyBypassAndOrdering(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)

	// Deny schedule.manage BEFORE step-up -> returns 403 CONTROL_EXIT_FORBIDDEN even when StepUpTOTPVerified == false!
	h.IAM.DenyScheduleManage = true
	_, err := h.Authority.ExecuteSiteControlShutdown(context.Background(), op, caSiteID, SiteControlShutdownInput{
		Mode: ShutdownModePlantSafe, Reason: "unauthorized shutdown",
	})
	var ce *ControlError
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusForbidden || ce.Code != "CONTROL_EXIT_FORBIDDEN" {
		t.Fatalf("expected 403 before 428 on unauthorized shutdown, got %v", err)
	}

	h.IAM.DenyScheduleManage = false
	_, err = h.Authority.ExecuteSiteControlShutdown(context.Background(), op, caSiteID, SiteControlShutdownInput{
		Mode: ShutdownModePlantSafe, Reason: "missing totp",
	})
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusPreconditionRequired || ce.Code != "STEP_UP_REQUIRED" {
		t.Fatalf("expected 428 STEP_UP_REQUIRED after authz passes, got %v", err)
	}

	// Degraded/stale chiller telemetry: PLANT_SAFE_SHUTDOWN submits chiller STOP with EMERGENCY_BYPASS
	// (no fake GOOD telemetry) and defers the holding pump cleanly.
	stale := healthyTextObs(now, "RUNNING", 19)
	stale.Freshness = "STALE"
	h.Live.Set(caChillerDev, "run_state", stale, "s2:sha256:0000000000000000000000000000000000000000000000000000000000000009")

	op.StepUpTOTPVerified = true
	resp, err := h.Authority.ExecuteSiteControlShutdown(context.Background(), op, caSiteID, SiteControlShutdownInput{
		Mode: ShutdownModePlantSafe, Reason: "安全紧急停机", IdempotencyKey: "sd-stale-1",
	})
	if err != nil {
		t.Fatalf("plant safe shutdown failed: %v", err)
	}
	if resp.SiteControlState.ControlMode != SiteControlModeSafeShutdownExit || len(resp.SubmittedCommandIDs) != 1 || len(resp.DeferredHoldingAssetIds) != 1 || resp.DeferredHoldingAssetIds[0] != caPumpID {
		t.Fatalf("unexpected shutdown response: %+v", resp)
	}
	intent, getErr := h.Store.Get(context.Background(), caTenantID, resp.SubmittedCommandIDs[0])
	if getErr != nil {
		t.Fatal(getErr)
	}
	if intent.PreconditionMode != commandmodel.PreconditionEmergencyBypass || intent.SnapshotRevision != 0 || intent.BypassReason != "安全紧急停机" {
		t.Fatalf("expected EMERGENCY_BYPASS intent with SnapshotRevision=0, got %+v", intent)
	}

	// EMERGENCY_DO_RELEASE succeeds even when TelemetryLivePort returns an error.
	h.Live.SetError(caEmergDev, "run_state", unavailableControlError("telemetry offline"))
	emergResp, err := h.Authority.ExecuteSiteControlShutdown(context.Background(), op, caSiteID, SiteControlShutdownInput{
		Mode: ShutdownModeEmergencyDoRelease, ConfirmEmergencyDoRelease: true,
		Reason: "断网全站急停", IdempotencyKey: "sd-emerg-1",
	})
	if err != nil {
		t.Fatalf("emergency DO release failed under offline telemetry: %v", err)
	}
	if len(emergResp.SubmittedCommandIDs) != 1 {
		t.Fatalf("expected 1 submitted emergency DO release command, got %+v", emergResp)
	}
}

func TestControlAuthorityClaimDispatchPreDispatchGateScanAndTransientHandling(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)
	op.StepUpTOTPVerified = true

	// Submit queued command #1: chiller setpoint (will become locked BEFORE dispatch!).
	cmd1, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caSetpointPt,
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24.0}, IdempotencyKey: "cmd-q1",
	})
	if err != nil {
		t.Fatalf("submit cmd1 failed: %v", err)
	}

	// Submit queued command #2 on another device: pump stop (safe when chiller stopped).
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(now, "RUNNING", 20), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000010")
	cmd2, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, IdempotencyKey: "cmd-q2",
	})
	if err != nil {
		t.Fatalf("submit cmd2 failed: %v", err)
	}

	// Lock chiller directly in twin state without CancelPending so cmd1 stays QUEUED until ClaimDispatch catches it!
	h.Twin.ControlStates[caSiteID] = SiteControlStateView{
		TenantID: caTenantID, SiteID: caSiteID, ControlMode: SiteControlModeRemoteAuto,
		AssetLocks: []AssetControlLockView{{AssetID: caChillerID, SiteID: caSiteID, LockMode: AssetControlLockManual}},
	}

	// Single ClaimDispatch must reject cmd1 terminally as DISPATCH_SAFETY_CONTROL_LOCKED AND scan ahead to return cmd2!
	env, claimErr := h.Authority.ClaimDispatch(context.Background(), caTenantID, "dispatcher-1", 30*time.Second)
	if claimErr != nil {
		t.Fatalf("expected scan-ahead to claim cmd2, got err=%v", claimErr)
	}
	if env.CommandID != cmd2.View.CommandID {
		t.Fatalf("claimed=%s, want cmd2=%s", env.CommandID, cmd2.View.CommandID)
	}
	got1, _ := h.Store.Get(context.Background(), caTenantID, cmd1.View.CommandID)
	if got1.Status != commandmodel.IntentRejected {
		t.Fatalf("cmd1 status=%s, want REJECTED after terminal pre-dispatch rejection", got1.Status)
	}

	// Resolve cmd2 as pre-send transient error or complete it; queue another pump stop with transient telemetry error.
	_ = h.Authority.ResolveDispatch(context.Background(), env, commandmodel.ConnectorResult{Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "COMMAND_CONTROL_DISABLED"})

	h.Live.Set(caPumpDev, "run_state", healthyTextObs(now, "RUNNING", 21), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000011")
	cmd3, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, IdempotencyKey: "cmd-q3",
	})
	if err != nil {
		t.Fatalf("submit cmd3 failed: %v", err)
	}
	h.Live.SetError(caPumpDev, "run_state", unavailableControlError("transient telemetry timeout"))
	_, transientErr := h.Authority.ClaimDispatch(context.Background(), caTenantID, "dispatcher-1", 30*time.Second)
	if !errors.Is(transientErr, ErrNoDispatchAvailable) || !errors.Is(transientErr, errTransientSafetyEvaluation) {
		t.Fatalf("expected ErrNoDispatchAvailable wrapping errTransientSafetyEvaluation on transient failure, got %v", transientErr)
	}
	got3, _ := h.Store.Get(context.Background(), caTenantID, cmd3.View.CommandID)
	if got3.Status != commandmodel.IntentQueued || len(got3.Attempts) != 1 {
		t.Fatalf("expected 1 retryable attempt keeping QUEUED, got status=%s attempts=%d", got3.Status, len(got3.Attempts))
	}

	h.Live.Set(caPumpDev, "run_state", healthyTextObs(now, "RUNNING", 21), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000012")
	badInterlockModel := h.Twin.Models[caSiteID]
	pts := append([]TelemetryPointDTO(nil), badInterlockModel.TelemetryPoints...)
	for i := range pts {
		if pts[i].ID == caPumpStopPt {
			metaCopy := make(map[string]any, len(pts[i].SourceMetadata)+1)
			for k, v := range pts[i].SourceMetadata {
				metaCopy[k] = v
			}
			metaCopy["interlockChecks"] = []any{map[string]any{"deviceId": 123, "key": "run_state", "expected": "RUNNING"}}
			pts[i].SourceMetadata = metaCopy
		}
	}
	badInterlockModel.TelemetryPoints = pts
	h.Twin.Models[caSiteID] = badInterlockModel
	_, interlockErr := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, IdempotencyKey: "cmd-bad-interlock-type",
	})
	var ctrlErr *ControlError
	if !errors.As(interlockErr, &ctrlErr) || ctrlErr.HTTPStatus != 400 || ctrlErr.Code != "COMMAND_REQUEST_INVALID" {
		t.Fatalf("expected 400 COMMAND_REQUEST_INVALID for malformed interlockChecks field, got %v", interlockErr)
	}
}

func TestOperatorHTTPHandlerAuthAndRoutes(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, _ := seedPlantHarness(t, now)

	signCtx := func(actions []string) string {
		claims := identitycontext.DelegationClaims{
			Issuer: "spiffe://hvac.local/platform-gateway", Subject: "op-1", SubjectIssuer: "https://issuer.test",
			ExecutingService: "spiffe://hvac.local/platform-gateway", Audience: "command-service",
			TenantID: caTenantID, Actions: actions, Scopes: []string{"tenant:" + caTenantID, "session:sess-1"},
			PolicyRevision: "policy-v1", SessionID: "sess-1", IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(),
			TokenID: "ctx-1",
		}
		signed, err := identitycontext.SignDelegation(h.GatewaySigner, claims)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}

	// Missing X-Command-Control-Context rejected with 403.
	unauthReq := httptest.NewRequest(http.MethodPost, InternalOperatorCommandsPath, bytes.NewBufferString(`{}`))
	unauthRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusForbidden {
		t.Fatalf("missing context status=%d, want 403", unauthRec.Code)
	}

	// Low-risk setpoint works via HTTP and sets 202 + Location header.
	body := `{"assetId":"` + caChillerID + `","commandPointId":"` + caSetpointPt + `","parameters":{"setpointC":23.5}}`
	req := httptest.NewRequest(http.MethodPost, InternalOperatorCommandsPath, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "http-op-1")
	req.Header.Set("X-Command-Control-Context", signCtx([]string{OperatorControlContextAction}))
	rec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || rec.Header().Get("Location") == "" {
		t.Fatalf("http operator create status=%d location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	var view CommandView
	if json.NewDecoder(rec.Body).Decode(&view) != nil || view.CommandID == "" {
		t.Fatalf("invalid command view body=%s", rec.Body.String())
	}
}

func TestControlAuthoritySafeShutdownExitBlockedByDispatchingCommand(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)
	op.StepUpTOTPVerified = true

	if _, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caSetpointPt,
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 23.5}, IdempotencyKey: "safe-exit-precond-1",
	}); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	env, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	_, exitErr := h.Authority.PutSiteControlMode(context.Background(), op, caSiteID, SiteControlModeInput{
		ControlMode: SiteControlModeSafeShutdownExit, Reason: "exit check",
	})
	var ce *ControlError
	if !errors.As(exitErr, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "SAFE_SHUTDOWN_PRECONDITION_FAILED" {
		t.Fatalf("expected 409 SAFE_SHUTDOWN_PRECONDITION_FAILED, got %v", exitErr)
	}

	if err := h.Authority.ResolveDispatch(context.Background(), env, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "ABORTED",
	}); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if _, err := h.Authority.PutSiteControlMode(context.Background(), op, caSiteID, SiteControlModeInput{
		ControlMode: SiteControlModeSafeShutdownExit, Reason: "exit after clear",
	}); err != nil {
		t.Fatalf("expected SAFE_SHUTDOWN_EXIT to succeed after clearing DISPATCHING, got %v", err)
	}
}

type storeWithoutPriorityLocker struct {
	HTTPAuthority
}

func TestControlAuthorityGetSiteControlStatePriorityLocks(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	clock := now
	h, op := seedPlantHarness(t, now)
	h.Authority.now = func() time.Time { return clock }
	h.Store.now = func() time.Time { return clock }
	h.Store.svc.now = func() time.Time { return clock }

	// Empty active slice on default state.
	state, err := h.Authority.GetSiteControlState(context.Background(), op, caSiteID)
	if err != nil {
		t.Fatalf("GetSiteControlState empty: %v", err)
	}
	if state.DevicePriorityLocks == nil || len(state.DevicePriorityLocks) != 0 {
		t.Fatalf("expected non-nil empty devicePriorityLocks, got %#v", state.DevicePriorityLocks)
	}

	// Best-effort empty slice when store does not implement DevicePriorityLocker.
	noLockerAuth, err := NewControlAuthority(ControlAuthorityConfig{
		Store:   storeWithoutPriorityLocker{HTTPAuthority: h.Store},
		Runtime: h.Store,
		Twin:    h.Twin,
		Live:    h.Live,
		IAM:     h.IAM,
		Now:     func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	noLockerState, err := noLockerAuth.GetSiteControlState(context.Background(), op, caSiteID)
	if err != nil || noLockerState.DevicePriorityLocks == nil || len(noLockerState.DevicePriorityLocks) != 0 {
		t.Fatalf("expected non-nil empty devicePriorityLocks without locker, got %#v err=%v", noLockerState.DevicePriorityLocks, err)
	}

	// Acquire out-of-order devices + an already-short lock that later expires.
	if _, err := h.Store.AcquireDevicePriorityLock(context.Background(), commandmodel.DevicePriorityLock{
		TenantID: caTenantID, SiteID: caSiteID, DeviceID: caPumpDev,
		LockedPriority: commandmodel.PriorityProtectionRule, LockedBy: "op-2",
		LockedUntil: clock.Add(2 * time.Hour), Reason: "pump freeze protection",
	}); err != nil {
		t.Fatalf("acquire pump lock: %v", err)
	}
	if _, err := h.Store.AcquireDevicePriorityLock(context.Background(), commandmodel.DevicePriorityLock{
		TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev,
		LockedPriority: commandmodel.PriorityManualOverride, LockedBy: "op-1",
		LockedUntil: clock.Add(15 * time.Minute), Reason: "chiller manual hold",
	}); err != nil {
		t.Fatalf("acquire chiller lock: %v", err)
	}

	activeState, err := h.Authority.GetSiteControlState(context.Background(), op, caSiteID)
	if err != nil {
		t.Fatalf("GetSiteControlState active: %v", err)
	}
	if len(activeState.DevicePriorityLocks) != 2 {
		t.Fatalf("expected 2 active locks, got %d", len(activeState.DevicePriorityLocks))
	}
	if activeState.DevicePriorityLocks[0].DeviceID != caChillerDev || activeState.DevicePriorityLocks[1].DeviceID != caPumpDev {
		t.Fatalf("expected deterministic deviceId sort, got %+v", activeState.DevicePriorityLocks)
	}

	// Advance clock past chiller lock expiry.
	clock = clock.Add(30 * time.Minute)
	afterExpiry, err := h.Authority.GetSiteControlState(context.Background(), op, caSiteID)
	if err != nil {
		t.Fatalf("GetSiteControlState after expiry: %v", err)
	}
	if len(afterExpiry.DevicePriorityLocks) != 1 || afterExpiry.DevicePriorityLocks[0].DeviceID != caPumpDev {
		t.Fatalf("expected only pump lock after chiller lock expired, got %+v", afterExpiry.DevicePriorityLocks)
	}

	// Fail closed when priority locker returns a store error.
	h.Store.PriorityLockErr = errors.New("db unavailable")
	if _, err := h.Authority.GetSiteControlState(context.Background(), op, caSiteID); err == nil {
		t.Fatal("expected fail-closed error when PriorityLockErr is set")
	} else {
		var ce *ControlError
		if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 ControlError, got %v", err)
		}
	}
}

func TestControlAuthorityPutAndDeleteDevicePriorityLock(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)

	// Invalid UUIDv7 -> 404.
	if _, err := h.Authority.PutDevicePriorityLock(context.Background(), op, "not-uuid", caChillerDev, DevicePriorityLockInput{
		LockedPriority: "MANUAL_OVERRIDE", Reason: "hold",
	}); err == nil {
		t.Fatal("expected invalid siteId error")
	}
	if _, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, "not-uuid", DevicePriorityLockInput{
		LockedPriority: "MANUAL_OVERRIDE", Reason: "hold",
	}); err == nil {
		t.Fatal("expected invalid deviceId error")
	}

	// Device not on site -> 404.
	unknownDevice := "018f3e00-3000-7000-8000-000000000099"
	if _, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, unknownDevice, DevicePriorityLockInput{
		LockedPriority: "MANUAL_OVERRIDE", Reason: "hold",
	}); err == nil {
		t.Fatal("expected unknown device 404")
	} else {
		var ce *ControlError
		if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusNotFound {
			t.Fatalf("expected 404, got %v", err)
		}
	}

	// Unauthorized action or site -> 403.
	h.IAM.DenyScheduleManage = true
	if _, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, DevicePriorityLockInput{
		LockedPriority: "MANUAL_OVERRIDE", Reason: "hold",
	}); err == nil {
		t.Fatal("expected 403 when ActionScheduleManage is denied")
	} else {
		var ce *ControlError
		if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusForbidden {
			t.Fatalf("expected 403, got %v", err)
		}
	}
	h.IAM.DenyScheduleManage = false
	h.IAM.AllowedSites = []string{"018f3e00-1000-7000-8000-000000000099"}
	if _, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, DevicePriorityLockInput{
		LockedPriority: "MANUAL_OVERRIDE", Reason: "hold",
	}); err == nil {
		t.Fatal("expected 403 when site is outside AllowedSiteIDs")
	}
	h.IAM.AllowedSites = []string{caSiteID}

	// Invalid lockedPriority -> 400 CONTROL_EXIT_REQUEST_INVALID.
	for _, badPriority := range []string{"", "AI_OPTIMIZATION", "SCHEDULE", "UNKNOWN"} {
		_, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, DevicePriorityLockInput{
			LockedPriority: badPriority, Reason: "valid reason",
		})
		var ce *ControlError
		if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusBadRequest || ce.Code != "CONTROL_EXIT_REQUEST_INVALID" {
			t.Fatalf("priority %q expected 400 CONTROL_EXIT_REQUEST_INVALID, got %v", badPriority, err)
		}
	}

	// Invalid durationMinutes -> 400 CONTROL_EXIT_REQUEST_INVALID.
	for _, badDur := range []int{0, 4, 1441} {
		dur := badDur
		_, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, DevicePriorityLockInput{
			LockedPriority: "MANUAL_OVERRIDE", DurationMinutes: &dur, Reason: "valid reason",
		})
		var ce *ControlError
		if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusBadRequest || ce.Code != "CONTROL_EXIT_REQUEST_INVALID" {
			t.Fatalf("duration %d expected 400 CONTROL_EXIT_REQUEST_INVALID, got %v", badDur, err)
		}
	}

	// Invalid reason (blank or >1000 runes) -> 400 CONTROL_EXIT_REQUEST_INVALID.
	for _, badReason := range []string{"", "   ", strings.Repeat("锁", 1001)} {
		_, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, DevicePriorityLockInput{
			LockedPriority: "MANUAL_OVERRIDE", Reason: badReason,
		})
		var ce *ControlError
		if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusBadRequest || ce.Code != "CONTROL_EXIT_REQUEST_INVALID" {
			t.Fatalf("bad reason expected 400 CONTROL_EXIT_REQUEST_INVALID, got %v", err)
		}
	}

	// Omitted duration defaults to 120m; exact 1000-rune reason succeeds.
	maxReason := strings.Repeat("锁", 1000)
	view, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, DevicePriorityLockInput{
		LockedPriority: "MANUAL_OVERRIDE", Reason: maxReason,
	})
	if err != nil {
		t.Fatalf("PutDevicePriorityLock default duration: %v", err)
	}
	wantUntil := now.Add(120 * time.Minute).UTC().Format(time.RFC3339Nano)
	if view.DeviceID != caChillerDev || view.LockedPriority != "MANUAL_OVERRIDE" || view.CommandSource != "OPERATOR" || view.LockedUntil != wantUntil {
		t.Fatalf("unexpected lock view: %+v", view)
	}

	// Upgrade to SAFETY_INTERLOCK with explicit 5m duration succeeds, then lower priority returns 409 DEVICE_PRIORITY_LOCKED.
	dur5 := 5
	interlockView, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, DevicePriorityLockInput{
		LockedPriority: "SAFETY_INTERLOCK", DurationMinutes: &dur5, Reason: "high condenser pressure",
	})
	if err != nil || interlockView.LockedPriority != "SAFETY_INTERLOCK" || interlockView.CommandSource != string(commandmodel.SourceSafetyInterlock) {
		t.Fatalf("upgrade to SAFETY_INTERLOCK failed: %+v err=%v", interlockView, err)
	}
	_, preemptErr := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, DevicePriorityLockInput{
		LockedPriority: "MANUAL_OVERRIDE", Reason: "downgrade attempt",
	})
	var ce *ControlError
	if !errors.As(preemptErr, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "DEVICE_PRIORITY_LOCKED" {
		t.Fatalf("expected 409 DEVICE_PRIORITY_LOCKED on downgrade, got %v", preemptErr)
	}

	// Delete requires non-empty reason <= 1000 runes, and releases idempotently.
	if _, err := h.Authority.DeleteDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, "   "); err == nil {
		t.Fatal("expected 400 on blank delete reason")
	}
	rel1, err := h.Authority.DeleteDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, "interlock cleared")
	if err != nil || !rel1.Released || rel1.DeviceID != caChillerDev {
		t.Fatalf("first release got %+v err=%v", rel1, err)
	}
	rel2, err := h.Authority.DeleteDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, "repeat clear")
	if err != nil || rel2.Released || rel2.DeviceID != caChillerDev {
		t.Fatalf("second idempotent release got %+v err=%v", rel2, err)
	}
}

func TestOperatorHTTPDevicePriorityLockRoutesAndObservability(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, _ := seedPlantHarness(t, now)

	signCtx := func() string {
		claims := identitycontext.DelegationClaims{
			Issuer: "spiffe://hvac.local/platform-gateway", Subject: "op-1", SubjectIssuer: "https://issuer.test",
			ExecutingService: "spiffe://hvac.local/platform-gateway", Audience: "command-service",
			TenantID: caTenantID, Actions: []string{OperatorControlContextAction}, Scopes: []string{"tenant:" + caTenantID, "session:sess-1"},
			PolicyRevision: "policy-v1", SessionID: "sess-1", IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(),
			TokenID: "ctx-lock-1",
		}
		signed, err := identitycontext.SignDelegation(h.GatewaySigner, claims)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}

	lockPath := InternalOperatorPrefix + "/sites/" + caSiteID + "/devices/" + caChillerDev + "/priority-lock"
	statePath := InternalOperatorPrefix + "/sites/" + caSiteID + "/control-state"

	// ObservabilityRoute maps lock path to bounded "control.lock" label.
	if got := ObservabilityRoute(httptest.NewRequest(http.MethodPut, lockPath, nil)); got != "control.lock" {
		t.Fatalf("ObservabilityRoute PUT = %q want control.lock", got)
	}
	if got := ObservabilityRoute(httptest.NewRequest(http.MethodDelete, lockPath, nil)); got != "control.lock" {
		t.Fatalf("ObservabilityRoute DELETE = %q want control.lock", got)
	}

	// Unsupported GET -> 405 with Allow: PUT, DELETE.
	getLockReq := httptest.NewRequest(http.MethodGet, lockPath, nil)
	getLockReq.Header.Set("X-Command-Control-Context", signCtx())
	getLockRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(getLockRec, getLockReq)
	if getLockRec.Code != http.StatusMethodNotAllowed || getLockRec.Header().Get("Allow") != "PUT, DELETE" {
		t.Fatalf("unexpected GET priority-lock status=%d allow=%q", getLockRec.Code, getLockRec.Header().Get("Allow"))
	}

	// Empty control-state returns "devicePriorityLocks": [].
	emptyStateReq := httptest.NewRequest(http.MethodGet, statePath, nil)
	emptyStateReq.Header.Set("X-Command-Control-Context", signCtx())
	emptyStateRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(emptyStateRec, emptyStateReq)
	if emptyStateRec.Code != http.StatusOK || !strings.Contains(emptyStateRec.Body.String(), `"devicePriorityLocks":[]`) {
		t.Fatalf("expected non-null empty devicePriorityLocks JSON, status=%d body=%s", emptyStateRec.Code, emptyStateRec.Body.String())
	}

	// Unknown JSON field rejected with 400.
	badPutReq := httptest.NewRequest(http.MethodPut, lockPath, bytes.NewBufferString(`{"lockedPriority":"MANUAL_OVERRIDE","reason":"ok","extra":1}`))
	badPutReq.Header.Set("X-Command-Control-Context", signCtx())
	badPutRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(badPutRec, badPutReq)
	if badPutRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on unknown JSON field, got %d body=%s", badPutRec.Code, badPutRec.Body.String())
	}

	// Valid PUT installs lock and GET control-state surfaces it.
	putReq := httptest.NewRequest(http.MethodPut, lockPath, bytes.NewBufferString(`{"lockedPriority":"PROTECTION_RULE","durationMinutes":60,"reason":"coil freeze guard"}`))
	putReq.Header.Set("X-Command-Control-Context", signCtx())
	putRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT lock status=%d body=%s", putRec.Code, putRec.Body.String())
	}

	stateReq := httptest.NewRequest(http.MethodGet, statePath, nil)
	stateReq.Header.Set("X-Command-Control-Context", signCtx())
	stateRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(stateRec, stateReq)
	var state SiteControlStateView
	if stateRec.Code != http.StatusOK || json.NewDecoder(stateRec.Body).Decode(&state) != nil {
		t.Fatalf("GET state failed status=%d", stateRec.Code)
	}
	if len(state.DevicePriorityLocks) != 1 || state.DevicePriorityLocks[0].LockedPriority != "PROTECTION_RULE" {
		t.Fatalf("expected 1 PROTECTION_RULE lock in control-state, got %+v", state.DevicePriorityLocks)
	}

	// DELETE via JSON body then idempotently via ?reason= query fallback.
	delReq := httptest.NewRequest(http.MethodDelete, lockPath, bytes.NewBufferString(`{"reason":"coil recovered"}`))
	delReq.Header.Set("X-Command-Control-Context", signCtx())
	delRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(delRec, delReq)
	var rel1 DevicePriorityLockReleaseResponse
	if delRec.Code != http.StatusOK || json.NewDecoder(delRec.Body).Decode(&rel1) != nil || !rel1.Released {
		t.Fatalf("DELETE JSON failed status=%d resp=%+v", delRec.Code, rel1)
	}

	delReq2 := httptest.NewRequest(http.MethodDelete, lockPath+"?reason=confirm+cleared", nil)
	delReq2.Header.Set("X-Command-Control-Context", signCtx())
	delRec2 := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(delRec2, delReq2)
	var rel2 DevicePriorityLockReleaseResponse
	if delRec2.Code != http.StatusOK || json.NewDecoder(delRec2.Body).Decode(&rel2) != nil || rel2.Released {
		t.Fatalf("DELETE query fallback failed status=%d resp=%+v", delRec2.Code, rel2)
	}
}

func TestControlModeAndShutdownPopulateDevicePriorityLocks(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)
	op.StepUpTOTPVerified = true

	if _, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, DevicePriorityLockInput{
		LockedPriority: "MANUAL_OVERRIDE",
		Reason:         "hold chiller during inspection",
	}); err != nil {
		t.Fatalf("PutDevicePriorityLock: %v", err)
	}

	modeResp, err := h.Authority.PutSiteControlMode(context.Background(), op, caSiteID, SiteControlModeInput{
		ControlMode:    SiteControlModeManualOverride,
		Reason:         "switch to manual override",
		IdempotencyKey: "mode-lock-pop-1",
	})
	if err != nil {
		t.Fatalf("PutSiteControlMode: %v", err)
	}
	if len(modeResp.DevicePriorityLocks) != 1 || modeResp.DevicePriorityLocks[0].DeviceID != caChillerDev {
		t.Fatalf("expected active DevicePriorityLocks in PutSiteControlMode response, got %#v", modeResp.DevicePriorityLocks)
	}

	shutResp, err := h.Authority.ExecuteSiteControlShutdown(context.Background(), op, caSiteID, SiteControlShutdownInput{
		Mode:           ShutdownModePlantSafe,
		Reason:         "safe stop test",
		IdempotencyKey: "shut-lock-pop-1",
	})
	if err != nil {
		t.Fatalf("ExecuteSiteControlShutdown: %v", err)
	}
	if len(shutResp.SiteControlState.DevicePriorityLocks) != 1 || shutResp.SiteControlState.DevicePriorityLocks[0].DeviceID != caChillerDev {
		t.Fatalf("expected active DevicePriorityLocks in ExecuteSiteControlShutdown response, got %#v", shutResp.SiteControlState.DevicePriorityLocks)
	}
}

func TestSubmitProtectionRuleCommandResolvesPointCodeAndEnforcesPriority(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)

	// Missing point code returns 404.
	if _, err := h.Authority.SubmitProtectionRuleCommand(context.Background(), ProtectionRuleCommandInput{
		TenantID:        caTenantID,
		SiteID:          caSiteID,
		DeviceID:        caChillerDev,
		TargetPointCode: "missing_point",
		Capability:      commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters:      commandmodel.CommandParameters{"setpointC": 23.5},
		IdempotencyKey:  "alarm-rule-action:01910000-0000-7000-8000-000000000001",
		OutboxID:        "01910000-0000-7000-8000-000000000001",
		AssignmentID:    "01910000-7a02-7000-8000-000000000001",
	}); err == nil {
		t.Fatal("expected 404 for unknown targetPointCode")
	} else {
		var ce *ControlError
		if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusNotFound {
			t.Fatalf("expected 404 ControlError, got %v", err)
		}
	}

	// Valid human point_code resolves caSetpointPt and submits with PROTECTION_RULE priority.
	res, err := h.Authority.SubmitProtectionRuleCommand(context.Background(), ProtectionRuleCommandInput{
		TenantID:        caTenantID,
		SiteID:          caSiteID,
		DeviceID:        caChillerDev,
		TargetPointCode: "temp_sp",
		Capability:      commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters:      commandmodel.CommandParameters{"setpointC": 23.5},
		IdempotencyKey:  "alarm-rule-action:01910000-0000-7000-8000-000000000002",
		TraceID:         "alarm-action-01910000-0000-7000-8000-000000000002",
		OutboxID:        "01910000-0000-7000-8000-000000000002",
		AssignmentID:    "01910000-7a02-7000-8000-000000000001",
	})
	if err != nil {
		t.Fatalf("SubmitProtectionRuleCommand failed: %v", err)
	}
	if res.Intent.PointID != caSetpointPt || res.Intent.PriorityTier != commandmodel.PriorityProtectionRule || res.Intent.CommandSource != commandmodel.SourceProtectionRule {
		t.Fatalf("unexpected submitted protection command intent: %+v", res.Intent)
	}

	// Higher MANUAL_OVERRIDE device priority lock blocks PROTECTION_RULE submission.
	if _, err := h.Authority.PutDevicePriorityLock(context.Background(), op, caSiteID, caChillerDev, DevicePriorityLockInput{
		LockedPriority: "MANUAL_OVERRIDE",
		Reason:         "operator override",
	}); err != nil {
		t.Fatalf("PutDevicePriorityLock: %v", err)
	}
	if _, err := h.Authority.SubmitProtectionRuleCommand(context.Background(), ProtectionRuleCommandInput{
		TenantID:        caTenantID,
		SiteID:          caSiteID,
		DeviceID:        caChillerDev,
		TargetPointCode: "temp_sp",
		Capability:      commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters:      commandmodel.CommandParameters{"setpointC": 24.0},
		IdempotencyKey:  "alarm-rule-action:01910000-0000-7000-8000-000000000003",
		OutboxID:        "01910000-0000-7000-8000-000000000003",
		AssignmentID:    "01910000-7a02-7000-8000-000000000001",
	}); err == nil {
		t.Fatal("expected DEVICE_PRIORITY_LOCKED conflict when MANUAL_OVERRIDE lock holds")
	} else {
		var ce *ControlError
		if !errors.As(err, &ce) || ce.Code != "DEVICE_PRIORITY_LOCKED" {
			t.Fatalf("expected DEVICE_PRIORITY_LOCKED ControlError, got %v", err)
		}
	}
}

func TestControlAuthorityRejectsHardRealtimeSafetyBoundaryEvenWithEmergencyBypass(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)
	op.StepUpTOTPVerified = true
	stopProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStop)

	// Register a hard real-time safety PLC trip point on an asset.
	safetyPtID := "018f3e00-4000-7000-8000-000000000099"
	model := h.Twin.Models[caSiteID]
	model.TelemetryPoints = append(model.TelemetryPoints, TelemetryPointDTO{
		ID:                safetyPtID,
		TenantID:          caTenantID,
		SiteID:            caSiteID,
		ReportingDeviceID: caChillerDev,
		PointCode:         "plc_estop_trip",
		PointType:         "COMMAND",
		ValueType:         "BOOLEAN",
		Writable:          true,
		Status:            "ACTIVE",
		SourceMetadata: map[string]any{
			"capability":         "STOP",
			"capabilityRevision": stopProfile.Revision,
			"safetyBoundary":     "HARD_REALTIME_SAFETY",
			"feedbackPointKey":   "run_state",
		},
	})
	model.Relationships = append(model.Relationships, AssetRelationshipDTO{
		ID: "rel-safety-pt", TenantID: caTenantID, SiteID: caSiteID,
		FromType: "POINT", FromID: safetyPtID, ToType: "ASSET", ToID: caChillerID,
		Role: "CONTROLS", Status: "ACTIVE", ValidFrom: now.Add(-time.Hour).Format(time.RFC3339),
	})
	h.Twin.Models[caSiteID] = model
	h.Live.Set(caChillerDev, "plc_estop_trip", healthyTextObs(now, "ARMED", 21), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000021")

	_, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: safetyPtID, IdempotencyKey: "k-hr-operator",
	})
	var ce *ControlError
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusForbidden || ce.Code != "HARD_REALTIME_SAFETY_BOUNDARY" {
		t.Fatalf("expected 403 HARD_REALTIME_SAFETY_BOUNDARY on operator submit, got %v", err)
	}
	if h.IAM.CommandAuthorizeCalls != 0 {
		t.Fatalf("hard-realtime rejection must consume 0 IAM grants, got %d", h.IAM.CommandAuthorizeCalls)
	}

	_, err = h.Authority.SubmitProtectionRuleCommand(context.Background(), ProtectionRuleCommandInput{
		TenantID:        caTenantID,
		SiteID:          caSiteID,
		DeviceID:        caChillerDev,
		TargetPointCode: "plc_estop_trip",
		Capability:      commandmodel.CapabilityStop,
		IdempotencyKey:  "alarm-rule-action:01910000-0000-7000-8000-000000000099",
		OutboxID:        "01910000-0000-7000-8000-000000000099",
		AssignmentID:    "01910000-7a02-7000-8000-000000000099",
	})
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusForbidden || ce.Code != "HARD_REALTIME_SAFETY_BOUNDARY" {
		t.Fatalf("expected 403 HARD_REALTIME_SAFETY_BOUNDARY on protection rule submit, got %v", err)
	}

	_, rej, _, evalErr := h.Authority.evaluatePreDispatch(
		context.Background(),
		commandmodel.DispatchEnvelope{CommandID: "018f3e00-5000-7000-8000-000000000099", ExecutionFence: 1},
		commandmodel.CommandIntent{
			ID:               "018f3e00-5000-7000-8000-000000000099",
			TenantID:         caTenantID,
			SiteID:           caSiteID,
			DeviceID:         caChillerDev,
			PointID:          "plc_estop_trip",
			Capability:       commandmodel.CapabilityStop,
			IdempotencyKey:   "alarm-rule-action:01910000-0000-7000-8000-000000000099",
			PreconditionMode: commandmodel.PreconditionEmergencyBypass,
			BypassReason:     "bypass hardware trip",
			PriorityTier:     commandmodel.PrioritySafetyInterlock,
			CommandSource:    commandmodel.SourceProtectionRule,
		},
		ControlDispatchContext{
			TenantID:       caTenantID,
			SiteID:         caSiteID,
			DeviceID:       caChillerDev,
			PointID:        safetyPtID,
			ControlMode:    SiteControlModeRemoteAuto,
			SafetyBoundary: "HARD_REALTIME_SAFETY",
		},
	)
	if evalErr != nil {
		t.Fatalf("evaluatePreDispatch error = %v", evalErr)
	}
	if rej == nil || rej.HTTPStatus != http.StatusConflict || rej.Code != "DISPATCH_SAFETY_HARD_REALTIME_BOUNDARY" {
		t.Fatalf("expected 409 DISPATCH_SAFETY_HARD_REALTIME_BOUNDARY overriding emergency bypass, got %+v", rej)
	}
}

func TestControlAuthorityBatchSubmitPartialReplayStepUpCancelAndClaimSkip(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)

	// Configure a second setpoint point on pump device so batch targets two distinct devices.
	spProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilitySetTemperatureSetpoint)
	pumpSetpointPt := "018f3e00-4000-7000-8000-000000000088"
	model := h.Twin.Models[caSiteID]
	model.TelemetryPoints = append(model.TelemetryPoints,
		TelemetryPointDTO{
			ID: "018f3e00-4100-7000-8000-000000000088", TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caPumpDev,
			PointCode: "zone_temperature", PointType: "TELEMETRY", ValueType: "NUMBER", Status: "ACTIVE",
		},
		TelemetryPointDTO{
			ID: pumpSetpointPt, TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caPumpDev,
			PointCode: "pump_temp_sp", PointType: "COMMAND", ValueType: "NUMBER", Writable: true, Status: "ACTIVE",
			SourceMetadata: map[string]any{"capability": "SET_TEMPERATURE_SETPOINT", "capabilityRevision": spProfile.Revision, "feedbackPointKey": "zone_temperature"},
		},
	)
	model.Relationships = append(model.Relationships, AssetRelationshipDTO{
		ID: "rel-pump-sp", TenantID: caTenantID, SiteID: caSiteID,
		FromType: "POINT", FromID: pumpSetpointPt, ToType: "ASSET", ToID: caPumpID,
		Role: "CONTROLS", Status: "ACTIVE", ValidFrom: now.Add(-time.Hour).Format(time.RFC3339),
	})
	h.Twin.Models[caSiteID] = model

	val22 := 22.0
	h.Live.Set(caChillerDev, "zone_temperature", &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "SLEEPING", Readiness: "CURRENT", Freshness: "STALE", Quality: "GOOD",
		BusinessRevision: 15, ObservedAt: now.Add(-time.Minute),
	}, "s2:sleep")
	h.Live.Set(caPumpDev, "zone_temperature", &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: 16, CurrentValue: &val22, ReportedValue: commandmodel.NumberScalar(22), ObservedAt: now,
	}, "s2:online")

	// 1. Fail-closed TOTP check BEFORE writing any batch or child command when START targets need step-up.
	h.Live.Set(caChillerDev, "run_state", &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: 10, ReportedValue: commandmodel.TextScalar("STOPPED"), ObservedAt: now,
	}, "s2:run-stop")
	h.Live.Set(caPumpDev, "run_state", &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: 11, ReportedValue: commandmodel.TextScalar("RUNNING"), ObservedAt: now,
	}, "s2:run-ok")
	op.StepUpTOTPVerified = false
	_, err := h.Authority.SubmitOperatorCommandBatch(context.Background(), op, SubmitCommandBatchInput{
		SiteID: caSiteID, Capability: commandmodel.CapabilityStart, IdempotencyKey: "batch-totp-precheck",
		Targets: []CommandBatchTargetInput{{DeviceID: caChillerDev, PointID: caStartPt}},
	})
	var ce *ControlError
	if !errors.As(err, &ce) || ce.HTTPStatus != 428 || ce.Code != "STEP_UP_REQUIRED" {
		t.Fatalf("expected 428 STEP_UP_REQUIRED before writes, got %v", err)
	}
	listedAfterStepUp, _ := h.Authority.ListOperatorCommandBatches(context.Background(), op, caSiteID, 10)
	if len(listedAfterStepUp.Items) != 0 {
		t.Fatalf("expected zero persisted batches when step-up fails pre-check, got %d", len(listedAfterStepUp.Items))
	}

	// 2. Partial batch: caChillerDev (SLEEPING) + caPumpDev (ONLINE) + bad target -> 2 admitted, 1 rejected.
	ttl := 300
	batchInput := SubmitCommandBatchInput{
		SiteID:         caSiteID,
		Capability:     commandmodel.CapabilitySetTemperatureSetpoint,
		DeliveryMode:   commandmodel.DeliveryModeQueueIfOffline,
		TTLSeconds:     &ttl,
		IdempotencyKey: "batch-sp-001",
		Reason:         "Night setback group broadcast",
		Targets: []CommandBatchTargetInput{
			{DeviceID: caChillerDev, PointID: caSetpointPt, Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24}},
			{DeviceID: caPumpDev, PointID: pumpSetpointPt, Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 23}},
			{DeviceID: caEmergDev, PointID: "018f3e00-4000-7000-8000-000000000999", Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 23}},
		},
	}
	created, err := h.Authority.SubmitOperatorCommandBatch(context.Background(), op, batchInput)
	if err != nil {
		t.Fatalf("SubmitOperatorCommandBatch failed: %v", err)
	}
	if created.AcceptedCount != 2 || created.RejectedCount != 1 || created.Status != "PENDING" || len(created.Items) != 3 {
		t.Fatalf("unexpected batch counts/status: %+v", created)
	}

	// 3. Idempotent replay returns replayed=true without duplicating commands; conflict returns 409.
	replayed, err := h.Authority.SubmitOperatorCommandBatch(context.Background(), op, batchInput)
	if err != nil || !replayed.Replayed || replayed.ID != created.ID {
		t.Fatalf("expected idempotent replay, got replayed=%+v err=%v", replayed, err)
	}
	conflictInput := batchInput
	conflictInput.Reason = "Changed payload"
	if _, err := h.Authority.SubmitOperatorCommandBatch(context.Background(), op, conflictInput); !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict {
		t.Fatalf("expected 409 conflict on reused key with changed payload, got %v", err)
	}

	// 4. ClaimDispatch skips sleeping chiller (deferring it) and claims online pump on the same ClaimDispatch call!
	h.Live.Set(caChillerDev, "run_state", &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "SLEEPING", Readiness: "CURRENT", Freshness: "STALE", Quality: "GOOD",
		BusinessRevision: 15, ObservedAt: now.Add(-time.Minute),
	}, "s2:sleep")
	h.Live.Set(caPumpDev, "run_state", &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: 16, CurrentValue: &val22, ReportedValue: commandmodel.NumberScalar(22), ObservedAt: now,
	}, "s2:online")

	claimedEnv, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "dispatcher-batch-test", 15*time.Second)
	if err != nil {
		t.Fatalf("expected ClaimDispatch to skip sleeping chiller and claim online pump, got err=%v", err)
	}
	if claimedEnv.DeviceID != caPumpDev {
		t.Fatalf("expected online caPumpDev to be claimed, got %s", claimedEnv.DeviceID)
	}

	// 5. Cancel remaining QUEUED sleeping command in the batch + test OperatorHTTPHandler routes + ObservabilityRoute.
	cancelResp, err := h.Authority.CancelOperatorCommandBatch(context.Background(), op, caSiteID, created.ID, "cancel remaining")
	if err != nil {
		t.Fatalf("CancelOperatorCommandBatch failed: %v", err)
	}
	if len(cancelResp.CancelledCommandIDs) != 1 || cancelResp.CancelledCommandIDs[0] != *created.Items[0].CommandID {
		t.Fatalf("expected only sleeping item 0 cancelled, got %+v", cancelResp)
	}

	getBatchReq := httptest.NewRequest(http.MethodGet, InternalOperatorPrefix+"/sites/"+caSiteID+"/command-batches/"+created.ID, nil)
	if got := ObservabilityRoute(getBatchReq); got != "command-batches.get" {
		t.Fatalf("ObservabilityRoute(get batch) = %q, want command-batches.get", got)
	}
	postBatchReq := httptest.NewRequest(http.MethodPost, InternalOperatorPrefix+"/sites/"+caSiteID+"/command-batches", nil)
	if got := ObservabilityRoute(postBatchReq); got != "command-batches.create" {
		t.Fatalf("ObservabilityRoute(post batch) = %q, want command-batches.create", got)
	}
	wakeReq := httptest.NewRequest(http.MethodPost, InternalDispatchWakePath, nil)
	if got := ObservabilityRoute(wakeReq); got != "dispatch.wake" {
		t.Fatalf("ObservabilityRoute(wake) = %q, want dispatch.wake", got)
	}
}
