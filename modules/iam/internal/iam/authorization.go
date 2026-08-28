package iam

import (
	"context"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type FactStatus string
type BindingEffect string

const (
	FactStatusActive    FactStatus = "ACTIVE"
	FactStatusSuspended FactStatus = "SUSPENDED"
	FactStatusRevoked   FactStatus = "REVOKED"

	BindingEffectAllow BindingEffect = "ALLOW"
	BindingEffectDeny  BindingEffect = "DENY"
)

type PrincipalRecord struct {
	ID            string
	SubjectIssuer string
	Subject       string
	Status        FactStatus
}

type TenantMembership struct {
	TenantID  string
	Status    FactStatus
	ValidFrom time.Time
	ValidTo   *time.Time
}

type RoleBinding struct {
	TenantID     string
	SiteID       string
	RoleKey      string
	Actions      []registryauth.Action
	Capabilities []identitycontext.Capability
	Effect       BindingEffect
	Status       FactStatus
	ValidFrom    time.Time
	ValidTo      *time.Time
}

type SiteBinding struct {
	TenantID  string
	SiteID    string
	Actions   []registryauth.Action
	Effect    BindingEffect
	Status    FactStatus
	ValidFrom time.Time
	ValidTo   *time.Time
}

type ExplicitDeny struct {
	TenantID     string
	SiteID       string
	Actions      []registryauth.Action
	Capabilities []identitycontext.Capability
	Status       FactStatus
	ValidFrom    time.Time
	ValidTo      *time.Time
}

type AuthorizationFacts struct {
	Found          bool
	PolicyRevision string
	Principal      PrincipalRecord
	TenantSiteIDs  []string
	Memberships    []TenantMembership
	RoleBindings   []RoleBinding
	SiteBindings   []SiteBinding
	ExplicitDenies []ExplicitDeny
}

type AuthorizationLookup struct {
	SubjectIssuer string
	Subject       string
	TenantID      string
	At            time.Time
}

type AuthorizationStore interface {
	LookupRegistryAuthorization(context.Context, AuthorizationLookup) (AuthorizationFacts, error)
}

type staticAuthorizationStore struct {
	policyRevision string
	facts          map[string]AuthorizationFacts
}

func newStaticAuthorizationStore(policyRevision string, facts []AuthorizationFacts) AuthorizationStore {
	indexed := make(map[string]AuthorizationFacts, len(facts))
	for _, value := range facts {
		value.Found = true
		value.PolicyRevision = policyRevision
		indexed[authorizationIdentityKey(value.Principal.SubjectIssuer, value.Principal.Subject)] = cloneAuthorizationFacts(value)
	}
	return &staticAuthorizationStore{policyRevision: policyRevision, facts: indexed}
}

func newDenyAllAuthorizationStore(policyRevision string) AuthorizationStore {
	return &staticAuthorizationStore{policyRevision: policyRevision, facts: map[string]AuthorizationFacts{}}
}

func (store *staticAuthorizationStore) LookupRegistryAuthorization(_ context.Context, lookup AuthorizationLookup) (AuthorizationFacts, error) {
	value, ok := store.facts[authorizationIdentityKey(lookup.SubjectIssuer, lookup.Subject)]
	if !ok {
		return AuthorizationFacts{Found: false, PolicyRevision: store.policyRevision}, nil
	}
	return cloneAuthorizationFacts(value), nil
}

func evaluateRegistryAuthorization(ctx context.Context, store AuthorizationStore, now time.Time, subjectIssuer, subject string, request registryauth.DecisionRequest) (registryauth.Decision, error) {
	facts, err := store.LookupRegistryAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer: subjectIssuer,
		Subject:       subject,
		TenantID:      request.TenantID,
		At:            now,
	})
	if err != nil {
		return registryauth.Decision{}, err
	}
	decision := registryauth.Decision{
		TenantID:       request.TenantID,
		AllowedSiteIDs: []string{},
		DeniedSiteIDs:  []string{},
		Actions:        []registryauth.Action{request.Action},
		PolicyRevision: facts.PolicyRevision,
		SubjectIssuer:  subjectIssuer,
		Subject:        subject,
		DecidedAt:      formatInstant(now),
	}
	if !facts.Found {
		decision.ReasonCode = registryauth.ReasonDenyPrincipalNotFound
		return decision, nil
	}
	decision.PrincipalID = facts.Principal.ID
	if facts.Principal.Status != FactStatusActive {
		decision.ReasonCode = registryauth.ReasonDenyPrincipalInactive
		return decision, nil
	}
	membershipActive, membershipRevoked := tenantMembershipState(facts.Memberships, request.TenantID, now)
	if !membershipActive {
		if membershipRevoked {
			decision.ReasonCode = registryauth.ReasonDenyMembershipRevoked
		} else {
			decision.ReasonCode = registryauth.ReasonDenyMembershipRequired
		}
		return decision, nil
	}

	tenantSites := make(map[string]struct{}, len(facts.TenantSiteIDs))
	for _, siteID := range facts.TenantSiteIDs {
		if siteID != "" {
			tenantSites[siteID] = struct{}{}
		}
	}
	for _, binding := range facts.RoleBindings {
		if binding.TenantID == request.TenantID && binding.SiteID != "" {
			tenantSites[binding.SiteID] = struct{}{}
		}
	}
	for _, binding := range facts.SiteBindings {
		if binding.TenantID == request.TenantID && binding.SiteID != "" {
			tenantSites[binding.SiteID] = struct{}{}
		}
	}
	for _, deny := range facts.ExplicitDenies {
		if deny.TenantID == request.TenantID && deny.SiteID != "" {
			tenantSites[deny.SiteID] = struct{}{}
		}
	}

	allowedSites := map[string]struct{}{}
	deniedSites := map[string]struct{}{}
	allowReason := registryauth.ReasonCode("")

	tenantRoleActions := []registryauth.Action{}
	roleSiteActions := map[string][]registryauth.Action{}
	for _, binding := range facts.RoleBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != request.TenantID || !bindingEffectAllows(binding.Effect) {
			continue
		}
		if binding.SiteID == "" {
			tenantRoleActions = append(tenantRoleActions, binding.Actions...)
			continue
		}
		if _, known := tenantSites[binding.SiteID]; known {
			roleSiteActions[binding.SiteID] = append(roleSiteActions[binding.SiteID], binding.Actions...)
		}
	}
	if request.Action.TenantScoped() {
		if !actionsAllow(tenantRoleActions, request.Action) {
			decision.ReasonCode = registryauth.ReasonDenyActionNotGranted
			return decision, nil
		}
		for _, binding := range facts.RoleBindings {
			if binding.Status == FactStatusActive && factEffective(binding.ValidFrom, binding.ValidTo, now) && binding.TenantID == request.TenantID && binding.SiteID == "" && binding.Effect == BindingEffectDeny && actionsDeny(binding.Actions, request.Action) {
				decision.ReasonCode = registryauth.ReasonDenyExplicit
				return decision, nil
			}
		}
		for _, deny := range facts.ExplicitDenies {
			if deny.Status == FactStatusActive && factEffective(deny.ValidFrom, deny.ValidTo, now) && deny.TenantID == request.TenantID && deny.SiteID == "" && actionsDeny(deny.Actions, request.Action) {
				decision.ReasonCode = registryauth.ReasonDenyExplicit
				return decision, nil
			}
		}
		decision.Allowed = true
		decision.ReasonCode = registryauth.ReasonAllowTenantRole
		return decision, nil
	}
	if actionsAllow(tenantRoleActions, request.Action) {
		for siteID := range tenantSites {
			allowedSites[siteID] = struct{}{}
		}
		if len(allowedSites) > 0 {
			allowReason = registryauth.ReasonAllowTenantRole
		}
	}
	for siteID, siteActions := range roleSiteActions {
		if actionsAllow(combineRegistryActions(tenantRoleActions, siteActions), request.Action) {
			allowedSites[siteID] = struct{}{}
			if allowReason == "" {
				allowReason = registryauth.ReasonAllowSiteRole
			}
		}
	}

	siteBindingActions := map[string][]registryauth.Action{}
	for _, binding := range facts.SiteBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != request.TenantID || !bindingEffectAllows(binding.Effect) {
			continue
		}
		if _, known := tenantSites[binding.SiteID]; known {
			siteBindingActions[binding.SiteID] = append(siteBindingActions[binding.SiteID], binding.Actions...)
		}
	}
	for siteID, siteActions := range siteBindingActions {
		if actionsAllow(combineRegistryActions(tenantRoleActions, roleSiteActions[siteID], siteActions), request.Action) {
			allowedSites[siteID] = struct{}{}
			if allowReason == "" {
				allowReason = registryauth.ReasonAllowSiteBinding
			}
		}
	}

	explicitDenyMatched := false
	for _, binding := range facts.RoleBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != request.TenantID || !actionsDeny(binding.Actions, request.Action) || binding.Effect != BindingEffectDeny {
			continue
		}
		explicitDenyMatched = true
		if binding.SiteID == "" {
			clear(allowedSites)
			for siteID := range tenantSites {
				deniedSites[siteID] = struct{}{}
			}
			continue
		}
		if _, known := tenantSites[binding.SiteID]; known {
			delete(allowedSites, binding.SiteID)
			deniedSites[binding.SiteID] = struct{}{}
		}
	}
	for _, binding := range facts.SiteBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != request.TenantID || !actionsDeny(binding.Actions, request.Action) || binding.Effect != BindingEffectDeny {
			continue
		}
		if _, known := tenantSites[binding.SiteID]; !known {
			continue
		}
		explicitDenyMatched = true
		delete(allowedSites, binding.SiteID)
		deniedSites[binding.SiteID] = struct{}{}
	}
	for _, deny := range facts.ExplicitDenies {
		if deny.Status != FactStatusActive || !factEffective(deny.ValidFrom, deny.ValidTo, now) || deny.TenantID != request.TenantID || !actionsDeny(deny.Actions, request.Action) {
			continue
		}
		explicitDenyMatched = true
		if deny.SiteID != "" {
			if _, known := tenantSites[deny.SiteID]; known {
				delete(allowedSites, deny.SiteID)
				deniedSites[deny.SiteID] = struct{}{}
			}
			continue
		}
		clear(allowedSites)
		for siteID := range tenantSites {
			deniedSites[siteID] = struct{}{}
		}
	}

	decision.AllowedSiteIDs = sortedKeys(allowedSites)
	decision.DeniedSiteIDs = sortedKeys(deniedSites)
	decision.Allowed = len(decision.AllowedSiteIDs) > 0
	if decision.Allowed {
		decision.ReasonCode = allowReason
		return decision, nil
	}
	if explicitDenyMatched {
		decision.ReasonCode = registryauth.ReasonDenyExplicit
	} else {
		decision.ReasonCode = registryauth.ReasonDenyActionNotGranted
	}
	return decision, nil
}

