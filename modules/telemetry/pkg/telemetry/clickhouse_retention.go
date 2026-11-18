package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultClickHouseObservationsRetention   = 180 * 24 * time.Hour
	DefaultClickHouseNumeric1MinRetention    = 365 * 24 * time.Hour
	DefaultClickHouseNumeric15MinRetention   = 1825 * 24 * time.Hour
	DefaultClickHouseNumericHourlyRetention  = 1825 * 24 * time.Hour
	DefaultClickHouseEnergyIntervalRetention = 1825 * 24 * time.Hour
	DefaultClickHouseEnergyHourlyRetention   = 1825 * 24 * time.Hour

	defaultClickHouseHistoryDatabase   = "telemetry_history"
	defaultClickHouseAnalyticsDatabase = "analytics"
)

// ClickHouseLegalHoldScope exempts either an entire tenant (SiteID == "") or a single site
// within a tenant from ClickHouse retention deletions.
type ClickHouseLegalHoldScope struct {
	TenantID string
	SiteID   string
}

// ClickHouseRetentionConfig configures tiered ClickHouse TTL pruning via ALTER TABLE ... DELETE.
type ClickHouseRetentionConfig struct {
	BaseURL                 string
	HistoryDatabase         string
	AnalyticsDatabase       string
	Username                string
	Password                string
	HTTPClient              *http.Client
	ObservationsRetention   time.Duration
	Numeric1MinRetention    time.Duration
	Numeric15MinRetention   time.Duration
	NumericHourlyRetention  time.Duration
	EnergyIntervalRetention time.Duration
	EnergyHourlyRetention   time.Duration
	LegalHolds              []ClickHouseLegalHoldScope
}

type clickHouseRetentionTarget struct {
	database      string
	table         string
	timeColumn    string
	paramType     string
	timeLayout    string
	retention     time.Duration
	nullableScope bool
}

// ClickHouseRetentionPruner issues parameterized ALTER TABLE ... DELETE mutations across tiered
// telemetry_history and analytics tables while respecting configured legal-hold scopes.
type ClickHouseRetentionPruner struct {
	baseURL    *url.URL
	username   string
	password   string
	httpClient *http.Client
	targets    []clickHouseRetentionTarget
	legalHolds []ClickHouseLegalHoldScope
}

// ClickHouseRetentionResult records how many table delete mutations were submitted in a pass.
type ClickHouseRetentionResult struct {
	SubmittedTables []string
}

func NewClickHouseRetentionPruner(config ClickHouseRetentionConfig) (*ClickHouseRetentionPruner, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("ClickHouse retention base URL must be an HTTP(S) origin without user info")
	}
	historyDB := strings.TrimSpace(config.HistoryDatabase)
	if historyDB == "" {
		historyDB = defaultClickHouseHistoryDatabase
	}
	analyticsDB := strings.TrimSpace(config.AnalyticsDatabase)
	if analyticsDB == "" {
		analyticsDB = defaultClickHouseAnalyticsDatabase
	}
	if !clickHouseIdentifierPattern.MatchString(historyDB) || !clickHouseIdentifierPattern.MatchString(analyticsDB) {
		return nil, errors.New("ClickHouse retention database identifiers are invalid")
	}

	observationsRetention, err := resolveRetentionDuration(config.ObservationsRetention, DefaultClickHouseObservationsRetention, "observations")
	if err != nil {
		return nil, err
	}
	numeric1MinRetention, err := resolveRetentionDuration(config.Numeric1MinRetention, DefaultClickHouseNumeric1MinRetention, "numeric_1min_states")
	if err != nil {
		return nil, err
	}
	numeric15MinRetention, err := resolveRetentionDuration(config.Numeric15MinRetention, DefaultClickHouseNumeric15MinRetention, "numeric_15min_states")
	if err != nil {
		return nil, err
	}
	numericHourlyRetention, err := resolveRetentionDuration(config.NumericHourlyRetention, DefaultClickHouseNumericHourlyRetention, "numeric_hourly_states")
	if err != nil {
		return nil, err
	}
	energyIntervalRetention, err := resolveRetentionDuration(config.EnergyIntervalRetention, DefaultClickHouseEnergyIntervalRetention, "energy_interval_facts")
	if err != nil {
		return nil, err
	}
	energyHourlyRetention, err := resolveRetentionDuration(config.EnergyHourlyRetention, DefaultClickHouseEnergyHourlyRetention, "energy_hourly_states")
	if err != nil {
		return nil, err
	}

	holds := make([]ClickHouseLegalHoldScope, 0, len(config.LegalHolds))
	for _, hold := range config.LegalHolds {
		tenantID := strings.ToLower(strings.TrimSpace(hold.TenantID))
		siteID := strings.ToLower(strings.TrimSpace(hold.SiteID))
		if !uuidPattern.MatchString(tenantID) {
			return nil, errors.New("ClickHouse legal hold tenantId must be a valid UUID")
		}
		if siteID != "" && !uuidPattern.MatchString(siteID) {
			return nil, errors.New("ClickHouse legal hold siteId must be a valid UUID")
		}
		holds = append(holds, ClickHouseLegalHoldScope{TenantID: tenantID, SiteID: siteID})
	}

	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	return &ClickHouseRetentionPruner{
		baseURL:    parsed,
		username:   config.Username,
		password:   config.Password,
		httpClient: client,
		legalHolds: holds,
		targets: []clickHouseRetentionTarget{
			{
				database:      historyDB,
				table:         "observations",
				timeColumn:    "sampled_at",
				paramType:     "DateTime64(3, 'UTC')",
				timeLayout:    "2006-01-02 15:04:05.000",
				retention:     observationsRetention,
				nullableScope: true,
			},
			{
				database:   historyDB,
				table:      "numeric_1min_states",
				timeColumn: "bucket",
				paramType:  "DateTime('UTC')",
				timeLayout: "2006-01-02 15:04:05",
				retention:  numeric1MinRetention,
			},
			{
				database:   historyDB,
				table:      "numeric_15min_states",
				timeColumn: "bucket",
				paramType:  "DateTime('UTC')",
				timeLayout: "2006-01-02 15:04:05",
				retention:  numeric15MinRetention,
			},
			{
				database:   historyDB,
				table:      "numeric_gauge_1min_states",
				timeColumn: "bucket",
				paramType:  "DateTime('UTC')",
				timeLayout: "2006-01-02 15:04:05",
				retention:  numeric1MinRetention,
			},
			{
				database:   historyDB,
				table:      "numeric_gauge_15min_states",
				timeColumn: "bucket",
				paramType:  "DateTime('UTC')",
				timeLayout: "2006-01-02 15:04:05",
				retention:  numeric15MinRetention,
			},
			{
				database:   historyDB,
				table:      "numeric_hourly_states",
				timeColumn: "hour",
				paramType:  "DateTime('UTC')",
				timeLayout: "2006-01-02 15:04:05",
				retention:  numericHourlyRetention,
			},
			{
				database:   analyticsDB,
				table:      "energy_interval_facts",
				timeColumn: "period_end",
				paramType:  "DateTime64(3, 'UTC')",
				timeLayout: "2006-01-02 15:04:05.000",
				retention:  energyIntervalRetention,
			},
			{
				database:   analyticsDB,
				table:      "energy_hourly_states",
				timeColumn: "hour",
				paramType:  "DateTime64(3, 'UTC')",
				timeLayout: "2006-01-02 15:04:05.000",
				retention:  energyHourlyRetention,
			},
		},
	}, nil
}

