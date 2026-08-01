package identitycontext

import (
	"errors"
	"fmt"
	"strings"
)

const CapabilitySetVersion = 5

type Capability string

const (
	CapabilityOrganizationList      Capability = "organization.list"
	CapabilityOrganizationRead      Capability = "organization.read"
	CapabilitySiteList              Capability = "site.list"
	CapabilitySiteRead              Capability = "site.read"
	CapabilityEquipmentList         Capability = "equipment.list"
	CapabilityEquipmentRead         Capability = "equipment.read"
	CapabilityDeviceList            Capability = "device.list"
	CapabilityDeviceRead            Capability = "device.read"
	CapabilityTelemetrySnapshotRead Capability = "telemetry.snapshot.read"
	CapabilityTelemetryBatchRead    Capability = "telemetry.batch.read"
	CapabilityTelemetrySubscribe    Capability = "telemetry.subscribe"
	CapabilityTelemetryHistoryRead  Capability = "telemetry.history.read"
	CapabilityAlarmList             Capability = "alarm.list"
	CapabilityAlarmRead             Capability = "alarm.read"
	CapabilityWorkOrderList         Capability = "work-order.list"
	CapabilityWorkOrderRead         Capability = "work-order.read"
	CapabilityWorkOrderCreate       Capability = "work-order.create"
	CapabilityWorkOrderAssign       Capability = "work-order.assign"
)

var supportedCapabilities = [...]Capability{
	CapabilityOrganizationList,
	CapabilityOrganizationRead,
	CapabilitySiteList,
	CapabilitySiteRead,
	CapabilityEquipmentList,
	CapabilityEquipmentRead,
	CapabilityDeviceList,
	CapabilityDeviceRead,
	CapabilityTelemetrySnapshotRead,
	CapabilityTelemetryBatchRead,
	CapabilityTelemetrySubscribe,
	CapabilityTelemetryHistoryRead,
	CapabilityAlarmList,
	CapabilityAlarmRead,
	CapabilityWorkOrderList,
	CapabilityWorkOrderRead,
	CapabilityWorkOrderCreate,
	CapabilityWorkOrderAssign,
}

func SupportedCapabilities() []Capability {
	return append([]Capability(nil), supportedCapabilities[:]...)
}

func (capability Capability) Valid() bool {
	switch capability {
	case CapabilityOrganizationList,
		CapabilityOrganizationRead,
		CapabilitySiteList,
		CapabilitySiteRead,
		CapabilityEquipmentList,
		CapabilityEquipmentRead,
		CapabilityDeviceList,
		CapabilityDeviceRead,
		CapabilityTelemetrySnapshotRead,
		CapabilityTelemetryBatchRead,
		CapabilityTelemetrySubscribe,
		CapabilityTelemetryHistoryRead,
		CapabilityAlarmList,
		CapabilityAlarmRead,
		CapabilityWorkOrderList,
		CapabilityWorkOrderRead,
		CapabilityWorkOrderCreate,
		CapabilityWorkOrderAssign:
		return true
	default:
		return false
	}
}

type EffectiveAuthorization struct {
	CapabilitySetVersion int          `json:"capabilitySetVersion"`
	PolicyRevision       string       `json:"policyRevision"`
	Capabilities         []Capability `json:"capabilities"`
}

func (authorization EffectiveAuthorization) Validate() error {
	if authorization.CapabilitySetVersion != CapabilitySetVersion {
		return fmt.Errorf("capability set version %d is unsupported", authorization.CapabilitySetVersion)
	}
	if strings.TrimSpace(authorization.PolicyRevision) == "" {
		return errors.New("policy revision is required")
	}
	if len(authorization.PolicyRevision) > 128 {
		return errors.New("policy revision exceeds 128 characters")
	}
	if authorization.Capabilities == nil {
		return errors.New("capabilities must be an explicit array")
	}
	if len(authorization.Capabilities) > len(supportedCapabilities) {
		return errors.New("capability set exceeds the supported vocabulary")
	}
	seen := make(map[Capability]struct{}, len(authorization.Capabilities))
	for _, capability := range authorization.Capabilities {
		if !capability.Valid() {
			return fmt.Errorf("capability %q is unsupported", capability)
		}
		if _, duplicate := seen[capability]; duplicate {
			return fmt.Errorf("capability %q is duplicated", capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}
