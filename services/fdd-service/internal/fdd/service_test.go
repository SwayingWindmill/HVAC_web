package fdd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

type memoryStore struct {
	rules    []RuleRevision
	topology Topology
	findings []intelligencemodel.FDDFinding
}

// testGates are the approved default gates (migration 033): a chiller above 10% of its rated
// power, a load ratio of at least 15%, and 70% of the window for both operating and low minutes.
func testGates() OperatingGates {
	return OperatingGates{
		MinimumOperatingShare: 0.7, MinimumLowShare: 0.7, TemperatureHoldMinutes: 2, GateHoldMinutes: 10,
		ChillerRunning:          &ChillerRunningCondition{MinimumRatedPowerFraction: 0.1},
		LoadRatio:               &LoadRatioCondition{Minimum: 0.15},
		ChilledWaterPumpRunning: &PumpRunningCondition{},
	}
}

// testRule is the Tenant's first low-delta-T revision: 5 K over 15 minutes under the default gates.
var testRule = RuleRevision{
	ID: "01990000-9500-7000-8000-000000000001", Revision: 1, MinimumDeltaTC: 5, WindowMinutes: 15,
	OperatingGates: testGates(), EffectiveFrom: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
}

const testChillerMeter = "01990000-6100-7000-8000-000000000002"

// testTopology is one chiller whose meter reports run state; its rated values are unknown.
func testTopology() Topology {
	return Topology{Chillers: []GateAsset{{AssetID: "01990000-6000-7000-8000-000000000002", DeviceIDs: []string{testChillerMeter}}}}
}

func newStore() *memoryStore {
	return &memoryStore{rules: []RuleRevision{testRule}, topology: testTopology()}
}

func (store *memoryStore) OperatingTopology(context.Context, string, string, time.Time) (Topology, error) {
	return store.topology, nil
}

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
	samples []Sample
	// steady answers any window with the steady loop, for evaluations at several times.
	steady bool
	err    error
	query  WindowQuery
}

