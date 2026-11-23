package optimization

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	EdgeArtifactMediaType   = "application/vnd.hvac.edge-model-bundle+json"
	EdgeApprovalPolicyRev   = "edge-mlops-v1"
	DefaultEdgeModelServing = "site-edge-pool"
	DefaultMaxShadowDev     = 1.5
)

var (
	ErrEdgeModelNotFound         = errors.New("edge model record not found")
	ErrEdgeModelInvalid          = errors.New("edge model request invalid")
	ErrEdgeModelConflict         = errors.New("edge model version already exists")
	ErrEdgeModelApprovalRequired = errors.New("edge model approval required before deployment")
	ErrEdgeModelRolloutConflict  = errors.New("edge model deployment is not active for rollback")
)

type edgeModelSigner struct {
	keyID      string
	privateKey ed25519.PrivateKey
}

// EdgeModelArtifactMetadata is stored typed inside core_registry.mlops_artifacts.metadata jsonb.
type EdgeModelArtifactMetadata struct {
	ModelKey           string                           `json:"modelKey"`
	Version            string                           `json:"version"`
	Name               string                           `json:"name"`
	Description        string                           `json:"description,omitempty"`
	MaxShadowDeviation float64                          `json:"maxShadowDeviation"`
	Hyperparams        edgecontrol.EdgeModelHyperparams `json:"hyperparams"`
	KeyID              string                           `json:"keyId,omitempty"`
	Signature          string                           `json:"signature,omitempty"`
	GatewayID          string                           `json:"gatewayId,omitempty"`
	CreatedBy          string                           `json:"createdBy,omitempty"`
}

// EdgeModelEvaluationMetrics is stored typed inside core_registry.mlops_evaluations.metrics jsonb.
type EdgeModelEvaluationMetrics struct {
	ModelKey         string   `json:"modelKey"`
	Version          string   `json:"version"`
	SampleCount      int64    `json:"sampleCount"`
	MeanAbsDeviation float64  `json:"meanAbsDeviation"`
	MaxAbsDeviation  float64  `json:"maxAbsDeviation"`
	ThresholdValue   float64  `json:"thresholdValue"`
	BaselineCOP      *float64 `json:"baselineCop,omitempty"`
	CandidateCOP     *float64 `json:"candidateCop,omitempty"`
	EnergySavingPct  *float64 `json:"energySavingPct,omitempty"`
	Notes            string   `json:"notes,omitempty"`
}

// EdgeModelDriftEvidence is stored typed inside core_registry.mlops_drift_observations.evidence jsonb.
type EdgeModelDriftEvidence struct {
	ModelKey         string  `json:"modelKey"`
	Version          string  `json:"version"`
	EvaluationID     string  `json:"evaluationId,omitempty"`
	SampleCount      int64   `json:"sampleCount"`
	MeanAbsDeviation float64 `json:"meanAbsDeviation"`
	Notes            string  `json:"notes,omitempty"`
}

type EdgeModelArtifactRecord struct {
	ID             string
	TenantID       string
	SiteID         string
	Domain         edgecontrol.EdgeModelDomain
	VersionID      string
	ObjectURI      string
	ChecksumSHA256 string
	MediaType      string
	SizeBytes      int64
	Metadata       EdgeModelArtifactMetadata
	CreatedAt      time.Time
}

type EdgeModelEvaluationRecord struct {
	ID                string
	TenantID          string
	SiteID            string
	Domain            edgecontrol.EdgeModelDomain
	VersionID         string
	EvaluationType    string
	DatasetArtifactID *string
	Metrics           EdgeModelEvaluationMetrics
	Outcome           string
	Evaluator         string
	EvaluatedAt       time.Time
	CreatedAt         time.Time
}

type EdgeModelApprovalRecord struct {
	ID             string
	TenantID       string
	SiteID         string
	Domain         edgecontrol.EdgeModelDomain
	VersionID      string
	EvaluationID   *string
	Decision       string
	ActorType      string
	ActorID        string
	Reason         string
	PolicyRevision string
	DecidedAt      time.Time
	CreatedAt      time.Time
}

