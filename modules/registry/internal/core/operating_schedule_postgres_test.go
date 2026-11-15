package core

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestPostgresOperatingScheduleAndExceptionsLifecycle(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)

	readClaims := writerIntegrationClaims(registryauth.ActionSiteRead, testSiteA1)
	initial, err := store.GetOperatingSchedule(ctx, readClaims, testSiteA1)
	if err != nil {
		t.Fatalf("GetOperatingSchedule default error=%v", err)
	}
	if initial.Revision != 0 || initial.PreCoolLeadMinutes != 45 || initial.CoastDownLagMinutes != 30 || len(initial.Slots) != 0 {
		t.Fatalf("unexpected default schedule: %#v", initial)
	}

	manageClaims := writerIntegrationClaims(registryauth.ActionScheduleManage, testSiteA1)
	temp7 := 7.0
	spanGroupID := "01990000-8400-7000-8000-000000000001"
	firstSave := OperatingScheduleMutation{
		SiteID:              testSiteA1,
		Name:                "松雅轩周运行计划",
		PreCoolLeadMinutes:  45,
		CoastDownLagMinutes: 30,
		Slots: []OperatingScheduleSlotInput{
			{DayOfWeek: 5, StartMinute: 1320, EndMinute: 1440, TargetSupplyTempC: &temp7, SpanGroupID: &spanGroupID},
			{DayOfWeek: 6, StartMinute: 0, EndMinute: 360, TargetSupplyTempC: &temp7, SpanGroupID: &spanGroupID},
		},
		Meta: MutationMeta{ExpectedRevision: 0, IdempotencyKey: "pg-sched-save-0001", Reason: "保存跨午夜周运行计划"},
	}
	saved1, replayed, err := store.SaveOperatingSchedule(ctx, manageClaims, firstSave)
	if err != nil || replayed || saved1.Revision != 1 || len(saved1.Slots) != 2 {
		t.Fatalf("first SaveOperatingSchedule=%#v replayed=%v err=%v", saved1, replayed, err)
	}

	// Replay returns identical schedule without incrementing revision.
	replayedSchedule, replayed, err := store.SaveOperatingSchedule(ctx, manageClaims, firstSave)
	if err != nil || !replayed || replayedSchedule.Revision != 1 {
		t.Fatalf("replayed SaveOperatingSchedule=%#v replayed=%v err=%v", replayedSchedule, replayed, err)
	}

	// Update revision 1 -> 2 appends immutable slot rows without needing DELETE privileges.
	secondSave := OperatingScheduleMutation{
		SiteID:              testSiteA1,
		Name:                "松雅轩夏令周运行计划",
		PreCoolLeadMinutes:  60,
		CoastDownLagMinutes: 20,
		Slots: []OperatingScheduleSlotInput{
			{DayOfWeek: 1, StartMinute: 480, EndMinute: 1260, TargetSupplyTempC: &temp7},
		},
		Meta: MutationMeta{ExpectedRevision: 1, IdempotencyKey: "pg-sched-save-0002", Reason: "调整周一供冷时段"},
	}
	saved2, replayed, err := store.SaveOperatingSchedule(ctx, manageClaims, secondSave)
	if err != nil || replayed || saved2.Revision != 2 || len(saved2.Slots) != 1 || saved2.PreCoolLeadMinutes != 60 {
		t.Fatalf("second SaveOperatingSchedule=%#v replayed=%v err=%v", saved2, replayed, err)
	}

	readBack, err := store.GetOperatingSchedule(ctx, readClaims, testSiteA1)
	if err != nil || readBack.Revision != 2 || len(readBack.Slots) != 1 || readBack.Slots[0].DayOfWeek != 1 {
		t.Fatalf("GetOperatingSchedule after v2=%#v err=%v", readBack, err)
	}

	// Operator creates PENDING_APPROVAL exception, manager approves, and overlapping APPROVED date range fails via DB trigger.
	requestClaims := writerIntegrationClaims(registryauth.ActionScheduleRequest, testSiteA1)
	requestClaims.PrincipalID = "018f1e00-2000-7000-8000-000000000002"
	exc1, _, err := store.CreateOperatingException(ctx, requestClaims, CreateOperatingExceptionRequest{
		SiteID:           testSiteA1,
		ActorDisplayName: "张工",
		Title:            "国庆停机",
		Kind:             "HOLIDAY_SHUTDOWN",
		StartDate:        "2026-10-01",
		EndDate:          "2026-10-03",
		ReplacementSlots: []OperatingExceptionSlot{},
		Meta:             MutationMeta{ExpectedRevision: 0, IdempotencyKey: "pg-exc-create-0001", Reason: "国庆节停机三天"},
	})
	if err != nil || exc1.Status != "PENDING_APPROVAL" {
		t.Fatalf("CreateOperatingException=%#v err=%v", exc1, err)
	}

	approved1, _, err := store.DecideOperatingException(ctx, manageClaims, DecideOperatingExceptionRequest{
		SiteID:           testSiteA1,
		ExceptionID:      exc1.ID,
		ActorDisplayName: "李主管",
		Decision:         "APPROVE",
		DecisionNote:     "同意停机",
		Meta:             MutationMeta{ExpectedRevision: 1, IdempotencyKey: "pg-exc-approve-0001", Reason: "审批国庆停机"},
	})
	if err != nil || approved1.Status != "APPROVED" {
		t.Fatalf("DecideOperatingException APPROVE=%#v err=%v", approved1, err)
	}

	_, _, err = store.CreateOperatingException(ctx, manageClaims, CreateOperatingExceptionRequest{
		SiteID:           testSiteA1,
		ActorDisplayName: "李主管",
		Title:            "10月3日临时加班供冷",
		Kind:             "OVERTIME_COOLING",
		StartDate:        "2026-10-03",
		EndDate:          "2026-10-04",
		ReplacementSlots: []OperatingExceptionSlot{{StartMinute: 540, EndMinute: 1080}},
		AutoApprove:      true,
		Meta:             MutationMeta{ExpectedRevision: 0, IdempotencyKey: "pg-exc-overlap-0001", Reason: "重叠日期测试"},
	})
	if !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("overlapping APPROVED exception err=%v, want ErrBindingConflict", err)
	}
}

