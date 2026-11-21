package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

func strPtr(v string) *string { return &v }

type fakePointHealthChecker struct {
	mu          sync.Mutex
	err         error
	snapshot    *OptimizationPointHealthSnapshot
	livePoints  []TelemetryLivePoint
	lastDevices []string
	lastKeys    []string
}

func (checker *fakePointHealthChecker) setErr(err error) {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	checker.err = err
}

func (checker *fakePointHealthChecker) setLivePoints(points []TelemetryLivePoint) {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	checker.livePoints = append([]TelemetryLivePoint(nil), points...)
}

func (checker *fakePointHealthChecker) CheckTelemetryPointHealth(ctx context.Context, tenantID, siteID string, deviceIDs, telemetryKeys []string) error {
	snapshot, err := checker.InspectTelemetryPointHealth(ctx, tenantID, siteID, deviceIDs, telemetryKeys)
	if err != nil {
		return err
	}
	if !snapshot.Healthy {
		return unhealthyPointHealthError(snapshot)
	}
	return nil
}

func (checker *fakePointHealthChecker) InspectTelemetryPointHealth(_ context.Context, _, _ string, deviceIDs, telemetryKeys []string) (OptimizationPointHealthSnapshot, error) {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	checker.lastDevices = append([]string(nil), deviceIDs...)
	checker.lastKeys = append([]string(nil), telemetryKeys...)
	if checker.err != nil {
		return OptimizationPointHealthSnapshot{
			Healthy:    false,
			LivePoints: append([]TelemetryLivePoint(nil), checker.livePoints...),
		}, checker.err
	}
	if checker.snapshot != nil {
		snap := *checker.snapshot
		snap.LivePoints = append([]TelemetryLivePoint(nil), checker.snapshot.LivePoints...)
		if len(snap.LivePoints) == 0 && len(checker.livePoints) > 0 {
			snap.LivePoints = append([]TelemetryLivePoint(nil), checker.livePoints...)
		}
		return snap, nil
	}
	return OptimizationPointHealthSnapshot{
		Healthy:    true,
		LivePoints: append([]TelemetryLivePoint(nil), checker.livePoints...),
	}, nil
}

type manualOverrideRecordCall struct {
	MonitorID    string
	ClaimOwner   string
	Reason       string
	BackoffUntil time.Time
	CheckedAt    time.Time
}

type tripRecordCall struct {
	MonitorID               string
	ClaimOwner              string
	NewStatus               string
	TripReason              string
	RollbackCommandIntentID *string
	TrippedAt               time.Time
}

type fakeAutoControlStore struct {
	mu                    sync.Mutex
	lease                 *AutoControlLeaseRecord
	leaseErr              error
	siteCtx               AutoControlSiteContext
	siteCtxErr            error
	claimHook             func(claimOwner string)
	dispatchedRecs        []Recommendation
	monitors              []OptimizationExecutionMonitorRecord
	claimCalls            int
	releaseCalls          int
	heartbeatCalls        int
	unhealthyCalls        int
	verifiedCalls         int
	submitFailedCalls     int
	trippedRecords        []tripRecordCall
	manualOverrideRecords []manualOverrideRecordCall
	recordMonitorSeq      int
}

func (store *fakeAutoControlStore) ActiveAutoControlLease(context.Context, string, string, time.Time) (*AutoControlLeaseRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.lease == nil {
		return nil, store.leaseErr
	}
	leaseCopy := *store.lease
	return &leaseCopy, store.leaseErr
}

func (store *fakeAutoControlStore) LoadAutoControlSiteContext(context.Context, string, string, string, time.Time) (AutoControlSiteContext, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.siteCtx, store.siteCtxErr
}

func (store *fakeAutoControlStore) PreRegisterAutomatedDispatch(_ context.Context, reg AutomatedDispatchRegistration) (PreRegisterDispatchResult, error) {
	return store.recordFakeMonitor(reg, false)
}

func (store *fakeAutoControlStore) RecordShadowMonitor(_ context.Context, reg AutomatedDispatchRegistration) (PreRegisterDispatchResult, error) {
	return store.recordFakeMonitor(reg, true)
}

func (store *fakeAutoControlStore) recordFakeMonitor(reg AutomatedDispatchRegistration, shadow bool) (PreRegisterDispatchResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	nowUTC := reg.At.UTC()
	if store.lease == nil || store.lease.Status != "ACTIVE" || !store.lease.ExpiresAt.After(nowUTC) {
		return PreRegisterDispatchResult{SkipReason: "MANUAL_CONFIRM"}, nil
	}
	if store.lease.ManualBackoffUntil != nil && store.lease.ManualBackoffUntil.UTC().After(nowUTC) {
		return PreRegisterDispatchResult{SkipReason: "MANUAL_BACKOFF_ACTIVE"}, nil
	}
	if !shadow && normalizeExecutionMode(store.lease.ExecutionMode) == ExecutionModeShadow {
		return PreRegisterDispatchResult{SkipReason: "SHADOW_MODE_ACTIVE"}, nil
	}
	targetCap := reg.TargetCapability
	if strings.TrimSpace(string(targetCap)) == "" {
		targetCap = commandmodel.CapabilitySetChilledWaterTemperatureSetpoint
	}
	loopKey := normalizeMonitorLoopKey(reg.LoopKey, targetCap)
	targetUnit := firstNonEmptyString(strings.TrimSpace(reg.TargetUnit), targetUnitForCapability(targetCap))

	if loopKey == LoopKeyPlant {
		for _, rec := range store.dispatchedRecs {
			if rec.ID == reg.Recommendation.ID {
				return PreRegisterDispatchResult{SkipReason: "ALREADY_DISPATCHED"}, nil
			}
		}
	} else if !shadow {
		for _, mon := range store.monitors {
			if mon.RecommendationID == reg.Recommendation.ID &&
				normalizeMonitorLoopKey(mon.LoopKey, effectiveMonitorCapability(mon)) == loopKey &&
				mon.Status != ExecutionMonitorSubmitFailed {
				return PreRegisterDispatchResult{SkipReason: "ALREADY_DISPATCHED"}, nil
			}
		}
	}
	recentCount := 0
	recentShadowCount := 0
	hourAgo := nowUTC.Add(-time.Hour)
	for _, mon := range store.monitors {
		if mon.TenantID != reg.Recommendation.TenantID || mon.SiteID != reg.Recommendation.SiteID {
			continue
		}
		if !shadow &&
			normalizeMonitorLoopKey(mon.LoopKey, effectiveMonitorCapability(mon)) == loopKey &&
			(mon.Status == ExecutionMonitorPendingSubmit || mon.Status == ExecutionMonitorObserving) {
			return PreRegisterDispatchResult{SkipReason: "ACTIVE_MONITOR_EXISTS"}, nil
		}
		if !mon.StartedAt.Before(hourAgo) {
			switch mon.Status {
			case ExecutionMonitorPendingSubmit,
				ExecutionMonitorObserving,
				ExecutionMonitorVerified,
				ExecutionMonitorTrippedRolledBack,
				ExecutionMonitorRollbackFailed,
				ExecutionMonitorManualOverride:
				recentCount++
			case ExecutionMonitorShadowRecorded:
				recentShadowCount++
			}
		}
	}
	if shadow {
		if recentShadowCount >= DefaultShadowMaxPerHour {
			return PreRegisterDispatchResult{SkipReason: "SHADOW_RATE_LIMIT_REACHED"}, nil
		}
	} else if recentCount >= store.lease.MaxCommandsPerHour {
		return PreRegisterDispatchResult{SkipReason: "RATE_LIMIT_REACHED"}, nil
	}

	store.recordMonitorSeq++
	monitorID, idErr := optimizationUUIDv7(nowUTC.Add(time.Duration(store.recordMonitorSeq) * time.Millisecond))
	if idErr != nil {
		return PreRegisterDispatchResult{}, idErr
	}
	baseline := reg.BaselineSupplyC
	applied := reg.AppliedSupplyC
	restore := reg.RestoreSupplyC
	rawRec := reg.RawRecommendedC
	var clampedCopy *float64
	if reg.ClampedC != nil {
		v := *reg.ClampedC
		clampedCopy = &v
	}
	status := ExecutionMonitorPendingSubmit
	executionMode := ExecutionModeClosedLoop
	if shadow {
		status = ExecutionMonitorShadowRecorded
		executionMode = ExecutionModeShadow
	}
	mon := OptimizationExecutionMonitorRecord{
		ID:                  monitorID,
		TenantID:            reg.Recommendation.TenantID,
		SiteID:              reg.Recommendation.SiteID,
		LeaseID:             reg.LeaseID,
		RecommendationID:    reg.Recommendation.ID,
		DeviceID:            reg.Target.DeviceID,
		TargetCapability:    string(targetCap),
		LoopKey:             loopKey,
		TargetUnit:          targetUnit,
		BaselineSupplyTempC: &baseline,
		AppliedSupplyTempC:  &applied,
		RestoreSupplyTempC:  &restore,
		ZoneTempMaxC:        reg.ZoneMaxC,
		SupplyTempMinC:      reg.SupplyMinC,
		SupplyTempMaxC:      reg.SupplyMaxC,
		RawRecommendedValue: &rawRec,
		ClampedValue:        clampedCopy,
		GuardrailVerdict:    reg.GuardrailVerdict,
		GuardrailTrace:      append(json.RawMessage(nil), reg.GuardrailTrace...),
		StartedAt:           nowUTC,
		WindowEndsAt:        nowUTC.Add(DefaultExecutionMonitorWindow),
		LastCheckedAt:       &nowUTC,
		Status:              status,
		ExecutionMode:       executionMode,
		DecisionID:          reg.DecisionID,
		TraceID:             reg.TraceID,
	}
	store.monitors = append(store.monitors, mon)
	return PreRegisterDispatchResult{Monitor: mon}, nil
}

func (store *fakeAutoControlStore) ActivateAutomatedDispatch(_ context.Context, recommendation Recommendation, monitorID, commandIntentID string, at time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	nowUTC := at.UTC()
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitorID {
			continue
		}
		if store.monitors[idx].Status != ExecutionMonitorPendingSubmit {
			return errors.New("pending monitor already transitioned")
		}
		store.monitors[idx].Status = ExecutionMonitorObserving
		store.monitors[idx].CommandIntentID = commandIntentID
		store.monitors[idx].LastCheckedAt = &nowUTC
		if normalizeMonitorLoopKey(store.monitors[idx].LoopKey, effectiveMonitorCapability(store.monitors[idx])) == LoopKeyPlant {
			store.dispatchedRecs = append(store.dispatchedRecs, recommendation)
		}
		return nil
	}
	return errors.New("pending monitor not found")
}

func (store *fakeAutoControlStore) MarkAutomatedDispatchFailed(_ context.Context, _, _, monitorID, errorSummary string, at time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.submitFailedCalls++
	nowUTC := at.UTC()
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitorID {
			continue
		}
		if store.monitors[idx].Status != ExecutionMonitorPendingSubmit {
			return errors.New("pending monitor already transitioned")
		}
		summary := truncateSummary(strings.TrimSpace(errorSummary), 500)
		store.monitors[idx].Status = ExecutionMonitorSubmitFailed
		store.monitors[idx].TripReason = &summary
		store.monitors[idx].LastCheckedAt = &nowUTC
		return nil
	}
	return errors.New("pending monitor not found")
}

