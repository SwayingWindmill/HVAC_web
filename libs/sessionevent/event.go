package sessionevent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

const (
	SchemaVersion uint32 = 1
	MessageType          = "hvac.security.session.v1"
	Producer             = "platform-gateway"
	AggregateType        = "bff-session"
	ControlTopic         = "control.security.session.v1"
)

// ActorChainV1 keeps the initiating user and executing workload distinct.
type ActorChainV1 struct {
	InitiatingSubject    string
	InitiatingIssuer     string
	ExecutingService     string
	ExecutingSPIFFEID    string
	ActingOrganizationID string
}

// SessionAuditEventV1 is the repository-owned representation of
// contracts/events/session-audit.v1.proto. MarshalBinary emits deterministic
// protobuf wire bytes using the field numbers fixed by that contract.
type SessionAuditEventV1 struct {
	MessageID         string
	SchemaVersion     uint32
	MessageType       string
	Producer          string
	OrganizationID    string
	PartitionKey      string
	AggregateType     string
	AggregateID       string
	AggregateVersion  uint64
	OccurredAtUnixMS  int64
	PublishedAtUnixMS int64
	CorrelationID     string
	CausationID       string
	TraceID           string
	Traceparent       string
	Actor             ActorChainV1
	Action            string
	Result            string
	PolicyRevision    string
	PayloadSHA256     string
	SessionState      string
}

func (event SessionAuditEventV1) Validate() error {
	if event.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", event.SchemaVersion)
	}
	if event.MessageType != MessageType || event.Producer != Producer || event.AggregateType != AggregateType {
		return errors.New("event identity fields are invalid")
	}
	if event.MessageID == "" || event.OrganizationID == "" || event.PartitionKey == "" || event.AggregateID == "" || event.AggregateVersion == 0 {
		return errors.New("event aggregate fields are incomplete")
	}
	if len(event.AggregateID) != sha256.Size*2 || strings.ToLower(event.AggregateID) != event.AggregateID {
		return errors.New("event aggregate id must be lowercase sha256 hex")
	}
	if _, err := hex.DecodeString(event.AggregateID); err != nil {
		return errors.New("event aggregate id must be lowercase sha256 hex")
	}
	if event.PartitionKey != AggregateType+":"+event.AggregateID {
		return errors.New("event partition key does not match the redacted aggregate id")
	}
	if event.OccurredAtUnixMS <= 0 || event.PublishedAtUnixMS <= 0 || event.PublishedAtUnixMS < event.OccurredAtUnixMS {
		return errors.New("event timestamps are invalid")
	}
	if event.Actor.InitiatingSubject == "" || event.Actor.InitiatingIssuer == "" || event.Actor.ExecutingService == "" || event.Actor.ExecutingSPIFFEID == "" || event.Actor.ActingOrganizationID != event.OrganizationID {
		return errors.New("event actor chain is invalid")
	}
	if event.Action == "" || event.Result == "" || event.PolicyRevision == "" || event.PayloadSHA256 == "" || event.SessionState == "" {
		return errors.New("event audit fields are incomplete")
	}
	if !validTraceparent(event.Traceparent, event.TraceID) {
		return errors.New("event traceparent is invalid")
	}
	if _, err := hex.DecodeString(event.PayloadSHA256); err != nil || len(event.PayloadSHA256) != sha256.Size*2 {
		return errors.New("payload hash must be lowercase sha256 hex")
	}
	for _, value := range []string{
		event.MessageID, event.OrganizationID, event.PartitionKey, event.AggregateID,
		event.CorrelationID, event.CausationID, event.TraceID, event.Traceparent, event.Action,
		event.Result, event.PolicyRevision, event.SessionState,
		event.Actor.InitiatingSubject, event.Actor.InitiatingIssuer,
		event.Actor.ExecutingService, event.Actor.ExecutingSPIFFEID,
	} {
		if containsSensitiveMarker(value) {
			return errors.New("event contains a credential or grant marker")
		}
	}
	return nil
}

func (event SessionAuditEventV1) MarshalBinary() ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	var output []byte
	output = appendString(output, 1, event.MessageID)
	output = appendVarint(output, 2, uint64(event.SchemaVersion))
	output = appendString(output, 3, event.MessageType)
	output = appendString(output, 4, event.Producer)
	output = appendString(output, 5, event.OrganizationID)
	output = appendString(output, 6, event.PartitionKey)
	output = appendString(output, 7, event.AggregateType)
	output = appendString(output, 8, event.AggregateID)
	output = appendVarint(output, 9, event.AggregateVersion)
	output = appendVarint(output, 10, uint64(event.OccurredAtUnixMS))
	output = appendVarint(output, 11, uint64(event.PublishedAtUnixMS))
	output = appendString(output, 12, event.CorrelationID)
	output = appendString(output, 13, event.CausationID)
	output = appendString(output, 14, event.TraceID)
	actor := marshalActor(event.Actor)
	output = protowire.AppendTag(output, 15, protowire.BytesType)
	output = protowire.AppendBytes(output, actor)
	output = appendString(output, 16, event.Action)
	output = appendString(output, 17, event.Result)
	output = appendString(output, 18, event.PolicyRevision)
	output = appendString(output, 19, event.PayloadSHA256)
	output = appendString(output, 20, event.SessionState)
	output = appendString(output, 21, event.Traceparent)
	return output, nil
}

