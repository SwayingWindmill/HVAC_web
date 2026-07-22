package iam

import "github.com/quanlaihe/hvac-web/libs/registryauth"

const (
	S1FixturePolicyRevision = "s1-policy-v1"

	S1FixtureOwnerAOrganizationID = "018f1e00-0000-7000-8000-000000000001"
	S1FixtureOwnerBOrganizationID = "018f1e00-0000-7000-8000-000000000002"
	S1FixtureActingOrganizationID = "018f1e00-0000-7000-8000-000000000003"

	S1FixtureOwnerASite1ID = "018f1e00-1000-7000-8000-000000000001"
	S1FixtureOwnerASite2ID = "018f1e00-1000-7000-8000-000000000002"
	S1FixtureOwnerBSite1ID = "018f1e00-1000-7000-8000-000000000003"

	S1FixtureOwnerAPrincipalID = "018f1e00-2000-7000-8000-000000000001"
	S1FixtureDelegatedID       = "018f1e00-2000-7000-8000-000000000002"
	S1FixtureDeniedID          = "018f1e00-2000-7000-8000-000000000003"
	S1FixtureNoAccessID        = "018f1e00-2000-7000-8000-000000000004"
	S1FixtureRevokedMemberID   = "018f1e00-2000-7000-8000-000000000005"
)

func NewS1FixtureAuthorizationStore(subjectIssuer string) AuthorizationStore {
	if subjectIssuer == "" {
		subjectIssuer = "https://issuer.example.test"
	}
	return newStaticAuthorizationStore(S1FixturePolicyRevision, []AuthorizationFacts{
		{
			Principal: PrincipalRecord{ID: S1FixtureOwnerAPrincipalID, SubjectIssuer: subjectIssuer, Subject: "fixture-user", Status: FactStatusActive},
			Memberships: []OrganizationMembership{
				{OrganizationID: S1FixtureOwnerAOrganizationID, Status: FactStatusActive},
			},
			RoleBindings: []RoleBinding{
				{OrganizationID: S1FixtureOwnerAOrganizationID, Actions: []registryauth.Action{registryauth.ActionRegistryRead}, Status: FactStatusActive},
			},
		},
		{
			Principal: PrincipalRecord{ID: S1FixtureDelegatedID, SubjectIssuer: subjectIssuer, Subject: "fixture-delegated-user", Status: FactStatusActive},
			Memberships: []OrganizationMembership{
				{OrganizationID: S1FixtureActingOrganizationID, Status: FactStatusActive},
			},
			SiteBindings: []SiteBinding{
				{
					ActingOrganizationID: S1FixtureActingOrganizationID,
					OwningOrganizationID: S1FixtureOwnerAOrganizationID,
					SiteID:               S1FixtureOwnerASite1ID,
					Actions:              []registryauth.Action{registryauth.ActionRegistryRead},
					Status:               FactStatusActive,
				},
			},
		},
		{
			Principal: PrincipalRecord{ID: S1FixtureDeniedID, SubjectIssuer: subjectIssuer, Subject: "fixture-denied-user", Status: FactStatusActive},
			Memberships: []OrganizationMembership{
				{OrganizationID: S1FixtureOwnerAOrganizationID, Status: FactStatusActive},
			},
			RoleBindings: []RoleBinding{
				{OrganizationID: S1FixtureOwnerAOrganizationID, Actions: []registryauth.Action{registryauth.ActionRegistryRead}, Status: FactStatusActive},
			},
			ExplicitDenies: []ExplicitDeny{
				{ActingOrganizationID: S1FixtureOwnerAOrganizationID, OrganizationID: S1FixtureOwnerAOrganizationID, Actions: []registryauth.Action{registryauth.ActionRegistryRead}, Status: FactStatusActive},
			},
		},
		{
			Principal: PrincipalRecord{ID: S1FixtureNoAccessID, SubjectIssuer: subjectIssuer, Subject: "fixture-no-access-user", Status: FactStatusActive},
			Memberships: []OrganizationMembership{
				{OrganizationID: S1FixtureActingOrganizationID, Status: FactStatusActive},
			},
		},
		{
			Principal: PrincipalRecord{ID: S1FixtureRevokedMemberID, SubjectIssuer: subjectIssuer, Subject: "fixture-revoked-user", Status: FactStatusActive},
			Memberships: []OrganizationMembership{
				{OrganizationID: S1FixtureActingOrganizationID, Status: FactStatusRevoked},
			},
			SiteBindings: []SiteBinding{
				{
					ActingOrganizationID: S1FixtureActingOrganizationID,
					OwningOrganizationID: S1FixtureOwnerAOrganizationID,
					SiteID:               S1FixtureOwnerASite1ID,
					Actions:              []registryauth.Action{registryauth.ActionRegistryRead},
					Status:               FactStatusActive,
				},
			},
		},
	})
}

func NewDenyAllAuthorizationStore(policyRevision string) AuthorizationStore {
	if policyRevision == "" {
		policyRevision = "policy-unconfigured"
	}
	return newDenyAllAuthorizationStore(policyRevision)
}
