package iam_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/testpki"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

const (
	fixtureSessionID            = "fixture-session"
	fixtureInboundID            = "fixture-inbound-identifier"
	fixtureRegistryID           = "fixture-registry-identifier"
	fixtureSubjectIssuer        = "https://issuer.example.test"
	fixtureCoreAudience         = "platform-core-service"
	fixtureOperationsPresenter  = "spiffe://hvac.local/operations-agent-service"
	registryAuthorize           = "registry:authorize"
	analyticsAuthorizeAction    = "analytics:authorize"
	maximumTestDecisionBodySize = 70 << 10
)

func TestIAMAcceptsOnlyVerifiedGatewayDelegation(t *testing.T) {
	harness := newIAMHarness(t)
	request := harness.request(t, iam.CurrentPrincipalPath, nil, validIAMClaims(harness.now, "fixture-user", "principal:read"), harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var principal identitycontext.InternalPrincipalResponse
	if err := json.NewDecoder(recorder.Body).Decode(&principal); err != nil {
		t.Fatal(err)
	}
	if principal.Principal.Subject != "fixture-user" || principal.Context.ExecutingServicePrincipal.SPIFFEID != harness.gatewaySPIFFEID {
		t.Fatalf("unexpected actor chain: %#v", principal)
	}
}

func TestIAMAuthorizesEnergyAnalyticsForExactSite(t *testing.T) {
	harness := newIAMHarness(t)
	claims := validIAMClaims(harness.now, "fixture-user", analyticsAuthorizeAction)
	claims.ActingOrganizationID = iam.S1FixtureOwnerAOrganizationID
	body, err := json.Marshal(analyticsmodel.AuthorizationDecisionRequest{
		ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID,
		SiteID:               iam.S1FixtureOwnerASite1ID,
		Action:               analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, iam.AnalyticsDecisionPath, strings.NewReader(string(body)), claims, harness.gatewaySigner))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response analyticsmodel.AuthorizationDecisionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.Decision.Allowed || response.Decision.SiteID != iam.S1FixtureOwnerASite1ID || response.Decision.Action != analyticsmodel.EnergySeriesAction {
		t.Fatalf("decision=%#v", response.Decision)
	}

	mismatchBody, err := json.Marshal(analyticsmodel.AuthorizationDecisionRequest{
		ActingOrganizationID: iam.S1FixtureActingOrganizationID,
		SiteID:               iam.S1FixtureOwnerASite1ID,
		Action:               analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		t.Fatal(err)
	}
	mismatch := httptest.NewRecorder()
	harness.handler.ServeHTTP(mismatch, harness.request(t, iam.AnalyticsDecisionPath, strings.NewReader(string(mismatchBody)), claims, harness.gatewaySigner))
	assertIAMProblem(t, mismatch, http.StatusForbidden, "IAM_ANALYTICS_CONTEXT_MISMATCH")
}

func TestIAMRejectsUnverifiedWorkloadAndForgedHeaders(t *testing.T) {
	harness := newIAMHarness(t)

	withoutTLS := httptest.NewRequest(http.MethodPost, iam.CurrentPrincipalPath, nil)
	withoutTLS.Header.Set("X-Delegation-Grant", "not-relevant")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, withoutTLS)
	assertIAMProblem(t, recorder, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID")

	for _, header := range []string{"X-Admin", "X-Organization-ID", "X-Organization-Scope", "X-Site-Scope", "X-Principal-Subject", "X-Role", "X-Scope"} {
		t.Run(header, func(t *testing.T) {
			forged := harness.request(t, iam.CurrentPrincipalPath, nil, validIAMClaims(harness.now, "fixture-user", "principal:read"), harness.gatewaySigner)
			forged.Header.Set(header, "forged")
			recorder := httptest.NewRecorder()
			harness.handler.ServeHTTP(recorder, forged)
			assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER")
		})
	}
}

func TestIAMRejectsExpandedForwardedAndInvalidDelegation(t *testing.T) {
	harness := newIAMHarness(t)
	cases := []struct {
		name   string
		mutate func(*identitycontext.DelegationClaims)
	}{
		{name: "wrong audience", mutate: func(claims *identitycontext.DelegationClaims) { claims.Audience = "audit-service" }},
		{name: "expanded actions", mutate: func(claims *identitycontext.DelegationClaims) {
			claims.Actions = []string{"principal:read", "session:revoke"}
		}},
		{name: "expanded scopes", mutate: func(claims *identitycontext.DelegationClaims) {
			claims.Scopes = []string{"session:" + fixtureSessionID, "organization:*"}
		}},
		{name: "forwarded by IAM", mutate: func(claims *identitycontext.DelegationClaims) {
			claims.ExecutingService = "spiffe://hvac.local/iam-service"
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			claims := validIAMClaims(harness.now, "fixture-user", "principal:read")
			testCase.mutate(&claims)
			recorder := httptest.NewRecorder()
			harness.handler.ServeHTTP(recorder, harness.request(t, iam.CurrentPrincipalPath, nil, claims, harness.gatewaySigner))
			assertIAMProblem(t, recorder, http.StatusForbidden, "IAM_DELEGATION_REJECTED")
		})
	}

	otherSigner, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, iam.CurrentPrincipalPath, nil, validIAMClaims(harness.now, "fixture-user", "principal:read"), otherSigner))
	assertIAMProblem(t, recorder, http.StatusUnauthorized, "IAM_DELEGATION_INVALID")
}

