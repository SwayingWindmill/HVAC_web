package iam_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

func TestIAMPublishesEffectiveCapabilitiesFromAuthorizationFacts(t *testing.T) {
	harness := newIAMHarness(t)
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
	if response.Authorization.PolicyRevision != iam.S1FixturePolicyRevision {
		t.Fatalf("policy revision = %q", response.Authorization.PolicyRevision)
	}
	assertCapabilitiesEqual(t, response.Authorization.Capabilities, identitycontext.SupportedCapabilities())
	if len(response.Principal.Roles) != 1 || response.Principal.Roles[0] != "descriptive-role-only" {
		t.Fatalf("roles were reinterpreted or rewritten: %#v", response.Principal.Roles)
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
	if response.Authorization.PolicyRevision != iam.S1FixturePolicyRevision {
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
