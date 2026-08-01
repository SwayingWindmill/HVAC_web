package iam

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

type WorkOrderPermission struct {
	OrganizationID string
	SiteID         string
	Action         workorderauth.Action
	Effect         BindingEffect
	Status         FactStatus
	ValidFrom      time.Time
	ValidTo        *time.Time
}

type WorkOrderAuthorizationFacts struct {
	Found          bool
	PolicyRevision string
	Principal      PrincipalRecord
	Memberships    []OrganizationMembership
	Permissions    []WorkOrderPermission
}

type WorkOrderAuthorizationStore interface {
	LookupWorkOrderAuthorization(context.Context, AuthorizationLookup) (WorkOrderAuthorizationFacts, error)
}

type staticWorkOrderAuthorizationStore struct {
	policyRevision string
	facts          map[string]WorkOrderAuthorizationFacts
}

func newStaticWorkOrderAuthorizationStore(policyRevision string, facts []WorkOrderAuthorizationFacts) WorkOrderAuthorizationStore {
	indexed := make(map[string]WorkOrderAuthorizationFacts, len(facts))
	for _, value := range facts {
		value.Found = true
		value.PolicyRevision = policyRevision
		indexed[authorizationIdentityKey(value.Principal.SubjectIssuer, value.Principal.Subject)] = cloneWorkOrderAuthorizationFacts(value)
	}
	return &staticWorkOrderAuthorizationStore{policyRevision: policyRevision, facts: indexed}
}

func newDenyAllWorkOrderAuthorizationStore(policyRevision string) WorkOrderAuthorizationStore {
	return &staticWorkOrderAuthorizationStore{policyRevision: policyRevision, facts: map[string]WorkOrderAuthorizationFacts{}}
}

func (store *staticWorkOrderAuthorizationStore) LookupWorkOrderAuthorization(_ context.Context, lookup AuthorizationLookup) (WorkOrderAuthorizationFacts, error) {
	value, ok := store.facts[authorizationIdentityKey(lookup.SubjectIssuer, lookup.Subject)]
	if !ok {
		return WorkOrderAuthorizationFacts{PolicyRevision: store.policyRevision}, nil
	}
	return cloneWorkOrderAuthorizationFacts(value), nil
}

func evaluateWorkOrderAuthorization(ctx context.Context, store WorkOrderAuthorizationStore, now time.Time, subjectIssuer, subject string, request workorderauth.DecisionRequest) (workorderauth.Decision, error) {
	facts, err := store.LookupWorkOrderAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer: subjectIssuer, Subject: subject, ActingOrganizationID: request.ActingOrganizationID,
	})
	if err != nil {
		return workorderauth.Decision{}, err
	}
	decision := workorderauth.Decision{
		SubjectIssuer: subjectIssuer, Subject: subject,
		ActingOrganizationID: request.ActingOrganizationID, SiteID: request.SiteID, WorkOrderID: request.WorkOrderID,
		Action: request.Action, PolicyRevision: facts.PolicyRevision, ReasonCode: workorderauth.ReasonDenyPrincipal,
		DecidedAt: now.UTC().Format(time.RFC3339Nano),
	}
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return decision, nil
	}
	decision.PrincipalID = facts.Principal.ID
	membershipActive, _ := membershipState(facts.Memberships, request.ActingOrganizationID, now)
	if !membershipActive {
		decision.ReasonCode = workorderauth.ReasonDenyMembership
		return decision, nil
	}
	allowFound := false
	for _, permission := range facts.Permissions {
		if permission.Status != FactStatusActive || !factEffective(permission.ValidFrom, permission.ValidTo, now) ||
			permission.OrganizationID != request.ActingOrganizationID || permission.SiteID != request.SiteID || permission.Action != request.Action {
			continue
		}
		if permission.Effect == BindingEffectDeny {
			decision.ReasonCode = workorderauth.ReasonDenyExplicit
			return decision, nil
		}
		if permission.Effect == BindingEffectAllow {
			allowFound = true
		}
	}
	if !allowFound {
		decision.ReasonCode = workorderauth.ReasonDenyScope
		return decision, nil
	}
	decision.Allowed = true
	decision.ReasonCode = workorderauth.ReasonAllowExactScope
	return decision, nil
}

func workOrderCapabilityAllowed(facts WorkOrderAuthorizationFacts, now time.Time, actingOrganizationID string, action workorderauth.Action) bool {
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

func cloneWorkOrderAuthorizationFacts(value WorkOrderAuthorizationFacts) WorkOrderAuthorizationFacts {
	cloned := value
	cloned.Memberships = append([]OrganizationMembership(nil), value.Memberships...)
	cloned.Permissions = append([]WorkOrderPermission(nil), value.Permissions...)
	return cloned
}
