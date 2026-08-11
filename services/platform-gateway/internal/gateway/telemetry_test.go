package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
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

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/s2telemetryapi"
)

const (
	telemetryTestOrganization = "018f2e00-1000-7000-8000-000000000001"
	telemetryTestSite         = "018f2e00-2000-7000-8000-000000000001"
	telemetryTestDeviceOne    = "018f2e00-3000-7000-8000-000000000001"
	telemetryTestDeviceTwo    = "018f2e00-3000-7000-8000-000000000002"
	telemetryTestPrincipal    = "018f2e00-6000-7000-8000-000000000001"
	telemetryTestSPIFFE       = "spiffe://hvac.local/platform-gateway"
	telemetryTestPolicy       = "telemetry-policy-7"
	telemetryTestCSRF         = "csrf-fixture"
)

func TestTelemetryGatewaySingleAndBatchPreserveOrder(t *testing.T) {
	fixture := newTelemetryGatewayFixture(t, "")

	single := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+telemetryTestDeviceOne+"/observation-snapshot?keys=humidity,temperature", nil)
	fixture.authenticate(single)
	singleRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(singleRecorder, single)
	if singleRecorder.Code != http.StatusOK {
		t.Fatalf("single status=%d body=%s", singleRecorder.Code, singleRecorder.Body.String())
	}
	var snapshot s2telemetryapi.DeviceObservationSnapshot
	if err := json.NewDecoder(singleRecorder.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Values) != 2 || snapshot.Values[0].Missing == nil || snapshot.Values[1].Missing == nil || snapshot.Values[0].Missing.Key != "humidity" || snapshot.Values[1].Missing.Key != "temperature" {
		t.Fatalf("single key order drifted: %#v", snapshot.Values)
	}

	batchInput := s2telemetryapi.BatchGetObservationSnapshotsRequest{Requests: []s2telemetryapi.ObservationSnapshotTarget{
		{RequestId: "second", DeviceId: telemetryTestDeviceTwo, Keys: []s2telemetryapi.TelemetryKey{"supplyTemp"}},
		{RequestId: "first", DeviceId: telemetryTestDeviceOne, Keys: []s2telemetryapi.TelemetryKey{}},
	}}
	body, _ := json.Marshal(batchInput)
	batch := httptest.NewRequest(http.MethodPost, s2telemetryapi.BatchGetDeviceObservationSnapshotsPath, bytes.NewReader(body))
	fixture.authenticate(batch)
	batch.Header.Set("Origin", "https://web.example.test")
	batch.Header.Set("X-CSRF-Token", telemetryTestCSRF)
	batchRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(batchRecorder, batch)
	if batchRecorder.Code != http.StatusOK {
		t.Fatalf("batch status=%d body=%s", batchRecorder.Code, batchRecorder.Body.String())
	}
	var output s2telemetryapi.BatchGetObservationSnapshotsResponse
	if err := json.NewDecoder(batchRecorder.Body).Decode(&output); err != nil {
		t.Fatal(err)
	}
	if len(output.Items) != 2 || output.Items[0].Success == nil || output.Items[1].Success == nil || output.Items[0].Success.RequestId != "second" || output.Items[1].Success.RequestId != "first" {
		t.Fatalf("batch order drifted: %#v", output.Items)
	}
	if fixture.iamCalls.Load() != 2 || fixture.runtimeCalls.Load() != 2 {
		t.Fatalf("upstream calls IAM=%d runtime=%d", fixture.iamCalls.Load(), fixture.runtimeCalls.Load())
	}
}

