package auditserver

import (
	"net/http"

	"github.com/quanlaihe/hvac-web/services/audit-ledger-service/internal/audit"
)

const SessionAuditPathPrefix = audit.SessionAuditPathPrefix

type Config = audit.ServerConfig
type Record = audit.Record
type RecordReader = audit.RecordReader

var ErrRecordNotFound = audit.ErrRecordNotFound

func NewHandler(config Config) http.Handler {
	return audit.NewHandler(config)
}
