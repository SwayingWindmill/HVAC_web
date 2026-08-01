package iam

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

const (
	workOrderTestIssuer         = "https://issuer.example"
	workOrderTestSubject        = "subject-workOrder"
	workOrderTestPrincipalID    = "principal-workOrder"
	workOrderTestOrganizationID = "01910000-0000-7000-8000-000000000001"
	workOrderTestSiteID         = "01910000-0001-7000-8000-000000000001"
	workOrderTestOtherSiteID    = "01910000-0002-7000-8000-000000000001"
	workOrderTestWorkOrderID    = "01910000-1000-7000-8000-000000000001"
)

func TestEvaluateWorkOrderAuthorizationAllowsExactSiteAction(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := workOrderAuthorizationFixture(now, []WorkOrderPermission{{
		OrganizationID: workOrderTestOrganizationID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionRead,
		Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
	}})
	decision, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, workorderauth.DecisionRequest{
		ActingOrganizationID: workOrderTestOrganizationID, SiteID: workOrderTestSiteID, WorkOrderID: workOrderTestWorkOrderID, Action: workorderauth.ActionRead,
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
		{OrganizationID: workOrderTestOrganizationID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionList, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
		{OrganizationID: workOrderTestOrganizationID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionList, Effect: BindingEffectDeny, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
	})
	denied, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, workorderauth.DecisionRequest{
		ActingOrganizationID: workOrderTestOrganizationID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionList,
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.ReasonCode != workorderauth.ReasonDenyExplicit {
		t.Fatalf("explicit deny was not authoritative: %#v", denied)
	}
	crossSite, err := evaluateWorkOrderAuthorization(context.Background(), store, now, workOrderTestIssuer, workOrderTestSubject, workorderauth.DecisionRequest{
		ActingOrganizationID: workOrderTestOrganizationID, SiteID: workOrderTestOtherSiteID, Action: workorderauth.ActionList,
	})
	if err != nil {
		t.Fatal(err)
	}
	if crossSite.Allowed || crossSite.ReasonCode != workorderauth.ReasonDenyScope {
		t.Fatalf("cross-Site WorkOrder authorization was not denied: %#v", crossSite)
	}
}

func TestWorkOrderCapabilityAllowedKeepsOtherAllowedSiteWhenOneSiteDenied(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	facts, err := workOrderAuthorizationFixture(now, []WorkOrderPermission{
		{OrganizationID: workOrderTestOrganizationID, SiteID: workOrderTestSiteID, Action: workorderauth.ActionList, Effect: BindingEffectDeny, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
		{OrganizationID: workOrderTestOrganizationID, SiteID: workOrderTestOtherSiteID, Action: workorderauth.ActionList, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
	}).LookupWorkOrderAuthorization(context.Background(), AuthorizationLookup{SubjectIssuer: workOrderTestIssuer, Subject: workOrderTestSubject, ActingOrganizationID: workOrderTestOrganizationID})
	if err != nil {
		t.Fatal(err)
	}
	if !workOrderCapabilityAllowed(facts, now, workOrderTestOrganizationID, workorderauth.ActionList) {
		t.Fatal("an allowed WorkOrder Site did not produce an effective capability")
	}
}

func workOrderAuthorizationFixture(now time.Time, permissions []WorkOrderPermission) WorkOrderAuthorizationStore {
	return newStaticWorkOrderAuthorizationStore("workOrder-policy-1", []WorkOrderAuthorizationFacts{{
		Principal:   PrincipalRecord{ID: workOrderTestPrincipalID, SubjectIssuer: workOrderTestIssuer, Subject: workOrderTestSubject, Status: FactStatusActive},
		Memberships: []OrganizationMembership{{OrganizationID: workOrderTestOrganizationID, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		Permissions: permissions,
	}})
}
