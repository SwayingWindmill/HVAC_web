package forecast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

type Sink interface {
	InsertForecastPoints(context.Context, []Point) error
	HasForecastJob(context.Context, string, int) (bool, error)
	ReadForecastJob(context.Context, string) ([]Point, error)
}

// ForecastEngine owns model inference only. Durable job state and publication stay
// in Service/PostgresStore so model providers cannot become workflow authorities.
type ForecastEngine interface {
	Generate(context.Context, Request, time.Time) ([]Point, error)
}

// TrendEngine is the production baseline for the local Forecast chain. Four or
// more observations produce an actual fitted model with an uncertainty band;
// shorter but non-empty input is explicitly downgraded to LAST_OBSERVATION.
type TrendEngine struct{}

func (TrendEngine) Generate(_ context.Context, request Request, generatedAt time.Time) ([]Point, error) {
	if len(request.Observations) < 4 {
		return generateLastObservationFallback(request, generatedAt), nil
	}
	firstAt := request.Observations[0].ObservedAt.UTC()
	var sumX, sumY float64
	for _, observation := range request.Observations {
		x := observation.ObservedAt.UTC().Sub(firstAt).Minutes()
		sumX += x
		sumY += observation.Value
	}
	n := float64(len(request.Observations))
	meanX, meanY := sumX/n, sumY/n
	var numerator, denominator float64
	for _, observation := range request.Observations {
		x := observation.ObservedAt.UTC().Sub(firstAt).Minutes()
		numerator += (x - meanX) * (observation.Value - meanY)
		denominator += (x - meanX) * (x - meanX)
	}
	if denominator == 0 {
		return generateLastObservationFallback(request, generatedAt), nil
	}
	slope := numerator / denominator
	intercept := meanY - slope*meanX
	var squaredResidual float64
	for _, observation := range request.Observations {
		x := observation.ObservedAt.UTC().Sub(firstAt).Minutes()
		residual := observation.Value - (intercept + slope*x)
		squaredResidual += residual * residual
	}
	rmse := math.Sqrt(squaredResidual / n)
	return generatePoints(request, generatedAt, "VALID", func(forecastFor time.Time) (float64, *float64, *float64) {
		x := forecastFor.UTC().Sub(firstAt).Minutes()
		value := intercept + slope*x
		if request.Target == "SITE_LOAD" || request.Target == "PV_GENERATION" {
			value = math.Max(0, value)
		}
		lower, upper := value-1.96*rmse, value+1.96*rmse
		if request.Target == "SITE_LOAD" || request.Target == "PV_GENERATION" {
			lower = math.Max(0, lower)
		}
		return value, &lower, &upper
	}), nil
}

func generateLastObservationFallback(request Request, generatedAt time.Time) []Point {
	value := request.Observations[len(request.Observations)-1].Value
	return generatePoints(request, generatedAt, "FALLBACK", func(time.Time) (float64, *float64, *float64) {
		return value, nil, nil
	})
}

func generatePoints(request Request, generatedAt time.Time, quality string, valueFor func(time.Time) (float64, *float64, *float64)) []Point {
	minutes, _ := granularityMinutes(request.Granularity)
	origin := request.ForecastOrigin.UTC()
	pointCount := request.HorizonMinutes / minutes
	points := make([]Point, 0, pointCount)
	for index := 1; index <= pointCount; index++ {
		forecastFor := origin.Add(time.Duration(index*minutes) * time.Minute)
		value, lower, upper := valueFor(forecastFor)
		points = append(points, Point{
			ForecastID: deterministicForecastID(request.ForecastJobID, forecastFor), TenantID: request.TenantID, SiteID: request.SiteID,
			SubjectType: request.SubjectType, SubjectID: request.SubjectID, Target: request.Target, ForecastJobID: request.ForecastJobID,
			ForecastSnapshotID: request.ForecastSnapshotID, DeploymentID: request.DeploymentID, ModelID: request.ModelID,
			ModelVersionID: request.ModelVersionID, ModelVersion: request.ModelVersion, FeatureSetVersionID: request.FeatureSetVersionID,
			FeatureSetVersion: request.FeatureSetVersion, InputSnapshotID: request.InputSnapshotID, TopologyVersionID: request.TopologyVersionID,
			ForecastOrigin: origin, ForecastFor: forecastFor, HorizonMinutes: uint32(index * minutes), Value: value, Unit: request.Unit,
			LowerBound: lower, UpperBound: upper, Quality: quality, GeneratedAt: generatedAt.UTC(),
		})
	}
	return points
}

type Clock func() time.Time

type Service struct {
	sink        Sink
	publication PublicationStore
	engine      ForecastEngine
	clock       Clock
}

func NewService(sink Sink, publication PublicationStore, clock Clock) (*Service, error) {
	return NewServiceWithEngine(sink, publication, TrendEngine{}, clock)
}

