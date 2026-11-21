package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type memoryAccountingConfigBackend struct {
	fakeRegistryStore
	RegistryWriter
	mu            sync.Mutex
	holidays      map[string]map[string]SiteCalendarHoliday
	ratios        map[string][]MeterRatioVersion
	jobs          map[string][]AccountingRecalcJob
	meterBindings map[string]memoryMeterBinding
	points        map[string]bool
	seq           int
}

type memoryMeterBinding struct {
	id      string
	meterID string
	pointID string
}

func newMemoryAccountingConfigBackend() *memoryAccountingConfigBackend {
	pointA := "01990000-1340-7000-8000-000000000001"
	pointB := "01990000-1340-7000-8000-000000000020"
	unboundPoint := "01990000-1340-7000-8000-000000000099"
	return &memoryAccountingConfigBackend{
		fakeRegistryStore: fakeRegistryStore{
			site: Site{
				ID: testSiteA1, TenantID: testTenantA, Code: "songyaxuan", DisplayName: "松雅轩", Timezone: "Asia/Shanghai", Status: "ACTIVE",
			},
		},
		holidays: map[string]map[string]SiteCalendarHoliday{},
		ratios: map[string][]MeterRatioVersion{
			testSiteA1: {
				{
					ID:                   "01990000-1350-7000-8000-000000000090",
					TenantID:             testTenantA,
					SiteID:               testSiteA1,
					MeterID:              strPtr("01990000-1330-7000-8000-000000000001"),
					MeterBindingID:       strPtr("01990000-1360-7000-8000-000000000001"),
					PointID:              strPtr(pointA),
					Version:              int64Ptr(1),
					CTRatio:              20,
					PTRatio:              1,
					MeterMultiplier:      1,
					CloudMultiplier:      20,
					RatioApplicationMode: RatioApplicationModePlatformApplied,
					ReadingUnit:          MeterRatioReadingUnitKWh,
					EffectiveFrom:        "2026-01-01T00:00:00.000Z",
					EffectiveTo:          strPtr("2026-06-01T00:00:00.000Z"),
					Status:               MeterRatioStatusSuperseded,
					Revision:             2,
					CreatedAt:            "2026-01-01T00:00:00.000Z",
					UpdatedAt:            "2026-06-01T00:00:00.000Z",
				},
			},
		},
		jobs: map[string][]AccountingRecalcJob{},
		meterBindings: map[string]memoryMeterBinding{
			pointA: {id: "01990000-1360-7000-8000-000000000001", meterID: "01990000-1330-7000-8000-000000000001", pointID: pointA},
			pointB: {id: "01990000-1360-7000-8000-000000000020", meterID: "01990000-1330-7000-8000-000000000020", pointID: pointB},
		},
		points: map[string]bool{
			pointA:       true,
			pointB:       true,
			unboundPoint: true,
		},
	}
}

func strPtr(v string) *string  { return &v }
func int64Ptr(v int64) *int64  { return &v }
func float64Ptr(v float64) *float64 {
	return &v
}

func (backend *memoryAccountingConfigBackend) siteVisible(claims registryauth.GrantClaims, siteID string) bool {
	if siteID != testSiteA1 {
		return false
	}
	for _, denied := range claims.DeniedSiteIDs {
		if denied == siteID {
			return false
		}
	}
	for _, allowed := range claims.AllowedSiteIDs {
		if allowed == siteID {
			return true
		}
	}
	return false
}

func (backend *memoryAccountingConfigBackend) ListAccountingRecalcJobs(_ context.Context, claims registryauth.GrantClaims, siteID string, limit int) (AccountingRecalcJobList, error) {
	if !validUUIDv7(siteID) {
		return AccountingRecalcJobList{}, ErrNotFound
	}
	if limit == 0 {
		limit = defaultAccountingRecalcJobsLimit
	}
	if limit < 1 || limit > maxAccountingRecalcJobsLimit {
		return AccountingRecalcJobList{}, ErrInvalidPage
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return AccountingRecalcJobList{}, ErrNotFound
	}
	all := append([]AccountingRecalcJob(nil), backend.jobs[siteID]...)
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt != all[j].CreatedAt {
			return all[i].CreatedAt > all[j].CreatedAt
		}
		return all[i].ID > all[j].ID
	})
	if len(all) > limit {
		all = all[:limit]
	}
	if all == nil {
		all = []AccountingRecalcJob{}
	}
	return AccountingRecalcJobList{Items: all}, nil
}

func (backend *memoryAccountingConfigBackend) appendJobLocked(claims registryauth.GrantClaims, siteID string, start, end time.Time, source, reason, actorDisplay string, now time.Time) string {
	backend.seq++
	jobID := fmt.Sprintf("01990000-9500-7000-8000-%012d", backend.seq)
	job := AccountingRecalcJob{
		ID:                 jobID,
		TenantID:           claims.TenantID,
		SiteID:             siteID,
		Granularity:        AccountingRecalcGranularityAll,
		WindowStart:        formatInstant(start),
		WindowEnd:          formatInstant(end),
		Status:             AccountingRecalcStatusPending,
		AvailableAt:        formatInstant(now),
		RequestedBy:        claims.PrincipalID,
		RequestedByDisplay: normalizeActorDisplay(actorDisplay, claims.PrincipalID),
		Reason:             reason,
		Source:             source,
		AttemptCount:       0,
		LastError:          "",
		CreatedAt:          formatInstant(now),
		UpdatedAt:          formatInstant(now),
		FinishedAt:         nil,
	}
	backend.jobs[siteID] = append(backend.jobs[siteID], job)
	return jobID
}

func (backend *memoryAccountingConfigBackend) ListSiteCalendarHolidays(_ context.Context, claims registryauth.GrantClaims, siteID string, year *int) (SiteCalendarHolidayList, error) {
	if !validUUIDv7(siteID) || !registryauth.ScopeAllows(claims, siteID) {
		return SiteCalendarHolidayList{}, ErrNotFound
	}
	if year != nil && (*year < minAccountingConfigYr || *year > maxAccountingConfigYr) {
		return SiteCalendarHolidayList{}, ErrInvalidPage
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return SiteCalendarHolidayList{}, ErrNotFound
	}
	prefix := ""
	if year != nil {
		prefix = fmt.Sprintf("%04d-", *year)
	}
	items := []SiteCalendarHoliday{}
	for _, item := range backend.holidays[siteID] {
		if prefix != "" && !strings.HasPrefix(item.HolidayDate, prefix) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].HolidayDate < items[j].HolidayDate
	})
	return SiteCalendarHolidayList{Items: items}, nil
}

