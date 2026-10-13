package fdd

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// LowDeltaTRuleCode is the chilled-water low-delta-T rule, the one rule FDD evaluates.
const LowDeltaTRuleCode = "CHILLED_WATER_LOW_DELTA_T"

// ErrRuleNotFound means the Tenant has no revision of the rule in effect.
var ErrRuleNotFound = errors.New("no FDD rule revision is in effect")

// RuleRevision is one immutable revision of a Tenant's rule: the delta-T below which the loop is
// diagnosed, over the window of minutes before the evaluation.
type RuleRevision struct {
	ID             string    `json:"revisionId"`
	Revision       int       `json:"revision"`
	MinimumDeltaTC float64   `json:"minimumDeltaTC"`
	WindowMinutes  int       `json:"windowMinutes"`
	EffectiveFrom  time.Time `json:"effectiveFrom"`
}

// EvaluationRequest names what to diagnose and when; the rule in effect then says how.
type EvaluationRequest struct {
	TenantID string
	SiteID   string
	AssetID  string
	DeviceID string
	At       time.Time
}

type EvaluationResult struct {
	Status  string                        `json:"status"`
	DeltaTC float64                       `json:"deltaTC"`
	Rule    RuleRevision                  `json:"rule"`
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