type EdgeModelDeploymentRecord struct {
	ID            string
	TenantID      string
	SiteID        string
	Domain        edgecontrol.EdgeModelDomain
	VersionID     string
	ApprovalID    string
	Environment   string
	Status        string
	ServingRef    string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	Revision      int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type EdgeModelDriftRecord struct {
	ID            string
	TenantID      string
	SiteID        string
	DeploymentID  string
	MetricCode    string
	ObservedValue float64
	Threshold     float64
	DriftStatus   string
	Evidence      EdgeModelDriftEvidence
	ObservedAt    time.Time
	CreatedAt     time.Time
}

type EdgeModelRollbackRecord struct {
	ID            string
	TenantID      string
	SiteID        string
	DeploymentID  string
	FromVersionID string
	ToVersionID   string
	Reason        string
	ActorType     string
	ActorID       string
	Status        string
	RequestedAt   time.Time
	AppliedAt     *time.Time
	CreatedAt     time.Time
}

type EdgeModelArtifactView struct {
	ID                   string                           `json:"id"`
	VersionID            string                           `json:"versionId"`
	Domain               edgecontrol.EdgeModelDomain      `json:"domain"`
	ModelKey             string                           `json:"modelKey"`
	Version              string                           `json:"version"`
	Name                 string                           `json:"name"`
	Description          string                           `json:"description,omitempty"`
	ObjectURI            string                           `json:"objectUri"`
	ChecksumSHA256       string                           `json:"checksumSha256"`
	MaxShadowDeviation   float64                          `json:"maxShadowDeviation"`
	Hyperparams          edgecontrol.EdgeModelHyperparams `json:"hyperparams"`
	GatewayID            string                           `json:"gatewayId,omitempty"`
	Signed               bool                             `json:"signed"`
	ApprovalStatus       string                           `json:"approvalStatus"`
	LatestApprovalReason string                           `json:"latestApprovalReason,omitempty"`
	LatestEvaluation     string                           `json:"latestEvaluation,omitempty"`
	CreatedAt            time.Time                        `json:"createdAt"`
}

type EdgeModelApprovalView struct {
	ID             string                      `json:"id"`
	ArtifactID     string                      `json:"artifactId,omitempty"`
	VersionID      string                      `json:"versionId"`
	Domain         edgecontrol.EdgeModelDomain `json:"domain"`
	ModelKey       string                      `json:"modelKey"`
	Version        string                      `json:"version"`
	EvaluationID   *string                     `json:"evaluationId,omitempty"`
	Decision       string                      `json:"decision"`
	ActorType      string                      `json:"actorType"`
	ActorID        string                      `json:"actorId"`
	Reason         string                      `json:"reason"`
	PolicyRevision string                      `json:"policyRevision"`
	DecidedAt      time.Time                   `json:"decidedAt"`
}

type EdgeModelDeploymentView struct {
	ID            string                           `json:"id"`
	ArtifactID    string                           `json:"artifactId,omitempty"`
	VersionID     string                           `json:"versionId"`
	Domain        edgecontrol.EdgeModelDomain      `json:"domain"`
	ModelKey      string                           `json:"modelKey"`
	Version       string                           `json:"version"`
	ApprovalID    string                           `json:"approvalId"`
	Environment   string                           `json:"environment"`
	Status        string                           `json:"status"`
	ServingRef    string                           `json:"servingRef"`
	GatewayID     string                           `json:"gatewayId,omitempty"`
	Revision      int64                            `json:"revision"`
	EffectiveFrom time.Time                        `json:"effectiveFrom"`
	EffectiveTo   *time.Time                       `json:"effectiveTo,omitempty"`
	Hyperparams   edgecontrol.EdgeModelHyperparams `json:"hyperparams"`
}

type EdgeModelEvaluationView struct {
	ID             string                      `json:"id"`
	ArtifactID     string                      `json:"artifactId,omitempty"`
	VersionID      string                      `json:"versionId"`
	Domain         edgecontrol.EdgeModelDomain `json:"domain"`
	ModelKey       string                      `json:"modelKey"`
	Version        string                      `json:"version"`
	EvaluationType string                      `json:"evaluationType"`
	Outcome        string                      `json:"outcome"`
	Evaluator      string                      `json:"evaluator"`
	Metrics        EdgeModelEvaluationMetrics  `json:"metrics"`
	EvaluatedAt    time.Time                   `json:"evaluatedAt"`
}

type EdgeModelDriftView struct {
	ID            string                 `json:"id"`
	DeploymentID  string                 `json:"deploymentId"`
	MetricCode    string                 `json:"metricCode"`
	ObservedValue float64                `json:"observedValue"`
	Threshold     float64                `json:"threshold"`
	DriftStatus   string                 `json:"driftStatus"`
	Evidence      EdgeModelDriftEvidence `json:"evidence"`
	ObservedAt    time.Time              `json:"observedAt"`
}

type EdgeModelRollbackView struct {
	ID            string     `json:"id"`
	DeploymentID  string     `json:"deploymentId"`
	FromVersionID string     `json:"fromVersionId"`
	ToVersionID   string     `json:"toVersionId"`
	FromVersion   string     `json:"fromVersion,omitempty"`
	ToVersion     string     `json:"toVersion,omitempty"`
	Reason        string     `json:"reason"`
	ActorType     string     `json:"actorType"`
	ActorID       string     `json:"actorId"`
	Status        string     `json:"status"`
	RequestedAt   time.Time  `json:"requestedAt"`
	AppliedAt     *time.Time `json:"appliedAt,omitempty"`
}

type EdgeModelWorkspace struct {
	TenantID          string                        `json:"tenantId"`
	SiteID            string                        `json:"siteId"`
	GeneratedAt       time.Time                     `json:"generatedAt"`
	Artifacts         []EdgeModelArtifactView       `json:"artifacts"`
	Approvals         []EdgeModelApprovalView       `json:"approvals"`
	Deployments       []EdgeModelDeploymentView     `json:"deployments"`
	Evaluations       []EdgeModelEvaluationView     `json:"evaluations"`
	DriftObservations []EdgeModelDriftView          `json:"driftObservations"`
	Rollbacks         []EdgeModelRollbackView       `json:"rollbacks"`
	DesiredBundles    []edgecontrol.EdgeModelBundle `json:"desiredBundles"`
}

type CreateEdgeModelArtifactRequest struct {
	Domain             edgecontrol.EdgeModelDomain      `json:"domain"`
	ModelKey           string                           `json:"modelKey"`
	Version            string                           `json:"version"`
	Name               string                           `json:"name,omitempty"`
	Description        string                           `json:"description,omitempty"`
	MaxShadowDeviation *float64                         `json:"maxShadowDeviation,omitempty"`
	Hyperparams        edgecontrol.EdgeModelHyperparams `json:"hyperparams"`
	ArtifactSHA256     string                           `json:"artifactSha256,omitempty"`
	GatewayID          string                           `json:"gatewayId,omitempty"`
}

type ApproveEdgeModelRequest struct {
	Decision       string  `json:"decision"`
	Reason         string  `json:"reason"`
	EvaluationID   *string `json:"evaluationId,omitempty"`
	PolicyRevision string  `json:"policyRevision,omitempty"`
}

type DeployEdgeModelRequest struct {
	ArtifactID  string `json:"artifactId"`
	Environment string `json:"environment"`
	GatewayID   string `json:"gatewayId,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type EdgeModelDeploymentResult struct {
	Deployment     EdgeModelDeploymentView       `json:"deployment"`
	GatewayID      string                        `json:"gatewayId,omitempty"`
	DesiredBundles []edgecontrol.EdgeModelBundle `json:"desiredBundles"`
}

type RollbackEdgeModelRequest struct {
	Reason           string  `json:"reason"`
	TargetArtifactID *string `json:"targetArtifactId,omitempty"`
	GatewayID        string  `json:"gatewayId,omitempty"`
}

type EdgeModelRollbackResult struct {
	Rollback           EdgeModelRollbackView         `json:"rollback"`
	RestoredDeployment *EdgeModelDeploymentView      `json:"restoredDeployment,omitempty"`
	GatewayID          string                        `json:"gatewayId,omitempty"`
	DesiredBundles     []edgecontrol.EdgeModelBundle `json:"desiredBundles"`
}

type RecordEdgeModelEvaluationRequest struct {
	ArtifactID       string   `json:"artifactId"`
	DeploymentID     *string  `json:"deploymentId,omitempty"`
	EvaluationType   string   `json:"evaluationType"`
	SampleCount      int64    `json:"sampleCount"`
	MeanAbsDeviation float64  `json:"meanAbsDeviation"`
	MaxAbsDeviation  float64  `json:"maxAbsDeviation"`
	BaselineCOP      *float64 `json:"baselineCop,omitempty"`
	CandidateCOP     *float64 `json:"candidateCop,omitempty"`
	EnergySavingPct  *float64 `json:"energySavingPct,omitempty"`
	Outcome          string   `json:"outcome,omitempty"`
	Notes            string   `json:"notes,omitempty"`
}

type EdgeModelEvaluationResult struct {
	Evaluation       EdgeModelEvaluationView `json:"evaluation"`
	DriftObservation *EdgeModelDriftView     `json:"driftObservation,omitempty"`
	Breached         bool                    `json:"breached"`
}

type EdgeModelSiteSnapshot struct {
	Artifacts         []EdgeModelArtifactRecord
	Approvals         []EdgeModelApprovalRecord
	Deployments       []EdgeModelDeploymentRecord
	Evaluations       []EdgeModelEvaluationRecord
	DriftObservations []EdgeModelDriftRecord
	Rollbacks         []EdgeModelRollbackRecord
}

type EdgeModelStore interface {
	LoadEdgeModelSiteSnapshot(ctx context.Context, tenantID, siteID string) (EdgeModelSiteSnapshot, error)
	InsertEdgeModelArtifact(ctx context.Context, record EdgeModelArtifactRecord) (EdgeModelArtifactRecord, error)
	InsertEdgeModelApproval(ctx context.Context, record EdgeModelApprovalRecord) (EdgeModelApprovalRecord, error)
	DeployEdgeModelVersion(ctx context.Context, tenantID, siteID string, artifact EdgeModelArtifactRecord, approvalID, environment, servingRef string, at time.Time) (EdgeModelDeploymentRecord, []EdgeModelDeploymentRecord, error)
	RollbackEdgeModelDeployment(ctx context.Context, tenantID, siteID, deploymentID string, targetArtifact *EdgeModelArtifactRecord, targetApprovalID, reason, actorType, actorID, servingRef string, at time.Time) (EdgeModelRollbackRecord, *EdgeModelDeploymentRecord, []EdgeModelDeploymentRecord, error)
	InsertEdgeModelEvaluation(ctx context.Context, eval EdgeModelEvaluationRecord, drift *EdgeModelDriftRecord) error
}

func (service *Service) WithEdgeModelStore(store EdgeModelStore) *Service {
	service.edgeModelStore = store
	return service
}

func (service *Service) WithEdgeModelSigningKey(keyID string, privateKey ed25519.PrivateKey) *Service {
	trimmed := strings.TrimSpace(keyID)
	if trimmed == "" || len(privateKey) != ed25519.PrivateKeySize {
		service.edgeModelSigner = nil
		return service
	}
	cpy := append(ed25519.PrivateKey(nil), privateKey...)
	service.edgeModelSigner = &edgeModelSigner{keyID: trimmed, privateKey: cpy}
	return service
}

func (service *Service) effectiveEdgeModelStore() EdgeModelStore {
	if service == nil {
		return nil
	}
	if service.edgeModelStore != nil {
		return service.edgeModelStore
	}
	if store, ok := service.publication.(EdgeModelStore); ok {
		return store
	}
	return nil
}

func (service *Service) ensureEdgeModelSiteVisible(ctx context.Context, tenantID, siteID string) (string, string, error) {
	if service == nil {
		return "", "", errors.New("optimization service is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return "", "", fmt.Errorf("%w: invalid tenant or site id", ErrEdgeModelInvalid)
	}
	if checker := service.effectiveSiteVisibilityChecker(); checker != nil {
		visible, err := checker.CheckSiteVisible(ctx, tenant, site)
		if err != nil {
			return "", "", err
		}
		if !visible {
			return "", "", ErrOptimizationNotFound
		}
	}
	return tenant, site, nil
}

func resolveActorIdentity(subject string) (string, string) {
	trimmed := strings.TrimSpace(subject)
	if trimmed == "" {
		return "WORKLOAD", "workload:optimization"
	}
	if len(trimmed) > 256 {
		trimmed = trimmed[:256]
	}
	return "USER", trimmed
}

func parseServingGatewayID(servingRef string) string {
	trimmed := strings.TrimSpace(servingRef)
	if gw, ok := strings.CutPrefix(trimmed, "edge-gateway://"); ok && uuidPattern.MatchString(gw) {
		return gw
	}
	return ""
}

func formatEdgeServingRef(gatewayID string) string {
	gw := strings.TrimSpace(gatewayID)
	if gw == "" {
		return DefaultEdgeModelServing
	}
	return "edge-gateway://" + gw
}

func (service *Service) buildDesiredEdgeBundles(artifacts []EdgeModelArtifactRecord, deployments []EdgeModelDeploymentRecord) ([]edgecontrol.EdgeModelBundle, error) {
	byVersion := make(map[string]EdgeModelArtifactRecord, len(artifacts))
	for _, art := range artifacts {
		byVersion[art.VersionID] = art
	}
	bundles := make([]edgecontrol.EdgeModelBundle, 0)
	for _, dep := range deployments {
		if dep.Status != "ACTIVE" {
			continue
		}
		art, ok := byVersion[dep.VersionID]
		if !ok {
			continue
		}
		rolloutMode := edgecontrol.EdgeModelRolloutShadow
		if dep.Environment == "PRODUCTION" {
			rolloutMode = edgecontrol.EdgeModelRolloutActive
		}
		b := edgecontrol.EdgeModelBundle{
			BundleID:           art.VersionID,
			Domain:             art.Domain,
			ModelKey:           art.Metadata.ModelKey,
			Version:            art.Metadata.Version,
			RolloutMode:        rolloutMode,
			ArtifactSHA256:     art.ChecksumSHA256,
			KeyID:              art.Metadata.KeyID,
			Signature:          art.Metadata.Signature,
			MaxShadowDeviation: art.Metadata.MaxShadowDeviation,
			Hyperparams:        art.Metadata.Hyperparams.Clone(),
			UpdatedAt:          dep.UpdatedAt.UTC(),
		}
		if service.edgeModelSigner != nil {
			if err := edgecontrol.SignEdgeModelBundleEd25519(&b, service.edgeModelSigner.keyID, service.edgeModelSigner.privateKey); err != nil {
				return nil, err
			}
		}
		bundles = append(bundles, b)
	}
	sort.Slice(bundles, func(i, j int) bool {
		if bundles[i].ModelKey != bundles[j].ModelKey {
			return bundles[i].ModelKey < bundles[j].ModelKey
		}
		return bundles[i].RolloutMode < bundles[j].RolloutMode
	})
	return bundles, nil
}

func (service *Service) GetEdgeModelWorkspace(ctx context.Context, tenantID, siteID string) (EdgeModelWorkspace, error) {
	tenant, site, err := service.ensureEdgeModelSiteVisible(ctx, tenantID, siteID)
	if err != nil {
		return EdgeModelWorkspace{}, err
	}
	store := service.effectiveEdgeModelStore()
	if store == nil {
		return EdgeModelWorkspace{}, errors.New("edge model store is unavailable")
	}
	snap, err := store.LoadEdgeModelSiteSnapshot(ctx, tenant, site)
	if err != nil {
		return EdgeModelWorkspace{}, err
	}

	byVersion := make(map[string]EdgeModelArtifactRecord, len(snap.Artifacts))
	for _, art := range snap.Artifacts {
		byVersion[art.VersionID] = art
	}
	latestApprovalByVer := make(map[string]EdgeModelApprovalRecord, len(snap.Approvals))
	for _, ap := range snap.Approvals {
		if _, exists := latestApprovalByVer[ap.VersionID]; !exists {
			latestApprovalByVer[ap.VersionID] = ap
		}
	}
	latestEvalByVer := make(map[string]EdgeModelEvaluationRecord, len(snap.Evaluations))
	for _, ev := range snap.Evaluations {
		if _, exists := latestEvalByVer[ev.VersionID]; !exists {
			latestEvalByVer[ev.VersionID] = ev
		}
	}

	artifacts := make([]EdgeModelArtifactView, 0, len(snap.Artifacts))
	for _, art := range snap.Artifacts {
		approvalStatus := "PENDING"
		approvalReason := ""
		if ap, ok := latestApprovalByVer[art.VersionID]; ok {
			approvalStatus = ap.Decision
			approvalReason = ap.Reason
		}
		evalOutcome := ""
		if ev, ok := latestEvalByVer[art.VersionID]; ok {
			evalOutcome = ev.Outcome
		}
		artifacts = append(artifacts, EdgeModelArtifactView{
			ID:                   art.ID,
			VersionID:            art.VersionID,
			Domain:               art.Domain,
			ModelKey:             art.Metadata.ModelKey,
			Version:              art.Metadata.Version,
			Name:                 art.Metadata.Name,
			Description:          art.Metadata.Description,
			ObjectURI:            art.ObjectURI,
			ChecksumSHA256:       art.ChecksumSHA256,
			MaxShadowDeviation:   art.Metadata.MaxShadowDeviation,
			Hyperparams:          art.Metadata.Hyperparams.Clone(),
			GatewayID:            art.Metadata.GatewayID,
			Signed:               art.Metadata.Signature != "" || service.edgeModelSigner != nil,
			ApprovalStatus:       approvalStatus,
			LatestApprovalReason: approvalReason,
			LatestEvaluation:     evalOutcome,
			CreatedAt:            art.CreatedAt.UTC(),
		})
	}

	approvals := make([]EdgeModelApprovalView, 0, len(snap.Approvals))
	for _, ap := range snap.Approvals {
		art := byVersion[ap.VersionID]
		approvals = append(approvals, EdgeModelApprovalView{
			ID:             ap.ID,
			ArtifactID:     art.ID,
			VersionID:      ap.VersionID,
			Domain:         ap.Domain,
			ModelKey:       art.Metadata.ModelKey,
			Version:        art.Metadata.Version,
			EvaluationID:   ap.EvaluationID,
			Decision:       ap.Decision,
			ActorType:      ap.ActorType,
			ActorID:        ap.ActorID,
			Reason:         ap.Reason,
			PolicyRevision: ap.PolicyRevision,
			DecidedAt:      ap.DecidedAt.UTC(),
		})
	}

	deployments := make([]EdgeModelDeploymentView, 0, len(snap.Deployments))
	for _, dep := range snap.Deployments {
		art := byVersion[dep.VersionID]
		deployments = append(deployments, toEdgeModelDeploymentView(dep, art))
	}

	evaluations := make([]EdgeModelEvaluationView, 0, len(snap.Evaluations))
	for _, ev := range snap.Evaluations {
		art := byVersion[ev.VersionID]
		evaluations = append(evaluations, EdgeModelEvaluationView{
			ID:             ev.ID,
			ArtifactID:     art.ID,
			VersionID:      ev.VersionID,
			Domain:         ev.Domain,
			ModelKey:       art.Metadata.ModelKey,
			Version:        art.Metadata.Version,
			EvaluationType: ev.EvaluationType,
			Outcome:        ev.Outcome,
			Evaluator:      ev.Evaluator,
			Metrics:        ev.Metrics,
			EvaluatedAt:    ev.EvaluatedAt.UTC(),
		})
	}

	driftObservations := make([]EdgeModelDriftView, 0, len(snap.DriftObservations))
	for _, dr := range snap.DriftObservations {
		driftObservations = append(driftObservations, EdgeModelDriftView{
			ID:            dr.ID,
			DeploymentID:  dr.DeploymentID,
			MetricCode:    dr.MetricCode,
			ObservedValue: dr.ObservedValue,
			Threshold:     dr.Threshold,
			DriftStatus:   dr.DriftStatus,
			Evidence:      dr.Evidence,
			ObservedAt:    dr.ObservedAt.UTC(),
		})
	}

	rollbacks := make([]EdgeModelRollbackView, 0, len(snap.Rollbacks))
	for _, rb := range snap.Rollbacks {
		rollbacks = append(rollbacks, EdgeModelRollbackView{
			ID:            rb.ID,
			DeploymentID:  rb.DeploymentID,
			FromVersionID: rb.FromVersionID,
			ToVersionID:   rb.ToVersionID,
			FromVersion:   byVersion[rb.FromVersionID].Metadata.Version,
			ToVersion:     byVersion[rb.ToVersionID].Metadata.Version,
			Reason:        rb.Reason,
			ActorType:     rb.ActorType,
			ActorID:       rb.ActorID,
			Status:        rb.Status,
			RequestedAt:   rb.RequestedAt.UTC(),
			AppliedAt:     rb.AppliedAt,
		})
	}

	desiredBundles, err := service.buildDesiredEdgeBundles(snap.Artifacts, snap.Deployments)
	if err != nil {
		return EdgeModelWorkspace{}, err
	}
	return EdgeModelWorkspace{
		TenantID:          tenant,
		SiteID:            site,
		GeneratedAt:       service.clock().UTC(),
		Artifacts:         artifacts,
		Approvals:         approvals,
		Deployments:       deployments,
		Evaluations:       evaluations,
		DriftObservations: driftObservations,
		Rollbacks:         rollbacks,
		DesiredBundles:    desiredBundles,
	}, nil
}

func toEdgeModelDeploymentView(dep EdgeModelDeploymentRecord, art EdgeModelArtifactRecord) EdgeModelDeploymentView {
	return EdgeModelDeploymentView{
		ID:            dep.ID,
		ArtifactID:    art.ID,
		VersionID:     dep.VersionID,
		Domain:        dep.Domain,
		ModelKey:      art.Metadata.ModelKey,
		Version:       art.Metadata.Version,
		ApprovalID:    dep.ApprovalID,
		Environment:   dep.Environment,
		Status:        dep.Status,
		ServingRef:    dep.ServingRef,
		GatewayID:     parseServingGatewayID(dep.ServingRef),
		Revision:      dep.Revision,
		EffectiveFrom: dep.EffectiveFrom.UTC(),
		EffectiveTo:   dep.EffectiveTo,
		Hyperparams:   art.Metadata.Hyperparams.Clone(),
	}
}

func (service *Service) CreateEdgeModelArtifact(ctx context.Context, tenantID, siteID, actor string, input CreateEdgeModelArtifactRequest) (EdgeModelArtifactView, error) {
	tenant, site, err := service.ensureEdgeModelSiteVisible(ctx, tenantID, siteID)
	if err != nil {
		return EdgeModelArtifactView{}, err
	}
	store := service.effectiveEdgeModelStore()
	if store == nil {
		return EdgeModelArtifactView{}, errors.New("edge model store is unavailable")
	}
	if !edgecontrol.ValidEdgeModelDomain(input.Domain) {
		return EdgeModelArtifactView{}, fmt.Errorf("%w: unsupported domain %q", edgecontrol.ErrEdgeModelValidation, input.Domain)
	}
	modelKey := strings.TrimSpace(input.ModelKey)
	version := strings.TrimSpace(input.Version)
	gatewayID := strings.TrimSpace(input.GatewayID)
	if gatewayID != "" && !uuidPattern.MatchString(gatewayID) {
		return EdgeModelArtifactView{}, fmt.Errorf("%w: invalid gatewayId", ErrEdgeModelInvalid)
	}
	maxDev := DefaultMaxShadowDev
	if input.MaxShadowDeviation != nil {
		maxDev = *input.MaxShadowDeviation
	}
	checksum, err := edgecontrol.ComputeEdgeModelHyperparamsSHA256(input.Hyperparams)
	if err != nil {
		return EdgeModelArtifactView{}, err
	}
	if supplied := strings.ToLower(strings.TrimSpace(input.ArtifactSHA256)); supplied != "" && supplied != checksum {
		return EdgeModelArtifactView{}, fmt.Errorf("%w: artifactSha256 mismatch (got %q want %q)", edgecontrol.ErrEdgeModelValidation, supplied, checksum)
	}
	now := service.clock().UTC()
	versionID := deterministicV7("edge_model_version", tenant+"|"+site+"|"+string(input.Domain)+"|"+modelKey+"|"+version, now)
	bundleCheck := edgecontrol.EdgeModelBundle{
		BundleID:           versionID,
		Domain:             input.Domain,
		ModelKey:           modelKey,
		Version:            version,
		RolloutMode:        edgecontrol.EdgeModelRolloutShadow,
		ArtifactSHA256:     checksum,
		MaxShadowDeviation: maxDev,
		Hyperparams:        input.Hyperparams.Clone(),
	}
	if err := edgecontrol.ValidateEdgeModelBundleEnvelope(bundleCheck); err != nil {
		return EdgeModelArtifactView{}, err
	}

	snap, err := store.LoadEdgeModelSiteSnapshot(ctx, tenant, site)
	if err != nil {
		return EdgeModelArtifactView{}, err
	}
	for _, existing := range snap.Artifacts {
		if existing.Domain == input.Domain && strings.EqualFold(existing.Metadata.ModelKey, modelKey) && strings.EqualFold(existing.Metadata.Version, version) {
			return EdgeModelArtifactView{}, ErrEdgeModelConflict
		}
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = modelKey + "@" + version
	}
	if len([]rune(name)) > 128 {
		return EdgeModelArtifactView{}, fmt.Errorf("%w: name exceeds 128 characters", ErrEdgeModelInvalid)
	}
	desc := strings.TrimSpace(input.Description)
	if len([]rune(desc)) > 512 {
		return EdgeModelArtifactView{}, fmt.Errorf("%w: description exceeds 512 characters", ErrEdgeModelInvalid)
	}

	meta := EdgeModelArtifactMetadata{
		ModelKey:           modelKey,
		Version:            version,
		Name:               name,
		Description:        desc,
		MaxShadowDeviation: maxDev,
		Hyperparams:        input.Hyperparams.Clone(),
		GatewayID:          gatewayID,
		CreatedBy:          strings.TrimSpace(actor),
	}
	rawMeta, err := json.Marshal(meta)
	if err != nil {
		return EdgeModelArtifactView{}, err
	}
	record := EdgeModelArtifactRecord{
		ID:             deterministicV7("edge_model_artifact", tenant+"|"+site+"|"+versionID, now),
		TenantID:       tenant,
		SiteID:         site,
		Domain:         input.Domain,
		VersionID:      versionID,
		ObjectURI:      fmt.Sprintf("edge-bundle://%s/%s/%s", input.Domain, modelKey, version),
		ChecksumSHA256: checksum,
		MediaType:      EdgeArtifactMediaType,
		SizeBytes:      int64(len(rawMeta)),
		Metadata:       meta,
		CreatedAt:      now,
	}
	saved, err := store.InsertEdgeModelArtifact(ctx, record)
	if err != nil {
		return EdgeModelArtifactView{}, err
	}
	return EdgeModelArtifactView{
		ID:                 saved.ID,
		VersionID:          saved.VersionID,
		Domain:             saved.Domain,
		ModelKey:           saved.Metadata.ModelKey,
		Version:            saved.Metadata.Version,
		Name:               saved.Metadata.Name,
		Description:        saved.Metadata.Description,
		ObjectURI:          saved.ObjectURI,
		ChecksumSHA256:     saved.ChecksumSHA256,
		MaxShadowDeviation: saved.Metadata.MaxShadowDeviation,
		Hyperparams:        saved.Metadata.Hyperparams.Clone(),
		GatewayID:          saved.Metadata.GatewayID,
		Signed:             service.edgeModelSigner != nil,
		ApprovalStatus:     "PENDING",
		CreatedAt:          saved.CreatedAt.UTC(),
	}, nil
}

func (service *Service) ApproveEdgeModelArtifact(ctx context.Context, tenantID, siteID, artifactID, subject string, input ApproveEdgeModelRequest) (EdgeModelApprovalView, error) {
	tenant, site, err := service.ensureEdgeModelSiteVisible(ctx, tenantID, siteID)
	if err != nil {
		return EdgeModelApprovalView{}, err
	}
	store := service.effectiveEdgeModelStore()
	if store == nil {
		return EdgeModelApprovalView{}, errors.New("edge model store is unavailable")
	}
	artID := strings.TrimSpace(artifactID)
	if !uuidPattern.MatchString(artID) {
		return EdgeModelApprovalView{}, fmt.Errorf("%w: invalid artifactId", ErrEdgeModelInvalid)
	}
	decision := strings.ToUpper(strings.TrimSpace(input.Decision))
	if decision != "APPROVED" && decision != "REJECTED" {
		return EdgeModelApprovalView{}, fmt.Errorf("%w: decision must be APPROVED or REJECTED", ErrEdgeModelInvalid)
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" || len([]rune(reason)) > 2048 {
		return EdgeModelApprovalView{}, fmt.Errorf("%w: reason is required", ErrEdgeModelInvalid)
	}
	policyRev := strings.TrimSpace(input.PolicyRevision)
	if policyRev == "" {
		policyRev = EdgeApprovalPolicyRev
	}
	snap, err := store.LoadEdgeModelSiteSnapshot(ctx, tenant, site)
	if err != nil {
		return EdgeModelApprovalView{}, err
	}
	var art *EdgeModelArtifactRecord
	for i := range snap.Artifacts {
		if snap.Artifacts[i].ID == artID {
			art = &snap.Artifacts[i]
			break
		}
	}
	if art == nil {
		return EdgeModelApprovalView{}, ErrEdgeModelNotFound
	}
	var evalPtr *string
	if input.EvaluationID != nil && strings.TrimSpace(*input.EvaluationID) != "" {
		eid := strings.TrimSpace(*input.EvaluationID)
		found := false
		for _, ev := range snap.Evaluations {
			if ev.ID == eid && ev.VersionID == art.VersionID {
				found = true
				break
			}
		}
		if !found {
			return EdgeModelApprovalView{}, fmt.Errorf("%w: referenced evaluationId not found for artifact", ErrEdgeModelInvalid)
		}
		evalPtr = &eid
	}
	now := service.clock().UTC()
	actorType, actorID := resolveActorIdentity(subject)
	rec := EdgeModelApprovalRecord{
		ID:             deterministicV7("edge_model_approval", tenant+"|"+art.ID+"|"+decision, now),
		TenantID:       tenant,
		SiteID:         site,
		Domain:         art.Domain,
		VersionID:      art.VersionID,
		EvaluationID:   evalPtr,
		Decision:       decision,
		ActorType:      actorType,
		ActorID:        actorID,
		Reason:         reason,
		PolicyRevision: policyRev,
		DecidedAt:      now,
		CreatedAt:      now,
	}
	saved, err := store.InsertEdgeModelApproval(ctx, rec)
	if err != nil {
		return EdgeModelApprovalView{}, err
	}
	return EdgeModelApprovalView{
		ID:             saved.ID,
		ArtifactID:     art.ID,
		VersionID:      saved.VersionID,
		Domain:         saved.Domain,
		ModelKey:       art.Metadata.ModelKey,
		Version:        art.Metadata.Version,
		EvaluationID:   saved.EvaluationID,
		Decision:       saved.Decision,
		ActorType:      saved.ActorType,
		ActorID:        saved.ActorID,
		Reason:         saved.Reason,
		PolicyRevision: saved.PolicyRevision,
		DecidedAt:      saved.DecidedAt.UTC(),
	}, nil
}

func (service *Service) DeployEdgeModelArtifact(ctx context.Context, tenantID, siteID string, input DeployEdgeModelRequest) (EdgeModelDeploymentResult, error) {
	tenant, site, err := service.ensureEdgeModelSiteVisible(ctx, tenantID, siteID)
	if err != nil {
		return EdgeModelDeploymentResult{}, err
	}
	store := service.effectiveEdgeModelStore()
	if store == nil {
		return EdgeModelDeploymentResult{}, errors.New("edge model store is unavailable")
	}
	artID := strings.TrimSpace(input.ArtifactID)
	if !uuidPattern.MatchString(artID) {
		return EdgeModelDeploymentResult{}, fmt.Errorf("%w: invalid artifactId", ErrEdgeModelInvalid)
	}
	env := strings.ToUpper(strings.TrimSpace(input.Environment))
	if env != "SHADOW" && env != "PRODUCTION" {
		return EdgeModelDeploymentResult{}, fmt.Errorf("%w: environment must be SHADOW or PRODUCTION", ErrEdgeModelInvalid)
	}
	gatewayID := strings.TrimSpace(input.GatewayID)
	if gatewayID != "" && !uuidPattern.MatchString(gatewayID) {
		return EdgeModelDeploymentResult{}, fmt.Errorf("%w: invalid gatewayId", ErrEdgeModelInvalid)
	}

	snap, err := store.LoadEdgeModelSiteSnapshot(ctx, tenant, site)
	if err != nil {
		return EdgeModelDeploymentResult{}, err
	}
	var art *EdgeModelArtifactRecord
	for i := range snap.Artifacts {
		if snap.Artifacts[i].ID == artID {
			art = &snap.Artifacts[i]
			break
		}
	}
	if art == nil {
		return EdgeModelDeploymentResult{}, ErrEdgeModelNotFound
	}
	if err := edgecontrol.ValidateEdgeModelHyperparams(art.Domain, art.Metadata.Hyperparams); err != nil {
		return EdgeModelDeploymentResult{}, err
	}
	var approvalID string
	for _, ap := range snap.Approvals {
		if ap.VersionID == art.VersionID {
			if ap.Decision == "APPROVED" {
				approvalID = ap.ID
			}
			break
		}
	}
	if approvalID == "" {
		return EdgeModelDeploymentResult{}, ErrEdgeModelApprovalRequired
	}
	if gatewayID == "" {
		gatewayID = art.Metadata.GatewayID
	}
	servingRef := formatEdgeServingRef(gatewayID)
	now := service.clock().UTC()
	dep, allDeps, err := store.DeployEdgeModelVersion(ctx, tenant, site, *art, approvalID, env, servingRef, now)
	if err != nil {
		return EdgeModelDeploymentResult{}, err
	}
	desiredBundles, err := service.buildDesiredEdgeBundles(snap.Artifacts, allDeps)
	if err != nil {
		return EdgeModelDeploymentResult{}, err
	}
	return EdgeModelDeploymentResult{
		Deployment:     toEdgeModelDeploymentView(dep, *art),
		GatewayID:      parseServingGatewayID(dep.ServingRef),
		DesiredBundles: desiredBundles,
	}, nil
}

func (service *Service) RollbackEdgeModelDeployment(ctx context.Context, tenantID, siteID, deploymentID, subject string, input RollbackEdgeModelRequest) (EdgeModelRollbackResult, error) {
	tenant, site, err := service.ensureEdgeModelSiteVisible(ctx, tenantID, siteID)
	if err != nil {
		return EdgeModelRollbackResult{}, err
	}
	store := service.effectiveEdgeModelStore()
	if store == nil {
		return EdgeModelRollbackResult{}, errors.New("edge model store is unavailable")
	}
	depID := strings.TrimSpace(deploymentID)
	if !uuidPattern.MatchString(depID) {
		return EdgeModelRollbackResult{}, fmt.Errorf("%w: invalid deploymentId", ErrEdgeModelInvalid)
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" || len([]rune(reason)) > 2048 {
		return EdgeModelRollbackResult{}, fmt.Errorf("%w: rollback reason is required", ErrEdgeModelInvalid)
	}
	snap, err := store.LoadEdgeModelSiteSnapshot(ctx, tenant, site)
	if err != nil {
		return EdgeModelRollbackResult{}, err
	}
	var targetDep *EdgeModelDeploymentRecord
	for i := range snap.Deployments {
		if snap.Deployments[i].ID == depID {
			targetDep = &snap.Deployments[i]
			break
		}
	}
	if targetDep == nil {
		return EdgeModelRollbackResult{}, ErrEdgeModelNotFound
	}
	if targetDep.Status != "ACTIVE" {
		return EdgeModelRollbackResult{}, ErrEdgeModelRolloutConflict
	}

	byVersion := make(map[string]EdgeModelArtifactRecord, len(snap.Artifacts))
	byArtifactID := make(map[string]EdgeModelArtifactRecord, len(snap.Artifacts))
	for _, art := range snap.Artifacts {
		byVersion[art.VersionID] = art
		byArtifactID[art.ID] = art
	}
	currentArt, ok := byVersion[targetDep.VersionID]
	if !ok {
		return EdgeModelRollbackResult{}, ErrEdgeModelNotFound
	}
	approvedByVer := make(map[string]string)
	for _, ap := range snap.Approvals {
		if _, seen := approvedByVer[ap.VersionID]; !seen && ap.Decision == "APPROVED" {
			approvedByVer[ap.VersionID] = ap.ID
		}
	}

	var restoreArt *EdgeModelArtifactRecord
	var restoreApprovalID string
	if input.TargetArtifactID != nil && strings.TrimSpace(*input.TargetArtifactID) != "" {
		cand, exists := byArtifactID[strings.TrimSpace(*input.TargetArtifactID)]
		if !exists {
			return EdgeModelRollbackResult{}, ErrEdgeModelNotFound
		}
		if cand.Domain != currentArt.Domain || cand.Metadata.ModelKey != currentArt.Metadata.ModelKey || cand.VersionID == currentArt.VersionID {
			return EdgeModelRollbackResult{}, fmt.Errorf("%w: targetArtifactId must belong to the same modelKey and differ from active version", ErrEdgeModelInvalid)
		}
		apID := approvedByVer[cand.VersionID]
		if apID == "" {
			return EdgeModelRollbackResult{}, ErrEdgeModelApprovalRequired
		}
		restoreArt = &cand
		restoreApprovalID = apID
	} else {
		for _, dep := range snap.Deployments {
			if dep.ID == targetDep.ID || dep.Environment != targetDep.Environment || dep.VersionID == targetDep.VersionID {
				continue
			}
			cand, exists := byVersion[dep.VersionID]
			if !exists || cand.Domain != currentArt.Domain || cand.Metadata.ModelKey != currentArt.Metadata.ModelKey {
				continue
			}
			if apID := approvedByVer[cand.VersionID]; apID != "" {
				restoreArt = &cand
				restoreApprovalID = apID
				break
			}
		}
	}

	servingRef := targetDep.ServingRef
	if gw := strings.TrimSpace(input.GatewayID); gw != "" {
		if !uuidPattern.MatchString(gw) {
			return EdgeModelRollbackResult{}, fmt.Errorf("%w: invalid gatewayId", ErrEdgeModelInvalid)
		}
		servingRef = formatEdgeServingRef(gw)
	}
	now := service.clock().UTC()
	actorType, actorID := resolveActorIdentity(subject)
	rb, restoredDep, allDeps, err := store.RollbackEdgeModelDeployment(ctx, tenant, site, targetDep.ID, restoreArt, restoreApprovalID, reason, actorType, actorID, servingRef, now)
	if err != nil {
		return EdgeModelRollbackResult{}, err
	}
	desiredBundles, err := service.buildDesiredEdgeBundles(snap.Artifacts, allDeps)
	if err != nil {
		return EdgeModelRollbackResult{}, err
	}
	var restoredView *EdgeModelDeploymentView
	toVerLabel := ""
	if restoredDep != nil && restoreArt != nil {
		v := toEdgeModelDeploymentView(*restoredDep, *restoreArt)
		restoredView = &v
		toVerLabel = restoreArt.Metadata.Version
	}
	return EdgeModelRollbackResult{
		Rollback: EdgeModelRollbackView{
			ID:            rb.ID,
			DeploymentID:  rb.DeploymentID,
			FromVersionID: rb.FromVersionID,
			ToVersionID:   rb.ToVersionID,
			FromVersion:   currentArt.Metadata.Version,
			ToVersion:     toVerLabel,
			Reason:        rb.Reason,
			ActorType:     rb.ActorType,
			ActorID:       rb.ActorID,
			Status:        rb.Status,
			RequestedAt:   rb.RequestedAt.UTC(),
			AppliedAt:     rb.AppliedAt,
		},
		RestoredDeployment: restoredView,
		GatewayID:          parseServingGatewayID(servingRef),
		DesiredBundles:     desiredBundles,
	}, nil
}

func (service *Service) RecordEdgeModelEvaluation(ctx context.Context, tenantID, siteID, subject string, input RecordEdgeModelEvaluationRequest) (EdgeModelEvaluationResult, error) {
	tenant, site, err := service.ensureEdgeModelSiteVisible(ctx, tenantID, siteID)
	if err != nil {
		return EdgeModelEvaluationResult{}, err
	}
	store := service.effectiveEdgeModelStore()
	if store == nil {
		return EdgeModelEvaluationResult{}, errors.New("edge model store is unavailable")
	}
	artID := strings.TrimSpace(input.ArtifactID)
	if !uuidPattern.MatchString(artID) {
		return EdgeModelEvaluationResult{}, fmt.Errorf("%w: invalid artifactId", ErrEdgeModelInvalid)
	}
	evalType := strings.ToUpper(strings.TrimSpace(input.EvaluationType))
	switch evalType {
	case "OFFLINE", "SHADOW", "PRODUCTION", "REGRESSION":
	default:
		return EdgeModelEvaluationResult{}, fmt.Errorf("%w: invalid evaluationType", ErrEdgeModelInvalid)
	}
	if input.SampleCount <= 0 || math.IsNaN(input.MeanAbsDeviation) || math.IsInf(input.MeanAbsDeviation, 0) || input.MeanAbsDeviation < 0 ||
		math.IsNaN(input.MaxAbsDeviation) || math.IsInf(input.MaxAbsDeviation, 0) || input.MaxAbsDeviation < 0 {
		return EdgeModelEvaluationResult{}, fmt.Errorf("%w: sampleCount and deviations must be valid positive numbers", ErrEdgeModelInvalid)
	}
	snap, err := store.LoadEdgeModelSiteSnapshot(ctx, tenant, site)
	if err != nil {
		return EdgeModelEvaluationResult{}, err
	}
	var art *EdgeModelArtifactRecord
	for i := range snap.Artifacts {
		if snap.Artifacts[i].ID == artID {
			art = &snap.Artifacts[i]
			break
		}
	}
	if art == nil {
		return EdgeModelEvaluationResult{}, ErrEdgeModelNotFound
	}
	threshold := art.Metadata.MaxShadowDeviation
	breached := input.MaxAbsDeviation > threshold
	outcome := strings.ToUpper(strings.TrimSpace(input.Outcome))
	if outcome == "" {
		if breached {
			outcome = "FAILED"
		} else {
			outcome = "PASSED"
		}
	}
	switch outcome {
	case "PASSED", "FAILED", "INCONCLUSIVE":
	default:
		return EdgeModelEvaluationResult{}, fmt.Errorf("%w: invalid outcome", ErrEdgeModelInvalid)
	}

	now := service.clock().UTC()
	_, evaluator := resolveActorIdentity(subject)
	evalID := deterministicV7("edge_model_eval", tenant+"|"+art.ID+"|"+evalType, now)
	metrics := EdgeModelEvaluationMetrics{
		ModelKey:         art.Metadata.ModelKey,
		Version:          art.Metadata.Version,
		SampleCount:      input.SampleCount,
		MeanAbsDeviation: input.MeanAbsDeviation,
		MaxAbsDeviation:  input.MaxAbsDeviation,
		ThresholdValue:   threshold,
		BaselineCOP:      input.BaselineCOP,
		CandidateCOP:     input.CandidateCOP,
		EnergySavingPct:  input.EnergySavingPct,
		Notes:            strings.TrimSpace(input.Notes),
	}
	evalRec := EdgeModelEvaluationRecord{
		ID:             evalID,
		TenantID:       tenant,
		SiteID:         site,
		Domain:         art.Domain,
		VersionID:      art.VersionID,
		EvaluationType: evalType,
		Metrics:        metrics,
		Outcome:        outcome,
		Evaluator:      evaluator,
		EvaluatedAt:    now,
		CreatedAt:      now,
	}

	var driftRec *EdgeModelDriftRecord
	var driftView *EdgeModelDriftView
	if input.DeploymentID != nil && strings.TrimSpace(*input.DeploymentID) != "" {
		depID := strings.TrimSpace(*input.DeploymentID)
		var matched *EdgeModelDeploymentRecord
		for i := range snap.Deployments {
			if snap.Deployments[i].ID == depID && snap.Deployments[i].VersionID == art.VersionID {
				matched = &snap.Deployments[i]
				break
			}
		}
		if matched == nil {
			return EdgeModelEvaluationResult{}, ErrEdgeModelNotFound
		}
		driftStatus := "NORMAL"
		if breached {
			driftStatus = "BREACHED"
		} else if threshold > 0 && input.MaxAbsDeviation >= threshold*0.8 {
			driftStatus = "WARNING"
		}
		evidence := EdgeModelDriftEvidence{
			ModelKey:         art.Metadata.ModelKey,
			Version:          art.Metadata.Version,
			EvaluationID:     evalID,
			SampleCount:      input.SampleCount,
			MeanAbsDeviation: input.MeanAbsDeviation,
			Notes:            strings.TrimSpace(input.Notes),
		}
		dr := EdgeModelDriftRecord{
			ID:            deterministicV7("edge_model_drift", tenant+"|"+depID, now),
			TenantID:      tenant,
			SiteID:        site,
			DeploymentID:  depID,
			MetricCode:    "SHADOW_MAX_ABS_DEVIATION",
			ObservedValue: input.MaxAbsDeviation,
			Threshold:     threshold,
			DriftStatus:   driftStatus,
			Evidence:      evidence,
			ObservedAt:    now,
			CreatedAt:     now,
		}
		driftRec = &dr
		v := EdgeModelDriftView{
			ID:            dr.ID,
			DeploymentID:  dr.DeploymentID,
			MetricCode:    dr.MetricCode,
			ObservedValue: dr.ObservedValue,
			Threshold:     dr.Threshold,
			DriftStatus:   dr.DriftStatus,
			Evidence:      dr.Evidence,
			ObservedAt:    dr.ObservedAt.UTC(),
		}
		driftView = &v
	}

	if err := store.InsertEdgeModelEvaluation(ctx, evalRec, driftRec); err != nil {
		return EdgeModelEvaluationResult{}, err
	}
	return EdgeModelEvaluationResult{
		Evaluation: EdgeModelEvaluationView{
			ID:             evalRec.ID,
			ArtifactID:     art.ID,
			VersionID:      evalRec.VersionID,
			Domain:         evalRec.Domain,
			ModelKey:       art.Metadata.ModelKey,
			Version:        art.Metadata.Version,
			EvaluationType: evalRec.EvaluationType,
			Outcome:        evalRec.Outcome,
			Evaluator:      evalRec.Evaluator,
			Metrics:        evalRec.Metrics,
			EvaluatedAt:    evalRec.EvaluatedAt.UTC(),
		},
		DriftObservation: driftView,
		Breached:         breached,
	}, nil
}

func decodeEdgeModelStrictJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	reader := http.MaxBytesReader(writer, request.Body, 128*1024)
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func (handler *HTTPHandler) writeEdgeModelErr(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrOptimizationNotFound), errors.Is(err, ErrEdgeModelNotFound):
		writeJSONError(writer, http.StatusNotFound, "edge_model_not_found")
	case errors.Is(err, ErrEdgeModelConflict):
		writeJSONError(writer, http.StatusConflict, "edge_model_conflict")
	case errors.Is(err, ErrEdgeModelApprovalRequired):
		writeJSONError(writer, http.StatusConflict, "edge_model_approval_required")
	case errors.Is(err, ErrEdgeModelRolloutConflict):
		writeJSONError(writer, http.StatusConflict, "edge_model_rollout_conflict")
	case errors.Is(err, edgecontrol.ErrEdgeModelValidation), errors.Is(err, edgecontrol.ErrEdgeModelSignature):
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"error":  "edge_model_validation_failed",
			"detail": err.Error(),
		})
	case errors.Is(err, ErrEdgeModelInvalid):
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"error":  "invalid_request",
			"detail": err.Error(),
		})
	default:
		writeJSONError(writer, http.StatusBadGateway, "optimization_unavailable")
	}
}

func (handler *HTTPHandler) handleGetEdgeModelWorkspace(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	result, err := handler.service.GetEdgeModelWorkspace(request.Context(), site.TenantID, site.SiteID)
	if err != nil {
		handler.writeEdgeModelErr(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleCreateEdgeModelArtifact(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	var input CreateEdgeModelArtifactRequest
	if err := decodeEdgeModelStrictJSON(writer, request, &input); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := handler.service.CreateEdgeModelArtifact(request.Context(), site.TenantID, site.SiteID, site.Subject, input)
	if err != nil {
		handler.writeEdgeModelErr(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleApproveEdgeModelArtifact(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	var input ApproveEdgeModelRequest
	if err := decodeEdgeModelStrictJSON(writer, request, &input); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := handler.service.ApproveEdgeModelArtifact(request.Context(), site.TenantID, site.SiteID, request.PathValue("artifactId"), site.Subject, input)
	if err != nil {
		handler.writeEdgeModelErr(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleDeployEdgeModelArtifact(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	var input DeployEdgeModelRequest
	if err := decodeEdgeModelStrictJSON(writer, request, &input); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := handler.service.DeployEdgeModelArtifact(request.Context(), site.TenantID, site.SiteID, input)
	if err != nil {
		handler.writeEdgeModelErr(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleRollbackEdgeModelDeployment(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	var input RollbackEdgeModelRequest
	if err := decodeEdgeModelStrictJSON(writer, request, &input); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := handler.service.RollbackEdgeModelDeployment(request.Context(), site.TenantID, site.SiteID, request.PathValue("deploymentId"), site.Subject, input)
	if err != nil {
		handler.writeEdgeModelErr(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *HTTPHandler) handleRecordEdgeModelEvaluation(writer http.ResponseWriter, request *http.Request, site identitycontext.SiteContext) {
	var input RecordEdgeModelEvaluationRequest
	if err := decodeEdgeModelStrictJSON(writer, request, &input); err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := handler.service.RecordEdgeModelEvaluation(request.Context(), site.TenantID, site.SiteID, site.Subject, input)
	if err != nil {
		handler.writeEdgeModelErr(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(result)
}