func (backend *memoryAccountingConfigBackend) UpsertSiteCalendarHoliday(_ context.Context, claims registryauth.GrantClaims, input UpsertSiteCalendarHolidayInput) (SiteCalendarHoliday, error) {
	if !billingManageAllowed(claims) {
		return SiteCalendarHoliday{}, ErrMutationForbidden
	}
	if !validUUIDv7(input.SiteID) || !registryauth.ScopeAllows(claims, input.SiteID) {
		return SiteCalendarHoliday{}, ErrNotFound
	}
	normalized, err := input.normalize()
	if err != nil {
		return SiteCalendarHoliday{}, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, normalized.siteID) {
		return SiteCalendarHoliday{}, ErrNotFound
	}
	windowStart, windowEnd, err := siteDateRangeToUTC(backend.site.Timezone, normalized.parsedDate, normalized.parsedDate)
	if err != nil {
		return SiteCalendarHoliday{}, err
	}
	if backend.holidays[normalized.siteID] == nil {
		backend.holidays[normalized.siteID] = map[string]SiteCalendarHoliday{}
	}
	backend.seq++
	now := time.Date(2026, 10, 12, 8, 0, backend.seq, 0, time.UTC)
	existing, exists := backend.holidays[normalized.siteID][normalized.holidayDate]
	id := existing.ID
	createdAt := existing.CreatedAt
	rev := int64(1)
	if exists {
		rev = existing.Revision + 1
	} else {
		id = fmt.Sprintf("01990000-9600-7000-8000-%012d", backend.seq)
		createdAt = formatInstant(now)
	}
	jobID := backend.appendJobLocked(
		claims,
		normalized.siteID,
		windowStart,
		windowEnd,
		AccountingRecalcSourceTariffChange,
		fmt.Sprintf("站点日历覆写变更(%s:%s)", normalized.holidayDate, normalized.dayType),
		normalized.actorDisplayName,
		now,
	)
	saved := SiteCalendarHoliday{
		ID:          id,
		TenantID:    claims.TenantID,
		SiteID:      normalized.siteID,
		HolidayDate: normalized.holidayDate,
		DayType:     normalized.dayType,
		Label:       normalized.label,
		Revision:    rev,
		CreatedAt:   createdAt,
		UpdatedAt:   formatInstant(now),
	}
	backend.holidays[normalized.siteID][normalized.holidayDate] = saved
	saved.RecalcJobID = jobID
	return saved, nil
}

func (backend *memoryAccountingConfigBackend) DeleteSiteCalendarHoliday(_ context.Context, claims registryauth.GrantClaims, siteID string, holidayDate string) (SiteCalendarHolidayDeleteResult, error) {
	if !billingManageAllowed(claims) {
		return SiteCalendarHolidayDeleteResult{}, ErrMutationForbidden
	}
	if !validUUIDv7(siteID) || !registryauth.ScopeAllows(claims, siteID) {
		return SiteCalendarHolidayDeleteResult{}, ErrNotFound
	}
	parsedDate, ok := parseISODate(holidayDate)
	if !ok {
		return SiteCalendarHolidayDeleteResult{}, ErrInvalidMutation
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return SiteCalendarHolidayDeleteResult{}, ErrNotFound
	}
	if _, exists := backend.holidays[siteID][holidayDate]; !exists {
		return SiteCalendarHolidayDeleteResult{}, ErrNotFound
	}
	windowStart, windowEnd, err := siteDateRangeToUTC(backend.site.Timezone, parsedDate, parsedDate)
	if err != nil {
		return SiteCalendarHolidayDeleteResult{}, err
	}
	delete(backend.holidays[siteID], holidayDate)
	backend.seq++
	now := time.Date(2026, 10, 12, 8, 0, backend.seq, 0, time.UTC)
	jobID := backend.appendJobLocked(
		claims,
		siteID,
		windowStart,
		windowEnd,
		AccountingRecalcSourceTariffChange,
		fmt.Sprintf("删除站点日历覆写(%s)", holidayDate),
		"",
		now,
	)
	return SiteCalendarHolidayDeleteResult{
		SiteID:      siteID,
		HolidayDate: holidayDate,
		Deleted:     true,
		RecalcJobID: jobID,
	}, nil
}

func (backend *memoryAccountingConfigBackend) ListSiteMeterRatioVersions(_ context.Context, claims registryauth.GrantClaims, input ListSiteMeterRatioVersionsInput) (MeterRatioVersionList, error) {
	if !validUUIDv7(input.SiteID) || !registryauth.ScopeAllows(claims, input.SiteID) {
		return MeterRatioVersionList{}, ErrNotFound
	}
	if input.PointID != "" && !validUUIDv7(input.PointID) {
		return MeterRatioVersionList{}, ErrInvalidPage
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return MeterRatioVersionList{}, ErrNotFound
	}
	items := []MeterRatioVersion{}
	for _, item := range backend.ratios[input.SiteID] {
		if !input.IncludeSuperseded && item.Status == MeterRatioStatusSuperseded {
			continue
		}
		if input.PointID != "" && (item.PointID == nil || *item.PointID != input.PointID) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].EffectiveFrom != items[j].EffectiveFrom {
			return items[i].EffectiveFrom > items[j].EffectiveFrom
		}
		return items[i].ID > items[j].ID
	})
	return MeterRatioVersionList{Items: items}, nil
}

func (backend *memoryAccountingConfigBackend) ratioOverlapsLocked(siteID, excludeID, meterID, pointID string, start time.Time, end *time.Time) bool {
	endOrMax := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	if end != nil {
		endOrMax = *end
	}
	for _, existing := range backend.ratios[siteID] {
		if existing.ID == excludeID || existing.Status != MeterRatioStatusReleased {
			continue
		}
		sameMeter := meterID != "" && existing.MeterID != nil && *existing.MeterID == meterID
		samePoint := pointID != "" && existing.PointID != nil && *existing.PointID == pointID
		if !sameMeter && !samePoint {
			continue
		}
		exStart, _ := time.Parse(time.RFC3339Nano, existing.EffectiveFrom)
		exEnd := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		if existing.EffectiveTo != nil {
			exEnd, _ = time.Parse(time.RFC3339Nano, *existing.EffectiveTo)
		}
		if exStart.Before(endOrMax) && start.Before(exEnd) {
			return true
		}
	}
	return false
}

