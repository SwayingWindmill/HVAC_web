package iam

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

const (
	telemetryTestIssuer        = "https://identity.example.test/oidc"
	telemetryTestSubject       = "delegated"
	telemetryTestPrincipalID   = "018f1e00-2000-7000-8000-000000000002"
	telemetryTestActingOrgID   = "018f1e00-0000-7000-8000-000000000003"
	telemetryTestOwningOrgID   = "018f1e00-0000-7000-8000-000000000001"
	telemetryTestOtherOrgID    = "018f1e00-0000-7000-8000-000000000002"
	telemetryTestSiteID        = "018f1e00-1000-7000-8000-000000000001"
	telemetryTestSiblingSite   = "018f1e00-1000-7000-8000-000000000002"
	telemetryTestOtherSite     = "018f1e00-1000-7000-8000-000000000003"
	telemetryTestDeviceID      = "018f1e00-4000-7000-8000-000000000001"
	telemetryTestSiblingDevice = "018f1e00-4000-7000-8000-000000000002"
	telemetryTestOtherDevice   = "018f1e00-4000-7000-8000-000000000003"
)

func TestS2FixtureTelemetryAuthorizationSupportsBrowserSnapshotAndNondiscovery(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := NewS2FixtureTelemetryAuthorizationStore(telemetryTestIssuer)
	allowed, err := evaluateTelemetryAuthorization(context.Background(), store, now, telemetryTestIssuer, "fixture-user", telemetryauth.DecisionRequest{
		ActingOrganizationID: S2FixtureActingOrganization,
		Action:               telemetryauth.ActionSnapshotRead,
		Targets:              []telemetryauth.Target{{DeviceID: S2FixtureDevice, Keys: []string{"humidity", "temperature"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed.Allowed || allowed.ReasonCode != telemetryauth.ReasonAllowExactScope || len(allowed.Targets) != 1 || allowed.Targets[0].DeviceID != S2FixtureDevice {
		t.Fatalf("fixture allow decision = %#v", allowed)
	}
	denied, err := evaluateTelemetryAuthorization(context.Background(), store, now, telemetryTestIssuer, "fixture-user", telemetryauth.DecisionRequest{
		ActingOrganizationID: S2FixtureActingOrganization,
		Action:               telemetryauth.ActionSnapshotRead,
		Targets:              []telemetryauth.Target{{DeviceID: "018f2e00-3000-7000-8000-000000000099"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.ReasonCode != telemetryauth.ReasonResourceNotFound || len(denied.Targets) != 0 {
		t.Fatalf("fixture nondiscovery decision = %#v", denied)
	}
}

func TestTelemetryAuthorizationRequiresExactDeviceAndKeyScope(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	facts := telemetryFixtureFacts(now)
	store := newStaticTelemetryAuthorizationStore(facts)
	request := telemetryauth.DecisionRequest{
		ActingOrganizationID: telemetryTestActingOrgID,
		Action:               telemetryauth.ActionSnapshotRead,
		Targets:              []telemetryauth.Target{{DeviceID: telemetryTestDeviceID, Keys: []string{"zone.temperature"}}},
	}
	decision, err := evaluateTelemetryAuthorization(context.Background(), store, now, telemetryTestIssuer, telemetryTestSubject, request)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed || decision.ReasonCode != telemetryauth.ReasonAllowExactScope || len(decision.Targets) != 1 {
		t.Fatalf("allow decision = %#v", decision)
	}
	if decision.Targets[0].OwningOrganizationID != telemetryTestOwningOrgID || decision.Targets[0].SiteID != telemetryTestSiteID {
		t.Fatalf("resolved target = %#v", decision.Targets[0])
	}
}

func TestTelemetryAuthorizationDoesNotTreatMembershipAsAllSites(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	facts := telemetryFixtureFacts(now)
	facts.Devices = append(facts.Devices, TelemetryDevice{ID: telemetryTestSiblingDevice, OwningOrganizationID: telemetryTestOwningOrgID, SiteID: telemetryTestSiblingSite, Status: FactStatusActive})
	request := telemetryauth.DecisionRequest{
		ActingOrganizationID: telemetryTestActingOrgID,
		Action:               telemetryauth.ActionSnapshotRead,
		Targets:              []telemetryauth.Target{{DeviceID: telemetryTestSiblingDevice}},
	}
	decision, err := evaluateTelemetryAuthorization(context.Background(), newStaticTelemetryAuthorizationStore(facts), now, telemetryTestIssuer, telemetryTestSubject, request)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.ReasonCode != telemetryauth.ReasonResourceNotFound {
		t.Fatalf("sibling Site decision = %#v", decision)
	}
}

func TestTelemetryAuthorizationRequiresExactCrossOrganizationSiteBinding(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	facts := telemetryFixtureFacts(now)
	facts.Devices = append(facts.Devices, TelemetryDevice{ID: telemetryTestOtherDevice, OwningOrganizationID: telemetryTestOtherOrgID, SiteID: telemetryTestOtherSite, Status: FactStatusActive})
	facts.ScopeBindings = append(facts.ScopeBindings, TelemetryScopeBinding{
		ActingOrganizationID: telemetryTestActingOrgID, OwningOrganizationID: telemetryTestOtherOrgID, SiteID: telemetryTestOtherSite, DeviceID: telemetryTestOtherDevice,
		Actions: []telemetryauth.Action{telemetryauth.ActionSnapshotRead}, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
	})
	facts.KeyBindings = append(facts.KeyBindings, TelemetryKeyBinding{
		ActingOrganizationID: telemetryTestActingOrgID, DeviceID: telemetryTestOtherDevice, Key: "zone.temperature",
		Actions: []telemetryauth.Action{telemetryauth.ActionSnapshotRead}, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
	})
	request := telemetryauth.DecisionRequest{
		ActingOrganizationID: telemetryTestActingOrgID,
		Action:               telemetryauth.ActionSnapshotRead,
		Targets:              []telemetryauth.Target{{DeviceID: telemetryTestOtherDevice, Keys: []string{"zone.temperature"}}},
	}
	decision, err := evaluateTelemetryAuthorization(context.Background(), newStaticTelemetryAuthorizationStore(facts), now, telemetryTestIssuer, telemetryTestSubject, request)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.ReasonCode != telemetryauth.ReasonResourceNotFound || len(decision.Targets) != 0 {
		t.Fatalf("unbound second-Organization Device decision = %#v", decision)
	}
}

func TestTelemetryAuthorizationDenyPrecedenceAndStableKeyFailure(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	facts := telemetryFixtureFacts(now)
	facts.KeyBindings = append(facts.KeyBindings, TelemetryKeyBinding{
		ActingOrganizationID: telemetryTestActingOrgID, DeviceID: telemetryTestDeviceID, Key: "zone.temperature",
		Actions: []telemetryauth.Action{telemetryauth.ActionSnapshotRead}, Effect: BindingEffectDeny, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
	})
	request := telemetryauth.DecisionRequest{
		ActingOrganizationID: telemetryTestActingOrgID,
		Action:               telemetryauth.ActionSnapshotRead,
		Targets:              []telemetryauth.Target{{DeviceID: telemetryTestDeviceID, Keys: []string{"zone.temperature"}}},
	}
	decision, err := evaluateTelemetryAuthorization(context.Background(), newStaticTelemetryAuthorizationStore(facts), now, telemetryTestIssuer, telemetryTestSubject, request)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.ReasonCode != telemetryauth.ReasonTelemetryKeyInvalid {
		t.Fatalf("explicit key deny decision = %#v", decision)
	}

	request.Targets[0].Keys = []string{"unknown.key"}
	decision, err = evaluateTelemetryAuthorization(context.Background(), newStaticTelemetryAuthorizationStore(telemetryFixtureFacts(now)), now, telemetryTestIssuer, telemetryTestSubject, request)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.ReasonCode != telemetryauth.ReasonTelemetryKeyInvalid {
		t.Fatalf("unknown key decision = %#v", decision)
	}
}

func TestTelemetryAuthorizationIsAllOrNothing(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	facts := telemetryFixtureFacts(now)
	facts.Devices = append(facts.Devices, TelemetryDevice{ID: telemetryTestSiblingDevice, OwningOrganizationID: telemetryTestOwningOrgID, SiteID: telemetryTestSiblingSite, Status: FactStatusActive})
	request := telemetryauth.DecisionRequest{
		ActingOrganizationID: telemetryTestActingOrgID,
		Action:               telemetryauth.ActionSnapshotRead,
		Targets:              []telemetryauth.Target{{DeviceID: telemetryTestDeviceID, Keys: []string{"zone.temperature"}}, {DeviceID: telemetryTestSiblingDevice}},
	}
	decision, err := evaluateTelemetryAuthorization(context.Background(), newStaticTelemetryAuthorizationStore(facts), now, telemetryTestIssuer, telemetryTestSubject, request)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.ReasonCode != telemetryauth.ReasonResourceNotFound || len(decision.Targets) != 0 || decision.ScopeDigest != "" {
		t.Fatalf("partial batch leaked authorization state: %#v", decision)
	}
}

func TestPostgresTelemetryActionProjectionRejectsUnknownSchemaDrift(t *testing.T) {
	actions, err := postgresTelemetryRegistryActions([]string{string(registryauth.ActionRegistryRead), string(telemetryauth.ActionSnapshotRead)})
	if err != nil || len(actions) != 1 || string(actions[0]) != string(telemetryauth.ActionSnapshotRead) {
		t.Fatalf("known cross-domain actions = %#v, err=%v", actions, err)
	}
	if _, err := postgresTelemetryRegistryActions([]string{"telemetry.unknown"}); err == nil {
		t.Fatal("unknown IAM action schema drift was silently ignored")
	}
}

func telemetryFixtureFacts(now time.Time) TelemetryAuthorizationFacts {
	return TelemetryAuthorizationFacts{
		PolicyRevision: "telemetry-access:1",
		Principal:      PrincipalRecord{ID: telemetryTestPrincipalID, SubjectIssuer: telemetryTestIssuer, Subject: telemetryTestSubject, Status: FactStatusActive},
		Memberships:    []OrganizationMembership{{OrganizationID: telemetryTestActingOrgID, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		RoleBindings: []RoleBinding{{
			OrganizationID: telemetryTestActingOrgID,
			Actions:        []registryauth.Action{registryauth.Action(telemetryauth.ActionSnapshotRead), registryauth.Action(telemetryauth.ActionSubscribe)},
			Effect:         BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
		}},
		SiteBindings: []SiteBinding{{
			ActingOrganizationID: telemetryTestActingOrgID, OwningOrganizationID: telemetryTestOwningOrgID, SiteID: telemetryTestSiteID,
			Actions: []registryauth.Action{registryauth.Action(telemetryauth.ActionSnapshotRead), registryauth.Action(telemetryauth.ActionSubscribe)},
			Effect:  BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
		}},
		Devices: []TelemetryDevice{{ID: telemetryTestDeviceID, OwningOrganizationID: telemetryTestOwningOrgID, SiteID: telemetryTestSiteID, Status: FactStatusActive}},
		ScopeBindings: []TelemetryScopeBinding{{
			ActingOrganizationID: telemetryTestActingOrgID, OwningOrganizationID: telemetryTestOwningOrgID, SiteID: telemetryTestSiteID, DeviceID: telemetryTestDeviceID,
			Actions: []telemetryauth.Action{telemetryauth.ActionSnapshotRead, telemetryauth.ActionSubscribe}, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
		}},
		KeyBindings: []TelemetryKeyBinding{{
			ActingOrganizationID: telemetryTestActingOrgID, DeviceID: telemetryTestDeviceID, Key: "zone.temperature",
			Actions: []telemetryauth.Action{telemetryauth.ActionSnapshotRead, telemetryauth.ActionSubscribe}, Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
		}},
	}
}
