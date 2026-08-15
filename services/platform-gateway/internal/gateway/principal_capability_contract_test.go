package gateway_test

import (
	"testing"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

func TestIAMCapabilityVocabularyMatchesPublicGoContract(t *testing.T) {
	if platformapi.CapabilitySetVersion != identitycontext.CapabilitySetVersion {
		t.Fatalf("capability set version = %d; IAM version = %d", platformapi.CapabilitySetVersion, identitycontext.CapabilitySetVersion)
	}

	publicCapabilities := []platformapi.Capability{
		platformapi.CapabilitySiteList,
		platformapi.CapabilitySiteRead,
		platformapi.CapabilityEquipmentList,
		platformapi.CapabilityEquipmentRead,
		platformapi.CapabilityDeviceList,
		platformapi.CapabilityDeviceRead,
		platformapi.CapabilityTelemetrySnapshotRead,
		platformapi.CapabilityTelemetryBatchRead,
		platformapi.CapabilityTelemetrySubscribe,
		platformapi.CapabilityTelemetryHistoryRead,
		platformapi.CapabilityAlarmList,
		platformapi.CapabilityAlarmRead,
		platformapi.CapabilityWorkOrderList,
		platformapi.CapabilityWorkOrderRead,
		platformapi.CapabilityWorkOrderCreate,
		platformapi.CapabilityWorkOrderAssign,
		platformapi.CapabilityWorkOrderLifecycle,
	}
	internalCapabilities := identitycontext.SupportedCapabilities()
	if len(publicCapabilities) != len(internalCapabilities) {
		t.Fatalf("public capabilities = %#v; IAM capabilities = %#v", publicCapabilities, internalCapabilities)
	}
	for index := range internalCapabilities {
		if string(publicCapabilities[index]) != string(internalCapabilities[index]) {
			t.Fatalf("public capabilities = %#v; IAM capabilities = %#v", publicCapabilities, internalCapabilities)
		}
	}
}
