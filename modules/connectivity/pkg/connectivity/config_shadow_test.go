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
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
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
	if _, err := normalizeAndValidateDesiredShadow(validMappings, validPolicies, validUnits, nil, nil, nil, nil, nil, nil); err != nil {
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
			if _, err := normalizeAndValidateDesiredShadow(tc.mappings, tc.policies, tc.units, nil, nil, nil, nil, nil, nil); !errors.Is(err, ErrConfigShadowInvalid) {
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
	shadow             GatewayConfigShadow
	pullResp           GatewayShadowPullResponse
	updateErr          error
	pullErr            error
	reportErr          error
	lastSince          int64
	lastReport         *GatewayShadowReportInput
	lastUpdate         *UpdateDesiredConfigInput
	lastTemplateUpsert *TemplateDriverUpsertInput
	templateUpsertRes  TemplateDriverUpsertResult
	templateUpsertErr  error
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

func (f *fakeConfigShadowStore) UpsertGatewayTemplateDriverConfig(_ context.Context, _ string, input TemplateDriverUpsertInput) (TemplateDriverUpsertResult, error) {
	f.lastTemplateUpsert = &input
	if f.templateUpsertErr != nil {
		return TemplateDriverUpsertResult{}, f.templateUpsertErr
	}
	res := f.templateUpsertRes
	if res.Shadow.GatewayID == "" {
		res.Shadow = f.shadow
	}
	return res, nil
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
	handler := NewCredentialHandler(nil, nil, WithConfigShadowHandler(shadowHandler))

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
	if store.lastUpdate == nil || store.lastUpdate.AutonomyPolicy.IsSet() || store.lastUpdate.DriverConfigs != nil {
		t.Fatalf("expected omitted driverConfigs and autonomyPolicy on partial PUT, got %+v", store.lastUpdate)
	}

	// Operator PUT with explicit autonomyPolicy:null sets IsSet=true, Value=nil; unknown field inside autonomyPolicy returns 400.
	clearAutonomyReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+gwID+"/config-shadow", bytes.NewBufferString(`{"driverConfigs":[],"autonomyPolicy":null,"reason":"clear autonomy"}`))
	clearAutonomyReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	clearAutonomyRec := httptest.NewRecorder()
	handler.ServeHTTP(clearAutonomyRec, clearAutonomyReq)
	if clearAutonomyRec.Code != http.StatusOK || store.lastUpdate == nil || store.lastUpdate.DriverConfigs == nil || len(*store.lastUpdate.DriverConfigs) != 0 || !store.lastUpdate.AutonomyPolicy.IsSet() || store.lastUpdate.AutonomyPolicy.Value != nil {
		t.Fatalf("clear PUT status=%d lastUpdate=%+v body=%s", clearAutonomyRec.Code, store.lastUpdate, clearAutonomyRec.Body.String())
	}

	badUnknownReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+gwID+"/config-shadow", bytes.NewBufferString(`{"autonomyPolicy":{"enabled":true,"bogus":1},"reason":"reject unknown"}`))
	badUnknownReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	badUnknownRec := httptest.NewRecorder()
	handler.ServeHTTP(badUnknownRec, badUnknownReq)
	if badUnknownRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on unknown field inside autonomyPolicy, got %d body=%s", badUnknownRec.Code, badUnknownRec.Body.String())
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
	if _, err := marshalShadowSections(nil, nearLimit, nil, nil, nil, nil, nil, nil, nil, nil); !errors.Is(err, ErrConfigShadowInvalid) {
		t.Fatalf("expected jsonb::text limit rejection, got %v", err)
	}

	// Near-262144 driverConfigs compact JSON that exceeds 262144 once PostgreSQL jsonb::text adds spaces.
	var nearDrivers []GatewayModbusDriverConfig
	for i := 0; len(nearDrivers) < 2000; i++ {
		nearDrivers = append(nearDrivers, GatewayModbusDriverConfig{
			DeviceKey:  "dev-driver-" + strings.Repeat("d", 30) + "-" + string(rune('a'+i/26)) + string(rune('a'+i%26)),
			DriverType: "MODBUS_TCP",
			Endpoint:   "192.168.100.10:502",
			UnitID:     1,
			TimeoutMS:  1000,
			Retries:    1,
			Registers: []GatewayModbusRegisterMapping{{
				PointCode:     "p_a",
				FunctionCode:  3,
				Address:       1,
				RegisterCount: 1,
				RawType:       "UINT16",
				ByteOrder:     "BIG_ENDIAN",
				WordOrder:     "BIG_ENDIAN",
				Scale:         1,
				Offset:        0,
				PollPriority:  "HIGH",
			}},
		})
		compactDrivers, _ := json.Marshal(nearDrivers)
		if len(compactDrivers) <= maxDriverConfigsJSONBytes && estimatedPostgresJSONBTextBytes(compactDrivers) > maxDriverConfigsJSONBytes {
			break
		}
	}
	compactDrivers, _ := json.Marshal(nearDrivers)
	if len(compactDrivers) > maxDriverConfigsJSONBytes || estimatedPostgresJSONBTextBytes(compactDrivers) <= maxDriverConfigsJSONBytes {
		t.Fatalf("failed to construct driverConfigs boundary payload: compact=%d est=%d", len(compactDrivers), estimatedPostgresJSONBTextBytes(compactDrivers))
	}
	if _, err := marshalShadowSections(nil, nil, nil, nearDrivers, nil, nil, nil, nil, nil, nil); !errors.Is(err, ErrConfigShadowInvalid) {
		t.Fatalf("expected driverConfigs jsonb::text limit rejection, got %v", err)
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

func intPtr(v int) *int {
	return &v
}

func sampleValidAutonomyPolicy() *GatewayAutonomyPolicy {
	return &GatewayAutonomyPolicy{
		Enabled:                            true,
		OfflineGraceSeconds:                120,
		DefaultChilledWaterSupplySetpointC: 7.5,
		DefaultPumpFrequencyHz:             38.0,
		MinPumpFrequencyHz:                 25.0,
		MaxPumpFrequencyHz:                 48.0,
		MinRunSeconds:                      300,
		MinStopSeconds:                     180,
		SnapshotIntervalSeconds:            60,
	}
}

func sampleValidDriverConfigs() []GatewayModbusDriverConfig {
	return []GatewayModbusDriverConfig{
		{
			DeviceKey:         "vfd-1",
			DriverType:        "MODBUS_RTU",
			Endpoint:          "/dev/ttyUSB0",
			UnitID:            3,
			CapabilityProfile: "VARIABLE_SPEED_PUMP",
			TimeoutMS:         1500,
			Retries:           2,
			Registers: []GatewayModbusRegisterMapping{
				{
					PointCode:         "run_status",
					SemanticChannel:   "StartCommand",
					FunctionCode:      3,
					WriteFunctionCode: intPtr(6),
					Address:           8501,
					RegisterCount:     1,
					RawType:           "BIT_BOOL",
					ByteOrder:         "BIG_ENDIAN",
					WordOrder:         "BIG_ENDIAN",
					BitIndex:          intPtr(0),
					Scale:             1,
					Offset:            0,
					PollPriority:      "VERY_HIGH",
				},
				{
					PointCode:       "drive_mode",
					SemanticChannel: "RunState",
					FunctionCode:    3,
					Address:         8502,
					RegisterCount:   1,
					RawType:         "ENUM16",
					ByteOrder:       "BIG_ENDIAN",
					WordOrder:       "BIG_ENDIAN",
					Scale:           1,
					Offset:          0,
					PollPriority:    "HIGH",
					EnumMap:         map[string]string{"0": "STOP", "1": "AUTO"},
				},
			},
		},
		{
			DeviceKey:  "chiller-1",
			DriverType: "MODBUS_TCP",
			Endpoint:   "192.168.10.21:502",
			UnitID:     1,
			TimeoutMS:  2000,
			Retries:    3,
			Registers: []GatewayModbusRegisterMapping{
				{
					PointCode:         "chw_supply_temp",
					FunctionCode:      3,
					WriteFunctionCode: intPtr(16),
					Address:           100,
					RegisterCount:     2,
					RawType:           "FLOAT32",
					ByteOrder:         "BIG_ENDIAN",
					WordOrder:         "LITTLE_ENDIAN",
					Scale:             0.1,
					Offset:            -0.5,
					PollPriority:      "MEDIUM",
				},
				{
					PointCode:     "alarm_code",
					FunctionCode:  3,
					Address:       0,
					RegisterCount: 1,
					RawType:       "UINT16",
					ByteOrder:     "BIG_ENDIAN",
					WordOrder:     "BIG_ENDIAN",
					Scale:         1,
					Offset:        0,
					PollPriority:  "LOW",
				},
			},
		},
	}
}

func TestConfigShadowDriverAndAutonomyValidationAndCanonicalization(t *testing.T) {
	drivers := sampleValidDriverConfigs()
	autonomy := sampleValidAutonomyPolicy()

	validated, err := normalizeAndValidateDesiredShadow(nil, nil, nil, drivers, autonomy, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("expected valid driverConfigs + autonomyPolicy to pass, got %v", err)
	}
	if len(validated.drivers) != 2 || validated.drivers[0].DeviceKey != "chiller-1" || validated.drivers[1].DeviceKey != "vfd-1" {
		t.Fatalf("expected drivers sorted by deviceKey ascending, got %+v", validated.drivers)
	}
	if validated.drivers[0].Registers[0].PointCode != "alarm_code" || validated.drivers[0].Registers[1].PointCode != "chw_supply_temp" {
		t.Fatalf("expected chiller-1 registers sorted by pointCode ascending, got %+v", validated.drivers[0].Registers)
	}
	if validated.drivers[1].Registers[0].PointCode != "drive_mode" || validated.drivers[1].Registers[1].PointCode != "run_status" {
		t.Fatalf("expected vfd-1 registers sorted by pointCode ascending, got %+v", validated.drivers[1].Registers)
	}
	if validated.drivers[1].Registers[1].BitIndex == nil || *validated.drivers[1].Registers[1].BitIndex != 0 {
		t.Fatalf("expected bitIndex=0 preserved, got %+v", validated.drivers[1].Registers[1].BitIndex)
	}

	// Ensure bitIndex=0 survives JSON marshaling (pointer, not dropped by omitempty).
	encodedReg, err := json.Marshal(validated.drivers[1].Registers[1])
	if err != nil || !strings.Contains(string(encodedReg), `"bitIndex":0`) {
		t.Fatalf("expected bitIndex:0 in marshaled register JSON, got %s err=%v", string(encodedReg), err)
	}

	badDriverCases := []struct {
		name    string
		drivers []GatewayModbusDriverConfig
	}{
		{
			name: "duplicate deviceKey",
			drivers: []GatewayModbusDriverConfig{
				{
					DeviceKey: "chiller-1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
					Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
				},
				{
					DeviceKey: " chiller-1 ", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.2:502", UnitID: 2, TimeoutMS: 1000, Retries: 0,
					Registers: []GatewayModbusRegisterMapping{{PointCode: "p2", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
				},
			},
		},
		{
			name: "invalid driverType",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "BACNET_IP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "empty endpoint",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: " ", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "unitId out of range",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 248, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "timeoutMs out of range",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 99, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "retries out of range",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 11,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "capabilityProfile too long",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, CapabilityProfile: strings.Repeat("c", 65), TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "empty registers",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{},
			}},
		},
		{
			name: "duplicate pointCode",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{
					{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"},
					{PointCode: "p1", FunctionCode: 4, Address: 2, RegisterCount: 1, RawType: "INT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"},
				},
			}},
		},
		{
			name: "invalid writeFunctionCode",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, WriteFunctionCode: intPtr(5), Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "registerCount mismatch for FLOAT32",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "FLOAT32", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "registerCount mismatch for UINT16",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 2, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "BIT_BOOL missing bitIndex",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "BIT_BOOL", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "BIT_BOOL bitIndex out of range",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "BIT_BOOL", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", BitIndex: intPtr(16), Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "non-BIT_BOOL with bitIndex",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", BitIndex: intPtr(0), Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "ENUM16 missing enumMap",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "ENUM16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "scale zero",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "scale NaN",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: math.NaN(), Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "offset Inf",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: math.Inf(1), PollPriority: "HIGH"}},
			}},
		},
		{
			name: "FC4 with writeFunctionCode",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 4, WriteFunctionCode: intPtr(16), Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "FC6 on registerCount 2",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, WriteFunctionCode: intPtr(6), Address: 1, RegisterCount: 2, RawType: "FLOAT32", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "address plus registerCount exceeds 65536",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 65535, RegisterCount: 2, RawType: "FLOAT32", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "non-uint16 enumMap key",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "ENUM16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH", EnumMap: map[string]string{"01": "RUN"}}},
			}},
		},
		{
			name: "invalid TCP endpoint",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:0", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "unknown capabilityProfile",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, CapabilityProfile: "vfd.atv630.v1", TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{{PointCode: "p1", FunctionCode: 3, Address: 1, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH"}},
			}},
		},
		{
			name: "duplicate BIT_BOOL bit mapping",
			drivers: []GatewayModbusDriverConfig{{
				DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0,
				Registers: []GatewayModbusRegisterMapping{
					{PointCode: "p1", FunctionCode: 3, Address: 10, RegisterCount: 1, RawType: "BIT_BOOL", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", BitIndex: intPtr(2), Scale: 1, Offset: 0, PollPriority: "HIGH"},
					{PointCode: "p2", FunctionCode: 3, Address: 10, RegisterCount: 1, RawType: "BIT_BOOL", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", BitIndex: intPtr(2), Scale: 1, Offset: 0, PollPriority: "HIGH"},
				},
			}},
		},
	}

	for _, tc := range badDriverCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateDriverConfigs(tc.drivers); !errors.Is(err, ErrConfigShadowInvalid) {
				t.Fatalf("%s: expected ErrConfigShadowInvalid, got %v", tc.name, err)
			}
		})
	}

	// Over 256 registers rejected.
	tooManyRegs := make([]GatewayModbusRegisterMapping, 257)
	for i := range tooManyRegs {
		tooManyRegs[i] = GatewayModbusRegisterMapping{
			PointCode:    "p_" + string(rune('a'+i/26)) + string(rune('a'+i%26)),
			FunctionCode: 3, Address: i, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "LOW",
		}
	}
	if _, err := validateDriverConfigs([]GatewayModbusDriverConfig{{
		DeviceKey: "d1", DriverType: "MODBUS_TCP", Endpoint: "10.0.0.1:502", UnitID: 1, TimeoutMS: 1000, Retries: 0, Registers: tooManyRegs,
	}}); !errors.Is(err, ErrConfigShadowInvalid) {
		t.Fatalf("expected >256 registers rejection, got %v", err)
	}

	// AutonomyPolicy invalid cases.
	badAutonomyCases := []struct {
		name   string
		mutate func(*GatewayAutonomyPolicy)
	}{
		{"offlineGrace too low", func(p *GatewayAutonomyPolicy) { p.OfflineGraceSeconds = 9 }},
		{"offlineGrace too high", func(p *GatewayAutonomyPolicy) { p.OfflineGraceSeconds = 3601 }},
		{"setpoint too low", func(p *GatewayAutonomyPolicy) { p.DefaultChilledWaterSupplySetpointC = 3.9 }},
		{"setpoint NaN", func(p *GatewayAutonomyPolicy) { p.DefaultChilledWaterSupplySetpointC = math.NaN() }},
		{"minPump > defaultPump", func(p *GatewayAutonomyPolicy) { p.MinPumpFrequencyHz = 40; p.DefaultPumpFrequencyHz = 35 }},
		{"defaultPump > maxPump", func(p *GatewayAutonomyPolicy) { p.DefaultPumpFrequencyHz = 49; p.MaxPumpFrequencyHz = 45 }},
		{"pump < 15", func(p *GatewayAutonomyPolicy) { p.MinPumpFrequencyHz = 14.9 }},
		{"minRunSeconds > 3600", func(p *GatewayAutonomyPolicy) { p.MinRunSeconds = 3601 }},
		{"minStopSeconds < 0", func(p *GatewayAutonomyPolicy) { p.MinStopSeconds = -1 }},
		{"snapshotIntervalSeconds < 30", func(p *GatewayAutonomyPolicy) { p.SnapshotIntervalSeconds = 29 }},
	}
	for _, tc := range badAutonomyCases {
		t.Run(tc.name, func(t *testing.T) {
			p := sampleValidAutonomyPolicy()
			tc.mutate(p)
			if _, err := validateAutonomyPolicy(p); !errors.Is(err, ErrConfigShadowInvalid) {
				t.Fatalf("%s: expected ErrConfigShadowInvalid, got %v", tc.name, err)
			}
		})
	}
}