func (backend *memoryAccountingConfigBackend) CreateSiteMeterRatioVersion(_ context.Context, claims registryauth.GrantClaims, input CreateSiteMeterRatioVersionInput) (MeterRatioVersion, error) {
	if !billingManageAllowed(claims) {
		return MeterRatioVersion{}, ErrMutationForbidden
	}
	if !validUUIDv7(input.SiteID) || !registryauth.ScopeAllows(claims, input.SiteID) {
		return MeterRatioVersion{}, ErrNotFound
	}
	normalized, err := input.normalize()
	if err != nil {
		return MeterRatioVersion{}, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, normalized.siteID) {
		return MeterRatioVersion{}, ErrNotFound
	}
	if !backend.points[normalized.pointID] {
		return MeterRatioVersion{}, ErrNotFound
	}
	var meterID, meterBindingID string
	if normalized.meterBindingID != nil {
		found := false
		for _, mb := range backend.meterBindings {
			if mb.id == *normalized.meterBindingID {
				if mb.pointID != normalized.pointID {
					return MeterRatioVersion{}, ErrInvalidMutation
				}
				if normalized.meterID != nil && *normalized.meterID != mb.meterID {
					return MeterRatioVersion{}, ErrInvalidMutation
				}
				meterID = mb.meterID
				meterBindingID = mb.id
				found = true
				break
			}
		}
		if !found {
			return MeterRatioVersion{}, ErrNotFound
		}
	} else if mb, ok := backend.meterBindings[normalized.pointID]; ok {
		if normalized.meterID != nil && *normalized.meterID != mb.meterID {
			return MeterRatioVersion{}, ErrInvalidMutation
		}
		meterID = mb.meterID
		meterBindingID = mb.id
	} else if normalized.meterID != nil {
		meterID = *normalized.meterID
	} else {
		return MeterRatioVersion{}, ErrInvalidMutation
	}

	if backend.ratioOverlapsLocked(normalized.siteID, "", meterID, normalized.pointID, normalized.effectiveFrom, normalized.effectiveTo) {
		return MeterRatioVersion{}, ErrMeterRatioOverlap
	}

	var maxVer int64
	for _, existing := range backend.ratios[normalized.siteID] {
		if existing.Version == nil {
			continue
		}
		if (meterID != "" && existing.MeterID != nil && *existing.MeterID == meterID) ||
			(meterID == "" && existing.PointID != nil && *existing.PointID == normalized.pointID) {
			if *existing.Version > maxVer {
				maxVer = *existing.Version
			}
		}
	}
	nextVer := maxVer + 1

	backend.seq++
	now := time.Date(2026, 10, 12, 8, 0, backend.seq, 0, time.UTC)
	rowID := fmt.Sprintf("01990000-9700-7000-8000-%012d", backend.seq)
	var meterIDPtr, bindingIDPtr, effectiveToPtr *string
	if meterID != "" {
		meterIDPtr = &meterID
	}
	if meterBindingID != "" {
		bindingIDPtr = &meterBindingID
	}
	if normalized.effectiveTo != nil {
		formatted := formatInstant(*normalized.effectiveTo)
		effectiveToPtr = &formatted
	}
	pointCopy := normalized.pointID
	verCopy := nextVer
	windowStart, windowEnd := ratioDirtyWindowBounds(normalized.effectiveFrom, normalized.effectiveTo, now)
	jobID := backend.appendJobLocked(
		claims,
		normalized.siteID,
		windowStart,
		windowEnd,
		AccountingRecalcSourceRatioChange,
		fmt.Sprintf("新增计量变比版本(point=%s,v=%d)", normalized.pointID, nextVer),
		normalized.actorDisplayName,
		now,
	)

	saved := MeterRatioVersion{
		ID:                   rowID,
		TenantID:             claims.TenantID,
		SiteID:               normalized.siteID,
		MeterID:              meterIDPtr,
		MeterBindingID:       bindingIDPtr,
		PointID:              &pointCopy,
		Version:              &verCopy,
		CTRatio:              normalized.ctRatio,
		PTRatio:              normalized.ptRatio,
		MeterMultiplier:      normalized.meterMultiplier,
		CloudMultiplier:      computeCloudMultiplier(normalized.ctRatio, normalized.ptRatio, normalized.meterMultiplier, normalized.ratioApplicationMode),
		RatioApplicationMode: normalized.ratioApplicationMode,
		ReadingUnit:          normalized.readingUnit,
		EffectiveFrom:        formatInstant(normalized.effectiveFrom),
		EffectiveTo:          effectiveToPtr,
		Status:               MeterRatioStatusReleased,
		Revision:             1,
		CreatedAt:            formatInstant(now),
		UpdatedAt:            formatInstant(now),
	}
	backend.ratios[normalized.siteID] = append(backend.ratios[normalized.siteID], saved)
	saved.RecalcJobID = jobID
	return saved, nil
}