func TestIAMIssuesTenantRoleAsExactSiteRegistryGrant(t *testing.T) {
	harness := newIAMHarness(t)
	response := harness.registryDecision(t, "fixture-user", iam.S1FixtureOwnerAOrganizationID, registryauth.ActionSiteRead)
	if !response.Decision.Allowed || response.Decision.ReasonCode != registryauth.ReasonAllowTenantRole {
		t.Fatalf("unexpected Tenant decision: %#v", response.Decision)
	}
	assertStringsEqual(t, response.Decision.AllowedSiteIDs, []string{iam.S1FixtureOwnerASite1ID, iam.S1FixtureOwnerASite2ID})
	if response.DelegationGrant == "" {
		t.Fatal("allowed decision did not include a Core delegation")
	}

	claims := harness.verifyRegistryGrant(t, response.DelegationGrant, registryauth.ActionSiteRead)
	if claims.PrincipalID != iam.S1FixtureOwnerAPrincipalID || claims.ParentTokenID != fixtureInboundID || claims.TokenID != fixtureRegistryID {
		t.Fatalf("unexpected registry actor chain: %#v", claims)
	}
	if !registryauth.ScopeAllows(claims, iam.S1FixtureOwnerASite1ID) {
		t.Fatal("Tenant grant did not allow an exact Site")
	}
	if registryauth.ScopeAllows(claims, iam.S1FixtureOwnerBSite1ID) {
		t.Fatal("Tenant grant expanded outside its exact Site set")
	}
}

