package telemetryhistorymodel

import (
	"testing"
	"time"
)

const (
	testOrganizationID = "018f1e00-0000-7000-8000-000000000001"
	testSiteID         = "018f1e00-1000-7000-8000-000000000001"
	testDeviceID       = "018f1e00-4000-7000-8000-000000000001"
	testObservationID  = "018f1e00-8000-7000-8000-000000000001"
	testPointID        = "018f1e00-5000-7000-8000-000000000001"
	testSensorID       = "018f1e00-6000-7000-8000-000000000001"
)

func TestDeviceHistoryQueryCanonicalDigestIsOrderIndependent(t *testing.T) {
	from := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	left := DeviceHistoryQuery{
		ActingOrganizationID: testOrganizationID, OwningOrganizationID: testOrganizationID, SiteID: testSiteID, DeviceID: testDeviceID,
		Keys: []string{"zone.humidity", "zone.temperature"}, From: from, To: from.Add(6 * time.Hour), MaxPointsPerKey: 200,
	}
	right := left
	right.Keys = []string{"zone.temperature", "zone.humidity"}
	leftDigest, err := left.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest || len(leftDigest) != 64 {
		t.Fatalf("digests = %q, %q", leftDigest, rightDigest)
	}
}

func TestDeviceHistoryQueryRejectsUnsupportedProductBounds(t *testing.T) {
	from := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	valid := DeviceHistoryQuery{
		ActingOrganizationID: testOrganizationID, OwningOrganizationID: testOrganizationID, SiteID: testSiteID, DeviceID: testDeviceID,
		Keys: []string{"zone.temperature"}, From: from, To: from.Add(time.Hour), MaxPointsPerKey: 100,
	}
	tests := []struct {
		name   string
		mutate func(*DeviceHistoryQuery)
	}{
		{"invalid organization", func(query *DeviceHistoryQuery) { query.ActingOrganizationID = "not-a-uuid" }},
		{"no keys", func(query *DeviceHistoryQuery) { query.Keys = nil }},
		{"duplicate key", func(query *DeviceHistoryQuery) { query.Keys = []string{"zone.temperature", "zone.temperature"} }},
		{"invalid key", func(query *DeviceHistoryQuery) { query.Keys = []string{"zone temperature"} }},
		{"range over 24 hours", func(query *DeviceHistoryQuery) { query.To = query.From.Add(24*time.Hour + time.Millisecond) }},
		{"non UTC range", func(query *DeviceHistoryQuery) { query.From = query.From.In(time.FixedZone("JST", 9*60*60)) }},
		{"too many points", func(query *DeviceHistoryQuery) { query.MaxPointsPerKey = MaximumPointsPerKey + 1 }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			query := valid
			query.Keys = append([]string(nil), valid.Keys...)
			testCase.mutate(&query)
			if err := query.Validate(); err == nil {
				t.Fatal("invalid query was accepted")
			}
		})
	}
}

func TestDeviceHistoryResponseValidatesScopeOrderingAndMetadata(t *testing.T) {
	from := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	query := DeviceHistoryQuery{
		ActingOrganizationID: testOrganizationID, OwningOrganizationID: testOrganizationID, SiteID: testSiteID, DeviceID: testDeviceID,
		Keys: []string{"zone.temperature"}, From: from, To: from.Add(time.Hour), MaxPointsPerKey: 100,
	}
	unit := "Cel"
	watermark := from.Add(55 * time.Minute)
	response := DeviceHistoryResponse{
		SchemaVersion: 1, OwningOrganizationID: testOrganizationID, SiteID: testSiteID, DeviceID: testDeviceID,
		Series: []DeviceHistorySeries{{Key: "zone.temperature", Points: []DeviceHistoryPoint{{
			ObservationID: testObservationID, PointID: testPointID, SensorID: stringPointer(testSensorID), SampledAt: from.Add(5 * time.Minute), ReceivedAt: from.Add(5*time.Minute + time.Second),
			Value: 22.5, Unit: &unit, Quality: QualityGood, QualityReasons: []string{}, Revision: 7,
		}}}},
		Metadata: DeviceHistoryMetadata{
			RequestedFrom: from, RequestedTo: from.Add(time.Hour), DataWatermark: &watermark,
			DatasetRevision: "telemetry-history:v1:7", Partial: true, MaxPointsPerKey: 100, ReturnedPoints: 1, TruncatedKeys: []string{},
		},
	}
	if err := response.ValidateFor(query); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
	response.Series[0].Points[0].PointID = "not-a-point"
	if err := response.ValidateFor(query); err == nil {
		t.Fatal("invalid point identity was accepted")
	}
	response.Series[0].Points[0].PointID = testPointID
	response.Series[0].Points[0].SensorID = stringPointer("not-a-sensor")
	if err := response.ValidateFor(query); err == nil {
		t.Fatal("invalid sensor identity was accepted")
	}
	response.Series[0].Points[0].SensorID = stringPointer(testSensorID)
	response.Series[0].Key = "unrequested.key"
	if err := response.ValidateFor(query); err == nil {
		t.Fatal("response scope drift was accepted")
	}
	response.Series[0].Key = "zone.temperature"
	response.Series = nil
	if err := response.ValidateFor(query); err == nil {
		t.Fatal("missing requested series was accepted")
	}
	response.Series = []DeviceHistorySeries{{Key: "zone.temperature", Points: []DeviceHistoryPoint{}}}
	response.Metadata.ReturnedPoints = 0
	response.Metadata.Partial = false
	if err := response.ValidateFor(query); err == nil {
		t.Fatal("empty series without partial state was accepted")
	}
}

func stringPointer(value string) *string {
	return &value
}
