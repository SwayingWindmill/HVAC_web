package iam

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	analyticsTestOrganization = "018f1e00-0000-7000-8000-000000000001"
	analyticsTestSite         = "018f1e00-1000-7000-8000-000000000001"
	analyticsOtherSite        = "018f1e00-1000-7000-8000-000000000002"
)

func TestEvaluateAnalyticsAuthorizationRequiresExactSiteScope(t *testing.T) {
	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	facts := analyticsAuthorizationFacts(now)
	store := newStaticAuthorizationStore("analytics-policy-1", []AuthorizationFacts{facts})

	allowed, err := evaluateAnalyticsAuthorization(context.Background(), store, now, facts.Principal.SubjectIssuer, facts.Principal.Subject, analyticsmodel.AuthorizationDecisionRequest{
		ActingOrganizationID: analyticsTestOrganization, SiteID: analyticsTestSite, Action: analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed.Allowed || allowed.ReasonCode != analyticsmodel.AuthorizationReasonAllowSiteBinding || allowed.PolicyRevision != "analytics-policy-1" {
		t.Fatalf("allowed decision = %#v", allowed)
	}

	denied, err := evaluateAnalyticsAuthorization(context.Background(), store, now, facts.Principal.SubjectIssuer, facts.Principal.Subject, analyticsmodel.AuthorizationDecisionRequest{
		ActingOrganizationID: analyticsTestOrganization, SiteID: analyticsOtherSite, Action: analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.ReasonCode != analyticsmodel.AuthorizationReasonDenyAction {
		t.Fatalf("cross-site decision = %#v", denied)
	}
}

func TestEvaluateAnalyticsAuthorizationDoesNotTreatOrganizationRoleAsSiteProof(t *testing.T) {
	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	facts := analyticsAuthorizationFacts(now)
	facts.SiteBindings = nil
	facts.RoleBindings = []RoleBinding{{
		OrganizationID: analyticsTestOrganization,
		Actions:        []registryauth.Action{registryauth.Action(analyticsmodel.EnergySeriesAction)},
		Effect:         BindingEffectAllow,
		Status:         FactStatusActive,
		ValidFrom:      now.Add(-time.Hour),
	}}
	store := newStaticAuthorizationStore("analytics-policy-site-proof", []AuthorizationFacts{facts})
	decision, err := evaluateAnalyticsAuthorization(context.Background(), store, now, facts.Principal.SubjectIssuer, facts.Principal.Subject, analyticsmodel.AuthorizationDecisionRequest{
		ActingOrganizationID: analyticsTestOrganization, SiteID: analyticsTestSite, Action: analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.ReasonCode != analyticsmodel.AuthorizationReasonDenyAction {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestEvaluateAnalyticsAuthorizationExplicitDenyWins(t *testing.T) {
	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	facts := analyticsAuthorizationFacts(now)
	facts.ExplicitDenies = []ExplicitDeny{{
		ActingOrganizationID: analyticsTestOrganization,
		SiteID:               analyticsTestSite,
		Actions:              []registryauth.Action{registryauth.Action(analyticsmodel.EnergySeriesAction)},
		Status:               FactStatusActive,
		ValidFrom:            now.Add(-time.Hour),
	}}
	store := newStaticAuthorizationStore("analytics-policy-2", []AuthorizationFacts{facts})
	decision, err := evaluateAnalyticsAuthorization(context.Background(), store, now, facts.Principal.SubjectIssuer, facts.Principal.Subject, analyticsmodel.AuthorizationDecisionRequest{
		ActingOrganizationID: analyticsTestOrganization, SiteID: analyticsTestSite, Action: analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.ReasonCode != analyticsmodel.AuthorizationReasonDenyExplicit {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestPostgresRegistryActionsPreservesAnalyticsAction(t *testing.T) {
	actions, err := postgresRegistryActions([]string{analyticsmodel.EnergySeriesAction})
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || string(actions[0]) != analyticsmodel.EnergySeriesAction {
		t.Fatalf("actions=%#v", actions)
	}
}

func analyticsAuthorizationFacts(now time.Time) AuthorizationFacts {
	return AuthorizationFacts{
		Principal:   PrincipalRecord{ID: "018f1e00-2000-7000-8000-000000000001", SubjectIssuer: "https://issuer.example.test", Subject: "energy-user", Status: FactStatusActive},
		Memberships: []OrganizationMembership{{OrganizationID: analyticsTestOrganization, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		SiteBindings: []SiteBinding{{
			ActingOrganizationID: analyticsTestOrganization,
			OwningOrganizationID: analyticsTestOrganization,
			SiteID:               analyticsTestSite,
			Actions:              []registryauth.Action{registryauth.Action(analyticsmodel.EnergySeriesAction)},
			Effect:               BindingEffectAllow,
			Status:               FactStatusActive,
			ValidFrom:            now.Add(-time.Hour),
		}},
	}
}
