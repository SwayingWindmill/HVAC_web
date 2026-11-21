package simulator

import (
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
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func TestConfigShadowReconnectDeltaSyncAndPersistence(t *testing.T) {
	dir := t.TempDir()
	caPath, certPath, keyPath, serverCert := writeTestMTLSBundle(t, dir)

	var (
		mu             sync.Mutex
		pullQueries    []string
		reportedBodies []GatewayShadowReportRequest
		desired        = GatewayDesiredConfig{
			DesiredRevision: 2,
			PointMappings: []GatewayPointMapping{
				{
					SourceChannel:    "chwp-01:supply_temp",
					PrimaryDeviceKey: "chwp-01",
					PrimaryPointCode: "supply_temp_c",
					CopyTargets: []PointCopyTarget{
						{DeviceKey: "plant-header", PointCode: "chilled_supply_temp_c"},
					},
				},
			},
			SamplingPolicies: []GatewaySamplingPolicy{
				{
					DeviceKey:         "chwp-01",
					PointCode:         "supply_temp_c",
					SampleIntervalMS:  1000,
					PublishIntervalMS: 10000,
				},
			},
			ConcentratorConfigs: map[string]ConcentratorUnitShadow{
				"1": {
					Params: &ConcentratorShadowParams{
						ModbusID:        1,
						BaudRateCode:    3,
						OffLineTime:     30,
						DataUplinkCycle: 60,
						LocalNet:        12,
						TimeMark:        1,
						RfTxPower:       20,
						RfFrequency:     433,
						RfBandWidth:     1,
						RfSf:            9,
						RfLeadCode:      8,
						TxTimeout:       500,
						RxTimeout:       500,
						RssiOut:         1,
					},
					EIDs: []ConcentratorShadowEIDSlot{
						{Slot: 1, EID: 100201},
					},
				},
			},
		}
	)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/gateway/v1/config-shadow":
			assertValidGatewayShadowPoPHeaders(t, r, certPath)
			mu.Lock()
			sinceRaw := r.URL.Query().Get("sinceRevision")
			pullQueries = append(pullQueries, sinceRaw)
			resp := GatewayShadowPullResponse{
				GatewayID:       "0193f000-2000-7000-8000-000000000001",
				DesiredRevision: desired.DesiredRevision,
				HasDelta:        sinceRaw != "2",
			}
			if resp.HasDelta {
				cp := desired
				resp.Desired = &cp
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPost && r.URL.Path == "/gateway/v1/config-shadow/report":
			assertValidGatewayShadowPoPHeaders(t, r, certPath)
			var req GatewayShadowReportRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode shadow report failed: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			reportedBodies = append(reportedBodies, req)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"syncStatus":"SYNCED"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read test CA: %v", err)
	}
	clientCAPool := x509.NewCertPool()
	if !clientCAPool.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to parse test CA pool")
	}
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAPool,
	}
	srv.StartTLS()
	defer srv.Close()

	statePath := filepath.Join(dir, "queue", "config-shadow.v1.json")
	shadow := newEdgeConfigShadow(statePath)
	cfg := MQTTGatewayConfig{
		GatewayID:            "0193f000-2000-7000-8000-000000000001",
		EnrollmentURL:        srv.URL,
		EnrollmentCAFile:     caPath,
		EnrollmentServerName: "127.0.0.1",
		CertFile:             certPath,
		KeyFile:              keyPath,
		QueueDirectory:       filepath.Join(dir, "queue"),
	}

	if err := syncConfigShadowHTTP(context.Background(), cfg, shadow, nil); err != nil {
		t.Fatalf("first config shadow sync failed: %v", err)
	}

	reloaded := newEdgeConfigShadow(statePath)
	if err := reloaded.load(); err != nil {
		t.Fatalf("reload persisted config shadow failed: %v", err)
	}
	snap := reloaded.snapshot()
	if snap.AppliedRevision != 2 {
		t.Fatalf("expected persisted appliedRevision=2, got %d", snap.AppliedRevision)
	}
	if len(snap.PointMappings) != 1 || len(snap.PointMappings[0].CopyTargets) != 1 {
		t.Fatalf("unexpected persisted point mappings: %+v", snap.PointMappings)
	}
	if len(snap.ConcentratorConfigs) != 0 {
		t.Fatalf("expected ConcentratorConfigs empty when applyHook is nil, got %+v", snap.ConcentratorConfigs)
	}

	if err := syncConfigShadowHTTP(context.Background(), cfg, reloaded, nil); err != nil {
		t.Fatalf("second config shadow sync failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(pullQueries) != 2 || pullQueries[0] != "0" || pullQueries[1] != "2" {
		t.Fatalf("expected delta pull revisions [0, 2], got %v", pullQueries)
	}
	if len(reportedBodies) != 1 {
		t.Fatalf("expected exactly 1 report after convergence, got %d", len(reportedBodies))
	}
	if reportedBodies[0].ReportedRevision != 2 || reportedBodies[0].AppliedStatus != shadowAppliedStatusApplied || len(reportedBodies[0].ConcentratorConfigs) != 0 {
		t.Fatalf("unexpected reported payload: %+v", reportedBodies[0])
	}
}

