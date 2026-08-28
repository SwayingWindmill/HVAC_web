package iam

import (
	"context"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

type TelemetryDevice struct {
	ID       string
	TenantID string
	SiteID   string
	Status   FactStatus
}

type TelemetryScopeBinding struct {
	TenantID string
	SiteID               string
	DeviceID             string
	Actions              []telemetryauth.Action
	Effect               BindingEffect
	Status               FactStatus
	ValidFrom            time.Time
	ValidTo              *time.Time
}

type TelemetryKeyBinding struct {
	TenantID string
	DeviceID             string
	Key                  string
	Actions              []telemetryauth.Action
	Effect               BindingEffect
	Status               FactStatus
	ValidFrom            time.Time
	ValidTo              *time.Time
}

type TelemetryAuthorizationFacts struct {
	Found          bool
	PolicyRevision string
	Principal      PrincipalRecord
	Memberships    []TenantMembership
	RoleBindings   []RoleBinding
	SiteBindings   []SiteBinding
	ExplicitDenies []ExplicitDeny
	Devices        []TelemetryDevice
	ScopeBindings  []TelemetryScopeBinding
	KeyBindings    []TelemetryKeyBinding
}

type TelemetryAuthorizationLookup struct {
	SubjectIssuer        string
	Subject              string
	TenantID string
	Targets              []telemetryauth.Target
}

type TelemetryAuthorizationStore interface {
	LookupTelemetryAuthorization(context.Context, TelemetryAuthorizationLookup) (TelemetryAuthorizationFacts, error)
	LookupPrincipalTelemetryCapabilities(context.Context, PrincipalCapabilityLookup) (TelemetryAuthorizationFacts, error)
}

type staticTelemetryAuthorizationStore struct {
	facts TelemetryAuthorizationFacts
}

func newStaticTelemetryAuthorizationStore(facts TelemetryAuthorizationFacts) TelemetryAuthorizationStore {
	facts.Found = true
	return &staticTelemetryAuthorizationStore{facts: facts}
}

func newDenyAllTelemetryAuthorizationStore(policyRevision string) TelemetryAuthorizationStore {
	return &staticTelemetryAuthorizationStore{facts: TelemetryAuthorizationFacts{PolicyRevision: policyRevision}}
}

func (store *staticTelemetryAuthorizationStore) LookupTelemetryAuthorization(_ context.Context, lookup TelemetryAuthorizationLookup) (TelemetryAuthorizationFacts, error) {
	facts := store.facts
	if facts.Principal.SubjectIssuer != lookup.SubjectIssuer || facts.Principal.Subject != lookup.Subject {
		return TelemetryAuthorizationFacts{PolicyRevision: facts.PolicyRevision}, nil
	}
	return cloneTelemetryAuthorizationFacts(facts), nil
}

func (store *staticTelemetryAuthorizationStore) LookupPrincipalTelemetryCapabilities(_ context.Context, lookup PrincipalCapabilityLookup) (TelemetryAuthorizationFacts, error) {
	facts := store.facts
	if facts.Principal.SubjectIssuer != lookup.SubjectIssuer || facts.Principal.Subject != lookup.Subject {
		return TelemetryAuthorizationFacts{PolicyRevision: facts.PolicyRevision}, nil
	}
	return cloneTelemetryAuthorizationFacts(facts), nil
}

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
	membershipActive, _ := tenantMembershipState(facts.Memberships, request.TenantID, now)
	if !membershipActive {
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
		actionAllowed, actionDenied := telemetryActionScope(facts.RoleBindings, facts.SiteBindings, facts.ExplicitDenies, now, request.TenantID, device, request.Action)
		if actionDenied || !actionAllowed {
			return denyTelemetryDecision(decision, telemetryauth.ReasonResourceNotFound), nil
		}
		allowed, denied := telemetryDeviceScope(facts.ScopeBindings, now, request.TenantID, device, request.Action)
		if denied || !allowed {
			return denyTelemetryDecision(decision, telemetryauth.ReasonResourceNotFound), nil
		}
		for _, key := range target.Keys {
			keyAllowed, keyDenied := telemetryKeyScope(facts.KeyBindings, now, request.TenantID, device.ID, key, request.Action)
			if keyDenied || !keyAllowed {
				return denyTelemetryDecision(decision, telemetryauth.ReasonTelemetryKeyInvalid), nil
			}
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

func telemetryActionScope(roleBindings []RoleBinding, siteBindings []SiteBinding, explicitDenies []ExplicitDeny, now time.Time, tenantID string, device TelemetryDevice, action telemetryauth.Action) (bool, bool) {
	roleAllowed := false
	roleDenied := false
	siteAllowed := false
	for _, binding := range roleBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != tenantID || !telemetryRegistryActionsAllow(binding.Actions, action) {
			continue
		}
		if binding.SiteID != "" && binding.SiteID != device.SiteID {
			continue
		}
		if binding.Effect == BindingEffectDeny {
			roleDenied = true
		} else if binding.Effect == BindingEffectAllow {
			roleAllowed = true
			if binding.SiteID == device.SiteID {
				siteAllowed = true
			}
		}
	}

	siteDenied := false
	for _, binding := range siteBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != tenantID || binding.SiteID != device.SiteID || !telemetryRegistryActionsAllow(binding.Actions, action) {
			continue
		}
		if binding.Effect == BindingEffectDeny {
			siteDenied = true
		} else if binding.Effect == BindingEffectAllow {
			siteAllowed = true
		}
	}

	explicitDenied := false
	for _, deny := range explicitDenies {
		if deny.Status != FactStatusActive || !factEffective(deny.ValidFrom, deny.ValidTo, now) || !telemetryRegistryActionsAllow(deny.Actions, action) {
			continue
		}
		if deny.TenantID != "" && deny.TenantID != tenantID {
			continue
		}
		if deny.SiteID != "" && deny.SiteID != device.SiteID {
			continue
		}
		explicitDenied = true
	}

	return roleAllowed || siteAllowed, roleDenied || siteDenied || explicitDenied
}

func telemetryRegistryActionsAllow(actions []registryauth.Action, requested telemetryauth.Action) bool {
	for _, action := range actions {
		if string(action) == string(requested) {
			return true
		}
	}
	return false
}

func denyTelemetryDecision(decision telemetryauth.Decision, reason telemetryauth.ReasonCode) telemetryauth.Decision {
	decision.Allowed = false
	decision.Targets = []telemetryauth.AuthorizedTarget{}
	decision.ScopeDigest = ""
	decision.ReasonCode = reason
	return decision
}

func telemetryDeviceScope(bindings []TelemetryScopeBinding, now time.Time, tenantID string, device TelemetryDevice, action telemetryauth.Action) (bool, bool) {
	allowed := false
	denied := false
	for _, binding := range bindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != tenantID || !telemetryActionsAllow(binding.Actions, action) {
			continue
		}
		if binding.SiteID != device.SiteID || binding.DeviceID != "" && binding.DeviceID != device.ID {
			continue
		}
		if binding.Effect == BindingEffectDeny {
			denied = true
		} else if binding.Effect == BindingEffectAllow {
			allowed = true
		}
	}
	return allowed, denied
}