func resolveRetentionDuration(configured, defaultValue time.Duration, label string) (time.Duration, error) {
	if configured < 0 {
		return 0, fmt.Errorf("ClickHouse retention for %s must be non-negative", label)
	}
	if configured == 0 {
		return defaultValue, nil
	}
	return configured, nil
}

// Prune submits ALTER TABLE ... DELETE mutations for each configured ClickHouse table at now.
func (pruner *ClickHouseRetentionPruner) Prune(ctx context.Context, now time.Time) (ClickHouseRetentionResult, error) {
	if pruner == nil || pruner.baseURL == nil || pruner.httpClient == nil {
		return ClickHouseRetentionResult{}, errors.New("ClickHouse retention pruner is closed")
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	var result ClickHouseRetentionResult
	for _, target := range pruner.targets {
		cutoff := now.Add(-target.retention)
		sql, params := buildClickHouseRetentionDelete(target, cutoff, pruner.legalHolds)
		if err := pruner.execDelete(ctx, target.database+"."+target.table, sql, params); err != nil {
			return result, err
		}
		result.SubmittedTables = append(result.SubmittedTables, target.database+"."+target.table)
	}
	return result, nil
}

func buildClickHouseRetentionDelete(target clickHouseRetentionTarget, cutoff time.Time, holds []ClickHouseLegalHoldScope) (string, url.Values) {
	params := url.Values{
		"wait_end_of_query": {"1"},
		"param_cutoff":      {cutoff.UTC().Format(target.timeLayout)},
	}
	var sql strings.Builder
	fmt.Fprintf(&sql, "ALTER TABLE %s.%s DELETE WHERE %s < {cutoff:%s}",
		target.database, target.table, target.timeColumn, target.paramType)
	for index, hold := range holds {
		tenantParam := fmt.Sprintf("hold_tenant_%d", index)
		params.Set("param_"+tenantParam, hold.TenantID)
		if hold.SiteID == "" {
			if target.nullableScope {
				fmt.Fprintf(&sql, " AND (tenant_id IS NULL OR tenant_id != {%s:UUID})", tenantParam)
			} else {
				fmt.Fprintf(&sql, " AND tenant_id != {%s:UUID}", tenantParam)
			}
			continue
		}
		siteParam := fmt.Sprintf("hold_site_%d", index)
		params.Set("param_"+siteParam, hold.SiteID)
		if target.nullableScope {
			fmt.Fprintf(&sql, " AND (tenant_id IS NULL OR site_id IS NULL OR tenant_id != {%s:UUID} OR site_id != {%s:UUID})", tenantParam, siteParam)
		} else {
			fmt.Fprintf(&sql, " AND (tenant_id != {%s:UUID} OR site_id != {%s:UUID})", tenantParam, siteParam)
		}
	}
	return sql.String(), params
}

func (pruner *ClickHouseRetentionPruner) execDelete(ctx context.Context, qualifiedTable, sql string, params url.Values) error {
	endpoint := *pruner.baseURL
	endpoint.RawQuery = params.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(sql))
	if err != nil {
		return fmt.Errorf("create ClickHouse retention request for %s: %w", qualifiedTable, err)
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if pruner.username != "" {
		request.SetBasicAuth(pruner.username, pruner.password)
	}
	response, err := pruner.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("ClickHouse retention delete for %s: %w", qualifiedTable, err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	if readErr != nil {
		return fmt.Errorf("read ClickHouse retention response for %s: %w", qualifiedTable, readErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if len(message) > 512 {
			message = message[:512]
		}
		return fmt.Errorf("ClickHouse retention delete for %s returned %d: %s", qualifiedTable, response.StatusCode, message)
	}
	return nil
}
