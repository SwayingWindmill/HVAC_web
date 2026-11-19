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

const (
	// LowDeltaTRuleCode is the chilled-water low-delta-T rule.
	LowDeltaTRuleCode = "CHILLED_WATER_LOW_DELTA_T"
	// MeterImbalanceRuleCode is the hourly site main-meter vs sub-meter imbalance rule.
	MeterImbalanceRuleCode     = "METER_IMBALANCE"
	MeterBalanceRuleRevisionID = "meter-balance-v1"

	defaultMaxImbalanceRatio     = 0.05
	defaultMinMainElectricityKWh = 10.0
	defaultMinConsecutiveHours   = 2
)

// ErrRuleNotFound means the Tenant has no revision of the rule in effect.
var ErrRuleNotFound = errors.New("no FDD rule revision is in effect")

// RuleRevision is one immutable revision of a Tenant's rule: the delta-T below which the loop is
// diagnosed, over the window of minutes before the evaluation, and the operating gates that say
// which of those minutes count.
type RuleRevision struct {
	ID             string         `json:"revisionId"`
	Revision       int            `json:"revision"`
	MinimumDeltaTC float64        `json:"minimumDeltaTC"`
	WindowMinutes  int            `json:"windowMinutes"`
	OperatingGates OperatingGates `json:"operatingGates"`
	EffectiveFrom  time.Time      `json:"effectiveFrom"`
}

// EvaluationRequest names what to diagnose and when; the rule in effect then says how.
type EvaluationRequest struct {
	TenantID string
	SiteID   string
	AssetID  string
	DeviceID string
	At       time.Time
}

// EvaluationResult is CLEAR, NOT_OPERATING (too few operating minutes to judge), FINDING or
// ONGOING; DeltaTC is the mean over the operating minutes.
type EvaluationResult struct {
	Status  string                        `json:"status"`
	DeltaTC float64                       `json:"deltaTC"`
	Rule    RuleRevision                  `json:"rule"`
	Window  WindowAssessment              `json:"window"`
	Finding *intelligencemodel.FDDFinding `json:"finding,omitempty"`
}

func (request EvaluationRequest) Validate() error {
	for name, value := range map[string]string{
		"tenantId": request.TenantID,
		"siteId":   request.SiteID,
		"assetId":  request.AssetID,
		"deviceId": request.DeviceID,
	} {
		if !uuidPattern.MatchString(value) {
			return fmt.Errorf("%s must be a UUID", name)
		}
	}
	if request.At.IsZero() {
		return errors.New("evaluation time is required")
	}
	return nil
}

// MeterBalanceHour holds one UTC hour from analytics.site_meter_balance_hourly.
type MeterBalanceHour struct {
	Hour            time.Time `json:"hour"`
	SiteKWh         float64   `json:"siteKWh"`
	SubmeterSumKWh  float64   `json:"submeterSumKWh"`
	DiffKWh         float64   `json:"diffKWh"`
	ImbalanceRatio  *float64  `json:"imbalanceRatio"`
	SubmeterCount   uint64    `json:"submeterCount"`
	ImbalanceStatus string    `json:"imbalanceStatus"`
}

// MeterImbalanceRequest names a Site window and thresholds for meter-imbalance diagnostics.
type MeterImbalanceRequest struct {
	TenantID              string    `json:"tenantId"`
	SiteID                string    `json:"siteId"`
	From                  time.Time `json:"from"`
	To                    time.Time `json:"to"`
	MaxImbalanceRatio     float64   `json:"maxImbalanceRatio,omitempty"`
	MinMainElectricityKWh float64   `json:"minMainElectricityKWh,omitempty"`
	MinConsecutiveHours   int       `json:"minConsecutiveHours,omitempty"`
}

// MeterImbalanceResult summarizes sustained main-vs-submeter discrepancies over the evaluated window.
type MeterImbalanceResult struct {
	Status          string                        `json:"status"`
	ImbalanceStatus string                        `json:"imbalanceStatus,omitempty"`
	WorstRatio      float64                       `json:"worstRatio"`
	ObservedGapKWh  float64                       `json:"observedGapKWh"`
	ThresholdRatio  float64                       `json:"thresholdRatio"`
	AffectedHours   int                           `json:"affectedHours"`
	Finding         *intelligencemodel.FDDFinding `json:"finding,omitempty"`
}

func (request MeterImbalanceRequest) Validate() error {
	if !uuidPattern.MatchString(request.TenantID) {
		return errors.New("tenantId must be a UUID")
	}
	if !uuidPattern.MatchString(request.SiteID) {
		return errors.New("siteId must be a UUID")
	}
	if request.From.IsZero() || request.To.IsZero() || !request.To.After(request.From) {
		return errors.New("meter imbalance evaluation window is invalid")
	}
	if math.IsNaN(request.MaxImbalanceRatio) || math.IsInf(request.MaxImbalanceRatio, 0) || request.MaxImbalanceRatio < 0 {
		return errors.New("maxImbalanceRatio must be non-negative")
	}
	if math.IsNaN(request.MinMainElectricityKWh) || math.IsInf(request.MinMainElectricityKWh, 0) || request.MinMainElectricityKWh < 0 {
		return errors.New("minMainElectricityKWh must be non-negative")
	}
	if request.MinConsecutiveHours < 0 {
		return errors.New("minConsecutiveHours must be non-negative")
	}
	return nil
}

func (request MeterImbalanceRequest) normalized() (maxRatio float64, minMainKWh float64, minConsecutive int) {
	maxRatio = request.MaxImbalanceRatio
	if maxRatio == 0 {
		maxRatio = defaultMaxImbalanceRatio
	}
	minMainKWh = request.MinMainElectricityKWh
	if minMainKWh == 0 {
		minMainKWh = defaultMinMainElectricityKWh
	}
	minConsecutive = request.MinConsecutiveHours
	if minConsecutive == 0 {
		minConsecutive = defaultMinConsecutiveHours
	}
	return maxRatio, minMainKWh, minConsecutive
}
