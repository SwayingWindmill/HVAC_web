package iam_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

func principalFacts(assignments []iam.RoleAssignment, denies []iam.ExplicitDeny) iam.AuthorizationFacts {
	return iam.AuthorizationFacts{
		Found:          true,
		PolicyRevision: iam.S1FixturePolicyRevision,
		Principal:      iam.PrincipalRecord{ID: iam.S1FixtureOwnerAPrincipalID, SubjectIssuer: fixtureSubjectIssuer, Subject: "fixture-user", Status: iam.FactStatusActive},
		TenantSiteIDs:  []string{iam.S1FixtureOwnerASite1ID, iam.S1FixtureOwnerASite2ID},
		Memberships:    []iam.TenantMembership{{TenantID: iam.S1FixtureTenantAID, Status: iam.FactStatusActive}},
		Assignments:    assignments,
		ExplicitDenies: denies,
	}
}

func currentPrincipal(t *testing.T, facts iam.AuthorizationFacts) identitycontext.InternalPrincipalResponse {
	t.Helper()
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AuthorizationStore = fixedAuthorizationStore{facts: facts}
	})
	claims := validIAMClaims(harness.now, "fixture-user", "principal:read")
	claims.TenantID = iam.S1FixtureTenantAID
	claims.Roles = []string{"descriptive-role-only"}
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, iam.CurrentPrincipalPath, nil, claims, harness.gatewaySigner))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response identitycontext.InternalPrincipalResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestIAMPublishesEffectiveCapabilitiesFromRoleAssignments(t *testing.T) {
	response := currentPrincipal(t, principalFacts([]iam.RoleAssignment{
		{TenantID: iam.S1FixtureTenantAID, RoleKey: "viewer", Status: iam.FactStatusActive, Capabilities: []string{
			string(registryauth.ActionRegistryRead), string(telemetryauth.ActionSnapshotRead), string(alarmauth.ActionRead), string(workorderauth.ActionList),
		}},
		{TenantID: iam.S1FixtureTenantAID, SiteID: iam.S1FixtureOwnerASite1ID, RoleKey: "site-manager", Status: iam.FactStatusActive, Capabilities: []string{
			string(alarmauth.ActionAssign), string(workorderauth.ActionStart),
			// A Site assignment never carries a Tenant-scoped capability.
			string(identitycontext.CapabilityIAMAdmin),
		}},
	}, nil))
	if response.Authorization.CapabilitySetVersion != identitycontext.CapabilitySetVersion {
		t.Fatalf("capability set version = %d", response.Authorization.CapabilitySetVersion)
	}
	digest := sha256.Sum256([]byte(iam.S1FixturePolicyRevision))
	if response.Authorization.PolicyRevision != "capability-v7:"+hex.EncodeToString(digest[:]) {
		t.Fatalf("policy revision = %q", response.Authorization.PolicyRevision)
	}
	assertCapabilitiesEqual(t, response.Authorization.Capabilities, []identitycontext.Capability{
		identitycontext.CapabilitySiteList,
		identitycontext.CapabilitySiteRead,
		identitycontext.CapabilityAssetList,
		identitycontext.CapabilityAssetRead,
		identitycontext.CapabilityDeviceList,
		identitycontext.CapabilityDeviceRead,
		identitycontext.CapabilityTelemetrySnapshotRead,
		identitycontext.CapabilityAlarmList,
		identitycontext.CapabilityAlarmRead,
		identitycontext.CapabilityAlarmAssign,
		identitycontext.CapabilityWorkOrderList,
		identitycontext.CapabilityWorkOrderLifecycle,
	})
	if len(response.Principal.Roles) != 2 || response.Principal.Roles[0] != "viewer" || response.Principal.Roles[1] != "site-manager" {
		t.Fatalf("principal roles = %#v", response.Principal.Roles)
	}
}

func TestIAMCapabilityProjectionPreservesExplicitDenyPrecedence(t *testing.T) {
	viewer := iam.RoleAssignment{TenantID: iam.S1FixtureTenantAID, RoleKey: "viewer", Status: iam.FactStatusActive, Capabilities: []string{
		string(telemetryauth.ActionSnapshotRead), string(telemetryauth.ActionSubscribe), string(identitycontext.CapabilityAuditRead),
	}}
	// Denying one Site keeps the capability on the other; denying the Tenant removes it.
	response := currentPrincipal(t, principalFacts([]iam.RoleAssignment{viewer}, []iam.ExplicitDeny{
		{TenantID: iam.S1FixtureTenantAID, SiteID: iam.S1FixtureOwnerASite1ID, Capability: string(telemetryauth.ActionSubscribe)},
		{TenantID: iam.S1FixtureTenantAID, Capability: string(telemetryauth.ActionSnapshotRead)},
		{TenantID: iam.S1FixtureTenantAID, Capability: string(identitycontext.CapabilityAuditRead)},
	}))
	assertCapabilitiesEqual(t, response.Authorization.Capabilities, []identitycontext.Capability{identitycontext.CapabilityTelemetrySubscribe})
}

