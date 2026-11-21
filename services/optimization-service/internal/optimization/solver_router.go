package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

const (
	DefaultBuiltinSolverRef    = "builtin://hvac-recommendation-v1"
	ChillerCOPBuiltinSolverRef = "builtin://chiller-cop-model"
	DefaultSolverLoopKey       = "PLANT"

	SolverSourceDefault           = "DEFAULT"
	SolverSourceDeploymentBinding = "DEPLOYMENT_BINDING"
	SolverSourceSiteLoopCanary    = "SITE_LOOP_CANARY"

	defaultRemoteSolverTimeout       = intelligencemodel.DefaultInferenceCallTimeout
	maximumRemoteSolverResponseBytes = int64(1 << 20)
)

// ActiveSolverSelection describes the resolved optimization solver endpoint or builtin strategy
// for a given (tenant, site, loopKey) scope.
type ActiveSolverSelection struct {
	ServingRef           string  `json:"servingRef"`
	Source               string  `json:"source"`
	LoopKey              string  `json:"loopKey"`
	DeploymentRevisionID *string `json:"deploymentRevisionId"`
}

// ActiveSolverResolver resolves which optimization solver serves a Site/Loop at a point in time.
type ActiveSolverResolver interface {
	ResolveActiveSolver(ctx context.Context, tenantID, siteID, loopKey string, at time.Time) (ActiveSolverSelection, error)
}

// ResolveActiveSolver resolves the active solver using canary loop bindings first, then active
// AI deployment bindings, and finally the builtin baseline solver.
func (store *PostgresStore) ResolveActiveSolver(ctx context.Context, tenantID, siteID, loopKey string, _ time.Time) (ActiveSolverSelection, error) {
	if store == nil || store.pool == nil {
		return ActiveSolverSelection{}, errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return ActiveSolverSelection{}, errors.New("solver resolution scope is invalid")
	}
	effectiveLoop := normalizeSolverLoopKey(loopKey)

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return ActiveSolverSelection{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenant, site); err != nil {
		return ActiveSolverSelection{}, err
	}

	var canaryRef, matchedLoop string
	err = tx.QueryRow(ctx, `
SELECT serving_ref, loop_key
FROM core_registry.optimization_canary_bindings
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND status = 'ACTIVE'
  AND loop_key IN ($3, 'PLANT')
ORDER BY CASE WHEN loop_key = $3 THEN 0 ELSE 1 END
LIMIT 1
`, tenant, site, effectiveLoop).Scan(&canaryRef, &matchedLoop)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ActiveSolverSelection{}, fmt.Errorf("query site loop canary binding: %w", err)
	}
	if err == nil {
		if sanitized := sanitizeServingRef(canaryRef); sanitized != "" {
			if err = tx.Commit(ctx); err != nil {
				return ActiveSolverSelection{}, err
			}
			cleanLoop := normalizeSolverLoopKey(matchedLoop)
			return ActiveSolverSelection{
				ServingRef: sanitized,
				Source:     SolverSourceSiteLoopCanary,
				LoopKey:    cleanLoop,
			}, nil
		}
	}

	var deploymentRef, revisionID string
	err = tx.QueryRow(ctx, `
SELECT m.serving_ref, r.id::text
FROM core_registry.ai_deployment_bindings AS b
JOIN core_registry.ai_deployment_revisions AS r
  ON r.tenant_id = b.tenant_id AND r.id = b.deployment_revision_id
JOIN core_registry.ai_model_definitions AS m
  ON m.tenant_id = r.tenant_id AND m.id = r.model_definition_id
WHERE b.tenant_id = $1::uuid
  AND b.site_id = $2::uuid
  AND b.use_case = 'OPTIMIZATION'
  AND b.status = 'ACTIVE'
  AND r.use_case = 'OPTIMIZATION'
  AND r.enabled = true
LIMIT 1
`, tenant, site).Scan(&deploymentRef, &revisionID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ActiveSolverSelection{}, fmt.Errorf("query optimization deployment binding: %w", err)
	}
	if err == nil {
		sanitized := sanitizeServingRef(deploymentRef)
		if sanitized == "" {
			sanitized = DefaultBuiltinSolverRef
		}
		if err = tx.Commit(ctx); err != nil {
			return ActiveSolverSelection{}, err
		}
		revCopy := strings.TrimSpace(revisionID)
		var revPtr *string
		if revCopy != "" {
			revPtr = &revCopy
		}
		return ActiveSolverSelection{
			ServingRef:           sanitized,
			Source:               SolverSourceDeploymentBinding,
			LoopKey:              effectiveLoop,
			DeploymentRevisionID: revPtr,
		}, nil
	}

	if err = tx.Commit(ctx); err != nil {
		return ActiveSolverSelection{}, err
	}
	return ActiveSolverSelection{
		ServingRef: DefaultBuiltinSolverRef,
		Source:     SolverSourceDefault,
		LoopKey:    effectiveLoop,
	}, nil
}

