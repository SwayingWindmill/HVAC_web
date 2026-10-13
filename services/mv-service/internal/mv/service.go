package mv

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
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
	ErrMeasureNotFound   = errors.New("savings measure not found")
	ErrInvalidMeasure    = errors.New("savings measure input is invalid")
	ErrWrongStatus       = errors.New("savings measure is not in a state that allows this")
	ErrReportingOverlap  = errors.New("another measure on the same measurement boundary reports over the same period")
	ErrBaselineLocked    = errors.New("the baseline is frozen: daily results have been published")
	ErrBoundaryUnmetered = errors.New("the device has no hourly energy Metric to verify on")
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
	// Evidence says in the operator's words what the measure rests on, such as a contract.
	Evidence string
	// EvidenceCommandID is the executed command the measure came from, when it came from one.
	EvidenceCommandID *string
	// BoundaryDeviceID is the device whose sub-meter bounds the measure (Option B); nil is the
	// plant's total meter (Option C).
	BoundaryDeviceID *string
	Status           Status
	Baseline         *Baseline
	CreatedBy        string
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

// HourlySource reads complete hours of a measurement boundary's electricity and the plant's
// cooling and electricity for a Site; a nil device is the plant's total meter.
type HourlySource interface {
	HourlySamples(ctx context.Context, tenantID, siteID string, boundaryDeviceID *string, from, to time.Time) ([]HourSample, error)
	// HasDeviceEnergy says whether the device has hourly energy Metric results on the Site.
	HasDeviceEnergy(ctx context.Context, tenantID, siteID, deviceID string) (bool, error)
}

// SiteClock is the Site's timezone, which days are verified on, and the import Tariff in effect,
// which prices hours on its own timezone as Settlement does; Tariff is nil when none is assigned and
// every hour then goes unpriced.
type SiteClock struct {
	Location *time.Location
	Tariff   *ImportTariff
}

// ImportTariff is the energy rate periods of the Tariff Version in effect, on the version's clock.
type ImportTariff struct {
	Location *time.Location
	Periods  []tariffmodel.Period
}

func (clock SiteClock) rates() func(time.Time) (float64, bool) {
	if clock.Tariff == nil {
		return func(time.Time) (float64, bool) { return 0, false }
	}
	return tariffmodel.Rates(clock.Tariff.Periods, clock.Tariff.Location)
}

type ClockSource interface {
	SiteClock(ctx context.Context, tenantID, siteID string) (SiteClock, error)
}

type Service struct {
	store   Store
	history HourlySource
	clocks  ClockSource
	now     func() time.Time
	newID   func(time.Time) (string, error)
}

func NewService(store Store, history HourlySource, clocks ClockSource, now func() time.Time, newID func(time.Time) (string, error)) (*Service, error) {
	if store == nil || history == nil || clocks == nil || now == nil || newID == nil {
		return nil, errors.New("savings verification needs a store, an hourly source, a Site clock source, a time source and an id source")
	}
	return &Service{store: store, history: history, clocks: clocks, now: now, newID: newID}, nil
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
	EvidenceCommandID *string
	BoundaryDeviceID  *string
	Actor             string
}

