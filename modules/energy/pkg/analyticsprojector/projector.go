package analyticsprojector

import (
	"net/http"

	clickhouseclient "github.com/quanlaihe/hvac-web/modules/energy/internal/clickhouse"
	coreclient "github.com/quanlaihe/hvac-web/modules/energy/internal/coreclient"
	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

const EnergyTypeElectricity = energy.EnergyTypeElectricity

type ReaderConfig = clickhouseclient.ReaderConfig
type WriterConfig = clickhouseclient.WriterConfig
type ProjectorConfig = energy.ProjectorConfig
type BindingResolverConfig = coreclient.Config
type BindingResolver = energy.BindingResolver
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

func NewBindingResolver(config BindingResolverConfig) (BindingResolver, error) {
	return coreclient.NewResolver(config)
}

func DefaultHTTPClient() *http.Client {
	return &http.Client{}
}
