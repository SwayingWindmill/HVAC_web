package iam

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type PrincipalCapabilityLookup struct {
	SubjectIssuer        string
	Subject              string
	ActingOrganizationID string
}

type PrincipalCapabilityResolver interface {
	ResolvePrincipalCapabilities(context.Context, PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error)
}

type registryPrincipalCapabilityResolver struct {
	store AuthorizationStore
	now   func() time.Time
}

type resolvedAuthorizationStore struct {
	facts AuthorizationFacts
}

func newRegistryPrincipalCapabilityResolver(store AuthorizationStore, now func() time.Time) PrincipalCapabilityResolver {
	return &registryPrincipalCapabilityResolver{store: store, now: now}
}

func (resolver *registryPrincipalCapabilityResolver) ResolvePrincipalCapabilities(ctx context.Context, lookup PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error) {
	facts, err := resolver.store.LookupRegistryAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer:        lookup.SubjectIssuer,
		Subject:              lookup.Subject,
		ActingOrganizationID: lookup.ActingOrganizationID,
	})
	if err != nil {
		return identitycontext.EffectiveAuthorization{}, err
	}

	capabilities := make([]identitycontext.Capability, 0, len(principalRegistryCapabilities))
	factStore := resolvedAuthorizationStore{facts: facts}
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

	return identitycontext.EffectiveAuthorization{
		CapabilitySetVersion: identitycontext.CapabilitySetVersion,
		PolicyRevision:       facts.PolicyRevision,
		Capabilities:         capabilities,
	}, nil
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
