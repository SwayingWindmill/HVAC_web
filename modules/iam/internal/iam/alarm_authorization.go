package iam

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
)

// AlarmAuthorizationStore reads the Principal's access under the Alarm policy.
type AlarmAuthorizationStore interface {
	LookupAlarmAuthorization(context.Context, AuthorizationLookup) (AuthorizationFacts, error)
}

type staticAlarmAuthorizationStore struct {
	policyRevision string
	facts          map[string]AuthorizationFacts
}

func newStaticAlarmAuthorizationStore(policyRevision string, facts []AuthorizationFacts) AlarmAuthorizationStore {
	return &staticAlarmAuthorizationStore{policyRevision: policyRevision, facts: indexAuthorizationFacts(policyRevision, facts)}
}

func newDenyAllAlarmAuthorizationStore(policyRevision string) AlarmAuthorizationStore {
	return &staticAlarmAuthorizationStore{policyRevision: policyRevision, facts: map[string]AuthorizationFacts{}}
}

func (store *staticAlarmAuthorizationStore) LookupAlarmAuthorization(_ context.Context, lookup AuthorizationLookup) (AuthorizationFacts, error) {
	value, ok := store.facts[authorizationIdentityKey(lookup.SubjectIssuer, lookup.Subject)]
	if !ok {
		return AuthorizationFacts{PolicyRevision: store.policyRevision}, nil
	}
	return cloneAuthorizationFacts(value), nil
}

func evaluateAlarmAuthorization(ctx context.Context, store AlarmAuthorizationStore, now time.Time, subjectIssuer, subject string, request alarmauth.DecisionRequest) (alarmauth.Decision, error) {
	facts, err := store.LookupAlarmAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer: subjectIssuer, Subject: subject, TenantID: request.TenantID,
	})
	if err != nil {
		return alarmauth.Decision{}, err
	}
	decision := alarmauth.Decision{
		SubjectIssuer: subjectIssuer, Subject: subject,
		TenantID: request.TenantID, SiteID: request.SiteID, AlarmID: request.AlarmID,
		Action: request.Action, PolicyRevision: facts.PolicyRevision, ReasonCode: alarmauth.ReasonDenyPrincipal,
		DecidedAt: now.UTC().Format(time.RFC3339Nano),
	}
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return decision, nil
	}
	decision.PrincipalID = facts.Principal.ID
	if active, _ := facts.member(request.TenantID, now); !active {
		decision.ReasonCode = alarmauth.ReasonDenyMembership
		return decision, nil
	}
	allowed, denied := facts.grantsAt(request.TenantID, request.SiteID, string(request.Action), now)
	switch {
	case denied:
		decision.ReasonCode = alarmauth.ReasonDenyExplicit
	case !allowed:
		decision.ReasonCode = alarmauth.ReasonDenyScope
	default:
		decision.Allowed = true
		decision.ReasonCode = alarmauth.ReasonAllowExactScope
	}
	return decision, nil
}
