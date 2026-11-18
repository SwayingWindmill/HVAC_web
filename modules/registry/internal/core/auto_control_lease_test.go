package core

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type memoryAutoLeaseBackend struct {
	*memoryScheduleBackend
	leaseMu  sync.Mutex
	leases   map[string][]AutoControlLease
	monitors map[string][]OptimizationExecutionMonitor
	now      func() time.Time
}

func newMemoryAutoLeaseBackend(now func() time.Time) *memoryAutoLeaseBackend {
	return &memoryAutoLeaseBackend{
		memoryScheduleBackend: newMemoryScheduleBackend(),
		leases:                map[string][]AutoControlLease{},
		monitors:              map[string][]OptimizationExecutionMonitor{},
		now:                   now,
	}
}

func (backend *memoryAutoLeaseBackend) GetSiteAutoControlLease(_ context.Context, claims registryauth.GrantClaims, siteID string) (SiteAutoControlLeaseState, error) {
	backend.leaseMu.Lock()
	defer backend.leaseMu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return SiteAutoControlLeaseState{}, ErrNotFound
	}
	now := backend.now().UTC()
	state := SiteAutoControlLeaseState{
		TenantID:         claims.TenantID,
		SiteID:           siteID,
		ConfirmationMode: ConfirmationModeManual,
	}
	items := backend.leases[siteID]
	if len(items) == 0 {
		return state, nil
	}
	latest := items[len(items)-1]
	expiresAt, _ := time.Parse(time.RFC3339Nano, latest.ExpiresAt)
	if latest.Status == AutoControlLeaseActive && !expiresAt.After(now) {
		latest.Status = AutoControlLeaseExpired
	}
	state.LatestLease = &latest
	if latest.Status == AutoControlLeaseActive {
		active := latest
		state.ActiveLease = &active
		state.ConfirmationMode = ConfirmationModeAutoWithLease
	}
	return state, nil
}

func (backend *memoryAutoLeaseBackend) ArmSiteAutoControlLease(_ context.Context, claims registryauth.GrantClaims, input ArmAutoControlLeaseMutation) (SiteAutoControlLeaseState, bool, error) {
	now := backend.now().UTC()
	values, err := input.normalizedValues(now)
	if err != nil {
		return SiteAutoControlLeaseState{}, false, err
	}
	backend.leaseMu.Lock()
	defer backend.leaseMu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return SiteAutoControlLeaseState{}, false, ErrNotFound
	}
	items := backend.leases[input.SiteID]
	if len(items) > 0 {
		last := &items[len(items)-1]
		lastExp, _ := time.Parse(time.RFC3339Nano, last.ExpiresAt)
		if last.Status == AutoControlLeaseActive && lastExp.After(now) {
			if input.Meta.ExpectedRevision != last.Revision {
				return SiteAutoControlLeaseState{}, false, ErrRevisionConflict
			}
			last.Status = AutoControlLeaseRevoked
			reason := "superseded by new operator lease"
			last.TripReason = &reason
			last.Revision++
			last.UpdatedAt = formatInstant(now)
		}
	} else if input.Meta.ExpectedRevision != 0 {
		return SiteAutoControlLeaseState{}, false, ErrRevisionConflict
	}
	leaseID, _ := newCoreUUIDv7(now)
	lease := AutoControlLease{
		ID:                  leaseID,
		TenantID:            claims.TenantID,
		SiteID:              input.SiteID,
		Status:              AutoControlLeaseActive,
		ArmedBy:             claims.PrincipalID,
		ArmedAt:             formatInstant(now),
		ExpiresAt:           formatInstant(values.expiresAt),
		AllowedCapabilities: values.capabilities,
		MaxSetpointStepC:    values.maxStep,
		DeadbandC:           values.deadbandC,
		SupplyTempMinC:      values.supplyMin,
		SupplyTempMaxC:      values.supplyMax,
		ZoneTempMaxC:        values.zoneMax,
		MinRunningChillers:  values.minChillers,
		MaxCommandsPerHour:  values.maxPerHour,
		Revision:            1,
		CreatedAt:           formatInstant(now),
		UpdatedAt:           formatInstant(now),
	}
	backend.leases[input.SiteID] = append(items, lease)
	leaseCopy := lease
	return SiteAutoControlLeaseState{
		TenantID:         claims.TenantID,
		SiteID:           input.SiteID,
		ConfirmationMode: ConfirmationModeAutoWithLease,
		ActiveLease:      &leaseCopy,
		LatestLease:      &leaseCopy,
	}, false, nil
}

