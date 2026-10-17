package clickhouse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"

	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

type pricedSiteRow struct {
	TenantID string `json:"tenant_id"`
	SiteID   string `json:"site_id"`
}

func (reader *Reader) PricedSites(ctx context.Context, since time.Time) ([]energy.PricedSite, error) {
	payload, err := reader.query(ctx, "priced Site query", fmt.Sprintf(`SELECT toString(tenant_id) AS tenant_id, toString(site_id) AS site_id
FROM %[1]s.%[2]s
WHERE period_end >= {since:DateTime64(3, 'UTC')} AND energy_type = 'electricity' AND accounting_scope = 'SITE'
UNION DISTINCT
SELECT toString(tenant_id) AS tenant_id, toString(site_id) AS site_id
FROM %[1]s.tariff_price_window_syncs
ORDER BY tenant_id, site_id
FORMAT JSONEachRow`, reader.analyticsDatabase, reader.analyticsTable), url.Values{"param_since": {clickHouseTime(since)}})
	if err != nil {
		return nil, err
	}
	var sites []energy.PricedSite
	decoder := json.NewDecoder(bytes.NewReader(payload))
	for {
		var row pricedSiteRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			return sites, nil
		} else if err != nil {
			return nil, fmt.Errorf("decode priced Site: %w", err)
		}
		sites = append(sites, energy.PricedSite{TenantID: row.TenantID, SiteID: row.SiteID})
	}
}

type priceWindowSyncRow struct {
	Generation     string `json:"generation"`
	TimelineDigest string `json:"timeline_digest"`
	CoveredTo      string `json:"covered_to"`
}

func (reader *Reader) PriceWindowSync(ctx context.Context, site energy.PricedSite) (energy.PriceWindowSync, bool, error) {
	payload, err := reader.query(ctx, "price window sync query", fmt.Sprintf(`SELECT toString(generation) AS generation, timeline_digest, toString(covered_to) AS covered_to
FROM %s.tariff_price_window_syncs
WHERE tenant_id = {tenant:UUID} AND site_id = {site:UUID}
ORDER BY generation DESC, covered_to DESC
LIMIT 1
FORMAT JSONEachRow`, reader.analyticsDatabase), url.Values{"param_tenant": {site.TenantID}, "param_site": {site.SiteID}})
	if err != nil {
		return energy.PriceWindowSync{}, false, err
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return energy.PriceWindowSync{}, false, nil
	}
	var row priceWindowSyncRow
	if err := json.Unmarshal(payload, &row); err != nil {
		return energy.PriceWindowSync{}, false, fmt.Errorf("decode price window sync: %w", err)
	}
	generation, err := strconv.ParseUint(row.Generation, 10, 64)
	if err != nil {
		return energy.PriceWindowSync{}, false, fmt.Errorf("decode price window generation: %w", err)
	}
	coveredTo, err := parseClickHouseTime(row.CoveredTo)
	if err != nil {
		return energy.PriceWindowSync{}, false, fmt.Errorf("decode price window coverage: %w", err)
	}
	return energy.PriceWindowSync{
		TenantID: site.TenantID, SiteID: site.SiteID, Generation: generation, TimelineDigest: row.TimelineDigest, CoveredTo: coveredTo,
	}, true, nil
}

func (writer *Writer) WritePriceWindows(ctx context.Context, sync energy.PriceWindowSync, windows []tariffmodel.Window) error {
	if len(windows) == 0 {
		return nil
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, window := range windows {
		if err := encoder.Encode(map[string]any{
			"tenant_id": sync.TenantID, "site_id": sync.SiteID, "generation": sync.Generation,
			"window_start": clickHouseTime(window.Start), "window_end": clickHouseTime(window.End),
			"period_code": window.PeriodCode, "energy_rate_per_kwh": window.EnergyRatePerKWh, "tariff_version_id": window.VersionID,
		}); err != nil {
			return fmt.Errorf("encode price window: %w", err)
		}
	}
	return writer.insert(ctx, "price window insert", url.Values{
		"query":             {"INSERT INTO " + writer.database + ".tariff_price_windows (tenant_id, site_id, generation, window_start, window_end, period_code, energy_rate_per_kwh, tariff_version_id) FORMAT JSONEachRow"},
		"wait_end_of_query": {"1"},
	}, body.Bytes())
}

func (writer *Writer) RecordPriceWindowSync(ctx context.Context, sync energy.PriceWindowSync) error {
	body, err := json.Marshal(map[string]any{
		"tenant_id": sync.TenantID, "site_id": sync.SiteID, "generation": sync.Generation,
		"timeline_digest": sync.TimelineDigest, "covered_to": clickHouseTime(sync.CoveredTo),
	})
	if err != nil {
		return fmt.Errorf("encode price window sync: %w", err)
	}
	return writer.insert(ctx, "price window sync insert", url.Values{
		"query":             {"INSERT INTO " + writer.database + ".tariff_price_window_syncs (tenant_id, site_id, generation, timeline_digest, covered_to) FORMAT JSONEachRow"},
		"wait_end_of_query": {"1"},
	}, append(body, '\n'))
}

// DropPriceWindowsBefore deletes the generations the sync replaced; they are no longer visible.
func (writer *Writer) DropPriceWindowsBefore(ctx context.Context, sync energy.PriceWindowSync) error {
	return writer.insert(ctx, "price window delete", url.Values{
		"query":        {"ALTER TABLE " + writer.database + ".tariff_price_windows DELETE WHERE tenant_id = {tenant:UUID} AND site_id = {site:UUID} AND generation < {generation:UInt64}"},
		"param_tenant": {sync.TenantID}, "param_site": {sync.SiteID}, "param_generation": {strconv.FormatUint(sync.Generation, 10)},
	}, nil)
}

var (
	_ energy.PriceWindowReader = (*Reader)(nil)
	_ energy.PriceWindowWriter = (*Writer)(nil)
)
