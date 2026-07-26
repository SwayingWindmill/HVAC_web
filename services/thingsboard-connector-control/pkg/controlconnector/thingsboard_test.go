package controlconnector

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type staticTargetResolver struct{ target Target }

func (r staticTargetResolver) ResolveThingsBoardTarget(context.Context, commandmodel.DispatchEnvelope) (Target, error) {
	return r.target, nil
}

type staticCredentialProvider struct{ value string }

func (p staticCredentialProvider) ProviderCredential(context.Context, Target) (string, error) {
	return p.value, nil
}

type memoryEvidenceStore struct {
	mu          sync.Mutex
	prepared    []PreparedEvidence
	completed   []CompletedEvidence
	prepareErr  error
	completeErr error
}

func (s *memoryEvidenceStore) Prepare(_ context.Context, evidence PreparedEvidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepared = append(s.prepared, evidence)
	return s.prepareErr
}

func (s *memoryEvidenceStore) Complete(_ context.Context, evidence CompletedEvidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = append(s.completed, evidence)
	return s.completeErr
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestThingsBoardTwoWayRPCReturnsAcknowledgedButNotVerified(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/rpc/twoway/external-device-1" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("X-Authorization") != providerAuthorization("credential-value") {
			t.Fatal("provider authorization header missing")
		}
		body, _ := io.ReadAll(request.Body)
		receivedBody = string(body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"appliedSetpointC":24.5}`))
	}))
	defer server.Close()

	evidence := &memoryEvidenceStore{}
	connector := newTestThingsBoard(t, server.URL, server.Client(), evidence)
	result, err := connector.Execute(t.Context(), testEnvelope(4, 24.5))
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != commandmodel.ConnectorAcknowledged || !result.Acknowledged || result.Verified {
		t.Fatalf("unexpected connector result %#v", result)
	}
	if !strings.Contains(receivedBody, `"method":"setTemperatureSetpoint"`) || !strings.Contains(receivedBody, `"setpointC":24.5`) {
		t.Fatalf("unexpected RPC body %s", receivedBody)
	}
	if len(evidence.prepared) != 1 || len(evidence.completed) != 1 {
		t.Fatalf("evidence counts prepared=%d completed=%d", len(evidence.prepared), len(evidence.completed))
	}
	if evidence.prepared[0].ProviderEndpoint != "/api/rpc/twoway/{deviceId}" || evidence.prepared[0].ProviderMethod != "setTemperatureSetpoint" {
		t.Fatalf("unexpected prepared evidence %#v", evidence.prepared[0])
	}
	if strings.Contains(evidence.prepared[0].RequestSHA256, "credential") {
		t.Fatal("credential leaked into evidence")
	}
}

func TestThingsBoardHTTPTimeoutIsCommittedUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer server.Close()
	connector := newTestThingsBoard(t, server.URL, server.Client(), &memoryEvidenceStore{})
	result, err := connector.Execute(t.Context(), testEnvelope(5, 24))
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != commandmodel.ConnectorRequestCommitted || result.FailureCode != "THINGSBOARD_RPC_TIMEOUT" {
		t.Fatalf("unexpected result %#v", result)
	}
}

func TestThingsBoardTransportFailureBeforeWriteIsSafePreSend(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused before request write")
	})}
	connector := newTestThingsBoard(t, "http://127.0.0.1:1", client, &memoryEvidenceStore{})
	result, err := connector.Execute(t.Context(), testEnvelope(6, 24))
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != commandmodel.ConnectorPreSendRejected || result.FailureCode != "THINGSBOARD_PRE_SEND_TRANSPORT_ERROR" {
		t.Fatalf("unexpected result %#v", result)
	}
}

func TestThingsBoardEvidenceCompletionFailureAfterWriteFreezesUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	evidence := &memoryEvidenceStore{completeErr: errors.New("store unavailable")}
	connector := newTestThingsBoard(t, server.URL, server.Client(), evidence)
	result, err := connector.Execute(t.Context(), testEnvelope(7, 24))
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != commandmodel.ConnectorRequestCommitted || result.FailureCode != "CONNECTOR_EVIDENCE_COMPLETION_FAILED" {
		t.Fatalf("unexpected result %#v", result)
	}
}

func TestThingsBoardRequiresVerifiedMappingAndRejectsOldFence(t *testing.T) {
	config := testThingsBoardConfig("http://localhost", &http.Client{}, &memoryEvidenceStore{})
	config.AllowLocalVerified = false
	connector, err := NewThingsBoard(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connector.Execute(t.Context(), testEnvelope(1, 24)); !errors.Is(err, ErrMappingNotVerified) {
		t.Fatalf("unverified mapping err=%v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	connector = newTestThingsBoard(t, server.URL, server.Client(), &memoryEvidenceStore{})
	if _, err := connector.Execute(t.Context(), testEnvelope(10, 24)); err != nil {
		t.Fatal(err)
	}
	if _, err := connector.Execute(t.Context(), testEnvelope(9, 24)); !errors.Is(err, ErrOldFence) {
		t.Fatalf("old fence err=%v", err)
	}
}

func newTestThingsBoard(t *testing.T, baseURL string, client *http.Client, evidence EvidenceStore) *ThingsBoard {
	t.Helper()
	connector, err := NewThingsBoard(testThingsBoardConfig(baseURL, client, evidence))
	if err != nil {
		t.Fatal(err)
	}
	return connector
}

func testThingsBoardConfig(baseURL string, client *http.Client, evidence EvidenceStore) ThingsBoardConfig {
	return ThingsBoardConfig{
		BaseURL:    baseURL,
		HTTPClient: client,
		TargetResolver: staticTargetResolver{target: Target{
			IntegrationID: "tb-local", ExternalDeviceID: "external-device-1", BindingRevision: "binding:v1",
		}},
		CredentialProvider: staticCredentialProvider{value: "credential-value"},
		EvidenceStore:      evidence,
		Mappings: []Mapping{{
			Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
			CapabilityRevision: "capability:set-temperature-setpoint:v1",
			MappingRevision:    "thingsboard:set-temperature-setpoint:local-v1",
			Status:             MappingLocalVerified,
			Method:             "setTemperatureSetpoint",
			Timeout:            5 * time.Second,
		}},
		AllowLocalVerified: true,
		Now:                func() time.Time { return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC) },
	}
}

func testEnvelope(fence uint64, setpoint float64) commandmodel.DispatchEnvelope {
	return commandmodel.DispatchEnvelope{
		CommandID: "command-1", AttemptID: "attempt-" + string(rune('a'+fence%20)),
		OrganizationID: "org-1", SiteID: "site-1", DeviceID: "device-1",
		Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision: "capability:set-temperature-setpoint:v1",
		SetpointC:          setpoint, PayloadHash: "payload-hash", ExecutionFence: fence,
		DeviceCommandSequence: 1, LeaseOwner: "dispatcher-a",
		LeaseUntil: time.Date(2026, 7, 26, 12, 1, 0, 0, time.UTC),
	}
}
