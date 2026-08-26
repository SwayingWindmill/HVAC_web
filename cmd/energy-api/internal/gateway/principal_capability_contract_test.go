package gateway_test

import (
	"testing"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
)

func TestIAMCapabilityVocabularyMatchesPublicGoContract(t *testing.T) {
	if platformapi.CapabilitySetVersion != identitycontext.CapabilitySetVersion {
		t.Fatalf("capability set version = %d; IAM version = %d", platformapi.CapabilitySetVersion, identitycontext.CapabilitySetVersion)
	}

	publicCapabilities := []platformapi.Capability{
		platformapi.CapabilitySiteList,
		platformapi.CapabilitySiteRead,
		platformapi.CapabilityAssetList,
		platformapi.CapabilityAssetRead,
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
		platformapi.CapabilitySessionRevoke,
		platformapi.CapabilityAuditRead,
		platformapi.CapabilityIAMAdmin,
		platformapi.CapabilityAPICredentialManage,
		platformapi.CapabilitySiteWrite,
		platformapi.CapabilitySpaceWrite,
		platformapi.CapabilityAssetWrite,
		platformapi.CapabilityDeviceWrite,
		platformapi.CapabilitySensorWrite,
		platformapi.CapabilityPointWrite,
		platformapi.CapabilityBindingWrite,
		platformapi.CapabilityTemplateManage,
		platformapi.CapabilityRegistryImport,
		platformapi.CapabilityRegistryRetire,
		platformapi.CapabilityRuleManage,
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
