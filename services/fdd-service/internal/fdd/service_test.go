package fdd

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

type memoryStore struct {
	rules    []RuleRevision
	findings []intelligencemodel.FDDFinding
}

// testRule is the Tenant's first low-delta-T revision: 5 K over 15 minutes.
var testRule = RuleRevision{
	ID: "01990000-9500-7000-8000-000000000001", Revision: 1, MinimumDeltaTC: 5, WindowMinutes: 15,
	EffectiveFrom: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
}

func newStore() *memoryStore { return &memoryStore{rules: []RuleRevision{testRule}} }

func (store *memoryStore) CurrentRule(_ context.Context, _, _ string, at time.Time) (RuleRevision, error) {
	var current *RuleRevision
	for index, rule := range store.rules {
		if !rule.EffectiveFrom.After(at) && (current == nil || rule.Revision > current.Revision) {
			current = &store.rules[index]
		}
	}
	if current == nil {
		return RuleRevision{}, ErrRuleNotFound
	}
	return *current, nil
}

type loopEvidence struct {
	readings LoopReadings
	err      error
	query    LoopQuery
}

func (source *loopEvidence) LatestLoopReadings(_ context.Context, query LoopQuery) (LoopReadings, error) {
	source.query = query
	return source.readings, source.err
}

func (store *memoryStore) InsertFinding(_ context.Context, finding intelligencemodel.FDDFinding) error {
	store.findings = append(store.findings, finding)
	return nil
}

func (store *memoryStore) RecentFinding(_ context.Context, tenantID, siteID, assetID, findingType, ruleRevisionID string, since time.Time) (intelligencemodel.FDDFinding, bool, error) {
	for index := len(store.findings) - 1; index >= 0; index-- {
		finding := store.findings[index]
		if finding.TenantID == tenantID && finding.SiteID == siteID && finding.AssetID == assetID && finding.FindingType == findingType &&
			finding.RuleRevisionID == ruleRevisionID && !finding.EvaluationTo.Before(since) {
			return finding, true, nil
		}
	}
	return intelligencemodel.FDDFinding{}, false, nil
}

func (store *memoryStore) ListFindings(_ context.Context, _, _ string, filter FindingFilter) ([]intelligencemodel.FDDFinding, error) {
	findings := make([]intelligencemodel.FDDFinding, 0, min(filter.Limit, len(store.findings)))
	for _, finding := range store.findings {
		if filter.AlarmID != "" && finding.AlarmID != filter.AlarmID {
			continue
		}
		if filter.WorkOrderID != "" && finding.WorkOrderID != filter.WorkOrderID {
			continue
		}
		findings = append(findings, finding)
		if len(findings) == filter.Limit {
			break
		}
	}
	return findings, nil
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
	return EvaluationRequest{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001", AssetID: "01990000-6000-7000-8000-000000000001",
		DeviceID: "01990000-6100-7000-8000-000000000001", At: time.Date(2026, 8, 19, 10, 15, 0, 0, time.UTC),
	}
}

