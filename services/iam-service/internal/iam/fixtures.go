package iam

import (
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

const (
	S1FixturePolicyRevision = "s1-policy-v1"
	S1FixtureTenantAID      = "018f1d00-0000-7000-8000-000000000001"
	S1FixtureTenantBID      = "018f1d00-0000-7000-8000-000000000002"

	S1FixtureOwnerASite1ID = "018f1e00-1000-7000-8000-000000000001"
	S1FixtureOwnerASite2ID = "018f1e00-1000-7000-8000-000000000002"
	S1FixtureOwnerBSite1ID = "018f1e00-1000-7000-8000-000000000003"

	S1FixtureOwnerAPrincipalID = "018f1e00-2000-7000-8000-000000000001"
	S1FixtureDelegatedID       = "018f1e00-2000-7000-8000-000000000002"
	S1FixtureDeniedID          = "018f1e00-2000-7000-8000-000000000003"
	S1FixtureNoAccessID        = "018f1e00-2000-7000-8000-000000000004"
	S1FixtureRevokedMemberID   = "018f1e00-2000-7000-8000-000000000005"

	S2FixturePolicyRevision = "s2-policy-v1"
	S2FixtureTenantID       = "018f2d00-0000-7000-8000-000000000001"
	S2FixtureSite           = "018f2e00-2000-7000-8000-000000000001"
	S2FixtureDevice         = "018f2e00-3000-7000-8000-000000000001"
	S2FixtureDeviceTwo      = "018f2e00-3000-7000-8000-000000000002"
	S2FixturePrincipal      = "018f2e00-6000-7000-8000-000000000001"
)

func NewS1FixtureAuthorizationStore(subjectIssuer string) AuthorizationStore {
	if subjectIssuer == "" {
		subjectIssuer = "https://issuer.example.test"
	}
	return newStaticAuthorizationStore(S1FixturePolicyRevision, []AuthorizationFacts{
		{
			Principal:     PrincipalRecord{ID: S1FixtureOwnerAPrincipalID, SubjectIssuer: subjectIssuer, Subject: "fixture-user", Status: FactStatusActive},
			TenantSiteIDs: []string{S1FixtureOwnerASite1ID, S1FixtureOwnerASite2ID},
			Memberships:   []TenantMembership{{TenantID: S1FixtureTenantAID, Status: FactStatusActive}},
			RoleBindings: []RoleBinding{
				{
					TenantID: S1FixtureTenantAID,
					Actions:  []registryauth.Action{registryauth.ActionRegistryRead},
					Capabilities: []identitycontext.Capability{
						identitycontext.CapabilitySessionRevoke,
						identitycontext.CapabilityAuditRead,
						identitycontext.CapabilityIAMAdmin,
						identitycontext.CapabilityAPICredentialManage,
					},
					Status: FactStatusActive,
				},
			},
			SiteBindings: []SiteBinding{
				{TenantID: S1FixtureTenantAID, SiteID: S1FixtureOwnerASite1ID, Actions: []registryauth.Action{registryauth.Action(analyticsmodel.EnergySeriesAction)}, Status: FactStatusActive},
			},
		},
		{
			Principal:     PrincipalRecord{ID: S1FixtureDelegatedID, SubjectIssuer: subjectIssuer, Subject: "fixture-delegated-user", Status: FactStatusActive},
			TenantSiteIDs: []string{S1FixtureOwnerASite1ID, S1FixtureOwnerASite2ID},
			Memberships:   []TenantMembership{{TenantID: S1FixtureTenantAID, Status: FactStatusActive}},
			SiteBindings: []SiteBinding{
				{TenantID: S1FixtureTenantAID, SiteID: S1FixtureOwnerASite1ID, Actions: []registryauth.Action{registryauth.ActionRegistryRead}, Status: FactStatusActive},
			},
		},
		{
			Principal:     PrincipalRecord{ID: S1FixtureDeniedID, SubjectIssuer: subjectIssuer, Subject: "fixture-denied-user", Status: FactStatusActive},
			TenantSiteIDs: []string{S1FixtureOwnerASite1ID, S1FixtureOwnerASite2ID},
			Memberships:   []TenantMembership{{TenantID: S1FixtureTenantAID, Status: FactStatusActive}},
			RoleBindings: []RoleBinding{
				{TenantID: S1FixtureTenantAID, Actions: []registryauth.Action{registryauth.ActionRegistryRead}, Status: FactStatusActive},
			},
			ExplicitDenies: []ExplicitDeny{
				{TenantID: S1FixtureTenantAID, Actions: []registryauth.Action{registryauth.ActionRegistryRead}, Status: FactStatusActive},
			},
		},
		{
			Principal:   PrincipalRecord{ID: S1FixtureNoAccessID, SubjectIssuer: subjectIssuer, Subject: "fixture-no-access-user", Status: FactStatusActive},
			Memberships: []TenantMembership{{TenantID: S1FixtureTenantAID, Status: FactStatusActive}},
		},
		{
			Principal:   PrincipalRecord{ID: S1FixtureRevokedMemberID, SubjectIssuer: subjectIssuer, Subject: "fixture-revoked-user", Status: FactStatusActive},
			Memberships: []TenantMembership{{TenantID: S1FixtureTenantAID, Status: FactStatusRevoked}},
			SiteBindings: []SiteBinding{
				{TenantID: S1FixtureTenantAID, SiteID: S1FixtureOwnerASite1ID, Actions: []registryauth.Action{registryauth.ActionRegistryRead}, Status: FactStatusActive},
			},
		},
	})
}

func NewS2FixtureTelemetryAuthorizationStore(subjectIssuer string) TelemetryAuthorizationStore {
	if subjectIssuer == "" {
		subjectIssuer = "https://issuer.example.test"
	}
	actions := []telemetryauth.Action{telemetryauth.ActionSnapshotRead, telemetryauth.ActionBatchRead, telemetryauth.ActionSubscribe, telemetryauth.ActionHistoryRead}
	registryActions := []registryauth.Action{
		registryauth.Action(telemetryauth.ActionSnapshotRead),
		registryauth.Action(telemetryauth.ActionBatchRead),
		registryauth.Action(telemetryauth.ActionSubscribe),
		registryauth.Action(telemetryauth.ActionHistoryRead),
	}
	return newStaticTelemetryAuthorizationStore(TelemetryAuthorizationFacts{
		PolicyRevision: S2FixturePolicyRevision,
		Principal:      PrincipalRecord{ID: S2FixturePrincipal, SubjectIssuer: subjectIssuer, Subject: "fixture-user", Status: FactStatusActive},
		Memberships:    []TenantMembership{{TenantID: S2FixtureTenantID, Status: FactStatusActive}},
		RoleBindings:   []RoleBinding{{TenantID: S2FixtureTenantID, Actions: registryActions, Effect: BindingEffectAllow, Status: FactStatusActive}},
		SiteBindings:   []SiteBinding{{TenantID: S2FixtureTenantID, SiteID: S2FixtureSite, Actions: registryActions, Effect: BindingEffectAllow, Status: FactStatusActive}},
		Devices: []TelemetryDevice{
			{ID: S2FixtureDevice, TenantID: S2FixtureTenantID, SiteID: S2FixtureSite, Status: FactStatusActive},
			{ID: S2FixtureDeviceTwo, TenantID: S2FixtureTenantID, SiteID: S2FixtureSite, Status: FactStatusActive},
		},
		ScopeBindings: []TelemetryScopeBinding{
			{TenantID: S2FixtureTenantID, SiteID: S2FixtureSite, DeviceID: S2FixtureDevice, Actions: actions, Effect: BindingEffectAllow, Status: FactStatusActive},
			{TenantID: S2FixtureTenantID, SiteID: S2FixtureSite, DeviceID: S2FixtureDeviceTwo, Actions: actions, Effect: BindingEffectAllow, Status: FactStatusActive},
		},
		KeyBindings: []TelemetryKeyBinding{
			{TenantID: S2FixtureTenantID, DeviceID: S2FixtureDevice, Key: "temperature", Actions: actions, Effect: BindingEffectAllow, Status: FactStatusActive},
			{TenantID: S2FixtureTenantID, DeviceID: S2FixtureDevice, Key: "humidity", Actions: actions, Effect: BindingEffectAllow, Status: FactStatusActive},
		},
	})
}

func NewDenyAllAuthorizationStore(policyRevision string) AuthorizationStore {
	if policyRevision == "" {
		policyRevision = "policy-unconfigured"
	}
	return newDenyAllAuthorizationStore(policyRevision)
}
