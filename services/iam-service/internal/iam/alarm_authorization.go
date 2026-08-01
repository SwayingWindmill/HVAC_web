package iam

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
)

type AlarmPermission struct {
	OrganizationID string
	SiteID         string
	Action         alarmauth.Action
	Effect         BindingEffect
	Status         FactStatus
	ValidFrom      time.Time
	ValidTo        *time.Time
}

type AlarmAuthorizationFacts struct {
	Found          bool
	PolicyRevision string
	Principal      PrincipalRecord
	Memberships    []OrganizationMembership
	Permissions    []AlarmPermission
}

type AlarmAuthorizationStore interface {
	LookupAlarmAuthorization(context.Context, AuthorizationLookup) (AlarmAuthorizationFacts, error)
}

type staticAlarmAuthorizationStore struct {
	policyRevision string
	facts          map[string]AlarmAuthorizationFacts
}

func newStaticAlarmAuthorizationStore(policyRevision string, facts []AlarmAuthorizationFacts) AlarmAuthorizationStore {
	indexed := make(map[string]AlarmAuthorizationFacts, len(facts))
	for _, value := range facts {
		value.Found = true
		value.PolicyRevision = policyRevision
		indexed[authorizationIdentityKey(value.Principal.SubjectIssuer, value.Principal.Subject)] = cloneAlarmAuthorizationFacts(value)
	}
	return &staticAlarmAuthorizationStore{policyRevision: policyRevision, facts: indexed}
}

func newDenyAllAlarmAuthorizationStore(policyRevision string) AlarmAuthorizationStore {
	return &staticAlarmAuthorizationStore{policyRevision: policyRevision, facts: map[string]AlarmAuthorizationFacts{}}
}

func (store *staticAlarmAuthorizationStore) LookupAlarmAuthorization(_ context.Context, lookup AuthorizationLookup) (AlarmAuthorizationFacts, error) {
	value, ok := store.facts[authorizationIdentityKey(lookup.SubjectIssuer, lookup.Subject)]
	if !ok {
		return AlarmAuthorizationFacts{PolicyRevision: store.policyRevision}, nil
	}
	return cloneAlarmAuthorizationFacts(value), nil
}

func evaluateAlarmAuthorization(ctx context.Context, store AlarmAuthorizationStore, now time.Time, subjectIssuer, subject string, request alarmauth.DecisionRequest) (alarmauth.Decision, error) {
	facts, err := store.LookupAlarmAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer: subjectIssuer, Subject: subject, ActingOrganizationID: request.ActingOrganizationID,
	})
	if err != nil {
		return alarmauth.Decision{}, err
	}
	decision := alarmauth.Decision{
		SubjectIssuer: subjectIssuer, Subject: subject,
		ActingOrganizationID: request.ActingOrganizationID, SiteID: request.SiteID, AlarmID: request.AlarmID,
		Action: request.Action, PolicyRevision: facts.PolicyRevision, ReasonCode: alarmauth.ReasonDenyPrincipal,
		DecidedAt: now.UTC().Format(time.RFC3339Nano),
	}
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return decision, nil
	}
	decision.PrincipalID = facts.Principal.ID
	membershipActive, _ := membershipState(facts.Memberships, request.ActingOrganizationID, now)
	if !membershipActive {
		decision.ReasonCode = alarmauth.ReasonDenyMembership
		return decision, nil
	}
	allowFound := false
	for _, permission := range facts.Permissions {
		if permission.Status != FactStatusActive || !factEffective(permission.ValidFrom, permission.ValidTo, now) ||
			permission.OrganizationID != request.ActingOrganizationID || permission.SiteID != request.SiteID || permission.Action != request.Action {
			continue
		}
		if permission.Effect == BindingEffectDeny {
			decision.ReasonCode = alarmauth.ReasonDenyExplicit
			return decision, nil
		}
		if permission.Effect == BindingEffectAllow {
			allowFound = true
		}
	}
	if !allowFound {
		decision.ReasonCode = alarmauth.ReasonDenyScope
		return decision, nil
	}
	decision.Allowed = true
	decision.ReasonCode = alarmauth.ReasonAllowExactScope
	return decision, nil
}

func alarmCapabilityAllowed(facts AlarmAuthorizationFacts, now time.Time, actingOrganizationID string, action alarmauth.Action) bool {
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return false
	}
	membershipActive, _ := membershipState(facts.Memberships, actingOrganizationID, now)
	if !membershipActive {
		return false
	}
	allowedSites := map[string]bool{}
	deniedSites := map[string]bool{}
	for _, permission := range facts.Permissions {
		if permission.Status != FactStatusActive || !factEffective(permission.ValidFrom, permission.ValidTo, now) ||
			permission.OrganizationID != actingOrganizationID || permission.Action != action {
			continue
		}
		switch permission.Effect {
		case BindingEffectDeny:
			deniedSites[permission.SiteID] = true
		case BindingEffectAllow:
			allowedSites[permission.SiteID] = true
		}
	}
	for siteID := range allowedSites {
		if !deniedSites[siteID] {
			return true
		}
	}
	return false
}

func cloneAlarmAuthorizationFacts(value AlarmAuthorizationFacts) AlarmAuthorizationFacts {
	cloned := value
	cloned.Memberships = append([]OrganizationMembership(nil), value.Memberships...)
	cloned.Permissions = append([]AlarmPermission(nil), value.Permissions...)
	return cloned
}
