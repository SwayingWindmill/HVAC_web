package adapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	mu                   sync.Mutex
	inactiveCredential   map[string]bool
	quarantines          []UplinkQuarantine
	quarantineErr        error
	resolveBindingsCalls [][]DevicePointQuery
	resolveBindingsErr   error
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

func (identities *fakeIdentities) ResolveBindings(_ context.Context, _ Gateway, queries []DevicePointQuery) (ResolvedBindings, error) {
	identities.mu.Lock()
	defer identities.mu.Unlock()
	identities.resolveBindingsCalls = append(identities.resolveBindingsCalls, append([]DevicePointQuery(nil), queries...))
	if identities.resolveBindingsErr != nil {
		return ResolvedBindings{}, identities.resolveBindingsErr
	}
	bindings := ResolvedBindings{
		Devices: make(map[string]string),
		Points:  make(map[string]*ResolvedPoint),
	}
	for _, query := range queries {
		if query.SourceKey != "METER-01" {
			continue
		}
		bindings.Devices[query.SourceKey] = testDeviceID
		if query.PointCode == "unregistered_point" {
			continue
		}
		bindings.Points[PointBindingKey(testDeviceID, query.PointCode)] = &ResolvedPoint{
			PointID:       testPointID,
			PointType:     "TELEMETRY",
			ValueType:     "NUMBER",
			PointRevision: 1,
		}
	}
	return bindings, nil
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

func (identities *fakeIdentities) snapshotQuarantines() []UplinkQuarantine {
	identities.mu.Lock()
	defer identities.mu.Unlock()
	return append([]UplinkQuarantine(nil), identities.quarantines...)
}

func (identities *fakeIdentities) bindingsCalls() [][]DevicePointQuery {
	identities.mu.Lock()
	defer identities.mu.Unlock()
	calls := make([][]DevicePointQuery, len(identities.resolveBindingsCalls))
	for index, call := range identities.resolveBindingsCalls {
		calls[index] = append([]DevicePointQuery(nil), call...)
	}
	return calls
}

// fakeTelemetry accepts observation batches, failing the first failures calls and holding
// each call until release is closed when release is set.
type fakeTelemetry struct {
	mu              sync.Mutex
	observations    []Observation
	batches         [][]Observation
	events          []RuntimeEventEvidence
	coverageReports []SourceCoverageReport
	coverageErr     error
	failures        int
	calls           int
	failHook        func(call int, batch []Observation) error
	release         chan struct{}
	entered         chan struct{}
}

func (telemetry *fakeTelemetry) AcceptObservationBatch(ctx context.Context, observations []Observation) ([]ObservationReceipt, error) {
	if telemetry.entered != nil {
		select {
		case telemetry.entered <- struct{}{}:
		default:
		}
	}
	if telemetry.release != nil {
		select {
		case <-telemetry.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	telemetry.mu.Lock()
	telemetry.calls++
	call := telemetry.calls
	failHook := telemetry.failHook
	if telemetry.failures > 0 {
		telemetry.failures--
		telemetry.mu.Unlock()
		return nil, errors.New("telemetry runtime unavailable")
	}
	telemetry.mu.Unlock()

	if failHook != nil {
		if err := failHook(call, append([]Observation(nil), observations...)); err != nil {
			return nil, err
		}
	}

	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	batchCopy := append([]Observation(nil), observations...)
	telemetry.batches = append(telemetry.batches, batchCopy)
	telemetry.observations = append(telemetry.observations, observations...)
	receipts := make([]ObservationReceipt, len(observations))
	for index, observation := range observations {
		status := "ACCEPTED"
		if observation.Device == nil || observation.Point == nil {
			status = "QUARANTINED"
		}
		receipts[index] = ObservationReceipt{Status: status}
	}
	return receipts, nil
}

func (telemetry *fakeTelemetry) AcceptRuntimeEvent(_ context.Context, evidence RuntimeEventEvidence) error {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	telemetry.events = append(telemetry.events, evidence)
	return nil
}

func (telemetry *fakeTelemetry) ReportSourceCoverage(_ context.Context, report SourceCoverageReport) error {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	if telemetry.coverageErr != nil {
		return telemetry.coverageErr
	}
	telemetry.coverageReports = append(telemetry.coverageReports, report)
	return nil
}

func (telemetry *fakeTelemetry) accepted() []Observation {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	return append([]Observation(nil), telemetry.observations...)
}

func (telemetry *fakeTelemetry) recordedBatches() [][]Observation {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	batches := make([][]Observation, len(telemetry.batches))
	for index, batch := range telemetry.batches {
		batches[index] = append([]Observation(nil), batch...)
	}
	return batches
}

func (telemetry *fakeTelemetry) recordedEvents() []RuntimeEventEvidence {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	return append([]RuntimeEventEvidence(nil), telemetry.events...)
}

func telemetryTopic(gatewayID string) string {
	return "hvac/v1/" + gatewayID + "/up/telemetry"
}

func eventTopic(gatewayID string) string {
	return "hvac/v1/" + gatewayID + "/up/event"
}

func telemetryPayload(gatewayID, messageID string, sequence int, sourceKey, pointCode string) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"2.0","messageId":%q,"gatewayId":%q,"timestamp":1786352400000,"sequence":%d,"replay":false,"payload":{"devices":[{"deviceId":%q,"deviceTimestamp":1786352399000,"points":[{"code":%q,"value":126.4,"quality":0,"unit":"kW"}]}]}}`,
		messageID, gatewayID, sequence, sourceKey, pointCode))
}

func multiPointTelemetryPayload(gatewayID, messageID string, sequence int, sourceKey string, pointCount int) []byte {
	points := make([]string, pointCount)
	for index := 0; index < pointCount; index++ {
		points[index] = fmt.Sprintf(`{"code":"point_%04d","value":%d,"quality":0,"unit":"kW"}`, index, index)
	}
	return []byte(fmt.Sprintf(`{"schemaVersion":"2.0","messageId":%q,"gatewayId":%q,"timestamp":1786352400000,"sequence":%d,"replay":false,"payload":{"devices":[{"deviceId":%q,"deviceTimestamp":1786352399000,"points":[%s]}]}}`,
		messageID, gatewayID, sequence, sourceKey, strings.Join(points, ",")))
}

func eventPayload(gatewayID, messageID string, sequence int) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"2.0","messageId":%q,"gatewayId":%q,"timestamp":1786352400000,"sequence":%d,"payload":{"eventType":"DEVICE_OFFLINE","sourceType":"DEVICE","sourceId":"METER-01","eventTime":1786352399000,"severity":"WARNING","data":{}}}`,
		messageID, gatewayID, sequence))
}

func messageID(index int) string {
	return fmt.Sprintf("0198a100-0000-7000-8000-%012d", index)
}
