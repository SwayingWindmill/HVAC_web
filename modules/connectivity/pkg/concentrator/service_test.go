package concentrator

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
	"fmt"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/mqttclient"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

type fakeStore struct {
	mu         sync.Mutex
	clock      time.Time
	tenantID   string
	resolveErr error
	auditErr   error
	audits     []connectivity.ConcentratorAuditEntry
	desired    *connectivity.GatewayDesiredConfig
	reported   *connectivity.GatewayReportedConfig
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		clock:    time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC),
		tenantID: "018f3d00-1000-7000-8000-000000000001",
	}
}

func (f *fakeStore) Clock() time.Time {
	return f.clock
}

func (f *fakeStore) ResolveGatewayForOperator(_ context.Context, _ registryauth.GrantClaims, _ string) (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return f.tenantID, nil
}

func (f *fakeStore) RecordConcentratorAudit(ctx context.Context, _ string, _ string, entry connectivity.ConcentratorAuditEntry) (connectivity.ConcentratorAuditEntry, error) {
	if err := ctx.Err(); err != nil {
		return connectivity.ConcentratorAuditEntry{}, err
	}
	if f.auditErr != nil {
		return connectivity.ConcentratorAuditEntry{}, f.auditErr
	}
	id, _ := connectivity.NewUUIDv7At(f.clock)
	entry.ID = id
	f.mu.Lock()
	f.audits = append([]connectivity.ConcentratorAuditEntry{entry}, f.audits...)
	f.mu.Unlock()
	return entry, nil
}

func (f *fakeStore) ListConcentratorAudits(_ context.Context, _ string, _ string, unitID, limit int) ([]connectivity.ConcentratorAuditEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []connectivity.ConcentratorAuditEntry{}
	for _, a := range f.audits {
		if a.UnitID == unitID {
			out = append(out, a)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeStore) LoadGatewayConfigShadowByGatewayID(_ context.Context, gatewayID string) (connectivity.GatewayConfigShadow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	drift := connectivity.ComputeConfigShadowDrift(f.desired, f.reported)
	status := connectivity.ShadowSyncStatusUnconfigured
	switch {
	case f.desired == nil && f.reported == nil:
		status = connectivity.ShadowSyncStatusUnconfigured
	case f.desired != nil && (f.reported == nil || f.reported.ReportedRevision < f.desired.DesiredRevision):
		status = connectivity.ShadowSyncStatusPendingSync
	case f.desired != nil && f.reported != nil && f.reported.ReportedRevision == f.desired.DesiredRevision && f.reported.AppliedStatus == connectivity.AppliedStatusApplied && len(drift) == 0:
		status = connectivity.ShadowSyncStatusSynced
	case f.reported != nil:
		status = connectivity.ShadowSyncStatusDrifted
	}
	return connectivity.GatewayConfigShadow{
		TenantID:   f.tenantID,
		GatewayID:  gatewayID,
		SyncStatus: status,
		Desired:    f.desired,
		Reported:   f.reported,
		Drift:      drift,
	}, nil
}

func (f *fakeStore) UpsertConcentratorShadowState(ctx context.Context, _ string, _ string, unitID int, params *connectivity.ConcentratorShadowParams, eids []connectivity.ConcentratorShadowEIDSlot, actorID, reason string, appliedToEdge bool) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.desired == nil {
		f.desired = &connectivity.GatewayDesiredConfig{
			DesiredRevision:     1,
			PointMappings:       []connectivity.GatewayPointMapping{},
			SamplingPolicies:    []connectivity.GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]connectivity.ConcentratorUnitShadow{},
			UpdatedBy:           actorID,
			Reason:              reason,
			CreatedAt:           f.clock,
			UpdatedAt:           f.clock,
		}
	} else {
		f.desired.DesiredRevision++
		f.desired.UpdatedBy = actorID
		f.desired.Reason = reason
		f.desired.UpdatedAt = f.clock
	}
	unitKeyStr := strconv.Itoa(unitID)
	u := f.desired.ConcentratorConfigs[unitKeyStr]
	if params != nil {
		cp := *params
		u.Params = &cp
	}
	if eids != nil {
		u.EIDs = slices.Clone(eids)
	}
	f.desired.ConcentratorConfigs[unitKeyStr] = u
	if appliedToEdge {
		if f.reported == nil {
			f.reported = &connectivity.GatewayReportedConfig{
				ReportedRevision:    f.desired.DesiredRevision,
				AppliedStatus:       connectivity.AppliedStatusApplied,
				PointMappings:       []connectivity.GatewayPointMapping{},
				SamplingPolicies:    []connectivity.GatewaySamplingPolicy{},
				ConcentratorConfigs: map[string]connectivity.ConcentratorUnitShadow{},
				DriftDetails:        []connectivity.DriftDetail{},
				ReportedAt:          f.clock,
				UpdatedAt:           f.clock,
			}
		} else {
			f.reported.ReportedRevision = f.desired.DesiredRevision
			f.reported.AppliedStatus = connectivity.AppliedStatusApplied
		}
		ru := f.reported.ConcentratorConfigs[unitKeyStr]
		if params != nil {
			cp := *params
			ru.Params = &cp
		}
		if eids != nil {
			ru.EIDs = slices.Clone(eids)
		}
		f.reported.ConcentratorConfigs[unitKeyStr] = ru
	}
	return f.desired.DesiredRevision, nil
}

func (f *fakeStore) RecordConcentratorReconcileResult(_ context.Context, _ string, _ string, desiredRevision int64, reconciledUnits map[string]connectivity.ConcentratorUnitShadow, renamedUnits map[string]string, failedDrifts []connectivity.DriftDetail, errorMessage *string, gatewayUnreachable bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reported == nil {
		f.reported = &connectivity.GatewayReportedConfig{
			PointMappings:       []connectivity.GatewayPointMapping{},
			SamplingPolicies:    []connectivity.GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]connectivity.ConcentratorUnitShadow{},
			DriftDetails:        []connectivity.DriftDetail{},
		}
	}
	for oldKey, newKey := range renamedUnits {
		if oldKey == "" || newKey == "" || oldKey == newKey {
			continue
		}
		if prev, ok := f.reported.ConcentratorConfigs[oldKey]; ok {
			delete(f.reported.ConcentratorConfigs, oldKey)
			f.reported.ConcentratorConfigs[newKey] = prev
		}
		if f.desired != nil && f.desired.DesiredRevision == desiredRevision {
			if dPrev, ok := f.desired.ConcentratorConfigs[oldKey]; ok {
				delete(f.desired.ConcentratorConfigs, oldKey)
				f.desired.ConcentratorConfigs[newKey] = dPrev
			}
		}
	}
	for k, u := range reconciledUnits {
		cur := f.reported.ConcentratorConfigs[k]
		if u.Params != nil {
			cp := *u.Params
			cur.Params = &cp
		}
		if u.EIDs != nil {
			cur.EIDs = slices.Clone(u.EIDs)
		}
		f.reported.ConcentratorConfigs[k] = cur
	}
	if f.desired != nil && f.desired.DesiredRevision == desiredRevision {
		if len(failedDrifts) == 0 {
			f.reported.ReportedRevision = desiredRevision
			f.reported.AppliedStatus = connectivity.AppliedStatusApplied
			f.reported.DriftDetails = []connectivity.DriftDetail{}
			f.reported.ErrorMessage = nil
		} else {
			if !gatewayUnreachable {
				f.reported.ReportedRevision = desiredRevision
			}
			f.reported.AppliedStatus = connectivity.AppliedStatusPartial
			f.reported.DriftDetails = failedDrifts
			f.reported.ErrorMessage = errorMessage
		}
	}
	f.reported.ReportedAt = f.clock
	f.reported.UpdatedAt = f.clock
	return nil
}

