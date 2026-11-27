package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/planttwin"
)

const (
	DefaultStagingSweepInterval    = time.Hour
	defaultStagingSiteLookback     = 24 * time.Hour
	StagingEvaluationSolverName    = "STAGING_RULE_ENGINE"
	StagingEvaluationMode          = "STAGING_ONLY"
	StagingEvaluationQuality       = "STAGING_ONLY"
	StagingEvaluationObjective     = "STAGING"
	stagingZeroProvenanceUUID      = "00000000-0000-0000-0000-000000000000"
	stagingOutcomeRecommended      = "RECOMMENDED"
	stagingOutcomeSuppressed       = "SUPPRESSED"
	stagingOutcomeNoOp             = "NO_OP"
	stagingEvaluationSiteLimit     = 1000
	stagingSiteEvaluationNamespace = "staging_only"
)

// StagingSiteScope identifies a Tenant and Site eligible for independent staging evaluation.
type StagingSiteScope struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
}

// StagingSiteLister lists Tenant/Site scopes with active plant twins or telemetry.
type StagingSiteLister interface {
	ListStagingSites(ctx context.Context, at time.Time) ([]StagingSiteScope, error)
}

// StagingSweepSummary reports counts for one hourly independent staging evaluation sweep.
type StagingSweepSummary struct {
	EvaluatedAt  time.Time `json:"evaluatedAt"`
	Sites        int       `json:"sites"`
	Persisted    int       `json:"persisted"`
	Deduplicated int       `json:"deduplicated"`
	Skipped      int       `json:"skipped"`
	Failed       int       `json:"failed"`
}

// StagingEvaluationPayload is the typed JSON body stored in analytics.optimization_evaluations.evaluation_json
// for sensor-only STAGING_ONLY evaluations. It never carries command_intents or control setpoints.
type StagingEvaluationPayload struct {
	Solver           string                       `json:"solver"`
	Mode             string                       `json:"mode"`
	Reason           string                       `json:"reason"`
	ConfirmationMode string                       `json:"confirmationMode"`
	ReadOnly         bool                         `json:"readOnly"`
	BucketAt         time.Time                    `json:"bucketAt"`
	Outcome          string                       `json:"outcome"`
	SiteControlMode  string                       `json:"siteControlMode,omitempty"`
	EvaluatedAt      time.Time                    `json:"evaluatedAt"`
	Recommendations  []StagingRecommendationDraft `json:"recommendations"`
	Diagnostics      []StagingDiagnostic          `json:"diagnostics"`
}

// WithStagingSiteLister configures how scheduled staging sweeps discover active sites.
func (service *Service) WithStagingSiteLister(lister StagingSiteLister) *Service {
	service.stagingSiteLister = lister
	return service
}

func (service *Service) effectiveStagingSiteLister() StagingSiteLister {
	if service == nil {
		return nil
	}
	if service.stagingSiteLister != nil {
		return service.stagingSiteLister
	}
	if service.staging != nil {
		if lister, ok := service.staging.History.(StagingSiteLister); ok && lister != nil {
			return lister
		}
		if lister, ok := service.staging.Catalog.(StagingSiteLister); ok && lister != nil {
			return lister
		}
	}
	if lister, ok := service.publication.(StagingSiteLister); ok && lister != nil {
		return lister
	}
	return nil
}

type compositeStagingSiteLister struct {
	listers []StagingSiteLister
}

