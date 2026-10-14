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

type TenantMembership struct {
	TenantID  string
	Status    FactStatus
	ValidFrom time.Time
	ValidTo   *time.Time
}

// AuthorizationFacts is everything IAM knows about one Principal's access in one Tenant,
// read under one domain policy. Every decision evaluates it (ADR 0022).
type AuthorizationFacts struct {
	Found          bool
	PolicyRevision string
	Principal      PrincipalRecord
	TenantSiteIDs  []string
	Memberships    []TenantMembership
	Assignments    []RoleAssignment
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
	return &staticAuthorizationStore{policyRevision: policyRevision, facts: indexAuthorizationFacts(policyRevision, facts)}
}

// indexAuthorizationFacts keys fixture facts by external identity, as every static store looks them up.
func indexAuthorizationFacts(policyRevision string, facts []AuthorizationFacts) map[string]AuthorizationFacts {
	indexed := make(map[string]AuthorizationFacts, len(facts))
	for _, value := range facts {
		value.Found = true
		value.PolicyRevision = policyRevision
		indexed[authorizationIdentityKey(value.Principal.SubjectIssuer, value.Principal.Subject)] = cloneAuthorizationFacts(value)
	}
	return indexed
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
	if active, revoked := facts.member(request.TenantID, now); !active {
		decision.ReasonCode = registryauth.ReasonDenyMembershipRequired
		if revoked {
			decision.ReasonCode = registryauth.ReasonDenyMembershipRevoked
		}
		return decision, nil
	}

	capability := string(request.Action)
	if request.Action.TenantScoped() {
		allowed, denied := facts.grantsAt(request.TenantID, "", capability, now)
		switch {
		case denied:
			decision.ReasonCode = registryauth.ReasonDenyExplicit
		case !allowed:
			decision.ReasonCode = registryauth.ReasonDenyActionNotGranted
		default:
			decision.Allowed = true
			decision.ReasonCode = registryauth.ReasonAllowTenantRole
		}
		return decision, nil
	}

	decision.AllowedSiteIDs, decision.DeniedSiteIDs = facts.sitesGranting(request.TenantID, capability, now)
	decision.Allowed = len(decision.AllowedSiteIDs) > 0
	switch {
	case decision.Allowed && facts.grantsByTenantRole(request.TenantID, capability, now):
		decision.ReasonCode = registryauth.ReasonAllowTenantRole
	case decision.Allowed:
		decision.ReasonCode = registryauth.ReasonAllowSiteRole
	case len(decision.DeniedSiteIDs) > 0:
		decision.ReasonCode = registryauth.ReasonDenyExplicit
	default:
		decision.ReasonCode = registryauth.ReasonDenyActionNotGranted
	}
	return decision, nil
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
	value.Assignments = append([]RoleAssignment(nil), value.Assignments...)
	for index := range value.Assignments {
		value.Assignments[index].Capabilities = append([]string(nil), value.Assignments[index].Capabilities...)
	}
	value.ExplicitDenies = append([]ExplicitDeny(nil), value.ExplicitDenies...)
	return value
}

func formatInstant(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}
