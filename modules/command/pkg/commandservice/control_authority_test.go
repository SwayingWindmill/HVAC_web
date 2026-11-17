package commandservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

	h.Live.SetError(caPumpDev, "run_state", unavailableControlError("transient telemetry timeout"))
	cmd3, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, IdempotencyKey: "cmd-q3",
	})
	if err != nil {
		// Wait: Submit fails because Live.ReadLivePoint returns 503; restore healthy for submit then inject error before ClaimDispatch!
		h.Live.Set(caPumpDev, "run_state", healthyTextObs(now, "RUNNING", 21), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000011")
		cmd3, err = h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
			AssetID: caPumpID, CommandPointID: caPumpStopPt, IdempotencyKey: "cmd-q3",
		})
		if err != nil {
			t.Fatalf("submit cmd3 failed: %v", err)
		}
		h.Live.SetError(caPumpDev, "run_state", unavailableControlError("transient telemetry timeout"))
	}
	_, transientErr := h.Authority.ClaimDispatch(context.Background(), caTenantID, "dispatcher-1", 30*time.Second)
	if !errors.Is(transientErr, ErrNoDispatchAvailable) {
		t.Fatalf("expected ErrNoDispatchAvailable on transient failure, got %v", transientErr)
	}
	got3, _ := h.Store.Get(context.Background(), caTenantID, cmd3.View.CommandID)
	// Must remain QUEUED (requeued once, NOT burned 3 times in the same ClaimDispatch call).
	if got3.Status != commandmodel.IntentQueued || len(got3.Attempts) != 1 {
		t.Fatalf("expected 1 retryable attempt keeping QUEUED, got status=%s attempts=%d", got3.Status, len(got3.Attempts))
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
