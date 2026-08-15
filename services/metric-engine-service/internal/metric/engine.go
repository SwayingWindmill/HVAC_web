package metric

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
)

type Dependency struct {
	Type     string
	Code     string
	MetricID string
	Required bool
}

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
	Quality         string  `json:"quality"`
	Completeness    float64 `json:"completeness"`
}

type Result struct {
	RunID        string
	ResultID     string
	Binding      Binding
	PeriodStart  time.Time
	PeriodEnd    time.Time
	CalculatedAt time.Time
	Value        float64
	Quality      string
	Completeness float64
	Inputs       []Input
}

type RunRequest struct {
	TenantID    string
	SiteID      string
	BindingID   string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Reason      string
}

type Registry interface {
	LoadBinding(context.Context, string, string, string, time.Time) (Binding, error)
	CreateRun(context.Context, Result, string, []byte) error
	MarkRunRunning(context.Context, string, string, string, time.Time) error
	BeginPublication(context.Context, Result, time.Time) error
	CompletePublication(context.Context, Result, time.Time) error
	FailRun(context.Context, Result, string, time.Time) error
	ListStalePublications(context.Context, time.Time, int) ([]Result, error)
}

type SeriesStore interface {
	ReadPoint(context.Context, Binding, string, time.Time, time.Time) (Input, error)
	ReadMetric(context.Context, Binding, Dependency, time.Time, time.Time) (Input, error)
	InsertMetric(context.Context, Result) error
	HasMetricResult(context.Context, string) (bool, error)
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
	inputs := make([]Input, 0, len(binding.Dependencies))
	for _, dep := range binding.Dependencies {
		var input Input
		switch dep.Type {
		case "POINT":
			pointID, ok := stringMap(binding.SourceDefinition, "points", dep.Code)
			if !ok {
				if dep.Required {
					return Result{}, fmt.Errorf("metric point dependency %s is not bound", dep.Code)
				}
				continue
			}
			input, err = e.series.ReadPoint(ctx, binding, pointID, request.PeriodStart, request.PeriodEnd)
		case "METRIC":
			input, err = e.series.ReadMetric(ctx, binding, dep, request.PeriodStart, request.PeriodEnd)
		case "EXTERNAL":
			value, ok := numberMap(binding.SourceDefinition, "externals", dep.Code)
			if !ok {
				if dep.Required {
					return Result{}, fmt.Errorf("metric external dependency %s is not bound", dep.Code)
				}
				continue
			}
			input = Input{Code: dep.Code, Type: dep.Type, Reference: "binding:externals:" + dep.Code, Value: value, FirstValue: value, LastValue: value, Count: 1, Quality: "GOOD", Completeness: 1}
		default:
			return Result{}, fmt.Errorf("unsupported metric dependency type %s", dep.Type)
		}
		if err != nil {
			if dep.Required {
				return Result{}, fmt.Errorf("resolve metric dependency %s: %w", dep.Code, err)
			}
			continue
		}
		input.Code, input.Type = dep.Code, dep.Type
		inputs = append(inputs, input)
	}
	if len(inputs) == 0 {
		return Result{}, errors.New("metric run has no resolved inputs")
	}
	value, err := calculate(binding, inputs)
	if err != nil {
		return Result{}, err
	}
	quality, completeness := summarize(inputs, binding.QualityPolicy)
	if binding.QualityPolicy == "STRICT" && quality != "GOOD" {
		return Result{}, errors.New("metric strict quality policy rejected inputs")
	}
	now := e.now().UTC()
	runID, err := uuidv7(now)
	if err != nil {
		return Result{}, err
	}
	resultID, err := uuidv7(now)
	if err != nil {
		return Result{}, err
	}
	result := Result{RunID: runID, ResultID: resultID, Binding: binding, PeriodStart: request.PeriodStart.UTC(), PeriodEnd: request.PeriodEnd.UTC(), CalculatedAt: now, Value: value, Quality: quality, Completeness: completeness, Inputs: inputs}
	refs, _ := json.Marshal(inputs)
	if err = e.registry.CreateRun(ctx, result, request.Reason, refs); err != nil {
		return Result{}, err
	}
	if err = e.registry.MarkRunRunning(ctx, binding.TenantID, binding.SiteID, runID, now); err != nil {
		return Result{}, err
	}
	if err = e.registry.BeginPublication(ctx, result, e.now().UTC()); err != nil {
		return Result{}, err
	}
	if err = e.series.InsertMetric(ctx, result); err != nil {
		_ = e.registry.FailRun(ctx, result, "CLICKHOUSE_INSERT_FAILED", e.now().UTC())
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
	for _, result := range stale {
		exists, checkErr := e.series.HasMetricResult(ctx, result.ResultID)
		if checkErr != nil {
			return repaired, checkErr
		}
		if !exists {
			continue
		}
		if err := e.registry.CompletePublication(ctx, result, e.now().UTC()); err != nil {
			return repaired, err
		}
		repaired++
	}
	return repaired, nil
}

func calculate(binding Binding, inputs []Input) (float64, error) {
	values := make([]float64, len(inputs))
	byCode := make(map[string]float64, len(inputs))
	for i := range inputs {
		values[i] = inputs[i].Value
		byCode[inputs[i].Code] = inputs[i].Value
	}
	method := strings.ToUpper(strings.TrimSpace(binding.CalculationMethod))
	switch method {
	case "IDENTITY":
		if len(values) != 1 {
			return 0, errors.New("IDENTITY requires one input")
		}
		return values[0], nil
	case "SUM":
		var v float64
		for _, x := range values {
			v += x
		}
		return v, nil
	case "AVG", "AVERAGE":
		var v float64
		for _, x := range values {
			v += x
		}
		return v / float64(len(values)), nil
	case "MIN":
		return min(values), nil
	case "MAX":
		return max(values), nil
	case "FIRST":
		if len(inputs) != 1 {
			return 0, errors.New("FIRST requires one input")
		}
		return inputs[0].FirstValue, nil
	case "LAST":
		if len(inputs) != 1 {
			return 0, errors.New("LAST requires one input")
		}
		return inputs[0].LastValue, nil
	case "DELTA":
		if len(inputs) != 1 {
			return 0, errors.New("DELTA requires one counter input")
		}
		return counterDelta(binding, inputs[0])
	case "COUNT":
		var count int64
		for _, in := range inputs {
			count += in.Count
		}
		return float64(count), nil
	case "DURATION":
		var seconds float64
		for _, in := range inputs {
			seconds += in.DurationSeconds
		}
		return seconds, nil
	case "INTEGRAL":
		var integral float64
		for _, in := range inputs {
			integral += in.Integral
		}
		return integral, nil
	case "DIFFERENCE":
		if len(values) != 2 {
			return 0, errors.New("DIFFERENCE requires two inputs")
		}
		return values[0] - values[1], nil
	case "RATIO":
		if len(values) != 2 || values[1] == 0 {
			return 0, errors.New("RATIO requires two inputs and non-zero denominator")
		}
		return values[0] / values[1], nil
	case "EXPRESSION":
		expression, ok := stringValue(binding.SourceDefinition, "expression")
		if !ok {
			return 0, errors.New("EXPRESSION requires sourceDefinition.expression")
		}
		return evaluateExpression(expression, byCode)
	case "MODEL":
		return 0, errors.New("MODEL calculation requires a registered Metric model evaluator")
	default:
		return 0, fmt.Errorf("unsupported metric calculation method %s", method)
	}
}

func counterDelta(binding Binding, input Input) (float64, error) {
	if input.LastValue >= input.FirstValue {
		return input.LastValue - input.FirstValue, nil
	}
	counter, _ := binding.SourceDefinition["counter"].(map[string]any)
	mode, _ := counter["decreaseMode"].(string)
	mode = strings.ToUpper(strings.TrimSpace(mode))
	switch mode {
	case "RESET":
		return input.LastValue, nil
	case "ROLLOVER":
		modulus, ok := counter["rolloverModulus"].(float64)
		if !ok || modulus <= input.FirstValue {
			return 0, errors.New("counter rollover requires rolloverModulus greater than first value")
		}
		return modulus - input.FirstValue + input.LastValue, nil
	case "METER_REPLACEMENT":
		carry, _ := counter["replacementCarry"].(float64)
		return carry + input.LastValue, nil
	default:
		return 0, errors.New("counter decreased without RESET, ROLLOVER, or METER_REPLACEMENT policy")
	}
}

func evaluateExpression(expression string, values map[string]float64) (float64, error) {
	node, err := parser.ParseExpr(expression)
	if err != nil {
		return 0, fmt.Errorf("parse metric expression: %w", err)
	}
	var eval func(ast.Expr) (float64, error)
	eval = func(expr ast.Expr) (float64, error) {
		switch value := expr.(type) {
		case *ast.BasicLit:
			if value.Kind != token.INT && value.Kind != token.FLOAT {
				return 0, errors.New("expression literal must be numeric")
			}
			var number float64
			if _, err := fmt.Sscan(value.Value, &number); err != nil {
				return 0, err
			}
			return number, nil
		case *ast.Ident:
			number, ok := values[value.Name]
			if !ok {
				return 0, fmt.Errorf("unknown metric expression dependency %s", value.Name)
			}
			return number, nil
		case *ast.ParenExpr:
			return eval(value.X)
		case *ast.UnaryExpr:
			number, err := eval(value.X)
			if err != nil {
				return 0, err
			}
			if value.Op == token.SUB {
				return -number, nil
			}
			if value.Op == token.ADD {
				return number, nil
			}
			return 0, errors.New("unsupported unary operator")
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
				return left + right, nil
			case token.SUB:
				return left - right, nil
			case token.MUL:
				return left * right, nil
			case token.QUO:
				if right == 0 {
					return 0, errors.New("expression division by zero")
				}
				return left / right, nil
			default:
				return 0, errors.New("unsupported expression operator")
			}
		default:
			return 0, errors.New("metric expression contains unsupported syntax")
		}
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

func uuidv7(now time.Time) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	ms := uint64(now.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

func SortedInputs(inputs []Input) []Input {
	out := append([]Input(nil), inputs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}