func (store *fakeAutoControlStore) ClaimObservingExecutionMonitors(_ context.Context, claimOwner string, leaseDuration time.Duration, now time.Time, limit int) ([]OptimizationExecutionMonitorRecord, error) {
	owner := strings.TrimSpace(claimOwner)
	if owner == "" || leaseDuration <= 0 {
		return nil, errors.New("execution monitor claim owner and positive lease duration are required")
	}
	nowUTC := now.UTC()
	until := nowUTC.Add(leaseDuration)
	store.mu.Lock()
	store.claimCalls++
	hook := store.claimHook
	out := make([]OptimizationExecutionMonitorRecord, 0, len(store.monitors))
	for idx := range store.monitors {
		mon := &store.monitors[idx]
		if mon.Status != ExecutionMonitorObserving {
			continue
		}
		if mon.ClaimUntil != nil && mon.ClaimUntil.After(nowUTC) {
			continue
		}
		ownerCopy := owner
		untilCopy := until
		mon.ClaimOwner = &ownerCopy
		mon.ClaimUntil = &untilCopy
		out = append(out, *mon)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	store.mu.Unlock()
	if hook != nil {
		hook(owner)
	}
	return out, nil
}

func matchesClaimOwnership(mon OptimizationExecutionMonitorRecord, claimOwner string) bool {
	owner := strings.TrimSpace(claimOwner)
	if mon.Status != ExecutionMonitorObserving || mon.ClaimOwner == nil || owner == "" {
		return false
	}
	return *mon.ClaimOwner == owner
}

func (store *fakeAutoControlStore) ReleaseMonitorClaim(_ context.Context, monitorID, _, _, claimOwner string, _ time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.releaseCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitorID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

func (store *fakeAutoControlStore) RecordMonitorHealthyHeartbeat(_ context.Context, monitorID, _, _, claimOwner string, checkedAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.heartbeatCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitorID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		t := checkedAt.UTC()
		store.monitors[idx].TelemetryUnhealthySince = nil
		store.monitors[idx].LastCheckedAt = &t
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

func (store *fakeAutoControlStore) RecordMonitorTelemetryUnhealthy(_ context.Context, monitorID, _, _, claimOwner string, unhealthySince, checkedAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.unhealthyCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitorID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		if store.monitors[idx].TelemetryUnhealthySince == nil {
			u := unhealthySince.UTC()
			store.monitors[idx].TelemetryUnhealthySince = &u
		}
		c := checkedAt.UTC()
		store.monitors[idx].LastCheckedAt = &c
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

func (store *fakeAutoControlStore) CompleteMonitorVerified(_ context.Context, monitorID, _, _, claimOwner string, checkedAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.verifiedCalls++
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitorID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		c := checkedAt.UTC()
		store.monitors[idx].Status = ExecutionMonitorVerified
		store.monitors[idx].TelemetryUnhealthySince = nil
		store.monitors[idx].LastCheckedAt = &c
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

func (store *fakeAutoControlStore) MarkMonitorManualOverride(_ context.Context, monitor OptimizationExecutionMonitorRecord, claimOwner, reason string, backoffUntil, checkedAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitor.ID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		b := backoffUntil.UTC()
		c := checkedAt.UTC()
		store.manualOverrideRecords = append(store.manualOverrideRecords, manualOverrideRecordCall{
			MonitorID:    monitor.ID,
			ClaimOwner:   claimOwner,
			Reason:       reason,
			BackoffUntil: b,
			CheckedAt:    c,
		})
		if store.lease != nil && store.lease.Status == "ACTIVE" {
			store.lease.ManualBackoffUntil = &b
			store.lease.ManualBackoffReason = reason
			store.lease.Revision++
		}
		r := reason
		store.monitors[idx].Status = ExecutionMonitorManualOverride
		store.monitors[idx].TripReason = &r
		store.monitors[idx].LastCheckedAt = &c
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

func (store *fakeAutoControlStore) TripMonitorAndLease(_ context.Context, monitor OptimizationExecutionMonitorRecord, claimOwner, newStatus, tripReason string, rollbackCommandIntentID *string, trippedAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for idx := range store.monitors {
		if store.monitors[idx].ID != monitor.ID {
			continue
		}
		if !matchesClaimOwnership(store.monitors[idx], claimOwner) {
			return ErrExecutionMonitorClaimLost
		}
		store.trippedRecords = append(store.trippedRecords, tripRecordCall{
			MonitorID:               monitor.ID,
			ClaimOwner:              claimOwner,
			NewStatus:               newStatus,
			TripReason:              tripReason,
			RollbackCommandIntentID: rollbackCommandIntentID,
			TrippedAt:               trippedAt.UTC(),
		})
		if store.lease != nil {
			store.lease.Status = "TRIPPED"
		}
		store.monitors[idx].Status = newStatus
		r := tripReason
		store.monitors[idx].TripReason = &r
		store.monitors[idx].RollbackCommandIntentID = rollbackCommandIntentID
		t := trippedAt.UTC()
		store.monitors[idx].LastCheckedAt = &t
		store.monitors[idx].ClaimOwner = nil
		store.monitors[idx].ClaimUntil = nil
		return nil
	}
	return ErrExecutionMonitorClaimLost
}

type fakeTelemetryReader struct {
	obs AutoControlLiveObservation
	err error
}

func (reader *fakeTelemetryReader) ReadLiveAutoControlObservation(context.Context, string, string, InputMapping, StagingTopology, time.Time) (AutoControlLiveObservation, error) {
	return reader.obs, reader.err
}

type fakeDispatcher struct {
	mu                 sync.Mutex
	grantReqs          []commandauth.WorkloadDecisionRequest
	submitInputs       []AutoControlDispatchInput
	submitTraceparents []string
	decision           commandauth.Decision
	grant              string
	commandID          string
	grantErr           error
	submitErr          error
	submitHook         func(ctx context.Context, input AutoControlDispatchInput)
}

func (disp *fakeDispatcher) RequestCommandGrant(_ context.Context, req commandauth.WorkloadDecisionRequest) (string, commandauth.Decision, error) {
	disp.mu.Lock()
	defer disp.mu.Unlock()
	disp.grantReqs = append(disp.grantReqs, req)
	return disp.grant, disp.decision, disp.grantErr
}

func (disp *fakeDispatcher) SubmitAutoCommand(ctx context.Context, _ string, input AutoControlDispatchInput) (string, error) {
	disp.mu.Lock()
	hook := disp.submitHook
	disp.submitInputs = append(disp.submitInputs, input)
	disp.submitTraceparents = append(disp.submitTraceparents, observability.Traceparent(ctx))
	cmdID := disp.commandID
	submitErr := disp.submitErr
	disp.mu.Unlock()
	if hook != nil {
		hook(ctx, input)
	}
	return cmdID, submitErr
}

type fakeControlEventPublisher struct {
	mu     sync.Mutex
	events []ControlEvent
	err    error
}

func (p *fakeControlEventPublisher) PublishControlEvent(_ context.Context, event ControlEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
	return p.err
}

func testAutoLease(now time.Time) *AutoControlLeaseRecord {
	return &AutoControlLeaseRecord{
		ID:                  "01990000-8800-7000-8000-000000000011",
		TenantID:            "01990000-3000-7000-8000-000000000001",
		SiteID:              "01990000-5000-7000-8000-000000000001",
		Status:              "ACTIVE",
		ArmedBy:             "01990000-1000-7000-8000-000000000001",
		ArmedAt:             now.Add(-10 * time.Minute),
		ExpiresAt:           now.Add(4 * time.Hour),
		AllowedCapabilities: []string{string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint)},
		MaxSetpointStepC:    1.5,
		SupplyTempMinC:      6.0,
		SupplyTempMaxC:      12.0,
		ZoneTempMaxC:        26.0,
		MinRunningChillers:  1,
		MaxCommandsPerHour:  2,
		DeadbandC:           0.2,
		Revision:            1,
	}
}

func testAutoSiteCtx() AutoControlSiteContext {
	return AutoControlSiteContext{
		SiteControlMode: SiteControlModeRemoteAuto,
		DeviceIDs:       []string{"01990000-6000-7000-8000-000000000001"},
		InputMapping: InputMapping{
			SupplyTemperatureKey: "supply_water_temperature",
			ZoneTemperatureKeys:  []string{"zone_temperature_a"},
		},
		SetpointTarget: &SetpointCommandTarget{
			DeviceID:         "01990000-6000-7000-8000-000000000001",
			PointID:          "01990000-7000-7000-8000-000000000001",
			PointCode:        "chw_sp",
			FeedbackPointKey: "chilled_water_supply_temp_sp",
			AssetID:          "01990000-4000-7000-8000-000000000001",
			SiteControlMode:  SiteControlModeRemoteAuto,
		},
		Topology: StagingTopology{SiteControlMode: SiteControlModeRemoteAuto},
	}
}

func testLivePoints(now time.Time, supplyC, setpointC, zoneC float64) []TelemetryLivePoint {
	devID := "01990000-6000-7000-8000-000000000001"
	sampledAt := now.Add(-15 * time.Second).UTC()
	receivedAt := now.Add(-14 * time.Second).UTC()
	return []TelemetryLivePoint{
		{
			DeviceID:         devID,
			TelemetryKey:     "supply_water_temperature",
			NumericValue:     float64Ptr(supplyC),
			Unit:             "Cel",
			Quality:          "GOOD",
			Freshness:        "FRESH",
			DevicePresence:   "ONLINE",
			BusinessRevision: 101,
			PointRevision:    42,
			SampledAt:        sampledAt,
			ReceivedAt:       receivedAt,
		},
		{
			DeviceID:         devID,
			TelemetryKey:     "chilled_water_supply_temp_sp",
			NumericValue:     float64Ptr(setpointC),
			Unit:             "Cel",
			Quality:          "GOOD",
			Freshness:        "FRESH",
			DevicePresence:   "ONLINE",
			BusinessRevision: 202,
			PointRevision:    43,
			SampledAt:        sampledAt,
			ReceivedAt:       receivedAt,
		},
		{
			DeviceID:         devID,
			TelemetryKey:     "zone_temperature_a",
			NumericValue:     float64Ptr(zoneC),
			Unit:             "Cel",
			Quality:          "GOOD",
			Freshness:        "FRESH",
			DevicePresence:   "ONLINE",
			BusinessRevision: 303,
			PointRevision:    12,
			SampledAt:        sampledAt,
			ReceivedAt:       receivedAt,
		},
		{
			DeviceID:         devID,
			TelemetryKey:     "fault_code",
			TextValue:        strPtr("0"),
			Quality:          "GOOD",
			Freshness:        "FRESH",
			DevicePresence:   "ONLINE",
			BusinessRevision: 404,
			PointRevision:    13,
			SampledAt:        sampledAt,
			ReceivedAt:       receivedAt,
		},
		{
			DeviceID:         devID,
			TelemetryKey:     "local_remote_mode",
			TextValue:        strPtr("REMOTE"),
			Quality:          "GOOD",
			Freshness:        "FRESH",
			DevicePresence:   "ONLINE",
			BusinessRevision: 505,
			PointRevision:    14,
			SampledAt:        sampledAt,
			ReceivedAt:       receivedAt,
		},
		{
			DeviceID:         devID,
			TelemetryKey:     "chiller_run_state",
			TextValue:        strPtr("RUNNING"),
			Quality:          "GOOD",
			Freshness:        "FRESH",
			DevicePresence:   "ONLINE",
			BusinessRevision: 606,
			PointRevision:    15,
			SampledAt:        sampledAt,
			ReceivedAt:       receivedAt,
		},
	}
}

func mutateLivePoint(points []TelemetryLivePoint, key string, fn func(*TelemetryLivePoint)) []TelemetryLivePoint {
	out := append([]TelemetryLivePoint(nil), points...)
	for idx := range out {
		if out[idx].TelemetryKey == key {
			fn(&out[idx])
		}
	}
	return out
}

func testLiveObservation(now time.Time, supplyC, zoneC float64) AutoControlLiveObservation {
	return AutoControlLiveObservation{
		SupplyTemp: &TelemetryEvidence{
			PointRevision: 42,
			SampledAt:     now.Add(-30 * time.Second),
			Value:         supplyC,
			Quality:       "GOOD",
		},
		ZoneTemps: map[string]TelemetryEvidence{
			"zone_temperature_a": {
				PointRevision: 12,
				SampledAt:     now.Add(-30 * time.Second),
				Value:         zoneC,
				Quality:       "GOOD",
			},
		},
		RunningChillers: 2,
		ChillersHealthy: true,
	}
}

func testAutoRecommendation(createdAt time.Time) Recommendation {
	validUntil := createdAt.Add(intelligencemodel.DefaultRecommendationTTL)
	return Recommendation{
		ID:                 "01990000-2000-7000-8000-000000000001",
		TenantID:           "01990000-3000-7000-8000-000000000001",
		SiteID:             "01990000-5000-7000-8000-000000000001",
		InputSnapshotID:    "01990000-1930-7000-8000-000000000001",
		DeploymentRevision: "01990000-1960-7000-8000-000000000001",
		Approval:           intelligencemodel.RecommendationDraft,
		Baseline: intelligencemodel.RecommendationBaseline{
			DailyEnergyKWh: 2400, DailyCost: 360, SupplyTempC: 7.0, ZoneTempC: 23.0,
		},
		Objective: intelligencemodel.RecommendationObjective{Kind: "COST"},
		Constraints: []intelligencemodel.RecommendationConstraint{
			{Kind: "SUPPLY_TEMP", MinC: float64Ptr(6.0)},
		},
		Candidate: intelligencemodel.RecommendationCandidate{
			SupplyTempC: 8.0, ProjectedZoneTempC: 23.3, DailyEnergyKWh: 2220, DailyCost: 333,
		},
		ExpectedImpact:   intelligencemodel.RecommendationExpectedImpact{EnergySavingKWhPerDay: 180.0, CostSavingPerDay: 27.0},
		Uncertainty:      intelligencemodel.RecommendationUncertainty{EnergyP90KWh: 60.0, ZoneTempP90C: 0.2},
		Risk:             intelligencemodel.RecommendationRisk{Level: "LOW", ComfortMarginC: 1.5},
		RollbackPlan:     intelligencemodel.RecommendationRollbackPlan{RestoreSupplyTempC: float64Ptr(7.0)},
		VerificationPlan: intelligencemodel.RecommendationVerificationPlan{WindowMinutes: 30},
		CreatedAt:        createdAt,
		ValidUntil:       &validUntil,
	}
}

func TestAutoControllerTryDispatchPreconditionsAndHappyPath(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)

	t.Run("default MANUAL_CONFIRM skips when no active lease", func(t *testing.T) {
		store := &fakeAutoControlStore{siteCtx: testAutoSiteCtx()}
		disp := &fakeDispatcher{}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(now, 7.2, 7.0, 23.0)})
		out, err := ctrl.TryDispatch(context.Background(), testAutoRecommendation(now.Add(-5*time.Minute)), now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Dispatched || out.SkipReason != "MANUAL_CONFIRM" || len(disp.submitInputs) != 0 {
			t.Fatalf("unexpected outcome: %+v", out)
		}
	})

	cases := []struct {
		name       string
		mutateRec  func(*Recommendation)
		mutateCtx  func(*AutoControlSiteContext)
		mutateObs  func(*AutoControlLiveObservation)
		mutatePts  func([]TelemetryLivePoint) []TelemetryLivePoint
		healthErr  error
		wantReason string
	}{
		{
			name: "expired 65m recommendation rejected",
			mutateRec: func(rec *Recommendation) {
				expired := now.Add(-time.Second)
				rec.ValidUntil = &expired
			},
			wantReason: "RECOMMENDATION_EXPIRED",
		},
		{
			name: "supply drift above 0.5C rejected",
			mutatePts: func(pts []TelemetryLivePoint) []TelemetryLivePoint {
				return mutateLivePoint(pts, "supply_water_temperature", func(pt *TelemetryLivePoint) {
					pt.NumericValue = float64Ptr(7.6)
				})
			},
			wantReason: "SUPPLY_TEMP_DRIFT_EXCEEDED",
		},
		{
			name: "chiller rated power above 90 percent rejected",
			mutateObs: func(obs *AutoControlLiveObservation) {
				obs.ChillerPowerTrip = &ChillerPowerViolation{AssetCode: "chiller-a", PowerKW: 120, RatedPowerKW: 127.5}
			},
			wantReason: "CHILLER_POWER_RATIO_EXCEEDED",
		},
		{
			name: "rate limit reached rejected",
			mutateCtx: func(ctx *AutoControlSiteContext) {
				ctx.RecentAutoCountHour = 2
			},
			wantReason: "RATE_LIMIT_REACHED",
		},
		{
			name: "active monitor exists rejected",
			mutateCtx: func(ctx *AutoControlSiteContext) {
				ctx.HasActiveMonitor = true
			},
			wantReason: "ACTIVE_MONITOR_EXISTS",
		},
		{
			name: "asset lock rejected",
			mutateCtx: func(ctx *AutoControlSiteContext) {
				ctx.SetpointTarget.AssetLocked = true
			},
			wantReason: "ASSET_CONTROL_LOCKED",
		},
		{
			name: "site exit mode rejected",
			mutateCtx: func(ctx *AutoControlSiteContext) {
				ctx.SiteControlMode = "MANUAL_OVERRIDE"
			},
			wantReason: "SITE_CONTROL_EXIT_ACTIVE",
		},
		{
			name:       "unhealthy telemetry rejected",
			healthErr:  errors.New("PROBE_OFFSET"),
			wantReason: "TELEMETRY_UNHEALTHY",
		},
		{
			name: "zero pointRevision fails closed",
			mutatePts: func(pts []TelemetryLivePoint) []TelemetryLivePoint {
				return mutateLivePoint(pts, "chilled_water_supply_temp_sp", func(pt *TelemetryLivePoint) {
					pt.PointRevision = 0
				})
			},
			wantReason: "TELEMETRY_REVALIDATION_FAILED",
		},
		{
			name: "zero businessRevision fails closed",
			mutatePts: func(pts []TelemetryLivePoint) []TelemetryLivePoint {
				return mutateLivePoint(pts, "chilled_water_supply_temp_sp", func(pt *TelemetryLivePoint) {
					pt.BusinessRevision = 0
				})
			},
			wantReason: "TELEMETRY_REVALIDATION_FAILED",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := testAutoRecommendation(now.Add(-5 * time.Minute))
			siteCtx := testAutoSiteCtx()
			obs := testLiveObservation(now, 7.2, 23.0)
			pts := testLivePoints(now, 7.2, 7.0, 23.0)
			if tc.mutateRec != nil {
				tc.mutateRec(&rec)
			}
			if tc.mutateCtx != nil {
				tc.mutateCtx(&siteCtx)
			}
			if tc.mutateObs != nil {
				tc.mutateObs(&obs)
			}
			if tc.mutatePts != nil {
				pts = tc.mutatePts(pts)
			}
			store := &fakeAutoControlStore{
				lease:   testAutoLease(now),
				siteCtx: siteCtx,
			}
			disp := &fakeDispatcher{}
			ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, func() time.Time { return now }, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctrl.WithPointHealth(&fakePointHealthChecker{err: tc.healthErr, livePoints: pts})
			out, err := ctrl.TryDispatch(context.Background(), rec, now)
			if err != nil {
				t.Fatal(err)
			}
			if out.Dispatched || out.SkipReason != tc.wantReason {
				t.Fatalf("got outcome=%+v want SkipReason=%s", out, tc.wantReason)
			}
			if len(disp.submitInputs) != 0 {
				t.Fatalf("expected no submitted commands on %s", tc.wantReason)
			}
		})
	}

	t.Run("happy path uses real live telemetry, pre-registers PENDING_SUBMIT, submits and activates OBSERVING monitor", func(t *testing.T) {
		store := &fakeAutoControlStore{
			lease:   testAutoLease(now),
			siteCtx: testAutoSiteCtx(),
		}
		pts := testLivePoints(now, 7.2, 7.0, 23.5)
		obs := testLiveObservation(now, 7.2, 23.5)
		var observedPendingBeforeSubmit bool
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "header.sig",
			commandID: "01990000-7700-7000-8000-000000000099",
			submitHook: func(context.Context, AutoControlDispatchInput) {
				store.mu.Lock()
				defer store.mu.Unlock()
				if len(store.monitors) == 1 && store.monitors[0].Status == ExecutionMonitorPendingSubmit && store.monitors[0].CommandIntentID == "" {
					observedPendingBeforeSubmit = true
				}
			},
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: pts})
		rec := testAutoRecommendation(now.Add(-5 * time.Minute))
		out, err := ctrl.TryDispatch(context.Background(), rec, now)
		if err != nil {
			t.Fatal(err)
		}
		if !observedPendingBeforeSubmit {
			t.Fatal("expected PENDING_SUBMIT monitor row with empty commandIntentID before HTTP SubmitAutoCommand")
		}
		if !out.Dispatched || out.CommandIntentID != disp.commandID || out.MonitorID == "" {
			t.Fatalf("expected dispatch outcome, got %+v", out)
		}
		if len(disp.submitInputs) != 1 {
			t.Fatalf("expected 1 submitted command, got %d", len(disp.submitInputs))
		}
		sub := disp.submitInputs[0]
		if sub.IdempotencyKey != "auto-opt:"+rec.ID || sub.IsRollback ||
			sub.BusinessRevision != 202 || sub.Quality != "GOOD" || sub.Presence != "ONLINE" ||
			sub.Readiness != "CURRENT" || sub.EvaluationAvailability != "AVAILABLE" ||
			sub.CurrentValue == nil || *sub.CurrentValue != 7.0 || !sub.ObservedAt.Equal(now.Add(-15*time.Second)) {
			t.Fatalf("unexpected real-telemetry command submit payload: %+v", sub)
		}
		if len(store.dispatchedRecs) != 1 || store.dispatchedRecs[0].Approval != intelligencemodel.RecommendationApproved || store.dispatchedRecs[0].Revalidation == nil {
			t.Fatalf("expected revalidated APPROVED recommendation, got %+v", store.dispatchedRecs)
		}
		if err := store.dispatchedRecs[0].CanCreateCommand(now); err != nil {
			t.Fatalf("recorded recommendation failed CanCreateCommand: %v", err)
		}
		if len(store.monitors) != 1 || store.monitors[0].Status != ExecutionMonitorObserving ||
			store.monitors[0].CommandIntentID != disp.commandID ||
			store.monitors[0].SupplyTempMinC != 6.0 ||
			store.monitors[0].WindowEndsAt.Sub(store.monitors[0].StartedAt) != DefaultExecutionMonitorWindow {
			t.Fatalf("expected 30m OBSERVING monitor, got %+v", store.monitors)
		}
	})
}

