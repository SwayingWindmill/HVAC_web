package fdd

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

type memoryStore struct {
	findings []intelligencemodel.FDDFinding
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
		EvaluationFrom: from, EvaluationTo: from.Add(15 * time.Minute), RuleRevisionID: "fdd-low-delta-t/v1", MinimumDeltaTC: 5,
		Evidence: []EvidenceValue{
			{EvidenceID: "ev-supply", Signal: "chilled_water_supply_temperature", ObservedAt: from.Add(10 * time.Minute), Value: 7, Unit: "Cel"},
			{EvidenceID: "ev-return", Signal: "chilled_water_return_temperature", ObservedAt: from.Add(10 * time.Minute), Value: 10, Unit: "Cel"},
		},
	}
}

func TestLowDeltaTGeneratesEvidenceBackedFindingWithoutAlarmSideEffect(t *testing.T) {
	store := &memoryStore{}
	now := time.Date(2026, 8, 19, 10, 16, 0, 0, time.UTC)
	service, _ := NewService(store, func() time.Time { return now })
	result, err := service.EvaluateLowDeltaT(t.Context(), validEvaluation())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "FINDING" || result.DeltaTC != 3 || result.Finding == nil || len(store.findings) != 1 {
		t.Fatalf("result=%#v persisted=%#v", result, store.findings)
	}
	finding := *result.Finding
	if finding.FindingType != "CHILLED_WATER_LOW_DELTA_T" || finding.RuleRevisionID != "fdd-low-delta-t/v1" || len(finding.EvidenceIDs) != 2 || finding.Confidence <= 0.5 {
		t.Fatalf("finding=%#v", finding)
	}
	if finding.AlarmID != "" || finding.WorkOrderID != "" {
		t.Fatal("FDD evaluation must not create Alarm or Work Order side effects")
	}
}

func TestHealthyDeltaTDoesNotFabricateFinding(t *testing.T) {
	store := &memoryStore{}
	service, _ := NewService(store, time.Now)
	request := validEvaluation()
	request.Evidence[1].Value = 13
	result, err := service.EvaluateLowDeltaT(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "CLEAR" || result.Finding != nil || len(store.findings) != 0 {
		t.Fatalf("result=%#v persisted=%#v", result, store.findings)
	}
}

func TestFDDRejectsEvidenceOutsideFrozenWindow(t *testing.T) {
	store := &memoryStore{}
	service, _ := NewService(store, time.Now)
	request := validEvaluation()
	request.Evidence[0].ObservedAt = request.EvaluationTo.Add(time.Second)
	if _, err := service.EvaluateLowDeltaT(t.Context(), request); err == nil {
		t.Fatal("expected out-of-window evidence to fail")
	}
	if len(store.findings) != 0 {
		t.Fatal("invalid evidence must not create a finding")
	}
}

func TestFDDLinkingIsExplicitAndSeparateFromDetection(t *testing.T) {
	store := &memoryStore{}
	service, _ := NewService(store, time.Now)
	result, err := service.EvaluateLowDeltaT(t.Context(), validEvaluation())
	if err != nil {
		t.Fatal(err)
	}
	linked, err := service.LinkFinding(t.Context(), validEvaluation().TenantID, validEvaluation().SiteID, result.Finding.ID,
		"01990000-7000-7000-8000-000000000001", "01990000-8000-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if linked.AlarmID == "" || linked.WorkOrderID == "" {
		t.Fatalf("linked=%#v", linked)
	}
}
