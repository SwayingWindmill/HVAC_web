package intelligencemodel

import (
	"errors"
	"strings"
	"time"
)

type UseCase string

const (
	UseCaseForecast            UseCase = "FORECAST"
	UseCaseFDD                 UseCase = "FDD"
	UseCaseOptimization        UseCase = "OPTIMIZATION"
	UseCaseOperationsSynthesis UseCase = "OPERATIONS_SYNTHESIS"
)

type ModelDefinition struct {
	ID               string   `json:"id"`
	TenantID         string   `json:"tenantId"`
	Name             string   `json:"name"`
	Provider         string   `json:"provider"`
	ModelID          string   `json:"modelId"`
	Capabilities     []string `json:"capabilities"`
	CredentialRef    string   `json:"credentialRef,omitempty"`
	EndpointPolicyID string   `json:"endpointPolicyId,omitempty"`
	Status           string   `json:"status"`
	Revision         uint64   `json:"revision"`
}

type DataEgressPolicy struct {
	ID                 string   `json:"id"`
	TenantID           string   `json:"tenantId"`
	Name               string   `json:"name"`
	AllowedDataClasses []string `json:"allowedDataClasses"`
	AllowedRegions     []string `json:"allowedRegions"`
	MaxInputBytes      int64    `json:"maxInputBytes"`
	Enabled            bool     `json:"enabled"`
	Revision           uint64   `json:"revision"`
}

type DeploymentRevision struct {
	ID                  string    `json:"id"`
	TenantID            string    `json:"tenantId"`
	ModelDefinitionID   string    `json:"modelDefinitionId"`
	UseCase             UseCase   `json:"useCase"`
	Revision            uint64    `json:"revision"`
	OutputSchemaVersion string    `json:"outputSchemaVersion"`
	DataEgressPolicyID  string    `json:"dataEgressPolicyId,omitempty"`
	PromptPolicyVersion string    `json:"promptPolicyVersion,omitempty"`
	Enabled             bool      `json:"enabled"`
	CreatedAt           time.Time `json:"createdAt"`
}

type InvocationStatus string

const (
	InvocationSucceeded InvocationStatus = "SUCCEEDED"
	InvocationFallback  InvocationStatus = "FALLBACK"
	InvocationFailed    InvocationStatus = "FAILED"
)

type InvocationProvenance struct {
	ID                   string           `json:"id"`
	TenantID             string           `json:"tenantId"`
	SiteID               string           `json:"siteId,omitempty"`
	UseCase              UseCase          `json:"useCase"`
	DeploymentRevisionID string           `json:"deploymentRevisionId"`
	InputSnapshotID      string           `json:"inputSnapshotId,omitempty"`
	InputDigest          string           `json:"inputDigest"`
	EvidenceIDs          []string         `json:"evidenceIds"`
	OutputSchemaVersion  string           `json:"outputSchemaVersion"`
	Status               InvocationStatus `json:"status"`
	ProviderRequestID    string           `json:"providerRequestId,omitempty"`
	TokenUsage           int64            `json:"tokenUsage,omitempty"`
	CostMicros           int64            `json:"costMicros,omitempty"`
	LatencyMillis        int64            `json:"latencyMillis"`
	FallbackReason       string           `json:"fallbackReason,omitempty"`
	FailureCode          string           `json:"failureCode,omitempty"`
	CreatedAt            time.Time        `json:"createdAt"`
}

