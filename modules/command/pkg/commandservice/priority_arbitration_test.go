package commandservice

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestPriorityLockBlocksLowerPriorityAndAllowsEqualOrHigher(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	svc := New(func() time.Time { return now })

	lock, err := svc.AcquireDevicePriorityLock(commandmodel.DevicePriorityLock{
		TenantID:       "tenant-1",
		SiteID:         "site-1",
		DeviceID:       "device-1",
		LockedPriority: commandmodel.PriorityManualOverride,
		CommandSource:  commandmodel.SourceOperator,
		LockedBy:       "operator-1",
		LockedUntil:    now.Add(15 * time.Minute),
		Reason:         "manual plant balancing",
	})
	if err != nil {
		t.Fatalf("acquire manual override lock failed: %v", err)
	}
	if lock.LockedPriority != commandmodel.PriorityManualOverride || lock.CommandSource != commandmodel.SourceOperator {
		t.Fatalf("unexpected lock stored: %+v", lock)
	}

	gotLock, err := svc.GetDevicePriorityLock("tenant-1", "device-1")
	if err != nil || gotLock == nil || gotLock.LockedBy != "operator-1" {
		t.Fatalf("GetDevicePriorityLock = %+v err=%v", gotLock, err)
	}

	// Lower-priority lock acquisition cannot downgrade an active higher-priority lock.
	if _, err := svc.AcquireDevicePriorityLock(commandmodel.DevicePriorityLock{
		TenantID:       "tenant-1",
		SiteID:         "site-1",
		DeviceID:       "device-1",
		LockedPriority: commandmodel.PriorityAIOptimization,
		CommandSource:  commandmodel.SourceOptimization,
		LockedBy:       "optimizer-1",
		LockedUntil:    now.Add(20 * time.Minute),
		Reason:         "attempt downgrade",
	}); !errors.Is(err, ErrDevicePriorityLocked) {
		t.Fatalf("expected ErrDevicePriorityLocked on lock downgrade, got %v", err)
	}

	// Automated AI optimization and schedule submits fail closed while MANUAL_OVERRIDE lock holds.
	aiReq := validRequest()
	aiReq.IdempotencyKey = "prio-ai-blocked"
	aiReq.Authorization.WorkloadSPIFFE = "spiffe://hvac.local/optimizer"
	aiReq.Authorization.LeaseID = "018f3e00-0000-7000-8000-000000000101"
	aiReq.Authorization.ArmedBy = "operator-1"
	if _, err := svc.Submit(aiReq); !errors.Is(err, ErrDevicePriorityLocked) {
		t.Fatalf("expected AI_OPTIMIZATION blocked by MANUAL_OVERRIDE lock, got %v", err)
	}

	schedReq := validRequest()
	schedReq.IdempotencyKey = "prio-sched-blocked"
	schedReq.PriorityTier = commandmodel.PrioritySchedule
	schedReq.CommandSource = commandmodel.SourceSchedule
	if _, err := svc.Submit(schedReq); !errors.Is(err, ErrDevicePriorityLocked) {
		t.Fatalf("expected SCHEDULE blocked by MANUAL_OVERRIDE lock, got %v", err)
	}

	// Equal priority (MANUAL_OVERRIDE) is allowed under MANUAL_OVERRIDE lock.
	manualReq := validRequest()
	manualReq.IdempotencyKey = "prio-manual-ok"
	manualSub, err := svc.Submit(manualReq)
	if err != nil {
		t.Fatalf("expected MANUAL_OVERRIDE allowed under MANUAL_OVERRIDE lock, got %v", err)
	}
	if manualSub.Intent.PriorityTier != commandmodel.PriorityManualOverride || manualSub.Intent.CommandSource != commandmodel.SourceOperator {
		t.Fatalf("unexpected priority projection: (%s, %s)", manualSub.Intent.PriorityTier, manualSub.Intent.CommandSource)
	}
	if _, err := svc.PrepareDispatch(manualSub.Intent.ID, "disp-1", now.Add(time.Minute)); err != nil {
		t.Fatalf("expected equal-priority dispatch allowed: %v", err)
	}

	// Upgrading lock to SAFETY_INTERLOCK succeeds and blocks new MANUAL_OVERRIDE on another device test or same device.
	if _, err := svc.AcquireDevicePriorityLock(commandmodel.DevicePriorityLock{
		TenantID:       "tenant-1",
		SiteID:         "site-1",
		DeviceID:       "device-1",
		LockedPriority: commandmodel.PrioritySafetyInterlock,
		CommandSource:  commandmodel.SourceSafetyInterlock,
		LockedBy:       "safety-interlock-1",
		LockedUntil:    now.Add(10 * time.Minute),
		Reason:         "low evaporator pressure trip",
	}); err != nil {
		t.Fatalf("expected SAFETY_INTERLOCK lock upgrade to succeed: %v", err)
	}

	blockedManual := validRequest()
	blockedManual.IdempotencyKey = "prio-manual-blocked"
	if _, err := svc.Submit(blockedManual); !errors.Is(err, ErrDevicePriorityLocked) {
		t.Fatalf("expected MANUAL_OVERRIDE blocked under SAFETY_INTERLOCK lock, got %v", err)
	}

	// Advancing past expiry removes blocking automatically.
	now = now.Add(16 * time.Minute)
	expiredLock, err := svc.GetDevicePriorityLock("tenant-1", "device-1")
	if err != nil || expiredLock != nil {
		t.Fatalf("expected expired lock nil, got %+v err=%v", expiredLock, err)
	}
}

