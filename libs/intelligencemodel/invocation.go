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
}

func NewInvoker(provider Provider, now func() time.Time) (*Invoker, error) {
	if provider == nil {
		return nil, errors.New("model provider is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Invoker{provider: provider, now: now}, nil
}

func (invoker *Invoker) InvokeStructured(ctx context.Context, request InvocationRequest, output any) (InvocationProvenance, error) {
	startedAt := invoker.now().UTC()
	provenance := InvocationProvenance{
		ID: request.ID, TenantID: request.TenantID, SiteID: request.SiteID, UseCase: request.UseCase,
		DeploymentRevisionID: request.Deployment.ID, InputSnapshotID: request.InputSnapshotID,
		EvidenceIDs: append([]string(nil), request.EvidenceIDs...), OutputSchemaVersion: request.Deployment.OutputSchemaVersion,
		Status: InvocationFailed, CreatedAt: startedAt,
	}
	digest := sha256.Sum256(request.Input)
	provenance.InputDigest = hex.EncodeToString(digest[:])
	if failure := validateInvocationRequest(request); failure != nil {
		provenance.FailureCode = failure.Code
		return provenance, failure
	}
	response, err := invoker.provider.Invoke(ctx, ProviderRequest{
		ModelID: request.Model.ModelID, CredentialRef: request.Model.CredentialRef,
		Input: append(json.RawMessage(nil), request.Input...), OutputSchemaName: request.OutputSchemaName,
	})
	if err != nil {
		failure := &InvocationFailure{Code: FailureProviderUnavailable, Retryable: true, Cause: err}
		provenance.FailureCode = failure.Code
		return provenance, failure
	}
	provenance.ProviderRequestID = strings.TrimSpace(response.RequestID)
	provenance.TokenUsage = response.TokenUsage
	provenance.CostMicros = response.CostMicros
	provenance.LatencyMillis = response.Latency.Milliseconds()
	if request.BudgetMicros > 0 && response.CostMicros > request.BudgetMicros {
		failure := &InvocationFailure{Code: FailureBudgetExceeded, Retryable: false, Cause: fmt.Errorf("provider cost %d exceeds budget %d", response.CostMicros, request.BudgetMicros)}
		provenance.FailureCode = failure.Code
		return provenance, failure
	}
	if output == nil {
		failure := &InvocationFailure{Code: FailureOutputSchemaInvalid, Retryable: false, Cause: errors.New("structured output destination is required")}
		provenance.FailureCode = failure.Code
		return provenance, failure
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(output); err != nil || ensureJSONEOF(decoder) != nil {
		if err == nil {
			err = errors.New("structured output contains trailing JSON")
		}
		failure := &InvocationFailure{Code: FailureOutputSchemaInvalid, Retryable: false, Cause: err}
		provenance.FailureCode = failure.Code
		return provenance, failure
	}
	provenance.Status = InvocationSucceeded
	return provenance, nil
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
