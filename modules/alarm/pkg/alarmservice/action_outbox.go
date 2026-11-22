package alarmservice

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type RuleActionStatus string

const (
	RuleActionStatusPending    RuleActionStatus = "PENDING"
	RuleActionStatusDelivered  RuleActionStatus = "DELIVERED"
	RuleActionStatusDeadLetter RuleActionStatus = "DEAD_LETTER"
)

const (
	defaultActionRelayBatchSize  = 32
	defaultActionRelayLease      = 30 * time.Second
	defaultActionDeliveryTimeout = 10 * time.Second
	defaultActionMaxAttempts     = 5
	defaultActionBaseBackoff     = 5 * time.Second
	defaultActionMaxBackoff      = 5 * time.Minute
	maxDeadLetterListLimit       = 200
	defaultDeadLetterListLimit   = 50
	webhookResponseMaxBytes      = 4096
	actionOutboxMetricDepthName  = "hvac_alarm_action_outbox_depth"
	actionOutboxMetricDeadTotal  = "hvac_alarm_action_dead_letter_total"
	actionPayloadSchemaVersion   = 1
)

var (
	ErrWebhookSSRFBlocked     = errors.New("webhook target resolved to non-public IP address")
	ErrWebhookRedirectBlocked = errors.New("webhook redirects are forbidden")
	ErrRuleActionClaimLost    = errors.New("alarm rule action claim was lost")
)

type PermanentRuleActionError struct {
	ReasonCode string
	Err        error
}

func (err *PermanentRuleActionError) Error() string {
	if err == nil {
		return ""
	}
	if err.Err != nil {
		return fmt.Sprintf("%s: %v", err.ReasonCode, err.Err)
	}
	return err.ReasonCode
}