func TestConfigShadowDriverAndAutonomyDriftAndTriStateJSON(t *testing.T) {
	desiredDrivers := sampleValidDriverConfigs()
	// Reverse driver order and register order in reported -> must produce ZERO drift.
	reportedDrivers := []GatewayModbusDriverConfig{
		{
			DeviceKey:  desiredDrivers[1].DeviceKey,
			DriverType: desiredDrivers[1].DriverType,
			Endpoint:   desiredDrivers[1].Endpoint,
			UnitID:     desiredDrivers[1].UnitID,
			TimeoutMS:  desiredDrivers[1].TimeoutMS,
			Retries:    desiredDrivers[1].Retries,
			Registers:  []GatewayModbusRegisterMapping{desiredDrivers[1].Registers[1], desiredDrivers[1].Registers[0]},
		},
		{
			DeviceKey:         desiredDrivers[0].DeviceKey,
			DriverType:        desiredDrivers[0].DriverType,
			Endpoint:          desiredDrivers[0].Endpoint,
			UnitID:            desiredDrivers[0].UnitID,
			CapabilityProfile: desiredDrivers[0].CapabilityProfile,
			TimeoutMS:         desiredDrivers[0].TimeoutMS,
			Retries:           desiredDrivers[0].Retries,
			Registers:         []GatewayModbusRegisterMapping{desiredDrivers[0].Registers[1], desiredDrivers[0].Registers[0]},
		},
	}

	desired := &GatewayDesiredConfig{
		DesiredRevision: 2,
		DriverConfigs:   desiredDrivers,
		AutonomyPolicy:  sampleValidAutonomyPolicy(),
	}
	reportedMatch := &GatewayReportedConfig{
		ReportedRevision: 2,
		AppliedStatus:    AppliedStatusApplied,
		DriverConfigs:    reportedDrivers,
		AutonomyPolicy:   sampleValidAutonomyPolicy(),
	}
	if drift := ComputeConfigShadowDrift(desired, reportedMatch); len(drift) != 0 {
		t.Fatalf("expected order-independent zero drift, got %+v", drift)
	}

	// Drift scenarios: missing chiller-1, value mismatch on vfd-1, unexpected extra-1, and autonomy mismatch.
	reportedMutatedDrivers := []GatewayModbusDriverConfig{
		{
			DeviceKey:  "vfd-1",
			DriverType: "MODBUS_RTU",
			Endpoint:   "/dev/ttyUSB1",
			UnitID:     3,
			TimeoutMS:  1500,
			Retries:    2,
			Registers:  desiredDrivers[0].Registers,
		},
		{
			DeviceKey:  "extra-1",
			DriverType: "MODBUS_TCP",
			Endpoint:   "10.0.0.9:502",
			UnitID:     9,
			TimeoutMS:  1000,
			Retries:    1,
			Registers:  desiredDrivers[1].Registers,
		},
	}
	mutatedAutonomy := sampleValidAutonomyPolicy()
	mutatedAutonomy.OfflineGraceSeconds = 240
	drift := ComputeConfigShadowDrift(desired, &GatewayReportedConfig{
		ReportedRevision: 2,
		AppliedStatus:    AppliedStatusApplied,
		DriverConfigs:    reportedMutatedDrivers,
		AutonomyPolicy:   mutatedAutonomy,
	})
	if len(drift) != 4 {
		t.Fatalf("expected 4 drift entries (missing, unexpected, driver mismatch, autonomy mismatch), got %+v", drift)
	}

	// Autonomy missing vs unexpected.
	if d := ComputeConfigShadowDrift(&GatewayDesiredConfig{AutonomyPolicy: sampleValidAutonomyPolicy()}, &GatewayReportedConfig{}); len(d) != 1 || d[0].Section != DriftSectionAutonomyPolicy || d[0].Target != "autonomyPolicy" || d[0].Reason != DriftReasonMissingOnEdge {
		t.Fatalf("unexpected autonomy missing drift: %+v", d)
	}
	if d := ComputeConfigShadowDrift(&GatewayDesiredConfig{}, &GatewayReportedConfig{AutonomyPolicy: sampleValidAutonomyPolicy()}); len(d) != 1 || d[0].Section != DriftSectionAutonomyPolicy || d[0].Target != "autonomyPolicy" || d[0].Reason != DriftReasonUnexpectedOnEdge {
		t.Fatalf("unexpected autonomy unexpected drift: %+v", d)
	}

	// Validate report driftDetails section whitelist includes DRIVER_CONFIGS & AUTONOMY_POLICY.
	if _, _, err := validateReportDriftDetails([]DriftDetail{
		{Section: DriftSectionDriverConfigs, Target: "chiller-1", Reason: DriftReasonEdgeApplyFailed, Detail: "dial timeout"},
		{Section: DriftSectionAutonomyPolicy, Target: "autonomyPolicy", Reason: DriftReasonEdgeApplyFailed, Detail: "policy invalid"},
	}); err != nil {
		t.Fatalf("expected DRIVER_CONFIGS & AUTONOMY_POLICY drift sections allowed, got %v", err)
	}

	// Tri-state OptionalAutonomyPolicy JSON decode + marshal round-trip.
	var omitted UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"reason":"omit"}`), &omitted); err != nil || omitted.AutonomyPolicy.IsSet() || !omitted.AutonomyPolicy.IsZero() {
		t.Fatalf("expected omitted autonomyPolicy, got %+v err=%v", omitted.AutonomyPolicy, err)
	}
	omittedBytes, _ := json.Marshal(omitted)
	if strings.Contains(string(omittedBytes), "autonomyPolicy") {
		t.Fatalf("omitted autonomyPolicy must not marshal key, got %s", string(omittedBytes))
	}

	var cleared UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"autonomyPolicy":null,"reason":"clear"}`), &cleared); err != nil || !cleared.AutonomyPolicy.IsSet() || cleared.AutonomyPolicy.Value != nil || cleared.AutonomyPolicy.IsZero() {
		t.Fatalf("expected explicit null autonomyPolicy, got %+v err=%v", cleared.AutonomyPolicy, err)
	}
	clearedBytes, _ := json.Marshal(cleared)
	if !strings.Contains(string(clearedBytes), `"autonomyPolicy":null`) {
		t.Fatalf("explicit null autonomyPolicy must marshal null, got %s", string(clearedBytes))
	}

	var replaced UpdateDesiredConfigInput
	payload, _ := json.Marshal(map[string]any{"autonomyPolicy": sampleValidAutonomyPolicy(), "reason": "set"})
	if err := json.Unmarshal(payload, &replaced); err != nil || !replaced.AutonomyPolicy.IsSet() || replaced.AutonomyPolicy.Value == nil || replaced.AutonomyPolicy.Value.OfflineGraceSeconds != 120 {
		t.Fatalf("expected object autonomyPolicy, got %+v err=%v", replaced.AutonomyPolicy, err)
	}

	var unknownField UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"autonomyPolicy":{"enabled":true,"extraField":1},"reason":"bad"}`), &unknownField); err == nil {
		t.Fatalf("expected unknown field inside autonomyPolicy to be rejected")
	}
}

func TestConfigShadowTemplateDriverUpsertHTTPAndSPIFFEACL(t *testing.T) {
	const gwID = "0194f000-3000-7000-8000-000000000001"
	const tenantID = "0194f000-1000-7000-8000-000000000001"

	store := &fakeConfigShadowStore{
		shadow: GatewayConfigShadow{
			TenantID:   tenantID,
			GatewayID:  gwID,
			SyncStatus: ShadowSyncStatusPendingSync,
			Drift:      []DriftDetail{},
		},
		templateUpsertRes: TemplateDriverUpsertResult{
			Changed: true,
			Skipped: false,
		},
	}
	var updatedCalls []string
	shadowHandler := NewConfigShadowHandler(store, fakePeerVerifier{gatewayID: gwID}, func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
		return registryauth.GrantClaims{}, ErrCredentialForbidden
	}, func(gatewayID string) {
		updatedCalls = append(updatedCalls, gatewayID)
	})
	handler := NewCredentialHandler(nil, nil, WithConfigShadowHandler(shadowHandler))

	coreURI, _ := url.Parse("spiffe://hvac.local/platform-core-service")
	corePeer := &x509.Certificate{URIs: []*url.URL{coreURI}}
	gwURI, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	gwPeer := &x509.Certificate{URIs: []*url.URL{gwURI}}

	body := `{"tenantId":"` + tenantID + `","deviceKey":"chiller-1","driverType":"MODBUS_TCP","endpoint":"10.0.0.5:502","unitId":1,"registers":[{"pointCode":"chw_temp","functionCode":3,"address":1,"registerCount":1,"rawType":"INT16","byteOrder":"BIG_ENDIAN","wordOrder":"BIG_ENDIAN","scale":0.1,"offset":0,"pollPriority":"HIGH"}],"reason":"template sync"}`

	reqChanged := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gwID+"/config-shadow:upsert-template-driver", strings.NewReader(body))
	reqChanged.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
	recChanged := httptest.NewRecorder()
	handler.ServeHTTP(recChanged, reqChanged)
	if recChanged.Code != http.StatusOK || len(updatedCalls) != 1 || updatedCalls[0] != gwID {
		t.Fatalf("Changed=true status=%d updatedCalls=%v body=%s", recChanged.Code, updatedCalls, recChanged.Body.String())
	}
	if store.lastTemplateUpsert == nil || store.lastTemplateUpsert.DeviceKey != "chiller-1" {
		t.Fatalf("unexpected captured upsert input: %+v", store.lastTemplateUpsert)
	}

	store.templateUpsertRes = TemplateDriverUpsertResult{Changed: false, Skipped: true}
	reqNoChange := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gwID+"/config-shadow:upsert-template-driver", strings.NewReader(body))
	reqNoChange.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
	recNoChange := httptest.NewRecorder()
	handler.ServeHTTP(recNoChange, reqNoChange)
	if recNoChange.Code != http.StatusOK || len(updatedCalls) != 1 {
		t.Fatalf("Skipped=true status=%d updatedCalls=%v body=%s", recNoChange.Code, updatedCalls, recNoChange.Body.String())
	}

	for _, blocked := range []struct {
		method, path string
	}{
		{http.MethodGet, "/internal/v1/gateways/" + gwID + "/config-shadow"},
		{http.MethodPut, "/internal/v1/gateways/" + gwID + "/config-shadow"},
		{http.MethodGet, "/gateway/v1/config-shadow"},
	} {
		req := httptest.NewRequest(blocked.method, blocked.path, nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for platform-core-service on %s %s, got %d", blocked.method, blocked.path, rec.Code)
		}
	}

	spoofReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gwID+"/config-shadow:upsert-template-driver", strings.NewReader(body))
	spoofReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
	spoofReq.Header.Set("X-Gateway-Client-Cert", "spoof")
	spoofRec := httptest.NewRecorder()
	handler.ServeHTTP(spoofRec, spoofReq)
	if spoofRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with X-Gateway-Client-Cert on internal route, got %d", spoofRec.Code)
	}

	gwDeniedReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gwID+"/config-shadow:upsert-template-driver", strings.NewReader(body))
	gwDeniedReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	gwDeniedRec := httptest.NewRecorder()
	handler.ServeHTTP(gwDeniedRec, gwDeniedReq)
	if gwDeniedRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for platform-gateway without grant on upsert route, got %d", gwDeniedRec.Code)
	}

	const tenantB = "0194f000-1000-7000-8000-000000000099"
	crossTenantHandler := NewConfigShadowHandler(store, fakePeerVerifier{gatewayID: gwID}, func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
		return registryauth.GrantClaims{TenantID: tenantB}, nil
	}, nil)
	crossTenantOuter := NewCredentialHandler(nil, nil, WithConfigShadowHandler(crossTenantHandler))
	crossReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gwID+"/config-shadow:upsert-template-driver", strings.NewReader(body))
	crossReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	crossRec := httptest.NewRecorder()
	crossTenantOuter.ServeHTTP(crossRec, crossReq)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant platform-gateway grant on upsert route, got %d (%s)", crossRec.Code, crossRec.Body.String())
	}
}

func TestConfigShadowMultiProtocolBACnetIPAndOPCUAValidationAndDrift(t *testing.T) {
	t.Parallel()

	devInst, writePrio := 204, 8
	multiDrivers := []GatewayModbusDriverConfig{
		{
			DeviceKey:  "ahu-bacnet-1",
			DriverType: "BACNET_IP",
			Endpoint:   "192.168.10.30:47808",
			TimeoutMS:  1200,
			Retries:    2,
			BACnet: &edgecontrol.BACnetDriverSpecInput{
				DeviceInstance: &devInst,
				WritePriority:  &writePrio,
				Objects: []edgecontrol.BACnetObjectInput{
					{PointCode: "sa_temp", ObjectType: "ANALOG_INPUT", ObjectInstance: 1, DataType: "DOUBLE", COVSubscribe: true, PollPriority: "HIGH"},
					{PointCode: "sa_sp", ObjectType: "ANALOG_OUTPUT", ObjectInstance: 2, DataType: "DOUBLE", Writable: true, PollPriority: "HIGH"},
				},
			},
		},
		{
			DeviceKey:  "bms-opcua-1",
			DriverType: "OPC_UA",
			Endpoint:   "opc.tcp://192.168.10.40:4840/plant",
			TimeoutMS:  1500,
			Retries:    1,
			OPCUA: &edgecontrol.OPCUADriverSpecInput{
				SecurityPolicy: edgecontrol.OPCUASecurityPolicyBasic256Sha256,
				SecurityMode:   edgecontrol.OPCUASecurityModeSignAndEncrypt,
				AuthMode:       edgecontrol.OPCUAAuthAnonymous,
				Nodes: []edgecontrol.OPCUANodeInput{
					{PointCode: "chw_flow", NodeID: "ns=2;i=10", DataType: "DOUBLE", Subscribe: true, PollPriority: "HIGH"},
				},
			},
		},
	}

	cleaned, err := validateDriverConfigs(multiDrivers)
	if err != nil || len(cleaned) != 2 {
		t.Fatalf("validateDriverConfigs mixed BACnet/OPC-UA failed: len=%d err=%v", len(cleaned), err)
	}
	if drift := ComputeConfigShadowDrift(
		&GatewayDesiredConfig{DriverConfigs: cleaned},
		&GatewayReportedConfig{DriverConfigs: []GatewayModbusDriverConfig{cleaned[1], cleaned[0]}},
	); len(drift) != 0 {
		t.Fatalf("expected zero order-independent multi-protocol drift, got %+v", drift)
	}

	// Reject EDGE-003 writable protection point in OPC-UA and input-only writable in BACnet/IP.
	if _, err := validateDriverConfigs([]GatewayModbusDriverConfig{{
		DeviceKey:  "bad-bac",
		DriverType: "BACNET_IP",
		Endpoint:   "192.168.10.30:47808",
		TimeoutMS:  1000,
		Retries:    1,
		BACnet: &edgecontrol.BACnetDriverSpecInput{
			DeviceInstance: &devInst,
			Objects:        []edgecontrol.BACnetObjectInput{{PointCode: "sa_temp", ObjectType: "ANALOG_INPUT", ObjectInstance: 1, Writable: true, PollPriority: "HIGH"}},
		},
	}}); err == nil {
		t.Fatal("expected read-only BACnet input object rejection")
	}

	if _, err := validateDriverConfigs([]GatewayModbusDriverConfig{{
		DeviceKey:  "bad-ua-safety",
		DriverType: "OPC_UA",
		Endpoint:   "opc.tcp://192.168.10.40:4840",
		TimeoutMS:  1000,
		Retries:    1,
		OPCUA: &edgecontrol.OPCUADriverSpecInput{
			SecurityPolicy: edgecontrol.OPCUASecurityPolicyNone,
			SecurityMode:   edgecontrol.OPCUASecurityModeNone,
			Nodes:          []edgecontrol.OPCUANodeInput{{PointCode: "plc_estop_cmd", SafetyBoundary: "HARD_REALTIME_SAFETY", NodeID: "ns=2;i=99", Writable: true, PollPriority: "VERY_HIGH"}},
		},
	}}); err == nil {
		t.Fatal("expected EDGE-003 hard real-time OPC-UA write rejection")
	}
}
