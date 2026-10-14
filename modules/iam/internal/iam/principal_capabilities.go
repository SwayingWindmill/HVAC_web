package iam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

type PrincipalCapabilityLookup struct {
	SubjectIssuer string
	Subject       string
	TenantID      string
}

type PrincipalCapabilityResolver interface {
	ResolvePrincipalCapabilities(context.Context, PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error)
}

type principalCapabilityResolver struct {
	store AuthorizationStore
	now   func() time.Time
}

func newPrincipalCapabilityResolver(store AuthorizationStore, now func() time.Time) PrincipalCapabilityResolver {
	return &principalCapabilityResolver{store: store, now: now}
}

// principalCapabilityActions maps a navigation capability to the stored capabilities that
// imply it; any one is enough. A capability not listed is implied by itself.
var principalCapabilityActions = map[identitycontext.Capability][]string{
	identitycontext.CapabilityAlarmList:          {string(alarmauth.ActionRead)},
	identitycontext.CapabilityAlarmRead:          {string(alarmauth.ActionRead)},
	identitycontext.CapabilityAlarmAssign:        {string(alarmauth.ActionAssign)},
	identitycontext.CapabilityWorkOrderList:      {string(workorderauth.ActionList)},
	identitycontext.CapabilityWorkOrderRead:      {string(workorderauth.ActionRead)},
	identitycontext.CapabilityWorkOrderCreate:    {string(workorderauth.ActionCreate)},
	identitycontext.CapabilityWorkOrderAssign:    {string(workorderauth.ActionAssign)},
	identitycontext.CapabilityWorkOrderLifecycle: {string(workorderauth.ActionPlan), string(workorderauth.ActionStart), string(workorderauth.ActionBlock), string(workorderauth.ActionResume), string(workorderauth.ActionComplete), string(workorderauth.ActionCancel), string(workorderauth.ActionReopen)},
}

// ResolvePrincipalCapabilities lists what the Principal can do anywhere in the Tenant: the union
// over its Sites, and Tenant-scoped capabilities from assignments to the whole Tenant.
func (resolver *principalCapabilityResolver) ResolvePrincipalCapabilities(ctx context.Context, lookup PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error) {
	now := resolver.now()
	facts, err := resolver.store.LookupRegistryAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer: lookup.SubjectIssuer,
		Subject:       lookup.Subject,
		TenantID:      lookup.TenantID,
		At:            now,
	})
	if err != nil {
		return identitycontext.EffectiveAuthorization{}, err
	}
	capabilities := []identitycontext.Capability{}
	for _, capability := range identitycontext.SupportedCapabilities() {
		actions, mapped := principalCapabilityActions[capability]
		if !mapped {
			actions = []string{string(capability)}
		}
		for _, action := range actions {
			if facts.grantsAnywhere(lookup.TenantID, action, now) {
				capabilities = append(capabilities, capability)
				break
			}
		}
	}
	digest := sha256.Sum256([]byte(facts.PolicyRevision))
	return identitycontext.EffectiveAuthorization{
		CapabilitySetVersion: identitycontext.CapabilitySetVersion,
		PolicyRevision:       "capability-v7:" + hex.EncodeToString(digest[:]),
		Capabilities:         capabilities,
	}, nil
}