func TestBuildEnvelopePointMappingCopiesAndSamplingThrottle(t *testing.T) {
	shadow := newEdgeConfigShadow("")
	_, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 1,
		PointMappings: []GatewayPointMapping{
			{
				SourceChannel:    "chwp-01:supply_temp",
				PrimaryDeviceKey: "chwp-01",
				PrimaryPointCode: "supply_temp_c",
				CopyTargets: []PointCopyTarget{
					{DeviceKey: "plant-header", PointCode: "chilled_supply_temp_c"},
				},
			},
		},
		SamplingPolicies: []GatewaySamplingPolicy{
			{
				DeviceKey:         "chwp-01",
				PointCode:         "supply_temp_c",
				SampleIntervalMS:  1000,
				PublishIntervalMS: 10000,
			},
			{
				DeviceKey:         "plant-header",
				PointCode:         "*",
				SampleIntervalMS:  1000,
				PublishIntervalMS: 10000,
			},
		},
		ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
	})
	if err != nil {
		t.Fatalf("applyDesired failed: %v", err)
	}

	publisher := &MQTTPublisher{
		gatewayID: "0193f000-2000-7000-8000-000000000001",
		pointByKey: map[string]PointConfig{
			pointReference("chwp-01", "supply_temp"): {
				DeviceID:     "chwp-01",
				TelemetryKey: "supply_temp",
				PointCode:    "supply_temp_c",
				Unit:         "degC",
			},
		},
		configShadow: shadow,
	}

	t0 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	env1, err := publisher.buildEnvelope([]Measurement{
		{
			DeviceID:     "chwp-01",
			TelemetryKey: "supply_temp",
			Value:        7.2,
			Quality:      "GOOD",
			ObservedAt:   t0,
			Sequence:     1,
		},
	})
	if err != nil {
		t.Fatalf("buildEnvelope t0 failed: %v", err)
	}
	if len(env1.Payload.Devices) != 2 {
		t.Fatalf("expected 2 devices (primary + COPIES target), got %+v", env1.Payload.Devices)
	}
	if env1.Payload.Devices[0].DeviceID != "chwp-01" || env1.Payload.Devices[0].Points[0].Code != "supply_temp_c" {
		t.Fatalf("unexpected primary device point: %+v", env1.Payload.Devices[0])
	}
	if env1.Payload.Devices[1].DeviceID != "plant-header" || env1.Payload.Devices[1].Points[0].Code != "chilled_supply_temp_c" || env1.Payload.Devices[1].Points[0].Value != 7.2 {
		t.Fatalf("unexpected copy target device point: %+v", env1.Payload.Devices[1])
	}

	envThrottled, err := publisher.buildEnvelope([]Measurement{
		{
			DeviceID:     "chwp-01",
			TelemetryKey: "supply_temp",
			Value:        7.4,
			Quality:      "GOOD",
			ObservedAt:   t0.Add(3 * time.Second),
			Sequence:     2,
		},
	})
	if err != nil {
		t.Fatalf("buildEnvelope throttled failed: %v", err)
	}
	if len(envThrottled.Payload.Devices) != 0 {
		t.Fatalf("expected measurement inside 10s publishIntervalMs to be throttled, got %+v", envThrottled.Payload.Devices)
	}

	env2, err := publisher.buildEnvelope([]Measurement{
		{
			DeviceID:     "chwp-01",
			TelemetryKey: "supply_temp",
			Value:        7.6,
			Quality:      "GOOD",
			ObservedAt:   t0.Add(11 * time.Second),
			Sequence:     3,
		},
	})
	if err != nil {
		t.Fatalf("buildEnvelope t0+11s failed: %v", err)
	}
	if len(env2.Payload.Devices) != 2 {
		t.Fatalf("expected 2 devices after publishIntervalMs elapsed, got %+v", env2.Payload.Devices)
	}
}

