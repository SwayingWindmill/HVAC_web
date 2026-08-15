package optimization

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validRequest() Request {
	return Request{
		TenantID:               "01990000-3000-7000-8000-000000000001",
		SiteID:                 "01990000-5000-7000-8000-000000000001",
		SubjectType:            "SITE",
		SubjectID:              "01990000-5000-7000-8000-000000000001",
		OptimizationRunID:      "01990000-1950-7000-8000-000000000001",
		InputSnapshotID:        "01990000-1930-7000-8000-000000000001",
		InputChecksum:          strings.Repeat("a", 64),
		PolicyVersionID:        "01990000-1920-7000-8000-000000000001",
		TopologyVersionID:      "01990000-1300-7000-8000-000000000001",
		LoadForecastSnapshotID: "01990000-1890-7000-8000-000000000001",
		TariffVersionID:        "01990000-1420-7000-8000-000000000001",
		Objective:              "COST",
		Horizon:                "DAY_AHEAD",
		HorizonMinutes:         1440,
		Granularity:            "15MIN",
		DispatchMode:           "SHADOW",
		ValidFrom:              time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		Resources: []Resource{
			{ResourceID: "01990000-1900-7000-8000-000000000001", SOC: 0.5, MinSOC: 0.2, MaxSOC: 0.9, ChargePowerLimitKW: 100, DischargePowerLimitKW: 100, Availability: true, ControlMode: "REMOTE"},
			{ResourceID: "01990000-1900-7000-8000-000000000002", SOC: 0.6, MinSOC: 0.2, MaxSOC: 0.9, ChargePowerLimitKW: 50, DischargePowerLimitKW: 50, Availability: false, ControlMode: "LOCAL"},
		},
	}
}

func TestNoDispatchShadowPlanPreservesInputSnapshotAndNeverCommands(t *testing.T) {
	request := validRequest()
	plan, err := Optimize(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "DRAFT" || plan.Quality != "FALLBACK" || plan.FallbackPolicy != "NO_DISPATCH" {
		t.Fatalf("plan=%#v", plan)
	}
	if plan.InputSnapshotID != request.InputSnapshotID || plan.InputChecksum != request.InputChecksum ||
		plan.PolicyVersionID != request.PolicyVersionID || plan.TopologyVersionID != request.TopologyVersionID ||
		plan.LoadForecastSnapshotID != request.LoadForecastSnapshotID || plan.TariffVersionID != request.TariffVersionID {
		t.Fatalf("plan lineage=%#v", plan)
	}
	if len(plan.Intervals) != 192 {
		t.Fatalf("intervals=%d", len(plan.Intervals))
	}
	for index, interval := range plan.Intervals {
		if interval.TargetType != "POWER_SETPOINT" || interval.TargetValue != 0 || interval.Unit != "kW" {
			t.Fatalf("interval[%d]=%#v", index, interval)
		}
		if !interval.EndTime.Equal(interval.StartTime.Add(15 * time.Minute)) {
			t.Fatalf("interval[%d] duration=%s", index, interval.EndTime.Sub(interval.StartTime))
		}
	}
	if plan.Intervals[0].ExpectedSOC != 0.5 || plan.Intervals[96].ExpectedSOC != 0.6 {
		t.Fatalf("SOC drift first=%f second=%f", plan.Intervals[0].ExpectedSOC, plan.Intervals[96].ExpectedSOC)
	}
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(body))
	for _, forbidden := range []string{"commandid", "command_id", "mqtt", "execute", "executionid", "controlrequest"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("Plan response contains execution surface %q: %s", forbidden, lower)
		}
	}
}

func TestNoDispatchBaselineRejectsExecutableOrUnfrozenRequests(t *testing.T) {
	for name, mutate := range map[string]func(*Request){
		"assisted mode":     func(request *Request) { request.DispatchMode = "ASSISTED" },
		"auto mode":         func(request *Request) { request.DispatchMode = "AUTO" },
		"wrong subject":     func(request *Request) { request.SubjectID = "01990000-5000-7000-8000-000000000002" },
		"missing checksum":  func(request *Request) { request.InputChecksum = "" },
		"wrong horizon":     func(request *Request) { request.HorizonMinutes = 720 },
		"missing resources": func(request *Request) { request.Resources = nil },
		"SOC out of bounds": func(request *Request) { request.Resources[0].SOC = 0.95 },
	} {
		t.Run(name, func(t *testing.T) {
			request := validRequest()
			mutate(&request)
			if _, err := Optimize(request); err == nil {
				t.Fatal("expected optimization request to fail")
			}
		})
	}
}

func TestNoDispatchPlanIsDeterministicForSameFrozenInput(t *testing.T) {
	request := validRequest()
	first, err := Optimize(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Optimize(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.PlanID != second.PlanID || first.Intervals[0].IntervalID != second.Intervals[0].IntervalID || first.Intervals[191].IntervalID != second.Intervals[191].IntervalID {
		t.Fatalf("determinism drift first=%s second=%s", first.PlanID, second.PlanID)
	}
}
