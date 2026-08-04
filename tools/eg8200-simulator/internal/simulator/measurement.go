package simulator

import (
	"errors"
	"fmt"
	"time"
)

type Measurement struct {
	DeviceID       string
	SensorID       string
	SubjectType    string
	SubjectID      string
	SourceKey      string
	TelemetryKey   string
	SourceProtocol string
	SourceAddress  string
	ObservedAt     time.Time
	Value          any
	Quality        string
	Sequence       uint64
}

type scheduledPoint struct {
	config      PointConfig
	nextSample  time.Time
	nextPublish time.Time
	latest      *Measurement
	sequence    uint64
}

type MeasurementScheduler struct {
	points []scheduledPoint
}

func NewMeasurementScheduler(config Config) (*MeasurementScheduler, error) {
	if len(config.Points) == 0 {
		return nil, errors.New("measurement scheduler requires telemetry points")
	}
	points := make([]scheduledPoint, 0, len(config.Points))
	for _, point := range config.Points {
		points = append(points, scheduledPoint{config: point})
	}
	return &MeasurementScheduler{points: points}, nil
}

func (scheduler *MeasurementScheduler) Observe(snapshot Snapshot) ([]Measurement, error) {
	if scheduler == nil {
		return nil, errors.New("measurement scheduler is unavailable")
	}
	measurements := make([]Measurement, 0, len(scheduler.points))
	for index := range scheduler.points {
		point := &scheduler.points[index]
		deviceTelemetry, ok := snapshot.Devices[point.config.DeviceID]
		if !ok {
			return nil, fmt.Errorf("snapshot is missing device %s", point.config.DeviceID)
		}
		if point.nextSample.IsZero() || !snapshot.ObservedAt.Before(point.nextSample) {
			value, exists := deviceTelemetry[point.config.SourceKey]
			if !exists {
				return nil, fmt.Errorf("snapshot device %s is missing source point %s", point.config.DeviceID, point.config.SourceKey)
			}
			point.sequence++
			point.latest = &Measurement{
				DeviceID:       point.config.DeviceID,
				SensorID:       point.config.SensorID,
				SubjectType:    point.config.SubjectType,
				SubjectID:      point.config.SubjectID,
				SourceKey:      point.config.SourceKey,
				TelemetryKey:   point.config.TelemetryKey,
				SourceProtocol: point.config.SourceProtocol,
				SourceAddress:  point.config.SourceAddress,
				ObservedAt:     snapshot.ObservedAt,
				Value:          value,
				Quality:        "GOOD",
				Sequence:       point.sequence,
			}
			point.nextSample = advanceSchedule(point.nextSample, snapshot.ObservedAt, point.config.SampleEvery())
		}
		if point.latest != nil && (point.nextPublish.IsZero() || !snapshot.ObservedAt.Before(point.nextPublish)) {
			measurements = append(measurements, *point.latest)
			point.latest = nil
			point.nextPublish = advanceSchedule(point.nextPublish, snapshot.ObservedAt, point.config.PublishEvery())
		}
	}
	return measurements, nil
}

func advanceSchedule(current, observedAt time.Time, interval time.Duration) time.Time {
	if current.IsZero() {
		return observedAt.Add(interval)
	}
	for !current.After(observedAt) {
		current = current.Add(interval)
	}
	return current
}