func (input MeasureInput) validate() error {
	name := strings.TrimSpace(input.Name)
	switch {
	case !uuidPattern.MatchString(input.TenantID) || !uuidPattern.MatchString(input.SiteID) || !uuidPattern.MatchString(input.Actor):
		return fmt.Errorf("%w: tenant, site and actor must be UUIDs", ErrInvalidMeasure)
	case name == "" || utf8.RuneCountInString(name) > 100:
		return fmt.Errorf("%w: name must be 1–100 characters", ErrInvalidMeasure)
	case utf8.RuneCountInString(input.Description) > 2000 || utf8.RuneCountInString(input.Evidence) > 200:
		return fmt.Errorf("%w: description or evidence is too long", ErrInvalidMeasure)
	case input.EvidenceCommandID != nil && !uuidPattern.MatchString(*input.EvidenceCommandID):
		return fmt.Errorf("%w: the cited command must be a UUID", ErrInvalidMeasure)
	case input.BoundaryDeviceID != nil && !uuidPattern.MatchString(*input.BoundaryDeviceID):
		return fmt.Errorf("%w: the boundary device must be a UUID", ErrInvalidMeasure)
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
	if input.BoundaryDeviceID != nil {
		metered, err := service.history.HasDeviceEnergy(ctx, input.TenantID, input.SiteID, *input.BoundaryDeviceID)
		if err != nil {
			return Measure{}, fmt.Errorf("read the boundary device's energy: %w", err)
		}
		if !metered {
			return Measure{}, ErrBoundaryUnmetered
		}
	}
	now := service.now().UTC()
	id, err := service.newID(now)
	if err != nil {
		return Measure{}, err
	}
	measure := Measure{
		ID: id, TenantID: input.TenantID, SiteID: input.SiteID, Name: strings.TrimSpace(input.Name), Kind: input.Kind,
		Description: strings.TrimSpace(input.Description), PlannedAt: input.PlannedAt.UTC(),
		InvestmentCNY: input.InvestmentCNY, ExpectedAnnualKWh: input.ExpectedAnnualKWh, Evidence: input.Evidence, EvidenceCommandID: input.EvidenceCommandID,
		BoundaryDeviceID: input.BoundaryDeviceID, Status: StatusPlanned, CreatedBy: input.Actor, UpdatedBy: input.Actor, CreatedAt: now, UpdatedAt: now,
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
		if other.ID != measure.ID && other.ImplementedAt != nil && (other.EndedAt == nil || other.EndedAt.After(implementedAt)) && sharesBoundary(other, measure) {
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
	// A baseline that passed stays; refitting is for finding a period that can pass at all.
	if measure.Baseline.Trusted {
		return Measure{}, ErrWrongStatus
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

// sharesBoundary says two measures would count the same energy: they are on the same device, or
// one is on the plant's total meter, which holds every sub-meter.
func sharesBoundary(one, other Measure) bool {
	return one.BoundaryDeviceID == nil || other.BoundaryDeviceID == nil || *one.BoundaryDeviceID == *other.BoundaryDeviceID
}

func (service *Service) fit(ctx context.Context, measure Measure, from, to time.Time) (Baseline, error) {
	samples, err := service.history.HourlySamples(ctx, measure.TenantID, measure.SiteID, measure.BoundaryDeviceID, from, to)
	if err != nil {
		return Baseline{}, fmt.Errorf("read baseline hours: %w", err)
	}
	return FitBaseline(samples, from, to), nil
}

// SettlingDelay is how long after a Site-local day ends its hourly Metrics are taken as released.
const SettlingDelay = time.Hour

// VerifyDay verifies the Site-local day starting at dayStart for a measure with a trusted baseline.
func (service *Service) VerifyDay(ctx context.Context, measure Measure, dayStart time.Time) error {
	if !measure.verifiable() {
		return ErrWrongStatus
	}
	clock, err := service.clocks.SiteClock(ctx, measure.TenantID, measure.SiteID)
	if err != nil {
		return fmt.Errorf("read Site clock: %w", err)
	}
	return service.verifyDay(ctx, measure, clock, dayStart.In(clock.Location))
}

func (service *Service) verifyDay(ctx context.Context, measure Measure, clock SiteClock, local time.Time) error {
	dayEnd := local.AddDate(0, 0, 1)
	if measure.EndedAt != nil && measure.EndedAt.Before(dayEnd) {
		// The hour a measure ends in is not wholly its own, so verification stops at its start.
		ended := measure.EndedAt.In(clock.Location)
		dayEnd = time.Date(ended.Year(), ended.Month(), ended.Day(), ended.Hour(), 0, 0, 0, clock.Location)
	}
	samples, err := service.history.HourlySamples(ctx, measure.TenantID, measure.SiteID, measure.BoundaryDeviceID, local, dayEnd)
	if err != nil {
		return fmt.Errorf("read reporting hours: %w", err)
	}
	result := VerifyDay(*measure.Baseline, samples, local, dayEnd, measure.ImplementedAt.Add(StabilizationPeriod), clock.rates())
	return service.store.InsertDailyResult(ctx, DailyResult{
		MeasureID: measure.ID, TenantID: measure.TenantID, SiteID: measure.SiteID,
		Day: local.Format(time.DateOnly), DayResult: result, VerifiedAt: service.now().UTC(),
	})
}

// VerifyDueDays verifies every Site-local day with reporting hours that settled by until and has no
// result yet, for each measure on the Site with a trusted baseline. A missed run is caught up here.
func (service *Service) VerifyDueDays(ctx context.Context, tenantID, siteID string, until time.Time) error {
	measures, err := service.store.Measures(ctx, tenantID, siteID)
	if err != nil {
		return err
	}
	var clock *SiteClock
	for _, measure := range measures {
		if !measure.verifiable() {
			continue
		}
		if clock == nil {
			read, err := service.clocks.SiteClock(ctx, tenantID, siteID)
			if err != nil {
				return fmt.Errorf("read Site clock: %w", err)
			}
			clock = &read
		}
		results, err := service.store.DailyResults(ctx, tenantID, siteID, measure.ID)
		if err != nil {
			return err
		}
		verified := make(map[string]bool, len(results))
		for _, result := range results {
			verified[result.Day] = true
		}
		reporting := measure.ImplementedAt.Add(StabilizationPeriod).In(clock.Location)
		day := time.Date(reporting.Year(), reporting.Month(), reporting.Day(), 0, 0, 0, 0, clock.Location)
		for ; !day.AddDate(0, 0, 1).After(until.Add(-SettlingDelay)); day = day.AddDate(0, 0, 1) {
			if measure.EndedAt != nil && !day.Before(*measure.EndedAt) {
				break
			}
			if verified[day.Format(time.DateOnly)] {
				continue
			}
			if err := service.verifyDay(ctx, measure, *clock, day); err != nil {
				return fmt.Errorf("verify %s for measure %s: %w", day.Format(time.DateOnly), measure.ID, err)
			}
		}
	}
	return nil
}

// verifiable says the measure was implemented on a baseline that passed, so its days can be verified.
func (measure Measure) verifiable() bool {
	return measure.ImplementedAt != nil && measure.Baseline != nil && measure.Baseline.Trusted
}

// Totals sums verified days.
type Totals struct {
	// Days counts each verified day once, however many measures verified it.
	Days int
	// UnpricedHours were verified without an energy rate, so their avoided cost is not counted.
	UnpricedHours       int
	AdjustedBaselineKWh float64
	ActualKWh           float64
	AvoidedKWh          float64
	AvoidedCost         float64
	// PlantKWh is the plant's electricity over the verified hours, each day counted once.
	PlantKWh float64
}

func totalOf(days []DailyResult) Totals {
	var totals Totals
	for _, day := range days {
		totals.AdjustedBaselineKWh += day.AdjustedBaselineKWh
		totals.ActualKWh += day.ActualKWh
		totals.AvoidedKWh += day.AvoidedKWh
		totals.AvoidedCost += day.AvoidedCost
		totals.UnpricedHours += day.UnpricedHours
	}
	// Measures on different devices verify the same day; the plant used its electricity once.
	plant := map[string]float64{}
	for _, day := range days {
		plant[day.Day] = max(plant[day.Day], day.PlantKWh)
	}
	totals.Days = len(plant)
	for _, kwh := range plant {
		totals.PlantKWh += kwh
	}
	return totals
}

// VerifiedMeasure is a measure with every day verified against its baseline.
type VerifiedMeasure struct {
	Measure
	Days []DailyResult
}

func (verified VerifiedMeasure) Totals() Totals { return totalOf(verified.Days) }

func (service *Service) withDays(ctx context.Context, measure Measure) (VerifiedMeasure, error) {
	days, err := service.store.DailyResults(ctx, measure.TenantID, measure.SiteID, measure.ID)
	return VerifiedMeasure{Measure: measure, Days: days}, err
}

// Ledger is the Site's measures with their verified days.
func (service *Service) Ledger(ctx context.Context, tenantID, siteID string) ([]VerifiedMeasure, error) {
	measures, err := service.store.Measures(ctx, tenantID, siteID)
	if err != nil {
		return nil, err
	}
	ledger := make([]VerifiedMeasure, 0, len(measures))
	for _, measure := range measures {
		verified, err := service.withDays(ctx, measure)
		if err != nil {
			return nil, err
		}
		ledger = append(ledger, verified)
	}
	return ledger, nil
}

func (service *Service) Verified(ctx context.Context, tenantID, siteID, id string) (VerifiedMeasure, error) {
	measure, err := service.store.Measure(ctx, tenantID, siteID, id)
	if err != nil {
		return VerifiedMeasure{}, err
	}
	return service.withDays(ctx, measure)
}

// Savings totals the Site's days verified between two Site-local dates, inclusive.
func (service *Service) Savings(ctx context.Context, tenantID, siteID, from, to string) (Totals, error) {
	_, fromErr := time.Parse(time.DateOnly, from)
	_, toErr := time.Parse(time.DateOnly, to)
	if fromErr != nil || toErr != nil || to < from {
		return Totals{}, fmt.Errorf("%w: the period must be two dates in order", ErrInvalidMeasure)
	}
	ledger, err := service.Ledger(ctx, tenantID, siteID)
	if err != nil {
		return Totals{}, err
	}
	var days []DailyResult
	for _, verified := range ledger {
		for _, day := range verified.Days {
			if day.Day >= from && day.Day <= to {
				days = append(days, day)
			}
		}
	}
	return totalOf(days), nil
}
