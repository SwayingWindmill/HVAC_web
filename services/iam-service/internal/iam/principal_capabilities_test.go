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
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

const (
	principalCapabilityTelemetryPolicy = "telemetry-access:2"
	principalCapabilityAlarmPolicy     = "alarm-access:1"
	principalCapabilityWorkOrderPolicy = "work-order-access:1"
)

func TestIAMPublishesEffectiveCapabilitiesFromAuthorizationFacts(t *testing.T) {
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.TelemetryAuthorizationStore = fixedTelemetryStore{facts: principalTelemetryFacts(nil)}
		config.AlarmAuthorizationStore = fixedAlarmStore{facts: principalAlarmFacts()}
		config.WorkOrderAuthorizationStore = fixedWorkOrderStore{facts: principalWorkOrderFacts()}
	})
	claims := validIAMClaims(harness.now, "fixture-user", "principal:read")
	claims.ActingOrganizationID = iam.S1FixtureOwnerAOrganizationID
	claims.Roles = []string{"descriptive-role-only"}
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
	if response.Authorization.CapabilitySetVersion != identitycontext.CapabilitySetVersion {
		t.Fatalf("capability set version = %d", response.Authorization.CapabilitySetVersion)
	}
	if response.Authorization.PolicyRevision != capabilityPolicyRevision(iam.S1FixturePolicyRevision, principalCapabilityTelemetryPolicy, principalCapabilityAlarmPolicy, principalCapabilityWorkOrderPolicy) {
		t.Fatalf("policy revision = %q", response.Authorization.PolicyRevision)
	}
	assertCapabilitiesEqual(t, response.Authorization.Capabilities, identitycontext.SupportedCapabilities())
	if len(response.Principal.Roles) != 1 || response.Principal.Roles[0] != "descriptive-role-only" {
		t.Fatalf("roles were reinterpreted or rewritten: %#v", response.Principal.Roles)
	}
}

func TestIAMTelemetryCapabilityProjectionPreservesExplicitDenyPrecedence(t *testing.T) {
	denies := []iam.ExplicitDeny{{
		ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID,
		OrganizationID:       iam.S1FixtureOwnerAOrganizationID,
		SiteID:               iam.S1FixtureOwnerASite1ID,
		Actions:              []registryauth.Action{registryauth.Action(telemetryauth.ActionSubscribe)},
		Status:               iam.FactStatusActive,
	}}
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.TelemetryAuthorizationStore = fixedTelemetryStore{facts: principalTelemetryFacts(denies)}
	})
	claims := validIAMClaims(harness.now, "fixture-user", "principal:read")
	claims.ActingOrganizationID = iam.S1FixtureOwnerAOrganizationID
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, iam.CurrentPrincipalPath, nil, claims, harness.gatewaySigner))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response identitycontext.InternalPrincipalResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	for _, capability := range response.Authorization.Capabilities {
		if capability == identitycontext.CapabilityTelemetrySubscribe {
			t.Fatal("explicit telemetry subscribe deny was projected as allowed")
		}
	}
	for _, expected := range []identitycontext.Capability{
		identitycontext.CapabilityTelemetrySnapshotRead,
		identitycontext.CapabilityTelemetryBatchRead,
		identitycontext.CapabilityTelemetryHistoryRead,
	} {
		if !containsCapability(response.Authorization.Capabilities, expected) {
			t.Fatalf("capability %q was unexpectedly removed", expected)
		}
	}
}

