package iam

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type CommandPermission struct {
	OrganizationID     string
	SiteID             string
	DeviceID           string
	Capability         commandmodel.Capability
	CapabilityRevision string
	Purpose            commandmodel.AuthorizationPurpose
	MaximumRisk        commandmodel.RiskLevel
	Effect             BindingEffect
	Status             FactStatus
	ValidFrom          time.Time
	ValidTo            *time.Time
}

type CommandAuthorizationFacts struct {
	Found                       bool
	PolicyRevision              string
	EmergencyRevocationRevision uint64
	Principal                   PrincipalRecord
	Memberships                 []OrganizationMembership
	Permissions                 []CommandPermission
}

type CommandAuthorizationStore interface {
	LookupCommandAuthorization(context.Context, AuthorizationLookup) (CommandAuthorizationFacts, error)
}

type staticCommandAuthorizationStore struct {
	policyRevision              string
	emergencyRevocationRevision uint64
	facts                       map[string]CommandAuthorizationFacts
}

func newStaticCommandAuthorizationStore(policyRevision string, emergencyRevocationRevision uint64, facts []CommandAuthorizationFacts) CommandAuthorizationStore {
	indexed := make(map[string]CommandAuthorizationFacts, len(facts))
	for _, value := range facts {
		value.Found = true
		value.PolicyRevision = policyRevision
		value.EmergencyRevocationRevision = emergencyRevocationRevision
		indexed[authorizationIdentityKey(value.Principal.SubjectIssuer, value.Principal.Subject)] = cloneCommandAuthorizationFacts(value)
	}
	return &staticCommandAuthorizationStore{
		policyRevision: policyRevision, emergencyRevocationRevision: emergencyRevocationRevision, facts: indexed,
	}
}

func newDenyAllCommandAuthorizationStore(policyRevision string) CommandAuthorizationStore {
	return &staticCommandAuthorizationStore{policyRevision: policyRevision, facts: map[string]CommandAuthorizationFacts{}}
}

func (store *staticCommandAuthorizationStore) LookupCommandAuthorization(_ context.Context, lookup AuthorizationLookup) (CommandAuthorizationFacts, error) {
	value, ok := store.facts[authorizationIdentityKey(lookup.SubjectIssuer, lookup.Subject)]
	if !ok {
		return CommandAuthorizationFacts{PolicyRevision: store.policyRevision, EmergencyRevocationRevision: store.emergencyRevocationRevision}, nil
	}
	return cloneCommandAuthorizationFacts(value), nil
}

func evaluateCommandAuthorization(ctx context.Context, store CommandAuthorizationStore, now time.Time, subjectIssuer, subject string, request commandauth.DecisionRequest) (commandauth.Decision, error) {
	facts, err := store.LookupCommandAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer: subjectIssuer, Subject: subject, ActingOrganizationID: request.ActingOrganizationID,
	})
	if err != nil {
		return commandauth.Decision{}, err
	}
	decision := commandauth.Decision{
		SubjectIssuer: subjectIssuer, Subject: subject,
		ActingOrganizationID: request.ActingOrganizationID, SiteID: request.SiteID, DeviceID: request.DeviceID,
		Capability: request.Capability, CapabilityRevision: request.CapabilityRevision, Purpose: request.Purpose,
		PolicyRevision: facts.PolicyRevision, EmergencyRevocationRevision: facts.EmergencyRevocationRevision,
		ReasonCode: commandauth.ReasonDenyPrincipal, DecidedAt: commandauth.FormatInstant(now),
	}
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return decision, nil
	}
	decision.PrincipalID = facts.Principal.ID
	membershipActive, _ := membershipState(facts.Memberships, request.ActingOrganizationID, now)
	if !membershipActive {
		decision.ReasonCode = commandauth.ReasonDenyMembership
		return decision, nil
	}

	allowFound := false
	maximumRisk := commandmodel.RiskLevel("")
	for _, permission := range facts.Permissions {
		if permission.Status != FactStatusActive || !factEffective(permission.ValidFrom, permission.ValidTo, now) {
			continue
		}
		if permission.OrganizationID != request.ActingOrganizationID || permission.SiteID != request.SiteID || permission.DeviceID != request.DeviceID {
			continue
		}
		if permission.Capability != request.Capability || permission.CapabilityRevision != request.CapabilityRevision {
			continue
		}
		if permission.Purpose != request.Purpose {
			continue
		}
		if permission.Effect == BindingEffectDeny {
			decision.ReasonCode = commandauth.ReasonDenyExplicit
			return decision, nil
		}
		if permission.Effect != BindingEffectAllow {
			continue
		}
		allowFound = true
		if maximumRisk == "" || commandRiskOrdinal(permission.MaximumRisk) > commandRiskOrdinal(maximumRisk) {
			maximumRisk = permission.MaximumRisk
		}
	}
	if !allowFound || maximumRisk == "" {
		decision.ReasonCode = commandauth.ReasonDenyScope
		return decision, nil
	}
	decision.Allowed = true
	decision.MaximumRisk = maximumRisk
	decision.ReasonCode = commandauth.ReasonAllowExactCapability
	return decision, nil
}

func commandRiskOrdinal(value commandmodel.RiskLevel) int {
	switch value {
	case commandmodel.RiskLow:
		return 1
	case commandmodel.RiskMedium:
		return 2
	case commandmodel.RiskHigh:
		return 3
	case commandmodel.RiskCritical:
		return 4
	default:
		return 99
	}
}

func cloneCommandAuthorizationFacts(value CommandAuthorizationFacts) CommandAuthorizationFacts {
	cloned := value
	cloned.Memberships = append([]OrganizationMembership(nil), value.Memberships...)
	cloned.Permissions = append([]CommandPermission(nil), value.Permissions...)
	return cloned
}
