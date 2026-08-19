package telemetryhistorymodel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	testTenantID = "018f2e00-1000-7000-8000-000000000001"
	testSiteID   = "018f2e00-2000-7000-8000-000000000001"
	testDeviceID = "018f2e00-3000-7000-8000-000000000001"
	testPointID  = "018f2e00-4000-7000-8000-000000000001"
)

func TestHistoryQueryCanonicalCursorScopeAndValidation(t *testing.T) {
	from := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	cursor := "cursor-a"
	query := DeviceHistoryQuery{
		TenantID: testTenantID, SiteID: testSiteID, DeviceID: testDeviceID,
		Keys: []string{"zone.temperature", "zone.mode"}, From: from, To: from.Add(time.Hour), PageSize: 200, Cursor: &cursor,
	}
	canonical, err := query.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(canonical.Keys, ",") != "zone.mode,zone.temperature" || canonical.PageSize != 200 || canonical.Cursor == nil || *canonical.Cursor != cursor {
		t.Fatalf("canonical=%#v", canonical)
	}
	withCursor, err := canonical.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	withoutCursor, err := canonical.CursorScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	canonical.Cursor = nil
	secondWithoutCursor, err := canonical.CursorScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	if withCursor == withoutCursor || withoutCursor != secondWithoutCursor {
		t.Fatalf("scope digests cursor=%s no-cursor=%s second=%s", withCursor, withoutCursor, secondWithoutCursor)
	}
}

func TestHistoryResponsePreservesTypedAndOutOfOrderSameTimestampObservations(t *testing.T) {
	from := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	query := DeviceHistoryQuery{TenantID: testTenantID, SiteID: testSiteID, DeviceID: testDeviceID, Keys: []string{"zone.mode"}, From: from, To: from.Add(time.Hour), PageSize: 10}
	watermark := from.Add(5 * time.Minute)
	response := DeviceHistoryResponse{
		SchemaVersion: 2, TenantID: testTenantID, SiteID: testSiteID, DeviceID: testDeviceID,
		Observations: []DeviceHistoryObservation{
			testObservation("018f2e00-5000-7000-8000-000000000001", "zone.mode", from.Add(time.Minute), AcceptanceAccepted, ValueTypeString, `"COOL"`),
			testObservation("018f2e00-5000-7000-8000-000000000002", "zone.mode", from.Add(time.Minute), AcceptanceOutOfOrder, ValueTypeJSON, `{"mode":"AUTO"}`),
		},
		Metadata: DeviceHistoryMetadata{RequestedFrom: from, RequestedTo: from.Add(time.Hour), ProjectionWatermark: &watermark, PageSize: 10, ReturnedObservations: 2},
	}
	if err := response.ValidateFor(query); err != nil {
		t.Fatal(err)
	}

	boolean := testObservation("018f2e00-5000-7000-8000-000000000003", "zone.mode", from.Add(2*time.Minute), AcceptanceAccepted, ValueTypeBoolean, `true`)
	if err := validateTypedValue(boolean.ValueType, boolean.Value); err != nil {
		t.Fatal(err)
	}
	number := testObservation("018f2e00-5000-7000-8000-000000000004", "zone.mode", from.Add(3*time.Minute), AcceptanceAccepted, ValueTypeNumber, `21.5`)
	if err := validateTypedValue(number.ValueType, number.Value); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryJSONHasNoPseudoRevisionOrWatermarkFields(t *testing.T) {
	from := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	response := DeviceHistoryResponse{
		SchemaVersion: 2, TenantID: testTenantID, SiteID: testSiteID, DeviceID: testDeviceID,
		Observations: []DeviceHistoryObservation{testObservation("018f2e00-5000-7000-8000-000000000005", "zone.temperature", from.Add(time.Minute), AcceptanceAccepted, ValueTypeNumber, `21.5`)},
		Metadata:     DeviceHistoryMetadata{RequestedFrom: from, RequestedTo: from.Add(time.Hour), PageSize: 10, ReturnedObservations: 1},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{`"revision"`, `"datasetRevision"`, `"dataWatermark"`, `"partial"`, `"maxPointsPerKey"`, `"series"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("legacy field %s leaked: %s", forbidden, text)
		}
	}
	for _, required := range []string{`"pointRevision"`, `"sourcePosition"`, `"observationId"`, `"acceptance"`, `"projectionWatermark"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("required field %s missing: %s", required, text)
		}
	}
}

func TestTypedJSONRejectsPrimitiveValues(t *testing.T) {
	for _, raw := range []string{`1`, `"state"`, `true`, `null`} {
		if err := validateTypedValue(ValueTypeJSON, json.RawMessage(raw)); err == nil {
			t.Fatalf("primitive JSON value %s was accepted", raw)
		}
	}
	for _, raw := range []string{`{"mode":"AUTO"}`, `[1,"AUTO"]`} {
		if err := validateTypedValue(ValueTypeJSON, json.RawMessage(raw)); err != nil {
			t.Fatalf("structured JSON value %s was rejected: %v", raw, err)
		}
	}
}

func testObservation(id, key string, sampledAt time.Time, acceptance Acceptance, valueType ValueType, value string) DeviceHistoryObservation {
	return DeviceHistoryObservation{
		ObservationID: id, TelemetryKey: key, PointID: testPointID, PointType: PointTypeTelemetry, PointRevision: 7,
		SampledAt: sampledAt, ReceivedAt: sampledAt.Add(time.Second), Acceptance: acceptance,
		ValueType: valueType, Value: json.RawMessage(value), Quality: QualityGood, QualityReasons: []string{},
		SourcePosition: SourcePosition{Partition: "mqtt:gateway:device:" + key, Offset: 42, EventID: id},
	}
}