func TestTelemetryGatewayDeviceHistoryBindsExactScopeAndPreservesMetadata(t *testing.T) {
	fixture := newTelemetryGatewayFixture(t, "")
	input := s2telemetryapi.DeviceHistoryRequest{
		DeviceId:        telemetryTestDeviceOne,
		Keys:            []s2telemetryapi.TelemetryKey{"temperature"},
		From:            s2telemetryapi.HistoryInstant("2026-07-24T06:00:00Z"),
		To:              s2telemetryapi.HistoryInstant("2026-07-24T12:00:00Z"),
		MaxPointsPerKey: 100,
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("organizationId")) || bytes.Contains(body, []byte("siteId")) {
		t.Fatalf("public Device History request exposed authority fields: %s", body)
	}
	request := httptest.NewRequest(http.MethodPost, s2telemetryapi.QueryDeviceHistoryPath, bytes.NewReader(body))
	fixture.authenticate(request)
	request.Header.Set("Origin", "https://web.example.test")
	request.Header.Set("X-CSRF-Token", telemetryTestCSRF)
	request = request.WithContext(context.WithValue(request.Context(), routeDecisionContextKey, ownershipregistry.Decision{
		RegistryRevision: 12,
		SelectedOwner:    ownershipregistry.OwnerAnalyticsQuery,
	}))
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("history status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response s2telemetryapi.DeviceHistoryResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.OwningOrganizationId != telemetryTestOrganization || response.SiteId != telemetryTestSite || response.DeviceId != telemetryTestDeviceOne || response.Metadata.DatasetRevision != "telemetry-history:v1:7" || response.Metadata.ReturnedPoints != 1 || len(response.Series) != 1 || len(response.Series[0].Points) != 1 {
		t.Fatalf("history response drifted: %+v", response)
	}
	fixture.mu.Lock()
	actions := append([]telemetryauth.Action(nil), fixture.iamActions...)
	fixture.mu.Unlock()
	if !slices.Equal(actions, []telemetryauth.Action{telemetryauth.ActionHistoryRead}) || fixture.iamCalls.Load() != 1 || fixture.queryCalls.Load() != 1 || fixture.runtimeCalls.Load() != 0 {
		t.Fatalf("history upstream boundary drifted: actions=%v IAM=%d query=%d runtime=%d", actions, fixture.iamCalls.Load(), fixture.queryCalls.Load(), fixture.runtimeCalls.Load())
	}
}

func TestTelemetryGatewayDeviceHistoryRejectsRuntimeRouteOwner(t *testing.T) {
	fixture := newTelemetryGatewayFixture(t, "")
	input := s2telemetryapi.DeviceHistoryRequest{
		DeviceId: telemetryTestDeviceOne, Keys: []s2telemetryapi.TelemetryKey{"temperature"},
		From: s2telemetryapi.HistoryInstant("2026-07-24T06:00:00Z"), To: s2telemetryapi.HistoryInstant("2026-07-24T12:00:00Z"), MaxPointsPerKey: 100,
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, s2telemetryapi.QueryDeviceHistoryPath, bytes.NewReader(body))
	fixture.authenticate(request)
	request.Header.Set("Origin", "https://web.example.test")
	request.Header.Set("X-CSRF-Token", telemetryTestCSRF)
	request = request.WithContext(context.WithValue(request.Context(), routeDecisionContextKey, ownershipregistry.Decision{
		RegistryRevision: 12,
		SelectedOwner:    ownershipregistry.OwnerTelemetryRuntime,
	}))
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("history wrong-owner status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 0 || fixture.queryCalls.Load() != 0 || fixture.runtimeCalls.Load() != 0 {
		t.Fatalf("wrong route owner reached upstreams: IAM=%d query=%d runtime=%d", fixture.iamCalls.Load(), fixture.queryCalls.Load(), fixture.runtimeCalls.Load())
	}
}

