package core

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	testControlAssetChiller1 = "018f1e00-7700-7000-8000-000000000001"
	testControlDevicePLC1    = "018f1e00-7700-7000-8000-000000000101"
)

type memoryControlLockBackend struct {
	*memoryScheduleBackend
	lockMu       sync.Mutex
	now          func() time.Time
	siteStates   map[string]SiteControlState
	assetSite    map[string]string
	assetDevices map[string][]string
	assetLocks   map[string]AssetControlLockState
}

func newMemoryControlLockBackend() *memoryControlLockBackend {
	return &memoryControlLockBackend{
		memoryScheduleBackend: newMemoryScheduleBackend(),
		siteStates:            map[string]SiteControlState{},
		assetSite:             map[string]string{testControlAssetChiller1: testSiteA1},
		assetDevices:          map[string][]string{testControlAssetChiller1: {testControlDevicePLC1}},
		assetLocks:            map[string]AssetControlLockState{},
	}
}

func (backend *memoryControlLockBackend) currentTime() time.Time {
	if backend.now == nil {
		return time.Now().UTC()
	}
	return backend.now().UTC()
}

func (backend *memoryControlLockBackend) activeMaintenanceWindow(siteID string) *ActiveMaintenanceWindow {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	siteDate := siteLocalCalendarDate(backend.site.Timezone, backend.currentTime())
	matches := make([]OperatingException, 0)
	for _, exc := range backend.exceptions[siteID] {
		if exc.Status == "APPROVED" && exc.Kind == "MAINTENANCE_WINDOW" && exc.StartDate <= siteDate && exc.EndDate >= siteDate {
			matches = append(matches, exc)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].StartDate != matches[j].StartDate {
			return matches[i].StartDate < matches[j].StartDate
		}
		return matches[i].ID < matches[j].ID
	})
	first := matches[0]
	return &ActiveMaintenanceWindow{
		ID:        first.ID,
		Title:     first.Title,
		StartDate: first.StartDate,
		EndDate:   first.EndDate,
		Reason:    first.Reason,
	}
}

func (backend *memoryControlLockBackend) GetSiteControlState(_ context.Context, claims registryauth.GrantClaims, siteID string) (SiteControlState, error) {
	window := backend.activeMaintenanceWindow(siteID)
	backend.lockMu.Lock()
	defer backend.lockMu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return SiteControlState{}, ErrNotFound
	}
	state, ok := backend.siteStates[siteID]
	if !ok {
		state = SiteControlState{
			TenantID:    claims.TenantID,
			SiteID:      siteID,
			ControlMode: SiteControlModeRemoteAuto,
			AssetLocks:  []AssetControlLockState{},
		}
	}
	locks := []AssetControlLockState{}
	for _, lock := range backend.assetLocks {
		if lock.SiteID == siteID {
			locks = append(locks, lock)
		}
	}
	state.AssetLocks = locks
	state.ActiveMaintenanceWindow = window
	return state, nil
}

func (backend *memoryControlLockBackend) SaveSiteControlMode(_ context.Context, claims registryauth.GrantClaims, input SiteControlModeMutation) (SiteControlState, bool, error) {
	if err := input.Validate(); err != nil {
		return SiteControlState{}, false, err
	}
	window := backend.activeMaintenanceWindow(input.SiteID)
	backend.lockMu.Lock()
	defer backend.lockMu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return SiteControlState{}, false, ErrNotFound
	}
	current, ok := backend.siteStates[input.SiteID]
	var currentRev int64
	if ok {
		currentRev = current.Revision
	}
	if input.Meta.ExpectedRevision != currentRev {
		return SiteControlState{}, false, ErrRevisionConflict
	}
	id := "01990000-8400-7000-8000-000000000001"
	updatedBy := claims.PrincipalID
	updatedAt := "2026-11-14T10:00:00.000Z"
	saved := SiteControlState{
		ID:                      &id,
		TenantID:                claims.TenantID,
		SiteID:                  input.SiteID,
		ControlMode:             input.ControlMode,
		Reason:                  effectiveControlReason(input.Reason, input.Meta.Reason),
		UpdatedBy:               &updatedBy,
		UpdatedAt:               &updatedAt,
		Revision:                currentRev + 1,
		AssetLocks:              []AssetControlLockState{},
		ActiveMaintenanceWindow: window,
	}
	for _, lock := range backend.assetLocks {
		if lock.SiteID == input.SiteID {
			saved.AssetLocks = append(saved.AssetLocks, lock)
		}
	}
	backend.siteStates[input.SiteID] = saved
	return saved, false, nil
}

