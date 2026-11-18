package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	lifecycleAuditSourceType = "DATA_LIFECYCLE_POLICY"
	lifecycleAuditDedupPref  = "lifecycle-audit:"
)

type LegalHoldScope struct {
	ID            string
	TenantID      string
	SiteID        *string
	DatasetCode   *string
	ScopeType     string
	ResourceKey   *string
	Reason        string
	Status        string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
}

type LifecyclePolicyRecord struct {
	ID               string
	TenantID         string
	DatasetCode      string
	DataClass        string
	HotRetentionDays int
	ArchiveAfterDays *int
	DeleteAfterDays  *int
	ArchiveRequired  bool
	EffectiveFrom    time.Time
	EffectiveTo      *time.Time
	Status           string
	Revision         int64
}

type LifecycleAuditFinding struct {
	TenantID   string
	PolicyID   string
	Code       string
	Severity   string
	ActionCode string
	DedupKey   string
	Details    map[string]any
}

type LifecycleAuditSummary struct {
	PoliciesChecked  int
	ActiveLegalHolds int
	FindingsCount    int
	EventsResolved   int
}

type rowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (s *Store) ActiveLegalHoldScopes(ctx context.Context, now time.Time) ([]LegalHoldScope, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("maintenance PostgreSQL store is closed")
	}
	return loadActiveLegalHoldScopes(ctx, s.pool, normalizeAuditTime(now))
}

func (s *Store) AuditLifecycleGovernance(ctx context.Context, now time.Time) (LifecycleAuditSummary, error) {
	if s == nil || s.pool == nil {
		return LifecycleAuditSummary{}, errors.New("maintenance PostgreSQL store is closed")
	}
	auditTime := normalizeAuditTime(now)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LifecycleAuditSummary{}, err
	}
	defer tx.Rollback(ctx)

	policies, err := loadReleasedLifecyclePolicies(ctx, tx, auditTime)
	if err != nil {
		return LifecycleAuditSummary{}, err
	}
	holds, err := loadActiveLegalHoldScopes(ctx, tx, auditTime)
	if err != nil {
		return LifecycleAuditSummary{}, err
	}
	findings := EvaluateLifecycleGovernance(policies, holds, auditTime)

	activeKeys := make([]string, 0, len(findings))
	for _, finding := range findings {
		if err = upsertLifecycleAuditEvent(ctx, tx, finding, auditTime); err != nil {
			return LifecycleAuditSummary{}, err
		}
		activeKeys = append(activeKeys, finding.DedupKey)
	}

	// Resolve prior lifecycle-audit events once their policy inconsistency or hold conflict clears.
	tag, err := tx.Exec(ctx, `UPDATE core_registry.maintenance_events
SET status = 'RESOLVED',
    resolved_at = GREATEST(detected_at, $1),
    updated_at = GREATEST(created_at, $1)
WHERE source_type = $2
  AND dedup_key LIKE 'lifecycle-audit:%'
  AND status <> 'RESOLVED'
  AND NOT (dedup_key = ANY(COALESCE($3::text[], ARRAY[]::text[])))`,
		auditTime, lifecycleAuditSourceType, activeKeys)
	if err != nil {
		return LifecycleAuditSummary{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LifecycleAuditSummary{}, err
	}
	return LifecycleAuditSummary{
		PoliciesChecked:  len(policies),
		ActiveLegalHolds: len(holds),
		FindingsCount:    len(findings),
		EventsResolved:   int(tag.RowsAffected()),
	}, nil
}

func EvaluateLifecycleGovernance(policies []LifecyclePolicyRecord, holds []LegalHoldScope, now time.Time) []LifecycleAuditFinding {
	auditTime := normalizeAuditTime(now)
	activeHolds := filterActiveLegalHolds(holds, auditTime)
	overlappingIDs := detectOverlappingReleasedPolicies(policies, auditTime)

	var findings []LifecycleAuditFinding
	for _, policy := range policies {
		if policy.Status != "RELEASED" {
			continue
		}
		if policy.EffectiveTo != nil && !policy.EffectiveTo.After(auditTime) {
			continue
		}
		findings = append(findings, evaluatePolicyFindings(policy, activeHolds, overlappingIDs[policy.ID], auditTime)...)
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].TenantID != findings[j].TenantID {
			return findings[i].TenantID < findings[j].TenantID
		}
		if findings[i].PolicyID != findings[j].PolicyID {
			return findings[i].PolicyID < findings[j].PolicyID
		}
		return findings[i].Code < findings[j].Code
	})
	return findings
}