type fakeRPC struct {
	fn    func(context.Context, RPCRequest) (RPCResponse, error)
	calls atomic.Int32
}

func (r *fakeRPC) Call(ctx context.Context, req RPCRequest) (RPCResponse, error) {
	r.calls.Add(1)
	return r.fn(ctx, req)
}

func testClaims() registryauth.GrantClaims {
	return registryauth.GrantClaims{
		PrincipalID: "018f3e00-5000-7000-8000-000000000001",
		TenantID:    "018f3d00-1000-7000-8000-000000000001",
	}
}

func testGatewayID() string {
	return "018f3d00-2000-7000-8000-000000000001"
}

func makeFullSlots(eid1 uint32) []EIDSlot {
	slots := make([]EIDSlot, EIDSlotCount)
	for i := 0; i < EIDSlotCount; i++ {
		slots[i] = EIDSlot{Slot: i + 1, EID: 0}
	}
	slots[0].EID = eid1
	return slots
}

func TestServiceReadWriteParamsAndAudits(t *testing.T) {
	store := newFakeStore()
	currentTxPower := 20
	rpc := &fakeRPC{fn: func(_ context.Context, req RPCRequest) (RPCResponse, error) {
		switch req.Operation {
		case OperationReadStatus:
			temp := 23.4
			mv := 3580
			rssi := -71
			return RPCResponse{
				SchemaVersion: rpcSchemaVersion, RequestID: req.RequestID, GatewayID: req.GatewayID, UnitID: req.UnitID,
				Operation: OperationReadStatus, Status: "OK", RespondedAt: store.clock.UnixMilli(),
				Params:   &Params{ModbusID: 1, BaudRateCode: 3, OffLineTime: 60, DataUplinkCycle: 30, LocalNet: 100, TimeMark: 1, RfTxPower: currentTxPower, RfFrequency: 470, RfBandWidth: 2, RfSf: 7, RfLeadCode: 8, TxTimeout: 3000, RxTimeout: 3000, RssiOut: 1},
				Channels: []ChannelReading{{Channel: 1, EID: 33661717, Online: true, SensorType: 0x35, SensorModel: "TS01", TemperatureC: &temp, VoltageMv: &mv, RssiDbm: &rssi}},
			}, nil
		case OperationWriteParams:
			currentTxPower = req.Params["rfTxPower"]
			return RPCResponse{
				SchemaVersion: rpcSchemaVersion, RequestID: req.RequestID, GatewayID: req.GatewayID, UnitID: req.UnitID,
				Operation: OperationWriteParams, Status: "OK", RespondedAt: store.clock.UnixMilli(), WrittenCount: len(req.Params),
				Params: &Params{ModbusID: 1, BaudRateCode: 3, OffLineTime: 60, DataUplinkCycle: 30, LocalNet: 100, TimeMark: 1, RfTxPower: currentTxPower, RfFrequency: 470, RfBandWidth: 2, RfSf: 7, RfLeadCode: 8, TxTimeout: 3000, RxTimeout: 3000, RssiOut: 1},
			}, nil
		default:
			return RPCResponse{}, errors.New("unexpected op")
		}
	}}

	svc := NewService(store, rpc)
	txPower := 22
	writeRes, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), 1, ParamsUpdateRequest{
		Params: ParamsPatch{RfTxPower: &txPower},
		Reason: "improve RF link margin",
	})
	if err != nil {
		t.Fatalf("UpdateParams: %v", err)
	}
	if writeRes.Params.RfTxPower != 22 || writeRes.WrittenCount != 1 || writeRes.Audit.Outcome != OutcomeApplied {
		t.Fatalf("unexpected write result: %+v", writeRes)
	}

	status, err := svc.Status(context.Background(), testClaims(), testGatewayID(), 1)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Channels) != 1 || status.Params.RfTxPower != 22 || len(status.RecentAudits) != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestServiceProtectedUIDAndValidationGuards(t *testing.T) {
	store := newFakeStore()
	rpc := &fakeRPC{fn: func(_ context.Context, _ RPCRequest) (RPCResponse, error) {
		t.Fatal("RPC should never be invoked on validation or protected UID rejection")
		return RPCResponse{}, nil
	}}
	svc := NewService(store, rpc)

	for _, uid := range []int{2, 3, 4, 5, 10, 31} {
		if _, err := svc.Status(context.Background(), testClaims(), testGatewayID(), uid); !errors.Is(err, ErrProtectedUID) {
			t.Fatalf("Status uid=%d got %v, want ErrProtectedUID", uid, err)
		}
		if _, err := svc.EIDs(context.Background(), testClaims(), testGatewayID(), uid); !errors.Is(err, ErrProtectedUID) {
			t.Fatalf("EIDs uid=%d got %v, want ErrProtectedUID", uid, err)
		}
		pw := 20
		if _, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), uid, ParamsUpdateRequest{Params: ParamsPatch{RfTxPower: &pw}, Reason: "x"}); !errors.Is(err, ErrProtectedUID) {
			t.Fatalf("UpdateParams uid=%d got %v, want ErrProtectedUID", uid, err)
		}
		if _, err := svc.UpdateEIDs(context.Background(), testClaims(), testGatewayID(), uid, EIDUpdateRequest{Slots: makeFullSlots(1), Reason: "x"}); !errors.Is(err, ErrProtectedUID) {
			t.Fatalf("UpdateEIDs uid=%d got %v, want ErrProtectedUID", uid, err)
		}
	}

	protectedTarget := 5
	if _, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), 1, ParamsUpdateRequest{
		Params: ParamsPatch{ModbusID: &protectedTarget},
		Reason: "readdress",
	}); !errors.Is(err, ErrProtectedUID) {
		t.Fatalf("patching modbusId to protected UID got %v, want ErrProtectedUID", err)
	}

	dupSlots := makeFullSlots(33661717)
	dupSlots[9].EID = 33661717
	if _, err := svc.UpdateEIDs(context.Background(), testClaims(), testGatewayID(), 1, EIDUpdateRequest{Slots: dupSlots, Reason: "dup"}); !errors.Is(err, ErrDuplicateEID) {
		t.Fatalf("duplicate EID got %v, want ErrDuplicateEID", err)
	}

	if _, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), 1, ParamsUpdateRequest{Params: ParamsPatch{}, Reason: "   "}); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("empty reason got %v, want ErrReasonRequired", err)
	}

	badFreq := 1200
	if _, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), 1, ParamsUpdateRequest{Params: ParamsPatch{RfFrequency: &badFreq}, Reason: "bad freq"}); !errors.Is(err, ErrParamsInvalid) {
		t.Fatalf("bad freq got %v, want ErrParamsInvalid", err)
	}

	if rpc.calls.Load() != 0 {
		t.Fatalf("expected 0 RPC calls, got %d", rpc.calls.Load())
	}
}

