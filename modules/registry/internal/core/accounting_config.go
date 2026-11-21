package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	HolidayDayTypeHoliday    = "HOLIDAY"
	HolidayDayTypeSpecialDay = "SPECIAL_DAY"
	HolidayDayTypeWeekday    = "WEEKDAY"
	HolidayDayTypeWeekend    = "WEEKEND"

	RatioApplicationModeDeviceApplied   = "DEVICE_APPLIED"
	RatioApplicationModeEdgeApplied     = "EDGE_APPLIED"
	RatioApplicationModePlatformApplied = "PLATFORM_APPLIED"

	MeterRatioReadingUnitWh  = "Wh"
	MeterRatioReadingUnitKWh = "kWh"
	MeterRatioReadingUnitMWh = "MWh"
	MeterRatioReadingUnitGJ  = "GJ"
	MeterRatioReadingUnitMJ  = "MJ"
	MeterRatioReadingUnitRTh = "RTh"

	MeterRatioStatusDraft      = "DRAFT"
	MeterRatioStatusReleased   = "RELEASED"
	MeterRatioStatusSuperseded = "SUPERSEDED"

	maxHolidayLabelRunes  = 256
	maxMeterRatioNumeric  = 1_000_000_000.0
	minAccountingConfigYr = 1900
	maxAccountingConfigYr = 9999
)

var ErrMeterRatioOverlap = errors.New("meter ratio version overlaps an existing released window")

type AccountingConfigStore interface {
	ListSiteCalendarHolidays(ctx context.Context, claims registryauth.GrantClaims, siteID string, year *int) (SiteCalendarHolidayList, error)
	UpsertSiteCalendarHoliday(ctx context.Context, claims registryauth.GrantClaims, input UpsertSiteCalendarHolidayInput) (SiteCalendarHoliday, error)
	DeleteSiteCalendarHoliday(ctx context.Context, claims registryauth.GrantClaims, siteID string, holidayDate string) (SiteCalendarHolidayDeleteResult, error)
	ListSiteMeterRatioVersions(ctx context.Context, claims registryauth.GrantClaims, input ListSiteMeterRatioVersionsInput) (MeterRatioVersionList, error)
	CreateSiteMeterRatioVersion(ctx context.Context, claims registryauth.GrantClaims, input CreateSiteMeterRatioVersionInput) (MeterRatioVersion, error)
	RetireSiteMeterRatioVersion(ctx context.Context, claims registryauth.GrantClaims, input RetireSiteMeterRatioVersionInput) (MeterRatioVersion, error)
}

type SiteCalendarHoliday struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	SiteID      string `json:"siteId"`
	HolidayDate string `json:"holidayDate"`
	DayType     string `json:"dayType"`
	Label       string `json:"label"`
	Revision    int64  `json:"revision"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	RecalcJobID string `json:"recalcJobId,omitempty"`
}

type SiteCalendarHolidayList struct {
	Items []SiteCalendarHoliday `json:"items"`
}

type UpsertSiteCalendarHolidayInput struct {
	SiteID           string `json:"siteId,omitempty"`
	HolidayDate      string `json:"holidayDate,omitempty"`
	DayType          string `json:"dayType"`
	Label            string `json:"label"`
	ActorDisplayName string `json:"-"`
}

type SiteCalendarHolidayDeleteResult struct {
	SiteID      string `json:"siteId"`
	HolidayDate string `json:"holidayDate"`
	Deleted     bool   `json:"deleted"`
	RecalcJobID string `json:"recalcJobId,omitempty"`
}

type MeterRatioVersion struct {
	ID                   string  `json:"id"`
	TenantID             string  `json:"tenantId"`
	SiteID               string  `json:"siteId"`
	MeterID              *string `json:"meterId"`
	MeterBindingID       *string `json:"meterBindingId"`
	PointID              *string `json:"pointId"`
	Version              *int64  `json:"version"`
	CTRatio              float64 `json:"ctRatio"`
	PTRatio              float64 `json:"ptRatio"`
	MeterMultiplier      float64 `json:"meterMultiplier"`
	CloudMultiplier      float64 `json:"cloudMultiplier"`
	RatioApplicationMode string  `json:"ratioApplicationMode"`
	ReadingUnit          string  `json:"readingUnit"`
	EffectiveFrom        string  `json:"effectiveFrom"`
	EffectiveTo          *string `json:"effectiveTo"`
	Status               string  `json:"status"`
	Revision             int64   `json:"revision"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
	RecalcJobID          string  `json:"recalcJobId,omitempty"`
}

type MeterRatioVersionList struct {
	Items []MeterRatioVersion `json:"items"`
}

type ListSiteMeterRatioVersionsInput struct {
	SiteID            string
	PointID           string
	IncludeSuperseded bool
}

type CreateSiteMeterRatioVersionInput struct {
	SiteID               string   `json:"siteId,omitempty"`
	PointID              string   `json:"pointId"`
	MeterBindingID       *string  `json:"meterBindingId,omitempty"`
	MeterID              *string  `json:"meterId,omitempty"`
	CTRatio              float64  `json:"ctRatio"`
	PTRatio              float64  `json:"ptRatio"`
	MeterMultiplier      *float64 `json:"meterMultiplier,omitempty"`
	RatioApplicationMode string   `json:"ratioApplicationMode"`
	ReadingUnit          string   `json:"readingUnit,omitempty"`
	EffectiveFrom        string   `json:"effectiveFrom"`
	EffectiveTo          *string  `json:"effectiveTo,omitempty"`
	ActorDisplayName     string   `json:"-"`
}

