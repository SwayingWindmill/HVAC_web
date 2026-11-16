package iam

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type memoryCommandWorkloadStore struct {
	facts CommandWorkloadAuthorizationFacts
	last  CommandWorkloadAuthorizationLookup
}

func (store *memoryCommandWorkloadStore) LookupCommandWorkloadAuthorization(_ context.Context, lookup CommandWorkloadAuthorizationLookup) (CommandWorkloadAuthorizationFacts, error) {
	store.last = lookup
	return store.facts, nil
}

func workloadCertTLSState(t *testing.T, spiffeID string) *tls.ConnectionState {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(spiffeID)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cert},
		VerifiedChains:   [][]*x509.Certificate{{cert}},
	}
}

func validWorkloadBaseFacts(now time.Time) CommandWorkloadAuthorizationFacts {
	return CommandWorkloadAuthorizationFacts{
		PrincipalFound:              true,
		PrincipalID:                 "01990000-1000-7000-8000-0000000000a1",
		PrincipalStatus:             FactStatusActive,
		TenantMemberActive:          true,
		PolicyRevision:              "command-policy-1",
		EmergencyRevocationRevision: 2,
		DeviceSiteID:                S1FixtureOwnerASite1ID,
		SiteControlMode:             "REMOTE_AUTO",
		HasAssetControlLock:         false,
		ActiveLease: &AutoControlLeaseFacts{
			ID:                  "01990000-2000-7000-8000-0000000000b1",
			ArmedBy:             "01990000-1000-7000-8000-000000000001",
			ExpiresAt:           now.Add(4 * time.Hour),
			AllowedCapabilities: []string{string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint)},
			MaxSetpointStepC:    1.0,
			SupplyTempMinC:      6.0,
			SupplyTempMaxC:      12.0,
			MaxCommandsPerHour:  2,
		},
		RecentAutoCommandsLastHour: 0,
	}
}

