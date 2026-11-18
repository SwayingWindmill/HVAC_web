package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readClickHouseRequestBody(t *testing.T, r *http.Request) ([]byte, []byte) {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.Header.Get("Content-Encoding") != "gzip" {
		return raw, raw
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("open gzip request body: %v", err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("gunzip request body: %v", err)
	}
	return raw, decoded
}

func TestClickHouseHistorySinkRetryPreservesPayloadAndToken(t *testing.T) {
	var rawBodies [][]byte
	var bodies [][]byte
	var encodings []string
	var tokens []string
	var mutex sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, decoded := readClickHouseRequestBody(t, r)
		mutex.Lock()
		rawBodies = append(rawBodies, raw)
		bodies = append(bodies, decoded)
		encodings = append(encodings, r.Header.Get("Content-Encoding"))
		tokens = append(tokens, r.URL.Query().Get("insert_deduplication_token"))
		mutex.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	rows := []HistoryObservation{
		{ObservationID: "018f2e00-9100-7000-8000-000000000001", PayloadSHA256: strings.Repeat("a", 64)},
		{ObservationID: "018f2e00-9100-7000-8000-000000000002", PayloadSHA256: strings.Repeat("b", 64)},
	}
	if err := sink.InsertObservations(t.Context(), 1, rows); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(rows)
	if err := sink.InsertObservations(t.Context(), 1, rows); err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	sameRetry := bytes.Equal(bodies[0], bodies[1]) && tokens[0] == tokens[1]
	expectedToken := fmt.Sprintf("%x", sha256.Sum256(bodies[0]))
	sameTokenHash := tokens[0] == expectedToken
	bothGzipped := encodings[0] == "gzip" && encodings[1] == "gzip" && !bytes.Equal(rawBodies[0], bodies[0])
	mutex.Unlock()
	if !bothGzipped {
		t.Fatalf("expected gzip Content-Encoding and compressed body, encodings=%v", encodings)
	}
	if !sameTokenHash {
		t.Fatalf("dedup token=%q want sha256(uncompressed)=%q", tokens[0], expectedToken)
	}
	if !sameRetry {
		t.Fatal("retry payload/token changed with RETURNING order")
	}
	if rows[0].ObservationID != "018f2e00-9100-7000-8000-000000000002" {
		t.Fatal("sink mutated caller data")
	}
	rows[0].SourceOffset++
	if err := sink.InsertObservations(t.Context(), 1, rows); err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	tokenReused := tokens[2] == tokens[1]
	mutex.Unlock()
	if tokenReused {
		t.Fatal("different payload reused token")
	}
	rows[0].ValueString = stringPointer(strings.Repeat("x", maxHistoryBatchBytes))
	if err := sink.InsertObservations(t.Context(), 1, rows); err == nil {
		t.Fatal("oversized request accepted")
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(bodies) != 3 {
		t.Fatal("oversized request performed I/O")
	}
}

func TestClickHouseHistorySinkWritesStableMicrobatch(t *testing.T) {
	observationIDs := []string{
		"018f2e00-9100-7000-8000-000000000001",
		"018f2e00-9100-7000-8000-000000000002",
	}
	requests := make(map[string]url.Values, len(observationIDs))
	rows := make(map[string]HistoryObservation, len(observationIDs))
	var mutex sync.Mutex
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "telemetry_history" || password != "[REDACTED_SECRET]" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/x-ndjson" || request.Header.Get("Content-Encoding") != "gzip" {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		_, decoded := readClickHouseRequestBody(t, request)
		mutex.Lock()
		requestCount++
		decoder := json.NewDecoder(bytes.NewReader(decoded))
		for {
			var row HistoryObservation
			if err := decoder.Decode(&row); err == io.EOF {
				break
			} else if err != nil {
				mutex.Unlock()
				http.Error(writer, "invalid row", http.StatusBadRequest)
				return
			}
			requests[row.ObservationID] = request.URL.Query()
			rows[row.ObservationID] = row
		}
		mutex.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL:    server.URL,
		Database:   "telemetry_history",
		Table:      "observations",
		Username:   "telemetry_history",
		Password:   "[REDACTED_SECRET]",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	number := 812.5
	pointRevision := int64(1)
	observations := []HistoryObservation{
		{
			ObservationID: observationIDs[0], TenantID: stringPointer("018f2d00-0000-7000-8000-000000000001"),
			SiteID: stringPointer("018f2e00-1000-7000-8000-000000000001"), DeviceID: stringPointer("018f2e00-3000-7000-8000-000000000001"),
			PointID: stringPointer("018f2e00-3100-7000-8000-000000000001"), SensorID: stringPointer("018f2e00-3200-7000-8000-000000000001"),
			SourceID:      sourceA,
			SourceEventID: "018f2e00-6200-7000-8000-000000000001", SourcePartition: "tb-a", SourceOffset: 10,
			SourcePath: "POLL", TelemetryKey: "chiller.power", PointType: stringPointer("TELEMETRY"), PointRevision: &pointRevision, ValueNumber: &number,
			SampledAt: time.Date(2026, 7, 29, 7, 59, 58, 0, time.UTC), ReceivedAt: time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC),
			AcceptanceStatus: "ACCEPTED", Quality: "GOOD", QualityReasons: []string{}, PayloadSHA256: strings.Repeat("a", 64),
		},
		{
			ObservationID: observationIDs[1], SourceID: sourceA,
			SourceEventID: "018f2e00-6200-7000-8000-000000000002", SourcePartition: "tb-a", SourceOffset: 11,
			SourcePath: "POLL", TelemetryKey: "chiller.cop",
			SampledAt: time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC), ReceivedAt: time.Date(2026, 7, 29, 8, 0, 1, 0, time.UTC),
			AcceptanceStatus: "REJECTED", Quality: "REJECTED", QualityReasons: []string{"OUT_OF_RANGE"}, PayloadSHA256: strings.Repeat("b", 64),
		},
	}
	if err := sink.InsertObservations(t.Context(), 1, observations); err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(requests) != 2 || len(rows) != 2 {
		t.Fatalf("requests=%d rows=%d", len(requests), len(rows))
	}
	if requestCount != 1 {
		t.Fatalf("microbatch used %d requests, want 1", requestCount)
	}
	for _, observationID := range observationIDs {
		values, ok := requests[observationID]
		if !ok {
			t.Fatalf("missing request for %s", observationID)
		}
		if len(values.Get("insert_deduplication_token")) != 64 {
			t.Fatalf("dedup token=%q", values.Get("insert_deduplication_token"))
		}
		if values.Get("deduplicate_blocks_in_dependent_materialized_views") != "1" || values.Get("materialized_views_ignore_errors") != "0" {
			t.Fatalf("hourly recovery settings=%v", values)
		}
		if values.Get("date_time_input_format") != "best_effort" {
			t.Fatalf("date time input format=%q", values.Get("date_time_input_format"))
		}
		if values.Get("async_insert") != "0" || values.Get("insert_deduplicate") != "1" {
			t.Fatalf("synchronous deduplicated insert settings=%v", values)
		}
		if values.Has("wait_for_async_insert") || values.Has("async_insert_deduplicate") {
			t.Fatalf("async-only insert settings must be absent: %v", values)
		}
		if query := values.Get("query"); query != "INSERT INTO telemetry_history.observations FORMAT JSONEachRow" {
			t.Fatalf("query=%q", query)
		}
	}
	first := rows[observations[0].ObservationID]
	if first.ObservationID != observations[0].ObservationID || first.ValueNumber == nil || *first.ValueNumber != number {
		t.Fatalf("first row=%#v", first)
	}
}

