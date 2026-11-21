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

	// Rollback with matching OBSERVING monitor succeeds even after lease expires or is revoked mid-window.
	expiredOrRevokedFacts := rollbackFacts
	expiredOrRevokedFacts.ActiveLease = &AutoControlLeaseFacts{
		ID:                  rollbackFacts.ActiveLease.ID,
		ArmedBy:             rollbackFacts.ActiveLease.ArmedBy,
		ExpiresAt:           now.Add(-5 * time.Minute),
		AllowedCapabilities: rollbackFacts.ActiveLease.AllowedCapabilities,
		MaxSetpointStepC:    1.5,
		SupplyTempMinC:      6.0,
		SupplyTempMaxC:      12.0,
		MaxCommandsPerHour:  2,
	}
	appliedStep := 8.5
	rollbackReq.CurrentValue = &appliedStep
	decision, err = evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: expiredOrRevokedFacts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, rollbackReq)
	if err != nil || !decision.Allowed || decision.ReasonCode != commandauth.ReasonAllowAutoLease || decision.MaximumRisk != commandmodel.RiskMedium {
		t.Fatalf("expected rollback under expired/revoked lease with OBSERVING monitor to allow medium risk, got %#v err=%v", decision, err)
	}

	// Control exit or asset control lock still blocks rollback during observation window.
	lockedRollback := expiredOrRevokedFacts
	lockedRollback.HasAssetControlLock = true
	decision, err = evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: lockedRollback}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, rollbackReq)
	if err != nil || decision.Allowed || decision.ReasonCode != commandauth.ReasonDenyControlLock {
		t.Fatalf("expected asset control lock to block rollback, got %#v err=%v", decision, err)
	}

	// Active manual backoff pauses normal automated dispatch while permitting explicit rollback.
	backoffUntil := now.Add(30 * time.Minute)
	backoffFacts := validWorkloadBaseFacts(now)
	backoffFacts.ActiveLease.ManualBackoffUntil = &backoffUntil
	decision, err = evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: backoffFacts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, baseReq)
	if err != nil || decision.Allowed || decision.ReasonCode != commandauth.ReasonDenyAutoLease {
		t.Fatalf("expected active manual backoff to block automated command, got %#v err=%v", decision, err)
	}

	// Safety rollback restoring lease-bounded setpoint succeeds even if live delta exceeds 3.0 C.
	largeDeltaRollback := rollbackReq
	driftedCurrent := 11.0
	largeDeltaRollback.CurrentValue = &driftedCurrent
	decision, err = evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: rollbackFacts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, largeDeltaRollback)
	if err != nil || !decision.Allowed || decision.ReasonCode != commandauth.ReasonAllowAutoLease || decision.MaximumRisk != commandmodel.RiskMedium {
		t.Fatalf("expected large-delta rollback inside lease bounds to allow medium risk, got %#v err=%v", decision, err)
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

func TestIAMCommandWorkloadDecisionShadowModeBlocksDispatchButAllowsRollback(t *testing.T) {
	now := time.Date(2026, 11, 18, 5, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
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

	for _, mode := range []string{"SHADOW", " shadow "} {
		t.Run("non-rollback denied for "+mode, func(t *testing.T) {
			facts := validWorkloadBaseFacts(now)
			facts.ActiveLease.ExecutionMode = mode
			decision, evalErr := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: facts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, baseReq)
			if evalErr != nil {
				t.Fatal(evalErr)
			}
			if decision.Allowed || decision.ReasonCode != commandauth.ReasonDenyAutoLease || decision.Detail != "shadow_mode_lease_blocks_closed_loop_dispatch" {
				t.Fatalf("expected SHADOW deny, got %#v", decision)
			}
		})
	}

	for _, mode := range []string{"", "CLOSED_LOOP", " closed_loop "} {
		t.Run("non-rollback allowed for "+mode, func(t *testing.T) {
			facts := validWorkloadBaseFacts(now)
			facts.ActiveLease.ExecutionMode = mode
			decision, evalErr := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: facts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, baseReq)
			if evalErr != nil {
				t.Fatal(evalErr)
			}
			if !decision.Allowed || decision.ReasonCode != commandauth.ReasonAllowAutoLease {
				t.Fatalf("expected CLOSED_LOOP allow, got %#v", decision)
			}
		})
	}

	t.Run("unsupported execution mode fails closed", func(t *testing.T) {
		facts := validWorkloadBaseFacts(now)
		facts.ActiveLease.ExecutionMode = "DRY_RUN_UNKNOWN"
		decision, evalErr := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: facts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, baseReq)
		if evalErr != nil {
			t.Fatal(evalErr)
		}
		if decision.Allowed || decision.ReasonCode != commandauth.ReasonDenyAutoLease {
			t.Fatalf("expected unsupported mode deny, got %#v", decision)
		}
	})

	t.Run("rollback of OBSERVING monitor succeeds even when lease is SHADOW", func(t *testing.T) {
		facts := validWorkloadBaseFacts(now)
		facts.ActiveLease.ExecutionMode = "SHADOW"
		facts.MatchingObservingRollback = true
		rollbackReq := baseReq
		rollbackReq.IsRollback = true
		rollbackReq.Parameters = commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 7.0}
		applied := 8.0
		rollbackReq.CurrentValue = &applied

		decision, evalErr := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: facts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, rollbackReq)
		if evalErr != nil || !decision.Allowed || decision.ReasonCode != commandauth.ReasonAllowAutoLease {
			t.Fatalf("expected rollback under SHADOW lease with OBSERVING monitor to allow, got %#v err=%v", decision, evalErr)
		}
	})

	t.Run("rollback without matching OBSERVING monitor denied under SHADOW", func(t *testing.T) {
		facts := validWorkloadBaseFacts(now)
		facts.ActiveLease.ExecutionMode = "SHADOW"
		facts.MatchingObservingRollback = false
		rollbackReq := baseReq
		rollbackReq.IsRollback = true

		decision, evalErr := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: facts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, rollbackReq)
		if evalErr != nil || decision.Allowed || decision.ReasonCode != commandauth.ReasonDenyAutoLease {
			t.Fatalf("expected unmatched rollback under SHADOW lease to deny, got %#v err=%v", decision, evalErr)
		}
	})

	t.Run("HTTP endpoint blocks SHADOW dispatch and signs grant for SHADOW rollback", func(t *testing.T) {
		shadowFacts := validWorkloadBaseFacts(now)
		shadowFacts.ActiveLease.ExecutionMode = "SHADOW"
		shadowFacts.MatchingObservingRollback = true
		store := &memoryCommandWorkloadStore{facts: shadowFacts}
		h := NewHandler(Config{
			Now:                               func() time.Time { return now },
			CommandWorkloadAuthorizationStore: store,
			CommandGrantSigner:                signer,
			CommandGrantIssuer:                "spiffe://hvac.local/iam-service",
			CommandGrantAudience:              "command-service",
			NewCommandGrantID:                 func() string { return "grant-shadow-rollback-1" },
		})

		dispatchBody, _ := json.Marshal(baseReq)
		dispatchHTTP := httptest.NewRequest(http.MethodPost, CommandWorkloadDecisionPath, bytes.NewReader(dispatchBody))
		dispatchHTTP.TLS = workloadCertTLSState(t, defaultOptimizationCommandWorkload)
		dispatchRec := httptest.NewRecorder()
		h.ServeHTTP(dispatchRec, dispatchHTTP)
		if dispatchRec.Code != http.StatusOK {
			t.Fatalf("dispatch status=%d body=%s", dispatchRec.Code, dispatchRec.Body.String())
		}
		var dispatchOut commandauth.DecisionResponse
		if err := json.NewDecoder(dispatchRec.Body).Decode(&dispatchOut); err != nil {
			t.Fatal(err)
		}
		if dispatchOut.Decision.Allowed || dispatchOut.Decision.ReasonCode != commandauth.ReasonDenyAutoLease || dispatchOut.Decision.Detail != "shadow_mode_lease_blocks_closed_loop_dispatch" || dispatchOut.DelegationGrant != "" {
			t.Fatalf("expected SHADOW HTTP dispatch deny without grant, got %#v", dispatchOut)
		}

		rbReq := baseReq
		rbReq.IsRollback = true
		rbReq.Parameters = commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 7.0}
		applied := 8.0
		rbReq.CurrentValue = &applied
		rbBody, _ := json.Marshal(rbReq)
		rbHTTP := httptest.NewRequest(http.MethodPost, CommandWorkloadDecisionPath, bytes.NewReader(rbBody))
		rbHTTP.TLS = workloadCertTLSState(t, defaultOptimizationCommandWorkload)
		rbRec := httptest.NewRecorder()
		h.ServeHTTP(rbRec, rbHTTP)
		if rbRec.Code != http.StatusOK {
			t.Fatalf("rollback status=%d body=%s", rbRec.Code, rbRec.Body.String())
		}
		var rbOut commandauth.DecisionResponse
		if err := json.NewDecoder(rbRec.Body).Decode(&rbOut); err != nil {
			t.Fatal(err)
		}
		if !rbOut.Decision.Allowed || rbOut.Decision.ReasonCode != commandauth.ReasonAllowAutoLease || rbOut.DelegationGrant == "" {
			t.Fatalf("expected SHADOW HTTP rollback grant, got %#v", rbOut)
		}
	})
}

