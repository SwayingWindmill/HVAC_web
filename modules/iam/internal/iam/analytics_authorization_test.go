package iam

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

const (
	analyticsTestSite  = "018f1e00-1000-7000-8000-000000000001"
	analyticsOtherSite = "018f1e00-1000-7000-8000-000000000002"
)

func TestEvaluateAnalyticsAuthorizationFollowsTheAssignmentScope(t *testing.T) {
	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	siteScoped := analyticsAuthorizationFacts(now, analyticsTestSite)
	store := newStaticAuthorizationStore("analytics-policy-1", []AuthorizationFacts{siteScoped})

	allowed, err := evaluateAnalyticsAuthorization(context.Background(), store, now, siteScoped.Principal.SubjectIssuer, siteScoped.Principal.Subject, analyticsmodel.AuthorizationDecisionRequest{
		TenantID: S1FixtureTenantAID, SiteID: analyticsTestSite, Action: analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed.Allowed || allowed.TenantID != S1FixtureTenantAID || allowed.ReasonCode != analyticsmodel.AuthorizationReasonAllowRole || allowed.PolicyRevision != "analytics-policy-1" {
		t.Fatalf("allowed decision = %#v", allowed)
	}
	denied, err := evaluateAnalyticsAuthorization(context.Background(), store, now, siteScoped.Principal.SubjectIssuer, siteScoped.Principal.Subject, analyticsmodel.AuthorizationDecisionRequest{
		TenantID: S1FixtureTenantAID, SiteID: analyticsOtherSite, Action: analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.ReasonCode != analyticsmodel.AuthorizationReasonDenyAction {
		t.Fatalf("cross-site decision = %#v", denied)
	}

	// An assignment to the whole Tenant covers every Site, including ones it never named.
	tenantWide := analyticsAuthorizationFacts(now, "")
	tenantStore := newStaticAuthorizationStore("analytics-policy-1", []AuthorizationFacts{tenantWide})
	other, err := evaluateAnalyticsAuthorization(context.Background(), tenantStore, now, tenantWide.Principal.SubjectIssuer, tenantWide.Principal.Subject, analyticsmodel.AuthorizationDecisionRequest{
		TenantID: S1FixtureTenantAID, SiteID: analyticsOtherSite, Action: analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !other.Allowed {
		t.Fatalf("Tenant-wide decision = %#v", other)
	}
}

func TestEvaluateAnalyticsAuthorizationExplicitDenyWins(t *testing.T) {
	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	facts := analyticsAuthorizationFacts(now, analyticsTestSite)
	facts.ExplicitDenies = []ExplicitDeny{{TenantID: S1FixtureTenantAID, SiteID: analyticsTestSite, Capability: analyticsmodel.EnergySeriesAction, ValidFrom: now.Add(-time.Hour)}}
	store := newStaticAuthorizationStore("analytics-policy-2", []AuthorizationFacts{facts})
	decision, err := evaluateAnalyticsAuthorization(context.Background(), store, now, facts.Principal.SubjectIssuer, facts.Principal.Subject, analyticsmodel.AuthorizationDecisionRequest{
		TenantID: S1FixtureTenantAID, SiteID: analyticsTestSite, Action: analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.ReasonCode != analyticsmodel.AuthorizationReasonDenyExplicit {
		t.Fatalf("decision = %#v", decision)
	}
}

func analyticsAuthorizationFacts(now time.Time, siteID string) AuthorizationFacts {
	return AuthorizationFacts{
		Principal:   PrincipalRecord{ID: "018f1e00-2000-7000-8000-000000000001", SubjectIssuer: "https://issuer.example.test", Subject: "energy-user", Status: FactStatusActive},
		Memberships: []TenantMembership{{TenantID: S1FixtureTenantAID, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		Assignments: []RoleAssignment{{
			TenantID: S1FixtureTenantAID, SiteID: siteID, RoleKey: "viewer", Capabilities: []string{analyticsmodel.EnergySeriesAction},
			Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
		}},
	}
}