func TestClickHouseHistorySinkValidatesEntireBatchBeforeInsert(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = sink.InsertObservations(t.Context(), 1, []HistoryObservation{
		{ObservationID: "018f2e00-9100-7000-8000-000000000003", PayloadSHA256: strings.Repeat("c", 64)},
		{ObservationID: "018f2e00-9100-7000-8000-000000000004", PayloadSHA256: "INVALID"},
	})
	if err == nil || !strings.Contains(err.Error(), "lowercase SHA-256") {
		t.Fatalf("err=%v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestClickHouseHistorySinkRejectsAcceptedObservationWithoutTenantScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = sink.InsertObservations(t.Context(), 1, []HistoryObservation{{
		ObservationID: "018f2e00-9100-7000-8000-000000000006", AcceptanceStatus: "ACCEPTED",
		PayloadSHA256: strings.Repeat("e", 64),
	}})
	if err == nil || !strings.Contains(err.Error(), "requires UUIDv7 Tenant") {
		t.Fatalf("err=%v", err)
	}
}

func TestClickHouseHistorySinkRejectsFailedInsert(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "readonly", http.StatusForbidden)
	}))
	defer server.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = sink.InsertObservations(t.Context(), 1, []HistoryObservation{{
		ObservationID: "018f2e00-9100-7000-8000-000000000005",
		PayloadSHA256: strings.Repeat("d", 64),
	}})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err=%v", err)
	}
	if IsPermanentHistoryError(err) {
		t.Fatalf("expected HTTP 403 error not to be classified as permanent batch rejection: %v", err)
	}
}

