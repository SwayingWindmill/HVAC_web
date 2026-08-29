package fdd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

const (
	supplyTemperatureKey = "btu_meter.supply_water_temperature"
	returnTemperatureKey = "btu_meter.return_water_temperature"
	historyPageSize      = telemetryhistorymodel.MaximumHistoryPageSize
)

type FindingFilter struct {
	AlarmID     string
	WorkOrderID string
	Limit       int
}

type Store interface {
	InsertFinding(context.Context, intelligencemodel.FDDFinding) error
	ListFindings(context.Context, string, string, FindingFilter) ([]intelligencemodel.FDDFinding, error)
	LinkFinding(context.Context, string, string, string, string, string, time.Time) (intelligencemodel.FDDFinding, error)
}

type HistorySource interface {
	QueryDeviceHistory(context.Context, telemetryhistorymodel.DeviceHistoryQuery, string) (telemetryhistorymodel.DeviceHistoryResponse, error)
}

type Service struct {
	store   Store
	history HistorySource
	now     func() time.Time
}

func NewService(store Store, history HistorySource, now func() time.Time) (*Service, error) {
	if store == nil {
		return nil, errors.New("FDD store is required")
	}
	if history == nil {
		return nil, errors.New("FDD history source is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, history: history, now: now}, nil
}

func (service *Service) EvaluateLowDeltaT(ctx context.Context, request EvaluationRequest, delegationGrant string) (EvaluationResult, error) {
	if err := request.Validate(); err != nil {
		return EvaluationResult{}, err
	}
	if strings.TrimSpace(delegationGrant) == "" {
		return EvaluationResult{}, errors.New("telemetry history delegation grant is required")
	}

	query, err := (telemetryhistorymodel.DeviceHistoryQuery{
		TenantID: request.TenantID,
		SiteID:   request.SiteID,
		DeviceID: request.DeviceID,
		Keys:     []string{supplyTemperatureKey, returnTemperatureKey},
		From:     request.EvaluationFrom.UTC(),
		To:       request.EvaluationTo.UTC(),
		PageSize: historyPageSize,
	}).Canonical()
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("build FDD history query: %w", err)
	}
	evidence := lowDeltaTEvidenceSet{}
	pageQuery := query
	for {
		response, err := service.history.QueryDeviceHistory(ctx, pageQuery, delegationGrant)
		if err != nil {
			return EvaluationResult{}, fmt.Errorf("query authoritative telemetry history: %w", err)
		}
		if err := response.ValidateFor(pageQuery); err != nil {
			return EvaluationResult{}, fmt.Errorf("validate authoritative telemetry history: %w", err)
		}
		evidence.observe(response.Observations)
		if response.Metadata.NextCursor == nil {
			break
		}
		cursor := strings.TrimSpace(*response.Metadata.NextCursor)
		pageQuery.Cursor = &cursor
		pageQuery, err = pageQuery.Canonical()
		if err != nil {
			return EvaluationResult{}, fmt.Errorf("continue authoritative telemetry history: %w", err)
		}
	}

	supply, returnTemp, err := evidence.values()
	if err != nil {
		return EvaluationResult{}, err
	}
	deltaT := returnTemp.value - supply.value
	if deltaT >= request.MinimumDeltaTC {
		return EvaluationResult{Status: "CLEAR", DeltaTC: deltaT}, nil
	}
	findingID, err := uuidv7(service.now().UTC())
	if err != nil {
		return EvaluationResult{}, err
	}
	deficitRatio := math.Max(0, (request.MinimumDeltaTC-deltaT)/request.MinimumDeltaTC)
	confidence := math.Min(0.99, 0.5+0.5*deficitRatio)
	finding := intelligencemodel.FDDFinding{
		ID: findingID, TenantID: request.TenantID, SiteID: request.SiteID, AssetID: request.AssetID,
		FindingType: "CHILLED_WATER_LOW_DELTA_T", EvaluationFrom: request.EvaluationFrom.UTC(), EvaluationTo: request.EvaluationTo.UTC(),
		EvidenceIDs: []string{supply.observationID, returnTemp.observationID}, ModelDeploymentID: request.ModelDeploymentRevisionID,
		RuleRevisionID: request.RuleRevisionID, Confidence: confidence, CreatedAt: service.now().UTC(),
	}
	if err = service.store.InsertFinding(ctx, finding); err != nil {
		return EvaluationResult{}, fmt.Errorf("persist FDD finding: %w", err)
	}
	return EvaluationResult{Status: "FINDING", DeltaTC: deltaT, Finding: &finding}, nil
}

type evidenceValue struct {
	observationID string
	value         float64
}

type lowDeltaTEvidenceSet struct {
	supply        telemetryhistorymodel.DeviceHistoryObservation
	returnTemp    telemetryhistorymodel.DeviceHistoryObservation
	hasSupply     bool
	hasReturnTemp bool
}

func (evidence *lowDeltaTEvidenceSet) observe(observations []telemetryhistorymodel.DeviceHistoryObservation) {
	for index := range observations {
		observation := observations[index]
		switch observation.TelemetryKey {
		case supplyTemperatureKey:
			evidence.supply = observation
			evidence.hasSupply = true
		case returnTemperatureKey:
			evidence.returnTemp = observation
			evidence.hasReturnTemp = true
		}
	}
}

func (evidence lowDeltaTEvidenceSet) values() (evidenceValue, evidenceValue, error) {
	if !evidence.hasSupply || !evidence.hasReturnTemp {
		return evidenceValue{}, evidenceValue{}, errors.New("low-delta-T evaluation requires authoritative supply and return history")
	}
	supply, err := temperatureEvidence(evidence.supply)
	if err != nil {
		return evidenceValue{}, evidenceValue{}, fmt.Errorf("supply temperature evidence: %w", err)
	}
	returnTemp, err := temperatureEvidence(evidence.returnTemp)
	if err != nil {
		return evidenceValue{}, evidenceValue{}, fmt.Errorf("return temperature evidence: %w", err)
	}
	return supply, returnTemp, nil
}

func temperatureEvidence(observation telemetryhistorymodel.DeviceHistoryObservation) (evidenceValue, error) {
	if observation.PointType != telemetryhistorymodel.PointTypeTelemetry || observation.Quality != telemetryhistorymodel.QualityGood {
		return evidenceValue{}, errors.New("latest authoritative observation is not good telemetry")
	}
	if observation.Unit == nil || *observation.Unit != "Cel" || observation.ValueType != telemetryhistorymodel.ValueTypeNumber {
		return evidenceValue{}, errors.New("latest authoritative observation is not a Celsius number")
	}
	var value float64
	if err := json.Unmarshal(observation.Value, &value); err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return evidenceValue{}, errors.New("latest authoritative observation has an invalid numeric value")
	}
	return evidenceValue{observationID: observation.ObservationID, value: value}, nil
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

func uuidv7(at time.Time) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	milliseconds := uint64(at.UTC().UnixMilli())
	bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5] = byte(milliseconds>>40), byte(milliseconds>>32), byte(milliseconds>>24), byte(milliseconds>>16), byte(milliseconds>>8), byte(milliseconds)
	bytes[6] = (bytes[6] & 0x0f) | 0x70
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	value := hex.EncodeToString(bytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", value[0:8], value[8:12], value[12:16], value[16:20], value[20:32]), nil
}