func TestAutoControllerSafetyGuardrails(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)

	t.Run("clamps proposed setpoint below 5.0C up to hard floor 5.0C and records raw and clamped values", func(t *testing.T) {
		lease := testAutoLease(now)
		lease.SupplyTempMinC = 4.0
		lease.MaxSetpointStepC = 2.0
		store := &fakeAutoControlStore{lease: lease, siteCtx: testAutoSiteCtx()}
		pts := testLivePoints(now, 6.2, 6.0, 24.0)
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "header.sig",
			commandID: "01990000-7700-7000-8000-000000000101",
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: pts})

		rec := testAutoRecommendation(now.Add(-5 * time.Minute))
		rec.Baseline.SupplyTempC = 6.0
		rec.Candidate.SupplyTempC = 4.2
		out, err := ctrl.TryDispatch(context.Background(), rec, now)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Dispatched || len(disp.submitInputs) != 1 {
			t.Fatalf("expected clamped dispatch, got outcome=%+v submits=%d", out, len(disp.submitInputs))
		}
		if got := disp.submitInputs[0].Parameters[commandmodel.ParameterSetpointC]; got != 5.0 {
			t.Fatalf("dispatched setpoint=%v, want 5.0", got)
		}
		mon := store.monitors[0]
		if mon.SupplyTempMinC != 5.0 ||
			mon.RawRecommendedValue == nil || *mon.RawRecommendedValue != 4.2 ||
			mon.ClampedValue == nil || *mon.ClampedValue != 5.0 ||
			mon.AppliedSupplyTempC == nil || *mon.AppliedSupplyTempC != 5.0 ||
			mon.GuardrailVerdict != string(commandmodel.GuardrailVerdictClamped) ||
			len(mon.GuardrailTrace) == 0 {
			t.Fatalf("unexpected clamped monitor metadata: %+v", mon)
		}
	})

	t.Run("clamps setpoint exceeding max step slew limit", func(t *testing.T) {
		lease := testAutoLease(now)
		lease.MaxSetpointStepC = 1.5
		store := &fakeAutoControlStore{lease: lease, siteCtx: testAutoSiteCtx()}
		pts := testLivePoints(now, 7.1, 7.0, 23.0)
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "header.sig",
			commandID: "01990000-7700-7000-8000-000000000102",
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: pts})

		rec := testAutoRecommendation(now.Add(-5 * time.Minute))
		rec.Candidate.SupplyTempC = 10.0
		out, err := ctrl.TryDispatch(context.Background(), rec, now)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Dispatched || len(disp.submitInputs) != 1 {
			t.Fatalf("expected slew-clamped dispatch, got outcome=%+v", out)
		}
		if got := disp.submitInputs[0].Parameters[commandmodel.ParameterSetpointC]; got != 8.5 {
			t.Fatalf("slew-clamped setpoint=%v, want 8.5", got)
		}
		mon := store.monitors[0]
		if mon.RawRecommendedValue == nil || *mon.RawRecommendedValue != 10.0 ||
			mon.ClampedValue == nil || *mon.ClampedValue != 8.5 ||
			mon.GuardrailVerdict != string(commandmodel.GuardrailVerdictClamped) {
			t.Fatalf("unexpected slew-clamped monitor metadata: %+v", mon)
		}
	})

	t.Run("skips cleanly within deadband without error or command", func(t *testing.T) {
		lease := testAutoLease(now)
		lease.DeadbandC = 0.2
		store := &fakeAutoControlStore{lease: lease, siteCtx: testAutoSiteCtx()}
		pts := testLivePoints(now, 7.0, 7.0, 23.0)
		disp := &fakeDispatcher{}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: pts})

		rec := testAutoRecommendation(now.Add(-5 * time.Minute))
		rec.Candidate.SupplyTempC = 7.1
		out, err := ctrl.TryDispatch(context.Background(), rec, now)
		if err != nil {
			t.Fatalf("deadband skip must not return error: %v", err)
		}
		if out.Dispatched || out.SkipReason == "" || len(disp.submitInputs) != 0 || len(store.monitors) != 0 {
			t.Fatalf("expected clean deadband skip, got out=%+v submits=%d monitors=%d", out, len(disp.submitInputs), len(store.monitors))
		}
	})

	t.Run("vetoes stale telemetry over 90 seconds", func(t *testing.T) {
		store := &fakeAutoControlStore{lease: testAutoLease(now), siteCtx: testAutoSiteCtx()}
		pts := mutateLivePoint(testLivePoints(now, 7.1, 7.0, 23.0), "supply_water_temperature", func(pt *TelemetryLivePoint) {
			pt.SampledAt = now.Add(-120 * time.Second)
		})
		disp := &fakeDispatcher{}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: pts})

		out, err := ctrl.TryDispatch(context.Background(), testAutoRecommendation(now.Add(-5*time.Minute)), now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Dispatched || out.SkipReason == "" || len(disp.submitInputs) != 0 {
			t.Fatalf("expected stale telemetry veto, got %+v", out)
		}
	})

	t.Run("vetoes active fault code, cabinet local manual, and device offline", func(t *testing.T) {
		vetoCases := []struct {
			name      string
			mutatePts func([]TelemetryLivePoint) []TelemetryLivePoint
		}{
			{
				name: "active fault_code string",
				mutatePts: func(pts []TelemetryLivePoint) []TelemetryLivePoint {
					return mutateLivePoint(pts, "fault_code", func(pt *TelemetryLivePoint) {
						pt.TextValue = strPtr("16")
					})
				},
			},
			{
				name: "cabinet local_remote_mode LOCAL",
				mutatePts: func(pts []TelemetryLivePoint) []TelemetryLivePoint {
					return mutateLivePoint(pts, "local_remote_mode", func(pt *TelemetryLivePoint) {
						pt.TextValue = strPtr("LOCAL")
					})
				},
			},
			{
				name: "device offline",
				mutatePts: func(pts []TelemetryLivePoint) []TelemetryLivePoint {
					return mutateLivePoint(pts, "chilled_water_supply_temp_sp", func(pt *TelemetryLivePoint) {
						pt.DevicePresence = "OFFLINE"
					})
				},
			},
		}

		for _, tc := range vetoCases {
			t.Run(tc.name, func(t *testing.T) {
				store := &fakeAutoControlStore{lease: testAutoLease(now), siteCtx: testAutoSiteCtx()}
				pts := tc.mutatePts(testLivePoints(now, 7.1, 7.0, 23.0))
				disp := &fakeDispatcher{}
				ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
				if err != nil {
					t.Fatal(err)
				}
				ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: pts})

				out, err := ctrl.TryDispatch(context.Background(), testAutoRecommendation(now.Add(-5*time.Minute)), now)
				if err != nil {
					t.Fatal(err)
				}
				if out.Dispatched || out.SkipReason == "" || len(disp.submitInputs) != 0 {
					t.Fatalf("expected veto for %s, got %+v", tc.name, out)
				}
			})
		}
	})

	t.Run("vetoes dispatch while manual_backoff_until is active and resumes after expiry", func(t *testing.T) {
		backoffUntil := now.Add(2 * time.Hour)
		lease := testAutoLease(now)
		lease.ManualBackoffUntil = &backoffUntil
		lease.ManualBackoffReason = "cabinet_setpoint_manual_override"

		store := &fakeAutoControlStore{lease: lease, siteCtx: testAutoSiteCtx()}
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "header.sig",
			commandID: "01990000-7700-7000-8000-000000000103",
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(now, 7.1, 7.0, 23.0)})

		out, err := ctrl.TryDispatch(context.Background(), testAutoRecommendation(now.Add(-5*time.Minute)), now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Dispatched || out.SkipReason == "" || len(disp.submitInputs) != 0 {
			t.Fatalf("expected manual backoff veto, got %+v", out)
		}

		afterBackoff := backoffUntil.Add(time.Minute)
		lease.ExpiresAt = afterBackoff.Add(2 * time.Hour)
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(afterBackoff, 7.1, 7.0, 23.0)})
		recAfter := testAutoRecommendation(afterBackoff.Add(-5 * time.Minute))
		outAfter, err := ctrl.TryDispatch(context.Background(), recAfter, afterBackoff)
		if err != nil {
			t.Fatal(err)
		}
		if !outAfter.Dispatched {
			t.Fatalf("expected dispatch after manual backoff expiry, got %+v", outAfter)
		}
	})

	t.Run("split-brain submit failure marks monitor SUBMIT_FAILED instead of leaving PENDING_SUBMIT wedged", func(t *testing.T) {
		store := &fakeAutoControlStore{lease: testAutoLease(now), siteCtx: testAutoSiteCtx()}
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "header.sig",
			submitErr: context.DeadlineExceeded,
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(now, 7.1, 7.0, 23.0)})

		expiredCtx, cancel := context.WithCancel(context.Background())
		disp.submitHook = func(context.Context, AutoControlDispatchInput) {
			cancel()
		}

		out, err := ctrl.TryDispatch(expiredCtx, testAutoRecommendation(now.Add(-5*time.Minute)), now)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected submit deadline error, got %v", err)
		}
		if out.Dispatched || out.MonitorID == "" {
			t.Fatalf("unexpected outcome on submit failure: %+v", out)
		}
		if store.submitFailedCalls != 1 || len(store.monitors) != 1 ||
			store.monitors[0].Status != ExecutionMonitorSubmitFailed ||
			store.monitors[0].CommandIntentID != "" ||
			store.monitors[0].TripReason == nil || *store.monitors[0].TripReason == "" ||
			len(store.dispatchedRecs) != 0 {
			t.Fatalf("expected monitor transitioned to SUBMIT_FAILED without approving rec, got monitors=%+v recs=%d", store.monitors, len(store.dispatchedRecs))
		}
	})

	t.Run("concurrent TryDispatch prevents hourly rate-limit bypass", func(t *testing.T) {
		lease := testAutoLease(now)
		lease.MaxCommandsPerHour = 1
		store := &fakeAutoControlStore{lease: lease, siteCtx: testAutoSiteCtx()}
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "header.sig",
			commandID: "01990000-7700-7000-8000-000000000104",
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(now, 7.1, 7.0, 23.0)})

		rec1 := testAutoRecommendation(now.Add(-5 * time.Minute))
		rec2 := testAutoRecommendation(now.Add(-5 * time.Minute))
		rec2.ID = "01990000-2000-7000-8000-000000000002"

		start := make(chan struct{})
		var wg sync.WaitGroup
		outcomes := make([]AutoDispatchOutcome, 2)
		errs := make([]error, 2)
		for idx, rec := range []Recommendation{rec1, rec2} {
			wg.Add(1)
			go func(i int, r Recommendation) {
				defer wg.Done()
				<-start
				outcomes[i], errs[i] = ctrl.TryDispatch(context.Background(), r, now)
			}(idx, rec)
		}
		close(start)
		wg.Wait()

		for i, runErr := range errs {
			if runErr != nil {
				t.Fatalf("goroutine %d err=%v", i, runErr)
			}
		}
		dispatchedCount := 0
		for _, out := range outcomes {
			if out.Dispatched {
				dispatchedCount++
			}
		}
		if dispatchedCount != 1 || len(disp.submitInputs) != 1 {
			t.Fatalf("expected exactly 1 concurrent dispatch, outcomes=%+v submits=%d", outcomes, len(disp.submitInputs))
		}
	})
}