func TestClickHouseHistorySinkClassifiesPermanentVsTransientFailures(t *testing.T) {
	statusCode := http.StatusBadRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "cannot parse input", statusCode)
	}))
	defer server.Close()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	valid := []HistoryObservation{{
		ObservationID: "018f2e00-9100-7000-8000-000000000005",
		PayloadSHA256: strings.Repeat("d", 64),
	}}
	badReqErr := sink.InsertObservations(t.Context(), 1, valid)
	if !IsPermanentHistoryError(badReqErr) {
		t.Fatalf("expected HTTP 400 to satisfy IsPermanentHistoryError, got %v", badReqErr)
	}

	statusCode = http.StatusServiceUnavailable
	transientErr := sink.InsertObservations(t.Context(), 1, valid)
	if transientErr == nil || IsPermanentHistoryError(transientErr) {
		t.Fatalf("expected HTTP 503 not to be permanent, got %v", transientErr)
	}

	validationErr := sink.InsertObservations(t.Context(), 1, []HistoryObservation{{
		ObservationID: "bad-id",
		PayloadSHA256: strings.Repeat("d", 64),
	}})
	if !IsPermanentHistoryError(validationErr) {
		t.Fatalf("expected row validation failure to satisfy IsPermanentHistoryError, got %v", validationErr)
	}
}

func TestNewClickHouseHistorySinkDefaultHTTPTransport(t *testing.T) {
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL:  "http://127.0.0.1:8123",
		Database: "telemetry_history",
		Table:    "observations",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sink.httpClient == nil {
		t.Fatal("expected default HTTPClient to be initialized")
	}
	if sink.httpClient.Timeout != 30*time.Second {
		t.Fatalf("Timeout=%s, want 30s", sink.httpClient.Timeout)
	}
	transport, ok := sink.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("Transport=%T, want *http.Transport", sink.httpClient.Transport)
	}
	if transport.MaxConnsPerHost != 16 ||
		transport.MaxIdleConns != 64 ||
		transport.MaxIdleConnsPerHost != 16 ||
		transport.IdleConnTimeout != 90*time.Second ||
		transport.TLSHandshakeTimeout != 10*time.Second ||
		transport.ResponseHeaderTimeout != 30*time.Second ||
		transport.ExpectContinueTimeout != time.Second ||
		transport.DialContext == nil {
		t.Fatalf("unexpected transport tuning: MaxConnsPerHost=%d MaxIdleConns=%d MaxIdleConnsPerHost=%d IdleConnTimeout=%s TLSHandshakeTimeout=%s ResponseHeaderTimeout=%s ExpectContinueTimeout=%s DialContext=%v",
			transport.MaxConnsPerHost, transport.MaxIdleConns, transport.MaxIdleConnsPerHost, transport.IdleConnTimeout, transport.TLSHandshakeTimeout, transport.ResponseHeaderTimeout, transport.ExpectContinueTimeout, transport.DialContext != nil)
	}
}

func TestClickHouseHistorySinkPing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "ch_user" || pass != "ch_pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("query") != "SELECT 1" || r.URL.Query().Get("enable_http_compression") != "1" {
			t.Fatalf("unexpected query params: %v", r.URL.Query())
		}
		if r.Header.Get("Accept-Encoding") != "gzip" {
			t.Fatalf("expected Accept-Encoding: gzip, got %q", r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = io.WriteString(gz, "1\n")
		_ = gz.Close()
	}))
	defer server.Close()

	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations",
		Username: "ch_user", Password: "ch_pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}

