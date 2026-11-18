package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultClickHouseObservationsRetention   = 180 * 24 * time.Hour
	DefaultClickHouseNumeric1MinRetention    = 365 * 24 * time.Hour
	DefaultClickHouseNumeric15MinRetention   = 1825 * 24 * time.Hour
	DefaultClickHouseNumericHourlyRetention  = 1825 * 24 * time.Hour
	DefaultClickHouseEnergyIntervalRetention = 1825 * 24 * time.Hour
	DefaultClickHouseEnergyHourlyRetention   = 1825 * 24 * time.Hour
	DefaultClickHouseRetentionDeleteCooldown = 24 * time.Hour

	defaultClickHouseHistoryDatabase   = "telemetry_history"
	defaultClickHouseAnalyticsDatabase = "analytics"
)

var clickHouseMonthPartitionPattern = regexp.MustCompile(`^[0-9]{6}$`)

// ClickHouseLegalHoldScope exempts either an entire tenant (SiteID == "") or a single site
// within a tenant from ClickHouse retention deletions.
type ClickHouseLegalHoldScope struct {
	TenantID string
	SiteID   string
}

// ColdArchiveExportRunner exports expiring rows for archiveable raw ClickHouse tables
// before retention deletion mutations execute.
type ColdArchiveExportRunner interface {
	ExportBeforePrune(ctx context.Context, req ColdArchiveExportRequest) (ColdArchiveExportResult, error)
}

// ClickHouseRetentionConfig configures tiered ClickHouse retention pruning via whole-partition
// drops and governed ALTER TABLE ... DELETE tail mutations.
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
	DeleteCooldown          time.Duration
	LegalHolds              []ClickHouseLegalHoldScope
	ArchiveExporter         ColdArchiveExportRunner
	ArchiveRequired         bool
	Metrics                 *PipelineMetrics
}

type clickHouseRetentionTarget struct {
	database      string
	table         string
	timeColumn    string
	paramType     string
	timeLayout    string
	retention     time.Duration
	nullableScope bool
	archiveable   bool
}

// ClickHouseRetentionPruner drops expired whole-month partitions when no legal holds apply and
// issues guarded ALTER TABLE ... DELETE tail mutations across tiered telemetry_history and
// analytics tables.
type ClickHouseRetentionPruner struct {
	baseURL         *url.URL
	username        string
	password        string
	httpClient      *http.Client
	targets         []clickHouseRetentionTarget
	deleteCooldown  time.Duration
	legalHolds      []ClickHouseLegalHoldScope
	archiveExporter ColdArchiveExportRunner
	archiveRequired bool
	metrics         *PipelineMetrics

	mu           sync.Mutex
	lastDeleteAt map[string]time.Time
}

// ClickHouseRetentionResult records dropped partitions and submitted delete mutations in a pass.
type ClickHouseRetentionResult struct {
	SubmittedTables   []string
	DroppedPartitions []string
	ArchivedTables    []string
}

func validateClickHouseBaseURL(raw, label string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%s base URL must be an HTTP(S) origin without user info", label)
	}
	return parsed, nil
}

