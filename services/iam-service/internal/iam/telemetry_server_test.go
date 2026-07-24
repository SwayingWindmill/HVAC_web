package iam_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

const (
	telemetryAuthorize = "telemetry:authorize"
	telemetryDeviceID  = "018f1e00-4000-7000-8000-000000000001"
	telemetryOwnerID   = "018f1e00-0000-7000-8000-000000000001"
	telemetrySiteID    = "018f1e00-1000-7000-8000-000000000001"
)

type fixedTelemetryStore struct {
	facts iam.TelemetryAuthorizationFacts
	err   error
}

type memoryTelemetryGrantStore struct {
	used       map[string]bool
	facts      []iam.TelemetryRevocationFact
	consumeErr error
	pollErr    error
}

func (store *memoryTelemetryGrantStore) ConsumeGrant(_ context.Context, claims telemetryauth.GrantClaims, _ time.Time) (telemetryauth.GrantUseStatus, error) {
	if store.consumeErr != nil {
		return telemetryauth.GrantUseStatus{}, store.consumeErr
	}
	if store.used == nil {
		store.used = map[string]bool{}
	}
	status := telemetryauth.GrantUseStatus{CurrentPolicyRevision: claims.PolicyRevision, Replayed: store.used[claims.TokenID]}
	store.used[claims.TokenID] = true
	return status, nil
}

func (store *memoryTelemetryGrantStore) PollRevocations(_ context.Context, _ string, afterSequence int64, limit int) ([]iam.TelemetryRevocationFact, error) {
	if store.pollErr != nil {
		return nil, store.pollErr
	}
	result := []iam.TelemetryRevocationFact{}
	for _, fact := range store.facts {
		if fact.Sequence > afterSequence && len(result) < limit {
			result = append(result, fact)
		}
	}
	return result, nil
}

func (store fixedTelemetryStore) LookupTelemetryAuthorization(context.Context, iam.TelemetryAuthorizationLookup) (iam.TelemetryAuthorizationFacts, error) {
	return store.facts, store.err
}

func TestIAMTelemetryDecisionIssuesExactNonTransitiveGrant(t *testing.T) {
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.TelemetryAuthorizationStore = fixedTelemetryStore{facts: telemetryHTTPFacts(harnessTime())}
		config.NewTelemetryGrantID = func() string { return "telemetry-decision-1" }
	})
	body := `{"actingOrganizationId":"` + iam.S1FixtureActingOrganizationID + `","action":"telemetry.snapshot.read","targets":[{"deviceId":"` + telemetryDeviceID + `","keys":["zone.temperature"]}]}`
	request := harness.request(t, iam.TelemetryDecisionPath, strings.NewReader(body), validIAMClaims(harness.now, "fixture-user", telemetryAuthorize), harness.gatewaySigner)
	request.Header.Set("X-Request-ID", "request-telemetry-1")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response telemetryauth.DecisionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.Decision.Allowed || response.DelegationGrant == "" {
		t.Fatalf("response=%#v", response)
	}
	claims, err := telemetryauth.VerifyGrant(harness.iamSigner.Public(), response.DelegationGrant)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Audience != "telemetry-runtime-service" || claims.Presenter != harness.gatewaySPIFFEID || claims.Action != telemetryauth.ActionSnapshotRead || claims.Transitive || claims.RequestID != "request-telemetry-1" || claims.Route != "/api/v1/devices/{deviceId}/observation-snapshot" {
		t.Fatalf("grant claims=%#v", claims)
	}
}

