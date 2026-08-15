package coreservice

import (
	"context"
	"net/http"

	"github.com/quanlaihe/hvac-web/services/platform-core-service/internal/core"
)

const RegistryPathPrefix = core.RegistryPathPrefix

type ServerConfig = core.ServerConfig
type PostgresStore = core.PostgresStore
type CursorCodec = core.CursorCodec
type HTTPGrantStatusProvider = core.HTTPGrantStatusProvider

func NewHandler(config ServerConfig) http.Handler {
	return core.NewHandler(config)
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	return core.OpenPostgresStore(ctx, databaseURL)
}

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	return core.NewCursorCodec(key)
}

func NewHTTPGrantStatusProvider(endpoint string, client *http.Client) (*HTTPGrantStatusProvider, error) {
	return core.NewHTTPGrantStatusProvider(endpoint, client)
}
