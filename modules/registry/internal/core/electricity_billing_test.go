package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type memoryBillingBackend struct {
	fakeRegistryStore
	mu      sync.Mutex
	budgets map[string]map[string]MonthlyElectricityBudget
	bills   map[string]map[string]UtilityBill
	seq     int
}

func newMemoryBillingBackend() *memoryBillingBackend {
	return &memoryBillingBackend{
		fakeRegistryStore: fakeRegistryStore{
			site: Site{
				ID: testSiteA1, TenantID: testTenantA, Code: "songyaxuan", DisplayName: "松雅轩", Timezone: "Asia/Shanghai", Status: "ACTIVE",
			},
		},
		budgets: map[string]map[string]MonthlyElectricityBudget{},
		bills:   map[string]map[string]UtilityBill{},
	}
}

func (backend *memoryBillingBackend) siteVisible(claims registryauth.GrantClaims, siteID string) bool {
	if siteID != testSiteA1 {
		return false
	}
	for _, denied := range claims.DeniedSiteIDs {
		if denied == siteID {
			return false
		}
	}
	for _, allowed := range claims.AllowedSiteIDs {
		if allowed == siteID {
			return true
		}
	}
	return false
}

func (backend *memoryBillingBackend) ListMonthlyElectricityBudgets(_ context.Context, claims registryauth.GrantClaims, siteID string, filter MonthlyElectricityBudgetFilter) (MonthlyElectricityBudgetList, error) {
	if err := filter.Validate(); err != nil {
		return MonthlyElectricityBudgetList{}, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return MonthlyElectricityBudgetList{}, ErrNotFound
	}
	items := []MonthlyElectricityBudget{}
	for _, item := range backend.budgets[siteID] {
		if filter.BillingMonth != "" && item.BillingMonth != filter.BillingMonth {
			continue
		}
		if filter.FromMonth != "" && item.BillingMonth < filter.FromMonth {
			continue
		}
		if filter.ToMonth != "" && item.BillingMonth > filter.ToMonth {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].BillingMonth > items[j].BillingMonth
	})
	return MonthlyElectricityBudgetList{Items: items}, nil
}

func (backend *memoryBillingBackend) SaveMonthlyElectricityBudget(_ context.Context, claims registryauth.GrantClaims, input MonthlyElectricityBudgetMutation) (MonthlyElectricityBudget, bool, error) {
	if err := input.Validate(); err != nil {
		return MonthlyElectricityBudget{}, false, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return MonthlyElectricityBudget{}, false, ErrNotFound
	}
	if backend.budgets[input.SiteID] == nil {
		backend.budgets[input.SiteID] = map[string]MonthlyElectricityBudget{}
	}
	current, exists := backend.budgets[input.SiteID][input.BillingMonth]
	var currentRev int64
	if exists {
		currentRev = current.Revision
	}
	if currentRev != input.Meta.ExpectedRevision {
		return MonthlyElectricityBudget{}, false, ErrRevisionConflict
	}
	now := "2026-10-12T08:00:00.000Z"
	id := current.ID
	createdAt := current.CreatedAt
	if !exists {
		backend.seq++
		id = fmt.Sprintf("01990000-9100-7000-8000-%012d", backend.seq)
		createdAt = now
	}
	display := normalizeActorDisplay(input.ActorDisplayName, claims.PrincipalID)
	saved := MonthlyElectricityBudget{
		ID:               id,
		TenantID:         testTenantA,
		SiteID:           input.SiteID,
		BillingMonth:     input.BillingMonth,
		BudgetAmountCny:  roundCents(input.BudgetAmountCny),
		Note:             input.Note,
		Revision:         currentRev + 1,
		UpdatedBy:        claims.PrincipalID,
		UpdatedByDisplay: display,
		CreatedAt:        createdAt,
		UpdatedAt:        now,
	}
	backend.budgets[input.SiteID][input.BillingMonth] = saved
	return saved, false, nil
}

