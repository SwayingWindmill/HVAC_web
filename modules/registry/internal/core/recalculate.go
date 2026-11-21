package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	AccountingRecalcGranularityHour  = "HOUR"
	AccountingRecalcGranularityDay   = "DAY"
	AccountingRecalcGranularityMonth = "MONTH"
	AccountingRecalcGranularityAll   = "ALL"

	AccountingRecalcStatusPending   = "PENDING"
	AccountingRecalcStatusRunning   = "RUNNING"
	AccountingRecalcStatusSucceeded = "SUCCEEDED"
	AccountingRecalcStatusFailed    = "FAILED"

	AccountingRecalcSourceBackfill     = "BACKFILL"
	AccountingRecalcSourceManual       = "MANUAL"
	AccountingRecalcSourceTariffChange = "TARIFF_CHANGE"
	AccountingRecalcSourceRatioChange  = "RATIO_CHANGE"

	maxAccountingRecalcRangeDays     = 90
	defaultAccountingRecalcJobsLimit = 50
	maxAccountingRecalcJobsLimit     = 100
)

type AccountingRecalcStore interface {
	ListAccountingRecalcJobs(ctx context.Context, claims registryauth.GrantClaims, siteID string, limit int) (AccountingRecalcJobList, error)
}

type AccountingRecalcWriter interface {
	RequestAccountingRecalculation(ctx context.Context, claims registryauth.GrantClaims, input AccountingRecalculateRequest) (AccountingRecalcJob, error)
}

type InternalDirtyWindowWriter interface {
	EnqueueInternalDirtyWindow(ctx context.Context, claims registryauth.GrantClaims, input InternalDirtyWindowRequest) (AccountingRecalcJob, error)
}

type AccountingRecalculateRequest struct {
	SiteID           string `json:"siteId,omitempty"`
	ActorDisplayName string `json:"-"`
	FromDate         string `json:"fromDate"`
	ToDate           string `json:"toDate"`
	Granularity      string `json:"granularity,omitempty"`
	Reason           string `json:"reason"`
}

type InternalDirtyWindowRequest struct {
	TenantID           string    `json:"tenantId,omitempty"`
	SiteID             string    `json:"siteId,omitempty"`
	Granularity        string    `json:"granularity"`
	WindowStart        time.Time `json:"windowStart"`
	WindowEnd          time.Time `json:"windowEnd"`
	AvailableAt        time.Time `json:"availableAt,omitempty"`
	RequestedBy        string    `json:"requestedBy,omitempty"`
	RequestedByDisplay string    `json:"requestedByDisplay,omitempty"`
	Reason             string    `json:"reason"`
	Source             string    `json:"source"`
}

type AccountingRecalcJob struct {
	ID                 string  `json:"id"`
	TenantID           string  `json:"tenantId"`
	SiteID             string  `json:"siteId"`
	Granularity        string  `json:"granularity"`
	WindowStart        string  `json:"windowStart"`
	WindowEnd          string  `json:"windowEnd"`
	Status             string  `json:"status"`
	AvailableAt        string  `json:"availableAt"`
	RequestedBy        string  `json:"requestedBy"`
	RequestedByDisplay string  `json:"requestedByDisplay"`
	Reason             string  `json:"reason"`
	Source             string  `json:"source"`
	AttemptCount       int     `json:"attemptCount"`
	LastError          string  `json:"lastError"`
	CreatedAt          string  `json:"createdAt"`
	UpdatedAt          string  `json:"updatedAt"`
	FinishedAt         *string `json:"finishedAt"`
}

type AccountingRecalcJobList struct {
	Items []AccountingRecalcJob `json:"items"`
}

type normalizedAccountingRecalcInput struct {
	siteID           string
	actorDisplayName string
	fromDate         time.Time
	toDate           time.Time
	granularity      string
	reason           string
}

