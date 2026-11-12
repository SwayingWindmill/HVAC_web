package energy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TotalSubMeter is one branch electricity meter whose Counter Point contributes to a Site's
// synthesized total electricity.
type TotalSubMeter struct {
	PointID   string `json:"pointId"`
	DeviceID  string `json:"deviceId"`
	MeterName string `json:"meterName"`
	Ordinal   int    `json:"ordinal"`
}

// SiteTotalElectricitySources describes whether a Site synthesizes its total electricity from
// branch sub-meters and, if so, which sub-meters participate.
type SiteTotalElectricitySources struct {
	Synthesized bool            `json:"synthesized"`
	SubMeters   []TotalSubMeter `json:"subMeters"`
}

// TotalSubmeterSync is the visible generation of a Site's synthesized total sub-meter list.
type TotalSubmeterSync struct {
	PricedSite
	Generation    uint64
	Synthesized   bool
	SourcesDigest string
}

type TotalSubmeterReader interface {
	SubmeterSites(ctx context.Context, since time.Time) ([]PricedSite, error)
	TotalSubmeterSync(ctx context.Context, site PricedSite) (TotalSubmeterSync, bool, error)
}

type TotalSubmeterWriter interface {
	WriteTotalSubmeters(ctx context.Context, sync TotalSubmeterSync, subMeters []TotalSubMeter) error
	RecordTotalSubmeterSync(ctx context.Context, sync TotalSubmeterSync) error
	DropTotalSubmetersBefore(ctx context.Context, sync TotalSubmeterSync) error
}

// TotalSubmeterSource reads a Site's synthesized total electricity sub-meters from Registry.
type TotalSubmeterSource interface {
	TotalElectricitySources(ctx context.Context, tenantID, siteID string) (SiteTotalElectricitySources, error)
}

type TotalSubmeterProjectorConfig struct {
	Reader  TotalSubmeterReader
	Writer  TotalSubmeterWriter
	Sources TotalSubmeterSource
	Now     func() time.Time
}

// TotalSubmeterProjector keeps every electricity Site's synthesized total sub-meter list synced to
// ClickHouse so Cube can sum branch facts and detect incomplete hours without reading PostgreSQL.
type TotalSubmeterProjector struct {
	reader  TotalSubmeterReader
	writer  TotalSubmeterWriter
	sources TotalSubmeterSource
	now     func() time.Time
}

func NewTotalSubmeterProjector(config TotalSubmeterProjectorConfig) (*TotalSubmeterProjector, error) {
	if config.Reader == nil || config.Writer == nil || config.Sources == nil {
		return nil, errors.New("total submeter projector requires a reader, a writer and a Registry source")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &TotalSubmeterProjector{
		reader:  config.Reader,
		writer:  config.Writer,
		sources: config.Sources,
		now:     config.Now,
	}, nil
}

func (projector *TotalSubmeterProjector) SyncOnce(ctx context.Context) error {
	now := projector.now().UTC()
	sites, err := projector.reader.SubmeterSites(ctx, now.Add(-pricedSiteLookback))
	if err != nil {
		return fmt.Errorf("list Sites for total submeter sync: %w", err)
	}
	var failures []error
	for _, site := range sites {
		if err := projector.syncSite(ctx, site, now); err != nil {
			failures = append(failures, fmt.Errorf("total submeters of Site %s: %w", site.SiteID, err))
		}
	}
	return errors.Join(failures...)
}

func (projector *TotalSubmeterProjector) syncSite(ctx context.Context, site PricedSite, now time.Time) error {
	sources, err := projector.sources.TotalElectricitySources(ctx, site.TenantID, site.SiteID)
	if err != nil {
		return err
	}
	if !sources.Synthesized || len(sources.SubMeters) == 0 {
		sources = SiteTotalElectricitySources{Synthesized: false, SubMeters: []TotalSubMeter{}}
	}
	digest, err := totalSubmeterDigest(sources)
	if err != nil {
		return err
	}
	previous, found, err := projector.reader.TotalSubmeterSync(ctx, site)
	if err != nil {
		return err
	}
	if found && previous.Synthesized == sources.Synthesized && previous.SourcesDigest == digest {
		return nil
	}
	next := TotalSubmeterSync{
		PricedSite:    site,
		Generation:    max(previous.Generation+1, uint64(now.UnixMilli())),
		Synthesized:   sources.Synthesized,
		SourcesDigest: digest,
	}
	if len(sources.SubMeters) > 0 {
		if err := projector.writer.WriteTotalSubmeters(ctx, next, sources.SubMeters); err != nil {
			return err
		}
	}
	if err := projector.writer.RecordTotalSubmeterSync(ctx, next); err != nil {
		return err
	}
	return projector.writer.DropTotalSubmetersBefore(ctx, next)
}

func totalSubmeterDigest(sources SiteTotalElectricitySources) (string, error) {
	payload, err := json.Marshal(sources)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
