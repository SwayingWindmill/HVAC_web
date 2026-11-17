package connectivity

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func sampleShadowParams(modbusID, txPower int) *ConcentratorShadowParams {
	return &ConcentratorShadowParams{
		ModbusID:        modbusID,
		BaudRateCode:    3,
		OffLineTime:     60,
		DataUplinkCycle: 30,
		LocalNet:        100,
		TimeMark:        1,
		RfTxPower:       txPower,
		RfFrequency:     470,
		RfBandWidth:     2,
		RfSf:            7,
		RfLeadCode:      8,
		TxTimeout:       3000,
		RxTimeout:       3000,
		RssiOut:         1,
	}
}

func TestConfigShadowValidationRules(t *testing.T) {
	validMappings := []GatewayPointMapping{
		{
			SourceChannel:    "t10",
			PrimaryDeviceKey: "chiller-a",
			PrimaryPointCode: "chw_entering_temp",
			CopyTargets: []PointCopyTarget{
				{DeviceKey: "plant-header", PointCode: "chw_branch_temp"},
			},
		},
	}
	validPolicies := []GatewaySamplingPolicy{
		{DeviceKey: "chiller-a", PointCode: "*", SampleIntervalMS: 5000, PublishIntervalMS: 10000},
		{DeviceKey: "chiller-a", PointCode: "chw_entering_temp", SampleIntervalMS: 1000, PublishIntervalMS: 5000},
	}
	validUnits := map[string]ConcentratorUnitShadow{
		"20": {
			Params: sampleShadowParams(20, 20),
			EIDs:   []ConcentratorShadowEIDSlot{{Slot: 1, EID: 33661717}, {Slot: 2, EID: 0}},
		},
	}
	if _, _, _, _, _, _, err := normalizeAndValidateDesiredShadow(validMappings, validPolicies, validUnits); err != nil {
		t.Fatalf("expected valid desired config to pass, got %v", err)
	}

	invalidCases := []struct {
		name     string
		mappings []GatewayPointMapping
		policies []GatewaySamplingPolicy
		units    map[string]ConcentratorUnitShadow
	}{
		{
			name:     "empty sourceChannel",
			mappings: []GatewayPointMapping{{SourceChannel: " ", PrimaryDeviceKey: "dev1", PrimaryPointCode: "temp_c"}},
		},
		{
			name:     "invalid primaryPointCode",
			mappings: []GatewayPointMapping{{SourceChannel: "t1", PrimaryDeviceKey: "dev1", PrimaryPointCode: "TempC"}},
		},
		{
			name: "copy target equals primary",
			mappings: []GatewayPointMapping{{
				SourceChannel:    "t1",
				PrimaryDeviceKey: "dev1",
				PrimaryPointCode: "temp_c",
				CopyTargets:      []PointCopyTarget{{DeviceKey: "dev1", PointCode: "temp_c"}},
			}},
		},
		{
			name: "duplicate copy target within mapping",
			mappings: []GatewayPointMapping{{
				SourceChannel:    "t1",
				PrimaryDeviceKey: "dev1",
				PrimaryPointCode: "temp_c",
				CopyTargets: []PointCopyTarget{
					{DeviceKey: "dev2", PointCode: "temp_c"},
					{DeviceKey: "dev2", PointCode: "temp_c"},
				},
			}},
		},
		{
			name: "copy target collides with another primary",
			mappings: []GatewayPointMapping{
				{
					SourceChannel:    "t1",
					PrimaryDeviceKey: "dev1",
					PrimaryPointCode: "temp_c",
					CopyTargets:      []PointCopyTarget{{DeviceKey: "dev2", PointCode: "temp_c"}},
				},
				{
					SourceChannel:    "t2",
					PrimaryDeviceKey: "dev2",
					PrimaryPointCode: "temp_c",
				},
			},
		},
		{
			name:     "sampling interval below 100ms",
			policies: []GatewaySamplingPolicy{{DeviceKey: "dev1", SampleIntervalMS: 50, PublishIntervalMS: 1000}},
		},
		{
			name:     "publish interval below sample interval",
			policies: []GatewaySamplingPolicy{{DeviceKey: "dev1", SampleIntervalMS: 5000, PublishIntervalMS: 1000}},
		},
		{
			name: "duplicate wildcard policy empty vs star",
			policies: []GatewaySamplingPolicy{
				{DeviceKey: "dev1", PointCode: "", SampleIntervalMS: 1000, PublishIntervalMS: 5000},
				{DeviceKey: "dev1", PointCode: "*", SampleIntervalMS: 2000, PublishIntervalMS: 5000},
			},
		},
		{
			name:  "protected concentrator UID key",
			units: map[string]ConcentratorUnitShadow{"31": {Params: sampleShadowParams(1, 20)}},
		},
		{
			name:  "non-canonical unit key",
			units: map[string]ConcentratorUnitShadow{"01": {Params: sampleShadowParams(1, 20)}},
		},
		{
			name:  "protected modbusId in params",
			units: map[string]ConcentratorUnitShadow{"1": {Params: sampleShadowParams(5, 20)}},
		},
		{
			name: "duplicate non-zero EID",
			units: map[string]ConcentratorUnitShadow{
				"1": {EIDs: []ConcentratorShadowEIDSlot{{Slot: 1, EID: 12345}, {Slot: 2, EID: 12345}}},
			},
		},
		{
			name: "duplicate EID slot",
			units: map[string]ConcentratorUnitShadow{
				"1": {EIDs: []ConcentratorShadowEIDSlot{{Slot: 1, EID: 111}, {Slot: 1, EID: 222}}},
			},
		},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, _, _, _, err := normalizeAndValidateDesiredShadow(tc.mappings, tc.policies, tc.units); !errors.Is(err, ErrConfigShadowInvalid) {
				t.Fatalf("%s: expected ErrConfigShadowInvalid, got %v", tc.name, err)
			}
		})
	}
}