// NewCompositeStagingSiteLister merges active site scopes across multiple listers (for example
// ClickHouse recent telemetry sites and Postgres scheduled optimization sites), deduplicating by
// normalized tenant/site UUID.
func NewCompositeStagingSiteLister(listers ...StagingSiteLister) StagingSiteLister {
	filtered := make([]StagingSiteLister, 0, len(listers))
	for _, lister := range listers {
		if lister != nil {
			filtered = append(filtered, lister)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return compositeStagingSiteLister{listers: filtered}
}

func (c compositeStagingSiteLister) ListStagingSites(ctx context.Context, at time.Time) ([]StagingSiteScope, error) {
	var collected []StagingSiteScope
	var errs []error
	succeeded := 0
	for _, lister := range c.listers {
		sites, err := lister.ListStagingSites(ctx, at)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		succeeded++
		collected = append(collected, sites...)
	}
	deduped := deduplicateStagingSites(collected)
	if succeeded == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	if len(errs) > 0 && len(deduped) == 0 {
		return nil, errors.Join(errs...)
	}
	return deduped, nil
}

// ListStagingSites discovers Tenant/Site pairs that reported accepted telemetry within the last 24
// hours up to at, enabling STAGING_ONLY evaluations for twin-only sites without policies or bindings.
func (reader *ClickHouseInputReader) ListStagingSites(ctx context.Context, at time.Time) ([]StagingSiteScope, error) {
	if reader == nil || reader.baseURL == nil || reader.client == nil {
		return nil, errors.New("optimization staging site reader is not configured")
	}
	if at.IsZero() {
		at = reader.now().UTC()
	} else {
		at = at.UTC()
	}
	from := at.Add(-defaultStagingSiteLookback)
	sql := fmt.Sprintf(`SELECT DISTINCT
 toString(assumeNotNull(tenant_id)) AS tenantId,
 toString(assumeNotNull(site_id)) AS siteId
FROM telemetry_history.observations
WHERE tenant_id IS NOT NULL
  AND site_id IS NOT NULL
  AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC')
  AND sampled_at <= parseDateTime64BestEffort('%s',3,'UTC')
  AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER')
ORDER BY tenantId ASC, siteId ASC
LIMIT %d
FORMAT JSONEachRow`, formatOptimizationTime(from), formatOptimizationTime(at), stagingEvaluationSiteLimit)

	payload, err := reader.queryLimited(ctx, sql, maximumOptimizationInputResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("list staging sites from telemetry history: %w", err)
	}
	return decodeStagingSiteScopes(bytes.NewReader(payload))
}

func decodeStagingSiteScopes(source io.Reader) ([]StagingSiteScope, error) {
	type rawScope struct {
		TenantID string `json:"tenantId"`
		SiteID   string `json:"siteId"`
	}
	decoder := json.NewDecoder(source)
	var scopes []StagingSiteScope
	for {
		var row rawScope
		err := decoder.Decode(&row)
		if errors.Is(err, io.EOF) {
			return deduplicateStagingSites(scopes), nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode staging site scope: %w", err)
		}
		tenant := strings.ToLower(strings.TrimSpace(row.TenantID))
		site := strings.ToLower(strings.TrimSpace(row.SiteID))
		if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
			continue
		}
		scopes = append(scopes, StagingSiteScope{TenantID: tenant, SiteID: site})
	}
}

func deduplicateStagingSites(sites []StagingSiteScope) []StagingSiteScope {
	seen := make(map[string]struct{}, len(sites))
	out := make([]StagingSiteScope, 0, len(sites))
	for _, s := range sites {
		tenant := strings.ToLower(strings.TrimSpace(s.TenantID))
		site := strings.ToLower(strings.TrimSpace(s.SiteID))
		if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
			continue
		}
		key := tenant + "|" + site
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, StagingSiteScope{TenantID: tenant, SiteID: site})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TenantID != out[j].TenantID {
			return out[i].TenantID < out[j].TenantID
		}
		return out[i].SiteID < out[j].SiteID
	})
	return out
}

// PersistStagingEvaluation evaluates sensor-only staging recommendations for one Site at evalAt
// and persists a read-only STAGING_ONLY / MANUAL_CONFIRM record to analytics.optimization_evaluations.
// It is idempotent per (tenantID, siteID, hourBucket): repeated calls inside the same UTC hour
// return persisted=false, err=nil without inserting duplicate rows.
func (service *Service) PersistStagingEvaluation(ctx context.Context, tenantID, siteID string, evalAt time.Time) (bool, error) {
	if service == nil || service.staging == nil || service.evaluations == nil {
		return false, ErrStagingEvaluationNotFound
	}
	normTenant := strings.ToLower(strings.TrimSpace(tenantID))
	normSite := strings.ToLower(strings.TrimSpace(siteID))
	if !uuidPattern.MatchString(normTenant) || !uuidPattern.MatchString(normSite) {
		return false, errors.New("tenantId and siteId must be UUIDs")
	}
	if evalAt.IsZero() {
		evalAt = service.clock().UTC()
	} else {
		evalAt = evalAt.UTC()
	}

	probe := stagingEvaluationIdentity(normTenant, normSite, evalAt)
	present, err := service.evaluations.HasEvaluation(ctx, probe)
	if err != nil {
		return false, fmt.Errorf("check existing staging evaluation: %w", err)
	}
	if present {
		return false, nil
	}

	stagingEval, err := service.staging.Evaluate(ctx, normTenant, normSite, evalAt)
	if err != nil {
		return false, err
	}
	evaluation, err := buildStagingOnlyEvaluation(probe, stagingEval, evalAt)
	if err != nil {
		return false, err
	}
	if err := service.evaluations.InsertEvaluation(ctx, evaluation); err != nil {
		return false, fmt.Errorf("persist staging evaluation: %w", err)
	}
	return true, nil
}