func (input AccountingRecalculateRequest) normalize() (normalizedAccountingRecalcInput, error) {
	if !validUUIDv7(input.SiteID) {
		return normalizedAccountingRecalcInput{}, ErrInvalidMutation
	}
	fromDate, err := time.Parse("2006-01-02", input.FromDate)
	if err != nil {
		return normalizedAccountingRecalcInput{}, ErrInvalidMutation
	}
	toDate, err := time.Parse("2006-01-02", input.ToDate)
	if err != nil {
		return normalizedAccountingRecalcInput{}, ErrInvalidMutation
	}
	if toDate.Before(fromDate) {
		return normalizedAccountingRecalcInput{}, ErrInvalidMutation
	}
	if !toDate.Before(fromDate.AddDate(0, 0, maxAccountingRecalcRangeDays)) {
		return normalizedAccountingRecalcInput{}, ErrInvalidMutation
	}
	granularity := strings.TrimSpace(input.Granularity)
	if granularity == "" {
		granularity = AccountingRecalcGranularityHour
	}
	switch granularity {
	case AccountingRecalcGranularityHour, AccountingRecalcGranularityDay, AccountingRecalcGranularityMonth, AccountingRecalcGranularityAll:
	default:
		return normalizedAccountingRecalcInput{}, ErrInvalidMutation
	}
	trimmedReason := strings.TrimSpace(input.Reason)
	if trimmedReason == "" || utf8.RuneCountInString(trimmedReason) > 1000 || utf8.RuneCountInString(input.Reason) > 1000 {
		return normalizedAccountingRecalcInput{}, ErrInvalidMutation
	}
	return normalizedAccountingRecalcInput{
		siteID:           input.SiteID,
		actorDisplayName: input.ActorDisplayName,
		fromDate:         fromDate,
		toDate:           toDate,
		granularity:      granularity,
		reason:           trimmedReason,
	}, nil
}

func (input AccountingRecalculateRequest) Validate() error {
	_, err := input.normalize()
	return err
}

func siteDateRangeToUTC(timezone string, fromDate, toDate time.Time) (time.Time, time.Time, error) {
	location, err := loadSiteLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	windowStart := time.Date(fromDate.Year(), fromDate.Month(), fromDate.Day(), 0, 0, 0, 0, location).UTC()
	endDateExclusive := toDate.AddDate(0, 0, 1)
	windowEnd := time.Date(endDateExclusive.Year(), endDateExclusive.Month(), endDateExclusive.Day(), 0, 0, 0, 0, location).UTC()
	if !windowEnd.After(windowStart) {
		return time.Time{}, time.Time{}, ErrInvalidMutation
	}
	return windowStart, windowEnd, nil
}

func loadSiteLocation(timezone string) (*time.Location, error) {
	trimmed := strings.TrimSpace(timezone)
	if trimmed == "" || strings.EqualFold(trimmed, "UTC") {
		return time.UTC, nil
	}
	if strings.EqualFold(trimmed, "Local") {
		return nil, ErrInvalidMutation
	}
	loaded, err := time.LoadLocation(trimmed)
	if err != nil {
		return nil, fmt.Errorf("load site timezone %q: %w", trimmed, err)
	}
	return loaded, nil
}

func accountingRecalcActionAllowed(claims registryauth.GrantClaims) (registryauth.Action, bool) {
	if len(claims.Actions) != 1 {
		return "", false
	}
	switch claims.Actions[0] {
	case registryauth.ActionBillingManage, registryauth.ActionSavingsManage:
		return claims.Actions[0], true
	default:
		return "", false
	}
}

func (store *PostgresStore) ListAccountingRecalcJobs(ctx context.Context, claims registryauth.GrantClaims, siteID string, limit int) (AccountingRecalcJobList, error) {
	if !validUUIDv7(siteID) {
		return AccountingRecalcJobList{}, ErrNotFound
	}
	if limit == 0 {
		limit = defaultAccountingRecalcJobsLimit
	}
	if limit < 1 || limit > maxAccountingRecalcJobsLimit {
		return AccountingRecalcJobList{}, ErrInvalidPage
	}
	list := AccountingRecalcJobList{Items: []AccountingRecalcJob{}}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var exists bool
		if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE id = $1::uuid AND NOT (id = ANY($2::uuid[]))
)
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&exists); err != nil {
			return fmt.Errorf("verify Site for accounting recalc jobs list: %w", err)
		}
		if !exists {
			return ErrNotFound
		}

		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, granularity,
       window_start, window_end, status, available_at,
       requested_by::text, requested_by_display, reason, source,
       attempt_count, last_error, created_at, updated_at, finished_at
