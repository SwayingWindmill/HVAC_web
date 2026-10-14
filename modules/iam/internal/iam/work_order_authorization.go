package iam

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

type WorkOrderOwnershipTarget struct {
	TenantID   string
	SiteID     string
	TargetType string
	TargetID   string
	Effect     BindingEffect
	Status     FactStatus
	ValidFrom  time.Time
	ValidTo    *time.Time
}

// WorkOrderAuthorizationFacts adds what assigning ownership needs: the Team targets the
// Principal may assign to, and the access of the person a work order would be assigned to.
type WorkOrderAuthorizationFacts struct {
	AuthorizationFacts
	Targets  []WorkOrderOwnershipTarget
	Assignee AuthorizationFacts
}

type WorkOrderAuthorizationLookup struct {
	AuthorizationLookup
	AssigneePrincipalID string
}

type WorkOrderAuthorizationStore interface {
	LookupWorkOrderAuthorization(context.Context, WorkOrderAuthorizationLookup) (WorkOrderAuthorizationFacts, error)
}

type staticWorkOrderAuthorizationStore struct {
	policyRevision string
	facts          map[string]WorkOrderAuthorizationFacts
	byPrincipalID  map[string]AuthorizationFacts
}

func newStaticWorkOrderAuthorizationStore(policyRevision string, facts []WorkOrderAuthorizationFacts) WorkOrderAuthorizationStore {
	indexed := make(map[string]WorkOrderAuthorizationFacts, len(facts))
	byPrincipalID := make(map[string]AuthorizationFacts, len(facts))
	for _, value := range facts {
		value.Found = true
		value.PolicyRevision = policyRevision
		indexed[authorizationIdentityKey(value.Principal.SubjectIssuer, value.Principal.Subject)] = cloneWorkOrderAuthorizationFacts(value)
		byPrincipalID[value.Principal.ID] = cloneAuthorizationFacts(value.AuthorizationFacts)
	}
	return &staticWorkOrderAuthorizationStore{policyRevision: policyRevision, facts: indexed, byPrincipalID: byPrincipalID}
}

func newDenyAllWorkOrderAuthorizationStore(policyRevision string) WorkOrderAuthorizationStore {
	return &staticWorkOrderAuthorizationStore{policyRevision: policyRevision, facts: map[string]WorkOrderAuthorizationFacts{}}
}

func (store *staticWorkOrderAuthorizationStore) LookupWorkOrderAuthorization(_ context.Context, lookup WorkOrderAuthorizationLookup) (WorkOrderAuthorizationFacts, error) {
	value, ok := store.facts[authorizationIdentityKey(lookup.SubjectIssuer, lookup.Subject)]
	if !ok {
		return WorkOrderAuthorizationFacts{AuthorizationFacts: AuthorizationFacts{PolicyRevision: store.policyRevision}}, nil
	}
	value = cloneWorkOrderAuthorizationFacts(value)
	if lookup.AssigneePrincipalID != "" {
		value.Assignee = cloneAuthorizationFacts(store.byPrincipalID[lookup.AssigneePrincipalID])
	}
	return value, nil
}

func evaluateWorkOrderAuthorization(ctx context.Context, store WorkOrderAuthorizationStore, now time.Time, subjectIssuer, subject string, request workorderauth.DecisionRequest) (workorderauth.Decision, error) {
	lookup := WorkOrderAuthorizationLookup{AuthorizationLookup: AuthorizationLookup{
		SubjectIssuer: subjectIssuer, Subject: subject, TenantID: request.TenantID,
	}}
	assigning := request.Action == workorderauth.ActionCreate || request.Action == workorderauth.ActionAssign
	if assigning && request.AssigneeID != nil {
		lookup.AssigneePrincipalID = *request.AssigneeID
	}
	facts, err := store.LookupWorkOrderAuthorization(ctx, lookup)
	if err != nil {
		return workorderauth.Decision{}, err
	}
	decision := workorderauth.Decision{
		SubjectIssuer: subjectIssuer, Subject: subject,
		TenantID: request.TenantID, SiteID: request.SiteID, WorkOrderID: request.WorkOrderID,
		AssigneeID: request.AssigneeID, TeamID: request.TeamID,
		Action: request.Action, PolicyRevision: facts.PolicyRevision, ReasonCode: workorderauth.ReasonDenyPrincipal,
		DecidedAt: now.UTC().Format(time.RFC3339Nano),
	}
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return decision, nil
	}
	decision.PrincipalID = facts.Principal.ID
	if active, _ := facts.member(request.TenantID, now); !active {
		decision.ReasonCode = workorderauth.ReasonDenyMembership
		return decision, nil
	}
	allowed, denied := facts.grantsAt(request.TenantID, request.SiteID, string(request.Action), now)
	if denied {
		decision.ReasonCode = workorderauth.ReasonDenyExplicit
		return decision, nil
	}
	if !allowed {
		decision.ReasonCode = workorderauth.ReasonDenyScope
		return decision, nil
	}
	if assigning {
		// A person may own a work order on a Site where they may start work.
		if request.AssigneeID != nil {
			if active, _ := facts.Assignee.member(request.TenantID, now); !active {
				decision.ReasonCode = workorderauth.ReasonDenyScope
				return decision, nil
			}
			startAllowed, startDenied := facts.Assignee.grantsAt(request.TenantID, request.SiteID, string(workorderauth.ActionStart), now)
			if !startAllowed || startDenied {
				decision.ReasonCode = workorderauth.ReasonDenyScope
				return decision, nil
			}
		}
		if request.TeamID != nil {
			if reason, ok := teamTargetAllowed(facts.Targets, request, now); !ok {
				decision.ReasonCode = reason
				return decision, nil
			}
		}
	}
	decision.Allowed = true
	decision.ReasonCode = workorderauth.ReasonAllowExactScope
	return decision, nil
}

func teamTargetAllowed(targets []WorkOrderOwnershipTarget, request workorderauth.DecisionRequest, now time.Time) (workorderauth.ReasonCode, bool) {
	allowed := false
	for _, target := range targets {
		if target.Status != FactStatusActive || !factEffective(target.ValidFrom, target.ValidTo, now) ||
			target.TenantID != request.TenantID || target.SiteID != request.SiteID ||
			target.TargetType != "TEAM" || target.TargetID != *request.TeamID {
			continue
		}
		if target.Effect == BindingEffectDeny {
			return workorderauth.ReasonDenyExplicit, false
		}
		allowed = allowed || target.Effect == BindingEffectAllow
	}
	if !allowed {
		return workorderauth.ReasonDenyScope, false
	}
	return "", true
}

func cloneWorkOrderAuthorizationFacts(value WorkOrderAuthorizationFacts) WorkOrderAuthorizationFacts {
	cloned := value
	cloned.AuthorizationFacts = cloneAuthorizationFacts(value.AuthorizationFacts)
	cloned.Targets = append([]WorkOrderOwnershipTarget(nil), value.Targets...)
	cloned.Assignee = cloneAuthorizationFacts(value.Assignee)
	return cloned
}
