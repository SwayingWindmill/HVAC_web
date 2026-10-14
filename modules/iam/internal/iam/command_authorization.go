package iam

import (
	"context"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

// commandRiskLevels orders the risk ceilings a Role can grant, lowest first.
var commandRiskLevels = []commandmodel.RiskLevel{commandmodel.RiskLow, commandmodel.RiskMedium, commandmodel.RiskHigh, commandmodel.RiskCritical}

// commandCapability names the capability that allows submitting or approving commands up to a
// risk, for example "command.submit.medium".
func commandCapability(purpose commandmodel.AuthorizationPurpose, risk commandmodel.RiskLevel) string {
	verb := "submit"
	if purpose == commandmodel.AuthorizationCommandApprove {
		verb = "approve"
	}
	return "command." + verb + "." + strings.ToLower(string(risk))
}

func commandCapabilityValid(value string) bool {
	for _, purpose := range []commandmodel.AuthorizationPurpose{commandmodel.AuthorizationCommandSubmit, commandmodel.AuthorizationCommandApprove} {
		for _, risk := range commandRiskLevels {
			if value == commandCapability(purpose, risk) {
				return true
			}
		}
	}
	return false
}

// CommandAuthorizationFacts adds the Site the target Device belongs to ("" when IAM cannot see it).
type CommandAuthorizationFacts struct {
	AuthorizationFacts
	EmergencyRevocationRevision uint64
	DeviceSiteID                string
}

type CommandAuthorizationLookup struct {
	AuthorizationLookup
	DeviceID string
}

type CommandAuthorizationStore interface {
	LookupCommandAuthorization(context.Context, CommandAuthorizationLookup) (CommandAuthorizationFacts, error)
}

type staticCommandAuthorizationStore struct {
	policyRevision              string
	emergencyRevocationRevision uint64
	facts                       map[string]AuthorizationFacts
	deviceSites                 map[string]string
}

func newStaticCommandAuthorizationStore(policyRevision string, emergencyRevocationRevision uint64, deviceSites map[string]string, facts []AuthorizationFacts) CommandAuthorizationStore {
	return &staticCommandAuthorizationStore{
		policyRevision: policyRevision, emergencyRevocationRevision: emergencyRevocationRevision,
		facts: indexAuthorizationFacts(policyRevision, facts), deviceSites: deviceSites,
	}
}

func newDenyAllCommandAuthorizationStore(policyRevision string) CommandAuthorizationStore {
	return &staticCommandAuthorizationStore{policyRevision: policyRevision, facts: map[string]AuthorizationFacts{}}
}

func (store *staticCommandAuthorizationStore) LookupCommandAuthorization(_ context.Context, lookup CommandAuthorizationLookup) (CommandAuthorizationFacts, error) {
	facts := CommandAuthorizationFacts{
		AuthorizationFacts:          AuthorizationFacts{PolicyRevision: store.policyRevision},
		EmergencyRevocationRevision: store.emergencyRevocationRevision,
		DeviceSiteID:                store.deviceSites[lookup.DeviceID],
	}
	if value, ok := store.facts[authorizationIdentityKey(lookup.SubjectIssuer, lookup.Subject)]; ok {
		facts.AuthorizationFacts = cloneAuthorizationFacts(value)
	}
	return facts, nil
}

// evaluateCommandAuthorization allows a command purpose on a Device of the requested Site; the
// decision's maximum risk is the highest ceiling the Principal holds for that purpose there.
func evaluateCommandAuthorization(ctx context.Context, store CommandAuthorizationStore, now time.Time, subjectIssuer, subject string, request commandauth.DecisionRequest) (commandauth.Decision, error) {
	facts, err := store.LookupCommandAuthorization(ctx, CommandAuthorizationLookup{
		AuthorizationLookup: AuthorizationLookup{SubjectIssuer: subjectIssuer, Subject: subject, TenantID: request.TenantID},
		DeviceID:            request.DeviceID,
	})
	if err != nil {
		return commandauth.Decision{}, err
	}
	decision := commandauth.Decision{
		SubjectIssuer: subjectIssuer, Subject: subject,
		TenantID: request.TenantID, SiteID: request.SiteID, DeviceID: request.DeviceID,
		Capability: request.Capability, CapabilityRevision: request.CapabilityRevision, Purpose: request.Purpose,
		PolicyRevision: facts.PolicyRevision, EmergencyRevocationRevision: facts.EmergencyRevocationRevision,
		ReasonCode: commandauth.ReasonDenyPrincipal, DecidedAt: commandauth.FormatInstant(now),
	}
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return decision, nil
	}
	decision.PrincipalID = facts.Principal.ID
	if active, _ := facts.member(request.TenantID, now); !active {
		decision.ReasonCode = commandauth.ReasonDenyMembership
		return decision, nil
	}
	if facts.DeviceSiteID == "" || facts.DeviceSiteID != request.SiteID {
		decision.ReasonCode = commandauth.ReasonDenyScope
		return decision, nil
	}
	for index := len(commandRiskLevels) - 1; index >= 0; index-- {
		risk := commandRiskLevels[index]
		allowed, denied := facts.grantsAt(request.TenantID, request.SiteID, commandCapability(request.Purpose, risk), now)
		if denied {
			decision.ReasonCode = commandauth.ReasonDenyExplicit
			return decision, nil
		}
		if allowed {
			decision.Allowed = true
			decision.MaximumRisk = risk
			decision.ReasonCode = commandauth.ReasonAllowExactCapability
			return decision, nil
		}
	}
	decision.ReasonCode = commandauth.ReasonDenyScope
	return decision, nil
}