func membershipState(memberships []TenantMembership, tenantID string, now time.Time) (bool, bool) {
	return tenantMembershipState(memberships, tenantID, now)
}

func tenantMembershipState(memberships []TenantMembership, tenantID string, now time.Time) (bool, bool) {
	revoked := false
	for _, membership := range memberships {
		if membership.TenantID != tenantID {
			continue
		}
		if membership.Status == FactStatusRevoked {
			revoked = true
		}
		if membership.Status == FactStatusActive && factEffective(membership.ValidFrom, membership.ValidTo, now) {
			return true, revoked
		}
	}
	return false, revoked
}

func factEffective(validFrom time.Time, validTo *time.Time, now time.Time) bool {
	if !validFrom.IsZero() && now.Before(validFrom) {
		return false
	}
	return validTo == nil || now.Before(*validTo)
}

func combineRegistryActions(groups ...[]registryauth.Action) []registryauth.Action {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	combined := make([]registryauth.Action, 0, total)
	for _, group := range groups {
		combined = append(combined, group...)
	}
	return combined
}

func actionsAllow(actions []registryauth.Action, requested registryauth.Action) bool {
	if requested == registryauth.ActionDeviceBindingList {
		broadRead := hasRegistryAction(actions, registryauth.ActionRegistryRead)
		explicitRead := hasRegistryAction(actions, registryauth.ActionDeviceBindingList)
		constituentReads := hasRegistryAction(actions, registryauth.ActionAssetList) && hasRegistryAction(actions, registryauth.ActionDeviceList)
		return broadRead || explicitRead || constituentReads
	}
	for _, action := range actions {
		if registryauth.ActionAllows(action, requested) {
			return true
		}
	}
	return false
}