func TestServicePerUnitMutexSerializesConcurrentOperations(t *testing.T) {
	store := newFakeStore()
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	enteredFirst := make(chan struct{})
	releaseFirst := make(chan struct{})

	rpc := &fakeRPC{fn: func(_ context.Context, req RPCRequest) (RPCResponse, error) {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		if req.Params["rfTxPower"] == 18 {
			close(enteredFirst)
			<-releaseFirst
		}
		inFlight.Add(-1)
		return RPCResponse{
			SchemaVersion: rpcSchemaVersion, RequestID: req.RequestID, GatewayID: req.GatewayID, UnitID: req.UnitID,
			Operation: req.Operation, Status: "OK", RespondedAt: store.clock.UnixMilli(), WrittenCount: 1,
			Params: &Params{RfTxPower: req.Params["rfTxPower"]},
		}, nil
	}}

	svc := NewService(store, rpc)
	errCh := make(chan error, 2)
	go func() {
		p := 18
		_, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), 1, ParamsUpdateRequest{Params: ParamsPatch{RfTxPower: &p}, Reason: "first"})
		errCh <- err
	}()
	<-enteredFirst
	go func() {
		p := 20
		_, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), 1, ParamsUpdateRequest{Params: ParamsPatch{RfTxPower: &p}, Reason: "second"})
		errCh <- err
	}()
	time.Sleep(25 * time.Millisecond)
	close(releaseFirst)

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent op %d failed: %v", i, err)
		}
	}
	if maxInFlight.Load() != 1 {
		t.Fatalf("expected strict serialization (maxInFlight=1), got %d", maxInFlight.Load())
	}
}

func TestMQTTTransportDeliverReplyChecksGatewayBinding(t *testing.T) {
	gwA := testGatewayID()
	gwB := "018f3d00-2000-7000-8000-000000000002"
	reqID := "018f3d00-3000-7000-8000-000000000009"

	tr := &MQTTTransport{waiters: map[waiterKey]chan RPCResponse{}}
	ch := make(chan RPCResponse, 1)
	tr.waiters[waiterKey{gatewayID: gwA, requestID: reqID}] = ch

	spoofedPayload, _ := json.Marshal(RPCResponse{
		SchemaVersion: rpcSchemaVersion, RequestID: reqID, GatewayID: gwB, UnitID: 1, Operation: OperationReadStatus, Status: "OK",
	})
	if tr.DeliverReply("hvac/v1/"+gwA+"/up/concentrator", spoofedPayload) {
		t.Fatal("spoofed gatewayId reply must be ignored")
	}

	if tr.DeliverReply("hvac/v1/"+gwA+"/up/concentrator", []byte(`{"schemaVersion":"1.0","requestId":"`+reqID+`","gatewayId":"`+gwA+`","unexpectedField":1}`)) {
		t.Fatal("reply with unknown JSON field must be rejected")
	}

	validPayload, _ := json.Marshal(RPCResponse{
		SchemaVersion: rpcSchemaVersion, RequestID: reqID, GatewayID: gwA, UnitID: 1, Operation: OperationReadStatus, Status: "OK",
	})
	if !tr.DeliverReply("hvac/v1/"+gwA+"/up/concentrator", validPayload) {
		t.Fatal("matching reply should be accepted")
	}
	select {
	case got := <-ch:
		if got.GatewayID != gwA || got.RequestID != reqID {
			t.Fatalf("unexpected delivered response: %+v", got)
		}
	default:
		t.Fatal("expected waiter channel to receive reply")
	}
}

func TestHTTPHandlerSPIFFEAuthAndTimeoutAudit(t *testing.T) {
	store := newFakeStore()
	rpc := &fakeRPC{fn: func(_ context.Context, _ RPCRequest) (RPCResponse, error) {
		return RPCResponse{}, ErrRPCTimeout
	}}
	svc := NewService(store, rpc)
	rawHandler := NewHTTPHandler(svc, func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
		return testClaims(), nil
	})
	wrapped := connectivity.NewCredentialHandler(nil, nil, connectivity.WithConcentratorHandler(rawHandler))

	unauthReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+testGatewayID()+"/concentrators/1/params", strings.NewReader(`{"params":{"rfTxPower":20},"reason":"timeout test"}`))
	unauthRec := httptest.NewRecorder()
	wrapped.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without SPIFFE TLS chain, got %d", unauthRec.Code)
	}

	spiffeURI, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	authReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+testGatewayID()+"/concentrators/1/params", strings.NewReader(`{"params":{"rfTxPower":20},"reason":"timeout test"}`))
	authReq.TLS = &tls.ConnectionState{
		VerifiedChains: [][]*x509.Certificate{{
			{Subject: pkix.Name{CommonName: "platform-gateway"}, URIs: []*url.URL{spiffeURI}},
		}},
	}
	authRec := httptest.NewRecorder()
	wrapped.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 on timeout, got %d body=%s", authRec.Code, authRec.Body.String())
	}

	audits, _ := store.ListConcentratorAudits(context.Background(), testClaims().TenantID, testGatewayID(), 1, 10)
	if len(audits) != 1 || audits[0].Outcome != OutcomeTimeout {
		t.Fatalf("expected 1 TIMEOUT audit recorded, got %+v", audits)
	}

	status, code := MapError(adapter.ErrGatewayCredentialInactive)
	if status != http.StatusConflict || code != "GATEWAY_CREDENTIAL_INACTIVE" {
		t.Fatalf("MapError(ErrGatewayCredentialInactive) = (%d, %q), want (409, GATEWAY_CREDENTIAL_INACTIVE)", status, code)
	}
}

