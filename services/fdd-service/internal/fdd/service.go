package fdd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

type Store interface {
	InsertFinding(context.Context, intelligencemodel.FDDFinding) error
	ListFindings(context.Context, string, string, int) ([]intelligencemodel.FDDFinding, error)
	LinkFinding(context.Context, string, string, string, string, string, time.Time) (intelligencemodel.FDDFinding, error)
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store, now func() time.Time) (*Service, error) {
	if store == nil {
		return nil, errors.New("FDD store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}, nil
}

func (service *Service) EvaluateLowDeltaT(ctx context.Context, request EvaluationRequest) (EvaluationResult, error) {
	if err := request.Validate(); err != nil {
		return EvaluationResult{}, err
	}
	var supply, returnTemp *EvidenceValue
	for index := range request.Evidence {
		evidence := &request.Evidence[index]
		switch evidence.Signal {
		case "chilled_water_supply_temperature":
			if supply == nil || evidence.ObservedAt.After(supply.ObservedAt) {
				supply = evidence
			}
		case "chilled_water_return_temperature":
			if returnTemp == nil || evidence.ObservedAt.After(returnTemp.ObservedAt) {
				returnTemp = evidence
			}
		}
	}
	if supply == nil || returnTemp == nil {
		return EvaluationResult{}, errors.New("low-delta-T evaluation requires supply and return evidence")
	}
	deltaT := returnTemp.Value - supply.Value
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
		EvidenceIDs: []string{supply.EvidenceID, returnTemp.EvidenceID}, ModelDeploymentID: request.ModelDeploymentRevisionID,
		RuleRevisionID: request.RuleRevisionID, Confidence: confidence, CreatedAt: service.now().UTC(),
	}
	if err = service.store.InsertFinding(ctx, finding); err != nil {
		return EvaluationResult{}, fmt.Errorf("persist FDD finding: %w", err)
	}
	return EvaluationResult{Status: "FINDING", DeltaTC: deltaT, Finding: &finding}, nil
}

func (service *Service) ListFindings(ctx context.Context, tenantID, siteID string, limit int) ([]intelligencemodel.FDDFinding, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || limit <= 0 || limit > 200 {
		return nil, errors.New("FDD list scope or limit is invalid")
	}
	return service.store.ListFindings(ctx, tenantID, siteID, limit)
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
