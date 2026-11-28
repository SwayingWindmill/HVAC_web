package iam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
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
	identitycontext.CapabilityAlarmList:          {string(alarmmodel.ActionRead)},
	identitycontext.CapabilityAlarmRead:          {string(alarmmodel.ActionRead)},
	identitycontext.CapabilityAlarmAssign:        {string(alarmmodel.ActionAssign)},
	identitycontext.CapabilityWorkOrderList:      {string(workordermodel.ActionList)},
	identitycontext.CapabilityWorkOrderRead:      {string(workordermodel.ActionRead)},
	identitycontext.CapabilityWorkOrderCreate:    {string(workordermodel.ActionCreate)},
	identitycontext.CapabilityWorkOrderAssign:    {string(workordermodel.ActionAssign)},
	identitycontext.CapabilityWorkOrderLifecycle: {string(workordermodel.ActionPlan), string(workordermodel.ActionStart), string(workordermodel.ActionBlock), string(workordermodel.ActionResume), string(workordermodel.ActionComplete), string(workordermodel.ActionCancel), string(workordermodel.ActionReopen)},
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
