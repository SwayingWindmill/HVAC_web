package auditserver

import (
	"context"
	"net/http"

	"github.com/quanlaihe/hvac-web/modules/audit/internal/audit"
)

const (
	DomainEventSchemaVersion = audit.DomainEventSchemaVersion
	SessionAuditPathPrefix   = audit.SessionAuditPathPrefix
	DomainAuditPath          = audit.DomainAuditPath

	AggregateTypeRemoteCommand     = audit.AggregateTypeRemoteCommand
	AggregateTypeControlLock       = audit.AggregateTypeControlLock
	AggregateTypeOperatingSchedule = audit.AggregateTypeOperatingSchedule
	AggregateTypeRegistryMutation  = audit.AggregateTypeRegistryMutation
	AggregateTypeIAMAdmin          = audit.AggregateTypeIAMAdmin
	AggregateTypeGatewayCredential = audit.AggregateTypeGatewayCredential
	CategoryControlSchedule        = audit.CategoryControlSchedule

	ClientKindBrowser     = audit.ClientKindBrowser
	ClientKindMiniProgram = audit.ClientKindMiniProgram
	ClientKindSystem      = audit.ClientKindSystem
)

type Config = audit.ServerConfig
type Record = audit.Record
type RecordReader = audit.RecordReader
type DomainEvent = audit.DomainEvent
type DomainEventWriter = audit.DomainEventWriter
type SearchFilter = audit.SearchFilter
type SearchRecord = audit.SearchRecord
type Store = audit.Store
type MessageMetadata = audit.MessageMetadata

var ErrRecordNotFound = audit.ErrRecordNotFound

func NewHandler(config Config) http.Handler {
	return audit.NewHandler(config)
}

func OpenStore(ctx context.Context, consumerDSN, queryDSN string) (*Store, error) {
	return audit.OpenStore(ctx, consumerDSN, queryDSN)
}