func telemetryKeyScope(bindings []TelemetryKeyBinding, now time.Time, tenantID, deviceID, key string, action telemetryauth.Action) (bool, bool) {
	allowed := false
	denied := false
	for _, binding := range bindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != tenantID || binding.DeviceID != deviceID || binding.Key != key || !telemetryActionsAllow(binding.Actions, action) {
			continue
		}
		if binding.Effect == BindingEffectDeny {
			denied = true
		} else if binding.Effect == BindingEffectAllow {
			allowed = true
		}
	}
	return allowed, denied
}

func telemetryActionsAllow(actions []telemetryauth.Action, requested telemetryauth.Action) bool {
	for _, action := range actions {
		if action == requested {
			return true
		}
	}
	return false
}

func cloneTelemetryAuthorizationFacts(value TelemetryAuthorizationFacts) TelemetryAuthorizationFacts {
	copyValue := value
	copyValue.Memberships = append([]TenantMembership(nil), value.Memberships...)
	copyValue.RoleBindings = append([]RoleBinding(nil), value.RoleBindings...)
	copyValue.SiteBindings = append([]SiteBinding(nil), value.SiteBindings...)
	copyValue.ExplicitDenies = append([]ExplicitDeny(nil), value.ExplicitDenies...)
	copyValue.Devices = append([]TelemetryDevice(nil), value.Devices...)
	copyValue.ScopeBindings = append([]TelemetryScopeBinding(nil), value.ScopeBindings...)
	copyValue.KeyBindings = append([]TelemetryKeyBinding(nil), value.KeyBindings...)
	for index := range copyValue.RoleBindings {
		copyValue.RoleBindings[index].Actions = append([]registryauth.Action(nil), value.RoleBindings[index].Actions...)
	}
	for index := range copyValue.SiteBindings {
		copyValue.SiteBindings[index].Actions = append([]registryauth.Action(nil), value.SiteBindings[index].Actions...)
	}
	for index := range copyValue.ExplicitDenies {
		copyValue.ExplicitDenies[index].Actions = append([]registryauth.Action(nil), value.ExplicitDenies[index].Actions...)
	}
	for index := range copyValue.ScopeBindings {
		copyValue.ScopeBindings[index].Actions = append([]telemetryauth.Action(nil), value.ScopeBindings[index].Actions...)
	}
	for index := range copyValue.KeyBindings {
		copyValue.KeyBindings[index].Actions = append([]telemetryauth.Action(nil), value.KeyBindings[index].Actions...)
	}
	sort.Slice(copyValue.Devices, func(left, right int) bool { return copyValue.Devices[left].ID < copyValue.Devices[right].ID })
	return copyValue
}
