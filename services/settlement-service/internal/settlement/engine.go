package settlement

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Period struct {
	TenantID, SiteID, ID, BoundaryID, Timezone, Status string
	Start, End                                         time.Time
	MeterBindingRefs                                   []string
}

type MetricBinding struct {
	ID, MetricBindingID, MetricVersionID, MetricCode, Role, TariffPeriodCode string
}

type TariffPeriod struct {
	ID, Code, DayType      string
	StartMinute, EndMinute int
	EnergyRate, DemandRate float64
}

type Tariff struct {
	ID, VersionID, Currency, Timezone string
	Version                           int64
	Periods                           []TariffPeriod
}

type Fact struct {
	ID, MetricBindingID, MetricVersionID, MetricCode, Role, TariffPeriodCode string
	Start, End, CalculatedAt                                                 time.Time
	Value                                                                    float64
	Revision                                                                 uint64
	Quality                                                                  string
	Completeness                                                             float64
}

type Calculation struct {
	EnergyBreakdown          map[string]float64
	DemandKW                 map[string]float64
	EnergyCost               map[string]float64
	DemandCost               map[string]float64
	TotalCost                float64
	Currency, Quality        string
	Completeness             float64
	SourceRefs               []string
	MeterRefs                []string
	MetricBindingRefs        []string
	MetricVersionRefs        []string
	MissingMetricBindingRefs []string
	SourceMetricRevisions    map[string]uint64
	SourceWatermark          time.Time
	TariffVersionID          string
}

type Snapshot struct {
	ID                                       string
	RevisionNo                               int
	DatasetRevision                          uint64
	PreviousSnapshotID, SettlementRevisionID string
	Period                                   Period
	Calculation                              Calculation
	CreatedAt                                time.Time
}

type Candidate struct {
	ID, TenantID, SiteID, PeriodID, BaseSnapshotID, Reason, CalculationDigest string
	Calculation                                                               Calculation
}

type Repository interface {
	LoadPeriod(context.Context, string, string, string) (Period, []MetricBinding, Tariff, error)
	TransitionPeriod(context.Context, Period, string, time.Time) error
	InsertSnapshot(context.Context, Snapshot) error
	LatestSnapshot(context.Context, string, string, string) (Snapshot, error)
	CreateCandidate(context.Context, Candidate, time.Time) (string, error)
	ApproveCandidate(context.Context, string, string, string, time.Time) error
	LoadApprovedCandidate(context.Context, string, string, string) (Candidate, Snapshot, error)
	ApplyRevision(context.Context, Candidate, Snapshot, Snapshot, time.Time) error
}

type FactStore interface {
	ReadMetricFacts(context.Context, Period, []MetricBinding) ([]Fact, error)
}

type Engine struct {
	repo  Repository
	facts FactStore
	now   func() time.Time
}

func New(repo Repository, facts FactStore) (*Engine, error) {
	if repo == nil || facts == nil {
		return nil, errors.New("settlement repository and Metric fact store are required")
	}
	return &Engine{repo: repo, facts: facts, now: time.Now}, nil
}

func (e *Engine) CalculatePeriod(ctx context.Context, tenantID, siteID, periodID string) (Snapshot, error) {
	period, bindings, tariff, err := e.repo.LoadPeriod(ctx, tenantID, siteID, periodID)
	if err != nil {
		return Snapshot{}, err
	}
	if period.Status != "OPEN" && period.Status != "REVIEW" && period.Status != "CALCULATING" {
		return Snapshot{}, fmt.Errorf("settlement period cannot calculate from %s", period.Status)
	}
	now := e.now().UTC()
	originalStatus := period.Status
	if period.Status != "CALCULATING" {
		if err = e.repo.TransitionPeriod(ctx, period, "CALCULATING", now); err != nil {
			return Snapshot{}, err
		}
		period.Status = "CALCULATING"
	}
	facts, err := e.facts.ReadMetricFacts(ctx, period, bindings)
	if err != nil {
		return Snapshot{}, err
	}
	calc, err := calculate(period, bindings, tariff, facts)
	if err != nil {
		return Snapshot{}, err
	}
	if originalStatus == "REVIEW" {
		current, currentErr := e.repo.LatestSnapshot(ctx, tenantID, siteID, periodID)
		if currentErr == nil && digest(current.Calculation) == digest(calc) {
			if err = e.repo.TransitionPeriod(ctx, period, "REVIEW", e.now().UTC()); err != nil {
				return Snapshot{}, err
			}
			return current, nil
		}
		if currentErr == nil {
			if err = e.repo.TransitionPeriod(ctx, period, "REVIEW", e.now().UTC()); err != nil {
				return Snapshot{}, err
			}
			return Snapshot{}, errors.New("settlement review inputs changed; reconcile the current snapshot")
		}
		if err = e.repo.TransitionPeriod(ctx, period, "REVIEW", e.now().UTC()); err != nil {
			return Snapshot{}, err
		}
		return Snapshot{}, fmt.Errorf("load current settlement snapshot: %w", currentErr)
	}
	id, err := uuidv7(now)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{ID: id, RevisionNo: 0, DatasetRevision: 1, Period: period, Calculation: calc, CreatedAt: now}
	if err = e.repo.InsertSnapshot(ctx, snapshot); err != nil {
		return Snapshot{}, err
	}
	if err = e.repo.TransitionPeriod(ctx, period, "REVIEW", e.now().UTC()); err != nil {
		return Snapshot{}, err
	}
	snapshot.Period.Status = "REVIEW"
	return snapshot, nil
}