func (backend *memoryBillingBackend) ListUtilityBills(_ context.Context, claims registryauth.GrantClaims, siteID string, filter UtilityBillFilter) (UtilityBillList, error) {
	if err := filter.Validate(); err != nil {
		return UtilityBillList{}, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return UtilityBillList{}, ErrNotFound
	}
	items := []UtilityBill{}
	for _, item := range backend.bills[siteID] {
		if filter.BillingMonth != "" && item.BillingMonth != filter.BillingMonth {
			continue
		}
		if filter.MeterScope != "" && item.MeterScope != filter.MeterScope {
			continue
		}
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].BillingMonth != items[j].BillingMonth {
			return items[i].BillingMonth > items[j].BillingMonth
		}
		return items[i].ID > items[j].ID
	})
	return UtilityBillList{Items: items}, nil
}

func (backend *memoryBillingBackend) SaveUtilityBill(_ context.Context, claims registryauth.GrantClaims, input UtilityBillMutation) (UtilityBill, bool, error) {
	if err := input.Validate(); err != nil {
		return UtilityBill{}, false, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return UtilityBill{}, false, ErrNotFound
	}
	if backend.bills[input.SiteID] == nil {
		backend.bills[input.SiteID] = map[string]UtilityBill{}
	}
	now := "2026-10-12T08:00:00.000Z"
	display := normalizeActorDisplay(input.ActorDisplayName, claims.PrincipalID)
	var id, createdAt string
	var nextRev int64
	if input.ID == "" {
		backend.seq++
		id = fmt.Sprintf("01990000-9200-7000-8000-%012d", backend.seq)
		createdAt = now
		nextRev = 1
	} else {
		current, exists := backend.bills[input.SiteID][input.ID]
		if !exists {
			return UtilityBill{}, false, ErrNotFound
		}
		if current.Status != UtilityBillStatusDraft {
			return UtilityBill{}, false, ErrUtilityBillStateConflict
		}
		if current.Revision != input.Meta.ExpectedRevision {
			return UtilityBill{}, false, ErrRevisionConflict
		}
		id = current.ID
		createdAt = current.CreatedAt
		nextRev = current.Revision + 1
	}
	saved := UtilityBill{
		ID:                        id,
		TenantID:                  testTenantA,
		SiteID:                    input.SiteID,
		BillingMonth:              input.BillingMonth,
		ReadingStartDate:          input.ReadingStartDate,
		ReadingEndDate:            input.ReadingEndDate,
		MeterScope:                input.MeterScope,
		BilledEnergyKwh:           input.BilledEnergyKwh,
		TouBreakdown:              normalizeTouBreakdown(input.TouBreakdown),
		EnergyChargeCny:           roundCents(input.EnergyChargeCny),
		BasicChargeMode:           input.BasicChargeMode,
		BasicChargeQuantity:       input.BasicChargeQuantity,
		BasicChargeCny:            roundCents(input.BasicChargeCny),
		StandardPowerFactor:       input.StandardPowerFactor,
		ActualPowerFactor:         input.ActualPowerFactor,
		PowerFactorAdjustmentCny:  roundCents(input.PowerFactorAdjustmentCny),
		OtherChargesCny:           roundCents(input.OtherChargesCny),
		TotalPayableCny:           roundCents(input.TotalPayableCny),
		ChillerShareOverrideRatio: input.ChillerShareOverrideRatio,
		UtilityAccountNo:          input.UtilityAccountNo,
		InvoiceNo:                 input.InvoiceNo,
		Note:                      input.Note,
		Status:                    UtilityBillStatusDraft,
		UpdatedBy:                 claims.PrincipalID,
		UpdatedByDisplay:          display,
		Revision:                  nextRev,
		CreatedAt:                 createdAt,
		UpdatedAt:                 now,
	}
	backend.bills[input.SiteID][id] = saved
	return saved, false, nil
}

