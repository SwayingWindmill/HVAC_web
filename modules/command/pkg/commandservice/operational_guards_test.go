package commandservice

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func validTestEdgeExecution(value string) *commandmodel.EdgeExecutionEvidence {
	v := commandmodel.TextScalar(value)
	return &commandmodel.EdgeExecutionEvidence{
		Requested:          commandmodel.TextScalar(value),
		Effective:          &v,
		Applied:            &v,
		WinnerControllerID: "cloud-command-intent",
		Cycle:              1,
	}
}

func seedCommandHistoryEntry(
	t *testing.T,
	h *TestControlHarness,
	op OperatorIdentity,
	deviceID, pointID string,
	capability commandmodel.Capability,
	finalStatus commandmodel.IntentStatus,
	at time.Time,
	idemKey string,
) string {
	t.Helper()
	profile, ok := commandmodel.CapabilityProfileFor(capability)
	if !ok {
		t.Fatalf("missing capability profile for %s", capability)
	}
	_, claims, err := h.IAM.AuthorizeCommand(context.Background(), op, commandmodel.AuthorizationCommandSubmit, caSiteID, deviceID, capability, profile.Revision)
	if err != nil {
		t.Fatalf("authorize seed command: %v", err)
	}
	res, err := h.Store.Submit(context.Background(), commandmodel.SubmitRequest{
		TenantID:             caTenantID,
		SiteID:               caSiteID,
		DeviceID:             deviceID,
		PointID:              pointID,
		PrincipalID:          claims.PrincipalID,
		IdempotencyKey:       idemKey,
		Capability:           capability,
		Parameters:           commandmodel.CommandParameters{},
		VerificationPointKey: "run_state",
		PreconditionMode:     commandmodel.PreconditionRequireCurrentState,
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE",
			Presence:               "ONLINE",
			Readiness:              "CURRENT",
			Quality:                "GOOD",
			BusinessRevision:       10,
			ObservedAt:             at,
		},
		Authorization: commandauth.Snapshot(claims),
	})
	if err != nil {
		t.Fatalf("submit seed command: %v", err)
	}
	cmdID := res.Intent.ID
	if finalStatus == commandmodel.IntentDispatching || finalStatus == commandmodel.IntentSucceeded {
		env, err := h.Store.ClaimDispatch(context.Background(), caTenantID, "disp-seed", time.Minute)
		if err != nil || env.CommandID != cmdID {
			t.Fatalf("claim seed dispatch: env=%+v err=%v", env, err)
		}
		if err := h.Store.ResolveDispatch(context.Background(), env, commandmodel.ConnectorResult{
			Phase:         commandmodel.ConnectorAcknowledged,
			Acknowledged:  true,
			EvidenceID:    "ev-ack-" + idemKey,
			EdgeExecution: validTestEdgeExecution(string(capability)),
		}); err != nil {
			t.Fatalf("resolve seed dispatch: %v", err)
		}
	}
	if finalStatus == commandmodel.IntentSucceeded {
		venv, err := h.Store.ClaimVerification(context.Background(), caTenantID, "ver-seed", time.Minute)
		if err != nil || venv.CommandID != cmdID {
			t.Fatalf("claim seed verification: venv=%+v err=%v", venv, err)
		}
		reported := "STOPPED"
		if isStartCapability(capability) {
			reported = "RUNNING"
		}
		if err := h.Store.ResolveVerification(context.Background(), venv, commandmodel.VerificationResult{
			Outcome:    commandmodel.VerificationSucceeded,
			EvidenceID: "ev-ver-" + idemKey,
			Reported: commandmodel.ReportedStateEvidence{
				TenantID:               caTenantID,
				SiteID:                 caSiteID,
				DeviceID:               deviceID,
				EvaluationAvailability: "AVAILABLE",
				Presence:               "ONLINE",
				Readiness:              "CURRENT",
				Freshness:              "FRESH",
				Quality:                "GOOD",
				BusinessRevision:       100,
				ReportedValue:          commandmodel.TextScalar(reported),
				ObservedAt:             h.Store.now().UTC().Add(time.Second),
			},
		}); err != nil {
			t.Fatalf("resolve seed verification: %v", err)
		}
	}
	h.Store.SetCommandUpdatedAtForTest(cmdID, at)
	return cmdID
}

func TestControlAuthorityAuxStartupBlindWindow(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	clk := now
	h, err := NewControlAuthorityForTest(func() time.Time { return clk })
	if err != nil {
		t.Fatal(err)
	}
	baseH, op := seedPlantHarness(t, now)
	h.IAM.AllowedSites = baseH.IAM.AllowedSites
	h.Twin.BoundDevices = baseH.Twin.BoundDevices
	h.Twin.Assets = baseH.Twin.Assets
	h.Twin.Models = baseH.Twin.Models
	op.StepUpTOTPVerified = true

	// Chiller run_state is STOPPED (so static interlock passes) and pump is RUNNING.
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "STOPPED", 11), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000001")
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(clk, "RUNNING", 12), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000002")

	// 1. In-flight (QUEUED) START on chiller blocks auxiliary pump STOP.
	inFlightID := seedCommandHistoryEntry(t, h, op, caChillerDev, caStartPt, commandmodel.CapabilityStart, commandmodel.IntentQueued, clk, "seed-chiller-queued-start")
	_, err = h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "pump-stop-inflight-block",
	})
	var ce *ControlError
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_INTERLOCK_BLOCKED" || !strings.Contains(ce.Detail, "in progress") {
		t.Fatalf("expected 409 COMMAND_INTERLOCK_BLOCKED (in progress), got %v", err)
	}
	if _, err := h.Authority.CancelOperatorCommand(context.Background(), op, inFlightID, "clear queued start"); err != nil {
		t.Fatalf("cancel queued start: %v", err)
	}

	// 2. Succeeded START 3m ago keeps pump blocked inside 6-minute blind window.
	seedCommandHistoryEntry(t, h, op, caChillerDev, caStartPt, commandmodel.CapabilityStart, commandmodel.IntentSucceeded, clk.Add(-3*time.Minute), "seed-chiller-recent-start")
	_, err = h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "pump-stop-blind-window",
	})
	ce = nil
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_INTERLOCK_BLOCKED" || !strings.Contains(ce.Detail, "6-minute") {
		t.Fatalf("expected 409 COMMAND_INTERLOCK_BLOCKED (6-minute blind window), got %v", err)
	}

	// 3. Advancing past 6 minutes unblocks pump STOP.
	clk = clk.Add(4 * time.Minute)
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "STOPPED", 15), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000003")
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(clk, "RUNNING", 16), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000004")
	op.IssuedAt = clk
	op.ExpiresAt = clk.Add(5 * time.Minute)
	if _, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "pump-stop-after-blind-window",
	}); err != nil {
		t.Fatalf("expected pump STOP to succeed after 6-minute blind window, got %v", err)
	}
}

