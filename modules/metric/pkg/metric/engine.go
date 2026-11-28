package metric

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/measurement"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

type Dependency struct {
	Type     string
	Code     string
	MetricID string
	Required bool
}

var (
	// ErrNoNumericObservations signals that a required point dependency had no quality-eligible numeric samples in the window.
	ErrNoNumericObservations = errors.New("metric point dependency has no numeric observations")
	// ErrEmptyPointDependency signals that zero observations fell inside the requested evaluation window.
	ErrEmptyPointDependency = errors.New("metric point dependency is empty")
	// ErrMissingMetricDependency signals that an upstream metric dependency has no published result for the window.
	ErrMissingMetricDependency = errors.New("load current Metric dependency result")
)

type Binding struct {
	TenantID          string
	SiteID            string
	BindingID         string
	MetricVersionID   string
	MetricID          string
	MetricCode        string
	MetricVersion     int64
	BindingVersion    int64
	SubjectType       string
	SubjectID         string
	Granularity       string
	DataType          string
	Unit              string
	Aggregation       string
	CalculationMethod string
	QualityPolicy     string
	SourceDefinition  map[string]any
	Dependencies      []Dependency
}

type Input struct {
	Code            string  `json:"code"`
	Type            string  `json:"type"`
	Reference       string  `json:"reference"`
	Value           float64 `json:"value"`
	FirstValue      float64 `json:"firstValue"`
	LastValue       float64 `json:"lastValue"`
	Count           int64   `json:"count"`
	DurationSeconds float64 `json:"durationSeconds"`
	Integral        float64 `json:"integral"`
	// A counter's steps between consecutive readings: the sum of its rises, how often and by how
	// much it fell, and the readings right after each fall.
	Rise            float64 `json:"rise"`
	Drops           int64   `json:"drops"`
	DropSum         float64 `json:"dropSum"`
	ValueAfterDrops float64 `json:"valueAfterDrops"`
	Quality         string  `json:"quality"`
	Completeness    float64 `json:"completeness"`
}

type Result struct {
	RunID          string
	ResultID       string
	Revision       uint64
	SchedulerJobID string
	Binding        Binding
	PeriodStart    time.Time
	PeriodEnd      time.Time
	CalculatedAt   time.Time
	Value          float64
	Quality        string
	Completeness   float64
	Inputs         []Input
}

type RunRequest struct {
	TenantID       string
	SiteID         string
	BindingID      string
	SchedulerJobID string
	PeriodStart    time.Time
	PeriodEnd      time.Time
	Reason         string
}

type Registry interface {
	LoadBinding(context.Context, string, string, string, time.Time) (Binding, error)
	LoadCurrentMetricInput(context.Context, Binding, Dependency, time.Time, time.Time) (Input, error)
	CreateRun(context.Context, Result, string, []byte) (Result, error)
	MarkRunRunning(context.Context, string, string, string, time.Time) error
	BeginPublication(context.Context, Result, time.Time) (Result, error)
	CompletePublication(context.Context, Result, time.Time) error
	FailRun(context.Context, Result, string, time.Time) error
	ListStalePublications(context.Context, time.Time, int) ([]Result, error)
}

type SeriesStore interface {
	ReadPoint(context.Context, Binding, string, time.Time, time.Time) (Input, error)
	ReadMetricResult(context.Context, Result) (Result, error)
	InsertMetric(context.Context, Result) error
	HasMetricResult(context.Context, Result) (bool, error)
}

// BatchSeriesStore optionally persists multiple reconciled metric results in one ClickHouse block.
type BatchSeriesStore interface {
	InsertMetrics(context.Context, []Result) error
}

type Engine struct {
	registry Registry
	series   SeriesStore
	latest   LatestStore
	now      func() time.Time
}

func New(registry Registry, series SeriesStore, latest LatestStore) (*Engine, error) {
	if registry == nil || series == nil || latest == nil {
		return nil, errors.New("metric registry, series store, and Redis Latest store are required")
	}
	return &Engine{registry: registry, series: series, latest: latest, now: time.Now}, nil
}

type copSeriesStore interface {
	ReadEnergyWeightedCOP(context.Context, Binding, string, string, time.Time, time.Time, COPGuardrailOptions) (COPAggregate, error)
}

