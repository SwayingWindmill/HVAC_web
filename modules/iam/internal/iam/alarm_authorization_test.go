package iam

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
)

const (
	alarmTestIssuer         = "https://issuer.example"
	alarmTestSubject        = "subject-alarm"
	alarmTestPrincipalID    = "principal-alarm"
	alarmTestTenantID = "01910000-0000-7000-8000-000000000001"
	alarmTestSiteID         = "01910000-0001-7000-8000-000000000001"
	alarmTestOtherSiteID    = "01910000-0002-7000-8000-000000000001"
	alarmTestAlarmID        = "01910000-1000-7000-8000-000000000001"
)

func TestEvaluateAlarmAuthorizationAllowsExactSiteAction(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := alarmAuthorizationFixture(now, []AlarmPermission{{
		TenantID: alarmTestTenantID, SiteID: alarmTestSiteID, Action: alarmauth.ActionRead,
		Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
	}})
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
}

func TestEvaluateAlarmAuthorizationDeniesCrossSiteAndExplicitDeny(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := alarmAuthorizationFixture(now, []AlarmPermission{
		{TenantID: alarmTestTenantID, SiteID: alarmTestSiteID, Action: alarmauth.ActionRead, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
		{TenantID: alarmTestTenantID, SiteID: alarmTestSiteID, Action: alarmauth.ActionRead, Effect: BindingEffectDeny, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
	})
	denied, err := evaluateAlarmAuthorization(context.Background(), store, now, alarmTestIssuer, alarmTestSubject, alarmauth.DecisionRequest{
		TenantID: alarmTestTenantID, SiteID: alarmTestSiteID, Action: alarmauth.ActionRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.ReasonCode != alarmauth.ReasonDenyExplicit {
		t.Fatalf("explicit deny was not authoritative: %#v", denied)
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

func TestAlarmCapabilityAllowedKeepsOtherAllowedSiteWhenOneSiteDenied(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	facts, err := alarmAuthorizationFixture(now, []AlarmPermission{
		{TenantID: alarmTestTenantID, SiteID: alarmTestSiteID, Action: alarmauth.ActionRead, Effect: BindingEffectDeny, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
		{TenantID: alarmTestTenantID, SiteID: alarmTestOtherSiteID, Action: alarmauth.ActionRead, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
	}).LookupAlarmAuthorization(context.Background(), AuthorizationLookup{SubjectIssuer: alarmTestIssuer, Subject: alarmTestSubject, TenantID: alarmTestTenantID})
	if err != nil {
		t.Fatal(err)
	}
	if !alarmCapabilityAllowed(facts, now, alarmTestTenantID, alarmauth.ActionRead) {
		t.Fatal("an allowed Alarm Site did not produce an effective capability")
	}
}

func alarmAuthorizationFixture(now time.Time, permissions []AlarmPermission) AlarmAuthorizationStore {
	return newStaticAlarmAuthorizationStore("alarm-policy-1", []AlarmAuthorizationFacts{{
		Principal:   PrincipalRecord{ID: alarmTestPrincipalID, SubjectIssuer: alarmTestIssuer, Subject: alarmTestSubject, Status: FactStatusActive},
		Memberships: []TenantMembership{{TenantID: alarmTestTenantID, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		Permissions: permissions,
	}})
}