func (err *PermanentRuleActionError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func MarkPermanentActionFailure(reasonCode string, err error) error {
	return &PermanentRuleActionError{ReasonCode: sanitizeActionErrorCode(reasonCode, "permanent_failure"), Err: err}
}

type WebhookHTTPError struct {
	StatusCode int
}

func (err *WebhookHTTPError) Error() string {
	return fmt.Sprintf("webhook returned HTTP status %d", err.StatusCode)
}

type RuleActionPayload struct {
	SchemaVersion     int                `json:"schemaVersion"`
	TenantID          string             `json:"tenantId"`
	SiteID            string             `json:"siteId"`
	AlarmID           string             `json:"alarmId"`
	AlarmVersion      uint64             `json:"alarmVersion"`
	AlarmType         string             `json:"alarmType"`
	Severity          string             `json:"severity"`
	Title             string             `json:"title"`
	AssignmentID      string             `json:"assignmentId"`
	ActionIndex       int                `json:"actionIndex"`
	TriggerKind       ActionTriggerOn    `json:"triggerKind"`
	OccurredAt        string             `json:"occurredAt"`
	Kind              RuleActionKind     `json:"kind"`
	WebhookURL        string             `json:"webhookUrl,omitempty"`
	SecretRef         string             `json:"secretRef,omitempty"`
	WorkOrderPriority string             `json:"workOrderPriority,omitempty"`
	WorkOrderTitle    string             `json:"workOrderTitle,omitempty"`
	TargetDeviceID    string             `json:"targetDeviceId,omitempty"`
	TargetPointCode   string             `json:"targetPointCode,omitempty"`
	Capability        string             `json:"capability,omitempty"`
	Parameters        map[string]float64 `json:"parameters,omitempty"`
}

type webhookOutboundEvent struct {
	OutboxID     string          `json:"outboxId"`
	TenantID     string          `json:"tenantId"`
	SiteID       string          `json:"siteId"`
	AlarmID      string          `json:"alarmId"`
	AlarmVersion uint64          `json:"alarmVersion"`
	AlarmType    string          `json:"alarmType"`
	Severity     string          `json:"severity"`
	Title        string          `json:"title"`
	AssignmentID string          `json:"assignmentId"`
	TriggerKind  ActionTriggerOn `json:"triggerKind"`
	OccurredAt   string          `json:"occurredAt"`
}

type ClaimedRuleAction struct {
	OutboxID     string
	TenantID     string
	SiteID       string
	AlarmID      string
	AlarmVersion int64
	AssignmentID string
	ActionIndex  int
	TriggerKind  ActionTriggerOn
	ActionKind   RuleActionKind
	TargetRef    string
	Payload      RuleActionPayload
	Attempts     int
	CreatedAt    time.Time
}

type RuleActionDeadLetterView struct {
	OutboxID       string          `json:"outboxId"`
	SiteID         string          `json:"siteId"`
	AlarmID        string          `json:"alarmId"`
	AlarmVersion   int64           `json:"alarmVersion"`
	AssignmentID   string          `json:"assignmentId"`
	ActionIndex    int             `json:"actionIndex"`
	TriggerKind    ActionTriggerOn `json:"triggerKind"`
	ActionKind     RuleActionKind  `json:"actionKind"`
	TargetRef      string          `json:"targetRef"`
	Attempts       int             `json:"attempts"`
	LastErrorCode  string          `json:"lastErrorCode,omitempty"`
	DeadLetteredAt string          `json:"deadLetteredAt"`
	CreatedAt      string          `json:"createdAt"`
}

type RuleActionDeadLetterList struct {
	Items []RuleActionDeadLetterView `json:"items"`
}

type RuleActionReplayResult struct {
	OutboxID string           `json:"outboxId"`
	SiteID   string           `json:"siteId"`
	Status   RuleActionStatus `json:"status"`
}

type RuleActionWorkOrderRequest struct {
	OutboxID       string
	IdempotencyKey string
	TenantID       string
	SiteID         string
	AlarmID        string
	AlarmVersion   uint64
	AssignmentID   string
	TriggerKind    ActionTriggerOn
	Priority       string
	Title          string
	OccurredAt     string
}

type RuleActionProtectionCommandRequest struct {
	OutboxID        string
	IdempotencyKey  string
	TenantID        string
	SiteID          string
	AlarmID         string
	AlarmVersion    uint64
	AssignmentID    string
	TriggerKind     ActionTriggerOn
	TargetDeviceID  string
	TargetPointCode string
	Capability      commandmodel.Capability
	Parameters      commandmodel.CommandParameters
	PriorityTier    commandmodel.PriorityTier
	CommandSource   commandmodel.CommandSource
	TraceID         string
	OccurredAt      string
}

type WebhookActionSender interface {
	Send(ctx context.Context, action ClaimedRuleAction) error
}

type RuleActionStore interface {
	ListTenantsWithPendingRuleActions(ctx context.Context, readyAt time.Time) ([]string, error)
	ClaimRuleActionOutbox(ctx context.Context, tenantID, workerID string, now time.Time, lease time.Duration, limit int) ([]ClaimedRuleAction, error)
	MarkRuleActionDelivered(ctx context.Context, tenantID, outboxID, workerID string, now time.Time) error
	FailRuleActionOutbox(ctx context.Context, tenantID, outboxID, workerID, reasonCode string, nextAttempt time.Time, now time.Time, deadLetter bool) error
	CountRuleActionOutboxStates(ctx context.Context) (pending int64, deadLetter int64, err error)
}

type RuleActionDeadLetterStore interface {
	ListRuleActionDeadLetters(ctx context.Context, tenantID, siteID string, limit int) (RuleActionDeadLetterList, error)
	ReplayRuleActionDeadLetter(ctx context.Context, tenantID, siteID, outboxID string, now time.Time) (RuleActionStatus, error)
}

type SSRFWebhookSender struct {
	Client         *http.Client
	SecretResolver func(ctx context.Context, secretRef string) ([]byte, error)
	Now            func() time.Time
}

func NewSSRFWebhookSender(secretResolver func(ctx context.Context, secretRef string) ([]byte, error)) *SSRFWebhookSender {
	return &SSRFWebhookSender{
		Client:         NewSSRFWebhookHTTPClient(nil),
		SecretResolver: secretResolver,
		Now:            time.Now,
	}
}

// NewDirWebhookSecretResolver loads bounded HMAC signing secrets by validated secretRef inside baseDir.
func NewDirWebhookSecretResolver(baseDir string) func(context.Context, string) ([]byte, error) {
	trimmedBase := strings.TrimSpace(baseDir)
	if trimmedBase == "" {
		return nil
	}
	cleanBase := filepath.Clean(trimmedBase)
	const maxSecretBytes = 4096
	return func(_ context.Context, secretRef string) ([]byte, error) {
		if secretRef == "" ||
			!actionSecretRefPattern.MatchString(secretRef) ||
			strings.HasPrefix(secretRef, ".") ||
			strings.Contains(secretRef, "..") ||
			strings.ContainsAny(secretRef, `/\`) {
			return nil, MarkPermanentActionFailure("secret_unavailable", errors.New("webhook secretRef is invalid"))
		}
		absBase, err := filepath.EvalSymlinks(cleanBase)
		if err != nil {
			return nil, MarkPermanentActionFailure("secret_unavailable", errors.New("webhook secret directory is unavailable"))
		}
		candidate := filepath.Join(absBase, secretRef)
		realPath, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return nil, MarkPermanentActionFailure("secret_unavailable", errors.New("webhook signing secret was not found"))
		}
		rel, err := filepath.Rel(absBase, realPath)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, MarkPermanentActionFailure("secret_unavailable", errors.New("webhook secret path escaped base directory"))
		}
		info, err := os.Stat(realPath)
		if err != nil || !info.Mode().IsRegular() {
			return nil, MarkPermanentActionFailure("secret_unavailable", errors.New("webhook secret must be a regular file"))
		}
		file, err := os.Open(realPath)
		if err != nil {
			return nil, MarkPermanentActionFailure("secret_unavailable", errors.New("webhook signing secret cannot be opened"))
		}
		defer func() { _ = file.Close() }()
		raw, err := io.ReadAll(io.LimitReader(file, maxSecretBytes+1))
		if err != nil {
			return nil, MarkPermanentActionFailure("secret_unavailable", errors.New("webhook signing secret cannot be read"))
		}
		if len(raw) > maxSecretBytes {
			return nil, MarkPermanentActionFailure("secret_unavailable", errors.New("webhook signing secret exceeds size limit"))
		}
		trimmed := bytes.TrimRight(raw, "\r\n")
		if len(trimmed) == 0 {
			return nil, MarkPermanentActionFailure("secret_unavailable", errors.New("webhook signing secret is empty"))
		}
		return trimmed, nil
	}
}

// NewSSRFWebhookHTTPClient builds an HTTP client restricted to HTTPS targets on public unicast IPs
// with proxies disabled and HTTP redirects rejected.
func NewSSRFWebhookHTTPClient(tlsConfig *tls.Config) *http.Client {
	effectiveTLS := &tls.Config{MinVersion: tls.VersionTLS12}
	if tlsConfig != nil {
		effectiveTLS = tlsConfig.Clone()
		if effectiveTLS.MinVersion < tls.VersionTLS12 {
			effectiveTLS.MinVersion = tls.VersionTLS12
		}
	}
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 15 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			return ValidatePublicWebhookAddress(address)
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       effectiveTLS,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return ErrWebhookRedirectBlocked
		},
	}
}

func ValidatePublicWebhookAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: invalid address", ErrWebhookSSRFBlocked)
	}
	if !isPublicUnicastAddr(addr) {
		return ErrWebhookSSRFBlocked
	}
	return nil
}

func isPublicUnicastAddr(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() ||
		addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() ||
		!addr.IsGlobalUnicast() {
		return false
	}
	if addr.Is4() {
		b := addr.As4()
		if b[0] == 0 {
			return false
		}
		if b[0] == 100 && b[1] >= 64 && b[1] <= 127 {
			return false
		}
		if b[0] == 192 && b[1] == 0 && (b[2] == 0 || b[2] == 2) {
			return false
		}
		if b[0] == 198 && (b[1] == 18 || b[1] == 19) {
			return false
		}
		if b[0] == 198 && b[1] == 51 && b[2] == 100 {
			return false
		}
		if b[0] == 203 && b[1] == 0 && b[2] == 113 {
			return false
		}
		if b[0] >= 224 {
			return false
		}
		return true
	}
	b6 := addr.As16()
	// Documentation prefix 2001:db8::/32
	if b6[0] == 0x20 && b6[1] == 0x01 && b6[2] == 0x0d && b6[3] == 0xb8 {
		return false
	}
	// 6to4 prefix 2002::/16 (can embed private/reserved IPv4 addresses)
	if b6[0] == 0x20 && b6[1] == 0x02 {
		return false
	}
	// Discard-only prefix 100::/64
	if b6[0] == 0x01 && b6[1] == 0x00 && b6[2] == 0 && b6[3] == 0 && b6[4] == 0 && b6[5] == 0 && b6[6] == 0 && b6[7] == 0 {
		return false
	}
	// NAT64 well-known 64:ff9b::/96 and local-use 64:ff9b:1::/48
	if b6[0] == 0x00 && b6[1] == 0x64 && b6[2] == 0xff && b6[3] == 0x9b {
		if b6[4] == 0x00 && b6[5] == 0x01 {
			return false
		}
		if b6[4] == 0 && b6[5] == 0 && b6[6] == 0 && b6[7] == 0 && b6[8] == 0 && b6[9] == 0 && b6[10] == 0 && b6[11] == 0 {
			return false
		}
	}
	return true
}

func (sender *SSRFWebhookSender) Send(ctx context.Context, action ClaimedRuleAction) error {
	parsedURL, err := url.Parse(action.Payload.WebhookURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Hostname() == "" || parsedURL.User != nil {
		return MarkPermanentActionFailure("invalid_webhook_url", errors.New("webhook URL must be valid https"))
	}
	if ipLiteral, parseErr := netip.ParseAddr(parsedURL.Hostname()); parseErr == nil && !isPublicUnicastAddr(ipLiteral) {
		return ErrWebhookSSRFBlocked
	}
	event := webhookOutboundEvent{
		OutboxID:     action.OutboxID,
		TenantID:     action.TenantID,
		SiteID:       action.SiteID,
		AlarmID:      action.AlarmID,
		AlarmVersion: uint64(action.AlarmVersion),
		AlarmType:    action.Payload.AlarmType,
		Severity:     action.Payload.Severity,
		Title:        action.Payload.Title,
		AssignmentID: action.AssignmentID,
		TriggerKind:  action.TriggerKind,
		OccurredAt:   action.Payload.OccurredAt,
	}
	body, err := json.Marshal(event)
	if err != nil {
		return MarkPermanentActionFailure("invalid_payload", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, action.Payload.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return MarkPermanentActionFailure("invalid_webhook_url", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if action.Payload.SecretRef != "" {
		if sender.SecretResolver == nil {
			return MarkPermanentActionFailure("secret_unavailable", errors.New("webhook secret resolver is not configured"))
		}
		secret, secretErr := sender.SecretResolver(ctx, action.Payload.SecretRef)
		if secretErr != nil {
			return fmt.Errorf("resolve webhook signing secret: %w", secretErr)
		}
		if len(secret) == 0 {
			return MarkPermanentActionFailure("secret_unavailable", errors.New("webhook signing secret is empty"))
		}
		nowFn := sender.Now
		if nowFn == nil {
			nowFn = time.Now
		}
		timestamp := strconv.FormatInt(nowFn().UTC().Unix(), 10)
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(timestamp))
		mac.Write([]byte("."))
		mac.Write(body)
		request.Header.Set("X-HVAC-Alarm-Timestamp", timestamp)
		request.Header.Set("X-HVAC-Alarm-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	client := sender.Client
	if client == nil {
		client = NewSSRFWebhookHTTPClient(nil)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, webhookResponseMaxBytes))
		_ = response.Body.Close()
	}()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return ErrWebhookRedirectBlocked
	}
	return &WebhookHTTPError{StatusCode: response.StatusCode}
}

type RuleActionRelayConfig struct {
	Store                   RuleActionStore
	WebhookSender           WebhookActionSender
	CreateWorkOrder         func(context.Context, RuleActionWorkOrderRequest) error
	SubmitProtectionCommand func(context.Context, RuleActionProtectionCommandRequest) error
	Metrics                 *observability.Registry
	Logger                  *slog.Logger
	WorkerID                string
	Now                     func() time.Time
	BatchSize               int
	LeaseDuration           time.Duration
	DeliveryTimeout         time.Duration
	MaxAttempts             int
	BaseBackoff             time.Duration
	MaxBackoff              time.Duration
}

type RuleActionRelay struct {
	store                   RuleActionStore
	webhookSender           WebhookActionSender
	createWorkOrder         func(context.Context, RuleActionWorkOrderRequest) error
	submitProtectionCommand func(context.Context, RuleActionProtectionCommandRequest) error
	metrics                 *observability.Registry
	logger                  *slog.Logger
	workerID                string
	now                     func() time.Time
	batchSize               int
	leaseDuration           time.Duration
	deliveryTimeout         time.Duration
	maxAttempts             int
	baseBackoff             time.Duration
	maxBackoff              time.Duration
}

type RuleActionRelayResult struct {
	Claimed      int
	Delivered    int
	Retried      int
	DeadLettered int
	Failed       int
}

func NewRuleActionRelay(config RuleActionRelayConfig) (*RuleActionRelay, error) {
	if config.Store == nil {
		return nil, errors.New("rule action outbox store is required")
	}
	workerID := strings.TrimSpace(config.WorkerID)
	if workerID == "" {
		host, err := os.Hostname()
		if err != nil || strings.TrimSpace(host) == "" {
			host = "alarm-owner"
		}
		workerID = "alarm-rule-action-relay:" + host
	}
	webhookSender := config.WebhookSender
	if webhookSender == nil {
		webhookSender = NewSSRFWebhookSender(nil)
	}
	nowFn := config.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	batchSize := config.BatchSize
	if batchSize <= 0 {
		batchSize = defaultActionRelayBatchSize
	}
	leaseDuration := config.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = defaultActionRelayLease
	}
	deliveryTimeout := config.DeliveryTimeout
	if deliveryTimeout <= 0 {
		deliveryTimeout = defaultActionDeliveryTimeout
	}
	maxAttempts := config.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultActionMaxAttempts
	}
	baseBackoff := config.BaseBackoff
	if baseBackoff <= 0 {
		baseBackoff = defaultActionBaseBackoff
	}
	maxBackoff := config.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultActionMaxBackoff
	}
	if maxBackoff < baseBackoff {
		maxBackoff = baseBackoff
	}
	return &RuleActionRelay{
		store:                   config.Store,
		webhookSender:           webhookSender,
		createWorkOrder:         config.CreateWorkOrder,
		submitProtectionCommand: config.SubmitProtectionCommand,
		metrics:                 config.Metrics,
		logger:                  config.Logger,
		workerID:                workerID,
		now:                     nowFn,
		batchSize:               batchSize,
		leaseDuration:           leaseDuration,
		deliveryTimeout:         deliveryTimeout,
		maxAttempts:             maxAttempts,
		baseBackoff:             baseBackoff,
		maxBackoff:              maxBackoff,
	}, nil
}

func (relay *RuleActionRelay) Start(ctx context.Context, interval time.Duration) <-chan struct{} {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer observability.Recover(relay.logger, relay.metrics, "alarm.rule_action_relay_loop")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			_, err := relay.RelayOnce(ctx)
			if err != nil && relay.logger != nil {
				relay.logger.Warn("alarm_rule_action_relay_pass_failed", "error_code", "ALARM_RULE_ACTION_RELAY_FAILED")
			}
		}
	}()
	return done
}

func (relay *RuleActionRelay) RelayOnce(ctx context.Context) (RuleActionRelayResult, error) {
	now := relay.now().UTC()
	tenants, err := relay.store.ListTenantsWithPendingRuleActions(ctx, now)
	if err != nil {
		return RuleActionRelayResult{}, err
	}
	var result RuleActionRelayResult
	var failures []error
	for _, tenantID := range tenants {
		tenantCtx := identitycontext.WithTenantID(ctx, tenantID)
		claimed, err := relay.store.ClaimRuleActionOutbox(tenantCtx, tenantID, relay.workerID, now, relay.leaseDuration, relay.batchSize)
		if err != nil {
			result.Failed++
			failures = append(failures, err)
			continue
		}
		for _, item := range claimed {
			result.Claimed++
			deliveryCtx, cancel := context.WithTimeout(tenantCtx, relay.deliveryTimeout)
			deliverErr := relay.deliverAction(deliveryCtx, item)
			cancel()

			finishedAt := relay.now().UTC()
			if deliverErr == nil {
				if err := relay.store.MarkRuleActionDelivered(tenantCtx, tenantID, item.OutboxID, relay.workerID, finishedAt); err != nil {
					if errors.Is(err, ErrRuleActionClaimLost) {
						continue
					}
					result.Failed++
					failures = append(failures, err)
					continue
				}
				result.Delivered++
				continue
			}

			reasonCode, permanent := classifyRuleActionFailure(item.ActionKind, deliverErr)
			deadLetter := permanent || item.Attempts >= relay.maxAttempts
			nextAttempt := finishedAt
			if !deadLetter {
				nextAttempt = finishedAt.Add(relay.backoffForAttempt(item.Attempts))
			}
			if err := relay.store.FailRuleActionOutbox(tenantCtx, tenantID, item.OutboxID, relay.workerID, reasonCode, nextAttempt, finishedAt, deadLetter); err != nil {
				if errors.Is(err, ErrRuleActionClaimLost) {
					continue
				}
				result.Failed++
				failures = append(failures, err)
				continue
			}
			if deadLetter {
				result.DeadLettered++
				relay.recordDeadLetterMetric(reasonCode)
			} else {
				result.Retried++
			}
		}
	}
	relay.refreshOutboxDepthMetrics(ctx)
	return result, errors.Join(failures...)
}

func (relay *RuleActionRelay) deliverAction(ctx context.Context, item ClaimedRuleAction) error {
	switch item.ActionKind {
	case RuleActionWebhook:
		if relay.webhookSender == nil {
			return MarkPermanentActionFailure("handler_unconfigured", errors.New("webhook sender is nil"))
		}
		return relay.webhookSender.Send(ctx, item)
	case RuleActionWorkOrder:
		if relay.createWorkOrder == nil {
			return MarkPermanentActionFailure("handler_unconfigured", errors.New("work order handler is not configured"))
		}
		return relay.createWorkOrder(ctx, RuleActionWorkOrderRequest{
			OutboxID:       item.OutboxID,
			IdempotencyKey: "alarm-rule-action:" + item.OutboxID,
			TenantID:       item.TenantID,
			SiteID:         item.SiteID,
			AlarmID:        item.AlarmID,
			AlarmVersion:   uint64(item.AlarmVersion),
			AssignmentID:   item.AssignmentID,
			TriggerKind:    item.TriggerKind,
			Priority:       item.Payload.WorkOrderPriority,
			Title:          item.Payload.WorkOrderTitle,
			OccurredAt:     item.Payload.OccurredAt,
		})
	case RuleActionProtectionCommand:
		if relay.submitProtectionCommand == nil {
			return MarkPermanentActionFailure("handler_unconfigured", errors.New("protection command handler is not configured"))
		}
		var params commandmodel.CommandParameters
		if item.Payload.Parameters != nil {
			params = make(commandmodel.CommandParameters, len(item.Payload.Parameters))
			for key, value := range item.Payload.Parameters {
				params[key] = value
			}
		}
		return relay.submitProtectionCommand(ctx, RuleActionProtectionCommandRequest{
			OutboxID:        item.OutboxID,
			IdempotencyKey:  "alarm-rule-action:" + item.OutboxID,
			TenantID:        item.TenantID,
			SiteID:          item.SiteID,
			AlarmID:         item.AlarmID,
			AlarmVersion:    uint64(item.AlarmVersion),
			AssignmentID:    item.AssignmentID,
			TriggerKind:     item.TriggerKind,
			TargetDeviceID:  item.Payload.TargetDeviceID,
			TargetPointCode: item.Payload.TargetPointCode,
			Capability:      commandmodel.Capability(item.Payload.Capability),
			Parameters:      params,
			PriorityTier:    commandmodel.PriorityProtectionRule,
			CommandSource:   commandmodel.SourceProtectionRule,
			TraceID:         commandmodel.NormalizeTraceID("alarm-action-" + item.OutboxID),
			OccurredAt:      item.Payload.OccurredAt,
		})
	default:
		return MarkPermanentActionFailure("invalid_payload", errors.New("unsupported rule action kind"))
	}
}

func (relay *RuleActionRelay) backoffForAttempt(attempt int) time.Duration {
	if attempt <= 1 {
		return relay.baseBackoff
	}
	delay := relay.baseBackoff
	for i := 1; i < attempt; i++ {
		if delay >= relay.maxBackoff/2 {
			return relay.maxBackoff
		}
		delay *= 2
	}
	if delay > relay.maxBackoff {
		return relay.maxBackoff
	}
	return delay
}

func (relay *RuleActionRelay) recordDeadLetterMetric(reasonCode string) {
	if relay.metrics == nil {
		return
	}
	_ = relay.metrics.AddCounter(
		actionOutboxMetricDeadTotal,
		"Total Alarm rule actions transitioned to DEAD_LETTER by error reason family.",
		map[string]string{"reason_family": ruleActionReasonFamily(reasonCode)},
		1,
	)
}

func ruleActionReasonFamily(reasonCode string) string {
	normalized := strings.ToLower(strings.TrimSpace(reasonCode))
	switch normalized {
	case "ssrf_blocked", "redirect_blocked", "http_401", "http_403":
		return "authorization"
	case "http_429":
		return "rate_limited"
	case "http_408", "timeout", "network_error":
		return "transport"
	case "http_5xx", "work_order_error", "command_error", "permanent_failure":
		return "dependency"
	case "http_4xx", "invalid_payload", "payload_invalid":
		return "scope"
	}
	switch {
	case strings.Contains(normalized, "rate_limit"), strings.Contains(normalized, "429"), strings.Contains(normalized, "throttle"):
		return "rate_limited"
	case strings.Contains(normalized, "auth"), strings.Contains(normalized, "ssrf"), strings.Contains(normalized, "redirect"), strings.Contains(normalized, "forbidden"), strings.Contains(normalized, "denied"):
		return "authorization"
	case strings.Contains(normalized, "capacity"), strings.Contains(normalized, "quota"), strings.Contains(normalized, "overflow"):
		return "capacity"
	case strings.Contains(normalized, "invalid"), strings.Contains(normalized, "payload"), strings.Contains(normalized, "scope"), strings.Contains(normalized, "schema"), strings.Contains(normalized, "4xx"):
		return "scope"
	case strings.Contains(normalized, "timeout"), strings.Contains(normalized, "network"), strings.Contains(normalized, "transport"), strings.Contains(normalized, "dial"), strings.Contains(normalized, "tls"), strings.Contains(normalized, "dns"):
		return "transport"
	case strings.Contains(normalized, "work_order"), strings.Contains(normalized, "command"), strings.Contains(normalized, "5xx"), strings.Contains(normalized, "unavailable"), strings.Contains(normalized, "reject"), strings.Contains(normalized, "dependency"), strings.Contains(normalized, "permanent"):
		return "dependency"
	default:
		return "unknown"
	}
}

func (relay *RuleActionRelay) refreshOutboxDepthMetrics(ctx context.Context) {
	if relay.metrics == nil {
		return
	}
	pending, deadLetter, err := relay.store.CountRuleActionOutboxStates(ctx)
	if err != nil {
		return
	}
	_ = relay.metrics.SetGauge(
		actionOutboxMetricDepthName,
		"Current Alarm rule action outbox entries by delivery state.",
		map[string]string{"state": "pending"},
		float64(pending),
	)
	_ = relay.metrics.SetGauge(
		actionOutboxMetricDepthName,
		"Current Alarm rule action outbox entries by delivery state.",
		map[string]string{"state": "dead_letter"},
		float64(deadLetter),
	)
}

func classifyRuleActionFailure(kind RuleActionKind, err error) (string, bool) {
	if errors.Is(err, ErrWebhookSSRFBlocked) {
		return "ssrf_blocked", true
	}
	if errors.Is(err, ErrWebhookRedirectBlocked) {
		return "redirect_blocked", true
	}
	var permanentErr *PermanentRuleActionError
	if errors.As(err, &permanentErr) {
		return sanitizeActionErrorCode(permanentErr.ReasonCode, "permanent_failure"), true
	}
	var httpErr *WebhookHTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.StatusCode == http.StatusRequestTimeout:
			return "http_408", false
		case httpErr.StatusCode == http.StatusTooManyRequests:
			return "http_429", false
		case httpErr.StatusCode >= 400 && httpErr.StatusCode < 500:
			return "http_4xx", true
		case httpErr.StatusCode >= 500:
			return "http_5xx", false
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout", false
	}
	switch kind {
	case RuleActionWebhook:
		return "network_error", false
	case RuleActionWorkOrder:
		return "work_order_error", false
	case RuleActionProtectionCommand:
		return "command_error", false
	default:
		return "delivery_error", false
	}
}

func sanitizeActionErrorCode(raw, fallback string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback
	}
	var builder strings.Builder
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else if r >= 'A' && r <= 'Z' {
			builder.WriteRune(r + ('a' - 'A'))
		}
		if builder.Len() >= 64 {
			break
		}
	}
	cleaned := builder.String()
	if cleaned == "" {
		return fallback
	}
	return cleaned
}

func (store *PostgresStore) nextID(at time.Time) (string, error) {
	if store != nil && store.newID != nil {
		return store.newID(at)
	}
	return newUUIDv7(at)
}

func (store *PostgresStore) enqueueRuleActionsInTx(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, siteID string,
	alarm alarmmodel.Alarm,
	assigned assignedAlarmPolicy,
	triggerKind ActionTriggerOn,
	occurredAt time.Time,
) error {
	return store.enqueueRuleActionsWithFilterInTx(ctx, tx, tenantID, siteID, alarm, assigned, triggerKind, occurredAt, true)
}

func (store *PostgresStore) enqueueRuleActionsWithFilterInTx(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, siteID string,
	alarm alarmmodel.Alarm,
	assigned assignedAlarmPolicy,
	triggerKind ActionTriggerOn,
	occurredAt time.Time,
	includeProtectionCommands bool,
) error {
	if len(assigned.Policy.Actions) == 0 {
		return nil
	}
	effect := EvaluationEffectPublish
	if triggerKind == ActionTriggerClear {
		effect = EvaluationEffectClear
	}
	for index, action := range assigned.Policy.Actions {
		if !includeProtectionCommands && action.Kind == RuleActionProtectionCommand {
			continue
		}
		if !action.MatchesEffect(effect) {
			continue
		}
		outboxID, err := store.nextID(occurredAt.Add(time.Duration(index) * time.Nanosecond))
		if err != nil {
			return fmt.Errorf("generate rule action outbox id: %w", err)
		}
		payload := RuleActionPayload{
			SchemaVersion:     actionPayloadSchemaVersion,
			TenantID:          tenantID,
			SiteID:            siteID,
			AlarmID:           alarm.AlarmID,
			AlarmVersion:      alarm.Version,
			AlarmType:         alarm.AlarmType,
			Severity:          string(alarm.CurrentSeverity),
			Title:             alarm.Title,
			AssignmentID:      assigned.AssignmentID,
			ActionIndex:       index,
			TriggerKind:       triggerKind,
			OccurredAt:        occurredAt.UTC().Format(time.RFC3339Nano),
			Kind:              action.Kind,
			WebhookURL:        action.WebhookURL,
			SecretRef:         action.SecretRef,
			WorkOrderPriority: action.WorkOrderPriority,
			WorkOrderTitle:    action.WorkOrderTitle,
			TargetDeviceID:    action.TargetDeviceID,
			TargetPointCode:   action.TargetPointCode,
			Capability:        action.Capability,
			Parameters:        action.Parameters,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode rule action outbox payload: %w", err)
		}
		_, err = tx.Exec(ctx, `
INSERT INTO alarm_runtime.rule_action_outbox (
	outbox_id, tenant_id, site_id, alarm_id, alarm_version, assignment_id, action_index,
	trigger_kind, action_kind, target_ref, payload, status, attempts, available_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'PENDING',0,$12,$12,$12)
ON CONFLICT (tenant_id, alarm_id, alarm_version, action_index) DO NOTHING`,
			outboxID, tenantID, siteID, alarm.AlarmID, int64(alarm.Version), assigned.AssignmentID, index,
			string(triggerKind), string(action.Kind), action.SanitizedTargetRef(), encoded, occurredAt.UTC())
		if err != nil {
			return fmt.Errorf("enqueue rule action outbox: %w", err)
		}
	}
	return nil
}

func (store *PostgresStore) ListTenantsWithPendingRuleActions(ctx context.Context, readyAt time.Time) ([]string, error) {
	if store == nil || store.pool == nil {
		return nil, ErrUnavailable
	}
	rows, err := store.pool.Query(ctx, `SELECT tenant_id::text FROM alarm_runtime.tenants_with_pending_rule_actions($1) AS t(tenant_id)`, readyAt.UTC())
	if err != nil {
		return nil, fmt.Errorf("list Tenants with pending Alarm rule actions: %w", err)
	}
	defer rows.Close()
	var tenants []string
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, fmt.Errorf("scan Tenant with pending Alarm rule actions: %w", err)
		}
		tenants = append(tenants, tenantID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Tenants with pending Alarm rule actions: %w", err)
	}
	return tenants, nil
}

func (store *PostgresStore) ClaimRuleActionOutbox(ctx context.Context, tenantID, workerID string, now time.Time, lease time.Duration, limit int) ([]ClaimedRuleAction, error) {
	if store == nil || store.pool == nil {
		return nil, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || strings.TrimSpace(workerID) == "" || lease <= 0 {
		return nil, alarmmodel.ErrInvalidOperation
	}
	if limit <= 0 {
		limit = defaultActionRelayBatchSize
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	nowUTC := now.UTC()
	leaseUntil := nowUTC.Add(lease)
	rows, err := tx.Query(ctx, `
WITH next_batch AS (
	SELECT outbox_id
	FROM alarm_runtime.rule_action_outbox
	WHERE tenant_id = $1
	  AND status = 'PENDING'
	  AND available_at <= $2
	  AND (lease_until IS NULL OR lease_until <= $2)
	ORDER BY available_at, created_at, outbox_id
	FOR UPDATE SKIP LOCKED
	LIMIT $3
)
UPDATE alarm_runtime.rule_action_outbox AS outbox
SET lease_owner = $4,
    lease_until = $5,
    attempts = outbox.attempts + 1,
    updated_at = $2
FROM next_batch
WHERE outbox.tenant_id = $1
  AND outbox.outbox_id = next_batch.outbox_id
RETURNING outbox.outbox_id::text, outbox.tenant_id::text, outbox.site_id::text,
          outbox.alarm_id::text, outbox.alarm_version, outbox.assignment_id::text,
          outbox.action_index, outbox.trigger_kind, outbox.action_kind,
          outbox.target_ref, outbox.payload, outbox.attempts, outbox.created_at`,
		tenantID, nowUTC, limit, workerID, leaseUntil)
	if err != nil {
		return nil, fmt.Errorf("claim Alarm rule action outbox: %w", err)
	}

	type rawClaim struct {
		item        ClaimedRuleAction
		payloadJSON []byte
	}
	var scanned []rawClaim
	for rows.Next() {
		var item ClaimedRuleAction
		var triggerRaw, kindRaw string
		var payloadJSON []byte
		if err := rows.Scan(
			&item.OutboxID, &item.TenantID, &item.SiteID,
			&item.AlarmID, &item.AlarmVersion, &item.AssignmentID,
			&item.ActionIndex, &triggerRaw, &kindRaw,
			&item.TargetRef, &payloadJSON, &item.Attempts, &item.CreatedAt,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan claimed Alarm rule action: %w", err)
		}
		item.TriggerKind = ActionTriggerOn(triggerRaw)
		item.ActionKind = RuleActionKind(kindRaw)
		scanned = append(scanned, rawClaim{item: item, payloadJSON: payloadJSON})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate claimed Alarm rule actions: %w", err)
	}
	rows.Close()

	claimed := make([]ClaimedRuleAction, 0, len(scanned))
	for _, entry := range scanned {
		var payload RuleActionPayload
		if err := json.Unmarshal(entry.payloadJSON, &payload); err != nil {
			if _, markErr := tx.Exec(ctx, `
UPDATE alarm_runtime.rule_action_outbox
SET status = 'DEAD_LETTER',
    dead_lettered_at = $3,
    delivered_at = NULL,
    lease_owner = NULL,
    lease_until = NULL,
    last_error_code = 'invalid_payload',
    updated_at = $3
WHERE tenant_id = $1 AND outbox_id = $2`,
				tenantID, entry.item.OutboxID, nowUTC); markErr != nil {
				return nil, fmt.Errorf("dead-letter corrupt Alarm rule action: %w", markErr)
			}
			continue
		}
		entry.item.Payload = payload
		claimed = append(claimed, entry.item)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit Alarm rule action claim: %w", err)
	}
	return claimed, nil
}

func (store *PostgresStore) MarkRuleActionDelivered(ctx context.Context, tenantID, outboxID, workerID string, now time.Time) error {
	if store == nil || store.pool == nil {
		return ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(outboxID) || strings.TrimSpace(workerID) == "" {
		return alarmmodel.ErrInvalidOperation
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	nowUTC := now.UTC()
	command, err := tx.Exec(ctx, `
UPDATE alarm_runtime.rule_action_outbox
SET status = 'DELIVERED',
    delivered_at = $4,
    dead_lettered_at = NULL,
    lease_owner = NULL,
    lease_until = NULL,
    last_error_code = NULL,
    updated_at = $4
WHERE tenant_id = $1
  AND outbox_id = $2
  AND status = 'PENDING'
  AND lease_owner = $3`,
		tenantID, outboxID, workerID, nowUTC)
	if err != nil {
		return fmt.Errorf("mark Alarm rule action delivered: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrRuleActionClaimLost
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Alarm rule action delivery: %w", err)
	}
	return nil
}

func (store *PostgresStore) FailRuleActionOutbox(ctx context.Context, tenantID, outboxID, workerID, reasonCode string, nextAttempt time.Time, now time.Time, deadLetter bool) error {
	if store == nil || store.pool == nil {
		return ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(outboxID) || strings.TrimSpace(workerID) == "" {
		return alarmmodel.ErrInvalidOperation
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	nowUTC := now.UTC()
	code := sanitizeActionErrorCode(reasonCode, "delivery_error")
	var command interface{ RowsAffected() int64 }
	if deadLetter {
		command, err = tx.Exec(ctx, `
UPDATE alarm_runtime.rule_action_outbox
SET status = 'DEAD_LETTER',
    dead_lettered_at = $4,
    delivered_at = NULL,
    lease_owner = NULL,
    lease_until = NULL,
    last_error_code = $5,
    updated_at = $4
WHERE tenant_id = $1
  AND outbox_id = $2
  AND status = 'PENDING'
  AND lease_owner = $3`,
			tenantID, outboxID, workerID, nowUTC, code)
	} else {
		command, err = tx.Exec(ctx, `
UPDATE alarm_runtime.rule_action_outbox
SET status = 'PENDING',
    available_at = $5,
    lease_owner = NULL,
    lease_until = NULL,
    last_error_code = $6,
    updated_at = $4
WHERE tenant_id = $1
  AND outbox_id = $2
  AND status = 'PENDING'
  AND lease_owner = $3`,
			tenantID, outboxID, workerID, nowUTC, nextAttempt.UTC(), code)
	}
	if err != nil {
		return fmt.Errorf("record Alarm rule action failure: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrRuleActionClaimLost
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Alarm rule action failure: %w", err)
	}
	return nil
}

func (store *PostgresStore) CountRuleActionOutboxStates(ctx context.Context) (int64, int64, error) {
	if store == nil || store.pool == nil {
		return 0, 0, ErrUnavailable
	}
	var pending, deadLetter int64
	if err := store.pool.QueryRow(ctx, `SELECT pending_count, dead_letter_count FROM alarm_runtime.rule_action_outbox_state_counts()`).Scan(&pending, &deadLetter); err != nil {
		return 0, 0, fmt.Errorf("count Alarm rule action outbox states: %w", err)
	}
	return pending, deadLetter, nil
}

func (store *PostgresStore) ListRuleActionDeadLetters(ctx context.Context, tenantID, siteID string, limit int) (RuleActionDeadLetterList, error) {
	if store == nil || store.pool == nil {
		return RuleActionDeadLetterList{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) {
		return RuleActionDeadLetterList{}, alarmmodel.ErrInvalidOperation
	}
	if limit <= 0 {
		limit = defaultDeadLetterListLimit
	}
	if limit > maxDeadLetterListLimit {
		limit = maxDeadLetterListLimit
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return RuleActionDeadLetterList{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
SELECT outbox_id::text, site_id::text, alarm_id::text, alarm_version, assignment_id::text,
       action_index, trigger_kind, action_kind, target_ref, attempts,
       COALESCE(last_error_code, ''), dead_lettered_at, created_at
FROM alarm_runtime.rule_action_outbox
WHERE tenant_id = $1 AND site_id = $2 AND status = 'DEAD_LETTER'
ORDER BY dead_lettered_at DESC, outbox_id DESC
LIMIT $3`, tenantID, siteID, limit)
	if err != nil {
		return RuleActionDeadLetterList{}, fmt.Errorf("list Alarm rule action dead letters: %w", err)
	}
	defer rows.Close()

	items := make([]RuleActionDeadLetterView, 0)
	for rows.Next() {
		var view RuleActionDeadLetterView
		var triggerRaw, kindRaw string
		var deadLetteredAt, createdAt time.Time
		if err := rows.Scan(
			&view.OutboxID, &view.SiteID, &view.AlarmID, &view.AlarmVersion, &view.AssignmentID,
			&view.ActionIndex, &triggerRaw, &kindRaw, &view.TargetRef, &view.Attempts,
			&view.LastErrorCode, &deadLetteredAt, &createdAt,
		); err != nil {
			return RuleActionDeadLetterList{}, fmt.Errorf("scan Alarm rule action dead letter: %w", err)
		}
		view.TriggerKind = ActionTriggerOn(triggerRaw)
		view.ActionKind = RuleActionKind(kindRaw)
		view.DeadLetteredAt = deadLetteredAt.UTC().Format(time.RFC3339Nano)
		view.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		items = append(items, view)
	}
	if err := rows.Err(); err != nil {
		return RuleActionDeadLetterList{}, fmt.Errorf("iterate Alarm rule action dead letters: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RuleActionDeadLetterList{}, fmt.Errorf("commit Alarm rule action dead letters read: %w", err)
	}
	return RuleActionDeadLetterList{Items: items}, nil
}

func (store *PostgresStore) ReplayRuleActionDeadLetter(ctx context.Context, tenantID, siteID, outboxID string, now time.Time) (RuleActionStatus, error) {
	if store == nil || store.pool == nil {
		return "", ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) || !alarmmodel.IsUUIDv7(outboxID) {
		return "", alarmmodel.ErrInvalidOperation
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	nowUTC := now.UTC()
	command, err := tx.Exec(ctx, `
UPDATE alarm_runtime.rule_action_outbox
SET status = 'PENDING',
    attempts = 0,
    available_at = $4,
    lease_owner = NULL,
    lease_until = NULL,
    delivered_at = NULL,
    dead_lettered_at = NULL,
    last_error_code = NULL,
    updated_at = $4
WHERE tenant_id = $1 AND site_id = $2 AND outbox_id = $3 AND status = 'DEAD_LETTER'`,
		tenantID, siteID, outboxID, nowUTC)
	if err != nil {
		return "", fmt.Errorf("replay Alarm rule action dead letter: %w", err)
	}
	status := RuleActionStatusPending
	if command.RowsAffected() == 0 {
		var existingStatus string
		scanErr := tx.QueryRow(ctx, `
SELECT status
FROM alarm_runtime.rule_action_outbox
WHERE tenant_id = $1 AND site_id = $2 AND outbox_id = $3`,
			tenantID, siteID, outboxID).Scan(&existingStatus)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		if scanErr != nil {
			return "", fmt.Errorf("check Alarm rule action dead letter status: %w", scanErr)
		}
		switch RuleActionStatus(existingStatus) {
		case RuleActionStatusPending, RuleActionStatusDelivered:
			status = RuleActionStatus(existingStatus)
		default:
			return "", alarmmodel.ErrInvalidOperation
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit Alarm rule action dead letter replay: %w", err)
	}
	return status, nil
}
