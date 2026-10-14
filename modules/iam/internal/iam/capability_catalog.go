package iam

import (
	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

// catalogCapabilityValid reports whether a Role may carry the capability. A stored capability
// outside this set makes authorization unavailable rather than being ignored.
func catalogCapabilityValid(value string) bool {
	if identitycontext.Capability(value).Valid() || registryauth.Action(value).Valid() || telemetryauth.Action(value).Valid() ||
		value == analyticsmodel.EnergySeriesAction || commandCapabilityValid(value) {
		return true
	}
	switch alarmauth.Action(value) {
	case alarmauth.ActionRead, alarmauth.ActionAck, alarmauth.ActionAssign:
		return true
	}
	switch workorderauth.Action(value) {
	case workorderauth.ActionList,
		workorderauth.ActionRead,
		workorderauth.ActionCreate,
		workorderauth.ActionAssign,
		workorderauth.ActionPlan,
		workorderauth.ActionStart,
		workorderauth.ActionBlock,
		workorderauth.ActionResume,
		workorderauth.ActionComplete,
		workorderauth.ActionCancel,
		workorderauth.ActionReopen:
		return true
	}
	return false
}
