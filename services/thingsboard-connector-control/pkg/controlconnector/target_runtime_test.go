package controlconnector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestLoadApprovedCohortAndResolveExactDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cohort.json")
	content := `{
  "schemaVersion": 1,
  "organizationId": "018f2e00-0000-7000-8000-000000000001",
  "siteId": "018f2e00-1000-7000-8000-000000000001",
  "deviceId": "018f2e00-3000-7000-8000-000000000001",
  "integrationId": "thingsboard-prod",
  "externalDeviceId": "tb-device-1",
  "bindingRevision": "binding:v1",
  "capability": "SET_TEMPERATURE_SETPOINT",
  "capabilityRevision": "capability:set-temperature-setpoint:v1",
  "mappingRevision": "thingsboard:setpoint:v1",
  "mappingStatus": "VERIFIED",
  "providerContract": "THINGSBOARD_CE_4.3.1.3",
  "providerMethod": "setTemperatureSetpoint",
  "reportedStateKey": "zone.temperature_setpoint",
  "timeoutMilliseconds": 7000,
  "credentialReference": "workload://eks/hvac-s3/s3-certification/thingsboard-connector-control"
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cohort, err := LoadApprovedCohort(path)
	if err != nil {
		t.Fatalf("load cohort: %v", err)
	}
	resolver, err := NewApprovedCohortTargetResolver(cohort)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolver.ResolveThingsBoardTarget(context.Background(), commandmodel.DispatchEnvelope{
		OrganizationID: cohort.OrganizationID, SiteID: cohort.SiteID, DeviceID: cohort.DeviceID,
		Capability: cohort.Capability, CapabilityRevision: cohort.CapabilityRevision,
		CommandID: "command-1", AttemptID: "attempt-1", PayloadHash: "hash", ExecutionFence: 1,
	})
	if err != nil || target.ExternalDeviceID != "tb-device-1" || target.BindingRevision != "binding:v1" {
		t.Fatalf("target=%#v err=%v", target, err)
	}
	mapping := cohort.Mapping()
	if mapping.Status != MappingProductionVerified || mapping.Timeout != 7*time.Second {
		t.Fatalf("mapping=%#v", mapping)
	}
}

func TestApprovedCohortRejectsWrongDeviceAndUnverifiedMapping(t *testing.T) {
	cohort := validApprovedCohort()
	resolver, err := NewApprovedCohortTargetResolver(cohort)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.ResolveThingsBoardTarget(context.Background(), commandmodel.DispatchEnvelope{
		OrganizationID: cohort.OrganizationID, SiteID: cohort.SiteID, DeviceID: "other-device",
		Capability: cohort.Capability, CapabilityRevision: cohort.CapabilityRevision,
		CommandID: "command-1", AttemptID: "attempt-1", PayloadHash: "hash", ExecutionFence: 1,
	})
	if err != ErrTargetUnavailable {
		t.Fatalf("expected target unavailable, got %v", err)
	}
	cohort.MappingStatus = "LOCAL_VERIFIED"
	if _, err := NewApprovedCohortTargetResolver(cohort); err == nil {
		t.Fatal("expected unverified mapping to fail closed")
	}
}

func TestFileCredentialProviderReadsOnlyApprovedIntegration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("credential-fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewFileCredentialProvider(FileCredentialConfig{
		Path: path, CredentialReference: "workload://eks/hvac-s3/s3-certification/thingsboard-connector-control", IntegrationID: "thingsboard-prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := provider.ProviderCredential(context.Background(), Target{IntegrationID: "thingsboard-prod", ExternalDeviceID: "tb-device-1", BindingRevision: "binding:v1"})
	if err != nil || credential != "credential-fixture" {
		t.Fatalf("credential length=%d err=%v", len(credential), err)
	}
	if provider.CredentialReference() != "workload://eks/hvac-s3/s3-certification/thingsboard-connector-control" {
		t.Fatalf("reference=%q", provider.CredentialReference())
	}
	if _, err := provider.ProviderCredential(context.Background(), Target{IntegrationID: "other"}); err != ErrTargetUnavailable {
		t.Fatalf("expected target unavailable, got %v", err)
	}
}

func validApprovedCohort() ApprovedCohort {
	return ApprovedCohort{
		SchemaVersion:         1,
		OrganizationID:        "018f2e00-0000-7000-8000-000000000001",
		SiteID:                "018f2e00-1000-7000-8000-000000000001",
		DeviceID:              "018f2e00-3000-7000-8000-000000000001",
		IntegrationID:         "thingsboard-prod",
		ExternalDeviceID:      "tb-device-1",
		BindingRevision:       "binding:v1",
		Capability:            commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision:    "capability:set-temperature-setpoint:v1",
		MappingRevision:       "thingsboard:setpoint:v1",
		MappingStatus:         "VERIFIED",
		ProviderContract:      "THINGSBOARD_CE_4.3.1.3",
		ProviderMethod:        "setTemperatureSetpoint",
		ReportedStateKey:    "zone.temperature_setpoint",
		TimeoutMilliseconds: 7000,
		CredentialReference: "workload://eks/hvac-s3/s3-certification/thingsboard-connector-control",
	}
}