func TestTelemetryGatewayBootstrapCheckpointAndRecoveryUseExactScope(t *testing.T) {
	fixture := newTelemetryGatewayFixture(t, "")
	bootstrapInput := s2telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []s2telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "zone-temperature", DeviceId: telemetryTestDeviceOne, Keys: []s2telemetryapi.TelemetryKey{"temperature"}},
	}}
	bootstrapBody, _ := json.Marshal(bootstrapInput)
	bootstrapRequest := httptest.NewRequest(http.MethodPost, s2telemetryapi.BootstrapTelemetrySubscriptionsPath, bytes.NewReader(bootstrapBody))
	fixture.authenticate(bootstrapRequest)
	bootstrapRequest.Header.Set("Origin", "https://web.example.test")
	bootstrapRequest.Header.Set("X-CSRF-Token", telemetryTestCSRF)
	bootstrapRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(bootstrapRecorder, bootstrapRequest)
	if bootstrapRecorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status=%d body=%s", bootstrapRecorder.Code, bootstrapRecorder.Body.String())
	}
	var bootstrap s2telemetryapi.SubscriptionBootstrapResponse
	if err := json.NewDecoder(bootstrapRecorder.Body).Decode(&bootstrap); err != nil || len(bootstrap.Subscriptions) != 1 {
		t.Fatalf("bootstrap decode=%v output=%+v", err, bootstrap)
	}

	checkpointInput := s2telemetryapi.RecoveryCursorCheckpointRequest{Checkpoints: []s2telemetryapi.RecoveryCursorCheckpoint{
		{SubscriptionId: bootstrap.Subscriptions[0].SubscriptionId, BusinessRevision: 9, TransportPosition: s2telemetryapi.TransportPosition{Epoch: "epoch-a", Offset: 42}},
	}}
	checkpointBody, _ := json.Marshal(checkpointInput)
	checkpointRequest := httptest.NewRequest(http.MethodPost, s2telemetryapi.CheckpointTelemetryRecoveryCursorsPath, bytes.NewReader(checkpointBody))
	fixture.authenticate(checkpointRequest)
	checkpointRequest.Header.Set("Origin", "https://web.example.test")
	checkpointRequest.Header.Set("X-CSRF-Token", telemetryTestCSRF)
	checkpointRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(checkpointRecorder, checkpointRequest)
	if checkpointRecorder.Code != http.StatusOK {
		t.Fatalf("checkpoint status=%d body=%s", checkpointRecorder.Code, checkpointRecorder.Body.String())
	}
	var checkpoint s2telemetryapi.RecoveryCursorCheckpointResponse
	if err := json.NewDecoder(checkpointRecorder.Body).Decode(&checkpoint); err != nil || len(checkpoint.Items) != 1 {
		t.Fatalf("checkpoint decode=%v output=%+v", err, checkpoint)
	}

	cursor := checkpoint.Items[0].RecoveryCursor
	bootstrapInput.Subscriptions[0].RecoveryCursor = &cursor
	recoveryBody, _ := json.Marshal(bootstrapInput)
	recoveryRequest := httptest.NewRequest(http.MethodPost, s2telemetryapi.BootstrapTelemetrySubscriptionsPath, bytes.NewReader(recoveryBody))
	fixture.authenticate(recoveryRequest)
	recoveryRequest.Header.Set("Origin", "https://web.example.test")
	recoveryRequest.Header.Set("X-CSRF-Token", telemetryTestCSRF)
	recoveryRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recoveryRecorder, recoveryRequest)
	if recoveryRecorder.Code != http.StatusOK {
		t.Fatalf("recovery bootstrap status=%d body=%s", recoveryRecorder.Code, recoveryRecorder.Body.String())
	}

	fixture.mu.Lock()
	actions := append([]telemetryauth.Action(nil), fixture.iamActions...)
	paths := append([]string(nil), fixture.runtimePaths...)
	fixture.mu.Unlock()
	if !slices.Equal(actions, []telemetryauth.Action{telemetryauth.ActionSubscribe, telemetryauth.ActionRecoveryCheckpoint, telemetryauth.ActionRecoveryUse}) {
		t.Fatalf("IAM action sequence drifted: %v", actions)
	}
	if !slices.Equal(paths, []string{internalTelemetryBootstrapPath, internalTelemetryCheckpointResolvePath, internalTelemetryCheckpointPath, internalTelemetryBootstrapPath}) {
		t.Fatalf("Runtime path sequence drifted: %v", paths)
	}
	if fixture.iamCalls.Load() != 3 || fixture.runtimeCalls.Load() != 4 {
		t.Fatalf("realtime upstream calls IAM=%d runtime=%d", fixture.iamCalls.Load(), fixture.runtimeCalls.Load())
	}
}

func TestTelemetryGatewayRejectsCSRFAndForgedIdentityBeforeUpstream(t *testing.T) {
	fixture := newTelemetryGatewayFixture(t, "")
	body := `{"requests":[{"requestId":"one","deviceId":"` + telemetryTestDeviceOne + `","keys":[]}]}`

	missingCSRF := httptest.NewRequest(http.MethodPost, s2telemetryapi.BatchGetDeviceObservationSnapshotsPath, strings.NewReader(body))
	fixture.authenticate(missingCSRF)
	missingCSRF.Header.Set("Origin", "https://web.example.test")
	missingRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(missingRecorder, missingCSRF)
	assertTelemetryProblem(t, missingRecorder, http.StatusForbidden, "CSRF_REQUIRED")

	forged := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+telemetryTestDeviceOne+"/observation-snapshot", nil)
	fixture.authenticate(forged)
	forged.Header.Set("X-Admin", "true")
	forgedRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(forgedRecorder, forged)
	assertTelemetryProblem(t, forgedRecorder, http.StatusBadRequest, "FORGED_IDENTITY_HEADER")

	if fixture.iamCalls.Load() != 0 || fixture.runtimeCalls.Load() != 0 {
		t.Fatalf("rejected requests reached upstream IAM=%d runtime=%d", fixture.iamCalls.Load(), fixture.runtimeCalls.Load())
	}
}