func normalizeSolverLoopKey(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return DefaultSolverLoopKey
	}
	return trimmed
}

// sanitizeServingRef parses raw as a URL/URI and strips any userinfo, query string, or fragment.
func sanitizeServingRef(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.Scheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if parsed.Scheme == "" || strings.TrimSpace(parsed.Host) == "" {
		return ""
	}
	return parsed.String()
}

// ParseAllowedSolverHosts parses a comma-separated host allowlist such as OPTIMIZATION_ALLOWED_SOLVER_HOSTS.
func ParseAllowedSolverHosts(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		clean := strings.ToLower(strings.TrimSpace(part))
		if clean == "" {
			continue
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func isAllowedSolverHost(parsed *url.URL, allowedHosts []string) bool {
	if parsed == nil || len(allowedHosts) == 0 {
		return false
	}
	hostname := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	hostPort := strings.ToLower(strings.TrimSpace(parsed.Host))
	if hostname == "" {
		return false
	}
	for _, entry := range allowedHosts {
		clean := strings.ToLower(strings.TrimSpace(entry))
		if clean == "" {
			continue
		}
		if strings.Trim(clean, "[]") == hostname || clean == hostPort {
			return true
		}
	}
	return false
}

// RoutingSolverConfig configures a RoutingSolverAdapter.
type RoutingSolverConfig struct {
	Resolver     ActiveSolverResolver
	Baseline     SolverAdapter
	COPModel     *COPModelSolver
	HTTPClient   *http.Client
	AllowedHosts []string
	Breaker      *intelligencemodel.CircuitBreaker
	Bulkhead     *intelligencemodel.Bulkhead
	Timeout      time.Duration
	Logf         func(string, ...any)
}

// RoutingSolverAdapter routes optimization requests per site/loop to the active builtin or remote
// HTTP solver with allowlist validation, circuit breaker, bulkhead, timeout, and bumpless fallback.
type RoutingSolverAdapter struct {
	Resolver     ActiveSolverResolver
	Baseline     SolverAdapter
	COPModel     *COPModelSolver
	HTTPClient   *http.Client
	AllowedHosts []string
	Breaker      *intelligencemodel.CircuitBreaker
	Bulkhead     *intelligencemodel.Bulkhead
	Timeout      time.Duration
	Logf         func(string, ...any)

	initOnce        sync.Once
	defaultBreaker  *intelligencemodel.CircuitBreaker
	defaultBulkhead *intelligencemodel.Bulkhead
	defaultHTTP     *http.Client
}

// NewRoutingSolverAdapter constructs a RoutingSolverAdapter with resilient defaults.
func NewRoutingSolverAdapter(cfg RoutingSolverConfig) *RoutingSolverAdapter {
	baseline := cfg.Baseline
	if baseline == nil {
		baseline = HVACRecommendationSolver{}
	}
	breaker := cfg.Breaker
	if breaker == nil {
		breaker = intelligencemodel.NewCircuitBreaker(intelligencemodel.CircuitBreakerConfig{})
	}
	bulkhead := cfg.Bulkhead
	if bulkhead == nil {
		bulkhead = intelligencemodel.NewBulkhead(intelligencemodel.DefaultBulkheadMaxConcurrent)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultRemoteSolverTimeout
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = newSafeRemoteSolverHTTPClient(timeout)
	}
	return &RoutingSolverAdapter{
		Resolver:     cfg.Resolver,
		Baseline:     baseline,
		COPModel:     cfg.COPModel,
		HTTPClient:   httpClient,
		AllowedHosts: append([]string(nil), cfg.AllowedHosts...),
		Breaker:      breaker,
		Bulkhead:     bulkhead,
		Timeout:      timeout,
		Logf:         cfg.Logf,
	}
}

func newSafeRemoteSolverHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultRemoteSolverTimeout
	}
	return newPooledHTTPClient(timeout, true)
}

