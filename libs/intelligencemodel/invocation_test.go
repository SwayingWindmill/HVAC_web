package intelligencemodel

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

type fakeProvider struct {
	response       ProviderResponse
	err            error
	calls          int
	request        ProviderRequest
	ctxTraceparent string
	ctxTraceID     string
}

func (provider *fakeProvider) Invoke(ctx context.Context, request ProviderRequest) (ProviderResponse, error) {
	provider.calls++
	provider.request = request
	provider.ctxTraceparent = TraceparentFromContext(ctx)
	provider.ctxTraceID = TraceIDFromContext(ctx)
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

func TestNormalizeTraceID32(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "valid lowercase", raw: "4bf92f3577b34da6a3ce929d0e0e4736", want: "4bf92f3577b34da6a3ce929d0e0e4736"},
		{name: "uppercase trimmed", raw: "  4BF92F3577B34DA6A3CE929D0E0E4736\t", want: "4bf92f3577b34da6a3ce929d0e0e4736"},
		{name: "all zeros rejected", raw: "00000000000000000000000000000000", want: ""},
		{name: "wrong length rejected", raw: "4bf92f3577b34da6", want: ""},
		{name: "non hex rejected", raw: "4bf92f3577b34da6a3ce929d0e0e473z", want: ""},
		{name: "empty rejected", raw: "   ", want: ""},
	}
	for _, tc := range cases {
		if got := NormalizeTraceID32(tc.raw); got != tc.want {
			t.Fatalf("%s: NormalizeTraceID32(%q) = %q, want %q", tc.name, tc.raw, got, tc.want)
		}
	}
}

