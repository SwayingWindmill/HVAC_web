package workorderauth

import "testing"

const (
	testOrganizationID = "01910000-0000-7000-8000-000000000001"
	testSiteID         = "01910000-0001-7000-8000-000000000001"
	testWorkOrderID    = "01910000-5000-7000-8000-000000000001"
)

func TestDecisionRequestRequiresExactScopeForAction(t *testing.T) {
	tests := map[string]struct {
		request DecisionRequest
		valid   bool
	}{
		"list exact Site":           {DecisionRequest{ActingOrganizationID: testOrganizationID, SiteID: testSiteID, Action: ActionList}, true},
		"detail exact Work Order":   {DecisionRequest{ActingOrganizationID: testOrganizationID, SiteID: testSiteID, WorkOrderID: testWorkOrderID, Action: ActionRead}, true},
		"list with Work Order":      {DecisionRequest{ActingOrganizationID: testOrganizationID, SiteID: testSiteID, WorkOrderID: testWorkOrderID, Action: ActionList}, false},
		"detail without Work Order": {DecisionRequest{ActingOrganizationID: testOrganizationID, SiteID: testSiteID, Action: ActionRead}, false},
		"unsupported action":        {DecisionRequest{ActingOrganizationID: testOrganizationID, SiteID: testSiteID, Action: "work-order:write"}, false},
		"invalid Organization":      {DecisionRequest{ActingOrganizationID: "organization", SiteID: testSiteID, Action: ActionList}, false},
		"invalid Site":              {DecisionRequest{ActingOrganizationID: testOrganizationID, SiteID: "site", Action: ActionList}, false},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := test.request.Validate()
			if test.valid && err != nil {
				t.Fatalf("valid request rejected: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func TestDecisionValidationPreservesAllowAndDenyEvidence(t *testing.T) {
	allowed := Decision{
		Allowed: true, PrincipalID: "01910000-7000-7000-8000-000000000001",
		SubjectIssuer: "https://identity.example.test", Subject: "operator",
		ActingOrganizationID: testOrganizationID, SiteID: testSiteID,
		WorkOrderID: testWorkOrderID, Action: ActionRead,
		PolicyRevision: "work-order-access:1", ReasonCode: ReasonAllowExactScope,
		DecidedAt: "2026-08-01T10:00:00Z",
	}
	if err := allowed.Validate(); err != nil {
		t.Fatalf("allowed decision rejected: %v", err)
	}
	denied := allowed
	denied.Allowed = false
	denied.PrincipalID = ""
	denied.ReasonCode = ReasonDenyExplicit
	if err := denied.Validate(); err != nil {
		t.Fatalf("denied decision rejected: %v", err)
	}
	invalid := []Decision{
		func() Decision { value := allowed; value.PrincipalID = ""; return value }(),
		func() Decision { value := allowed; value.ReasonCode = ReasonDenyScope; return value }(),
		func() Decision { value := denied; value.ReasonCode = ReasonAllowExactScope; return value }(),
		func() Decision { value := denied; value.DecidedAt = "invalid"; return value }(),
		func() Decision { value := denied; value.PolicyRevision = ""; return value }(),
	}
	for index, decision := range invalid {
		if err := decision.Validate(); err == nil {
			t.Fatalf("invalid decision %d accepted", index)
		}
	}
}