func normalizeClickHouseLegalHolds(raw []ClickHouseLegalHoldScope) ([]ClickHouseLegalHoldScope, error) {
	holds := make([]ClickHouseLegalHoldScope, 0, len(raw))
	for _, hold := range raw {
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
	return holds, nil
}

func NewClickHouseRetentionPruner(config ClickHouseRetentionConfig) (*ClickHouseRetentionPruner, error) {
	parsed, err := validateClickHouseBaseURL(config.BaseURL, "ClickHouse retention")
	if err != nil {
		return nil, err
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
	numeric1MinRetention, err := resolveRetentionDuration(config.Numeric1MinRetention, DefaultClickHouseNumeric1MinRetention, "numeric_gauge_1min_states")
	if err != nil {
		return nil, err
	}
	numeric15MinRetention, err := resolveRetentionDuration(config.Numeric15MinRetention, DefaultClickHouseNumeric15MinRetention, "numeric_gauge_15min_states")
	if err != nil {
		return nil, err
	}
	if _, err := resolveRetentionDuration(config.NumericHourlyRetention, DefaultClickHouseNumericHourlyRetention, "numeric_hourly_retention"); err != nil {
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

	if config.DeleteCooldown < 0 {
		return nil, errors.New("ClickHouse retention delete cooldown must be non-negative")
	}
	deleteCooldown := config.DeleteCooldown
	if deleteCooldown == 0 {
		deleteCooldown = DefaultClickHouseRetentionDeleteCooldown
	}

	holds, err := normalizeClickHouseLegalHolds(config.LegalHolds)
	if err != nil {
		return nil, err
	}

	client := config.HTTPClient
	if client == nil {
		client = newDefaultClickHouseHistoryHTTPClient(30 * time.Second)
	}

	return &ClickHouseRetentionPruner{
		baseURL:         parsed,
		username:        config.Username,
		password:        config.Password,
		httpClient:      client,
		deleteCooldown:  deleteCooldown,
		legalHolds:      holds,
		archiveExporter: config.ArchiveExporter,
		archiveRequired: config.ArchiveRequired,
		metrics:         config.Metrics,
		lastDeleteAt:    make(map[string]time.Time),
		targets: []clickHouseRetentionTarget{
			{
				database:      historyDB,
				table:         "observations",
				timeColumn:    "sampled_at",
				paramType:     "DateTime64(3, 'UTC')",
				timeLayout:    "2006-01-02 15:04:05.000",
				retention:     observationsRetention,
				nullableScope: true,
				archiveable:   true,
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
				database:    analyticsDB,
				table:       "energy_interval_facts",
				timeColumn:  "period_end",
				paramType:   "DateTime64(3, 'UTC')",
				timeLayout:  "2006-01-02 15:04:05.000",
				retention:   energyIntervalRetention,
				archiveable: true,
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

// Prune exports archiveable raw tables when configured, drops fully expired month
// partitions when no legal holds exist, and submits guarded ALTER TABLE ... DELETE
// tail mutations across configured ClickHouse tables.
func (pruner *ClickHouseRetentionPruner) Prune(ctx context.Context, now time.Time) (ClickHouseRetentionResult, error) {
	if pruner == nil || pruner.baseURL == nil || pruner.httpClient == nil {
		return ClickHouseRetentionResult{}, errors.New("ClickHouse retention pruner is closed")
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	var result ClickHouseRetentionResult

	// Export all archiveable raw tables before issuing any partition drop or delete mutation.
	for _, target := range pruner.targets {
		if !target.archiveable {
			continue
		}
		if pruner.archiveRequired && pruner.archiveExporter == nil {
			pruner.metrics.ObserveColdArchiveRun(ColdArchiveOutcomeFailed)
			return ClickHouseRetentionResult{}, fmt.Errorf("ClickHouse cold archive exporter is required before pruning %s.%s", target.database, target.table)
		}
		if pruner.archiveExporter == nil {
			continue
		}
		cutoff := now.Add(-target.retention)
		if _, err := pruner.archiveExporter.ExportBeforePrune(ctx, ColdArchiveExportRequest{
			Database:   target.database,
			Table:      target.table,
			Cutoff:     cutoff,
			LegalHolds: pruner.legalHolds,
		}); err != nil {
			pruner.metrics.ObserveColdArchiveRun(ColdArchiveOutcomeFailed)
			return ClickHouseRetentionResult{}, fmt.Errorf("ClickHouse cold archive export failed for %s.%s: %w", target.database, target.table, err)
		}
		pruner.metrics.ObserveColdArchiveRun(ColdArchiveOutcomeSucceeded)
		result.ArchivedTables = append(result.ArchivedTables, target.database+"."+target.table)
	}

	for _, target := range pruner.targets {
		qualified := target.database + "." + target.table
		cutoff := now.Add(-target.retention)
		if len(pruner.legalHolds) == 0 {
			dropped, err := pruner.dropExpiredMonthPartitions(ctx, target, cutoff)
			if err != nil {
				return result, err
			}
			result.DroppedPartitions = append(result.DroppedPartitions, dropped...)
		}

		if pruner.withinDeleteCooldown(qualified, now) {
			continue
		}
		activeMutations, err := pruner.activeMutationCount(ctx, target)
		if err != nil {
			return result, err
		}
		if activeMutations > 0 {
			continue
		}
		hasRows, err := pruner.hasExpiredRows(ctx, target, cutoff)
		if err != nil {
			return result, err
		}
		if !hasRows {
			pruner.recordDeleteCheck(qualified, now)
			continue
		}
		sql, params := buildClickHouseRetentionDelete(target, cutoff, pruner.legalHolds)
		if _, err := pruner.execQuery(ctx, qualified, "delete", sql, params); err != nil {
			return result, err
		}
		pruner.recordDeleteCheck(qualified, now)
		result.SubmittedTables = append(result.SubmittedTables, qualified)
	}
	return result, nil
}

func (pruner *ClickHouseRetentionPruner) withinDeleteCooldown(qualified string, now time.Time) bool {
	if pruner.deleteCooldown <= 0 {
		return false
	}
	pruner.mu.Lock()
	defer pruner.mu.Unlock()
	last, ok := pruner.lastDeleteAt[qualified]
	if !ok || now.Before(last) {
		return false
	}
	return now.Sub(last) < pruner.deleteCooldown
}

func (pruner *ClickHouseRetentionPruner) recordDeleteCheck(qualified string, now time.Time) {
	pruner.mu.Lock()
	defer pruner.mu.Unlock()
	pruner.lastDeleteAt[qualified] = now
}

func (pruner *ClickHouseRetentionPruner) dropExpiredMonthPartitions(ctx context.Context, target clickHouseRetentionTarget, cutoff time.Time) ([]string, error) {
	qualified := target.database + "." + target.table
	body, err := pruner.execQuery(ctx, qualified, "partition lookup",
		"SELECT DISTINCT partition_id FROM system.parts WHERE database = {db:String} AND table = {table:String} AND active = 1 ORDER BY partition_id ASC FORMAT TabSeparated",
		url.Values{"param_db": {target.database}, "param_table": {target.table}},
	)
	if err != nil {
		return nil, err
	}
	var dropped []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		partitionID := strings.TrimSpace(line)
		partitionEnd, ok := monthPartitionEndUTC(partitionID)
		if !ok || partitionEnd.After(cutoff) {
			continue
		}
		dropSQL := fmt.Sprintf("ALTER TABLE %s.%s DROP PARTITION ID '%s'", target.database, target.table, partitionID)
		if _, err := pruner.execQuery(ctx, qualified, "drop partition", dropSQL, url.Values{"wait_end_of_query": {"1"}}); err != nil {
			return dropped, err
		}
		dropped = append(dropped, qualified+":"+partitionID)
	}
	return dropped, nil
}

func monthPartitionEndUTC(partitionID string) (time.Time, bool) {
	if !clickHouseMonthPartitionPattern.MatchString(partitionID) {
		return time.Time{}, false
	}
	year, err := strconv.Atoi(partitionID[:4])
	if err != nil {
		return time.Time{}, false
	}
	month, err := strconv.Atoi(partitionID[4:6])
	if err != nil || month < 1 || month > 12 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0), true
}

func (pruner *ClickHouseRetentionPruner) activeMutationCount(ctx context.Context, target clickHouseRetentionTarget) (int64, error) {
	qualified := target.database + "." + target.table
	body, err := pruner.execQuery(ctx, qualified, "active mutations check",
		"SELECT count() FROM system.mutations WHERE database = {db:String} AND table = {table:String} AND is_done = 0 FORMAT TabSeparated",
		url.Values{"param_db": {target.database}, "param_table": {target.table}},
	)
	if err != nil {
		return 0, err
	}
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return 0, nil
	}
	count, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ClickHouse active mutations count for %s: %w", qualified, err)
	}
	return count, nil
}

func (pruner *ClickHouseRetentionPruner) hasExpiredRows(ctx context.Context, target clickHouseRetentionTarget, cutoff time.Time) (bool, error) {
	qualified := target.database + "." + target.table
	where, params := buildClickHouseRetentionWhereClause(target.timeColumn, target.paramType, target.timeLayout, target.nullableScope, cutoff, pruner.legalHolds)
	sql := fmt.Sprintf("SELECT 1 FROM %s.%s WHERE %s LIMIT 1 FORMAT TabSeparated", target.database, target.table, where)
	body, err := pruner.execQuery(ctx, qualified, "existence check", sql, params)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(body)) != "", nil
}

func buildClickHouseRetentionWhereClause(timeColumn, paramType, timeLayout string, nullableScope bool, cutoff time.Time, holds []ClickHouseLegalHoldScope) (string, url.Values) {
	params := url.Values{
		"param_cutoff": {cutoff.UTC().Format(timeLayout)},
	}
	var where strings.Builder
	fmt.Fprintf(&where, "%s < {cutoff:%s}", timeColumn, paramType)
	for index, hold := range holds {
		tenantParam := fmt.Sprintf("hold_tenant_%d", index)
		params.Set("param_"+tenantParam, hold.TenantID)
		if hold.SiteID == "" {
			if nullableScope {
				fmt.Fprintf(&where, " AND (tenant_id IS NULL OR tenant_id != {%s:UUID})", tenantParam)
			} else {
				fmt.Fprintf(&where, " AND tenant_id != {%s:UUID}", tenantParam)
			}
			continue
		}
		siteParam := fmt.Sprintf("hold_site_%d", index)
		params.Set("param_"+siteParam, hold.SiteID)
		if nullableScope {
			fmt.Fprintf(&where, " AND (tenant_id IS NULL OR site_id IS NULL OR tenant_id != {%s:UUID} OR site_id != {%s:UUID})", tenantParam, siteParam)
		} else {
			fmt.Fprintf(&where, " AND (tenant_id != {%s:UUID} OR site_id != {%s:UUID})", tenantParam, siteParam)
		}
	}
	return where.String(), params
}

func buildClickHouseRetentionDelete(target clickHouseRetentionTarget, cutoff time.Time, holds []ClickHouseLegalHoldScope) (string, url.Values) {
	where, params := buildClickHouseRetentionWhereClause(target.timeColumn, target.paramType, target.timeLayout, target.nullableScope, cutoff, holds)
	params.Set("wait_end_of_query", "1")
	return fmt.Sprintf("ALTER TABLE %s.%s DELETE WHERE %s", target.database, target.table, where), params
}

func (pruner *ClickHouseRetentionPruner) execQuery(ctx context.Context, qualifiedTable, opLabel, sql string, params url.Values) ([]byte, error) {
	endpoint := *pruner.baseURL
	endpoint.RawQuery = params.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(sql))
	if err != nil {
		return nil, fmt.Errorf("create ClickHouse retention %s request for %s: %w", opLabel, qualifiedTable, err)
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if pruner.username != "" {
		request.SetBasicAuth(pruner.username, pruner.password)
	}
	response, err := pruner.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("ClickHouse retention %s for %s: %w", opLabel, qualifiedTable, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_, _ = io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read ClickHouse retention %s response for %s: %w", opLabel, qualifiedTable, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close ClickHouse retention %s response for %s: %w", opLabel, qualifiedTable, closeErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if len(message) > 512 {
			message = message[:512]
		}
		return nil, fmt.Errorf("ClickHouse retention %s for %s returned %d: %s", opLabel, qualifiedTable, response.StatusCode, message)
	}
	return body, nil
}