func actionsDeny(actions []registryauth.Action, requested registryauth.Action) bool {
	if requested == registryauth.ActionDeviceBindingList {
		broadDeny := hasRegistryAction(actions, registryauth.ActionRegistryRead) || hasRegistryAction(actions, registryauth.ActionDeviceBindingList)
		constituentDeny := hasRegistryAction(actions, registryauth.ActionAssetList) || hasRegistryAction(actions, registryauth.ActionDeviceList)
		return broadDeny || constituentDeny
	}
	return actionsAllow(actions, requested)
}

func hasRegistryAction(actions []registryauth.Action, requested registryauth.Action) bool {
	for _, action := range actions {
		if action == requested {
			return true
		}
	}
	return false
}

func bindingEffectAllows(effect BindingEffect) bool {
	return effect == "" || effect == BindingEffectAllow
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func authorizationIdentityKey(subjectIssuer, subject string) string {
	return subjectIssuer + "\x00" + subject
}

func cloneAuthorizationFacts(value AuthorizationFacts) AuthorizationFacts {
	value.TenantSiteIDs = append([]string(nil), value.TenantSiteIDs...)
	value.Memberships = append([]TenantMembership(nil), value.Memberships...)
	value.RoleBindings = append([]RoleBinding(nil), value.RoleBindings...)
	for index := range value.RoleBindings {
		value.RoleBindings[index].Actions = append([]registryauth.Action(nil), value.RoleBindings[index].Actions...)
		value.RoleBindings[index].Capabilities = append([]identitycontext.Capability(nil), value.RoleBindings[index].Capabilities...)
	}
	value.SiteBindings = append([]SiteBinding(nil), value.SiteBindings...)
	for index := range value.SiteBindings {
		value.SiteBindings[index].Actions = append([]registryauth.Action(nil), value.SiteBindings[index].Actions...)
	}
	value.ExplicitDenies = append([]ExplicitDeny(nil), value.ExplicitDenies...)
	for index := range value.ExplicitDenies {
		value.ExplicitDenies[index].Actions = append([]registryauth.Action(nil), value.ExplicitDenies[index].Actions...)
		value.ExplicitDenies[index].Capabilities = append([]identitycontext.Capability(nil), value.ExplicitDenies[index].Capabilities...)
	}
	return value
}

func formatInstant(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}