FROM core_registry.energy_recalc_dirty_windows
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND NOT (site_id = ANY($3::uuid[]))
ORDER BY created_at DESC, id DESC
LIMIT $4
`, claims.TenantID, siteID, postgresUUIDArray(claims.DeniedSiteIDs), limit)
		if err != nil {
			return fmt.Errorf("query Site accounting recalc jobs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanAccountingRecalcJob(rows)
			if err != nil {
				return err
			}
			list.Items = append(list.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return AccountingRecalcJobList{}, err
	}
	return list, nil
}

func scanAccountingRecalcJob(row rowScanner) (AccountingRecalcJob, error) {
	var item AccountingRecalcJob
	var windowStart, windowEnd, availableAt, createdAt, updatedAt time.Time
	var finishedAt *time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID, &item.Granularity,
		&windowStart, &windowEnd, &item.Status, &availableAt,
		&item.RequestedBy, &item.RequestedByDisplay, &item.Reason, &item.Source,
		&item.AttemptCount, &item.LastError, &createdAt, &updatedAt, &finishedAt,
	); err != nil {
		return AccountingRecalcJob{}, fmt.Errorf("scan accounting recalc job: %w", err)
	}
	item.WindowStart = formatInstant(windowStart)
	item.WindowEnd = formatInstant(windowEnd)
	item.AvailableAt = formatInstant(availableAt)
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	if finishedAt != nil {
		formatted := formatInstant(*finishedAt)
		item.FinishedAt = &formatted
	}
	return item, nil
}

func (store *PostgresStore) RequestAccountingRecalculation(ctx context.Context, claims registryauth.GrantClaims, input AccountingRecalculateRequest) (AccountingRecalcJob, error) {
	grantedAction, allowed := accountingRecalcActionAllowed(claims)
	if !allowed {
		return AccountingRecalcJob{}, ErrMutationForbidden
	}
	if !validUUIDv7(claims.PrincipalID) {
		return AccountingRecalcJob{}, ErrInvalidMutation
	}
	normalized, err := input.normalize()
	if err != nil {
		return AccountingRecalcJob{}, err
	}
	if !registryauth.ScopeAllows(claims, normalized.siteID) {
		return AccountingRecalcJob{}, ErrNotFound
	}
	actorDisplay := normalizeActorDisplay(normalized.actorDisplayName, claims.PrincipalID)

	var job AccountingRecalcJob
	err = store.withWriteTransaction(ctx, claims, grantedAction, func(tx pgx.Tx) error {
		var timezone string
		if err := tx.QueryRow(ctx, `
SELECT timezone
FROM core_registry.sites
WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
`, claims.TenantID, normalized.siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&timezone); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("query Site timezone for accounting recalc: %w", err)
		}
		windowStart, windowEnd, err := siteDateRangeToUTC(timezone, normalized.fromDate, normalized.toDate)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		jobID, err := newCoreUUIDv7(now)
		if err != nil {
			return err
		}
		availableAt := now
		if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.energy_recalc_dirty_windows (
  id, tenant_id, site_id, granularity, window_start, window_end,
  status, available_at, requested_by, requested_by_display,
  reason, source, attempt_count, last_error, created_at, updated_at, finished_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5, $6,
  $7, $8, $9::uuid, $10,
  $11, $12, 0, '', $13, $13, NULL
)
`, jobID, claims.TenantID, normalized.siteID, normalized.granularity, windowStart, windowEnd,
			AccountingRecalcStatusPending, availableAt, claims.PrincipalID, actorDisplay,
			normalized.reason, AccountingRecalcSourceManual, now); err != nil {
			return mapRegistryWriteError(fmt.Errorf("insert energy recalc dirty window: %w", err))
		}
		job = AccountingRecalcJob{
			ID:                 jobID,
			TenantID:           claims.TenantID,
			SiteID:             normalized.siteID,
			Granularity:        normalized.granularity,
			WindowStart:        formatInstant(windowStart),
			WindowEnd:          formatInstant(windowEnd),
			Status:             AccountingRecalcStatusPending,
			AvailableAt:        formatInstant(availableAt),
			RequestedBy:        claims.PrincipalID,
			RequestedByDisplay: actorDisplay,
			Reason:             normalized.reason,
			Source:             AccountingRecalcSourceManual,
			AttemptCount:       0,
			LastError:          "",
			CreatedAt:          formatInstant(now),
			UpdatedAt:          formatInstant(now),
			FinishedAt:         nil,
		}
		return nil
	})
	if err != nil {
		return AccountingRecalcJob{}, err
	}
	return job, nil
}

