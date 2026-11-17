package alarmservice

import (
	"context"
	"os"
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
}