func TestActiveHigherPriorityBlocksLowerSubmitAndOvertakesQueuedLowerPriority(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	store := NewMemoryAuthorityStore(func() time.Time { return now })
	ctx := context.Background()

	// 1. Queued AI_OPTIMIZATION followed by MANUAL_OVERRIDE: ClaimDispatch picks MANUAL_OVERRIDE first.
	aiReq := validRequest()
	aiReq.IdempotencyKey = "prio-order-ai"
	aiReq.Authorization.WorkloadSPIFFE = "spiffe://hvac.local/optimizer"
	aiReq.Authorization.LeaseID = "018f3e00-0000-7000-8000-000000000102"
	aiReq.Authorization.ArmedBy = "operator-1"
	aiSub, err := store.Submit(ctx, aiReq)
	if err != nil {
		t.Fatalf("submit AI_OPTIMIZATION failed: %v", err)
	}

	manualReq := validRequest()
	manualReq.IdempotencyKey = "prio-order-manual"
	manualSub, err := store.Submit(ctx, manualReq)
	if err != nil {
		t.Fatalf("submit MANUAL_OVERRIDE while AI queued failed: %v", err)
	}

	firstEnv, err := store.ClaimDispatch(ctx, "tenant-1", "disp-1", time.Minute)
	if err != nil {
		t.Fatalf("first ClaimDispatch failed: %v", err)
	}
	if firstEnv.CommandID != manualSub.Intent.ID {
		t.Fatalf("ClaimDispatch claimed %s (AI=%s), want higher-priority MANUAL=%s", firstEnv.CommandID, aiSub.Intent.ID, manualSub.Intent.ID)
	}

	// While MANUAL_OVERRIDE is DISPATCHING, a new lower-priority submit fails closed and AI cannot claim.
	aiBlocked := aiReq
	aiBlocked.IdempotencyKey = "prio-blocked-while-manual-dispatching"
	if _, err := store.Submit(ctx, aiBlocked); !errors.Is(err, ErrDevicePriorityLocked) {
		t.Fatalf("expected lower-priority submit blocked while higher-priority DISPATCHING, got %v", err)
	}
	if _, err := store.ClaimDispatch(ctx, "tenant-1", "disp-1", time.Minute); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("expected ErrNoDispatchAvailable while higher-priority DISPATCHING, got %v", err)
	}

	// Complete MANUAL_OVERRIDE cleanly through acknowledge and verification.
	ackVal := commandmodel.NumberScalar(24.0)
	if err := store.ResolveDispatch(ctx, firstEnv, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, Acknowledged: true, EvidenceID: "ack-manual",
		EdgeExecution: &commandmodel.EdgeExecutionEvidence{
			Requested: ackVal, Effective: &ackVal, Applied: &ackVal, WinnerControllerID: "edge", Cycle: 1,
		},
	}); err != nil {
		t.Fatalf("ack manual dispatch failed: %v", err)
	}
	verEnv, err := store.ClaimVerification(ctx, "tenant-1", "verifier-1", 30*time.Second)
	if err != nil {
		t.Fatalf("claim verification failed: %v", err)
	}
	if err := store.ResolveVerification(ctx, verEnv, commandmodel.VerificationResult{
		Outcome:    commandmodel.VerificationSucceeded,
		EvidenceID: "verify-manual",
		Reported: commandmodel.ReportedStateEvidence{
			TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
			BusinessRevision: 18, ObservedAt: now.Add(5 * time.Second), ReportedValue: ackVal,
		},
	}); err != nil {
		t.Fatalf("resolve verification failed: %v", err)
	}

	// Now AI_OPTIMIZATION is unblocked and claimed cleanly.
	secondEnv, err := store.ClaimDispatch(ctx, "tenant-1", "disp-1", time.Minute)
	if err != nil {
		t.Fatalf("expected queued AI_OPTIMIZATION to claim once MANUAL completed: %v", err)
	}
	if secondEnv.CommandID != aiSub.Intent.ID {
		t.Fatalf("claimed %s, want %s", secondEnv.CommandID, aiSub.Intent.ID)
	}
}

func TestPriorityLockRetiresAlreadyQueuedLowerPriorityAtClaimDispatch(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	store := NewMemoryAuthorityStore(func() time.Time { return now })
	ctx := context.Background()

	aiReq := validRequest()
	aiReq.IdempotencyKey = "prio-retire-ai"
	aiReq.Authorization.WorkloadSPIFFE = "spiffe://hvac.local/optimizer"
	aiReq.Authorization.LeaseID = "018f3e00-0000-7000-8000-000000000103"
	aiReq.Authorization.ArmedBy = "operator-1"
	aiSub, err := store.Submit(ctx, aiReq)
	if err != nil {
		t.Fatalf("submit AI_OPTIMIZATION failed: %v", err)
	}

	if _, err := store.AcquireDevicePriorityLock(ctx, commandmodel.DevicePriorityLock{
		TenantID:       "tenant-1",
		SiteID:         "site-1",
		DeviceID:       "device-1",
		LockedPriority: commandmodel.PriorityManualOverride,
		CommandSource:  commandmodel.SourceOperator,
		LockedBy:       "operator-1",
		LockedUntil:    now.Add(15 * time.Minute),
		Reason:         "operator locked device after AI queued",
	}); err != nil {
		t.Fatalf("AcquireDevicePriorityLock failed: %v", err)
	}

	if _, err := store.ClaimDispatch(ctx, "tenant-1", "disp-1", time.Minute); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("expected ErrNoDispatchAvailable when lock blocks queued AI command, got %v", err)
	}
	after, err := store.Get(ctx, "tenant-1", aiSub.Intent.ID)
	if err != nil {
		t.Fatalf("Get retired AI command failed: %v", err)
	}
	if after.Status != commandmodel.IntentRejected {
		t.Fatalf("expected queued AI command status REJECTED, got %s", after.Status)
	}
	last := after.Transitions[len(after.Transitions)-1]
	if last.Reason != "DEVICE_PRIORITY_LOCKED" {
		t.Fatalf("expected DEVICE_PRIORITY_LOCKED transition reason, got %s", last.Reason)
	}
}