type normalizedInternalDirtyWindowInput struct {
	tenantID           string
	siteID             string
	granularity        string
	windowStart        time.Time
	windowEnd          time.Time
	availableAt        time.Time
	requestedBy        string
	requestedByDisplay string
	reason             string
	source             string
}

func (input InternalDirtyWindowRequest) normalize(fallbackRequestedBy string, now time.Time) (normalizedInternalDirtyWindowInput, error) {
	if !validUUIDv7(input.TenantID) || !validUUIDv7(input.SiteID) {
		return normalizedInternalDirtyWindowInput{}, ErrInvalidMutation
	}
	requestedBy := strings.TrimSpace(input.RequestedBy)
	if requestedBy == "" {
		requestedBy = strings.TrimSpace(fallbackRequestedBy)
	}
	if requestedBy != "" && !validUUIDv7(requestedBy) {
		return normalizedInternalDirtyWindowInput{}, ErrInvalidMutation
	}
	granularity := strings.TrimSpace(input.Granularity)
	switch granularity {
	case AccountingRecalcGranularityHour, AccountingRecalcGranularityDay, AccountingRecalcGranularityMonth, AccountingRecalcGranularityAll:
	default:
		return normalizedInternalDirtyWindowInput{}, ErrInvalidMutation
	}
	if input.WindowStart.IsZero() || input.WindowEnd.IsZero() {
		return normalizedInternalDirtyWindowInput{}, ErrInvalidMutation
	}
	windowStart := input.WindowStart.UTC()
	windowEnd := input.WindowEnd.UTC()
	if !windowStart.Before(windowEnd) {
		return normalizedInternalDirtyWindowInput{}, ErrInvalidMutation
	}
	if windowEnd.After(windowStart.Add(maxAccountingRecalcRangeDays * 24 * time.Hour)) {
		return normalizedInternalDirtyWindowInput{}, ErrInvalidMutation
	}
	source := strings.TrimSpace(input.Source)
	switch source {
	case AccountingRecalcSourceBackfill, AccountingRecalcSourceTariffChange, AccountingRecalcSourceRatioChange:
	default:
		return normalizedInternalDirtyWindowInput{}, ErrInvalidMutation
	}
	trimmedReason := strings.TrimSpace(input.Reason)
	if trimmedReason == "" || utf8.RuneCountInString(trimmedReason) > 1000 || utf8.RuneCountInString(input.Reason) > 1000 {
		return normalizedInternalDirtyWindowInput{}, ErrInvalidMutation
	}
	availableAt := now.UTC()
	if !input.AvailableAt.IsZero() {
		availableAt = input.AvailableAt.UTC()
	}
	return normalizedInternalDirtyWindowInput{
		tenantID:           input.TenantID,
		siteID:             input.SiteID,
		granularity:        granularity,
		windowStart:        windowStart,
		windowEnd:          windowEnd,
		availableAt:        availableAt,
		requestedBy:        requestedBy,
		requestedByDisplay: normalizeActorDisplay(input.RequestedByDisplay, requestedBy),
		reason:             trimmedReason,
		source:             source,
	}, nil
}

func (input InternalDirtyWindowRequest) Validate() error {
	_, err := input.normalize("", time.Now().UTC())
	return err
}