func TestIAMIssuesRegistryGrantForAllowedDelegatedPresenter(t *testing.T) {
	harness := newIAMHarness(t)
	claims := validIAMClaims(harness.now, "fixture-user", registryAuthorize)
	payload, err := json.Marshal(registryauth.DecisionRequest{
		ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID,
		Action:               registryauth.ActionSiteRead,
		GrantPresenter:       fixtureOperationsPresenter,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := harness.request(t, iam.RegistryReadDecisionPath, strings.NewReader(string(payload)), claims, harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response registryauth.DecisionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	grant, err := registryauth.VerifyGrant(harness.iamSigner.Public(), response.DelegationGrant)
	if err != nil {
		t.Fatal(err)
	}
	if grant.Presenter != fixtureOperationsPresenter {
		t.Fatalf("grant presenter = %q", grant.Presenter)
	}
	if err := registryauth.ValidateGrant(grant, registryauth.GrantValidation{
		Now:                   harness.now,
		Issuer:                harness.iamSPIFFEID,
		Presenter:             fixtureOperationsPresenter,
		Audience:              fixtureCoreAudience,
		Action:                registryauth.ActionSiteRead,
		CurrentPolicyRevision: iam.S1FixturePolicyRevision,
		IsRevoked:             func(string) (bool, error) { return false, nil },
	}); err != nil {
		t.Fatalf("delegated presenter grant invalid: %v", err)
	}

	payload, err = json.Marshal(registryauth.DecisionRequest{
		ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID,
		Action:               registryauth.ActionSiteRead,
		GrantPresenter:       "spiffe://hvac.local/untrusted-service",
	})
	if err != nil {
		t.Fatal(err)
	}
	request = harness.request(t, iam.RegistryReadDecisionPath, strings.NewReader(string(payload)), claims, harness.gatewaySigner)
	recorder = httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusForbidden, "IAM_REGISTRY_GRANT_PRESENTER_REJECTED")
}

func TestIAMCrossOrganizationBindingIsSiteOnly(t *testing.T) {
	harness := newIAMHarness(t)
	response := harness.registryDecision(t, "fixture-delegated-user", iam.S1FixtureActingOrganizationID, registryauth.ActionDeviceRead)
	if !response.Decision.Allowed || response.Decision.ReasonCode != registryauth.ReasonAllowSiteBinding {
		t.Fatalf("unexpected delegated decision: %#v", response.Decision)
	}
	assertStringsEqual(t, response.Decision.AllowedSiteIDs, []string{iam.S1FixtureOwnerASite1ID})
	claims := harness.verifyRegistryGrant(t, response.DelegationGrant, registryauth.ActionDeviceRead)
	if !registryauth.ScopeAllows(claims, iam.S1FixtureOwnerASite1ID) {
		t.Fatal("delegated Site was rejected")
	}
	if registryauth.ScopeAllows(claims, iam.S1FixtureOwnerASite2ID) {
		t.Fatal("delegated grant exposed a sibling Site")
	}
	if registryauth.ScopeAllows(claims, "") {
		t.Fatal("delegated grant accepted an empty Site scope")
	}
}

func TestIAMDeviceBindingListRequiresBothConstituentReadActions(t *testing.T) {
	validFrom := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	facts := func(actions []registryauth.Action, denies []iam.ExplicitDeny) iam.AuthorizationFacts {
		return iam.AuthorizationFacts{
			Found:          true,
			PolicyRevision: iam.S1FixturePolicyRevision,
			TenantSiteIDs:  []string{iam.S1FixtureOwnerASite1ID, iam.S1FixtureOwnerASite2ID},
			Principal: iam.PrincipalRecord{
				ID:            iam.S1FixtureOwnerAPrincipalID,
				SubjectIssuer: fixtureSubjectIssuer,
				Subject:       "fixture-user",
				Status:        iam.FactStatusActive,
			},
			Memberships: []iam.OrganizationMembership{{
				TenantID:       iam.S1FixtureTenantAID,
				OrganizationID: iam.S1FixtureOwnerAOrganizationID,
				Status:         iam.FactStatusActive,
				ValidFrom:      validFrom,
			}},
			RoleBindings: []iam.RoleBinding{{
				TenantID:       iam.S1FixtureTenantAID,
				OrganizationID: iam.S1FixtureOwnerAOrganizationID,
				Actions:        actions,
				Effect:         iam.BindingEffectAllow,
				Status:         iam.FactStatusActive,
				ValidFrom:      validFrom,
			}},
			ExplicitDenies: denies,
		}
	}

	t.Run("actions may be aggregated across effective role bindings", func(t *testing.T) {
		splitFacts := facts(nil, nil)
		splitFacts.RoleBindings = []iam.RoleBinding{
			{TenantID: iam.S1FixtureTenantAID, OrganizationID: iam.S1FixtureOwnerAOrganizationID, Actions: []registryauth.Action{registryauth.ActionEquipmentList}, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive, ValidFrom: validFrom},
			{TenantID: iam.S1FixtureTenantAID, OrganizationID: iam.S1FixtureOwnerAOrganizationID, Actions: []registryauth.Action{registryauth.ActionDeviceList}, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive, ValidFrom: validFrom},
		}
		harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
			config.AuthorizationStore = fixedAuthorizationStore{facts: splitFacts}
		})
		response := harness.registryDecision(t, "fixture-user", iam.S1FixtureOwnerAOrganizationID, registryauth.ActionDeviceBindingList)
		if !response.Decision.Allowed || response.Decision.ReasonCode != registryauth.ReasonAllowTenantRole {
			t.Fatalf("unexpected DeviceBinding decision: %#v", response.Decision)
		}
		harness.verifyRegistryGrant(t, response.DelegationGrant, registryauth.ActionDeviceBindingList)
	})

	t.Run("one constituent action is insufficient", func(t *testing.T) {
		harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
			config.AuthorizationStore = fixedAuthorizationStore{facts: facts([]registryauth.Action{registryauth.ActionDeviceList}, nil)}
		})
		response := harness.registryDecision(t, "fixture-user", iam.S1FixtureOwnerAOrganizationID, registryauth.ActionDeviceBindingList)
		if response.Decision.Allowed || response.DelegationGrant != "" || response.Decision.ReasonCode != registryauth.ReasonDenyActionNotGranted {
			t.Fatalf("partial DeviceBinding permission was accepted: %#v", response)
		}
	})

	t.Run("a deny on either constituent action denies relationships", func(t *testing.T) {
		denies := []iam.ExplicitDeny{{
			TenantID:             iam.S1FixtureTenantAID,
			ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID,
			OrganizationID:       iam.S1FixtureOwnerAOrganizationID,
			Actions:              []registryauth.Action{registryauth.ActionEquipmentList},
			Status:               iam.FactStatusActive,
			ValidFrom:            validFrom,
		}}
		harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
			config.AuthorizationStore = fixedAuthorizationStore{facts: facts([]registryauth.Action{registryauth.ActionEquipmentList, registryauth.ActionDeviceList}, denies)}
		})
		response := harness.registryDecision(t, "fixture-user", iam.S1FixtureOwnerAOrganizationID, registryauth.ActionDeviceBindingList)
		if response.Decision.Allowed || response.DelegationGrant != "" || response.Decision.ReasonCode != registryauth.ReasonDenyExplicit {
			t.Fatalf("constituent deny did not fail closed: %#v", response)
		}
	})
}