func TestClickHouseHistorySinkGzipPrepareAndInsertPrepared(t *testing.T) {
	var mu sync.Mutex
	var seenEncoding, seenContentType, seenToken string
	var seenCompressed, seenUncompressed []byte
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		compressed, uncompressed := readClickHouseRequestBody(t, r)
		mu.Lock()
		seenEncoding = r.Header.Get("Content-Encoding")
		seenContentType = r.Header.Get("Content-Type")
		seenToken = r.URL.Query().Get("insert_deduplication_token")
		seenCompressed = compressed
		seenUncompressed = uncompressed
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	rows := make([]HistoryObservation, 16)
	for i := range rows {
		rows[i] = HistoryObservation{
			ObservationID:    fmt.Sprintf("018f2e00-9100-7000-8000-%012d", i+1),
			SourceID:         sourceA,
			SourceEventID:    fmt.Sprintf("018f2e00-6200-7000-8000-%012d", i+1),
			SourcePartition:  "tb-a",
			SourceOffset:     int64(i + 1),
			SourcePath:       "POLL",
			TelemetryKey:     "chiller.power",
			SampledAt:        time.Date(2026, 7, 29, 8, 0, i, 0, time.UTC),
			ReceivedAt:       time.Date(2026, 7, 29, 8, 0, i+1, 0, time.UTC),
			AcceptanceStatus: "REJECTED",
			Quality:          "INVALID",
			QualityReasons:   []string{"OUT_OF_RANGE"},
			PayloadSHA256:    strings.Repeat("a", 64),
		}
	}

	compressed, token, err := sink.PrepareObservations(7, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) == 0 || len(token) != 64 {
		t.Fatalf("unexpected prepared batch len=%d token=%q", len(compressed), token)
	}
	if err := sink.InsertPrepared(t.Context(), compressed, token); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d, want 1", requests.Load())
	}
	mu.Lock()
	gotEncoding := seenEncoding
	gotContentType := seenContentType
	gotToken := seenToken
	gotCompressed := slices.Clone(seenCompressed)
	gotUncompressed := slices.Clone(seenUncompressed)
	mu.Unlock()
	if gotEncoding != "gzip" || gotContentType != "application/x-ndjson" {
		t.Fatalf("headers encoding=%q contentType=%q", gotEncoding, gotContentType)
	}
	if gotToken != token {
		t.Fatalf("query token=%q, want %q", gotToken, token)
	}
	wantToken := fmt.Sprintf("%x", sha256.Sum256(gotUncompressed))
	if gotToken != wantToken {
		t.Fatalf("token %q != sha256(uncompressed) %q", gotToken, wantToken)
	}
	if len(gotCompressed) >= len(gotUncompressed) {
		t.Fatalf("expected gzip compression to shrink 16-row NDJSON (%d >= %d)", len(gotCompressed), len(gotUncompressed))
	}
	dec := json.NewDecoder(bytes.NewReader(gotUncompressed))
	count := 0
	for {
		var row HistoryObservation
		if err := dec.Decode(&row); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("gunzipped NDJSON invalid: %v", err)
		}
		if row.HistorySequence != 7 {
			t.Fatalf("HistorySequence=%d, want 7", row.HistorySequence)
		}
		count++
	}
	if count != len(rows) {
		t.Fatalf("decoded rows=%d, want %d", count, len(rows))
	}

	// Retry sends identical token and valid gzipped NDJSON.
	if err := sink.InsertPrepared(t.Context(), compressed, token); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	retryToken := seenToken
	mu.Unlock()
	if retryToken != token {
		t.Fatalf("retry changed token: %q != %q", retryToken, token)
	}
}

