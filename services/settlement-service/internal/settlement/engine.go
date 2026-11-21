package settlement

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/quanlaihe/hvac-web/libs/fixedpoint"
	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
)

type Period struct {
	TenantID, SiteID, ID, BoundaryID, Timezone, Status string
	Start, End                                         time.Time
	MeterBindingRefs                                   []string
}

type MetricBinding struct {
	ID, MetricID, MetricBindingID, MetricVersionID, MetricCode, Role, TariffPeriodCode string
}

type TariffPeriod struct {
	ID, Code, DayType      string
	StartMinute, EndMinute int
	EnergyRate, DemandRate float64
}

type TariffVersion struct {
	ID, VersionID, Currency, Timezone string
	Version                           int64
	EffectiveFrom                     time.Time
	EffectiveTo                       *time.Time
	Periods                           []TariffPeriod
}

type Tariff struct {
	ID, VersionID, Currency, Timezone string
	Version                           int64
	EffectiveFrom                     time.Time
	EffectiveTo                       *time.Time
	Periods                           []TariffPeriod
	Versions                          []TariffVersion
	Holidays                          map[string]string
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
	TariffVersionRefs        []string `json:"TariffVersionRefs,omitempty"`
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
	periodLocation, err := loadExplicitLocation(period.Timezone)
	if err != nil {
		return Calculation{}, err
	}
	versions, err := normalizeTariffVersions(tariff)
	if err != nil {
		return Calculation{}, err
	}
	versionLocations := make([]*time.Location, len(versions))
	for index, version := range versions {
		loc := periodLocation
		if strings.TrimSpace(version.Timezone) != "" {
			loc, err = loadExplicitLocation(version.Timezone)
			if err != nil {
				return Calculation{}, err
			}
		}
		versionLocations[index] = loc
	}
	calc := Calculation{
		EnergyBreakdown: map[string]float64{}, DemandKW: map[string]float64{}, EnergyCost: map[string]float64{}, DemandCost: map[string]float64{},
		Currency: versions[0].Currency, Quality: "GOOD", Completeness: 1, TariffVersionID: versions[0].VersionID,
		TariffVersionRefs: []string{versions[0].VersionID},
		MeterRefs:         append([]string(nil), period.MeterBindingRefs...), SourceMetricRevisions: map[string]uint64{},
	}
	bindingByID := make(map[string]MetricBinding, len(bindings))
	for _, binding := range bindings {
		bindingByID[binding.MetricBindingID] = binding
	}
	bindingRefs := map[string]struct{}{}
	versionRefs := map[string]struct{}{}
	usedTariffVersions := []string{}
	seenTariffVersions := map[string]struct{}{}
	recordTariffVersion := func(versionID string) {
		if versionID == "" {
			return
		}
		if _, exists := seenTariffVersions[versionID]; !exists {
			seenTariffVersions[versionID] = struct{}{}
			usedTariffVersions = append(usedTariffVersions, versionID)
		}
	}
	energyByCode := map[string]fixedpoint.Decimal6{}
	energyCostByCode := map[string]fixedpoint.Decimal6{}
	demandKWByCode := map[string]fixedpoint.Decimal6{}
	demandCostMaxByCode := map[string]fixedpoint.Decimal6{}
	completenessTotal := 0.0
	for _, fact := range facts {
		binding, ok := bindingByID[fact.MetricBindingID]
		if !ok || fact.MetricVersionID != binding.MetricVersionID {
			return Calculation{}, errors.New("settlement Metric result does not match the released binding revision")
		}
		if math.IsNaN(fact.Value) || math.IsInf(fact.Value, 0) || math.IsNaN(fact.Completeness) || math.IsInf(fact.Completeness, 0) {
			return Calculation{}, errors.New("settlement Metric fact contains non-finite numeric value")
		}
		if fact.Start.IsZero() {
			return Calculation{}, errors.New("settlement Metric fact start timestamp is required")
		}
		factValDec, valErr := fixedpoint.FromFloat64(fact.Value)
		if valErr != nil {
			return Calculation{}, fmt.Errorf("settlement Metric fact value overflow: %w", valErr)
		}
		segments, segErr := resolveFactTariffSegments(versions, fact.Start.UTC(), fact.End.UTC())
		if segErr != nil {
			return Calculation{}, segErr
		}
		periodCode := strings.TrimSpace(fact.TariffPeriodCode)
		switch fact.Role {
		case "ENERGY":
			for _, segment := range segments {
				version := versions[segment.versionIndex]
				loc := versionLocations[segment.versionIndex]
				tariffSlice, sliceErr := resolveTariffSlice(version.Periods, periodCode, segment.start.In(loc), tariff.Holidays)
				if sliceErr != nil {
					return Calculation{}, sliceErr
				}
				segmentEnergyDec := factValDec
				if len(segments) > 1 {
					segmentEnergyDec, err = mulDivDuration(factValDec, segment.duration, segment.totalDuration)
					if err != nil {
						return Calculation{}, fmt.Errorf("settlement segment energy calculation: %w", err)
					}
				}
				energyByCode[tariffSlice.Code], err = energyByCode[tariffSlice.Code].Add(segmentEnergyDec)
				if err != nil {
					return Calculation{}, fmt.Errorf("settlement energy accumulation: %w", err)
				}
				energyRateDec, rateErr := fixedpoint.FromFloat64(tariffSlice.EnergyRate)
				if rateErr != nil {
					return Calculation{}, fmt.Errorf("settlement energy rate: %w", rateErr)
				}
				segmentCostDec, costErr := segmentEnergyDec.Mul(energyRateDec)
				if costErr != nil {
					return Calculation{}, fmt.Errorf("settlement energy cost: %w", costErr)
				}
				energyCostByCode[tariffSlice.Code], err = energyCostByCode[tariffSlice.Code].Add(segmentCostDec)
				if err != nil {
					return Calculation{}, fmt.Errorf("settlement energy cost accumulation: %w", err)
				}
				recordTariffVersion(version.VersionID)
			}
		case "DEMAND":
			for _, segment := range segments {
				version := versions[segment.versionIndex]
				loc := versionLocations[segment.versionIndex]
				tariffSlice, sliceErr := resolveTariffSlice(version.Periods, periodCode, segment.start.In(loc), tariff.Holidays)
				if sliceErr != nil {
					return Calculation{}, sliceErr
				}
				if factValDec > demandKWByCode[tariffSlice.Code] {
					demandKWByCode[tariffSlice.Code] = factValDec
				}
				demandRateDec, rateErr := fixedpoint.FromFloat64(tariffSlice.DemandRate)
				if rateErr != nil {
					return Calculation{}, fmt.Errorf("settlement demand rate: %w", rateErr)
				}
				if factValDec > 0 && demandRateDec > 0 {
					candidateCostDec, costErr := factValDec.Mul(demandRateDec)
					if costErr != nil {
						return Calculation{}, fmt.Errorf("settlement demand cost: %w", costErr)
					}
					if candidateCostDec > demandCostMaxByCode[tariffSlice.Code] {
						demandCostMaxByCode[tariffSlice.Code] = candidateCostDec
					}
				}
				recordTariffVersion(version.VersionID)
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
	if len(usedTariffVersions) > 0 {
		calc.TariffVersionRefs = usedTariffVersions
		calc.TariffVersionID = usedTariffVersions[0]
	}
	for _, binding := range bindings {
		if _, found := bindingRefs[binding.MetricBindingID]; !found {
			calc.MissingMetricBindingRefs = append(calc.MissingMetricBindingRefs, binding.MetricBindingID)
		}
	}
	for code, energyDec := range energyByCode {
		calc.EnergyBreakdown[code] = energyDec.Float64()
	}
	for code, demandDec := range demandKWByCode {
		calc.DemandKW[code] = demandDec.Float64()
	}
	totalCostDec := fixedpoint.Zero
	for code, costDec := range energyCostByCode {
		rounded := costDec.RoundHalfUp(2)
		calc.EnergyCost[code] = rounded.Float64()
		totalCostDec, err = totalCostDec.Add(rounded)
		if err != nil {
			return Calculation{}, fmt.Errorf("settlement total energy cost: %w", err)
		}
	}
	for code, demandCostDec := range demandCostMaxByCode {
		if demandCostDec > 0 {
			rounded := demandCostDec.RoundHalfUp(2)
			calc.DemandCost[code] = rounded.Float64()
			totalCostDec, err = totalCostDec.Add(rounded)
			if err != nil {
				return Calculation{}, fmt.Errorf("settlement total demand cost: %w", err)
			}
		}
	}
	calc.TotalCost = totalCostDec.RoundHalfUp(2).Float64()
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

type tariffSegment struct {
	versionIndex  int
	start         time.Time
	duration      time.Duration
	totalDuration time.Duration
}

func mulDivDuration(value fixedpoint.Decimal6, num, den time.Duration) (fixedpoint.Decimal6, error) {
	if num == den || den == 0 {
		return value, nil
	}
	prod := new(big.Int).Mul(big.NewInt(value.Int64Scaled()), big.NewInt(int64(num)))
	denInt := big.NewInt(int64(den))
	q, rem := new(big.Int).QuoRem(prod, denInt, new(big.Int))
	absRem2 := new(big.Int).Abs(rem)
	absRem2.Lsh(absRem2, 1)
	if absRem2.Cmp(denInt) >= 0 {
		if prod.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	if !q.IsInt64() {
		return 0, fixedpoint.ErrOverflow
	}
	return fixedpoint.FromInt64Scaled(q.Int64()), nil
}

func loadExplicitLocation(raw string) (*time.Location, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" || strings.EqualFold(clean, "Local") {
		return nil, errors.New("settlement timezone must be an explicit IANA timezone")
	}
	loc, err := time.LoadLocation(clean)
	if err != nil {
		return nil, fmt.Errorf("invalid settlement timezone %q: %w", clean, err)
	}
	return loc, nil
}

func normalizeTariffVersions(tariff Tariff) ([]TariffVersion, error) {
	var versions []TariffVersion
	if len(tariff.Versions) > 0 {
		versions = make([]TariffVersion, len(tariff.Versions))
		for index, v := range tariff.Versions {
			v.EffectiveFrom = v.EffectiveFrom.UTC()
			if v.EffectiveTo != nil {
				end := v.EffectiveTo.UTC()
				v.EffectiveTo = &end
				if !end.After(v.EffectiveFrom) {
					return nil, errors.New("released tariff version effective window is empty")
				}
			}
			if len(v.Periods) == 0 {
				return nil, errors.New("released tariff version has no tariff periods")
			}
			versions[index] = v
		}
	} else {
		if len(tariff.Periods) == 0 {
			return nil, errors.New("released tariff version has no tariff periods")
		}
		var effectiveTo *time.Time
		if tariff.EffectiveTo != nil {
			end := tariff.EffectiveTo.UTC()
			effectiveTo = &end
			if !end.After(tariff.EffectiveFrom.UTC()) {
				return nil, errors.New("released tariff version effective window is empty")
			}
		}
		versions = []TariffVersion{{
			ID:            tariff.ID,
			VersionID:     tariff.VersionID,
			Currency:      tariff.Currency,
			Timezone:      tariff.Timezone,
			Version:       tariff.Version,
			EffectiveFrom: tariff.EffectiveFrom.UTC(),
			EffectiveTo:   effectiveTo,
			Periods:       tariff.Periods,
		}}
	}
	sort.SliceStable(versions, func(left, right int) bool {
		if !versions[left].EffectiveFrom.Equal(versions[right].EffectiveFrom) {
			return versions[left].EffectiveFrom.Before(versions[right].EffectiveFrom)
		}
		if versions[left].Version != versions[right].Version {
			return versions[left].Version < versions[right].Version
		}
		return versions[left].VersionID < versions[right].VersionID
	})
	currency := versions[0].Currency
	for index, version := range versions {
		if version.Currency != currency {
			return nil, errors.New("settlement period spans mismatched tariff currencies")
		}
		if index > 0 {
			prev := versions[index-1]
			if prev.EffectiveTo == nil || version.EffectiveFrom.Before(*prev.EffectiveTo) {
				return nil, fmt.Errorf("overlapping released tariff versions at %s", version.EffectiveFrom.UTC().Format(time.RFC3339))
			}
		}
	}
	return versions, nil
}

func resolveFactTariffSegments(versions []TariffVersion, start, end time.Time) ([]tariffSegment, error) {
	if !end.After(start) {
		index, err := versionAt(versions, start)
		if err != nil {
			return nil, err
		}
		return []tariffSegment{{versionIndex: index, start: start, duration: 1, totalDuration: 1}}, nil
	}
	totalDuration := end.Sub(start)
	segments := []tariffSegment{}
	cursor := start
	for cursor.Before(end) {
		index, err := versionAt(versions, cursor)
		if err != nil {
			return nil, err
		}
		version := versions[index]
		segEnd := end
		if version.EffectiveTo != nil && version.EffectiveTo.Before(segEnd) {
			segEnd = *version.EffectiveTo
		}
		if !segEnd.After(cursor) {
			return nil, fmt.Errorf("non-advancing tariff version boundary at %s", cursor.UTC().Format(time.RFC3339))
		}
		segments = append(segments, tariffSegment{versionIndex: index, start: cursor, duration: segEnd.Sub(cursor), totalDuration: totalDuration})
		cursor = segEnd
	}
	return segments, nil
}

func versionAt(versions []TariffVersion, at time.Time) (int, error) {
	matched := -1
	for index, version := range versions {
		if version.EffectiveFrom.IsZero() && version.EffectiveTo == nil {
			if matched >= 0 {
				return -1, fmt.Errorf("multiple released tariff versions overlap at %s", at.UTC().Format(time.RFC3339))
			}
			matched = index
			continue
		}
		if !at.Before(version.EffectiveFrom) && (version.EffectiveTo == nil || at.Before(*version.EffectiveTo)) {
			if matched >= 0 {
				return -1, fmt.Errorf("multiple released tariff versions overlap at %s", at.UTC().Format(time.RFC3339))
			}
			matched = index
		}
	}
	if matched < 0 {
		return -1, fmt.Errorf("no released tariff version active at %s", at.UTC().Format(time.RFC3339))
	}
	return matched, nil
}

func resolveSettlementDayType(periods []TariffPeriod, localizedAt time.Time, holidays map[string]string) string {
	defined := make(map[string]struct{}, len(periods))
	for _, period := range periods {
		defined[period.DayType] = struct{}{}
	}
	return tariffmodel.ResolveDayType(localizedAt, holidays, func(dayType string) bool {
		_, ok := defined[dayType]
		return ok
	})
}

func resolveTariffSlice(periods []TariffPeriod, periodCode string, localizedAt time.Time, holidays map[string]string) (TariffPeriod, error) {
	day := resolveSettlementDayType(periods, localizedAt, holidays)
	minute := localizedAt.Hour()*60 + localizedAt.Minute()
	if periodCode != "" {
		return tariffByCode(periods, periodCode, day, minute)
	}
	return tariffFor(periods, day, minute)
}

func tariffByCode(periods []TariffPeriod, code, day string, minute int) (TariffPeriod, error) {
	for _, period := range periods {
		if period.Code == code && period.DayType == day && minute >= period.StartMinute && minute < period.EndMinute {
			return period, nil
		}
	}
	for _, period := range periods {
		if period.Code == code && period.DayType == day {
			return period, nil
		}
	}
	for _, period := range periods {
		if period.Code == code {
			return period, nil
		}
	}
	return TariffPeriod{}, fmt.Errorf("tariff period %s is not defined", code)
}

func tariffFor(periods []TariffPeriod, day string, minute int) (TariffPeriod, error) {
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
