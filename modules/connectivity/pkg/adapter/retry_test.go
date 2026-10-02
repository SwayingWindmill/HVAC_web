package adapter

import (
	"errors"
	"testing"
	"time"
)

func TestPermanentMessageClassification(t *testing.T) {
	plain := errors.New("dependency unavailable")
	if isPermanentMessageError(plain) {
		t.Fatal("plain dependency error was classified as permanent")
	}
	permanent := permanentMessage(errors.New("invalid envelope"))
	if !isPermanentMessageError(permanent) || permanent.Error() != "invalid envelope" {
		t.Fatalf("permanent error=%v", permanent)
	}
}

func TestMQTTRetryDelayIsBounded(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 500 * time.Millisecond},
		{attempt: 1, want: 500 * time.Millisecond},
		{attempt: 2, want: time.Second},
		{attempt: 5, want: 8 * time.Second},
		{attempt: 6, want: 10 * time.Second},
		{attempt: 100, want: 10 * time.Second},
	}
	for _, test := range cases {
		if got := mqttRetryDelay(test.attempt); got != test.want {
			t.Fatalf("attempt %d delay=%s want=%s", test.attempt, got, test.want)
		}
	}
}