func TestControlAuthorityChillerCooldownsAndFailClosedHistory(t *testing.T) {
	baseNow := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	clk := baseNow
	h, err := NewControlAuthorityForTest(func() time.Time { return clk })
	if err != nil {
		t.Fatal(err)
	}
	baseH, op := seedPlantHarness(t, baseNow)
	h.IAM.AllowedSites = baseH.IAM.AllowedSites
	h.Twin.BoundDevices = baseH.Twin.BoundDevices
	h.Twin.Assets = baseH.Twin.Assets
	h.Twin.Models = baseH.Twin.Models

	// Fail-closed 503 when store history lookup fails.
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "RUNNING", 11), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000001")
	h.Store.ListErr = errors.New("postgres read timeout")
	op.StepUpTOTPVerified = true
	_, err = h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "stop-history-err",
	})
	var ce *ControlError
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusServiceUnavailable || ce.Code != "COMMAND_UNAVAILABLE" {
		t.Fatalf("expected fail-closed 503 COMMAND_UNAVAILABLE when store.List fails, got %v", err)
	}
	h.Store.ListErr = nil

	// 1. 20m min-run dwell blocks chiller STOP (before STEP_UP_REQUIRED check too).
	seedCommandHistoryEntry(t, h, op, caChillerDev, caStartPt, commandmodel.CapabilityStart, commandmodel.IntentSucceeded, clk.Add(-50*time.Minute), "seed-start-1")
	h.Store.SetCommandUpdatedAtForTest(
		func() string {
			cmds, _ := h.Store.List(context.Background(), caTenantID, caSiteID, 1)
			return cmds[0].ID
		}(),
		clk.Add(-10*time.Minute),
	)
	op.StepUpTOTPVerified = false
	_, err = h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "stop-min-run-block",
	})
	ce = nil
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_COOLDOWN_ACTIVE" || !strings.Contains(ce.Detail, "20-minute") {
		t.Fatalf("expected 409 COMMAND_COOLDOWN_ACTIVE (20-minute), got %v", err)
	}

	// Advancing +11m allows STOP with step-up verified.
	clk = clk.Add(11 * time.Minute)
	op.StepUpTOTPVerified = true
	op.IssuedAt = clk
	op.ExpiresAt = clk.Add(5 * time.Minute)
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "RUNNING", 12), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000002")
	if _, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "stop-after-min-run",
	}); err != nil {
		t.Fatalf("expected STOP after 21m run dwell to succeed, got %v", err)
	}

	// Fresh harness for 15m min-off dwell + 2-starts/60m anti-recycle.
	clk = baseNow
	h2, err := NewControlAuthorityForTest(func() time.Time { return clk })
	if err != nil {
		t.Fatal(err)
	}
	h2.IAM.AllowedSites = baseH.IAM.AllowedSites
	h2.Twin.BoundDevices = baseH.Twin.BoundDevices
	h2.Twin.Assets = baseH.Twin.Assets
	h2.Twin.Models = baseH.Twin.Models
	op.IssuedAt = clk
	op.ExpiresAt = clk.Add(5 * time.Minute)
	h2.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "STOPPED", 20), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000003")
	h2.Live.Set(caPumpDev, "run_state", healthyTextObs(clk, "RUNNING", 21), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000004")

	// Seed: Start at -42m, Stop at -37m, Start at -20m, Stop at -17m => after -17m min-off (17m > 15m),
	// wait: test min-off first when Stop is at -8m.
	seedCommandHistoryEntry(t, h2, op, caChillerDev, caStopPt, commandmodel.CapabilityStop, commandmodel.IntentSucceeded, clk.Add(-8*time.Minute), "seed-stop-minoff")
	_, err = h2.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStartPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "start-min-off-block",
	})
	ce = nil
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_COOLDOWN_ACTIVE" || !strings.Contains(ce.Detail, "15-minute") {
		t.Fatalf("expected 409 COMMAND_COOLDOWN_ACTIVE (15-minute min off), got %v", err)
	}

	// Move forward so Stop is -17m (>15m min off), and 2 starts exist within [-42m, -20m] => anti-recycle blocks 3rd start!
	clk = clk.Add(9 * time.Minute)
	op.IssuedAt = clk
	op.ExpiresAt = clk.Add(5 * time.Minute)
	h2.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "STOPPED", 22), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000005")
	h2.Live.Set(caPumpDev, "run_state", healthyTextObs(clk, "RUNNING", 23), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000006")
	seedCommandHistoryEntry(t, h2, op, caChillerDev, caStartPt, commandmodel.CapabilityStart, commandmodel.IntentSucceeded, clk.Add(-42*time.Minute), "seed-start-win-1")
	seedCommandHistoryEntry(t, h2, op, caChillerDev, caStopPt, commandmodel.CapabilityStop, commandmodel.IntentSucceeded, clk.Add(-38*time.Minute), "seed-stop-win-1")
	seedCommandHistoryEntry(t, h2, op, caChillerDev, caStartPt, commandmodel.CapabilityStart, commandmodel.IntentSucceeded, clk.Add(-20*time.Minute), "seed-start-win-2")
	seedCommandHistoryEntry(t, h2, op, caChillerDev, caStopPt, commandmodel.CapabilityStop, commandmodel.IntentSucceeded, clk.Add(-17*time.Minute), "seed-stop-win-2")

	_, err = h2.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStartPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "start-anti-recycle-block",
	})
	ce = nil
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_COOLDOWN_ACTIVE" || !strings.Contains(ce.Detail, "maximum of 2 starts") {
		t.Fatalf("expected 409 COMMAND_COOLDOWN_ACTIVE (anti-recycle), got %v", err)
	}

	// Advancing +20m ages out win-1 (>60m ago), unblocking START.
	clk = clk.Add(20 * time.Minute)
	op.IssuedAt = clk
	op.ExpiresAt = clk.Add(5 * time.Minute)
	h2.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "STOPPED", 24), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000007")
	h2.Live.Set(caPumpDev, "run_state", healthyTextObs(clk, "RUNNING", 25), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000008")
	if _, err := h2.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStartPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "start-after-anti-recycle",
	}); err != nil {
		t.Fatalf("expected START after anti-recycle window expiry to succeed, got %v", err)
	}
}

