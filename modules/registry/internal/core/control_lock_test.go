package core

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	testControlAssetChiller1 = "018f1e00-7700-7000-8000-000000000001"
	testControlDevicePLC1    = "018f1e00-7700-7000-8000-000000000101"
)

type memoryControlLockBackend struct {
	*memoryScheduleBackend
	lockMu       sync.Mutex
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

func (backend *memoryControlLockBackend) GetSiteControlState(_ context.Context, claims registryauth.GrantClaims, siteID string) (SiteControlState, error) {
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
	return state, nil
}

func (backend *memoryControlLockBackend) SaveSiteControlMode(_ context.Context, claims registryauth.GrantClaims, input SiteControlModeMutation) (SiteControlState, bool, error) {
	if err := input.Validate(); err != nil {
		return SiteControlState{}, false, err
	}
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
		ID:          &id,
		TenantID:    claims.TenantID,
		SiteID:      input.SiteID,
		ControlMode: input.ControlMode,
		Reason:      effectiveControlReason(input.Reason, input.Meta.Reason),
		UpdatedBy:   &updatedBy,
		UpdatedAt:   &updatedAt,
		Revision:    currentRev + 1,
		AssetLocks:  []AssetControlLockState{},
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
