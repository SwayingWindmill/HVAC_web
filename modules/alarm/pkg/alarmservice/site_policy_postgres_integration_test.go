package alarmservice

import (
	"context"
	"os"
	"testing"
	"time"

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