func TestConfigShadowDriftCanonicalizationAndStatus(t *testing.T) {
	desired := &GatewayDesiredConfig{
		DesiredRevision: 3,
		PointMappings: []GatewayPointMapping{
			{
				SourceChannel:    "t1",
				PrimaryDeviceKey: "dev-a",
				PrimaryPointCode: "temp_a",
				CopyTargets: []PointCopyTarget{
					{DeviceKey: "dev-z", PointCode: "z_temp"},
					{DeviceKey: "dev-b", PointCode: "b_temp"},
				},
			},
			{SourceChannel: "t2", PrimaryDeviceKey: "dev-c", PrimaryPointCode: "temp_c"},
		},
		SamplingPolicies: []GatewaySamplingPolicy{
			{DeviceKey: "dev-a", PointCode: "", SampleIntervalMS: 1000, PublishIntervalMS: 5000},
			{DeviceKey: "dev-c", PointCode: "temp_c", SampleIntervalMS: 2000, PublishIntervalMS: 10000},
		},
		ConcentratorConfigs: map[string]ConcentratorUnitShadow{
			"20": {
				Params: sampleShadowParams(20, 22),
				EIDs:   []ConcentratorShadowEIDSlot{{Slot: 5, EID: 5005}, {Slot: 1, EID: 1001}},
			},
		},
	}

	// Reordered arrays, "" vs "*", and explicit zero EID slots must still produce zero drift and SYNCED.
	reported := &GatewayReportedConfig{
		ReportedRevision: 3,
		AppliedStatus:    AppliedStatusApplied,
		PointMappings: []GatewayPointMapping{
			{SourceChannel: "t2", PrimaryDeviceKey: "dev-c", PrimaryPointCode: "temp_c"},
			{
				SourceChannel:    "t1",
				PrimaryDeviceKey: "dev-a",
				PrimaryPointCode: "temp_a",
				CopyTargets: []PointCopyTarget{
					{DeviceKey: "dev-b", PointCode: "b_temp"},
					{DeviceKey: "dev-z", PointCode: "z_temp"},
				},
			},
		},
		SamplingPolicies: []GatewaySamplingPolicy{
			{DeviceKey: "dev-c", PointCode: "temp_c", SampleIntervalMS: 2000, PublishIntervalMS: 10000},
			{DeviceKey: "dev-a", PointCode: "*", SampleIntervalMS: 1000, PublishIntervalMS: 5000},
		},
		ConcentratorConfigs: map[string]ConcentratorUnitShadow{
			"20": {
				Params: sampleShadowParams(20, 22),
				EIDs:   []ConcentratorShadowEIDSlot{{Slot: 1, EID: 1001}, {Slot: 2, EID: 0}, {Slot: 5, EID: 5005}},
			},
		},
	}
	shadowSynced := buildGatewayConfigShadow("0194f000-1000-7000-8000-000000000001", "0194f000-3000-7000-8000-000000000001", desired, reported)
	if shadowSynced.SyncStatus != ShadowSyncStatusSynced || len(shadowSynced.Drift) != 0 {
		t.Fatalf("expected SYNCED with 0 drift, got status=%s drift=%+v", shadowSynced.SyncStatus, shadowSynced.Drift)
	}

	// Mutate edge state -> VALUE_MISMATCH + MISSING_ON_EDGE + UNEXPECTED_ON_EDGE -> DRIFTED.
	driftedReported := &GatewayReportedConfig{
		ReportedRevision: 3,
		AppliedStatus:    AppliedStatusApplied,
		PointMappings: []GatewayPointMapping{
			{SourceChannel: "t1", PrimaryDeviceKey: "dev-a", PrimaryPointCode: "temp_a"},
			{SourceChannel: "t9", PrimaryDeviceKey: "dev-x", PrimaryPointCode: "temp_x"},
		},
		SamplingPolicies: []GatewaySamplingPolicy{
			{DeviceKey: "dev-a", PointCode: "*", SampleIntervalMS: 1000, PublishIntervalMS: 15000},
		},
		ConcentratorConfigs: map[string]ConcentratorUnitShadow{
			"20": {Params: sampleShadowParams(20, 14)},
		},
	}
	shadowDrifted := buildGatewayConfigShadow("0194f000-1000-7000-8000-000000000001", "0194f000-3000-7000-8000-000000000001", desired, driftedReported)
	if shadowDrifted.SyncStatus != ShadowSyncStatusDrifted || len(shadowDrifted.Drift) < 5 {
		t.Fatalf("expected DRIFTED with detailed drift entries, got status=%s drift=%+v", shadowDrifted.SyncStatus, shadowDrifted.Drift)
	}

	// Higher desired revision -> PENDING_SYNC.
	desiredAhead := *desired
	desiredAhead.DesiredRevision = 4
	shadowPending := buildGatewayConfigShadow("0194f000-1000-7000-8000-000000000001", "0194f000-3000-7000-8000-000000000001", &desiredAhead, reported)
	if shadowPending.SyncStatus != ShadowSyncStatusPendingSync {
		t.Fatalf("expected PENDING_SYNC, got %s", shadowPending.SyncStatus)
	}

	// Unconfigured when both nil.
	shadowEmpty := buildGatewayConfigShadow("0194f000-1000-7000-8000-000000000001", "0194f000-3000-7000-8000-000000000001", nil, nil)
	if shadowEmpty.SyncStatus != ShadowSyncStatusUnconfigured {
		t.Fatalf("expected UNCONFIGURED, got %s", shadowEmpty.SyncStatus)
	}
}

