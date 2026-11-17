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
	"math"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/packets"
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

func (f *fakeStore) RecordConcentratorAudit(_ context.Context, _ string, _ string, entry connectivity.ConcentratorAuditEntry) (connectivity.ConcentratorAuditEntry, error) {
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
	wrapped := connectivity.NewCredentialHandlerWithExtensions(nil, nil, nil, rawHandler)

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