func (backend *memoryBillingBackend) ConfirmUtilityBill(_ context.Context, claims registryauth.GrantClaims, input ConfirmUtilityBillRequest) (UtilityBill, bool, error) {
	if err := input.Validate(); err != nil {
		return UtilityBill{}, false, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return UtilityBill{}, false, ErrNotFound
	}
	current, exists := backend.bills[input.SiteID][input.BillID]
	if !exists {
		return UtilityBill{}, false, ErrNotFound
	}
	if current.Status != UtilityBillStatusDraft {
		return UtilityBill{}, false, ErrUtilityBillStateConflict
	}
	if current.Revision != input.Meta.ExpectedRevision {
		return UtilityBill{}, false, ErrRevisionConflict
	}
	for _, other := range backend.bills[input.SiteID] {
		if other.ID != current.ID && other.BillingMonth == current.BillingMonth && other.MeterScope == current.MeterScope && other.Status == UtilityBillStatusConfirmed {
			return UtilityBill{}, false, ErrBindingConflict
		}
	}
	now := "2026-10-12T09:00:00.000Z"
	display := normalizeActorDisplay(input.ActorDisplayName, claims.PrincipalID)
	confirmedBy := claims.PrincipalID
	current.Status = UtilityBillStatusConfirmed
	current.ConfirmedBy = &confirmedBy
	current.ConfirmedByDisplay = &display
	current.ConfirmedAt = &now
	if input.Note != nil {
		current.Note = *input.Note
	}
	current.UpdatedBy = claims.PrincipalID
	current.UpdatedByDisplay = display
	current.Revision++
	current.UpdatedAt = now
	backend.bills[input.SiteID][current.ID] = current
	return current, false, nil
}

func (*memoryBillingBackend) SaveSite(context.Context, registryauth.GrantClaims, SiteMutation) (Site, bool, error) {
	return Site{}, false, ErrInvalidMutation
}
func (*memoryBillingBackend) SaveSpace(context.Context, registryauth.GrantClaims, SpaceMutation) (Space, bool, error) {
	return Space{}, false, ErrInvalidMutation
}
func (*memoryBillingBackend) SaveAsset(context.Context, registryauth.GrantClaims, AssetMutation) (Asset, bool, error) {
	return Asset{}, false, ErrInvalidMutation
}
func (*memoryBillingBackend) SaveDevice(context.Context, registryauth.GrantClaims, DeviceMutation) (Device, bool, error) {
	return Device{}, false, ErrInvalidMutation
}
func (*memoryBillingBackend) SaveSensor(context.Context, registryauth.GrantClaims, SensorMutation) (Sensor, bool, error) {
	return Sensor{}, false, ErrInvalidMutation
}
func (*memoryBillingBackend) SavePoint(context.Context, registryauth.GrantClaims, PointMutation) (TelemetryPoint, bool, error) {
	return TelemetryPoint{}, false, ErrInvalidMutation
}
func (*memoryBillingBackend) Rebind(context.Context, registryauth.GrantClaims, RebindRequest) (BindingMutationResult, error) {
	return BindingMutationResult{}, ErrInvalidMutation
}
func (*memoryBillingBackend) ReleaseTemplate(context.Context, registryauth.GrantClaims, ReleaseTemplateRequest) (TemplateRevision, bool, error) {
	return TemplateRevision{}, false, ErrInvalidMutation
}
func (*memoryBillingBackend) AssignTemplate(context.Context, registryauth.GrantClaims, AssignTemplateRequest) (TemplateAssignment, bool, error) {
	return TemplateAssignment{}, false, ErrInvalidMutation
}
func (*memoryBillingBackend) PlanImport(context.Context, registryauth.GrantClaims, ImportPlanRequest) (ImportPlan, error) {
	return ImportPlan{}, ErrInvalidMutation
}
func (*memoryBillingBackend) CommitImport(context.Context, registryauth.GrantClaims, ImportCommitRequest) (ImportCommitResult, error) {
	return ImportCommitResult{}, ErrInvalidMutation
}
func (*memoryBillingBackend) Retire(context.Context, registryauth.GrantClaims, RetireRequest) (RetirementResult, error) {
	return RetirementResult{}, ErrInvalidMutation
}
func (*memoryBillingBackend) SaveOperatingSchedule(context.Context, registryauth.GrantClaims, OperatingScheduleMutation) (OperatingSchedule, bool, error) {
	return OperatingSchedule{}, false, ErrInvalidMutation
}
func (*memoryBillingBackend) CreateOperatingException(context.Context, registryauth.GrantClaims, CreateOperatingExceptionRequest) (OperatingException, bool, error) {
	return OperatingException{}, false, ErrInvalidMutation
}
func (*memoryBillingBackend) DecideOperatingException(context.Context, registryauth.GrantClaims, DecideOperatingExceptionRequest) (OperatingException, bool, error) {
	return OperatingException{}, false, ErrInvalidMutation
}