func (adapter *RoutingSolverAdapter) ensureDefaults() {
	adapter.initOnce.Do(func() {
		adapter.defaultBreaker = intelligencemodel.NewCircuitBreaker(intelligencemodel.CircuitBreakerConfig{})
		adapter.defaultBulkhead = intelligencemodel.NewBulkhead(intelligencemodel.DefaultBulkheadMaxConcurrent)
		timeout := adapter.Timeout
		if timeout <= 0 {
			timeout = defaultRemoteSolverTimeout
		}
		adapter.defaultHTTP = newSafeRemoteSolverHTTPClient(timeout)
	})
}

func (adapter *RoutingSolverAdapter) baselineSolver() SolverAdapter {
	if adapter != nil && adapter.Baseline != nil {
		return adapter.Baseline
	}
	return HVACRecommendationSolver{}
}

func (adapter *RoutingSolverAdapter) logf(format string, args ...any) {
	if adapter != nil && adapter.Logf != nil {
		adapter.Logf(format, args...)
	}
}

func (adapter *RoutingSolverAdapter) resilienceConfig() intelligencemodel.ResilientInvokeConfig {
	adapter.ensureDefaults()
	timeout := adapter.Timeout
	if timeout <= 0 {
		timeout = defaultRemoteSolverTimeout
	}
	breaker := adapter.Breaker
	if breaker == nil {
		breaker = adapter.defaultBreaker
	}
	bulkhead := adapter.Bulkhead
	if bulkhead == nil {
		bulkhead = adapter.defaultBulkhead
	}
	return intelligencemodel.ResilientInvokeConfig{
		Timeout:  timeout,
		Breaker:  breaker,
		Bulkhead: bulkhead,
	}
}

func (adapter *RoutingSolverAdapter) httpClient() *http.Client {
	adapter.ensureDefaults()
	if adapter.HTTPClient != nil {
		return adapter.HTTPClient
	}
	return adapter.defaultHTTP
}