func (e *Engine) Execute(ctx context.Context, request RunRequest) (Result, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.SiteID) == "" || strings.TrimSpace(request.BindingID) == "" || !request.PeriodEnd.After(request.PeriodStart) {
		return Result{}, errors.New("metric run request is invalid")
	}
	if request.Reason == "" {
		request.Reason = "SCHEDULED"
	}
	binding, err := e.registry.LoadBinding(ctx, request.TenantID, request.SiteID, request.BindingID, request.PeriodStart)
	if err != nil {
		return Result{}, err
	}
	inputs, handledValue, hasHandledValue, handledQuality, handledCompleteness, err := e.resolveInputs(ctx, binding, request.PeriodStart, request.PeriodEnd)
	if err != nil {
		return Result{}, err
	}
	var value float64
	if hasHandledValue {
		value = handledValue
	} else {
		value, err = calculate(binding, inputs)
		if err != nil {
			return Result{}, err
		}
	}
	quality, completeness := summarize(inputs, binding.QualityPolicy)
	if hasHandledValue && handledQuality != "" {
		quality = handledQuality
		completeness = handledCompleteness
		if binding.QualityPolicy == "ESTIMATION_ALLOWED" && quality != "GOOD" {
			quality = "ESTIMATED"
		}
	}
	if binding.QualityPolicy == "STRICT" && quality != "GOOD" {
		return Result{}, errors.New("metric strict quality policy rejected inputs")
	}
	now := e.now().UTC()
	schedulerJobID := strings.TrimSpace(request.SchedulerJobID)
	runID := schedulerJobID
	if runID == "" {
		runID, err = uuidv7.NewAt(now)
		if err != nil {
			return Result{}, err
		}
	}
	resultID, err := uuidv7.NewAt(now)
	if err != nil {
		return Result{}, err
	}
	result := Result{RunID: runID, ResultID: resultID, SchedulerJobID: schedulerJobID, Binding: binding, PeriodStart: request.PeriodStart.UTC(), PeriodEnd: request.PeriodEnd.UTC(), CalculatedAt: now, Value: value, Quality: quality, Completeness: completeness, Inputs: inputs}
	refs, _ := json.Marshal(inputs)
	result, err = e.registry.CreateRun(ctx, result, request.Reason, refs)
	if err != nil {
		return Result{}, err
	}
	if !hasHandledValue {
		value, err = calculate(binding, result.Inputs)
		if err != nil {
			return Result{}, err
		}
		quality, completeness = summarize(result.Inputs, binding.QualityPolicy)
		if binding.QualityPolicy == "STRICT" && quality != "GOOD" {
			return Result{}, errors.New("metric strict quality policy rejected persisted run inputs")
		}
	}
	result.Value, result.Quality, result.Completeness = value, quality, completeness
	if err = e.registry.MarkRunRunning(ctx, binding.TenantID, binding.SiteID, result.RunID, now); err != nil {
		return Result{}, err
	}
	result, err = e.registry.BeginPublication(ctx, result, e.now().UTC())
	if err != nil {
		return Result{}, err
	}
	if err = e.series.InsertMetric(ctx, result); err != nil {
		// The ClickHouse outcome may be unknown (for example a lost HTTP response).
		// Keep the publication PERSISTING so reconciliation can prove or replay the
		// exact same Result ID/revision instead of manufacturing a second Result.
		return Result{}, err
	}
	if err = e.registry.CompletePublication(ctx, result, e.now().UTC()); err != nil {
		// Leave the publication in PERSISTING. Reconciliation can prove whether
		// the ClickHouse result exists by calculation_run_id/result_id and finish
		// the PostgreSQL state + durable event without duplicating the result.
		return Result{}, err
	}
	// Redis Latest is explicitly rebuildable. A cache write failure must not
	// roll back a durable ClickHouse result + PostgreSQL publication fact.
	_ = e.latest.PutMetric(ctx, result)
	return result, nil
}

func (e *Engine) Reconcile(ctx context.Context, staleBefore time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	stale, err := e.registry.ListStalePublications(ctx, staleBefore, limit)
	if err != nil {
		return 0, err
	}
	return e.reconcileResults(ctx, stale)
}

type scopedPublicationRegistry interface {
	ListStalePublicationsForScope(context.Context, string, string, time.Time, int) ([]Result, error)
}

func (e *Engine) ReconcileScope(ctx context.Context, tenantID, siteID string, staleBefore time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	registry, ok := e.registry.(scopedPublicationRegistry)
	if !ok {
		return 0, errors.New("metric Registry does not support scoped reconciliation")
	}
	stale, err := registry.ListStalePublicationsForScope(ctx, tenantID, siteID, staleBefore, limit)
	if err != nil {
		return 0, err
	}
	return e.reconcileResults(ctx, stale)
}