type RetireSiteMeterRatioVersionInput struct {
	SiteID           string  `json:"siteId,omitempty"`
	VersionID        string  `json:"versionId,omitempty"`
	EffectiveTo      *string `json:"effectiveTo,omitempty"`
	ActorDisplayName string  `json:"-"`
}

type normalizedUpsertHolidayInput struct {
	siteID           string
	holidayDate      string
	parsedDate       time.Time
	dayType          string
	label            string
	actorDisplayName string
}

func (input UpsertSiteCalendarHolidayInput) normalize() (normalizedUpsertHolidayInput, error) {
	parsedDate, ok := parseISODate(input.HolidayDate)
	if !ok {
		return normalizedUpsertHolidayInput{}, ErrInvalidMutation
	}
	dayType := strings.TrimSpace(input.DayType)
	switch dayType {
	case HolidayDayTypeHoliday, HolidayDayTypeSpecialDay, HolidayDayTypeWeekday, HolidayDayTypeWeekend:
	default:
		return normalizedUpsertHolidayInput{}, ErrInvalidMutation
	}
	trimmedLabel := strings.TrimSpace(input.Label)
	if utf8.RuneCountInString(trimmedLabel) > maxHolidayLabelRunes || utf8.RuneCountInString(input.Label) > maxHolidayLabelRunes {
		return normalizedUpsertHolidayInput{}, ErrInvalidMutation
	}
	return normalizedUpsertHolidayInput{
		siteID:           input.SiteID,
		holidayDate:      input.HolidayDate,
		parsedDate:       parsedDate,
		dayType:          dayType,
		label:            trimmedLabel,
		actorDisplayName: input.ActorDisplayName,
	}, nil
}

func (input UpsertSiteCalendarHolidayInput) Validate() error {
	if !validUUIDv7(input.SiteID) {
		return ErrInvalidMutation
	}
	_, err := input.normalize()
	return err
}

type normalizedCreateMeterRatioInput struct {
	siteID               string
	pointID              string
	meterBindingID       *string
	meterID              *string
	ctRatio              float64
	ptRatio              float64
	meterMultiplier      float64
	ratioApplicationMode string
	readingUnit          string
	effectiveFrom        time.Time
	effectiveTo          *time.Time
	actorDisplayName     string
}

func validRatioFactor(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0 && value <= maxMeterRatioNumeric
}

func normalizeOptionalUUIDv7Ptr(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	if !validUUIDv7(trimmed) {
		return nil, ErrInvalidMutation
	}
	return &trimmed, nil
}

func (input CreateSiteMeterRatioVersionInput) normalize() (normalizedCreateMeterRatioInput, error) {
	if !validUUIDv7(input.PointID) {
		return normalizedCreateMeterRatioInput{}, ErrInvalidMutation
	}
	meterBindingID, err := normalizeOptionalUUIDv7Ptr(input.MeterBindingID)
	if err != nil {
		return normalizedCreateMeterRatioInput{}, err
	}
	meterID, err := normalizeOptionalUUIDv7Ptr(input.MeterID)
	if err != nil {
		return normalizedCreateMeterRatioInput{}, err
	}
	if !validRatioFactor(input.CTRatio) || !validRatioFactor(input.PTRatio) {
		return normalizedCreateMeterRatioInput{}, ErrInvalidMutation
	}
	meterMultiplier := 1.0
	if input.MeterMultiplier != nil {
		if !validRatioFactor(*input.MeterMultiplier) {
			return normalizedCreateMeterRatioInput{}, ErrInvalidMutation
		}
		meterMultiplier = *input.MeterMultiplier
	}
	mode := strings.TrimSpace(input.RatioApplicationMode)
	switch mode {
	case RatioApplicationModeDeviceApplied, RatioApplicationModeEdgeApplied, RatioApplicationModePlatformApplied:
	default:
		return normalizedCreateMeterRatioInput{}, ErrInvalidMutation
	}
	readingUnit := strings.TrimSpace(input.ReadingUnit)
	if readingUnit == "" {
		readingUnit = MeterRatioReadingUnitKWh
	}
	switch readingUnit {
	case MeterRatioReadingUnitWh, MeterRatioReadingUnitKWh, MeterRatioReadingUnitMWh, MeterRatioReadingUnitGJ, MeterRatioReadingUnitMJ, MeterRatioReadingUnitRTh:
	default:
		return normalizedCreateMeterRatioInput{}, ErrInvalidMutation
	}
	effectiveFrom, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.EffectiveFrom))
	if err != nil {
		return normalizedCreateMeterRatioInput{}, ErrInvalidMutation
	}
	effectiveFrom = effectiveFrom.UTC()
	var effectiveTo *time.Time
	if input.EffectiveTo != nil {
		trimmedTo := strings.TrimSpace(*input.EffectiveTo)
		if trimmedTo != "" {
			parsedTo, parseErr := time.Parse(time.RFC3339Nano, trimmedTo)
			if parseErr != nil {
				return normalizedCreateMeterRatioInput{}, ErrInvalidMutation
			}
			utcTo := parsedTo.UTC()
			if !utcTo.After(effectiveFrom) {
				return normalizedCreateMeterRatioInput{}, ErrInvalidMutation
			}
			effectiveTo = &utcTo
		}
	}
	return normalizedCreateMeterRatioInput{
		siteID:               input.SiteID,
		pointID:              input.PointID,
		meterBindingID:       meterBindingID,
		meterID:              meterID,
		ctRatio:              input.CTRatio,
		ptRatio:              input.PTRatio,
		meterMultiplier:      meterMultiplier,
		ratioApplicationMode: mode,
		readingUnit:          readingUnit,
		effectiveFrom:        effectiveFrom,
		effectiveTo:          effectiveTo,
		actorDisplayName:     input.ActorDisplayName,
	}, nil
}

