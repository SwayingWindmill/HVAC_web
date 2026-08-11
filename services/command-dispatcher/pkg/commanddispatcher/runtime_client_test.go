package commanddispatcher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/services/command-service/pkg/commandservice"
)

const (
	runtimeClientTestOrganization = "018f3e00-0000-7000-8000-000000000001"
	runtimeClientTestSite         = "018f3e00-1000-7000-8000-000000000001"
	runtimeClientTestDevice       = "018f3e00-3000-7000-8000-000000000001"
)

func TestRuntimeClientClaimsAndResolvesDispatch(t *testing.T) {
	envelope := commandmodel.DispatchEnvelope{
		CommandID: "command-1", AttemptID: "attempt-1", OrganizationID: runtimeClientTestOrganization, SiteID: runtimeClientTestSite, DeviceID: runtimeClientTestDevice,
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, CapabilityRevision: "capability:set-temperature-setpoint:v1",
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 22}, PayloadHash: "hash", ExecutionFence: 1, DeviceCommandSequence: 1, LeaseOwner: "dispatcher-a", LeaseUntil: time.Now().UTC().Add(time.Minute),
	}
	resolved := false
	prepared := false
	completed := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case commandservice.InternalDispatchClaimPath:
			var input map[string]any
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input["leaseOwner"] != "dispatcher-a" || input["leaseSeconds"] != float64(30) || input["organizationId"] != nil || input["siteId"] != nil || input["deviceId"] != nil {
				t.Fatalf("invalid claim input: %#v err=%v", input, err)
			}
			writeTestJSON(writer, http.StatusOK, envelope)
		case commandservice.InternalDispatchResolvePath:
			var input runtimeDispatchResolveRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Envelope.AttemptID != "attempt-1" || input.Result.EvidenceID != "evidence-1" {
				t.Fatalf("invalid resolve input: %#v err=%v", input, err)
			}
			resolved = true
			writer.WriteHeader(http.StatusNoContent)
		case commandservice.InternalConnectorPreparePath:
			var input commandmodel.PreparedConnectorEvidence
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.AttemptID != "attempt-1" {
				t.Fatalf("invalid prepared evidence: %#v err=%v", input, err)
			}
			prepared = true
			writer.WriteHeader(http.StatusNoContent)
		case commandservice.InternalConnectorCompletePath:
			var input commandmodel.CompletedConnectorEvidence
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.AttemptID != "attempt-1" || input.ConnectorPhase != commandmodel.ConnectorAcknowledged {
				t.Fatalf("invalid completed evidence: %#v err=%v", input, err)
			}
			completed = true
			writer.WriteHeader(http.StatusNoContent)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewRuntimeClient(RuntimeClientConfig{
		BaseURL: server.URL, HTTPClient: server.Client(),
		OrganizationID: runtimeClientTestOrganization, SiteID: runtimeClientTestSite, DeviceID: runtimeClientTestDevice,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := client.ClaimDispatch(context.Background(), runtimeClientTestOrganization, "dispatcher-a", 30*time.Second)
	if err != nil || claimed.AttemptID != envelope.AttemptID {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	if err := client.ResolveDispatch(context.Background(), claimed, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorAcknowledged, EvidenceID: "evidence-1", Acknowledged: true,
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !resolved {
		t.Fatal("resolve request was not observed")
	}
	preparedEvidence := commandmodel.PreparedConnectorEvidence{
		AttemptID: "attempt-1", CommandID: "command-1", OrganizationID: runtimeClientTestOrganization, SiteID: runtimeClientTestSite, DeviceID: runtimeClientTestDevice,
		ExternalDeviceID: "tb-device-1", ExecutionFence: 1, PayloadHash: "hash", MappingRevision: "mapping-1",
		BindingRevision: "binding-1", ProviderEndpoint: "/api/rpc/twoway/{deviceId}", ProviderMethod: "setTemperatureSetpoint",
		RequestSHA256: "request-hash", PreparedAt: time.Now().UTC(),
	}
	if err := client.Prepare(context.Background(), preparedEvidence); err != nil {
		t.Fatalf("prepare evidence: %v", err)
	}
	if err := client.Complete(context.Background(), commandmodel.CompletedConnectorEvidence{
		PreparedConnectorEvidence: preparedEvidence, ProviderStatusCode: http.StatusOK, ResponseSHA256: "response-hash",
		RequestWritten: true, ConnectorPhase: commandmodel.ConnectorAcknowledged, CompletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("complete evidence: %v", err)
	}
	if !prepared || !completed {
		t.Fatalf("evidence requests prepared=%v completed=%v", prepared, completed)
	}
}

func TestRuntimeClientMapsNoWorkAndStaleFence(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case commandservice.InternalDispatchClaimPath:
			writer.WriteHeader(http.StatusNoContent)
		case commandservice.InternalDispatchResolvePath:
			writeTestJSON(writer, http.StatusConflict, map[string]any{"code": "COMMAND_RUNTIME_STALE_FENCE", "retryable": false})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewRuntimeClient(RuntimeClientConfig{
		BaseURL: server.URL, HTTPClient: server.Client(),
		OrganizationID: runtimeClientTestOrganization, SiteID: runtimeClientTestSite, DeviceID: runtimeClientTestDevice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ClaimDispatch(context.Background(), "other-org", "dispatcher-a", 30*time.Second); !errors.Is(err, commandservice.ErrInvalidRequest) {
		t.Fatalf("expected wrong Organization to fail locally, got %v", err)
	}
	if _, err := client.ClaimDispatch(context.Background(), runtimeClientTestOrganization, "dispatcher-a", 30*time.Second); !errors.Is(err, commandservice.ErrNoDispatchAvailable) {
		t.Fatalf("expected no work, got %v", err)
	}
	if err := client.ResolveDispatch(context.Background(), commandmodel.DispatchEnvelope{}, commandmodel.ConnectorResult{}); !errors.Is(err, commandservice.ErrStaleFence) {
		t.Fatalf("expected stale fence, got %v", err)
	}
}

func writeTestJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
