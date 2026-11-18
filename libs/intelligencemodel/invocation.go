package intelligencemodel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	FailureProviderUnavailable = "PROVIDER_UNAVAILABLE"
	FailureOutputSchemaInvalid = "OUTPUT_SCHEMA_INVALID"
	FailureBudgetExceeded      = "BUDGET_EXCEEDED"
	FailureDataEgressDenied    = "DATA_EGRESS_DENIED"
	FailureDeploymentDisabled  = "DEPLOYMENT_DISABLED"
)

type InvocationFailure struct {
	Code      string
	Retryable bool
	Cause     error
}

func (failure *InvocationFailure) Error() string {
	if failure == nil {
		return ""
	}
	if failure.Cause != nil {
		return failure.Code + ": " + failure.Cause.Error()
	}
	return failure.Code
}

func (failure *InvocationFailure) Unwrap() error { return failure.Cause }

type ProviderRequest struct {
	ModelID          string
	CredentialRef    string
	Input            json.RawMessage
	OutputSchemaName string
}

type ProviderResponse struct {
	Body       json.RawMessage
	RequestID  string
	TokenUsage int64
	CostMicros int64
	Latency    time.Duration
}

type Provider interface {
	Invoke(context.Context, ProviderRequest) (ProviderResponse, error)
}

type InvocationRequest struct {
	ID                 string
	TenantID           string
	SiteID             string
	UseCase            UseCase
	Model              ModelDefinition
	Deployment         DeploymentRevision
	EgressPolicy       DataEgressPolicy
	InputSnapshotID    string
	EvidenceIDs        []string
	Input              json.RawMessage
	DataClasses        []string
	BudgetMicros       int64
	ExpectedCostMicros int64
	OutputSchemaName   string
}

type Invoker struct {
	provider Provider
	now      func() time.Time
	timeout  time.Duration
	breaker  *CircuitBreaker
	bulkhead *Bulkhead
}

