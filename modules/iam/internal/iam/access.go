package iam

import (
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

// RoleAssignment is the only authorization fact (ADR 0022): one Role held in one Tenant,
// across the whole Tenant (SiteID "") or on one Site.
type RoleAssignment struct {
	TenantID     string
	SiteID       string
	RoleKey      string
	Capabilities []string
	Status       FactStatus
	ValidFrom    time.Time
	ValidTo      *time.Time
}

// ExplicitDeny removes one capability across the whole Tenant (SiteID "") or on one Site.
type ExplicitDeny struct {
	TenantID   string
	SiteID     string
	Capability string
	ValidFrom  time.Time
	ValidTo    *time.Time
}

// tenantScopedCapabilities are granted only by an assignment to the whole Tenant.
var tenantScopedCapabilities = map[string]bool{
	string(identitycontext.CapabilityIAMAdmin):            true,
	string(identitycontext.CapabilityAPICredentialManage): true,
	string(identitycontext.CapabilitySessionRevoke):       true,
	string(identitycontext.CapabilityAuditRead):           true,
	string(identitycontext.CapabilityRuleManage):          true,
}

func capabilityTenantScoped(capability string) bool {
	return tenantScopedCapabilities[capability] || registryauth.Action(capability).TenantScoped()
}

// member reports whether the Principal is active and holds an effective membership of the Tenant.
func (facts AuthorizationFacts) member(tenantID string, now time.Time) (active, revoked bool) {
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		return false, false
	}
	return tenantMembershipState(facts.Memberships, tenantID, now)
}

// grantsAt decides one capability on one Site, or across the whole Tenant when siteID is "".
// A Site is covered by assignments to the whole Tenant and to that Site; a Tenant-scoped
// capability only by assignments to the whole Tenant. It does not check membership.
func (facts AuthorizationFacts) grantsAt(tenantID, siteID, capability string, now time.Time) (allowed, denied bool) {
	tenantScoped := capabilityTenantScoped(capability)
	if siteID != "" && tenantScoped {
		return false, false
	}
	granted := []registryauth.Action{}
	for _, assignment := range facts.Assignments {
		if assignment.TenantID != tenantID || assignment.Status != FactStatusActive || !factEffective(assignment.ValidFrom, assignment.ValidTo, now) {
			continue
		}
		if assignment.SiteID != "" && assignment.SiteID != siteID {
			continue
		}
		for _, value := range assignment.Capabilities {
			granted = append(granted, registryauth.Action(value))
		}
	}
	refused := []registryauth.Action{}
	for _, deny := range facts.ExplicitDenies {
		if deny.TenantID != tenantID || !factEffective(deny.ValidFrom, deny.ValidTo, now) {
			continue
		}
		if deny.SiteID != "" && deny.SiteID != siteID {
			continue
		}
		refused = append(refused, registryauth.Action(deny.Capability))
	}
	requested := registryauth.Action(capability)
	return actionsAllow(granted, requested), actionsDeny(refused, requested)
}

// candidateSites are the Sites a Tenant-wide question ranges over: the Tenant's own Sites
// plus any Site an assignment or deny names.
func (facts AuthorizationFacts) candidateSites(tenantID string) []string {
	sites := map[string]struct{}{}
	for _, siteID := range facts.TenantSiteIDs {
		if siteID != "" {
			sites[siteID] = struct{}{}
		}
	}
	for _, assignment := range facts.Assignments {
		if assignment.TenantID == tenantID && assignment.SiteID != "" {
			sites[assignment.SiteID] = struct{}{}
		}
	}
	for _, deny := range facts.ExplicitDenies {
		if deny.TenantID == tenantID && deny.SiteID != "" {
			sites[deny.SiteID] = struct{}{}
		}
	}
	return sortedKeys(sites)
}

// sitesGranting splits the candidate Sites into those where the capability applies and those
// where an explicit deny removed it.
func (facts AuthorizationFacts) sitesGranting(tenantID, capability string, now time.Time) (allowed, denied []string) {
	allowed, denied = []string{}, []string{}
	for _, siteID := range facts.candidateSites(tenantID) {
		siteAllowed, siteDenied := facts.grantsAt(tenantID, siteID, capability, now)
		switch {
		case siteDenied:
			denied = append(denied, siteID)
		case siteAllowed:
			allowed = append(allowed, siteID)
		}
	}
	return allowed, denied
}

// grantsAnywhere reports whether an active member holds the capability on at least one Site,
// or across the Tenant for a Tenant-scoped capability.
func (facts AuthorizationFacts) grantsAnywhere(tenantID, capability string, now time.Time) bool {
	if active, _ := facts.member(tenantID, now); !active {
		return false
	}
	if capabilityTenantScoped(capability) {
		allowed, denied := facts.grantsAt(tenantID, "", capability, now)
		return allowed && !denied
	}
	allowed, _ := facts.sitesGranting(tenantID, capability, now)
	return len(allowed) > 0
}

// grantsByTenantRole reports whether an assignment to the whole Tenant carries the capability.
func (facts AuthorizationFacts) grantsByTenantRole(tenantID, capability string, now time.Time) bool {
	granted := []registryauth.Action{}
	for _, assignment := range facts.Assignments {
		if assignment.TenantID != tenantID || assignment.SiteID != "" || assignment.Status != FactStatusActive || !factEffective(assignment.ValidFrom, assignment.ValidTo, now) {
			continue
		}
		for _, value := range assignment.Capabilities {
			granted = append(granted, registryauth.Action(value))
		}
	}
	return actionsAllow(granted, registryauth.Action(capability))
}