func NewServiceWithEngine(sink Sink, publication PublicationStore, engine ForecastEngine, clock Clock) (*Service, error) {
	if sink == nil || publication == nil {
		return nil, fmt.Errorf("forecast ClickHouse sink and PostgreSQL publication store are required")
	}
	if engine == nil {
		return nil, fmt.Errorf("forecast engine is required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Service{sink: sink, publication: publication, engine: engine, clock: clock}, nil
}

func (service *Service) Forecast(ctx context.Context, request Request) ([]Point, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	startedAt := service.clock().UTC()
	if err := service.publication.StartJob(ctx, request, startedAt); err != nil {
		return nil, fmt.Errorf("start forecast job: %w", err)
	}
	points, err := service.engine.Generate(ctx, request, startedAt)
	if err != nil {
		_ = service.publication.FailJob(ctx, request, "ENGINE_FAILED", service.clock().UTC())
		return nil, fmt.Errorf("forecast engine: %w", err)
	}
	publication, err := describePublication(request, points)
	if err != nil {
		_ = service.publication.FailJob(ctx, request, "RESULT_INVALID", service.clock().UTC())
		return nil, err
	}
	if err = service.publication.BeginPublication(ctx, publication, service.clock().UTC()); err != nil {
		_ = service.publication.FailJob(ctx, request, "PUBLICATION_BEGIN_FAILED", service.clock().UTC())
		return nil, fmt.Errorf("begin forecast publication: %w", err)
	}
	if err = service.sink.InsertForecastPoints(ctx, points); err != nil {
		_ = service.publication.FailJob(ctx, request, "CLICKHOUSE_WRITE_FAILED", service.clock().UTC())
		return nil, fmt.Errorf("persist forecast result: %w", err)
	}
	// Once ClickHouse succeeds, PostgreSQL completion failure must leave the job
	// PERSISTING. Reconcile proves the immutable result by forecastJobId before
	// creating the Snapshot/outbox event; it never fabricates a second result.
	if err = service.publication.CompletePublication(ctx, publication, service.clock().UTC()); err != nil {
		return nil, fmt.Errorf("complete forecast publication: %w", err)
	}
	return points, nil
}

type PublishedForecast struct {
	Snapshot ForecastSnapshotReference `json:"snapshot"`
	Points   []Point                   `json:"points"`
}

func (service *Service) LatestForecast(ctx context.Context, tenantID, siteID, target string) (PublishedForecast, error) {
	reference, err := service.publication.LatestForecast(ctx, tenantID, siteID, target)
	if err != nil {
		return PublishedForecast{}, err
	}
	points, err := service.sink.ReadForecastJob(ctx, reference.ForecastJobID)
	if err != nil {
		return PublishedForecast{}, err
	}
	if len(points) != reference.ResultCount {
		return PublishedForecast{}, fmt.Errorf("forecast snapshot expected %d points, found %d", reference.ResultCount, len(points))
	}
	for _, point := range points {
		if point.ForecastJobID != reference.ForecastJobID || point.ForecastSnapshotID != reference.SnapshotID || point.TenantID != tenantID || point.SiteID != siteID || point.Target != target || point.Quality != reference.Quality {
			return PublishedForecast{}, errors.New("forecast history does not match persisted snapshot provenance")
		}
	}
	return PublishedForecast{Snapshot: reference, Points: points}, nil
}

func (service *Service) Reconcile(ctx context.Context, staleBefore time.Time, limit int) (int, error) {
	publications, err := service.publication.ListStalePublications(ctx, staleBefore.UTC(), limit)
	if err != nil {
		return 0, err
	}
	repaired := 0
	for _, publication := range publications {
		present, checkErr := service.sink.HasForecastJob(ctx, publication.Request.ForecastJobID, publication.ResultCount)
		if checkErr != nil {
			return repaired, checkErr
		}
		if !present {
			continue
		}
		if err = service.publication.CompletePublication(ctx, publication, service.clock().UTC()); err != nil {
			return repaired, err
		}
		repaired++
	}
	return repaired, nil
}

func describePublication(request Request, points []Point) (Publication, error) {
	if len(points) == 0 {
		return Publication{}, fmt.Errorf("forecast engine returned no results")
	}
	quality := points[0].Quality
	if quality != "VALID" && quality != "DEGRADED" && quality != "FALLBACK" {
		return Publication{}, fmt.Errorf("forecast engine returned unsupported quality %q", quality)
	}
	ordered := append([]Point(nil), points...)
	for _, point := range ordered {
		if point.Quality != quality {
			return Publication{}, fmt.Errorf("forecast engine mixed result quality within one snapshot")
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].ForecastFor.Equal(ordered[j].ForecastFor) {
			return ordered[i].ForecastID < ordered[j].ForecastID
		}
		return ordered[i].ForecastFor.Before(ordered[j].ForecastFor)
	})
	payload, err := json.Marshal(ordered)
	if err != nil {
		return Publication{}, fmt.Errorf("encode forecast result digest: %w", err)
	}
	digest := sha256.Sum256(payload)
	return Publication{
		Request: request, ResultCount: len(ordered), ResultChecksum: hex.EncodeToString(digest[:]), Quality: quality,
		WindowStart: ordered[0].ForecastFor.UTC(), WindowEnd: ordered[len(ordered)-1].ForecastFor.UTC(),
	}, nil
}

func deterministicForecastID(jobID string, forecastFor time.Time) string {
	digest := sha256.Sum256([]byte(jobID + "|" + forecastFor.UTC().Format(time.RFC3339Nano)))
	bytes := digest[:16]
	bytes[6] = (bytes[6] & 0x0f) | 0x50
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(bytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[0:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:32])
}
