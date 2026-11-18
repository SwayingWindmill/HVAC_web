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

type slowProvider struct {
	delay    time.Duration
	response ProviderResponse
}

func (p *slowProvider) Invoke(ctx context.Context, _ ProviderRequest) (ProviderResponse, error) {
	select {
	case <-time.After(p.delay):
		return p.response, nil
	case <-ctx.Done():
		return ProviderResponse{}, ctx.Err()
	}
}

func TestInvokerEnforcesConfiguredTimeout(t *testing.T) {
	provider := &slowProvider{
		delay:    200 * time.Millisecond,
		response: ProviderResponse{Body: json.RawMessage(`{"value":7.2}`)},
	}
	invoker, err := NewInvoker(provider, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	invoker.WithTimeout(20 * time.Millisecond)
	if invoker.ResilienceConfig().Timeout != 20*time.Millisecond {
		t.Fatalf("unexpected timeout=%s", invoker.ResilienceConfig().Timeout)
	}

	var output struct {
		Value float64 `json:"value"`
	}
	provenance, err := invoker.InvokeStructured(t.Context(), validInvocationRequest(), &output)
	var failure *InvocationFailure
	if !errors.As(err, &failure) || failure.Code != FailureProviderUnavailable || !failure.Retryable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout FailureProviderUnavailable, got failure=%#v err=%v", failure, err)
	}
	if provenance.Status != InvocationFailed || provenance.FailureCode != FailureProviderUnavailable {
		t.Fatalf("unexpected provenance=%#v", provenance)
	}
}

func TestInvokeStructuredWithFallbackRecordsFallbackAndPreservesGovernanceGates(t *testing.T) {
	type forecastOut struct {
		Value float64 `json:"value"`
	}

	t.Run("provider outage triggers bumpless fallback and records FALLBACK provenance", func(t *testing.T) {
		provider := &fakeProvider{err: errors.New("upstream 503")}
		cfg := DefaultResilientInvokeConfig()
		cfg.Breaker = NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1, Cooldown: 30 * time.Second})
		invoker, err := NewInvoker(provider, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		invoker.WithResilience(cfg)

		val, prov, fbStatus, err := InvokeStructuredWithFallback(
			t.Context(),
			invoker,
			validInvocationRequest(),
			func(context.Context, error) (forecastOut, error) {
				return forecastOut{Value: 750.0}, nil
			},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val.Value != 750.0 || !fbStatus.FallbackUsed || fbStatus.Reason != FallbackReasonUpstreamError {
			t.Fatalf("unexpected output/status: val=%+v status=%+v", val, fbStatus)
		}
		if prov.Status != InvocationFallback || prov.FallbackReason != FallbackReasonUpstreamError || prov.FailureCode != FailureProviderUnavailable {
			t.Fatalf("unexpected provenance: %+v", prov)
		}

		// Second call short-circuits immediately with circuit_open.
		_, prov2, fbStatus2, err2 := InvokeStructuredWithFallback(
			t.Context(),
			invoker,
			validInvocationRequest(),
			func(context.Context, error) (forecastOut, error) {
				return forecastOut{Value: 750.0}, nil
			},
		)
		if err2 != nil || !fbStatus2.FallbackUsed || fbStatus2.Reason != FallbackReasonCircuitOpen || prov2.FallbackReason != FallbackReasonCircuitOpen {
			t.Fatalf("expected circuit_open fallback, got err=%v status=%+v prov=%+v", err2, fbStatus2, prov2)
		}
		if provider.calls != 1 {
			t.Fatalf("provider.calls=%d, want 1", provider.calls)
		}
	})

	t.Run("policy violation fails before resilience without triggering fallback or tripping breaker", func(t *testing.T) {
		provider := &fakeProvider{response: ProviderResponse{Body: json.RawMessage(`{"value":812.5}`)}}
		cfg := DefaultResilientInvokeConfig()
		cfg.Breaker = NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1})
		invoker, _ := NewInvoker(provider, time.Now)
		invoker.WithResilience(cfg)

		req := validInvocationRequest()
		req.DataClasses = []string{"RAW_PERSONAL_DATA"}
		fbCalled := false
		_, prov, fbStatus, err := InvokeStructuredWithFallback(
			t.Context(),
			invoker,
			req,
			func(context.Context, error) (forecastOut, error) {
				fbCalled = true
				return forecastOut{Value: 1.0}, nil
			},
		)
		var failure *InvocationFailure
		if !errors.As(err, &failure) || failure.Code != FailureDataEgressDenied || fbCalled || fbStatus.FallbackUsed {
			t.Fatalf("expected immediate egress failure without fallback, got err=%v fbCalled=%v status=%+v", err, fbCalled, fbStatus)
		}
		if prov.Status != InvocationFailed || prov.FailureCode != FailureDataEgressDenied || cfg.Breaker.State() != BreakerClosed {
			t.Fatalf("unexpected provenance/breaker: prov=%+v state=%s", prov, cfg.Breaker.State())
		}
	})
}