func (e *Engine) reconcileResults(ctx context.Context, stale []Result) (int, error) {
	repaired := 0
	missing := make([]Result, 0, len(stale))
	completed := make([]Result, 0, len(stale))
	for _, result := range stale {
		exists, checkErr := e.series.HasMetricResult(ctx, result)
		if checkErr != nil {
			return repaired, checkErr
		}
		if !exists {
			missing = append(missing, result)
			continue
		}
		loaded, checkErr := e.series.ReadMetricResult(ctx, result)
		if checkErr != nil {
			return repaired, checkErr
		}
		completed = append(completed, loaded)
	}
	if len(missing) > 0 {
		if batchStore, ok := e.series.(BatchSeriesStore); ok {
			if checkErr := batchStore.InsertMetrics(ctx, missing); checkErr != nil {
				return repaired, checkErr
			}
			completed = append(completed, missing...)
		} else {
			for _, item := range missing {
				if checkErr := e.series.InsertMetric(ctx, item); checkErr != nil {
					return repaired, checkErr
				}
				completed = append(completed, item)
			}
		}
	}
	for _, result := range completed {
		if err := e.registry.CompletePublication(ctx, result, e.now().UTC()); err != nil {
			return repaired, err
		}
		repaired++
	}
	return repaired, nil
}

func (e *Engine) resolveInputs(ctx context.Context, binding Binding, start, end time.Time) ([]Input, float64, bool, string, float64, error) {
	if copReader, ok := e.series.(copSeriesStore); ok && strings.EqualFold(strings.TrimSpace(binding.CalculationMethod), "COP") {
		if coolingID, powerID, opts, ready := copPointPair(binding); ready {
			agg, err := copReader.ReadEnergyWeightedCOP(ctx, binding, coolingID, powerID, start, end, opts)
			if err != nil {
				return nil, 0, false, "", 0, err
			}
			activeSec := float64(agg.EligibleBuckets * 60)
			inputs := []Input{
				{
					Code:            "cooling",
					Type:            "POINT",
					Reference:       "point:" + coolingID,
					Value:           agg.CoolingKWh,
					FirstValue:      agg.CoolingKWh,
					LastValue:       agg.CoolingKWh,
					Count:           agg.EligibleBuckets,
					DurationSeconds: activeSec,
					Integral:        agg.CoolingKWh,
					Quality:         agg.Quality,
					Completeness:    agg.Completeness,
				},
				{
					Code:            "power",
					Type:            "POINT",
					Reference:       "point:" + powerID,
					Value:           agg.ElectricKWh,
					FirstValue:      agg.ElectricKWh,
					LastValue:       agg.ElectricKWh,
					Count:           agg.EligibleBuckets,
					DurationSeconds: activeSec,
					Integral:        agg.ElectricKWh,
					Quality:         agg.Quality,
					Completeness:    agg.Completeness,
				},
			}
			return inputs, agg.COP, true, agg.Quality, agg.Completeness, nil
		}
	}

	inputs := make([]Input, 0, len(binding.Dependencies))
	for _, dep := range binding.Dependencies {
		var (
			input Input
			err   error
		)
		switch dep.Type {
		case "POINT":
			pointIDs, ok := stringSliceMap(binding.SourceDefinition, "points", dep.Code)
			if !ok {
				if dep.Required {
					return nil, 0, false, "", 0, fmt.Errorf("metric point dependency %s is not bound", dep.Code)
				}
				continue
			}
			input, err = e.readBoundPoints(ctx, binding, pointIDs, start, end)
		case "METRIC":
			input, err = e.registry.LoadCurrentMetricInput(ctx, binding, dep, start, end)
		case "EXTERNAL":
			value, ok := numberMap(binding.SourceDefinition, "externals", dep.Code)
			if !ok {
				if dep.Required {
					return nil, 0, false, "", 0, fmt.Errorf("metric external dependency %s is not bound", dep.Code)
				}
				continue
			}
			input = Input{Code: dep.Code, Type: dep.Type, Reference: "binding:externals:" + dep.Code, Value: value, FirstValue: value, LastValue: value, Count: 1, Quality: "GOOD", Completeness: 1}
		default:
			return nil, 0, false, "", 0, fmt.Errorf("unsupported metric dependency type %s", dep.Type)
		}
		if err != nil {
			if dep.Required {
				return nil, 0, false, "", 0, fmt.Errorf("resolve metric dependency %s: %w", dep.Code, err)
			}
			continue
		}
		input.Code, input.Type = dep.Code, dep.Type
		inputs = append(inputs, input)
	}
	if len(inputs) == 0 {
		return nil, 0, false, "", 0, errors.New("metric run has no resolved inputs")
	}
	return inputs, 0, false, "", 0, nil
}

