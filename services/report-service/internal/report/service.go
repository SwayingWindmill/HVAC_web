package report

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrSiteNotFound     = errors.New("site not found")
	ErrSnapshotNotFound = errors.New("report snapshot not found")
	// ErrInvalidMonth and ErrMonthNotOver are a job naming a month it cannot take: retrying won't help.
	ErrInvalidMonth = errors.New("report month is invalid")
	ErrMonthNotOver = errors.New("a month's report is taken once the month is over")
)

// StoredSnapshot is one version of a month's snapshot as kept.
type StoredSnapshot struct {
	ID             string    `json:"id"`
	Month          string    `json:"month"`
	Version        int       `json:"version"`
	GeneratedAt    time.Time `json:"generatedAt"`
	SchedulerJobID *string   `json:"-"`
	Report         Snapshot  `json:"report"`
}

// Store is what a snapshot is read from and kept in.
type Store interface {
	SiteLocation(ctx context.Context, tenantID, siteID string) (*time.Location, error)
	Tariffs(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]Tariff, error)
	SavingsDays(ctx context.Context, tenantID, siteID string, from, to string) ([]SavingsDay, error)
	FindingCounts(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]FindingCount, error)
	InsertSnapshot(ctx context.Context, tenantID, siteID string, stored StoredSnapshot) (StoredSnapshot, error)
	LatestSnapshot(ctx context.Context, tenantID, siteID, month string) (StoredSnapshot, error)
	YearSnapshots(ctx context.Context, tenantID, siteID string, year int) ([]StoredSnapshot, error)
}

// HourSource is a Site's valid energy by hour.
type HourSource interface {
	Hours(ctx context.Context, tenantID, siteID string, location *time.Location, from, to time.Time) ([]Hour, error)
}

type Service struct {
	store Store
	hours HourSource
	now   func() time.Time
	newID func(time.Time) (string, error)
}

func NewService(store Store, hours HourSource, now func() time.Time, newID func(time.Time) (string, error)) (*Service, error) {
	if store == nil || hours == nil || now == nil || newID == nil {
		return nil, errors.New("reports need a store, an hour source, a time source and an id source")
	}
	return &Service{store: store, hours: hours, now: now, newID: newID}, nil
}

// MonthBefore is the Site-local month before the one an instant falls in, as YYYY-MM.
func (service *Service) MonthBefore(ctx context.Context, tenantID, siteID string, at time.Time) (string, error) {
	location, err := service.store.SiteLocation(ctx, tenantID, siteID)
	if err != nil {
		return "", err
	}
	local := at.In(location)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location).AddDate(0, -1, 0).Format("2006-01"), nil
}

// Generate takes a snapshot of a month that is over and keeps it as the month's next version.
func (service *Service) Generate(ctx context.Context, tenantID, siteID, month string, schedulerJobID *string) (StoredSnapshot, error) {
	location, err := service.store.SiteLocation(ctx, tenantID, siteID)
	if err != nil {
		return StoredSnapshot{}, err
	}
	from, to, _, err := MonthBounds(month, location)
	if err != nil {
		return StoredSnapshot{}, err
	}
	now := service.now().UTC()
	if to.After(now) {
		return StoredSnapshot{}, fmt.Errorf("%w: %s", ErrMonthNotOver, month)
	}
	hours, err := service.hours.Hours(ctx, tenantID, siteID, location, from, to)
	if err != nil {
		return StoredSnapshot{}, err
	}
	tariffs, err := service.store.Tariffs(ctx, tenantID, siteID, from, to)
	if err != nil {
		return StoredSnapshot{}, err
	}
	savings, err := service.store.SavingsDays(ctx, tenantID, siteID, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return StoredSnapshot{}, err
	}
	findings, err := service.store.FindingCounts(ctx, tenantID, siteID, from, to)
	if err != nil {
		return StoredSnapshot{}, err
	}
	snapshot, err := Build(month, location, hours, tariffs, savings, findings)
	if err != nil {
		return StoredSnapshot{}, err
	}
	id, err := service.newID(now)
	if err != nil {
		return StoredSnapshot{}, err
	}
	return service.store.InsertSnapshot(ctx, tenantID, siteID, StoredSnapshot{
		ID: id, Month: month, GeneratedAt: now, SchedulerJobID: schedulerJobID, Report: snapshot,
	})
}

func (service *Service) Latest(ctx context.Context, tenantID, siteID, month string) (StoredSnapshot, error) {
	return service.store.LatestSnapshot(ctx, tenantID, siteID, month)
}

func (service *Service) Year(ctx context.Context, tenantID, siteID string, year int) ([]StoredSnapshot, error) {
	return service.store.YearSnapshots(ctx, tenantID, siteID, year)
}