func newBillingHTTPClient(t *testing.T, backend *memoryBillingBackend) scheduleHTTPClient {
	t.Helper()
	return newScheduleHTTPClientWithInterfaces(t, backend, backend)
}

func TestElectricityBillingBudgetLifecycleAndAuthorization(t *testing.T) {
	backend := newMemoryBillingBackend()
	client := newBillingHTTPClient(t, backend)

	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	manageClaims := testGrantClaims(registryauth.ActionBillingManage)

	// Site read without billing.manage cannot write budgets.
	forbiddenResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/electricity-budgets", readClaims, "观察员", map[string]any{
		"billingMonth":    "2026-10",
		"budgetAmountCny": 60000,
		"note":            "十月冷站预算",
		"meta":            map[string]any{"expectedRevision": 0, "idempotencyKey": "budget-forbid-0001", "reason": "设置预算"},
	})
	if forbiddenResp.Code != http.StatusForbidden {
		t.Fatalf("read-only PUT budget status=%d, want 403; body=%s", forbiddenResp.Code, forbiddenResp.Body.String())
	}

	// Create 2026-10 budget (expectedRevision = 0 -> revision = 1).
	createResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/electricity-budgets", manageClaims, "李主管", map[string]any{
		"billingMonth":    "2026-10",
		"budgetAmountCny": 60000,
		"note":            "十月冷站预算",
		"meta":            map[string]any{"expectedRevision": 0, "idempotencyKey": "budget-create-0001", "reason": "年初核定十月预算"},
	})
	if createResp.Code != http.StatusOK {
		t.Fatalf("create budget status=%d, want 200; body=%s", createResp.Code, createResp.Body.String())
	}
	var created MonthlyElectricityBudget
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.BillingMonth != "2026-10" || created.BudgetAmountCny != 60000 || created.Revision != 1 || created.UpdatedByDisplay != "李主管" {
		t.Fatalf("unexpected created budget: %#v", created)
	}

	// Stale revision conflicts with 409 REGISTRY_REVISION_CONFLICT.
	conflictResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/electricity-budgets", manageClaims, "李主管", map[string]any{
		"billingMonth":    "2026-10",
		"budgetAmountCny": 65000,
		"note":            "追加预算",
		"meta":            map[string]any{"expectedRevision": 0, "idempotencyKey": "budget-conflict-0001", "reason": "过期版本测试"},
	})
	if conflictResp.Code != http.StatusConflict {
		t.Fatalf("stale budget PUT status=%d, want 409; body=%s", conflictResp.Code, conflictResp.Body.String())
	}

	// Update revision 1 -> 2 and list via site.read.
	updateResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/electricity-budgets", manageClaims, "李主管", map[string]any{
		"billingMonth":    "2026-10",
		"budgetAmountCny": 62500.50,
		"note":            "十月高温修正预算",
		"meta":            map[string]any{"expectedRevision": 1, "idempotencyKey": "budget-update-0002", "reason": "秋老虎追加预算"},
	})
	if updateResp.Code != http.StatusOK {
		t.Fatalf("update budget status=%d, want 200; body=%s", updateResp.Code, updateResp.Body.String())
	}

	listResp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/electricity-budgets?billingMonth=2026-10", readClaims, "", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list budgets status=%d, want 200; body=%s", listResp.Code, listResp.Body.String())
	}
	var list MonthlyElectricityBudgetList
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || math.Abs(list.Items[0].BudgetAmountCny-62500.50) > 1e-6 || list.Items[0].Revision != 2 {
		t.Fatalf("unexpected listed budgets: %#v", list.Items)
	}
}

