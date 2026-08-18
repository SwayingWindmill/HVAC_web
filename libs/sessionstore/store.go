package sessionstore

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/sessionevent"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionRevoked  = errors.New("session revoked")
	ErrSessionConflict = errors.New("session conflict")
)

type FailurePoint string

const (
	FailureAfterStateWrite  FailurePoint = "after_state_write"
	FailureAfterAuditIntent FailurePoint = "after_audit_intent"
	FailureBeforeCommit     FailurePoint = "before_commit"
)

type FailureInjector func(FailurePoint) error

type IDGenerator func() string

type Session struct {
	ID                       string
	Principal                identitycontext.UserPrincipal
	TenantID                 string
	CSRFTokenCiphertext      []byte
	ProviderTokensCiphertext []byte
	AuthenticationACR        string
	AuthenticationAMR        []string
	AuthenticationTime       time.Time
	ExpiresAt                time.Time
	LastActivityAt           time.Time
	RevokedAt                *time.Time
	AggregateVersion         uint64
	LastAuditMessageID       string
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type MutationContext struct {
	Action            string
	Result            string
	PolicyRevision    string
	CorrelationID     string
	CausationID       string
	TraceID           string
	Traceparent       string
	ExecutingService  string
	ExecutingSPIFFEID string
	OccurredAt        time.Time
}

type Store interface {
	CreateSession(context.Context, Session, MutationContext) (Session, error)
	GetSession(context.Context, string) (Session, error)
	TouchSession(context.Context, string, time.Time) (Session, error)
	RevokeSession(context.Context, string, MutationContext) (Session, error)
}

type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
	events   map[string]sessionevent.SessionAuditEventV1
	ids      IDGenerator
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: map[string]Session{},
		events:   map[string]sessionevent.SessionAuditEventV1{},
		ids:      randomID,
	}
}

func (store *MemoryStore) CreateSession(ctx context.Context, session Session, mutation MutationContext) (Session, error) {
	_, span := observability.Start(ctx, "sessionstore.memory.create", observability.SpanKindInternal, map[string]any{"db.system": "memory", "db.operation": "session.create"})
	defer span.End()
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.sessions[session.ID]; exists {
		return Session{}, ErrSessionConflict
	}
	messageID := store.ids()
	session.AggregateVersion = 1
	session.LastAuditMessageID = messageID
	session.CreatedAt = mutation.OccurredAt
	session.UpdatedAt = mutation.OccurredAt
	if session.LastActivityAt.IsZero() {
		session.LastActivityAt = mutation.OccurredAt
	}
	event, _, err := buildEvent(session, mutation, messageID, "ACTIVE")
	if err != nil {
		return Session{}, err
	}
	store.sessions[session.ID] = cloneSession(session)
	store.events[messageID] = event
	return cloneSession(session), nil
}

func (store *MemoryStore) GetSession(ctx context.Context, sessionID string) (Session, error) {
	_, span := observability.Start(ctx, "sessionstore.memory.get", observability.SpanKindInternal, map[string]any{"db.system": "memory", "db.operation": "session.get"})
	defer span.End()
	store.mu.RLock()
	defer store.mu.RUnlock()
	session, exists := store.sessions[sessionID]
	if !exists {
		return Session{}, ErrSessionNotFound
	}
	return cloneSession(session), nil
}

func (store *MemoryStore) TouchSession(ctx context.Context, sessionID string, at time.Time) (Session, error) {
	_, span := observability.Start(ctx, "sessionstore.memory.touch", observability.SpanKindInternal, map[string]any{"db.system": "memory", "db.operation": "session.touch"})
	defer span.End()
	store.mu.Lock()
	defer store.mu.Unlock()
	session, exists := store.sessions[sessionID]
	if !exists {
		return Session{}, ErrSessionNotFound
	}
	if session.RevokedAt != nil {
		return Session{}, ErrSessionRevoked
	}
	session.LastActivityAt = at.UTC()
	store.sessions[sessionID] = cloneSession(session)
	return cloneSession(session), nil
}