func TestAutoControllerRunMonitorPass(t *testing.T) {
	base := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)

	t.Run("completes VERIFIED after 30m healthy window", func(t *testing.T) {
		applied := 8.0
		restore := 7.0
		store := &fakeAutoControlStore{
			lease:   testAutoLease(base),
			siteCtx: testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{{
				ID:                 "01990000-9900-7000-8000-000000000001",
				TenantID:           "01990000-3000-7000-8000-000000000001",
				SiteID:             "01990000-5000-7000-8000-000000000001",
				LeaseID:            "01990000-8800-7000-8000-000000000011",
				DeviceID:           "01990000-6000-7000-8000-000000000001",
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: &applied,
				RestoreSupplyTempC: &restore,
				ZoneTempMaxC:       26.0,
				SupplyTempMinC:     6.0,
				SupplyTempMaxC:     12.0,
				StartedAt:          base,
				WindowEndsAt:       base.Add(30 * time.Minute),
				Status:             ExecutionMonitorObserving,
			}},
		}
		checkAt := base.Add(30 * time.Minute)
		obs := testLiveObservation(checkAt, 8.0, 24.0)
		disp := &fakeDispatcher{}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(checkAt, 8.0, 8.0, 24.0)})
		summary, err := ctrl.RunMonitorPass(context.Background(), checkAt)
		if err != nil {
			t.Fatal(err)
		}
		if summary.Verified != 1 || summary.RolledBack != 0 || store.monitors[0].Status != ExecutionMonitorVerified || store.lease.Status != "ACTIVE" {
			t.Fatalf("unexpected verify summary=%+v monitor=%+v lease=%+v", summary, store.monitors[0], store.lease)
		}
	})

	violationCases := []struct {
		name       string
		supplyC    float64
		zoneC      float64
		powerTrip  *ChillerPowerViolation
		wantPrefix string
	}{
		{name: "supply below min freeze protection triggers rollback and trip", supplyC: 5.5, zoneC: 24.0, wantPrefix: "supply_temperature_below_min"},
		{name: "zone over limit triggers rollback and trip", supplyC: 8.0, zoneC: 26.4, wantPrefix: "zone_temperature_exceeded_max"},
		{name: "supply over limit triggers rollback and trip", supplyC: 12.4, zoneC: 24.0, wantPrefix: "supply_temperature_exceeded_max"},
		{name: "chiller power over 90pct triggers rollback and trip", supplyC: 8.0, zoneC: 24.0, powerTrip: &ChillerPowerViolation{AssetCode: "chiller-a", PowerKW: 120, RatedPowerKW: 127.5}, wantPrefix: "chiller_power_exceeded_90pct"},
	}

	for _, tc := range violationCases {
		t.Run(tc.name, func(t *testing.T) {
			applied := 8.0
			restore := 7.0
			monID := "01990000-9900-7000-8000-000000000009"
			store := &fakeAutoControlStore{
				lease:   testAutoLease(base),
				siteCtx: testAutoSiteCtx(),
				monitors: []OptimizationExecutionMonitorRecord{{
					ID:                 monID,
					TenantID:           "01990000-3000-7000-8000-000000000001",
					SiteID:             "01990000-5000-7000-8000-000000000001",
					LeaseID:            "01990000-8800-7000-8000-000000000011",
					DeviceID:           "01990000-6000-7000-8000-000000000001",
					TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
					AppliedSupplyTempC: &applied,
					RestoreSupplyTempC: &restore,
					ZoneTempMaxC:       26.0,
					SupplyTempMinC:     6.0,
					SupplyTempMaxC:     12.0,
					StartedAt:          base,
					WindowEndsAt:       base.Add(30 * time.Minute),
					Status:             ExecutionMonitorObserving,
				}},
			}
			checkAt := base.Add(10 * time.Minute)
			obs := testLiveObservation(checkAt, tc.supplyC, tc.zoneC)
			obs.ChillerPowerTrip = tc.powerTrip
			disp := &fakeDispatcher{
				decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
				grant:     "rollback.sig",
				commandID: "01990000-7700-7000-8000-000000000111",
			}
			ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(checkAt, tc.supplyC, applied, tc.zoneC)})
			summary, err := ctrl.RunMonitorPass(context.Background(), checkAt)
			if err != nil {
				t.Fatal(err)
			}
			if summary.RolledBack != 1 || store.lease.Status != "TRIPPED" || store.monitors[0].Status != ExecutionMonitorTrippedRolledBack {
				t.Fatalf("expected rollback and tripped lease, summary=%+v lease=%+v mon=%+v", summary, store.lease, store.monitors[0])
			}
			if len(disp.submitInputs) != 1 || !disp.submitInputs[0].IsRollback || disp.submitInputs[0].IdempotencyKey != "auto-rollback:"+monID {
				t.Fatalf("unexpected rollback submit payload: %+v", disp.submitInputs)
			}
			if got := disp.submitInputs[0].Parameters[commandmodel.ParameterSetpointC]; got != restore {
				t.Fatalf("rollback setpoint=%v want %v", got, restore)
			}
			if len(store.trippedRecords) != 1 || !strings.HasPrefix(store.trippedRecords[0].TripReason, tc.wantPrefix) {
				t.Fatalf("unexpected trip records: %+v", store.trippedRecords)
			}
		})
	}

	t.Run("telemetry unhealthy over 5m trips and sends emergency bypass rollback with real degraded telemetry", func(t *testing.T) {
		applied := 8.0
		restore := 7.0
		monID := "01990000-9900-7000-8000-000000000010"
		store := &fakeAutoControlStore{
			lease:   testAutoLease(base),
			siteCtx: testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{{
				ID:                 monID,
				TenantID:           "01990000-3000-7000-8000-000000000001",
				SiteID:             "01990000-5000-7000-8000-000000000001",
				LeaseID:            "01990000-8800-7000-8000-000000000011",
				DeviceID:           "01990000-6000-7000-8000-000000000001",
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: &applied,
				RestoreSupplyTempC: &restore,
				ZoneTempMaxC:       26.0,
				SupplyTempMinC:     6.0,
				SupplyTempMaxC:     12.0,
				StartedAt:          base,
				WindowEndsAt:       base.Add(30 * time.Minute),
				Status:             ExecutionMonitorObserving,
			}},
		}
		degradedPts := mutateLivePoint(testLivePoints(base, 8.0, 8.0, 24.0), "chilled_water_supply_temp_sp", func(pt *TelemetryLivePoint) {
			pt.DevicePresence = "OFFLINE"
			pt.Quality = "BAD"
			pt.Freshness = "STALE"
			pt.BusinessRevision = 0
		})
		healthChecker := &fakePointHealthChecker{err: errors.New("OFFLINE"), livePoints: degradedPts}
		reader := &fakeTelemetryReader{obs: testLiveObservation(base, 8.0, 24.0)}
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "rollback.sig",
			commandID: "01990000-7700-7000-8000-000000000222",
		}
		ctrl, err := NewAutoController(store, reader, disp, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(healthChecker)

		// T+2m: first unhealthy sample sets TelemetryUnhealthySince, no trip yet.
		if _, err := ctrl.RunMonitorPass(context.Background(), base.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if store.monitors[0].TelemetryUnhealthySince == nil || store.lease.Status != "ACTIVE" {
			t.Fatalf("expected unhealthy timestamp recorded without tripping: %+v", store.monitors[0])
		}

		// T+4m: telemetry recovers -> clears TelemetryUnhealthySince.
		healthChecker.setErr(nil)
		healthChecker.setLivePoints(testLivePoints(base.Add(4*time.Minute), 8.0, 8.0, 24.0))
		if _, err := ctrl.RunMonitorPass(context.Background(), base.Add(4*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if store.monitors[0].TelemetryUnhealthySince != nil || store.heartbeatCalls != 1 {
			t.Fatalf("expected unhealthy marker cleared on recovery: %+v", store.monitors[0])
		}

		// T+5m then T+11m: unhealthy for >5m triggers rollback + trip with EMERGENCY_BYPASS and real telemetry.
		healthChecker.setErr(errors.New("OFFLINE"))
		healthChecker.setLivePoints(degradedPts)
		if _, err := ctrl.RunMonitorPass(context.Background(), base.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		summary, err := ctrl.RunMonitorPass(context.Background(), base.Add(11*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if summary.RolledBack != 1 || store.lease.Status != "TRIPPED" || len(disp.submitInputs) != 1 || !disp.submitInputs[0].IsRollback {
			t.Fatalf("expected rollback on >5m unhealthy telemetry, summary=%+v disp=%+v", summary, disp.submitInputs)
		}
		sub := disp.submitInputs[0]
		if sub.PreconditionMode != commandmodel.PreconditionEmergencyBypass ||
			sub.BypassReason != "automated_execution_monitor_rollback:telemetry_unhealthy_over_5m" ||
			sub.Quality == "GOOD" || sub.Presence == "ONLINE" || sub.BusinessRevision != 0 {
			t.Fatalf("rollback must pass real degraded telemetry with EMERGENCY_BYPASS without fabricating GOOD/1: %+v", sub)
		}
	})

	t.Run("rollback anchors risk delta on applied setpoint when live supply drifted over 3C", func(t *testing.T) {
		applied := 8.5
		restore := 7.0
		monID := "01990000-9900-7000-8000-000000000012"
		store := &fakeAutoControlStore{
			lease:   testAutoLease(base),
			siteCtx: testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{{
				ID:                 monID,
				TenantID:           "01990000-3000-7000-8000-000000000001",
				SiteID:             "01990000-5000-7000-8000-000000000001",
				LeaseID:            "01990000-8800-7000-8000-000000000011",
				DeviceID:           "01990000-6000-7000-8000-000000000001",
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: &applied,
				RestoreSupplyTempC: &restore,
				ZoneTempMaxC:       26.0,
				SupplyTempMinC:     6.0,
				SupplyTempMaxC:     11.0,
				StartedAt:          base,
				WindowEndsAt:       base.Add(30 * time.Minute),
				Status:             ExecutionMonitorObserving,
			}},
		}
		checkAt := base.Add(8 * time.Minute)
		obs := testLiveObservation(checkAt, 11.4, 24.0)
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "rollback.sig",
			commandID: "01990000-7700-7000-8000-000000000444",
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(checkAt, 11.4, applied, 24.0)})
		summary, err := ctrl.RunMonitorPass(context.Background(), checkAt)
		if err != nil {
			t.Fatal(err)
		}
		if summary.RolledBack != 1 || len(disp.grantReqs) != 1 || disp.grantReqs[0].CurrentValue == nil {
			t.Fatalf("expected rollback grant request after drift, summary=%+v grantReqs=%+v", summary, disp.grantReqs)
		}
		if *disp.grantReqs[0].CurrentValue != applied {
			t.Fatalf("expected rollback currentValue anchored to applied setpoint %v, got %v", applied, *disp.grantReqs[0].CurrentValue)
		}
		if len(disp.submitInputs) != 1 || disp.submitInputs[0].CurrentValue == nil || *disp.submitInputs[0].CurrentValue != applied || disp.submitInputs[0].Parameters[commandmodel.ParameterSetpointC] != restore {
			t.Fatalf("unexpected rollback command submit payload: %+v", disp.submitInputs)
		}
	})

	t.Run("cabinet manual setpoint tamper sets 2h manual_backoff_until and marks MANUAL_OVERRIDE without rollback", func(t *testing.T) {
		applied := 8.0
		restore := 7.0
		monID := "01990000-9900-7000-8000-000000000015"
		newMonitor := func() OptimizationExecutionMonitorRecord {
			return OptimizationExecutionMonitorRecord{
				ID:                 monID,
				TenantID:           "01990000-3000-7000-8000-000000000001",
				SiteID:             "01990000-5000-7000-8000-000000000001",
				LeaseID:            "01990000-8800-7000-8000-000000000011",
				DeviceID:           "01990000-6000-7000-8000-000000000001",
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: &applied,
				RestoreSupplyTempC: &restore,
				ZoneTempMaxC:       26.0,
				SupplyTempMinC:     6.0,
				SupplyTempMaxC:     12.0,
				StartedAt:          base,
				WindowEndsAt:       base.Add(30 * time.Minute),
				Status:             ExecutionMonitorObserving,
			}
		}

		// Subcase 1: setpoint still equals RestoreSupplyTempC (7.0) -> not tamper, normal heartbeat.
		check1 := base.Add(2 * time.Minute)
		store1 := &fakeAutoControlStore{
			lease:    testAutoLease(base),
			siteCtx:  testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{newMonitor()},
		}
		disp1 := &fakeDispatcher{}
		ctrl1, err := NewAutoController(store1, &fakeTelemetryReader{}, disp1, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl1.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(check1, 7.5, restore, 24.0)})
		summary1, err := ctrl1.RunMonitorPass(context.Background(), check1)
		if err != nil {
			t.Fatal(err)
		}
		if summary1.ManualOverrides != 0 || store1.heartbeatCalls != 1 || store1.monitors[0].Status != ExecutionMonitorObserving {
			t.Fatalf("setpoint equal to restore should not trigger manual override: summary=%+v mon=%+v", summary1, store1.monitors[0])
		}

		// Subcase 2: setpoint manually overridden on cabinet to 9.5 (>0.35C from 8.0 and != 7.0) -> MANUAL_OVERRIDE + 2h backoff, no rollback!
		check2 := base.Add(5 * time.Minute)
		store2 := &fakeAutoControlStore{
			lease:    testAutoLease(base),
			siteCtx:  testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{newMonitor()},
		}
		disp2 := &fakeDispatcher{}
		ctrl2, err := NewAutoController(store2, &fakeTelemetryReader{}, disp2, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl2.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(check2, 8.0, 9.5, 24.0)})
		summary2, err := ctrl2.RunMonitorPass(context.Background(), check2)
		if err != nil {
			t.Fatal(err)
		}
		if summary2.ManualOverrides != 1 || summary2.RolledBack != 0 || summary2.Tripped != 0 {
			t.Fatalf("unexpected summary on cabinet manual tamper: %+v", summary2)
		}
		if len(disp2.submitInputs) != 0 {
			t.Fatalf("expected NO rollback command when cabinet operator overrides setpoint, got %+v", disp2.submitInputs)
		}
		if store2.monitors[0].Status != ExecutionMonitorManualOverride ||
			store2.monitors[0].TripReason == nil || *store2.monitors[0].TripReason != "cabinet_setpoint_manual_override" {
			t.Fatalf("unexpected monitor state after manual override: %+v", store2.monitors[0])
		}
		if store2.lease.Status != "ACTIVE" ||
			store2.lease.ManualBackoffUntil == nil || !store2.lease.ManualBackoffUntil.Equal(check2.Add(2*time.Hour)) ||
			store2.lease.ManualBackoffReason != "cabinet_setpoint_manual_override" {
			t.Fatalf("expected active lease 2h backoff, got lease=%+v", store2.lease)
		}
	})
}

func TestWorkloadCommandClientHTTP(t *testing.T) {
	iamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != iamCommandWorkloadDecisionPath || r.Method != http.MethodPost {
			t.Fatalf("unexpected IAM path %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(commandauth.DecisionResponse{
			Decision:        commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			DelegationGrant: "signed.grant",
		})
	}))
	defer iamSrv.Close()

	var capturedBodies [][]byte
	var capturedTraceparents []string
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sites/01990000-5000-7000-8000-000000000001/optimization/auto-commands" {
			t.Fatalf("unexpected gateway path %s", r.URL.Path)
		}
		raw, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Fatalf("read gateway request body: %v", readErr)
		}
		capturedBodies = append(capturedBodies, raw)
		capturedTraceparents = append(capturedTraceparents, r.Header.Get("traceparent"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"commandId":"01990000-7700-7000-8000-000000000333"}`))
	}))
	defer gwSrv.Close()

	client, err := NewWorkloadCommandClient(WorkloadCommandClientConfig{
		IAMURL:        iamSrv.URL,
		GatewayURL:    gwSrv.URL,
		IAMClient:     iamSrv.Client(),
		GatewayClient: gwSrv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, decision, err := client.RequestCommandGrant(context.Background(), commandauth.WorkloadDecisionRequest{
		TenantID:   "01990000-3000-7000-8000-000000000001",
		SiteID:     "01990000-5000-7000-8000-000000000001",
		DeviceID:   "01990000-6000-7000-8000-000000000001",
		Capability: commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
	})
	if err != nil || !decision.Allowed || grant != "signed.grant" {
		t.Fatalf("RequestCommandGrant failed: grant=%q dec=%+v err=%v", grant, decision, err)
	}

	// Normal command with fabricated/missing state must fail closed.
	_, err = client.SubmitAutoCommand(context.Background(), grant, AutoControlDispatchInput{
		TenantID:             "01990000-3000-7000-8000-000000000001",
		SiteID:               "01990000-5000-7000-8000-000000000001",
		DeviceID:             "01990000-6000-7000-8000-000000000001",
		PointID:              "01990000-7000-7000-8000-000000000001",
		Capability:           commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.0},
		VerificationPointKey: "chilled_water_supply_temp_sp",
		IdempotencyKey:       "auto-opt:01990000-2000-7000-8000-000000000001",
		CurrentValue:         float64Ptr(7.0),
		BusinessRevision:     0,
		ObservedAt:           time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected SubmitAutoCommand without verified live telemetry fields to fail closed")
	}

	// Normal command must never use emergency bypass.
	_, err = client.SubmitAutoCommand(context.Background(), grant, AutoControlDispatchInput{
		TenantID:               "01990000-3000-7000-8000-000000000001",
		SiteID:                 "01990000-5000-7000-8000-000000000001",
		DeviceID:               "01990000-6000-7000-8000-000000000001",
		PointID:                "01990000-7000-7000-8000-000000000001",
		Capability:             commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		Parameters:             commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.0},
		VerificationPointKey:   "chilled_water_supply_temp_sp",
		IdempotencyKey:         "auto-opt:01990000-2000-7000-8000-000000000001",
		PreconditionMode:       commandmodel.PreconditionEmergencyBypass,
		BypassReason:           "should_be_rejected",
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Quality:                "GOOD",
		CurrentValue:           float64Ptr(7.0),
		BusinessRevision:       42,
		ObservedAt:             time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected normal SubmitAutoCommand with emergency bypass to fail closed")
	}

	traceIDNormal := "0123456789abcdef0123456789abcdef"
	cmdID, err := client.SubmitAutoCommand(context.Background(), grant, AutoControlDispatchInput{
		TenantID:               "01990000-3000-7000-8000-000000000001",
		SiteID:                 "01990000-5000-7000-8000-000000000001",
		DeviceID:               "01990000-6000-7000-8000-000000000001",
		PointID:                "01990000-7000-7000-8000-000000000001",
		Capability:             commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		Parameters:             commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.0},
		VerificationPointKey:   "chilled_water_supply_temp_sp",
		IdempotencyKey:         "auto-opt:01990000-2000-7000-8000-000000000001",
		PreconditionMode:       commandmodel.PreconditionRequireCurrentState,
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Quality:                "GOOD",
		CurrentValue:           float64Ptr(7.0),
		BusinessRevision:       42,
		ObservedAt:             time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC),
		TraceID:                traceIDNormal,
	})
	if err != nil || cmdID != "01990000-7700-7000-8000-000000000333" {
		t.Fatalf("SubmitAutoCommand failed: cmdID=%q err=%v", cmdID, err)
	}

	// Rollback with offline/bad telemetry sends real telemetry + EMERGENCY_BYPASS without fabricating GOOD/1.
	traceIDRollback := "fedcba9876543210fedcba9876543210"
	_, err = client.SubmitAutoCommand(context.Background(), grant, AutoControlDispatchInput{
		TenantID:               "01990000-3000-7000-8000-000000000001",
		SiteID:                 "01990000-5000-7000-8000-000000000001",
		DeviceID:               "01990000-6000-7000-8000-000000000001",
		PointID:                "01990000-7000-7000-8000-000000000001",
		Capability:             commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		Parameters:             commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 7.0},
		VerificationPointKey:   "chilled_water_supply_temp_sp",
		IdempotencyKey:         "auto-rollback:01990000-9900-7000-8000-000000000010",
		IsRollback:             true,
		PreconditionMode:       commandmodel.PreconditionEmergencyBypass,
		BypassReason:           "automated_execution_monitor_rollback:telemetry_unhealthy_over_5m",
		EvaluationAvailability: "UNAVAILABLE",
		Presence:               "OFFLINE",
		Readiness:              "DEGRADED",
		Quality:                "BAD",
		CurrentValue:           nil,
		BusinessRevision:       0,
		TraceID:                traceIDRollback,
	})
	if err != nil {
		t.Fatalf("SubmitAutoCommand rollback failed: %v", err)
	}
	if len(capturedBodies) != 2 || len(capturedTraceparents) != 2 {
		t.Fatalf("expected 2 captured HTTP payloads, got bodies=%d traceparents=%d", len(capturedBodies), len(capturedTraceparents))
	}
	if sc0, ok0 := observability.ParseTraceparent(capturedTraceparents[0], ""); !ok0 || sc0.TraceID != traceIDNormal {
		t.Fatalf("normal command traceparent=%q want traceID=%s", capturedTraceparents[0], traceIDNormal)
	}
	if sc1, ok1 := observability.ParseTraceparent(capturedTraceparents[1], ""); !ok1 || sc1.TraceID != traceIDRollback {
		t.Fatalf("rollback command traceparent=%q want traceID=%s", capturedTraceparents[1], traceIDRollback)
	}
	if strings.Contains(string(capturedBodies[0]), "preconditionMode") || strings.Contains(string(capturedBodies[0]), "bypassReason") {
		t.Fatalf("normal command payload should omit default preconditionMode/bypassReason on wire, got %s", string(capturedBodies[0]))
	}
	var rollbackPayload autoCommandPayload
	if err := json.Unmarshal(capturedBodies[1], &rollbackPayload); err != nil {
		t.Fatal(err)
	}
	if rollbackPayload.PreconditionMode != commandmodel.PreconditionEmergencyBypass ||
		rollbackPayload.BypassReason != "automated_execution_monitor_rollback:telemetry_unhealthy_over_5m" ||
		rollbackPayload.CurrentState.Quality != "BAD" ||
		rollbackPayload.CurrentState.Presence != "OFFLINE" ||
		rollbackPayload.CurrentState.BusinessRevision != 0 {
		t.Fatalf("unexpected rollback HTTP payload: %+v", rollbackPayload)
	}
}

func TestInspectTelemetryPointHealthDecodesLivePoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "healthy": true,
  "livePoints": [
    {
      "deviceId": "01990000-6000-7000-8000-000000000001",
      "telemetryKey": "chilled_water_supply_temp",
      "numericValue": 7.2,
      "unit": "Cel",
      "quality": "GOOD",
      "freshness": "FRESH",
      "devicePresence": "ONLINE",
      "businessRevision": 88,
      "pointRevision": 9,
      "sampledAt": "2026-11-16T03:59:45Z",
      "receivedAt": "2026-11-16T03:59:46Z"
    },
    {
      "deviceId": "01990000-6000-7000-8000-000000000001",
      "telemetryKey": "fault_code",
      "boolValue": false,
      "textValue": "",
      "quality": "GOOD",
      "freshness": "FRESH",
      "devicePresence": "ONLINE",
      "businessRevision": 89,
      "pointRevision": 10,
      "sampledAt": "",
      "receivedAt": null
    }
  ]
}`))
	}))
	defer srv.Close()

	client, err := NewWorkloadTelemetryHealthClient(TelemetryHealthClientConfig{
		TelemetryURL: srv.URL,
		HTTPClient:   srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := client.InspectTelemetryPointHealth(
		context.Background(),
		"01990000-3000-7000-8000-000000000001",
		"01990000-5000-7000-8000-000000000001",
		[]string{"01990000-6000-7000-8000-000000000001"},
		[]string{"chilled_water_supply_temp", "fault_code"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Healthy || len(snap.LivePoints) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if snap.LivePoints[0].NumericValue == nil || *snap.LivePoints[0].NumericValue != 7.2 ||
		snap.LivePoints[0].BusinessRevision != 88 || snap.LivePoints[0].PointRevision != 9 ||
		snap.LivePoints[0].SampledAt.IsZero() {
		t.Fatalf("unexpected decoded live point[0]: %+v", snap.LivePoints[0])
	}
	if snap.LivePoints[1].BoolValue == nil || *snap.LivePoints[1].BoolValue || !snap.LivePoints[1].SampledAt.IsZero() || !snap.LivePoints[1].ReceivedAt.IsZero() {
		t.Fatalf("unexpected decoded live point[1]: %+v", snap.LivePoints[1])
	}
}

func TestExtractAutoControlLiveStateCanonicalKeysAndFlags(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	siteCtx := AutoControlSiteContext{
		InputMapping: InputMapping{
			SupplyTemperatureKey: "chilled_water_supply_temp",
			ZoneTemperatureKeys:  []string{"zone_temp"},
		},
		SetpointTarget: &SetpointCommandTarget{
			DeviceID:         "01990000-6000-7000-8000-000000000001",
			FeedbackPointKey: "supply_temp_setpoint",
		},
	}
	sampled := now.Add(-10 * time.Second)
	pts := []TelemetryLivePoint{
		{DeviceID: "01990000-6000-7000-8000-000000000001", TelemetryKey: "chilled_water_supply_temp", NumericValue: float64Ptr(7.1), Quality: "GOOD", Freshness: "FRESH", DevicePresence: "ONLINE", BusinessRevision: 10, PointRevision: 2, SampledAt: sampled},
		{DeviceID: "01990000-6000-7000-8000-000000000001", TelemetryKey: "supply_temp_setpoint", NumericValue: float64Ptr(7.0), Quality: "GOOD", Freshness: "FRESH", DevicePresence: "ONLINE", BusinessRevision: 11, PointRevision: 3, SampledAt: sampled},
		{DeviceID: "01990000-6000-7000-8000-000000000002", TelemetryKey: "zone_temp", NumericValue: float64Ptr(23.2), Quality: "GOOD", Freshness: "FRESH", DevicePresence: "ONLINE", BusinessRevision: 12, PointRevision: 4, SampledAt: sampled},
		{DeviceID: "01990000-6000-7000-8000-000000000001", TelemetryKey: "fault_code", BoolValue: boolPtr(true), Quality: "GOOD", Freshness: "FRESH", DevicePresence: "ONLINE", BusinessRevision: 13, PointRevision: 5, SampledAt: sampled},
		{DeviceID: "01990000-6000-7000-8000-000000000001", TelemetryKey: "local_remote_mode", TextValue: strPtr("MANUAL"), Quality: "GOOD", Freshness: "FRESH", DevicePresence: "ONLINE", BusinessRevision: 14, PointRevision: 6, SampledAt: sampled},
		{DeviceID: "01990000-6000-7000-8000-000000000001", TelemetryKey: "chiller_run_state", TextValue: strPtr("RUNNING"), Quality: "GOOD", Freshness: "FRESH", DevicePresence: "ONLINE", BusinessRevision: 15, PointRevision: 7, SampledAt: sampled},
	}

	state := extractAutoControlLiveState(pts, siteCtx, "01990000-6000-7000-8000-000000000001", now)
	if state.SupplyTemp == nil || state.Setpoint == nil || len(state.ZoneTemps) != 1 ||
		!state.DeviceFaultActive || !state.CabinetLocalManual || state.DeviceOffline || state.RunningChillers != 1 {
		t.Fatalf("unexpected extracted live state: %+v", state)
	}
}

func TestAutoControllerWorkerDefaultsAndOverrides(t *testing.T) {
	ctrl, err := NewAutoController(&fakeAutoControlStore{}, &fakeTelemetryReader{}, &fakeDispatcher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctrl.effectiveWorkerID(), ":") {
		t.Fatalf("default workerID=%q, want hostname:pid", ctrl.effectiveWorkerID())
	}
	if ctrl.effectiveLeaseDuration() != DefaultExecutionMonitorClaimLease {
		t.Fatalf("default leaseDuration=%v, want %v", ctrl.effectiveLeaseDuration(), DefaultExecutionMonitorClaimLease)
	}
	ctrl.WithWorkerID("  replica-7  ").WithLeaseDuration(90 * time.Second)
	if ctrl.effectiveWorkerID() != "replica-7" {
		t.Fatalf("overridden workerID=%q, want replica-7", ctrl.effectiveWorkerID())
	}
	if ctrl.effectiveLeaseDuration() != 90*time.Second {
		t.Fatalf("overridden leaseDuration=%v, want 90s", ctrl.effectiveLeaseDuration())
	}
	ctrl.WithWorkerID("   ").WithLeaseDuration(0)
	if ctrl.effectiveWorkerID() != "replica-7" || ctrl.effectiveLeaseDuration() != 90*time.Second {
		t.Fatalf("blank/zero overrides should be ignored: workerID=%q lease=%v", ctrl.effectiveWorkerID(), ctrl.effectiveLeaseDuration())
	}
}

func TestAutoControllerMultiReplicaMonitorClaimPreventsDuplicateRollback(t *testing.T) {
	base := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	applied := 8.0
	restore := 7.0
	monID := "01990000-9900-7000-8000-000000000021"
	store := &fakeAutoControlStore{
		lease:   testAutoLease(base),
		siteCtx: testAutoSiteCtx(),
		monitors: []OptimizationExecutionMonitorRecord{{
			ID:                 monID,
			TenantID:           "01990000-3000-7000-8000-000000000001",
			SiteID:             "01990000-5000-7000-8000-000000000001",
			LeaseID:            "01990000-8800-7000-8000-000000000011",
			DeviceID:           "01990000-6000-7000-8000-000000000001",
			TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
			AppliedSupplyTempC: &applied,
			RestoreSupplyTempC: &restore,
			ZoneTempMaxC:       26.0,
			SupplyTempMinC:     6.0,
			SupplyTempMaxC:     12.0,
			StartedAt:          base,
			WindowEndsAt:       base.Add(30 * time.Minute),
			Status:             ExecutionMonitorObserving,
		}},
	}
	checkAt := base.Add(10 * time.Minute)
	obs := testLiveObservation(checkAt, 8.0, 26.8)
	disp := &fakeDispatcher{
		decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
		grant:     "rollback.sig",
		commandID: "01990000-7700-7000-8000-000000000555",
	}

	ctrlA, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctrlA.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(checkAt, 8.0, applied, 26.8)}).WithWorkerID("replica-a").WithLeaseDuration(45 * time.Second)

	ctrlB, err := NewAutoController(store, &fakeTelemetryReader{obs: obs}, disp, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctrlB.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(checkAt, 8.0, applied, 26.8)}).WithWorkerID("replica-b").WithLeaseDuration(45 * time.Second)

	start := make(chan struct{})
	var wg sync.WaitGroup
	summaries := make([]MonitorPassSummary, 2)
	errs := make([]error, 2)
	for idx, ctrl := range []*AutoController{ctrlA, ctrlB} {
		wg.Add(1)
		go func(i int, c *AutoController) {
			defer wg.Done()
			<-start
			summaries[i], errs[i] = c.RunMonitorPass(context.Background(), checkAt)
		}(idx, ctrl)
	}
	close(start)
	wg.Wait()

	for i, runErr := range errs {
		if runErr != nil {
			t.Fatalf("replica %d RunMonitorPass err=%v", i, runErr)
		}
	}
	totalRolledBack := summaries[0].RolledBack + summaries[1].RolledBack
	totalChecked := summaries[0].Checked + summaries[1].Checked
	if totalChecked != 1 || totalRolledBack != 1 {
		t.Fatalf("expected exactly one replica to claim and roll back monitor, summaries=%+v", summaries)
	}
	if len(disp.submitInputs) != 1 || len(store.trippedRecords) != 1 {
		t.Fatalf("expected single rollback and single trip event, submits=%d trips=%d", len(disp.submitInputs), len(store.trippedRecords))
	}
	if store.monitors[0].ClaimOwner != nil || store.monitors[0].ClaimUntil != nil {
		t.Fatalf("expected monitor claim released after trip, got owner=%v until=%v", store.monitors[0].ClaimOwner, store.monitors[0].ClaimUntil)
	}
}