func (store *PostgresStore) EnqueueInternalDirtyWindow(ctx context.Context, claims registryauth.GrantClaims, input InternalDirtyWindowRequest) (AccountingRecalcJob, error) {
	if len(claims.Actions) != 1 || !registryauth.ActionAllows(claims.Actions[0], registryauth.ActionMeterBindingResolve) {
		return AccountingRecalcJob{}, ErrMutationForbidden
	}
	if !validUUIDv7(claims.PrincipalID) || !validUUIDv7(claims.TenantID) {
		return AccountingRecalcJob{}, ErrInvalidMutation
	}
	now := time.Now().UTC()
	normalized, err := input.normalize(claims.PrincipalID, now)
	if err != nil {
		return AccountingRecalcJob{}, err
	}
	if normalized.tenantID != claims.TenantID {
		return AccountingRecalcJob{}, ErrInvalidMutation
	}
	if !registryauth.ScopeAllows(claims, normalized.siteID) {
		return AccountingRecalcJob{}, ErrNotFound
	}

	var job AccountingRecalcJob
	err = store.withWriteTransaction(ctx, claims, registryauth.ActionMeterBindingResolve, func(tx pgx.Tx) error {
		var siteExists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
)
`, normalized.tenantID, normalized.siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&siteExists); err != nil {
			return fmt.Errorf("verify Site for internal dirty window: %w", err)
		}
		if !siteExists {
			return ErrNotFound
		}

		var existingID string
		err := tx.QueryRow(ctx, `
SELECT id::text
FROM core_registry.energy_recalc_dirty_windows
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND NOT (site_id = ANY($3::uuid[]))
  AND granularity = $4
  AND source = $5
  AND status = $6
  AND window_start <= $8
  AND window_end >= $7
ORDER BY created_at ASC, id ASC
LIMIT 1
FOR UPDATE
`, normalized.tenantID, normalized.siteID, postgresUUIDArray(claims.DeniedSiteIDs),
			normalized.granularity, normalized.source, AccountingRecalcStatusPending,
			normalized.windowStart, normalized.windowEnd).Scan(&existingID)
		if err == nil {
			updated, scanErr := scanAccountingRecalcJob(tx.QueryRow(ctx, `
UPDATE core_registry.energy_recalc_dirty_windows
SET window_start = LEAST(window_start, $3),
    window_end = GREATEST(window_end, $4),
    available_at = GREATEST(available_at, $5),
    updated_at = GREATEST($6, created_at)
WHERE tenant_id = $1::uuid
  AND id = $2::uuid
  AND status = $7
RETURNING id::text, tenant_id::text, site_id::text, granularity,
          window_start, window_end, status, available_at,
          requested_by::text, requested_by_display, reason, source,
          attempt_count, last_error, created_at, updated_at, finished_at
`, normalized.tenantID, existingID, normalized.windowStart, normalized.windowEnd,
				normalized.availableAt, now, AccountingRecalcStatusPending))
			if scanErr != nil {
				return mapRegistryWriteError(fmt.Errorf("update overlapping energy recalc dirty window: %w", scanErr))
			}
			job = updated
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query overlapping energy recalc dirty window: %w", err)
		}

		jobID, err := newCoreUUIDv7(now)
		if err != nil {
			return err
		}
		inserted, err := scanAccountingRecalcJob(tx.QueryRow(ctx, `
INSERT INTO core_registry.energy_recalc_dirty_windows (
  id, tenant_id, site_id, granularity, window_start, window_end,
  status, available_at, requested_by, requested_by_display,
  reason, source, attempt_count, last_error, created_at, updated_at, finished_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5, $6,
  $7, $8, $9::uuid, $10,
  $11, $12, 0, '', $13, $13, NULL
)
RETURNING id::text, tenant_id::text, site_id::text, granularity,
          window_start, window_end, status, available_at,
          requested_by::text, requested_by_display, reason, source,
          attempt_count, last_error, created_at, updated_at, finished_at
`, jobID, normalized.tenantID, normalized.siteID, normalized.granularity,
			normalized.windowStart, normalized.windowEnd, AccountingRecalcStatusPending,
			normalized.availableAt, normalized.requestedBy, normalized.requestedByDisplay,
			normalized.reason, normalized.source, now))
		if err != nil {
			return mapRegistryWriteError(fmt.Errorf("insert internal energy recalc dirty window: %w", err))
		}
		job = inserted
		return nil
	})
	if err != nil {
		return AccountingRecalcJob{}, err
	}
	return job, nil
}

var (
	_ AccountingRecalcStore     = (*PostgresStore)(nil)
	_ AccountingRecalcWriter    = (*PostgresStore)(nil)
	_ InternalDirtyWindowWriter = (*PostgresStore)(nil)
)
