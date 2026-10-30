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
	if recommendation.Candidate["supplyTempC"].(float64) != 8 || recommendation.ExpectedImpact["energySavingKWhPerDay"].(float64) <= 0 {
		t.Fatalf("recommendation=%+v", recommendation)
	}
	if evaluation.EnergyResponse["source"] != "FITTED_FROM_HISTORY" || evaluation.EnergyResponse["dailyEnergyDeltaKWhPerSupplyTempC"].(float64) >= 0 {
		t.Fatalf("energy response=%v", evaluation.EnergyResponse)
	}
	if evaluation.ZoneResponse["source"] != "POLICY" || evaluation.CondenserReset["reason"] != "NO_CONDENSER_SETPOINT" {
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
	for _, want := range []string{`tenant_id=toUUID('t\'1')`, `site_id=toUUID('s1')`, `received_at <= parseDateTime64BestEffort('2026-10-30T00:00:00.000Z'`, `sampled_at >= parseDateTime64BestEffort('2026-10-16T00:00:00.000Z'`, `'cw_leaving_temperature'`} {
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

type shadowFailingSink struct{ captureEvaluationSink }

func (sink *shadowFailingSink) InsertEvaluation(ctx context.Context, evaluation Evaluation) error {
	if evaluation.Quality == "SHADOW" {
		return errors.New("clickhouse unavailable")
	}
	return sink.captureEvaluationSink.InsertEvaluation(ctx, evaluation)
}