func validReadings(request EvaluationRequest) LoopReadings {
	reading := func(id string, value float64) Reading {
		return Reading{ObservationID: id, PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", Quality: "GOOD", Value: value, SampledAt: request.At.Add(-5 * time.Minute)}
	}
	return LoopReadings{Supply: reading("01990000-6200-7000-8000-000000000002", 7), Return: reading("01990000-6200-7000-8000-000000000001", 10)}
}

func newTestService(t *testing.T, store Store, evidence EvidenceSource, now func() time.Time) *Service {
	t.Helper()
	service, err := NewService(store, evidence, now)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestLowDeltaTRecordsAFindingFromTheLoopsLatestReadings(t *testing.T) {
	store := newStore()
	request := validEvaluation()
	evidence := &loopEvidence{readings: validReadings(request)}
	now := time.Date(2026, 8, 19, 10, 16, 0, 0, time.UTC)
	service := newTestService(t, store, evidence, func() time.Time { return now })

	result, err := service.EvaluateLowDeltaT(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "FINDING" || result.DeltaTC != 3 || result.Finding == nil || len(store.findings) != 1 {
		t.Fatalf("result=%#v persisted=%#v", result, store.findings)
	}
	finding := *result.Finding
	if finding.FindingType != "CHILLED_WATER_LOW_DELTA_T" || finding.RuleRevisionID != testRule.ID || result.Rule != testRule || finding.Confidence <= 0.5 {
		t.Fatalf("finding=%#v", finding)
	}
	// A finding states what was measured against what threshold, so it reads without its evidence.
	if finding.ObservedValue != 3 || finding.ThresholdValue != 5 || finding.ValueUnit != "K" {
		t.Fatalf("finding measurement=%v/%v %q", finding.ObservedValue, finding.ThresholdValue, finding.ValueUnit)
	}
	if len(finding.EvidenceIDs) != 2 || finding.EvidenceIDs[0] != "01990000-6200-7000-8000-000000000002" || finding.EvidenceIDs[1] != "01990000-6200-7000-8000-000000000001" {
		t.Fatalf("evidence IDs must be authoritative observation IDs: %#v", finding.EvidenceIDs)
	}
	// The rule's window, not the caller, decides how far back the evidence is read.
	if evidence.query != (LoopQuery{TenantID: request.TenantID, SiteID: request.SiteID, DeviceID: request.DeviceID, From: request.At.Add(-15 * time.Minute), To: request.At}) {
		t.Fatalf("evidence query=%#v", evidence.query)
	}
	if !finding.EvaluationFrom.Equal(request.At.Add(-15*time.Minute)) || !finding.EvaluationTo.Equal(request.At) {
		t.Fatalf("finding window=%s–%s", finding.EvaluationFrom, finding.EvaluationTo)
	}
}

// A new revision takes effect from its time: later evaluations use its threshold and window and
// cite it, and the old revision's finding does not make the condition "ongoing" under the new one.
func TestANewRuleRevisionAppliesFromItsEffectiveTime(t *testing.T) {
	store := newStore()
	request := validEvaluation()
	service := newTestService(t, store, &loopEvidence{readings: validReadings(request)}, func() time.Time { return request.At })
	first, err := service.EvaluateLowDeltaT(t.Context(), request)
	if err != nil || first.Finding.ThresholdValue != 5 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	revised := RuleRevision{ID: "01990000-9500-7000-8000-000000000002", Revision: 2, MinimumDeltaTC: 4, WindowMinutes: 30, EffectiveFrom: request.At.Add(time.Minute)}
	store.rules = append(store.rules, revised)
	later := request
	later.At = request.At.Add(15 * time.Minute)
	evidence := &loopEvidence{readings: validReadings(later)}
	service = newTestService(t, store, evidence, func() time.Time { return later.At })
	second, err := service.EvaluateLowDeltaT(t.Context(), later)
	if err != nil || second.Status != "FINDING" || second.Rule != revised || second.Finding.RuleRevisionID != revised.ID || second.Finding.ThresholdValue != 4 {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	if !evidence.query.From.Equal(later.At.Add(-30 * time.Minute)) {
		t.Fatalf("revised window from=%s", evidence.query.From)
	}
	// At its own time the first evaluation's revision still decides.
	if rule, err := service.store.CurrentRule(t.Context(), request.TenantID, request.SiteID, request.At); err != nil || rule != testRule {
		t.Fatalf("rule at the first evaluation=%#v err=%v", rule, err)
	}
}

func TestATenantWithoutTheRuleIsNotEvaluated(t *testing.T) {
	store := &memoryStore{}
	request := validEvaluation()
	service := newTestService(t, store, &loopEvidence{readings: validReadings(request)}, time.Now)
	if _, err := service.EvaluateLowDeltaT(t.Context(), request); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("evaluation without a rule err=%v", err)
	}
	if len(store.findings) != 0 {
		t.Fatal("a finding was recorded without a rule")
	}
}

func TestHealthyDeltaTClearsWithoutFinding(t *testing.T) {
	store := newStore()
	request := validEvaluation()
	readings := validReadings(request)
	readings.Return.Value = 13
	service := newTestService(t, store, &loopEvidence{readings: readings}, time.Now)

	result, err := service.EvaluateLowDeltaT(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "CLEAR" || result.Finding != nil || len(store.findings) != 0 {
		t.Fatalf("result=%#v persisted=%#v", result, store.findings)
	}
}

// Every fifteen minutes the schedule finds the same condition again; one finding a day stands
// for it, and later evaluations report it as ongoing instead of adding another.
func TestAPersistingConditionIsReportedOngoingWithinADay(t *testing.T) {
	store := newStore()
	request := validEvaluation()
	now := request.At.Add(time.Minute)
	service := newTestService(t, store, &loopEvidence{readings: validReadings(request)}, func() time.Time { return now })
	first, err := service.EvaluateLowDeltaT(t.Context(), request)
	if err != nil || first.Status != "FINDING" {
		t.Fatalf("first=%#v err=%v", first, err)
	}

	later := request
	later.At = request.At.Add(15 * time.Minute)
	ongoing, err := service.EvaluateLowDeltaT(t.Context(), later)
	if err != nil {
		t.Fatal(err)
	}
	if ongoing.Status != "ONGOING" || ongoing.Finding == nil || ongoing.Finding.ID != first.Finding.ID || ongoing.DeltaTC != 3 || len(store.findings) != 1 {
		t.Fatalf("ongoing=%#v findings=%d", ongoing, len(store.findings))
	}

	nextDay := request
	nextDay.At = request.At.Add(25 * time.Hour)
	again, err := service.EvaluateLowDeltaT(t.Context(), nextDay)
	if err != nil || again.Status != "FINDING" || len(store.findings) != 2 {
		t.Fatalf("a day later=%#v findings=%d err=%v", again, len(store.findings), err)
	}
}

func TestFDDRejectsMissingOrInvalidEvidence(t *testing.T) {
	request := validEvaluation()
	store := newStore()

	service := newTestService(t, store, &loopEvidence{err: ErrNoLoopEvidence}, time.Now)
	if _, err := service.EvaluateLowDeltaT(t.Context(), request); !errors.Is(err, ErrNoLoopEvidence) {
		t.Fatalf("missing evidence err=%v", err)
	}

	readings := validReadings(request)
	readings.Return.Quality = "BAD"
	service = newTestService(t, store, &loopEvidence{readings: readings}, time.Now)
	if _, err := service.EvaluateLowDeltaT(t.Context(), request); err == nil {
		t.Fatal("expected non-GOOD evidence to fail")
	}

	readings = validReadings(request)
	readings.Supply.Unit = "K"
	service = newTestService(t, store, &loopEvidence{readings: readings}, time.Now)
	if _, err := service.EvaluateLowDeltaT(t.Context(), request); err == nil {
		t.Fatal("expected non-Celsius evidence to fail")
	}
	if len(store.findings) != 0 {
		t.Fatal("missing or invalid evidence must not create a finding")
	}
}

func TestAScheduledEvaluationDiagnosesTheLoopAtItsFireTime(t *testing.T) {
	request := validEvaluation()
	payload, err := json.Marshal(ScheduledEvaluation{AssetID: request.AssetID, DeviceID: request.DeviceID})
	if err != nil {
		t.Fatal(err)
	}
	job := schedulerjob.Job{
		JobID: "01990000-9000-7000-8000-000000000001", TriggerType: "SCHEDULE", JobType: EvaluationJobType,
		TenantID: request.TenantID, SiteID: request.SiteID, Payload: payload, AttemptNo: 1, MaxAttempts: 3, TimeoutSeconds: 60,
		ScheduledFor: request.At,
	}
	scheduled, err := ScheduledRequest(job)
	if err != nil {
		t.Fatal(err)
	}
	if scheduled != request {
		t.Fatalf("scheduled=%#v want=%#v", scheduled, request)
	}

	// The rule is FDD's own; a schedule names the loop and nothing about the threshold.
	job.Payload = json.RawMessage(`{"assetId":"` + request.AssetID + `","deviceId":"` + request.DeviceID + `","minimumDeltaTC":5}`)
	if _, err = ScheduledRequest(job); err == nil {
		t.Fatal("a schedule carried its own threshold")
	}
}

func TestFDDLinkingIsExplicitAndSeparateFromDetection(t *testing.T) {
	store := newStore()
	request := validEvaluation()
	service := newTestService(t, store, &loopEvidence{readings: validReadings(request)}, time.Now)
	result, err := service.EvaluateLowDeltaT(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	alarmID := "01990000-7000-7000-8000-000000000001"
	workOrderID := "01990000-8000-7000-8000-000000000001"
	linked, err := service.LinkFinding(t.Context(), request.TenantID, request.SiteID, result.Finding.ID, alarmID, workOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if linked.AlarmID == "" || linked.WorkOrderID == "" {
		t.Fatalf("linked=%#v", linked)
	}
	byAlarm, err := service.ListFindings(t.Context(), request.TenantID, request.SiteID, FindingFilter{AlarmID: alarmID, Limit: 20})
	if err != nil || len(byAlarm) != 1 || byAlarm[0].ID != linked.ID {
		t.Fatalf("Alarm reverse association=%#v err=%v", byAlarm, err)
	}
	byWorkOrder, err := service.ListFindings(t.Context(), request.TenantID, request.SiteID, FindingFilter{WorkOrderID: workOrderID, Limit: 20})
	if err != nil || len(byWorkOrder) != 1 || byWorkOrder[0].ID != linked.ID {
		t.Fatalf("Work Order reverse association=%#v err=%v", byWorkOrder, err)
	}
}