func copPointPair(binding Binding) (string, string, COPGuardrailOptions, bool) {
	if len(binding.Dependencies) < 2 {
		return "", "", COPGuardrailOptions{}, false
	}
	var coolingCode, powerCode string
	for _, dep := range binding.Dependencies {
		if dep.Type != "POINT" {
			return "", "", COPGuardrailOptions{}, false
		}
		code := strings.ToLower(strings.TrimSpace(dep.Code))
		switch {
		case strings.Contains(code, "cool") || strings.HasPrefix(code, "q_"):
			coolingCode = dep.Code
		case strings.Contains(code, "power") || strings.Contains(code, "elec") || strings.Contains(code, "energy"):
			powerCode = dep.Code
		}
	}
	if coolingCode == "" || powerCode == "" {
		coolingCode = binding.Dependencies[0].Code
		powerCode = binding.Dependencies[1].Code
	}
	cIDs, okC := stringSliceMap(binding.SourceDefinition, "points", coolingCode)
	pIDs, okP := stringSliceMap(binding.SourceDefinition, "points", powerCode)
	if !okC || !okP || len(cIDs) != 1 || len(pIDs) != 1 {
		return "", "", COPGuardrailOptions{}, false
	}
	opts := copGuardrailOptionsFromBinding(binding)
	if tz, ok := stringValue(binding.SourceDefinition, "timezone"); ok {
		opts.SiteTimezone = tz
	}
	if dtIDs, ok := stringSliceMap(binding.SourceDefinition, "points", "delta_t"); ok && len(dtIDs) == 1 {
		opts.DeltaTPointID = dtIDs[0]
	}
	if supIDs, ok := stringSliceMap(binding.SourceDefinition, "points", "supply_temp"); ok && len(supIDs) == 1 {
		if retIDs, ok := stringSliceMap(binding.SourceDefinition, "points", "return_temp"); ok && len(retIDs) == 1 {
			opts.SupplyPointID = supIDs[0]
			opts.ReturnPointID = retIDs[0]
		}
	}
	return cIDs[0], pIDs[0], opts, true
}

func calculate(binding Binding, inputs []Input) (float64, error) {
	if len(inputs) == 0 {
		return 0, errors.New("metric calculation requires at least one input")
	}
	values := make([]float64, len(inputs))
	byCode := make(map[string]float64, len(inputs))
	for i := range inputs {
		if err := validateFiniteInput(inputs[i]); err != nil {
			return 0, err
		}
		values[i] = inputs[i].Value
		byCode[inputs[i].Code] = inputs[i].Value
	}
	method := strings.ToUpper(strings.TrimSpace(binding.CalculationMethod))
	var (
		out float64
		err error
	)
	switch method {
	case "IDENTITY":
		if len(values) != 1 {
			return 0, errors.New("IDENTITY requires one input")
		}
		out = values[0]
	case "SUM":
		for _, x := range values {
			out += x
		}
	case "AVG", "AVERAGE":
		if isCOPBinding(binding, inputs) {
			return 0, errors.New("COP aggregation cannot use arithmetic mean; use energy-weighted cooling and electric totals")
		}
		for _, x := range values {
			out += x
		}
		out /= float64(len(values))
	case "MIN":
		out = min(values)
	case "MAX":
		out = max(values)
	case "FIRST":
		if len(inputs) != 1 {
			return 0, errors.New("FIRST requires one input")
		}
		out = inputs[0].FirstValue
	case "LAST":
		if len(inputs) != 1 {
			return 0, errors.New("LAST requires one input")
		}
		out = inputs[0].LastValue
	case "DELTA":
		if len(inputs) != 1 {
			return 0, errors.New("DELTA requires one counter input")
		}
		out, err = counterDelta(binding, inputs[0])
	case "COUNT":
		var count int64
		for _, in := range inputs {
			count += in.Count
		}
		out = float64(count)
	case "DURATION":
		for _, in := range inputs {
			out += in.DurationSeconds
		}
	case "INTEGRAL":
		for _, in := range inputs {
			out += in.Integral
		}
	case "DIFFERENCE":
		if len(values) != 2 {
			return 0, errors.New("DIFFERENCE requires two inputs")
		}
		out = values[0] - values[1]
		if isThermalDeltaBinding(binding) {
			if err = validateThermalDelta(binding, out); err != nil {
				return 0, err
			}
		}
	case "RATIO":
		if len(values) != 2 || values[1] == 0 {
			return 0, errors.New("RATIO requires two inputs and non-zero denominator")
		}
		if isCOPBinding(binding, inputs) {
			out, err = calculateEnergyWeightedCOP(binding, inputs)
		} else {
			out = values[0] / values[1]
		}
	case "COP":
		out, err = calculateEnergyWeightedCOP(binding, inputs)
	case "COOLING_CAPACITY", "THERMAL_POWER":
		out, err = calculateThermalPower(binding, byCode)
	case "EXPRESSION":
		expression, ok := stringValue(binding.SourceDefinition, "expression")
		if !ok {
			return 0, errors.New("EXPRESSION requires sourceDefinition.expression")
		}
		out, err = evaluateExpression(expression, byCode)
		if err == nil && isCOPBinding(binding, inputs) {
			err = validateThermalAndCOPContext(binding, inputs, byCode, out)
		}
	case "MODEL":
		return 0, errors.New("MODEL calculation requires a registered Metric model evaluator")
	default:
		return 0, fmt.Errorf("unsupported metric calculation method %s", method)
	}
	if err != nil {
		return 0, err
	}
	if math.IsNaN(out) || math.IsInf(out, 0) {
		return 0, errors.New("metric calculation produced non-finite result")
	}
	return out, nil
}

