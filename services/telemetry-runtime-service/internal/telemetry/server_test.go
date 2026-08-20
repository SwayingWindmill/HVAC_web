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
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

const gatewaySPIFFE = "spiffe://hvac.local/platform-gateway"

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
	snapshots map[string]telemetryapi.DeviceObservationSnapshot
	err       error
	calls     []string
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

func (fake *fakeLatestCache) Close() error { return nil }

func TestInternalSingleSnapshotRequiresGatewayIdentityAndPreservesSelection(t *testing.T) {
	authorizer := &fakeAuthorizer{}
	store := &fakeSnapshotStore{errByDevice: map[string]error{}}
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	latestCache := &fakeLatestCache{snapshots: map[string]telemetryapi.DeviceObservationSnapshot{
		deviceA: snapshotFixture(deviceA, now, []string{"zone.humidity", "zone.temperature"}),
	}}
	handler := NewHandler(ServerConfig{Store: store, LatestCache: latestCache, Authorizer: authorizer, AllowedGatewaySPIFFE: gatewaySPIFFE, Now: func() time.Time { return now }})

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
	if len(store.calls) != 0 || len(latestCache.calls) != 2 {
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

func TestInternalSnapshotFailsClosedForGrantAndLatestCacheDependencies(t *testing.T) {
	tests := []struct {
		name     string
		authErr  error
		cacheErr error
		status   int
		code     string
	}{
		{name: "grant rejected is indistinguishable from missing", authErr: ErrGrantRejected, status: http.StatusNotFound, code: "RESOURCE_NOT_FOUND"},
		{name: "IAM unavailable", authErr: ErrAuthorizationUnavailable, status: http.StatusServiceUnavailable, code: "TELEMETRY_AUTHORIZATION_UNAVAILABLE"},
		{name: "Redis Latest unavailable", cacheErr: ErrLatestCacheUnavailable, status: http.StatusServiceUnavailable, code: "TELEMETRY_RUNTIME_UNAVAILABLE"},
		{name: "Redis Latest miss returns typed not-found without PostgreSQL fallback", cacheErr: ErrLatestCacheMiss, status: http.StatusNotFound, code: "RESOURCE_NOT_FOUND"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeSnapshotStore{errByDevice: map[string]error{}}
			latestCache := &fakeLatestCache{
				snapshots: map[string]telemetryapi.DeviceObservationSnapshot{deviceA: snapshotFixture(deviceA, time.Now().UTC(), nil)},
				err:       test.cacheErr,
			}
			handler := NewHandler(ServerConfig{Store: store, LatestCache: latestCache, Authorizer: &fakeAuthorizer{err: test.authErr}, AllowedGatewaySPIFFE: gatewaySPIFFE})
			request := httptest.NewRequest(http.MethodGet, InternalDeviceSnapshotPrefix+deviceA+"/observation-snapshot", nil)
			request.Header.Set("Authorization", "Bearer grant")
			request.TLS = verifiedTLSState(gatewaySPIFFE)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if len(store.calls) != 0 {
				t.Fatalf("current read fell back to PostgreSQL: %#v", store.calls)
			}
		})
	}
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

func verifiedTLSState(spiffe string) *tls.ConnectionState {
	uri, _ := url.Parse(spiffe)
	certificate := &x509.Certificate{URIs: []*url.URL{uri}}
	return &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}}}
}