func (backend *memoryAutoLeaseBackend) RevokeSiteAutoControlLease(_ context.Context, claims registryauth.GrantClaims, input RevokeAutoControlLeaseMutation) (SiteAutoControlLeaseState, bool, error) {
	if err := input.Validate(); err != nil {
		return SiteAutoControlLeaseState{}, false, err
	}
	backend.leaseMu.Lock()
	defer backend.leaseMu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return SiteAutoControlLeaseState{}, false, ErrNotFound
	}
	items := backend.leases[input.SiteID]
	for idx := len(items) - 1; idx >= 0; idx-- {
		if items[idx].Status == AutoControlLeaseActive {
			if input.Meta.ExpectedRevision != 0 && input.Meta.ExpectedRevision != items[idx].Revision {
				return SiteAutoControlLeaseState{}, false, ErrRevisionConflict
			}
			now := backend.now().UTC()
			reason := effectiveControlReason(input.Reason, input.Meta.Reason)
			items[idx].Status = AutoControlLeaseRevoked
			items[idx].TripReason = &reason
			items[idx].Revision++
			items[idx].UpdatedAt = formatInstant(now)
			leaseCopy := items[idx]
			return SiteAutoControlLeaseState{
				TenantID:         claims.TenantID,
				SiteID:           input.SiteID,
				ConfirmationMode: ConfirmationModeManual,
				ActiveLease:      nil,
				LatestLease:      &leaseCopy,
			}, false, nil
		}
	}
	return SiteAutoControlLeaseState{}, false, ErrNotFound
}

func (backend *memoryAutoLeaseBackend) ListOptimizationExecutionMonitors(_ context.Context, claims registryauth.GrantClaims, siteID string) (OptimizationExecutionMonitorList, error) {
	backend.leaseMu.Lock()
	defer backend.leaseMu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return OptimizationExecutionMonitorList{}, ErrNotFound
	}
	items := append([]OptimizationExecutionMonitor(nil), backend.monitors[siteID]...)
	if items == nil {
		items = []OptimizationExecutionMonitor{}
	}
	return OptimizationExecutionMonitorList{
		TenantID: claims.TenantID,
		SiteID:   siteID,
		Items:    items,
	}, nil
}