func (backend *memoryAccountingConfigBackend) RetireSiteMeterRatioVersion(_ context.Context, claims registryauth.GrantClaims, input RetireSiteMeterRatioVersionInput) (MeterRatioVersion, error) {
	if !billingManageAllowed(claims) {
		return MeterRatioVersion{}, ErrMutationForbidden
	}
	if !validUUIDv7(input.SiteID) || !validUUIDv7(input.VersionID) || !registryauth.ScopeAllows(claims, input.SiteID) {
		return MeterRatioVersion{}, ErrNotFound
	}
	parsedEffectiveTo, err := input.parseEffectiveTo()
	if err != nil {
		return MeterRatioVersion{}, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return MeterRatioVersion{}, ErrNotFound
	}
	list := backend.ratios[input.SiteID]
	targetIdx := -1
	for i := range list {
		if list[i].ID == input.VersionID {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		return MeterRatioVersion{}, ErrNotFound
	}
	current := list[targetIdx]
	if current.Status != MeterRatioStatusReleased {
		return MeterRatioVersion{}, ErrBindingConflict
	}
	effectiveFrom, _ := time.Parse(time.RFC3339Nano, current.EffectiveFrom)
	backend.seq++
	now := time.Date(2026, 10, 12, 8, 0, backend.seq, 0, time.UTC)
	effectiveTo := now
	if parsedEffectiveTo != nil {
		effectiveTo = *parsedEffectiveTo
	}
	if !effectiveTo.After(effectiveFrom) {
		return MeterRatioVersion{}, ErrInvalidMutation
	}
	var meterID, pointID string
	if current.MeterID != nil {
		meterID = *current.MeterID
	}
	if current.PointID != nil {
		pointID = *current.PointID
	}
	if backend.ratioOverlapsLocked(input.SiteID, current.ID, meterID, pointID, effectiveFrom, &effectiveTo) {
		return MeterRatioVersion{}, ErrMeterRatioOverlap
	}
	formattedTo := formatInstant(effectiveTo)
	current.EffectiveTo = &formattedTo
	current.Status = MeterRatioStatusReleased
	current.Revision++
	current.UpdatedAt = formatInstant(now)
	list[targetIdx] = current
	backend.ratios[input.SiteID] = list

	windowStart, windowEnd := ratioDirtyWindowBounds(effectiveFrom, &effectiveTo, now)
	jobID := backend.appendJobLocked(
		claims,
		input.SiteID,
		windowStart,
		windowEnd,
		AccountingRecalcSourceRatioChange,
		fmt.Sprintf("终止计量变比版本生效窗口(point=%s,id=%s)", pointID, input.VersionID),
		input.ActorDisplayName,
		now,
	)
	current.RecalcJobID = jobID
	return current, nil
}

func TestAccountingConfigValidationAndErrorMapping(t *testing.T) {
	if err := (UpsertSiteCalendarHolidayInput{
		SiteID:      testSiteA1,
		HolidayDate: "2026-10-01",
		DayType:     HolidayDayTypeHoliday,
		Label:       "国庆节",
	}).Validate(); err != nil {
		t.Fatalf("valid holiday input rejected: %v", err)
	}
	for _, tc := range []struct {
		name  string
		input UpsertSiteCalendarHolidayInput
	}{
		{"invalid date", UpsertSiteCalendarHolidayInput{SiteID: testSiteA1, HolidayDate: "2026-02-30", DayType: HolidayDayTypeHoliday}},
		{"bad dayType", UpsertSiteCalendarHolidayInput{SiteID: testSiteA1, HolidayDate: "2026-10-01", DayType: "FESTIVAL"}},
		{"label over 256 runes", UpsertSiteCalendarHolidayInput{SiteID: testSiteA1, HolidayDate: "2026-10-01", DayType: HolidayDayTypeHoliday, Label: strings.Repeat("节", 257)}},
	} {
		if err := tc.input.Validate(); !errors.Is(err, ErrInvalidMutation) {
			t.Fatalf("%s err=%v, want ErrInvalidMutation", tc.name, err)
		}
	}

	validPoint := "01990000-1340-7000-8000-000000000001"
	validRatio := CreateSiteMeterRatioVersionInput{
		SiteID:               testSiteA1,
		PointID:              validPoint,
		CTRatio:              40,
		PTRatio:              1,
		RatioApplicationMode: RatioApplicationModePlatformApplied,
		EffectiveFrom:        "2026-08-01T00:00:00Z",
	}
	normalizedRatio, err := validRatio.normalize()
	if err != nil {
		t.Fatalf("valid ratio normalize failed: %v", err)
	}
	if normalizedRatio.meterMultiplier != 1 || normalizedRatio.readingUnit != MeterRatioReadingUnitKWh {
		t.Fatalf("defaults not applied: %#v", normalizedRatio)
	}
	for _, tc := range []struct {
		name  string
		build func() CreateSiteMeterRatioVersionInput
	}{
		{"bad pointId", func() CreateSiteMeterRatioVersionInput { r := validRatio; r.PointID = "not-uuid"; return r }},
		{"zero ctRatio", func() CreateSiteMeterRatioVersionInput { r := validRatio; r.CTRatio = 0; return r }},
		{"negative ptRatio", func() CreateSiteMeterRatioVersionInput { r := validRatio; r.PTRatio = -1; return r }},
		{"zero meterMultiplier", func() CreateSiteMeterRatioVersionInput { r := validRatio; r.MeterMultiplier = float64Ptr(0); return r }},
		{"bad ratioApplicationMode", func() CreateSiteMeterRatioVersionInput { r := validRatio; r.RatioApplicationMode = "CLOUD"; return r }},
		{"bad readingUnit", func() CreateSiteMeterRatioVersionInput { r := validRatio; r.ReadingUnit = "kW"; return r }},
		{"bad effectiveFrom", func() CreateSiteMeterRatioVersionInput { r := validRatio; r.EffectiveFrom = "2026-08-01"; return r }},
		{"effectiveTo equals effectiveFrom", func() CreateSiteMeterRatioVersionInput { r := validRatio; r.EffectiveTo = strPtr("2026-08-01T00:00:00Z"); return r }},
		{"effectiveTo before effectiveFrom", func() CreateSiteMeterRatioVersionInput { r := validRatio; r.EffectiveTo = strPtr("2026-07-31T23:59:59Z"); return r }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.build().Validate(); !errors.Is(err, ErrInvalidMutation) {
				t.Fatalf("%s err=%v, want ErrInvalidMutation", tc.name, err)
			}
		})
	}

	// Future ratio dirty window uses [effectiveFrom, effectiveFrom+1h).
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	futureFrom := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	wStart, wEnd := ratioDirtyWindowBounds(futureFrom, nil, now)
	if !wStart.Equal(futureFrom) || !wEnd.Equal(futureFrom.Add(time.Hour)) {
		t.Fatalf("future dirty window = [%s, %s)", wStart, wEnd)
	}
	// Historical ratio dirty window uses [effectiveFrom, min(now, effectiveTo)).
	pastFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	pastTo := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	wStart, wEnd = ratioDirtyWindowBounds(pastFrom, &pastTo, now)
	if !wStart.Equal(pastFrom) || !wEnd.Equal(pastTo) {
		t.Fatalf("historical bounded dirty window = [%s, %s)", wStart, wEnd)
	}
	futureTo := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	wStart, wEnd = ratioDirtyWindowBounds(pastFrom, &futureTo, now)
	if !wStart.Equal(pastFrom) || !wEnd.Equal(now) {
		t.Fatalf("active dirty window = [%s, %s)", wStart, wEnd)
	}

	// PG SQLSTATE 23514 / 23P01 map to ErrMeterRatioOverlap.
	if err := mapMeterRatioWriteError(&pgconn.PgError{Code: "23514", Message: "released Meter ratio versions cannot overlap"}); !errors.Is(err, ErrMeterRatioOverlap) {
		t.Fatalf("23514 overlap mapped to %v, want ErrMeterRatioOverlap", err)
	}
	if err := mapMeterRatioWriteError(&pgconn.PgError{Code: "23P01", Message: "overlapping released meter_ratio_versions window"}); !errors.Is(err, ErrMeterRatioOverlap) {
		t.Fatalf("23P01 overlap mapped to %v, want ErrMeterRatioOverlap", err)
	}
	if err := mapMeterRatioWriteError(&pgconn.PgError{Code: "23514", ConstraintName: "meter_ratio_versions_target_check"}); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("23514 target check mapped to %v, want ErrInvalidMutation", err)
	}
	if err := mapMeterRatioWriteError(&pgconn.PgError{Code: "23503"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("23503 FK mapped to %v, want ErrNotFound", err)
	}
}

