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

// ResolveGatewayChild names each registered child after itself; the test Point registry
// knows every point except "unregistered_point".
func (authorizer *testBindingAuthorizer) ResolveGatewayChild(_ context.Context, _ string, gatewayExternalID, externalDeviceID string) (string, error) {
	children := authorizer.children[strings.TrimSpace(gatewayExternalID)]
	if _, ok := children[strings.TrimSpace(externalDeviceID)]; !ok {
		return "", nil
	}
	return testDeviceID, nil
}

func (authorizer *testBindingAuthorizer) ResolvePoint(_ context.Context, _ string, sourceKey string) (*ResolvedPoint, error) {
	if sourceKey == "unregistered_point" {
		return nil, nil
	}
	return &ResolvedPoint{PointID: testPointID, PointType: "TELEMETRY", ValueType: "NUMBER", PointRevision: 1}, nil
}

const (
	testDeviceID = "018f3e00-4000-7000-8000-000000000001"
	testPointID  = "018f3e00-5000-7000-8000-000000000001"
)
