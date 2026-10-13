package fdd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

const (
	supplyTemperatureKey = "supply_water_temperature"
	returnTemperatureKey = "return_water_temperature"
	lowDeltaTFindingType = "CHILLED_WATER_LOW_DELTA_T"
	// A condition that persists is one finding a day, not one per evaluation (ADR 0020).
	findingRecurrence = 24 * time.Hour
	// EvaluationJobType is the Scheduler job that runs a Site's low-delta-T rule.
	EvaluationJobType = "FDD_EVALUATION"
)

// ErrNoLoopEvidence means the window holds no supply or no return reading to compare.
var ErrNoLoopEvidence = errors.New("low-delta-T evaluation requires the loop's supply and return readings in the window")

type FindingFilter struct {
	AlarmID     string
	WorkOrderID string
	Limit       int
}

type Store interface {
	// CurrentRule is the Tenant's latest revision of a rule in effect at a time.
	CurrentRule(ctx context.Context, tenantID, siteID, ruleCode string, at time.Time) (RuleRevision, error)
	InsertFinding(context.Context, intelligencemodel.FDDFinding) error
	// RecentFinding is the latest finding of a type for an asset and rule whose window ended at or after since.
	RecentFinding(ctx context.Context, tenantID, siteID, assetID, findingType, ruleRevisionID string, since time.Time) (intelligencemodel.FDDFinding, bool, error)
	ListFindings(context.Context, string, string, FindingFilter) ([]intelligencemodel.FDDFinding, error)
	LinkFinding(context.Context, string, string, string, string, string, time.Time) (intelligencemodel.FDDFinding, error)
}

type LoopQuery struct {
	TenantID string
	SiteID   string
	DeviceID string
	From     time.Time
	To       time.Time
}

// Reading is one authoritative observation as Telemetry history recorded it.
type Reading struct {
	ObservationID string
	PointType     string
	ValueType     string
	Unit          string
	Quality       string
	Value         float64
	SampledAt     time.Time
}

type LoopReadings struct {
	Supply Reading
	Return Reading
}

// EvidenceSource reads the latest supply and return readings of a loop's meter in a window.
type EvidenceSource interface {
	LatestLoopReadings(context.Context, LoopQuery) (LoopReadings, error)
}

type Service struct {
	store    Store
	evidence EvidenceSource
	now      func() time.Time
}

func NewService(store Store, evidence EvidenceSource, now func() time.Time) (*Service, error) {
	if store == nil {
		return nil, errors.New("FDD store is required")
	}
	if evidence == nil {
		return nil, errors.New("FDD evidence source is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, evidence: evidence, now: now}, nil
}

// CurrentRule is the low-delta-T rule revision in effect for the Site's Tenant now.
func (service *Service) CurrentRule(ctx context.Context, tenantID, siteID string) (RuleRevision, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return RuleRevision{}, errors.New("FDD rule scope is invalid")
	}
	return service.store.CurrentRule(ctx, tenantID, siteID, LowDeltaTRuleCode, service.now().UTC())
}

func (service *Service) EvaluateLowDeltaT(ctx context.Context, request EvaluationRequest) (EvaluationResult, error) {
	if err := request.Validate(); err != nil {
		return EvaluationResult{}, err
	}
	to := request.At.UTC()
	rule, err := service.store.CurrentRule(ctx, request.TenantID, request.SiteID, LowDeltaTRuleCode, to)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("read the low-delta-T rule: %w", err)
	}
	from := to.Add(-time.Duration(rule.WindowMinutes) * time.Minute)
	readings, err := service.evidence.LatestLoopReadings(ctx, LoopQuery{
		TenantID: request.TenantID, SiteID: request.SiteID, DeviceID: request.DeviceID, From: from, To: to,
	})
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("read loop evidence: %w", err)
	}
	if err = temperatureEvidence(readings.Supply); err != nil {
		return EvaluationResult{}, fmt.Errorf("supply temperature evidence: %w", err)
	}
	if err = temperatureEvidence(readings.Return); err != nil {
		return EvaluationResult{}, fmt.Errorf("return temperature evidence: %w", err)
	}
	deltaT := readings.Return.Value - readings.Supply.Value
	if deltaT >= rule.MinimumDeltaTC {
		return EvaluationResult{Status: "CLEAR", DeltaTC: deltaT, Rule: rule}, nil
	}
	open, found, err := service.store.RecentFinding(ctx, request.TenantID, request.SiteID, request.AssetID, lowDeltaTFindingType, rule.ID, to.Add(-findingRecurrence))
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("read recent FDD finding: %w", err)
	}
	if found {
		return EvaluationResult{Status: "ONGOING", DeltaTC: deltaT, Rule: rule, Finding: &open}, nil
	}
	findingID, err := schedulerjob.UUIDv7(service.now().UTC())
	if err != nil {
		return EvaluationResult{}, err
	}
	deficitRatio := math.Max(0, (rule.MinimumDeltaTC-deltaT)/rule.MinimumDeltaTC)
	confidence := math.Min(0.99, 0.5+0.5*deficitRatio)
	finding := intelligencemodel.FDDFinding{
		ID: findingID, TenantID: request.TenantID, SiteID: request.SiteID, AssetID: request.AssetID,
		FindingType: lowDeltaTFindingType, EvaluationFrom: from, EvaluationTo: to,
		EvidenceIDs:    []string{readings.Supply.ObservationID, readings.Return.ObservationID},
		RuleRevisionID: rule.ID, Confidence: confidence, CreatedAt: service.now().UTC(),
		ObservedValue: deltaT, ThresholdValue: rule.MinimumDeltaTC, ValueUnit: "K",
	}
	if err = service.store.InsertFinding(ctx, finding); err != nil {
		return EvaluationResult{}, fmt.Errorf("persist FDD finding: %w", err)
	}
	return EvaluationResult{Status: "FINDING", DeltaTC: deltaT, Rule: rule, Finding: &finding}, nil
}

