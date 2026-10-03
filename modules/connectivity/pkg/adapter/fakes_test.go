package adapter

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

const (
	testTenantID  = "018f2d00-0000-7000-8000-000000000001"
	testSiteID    = "018f2e00-1000-7000-8000-000000000001"
	testSiteB     = "018f2e00-1000-7000-8000-000000000002"
	testGatewayA  = "018f3e00-4000-7000-8000-000000000100"
	testGatewayB  = "018f3e00-4000-7000-8000-000000000200"
	testDeviceID  = "018f3e00-4000-7000-8000-000000000001"
	testPointID   = "018f3e00-5000-7000-8000-000000000001"
	testMessageID = "0198a100-0000-7000-8000-000000000001"
)

// fakeIdentities is the Registry read port: two Gateways at two Sites, each naming the
// Device METER-01 whose Points are everything except "unregistered_point".
type fakeIdentities struct {
	mu                 sync.Mutex
	inactiveCredential map[string]bool
	quarantines        []UplinkQuarantine
	quarantineErr      error
}

func newFakeIdentities() *fakeIdentities {
	return &fakeIdentities{inactiveCredential: map[string]bool{}}
}

func (identities *fakeIdentities) ResolveGateway(_ context.Context, gatewayID string) (Gateway, error) {
	identities.mu.Lock()
	defer identities.mu.Unlock()
	site := map[string]string{testGatewayA: testSiteID, testGatewayB: testSiteB}[gatewayID]
	if site == "" {
		return Gateway{}, ErrGatewayUnknown
	}
	gateway := Gateway{ID: gatewayID, TenantID: testTenantID, SiteID: site}
	if identities.inactiveCredential[gatewayID] {
		return gateway, ErrGatewayCredentialInactive
	}
	return gateway, nil
}

func (identities *fakeIdentities) ResolveDevice(_ context.Context, _ Gateway, sourceKey string) (string, error) {
	if sourceKey != "METER-01" {
		return "", nil
	}
	return testDeviceID, nil
}

func (identities *fakeIdentities) ResolvePoint(_ context.Context, _ Gateway, _ string, pointCode string) (*ResolvedPoint, error) {
	if pointCode == "unregistered_point" {
		return nil, nil
	}
	return &ResolvedPoint{PointID: testPointID, PointType: "TELEMETRY", ValueType: "NUMBER", PointRevision: 1}, nil
}

func (identities *fakeIdentities) QuarantineUplink(_ context.Context, quarantine UplinkQuarantine) error {
	identities.mu.Lock()
	defer identities.mu.Unlock()
	if identities.quarantineErr != nil {
		return identities.quarantineErr
	}
	identities.quarantines = append(identities.quarantines, quarantine)
	return nil
}

// fakeTelemetry accepts observations, failing the first failures calls and holding each
// call until release is closed when release is set.
type fakeTelemetry struct {
	mu           sync.Mutex
	observations []Observation
	events       []RuntimeEventEvidence
	failures     int
	release      chan struct{}
}

func (telemetry *fakeTelemetry) AcceptObservation(ctx context.Context, observation Observation) (ObservationReceipt, error) {
	if telemetry.release != nil {
		select {
		case <-telemetry.release:
		case <-ctx.Done():
			return ObservationReceipt{}, ctx.Err()
		}
	}
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	if telemetry.failures > 0 {
		telemetry.failures--
		return ObservationReceipt{}, errors.New("telemetry runtime unavailable")
	}
	telemetry.observations = append(telemetry.observations, observation)
	status := "ACCEPTED"
	if observation.Device == nil || observation.Point == nil {
		status = "QUARANTINED"
	}
	return ObservationReceipt{Status: status}, nil
}

func (telemetry *fakeTelemetry) AcceptRuntimeEvent(_ context.Context, evidence RuntimeEventEvidence) error {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	telemetry.events = append(telemetry.events, evidence)
	return nil
}

func (telemetry *fakeTelemetry) accepted() []Observation {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	return append([]Observation(nil), telemetry.observations...)
}

func telemetryTopic(gatewayID string) string {
	return "hvac/v1/" + gatewayID + "/up/telemetry"
}

func telemetryPayload(gatewayID, messageID string, sequence int, sourceKey, pointCode string) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"2.0","messageId":%q,"gatewayId":%q,"timestamp":1786352400000,"sequence":%d,"replay":false,"payload":{"devices":[{"deviceId":%q,"deviceTimestamp":1786352399000,"points":[{"code":%q,"value":126.4,"quality":0,"unit":"kW"}]}]}}`,
		messageID, gatewayID, sequence, sourceKey, pointCode))
}

func messageID(index int) string {
	return fmt.Sprintf("0198a100-0000-7000-8000-%012d", index)
}
