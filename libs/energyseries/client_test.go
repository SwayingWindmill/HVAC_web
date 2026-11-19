package energyseries

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

func validTestQuery() analyticsmodel.EnergySeriesQuery {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return analyticsmodel.EnergySeriesQuery{
		TenantID:      "018f1d00-0000-7000-8000-000000000001",
		SiteID:        "018f1e00-1000-7000-8000-000000000001",
		EnergyType:    analyticsmodel.EnergyTypeElectricity,
		Granularity:   analyticsmodel.GranularityHour,
		Timezone:      "Asia/Shanghai",
		From:          from,
		To:            from.Add(24 * time.Hour),
		QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	}
}

func TestClientReportEnrichmentUsesWorkloadDecisionAndExactQueryBody(t *testing.T) {
	wantQuery := validTestQuery()
	var iamBody []byte
	var queryBody []byte
	var queryPath string
	var gotGrant string

	iam := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != workloadDecisionPath {
			t.Fatalf("IAM path = %s", request.URL.Path)
		}
		iamBody, _ = io.ReadAll(request.Body)
		var decoded analyticsmodel.EnergySeriesQuery
		dec := json.NewDecoder(http.MaxBytesReader(writer, io.NopCloser(bytesReader(iamBody)), 64<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&decoded); err != nil {
			t.Fatalf("IAM strict decode failed: %v body=%s", err, string(iamBody))
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"delegationGrant": "grant-enrichment"})
	}))
	defer iam.Close()

	plantCOP := 4.75
	wetBulb := 21.8
	querySrv := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		queryPath = request.URL.Path
		gotGrant = request.Header.Get("X-Delegation-Grant")
		queryBody, _ = io.ReadAll(request.Body)
		balance := analyticsmodel.ComputeMeterBalance(1000, 960, 0.05, false)
		_ = json.NewEncoder(writer).Encode(analyticsmodel.ReportEnrichmentResponse{
			LayeredEfficiency: &analyticsmodel.LayeredEfficiency{PlantCOP: &plantCOP},
			MeterBalance:      &balance,
			ConditionAnchors:  &analyticsmodel.ReportConditionAnchorsSummary{MeanWetBulbC: &wetBulb},
		})
	}))
	defer querySrv.Close()

	client := New(Config{
		IAMURL:      iam.URL,
		QueryURL:    querySrv.URL,
		IAMClient:   iam.Client(),
		QueryClient: querySrv.Client(),
	})

	resp, err := client.ReportEnrichment(context.Background(), wantQuery)
	if err != nil {
		t.Fatalf("ReportEnrichment failed: %v", err)
	}
	if queryPath != reportEnrichmentPath || gotGrant != "grant-enrichment" {
		t.Fatalf("path=%q grant=%q", queryPath, gotGrant)
	}
	if string(iamBody) != string(queryBody) {
		t.Fatalf("IAM and query payloads differ: iam=%s query=%s", iamBody, queryBody)
	}
	if resp.LayeredEfficiency == nil || resp.LayeredEfficiency.PlantCOP == nil || *resp.LayeredEfficiency.PlantCOP != 4.75 {
		t.Fatalf("unexpected enrichment response: %#v", resp)
	}
	if resp.MeterBalance == nil || resp.MeterBalance.Status != analyticsmodel.MeterBalanceBalanced {
		t.Fatalf("unexpected meter balance: %#v", resp.MeterBalance)
	}
	if resp.ConditionAnchors == nil || resp.ConditionAnchors.MeanWetBulbC == nil || *resp.ConditionAnchors.MeanWetBulbC != 21.8 {
		t.Fatalf("unexpected condition anchors: %#v", resp.ConditionAnchors)
	}
}

func TestClientReportEnrichmentSiteNotBoundAndQueryFailure(t *testing.T) {
	query := validTestQuery()
	iam := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer iam.Close()

	querySrv := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer querySrv.Close()

	client := New(Config{
		IAMURL:      iam.URL,
		QueryURL:    querySrv.URL,
		IAMClient:   iam.Client(),
		QueryClient: querySrv.Client(),
	})
	_, err := client.ReportEnrichment(context.Background(), query)
	if !errors.Is(err, ErrSiteNotBound) {
		t.Fatalf("err = %v, want ErrSiteNotBound", err)
	}

	iamOK := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"delegationGrant": "grant"})
	}))
	defer iamOK.Close()

	clientErr := New(Config{
		IAMURL:      iamOK.URL,
		QueryURL:    querySrv.URL,
		IAMClient:   iamOK.Client(),
		QueryClient: querySrv.Client(),
	})
	if _, err := clientErr.ReportEnrichment(context.Background(), query); err == nil {
		t.Fatal("expected error when query service returns 503")
	}
}

func bytesReader(data []byte) io.Reader {
	return &sliceReader{data: data}
}

type sliceReader struct {
	data []byte
	pos  int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