func TestIAMPublishesExplicitEmptyCapabilities(t *testing.T) {
	harness := newIAMHarness(t)
	claims := validIAMClaims(harness.now, "fixture-no-access-user", "principal:read")
	claims.TenantID = iam.S1FixtureTenantAID
	request := harness.request(t, iam.CurrentPrincipalPath, nil, claims, harness.gatewaySigner)
	recorder := httptest.NewRecorder()

	harness.handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response identitycontext.InternalPrincipalResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Authorization.Capabilities == nil || len(response.Authorization.Capabilities) != 0 {
		t.Fatalf("empty capability set must be an explicit array: %#v", response.Authorization.Capabilities)
	}
}

func TestIAMRejectsInvalidOrUnavailableCapabilityDecisions(t *testing.T) {
	tests := []struct {
		name        string
		resolver    fixedPrincipalCapabilityResolver
		problemCode string
	}{
		{
			name: "duplicate capability",
			resolver: fixedPrincipalCapabilityResolver{authorization: identitycontext.EffectiveAuthorization{
				CapabilitySetVersion: identitycontext.CapabilitySetVersion,
				PolicyRevision:       "registry-read:7",
				Capabilities: []identitycontext.Capability{
					identitycontext.CapabilitySiteRead,
					identitycontext.CapabilitySiteRead,
				},
			}},
			problemCode: "IAM_PRINCIPAL_CAPABILITIES_INVALID",
		},
		{
			name:        "resolver unavailable",
			resolver:    fixedPrincipalCapabilityResolver{err: errors.New("store unavailable")},
			problemCode: "IAM_PRINCIPAL_CAPABILITIES_UNAVAILABLE",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
				config.PrincipalCapabilityResolver = testCase.resolver
			})
			request := harness.request(t, iam.CurrentPrincipalPath, nil, validIAMClaims(harness.now, "fixture-user", "principal:read"), harness.gatewaySigner)
			recorder := httptest.NewRecorder()
			harness.handler.ServeHTTP(recorder, request)
			assertIAMProblem(t, recorder, http.StatusServiceUnavailable, testCase.problemCode)
		})
	}
}

type fixedPrincipalCapabilityResolver struct {
	authorization identitycontext.EffectiveAuthorization
	err           error
}

func (resolver fixedPrincipalCapabilityResolver) ResolvePrincipalCapabilities(context.Context, iam.PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error) {
	return resolver.authorization, resolver.err
}

func assertCapabilitiesEqual(t *testing.T, actual, expected []identitycontext.Capability) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("capabilities = %#v; expected %#v", actual, expected)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("capabilities = %#v; expected %#v", actual, expected)
		}
	}
}

// Site 1 operator access, as the Alarm and Work Order server tests decide against it.
func principalSiteOperatorFacts(policyRevision string) iam.AuthorizationFacts {
	facts := principalFacts([]iam.RoleAssignment{{
		TenantID: iam.S1FixtureTenantAID, SiteID: iam.S1FixtureOwnerASite1ID, RoleKey: "operator", Status: iam.FactStatusActive,
		Capabilities: []string{string(alarmauth.ActionRead), string(alarmauth.ActionAssign), string(workorderauth.ActionList), string(workorderauth.ActionRead), string(workorderauth.ActionCreate), string(workorderauth.ActionAssign), string(workorderauth.ActionStart)},
	}}, nil)
	facts.PolicyRevision = policyRevision
	return facts
}

func principalAlarmFacts() iam.AuthorizationFacts {
	return principalSiteOperatorFacts(principalCapabilityAlarmPolicy)
}

type fixedAlarmStore struct {
	facts iam.AuthorizationFacts
	err   error
}

func (store fixedAlarmStore) LookupAlarmAuthorization(context.Context, iam.AuthorizationLookup) (iam.AuthorizationFacts, error) {
	return store.facts, store.err
}

func principalWorkOrderFacts() iam.WorkOrderAuthorizationFacts {
	return iam.WorkOrderAuthorizationFacts{AuthorizationFacts: principalSiteOperatorFacts(principalCapabilityWorkOrderPolicy)}
}

type fixedWorkOrderStore struct {
	facts iam.WorkOrderAuthorizationFacts
	err   error
}

func (store fixedWorkOrderStore) LookupWorkOrderAuthorization(context.Context, iam.WorkOrderAuthorizationLookup) (iam.WorkOrderAuthorizationFacts, error) {
	return store.facts, store.err
}

const (
	principalCapabilityAlarmPolicy     = "alarm-access:1"
	principalCapabilityWorkOrderPolicy = "work-order-access:1"
)