// Recommend resolves the active solver for (tenantID, siteID, "PLANT") and executes it with
// bumpless fallback to the baseline HVACRecommendationSolver.
func (adapter *RoutingSolverAdapter) Recommend(ctx context.Context, request Request, at time.Time) (Recommendation, error) {
	if err := request.Validate(); err != nil {
		return Recommendation{}, err
	}
	baseline := adapter.baselineSolver()
	selection := ActiveSolverSelection{
		ServingRef: DefaultBuiltinSolverRef,
		Source:     SolverSourceDefault,
		LoopKey:    DefaultSolverLoopKey,
	}
	if adapter != nil && adapter.Resolver != nil {
		resolved, err := adapter.Resolver.ResolveActiveSolver(ctx, request.TenantID, request.SiteID, DefaultSolverLoopKey, at)
		if err != nil {
			adapter.logf("solver_resolution_fallback: site=%s err=%v", request.SiteID, err)
			return baseline.Recommend(ctx, request, at)
		}
		selection = resolved
	}
	servingRef := sanitizeServingRef(selection.ServingRef)
	if servingRef == "" || servingRef == DefaultBuiltinSolverRef {
		return baseline.Recommend(ctx, request, at)
	}
	if servingRef == ChillerCOPBuiltinSolverRef {
		if adapter == nil || adapter.COPModel == nil {
			adapter.logf("cop_solver_unconfigured_fallback: site=%s", request.SiteID)
			return baseline.Recommend(ctx, request, at)
		}
		rec, err := adapter.COPModel.Recommend(ctx, request, at)
		if err != nil {
			adapter.logf("cop_solver_fallback: site=%s err=%v", request.SiteID, err)
			return baseline.Recommend(ctx, request, at)
		}
		return rec, nil
	}

	parsed, err := url.Parse(servingRef)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		adapter.logf("solver_scheme_rejected_fallback: site=%s ref=%s", request.SiteID, servingRef)
		return baseline.Recommend(ctx, request, at)
	}
	if !isAllowedSolverHost(parsed, adapter.AllowedHosts) {
		adapter.logf("solver_host_not_allowlisted_fallback: site=%s host=%s", request.SiteID, parsed.Hostname())
		return baseline.Recommend(ctx, request, at)
	}

	rec, fbStatus, err := intelligencemodel.InvokeWithBumplessFallback(
		ctx,
		adapter.resilienceConfig(),
		func(callCtx context.Context) (Recommendation, error) {
			return adapter.invokeRemoteHTTP(callCtx, servingRef, request, at)
		},
		func(fbCtx context.Context, cause error) (Recommendation, error) {
			adapter.logf("remote_solver_bumpless_fallback: site=%s cause=%v", request.SiteID, cause)
			return baseline.Recommend(fbCtx, request, at)
		},
	)
	if err != nil {
		return Recommendation{}, err
	}
	if fbStatus.FallbackUsed {
		adapter.logf("remote_solver_fallback_completed: site=%s reason=%s", request.SiteID, fbStatus.Reason)
	}
	return rec, nil
}

type RemoteAuxiliaryTarget struct {
	LoopKey     string  `json:"loopKey"`
	Capability  string  `json:"capability"`
	TargetValue float64 `json:"targetValue"`
	Unit        string  `json:"unit"`
	Rationale   string  `json:"rationale,omitempty"`
}

type remoteSolverResponse struct {
	ID                 string                                            `json:"id,omitempty"`
	TenantID           string                                            `json:"tenantId,omitempty"`
	SiteID             string                                            `json:"siteId,omitempty"`
	InputSnapshotID    string                                            `json:"inputSnapshotId,omitempty"`
	DeploymentRevision string                                            `json:"deploymentRevisionId,omitempty"`
	Baseline           *intelligencemodel.RecommendationBaseline         `json:"baseline,omitempty"`
	Objective          *intelligencemodel.RecommendationObjective        `json:"objective,omitempty"`
	Constraints        []intelligencemodel.RecommendationConstraint      `json:"constraints,omitempty"`
	Candidate          *intelligencemodel.RecommendationCandidate        `json:"candidate,omitempty"`
	SupplyTempC        *float64                                          `json:"supplyTempC,omitempty"`
	ProjectedZoneTempC *float64                                          `json:"projectedZoneTempC,omitempty"`
	DailyEnergyKWh     *float64                                          `json:"dailyEnergyKWh,omitempty"`
	DailyCost          *float64                                          `json:"dailyCost,omitempty"`
	ExpectedImpact     *intelligencemodel.RecommendationExpectedImpact   `json:"expectedImpact,omitempty"`
	Uncertainty        *intelligencemodel.RecommendationUncertainty      `json:"uncertainty,omitempty"`
	Risk               *intelligencemodel.RecommendationRisk             `json:"risk,omitempty"`
	RollbackPlan       *intelligencemodel.RecommendationRollbackPlan     `json:"rollbackPlan,omitempty"`
	VerificationPlan   *intelligencemodel.RecommendationVerificationPlan `json:"verificationPlan,omitempty"`
	Approval           string                                            `json:"approval,omitempty"`
	Revalidation       *intelligencemodel.CurrentStateRevalidation       `json:"currentStateRevalidation,omitempty"`
	CommandIntentID    string                                            `json:"commandIntentId,omitempty"`
	ValidUntil         *time.Time                                        `json:"validUntil,omitempty"`
	CreatedAt          *time.Time                                        `json:"createdAt,omitempty"`
	SolverVersion      string                                            `json:"solverVersion,omitempty"`
	AuxiliaryTargets   []RemoteAuxiliaryTarget                           `json:"auxiliaryTargets,omitempty"`
}