func TestAutoControllerLeaseExpiryTakeoverFencesStaleReplicaAndReleasesOnError(t *testing.T) {
	base := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	monID := "01990000-9900-7000-8000-000000000022"
	tenantID := "01990000-3000-7000-8000-000000000001"
	siteID := "01990000-5000-7000-8000-000000000001"
	applied := 8.0
	restore := 7.0
	store := &fakeAutoControlStore{
		lease:   testAutoLease(base),
		siteCtx: testAutoSiteCtx(),
		monitors: []OptimizationExecutionMonitorRecord{{
			ID:                 monID,
			TenantID:           tenantID,
			SiteID:             siteID,
			LeaseID:            "01990000-8800-7000-8000-000000000011",
			DeviceID:           "01990000-6000-7000-8000-000000000001",
			TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
			AppliedSupplyTempC: &applied,
			RestoreSupplyTempC: &restore,
			ZoneTempMaxC:       26.0,
			SupplyTempMinC:     6.0,
			SupplyTempMaxC:     12.0,
			StartedAt:          base,
			WindowEndsAt:       base.Add(30 * time.Minute),
			Status:             ExecutionMonitorObserving,
		}},
	}

	claimedA, err := store.ClaimObservingExecutionMonitors(context.Background(), "replica-a", 45*time.Second, base.Add(time.Minute), 10)
	if err != nil || len(claimedA) != 1 {
		t.Fatalf("replica-a claim failed: len=%d err=%v", len(claimedA), err)
	}
	claimedB, err := store.ClaimObservingExecutionMonitors(context.Background(), "replica-b", 45*time.Second, base.Add(90*time.Second), 10)
	if err != nil || len(claimedB) != 0 {
		t.Fatalf("replica-b claimed active lease unexpectedly: len=%d err=%v", len(claimedB), err)
	}
	claimedB, err = store.ClaimObservingExecutionMonitors(context.Background(), "replica-b", 45*time.Second, base.Add(2*time.Minute), 10)
	if err != nil || len(claimedB) != 1 || claimedB[0].ClaimOwner == nil || *claimedB[0].ClaimOwner != "replica-b" {
		t.Fatalf("replica-b takeover failed: claimed=%+v err=%v", claimedB, err)
	}

	if err := store.RecordMonitorHealthyHeartbeat(context.Background(), monID, tenantID, siteID, "replica-a", base.Add(2*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale heartbeat, got %v", err)
	}
	if err := store.RecordMonitorTelemetryUnhealthy(context.Background(), monID, tenantID, siteID, "replica-a", base.Add(2*time.Minute), base.Add(2*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale unhealthy update, got %v", err)
	}
	if err := store.CompleteMonitorVerified(context.Background(), monID, tenantID, siteID, "replica-a", base.Add(30*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale verify, got %v", err)
	}
	if err := store.TripMonitorAndLease(context.Background(), claimedA[0], "replica-a", ExecutionMonitorRollbackFailed, "stale_trip", nil, base.Add(2*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale trip, got %v", err)
	}
	if err := store.MarkMonitorManualOverride(context.Background(), claimedA[0], "replica-a", "cabinet_setpoint_manual_override", base.Add(2*time.Hour), base.Add(2*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale manual override, got %v", err)
	}
	if err := store.ReleaseMonitorClaim(context.Background(), monID, tenantID, siteID, "replica-a", base.Add(2*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale release, got %v", err)
	}
	if store.lease.Status != "ACTIVE" || store.monitors[0].Status != ExecutionMonitorObserving || *store.monitors[0].ClaimOwner != "replica-b" {
		t.Fatalf("stale replica mutated active state: lease=%+v monitor=%+v", store.lease, store.monitors[0])
	}

	if err := store.RecordMonitorHealthyHeartbeat(context.Background(), monID, tenantID, siteID, "replica-b", base.Add(2*time.Minute)); err != nil {
		t.Fatalf("replica-b heartbeat failed: %v", err)
	}
	if store.monitors[0].ClaimOwner != nil || store.monitors[0].ClaimUntil != nil {
		t.Fatalf("expected claim cleared after healthy heartbeat, got %+v", store.monitors[0])
	}
	if err := store.TripMonitorAndLease(context.Background(), claimedA[0], "replica-a", ExecutionMonitorRollbackFailed, "late_slow_replica_trip", nil, base.Add(2*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost after claim cleared, got %v", err)
	}
	if err := store.CompleteMonitorVerified(context.Background(), monID, tenantID, siteID, "replica-a", base.Add(30*time.Minute)); !errors.Is(err, ErrExecutionMonitorClaimLost) {
		t.Fatalf("expected ErrExecutionMonitorClaimLost on stale verify after claim cleared, got %v", err)
	}

	store.siteCtxErr = errors.New("db hiccup")
	beforeReleases := store.releaseCalls
	ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, &fakeDispatcher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.WithPointHealth(&fakePointHealthChecker{}).WithWorkerID("replica-c")
	summary, err := ctrl.RunMonitorPass(context.Background(), base.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("RunMonitorPass returned unexpected error: %v", err)
	}
	if summary.Checked != 1 || store.releaseCalls != beforeReleases+1 || store.monitors[0].ClaimOwner != nil || store.monitors[0].ClaimUntil != nil {
		t.Fatalf("expected claim released on early error exit: summary=%+v releases=%d before=%d monitor=%+v", summary, store.releaseCalls, beforeReleases, store.monitors[0])
	}
}

func TestAutoControllerContextCancelReleasesClaimWithoutMarkingUnhealthy(t *testing.T) {
	base := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	monID := "01990000-9900-7000-8000-000000000023"
	unhealthySince := base.Add(-10 * time.Minute)
	store := &fakeAutoControlStore{
		lease:   testAutoLease(base),
		siteCtx: testAutoSiteCtx(),
		monitors: []OptimizationExecutionMonitorRecord{{
			ID:                      monID,
			TenantID:                "01990000-3000-7000-8000-000000000001",
			SiteID:                  "01990000-5000-7000-8000-000000000001",
			LeaseID:                 "01990000-8800-7000-8000-000000000011",
			DeviceID:                "01990000-6000-7000-8000-000000000001",
			TargetCapability:        string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
			ZoneTempMaxC:            26.0,
			SupplyTempMinC:          6.0,
			SupplyTempMaxC:          12.0,
			StartedAt:               base.Add(-15 * time.Minute),
			WindowEndsAt:            base.Add(15 * time.Minute),
			TelemetryUnhealthySince: &unhealthySince,
			Status:                  ExecutionMonitorObserving,
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	checker := &cancellingPointHealthChecker{cancel: cancel}
	ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, &fakeDispatcher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.WithPointHealth(checker).WithWorkerID("replica-cancel")

	if _, err := ctrl.RunMonitorPass(ctx, base); err != nil {
		t.Fatalf("RunMonitorPass err=%v", err)
	}
	if store.releaseCalls != 1 || len(store.trippedRecords) != 0 || store.monitors[0].Status != ExecutionMonitorObserving || store.monitors[0].ClaimOwner != nil {
		t.Fatalf("expected claim released without tripping on context cancel: releases=%d trips=%d mon=%+v", store.releaseCalls, len(store.trippedRecords), store.monitors[0])
	}
}

type cancellingPointHealthChecker struct {
	cancel context.CancelFunc
}

func (checker *cancellingPointHealthChecker) CheckTelemetryPointHealth(context.Context, string, string, []string, []string) error {
	checker.cancel()
	return context.Canceled
}

func (checker *cancellingPointHealthChecker) InspectTelemetryPointHealth(context.Context, string, string, []string, []string) (OptimizationPointHealthSnapshot, error) {
	checker.cancel()
	return OptimizationPointHealthSnapshot{}, context.Canceled
}

func TestAutoControlCrossDeviceTelemetryIsolationAndMonitorSafetyTrips(t *testing.T) {
	base := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)

	t.Run("secondary device stale point does not veto primary target and setpoints never cross-borrow", func(t *testing.T) {
		siteCtx := testAutoSiteCtx()
		primaryDev := "01990000-6000-7000-8000-000000000001"
		secondaryDev := "01990000-6000-7000-8000-000000000099"
		zoneDev := "01990000-6000-7000-8000-000000000002"
		points := []TelemetryLivePoint{
			{DeviceID: primaryDev, TelemetryKey: "chilled_water_supply_temp", NumericValue: float64Ptr(7.0), Quality: "GOOD", Freshness: "FRESH", DevicePresence: "ONLINE", BusinessRevision: 10, PointRevision: 2, SampledAt: base},
			// Secondary chiller has stale supply_temp_setpoint AND local mode; primary has no setpoint yet.
			{DeviceID: secondaryDev, TelemetryKey: "supply_temp_setpoint", NumericValue: float64Ptr(10.0), Quality: "GOOD", Freshness: "STALE", DevicePresence: "ONLINE", BusinessRevision: 9, PointRevision: 1, SampledAt: base.Add(-10 * time.Minute)},
			{DeviceID: zoneDev, TelemetryKey: "zone_temp", NumericValue: float64Ptr(23.5), Quality: "GOOD", Freshness: "FRESH", DevicePresence: "ONLINE", BusinessRevision: 11, PointRevision: 3, SampledAt: base},
		}
		liveState := extractAutoControlLiveState(points, siteCtx, primaryDev, base)
		if liveState.Setpoint != nil {
			t.Fatalf("expected nil Setpoint on primary device without cross-device borrow, got %+v", liveState.Setpoint)
		}
		samples := buildGuardrailPointSamples(siteCtx, points, liveState)
		for _, sample := range samples {
			if !sample.Present && sample.Key == "supply_temp_setpoint" {
				// Expected missing sample for missing target setpoint
				continue
			}
			if sample.SampledAt.Before(base.Add(-2*time.Minute)) && sample.Present {
				t.Fatalf("unexpected secondary stale sample leaked into primary guardrail check: %+v", sample)
			}
		}
	})

	t.Run("RunMonitorPass records MANUAL_OVERRIDE on cabinet local_remote_mode=false and trips on active fault", func(t *testing.T) {
		monID := "01990000-9900-7000-8000-000000000051"
		devID := "01990000-6000-7000-8000-000000000001"
		pts := testLivePoints(base, 7.0, 7.6, 23.0)
		for i := range pts {
			if pts[i].TelemetryKey == "local_remote_mode" {
				pts[i].TextValue = nil
				pts[i].BoolValue = boolPtr(false)
			}
		}
		store := &fakeAutoControlStore{
			lease:   testAutoLease(base),
			siteCtx: testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{{
				ID: monID, TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
				LeaseID: "01990000-8800-7000-8000-000000000011", DeviceID: devID,
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: float64Ptr(7.6), RestoreSupplyTempC: float64Ptr(7.0),
				ZoneTempMaxC: 26.0, SupplyTempMinC: 5.0, SupplyTempMaxC: 12.0,
				StartedAt: base.Add(-5 * time.Minute), WindowEndsAt: base.Add(25 * time.Minute), Status: ExecutionMonitorObserving,
			}},
		}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, &fakeDispatcher{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: pts})
		summary, err := ctrl.RunMonitorPass(context.Background(), base)
		if err != nil || summary.ManualOverrides != 1 || store.monitors[0].Status != ExecutionMonitorManualOverride {
			t.Fatalf("expected MANUAL_OVERRIDE on cabinet local mode, got summary=%+v mon=%+v err=%v", summary, store.monitors[0], err)
		}

		// Fault during observation trips and triggers rollback.
		faultPts := testLivePoints(base, 7.0, 7.6, 23.0)
		for i := range faultPts {
			if faultPts[i].TelemetryKey == "fault_code" {
				faultPts[i].TextValue = strPtr("E12")
			}
		}
		store.monitors[0].Status = ExecutionMonitorObserving
		store.monitors[0].TripReason = nil
		store.trippedRecords = nil
		disp := &fakeDispatcher{decision: commandauth.Decision{Allowed: true}, grant: "g-rollback", commandID: "01990000-7700-7000-8000-000000000888"}
		ctrl2, err := NewAutoController(store, &fakeTelemetryReader{}, disp, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl2.WithPointHealth(&fakePointHealthChecker{livePoints: faultPts})
		summary2, err := ctrl2.RunMonitorPass(context.Background(), base)
		if err != nil || summary2.RolledBack != 1 || len(store.trippedRecords) != 1 || store.trippedRecords[0].TripReason != "device_fault_active" {
			t.Fatalf("expected fault trip+rollback, got summary=%+v trips=%+v err=%v", summary2, store.trippedRecords, err)
		}
	})
}

func TestAutoControllerShadowModeLastMileInterceptionAndTracePropagation(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)

	t.Run("SHADOW lease runs identical guardrails and clamps without calling commandClient", func(t *testing.T) {
		lease := testAutoLease(now)
		lease.ExecutionMode = ExecutionModeShadow
		lease.SupplyTempMinC = 4.0
		lease.MaxSetpointStepC = 2.0
		siteCtx := testAutoSiteCtx()
		siteCtx.HasActiveMonitor = true // must NOT block shadow evaluation
		siteCtx.RecentAutoCountHour = lease.MaxCommandsPerHour

		store := &fakeAutoControlStore{lease: lease, siteCtx: siteCtx}
		disp := &fakeDispatcher{} // zero grant/command configured: must NEVER be invoked
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: testLiveObservation(now, 6.2, 23.5)}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(now, 6.2, 6.0, 23.5)})

		rec := testAutoRecommendation(now.Add(-5 * time.Minute))
		rec.Baseline.SupplyTempC = 6.0
		rec.Candidate.SupplyTempC = 4.2 // clamped to hard floor 5.0

		incomingTrace := "00-11112222333344445555666677778888-9999aaaabbbbcccc-01"
		ctx := observability.ContextWithRemoteParent(context.Background(), observability.NewTracer("test", nil), incomingTrace, "")

		out, err := ctrl.TryDispatch(ctx, rec, now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Dispatched || !out.ShadowRecorded || out.ExecutionMode != ExecutionModeShadow || out.MonitorID == "" {
			t.Fatalf("expected shadow recorded outcome without dispatch, got %+v", out)
		}
		if !uuidPattern.MatchString(out.DecisionID) {
			t.Fatalf("expected valid UUIDv7 DecisionID, got %q", out.DecisionID)
		}
		if out.TraceID != "11112222333344445555666677778888" {
			t.Fatalf("expected preserved incoming TraceID, got %q", out.TraceID)
		}
		if len(disp.grantReqs) != 0 || len(disp.submitInputs) != 0 {
			t.Fatalf("SHADOW mode must NEVER call commandClient, got grants=%d submits=%d", len(disp.grantReqs), len(disp.submitInputs))
		}
		if len(store.dispatchedRecs) != 0 {
			t.Fatalf("SHADOW mode must not approve recommendation, got %+v", store.dispatchedRecs)
		}
		if len(store.monitors) != 1 {
			t.Fatalf("expected 1 shadow monitor row, got %d", len(store.monitors))
		}
		mon := store.monitors[0]
		if mon.Status != ExecutionMonitorShadowRecorded ||
			mon.ExecutionMode != ExecutionModeShadow ||
			mon.CommandIntentID != "" ||
			mon.DecisionID != out.DecisionID ||
			mon.TraceID != out.TraceID ||
			mon.RawRecommendedValue == nil || *mon.RawRecommendedValue != 4.2 ||
			mon.ClampedValue == nil || *mon.ClampedValue != 5.0 ||
			mon.GuardrailVerdict != string(commandmodel.GuardrailVerdictClamped) ||
			len(mon.GuardrailTrace) == 0 {
			t.Fatalf("unexpected SHADOW_RECORDED monitor row: %+v", mon)
		}

		// Subsequent CLOSED_LOOP dispatch is neither blocked by nor quota-starved by SHADOW_RECORDED monitors.
		store.lease.ExecutionMode = ExecutionModeClosedLoop
		siteCtxClosed := testAutoSiteCtx()
		siteCtxClosed.RecentAutoCountHour = 0
		store.siteCtx = siteCtxClosed
		dispClosed := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "closed.grant",
			commandID: "01990000-7700-7000-8000-000000000901",
		}
		ctrlClosed, err := NewAutoController(store, &fakeTelemetryReader{obs: testLiveObservation(now, 6.2, 23.5)}, dispClosed, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrlClosed.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(now, 6.2, 6.0, 23.5)})

		recClosed := testAutoRecommendation(now.Add(-4 * time.Minute))
		recClosed.ID = "01990000-2000-7000-8000-000000000009"
		recClosed.Baseline.SupplyTempC = 6.0
		recClosed.Candidate.SupplyTempC = 7.2
		outClosed, err := ctrlClosed.TryDispatch(context.Background(), recClosed, now)
		if err != nil {
			t.Fatal(err)
		}
		if !outClosed.Dispatched || outClosed.ShadowRecorded || outClosed.ExecutionMode != ExecutionModeClosedLoop {
			t.Fatalf("expected closed-loop dispatch after shadow run, got %+v", outClosed)
		}
		if !uuidPattern.MatchString(outClosed.DecisionID) || normalizeTraceID(outClosed.TraceID) == "" {
			t.Fatalf("expected generated DecisionID and 32-hex TraceID, got decision=%q trace=%q", outClosed.DecisionID, outClosed.TraceID)
		}
		if len(dispClosed.submitInputs) != 1 ||
			dispClosed.submitInputs[0].DecisionID != outClosed.DecisionID ||
			dispClosed.submitInputs[0].TraceID != outClosed.TraceID {
			t.Fatalf("expected DecisionID/TraceID propagated into dispatch input, got %+v vs %+v", dispClosed.submitInputs, outClosed)
		}
		if parsed, ok := observability.ParseTraceparent(dispClosed.submitTraceparents[0], ""); !ok || parsed.TraceID != outClosed.TraceID {
			t.Fatalf("expected W3C traceparent propagated to SubmitAutoCommand, got %q", dispClosed.submitTraceparents[0])
		}
	})

	t.Run("SHADOW lease vetoes unsafe telemetry identically to CLOSED_LOOP", func(t *testing.T) {
		lease := testAutoLease(now)
		lease.ExecutionMode = ExecutionModeShadow
		store := &fakeAutoControlStore{lease: lease, siteCtx: testAutoSiteCtx()}
		disp := &fakeDispatcher{}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, disp, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		pts := mutateLivePoint(testLivePoints(now, 7.1, 7.0, 23.0), "fault_code", func(pt *TelemetryLivePoint) {
			pt.TextValue = strPtr("E99")
		})
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: pts})

		out, err := ctrl.TryDispatch(context.Background(), testAutoRecommendation(now.Add(-5*time.Minute)), now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Dispatched || out.ShadowRecorded || out.SkipReason == "" || len(store.monitors) != 0 {
			t.Fatalf("expected guardrail veto in SHADOW mode without monitor row, got out=%+v monitors=%d", out, len(store.monitors))
		}
		if !uuidPattern.MatchString(out.DecisionID) || normalizeTraceID(out.TraceID) == "" {
			t.Fatalf("expected DecisionID and TraceID on skip outcome, got %+v", out)
		}
	})

	t.Run("SHADOW rate limit caps at 12 per hour independently", func(t *testing.T) {
		lease := testAutoLease(now)
		lease.ExecutionMode = ExecutionModeShadow
		siteCtx := testAutoSiteCtx()
		siteCtx.RecentShadowCountHour = DefaultShadowMaxPerHour
		store := &fakeAutoControlStore{lease: lease, siteCtx: siteCtx}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, &fakeDispatcher{}, func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(now, 7.1, 7.0, 23.0)})
		out, err := ctrl.TryDispatch(context.Background(), testAutoRecommendation(now.Add(-5*time.Minute)), now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Dispatched || out.ShadowRecorded || out.SkipReason != "SHADOW_RATE_LIMIT_REACHED" {
			t.Fatalf("expected SHADOW_RATE_LIMIT_REACHED, got %+v", out)
		}
	})
}

func TestAutoControllerTrackingDeviationAndRollbackAlarmLinkage(t *testing.T) {
	base := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)

	t.Run("grace period under 10m does not trip on restore setpoint; >=10m trips and publishes SETPOINT_TRACKING_DEVIATION", func(t *testing.T) {
		applied := 8.0
		restore := 7.0
		monID := "01990000-9900-7000-8000-000000000061"
		cmdID := "01990000-7700-7000-8000-000000000661"
		decisionID := "01990000-aaaa-7000-8000-000000000661"
		traceID := "abcdef0123456789abcdef0123456789"
		store := &fakeAutoControlStore{
			lease:   testAutoLease(base),
			siteCtx: testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{{
				ID:                 monID,
				TenantID:           "01990000-3000-7000-8000-000000000001",
				SiteID:             "01990000-5000-7000-8000-000000000001",
				LeaseID:            "01990000-8800-7000-8000-000000000011",
				CommandIntentID:    cmdID,
				DeviceID:           "01990000-6000-7000-8000-000000000001",
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: &applied,
				RestoreSupplyTempC: &restore,
				ZoneTempMaxC:       26.0,
				SupplyTempMinC:     5.0,
				SupplyTempMaxC:     12.0,
				StartedAt:          base,
				WindowEndsAt:       base.Add(30 * time.Minute),
				Status:             ExecutionMonitorObserving,
				DecisionID:         decisionID,
				TraceID:            traceID,
			}},
		}
		events := &fakeControlEventPublisher{}
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "rollback.sig",
			commandID: "01990000-7700-7000-8000-000000000777",
		}
		health := &fakePointHealthChecker{livePoints: testLivePoints(base.Add(9*time.Minute), 7.5, restore, 23.0)}
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: testLiveObservation(base, 7.5, 23.0)}, disp, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(health).WithControlEvents(events)

		// T+9m (< 10m grace): still at restore setpoint 7.0 -> heartbeat only, no trip or alarm.
		summary9, err := ctrl.RunMonitorPass(context.Background(), base.Add(9*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if summary9.RolledBack != 0 || summary9.Tripped != 0 || len(events.events) != 0 || store.monitors[0].Status != ExecutionMonitorObserving {
			t.Fatalf("expected no trip before 10m grace, summary=%+v events=%+v", summary9, events.events)
		}

		// T+10m: setpoint exactly at 8.3 (0.3C boundary) -> must NOT falsely trip due to float rounding.
		check10 := base.Add(10 * time.Minute)
		health.setLivePoints(testLivePoints(check10, 8.0, 8.3, 23.0))
		summaryBoundary, err := ctrl.RunMonitorPass(context.Background(), check10)
		if err != nil {
			t.Fatal(err)
		}
		if summaryBoundary.RolledBack != 0 || len(events.events) != 0 {
			t.Fatalf("expected 0.3C exact boundary not to trip, summary=%+v events=%+v", summaryBoundary, events.events)
		}

		// T+12m: setpoint still stuck at restore 7.0 (deviation 1.0C > 0.3C) -> trip + rollback + alarm!
		check12 := base.Add(12 * time.Minute)
		health.setLivePoints(testLivePoints(check12, 7.5, restore, 23.0))
		summary12, err := ctrl.RunMonitorPass(context.Background(), check12)
		if err != nil {
			t.Fatal(err)
		}
		if summary12.RolledBack != 1 || store.monitors[0].Status != ExecutionMonitorTrippedRolledBack ||
			len(store.trippedRecords) != 1 || store.trippedRecords[0].TripReason != "setpoint_tracking_deviation" {
			t.Fatalf("expected setpoint_tracking_deviation trip+rollback, summary=%+v trips=%+v", summary12, store.trippedRecords)
		}
		if parsed, ok := observability.ParseTraceparent(disp.submitTraceparents[0], ""); !ok || parsed.TraceID != traceID {
			t.Fatalf("expected rollback HTTP traceparent carrying monitor TraceID %s, got %q", traceID, disp.submitTraceparents[0])
		}
		if len(events.events) != 1 {
			t.Fatalf("expected 1 tracking deviation alarm event, got %+v", events.events)
		}
		ev := events.events[0]
		if ev.EventKind != ControlEventSetpointTrackingDeviation ||
			ev.CommandID != cmdID ||
			ev.DeviceID == nil || *ev.DeviceID != store.monitors[0].DeviceID ||
			ev.Capability != store.monitors[0].TargetCapability ||
			ev.ReasonCode != ControlEventSetpointTrackingDeviation ||
			ev.CorrelationID != decisionID {
			t.Fatalf("unexpected tracking deviation alarm payload: %+v", ev)
		}
	})

	t.Run("AUTO_CONTROL_ROLLBACK_FAILED published on rollback failure alongside SETPOINT_TRACKING_DEVIATION", func(t *testing.T) {
		applied := 8.0
		restore := 7.0
		monID := "01990000-9900-7000-8000-000000000062"
		decisionID := "01990000-aaaa-7000-8000-000000000662"
		store := &fakeAutoControlStore{
			lease:   testAutoLease(base),
			siteCtx: testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{{
				ID:                 monID,
				TenantID:           "01990000-3000-7000-8000-000000000001",
				SiteID:             "01990000-5000-7000-8000-000000000001",
				LeaseID:            "01990000-8800-7000-8000-000000000011",
				CommandIntentID:    "", // verifies fallback to monitor.ID
				DeviceID:           "01990000-6000-7000-8000-000000000001",
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: &applied,
				RestoreSupplyTempC: &restore,
				ZoneTempMaxC:       26.0,
				SupplyTempMinC:     5.0,
				SupplyTempMaxC:     12.0,
				StartedAt:          base,
				WindowEndsAt:       base.Add(30 * time.Minute),
				Status:             ExecutionMonitorObserving,
				DecisionID:         decisionID,
			}},
		}
		events := &fakeControlEventPublisher{}
		disp := &fakeDispatcher{
			decision:  commandauth.Decision{Allowed: true, ReasonCode: commandauth.ReasonAllowAutoLease},
			grant:     "rollback.sig",
			submitErr: errors.New("gateway timeout"),
		}
		checkAt := base.Add(15 * time.Minute)
		// Drift 0.32C (>0.3C deviation, <=0.35C tamper threshold) triggers setpoint_tracking_deviation rather than MANUAL_OVERRIDE.
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{obs: testLiveObservation(checkAt, 8.0, 23.0)}, disp, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(checkAt, 8.0, 8.32, 23.0)}).WithControlEvents(events)

		summary, err := ctrl.RunMonitorPass(context.Background(), checkAt)
		if err != nil {
			t.Fatal(err)
		}
		if summary.Tripped != 1 || summary.RolledBack != 0 || store.monitors[0].Status != ExecutionMonitorRollbackFailed {
			t.Fatalf("expected ROLLBACK_FAILED trip, summary=%+v mon=%+v", summary, store.monitors[0])
		}
		if len(events.events) != 2 {
			t.Fatalf("expected 2 alarm events (SETPOINT_TRACKING_DEVIATION + AUTO_CONTROL_ROLLBACK_FAILED), got %+v", events.events)
		}
		if events.events[0].EventKind != ControlEventSetpointTrackingDeviation ||
			events.events[0].CommandID != monID ||
			events.events[0].CorrelationID != decisionID {
			t.Fatalf("unexpected first alarm event: %+v", events.events[0])
		}
		if events.events[1].EventKind != ControlEventAutoControlRollbackFailed ||
			events.events[1].CommandID != monID ||
			events.events[1].Severity != "CRITICAL" ||
			events.events[1].CorrelationID != decisionID ||
			!strings.Contains(events.events[1].Detail, "gateway timeout") {
			t.Fatalf("unexpected rollback failed alarm event: %+v", events.events[1])
		}
	})

	t.Run("MANUAL_OVERRIDE after 10m suppresses setpoint_tracking_deviation alarm", func(t *testing.T) {
		applied := 8.0
		restore := 7.0
		store := &fakeAutoControlStore{
			lease:   testAutoLease(base),
			siteCtx: testAutoSiteCtx(),
			monitors: []OptimizationExecutionMonitorRecord{{
				ID:                 "01990000-9900-7000-8000-000000000063",
				TenantID:           "01990000-3000-7000-8000-000000000001",
				SiteID:             "01990000-5000-7000-8000-000000000001",
				LeaseID:            "01990000-8800-7000-8000-000000000011",
				DeviceID:           "01990000-6000-7000-8000-000000000001",
				TargetCapability:   string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
				AppliedSupplyTempC: &applied,
				RestoreSupplyTempC: &restore,
				ZoneTempMaxC:       26.0,
				SupplyTempMinC:     5.0,
				SupplyTempMaxC:     12.0,
				StartedAt:          base,
				WindowEndsAt:       base.Add(30 * time.Minute),
				Status:             ExecutionMonitorObserving,
			}},
		}
		events := &fakeControlEventPublisher{}
		checkAt := base.Add(15 * time.Minute)
		ctrl, err := NewAutoController(store, &fakeTelemetryReader{}, &fakeDispatcher{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctrl.WithPointHealth(&fakePointHealthChecker{livePoints: testLivePoints(checkAt, 8.0, 9.5, 23.0)}).WithControlEvents(events)

		summary, err := ctrl.RunMonitorPass(context.Background(), checkAt)
		if err != nil {
			t.Fatal(err)
		}
		if summary.ManualOverrides != 1 || store.monitors[0].Status != ExecutionMonitorManualOverride || len(events.events) != 0 {
			t.Fatalf("expected MANUAL_OVERRIDE without tracking alarm, summary=%+v mon=%+v events=%+v", summary, store.monitors[0], events.events)
		}
	})
}

func TestHTTPControlEventPublisher(t *testing.T) {
	var receivedHeader string
	var receivedBody []byte
	statusCode := http.StatusCreated

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != InternalControlEventsPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		receivedHeader = r.Header.Get("traceparent")
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(`{"alarmId":"01990000-bbbb-7000-8000-000000000001"}`))
	}))
	defer srv.Close()

	pub, err := NewHTTPControlEventPublisher(ControlEventClientConfig{
		AlarmURL:   srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	traceparent := "00-0123456789abcdef0123456789abcdef-1111222233334444-01"
	ctx := observability.ContextWithRemoteParent(context.Background(), observability.NewTracer("test", nil), traceparent, "")
	devID := "01990000-6000-7000-8000-000000000001"
	err = pub.PublishControlEvent(ctx, ControlEvent{
		SchemaVersion: 1,
		TenantID:      "01990000-3000-7000-8000-000000000001",
		SiteID:        "01990000-5000-7000-8000-000000000001",
		DeviceID:      &devID,
		CommandID:     "01990000-7700-7000-8000-000000000001",
		EventKind:     ControlEventAutoControlRollbackFailed,
		Capability:    string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
		ReasonCode:    ControlEventAutoControlRollbackFailed,
		CorrelationID: "01990000-aaaa-7000-8000-000000000001",
	})
	if err != nil {
		t.Fatalf("PublishControlEvent failed: %v", err)
	}
	if receivedHeader != traceparent {
		t.Fatalf("traceparent=%q want %q", receivedHeader, traceparent)
	}
	var decoded ControlEvent
	dec := json.NewDecoder(strings.NewReader(string(receivedBody)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		t.Fatalf("decode published ControlEvent failed: %v body=%s", err, string(receivedBody))
	}
	if decoded.SchemaVersion != 1 || decoded.EventKind != ControlEventAutoControlRollbackFailed || decoded.OccurredAt == "" {
		t.Fatalf("unexpected decoded payload: %+v", decoded)
	}

	statusCode = http.StatusInternalServerError
	if err := pub.PublishControlEvent(context.Background(), ControlEvent{
		TenantID:  "01990000-3000-7000-8000-000000000001",
		SiteID:    "01990000-5000-7000-8000-000000000001",
		CommandID: "01990000-7700-7000-8000-000000000001",
		EventKind: ControlEventSetpointTrackingDeviation,
	}); err == nil {
		t.Fatal("expected error on 500 response from alarm service")
	}
}
