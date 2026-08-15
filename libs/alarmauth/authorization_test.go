package alarmauth

import "testing"

const (
	testTenantID = "01910000-0000-7000-8000-000000000001"
	testSiteID         = "01910000-0001-7000-8000-000000000001"
	testAlarmID        = "01910000-1000-7000-8000-000000000001"
)

func TestDecisionRequestRequiresExactListAndReadShape(t *testing.T) {
	if err := (DecisionRequest{TenantID: testTenantID, SiteID: testSiteID, Action: ActionList}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (DecisionRequest{TenantID: testTenantID, SiteID: testSiteID, AlarmID: testAlarmID, Action: ActionRead}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []DecisionRequest{
		{TenantID: testTenantID, SiteID: testSiteID, AlarmID: testAlarmID, Action: ActionList},
		{TenantID: testTenantID, SiteID: testSiteID, Action: ActionRead},
		{TenantID: testTenantID, SiteID: "site-1", Action: ActionList},
		{TenantID: testTenantID, SiteID: testSiteID, Action: "alarm:delete"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid request was accepted: %#v", invalid)
		}
	}
}

func TestDecisionValidationRequiresConvergentEvidence(t *testing.T) {
	decision := Decision{
		Allowed: true, PrincipalID: "principal-1", SubjectIssuer: "https://issuer.example", Subject: "subject-1",
		TenantID: testTenantID, SiteID: testSiteID, AlarmID: testAlarmID, Action: ActionRead,
		PolicyRevision: "alarm-policy-1", ReasonCode: ReasonAllowExactScope, DecidedAt: "2026-08-01T00:00:00Z",
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
	decision.PrincipalID = ""
	if err := decision.Validate(); err == nil {
		t.Fatal("allowed decision without principal was accepted")
	}
}