func evaluatePolicyFindings(policy LifecyclePolicyRecord, activeHolds []LegalHoldScope, overlaps bool, now time.Time) []LifecycleAuditFinding {
	var findings []LifecycleAuditFinding
	addFinding := func(code, severity, actionCode string, extra map[string]any) {
		details := map[string]any{
			"datasetCode":      policy.DatasetCode,
			"dataClass":        policy.DataClass,
			"revision":         policy.Revision,
			"findingCode":      code,
			"hotRetentionDays": policy.HotRetentionDays,
			"archiveRequired":  policy.ArchiveRequired,
		}
		if policy.ArchiveAfterDays != nil {
			details["archiveAfterDays"] = *policy.ArchiveAfterDays
		}
		if policy.DeleteAfterDays != nil {
			details["deleteAfterDays"] = *policy.DeleteAfterDays
		}
		for k, v := range extra {
			details[k] = v
		}
		findings = append(findings, LifecycleAuditFinding{
			TenantID:   policy.TenantID,
			PolicyID:   policy.ID,
			Code:       code,
			Severity:   severity,
			ActionCode: actionCode,
			DedupKey:   fmt.Sprintf("%s%s:%s", lifecycleAuditDedupPref, policy.ID, code),
			Details:    details,
		})
	}

	if policy.EffectiveTo != nil && !policy.EffectiveTo.After(policy.EffectiveFrom) {
		addFinding("INVALID_EFFECTIVE_WINDOW", "CRITICAL", "FIX_LIFECYCLE_POLICY", nil)
	}
	if policy.HotRetentionDays < 0 || (policy.ArchiveAfterDays != nil && *policy.ArchiveAfterDays < 0) || (policy.DeleteAfterDays != nil && *policy.DeleteAfterDays < 0) {
		addFinding("NEGATIVE_RETENTION_WINDOW", "CRITICAL", "FIX_LIFECYCLE_POLICY", nil)
	}
	if policy.ArchiveRequired && policy.ArchiveAfterDays == nil {
		addFinding("ARCHIVE_AFTER_DAYS_MISSING", "CRITICAL", "FIX_LIFECYCLE_POLICY", nil)
	}
	if policy.ArchiveAfterDays != nil && *policy.ArchiveAfterDays < policy.HotRetentionDays {
		addFinding("ARCHIVE_BEFORE_HOT_RETENTION", "CRITICAL", "FIX_LIFECYCLE_POLICY", nil)
	}
	if policy.DeleteAfterDays != nil && *policy.DeleteAfterDays < policy.HotRetentionDays {
		addFinding("DELETE_BEFORE_HOT_RETENTION", "CRITICAL", "FIX_LIFECYCLE_POLICY", nil)
	}
	if policy.ArchiveAfterDays != nil && policy.DeleteAfterDays != nil && *policy.DeleteAfterDays < *policy.ArchiveAfterDays {
		addFinding("DELETE_BEFORE_ARCHIVE", "CRITICAL", "FIX_LIFECYCLE_POLICY", nil)
	}
	if overlaps {
		addFinding("OVERLAPPING_RELEASED_POLICY", "CRITICAL", "FIX_LIFECYCLE_POLICY", nil)
	}

	zeroDelete := policy.DeleteAfterDays != nil && *policy.DeleteAfterDays == 0
	zeroHot := policy.HotRetentionDays == 0
	if zeroDelete || zeroHot {
		severity := "WARNING"
		if zeroDelete || policy.DataClass == "CRITICAL" || policy.DataClass == "IMPORTANT" {
			severity = "CRITICAL"
		}
		addFinding("ZERO_RETENTION_RISK", severity, "REVIEW_ZERO_RETENTION_POLICY", map[string]any{
			"zeroHotRetention":    zeroHot,
			"zeroDeleteRetention": zeroDelete,
		})
	}

	if policy.DeleteAfterDays != nil && !policy.EffectiveFrom.After(now) && (policy.EffectiveTo == nil || policy.EffectiveTo.After(now)) {
		matchingHolds := matchingLegalHoldsForPolicy(policy, activeHolds)
		if len(matchingHolds) > 0 {
			holdIDs := make([]string, 0, len(matchingHolds))
			scopeTypes := make([]string, 0, len(matchingHolds))
			tenantWideBlocked := false
			for _, hold := range matchingHolds {
				holdIDs = append(holdIDs, hold.ID)
				if !slices.Contains(scopeTypes, hold.ScopeType) {
					scopeTypes = append(scopeTypes, hold.ScopeType)
				}
				if hold.ScopeType == "TENANT" {
					tenantWideBlocked = true
				}
			}
			slices.Sort(holdIDs)
			slices.Sort(scopeTypes)
			addFinding("LEGAL_HOLD_BLOCKED", "WARNING", "REVIEW_LEGAL_HOLD_CONFLICT", map[string]any{
				"holdIds":           holdIDs,
				"scopeTypes":        scopeTypes,
				"tenantWideBlocked": tenantWideBlocked,
			})
		}
	}

	return findings
}

