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
	"github.com/quanlaihe/hvac-web/libs/planttwin"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

type memoryStore struct {
	rules    []RuleRevision
	topology Topology
	twin     *planttwin.SiteTwin
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

func (store *memoryStore) TwinLoader() planttwin.Loader {
	if store != nil && store.twin != nil {
		loader, err := planttwin.NewStaticLoader(*store.twin)
		if err != nil {
			panic(err)
		}
		return loader
	}
	req := validEvaluation()
	twin := planttwin.SiteTwin{
		Scope: planttwin.SiteScope{TenantID: req.TenantID, SiteID: req.SiteID},
	}
	if store != nil {
		appendUnits := func(assets []GateAsset, assetType string) {
			for idx, asset := range assets {
				unit := planttwin.PlantUnit{
					AssetID:                asset.AssetID,
					AssetCode:              fmt.Sprintf("%s-%d", assetType, idx+1),
					AssetName:              asset.AssetID,
					AssetType:              assetType,
					FeederAuxiliaryPowerKW: asset.FeederAuxiliaryPowerKW,
				}
				if asset.RatedPowerKW > 0 {
					v := asset.RatedPowerKW
					unit.RatedPowerKW = &v
				}
				if asset.RatedCapacityKW > 0 {
					v := asset.RatedCapacityKW
					unit.RatedCapacityKW = &v
				}
				twin.Units = append(twin.Units, unit)
				for _, devID := range asset.DeviceIDs {
					twin.Devices = append(twin.Devices, planttwin.TwinDevice{
						DeviceID:   devID,
						DeviceCode: devID,
						DeviceType: "METER",
					})
					twin.DeviceBindings = append(twin.DeviceBindings, planttwin.DeviceBinding{
						AssetID:     asset.AssetID,
						DeviceID:    devID,
						BindingRole: "METER",
					})
				}
			}
		}
		appendUnits(store.topology.Chillers, planttwin.AssetTypeChiller)
		appendUnits(store.topology.Pumps, planttwin.AssetTypeChilledWaterPump)
	}
	loader, err := planttwin.NewStaticLoader(twin)
	if err != nil {
		panic(err)
	}
	return loader
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
	var twins planttwin.Loader
	if provider, ok := store.(interface{ TwinLoader() planttwin.Loader }); ok {
		twins = provider.TwinLoader()
	}
	service, err := NewService(store, twins, evidence, now)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func newTestServiceWithTwin(t *testing.T, store Store, twins ...planttwin.SiteTwin) (*Service, *loopEvidence) {
	t.Helper()
	loader, err := planttwin.NewStaticLoader(twins...)
	if err != nil {
		t.Fatal(err)
	}
	evidence := &loopEvidence{}
	service, err := NewService(store, loader, evidence, func() time.Time { return validEvaluation().At.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	return service, evidence
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
	want := WindowQuery{
		TenantID: request.TenantID, SiteID: request.SiteID, LoopDeviceID: request.DeviceID, GateDeviceIDs: []string{testChillerMeter},
		Points: []PointRef{
			{DeviceID: request.DeviceID, Key: coolingLoadKey},
			{DeviceID: request.DeviceID, Key: returnTemperatureKey},
			{DeviceID: request.DeviceID, Key: supplyTemperatureKey},
			{DeviceID: testChillerMeter, Key: powerTotalKey},
			{DeviceID: testChillerMeter, Key: runStateKey},
		},
		From: request.At.Add(-25 * time.Minute), To: request.At,
	}
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

func floatPtr(v float64) *float64 { return &v }

func TestEvaluateLowDeltaTSplitSensorsAndSemanticRolesWithStaticLoader(t *testing.T) {
	req := validEvaluation()
	loopID := req.AssetID
	chillerID := "019a0000-3000-7000-8000-000000000001"
	chwpID := "019a0000-3000-7000-8000-000000000002"
	tempDev := "019a0000-4000-7000-8000-000000000001"
	btuDev := "019a0000-4000-7000-8000-000000000002"
	meterDev := "019a0000-4000-7000-8000-000000000003"
	pumpCtrl := "019a0000-4000-7000-8000-000000000004"

	twin := planttwin.SiteTwin{
		Scope:    planttwin.SiteScope{TenantID: req.TenantID, SiteID: req.SiteID},
		Timezone: "Asia/Shanghai",
		Units: []planttwin.PlantUnit{
			{AssetID: loopID, AssetCode: "chw-loop", AssetType: planttwin.AssetTypeChilledWaterLoop},
			{
				AssetID: chillerID, AssetCode: "chiller-1", AssetType: planttwin.AssetTypeChiller,
				RatedPowerKW: floatPtr(127.5), RatedCapacityKW: floatPtr(572), FeederAuxiliaryPowerKW: 30,
			},
			{AssetID: chwpID, AssetCode: "chwp-1", AssetType: planttwin.AssetTypeChilledWaterPump},
		},
		HydraulicEdges: []planttwin.HydraulicEdge{
			{FromAssetID: chwpID, ToAssetID: chillerID, RelationshipRole: planttwin.RelationshipServes},
			{FromAssetID: chillerID, ToAssetID: loopID, RelationshipRole: planttwin.RelationshipServes},
		},
		PointBindings: []planttwin.PointBinding{
			{PointID: "p-sup", DeviceID: tempDev, DeviceCode: "temp-mod", PointCode: "probe_t18", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeAsset, AssetID: loopID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleSupplyWaterTemp},
			{PointID: "p-ret", DeviceID: tempDev, DeviceCode: "temp-mod", PointCode: "probe_t16", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeAsset, AssetID: loopID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleReturnWaterTemp},
			{PointID: "p-load", DeviceID: btuDev, DeviceCode: "btu-calc", PointCode: "loop_kw", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeSite, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCoolingLoad},
			{PointID: "p-ch-kw", DeviceID: meterDev, DeviceCode: "meter-1", PointCode: "custom_kw", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleActivePower},
			{PointID: "p-pump-st", DeviceID: pumpCtrl, DeviceCode: "ctrl-chwp", PointCode: "pump_running", PointType: "STATE", SubjectType: planttwin.SubjectTypeAsset, AssetID: chwpID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleRunState},
		},
	}

	store := newStore()
	service, evidence := newTestServiceWithTwin(t, store, twin)
	from, _ := WindowMinutes(testRule, req.At)
	for index := 0; index < 15; index++ {
		at := from.Add(time.Duration(index) * time.Minute)
		evidence.samples = append(evidence.samples,
			Sample{DeviceID: tempDev, Key: "probe_t18", Minute: at, ObservationID: fmt.Sprintf("obs-sup-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", Quality: "GOOD", Number: 7.0, HasNumber: true},
			Sample{DeviceID: tempDev, Key: "probe_t16", Minute: at, ObservationID: fmt.Sprintf("obs-ret-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", Quality: "GOOD", Number: 9.0, HasNumber: true},
			Sample{DeviceID: btuDev, Key: "loop_kw", Minute: at, ObservationID: fmt.Sprintf("obs-load-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "kW", Quality: "GOOD", Number: 200, HasNumber: true},
			Sample{DeviceID: meterDev, Key: "custom_kw", Minute: at, ObservationID: fmt.Sprintf("obs-kw-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "kW", Quality: "GOOD", Number: 70, HasNumber: true},
			Sample{DeviceID: pumpCtrl, Key: "pump_running", Minute: at, ObservationID: fmt.Sprintf("obs-st-%02d", index), PointType: "STATE", ValueType: "STRING", Quality: "GOOD", Text: "RUNNING"},
		)
	}

	result, err := service.EvaluateLowDeltaT(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "FINDING" || result.DeltaTC != 2 || result.Finding == nil || len(store.findings) != 1 {
		t.Fatalf("expected FINDING with deltaTC=2, got %+v findings=%d", result, len(store.findings))
	}
	if result.Window.OperatingMinutes != 15 || !result.Window.Gates[0].Applied || !result.Window.Gates[1].Applied || !result.Window.Gates[2].Applied {
		t.Fatalf("expected all 3 gates applied, got %+v", result.Window)
	}
	wantPoints := []PointRef{
		{DeviceID: tempDev, Key: "probe_t16"},
		{DeviceID: tempDev, Key: "probe_t18"},
		{DeviceID: btuDev, Key: "loop_kw"},
		{DeviceID: meterDev, Key: "custom_kw"},
		{DeviceID: pumpCtrl, Key: "pump_running"},
	}
	if !reflect.DeepEqual(evidence.query.Points, wantPoints) {
		t.Fatalf("unexpected resolved query points: got=%+v want=%+v", evidence.query.Points, wantPoints)
	}
}

func TestEvaluateLowDeltaTHydraulicLoopIsolation(t *testing.T) {
	req := validEvaluation()
	loop1ID := req.AssetID
	loop2ID := "019a0000-3000-7000-8000-000000000010"
	chiller1ID := "019a0000-3000-7000-8000-000000000001"
	chwp1ID := "019a0000-3000-7000-8000-000000000002"
	chiller2ID := "019a0000-3000-7000-8000-000000000011"
	chwp2ID := "019a0000-3000-7000-8000-000000000012"

	loop1Dev := req.DeviceID
	ch1Meter := "019a0000-4000-7000-8000-000000000001"
	chwp1Ctrl := "019a0000-4000-7000-8000-000000000002"
	loop2Dev := "019a0000-4000-7000-8000-000000000010"
	ch2Meter := "019a0000-4000-7000-8000-000000000011"
	chwp2Ctrl := "019a0000-4000-7000-8000-000000000012"

	twin := planttwin.SiteTwin{
		Scope:    planttwin.SiteScope{TenantID: req.TenantID, SiteID: req.SiteID},
		Timezone: "Asia/Shanghai",
		Units: []planttwin.PlantUnit{
			{AssetID: loop1ID, AssetCode: "chw-loop-1", AssetType: planttwin.AssetTypeChilledWaterLoop},
			{AssetID: chiller1ID, AssetCode: "chiller-1", AssetType: planttwin.AssetTypeChiller, RatedPowerKW: floatPtr(120), RatedCapacityKW: floatPtr(500)},
			{AssetID: chwp1ID, AssetCode: "chwp-1", AssetType: planttwin.AssetTypeChilledWaterPump},
			{AssetID: loop2ID, AssetCode: "chw-loop-2", AssetType: planttwin.AssetTypeChilledWaterLoop},
			{AssetID: chiller2ID, AssetCode: "chiller-2", AssetType: planttwin.AssetTypeChiller, RatedPowerKW: floatPtr(120), RatedCapacityKW: floatPtr(500)},
			{AssetID: chwp2ID, AssetCode: "chwp-2", AssetType: planttwin.AssetTypeChilledWaterPump},
		},
		HydraulicEdges: []planttwin.HydraulicEdge{
			{FromAssetID: chwp1ID, ToAssetID: chiller1ID, RelationshipRole: planttwin.RelationshipServes},
			{FromAssetID: chiller1ID, ToAssetID: loop1ID, RelationshipRole: planttwin.RelationshipServes},
			{FromAssetID: chwp2ID, ToAssetID: chiller2ID, RelationshipRole: planttwin.RelationshipServes},
			{FromAssetID: chiller2ID, ToAssetID: loop2ID, RelationshipRole: planttwin.RelationshipServes},
		},
		PointBindings: []planttwin.PointBinding{
			{PointID: "p-l1-s", DeviceID: loop1Dev, PointCode: "l1_sup", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeAsset, AssetID: loop1ID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleSupplyWaterTemp},
			{PointID: "p-l1-r", DeviceID: loop1Dev, PointCode: "l1_ret", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeAsset, AssetID: loop1ID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleReturnWaterTemp},
			{PointID: "p-l1-q", DeviceID: loop1Dev, PointCode: "l1_load", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeAsset, AssetID: loop1ID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCoolingLoad},
			{PointID: "p-c1-kw", DeviceID: ch1Meter, PointCode: "c1_kw", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeAsset, AssetID: chiller1ID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleActivePower},
			{PointID: "p-p1-st", DeviceID: chwp1Ctrl, PointCode: "p1_st", PointType: "STATE", SubjectType: planttwin.SubjectTypeAsset, AssetID: chwp1ID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleRunState},

			{PointID: "p-l2-s", DeviceID: loop2Dev, PointCode: "l2_sup", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeAsset, AssetID: loop2ID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleSupplyWaterTemp},
			{PointID: "p-l2-r", DeviceID: loop2Dev, PointCode: "l2_ret", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeAsset, AssetID: loop2ID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleReturnWaterTemp},
			{PointID: "p-l2-q", DeviceID: loop2Dev, PointCode: "l2_load", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeAsset, AssetID: loop2ID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCoolingLoad},
			{PointID: "p-c2-kw", DeviceID: ch2Meter, PointCode: "c2_kw", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeAsset, AssetID: chiller2ID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleActivePower},
			{PointID: "p-p2-st", DeviceID: chwp2Ctrl, PointCode: "p2_st", PointType: "STATE", SubjectType: planttwin.SubjectTypeAsset, AssetID: chwp2ID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleRunState},
		},
	}

	store := newStore()
	service, evidence := newTestServiceWithTwin(t, store, twin)
	from, _ := WindowMinutes(testRule, req.At)
	for index := 0; index < 15; index++ {
		at := from.Add(time.Duration(index) * time.Minute)
		evidence.samples = append(evidence.samples,
			// Loop 1 has low delta-T but its own chiller & pump are stopped.
			Sample{DeviceID: loop1Dev, Key: "l1_sup", Minute: at, ObservationID: fmt.Sprintf("l1-s-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", Quality: "GOOD", Number: 10.0, HasNumber: true},
			Sample{DeviceID: loop1Dev, Key: "l1_ret", Minute: at, ObservationID: fmt.Sprintf("l1-r-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", Quality: "GOOD", Number: 10.5, HasNumber: true},
			Sample{DeviceID: loop1Dev, Key: "l1_load", Minute: at, ObservationID: fmt.Sprintf("l1-q-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "kW", Quality: "GOOD", Number: 200, HasNumber: true},
			Sample{DeviceID: ch1Meter, Key: "c1_kw", Minute: at, ObservationID: fmt.Sprintf("c1-kw-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "kW", Quality: "GOOD", Number: 0, HasNumber: true},
			Sample{DeviceID: chwp1Ctrl, Key: "p1_st", Minute: at, ObservationID: fmt.Sprintf("p1-st-%02d", index), PointType: "STATE", ValueType: "STRING", Quality: "GOOD", Text: "STOPPED"},
			// Loop 2's chiller & pump ARE running and must not bleed into loop 1.
			Sample{DeviceID: ch2Meter, Key: "c2_kw", Minute: at, ObservationID: fmt.Sprintf("c2-kw-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "kW", Quality: "GOOD", Number: 80, HasNumber: true},
			Sample{DeviceID: chwp2Ctrl, Key: "p2_st", Minute: at, ObservationID: fmt.Sprintf("p2-st-%02d", index), PointType: "STATE", ValueType: "STRING", Quality: "GOOD", Text: "RUNNING"},
		)
	}

	result, err := service.EvaluateLowDeltaT(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusNotOperating || result.Window.OperatingMinutes != 0 || len(store.findings) != 0 {
		t.Fatalf("expected loop 1 NOT_OPERATING without loop 2 interference, got %+v findings=%d", result, len(store.findings))
	}
	for _, ref := range evidence.query.Points {
		if ref.DeviceID == ch2Meter || ref.DeviceID == chwp2Ctrl || ref.DeviceID == loop2Dev {
			t.Fatalf("loop 2 device %s leaked into loop 1 query: %+v", ref.DeviceID, evidence.query.Points)
		}
	}

	// Evaluating via chiller1ID resolves to loop1 via CoolingLoopsForChiller and still isolates loop 2.
	viaChiller := req
	viaChiller.AssetID = chiller1ID
	chillerResult, err := service.EvaluateLowDeltaT(t.Context(), viaChiller)
	if err != nil {
		t.Fatal(err)
	}
	if chillerResult.Status != StatusNotOperating || chillerResult.Window.OperatingMinutes != 0 {
		t.Fatalf("expected chiller-linked loop 1 NOT_OPERATING, got %+v", chillerResult)
	}
}

func TestEvaluateLowDeltaTScheduleSuppressionAndUnconfigured(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	baseReq := validEvaluation()
	baseTwin := func() planttwin.SiteTwin {
		return planttwin.SiteTwin{
			Scope:    planttwin.SiteScope{TenantID: baseReq.TenantID, SiteID: baseReq.SiteID},
			Timezone: "Asia/Shanghai",
			Units: []planttwin.PlantUnit{
				{AssetID: baseReq.AssetID, AssetCode: "chw-loop", AssetType: planttwin.AssetTypeChilledWaterLoop},
				{AssetID: "019a0000-3000-7000-8000-000000000001", AssetCode: "chiller-1", AssetType: planttwin.AssetTypeChiller},
			},
			HydraulicEdges: []planttwin.HydraulicEdge{
				{FromAssetID: "019a0000-3000-7000-8000-000000000001", ToAssetID: baseReq.AssetID, RelationshipRole: planttwin.RelationshipServes},
			},
			PointBindings: []planttwin.PointBinding{
				{PointID: "p-sup", DeviceID: baseReq.DeviceID, PointCode: "sup_t", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeAsset, AssetID: baseReq.AssetID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleSupplyWaterTemp},
				{PointID: "p-ret", DeviceID: baseReq.DeviceID, PointCode: "ret_t", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeAsset, AssetID: baseReq.AssetID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleReturnWaterTemp},
				{PointID: "p-run", DeviceID: testChillerMeter, PointCode: "ch_run", PointType: "STATE", SubjectType: planttwin.SubjectTypeAsset, AssetID: "019a0000-3000-7000-8000-000000000001", BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleRunState},
			},
			Schedule: planttwin.OperatingSchedule{
				ScheduleID:          "019a0000-7100-7000-8000-000000000001",
				Timezone:            "Asia/Shanghai",
				PreCoolLeadMinutes:  30,
				CoastDownLagMinutes: 15,
				Revision:            1,
				Slots: []planttwin.ScheduleSlot{
					{DayOfWeek: 1, StartMinute: 8*60 + 30, EndMinute: 18 * 60}, // Mon 08:30-18:00
				},
				Exceptions: []planttwin.ScheduleException{
					{ID: "exc-maint", Title: "检修", Kind: planttwin.ExceptionMaintenanceWindow, StartDate: "2026-08-24", EndDate: "2026-08-24", Reason: "Condenser wash"},
					{ID: "exc-hol", Title: "假日", Kind: planttwin.ExceptionHolidayShutdown, StartDate: "2026-08-31", EndDate: "2026-08-31", Reason: "Holiday"},
				},
			},
		}
	}

	buildLowSamples := func(at time.Time) []Sample {
		from, _ := WindowMinutes(testRule, at)
		out := make([]Sample, 0, 45)
		for index := 0; index < 15; index++ {
			m := from.Add(time.Duration(index) * time.Minute)
			out = append(out,
				Sample{DeviceID: baseReq.DeviceID, Key: "sup_t", Minute: m, ObservationID: fmt.Sprintf("s-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", Quality: "GOOD", Number: 7.0, HasNumber: true},
				Sample{DeviceID: baseReq.DeviceID, Key: "ret_t", Minute: m, ObservationID: fmt.Sprintf("r-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", Quality: "GOOD", Number: 9.0, HasNumber: true},
				Sample{DeviceID: testChillerMeter, Key: "ch_run", Minute: m, ObservationID: fmt.Sprintf("u-%02d", index), PointType: "STATE", ValueType: "STRING", Quality: "GOOD", Text: "RUNNING"},
			)
		}
		return out
	}

	for _, tc := range []struct {
		name           string
		atLocal        time.Time
		emptyEvidence  bool
		wantSuppressed bool
		wantReason     string
		wantState      string
		wantStatus     string
	}{
		{
			name:           "maintenance window suppresses even with empty samples",
			atLocal:        time.Date(2026, 8, 24, 10, 0, 0, 0, shanghai),
			emptyEvidence:  true,
			wantSuppressed: true,
			wantReason:     planttwin.ExceptionMaintenanceWindow,
			wantState:      planttwin.OperatingStateExceptionClosed,
			wantStatus:     StatusNotOperating,
		},
		{
			name:           "holiday shutdown suppresses even with running low delta-T",
			atLocal:        time.Date(2026, 8, 31, 10, 0, 0, 0, shanghai),
			wantSuppressed: true,
			wantReason:     planttwin.ExceptionHolidayShutdown,
			wantState:      planttwin.OperatingStateExceptionClosed,
			wantStatus:     StatusNotOperating,
		},
		{
			name:           "outside schedule suppresses off-hours",
			atLocal:        time.Date(2026, 8, 17, 21, 0, 0, 0, shanghai),
			wantSuppressed: true,
			wantReason:     planttwin.OperatingStateOutsideSchedule,
			wantState:      planttwin.OperatingStateOutsideSchedule,
			wantStatus:     StatusNotOperating,
		},
		{
			name:           "scheduled open evaluates and persists finding",
			atLocal:        time.Date(2026, 8, 17, 10, 0, 0, 0, shanghai),
			wantSuppressed: false,
			wantState:      planttwin.OperatingStateScheduledOpen,
			wantStatus:     "FINDING",
		},
		{
			name:           "pre-cool window evaluates and persists finding",
			atLocal:        time.Date(2026, 8, 17, 8, 15, 0, 0, shanghai),
			wantSuppressed: false,
			wantState:      planttwin.OperatingStatePreCool,
			wantStatus:     "FINDING",
		},
		{
			name:           "coast-down window evaluates and persists finding",
			atLocal:        time.Date(2026, 8, 17, 18, 10, 0, 0, shanghai),
			wantSuppressed: false,
			wantState:      planttwin.OperatingStateCoastDown,
			wantStatus:     "FINDING",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore()
			service, evidence := newTestServiceWithTwin(t, store, baseTwin())
			req := baseReq
			req.At = tc.atLocal.UTC()
			if !tc.emptyEvidence {
				evidence.samples = buildLowSamples(req.At)
			}
			result, evalErr := service.EvaluateLowDeltaT(t.Context(), req)
			if evalErr != nil {
				t.Fatal(evalErr)
			}
			if result.Status != tc.wantStatus || result.Window.Suppressed != tc.wantSuppressed ||
				result.Window.SuppressionReason != tc.wantReason || result.Window.ScheduleState != tc.wantState {
				t.Fatalf("unexpected result=%+v", result)
			}
			if tc.wantSuppressed && (len(store.findings) != 0 || result.Finding != nil || result.Window.Gates[3].Gate != OperatingScheduleGate) {
				t.Fatalf("suppressed evaluation must not persist finding and must record schedule gate: %+v", result)
			}
			if !tc.wantSuppressed && (len(store.findings) != 1 || result.Finding == nil) {
				t.Fatalf("scheduled open evaluation should persist finding: %+v", result)
			}
		})
	}

	t.Run("unconfigured schedule stays unblocked", func(t *testing.T) {
		twin := baseTwin()
		twin.Timezone = ""
		twin.Schedule = planttwin.OperatingSchedule{}
		store := newStore()
		service, evidence := newTestServiceWithTwin(t, store, twin)
		req := baseReq
		req.At = time.Date(2026, 8, 17, 21, 0, 0, 0, shanghai).UTC()
		evidence.samples = buildLowSamples(req.At)

		result, evalErr := service.EvaluateLowDeltaT(t.Context(), req)
		if evalErr != nil {
			t.Fatal(evalErr)
		}
		if result.Status != "FINDING" || result.Window.Suppressed || len(store.findings) != 1 {
			t.Fatalf("unconfigured schedule should evaluate normally, got %+v", result)
		}
	})

	t.Run("unconfigured schedule with timezone stays unblocked", func(t *testing.T) {
		twin := baseTwin()
		twin.Schedule = planttwin.OperatingSchedule{Timezone: "Asia/Shanghai"}
		store := newStore()
		service, evidence := newTestServiceWithTwin(t, store, twin)
		req := baseReq
		req.At = time.Date(2026, 8, 17, 21, 0, 0, 0, shanghai).UTC()
		evidence.samples = buildLowSamples(req.At)

		result, evalErr := service.EvaluateLowDeltaT(t.Context(), req)
		if evalErr != nil {
			t.Fatal(evalErr)
		}
		if result.Status != "FINDING" || result.Window.Suppressed || len(store.findings) != 1 {
			t.Fatalf("unconfigured schedule with timezone should evaluate normally, got %+v", result)
		}
	})

	t.Run("invalid timezone on configured schedule fails explicitly", func(t *testing.T) {
		twin := baseTwin()
		twin.Timezone = "Invalid/Zone"
		twin.Schedule.Timezone = "Invalid/Zone"
		store := newStore()
		service, evidence := newTestServiceWithTwin(t, store, twin)
		evidence.samples = buildLowSamples(baseReq.At)
		if _, evalErr := service.EvaluateLowDeltaT(t.Context(), baseReq); !errors.Is(evalErr, planttwin.ErrInvalidTimezone) {
			t.Fatalf("expected ErrInvalidTimezone, got %v", evalErr)
		}
	})

	t.Run("configured schedule slots with empty timezone fail explicitly", func(t *testing.T) {
		twin := baseTwin()
		twin.Timezone = ""
		twin.Schedule.Timezone = ""
		store := newStore()
		service, evidence := newTestServiceWithTwin(t, store, twin)
		evidence.samples = buildLowSamples(baseReq.At)
		if _, evalErr := service.EvaluateLowDeltaT(t.Context(), baseReq); !errors.Is(evalErr, planttwin.ErrInvalidTimezone) {
			t.Fatalf("expected ErrInvalidTimezone when schedule slots lack timezone, got %v", evalErr)
		}
	})
}

func TestEvaluateLowDeltaTLegacyAndPartialSemanticFallbacks(t *testing.T) {
	req := validEvaluation()
	chillerID := "019a0000-3000-7000-8000-000000000001"

	t.Run("device bindings fallback when zero semantic roles exist", func(t *testing.T) {
		twin := planttwin.SiteTwin{
			Scope: planttwin.SiteScope{TenantID: req.TenantID, SiteID: req.SiteID},
			Units: []planttwin.PlantUnit{
				{AssetID: chillerID, AssetCode: "chiller-1", AssetType: planttwin.AssetTypeChiller},
			},
			Devices: []planttwin.TwinDevice{
				{DeviceID: testChillerMeter, DeviceCode: "meter-1", DeviceType: "METER"},
			},
			DeviceBindings: []planttwin.DeviceBinding{
				{AssetID: chillerID, DeviceID: testChillerMeter, BindingRole: "METER"},
			},
		}
		store := newStore()
		service, evidence := newTestServiceWithTwin(t, store, twin)
		evidence.samples = validSamples(req)
		result, err := service.EvaluateLowDeltaT(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "FINDING" || !result.Window.Gates[0].Applied || result.Window.Gates[0].PassedMinutes != 15 {
			t.Fatalf("expected legacy DeviceBindings fallback to apply chiller gate, got %+v", result)
		}
	})

	t.Run("semantic twin without cooling load role does not fall back to raw cooling load key", func(t *testing.T) {
		twin := planttwin.SiteTwin{
			Scope: planttwin.SiteScope{TenantID: req.TenantID, SiteID: req.SiteID},
			Units: []planttwin.PlantUnit{
				{AssetID: req.AssetID, AssetCode: "loop-1", AssetType: planttwin.AssetTypeChilledWaterLoop},
				{AssetID: chillerID, AssetCode: "chiller-1", AssetType: planttwin.AssetTypeChiller, RatedPowerKW: floatPtr(200), RatedCapacityKW: floatPtr(1000)},
			},
			HydraulicEdges: []planttwin.HydraulicEdge{
				{FromAssetID: chillerID, ToAssetID: req.AssetID, RelationshipRole: planttwin.RelationshipServes},
			},
			PointBindings: []planttwin.PointBinding{
				{PointID: "p-sup", DeviceID: req.DeviceID, PointCode: "custom_sup", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeAsset, AssetID: req.AssetID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleSupplyWaterTemp},
				{PointID: "p-ret", DeviceID: req.DeviceID, PointCode: "custom_ret", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeAsset, AssetID: req.AssetID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleReturnWaterTemp},
				{PointID: "p-kw", DeviceID: testChillerMeter, PointCode: "custom_kw", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleActivePower},
			},
		}
		store := newStore()
		service, evidence := newTestServiceWithTwin(t, store, twin)
		from, _ := WindowMinutes(testRule, req.At)
		for index := 0; index < 15; index++ {
			at := from.Add(time.Duration(index) * time.Minute)
			evidence.samples = append(evidence.samples,
				Sample{DeviceID: req.DeviceID, Key: "custom_sup", Minute: at, ObservationID: fmt.Sprintf("s-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", Quality: "GOOD", Number: 7.0, HasNumber: true},
				Sample{DeviceID: req.DeviceID, Key: "custom_ret", Minute: at, ObservationID: fmt.Sprintf("r-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", Quality: "GOOD", Number: 9.0, HasNumber: true},
				// Unmapped raw key instant_cooling_capacity must be ignored once semantic roles are active.
				Sample{DeviceID: req.DeviceID, Key: coolingLoadKey, Minute: at, ObservationID: fmt.Sprintf("q-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "kW", Quality: "GOOD", Number: 500, HasNumber: true},
				Sample{DeviceID: testChillerMeter, Key: "custom_kw", Minute: at, ObservationID: fmt.Sprintf("kw-%02d", index), PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "kW", Quality: "GOOD", Number: 80, HasNumber: true},
			)
		}
		result, err := service.EvaluateLowDeltaT(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if result.Window.Gates[1].Applied || result.Window.Gates[1].SkippedReason != SkippedNoCoolingLoad {
			t.Fatalf("expected LoadRatio skipped with NO_COOLING_LOAD_READING, got %+v", result.Window.Gates[1])
		}
	})
}