type fakeConfigShadowStore struct {
	shadow     GatewayConfigShadow
	pullResp   GatewayShadowPullResponse
	updateErr  error
	pullErr    error
	reportErr  error
	lastSince  int64
	lastReport *GatewayShadowReportInput
	lastUpdate *UpdateDesiredConfigInput
}

func (f *fakeConfigShadowStore) GetGatewayConfigShadowForOperator(_ context.Context, _ registryauth.GrantClaims, _ string) (GatewayConfigShadow, error) {
	return f.shadow, nil
}

func (f *fakeConfigShadowStore) UpdateGatewayDesiredConfig(_ context.Context, _ registryauth.GrantClaims, _ string, input UpdateDesiredConfigInput) (GatewayConfigShadow, error) {
	f.lastUpdate = &input
	if f.updateErr != nil {
		return GatewayConfigShadow{}, f.updateErr
	}
	return f.shadow, nil
}

func (f *fakeConfigShadowStore) PullGatewayDesiredConfig(_ context.Context, _ string, sinceRevision int64) (GatewayShadowPullResponse, error) {
	f.lastSince = sinceRevision
	if f.pullErr != nil {
		return GatewayShadowPullResponse{}, f.pullErr
	}
	return f.pullResp, nil
}

func (f *fakeConfigShadowStore) ReportGatewayAppliedConfig(_ context.Context, _ string, input GatewayShadowReportInput) (GatewayConfigShadow, error) {
	f.lastReport = &input
	if f.reportErr != nil {
		return GatewayConfigShadow{}, f.reportErr
	}
	return f.shadow, nil
}

type fakePeerVerifier struct {
	gatewayID string
	err       error
}

func (v fakePeerVerifier) VerifyActiveCredential(_ context.Context, _ *x509.Certificate) (string, error) {
	if v.err != nil {
		return "", v.err
	}
	return v.gatewayID, nil
}