func detectOverlappingReleasedPolicies(policies []LifecyclePolicyRecord, now time.Time) map[string]bool {
	type scopeKey struct {
		tenantID    string
		datasetCode string
		dataClass   string
	}
	grouped := make(map[scopeKey][]LifecyclePolicyRecord)
	for _, policy := range policies {
		if policy.Status != "RELEASED" {
			continue
		}
		if policy.EffectiveTo != nil && !policy.EffectiveTo.After(now) {
			continue
		}
		key := scopeKey{tenantID: policy.TenantID, datasetCode: policy.DatasetCode, dataClass: policy.DataClass}
		grouped[key] = append(grouped[key], policy)
	}
	overlaps := make(map[string]bool)
	for _, group := range grouped {
		if len(group) < 2 {
			continue
		}
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				if policyWindowsOverlap(group[i], group[j]) {
					overlaps[group[i].ID] = true
					overlaps[group[j].ID] = true
				}
			}
		}
	}
	return overlaps
}

func policyWindowsOverlap(a, b LifecyclePolicyRecord) bool {
	if a.EffectiveTo != nil && !a.EffectiveTo.After(b.EffectiveFrom) {
		return false
	}
	if b.EffectiveTo != nil && !b.EffectiveTo.After(a.EffectiveFrom) {
		return false
	}
	return true
}

func filterActiveLegalHolds(holds []LegalHoldScope, now time.Time) []LegalHoldScope {
	active := make([]LegalHoldScope, 0, len(holds))
	for _, hold := range holds {
		if hold.Status != "" && hold.Status != "ACTIVE" {
			continue
		}
		if hold.EffectiveFrom.After(now) {
			continue
		}
		if hold.EffectiveTo != nil && !hold.EffectiveTo.After(now) {
			continue
		}
		active = append(active, hold)
	}
	return active
}

func matchingLegalHoldsForPolicy(policy LifecyclePolicyRecord, holds []LegalHoldScope) []LegalHoldScope {
	var matched []LegalHoldScope
	for _, hold := range holds {
		if hold.TenantID != policy.TenantID {
			continue
		}
		if hold.DatasetCode != nil && strings.TrimSpace(*hold.DatasetCode) != "" && *hold.DatasetCode != policy.DatasetCode {
			continue
		}
		matched = append(matched, hold)
	}
	return matched
}