func TestSafetyInterlockPreemptsLowerPriorityWithoutOutcomeUnknownFreeze(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	svc := New(func() time.Time { return now })

	// 1. Submit QUEUED SCHEDULE command on device-1.
	schedReq := validRequest()
	schedReq.IdempotencyKey = "preempt-sched"
	schedReq.PriorityTier = commandmodel.PrioritySchedule
	schedReq.CommandSource = commandmodel.SourceSchedule
	schedSub, err := svc.Submit(schedReq)
	if err != nil {
		t.Fatalf("submit schedule failed: %v", err)
	}

	// 2. Submit AWAITING_APPROVAL MANUAL_OVERRIDE medium-risk command on device-1.
	medReq := validRequest()
	medReq.IdempotencyKey = "preempt-med-approval"
	medReq.Capability = commandmodel.CapabilitySetChilledWaterTemperatureSetpoint
	medReq.Parameters = commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.5}
	medReq.CurrentState.CurrentValue = testFloat64Pointer(7.0)
	medReq.Authorization.Capability = commandmodel.CapabilitySetChilledWaterTemperatureSetpoint
	medReq.Authorization.CapabilityRevision = "capability:set-chilled-water-temperature-setpoint:v1"
	medReq.Authorization.MaximumRisk = commandmodel.RiskMedium
	medSub, err := svc.Submit(medReq)
	if err != nil {
		t.Fatalf("submit awaiting-approval command failed: %v", err)
	}
	if medSub.Intent.Status != commandmodel.IntentAwaitingApproval {
		t.Fatalf("expected AWAITING_APPROVAL, got %s", medSub.Intent.Status)
	}

	// 3. Submit SAFETY_INTERLOCK emergency stop -> preempts both cleanly to CANCELLED (PRIORITY_PREEMPTED).
	stopProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStop)
	safetyReq := validRequest()
	safetyReq.IdempotencyKey = "preempt-safety-stop"
	safetyReq.Capability = commandmodel.CapabilityStop
	safetyReq.Parameters = commandmodel.CommandParameters{}
	safetyReq.VerificationPointKey = "run_state"
	safetyReq.PreconditionMode = commandmodel.PreconditionEmergencyBypass
	safetyReq.BypassReason = "emergency chill water low temp trip"
	safetyReq.CurrentState = commandmodel.CurrentStateEvidence{}
	safetyReq.Authorization.Capability = commandmodel.CapabilityStop
	safetyReq.Authorization.CapabilityRevision = stopProfile.Revision
	safetyReq.Authorization.MaximumRisk = commandmodel.RiskCritical
	safetySub, err := svc.Submit(safetyReq)
	if err != nil {
		t.Fatalf("submit safety interlock failed: %v", err)
	}
	if safetySub.Intent.PriorityTier != commandmodel.PrioritySafetyInterlock || safetySub.Intent.CommandSource != commandmodel.SourceSafetyInterlock {
		t.Fatalf("unexpected safety priority projection: (%s, %s)", safetySub.Intent.PriorityTier, safetySub.Intent.CommandSource)
	}

	for _, id := range []string{schedSub.Intent.ID, medSub.Intent.ID} {
		reloaded, err := svc.Get(id)
		if err != nil {
			t.Fatalf("Get(%s) failed: %v", id, err)
		}
		if reloaded.Status != commandmodel.IntentCancelled {
			t.Fatalf("command %s expected CANCELLED, got %s", id, reloaded.Status)
		}
		last := reloaded.Transitions[len(reloaded.Transitions)-1]
		if last.Reason != "PRIORITY_PREEMPTED" {
			t.Fatalf("command %s expected reason PRIORITY_PREEMPTED, got %s", id, last.Reason)
		}
	}

	if _, err := svc.PrepareDispatch(safetySub.Intent.ID, "disp-safety", now.Add(time.Minute)); err != nil {
		t.Fatalf("expected safety interlock to dispatch immediately after preemption, got %v", err)
	}
}

