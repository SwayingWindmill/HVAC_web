package concentrator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
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
