package report

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

type fakeStore struct {
	location *time.Location
	savings  []SavingsDay
	findings []FindingCount
	stored   []StoredSnapshot
	asked    struct{ savingsFrom, savingsTo string }
}

func (store *fakeStore) SiteLocation(context.Context, string, string) (*time.Location, error) {
	return store.location, nil
}

func (store *fakeStore) SavingsDays(_ context.Context, _, _ string, from, to string) ([]SavingsDay, error) {
	store.asked.savingsFrom, store.asked.savingsTo = from, to
	return store.savings, nil
}

func (store *fakeStore) FindingCounts(context.Context, string, string, time.Time, time.Time) ([]FindingCount, error) {
	return store.findings, nil
}

func (store *fakeStore) InsertSnapshot(_ context.Context, _, _ string, stored StoredSnapshot) (StoredSnapshot, error) {
	for _, existing := range store.stored {
		if existing.Month == stored.Month {
			stored.Version = max(stored.Version, existing.Version)
		}
	}
	stored.Version++
	store.stored = append(store.stored, stored)
	return stored, nil
}

func (store *fakeStore) LatestSnapshot(_ context.Context, _, _, month string) (StoredSnapshot, error) {
	for index := len(store.stored) - 1; index >= 0; index-- {
		if store.stored[index].Month == month {
			return store.stored[index], nil
		}
	}
	return StoredSnapshot{}, ErrSnapshotNotFound
}

func (store *fakeStore) YearSnapshots(context.Context, string, string, int) ([]StoredSnapshot, error) {
	return store.stored, nil
}

type fakeHours struct {
	hours    []Hour
	from, to time.Time
}

func (source *fakeHours) Hours(_ context.Context, _, _ string, _ *time.Location, from, to time.Time) ([]Hour, error) {
	source.from, source.to = from, to
	return source.hours, nil
}

const (
	testTenant = "018f3d00-0000-7000-8000-000000000001"
	testSite   = "018f3e00-1000-7000-8000-000000000001"
)

func testService(t *testing.T, now time.Time) (*Service, *fakeStore, *fakeHours) {
	t.Helper()
	store := &fakeStore{location: shanghai}
	hours := &fakeHours{hours: day(time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai), 10, 40, always)}
	service, err := NewService(store, hours, func() time.Time { return now }, schedulerjob.UUIDv7)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, hours
}

func TestAClosedMonthIsSnapshotOverItsSiteCalendarAsTheNextVersion(t *testing.T) {
	service, store, hours := testService(t, time.Date(2026, 10, 1, 3, 0, 0, 0, shanghai))
	first, err := service.Generate(context.Background(), testTenant, testSite, "2026-09", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hours.from.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, shanghai)) || !hours.to.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai)) {
		t.Fatalf("hours read %s – %s", hours.from, hours.to)
	}
	if store.asked.savingsFrom != "2026-09-01" || store.asked.savingsTo != "2026-10-01" {
		t.Fatalf("savings read %#v", store.asked)
	}
	if first.Version != 1 || first.Report.DaysWithData != 1 || first.Report.ElectricityKWh != 240 {
		t.Fatalf("first = %#v", first)
	}
	again, err := service.Generate(context.Background(), testTenant, testSite, "2026-09", nil)
	if err != nil || again.Version != 2 || again.ID == first.ID {
		t.Fatalf("again = %#v err=%v", again, err)
	}
}

func TestAMonthStillRunningIsNotSnapshot(t *testing.T) {
	service, store, _ := testService(t, time.Date(2026, 9, 30, 23, 0, 0, 0, shanghai))
	if _, err := service.Generate(context.Background(), testTenant, testSite, "2026-09", nil); !errors.Is(err, ErrMonthNotOver) {
		t.Fatalf("snapshot a month that is not over: %v", err)
	}
	if _, err := service.Generate(context.Background(), testTenant, testSite, "2026-9", nil); !errors.Is(err, ErrInvalidMonth) {
		t.Fatalf("a malformed month: %v", err)
	}
	if len(store.stored) != 0 {
		t.Fatal("stored a running month")
	}
}

func TestAScheduledGenerationTakesTheMonthBeforeOnTheSiteCalendar(t *testing.T) {
	service, _, _ := testService(t, time.Now())
	// 2026-09-30 18:00 UTC is already October 1 in Shanghai.
	month, err := jobMonth(context.Background(), service, schedulerjob.Job{TenantID: testTenant, SiteID: testSite, ScheduledFor: time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC), Payload: json.RawMessage(`{}`)})
	if err != nil || month != "2026-09" {
		t.Fatalf("month = %q err=%v", month, err)
	}
	named, err := jobMonth(context.Background(), service, schedulerjob.Job{TenantID: testTenant, SiteID: testSite, Payload: json.RawMessage(`{"month":"2026-07"}`)})
	if err != nil || named != "2026-07" {
		t.Fatalf("named = %q err=%v", named, err)
	}
}

func TestTheInternalAPIServesTheLatestVersionAndTheYear(t *testing.T) {
	service, _, _ := testService(t, time.Date(2026, 10, 1, 3, 0, 0, 0, shanghai))
	for range 2 {
		if _, err := service.Generate(context.Background(), testTenant, testSite, "2026-09", nil); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := NewHTTPHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set(headerTenant, testTenant)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	month := get("/v1/sites/" + testSite + "/reports/monthly/2026-09")
	var stored StoredSnapshot
	if month.Code != http.StatusOK || json.Unmarshal(month.Body.Bytes(), &stored) != nil || stored.Version != 2 || stored.Report.Month != "2026-09" {
		t.Fatalf("month = %d %s", month.Code, month.Body.String())
	}
	if strings.Contains(month.Body.String(), "schedulerJobId") {
		t.Fatal("the scheduler job id is internal")
	}
	if missing := get("/v1/sites/" + testSite + "/reports/monthly/2026-08"); missing.Code != http.StatusNotFound {
		t.Fatalf("missing = %d", missing.Code)
	}
	if year := get("/v1/sites/" + testSite + "/reports/monthly?year=2026"); year.Code != http.StatusOK || !strings.Contains(year.Body.String(), `"items"`) {
		t.Fatalf("year = %d %s", year.Code, year.Body.String())
	}
	if invalid := get("/v1/sites/" + testSite + "/reports/monthly/2026-13"); invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid = %d", invalid.Code)
	}
}