func TestTraceContextGenerationAndStructuredPropagation(t *testing.T) {
	const validTP = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	const validTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	tpPattern := regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$`)

	var nilCtx context.Context
	if got := TraceparentFromContext(nilCtx); got != "" {
		t.Fatalf("nil context traceparent=%q, want empty", got)
	}
	if got := TraceIDFromContext(nilCtx); got != "" {
		t.Fatalf("nil context traceID=%q, want empty", got)
	}

	// Invalid or all-zero traceparents are ignored by WithTraceContext.
	for _, bad := range []string{
		"",
		"not-a-traceparent",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01",
	} {
		if got := TraceparentFromContext(WithTraceContext(t.Context(), bad)); got != "" {
			t.Fatalf("invalid traceparent %q leaked into context as %q", bad, got)
		}
	}

	// Valid WithTraceContext round-trips and inherits into GenerateTraceparent / EnsureTraceContext.
	ctx := WithTraceContext(t.Context(), "  "+validTP+"  ")
	if TraceparentFromContext(ctx) != validTP || TraceIDFromContext(ctx) != validTraceID {
		t.Fatalf("unexpected context trace: tp=%q id=%q", TraceparentFromContext(ctx), TraceIDFromContext(ctx))
	}
	genID, genTP := GenerateTraceparent(ctx)
	if genID != validTraceID || genTP != validTP {
		t.Fatalf("GenerateTraceparent did not inherit context: id=%q tp=%q", genID, genTP)
	}

	// Observability remote parent is automatically inherited.
	obsCtx := observability.ContextWithRemoteParent(t.Context(), observability.NewTracer("optimization-service", nil), validTP, "")
	if TraceparentFromContext(obsCtx) != validTP || TraceIDFromContext(obsCtx) != validTraceID {
		t.Fatalf("observability context not inherited: tp=%q id=%q", TraceparentFromContext(obsCtx), TraceIDFromContext(obsCtx))
	}

	// Empty context generates fresh non-zero W3C traceparent.
	freshID, freshTP := GenerateTraceparent(t.Context())
	match := tpPattern.FindStringSubmatch(freshTP)
	if len(match) != 4 || match[1] != freshID || match[1] == strings.Repeat("0", 32) || match[2] == strings.Repeat("0", 16) {
		t.Fatalf("invalid generated traceparent: id=%q tp=%q", freshID, freshTP)
	}

	// EnsureTraceContext accepts valid incoming traceparent and rejects invalid incoming by generating fresh.
	ensuredCtx, ensuredID, ensuredTP := EnsureTraceContext(t.Context(), validTP)
	if ensuredID != validTraceID || ensuredTP != validTP || TraceparentFromContext(ensuredCtx) != validTP {
		t.Fatalf("EnsureTraceContext valid mismatch: id=%q tp=%q", ensuredID, ensuredTP)
	}
	fallbackCtx, fallbackID, fallbackTP := EnsureTraceContext(t.Context(), "00-00000000000000000000000000000000-00f067aa0ba902b7-01")
	if !tpPattern.MatchString(fallbackTP) || fallbackID == strings.Repeat("0", 32) || TraceIDFromContext(fallbackCtx) != fallbackID {
		t.Fatalf("EnsureTraceContext fallback invalid: id=%q tp=%q", fallbackID, fallbackTP)
	}

	// InjectTraceparent sets header only when present in context.
	reqWithout := httptest.NewRequest(http.MethodPost, "http://example.test/infer", nil)
	InjectTraceparent(t.Context(), reqWithout.Header)
	if got := reqWithout.Header.Get("traceparent"); got != "" {
		t.Fatalf("expected empty traceparent header without context trace, got %q", got)
	}
	reqWith := httptest.NewRequest(http.MethodPost, "http://example.test/infer", nil)
	InjectTraceparent(ctx, reqWith.Header)
	if got := reqWith.Header.Get("traceparent"); got != validTP {
		t.Fatalf("traceparent header=%q, want %q", got, validTP)
	}

	// Structured invocation propagates traceparent and traceID to ProviderRequest and callCtx.
	provider := &fakeProvider{response: ProviderResponse{Body: json.RawMessage(`{"value":812.5}`), CostMicros: 500}}
	invoker, err := NewInvoker(provider, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Value float64 `json:"value"`
	}
	if _, err := invoker.InvokeStructured(ctx, validInvocationRequest(), &out); err != nil {
		t.Fatal(err)
	}
	if provider.request.Traceparent != validTP || provider.request.TraceID != validTraceID || provider.ctxTraceparent != validTP || provider.ctxTraceID != validTraceID {
		t.Fatalf("InvokeStructured did not propagate trace: req=%#v ctxTP=%q ctxID=%q", provider.request, provider.ctxTraceparent, provider.ctxTraceID)
	}

	// InvokeStructuredWithFallback propagates traceparent as well.
	provider.calls = 0
	type forecastOut struct {
		Value float64 `json:"value"`
	}
	if _, _, _, err := InvokeStructuredWithFallback[forecastOut](obsCtx, invoker, validInvocationRequest(), nil); err != nil {
		t.Fatal(err)
	}
	if provider.request.Traceparent != validTP || provider.request.TraceID != validTraceID || provider.ctxTraceparent != validTP {
		t.Fatalf("InvokeStructuredWithFallback did not propagate trace: req=%#v ctxTP=%q", provider.request, provider.ctxTraceparent)
	}
}

func TestNewDecisionUUIDv7(t *testing.T) {
	uuidV7Pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	ts := time.Date(2026, 11, 18, 5, 10, 11, 123*int(time.Millisecond), time.UTC)

	id1, err := NewDecisionUUIDv7(ts)
	if err != nil {
		t.Fatalf("NewDecisionUUIDv7 failed: %v", err)
	}
	id2, err := NewDecisionUUIDv7(ts)
	if err != nil {
		t.Fatalf("NewDecisionUUIDv7 second call failed: %v", err)
	}
	if !uuidV7Pattern.MatchString(id1) || !uuidV7Pattern.MatchString(id2) {
		t.Fatalf("invalid UUIDv7 format: %q %q", id1, id2)
	}
	if id1 == id2 {
		t.Fatalf("expected distinct random bits across calls, got identical %q", id1)
	}

	rawHex := strings.ReplaceAll(id1, "-", "")
	decoded, err := hex.DecodeString(rawHex)
	if err != nil || len(decoded) != 16 {
		t.Fatalf("decode UUIDv7 hex failed: %v", err)
	}
	gotMillis := uint64(decoded[0])<<40 | uint64(decoded[1])<<32 | uint64(decoded[2])<<24 | uint64(decoded[3])<<16 | uint64(decoded[4])<<8 | uint64(decoded[5])
	if gotMillis != uint64(ts.UnixMilli()) {
		t.Fatalf("encoded millis=%d, want %d", gotMillis, ts.UnixMilli())
	}

	zeroID, err := NewDecisionUUIDv7(time.Time{})
	if err != nil || !uuidV7Pattern.MatchString(zeroID) {
		t.Fatalf("zero time UUIDv7 failed: id=%q err=%v", zeroID, err)
	}
	if _, err := NewDecisionUUIDv7(time.UnixMilli(-1)); err == nil {
		t.Fatal("expected pre-epoch timestamp to return error")
	}
}
