package fdd

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

type memoryStore struct {
	findings []intelligencemodel.FDDFinding
}

type historySource struct {
	response telemetryhistorymodel.DeviceHistoryResponse
	err      error
	query    telemetryhistorymodel.DeviceHistoryQuery
	grant    string
}

type pagedHistorySource struct {
	pages   []telemetryhistorymodel.DeviceHistoryResponse
	queries []telemetryhistorymodel.DeviceHistoryQuery
	grants  []string
}

func (source *historySource) QueryDeviceHistory(_ context.Context, query telemetryhistorymodel.DeviceHistoryQuery, grant string) (telemetryhistorymodel.DeviceHistoryResponse, error) {
	source.query = query
	source.grant = grant
	return source.response, source.err
}

func (source *pagedHistorySource) QueryDeviceHistory(_ context.Context, query telemetryhistorymodel.DeviceHistoryQuery, grant string) (telemetryhistorymodel.DeviceHistoryResponse, error) {
	index := len(source.queries)
	if index >= len(source.pages) {
		return telemetryhistorymodel.DeviceHistoryResponse{}, errors.New("unexpected history page")
	}
	source.queries = append(source.queries, query)
	source.grants = append(source.grants, grant)
	return source.pages[index], nil
}

func (store *memoryStore) InsertFinding(_ context.Context, finding intelligencemodel.FDDFinding) error {
	store.findings = append(store.findings, finding)
	return nil
}

func (store *memoryStore) ListFindings(context.Context, string, string, int) ([]intelligencemodel.FDDFinding, error) {
	return append([]intelligencemodel.FDDFinding(nil), store.findings...), nil
}

func (store *memoryStore) LinkFinding(_ context.Context, _, _, findingID, alarmID, workOrderID string, _ time.Time) (intelligencemodel.FDDFinding, error) {
	for index := range store.findings {
		if store.findings[index].ID == findingID {
			store.findings[index].AlarmID = alarmID
			store.findings[index].WorkOrderID = workOrderID
			return store.findings[index], nil
		}
	}
	return intelligencemodel.FDDFinding{}, nil
}

func validEvaluation() EvaluationRequest {
	from := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	return EvaluationRequest{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001", AssetID: "01990000-6000-7000-8000-000000000001",
		DeviceID:       "01990000-6100-7000-8000-000000000001",
		EvaluationFrom: from, EvaluationTo: from.Add(15 * time.Minute), RuleRevisionID: "fdd-low-delta-t/v1", MinimumDeltaTC: 5,
	}
}

func validHistoryResponse(request EvaluationRequest) telemetryhistorymodel.DeviceHistoryResponse {
	unit := "Cel"
	return telemetryhistorymodel.DeviceHistoryResponse{
		SchemaVersion: 2,
		TenantID:      request.TenantID,
		SiteID:        request.SiteID,
		DeviceID:      request.DeviceID,
		Observations: []telemetryhistorymodel.DeviceHistoryObservation{
			{
				ObservationID: "01990000-6200-7000-8000-000000000001", TelemetryKey: returnTemperatureKey,
				PointID: "01990000-6300-7000-8000-000000000001", PointType: telemetryhistorymodel.PointTypeTelemetry, PointRevision: 1,
				SampledAt: request.EvaluationFrom.Add(10 * time.Minute), ReceivedAt: request.EvaluationFrom.Add(10*time.Minute + time.Second),
				Acceptance: telemetryhistorymodel.AcceptanceAccepted, ValueType: telemetryhistorymodel.ValueTypeNumber, Value: json.RawMessage("10"), Unit: &unit,
				Quality: telemetryhistorymodel.QualityGood, QualityReasons: []string{},
				SourcePosition: telemetryhistorymodel.SourcePosition{Partition: "telemetry-0", Offset: 2, EventID: "01990000-6400-7000-8000-000000000002"},
			},
			{
				ObservationID: "01990000-6200-7000-8000-000000000002", TelemetryKey: supplyTemperatureKey,
				PointID: "01990000-6300-7000-8000-000000000002", PointType: telemetryhistorymodel.PointTypeTelemetry, PointRevision: 1,
				SampledAt: request.EvaluationFrom.Add(10 * time.Minute), ReceivedAt: request.EvaluationFrom.Add(10*time.Minute + time.Second),
				Acceptance: telemetryhistorymodel.AcceptanceAccepted, ValueType: telemetryhistorymodel.ValueTypeNumber, Value: json.RawMessage("7"), Unit: &unit,
				Quality: telemetryhistorymodel.QualityGood, QualityReasons: []string{},
				SourcePosition: telemetryhistorymodel.SourcePosition{Partition: "telemetry-0", Offset: 1, EventID: "01990000-6400-7000-8000-000000000001"},
			},
		},
		Metadata: telemetryhistorymodel.DeviceHistoryMetadata{
			RequestedFrom: request.EvaluationFrom, RequestedTo: request.EvaluationTo,
			PageSize: historyPageSize, ReturnedObservations: 2,
		},
	}
}