func TestHTTPCreateCommandPriorityAndLockMapping(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	iamSigner, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	gatewaySigner, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryAuthorityStore(func() time.Time { return now })
	handler := newCommandHTTPTestHandler(t, store, iamSigner, gatewaySigner, now)
	grant1 := signCommandTestGrant(t, iamSigner, now, commandmodel.AuthorizationCommandSubmit, "principal-1", "device-1")
	grant2 := signCommandTestGrant(t, iamSigner, now, commandmodel.AuthorizationCommandSubmit, "principal-1", "device-2")

	postCommand := func(grant string, payload map[string]any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, InternalCommandsPath, bytes.NewReader(raw))
		req.Header.Set(commandGrantHeader, grant)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	baseState := map[string]any{
		"evaluationAvailability": "AVAILABLE",
		"presence":               "ONLINE",
		"readiness":              "CURRENT",
		"quality":                "GOOD",
		"businessRevision":       17,
		"currentValue":           23.0,
		"observedAt":             now.Format(time.RFC3339),
	}

	// 1. Valid explicit SCHEDULE priorityTier/commandSource projected on CommandView.
	okRec := postCommand(grant1, map[string]any{
		"tenantId":             "tenant-1",
		"siteId":               "site-1",
		"deviceId":             "device-1",
		"pointId":              "point-1",
		"principalId":          "principal-1",
		"idempotencyKey":       "http-prio-sched",
		"capability":           commandmodel.CapabilitySetTemperatureSetpoint,
		"parameters":           map[string]any{"setpointC": 23.5},
		"verificationPointKey": "zone.temperature_setpoint",
		"priorityTier":         "SCHEDULE",
		"commandSource":        "SCHEDULE",
		"currentState":         baseState,
	})
	if okRec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d body=%s", okRec.Code, okRec.Body.String())
	}
	var view CommandView
	if err := json.Unmarshal(okRec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode CommandView failed: %v", err)
	}
	if view.PriorityTier != commandmodel.PrioritySchedule || view.CommandSource != commandmodel.SourceSchedule {
		t.Fatalf("CommandView priority mismatch: tier=%s source=%s", view.PriorityTier, view.CommandSource)
	}

	// 2. Incompatible priorityTier/commandSource fails closed with 400 Bad Request.
	badRec := postCommand(grant2, map[string]any{
		"tenantId":             "tenant-1",
		"siteId":               "site-1",
		"deviceId":             "device-2",
		"pointId":              "point-2",
		"principalId":          "principal-1",
		"idempotencyKey":       "http-prio-mismatch",
		"capability":           commandmodel.CapabilitySetTemperatureSetpoint,
		"parameters":           map[string]any{"setpointC": 23.5},
		"verificationPointKey": "zone.temperature_setpoint",
		"priorityTier":         "MANUAL_OVERRIDE",
		"commandSource":        "OPTIMIZATION",
		"currentState":         baseState,
	})
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on incompatible priority/source, got %d body=%s", badRec.Code, badRec.Body.String())
	}

	// 3. Higher-priority device lock maps ErrDevicePriorityLocked to 409 Conflict DEVICE_PRIORITY_LOCKED.
	if _, err := store.AcquireDevicePriorityLock(context.Background(), commandmodel.DevicePriorityLock{
		TenantID:       "tenant-1",
		SiteID:         "site-1",
		DeviceID:       "device-1",
		LockedPriority: commandmodel.PriorityManualOverride,
		CommandSource:  commandmodel.SourceOperator,
		LockedBy:       "operator-1",
		LockedUntil:    now.Add(10 * time.Minute),
		Reason:         "manual lock",
	}); err != nil {
		t.Fatal(err)
	}
	lockedRec := postCommand(grant1, map[string]any{
		"tenantId":             "tenant-1",
		"siteId":               "site-1",
		"deviceId":             "device-1",
		"pointId":              "point-1",
		"principalId":          "principal-1",
		"idempotencyKey":       "http-prio-locked",
		"capability":           commandmodel.CapabilitySetTemperatureSetpoint,
		"parameters":           map[string]any{"setpointC": 23.5},
		"verificationPointKey": "zone.temperature_setpoint",
		"priorityTier":         "SCHEDULE",
		"commandSource":        "SCHEDULE",
		"currentState":         baseState,
	})
	if lockedRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict under priority lock, got %d body=%s", lockedRec.Code, lockedRec.Body.String())
	}
	var problem commandProblem
	if err := json.Unmarshal(lockedRec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "DEVICE_PRIORITY_LOCKED" {
		t.Fatalf("expected problem code DEVICE_PRIORITY_LOCKED, got %+v", problem)
	}
}

func TestControlAuthorityEmergencyDoReleaseTagsSafetyInterlockPriority(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, op := seedPlantHarness(t, now)
	op.StepUpTOTPVerified = true

	resp, err := h.Authority.ExecuteSiteControlShutdown(context.Background(), op, caSiteID, SiteControlShutdownInput{
		Mode:                      ShutdownModeEmergencyDoRelease,
		ConfirmEmergencyDoRelease: true,
		Reason:                    "safety emergency DO release",
		IdempotencyKey:            "sd-emerg-prio-1",
	})
	if err != nil {
		t.Fatalf("ExecuteSiteControlShutdown failed: %v", err)
	}
	if len(resp.SubmittedCommandIDs) != 1 {
		t.Fatalf("expected 1 submitted emergency DO release command, got %+v", resp.SubmittedCommandIDs)
	}
	intent, err := h.Store.Get(context.Background(), caTenantID, resp.SubmittedCommandIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if intent.PriorityTier != commandmodel.PrioritySafetyInterlock || intent.CommandSource != commandmodel.SourceSafetyInterlock {
		t.Fatalf("expected (SAFETY_INTERLOCK, SAFETY_INTERLOCK), got (%s, %s)", intent.PriorityTier, intent.CommandSource)
	}
}

func completeTestDispatchForAuthority(
	t *testing.T,
	auth *ControlAuthority,
	tenantID, siteID, deviceID string,
	env commandmodel.DispatchEnvelope,
	reported commandmodel.ScalarValue,
	at time.Time,
	rev uint64,
) {
	t.Helper()
	ctx := context.Background()
	if err := auth.ResolveDispatch(ctx, env, commandmodel.ConnectorResult{
		Phase:        commandmodel.ConnectorAcknowledged,
		Acknowledged: true,
		EvidenceID:   "ack-" + env.CommandID,
		EdgeExecution: &commandmodel.EdgeExecutionEvidence{
			Requested:          reported,
			Effective:          &reported,
			Applied:            &reported,
			WinnerControllerID: "edge",
			Cycle:              1,
		},
	}); err != nil {
		t.Fatalf("ResolveDispatch(%s) failed: %v", env.CommandID, err)
	}
	verEnv, err := auth.ClaimVerification(ctx, tenantID, "verifier-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimVerification(%s) failed: %v", env.CommandID, err)
	}
	if err := auth.ResolveVerification(ctx, verEnv, commandmodel.VerificationResult{
		Outcome:    commandmodel.VerificationSucceeded,
		EvidenceID: "verify-" + env.CommandID,
		Reported: commandmodel.ReportedStateEvidence{
			TenantID: tenantID, SiteID: siteID, DeviceID: deviceID,
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
			BusinessRevision: rev, ObservedAt: at, ReportedValue: reported,
		},
	}); err != nil {
		t.Fatalf("ResolveVerification(%s) failed: %v", env.CommandID, err)
	}
}

func testAuthSnapshot(
	now time.Time,
	principalID, siteID, deviceID string,
	capability commandmodel.Capability,
	maxRisk commandmodel.RiskLevel,
) commandmodel.AuthorizationSnapshot {
	profile, _ := commandmodel.CapabilityProfileFor(capability)
	return commandmodel.AuthorizationSnapshot{
		GrantID:                     "grant-test",
		PolicyRevision:              "command-policy-1",
		Purpose:                     commandmodel.AuthorizationCommandSubmit,
		PrincipalID:                 principalID,
		TenantID:                    caTenantID,
		SiteID:                      siteID,
		DeviceID:                    deviceID,
		Capability:                  capability,
		MaximumRisk:                 maxRisk,
		CapabilityRevision:          profile.Revision,
		EmergencyRevocationRevision: 1,
		IssuedAt:                    now.Add(-5 * time.Second),
		ExpiresAt:                   now.Add(25 * time.Second),
	}
}

