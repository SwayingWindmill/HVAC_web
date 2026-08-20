package optimization

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type HVACBaseline struct {
	DailyEnergyKWh float64 `json:"dailyEnergyKWh"`
	DailyCost      float64 `json:"dailyCost"`
	SupplyTempC    float64 `json:"supplyTempC"`
	ZoneTempC      float64 `json:"zoneTempC"`
}

type HVACConstraints struct {
	SupplyTempMinC    float64 `json:"supplyTempMinC"`
	SupplyTempMaxC    float64 `json:"supplyTempMaxC"`
	ZoneTempMinC      float64 `json:"zoneTempMinC"`
	ZoneTempMaxC      float64 `json:"zoneTempMaxC"`
	MaxSupplyTempStep float64 `json:"maxSupplyTempStepC"`
}

type HVACResponseModel struct {
	DailyEnergyDeltaPerSupplyTempC float64 `json:"dailyEnergyDeltaKWhPerSupplyTempC"`
	ZoneTempDeltaPerSupplyTempC    float64 `json:"zoneTempDeltaCPerSupplyTempC"`
	EnergyUncertaintyP90KWh        float64 `json:"energyUncertaintyP90KWh"`
	ZoneTempUncertaintyP90C        float64 `json:"zoneTempUncertaintyP90C"`
}

type Request struct {
	TenantID               string            `json:"tenantId"`
	SiteID                 string            `json:"siteId"`
	SubjectType            string            `json:"subjectType"`
	SubjectID              string            `json:"subjectId"`
	OptimizationRunID      string            `json:"optimizationRunId"`
	InputSnapshotID        string            `json:"inputSnapshotId"`
	InputChecksum          string            `json:"inputChecksum"`
	PolicyVersionID        string            `json:"policyVersionId"`
	TopologyVersionID      string            `json:"topologyVersionId"`
	LoadForecastSnapshotID string            `json:"loadForecastSnapshotId"`
	PVForecastSnapshotID   *string           `json:"pvForecastSnapshotId"`
	TariffVersionID        string            `json:"tariffVersionId"`
	DeploymentRevisionID   string            `json:"deploymentRevisionId"`
	Objective              string            `json:"objective"`
	Horizon                string            `json:"horizon"`
	HorizonMinutes         int               `json:"horizonMinutes"`
	Granularity            string            `json:"granularity"`
	ValidFrom              time.Time         `json:"validFrom"`
	Baseline               HVACBaseline      `json:"baseline"`
	Constraints            HVACConstraints   `json:"constraints"`
	ResponseModel          HVACResponseModel `json:"responseModel"`
}

type Recommendation = intelligencemodel.OptimizationRecommendation

type Evaluation struct {
	EvaluationID           string    `json:"evaluation_id"`
	TenantID               string    `json:"tenant_id"`
	SiteID                 string    `json:"site_id"`
	OptimizationRunID      string    `json:"optimization_run_id"`
	RecommendationID       string    `json:"recommendation_id"`
	SubjectType            string    `json:"subject_type"`
	SubjectID              string    `json:"subject_id"`
	Objective              string    `json:"objective"`
	SolverOutcome          string    `json:"solver_outcome"`
	Quality                string    `json:"quality"`
	ConstraintCount        uint32    `json:"constraint_count"`
	InputSnapshotID        string    `json:"input_snapshot_id"`
	PolicyVersionID        string    `json:"policy_version_id"`
	TopologyVersionID      string    `json:"topology_version_id"`
	LoadForecastSnapshotID string    `json:"load_forecast_snapshot_id"`
	PVForecastSnapshotID   *string   `json:"pv_forecast_snapshot_id"`
	TariffVersionID        string    `json:"tariff_version_id"`
	EvaluationJSON         string    `json:"evaluation_json"`
	GeneratedAt            time.Time `json:"generated_at"`
}

func (request Request) Validate() error {
	for field, value := range map[string]string{
		"tenantId": request.TenantID, "siteId": request.SiteID, "subjectId": request.SubjectID,
		"optimizationRunId": request.OptimizationRunID, "inputSnapshotId": request.InputSnapshotID,
		"policyVersionId": request.PolicyVersionID, "topologyVersionId": request.TopologyVersionID,
		"loadForecastSnapshotId": request.LoadForecastSnapshotID, "tariffVersionId": request.TariffVersionID,
		"deploymentRevisionId": request.DeploymentRevisionID,
	} {
		if !uuidPattern.MatchString(value) {
			return fmt.Errorf("%s must be a UUID", field)
		}
	}
	if request.PVForecastSnapshotID != nil && !uuidPattern.MatchString(*request.PVForecastSnapshotID) {
		return errors.New("pvForecastSnapshotId must be a UUID when provided")
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(request.InputChecksum) {
		return errors.New("inputChecksum must be lowercase SHA-256 hex")
	}
	if request.SubjectType != "SITE" || request.SubjectID != request.SiteID {
		return errors.New("optimization subject must be the exact SITE")
	}
	if request.Objective != "COST" {
		return errors.New("current HVAC recommendation solver supports the COST objective")
	}
	if request.Horizon != "DAY_AHEAD" || request.HorizonMinutes != 1440 || request.Granularity != "15MIN" {
		return errors.New("HVAC recommendation requires DAY_AHEAD 1440-minute horizon at 15MIN granularity")
	}
	if request.ValidFrom.IsZero() {
		return errors.New("validFrom is required")
	}
	if !finitePositive(request.Baseline.DailyEnergyKWh) || !finiteNonNegative(request.Baseline.DailyCost) || !finite(request.Baseline.SupplyTempC) || !finite(request.Baseline.ZoneTempC) {
		return errors.New("HVAC baseline values are invalid")
	}
	constraints := request.Constraints
	if !finite(constraints.SupplyTempMinC) || !finite(constraints.SupplyTempMaxC) || constraints.SupplyTempMinC >= constraints.SupplyTempMaxC ||
		!finite(constraints.ZoneTempMinC) || !finite(constraints.ZoneTempMaxC) || constraints.ZoneTempMinC >= constraints.ZoneTempMaxC || !finitePositive(constraints.MaxSupplyTempStep) {
		return errors.New("HVAC comfort/safety constraints are invalid")
	}
	if request.Baseline.SupplyTempC < constraints.SupplyTempMinC || request.Baseline.SupplyTempC > constraints.SupplyTempMaxC ||
		request.Baseline.ZoneTempC < constraints.ZoneTempMinC || request.Baseline.ZoneTempC > constraints.ZoneTempMaxC {
		return errors.New("HVAC baseline is already outside the frozen constraints")
	}
	model := request.ResponseModel
	if !finite(model.DailyEnergyDeltaPerSupplyTempC) || !finite(model.ZoneTempDeltaPerSupplyTempC) || !finiteNonNegative(model.EnergyUncertaintyP90KWh) || !finiteNonNegative(model.ZoneTempUncertaintyP90C) {
		return errors.New("HVAC response model coefficients or uncertainty are invalid")
	}
	return nil
}

func finite(value float64) bool            { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func finitePositive(value float64) bool    { return finite(value) && value > 0 }
func finiteNonNegative(value float64) bool { return finite(value) && value >= 0 }
