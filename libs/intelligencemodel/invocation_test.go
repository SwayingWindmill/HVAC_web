package intelligencemodel

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type fakeProvider struct {
	response ProviderResponse
	err      error
	calls    int
	request  ProviderRequest
}

func (provider *fakeProvider) Invoke(_ context.Context, request ProviderRequest) (ProviderResponse, error) {
	provider.calls++
	provider.request = request
	return provider.response, provider.err
}

func validInvocationRequest() InvocationRequest {
	return InvocationRequest{
		ID: "invocation-1", TenantID: "tenant-1", SiteID: "site-1", UseCase: UseCaseForecast,
		Model:           ModelDefinition{ID: "model-1", TenantID: "tenant-1", Name: "load-model", Provider: "OPENAI", ModelID: "provider-model-1", Capabilities: []string{"FORECAST"}, CredentialRef: "credential-ref-1", Status: "ACTIVE", Revision: 1},
		Deployment:      DeploymentRevision{ID: "deployment-1", TenantID: "tenant-1", ModelDefinitionID: "model-1", UseCase: UseCaseForecast, Revision: 1, OutputSchemaVersion: "forecast/v1", DataEgressPolicyID: "egress-1", Enabled: true, CreatedAt: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)},
		EgressPolicy:    DataEgressPolicy{ID: "egress-1", TenantID: "tenant-1", Name: "forecast-egress", AllowedDataClasses: []string{"TELEMETRY_AGGREGATE", "WEATHER"}, MaxInputBytes: 4096, Enabled: true, Revision: 1},
		InputSnapshotID: "input-1", EvidenceIDs: []string{"evidence-1"}, Input: json.RawMessage(`{"siteLoadKw":812.5}`), DataClasses: []string{"TELEMETRY_AGGREGATE"},
		BudgetMicros: 5000, ExpectedCostMicros: 1000, OutputSchemaName: "forecast/v1",
	}
}

func TestInvokerReturnsExplicitProviderOutage(t *testing.T) {
	provider := &fakeProvider{err: errors.New("network unavailable")}
	invoker, _ := NewInvoker(provider, time.Now)
	provenance, err := invoker.InvokeStructured(t.Context(), validInvocationRequest(), &struct {
		Value float64 `json:"value"`
	}{})
	var failure *InvocationFailure
	if !errors.As(err, &failure) || failure.Code != FailureProviderUnavailable || !failure.Retryable {
		t.Fatalf("failure=%#v err=%v", failure, err)
	}
	if provenance.Status != InvocationFailed || provenance.FailureCode != FailureProviderUnavailable || provider.calls != 1 {
		t.Fatalf("provenance=%#v calls=%d", provenance, provider.calls)
	}
}

func TestInvokerRejectsOutputOutsideStrictSchema(t *testing.T) {
	provider := &fakeProvider{response: ProviderResponse{Body: json.RawMessage(`{"value":812.5,"unexpected":true}`), CostMicros: 500}}
	invoker, _ := NewInvoker(provider, time.Now)
	var output struct {
		Value float64 `json:"value"`
	}
	provenance, err := invoker.InvokeStructured(t.Context(), validInvocationRequest(), &output)
	var failure *InvocationFailure
	if !errors.As(err, &failure) || failure.Code != FailureOutputSchemaInvalid || failure.Retryable {
		t.Fatalf("failure=%#v err=%v", failure, err)
	}
	if provenance.FailureCode != FailureOutputSchemaInvalid {
		t.Fatalf("provenance=%#v", provenance)
	}
}

func TestInvokerBlocksBudgetBeforeProviderCall(t *testing.T) {
	provider := &fakeProvider{}
	invoker, _ := NewInvoker(provider, time.Now)
	request := validInvocationRequest()
	request.ExpectedCostMicros = 6000
	provenance, err := invoker.InvokeStructured(t.Context(), request, &struct{}{})
	var failure *InvocationFailure
	if !errors.As(err, &failure) || failure.Code != FailureBudgetExceeded || provider.calls != 0 {
		t.Fatalf("failure=%#v calls=%d", failure, provider.calls)
	}
	if provenance.FailureCode != FailureBudgetExceeded {
		t.Fatalf("provenance=%#v", provenance)
	}
}

func TestInvokerBlocksDisallowedDataEgressBeforeProviderCall(t *testing.T) {
	provider := &fakeProvider{}
	invoker, _ := NewInvoker(provider, time.Now)
	request := validInvocationRequest()
	request.DataClasses = []string{"RAW_PERSONAL_DATA"}
	provenance, err := invoker.InvokeStructured(t.Context(), request, &struct{}{})
	var failure *InvocationFailure
	if !errors.As(err, &failure) || failure.Code != FailureDataEgressDenied || provider.calls != 0 {
		t.Fatalf("failure=%#v calls=%d", failure, provider.calls)
	}
	if provenance.FailureCode != FailureDataEgressDenied {
		t.Fatalf("provenance=%#v", provenance)
	}
}

func TestInvokerRecordsProvenanceWithoutCredentialMaterial(t *testing.T) {
	provider := &fakeProvider{response: ProviderResponse{Body: json.RawMessage(`{"value":812.5}`), RequestID: "provider-request-1", TokenUsage: 42, CostMicros: 700, Latency: 25 * time.Millisecond}}
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	invoker, _ := NewInvoker(provider, func() time.Time { return now })
	var output struct {
		Value float64 `json:"value"`
	}
	provenance, err := invoker.InvokeStructured(t.Context(), validInvocationRequest(), &output)
	if err != nil {
		t.Fatal(err)
	}
	if output.Value != 812.5 || provenance.Status != InvocationSucceeded || provenance.ProviderRequestID != "provider-request-1" || provenance.TokenUsage != 42 || provenance.CostMicros != 700 || provenance.LatencyMillis != 25 {
		t.Fatalf("output=%#v provenance=%#v", output, provenance)
	}
	if provider.request.CredentialRef != "credential-ref-1" {
		t.Fatalf("provider request should carry only the credential reference: %#v", provider.request)
	}
	payload, err := json.Marshal(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "" || containsCredentialMaterial(string(payload), "credential-ref-1") {
		t.Fatalf("provenance must not expose credential reference or secret material: %s", payload)
	}
}

func containsCredentialMaterial(payload, credentialRef string) bool {
	return credentialRef != "" && (len(payload) >= len(credentialRef)) && jsonContains(payload, credentialRef)
}

func jsonContains(payload, fragment string) bool {
	for index := 0; index+len(fragment) <= len(payload); index++ {
		if payload[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