func TestSubmitOperatorCommandInstallsTwoHourManualCooldownLock(t *testing.T) {
	currentNow := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	ref, op := seedPlantHarness(t, currentNow)
	h, err := NewControlAuthorityForTest(func() time.Time { return currentNow })
	if err != nil {
		t.Fatal(err)
	}
	h.IAM.AllowedSites = ref.IAM.AllowedSites
	h.Twin.Assets = ref.Twin.Assets
	h.Twin.BoundDevices = ref.Twin.BoundDevices
	h.Twin.Models = ref.Twin.Models
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(currentNow, "STOPPED", 11), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000001")
	h.Live.Set(caPumpDev, "run_state", healthyTextObs(currentNow, "RUNNING", 12), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000002")
	h.Live.Set(caChillerDev, "zone_temperature", healthyNumObs(currentNow, 23.0, 17), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000003")

	op.StepUpTOTPVerified = true
	submitted, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID:        caChillerID,
		CommandPointID: caStartPt,
		IdempotencyKey: "op-manual-start-1",
	})
	if err != nil {
		t.Fatalf("SubmitOperatorCommand failed: %v", err)
	}
	if submitted.View.Status == "" {
		t.Fatalf("expected valid submitted view")
	}

	lock, err := h.Store.GetDevicePriorityLock(context.Background(), caTenantID, caChillerDev)
	if err != nil {
		t.Fatalf("GetDevicePriorityLock failed: %v", err)
	}
	wantUntil := currentNow.Add(2 * time.Hour)
	if lock == nil || lock.LockedPriority != commandmodel.PriorityManualOverride ||
		lock.CommandSource != commandmodel.SourceOperator ||
		lock.LockedBy != op.Subject ||
		lock.Reason != "operator manual command cooldown" ||
		!lock.LockedUntil.Equal(wantUntil) {
		t.Fatalf("unexpected manual cooldown lock: %+v (want LockedUntil=%s)", lock, wantUntil)
	}

	// Replaying the same idempotency key returns Replayed=true without failing.
	replayed, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID:        caChillerID,
		CommandPointID: caStartPt,
		IdempotencyKey: "op-manual-start-1",
	})
	if err != nil || !replayed.Replayed {
		t.Fatalf("expected idempotent replay, got replayed=%+v err=%v", replayed, err)
	}

	// Complete operator command cleanly so active command alone wouldn't block; cooldown lock must still block AI until 2h elapses.
	env, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimDispatch operator command failed: %v", err)
	}
	completeTestDispatchForAuthority(t, h.Authority, caTenantID, caSiteID, caChillerDev, env, commandmodel.TextScalar("RUNNING"), currentNow.Add(time.Second), 12)

	currentNow = currentNow.Add(119 * time.Minute)
	aiBlocked := commandmodel.SubmitRequest{
		TenantID:             caTenantID,
		SiteID:               caSiteID,
		DeviceID:             caChillerDev,
		PointID:              caSetpointPt,
		PrincipalID:          "optimizer-1",
		IdempotencyKey:       "ai-cooldown-blocked",
		Capability:           commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24.0},
		VerificationPointKey: "zone_temperature",
		PriorityTier:         commandmodel.PriorityAIOptimization,
		CommandSource:        commandmodel.SourceOptimization,
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE",
			Presence:               "ONLINE",
			Readiness:              "CURRENT",
			Quality:                "GOOD",
			BusinessRevision:       17,
			CurrentValue:           testFloat64Pointer(23.0),
			ObservedAt:             currentNow,
		},
		Authorization: testAuthSnapshot(currentNow, "optimizer-1", caSiteID, caChillerDev, commandmodel.CapabilitySetTemperatureSetpoint, commandmodel.RiskHigh),
	}
	if _, err := h.Store.Submit(context.Background(), aiBlocked); !errors.Is(err, ErrDevicePriorityLocked) {
		t.Fatalf("expected ErrDevicePriorityLocked at 1h59m, got %v", err)
	}

	// Another operator manual command remains permitted while manual lock holds and refreshes the 2h lock.
	h.Live.Set(caChillerDev, "run_state", healthyTextObs(currentNow, "STOPPED", 18), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000021")
	op.IssuedAt = currentNow
	op.ExpiresAt = currentNow.Add(time.Minute)
	if _, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
		AssetID:        caChillerID,
		CommandPointID: caStartPt,
		IdempotencyKey: "op-manual-start-2",
	}); err != nil {
		t.Fatalf("expected equal-priority operator command allowed during cooldown, got %v", err)
	}
	env2, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimDispatch second operator command failed: %v", err)
	}
	completeTestDispatchForAuthority(t, h.Authority, caTenantID, caSiteID, caChillerDev, env2, commandmodel.TextScalar("RUNNING"), currentNow.Add(time.Second), 19)

	// Advance past the 2h cooldown from the latest operator manual command.
	currentNow = currentNow.Add(2*time.Hour + time.Second)
	aiBlocked.CurrentState.ObservedAt = currentNow
	aiBlocked.Authorization = testAuthSnapshot(currentNow, "optimizer-1", caSiteID, caChillerDev, commandmodel.CapabilitySetTemperatureSetpoint, commandmodel.RiskHigh)
	if _, err := h.Store.Submit(context.Background(), aiBlocked); err != nil {
		t.Fatalf("expected AI optimization allowed after 2h cooldown expires, got %v", err)
	}
}

