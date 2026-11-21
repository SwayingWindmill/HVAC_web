package alarmservice

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	postgresSitePolicyID           = "01910000-5200-7000-8000-000000000001"
	postgresSitePolicyAssignmentID = "01910000-7200-7000-8000-000000000001"
)

// #609: publishing a Site's rules file again changes nothing; a changed threshold becomes the next
// revision of the same family and the Site assignment evaluates it.
func TestPostgresPublishSitePolicyIsIdempotentAndRevisesOnChange(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := identitycontext.WithTenantID(context.Background(), postgresTestTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	publishedAt := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	first, changed, err := store.PublishSitePolicy(ctx, postgresTestTenantID, postgresTestSiteID, unpublishedSitePolicy(30), postgresSitePolicyAssignmentID, publishedAt, "alarm-rules:test")
	if err != nil {
		t.Fatal(err)
	}
	if !changed || first.Revision != 1 || first.Validate() != nil {
		t.Fatalf("first publish did not release revision 1: changed=%v %#v", changed, first)
	}

	again, changed, err := store.PublishSitePolicy(ctx, postgresTestTenantID, postgresTestSiteID, unpublishedSitePolicy(30), postgresSitePolicyAssignmentID, publishedAt.Add(time.Minute), "alarm-rules:test")
	if err != nil {
		t.Fatal(err)
	}
	if changed || again.PolicyRevisionID != first.PolicyRevisionID {
		t.Fatalf("publishing the same rule again released a new revision: changed=%v %#v", changed, again)
	}

	revised, changed, err := store.PublishSitePolicy(ctx, postgresTestTenantID, postgresTestSiteID, unpublishedSitePolicy(32), postgresSitePolicyAssignmentID, publishedAt.Add(2*time.Minute), "alarm-rules:test")
	if err != nil {
		t.Fatal(err)
	}
	if !changed || revised.Revision != 2 || revised.PolicyRevisionID == first.PolicyRevisionID {
		t.Fatalf("a changed threshold did not release revision 2: changed=%v %#v", changed, revised)
	}
	evaluatedAt := publishedAt.Add(3 * time.Minute)
	decision, err := store.EvaluateAssignedSnapshot(ctx, postgresSitePolicyAssignmentID, postgresEvaluatorSnapshot("site-policy-r2", evaluatedAt, 31, "GOOD"), evaluatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if decision.State.PolicyRevisionID != revised.PolicyRevisionID {
		t.Fatalf("the Site assignment still evaluates %s, not revision 2 %s", decision.State.PolicyRevisionID, revised.PolicyRevisionID)
	}
}

// unpublishedSitePolicy is a rule as the rules file gives it: no revision identity yet.
func unpublishedSitePolicy(trigger float64) AlarmPolicyRevision {
	policy := validAlarmPolicy()
	policy.PolicyID = postgresSitePolicyID
	policy.PolicyRevisionID = ""
	policy.Revision = 0
	policy.Digest = ""
	policy.AlarmType = "chilled-water-supply-high"
	policy.SourceReference = "alarm-rules:postgres-test/chilled-water-supply-high"
	policy.Title = "冷冻水供水温度过高"
	policy.Raise = Condition{Kind: ConditionCompare, Input: "supplyTemp", Operator: CompareGT, Value: NumberValue(trigger)}
	policy.Clear = Condition{Kind: ConditionCompare, Input: "supplyTemp", Operator: CompareLTE, Value: NumberValue(28)}
	return policy
}

const (
	retireSiteID           = "01910000-c100-7000-8000-000000000002"
	retirePolicyID         = "01910000-c100-7000-8000-000000000005"
	retirePolicyRevisionID = "01910000-c100-7000-8000-000000000006"
	retireAssignmentID     = "01910000-c100-7000-8000-000000000007"
	retireEventFirst       = "01910000-c100-7000-8000-000000000011"
	retireEventAfter       = "01910000-c100-7000-8000-000000000012"
)

// #693: a rule the Site's file no longer has is retired: it leaves the Site's rules, its active
// Alarm recovers because the rule was retired, and neither readings nor the scheduled pass judge it.
func TestPostgresRetiredSiteRuleStopsAndRecoversItsAlarm(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := identitycontext.WithTenantID(context.Background(), silenceTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy := silencePolicy()
	policy.PolicyID, policy.PolicyRevisionID = retirePolicyID, retirePolicyRevisionID
	sealAlarmPolicy(&policy)
	lastReading := time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC)
	if err := store.ReleaseAlarmPolicyRevision(ctx, silenceTenantID, retireSiteID, policy, lastReading.Add(-time.Minute), "test:retire"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignAlarmPolicyRevision(ctx, silenceTenantID, retireSiteID, AlarmPolicyAssignmentInput{
		AssignmentID: retireAssignmentID, AssignmentRevision: 1, PolicyRevisionID: retirePolicyRevisionID,
		SubjectType: "SITE", SubjectID: retireSiteID, AssignedAt: lastReading.Add(-30 * time.Second).Format(time.RFC3339Nano), AssignedBy: "test:retire",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := silenceSnapshot(1, lastReading)
	snapshot.SiteId = retireSiteID
	if _, err := store.EvaluateTelemetrySnapshot(ctx, retireEventFirst, snapshot, lastReading); err != nil {
		t.Fatal(err)
	}
	if silent, err := store.RunDueEvaluations(context.Background(), "retire-test", lastReading.Add(900*time.Second)); err != nil || silent.Raised != 1 {
		t.Fatalf("the rule did not raise before it was retired: %#v %v", silent, err)
	}
	if rules, err := store.SiteRules(ctx, silenceTenantID, retireSiteID); err != nil || len(rules) != 1 || rules[0].AssignmentID != retireAssignmentID || rules[0].Policy.Title != "网关上报中断" {
		t.Fatalf("site rules before retiring: %#v %v", rules, err)
	}

	retiredAt := lastReading.Add(20 * time.Minute)
	if err := store.RetireSiteRule(ctx, silenceTenantID, retireSiteID, retireAssignmentID, retiredAt, "alarm-rules:test"); err != nil {
		t.Fatal(err)
	}

	if rules, err := store.SiteRules(ctx, silenceTenantID, retireSiteID); err != nil || len(rules) != 0 {
		t.Fatalf("a retired rule is still on the Site: %#v %v", rules, err)
	}
	cleared := siteAlarms(t, ctx, store, retireSiteID, alarmmodel.ConditionCleared)
	if len(cleared) != 1 || cleared[0].Timeline[len(cleared[0].Timeline)-1].Reason != RuleRetiredReason {
		t.Fatalf("the retired rule's Alarm did not recover as retired: %#v", cleared)
	}
	if pass, err := store.RunDueEvaluations(context.Background(), "retire-test", retiredAt.Add(2*time.Hour)); err != nil || pass.Evaluated != 0 || pass.Raised != 0 || pass.Failed != 0 {
		t.Fatalf("the scheduled pass still judged the retired rule: %#v %v", pass, err)
	}
	after := silenceSnapshot(2, retiredAt.Add(3*time.Hour))
	after.SiteId = retireSiteID
	if decisions, err := store.EvaluateTelemetrySnapshot(ctx, retireEventAfter, after, retiredAt.Add(3*time.Hour)); err != nil || len(decisions) != 0 {
		t.Fatalf("a reading still judged the retired rule: %#v %v", decisions, err)
	}
	if active := siteAlarms(t, ctx, store, retireSiteID, alarmmodel.ConditionActive); len(active) != 0 {
		t.Fatalf("the retired rule raised again: %#v", active)
	}

	// Putting the unchanged rule back in the file brings it back, and the scheduled pass judges it
	// with the Site's last readings without waiting for a new one.
	unpublished := policy
	unpublished.PolicyRevisionID, unpublished.Revision, unpublished.Digest = "", 0, ""
	republishedAt := retiredAt.Add(4 * time.Hour)
	if _, changed, err := store.PublishSitePolicy(ctx, silenceTenantID, retireSiteID, unpublished, retireAssignmentID, republishedAt, "alarm-rules:test"); err != nil || !changed {
		t.Fatalf("publishing the retired rule again did not bring it back: changed=%v %v", changed, err)
	}
	if rules, err := store.SiteRules(ctx, silenceTenantID, retireSiteID); err != nil || len(rules) != 1 {
		t.Fatalf("site rules after publishing again: %#v %v", rules, err)
	}
	if pass, err := store.RunDueEvaluations(context.Background(), "retire-test", republishedAt.Add(time.Minute)); err != nil || pass.Raised != 1 {
		t.Fatalf("the scheduled pass did not judge the rule brought back: %#v %v", pass, err)
	}
}

func TestPostgresSyncSiteRulesHotSyncsAtomicallyAndPreservesUnchangedEvaluationState(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := identitycontext.WithTenantID(context.Background(), postgresTestTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	syncSiteID := "01910000-d200-7000-8000-000000000001"
	keptAssign := "01910000-d200-7000-8000-000000000011"
	changedAssign := "01910000-d200-7000-8000-000000000012"
	retiredAssign := "01910000-d200-7000-8000-000000000013"
	addedAssign := "01910000-d200-7000-8000-000000000014"

	makeRule := func(assignID, policyID, alarmType, sourceRef string, trigger float64, holdSeconds int64) SiteRule {
		p := unpublishedSitePolicy(trigger)
		p.PolicyID = policyID
		p.AlarmType = alarmType
		p.SourceReference = sourceRef
		p.Title = alarmType
		if holdSeconds > 0 {
			p.TriggerMode = TriggerDuration
			p.DurationSeconds = holdSeconds
		} else {
			p.TriggerMode = TriggerSimple
			p.DurationSeconds = 0
		}
		return SiteRule{AssignmentID: assignID, Policy: p}
	}

	t0 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	keptRule := makeRule(keptAssign, "01910000-d200-7000-8000-000000000101", "SYNC_KEPT", "alarm-rules:sync/kept", 30, 300)
	changedRule := makeRule(changedAssign, "01910000-d200-7000-8000-000000000102", "SYNC_CHANGED", "alarm-rules:sync/changed", 30, 0)
	retiredRule := makeRule(retiredAssign, "01910000-d200-7000-8000-000000000103", "SYNC_RETIRED", "alarm-rules:sync/retired", 30, 0)

	initial, err := store.SyncSiteRules(ctx, postgresTestTenantID, syncSiteID, []SiteRule{keptRule, changedRule, retiredRule}, t0, "alarm-rules:sync-1")
	if err != nil {
		t.Fatal(err)
	}
	if initial.Added != 3 || initial.Changed != 0 || initial.Unchanged != 0 || initial.Retired != 0 {
		t.Fatalf("unexpected initial sync summary: %#v", initial)
	}

	// Start in-flight candidate state on the rule that will remain unchanged, and raise an Alarm on the rule that will be retired.
	evalSnapshot := postgresEvaluatorSnapshot("sync-eval-1", t0.Add(time.Minute), 31, "GOOD")
	evalSnapshot.SiteID = syncSiteID
	evalSnapshot.SubjectID = syncSiteID
	keptDecision, err := store.EvaluateAssignedSnapshot(ctx, keptAssign, evalSnapshot, t0.Add(time.Minute))
	if err != nil || keptDecision.State.CandidateSince == nil {
		t.Fatalf("kept rule did not start candidate state: %#v %v", keptDecision, err)
	}
	if retiredDecision, err := store.EvaluateAssignedSnapshot(ctx, retiredAssign, evalSnapshot, t0.Add(time.Minute)); err != nil || retiredDecision.Effect != EvaluationEffectPublish {
		t.Fatalf("retired rule did not raise initial Alarm: %#v %v", retiredDecision, err)
	}

	// Hot-sync updated desired set: 1 unchanged, 1 changed, 1 retired, 1 added.
	changedRuleV2 := makeRule(changedAssign, "01910000-d200-7000-8000-000000000102", "SYNC_CHANGED", "alarm-rules:sync/changed", 35, 0)
	addedRule := makeRule(addedAssign, "01910000-d200-7000-8000-000000000104", "SYNC_ADDED", "alarm-rules:sync/added", 29, 0)
	summary, err := store.SyncSiteRules(ctx, postgresTestTenantID, syncSiteID, []SiteRule{keptRule, changedRuleV2, addedRule}, t0.Add(2*time.Minute), "alarm-rules:sync-2")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Added != 1 || summary.Changed != 1 || summary.Unchanged != 1 || summary.Retired != 1 {
		t.Fatalf("unexpected hot-sync summary: %#v", summary)
	}

	rules, err := store.SiteRules(ctx, postgresTestTenantID, syncSiteID)
	if err != nil || len(rules) != 3 {
		t.Fatalf("active site rules after hot-sync = %#v err=%v", rules, err)
	}

	// Verify unchanged rule's in-flight candidate_since was NOT touched or reset.
	tx, err := store.beginTenantTransaction(ctx, postgresTestTenantID, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var candidateSince time.Time
	var repeatCount uint64
	if err := tx.QueryRow(ctx, `
		SELECT candidate_since, repeat_count
		FROM alarm_runtime.alarm_evaluation_state
		WHERE tenant_id=$1 AND site_id=$2 AND assignment_id=$3`,
		postgresTestTenantID, syncSiteID, keptAssign).Scan(&candidateSince, &repeatCount); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if !candidateSince.UTC().Equal(t0.Add(time.Minute)) || repeatCount != keptDecision.State.RepeatCount {
		t.Fatalf("unchanged rule evaluation state mutated: candidateSince=%v repeatCount=%d wantRepeat=%d", candidateSince, repeatCount, keptDecision.State.RepeatCount)
	}
	evalFollowUp := postgresEvaluatorSnapshot("sync-eval-2", t0.Add(6*time.Minute), 31, "GOOD")
	evalFollowUp.SiteID = syncSiteID
	evalFollowUp.SubjectID = syncSiteID
	keptPublished, err := store.EvaluateAssignedSnapshot(ctx, keptAssign, evalFollowUp, t0.Add(6*time.Minute))
	if err != nil || keptPublished.Effect != EvaluationEffectPublish {
		t.Fatalf("preserved candidate_since should fire after hold period without reset: decision=%#v err=%v", keptPublished, err)
	}
	listed, err := store.List(ctx, postgresTestTenantID, syncSiteID, Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	cleared := []alarmmodel.Alarm{}
	for _, candidate := range listed.Items {
		if candidate.Condition == alarmmodel.ConditionCleared {
			cleared = append(cleared, candidate)
		}
	}
	if len(cleared) != 1 || cleared[0].Timeline[len(cleared[0].Timeline)-1].Reason != RuleRetiredReason {
		t.Fatalf("retired rule alarm was not cleared on hot-sync: %#v", cleared)
	}
	revs, err := store.ListSiteRuleBundleRevisions(ctx, postgresTestTenantID, syncSiteID, 10)
	if err != nil || revs.CurrentRevision < 1 || len(revs.Items) < 1 || revs.Items[0].Source != RuleBundleSourceCLISync {
		t.Fatalf("SyncSiteRules did not record CLI_SYNC bundle revision: %#v err=%v", revs, err)
	}
}

func TestPostgresPublishDiffRollbackAndSiteRuleCacheLifecycle(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := identitycontext.WithTenantID(context.Background(), postgresTestTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	bundleSiteID := "01910000-e500-7000-8000-000000000002"
	bundleDeviceID := "01910000-e520-7000-8000-000000000001"
	ruleHoldID := "01910000-e510-7000-8000-000000000001"
	ruleRetireID := "01910000-e510-7000-8000-000000000002"
	ruleLinkedID := "01910000-e510-7000-8000-000000000003"
	t0 := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

	reading := &RuleReading{DeviceID: bundleDeviceID, PointCode: "supply_water_temperature"}
	clearCond := RuleCondition{Kind: ConditionCompare, Reading: reading, Operator: CompareLTE, Value: 28.0}
	holdDraft := SiteRuleDraft{
		RuleID:          ruleHoldID,
		Title:           "供水持续超温保持",
		AlarmType:       "supply-hold-check",
		Severity:        alarmmodel.SeverityMajor,
		TriggerMode:     TriggerDuration,
		DurationSeconds: 600,
		Raise:           RuleCondition{Kind: ConditionCompare, Reading: reading, Operator: CompareGT, Value: 30.0},
		Clear:           clearCond,
	}
	retireDraft := SiteRuleDraft{
		RuleID:      ruleRetireID,
		Title:       "供水瞬时越限待停用",
		AlarmType:   "supply-retire-check",
		Severity:    alarmmodel.SeverityCritical,
		TriggerMode: TriggerSimple,
		Raise:       RuleCondition{Kind: ConditionCompare, Reading: reading, Operator: CompareGT, Value: 29.0},
		Clear:       clearCond,
	}

	// Publish v1 via workbench.
	pub1, err := store.PublishSiteRules(ctx, postgresTestTenantID, bundleSiteID, SiteRulePublishRequest{
		ExpectedRevision: 0,
		ChangeNote:       "初始化站点规则包",
		Rules:            []SiteRuleDraft{holdDraft, retireDraft},
	}, "principal:author-1", "值班工程师甲", t0)
	if err != nil {
		t.Fatalf("PublishSiteRules v1 failed: %v", err)
	}
	if pub1.BundleRevision != 1 || !pub1.Changed || pub1.Source != RuleBundleSourceWebWorkbench || len(pub1.Items) != 2 {
		t.Fatalf("unexpected v1 publish result: %#v", pub1)
	}

	// Evaluate snapshot: starts holdDraft candidate_since timer and fires retireDraft active alarm.
	eval1 := postgresEvaluatorSnapshot("bundle-eval-1", t0.Add(time.Minute), 31, "GOOD")
	eval1.SiteID = bundleSiteID
	eval1.SubjectID = bundleSiteID
	eval1.Inputs = map[string]InputFact{
		bundleDeviceID + "/supply_water_temperature": NumberInput(bundleDeviceID+"/supply_water_temperature", 31, "GOOD", t0.Add(time.Minute).Format(time.RFC3339Nano)),
	}
	dHold, err := store.EvaluateAssignedSnapshot(ctx, ruleHoldID, eval1, t0.Add(time.Minute))
	if err != nil || dHold.Effect != EvaluationEffectNone || dHold.State.CandidateSince == nil {
		t.Fatalf("expected pending duration hold timer on v1 ruleHold: %#v err=%v", dHold, err)
	}
	dRetire, err := store.EvaluateAssignedSnapshot(ctx, ruleRetireID, eval1, t0.Add(time.Minute))
	if err != nil || dRetire.Effect != EvaluationEffectPublish {
		t.Fatalf("expected active alarm on v1 ruleRetire: %#v err=%v", dRetire, err)
	}

	// Optimistic lock mismatch returns ErrRuleBundleConflict with currentRevision=1.
	if _, err := store.PublishSiteRules(ctx, postgresTestTenantID, bundleSiteID, SiteRulePublishRequest{
		ExpectedRevision: 0,
		Rules:            []SiteRuleDraft{holdDraft},
	}, "principal:author-2", "值班工程师乙", t0.Add(2*time.Minute)); !errors.Is(err, ErrRuleBundleConflict) {
		t.Fatalf("expected ErrRuleBundleConflict, got %v", err)
	}

	// Diff previews linked rule additions and warns that retiring ruleRetire will clear its active alarm.
	linkedDraft := SiteRuleDraft{
		RuleID:           ruleLinkedID,
		Title:            "供水二级联动确认",
		Severity:         alarmmodel.SeverityWarning,
		DependsOnRuleIDs: []string{ruleHoldID},
		DependencyGate:   DependencyGateAnyActive,
		TriggerMode:      TriggerSimple,
		Raise:            RuleCondition{Kind: ConditionCompare, Reading: reading, Operator: CompareGT, Value: 30.5},
		Clear:            clearCond,
	}
	diffRes, err := store.DiffSiteRules(ctx, postgresTestTenantID, bundleSiteID, []SiteRuleDraft{holdDraft, linkedDraft})
	if err != nil || !diffRes.Valid || diffRes.Summary.Added != 1 || diffRes.Summary.Retired != 1 || diffRes.Summary.Unchanged != 1 || diffRes.Graph.MaxHopDepth != 1 {
		t.Fatalf("unexpected DiffSiteRules result: %#v err=%v", diffRes, err)
	}
	sawClearWarning := false
	for _, ch := range diffRes.Changes {
		if ch.RuleID == ruleRetireID && ch.Change == "RETIRED" && ch.WillClearActiveAlarm {
			sawClearWarning = true
		}
	}
	if !sawClearWarning {
		t.Fatalf("expected WillClearActiveAlarm warning on retired rule: %#v", diffRes.Changes)
	}

	// Publish v2: preserves ruleHold evaluation timer, clears ruleRetire active alarm, activates ruleLinked.
	pub2, err := store.PublishSiteRules(ctx, postgresTestTenantID, bundleSiteID, SiteRulePublishRequest{
		ExpectedRevision: 1,
		ChangeNote:       "新增联动并停用瞬时规则",
		Rules:            []SiteRuleDraft{holdDraft, linkedDraft},
	}, "principal:author-1", "值班工程师甲", t0.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("PublishSiteRules v2 failed: %v", err)
	}
	if pub2.BundleRevision != 2 || pub2.Summary.Retired != 1 || pub2.Summary.Added != 1 || pub2.Summary.Unchanged != 1 {
		t.Fatalf("unexpected v2 publish result: %#v", pub2)
	}

	// Verify unchanged ruleHold fires cleanly after hold window elapses without resetting candidate_since.
	eval2 := eval1
	eval2.InputRevision = "bundle-eval-2"
	eval2.AsOf = t0.Add(12 * time.Minute).Format(time.RFC3339Nano)
	dHoldFired, err := store.EvaluateAssignedSnapshot(ctx, ruleHoldID, eval2, t0.Add(12*time.Minute))
	if err != nil || dHoldFired.Effect != EvaluationEffectPublish {
		t.Fatalf("unchanged ruleHold should fire after preserved hold period: %#v err=%v", dHoldFired, err)
	}

	// Verify ruleRetire's alarm cleared with RULE_RETIRED.
	listedAfterV2, err := store.List(ctx, postgresTestTenantID, bundleSiteID, Filter{Limit: 10})
	if err != nil {
		t.Fatalf("list site alarms after v2 publish: %v", err)
	}
	clearedList := make([]alarmmodel.Alarm, 0, len(listedAfterV2.Items))
	for _, alarm := range listedAfterV2.Items {
		if alarm.Condition == alarmmodel.ConditionCleared {
			clearedList = append(clearedList, alarm)
		}
	}
	if len(clearedList) != 1 || clearedList[0].Timeline[len(clearedList[0].Timeline)-1].Reason != RuleRetiredReason {
		t.Fatalf("expected RULE_RETIRED alarm recovery on v2 publish: %#v", clearedList)
	}

	// Warm siteRuleCache via loadSiteTelemetryEvaluationState & verify subsequent reads match stamp cleanly.
	txCheck, err := store.beginTenantTransaction(ctx, postgresTestTenantID, true)
	if err != nil {
		t.Fatal(err)
	}
	bundleRev, assignCount, stampErr := store.siteRuleCacheStampInTx(ctx, txCheck, postgresTestTenantID, bundleSiteID)
	_ = txCheck.Rollback(ctx)
	if stampErr != nil || bundleRev != 2 || assignCount == 0 {
		t.Fatalf("unexpected cache stamp: rev=%d count=%d err=%v", bundleRev, assignCount, stampErr)
	}
	cachedRules, hit := store.ruleCache.Get(postgresTestTenantID, bundleSiteID, bundleRev, assignCount)
	if !hit || len(cachedRules) != 2 {
		t.Fatalf("expected hot-published rules warmed in siteRuleCache: hit=%v len=%d", hit, len(cachedRules))
	}

	// Rollback to v1 re-activates previously retired ruleRetire cleanly without PolicyID switch conflict!
	rb, err := store.RollbackSiteRules(ctx, postgresTestTenantID, bundleSiteID, 1, SiteRuleRollbackRequest{
		ExpectedRevision: 2,
		ChangeNote:       "回退至首版规则包",
	}, "principal:author-2", "值班工程师乙", t0.Add(15*time.Minute))
	if err != nil {
		t.Fatalf("RollbackSiteRules to v1 failed: %v", err)
	}
	if rb.BundleRevision != 3 || rb.Source != RuleBundleSourceRollback || rb.RolledBackFromRevision == nil || *rb.RolledBackFromRevision != 1 || len(rb.Items) != 2 {
		t.Fatalf("unexpected rollback result: %#v", rb)
	}

	// Revision ledger lists v3 -> v2 -> v1 in descending order.
	hist, err := store.ListSiteRuleBundleRevisions(ctx, postgresTestTenantID, bundleSiteID, 10)
	if err != nil || hist.CurrentRevision != 3 || len(hist.Items) != 3 || !strings.Contains(hist.Items[0].ChangeNote, "回退") {
		t.Fatalf("unexpected revision ledger: %#v err=%v", hist, err)
	}

	// Retiring a DURATION rule with an in-flight candidate_since clears timer state so reactivation never fires early.
	retireDurID := "01910000-e510-7000-8000-000000000004"
	durDraft := holdDraft
	durDraft.RuleID = retireDurID
	durDraft.AlarmType = "supply-retire-duration"
	durDraft.Title = "短暂休眠重启防抖校验"
	durDraft.DurationSeconds = 600
	if _, err := store.PublishSiteRules(ctx, postgresTestTenantID, bundleSiteID, SiteRulePublishRequest{
		ExpectedRevision: 3,
		Rules:            []SiteRuleDraft{holdDraft, retireDraft, durDraft},
	}, "principal:author-1", "值班工程师甲", t0.Add(16*time.Minute)); err != nil {
		t.Fatalf("PublishSiteRules v4 failed: %v", err)
	}
	evalHoldStart := eval1
	evalHoldStart.InputRevision = "bundle-dur-start"
	evalHoldStart.AsOf = t0.Add(17 * time.Minute).Format(time.RFC3339Nano)
	evalHoldStart.Inputs = map[string]InputFact{
		bundleDeviceID + "/supply_water_temperature": NumberInput(bundleDeviceID+"/supply_water_temperature", 31, "GOOD", t0.Add(17*time.Minute).Format(time.RFC3339Nano)),
	}
	if res, err := store.EvaluateAssignedSnapshot(ctx, retireDurID, evalHoldStart, t0.Add(17*time.Minute)); err != nil || res.State.CandidateSince == nil {
		t.Fatalf("expected initial pending candidate_since: %#v err=%v", res, err)
	}
	// Retire durDraft in v5, then restore v4 in v6 and evaluate 20 minutes later (first sample must NOT fire early).
	if _, err := store.PublishSiteRules(ctx, postgresTestTenantID, bundleSiteID, SiteRulePublishRequest{
		ExpectedRevision: 4,
		Rules:            []SiteRuleDraft{holdDraft, retireDraft},
	}, "principal:author-1", "值班工程师甲", t0.Add(18*time.Minute)); err != nil {
		t.Fatalf("PublishSiteRules v5 retire durDraft failed: %v", err)
	}
	if _, err := store.RollbackSiteRules(ctx, postgresTestTenantID, bundleSiteID, 4, SiteRuleRollbackRequest{
		ExpectedRevision: 5,
	}, "principal:author-2", "值班工程师乙", t0.Add(19*time.Minute)); err != nil {
		t.Fatalf("RollbackSiteRules to v4 failed: %v", err)
	}
	evalAfterReactivate := eval1
	evalAfterReactivate.InputRevision = "bundle-dur-reactivated"
	evalAfterReactivate.AsOf = t0.Add(35 * time.Minute).Format(time.RFC3339Nano)
	evalAfterReactivate.Inputs = map[string]InputFact{
		bundleDeviceID + "/supply_water_temperature": NumberInput(bundleDeviceID+"/supply_water_temperature", 31, "GOOD", t0.Add(35*time.Minute).Format(time.RFC3339Nano)),
	}
	resReactivated, err := store.EvaluateAssignedSnapshot(ctx, retireDurID, evalAfterReactivate, t0.Add(35*time.Minute))
	if err != nil || resReactivated.Effect == EvaluationEffectPublish || resReactivated.State.CandidateSince == nil || *resReactivated.State.CandidateSince != t0.Add(35*time.Minute).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("expected fresh candidate_since after reactivation, got effect=%s candidate=%v err=%v", resReactivated.Effect, resReactivated.State.CandidateSince, err)
	}

	// Concurrent PublishSiteRules race with identical expectedRevision serializes cleanly -> 1 win + 1 ErrRuleBundleConflict (no 23505/40001).
	raceStart := make(chan struct{})
	type raceOutcome struct {
		rev uint64
		err error
	}
	raceCh := make(chan raceOutcome, 2)
	for i := 0; i < 2; i++ {
		titleSuffix := "A"
		if i == 1 {
			titleSuffix = "B"
		}
		go func(suffix string) {
			<-raceStart
			d := holdDraft
			d.Title = holdDraft.Title + "-" + suffix
			pub, pErr := store.PublishSiteRules(ctx, postgresTestTenantID, bundleSiteID, SiteRulePublishRequest{
				ExpectedRevision: 6,
				Rules:            []SiteRuleDraft{d, retireDraft, durDraft},
			}, "principal:racer-"+suffix, "并发操作员"+suffix, t0.Add(36*time.Minute))
			raceCh <- raceOutcome{rev: pub.BundleRevision, err: pErr}
		}(titleSuffix)
	}
	close(raceStart)
	wins := 0
	conflicts := 0
	for i := 0; i < 2; i++ {
		out := <-raceCh
		switch {
		case out.err == nil && out.rev == 7:
			wins++
		case errors.Is(out.err, ErrRuleBundleConflict):
			var conflictErr *RuleBundleConflictError
			if !errors.As(out.err, &conflictErr) || conflictErr.CurrentRevision != 7 {
				t.Fatalf("expected conflict currentRevision=7, got %v", out.err)
			}
			conflicts++
		default:
			t.Fatalf("expected 1 success and 1 ErrRuleBundleConflict, got rev=%d err=%v", out.rev, out.err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("expected wins=1 conflicts=1, got wins=%d conflicts=%d", wins, conflicts)
	}
}
