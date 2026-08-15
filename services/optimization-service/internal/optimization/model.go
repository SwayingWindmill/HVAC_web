package optimization

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Resource struct {
	ResourceID            string  `json:"resourceId"`
	SOC                   float64 `json:"soc"`
	MinSOC                float64 `json:"minSoc"`
	MaxSOC                float64 `json:"maxSoc"`
	ChargePowerLimitKW    float64 `json:"chargePowerLimitKw"`
	DischargePowerLimitKW float64 `json:"dischargePowerLimitKw"`
	Availability          bool    `json:"availability"`
	ControlMode           string  `json:"controlMode"`
}

type Request struct {
	TenantID               string     `json:"tenantId"`
	SiteID                 string     `json:"siteId"`
	SubjectType            string     `json:"subjectType"`
	SubjectID              string     `json:"subjectId"`
	OptimizationRunID      string     `json:"optimizationRunId"`
	InputSnapshotID        string     `json:"inputSnapshotId"`
	InputChecksum          string     `json:"inputChecksum"`
	PolicyVersionID        string     `json:"policyVersionId"`
	TopologyVersionID      string     `json:"topologyVersionId"`
	LoadForecastSnapshotID string     `json:"loadForecastSnapshotId"`
	PVForecastSnapshotID   *string    `json:"pvForecastSnapshotId"`
	TariffVersionID        string     `json:"tariffVersionId"`
	Objective              string     `json:"objective"`
	Horizon                string     `json:"horizon"`
	HorizonMinutes         int        `json:"horizonMinutes"`
	Granularity            string     `json:"granularity"`
	DispatchMode           string     `json:"dispatchMode"`
	ValidFrom              time.Time  `json:"validFrom"`
	Resources              []Resource `json:"resources"`
}

type Interval struct {
	IntervalID       string         `json:"intervalId"`
	ResourceID       string         `json:"resourceId"`
	StartTime        time.Time      `json:"startTime"`
	EndTime          time.Time      `json:"endTime"`
	TargetType       string         `json:"targetType"`
	TargetValue      float64        `json:"targetValue"`
	Unit             string         `json:"unit"`
	ExpectedSOC      float64        `json:"expectedSoc"`
	ConstraintMargin map[string]any `json:"constraintMargin"`
	Ordinal          int            `json:"ordinal"`
}

type Evaluation struct {
	EvaluationID           string         `json:"evaluation_id"`
	TenantID               string         `json:"tenant_id"`
	SiteID                 string         `json:"site_id"`
	OptimizationRunID      string         `json:"optimization_run_id"`
	DispatchPlanID         string         `json:"dispatch_plan_id"`
	SubjectType            string         `json:"subject_type"`
	SubjectID              string         `json:"subject_id"`
	Objective              string         `json:"objective"`
	SolverOutcome          string         `json:"solver_outcome"`
	Quality                string         `json:"quality"`
	IntervalCount          uint32         `json:"interval_count"`
	InputSnapshotID        string         `json:"input_snapshot_id"`
	PolicyVersionID        string         `json:"policy_version_id"`
	TopologyVersionID      string         `json:"topology_version_id"`
	LoadForecastSnapshotID string         `json:"load_forecast_snapshot_id"`
	PVForecastSnapshotID   *string        `json:"pv_forecast_snapshot_id"`
	TariffVersionID        string         `json:"tariff_version_id"`
	EvaluationJSON         string         `json:"evaluation_json"`
	GeneratedAt            time.Time      `json:"generated_at"`
}

type Plan struct {
	PlanID                 string         `json:"planId"`
	OptimizationRunID      string         `json:"optimizationRunId"`
	InputSnapshotID        string         `json:"inputSnapshotId"`
	InputChecksum          string         `json:"inputChecksum"`
	PolicyVersionID        string         `json:"policyVersionId"`
	TopologyVersionID      string         `json:"topologyVersionId"`
	LoadForecastSnapshotID string         `json:"loadForecastSnapshotId"`
	PVForecastSnapshotID   *string        `json:"pvForecastSnapshotId"`
	TariffVersionID        string         `json:"tariffVersionId"`
	SubjectType            string         `json:"subjectType"`
	SubjectID              string         `json:"subjectId"`
	PlanVersion            uint64         `json:"planVersion"`
	Quality                string         `json:"quality"`
	Status                 string         `json:"status"`
	ValidFrom              time.Time      `json:"validFrom"`
	ValidTo                time.Time      `json:"validTo"`
	Objective              string         `json:"objective"`
	FallbackPolicy         string         `json:"fallbackPolicy"`
	Explanation            map[string]any `json:"explanation"`
	Intervals              []Interval     `json:"intervals"`
}

func (request Request) Validate() error {
	for field, value := range map[string]string{
		"tenantId":               request.TenantID,
		"siteId":                 request.SiteID,
		"subjectId":              request.SubjectID,
		"optimizationRunId":      request.OptimizationRunID,
		"inputSnapshotId":        request.InputSnapshotID,
		"policyVersionId":        request.PolicyVersionID,
		"topologyVersionId":      request.TopologyVersionID,
		"loadForecastSnapshotId": request.LoadForecastSnapshotID,
		"tariffVersionId":        request.TariffVersionID,
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
		return errors.New("Optimization P0 subject must be the exact SITE")
	}
	if request.Objective != "COST" && request.Objective != "DEMAND" && request.Objective != "CARBON" && request.Objective != "WEIGHTED" {
		return errors.New("objective is invalid")
	}
	if request.Horizon != "DAY_AHEAD" || request.HorizonMinutes != 1440 || request.Granularity != "15MIN" {
		return errors.New("Optimization P0 requires DAY_AHEAD 1440-minute horizon at 15MIN granularity")
	}
	if request.DispatchMode != "SHADOW" {
		return errors.New("NO_DISPATCH baseline is intentionally restricted to SHADOW mode")
	}
	if request.ValidFrom.IsZero() {
		return errors.New("validFrom is required")
	}
	if len(request.Resources) == 0 {
		return errors.New("at least one ESS resource is required")
	}
	seen := map[string]struct{}{}
	for _, resource := range request.Resources {
		if !uuidPattern.MatchString(resource.ResourceID) {
			return errors.New("resourceId must be a UUID")
		}
		if _, exists := seen[resource.ResourceID]; exists {
			return errors.New("duplicate ESS resourceId")
		}
		seen[resource.ResourceID] = struct{}{}
		if resource.MinSOC < 0 || resource.MaxSOC > 1 || resource.MinSOC >= resource.MaxSOC || resource.SOC < resource.MinSOC || resource.SOC > resource.MaxSOC {
			return errors.New("ESS SOC must remain within snapshotted min/max bounds")
		}
		if resource.ChargePowerLimitKW < 0 || resource.DischargePowerLimitKW < 0 {
			return errors.New("ESS charge/discharge limits must be non-negative")
		}
		if resource.ControlMode != "REMOTE" && resource.ControlMode != "LOCAL" && resource.ControlMode != "DISABLED" {
			return errors.New("ESS controlMode is invalid")
		}
	}
	return nil
}
