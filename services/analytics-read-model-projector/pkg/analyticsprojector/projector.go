package analyticsprojector

import (
	"net/http"

	clickhouseclient "github.com/quanlaihe/hvac-web/services/analytics-read-model-projector/internal/clickhouse"
	"github.com/quanlaihe/hvac-web/services/analytics-read-model-projector/internal/energy"
)

const EnergyTypeElectricity = energy.EnergyTypeElectricity

type ReaderConfig = clickhouseclient.ReaderConfig
type WriterConfig = clickhouseclient.WriterConfig
type ProjectorConfig = energy.ProjectorConfig
type Reader = clickhouseclient.Reader
type Writer = clickhouseclient.Writer
type Projector = energy.Projector

func NewReader(config ReaderConfig) (*Reader, error) {
	return clickhouseclient.NewReader(config)
}

func NewWriter(config WriterConfig) (*Writer, error) {
	return clickhouseclient.NewWriter(config)
}

func NewProjector(config ProjectorConfig) (*Projector, error) {
	return energy.NewProjector(config)
}

func DefaultHTTPClient() *http.Client {
	return &http.Client{}
}
