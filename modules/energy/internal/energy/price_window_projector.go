package energy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
)

// The price windows reach this far past now, so today and the next days are priced as their
// energy arrives, whatever the Site's timezone.
const (
	priceWindowHorizonDays = 4
	pricedSiteLookback     = 48 * time.Hour
)

// PricedSite is a Site whose electricity Cube prices.
type PricedSite struct {
	TenantID string
	SiteID   string
}

// PriceWindowSync is how far a Site's current generation of price windows reaches and which
// Tariff timeline it was laid out from.
type PriceWindowSync struct {
	PricedSite
	Generation     uint64
	TimelineDigest string
	CoveredTo      time.Time
}

type PriceWindowReader interface {
	// PricedSites are the Sites with Site-total electricity facts ending since then, and the Sites
	// already synced.
	PricedSites(ctx context.Context, since time.Time) ([]PricedSite, error)
	PriceWindowSync(ctx context.Context, site PricedSite) (PriceWindowSync, bool, error)
}

type PriceWindowWriter interface {
	WritePriceWindows(ctx context.Context, sync PriceWindowSync, windows []tariffmodel.Window) error
	// RecordPriceWindowSync makes the sync's generation the visible one, so it is written last.
	RecordPriceWindowSync(ctx context.Context, sync PriceWindowSync) error
	DropPriceWindowsBefore(ctx context.Context, sync PriceWindowSync) error
}

// TariffSource reads a Site's import Tariff Versions in effect before an instant from Registry.
type TariffSource interface {
	ImportTariffs(ctx context.Context, tenantID, siteID string, before time.Time) ([]tariffmodel.Version, error)
}

type PriceWindowProjectorConfig struct {
	Reader  PriceWindowReader
	Writer  PriceWindowWriter
	Tariffs TariffSource
	Now     func() time.Time
}

// PriceWindowProjector keeps every priced Site's Tariff laid out as price windows (#575).
type PriceWindowProjector struct {
	reader  PriceWindowReader
	writer  PriceWindowWriter
	tariffs TariffSource
	now     func() time.Time
}

func NewPriceWindowProjector(config PriceWindowProjectorConfig) (*PriceWindowProjector, error) {
	if config.Reader == nil || config.Writer == nil || config.Tariffs == nil {
		return nil, errors.New("price window projector requires a reader, a writer and a Tariff source")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &PriceWindowProjector{reader: config.Reader, writer: config.Writer, tariffs: config.Tariffs, now: config.Now}, nil
}

// SyncOnce brings every priced Site's windows up to the horizon. A Site that fails keeps its
// windows and does not stop the others; the failures are returned together.
func (projector *PriceWindowProjector) SyncOnce(ctx context.Context) error {
	now := projector.now().UTC()
	sites, err := projector.reader.PricedSites(ctx, now.Add(-pricedSiteLookback))
	if err != nil {
		return fmt.Errorf("list priced Sites: %w", err)
	}
	horizon := now.Truncate(24*time.Hour).AddDate(0, 0, priceWindowHorizonDays)
	var failures []error
	for _, site := range sites {
		if err := projector.syncSite(ctx, site, now, horizon); err != nil {
			failures = append(failures, fmt.Errorf("price windows of Site %s: %w", site.SiteID, err))
		}
	}
	return errors.Join(failures...)
}

func (projector *PriceWindowProjector) syncSite(ctx context.Context, site PricedSite, now, horizon time.Time) error {
	versions, err := projector.tariffs.ImportTariffs(ctx, site.TenantID, site.SiteID, horizon)
	if err != nil {
		return err
	}
	digest, err := timelineDigest(versions)
	if err != nil {
		return err
	}
	previous, found, err := projector.reader.PriceWindowSync(ctx, site)
	if err != nil {
		return err
	}
	if found && previous.TimelineDigest == digest {
		if !previous.CoveredTo.Before(horizon) {
			return nil
		}
		extended := previous
		extended.CoveredTo = horizon
		if err := projector.writer.WritePriceWindows(ctx, extended, tariffmodel.Windows(versions, previous.CoveredTo, horizon)); err != nil {
			return err
		}
		return projector.writer.RecordPriceWindowSync(ctx, extended)
	}
	// Each attempt writes under its own generation, so windows of an attempt whose sync row failed
	// never mix with the next timeline's; they stay invisible until a later sync drops them.
	next := PriceWindowSync{PricedSite: site, Generation: max(previous.Generation+1, uint64(now.UnixMilli())), TimelineDigest: digest, CoveredTo: horizon}
	if len(versions) > 0 {
		if err := projector.writer.WritePriceWindows(ctx, next, tariffmodel.Windows(versions, versions[0].EffectiveFrom, horizon)); err != nil {
			return err
		}
	}
	if err := projector.writer.RecordPriceWindowSync(ctx, next); err != nil {
		return err
	}
	return projector.writer.DropPriceWindowsBefore(ctx, next)
}

// timelineDigest identifies a Tariff timeline, so an unchanged one is only extended.
func timelineDigest(versions []tariffmodel.Version) (string, error) {
	type version struct {
		ID            string
		EffectiveFrom time.Time
		EffectiveTo   *time.Time
		Timezone      string
		Periods       []tariffmodel.Period
	}
	timeline := make([]version, len(versions))
	for index, each := range versions {
		timeline[index] = version{ID: each.ID, EffectiveFrom: each.EffectiveFrom.UTC(), EffectiveTo: each.EffectiveTo, Timezone: each.Location.String(), Periods: each.Periods}
	}
	payload, err := json.Marshal(timeline)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
