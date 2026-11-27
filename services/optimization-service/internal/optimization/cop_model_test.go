package optimization

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/planttwin"
)

type fixedPlantHistory struct {
	buckets []PlantBucket
	err     error
	queries []PlantHistoryQuery
}

func (history *fixedPlantHistory) PlantBuckets(_ context.Context, query PlantHistoryQuery) ([]PlantBucket, error) {
	history.queries = append(history.queries, query)
	return history.buckets, history.err
}

// syntheticPlant runs two Chillers whose COP is a known function, sharing the load by an equal
// evaporator flow, for the given number of 15-minute buckets ending at `to`.
func syntheticPlant(to time.Time, buckets int, withCondenser bool) []PlantBucket {
	out := []PlantBucket{}
	cop := func(plr, supply, condenser float64) float64 {
		return 2 + 6*plr - 3*plr*plr + 0.15*supply - 0.05*condenser
	}
	for index := 0; index < buckets; index++ {
		at := to.Add(time.Duration(index-buckets) * 15 * time.Minute)
		phase := float64(index)
		supply := 6 + 2*math.Sin(phase/7)
		condenser := 30 + 4*math.Sin(phase/11)
		drops := []float64{2 + math.Sin(phase/5), 3 + math.Cos(phase/3)}
		load := 300 + 150*math.Sin(phase/13)
		for chiller, id := range []string{"chiller-a", "chiller-b"} {
			share := drops[chiller] / (drops[0] + drops[1]) * load
			power := share / cop(share/300, supply, condenser)
			bucket := PlantBucket{Bucket: at, DeviceID: id, PowerKW: ptr(power), ChwLeavingC: ptr(supply), ChwEnteringC: ptr(supply + drops[chiller])}
			if withCondenser {
				bucket.CwLeavingC = ptr(condenser)
			}
			out = append(out, bucket)
		}
		out = append(out, PlantBucket{Bucket: at, DeviceID: "btu-meter", CoolingKW: ptr(load)})
	}
	return out
}

func copRequest() Request {
	request := validRequest()
	request.Baseline.ZoneTempC = 23
	request.ResponseModel.ZoneTempDeltaPerSupplyTempC = 0.4
	return request
}

func TestChillerCOPModelFitsEachChillerAndKeepsTheFitInItsEvidence(t *testing.T) {
	at := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	history := &fixedPlantHistory{buckets: syntheticPlant(at, 14*96, true)}
	evaluation := (COPModelSolver{History: history}).Evaluate(t.Context(), copRequest(), at)
	if evaluation.Outcome != ShadowFeasible || evaluation.Recommendation == nil {
		t.Fatalf("evaluation=%+v", evaluation)
	}
	if len(history.queries) != 1 || !history.queries[0].To.Equal(at) || !history.queries[0].From.Equal(at.AddDate(0, 0, -14)) {
		t.Fatalf("history window=%+v", history.queries)
	}
	if evaluation.Fit.History.LoadAllocation["evaporatorDeltaT"] == 0 || evaluation.Fit.History.Chillers != 2 {
		t.Fatalf("history evidence=%+v", evaluation.Fit.History)
	}
	for _, chiller := range evaluation.Fit.Chillers {
		if chiller.Status != ChillerFitted || chiller.CoefficientSource != "FITTED_FROM_HISTORY" {
			t.Fatalf("chiller=%+v", chiller)
		}
		if math.Abs(chiller.Coefficients["chilledWaterSupplyTempC"]-0.15) > 1e-6 || math.Abs(chiller.Coefficients["condenserReturnTempC"]+0.05) > 1e-6 {
			t.Fatalf("coefficients=%v", chiller.Coefficients)
		}
		if *chiller.CVRMSEPower > 1e-6 {
			t.Fatalf("exact plant should fit exactly, CV(RMSE)=%v", *chiller.CVRMSEPower)
		}
	}
	if evaluation.CVRMSE == nil || *evaluation.CVRMSE > 1e-6 {
		t.Fatalf("plant CV(RMSE)=%v", evaluation.CVRMSE)
	}
	// A warmer supply raises COP, so the solver takes the full step the comfort band allows.
	recommendation := evaluation.Recommendation
	if recommendation.Candidate.SupplyTempC != 8 || recommendation.ExpectedImpact.EnergySavingKWhPerDay <= 0 {
		t.Fatalf("recommendation=%+v", recommendation)
	}
	if evaluation.EnergyResponse == nil || evaluation.EnergyResponse.Source != "FITTED_FROM_HISTORY" || evaluation.EnergyResponse.DailyEnergyDeltaKWhPerSupplyTempC >= 0 {
		t.Fatalf("energy response=%v", evaluation.EnergyResponse)
	}
	if evaluation.ZoneResponse == nil || evaluation.ZoneResponse.Source != "POLICY" || evaluation.CondenserReset == nil || evaluation.CondenserReset.Reason != "NO_CONDENSER_SETPOINT" {
		t.Fatalf("zone=%v condenser=%v", evaluation.ZoneResponse, evaluation.CondenserReset)
	}
	if err := recommendation.CanCreateCommand(at.Add(time.Minute)); err == nil {
		t.Fatal("shadow recommendation must never create a command")
	}
}

