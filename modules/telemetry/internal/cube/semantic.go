package cube

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/modules/telemetry/internal/analytics"
)

// QuerySemantic answers the Operations Agent's semantic query (#573): the catalog's members of one
// view by time bucket, always cut to the query's Site, with the Site's data watermark. More rows than
// the limit is ErrSemanticRowLimit rather than a silently shortened answer.
func (client *Client) QuerySemantic(ctx context.Context, caller analytics.CallerContext, query analyticsmodel.SemanticQuery) (analyticsmodel.SemanticQueryResponse, error) {
	if err := query.Validate(); err != nil {
		return analyticsmodel.SemanticQueryResponse{}, err
	}
	view, _ := analyticsmodel.Catalog().View(query.View)
	location, err := time.LoadLocation(query.Timezone)
	if err != nil {
		return analyticsmodel.SemanticQueryResponse{}, err
	}
	queryContext, cancel := context.WithTimeout(ctx, maximumCubeQueryDuration)
	defer cancel()
	token, err := client.tokenFactory.Token(queryContext, caller, query.TenantID, query.SiteID)
	if err != nil {
		return analyticsmodel.SemanticQueryResponse{}, fmt.Errorf("create Cube token: %w", err)
	}
	period := member(query.View, analyticsmodel.Catalog().TimeDimension) + "." + string(query.Granularity)
	cubeRequest := cubeQuery{
		Filters: []cubeFilter{{Member: member(query.View, "site_id"), Operator: "equals", Values: []string{query.SiteID}}},
		TimeDimensions: []cubeTimeDimension{{
			Dimension:   member(query.View, analyticsmodel.Catalog().TimeDimension),
			DateRange:   []string{query.From.In(location).Format(cubeWallTime), query.To.In(location).Add(-time.Millisecond).Format(cubeWallTime)},
			Granularity: string(query.Granularity),
		}},
		Order:    map[string]string{period: "asc"},
		Timezone: query.Timezone,
		Limit:    analyticsmodel.SemanticRowLimit + 1,
	}
	columns := []analyticsmodel.SemanticColumn{{Name: analyticsmodel.Catalog().TimeDimension, Title: "时段", Kind: analyticsmodel.SemanticColumnTime}}
	for _, name := range query.Dimensions {
		dimension, _ := view.Dimension(name)
		cubeRequest.Dimensions = append(cubeRequest.Dimensions, member(query.View, name))
		columns = append(columns, analyticsmodel.SemanticColumn{Name: name, Title: dimension.Title, Kind: analyticsmodel.SemanticColumnDimension})
	}
	for _, name := range query.Measures {
		measure, _ := view.Measure(name)
		cubeRequest.Measures = append(cubeRequest.Measures, member(query.View, name))
		columns = append(columns, analyticsmodel.SemanticColumn{Name: name, Title: measure.Title, Kind: analyticsmodel.SemanticColumnMeasure})
	}
	decoded, err := client.load(queryContext, token, cubeRequest)
	if err != nil {
		return analyticsmodel.SemanticQueryResponse{}, fmt.Errorf("query Cube semantic view: %w", err)
	}
	if len(decoded.Data) > analyticsmodel.SemanticRowLimit {
		return analyticsmodel.SemanticQueryResponse{}, analyticsmodel.ErrSemanticRowLimit
	}
	rows := make([][]any, 0, len(decoded.Data))
	buckets := map[any]bool{}
	for _, row := range decoded.Data {
		mapped, err := mapSemanticRow(row, period, query, location)
		if err != nil {
			return analyticsmodel.SemanticQueryResponse{}, err
		}
		rows = append(rows, mapped)
		buckets[mapped[0]] = true
	}
	metadata, err := client.semanticMetadata(queryContext, token, query, location)
	if err != nil {
		return analyticsmodel.SemanticQueryResponse{}, err
	}
	// Cube leaves out a bucket nothing was metered in; such a gap leaves the answer incomplete too.
	metadata.Partial = metadata.Partial || len(buckets) < query.Buckets()
	return analyticsmodel.SemanticQueryResponse{SchemaVersion: 1, Columns: columns, Rows: rows, Metadata: metadata}, nil
}

func mapSemanticRow(row map[string]json.RawMessage, period string, query analyticsmodel.SemanticQuery, location *time.Location) ([]any, error) {
	bucket, err := scalarString(row[period])
	if err != nil {
		return nil, fmt.Errorf("decode Cube semantic bucket: %w", err)
	}
	start, err := parseCubeTime(bucket, location)
	if err != nil {
		return nil, fmt.Errorf("decode Cube semantic bucket: %w", err)
	}
	values := []any{start.UTC().Format(time.RFC3339)}
	for _, name := range query.Dimensions {
		value, present, err := optionalScalarString(row[member(query.View, name)])
		if err != nil {
			return nil, fmt.Errorf("decode Cube dimension %s: %w", name, err)
		}
		if present {
			values = append(values, value)
		} else {
			values = append(values, nil)
		}
	}
	for _, name := range query.Measures {
		value, present, err := optionalScalarString(row[member(query.View, name)])
		if err != nil {
			return nil, fmt.Errorf("decode Cube measure %s: %w", name, err)
		}
		if !present {
			values = append(values, nil)
			continue
		}
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("decode Cube measure %s: %w", name, err)
		}
		values = append(values, number)
	}
	return values, nil
}

// semanticMetadata is how far the Site's metered energy reaches since the range began, whichever
// view was asked: every view reads the same facts.
func (client *Client) semanticMetadata(ctx context.Context, token string, query analyticsmodel.SemanticQuery, location *time.Location) (analyticsmodel.SemanticMetadata, error) {
	decoded, err := client.load(ctx, token, cubeQuery{
		Measures: []string{member(siteView, "max_data_watermark"), member(siteView, "max_dataset_revision")},
		Filters: []cubeFilter{
			{Member: member(siteView, "site_id"), Operator: "equals", Values: []string{query.SiteID}},
			{Member: member(siteView, "period_end"), Operator: "afterOrOnDate", Values: []string{query.From.In(location).Format(cubeWallTime)}},
		},
		Timezone: query.Timezone,
		Limit:    1,
	})
	if err != nil {
		return analyticsmodel.SemanticMetadata{}, fmt.Errorf("query Cube semantic metadata: %w", err)
	}
	watermark, revision, err := mapEnergyMetadata(decoded)
	if err != nil {
		return analyticsmodel.SemanticMetadata{}, err
	}
	metadata := analyticsmodel.SemanticMetadata{DatasetRevision: client.datasetRevision + ":empty", Partial: true}
	if revision > 0 {
		metadata.DatasetRevision = fmt.Sprintf("%s:%d", client.datasetRevision, revision)
	}
	if !watermark.IsZero() {
		metadata.DataWatermark = &watermark
		metadata.Partial = watermark.Before(query.To.UTC())
	}
	return metadata, nil
}