func TestControlAuthorityLastActiveChillerGuard(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC) // Monday (1), 10:00 UTC = minute 600
	h, op := seedPlantHarness(t, now)
	op.StepUpTOTPVerified = true

	chillerBID := "018f3e00-2000-7000-8000-000000000009"
	chillerBDev := "018f3e00-3000-7000-8000-000000000009"
	h.Twin.BoundDevices[chillerBID] = []string{chillerBDev}
	model := h.Twin.Models[caSiteID]
	model.Assets = append(model.Assets, AssetDTO{ID: chillerBID, TenantID: caTenantID, SiteID: caSiteID, Code: "chiller-b", AssetType: "CHILLER", Status: "ACTIVE"})
	model.Devices = append(model.Devices, DeviceDTO{ID: chillerBDev, TenantID: caTenantID, SiteID: caSiteID, Code: "ctrl-ch-b", Status: "ACTIVE"})
	model.TelemetryPoints = append(model.TelemetryPoints, TelemetryPointDTO{
		ID: "018f3e00-4100-7000-8000-000000000009", TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: chillerBDev,
		PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE",
	})
	model.Relationships = append(model.Relationships, AssetRelationshipDTO{
		ID: "rel-chb-dev", TenantID: caTenantID, SiteID: caSiteID, FromType: "DEVICE", FromID: chillerBDev, ToType: "ASSET", ToID: chillerBID, Role: "MONITORED_BY", Status: "ACTIVE", ValidFrom: now.Format(time.RFC3339),
	})
	h.Twin.Models[caSiteID] = model

	h.Twin.Schedules[caSiteID] = OperatingScheduleDTO{
		Timezone: "UTC",
		Slots:    []OperatingScheduleSlotDTO{{DayOfWeek: 1, StartMinute: 480, EndMinute: 1080}},
	}
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(now, "RUNNING", 30), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000001")
	h.Live.Set(chillerBDev, "run_state", healthyTextObs(now, "STOPPED", 31), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000002")

	// 1. Sole running chiller during cooling slot -> 409 COMMAND_LAST_ACTIVE_CHILLER_PROTECTED.
	_, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "last-chiller-blocked",
	})
	var ce *ControlError
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_LAST_ACTIVE_CHILLER_PROTECTED" {
		t.Fatalf("expected 409 COMMAND_LAST_ACTIVE_CHILLER_PROTECTED, got %v", err)
	}

	// 2. Approved MAINTENANCE_WINDOW lifts protection cleanly.
	h.Twin.Exceptions[caSiteID] = []OperatingExceptionDTO{{
		Kind: "MAINTENANCE_WINDOW", StartDate: "2026-11-16", EndDate: "2026-11-16", Status: "APPROVED",
	}}
	if _, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "last-chiller-maint-ok",
	}); err != nil {
		t.Fatalf("expected chiller STOP to succeed during MAINTENANCE_WINDOW exception, got %v", err)
	}

	// 3. Removing exception but starting second chiller also allows stopping chiller A.
	h.Twin.Exceptions[caSiteID] = nil
	h.Live.Set(chillerBDev, "run_state", healthyTextObs(now, "RUNNING", 32), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000003")
	if _, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "second-chiller-running-ok",
	}); err != nil {
		t.Fatalf("expected chiller STOP to succeed when chiller-b is running, got %v", err)
	}
}