func TestCoreHTTPSiteCalendarHolidaysCRUDAndAutoRecalc(t *testing.T) {
	backend := newMemoryAccountingConfigBackend()
	client := newScheduleHTTPClientWithInterfaces(t, backend, backend)
	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	billingClaims := testGrantClaims(registryauth.ActionBillingManage)
	otherClaims := testGrantClaims(registryauth.ActionDeviceWrite)

	basePath := RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/holidays"

	// Unauthorized action checks.
	if resp := client.do(t, http.MethodPut, basePath+"/2026-10-01", readClaims, "", map[string]any{
		"dayType": "HOLIDAY",
		"label":   "国庆节",
	}); resp.Code != http.StatusForbidden {
		t.Fatalf("site.read PUT holiday status=%d, want 403", resp.Code)
	}
	if resp := client.do(t, http.MethodDelete, basePath+"/2026-10-01", readClaims, "", nil); resp.Code != http.StatusForbidden {
		t.Fatalf("site.read DELETE holiday status=%d, want 403", resp.Code)
	}
	if resp := client.do(t, http.MethodGet, basePath, otherClaims, "", nil); resp.Code != http.StatusForbidden {
		t.Fatalf("device.write GET holidays status=%d, want 403", resp.Code)
	}

	// Bad date and mismatched body checks.
	if resp := client.do(t, http.MethodPut, basePath+"/2026-02-30", billingClaims, "", map[string]any{
		"dayType": "HOLIDAY",
		"label":   "非法日期",
	}); resp.Code != http.StatusBadRequest {
		t.Fatalf("invalid date PUT status=%d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	if resp := client.do(t, http.MethodPut, basePath+"/2026-10-01", billingClaims, "", map[string]any{
		"holidayDate": "2026-10-02",
		"dayType":     "HOLIDAY",
		"label":       "日期不一致",
	}); resp.Code != http.StatusBadRequest {
		t.Fatalf("mismatched holidayDate status=%d, want 400", resp.Code)
	}

	// Upsert create (rev 1).
	put1 := client.do(t, http.MethodPut, basePath+"/2026-10-01", billingClaims, "电价管理员", map[string]any{
		"dayType": "HOLIDAY",
		"label":   "  国庆节  ",
	})
	if put1.Code != http.StatusOK {
		t.Fatalf("PUT holiday create status=%d, want 200; body=%s", put1.Code, put1.Body.String())
	}
	var rawPut1 map[string]any
	if err := json.Unmarshal(put1.Body.Bytes(), &rawPut1); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "tenantId", "siteId", "holidayDate", "dayType", "label", "revision", "createdAt", "updatedAt", "recalcJobId"} {
		if _, ok := rawPut1[key]; !ok {
			t.Fatalf("PUT holiday response missing wire field %q: %s", key, put1.Body.String())
		}
	}
	var h1 SiteCalendarHoliday
	if err := json.Unmarshal(put1.Body.Bytes(), &h1); err != nil {
		t.Fatal(err)
	}
	if h1.HolidayDate != "2026-10-01" || h1.DayType != "HOLIDAY" || h1.Label != "国庆节" || h1.Revision != 1 || h1.RecalcJobID == "" {
		t.Fatalf("unexpected h1: %#v", h1)
	}

	// Upsert update (rev 2) + second year entry.
	put2 := client.do(t, http.MethodPut, basePath+"/2026-10-01", billingClaims, "电价管理员", map[string]any{
		"dayType": "SPECIAL_DAY",
		"label":   "国庆调休特殊日",
	})
	if put2.Code != http.StatusOK {
		t.Fatalf("PUT holiday update status=%d, want 200; body=%s", put2.Code, put2.Body.String())
	}
	var h2 SiteCalendarHoliday
	if err := json.Unmarshal(put2.Body.Bytes(), &h2); err != nil {
		t.Fatal(err)
	}
	if h2.Revision != 2 || h2.DayType != "SPECIAL_DAY" || h2.RecalcJobID == "" || h2.RecalcJobID == h1.RecalcJobID {
		t.Fatalf("unexpected h2: %#v", h2)
	}

	if put2025 := client.do(t, http.MethodPut, basePath+"/2025-10-01", billingClaims, "电价管理员", map[string]any{
		"dayType": "HOLIDAY",
		"label":   "2025国庆",
	}); put2025.Code != http.StatusOK {
		t.Fatalf("PUT 2025 holiday status=%d", put2025.Code)
	}

	// List with ?year=2026 and invalid ?year.
	if badYear := client.do(t, http.MethodGet, basePath+"?year=26", readClaims, "", nil); badYear.Code != http.StatusBadRequest {
		t.Fatalf("bad year status=%d, want 400", badYear.Code)
	}
	list2026 := client.do(t, http.MethodGet, basePath+"?year=2026", readClaims, "", nil)
	if list2026.Code != http.StatusOK {
		t.Fatalf("GET holidays?year=2026 status=%d, want 200; body=%s", list2026.Code, list2026.Body.String())
	}
	var listed SiteCalendarHolidayList
	if err := json.Unmarshal(list2026.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].HolidayDate != "2026-10-01" || listed.Items[0].Revision != 2 {
		t.Fatalf("unexpected 2026 holidays: %#v", listed.Items)
	}

	// Delete missing returns 404 without enqueuing recalc.
	jobsBeforeDelete := len(backend.jobs[testSiteA1])
	delMissing := client.do(t, http.MethodDelete, basePath+"/2026-10-02", billingClaims, "", nil)
	if delMissing.Code != http.StatusNotFound {
		t.Fatalf("DELETE missing holiday status=%d, want 404; body=%s", delMissing.Code, delMissing.Body.String())
	}
	if len(backend.jobs[testSiteA1]) != jobsBeforeDelete {
		t.Fatalf("DELETE 404 should not enqueue recalc job")
	}

	// Delete existing returns deleted:true and recalcJobId.
	delResp := client.do(t, http.MethodDelete, basePath+"/2026-10-01", billingClaims, "", nil)
	if delResp.Code != http.StatusOK {
		t.Fatalf("DELETE holiday status=%d, want 200; body=%s", delResp.Code, delResp.Body.String())
	}
	var delResult SiteCalendarHolidayDeleteResult
	if err := json.Unmarshal(delResp.Body.Bytes(), &delResult); err != nil {
		t.Fatal(err)
	}
	if !delResult.Deleted || delResult.HolidayDate != "2026-10-01" || delResult.RecalcJobID == "" {
		t.Fatalf("unexpected delete result: %#v", delResult)
	}

	// Verify recalc dirty windows recorded with TARIFF_CHANGE, ALL, Asia/Shanghai [date 00:00, date+1 00:00).
	jobsResp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/recalculate-jobs", readClaims, "", nil)
	if jobsResp.Code != http.StatusOK {
		t.Fatalf("GET recalculate-jobs status=%d", jobsResp.Code)
	}
	var jobs AccountingRecalcJobList
	if err := json.Unmarshal(jobsResp.Body.Bytes(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 4 {
		t.Fatalf("expected 4 recalc jobs, got %d", len(jobs.Items))
	}
	latestJob := jobs.Items[0]
	if latestJob.Source != AccountingRecalcSourceTariffChange || latestJob.Granularity != AccountingRecalcGranularityAll ||
		latestJob.WindowStart != "2026-09-30T16:00:00.000Z" || latestJob.WindowEnd != "2026-10-01T16:00:00.000Z" {
		t.Fatalf("unexpected latest holiday dirty window job: %#v", latestJob)
	}
}

func TestCoreHTTPMeterRatiosCRUDConflictRetireAndAutoRecalc(t *testing.T) {
	backend := newMemoryAccountingConfigBackend()
	client := newScheduleHTTPClientWithInterfaces(t, backend, backend)
	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	billingClaims := testGrantClaims(registryauth.ActionBillingManage)

	basePath := RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/meter-ratios"
	pointA := "01990000-1340-7000-8000-000000000001"
	pointB := "01990000-1340-7000-8000-000000000020"

	// Default list excludes SUPERSEDED; includeSuperseded=true includes it.
	listDefault := client.do(t, http.MethodGet, basePath, readClaims, "", nil)
	if listDefault.Code != http.StatusOK {
		t.Fatalf("GET meter-ratios status=%d; body=%s", listDefault.Code, listDefault.Body.String())
	}
	var defaultRatios MeterRatioVersionList
	if err := json.Unmarshal(listDefault.Body.Bytes(), &defaultRatios); err != nil {
		t.Fatal(err)
	}
	if len(defaultRatios.Items) != 0 {
		t.Fatalf("expected 0 non-superseded ratios initially, got %#v", defaultRatios.Items)
	}
	listAll := client.do(t, http.MethodGet, basePath+"?includeSuperseded=true&pointId="+pointA, readClaims, "", nil)
	if listAll.Code != http.StatusOK {
		t.Fatalf("GET meter-ratios includeSuperseded status=%d; body=%s", listAll.Code, listAll.Body.String())
	}
	var allRatios MeterRatioVersionList
	if err := json.Unmarshal(listAll.Body.Bytes(), &allRatios); err != nil {
		t.Fatal(err)
	}
	if len(allRatios.Items) != 1 || allRatios.Items[0].Status != MeterRatioStatusSuperseded {
		t.Fatalf("expected 1 superseded ratio, got %#v", allRatios.Items)
	}
	if badQuery := client.do(t, http.MethodGet, basePath+"?includeSuperseded=yes", readClaims, "", nil); badQuery.Code != http.StatusBadRequest {
		t.Fatalf("bad includeSuperseded status=%d, want 400", badQuery.Code)
	}
	if badPoint := client.do(t, http.MethodGet, basePath+"?pointId=bad", readClaims, "", nil); badPoint.Code != http.StatusBadRequest {
		t.Fatalf("bad pointId status=%d, want 400", badPoint.Code)
	}

	// site.read cannot POST create or retire.
	if forbid := client.do(t, http.MethodPost, basePath, readClaims, "", map[string]any{
		"pointId":              pointA,
		"ctRatio":              40,
		"ptRatio":              1,
		"ratioApplicationMode": "PLATFORM_APPLIED",
		"effectiveFrom":        "2026-08-01T00:00:00Z",
	}); forbid.Code != http.StatusForbidden {
		t.Fatalf("site.read POST meter-ratios status=%d, want 403", forbid.Code)
	}

	// Create first RELEASED version (defaults meterMultiplier=1, readingUnit=kWh, populates meterId & meterBindingId).
	createResp1 := client.do(t, http.MethodPost, basePath, billingClaims, "计量专工", map[string]any{
		"pointId":              pointA,
		"ctRatio":              40,
		"ptRatio":              1.5,
		"ratioApplicationMode": "PLATFORM_APPLIED",
		"effectiveFrom":        "2026-08-01T00:00:00Z",
		"effectiveTo":          "2026-09-01T00:00:00Z",
	})
	if createResp1.Code != http.StatusCreated {
		t.Fatalf("POST meter-ratios status=%d, want 201; body=%s", createResp1.Code, createResp1.Body.String())
	}
	var rawCreate1 map[string]any
	if err := json.Unmarshal(createResp1.Body.Bytes(), &rawCreate1); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"id", "tenantId", "siteId", "meterId", "meterBindingId", "pointId", "version",
		"ctRatio", "ptRatio", "meterMultiplier", "cloudMultiplier", "ratioApplicationMode",
		"readingUnit", "effectiveFrom", "effectiveTo", "status", "revision", "createdAt", "updatedAt", "recalcJobId",
	} {
		if _, ok := rawCreate1[key]; !ok {
			t.Fatalf("POST meter-ratios response missing wire field %q: %s", key, createResp1.Body.String())
		}
	}
	var v1 MeterRatioVersion
	if err := json.Unmarshal(createResp1.Body.Bytes(), &v1); err != nil {
		t.Fatal(err)
	}
	if v1.Status != MeterRatioStatusReleased || v1.MeterMultiplier != 1 || v1.ReadingUnit != "kWh" ||
		v1.CloudMultiplier != 60 || v1.MeterID == nil || *v1.MeterID != "01990000-1330-7000-8000-000000000001" ||
		v1.MeterBindingID == nil || *v1.MeterBindingID != "01990000-1360-7000-8000-000000000001" ||
		v1.Version == nil || *v1.Version != 2 || v1.RecalcJobID == "" {
		t.Fatalf("unexpected v1: %#v", v1)
	}

	// Overlapping window -> 409 METER_RATIO_OVERLAP.
	overlapResp := client.do(t, http.MethodPost, basePath, billingClaims, "计量专工", map[string]any{
		"pointId":              pointA,
		"ctRatio":              50,
		"ptRatio":              1,
		"ratioApplicationMode": "PLATFORM_APPLIED",
		"effectiveFrom":        "2026-08-15T00:00:00Z",
	})
	if overlapResp.Code != http.StatusConflict || !strings.Contains(overlapResp.Body.String(), `"code":"METER_RATIO_OVERLAP"`) {
		t.Fatalf("overlap POST status=%d, want 409 METER_RATIO_OVERLAP; body=%s", overlapResp.Code, overlapResp.Body.String())
	}

	// Non-overlapping open-ended window on pointA + EDGE_APPLIED on pointB (cloudMultiplier = 1).
	createResp2 := client.do(t, http.MethodPost, basePath, billingClaims, "计量专工", map[string]any{
		"pointId":              pointA,
		"ctRatio":              80,
		"ptRatio":              1,
		"meterMultiplier":      2,
		"ratioApplicationMode": "PLATFORM_APPLIED",
		"readingUnit":          "MWh",
		"effectiveFrom":        "2026-09-01T00:00:00Z",
	})
	if createResp2.Code != http.StatusCreated {
		t.Fatalf("POST v2 status=%d, want 201; body=%s", createResp2.Code, createResp2.Body.String())
	}
	var v2 MeterRatioVersion
	if err := json.Unmarshal(createResp2.Body.Bytes(), &v2); err != nil {
		t.Fatal(err)
	}
	if v2.Version == nil || *v2.Version != 3 || v2.CloudMultiplier != 160 || v2.ReadingUnit != "MWh" {
		t.Fatalf("unexpected v2: %#v", v2)
	}

	// Retire v2 with invalid effectiveTo <= effectiveFrom -> 400.
	badRetire := client.do(t, http.MethodPost, basePath+"/"+v2.ID+"/retire", billingClaims, "计量专工", map[string]any{
		"effectiveTo": "2026-09-01T00:00:00Z",
	})
	if badRetire.Code != http.StatusBadRequest {
		t.Fatalf("bad retire effectiveTo status=%d, want 400; body=%s", badRetire.Code, badRetire.Body.String())
	}

	// Retire v1 extending effectiveTo into v2 -> 409 METER_RATIO_OVERLAP.
	overlapRetire := client.do(t, http.MethodPost, basePath+"/"+v1.ID+"/retire", billingClaims, "计量专工", map[string]any{
		"effectiveTo": "2026-09-10T00:00:00Z",
	})
	if overlapRetire.Code != http.StatusConflict || !strings.Contains(overlapRetire.Body.String(), `"code":"METER_RATIO_OVERLAP"`) {
		t.Fatalf("overlap retire status=%d, want 409 METER_RATIO_OVERLAP; body=%s", overlapRetire.Code, overlapRetire.Body.String())
	}

	// Retire v2 cleanly: keeps status RELEASED, bumps revision, sets effectiveTo & recalcJobId.
	retireResp := client.do(t, http.MethodPost, basePath+"/"+v2.ID+"/retire", billingClaims, "计量专工", map[string]any{
		"effectiveTo": "2026-10-01T00:00:00Z",
	})
	if retireResp.Code != http.StatusOK {
		t.Fatalf("retire v2 status=%d, want 200; body=%s", retireResp.Code, retireResp.Body.String())
	}
	var retired MeterRatioVersion
	if err := json.Unmarshal(retireResp.Body.Bytes(), &retired); err != nil {
		t.Fatal(err)
	}
	if retired.Status != MeterRatioStatusReleased || retired.Revision != 2 ||
		retired.EffectiveTo == nil || *retired.EffectiveTo != "2026-10-01T00:00:00.000Z" ||
		retired.RecalcJobID == "" {
		t.Fatalf("unexpected retired v2: %#v", retired)
	}

	// Create EDGE_APPLIED on pointB (future window -> [effectiveFrom, effectiveFrom + 1h)).
	createPointB := client.do(t, http.MethodPost, basePath, billingClaims, "计量专工", map[string]any{
		"pointId":              pointB,
		"ctRatio":              100,
		"ptRatio":              1,
		"ratioApplicationMode": "EDGE_APPLIED",
		"effectiveFrom":        "2099-01-01T00:00:00Z",
	})
	if createPointB.Code != http.StatusCreated {
		t.Fatalf("POST pointB status=%d, want 201; body=%s", createPointB.Code, createPointB.Body.String())
	}
	var vB MeterRatioVersion
	if err := json.Unmarshal(createPointB.Body.Bytes(), &vB); err != nil {
		t.Fatal(err)
	}
	if vB.CloudMultiplier != 1 {
		t.Fatalf("EDGE_APPLIED cloudMultiplier = %v, want 1", vB.CloudMultiplier)
	}

	// Empty-body retire on missing version returns 404.
	reqMissing := httptest.NewRequest(http.MethodPost, basePath+"/01990000-9999-7000-8000-000000000001/retire", bytes.NewBuffer(nil))
	claims := billingClaims
	claims.IssuedAt = client.now.Unix()
	claims.ExpiresAt = client.now.Add(20 * time.Second).Unix()
	claims.Presenter = testPresenter
	grant, err := registryauth.SignGrant(client.signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	reqMissing.Header.Set("X-Delegation-Grant", grant)
	peerURI, _ := url.Parse(testPresenter)
	peer := &x509.Certificate{URIs: []*url.URL{peerURI}}
	reqMissing.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{peer}, VerifiedChains: [][]*x509.Certificate{{peer}}}
	recMissing := httptest.NewRecorder()
	client.handler.ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusNotFound {
		t.Fatalf("retire missing version status=%d, want 404; body=%s", recMissing.Code, recMissing.Body.String())
	}

	// Check recalc dirty window on pointB future create.
	latestJob := backend.jobs[testSiteA1][len(backend.jobs[testSiteA1])-1]
	if latestJob.Source != AccountingRecalcSourceRatioChange || latestJob.Granularity != AccountingRecalcGranularityAll ||
		latestJob.WindowStart != "2099-01-01T00:00:00.000Z" || latestJob.WindowEnd != "2099-01-01T01:00:00.000Z" {
		t.Fatalf("unexpected future ratio dirty window: %#v", latestJob)
	}
}