func TestIAMDenyMatrixDoesNotIssueDelegations(t *testing.T) {
	harness := newIAMHarness(t)
	cases := []struct {
		name       string
		subject    string
		actingOrg  string
		reasonCode registryauth.ReasonCode
	}{
		{name: "explicit deny", subject: "fixture-denied-user", actingOrg: iam.S1FixtureOwnerAOrganizationID, reasonCode: registryauth.ReasonDenyExplicit},
		{name: "no action binding", subject: "fixture-no-access-user", actingOrg: iam.S1FixtureActingOrganizationID, reasonCode: registryauth.ReasonDenyActionNotGranted},
		{name: "revoked membership", subject: "fixture-revoked-user", actingOrg: iam.S1FixtureActingOrganizationID, reasonCode: registryauth.ReasonDenyMembershipRevoked},
		{name: "unmapped subject", subject: "fixture-unmapped-user", actingOrg: iam.S1FixtureOwnerAOrganizationID, reasonCode: registryauth.ReasonDenyPrincipalNotFound},
		{name: "body cannot select unowned organization", subject: "fixture-user", actingOrg: iam.S1FixtureOwnerBOrganizationID, reasonCode: registryauth.ReasonDenyMembershipRequired},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := harness.registryDecision(t, testCase.subject, testCase.actingOrg, registryauth.ActionSiteRead)
			if response.Decision.Allowed || response.DelegationGrant != "" || response.Decision.ReasonCode != testCase.reasonCode {
				t.Fatalf("unexpected deny decision: %#v", response)
			}
		})
	}
}