func TestTelemetryGatewayWorkloadMTLSUsesExactIAMScopeWithoutCSRF(t *testing.T) {
	fixture := newTelemetryGatewayFixture(t, "")
	body := `{"requests":[{"requestId":"workload-one","deviceId":"` + telemetryTestDeviceOne + `","keys":["temperature"]}]}`

	request := httptest.NewRequest(http.MethodPost, s2telemetryapi.BatchGetDeviceObservationSnapshotsPath, strings.NewReader(body))
	request.TLS = verifiedWorkloadTLSState(t, "spiffe://hvac.local/automation-service")
	request.Header.Set("X-Organization-ID", telemetryTestOrganization)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("workload batch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 1 || fixture.runtimeCalls.Load() != 1 {
		t.Fatalf("workload upstream calls IAM=%d runtime=%d", fixture.iamCalls.Load(), fixture.runtimeCalls.Load())
	}

	unverified := httptest.NewRequest(http.MethodPost, s2telemetryapi.BatchGetDeviceObservationSnapshotsPath, strings.NewReader(body))
	unverified.TLS = &tls.ConnectionState{PeerCertificates: verifiedWorkloadTLSState(t, "spiffe://hvac.local/automation-service").VerifiedChains[0]}
	unverified.Header.Set("X-Organization-ID", telemetryTestOrganization)
	unverifiedRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(unverifiedRecorder, unverified)
	assertTelemetryProblem(t, unverifiedRecorder, http.StatusBadRequest, "FORGED_IDENTITY_HEADER")

	forgedRole := httptest.NewRequest(http.MethodPost, s2telemetryapi.BatchGetDeviceObservationSnapshotsPath, strings.NewReader(body))
	forgedRole.TLS = verifiedWorkloadTLSState(t, "spiffe://hvac.local/automation-service")
	forgedRole.Header.Set("X-Organization-ID", telemetryTestOrganization)
	forgedRole.Header.Set("X-Roles", "admin")
	forgedRoleRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(forgedRoleRecorder, forgedRole)
	assertTelemetryProblem(t, forgedRoleRecorder, http.StatusBadRequest, "FORGED_IDENTITY_HEADER")

	if fixture.iamCalls.Load() != 1 || fixture.runtimeCalls.Load() != 1 {
		t.Fatalf("rejected workload requests reached upstream IAM=%d runtime=%d", fixture.iamCalls.Load(), fixture.runtimeCalls.Load())
	}
}

func TestTelemetryGatewayIAMUnavailableIsStableAndDoesNotReachRuntime(t *testing.T) {
	fixture := newTelemetryGatewayFixture(t, "")
	fixture.iamHTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		fixture.iamCalls.Add(1)
		return nil, errors.New("iam unavailable")
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+telemetryTestDeviceOne+"/observation-snapshot", nil)
	fixture.authenticate(request)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	problem := assertTelemetryProblem(t, recorder, http.StatusServiceUnavailable, "TELEMETRY_AUTHORIZATION_UNAVAILABLE")
	if !problem.Retryable || fixture.runtimeCalls.Load() != 0 {
		t.Fatalf("IAM failure boundary drifted: retryable=%t runtime=%d", problem.Retryable, fixture.runtimeCalls.Load())
	}
}

func TestTelemetryGatewayNondiscoveryLimitsAndTimeout(t *testing.T) {
	denied := newTelemetryGatewayFixture(t, telemetryauth.ReasonResourceNotFound)
	problems := make([]s2telemetryapi.ProblemDetails, 0, 2)
	for _, deviceID := range []string{telemetryTestDeviceOne, telemetryTestDeviceTwo} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+deviceID+"/observation-snapshot", nil)
		denied.authenticate(request)
		recorder := httptest.NewRecorder()
		denied.handler.ServeHTTP(recorder, request)
		problems = append(problems, assertTelemetryProblem(t, recorder, http.StatusNotFound, "RESOURCE_NOT_FOUND"))
	}
	if problems[0].Title != problems[1].Title || problems[0].Detail != problems[1].Detail || problems[0].Retryable != problems[1].Retryable || denied.runtimeCalls.Load() != 0 {
		t.Fatalf("nondiscovery boundary drifted: %#v %#v runtime=%d", problems[0], problems[1], denied.runtimeCalls.Load())
	}

	fixture := newTelemetryGatewayFixture(t, "")
	keys := make([]string, telemetryauth.MaximumKeysPerTarget+1)
	for index := range keys {
		keys[index] = "key" + string(rune('a'+index%26)) + strings.Repeat("x", index/26)
	}
	limit := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+telemetryTestDeviceOne+"/observation-snapshot?keys="+strings.Join(keys, ","), nil)
	fixture.authenticate(limit)
	limitRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(limitRecorder, limit)
	assertTelemetryProblem(t, limitRecorder, http.StatusRequestEntityTooLarge, "TELEMETRY_BATCH_LIMIT_EXCEEDED")
	if fixture.iamCalls.Load() != 0 {
		t.Fatal("limit failure reached IAM")
	}

	fixture.runtimeHTTPClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.runtimeCalls.Add(1)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	fixture.handler.telemetry.timeout = 5 * time.Millisecond
	timeout := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+telemetryTestDeviceOne+"/observation-snapshot", nil)
	fixture.authenticate(timeout)
	timeoutRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(timeoutRecorder, timeout)
	problem := assertTelemetryProblem(t, timeoutRecorder, http.StatusGatewayTimeout, "TELEMETRY_TIMEOUT")
	if !problem.Retryable {
		t.Fatal("timeout must be retryable")
	}
}

