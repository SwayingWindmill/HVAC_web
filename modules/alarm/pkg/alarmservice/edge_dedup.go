package alarmservice

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	defaultEdgeSyncCheckTimeout = 2 * time.Second
	defaultEdgeSyncCacheTTL     = 5 * time.Second
)

// Local wire DTOs matching connectivity's internal site edge-rule sync status JSON
// without importing a peer domain module.
type EdgeGatewayRuleSyncSummary struct {
	GatewayID                string   `json:"gatewayId"`
	Online                   bool     `json:"online"`
	SyncStatus               string   `json:"syncStatus"`
	AppliedStatus            string   `json:"appliedStatus,omitempty"`
	EdgeRulesSynced          bool     `json:"edgeRulesSynced"`
	DesiredBundleRevision    int64    `json:"desiredBundleRevision"`
	DesiredEdgeBundleDigest  string   `json:"desiredEdgeBundleDigest,omitempty"`
	ReportedBundleRevision   int64    `json:"reportedBundleRevision"`
	ReportedEdgeBundleDigest string   `json:"reportedEdgeBundleDigest,omitempty"`
	ReportedRuleIDs          []string `json:"reportedRuleIds"`
	ReportedEdgeOnlyRuleIDs  []string `json:"reportedEdgeOnlyRuleIds"`
}

type EdgeSiteRuleSyncStatus struct {
	SchemaVersion int                          `json:"schemaVersion"`
	TenantID      string                       `json:"tenantId"`
	SiteID        string                       `json:"siteId"`
	Gateways      []EdgeGatewayRuleSyncSummary `json:"gateways"`
}

type EdgeRuleSyncChecker interface {
	GetSiteEdgeRuleSyncStatus(ctx context.Context, tenantID, siteID string) (EdgeSiteRuleSyncStatus, error)
}

type HTTPEdgeRuleSyncCheckerConfig struct {
	BaseURL    string
	HTTPClient *http.Client
	CacheTTL   time.Duration
	Timeout    time.Duration
	Now        func() time.Time
}

type cachedSiteEdgeSyncEntry struct {
	expiresAt time.Time
	status    EdgeSiteRuleSyncStatus
	err       error
}

type HTTPEdgeRuleSyncChecker struct {
	baseURL  string
	client   *http.Client
	cacheTTL time.Duration
	timeout  time.Duration
	now      func() time.Time
	mu       sync.Mutex
	cache    map[string]cachedSiteEdgeSyncEntry
}

func NewHTTPEdgeRuleSyncChecker(config HTTPEdgeRuleSyncCheckerConfig) (*HTTPEdgeRuleSyncChecker, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || config.HTTPClient == nil {
		return nil, errors.New("edge rule sync checker configuration is invalid")
	}
	cacheTTL := config.CacheTTL
	if cacheTTL == 0 {
		cacheTTL = defaultEdgeSyncCacheTTL
	}
	timeout := config.Timeout
	if timeout == 0 {
		timeout = defaultEdgeSyncCheckTimeout
	}
	nowFn := config.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &HTTPEdgeRuleSyncChecker{
		baseURL:  trimmed,
		client:   config.HTTPClient,
		cacheTTL: cacheTTL,
		timeout:  timeout,
		now:      nowFn,
		cache:    make(map[string]cachedSiteEdgeSyncEntry),
	}, nil
}

func (c *HTTPEdgeRuleSyncChecker) GetSiteEdgeRuleSyncStatus(ctx context.Context, tenantID, siteID string) (EdgeSiteRuleSyncStatus, error) {
	if c == nil || c.client == nil {
		return EdgeSiteRuleSyncStatus{}, errors.New("edge rule sync checker unavailable")
	}
	now := c.now().UTC()
	key := tenantID + "|" + siteID

	c.mu.Lock()
	if entry, ok := c.cache[key]; ok && now.Before(entry.expiresAt) {
		c.mu.Unlock()
		return entry.status, entry.err
	}
	c.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	endpoint := fmt.Sprintf("%s/internal/v1/sites/%s/edge-rule-sync-status?tenantId=%s",
		c.baseURL, url.PathEscape(siteID), url.QueryEscape(tenantID))
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return EdgeSiteRuleSyncStatus{}, err
	}
	resp, err := c.client.Do(req)
	var status EdgeSiteRuleSyncStatus
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
			err = fmt.Errorf("connectivity site edge rule sync returned HTTP %d", resp.StatusCode)
		} else if decErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status); decErr != nil {
			err = fmt.Errorf("decode connectivity site edge rule sync response: %w", decErr)
		}
	}

	c.mu.Lock()
	if len(c.cache) >= 512 {
		c.cache = make(map[string]cachedSiteEdgeSyncEntry)
	}
	c.cache[key] = cachedSiteEdgeSyncEntry{
		expiresAt: now.Add(c.cacheTTL),
		status:    status,
		err:       err,
	}
	c.mu.Unlock()
	return status, err
}

