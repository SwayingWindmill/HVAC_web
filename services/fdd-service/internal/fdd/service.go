package fdd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/planttwin"
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

// ErrNoLoopEvidence means no minute of the window holds both a supply and a return reading.
var ErrNoLoopEvidence = errors.New("low-delta-T evaluation requires the loop's supply and return readings in the window")

type FindingFilter struct {
	AlarmID     string
	WorkOrderID string
	Limit       int
}

type Store interface {
	// CurrentRule is the Tenant's latest low-delta-T rule revision in effect at a time.
	CurrentRule(ctx context.Context, tenantID, siteID string, at time.Time) (RuleRevision, error)
	InsertFinding(context.Context, intelligencemodel.FDDFinding) error
	// RecentFinding is the latest finding of a type for an asset and rule whose window ended at or after since.
	RecentFinding(ctx context.Context, tenantID, siteID, assetID, findingType, ruleRevisionID string, since time.Time) (intelligencemodel.FDDFinding, bool, error)
	ListFindings(context.Context, string, string, FindingFilter) ([]intelligencemodel.FDDFinding, error)
	LinkFinding(context.Context, string, string, string, string, string, time.Time) (intelligencemodel.FDDFinding, error)
}

// WindowQuery asks for the per-minute samples of the loop and gate points from From up to,
// not including, To.
type WindowQuery struct {
	TenantID      string
	SiteID        string
	LoopDeviceID  string
	GateDeviceIDs []string
	Points        []PointRef
	From          time.Time
	To            time.Time
}

// EvidenceSource reads a window's per-minute samples from Telemetry history.
type EvidenceSource interface {
	WindowSamples(context.Context, WindowQuery) ([]Sample, error)
}

// MeterBalanceSource reads hourly site meter balance rows for meter-imbalance diagnostics.
type MeterBalanceSource interface {
	MeterBalanceHours(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]MeterBalanceHour, error)
}

type Service struct {
	store    Store
	twins    planttwin.Loader
	evidence EvidenceSource
	now      func() time.Time
}