func validateFiniteInput(in Input) error {
	for _, v := range []float64{in.Value, in.FirstValue, in.LastValue, in.DurationSeconds, in.Integral, in.Rise, in.DropSum, in.ValueAfterDrops, in.Completeness} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("metric input %s contains non-finite value", in.Code)
		}
	}
	return nil
}

func isCOPBinding(binding Binding, inputs []Input) bool {
	if strings.EqualFold(strings.TrimSpace(binding.CalculationMethod), "COP") {
		return true
	}
	if _, ok := binding.SourceDefinition["cop"].(map[string]any); ok {
		return true
	}
	code := strings.ToLower(strings.TrimSpace(binding.MetricCode))
	if strings.Contains(code, "cop") {
		return true
	}
	var hasCooling, hasPower bool
	for _, in := range inputs {
		c := strings.ToLower(strings.TrimSpace(in.Code))
		if strings.Contains(c, "cool") {
			hasCooling = true
		}
		if strings.Contains(c, "power") || strings.Contains(c, "elec") {
			hasPower = true
		}
	}
	return hasCooling && hasPower
}

func isThermalDeltaBinding(binding Binding) bool {
	if _, ok := binding.SourceDefinition["thermal"].(map[string]any); ok {
		return true
	}
	code := strings.ToLower(strings.TrimSpace(binding.MetricCode))
	return strings.Contains(code, "delta_t") || strings.Contains(code, "chilled_delta")
}

func copGuardrailOptionsFromBinding(binding Binding) COPGuardrailOptions {
	opts := COPGuardrailOptions{
		MinPowerKW: 5.0,
		MinDeltaTK: 0.5,
		MinCOP:     0.5,
		MaxCOP:     12.0,
	}
	if copCfg, ok := binding.SourceDefinition["cop"].(map[string]any); ok {
		if v, ok := floatFromAny(copCfg["minPowerKw"]); ok {
			opts.MinPowerKW = v
		} else if v, ok := floatFromAny(copCfg["standbyPowerKw"]); ok {
			opts.MinPowerKW = v
		}
		if v, ok := floatFromAny(copCfg["minCoolingKw"]); ok {
			opts.MinCoolingKW = v
		}
		if v, ok := floatFromAny(copCfg["minDeltaTK"]); ok {
			opts.MinDeltaTK = v
		}
		if v, ok := floatFromAny(copCfg["minCop"]); ok && v > 0 {
			opts.MinCOP = v
		}
		if v, ok := floatFromAny(copCfg["maxCop"]); ok && v > 0 {
			opts.MaxCOP = v
		}
	}
	if thermalCfg, ok := binding.SourceDefinition["thermal"].(map[string]any); ok {
		if v, ok := floatFromAny(thermalCfg["minDeltaTK"]); ok {
			opts.MinDeltaTK = v
		}
	}
	return opts
}

func validateThermalDelta(binding Binding, deltaT float64) error {
	if math.IsNaN(deltaT) || math.IsInf(deltaT, 0) {
		return errors.New("thermal delta-T must be finite")
	}
	if deltaT < 0 {
		return errors.New("negative thermal delta-T rejected")
	}
	opts := copGuardrailOptionsFromBinding(binding)
	if deltaT < opts.MinDeltaTK {
		return fmt.Errorf("thermal delta-T %.3f K below deadband %.3f K", deltaT, opts.MinDeltaTK)
	}
	return nil
}

func validateThermalAndCOPContext(binding Binding, inputs []Input, byCode map[string]float64, cop float64) error {
	if math.IsNaN(cop) || math.IsInf(cop, 0) {
		return errors.New("COP calculation produced non-finite value")
	}
	opts := copGuardrailOptionsFromBinding(binding)
	if deltaT, ok := resolveDeltaTFromInputs(byCode); ok {
		if err := validateThermalDelta(binding, deltaT); err != nil {
			return err
		}
	}
	if powerIn, hasPower := findInputByRole(inputs, "power", "elec", "energy"); hasPower {
		if powerIn.Value <= 0 {
			return errors.New("COP requires positive power or energy input")
		}
		effectivePower := effectivePowerForDeadband(binding, powerIn)
		if opts.MinPowerKW > 0 && effectivePower < opts.MinPowerKW {
			return fmt.Errorf("power %.3f kW is in standby deadband (< %.3f kW)", effectivePower, opts.MinPowerKW)
		}
	}
	if coolingIn, hasCooling := findInputByRole(inputs, "cool"); hasCooling {
		if coolingIn.Value < 0 || coolingIn.Value < opts.MinCoolingKW {
			return errors.New("COP cooling output is negative or below minimum threshold")
		}
	}
	if cop < opts.MinCOP || cop > opts.MaxCOP {
		return fmt.Errorf("COP %.3f outside physical bounds [%.2f, %.2f]", cop, opts.MinCOP, opts.MaxCOP)
	}
	return nil
}

