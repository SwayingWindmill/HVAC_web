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

func workOrderAssignment(now time.Time, siteID string, actions ...workorderauth.Action) RoleAssignment {
	capabilities := make([]string, len(actions))
	for index, action := range actions {
		capabilities[index] = string(action)
	}
	return RoleAssignment{TenantID: workOrderTestTenantID, SiteID: siteID, RoleKey: "operator", Capabilities: capabilities, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}
}

func workOrderFacts(now time.Time, principalID, subject string, assignments ...RoleAssignment) WorkOrderAuthorizationFacts {
	return WorkOrderAuthorizationFacts{AuthorizationFacts: AuthorizationFacts{
		Principal:   PrincipalRecord{ID: principalID, SubjectIssuer: workOrderTestIssuer, Subject: subject, Status: FactStatusActive},
		Memberships: []TenantMembership{{TenantID: workOrderTestTenantID, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		Assignments: assignments,
	}}
}

func decideWorkOrder(t *testing.T, now time.Time, store WorkOrderAuthorizationStore, request workorderauth.DecisionRequest) workorderauth.Decision {
	t.Helper()
	request.TenantID = workOrderTestTenantID
	decision, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, request)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func TestEvaluateWorkOrderAuthorizationAllowsTheAssignedSiteOnly(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := newStaticWorkOrderAuthorizationStore("workOrder-policy-1", []WorkOrderAuthorizationFacts{
		workOrderFacts(now, workOrderTestPrincipalID, workOrderTestSubject, workOrderAssignment(now, workOrderTestSiteID, workorderauth.ActionRead)),
	})
	decision := decideWorkOrder(t, now, store, workorderauth.DecisionRequest{SiteID: workOrderTestSiteID, WorkOrderID: workOrderTestWorkOrderID, Action: workorderauth.ActionRead})
	if !decision.Allowed || decision.PrincipalID != workOrderTestPrincipalID || decision.ReasonCode != workorderauth.ReasonAllowExactScope {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
	if crossSite := decideWorkOrder(t, now, store, workorderauth.DecisionRequest{SiteID: workOrderTestOtherSiteID, WorkOrderID: workOrderTestWorkOrderID, Action: workorderauth.ActionRead}); crossSite.Allowed || crossSite.ReasonCode != workorderauth.ReasonDenyScope {
		t.Fatalf("cross-Site WorkOrder authorization was not denied: %#v", crossSite)
	}
}

func TestEvaluateWorkOrderAuthorizationAssignsPeopleWhoMayStartWorkOnTheSite(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	dispatcher := workOrderFacts(now, workOrderTestPrincipalID, workOrderTestSubject, workOrderAssignment(now, workOrderTestSiteID, workorderauth.ActionAssign, workorderauth.ActionStart))
	worker := workOrderFacts(now, "principal-worker", "subject-worker", workOrderAssignment(now, workOrderTestSiteID, workorderauth.ActionStart))
	elsewhere := workOrderFacts(now, "principal-elsewhere", "subject-elsewhere", workOrderAssignment(now, workOrderTestOtherSiteID, workorderauth.ActionStart))
	viewer := workOrderFacts(now, "principal-viewer", "subject-viewer", workOrderAssignment(now, workOrderTestSiteID, workorderauth.ActionRead))
	store := newStaticWorkOrderAuthorizationStore("workOrder-policy-assign", []WorkOrderAuthorizationFacts{dispatcher, worker, elsewhere, viewer})
	assign := func(assignee string) workorderauth.Decision {
		return decideWorkOrder(t, now, store, workorderauth.DecisionRequest{
			SiteID: workOrderTestSiteID, WorkOrderID: workOrderTestWorkOrderID, AssigneeID: &assignee, Action: workorderauth.ActionAssign,
		})
	}
	for _, assignee := range []string{workOrderTestPrincipalID, "principal-worker"} {
		if decision := assign(assignee); !decision.Allowed || decision.AssigneeID == nil || *decision.AssigneeID != assignee {
			t.Fatalf("assigning %s was refused: %#v", assignee, decision)
		}
	}
	for _, assignee := range []string{"principal-elsewhere", "principal-viewer", "principal-unknown"} {
		if decision := assign(assignee); decision.Allowed || decision.ReasonCode != workorderauth.ReasonDenyScope {
			t.Fatalf("assigning %s was allowed: %#v", assignee, decision)
		}
	}
}

func TestEvaluateWorkOrderAuthorizationRequiresADeclaredTeamTarget(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	team := "team:hvac"
	facts := workOrderFacts(now, workOrderTestPrincipalID, workOrderTestSubject, workOrderAssignment(now, workOrderTestSiteID, workorderauth.ActionAssign))
	assignTeam := func(facts WorkOrderAuthorizationFacts) workorderauth.Decision {
		store := newStaticWorkOrderAuthorizationStore("workOrder-policy-team", []WorkOrderAuthorizationFacts{facts})
		return decideWorkOrder(t, now, store, workorderauth.DecisionRequest{SiteID: workOrderTestSiteID, WorkOrderID: workOrderTestWorkOrderID, TeamID: &team, Action: workorderauth.ActionAssign})
	}
	if decision := assignTeam(facts); decision.Allowed || decision.ReasonCode != workorderauth.ReasonDenyScope {
		t.Fatalf("undeclared team was assignable: %#v", decision)
	}
	facts.Targets = []WorkOrderOwnershipTarget{{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, TargetType: "TEAM", TargetID: team, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}}
	if decision := assignTeam(facts); !decision.Allowed {
		t.Fatalf("declared team was refused: %#v", decision)
	}
	facts.Targets = append(facts.Targets, WorkOrderOwnershipTarget{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, TargetType: "TEAM", TargetID: team, Effect: BindingEffectDeny, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)})
	if decision := assignTeam(facts); decision.Allowed || decision.ReasonCode != workorderauth.ReasonDenyExplicit {
		t.Fatalf("team deny did not win: %#v", decision)
	}
}

func TestEvaluateWorkOrderLifecycleAuthorizationRequiresExactResourceAndPreservesDenyWins(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	facts := workOrderFacts(now, workOrderTestPrincipalID, workOrderTestSubject, workOrderAssignment(now, "", workorderauth.ActionComplete))
	facts.ExplicitDenies = []ExplicitDeny{{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Capability: string(workorderauth.ActionComplete), ValidFrom: now.Add(-time.Minute)}}
	store := newStaticWorkOrderAuthorizationStore("workOrder-policy-1", []WorkOrderAuthorizationFacts{facts})
	if decision := decideWorkOrder(t, now, store, workorderauth.DecisionRequest{SiteID: workOrderTestSiteID, WorkOrderID: workOrderTestWorkOrderID, Action: workorderauth.ActionComplete}); decision.Allowed || decision.ReasonCode != workorderauth.ReasonDenyExplicit {
		t.Fatalf("lifecycle deny-wins decision=%#v", decision)
	}
	for _, action := range []workorderauth.Action{workorderauth.ActionPlan, workorderauth.ActionStart, workorderauth.ActionBlock, workorderauth.ActionResume, workorderauth.ActionComplete, workorderauth.ActionCancel, workorderauth.ActionReopen} {
		if err := (workorderauth.DecisionRequest{TenantID: workOrderTestTenantID, SiteID: workOrderTestSiteID, Action: action}).Validate(); err == nil {
			t.Fatalf("action %q accepted without Work Order identity", action)
		}
	}
}
