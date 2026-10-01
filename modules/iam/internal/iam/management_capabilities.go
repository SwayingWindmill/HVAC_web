package iam

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

type managementCapabilityResolver struct {
	base  PrincipalCapabilityResolver
	store AuthorizationStore
	now   func() time.Time
}

func withManagementCapabilities(base PrincipalCapabilityResolver, store AuthorizationStore, now func() time.Time) PrincipalCapabilityResolver {
	return &managementCapabilityResolver{base: base, store: store, now: now}
}

func (resolver *managementCapabilityResolver) ResolvePrincipalCapabilities(ctx context.Context, lookup PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error) {
	authorization, err := resolver.base.ResolvePrincipalCapabilities(ctx, lookup)
	if err != nil {
		return identitycontext.EffectiveAuthorization{}, err
	}
	facts, err := resolver.store.LookupRegistryAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer: lookup.SubjectIssuer,
		Subject:       lookup.Subject,
		TenantID:      lookup.TenantID,
		At:            resolver.now(),
	})
	if err != nil {
		return identitycontext.EffectiveAuthorization{}, err
	}
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return authorization, nil
	}
	now := resolver.now()
	membershipActive, _ := tenantMembershipState(facts.Memberships, lookup.TenantID, now)
	if !membershipActive {
		return authorization, nil
	}

	allowed := make(map[identitycontext.Capability]struct{}, len(authorization.Capabilities))
	for _, capability := range authorization.Capabilities {
		allowed[capability] = struct{}{}
	}
	denied := map[identitycontext.Capability]struct{}{}
	for _, binding := range facts.RoleBindings {
		if binding.Status != FactStatusActive || binding.TenantID != lookup.TenantID || !factEffective(binding.ValidFrom, binding.ValidTo, now) {
			continue
		}
		for _, capability := range binding.Capabilities {
			if binding.Effect == BindingEffectDeny {
				denied[capability] = struct{}{}
				continue
			}
			allowed[capability] = struct{}{}
		}
	}
	for _, deny := range facts.ExplicitDenies {
		if deny.Status != FactStatusActive || deny.TenantID != lookup.TenantID || !factEffective(deny.ValidFrom, deny.ValidTo, now) || deny.SiteID != "" {
			continue
		}
		for _, capability := range deny.Capabilities {
			denied[capability] = struct{}{}
		}
	}
	for capability := range denied {
		delete(allowed, capability)
	}

	capabilities := make([]identitycontext.Capability, 0, len(allowed))
	for _, capability := range identitycontext.SupportedCapabilities() {
		if _, ok := allowed[capability]; ok {
			capabilities = append(capabilities, capability)
		}
	}
	authorization.Capabilities = capabilities
	return authorization, nil
}
