package settlement

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ClickHouseStore struct {
	base           *url.URL
	user, password string
	client         *http.Client
}

func NewClickHouseStore(baseURL, user, password string, client *http.Client) (*ClickHouseStore, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("settlement ClickHouse URL must be absolute http/https")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &ClickHouseStore{base: u, user: user, password: password, client: client}, nil
}

func sqlQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func (s *ClickHouseStore) ReadMetricFacts(ctx context.Context, period Period, bindings []MetricBinding) ([]Fact, error) {
	if len(bindings) == 0 {
		return nil, errors.New("settlement Metric bindings are empty")
	}
	bindingByID := make(map[string]MetricBinding, len(bindings))
	ids := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		bindingByID[binding.MetricBindingID] = binding
		ids = append(ids, "toUUID("+sqlQuote(binding.MetricBindingID)+")")
	}
	sql := fmt.Sprintf(`SELECT result_id,metric_binding_id,metric_version_id,metric_code,period_start,period_end,value_number,quality,completeness
FROM analytics.metric_series
WHERE tenant_id=toUUID(%s) AND site_id=toUUID(%s)
  AND metric_binding_id IN (%s)
  AND period_start >= parseDateTime64BestEffort(%s)
  AND period_end <= parseDateTime64BestEffort(%s)
  AND value_number IS NOT NULL
ORDER BY metric_binding_id,period_start,period_end,revision DESC,calculated_at DESC
LIMIT 1 BY metric_binding_id,period_start,period_end
FORMAT TabSeparated`, sqlQuote(period.TenantID), sqlQuote(period.SiteID), strings.Join(ids, ","), sqlQuote(period.Start.UTC().Format(time.RFC3339Nano)), sqlQuote(period.End.UTC().Format(time.RFC3339Nano)))
	u := *s.base
	query := u.Query()
	query.Set("query", sql)
	query.Set("date_time_output_format", "iso")
	u.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if s.user != "" {
		request.SetBasicAuth(s.user, s.password)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("settlement ClickHouse query returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	facts := make([]Fact, 0, len(lines))
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) != 9 {
			return nil, errors.New("settlement Metric result row is malformed")
		}
		binding, ok := bindingByID[parts[1]]
		if !ok {
			return nil, errors.New("settlement Metric result references an unbound Metric")
		}
		start, err := parseClickHouseTime(parts[4])
		if err != nil {
			return nil, err
		}
		end, err := parseClickHouseTime(parts[5])
		if err != nil {
			return nil, err
		}
		value, err := strconv.ParseFloat(parts[6], 64)
		if err != nil {
			return nil, err
		}
		completeness, err := strconv.ParseFloat(parts[8], 64)
		if err != nil {
			return nil, err
		}
		facts = append(facts, Fact{
			ID: parts[0], MetricBindingID: parts[1], MetricVersionID: parts[2], MetricCode: parts[3],
			Role: binding.Role, TariffPeriodCode: binding.TariffPeriodCode,
			Start: start, End: end, Value: value, Quality: parts[7], Completeness: completeness,
		})
	}
	return facts, nil
}

func parseClickHouseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return parsed.UTC(), nil
	}
	parsed, err = time.Parse("2006-01-02 15:04:05.000", value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
