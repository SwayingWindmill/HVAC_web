package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

// A History batch holding many minutes of a meter becomes one fact per interval, however the
// projector pages through it. Telemetry Runtime's UUIDv7 ids are not in ClickHouse's UUID order,
// so paging must follow that order, not the ids' text.
func TestProjectorWritesEveryIntervalOfALargeHistoryBatch(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_TEST_URL"))
	if baseURL == "" {
		t.Skip("ANALYTICS_CLICKHOUSE_TEST_URL is not configured")
	}
	adminUsername := envOr("ANALYTICS_CLICKHOUSE_TEST_ADMIN_USERNAME", "telemetry_history")
	adminPassword := os.Getenv("ANALYTICS_CLICKHOUSE_TEST_ADMIN_PASSWORD")
	client := &http.Client{Timeout: 30 * time.Second}
	tenantID := "018f4f00-0100-7000-8000-000000000002"
	siteID := fmt.Sprintf("018f4f00-1000-7000-8000-%012x", time.Now().UnixNano()&0xffffffffffff)
	deviceID := "018f4f00-2000-7000-8000-000000000002"
	partition := "analytics-integration-" + t.Name()
	const minutes = 180

	start := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	admitted := time.Now()
	rows := make([]map[string]any, 0, minutes)
	for minute := 0; minute < minutes; minute++ {
		sampledAt := start.Add(time.Duration(minute) * time.Minute)
		rows = append(rows, historyObservation(newTestUUIDv7(admitted.Add(time.Duration(minute)*time.Millisecond)), newTestUUIDv7(admitted),
			tenantID, siteID, deviceID, partition, uint64(sampledAt.UnixMilli()), float64(minute), sampledAt.Format("2006-01-02T15:04:05.000Z")))
	}
	insertJSONEachRow(t, client, baseURL, adminUsername, adminPassword, "telemetry_history.observations", inHistoryBatch(rows))

	reader, writer := integrationReaderWriter(t, client, baseURL)
	projector, err := energy.NewProjector(energy.ProjectorConfig{CounterSource: reader, BindingResolver: integrationBindingResolver{
		tenantID: tenantID, siteID: siteID, deviceID: deviceID,
	}, FactSink: writer, RebuildEvents: writer, BatchSize: 32, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	// Like the running projector, keep going until the checkpoint stops moving.
	for batches, checkpoint := 0, ""; ; batches++ {
		if batches > 100 {
			t.Fatal("projector did not drain the History batch")
		}
		if _, err := projector.ProjectOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		next := projectionCheckpoint(t, client, baseURL, adminUsername, adminPassword)
		if next == checkpoint {
			break
		}
		checkpoint = next
	}

	facts := strings.TrimSpace(string(executeClickHouse(t, client, baseURL, adminUsername, adminPassword, fmt.Sprintf(
		`SELECT count() FROM analytics.energy_interval_facts WHERE site_id = toUUID('%s') FORMAT TSVRaw`, siteID))))
	if facts != fmt.Sprint(minutes-1) {
		t.Fatalf("facts = %s, want one per interval (%d)", facts, minutes-1)
	}
}

// newTestUUIDv7 is a UUIDv7 stamped at the given time with random bits, like the ids Telemetry
// Runtime assigns on admission.
func newTestUUIDv7(at time.Time) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	var millis [8]byte
	binary.BigEndian.PutUint64(millis[:], uint64(at.UnixMilli()))
	copy(value[:6], millis[2:])
	value[6] = value[6]&0x0f | 0x70
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}