func TestConfigShadowHTTPHandlersAndSPIFFEAuth(t *testing.T) {
	const gwID = "0194f000-3000-7000-8000-000000000001"
	const tenantID = "0194f000-1000-7000-8000-000000000001"
	store := &fakeConfigShadowStore{
		shadow: GatewayConfigShadow{
			TenantID:   tenantID,
			GatewayID:  gwID,
			SyncStatus: ShadowSyncStatusPendingSync,
			Drift:      []DriftDetail{},
		},
		pullResp: GatewayShadowPullResponse{
			GatewayID:       gwID,
			DesiredRevision: 2,
			HasDelta:        true,
			Desired: &GatewayDesiredConfig{
				DesiredRevision: 2,
				PointMappings:   []GatewayPointMapping{{SourceChannel: "t1", PrimaryDeviceKey: "chiller-1", PrimaryPointCode: "chw_temp"}},
			},
		},
	}
	var updatedHookID string
	shadowHandler := NewConfigShadowHandler(store, fakePeerVerifier{gatewayID: gwID}, func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
		return registryauth.GrantClaims{TenantID: tenantID, PrincipalID: "0194f000-4000-7000-8000-000000000001"}, nil
	}, func(gatewayID string) {
		updatedHookID = gatewayID
	})
	handler := NewCredentialHandlerWithShadow(nil, nil, nil, nil, shadowHandler)

	gwURI, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	gwPeer := &x509.Certificate{URIs: []*url.URL{gwURI}}

	// Without SPIFFE client cert -> 401.
	unauthReq := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/"+gwID+"/config-shadow", nil)
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without SPIFFE TLS, got %d", unauthRec.Code)
	}

	// Operator PUT triggers onDesiredUpdated and returns 200.
	putReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+gwID+"/config-shadow", bytes.NewBufferString(`{"pointMappings":[],"samplingPolicies":[],"concentratorConfigs":{},"reason":"sync test"}`))
	putReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK || updatedHookID != gwID {
		t.Fatalf("PUT status=%d hook=%q body=%s", putRec.Code, updatedHookID, putRec.Body.String())
	}

	// Revision conflict maps to 409 CONFIG_SHADOW_REVISION_CONFLICT.
	store.updateErr = ErrConfigShadowConflict
	conflictReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+gwID+"/config-shadow", bytes.NewBufferString(`{"expectedDesiredRevision":1,"reason":"stale"}`))
	conflictReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	conflictRec := httptest.NewRecorder()
	handler.ServeHTTP(conflictRec, conflictReq)
	if conflictRec.Code != http.StatusConflict || !strings.Contains(conflictRec.Body.String(), "CONFIG_SHADOW_REVISION_CONFLICT") {
		t.Fatalf("expected 409 CONFIG_SHADOW_REVISION_CONFLICT, got %d %s", conflictRec.Code, conflictRec.Body.String())
	}
	store.updateErr = nil

	// Gateway pull requires X-Gateway-Client-Cert.
	noCertPull := httptest.NewRequest(http.MethodGet, "/gateway/v1/config-shadow?sinceRevision=1", nil)
	noCertPull.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	noCertRec := httptest.NewRecorder()
	handler.ServeHTTP(noCertRec, noCertPull)
	if noCertRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on missing X-Gateway-Client-Cert, got %d", noCertRec.Code)
	}

	// Valid gateway cert pulls delta with sinceRevision=1.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: gwID},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: gwID},
	}, &key.PublicKey, key)
	escapedCert := url.QueryEscape(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))

	pullReq := httptest.NewRequest(http.MethodGet, "/gateway/v1/config-shadow?sinceRevision=1", nil)
	pullReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	pullReq.Header.Set("X-Gateway-Client-Cert", escapedCert)
	pullRec := httptest.NewRecorder()
	handler.ServeHTTP(pullRec, pullReq)
	if pullRec.Code != http.StatusOK || store.lastSince != 1 {
		t.Fatalf("pull status=%d since=%d body=%s", pullRec.Code, store.lastSince, pullRec.Body.String())
	}
	var gotPull GatewayShadowPullResponse
	if err := json.Unmarshal(pullRec.Body.Bytes(), &gotPull); err != nil || !gotPull.HasDelta || gotPull.DesiredRevision != 2 {
		t.Fatalf("unexpected pull payload: %+v err=%v", gotPull, err)
	}

	// Gateway report submits applied config.
	reportReq := httptest.NewRequest(http.MethodPost, "/gateway/v1/config-shadow/report", bytes.NewBufferString(`{"reportedRevision":2,"appliedStatus":"APPLIED","pointMappings":[],"samplingPolicies":[],"concentratorConfigs":{}}`))
	reportReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	reportReq.Header.Set("X-Gateway-Client-Cert", escapedCert)
	reportRec := httptest.NewRecorder()
	handler.ServeHTTP(reportRec, reportReq)
	if reportRec.Code != http.StatusOK || store.lastReport == nil || store.lastReport.ReportedRevision != 2 {
		t.Fatalf("report status=%d report=%+v body=%s", reportRec.Code, store.lastReport, reportRec.Body.String())
	}

	// Internal errors must not leak raw upstream error messages.
	store.updateErr = errors.New("pq: secret dsn postgres://user:pass@db/hvac failed")
	internalErrReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+gwID+"/config-shadow", bytes.NewBufferString(`{"reason":"mask check"}`))
	internalErrReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	internalErrRec := httptest.NewRecorder()
	handler.ServeHTTP(internalErrRec, internalErrReq)
	if internalErrRec.Code != http.StatusInternalServerError || strings.Contains(internalErrRec.Body.String(), "secret") || !strings.Contains(internalErrRec.Body.String(), "gateway config shadow service unavailable") {
		t.Fatalf("expected masked 500 error, got %d body=%s", internalErrRec.Code, internalErrRec.Body.String())
	}
	store.updateErr = nil
}