func TestConfigShadowRejectsProtectedConcentratorUID(t *testing.T) {
	shadow := newEdgeConfigShadow("")
	shadow.applyHook = func(context.Context, GatewayDesiredConfig) ([]ShadowDriftDetail, string, error) {
		return nil, "", nil
	}
	report, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision:  3,
		PointMappings:    []GatewayPointMapping{},
		SamplingPolicies: []GatewaySamplingPolicy{},
		ConcentratorConfigs: map[string]ConcentratorUnitShadow{
			"1": {
				EIDs: []ConcentratorShadowEIDSlot{{Slot: 1, EID: 42}},
			},
			"2": {
				EIDs: []ConcentratorShadowEIDSlot{{Slot: 1, EID: 99}},
			},
		},
	})
	if err != nil {
		t.Fatalf("applyDesired failed: %v", err)
	}
	if report.AppliedStatus != shadowAppliedStatusPartial {
		t.Fatalf("expected PARTIAL status for protected UID rejection, got %s", report.AppliedStatus)
	}
	if len(report.DriftDetails) != 1 || report.DriftDetails[0].Target != "unit:2" || report.DriftDetails[0].Reason != shadowDriftReasonApplyFailed {
		t.Fatalf("unexpected drift details: %+v", report.DriftDetails)
	}
	if _, exists := report.ConcentratorConfigs["2"]; exists {
		t.Fatalf("expected protected UID 2 to be excluded from applied concentrators")
	}
}

func TestConfigShadowFailedReportRetriesAndHookErrorMarksPartial(t *testing.T) {
	dir := t.TempDir()
	caPath, certPath, keyPath, serverCert := writeTestMTLSBundle(t, dir)

	var mu sync.Mutex
	var pullQueries []string
	reportStatus := http.StatusInternalServerError
	reportCount := 0
	desired := GatewayDesiredConfig{
		DesiredRevision:  2,
		PointMappings:    []GatewayPointMapping{},
		SamplingPolicies: []GatewaySamplingPolicy{},
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/gateway/v1/config-shadow":
			sinceRaw := r.URL.Query().Get("sinceRevision")
			pullQueries = append(pullQueries, sinceRaw)
			cp := desired
			_ = json.NewEncoder(w).Encode(GatewayShadowPullResponse{
				GatewayID:       "0193f000-2000-7000-8000-000000000001",
				DesiredRevision: desired.DesiredRevision,
				HasDelta:        sinceRaw != "2",
				Desired:         &cp,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/gateway/v1/config-shadow/report":
			reportCount++
			w.WriteHeader(reportStatus)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	caPEM, _ := os.ReadFile(caPath)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	srv.StartTLS()
	defer srv.Close()

	statePath := filepath.Join(dir, "queue", "config-shadow.v1.json")
	shadow := newEdgeConfigShadow(statePath)
	shadow.applyHook = func(context.Context, GatewayDesiredConfig) ([]ShadowDriftDetail, string, error) {
		return nil, "", context.DeadlineExceeded
	}
	cfg := MQTTGatewayConfig{
		GatewayID:            "0193f000-2000-7000-8000-000000000001",
		EnrollmentURL:        srv.URL,
		EnrollmentCAFile:     caPath,
		EnrollmentServerName: "127.0.0.1",
		CertFile:             certPath,
		KeyFile:              keyPath,
		QueueDirectory:       filepath.Join(dir, "queue"),
	}

	if err := syncConfigShadowHTTP(context.Background(), cfg, shadow, nil); err == nil {
		t.Fatal("expected error when POST /report returns 500")
	}
	if snap := shadow.snapshot(); snap.AppliedRevision != 0 {
		t.Fatalf("appliedRevision must remain 0 after failed POST /report, got %d", snap.AppliedRevision)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("persisted state must not be committed before POST /report 2xx, err=%v", err)
	}

	mu.Lock()
	reportStatus = http.StatusOK
	mu.Unlock()
	if err := syncConfigShadowHTTP(context.Background(), cfg, shadow, nil); err != nil {
		t.Fatalf("retry syncConfigShadowHTTP failed: %v", err)
	}
	if snap := shadow.snapshot(); snap.AppliedRevision != 2 {
		t.Fatalf("expected appliedRevision=2 after 2xx report, got %d", snap.AppliedRevision)
	}
	mu.Lock()
	gotPulls := append([]string(nil), pullQueries...)
	gotReports := reportCount
	mu.Unlock()
	if len(gotPulls) != 2 || gotPulls[0] != "0" || gotPulls[1] != "0" || gotReports != 2 {
		t.Fatalf("expected 2 pulls from 0 and 2 report attempts, got pulls=%v reports=%d", gotPulls, gotReports)
	}

	// Hook error with empty drift must produce PARTIAL.
	shadow2 := newEdgeConfigShadow("")
	shadow2.applyHook = func(context.Context, GatewayDesiredConfig) ([]ShadowDriftDetail, string, error) {
		return nil, "", context.DeadlineExceeded
	}
	rep, err := shadow2.applyDesired(context.Background(), desired)
	if err != nil || rep.AppliedStatus != shadowAppliedStatusPartial {
		t.Fatalf("expected PARTIAL on hook error, got status=%s err=%v", rep.AppliedStatus, err)
	}
}

func writeTestMTLSBundle(t *testing.T, dir string) (caPath, clientCertPath, clientKeyPath string, serverCert tls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	now := time.Now().UTC()
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-shadow-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	caPath = filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatalf("write CA cert: %v", err)
	}

	issueLeaf := func(serial int64, cn string, usage x509.ExtKeyUsage, ips []net.IP, dns []string) ([]byte, []byte, tls.Certificate) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate leaf key: %v", err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    now.Add(-time.Hour),
			NotAfter:     now.Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
			IPAddresses:  ips,
			DNSNames:     dns,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatalf("sign leaf cert: %v", err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatalf("marshal leaf key: %v", err)
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatalf("build keypair: %v", err)
		}
		return certPEM, keyPEM, pair
	}

	_, _, serverCert = issueLeaf(2, "127.0.0.1", x509.ExtKeyUsageServerAuth, []net.IP{net.ParseIP("127.0.0.1")}, []string{"localhost"})
	clientCertPEM, clientKeyPEM, _ := issueLeaf(3, "gateway-01", x509.ExtKeyUsageClientAuth, nil, nil)
	clientCertPath = filepath.Join(dir, "gateway.crt")
	clientKeyPath = filepath.Join(dir, "gateway.key")
	if err := os.WriteFile(clientCertPath, clientCertPEM, 0o600); err != nil {
		t.Fatalf("write client cert: %v", err)
	}
	if err := os.WriteFile(clientKeyPath, clientKeyPEM, 0o600); err != nil {
		t.Fatalf("write client key: %v", err)
	}
	return caPath, clientCertPath, clientKeyPath, serverCert
}

type shadowStubTransport struct {
	mu      sync.Mutex
	holding map[uint16]uint16
}

func (s *shadowStubTransport) Read(_ context.Context, task edgecontrol.ModbusReadTask) ([]uint16, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint16, task.Quantity)
	for i := uint16(0); i < task.Quantity; i++ {
		out[i] = s.holding[task.Address+i]
	}
	return out, nil
}

