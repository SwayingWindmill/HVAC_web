package iam_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

const (
	reportWorkload    = "spiffe://hvac.local/report-service"
	reportPrincipalID = "018f1e00-2000-7000-8000-0000000000bb"
	queryAudience     = "telemetry-query-service"
)

func reportQuery(siteID string) analyticsmodel.EnergySeriesQuery {
	return analyticsmodel.EnergySeriesQuery{
		TenantID: iam.S1FixtureTenantAID, SiteID: siteID, EnergyType: analyticsmodel.EnergyTypeElectricity,
		Granularity: analyticsmodel.GranularityHour, Timezone: "Asia/Shanghai",
		From: time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC),
		QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	}
}

func analyticsWorkloadHarness(t *testing.T) iamHarness {
	return newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AuthorizationStore = reportPrincipalStore{}
		config.AnalyticsGrantAudience = queryAudience
		config.AllowedAnalyticsWorkloads = []string{reportWorkload}
	})
}

func (h iamHarness) analyticsWorkloadDecision(t *testing.T, workload string, query analyticsmodel.EnergySeriesQuery) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, h.workloadRequestTo(t, iam.AnalyticsWorkloadDecisionPath, workload, string(body)))
	return recorder
}

func TestIAMSignsAWorkloadItsOwnEnergyGrantForABoundSite(t *testing.T) {
	harness := analyticsWorkloadHarness(t)
	query := reportQuery(iam.S1FixtureOwnerASite1ID)
	recorder := harness.analyticsWorkloadDecision(t, reportWorkload, query)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response iam.AnalyticsWorkloadGrantResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	claims, err := identitycontext.VerifyDelegation(harness.iamSigner.Public(), response.DelegationGrant)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := query.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	// The grant the Telemetry Query Service checks: issued by IAM, presented by the workload itself,
	// bound to this exact query, with no user Session behind it.
	if err := identitycontext.ValidateWorkloadDelegation(claims, harness.now, harness.iamSPIFFEID, reportWorkload, queryAudience, analyticsmodel.EnergySeriesAction, digest); err != nil {
		t.Fatalf("grant invalid at the query service: %v", err)
	}
	if claims.Subject != reportWorkload || claims.SubjectIssuer != workloadTrustDomain || claims.SessionID != "" ||
		claims.PrincipalID != reportPrincipalID || claims.TenantID != iam.S1FixtureTenantAID || response.ExpiresAt != "2026-07-21T12:00:30Z" {
		t.Fatalf("grant does not name the workload itself: %#v %#v", claims, response)
	}
}

func TestIAMTreatsASiteTheWorkloadIsNotBoundToAsNotFound(t *testing.T) {
	harness := analyticsWorkloadHarness(t)
	recorder := harness.analyticsWorkloadDecision(t, reportWorkload, reportQuery(iam.S1FixtureOwnerASite2ID))
	assertIAMProblem(t, recorder, http.StatusNotFound, "IAM_RESOURCE_NOT_FOUND")
}

func TestIAMAnalyticsWorkloadDecisionRefusesSessionsAndUnlistedWorkloads(t *testing.T) {
	harness := analyticsWorkloadHarness(t)
	body, _ := json.Marshal(reportQuery(iam.S1FixtureOwnerASite1ID))
	withDelegation := harness.workloadRequestTo(t, iam.AnalyticsWorkloadDecisionPath, reportWorkload, string(body))
	withDelegation.Header.Set("X-Delegation-Grant", "session-delegation")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, withDelegation)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER")

	recorder = harness.analyticsWorkloadDecision(t, projectorWorkload, reportQuery(iam.S1FixtureOwnerASite1ID))
	assertIAMProblem(t, recorder, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID")
}

// reportPrincipalStore knows the report workload with Energy read on Site 1 only.
type reportPrincipalStore struct{}

func (reportPrincipalStore) LookupRegistryAuthorization(_ context.Context, lookup iam.AuthorizationLookup) (iam.AuthorizationFacts, error) {
	if lookup.SubjectIssuer != workloadTrustDomain || lookup.Subject != reportWorkload {
		return iam.AuthorizationFacts{Found: false, PolicyRevision: iam.S1FixturePolicyRevision}, nil
	}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return iam.AuthorizationFacts{
		Found:          true,
		PolicyRevision: iam.S1FixturePolicyRevision,
		Principal:      iam.PrincipalRecord{ID: reportPrincipalID, SubjectIssuer: workloadTrustDomain, Subject: reportWorkload, Status: iam.FactStatusActive},
		TenantSiteIDs:  []string{iam.S1FixtureOwnerASite1ID, iam.S1FixtureOwnerASite2ID},
		Memberships:    []iam.TenantMembership{{TenantID: iam.S1FixtureTenantAID, Status: iam.FactStatusActive, ValidFrom: since}},
		Assignments: []iam.RoleAssignment{{
			TenantID: iam.S1FixtureTenantAID, SiteID: iam.S1FixtureOwnerASite1ID, RoleKey: "report-energy-read",
			Capabilities: []string{analyticsmodel.EnergySeriesAction}, Status: iam.FactStatusActive, ValidFrom: since,
		}},
	}, nil
}
