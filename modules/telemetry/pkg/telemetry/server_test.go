package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	gatewaySPIFFE    = "spiffe://hvac.local/platform-gateway"
	operationsSPIFFE = "spiffe://hvac.local/operations-agent-service"
)

type fakeAuthorizer struct {
	err     error
	access  AccessContext
	peer    string
	grant   string
	action  telemetryauth.Action
	targets []telemetryauth.Target
}

func (fake *fakeAuthorizer) Authorize(_ context.Context, peer, grant string, action telemetryauth.Action, targets []telemetryauth.Target) (AccessContext, error) {
	fake.peer, fake.grant, fake.action = peer, grant, action
	fake.targets = append([]telemetryauth.Target(nil), targets...)
	if fake.err != nil {
		return AccessContext{}, fake.err
	}
	if fake.access.PrincipalID != "" {
		return fake.access, nil
	}
	return AccessContext{PrincipalID: "018f2e00-2000-7000-8000-000000000001", Subject: "subject-a", SubjectIssuer: "https://issuer.example.test", SessionID: "session-a", TenantID: tenantA, TokenID: "grant-a", PolicyRevision: "telemetry-access:1"}, nil
}

type fakeSnapshotStore struct {
	calls       []telemetryauth.Target
	errByDevice map[string]error
	latest      map[string]telemetryapi.DeviceObservationSnapshot
	latestErr   error
	latestCalls []string
}

func (fake *fakeSnapshotStore) LatestSnapshot(_ context.Context, tenantID, deviceID string) (telemetryapi.DeviceObservationSnapshot, error) {
	fake.latestCalls = append(fake.latestCalls, deviceID)
	if fake.latestErr != nil {
		return telemetryapi.DeviceObservationSnapshot{}, fake.latestErr
	}
	snapshot, ok := fake.latest[deviceID]
	if !ok || string(snapshot.TenantId) != tenantID {
		return telemetryapi.DeviceObservationSnapshot{}, ErrSnapshotNotFound
	}
	return snapshot, nil
}

func (fake *fakeSnapshotStore) EvaluateAndRead(_ context.Context, target telemetryauth.Target, evaluatedAt time.Time) (SnapshotCommit, error) {
	fake.calls = append(fake.calls, target)
	if err := fake.errByDevice[target.DeviceID]; err != nil {
		return SnapshotCommit{}, err
	}
	return SnapshotCommit{Snapshot: snapshotFixture(target.DeviceID, evaluatedAt, target.Keys)}, nil
}

func snapshotFixture(deviceID string, evaluatedAt time.Time, keys []string) telemetryapi.DeviceObservationSnapshot {
	state := telemetryapi.DevicePresenceStateOnline
	display := telemetryapi.DeviceDisplayStateOnline
	values := make([]telemetryapi.TelemetryKeyState, 0, len(keys))
	for _, key := range keys {
		values = append(values, telemetryapi.TelemetryKeyState{Missing: &telemetryapi.TelemetryMissingState{Key: telemetryapi.TelemetryKey(key), State: "MISSING", Freshness: "MISSING", MissingReason: "NEVER_OBSERVED"}})
	}
	readiness := telemetryapi.TelemetryReadinessIncomplete
	if len(keys) == 0 {
		readiness = telemetryapi.TelemetryReadinessNotApplicable
	}
	return telemetryapi.DeviceObservationSnapshot{
		SchemaVersion: 1, TenantId: telemetryapi.UUIDv7(tenantA), SiteId: telemetryapi.UUIDv7(siteA), DeviceId: telemetryapi.UUIDv7(deviceID),
		BusinessRevision: 3, EvaluatedAt: instant(evaluatedAt), EvaluationAvailability: telemetryapi.EvaluationAvailabilityAvailable,
		AvailabilityReasons: []telemetryapi.AvailabilityReasonCode{}, Presence: telemetryapi.PresenceSnapshot{Applicability: telemetryapi.PresenceApplicabilityApplicable, CurrentState: &state},
		TelemetryReadiness: readiness, DisplayState: &display, Values: values,
	}
}

type fakeLatestCache struct {
	snapshots  map[string]telemetryapi.DeviceObservationSnapshot
	err        error
	calls      []string
	batchCalls int
}

func (fake *fakeLatestCache) PutIfNewer(_ context.Context, snapshot telemetryapi.DeviceObservationSnapshot) (bool, error) {
	if fake.snapshots == nil {
		fake.snapshots = map[string]telemetryapi.DeviceObservationSnapshot{}
	}
	fake.snapshots[string(snapshot.DeviceId)] = snapshot
	return true, nil
}