func TestChillerCOPModelDropsTheCondenserTermWhenNoCondenserReturnIsMeasured(t *testing.T) {
	at := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	fit := FitPlant(syntheticPlant(at, 400, false), at.Add(-400*15*time.Minute), at)
	for _, chiller := range fit.Chillers {
		if _, ok := chiller.Coefficients["condenserReturnTempC"]; ok || chiller.Status != ChillerFitted {
			t.Fatalf("chiller=%+v", chiller)
		}
	}
}

func TestChillerCOPModelRejectsAFitWithTheWrongPhysicalSign(t *testing.T) {
	at := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	buckets := syntheticPlant(at, 400, true)
	// Make COP fall as supply warms: scale power up with supply temperature.
	for index := range buckets {
		if buckets[index].PowerKW != nil {
			*buckets[index].PowerKW *= 1 + 0.2*(*buckets[index].ChwLeavingC-6)
		}
	}
	fit := FitPlant(buckets, at.Add(-400*15*time.Minute), at)
	for _, chiller := range fit.Chillers {
		if chiller.Status != ChillerRejected || !strings.Contains(chiller.Reason, "warmer chilled-water supply") {
			t.Fatalf("chiller=%+v", chiller)
		}
	}
	evaluation := (COPModelSolver{History: &fixedPlantHistory{buckets: buckets}}).Evaluate(t.Context(), copRequest(), at)
	if evaluation.Outcome != ShadowNoUsableModel || evaluation.Recommendation != nil {
		t.Fatalf("evaluation=%+v", evaluation)
	}
}

func TestChillerCOPModelSetsAsideImplausibleAndStartingBuckets(t *testing.T) {
	at := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	buckets := syntheticPlant(at, 50, true)
	// A supply reading of 30 ℃ is a wrong channel, not a chiller.
	for index := range buckets {
		if buckets[index].DeviceID == "chiller-a" && buckets[index].Bucket.Before(at.Add(-40*15*time.Minute)) {
			buckets[index].ChwLeavingC = ptr(30)
		}
		// chiller-b stops for one bucket, so its next bucket is a start-up, not a steady point.
		if buckets[index].DeviceID == "chiller-b" && buckets[index].Bucket.Equal(at.Add(-5*15*time.Minute)) {
			buckets[index].PowerKW = ptr(0)
		}
	}
	fit := FitPlant(buckets, at.Add(-50*15*time.Minute), at)
	if fit.History.SetAside["implausibleReading"] != 10 || fit.History.SetAside["startingUp"] != 1 {
		t.Fatalf("set aside=%v", fit.History.SetAside)
	}
	for _, chiller := range fit.Chillers {
		if chiller.Status != ChillerInsufficientData {
			t.Fatalf("chiller=%+v", chiller)
		}
	}
}

func TestChillerCOPModelReportsUnavailableHistory(t *testing.T) {
	evaluation := (COPModelSolver{History: &fixedPlantHistory{err: errors.New("clickhouse down")}}).Evaluate(t.Context(), copRequest(), time.Now())
	if evaluation.Outcome != ShadowHistoryUnavailable || evaluation.Recommendation != nil {
		t.Fatalf("evaluation=%+v", evaluation)
	}
	if _, err := (COPModelSolver{History: &fixedPlantHistory{}}).Recommend(t.Context(), copRequest(), time.Now()); !errors.Is(err, errNoShadowRecommendation) {
		t.Fatalf("err=%v", err)
	}
}