func (backend *memoryControlLockBackend) SaveAssetControlLock(_ context.Context, claims registryauth.GrantClaims, input AssetControlLockMutation) (AssetControlLockState, bool, error) {
	if err := input.Validate(); err != nil {
		return AssetControlLockState{}, false, err
	}
	backend.lockMu.Lock()
	defer backend.lockMu.Unlock()
	siteID, exists := backend.assetSite[input.AssetID]
	if !exists || !backend.siteVisible(claims, siteID) {
		return AssetControlLockState{}, false, ErrNotFound
	}
	current, ok := backend.assetLocks[input.AssetID]
	var currentRev int64
	if ok {
		currentRev = current.Revision
	}
	if input.Meta.ExpectedRevision != currentRev {
		return AssetControlLockState{}, false, ErrRevisionConflict
	}
	updatedBy := claims.PrincipalID
	updatedAt := "2026-11-14T10:05:00.000Z"
	saved := AssetControlLockState{
		ID:             "01990000-8400-7000-8000-000000000002",
		TenantID:       claims.TenantID,
		SiteID:         siteID,
		AssetID:        input.AssetID,
		LockMode:       input.LockMode,
		Reason:         effectiveControlReason(input.Reason, input.Meta.Reason),
		UpdatedBy:      &updatedBy,
		UpdatedAt:      &updatedAt,
		Revision:       currentRev + 1,
		BoundDeviceIDs: append([]string(nil), backend.assetDevices[input.AssetID]...),
	}
	backend.assetLocks[input.AssetID] = saved
	return saved, false, nil
}

