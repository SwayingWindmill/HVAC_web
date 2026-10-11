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

// profileDays is how many earlier days a forecast's time of day is averaged over.
const profileDays = 7

// BaselineEngine is the production baseline for the local Forecast chain. A chiller plant's
// load follows the working day, so where earlier days cover a forecast's time of day it forecasts
// their average at that time, with a band from how much those days differ; where none do it
// extends the linear trend of the history. Fewer than four observations are an explicit FALLBACK.
type BaselineEngine struct{}

func (BaselineEngine) Generate(_ context.Context, request Request, generatedAt time.Time) ([]Point, error) {
	if len(request.Observations) < 4 {
		return generateLastObservationFallback(request, generatedAt), nil
	}
	trend, rmse, ok := linearTrend(request.Observations)
	if !ok {
		return generateLastObservationFallback(request, generatedAt), nil
	}
	minutes, _ := granularityMinutes(request.Granularity)
	slot := time.Duration(minutes) * time.Minute
	bySlot := make(map[int64]float64, len(request.Observations))
	for _, observation := range request.Observations {
		bySlot[observation.ObservedAt.UTC().Truncate(slot).Unix()] = observation.Value
	}
	nonNegative := request.Target == "SITE_LOAD" || request.Target == "PV_GENERATION"
	return generatePoints(request, generatedAt, "VALID", func(forecastFor time.Time) (float64, *float64, *float64) {
		samples := make([]float64, 0, profileDays)
		for day := 1; day <= profileDays; day++ {
			if value, found := bySlot[forecastFor.UTC().Add(-time.Duration(day)*24*time.Hour).Truncate(slot).Unix()]; found {
				samples = append(samples, value)
			}
		}
		value, spread := trend(forecastFor), rmse
		if len(samples) > 0 {
			value, spread = meanAndDeviation(samples)
			if len(samples) == 1 {
				spread = rmse
			}
		}
		lower, upper := value-1.96*spread, value+1.96*spread
		if nonNegative {
			value, lower = math.Max(0, value), math.Max(0, lower)
		}
		return value, &lower, &upper
	}), nil
}

// linearTrend fits value against time; ok is false when every observation shares one instant.
func linearTrend(observations []Observation) (func(time.Time) float64, float64, bool) {
	firstAt := observations[0].ObservedAt.UTC()
	var sumX, sumY float64
	for _, observation := range observations {
		sumX += observation.ObservedAt.UTC().Sub(firstAt).Minutes()
		sumY += observation.Value
	}
	n := float64(len(observations))
	meanX, meanY := sumX/n, sumY/n
	var numerator, denominator float64
	for _, observation := range observations {
		x := observation.ObservedAt.UTC().Sub(firstAt).Minutes()
		numerator += (x - meanX) * (observation.Value - meanY)
		denominator += (x - meanX) * (x - meanX)
	}
	if denominator == 0 {
		return nil, 0, false
	}
	slope := numerator / denominator
	intercept := meanY - slope*meanX
	var squaredResidual float64
	for _, observation := range observations {
		residual := observation.Value - (intercept + slope*observation.ObservedAt.UTC().Sub(firstAt).Minutes())
		squaredResidual += residual * residual
	}
	return func(at time.Time) float64 { return intercept + slope*at.UTC().Sub(firstAt).Minutes() }, math.Sqrt(squaredResidual / n), true
}

func meanAndDeviation(values []float64) (float64, float64) {
	var sum float64
	for _, value := range values {
		sum += value
	}
	mean := sum / float64(len(values))
	var squared float64
	for _, value := range values {
		squared += (value - mean) * (value - mean)
	}
	return mean, math.Sqrt(squared / float64(len(values)))
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
	return NewServiceWithEngine(sink, publication, BaselineEngine{}, clock)
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
