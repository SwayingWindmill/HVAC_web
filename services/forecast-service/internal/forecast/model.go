package forecast

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Observation struct {
	ObservedAt time.Time `json:"observedAt"`
	Value      float64   `json:"value"`
}

type Request struct {
	TenantID            string        `json:"tenantId"`
	SiteID              string        `json:"siteId"`
	SubjectType         string        `json:"subjectType"`
	SubjectID           string        `json:"subjectId"`
	Target              string        `json:"target"`
	ForecastJobID       string        `json:"forecastJobId"`
	ForecastSnapshotID  string        `json:"forecastSnapshotId"`
	DeploymentID        string        `json:"deploymentId"`
	ModelID             string        `json:"modelId"`
	ModelVersionID      string        `json:"modelVersionId"`
	ModelVersion        uint64        `json:"modelVersion"`
	FeatureSetVersionID string        `json:"featureSetVersionId"`
	FeatureSetVersion   uint64        `json:"featureSetVersion"`
	InputSnapshotID     string        `json:"inputSnapshotId"`
	TopologyVersionID   string        `json:"topologyVersionId"`
	ForecastOrigin      time.Time     `json:"forecastOrigin"`
	HorizonMinutes      int           `json:"horizonMinutes"`
	Granularity         string        `json:"granularity"`
	Observations        []Observation `json:"observations"`
	Unit                string        `json:"unit"`
}

type Point struct {
	ForecastID          string    `json:"forecast_id"`
	TenantID            string    `json:"tenant_id"`
	SiteID              string    `json:"site_id"`
	SubjectType         string    `json:"subject_type"`
	SubjectID           string    `json:"subject_id"`
	Target              string    `json:"target"`
	ForecastJobID       string    `json:"forecast_job_id"`
	ForecastSnapshotID  string    `json:"forecast_snapshot_id"`
	DeploymentID        string    `json:"deployment_id"`
	ModelID             string    `json:"model_id"`
	ModelVersionID      string    `json:"model_version_id"`
	ModelVersion        uint64    `json:"model_version"`
	FeatureSetVersionID string    `json:"feature_set_version_id"`
	FeatureSetVersion   uint64    `json:"feature_set_version"`
	InputSnapshotID     string    `json:"input_snapshot_id"`
	TopologyVersionID   string    `json:"topology_version_id"`
	ForecastOrigin      time.Time `json:"forecast_origin"`
	ForecastFor         time.Time `json:"forecast_for"`
	HorizonMinutes      uint32    `json:"horizon_minutes"`
	Value               float64   `json:"value"`
	Unit                string    `json:"unit"`
	LowerBound          *float64  `json:"lower_bound"`
	UpperBound          *float64  `json:"upper_bound"`
	Quantile            *float64  `json:"quantile"`
	Quality             string    `json:"quality"`
	GeneratedAt         time.Time `json:"generated_at"`
}

func (request Request) Validate() error {
	for field, value := range map[string]string{
		"tenantId": request.TenantID, "siteId": request.SiteID, "subjectId": request.SubjectID,
		"forecastJobId": request.ForecastJobID, "forecastSnapshotId": request.ForecastSnapshotID,
		"deploymentId": request.DeploymentID, "modelId": request.ModelID, "modelVersionId": request.ModelVersionID,
		"featureSetVersionId": request.FeatureSetVersionID, "inputSnapshotId": request.InputSnapshotID,
		"topologyVersionId": request.TopologyVersionID,
	} {
		if !uuidPattern.MatchString(value) {
			return fmt.Errorf("%s must be a UUID", field)
		}
	}
	if request.SubjectType != "SITE" && request.SubjectType != "ENERGY_NODE" {
		return errors.New("subjectType must be SITE or ENERGY_NODE")
	}
	if request.SubjectType == "SITE" && request.SubjectID != request.SiteID {
		return errors.New("SITE forecast subjectId must equal siteId")
	}
	if request.Target != "SITE_LOAD" && request.Target != "PV_GENERATION" {
		return errors.New("target must be SITE_LOAD or PV_GENERATION")
	}
	if request.ModelVersion == 0 || request.FeatureSetVersion == 0 {
		return errors.New("modelVersion and featureSetVersion must be positive")
	}
	if request.ForecastOrigin.IsZero() {
		return errors.New("forecastOrigin is required")
	}
	minutes, err := granularityMinutes(request.Granularity)
	if err != nil {
		return err
	}
	if request.HorizonMinutes <= 0 || request.HorizonMinutes%minutes != 0 {
		return errors.New("horizonMinutes must be positive and divisible by granularity")
	}
	if len(request.Observations) == 0 {
		return errors.New("forecast input observations are required; no-input forecasts are not fabricated")
	}
	previous := time.Time{}
	for index, observation := range request.Observations {
		if observation.ObservedAt.IsZero() || observation.ObservedAt.After(request.ForecastOrigin) {
			return fmt.Errorf("observation %d must be timestamped at or before forecastOrigin", index)
		}
		if !previous.IsZero() && !observation.ObservedAt.After(previous) {
			return fmt.Errorf("observation %d must be strictly later than the previous observation", index)
		}
		if math.IsNaN(observation.Value) || math.IsInf(observation.Value, 0) {
			return fmt.Errorf("observation %d value must be finite", index)
		}
		previous = observation.ObservedAt
	}
	if request.Unit == "" {
		return errors.New("unit is required")
	}
	return nil
}

func granularityMinutes(value string) (int, error) {
	switch value {
	case "15MIN":
		return 15, nil
	case "30MIN":
		return 30, nil
	case "1H":
		return 60, nil
	default:
		return 0, errors.New("granularity must be 15MIN, 30MIN or 1H")
	}
}