func TestControlAuthorityPreDispatchHardeningFeedbackDiagnosticsAndDelta(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	t.Run("uses dctx.FeedbackPointKey instead of hardcoded run_state", func(t *testing.T) {
		h, op := seedPlantHarness(t, now)
		op.StepUpTOTPVerified = true

		// caSetpointPt uses feedbackPointKey="zone_temperature"; make run_state stale while zone_temperature is healthy.
		staleRun := healthyTextObs(now, "STOPPED", 11)
		staleRun.Freshness = "STALE"
		h.Live.Set(caChillerDev, "run_state", staleRun, "s2:sha256:0000000000000000000000000000000000000000000000000000000000000031")

		out, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
			AssetID:        caChillerID,
			CommandPointID: caSetpointPt,
			Parameters:     commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24.0},
			IdempotencyKey: "fb-key-healthy",
		})
		if err != nil {
			t.Fatalf("submit failed: %v", err)
		}
		env, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second)
		if err != nil || env.CommandID != out.View.CommandID {
			t.Fatalf("expected ClaimDispatch to succeed via FeedbackPointKey=zone_temperature even with stale run_state, got env=%+v err=%v", env, err)
		}
		completeTestDispatchForAuthority(t, h.Authority, caTenantID, caSiteID, caChillerDev, env, commandmodel.NumberScalar(24.0), now.Add(time.Second), 18)

		// Now flip: run_state healthy, zone_temperature stale -> ClaimDispatch must reject.
		h.Live.Set(caChillerDev, "run_state", healthyTextObs(now, "STOPPED", 11), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000032")
		h.Live.Set(caChillerDev, "zone_temperature", healthyNumObs(now, 24.0, 18), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000034")
		out2, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
			AssetID:        caChillerID,
			CommandPointID: caSetpointPt,
			Parameters:     commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24.5},
			IdempotencyKey: "fb-key-stale",
		})
		if err != nil {
			t.Fatalf("submit 2 failed: %v", err)
		}
		staleZone := healthyNumObs(now, 24.0, 18)
		staleZone.Freshness = "STALE"
		h.Live.Set(caChillerDev, "zone_temperature", staleZone, "s2:sha256:0000000000000000000000000000000000000000000000000000000000000033")

		if _, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
			t.Fatalf("expected ErrNoDispatchAvailable when zone_temperature stale, got %v", err)
		}
		got2, _ := h.Store.Get(context.Background(), caTenantID, out2.View.CommandID)
		if got2.Status != commandmodel.IntentRejected {
			t.Fatalf("expected out2 rejected when FeedbackPointKey stale, got %s", got2.Status)
		}
	})

	t.Run("blocks active fault_code or alarm_active for non-stop commands and permits stop/reset_fault", func(t *testing.T) {
		h, op := seedPlantHarness(t, now)
		op.StepUpTOTPVerified = true
		h.Live.Set(caChillerDev, "fault_code", healthyTextObs(now, "E04", 20), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000041")

		startOut, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
			AssetID: caChillerID, CommandPointID: caStartPt, IdempotencyKey: "fault-blocks-start",
		})
		if err != nil {
			t.Fatalf("submit start failed: %v", err)
		}
		if _, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
			t.Fatalf("expected start blocked by fault_code, got %v", err)
		}
		gotStart, _ := h.Store.Get(context.Background(), caTenantID, startOut.View.CommandID)
		if gotStart.Status != commandmodel.IntentRejected || !strings.Contains(gotStart.Transitions[len(gotStart.Transitions)-1].Reason, "DISPATCH_SAFETY_DEVICE_FAULT") {
			t.Fatalf("expected DISPATCH_SAFETY_DEVICE_FAULT rejection, got status=%s transitions=%+v", gotStart.Status, gotStart.Transitions)
		}

		// STOP must remain allowed on a faulted device.
		stopOut, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
			AssetID: caChillerID, CommandPointID: caStopPt, IdempotencyKey: "fault-allows-stop",
		})
		if err != nil {
			t.Fatalf("submit stop failed: %v", err)
		}
		env, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second)
		if err != nil || env.CommandID != stopOut.View.CommandID {
			t.Fatalf("expected STOP permitted while faulted, got env=%+v err=%v", env, err)
		}
		completeTestDispatchForAuthority(t, h.Authority, caTenantID, caSiteID, caChillerDev, env, commandmodel.TextScalar("STOPPED"), now.Add(time.Second), 21)

		// ResetFault must remain allowed cleanly inside evaluatePreDispatch.
		resetOut, err := h.Store.Submit(context.Background(), commandmodel.SubmitRequest{
			TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev, PointID: caStartPt,
			PrincipalID: op.Subject, IdempotencyKey: "fault-allows-reset",
			Capability: commandmodel.CapabilityResetFault, VerificationPointKey: "fault_code",
			PriorityTier: commandmodel.PriorityManualOverride, CommandSource: commandmodel.SourceOperator,
			CurrentState: commandmodel.CurrentStateEvidence{
				EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT",
				Quality: "GOOD", BusinessRevision: 20, ObservedAt: now,
			},
			Authorization: testAuthSnapshot(now, op.Subject, caSiteID, caChillerDev, commandmodel.CapabilityResetFault, commandmodel.RiskLow),
		})
		if err != nil {
			t.Fatalf("submit reset_fault failed: %v", err)
		}
		_, rej, _, evalErr := h.Authority.evaluatePreDispatch(context.Background(), commandmodel.DispatchEnvelope{
			CommandID: resetOut.Intent.ID, TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev, PointID: caStartPt,
		}, resetOut.Intent, ControlDispatchContext{
			TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev, PointID: caStartPt,
			AssetID: caChillerID, ControlMode: SiteControlModeRemoteAuto, FeedbackPointKey: "fault_code",
		})
		if evalErr != nil || rej != nil {
			t.Fatalf("expected RESET_FAULT permitted on faulted device, got rej=%+v err=%v", rej, evalErr)
		}
		// On a clean chiller (outside STOP off-dwell cooldown), cleared fault_code ("0") + alarm_active = true still blocks START.
		h2, op2 := seedPlantHarness(t, now)
		op2.StepUpTOTPVerified = true
		h2.Live.Set(caChillerDev, "fault_code", healthyTextObs(now, "0", 21), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000042")
		h2.Live.Set(caChillerDev, "alarm_active", &commandmodel.LivePointObservation{
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT",
			Freshness: "FRESH", Quality: "GOOD", BusinessRevision: 22,
			ReportedValue: commandmodel.BooleanScalar(true), ObservedAt: now,
		}, "s2:sha256:0000000000000000000000000000000000000000000000000000000000000043")
		alarmStart, err := h2.Authority.SubmitOperatorCommand(context.Background(), op2, SubmitOperatorCommandInput{
			AssetID: caChillerID, CommandPointID: caStartPt, IdempotencyKey: "alarm-blocks-start",
		})
		if err != nil {
			t.Fatalf("submit alarm start failed: %v", err)
		}
		if _, err := h2.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
			t.Fatalf("expected alarm_active=true to block start, got %v", err)
		}
		gotAlarm, _ := h2.Store.Get(context.Background(), caTenantID, alarmStart.View.CommandID)
		if gotAlarm.Status != commandmodel.IntentRejected {
			t.Fatalf("expected alarmStart rejected, got %s", gotAlarm.Status)
		}
	})

	t.Run("blocks local_remote_mode local/manual and ignores stale/absent optional diagnostics", func(t *testing.T) {
		h, op := seedPlantHarness(t, now)
		op.StepUpTOTPVerified = true

		// Stale fault_code / erroring alarm_active / remote local_remote_mode should not block.
		staleFault := healthyTextObs(now, "E99", 21)
		staleFault.Freshness = "STALE"
		h.Live.Set(caChillerDev, "fault_code", staleFault, "s2:sha256:0000000000000000000000000000000000000000000000000000000000000051")
		h.Live.SetError(caChillerDev, "alarm_active", unavailableControlError("point unsupported"))
		h.Live.Set(caChillerDev, "local_remote_mode", healthyTextObs(now, "REMOTE", 22), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000052")

		okStart, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
			AssetID: caChillerID, CommandPointID: caStartPt, IdempotencyKey: "remote-ok-start",
		})
		if err != nil {
			t.Fatalf("submit remote start failed: %v", err)
		}
		env, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second)
		if err != nil || env.CommandID != okStart.View.CommandID {
			t.Fatalf("expected remote mode + stale fault to dispatch, got env=%+v err=%v", env, err)
		}
		completeTestDispatchForAuthority(t, h.Authority, caTenantID, caSiteID, caChillerDev, env, commandmodel.TextScalar("RUNNING"), now.Add(time.Second), 23)

		// LOCAL_MANUAL blocks dispatch with DISPATCH_SAFETY_LOCAL_MANUAL_MODE.
		h.Live.Set(caChillerDev, "local_remote_mode", healthyTextObs(now, "LOCAL_MANUAL", 23), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000053")
		localStart, err := h.Authority.SubmitOperatorCommand(context.Background(), op, SubmitOperatorCommandInput{
			AssetID: caChillerID, CommandPointID: caStartPt, IdempotencyKey: "local-blocked-start",
		})
		if err != nil {
			t.Fatalf("submit local start failed: %v", err)
		}
		if _, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
			t.Fatalf("expected LOCAL_MANUAL to block dispatch, got %v", err)
		}
		gotLocal, _ := h.Store.Get(context.Background(), caTenantID, localStart.View.CommandID)
		if gotLocal.Status != commandmodel.IntentRejected || !strings.Contains(gotLocal.Transitions[len(gotLocal.Transitions)-1].Reason, "DISPATCH_SAFETY_LOCAL_MANUAL_MODE") {
			t.Fatalf("expected DISPATCH_SAFETY_LOCAL_MANUAL_MODE rejection, got status=%s transitions=%+v", gotLocal.Status, gotLocal.Transitions)
		}
	})

	t.Run("blocks AI setpoint when live feedback drifts past maximumDelta before dispatch", func(t *testing.T) {
		h, _ := seedPlantHarness(t, now)
		h.Live.Set(caChillerDev, "chw_supply_temp_setpoint", healthyNumObs(now, 11.0, 25), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000061")

		sub, err := h.Store.Submit(context.Background(), commandmodel.SubmitRequest{
			TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev, PointID: caSetpointPt,
			PrincipalID: "optimizer-1", IdempotencyKey: "ai-delta-drift-1",
			Capability:           commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
			Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 9.0},
			VerificationPointKey: "chw_supply_temp_setpoint",
			PriorityTier:         commandmodel.PriorityAIOptimization,
			CommandSource:        commandmodel.SourceOptimization,
			CurrentState: commandmodel.CurrentStateEvidence{
				EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT",
				Quality: "GOOD", BusinessRevision: 25, CurrentValue: testFloat64Pointer(11.0), ObservedAt: now,
			},
			Authorization: testAuthSnapshot(now, "optimizer-1", caSiteID, caChillerDev, commandmodel.CapabilitySetChilledWaterTemperatureSetpoint, commandmodel.RiskHigh),
		})
		if err != nil {
			t.Fatalf("submit AI setpoint failed: %v", err)
		}

		// Live setpoint drifts to 13.0 -> delta |9.0 - 13.0| = 4.0 > maximumDelta 3.0.
		h.Live.Set(caChillerDev, "chw_supply_temp_setpoint", healthyNumObs(now, 13.0, 26), "s2:sha256:0000000000000000000000000000000000000000000000000000000000000062")
		_, rej, _, evalErr := h.Authority.evaluatePreDispatch(context.Background(), commandmodel.DispatchEnvelope{
			CommandID: sub.Intent.ID, TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev, PointID: caSetpointPt,
			Parameters: cloneParameters(sub.Intent.Parameters),
		}, sub.Intent, ControlDispatchContext{
			TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev, PointID: caSetpointPt,
			AssetID: caChillerID, ControlMode: SiteControlModeRemoteAuto, FeedbackPointKey: "chw_supply_temp_setpoint",
		})
		if evalErr != nil || rej == nil || rej.Code != "DISPATCH_SAFETY_PRE_DISPATCH_DELTA_EXCEEDED" {
			t.Fatalf("expected DISPATCH_SAFETY_PRE_DISPATCH_DELTA_EXCEEDED, got rej=%+v err=%v", rej, evalErr)
		}
	})
}

