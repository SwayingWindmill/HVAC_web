package iam

import (
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

// catalogCapabilityValid reports whether a Role may carry the capability. A stored capability
// outside this set makes authorization unavailable rather than being ignored.
func catalogCapabilityValid(value string) bool {
	if identitycontext.Capability(value).Valid() || registryauth.Action(value).Valid() || telemetryauth.Action(value).Valid() ||
		value == analyticsmodel.EnergySeriesAction || commandCapabilityValid(value) {
		return true
	}
	switch alarmmodel.Action(value) {
	case alarmmodel.ActionRead,
		alarmmodel.ActionAck,
		alarmmodel.ActionAssign,
		alarmmodel.ActionUnassign,
		alarmmodel.ActionSuppress,
		alarmmodel.ActionUnsuppress:
		return true
	}
	switch workordermodel.Action(value) {
	case workordermodel.ActionList,
		workordermodel.ActionRead,
		workordermodel.ActionCreate,
		workordermodel.ActionAssign,
		workordermodel.ActionPlan,
		workordermodel.ActionStart,
		workordermodel.ActionBlock,
		workordermodel.ActionResume,
		workordermodel.ActionComplete,
		workordermodel.ActionCancel,
		workordermodel.ActionReopen:
		return true
	}
	return false
}
