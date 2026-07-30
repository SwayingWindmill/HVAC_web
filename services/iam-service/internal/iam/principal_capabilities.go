package iam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

type PrincipalCapabilityLookup struct {
	SubjectIssuer        string
	Subject              string
	ActingOrganizationID string
}

type PrincipalCapabilityResolver interface {
	ResolvePrincipalCapabilities(context.Context, PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error)
}

type principalCapabilityResolver struct {
	registryStore  AuthorizationStore
	telemetryStore TelemetryAuthorizationStore
	now            func() time.Time
}

type resolvedAuthorizationStore struct {
	facts AuthorizationFacts
}

func newPrincipalCapabilityResolver(registryStore AuthorizationStore, telemetryStore TelemetryAuthorizationStore, now func() time.Time) PrincipalCapabilityResolver {
	return &principalCapabilityResolver{registryStore: registryStore, telemetryStore: telemetryStore, now: now}
}

func (resolver *principalCapabilityResolver) ResolvePrincipalCapabilities(ctx context.Context, lookup PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error) {
	registryFacts, err := resolver.registryStore.LookupRegistryAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer:        lookup.SubjectIssuer,
		Subject:              lookup.Subject,
		ActingOrganizationID: lookup.ActingOrganizationID,
	})
	if err != nil {
		return identitycontext.EffectiveAuthorization{}, err
	}
	telemetryFacts, err := resolver.telemetryStore.LookupPrincipalTelemetryCapabilities(ctx, lookup)
	if err != nil {
		return identitycontext.EffectiveAuthorization{}, err
	}

	capabilities := make([]identitycontext.Capability, 0, len(principalRegistryCapabilities)+len(principalTelemetryCapabilities))
	factStore := resolvedAuthorizationStore{facts: registryFacts}
	decidedAt := resolver.now()
	for _, candidate := range principalRegistryCapabilities {
		decision, err := evaluateRegistryAuthorization(ctx, factStore, decidedAt, lookup.SubjectIssuer, lookup.Subject, registryauth.DecisionRequest{
			ActingOrganizationID: lookup.ActingOrganizationID,
			Action:               candidate.action,
		})
		if err != nil {
			return identitycontext.EffectiveAuthorization{}, err
		}
		if decision.Allowed {
			capabilities = append(capabilities, candidate.capability)
		}
	}
	for _, candidate := range principalTelemetryCapabilities {
		if telemetryCapabilityAllowed(telemetryFacts, decidedAt, lookup.ActingOrganizationID, candidate.action) {
			capabilities = append(capabilities, candidate.capability)
		}
	}

	return identitycontext.EffectiveAuthorization{
		CapabilitySetVersion: identitycontext.CapabilitySetVersion,
		PolicyRevision:       combinedCapabilityPolicyRevision(registryFacts.PolicyRevision, telemetryFacts.PolicyRevision),
		Capabilities:         capabilities,
	}, nil
}

func combinedCapabilityPolicyRevision(registryRevision, telemetryRevision string) string {
	digest := sha256.Sum256([]byte(registryRevision + "\x00" + telemetryRevision))
	return "capability-v2:" + hex.EncodeToString(digest[:])
}

func telemetryCapabilityAllowed(facts TelemetryAuthorizationFacts, now time.Time, actingOrganizationID string, action telemetryauth.Action) bool {
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return false
	}
	membershipActive, _ := membershipState(facts.Memberships, actingOrganizationID, now)
	if !membershipActive {
		return false
	}
	for _, binding := range facts.ScopeBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.ActingOrganizationID != actingOrganizationID || binding.Effect != BindingEffectAllow {
			continue
		}
		device := TelemetryDevice{
			ID:                   binding.DeviceID,
			OwningOrganizationID: binding.OwningOrganizationID,
			SiteID:               binding.SiteID,
			Status:               FactStatusActive,
		}
		actionAllowed, actionDenied := telemetryActionScope(facts.RoleBindings, facts.SiteBindings, facts.ExplicitDenies, now, actingOrganizationID, device, action)
		scopeAllowed, scopeDenied := telemetryDeviceScope(facts.ScopeBindings, now, actingOrganizationID, device, action)
		if actionAllowed && !actionDenied && scopeAllowed && !scopeDenied {
			return true
		}
	}
	return false
}

func (store resolvedAuthorizationStore) LookupRegistryAuthorization(context.Context, AuthorizationLookup) (AuthorizationFacts, error) {
	return cloneAuthorizationFacts(store.facts), nil
}

var principalRegistryCapabilities = []struct {
	capability identitycontext.Capability
	action     registryauth.Action
}{
	{identitycontext.CapabilityOrganizationList, registryauth.ActionOrganizationList},
	{identitycontext.CapabilityOrganizationRead, registryauth.ActionOrganizationRead},
	{identitycontext.CapabilitySiteList, registryauth.ActionSiteList},
	{identitycontext.CapabilitySiteRead, registryauth.ActionSiteRead},
	{identitycontext.CapabilityEquipmentList, registryauth.ActionEquipmentList},
	{identitycontext.CapabilityEquipmentRead, registryauth.ActionEquipmentRead},
	{identitycontext.CapabilityDeviceList, registryauth.ActionDeviceList},
	{identitycontext.CapabilityDeviceRead, registryauth.ActionDeviceRead},
}

var principalTelemetryCapabilities = []struct {
	capability identitycontext.Capability
	action     telemetryauth.Action
}{
	{identitycontext.CapabilityTelemetrySnapshotRead, telemetryauth.ActionSnapshotRead},
	{identitycontext.CapabilityTelemetryBatchRead, telemetryauth.ActionBatchRead},
	{identitycontext.CapabilityTelemetrySubscribe, telemetryauth.ActionSubscribe},
	{identitycontext.CapabilityTelemetryHistoryRead, telemetryauth.ActionHistoryRead},
}
