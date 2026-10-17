package analyticsprojector

import (
	clickhouseclient "github.com/quanlaihe/hvac-web/modules/energy/internal/clickhouse"
	coreclient "github.com/quanlaihe/hvac-web/modules/energy/internal/coreclient"
	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

type ReaderConfig = clickhouseclient.ReaderConfig
type WriterConfig = clickhouseclient.WriterConfig
type ProjectorConfig = energy.ProjectorConfig
type RegistryClientConfig = coreclient.Config
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

func NewBindingResolver(config RegistryClientConfig) (BindingResolver, error) {
	return coreclient.NewResolver(config)
}

type PriceWindowProjectorConfig = energy.PriceWindowProjectorConfig
type PriceWindowProjector = energy.PriceWindowProjector
type TariffSource = energy.TariffSource

func NewPriceWindowProjector(config PriceWindowProjectorConfig) (*PriceWindowProjector, error) {
	return energy.NewPriceWindowProjector(config)
}

// NewTariffSource reads import Tariffs from Core Registry with its own grants, so it can run
// beside the binding resolver.
func NewTariffSource(config RegistryClientConfig) (TariffSource, error) {
	return coreclient.NewResolver(config)
}