func (s *shadowStubTransport) Write(_ context.Context, task edgecontrol.ModbusWriteTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, v := range task.Values {
		s.holding[task.Address+uint16(i)] = v
	}
	return nil
}

func TestConfigShadowHotReloadDriverConfigsAndAutonomyPolicy(t *testing.T) {
	t.Parallel()

	config := loadGeneratedCentralPlantConfig(t)
	at := time.Date(2026, 11, 17, 17, 0, 0, 0, time.UTC)
	plant := NewPlant(config.Plant, config.Scenario, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatalf("NewEdgeControlRuntime() error = %v", err)
	}

	transport := &shadowStubTransport{
		holding: map[uint16]uint16{
			100: 75, // 7.5 degC with scale 0.1
		},
	}
	runtime.SetDriverTransportFactory(func(edgecontrol.DeclarativeModbusProfile) (edgecontrol.ModbusRegisterTransport, error) {
		return transport, nil
	})

	dir := t.TempDir()
	caPath, certPath, keyPath, serverCert := writeTestMTLSBundle(t, dir)

	var mu sync.Mutex
	var reports []GatewayShadowReportRequest
	desired := GatewayDesiredConfig{
		DesiredRevision:  10,
		PointMappings:    []GatewayPointMapping{},
		SamplingPolicies: []GatewaySamplingPolicy{},
		DriverConfigs: []GatewayModbusDriverConfig{
			{
				DeviceKey:  "PLC-AUX-01",
				DriverType: "MODBUS_TCP",
				Endpoint:   "127.0.0.1:5502",
				UnitID:     3,
				TimeoutMS:  500,
				Retries:    1,
				Registers: []GatewayModbusRegisterMapping{
					{
						PointCode:     "aux_supply_temp_c",
						FunctionCode:  3,
						Address:       100,
						RegisterCount: 1,
						RawType:       "INT16",
						ByteOrder:     "BIG_ENDIAN",
						WordOrder:     "BIG_ENDIAN",
						Scale:         0.1,
						Offset:        0,
						PollPriority:  "HIGH",
					},
				},
			},
		},
		AutonomyPolicy: &GatewayAutonomyPolicy{
			Enabled:                            true,
			OfflineGraceSeconds:                45,
			DefaultChilledWaterSupplySetpointC: 8.0,
			DefaultPumpFrequencyHz:             34.0,
			MinPumpFrequencyHz:                 22.0,
			MaxPumpFrequencyHz:                 46.0,
			MinRunSeconds:                      90,
			MinStopSeconds:                     60,
			SnapshotIntervalSeconds:            60,
		},
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/gateway/v1/config-shadow":
			cp := desired
			since := r.URL.Query().Get("sinceRevision")
			hasDelta := since != "10" && since != "11" && since != "12"
			_ = json.NewEncoder(w).Encode(GatewayShadowPullResponse{
				GatewayID:       "0193f000-2000-7000-8000-000000000001",
				DesiredRevision: cp.DesiredRevision,
				HasDelta:        hasDelta,
				Desired:         &cp,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/gateway/v1/config-shadow/report":
			var rep GatewayShadowReportRequest
			if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			reports = append(reports, rep)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	caPEM, _ := os.ReadFile(caPath)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	srv.StartTLS()
	defer srv.Close()

	statePath := filepath.Join(dir, "queue", "config-shadow.v1.json")
	shadow := newEdgeConfigShadow(statePath)
	publisher := &MQTTPublisher{
		gatewayID: "0193f000-2000-7000-8000-000000000001",
		config: MQTTGatewayConfig{
			GatewayID:            "0193f000-2000-7000-8000-000000000001",
			EnrollmentURL:        srv.URL,
			EnrollmentCAFile:     caPath,
			EnrollmentServerName: "127.0.0.1",
			CertFile:             certPath,
			KeyFile:              keyPath,
			QueueDirectory:       filepath.Join(dir, "queue"),
		},
		pointByKey:       map[string]PointConfig{},
		edgeRuntime:      runtime,
		configShadow:     shadow,
		snapshotInterval: defaultMQTTFullSnapshotInterval,
		lastPointStates:  map[string]lastEmittedPointState{},
	}
	shadow.applyDriverHook = func(_ context.Context, cfg GatewayModbusDriverConfig, profile edgecontrol.DeclarativeModbusProfile) error {
		if err := runtime.ApplyDriverProfile(profile); err != nil {
			return err
		}
		publisher.registerShadowDriverPoints(cfg)
		return nil
	}
	shadow.removeDriverHook = func(_ context.Context, deviceKey string) error {
		if err := runtime.RemoveDriverProfile(deviceKey); err != nil {
			return err
		}
		publisher.unregisterShadowDriverPoints(deviceKey)
		return nil
	}
	shadow.applyAutonomyHook = func(_ context.Context, policy *GatewayAutonomyPolicy) error {
		effective := edgecontrol.DefaultAutonomyPolicyConfig()
		if policy != nil {
			effective = *policy
		}
		if err := runtime.ApplyAutonomyPolicy(effective); err != nil {
			return err
		}
		publisher.SetSnapshotInterval(effective.SnapshotInterval())
		return nil
	}

	if err := publisher.SyncConfigShadow(context.Background()); err != nil {
		t.Fatalf("SyncConfigShadow() error = %v", err)
	}

	mu.Lock()
	if len(reports) != 1 || reports[0].AppliedStatus != shadowAppliedStatusApplied {
		t.Fatalf("expected APPLIED report, got %+v", reports)
	}
	if len(reports[0].DriverConfigs) != 1 || reports[0].AutonomyPolicy == nil || reports[0].AutonomyPolicy.SnapshotIntervalSeconds != 60 {
		t.Fatalf("report missing applied driverConfigs/autonomyPolicy: %+v", reports[0])
	}
	mu.Unlock()

	if publisher.SnapshotInterval() != 60*time.Second {
		t.Fatalf("publisher.SnapshotInterval() = %s, want 60s", publisher.SnapshotInterval())
	}
	if gotPolicy := runtime.AutonomyPolicy(); !gotPolicy.Enabled || gotPolicy.OfflineGraceSeconds != 45 || gotPolicy.MinRunSeconds != 90 {
		t.Fatalf("runtime.AutonomyPolicy() did not hot-update: %+v", gotPolicy)
	}

	// Verify hot-reloaded PLC-AUX-01 device polls inside existing runtime without restart, while CHILLER-01 stays alive.
	cycle := runtime.RunCycle(context.Background(), at.Add(time.Second))
	if snap, ok := cycle.Cycle.Image.Get("PLC-AUX-01/aux_supply_temp_c"); !ok || snap.Sample.Value.Double != 7.5 {
		t.Fatalf("expected hot-loaded PLC-AUX-01/aux_supply_temp_c = 7.5, got %+v (ok=%v)", snap, ok)
	}
	if _, ok := runtime.Host().Adapter(config.Plant.Chiller.ID); !ok {
		t.Fatal("unrelated CHILLER-01 adapter should remain registered after hot-reload")
	}

	// Reload persisted shadow from disk into a fresh instance to confirm durability.
	reloaded := newEdgeConfigShadow(statePath)
	if err := reloaded.load(); err != nil {
		t.Fatalf("reloaded.load() error = %v", err)
	}
	reloadedSnap := reloaded.snapshot()
	if reloadedSnap.AppliedRevision != 10 || len(reloadedSnap.DriverConfigs) != 1 || reloadedSnap.AutonomyPolicy == nil || reloadedSnap.AutonomyPolicy.OfflineGraceSeconds != 45 {
		t.Fatalf("reloaded persisted shadow mismatch: %+v", reloadedSnap)
	}

	// Test PARTIAL status when one driver fails validation and another succeeds.
	repPartial, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 11,
		DriverConfigs: []GatewayModbusDriverConfig{
			desired.DriverConfigs[0],
			{
				DeviceKey:  "PLC-BAD-01",
				DriverType: "MODBUS_TCP",
				Endpoint:   "not-a-host-port",
				UnitID:     1,
				TimeoutMS:  500,
				Retries:    0,
				Registers: []GatewayModbusRegisterMapping{
					{PointCode: "bad_pt", FunctionCode: 3, Address: 0, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "HIGH"},
				},
			},
		},
		AutonomyPolicy: desired.AutonomyPolicy,
	})
	if err != nil {
		t.Fatalf("partial applyDesired() error = %v", err)
	}
	if repPartial.AppliedStatus != shadowAppliedStatusPartial || len(repPartial.DriverConfigs) != 1 || len(repPartial.DriftDetails) != 1 {
		t.Fatalf("expected PARTIAL with 1 accepted driver and 1 drift detail, got %+v", repPartial)
	}
	if repPartial.DriftDetails[0].Section != shadowDriftSectionDriverConfigs || repPartial.DriftDetails[0].Reason != shadowDriftReasonApplyFailed {
		t.Fatalf("unexpected PARTIAL driftDetail: %+v", repPartial.DriftDetails[0])
	}

	// Test REJECTED status when all desired items fail (invalid AutonomyPolicy alone).
	repRejected, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 12,
		AutonomyPolicy: &GatewayAutonomyPolicy{
			Enabled:                            true,
			OfflineGraceSeconds:                3, // invalid (< 10)
			DefaultChilledWaterSupplySetpointC: 7.0,
			DefaultPumpFrequencyHz:             35.0,
			MinPumpFrequencyHz:                 20.0,
			MaxPumpFrequencyHz:                 50.0,
			SnapshotIntervalSeconds:            60,
		},
	})
	if err != nil {
		t.Fatalf("rejected applyDesired() error = %v", err)
	}
	if repRejected.AppliedStatus != shadowAppliedStatusRejected || repRejected.AutonomyPolicy != nil || len(repRejected.DriftDetails) != 1 {
		t.Fatalf("expected REJECTED with 1 drift detail and nil applied policy, got %+v", repRejected)
	}
	if repRejected.DriftDetails[0].Section != shadowDriftSectionAutonomyPolicy || repRejected.DriftDetails[0].Reason != shadowDriftReasonApplyFailed {
		t.Fatalf("unexpected REJECTED driftDetail: %+v", repRejected.DriftDetails[0])
	}
	// Previous valid autonomyPolicy remains active after rejected revision.
	if got := shadow.snapshot().AutonomyPolicy; got == nil || got.OfflineGraceSeconds != 45 {
		t.Fatalf("expected previous valid autonomyPolicy retained after REJECTED revision, got %+v", got)
	}
}

