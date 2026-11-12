package clickhouse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

func (reader *Reader) SubmeterSites(ctx context.Context, since time.Time) ([]energy.PricedSite, error) {
	return reader.electricitySites(ctx, "submeter Site", "site_total_submeter_syncs", since)
}

type totalSubmeterSyncRow struct {
	Generation    string `json:"generation"`
	Synthesized   uint8  `json:"synthesized"`
	SourcesDigest string `json:"sources_digest"`
}

func (reader *Reader) TotalSubmeterSync(ctx context.Context, site energy.PricedSite) (energy.TotalSubmeterSync, bool, error) {
	payload, err := reader.query(ctx, "total submeter sync query", fmt.Sprintf(`SELECT toString(generation) AS generation, synthesized, sources_digest
FROM %s.site_total_submeter_syncs
WHERE tenant_id = {tenant:UUID} AND site_id = {site:UUID}
ORDER BY generation DESC
LIMIT 1
FORMAT JSONEachRow`, reader.analyticsDatabase), url.Values{"param_tenant": {site.TenantID}, "param_site": {site.SiteID}})
	if err != nil {
		return energy.TotalSubmeterSync{}, false, err
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return energy.TotalSubmeterSync{}, false, nil
	}
	var row totalSubmeterSyncRow
	if err := json.Unmarshal(payload, &row); err != nil {
		return energy.TotalSubmeterSync{}, false, fmt.Errorf("decode total submeter sync: %w", err)
	}
	generation, err := strconv.ParseUint(row.Generation, 10, 64)
	if err != nil {
		return energy.TotalSubmeterSync{}, false, fmt.Errorf("decode total submeter generation: %w", err)
	}
	return energy.TotalSubmeterSync{
		PricedSite:    site,
		Generation:    generation,
		Synthesized:   row.Synthesized == 1,
		SourcesDigest: row.SourcesDigest,
	}, true, nil
}

func (writer *Writer) WriteTotalSubmeters(ctx context.Context, sync energy.TotalSubmeterSync, subMeters []energy.TotalSubMeter) error {
	if len(subMeters) == 0 {
		return nil
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, subMeter := range subMeters {
		if subMeter.Ordinal < 0 {
			return fmt.Errorf("submeter ordinal %d must be non-negative", subMeter.Ordinal)
		}
		if err := encoder.Encode(map[string]any{
			"tenant_id":  sync.TenantID,
			"site_id":    sync.SiteID,
			"generation": sync.Generation,
			"point_id":   subMeter.PointID,
			"device_id":  subMeter.DeviceID,
			"meter_name": subMeter.MeterName,
			"ordinal":    uint32(subMeter.Ordinal),
		}); err != nil {
			return fmt.Errorf("encode total submeter: %w", err)
		}
	}
	return writer.insert(ctx, "total submeter insert", url.Values{
		"query":             {"INSERT INTO " + writer.database + ".site_total_submeters (tenant_id, site_id, generation, point_id, device_id, meter_name, ordinal) FORMAT JSONEachRow"},
		"wait_end_of_query": {"1"},
	}, body.Bytes())
}

func (writer *Writer) RecordTotalSubmeterSync(ctx context.Context, sync energy.TotalSubmeterSync) error {
	synthesized := uint8(0)
	if sync.Synthesized {
		synthesized = 1
	}
	body, err := json.Marshal(map[string]any{
		"tenant_id":      sync.TenantID,
		"site_id":        sync.SiteID,
		"generation":     sync.Generation,
		"synthesized":    synthesized,
		"sources_digest": sync.SourcesDigest,
	})
	if err != nil {
		return fmt.Errorf("encode total submeter sync: %w", err)
	}
	return writer.insert(ctx, "total submeter sync insert", url.Values{
		"query":             {"INSERT INTO " + writer.database + ".site_total_submeter_syncs (tenant_id, site_id, generation, synthesized, sources_digest) FORMAT JSONEachRow"},
		"wait_end_of_query": {"1"},
	}, append(body, '\n'))
}

func (writer *Writer) DropTotalSubmetersBefore(ctx context.Context, sync energy.TotalSubmeterSync) error {
	return writer.insert(ctx, "total submeter delete", url.Values{
		"query":            {"ALTER TABLE " + writer.database + ".site_total_submeters DELETE WHERE tenant_id = {tenant:UUID} AND site_id = {site:UUID} AND generation < {generation:UInt64}"},
		"param_tenant":     {sync.TenantID},
		"param_site":       {sync.SiteID},
		"param_generation": {strconv.FormatUint(sync.Generation, 10)},
	}, nil)
}

var (
	_ energy.TotalSubmeterReader = (*Reader)(nil)
	_ energy.TotalSubmeterWriter = (*Writer)(nil)
)
