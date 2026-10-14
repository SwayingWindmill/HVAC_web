package iam

import (
	"context"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

type TelemetryDevice struct {
	ID       string
	TenantID string
	SiteID   string
	Status   FactStatus
}

// TelemetryAuthorizationFacts adds the requested Devices to the Principal's access, so each
// target can be checked against its own Site.
type TelemetryAuthorizationFacts struct {
	AuthorizationFacts
	Devices []TelemetryDevice
}

type TelemetryAuthorizationLookup struct {
	SubjectIssuer string
	Subject       string
	TenantID      string
	Targets       []telemetryauth.Target
}

type TelemetryAuthorizationStore interface {
	LookupTelemetryAuthorization(context.Context, TelemetryAuthorizationLookup) (TelemetryAuthorizationFacts, error)
}

type staticTelemetryAuthorizationStore struct {
	facts TelemetryAuthorizationFacts
}

func newStaticTelemetryAuthorizationStore(facts TelemetryAuthorizationFacts) TelemetryAuthorizationStore {
	facts.Found = true
	return &staticTelemetryAuthorizationStore{facts: facts}
}

func newDenyAllTelemetryAuthorizationStore(policyRevision string) TelemetryAuthorizationStore {
	return &staticTelemetryAuthorizationStore{facts: TelemetryAuthorizationFacts{AuthorizationFacts: AuthorizationFacts{PolicyRevision: policyRevision}}}
}

func (store *staticTelemetryAuthorizationStore) LookupTelemetryAuthorization(_ context.Context, lookup TelemetryAuthorizationLookup) (TelemetryAuthorizationFacts, error) {
	facts := store.facts
	if facts.Principal.SubjectIssuer != lookup.SubjectIssuer || facts.Principal.Subject != lookup.Subject {
		return TelemetryAuthorizationFacts{AuthorizationFacts: AuthorizationFacts{PolicyRevision: facts.PolicyRevision}}, nil
	}
	return cloneTelemetryAuthorizationFacts(facts), nil
}

// evaluateTelemetryAuthorization allows a request only when every target Device is visible,
// active and on a Site where the Principal holds the requested telemetry action.
func evaluateTelemetryAuthorization(ctx context.Context, store TelemetryAuthorizationStore, now time.Time, subjectIssuer, subject string, request telemetryauth.DecisionRequest) (telemetryauth.Decision, error) {
	canonicalTargets, err := telemetryauth.CanonicalTargets(request.Targets)
	if err != nil {
		return telemetryauth.Decision{}, err
	}
	facts, err := store.LookupTelemetryAuthorization(ctx, TelemetryAuthorizationLookup{
		SubjectIssuer: subjectIssuer, Subject: subject, TenantID: request.TenantID, Targets: canonicalTargets,
	})
	if err != nil {
		return telemetryauth.Decision{}, err
	}
	decision := telemetryauth.Decision{
		SubjectIssuer: subjectIssuer, Subject: subject, TenantID: request.TenantID,
		Action: request.Action, Targets: []telemetryauth.AuthorizedTarget{}, PolicyRevision: facts.PolicyRevision, DecidedAt: formatInstant(now),
	}
	if !facts.Found {
		decision.ReasonCode = telemetryauth.ReasonDenyPrincipalNotFound
		return decision, nil
	}
	decision.PrincipalID = facts.Principal.ID
	if facts.Principal.Status != FactStatusActive {
		decision.ReasonCode = telemetryauth.ReasonDenyPrincipalInactive
		return decision, nil
	}
	if active, _ := facts.member(request.TenantID, now); !active {
		decision.ReasonCode = telemetryauth.ReasonDenyMembership
		return decision, nil
	}

	devices := make(map[string]TelemetryDevice, len(facts.Devices))
	for _, device := range facts.Devices {
		devices[device.ID] = device
	}
	for _, target := range canonicalTargets {
		device, visible := devices[target.DeviceID]
		if !visible || device.TenantID != request.TenantID || device.Status != FactStatusActive {
			return denyTelemetryDecision(decision, telemetryauth.ReasonResourceNotFound), nil
		}
		// A Device on a Site the Principal cannot read is reported as absent, not forbidden.
		if allowed, denied := facts.grantsAt(request.TenantID, device.SiteID, string(request.Action), now); !allowed || denied {
			return denyTelemetryDecision(decision, telemetryauth.ReasonResourceNotFound), nil
		}
		decision.Targets = append(decision.Targets, telemetryauth.AuthorizedTarget{
			TenantID: device.TenantID, SiteID: device.SiteID, DeviceID: device.ID, Keys: append([]string(nil), target.Keys...),
		})
	}
	digest, err := telemetryauth.ScopeDigest(request.Action, request.TenantID, canonicalTargets)
	if err != nil {
		return telemetryauth.Decision{}, err
	}
	decision.Allowed = true
	decision.ScopeDigest = digest
	decision.ReasonCode = telemetryauth.ReasonAllowExactScope
	return decision, nil
}

func denyTelemetryDecision(decision telemetryauth.Decision, reason telemetryauth.ReasonCode) telemetryauth.Decision {
	decision.Allowed = false
	decision.Targets = []telemetryauth.AuthorizedTarget{}
	decision.ScopeDigest = ""
	decision.ReasonCode = reason
	return decision
}

func cloneTelemetryAuthorizationFacts(value TelemetryAuthorizationFacts) TelemetryAuthorizationFacts {
	copyValue := value
	copyValue.AuthorizationFacts = cloneAuthorizationFacts(value.AuthorizationFacts)
	copyValue.Devices = append([]TelemetryDevice(nil), value.Devices...)
	sort.Slice(copyValue.Devices, func(left, right int) bool { return copyValue.Devices[left].ID < copyValue.Devices[right].ID })
	return copyValue
}