type closableShadowStubTransport struct {
	*shadowStubTransport
	onClose func()
}

func (c *closableShadowStubTransport) Close() error {
	if c.onClose != nil {
		c.onClose()
	}
	return nil
}

func TestConfigShadowDriverDeleteRenamePolicyClearAndPartialPreservation(t *testing.T) {
	t.Parallel()

	config := loadGeneratedCentralPlantConfig(t)
	at := time.Date(2026, 11, 17, 17, 30, 0, 0, time.UTC)
	plant := NewPlant(config.Plant, config.Scenario, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatalf("NewEdgeControlRuntime() error = %v", err)
	}

	var closeMu sync.Mutex
	closedByKey := map[string]int{}
	runtime.SetDriverTransportFactory(func(profile edgecontrol.DeclarativeModbusProfile) (edgecontrol.ModbusRegisterTransport, error) {
		return &closableShadowStubTransport{
			shadowStubTransport: &shadowStubTransport{
				holding: map[uint16]uint16{100: 75, 101: 0, 102: 120},
			},
			onClose: func() {
				closeMu.Lock()
				closedByKey[profile.ComponentID]++
				closeMu.Unlock()
			},
		}, nil
	})

	dir := t.TempDir()
	shadow := newEdgeConfigShadow(filepath.Join(dir, "config-shadow.v1.json"))
	publisher := &MQTTPublisher{
		gatewayID:               "0193f000-2000-7000-8000-000000000091",
		pointByKey:              map[string]PointConfig{},
		shadowPointKeysByDevice: map[string]map[string]struct{}{},
		edgeRuntime:             runtime,
		configShadow:            shadow,
		snapshotInterval:        defaultMQTTFullSnapshotInterval,
		lastPointStates:         map[string]lastEmittedPointState{},
	}
	shadow.applyDriverHook = func(_ context.Context, cfg GatewayModbusDriverConfig, profile edgecontrol.DeclarativeModbusProfile) error {
		if err := runtime.ApplyDriverProfile(profile); err != nil {
			return err
		}
		publisher.registerShadowDriverPoints(cfg)
		return nil
	}
	shadow.removeDriverHook = func(_ context.Context, deviceKey string) error {
		if err := runtime.RemoveDriverProfile(deviceKey); err != nil {
			return err
		}
		publisher.unregisterShadowDriverPoints(deviceKey)
		return nil
	}
	shadow.applyAutonomyHook = func(_ context.Context, policy *GatewayAutonomyPolicy) error {
		effective := edgecontrol.DefaultAutonomyPolicyConfig()
		if policy != nil {
			effective = *policy
		}
		if err := runtime.ApplyAutonomyPolicy(effective); err != nil {
			return err
		}
		publisher.SetSnapshotInterval(effective.SnapshotInterval())
		return nil
	}

	validPolicy := &GatewayAutonomyPolicy{
		Enabled:                            true,
		OfflineGraceSeconds:                45,
		DefaultChilledWaterSupplySetpointC: 8.0,
		DefaultPumpFrequencyHz:             34.0,
		MinPumpFrequencyHz:                 22.0,
		MaxPumpFrequencyHz:                 46.0,
		MinRunSeconds:                      90,
		MinStopSeconds:                     60,
		SnapshotIntervalSeconds:            60,
	}
	drvAux1 := GatewayModbusDriverConfig{
		DeviceKey:         "PLC-AUX-01",
		DriverType:        "MODBUS_TCP",
		Endpoint:          "127.0.0.1:5502",
		UnitID:            3,
		CapabilityProfile: "THERMAL_ENERGY_METER",
		TimeoutMS:         500,
		Retries:           1,
		Registers: []GatewayModbusRegisterMapping{
			{PointCode: "aux_supply_temp_c", SemanticChannel: "SupplyTemperature", FunctionCode: 3, Address: 100, RegisterCount: 1, RawType: "INT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
			{PointCode: "old_fault_pt", SemanticChannel: "Flow", FunctionCode: 3, Address: 101, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "HIGH"},
		},
	}

	if rep, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 1,
		DriverConfigs:   []GatewayModbusDriverConfig{drvAux1},
		AutonomyPolicy:  validPolicy,
	}); err != nil || rep.AppliedStatus != shadowAppliedStatusApplied {
		t.Fatalf("rev 1 applyDesired failed: (%+v, %v)", rep, err)
	}
	if _, ok := publisher.lookupPoint("PLC-AUX-01", "old_fault_pt"); !ok {
		t.Fatal("expected old_fault_pt registered after rev 1")
	}

	// Update PLC-AUX-01 removing old_fault_pt and changing scale/unit propagation + adding new_pt.
	drvAux1Updated := drvAux1
	drvAux1Updated.Registers = []GatewayModbusRegisterMapping{
		{PointCode: "aux_supply_temp_c", SemanticChannel: "SupplyTemperature", FunctionCode: 3, Address: 100, RegisterCount: 1, RawType: "INT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.01, PollPriority: "HIGH"},
		{PointCode: "new_return_pt", SemanticChannel: "ReturnTemperature", FunctionCode: 3, Address: 102, RegisterCount: 1, RawType: "INT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
	}
	if rep, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 2,
		DriverConfigs:   []GatewayModbusDriverConfig{drvAux1Updated},
		AutonomyPolicy:  validPolicy,
	}); err != nil || rep.AppliedStatus != shadowAppliedStatusApplied {
		t.Fatalf("rev 2 applyDesired failed: (%+v, %v)", rep, err)
	}
	if _, ok := publisher.lookupPoint("PLC-AUX-01", "old_fault_pt"); ok {
		t.Fatal("expected removed register old_fault_pt purged from pointByKey")
	}
	if pt, ok := publisher.lookupPoint("PLC-AUX-01", "aux_supply_temp_c"); !ok || pt.Unit == "" {
		t.Fatalf("expected updated pointByKey metadata overwritten with Unit, got (%+v, ok=%v)", pt, ok)
	}
	if _, ok := publisher.lookupPoint("PLC-AUX-01", "new_return_pt"); !ok {
		t.Fatal("expected new_return_pt registered after rev 2")
	}

	// Partial failure: rename PLC-AUX-01 -> PLC-AUX-02 (succeeds) while invalid non-nil autonomyPolicy fails ->
	// PLC-AUX-01 must be removed & closed, PLC-AUX-02 added, AND prevPolicy must be preserved!
	drvAux2 := drvAux1Updated
	drvAux2.DeviceKey = "PLC-AUX-02"
	invalidPolicy := *validPolicy
	invalidPolicy.OfflineGraceSeconds = 2
	repPartial, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 3,
		DriverConfigs:   []GatewayModbusDriverConfig{drvAux2},
		AutonomyPolicy:  &invalidPolicy,
	})
	if err != nil || repPartial.AppliedStatus != shadowAppliedStatusPartial {
		t.Fatalf("rev 3 expected PARTIAL, got (%+v, %v)", repPartial, err)
	}
	if _, ok := runtime.Host().Adapter("PLC-AUX-01"); ok {
		t.Fatal("expected deleted/renamed PLC-AUX-01 unregistered from Host")
	}
	if _, ok := publisher.lookupPoint("PLC-AUX-01", "aux_supply_temp_c"); ok {
		t.Fatal("expected PLC-AUX-01 purged from publisher.pointByKey")
	}
	if _, ok := runtime.Host().Adapter("PLC-AUX-02"); !ok {
		t.Fatal("expected renamed PLC-AUX-02 active on Host")
	}
	if got := shadow.snapshot().AutonomyPolicy; got == nil || got.OfflineGraceSeconds != 45 {
		t.Fatalf("expected prevPolicy preserved on PARTIAL autonomy failure, got %+v", got)
	}
	if !runtime.AutonomyPolicy().Enabled || publisher.SnapshotInterval() != 60*time.Second {
		t.Fatalf("expected active runtime autonomy policy & 60s interval preserved on PARTIAL, got policy=%+v interval=%s", runtime.AutonomyPolicy(), publisher.SnapshotInterval())
	}
	closeMu.Lock()
	aux1Closes := closedByKey["PLC-AUX-01"]
	closeMu.Unlock()
	if aux1Closes < 2 {
		t.Fatalf("expected PLC-AUX-01 adapter closed on replace and delete (>=2), got %d", aux1Closes)
	}

	// Clearing autonomyPolicy (nil) must disable fallback and reset snapshotInterval to 300s.
	repClearedPolicy, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 4,
		DriverConfigs:   []GatewayModbusDriverConfig{drvAux2},
		AutonomyPolicy:  nil,
	})
	if err != nil || repClearedPolicy.AppliedStatus != shadowAppliedStatusApplied {
		t.Fatalf("rev 4 clear policy failed: (%+v, %v)", repClearedPolicy, err)
	}
	if got := shadow.snapshot().AutonomyPolicy; got != nil {
		t.Fatalf("expected committed autonomyPolicy cleared to nil, got %+v", got)
	}
	if runtime.AutonomyPolicy().Enabled || publisher.SnapshotInterval() != 300*time.Second {
		t.Fatalf("expected fallback disabled & 300s snapshotInterval when autonomyPolicy=nil, got policy=%+v interval=%s", runtime.AutonomyPolicy(), publisher.SnapshotInterval())
	}

	// Clearing all drivers (empty slice) removes PLC-AUX-02 and clears dynamic channels.
	repClearedDrivers, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 5,
		DriverConfigs:   []GatewayModbusDriverConfig{},
	})
	if err != nil || repClearedDrivers.AppliedStatus != shadowAppliedStatusApplied {
		t.Fatalf("rev 5 clear drivers failed: (%+v, %v)", repClearedDrivers, err)
	}
	if _, ok := runtime.Host().Adapter("PLC-AUX-02"); ok || runtime.HasDynamicChannels() {
		t.Fatal("expected PLC-AUX-02 removed and HasDynamicChannels()==false after empty driverConfigs")
	}
	closeMu.Lock()
	aux2Closes := closedByKey["PLC-AUX-02"]
	closeMu.Unlock()
	if aux2Closes < 1 {
		t.Fatalf("expected PLC-AUX-02 adapter closed on removal, got %d", aux2Closes)
	}
}