// EvaluateAndPersistAllSiteStaging enumerates active plant twin sites and persists hourly
// sensor-only STAGING_ONLY evaluations without requiring policies, model bindings, or room_temperature.
func (service *Service) EvaluateAndPersistAllSiteStaging(ctx context.Context, evalAt time.Time) (StagingSweepSummary, error) {
	if service == nil || service.staging == nil || service.evaluations == nil {
		return StagingSweepSummary{}, ErrStagingEvaluationNotFound
	}
	if evalAt.IsZero() {
		evalAt = service.clock().UTC()
	} else {
		evalAt = evalAt.UTC()
	}
	summary := StagingSweepSummary{EvaluatedAt: evalAt}
	lister := service.effectiveStagingSiteLister()
	if lister == nil {
		return summary, ErrStagingEvaluationNotFound
	}
	rawSites, err := lister.ListStagingSites(ctx, evalAt)
	if err != nil {
		return summary, fmt.Errorf("list active staging sites: %w", err)
	}
	sites := deduplicateStagingSites(rawSites)
	summary.Sites = len(sites)

	var errs []error
	for _, site := range sites {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		persisted, persistErr := service.PersistStagingEvaluation(ctx, site.TenantID, site.SiteID, evalAt)
		if persistErr != nil {
			if errors.Is(persistErr, planttwin.ErrSiteNotFound) {
				summary.Skipped++
				continue
			}
			summary.Failed++
			errs = append(errs, fmt.Errorf("site %s/%s: %w", site.TenantID, site.SiteID, persistErr))
			continue
		}
		if persisted {
			summary.Persisted++
		} else {
			summary.Deduplicated++
		}
	}
	return summary, errors.Join(errs...)
}

func stagingEvaluationIdentity(tenantID, siteID string, evalAt time.Time) Evaluation {
	bucketAt := evalAt.UTC().Truncate(time.Hour)
	bucketKey := bucketAt.Format(time.RFC3339)
	scope := stagingZeroProvenanceUUID
	if tenantID != "" && siteID != "" {
		scope = tenantID + ":" + siteID
	}
	return Evaluation{
		EvaluationID:           deterministicV7(stagingSiteEvaluationNamespace+":evaluation:"+scope, bucketKey, bucketAt),
		TenantID:               tenantID,
		SiteID:                 siteID,
		OptimizationRunID:      deterministicV7(stagingSiteEvaluationNamespace+":run:"+scope, bucketKey, bucketAt),
		RecommendationID:       deterministicV7(stagingSiteEvaluationNamespace+":recommendation:"+scope, bucketKey, bucketAt),
		SubjectType:            "SITE",
		SubjectID:              siteID,
		Objective:              StagingEvaluationObjective,
		Quality:                StagingEvaluationQuality,
		InputSnapshotID:        deterministicV7(stagingSiteEvaluationNamespace+":input:"+scope, bucketKey, bucketAt),
		PolicyVersionID:        stagingZeroProvenanceUUID,
		TopologyVersionID:      stagingZeroProvenanceUUID,
		LoadForecastSnapshotID: stagingZeroProvenanceUUID,
		TariffVersionID:        stagingZeroProvenanceUUID,
		GeneratedAt:            evalAt.UTC(),
	}
}

func buildStagingOnlyEvaluation(identity Evaluation, stagingEval StagingEvaluation, evalAt time.Time) (Evaluation, error) {
	bucketAt := evalAt.UTC().Truncate(time.Hour)
	confirmationMode := strings.TrimSpace(stagingEval.ConfirmationMode)
	if confirmationMode == "" {
		confirmationMode = ConfirmationModeManual
	}
	recommendations := stagingEval.Recommendations
	if recommendations == nil {
		recommendations = []StagingRecommendationDraft{}
	}
	diagnostics := stagingEval.Diagnostics
	if diagnostics == nil {
		diagnostics = []StagingDiagnostic{}
	}
	outcome := stagingOutcomeNoOp
	if len(recommendations) > 0 {
		outcome = stagingOutcomeRecommended
	} else {
		for _, diag := range diagnostics {
			if strings.EqualFold(strings.TrimSpace(diag.Status), stagingOutcomeSuppressed) {
				outcome = stagingOutcomeSuppressed
				break
			}
		}
	}
	evaluatedAt := stagingEval.EvaluatedAt.UTC()
	if evaluatedAt.IsZero() {
		evaluatedAt = evalAt.UTC()
	}
	payload := StagingEvaluationPayload{
		Solver:           StagingEvaluationSolverName,
		Mode:             StagingEvaluationMode,
		Reason:           StagingEvaluationMode,
		ConfirmationMode: confirmationMode,
		ReadOnly:         true,
		BucketAt:         bucketAt,
		Outcome:          outcome,
		SiteControlMode:  strings.TrimSpace(stagingEval.SiteControlMode),
		EvaluatedAt:      evaluatedAt,
		Recommendations:  recommendations,
		Diagnostics:      diagnostics,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Evaluation{}, fmt.Errorf("encode staging evaluation payload: %w", err)
	}
	identity.SolverOutcome = outcome
	identity.ConstraintCount = uint32(len(diagnostics))
	identity.EvaluationJSON = string(encoded)
	identity.GeneratedAt = evalAt.UTC()
	return identity, nil
}