func newTestService(t *testing.T, store Store, source HistorySource, now func() time.Time) *Service {
	t.Helper()
	service, err := NewService(store, source, now)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestLowDeltaTUsesAuthoritativeHistoryEvidence(t *testing.T) {
	store := &memoryStore{}
	request := validEvaluation()
	source := &historySource{response: validHistoryResponse(request)}
	now := time.Date(2026, 8, 19, 10, 16, 0, 0, time.UTC)
	service := newTestService(t, store, source, func() time.Time { return now })

	result, err := service.EvaluateLowDeltaT(t.Context(), request, "history-grant")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "FINDING" || result.DeltaTC != 3 || result.Finding == nil || len(store.findings) != 1 {
		t.Fatalf("result=%#v persisted=%#v", result, store.findings)
	}
	finding := *result.Finding
	if finding.FindingType != "CHILLED_WATER_LOW_DELTA_T" || finding.RuleRevisionID != "fdd-low-delta-t/v1" || finding.Confidence <= 0.5 {
		t.Fatalf("finding=%#v", finding)
	}
	if len(finding.EvidenceIDs) != 2 || finding.EvidenceIDs[0] != "01990000-6200-7000-8000-000000000002" || finding.EvidenceIDs[1] != "01990000-6200-7000-8000-000000000001" {
		t.Fatalf("evidence IDs must be authoritative observation IDs: %#v", finding.EvidenceIDs)
	}
	if source.grant != "history-grant" || source.query.DeviceID != request.DeviceID || len(source.query.Keys) != 2 || source.query.Keys[0] != returnTemperatureKey || source.query.Keys[1] != supplyTemperatureKey {
		t.Fatalf("history query=%#v grant=%q", source.query, source.grant)
	}
	if finding.AlarmID != "" || finding.WorkOrderID != "" {
		t.Fatal("FDD evaluation must not create Alarm or Work Order side effects")
	}
}

func TestLowDeltaTFollowsAuthoritativeHistoryCursor(t *testing.T) {
	store := &memoryStore{}
	request := validEvaluation()
	response := validHistoryResponse(request)
	cursor := "history-page-two"
	firstPage := response
	firstPage.Observations = response.Observations[:1]
	firstPage.Metadata.ReturnedObservations = 1
	firstPage.Metadata.NextCursor = &cursor
	secondPage := response
	secondPage.Observations = response.Observations[1:]
	secondPage.Metadata.ReturnedObservations = 1
	secondPage.Metadata.NextCursor = nil
	source := &pagedHistorySource{pages: []telemetryhistorymodel.DeviceHistoryResponse{firstPage, secondPage}}
	service := newTestService(t, store, source, time.Now)

	result, err := service.EvaluateLowDeltaT(t.Context(), request, "history-grant")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "FINDING" || result.Finding == nil || len(store.findings) != 1 {
		t.Fatalf("result=%#v findings=%#v", result, store.findings)
	}
	if len(source.queries) != 2 || source.queries[0].Cursor != nil || source.queries[1].Cursor == nil || *source.queries[1].Cursor != cursor {
		t.Fatalf("history queries=%#v", source.queries)
	}
	if len(source.grants) != 2 || source.grants[0] != "history-grant" || source.grants[1] != "history-grant" {
		t.Fatalf("history grants=%#v", source.grants)
	}
}

func TestHealthyDeltaTClearsWithoutFinding(t *testing.T) {
	store := &memoryStore{}
	request := validEvaluation()
	response := validHistoryResponse(request)
	response.Observations[0].Value = json.RawMessage("13")
	service := newTestService(t, store, &historySource{response: response}, time.Now)

	result, err := service.EvaluateLowDeltaT(t.Context(), request, "history-grant")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "CLEAR" || result.Finding != nil || len(store.findings) != 0 {
		t.Fatalf("result=%#v persisted=%#v", result, store.findings)
	}
}

func TestFDDRejectsMissingOrInvalidAuthoritativeHistory(t *testing.T) {
	request := validEvaluation()

	store := &memoryStore{}
	response := validHistoryResponse(request)
	response.Observations = response.Observations[:1]
	response.Metadata.ReturnedObservations = 1
	service := newTestService(t, store, &historySource{response: response}, time.Now)
	if _, err := service.EvaluateLowDeltaT(t.Context(), request, "history-grant"); err == nil {
		t.Fatal("expected missing supply evidence to fail")
	}
	if len(store.findings) != 0 {
		t.Fatal("missing evidence must not create a finding")
	}

	response = validHistoryResponse(request)
	response.Observations[1].Quality = telemetryhistorymodel.QualityInvalid
	service = newTestService(t, store, &historySource{response: response}, time.Now)
	if _, err := service.EvaluateLowDeltaT(t.Context(), request, "history-grant"); err == nil {
		t.Fatal("expected invalid-quality evidence to fail")
	}
	if len(store.findings) != 0 {
		t.Fatal("invalid evidence must not create a finding")
	}
}

func TestFDDRejectsUnauthorizedHistoryWithoutFallback(t *testing.T) {
	store := &memoryStore{}
	service := newTestService(t, store, &historySource{err: errors.New("history authorization rejected")}, time.Now)
	if _, err := service.EvaluateLowDeltaT(t.Context(), validEvaluation(), "unauthorized-grant"); err == nil {
		t.Fatal("expected authoritative history failure")
	}
	if len(store.findings) != 0 {
		t.Fatal("history failure must not create a finding")
	}
}

func TestFDDLinkingIsExplicitAndSeparateFromDetection(t *testing.T) {
	store := &memoryStore{}
	request := validEvaluation()
	service := newTestService(t, store, &historySource{response: validHistoryResponse(request)}, time.Now)
	result, err := service.EvaluateLowDeltaT(t.Context(), request, "history-grant")
	if err != nil {
		t.Fatal(err)
	}
	linked, err := service.LinkFinding(t.Context(), request.TenantID, request.SiteID, result.Finding.ID,
		"01990000-7000-7000-8000-000000000001", "01990000-8000-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if linked.AlarmID == "" || linked.WorkOrderID == "" {
		t.Fatalf("linked=%#v", linked)
	}
}