func TestIAMCommandWorkloadDecisionIssuesShortLivedLeaseBoundGrant(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryCommandWorkloadStore{facts: validWorkloadBaseFacts(now)}
	h := NewHandler(Config{
		Now:                               func() time.Time { return now },
		CommandWorkloadAuthorizationStore: store,
		CommandGrantSigner:                signer,
		CommandGrantIssuer:                "spiffe://hvac.local/iam-service",
		CommandGrantAudience:              "command-service",
		NewCommandGrantID:                 func() string { return "grant-workload-1" },
	})

	current := 7.0
	body, _ := json.Marshal(commandauth.WorkloadDecisionRequest{
		TenantID:           S1FixtureTenantAID,
		SiteID:             S1FixtureOwnerASite1ID,
		DeviceID:           "01990000-3000-7000-8000-0000000000c1",
		Capability:         commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		CapabilityRevision: "capability:set-chilled-water-temperature-setpoint:v1",
		Purpose:            commandmodel.AuthorizationCommandSubmit,
		Parameters:         commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.0},
		CurrentValue:       &current,
	})
	req := httptest.NewRequest(http.MethodPost, CommandWorkloadDecisionPath, bytes.NewReader(body))
	req.TLS = workloadCertTLSState(t, defaultOptimizationCommandWorkload)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out commandauth.DecisionResponse
	if json.NewDecoder(rec.Body).Decode(&out) != nil {
		t.Fatal("decode response failed")
	}
	if !out.Decision.Allowed || out.Decision.ReasonCode != commandauth.ReasonAllowAutoLease || out.DelegationGrant == "" {
		t.Fatalf("unexpected workload decision: %#v", out)
	}
	claims, err := commandauth.VerifyGrant(&signer.PublicKey, out.DelegationGrant)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Presenter != defaultCommandWorkloadPresenter ||
		claims.WorkloadSPIFFE != defaultOptimizationCommandWorkload ||
		claims.LeaseID != store.facts.ActiveLease.ID ||
		claims.ArmedBy != store.facts.ActiveLease.ArmedBy ||
		claims.Transitive ||
		claims.ExpiresAt-claims.IssuedAt > 30 {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func TestIAMCommandWorkloadDecisionEnforcesAllSafetyGates(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	current := 7.0
	baseReq := commandauth.WorkloadDecisionRequest{
		TenantID:           S1FixtureTenantAID,
		SiteID:             S1FixtureOwnerASite1ID,
		DeviceID:           "01990000-3000-7000-8000-0000000000c1",
		Capability:         commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		CapabilityRevision: "capability:set-chilled-water-temperature-setpoint:v1",
		Purpose:            commandmodel.AuthorizationCommandSubmit,
		Parameters:         commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.0},
		CurrentValue:       &current,
	}

	cases := []struct {
		name       string
		mutateFact func(*CommandWorkloadAuthorizationFacts)
		mutateReq  func(*commandauth.WorkloadDecisionRequest)
		wantReason commandauth.ReasonCode
	}{
		{
			name:       "no active lease",
			mutateFact: func(f *CommandWorkloadAuthorizationFacts) { f.ActiveLease = nil },
			wantReason: commandauth.ReasonDenyAutoLease,
		},
		{
			name:       "expired lease",
			mutateFact: func(f *CommandWorkloadAuthorizationFacts) { f.ActiveLease.ExpiresAt = now.Add(-time.Second) },
			wantReason: commandauth.ReasonDenyAutoLease,
		},
		{
			name:       "site control manual override",
			mutateFact: func(f *CommandWorkloadAuthorizationFacts) { f.SiteControlMode = "MANUAL_OVERRIDE" },
			wantReason: commandauth.ReasonDenyControlLock,
		},
		{
			name:       "asset control lock",
			mutateFact: func(f *CommandWorkloadAuthorizationFacts) { f.HasAssetControlLock = true },
			wantReason: commandauth.ReasonDenyControlLock,
		},
		{
			name: "capability not allowlisted",
			mutateFact: func(f *CommandWorkloadAuthorizationFacts) {
				f.ActiveLease.AllowedCapabilities = []string{"SET_TEMPERATURE_SETPOINT"}
			},
			wantReason: commandauth.ReasonDenyCapability,
		},
		{
			name:       "step exceeds max_setpoint_step_c",
			mutateReq:  func(r *commandauth.WorkloadDecisionRequest) { r.Parameters["setpointC"] = 8.5 },
			wantReason: commandauth.ReasonDenyAutoLease,
		},
		{
			name: "outside supply bounds",
			mutateFact: func(f *CommandWorkloadAuthorizationFacts) {
				f.ActiveLease.SupplyTempMaxC = 7.5
			},
			wantReason: commandauth.ReasonDenyAutoLease,
		},
		{
			name:       "hourly rate limit reached",
			mutateFact: func(f *CommandWorkloadAuthorizationFacts) { f.RecentAutoCommandsLastHour = 2 },
			wantReason: commandauth.ReasonDenyRateLimit,
		},
		{
			name:       "rollback without observing monitor rejected",
			mutateReq:  func(r *commandauth.WorkloadDecisionRequest) { r.IsRollback = true },
			wantReason: commandauth.ReasonDenyAutoLease,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := validWorkloadBaseFacts(now)
			if tc.mutateFact != nil {
				tc.mutateFact(&facts)
			}
			req := baseReq
			req.Parameters = commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.0}
			if tc.mutateReq != nil {
				tc.mutateReq(&req)
			}
			decision, err := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: facts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, req)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed || decision.ReasonCode != tc.wantReason {
				t.Fatalf("got allowed=%t reason=%s, want false/%s (detail=%q)", decision.Allowed, decision.ReasonCode, tc.wantReason, decision.Detail)
			}
		})
	}

	// Valid rollback with matching OBSERVING monitor succeeds even when hourly quota is full.
	rollbackFacts := validWorkloadBaseFacts(now)
	rollbackFacts.RecentAutoCommandsLastHour = 2
	rollbackFacts.MatchingObservingRollback = true
	rollbackReq := baseReq
	rollbackReq.IsRollback = true
	rollbackReq.Parameters = commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 7.0}
	currentAtFault := 8.0
	rollbackReq.CurrentValue = &currentAtFault
	decision, err := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: rollbackFacts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, rollbackReq)
	if err != nil || !decision.Allowed || decision.ReasonCode != commandauth.ReasonAllowAutoLease {
		t.Fatalf("expected valid rollback decision to allow, got %#v err=%v", decision, err)
	}
}

func TestIAMCommandWorkloadDecisionRejectsUnlistedWorkloadAndForgedHeaders(t *testing.T) {
	now := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	h := NewHandler(Config{Now: func() time.Time { return now }})
	current := 7.0
	body, _ := json.Marshal(commandauth.WorkloadDecisionRequest{
		TenantID: S1FixtureTenantAID, SiteID: S1FixtureOwnerASite1ID, DeviceID: "01990000-3000-7000-8000-0000000000c1",
		Capability:         commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		CapabilityRevision: "capability:set-chilled-water-temperature-setpoint:v1",
		Purpose:            commandmodel.AuthorizationCommandSubmit,
		Parameters:         commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.0},
		CurrentValue:       &current,
	})

	untrusted := httptest.NewRequest(http.MethodPost, CommandWorkloadDecisionPath, bytes.NewReader(body))
	untrusted.TLS = workloadCertTLSState(t, "spiffe://hvac.local/report-service")
	untrustedRec := httptest.NewRecorder()
	h.ServeHTTP(untrustedRec, untrusted)
	if untrustedRec.Code != http.StatusUnauthorized {
		t.Fatalf("untrusted workload status=%d want 401", untrustedRec.Code)
	}

	forged := httptest.NewRequest(http.MethodPost, CommandWorkloadDecisionPath, bytes.NewReader(body))
	forged.TLS = workloadCertTLSState(t, defaultOptimizationCommandWorkload)
	forged.Header.Set("X-Delegation-Grant", "forged")
	forgedRec := httptest.NewRecorder()
	h.ServeHTTP(forgedRec, forged)
	if forgedRec.Code != http.StatusBadRequest {
		t.Fatalf("forged header status=%d want 400", forgedRec.Code)
	}
}