func writeConcentratorTestPKI(t *testing.T) (TransportConfig, *tls.Config) {
	t.Helper()
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	issue := func(serial int64, name string, usage x509.ExtKeyUsage) tls.Certificate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	writePEM := func(name, kind string, der []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	client := issue(2, "concentrator", x509.ExtKeyUsageClientAuth)
	clientKey, _ := x509.MarshalECPrivateKey(client.PrivateKey.(*ecdsa.PrivateKey))
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	return TransportConfig{
		ClientID:   "concentrator-test",
		CAFile:     writePEM("ca.pem", "CERTIFICATE", caDER),
		CertFile:   writePEM("client.pem", "CERTIFICATE", client.Certificate[0]),
		KeyFile:    writePEM("client.key", "EC PRIVATE KEY", clientKey),
		ServerName: "mqtt.local",
	}, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{issue(3, "mqtt.local", x509.ExtKeyUsageServerAuth)},
		ClientCAs:    roots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
}

func TestConcentratorMessageExpirySecondsAndSelfHealingTransport(t *testing.T) {
	for _, tc := range []struct {
		timeout time.Duration
		want    uint32
	}{
		{-time.Second, 1},
		{0, 1},
		{150 * time.Millisecond, 1},
		{1200 * time.Millisecond, 2},
		{18 * time.Second, 18},
		{time.Duration(math.MaxInt64), math.MaxUint32},
	} {
		if got := concentratorMessageExpirySeconds(tc.timeout); got != tc.want {
			t.Fatalf("concentratorMessageExpirySeconds(%s) = %d, want %d", tc.timeout, got, tc.want)
		}
	}

	cfg, serverTLS := writeConcentratorTestPKI(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg.BrokerURL = "tls://" + listener.Addr().String()

	var connSeq atomic.Int32
	var conn1Subscribes atomic.Int32
	conn1Closed := make(chan struct{})
	conn2Subscribed := make(chan struct{})
	var conn2Mu sync.Mutex
	var conn2Server net.Conn
	var publishedExpiriesMu sync.Mutex
	var publishedExpiries []uint32

	go func() {
		for {
			c, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			seq := connSeq.Add(1)
			if seq == 2 {
				conn2Mu.Lock()
				conn2Server = c
				conn2Mu.Unlock()
			}
			go func(s int32, sc net.Conn) {
				defer sc.Close()
				pkt, err := packets.ReadPacket(sc)
				if err != nil || pkt.Type != packets.CONNECT {
					return
				}
				connack := packets.NewControlPacket(packets.CONNACK)
				connack.Content.(*packets.Connack).SessionPresent = false
				if _, err := connack.WriteTo(sc); err != nil {
					return
				}
				for {
					p, err := packets.ReadPacket(sc)
					if err != nil {
						if s == 1 {
							close(conn1Closed)
						}
						return
					}
					switch p.Type {
					case packets.SUBSCRIBE:
						sub := p.Content.(*packets.Subscribe)
						suback := packets.NewControlPacket(packets.SUBACK)
						sa := suback.Content.(*packets.Suback)
						sa.PacketID = sub.PacketID
						for range sub.Subscriptions {
							if s == 1 {
								sa.Reasons = append(sa.Reasons, 0x87)
							} else {
								sa.Reasons = append(sa.Reasons, 1)
							}
						}
						if s == 1 {
							conn1Subscribes.Add(1)
						}
						_, _ = suback.WriteTo(sc)
						if s == 2 {
							select {
							case <-conn2Subscribed:
							default:
								close(conn2Subscribed)
							}
						}
					case packets.PUBLISH:
						pub := p.Content.(*packets.Publish)
						puback := packets.NewControlPacket(packets.PUBACK)
						puback.Content.(*packets.Puback).PacketID = pub.PacketID
						_, _ = puback.WriteTo(sc)

						if pub.Properties != nil && pub.Properties.MessageExpiry != nil {
							publishedExpiriesMu.Lock()
							publishedExpiries = append(publishedExpiries, *pub.Properties.MessageExpiry)
							publishedExpiriesMu.Unlock()
						}
						var req RPCRequest
						if err := json.Unmarshal(pub.Payload, &req); err == nil {
							replyBytes, _ := json.Marshal(RPCResponse{
								SchemaVersion: rpcSchemaVersion,
								RequestID:     req.RequestID,
								GatewayID:     req.GatewayID,
								UnitID:        req.UnitID,
								Operation:     req.Operation,
								Status:        "OK",
							})
							replyPkt := packets.NewControlPacket(packets.PUBLISH)
							rp := replyPkt.Content.(*packets.Publish)
							rp.Topic = "hvac/v1/" + req.GatewayID + "/up/concentrator"
							rp.Payload = replyBytes
							rp.QoS = 1
							rp.PacketID = 99
							_, _ = replyPkt.WriteTo(sc)
						}
					case packets.PINGREQ:
						_, _ = packets.NewControlPacket(packets.PINGRESP).WriteTo(sc)
					}
				}
			}(seq, c)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr, err := newMQTTTransport(ctx, cfg, func(int) time.Duration { return 15 * time.Millisecond }, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("newMQTTTransport: %v", err)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutCancel()
		_ = tr.Close(shutCtx)
	}()

	if _, err := tr.Call(context.Background(), RPCRequest{
		SchemaVersion: rpcSchemaVersion,
		RequestID:     "018f3d00-3000-7000-8000-000000000000",
		GatewayID:     testGatewayID(),
		UnitID:        1,
		Operation:     OperationReadStatus,
	}); !errors.Is(err, ErrTransportUnavailable) {
		t.Fatalf("unsubscribed Call err=%v, want ErrTransportUnavailable", err)
	}

	select {
	case <-conn1Closed:
	case <-time.After(4 * time.Second):
		t.Fatal("concentrator transport did not close connection after SUBACK rejections")
	}
	if got := conn1Subscribes.Load(); got != 3 {
		t.Fatalf("conn1 subscribe attempts=%d, want 3", got)
	}

	waitReadyCtx, waitReadyCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer waitReadyCancel()
	if err := tr.WaitReady(waitReadyCtx); err != nil {
		t.Fatalf("WaitReady after reconnect: %v", err)
	}
	<-conn2Subscribed
	deadline := time.Now().Add(time.Second)
	for !tr.Subscribed() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !tr.Subscribed() {
		t.Fatal("expected Subscribed() == true after conn2 subscribed")
	}

	expiredCtx, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if _, err := tr.Call(expiredCtx, RPCRequest{
		SchemaVersion: rpcSchemaVersion,
		RequestID:     "018f3d00-3000-7000-8000-000000000001",
		GatewayID:     testGatewayID(),
		UnitID:        1,
		Operation:     OperationReadStatus,
	}); !errors.Is(err, ErrRPCTimeout) {
		t.Fatalf("expired Call err=%v, want ErrRPCTimeout", err)
	}

	shortCtx, cancelShort := context.WithTimeout(context.Background(), 4200*time.Millisecond)
	defer cancelShort()
	resp, err := tr.Call(shortCtx, RPCRequest{
		SchemaVersion: rpcSchemaVersion,
		RequestID:     "018f3d00-3000-7000-8000-000000000002",
		GatewayID:     testGatewayID(),
		UnitID:        1,
		Operation:     OperationReadStatus,
	})
	if err != nil || resp.Status != "OK" {
		t.Fatalf("Call=(%+v, %v)", resp, err)
	}
	publishedExpiriesMu.Lock()
	expiriesCopy := append([]uint32(nil), publishedExpiries...)
	publishedExpiriesMu.Unlock()
	if len(expiriesCopy) != 1 || expiriesCopy[0] < 4 || expiriesCopy[0] > 5 {
		t.Fatalf("wire PublishProperties.MessageExpiry=%v, want [4 or 5]", expiriesCopy)
	}

	conn2Mu.Lock()
	if conn2Server != nil {
		_ = conn2Server.Close()
	}
	conn2Mu.Unlock()
	deadline = time.Now().Add(time.Second)
	for tr.Subscribed() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if tr.Subscribed() {
		t.Fatal("expected Subscribed() == false immediately after disconnect")
	}
}

func TestServiceStageIfOfflineAndReconnectReconcile(t *testing.T) {
	store := newFakeStore()
	var online atomic.Bool
	var writeMu sync.Mutex
	var seenParamsWrites []map[string]int
	var seenEIDWrites int
	online.Store(true)

	rpc := &fakeRPC{fn: func(_ context.Context, req RPCRequest) (RPCResponse, error) {
		if !online.Load() {
			return RPCResponse{}, ErrTransportUnavailable
		}
		switch req.Operation {
		case OperationReadStatus:
			p := defaultConcentratorParams(req.UnitID)
			return RPCResponse{
				SchemaVersion: rpcSchemaVersion, RequestID: req.RequestID, GatewayID: req.GatewayID, UnitID: req.UnitID,
				Operation: OperationReadStatus, Status: "OK", RespondedAt: store.clock.UnixMilli(),
				Params: &p,
			}, nil
		case OperationWriteParams:
			writeMu.Lock()
			seenParamsWrites = append(seenParamsWrites, req.Params)
			writeMu.Unlock()
			p := defaultConcentratorParams(req.UnitID)
			if v, ok := req.Params["rfTxPower"]; ok {
				p.RfTxPower = v
			}
			return RPCResponse{
				SchemaVersion: rpcSchemaVersion, RequestID: req.RequestID, GatewayID: req.GatewayID, UnitID: req.UnitID,
				Operation: OperationWriteParams, Status: "OK", RespondedAt: store.clock.UnixMilli(), WrittenCount: len(req.Params),
				Params: &p,
			}, nil
		case OperationWriteEIDs:
			writeMu.Lock()
			seenEIDWrites++
			writeMu.Unlock()
			return RPCResponse{
				SchemaVersion: rpcSchemaVersion, RequestID: req.RequestID, GatewayID: req.GatewayID, UnitID: req.UnitID,
				Operation: OperationWriteEIDs, Status: "OK", RespondedAt: store.clock.UnixMilli(), UpdatedSlots: 1, ChunkCount: 1,
				Slots: req.Slots,
			}, nil
		default:
			return RPCResponse{}, errors.New("unexpected rpc op")
		}
	}}

	svc := NewService(store, rpc)
	// Establish unit baseline before gateway goes offline.
	if _, err := svc.Status(context.Background(), testClaims(), testGatewayID(), 1); err != nil {
		t.Fatalf("baseline Status: %v", err)
	}
	online.Store(false)

	txPower := 26
	paramRes, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), 1, ParamsUpdateRequest{
		Params:         ParamsPatch{RfTxPower: &txPower},
		Reason:         "stage tx power while gateway offline",
		StageIfOffline: true,
	})
	if err != nil {
		t.Fatalf("offline UpdateParams with StageIfOffline failed: %v", err)
	}
	if !paramRes.Staged || paramRes.DesiredRevision != 1 || paramRes.Audit.Outcome != OutcomeStagedShadow {
		t.Fatalf("unexpected staged param result: %+v", paramRes)
	}

	eidRes, err := svc.UpdateEIDs(context.Background(), testClaims(), testGatewayID(), 1, EIDUpdateRequest{
		Slots:          makeFullSlots(33661799),
		Reason:         "stage eid slot 1 while offline",
		StageIfOffline: true,
	})
	if err != nil {
		t.Fatalf("offline UpdateEIDs with StageIfOffline failed: %v", err)
	}
	if !eidRes.Staged || eidRes.DesiredRevision != 2 || eidRes.Audit.Outcome != OutcomeStagedShadow {
		t.Fatalf("unexpected staged eid result: %+v", eidRes)
	}

	shadowBefore, _ := store.LoadGatewayConfigShadowByGatewayID(context.Background(), testGatewayID())
	if shadowBefore.SyncStatus != connectivity.ShadowSyncStatusPendingSync {
		t.Fatalf("expected PENDING_SYNC before reconnect, got %s", shadowBefore.SyncStatus)
	}

	online.Store(true)
	if err := svc.ReconcileConcentratorShadow(context.Background(), testGatewayID()); err != nil {
		t.Fatalf("ReconcileConcentratorShadow: %v", err)
	}
	writeMu.Lock()
	paramsCount := len(seenParamsWrites)
	txPowerWritten := 0
	if paramsCount > 0 {
		txPowerWritten = seenParamsWrites[0]["rfTxPower"]
	}
	eidCount := seenEIDWrites
	writeMu.Unlock()
	if paramsCount != 1 || txPowerWritten != 26 || eidCount != 1 {
		t.Fatalf("unexpected reconcile RPC writes: params=%+v eids=%d", paramsCount, eidCount)
	}

	shadowAfter, _ := store.LoadGatewayConfigShadowByGatewayID(context.Background(), testGatewayID())
	if shadowAfter.SyncStatus != connectivity.ShadowSyncStatusSynced {
		t.Fatalf("expected SYNCED after reconcile, got %s (drift=%+v)", shadowAfter.SyncStatus, shadowAfter.Drift)
	}

	// Second reconcile with no pending changes must send zero RPCs.
	if err := svc.ReconcileConcentratorShadow(context.Background(), testGatewayID()); err != nil {
		t.Fatalf("idempotent ReconcileConcentratorShadow: %v", err)
	}
	writeMu.Lock()
	paramsCount2 := len(seenParamsWrites)
	eidCount2 := seenEIDWrites
	writeMu.Unlock()
	if paramsCount2 != 1 || eidCount2 != 1 {
		t.Fatalf("idempotent reconcile sent unexpected RPCs: params=%d eids=%d", paramsCount2, eidCount2)
	}
}

func TestServiceOfflineStagingBaselineTimeoutModbusRenameEIDClearAndSerialization(t *testing.T) {
	t.Run("partial offline patch without baseline fails while full 14-field patch succeeds", func(t *testing.T) {
		store := newFakeStore()
		rpc := &fakeRPC{fn: func(_ context.Context, _ RPCRequest) (RPCResponse, error) {
			return RPCResponse{}, ErrTransportUnavailable
		}}
		svc := NewService(store, rpc)
		p := 25
		if _, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), 1, ParamsUpdateRequest{
			Params: ParamsPatch{RfTxPower: &p}, Reason: "partial without baseline", StageIfOffline: true,
		}); !errors.Is(err, ErrParamsInvalid) {
			t.Fatalf("expected ErrParamsInvalid without baseline, got %v", err)
		}

		v := func(x int) *int { return &x }
		res, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), 1, ParamsUpdateRequest{
			Params: ParamsPatch{
				ModbusID: v(1), BaudRateCode: v(3), OffLineTime: v(60), DataUplinkCycle: v(30),
				LocalNet: v(100), TimeMark: v(1), RfTxPower: v(25), RfFrequency: v(470),
				RfBandWidth: v(2), RfSf: v(7), RfLeadCode: v(8), TxTimeout: v(3000), RxTimeout: v(3000), RssiOut: v(1),
			},
			Reason:         "complete 14-field offline init",
			StageIfOffline: true,
		})
		if err != nil || !res.Staged || res.Params.RfTxPower != 25 {
			t.Fatalf("full 14-field offline stage got %+v err=%v", res, err)
		}
	})

	t.Run("offline modbusId change addresses current unit then rekeys and clears removed EIDs", func(t *testing.T) {
		store := newFakeStore()
		base := defaultConcentratorParams(1)
		cp := connectivity.ConcentratorShadowParams(base)
		store.reported = &connectivity.GatewayReportedConfig{
			ReportedRevision: 1,
			AppliedStatus:    connectivity.AppliedStatusApplied,
			PointMappings:    []connectivity.GatewayPointMapping{},
			SamplingPolicies: []connectivity.GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]connectivity.ConcentratorUnitShadow{
				"1": {
					Params: &cp,
					EIDs:   []connectivity.ConcentratorShadowEIDSlot{{Slot: 1, EID: 1001}},
				},
			},
			DriftDetails: []connectivity.DriftDetail{},
		}
		desiredParams := cp
		desiredParams.ModbusID = 7
		store.desired = &connectivity.GatewayDesiredConfig{
			DesiredRevision:  2,
			PointMappings:    []connectivity.GatewayPointMapping{},
			SamplingPolicies: []connectivity.GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]connectivity.ConcentratorUnitShadow{
				"1": {
					Params: &desiredParams,
					EIDs:   nil, // cleared!
				},
			},
		}

		var mu sync.Mutex
		var ops []string
		rpc := &fakeRPC{fn: func(_ context.Context, req RPCRequest) (RPCResponse, error) {
			mu.Lock()
			ops = append(ops, req.Operation+":"+strconv.Itoa(req.UnitID))
			mu.Unlock()
			switch req.Operation {
			case OperationWriteParams:
				if req.UnitID != 1 {
					return RPCResponse{}, errors.New("expected write to current bus unit 1")
				}
				p := defaultConcentratorParams(7)
				return RPCResponse{SchemaVersion: rpcSchemaVersion, RequestID: req.RequestID, GatewayID: req.GatewayID, UnitID: 1, Operation: OperationWriteParams, Status: "OK", Params: &p, WrittenCount: 1}, nil
			case OperationWriteEIDs:
				if req.UnitID != 7 {
					return RPCResponse{}, errors.New("expected EID clear addressed to renamed unit 7")
				}
				return RPCResponse{SchemaVersion: rpcSchemaVersion, RequestID: req.RequestID, GatewayID: req.GatewayID, UnitID: 7, Operation: OperationWriteEIDs, Status: "OK", Slots: makeFullSlots(0)}, nil
			default:
				return RPCResponse{}, errors.New("unexpected op")
			}
		}}
		svc := NewService(store, rpc)
		if err := svc.ReconcileConcentratorShadow(context.Background(), testGatewayID()); err != nil {
			t.Fatalf("ReconcileConcentratorShadow: %v", err)
		}
		mu.Lock()
		gotOps := append([]string(nil), ops...)
		mu.Unlock()
		if len(gotOps) != 2 || gotOps[0] != "WRITE_PARAMS:1" || gotOps[1] != "WRITE_EIDS:7" {
			t.Fatalf("ops=%v, want [WRITE_PARAMS:1 WRITE_EIDS:7]", gotOps)
		}
		shadow, _ := store.LoadGatewayConfigShadowByGatewayID(context.Background(), testGatewayID())
		if shadow.SyncStatus != connectivity.ShadowSyncStatusSynced || shadow.Desired.ConcentratorConfigs["7"].Params == nil || shadow.Reported.ConcentratorConfigs["7"].Params == nil {
			t.Fatalf("expected rekeyed SYNCED unit 7, got %+v", shadow)
		}
		if _, oldExists := shadow.Reported.ConcentratorConfigs["1"]; oldExists {
			t.Fatalf("expected old key 1 removed after modbusId rename")
		}
	})

	t.Run("rpc timeout expiring caller ctx still stages shadow and offline unreachable keeps PENDING_SYNC", func(t *testing.T) {
		store := newFakeStore()
		base := defaultConcentratorParams(1)
		cp := connectivity.ConcentratorShadowParams(base)
		store.reported = &connectivity.GatewayReportedConfig{
			PointMappings:       []connectivity.GatewayPointMapping{},
			SamplingPolicies:    []connectivity.GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]connectivity.ConcentratorUnitShadow{"1": {Params: &cp}},
		}
		rpc := &fakeRPC{fn: func(rpcCtx context.Context, _ RPCRequest) (RPCResponse, error) {
			<-rpcCtx.Done()
			return RPCResponse{}, ErrRPCTimeout
		}}
		svc := NewService(store, rpc)

		shortCtx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		defer cancel()
		pw := 18
		staged, err := svc.UpdateParams(shortCtx, testClaims(), testGatewayID(), 1, ParamsUpdateRequest{
			Params: ParamsPatch{RfTxPower: &pw}, Reason: "rpc timeout stage", StageIfOffline: true,
		})
		if err != nil || !staged.Staged || staged.Params.RfTxPower != 18 {
			t.Fatalf("expected StageIfOffline to survive caller ctx timeout, got %+v err=%v", staged, err)
		}
		if shortCtx.Err() == nil {
			t.Fatal("expected caller ctx to be expired after RPC timeout")
		}

		// Reconcile while unreachable must stay PENDING_SYNC instead of flipping to DRIFTED.
		reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		defer reconcileCancel()
		_ = svc.ReconcileConcentratorShadow(reconcileCtx, testGatewayID())
		shadow, _ := store.LoadGatewayConfigShadowByGatewayID(context.Background(), testGatewayID())
		if shadow.SyncStatus != connectivity.ShadowSyncStatusPendingSync {
			t.Fatalf("expected PENDING_SYNC when gateway unreachable, got %s", shadow.SyncStatus)
		}
	})

	t.Run("concurrent ReconcileConcentratorShadow calls serialize per gateway", func(t *testing.T) {
		store := newFakeStore()
		p := connectivity.ConcentratorShadowParams(defaultConcentratorParams(1))
		p.RfTxPower = 24
		store.desired = &connectivity.GatewayDesiredConfig{
			DesiredRevision:     1,
			PointMappings:       []connectivity.GatewayPointMapping{},
			SamplingPolicies:    []connectivity.GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]connectivity.ConcentratorUnitShadow{"1": {Params: &p}},
		}
		var inFlight, maxInFlight, totalCalls atomic.Int32
		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once

		rpc := &fakeRPC{fn: func(_ context.Context, req RPCRequest) (RPCResponse, error) {
			totalCalls.Add(1)
			cur := inFlight.Add(1)
			for {
				prev := maxInFlight.Load()
				if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			once.Do(func() {
				close(entered)
				<-release
			})
			inFlight.Add(-1)
			ret := defaultConcentratorParams(1)
			ret.RfTxPower = 24
			return RPCResponse{SchemaVersion: rpcSchemaVersion, RequestID: req.RequestID, GatewayID: req.GatewayID, UnitID: 1, Operation: OperationWriteParams, Status: "OK", Params: &ret}, nil
		}}
		svc := NewService(store, rpc)
		errCh := make(chan error, 2)
		go func() { errCh <- svc.ReconcileConcentratorShadow(context.Background(), testGatewayID()) }()
		<-entered
		go func() { errCh <- svc.ReconcileConcentratorShadow(context.Background(), testGatewayID()) }()
		time.Sleep(25 * time.Millisecond)
		close(release)
		for i := 0; i < 2; i++ {
			if err := <-errCh; err != nil {
				t.Fatalf("reconcile %d failed: %v", i, err)
			}
		}
		if maxInFlight.Load() != 1 || totalCalls.Load() != 1 {
			t.Fatalf("expected serialized 1 RPC call, got maxInFlight=%d totalCalls=%d", maxInFlight.Load(), totalCalls.Load())
		}
	})

	t.Run("online UpdateParams serializes behind active ReconcileConcentratorShadow so stale reconcile never overwrites unit 1", func(t *testing.T) {
		store := newFakeStore()
		p1 := connectivity.ConcentratorShadowParams(defaultConcentratorParams(1))
		p1.RfTxPower = 24
		p7 := connectivity.ConcentratorShadowParams(defaultConcentratorParams(7))
		p7.RfTxPower = 25
		store.desired = &connectivity.GatewayDesiredConfig{
			DesiredRevision:  1,
			PointMappings:    []connectivity.GatewayPointMapping{},
			SamplingPolicies: []connectivity.GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]connectivity.ConcentratorUnitShadow{
				"1": {Params: &p1},
				"7": {Params: &p7},
			},
		}

		unit7Entered := make(chan struct{})
		releaseUnit7 := make(chan struct{})
		var unit7Once sync.Once
		rpc := &fakeRPC{fn: func(_ context.Context, req RPCRequest) (RPCResponse, error) {
			if req.UnitID == 7 {
				unit7Once.Do(func() { close(unit7Entered) })
				<-releaseUnit7
			}
			ret := defaultConcentratorParams(req.UnitID)
			if v, ok := req.Params["rfTxPower"]; ok {
				ret.RfTxPower = v
			}
			return RPCResponse{
				SchemaVersion: rpcSchemaVersion, RequestID: req.RequestID, GatewayID: req.GatewayID,
				UnitID: req.UnitID, Operation: OperationWriteParams, Status: "OK", Params: &ret,
			}, nil
		}}

		svc := NewService(store, rpc)
		reconcileDone := make(chan error, 1)
		go func() {
			reconcileDone <- svc.ReconcileConcentratorShadow(context.Background(), testGatewayID())
		}()

		// Reconcile has finished unit 1 (unlocked unit 1 mutex) and is paused in unit 7.
		select {
		case <-unit7Entered:
		case err := <-reconcileDone:
			t.Fatalf("reconcile finished unexpectedly before reaching unit 7: %v", err)
		}

		updateErr := make(chan error, 1)
		go func() {
			newPower := 28
			_, err := svc.UpdateParams(context.Background(), testClaims(), testGatewayID(), 1, ParamsUpdateRequest{
				Params: ParamsPatch{RfTxPower: &newPower},
				Reason: "concurrent operator write during unit 7 reconcile",
			})
			updateErr <- err
		}()

		// Give UpdateParams time to reach prepareWrite; it must wait for ReconcileConcentratorShadow to finish.
		time.Sleep(25 * time.Millisecond)
		close(releaseUnit7)

		if err := <-reconcileDone; err != nil {
			t.Fatalf("reconcile failed: %v", err)
		}
		if err := <-updateErr; err != nil {
			t.Fatalf("concurrent UpdateParams failed: %v", err)
		}
		shadow, _ := store.LoadGatewayConfigShadowByGatewayID(context.Background(), testGatewayID())
		if shadow.Desired == nil || shadow.Reported == nil {
			t.Fatalf("expected desired and reported shadows populated: %+v", shadow)
		}
		if shadow.Desired.ConcentratorConfigs["1"].Params.RfTxPower != 28 || shadow.Reported.ConcentratorConfigs["1"].Params.RfTxPower != 28 {
			t.Fatalf("expected newer UpdateParams rfTxPower=28 preserved without stale reconcile overwrite, desired=%d reported=%d",
				shadow.Desired.ConcentratorConfigs["1"].Params.RfTxPower, shadow.Reported.ConcentratorConfigs["1"].Params.RfTxPower)
		}
	})
}