type telemetryGatewayFixture struct {
	handler           *handler
	sessionID         string
	iamCalls          atomic.Int32
	runtimeCalls      atomic.Int32
	queryCalls        atomic.Int32
	iamHTTPClient     *http.Client
	runtimeHTTPClient *http.Client
	queryHTTPClient   *http.Client
	mu                sync.Mutex
	iamActions        []telemetryauth.Action
	runtimePaths      []string
}

func newTelemetryGatewayFixture(t *testing.T, denyReason telemetryauth.ReasonCode) *telemetryGatewayFixture {
	t.Helper()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	gatewaySigner, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := sessionstore.NewMemoryStore()
	fixture := &telemetryGatewayFixture{}
	subscriptionScopes := map[string]telemetryauth.Target{}
	fixture.runtimeHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.runtimeCalls.Add(1)
		fixture.mu.Lock()
		fixture.runtimePaths = append(fixture.runtimePaths, request.URL.Path)
		fixture.mu.Unlock()
		for _, header := range []string{"Cookie", "X-CSRF-Token", "X-Admin", "X-Principal", "X-Organization-ID"} {
			if request.Header.Get(header) != "" {
				t.Fatalf("browser authority leaked to runtime header %s", header)
			}
		}
		if request.URL.Path == internalTelemetryCheckpointResolvePath {
			if request.Header.Get("Authorization") != "" {
				t.Fatal("checkpoint resolver received an IAM bearer grant")
			}
			claims, err := identitycontext.VerifyDelegation(&gatewaySigner.PublicKey, request.Header.Get(telemetryContextGrantHeader))
			if err != nil || len(claims.Actions) != 1 || claims.Actions[0] != telemetryCheckpointResolveAction {
				t.Fatalf("checkpoint context grant invalid: claims=%+v err=%v", claims, err)
			}
			var input s2telemetryapi.RecoveryCursorCheckpointRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			targets := make([]telemetryauth.Target, 0, len(input.Checkpoints))
			for _, checkpoint := range input.Checkpoints {
				target, ok := subscriptionScopes[string(checkpoint.SubscriptionId)]
				if !ok {
					return telemetryJSONResponse(http.StatusNotFound, s2telemetryapi.ProblemDetails{Status: http.StatusNotFound, Code: "RESOURCE_NOT_FOUND"}), nil
				}
				targets = append(targets, target)
			}
			canonical, err := telemetryauth.CanonicalTargets(targets)
			if err != nil {
				t.Fatal(err)
			}
			return telemetryJSONResponse(http.StatusOK, telemetryCheckpointScopeResponse{Targets: canonical}), nil
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
			t.Fatal("runtime request omitted delegated authorization")
		}
		if request.Method == http.MethodGet {
			keys := append([]string(nil), request.URL.Query()["key"]...)
			return telemetryJSONResponse(http.StatusOK, telemetrySnapshot(telemetryTestDeviceOne, keys, now)), nil
		}
		switch request.URL.Path {
		case internalTelemetryBatchPath:
			var input s2telemetryapi.BatchGetObservationSnapshotsRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			output := s2telemetryapi.BatchGetObservationSnapshotsResponse{SchemaVersion: 1, Items: make([]s2telemetryapi.BatchObservationResult, 0, len(input.Requests))}
			for _, item := range input.Requests {
				keys := make([]string, len(item.Keys))
				for index, key := range item.Keys {
					keys[index] = string(key)
				}
				output.Items = append(output.Items, s2telemetryapi.BatchObservationResult{Success: &s2telemetryapi.BatchObservationSuccess{RequestId: item.RequestId, DeviceId: item.DeviceId, Status: "OK", Snapshot: telemetrySnapshot(string(item.DeviceId), keys, now)}})
			}
			return telemetryJSONResponse(http.StatusOK, output), nil
		case internalTelemetryBootstrapPath:
			var input s2telemetryapi.SubscriptionBootstrapRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			output := s2telemetryapi.SubscriptionBootstrapResponse{
				SchemaVersion: 1, TransportProtocol: "CENTRIFUGO_JSON_V1", Endpoint: "wss://realtime.example.test/connection/websocket",
				ConnectionToken: strings.Repeat("t", 32), ExpiresAt: s2telemetryapi.Instant(now.Add(5 * time.Minute).Format("2006-01-02T15:04:05.000Z")),
				Limits:        s2telemetryapi.SubscriptionLimits{MaxSubscriptions: 100, MaxKeysPerSubscription: 64, MaxTotalKeySelections: 2048},
				Subscriptions: make([]s2telemetryapi.SubscriptionDescriptor, 0, len(input.Subscriptions)),
			}
			for index, item := range input.Subscriptions {
				subscriptionID := base64.RawURLEncoding.EncodeToString([]byte("subscription-" + strconv.Itoa(index) + "-" + strings.Repeat("x", 16)))
				channel := "s2:" + base64.RawURLEncoding.EncodeToString([]byte("channel-"+strconv.Itoa(index)+"-"+strings.Repeat("y", 16)))
				keys := make([]string, len(item.Keys))
				for keyIndex, key := range item.Keys {
					keys[keyIndex] = string(key)
				}
				subscriptionScopes[subscriptionID] = telemetryauth.Target{DeviceID: string(item.DeviceId), Keys: keys}
				descriptor := s2telemetryapi.SubscriptionDescriptor{ClientSubscriptionId: item.ClientSubscriptionId, SubscriptionId: s2telemetryapi.OpaqueSubscriptionId(subscriptionID), DeviceId: item.DeviceId, Keys: append([]s2telemetryapi.TelemetryKey(nil), item.Keys...), Channel: s2telemetryapi.OpaqueChannel(channel), RecoveryMode: "SNAPSHOT_THEN_LIVE"}
				if item.RecoveryCursor != nil {
					descriptor.RecoveryMode = "ATTEMPT_RECOVERY"
					descriptor.TransportPosition = &s2telemetryapi.TransportPosition{Epoch: "epoch-a", Offset: 42}
					cursor := *item.RecoveryCursor
					descriptor.RecoveryCursor = &cursor
				}
				output.Subscriptions = append(output.Subscriptions, descriptor)
			}
			return telemetryJSONResponse(http.StatusOK, output), nil
		case internalTelemetryCheckpointPath:
			claims, err := identitycontext.VerifyDelegation(&gatewaySigner.PublicKey, request.Header.Get(telemetryContextGrantHeader))
			if err != nil || len(claims.Actions) != 1 || claims.Actions[0] != telemetryCheckpointResolveAction {
				t.Fatalf("final checkpoint context grant invalid: claims=%+v err=%v", claims, err)
			}
			var input s2telemetryapi.RecoveryCursorCheckpointRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			output := s2telemetryapi.RecoveryCursorCheckpointResponse{SchemaVersion: 1, Items: make([]s2telemetryapi.RecoveryCursorCheckpointResult, 0, len(input.Checkpoints))}
			for _, checkpoint := range input.Checkpoints {
				output.Items = append(output.Items, s2telemetryapi.RecoveryCursorCheckpointResult{SubscriptionId: checkpoint.SubscriptionId, BusinessRevision: checkpoint.BusinessRevision, RecoveryCursor: s2telemetryapi.OpaqueRecoveryCursor(strings.Repeat("c", 32) + "." + strings.Repeat("s", 43)), ExpiresAt: s2telemetryapi.Instant(now.Add(2 * time.Minute).Format("2006-01-02T15:04:05.000Z"))})
			}
			return telemetryJSONResponse(http.StatusOK, output), nil
		default:
			t.Fatalf("unexpected runtime path %s", request.URL.Path)
			return nil, errors.New("unexpected runtime path")
		}
	})}

	iamClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.iamCalls.Add(1)
		for _, header := range []string{"Cookie", "X-CSRF-Token", "X-Admin", "X-Principal", "X-Organization-ID"} {
			if request.Header.Get(header) != "" {
				t.Fatalf("browser authority leaked to IAM header %s", header)
			}
		}
		parent, err := identitycontext.VerifyDelegation(&gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("parent delegation invalid: %v", err)
		}
		var input telemetryauth.DecisionRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		fixture.mu.Lock()
		fixture.iamActions = append(fixture.iamActions, input.Action)
		fixture.mu.Unlock()
		canonical, err := telemetryauth.CanonicalTargets(input.Targets)
		if err != nil {
			t.Fatal(err)
		}
		decision := telemetryauth.Decision{PrincipalID: telemetryTestPrincipal, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, ActingOrganizationID: parent.ActingOrganizationID, Action: input.Action, PolicyRevision: telemetryTestPolicy, DecidedAt: now.Format(time.RFC3339Nano), Targets: []telemetryauth.AuthorizedTarget{}}
		if denyReason != "" {
			decision.ReasonCode = denyReason
			return telemetryJSONResponse(http.StatusOK, telemetryauth.DecisionResponse{Decision: decision}), nil
		}
		decision.Allowed = true
		decision.ReasonCode = telemetryauth.ReasonAllowExactScope
		decision.ScopeDigest, _ = telemetryauth.ScopeDigest(input.Action, input.ActingOrganizationID, canonical)
		keyCount := 0
		for _, target := range canonical {
			keyCount += len(target.Keys)
			decision.Targets = append(decision.Targets, telemetryauth.AuthorizedTarget{DeviceID: target.DeviceID, OwningOrganizationID: telemetryTestOrganization, SiteID: telemetryTestSite, Keys: target.Keys})
		}
		claims := telemetryauth.GrantClaims{Issuer: "spiffe://hvac.local/iam-service", Presenter: telemetryTestSPIFFE, Audience: "telemetry-runtime-service", PrincipalID: telemetryTestPrincipal, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, ActingOrganizationID: parent.ActingOrganizationID, ActorChain: []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: telemetryTestSPIFFE}}, Action: input.Action, ScopeDigest: decision.ScopeDigest, TargetCount: len(canonical), KeyCount: keyCount, PolicyRevision: telemetryTestPolicy, SessionID: parent.SessionID, ParentTokenID: parent.TokenID, RequestID: request.Header.Get("X-Request-ID"), TraceID: traceIDFromTraceparent(request.Header.Get("Traceparent")), Route: telemetryPublicRoute(input.Action), IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "grant-id"}
		return telemetryJSONResponse(http.StatusOK, telemetryauth.DecisionResponse{Decision: decision, DelegationGrant: unsignedTelemetryGrant(claims)}), nil
	})}
	fixture.iamHTTPClient = iamClient
	fixture.queryHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.queryCalls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != internalDeviceHistoryPath {
			t.Fatalf("unexpected query service request %s %s", request.Method, request.URL.Path)
		}
		for _, header := range []string{"Cookie", "X-CSRF-Token", "X-Admin", "X-Principal", "X-Organization-ID", "X-Site-ID"} {
			if request.Header.Get(header) != "" {
				t.Fatalf("browser authority leaked to query service header %s", header)
			}
		}
		var query telemetryhistorymodel.DeviceHistoryQuery
		if err := json.NewDecoder(request.Body).Decode(&query); err != nil {
			t.Fatal(err)
		}
		scope, err := query.ScopeDigest()
		if err != nil {
			t.Fatal(err)
		}
		claims, err := identitycontext.VerifyDelegation(&gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil || claims.PrincipalID != telemetryTestPrincipal || claims.PolicyRevision != telemetryTestPolicy || claims.ActingOrganizationID != telemetryTestOrganization || identitycontext.ValidateDelegation(claims, now, telemetryTestSPIFFE, "telemetry-query-service", telemetryhistorymodel.DeviceHistoryAction, scope) != nil {
			t.Fatalf("query delegation invalid: claims=%+v err=%v", claims, err)
		}
		if query.ActingOrganizationID != telemetryTestOrganization || query.OwningOrganizationID != telemetryTestOrganization || query.SiteID != telemetryTestSite || query.DeviceID != telemetryTestDeviceOne || len(query.Keys) != 1 || query.Keys[0] != "temperature" || query.MaxPointsPerKey != 100 {
			t.Fatalf("query scope drifted: %+v", query)
		}
		unit := "Cel"
		sensorID := "018f2e00-6000-7000-8000-000000000001"
		watermark := query.To
		response := telemetryhistorymodel.DeviceHistoryResponse{
			SchemaVersion: 1, OwningOrganizationID: query.OwningOrganizationID, SiteID: query.SiteID, DeviceID: query.DeviceID,
			Series:   []telemetryhistorymodel.DeviceHistorySeries{{Key: "temperature", Points: []telemetryhistorymodel.DeviceHistoryPoint{{ObservationID: "018f2e00-8000-7000-8000-000000000001", PointID: "018f2e00-5000-7000-8000-000000000001", SensorID: &sensorID, SampledAt: query.From.Add(time.Hour), ReceivedAt: query.From.Add(time.Hour + time.Second), Value: 22.5, Unit: &unit, Quality: telemetryhistorymodel.QualityGood, QualityReasons: []string{}, Revision: 7}}}},
			Metadata: telemetryhistorymodel.DeviceHistoryMetadata{RequestedFrom: query.From, RequestedTo: query.To, DataWatermark: &watermark, DatasetRevision: "telemetry-history:v1:7", Partial: false, MaxPointsPerKey: query.MaxPointsPerKey, ReturnedPoints: 1, TruncatedKeys: []string{}},
		}
		return telemetryJSONResponse(http.StatusOK, response), nil
	})}

	configured := NewHandler(Config{Now: func() time.Time { return now }, Identity: &IdentityConfig{OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback", PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service", ExecutingWorkloadSPIFFE: telemetryTestSPIFFE, PolicyRevision: "identity-policy-1", DelegationSigner: gatewaySigner, TokenEncryptionKey: make([]byte, 32), SessionStore: store, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second, IAMHTTPClient: iamClient}, Telemetry: &TelemetryConfig{RuntimeBaseURL: "https://telemetry.example.test", RuntimeHTTPClient: fixture.runtimeHTTPClient, RuntimeAudience: "telemetry-runtime-service", Timeout: time.Second}, Analytics: &AnalyticsConfig{QueryBaseURL: "https://query.example.test", QueryHTTPClient: fixture.queryHTTPClient, QueryAudience: "telemetry-query-service", Timeout: time.Second}}).(*handler)
	fixture.handler = configured
	csrfCiphertext, err := configured.identity.encryptBytes([]byte(telemetryTestCSRF))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{ID: "session-id", Principal: identitycontext.UserPrincipal{Subject: "fixture-user", Issuer: "https://issuer.example.test", Roles: []string{"operator"}}, ActingOrganizationID: telemetryTestOrganization, CSRFTokenCiphertext: csrfCiphertext, ExpiresAt: now.Add(time.Hour)}, sessionstore.MutationContext{Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "fixture", TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01", ExecutingService: "platform-gateway", ExecutingSPIFFEID: telemetryTestSPIFFE, OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	fixture.sessionID = created.ID
	return fixture
}