func TestSiteControlStateAndAssetLockRoutes(t *testing.T) {
	backend := newMemoryControlLockBackend()
	client := newScheduleHTTPClientWithInterfaces(t, backend, backend)

	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	operatorClaims := testGrantClaims(registryauth.ActionScheduleRequest)
	managerClaims := testGrantClaims(registryauth.ActionScheduleManage)

	// Default control state is REMOTE_AUTO at revision 0.
	resp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/control-state", readClaims, "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET control-state status=%d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	var initial SiteControlState
	if err := json.Unmarshal(resp.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode initial control-state: %v", err)
	}
	if initial.ControlMode != SiteControlModeRemoteAuto || initial.Revision != 0 || len(initial.AssetLocks) != 0 {
		t.Fatalf("unexpected initial control state: %#v", initial)
	}

	// Query params rejected on control-state.
	if badQuery := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/control-state?extra=1", readClaims, "", nil); badQuery.Code != http.StatusBadRequest {
		t.Fatalf("GET control-state with query param status=%d, want 400", badQuery.Code)
	}

	// Operator without schedule.manage cannot switch control-mode or lock assets.
	if forbidSite := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/control-mode", operatorClaims, "", map[string]any{
		"controlMode": SiteControlModeManualOverride,
		"reason":      "值班切本地手动",
		"meta":        map[string]any{"expectedRevision": 0, "idempotencyKey": "ctrl-mode-forbid-1", "reason": "值班切本地手动"},
	}); forbidSite.Code != http.StatusForbidden {
		t.Fatalf("operator PUT control-mode status=%d, want 403", forbidSite.Code)
	}
	if forbidAsset := client.do(t, http.MethodPut, RegistryPathPrefix+"assets/"+testControlAssetChiller1+"/control-lock", operatorClaims, "", map[string]any{
		"lockMode": AssetControlLockMaintenance,
		"reason":   "主机维保锁定",
		"meta":     map[string]any{"expectedRevision": 0, "idempotencyKey": "asset-lock-forbid-1", "reason": "主机维保锁定"},
	}); forbidAsset.Code != http.StatusForbidden {
		t.Fatalf("operator PUT asset control-lock status=%d, want 403", forbidAsset.Code)
	}

	// Invalid control mode returns 400.
	if badMode := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/control-mode", managerClaims, "", map[string]any{
		"controlMode": "BOGUS",
		"meta":        map[string]any{"expectedRevision": 0, "idempotencyKey": "ctrl-mode-bad-1", "reason": "无效模式"},
	}); badMode.Code != http.StatusBadRequest {
		t.Fatalf("invalid PUT control-mode status=%d, want 400; body=%s", badMode.Code, badMode.Body.String())
	}

	// Switch site to MANUAL_OVERRIDE (revision 0 -> 1).
	overrideResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/control-mode", managerClaims, "", map[string]any{
		"controlMode": SiteControlModeManualOverride,
		"reason":      "现场接管手自动切换",
		"meta":        map[string]any{"expectedRevision": 0, "idempotencyKey": "ctrl-mode-manual-1", "reason": "现场接管手自动切换"},
	})
	if overrideResp.Code != http.StatusOK {
		t.Fatalf("PUT control-mode status=%d, want 200; body=%s", overrideResp.Code, overrideResp.Body.String())
	}
	var updated SiteControlState
	if err := json.Unmarshal(overrideResp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated control-state: %v", err)
	}
	if updated.ControlMode != SiteControlModeManualOverride || updated.Revision != 1 || updated.Reason != "现场接管手自动切换" {
		t.Fatalf("unexpected updated control state: %#v", updated)
	}

	// Stale revision returns 409.
	if staleResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/control-mode", managerClaims, "", map[string]any{
		"controlMode": SiteControlModeSafeShutdownExit,
		"meta":        map[string]any{"expectedRevision": 0, "idempotencyKey": "ctrl-mode-stale-1", "reason": "过时版本号测试"},
	}); staleResp.Code != http.StatusConflict {
		t.Fatalf("stale PUT control-mode status=%d, want 409", staleResp.Code)
	}

	// Lock asset for maintenance (revision 0 -> 1), then inspect GET control-state.
	lockResp := client.do(t, http.MethodPut, RegistryPathPrefix+"assets/"+testControlAssetChiller1+"/control-lock", managerClaims, "", map[string]any{
		"lockMode": AssetControlLockMaintenance,
		"reason":   "1号冷机清洗检修",
		"meta":     map[string]any{"expectedRevision": 0, "idempotencyKey": "asset-lock-maint-1", "reason": "1号冷机清洗检修"},
	})
	if lockResp.Code != http.StatusOK {
		t.Fatalf("PUT asset control-lock status=%d, want 200; body=%s", lockResp.Code, lockResp.Body.String())
	}
	var lockedAsset AssetControlLockState
	if err := json.Unmarshal(lockResp.Body.Bytes(), &lockedAsset); err != nil {
		t.Fatalf("decode asset lock: %v", err)
	}
	if lockedAsset.LockMode != AssetControlLockMaintenance || lockedAsset.Revision != 1 || len(lockedAsset.BoundDeviceIDs) != 1 || lockedAsset.BoundDeviceIDs[0] != testControlDevicePLC1 {
		t.Fatalf("unexpected asset lock response: %#v", lockedAsset)
	}

	// Unknown asset or out-of-scope site returns 404.
	if missingLock := client.do(t, http.MethodPut, RegistryPathPrefix+"assets/018f1e00-7700-7000-8000-000000000999/control-lock", managerClaims, "", map[string]any{
		"lockMode": AssetControlLockManual,
		"meta":     map[string]any{"expectedRevision": 1, "idempotencyKey": "asset-lock-missing-1", "reason": "不存在资产测试"},
	}); missingLock.Code != http.StatusNotFound {
		t.Fatalf("missing asset control-lock status=%d, want 404", missingLock.Code)
	}
	otherSiteClaims := managerClaims
	otherSiteClaims.AllowedSiteIDs = []string{"018f1e00-0000-7000-8000-000000000999"}
	if outOfScope := client.do(t, http.MethodPut, RegistryPathPrefix+"assets/"+testControlAssetChiller1+"/control-lock", otherSiteClaims, "", map[string]any{
		"lockMode": AssetControlLockManual,
		"meta":     map[string]any{"expectedRevision": 1, "idempotencyKey": "asset-lock-scope-1", "reason": "跨站点越权测试"},
	}); outOfScope.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope asset control-lock status=%d, want 404", outOfScope.Code)
	}

	// GET control-state reflects both site mode and asset lock.
	afterResp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/control-state", readClaims, "", nil)
	if afterResp.Code != http.StatusOK {
		t.Fatalf("GET control-state after writes status=%d", afterResp.Code)
	}
	var after SiteControlState
	if err := json.Unmarshal(afterResp.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode after control-state: %v", err)
	}
	if after.ControlMode != SiteControlModeManualOverride || len(after.AssetLocks) != 1 || after.AssetLocks[0].LockMode != AssetControlLockMaintenance {
		t.Fatalf("unexpected after control state: %#v", after)
	}
}

