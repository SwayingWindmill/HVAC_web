package outbounddelivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type AdapterType string

const AdapterRESTWebhook AdapterType = "REST_WEBHOOK"

type IntentState string

const (
	IntentReady          IntentState = "READY"
	IntentLeased         IntentState = "LEASED"
	IntentRetryWait      IntentState = "RETRY_WAIT"
	IntentDelivered      IntentState = "DELIVERED"
	IntentOutcomeUnknown IntentState = "OUTCOME_UNKNOWN"
	IntentDead           IntentState = "DEAD"
)

type OutcomeClass string

const (
	OutcomeNotSent              OutcomeClass = "NOT_SENT"
	OutcomeMaybeSent            OutcomeClass = "MAYBE_SENT"
	OutcomeAcceptedNotConfirmed OutcomeClass = "ACCEPTED_NOT_CONFIRMED"
	OutcomeDelivered            OutcomeClass = "DELIVERED"
	OutcomeFailed               OutcomeClass = "FAILED"
)

const (
	MaxRequestBodyBytes   int64 = 1 << 20
	MaxResponseBodyBytes  int64 = 256 << 10
	MaxAdapterConcurrency       = 32
	MaxDeliveryAttempts         = 5
)

const MaxDeliveryTimeout = 30 * time.Second

var (
	ErrIdempotencyConflict = errors.New("delivery idempotency key reused with different intent")
	ErrRevisionConflict    = errors.New("integration definition revision conflict")
	ErrLeaseLost           = errors.New("delivery attempt lease is no longer owned by this worker")
	ErrReplayRiskRequired  = errors.New("replay approval must acknowledge potential duplicate delivery")
	ErrNothingReady        = errors.New("no delivery intent is ready")
)

type Scope struct {
	TenantID string
}

type IntegrationDefinition struct {
	ID                   string
	TenantID             string
	Name                 string
	Revision             uint64
	AdapterType          AdapterType
	DestinationURL       string
	AllowedHosts         []string
	CredentialRef        string
	Enabled              bool
	MaxRequestBytes      int64
	MaxResponseBytes     int64
	Timeout              time.Duration
	MaxConcurrency       int
	MaxAttempts          int
	RetryDelay           time.Duration
	CreatedAt            time.Time
	CreatedByPrincipalID string
}

func (definition IntegrationDefinition) Validate() error {
	if strings.TrimSpace(definition.TenantID) == "" {
		return errors.New("integration tenant id is required")
	}
	if strings.TrimSpace(definition.Name) == "" {
		return errors.New("integration name is required")
	}
	if definition.AdapterType != AdapterRESTWebhook {
		return fmt.Errorf("unsupported integration adapter %q", definition.AdapterType)
	}
	parsed, err := url.Parse(definition.DestinationURL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("integration destination must be an absolute http or https URL")
	}
	if parsed.User != nil {
		return errors.New("integration destination must not contain URL credentials")
	}
	if !hostAllowed(parsed.Hostname(), definition.AllowedHosts) {
		return errors.New("integration destination host is not in the destination allowlist")
	}
	if definition.MaxRequestBytes <= 0 || definition.MaxRequestBytes > MaxRequestBodyBytes {
		return fmt.Errorf("integration request body limit must be within 1..%d bytes", MaxRequestBodyBytes)
	}
	if definition.MaxResponseBytes <= 0 || definition.MaxResponseBytes > MaxResponseBodyBytes {
		return fmt.Errorf("integration response body limit must be within 1..%d bytes", MaxResponseBodyBytes)
	}
	if definition.Timeout <= 0 || definition.Timeout > MaxDeliveryTimeout {
		return fmt.Errorf("integration timeout must be within 1ns..%s", MaxDeliveryTimeout)
	}
	if definition.MaxConcurrency <= 0 || definition.MaxConcurrency > MaxAdapterConcurrency {
		return fmt.Errorf("integration concurrency must be within 1..%d", MaxAdapterConcurrency)
	}
	if definition.MaxAttempts <= 0 || definition.MaxAttempts > MaxDeliveryAttempts {
		return fmt.Errorf("integration attempts must be within 1..%d", MaxDeliveryAttempts)
	}
	if definition.RetryDelay <= 0 || definition.RetryDelay > 24*time.Hour {
		return errors.New("integration retry delay must be within 1ns..24h")
	}
	if definition.CreatedAt.IsZero() {
		return errors.New("integration revision creation time is required")
	}
	return nil
}

type PutIntegrationRequest struct {
	Definition       IntegrationDefinition
	ExpectedRevision uint64
}

