package fdd

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type EvidenceValue struct {
	EvidenceID string    `json:"evidenceId"`
	Signal     string    `json:"signal"`
	ObservedAt time.Time `json:"observedAt"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`
}

type EvaluationRequest struct {
	TenantID                  string          `json:"tenantId"`
	SiteID                    string          `json:"siteId"`
	AssetID                   string          `json:"assetId"`
	EvaluationFrom            time.Time       `json:"evaluationFrom"`
	EvaluationTo              time.Time       `json:"evaluationTo"`
	RuleRevisionID            string          `json:"ruleRevisionId"`
	ModelDeploymentRevisionID string          `json:"modelDeploymentRevisionId,omitempty"`
	MinimumDeltaTC            float64         `json:"minimumDeltaTC"`
	Evidence                  []EvidenceValue `json:"evidence"`
}

type EvaluationResult struct {
	Status  string                        `json:"status"`
	DeltaTC float64                       `json:"deltaTC"`
	Finding *intelligencemodel.FDDFinding `json:"finding,omitempty"`
}

func (request EvaluationRequest) Validate() error {
	for name, value := range map[string]string{"tenantId": request.TenantID, "siteId": request.SiteID, "assetId": request.AssetID} {
		if !uuidPattern.MatchString(value) {
			return fmt.Errorf("%s must be a UUID", name)
		}
	}
	if request.ModelDeploymentRevisionID != "" && !uuidPattern.MatchString(request.ModelDeploymentRevisionID) {
		return errors.New("modelDeploymentRevisionId must be a UUID when provided")
	}
	if request.RuleRevisionID == "" {
		return errors.New("ruleRevisionId is required")
	}
	if request.EvaluationFrom.IsZero() || request.EvaluationTo.IsZero() || !request.EvaluationTo.After(request.EvaluationFrom) {
		return errors.New("evaluation window is invalid")
	}
	if math.IsNaN(request.MinimumDeltaTC) || math.IsInf(request.MinimumDeltaTC, 0) || request.MinimumDeltaTC <= 0 {
		return errors.New("minimumDeltaTC must be positive and finite")
	}
	if len(request.Evidence) < 2 {
		return errors.New("supply and return temperature evidence are required")
	}
	seen := map[string]bool{}
	for index, evidence := range request.Evidence {
		if evidence.EvidenceID == "" || evidence.Signal == "" || evidence.ObservedAt.Before(request.EvaluationFrom) || evidence.ObservedAt.After(request.EvaluationTo) || math.IsNaN(evidence.Value) || math.IsInf(evidence.Value, 0) {
			return fmt.Errorf("evidence %d is invalid or outside the evaluation window", index)
		}
		if evidence.Unit != "Cel" {
			return fmt.Errorf("evidence %d must use Cel", index)
		}
		seen[evidence.Signal] = true
	}
	if !seen["chilled_water_supply_temperature"] || !seen["chilled_water_return_temperature"] {
		return errors.New("supply and return chilled-water temperature evidence are required")
	}
	return nil
}