func TestAutoRollbackPreconditionBypassEndToEndHTTPServiceAndAuthority(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	iamSigner := newECDSASigner(t)
	gatewaySigner := newECDSASigner(t)
	h, err := NewControlAuthorityForTest(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	h.Twin.ControlStates[caSiteID] = SiteControlStateView{
		TenantID: caTenantID, SiteID: caSiteID, ControlMode: SiteControlModeRemoteAuto,
	}

	handler := newCommandHTTPTestHandler(t, h.Store, iamSigner, gatewaySigner, now)
	chwProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint)
	grant, err := commandauth.SignGrant(iamSigner, commandauth.GrantClaims{
		Issuer:                      "spiffe://hvac.local/iam-service",
		Presenter:                   "spiffe://hvac.local/platform-gateway",
		Audience:                    "command-service",
		GrantID:                     "grant-rb-1",
		Purpose:                     commandmodel.AuthorizationCommandSubmit,
		PrincipalID:                 "optimization-service",
		TenantID:                    caTenantID,
		SiteID:                      caSiteID,
		DeviceID:                    caChillerDev,
		Capability:                  commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		MaximumRisk:                 commandmodel.RiskHigh,
		CapabilityRevision:          chwProfile.Revision,
		PolicyRevision:              "command-policy-1",
		EmergencyRevocationRevision: 2,
		WorkloadSPIFFE:              "spiffe://hvac.local/optimization-service",
		LeaseID:                     "lease-rb-1",
		IssuedAt:                    now.Unix(),
		ExpiresAt:                   now.Add(25 * time.Second).Unix(),
		TokenID:                     "grant-rb-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	postInternal := func(bodyStruct internalCreateCommandRequest) *httptest.ResponseRecorder {
		raw, err := json.Marshal(bodyStruct)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, InternalCommandsPath, bytes.NewReader(raw))
		req.Header.Set(commandGrantHeader, grant)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// 1. Valid auto-rollback PROTECTION_RULE setpoint bypass without CurrentValue/BusinessRevision is accepted over HTTP.
	validReq := internalCreateCommandRequest{
		TenantID:             caTenantID,
		SiteID:               caSiteID,
		DeviceID:             caChillerDev,
		PointID:              caSetpointPt,
		PrincipalID:          "optimization-service",
		IdempotencyKey:       "auto-rollback:rec-77",
		Capability:           commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.5},
		VerificationPointKey: "chw_supply_temp_setpoint",
		PreconditionMode:     commandmodel.PreconditionEmergencyBypass,
		BypassReason:         "revert optimization step after telemetry drop",
		PriorityTier:         commandmodel.PriorityProtectionRule,
		CommandSource:        commandmodel.SourceProtectionRule,
	}
	rec := postInternal(validReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for valid auto-rollback bypass, got %d body=%s", rec.Code, rec.Body.String())
	}
	var view CommandView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode view failed: %v", err)
	}

	// Simulate degraded telemetry in Live before ClaimDispatch -> ClaimDispatch must allow auto-rollback bypass!
	stale := healthyNumObs(now, 10.0, 99)
	stale.Freshness = "STALE"
	h.Live.Set(caChillerDev, "run_state", stale, "s2:sha256:0000000000000000000000000000000000000000000000000000000000000071")
	env, err := h.Authority.ClaimDispatch(context.Background(), caTenantID, "disp-1", 30*time.Second)
	if err != nil || env.CommandID != view.CommandID {
		t.Fatalf("expected ClaimDispatch to dispatch auto-rollback command under stale telemetry, got env=%+v err=%v", env, err)
	}

	// 2. Non-auto-rollback AI optimization attempt over HTTP is rejected cleanly with 400 Bad Request.
	aiAttempt := validReq
	aiAttempt.IdempotencyKey = "ai-opt:rec-78"
	aiAttempt.PriorityTier = commandmodel.PriorityAIOptimization
	aiAttempt.CommandSource = commandmodel.SourceOptimization
	badAIRec := postInternal(aiAttempt)
	if badAIRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for non-rollback AI bypass, got %d body=%s", badAIRec.Code, badAIRec.Body.String())
	}

	// 3. Direct Service.Submit rejects AI/missing-prefix bypass with ErrCapabilityDenied and blank reason with ErrInvalidRequest.
	svc := New(func() time.Time { return now })
	svcReq := commandmodel.SubmitRequest{
		TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev, PointID: caSetpointPt,
		PrincipalID: "optimization-service", IdempotencyKey: "auto-rollback:rec-79",
		Capability:           commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.5},
		VerificationPointKey: "chw_supply_temp_setpoint",
		PreconditionMode:     commandmodel.PreconditionEmergencyBypass,
		BypassReason:         "   ",
		PriorityTier:         commandmodel.PriorityProtectionRule,
		CommandSource:        commandmodel.SourceProtectionRule,
	}
	if _, err := svc.Submit(svcReq); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for blank auto-rollback reason, got %v", err)
	}
	svcReq.BypassReason = "revert"
	svcReq.IdempotencyKey = "normal-protection-key"
	if _, err := svc.Submit(svcReq); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("expected ErrCapabilityDenied when idempotencyKey lacks auto-rollback: prefix, got %v", err)
	}
}