type FDDFinding struct {
	ID                string    `json:"id"`
	TenantID          string    `json:"tenantId"`
	SiteID            string    `json:"siteId"`
	AssetID           string    `json:"assetId"`
	FindingType       string    `json:"findingType"`
	EvaluationFrom    time.Time `json:"evaluationFrom"`
	EvaluationTo      time.Time `json:"evaluationTo"`
	EvidenceIDs       []string  `json:"evidenceIds"`
	ModelDeploymentID string    `json:"modelDeploymentRevisionId,omitempty"`
	RuleRevisionID    string    `json:"ruleRevisionId,omitempty"`
	Confidence        float64   `json:"confidence"`
	QualityBlocker    string    `json:"qualityBlocker,omitempty"`
	AlarmID           string    `json:"alarmId,omitempty"`
	WorkOrderID       string    `json:"workOrderId,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

type RecommendationApproval string

const (
	RecommendationDraft    RecommendationApproval = "DRAFT"
	RecommendationApproved RecommendationApproval = "APPROVED"
	RecommendationRejected RecommendationApproval = "REJECTED"
)

type CurrentStateRevalidation struct {
	SnapshotID  string    `json:"snapshotId"`
	Accepted    bool      `json:"accepted"`
	ReasonCode  string    `json:"reasonCode"`
	ValidatedAt time.Time `json:"validatedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type OptimizationRecommendation struct {
	ID                 string                    `json:"id"`
	TenantID           string                    `json:"tenantId"`
	SiteID             string                    `json:"siteId"`
	InputSnapshotID    string                    `json:"inputSnapshotId"`
	DeploymentRevision string                    `json:"deploymentRevisionId"`
	Baseline           map[string]any            `json:"baseline"`
	Objective          map[string]any            `json:"objective"`
	Constraints        []map[string]any          `json:"constraints"`
	Candidate          map[string]any            `json:"candidate"`
	ExpectedImpact     map[string]any            `json:"expectedImpact"`
	Uncertainty        map[string]any            `json:"uncertainty"`
	Risk               map[string]any            `json:"risk"`
	RollbackPlan       map[string]any            `json:"rollbackPlan"`
	VerificationPlan   map[string]any            `json:"verificationPlan"`
	Approval           RecommendationApproval    `json:"approval"`
	Revalidation       *CurrentStateRevalidation `json:"currentStateRevalidation,omitempty"`
	CommandIntentID    string                    `json:"commandIntentId,omitempty"`
	CreatedAt          time.Time                 `json:"createdAt"`
}

func (definition ModelDefinition) Validate() error {
	if strings.TrimSpace(definition.ID) == "" || strings.TrimSpace(definition.TenantID) == "" || strings.TrimSpace(definition.Name) == "" || strings.TrimSpace(definition.Provider) == "" || strings.TrimSpace(definition.ModelID) == "" {
		return errors.New("model definition identity, name, provider and modelId are required")
	}
	if len(definition.Capabilities) == 0 || definition.Revision == 0 {
		return errors.New("model definition capabilities and revision are required")
	}
	if definition.Status != "ACTIVE" && definition.Status != "RETIRED" {
		return errors.New("model definition status must be ACTIVE or RETIRED")
	}
	if definition.Provider != "LOCAL" && strings.TrimSpace(definition.CredentialRef) == "" {
		return errors.New("external model provider requires a credentialRef")
	}
	return nil
}

func (deployment DeploymentRevision) Validate() error {
	if strings.TrimSpace(deployment.ID) == "" || strings.TrimSpace(deployment.TenantID) == "" || strings.TrimSpace(deployment.ModelDefinitionID) == "" || deployment.Revision == 0 || strings.TrimSpace(deployment.OutputSchemaVersion) == "" || deployment.CreatedAt.IsZero() {
		return errors.New("deployment revision identity, model, revision, schema and createdAt are required")
	}
	switch deployment.UseCase {
	case UseCaseForecast, UseCaseFDD, UseCaseOptimization, UseCaseOperationsSynthesis:
		return nil
	default:
		return errors.New("deployment revision useCase is invalid")
	}
}

func (recommendation OptimizationRecommendation) ValidateForApproval() error {
	if strings.TrimSpace(recommendation.ID) == "" || strings.TrimSpace(recommendation.TenantID) == "" || strings.TrimSpace(recommendation.SiteID) == "" || strings.TrimSpace(recommendation.InputSnapshotID) == "" || recommendation.CreatedAt.IsZero() {
		return errors.New("recommendation identity, scope, input snapshot and createdAt are required")
	}
	if len(recommendation.Baseline) == 0 || len(recommendation.Objective) == 0 || len(recommendation.Constraints) == 0 || len(recommendation.Candidate) == 0 || len(recommendation.ExpectedImpact) == 0 || len(recommendation.Uncertainty) == 0 || len(recommendation.Risk) == 0 || len(recommendation.RollbackPlan) == 0 || len(recommendation.VerificationPlan) == 0 {
		return errors.New("recommendation approval requires baseline, objective, constraints, candidate, impact, uncertainty, risk, rollback and verification plans")
	}
	return nil
}

func (recommendation OptimizationRecommendation) CanCreateCommand(now time.Time) error {
	if recommendation.Approval != RecommendationApproved {
		return errors.New("recommendation must be approved before command creation")
	}
	if recommendation.Revalidation == nil || !recommendation.Revalidation.Accepted || strings.TrimSpace(recommendation.Revalidation.SnapshotID) == "" {
		return errors.New("independent current-state revalidation is required before command creation")
	}
	if !recommendation.Revalidation.ValidatedAt.After(recommendation.CreatedAt) || !recommendation.Revalidation.ExpiresAt.After(now.UTC()) {
		return errors.New("current-state revalidation is stale")
	}
	return nil
}