func (e *Engine) ReconcilePeriod(ctx context.Context, tenantID, siteID, periodID, reason string) (string, error) {
	period, bindings, tariff, err := e.repo.LoadPeriod(ctx, tenantID, siteID, periodID)
	if err != nil {
		return "", err
	}
	base, err := e.repo.LatestSnapshot(ctx, tenantID, siteID, periodID)
	if err != nil {
		return "", err
	}
	facts, err := e.facts.ReadMetricFacts(ctx, period, bindings)
	if err != nil {
		return "", err
	}
	calc, err := calculate(period, bindings, tariff, facts)
	if err != nil {
		return "", err
	}
	if digest(base.Calculation) == digest(calc) {
		return "", nil
	}
	if reason == "" {
		reason = "METRIC_REVISION"
	}
	id, err := uuidv7(e.now().UTC())
	if err != nil {
		return "", err
	}
	candidate := Candidate{ID: id, TenantID: tenantID, SiteID: siteID, PeriodID: periodID, BaseSnapshotID: base.ID, Reason: reason, CalculationDigest: digest(calc), Calculation: calc}
	candidateID, err := e.repo.CreateCandidate(ctx, candidate, e.now().UTC())
	if err != nil {
		return "", err
	}
	return candidateID, nil
}

func (e *Engine) ApproveCandidate(ctx context.Context, tenantID, siteID, candidateID string) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(siteID) == "" || strings.TrimSpace(candidateID) == "" {
		return errors.New("settlement candidate approval scope is invalid")
	}
	return e.repo.ApproveCandidate(ctx, tenantID, siteID, candidateID, e.now().UTC())
}

func (e *Engine) ApplyApprovedRevision(ctx context.Context, tenantID, siteID, candidateID string) (Snapshot, error) {
	candidate, base, err := e.repo.LoadApprovedCandidate(ctx, tenantID, siteID, candidateID)
	if err != nil {
		return Snapshot{}, err
	}
	now := e.now().UTC()
	snapshotID, err := uuidv7(now)
	if err != nil {
		return Snapshot{}, err
	}
	revisionID, err := uuidv7(now)
	if err != nil {
		return Snapshot{}, err
	}
	next := Snapshot{ID: snapshotID, RevisionNo: base.RevisionNo + 1, DatasetRevision: base.DatasetRevision + 1, PreviousSnapshotID: base.ID, SettlementRevisionID: revisionID, Period: base.Period, Calculation: candidate.Calculation, CreatedAt: now}
	if err = e.repo.ApplyRevision(ctx, candidate, base, next, now); err != nil {
		return Snapshot{}, err
	}
	next.Period.Status = "REVISED"
	return next, nil
}

