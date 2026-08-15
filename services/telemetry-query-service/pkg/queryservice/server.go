package queryservice

import (
	"net/http"
	"time"

	"github.com/quanlaihe/hvac-web/services/telemetry-query-service/internal/cube"
	"github.com/quanlaihe/hvac-web/services/telemetry-query-service/internal/history"
	"github.com/quanlaihe/hvac-web/services/telemetry-query-service/internal/query"
)

type ServerConfig = query.ServerConfig
type CubeConfig = cube.Config
type HistoryConfig = history.Config
type CubeAccessFactory = cube.HMACTokenFactory
type CubeClient = cube.Client
type HistoryClient = history.Client

func NewHandler(config ServerConfig) http.Handler {
	return query.NewHandler(config)
}

func NewCubeAccessFactory(material []byte, now func() time.Time) (*CubeAccessFactory, error) {
	return cube.NewHMACTokenFactory(material, now)
}

func NewCubeClient(config CubeConfig) (*CubeClient, error) {
	return cube.NewClient(config)
}

func NewHistoryClient(config HistoryConfig) (*HistoryClient, error) {
	return history.NewClient(config)
}
