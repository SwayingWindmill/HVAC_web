package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const (
	CanaryStatusActive   = "ACTIVE"
	CanaryStatusDisabled = "DISABLED"

	SampleKindFilterAll        = "ALL"
	DefaultNegativeSampleLimit = 20
	MaxNegativeSampleLimit     = 100
)

var (
	ErrCanaryInvalidRequest = errors.New("optimization canary request invalid")
	ErrCanaryHostNotAllowed = errors.New("optimization solver host not allowlisted")
	canaryLoopKeyPattern    = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_]{0,63}$`)
)

// CanaryBindingRecord represents one persisted site-loop solver routing override in
// core_registry.optimization_canary_bindings.
type CanaryBindingRecord struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenantId"`
	SiteID     string    `json:"siteId"`
	LoopKey    string    `json:"loopKey"`
	ServingRef string    `json:"servingRef"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// CanaryBindingList returns all configured canary bindings for a Site alongside active solver
// resolution and allowlisted external solver hosts.
type CanaryBindingList struct {
	TenantID           string                `json:"tenantId"`
	SiteID             string                `json:"siteId"`
	AllowedSolverHosts []string              `json:"allowedSolverHosts"`
	ActiveSolver       ActiveSolverSelection `json:"activeSolver"`
	Items              []CanaryBindingRecord `json:"items"`
}

// UpsertCanaryBindingRequest validates operator-submitted canary binding parameters.
type UpsertCanaryBindingRequest struct {
	ServingRef string `json:"servingRef"`
	Status     string `json:"status"`
}

// DeleteCanaryBindingResult acknowledges deletion of a site-loop canary binding.
type DeleteCanaryBindingResult struct {
	Deleted bool   `json:"deleted"`
	SiteID  string `json:"siteId"`
	LoopKey string `json:"loopKey"`
}

// NegativeSampleFilter constrains paginated negative-sample queries for operator inspection.
type NegativeSampleFilter struct {
	SampleKind string
	From       time.Time
	To         time.Time
	Limit      int
	Offset     int
}

// NegativeSampleItem projects a single negative sample record for gateway JSON serialization.
type NegativeSampleItem struct {
	ID                    string                              `json:"id"`
	DecisionID            string                              `json:"decisionId"`
	RecommendationID      *string                             `json:"recommendationId"`
	MonitorID             *string                             `json:"monitorId"`
	TraceID               string                              `json:"traceId"`
	SampleKind            string                              `json:"sampleKind"`
	ReasonCode            string                              `json:"reasonCode"`
	SolverServingRef      string                              `json:"solverServingRef"`
	RawRecommendedValue   *float64                            `json:"rawRecommendedValue"`
	AppliedOrClampedValue *float64                            `json:"appliedOrClampedValue"`
	OccurredAt            time.Time                           `json:"occurredAt"`
	GuardrailTrace        []commandmodel.GuardrailStageResult `json:"guardrailTrace"`
	FeatureSnapshot       OptimizationFeatureSnapshot         `json:"featureSnapshot"`
}

// NegativeSamplePage returns a paginated slice of filtered flywheel negative samples.
type NegativeSamplePage struct {
	TenantID   string               `json:"tenantId"`
	SiteID     string               `json:"siteId"`
	WindowDays int                  `json:"windowDays"`
	SampleKind string               `json:"sampleKind"`
	Total      int                  `json:"total"`
	Limit      int                  `json:"limit"`
	Offset     int                  `json:"offset"`
	HasMore    bool                 `json:"hasMore"`
	Items      []NegativeSampleItem `json:"items"`
}

// CanaryBindingStore persists and retrieves site-level solver canary bindings.
type CanaryBindingStore interface {
	ListCanaryBindings(ctx context.Context, tenantID, siteID string) ([]CanaryBindingRecord, error)
	UpsertCanaryBinding(ctx context.Context, binding CanaryBindingRecord) (CanaryBindingRecord, error)
	DeleteCanaryBinding(ctx context.Context, tenantID, siteID, loopKey string) (bool, error)
}

// NegativeSamplePageStore queries negative samples with filter and offset pagination.
type NegativeSamplePageStore interface {
	QueryNegativeSamples(ctx context.Context, tenantID, siteID string, filter NegativeSampleFilter) ([]NegativeSampleRecord, int, error)
}

func (service *Service) effectiveCanaryStore() CanaryBindingStore {
	if service == nil {
		return nil
	}
	if service.canaryStore != nil {
		return service.canaryStore
	}
	if store, ok := service.publication.(CanaryBindingStore); ok {
		return store
	}
	return nil
}

func (service *Service) effectiveNegativeSamplePageStore() NegativeSamplePageStore {
	if service == nil {
		return nil
	}
	if service.flywheelStore != nil {
		if store, ok := service.flywheelStore.(NegativeSamplePageStore); ok {
			return store
		}
	}
	if store, ok := service.publication.(NegativeSamplePageStore); ok {
		return store
	}
	return nil
}

// NormalizeCanaryLoopKey validates and canonicalizes a control loop key for canary routing.
func NormalizeCanaryLoopKey(raw string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	if !canaryLoopKeyPattern.MatchString(normalized) {
		return "", fmt.Errorf("%w: invalid loop key", ErrCanaryInvalidRequest)
	}
	return normalized, nil
}

// NormalizeCanaryStatus validates a canary binding status.
func NormalizeCanaryStatus(raw string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	if trimmed == "" {
		return CanaryStatusActive, nil
	}
	switch trimmed {
	case CanaryStatusActive, CanaryStatusDisabled:
		return trimmed, nil
	default:
		return "", fmt.Errorf("%w: invalid status", ErrCanaryInvalidRequest)
	}
}

// NormalizeCanaryServingRef validates and canonicalizes a solver reference, enforcing the
// external solver host allowlist when servingRef is an http/https URL.
func NormalizeCanaryServingRef(raw string, allowedHosts []string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 512 || strings.ContainsAny(trimmed, " \t\r\n") {
		return "", fmt.Errorf("%w: invalid servingRef", ErrCanaryInvalidRequest)
	}
	if strings.Contains(trimmed, "@") {
		return "", fmt.Errorf("%w: userinfo in servingRef is forbidden", ErrCanaryInvalidRequest)
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "builtin://") {
		if lower != DefaultBuiltinSolverRef && lower != ChillerCOPBuiltinSolverRef {
			return "", fmt.Errorf("%w: unsupported builtin solver", ErrCanaryInvalidRequest)
		}
		return lower, nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil {
		return "", fmt.Errorf("%w: malformed servingRef URL", ErrCanaryInvalidRequest)
	}
	parsed.Scheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: unsupported servingRef scheme", ErrCanaryInvalidRequest)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return "", fmt.Errorf("%w: servingRef cannot contain userinfo, query, or fragment", ErrCanaryInvalidRequest)
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return "", fmt.Errorf("%w: missing servingRef host", ErrCanaryInvalidRequest)
	}
	if !isAllowedSolverHost(parsed, allowedHosts) {
		return "", fmt.Errorf("%w: host %q is not allowlisted", ErrCanaryHostNotAllowed, parsed.Hostname())
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

// ListCanaryBindings lists all canary solver bindings for a visible Site.
func (service *Service) ListCanaryBindings(ctx context.Context, tenantID, siteID string) (CanaryBindingList, error) {
	if service == nil {
		return CanaryBindingList{}, errors.New("optimization service is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return CanaryBindingList{}, fmt.Errorf("%w: invalid site scope", ErrCanaryInvalidRequest)
	}
	if checker := service.effectiveSiteVisibilityChecker(); checker != nil {
		visible, err := checker.CheckSiteVisible(ctx, tenant, site)
		if err != nil {
			return CanaryBindingList{}, err
		}
		if !visible {
			return CanaryBindingList{}, ErrOptimizationNotFound
		}
	}
	now := service.clock().UTC()
	activeSolver := ActiveSolverSelection{
		ServingRef: DefaultBuiltinSolverRef,
		Source:     SolverSourceDefault,
		LoopKey:    DefaultSolverLoopKey,
	}
	if resolver := service.effectiveSolverResolver(); resolver != nil {
		resolved, err := resolver.ResolveActiveSolver(ctx, tenant, site, DefaultSolverLoopKey, now)
		if err != nil {
			return CanaryBindingList{}, err
		}
		activeSolver = resolved
	}
	items := []CanaryBindingRecord{}
	if store := service.effectiveCanaryStore(); store != nil {
		rows, err := store.ListCanaryBindings(ctx, tenant, site)
		if err != nil {
			return CanaryBindingList{}, err
		}
		if len(rows) > 0 {
			items = rows
		}
	}
	hosts := make([]string, 0, len(service.allowedSolverHosts))
	hosts = append(hosts, service.allowedSolverHosts...)
	sort.Strings(hosts)
	return CanaryBindingList{
		TenantID:           tenant,
		SiteID:             site,
		AllowedSolverHosts: hosts,
		ActiveSolver:       activeSolver,
		Items:              items,
	}, nil
}

// UpsertCanaryBinding validates and upserts a site-loop canary binding.
func (service *Service) UpsertCanaryBinding(ctx context.Context, tenantID, siteID, loopKey string, input UpsertCanaryBindingRequest) (CanaryBindingRecord, error) {
	if service == nil {
		return CanaryBindingRecord{}, errors.New("optimization service is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return CanaryBindingRecord{}, fmt.Errorf("%w: invalid site scope", ErrCanaryInvalidRequest)
	}
	normLoop, err := NormalizeCanaryLoopKey(loopKey)
	if err != nil {
		return CanaryBindingRecord{}, err
	}
	normStatus, err := NormalizeCanaryStatus(input.Status)
	if err != nil {
		return CanaryBindingRecord{}, err
	}
	normRef, err := NormalizeCanaryServingRef(input.ServingRef, service.allowedSolverHosts)
	if err != nil {
		return CanaryBindingRecord{}, err
	}
	if checker := service.effectiveSiteVisibilityChecker(); checker != nil {
		visible, visErr := checker.CheckSiteVisible(ctx, tenant, site)
		if visErr != nil {
			return CanaryBindingRecord{}, visErr
		}
		if !visible {
			return CanaryBindingRecord{}, ErrOptimizationNotFound
		}
	}
	store := service.effectiveCanaryStore()
	if store == nil {
		return CanaryBindingRecord{}, errors.New("canary binding store is unavailable")
	}
	now := service.clock().UTC()
	return store.UpsertCanaryBinding(ctx, CanaryBindingRecord{
		ID:         deterministicV7("optimization_canary_binding", tenant+"|"+site+"|"+normLoop, now),
		TenantID:   tenant,
		SiteID:     site,
		LoopKey:    normLoop,
		ServingRef: normRef,
		Status:     normStatus,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
}

// DeleteCanaryBinding deletes a site-loop canary binding if present.
func (service *Service) DeleteCanaryBinding(ctx context.Context, tenantID, siteID, loopKey string) (DeleteCanaryBindingResult, error) {
	if service == nil {
		return DeleteCanaryBindingResult{}, errors.New("optimization service is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return DeleteCanaryBindingResult{}, fmt.Errorf("%w: invalid site scope", ErrCanaryInvalidRequest)
	}
	normLoop, err := NormalizeCanaryLoopKey(loopKey)
	if err != nil {
		return DeleteCanaryBindingResult{}, err
	}
	if checker := service.effectiveSiteVisibilityChecker(); checker != nil {
		visible, visErr := checker.CheckSiteVisible(ctx, tenant, site)
		if visErr != nil {
			return DeleteCanaryBindingResult{}, visErr
		}
		if !visible {
			return DeleteCanaryBindingResult{}, ErrOptimizationNotFound
		}
	}
	store := service.effectiveCanaryStore()
	if store == nil {
		return DeleteCanaryBindingResult{}, errors.New("canary binding store is unavailable")
	}
	deleted, err := store.DeleteCanaryBinding(ctx, tenant, site, normLoop)
	if err != nil {
		return DeleteCanaryBindingResult{}, err
	}
	if !deleted {
		return DeleteCanaryBindingResult{}, ErrOptimizationNotFound
	}
	return DeleteCanaryBindingResult{
		Deleted: true,
		SiteID:  site,
		LoopKey: normLoop,
	}, nil
}

// ListNegativeSamplesPage returns paginated negative samples filtered by windowDays and sampleKind.
func (service *Service) ListNegativeSamplesPage(ctx context.Context, tenantID, siteID string, windowDays int, sampleKind string, limit, offset int) (NegativeSamplePage, error) {
	if service == nil {
		return NegativeSamplePage{}, errors.New("optimization service is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return NegativeSamplePage{}, fmt.Errorf("%w: invalid site scope", ErrCanaryInvalidRequest)
	}
	if windowDays <= 0 {
		windowDays = DefaultGovernanceWindowDays
	} else if windowDays > MaxGovernanceWindowDays {
		return NegativeSamplePage{}, fmt.Errorf("%w: windowDays out of range", ErrCanaryInvalidRequest)
	}
	normKind, responseKind, err := parseSampleKindFilter(sampleKind)
	if err != nil {
		return NegativeSamplePage{}, err
	}
	if limit <= 0 {
		limit = DefaultNegativeSampleLimit
	} else if limit > MaxNegativeSampleLimit {
		return NegativeSamplePage{}, fmt.Errorf("%w: limit exceeds maximum", ErrCanaryInvalidRequest)
	}
	if offset < 0 {
		return NegativeSamplePage{}, fmt.Errorf("%w: offset cannot be negative", ErrCanaryInvalidRequest)
	}
	if checker := service.effectiveSiteVisibilityChecker(); checker != nil {
		visible, visErr := checker.CheckSiteVisible(ctx, tenant, site)
		if visErr != nil {
			return NegativeSamplePage{}, visErr
		}
		if !visible {
			return NegativeSamplePage{}, ErrOptimizationNotFound
		}
	}

	to := service.clock().UTC()
	from := to.Add(-time.Duration(windowDays) * 24 * time.Hour)
	var rows []NegativeSampleRecord
	var total int

	if pageStore := service.effectiveNegativeSamplePageStore(); pageStore != nil {
		rows, total, err = pageStore.QueryNegativeSamples(ctx, tenant, site, NegativeSampleFilter{
			SampleKind: normKind,
			From:       from,
			To:         to,
			Limit:      limit,
			Offset:     offset,
		})
		if err != nil {
			return NegativeSamplePage{}, err
		}
	} else if flywheel := service.effectiveFlywheelStore(); flywheel != nil {
		all, listErr := flywheel.ListNegativeSamples(ctx, tenant, site, from, to, 0)
		if listErr != nil {
			return NegativeSamplePage{}, listErr
		}
		rows, total = filterAndSliceNegativeSamples(all, normKind, limit, offset)
	}

	items := make([]NegativeSampleItem, 0, len(rows))
	for _, sample := range rows {
		items = append(items, toNegativeSampleItem(sample))
	}
	hasMore := offset+len(items) < total
	return NegativeSamplePage{
		TenantID:   tenant,
		SiteID:     site,
		WindowDays: windowDays,
		SampleKind: responseKind,
		Total:      total,
		Limit:      limit,
		Offset:     offset,
		HasMore:    hasMore,
		Items:      items,
	}, nil
}

func parseSampleKindFilter(raw string) (sqlFilter string, responseKind string, err error) {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	if trimmed == "" || trimmed == SampleKindFilterAll {
		return "", SampleKindFilterAll, nil
	}
	normalized := normalizeNegativeSampleKind(trimmed)
	if normalized == "" || normalized != trimmed {
		return "", "", fmt.Errorf("%w: unsupported sampleKind %q", ErrCanaryInvalidRequest, raw)
	}
	return normalized, normalized, nil
}

func filterAndSliceNegativeSamples(all []NegativeSampleRecord, kind string, limit, offset int) ([]NegativeSampleRecord, int) {
	filtered := make([]NegativeSampleRecord, 0, len(all))
	for _, item := range all {
		resolved := resolveNegativeSampleKind(item)
		if kind != "" && resolved != kind {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].OccurredAt.Equal(filtered[j].OccurredAt) {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].OccurredAt.After(filtered[j].OccurredAt)
	})
	total := len(filtered)
	if offset >= total {
		return nil, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return filtered[offset:end], total
}

func normalizeOptionalIDPtr(value *string) *string {
	if value == nil {
		return nil
	}
	return cleanStringPtr(*value)
}

func toNegativeSampleItem(sample NegativeSampleRecord) NegativeSampleItem {
	trace := []commandmodel.GuardrailStageResult{}
	if trimmed := bytes.TrimSpace(sample.GuardrailTrace); len(trimmed) > 0 && string(trimmed) != "null" {
		var parsed []commandmodel.GuardrailStageResult
		if err := json.Unmarshal(trimmed, &parsed); err == nil && parsed != nil {
			trace = parsed
		}
	}
	resolvedKind := resolveNegativeSampleKind(sample)
	if resolvedKind == "" {
		resolvedKind = NegativeSampleGuardrailVetoed
	}
	return NegativeSampleItem{
		ID:                    sample.ID,
		DecisionID:            sample.DecisionID,
		RecommendationID:      normalizeOptionalIDPtr(sample.RecommendationID),
		MonitorID:             normalizeOptionalIDPtr(sample.MonitorID),
		TraceID:               sample.TraceID,
		SampleKind:            resolvedKind,
		ReasonCode:            sample.ReasonCode,
		SolverServingRef:      sample.SolverServingRef,
		RawRecommendedValue:   sample.RawRecommendedValue,
		AppliedOrClampedValue: sample.AppliedOrClampedValue,
		OccurredAt:            sample.OccurredAt.UTC(),
		GuardrailTrace:        trace,
		FeatureSnapshot:       sample.FeatureSnapshot,
	}
}

// ListCanaryBindings reads configured canary solver bindings for a site.
func (store *PostgresStore) ListCanaryBindings(ctx context.Context, tenantID, siteID string) ([]CanaryBindingRecord, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return nil, errors.New("canary binding query scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenant, site); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, loop_key, serving_ref, status, created_at, updated_at
FROM core_registry.optimization_canary_bindings
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
ORDER BY CASE WHEN loop_key = 'PLANT' THEN 0 ELSE 1 END, loop_key ASC
`, tenant, site)
	if err != nil {
		return nil, fmt.Errorf("query canary bindings: %w", err)
	}
	defer rows.Close()

	bindings := []CanaryBindingRecord{}
	for rows.Next() {
		var row CanaryBindingRecord
		if scanErr := rows.Scan(
			&row.ID, &row.TenantID, &row.SiteID,
			&row.LoopKey, &row.ServingRef, &row.Status,
			&row.CreatedAt, &row.UpdatedAt,
		); scanErr != nil {
			return nil, scanErr
		}
		row.CreatedAt = row.CreatedAt.UTC()
		row.UpdatedAt = row.UpdatedAt.UTC()
		bindings = append(bindings, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return bindings, nil
}

// UpsertCanaryBinding inserts or updates one site-loop canary binding.
func (store *PostgresStore) UpsertCanaryBinding(ctx context.Context, binding CanaryBindingRecord) (CanaryBindingRecord, error) {
	if store == nil || store.pool == nil {
		return CanaryBindingRecord{}, errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(binding.TenantID)
	site := strings.TrimSpace(binding.SiteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return CanaryBindingRecord{}, errors.New("canary binding mutation scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CanaryBindingRecord{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenant, site); err != nil {
		return CanaryBindingRecord{}, err
	}

	var saved CanaryBindingRecord
	err = tx.QueryRow(ctx, `
INSERT INTO core_registry.optimization_canary_bindings (
  id, tenant_id, site_id, loop_key, serving_ref, status, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8
)
ON CONFLICT (tenant_id, site_id, loop_key) DO UPDATE
SET serving_ref = EXCLUDED.serving_ref,
    status = EXCLUDED.status,
    updated_at = EXCLUDED.updated_at
RETURNING id::text, tenant_id::text, site_id::text, loop_key, serving_ref, status, created_at, updated_at
`, binding.ID, tenant, site, binding.LoopKey, binding.ServingRef, binding.Status, binding.CreatedAt.UTC(), binding.UpdatedAt.UTC()).Scan(
		&saved.ID, &saved.TenantID, &saved.SiteID,
		&saved.LoopKey, &saved.ServingRef, &saved.Status,
		&saved.CreatedAt, &saved.UpdatedAt,
	)
	if err != nil {
		return CanaryBindingRecord{}, fmt.Errorf("upsert canary binding: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return CanaryBindingRecord{}, err
	}
	saved.CreatedAt = saved.CreatedAt.UTC()
	saved.UpdatedAt = saved.UpdatedAt.UTC()
	return saved, nil
}

// DeleteCanaryBinding removes a site-loop canary binding and returns whether a row was deleted.
func (store *PostgresStore) DeleteCanaryBinding(ctx context.Context, tenantID, siteID, loopKey string) (bool, error) {
	if store == nil || store.pool == nil {
		return false, errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return false, errors.New("canary binding deletion scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenant, site); err != nil {
		return false, err
	}

	tag, err := tx.Exec(ctx, `
DELETE FROM core_registry.optimization_canary_bindings
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND loop_key = $3
`, tenant, site, loopKey)
	if err != nil {
		return false, fmt.Errorf("delete canary binding: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// QueryNegativeSamples reads filtered and paginated negative samples alongside total matching count.
func (store *PostgresStore) QueryNegativeSamples(ctx context.Context, tenantID, siteID string, filter NegativeSampleFilter) ([]NegativeSampleRecord, int, error) {
	if store == nil || store.pool == nil {
		return nil, 0, errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return nil, 0, errors.New("negative sample query scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenant, site); err != nil {
		return nil, 0, err
	}

	var total int
	err = tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM core_registry.optimization_negative_samples
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND occurred_at >= $3
  AND occurred_at <= $4
  AND ($5 = '' OR sample_kind = $5)
`, tenant, site, filter.From.UTC(), filter.To.UTC(), filter.SampleKind).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count negative samples: %w", err)
	}

	rows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text,
       CASE WHEN recommendation_id IS NULL THEN NULL ELSE recommendation_id::text END,
       CASE WHEN monitor_id IS NULL THEN NULL ELSE monitor_id::text END,
       COALESCE(decision_id::text, ''), COALESCE(trace_id, ''),
       sample_kind, COALESCE(reason_code, ''), COALESCE(solver_serving_ref, ''),
       raw_recommended_value, applied_or_clamped_value,
       feature_snapshot, guardrail_trace, occurred_at
FROM core_registry.optimization_negative_samples
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND occurred_at >= $3
  AND occurred_at <= $4
  AND ($5 = '' OR sample_kind = $5)
ORDER BY occurred_at DESC, id DESC
LIMIT $6 OFFSET $7
`, tenant, site, filter.From.UTC(), filter.To.UTC(), filter.SampleKind, filter.Limit, filter.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query paginated negative samples: %w", err)
	}
	defer rows.Close()

	samples := []NegativeSampleRecord{}
	for rows.Next() {
		var item NegativeSampleRecord
		var snapshotBytes []byte
		if scanErr := rows.Scan(
			&item.ID, &item.TenantID, &item.SiteID,
			&item.RecommendationID, &item.MonitorID,
			&item.DecisionID, &item.TraceID,
			&item.SampleKind, &item.ReasonCode, &item.SolverServingRef,
			&item.RawRecommendedValue, &item.AppliedOrClampedValue,
			&snapshotBytes, &item.GuardrailTrace, &item.OccurredAt,
		); scanErr != nil {
			return nil, 0, scanErr
		}
		if len(snapshotBytes) > 0 && string(snapshotBytes) != "null" {
			if unmarshalErr := json.Unmarshal(snapshotBytes, &item.FeatureSnapshot); unmarshalErr != nil {
				return nil, 0, fmt.Errorf("decode negative sample feature snapshot: %w", unmarshalErr)
			}
		}
		item.OccurredAt = item.OccurredAt.UTC()
		samples = append(samples, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return samples, total, nil
}
