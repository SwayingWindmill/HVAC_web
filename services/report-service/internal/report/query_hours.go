package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

const (
	analyticsWorkloadDecisionPath = "/internal/v1/analytics/workload-decision"
	energySeriesPath              = "/internal/v1/analytics/energy-series"
	maximumResponseBytes          = int64(4 << 20)
)

// ErrSiteNotBound is IAM's answer when report-service holds no Energy read role on the Site.
var ErrSiteNotBound = errors.New("report-service is not bound to the Site for Energy reads")

// QueryHours reads a Site's valid electricity and cooling by Site-local hour from the Telemetry
// Query Service, as report-service's own Workload Principal (ADR 0017, ADR 0023): IAM signs a
// grant for each query, and the query service serves it.
type QueryHours struct {
	iamURL      string
	queryURL    string
	iamClient   *http.Client
	queryClient *http.Client
}

type QueryHoursConfig struct {
	IAMURL      string
	QueryURL    string
	IAMClient   *http.Client
	QueryClient *http.Client
}

func NewQueryHours(config QueryHoursConfig) *QueryHours {
	return &QueryHours{
		iamURL: strings.TrimRight(config.IAMURL, "/"), queryURL: strings.TrimRight(config.QueryURL, "/"),
		iamClient: config.IAMClient, queryClient: config.QueryClient,
	}
}

func (reader *QueryHours) Hours(ctx context.Context, tenantID, siteID string, location *time.Location, from, to time.Time) ([]Hour, error) {
	byStart := map[int64]*Hour{}
	at := func(start time.Time) *Hour {
		hour, ok := byStart[start.Unix()]
		if !ok {
			hour = &Hour{Start: start.UTC()}
			byStart[start.Unix()] = hour
		}
		return hour
	}
	for _, energyType := range []analyticsmodel.EnergyType{analyticsmodel.EnergyTypeElectricity, analyticsmodel.EnergyTypeCooling} {
		query := analyticsmodel.EnergySeriesQuery{
			TenantID: tenantID, SiteID: siteID, EnergyType: energyType, Granularity: analyticsmodel.GranularityHour,
			Timezone: location.String(), From: from, To: to, QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
		}
		series, err := reader.series(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, point := range series.Points {
			hour := at(point.PeriodStart)
			if energyType == analyticsmodel.EnergyTypeElectricity {
				hour.ElectricityKWh, hour.HasElectricity, hour.Cost = point.EnergyKWh, true, point.Cost
			} else {
				hour.CoolingKWh = point.EnergyKWh
			}
		}
	}
	hours := make([]Hour, 0, len(byStart))
	for _, hour := range byStart {
		hours = append(hours, *hour)
	}
	sort.Slice(hours, func(left, right int) bool { return hours[left].Start.Before(hours[right].Start) })
	return hours, nil
}

func (reader *QueryHours) series(ctx context.Context, query analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, err
	}
	var grant struct {
		DelegationGrant string `json:"delegationGrant"`
	}
	status, err := post(ctx, reader.iamClient, reader.iamURL+analyticsWorkloadDecisionPath, body, "", &grant)
	if status == http.StatusNotFound {
		return analyticsmodel.EnergySeriesResponse{}, ErrSiteNotBound
	}
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("IAM Energy grant: %w", err)
	}
	var series analyticsmodel.EnergySeriesResponse
	if _, err := post(ctx, reader.queryClient, reader.queryURL+energySeriesPath, body, grant.DelegationGrant, &series); err != nil {
		return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("Energy Series %s: %w", query.EnergyType, err)
	}
	return series, nil
}

func post(ctx context.Context, client *http.Client, target string, body []byte, grant string, into any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, application/problem+json")
	if grant != "" {
		request.Header.Set("X-Delegation-Grant", grant)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil {
		return response.StatusCode, err
	}
	if int64(len(payload)) > maximumResponseBytes {
		return response.StatusCode, errors.New("response exceeds 4 MiB")
	}
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, fmt.Errorf("returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return response.StatusCode, json.Unmarshal(payload, into)
}
