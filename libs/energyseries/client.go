// Package energyseries reads the Telemetry Query Service's Energy Series as the caller's own
// Workload Principal (ADR 0017, ADR 0023): IAM signs a grant for each query, and the query service
// serves it. report-service, the optimization service and M&V read energy and its cost this way.
package energyseries

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

const (
	workloadDecisionPath = "/internal/v1/analytics/workload-decision"
	energySeriesPath     = "/internal/v1/analytics/energy-series"
	maximumResponseBytes = int64(4 << 20)
)

// ErrSiteNotBound is IAM's answer when the workload holds no Energy read role on the Site.
var ErrSiteNotBound = errors.New("the workload is not bound to the Site for Energy reads")

type Config struct {
	IAMURL      string
	QueryURL    string
	IAMClient   *http.Client
	QueryClient *http.Client
}

type Client struct {
	iamURL      string
	queryURL    string
	iamClient   *http.Client
	queryClient *http.Client
}

func New(config Config) *Client {
	return &Client{
		iamURL: strings.TrimRight(config.IAMURL, "/"), queryURL: strings.TrimRight(config.QueryURL, "/"),
		iamClient: config.IAMClient, queryClient: config.QueryClient,
	}
}

// Series is one Energy Series query, answered under a grant IAM signed for exactly that query.
func (client *Client) Series(ctx context.Context, query analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, err
	}
	var grant struct {
		DelegationGrant string `json:"delegationGrant"`
	}
	status, err := post(ctx, client.iamClient, client.iamURL+workloadDecisionPath, body, "", &grant)
	if status == http.StatusNotFound {
		return analyticsmodel.EnergySeriesResponse{}, ErrSiteNotBound
	}
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("IAM Energy grant: %w", err)
	}
	var series analyticsmodel.EnergySeriesResponse
	if _, err := post(ctx, client.queryClient, client.queryURL+energySeriesPath, body, grant.DelegationGrant, &series); err != nil {
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