func TestIAMExplicitSiteDenyDoesNotExpandToOwningOrganization(t *testing.T) {
	validFrom := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AuthorizationStore = fixedAuthorizationStore{facts: iam.AuthorizationFacts{
			Found:          true,
			PolicyRevision: iam.S1FixturePolicyRevision,
			Principal: iam.PrincipalRecord{
				ID:            iam.S1FixtureOwnerAPrincipalID,
				SubjectIssuer: fixtureSubjectIssuer,
				Subject:       "fixture-user",
				Status:        iam.FactStatusActive,
			},
			Memberships: []iam.OrganizationMembership{{
				TenantID:       iam.S1FixtureTenantAID,
				OrganizationID: iam.S1FixtureActingOrganizationID,
				Status:         iam.FactStatusActive,
				ValidFrom:      validFrom,
			}},
			SiteBindings: []iam.SiteBinding{
				{TenantID: iam.S1FixtureTenantAID, ActingOrganizationID: iam.S1FixtureActingOrganizationID, OwningOrganizationID: iam.S1FixtureOwnerAOrganizationID, SiteID: iam.S1FixtureOwnerASite1ID, Actions: []registryauth.Action{registryauth.ActionSiteRead}, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive, ValidFrom: validFrom},
				{TenantID: iam.S1FixtureTenantAID, ActingOrganizationID: iam.S1FixtureActingOrganizationID, OwningOrganizationID: iam.S1FixtureOwnerAOrganizationID, SiteID: iam.S1FixtureOwnerASite2ID, Actions: []registryauth.Action{registryauth.ActionSiteRead}, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive, ValidFrom: validFrom},
			},
			ExplicitDenies: []iam.ExplicitDeny{{
				TenantID:             iam.S1FixtureTenantAID,
				ActingOrganizationID: iam.S1FixtureActingOrganizationID,
				OrganizationID:       iam.S1FixtureOwnerAOrganizationID,
				SiteID:               iam.S1FixtureOwnerASite1ID,
				Actions:              []registryauth.Action{registryauth.ActionSiteRead},
				Status:               iam.FactStatusActive,
				ValidFrom:            validFrom,
			}},
		}}
	})

	response := harness.registryDecision(t, "fixture-user", iam.S1FixtureActingOrganizationID, registryauth.ActionSiteRead)
	if !response.Decision.Allowed || response.Decision.ReasonCode != registryauth.ReasonAllowSiteBinding {
		t.Fatalf("site-specific deny expanded to the owning Organization: %#v", response)
	}
	assertStringsEqual(t, response.Decision.AllowedSiteIDs, []string{iam.S1FixtureOwnerASite2ID})
	assertStringsEqual(t, response.Decision.DeniedSiteIDs, []string{iam.S1FixtureOwnerASite1ID})
	claims := harness.verifyRegistryGrant(t, response.DelegationGrant, registryauth.ActionSiteRead)
	if registryauth.ScopeAllows(claims, iam.S1FixtureOwnerASite1ID) {
		t.Fatal("site-specific deny allowed the denied Site")
	}
	if !registryauth.ScopeAllows(claims, iam.S1FixtureOwnerASite2ID) {
		t.Fatal("site-specific deny removed the sibling Site")
	}
}

func TestIAMSiteBindingDenyOverridesAllow(t *testing.T) {
	membership := iam.OrganizationMembership{
		TenantID:       iam.S1FixtureTenantAID,
		OrganizationID: iam.S1FixtureActingOrganizationID,
		Status:         iam.FactStatusActive,
		ValidFrom:      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}
	principal := iam.PrincipalRecord{
		ID:            iam.S1FixtureOwnerAPrincipalID,
		SubjectIssuer: fixtureSubjectIssuer,
		Subject:       "fixture-user",
		Status:        iam.FactStatusActive,
	}
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AuthorizationStore = fixedAuthorizationStore{facts: iam.AuthorizationFacts{
			Found:          true,
			PolicyRevision: "registry-read:7",
			Principal:      principal,
			Memberships:    []iam.OrganizationMembership{membership},
			SiteBindings: []iam.SiteBinding{
				{TenantID: iam.S1FixtureTenantAID, ActingOrganizationID: iam.S1FixtureActingOrganizationID, OwningOrganizationID: iam.S1FixtureOwnerAOrganizationID, SiteID: iam.S1FixtureOwnerASite1ID, Actions: []registryauth.Action{registryauth.ActionSiteRead}, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive, ValidFrom: membership.ValidFrom},
				{TenantID: iam.S1FixtureTenantAID, ActingOrganizationID: iam.S1FixtureActingOrganizationID, OwningOrganizationID: iam.S1FixtureOwnerAOrganizationID, SiteID: iam.S1FixtureOwnerASite1ID, Actions: []registryauth.Action{registryauth.ActionSiteRead}, Effect: iam.BindingEffectDeny, Status: iam.FactStatusActive, ValidFrom: membership.ValidFrom},
			},
		}}
	})
	response := harness.registryDecision(t, "fixture-user", iam.S1FixtureActingOrganizationID, registryauth.ActionSiteRead)
	if response.Decision.Allowed || response.DelegationGrant != "" || response.Decision.ReasonCode != registryauth.ReasonDenyExplicit {
		t.Fatalf("Site binding deny did not override allow: %#v", response)
	}
	assertStringsEqual(t, response.Decision.DeniedSiteIDs, []string{iam.S1FixtureOwnerASite1ID})
}