// LoadConnectivityEdgeRuleSyncChecker initializes an mTLS Connectivity client for
// pure EDGE cloud evaluation deduplication. Explicitly configured ALARM_CONNECTIVITY_SERVICE_URL
// fails closed on invalid TLS/CA configuration; inherited default CONNECTIVITY_SERVICE_URL
// skips gracefully if gateway CA material is absent.
func LoadConnectivityEdgeRuleSyncChecker(logger *slog.Logger) (EdgeRuleSyncChecker, error) {
	explicitURL := strings.TrimSpace(os.Getenv("ALARM_CONNECTIVITY_SERVICE_URL"))
	baseURL := explicitURL
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("CONNECTIVITY_SERVICE_URL"))
	}
	if baseURL == "" || strings.EqualFold(baseURL, "off") {
		return nil, nil
	}
	certPath := strings.TrimSpace(os.Getenv("ALARM_CONNECTIVITY_TLS_CERT"))
	if certPath == "" {
		certPath = strings.TrimSpace(os.Getenv("ALARM_TLS_CERT"))
	}
	keyPath := strings.TrimSpace(os.Getenv("ALARM_CONNECTIVITY_TLS_KEY"))
	if keyPath == "" {
		keyPath = strings.TrimSpace(os.Getenv("ALARM_TLS_KEY"))
	}
	caPath := strings.TrimSpace(os.Getenv("ALARM_CONNECTIVITY_SERVER_CA"))
	if caPath == "" {
		caPath = strings.TrimSpace(os.Getenv("CONNECTIVITY_SERVER_CA"))
	}
	if caPath == "" {
		caPath = "/run/hvac/gateway-ca/ca.crt"
	}
	serverName := strings.TrimSpace(os.Getenv("ALARM_CONNECTIVITY_SERVER_NAME"))
	if serverName == "" {
		serverName = strings.TrimSpace(os.Getenv("CONNECTIVITY_SERVICE_SERVER_NAME"))
	}
	if serverName == "" {
		serverName = "connectivity"
	}

	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		if explicitURL == "" && errors.Is(err, os.ErrNotExist) {
			if logger != nil {
				logger.Warn("alarm_edge_dedup_connectivity_ca_missing", "ca_path", caPath)
			}
			return nil, nil
		}
		return nil, fmt.Errorf("load connectivity CA %q: %w", caPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("invalid connectivity CA bundle %q", caPath)
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load alarm mTLS client certificate: %w", err)
	}
	var tr *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		tr = base.Clone()
	} else {
		tr = &http.Transport{}
	}
	tr.Proxy = http.ProxyFromEnvironment
	tr.DialContext = (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 2 * time.Second
	tr.ResponseHeaderTimeout = defaultEdgeSyncCheckTimeout
	tr.TLSClientConfig = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      pool,
		Certificates: []tls.Certificate{cert},
		ServerName:   serverName,
	}
	client := &http.Client{
		Timeout:       defaultEdgeSyncCheckTimeout,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return NewHTTPEdgeRuleSyncChecker(HTTPEdgeRuleSyncCheckerConfig{
		BaseURL:    baseURL,
		HTTPClient: client,
	})
}

func (store *PostgresStore) latestPublishedSiteBundleRevision(ctx context.Context, tenantID, siteID string) (uint64, error) {
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	meta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return 0, err
	}
	return meta.BundleRevision, nil
}

func (store *PostgresStore) resolveEdgeDelegatedRules(
	ctx context.Context,
	tenantID, siteID string,
	rules []SiteRule,
) map[string]bool {
	if store == nil || store.edgeSyncChecker == nil || len(rules) == 0 {
		return nil
	}
	pureEdgeIDs := make([]string, 0)
	for _, rule := range rules {
		if rule.Policy.EffectiveExecutionTarget() == ExecutionTargetEdge {
			pureEdgeIDs = append(pureEdgeIDs, strings.ToLower(strings.TrimSpace(rule.AssignmentID)))
		}
	}
	if len(pureEdgeIDs) == 0 {
		return nil
	}
	return store.resolveEdgeDelegatedAssignmentIDs(ctx, tenantID, siteID, pureEdgeIDs)
}

