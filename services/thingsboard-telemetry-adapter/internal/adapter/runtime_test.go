package adapter

import (
	"testing"
	"time"
)

func TestPollTimeoutAllowsFullDeliveryWorkBeyondSampleInterval(t *testing.T) {
	if got := pollTimeoutFor(5 * time.Second); got != 30*time.Second {
		t.Fatalf("five-second polling received timeout %s", got)
	}
	if got := pollTimeoutFor(time.Minute); got != 5*time.Minute {
		t.Fatalf("one-minute polling received timeout %s", got)
	}
}
