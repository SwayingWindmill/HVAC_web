package adapter

import (
	"context"
	"errors"
	"strings"
)

type testBindingAuthorizer struct {
	scopes   map[string]TopicScope
	children map[string]map[string]struct{}
}

func newTestBindingAuthorizer(scopes []GatewayScopeConfig) *testBindingAuthorizer {
	authorizer := &testBindingAuthorizer{scopes: make(map[string]TopicScope), children: make(map[string]map[string]struct{})}
	for _, scope := range scopes {
		gatewayID := strings.TrimSpace(scope.GatewayID)
		authorizer.scopes[gatewayID] = TopicScope{GatewayID: gatewayID, TenantID: strings.TrimSpace(scope.TenantID), SiteID: strings.TrimSpace(scope.SiteID)}
		authorizer.children[gatewayID] = map[string]struct{}{
			"METER-01": {}, "CHILLER-01": {}, "CHWP-01": {}, "CWP-01": {}, "CT-01": {},
			"POISON-01": {}, "GOOD-01": {}, "FIRST-01": {}, "SECOND-01": {}, "PARKED-01": {},
		}
	}
	return authorizer
}

func (authorizer *testBindingAuthorizer) AuthorizeGateway(_ context.Context, _ string, tenantID, siteID, gatewayExternalID string) error {
	scope, ok := authorizer.scopes[strings.TrimSpace(gatewayExternalID)]
	if !ok || scope.TenantID != strings.TrimSpace(tenantID) || scope.SiteID != strings.TrimSpace(siteID) {
		return errors.New("gateway binding not found")
	}
	return nil
}

func (authorizer *testBindingAuthorizer) AuthorizeGatewayChild(_ context.Context, _ string, gatewayExternalID, externalDeviceID string) error {
	children := authorizer.children[strings.TrimSpace(gatewayExternalID)]
	if _, ok := children[strings.TrimSpace(externalDeviceID)]; !ok {
		return errors.New("child binding not found")
	}
	return nil
}
