package alarmservice

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	postgresEvaluatorAssignmentID          = "01910000-7000-7000-8000-000000000001"
	postgresEvaluatorSupersedeAssignmentID = "01910000-7000-7000-8000-000000000002"
	postgresEvaluatorLeaseAssignmentID     = "01910000-7000-7000-8000-000000000003"
	postgresPolicyLifecycleID              = "01910000-5100-7000-8000-000000000001"
	postgresPolicyLifecycleRevision1       = "01910000-5100-7000-8000-000000000002"
	postgresPolicyLifecycleRevision2       = "01910000-5100-7000-8000-000000000003"
	postgresPolicyLifecycleAssignmentID    = "01910000-7100-7000-8000-000000000001"
)

func TestPostgresAlarmPolicyReleaseAssignmentSwitchAndRollbackAreAppendOnly(t *testing.T) {
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

	releasedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	revision1 := postgresLifecyclePolicy(postgresPolicyLifecycleRevision1, 1, 30)
	if err := store.ReleaseAlarmPolicyRevision(ctx, postgresTestTenantID, postgresTestSiteID, revision1, releasedAt, "principal:policy-admin"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignAlarmPolicyRevision(ctx, postgresTestTenantID, postgresTestSiteID, AlarmPolicyAssignmentInput{
		AssignmentID: postgresPolicyLifecycleAssignmentID, AssignmentRevision: 1, PolicyRevisionID: revision1.PolicyRevisionID,
		SubjectType: "SITE", SubjectID: postgresTestSiteID, AssignedAt: releasedAt.Add(time.Second).Format(time.RFC3339Nano), AssignedBy: "principal:policy-admin",
	}); err != nil {
		t.Fatal(err)
	}
	firstSnapshot := postgresEvaluatorSnapshot("policy-switch-r1", releasedAt.Add(2*time.Second), 31, "GOOD")
	candidate, err := store.EvaluateAssignedSnapshot(ctx, postgresPolicyLifecycleAssignmentID, firstSnapshot, releasedAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Effect != EvaluationEffectNone || candidate.State.CandidateSince == nil {
		t.Fatalf("released policy revision did not start its duration candidate: %#v", candidate)
	}

	revision2 := postgresLifecyclePolicy(postgresPolicyLifecycleRevision2, 2, 32)
	if err := store.ReleaseAlarmPolicyRevision(ctx, postgresTestTenantID, postgresTestSiteID, revision2, releasedAt.Add(3*time.Second), "principal:policy-admin"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignAlarmPolicyRevision(ctx, postgresTestTenantID, postgresTestSiteID, AlarmPolicyAssignmentInput{
		AssignmentID: postgresPolicyLifecycleAssignmentID, AssignmentRevision: 2, PolicyRevisionID: revision2.PolicyRevisionID,
		SubjectType: "SITE", SubjectID: postgresTestSiteID, AssignedAt: releasedAt.Add(4 * time.Second).Format(time.RFC3339Nano), AssignedBy: "principal:policy-admin",
	}); err != nil {
		t.Fatal(err)
	}
	switchedSnapshot := postgresEvaluatorSnapshot("policy-switch-r2", releasedAt.Add(5*time.Second), 31, "GOOD")
	switched, err := store.EvaluateAssignedSnapshot(ctx, postgresPolicyLifecycleAssignmentID, switchedSnapshot, releasedAt.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if switched.Effect != EvaluationEffectNone || switched.State.PolicyRevisionID != revision2.PolicyRevisionID || switched.State.CandidateSince != nil || switched.State.Status != EvaluationNotMatched {
		t.Fatalf("new policy revision inherited incompatible duration state: %#v", switched)
	}

	if err := store.AssignAlarmPolicyRevision(ctx, postgresTestTenantID, postgresTestSiteID, AlarmPolicyAssignmentInput{
		AssignmentID: postgresPolicyLifecycleAssignmentID, AssignmentRevision: 3, PolicyRevisionID: revision1.PolicyRevisionID,
		SubjectType: "SITE", SubjectID: postgresTestSiteID, AssignedAt: releasedAt.Add(6 * time.Second).Format(time.RFC3339Nano), AssignedBy: "principal:policy-admin",
	}); err != nil {
		t.Fatal(err)
	}
	rollbackSnapshot := postgresEvaluatorSnapshot("policy-switch-r3", releasedAt.Add(7*time.Second), 31, "GOOD")
	rolledBack, err := store.EvaluateAssignedSnapshot(ctx, postgresPolicyLifecycleAssignmentID, rollbackSnapshot, releasedAt.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Effect != EvaluationEffectNone || rolledBack.State.PolicyRevisionID != revision1.PolicyRevisionID || rolledBack.State.CandidateSince == nil || *rolledBack.State.CandidateSince != rollbackSnapshot.AsOf {
		t.Fatalf("rollback assignment did not start revision-1 state afresh: %#v", rolledBack)
	}
}

func TestPostgresEvaluatorDurationRestartClaimAndClearAreAtomic(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := identitycontext.WithTenantID(context.Background(), postgresTestTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}

	startedAt := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	snapshot := postgresEvaluatorSnapshot("eval-r1", startedAt, 31, "GOOD")
	initial, err := store.EvaluateAssignedSnapshot(ctx, postgresEvaluatorAssignmentID, snapshot, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Effect != EvaluationEffectNone || initial.State.CandidateSince == nil || initial.State.NextEvaluationAt == nil || *initial.State.NextEvaluationAt != "2026-08-19T12:00:05Z" {
		t.Fatalf("duration state was not persisted before restart: %#v", initial)
	}
	store.Close()

	store, err = OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	claims, err := store.ClaimDueEvaluations(ctx, postgresTestTenantID, "alarm-evaluator-worker-a", startedAt.Add(5*time.Second), 30*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].AssignmentID != postgresEvaluatorAssignmentID {
		t.Fatalf("restart did not recover the due evaluator state: %#v", claims)
	}
	published, err := store.EvaluateClaim(ctx, claims[0], startedAt.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if published.Effect != EvaluationEffectPublish || published.State.ActiveAlarmID == "" || published.State.ActiveIncidentCorrelationID == "" || published.State.Status != EvaluationMatched {
		t.Fatalf("claimed duration evaluation did not publish one active Incident: %#v", published)
	}
	firstAlarmID := published.State.ActiveAlarmID
	if _, err := store.EvaluateClaim(ctx, claims[0], startedAt.Add(5*time.Second)); !errors.Is(err, ErrEvaluationClaimLost) {
		t.Fatalf("replayed timer claim was not fenced after commit: %v", err)
	}

	clearAt := startedAt.Add(6 * time.Second)
	cleared, err := store.EvaluateAssignedSnapshot(ctx, postgresEvaluatorAssignmentID, postgresEvaluatorSnapshot("eval-r2", clearAt, 27, "GOOD"), clearAt)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Effect != EvaluationEffectClear || cleared.State.ActiveAlarmID != "" || cleared.State.ActiveIncidentCorrelationID != "" || cleared.State.Status != EvaluationNotMatched {
		t.Fatalf("clear predicate did not atomically clear Incident and evaluator state: %#v", cleared)
	}
	oldIncident, err := store.Get(ctx, postgresTestTenantID, postgresTestSiteID, firstAlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if oldIncident.Condition != alarmmodel.ConditionCleared {
		t.Fatalf("evaluator state cleared without clearing the S13 Incident: %#v", oldIncident)
	}

	recurrenceAt := startedAt.Add(10 * time.Second)
	candidate, err := store.EvaluateAssignedSnapshot(ctx, postgresEvaluatorAssignmentID, postgresEvaluatorSnapshot("eval-r3", recurrenceAt, 31, "GOOD"), recurrenceAt)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Effect != EvaluationEffectNone {
		t.Fatalf("recurrence bypassed duration policy: %#v", candidate)
	}
	claims, err = store.ClaimDueEvaluations(ctx, postgresTestTenantID, "alarm-evaluator-worker-b", recurrenceAt.Add(5*time.Second), 30*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("recurrence timer was not claimable: %#v", claims)
	}
	recurrence, err := store.EvaluateClaim(ctx, claims[0], recurrenceAt.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if recurrence.Effect != EvaluationEffectPublish || recurrence.State.ActiveAlarmID == firstAlarmID || recurrence.State.ActiveIncidentCorrelationID == published.State.ActiveIncidentCorrelationID {
		t.Fatalf("recovery recurrence did not create a new Incident: first=%#v recurrence=%#v", published, recurrence)
	}
}

func TestPostgresEvaluatorNewSnapshotInvalidatesClaimedTimer(t *testing.T) {
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

	startedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	if _, err := store.EvaluateAssignedSnapshot(ctx, postgresEvaluatorSupersedeAssignmentID, postgresEvaluatorSnapshot("supersede-r1", startedAt, 31, "GOOD"), startedAt); err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimDueEvaluations(ctx, postgresTestTenantID, "alarm-evaluator-worker-stale", startedAt.Add(5*time.Second), 30*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	claim, found := evaluationClaimForAssignment(claims, postgresEvaluatorSupersedeAssignmentID)
	if !found {
		t.Fatalf("superseding assignment timer was not claimed: %#v", claims)
	}
	newerAt := startedAt.Add(5*time.Second + time.Millisecond)
	newer, err := store.EvaluateAssignedSnapshot(ctx, postgresEvaluatorSupersedeAssignmentID, postgresEvaluatorSnapshot("supersede-r2", newerAt, 27, "GOOD"), newerAt)
	if err != nil {
		t.Fatal(err)
	}
	if newer.Effect != EvaluationEffectNone || newer.State.Status != EvaluationNotMatched {
		t.Fatalf("new owner snapshot did not cancel the stale duration candidate: %#v", newer)
	}
	if _, err := store.EvaluateClaim(ctx, claim, newerAt.Add(time.Millisecond)); !errors.Is(err, ErrEvaluationClaimLost) {
		t.Fatalf("superseded timer claim remained effective: %v", err)
	}
}

func TestPostgresEvaluatorExpiredLeaseCannotExecute(t *testing.T) {
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

	startedAt := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	if _, err := store.EvaluateAssignedSnapshot(ctx, postgresEvaluatorLeaseAssignmentID, postgresEvaluatorSnapshot("lease-r1", startedAt, 31, "GOOD"), startedAt); err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimDueEvaluations(ctx, postgresTestTenantID, "alarm-evaluator-worker-expired", startedAt.Add(5*time.Second), time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	claim, found := evaluationClaimForAssignment(claims, postgresEvaluatorLeaseAssignmentID)
	if !found {
		t.Fatalf("lease test assignment timer was not claimed: %#v", claims)
	}
	if _, err := store.EvaluateClaim(ctx, claim, startedAt.Add(6*time.Second)); !errors.Is(err, ErrEvaluationClaimLost) {
		t.Fatalf("expired lease remained executable: %v", err)
	}
	reclaimed, err := store.ClaimDueEvaluations(ctx, postgresTestTenantID, "alarm-evaluator-worker-reclaimed", startedAt.Add(6*time.Second), 30*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	newClaim, found := evaluationClaimForAssignment(reclaimed, postgresEvaluatorLeaseAssignmentID)
	if !found || newClaim.Fence <= claim.Fence {
		t.Fatalf("expired evaluator work was not reclaimable with a higher fence: old=%#v new=%#v", claim, reclaimed)
	}
	decision, err := store.EvaluateClaim(ctx, newClaim, startedAt.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EvaluationEffectPublish || decision.State.ActiveAlarmID == "" {
		t.Fatalf("reclaimed evaluator timer did not publish exactly once: %#v", decision)
	}
}

func postgresLifecyclePolicy(revisionID string, revision uint64, trigger float64) AlarmPolicyRevision {
	policy := validAlarmPolicy()
	policy.PolicyID = postgresPolicyLifecycleID
	policy.PolicyRevisionID = revisionID
	policy.Revision = revision
	policy.AlarmType = "S14_POLICY_LIFECYCLE"
	policy.SourceReference = "alarm-policy:postgres-s14-policy-lifecycle"
	policy.Title = "S14 policy lifecycle"
	policy.Summary = "S14 immutable policy release and assignment fixture."
	policy.TriggerMode = TriggerDuration
	policy.DurationSeconds = 5
	policy.Raise = Condition{Kind: ConditionCompare, Input: "supplyTemp", Operator: CompareGT, Value: NumberValue(trigger)}
	policy.Clear = Condition{Kind: ConditionCompare, Input: "supplyTemp", Operator: CompareLTE, Value: NumberValue(28)}
	sealAlarmPolicy(&policy)
	return policy
}

func evaluationClaimForAssignment(claims []EvaluationClaim, assignmentID string) (EvaluationClaim, bool) {
	for _, claim := range claims {
		if claim.AssignmentID == assignmentID {
			return claim, true
		}
	}
	return EvaluationClaim{}, false
}

func postgresEvaluatorSnapshot(revision string, asOf time.Time, value float64, quality string) EvaluationSnapshot {
	return EvaluationSnapshot{
		TenantID:      postgresTestTenantID,
		SiteID:        postgresTestSiteID,
		SubjectType:   "SITE",
		SubjectID:     postgresTestSiteID,
		InputRevision: revision,
		AsOf:          asOf.UTC().Format(time.RFC3339Nano),
		Inputs: map[string]InputFact{
			"supplyTemp": NumberInput("supplyTemp", value, quality, asOf.UTC().Format(time.RFC3339Nano)),
		},
		CorrelationID: revision,
	}
}