func temperatureEvidence(reading Reading) error {
	if reading.PointType != "TELEMETRY" || reading.Quality != "GOOD" {
		return errors.New("latest authoritative observation is not good telemetry")
	}
	if reading.Unit != "Cel" || reading.ValueType != "NUMBER" {
		return errors.New("latest authoritative observation is not a Celsius number")
	}
	if math.IsNaN(reading.Value) || math.IsInf(reading.Value, 0) {
		return errors.New("latest authoritative observation has an invalid numeric value")
	}
	return nil
}

// ScheduledEvaluation is what an FDD_EVALUATION schedule carries: the loop to diagnose. The rule
// in effect at the job's fire time says over which window and against which threshold.
type ScheduledEvaluation struct {
	AssetID  string `json:"assetId"`
	DeviceID string `json:"deviceId"`
}

func ScheduledRequest(job schedulerjob.Job) (EvaluationRequest, error) {
	if job.JobType != EvaluationJobType || job.TriggerType != "SCHEDULE" || job.ScheduledFor.IsZero() {
		return EvaluationRequest{}, errors.New("an FDD evaluation plan only comes from an FDD_EVALUATION schedule")
	}
	var plan ScheduledEvaluation
	if err := decodeStrictFDDJSON(bytes.NewReader(job.Payload), &plan); err != nil {
		return EvaluationRequest{}, fmt.Errorf("decode FDD evaluation plan: %w", err)
	}
	request := EvaluationRequest{TenantID: job.TenantID, SiteID: job.SiteID, AssetID: plan.AssetID, DeviceID: plan.DeviceID, At: job.ScheduledFor.UTC()}
	return request, request.Validate()
}

func (service *Service) ListFindings(ctx context.Context, tenantID, siteID string, filter FindingFilter) ([]intelligencemodel.FDDFinding, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || filter.Limit <= 0 || filter.Limit > 200 {
		return nil, errors.New("FDD list scope or limit is invalid")
	}
	if filter.AlarmID != "" && !uuidPattern.MatchString(filter.AlarmID) {
		return nil, errors.New("FDD Alarm filter must be a UUID")
	}
	if filter.WorkOrderID != "" && !uuidPattern.MatchString(filter.WorkOrderID) {
		return nil, errors.New("FDD Work Order filter must be a UUID")
	}
	return service.store.ListFindings(ctx, tenantID, siteID, filter)
}

func (service *Service) LinkFinding(ctx context.Context, tenantID, siteID, findingID, alarmID, workOrderID string) (intelligencemodel.FDDFinding, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || !uuidPattern.MatchString(findingID) {
		return intelligencemodel.FDDFinding{}, errors.New("FDD link scope is invalid")
	}
	if alarmID == "" && workOrderID == "" {
		return intelligencemodel.FDDFinding{}, errors.New("alarmId or workOrderId is required")
	}
	if alarmID != "" && !uuidPattern.MatchString(alarmID) {
		return intelligencemodel.FDDFinding{}, errors.New("alarmId must be a UUID")
	}
	if workOrderID != "" && !uuidPattern.MatchString(workOrderID) {
		return intelligencemodel.FDDFinding{}, errors.New("workOrderId must be a UUID")
	}
	return service.store.LinkFinding(ctx, tenantID, siteID, findingID, alarmID, workOrderID, service.now().UTC())
}