func NewInvoker(provider Provider, now func() time.Time) (*Invoker, error) {
	if provider == nil {
		return nil, errors.New("model provider is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Invoker{
		provider: provider,
		now:      now,
		timeout:  DefaultInferenceCallTimeout,
	}, nil
}

// WithTimeout configures the per-call provider timeout enforced by Invoker.
// Non-positive durations keep the current timeout (default DefaultInferenceCallTimeout).
func (invoker *Invoker) WithTimeout(timeout time.Duration) *Invoker {
	if invoker != nil && timeout > 0 {
		invoker.timeout = timeout
	}
	return invoker
}

// WithResilience attaches circuit breaker, bulkhead, and optional timeout settings to Invoker.
func (invoker *Invoker) WithResilience(cfg ResilientInvokeConfig) *Invoker {
	if invoker == nil {
		return nil
	}
	if cfg.Timeout > 0 {
		invoker.timeout = cfg.Timeout
	}
	invoker.breaker = cfg.Breaker
	invoker.bulkhead = cfg.Bulkhead
	return invoker
}

// ResilienceConfig returns the effective resilient invocation settings for Invoker.
func (invoker *Invoker) ResilienceConfig() ResilientInvokeConfig {
	if invoker == nil {
		return ResilientInvokeConfig{Timeout: DefaultInferenceCallTimeout}
	}
	timeout := invoker.timeout
	if timeout <= 0 {
		timeout = DefaultInferenceCallTimeout
	}
	return ResilientInvokeConfig{
		Timeout:  timeout,
		Breaker:  invoker.breaker,
		Bulkhead: invoker.bulkhead,
	}
}

func (invoker *Invoker) InvokeStructured(ctx context.Context, request InvocationRequest, output any) (InvocationProvenance, error) {
	if invoker == nil || invoker.provider == nil {
		return InvocationProvenance{Status: InvocationFailed, FailureCode: FailureProviderUnavailable}, &InvocationFailure{
			Code: FailureProviderUnavailable, Retryable: false, Cause: errors.New("model invoker is not initialized"),
		}
	}
	provenance := newInvocationProvenance(request, invoker.now().UTC())
	if failure := validateInvocationRequest(request); failure != nil {
		provenance.FailureCode = failure.Code
		return provenance, failure
	}

	providerReq := buildProviderRequest(request)
	response, _, err := executeResilient(ctx, invoker.ResilienceConfig(), func(callCtx context.Context) (ProviderResponse, error) {
		return invoker.provider.Invoke(callCtx, providerReq)
	})
	if err != nil {
		failure := &InvocationFailure{Code: FailureProviderUnavailable, Retryable: true, Cause: err}
		provenance.FailureCode = failure.Code
		return provenance, failure
	}
	if failure := applyProviderResponseAndDecode(request, response, output, &provenance); failure != nil {
		provenance.FailureCode = failure.Code
		return provenance, failure
	}
	provenance.Status = InvocationSucceeded
	return provenance, nil
}

type structuredCallOutcome[T any] struct {
	value      T
	provenance InvocationProvenance
}

// InvokeStructuredWithFallback runs a structured model invocation through InvokeWithBumplessFallback
// using invoker's timeout, breaker, and bulkhead configuration, recording FALLBACK provenance when
// the fallback handler supplies the replacement output.
func InvokeStructuredWithFallback[T any](
	ctx context.Context,
	invoker *Invoker,
	request InvocationRequest,
	fallback func(context.Context, error) (T, error),
) (T, InvocationProvenance, InvocationFallbackStatus, error) {
	var zero T
	if invoker == nil || invoker.provider == nil {
		failure := &InvocationFailure{Code: FailureProviderUnavailable, Retryable: false, Cause: errors.New("model invoker is not initialized")}
		return zero, InvocationProvenance{Status: InvocationFailed, FailureCode: FailureProviderUnavailable}, InvocationFallbackStatus{}, failure
	}
	provenance := newInvocationProvenance(request, invoker.now().UTC())
	if failure := validateInvocationRequest(request); failure != nil {
		provenance.FailureCode = failure.Code
		return zero, provenance, InvocationFallbackStatus{}, failure
	}

	providerReq := buildProviderRequest(request)
	var wrappedFallback func(context.Context, error) (structuredCallOutcome[T], error)
	if fallback != nil {
		wrappedFallback = func(fbCtx context.Context, cause error) (structuredCallOutcome[T], error) {
			fbVal, fbErr := fallback(fbCtx, cause)
			fbProv := provenance
			fbProv.FailureCode = failureCodeForFallbackCause(cause)
			return structuredCallOutcome[T]{value: fbVal, provenance: fbProv}, fbErr
		}
	}

	outcome, fbStatus, err := InvokeWithBumplessFallback(
		ctx,
		invoker.ResilienceConfig(),
		func(callCtx context.Context) (structuredCallOutcome[T], error) {
			response, invokeErr := invoker.provider.Invoke(callCtx, providerReq)
			if invokeErr != nil {
				return structuredCallOutcome[T]{}, &InvocationFailure{Code: FailureProviderUnavailable, Retryable: true, Cause: invokeErr}
			}
			var decoded T
			localProv := provenance
			if failure := applyProviderResponseAndDecode(request, response, &decoded, &localProv); failure != nil {
				return structuredCallOutcome[T]{}, failure
			}
			localProv.Status = InvocationSucceeded
			return structuredCallOutcome[T]{value: decoded, provenance: localProv}, nil
		},
		wrappedFallback,
	)
	if err != nil {
		outProv := outcome.provenance
		if outProv.ID == "" && outProv.InputDigest == "" {
			outProv = provenance
		}
		outProv.Status = InvocationFailed
		if outProv.FailureCode == "" {
			outProv.FailureCode = failureCodeForFallbackCause(err)
		}
		outProv.FallbackReason = fbStatus.Reason
		return outcome.value, outProv, fbStatus, err
	}
	if fbStatus.FallbackUsed {
		fbProv := outcome.provenance
		fbProv.Status = InvocationFallback
		fbProv.FallbackReason = fbStatus.Reason
		return outcome.value, fbProv, fbStatus, nil
	}
	return outcome.value, outcome.provenance, fbStatus, nil
}

func newInvocationProvenance(request InvocationRequest, startedAt time.Time) InvocationProvenance {
	digest := sha256.Sum256(request.Input)
	return InvocationProvenance{
		ID:                   request.ID,
		TenantID:             request.TenantID,
		SiteID:               request.SiteID,
		UseCase:              request.UseCase,
		DeploymentRevisionID: request.Deployment.ID,
		InputSnapshotID:      request.InputSnapshotID,
		EvidenceIDs:          append([]string(nil), request.EvidenceIDs...),
		OutputSchemaVersion:  request.Deployment.OutputSchemaVersion,
		Status:               InvocationFailed,
		InputDigest:          hex.EncodeToString(digest[:]),
		CreatedAt:            startedAt,
	}
}

func buildProviderRequest(request InvocationRequest) ProviderRequest {
	return ProviderRequest{
		ModelID:          request.Model.ModelID,
		CredentialRef:    request.Model.CredentialRef,
		Input:            append(json.RawMessage(nil), request.Input...),
		OutputSchemaName: request.OutputSchemaName,
	}
}

func applyProviderResponseAndDecode(request InvocationRequest, response ProviderResponse, output any, provenance *InvocationProvenance) *InvocationFailure {
	provenance.ProviderRequestID = strings.TrimSpace(response.RequestID)
	provenance.TokenUsage = response.TokenUsage
	provenance.CostMicros = response.CostMicros
	provenance.LatencyMillis = response.Latency.Milliseconds()
	if request.BudgetMicros > 0 && response.CostMicros > request.BudgetMicros {
		return &InvocationFailure{
			Code:      FailureBudgetExceeded,
			Retryable: false,
			Cause:     fmt.Errorf("provider cost %d exceeds budget %d", response.CostMicros, request.BudgetMicros),
		}
	}
	if output == nil {
		return &InvocationFailure{
			Code:      FailureOutputSchemaInvalid,
			Retryable: false,
			Cause:     errors.New("structured output destination is required"),
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil || ensureJSONEOF(decoder) != nil {
		if err == nil {
			err = errors.New("structured output contains trailing JSON")
		}
		return &InvocationFailure{Code: FailureOutputSchemaInvalid, Retryable: false, Cause: err}
	}
	return nil
}

func failureCodeForFallbackCause(err error) string {
	var failure *InvocationFailure
	if errors.As(err, &failure) && failure.Code != "" {
		return failure.Code
	}
	return FailureProviderUnavailable
}

func validateInvocationRequest(request InvocationRequest) *InvocationFailure {
	if !request.Deployment.Enabled {
		return &InvocationFailure{Code: FailureDeploymentDisabled, Retryable: false}
	}
	if request.Model.ID != request.Deployment.ModelDefinitionID || request.Model.TenantID != request.TenantID || request.Deployment.TenantID != request.TenantID {
		return &InvocationFailure{Code: FailureDataEgressDenied, Retryable: false, Cause: errors.New("model deployment is outside the invocation tenant")}
	}
	if len(request.Input) == 0 || !json.Valid(request.Input) {
		return &InvocationFailure{Code: FailureOutputSchemaInvalid, Retryable: false, Cause: errors.New("invocation input must be valid JSON")}
	}
	if request.Model.Provider != "LOCAL" {
		if !request.EgressPolicy.Enabled || request.Deployment.DataEgressPolicyID == "" || request.Deployment.DataEgressPolicyID != request.EgressPolicy.ID || request.EgressPolicy.TenantID != request.TenantID {
			return &InvocationFailure{Code: FailureDataEgressDenied, Retryable: false, Cause: errors.New("external provider requires the active deployment data-egress policy")}
		}
		if request.EgressPolicy.MaxInputBytes > 0 && int64(len(request.Input)) > request.EgressPolicy.MaxInputBytes {
			return &InvocationFailure{Code: FailureDataEgressDenied, Retryable: false, Cause: errors.New("input exceeds data-egress size limit")}
		}
		allowed := make(map[string]struct{}, len(request.EgressPolicy.AllowedDataClasses))
		for _, dataClass := range request.EgressPolicy.AllowedDataClasses {
			allowed[dataClass] = struct{}{}
		}
		for _, dataClass := range request.DataClasses {
			if _, ok := allowed[dataClass]; !ok {
				return &InvocationFailure{Code: FailureDataEgressDenied, Retryable: false, Cause: fmt.Errorf("data class %q is not allowed", dataClass)}
			}
		}
	}
	if request.BudgetMicros > 0 && request.ExpectedCostMicros > request.BudgetMicros {
		return &InvocationFailure{Code: FailureBudgetExceeded, Retryable: false, Cause: errors.New("expected provider cost exceeds invocation budget")}
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("trailing JSON")
}