func NewService(store Store, twins planttwin.Loader, evidence EvidenceSource, now func() time.Time) (*Service, error) {
	if store == nil {
		return nil, errors.New("FDD store is required")
	}
	if twins == nil {
		return nil, errors.New("FDD plant twin loader is required")
	}
	if evidence == nil {
		return nil, errors.New("FDD evidence source is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, twins: twins, evidence: evidence, now: now}, nil
}

// CurrentRule is the low-delta-T rule revision in effect for the Site's Tenant now.
func (service *Service) CurrentRule(ctx context.Context, tenantID, siteID string) (RuleRevision, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return RuleRevision{}, errors.New("FDD rule scope is invalid")
	}
	return service.store.CurrentRule(ctx, tenantID, siteID, service.now().UTC())
}

func (service *Service) EvaluateLowDeltaT(ctx context.Context, request EvaluationRequest) (EvaluationResult, error) {
	if err := request.Validate(); err != nil {
		return EvaluationResult{}, err
	}
	at := request.At.UTC()
	rule, err := service.store.CurrentRule(ctx, request.TenantID, request.SiteID, at)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("read the low-delta-T rule: %w", err)
	}
	twin, err := service.twins.LoadSiteTwin(ctx, planttwin.SiteScope{TenantID: request.TenantID, SiteID: request.SiteID}, at)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("read the Site's semantic plant twin: %w", err)
	}
	from, to := WindowMinutes(rule, at)
	schedEval, err := evaluateTwinSchedule(twin, to)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("evaluate operating schedule: %w", err)
	}
	if suppressionCode, suppressed := scheduleSuppressionReason(schedEval); suppressed {
		return EvaluationResult{
			Status: StatusNotOperating,
			Rule:   rule,
			Window: suppressedWindowAssessment(rule, schedEval, suppressionCode),
		}, nil
	}

	topology := resolveTopologyFromTwin(twin, request)
	hold := max(rule.OperatingGates.TemperatureHoldMinutes, rule.OperatingGates.GateHoldMinutes)
	points := topology.PointRefs()
	var samples []Sample
	if len(points) > 0 {
		samples, err = service.evidence.WindowSamples(ctx, WindowQuery{
			TenantID:      request.TenantID,
			SiteID:        request.SiteID,
			LoopDeviceID:  request.DeviceID,
			GateDeviceIDs: topology.DeviceIDs(),
			Points:        points,
			From:          from.Add(-time.Duration(hold) * time.Minute),
			To:            to,
		})
		if err != nil {
			return EvaluationResult{}, fmt.Errorf("read loop evidence: %w", err)
		}
	}
	window, err := AssessWindow(rule, topology, request.DeviceID, samples, at)
	if err != nil {
		return EvaluationResult{}, err
	}
	if schedEval.OperatingState != planttwin.OperatingStateUnconfigured {
		window.ScheduleState = schedEval.OperatingState
		window.Gates = append(window.Gates, GateOutcome{
			Gate:          OperatingScheduleGate,
			Applied:       true,
			Bases:         []string{schedEval.OperatingState},
			PassedMinutes: rule.WindowMinutes,
		})
	}
	result := EvaluationResult{Status: window.Status, DeltaTC: window.MeanDeltaTC, Rule: rule, Window: window}
	if window.Status != statusLow {
		return result, nil
	}
	open, found, err := service.store.RecentFinding(ctx, request.TenantID, request.SiteID, request.AssetID, lowDeltaTFindingType, rule.ID, to.Add(-findingRecurrence))
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("read recent FDD finding: %w", err)
	}
	if found {
		result.Status, result.Finding = "ONGOING", &open
		return result, nil
	}
	findingID, err := schedulerjob.UUIDv7(service.now().UTC())
	if err != nil {
		return EvaluationResult{}, err
	}
	deficitRatio := math.Max(0, (rule.MinimumDeltaTC-window.MeanDeltaTC)/rule.MinimumDeltaTC)
	finding := intelligencemodel.FDDFinding{
		ID: findingID, TenantID: request.TenantID, SiteID: request.SiteID, AssetID: request.AssetID,
		FindingType: lowDeltaTFindingType, EvaluationFrom: from, EvaluationTo: to,
		EvidenceIDs:    window.LowEvidenceIDs,
		RuleRevisionID: rule.ID, Confidence: math.Min(0.99, 0.5+0.5*deficitRatio), CreatedAt: service.now().UTC(),
		ObservedValue: window.MeanDeltaTC, ThresholdValue: rule.MinimumDeltaTC, ValueUnit: "K",
	}
	if err = service.store.InsertFinding(ctx, finding); err != nil {
		return EvaluationResult{}, fmt.Errorf("persist FDD finding: %w", err)
	}
	result.Status, result.Finding = "FINDING", &finding
	return result, nil
}

