package metric

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// An Energy Interval Fact ending at a reading covers the step from the reading before it, and a
// window's facts are those ending inside it. A counter's window delta takes the same steps: the
// one from the last reading before the window to the first inside it belongs to the window, so
// consecutive windows add up to the meter's whole advance.
func TestCounterDeltaTakesTheStepIntoTheWindow(t *testing.T) {
	baseURL := os.Getenv("METRIC_CLICKHOUSE_TEST_URL")
	if baseURL == "" {
		t.Skip("METRIC_CLICKHOUSE_TEST_URL is not set")
	}
	tenant, site, device, point := newUUID(t), newUUID(t), newUUID(t), newUUID(t)
	// One reading a minute from 09:00, counting one kWh a minute.
	insert := fmt.Sprintf(`INSERT INTO telemetry_history.observations
 (observation_id, tenant_id, site_id, device_id, point_id, source_event_id, sampled_at, received_at, acceptance_status, quality, value_number)
SELECT generateUUIDv4(), toUUID('%[1]s'), toUUID('%[2]s'), toUUID('%[3]s'), toUUID('%[4]s'), generateUUIDv4(),
 toDateTime64('2026-09-14 09:00:00', 3, 'UTC') + number * 60, toDateTime64('2026-09-14 09:00:00', 3, 'UTC') + number * 60,
 'ACCEPTED', 'GOOD', toFloat64(number)
FROM numbers(151)`, tenant, site, device, point)
	request, err := http.NewRequest(http.MethodPost, baseURL, strings.NewReader(insert))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth(os.Getenv("METRIC_CLICKHOUSE_TEST_ADMIN_USERNAME"), "")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("insert readings: %d %s", response.StatusCode, body)
	}

	store, err := NewClickHouseStore(baseURL, "metric_engine_runtime", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{TenantID: tenant, SiteID: site, Aggregation: "LAST", CalculationMethod: "DELTA", SourceDefinition: map[string]any{}}
	delta := func(start, end time.Time) float64 {
		t.Helper()
		input, err := store.ReadPoint(context.Background(), binding, point, start, end)
		if err != nil {
			t.Fatal(err)
		}
		value, err := counterDelta(binding, input)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	ten := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	if hour := delta(ten, ten.Add(time.Hour)); math.Abs(hour-60) > 1e-9 {
		t.Fatalf("10:00–11:00 delta = %v kWh, want the 60 kWh of the readings ending 10:00 through 10:59", hour)
	}
	if halves := delta(ten, ten.Add(30*time.Minute)) + delta(ten.Add(30*time.Minute), ten.Add(time.Hour)); math.Abs(halves-60) > 1e-9 {
		t.Fatalf("two half hours add to %v kWh, want the hour's 60", halves)
	}
}

func newUUID(t *testing.T) string {
	t.Helper()
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatal(err)
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:])
}