func calculateEnergyWeightedCOP(binding Binding, inputs []Input) (float64, error) {
	if len(inputs) < 2 {
		return 0, errors.New("COP requires cooling and power/energy inputs")
	}
	byCode := make(map[string]float64, len(inputs))
	for _, in := range inputs {
		byCode[in.Code] = in.Value
	}
	opts := copGuardrailOptionsFromBinding(binding)
	if deltaT, ok := resolveDeltaTFromInputs(byCode); ok {
		if err := validateThermalDelta(binding, deltaT); err != nil {
			return 0, err
		}
	}

	coolingIn, hasCooling := findInputByRole(inputs, "cool")
	powerIn, hasPower := findInputByRole(inputs, "power", "elec", "energy")
	if !hasCooling || !hasPower {
		coolingIn = inputs[0]
		powerIn = inputs[1]
	}

	// Energy-weighted aggregation: prefer integrated energy (Integral) when both rate
	// inputs carry positive time integration, so uneven sampling durations conserve energy.
	coolingNum := coolingIn.Value
	powerDenom := powerIn.Value
	if strings.EqualFold(strings.TrimSpace(binding.Aggregation), "AVG") && coolingIn.Integral > 0 && powerIn.Integral > 0 {
		coolingNum = coolingIn.Integral
		powerDenom = powerIn.Integral
	}
	if coolingNum < 0 || coolingIn.Value < opts.MinCoolingKW {
		return 0, errors.New("COP cooling energy/output cannot be negative or below threshold")
	}
	if powerDenom <= 0 || powerIn.Value <= 0 {
		return 0, errors.New("COP requires positive power/energy denominator")
	}
	effectivePower := effectivePowerForDeadband(binding, powerIn)
	if opts.MinPowerKW > 0 && effectivePower < opts.MinPowerKW {
		return 0, fmt.Errorf("chiller/plant power %.3f kW is in standby deadband (< %.3f kW)", effectivePower, opts.MinPowerKW)
	}
	cop := coolingNum / powerDenom
	if math.IsNaN(cop) || math.IsInf(cop, 0) {
		return 0, errors.New("COP calculation produced non-finite value")
	}
	if cop < opts.MinCOP || cop > opts.MaxCOP {
		return 0, fmt.Errorf("COP %.3f outside physical bounds [%.2f, %.2f]", cop, opts.MinCOP, opts.MaxCOP)
	}
	return cop, nil
}

func calculateThermalPower(binding Binding, byCode map[string]float64) (float64, error) {
	flow, hasFlow := firstPresent(byCode, "flow", "chw_flow", "flow_rate")
	if !hasFlow || flow < 0 {
		return 0, errors.New("thermal power requires non-negative flow input")
	}
	deltaT, ok := resolveDeltaTFromInputs(byCode)
	if !ok {
		return 0, errors.New("thermal power requires delta_t or supply/return temperatures")
	}
	if err := validateThermalDelta(binding, deltaT); err != nil {
		return 0, err
	}
	// Specific heat factor 1.163 kW per (m3/h * K) matches gateway BTU calculation.
	q := measurement.WaterFactorKW * flow * deltaT
	if math.IsNaN(q) || math.IsInf(q, 0) {
		return 0, errors.New("thermal power calculation produced non-finite value")
	}
	return q, nil
}

func findInputByRole(inputs []Input, keywords ...string) (Input, bool) {
	for _, in := range inputs {
		c := strings.ToLower(strings.TrimSpace(in.Code))
		for _, kw := range keywords {
			if strings.Contains(c, kw) {
				return in, true
			}
		}
	}
	return Input{}, false
}

func effectivePowerForDeadband(binding Binding, powerIn Input) float64 {
	if powerIn.DurationSeconds > 0 && (powerIn.Type == "METRIC" || strings.EqualFold(binding.Unit, "kWh") || strings.Contains(strings.ToLower(powerIn.Code), "energy") || strings.Contains(strings.ToLower(powerIn.Code), "kwh")) {
		return powerIn.Value * 3600.0 / powerIn.DurationSeconds
	}
	return powerIn.Value
}

func resolveDeltaTFromInputs(byCode map[string]float64) (float64, bool) {
	if dt, ok := firstPresent(byCode, "delta_t", "chilled_delta", "chw_delta_t"); ok {
		return dt, true
	}
	ret, okRet := firstPresent(byCode, "return_temp", "t_return", "chw_return", "t_ret")
	sup, okSup := firstPresent(byCode, "supply_temp", "t_supply", "chw_supply", "t_sup")
	if okRet && okSup {
		return ret - sup, true
	}
	return 0, false
}