// EvaluateMeterImbalance evaluates hourly main-meter vs sub-meter energy balance for sustained
// gaps or submeter-exceeds-main anomalies, deduplicating findings on a 24-hour recurrence window.
func (service *Service) EvaluateMeterImbalance(ctx context.Context, req MeterImbalanceRequest) (MeterImbalanceResult, error) {
	if err := req.Validate(); err != nil {
		return MeterImbalanceResult{}, err
	}
	balanceSource, ok := service.evidence.(MeterBalanceSource)
	if !ok {
		return MeterImbalanceResult{}, errors.New("FDD evidence source does not support meter balance queries")
	}
	from := req.From.UTC()
	to := req.To.UTC()
	maxRatio, minMainKWh, minConsecutive := req.normalized()

	hours, err := balanceSource.MeterBalanceHours(ctx, req.TenantID, req.SiteID, from, to)
	if err != nil {
		return MeterImbalanceResult{}, fmt.Errorf("read meter balance evidence: %w", err)
	}

	sorted := append([]MeterBalanceHour(nil), hours...)
	slices.SortFunc(sorted, func(a, b MeterBalanceHour) int {
		return a.Hour.UTC().Compare(b.Hour.UTC())
	})

	var (
		overallWorst   *MeterBalanceHour
		triggeredHours []MeterBalanceHour
		currentRun     []MeterBalanceHour
	)
	flushRun := func() {
		if len(currentRun) >= minConsecutive {
			triggeredHours = append(triggeredHours, currentRun...)
		}
		currentRun = currentRun[:0]
	}

	for i := range sorted {
		h := sorted[i]
		if !finite(h.SiteKWh) || h.SiteKWh < minMainKWh || h.ImbalanceRatio == nil || !finite(*h.ImbalanceRatio) {
			flushRun()
			continue
		}
		ratio := *h.ImbalanceRatio
		if overallWorst == nil || math.Abs(ratio) > math.Abs(*overallWorst.ImbalanceRatio) {
			candidate := h
			overallWorst = &candidate
		}
		if math.Abs(ratio) <= maxRatio {
			flushRun()
			continue
		}
		curHour := h.Hour.UTC().Truncate(time.Hour)
		if len(currentRun) > 0 {
			prevHour := currentRun[len(currentRun)-1].Hour.UTC().Truncate(time.Hour)
			if curHour.Sub(prevHour) != time.Hour {
				flushRun()
			}
		}
		currentRun = append(currentRun, h)
	}
	flushRun()

	if len(triggeredHours) == 0 {
		result := MeterImbalanceResult{
			Status:          StatusClear,
			ImbalanceStatus: "BALANCED",
			ThresholdRatio:  maxRatio,
		}
		if overallWorst != nil {
			result.WorstRatio = *overallWorst.ImbalanceRatio
			if finite(overallWorst.DiffKWh) {
				result.ObservedGapKWh = overallWorst.DiffKWh
			}
		}
		return result, nil
	}

	worst := triggeredHours[0]
	evidenceIDs := make([]string, 0, len(triggeredHours))
	for _, h := range triggeredHours {
		if math.Abs(*h.ImbalanceRatio) > math.Abs(*worst.ImbalanceRatio) {
			worst = h
		}
		evidenceIDs = append(evidenceIDs, fmt.Sprintf("meter-balance:%s:%s", req.SiteID, h.Hour.UTC().Format(time.RFC3339)))
	}
	worstRatio := *worst.ImbalanceRatio
	observedGapKWh := 0.0
	if finite(worst.DiffKWh) {
		observedGapKWh = worst.DiffKWh
	}
	imbalanceStatus := "UNMETERED_GAP"
	if worstRatio < 0 {
		imbalanceStatus = "SUBMETER_EXCEEDS_MAIN"
	}

	result := MeterImbalanceResult{
		Status:          "FINDING",
		ImbalanceStatus: imbalanceStatus,
		WorstRatio:      worstRatio,
		ObservedGapKWh:  observedGapKWh,
		ThresholdRatio:  maxRatio,
		AffectedHours:   len(triggeredHours),
	}

	open, found, err := service.store.RecentFinding(ctx, req.TenantID, req.SiteID, req.SiteID, MeterImbalanceRuleCode, MeterBalanceRuleRevisionID, to.Add(-findingRecurrence))
	if err != nil {
		return MeterImbalanceResult{}, fmt.Errorf("read recent FDD finding: %w", err)
	}
	if found {
		result.Status = "ONGOING"
		result.Finding = &open
		return result, nil
	}

	now := service.now().UTC()
	findingID, err := schedulerjob.UUIDv7(now)
	if err != nil {
		return MeterImbalanceResult{}, err
	}
	excessRatio := math.Max(0, (math.Abs(worstRatio)-maxRatio)/math.Max(maxRatio, 0.01))
	finding := intelligencemodel.FDDFinding{
		ID:             findingID,
		TenantID:       req.TenantID,
		SiteID:         req.SiteID,
		AssetID:        req.SiteID,
		FindingType:    MeterImbalanceRuleCode,
		EvaluationFrom: from,
		EvaluationTo:   to,
		EvidenceIDs:    evidenceIDs,
		RuleRevisionID: MeterBalanceRuleRevisionID,
		Confidence:     math.Min(0.99, 0.5+0.5*math.Min(1.0, excessRatio)),
		CreatedAt:      now,
		ObservedValue:  worstRatio,
		ThresholdValue: maxRatio,
		ValueUnit:      "1",
	}
	if err = service.store.InsertFinding(ctx, finding); err != nil {
		return MeterImbalanceResult{}, fmt.Errorf("persist FDD finding: %w", err)
	}
	result.Finding = &finding
	return result, nil
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