func TestAutoControlLeaseValidationBounds(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	valid := ArmAutoControlLeaseMutation{
		SiteID:              testSiteA1,
		ExpiresInHours:      8,
		AllowedCapabilities: []string{"SET_CHILLED_WATER_TEMPERATURE_SETPOINT"},
		SupplyTempMinC:      6.0,
		SupplyTempMaxC:      12.0,
		Meta:                MutationMeta{ExpectedRevision: 0, IdempotencyKey: "lease-arm-0001", Reason: "启用冷站自动寻优"},
	}
	vals, err := valid.normalizedValues(now)
	if err != nil {
		t.Fatalf("valid lease rejected: %v", err)
	}
	if vals.deadbandC != 0.2 {
		t.Fatalf("expected default deadbandC=0.2, got %v", vals.deadbandC)
	}

	minSupplyAccepted := valid
	minSupplyAccepted.SupplyTempMinC = 5.0
	if _, err := minSupplyAccepted.normalizedValues(now); err != nil {
		t.Fatalf("expected supplyMin=5.0 to be accepted, got %v", err)
	}
	for _, lowMin := range []float64{4.0, 4.99} {
		belowMin := valid
		belowMin.SupplyTempMinC = lowMin
		if _, err := belowMin.normalizedValues(now); err == nil {
			t.Fatalf("expected supplyMin=%v (<5.0) to be rejected", lowMin)
		}
	}

	for _, okDeadband := range []float64{0.05, 0.2, 2.0} {
		withDeadband := valid
		d := okDeadband
		withDeadband.DeadbandC = &d
		got, err := withDeadband.normalizedValues(now)
		if err != nil || got.deadbandC != okDeadband {
			t.Fatalf("expected deadbandC=%v accepted, got %+v err=%v", okDeadband, got, err)
		}
	}
	for _, badDeadband := range []float64{0.04, 2.01, -0.1, math.NaN(), math.Inf(1)} {
		withBadDeadband := valid
		d := badDeadband
		withBadDeadband.DeadbandC = &d
		if _, err := withBadDeadband.normalizedValues(now); err == nil {
			t.Fatalf("expected deadbandC=%v to be rejected", badDeadband)
		}
	}

	tooLong := valid
	tooLong.ExpiresInHours = 25
	if _, err := tooLong.normalizedValues(now); err == nil {
		t.Fatal("expected >24h lease to be rejected")
	}
	badStep := valid
	step := 2.5
	badStep.MaxSetpointStepC = &step
	if _, err := badStep.normalizedValues(now); err == nil {
		t.Fatal("expected >2.0C step limit to be rejected")
	}
	badCap := valid
	badCap.AllowedCapabilities = []string{"UNKNOWN_CAP"}
	if _, err := badCap.normalizedValues(now); err == nil {
		t.Fatal("expected unsupported capability to be rejected")
	}
}

