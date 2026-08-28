package auditserver

import (
	"context"
	"net/http"

	"github.com/quanlaihe/hvac-web/modules/audit/internal/audit"
)

const SessionAuditPathPrefix = audit.SessionAuditPathPrefix

type Config = audit.ServerConfig
type Record = audit.Record
type RecordReader = audit.RecordReader
type Store = audit.Store
type MessageMetadata = audit.MessageMetadata

var ErrRecordNotFound = audit.ErrRecordNotFound

func NewHandler(config Config) http.Handler {
	return audit.NewHandler(config)
}

func OpenStore(ctx context.Context, consumerDSN, queryDSN string) (*Store, error) {
	return audit.OpenStore(ctx, consumerDSN, queryDSN)
}
