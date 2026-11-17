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
	"testing"
	"time"

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
