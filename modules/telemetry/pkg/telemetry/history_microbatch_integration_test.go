package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHistoryMicrobatchLeaseRetryAndDeadBoundary(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	historyURL := os.Getenv("S2_TELEMETRY_HISTORY_DATABASE_URL")
	if historyURL == "" {
		t.Skip("history database not configured")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo, err := OpenHistoryPostgresRepository(ctx, historyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	peer, err := OpenHistoryPostgresRepository(ctx, historyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	const partition = "history-microbatch-lease"
	defer func() {
		_, _ = admin.Exec(ctx, `DELETE FROM telemetry_runtime.source_observations WHERE source_partition=$1`, partition)
	}()
	for i := 1; i <= 3; i++ {
		candidate := ingestCandidate(fmt.Sprintf("018f2e00-9400-7000-8000-%012d", i), sourceA, partition, int64(i), SourcePathPoll, "mqtt-device-tenant-a-site-1", "zone.temperature", json.RawMessage(`25.0`), "NUMBER", "Cel", now.Add(time.Duration(i)*time.Second), now.Add(time.Duration(i)*time.Second))
		if _, err := store.AcceptObservation(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(time.Minute)
	type result struct {
		batch HistoryBatch
		err   error
	}
	results := make(chan result, 2)
	for _, worker := range []*HistoryPostgresRepository{repo, peer} {
		go func() { b, e := worker.ClaimHistoryBatch(ctx, 2, now, 30*time.Second, 3); results <- result{b, e} }()
	}
	var first HistoryBatch
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if len(r.batch.Observations) > 0 {
			if first.BatchID != "" {
				t.Fatal("two unresolved batches were created")
			}
			first = r.batch
		}
	}
	if len(first.Observations) != 2 {
		t.Fatalf("batch size=%d", len(first.Observations))
	}
	ids := func(b HistoryBatch) []string {
		v := make([]string, len(b.Observations))
		for i, o := range b.Observations {
			v[i] = o.ObservationID
		}
		sort.Strings(v)
		return v
	}
	retryAt := now.Add(10 * time.Second)
	if err := repo.RetryHistoryBatch(ctx, first.LeaseID, retryAt, "TEST_UNCERTAIN_WRITE", 4); err != nil {
		t.Fatal(err)
	}
	early, err := peer.ClaimHistoryBatch(ctx, 1, now.Add(time.Second), 30*time.Second, 3)
	if err != nil || len(early.Observations) != 0 {
		t.Fatalf("retry delay bypassed: %+v %v", early, err)
	}
	retry, err := peer.ClaimHistoryBatch(ctx, 1, retryAt, 30*time.Second, 3)
	if err != nil || retry.BatchID != first.BatchID || !reflect.DeepEqual(ids(retry), ids(first)) {
		t.Fatalf("retry reshaped: %+v %v", retry, err)
	}
	reclaimed, err := repo.ClaimHistoryBatch(ctx, 1, retryAt.Add(31*time.Second), 30*time.Second, 3)
	if err != nil || reclaimed.BatchID != first.BatchID || !reflect.DeepEqual(ids(reclaimed), ids(first)) {
		t.Fatalf("expired lease reshaped: %+v %v", reclaimed, err)
	}
	if err := peer.MarkHistoryBatchPublished(ctx, retry.LeaseID, now); err == nil {
		t.Fatal("stale worker acknowledged current lease")
	}
	// Third attempt dies without writing Retry. Lease expiry must still exhaust it.
	if _, err := peer.ClaimHistoryBatch(ctx, 2, now.Add(time.Hour), 30*time.Second, 3); err == nil || !strings.Contains(err.Error(), "DEAD") {
		t.Fatalf("DEAD did not block channel: %v", err)
	}
	if _, err := peer.ClaimHistoryBatch(ctx, 2, now.Add(2*time.Hour), 30*time.Second, 3); err == nil || !strings.Contains(err.Error(), "DEAD") {
		t.Fatalf("persisted DEAD did not block: %v", err)
	}
	var unassigned int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.telemetry_history_outbox WHERE payload->>'source_partition'=$1 AND batch_id IS NULL`, partition).Scan(&unassigned); err != nil || unassigned != 1 {
		t.Fatalf("new data overtook uncertain batch: %d %v", unassigned, err)
	}
}

func TestHistoryMicrobatchMixedPartitionsRecoversMaterializedView(t *testing.T) {
	baseURL := os.Getenv("S2_CLICKHOUSE_HTTP_URL")
	if baseURL == "" {
		t.Skip("ClickHouse not configured")
	}
	clickHouseQuery(t, baseURL, `CREATE TABLE telemetry_history.microbatch_raw AS telemetry_history.observations`)
	clickHouseQuery(t, baseURL, `CREATE TABLE telemetry_history.microbatch_hourly AS telemetry_history.numeric_hourly_states`)
	ddl := clickHouseQuery(t, baseURL, `SHOW CREATE TABLE telemetry_history.observations_to_numeric_hourly FORMAT TSVRaw`)
	ddl = strings.ReplaceAll(ddl, "observations_to_numeric_hourly", "microbatch_mv")
	ddl = strings.ReplaceAll(ddl, "numeric_hourly_states", "microbatch_hourly")
	ddl = strings.ReplaceAll(ddl, "telemetry_history.observations", "telemetry_history.microbatch_raw")
	clickHouseQuery(t, baseURL, ddl)
	defer func() {
		clickHouseQuery(t, baseURL, `DROP TABLE telemetry_history.microbatch_mv`)
		clickHouseQuery(t, baseURL, `DROP TABLE telemetry_history.microbatch_raw`)
		clickHouseQuery(t, baseURL, `DROP TABLE telemetry_history.microbatch_hourly`)
	}()
	sink, err := NewClickHouseHistorySink(ClickHouseHistoryConfig{BaseURL: baseURL, Database: "telemetry_history", Table: "microbatch_raw", Username: os.Getenv("S2_CLICKHOUSE_USERNAME"), Password: os.Getenv("S2_CLICKHOUSE_PASSWORD")})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]HistoryObservation, 2)
	revision := int64(1)
	for i := range rows {
		value := float64(10 + 20*i)
		rows[i] = HistoryObservation{ObservationID: fmt.Sprintf("018f2e00-9500-7000-8000-%012d", i), TenantID: stringPointer(tenantA), SiteID: stringPointer(siteA), DeviceID: stringPointer(deviceA), PointID: stringPointer("018f2e00-3100-7000-8000-000000000001"), SourceID: sourceA, SourceEventID: fmt.Sprintf("018f2e00-9600-7000-8000-%012d", i), SourcePartition: "microbatch-mv", SourceOffset: int64(i), SourcePath: "POLL", TelemetryKey: "temperature", PointType: stringPointer("TELEMETRY"), PointRevision: &revision, ValueType: stringPointer("NUMBER"), ValueNumber: &value, SampledAt: time.Date(2026, 7+time.Month(i), 1, 0, 0, 0, 0, time.UTC), ReceivedAt: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), AcceptanceStatus: "ACCEPTED", Quality: "GOOD", QualityReasons: []string{}, PayloadSHA256: strings.Repeat("a", 64)}
	}
	clickHouseQuery(t, baseURL, `ALTER TABLE telemetry_history.microbatch_hourly ADD CONSTRAINT injected_failure CHECK 0`)
	if err := sink.InsertObservations(t.Context(), 1, rows); err == nil {
		t.Fatal("MV failure was acknowledged as success")
	}
	if count := clickHouseQuery(t, baseURL, `SELECT count() FROM telemetry_history.microbatch_raw FORMAT TSVRaw`); count == "0" {
		t.Fatal("fault injection did not exercise a partially committed insert")
	}
	clickHouseQuery(t, baseURL, `ALTER TABLE telemetry_history.microbatch_hourly DROP CONSTRAINT injected_failure`)
	errors := make(chan error, 2)
	for range 2 {
		go func() { errors <- sink.InsertObservations(t.Context(), 1, rows) }()
	}
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if actual := clickHouseQuery(t, baseURL, `SELECT count(),uniqExact(observation_id),sum(value_number) FROM telemetry_history.microbatch_raw FORMAT TSVRaw`); actual != "2\t2\t40" {
		t.Fatalf("raw after recovery=%s", actual)
	}
	if actual := clickHouseQuery(t, baseURL, `SELECT countMerge(sample_count),avgMerge(average_value) FROM telemetry_history.microbatch_hourly FORMAT TSVRaw`); actual != "2\t20" {
		t.Fatalf("hourly after recovery=%s", actual)
	}
}
