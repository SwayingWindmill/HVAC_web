package core

import (
	"errors"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestPostgresElectricityBudgetsAndUtilityBillsLifecycle(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)

	readClaims := writerIntegrationClaims(registryauth.ActionSiteRead, testSiteA1)
	manageClaims := writerIntegrationClaims(registryauth.ActionBillingManage, testSiteA1)

	initialBudgets, err := store.ListMonthlyElectricityBudgets(ctx, readClaims, testSiteA1, MonthlyElectricityBudgetFilter{})
	if err != nil || len(initialBudgets.Items) != 0 {
		t.Fatalf("initial ListMonthlyElectricityBudgets=%#v err=%v", initialBudgets, err)
	}

	budgetSave1 := MonthlyElectricityBudgetMutation{
		SiteID:           testSiteA1,
		ActorDisplayName: "李主管",
		BillingMonth:     "2026-10",
		BudgetAmountCny:  60000.00,
		Note:             "十月冷站预算",
		Meta:             MutationMeta{ExpectedRevision: 0, IdempotencyKey: "pg-budget-save-0001", Reason: "核定十月预算"},
	}
	b1, replayed, err := store.SaveMonthlyElectricityBudget(ctx, manageClaims, budgetSave1)
	if err != nil || replayed || b1.Revision != 1 || b1.BudgetAmountCny != 60000.00 {
		t.Fatalf("SaveMonthlyElectricityBudget v1=%#v replayed=%v err=%v", b1, replayed, err)
	}

	// Idempotent replay does not bump revision.
	bReplay, replayed, err := store.SaveMonthlyElectricityBudget(ctx, manageClaims, budgetSave1)
	if err != nil || !replayed || bReplay.Revision != 1 {
		t.Fatalf("replayed SaveMonthlyElectricityBudget=%#v replayed=%v err=%v", bReplay, replayed, err)
	}

	// Revision 1 -> 2 update.
	b2, replayed, err := store.SaveMonthlyElectricityBudget(ctx, manageClaims, MonthlyElectricityBudgetMutation{
		SiteID:           testSiteA1,
		ActorDisplayName: "李主管",
		BillingMonth:     "2026-10",
		BudgetAmountCny:  63500.25,
		Note:             "十月高温调整预算",
		Meta:             MutationMeta{ExpectedRevision: 1, IdempotencyKey: "pg-budget-save-0002", Reason: "调整预算"},
	})
	if err != nil || replayed || b2.Revision != 2 || b2.BudgetAmountCny != 63500.25 {
		t.Fatalf("SaveMonthlyElectricityBudget v2=%#v replayed=%v err=%v", b2, replayed, err)
	}

	demandKw := 820.0
	stdPf := 0.90
	actPf := 0.952
	shareOverride := 0.425
	sharpKwh := 12000.0
	peakKwh := 78000.0
	flatKwh := 70000.0
	valleyKwh := 40000.0

	draft1, replayed, err := store.SaveUtilityBill(ctx, manageClaims, UtilityBillMutation{
		SiteID:           testSiteA1,
		ActorDisplayName: "财务王工",
		BillingMonth:     "2026-10",
		ReadingStartDate: "2026-09-25",
		ReadingEndDate:   "2026-10-24",
		MeterScope:       UtilityBillScopeWholeBuilding,
		BilledEnergyKwh:  200000,
		TouBreakdown: &UtilityBillTouBreakdown{
			SharpKwh:  &sharpKwh,
			PeakKwh:   &peakKwh,
			FlatKwh:   &flatKwh,
			ValleyKwh: &valleyKwh,
		},
		EnergyChargeCny:           142000.00,
		BasicChargeMode:           BasicChargeModeMaxDemandKW,
		BasicChargeQuantity:       &demandKw,
		BasicChargeCny:            26240.00,
		StandardPowerFactor:       &stdPf,
		ActualPowerFactor:         &actPf,
		PowerFactorAdjustmentCny:  -1261.80,
		OtherChargesCny:           321.80,
		TotalPayableCny:           167300.00,
		ChillerShareOverrideRatio: &shareOverride,
		UtilityAccountNo:          "GD-0755-882910",
		InvoiceNo:                 "INV-202610-001",
		Note:                      "整栋总表十月结算单",
		Meta:                      MutationMeta{ExpectedRevision: 0, IdempotencyKey: "pg-bill-create-0001", Reason: "录入十月电费单草稿"},
	})
	if err != nil || replayed || draft1.Status != UtilityBillStatusDraft || draft1.Revision != 1 || draft1.PowerFactorAdjustmentCny != -1261.80 {
		t.Fatalf("SaveUtilityBill draft=%#v replayed=%v err=%v", draft1, replayed, err)
	}

	confirmNote := "已与财务付款凭单核对一致"
	confirmed1, replayed, err := store.ConfirmUtilityBill(ctx, manageClaims, ConfirmUtilityBillRequest{
		SiteID:           testSiteA1,
		BillID:           draft1.ID,
		ActorDisplayName: "李主管",
		Note:             &confirmNote,
		Meta:             MutationMeta{ExpectedRevision: 1, IdempotencyKey: "pg-bill-confirm-0001", Reason: "确认归档十月电费单"},
	})
	if err != nil || replayed || confirmed1.Status != UtilityBillStatusConfirmed || confirmed1.Revision != 2 {
		t.Fatalf("ConfirmUtilityBill=%#v replayed=%v err=%v", confirmed1, replayed, err)
	}

	// Attempting to edit a CONFIRMED bill fails with ErrUtilityBillStateConflict.
	_, _, err = store.SaveUtilityBill(ctx, manageClaims, UtilityBillMutation{
		ID:                       draft1.ID,
		SiteID:                   testSiteA1,
		ActorDisplayName:         "财务王工",
		BillingMonth:             "2026-10",
		ReadingStartDate:         "2026-09-25",
		ReadingEndDate:           "2026-10-24",
		MeterScope:               UtilityBillScopeWholeBuilding,
		BilledEnergyKwh:          200000,
		EnergyChargeCny:          142000.00,
		BasicChargeMode:          BasicChargeModeMaxDemandKW,
		BasicChargeQuantity:      &demandKw,
		BasicChargeCny:           26240.00,
		PowerFactorAdjustmentCny: -1261.80,
		OtherChargesCny:          321.80,
		TotalPayableCny:          167300.00,
		Meta:                     MutationMeta{ExpectedRevision: 2, IdempotencyKey: "pg-bill-edit-locked-0001", Reason: "尝试修改已归档账单"},
	})
	if !errors.Is(err, ErrUtilityBillStateConflict) {
		t.Fatalf("edit confirmed bill err=%v, want ErrUtilityBillStateConflict", err)
	}

	// Creating another WHOLE_BUILDING draft for 2026-10 and confirming it triggers duplicate-confirmed conflict -> ErrBindingConflict.
	draft2, _, err := store.SaveUtilityBill(ctx, manageClaims, UtilityBillMutation{
		SiteID:                   testSiteA1,
		ActorDisplayName:         "财务王工",
		BillingMonth:             "2026-10",
		ReadingStartDate:         "2026-09-25",
		ReadingEndDate:           "2026-10-24",
		MeterScope:               UtilityBillScopeWholeBuilding,
		BilledEnergyKwh:          205000,
		EnergyChargeCny:          145000.00,
		BasicChargeMode:          BasicChargeModeNone,
		BasicChargeCny:           0,
		PowerFactorAdjustmentCny: 0,
		OtherChargesCny:          0,
		TotalPayableCny:          145000.00,
		Meta:                     MutationMeta{ExpectedRevision: 0, IdempotencyKey: "pg-bill-create-dup-0002", Reason: "第二张同月整栋草稿"},
	})
	if err != nil {
		t.Fatalf("create second draft err=%v", err)
	}
	_, _, err = store.ConfirmUtilityBill(ctx, manageClaims, ConfirmUtilityBillRequest{
		SiteID:           testSiteA1,
		BillID:           draft2.ID,
		ActorDisplayName: "李主管",
		Meta:             MutationMeta{ExpectedRevision: 1, IdempotencyKey: "pg-bill-confirm-dup-0002", Reason: "重复归档同月同口径"},
	})
	if !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("duplicate confirm err=%v, want ErrBindingConflict", err)
	}

	// Confirming a CHILLER_PLANT bill for the same 2026-10 month is allowed because meter_scope differs.
	plantDraft, _, err := store.SaveUtilityBill(ctx, manageClaims, UtilityBillMutation{
		SiteID:                   testSiteA1,
		ActorDisplayName:         "财务王工",
		BillingMonth:             "2026-10",
		ReadingStartDate:         "2026-09-25",
		ReadingEndDate:           "2026-10-24",
		MeterScope:               UtilityBillScopeChillerPlant,
		BilledEnergyKwh:          85000,
		EnergyChargeCny:          61200.00,
		BasicChargeMode:          BasicChargeModeNone,
		BasicChargeCny:           0,
		PowerFactorAdjustmentCny: 0,
		OtherChargesCny:          0,
		TotalPayableCny:          61200.00,
		Meta:                     MutationMeta{ExpectedRevision: 0, IdempotencyKey: "pg-bill-create-plant-0003", Reason: "冷站专变十月账单"},
	})
	if err != nil {
		t.Fatalf("create CHILLER_PLANT draft err=%v", err)
	}
	plantConfirmed, _, err := store.ConfirmUtilityBill(ctx, manageClaims, ConfirmUtilityBillRequest{
		SiteID:           testSiteA1,
		BillID:           plantDraft.ID,
		ActorDisplayName: "李主管",
		Meta:             MutationMeta{ExpectedRevision: 1, IdempotencyKey: "pg-bill-confirm-plant-0003", Reason: "确认冷站专变账单"},
	})
	if err != nil || plantConfirmed.Status != UtilityBillStatusConfirmed {
		t.Fatalf("confirm CHILLER_PLANT bill=%#v err=%v", plantConfirmed, err)
	}
}