func calculate(period Period, bindings []MetricBinding, tariff Tariff, facts []Fact) (Calculation, error) {
	if len(bindings) == 0 {
		return Calculation{}, errors.New("settlement boundary has no released Metric bindings")
	}
	location, err := time.LoadLocation(period.Timezone)
	if err != nil {
		return Calculation{}, err
	}
	calc := Calculation{
		EnergyBreakdown: map[string]float64{}, DemandKW: map[string]float64{}, EnergyCost: map[string]float64{}, DemandCost: map[string]float64{},
		Currency: tariff.Currency, Quality: "GOOD", Completeness: 1, TariffVersionID: tariff.VersionID,
		MeterRefs: append([]string(nil), period.MeterBindingRefs...), SourceMetricRevisions: map[string]uint64{},
	}
	bindingByID := make(map[string]MetricBinding, len(bindings))
	for _, binding := range bindings {
		bindingByID[binding.MetricBindingID] = binding
	}
	bindingRefs := map[string]struct{}{}
	versionRefs := map[string]struct{}{}
	completenessTotal := 0.0
	for _, fact := range facts {
		binding, ok := bindingByID[fact.MetricBindingID]
		if !ok || fact.MetricVersionID != binding.MetricVersionID {
			return Calculation{}, errors.New("settlement Metric result does not match the released binding revision")
		}
		periodCode := strings.TrimSpace(fact.TariffPeriodCode)
		var tariffSlice TariffPeriod
		if periodCode == "" {
			tariffSlice, err = tariffFor(tariff.Periods, fact.Start.In(location))
		} else {
			tariffSlice, err = tariffByCode(tariff.Periods, periodCode)
		}
		if err != nil {
			return Calculation{}, err
		}
		switch fact.Role {
		case "ENERGY":
			calc.EnergyBreakdown[tariffSlice.Code] += fact.Value
			calc.EnergyCost[tariffSlice.Code] += fact.Value * tariffSlice.EnergyRate
		case "DEMAND":
			if fact.Value > calc.DemandKW[tariffSlice.Code] {
				calc.DemandKW[tariffSlice.Code] = fact.Value
			}
		default:
			return Calculation{}, fmt.Errorf("unsupported settlement Metric role %s", fact.Role)
		}
		bindingRefs[fact.MetricBindingID] = struct{}{}
		versionRefs[fact.MetricVersionID] = struct{}{}
		calc.SourceRefs = append(calc.SourceRefs, fact.ID)
		if fact.Revision > calc.SourceMetricRevisions[fact.MetricBindingID] {
			calc.SourceMetricRevisions[fact.MetricBindingID] = fact.Revision
		}
		if fact.CalculatedAt.After(calc.SourceWatermark) {
			calc.SourceWatermark = fact.CalculatedAt.UTC()
		}
		completenessTotal += fact.Completeness
		if fact.Quality != "GOOD" {
			calc.Quality = "PARTIAL"
		}
	}
	for _, binding := range bindings {
		if _, found := bindingRefs[binding.MetricBindingID]; !found {
			calc.MissingMetricBindingRefs = append(calc.MissingMetricBindingRefs, binding.MetricBindingID)
		}
	}
	for _, tariffSlice := range tariff.Periods {
		if demand := calc.DemandKW[tariffSlice.Code]; demand > 0 && tariffSlice.DemandRate > 0 {
			calc.DemandCost[tariffSlice.Code] = demand * tariffSlice.DemandRate
		}
	}
	for _, value := range calc.EnergyCost {
		calc.TotalCost += value
	}
	for _, value := range calc.DemandCost {
		calc.TotalCost += value
	}
	denominator := len(facts) + len(calc.MissingMetricBindingRefs)
	if denominator > 0 {
		calc.Completeness = completenessTotal / float64(denominator)
	} else {
		calc.Completeness = 0
	}
	if calc.Completeness < 1 || len(calc.MissingMetricBindingRefs) > 0 {
		calc.Quality = "PARTIAL"
	}
	for id := range bindingRefs {
		calc.MetricBindingRefs = append(calc.MetricBindingRefs, id)
	}
	for id := range versionRefs {
		calc.MetricVersionRefs = append(calc.MetricVersionRefs, id)
	}
	sort.Strings(calc.MetricBindingRefs)
	sort.Strings(calc.MetricVersionRefs)
	sort.Strings(calc.MissingMetricBindingRefs)
	sort.Strings(calc.SourceRefs)
	return calc, nil
}

func tariffByCode(periods []TariffPeriod, code string) (TariffPeriod, error) {
	for _, period := range periods {
		if period.Code == code {
			return period, nil
		}
	}
	return TariffPeriod{}, fmt.Errorf("tariff period %s is not defined", code)
}

func tariffFor(periods []TariffPeriod, at time.Time) (TariffPeriod, error) {
	day := "WEEKDAY"
	if at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
		day = "WEEKEND"
	}
	minute := at.Hour()*60 + at.Minute()
	for _, period := range periods {
		if period.DayType == day && minute >= period.StartMinute && minute < period.EndMinute {
			return period, nil
		}
	}
	return TariffPeriod{}, fmt.Errorf("no tariff slice for %s minute %d", day, minute)
}

func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func uuidv7(now time.Time) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	milliseconds := uint64(now.UnixMilli())
	bytes[0] = byte(milliseconds >> 40)
	bytes[1] = byte(milliseconds >> 32)
	bytes[2] = byte(milliseconds >> 24)
	bytes[3] = byte(milliseconds >> 16)
	bytes[4] = byte(milliseconds >> 8)
	bytes[5] = byte(milliseconds)
	bytes[6] = (bytes[6] & 15) | 0x70
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(bytes)
	return hexValue[:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:], nil
}
