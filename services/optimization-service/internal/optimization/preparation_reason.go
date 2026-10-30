package optimization

import (
	"errors"
	"fmt"
)

// PreparationReason names the one input that kept a run from being prepared, so an operator can
// see what to fix instead of an empty recommendation.
type PreparationReason string

const (
	ReasonSiteNotVisible         PreparationReason = "SITE_NOT_VISIBLE"
	ReasonPolicyMissing          PreparationReason = "POLICY_MISSING"
	ReasonPolicyUnsupported      PreparationReason = "POLICY_UNSUPPORTED"
	ReasonMaintenanceActive      PreparationReason = "MAINTENANCE_ACTIVE"
	ReasonTopologyMissing        PreparationReason = "TOPOLOGY_MISSING"
	ReasonLoadForecastMissing    PreparationReason = "LOAD_FORECAST_MISSING"
	ReasonTariffMissing          PreparationReason = "TARIFF_MISSING"
	ReasonModelDeploymentMissing PreparationReason = "MODEL_DEPLOYMENT_MISSING"
	ReasonPricedDayMissing       PreparationReason = "PRICED_DAY_MISSING"
	ReasonSupplyTempMissing      PreparationReason = "SUPPLY_TEMPERATURE_MISSING"
	ReasonZoneTempMissing        PreparationReason = "ZONE_TEMPERATURE_MISSING"
	ReasonBaselineOutsideLimits  PreparationReason = "BASELINE_OUTSIDE_LIMITS"
	ReasonInputsInvalid          PreparationReason = "INPUTS_INVALID"
	// ReasonInputsUnreadable is an input owner that could not be asked, not an input that is missing.
	ReasonInputsUnreadable PreparationReason = "INPUTS_UNREADABLE"
)

var preparationReasons = map[PreparationReason]struct{}{
	ReasonSiteNotVisible: {}, ReasonPolicyMissing: {}, ReasonPolicyUnsupported: {}, ReasonMaintenanceActive: {},
	ReasonTopologyMissing: {}, ReasonLoadForecastMissing: {}, ReasonTariffMissing: {}, ReasonModelDeploymentMissing: {},
	ReasonPricedDayMissing: {}, ReasonSupplyTempMissing: {}, ReasonZoneTempMissing: {}, ReasonBaselineOutsideLimits: {},
	ReasonInputsInvalid: {}, ReasonInputsUnreadable: {},
}

// KnownPreparationReason reports whether a stored job error code is one of the reasons above.
func KnownPreparationReason(code string) bool {
	_, ok := preparationReasons[PreparationReason(code)]
	return ok
}

// PreparationUnavailableError is ErrPreparationUnavailable with the reason it happened.
type PreparationUnavailableError struct {
	Reason PreparationReason
	Detail string
}

func (err *PreparationUnavailableError) Error() string {
	return fmt.Sprintf("%s: %s: %s", ErrPreparationUnavailable, err.Reason, err.Detail)
}

func (err *PreparationUnavailableError) Is(target error) bool {
	return target == ErrPreparationUnavailable
}

func unavailable(reason PreparationReason, format string, args ...any) error {
	return &PreparationUnavailableError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// PreparationReasonOf is the reason a preparation was unavailable, or false when it failed otherwise.
func PreparationReasonOf(err error) (PreparationReason, bool) {
	var typed *PreparationUnavailableError
	if errors.As(err, &typed) {
		return typed.Reason, true
	}
	return "", false
}

// PreparationFailedCode is the job error code of a firing that failed for any other reason.
const PreparationFailedCode = "PREPARATION_FAILED"

// PreparationFailureCode is the error code a schedule firing that failed to prepare records: the
// missing input's reason, so the latest preparation can say what to fix.
func PreparationFailureCode(err error) string {
	if reason, ok := PreparationReasonOf(err); ok {
		return string(reason)
	}
	return PreparationFailedCode
}