func (input CreateSiteMeterRatioVersionInput) Validate() error {
	if !validUUIDv7(input.SiteID) {
		return ErrInvalidMutation
	}
	_, err := input.normalize()
	return err
}

func (input RetireSiteMeterRatioVersionInput) parseEffectiveTo() (*time.Time, error) {
	if input.EffectiveTo == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*input.EffectiveTo)
	if trimmed == "" {
		return nil, ErrInvalidMutation
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return nil, ErrInvalidMutation
	}
	utc := parsed.UTC()
	return &utc, nil
}

func (input RetireSiteMeterRatioVersionInput) Validate() error {
	if !validUUIDv7(input.SiteID) || !validUUIDv7(input.VersionID) {
		return ErrInvalidMutation
	}
	_, err := input.parseEffectiveTo()
	return err
}

func billingManageAllowed(claims registryauth.GrantClaims) bool {
	return len(claims.Actions) == 1 && registryauth.ActionAllows(claims.Actions[0], registryauth.ActionBillingManage)
}

func ratioDirtyWindowBounds(effectiveFrom time.Time, effectiveTo *time.Time, now time.Time) (time.Time, time.Time) {
	start := effectiveFrom.UTC()
	end := now.UTC()
	if effectiveTo != nil && effectiveTo.UTC().Before(end) {
		end = effectiveTo.UTC()
	}
	if !end.After(start) {
		fallbackEnd := start.Add(time.Hour)
		if effectiveTo != nil && effectiveTo.UTC().Before(fallbackEnd) {
			fallbackEnd = effectiveTo.UTC()
		}
		end = fallbackEnd
	}
	return start, end
}

func computeCloudMultiplier(ctRatio, ptRatio, meterMultiplier float64, mode string) float64 {
	if mode == RatioApplicationModePlatformApplied {
		return ctRatio * ptRatio * meterMultiplier
	}
	return 1.0
}

func mapMeterRatioWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrMeterRatioOverlap) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23P01" {
			return ErrMeterRatioOverlap
		}
		if pgErr.Code == "23514" {
			if pgErr.ConstraintName == "meter_ratio_versions_target_check" {
				return ErrInvalidMutation
			}
			return ErrMeterRatioOverlap
		}
	}
	return mapRegistryWriteError(err)
}