func loadActiveLegalHoldScopes(ctx context.Context, querier rowQuerier, now time.Time) ([]LegalHoldScope, error) {
	rows, err := querier.Query(ctx, `SELECT id::text, tenant_id::text, site_id::text, dataset_code, scope_type, resource_key, reason, status, effective_from, effective_to
FROM core_registry.legal_holds
WHERE status = 'ACTIVE'
  AND effective_from <= $1
  AND (effective_to IS NULL OR effective_to > $1)
ORDER BY effective_from ASC, id ASC`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var holds []LegalHoldScope
	for rows.Next() {
		var hold LegalHoldScope
		if err = rows.Scan(
			&hold.ID,
			&hold.TenantID,
			&hold.SiteID,
			&hold.DatasetCode,
			&hold.ScopeType,
			&hold.ResourceKey,
			&hold.Reason,
			&hold.Status,
			&hold.EffectiveFrom,
			&hold.EffectiveTo,
		); err != nil {
			return nil, err
		}
		holds = append(holds, hold)
	}
	return holds, rows.Err()
}

func loadReleasedLifecyclePolicies(ctx context.Context, querier rowQuerier, now time.Time) ([]LifecyclePolicyRecord, error) {
	rows, err := querier.Query(ctx, `SELECT id::text, tenant_id::text, dataset_code, data_class,
       hot_retention_days, archive_after_days, delete_after_days,
       archive_required, effective_from, effective_to, status, revision
FROM core_registry.data_lifecycle_policies
WHERE status = 'RELEASED'
  AND (effective_to IS NULL OR effective_to > $1)
ORDER BY tenant_id ASC, dataset_code ASC, data_class ASC, effective_from ASC, id ASC`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []LifecyclePolicyRecord
	for rows.Next() {
		var policy LifecyclePolicyRecord
		if err = rows.Scan(
			&policy.ID,
			&policy.TenantID,
			&policy.DatasetCode,
			&policy.DataClass,
			&policy.HotRetentionDays,
			&policy.ArchiveAfterDays,
			&policy.DeleteAfterDays,
			&policy.ArchiveRequired,
			&policy.EffectiveFrom,
			&policy.EffectiveTo,
			&policy.Status,
			&policy.Revision,
		); err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}

func upsertLifecycleAuditEvent(ctx context.Context, tx pgx.Tx, finding LifecycleAuditFinding, now time.Time) error {
	eventID, err := uuidv7(now)
	if err != nil {
		return err
	}
	encodedDetails, err := json.Marshal(finding.Details)
	if err != nil {
		return err
	}
	// maintenance_events enforces event_type IN ('CREDENTIAL_EXPIRY','DEAD_WORK','RETIREMENT_INCOMPLETE');
	// source_type='DATA_LIFECYCLE_POLICY' and dedup_key='lifecycle-audit:*' distinguish governance findings.
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.maintenance_events(
  event_id, tenant_id, event_type, source_type, source_id, severity, status,
  action_code, dedup_key, details, detected_at, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, 'RETIREMENT_INCOMPLETE', $3, $4, $5, 'OPEN',
  $6, $7, $8::jsonb, $9, $9, $9
)
ON CONFLICT (dedup_key) DO UPDATE SET
  severity = EXCLUDED.severity,
  action_code = EXCLUDED.action_code,
  details = EXCLUDED.details,
  status = CASE
    WHEN core_registry.maintenance_events.status = 'RESOLVED' THEN 'OPEN'
    ELSE core_registry.maintenance_events.status
  END,
  acknowledged_at = CASE
    WHEN core_registry.maintenance_events.status = 'RESOLVED' THEN NULL
    ELSE core_registry.maintenance_events.acknowledged_at
  END,
  resolved_at = CASE
    WHEN core_registry.maintenance_events.status = 'RESOLVED' THEN NULL
    ELSE core_registry.maintenance_events.resolved_at
  END,
  detected_at = CASE
    WHEN core_registry.maintenance_events.status = 'RESOLVED' THEN EXCLUDED.detected_at
    ELSE core_registry.maintenance_events.detected_at
  END,
  updated_at = GREATEST(core_registry.maintenance_events.created_at, EXCLUDED.updated_at)`,
		eventID,
		finding.TenantID,
		lifecycleAuditSourceType,
		finding.PolicyID,
		finding.Severity,
		finding.ActionCode,
		finding.DedupKey,
		encodedDetails,
		now,
	)
	return err
}

func normalizeAuditTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}