func firstPresent(values map[string]float64, keys ...string) (float64, bool) {
	for _, key := range keys {
		if v, ok := values[key]; ok {
			return v, true
		}
	}
	return 0, false
}

// counterDelta is what a counter accumulated over a window: every rise between consecutive
// readings, plus what each fall stands for under the binding's declared decrease mode.
func counterDelta(binding Binding, input Input) (float64, error) {
	if err := validateFiniteInput(input); err != nil {
		return 0, err
	}
	if input.Drops < 0 || input.Rise < 0 || input.ValueAfterDrops < 0 || input.DropSum > 0 {
		return 0, errors.New("counter input contains invalid negative/inverted step summary")
	}
	counter, _ := binding.SourceDefinition["counter"].(map[string]any)
	mode, _ := counter["decreaseMode"].(string)
	normalizedMode := strings.ToUpper(strings.TrimSpace(mode))

	var delta float64
	if input.Drops == 0 {
		delta = input.Rise
	} else {
		switch normalizedMode {
		case "RESET", "RESET_TO_ZERO":
			// The counter restarted from zero, so the reading after the fall is all new.
			delta = input.Rise + input.ValueAfterDrops
		case "ROLLOVER":
			modulus, ok := floatFromAny(counter["rolloverModulus"])
			if !ok || modulus <= 0 {
				return 0, errors.New("counter rollover requires a positive rolloverModulus")
			}
			rolloverStep := input.DropSum + float64(input.Drops)*modulus
			if rolloverStep < 0 {
				return 0, errors.New("counter rollover drop exceeds declared rolloverModulus")
			}
			// Each wrap counted up to the modulus and on from zero: the fall plus one modulus.
			delta = input.Rise + rolloverStep
		case "METER_REPLACEMENT":
			carry, hasCarry := floatFromAny(counter["replacementCarry"])
			if hasCarry && carry < 0 {
				return 0, errors.New("counter replacementCarry must be non-negative")
			}
			delta = input.Rise + input.ValueAfterDrops + float64(input.Drops)*carry
		case "INVALID":
			return 0, errors.New("counter decreased under INVALID decreaseMode policy")
		default:
			return 0, errors.New("counter decreased without RESET, RESET_TO_ZERO, ROLLOVER, METER_REPLACEMENT, or INVALID policy")
		}
	}

	if math.IsNaN(delta) || math.IsInf(delta, 0) || delta < 0 {
		return 0, errors.New("counter delta produced invalid non-finite or negative value")
	}
	if !strings.HasPrefix(input.Reference, "points:") {
		if maxAllowed, limited := configuredCounterMaxDelta(counter, input); limited && delta > maxAllowed {
			return 0, fmt.Errorf("counter delta %.3f exceeds physical step ceiling %.3f", delta, maxAllowed)
		}
	}
	return delta, nil
}

func configuredCounterMaxDelta(counter map[string]any, input Input) (float64, bool) {
	if counter == nil {
		return 0, false
	}
	if maxStep, ok := floatFromAny(counter["maxStep"]); ok && maxStep > 0 {
		return maxStep, true
	}
	if maxPower, ok := floatFromAny(counter["maxPowerKw"]); ok && maxPower > 0 {
		hours := input.DurationSeconds / 3600.0
		if hours < 1.0/60.0 {
			hours = 1.0 / 60.0
		}
		return maxPower * hours, true
	}
	return 0, false
}

func floatFromAny(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	default:
		return 0, false
	}
}

func evaluateExpression(expression string, values map[string]float64) (float64, error) {
	node, err := parser.ParseExpr(expression)
	if err != nil {
		return 0, fmt.Errorf("parse metric expression: %w", err)
	}
	var eval func(ast.Expr) (float64, error)
	eval = func(expr ast.Expr) (float64, error) {
		var result float64
		switch value := expr.(type) {
		case *ast.BasicLit:
			if value.Kind != token.INT && value.Kind != token.FLOAT {
				return 0, errors.New("expression literal must be numeric")
			}
			if _, err := fmt.Sscan(value.Value, &result); err != nil {
				return 0, err
			}
		case *ast.Ident:
			number, ok := values[value.Name]
			if !ok {
				return 0, fmt.Errorf("unknown metric expression dependency %s", value.Name)
			}
			result = number
		case *ast.ParenExpr:
			return eval(value.X)
		case *ast.UnaryExpr:
			number, err := eval(value.X)
			if err != nil {
				return 0, err
			}
			switch value.Op {
			case token.SUB:
				result = -number
			case token.ADD:
				result = number
			default:
				return 0, errors.New("unsupported unary operator")
			}
		case *ast.BinaryExpr:
			left, err := eval(value.X)
			if err != nil {
				return 0, err
			}
			right, err := eval(value.Y)
			if err != nil {
				return 0, err
			}
			switch value.Op {
			case token.ADD:
				result = left + right
			case token.SUB:
				result = left - right
			case token.MUL:
				result = left * right
			case token.QUO:
				if right == 0 {
					return 0, errors.New("expression division by zero")
				}
				result = left / right
			default:
				return 0, errors.New("unsupported expression operator")
			}
		default:
			return 0, errors.New("metric expression contains unsupported syntax")
		}
		if math.IsNaN(result) || math.IsInf(result, 0) {
			return 0, errors.New("expression evaluation produced non-finite value")
		}
		return result, nil
	}
	return eval(node)
}