func TestAutoControlLeaseAndMonitorsHTTPLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	backend := newMemoryAutoLeaseBackend(func() time.Time { return now })
	client := newScheduleHTTPClientWithInterfaces(t, backend, backend)
	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	manageClaims := testGrantClaims(registryauth.ActionScheduleManage)

	// Default when never armed is MANUAL_CONFIRM.
	resp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/auto-control-lease", readClaims, "值班员", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET initial lease status=%d body=%s", resp.Code, resp.Body.String())
	}
	var initial SiteAutoControlLeaseState
	if err := json.NewDecoder(resp.Body).Decode(&initial); err != nil {
		t.Fatal(err)
	}
	if initial.ConfirmationMode != ConfirmationModeManual || initial.ActiveLease != nil {
		t.Fatalf("expected MANUAL_CONFIRM with nil lease, got %#v", initial)
	}

	// Read-only grant cannot arm.
	forbidden := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/auto-control-lease", readClaims, "观察员", map[string]any{
		"expiresInHours":      8,
		"allowedCapabilities": []string{"SET_CHILLED_WATER_TEMPERATURE_SETPOINT"},
		"supplyTempMinC":      6.0,
		"supplyTempMaxC":      12.0,
		"meta":                map[string]any{"expectedRevision": 0, "idempotencyKey": "lease-arm-deny-1", "reason": "尝试授权"},
	})
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for read-only grant arming lease, got %d", forbidden.Code)
	}

	// Reject < 5.0C supplyTempMinC and out-of-range deadbandC.
	rejectedMin := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/auto-control-lease", manageClaims, "工程师", map[string]any{
		"expiresInHours":      8,
		"allowedCapabilities": []string{"SET_CHILLED_WATER_TEMPERATURE_SETPOINT"},
		"supplyTempMinC":      4.5,
		"supplyTempMaxC":      12.0,
		"meta":                map[string]any{"expectedRevision": 0, "idempotencyKey": "lease-arm-bad-min", "reason": "低于5度下限"},
	})
	if rejectedMin.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for supplyTempMinC=4.5, got %d body=%s", rejectedMin.Code, rejectedMin.Body.String())
	}

	// Schedule manage grant arms ACTIVE lease (defaults deadbandC=0.2 when omitted).
	armedResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/auto-control-lease", manageClaims, "工程师", map[string]any{
		"expiresInHours":      8,
		"allowedCapabilities": []string{"SET_CHILLED_WATER_TEMPERATURE_SETPOINT"},
		"supplyTempMinC":      6.0,
		"supplyTempMaxC":      12.0,
		"meta":                map[string]any{"expectedRevision": 0, "idempotencyKey": "lease-arm-ok-1", "reason": "白班启用自动节能"},
	})
	if armedResp.Code != http.StatusOK {
		t.Fatalf("PUT arm lease status=%d body=%s", armedResp.Code, armedResp.Body.String())
	}
	var armed SiteAutoControlLeaseState
	if err := json.NewDecoder(armedResp.Body).Decode(&armed); err != nil {
		t.Fatal(err)
	}
	if armed.ConfirmationMode != ConfirmationModeAutoWithLease || armed.ActiveLease == nil || armed.ActiveLease.Status != AutoControlLeaseActive || armed.ActiveLease.DeadbandC != 0.2 {
		t.Fatalf("expected ACTIVE lease with deadbandC=0.2, got %#v", armed)
	}

	// Re-arm with explicit deadbandC=0.35 and verify manualBackoff fields surface on GET.
	rearmResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/auto-control-lease", manageClaims, "工程师", map[string]any{
		"expiresInHours":      8,
		"allowedCapabilities": []string{"SET_CHILLED_WATER_TEMPERATURE_SETPOINT"},
		"deadbandC":           0.35,
		"supplyTempMinC":      5.0,
		"supplyTempMaxC":      12.0,
		"meta":                map[string]any{"expectedRevision": armed.ActiveLease.Revision, "idempotencyKey": "lease-arm-ok-2", "reason": "调整死区"},
	})
	if rearmResp.Code != http.StatusOK {
		t.Fatalf("PUT re-arm lease status=%d body=%s", rearmResp.Code, rearmResp.Body.String())
	}
	var rearmed SiteAutoControlLeaseState
	if err := json.NewDecoder(rearmResp.Body).Decode(&rearmed); err != nil {
		t.Fatal(err)
	}
	if rearmed.ActiveLease == nil || rearmed.ActiveLease.DeadbandC != 0.35 || rearmed.ActiveLease.SupplyTempMinC != 5.0 {
		t.Fatalf("expected deadbandC=0.35 and supplyTempMinC=5.0, got %#v", rearmed)
	}

	backoffUntil := formatInstant(now.Add(30 * time.Minute))
	backend.leases[testSiteA1][len(backend.leases[testSiteA1])-1].ManualBackoffUntil = &backoffUntil
	backend.leases[testSiteA1][len(backend.leases[testSiteA1])-1].ManualBackoffReason = "现场手动调节供水温度"
	getBackoffResp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/auto-control-lease", readClaims, "值班员", nil)
	if getBackoffResp.Code != http.StatusOK {
		t.Fatalf("GET lease with backoff status=%d body=%s", getBackoffResp.Code, getBackoffResp.Body.String())
	}
	var gotBackoff SiteAutoControlLeaseState
	if err := json.NewDecoder(getBackoffResp.Body).Decode(&gotBackoff); err != nil {
		t.Fatal(err)
	}
	if gotBackoff.ActiveLease == nil || gotBackoff.ActiveLease.ManualBackoffUntil == nil || *gotBackoff.ActiveLease.ManualBackoffUntil != backoffUntil || gotBackoff.ActiveLease.ManualBackoffReason != "现场手动调节供水温度" {
		t.Fatalf("expected manualBackoff fields in GET response, got %#v", gotBackoff.ActiveLease)
	}

	// Revoke returns site to MANUAL_CONFIRM.
	revokeResp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/auto-control-lease/revoke", manageClaims, "工程师", map[string]any{
		"reason": "现场巡检撤销自动授权",
		"meta":   map[string]any{"expectedRevision": rearmed.ActiveLease.Revision, "idempotencyKey": "lease-revoke-ok-1", "reason": "撤销授权"},
	})
	if revokeResp.Code != http.StatusOK {
		t.Fatalf("POST revoke lease status=%d body=%s", revokeResp.Code, revokeResp.Body.String())
	}
	var revoked SiteAutoControlLeaseState
	if err := json.NewDecoder(revokeResp.Body).Decode(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked.ConfirmationMode != ConfirmationModeManual || revoked.ActiveLease != nil || revoked.LatestLease == nil || revoked.LatestLease.Status != AutoControlLeaseRevoked {
		t.Fatalf("expected REVOKED state, got %#v", revoked)
	}

	// List execution monitors returns both command-backed and nullable-command-intent guardrail shadow items.
	intentID := "01990000-8800-7000-8000-000000000003"
	rawRec := 4.2
	clamped := 5.0
	backend.monitors[testSiteA1] = []OptimizationExecutionMonitor{
		{
			ID: "01990000-8800-7000-8000-000000000001", TenantID: testTenantA, SiteID: testSiteA1,
			LeaseID: revoked.LatestLease.ID, RecommendationID: "01990000-8800-7000-8000-000000000002",
			CommandIntentID: &intentID, DeviceID: "01990000-8800-7000-8000-000000000004",
			TargetCapability: "SET_CHILLED_WATER_TEMPERATURE_SETPOINT", Status: ExecutionMonitorVerified,
			SupplyTempMinC: 5.0, ZoneTempMaxC: 26.0, SupplyTempMaxC: 12.0,
			RawRecommendedValue: &rawRec, ClampedValue: &clamped, GuardrailVerdict: "CLAMPED",
			GuardrailTrace: json.RawMessage(`[{"rule":"supply_min","action":"CLAMP","from":4.2,"to":5.0}]`),
			StartedAt:      formatInstant(now), WindowEndsAt: formatInstant(now.Add(30 * time.Minute)),
			CreatedAt: formatInstant(now), UpdatedAt: formatInstant(now),
		},
		{
			ID: "01990000-8800-7000-8000-000000000005", TenantID: testTenantA, SiteID: testSiteA1,
			LeaseID: revoked.LatestLease.ID, RecommendationID: "01990000-8800-7000-8000-000000000006",
			CommandIntentID: nil, DeviceID: "01990000-8800-7000-8000-000000000004",
			TargetCapability: "SET_CHILLED_WATER_TEMPERATURE_SETPOINT", Status: ExecutionMonitorShadowRecorded,
			SupplyTempMinC: 5.0, ZoneTempMaxC: 26.0, SupplyTempMaxC: 12.0,
			RawRecommendedValue: &rawRec, GuardrailVerdict: "DEADBAND_SKIPPED",
			GuardrailTrace: json.RawMessage(`[{"rule":"deadband","action":"SKIP"}]`),
			StartedAt:      formatInstant(now), WindowEndsAt: formatInstant(now.Add(30 * time.Minute)),
			CreatedAt: formatInstant(now), UpdatedAt: formatInstant(now),
		},
	}
	monResp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/optimization-execution-monitors", readClaims, "值班员", nil)
	if monResp.Code != http.StatusOK {
		t.Fatalf("GET monitors status=%d body=%s", monResp.Code, monResp.Body.String())
	}
	rawMonBody := monResp.Body.String()
	if !strings.Contains(rawMonBody, `"commandIntentId":null`) {
		t.Fatalf("expected explicit null commandIntentId in JSON response, got %s", rawMonBody)
	}
	var monList OptimizationExecutionMonitorList
	if err := json.Unmarshal([]byte(rawMonBody), &monList); err != nil {
		t.Fatal(err)
	}
	if len(monList.Items) != 2 || monList.Items[0].Status != ExecutionMonitorVerified || monList.Items[0].CommandIntentID == nil ||
		monList.Items[1].Status != ExecutionMonitorShadowRecorded || monList.Items[1].CommandIntentID != nil || monList.Items[1].GuardrailVerdict != "DEADBAND_SKIPPED" {
		t.Fatalf("unexpected monitors list: %#v", monList)
	}
}
