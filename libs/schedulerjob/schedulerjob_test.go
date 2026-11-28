package schedulerjob

import (
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

func TestUUIDv7GeneratesValidTimestampedIdentifier(t *testing.T) {
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	id, err := UUIDv7(at)
	if err != nil {
		t.Fatalf("UUIDv7 failed: %v", err)
	}
	if !uuidv7.IsValid(id) {
		t.Fatalf("UUIDv7 returned invalid id %q", id)
	}
}

func TestRetryDelayBoundsAcrossAttempts(t *testing.T) {
	expectedBases := map[int]time.Duration{
		0: 5 * time.Second,
		1: 5 * time.Second,
		2: 30 * time.Second,
		3: 2 * time.Minute,
		4: 10 * time.Minute,
		5: 30 * time.Minute,
		6: 30 * time.Minute,
	}
	for attempt, base := range expectedBases {
		minDelay := time.Duration(float64(base) * 0.8)
		maxDelay := time.Duration(float64(base) * 1.2)
		for i := 0; i < 20; i++ {
			got := RetryDelay(attempt)
			if got < minDelay || got > maxDelay {
				t.Fatalf("RetryDelay(%d) = %v outside [%v, %v]", attempt, got, minDelay, maxDelay)
			}
		}
	}
}