func (adapter *RoutingSolverAdapter) invokeRemoteHTTP(ctx context.Context, endpoint string, request Request, at time.Time) (Recommendation, error) {
	ctx, _, _ = intelligencemodel.EnsureTraceContext(ctx, "")
	payload, err := json.Marshal(request)
	if err != nil {
		return Recommendation{}, fmt.Errorf("encode remote solver request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Recommendation{}, fmt.Errorf("build remote solver request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	intelligencemodel.InjectTraceparent(ctx, httpReq.Header)

	resp, err := adapter.httpClient().Do(httpReq)
	if err != nil {
		return Recommendation{}, fmt.Errorf("remote solver HTTP call failed: %w", err)
	}
	defer drainAndClose(resp.Body)

	body, err := io.ReadAll(io.LimitReader(resp.Body, maximumRemoteSolverResponseBytes+1))
	if err != nil {
		return Recommendation{}, fmt.Errorf("read remote solver response: %w", err)
	}
	if int64(len(body)) > maximumRemoteSolverResponseBytes {
		return Recommendation{}, errors.New("remote solver response exceeds maximum size")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Recommendation{}, fmt.Errorf("remote solver returned HTTP %d", resp.StatusCode)
	}
	return decodeRemoteRecommendation(body, request, at)
}

func decodeRemoteRecommendation(body []byte, request Request, at time.Time) (Recommendation, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var raw remoteSolverResponse
	if err := decoder.Decode(&raw); err != nil {
		return Recommendation{}, fmt.Errorf("decode remote solver response: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Recommendation{}, fmt.Errorf("remote solver response trailing data: %w", err)
	}

	var candidate intelligencemodel.RecommendationCandidate
	switch {
	case raw.Candidate != nil:
		candidate = *raw.Candidate
	case raw.SupplyTempC != nil:
		candidate.SupplyTempC = *raw.SupplyTempC
		delta := candidate.SupplyTempC - request.Baseline.SupplyTempC
		if raw.ProjectedZoneTempC != nil {
			candidate.ProjectedZoneTempC = *raw.ProjectedZoneTempC
		} else {
			candidate.ProjectedZoneTempC = request.Baseline.ZoneTempC + request.ResponseModel.ZoneTempDeltaPerSupplyTempC*delta
		}
		if raw.DailyEnergyKWh != nil {
			candidate.DailyEnergyKWh = *raw.DailyEnergyKWh
		} else {
			candidate.DailyEnergyKWh = math.Max(0, request.Baseline.DailyEnergyKWh+request.ResponseModel.DailyEnergyDeltaPerSupplyTempC*delta)
		}
		if raw.DailyCost != nil {
			candidate.DailyCost = *raw.DailyCost
		} else {
			costPerKWh := request.Baseline.DailyCost / request.Baseline.DailyEnergyKWh
			candidate.DailyCost = candidate.DailyEnergyKWh * costPerKWh
		}
	default:
		return Recommendation{}, errors.New("remote solver response missing candidate supplyTempC")
	}

	if err := validateRemoteCandidateBounds(candidate, request); err != nil {
		return Recommendation{}, err
	}
	if err := validateRemoteAuxiliaryTargets(raw.AuxiliaryTargets); err != nil {
		return Recommendation{}, err
	}

	recID := deterministicV7(request.OptimizationRunID, "recommendation:1", request.ValidFrom.UTC())
	if isUUIDv7(raw.ID) {
		recID = strings.TrimSpace(raw.ID)
	}

	expectedImpact := intelligencemodel.RecommendationExpectedImpact{
		EnergySavingKWhPerDay: request.Baseline.DailyEnergyKWh - candidate.DailyEnergyKWh,
		CostSavingPerDay:      request.Baseline.DailyCost - candidate.DailyCost,
	}
	if raw.ExpectedImpact != nil {
		if !finite(raw.ExpectedImpact.EnergySavingKWhPerDay) || !finite(raw.ExpectedImpact.CostSavingPerDay) {
			return Recommendation{}, errors.New("remote solver expectedImpact contains non-finite value")
		}
		if math.Abs(raw.ExpectedImpact.EnergySavingKWhPerDay-expectedImpact.EnergySavingKWhPerDay) > 1.0 ||
			math.Abs(raw.ExpectedImpact.CostSavingPerDay-expectedImpact.CostSavingPerDay) > 1.0 {
			return Recommendation{}, errors.New("remote solver expectedImpact inconsistent with candidate delta")
		}
		expectedImpact = *raw.ExpectedImpact
	}

	uncertainty := intelligencemodel.RecommendationUncertainty{
		EnergyP90KWh: request.ResponseModel.EnergyUncertaintyP90KWh,
		ZoneTempP90C: request.ResponseModel.ZoneTempUncertaintyP90C,
	}
	if raw.Uncertainty != nil {
		uncertainty = *raw.Uncertainty
	}

	zoneLower := candidate.ProjectedZoneTempC - uncertainty.ZoneTempP90C
	zoneUpper := candidate.ProjectedZoneTempC + uncertainty.ZoneTempP90C
	if zoneLower < request.Constraints.ZoneTempMinC || zoneUpper > request.Constraints.ZoneTempMaxC {
		return Recommendation{}, errors.New("remote solver candidate violates frozen comfort uncertainty bounds")
	}
	zoneMarginC := math.Min(zoneLower-request.Constraints.ZoneTempMinC, request.Constraints.ZoneTempMaxC-zoneUpper)
	riskLevel := "LOW"
	if zoneMarginC <= math.Max(0.5, 2*uncertainty.ZoneTempP90C) {
		riskLevel = "MEDIUM"
	}
	risk := intelligencemodel.RecommendationRisk{Level: riskLevel, ComfortMarginC: zoneMarginC}
	if raw.Risk != nil && strings.TrimSpace(raw.Risk.Level) != "" && finite(raw.Risk.ComfortMarginC) {
		normalizedLevel := strings.ToUpper(strings.TrimSpace(raw.Risk.Level))
		switch normalizedLevel {
		case "LOW", "MEDIUM", "HIGH":
		default:
			return Recommendation{}, fmt.Errorf("remote solver risk.level %q is invalid", raw.Risk.Level)
		}
		if riskLevel == "MEDIUM" && normalizedLevel == "LOW" {
			normalizedLevel = "MEDIUM"
		}
		margin := raw.Risk.ComfortMarginC
		if margin > zoneMarginC {
			margin = zoneMarginC
		}
		risk = intelligencemodel.RecommendationRisk{
			Level:          normalizedLevel,
			ComfortMarginC: margin,
		}
	}

	rollbackPlan := standardRollbackPlan(request.Baseline.SupplyTempC)
	if raw.RollbackPlan != nil && (raw.RollbackPlan.RestoreSupplyTempC != nil || len(raw.RollbackPlan.Conditions) > 0) {
		rollbackPlan = *raw.RollbackPlan
	}
	verificationPlan := standardVerificationPlan()
	if raw.VerificationPlan != nil && raw.VerificationPlan.WindowMinutes > 0 {
		verificationPlan = *raw.VerificationPlan
	}

	// Server always enforces tenant/site provenance, frozen baseline/constraints, and DRAFT approval state.
	recommendation := Recommendation{
		ID:                 recID,
		TenantID:           request.TenantID,
		SiteID:             request.SiteID,
		InputSnapshotID:    request.InputSnapshotID,
		DeploymentRevision: request.DeploymentRevisionID,
		Baseline: intelligencemodel.RecommendationBaseline{
			DailyEnergyKWh: request.Baseline.DailyEnergyKWh,
			DailyCost:      request.Baseline.DailyCost,
			SupplyTempC:    request.Baseline.SupplyTempC,
			ZoneTempC:      request.Baseline.ZoneTempC,
		},
		Objective: intelligencemodel.RecommendationObjective{
			Kind:           request.Objective,
			Horizon:        request.Horizon,
			HorizonMinutes: request.HorizonMinutes,
		},
		Constraints:      frozenRecommendationConstraints(request.Constraints),
		Candidate:        candidate,
		AuxiliaryTargets: toModelAuxiliaryTargets(raw.AuxiliaryTargets),
		ExpectedImpact:   expectedImpact,
		Uncertainty:      uncertainty,
		Risk:             risk,
		RollbackPlan:     rollbackPlan,
		VerificationPlan: verificationPlan,
		Approval:         intelligencemodel.RecommendationDraft,
		ValidUntil:       timePointer(at.UTC().Add(intelligencemodel.DefaultRecommendationTTL)),
		CreatedAt:        at.UTC(),
	}
	if err := recommendation.ValidateForApproval(); err != nil {
		return Recommendation{}, err
	}
	return recommendation, nil
}

func validateRemoteCandidateBounds(candidate intelligencemodel.RecommendationCandidate, request Request) error {
	if !finite(candidate.SupplyTempC) || !finite(candidate.ProjectedZoneTempC) ||
		!finiteNonNegative(candidate.DailyEnergyKWh) || !finiteNonNegative(candidate.DailyCost) {
		return errors.New("remote solver candidate contains invalid numeric fields")
	}
	if candidate.SupplyTempC < request.Constraints.SupplyTempMinC || candidate.SupplyTempC > request.Constraints.SupplyTempMaxC {
		return errors.New("remote solver supplyTempC violates frozen safety bounds")
	}
	if math.Abs(candidate.SupplyTempC-request.Baseline.SupplyTempC) > request.Constraints.MaxSupplyTempStep+1e-9 {
		return errors.New("remote solver supplyTempC exceeds maxSupplyTempStepC")
	}
	return nil
}

func validateRemoteAuxiliaryTargets(targets []RemoteAuxiliaryTarget) error {
	for _, target := range targets {
		if strings.TrimSpace(target.LoopKey) == "" {
			return errors.New("remote solver auxiliary target loopKey is required")
		}
		if !finite(target.TargetValue) {
			return fmt.Errorf("remote solver auxiliary target %s value must be finite", target.LoopKey)
		}
		switch target.Capability {
		case "SET_FREQUENCY":
			if target.Unit != "Hz" || target.TargetValue < 15.0 || target.TargetValue > 60.0 {
				return fmt.Errorf("remote solver auxiliary frequency target out of bounds: %.2f %s", target.TargetValue, target.Unit)
			}
		case "SET_FAN_SPEED":
			if target.Unit != "%" || target.TargetValue < 0.0 || target.TargetValue > 100.0 {
				return fmt.Errorf("remote solver auxiliary fan speed target out of bounds: %.2f %s", target.TargetValue, target.Unit)
			}
		default:
			return fmt.Errorf("remote solver unsupported auxiliary capability %q", target.Capability)
		}
	}
	return nil
}

func toModelAuxiliaryTargets(targets []RemoteAuxiliaryTarget) []intelligencemodel.OptimizationAuxiliaryTarget {
	if len(targets) == 0 {
		return nil
	}
	out := make([]intelligencemodel.OptimizationAuxiliaryTarget, 0, len(targets))
	for _, target := range targets {
		out = append(out, intelligencemodel.OptimizationAuxiliaryTarget{
			LoopKey:     strings.TrimSpace(target.LoopKey),
			Capability:  strings.TrimSpace(target.Capability),
			TargetValue: target.TargetValue,
			Unit:        strings.TrimSpace(target.Unit),
			Rationale:   strings.TrimSpace(target.Rationale),
		})
	}
	return out
}

func isUUIDv7(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return uuidPattern.MatchString(trimmed) && len(trimmed) == 36 && trimmed[14] == '7'
}