func stringValue(root map[string]any, key string) (string, bool) {
	value, ok := root[key].(string)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

func summarize(inputs []Input, policy string) (string, float64) {
	quality := "GOOD"
	completeness := 1.0
	for _, in := range inputs {
		if in.Completeness < completeness {
			completeness = in.Completeness
		}
		if in.Quality != "GOOD" {
			quality = "PARTIAL"
		}
	}
	if policy == "ESTIMATION_ALLOWED" && quality != "GOOD" {
		quality = "ESTIMATED"
	}
	return quality, math.Max(0, math.Min(1, completeness))
}

func stringMap(root map[string]any, section, key string) (string, bool) {
	m, ok := root[section].(map[string]any)
	if !ok {
		return "", false
	}
	v, ok := m[key].(string)
	return strings.TrimSpace(v), ok && strings.TrimSpace(v) != ""
}

func stringSliceMap(root map[string]any, section, key string) ([]string, bool) {
	if single, ok := stringMap(root, section, key); ok {
		return []string{single}, true
	}
	m, ok := root[section].(map[string]any)
	if !ok {
		return nil, false
	}
	switch raw := m[key].(type) {
	case []string:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				return nil, false
			}
			out = append(out, trimmed)
		}
		return out, len(out) > 0
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			str, ok := item.(string)
			trimmed := strings.TrimSpace(str)
			if !ok || trimmed == "" {
				return nil, false
			}
			out = append(out, trimmed)
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

// readBoundPoints resolves either a single bound Point or a synthesized sum across multiple
// sub-meter Points. When one or more sub-meters are silent in the window, the reported sub-meters
// are summed and the combined input is marked PARTIAL with completeness scaled by sub-meter count.
func (e *Engine) readBoundPoints(ctx context.Context, binding Binding, pointIDs []string, start, end time.Time) (Input, error) {
	if len(pointIDs) == 1 {
		return e.series.ReadPoint(ctx, binding, pointIDs[0], start, end)
	}
	isDelta := strings.EqualFold(strings.TrimSpace(binding.CalculationMethod), "DELTA")
	var (
		combined        Input
		reported        int
		allGood         = true
		completenessSum float64
		lastErr         error
	)
	combined.Reference = "points:" + strings.Join(pointIDs, ",")
	for _, pointID := range pointIDs {
		sub, err := e.series.ReadPoint(ctx, binding, pointID, start, end)
		if err != nil {
			lastErr = err
			allGood = false
			continue
		}
		reported++
		completenessSum += sub.Completeness
		if sub.Quality != "GOOD" {
			allGood = false
		}
		combined.Value += sub.Value
		combined.FirstValue += sub.FirstValue
		combined.LastValue += sub.LastValue
		combined.Count += sub.Count
		if sub.DurationSeconds > combined.DurationSeconds {
			combined.DurationSeconds = sub.DurationSeconds
		}
		combined.Integral += sub.Integral
		if isDelta {
			subDelta, deltaErr := counterDelta(binding, sub)
			if deltaErr != nil {
				return Input{}, deltaErr
			}
			combined.Rise += subDelta
		} else {
			combined.Rise += sub.Rise
			combined.Drops += sub.Drops
			combined.DropSum += sub.DropSum
			combined.ValueAfterDrops += sub.ValueAfterDrops
		}
	}
	if reported == 0 {
		if lastErr != nil {
			return Input{}, lastErr
		}
		return Input{}, ErrNoNumericObservations
	}
	if reported == len(pointIDs) && allGood {
		combined.Quality = "GOOD"
		combined.Completeness = 1
	} else {
		combined.Quality = "PARTIAL"
		combined.Completeness = completenessSum / float64(len(pointIDs))
	}
	return combined, nil
}
func numberMap(root map[string]any, section, key string) (float64, bool) {
	m, ok := root[section].(map[string]any)
	if !ok {
		return 0, false
	}
	v, ok := m[key].(float64)
	return v, ok
}
func min(v []float64) float64 {
	r := v[0]
	for _, x := range v[1:] {
		if x < r {
			r = x
		}
	}
	return r
}
func max(v []float64) float64 {
	r := v[0]
	for _, x := range v[1:] {
		if x > r {
			r = x
		}
	}
	return r
}

func SortedInputs(inputs []Input) []Input {
	out := append([]Input(nil), inputs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}