type SubmitIntentRequest struct {
	TenantID            string
	SiteID              string
	IntegrationID       string
	Purpose             string
	PayloadSchema       string
	Payload             json.RawMessage
	IdempotencyKey      string
	SourceAggregateType string
	SourceAggregateID   string
	Classification      string
	CreatedAt           time.Time
}

func (request SubmitIntentRequest) Validate() error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.IntegrationID) == "" {
		return errors.New("delivery tenant id and integration id are required")
	}
	if strings.TrimSpace(request.Purpose) == "" || strings.TrimSpace(request.PayloadSchema) == "" {
		return errors.New("delivery purpose and payload schema are required")
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return errors.New("delivery idempotency key is required")
	}
	if len(request.Payload) == 0 || !json.Valid(request.Payload) {
		return errors.New("delivery payload must be valid JSON")
	}
	if strings.TrimSpace(request.SourceAggregateType) == "" || strings.TrimSpace(request.SourceAggregateID) == "" {
		return errors.New("delivery source aggregate identity is required")
	}
	if strings.TrimSpace(request.Classification) == "" {
		return errors.New("delivery data classification is required")
	}
	if request.CreatedAt.IsZero() {
		return errors.New("delivery creation time is required")
	}
	return nil
}

type DeliveryIntent struct {
	ID                  string
	TenantID            string
	SiteID              string
	IntegrationID       string
	Purpose             string
	PayloadSchema       string
	Payload             json.RawMessage
	PayloadDigest       string
	IdempotencyKey      string
	SourceAggregateType string
	SourceAggregateID   string
	Classification      string
	State               IntentState
	AttemptCount        int
	NextRetryAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type DeliveryAttempt struct {
	ID                  string
	IntentID            string
	AttemptNo           int
	IntegrationRevision uint64
	Outcome             OutcomeClass
	Retryable           bool
	ErrorCode           string
	ProviderRequestID   string
	HTTPStatus          int
	ResponseDigest      string
	StartedAt           time.Time
	CompletedAt         *time.Time
	LeaseOwner          string
	LeaseUntil          time.Time
}

type ClaimedDelivery struct {
	Intent      DeliveryIntent
	Attempt     DeliveryAttempt
	Integration IntegrationDefinition
}

type AdapterResult struct {
	Outcome           OutcomeClass
	Retryable         bool
	ErrorCode         string
	ProviderRequestID string
	ProviderMessageID string
	HTTPStatus        int
	ResponseDigest    string
}

type DeliveryReceipt struct {
	ID                string
	IntentID          string
	AttemptID         string
	ProviderRequestID string
	ProviderMessageID string
	HTTPStatus        int
	ResponseDigest    string
	FinalOutcome      OutcomeClass
	AcceptedAt        time.Time
}

type DeadLetter struct {
	ID                       string
	IntentID                 string
	AttemptID                string
	ReasonCode               string
	RequiresDuplicateRiskAck bool
	CreatedAt                time.Time
}

type ReplayApproval struct {
	ID                  string
	DeadLetterID        string
	IntentID            string
	ApprovedByPrincipal string
	Reason              string
	AcceptDuplicateRisk bool
	CreatedAt           time.Time
}

type ReplayRequest struct {
	TenantID            string
	DeadLetterID        string
	ApprovedByPrincipal string
	Reason              string
	AcceptDuplicateRisk bool
	ApprovedAt          time.Time
}

func PayloadDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func hostAllowed(host string, allowlist []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, allowed := range allowlist {
		if host == strings.ToLower(strings.TrimSuffix(strings.TrimSpace(allowed), ".")) {
			return true
		}
	}
	return false
}

type completionDecision struct {
	State                    IntentState
	RetryAt                  *time.Time
	DeadLetter               bool
	RequiresDuplicateRiskAck bool
}

func decideCompletion(result AdapterResult, attemptNo, maxAttempts int, retryDelay time.Duration, now time.Time) completionDecision {
	switch result.Outcome {
	case OutcomeDelivered:
		return completionDecision{State: IntentDelivered}
	case OutcomeAcceptedNotConfirmed, OutcomeMaybeSent:
		return completionDecision{State: IntentOutcomeUnknown, DeadLetter: true, RequiresDuplicateRiskAck: true}
	case OutcomeNotSent:
		if result.Retryable && attemptNo < maxAttempts {
			retryAt := now.Add(retryDelay)
			return completionDecision{State: IntentRetryWait, RetryAt: &retryAt}
		}
		return completionDecision{State: IntentDead, DeadLetter: true}
	case OutcomeFailed:
		return completionDecision{State: IntentDead, DeadLetter: true}
	default:
		return completionDecision{State: IntentOutcomeUnknown, DeadLetter: true, RequiresDuplicateRiskAck: true}
	}
}