func (fake *fakeLatestCache) Get(_ context.Context, tenantID, siteID, deviceID string) (telemetryapi.DeviceObservationSnapshot, error) {
	snapshot, err := fake.GetForDevice(context.Background(), tenantID, deviceID)
	if err != nil {
		return telemetryapi.DeviceObservationSnapshot{}, err
	}
	if string(snapshot.SiteId) != siteID {
		return telemetryapi.DeviceObservationSnapshot{}, ErrLatestCacheMiss
	}
	return snapshot, nil
}

func (fake *fakeLatestCache) GetForDevice(_ context.Context, tenantID, deviceID string) (telemetryapi.DeviceObservationSnapshot, error) {
	fake.calls = append(fake.calls, deviceID)
	if fake.err != nil {
		return telemetryapi.DeviceObservationSnapshot{}, fake.err
	}
	snapshot, ok := fake.snapshots[deviceID]
	if !ok || string(snapshot.TenantId) != tenantID {
		return telemetryapi.DeviceObservationSnapshot{}, ErrLatestCacheMiss
	}
	return snapshot, nil
}

func (fake *fakeLatestCache) BatchGetForDevices(_ context.Context, tenantID string, deviceIDs []string) (map[string]telemetryapi.DeviceObservationSnapshot, error) {
	fake.batchCalls++
	fake.calls = append(fake.calls, deviceIDs...)
	if fake.err != nil {
		return nil, fake.err
	}
	result := make(map[string]telemetryapi.DeviceObservationSnapshot, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		snapshot, ok := fake.snapshots[deviceID]
		if ok && string(snapshot.TenantId) == tenantID {
			result[deviceID] = snapshot
		}
	}
	return result, nil
}

func (fake *fakeLatestCache) Close() error { return nil }

func (fake *fakeLatestCache) Ping(context.Context) error { return nil }

func TestInternalSingleSnapshotRequiresGatewayIdentityAndPreservesSelection(t *testing.T) {
	authorizer := &fakeAuthorizer{}
	store := &fakeSnapshotStore{errByDevice: map[string]error{}}
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	latestCache := &fakeLatestCache{snapshots: map[string]telemetryapi.DeviceObservationSnapshot{
		deviceA: snapshotFixture(deviceA, now, []string{"zone.humidity", "zone.temperature"}),
	}}
	handler := NewHandler(ServerConfig{
		Store: store, LatestCache: latestCache, Authorizer: authorizer,
		AllowedGatewaySPIFFE:         gatewaySPIFFE,
		AllowedSnapshotReaderSPIFFEs: []string{gatewaySPIFFE, operationsSPIFFE},
		Now:                          func() time.Time { return now },
	})

	request := httptest.NewRequest(http.MethodGet, InternalDeviceSnapshotPrefix+deviceA+"/observation-snapshot?key=zone.humidity&key=zone.temperature", nil)
	request.Header.Set("Authorization", "Bearer signed-grant")
	request.TLS = verifiedTLSState(gatewaySPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var snapshot telemetryapi.DeviceObservationSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Values) != 2 || snapshot.Values[0].Missing == nil || snapshot.Values[0].Missing.Key != "zone.humidity" || snapshot.Values[1].Missing == nil || snapshot.Values[1].Missing.Key != "zone.temperature" {
		t.Fatalf("values=%#v", snapshot.Values)
	}
	if authorizer.peer != gatewaySPIFFE || authorizer.grant != "signed-grant" || authorizer.action != telemetryauth.ActionSnapshotRead {
		t.Fatalf("authorization=%#v", authorizer)
	}
	if len(authorizer.targets) != 1 || authorizer.targets[0].DeviceID != deviceA || strings.Join(authorizer.targets[0].Keys, ",") != "zone.humidity,zone.temperature" {
		t.Fatalf("targets=%#v", authorizer.targets)
	}

	operationsRequest := httptest.NewRequest(http.MethodGet, InternalDeviceSnapshotPrefix+deviceA+"/observation-snapshot?key=zone.temperature", nil)
	operationsRequest.Header.Set("Authorization", "Bearer operations-grant")
	operationsRequest.TLS = verifiedTLSState(operationsSPIFFE)
	operationsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(operationsRecorder, operationsRequest)
	if operationsRecorder.Code != http.StatusOK || authorizer.peer != operationsSPIFFE || authorizer.grant != "operations-grant" {
		t.Fatalf("operations snapshot status=%d authorization=%#v body=%s", operationsRecorder.Code, authorizer, operationsRecorder.Body.String())
	}

	presenceRequest := httptest.NewRequest(http.MethodGet, InternalDeviceSnapshotPrefix+deviceA+"/observation-snapshot", nil)
	presenceRequest.Header.Set("Authorization", "Bearer presence-grant")
	presenceRequest.TLS = verifiedTLSState(gatewaySPIFFE)
	presenceRecorder := httptest.NewRecorder()
	handler.ServeHTTP(presenceRecorder, presenceRequest)
	if presenceRecorder.Code != http.StatusOK {
		t.Fatalf("presence status=%d body=%s", presenceRecorder.Code, presenceRecorder.Body.String())
	}
	var presenceSnapshot telemetryapi.DeviceObservationSnapshot
	if err := json.Unmarshal(presenceRecorder.Body.Bytes(), &presenceSnapshot); err != nil {
		t.Fatal(err)
	}
	if len(presenceSnapshot.Values) != 0 || presenceSnapshot.TelemetryReadiness != telemetryapi.TelemetryReadinessNotApplicable {
		t.Fatalf("presence-only snapshot leaked telemetry values: %#v", presenceSnapshot)
	}
	if len(store.calls) != 0 || len(store.latestCalls) != 0 || len(latestCache.calls) != 3 {
		t.Fatalf("current read used unexpected authority: store=%#v cache=%#v", store.calls, latestCache.calls)
	}
}

