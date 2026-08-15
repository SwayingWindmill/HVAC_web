package iam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

type PrincipalCapabilityLookup struct {
	SubjectIssuer        string
	Subject              string
	TenantID string
}

type PrincipalCapabilityResolver interface {
	ResolvePrincipalCapabilities(context.Context, PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error)
}

type principalCapabilityResolver struct {
	registryStore  AuthorizationStore
	telemetryStore TelemetryAuthorizationStore
	alarmStore     AlarmAuthorizationStore
	workOrderStore WorkOrderAuthorizationStore
	now            func() time.Time
}

type resolvedAuthorizationStore struct {
	facts AuthorizationFacts
}

func newPrincipalCapabilityResolver(registryStore AuthorizationStore, telemetryStore TelemetryAuthorizationStore, alarmStore AlarmAuthorizationStore, workOrderStore WorkOrderAuthorizationStore, now func() time.Time) PrincipalCapabilityResolver {
	return &principalCapabilityResolver{registryStore: registryStore, telemetryStore: telemetryStore, alarmStore: alarmStore, workOrderStore: workOrderStore, now: now}
}

func (resolver *principalCapabilityResolver) ResolvePrincipalCapabilities(ctx context.Context, lookup PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error) {
	registryFacts, err := resolver.registryStore.LookupRegistryAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer:        lookup.SubjectIssuer,
		Subject:              lookup.Subject,
		TenantID: lookup.TenantID,
	})
	if err != nil {
		return identitycontext.EffectiveAuthorization{}, err
	}
	telemetryFacts, err := resolver.telemetryStore.LookupPrincipalTelemetryCapabilities(ctx, lookup)
	if err != nil {
		return identitycontext.EffectiveAuthorization{}, err
	}
	alarmFacts, err := resolver.alarmStore.LookupAlarmAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer:        lookup.SubjectIssuer,
		Subject:              lookup.Subject,
		TenantID: lookup.TenantID,
	})
	if err != nil {
		return identitycontext.EffectiveAuthorization{}, err
	}
	workOrderFacts, err := resolver.workOrderStore.LookupWorkOrderAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer:        lookup.SubjectIssuer,
		Subject:              lookup.Subject,
		TenantID: lookup.TenantID,
	})
	if err != nil {
		return identitycontext.EffectiveAuthorization{}, err
	}

	capabilities := make([]identitycontext.Capability, 0, len(principalRegistryCapabilities)+len(principalTelemetryCapabilities)+len(principalAlarmCapabilities)+len(principalWorkOrderCapabilities))
	factStore := resolvedAuthorizationStore{facts: registryFacts}
	decidedAt := resolver.now()
	for _, candidate := range principalRegistryCapabilities {
		decision, err := evaluateRegistryAuthorization(ctx, factStore, decidedAt, lookup.SubjectIssuer, lookup.Subject, registryauth.DecisionRequest{
			TenantID: lookup.TenantID,
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
		if telemetryCapabilityAllowed(telemetryFacts, decidedAt, lookup.TenantID, candidate.action) {
			capabilities = append(capabilities, candidate.capability)
		}
	}
	for _, candidate := range principalAlarmCapabilities {
		if alarmCapabilityAllowed(alarmFacts, decidedAt, lookup.TenantID, candidate.action) {
			capabilities = append(capabilities, candidate.capability)
		}
	}
	for _, candidate := range principalWorkOrderCapabilities {
		if workOrderCapabilityAllowed(workOrderFacts, decidedAt, lookup.TenantID, candidate.action) {
			capabilities = append(capabilities, candidate.capability)
		}
	}
	if workOrderLifecycleCapabilityAllowed(workOrderFacts, decidedAt, lookup.TenantID) {
		capabilities = append(capabilities, identitycontext.CapabilityWorkOrderLifecycle)
	}

	return identitycontext.EffectiveAuthorization{
		CapabilitySetVersion: identitycontext.CapabilitySetVersion,
		PolicyRevision:       combinedCapabilityPolicyRevision(registryFacts.PolicyRevision, telemetryFacts.PolicyRevision, alarmFacts.PolicyRevision, workOrderFacts.PolicyRevision),
		Capabilities:         capabilities,
	}, nil
}

func combinedCapabilityPolicyRevision(registryRevision, telemetryRevision, alarmRevision, workOrderRevision string) string {
	digest := sha256.Sum256([]byte(registryRevision + "\x00" + telemetryRevision + "\x00" + alarmRevision + "\x00" + workOrderRevision))
	return "capability-v6:" + hex.EncodeToString(digest[:])
}

func telemetryCapabilityAllowed(facts TelemetryAuthorizationFacts, now time.Time, tenantID string, action telemetryauth.Action) bool {
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return false
	}
	membershipActive, _ := tenantMembershipState(facts.Memberships, tenantID, now)
	if !membershipActive {
		return false
	}
	for _, binding := range facts.ScopeBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != tenantID || binding.Effect != BindingEffectAllow {
			continue
		}
		device := TelemetryDevice{
			ID:       binding.DeviceID,
			TenantID: tenantID,
			SiteID:   binding.SiteID,
			Status:   FactStatusActive,
		}
		actionAllowed, actionDenied := telemetryActionScope(facts.RoleBindings, facts.SiteBindings, facts.ExplicitDenies, now, tenantID, device, action)
		scopeAllowed, scopeDenied := telemetryDeviceScope(facts.ScopeBindings, now, tenantID, device, action)
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
	{identitycontext.CapabilitySiteList, registryauth.ActionSiteList},
	{identitycontext.CapabilitySiteRead, registryauth.ActionSiteRead},
	{identitycontext.CapabilityEquipmentList, registryauth.ActionAssetList},
	{identitycontext.CapabilityEquipmentRead, registryauth.ActionAssetRead},
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

var principalAlarmCapabilities = []struct {
	capability identitycontext.Capability
	action     alarmauth.Action
}{
	{identitycontext.CapabilityAlarmList, alarmauth.ActionRead},
	{identitycontext.CapabilityAlarmRead, alarmauth.ActionRead},
}

var principalWorkOrderLifecycleActions = [...]workorderauth.Action{
	workorderauth.ActionPlan,
	workorderauth.ActionStart,
	workorderauth.ActionBlock,
	workorderauth.ActionResume,
	workorderauth.ActionComplete,
	workorderauth.ActionCancel,
	workorderauth.ActionReopen,
}

func workOrderLifecycleCapabilityAllowed(facts WorkOrderAuthorizationFacts, now time.Time, tenantID string) bool {
	for _, action := range principalWorkOrderLifecycleActions {
		if workOrderCapabilityAllowed(facts, now, tenantID, action) {
			return true
		}
	}
	return false
}

var principalWorkOrderCapabilities = []struct {
	capability identitycontext.Capability
	action     workorderauth.Action
}{
	{identitycontext.CapabilityWorkOrderList, workorderauth.ActionList},
	{identitycontext.CapabilityWorkOrderRead, workorderauth.ActionRead},
	{identitycontext.CapabilityWorkOrderCreate, workorderauth.ActionCreate},
	{identitycontext.CapabilityWorkOrderAssign, workorderauth.ActionAssign},
}