func TestControlAuthorityDegradedSafeShutdownCoastDownAbortAndEmergencyAlarm(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	clk := now
	h, err := NewControlAuthorityForTest(func() time.Time { return clk })
	if err != nil {
		t.Fatal(err)
	}
	baseH, op := seedPlantHarness(t, now)
	h.IAM.AllowedSites = baseH.IAM.AllowedSites
	h.Twin.BoundDevices = baseH.Twin.BoundDevices
	h.Twin.Assets = baseH.Twin.Assets
	h.Twin.Models = baseH.Twin.Models
	op.StepUpTOTPVerified = true

	// 1. PLANT_SAFE_SHUTDOWN under 503 telemetry outage degrades cleanly: returns 200 OK,
	// dispatches chiller-a STOP via EMERGENCY_BYPASS, and defers chwp-1.
	h.Live.SetError(caChillerDev, "run_state", unavailableControlError("telemetry offline"))
	h.Live.SetError(caPumpDev, "run_state", unavailableControlError("telemetry offline"))
	res, err := h.Authority.ExecuteSiteControlShutdown(context.Background(), op, caSiteID, SiteControlShutdownInput{
		Mode: ShutdownModePlantSafe, Reason: "telemetry outage safe exit", IdempotencyKey: "degraded-503-shutdown",
	})
	if err != nil {
		t.Fatalf("degraded PLANT_SAFE_SHUTDOWN failed: %v", err)
	}
	if res.SiteControlState.ControlMode != SiteControlModeSafeShutdownExit || len(res.SubmittedCommandIDs) != 1 || !slices.Equal(res.DeferredHoldingAssetIds, []string{caPumpID}) {
		t.Fatalf("unexpected degraded shutdown result: %+v", res)
	}
	cmdIntent, err := h.Store.Get(context.Background(), caTenantID, res.SubmittedCommandIDs[0])
	if err != nil || cmdIntent.PointID != caStopPt || cmdIntent.PreconditionMode != commandmodel.PreconditionEmergencyBypass {
		t.Fatalf("expected EMERGENCY_BYPASS STOP on chiller-a, got intent=%+v err=%v", cmdIntent, err)
	}
	// Pre-dispatch ClaimDispatch must still pass even with live telemetry unavailable!
	env, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-degraded", time.Minute)
	if err != nil || env.CommandID != res.SubmittedCommandIDs[0] {
		t.Fatalf("expected degraded chiller STOP to pass pre-dispatch gate, env=%+v err=%v", env, err)
	}
	if err := h.Authority.ResolveDispatch(context.Background(), env, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "ev-ack-ch-stop", EdgeExecution: validTestEdgeExecution("STOP"),
	}); err != nil {
		t.Fatalf("resolve dispatch: %v", err)
	}
	venv, err := h.Authority.ClaimVerification(context.Background(), caTenantID, "ver-degraded", time.Minute)
	if err != nil {
		t.Fatalf("claim verification: %v", err)
	}
	if err := h.Authority.ResolveVerification(context.Background(), venv, commandmodel.VerificationResult{
		Outcome: commandmodel.VerificationSucceeded, EvidenceID: "ev-ver-ch-stop",
		Reported: commandmodel.ReportedStateEvidence{
			TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev,
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
			BusinessRevision: 50, ReportedValue: commandmodel.TextScalar("STOPPED"), ObservedAt: clk.Add(time.Second),
		},
	}); err != nil {
		t.Fatalf("resolve verification: %v", err)
	}

	// 2. GET /control-state under SAFE_SHUTDOWN_EXIT:
	// - While telemetry is 503 -> 200 OK non-blocking, does not release chwp-1.
	if state, err := h.Authority.GetSiteControlState(context.Background(), op, caSiteID); err != nil || state.ControlMode != SiteControlModeSafeShutdownExit {
		t.Fatalf("expected 200 SAFE_SHUTDOWN_EXIT during 503 telemetry, state=%+v err=%v", state, err)
	}
	// - Telemetry recovers at +20s (<60s coast-down since chiller stop succeeded) -> chwp-1 stays running without stop command.
	clk = clk.Add(20 * time.Second)
	op.IssuedAt = clk
	op.ExpiresAt = clk.Add(5 * time.Minute)
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "STOPPED", 51), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000010")
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(clk, "RUNNING", 52), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000011")
	if _, err := h.Authority.GetSiteControlState(context.Background(), op, caSiteID); err != nil {
		t.Fatalf("get state before coast-down: %v", err)
	}
	cmdsBefore, _ := h.Store.List(context.Background(), caTenantID, caSiteID, MaximumListedCommands)
	if len(cmdsBefore) != 1 {
		t.Fatalf("expected chwp-1 NOT released before 60s coast-down, got %d commands", len(cmdsBefore))
	}
	// - Advance past 60s coast-down -> GET /control-state auto-releases chwp-1 once (and deduplicates subsequent polls).
	clk = clk.Add(50 * time.Second)
	op.IssuedAt = clk
	op.ExpiresAt = clk.Add(5 * time.Minute)
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "STOPPED", 53), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000012")
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(clk, "RUNNING", 54), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000013")
	if _, err := h.Authority.GetSiteControlState(context.Background(), op, caSiteID); err != nil {
		t.Fatalf("get state after coast-down: %v", err)
	}
	cmdsAfter, _ := h.Store.List(context.Background(), caTenantID, caSiteID, MaximumListedCommands)
	if len(cmdsAfter) != 2 || cmdsAfter[0].PointID != caPumpStopPt {
		t.Fatalf("expected chwp-1 STOP auto-dispatched after 60s coast-down, got %+v", cmdsAfter)
	}
	if _, err := h.Authority.GetSiteControlState(context.Background(), op, caSiteID); err != nil {
		t.Fatalf("second poll get state: %v", err)
	}
	if cmdsPoll2, _ := h.Store.List(context.Background(), caTenantID, caSiteID, MaximumListedCommands); len(cmdsPoll2) != 2 {
		t.Fatalf("expected no duplicate pump STOP on repeated GET /control-state, got %d", len(cmdsPoll2))
	}

	// 3. Explicit ABORT_UNIT_SEQUENCE point + CancelOperatorCommand on DISPATCHING START_UNIT_SEQUENCE.
	clk2 := now
	hAbort, err := NewControlAuthorityForTest(func() time.Time { return clk2 })
	if err != nil {
		t.Fatal(err)
	}
	baseH2, op2 := seedPlantHarness(t, now)
	hAbort.IAM.AllowedSites = baseH2.IAM.AllowedSites
	hAbort.Twin.BoundDevices = baseH2.Twin.BoundDevices
	hAbort.Twin.Assets = baseH2.Twin.Assets
	op2.StepUpTOTPVerified = true

	startSeqProf, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStartUnitSequence)
	stopSeqProf, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStopUnitSequence)
	startSeqPt := "018f3e00-4000-7000-8000-000000000011"
	abortSeqPt := "018f3e00-4000-7000-8000-000000000012"
	m := baseH2.Twin.Models[caSiteID]
	m.TelemetryPoints = append(m.TelemetryPoints,
		TelemetryPointDTO{
			ID: startSeqPt, TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caChillerDev,
			PointCode: "start_unit_seq", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE",
			SourceMetadata: map[string]any{"capability": "START_UNIT_SEQUENCE", "capabilityRevision": startSeqProf.Revision, "feedbackPointKey": "run_state"},
		},
		TelemetryPointDTO{
			ID: abortSeqPt, TenantID: caTenantID, SiteID: caSiteID, ReportingDeviceID: caChillerDev,
			PointCode: "abort_unit_seq", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true, Status: "ACTIVE",
			SourceMetadata: map[string]any{"capability": "STOP_UNIT_SEQUENCE", "capabilityRevision": stopSeqProf.Revision, "actuationMode": "ABORT_UNIT_SEQUENCE", "feedbackPointKey": "run_state"},
		},
	)
	m.Relationships = append(m.Relationships,
		AssetRelationshipDTO{ID: "rel-start-seq", TenantID: caTenantID, SiteID: caSiteID, FromType: "POINT", FromID: startSeqPt, ToType: "ASSET", ToID: caChillerID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: clk2.Format(time.RFC3339)},
		AssetRelationshipDTO{ID: "rel-abort-seq", TenantID: caTenantID, SiteID: caSiteID, FromType: "POINT", FromID: abortSeqPt, ToType: "ASSET", ToID: caChillerID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: clk2.Format(time.RFC3339)},
	)
	hAbort.Twin.Models[caSiteID] = m
	hAbort.Live.Set(caChillerDev, "run_state", healthyTextObs(clk2, "STOPPED", 11), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000021")
	hAbort.Live.Set(caPumpDev, "run_state", healthyTextObs(clk2, "STOPPED", 12), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000022")

	// Shutdown mode ABORT_UNIT_SEQUENCE on already-STOPPED chiller sends abort_unit_seq without pulsing regular caStopPt.
	abortResp, err := hAbort.Authority.ExecuteSiteControlShutdown(context.Background(), op2, caSiteID, SiteControlShutdownInput{
		Mode: ShutdownModeAbortUnitSequence, UnitCode: "chiller-a", Reason: "abort sequence on stopped chiller",
	})
	if err != nil {
		t.Fatalf("abort shutdown failed: %v", err)
	}
	foundAbortPoint := false
	for _, id := range abortResp.SubmittedCommandIDs {
		v, _ := hAbort.Authority.GetOperatorCommand(context.Background(), op2, id)
		if v.PointID == caStopPt {
			t.Fatalf("did not expect regular STOP pulse when explicit ABORT_UNIT_SEQUENCE point exists and chiller is STOPPED")
		}
		if v.PointID == abortSeqPt {
			foundAbortPoint = true
		}
	}
	if !foundAbortPoint {
		t.Fatalf("expected abort_unit_seq point in SubmittedCommandIDs, got %+v", abortResp.SubmittedCommandIDs)
	}

	// CancelOperatorCommand on DISPATCHING START_UNIT_SEQUENCE aborts sequence and dispatches abort_unit_seq!
	seqSub, err := hAbort.Authority.SubmitOperatorCommand(context.Background(), op2, SubmitOperatorCommandInput{
		AssetID: caChillerID, CommandPointID: startSeqPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "start-seq-cancel-dispatching",
	})
	if err != nil {
		t.Fatalf("submit start_unit_seq failed: %v", err)
	}
	// Clear earlier queued abort/pump commands first so ClaimDispatch returns seqSub.
	for range abortResp.SubmittedCommandIDs {
		envPrev, claimErr := hAbort.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", time.Minute)
		if claimErr != nil {
			t.Fatalf("claim prior abort failed: %v", claimErr)
		}
		if err := hAbort.Authority.ResolveDispatch(context.Background(), envPrev, commandmodel.ConnectorResult{
			Phase: commandmodel.ConnectorExecutionRejected, FailureCode: "TEST_CLEAR",
		}); err != nil {
			t.Fatalf("clear prior abort failed: %v", err)
		}
	}
	envSeq, err := hAbort.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-2", time.Minute)
	if err != nil || envSeq.CommandID != seqSub.View.CommandID {
		t.Fatalf("expected claim of %s, got env=%+v err=%v", seqSub.View.CommandID, envSeq, err)
	}
	if err := hAbort.Authority.ResolveDispatch(context.Background(), envSeq, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "ev-ack-start-seq", EdgeExecution: validTestEdgeExecution("START_UNIT_SEQUENCE"),
	}); err != nil {
		t.Fatalf("ack start_unit_seq failed: %v", err)
	}
	cancelledSeq, err := hAbort.Authority.CancelOperatorCommand(context.Background(), op2, seqSub.View.CommandID, "operator aborted active sequence")
	if err != nil {
		t.Fatalf("CancelOperatorCommand on DISPATCHING START_UNIT_SEQUENCE failed: %v", err)
	}
	if cancelledSeq.Status != commandmodel.IntentFailed {
		t.Fatalf("expected aborted sequence status FAILED, got %s", cancelledSeq.Status)
	}
	listedAfterCancel, _ := hAbort.Store.List(context.Background(), caTenantID, caSiteID, MaximumListedCommands)
	if len(listedAfterCancel) == 0 || listedAfterCancel[0].PointID != abortSeqPt || !strings.HasPrefix(listedAfterCancel[0].IdempotencyKey, "shd-abort-") {
		t.Fatalf("expected best-effort ABORT_UNIT_SEQUENCE dispatch on cancel, got %+v", listedAfterCancel)
	}

	// 4. EMERGENCY_DO_RELEASE emits ControlFailureEvent with ActorType=PRINCIPAL.
	hEmerg, opEmerg := seedPlantHarness(t, now)
	opEmerg.StepUpTOTPVerified = true
	emergResp, err := hEmerg.Authority.ExecuteSiteControlShutdown(context.Background(), opEmerg, caSiteID, SiteControlShutdownInput{
		Mode: ShutdownModeEmergencyDoRelease, ConfirmEmergencyDoRelease: true, Reason: "emergency stop all DO channels", IdempotencyKey: "emerg-do-alarm",
	})
	if err != nil {
		t.Fatalf("EMERGENCY_DO_RELEASE failed: %v", err)
	}
	select {
	case evt := <-hEmerg.FailureEvents:
		if evt.Kind != "EMERGENCY_DO_RELEASE" || evt.ReasonCode != "EMERGENCY_DO_RELEASE" ||
			evt.CommandID != emergResp.SubmittedCommandIDs[0] || evt.ActorType != "PRINCIPAL" || evt.ActorID == "" ||
			evt.DeviceID != caEmergDev || evt.PointID != caEmergPt {
			t.Fatalf("unexpected EMERGENCY_DO_RELEASE ControlFailureEvent: %+v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for EMERGENCY_DO_RELEASE ControlFailureEvent")
	}
}

func TestControlAuthorityLastActiveChillerGuardScheduleVariants(t *testing.T) {
	setup := func(now time.Time, tz string, slots []OperatingScheduleSlotDTO) (*TestControlHarness, OperatorIdentity) {
		t.Helper()
		h, op := seedPlantHarness(t, now)
		op.StepUpTOTPVerified = true
		h.Twin.Schedules[caSiteID] = OperatingScheduleDTO{
			Timezone: tz,
			Slots:    slots,
		}
		h.Live.Set(caChillerDev, "run_state", healthyTextObs(now, "RUNNING", 40), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000040")
		return h, op
	}

	t.Run("pending_maintenance_keeps_protection_while_approved_holiday_lifts_it", func(t *testing.T) {
		now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
		h, op := setup(now, "UTC", []OperatingScheduleSlotDTO{{DayOfWeek: 1, StartMinute: 480, EndMinute: 1080}})
		h.Twin.Exceptions[caSiteID] = []OperatingExceptionDTO{{
			Kind: "MAINTENANCE_WINDOW", StartDate: "2026-11-16", EndDate: "2026-11-16", Status: "PENDING_APPROVAL",
		}}
		_, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
			AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "pending-maint-still-blocked",
		})
		var ce *ControlError
		if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_LAST_ACTIVE_CHILLER_PROTECTED" {
			t.Fatalf("expected 409 COMMAND_LAST_ACTIVE_CHILLER_PROTECTED for PENDING_APPROVAL exception, got %v", err)
		}

		h.Twin.Exceptions[caSiteID] = []OperatingExceptionDTO{{
			Kind: "HOLIDAY_SHUTDOWN", StartDate: "2026-11-16", EndDate: "2026-11-16", Status: "APPROVED",
		}}
		if _, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
			AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "approved-holiday-ok",
		}); err != nil {
			t.Fatalf("expected APPROVED HOLIDAY_SHUTDOWN to allow chiller STOP, got %v", err)
		}
	})

	t.Run("overtime_cooling_uses_replacement_slots_and_site_timezone", func(t *testing.T) {
		// 2026-11-16 01:00 UTC is 09:00 Asia/Shanghai (inside 08:30-12:00 replacement slot).
		inReplacement := time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)
		h, op := setup(inReplacement, "Asia/Shanghai", []OperatingScheduleSlotDTO{{DayOfWeek: 1, StartMinute: 480, EndMinute: 1080}})
		h.Twin.Exceptions[caSiteID] = []OperatingExceptionDTO{{
			Kind:             "OVERTIME_COOLING",
			StartDate:        "2026-11-16",
			EndDate:          "2026-11-16",
			Status:           "APPROVED",
			ReplacementSlots: []OperatingExceptionSlotDTO{{StartMinute: 510, EndMinute: 720}},
		}}
		_, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
			AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "overtime-inside-slot",
		})
		var ce *ControlError
		if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_LAST_ACTIVE_CHILLER_PROTECTED" {
			t.Fatalf("expected 409 COMMAND_LAST_ACTIVE_CHILLER_PROTECTED inside overtime replacement slot, got %v", err)
		}

		// 2026-11-16 06:00 UTC is 14:00 Asia/Shanghai (inside weekly Monday slot, but outside 08:30-12:00 overtime replacement slot).
		outsideReplacement := time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)
		hOut, opOut := setup(outsideReplacement, "Asia/Shanghai", []OperatingScheduleSlotDTO{{DayOfWeek: 1, StartMinute: 480, EndMinute: 1080}})
		hOut.Twin.Exceptions[caSiteID] = h.Twin.Exceptions[caSiteID]
		if _, err := hOut.Authority.SubmitOperatorCommand(context.Background(), opOut, SubmitOperatorCommandInput{
			AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "overtime-outside-slot-ok",
		}); err != nil {
			t.Fatalf("expected chiller STOP outside overtime replacement slot to succeed, got %v", err)
		}
	})

	t.Run("outside_scheduled_hours_allows_stop_and_invalid_timezone_fails_closed", func(t *testing.T) {
		outsideHours := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)
		hOff, opOff := setup(outsideHours, "UTC", []OperatingScheduleSlotDTO{{DayOfWeek: 1, StartMinute: 480, EndMinute: 1080}})
		if _, err := hOff.Authority.SubmitOperatorCommand(context.Background(), opOff, SubmitOperatorCommandInput{
			AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "outside-hours-ok",
		}); err != nil {
			t.Fatalf("expected chiller STOP outside scheduled hours to succeed, got %v", err)
		}

		for _, tc := range []struct {
			name string
			tz   string
		}{
			{name: "empty", tz: ""},
			{name: "invalid", tz: "Invalid/Zone"},
		} {
			now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
			hBad, opBad := setup(now, tc.tz, []OperatingScheduleSlotDTO{{DayOfWeek: 1, StartMinute: 480, EndMinute: 1080}})
			_, err := hBad.Authority.SubmitOperatorCommand(context.Background(), opBad, SubmitOperatorCommandInput{
				AssetID: caChillerID, CommandPointID: caStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "bad-tz-" + tc.name,
			})
			var ce *ControlError
			if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusServiceUnavailable || ce.Code != "COMMAND_UNAVAILABLE" {
				t.Fatalf("timezone %q: expected fail-closed 503 COMMAND_UNAVAILABLE, got %v", tc.tz, err)
			}
		}
	})
}

