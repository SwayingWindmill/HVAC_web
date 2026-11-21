package intelligencemodel

import (
	"errors"
	"math"
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
	// What the evidence measured, against the threshold the rule applied, in UCUM units.
	ObservedValue  float64 `json:"observedValue"`
	ThresholdValue float64 `json:"thresholdValue"`
	ValueUnit      string  `json:"valueUnit"`
}

type RecommendationApproval string

const (
	RecommendationDraft    RecommendationApproval = "DRAFT"
	RecommendationApproved RecommendationApproval = "APPROVED"
	RecommendationRejected RecommendationApproval = "REJECTED"

	// DefaultRecommendationTTL caps the validity of a generated setpoint recommendation so stale
	// advice cannot execute past the next hourly optimization cycle plus preparation headroom.
	DefaultRecommendationTTL = 65 * time.Minute
)

type CurrentStateRevalidation struct {
	SnapshotID  string    `json:"snapshotId"`
	Accepted    bool      `json:"accepted"`
	ReasonCode  string    `json:"reasonCode"`
	ValidatedAt time.Time `json:"validatedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type RecommendationBaseline struct {
	DailyEnergyKWh float64 `json:"dailyEnergyKWh"`
	DailyCost      float64 `json:"dailyCost"`
	SupplyTempC    float64 `json:"supplyTempC"`
	ZoneTempC      float64 `json:"zoneTempC"`
}

type RecommendationObjective struct {
	Kind           string `json:"kind"`
	Horizon        string `json:"horizon,omitempty"`
	HorizonMinutes int    `json:"horizonMinutes,omitempty"`
}

type RecommendationConstraint struct {
	Kind     string   `json:"kind"`
	MinC     *float64 `json:"minC,omitempty"`
	MaxC     *float64 `json:"maxC,omitempty"`
	MaxStepC *float64 `json:"maxStepC,omitempty"`
	Required *bool    `json:"required,omitempty"`
}

type RecommendationCandidate struct {
	SupplyTempC        float64 `json:"supplyTempC"`
	ProjectedZoneTempC float64 `json:"projectedZoneTempC"`
	DailyEnergyKWh     float64 `json:"dailyEnergyKWh"`
	DailyCost          float64 `json:"dailyCost"`
}

type RecommendationExpectedImpact struct {
	EnergySavingKWhPerDay float64 `json:"energySavingKWhPerDay"`
	CostSavingPerDay      float64 `json:"costSavingPerDay"`
}

type RecommendationUncertainty struct {
	EnergyP90KWh    float64  `json:"energyP90KWh"`
	EnergyP90Source string   `json:"energyP90Source,omitempty"`
	ZoneTempP90C    float64  `json:"zoneTempP90C"`
	CVRMSEPower     *float64 `json:"cvRmsePower,omitempty"`
}

type RecommendationRisk struct {
	Level                         string  `json:"level"`
	ComfortMarginC                float64 `json:"comfortMarginC"`
	Extrapolated                  *bool   `json:"extrapolated,omitempty"`
	SavingWithinEnergyUncertainty *bool   `json:"savingWithinEnergyUncertainty,omitempty"`
}

type RecommendationRollbackPlan struct {
	RestoreSupplyTempC *float64 `json:"restoreSupplyTempC,omitempty"`
	Conditions         []string `json:"conditions,omitempty"`
}

type RecommendationVerificationPlan struct {
	WindowMinutes   int      `json:"windowMinutes"`
	RequiredSignals []string `json:"requiredSignals,omitempty"`
	SuccessCriteria []string `json:"successCriteria,omitempty"`
}

type OptimizationAuxiliaryTarget struct {
	LoopKey     string  `json:"loopKey"`
	Capability  string  `json:"capability"`
	TargetValue float64 `json:"targetValue"`
	Unit        string  `json:"unit"`
	Rationale   string  `json:"rationale,omitempty"`
}

type OptimizationRecommendation struct {
	ID                 string                         `json:"id"`
	TenantID           string                         `json:"tenantId"`
	SiteID             string                         `json:"siteId"`
	InputSnapshotID    string                         `json:"inputSnapshotId"`
	DeploymentRevision string                         `json:"deploymentRevisionId"`
	Baseline           RecommendationBaseline         `json:"baseline"`
	Objective          RecommendationObjective        `json:"objective"`
	Constraints        []RecommendationConstraint     `json:"constraints"`
	Candidate          RecommendationCandidate        `json:"candidate"`
	AuxiliaryTargets   []OptimizationAuxiliaryTarget  `json:"auxiliaryTargets,omitempty"`
	ExpectedImpact     RecommendationExpectedImpact   `json:"expectedImpact"`
	Uncertainty        RecommendationUncertainty      `json:"uncertainty"`
	Risk               RecommendationRisk             `json:"risk"`
	RollbackPlan       RecommendationRollbackPlan     `json:"rollbackPlan"`
	VerificationPlan   RecommendationVerificationPlan `json:"verificationPlan"`
	Approval           RecommendationApproval         `json:"approval"`
	Revalidation       *CurrentStateRevalidation      `json:"currentStateRevalidation,omitempty"`
	CommandIntentID    string                         `json:"commandIntentId,omitempty"`
	ValidUntil         *time.Time                     `json:"validUntil,omitempty"`
	CreatedAt          time.Time                      `json:"createdAt"`
}

func (recommendation OptimizationRecommendation) EffectiveValidUntil() time.Time {
	if recommendation.ValidUntil != nil && !recommendation.ValidUntil.IsZero() {
		return recommendation.ValidUntil.UTC()
	}
	if recommendation.CreatedAt.IsZero() {
		return time.Time{}
	}
	return recommendation.CreatedAt.UTC().Add(DefaultRecommendationTTL)
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

func finite(value float64) bool            { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func finitePositive(value float64) bool    { return finite(value) && value > 0 }
func finiteNonNegative(value float64) bool { return finite(value) && value >= 0 }

func (recommendation OptimizationRecommendation) ValidateForApproval() error {
	if strings.TrimSpace(recommendation.ID) == "" || strings.TrimSpace(recommendation.TenantID) == "" || strings.TrimSpace(recommendation.SiteID) == "" || strings.TrimSpace(recommendation.InputSnapshotID) == "" || recommendation.CreatedAt.IsZero() {
		return errors.New("recommendation identity, scope, input snapshot and createdAt are required")
	}
	if !finitePositive(recommendation.Baseline.DailyEnergyKWh) ||
		!finiteNonNegative(recommendation.Baseline.DailyCost) ||
		!finite(recommendation.Baseline.SupplyTempC) ||
		!finite(recommendation.Baseline.ZoneTempC) ||
		strings.TrimSpace(recommendation.Objective.Kind) == "" ||
		len(recommendation.Constraints) == 0 ||
		!finite(recommendation.Candidate.SupplyTempC) ||
		!finite(recommendation.Candidate.ProjectedZoneTempC) ||
		!finiteNonNegative(recommendation.Candidate.DailyEnergyKWh) ||
		!finiteNonNegative(recommendation.Candidate.DailyCost) ||
		!finite(recommendation.ExpectedImpact.EnergySavingKWhPerDay) ||
		!finite(recommendation.ExpectedImpact.CostSavingPerDay) ||
		!finiteNonNegative(recommendation.Uncertainty.EnergyP90KWh) ||
		!finiteNonNegative(recommendation.Uncertainty.ZoneTempP90C) ||
		strings.TrimSpace(recommendation.Risk.Level) == "" ||
		!finite(recommendation.Risk.ComfortMarginC) ||
		(recommendation.RollbackPlan.RestoreSupplyTempC == nil && len(recommendation.RollbackPlan.Conditions) == 0) ||
		recommendation.VerificationPlan.WindowMinutes <= 0 {
		return errors.New("recommendation approval requires baseline, objective, constraints, candidate, impact, uncertainty, risk, rollback and verification plans")
	}
	for _, constraint := range recommendation.Constraints {
		if strings.TrimSpace(constraint.Kind) == "" {
			return errors.New("recommendation constraint kind is required")
		}
	}
	if recommendation.RollbackPlan.RestoreSupplyTempC != nil && !finite(*recommendation.RollbackPlan.RestoreSupplyTempC) {
		return errors.New("recommendation rollback restoreSupplyTempC must be finite")
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
	validUntil := recommendation.EffectiveValidUntil()
	if validUntil.IsZero() || !validUntil.After(now.UTC()) {
		return errors.New("recommendation validity window has expired")
	}
	return nil
}
