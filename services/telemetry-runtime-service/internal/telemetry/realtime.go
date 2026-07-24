package telemetry

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

const (
	InternalSubscriptionBootstrapPath = "/internal/v1/telemetry/subscriptions:bootstrap"
	InternalRecoveryCheckpointPath    = "/internal/v1/telemetry/recovery-cursors:checkpoint"
	InternalCentrifugoSubscribePath   = "/internal/v1/telemetry/centrifugo/subscribe"
	InternalSubscriptionRevokePath    = "/internal/v1/telemetry/subscriptions:revoke"

	MaximumRealtimeSubscriptions = 100
	MaximumRecoveryCheckpoints   = 100
	MaximumSubscriptionTTL       = 5 * time.Minute
	MaximumConnectionTokenTTL    = 5 * time.Minute
	DefaultSubscriptionTTL       = MaximumSubscriptionTTL
	DefaultConnectionTokenTTL    = MaximumConnectionTokenTTL
	DefaultRecoveryCursorTTL     = 120 * time.Second
)

var (
	ErrRealtimeUnavailable    = errors.New("telemetry realtime unavailable")
	ErrSubscriptionNotFound   = errors.New("telemetry subscription not found")
	ErrSubscriptionConflict   = errors.New("telemetry subscription conflict")
	ErrRecoveryCursorRejected = errors.New("telemetry recovery cursor rejected")
	ErrPublicationNotFound    = errors.New("telemetry publication not found")

	clientSubscriptionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	opaqueSubscriptionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`)
	transportEpochPattern       = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

type SubscriptionStatus string

const (
	SubscriptionActive  SubscriptionStatus = "ACTIVE"
	SubscriptionRevoked SubscriptionStatus = "REVOKED"
	SubscriptionExpired SubscriptionStatus = "EXPIRED"
)

type RealtimeSubscription struct {
	SubscriptionID       string
	ClientSubscriptionID string
	PrincipalID          string
	Subject              string
	SubjectIssuer        string
	SessionID            string
	ActingOrganizationID string
	DeviceID             string
	Keys                 []string
	ScopeDigest          string
	PolicyRevision       string
	Channel              string
	Status               SubscriptionStatus
	ExpiresAt            time.Time
	RevokedAt            *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CheckpointIdentity struct {
	Subject              string
	SubjectIssuer        string
	SessionID            string
	ActingOrganizationID string
}

type RecoveryCursorRecord struct {
	CursorID         string
	SubscriptionID   string
	PrincipalID      string
	BusinessRevision int64
	TransportEpoch   string
	TransportOffset  int64
	ScopeDigest      string
	CursorSHA256     string
	ExpiresAt        time.Time
	RevokedAt        *time.Time
	CreatedAt        time.Time
}

type PendingPublication struct {
	EventID          string
	DeviceID         string
	PreviousRevision int64
	Revision         int64
	EvaluatedAt      time.Time
	Snapshot         telemetryapi.DeviceObservationSnapshot
	ChangedKeys      []string
}

type RealtimeRepository interface {
	SaveSubscriptions(context.Context, []RealtimeSubscription) error
	ActiveSubscription(context.Context, string, time.Time) (RealtimeSubscription, error)
	ActiveSubscriptionByChannel(context.Context, string, string, time.Time) (RealtimeSubscription, error)
	ActiveSubscriptionsForDevice(context.Context, string, time.Time) ([]RealtimeSubscription, error)
	SaveRecoveryCursors(context.Context, []RecoveryCursorRecord) error
	ActiveRecoveryCursor(context.Context, string, string, time.Time) (RecoveryCursorRecord, error)
	CurrentBusinessRevision(context.Context, string) (int64, error)
	PendingPublications(context.Context, int, time.Time) ([]PendingPublication, error)
	MarkPublicationPublished(context.Context, string, time.Time) error
	RevokeSubscriptions(context.Context, string, string, time.Time) ([]RealtimeSubscription, error)
}

type RealtimeTransport interface {
	Publish(context.Context, string, DeviceObservationPublication) error
	Unsubscribe(context.Context, string, string) error
}

type RealtimeConfig struct {
	Repository             RealtimeRepository
	Transport              RealtimeTransport
	PublicEndpoint         string
	CapabilityHMACKey      []byte
	ConnectionTokenHMACKey []byte
	SubscriptionTTL        time.Duration
	ConnectionTokenTTL     time.Duration
	RecoveryCursorTTL      time.Duration
	Now                    func() time.Time
	NewOpaqueID            func() (string, error)
}

type RealtimeService struct {
	repository         RealtimeRepository
	transport          RealtimeTransport
	publicEndpoint     string
	capabilityKey      []byte
	connectionTokenKey []byte
	subscriptionTTL    time.Duration
	connectionTokenTTL time.Duration
	recoveryCursorTTL  time.Duration
	now                func() time.Time
	newOpaqueID        func() (string, error)
}

type recoveryCursorClaims struct {
	Version              int      `json:"v"`
	CursorID             string   `json:"cid"`
	SubscriptionID       string   `json:"sid"`
	PrincipalID          string   `json:"pid"`
	Subject              string   `json:"sub"`
	SubjectIssuer        string   `json:"iss"`
	SessionID            string   `json:"session"`
	ActingOrganizationID string   `json:"org"`
	DeviceID             string   `json:"did"`
	Keys                 []string `json:"keys"`
	ScopeDigest          string   `json:"scope"`
	BusinessRevision     int64    `json:"rev"`
	TransportEpoch       string   `json:"epoch"`
	TransportOffset      int64    `json:"offset"`
	ExpiresAt            int64    `json:"exp"`
}

type connectionClaims struct {
	Subject              string `json:"sub"`
	ActingOrganizationID string `json:"org"`
	SessionID            string `json:"session"`
	IssuedAt             int64  `json:"iat"`
	ExpiresAt            int64  `json:"exp"`
	TokenID              string `json:"jti"`
}

type DeviceObservationPublication = telemetryapi.DeviceObservationPublication

type RecoveryDisposition string

const (
	RecoveryAttemptTransport RecoveryDisposition = "ATTEMPT_TRANSPORT_RECOVERY"
	RecoveryLoadSnapshot     RecoveryDisposition = "LOAD_AUTHORITATIVE_SNAPSHOT"
)

type RecoveryEvidence struct {
	WasRecovering        bool
	Recovered            bool
	ExpectedEpoch        string
	RecoveredEpoch       string
	AppliedRevision      int64
	PublicationRevisions []int64
}

func NewRealtimeService(config RealtimeConfig) (*RealtimeService, error) {
	if config.Repository == nil || config.Transport == nil {
		return nil, errors.New("realtime repository and transport are required")
	}
	if !strings.HasPrefix(strings.TrimSpace(config.PublicEndpoint), "wss://") {
		return nil, errors.New("realtime public endpoint must use wss")
	}
	if len(config.CapabilityHMACKey) < 32 || len(config.ConnectionTokenHMACKey) < 32 {
		return nil, errors.New("realtime HMAC keys must be at least 32 bytes")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newOpaqueID := config.NewOpaqueID
	if newOpaqueID == nil {
		newOpaqueID = randomOpaqueID
	}
	subscriptionTTL := positiveDuration(config.SubscriptionTTL, DefaultSubscriptionTTL)
	connectionTTL := positiveDuration(config.ConnectionTokenTTL, DefaultConnectionTokenTTL)
	cursorTTL := positiveDuration(config.RecoveryCursorTTL, DefaultRecoveryCursorTTL)
	if cursorTTL > DefaultRecoveryCursorTTL {
		return nil, errors.New("recovery cursor TTL exceeds the S2 maximum")
	}
	if connectionTTL > subscriptionTTL {
		return nil, errors.New("connection token TTL cannot exceed subscription TTL")
	}
	return &RealtimeService{
		repository:         config.Repository,
		transport:          config.Transport,
		publicEndpoint:     strings.TrimSpace(config.PublicEndpoint),
		capabilityKey:      append([]byte(nil), config.CapabilityHMACKey...),
		connectionTokenKey: append([]byte(nil), config.ConnectionTokenHMACKey...),
		subscriptionTTL:    subscriptionTTL,
		connectionTokenTTL: connectionTTL,
		recoveryCursorTTL:  cursorTTL,
		now:                now,
		newOpaqueID:        newOpaqueID,
	}, nil
}

func positiveDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func (service *RealtimeService) Bootstrap(ctx context.Context, access AccessContext, input telemetryapi.SubscriptionBootstrapRequest) (telemetryapi.SubscriptionBootstrapResponse, error) {
	if service == nil || service.repository == nil {
		return telemetryapi.SubscriptionBootstrapResponse{}, ErrRealtimeUnavailable
	}
	if strings.TrimSpace(access.PrincipalID) == "" || strings.TrimSpace(access.ActingOrganizationID) == "" || strings.TrimSpace(access.SessionID) == "" {
		return telemetryapi.SubscriptionBootstrapResponse{}, ErrRecoveryCursorRejected
	}
	if len(input.Subscriptions) == 0 || len(input.Subscriptions) > MaximumRealtimeSubscriptions {
		return telemetryapi.SubscriptionBootstrapResponse{}, ErrSubscriptionConflict
	}
	if _, err := aggregateSubscriptionTargets(input.Subscriptions); err != nil {
		return telemetryapi.SubscriptionBootstrapResponse{}, ErrSubscriptionConflict
	}

	now := service.now().UTC()
	expiresAt := now.Add(service.subscriptionTTL)
	seenClientIDs := make(map[string]struct{}, len(input.Subscriptions))
	subscriptions := make([]RealtimeSubscription, 0, len(input.Subscriptions))
	descriptors := make([]telemetryapi.SubscriptionDescriptor, 0, len(input.Subscriptions))

	for _, requested := range input.Subscriptions {
		clientID := string(requested.ClientSubscriptionId)
		if !clientSubscriptionIDPattern.MatchString(clientID) {
			return telemetryapi.SubscriptionBootstrapResponse{}, ErrSubscriptionConflict
		}
		if _, duplicate := seenClientIDs[clientID]; duplicate {
			return telemetryapi.SubscriptionBootstrapResponse{}, ErrSubscriptionConflict
		}
		seenClientIDs[clientID] = struct{}{}

		target := telemetryauth.Target{DeviceID: string(requested.DeviceId), Keys: telemetryKeysToStrings(requested.Keys)}
		canonical, err := telemetryauth.CanonicalTargets([]telemetryauth.Target{target})
		if err != nil || len(canonical) != 1 {
			return telemetryapi.SubscriptionBootstrapResponse{}, ErrSubscriptionConflict
		}
		target = canonical[0]
		scopeDigest, err := telemetryauth.ScopeDigest(telemetryauth.ActionSubscribe, access.ActingOrganizationID, []telemetryauth.Target{target})
		if err != nil {
			return telemetryapi.SubscriptionBootstrapResponse{}, ErrSubscriptionConflict
		}

		subscriptionID := ""
		recoveryMode := "SNAPSHOT_THEN_LIVE"
		var transportPosition *telemetryapi.TransportPosition
		var returnedCursor *telemetryapi.OpaqueRecoveryCursor
		if requested.RecoveryCursor != nil {
			claims, err := service.verifyRecoveryCursor(string(*requested.RecoveryCursor), access, target, now)
			if err != nil {
				return telemetryapi.SubscriptionBootstrapResponse{}, err
			}
			active, err := service.repository.ActiveSubscription(ctx, claims.SubscriptionID, now)
			if err != nil || !sameSubscriptionScope(active, access, target) {
				return telemetryapi.SubscriptionBootstrapResponse{}, ErrRecoveryCursorRejected
			}
			digest := sha256.Sum256([]byte(string(*requested.RecoveryCursor)))
			storedCursor, err := service.repository.ActiveRecoveryCursor(ctx, fmt.Sprintf("%x", digest[:]), claims.SubscriptionID, now)
			if err != nil || storedCursor.BusinessRevision != claims.BusinessRevision || storedCursor.TransportEpoch != claims.TransportEpoch || storedCursor.TransportOffset != claims.TransportOffset || storedCursor.ScopeDigest != claims.ScopeDigest {
				return telemetryapi.SubscriptionBootstrapResponse{}, ErrRecoveryCursorRejected
			}
			subscriptionID = claims.SubscriptionID
			recoveryMode = "ATTEMPT_RECOVERY"
			transportPosition = &telemetryapi.TransportPosition{Epoch: claims.TransportEpoch, Offset: claims.TransportOffset}
			cursor := telemetryapi.OpaqueRecoveryCursor(*requested.RecoveryCursor)
			returnedCursor = &cursor
		}
		if subscriptionID == "" {
			subscriptionID, err = service.newOpaqueID()
			if err != nil || !opaqueSubscriptionIDPattern.MatchString(subscriptionID) {
				return telemetryapi.SubscriptionBootstrapResponse{}, ErrRealtimeUnavailable
			}
		}

		channel := service.channelForSubscription(subscriptionID)
		subscriptions = append(subscriptions, RealtimeSubscription{
			SubscriptionID:       subscriptionID,
			ClientSubscriptionID: clientID,
			PrincipalID:          access.PrincipalID,
			Subject:              access.Subject,
			SubjectIssuer:        access.SubjectIssuer,
			SessionID:            access.SessionID,
			ActingOrganizationID: access.ActingOrganizationID,
			DeviceID:             target.DeviceID,
			Keys:                 append([]string(nil), target.Keys...),
			ScopeDigest:          scopeDigest,
			PolicyRevision:       access.PolicyRevision,
			Channel:              channel,
			Status:               SubscriptionActive,
			ExpiresAt:            expiresAt,
			CreatedAt:            now,
			UpdatedAt:            now,
		})
		descriptors = append(descriptors, telemetryapi.SubscriptionDescriptor{
			ClientSubscriptionId: requested.ClientSubscriptionId,
			SubscriptionId:       telemetryapi.OpaqueSubscriptionId(subscriptionID),
			DeviceId:             requested.DeviceId,
			Keys:                 append([]telemetryapi.TelemetryKey(nil), requested.Keys...),
			Channel:              telemetryapi.OpaqueChannel(channel),
			RecoveryMode:         recoveryMode,
			TransportPosition:    transportPosition,
			RecoveryCursor:       returnedCursor,
		})
	}

	if err := service.repository.SaveSubscriptions(ctx, subscriptions); err != nil {
		return telemetryapi.SubscriptionBootstrapResponse{}, normalizeRealtimeRepositoryError(err)
	}
	connectionExpiresAt := now.Add(service.connectionTokenTTL)
	connectionToken, err := service.connectionToken(access, now, connectionExpiresAt)
	if err != nil {
		return telemetryapi.SubscriptionBootstrapResponse{}, ErrRealtimeUnavailable
	}
	return telemetryapi.SubscriptionBootstrapResponse{
		SchemaVersion:     1,
		TransportProtocol: "CENTRIFUGO_JSON_V1",
		Endpoint:          service.publicEndpoint,
		ConnectionToken:   connectionToken,
		ExpiresAt:         instant(connectionExpiresAt),
		Subscriptions:     descriptors,
		Limits: telemetryapi.SubscriptionLimits{
			MaxSubscriptions:       MaximumRealtimeSubscriptions,
			MaxKeysPerSubscription: telemetryauth.MaximumKeysPerTarget,
			MaxTotalKeySelections:  telemetryauth.MaximumTotalKeys,
		},
	}, nil
}

func (service *RealtimeService) CheckpointTargets(ctx context.Context, input telemetryapi.RecoveryCursorCheckpointRequest) ([]telemetryauth.Target, error) {
	return service.checkpointTargets(ctx, CheckpointIdentity{}, input, false)
}

func (service *RealtimeService) CheckpointTargetsForIdentity(ctx context.Context, identity CheckpointIdentity, input telemetryapi.RecoveryCursorCheckpointRequest) ([]telemetryauth.Target, error) {
	if identity.Subject == "" || identity.SubjectIssuer == "" || identity.SessionID == "" || identity.ActingOrganizationID == "" {
		return nil, ErrRecoveryCursorRejected
	}
	return service.checkpointTargets(ctx, identity, input, true)
}

func (service *RealtimeService) checkpointTargets(ctx context.Context, identity CheckpointIdentity, input telemetryapi.RecoveryCursorCheckpointRequest, requireIdentity bool) ([]telemetryauth.Target, error) {
	if service == nil || service.repository == nil || len(input.Checkpoints) == 0 || len(input.Checkpoints) > MaximumRecoveryCheckpoints {
		return nil, ErrSubscriptionConflict
	}
	now := service.now().UTC()
	byDevice := make(map[string]map[string]struct{})
	seen := make(map[string]struct{}, len(input.Checkpoints))
	for _, checkpoint := range input.Checkpoints {
		subscriptionID := string(checkpoint.SubscriptionId)
		if _, duplicate := seen[subscriptionID]; duplicate || !opaqueSubscriptionIDPattern.MatchString(subscriptionID) {
			return nil, ErrSubscriptionConflict
		}
		seen[subscriptionID] = struct{}{}
		subscription, err := service.repository.ActiveSubscription(ctx, subscriptionID, now)
		if err != nil {
			return nil, ErrRecoveryCursorRejected
		}
		if requireIdentity && (subscription.Subject != identity.Subject || subscription.SubjectIssuer != identity.SubjectIssuer ||
			subscription.SessionID != identity.SessionID || subscription.ActingOrganizationID != identity.ActingOrganizationID) {
			return nil, ErrRecoveryCursorRejected
		}
		if _, ok := byDevice[subscription.DeviceID]; !ok {
			byDevice[subscription.DeviceID] = map[string]struct{}{}
		}
		for _, key := range subscription.Keys {
			byDevice[subscription.DeviceID][key] = struct{}{}
		}
	}
	targets := make([]telemetryauth.Target, 0, len(byDevice))
	for deviceID, keySet := range byDevice {
		keys := make([]string, 0, len(keySet))
		for key := range keySet {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		targets = append(targets, telemetryauth.Target{DeviceID: deviceID, Keys: keys})
	}
	return telemetryauth.CanonicalTargets(targets)
}

func (service *RealtimeService) Checkpoint(ctx context.Context, access AccessContext, input telemetryapi.RecoveryCursorCheckpointRequest) (telemetryapi.RecoveryCursorCheckpointResponse, error) {
	if service == nil || service.repository == nil {
		return telemetryapi.RecoveryCursorCheckpointResponse{}, ErrRealtimeUnavailable
	}
	if len(input.Checkpoints) == 0 || len(input.Checkpoints) > MaximumRecoveryCheckpoints {
		return telemetryapi.RecoveryCursorCheckpointResponse{}, ErrSubscriptionConflict
	}
	now := service.now().UTC()
	seen := make(map[string]struct{}, len(input.Checkpoints))
	records := make([]RecoveryCursorRecord, 0, len(input.Checkpoints))
	results := make([]telemetryapi.RecoveryCursorCheckpointResult, 0, len(input.Checkpoints))
	for _, checkpoint := range input.Checkpoints {
		subscriptionID := string(checkpoint.SubscriptionId)
		if !opaqueSubscriptionIDPattern.MatchString(subscriptionID) || checkpoint.BusinessRevision < 1 || !validTransportPosition(checkpoint.TransportPosition) {
			return telemetryapi.RecoveryCursorCheckpointResponse{}, ErrSubscriptionConflict
		}
		if _, duplicate := seen[subscriptionID]; duplicate {
			return telemetryapi.RecoveryCursorCheckpointResponse{}, ErrSubscriptionConflict
		}
		seen[subscriptionID] = struct{}{}
		subscription, err := service.repository.ActiveSubscription(ctx, subscriptionID, now)
		if err != nil || subscription.PrincipalID != access.PrincipalID || subscription.Subject != access.Subject ||
			subscription.SubjectIssuer != access.SubjectIssuer || subscription.SessionID != access.SessionID ||
			subscription.ActingOrganizationID != access.ActingOrganizationID {
			return telemetryapi.RecoveryCursorCheckpointResponse{}, ErrRecoveryCursorRejected
		}
		currentRevision, err := service.repository.CurrentBusinessRevision(ctx, subscription.DeviceID)
		if err != nil || int64(checkpoint.BusinessRevision) > currentRevision {
			return telemetryapi.RecoveryCursorCheckpointResponse{}, ErrRecoveryCursorRejected
		}
		cursorID, err := newUUIDv7(now)
		if err != nil {
			return telemetryapi.RecoveryCursorCheckpointResponse{}, ErrRealtimeUnavailable
		}
		expiresAt := now.Add(service.recoveryCursorTTL)
		claims := recoveryCursorClaims{
			Version:              1,
			CursorID:             cursorID,
			SubscriptionID:       subscription.SubscriptionID,
			PrincipalID:          subscription.PrincipalID,
			Subject:              subscription.Subject,
			SubjectIssuer:        subscription.SubjectIssuer,
			SessionID:            subscription.SessionID,
			ActingOrganizationID: subscription.ActingOrganizationID,
			DeviceID:             subscription.DeviceID,
			Keys:                 append([]string(nil), subscription.Keys...),
			ScopeDigest:          subscription.ScopeDigest,
			BusinessRevision:     int64(checkpoint.BusinessRevision),
			TransportEpoch:       checkpoint.TransportPosition.Epoch,
			TransportOffset:      checkpoint.TransportPosition.Offset,
			ExpiresAt:            expiresAt.Unix(),
		}
		cursor, err := service.signCapability(claims)
		if err != nil {
			return telemetryapi.RecoveryCursorCheckpointResponse{}, ErrRealtimeUnavailable
		}
		digest := sha256.Sum256([]byte(cursor))
		records = append(records, RecoveryCursorRecord{
			CursorID:         cursorID,
			SubscriptionID:   subscription.SubscriptionID,
			PrincipalID:      subscription.PrincipalID,
			BusinessRevision: claims.BusinessRevision,
			TransportEpoch:   claims.TransportEpoch,
			TransportOffset:  claims.TransportOffset,
			ScopeDigest:      subscription.ScopeDigest,
			CursorSHA256:     fmt.Sprintf("%x", digest[:]),
			ExpiresAt:        expiresAt,
			CreatedAt:        now,
		})
		results = append(results, telemetryapi.RecoveryCursorCheckpointResult{
			SubscriptionId:   checkpoint.SubscriptionId,
			BusinessRevision: checkpoint.BusinessRevision,
			RecoveryCursor:   telemetryapi.OpaqueRecoveryCursor(cursor),
			ExpiresAt:        instant(expiresAt),
		})
	}
	if err := service.repository.SaveRecoveryCursors(ctx, records); err != nil {
		return telemetryapi.RecoveryCursorCheckpointResponse{}, normalizeRealtimeRepositoryError(err)
	}
	return telemetryapi.RecoveryCursorCheckpointResponse{SchemaVersion: 1, Items: results}, nil
}

func (service *RealtimeService) AuthorizeSubscribe(ctx context.Context, principalID, channel string) (RealtimeSubscription, error) {
	if service == nil || service.repository == nil || strings.TrimSpace(principalID) == "" || strings.TrimSpace(channel) == "" {
		return RealtimeSubscription{}, ErrSubscriptionNotFound
	}
	subscription, err := service.repository.ActiveSubscriptionByChannel(ctx, principalID, channel, service.now().UTC())
	if err != nil || subscription.Status != SubscriptionActive || subscription.PrincipalID != principalID || subscription.Channel != channel {
		return RealtimeSubscription{}, ErrSubscriptionNotFound
	}
	return subscription, nil
}

func (service *RealtimeService) RelayOnce(ctx context.Context, limit int) (int, error) {
	if service == nil || service.repository == nil || service.transport == nil {
		return 0, ErrRealtimeUnavailable
	}
	if limit <= 0 || limit > 256 {
		limit = 64
	}
	now := service.now().UTC()
	pending, err := service.repository.PendingPublications(ctx, limit, now)
	if err != nil {
		return 0, normalizeRealtimeRepositoryError(err)
	}
	published := 0
	for _, intent := range pending {
		if intent.Revision < 1 || intent.Revision != intent.PreviousRevision+1 || string(intent.Snapshot.DeviceId) != intent.DeviceID || int64(intent.Snapshot.BusinessRevision) != intent.Revision {
			return published, ErrRealtimeUnavailable
		}
		subscriptions, err := service.repository.ActiveSubscriptionsForDevice(ctx, intent.DeviceID, now)
		if err != nil {
			return published, normalizeRealtimeRepositoryError(err)
		}
		for _, subscription := range subscriptions {
			if err := service.transport.Publish(ctx, subscription.Channel, buildSubscriptionPublication(intent, subscription, now)); err != nil {
				return published, ErrRealtimeUnavailable
			}
		}
		if err := service.repository.MarkPublicationPublished(ctx, intent.EventID, now); err != nil {
			return published, normalizeRealtimeRepositoryError(err)
		}
		published++
	}
	return published, nil
}

func (service *RealtimeService) Revoke(ctx context.Context, principalID, deviceID string) (int, error) {
	if service == nil || service.repository == nil || service.transport == nil {
		return 0, ErrRealtimeUnavailable
	}
	revoked, err := service.repository.RevokeSubscriptions(ctx, principalID, deviceID, service.now().UTC())
	if err != nil {
		return 0, normalizeRealtimeRepositoryError(err)
	}
	for _, subscription := range revoked {
		if err := service.transport.Unsubscribe(ctx, subscription.PrincipalID, subscription.Channel); err != nil {
			return len(revoked), ErrRealtimeUnavailable
		}
	}
	return len(revoked), nil
}

func EvaluateRecovery(evidence RecoveryEvidence) RecoveryDisposition {
	if !evidence.WasRecovering || !evidence.Recovered || evidence.ExpectedEpoch == "" || evidence.RecoveredEpoch != evidence.ExpectedEpoch || evidence.AppliedRevision < 1 {
		return RecoveryLoadSnapshot
	}
	expected := evidence.AppliedRevision + 1
	for _, revision := range evidence.PublicationRevisions {
		if revision < expected {
			continue
		}
		if revision != expected {
			return RecoveryLoadSnapshot
		}
		expected++
	}
	return RecoveryAttemptTransport
}

func buildSubscriptionPublication(intent PendingPublication, subscription RealtimeSubscription, publishedAt time.Time) DeviceObservationPublication {
	changed := make(map[string]struct{}, len(intent.ChangedKeys))
	for _, key := range intent.ChangedKeys {
		changed[key] = struct{}{}
	}
	selected := make(map[string]struct{}, len(subscription.Keys))
	for _, key := range subscription.Keys {
		selected[key] = struct{}{}
	}
	telemetryChanges := make([]telemetryapi.TelemetryKeyState, 0, len(subscription.Keys))
	for _, value := range intent.Snapshot.Values {
		key := telemetryStateKey(value)
		if _, selectedKey := selected[key]; !selectedKey {
			continue
		}
		if _, changedKey := changed[key]; !changedKey {
			continue
		}
		telemetryChanges = append(telemetryChanges, value)
	}
	return DeviceObservationPublication{
		SchemaVersion:          1,
		Kind:                   "DEVICE_OBSERVATION_DELTA",
		EventId:                telemetryapi.UUIDv7(intent.EventID),
		SubscriptionId:         telemetryapi.OpaqueSubscriptionId(subscription.SubscriptionID),
		DeviceId:               telemetryapi.UUIDv7(intent.DeviceID),
		PreviousRevision:       telemetryapi.BusinessRevision(intent.PreviousRevision),
		Revision:               telemetryapi.BusinessRevision(intent.Revision),
		EvaluatedAt:            instant(intent.EvaluatedAt),
		PublishedAt:            instant(publishedAt),
		EvaluationAvailability: intent.Snapshot.EvaluationAvailability,
		AvailabilityReasons:    append([]telemetryapi.AvailabilityReasonCode(nil), intent.Snapshot.AvailabilityReasons...),
		Presence:               intent.Snapshot.Presence,
		TelemetryReadiness:     intent.Snapshot.TelemetryReadiness,
		DisplayState:           intent.Snapshot.DisplayState,
		TelemetryChanges:       telemetryChanges,
	}
}

func telemetryStateKey(value telemetryapi.TelemetryKeyState) string {
	if value.Present != nil {
		return string(value.Present.Key)
	}
	if value.Missing != nil {
		return string(value.Missing.Key)
	}
	return ""
}

func aggregateSubscriptionTargets(requests []telemetryapi.SubscriptionTargetRequest) ([]telemetryauth.Target, error) {
	byDevice := make(map[string]map[string]struct{})
	totalSelections := 0
	for _, requested := range requests {
		deviceID := string(requested.DeviceId)
		if _, ok := byDevice[deviceID]; !ok {
			byDevice[deviceID] = map[string]struct{}{}
		}
		for _, key := range requested.Keys {
			byDevice[deviceID][string(key)] = struct{}{}
			totalSelections++
		}
	}
	if totalSelections > telemetryauth.MaximumTotalKeys {
		return nil, errors.New("subscription key selections exceed the maximum")
	}
	targets := make([]telemetryauth.Target, 0, len(byDevice))
	for deviceID, keySet := range byDevice {
		keys := make([]string, 0, len(keySet))
		for key := range keySet {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		targets = append(targets, telemetryauth.Target{DeviceID: deviceID, Keys: keys})
	}
	return telemetryauth.CanonicalTargets(targets)
}

func telemetryKeysToStrings(keys []telemetryapi.TelemetryKey) []string {
	result := make([]string, len(keys))
	for index, key := range keys {
		result[index] = string(key)
	}
	return result
}

func sameSubscriptionScope(subscription RealtimeSubscription, access AccessContext, target telemetryauth.Target) bool {
	return subscription.Status == SubscriptionActive && subscription.PrincipalID == access.PrincipalID &&
		subscription.Subject == access.Subject && subscription.SubjectIssuer == access.SubjectIssuer &&
		subscription.SessionID == access.SessionID && subscription.ActingOrganizationID == access.ActingOrganizationID &&
		subscription.DeviceID == target.DeviceID && slices.Equal(subscription.Keys, target.Keys)
}

func validTransportPosition(position telemetryapi.TransportPosition) bool {
	return transportEpochPattern.MatchString(position.Epoch) && position.Offset >= 0
}

func (service *RealtimeService) verifyRecoveryCursor(cursor string, access AccessContext, target telemetryauth.Target, now time.Time) (recoveryCursorClaims, error) {
	var claims recoveryCursorClaims
	if err := service.verifyCapability(cursor, &claims); err != nil {
		return recoveryCursorClaims{}, ErrRecoveryCursorRejected
	}
	digest, err := telemetryauth.ScopeDigest(telemetryauth.ActionSubscribe, access.ActingOrganizationID, []telemetryauth.Target{target})
	if err != nil || claims.Version != 1 || claims.ExpiresAt <= now.Unix() || claims.SubscriptionID == "" || claims.CursorID == "" ||
		claims.PrincipalID != access.PrincipalID || claims.Subject != access.Subject || claims.SubjectIssuer != access.SubjectIssuer ||
		claims.SessionID != access.SessionID || claims.ActingOrganizationID != access.ActingOrganizationID || claims.DeviceID != target.DeviceID ||
		!slices.Equal(claims.Keys, target.Keys) || claims.ScopeDigest != digest || claims.BusinessRevision < 1 ||
		!validTransportPosition(telemetryapi.TransportPosition{Epoch: claims.TransportEpoch, Offset: claims.TransportOffset}) {
		return recoveryCursorClaims{}, ErrRecoveryCursorRejected
	}
	return claims, nil
}

func (service *RealtimeService) channelForSubscription(subscriptionID string) string {
	mac := hmac.New(sha256.New, service.capabilityKey)
	_, _ = mac.Write([]byte("channel\x00" + subscriptionID))
	return "s2:" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:24])
}

func (service *RealtimeService) connectionToken(access AccessContext, issuedAt, expiresAt time.Time) (string, error) {
	tokenID, err := service.newOpaqueID()
	if err != nil {
		return "", err
	}
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(connectionClaims{
		Subject: access.PrincipalID, ActingOrganizationID: access.ActingOrganizationID,
		SessionID: access.SessionID, IssuedAt: issuedAt.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: tokenID,
	})
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, service.connectionTokenKey)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (service *RealtimeService) signCapability(claims any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, service.capabilityKey)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (service *RealtimeService) verifyCapability(token string, output any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 4096 {
		return ErrRecoveryCursorRejected
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrRecoveryCursorRejected
	}
	mac := hmac.New(sha256.New, service.capabilityKey)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return ErrRecoveryCursorRejected
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrRecoveryCursorRejected
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil || ensureJSONEOF(decoder) != nil {
		return ErrRecoveryCursorRejected
	}
	return nil
}

func randomOpaqueID() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func normalizeRealtimeRepositoryError(err error) error {
	switch {
	case errors.Is(err, ErrSubscriptionNotFound):
		return ErrSubscriptionNotFound
	case errors.Is(err, ErrSubscriptionConflict):
		return ErrSubscriptionConflict
	case errors.Is(err, ErrRecoveryCursorRejected):
		return ErrRecoveryCursorRejected
	default:
		return ErrRealtimeUnavailable
	}
}

func policyRevisionOrdinal(value string) int64 {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r < '0' || r > '9' })
	if len(parts) == 0 {
		return 1
	}
	parsed, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil || parsed < 1 {
		return 1
	}
	return parsed
}