func TestPostgresAccountingConfigHolidaysAndMeterRatios(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)

	readClaims := writerIntegrationClaims(registryauth.ActionSiteRead, testSiteA1)
	manageClaims := writerIntegrationClaims(registryauth.ActionBillingManage, testSiteA1)

	// 1. Holidays CRUD + auto-recalc dirty windows.
	holidayDate := "2027-05-01"
	upserted, err := store.UpsertSiteCalendarHoliday(ctx, manageClaims, UpsertSiteCalendarHolidayInput{
		SiteID:           testSiteA1,
		HolidayDate:      holidayDate,
		DayType:          HolidayDayTypeHoliday,
		Label:            "劳动节",
		ActorDisplayName: "财务李工",
	})
	if err != nil || upserted.Revision != 1 || upserted.RecalcJobID == "" {
		t.Fatalf("UpsertSiteCalendarHoliday insert=%#v err=%v", upserted, err)
	}

	updatedHoliday, err := store.UpsertSiteCalendarHoliday(ctx, manageClaims, UpsertSiteCalendarHolidayInput{
		SiteID:           testSiteA1,
		HolidayDate:      holidayDate,
		DayType:          HolidayDayTypeSpecialDay,
		Label:            "劳动节调休",
		ActorDisplayName: "财务李工",
	})
	if err != nil || updatedHoliday.Revision != 2 || updatedHoliday.DayType != HolidayDayTypeSpecialDay || updatedHoliday.RecalcJobID == "" {
		t.Fatalf("UpsertSiteCalendarHoliday update=%#v err=%v", updatedHoliday, err)
	}

	yr := 2027
	listedHolidays, err := store.ListSiteCalendarHolidays(ctx, readClaims, testSiteA1, &yr)
	if err != nil || len(listedHolidays.Items) != 1 || listedHolidays.Items[0].HolidayDate != holidayDate {
		t.Fatalf("ListSiteCalendarHolidays=%#v err=%v", listedHolidays, err)
	}

	deletedHoliday, err := store.DeleteSiteCalendarHoliday(ctx, manageClaims, testSiteA1, holidayDate)
	if err != nil || !deletedHoliday.Deleted || deletedHoliday.RecalcJobID == "" {
		t.Fatalf("DeleteSiteCalendarHoliday=%#v err=%v", deletedHoliday, err)
	}
	if _, err := store.DeleteSiteCalendarHoliday(ctx, manageClaims, testSiteA1, holidayDate); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second DeleteSiteCalendarHoliday err=%v, want ErrNotFound", err)
	}

	// 2. Meter ratio versions CRUD + overlap -> ErrMeterRatioOverlap + retire keeps RELEASED.
	const primaryPointID = "01990000-1340-7000-8000-000000000001"
	ratio1, err := store.CreateSiteMeterRatioVersion(ctx, manageClaims, CreateSiteMeterRatioVersionInput{
		SiteID:               testSiteA1,
		PointID:              primaryPointID,
		CTRatio:              40,
		PTRatio:              1,
		RatioApplicationMode: RatioApplicationModePlatformApplied,
		EffectiveFrom:        "2026-08-01T00:00:00Z",
		EffectiveTo:          strPtr("2026-09-01T00:00:00Z"),
		ActorDisplayName:     "计量张工",
	})
	if err != nil || ratio1.Status != MeterRatioStatusReleased || ratio1.CloudMultiplier != 40 || ratio1.MeterID == nil || ratio1.MeterBindingID == nil || ratio1.RecalcJobID == "" {
		t.Fatalf("CreateSiteMeterRatioVersion r1=%#v err=%v", ratio1, err)
	}

	if _, err := store.CreateSiteMeterRatioVersion(ctx, manageClaims, CreateSiteMeterRatioVersionInput{
		SiteID:               testSiteA1,
		PointID:              primaryPointID,
		CTRatio:              60,
		PTRatio:              1,
		RatioApplicationMode: RatioApplicationModePlatformApplied,
		EffectiveFrom:        "2026-08-15T00:00:00Z",
		ActorDisplayName:     "计量张工",
	}); !errors.Is(err, ErrMeterRatioOverlap) {
		t.Fatalf("overlapping CreateSiteMeterRatioVersion err=%v, want ErrMeterRatioOverlap", err)
	}

	ratio2, err := store.CreateSiteMeterRatioVersion(ctx, manageClaims, CreateSiteMeterRatioVersionInput{
		SiteID:               testSiteA1,
		PointID:              primaryPointID,
		CTRatio:              80,
		PTRatio:              1,
		RatioApplicationMode: RatioApplicationModePlatformApplied,
		ReadingUnit:          MeterRatioReadingUnitMWh,
		EffectiveFrom:        "2026-09-01T00:00:00Z",
		ActorDisplayName:     "计量张工",
	})
	if err != nil || ratio2.Version == nil || ratio1.Version == nil || *ratio2.Version != *ratio1.Version+1 {
		t.Fatalf("CreateSiteMeterRatioVersion r2=%#v err=%v", ratio2, err)
	}

	retired, err := store.RetireSiteMeterRatioVersion(ctx, manageClaims, RetireSiteMeterRatioVersionInput{
		SiteID:           testSiteA1,
		VersionID:        ratio2.ID,
		EffectiveTo:      strPtr("2026-10-01T00:00:00Z"),
		ActorDisplayName: "计量张工",
	})
	if err != nil || retired.Status != MeterRatioStatusReleased || retired.Revision != 2 || retired.RecalcJobID == "" {
		t.Fatalf("RetireSiteMeterRatioVersion=%#v err=%v", retired, err)
	}

	if _, err := store.RetireSiteMeterRatioVersion(ctx, manageClaims, RetireSiteMeterRatioVersionInput{
		SiteID:           testSiteA1,
		VersionID:        ratio1.ID,
		EffectiveTo:      strPtr("2026-09-15T00:00:00Z"),
		ActorDisplayName: "计量张工",
	}); !errors.Is(err, ErrMeterRatioOverlap) {
		t.Fatalf("overlapping RetireSiteMeterRatioVersion err=%v, want ErrMeterRatioOverlap", err)
	}

	listedRatios, err := store.ListSiteMeterRatioVersions(ctx, readClaims, ListSiteMeterRatioVersionsInput{
		SiteID:            testSiteA1,
		PointID:           primaryPointID,
		IncludeSuperseded: false,
	})
	if err != nil || len(listedRatios.Items) < 2 {
		t.Fatalf("ListSiteMeterRatioVersions=%#v err=%v", listedRatios, err)
	}

	recalcJobs, err := store.ListAccountingRecalcJobs(ctx, readClaims, testSiteA1, 10)
	if err != nil || len(recalcJobs.Items) < 5 {
		t.Fatalf("ListAccountingRecalcJobs=%#v err=%v", recalcJobs, err)
	}
}