func TestControlAuthorityServesTopologyUsesExplicitEdgesNotInterlockChecks(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)
	op.StepUpTOTPVerified = true

	// Remove the explicit ASSET->ASSET SERVES edge while keeping interlockChecks on caPumpStopPt.
	model := h.Twin.Models[caSiteID]
	filtered := make([]AssetRelationshipDTO, 0, len(model.Relationships))
	for _, rel := range model.Relationships {
		if rel.FromType == "ASSET" && rel.ToType == "ASSET" && strings.EqualFold(rel.Role, "SERVES") {
			continue
		}
		filtered = append(filtered, rel)
	}
	model.Relationships = filtered
	h.Twin.Models[caSiteID] = model

	topo := resolveSiteControlTopology(model, now)
	if len(topo.servedChillersByAux[caPumpID]) != 0 {
		t.Fatalf("expected empty servedChillersByAux without explicit SERVES edge, got %v", topo.servedChillersByAux[caPumpID])
	}

	// Point-level interlockChecks still blocks pump STOP while chiller is RUNNING.
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(now, "RUNNING", 20), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000050")
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(now, "RUNNING", 21), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000051")
	_, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "interlock-still-blocks",
	})
	var ce *ControlError
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "COMMAND_INTERLOCK_BLOCKED" {
		t.Fatalf("expected point-level interlockChecks to still return 409 COMMAND_INTERLOCK_BLOCKED, got %v", err)
	}

	// Without explicit SERVES edge, chiller in-flight START no longer infers pump->chiller SERVES for startup blind window.
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(now, "STOPPED", 22), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000052")
	seedCommandHistoryEntry(t, h, op, caChillerDev, caStartPt, commandmodel.CapabilityStart, commandmodel.IntentQueued, now, "seed-chiller-queued-no-serves")
	if _, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "pump-stop-no-serves-edge",
	}); err != nil {
		t.Fatalf("expected pump STOP without explicit SERVES edge not to infer startup blind window from interlockChecks, got %v", err)
	}

	// Multi-hop tower -> cwp -> chiller -> chw-loop and shared header pump -> chw-loop resolve via planttwin.NewHydraulicGraph.
	ts := now.Format(time.RFC3339)
	cwpID := "018f3e00-2000-7000-8000-000000000010"
	towerID := "018f3e00-2000-7000-8000-000000000011"
	hdrPumpID := "018f3e00-2000-7000-8000-000000000012"
	chwLoopID := "018f3e00-2000-7000-8000-000000000013"
	chillerBID := "018f3e00-2000-7000-8000-000000000014"
	chwpBID := "018f3e00-2000-7000-8000-000000000015"
	graphModel := SiteAssetModelDTO{
		SchemaVersion: 2,
		TenantID:      caTenantID,
		SiteID:        caSiteID,
		Assets: []AssetDTO{
			{ID: caChillerID, TenantID: caTenantID, SiteID: caSiteID, Code: "chiller-a", AssetType: "CHILLER", Status: "ACTIVE"},
			{ID: chillerBID, TenantID: caTenantID, SiteID: caSiteID, Code: "chiller-b", AssetType: "CHILLER", Status: "ACTIVE"},
			{ID: caPumpID, TenantID: caTenantID, SiteID: caSiteID, Code: "chwp-1", AssetType: "CHILLED_WATER_PUMP", Status: "ACTIVE"},
			{ID: chwpBID, TenantID: caTenantID, SiteID: caSiteID, Code: "chwp-2", AssetType: "CHILLED_WATER_PUMP", Status: "ACTIVE"},
			{ID: cwpID, TenantID: caTenantID, SiteID: caSiteID, Code: "cwp-1", AssetType: "COOLING_WATER_PUMP", Status: "ACTIVE"},
			{ID: towerID, TenantID: caTenantID, SiteID: caSiteID, Code: "ct-1", AssetType: "COOLING_TOWER", Status: "ACTIVE"},
			{ID: hdrPumpID, TenantID: caTenantID, SiteID: caSiteID, Code: "chwp-hdr", AssetType: "CHILLED_WATER_PUMP", Status: "ACTIVE"},
			{ID: chwLoopID, TenantID: caTenantID, SiteID: caSiteID, Code: "chw-loop", AssetType: "CHILLED_WATER_LOOP", Status: "ACTIVE"},
		},
		Relationships: []AssetRelationshipDTO{
			{ID: "g1", TenantID: caTenantID, SiteID: caSiteID, FromType: "ASSET", FromID: caPumpID, ToType: "ASSET", ToID: caChillerID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
			{ID: "g2", TenantID: caTenantID, SiteID: caSiteID, FromType: "ASSET", FromID: chwpBID, ToType: "ASSET", ToID: chillerBID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
			{ID: "g3", TenantID: caTenantID, SiteID: caSiteID, FromType: "ASSET", FromID: towerID, ToType: "ASSET", ToID: cwpID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
			{ID: "g4", TenantID: caTenantID, SiteID: caSiteID, FromType: "ASSET", FromID: cwpID, ToType: "ASSET", ToID: caChillerID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
			{ID: "g5", TenantID: caTenantID, SiteID: caSiteID, FromType: "ASSET", FromID: caChillerID, ToType: "ASSET", ToID: chwLoopID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
			{ID: "g6", TenantID: caTenantID, SiteID: caSiteID, FromType: "ASSET", FromID: chillerBID, ToType: "ASSET", ToID: chwLoopID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
			{ID: "g7", TenantID: caTenantID, SiteID: caSiteID, FromType: "ASSET", FromID: hdrPumpID, ToType: "ASSET", ToID: chwLoopID, Role: "SERVES", Status: "ACTIVE", ValidFrom: ts},
		},
	}
	graphTopo := resolveSiteControlTopology(graphModel, now)
	if !slices.Equal(graphTopo.servedChillersByAux[caPumpID], []string{caChillerID}) {
		t.Fatalf("chwp-1 served=%v, want [%s]", graphTopo.servedChillersByAux[caPumpID], caChillerID)
	}
	if !slices.Equal(graphTopo.servedChillersByAux[towerID], []string{caChillerID}) {
		t.Fatalf("multi-hop ct-1 served=%v, want [%s]", graphTopo.servedChillersByAux[towerID], caChillerID)
	}
	if !slices.Equal(graphTopo.servedChillersByAux[hdrPumpID], []string{caChillerID, chillerBID}) {
		t.Fatalf("header pump served=%v, want [%s %s]", graphTopo.servedChillersByAux[hdrPumpID], caChillerID, chillerBID)
	}
}

func TestControlAuthorityChillerPostStopPumpCooldown(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	clk := now
	h, err := NewControlAuthorityForTest(func() time.Time { return clk })
	if err != nil {
		t.Fatal(err)
	}
	baseH, op := seedPlantHarness(t, now)
	h.IAM.AllowedSites = baseH.IAM.AllowedSites
	h.Twin.BoundDevices = baseH.Twin.BoundDevices
	h.Twin.Assets = baseH.Twin.Assets
	h.Twin.Models = baseH.Twin.Models
	op.StepUpTOTPVerified = true

	// Chiller is STOPPED and served chilled-water pump is RUNNING.
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "STOPPED", 31), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000061")
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(clk, "RUNNING", 32), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000062")

	// 1. In-flight STOP command on served chiller blocks stopping the pump.
	inflightStopID := seedCommandHistoryEntry(t, h, op, caChillerDev, caStopPt, commandmodel.CapabilityStop, commandmodel.IntentQueued, clk, "seed-chiller-queued-stop")
	_, err = h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "pump-stop-inflight-chiller-stop",
	})
	var ce *ControlError
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "CHILLER_POST_STOP_COOLDOWN_ACTIVE" {
		t.Fatalf("expected 409 CHILLER_POST_STOP_COOLDOWN_ACTIVE (in-flight chiller STOP), got %v", err)
	}
	if _, cancelErr := h.Authority.CancelOperatorCommand(context.Background(), op, inflightStopID, "clear queued stop"); cancelErr != nil {
		t.Fatalf("cancel queued stop: %v", cancelErr)
	}

	// 2. Succeeded chiller STOP 2m ago blocks stopping served pump inside 5-minute cooldown.
	seedCommandHistoryEntry(t, h, op, caChillerDev, caStopPt, commandmodel.CapabilityStop, commandmodel.IntentSucceeded, clk.Add(-2*time.Minute), "seed-chiller-recent-stop")
	_, err = h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "pump-stop-cooldown-block",
	})
	ce = nil
	if !errors.As(err, &ce) || ce.HTTPStatus != http.StatusConflict || ce.Code != "CHILLER_POST_STOP_COOLDOWN_ACTIVE" || !strings.Contains(ce.Detail, "5-minute post-stop cool-down") {
		t.Fatalf("expected 409 CHILLER_POST_STOP_COOLDOWN_ACTIVE (5-minute cooldown), got %v", err)
	}

	// 3. Non-pump auxiliary (COOLING_TOWER) or detached pump is not blocked by pump post-stop cooldown.
	if err := checkChillerPostStopPumpCooldown(
		shutdownTopology{},
		AssetDTO{ID: "tower-1", Code: "CT-1", AssetType: "COOLING_TOWER"},
		[]string{caChillerID},
		[]commandmodel.CommandIntent{{DeviceID: caChillerDev, Capability: commandmodel.CapabilityStop, Status: commandmodel.IntentSucceeded, UpdatedAt: clk.Add(-1 * time.Minute)}},
		clk,
	); err != nil {
		t.Fatalf("expected non-pump cooling tower not to be blocked by pump post-stop cooldown, got %v", err)
	}

	// 4. Advancing past 5 minutes unlocks pump STOP cleanly.
	clk = clk.Add(4 * time.Minute)
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(clk, "STOPPED", 33), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000063")
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(clk, "RUNNING", 34), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000064")
	op.IssuedAt = clk
	op.ExpiresAt = clk.Add(5 * time.Minute)
	if _, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID: caPumpID, CommandPointID: caPumpStopPt, Parameters: commandmodel.CommandParameters{}, IdempotencyKey: "pump-stop-after-cooldown",
	}); err != nil {
		t.Fatalf("expected pump STOP to succeed after 5-minute chiller post-stop cooldown, got %v", err)
	}
}