func TestIAMCommandWorkloadDecisionEnforcesMultiLoopFrequencyAndFanBounds(t *testing.T) {
	now := time.Date(2026, 11, 21, 4, 0, 0, 0, time.UTC)
	freqMin := 30.0
	freqMax := 48.0
	freqStep := 2.5
	fanMin := 35.0
	fanMax := 90.0
	fanStep := 10.0

	facts := validWorkloadBaseFacts(now)
	facts.ActiveLease.AllowedCapabilities = []string{
		string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
		string(commandmodel.CapabilitySetFrequency),
		string(commandmodel.CapabilitySetFanSpeed),
	}
	facts.ActiveLease.PumpFreqMinHz = &freqMin
	facts.ActiveLease.PumpFreqMaxHz = &freqMax
	facts.ActiveLease.MaxFreqStepHz = &freqStep
	facts.ActiveLease.FanSpeedMinPct = &fanMin
	facts.ActiveLease.FanSpeedMaxPct = &fanMax
	facts.ActiveLease.MaxFanStepPct = &fanStep

	currentFreq := 40.0
	freqReq := commandauth.WorkloadDecisionRequest{
		TenantID:           "01990000-0000-7000-8000-000000000001",
		SiteID:             S1FixtureOwnerASite1ID,
		DeviceID:           "01990000-3000-7000-8000-0000000000f1",
		Capability:         commandmodel.CapabilitySetFrequency,
		CapabilityRevision: "capability:set-frequency:v1",
		Purpose:            commandmodel.AuthorizationCommandSubmit,
		Parameters:         commandmodel.CommandParameters{commandmodel.ParameterFrequencyHz: 42.0},
		CurrentValue:       &currentFreq,
	}
	decision, err := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: facts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, freqReq)
	if err != nil || !decision.Allowed || decision.ReasonCode != commandauth.ReasonAllowAutoLease {
		t.Fatalf("expected SET_FREQUENCY within lease bounds+step to allow, got %#v err=%v", decision, err)
	}

	outOfBoundsFreq := freqReq
	outOfBoundsFreq.Parameters = commandmodel.CommandParameters{commandmodel.ParameterFrequencyHz: 29.0}
	if got, err := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: facts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, outOfBoundsFreq); err != nil || got.Allowed || got.ReasonCode != commandauth.ReasonDenyAutoLease {
		t.Fatalf("expected SET_FREQUENCY below lease pumpFreqMinHz to deny, got %#v err=%v", got, err)
	}

	excessStepFreq := freqReq
	excessStepFreq.Parameters = commandmodel.CommandParameters{commandmodel.ParameterFrequencyHz: 44.0}
	if got, err := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: facts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, excessStepFreq); err != nil || got.Allowed || got.ReasonCode != commandauth.ReasonDenyAutoLease {
		t.Fatalf("expected SET_FREQUENCY step 4.0 > 2.5 Hz to deny, got %#v err=%v", got, err)
	}

	currentFan := 65.0
	fanReq := freqReq
	fanReq.Capability = commandmodel.CapabilitySetFanSpeed
	fanReq.CapabilityRevision = "capability:set-fan-speed:v1"
	fanReq.Parameters = commandmodel.CommandParameters{commandmodel.ParameterFanSpeedPct: 72.0}
	fanReq.CurrentValue = &currentFan
	if got, err := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: facts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, fanReq); err != nil || !got.Allowed || got.ReasonCode != commandauth.ReasonAllowAutoLease {
		t.Fatalf("expected SET_FAN_SPEED within lease bounds+step to allow, got %#v err=%v", got, err)
	}

	shadowFacts := facts
	shadowFacts.ActiveLease.ExecutionMode = "SHADOW"
	shadowFacts.MatchingObservingRollback = true
	rbFan := fanReq
	rbFan.IsRollback = true
	rbFan.CurrentValue = nil
	if got, err := evaluateWorkloadCommandAuthorization(t.Context(), &memoryCommandWorkloadStore{facts: shadowFacts}, now, "spiffe://hvac.local", defaultOptimizationCommandWorkload, rbFan); err != nil || !got.Allowed || got.MaximumRisk != commandmodel.RiskMedium {
		t.Fatalf("expected SET_FAN_SPEED rollback with nil CurrentValue to allow with medium risk, got %#v err=%v", got, err)
	}
}