func TestIAMRegistryDecisionRejectsBodyExpansionAndWrongInboundAction(t *testing.T) {
	harness := newIAMHarness(t)
	claims := validIAMClaims(harness.now, "fixture-user", registryAuthorize)
	request := harness.request(t, iam.RegistryReadDecisionPath, strings.NewReader(`{"actingOrganizationId":"`+iam.S1FixtureOwnerAOrganizationID+`","action":"site.read","roles":["platform-admin"]}`), claims, harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_REGISTRY_DECISION_REQUEST_INVALID")

	request = harness.request(t, iam.RegistryReadDecisionPath, registryDecisionBody("not-a-uuid", registryauth.ActionSiteRead), claims, harness.gatewaySigner)
	recorder = httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_REGISTRY_DECISION_REQUEST_INVALID")

	wrongAction := validIAMClaims(harness.now, "fixture-user", "principal:read")
	request = harness.request(t, iam.RegistryReadDecisionPath, registryDecisionBody(iam.S1FixtureOwnerAOrganizationID, registryauth.ActionSiteRead), wrongAction, harness.gatewaySigner)
	recorder = httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusForbidden, "IAM_DELEGATION_REJECTED")
}

func TestIAMAllowedDecisionRequiresSignerAndAuditEvidence(t *testing.T) {
	sink := &capturingRegistryAuditSink{}
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.RegistryGrantSigner = nil
		config.RegistryAuditSink = sink
	})
	claims := validIAMClaims(harness.now, "fixture-user", registryAuthorize)
	request := harness.request(t, iam.RegistryReadDecisionPath, registryDecisionBody(iam.S1FixtureOwnerAOrganizationID, registryauth.ActionSiteRead), claims, harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusServiceUnavailable, "IAM_REGISTRY_GRANT_SIGNER_UNAVAILABLE")
	if len(sink.events) != 1 || sink.events[0].GrantSigned || sink.events[0].DeliveryCode != "GRANT_SIGNER_UNAVAILABLE" {
		t.Fatalf("unexpected signer failure audit: %#v", sink.events)
	}
}

func TestIAMAuditFailurePreventsGrantDelivery(t *testing.T) {
	sink := &capturingRegistryAuditSink{err: errors.New("audit unavailable")}
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.RegistryAuditSink = sink
	})
	claims := validIAMClaims(harness.now, "fixture-user", registryAuthorize)
	request := harness.request(t, iam.RegistryReadDecisionPath, registryDecisionBody(iam.S1FixtureOwnerAOrganizationID, registryauth.ActionSiteRead), claims, harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE")
	if len(sink.events) != 1 || !sink.events[0].GrantSigned || sink.events[0].DeliveryCode != "GRANT_SIGNED" {
		t.Fatalf("unexpected audit failure event: %#v", sink.events)
	}
}

func TestIAMDenyDecisionRecordsPolicyEvidenceWithoutSigningGrant(t *testing.T) {
	sink := &capturingRegistryAuditSink{}
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.RegistryAuditSink = sink
	})
	response := harness.registryDecision(t, "fixture-denied-user", iam.S1FixtureOwnerAOrganizationID, registryauth.ActionSiteRead)
	if response.Decision.Allowed || response.DelegationGrant != "" {
		t.Fatalf("unexpected deny response: %#v", response)
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(sink.events))
	}
	event := sink.events[0]
	if event.Allowed || event.GrantSigned || event.DeliveryCode != "DECISION_DENIED" || event.ReasonCode != registryauth.ReasonDenyExplicit {
		t.Fatalf("unexpected deny audit event: %#v", event)
	}
}