func TestClickHouseHistorySinkFoldsDuplicatePointSampleWithinBatch(t *testing.T) {
	var decodedRows []HistoryObservation
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, decoded := readClickHouseRequestBody(t, request)
		decoder := json.NewDecoder(bytes.NewReader(decoded))
		for {
			var row HistoryObservation
			if err := decoder.Decode(&row); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			decodedRows = append(decodedRows, row)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	tenantID := "018f2d00-0000-7000-8000-000000000001"
	siteID := "018f2e00-1000-7000-8000-000000000001"
	deviceID := "018f2e00-3000-7000-8000-000000000001"
	pointID := "018f2e00-3100-7000-8000-000000000001"
	pointType := "TELEMETRY"
	rev := int64(1)
	vType := "NUMBER"
	v1, v2, v3 := 10.0, 20.0, 30.0
	sampledAt := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	observations := []HistoryObservation{
		{
			ObservationID: "018f2e00-9100-7000-8000-000000000001", TenantID: &tenantID, SiteID: &siteID,
			DeviceID: &deviceID, PointID: &pointID, SourceID: sourceA, SourceEventID: "018f2e00-6200-7000-8000-000000000001",
			SourcePartition: "p-a", SourceOffset: 1, SourcePath: "PUSH", TelemetryKey: "chiller.power",
			PointType: &pointType, PointRevision: &rev, ValueType: &vType, ValueNumber: &v1,
			SampledAt: sampledAt, ReceivedAt: sampledAt.Add(time.Second), AcceptanceStatus: "ACCEPTED",
			Quality: "GOOD", QualityReasons: []string{}, PayloadSHA256: strings.Repeat("a", 64),
		},
		{
			ObservationID: "018f2e00-9100-7000-8000-000000000002", TenantID: &tenantID, SiteID: &siteID,
			DeviceID: &deviceID, PointID: &pointID, SourceID: sourceA, SourceEventID: "018f2e00-6200-7000-8000-000000000002",
			SourcePartition: "p-replay", SourceOffset: 2, SourcePath: "HISTORY_REPLAY", TelemetryKey: "chiller.power",
			PointType: &pointType, PointRevision: &rev, ValueType: &vType, ValueNumber: &v2,
			SampledAt: sampledAt, ReceivedAt: sampledAt.Add(2 * time.Second), AcceptanceStatus: "OUT_OF_ORDER",
			Quality: "GOOD", QualityReasons: []string{"OUT_OF_ORDER"}, PayloadSHA256: strings.Repeat("b", 64),
		},
		{
			ObservationID: "018f2e00-9100-7000-8000-000000000003", TenantID: &tenantID, SiteID: &siteID,
			DeviceID: &deviceID, PointID: &pointID, SourceID: sourceA, SourceEventID: "018f2e00-6200-7000-8000-000000000003",
			SourcePartition: "p-a", SourceOffset: 3, SourcePath: "PUSH", TelemetryKey: "chiller.power",
			PointType: &pointType, PointRevision: &rev, ValueType: &vType, ValueNumber: &v3,
			SampledAt: sampledAt.Add(time.Minute), ReceivedAt: sampledAt.Add(61 * time.Second), AcceptanceStatus: "ACCEPTED",
			Quality: "GOOD", QualityReasons: []string{}, PayloadSHA256: strings.Repeat("c", 64),
		},
	}

	if err := sink.InsertObservations(t.Context(), 5, observations); err != nil {
		t.Fatal(err)
	}
	if len(decodedRows) != 2 {
		t.Fatalf("expected 2 deduplicated observations, got %d (%+v)", len(decodedRows), decodedRows)
	}
	if decodedRows[0].ObservationID != "018f2e00-9100-7000-8000-000000000002" || decodedRows[1].ObservationID != "018f2e00-9100-7000-8000-000000000003" {
		t.Fatalf("unexpected winning rows: %+v", decodedRows)
	}
}

func TestClickHouseHistorySinkSupports32KBatchAndTooManyPartsBackoff(t *testing.T) {
	var seenParams url.Values
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenParams = request.URL.Query()
		if seenParams.Get("insert_deduplication_token") == "fail_too_many_parts" {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, "Code: 252. DB::Exception: Too many parts (301) in partition '202608'. (TOO_MANY_PARTS)")
			return
		}
		if strings.Contains(request.Header.Get("User-Agent"), "") && seenParams.Get("max_insert_block_size") == "" {
			http.Error(writer, "missing block size", http.StatusBadRequest)
			return
		}
		if request.URL.Query().Get("insert_deduplication_token") != "" && seenParams.Get("min_insert_block_size_bytes") != "33554432" {
			http.Error(writer, "bad min bytes", http.StatusBadRequest)
			return
		}
		http.Error(writer, "Code: 252. DB::Exception: Too many parts (301). (TOO_MANY_PARTS)", http.StatusBadRequest)
	}))
	defer server.Close()

	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL:    server.URL,
		Database:   "telemetry_history",
		Table:      "observations",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	rows := make([]HistoryObservation, 5000)
	digest := strings.Repeat("a", 64)
	for i := range rows {
		rows[i] = HistoryObservation{
			ObservationID: fmt.Sprintf("018f2e00-9100-7000-8000-%012d", i+1),
			PayloadSHA256: digest,
		}
	}
	compressed, token, err := sink.PrepareObservations(1, rows)
	if err != nil || len(compressed) == 0 || len(token) != 64 {
		t.Fatalf("PrepareObservations(5000 rows) err=%v len=%d token=%q", err, len(compressed), token)
	}
	tooMany := make([]HistoryObservation, maxHistoryBatchRows+1)
	if _, _, err := sink.PrepareObservations(1, tooMany); err == nil {
		t.Fatal("expected PrepareObservations to reject 32769 rows")
	}

	err = sink.InsertPrepared(t.Context(), compressed, token)
	if !IsClickHouseTooManyPartsError(err) || IsPermanentHistoryError(err) {
		t.Fatalf("TOO_MANY_PARTS must classify as transient backoff, not permanent: err=%v", err)
	}
	if seenParams.Get("max_insert_block_size") != "32768" ||
		seenParams.Get("min_insert_block_size_rows") != "32768" ||
		seenParams.Get("min_insert_block_size_bytes") != "33554432" {
		t.Fatalf("unexpected insert block params: %v", seenParams)
	}
}
