package telemetry

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestNewClickHouseNativeHistorySinkValidation(t *testing.T) {
	if _, err := NewClickHouseNativeHistorySink(ClickHouseNativeHistoryConfig{
		Addr:     "clickhouse",
		Database: "telemetry_history",
		Table:    "observations",
	}); err == nil {
		t.Fatal("expected error for native address without port")
	}
	if _, err := NewClickHouseNativeHistorySink(ClickHouseNativeHistoryConfig{
		Addr:     ":9000",
		Database: "telemetry_history",
		Table:    "observations",
	}); err == nil {
		t.Fatal("expected error for native address without host")
	}
	if _, err := NewClickHouseNativeHistorySink(ClickHouseNativeHistoryConfig{
		Addr:     "127.0.0.1:9000",
		Database: "bad-db",
		Table:    "observations",
	}); err == nil {
		t.Fatal("expected error for invalid ClickHouse database identifier")
	}

	sink, err := NewClickHouseNativeHistorySink(ClickHouseNativeHistoryConfig{
		Addr:     "127.0.0.1:9000",
		Database: "telemetry_history",
		Table:    "observations",
		Username: "telemetry_history_writer",
	})
	if err != nil {
		t.Fatalf("unexpected open error: %v", err)
	}
	defer func() { _ = sink.Close() }()
	if !strings.Contains(sink.insertSQL, "INSERT INTO telemetry_history.observations (observation_id,") {
		t.Fatalf("unexpected insertSQL: %s", sink.insertSQL)
	}
	if strings.Contains(sink.insertSQL, "projected_at") {
		t.Fatalf("insertSQL should omit defaulted projected_at column: %s", sink.insertSQL)
	}
}

func TestClickHouseNativeHistorySinkClosedAndPermanentGuards(t *testing.T) {
	var nilSink *ClickHouseNativeHistorySink
	if err := nilSink.Ping(t.Context()); err == nil {
		t.Fatal("expected closed error on nil sink Ping")
	}
	if err := nilSink.InsertObservations(t.Context(), 1, []HistoryObservation{{ObservationID: "018f2e00-9100-7000-8000-000000000001"}}); err == nil {
		t.Fatal("expected closed error on nil sink InsertObservations")
	}

	sink, err := NewClickHouseNativeHistorySink(ClickHouseNativeHistoryConfig{
		Addr:     "127.0.0.1:9000",
		Database: "telemetry_history",
		Table:    "observations",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Poison rows fail before any TCP dial is attempted.
	err = sink.InsertObservations(t.Context(), 1, []HistoryObservation{{
		ObservationID: "not-a-uuid",
		SourceEventID: "018f2e00-9200-7000-8000-000000000001",
		PayloadSHA256: strings.Repeat("a", 64),
	}})
	if !IsPermanentHistoryError(err) {
		t.Fatalf("expected permanent error for bad observation ID, got %v", err)
	}

	err = sink.InsertObservations(t.Context(), 1, []HistoryObservation{{
		ObservationID: "018f2e00-9100-7000-8000-000000000001",
		SourceEventID: "018f2e00-9200-7000-8000-000000000001",
		SourceOffset:  -1,
		PayloadSHA256: strings.Repeat("a", 64),
	}})
	if !IsPermanentHistoryError(err) {
		t.Fatalf("expected permanent error for negative source offset, got %v", err)
	}

	if err := sink.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("second idempotent Close failed: %v", err)
	}
	if err := sink.Ping(t.Context()); err == nil {
		t.Fatal("expected Ping after Close to fail")
	}
	if err := sink.InsertObservations(t.Context(), 1, []HistoryObservation{{
		ObservationID: "018f2e00-9100-7000-8000-000000000001",
		SourceEventID: "018f2e00-9200-7000-8000-000000000001",
		PayloadSHA256: strings.Repeat("a", 64),
	}}); err == nil {
		t.Fatal("expected InsertObservations after Close to fail")
	}
}

func TestDeterministicNativeHistoryBatchTokenStableAcrossRetryOrder(t *testing.T) {
	rows := []HistoryObservation{
		{
			ObservationID:    "018f2e00-9100-7000-8000-000000000001",
			SourceEventID:    "018f2e00-9200-7000-8000-000000000001",
			PayloadSHA256:    strings.Repeat("a", 64),
			SampledAt:        time.Date(2026, 7, 29, 8, 0, 1, 0, time.FixedZone("CST", 8*3600)),
			ReceivedAt:       time.Date(2026, 7, 29, 8, 0, 2, 0, time.UTC),
			AcceptanceStatus: "ACCEPTED",
			Quality:          "GOOD",
			TenantID:         stringPointer("018f2d00-0000-7000-8000-000000000001"),
			SiteID:           stringPointer("018f2e00-1000-7000-8000-000000000001"),
			DeviceID:         stringPointer("018f2e00-3000-7000-8000-000000000001"),
			PointID:          stringPointer("018f2e00-3100-7000-8000-000000000001"),
			PointType:        stringPointer("TELEMETRY"),
			PointRevision:    func() *int64 { v := int64(1); return &v }(),
		},
		{
			ObservationID:    "018f2e00-9100-7000-8000-000000000002",
			SourceEventID:    "018f2e00-9200-7000-8000-000000000002",
			PayloadSHA256:    strings.Repeat("b", 64),
			SampledAt:        time.Date(2026, 7, 29, 8, 0, 3, 0, time.UTC),
			ReceivedAt:       time.Date(2026, 7, 29, 8, 0, 4, 0, time.UTC),
			AcceptanceStatus: "REJECTED",
			Quality:          "REJECTED",
		},
	}

	firstOrdered, err := prepareOrderedHistoryObservations(7, rows)
	if err != nil {
		t.Fatal(err)
	}
	reversed := slices.Clone(rows)
	slices.Reverse(reversed)
	secondOrdered, err := prepareOrderedHistoryObservations(7, reversed)
	if err != nil {
		t.Fatal(err)
	}
	tokenA := deterministicNativeHistoryBatchToken(7, firstOrdered)
	tokenB := deterministicNativeHistoryBatchToken(7, secondOrdered)
	if tokenA != tokenB {
		t.Fatalf("native batch token drifted across row order: %s != %s", tokenA, tokenB)
	}
	tokenDifferentSeq := deterministicNativeHistoryBatchToken(8, firstOrdered)
	if tokenDifferentSeq == tokenA {
		t.Fatal("expected different sequence to produce distinct token")
	}
}

func TestClassifyClickHouseNativeError(t *testing.T) {
	tooManyParts := classifyClickHouseNativeError(fmt.Errorf("wrapped: %w", &clickhouse.Exception{Code: 252, Message: "Too many parts (301)"}), "tok-252")
	if !IsClickHouseTooManyPartsError(tooManyParts) {
		t.Fatalf("expected ErrClickHouseTooManyParts for code 252, got %v", tooManyParts)
	}

	for _, code := range []int32{53, 469, 27} {
		perm := classifyClickHouseNativeError(&clickhouse.Exception{Code: code, Message: "type mismatch"}, "tok-perm")
		if !IsPermanentHistoryError(perm) {
			t.Fatalf("expected ErrHistoryBatchPermanent for code %d, got %v", code, perm)
		}
	}

	transient := classifyClickHouseNativeError(io.EOF, "tok-eof")
	if IsPermanentHistoryError(transient) || IsClickHouseTooManyPartsError(transient) || transient == nil {
		t.Fatalf("expected transient error for io.EOF, got %v", transient)
	}
}