func (fixture *telemetryGatewayFixture) authenticate(request *http.Request) {
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fixture.sessionID})
}

func telemetrySnapshot(deviceID string, keys []string, now time.Time) s2telemetryapi.DeviceObservationSnapshot {
	state := s2telemetryapi.DevicePresenceStateOnline
	policy := s2telemetryapi.PolicyRevision(7)
	display := s2telemetryapi.DeviceDisplayStateOnline
	values := make([]s2telemetryapi.TelemetryKeyState, 0, len(keys))
	for _, key := range keys {
		values = append(values, s2telemetryapi.TelemetryKeyState{Missing: &s2telemetryapi.TelemetryMissingState{Key: s2telemetryapi.TelemetryKey(key), State: "MISSING", Freshness: "MISSING", MissingReason: "NEVER_OBSERVED", PolicyRevision: &policy}})
	}
	instant := s2telemetryapi.Instant(now.Format(time.RFC3339Nano))
	return s2telemetryapi.DeviceObservationSnapshot{SchemaVersion: 1, DeviceId: s2telemetryapi.UUIDv7(deviceID), OwningOrganizationId: telemetryTestOrganization, SiteId: telemetryTestSite, BusinessRevision: 9, EvaluatedAt: instant, EvaluationAvailability: s2telemetryapi.EvaluationAvailabilityAvailable, AvailabilityReasons: []s2telemetryapi.AvailabilityReasonCode{}, Presence: s2telemetryapi.PresenceSnapshot{Applicability: s2telemetryapi.PresenceApplicabilityApplicable, CurrentState: &state, LastSeenAt: &instant, PolicyRevision: &policy}, TelemetryReadiness: s2telemetryapi.TelemetryReadinessIncomplete, DisplayState: &display, Values: values}
}

func verifiedWorkloadTLSState(t *testing.T, spiffeID string) *tls.ConnectionState {
	t.Helper()
	identity, err := url.Parse(spiffeID)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{URIs: []*url.URL{identity}}
	return &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
}

func unsignedTelemetryGrant(claims telemetryauth.GrantClaims) string {
	claims.Version = telemetryauth.GrantVersion
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString([]byte("signature"))
}

func telemetryJSONResponse(status int, value any) *http.Response {
	body, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
}

func traceIDFromTraceparent(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) == 4 {
		return parts[1]
	}
	return ""
}

func assertTelemetryProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) s2telemetryapi.ProblemDetails {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, status, recorder.Body.String())
	}
	var problem s2telemetryapi.ProblemDetails
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Status != status || problem.Code != code || problem.TraceId == "" || problem.Instance == "" {
		t.Fatalf("unexpected Problem: %#v", problem)
	}
	return problem
}
