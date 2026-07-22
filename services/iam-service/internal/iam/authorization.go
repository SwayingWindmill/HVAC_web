package iam

import (
	"context"
	"sort"
	"time"

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

type OrganizationMembership struct {
	OrganizationID string
	Status         FactStatus
	ValidFrom      time.Time
	ValidTo        *time.Time
}

type RoleBinding struct {
	OrganizationID string
	SiteID         string
	Actions        []registryauth.Action
	Effect         BindingEffect
	Status         FactStatus
	ValidFrom      time.Time
	ValidTo        *time.Time
}

type SiteBinding struct {
	ActingOrganizationID string
	OwningOrganizationID string
	SiteID               string
	Actions              []registryauth.Action
	Effect               BindingEffect
	Status               FactStatus
	ValidFrom            time.Time
	ValidTo              *time.Time
}

type ExplicitDeny struct {
	ActingOrganizationID string
	OrganizationID       string
	SiteID               string
	Actions              []registryauth.Action
	Status               FactStatus
	ValidFrom            time.Time
	ValidTo              *time.Time
}

type AuthorizationFacts struct {
	Found          bool
	PolicyRevision string
	Principal      PrincipalRecord
	Memberships    []OrganizationMembership
	RoleBindings   []RoleBinding
	SiteBindings   []SiteBinding
	ExplicitDenies []ExplicitDeny
}

type AuthorizationLookup struct {
	SubjectIssuer        string
	Subject              string
	ActingOrganizationID string
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
		SubjectIssuer:        subjectIssuer,
		Subject:              subject,
		ActingOrganizationID: request.ActingOrganizationID,
	})
	if err != nil {
		return registryauth.Decision{}, err
	}
	decision := registryauth.Decision{
		AllowedOrganizationIDs: []string{},
		AllowedSiteIDs:         []string{},
		DeniedOrganizationIDs:  []string{},
		DeniedSiteIDs:          []string{},
		Actions:                []registryauth.Action{request.Action},
		PolicyRevision:         facts.PolicyRevision,
		SubjectIssuer:          subjectIssuer,
		Subject:                subject,
		ActingOrganizationID:   request.ActingOrganizationID,
		DecidedAt:              formatInstant(now),
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
	membershipActive, membershipRevoked := membershipState(facts.Memberships, request.ActingOrganizationID, now)
	if !membershipActive {
		if membershipRevoked {
			decision.ReasonCode = registryauth.ReasonDenyMembershipRevoked
		} else {
			decision.ReasonCode = registryauth.ReasonDenyMembershipRequired
		}
		return decision, nil
	}

	allowedOrganizations := map[string]struct{}{}
	allowedSites := map[string]struct{}{}
	allowedSiteOwners := map[string]string{}
	deniedOrganizations := map[string]struct{}{}
	deniedSites := map[string]struct{}{}
	allowReason := registryauth.ReasonCode("")

	for _, binding := range facts.RoleBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.OrganizationID != request.ActingOrganizationID || !actionsAllow(binding.Actions, request.Action) || !bindingEffectAllows(binding.Effect) {
			continue
		}
		if binding.SiteID == "" {
			allowedOrganizations[binding.OrganizationID] = struct{}{}
			allowReason = registryauth.ReasonAllowOrganizationRole
			continue
		}
		if request.Action.SiteScoped() {
			allowedSites[binding.SiteID] = struct{}{}
			allowedSiteOwners[binding.SiteID] = binding.OrganizationID
			if allowReason == "" {
				allowReason = registryauth.ReasonAllowSiteRole
			}
		}
	}

	if request.Action.SiteScoped() {
		for _, binding := range facts.SiteBindings {
			if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.ActingOrganizationID != request.ActingOrganizationID || !actionsAllow(binding.Actions, request.Action) || !bindingEffectAllows(binding.Effect) {
				continue
			}
			allowedSites[binding.SiteID] = struct{}{}
			allowedSiteOwners[binding.SiteID] = binding.OwningOrganizationID
			if allowReason == "" {
				allowReason = registryauth.ReasonAllowSiteBinding
			}
		}
	}

	explicitDenyMatched := false
	for _, binding := range facts.RoleBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.OrganizationID != request.ActingOrganizationID || !actionsAllow(binding.Actions, request.Action) || binding.Effect != BindingEffectDeny {
			continue
		}
		explicitDenyMatched = true
		clear(allowedOrganizations)
		clear(allowedSites)
		deniedOrganizations[request.ActingOrganizationID] = struct{}{}
	}
	for _, binding := range facts.SiteBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.ActingOrganizationID != request.ActingOrganizationID || !actionsAllow(binding.Actions, request.Action) || binding.Effect != BindingEffectDeny {
			continue
		}
		explicitDenyMatched = true
		delete(allowedSites, binding.SiteID)
		deniedSites[binding.SiteID] = struct{}{}
	}
	for _, deny := range facts.ExplicitDenies {
		if deny.Status != FactStatusActive || !factEffective(deny.ValidFrom, deny.ValidTo, now) || !actionsAllow(deny.Actions, request.Action) {
			continue
		}
		if deny.ActingOrganizationID != "" && deny.ActingOrganizationID != request.ActingOrganizationID {
			continue
		}
		explicitDenyMatched = true
		if deny.SiteID != "" {
			delete(allowedSites, deny.SiteID)
			deniedSites[deny.SiteID] = struct{}{}
			continue
		}
		if deny.OrganizationID != "" {
			delete(allowedOrganizations, deny.OrganizationID)
			deniedOrganizations[deny.OrganizationID] = struct{}{}
			for siteID, ownerID := range allowedSiteOwners {
				if ownerID == deny.OrganizationID {
					delete(allowedSites, siteID)
				}
			}
		}
	}

	decision.AllowedOrganizationIDs = sortedKeys(allowedOrganizations)
	decision.AllowedSiteIDs = sortedKeys(allowedSites)
	decision.DeniedOrganizationIDs = sortedKeys(deniedOrganizations)
	decision.DeniedSiteIDs = sortedKeys(deniedSites)
	decision.Allowed = len(decision.AllowedOrganizationIDs) > 0 || len(decision.AllowedSiteIDs) > 0
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

func membershipState(memberships []OrganizationMembership, organizationID string, now time.Time) (bool, bool) {
	revoked := false
	for _, membership := range memberships {
		if membership.OrganizationID != organizationID {
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

func actionsAllow(actions []registryauth.Action, requested registryauth.Action) bool {
	for _, action := range actions {
		if registryauth.ActionAllows(action, requested) {
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
	value.Memberships = append([]OrganizationMembership(nil), value.Memberships...)
	value.RoleBindings = append([]RoleBinding(nil), value.RoleBindings...)
	for index := range value.RoleBindings {
		value.RoleBindings[index].Actions = append([]registryauth.Action(nil), value.RoleBindings[index].Actions...)
	}
	value.SiteBindings = append([]SiteBinding(nil), value.SiteBindings...)
	for index := range value.SiteBindings {
		value.SiteBindings[index].Actions = append([]registryauth.Action(nil), value.SiteBindings[index].Actions...)
	}
	value.ExplicitDenies = append([]ExplicitDeny(nil), value.ExplicitDenies...)
	for index := range value.ExplicitDenies {
		value.ExplicitDenies[index].Actions = append([]registryauth.Action(nil), value.ExplicitDenies[index].Actions...)
	}
	return value
}

func formatInstant(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}