func insertAccountingConfigDirtyWindow(
	ctx context.Context,
	tx pgx.Tx,
	claims registryauth.GrantClaims,
	siteID string,
	windowStart time.Time,
	windowEnd time.Time,
	source string,
	reason string,
	actorDisplayName string,
	now time.Time,
) (string, error) {
	jobID, err := newCoreUUIDv7(now)
	if err != nil {
		return "", err
	}
	actorDisplay := normalizeActorDisplay(actorDisplayName, claims.PrincipalID)
	trimmedReason := strings.TrimSpace(reason)
	if utf8.RuneCountInString(trimmedReason) > 1000 {
		trimmedReason = string([]rune(trimmedReason)[:1000])
	}
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
`, jobID, claims.TenantID, siteID, AccountingRecalcGranularityAll, windowStart.UTC(), windowEnd.UTC(),
		AccountingRecalcStatusPending, now.UTC(), claims.PrincipalID, actorDisplay,
		trimmedReason, source, now.UTC()); err != nil {
		return "", mapRegistryWriteError(fmt.Errorf("insert accounting config recalc dirty window: %w", err))
	}
	return jobID, nil
}

func (store *PostgresStore) ListSiteCalendarHolidays(ctx context.Context, claims registryauth.GrantClaims, siteID string, year *int) (SiteCalendarHolidayList, error) {
	if !validUUIDv7(siteID) || !registryauth.ScopeAllows(claims, siteID) {
		return SiteCalendarHolidayList{}, ErrNotFound
	}
	var startDateParam, endDateParam any
	if year != nil {
		if *year < minAccountingConfigYr || *year > maxAccountingConfigYr {
			return SiteCalendarHolidayList{}, ErrInvalidPage
		}
		startDateParam = fmt.Sprintf("%04d-01-01", *year)
		endDateParam = fmt.Sprintf("%04d-01-01", *year+1)
	}
	list := SiteCalendarHolidayList{Items: []SiteCalendarHoliday{}}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var exists bool
		if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE id = $1::uuid AND NOT (id = ANY($2::uuid[]))
)
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&exists); err != nil {
			return fmt.Errorf("verify Site for calendar holidays list: %w", err)
		}
		if !exists {
			return ErrNotFound
		}

		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text,
       to_char(holiday_date, 'YYYY-MM-DD'), day_type, label,
       revision, created_at, updated_at
FROM core_registry.site_calendar_holidays
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND NOT (site_id = ANY($3::uuid[]))
  AND ($4::date IS NULL OR (holiday_date >= $4::date AND holiday_date < $5::date))
ORDER BY holiday_date ASC
`, claims.TenantID, siteID, postgresUUIDArray(claims.DeniedSiteIDs), startDateParam, endDateParam)
		if err != nil {
			return fmt.Errorf("query Site calendar holidays list: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, scanErr := scanSiteCalendarHoliday(rows)
			if scanErr != nil {
				return scanErr
			}
			list.Items = append(list.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return SiteCalendarHolidayList{}, err
	}
	return list, nil
}

func scanSiteCalendarHoliday(row rowScanner) (SiteCalendarHoliday, error) {
	var item SiteCalendarHoliday
	var createdAt, updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID,
		&item.HolidayDate, &item.DayType, &item.Label,
		&item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return SiteCalendarHoliday{}, fmt.Errorf("scan Site calendar holiday: %w", err)
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func (store *PostgresStore) UpsertSiteCalendarHoliday(ctx context.Context, claims registryauth.GrantClaims, input UpsertSiteCalendarHolidayInput) (SiteCalendarHoliday, error) {
	if !billingManageAllowed(claims) {
		return SiteCalendarHoliday{}, ErrMutationForbidden
	}
	if !validUUIDv7(claims.PrincipalID) || !validUUIDv7(claims.TenantID) {
		return SiteCalendarHoliday{}, ErrInvalidMutation
	}
	if !validUUIDv7(input.SiteID) || !registryauth.ScopeAllows(claims, input.SiteID) {
		return SiteCalendarHoliday{}, ErrNotFound
	}
	normalized, err := input.normalize()
	if err != nil {
		return SiteCalendarHoliday{}, err
	}

	var saved SiteCalendarHoliday
	err = store.withWriteTransaction(ctx, claims, registryauth.ActionBillingManage, func(tx pgx.Tx) error {
		var timezone string
		if err := tx.QueryRow(ctx, `
SELECT timezone
FROM core_registry.sites
WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
`, claims.TenantID, normalized.siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&timezone); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("query Site timezone for holiday upsert: %w", err)
		}
		windowStart, windowEnd, err := siteDateRangeToUTC(timezone, normalized.parsedDate, normalized.parsedDate)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		holidayID, err := newCoreUUIDv7(now)
		if err != nil {
			return err
		}
		item, err := scanSiteCalendarHoliday(tx.QueryRow(ctx, `
INSERT INTO core_registry.site_calendar_holidays (
  id, tenant_id, site_id, holiday_date, day_type, label,
  revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::date, $5, $6,
  1, $7, $7
)
ON CONFLICT (tenant_id, site_id, holiday_date) DO UPDATE
SET day_type = EXCLUDED.day_type,
    label = EXCLUDED.label,
    revision = core_registry.site_calendar_holidays.revision + 1,
    updated_at = GREATEST(EXCLUDED.updated_at, core_registry.site_calendar_holidays.created_at)
RETURNING id::text, tenant_id::text, site_id::text,
          to_char(holiday_date, 'YYYY-MM-DD'), day_type, label,
          revision, created_at, updated_at
`, holidayID, claims.TenantID, normalized.siteID, normalized.holidayDate, normalized.dayType, normalized.label, now))
		if err != nil {
			return mapRegistryWriteError(fmt.Errorf("upsert Site calendar holiday: %w", err))
		}
		recalcJobID, err := insertAccountingConfigDirtyWindow(
			ctx, tx, claims, normalized.siteID,
			windowStart, windowEnd, AccountingRecalcSourceTariffChange,
			fmt.Sprintf("站点日历覆写变更(%s:%s)", normalized.holidayDate, normalized.dayType),
			normalized.actorDisplayName, now,
		)
		if err != nil {
			return err
		}
		item.RecalcJobID = recalcJobID
		saved = item
		return nil
	})
	if err != nil {
		return SiteCalendarHoliday{}, err
	}
	return saved, nil
}

func (store *PostgresStore) DeleteSiteCalendarHoliday(ctx context.Context, claims registryauth.GrantClaims, siteID string, holidayDate string) (SiteCalendarHolidayDeleteResult, error) {
	if !billingManageAllowed(claims) {
		return SiteCalendarHolidayDeleteResult{}, ErrMutationForbidden
	}
	if !validUUIDv7(claims.PrincipalID) || !validUUIDv7(claims.TenantID) {
		return SiteCalendarHolidayDeleteResult{}, ErrInvalidMutation
	}
	if !validUUIDv7(siteID) || !registryauth.ScopeAllows(claims, siteID) {
		return SiteCalendarHolidayDeleteResult{}, ErrNotFound
	}
	parsedDate, ok := parseISODate(holidayDate)
	if !ok {
		return SiteCalendarHolidayDeleteResult{}, ErrInvalidMutation
	}

	var result SiteCalendarHolidayDeleteResult
	err := store.withWriteTransaction(ctx, claims, registryauth.ActionBillingManage, func(tx pgx.Tx) error {
		var timezone string
		if err := tx.QueryRow(ctx, `
SELECT timezone
FROM core_registry.sites
WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
`, claims.TenantID, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&timezone); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("query Site timezone for holiday delete: %w", err)
		}
		windowStart, windowEnd, err := siteDateRangeToUTC(timezone, parsedDate, parsedDate)
		if err != nil {
			return err
		}
		var deletedID string
		if err := tx.QueryRow(ctx, `
DELETE FROM core_registry.site_calendar_holidays
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND holiday_date = $3::date
  AND NOT (site_id = ANY($4::uuid[]))
RETURNING id::text
`, claims.TenantID, siteID, holidayDate, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&deletedID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return mapRegistryWriteError(fmt.Errorf("delete Site calendar holiday: %w", err))
		}
		now := time.Now().UTC()
		recalcJobID, err := insertAccountingConfigDirtyWindow(
			ctx, tx, claims, siteID,
			windowStart, windowEnd, AccountingRecalcSourceTariffChange,
			fmt.Sprintf("删除站点日历覆写(%s)", holidayDate),
			"", now,
		)
		if err != nil {
			return err
		}
		result = SiteCalendarHolidayDeleteResult{
			SiteID:      siteID,
			HolidayDate: holidayDate,
			Deleted:     true,
			RecalcJobID: recalcJobID,
		}
		return nil
	})
	if err != nil {
		return SiteCalendarHolidayDeleteResult{}, err
	}
	return result, nil
}