func TestConfigShadowJSONBTextEstimateAndDriftBounds(t *testing.T) {
	// Near-131072 compact JSON that exceeds 131072 once PostgreSQL jsonb::text adds spaces after ':' and ','.
	var nearLimit []GatewaySamplingPolicy
	for i := 0; len(nearLimit) < 2000; i++ {
		nearLimit = append(nearLimit, GatewaySamplingPolicy{
			DeviceKey:         "dev-padding-" + strings.Repeat("x", 10),
			PointCode:         "p",
			SampleIntervalMS:  1000 + i,
			PublishIntervalMS: 5000 + i,
		})
		compact, _ := json.Marshal(nearLimit)
		if len(compact) <= maxSamplingPoliciesJSONBytes && estimatedPostgresJSONBTextBytes(compact) > maxSamplingPoliciesJSONBytes {
			break
		}
	}
	compact, _ := json.Marshal(nearLimit)
	if len(compact) > maxSamplingPoliciesJSONBytes || estimatedPostgresJSONBTextBytes(compact) <= maxSamplingPoliciesJSONBytes {
		t.Fatalf("failed to construct boundary payload: compact=%d est=%d", len(compact), estimatedPostgresJSONBTextBytes(compact))
	}
	if _, _, _, err := marshalShadowSections(nil, nearLimit, nil); !errors.Is(err, ErrConfigShadowInvalid) {
		t.Fatalf("expected jsonb::text limit rejection, got %v", err)
	}

	// Drift details must reject empty detail or >256 target / >512 detail.
	if _, _, err := validateReportDriftDetails([]DriftDetail{{
		Section: DriftSectionPointMappings,
		Target:  "dev/p",
		Reason:  DriftReasonEdgeApplyFailed,
		Detail:  "   ",
	}}); !errors.Is(err, ErrConfigShadowInvalid) {
		t.Fatalf("expected empty drift detail rejection, got %v", err)
	}
	if _, _, err := validateReportDriftDetails([]DriftDetail{{
		Section: DriftSectionPointMappings,
		Target:  strings.Repeat("a", 257),
		Reason:  DriftReasonEdgeApplyFailed,
		Detail:  "too long target",
	}}); !errors.Is(err, ErrConfigShadowInvalid) {
		t.Fatalf("expected >256 target rejection, got %v", err)
	}

	// Computed drift clamps 257-char target and >512-char detail within schema limits.
	dev128 := strings.Repeat("d", 128)
	pt128 := "p_" + strings.Repeat("a", 126)
	longDesired := &GatewayDesiredConfig{
		DesiredRevision: 1,
		PointMappings:   []GatewayPointMapping{{SourceChannel: strings.Repeat("c", 128), PrimaryDeviceKey: dev128, PrimaryPointCode: pt128}},
	}
	drift := ComputeConfigShadowDrift(longDesired, &GatewayReportedConfig{ReportedRevision: 1})
	if len(drift) != 1 || len(drift[0].Target) > maxDriftTargetRunes || len(drift[0].Detail) > maxDriftDetailRunes {
		t.Fatalf("unexpected drift lengths: %+v", drift)
	}
}
