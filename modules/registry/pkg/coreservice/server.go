package coreservice

import (
	"context"
	"net/http"

	"github.com/quanlaihe/hvac-web/modules/registry/internal/core"
)

const RegistryPathPrefix = core.RegistryPathPrefix

type ServerConfig = core.ServerConfig
type PostgresStore = core.PostgresStore
type CursorCodec = core.CursorCodec
type OutboxRelayConfig = core.OutboxRelayConfig
type OutboxRelay = core.OutboxRelay

func NewHandler(config ServerConfig) http.Handler {
	return core.NewHandler(config)
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	return core.OpenPostgresStore(ctx, databaseURL)
}

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	return core.NewCursorCodec(key)
}

func NewOutboxRelay(store *PostgresStore, config OutboxRelayConfig) (*OutboxRelay, error) {
	return core.NewOutboxRelay(store, config)
}