func UnmarshalBinary(input []byte) (SessionAuditEventV1, error) {
	var event SessionAuditEventV1
	for len(input) > 0 {
		number, wireType, consumed := protowire.ConsumeTag(input)
		if consumed < 0 {
			return SessionAuditEventV1{}, protowire.ParseError(consumed)
		}
		input = input[consumed:]
		switch number {
		case 1, 3, 4, 5, 6, 7, 8, 12, 13, 14, 16, 17, 18, 19, 20, 21:
			if wireType != protowire.BytesType {
				return SessionAuditEventV1{}, fmt.Errorf("field %d has wrong wire type", number)
			}
			value, size := protowire.ConsumeString(input)
			if size < 0 {
				return SessionAuditEventV1{}, protowire.ParseError(size)
			}
			assignString(&event, number, value)
			input = input[size:]
		case 2, 9, 10, 11:
			if wireType != protowire.VarintType {
				return SessionAuditEventV1{}, fmt.Errorf("field %d has wrong wire type", number)
			}
			value, size := protowire.ConsumeVarint(input)
			if size < 0 {
				return SessionAuditEventV1{}, protowire.ParseError(size)
			}
			switch number {
			case 2:
				event.SchemaVersion = uint32(value)
			case 9:
				event.AggregateVersion = value
			case 10:
				event.OccurredAtUnixMS = int64(value)
			case 11:
				event.PublishedAtUnixMS = int64(value)
			}
			input = input[size:]
		case 15:
			if wireType != protowire.BytesType {
				return SessionAuditEventV1{}, errors.New("actor has wrong wire type")
			}
			value, size := protowire.ConsumeBytes(input)
			if size < 0 {
				return SessionAuditEventV1{}, protowire.ParseError(size)
			}
			actor, err := unmarshalActor(value)
			if err != nil {
				return SessionAuditEventV1{}, err
			}
			event.Actor = actor
			input = input[size:]
		default:
			size := protowire.ConsumeFieldValue(number, wireType, input)
			if size < 0 {
				return SessionAuditEventV1{}, protowire.ParseError(size)
			}
			input = input[size:]
		}
	}
	if err := event.Validate(); err != nil {
		return SessionAuditEventV1{}, err
	}
	return event, nil
}

func AuditAggregateID(sessionID string) string {
	digest := sha256.Sum256([]byte("hvac:bff-session:audit-aggregate:v1\x00" + sessionID))
	return hex.EncodeToString(digest[:])
}

func SafePayloadHash(sessionID, state string, transitionUnixMS int64) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("session_id=%s\nstate=%s\ntransition_unix_ms=%d\n", sessionID, state, transitionUnixMS)))
	return hex.EncodeToString(digest[:])
}

func appendString(output []byte, number protowire.Number, value string) []byte {
	output = protowire.AppendTag(output, number, protowire.BytesType)
	return protowire.AppendString(output, value)
}

func appendVarint(output []byte, number protowire.Number, value uint64) []byte {
	output = protowire.AppendTag(output, number, protowire.VarintType)
	return protowire.AppendVarint(output, value)
}

func marshalActor(actor ActorChainV1) []byte {
	var output []byte
	output = appendString(output, 1, actor.InitiatingSubject)
	output = appendString(output, 2, actor.InitiatingIssuer)
	output = appendString(output, 3, actor.ExecutingService)
	output = appendString(output, 4, actor.ExecutingSPIFFEID)
	output = appendString(output, 5, actor.ActingOrganizationID)
	return output
}

func unmarshalActor(input []byte) (ActorChainV1, error) {
	var actor ActorChainV1
	for len(input) > 0 {
		number, wireType, consumed := protowire.ConsumeTag(input)
		if consumed < 0 {
			return ActorChainV1{}, protowire.ParseError(consumed)
		}
		input = input[consumed:]
		if wireType != protowire.BytesType {
			size := protowire.ConsumeFieldValue(number, wireType, input)
			if size < 0 {
				return ActorChainV1{}, protowire.ParseError(size)
			}
			input = input[size:]
			continue
		}
		value, size := protowire.ConsumeString(input)
		if size < 0 {
			return ActorChainV1{}, protowire.ParseError(size)
		}
		switch number {
		case 1:
			actor.InitiatingSubject = value
		case 2:
			actor.InitiatingIssuer = value
		case 3:
			actor.ExecutingService = value
		case 4:
			actor.ExecutingSPIFFEID = value
		case 5:
			actor.ActingOrganizationID = value
		}
		input = input[size:]
	}
	return actor, nil
}

func assignString(event *SessionAuditEventV1, number protowire.Number, value string) {
	switch number {
	case 1:
		event.MessageID = value
	case 3:
		event.MessageType = value
	case 4:
		event.Producer = value
	case 5:
		event.OrganizationID = value
	case 6:
		event.PartitionKey = value
	case 7:
		event.AggregateType = value
	case 8:
		event.AggregateID = value
	case 12:
		event.CorrelationID = value
	case 13:
		event.CausationID = value
	case 14:
		event.TraceID = value
	case 16:
		event.Action = value
	case 17:
		event.Result = value
	case 18:
		event.PolicyRevision = value
	case 19:
		event.PayloadSHA256 = value
	case 20:
		event.SessionState = value
	case 21:
		event.Traceparent = value
	}
}

func validTraceparent(value, traceID string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 4 || parts[0] != "00" || parts[1] != traceID || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return false
	}
	if parts[1] == strings.Repeat("0", 32) || parts[2] == strings.Repeat("0", 16) {
		return false
	}
	for _, part := range parts[1:] {
		if _, err := hex.DecodeString(part); err != nil || strings.ToLower(part) != part {
			return false
		}
	}
	return true
}

func containsSensitiveMarker(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"bearer ", "access_token", "refresh_token", "id_token", "authorization_code", "cookie", "delegation_grant", "x-delegation-grant"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