func TestInternalSnapshotRejectsForgedIdentityAndWrongWorkload(t *testing.T) {
	handler := NewHandler(ServerConfig{Store: &fakeSnapshotStore{errByDevice: map[string]error{}}, Authorizer: &fakeAuthorizer{}, AllowedGatewaySPIFFE: gatewaySPIFFE})
	forged := httptest.NewRequest(http.MethodGet, InternalDeviceSnapshotPrefix+deviceA+"/observation-snapshot", nil)
	forged.Header.Set("Authorization", "Bearer grant")
	forged.Header.Set("X-Organization-ID", orgA)
	forged.TLS = verifiedTLSState(gatewaySPIFFE)
	forgedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(forgedRecorder, forged)
	if forgedRecorder.Code != http.StatusBadRequest || !strings.Contains(forgedRecorder.Body.String(), "TELEMETRY_FORGED_IDENTITY_HEADER") {
		t.Fatalf("forged status=%d body=%s", forgedRecorder.Code, forgedRecorder.Body.String())
	}

	wrong := httptest.NewRequest(http.MethodGet, InternalDeviceSnapshotPrefix+deviceA+"/observation-snapshot", nil)
	wrong.Header.Set("Authorization", "Bearer grant")
	wrong.TLS = verifiedTLSState("spiffe://hvac.local/untrusted")
	wrongRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRecorder, wrong)
	if wrongRecorder.Code != http.StatusUnauthorized || !strings.Contains(wrongRecorder.Body.String(), "TELEMETRY_WORKLOAD_IDENTITY_INVALID") {
		t.Fatalf("wrong status=%d body=%s", wrongRecorder.Code, wrongRecorder.Body.String())
	}
}