func readSongyaxuanPlant(t *testing.T) []PlantBucket {
	t.Helper()
	file, err := os.Open("testdata/songyaxuan-plant-2026-10-16.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	buckets, err := decodePlantBuckets(reader)
	if err != nil {
		t.Fatal(err)
	}
	return buckets
}

// TestChillerCOPModelReplaysSongyaxuan fits songyaxuan's 14 days to 2026-10-30 00:00Z, read
// read-only from production Telemetry History. Before 2026-10-22 its chiller meters carried
// the wrong temperature channels; the plausibility filter sets those buckets aside.
func TestChillerCOPModelReplaysSongyaxuan(t *testing.T) {
	at := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	buckets := readSongyaxuanPlant(t)
	request := copRequest()
	request.Baseline = HVACBaseline{DailyEnergyKWh: 3000, DailyCost: 2400, SupplyTempC: 7, ZoneTempC: 23}
	request.ResponseModel.ZoneTempDeltaPerSupplyTempC = 0.4
	evaluation := (COPModelSolver{History: &fixedPlantHistory{buckets: buckets}}).Evaluate(t.Context(), request, at)
	payload, _ := json.MarshalIndent(evaluation, "", "  ")
	t.Logf("songyaxuan shadow evaluation:\n%s", payload)
	fitted := map[string]ChillerFit{}
	for _, chiller := range evaluation.Fit.Chillers {
		fitted[chiller.DeviceID] = chiller
	}
	for _, id := range []string{"01a14000-4000-7000-8000-000000000005", "01a14000-4000-7000-8000-000000000006"} {
		chiller := fitted[id]
		if chiller.Status != ChillerFitted || chiller.Samples < 250 || *chiller.CVRMSEPower > 0.2 {
			t.Fatalf("chiller %s=%+v", id, chiller)
		}
	}
	for _, id := range []string{"01a14000-4000-7000-8000-000000000003", "01a14000-4000-7000-8000-000000000004"} {
		if fitted[id].Status != ChillerInsufficientData {
			t.Fatalf("idle chiller %s=%+v", id, fitted[id])
		}
	}
	if evaluation.Fit.History.SetAside["implausibleReading"] < 200 {
		t.Fatalf("pre-2026-10-22 channel mix-up should be set aside: %v", evaluation.Fit.History.SetAside)
	}
	if evaluation.Outcome != ShadowFeasible || evaluation.CVRMSE == nil {
		t.Fatalf("outcome=%s %s", evaluation.Outcome, evaluation.Reason)
	}
}

func TestPlantHistoryQueryIsScopedAndReadsOnlyWhatHadArrived(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		body = string(payload)
		_, _ = writer.Write([]byte(`{"bucket":"2026-10-29T23:45:00Z","deviceId":"d1","powerKW":90.5,"chwLeavingC":7.1,"chwEnteringC":9.9,"cwLeavingC":null,"coolingKW":null}` + "\n"))
	}))
	defer server.Close()
	reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL, Energy: &energySeriesStub{}})
	if err != nil {
		t.Fatal(err)
	}
	to := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	buckets, err := reader.PlantBuckets(t.Context(), PlantHistoryQuery{TenantID: "t'1", SiteID: "s1", From: to.AddDate(0, 0, -14), To: to})
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].CwLeavingC != nil || *buckets[0].PowerKW != 90.5 {
		t.Fatalf("buckets=%+v", buckets)
	}
	for _, want := range []string{`tenant_id=toUUID('t\'1')`, `site_id=toUUID('s1')`, `received_at <= parseDateTime64BestEffort('2026-10-30T00:00:00.000Z'`, `sampled_at >= parseDateTime64BestEffort('2026-10-16T00:00:00.000Z'`, `'cw_leaving_temperature'`, `quality = 'GOOD'`} {
		if !strings.Contains(body, want) {
			t.Fatalf("query lacks %q:\n%s", want, body)
		}
	}
}

type fixedShadow struct{ evaluation ShadowEvaluation }

func (shadow fixedShadow) Evaluate(context.Context, Request, time.Time) ShadowEvaluation {
	return shadow.evaluation
}