func (store *PostgresStore) resolveEdgeDelegatedAssignmentIDs(
	ctx context.Context,
	tenantID, siteID string,
	pureEdgeAssignmentIDs []string,
) map[string]bool {
	if store == nil || store.edgeSyncChecker == nil || len(pureEdgeAssignmentIDs) == 0 {
		return nil
	}
	bundleRev, err := store.latestPublishedSiteBundleRevision(ctx, tenantID, siteID)
	if err != nil || bundleRev == 0 {
		return nil
	}
	syncStatus, err := store.edgeSyncChecker.GetSiteEdgeRuleSyncStatus(ctx, tenantID, siteID)
	if err != nil {
		return nil
	}
	delegated := make(map[string]bool)
	for _, gw := range syncStatus.Gateways {
		if !gw.Online || !gw.EdgeRulesSynced || uint64(gw.ReportedBundleRevision) != bundleRev || gw.ReportedEdgeBundleDigest == "" {
			continue
		}
		for _, id := range pureEdgeAssignmentIDs {
			if slices.Contains(gw.ReportedEdgeOnlyRuleIDs, id) {
				delegated[id] = true
			}
		}
	}
	if len(delegated) == 0 {
		return nil
	}
	return delegated
}

func (store *PostgresStore) isSingleRuleEdgeDelegated(ctx context.Context, tenantID, siteID, assignmentID string) bool {
	if store == nil || store.edgeSyncChecker == nil {
		return false
	}
	tenantCtx := identitycontext.WithTenantID(ctx, tenantID)
	tx, err := store.beginTenantTransaction(tenantCtx, tenantID, true)
	if err != nil {
		return false
	}
	assigned, loadErr := loadAssignedAlarmPolicy(tenantCtx, tx, tenantID, siteID, assignmentID)
	_ = tx.Rollback(tenantCtx)
	if loadErr != nil || assigned.Policy.EffectiveExecutionTarget() != ExecutionTargetEdge {
		return false
	}
	delegated := store.resolveEdgeDelegatedAssignmentIDs(tenantCtx, tenantID, siteID, []string{strings.ToLower(strings.TrimSpace(assignmentID))})
	return delegated[strings.ToLower(strings.TrimSpace(assignmentID))]
}

func (store *PostgresStore) deferDelegatedDueEvaluation(ctx context.Context, claim EvaluationClaim, deferUntil time.Time) (EvaluationDecision, error) {
	tx, err := store.beginTenantTransaction(ctx, claim.TenantID, false)
	if err != nil {
		return EvaluationDecision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	persisted, found, err := loadPersistedEvaluation(ctx, tx, claim.TenantID, claim.SiteID, claim.AssignmentID, true)
	if err != nil {
		return EvaluationDecision{}, err
	}
	if !found || persisted.LeaseOwner != claim.WorkerID || persisted.LeaseFence != claim.Fence || persisted.State.Version != claim.StateVersion {
		return EvaluationDecision{}, ErrEvaluationClaimLost
	}
	cmd, err := tx.Exec(ctx, `
UPDATE alarm_runtime.alarm_evaluation_state
SET lease_owner = NULL,
    lease_until = NULL,
    next_evaluation_at = GREATEST(COALESCE(next_evaluation_at, $5), $5)
WHERE tenant_id = $1 AND site_id = $2 AND assignment_id = $3 AND version = $4`,
		claim.TenantID, claim.SiteID, claim.AssignmentID, claim.StateVersion, deferUntil.UTC(),
	)
	if err != nil {
		return EvaluationDecision{}, fmt.Errorf("defer delegated edge rule evaluation: %w", err)
	}
	if cmd.RowsAffected() != 1 {
		return EvaluationDecision{}, ErrEvaluationClaimLost
	}
	if err := tx.Commit(ctx); err != nil {
		return EvaluationDecision{}, fmt.Errorf("commit deferred edge rule evaluation: %w", err)
	}
	return EvaluationDecision{
		Status:      persisted.State.Status,
		Effect:      EvaluationEffectNone,
		Fingerprint: persisted.Fingerprint,
		State:       persisted.State,
	}, nil
}
