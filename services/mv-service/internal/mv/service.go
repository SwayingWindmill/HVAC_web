package mv

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultBaselineDays is how far before implementation the baseline period reaches by default.
const DefaultBaselineDays = 14

type Status string

const (
	StatusPlanned     Status = "PLANNED"
	StatusImplemented Status = "IMPLEMENTED"
	StatusEnded       Status = "ENDED"
)

type Kind string

const (
	KindSetpointReset     Kind = "SETPOINT_RESET"
	KindControlSequence   Kind = "CONTROL_SEQUENCE"
	KindEquipmentRetrofit Kind = "EQUIPMENT_RETROFIT"
	KindOperatingSchedule Kind = "OPERATING_SCHEDULE"
	KindOther             Kind = "OTHER"
)

var (
	ErrMeasureNotFound  = errors.New("savings measure not found")
	ErrInvalidMeasure   = errors.New("savings measure input is invalid")
	ErrWrongStatus      = errors.New("savings measure is not in a state that allows this")
	ErrReportingOverlap = errors.New("another measure on this Site reports over the same period")
	ErrBaselineLocked   = errors.New("the baseline is frozen: daily results have been published")
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Measure is one energy conservation measure in a Site's ledger.
type Measure struct {
	ID                string
	TenantID          string
	SiteID            string
	Name              string
	Kind              Kind
	Description       string
	PlannedAt         time.Time
	ImplementedAt     *time.Time
	EndedAt           *time.Time
	InvestmentCNY     *float64
	ExpectedAnnualKWh *float64
	// Evidence names what the measure acted on, such as an optimization recommendation or a command.
	Evidence  string
	Status    Status
	Baseline  *Baseline
	CreatedBy string
	// UpdatedBy is who last implemented, refitted or ended the measure.
	UpdatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DailyResult is a measure's verified Site-local day; once written it is never changed.
type DailyResult struct {
	MeasureID string
	TenantID  string
	SiteID    string
	Day       string
	DayResult
	VerifiedAt time.Time
}

type Store interface {
	InsertMeasure(context.Context, Measure) error
	UpdateMeasure(context.Context, Measure) error
	Measure(ctx context.Context, tenantID, siteID, id string) (Measure, error)
	Measures(ctx context.Context, tenantID, siteID string) ([]Measure, error)
	// InsertDailyResult keeps the first result written for a measure and day.
	InsertDailyResult(context.Context, DailyResult) error
	DailyResults(ctx context.Context, tenantID, siteID, measureID string) ([]DailyResult, error)
}

// HourlySource reads complete hours of plant electricity and cooling for a Site.
type HourlySource interface {
	HourlySamples(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]HourSample, error)
}

// SiteTariff is the Site's import Tariff in effect, with the Site clock it prices on.
type SiteTariff struct {
	Location *time.Location
	Periods  []TariffPeriod
}

type TariffSource interface {
	ImportTariff(ctx context.Context, tenantID, siteID string) (SiteTariff, error)
}

type Service struct {
	store   Store
	history HourlySource
	tariffs TariffSource
	now     func() time.Time
	newID   func(time.Time) (string, error)
}

func NewService(store Store, history HourlySource, tariffs TariffSource, now func() time.Time, newID func(time.Time) (string, error)) (*Service, error) {
	if store == nil || history == nil || tariffs == nil || now == nil || newID == nil {
		return nil, errors.New("savings verification needs a store, an hourly source, a tariff source, a clock and an id source")
	}
	return &Service{store: store, history: history, tariffs: tariffs, now: now, newID: newID}, nil
}

type MeasureInput struct {
	TenantID          string
	SiteID            string
	Name              string
	Kind              Kind
	Description       string
	PlannedAt         time.Time
	InvestmentCNY     *float64
	ExpectedAnnualKWh *float64
	Evidence          string
	Actor             string
}

func (input MeasureInput) validate() error {
	name := strings.TrimSpace(input.Name)
	switch {
	case !uuidPattern.MatchString(input.TenantID) || !uuidPattern.MatchString(input.SiteID) || !uuidPattern.MatchString(input.Actor):
		return fmt.Errorf("%w: tenant, site and actor must be UUIDs", ErrInvalidMeasure)
	case name == "" || utf8.RuneCountInString(name) > 100:
		return fmt.Errorf("%w: name must be 1–100 characters", ErrInvalidMeasure)
	case utf8.RuneCountInString(input.Description) > 2000 || len(input.Evidence) > 200:
		return fmt.Errorf("%w: description or evidence is too long", ErrInvalidMeasure)
	case input.PlannedAt.IsZero():
		return fmt.Errorf("%w: planned time is required", ErrInvalidMeasure)
	case (input.InvestmentCNY != nil && *input.InvestmentCNY < 0) || (input.ExpectedAnnualKWh != nil && *input.ExpectedAnnualKWh < 0):
		return fmt.Errorf("%w: investment and expected savings cannot be negative", ErrInvalidMeasure)
	}
	switch input.Kind {
	case KindSetpointReset, KindControlSequence, KindEquipmentRetrofit, KindOperatingSchedule, KindOther:
		return nil
	default:
		return fmt.Errorf("%w: kind %q is unknown", ErrInvalidMeasure, input.Kind)
	}
}

func (service *Service) Register(ctx context.Context, input MeasureInput) (Measure, error) {
	if err := input.validate(); err != nil {
		return Measure{}, err
	}
	now := service.now().UTC()
	id, err := service.newID(now)
	if err != nil {
		return Measure{}, err
	}
	measure := Measure{
		ID: id, TenantID: input.TenantID, SiteID: input.SiteID, Name: strings.TrimSpace(input.Name), Kind: input.Kind,
		Description: strings.TrimSpace(input.Description), PlannedAt: input.PlannedAt.UTC(),
		InvestmentCNY: input.InvestmentCNY, ExpectedAnnualKWh: input.ExpectedAnnualKWh, Evidence: input.Evidence,
		Status: StatusPlanned, CreatedBy: input.Actor, UpdatedBy: input.Actor, CreatedAt: now, UpdatedAt: now,
	}
	return measure, service.store.InsertMeasure(ctx, measure)
}

// Implement records when the measure took effect and freezes its baseline from the period before.
func (service *Service) Implement(ctx context.Context, tenantID, siteID, id string, implementedAt time.Time, actor string) (Measure, error) {
	measure, err := service.store.Measure(ctx, tenantID, siteID, id)
	if err != nil {
		return Measure{}, err
	}
	if measure.Status != StatusPlanned {
		return Measure{}, ErrWrongStatus
	}
	implementedAt = implementedAt.UTC()
	if implementedAt.IsZero() || implementedAt.After(service.now()) {
		return Measure{}, fmt.Errorf("%w: implementation time must not be in the future", ErrInvalidMeasure)
	}
	others, err := service.store.Measures(ctx, tenantID, siteID)
	if err != nil {
		return Measure{}, err
	}
	for _, other := range others {
		if other.ID != measure.ID && other.ImplementedAt != nil && (other.EndedAt == nil || other.EndedAt.After(implementedAt)) {
			return Measure{}, ErrReportingOverlap
		}
	}
	baseline, err := service.fit(ctx, measure, implementedAt.Add(-DefaultBaselineDays*24*time.Hour), implementedAt)
	if err != nil {
		return Measure{}, err
	}
	measure.ImplementedAt = &implementedAt
	measure.Status = StatusImplemented
	measure.Baseline = &baseline
	measure.UpdatedAt = service.now().UTC()
	measure.UpdatedBy = actor
	return measure, service.store.UpdateMeasure(ctx, measure)
}

// Refit fits the baseline again on another period before implementation, until results are published.
func (service *Service) Refit(ctx context.Context, tenantID, siteID, id string, from, to time.Time, actor string) (Measure, error) {
	measure, err := service.store.Measure(ctx, tenantID, siteID, id)
	if err != nil {
		return Measure{}, err
	}
	if measure.Status != StatusImplemented {
		return Measure{}, ErrWrongStatus
	}
	results, err := service.store.DailyResults(ctx, tenantID, siteID, id)
	if err != nil {
		return Measure{}, err
	}
	if len(results) > 0 {
		return Measure{}, ErrBaselineLocked
	}
	from, to = from.UTC(), to.UTC()
	if !from.Before(to) || to.After(*measure.ImplementedAt) {
		return Measure{}, fmt.Errorf("%w: the baseline period must end by implementation", ErrInvalidMeasure)
	}
	baseline, err := service.fit(ctx, measure, from, to)
	if err != nil {
		return Measure{}, err
	}
	measure.Baseline = &baseline
	measure.UpdatedAt = service.now().UTC()
	measure.UpdatedBy = actor
	return measure, service.store.UpdateMeasure(ctx, measure)
}

func (service *Service) End(ctx context.Context, tenantID, siteID, id string, endedAt time.Time, actor string) (Measure, error) {
	measure, err := service.store.Measure(ctx, tenantID, siteID, id)
	if err != nil {
		return Measure{}, err
	}
	if measure.Status != StatusImplemented {
		return Measure{}, ErrWrongStatus
	}
	endedAt = endedAt.UTC()
	if !endedAt.After(*measure.ImplementedAt) {
		return Measure{}, fmt.Errorf("%w: a measure ends after it was implemented", ErrInvalidMeasure)
	}
	measure.EndedAt = &endedAt
	measure.Status = StatusEnded
	measure.UpdatedAt = service.now().UTC()
	measure.UpdatedBy = actor
	return measure, service.store.UpdateMeasure(ctx, measure)
}

func (service *Service) fit(ctx context.Context, measure Measure, from, to time.Time) (Baseline, error) {
	samples, err := service.history.HourlySamples(ctx, measure.TenantID, measure.SiteID, from, to)
	if err != nil {
		return Baseline{}, fmt.Errorf("read baseline hours: %w", err)
	}
	return FitBaseline(samples, from, to), nil
}

// VerifyDay verifies the Site-local day starting at dayStart for a measure with a trusted baseline.
func (service *Service) VerifyDay(ctx context.Context, measure Measure, dayStart time.Time) error {
	if measure.ImplementedAt == nil || measure.Baseline == nil || !measure.Baseline.Trusted {
		return ErrWrongStatus
	}
	tariff, err := service.tariffs.ImportTariff(ctx, measure.TenantID, measure.SiteID)
	if err != nil {
		return fmt.Errorf("read import tariff: %w", err)
	}
	local := dayStart.In(tariff.Location)
	dayEnd := local.AddDate(0, 0, 1)
	if measure.EndedAt != nil && measure.EndedAt.Before(dayEnd) {
		dayEnd = *measure.EndedAt
	}
	samples, err := service.history.HourlySamples(ctx, measure.TenantID, measure.SiteID, local, dayEnd)
	if err != nil {
		return fmt.Errorf("read reporting hours: %w", err)
	}
	result := VerifyDay(*measure.Baseline, samples, local, dayEnd, measure.ImplementedAt.Add(StabilizationPeriod), TariffRates(tariff.Periods, tariff.Location))
	return service.store.InsertDailyResult(ctx, DailyResult{
		MeasureID: measure.ID, TenantID: measure.TenantID, SiteID: measure.SiteID,
		Day: local.Format(time.DateOnly), DayResult: result, VerifiedAt: service.now().UTC(),
	})
}