func (source *loopEvidence) WindowSamples(_ context.Context, query WindowQuery) ([]Sample, error) {
	source.query = query
	if source.steady {
		minutes := make([]loopMinute, int(query.To.Sub(query.From)/time.Minute))
		for index := range minutes {
			minutes[index] = loopMinute{supply: 7, ret: 10, running: true}
		}
		samples := loopSamples(EvaluationRequest{DeviceID: query.LoopDeviceID, At: query.To}, minutes)
		// Every chiller and pump the Site binds runs, whichever the topology names.
		for _, device := range query.GateDeviceIDs {
			for minute := query.From; minute.Before(query.To); minute = minute.Add(time.Minute) {
				samples = append(samples, Sample{DeviceID: device, Key: runStateKey, Minute: minute, PointType: "TELEMETRY", ValueType: "STRING", Quality: "GOOD", Text: "RUNNING"})
			}
		}
		return samples, nil
	}
	return source.samples, source.err
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

// loopMinute is one minute of a loop: its supply and return and whether the chiller runs.
type loopMinute struct {
	supply, ret float64
	running     bool
}

// steadyLoop is a whole window of the chiller running with supply 7 and return 10 (3 K).
func steadyLoop() []loopMinute {
	minutes := make([]loopMinute, 15)
	for index := range minutes {
		minutes[index] = loopMinute{supply: 7, ret: 10, running: true}
	}
	return minutes
}

// loopSamples lays minutes out over the window ending at the request, with observation IDs that
// name the minute and key so evidence can be traced.
func loopSamples(request EvaluationRequest, minutes []loopMinute) []Sample {
	from := request.At.Truncate(time.Minute).Add(-time.Duration(len(minutes)) * time.Minute)
	var samples []Sample
	for index, minute := range minutes {
		at := from.Add(time.Duration(index) * time.Minute)
		temperature := func(key string, kind int, value float64) Sample {
			return Sample{DeviceID: request.DeviceID, Key: key, Minute: at, ObservationID: fmt.Sprintf("01990000-62%02d-7000-8000-%012d", kind, index),
				PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", Quality: "GOOD", Number: value, HasNumber: true}
		}
		state := "STOPPED"
		if minute.running {
			state = "RUNNING"
		}
		samples = append(samples,
			temperature(supplyTemperatureKey, 1, minute.supply), temperature(returnTemperatureKey, 2, minute.ret),
			Sample{DeviceID: testChillerMeter, Key: runStateKey, Minute: at, ObservationID: fmt.Sprintf("01990000-6203-7000-8000-%012d", index), PointType: "TELEMETRY", ValueType: "STRING", Quality: "GOOD", Text: state})
	}
	return samples
}

func validSamples(request EvaluationRequest) []Sample { return loopSamples(request, steadyLoop()) }

func newTestService(t *testing.T, store Store, evidence EvidenceSource, now func() time.Time) *Service {
	t.Helper()
	service, err := NewService(store, evidence, now)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestLowDeltaTRecordsAFindingFromTheWindowsOperatingMinutes(t *testing.T) {
	store := newStore()
	request := validEvaluation()
	evidence := &loopEvidence{samples: validSamples(request)}
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
	if finding.FindingType != "CHILLED_WATER_LOW_DELTA_T" || finding.RuleRevisionID != testRule.ID || !reflect.DeepEqual(result.Rule, testRule) || finding.Confidence <= 0.5 {
		t.Fatalf("finding=%#v", finding)
	}
	// A finding states what was measured against what threshold, so it reads without its evidence.
	if finding.ObservedValue != 3 || finding.ThresholdValue != 5 || finding.ValueUnit != "K" {
		t.Fatalf("finding measurement=%v/%v %q", finding.ObservedValue, finding.ThresholdValue, finding.ValueUnit)
	}
	// The evidence is every low minute's supply and return observation.
	if len(finding.EvidenceIDs) != 30 || finding.EvidenceIDs[0] != "01990000-6201-7000-8000-000000000000" || finding.EvidenceIDs[1] != "01990000-6202-7000-8000-000000000000" {
		t.Fatalf("evidence IDs must be authoritative observation IDs: %#v", finding.EvidenceIDs)
	}
	if result.Window.OperatingMinutes != 15 || result.Window.LowShare != 1 || result.Window.OperatingShare != 1 {
		t.Fatalf("window=%#v", result.Window)
	}
	// The rule's window, not the caller, decides how far back the evidence is read; the gate hold
	// reaches further back so a five-minute power sample can stand for the window's first minutes.
	want := WindowQuery{TenantID: request.TenantID, SiteID: request.SiteID, LoopDeviceID: request.DeviceID, GateDeviceIDs: []string{testChillerMeter},
		From: request.At.Add(-25 * time.Minute), To: request.At}
	if !reflect.DeepEqual(evidence.query, want) {
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
	service := newTestService(t, store, &loopEvidence{samples: validSamples(request)}, func() time.Time { return request.At })
	first, err := service.EvaluateLowDeltaT(t.Context(), request)
	if err != nil || first.Finding.ThresholdValue != 5 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	revised := RuleRevision{ID: "01990000-9500-7000-8000-000000000002", Revision: 2, MinimumDeltaTC: 4, WindowMinutes: 30, OperatingGates: testGates(), EffectiveFrom: request.At.Add(time.Minute)}
	store.rules = append(store.rules, revised)
	later := request
	later.At = request.At.Add(15 * time.Minute)
	evidence := &loopEvidence{samples: loopSamples(later, append(steadyLoop(), steadyLoop()...))}
	service = newTestService(t, store, evidence, func() time.Time { return later.At })
	second, err := service.EvaluateLowDeltaT(t.Context(), later)
	if err != nil || second.Status != "FINDING" || !reflect.DeepEqual(second.Rule, revised) || second.Finding.RuleRevisionID != revised.ID || second.Finding.ThresholdValue != 4 {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	if !evidence.query.From.Equal(later.At.Add(-40 * time.Minute)) {
		t.Fatalf("revised window from=%s", evidence.query.From)
	}
	// At its own time the first evaluation's revision still decides.
	if rule, err := service.store.CurrentRule(t.Context(), request.TenantID, request.SiteID, request.At); err != nil || !reflect.DeepEqual(rule, testRule) {
		t.Fatalf("rule at the first evaluation=%#v err=%v", rule, err)
	}
}

func TestATenantWithoutTheRuleIsNotEvaluated(t *testing.T) {
	store := &memoryStore{}
	request := validEvaluation()
	service := newTestService(t, store, &loopEvidence{samples: validSamples(request)}, time.Now)
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
	minutes := steadyLoop()
	for index := range minutes {
		minutes[index].ret = 13
	}
	service := newTestService(t, store, &loopEvidence{samples: loopSamples(request, minutes)}, time.Now)

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
	service := newTestService(t, store, &loopEvidence{steady: true}, func() time.Time { return now })
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

	service := newTestService(t, store, &loopEvidence{}, time.Now)
	if _, err := service.EvaluateLowDeltaT(t.Context(), request); !errors.Is(err, ErrNoLoopEvidence) {
		t.Fatalf("missing evidence err=%v", err)
	}
	for name, spoil := range map[string]func(*Sample){
		"non-GOOD":    func(sample *Sample) { sample.Quality = "BAD" },
		"non-Celsius": func(sample *Sample) { sample.Unit = "K" },
	} {
		samples := validSamples(request)
		for index := range samples {
			if samples[index].Key == returnTemperatureKey {
				spoil(&samples[index])
			}
		}
		service = newTestService(t, store, &loopEvidence{samples: samples}, time.Now)
		if _, err := service.EvaluateLowDeltaT(t.Context(), request); !errors.Is(err, ErrNoLoopEvidence) {
			t.Fatalf("%s evidence err=%v", name, err)
		}
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
	service := newTestService(t, store, &loopEvidence{samples: validSamples(request)}, time.Now)
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

// A plant that runs under 70% of the window is not operating: no finding, whatever its delta-T.
func TestALoopThatIsNotOperatingIsNotDiagnosed(t *testing.T) {
	store := newStore()
	request := validEvaluation()
	minutes := steadyLoop()
	for index := 9; index < 15; index++ {
		minutes[index].running = false
	}
	service := newTestService(t, store, &loopEvidence{samples: loopSamples(request, minutes)}, time.Now)
	result, err := service.EvaluateLowDeltaT(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusNotOperating || result.Finding != nil || len(store.findings) != 0 || result.Window.OperatingMinutes != 9 || result.DeltaTC != 3 {
		t.Fatalf("result=%+v findings=%d", result, len(store.findings))
	}
}
