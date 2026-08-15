package forecast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type Sink interface {
	InsertForecastPoints(context.Context, []Point) error
	HasForecastJob(context.Context, string, int) (bool, error)
}

// ForecastEngine isolates the Forecast domain from a concrete model/runtime.
type ForecastEngine interface {
	Generate(context.Context, Request, time.Time) ([]Point, error)
}

type LastValueEngine struct{}

func (LastValueEngine) Generate(_ context.Context, request Request, generatedAt time.Time) ([]Point, error) {
	minutes, _ := granularityMinutes(request.Granularity)
	origin := request.ForecastOrigin.UTC()
	pointCount := request.HorizonMinutes / minutes
	points := make([]Point, 0, pointCount)
	for index := 1; index <= pointCount; index++ {
		forecastFor := origin.Add(time.Duration(index*minutes) * time.Minute)
		points = append(points, Point{
			ForecastID: deterministicForecastID(request.ForecastJobID, forecastFor),
			TenantID: request.TenantID, SiteID: request.SiteID,
			SubjectType: request.SubjectType, SubjectID: request.SubjectID, Target: request.Target,
			ForecastJobID: request.ForecastJobID, ForecastSnapshotID: request.ForecastSnapshotID,
			DeploymentID: request.DeploymentID, ModelID: request.ModelID,
			ModelVersionID: request.ModelVersionID, ModelVersion: request.ModelVersion,
			FeatureSetVersionID: request.FeatureSetVersionID, FeatureSetVersion: request.FeatureSetVersion,
			InputSnapshotID: request.InputSnapshotID, TopologyVersionID: request.TopologyVersionID,
			ForecastOrigin: origin, ForecastFor: forecastFor, HorizonMinutes: uint32(index * minutes),
			Value: *request.LastValue, Unit: request.Unit, Quality: "FALLBACK", GeneratedAt: generatedAt.UTC(),
		})
	}
	return points, nil
}

type Clock func() time.Time

type Service struct {
	sink        Sink
	publication PublicationStore
	engine      ForecastEngine
	clock       Clock
}

func NewService(sink Sink, publication PublicationStore, clock Clock) (*Service, error) {
	return NewServiceWithEngine(sink, publication, LastValueEngine{}, clock)
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
	// PERSISTING. Reconcile() proves the ClickHouse result by forecastJobId and
	// repairs Snapshot + Outbox without writing duplicate forecast facts.
	if err = service.publication.CompletePublication(ctx, publication, service.clock().UTC()); err != nil {
		return nil, fmt.Errorf("complete forecast publication: %w", err)
	}
	return points, nil
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
	ordered := append([]Point(nil), points...)
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
		Request: request,
		ResultCount: len(ordered),
		ResultChecksum: hex.EncodeToString(digest[:]),
		WindowStart: ordered[0].ForecastFor.UTC(),
		WindowEnd: ordered[len(ordered)-1].ForecastFor.UTC(),
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