func TestIAMTelemetryDecisionDeniesWithoutGrantAndFailsClosedOnDependency(t *testing.T) {
	now := harnessTime()
	deniedFacts := telemetryHTTPFacts(now)
	deniedFacts.KeyBindings = nil
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.TelemetryAuthorizationStore = fixedTelemetryStore{facts: deniedFacts}
	})
	body := `{"actingOrganizationId":"` + iam.S1FixtureActingOrganizationID + `","action":"telemetry.snapshot.read","targets":[{"deviceId":"` + telemetryDeviceID + `","keys":["zone.temperature"]}]}`
	request := harness.request(t, iam.TelemetryDecisionPath, strings.NewReader(body), validIAMClaims(harness.now, "fixture-user", telemetryAuthorize), harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "delegationGrant") || !strings.Contains(recorder.Body.String(), "TELEMETRY_KEY_INVALID") {
		t.Fatalf("deny status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	unavailable := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.TelemetryAuthorizationStore = fixedTelemetryStore{err: context.DeadlineExceeded}
	})
	request = unavailable.request(t, iam.TelemetryDecisionPath, strings.NewReader(body), validIAMClaims(unavailable.now, "fixture-user", telemetryAuthorize), unavailable.gatewaySigner)
	recorder = httptest.NewRecorder()
	unavailable.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "IAM_AUTHORIZATION_UNAVAILABLE") {
		t.Fatalf("unavailable status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTelemetryRuntimeConsumesGrantOnceAndPollsRevocations(t *testing.T) {
	grantStore := &memoryTelemetryGrantStore{facts: []iam.TelemetryRevocationFact{{
		Sequence: 7, PrincipalID: "018f1e00-2000-7000-8000-000000000001",
		ActingOrganizationID: iam.S1FixtureActingOrganizationID, SourceType: "KEY_PERMISSION",
		DeviceID: telemetryDeviceID, TelemetryKey: "zone.temperature", PolicyRevision: "telemetry-access:1",
		ReasonCode: "KEY_SCOPE_CHANGED", OccurredAt: "2026-07-21T12:00:00.000Z",
	}}}
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.TelemetryAuthorizationStore = fixedTelemetryStore{facts: telemetryHTTPFacts(harnessTime())}
		config.NewTelemetryGrantID = func() string { return "telemetry-runtime-use-1" }
		config.TelemetryRuntimeSPIFFE = "spiffe://hvac.local/platform-gateway"
		config.TelemetryGrantStore = grantStore
	})
	decisionBody := `{"actingOrganizationId":"` + iam.S1FixtureActingOrganizationID + `","action":"telemetry.snapshot.read","targets":[{"deviceId":"` + telemetryDeviceID + `","keys":["zone.temperature"]}]}`
	decisionRequest := harness.request(t, iam.TelemetryDecisionPath, strings.NewReader(decisionBody), validIAMClaims(harness.now, "fixture-user", telemetryAuthorize), harness.gatewaySigner)
	decisionRecorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(decisionRecorder, decisionRequest)
	if decisionRecorder.Code != http.StatusOK {
		t.Fatalf("decision status=%d body=%s", decisionRecorder.Code, decisionRecorder.Body.String())
	}
	var decision telemetryauth.DecisionResponse
	if err := json.NewDecoder(decisionRecorder.Body).Decode(&decision); err != nil {
		t.Fatal(err)
	}
	claims, err := telemetryauth.VerifyGrant(harness.iamSigner.Public(), decision.DelegationGrant)
	if err != nil {
		t.Fatal(err)
	}
	consumePayload, err := json.Marshal(map[string]any{
		"delegationGrant":      decision.DelegationGrant,
		"principalId":          claims.PrincipalID,
		"sessionId":            claims.SessionID,
		"actingOrganizationId": claims.ActingOrganizationID,
		"action":               claims.Action,
		"targets":              []telemetryauth.Target{{DeviceID: telemetryDeviceID, Keys: []string{"zone.temperature"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	consume := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, iam.TelemetryGrantConsumePath, strings.NewReader(string(consumePayload)))
		request.Header.Set("Content-Type", "application/json")
		request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{harness.gatewayCert}, VerifiedChains: [][]*x509.Certificate{{harness.gatewayCert}}}
		recorder := httptest.NewRecorder()
		harness.handler.ServeHTTP(recorder, request)
		return recorder
	}
	if recorder := consume(); recorder.Code != http.StatusOK {
		t.Fatalf("first consume status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := consume(); recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "IAM_TELEMETRY_GRANT_REJECTED") {
		t.Fatalf("replay status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	grantStore.consumeErr = context.DeadlineExceeded
	if recorder := consume(); recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "IAM_TELEMETRY_GRANT_STATE_UNAVAILABLE") {
		t.Fatalf("grant-state unavailable status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	grantStore.consumeErr = nil

	pollPayload := `{"actingOrganizationId":"` + iam.S1FixtureActingOrganizationID + `","afterSequence":0,"limit":10}`
	pollRequest := httptest.NewRequest(http.MethodPost, iam.TelemetryRevocationPollPath, strings.NewReader(pollPayload))
	pollRequest.Header.Set("Content-Type", "application/json")
	pollRequest.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{harness.gatewayCert}, VerifiedChains: [][]*x509.Certificate{{harness.gatewayCert}}}
	pollRecorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(pollRecorder, pollRequest)
	if pollRecorder.Code != http.StatusOK || !strings.Contains(pollRecorder.Body.String(), `"nextSequence":7`) || !strings.Contains(pollRecorder.Body.String(), "KEY_SCOPE_CHANGED") {
		t.Fatalf("poll status=%d body=%s", pollRecorder.Code, pollRecorder.Body.String())
	}

	verifierUnavailable := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.RegistryGrantSigner = nil
		config.TelemetryGrantSigner = nil
		config.TelemetryRuntimeSPIFFE = "spiffe://hvac.local/platform-gateway"
		config.TelemetryGrantStore = grantStore
	})
	verifierRequest := httptest.NewRequest(http.MethodPost, iam.TelemetryGrantConsumePath, strings.NewReader(string(consumePayload)))
	verifierRequest.Header.Set("Content-Type", "application/json")
	verifierRequest.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{verifierUnavailable.gatewayCert}, VerifiedChains: [][]*x509.Certificate{{verifierUnavailable.gatewayCert}}}
	verifierRecorder := httptest.NewRecorder()
	verifierUnavailable.handler.ServeHTTP(verifierRecorder, verifierRequest)
	if verifierRecorder.Code != http.StatusServiceUnavailable || !strings.Contains(verifierRecorder.Body.String(), "IAM_TELEMETRY_GRANT_VERIFIER_UNAVAILABLE") {
		t.Fatalf("verifier unavailable status=%d body=%s", verifierRecorder.Code, verifierRecorder.Body.String())
	}
	if strings.Contains(harness.logs.String(), decision.DelegationGrant) {
		t.Fatal("Telemetry runtime logs leaked the raw delegation grant")
	}
}

func telemetryHTTPFacts(now time.Time) iam.TelemetryAuthorizationFacts {
	return iam.TelemetryAuthorizationFacts{
		Found: true, PolicyRevision: "telemetry-access:1",
		Principal:   iam.PrincipalRecord{ID: "018f1e00-2000-7000-8000-000000000001", SubjectIssuer: fixtureSubjectIssuer, Subject: "fixture-user", Status: iam.FactStatusActive},
		Memberships: []iam.OrganizationMembership{{OrganizationID: iam.S1FixtureActingOrganizationID, Status: iam.FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		RoleBindings: []iam.RoleBinding{{
			OrganizationID: iam.S1FixtureActingOrganizationID, Actions: []registryauth.Action{registryauth.Action(telemetryauth.ActionSnapshotRead)},
			Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive, ValidFrom: now.Add(-time.Hour),
		}},
		SiteBindings: []iam.SiteBinding{{
			ActingOrganizationID: iam.S1FixtureActingOrganizationID, OwningOrganizationID: telemetryOwnerID, SiteID: telemetrySiteID,
			Actions: []registryauth.Action{registryauth.Action(telemetryauth.ActionSnapshotRead)}, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive, ValidFrom: now.Add(-time.Hour),
		}},
		Devices:       []iam.TelemetryDevice{{ID: telemetryDeviceID, OwningOrganizationID: telemetryOwnerID, SiteID: telemetrySiteID, Status: iam.FactStatusActive}},
		ScopeBindings: []iam.TelemetryScopeBinding{{ActingOrganizationID: iam.S1FixtureActingOrganizationID, OwningOrganizationID: telemetryOwnerID, SiteID: telemetrySiteID, DeviceID: telemetryDeviceID, Actions: []telemetryauth.Action{telemetryauth.ActionSnapshotRead}, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		KeyBindings:   []iam.TelemetryKeyBinding{{ActingOrganizationID: iam.S1FixtureActingOrganizationID, DeviceID: telemetryDeviceID, Key: "zone.temperature", Actions: []telemetryauth.Action{telemetryauth.ActionSnapshotRead}, Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
	}
}

func harnessTime() time.Time {
	return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
}