func TestElectricityBillingUtilityBillValidationConfirmAndLocking(t *testing.T) {
	backend := newMemoryBillingBackend()
	client := newBillingHTTPClient(t, backend)

	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	manageClaims := testGrantClaims(registryauth.ActionBillingManage)

	// Four-part sum mismatch must fail with 400 REGISTRY_MUTATION_INVALID.
	badSumResp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/utility-bills", manageClaims, "财务王工", map[string]any{
		"billingMonth":             "2026-10",
		"readingStartDate":         "2026-09-25",
		"readingEndDate":           "2026-10-24",
		"meterScope":               "WHOLE_BUILDING",
		"billedEnergyKwh":          200000,
		"energyChargeCny":          142000.00,
		"basicChargeMode":          "MAX_DEMAND_KW",
		"basicChargeQuantity":      800,
		"basicChargeCny":           25600.00,
		"standardPowerFactor":      0.90,
		"actualPowerFactor":        0.95,
		"powerFactorAdjustmentCny": -1257.00,
		"otherChargesCny":          357.00,
		"totalPayableCny":          167000.00, // should be 166700.00
		"meta":                     map[string]any{"expectedRevision": 0, "idempotencyKey": "bill-bad-sum-0001", "reason": "测试总额不平"},
	})
	if badSumResp.Code != http.StatusBadRequest {
		t.Fatalf("mismatched four-part total status=%d, want 400; body=%s", badSumResp.Code, badSumResp.Body.String())
	}

	// Chiller share override on CHILLER_PLANT must fail with 400.
	badOverrideResp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/utility-bills", manageClaims, "财务王工", map[string]any{
		"billingMonth":              "2026-10",
		"readingStartDate":          "2026-09-25",
		"readingEndDate":            "2026-10-24",
		"meterScope":                "CHILLER_PLANT",
		"billedEnergyKwh":           85000,
		"energyChargeCny":           61200.00,
		"basicChargeMode":           "NONE",
		"basicChargeCny":            0,
		"powerFactorAdjustmentCny":  0,
		"otherChargesCny":           0,
		"totalPayableCny":           61200.00,
		"chillerShareOverrideRatio": 0.45,
		"meta":                      map[string]any{"expectedRevision": 0, "idempotencyKey": "bill-bad-override-0001", "reason": "专变不允许人工分摊比例"},
	})
	if badOverrideResp.Code != http.StatusBadRequest {
		t.Fatalf("chillerShareOverrideRatio on CHILLER_PLANT status=%d, want 400; body=%s", badOverrideResp.Code, badOverrideResp.Body.String())
	}

	// Create valid WHOLE_BUILDING draft bill with cross-month reading window and negative power-factor reward.
	createResp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/utility-bills", manageClaims, "财务王工", map[string]any{
		"billingMonth":     "2026-10",
		"readingStartDate": "2026-09-25",
		"readingEndDate":   "2026-10-24",
		"meterScope":       "WHOLE_BUILDING",
		"billedEnergyKwh":  200000,
		"touBreakdown": map[string]any{
			"sharpKwh":  15000,
			"peakKwh":   75000,
			"flatKwh":   70000,
			"valleyKwh": 40000,
		},
		"energyChargeCny":           142000.00,
		"basicChargeMode":           "MAX_DEMAND_KW",
		"basicChargeQuantity":       800,
		"basicChargeCny":            25600.00,
		"standardPowerFactor":       0.90,
		"actualPowerFactor":         0.95,
		"powerFactorAdjustmentCny":  -1257.00,
		"otherChargesCny":           357.00,
		"totalPayableCny":           166700.00,
		"chillerShareOverrideRatio": 0.42,
		"utilityAccountNo":          "GD-0755-882910",
		"invoiceNo":                 "INV-202610-001",
		"note":                      "供电局十月两部制电费单草稿",
		"meta":                      map[string]any{"expectedRevision": 0, "idempotencyKey": "bill-create-0001", "reason": "录入十月电费单"},
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create utility bill status=%d, want 201; body=%s", createResp.Code, createResp.Body.String())
	}
	var draft UtilityBill
	if err := json.NewDecoder(createResp.Body).Decode(&draft); err != nil {
		t.Fatal(err)
	}
	if draft.Status != "DRAFT" || draft.Revision != 1 || draft.PowerFactorAdjustmentCny != -1257.00 || draft.TotalPayableCny != 166700.00 {
		t.Fatalf("unexpected draft utility bill: %#v", draft)
	}

	// Edit DRAFT bill (revision 1 -> 2).
	editResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/utility-bills/"+draft.ID, manageClaims, "财务王工", map[string]any{
		"billingMonth":             "2026-10",
		"readingStartDate":         "2026-09-25",
		"readingEndDate":           "2026-10-24",
		"meterScope":               "WHOLE_BUILDING",
		"billedEnergyKwh":          200000,
		"energyChargeCny":          142000.00,
		"basicChargeMode":          "MAX_DEMAND_KW",
		"basicChargeQuantity":      800,
		"basicChargeCny":           25600.00,
		"standardPowerFactor":      0.90,
		"actualPowerFactor":        0.95,
		"powerFactorAdjustmentCny": -1257.00,
		"otherChargesCny":          457.00,
		"totalPayableCny":          166800.00,
		"utilityAccountNo":         "GD-0755-882910",
		"invoiceNo":                "INV-202610-001",
		"note":                     "核对附加费后更新草稿",
		"meta":                     map[string]any{"expectedRevision": 1, "idempotencyKey": "bill-edit-0002", "reason": "修正政府性基金附加"},
	})
	if editResp.Code != http.StatusOK {
		t.Fatalf("edit draft bill status=%d, want 200; body=%s", editResp.Code, editResp.Body.String())
	}

	// Confirm bill (revision 2 -> 3, status = CONFIRMED).
	confirmResp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/utility-bills/"+draft.ID+"/confirm", manageClaims, "李主管", map[string]any{
		"note": "核对无误，正式归档",
		"meta": map[string]any{"expectedRevision": 2, "idempotencyKey": "bill-confirm-0003", "reason": "确认十月整栋结算电费单"},
	})
	if confirmResp.Code != http.StatusOK {
		t.Fatalf("confirm bill status=%d, want 200; body=%s", confirmResp.Code, confirmResp.Body.String())
	}
	var confirmed UtilityBill
	if err := json.NewDecoder(confirmResp.Body).Decode(&confirmed); err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != "CONFIRMED" || confirmed.Revision != 3 || confirmed.ConfirmedByDisplay == nil || *confirmed.ConfirmedByDisplay != "李主管" {
		t.Fatalf("unexpected confirmed utility bill: %#v", confirmed)
	}

	// Editing a CONFIRMED bill must fail with 409 UTILITY_BILL_STATE_CONFLICT.
	lockedEditResp := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/utility-bills/"+draft.ID, manageClaims, "财务王工", map[string]any{
		"billingMonth":             "2026-10",
		"readingStartDate":         "2026-09-25",
		"readingEndDate":           "2026-10-24",
		"meterScope":               "WHOLE_BUILDING",
		"billedEnergyKwh":          200000,
		"energyChargeCny":          142000.00,
		"basicChargeMode":          "MAX_DEMAND_KW",
		"basicChargeQuantity":      800,
		"basicChargeCny":           25600.00,
		"powerFactorAdjustmentCny": -1257.00,
		"otherChargesCny":          457.00,
		"totalPayableCny":          166800.00,
		"meta":                     map[string]any{"expectedRevision": 3, "idempotencyKey": "bill-edit-locked-0004", "reason": "尝试篡改已归档账单"},
	})
	if lockedEditResp.Code != http.StatusConflict {
		t.Fatalf("editing confirmed bill status=%d, want 409; body=%s", lockedEditResp.Code, lockedEditResp.Body.String())
	}
	var lockedProblem struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(lockedEditResp.Body).Decode(&lockedProblem)
	if lockedProblem.Code != "UTILITY_BILL_STATE_CONFLICT" {
		t.Fatalf("locked bill problem code=%q, want UTILITY_BILL_STATE_CONFLICT", lockedProblem.Code)
	}

	// Creating a second draft for the same (billingMonth, meterScope) succeeds, but confirming it conflicts with 409 REGISTRY_BINDING_CONFLICT.
	secondDraftResp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/utility-bills", manageClaims, "财务王工", map[string]any{
		"billingMonth":             "2026-10",
		"readingStartDate":         "2026-09-25",
		"readingEndDate":           "2026-10-24",
		"meterScope":               "WHOLE_BUILDING",
		"billedEnergyKwh":          201000,
		"energyChargeCny":          142500.00,
		"basicChargeMode":          "NONE",
		"basicChargeCny":           0,
		"powerFactorAdjustmentCny": 0,
		"otherChargesCny":          0,
		"totalPayableCny":          142500.00,
		"meta":                     map[string]any{"expectedRevision": 0, "idempotencyKey": "bill-create-dup-0005", "reason": "测试同月重复草稿"},
	})
	if secondDraftResp.Code != http.StatusCreated {
		t.Fatalf("second draft status=%d, want 201; body=%s", secondDraftResp.Code, secondDraftResp.Body.String())
	}
	var secondDraft UtilityBill
	_ = json.NewDecoder(secondDraftResp.Body).Decode(&secondDraft)

	dupConfirmResp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/utility-bills/"+secondDraft.ID+"/confirm", manageClaims, "李主管", map[string]any{
		"meta": map[string]any{"expectedRevision": 1, "idempotencyKey": "bill-confirm-dup-0006", "reason": "尝试重复归档同月同口径"},
	})
	if dupConfirmResp.Code != http.StatusConflict {
		t.Fatalf("duplicate confirm status=%d, want 409; body=%s", dupConfirmResp.Code, dupConfirmResp.Body.String())
	}
	var dupProblem struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(dupConfirmResp.Body).Decode(&dupProblem)
	if dupProblem.Code != "REGISTRY_BINDING_CONFLICT" {
		t.Fatalf("duplicate confirm problem code=%q, want REGISTRY_BINDING_CONFLICT", dupProblem.Code)
	}

	// Read list filtered by status=CONFIRMED via site.read returns exactly the confirmed bill.
	confirmedListResp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/utility-bills?billingMonth=2026-10&status=CONFIRMED", readClaims, "", nil)
	if confirmedListResp.Code != http.StatusOK {
		t.Fatalf("list confirmed bills status=%d, want 200; body=%s", confirmedListResp.Code, confirmedListResp.Body.String())
	}
	var billList UtilityBillList
	if err := json.NewDecoder(confirmedListResp.Body).Decode(&billList); err != nil {
		t.Fatal(err)
	}
	if len(billList.Items) != 1 || billList.Items[0].ID != draft.ID {
		t.Fatalf("unexpected confirmed bill list: %#v", billList.Items)
	}
}