func TestSiteLocalCalendarDateCrossMidnightAndFallbacks(t *testing.T) {
	crossMidnightUTC := time.Date(2026, 11, 15, 16, 30, 0, 0, time.UTC)  // 2026-11-16 00:30+08:00
	beforeMidnightSH := time.Date(2026, 11, 15, 15, 59, 59, 0, time.UTC) // 2026-11-15 23:59:59+08:00

	cases := []struct {
		name     string
		timezone string
		at       time.Time
		want     string
	}{
		{name: "shanghai_cross_midnight", timezone: "Asia/Shanghai", at: crossMidnightUTC, want: "2026-11-16"},
		{name: "shanghai_before_midnight", timezone: "Asia/Shanghai", at: beforeMidnightSH, want: "2026-11-15"},
		{name: "utc_same_instant", timezone: "UTC", at: crossMidnightUTC, want: "2026-11-15"},
		{name: "blank_falls_back_to_utc", timezone: "   ", at: crossMidnightUTC, want: "2026-11-15"},
		{name: "invalid_falls_back_to_utc", timezone: "Mars/Olympus_Mons", at: crossMidnightUTC, want: "2026-11-15"},
		{name: "local_keyword_falls_back_to_utc", timezone: "Local", at: crossMidnightUTC, want: "2026-11-15"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := siteLocalCalendarDate(tc.timezone, tc.at); got != tc.want {
				t.Fatalf("siteLocalCalendarDate(%q, %s) = %q, want %q", tc.timezone, tc.at.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

func TestSiteControlStateIncludesAndOmitsActiveMaintenanceWindow(t *testing.T) {
	backend := newMemoryControlLockBackend()
	fixedNow := time.Date(2026, 11, 15, 16, 30, 0, 0, time.UTC) // Asia/Shanghai is 2026-11-16
	backend.now = func() time.Time { return fixedNow }
	client := newScheduleHTTPClientWithInterfaces(t, backend, backend)

	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	operatorClaims := testGrantClaims(registryauth.ActionScheduleRequest)
	operatorClaims.PrincipalID = testOperatorPrincipal
	managerClaims := testGrantClaims(registryauth.ActionScheduleManage)
	managerClaims.PrincipalID = testManagerPrincipal

	assertOmitted := func(t *testing.T, label string) {
		t.Helper()
		resp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/control-state", readClaims, "", nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: GET control-state status=%d body=%s", label, resp.Code, resp.Body.String())
		}
		if strings.Contains(resp.Body.String(), `"activeMaintenanceWindow"`) {
			t.Fatalf("%s: expected activeMaintenanceWindow to be omitted, got %s", label, resp.Body.String())
		}
		var state SiteControlState
		if err := json.Unmarshal(resp.Body.Bytes(), &state); err != nil {
			t.Fatalf("%s: decode control-state: %v", label, err)
		}
		if state.ActiveMaintenanceWindow != nil {
			t.Fatalf("%s: expected nil ActiveMaintenanceWindow, got %#v", label, state.ActiveMaintenanceWindow)
		}
	}

	// Default: no exception -> omitted.
	assertOmitted(t, "initial")

	// PENDING_APPROVAL MAINTENANCE_WINDOW on site-local date does NOT trigger gating.
	pendingRes := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions", operatorClaims, "张工", CreateOperatingExceptionRequest{
		Title:            "1号冷机清洗检修",
		Kind:             "MAINTENANCE_WINDOW",
		StartDate:        "2026-11-16",
		EndDate:          "2026-11-16",
		ReplacementSlots: []OperatingExceptionSlot{},
		Meta:             MutationMeta{ExpectedRevision: 0, IdempotencyKey: "maint-pending-0001", Reason: "冷却水塔停机清洗"},
	})
	if pendingRes.Code != http.StatusCreated {
		t.Fatalf("create pending maintenance exception status=%d body=%s", pendingRes.Code, pendingRes.Body.String())
	}
	var maintExc OperatingException
	if err := json.Unmarshal(pendingRes.Body.Bytes(), &maintExc); err != nil {
		t.Fatalf("decode created maintenance exception: %v", err)
	}
	assertOmitted(t, "pending_approval")

	// APPROVED HOLIDAY_SHUTDOWN (different kind, UTC date) does NOT appear as activeMaintenanceWindow.
	holidayRes := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions", managerClaims, "李主管", CreateOperatingExceptionRequest{
		Title:            "非维保休假停机",
		Kind:             "HOLIDAY_SHUTDOWN",
		StartDate:        "2026-11-15",
		EndDate:          "2026-11-15",
		ReplacementSlots: []OperatingExceptionSlot{},
		AutoApprove:      true,
		Meta:             MutationMeta{ExpectedRevision: 0, IdempotencyKey: "holiday-approved-0001", Reason: "休假停机测试"},
	})
	if holidayRes.Code != http.StatusCreated {
		t.Fatalf("create holiday exception status=%d body=%s", holidayRes.Code, holidayRes.Body.String())
	}
	assertOmitted(t, "approved_holiday_shutdown")

	// Approve MAINTENANCE_WINDOW -> cross-midnight site local date (2026-11-16) makes it active on GET & PUT.
	approveRes := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions/"+maintExc.ID+"/decide", managerClaims, "李主管", DecideOperatingExceptionRequest{
		Decision:     "APPROVE",
		DecisionNote: "同意维保停机",
		Meta:         MutationMeta{ExpectedRevision: maintExc.Revision, IdempotencyKey: "maint-approve-0001", Reason: "审批维保停机窗口"},
	})
	if approveRes.Code != http.StatusOK {
		t.Fatalf("approve maintenance exception status=%d body=%s", approveRes.Code, approveRes.Body.String())
	}
	var approvedExc OperatingException
	if err := json.Unmarshal(approveRes.Body.Bytes(), &approvedExc); err != nil {
		t.Fatalf("decode approved maintenance exception: %v", err)
	}

	getActive := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/control-state", readClaims, "", nil)
	if getActive.Code != http.StatusOK {
		t.Fatalf("GET active control-state status=%d body=%s", getActive.Code, getActive.Body.String())
	}
	var activeState SiteControlState
	if err := json.Unmarshal(getActive.Body.Bytes(), &activeState); err != nil {
		t.Fatalf("decode active control-state: %v", err)
	}
	if activeState.ActiveMaintenanceWindow == nil ||
		activeState.ActiveMaintenanceWindow.ID != maintExc.ID ||
		activeState.ActiveMaintenanceWindow.Title != "1号冷机清洗检修" ||
		activeState.ActiveMaintenanceWindow.StartDate != "2026-11-16" ||
		activeState.ActiveMaintenanceWindow.EndDate != "2026-11-16" ||
		activeState.ActiveMaintenanceWindow.Reason != "冷却水塔停机清洗" {
		t.Fatalf("unexpected activeMaintenanceWindow on GET: %#v", activeState.ActiveMaintenanceWindow)
	}

	// PUT control-mode also populates activeMaintenanceWindow in the mutation response.
	putResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/control-mode", managerClaims, "", map[string]any{
		"controlMode": SiteControlModeManualOverride,
		"reason":      "维保窗口内记录模式切换",
		"meta":        map[string]any{"expectedRevision": 0, "idempotencyKey": "maint-mode-0001", "reason": "维保窗口内记录模式切换"},
	})
	if putResp.Code != http.StatusOK {
		t.Fatalf("PUT control-mode status=%d body=%s", putResp.Code, putResp.Body.String())
	}
	var putState SiteControlState
	if err := json.Unmarshal(putResp.Body.Bytes(), &putState); err != nil {
		t.Fatalf("decode PUT control-mode state: %v", err)
	}
	if putState.ActiveMaintenanceWindow == nil || putState.ActiveMaintenanceWindow.ID != maintExc.ID {
		t.Fatalf("expected activeMaintenanceWindow on PUT control-mode, got %#v", putState.ActiveMaintenanceWindow)
	}

	// 1 hour earlier (2026-11-15T15:30Z = 2026-11-15 23:30+08:00): out of window in site timezone -> omitted.
	fixedNow = time.Date(2026, 11, 15, 15, 30, 0, 0, time.UTC)
	assertOmitted(t, "before_site_midnight")

	// Back in active date and cancel exception -> omitted again.
	fixedNow = time.Date(2026, 11, 15, 16, 30, 0, 0, time.UTC)
	cancelRes := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions/"+maintExc.ID+"/decide", managerClaims, "李主管", DecideOperatingExceptionRequest{
		Decision:     "CANCEL",
		DecisionNote: "提前完工取消",
		Meta:         MutationMeta{ExpectedRevision: approvedExc.Revision, IdempotencyKey: "maint-cancel-0001", Reason: "维保提前结束"},
	})
	if cancelRes.Code != http.StatusOK {
		t.Fatalf("cancel maintenance exception status=%d body=%s", cancelRes.Code, cancelRes.Body.String())
	}
	assertOmitted(t, "after_cancel")
}