func TestIAMPublishesExplicitEmptyCapabilities(t *testing.T) {
	harness := newIAMHarness(t)
	claims := validIAMClaims(harness.now, "fixture-no-access-user", "principal:read")
	claims.ActingOrganizationID = iam.S1FixtureActingOrganizationID
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
	if response.Authorization.PolicyRevision != capabilityPolicyRevision(iam.S1FixturePolicyRevision, "telemetry-policy-unconfigured", "alarm-policy-unconfigured", "work-order-policy-unconfigured") {
		t.Fatalf("policy revision = %q", response.Authorization.PolicyRevision)
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

func principalTelemetryFacts(denies []iam.ExplicitDeny) iam.TelemetryAuthorizationFacts {
	actions := []telemetryauth.Action{
		telemetryauth.ActionSnapshotRead,
		telemetryauth.ActionBatchRead,
		telemetryauth.ActionSubscribe,
		telemetryauth.ActionHistoryRead,
	}
	registryActions := make([]registryauth.Action, len(actions))
	for index, action := range actions {
		registryActions[index] = registryauth.Action(action)
	}
	return iam.TelemetryAuthorizationFacts{
		Found:          true,
		PolicyRevision: principalCapabilityTelemetryPolicy,
		Principal: iam.PrincipalRecord{
			ID:            iam.S1FixtureOwnerAPrincipalID,
			SubjectIssuer: fixtureSubjectIssuer,
			Subject:       "fixture-user",
			Status:        iam.FactStatusActive,
		},
		Memberships: []iam.OrganizationMembership{{OrganizationID: iam.S1FixtureOwnerAOrganizationID, Status: iam.FactStatusActive}},
		RoleBindings: []iam.RoleBinding{{
			OrganizationID: iam.S1FixtureOwnerAOrganizationID,
			Actions:        registryActions,
			Effect:         iam.BindingEffectAllow,
			Status:         iam.FactStatusActive,
		}},
		ExplicitDenies: denies,
		ScopeBindings: []iam.TelemetryScopeBinding{{
			ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID,
			OwningOrganizationID: iam.S1FixtureOwnerAOrganizationID,
			SiteID:               iam.S1FixtureOwnerASite1ID,
			DeviceID:             iam.S2FixtureDevice,
			Actions:              actions,
			Effect:               iam.BindingEffectAllow,
			Status:               iam.FactStatusActive,
		}},
	}
}

func principalAlarmFacts() iam.AlarmAuthorizationFacts {
	return iam.AlarmAuthorizationFacts{
		Found:          true,
		PolicyRevision: principalCapabilityAlarmPolicy,
		Principal: iam.PrincipalRecord{
			ID:            iam.S1FixtureOwnerAPrincipalID,
			SubjectIssuer: fixtureSubjectIssuer,
			Subject:       "fixture-user",
			Status:        iam.FactStatusActive,
		},
		Memberships: []iam.OrganizationMembership{{OrganizationID: iam.S1FixtureOwnerAOrganizationID, Status: iam.FactStatusActive}},
		Permissions: []iam.AlarmPermission{
			{OrganizationID: iam.S1FixtureOwnerAOrganizationID, SiteID: iam.S1FixtureOwnerASite1ID, Action: alarmauth.ActionList, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive},
			{OrganizationID: iam.S1FixtureOwnerAOrganizationID, SiteID: iam.S1FixtureOwnerASite1ID, Action: alarmauth.ActionRead, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive},
		},
	}
}

type fixedAlarmStore struct {
	facts iam.AlarmAuthorizationFacts
	err   error
}

func (store fixedAlarmStore) LookupAlarmAuthorization(context.Context, iam.AuthorizationLookup) (iam.AlarmAuthorizationFacts, error) {
	return store.facts, store.err
}

func principalWorkOrderFacts() iam.WorkOrderAuthorizationFacts {
	return iam.WorkOrderAuthorizationFacts{
		Found:          true,
		PolicyRevision: principalCapabilityWorkOrderPolicy,
		Principal: iam.PrincipalRecord{
			ID:            iam.S1FixtureOwnerAPrincipalID,
			SubjectIssuer: fixtureSubjectIssuer,
			Subject:       "fixture-user",
			Status:        iam.FactStatusActive,
		},
		Memberships: []iam.OrganizationMembership{{OrganizationID: iam.S1FixtureOwnerAOrganizationID, Status: iam.FactStatusActive}},
		Permissions: []iam.WorkOrderPermission{
			{OrganizationID: iam.S1FixtureOwnerAOrganizationID, SiteID: iam.S1FixtureOwnerASite1ID, Action: workorderauth.ActionList, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive},
			{OrganizationID: iam.S1FixtureOwnerAOrganizationID, SiteID: iam.S1FixtureOwnerASite1ID, Action: workorderauth.ActionRead, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive},
		},
	}
}

type fixedWorkOrderStore struct {
	facts iam.WorkOrderAuthorizationFacts
	err   error
}

func (store fixedWorkOrderStore) LookupWorkOrderAuthorization(context.Context, iam.AuthorizationLookup) (iam.WorkOrderAuthorizationFacts, error) {
	return store.facts, store.err
}

func capabilityPolicyRevision(registryRevision, telemetryRevision, alarmRevision, workOrderRevision string) string {
	digest := sha256.Sum256([]byte(registryRevision + "\x00" + telemetryRevision + "\x00" + alarmRevision + "\x00" + workOrderRevision))
	return "capability-v4:" + hex.EncodeToString(digest[:])
}

func containsCapability(capabilities []identitycontext.Capability, expected identitycontext.Capability) bool {
	for _, capability := range capabilities {
		if capability == expected {
			return true
		}
	}
	return false
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
