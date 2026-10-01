package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Realtime subscriptions are authorized when IAM consumes the single-use subscribe
// grant at bootstrap; the subscription row records that decision for at most
// MaximumSubscriptionTTL. IAM records every later change that can withdraw telemetry
// access as an ordered revocation fact. The relay applies those facts to the
// subscriptions they invalidate, so access withdrawal reaches open streams without
// waiting for the subscription to expire.

const iamRevocationPollPath = "/internal/v1/telemetry/revocations:poll"

// RevocationFact withdraws telemetry access a principal was granted before OccurredAt,
// for one device or, when DeviceID is empty, for every device.
type RevocationFact struct {
	Sequence    int64
	TenantID    string
	PrincipalID string
	DeviceID    string
	OccurredAt  time.Time
}

type RevocationSource interface {
	PollRevocations(ctx context.Context, tenantID string, afterSequence int64, limit int) ([]RevocationFact, error)
}

// RevocationCursor is the last IAM fact applied for a tenant that has open subscriptions.
type RevocationCursor struct {
	TenantID      string
	AfterSequence int64
}

type RevocationCursorStore interface {
	RevocationCursors(ctx context.Context, now time.Time) ([]RevocationCursor, error)
	SaveRevocationCursor(ctx context.Context, tenantID string, sequence int64, now time.Time) error
}

type RevocationRelay struct {
	source   RevocationSource
	cursors  RevocationCursorStore
	realtime *RealtimeService
	now      func() time.Time
	limit    int
}

func NewRevocationRelay(source RevocationSource, cursors RevocationCursorStore, realtime *RealtimeService, now func() time.Time) (*RevocationRelay, error) {
	if source == nil || cursors == nil || realtime == nil {
		return nil, errors.New("revocation relay requires an IAM source, a cursor store and the realtime service")
	}
	if now == nil {
		now = time.Now
	}
	return &RevocationRelay{source: source, cursors: cursors, realtime: realtime, now: now, limit: 200}, nil
}

// RelayOnce applies the pending IAM revocation facts of every tenant with open
// subscriptions and returns the number of revoked subscriptions.
func (relay *RevocationRelay) RelayOnce(ctx context.Context) (int, error) {
	cursors, err := relay.cursors.RevocationCursors(ctx, relay.now().UTC())
	if err != nil {
		return 0, err
	}
	revoked := 0
	for _, cursor := range cursors {
		facts, err := relay.source.PollRevocations(ctx, cursor.TenantID, cursor.AfterSequence, relay.limit)
		if err != nil {
			return revoked, err
		}
		applied := cursor.AfterSequence
		for _, fact := range facts {
			if fact.TenantID != cursor.TenantID || fact.Sequence <= applied || fact.PrincipalID == "" {
				return revoked, errors.New("IAM revocation facts are out of scope or out of order")
			}
			count, err := relay.realtime.Revoke(ctx, fact.PrincipalID, fact.DeviceID, fact.OccurredAt)
			revoked += count
			if err != nil {
				return revoked, err
			}
			applied = fact.Sequence
		}
		if applied != cursor.AfterSequence {
			if err := relay.cursors.SaveRevocationCursor(ctx, cursor.TenantID, applied, relay.now().UTC()); err != nil {
				return revoked, err
			}
		}
	}
	return revoked, nil
}

// HTTPRevocationSource polls the IAM owner over the workload mTLS client.
type HTTPRevocationSource struct {
	endpoint string
	client   *http.Client
}

func NewHTTPRevocationSource(iamEndpoint string, client *http.Client) (*HTTPRevocationSource, error) {
	parsed, err := url.Parse(strings.TrimSpace(iamEndpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()))) {
		return nil, errors.New("IAM revocation endpoint must use HTTPS or loopback HTTP")
	}
	if client == nil {
		return nil, errors.New("IAM revocation client is required")
	}
	return &HTTPRevocationSource{endpoint: strings.TrimRight(parsed.String(), "/") + iamRevocationPollPath, client: client}, nil
}

type revocationPollRequest struct {
	TenantID      string `json:"tenantId"`
	AfterSequence int64  `json:"afterSequence"`
	Limit         int    `json:"limit"`
}

type revocationPollFact struct {
	Sequence       int64  `json:"sequence"`
	PrincipalID    string `json:"principalId"`
	TenantID       string `json:"tenantId"`
	SourceType     string `json:"sourceType"`
	SourceID       string `json:"sourceId,omitempty"`
	DeviceID       string `json:"deviceId,omitempty"`
	TelemetryKey   string `json:"telemetryKey,omitempty"`
	Action         string `json:"action,omitempty"`
	PolicyRevision string `json:"policyRevision"`
	ReasonCode     string `json:"reasonCode"`
	OccurredAt     string `json:"occurredAt"`
}

type revocationPollResponse struct {
	Facts        []revocationPollFact `json:"facts"`
	NextSequence int64                `json:"nextSequence"`
}

func (source *HTTPRevocationSource) PollRevocations(ctx context.Context, tenantID string, afterSequence int64, limit int) ([]RevocationFact, error) {
	payload, err := json.Marshal(revocationPollRequest{TenantID: tenantID, AfterSequence: afterSequence, Limit: limit})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, source.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := source.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("poll IAM revocations: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("poll IAM revocations: status %d", response.StatusCode)
	}
	var body revocationPollResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return nil, fmt.Errorf("decode IAM revocations: %w", err)
	}
	facts := make([]RevocationFact, 0, len(body.Facts))
	for _, fact := range body.Facts {
		occurredAt, err := time.Parse(time.RFC3339Nano, fact.OccurredAt)
		if err != nil {
			return nil, fmt.Errorf("decode IAM revocation time: %w", err)
		}
		// IAM reports instants truncated to milliseconds; round up so an authorization
		// earlier in the same millisecond is still withdrawn.
		facts = append(facts, RevocationFact{
			Sequence: fact.Sequence, TenantID: fact.TenantID, PrincipalID: fact.PrincipalID,
			DeviceID: fact.DeviceID, OccurredAt: occurredAt.Truncate(time.Millisecond).Add(time.Millisecond),
		})
	}
	return facts, nil
}