func TestShadowSolverRecordsBesideThePublishedRunWithoutPublishing(t *testing.T) {
	store := &memoryEvaluationStore{}
	sink := &captureEvaluationSink{}
	at := time.Date(2026, 8, 19, 16, 0, 0, 0, time.UTC)
	service, _ := NewDefaultService(store, sink, func() time.Time { return at })
	history := &fixedPlantHistory{buckets: syntheticPlant(at, 14*96, true)}
	service.WithShadow(COPModelSolver{History: history}, time.Second, t.Logf)
	published, err := service.Optimize(t.Context(), copRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(store.begun) != 1 || len(store.completed) != 1 || len(sink.evaluations) != 2 {
		t.Fatalf("begun=%d completed=%d evaluations=%d", len(store.begun), len(store.completed), len(sink.evaluations))
	}
	if store.completed[0].Quality != "FEASIBLE" || store.completed[0].RecommendationID != published.ID {
		t.Fatalf("published evaluation=%+v", store.completed[0])
	}
	shadow := sink.evaluations[1]
	if shadow.Quality != "SHADOW" || shadow.SolverOutcome != ShadowFeasible || shadow.RecommendationID != published.ID || shadow.OptimizationRunID != copRequest().OptimizationRunID {
		t.Fatalf("shadow evaluation=%+v", shadow)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(shadow.EvaluationJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["solver"] != ChillerCOPSolverName || payload["mode"] != "SHADOW" || payload["publishedRecommendationId"] != published.ID || payload["cvRmse"] == nil {
		t.Fatalf("shadow payload keys=%v", payload)
	}
	shadowRecommendation := payload["recommendation"].(map[string]any)
	if shadowRecommendation["id"] == published.ID {
		t.Fatal("the shadow recommendation must not reuse the published id")
	}
}

func TestShadowSolverFailureNeverChangesThePublishedRun(t *testing.T) {
	store := &memoryEvaluationStore{}
	sink := &captureEvaluationSink{}
	service, _ := NewDefaultService(store, sink, time.Now)
	service.WithShadow(fixedShadow{ShadowEvaluation{Solver: ChillerCOPSolverName, Mode: "SHADOW", Outcome: ShadowHistoryUnavailable}}, time.Second, t.Logf)
	if _, err := service.Optimize(t.Context(), copRequest()); err != nil {
		t.Fatal(err)
	}
	if len(sink.evaluations) != 2 || sink.evaluations[1].SolverOutcome != ShadowHistoryUnavailable || len(store.failed) != 0 {
		t.Fatalf("evaluations=%+v failed=%v", sink.evaluations, store.failed)
	}
	// A shadow row that cannot be written is only logged.
	failing := &shadowFailingSink{captureEvaluationSink: captureEvaluationSink{}}
	service, _ = NewDefaultService(store, failing, time.Now)
	logged := []string{}
	service.WithShadow(fixedShadow{ShadowEvaluation{Solver: ChillerCOPSolverName, Outcome: ShadowNoUsableModel}}, time.Second, func(format string, args ...any) { logged = append(logged, format) })
	if _, err := service.Optimize(t.Context(), copRequest()); err != nil {
		t.Fatalf("shadow persistence failure leaked into the published run: %v", err)
	}
	if len(failing.evaluations) != 1 || len(logged) != 1 {
		t.Fatalf("evaluations=%d logged=%v", len(failing.evaluations), logged)
	}
}

func TestNameplateDropsPowerFarAboveThePlate(t *testing.T) {
	at := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	rated := 127.5
	buckets := []PlantBucket{}
	for hour := 0; hour < 3; hour++ {
		when := at.Add(time.Duration(hour) * 15 * time.Minute)
		power := 100.0
		if hour == 2 {
			power = 191.8
		}
		buckets = append(buckets,
			PlantBucket{Bucket: when, DeviceID: "chiller-c", PowerKW: ptr(power), ChwLeavingC: ptr(7), ChwEnteringC: ptr(8), CwLeavingC: ptr(30), RatedPowerKW: &rated},
			PlantBucket{Bucket: when, DeviceID: "btu", CoolingKW: ptr(5.02 * 127.5)},
		)
	}
	fit := FitPlant(buckets, at, at.Add(time.Hour))
	if fit.History.SetAside["aboveNameplate"] != 1 {
		t.Fatalf("set aside=%v", fit.History.SetAside)
	}
	if len(fit.Chillers) != 1 || fit.Chillers[0].Samples != 1 {
		t.Fatalf("the part-load bucket should remain: %+v", fit.Chillers)
	}
}

type shadowFailingSink struct{ captureEvaluationSink }

func (sink *shadowFailingSink) InsertEvaluation(ctx context.Context, evaluation Evaluation) error {
	if evaluation.Quality == "SHADOW" {
		return errors.New("clickhouse unavailable")
	}
	return sink.captureEvaluationSink.InsertEvaluation(ctx, evaluation)
}

func TestPlantBucketsResolvesSplitSensorsViaStaticTwin(t *testing.T) {
	tenantID := "33333333-1000-7000-8000-000000000001"
	siteID := "33333333-2000-7000-8000-000000000001"
	chillerID := "33333333-3000-7000-8000-000000000001"
	ampID := "33333333-4000-7000-8000-000000000001"
	hubID := "33333333-4000-7000-8000-000000000002"
	btuID := "33333333-4000-7000-8000-000000000003"
	ratedPower := 125.0

	twin := planttwin.SiteTwin{
		Scope:    planttwin.SiteScope{TenantID: tenantID, SiteID: siteID},
		Timezone: "Asia/Shanghai",
		Units: []planttwin.PlantUnit{
			{AssetID: chillerID, AssetCode: "ch-1", AssetName: "Chiller 1", AssetType: planttwin.AssetTypeChiller, RatedPowerKW: &ratedPower},
		},
		PointBindings: []planttwin.PointBinding{
			{PointID: "p-kw", DeviceID: ampID, PointCode: "p_kw", WireSourceKey: "p_kw", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerID, BindingRole: planttwin.BindingMeters, SemanticRole: planttwin.RoleActivePower},
			{PointID: "t-l", DeviceID: hubID, PointCode: "probe_t1", WireSourceKey: "probe_t1", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCHWLeavingTemp},
			{PointID: "t-e", DeviceID: hubID, PointCode: "probe_t2", WireSourceKey: "probe_t2", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCHWEnteringTemp},
			{PointID: "t-cw", DeviceID: hubID, PointCode: "probe_t3", WireSourceKey: "probe_t3", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCWLeavingTemp},
			{PointID: "l-kw", DeviceID: btuID, PointCode: "chw_load", WireSourceKey: "chw_load", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeSite, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCoolingLoad},
			{PointID: "l-kw-copy", DeviceID: hubID, PointCode: "copied_load", WireSourceKey: "copied_load", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeSite, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCoolingLoad, SourceMetadata: []byte(`{"copied":true}`)},
		},
	}
	loader, err := planttwin.NewStaticLoader(twin)
	if err != nil {
		t.Fatal(err)
	}
	loadedTwin, err := loader.LoadSiteTwin(t.Context(), twin.Scope, time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	var capturedSQL string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		capturedSQL = string(payload)
		rows := strings.Join([]string{
			`{"bucket":"2026-11-14T23:45:00Z","deviceId":"` + ampID + `","telemetryKey":"p_kw","meanValue":88.5}`,
			`{"bucket":"2026-11-14T23:45:00Z","deviceId":"` + hubID + `","telemetryKey":"probe_t1","meanValue":6.8}`,
			`{"bucket":"2026-11-14T23:45:00Z","deviceId":"` + hubID + `","telemetryKey":"probe_t2","meanValue":11.2}`,
			`{"bucket":"2026-11-14T23:45:00Z","deviceId":"` + hubID + `","telemetryKey":"probe_t3","meanValue":29.4}`,
			`{"bucket":"2026-11-14T23:45:00Z","deviceId":"` + btuID + `","telemetryKey":"chw_load","meanValue":410.0}`,
		}, "\n") + "\n"
		_, _ = writer.Write([]byte(rows))
	}))
	defer server.Close()

	reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL, Energy: &energySeriesStub{}})
	if err != nil {
		t.Fatal(err)
	}
	to := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	buckets, err := reader.PlantBuckets(t.Context(), PlantHistoryQuery{
		TenantID: tenantID,
		SiteID:   siteID,
		From:     to.AddDate(0, 0, -14),
		To:       to,
		Twin:     &loadedTwin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(capturedSQL, "copied_load") || !strings.Contains(capturedSQL, "'probe_t1'") || !strings.Contains(capturedSQL, "'chw_load'") {
		t.Fatalf("unexpected split-sensor history SQL: %s", capturedSQL)
	}
	if len(buckets) != 2 {
		t.Fatalf("expected 1 aggregated chiller bucket + 1 cooling load bucket, got %+v", buckets)
	}
	var chBucket, loadBucket *PlantBucket
	for i := range buckets {
		switch buckets[i].DeviceID {
		case chillerID:
			chBucket = &buckets[i]
		default:
			loadBucket = &buckets[i]
		}
	}
	if chBucket == nil || chBucket.PowerKW == nil || *chBucket.PowerKW != 88.5 ||
		chBucket.ChwLeavingC == nil || *chBucket.ChwLeavingC != 6.8 ||
		chBucket.ChwEnteringC == nil || *chBucket.ChwEnteringC != 11.2 ||
		chBucket.CwLeavingC == nil || *chBucket.CwLeavingC != 29.4 ||
		chBucket.RatedPowerKW == nil || *chBucket.RatedPowerKW != 125.0 {
		t.Fatalf("split chiller bucket not aggregated cleanly: %+v", chBucket)
	}
	if loadBucket == nil || loadBucket.CoolingKW == nil || *loadBucket.CoolingKW != 410.0 {
		t.Fatalf("cooling load bucket mismatch: %+v", loadBucket)
	}
}

func TestChillerCOPModelMeanCOPIsEnergyWeightedNotArithmetic(t *testing.T) {
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	const totalBuckets = 123 // > 96 steady samples after skipping the first start-up bucket
	loads := []float64{100, 300, 500}
	baseCOPs := []float64{5.5, 4.5, 3.5}
	buckets := make([]PlantBucket, 0, totalBuckets*2)

	var totalCoolingKW, totalPowerKW, sumInstantCOP float64
	var steadyCount int
	for index := 0; index < totalBuckets; index++ {
		when := at.Add(time.Duration(index-totalBuckets) * 15 * time.Minute)
		supply := 7.0 + 0.5*math.Sin(float64(index)/5.0)
		entering := supply + 4.0
		bucketIdx := index % 3
		load := loads[bucketIdx]
		cop := baseCOPs[bucketIdx] + 0.15*(supply-7.0)
		power := load / cop
		buckets = append(buckets,
			PlantBucket{Bucket: when, DeviceID: "chiller-weighted", PowerKW: ptr(power), ChwLeavingC: ptr(supply), ChwEnteringC: ptr(entering)},
			PlantBucket{Bucket: when, DeviceID: "btu-weighted", CoolingKW: ptr(load)},
		)
		if index > 0 { // bucket 0 is skipped as startingUp
			totalCoolingKW += load
			totalPowerKW += power
			sumInstantCOP += cop
			steadyCount++
		}
	}

	fit := FitPlant(buckets, at.Add(-totalBuckets*15*time.Minute), at)
	if len(fit.Chillers) != 1 {
		t.Fatalf("chillers = %+v", fit.Chillers)
	}
	chiller := fit.Chillers[0]
	if chiller.Status != ChillerFitted || chiller.MeanCOP == nil {
		t.Fatalf("expected fitted chiller with MeanCOP, got %+v", chiller)
	}
	expectedEnergyWeighted := round(totalCoolingKW/totalPowerKW, 3)
	arithmeticAvg := sumInstantCOP / float64(steadyCount)
	if math.Abs(*chiller.MeanCOP-expectedEnergyWeighted) > 1e-3 {
		t.Fatalf("MeanCOP = %v, want energy-weighted %v (arithmetic avg would be %.3f)", *chiller.MeanCOP, expectedEnergyWeighted, arithmeticAvg)
	}
	if math.Abs(*chiller.MeanCOP-arithmeticAvg) < 0.3 {
		t.Fatalf("expected energy-weighted MeanCOP (%v) to stay distinct from arithmetic average (%v)", *chiller.MeanCOP, arithmeticAvg)
	}
}

func TestChillerCOPModelFiltersStandbyNaNInfAndBounds(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

	// Standby-only chiller (power < 5 kW) and low-load/corrupt NaN/Inf readings must never fit or emit NaN/COP=80+.
	buckets := []PlantBucket{
		{Bucket: at.Add(-45 * time.Minute), DeviceID: "standby-chiller", PowerKW: ptr(2.0), ChwLeavingC: ptr(7.0), ChwEnteringC: ptr(10.0)},
		{Bucket: at.Add(-45 * time.Minute), DeviceID: "btu", CoolingKW: ptr(400.0)}, // 400 kW / 2 kW = COP 200 -> blocked by standby deadband
		{Bucket: at.Add(-30 * time.Minute), DeviceID: "standby-chiller", PowerKW: ptr(4.5), ChwLeavingC: ptr(7.0), ChwEnteringC: ptr(10.0)},
		{Bucket: at.Add(-30 * time.Minute), DeviceID: "btu", CoolingKW: ptr(400.0)}, // 400 kW / 4.5 kW = COP 88.9 -> blocked by standby deadband
		// Corrupt NaN / Inf buckets must not poison maxPower or temperatures.
		{Bucket: at.Add(-15 * time.Minute), DeviceID: "real-chiller", PowerKW: ptr(math.Inf(1)), ChwLeavingC: ptr(7.0), ChwEnteringC: ptr(11.0)},
		{Bucket: at.Add(-15 * time.Minute), DeviceID: "btu", CoolingKW: ptr(300.0)},
		{Bucket: at, DeviceID: "real-chiller", PowerKW: ptr(60.0), ChwLeavingC: ptr(math.NaN()), ChwEnteringC: ptr(11.0)},
		{Bucket: at, DeviceID: "btu", CoolingKW: ptr(300.0)},
		{Bucket: at.Add(15 * time.Minute), DeviceID: "real-chiller", PowerKW: ptr(60.0), ChwLeavingC: ptr(7.0), ChwEnteringC: ptr(11.0)},
		{Bucket: at.Add(15 * time.Minute), DeviceID: "btu", CoolingKW: ptr(5.0)}, // below 10 kW cooling deadband -> noCoolingLoad
		{Bucket: at.Add(30 * time.Minute), DeviceID: "real-chiller", PowerKW: ptr(60.0), ChwLeavingC: ptr(7.0), ChwEnteringC: ptr(11.0)},
		{Bucket: at.Add(30 * time.Minute), DeviceID: "btu", CoolingKW: ptr(math.NaN())},
	}

	fit := FitPlant(buckets, at.Add(-time.Hour), at.Add(time.Hour))
	if fit.CVRMSEPower != nil {
		t.Fatalf("expected nil plant CVRMSEPower without fitted chillers, got %v", *fit.CVRMSEPower)
	}
	if fit.History.SetAside["noCoolingLoad"] != 2 || fit.History.SetAside["implausibleReading"] != 1 {
		t.Fatalf("unexpected setAside counts: %+v", fit.History.SetAside)
	}
	for _, chiller := range fit.Chillers {
		if chiller.Status != ChillerInsufficientData || chiller.MeanCOP != nil {
			t.Fatalf("chiller %s should not fit or produce MeanCOP: %+v", chiller.DeviceID, chiller)
		}
	}
}

func TestFitPlantFeederAuxiliaryPowerAndColdSeasonCondenserWindow(t *testing.T) {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	t.Run("standby chiller with 30 kW feeder aux never triggers running or pollutes fit", func(t *testing.T) {
		buckets := syntheticPlant(at, 120, true)
		rated := 127.5
		// Add standby chiller-c reading 30.2-36.0 kW gross with 30 kW feeder aux (0.2-6.0 kW net <= 10% rated).
		for index := 0; index < 120; index++ {
			when := at.Add(time.Duration(index-120) * 15 * time.Minute)
			gross := 30.2 + float64(index%6) // up to 35.2 kW -> net 5.2 kW (< 12.75 kW = 10% of 127.5 kW)
			buckets = append(buckets, PlantBucket{
				Bucket:                 when,
				DeviceID:               "chiller-c",
				PowerKW:                ptr(gross),
				FeederAuxiliaryPowerKW: 30,
				ChwLeavingC:            ptr(7.2),
				ChwEnteringC:           ptr(11.0),
				CwLeavingC:             ptr(28.0),
				RatedPowerKW:           &rated,
			})
		}
		fit := FitPlant(buckets, at.Add(-120*15*time.Minute), at)
		if fit.History.RunningBuckets != 120 {
			t.Fatalf("expected 120 running buckets, got %d", fit.History.RunningBuckets)
		}
		if fit.History.LoadAllocation["evaporatorDeltaT"] != 120 {
			t.Fatalf("standby chiller must not pollute evaporatorDeltaT load allocation: %+v", fit.History.LoadAllocation)
		}
		byID := map[string]ChillerFit{}
		for _, ch := range fit.Chillers {
			byID[ch.DeviceID] = ch
		}
		if byID["chiller-c"].Status != ChillerInsufficientData || byID["chiller-c"].Samples != 0 || byID["chiller-c"].MeanCOP != nil {
			t.Fatalf("standby chiller-c with 30 kW feeder aux must not fit: %+v", byID["chiller-c"])
		}
		if byID["chiller-a"].Status != ChillerFitted || byID["chiller-b"].Status != ChillerFitted {
			t.Fatalf("co-running chillers must remain cleanly fitted: a=%+v b=%+v", byID["chiller-a"], byID["chiller-b"])
		}
	})

	t.Run("standby chiller without RatedPowerKW reading ~30-32 kW with 30 kW aux never fits", func(t *testing.T) {
		var buckets []PlantBucket
		for index := 0; index < 120; index++ {
			when := at.Add(time.Duration(index-120) * 15 * time.Minute)
			buckets = append(buckets,
				PlantBucket{
					Bucket:                 when,
					DeviceID:               "chiller-d-standby",
					PowerKW:                ptr(30.0 + 1.8*math.Sin(float64(index))),
					FeederAuxiliaryPowerKW: 30,
					ChwLeavingC:            ptr(7.0),
					ChwEnteringC:           ptr(10.0),
					CwLeavingC:             ptr(25.0),
				},
				PlantBucket{Bucket: when, DeviceID: "btu", CoolingKW: ptr(400)},
			)
		}
		fit := FitPlant(buckets, at.Add(-120*15*time.Minute), at)
		if fit.History.RunningBuckets != 0 {
			t.Fatalf("expected 0 running buckets for standby 30 kW aux meter, got %+v", fit.History)
		}
		if len(fit.Chillers) != 1 || fit.Chillers[0].Status != ChillerInsufficientData || fit.Chillers[0].Samples != 0 {
			t.Fatalf("expected INSUFFICIENT_DATA with 0 samples, got %+v", fit.Chillers)
		}
	})

	t.Run("running chiller deducts 30 kW aux before COP regression and nameplate check", func(t *testing.T) {
		copFn := func(plr, supply, condenser float64) float64 {
			return 2 + 6*plr - 3*plr*plr + 0.15*supply - 0.05*condenser
		}
		ratedPower := 127.5
		const auxKW = 30.0
		var buckets []PlantBucket
		for index := 0; index < 150; index++ {
			when := at.Add(time.Duration(index-150) * 15 * time.Minute)
			phase := float64(index)
			plr := 0.35 + 0.55*(0.5+0.5*math.Sin(phase/6))
			load := 450 * plr
			supply := 6.5 + 1.5*math.Sin(phase/7)
			condenser := 24.0 + 4.0*math.Sin(phase/11)
			netPower := load / copFn(plr, supply, condenser)
			grossMeterPower := netPower + auxKW
			buckets = append(buckets,
				PlantBucket{
					Bucket:                 when,
					DeviceID:               "chiller-c",
					PowerKW:                ptr(grossMeterPower),
					FeederAuxiliaryPowerKW: auxKW,
					ChwLeavingC:            ptr(supply),
					ChwEnteringC:           ptr(supply + 4.0),
					CwLeavingC:             ptr(condenser),
					RatedPowerKW:           &ratedPower,
				},
				PlantBucket{Bucket: when, DeviceID: "btu", CoolingKW: ptr(load)},
			)
		}
		fit := FitPlant(buckets, at.Add(-150*15*time.Minute), at)
		if len(fit.Chillers) != 1 || fit.Chillers[0].Status != ChillerFitted {
			t.Fatalf("expected chiller-c fitted after deducting 30 kW aux, got %+v", fit.Chillers)
		}
		ch := fit.Chillers[0]
		if ch.CVRMSEPower == nil || *ch.CVRMSEPower > 1e-4 {
			t.Fatalf("expected near-exact fit after aux deduction, got CVRMSE=%v", ch.CVRMSEPower)
		}
		if math.Abs(ch.Coefficients["chilledWaterSupplyTempC"]-0.15) > 1e-4 || math.Abs(ch.Coefficients["condenserReturnTempC"]+0.05) > 1e-4 {
			t.Fatalf("expected true compressor COP coefficients recovered, got %+v", ch.Coefficients)
		}
	})

	t.Run("cold-season CwLeavingC=13.3C passes plausible while <10C rejects", func(t *testing.T) {
		copFn := func(plr, supply, condenser float64) float64 {
			return 2.4 + 5.0*plr - 2.5*plr*plr + 0.14*supply - 0.04*condenser
		}
		var buckets []PlantBucket
		for index := 0; index < 140; index++ {
			when := at.Add(time.Duration(index-140) * 15 * time.Minute)
			phase := float64(index)
			plr := 0.30 + 0.50*(0.5+0.5*math.Sin(phase/5))
			load := 400 * plr
			supply := 6.5 + 1.5*math.Sin(phase/7)
			condenser := 13.3 + 2.0*math.Sin(phase/9) // spans 11.3..15.3 C, hitting Songyaxuan 13.3 C winter window
			if index == 139 {
				condenser = 9.5 // below 10 C floor -> implausibleReading
			}
			power := load / copFn(plr, supply, condenser)
			buckets = append(buckets,
				PlantBucket{
					Bucket:       when,
					DeviceID:     "chiller-winter",
					PowerKW:      ptr(power),
					ChwLeavingC:  ptr(supply),
					ChwEnteringC: ptr(supply + 3.5),
					CwLeavingC:   ptr(condenser),
				},
				PlantBucket{Bucket: when, DeviceID: "btu", CoolingKW: ptr(load)},
			)
		}
		// Force an exact 13.3 C bucket inside the steady range.
		buckets[40].CwLeavingC = ptr(13.3)
		*buckets[40].PowerKW = (*buckets[41].CoolingKW) / copFn((*buckets[41].CoolingKW)/400, *buckets[40].ChwLeavingC, 13.3)

		fit := FitPlant(buckets, at.Add(-140*15*time.Minute), at)
		if fit.History.SetAside["implausibleReading"] != 1 {
			t.Fatalf("expected only <10 C bucket rejected as implausibleReading, got setAside=%+v", fit.History.SetAside)
		}
		if len(fit.Chillers) != 1 || fit.Chillers[0].Status != ChillerFitted || fit.Chillers[0].Samples < 130 {
			t.Fatalf("expected winter condenser buckets (>=10 C, including 13.3 C) to fit cleanly, got %+v", fit.Chillers)
		}
	})

	t.Run("plantBucketsFromTwin carries FeederAuxiliaryPowerKW onto PlantBucket", func(t *testing.T) {
		tenantID := "77777777-1000-7000-8000-000000000001"
		siteID := "77777777-2000-7000-8000-000000000001"
		chillerID := "77777777-3000-7000-8000-000000000001"
		meterID := "77777777-4000-7000-8000-000000000001"
		rated := 127.5
		twin := planttwin.SiteTwin{
			Scope: planttwin.SiteScope{TenantID: tenantID, SiteID: siteID},
			Units: []planttwin.PlantUnit{
				{AssetID: chillerID, AssetCode: "chiller-c", AssetType: planttwin.AssetTypeChiller, RatedPowerKW: &rated, FeederAuxiliaryPowerKW: 30},
			},
			PointBindings: []planttwin.PointBinding{
				{PointID: "pt-p", DeviceID: meterID, PointCode: "p_kw", WireSourceKey: "p_kw", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerID, BindingRole: planttwin.BindingMeters, SemanticRole: planttwin.RoleActivePower},
				{PointID: "pt-t", DeviceID: meterID, PointCode: "chw_leaving_temperature", WireSourceKey: "chw_leaving_temperature", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCHWLeavingTemp},
			},
		}
		plan := buildTwinCOPQueryPlan(twin)
		buckets := aggregatePlantBucketsFromTwin(plan, []plantMetricBucketRow{
			{Bucket: at, DeviceID: meterID, TelemetryKey: "p_kw", MeanValue: 31.2},
			{Bucket: at, DeviceID: meterID, TelemetryKey: "chw_leaving_temperature", MeanValue: 7.5},
		})
		if len(buckets) != 1 || buckets[0].FeederAuxiliaryPowerKW != 30 {
			t.Fatalf("expected FeederAuxiliaryPowerKW=30 on PlantBucket, got %+v", buckets)
		}
	})
}