func TestMQTTTransportMetricsAndPacketHook(t *testing.T) {
	cfg, serverTLS := writeConcentratorTestPKI(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg.BrokerURL = "tls://" + listener.Addr().String()

	var dropReply atomic.Bool
	go func() {
		for {
			c, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(sc net.Conn) {
				defer sc.Close()
				pkt, err := packets.ReadPacket(sc)
				if err != nil || pkt.Type != packets.CONNECT {
					return
				}
				connack := packets.NewControlPacket(packets.CONNACK)
				connack.Content.(*packets.Connack).SessionPresent = false
				if _, err := connack.WriteTo(sc); err != nil {
					return
				}
				for {
					p, err := packets.ReadPacket(sc)
					if err != nil {
						return
					}
					switch p.Type {
					case packets.SUBSCRIBE:
						sub := p.Content.(*packets.Subscribe)
						suback := packets.NewControlPacket(packets.SUBACK)
						sa := suback.Content.(*packets.Suback)
						sa.PacketID = sub.PacketID
						for range sub.Subscriptions {
							sa.Reasons = append(sa.Reasons, 1)
						}
						_, _ = suback.WriteTo(sc)
					case packets.PUBLISH:
						pub := p.Content.(*packets.Publish)
						puback := packets.NewControlPacket(packets.PUBACK)
						puback.Content.(*packets.Puback).PacketID = pub.PacketID
						_, _ = puback.WriteTo(sc)
						if dropReply.Load() {
							continue
						}
						var req RPCRequest
						if err := json.Unmarshal(pub.Payload, &req); err == nil {
							replyBytes, _ := json.Marshal(RPCResponse{
								SchemaVersion: rpcSchemaVersion,
								RequestID:     req.RequestID,
								GatewayID:     req.GatewayID,
								UnitID:        req.UnitID,
								Operation:     req.Operation,
								Status:        "OK",
							})
							replyPkt := packets.NewControlPacket(packets.PUBLISH)
							rp := replyPkt.Content.(*packets.Publish)
							rp.Topic = "hvac/v1/" + req.GatewayID + "/up/concentrator"
							rp.Payload = replyBytes
							rp.QoS = 1
							rp.PacketID = 77
							_, _ = replyPkt.WriteTo(sc)
						}
					case packets.PINGREQ:
						_, _ = packets.NewControlPacket(packets.PINGRESP).WriteTo(sc)
					}
				}
			}(c)
		}
	}()

	type tappedPacket struct {
		direction string
		channel   string
		gatewayID string
		topic     string
		traceID   string
		payload   string
	}
	var tappedMu sync.Mutex
	var tapped []tappedPacket

	registry := observability.NewRegistry()
	cfg.Metrics = registry
	cfg.RPCTimeout = 100 * time.Millisecond
	cfg.PacketHook = func(direction, channel, gatewayID, topic, traceID string, payload []byte) {
		tappedMu.Lock()
		tapped = append(tapped, tappedPacket{
			direction: direction,
			channel:   channel,
			gatewayID: gatewayID,
			topic:     topic,
			traceID:   traceID,
			payload:   string(payload),
		})
		tappedMu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr, err := NewMQTTTransport(ctx, cfg)
	if err != nil {
		t.Fatalf("NewMQTTTransport: %v", err)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutCancel()
		_ = tr.Close(shutCtx)
	}()

	if _, err := tr.Call(context.Background(), RPCRequest{
		SchemaVersion: rpcSchemaVersion,
		RequestID:     "018f3d00-3000-7000-8000-000000000010",
		GatewayID:     testGatewayID(),
		UnitID:        1,
		Operation:     OperationReadStatus,
	}); !errors.Is(err, ErrTransportUnavailable) {
		t.Fatalf("unsubscribed Call err=%v, want ErrTransportUnavailable", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer waitCancel()
	if err := tr.WaitReady(waitCtx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	tracer := observability.NewTracer("concentrator-test", nil)
	const wantTrace = "0123456789abcdef0123456789abcdef"
	callCtx := observability.ContextWithRemoteParent(context.Background(), tracer, "00-"+wantTrace+"-0000000000000001-01", "")
	resp, err := tr.Call(callCtx, RPCRequest{
		SchemaVersion: rpcSchemaVersion,
		RequestID:     "018f3d00-3000-7000-8000-000000000011",
		GatewayID:     testGatewayID(),
		UnitID:        1,
		Operation:     OperationReadStatus,
	})
	if err != nil || resp.Status != "OK" {
		t.Fatalf("success Call=(%+v, %v)", resp, err)
	}

	dropReply.Store(true)
	if _, err := tr.Call(context.Background(), RPCRequest{
		SchemaVersion: rpcSchemaVersion,
		RequestID:     "018f3d00-3000-7000-8000-000000000012",
		GatewayID:     testGatewayID(),
		UnitID:        1,
		Operation:     OperationReadStatus,
	}); !errors.Is(err, ErrRPCTimeout) {
		t.Fatalf("timed-out Call err=%v, want ErrRPCTimeout", err)
	}

	tappedMu.Lock()
	packetsCopy := append([]tappedPacket(nil), tapped...)
	tappedMu.Unlock()
	if len(packetsCopy) < 3 {
		t.Fatalf("expected >= 3 tapped packets (downlink+uplink for success, downlink for timeout), got %#v", packetsCopy)
	}
	if packetsCopy[0].direction != "DOWNLINK" || packetsCopy[0].channel != "CONCENTRATOR" || packetsCopy[0].gatewayID != testGatewayID() ||
		packetsCopy[0].topic != DownlinkTopic(testGatewayID()) || packetsCopy[0].traceID != wantTrace {
		t.Fatalf("unexpected downlink packet[0]: %#v", packetsCopy[0])
	}
	if packetsCopy[1].direction != "UPLINK" || packetsCopy[1].channel != "CONCENTRATOR" || packetsCopy[1].gatewayID != testGatewayID() ||
		packetsCopy[1].topic != "hvac/v1/"+testGatewayID()+"/up/concentrator" || packetsCopy[1].traceID != wantTrace {
		t.Fatalf("unexpected uplink packet[1]: %#v", packetsCopy[1])
	}

	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`hvac_mqtt_concentrator_rpc_total{outcome="success"} 1`,
		`hvac_mqtt_concentrator_rpc_total{outcome="failed"} 1`,
		`hvac_mqtt_concentrator_rpc_total{outcome="timed_out"} 1`,
		`hvac_mqtt_concentrator_rpc_duration_seconds_count{outcome="success"} 1`,
		`hvac_mqtt_concentrator_rpc_duration_seconds_count{outcome="failed"} 1`,
		`hvac_mqtt_concentrator_rpc_duration_seconds_count{outcome="timed_out"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in concentrator metrics:\n%s", want, body)
		}
	}
}

func TestSharedMQTTTransportRPCAndCloseLeavesSessionOpen(t *testing.T) {
	cfg, serverTLS := writeConcentratorTestPKI(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg.BrokerURL = "tls://" + listener.Addr().String()
	cfg.RPCTimeout = 2 * time.Second

	var unsubMu sync.Mutex
	var unsubTopics []string
	var serverWriteMu sync.Mutex
	var activeServerConn net.Conn
	var nextServerID atomic.Uint32

	go func() {
		for {
			c, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(sc net.Conn) {
				defer sc.Close()
				pkt, err := packets.ReadPacket(sc)
				if err != nil || pkt.Type != packets.CONNECT {
					return
				}
				connack := packets.NewControlPacket(packets.CONNACK)
				connack.Content.(*packets.Connack).SessionPresent = false
				serverWriteMu.Lock()
				_, writeErr := connack.WriteTo(sc)
				activeServerConn = sc
				serverWriteMu.Unlock()
				if writeErr != nil {
					return
				}
				for {
					p, err := packets.ReadPacket(sc)
					if err != nil {
						return
					}
					switch p.Type {
					case packets.SUBSCRIBE:
						sub := p.Content.(*packets.Subscribe)
						suback := packets.NewControlPacket(packets.SUBACK)
						sa := suback.Content.(*packets.Suback)
						sa.PacketID = sub.PacketID
						for range sub.Subscriptions {
							sa.Reasons = append(sa.Reasons, 1)
						}
						serverWriteMu.Lock()
						_, _ = suback.WriteTo(sc)
						serverWriteMu.Unlock()
					case packets.UNSUBSCRIBE:
						unsub := p.Content.(*packets.Unsubscribe)
						unsubMu.Lock()
						unsubTopics = append(unsubTopics, unsub.Topics...)
						unsubMu.Unlock()
						unsuback := packets.NewControlPacket(packets.UNSUBACK)
						ua := unsuback.Content.(*packets.Unsuback)
						ua.PacketID = unsub.PacketID
						for range unsub.Topics {
							ua.Reasons = append(ua.Reasons, 0)
						}
						serverWriteMu.Lock()
						_, _ = unsuback.WriteTo(sc)
						serverWriteMu.Unlock()
					case packets.PUBLISH:
						pub := p.Content.(*packets.Publish)
						puback := packets.NewControlPacket(packets.PUBACK)
						puback.Content.(*packets.Puback).PacketID = pub.PacketID
						serverWriteMu.Lock()
						_, _ = puback.WriteTo(sc)
						serverWriteMu.Unlock()

						var req RPCRequest
						if err := json.Unmarshal(pub.Payload, &req); err == nil {
							replyBytes, _ := json.Marshal(RPCResponse{
								SchemaVersion: rpcSchemaVersion,
								RequestID:     req.RequestID,
								GatewayID:     req.GatewayID,
								UnitID:        req.UnitID,
								Operation:     req.Operation,
								Status:        "OK",
							})
							replyPkt := packets.NewControlPacket(packets.PUBLISH)
							rp := replyPkt.Content.(*packets.Publish)
							rp.Topic = "hvac/v1/" + req.GatewayID + "/up/concentrator"
							rp.Payload = replyBytes
							rp.QoS = 1
							rp.PacketID = uint16(nextServerID.Add(1))
							serverWriteMu.Lock()
							_, _ = replyPkt.WriteTo(sc)
							serverWriteMu.Unlock()
						}
					case packets.PINGREQ:
						serverWriteMu.Lock()
						_, _ = packets.NewControlPacket(packets.PINGRESP).WriteTo(sc)
						serverWriteMu.Unlock()
					case packets.DISCONNECT:
						return
					}
				}
			}(c)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := mqttclient.NewSharedSession(ctx, mqttclient.SharedSessionConfig{
		BrokerURL: cfg.BrokerURL,
		ClientID:  cfg.ClientID,
		TLS: &mqttclient.TLSConfig{
			CACertFile:     cfg.CAFile,
			ClientCertFile: cfg.CertFile,
			ClientKeyFile:  cfg.KeyFile,
			ServerName:     cfg.ServerName,
		},
		ConnectRetryDelay:    50 * time.Millisecond,
		SubscribeRetryDelays: []time.Duration{20 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewSharedSession: %v", err)
	}
	defer func() { _ = session.Close(context.Background()) }()

	siblingCh := make(chan string, 4)
	unregSibling, err := session.RegisterRoute("sibling-uplink", []string{"hvac/v1/+/up/telemetry"}, func(pkt *paho.Publish) {
		siblingCh <- string(pkt.Payload)
	})
	if err != nil {
		t.Fatalf("RegisterRoute sibling: %v", err)
	}
	defer func() { _ = unregSibling(context.Background()) }()

	tr, err := NewSharedMQTTTransport(ctx, session, cfg)
	if err != nil {
		t.Fatalf("NewSharedMQTTTransport: %v", err)
	}
	if err := tr.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady(shared): %v", err)
	}

	var callWG sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		callWG.Add(1)
		go func(idx int) {
			defer callWG.Done()
			reqID := "018f3d00-4000-7000-8000-00000000000" + strconv.Itoa(idx)
			resp, err := tr.Call(ctx, RPCRequest{
				SchemaVersion: rpcSchemaVersion,
				RequestID:     reqID,
				GatewayID:     testGatewayID(),
				UnitID:        1,
				Operation:     OperationReadStatus,
			})
			if err != nil || resp.Status != "OK" || resp.RequestID != reqID {
				errs <- fmt.Errorf("call %d failed: resp=%+v err=%v", idx, resp, err)
			}
		}(i)
	}
	callWG.Wait()
	close(errs)
	for callErr := range errs {
		t.Fatal(callErr)
	}

	// Closing shared transport unregisters its reply route without closing the shared session.
	if err := tr.Close(ctx); err != nil {
		t.Fatalf("tr.Close(shared): %v", err)
	}
	if tr.Subscribed() {
		t.Fatal("expected tr.Subscribed() == false after Close")
	}
	unsubMu.Lock()
	unsubscribedReply := slices.Contains(unsubTopics, ReplyTopicFilter)
	unsubMu.Unlock()
	if !unsubscribedReply {
		t.Fatalf("expected Close to UNSUBSCRIBE %q, got %v", ReplyTopicFilter, unsubTopics)
	}
	if st := session.State(); st.Closed || !st.Connected {
		t.Fatalf("shared session must stay open and connected after transport.Close, got %+v", st)
	}

	// Verify sibling route still receives traffic on the shared session.
	telePkt := packets.NewControlPacket(packets.PUBLISH)
	tp := telePkt.Content.(*packets.Publish)
	tp.Topic = "hvac/v1/" + testGatewayID() + "/up/telemetry"
	tp.Payload = []byte("still-alive")
	tp.QoS = 1
	tp.PacketID = uint16(nextServerID.Add(1))
	serverWriteMu.Lock()
	sc := activeServerConn
	if sc != nil {
		_, _ = telePkt.WriteTo(sc)
	}
	serverWriteMu.Unlock()

	select {
	case got := <-siblingCh:
		if got != "still-alive" {
			t.Fatalf("unexpected sibling payload: %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sibling route failed after shared concentrator transport Close")
	}
}
