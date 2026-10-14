package iam

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
)

const (
	alarmTestIssuer      = "https://issuer.example"
	alarmTestSubject     = "subject-alarm"
	alarmTestPrincipalID = "principal-alarm"
	alarmTestTenantID    = "01910000-0000-7000-8000-000000000001"
	alarmTestSiteID      = "01910000-0001-7000-8000-000000000001"
	alarmTestOtherSiteID = "01910000-0002-7000-8000-000000000001"
	alarmTestAlarmID     = "01910000-1000-7000-8000-000000000001"
)

func TestEvaluateAlarmAuthorizationAllowsTheAssignedSiteOnly(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := alarmAuthorizationFixture(now, []RoleAssignment{{
		TenantID: alarmTestTenantID, SiteID: alarmTestSiteID, RoleKey: "operator", Capabilities: []string{string(alarmauth.ActionRead)},
		Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
	}}, nil)
	decision, err := evaluateAlarmAuthorization(context.Background(), store, now, alarmTestIssuer, alarmTestSubject, alarmauth.DecisionRequest{
		TenantID: alarmTestTenantID, SiteID: alarmTestSiteID, AlarmID: alarmTestAlarmID, Action: alarmauth.ActionRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed || decision.PrincipalID != alarmTestPrincipalID || decision.ReasonCode != alarmauth.ReasonAllowExactScope {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
	crossSite, err := evaluateAlarmAuthorization(context.Background(), store, now, alarmTestIssuer, alarmTestSubject, alarmauth.DecisionRequest{
		TenantID: alarmTestTenantID, SiteID: alarmTestOtherSiteID, Action: alarmauth.ActionRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if crossSite.Allowed || crossSite.ReasonCode != alarmauth.ReasonDenyScope {
		t.Fatalf("cross-Site Alarm authorization was not denied: %#v", crossSite)
	}
}

func TestEvaluateAlarmAuthorizationHonoursAnExplicitDenyOverATenantRole(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := alarmAuthorizationFixture(now,
		[]RoleAssignment{{TenantID: alarmTestTenantID, RoleKey: "operator", Capabilities: []string{string(alarmauth.ActionRead)}, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		[]ExplicitDeny{{TenantID: alarmTestTenantID, SiteID: alarmTestSiteID, Capability: string(alarmauth.ActionRead), ValidFrom: now.Add(-time.Hour)}},
	)
	denied, err := evaluateAlarmAuthorization(context.Background(), store, now, alarmTestIssuer, alarmTestSubject, alarmauth.DecisionRequest{
		TenantID: alarmTestTenantID, SiteID: alarmTestSiteID, Action: alarmauth.ActionRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.ReasonCode != alarmauth.ReasonDenyExplicit {
		t.Fatalf("explicit deny was not authoritative: %#v", denied)
	}
	other, err := evaluateAlarmAuthorization(context.Background(), store, now, alarmTestIssuer, alarmTestSubject, alarmauth.DecisionRequest{
		TenantID: alarmTestTenantID, SiteID: alarmTestOtherSiteID, Action: alarmauth.ActionRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !other.Allowed {
		t.Fatalf("a Site deny removed the Tenant role elsewhere: %#v", other)
	}
}

func alarmAuthorizationFixture(now time.Time, assignments []RoleAssignment, denies []ExplicitDeny) AlarmAuthorizationStore {
	return newStaticAlarmAuthorizationStore("alarm-policy-1", []AuthorizationFacts{{
		Principal:      PrincipalRecord{ID: alarmTestPrincipalID, SubjectIssuer: alarmTestIssuer, Subject: alarmTestSubject, Status: FactStatusActive},
		Memberships:    []TenantMembership{{TenantID: alarmTestTenantID, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		Assignments:    assignments,
		ExplicitDenies: denies,
	}})
}