func TestInternalBatchPreservesOrderAndTypedNotFoundFromLatestCache(t *testing.T) {
	deviceB := "018f2e00-3000-7000-8000-000000000003"
	store := &fakeSnapshotStore{errByDevice: map[string]error{}}
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	latestCache := &fakeLatestCache{snapshots: map[string]telemetryapi.DeviceObservationSnapshot{
		deviceA: snapshotFixture(deviceA, now, []string{"zone.temperature"}),
	}}
	handler := NewHandler(ServerConfig{Store: store, LatestCache: latestCache, Authorizer: &fakeAuthorizer{}, AllowedGatewaySPIFFE: gatewaySPIFFE, Now: func() time.Time { return now }})
	body := `{"requests":[{"requestId":"first","deviceId":"` + deviceA + `","keys":["zone.temperature"]},{"requestId":"second","deviceId":"` + deviceB + `","keys":[]}]}`
	request := httptest.NewRequest(http.MethodPost, InternalBatchSnapshotPath, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer batch-grant")
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(gatewaySPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response telemetryapi.BatchGetObservationSnapshotsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.Items[0].Success == nil || response.Items[0].Success.RequestId != "first" {
		t.Fatalf("first=%#v", response.Items)
	}
	if response.Items[1].Failure == nil || response.Items[1].Failure.RequestId != "second" || response.Items[1].Failure.Problem.Code != "RESOURCE_NOT_FOUND" || response.Items[1].Failure.Problem.Status != http.StatusNotFound {
		t.Fatalf("second=%#v", response.Items[1])
	}
	if len(store.calls) != 0 || strings.Join(latestCache.calls, ",") != deviceA+","+deviceB {
		t.Fatalf("batch used unexpected authority: store=%#v cache=%#v", store.calls, latestCache.calls)
	}
}

func TestInternalSnapshotFailsClosedForGrantDependencies(t *testing.T) {
	tests := []struct {
		name    string
		authErr error
		status  int
		code    string
	}{
		{name: "grant rejected is indistinguishable from missing", authErr: ErrGrantRejected, status: http.StatusNotFound, code: "RESOURCE_NOT_FOUND"},
		{name: "IAM unavailable", authErr: ErrAuthorizationUnavailable, status: http.StatusServiceUnavailable, code: "TELEMETRY_AUTHORIZATION_UNAVAILABLE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			latestCache := &fakeLatestCache{snapshots: map[string]telemetryapi.DeviceObservationSnapshot{deviceA: snapshotFixture(deviceA, time.Now().UTC(), nil)}}
			handler := NewHandler(ServerConfig{Store: &fakeSnapshotStore{}, LatestCache: latestCache, Authorizer: &fakeAuthorizer{err: test.authErr}, AllowedGatewaySPIFFE: gatewaySPIFFE})
			recorder := getSnapshot(handler, deviceA, "zone.temperature")
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// Redis holds a rebuildable copy; PostgreSQL holds the current snapshot (ADR 0013 section 5).
func TestInternalSnapshotReadsPostgreSQLWhenRedisCannotAnswer(t *testing.T) {
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	stored := snapshotFixture(deviceA, now, []string{"zone.temperature"})
	tests := []struct {
		name      string
		cacheErr  error
		latest    map[string]telemetryapi.DeviceObservationSnapshot
		latestErr error
		status    int
		code      string
	}{
		{name: "Redis down returns the PostgreSQL snapshot", cacheErr: ErrLatestCacheUnavailable, latest: map[string]telemetryapi.DeviceObservationSnapshot{deviceA: stored}, status: http.StatusOK},
		{name: "Redis miss returns the PostgreSQL snapshot", cacheErr: ErrLatestCacheMiss, latest: map[string]telemetryapi.DeviceObservationSnapshot{deviceA: stored}, status: http.StatusOK},
		{name: "missing in both is not found", cacheErr: ErrLatestCacheUnavailable, status: http.StatusNotFound, code: "RESOURCE_NOT_FOUND"},
		{name: "PostgreSQL down too is unavailable", cacheErr: ErrLatestCacheUnavailable, latestErr: errors.New("postgres down"), status: http.StatusServiceUnavailable, code: "TELEMETRY_RUNTIME_UNAVAILABLE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeSnapshotStore{latest: test.latest, latestErr: test.latestErr}
			handler := NewHandler(ServerConfig{Store: store, LatestCache: &fakeLatestCache{err: test.cacheErr}, Authorizer: &fakeAuthorizer{}, AllowedGatewaySPIFFE: gatewaySPIFFE})
			recorder := getSnapshot(handler, deviceA, "zone.temperature")
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.status == http.StatusOK {
				var snapshot telemetryapi.DeviceObservationSnapshot
				if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
					t.Fatal(err)
				}
				if snapshot.BusinessRevision != stored.BusinessRevision || len(snapshot.Values) != 1 || snapshot.Values[0].Missing == nil || snapshot.Values[0].Missing.Key != "zone.temperature" {
					t.Fatalf("snapshot=%#v", snapshot)
				}
			}
		})
	}
}

func TestInternalBatchReadsPostgreSQLWhenRedisIsDown(t *testing.T) {
	deviceB := "018f2e00-3000-7000-8000-000000000003"
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	store := &fakeSnapshotStore{latest: map[string]telemetryapi.DeviceObservationSnapshot{deviceA: snapshotFixture(deviceA, now, []string{"zone.temperature"})}}
	handler := NewHandler(ServerConfig{Store: store, LatestCache: &fakeLatestCache{err: ErrLatestCacheUnavailable}, Authorizer: &fakeAuthorizer{}, AllowedGatewaySPIFFE: gatewaySPIFFE})
	body := `{"requests":[{"requestId":"first","deviceId":"` + deviceA + `","keys":["zone.temperature"]},{"requestId":"second","deviceId":"` + deviceB + `","keys":[]}]}`
	recorder := postBatch(handler, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response telemetryapi.BatchGetObservationSnapshotsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.Items[0].Success == nil || response.Items[1].Failure == nil || response.Items[1].Failure.Problem.Status != http.StatusNotFound {
		t.Fatalf("items=%#v", response.Items)
	}

	store.latestErr = errors.New("postgres down")
	if recorder := postBatch(handler, body); recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "TELEMETRY_RUNTIME_UNAVAILABLE") {
		t.Fatalf("PostgreSQL down too: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func postBatch(handler http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, InternalBatchSnapshotPath, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer batch-grant")
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(gatewaySPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func getSnapshot(handler http.Handler, deviceID, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, InternalDeviceSnapshotPrefix+deviceID+"/observation-snapshot?key="+key, nil)
	request.Header.Set("Authorization", "Bearer grant")
	request.TLS = verifiedTLSState(gatewaySPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestInternalRevocationRequiresIAMWorkloadAndUnsubscribes(t *testing.T) {
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	repository := NewMemoryRealtimeRepository()
	transport := &RecordingRealtimeTransport{}
	service := newRealtimeTestService(t, repository, transport, &now)
	access := realtimeTestAccess()
	bootstrap, err := service.Bootstrap(context.Background(), access, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "revoke-test", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	repository.SetCurrentRevision(realtimeTestDevice1, 3)
	checkpoint, err := service.Checkpoint(context.Background(), access, telemetryapi.RecoveryCursorCheckpointRequest{Checkpoints: []telemetryapi.RecoveryCursorCheckpoint{
		{SubscriptionId: bootstrap.Subscriptions[0].SubscriptionId, BusinessRevision: 3, TransportPosition: telemetryapi.TransportPosition{Epoch: "epoch-a", Offset: 7}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(ServerConfig{Realtime: service, AllowedIAMSPIFFE: "spiffe://hvac.local/iam-service", Now: func() time.Time { return now }})
	body := `{"principalId":"` + realtimeTestPrincipal + `","deviceId":"` + realtimeTestDevice1 + `","reason":"IAM_SCOPE_REVOKED","occurredAt":"` + now.Format(time.RFC3339Nano) + `"}`
	request := httptest.NewRequest(http.MethodPost, InternalSubscriptionRevokePath, strings.NewReader(body))
	request.TLS = verifiedTLSState("spiffe://hvac.local/iam-service")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(transport.Unsubscribes) != 1 {
		t.Fatalf("revoke status=%d body=%s unsubscribes=%+v", recorder.Code, recorder.Body.String(), transport.Unsubscribes)
	}
	if _, err := service.AuthorizeSubscribe(context.Background(), access.PrincipalID, string(bootstrap.Subscriptions[0].Channel)); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("revoked subscription remained active: %v", err)
	}
	cursor := checkpoint.Items[0].RecoveryCursor
	input := telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{{ClientSubscriptionId: "revoke-test", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}, RecoveryCursor: &cursor}}}
	if _, err := service.Bootstrap(context.Background(), access, input); !errors.Is(err, ErrRecoveryCursorRejected) {
		t.Fatalf("revoked recovery cursor remained usable: %v", err)
	}

	wrong := httptest.NewRequest(http.MethodPost, InternalSubscriptionRevokePath, strings.NewReader(body))
	wrong.TLS = verifiedTLSState("spiffe://hvac.local/untrusted")
	wrongRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRecorder, wrong)
	if wrongRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("untrusted revoker status=%d body=%s", wrongRecorder.Code, wrongRecorder.Body.String())
	}
}

func TestInternalBootstrapMapsCrossScopeCursorToStableInvalidProblem(t *testing.T) {
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	repository := NewMemoryRealtimeRepository()
	service := newRealtimeTestService(t, repository, &RecordingRealtimeTransport{}, &now)
	ownerAccess := realtimeTestAccess()
	bootstrap, err := service.Bootstrap(context.Background(), ownerAccess, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "cursor-owner", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	repository.SetCurrentRevision(realtimeTestDevice1, 3)
	checkpoint, err := service.Checkpoint(context.Background(), ownerAccess, telemetryapi.RecoveryCursorCheckpointRequest{Checkpoints: []telemetryapi.RecoveryCursorCheckpoint{
		{SubscriptionId: bootstrap.Subscriptions[0].SubscriptionId, BusinessRevision: 3, TransportPosition: telemetryapi.TransportPosition{Epoch: "epoch-a", Offset: 7}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	crossScope := ownerAccess
	crossScope.PrincipalID = "018f2e00-2000-7000-8000-000000000002"
	crossScope.Subject = "subject-b"
	handler := NewHandler(ServerConfig{
		Authorizer: &fakeAuthorizer{access: crossScope}, Realtime: service,
		AllowedGatewaySPIFFE: gatewaySPIFFE, Now: func() time.Time { return now },
	})
	cursor := checkpoint.Items[0].RecoveryCursor
	body, err := json.Marshal(telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "cursor-replay", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}, RecoveryCursor: &cursor},
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, InternalSubscriptionBootstrapPath, strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer recovery-grant")
	request.TLS = verifiedTLSState(gatewaySPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "RECOVERY_CURSOR_INVALID") {
		t.Fatalf("cross-scope cursor status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProblemDetailsAlwaysUseContractTraceID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/missing", nil)
	request.Header.Set("Traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	fromTraceparent := problemDetails(request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "missing", false)
	if fromTraceparent.TraceId != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("traceparent traceId=%s", fromTraceparent.TraceId)
	}

	request.Header.Set("Traceparent", "00-00000000000000000000000000000000-0123456789abcdef-01")
	request.Header.Set("X-Request-ID", "gateway-request-42")
	fallback := problemDetails(request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "missing", false)
	if !traceIDPattern.MatchString(fallback.TraceId) || fallback.TraceId == strings.Repeat("0", 32) {
		t.Fatalf("fallback traceId=%s", fallback.TraceId)
	}
	if fallback.TraceId != problemDetails(request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "missing", false).TraceId {
		t.Fatal("fallback traceId is not deterministic")
	}
}

func TestInternalSnapshotAppliesReadTimeFreshnessWithoutMutatingCache(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	now := t0.Add(6 * time.Minute)
	freshSeconds := 300
	online := telemetryapi.DevicePresenceStateOnline
	displayOnline := telemetryapi.DeviceDisplayStateOnline
	cached := telemetryapi.DeviceObservationSnapshot{
		SchemaVersion: 1, TenantId: telemetryapi.UUIDv7(tenantA), SiteId: telemetryapi.UUIDv7(siteA), DeviceId: telemetryapi.UUIDv7(deviceA),
		BusinessRevision: 8, EvaluatedAt: instant(t0), EvaluationAvailability: telemetryapi.EvaluationAvailabilityAvailable,
		AvailabilityReasons: []telemetryapi.AvailabilityReasonCode{},
		Presence:            telemetryapi.PresenceSnapshot{Applicability: telemetryapi.PresenceApplicabilityApplicable, CurrentState: &online},
		TelemetryReadiness:  telemetryapi.TelemetryReadinessCurrent, DisplayState: &displayOnline,
		Values: []telemetryapi.TelemetryKeyState{{Present: &telemetryapi.TelemetryPresentState{
			Key: "zone.temperature", State: "PRESENT", Value: json.RawMessage(`23.4`), ValueType: "NUMBER",
			SampledAt: instant(t0), ReceivedAt: instant(t0), FreshWithinSeconds: &freshSeconds,
			Freshness: "FRESH", Quality: telemetryapi.TelemetryQualityGood, QualityReasons: []telemetryapi.QualityReasonCode{}, PolicyRevision: 1,
			Health: &telemetryapi.TelemetryPointHealth{Status: HealthHealthy, Score: 100, Reasons: []telemetryapi.QualityReasonCode{}},
		}}},
	}
	latestCache := &fakeLatestCache{snapshots: map[string]telemetryapi.DeviceObservationSnapshot{deviceA: cached}}
	handler := NewHandler(ServerConfig{
		Store: &fakeSnapshotStore{}, LatestCache: latestCache, Authorizer: &fakeAuthorizer{},
		AllowedGatewaySPIFFE: gatewaySPIFFE, Now: func() time.Time { return now },
	})
	recorder := getSnapshot(handler, deviceA, "zone.temperature")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var got telemetryapi.DeviceObservationSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.BusinessRevision != 8 || got.TelemetryReadiness != telemetryapi.TelemetryReadinessDegraded || got.DisplayState == nil || *got.DisplayState != telemetryapi.DeviceDisplayStateStale {
		t.Fatalf("projected snapshot=%#v", got)
	}
	if got.Values[0].Present.Freshness != "STALE" || got.Values[0].Present.Health.Status != HealthStale {
		t.Fatalf("projected key=%#v", got.Values[0].Present)
	}
	if latestCache.snapshots[deviceA].Values[0].Present.Freshness != "FRESH" {
		t.Fatalf("cache was mutated during read: %#v", latestCache.snapshots[deviceA].Values[0].Present)
	}
}

func TestInternalBatchUsesBatchGetAndIsolatesIncompleteSnapshot(t *testing.T) {
	deviceB := "018f2e00-3000-7000-8000-000000000003"
	deviceMissing := "018f2e00-3000-7000-8000-000000000004"
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	store := &fakeSnapshotStore{
		latest: map[string]telemetryapi.DeviceObservationSnapshot{
			deviceB: snapshotFixture(deviceB, now, []string{"zone.temperature"}),
		},
	}
	latestCache := &fakeLatestCache{
		snapshots: map[string]telemetryapi.DeviceObservationSnapshot{
			deviceA: snapshotFixture(deviceA, now, []string{"zone.temperature"}),
		},
	}
	handler := NewHandler(ServerConfig{
		Store: store, LatestCache: latestCache, Authorizer: &fakeAuthorizer{},
		AllowedGatewaySPIFFE: gatewaySPIFFE, Now: func() time.Time { return now },
	})

	body := `{"requests":[{"requestId":"healthy-cache","deviceId":"` + deviceA + `","keys":["zone.temperature"]},{"requestId":"incomplete-store","deviceId":"` + deviceB + `","keys":["zone.temperature","new_unmaterialized_key"]},{"requestId":"not-found","deviceId":"` + deviceMissing + `","keys":["zone.temperature"]}]}`
	recorder := postBatch(handler, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("batch with one incomplete device returned status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if latestCache.batchCalls != 1 {
		t.Fatalf("expected 1 BatchGetForDevices call, got %d", latestCache.batchCalls)
	}
	if strings.Join(store.latestCalls, ",") != deviceB+","+deviceMissing {
		t.Fatalf("expected store fallback only for misses, got %v", store.latestCalls)
	}
	var response telemetryapi.BatchGetObservationSnapshotsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 3 {
		t.Fatalf("items=%d", len(response.Items))
	}
	if response.Items[0].Success == nil || response.Items[0].Success.RequestId != "healthy-cache" {
		t.Fatalf("item 0 expected OK, got %#v", response.Items[0])
	}
	if response.Items[1].Failure == nil || response.Items[1].Failure.Status != "ERROR" ||
		response.Items[1].Failure.Problem.Status != http.StatusServiceUnavailable ||
		response.Items[1].Failure.Problem.Code != "TELEMETRY_RUNTIME_UNAVAILABLE" ||
		!response.Items[1].Failure.Problem.Retryable {
		t.Fatalf("item 1 expected per-item 503 TELEMETRY_RUNTIME_UNAVAILABLE, got %#v", response.Items[1])
	}
	if response.Items[2].Failure == nil || response.Items[2].Failure.Problem.Status != http.StatusNotFound ||
		response.Items[2].Failure.Problem.Code != "RESOURCE_NOT_FOUND" || response.Items[2].Failure.Problem.Retryable {
		t.Fatalf("item 2 expected per-item 404 RESOURCE_NOT_FOUND, got %#v", response.Items[2])
	}

	singleOnly := `{"requests":[{"requestId":"only-incomplete","deviceId":"` + deviceB + `","keys":["new_unmaterialized_key"]}]}`
	singleBatchRecorder := postBatch(handler, singleOnly)
	if singleBatchRecorder.Code != http.StatusOK {
		t.Fatalf("single-item incomplete batch status=%d body=%s", singleBatchRecorder.Code, singleBatchRecorder.Body.String())
	}
	singleRecorder := getSnapshot(handler, deviceB, "new_unmaterialized_key")
	if singleRecorder.Code != http.StatusServiceUnavailable || !strings.Contains(singleRecorder.Body.String(), "TELEMETRY_RUNTIME_UNAVAILABLE") {
		t.Fatalf("handleSingle incomplete snapshot status=%d body=%s", singleRecorder.Code, singleRecorder.Body.String())
	}
}

func TestInternalRevocationBySessionSupportsGatewayAndRejectsInvalidScope(t *testing.T) {
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	repository := NewMemoryRealtimeRepository()
	transport := &RecordingRealtimeTransport{}
	service := newRealtimeTestService(t, repository, transport, &now)

	accessA := realtimeTestAccess()
	accessA.TenantID = tenantA
	accessA.SessionID = "session-a"
	bootstrapA, err := service.Bootstrap(context.Background(), accessA, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "sub-a", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	repository.SetCurrentRevision(realtimeTestDevice1, 3)
	checkpointA, err := service.Checkpoint(context.Background(), accessA, telemetryapi.RecoveryCursorCheckpointRequest{Checkpoints: []telemetryapi.RecoveryCursorCheckpoint{
		{SubscriptionId: bootstrapA.Subscriptions[0].SubscriptionId, BusinessRevision: 3, TransportPosition: telemetryapi.TransportPosition{Epoch: "epoch-a", Offset: 7}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	accessB := accessA
	accessB.SessionID = "session-b"
	bootstrapB, err := service.Bootstrap(context.Background(), accessB, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "sub-b", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(ServerConfig{
		Realtime:             service,
		AllowedIAMSPIFFE:     "spiffe://hvac.local/iam-service",
		AllowedGatewaySPIFFE: gatewaySPIFFE,
		Now:                  func() time.Time { return now },
	})

	missingTenant := httptest.NewRequest(http.MethodPost, InternalSubscriptionRevokePath, strings.NewReader(`{"sessionId":"session-a","reason":"SESSION_LOGGED_OUT","occurredAt":"`+now.Format(time.RFC3339Nano)+`"}`))
	missingTenant.TLS = verifiedTLSState(gatewaySPIFFE)
	missingTenantRec := httptest.NewRecorder()
	handler.ServeHTTP(missingTenantRec, missingTenant)
	if missingTenantRec.Code != http.StatusBadRequest || !strings.Contains(missingTenantRec.Body.String(), "TELEMETRY_REVOCATION_INVALID") {
		t.Fatalf("missing tenantId status=%d body=%s", missingTenantRec.Code, missingTenantRec.Body.String())
	}

	byPrincipal := httptest.NewRequest(http.MethodPost, InternalSubscriptionRevokePath, strings.NewReader(`{"principalId":"`+realtimeTestPrincipal+`","reason":"SESSION_LOGGED_OUT","occurredAt":"`+now.Format(time.RFC3339Nano)+`"}`))
	byPrincipal.TLS = verifiedTLSState(gatewaySPIFFE)
	byPrincipalRec := httptest.NewRecorder()
	handler.ServeHTTP(byPrincipalRec, byPrincipal)
	if byPrincipalRec.Code != http.StatusUnauthorized {
		t.Fatalf("gateway principal revocation status=%d body=%s", byPrincipalRec.Code, byPrincipalRec.Body.String())
	}

	revokeBody := `{"tenantId":"` + tenantA + `","sessionId":"session-a","reason":"SESSION_LOGGED_OUT","occurredAt":"` + now.Format(time.RFC3339Nano) + `"}`
	req := httptest.NewRequest(http.MethodPost, InternalSubscriptionRevokePath, strings.NewReader(revokeBody))
	req.TLS = verifiedTLSState(gatewaySPIFFE)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session revoke status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(transport.Unsubscribes) != 1 || transport.Unsubscribes[0].Channel != string(bootstrapA.Subscriptions[0].Channel) {
		t.Fatalf("expected immediate unsubscribe for session-a only, got %+v", transport.Unsubscribes)
	}
	if _, err := service.AuthorizeSubscribe(context.Background(), accessA.PrincipalID, string(bootstrapA.Subscriptions[0].Channel)); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("session-a channel remained active: %v", err)
	}
	if _, err := service.AuthorizeSubscribe(context.Background(), accessB.PrincipalID, string(bootstrapB.Subscriptions[0].Channel)); err != nil {
		t.Fatalf("session-b channel was unexpectedly revoked: %v", err)
	}
	cursor := checkpointA.Items[0].RecoveryCursor
	if _, err := service.Bootstrap(context.Background(), accessA, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "sub-a", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}, RecoveryCursor: &cursor},
	}}); !errors.Is(err, ErrRecoveryCursorRejected) {
		t.Fatalf("revoked session recovery cursor remained usable: %v", err)
	}
}

func verifiedTLSState(spiffe string) *tls.ConnectionState {
	uri, _ := url.Parse(spiffe)
	certificate := &x509.Certificate{URIs: []*url.URL{uri}}
	return &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}}}
}
