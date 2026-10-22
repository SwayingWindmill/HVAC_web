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
	retireSiteID           = "01910000-c000-7000-8000-000000000032"
	retirePolicyID         = "01910000-c000-7000-8000-000000000035"
	retirePolicyRevisionID = "01910000-c000-7000-8000-000000000036"
	retireAssignmentID     = "01910000-c000-7000-8000-000000000037"
	retireEventFirst       = "01910000-c000-7000-8000-000000000041"
	retireEventAfter       = "01910000-c000-7000-8000-000000000042"
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
	if len(cleared) != 1 || cleared[0].Timeline[len(cleared[0].Timeline)-1].Reason != "RULE_RETIRED" {
		t.Fatalf("the retired rule's Alarm did not recover as retired: %#v", cleared)
	}
	if pass, err := store.RunDueEvaluations(context.Background(), "retire-test", retiredAt.Add(2*time.Hour)); err != nil || pass.Raised != 0 || pass.Failed != 0 {
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
}