func (store *PostgresStore) ListSiteMeterRatioVersions(ctx context.Context, claims registryauth.GrantClaims, input ListSiteMeterRatioVersionsInput) (MeterRatioVersionList, error) {
	if !validUUIDv7(input.SiteID) || !registryauth.ScopeAllows(claims, input.SiteID) {
		return MeterRatioVersionList{}, ErrNotFound
	}
	var pointIDParam any
	if trimmedPointID := strings.TrimSpace(input.PointID); trimmedPointID != "" {
		if !validUUIDv7(trimmedPointID) {
			return MeterRatioVersionList{}, ErrInvalidPage
		}
		pointIDParam = trimmedPointID
	}
	list := MeterRatioVersionList{Items: []MeterRatioVersion{}}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var exists bool
		if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE id = $1::uuid AND NOT (id = ANY($2::uuid[]))
)
`, input.SiteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&exists); err != nil {
			return fmt.Errorf("verify Site for meter ratio versions list: %w", err)
		}
		if !exists {
			return ErrNotFound
		}

		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text,
       meter_id::text, meter_binding_id::text, point_id::text, version,
       ct_ratio::double precision, pt_ratio::double precision, meter_multiplier::double precision,
       cloud_multiplier::double precision, ratio_application_mode, reading_unit,
       effective_from, effective_to, status, revision, created_at, updated_at
FROM core_registry.meter_ratio_versions
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND NOT (site_id = ANY($3::uuid[]))
  AND ($4::uuid IS NULL OR point_id = $4::uuid)
  AND ($5::boolean OR status <> 'SUPERSEDED')
ORDER BY effective_from DESC, version DESC NULLS LAST, revision DESC, id DESC
`, claims.TenantID, input.SiteID, postgresUUIDArray(claims.DeniedSiteIDs), pointIDParam, input.IncludeSuperseded)
		if err != nil {
			return fmt.Errorf("query Site meter ratio versions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, scanErr := scanMeterRatioVersion(rows)
			if scanErr != nil {
				return scanErr
			}
			list.Items = append(list.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return MeterRatioVersionList{}, err
	}
	return list, nil
}

func scanMeterRatioVersion(row rowScanner) (MeterRatioVersion, error) {
	var item MeterRatioVersion
	var effectiveFrom, createdAt, updatedAt time.Time
	var effectiveTo *time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID,
		&item.MeterID, &item.MeterBindingID, &item.PointID, &item.Version,
		&item.CTRatio, &item.PTRatio, &item.MeterMultiplier,
		&item.CloudMultiplier, &item.RatioApplicationMode, &item.ReadingUnit,
		&effectiveFrom, &effectiveTo, &item.Status, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return MeterRatioVersion{}, fmt.Errorf("scan meter ratio version: %w", err)
	}
	item.EffectiveFrom = formatInstant(effectiveFrom)
	if effectiveTo != nil {
		formatted := formatInstant(*effectiveTo)
		item.EffectiveTo = &formatted
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func (store *PostgresStore) CreateSiteMeterRatioVersion(ctx context.Context, claims registryauth.GrantClaims, input CreateSiteMeterRatioVersionInput) (MeterRatioVersion, error) {
	if !billingManageAllowed(claims) {
		return MeterRatioVersion{}, ErrMutationForbidden
	}
	if !validUUIDv7(claims.PrincipalID) || !validUUIDv7(claims.TenantID) {
		return MeterRatioVersion{}, ErrInvalidMutation
	}
	if !validUUIDv7(input.SiteID) || !registryauth.ScopeAllows(claims, input.SiteID) {
		return MeterRatioVersion{}, ErrNotFound
	}
	normalized, err := input.normalize()
	if err != nil {
		return MeterRatioVersion{}, err
	}

	var created MeterRatioVersion
	err = store.withWriteTransaction(ctx, claims, registryauth.ActionBillingManage, func(tx pgx.Tx) error {
		var siteExists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
)
`, claims.TenantID, normalized.siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&siteExists); err != nil {
			return fmt.Errorf("verify Site for meter ratio version create: %w", err)
		}
		if !siteExists {
			return ErrNotFound
		}

		meterID, meterBindingID, err := resolveRatioVersionTarget(ctx, tx, claims.TenantID, normalized)
		if err != nil {
			return err
		}

		version, err := nextMeterRatioVersion(ctx, tx, claims.TenantID, normalized.siteID, meterID, normalized.pointID)
		if err != nil {
			return err
		}

		now := time.Now().UTC()
		versionRowID, err := newCoreUUIDv7(now)
		if err != nil {
			return err
		}
		var meterIDParam, meterBindingIDParam, effectiveToParam any
		if meterID != nil {
			meterIDParam = *meterID
		}
		if meterBindingID != nil {
			meterBindingIDParam = *meterBindingID
		}
		if normalized.effectiveTo != nil {
			effectiveToParam = *normalized.effectiveTo
		}

		item, err := scanMeterRatioVersion(tx.QueryRow(ctx, `
INSERT INTO core_registry.meter_ratio_versions (
  id, tenant_id, site_id, meter_id, meter_binding_id, point_id,
  version, ct_ratio, pt_ratio, meter_multiplier,
  ratio_application_mode, reading_unit,
  effective_from, effective_to, status, revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid,
  $7, $8::numeric(24,8), $9::numeric(24,8), $10::numeric(24,8),
  $11, $12,
  $13, $14, 'RELEASED', 1, $15, $15
)
RETURNING id::text, tenant_id::text, site_id::text,
          meter_id::text, meter_binding_id::text, point_id::text, version,
          ct_ratio::double precision, pt_ratio::double precision, meter_multiplier::double precision,
          cloud_multiplier::double precision, ratio_application_mode, reading_unit,
          effective_from, effective_to, status, revision, created_at, updated_at
`, versionRowID, claims.TenantID, normalized.siteID, meterIDParam, meterBindingIDParam, normalized.pointID,
			version,
			strconv.FormatFloat(normalized.ctRatio, 'f', -1, 64),
			strconv.FormatFloat(normalized.ptRatio, 'f', -1, 64),
			strconv.FormatFloat(normalized.meterMultiplier, 'f', -1, 64),
			normalized.ratioApplicationMode, normalized.readingUnit,
			normalized.effectiveFrom, effectiveToParam, now))
		if err != nil {
			return mapMeterRatioWriteError(fmt.Errorf("insert meter ratio version: %w", err))
		}

		windowStart, windowEnd := ratioDirtyWindowBounds(normalized.effectiveFrom, normalized.effectiveTo, now)
		recalcJobID, err := insertAccountingConfigDirtyWindow(
			ctx, tx, claims, normalized.siteID,
			windowStart, windowEnd, AccountingRecalcSourceRatioChange,
			fmt.Sprintf("新增计量变比版本(point=%s,v=%d)", normalized.pointID, version),
			normalized.actorDisplayName, now,
		)
		if err != nil {
			return err
		}
		item.RecalcJobID = recalcJobID
		created = item
		return nil
	})
	if err != nil {
		return MeterRatioVersion{}, err
	}
	return created, nil
}

func resolveRatioVersionTarget(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	normalized normalizedCreateMeterRatioInput,
) (*string, *string, error) {
	if normalized.meterBindingID != nil {
		var boundID, boundMeterID, boundPointID string
		err := tx.QueryRow(ctx, `
SELECT id::text, meter_id::text, point_id::text
FROM core_registry.meter_bindings
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
`, tenantID, normalized.siteID, *normalized.meterBindingID).Scan(&boundID, &boundMeterID, &boundPointID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		if err != nil {
			return nil, nil, fmt.Errorf("lookup meter binding by id: %w", err)
		}
		if boundPointID != normalized.pointID {
			return nil, nil, ErrInvalidMutation
		}
		if normalized.meterID != nil && *normalized.meterID != boundMeterID {
			return nil, nil, ErrInvalidMutation
		}
		return &boundMeterID, &boundID, nil
	}

	var boundID, boundMeterID string
	err := tx.QueryRow(ctx, `
SELECT id::text, meter_id::text
FROM core_registry.meter_bindings
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND point_id = $3::uuid
ORDER BY CASE status WHEN 'ACTIVE' THEN 0 WHEN 'RELEASED' THEN 1 WHEN 'DRAFT' THEN 2 ELSE 3 END,
         effective_from DESC,
         version DESC,
         id DESC
LIMIT 1
`, tenantID, normalized.siteID, normalized.pointID).Scan(&boundID, &boundMeterID)
	if err == nil {
		if normalized.meterID != nil && *normalized.meterID != boundMeterID {
			return nil, nil, ErrInvalidMutation
		}
		return &boundMeterID, &boundID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("lookup meter binding by point: %w", err)
	}

	var pointExists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.telemetry_points
  WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
)
`, tenantID, normalized.siteID, normalized.pointID).Scan(&pointExists); err != nil {
		return nil, nil, fmt.Errorf("verify point for meter ratio version: %w", err)
	}
	if !pointExists {
		return nil, nil, ErrNotFound
	}
	if normalized.meterID == nil {
		return nil, nil, ErrInvalidMutation
	}
	return normalized.meterID, nil, nil
}

func nextMeterRatioVersion(ctx context.Context, tx pgx.Tx, tenantID, siteID string, meterID *string, pointID string) (int64, error) {
	if meterID != nil {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, tenantID+"|"+siteID+"|meter|"+*meterID); err != nil {
			return 0, fmt.Errorf("lock meter ratio sequence: %w", err)
		}
		var next int64
		if err := tx.QueryRow(ctx, `
SELECT COALESCE(MAX(version), 0) + 1
FROM core_registry.meter_ratio_versions
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND meter_id = $3::uuid
`, tenantID, siteID, *meterID).Scan(&next); err != nil {
			return 0, fmt.Errorf("compute next meter ratio version: %w", err)
		}
		return next, nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, tenantID+"|"+siteID+"|point|"+pointID); err != nil {
		return 0, fmt.Errorf("lock point ratio sequence: %w", err)
	}
	var next int64
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(MAX(version), 0) + 1
FROM core_registry.meter_ratio_versions
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND point_id = $3::uuid
`, tenantID, siteID, pointID).Scan(&next); err != nil {
		return 0, fmt.Errorf("compute next point ratio version: %w", err)
	}
	return next, nil
}

func (store *PostgresStore) RetireSiteMeterRatioVersion(ctx context.Context, claims registryauth.GrantClaims, input RetireSiteMeterRatioVersionInput) (MeterRatioVersion, error) {
	if !billingManageAllowed(claims) {
		return MeterRatioVersion{}, ErrMutationForbidden
	}
	if !validUUIDv7(claims.PrincipalID) || !validUUIDv7(claims.TenantID) {
		return MeterRatioVersion{}, ErrInvalidMutation
	}
	if !validUUIDv7(input.SiteID) || !validUUIDv7(input.VersionID) || !registryauth.ScopeAllows(claims, input.SiteID) {
		return MeterRatioVersion{}, ErrNotFound
	}
	parsedEffectiveTo, err := input.parseEffectiveTo()
	if err != nil {
		return MeterRatioVersion{}, err
	}

	var retired MeterRatioVersion
	err = store.withWriteTransaction(ctx, claims, registryauth.ActionBillingManage, func(tx pgx.Tx) error {
		var existingEffectiveFrom time.Time
		var existingStatus string
		var existingPointID *string
		err := tx.QueryRow(ctx, `
SELECT effective_from, status, point_id::text
FROM core_registry.meter_ratio_versions
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
  AND NOT (site_id = ANY($4::uuid[]))
FOR UPDATE
`, claims.TenantID, input.SiteID, input.VersionID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(
			&existingEffectiveFrom, &existingStatus, &existingPointID,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock meter ratio version for retire: %w", err)
		}
		if existingStatus != MeterRatioStatusReleased {
			return ErrBindingConflict
		}

		now := time.Now().UTC()
		effectiveTo := now
		if parsedEffectiveTo != nil {
			effectiveTo = *parsedEffectiveTo
		}
		if !effectiveTo.After(existingEffectiveFrom) {
			return ErrInvalidMutation
		}

		item, err := scanMeterRatioVersion(tx.QueryRow(ctx, `
UPDATE core_registry.meter_ratio_versions
SET effective_to = $4,
    status = 'RELEASED',
    revision = revision + 1,
    updated_at = GREATEST($5, created_at)
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
RETURNING id::text, tenant_id::text, site_id::text,
          meter_id::text, meter_binding_id::text, point_id::text, version,
          ct_ratio::double precision, pt_ratio::double precision, meter_multiplier::double precision,
          cloud_multiplier::double precision, ratio_application_mode, reading_unit,
          effective_from, effective_to, status, revision, created_at, updated_at
`, claims.TenantID, input.SiteID, input.VersionID, effectiveTo, now))
		if err != nil {
			return mapMeterRatioWriteError(fmt.Errorf("retire meter ratio version: %w", err))
		}

		pointLabel := input.VersionID
		if existingPointID != nil && *existingPointID != "" {
			pointLabel = *existingPointID
		}
		windowStart, windowEnd := ratioDirtyWindowBounds(existingEffectiveFrom, &effectiveTo, now)
		recalcJobID, err := insertAccountingConfigDirtyWindow(
			ctx, tx, claims, input.SiteID,
			windowStart, windowEnd, AccountingRecalcSourceRatioChange,
			fmt.Sprintf("终止计量变比版本生效窗口(point=%s,id=%s)", pointLabel, input.VersionID),
			input.ActorDisplayName, now,
		)
		if err != nil {
			return err
		}
		item.RecalcJobID = recalcJobID
		retired = item
		return nil
	})
	if err != nil {
		return MeterRatioVersion{}, err
	}
	return retired, nil
}

func (server *server) tryHandleAccountingConfigRoute(
	writer http.ResponseWriter,
	request *http.Request,
	route registryRoute,
	claims registryauth.GrantClaims,
) (int, bool) {
	switch route.resource {
	case "accounting-holidays":
		query := request.URL.Query()
		for name, values := range query {
			if name != "year" || len(values) != 1 {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The accounting holidays query is invalid.", false)
				return http.StatusBadRequest, true
			}
		}
		var yearPtr *int
		if rawYear := query.Get("year"); rawYear != "" {
			if len(rawYear) != 4 {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The accounting holidays year filter is invalid.", false)
				return http.StatusBadRequest, true
			}
			parsedYear, err := strconv.Atoi(rawYear)
			if err != nil || parsedYear < minAccountingConfigYr || parsedYear > maxAccountingConfigYr {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The accounting holidays year filter is invalid.", false)
				return http.StatusBadRequest, true
			}
			yearPtr = &parsedYear
		}
		reader, ok := server.store.(AccountingConfigStore)
		if !ok {
			writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "The Core Registry accounting config store is not configured.", true)
			return http.StatusServiceUnavailable, true
		}
		list, err := reader.ListSiteCalendarHolidays(request.Context(), claims, route.parentID, yearPtr)
		if err != nil {
			return server.writeStoreError(writer, request, err), true
		}
		writeJSON(writer, http.StatusOK, list)
		return http.StatusOK, true

	case "accounting-holiday-upsert":
		if request.URL.RawQuery != "" {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation routes do not accept query parameters.", false)
			return http.StatusBadRequest, true
		}
		configWriter, status, ok := server.accountingConfigWriter(writer, request)
		if !ok {
			return status, true
		}
		var input UpsertSiteCalendarHolidayInput
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest, true
		}
		if input.SiteID == "" {
			input.SiteID = route.parentID
		} else if input.SiteID != route.parentID {
			return server.writeAccountingConfigError(writer, request, ErrInvalidMutation), true
		}
		if input.HolidayDate == "" {
			input.HolidayDate = route.id
		} else if input.HolidayDate != route.id {
			return server.writeAccountingConfigError(writer, request, ErrInvalidMutation), true
		}
		input.ActorDisplayName = request.Header.Get("X-Actor-Display-Name")
		saved, err := configWriter.UpsertSiteCalendarHoliday(request.Context(), claims, input)
		if err != nil {
			return server.writeAccountingConfigError(writer, request, err), true
		}
		writeJSON(writer, http.StatusOK, saved)
		return http.StatusOK, true

	case "accounting-holiday-delete":
		if request.URL.RawQuery != "" {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation routes do not accept query parameters.", false)
			return http.StatusBadRequest, true
		}
		configWriter, status, ok := server.accountingConfigWriter(writer, request)
		if !ok {
			return status, true
		}
		deleted, err := configWriter.DeleteSiteCalendarHoliday(request.Context(), claims, route.parentID, route.id)
		if err != nil {
			return server.writeAccountingConfigError(writer, request, err), true
		}
		writeJSON(writer, http.StatusOK, deleted)
		return http.StatusOK, true

	case "accounting-meter-ratios":
		query := request.URL.Query()
		for name, values := range query {
			if (name != "pointId" && name != "includeSuperseded") || len(values) != 1 {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The accounting meter ratios query is invalid.", false)
				return http.StatusBadRequest, true
			}
		}
		pointID := query.Get("pointId")
		if pointID != "" && !validUUIDv7(pointID) {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The accounting meter ratios pointId is invalid.", false)
			return http.StatusBadRequest, true
		}
		includeSuperseded := false
		if rawInclude := query.Get("includeSuperseded"); rawInclude != "" {
			switch rawInclude {
			case "true":
				includeSuperseded = true
			case "false":
				includeSuperseded = false
			default:
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The accounting meter ratios includeSuperseded filter is invalid.", false)
				return http.StatusBadRequest, true
			}
		}
		reader, ok := server.store.(AccountingConfigStore)
		if !ok {
			writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "The Core Registry accounting config store is not configured.", true)
			return http.StatusServiceUnavailable, true
		}
		list, err := reader.ListSiteMeterRatioVersions(request.Context(), claims, ListSiteMeterRatioVersionsInput{
			SiteID:            route.parentID,
			PointID:           pointID,
			IncludeSuperseded: includeSuperseded,
		})
		if err != nil {
			return server.writeStoreError(writer, request, err), true
		}
		writeJSON(writer, http.StatusOK, list)
		return http.StatusOK, true

	case "accounting-meter-ratio-create":
		if request.URL.RawQuery != "" {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation routes do not accept query parameters.", false)
			return http.StatusBadRequest, true
		}
		configWriter, status, ok := server.accountingConfigWriter(writer, request)
		if !ok {
			return status, true
		}
		var input CreateSiteMeterRatioVersionInput
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest, true
		}
		if input.SiteID == "" {
			input.SiteID = route.parentID
		} else if input.SiteID != route.parentID {
			return server.writeAccountingConfigError(writer, request, ErrInvalidMutation), true
		}
		input.ActorDisplayName = request.Header.Get("X-Actor-Display-Name")
		created, err := configWriter.CreateSiteMeterRatioVersion(request.Context(), claims, input)
		if err != nil {
			return server.writeAccountingConfigError(writer, request, err), true
		}
		writeJSON(writer, http.StatusCreated, created)
		return http.StatusCreated, true

	case "accounting-meter-ratio-retire":
		if request.URL.RawQuery != "" {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation routes do not accept query parameters.", false)
			return http.StatusBadRequest, true
		}
		configWriter, status, ok := server.accountingConfigWriter(writer, request)
		if !ok {
			return status, true
		}
		input, ok := decodeOptionalRetireInput(writer, request)
		if !ok {
			return http.StatusBadRequest, true
		}
		if input.SiteID == "" {
			input.SiteID = route.parentID
		} else if input.SiteID != route.parentID {
			return server.writeAccountingConfigError(writer, request, ErrInvalidMutation), true
		}
		if input.VersionID == "" {
			input.VersionID = route.id
		} else if input.VersionID != route.id {
			return server.writeAccountingConfigError(writer, request, ErrInvalidMutation), true
		}
		input.ActorDisplayName = request.Header.Get("X-Actor-Display-Name")
		retired, err := configWriter.RetireSiteMeterRatioVersion(request.Context(), claims, input)
		if err != nil {
			return server.writeAccountingConfigError(writer, request, err), true
		}
		writeJSON(writer, http.StatusOK, retired)
		return http.StatusOK, true
	default:
		return 0, false
	}
}

func (server *server) accountingConfigWriter(writer http.ResponseWriter, request *http.Request) (AccountingConfigStore, int, bool) {
	if server.writer == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_WRITER_UNAVAILABLE", "The Registry writer is not configured.", true)
		return nil, http.StatusServiceUnavailable, false
	}
	configWriter, ok := server.writer.(AccountingConfigStore)
	if !ok {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_WRITER_UNAVAILABLE", "The Registry accounting config writer is not configured.", true)
		return nil, http.StatusServiceUnavailable, false
	}
	return configWriter, 0, true
}

func decodeOptionalRetireInput(writer http.ResponseWriter, request *http.Request) (RetireSiteMeterRatioVersionInput, bool) {
	var input RetireSiteMeterRatioVersionInput
	request.Body = http.MaxBytesReader(writer, request.Body, maxRegistryMutationBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		if errors.Is(err, io.EOF) {
			return input, true
		}
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "The Registry mutation payload is invalid.", false)
		return RetireSiteMeterRatioVersionInput{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "The Registry mutation payload must contain exactly one JSON value.", false)
		return RetireSiteMeterRatioVersionInput{}, false
	}
	return input, true
}

func (server *server) writeAccountingConfigError(writer http.ResponseWriter, request *http.Request, err error) int {
	switch {
	case errors.Is(err, ErrMeterRatioOverlap):
		writeProblem(writer, request, http.StatusConflict, "METER_RATIO_OVERLAP", "The requested meter ratio version window overlaps an existing released version.", false)
		return http.StatusConflict
	case errors.Is(err, ErrMutationForbidden):
		writeProblem(writer, request, http.StatusForbidden, "ACCOUNTING_CONFIG_FORBIDDEN", "The authenticated Principal is not allowed to manage accounting configuration on this Site.", false)
		return http.StatusForbidden
	default:
		return server.writeMutationError(writer, request, err)
	}
}

var _ AccountingConfigStore = (*PostgresStore)(nil)