func TestPostgresSiteControlStateActiveMaintenanceWindow(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)

	suffix := time.Now().UnixNano()
	siteClaims := writerIntegrationClaims(registryauth.ActionSiteWrite)
	site, _, err := store.SaveSite(ctx, siteClaims, SiteMutation{
		Code:        fmt.Sprintf("w1-maint-%d", suffix),
		DisplayName: "维保门禁集成测试站",
		Timezone:    "Asia/Shanghai",
		Status:      "ACTIVE",
		Meta:        MutationMeta{IdempotencyKey: fmt.Sprintf("pg-w1-site-%d", suffix), Reason: "创建维保门禁测试站点"},
	})
	if err != nil {
		t.Fatalf("SaveSite err=%v", err)
	}

	readClaims := writerIntegrationClaims(registryauth.ActionSiteRead, site.ID)
	manageClaims := writerIntegrationClaims(registryauth.ActionScheduleManage, site.ID)

	initialState, err := store.GetSiteControlState(ctx, readClaims, site.ID)
	if err != nil {
		t.Fatalf("initial GetSiteControlState err=%v", err)
	}
	if initialState.ActiveMaintenanceWindow != nil {
		t.Fatalf("expected nil initial ActiveMaintenanceWindow, got %#v", initialState.ActiveMaintenanceWindow)
	}

	today := siteLocalCalendarDate("Asia/Shanghai", time.Now().UTC())
	exc, _, err := store.CreateOperatingException(ctx, manageClaims, CreateOperatingExceptionRequest{
		SiteID:           site.ID,
		ActorDisplayName: "李主管",
		Title:            "冷站主冷机季度维保",
		Kind:             "MAINTENANCE_WINDOW",
		StartDate:        today,
		EndDate:          today,
		ReplacementSlots: []OperatingExceptionSlot{},
		AutoApprove:      true,
		Meta:             MutationMeta{ExpectedRevision: 0, IdempotencyKey: fmt.Sprintf("pg-w1-exc-%d", suffix), Reason: "批准今日冷站维保窗口"},
	})
	if err != nil || exc.Status != "APPROVED" {
		t.Fatalf("CreateOperatingException=%#v err=%v", exc, err)
	}

	activeState, err := store.GetSiteControlState(ctx, readClaims, site.ID)
	if err != nil {
		t.Fatalf("active GetSiteControlState err=%v", err)
	}
	if activeState.ActiveMaintenanceWindow == nil ||
		activeState.ActiveMaintenanceWindow.ID != exc.ID ||
		activeState.ActiveMaintenanceWindow.Title != "冷站主冷机季度维保" ||
		activeState.ActiveMaintenanceWindow.StartDate != today ||
		activeState.ActiveMaintenanceWindow.EndDate != today ||
		activeState.ActiveMaintenanceWindow.Reason != "批准今日冷站维保窗口" {
		t.Fatalf("unexpected GetSiteControlState ActiveMaintenanceWindow: %#v", activeState.ActiveMaintenanceWindow)
	}

	savedMode, _, err := store.SaveSiteControlMode(ctx, manageClaims, SiteControlModeMutation{
		SiteID:      site.ID,
		ControlMode: SiteControlModeManualOverride,
		Reason:      "维保窗口内切换现场手动",
		Meta:        MutationMeta{ExpectedRevision: 0, IdempotencyKey: fmt.Sprintf("pg-w1-mode-%d", suffix), Reason: "维保窗口内切换现场手动"},
	})
	if err != nil {
		t.Fatalf("SaveSiteControlMode err=%v", err)
	}
	if savedMode.ActiveMaintenanceWindow == nil || savedMode.ActiveMaintenanceWindow.ID != exc.ID {
		t.Fatalf("expected SaveSiteControlMode ActiveMaintenanceWindow=%s, got %#v", exc.ID, savedMode.ActiveMaintenanceWindow)
	}

	cancelled, _, err := store.DecideOperatingException(ctx, manageClaims, DecideOperatingExceptionRequest{
		SiteID:           site.ID,
		ExceptionID:      exc.ID,
		ActorDisplayName: "李主管",
		Decision:         "CANCEL",
		DecisionNote:     "维保提前结束",
		Meta:             MutationMeta{ExpectedRevision: exc.Revision, IdempotencyKey: fmt.Sprintf("pg-w1-cancel-%d", suffix), Reason: "取消今日维保窗口"},
	})
	if err != nil || cancelled.Status != "CANCELLED" {
		t.Fatalf("DecideOperatingException CANCEL=%#v err=%v", cancelled, err)
	}

	afterCancel, err := store.GetSiteControlState(ctx, readClaims, site.ID)
	if err != nil {
		t.Fatalf("after-cancel GetSiteControlState err=%v", err)
	}
	if afterCancel.ActiveMaintenanceWindow != nil {
		t.Fatalf("expected nil ActiveMaintenanceWindow after cancel, got %#v", afterCancel.ActiveMaintenanceWindow)
	}
}