func (store *MemoryStore) RevokeSession(ctx context.Context, sessionID string, mutation MutationContext) (Session, error) {
	_, span := observability.Start(ctx, "sessionstore.memory.revoke", observability.SpanKindInternal, map[string]any{"db.system": "memory", "db.operation": "session.revoke"})
	defer span.End()
	store.mu.Lock()
	defer store.mu.Unlock()
	session, exists := store.sessions[sessionID]
	if !exists {
		return Session{}, ErrSessionNotFound
	}
	if session.RevokedAt != nil {
		return Session{}, ErrSessionRevoked
	}
	messageID := store.ids()
	revokedAt := mutation.OccurredAt
	if mutation.CausationID == "" {
		mutation.CausationID = session.LastAuditMessageID
	}
	session.RevokedAt = &revokedAt
	session.AggregateVersion++
	session.LastAuditMessageID = messageID
	session.UpdatedAt = revokedAt
	event, _, err := buildEvent(session, mutation, messageID, "REVOKED")
	if err != nil {
		return Session{}, err
	}
	store.sessions[sessionID] = cloneSession(session)
	store.events[messageID] = event
	return cloneSession(session), nil
}

func (store *MemoryStore) Event(messageID string) (sessionevent.SessionAuditEventV1, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	event, exists := store.events[messageID]
	return event, exists
}

func buildEvent(session Session, mutation MutationContext, messageID, state string) (sessionevent.SessionAuditEventV1, []byte, error) {
	if mutation.Result == "" {
		mutation.Result = "SUCCEEDED"
	}
	auditAggregateID := sessionevent.AuditAggregateID(session.ID)
	event := sessionevent.SessionAuditEventV1{
		MessageID:         messageID,
		SchemaVersion:     sessionevent.SchemaVersion,
		MessageType:       sessionevent.MessageType,
		Producer:          sessionevent.Producer,
		TenantID:          session.TenantID,
		PartitionKey:      sessionevent.AggregateType + ":" + auditAggregateID,
		AggregateType:     sessionevent.AggregateType,
		AggregateID:       auditAggregateID,
		AggregateVersion:  session.AggregateVersion,
		OccurredAtUnixMS:  mutation.OccurredAt.UTC().UnixMilli(),
		PublishedAtUnixMS: mutation.OccurredAt.UTC().UnixMilli(),
		CorrelationID:     mutation.CorrelationID,
		CausationID:       mutation.CausationID,
		TraceID:           mutation.TraceID,
		Traceparent:       mutation.Traceparent,
		Actor: sessionevent.ActorChainV1{
			InitiatingSubject: session.Principal.Subject,
			InitiatingIssuer:  session.Principal.Issuer,
			ExecutingService:  mutation.ExecutingService,
			ExecutingSPIFFEID: mutation.ExecutingSPIFFEID,
			TenantID:          session.TenantID,
		},
		Action:         mutation.Action,
		Result:         mutation.Result,
		PolicyRevision: mutation.PolicyRevision,
		PayloadSHA256:  sessionevent.SafePayloadHash(session.ID, state, mutation.OccurredAt.UTC().UnixMilli()),
		SessionState:   state,
	}
	payload, err := event.MarshalBinary()
	return event, payload, err
}

func cloneSession(session Session) Session {
	session.Principal.Roles = append([]string(nil), session.Principal.Roles...)
	session.CSRFTokenCiphertext = append([]byte(nil), session.CSRFTokenCiphertext...)
	session.ProviderTokensCiphertext = append([]byte(nil), session.ProviderTokensCiphertext...)
	session.AuthenticationAMR = append([]string(nil), session.AuthenticationAMR...)
	if session.RevokedAt != nil {
		value := *session.RevokedAt
		session.RevokedAt = &value
	}
	return session
}

func randomID() string {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
