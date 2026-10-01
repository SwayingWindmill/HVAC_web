package iam

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

const (
	workOrderTestIssuer      = "https://issuer.example"
	workOrderTestSubject     = "subject-workOrder"
	workOrderTestPrincipalID = "principal-workOrder"
	workOrderTestTenantID    = "01910000-0000-7000-8000-000000000001"
	workOrderTestSiteID      = "01910000-0001-7000-8000-000000000001"
	workOrderTestOtherSiteID = "01910000-0002-7000-8000-000000000001"
	workOrderTestWorkOrderID = "01910000-1000-7000-8000-000000000001"
)

func TestEvaluateWorkOrderAuthorizationAllowsExactSiteAction(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := workOrderAuthorizationFixture(now, []WorkOrderPermission{{
		TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionRead,
		Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
	}})
	decision, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, workorderauth.DecisionRequest{
		TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, WorkOrderID: workOrderTestWorkOrderID, Action: workorderauth.ActionRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed || decision.PrincipalID != workOrderTestPrincipalID || decision.ReasonCode != workorderauth.ReasonAllowExactScope {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateWorkOrderAuthorizationDeniesCrossSiteAndExplicitDeny(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := workOrderAuthorizationFixture(now, []WorkOrderPermission{
		{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionList, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
		{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionList, Effect: BindingEffectDeny, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
	})
	denied, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, workorderauth.DecisionRequest{
		TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionList,
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.ReasonCode != workorderauth.ReasonDenyExplicit {
		t.Fatalf("explicit deny was not authoritative: %#v", denied)
	}
	crossSite, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, workorderauth.DecisionRequest{
		TenantID: workOrderTestTenantID, SiteID: workOrderTestOtherSiteID, Action: workorderauth.ActionList,
	})
	if err != nil {
		t.Fatal(err)
	}
	if crossSite.Allowed || crossSite.ReasonCode != workorderauth.ReasonDenyScope {
		t.Fatalf("cross-Site WorkOrder authorization was not denied: %#v", crossSite)
	}
}

func TestEvaluateWorkOrderAuthorizationRequiresDeclaredOwnershipTargets(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	assignee := "principal:operator"
	team := "team:hvac"
	base := WorkOrderAuthorizationFacts{
		Principal:   PrincipalRecord{ID: workOrderTestPrincipalID, SubjectIssuer: workOrderTestIssuer, Subject: workOrderTestSubject, Status: FactStatusActive},
		Memberships: []TenantMembership{{TenantID: workOrderTestTenantID, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		Permissions: []WorkOrderPermission{{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionAssign, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		Targets: []WorkOrderOwnershipTarget{
			{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, TargetType: "PRINCIPAL", TargetID: assignee, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
			{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, TargetType: "TEAM", TargetID: team, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
		},
	}
	store := newStaticWorkOrderAuthorizationStore("workOrder-policy-targets-1", []WorkOrderAuthorizationFacts{base})
	allowed, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, workorderauth.DecisionRequest{
		TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, WorkOrderID: workOrderTestWorkOrderID,
		AssigneeID: &assignee, TeamID: &team, Action: workorderauth.ActionAssign,
	})
	if err != nil || !allowed.Allowed || allowed.AssigneeID == nil || *allowed.AssigneeID != assignee || allowed.TeamID == nil || *allowed.TeamID != team {
		t.Fatalf("declared targets were not authorized: decision=%#v err=%v", allowed, err)
	}
	undeclared := "principal:undeclared"
	denied, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, workorderauth.DecisionRequest{
		TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, WorkOrderID: workOrderTestWorkOrderID,
		AssigneeID: &undeclared, Action: workorderauth.ActionAssign,
	})
	if err != nil || denied.Allowed || denied.ReasonCode != workorderauth.ReasonDenyScope {
		t.Fatalf("undeclared target was not denied: decision=%#v err=%v", denied, err)
	}
	base.Targets = append(base.Targets, WorkOrderOwnershipTarget{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, TargetType: "PRINCIPAL", TargetID: assignee, Effect: BindingEffectDeny, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)})
	store = newStaticWorkOrderAuthorizationStore("workOrder-policy-targets-2", []WorkOrderAuthorizationFacts{base})
	explicit, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, workorderauth.DecisionRequest{
		TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, WorkOrderID: workOrderTestWorkOrderID,
		AssigneeID: &assignee, Action: workorderauth.ActionAssign,
	})
	if err != nil || explicit.Allowed || explicit.ReasonCode != workorderauth.ReasonDenyExplicit {
		t.Fatalf("explicit target deny did not win: decision=%#v err=%v", explicit, err)
	}
}

func TestWorkOrderCapabilityAllowedKeepsOtherAllowedSiteWhenOneSiteDenied(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	facts, err := workOrderAuthorizationFixture(now, []WorkOrderPermission{
		{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionList, Effect: BindingEffectDeny, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
		{TenantID: workOrderTestTenantID, SiteID: workOrderTestOtherSiteID, Action: workorderauth.ActionList, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
	}).LookupWorkOrderAuthorization(context.Background(), AuthorizationLookup{SubjectIssuer: workOrderTestIssuer, Subject: workOrderTestSubject, TenantID: workOrderTestTenantID})
	if err != nil {
		t.Fatal(err)
	}
	if !workOrderCapabilityAllowed(facts, now, workOrderTestTenantID, workorderauth.ActionList) {
		t.Fatal("an allowed WorkOrder Site did not produce an effective capability")
	}
}

func workOrderAuthorizationFixture(now time.Time, permissions []WorkOrderPermission) WorkOrderAuthorizationStore {
	return newStaticWorkOrderAuthorizationStore("workOrder-policy-1", []WorkOrderAuthorizationFacts{{
		Principal:   PrincipalRecord{ID: workOrderTestPrincipalID, SubjectIssuer: workOrderTestIssuer, Subject: workOrderTestSubject, Status: FactStatusActive},
		Memberships: []TenantMembership{{TenantID: workOrderTestTenantID, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		Permissions: permissions,
	}})
}

func TestEvaluateWorkOrderLifecycleAuthorizationRequiresExactResourceAndPreservesDenyWins(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	store := workOrderAuthorizationFixture(now, []WorkOrderPermission{
		{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionComplete, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
		{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionComplete, Effect: BindingEffectDeny, Status: FactStatusActive, ValidFrom: now.Add(-time.Minute)},
	})
	decision, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, workorderauth.DecisionRequest{
		TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, WorkOrderID: workOrderTestWorkOrderID, Action: workorderauth.ActionComplete,
	})
	if err != nil || decision.Allowed || decision.ReasonCode != workorderauth.ReasonDenyExplicit {
		t.Fatalf("lifecycle deny-wins decision=%#v err=%v", decision, err)
	}
	for _, action := range []workorderauth.Action{workorderauth.ActionPlan, workorderauth.ActionStart, workorderauth.ActionBlock, workorderauth.ActionResume, workorderauth.ActionComplete, workorderauth.ActionCancel, workorderauth.ActionReopen} {
		if err := (workorderauth.DecisionRequest{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Action: action}).Validate(); err == nil {
			t.Fatalf("action %q accepted without Work Order identity", action)
		}
	}
}