func TestIAMRegistryDecisionRejectsOversizedBody(t *testing.T) {
	harness := newIAMHarness(t)
	claims := validIAMClaims(harness.now, "fixture-user", registryAuthorize)
	body := strings.NewReader(`{"actingOrganizationId":"` + strings.Repeat("a", maximumTestDecisionBodySize) + `","action":"site.read"}`)
	request := harness.request(t, iam.RegistryReadDecisionPath, body, claims, harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_REGISTRY_DECISION_REQUEST_INVALID")
}

func TestIAMAuthorizationLogsExcludeDelegationMaterial(t *testing.T) {
	harness := newIAMHarness(t)
	claims := validIAMClaims(harness.now, "fixture-user", registryAuthorize)
	request := harness.request(t, iam.RegistryReadDecisionPath, registryDecisionBody(iam.S1FixtureOwnerAOrganizationID, registryauth.ActionSiteRead), claims, harness.gatewaySigner)
	incoming := request.Header.Get("X-Delegation-Grant")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response registryauth.DecisionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	logs := harness.logs.String()
	for _, forbidden := range []string{incoming, response.DelegationGrant, "X-Delegation-Grant", fixtureSubjectIssuer, "fixture-user"} {
		if forbidden != "" && strings.Contains(logs, forbidden) {
			t.Fatalf("authorization logs leaked %q: %s", forbidden, logs)
		}
	}
	for _, required := range []string{"iam_registry_authorization_decision", string(registryauth.ReasonAllowTenantRole), iam.S1FixtureOwnerAPrincipalID, iam.S1FixtureOwnerAOrganizationID} {
		if !strings.Contains(logs, required) {
			t.Fatalf("authorization logs omitted %q: %s", required, logs)
		}
	}
}

type iamHarness struct {
	handler         http.Handler
	now             time.Time
	gatewaySPIFFEID string
	iamSPIFFEID     string
	gatewayCert     *x509.Certificate
	gatewaySigner   crypto.Signer
	iamSigner       crypto.Signer
	logs            *bytes.Buffer
}

func newIAMHarness(t *testing.T) iamHarness {
	return newIAMHarnessWithConfig(t, nil)
}

func newIAMHarnessWithConfig(t *testing.T, mutate func(*iam.Config)) iamHarness {
	t.Helper()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	bundle, err := testpki.Generate("spiffe://hvac.local/iam-service", "spiffe://hvac.local/platform-gateway", now)
	if err != nil {
		t.Fatal(err)
	}
	gatewayPair, err := tls.X509KeyPair(bundle.ClientCertPEM, bundle.ClientKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	gatewayCertificate, err := x509.ParseCertificate(gatewayPair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	gatewaySigner, ok := gatewayPair.PrivateKey.(crypto.Signer)
	if !ok {
		t.Fatal("test Gateway key is not a signer")
	}
	iamPair, err := tls.X509KeyPair(bundle.ServerCertPEM, bundle.ServerKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	iamSigner, ok := iamPair.PrivateKey.(crypto.Signer)
	if !ok {
		t.Fatal("test IAM key is not a signer")
	}
	var logs bytes.Buffer
	config := iam.Config{
		AllowedWorkloadSPIFFE:          bundle.ClientSPIFFEID,
		Audience:                       "iam-service",
		Now:                            func() time.Time { return now },
		Logger:                         slog.New(slog.NewJSONHandler(&logs, nil)),
		AuthorizationStore:             iam.NewS1FixtureAuthorizationStore(fixtureSubjectIssuer),
		RegistryGrantSigner:            iamSigner,
		RegistryGrantIssuer:            bundle.ServerSPIFFEID,
		RegistryGrantAudience:          fixtureCoreAudience,
		AllowedRegistryGrantPresenters: []string{fixtureOperationsPresenter},
		NewRegistryGrantID:             func() string { return fixtureRegistryID },
	}
	if mutate != nil {
		mutate(&config)
	}
	return iamHarness{
		handler:         iam.NewHandler(config),
		now:             now,
		gatewaySPIFFEID: bundle.ClientSPIFFEID,
		iamSPIFFEID:     bundle.ServerSPIFFEID,
		gatewayCert:     gatewayCertificate,
		gatewaySigner:   gatewaySigner,
		iamSigner:       iamSigner,
		logs:            &logs,
	}
}

func (h iamHarness) request(t *testing.T, path string, body *strings.Reader, claims identitycontext.DelegationClaims, signer crypto.Signer) *http.Request {
	t.Helper()
	delegation, err := identitycontext.SignDelegation(signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(http.MethodPost, path, nil)
	} else {
		request = httptest.NewRequest(http.MethodPost, path, body)
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("X-Delegation-Grant", delegation)
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{h.gatewayCert},
		VerifiedChains:   [][]*x509.Certificate{{h.gatewayCert}},
	}
	return request
}

func (h iamHarness) registryDecision(t *testing.T, subject, actingOrganizationID string, action registryauth.Action) registryauth.DecisionResponse {
	t.Helper()
	claims := validIAMClaims(h.now, subject, registryAuthorize)
	request := h.request(t, iam.RegistryReadDecisionPath, registryDecisionBody(actingOrganizationID, action), claims, h.gatewaySigner)
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response registryauth.DecisionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func (h iamHarness) verifyRegistryGrant(t *testing.T, grant string, action registryauth.Action) registryauth.GrantClaims {
	t.Helper()
	claims, err := registryauth.VerifyGrant(h.iamSigner.Public(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if err := registryauth.ValidateGrant(claims, registryauth.GrantValidation{
		Now:                   h.now,
		Issuer:                h.iamSPIFFEID,
		Presenter:             h.gatewaySPIFFEID,
		Audience:              fixtureCoreAudience,
		Action:                action,
		CurrentPolicyRevision: iam.S1FixturePolicyRevision,
		IsRevoked:             func(string) (bool, error) { return false, nil },
	}); err != nil {
		t.Fatalf("IAM issued an invalid Core grant: %v", err)
	}
	return claims
}

func registryDecisionBody(actingOrganizationID string, action registryauth.Action) *strings.Reader {
	payload, err := json.Marshal(registryauth.DecisionRequest{ActingOrganizationID: actingOrganizationID, Action: action})
	if err != nil {
		panic(err)
	}
	return strings.NewReader(string(payload))
}

func validIAMClaims(now time.Time, subject, action string) identitycontext.DelegationClaims {
	return identitycontext.DelegationClaims{
		Issuer:               "spiffe://hvac.local/platform-gateway",
		Subject:              subject,
		SubjectIssuer:        fixtureSubjectIssuer,
		DisplayName:          "Fixture User",
		Email:                "fixture@example.test",
		Roles:                []string{"operator", "platform-admin"},
		ExecutingService:     "spiffe://hvac.local/platform-gateway",
		Audience:             "iam-service",
		ActingOrganizationID: iam.S1FixtureActingOrganizationID,
		Actions:              []string{action},
		Scopes:               []string{"session:" + fixtureSessionID},
		PolicyRevision:       "policy-v1",
		SessionID:            fixtureSessionID,
		IssuedAt:             now.Unix(),
		ExpiresAt:            now.Add(30 * time.Second).Unix(),
		TokenID:              fixtureInboundID,
	}
}

type fixedAuthorizationStore struct {
	facts iam.AuthorizationFacts
	err   error
}

func (store fixedAuthorizationStore) LookupRegistryAuthorization(_ context.Context, _ iam.AuthorizationLookup) (iam.AuthorizationFacts, error) {
	return store.facts, store.err
}

type capturingRegistryAuditSink struct {
	events []iam.RegistryDecisionAudit
	err    error
}

func (sink *capturingRegistryAuditSink) RecordRegistryDecision(_ context.Context, event iam.RegistryDecisionAudit) error {
	sink.events = append(sink.events, event)
	return sink.err
}

func assertIAMProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, status, recorder.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != code {
		t.Fatalf("code = %q, want %q", problem.Code, code)
	}
}

func assertStringsEqual(t *testing.T, actual, expected []string) {
	t.Helper()
	if strings.Join(actual, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("values = %#v, want %#v", actual, expected)
	}
}
