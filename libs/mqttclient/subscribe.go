package mqttclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/eclipse/paho.golang/paho"
)

const (
	// DefaultSubscribeAttemptTimeout bounds a single MQTT SUBSCRIBE/SUBACK exchange.
	DefaultSubscribeAttemptTimeout = 15 * time.Second
)

// DefaultSubscribeRetryDelays defines the default backoff delays between retry attempts
// when a SUBSCRIBE exchange fails or returns a broker rejection reason code.
var DefaultSubscribeRetryDelays = []time.Duration{
	200 * time.Millisecond,
	600 * time.Millisecond,
}

// Subscriber issues an MQTT SUBSCRIBE packet and waits for SUBACK.
type Subscriber interface {
	Subscribe(ctx context.Context, s *paho.Subscribe) (*paho.Suback, error)
}

// IsClosedSignal reports whether closed has already been signaled without blocking.
func IsClosedSignal(closed <-chan struct{}) bool {
	if closed == nil {
		return false
	}
	select {
	case <-closed:
		return true
	default:
		return false
	}
}

// CheckSubackReasons validates that SUBACK is present, covers at least expectedCount
// topic filters, and contains only MQTT 5.0 success reason codes (< 0x80).
func CheckSubackReasons(suback *paho.Suback, expectedCount int) error {
	if suback == nil {
		return errors.New("mqtt subscribe failed: missing SUBACK")
	}
	if expectedCount > 0 && len(suback.Reasons) != expectedCount {
		return fmt.Errorf("mqtt subscribe failed: SUBACK returned %d reason codes for %d subscriptions", len(suback.Reasons), expectedCount)
	}
	for index, reason := range suback.Reasons {
		if reason >= 0x80 {
			return fmt.Errorf("mqtt subscribe rejected topic filter %d with SUBACK reason code 0x%02x", index, reason)
		}
	}
	return nil
}

// SubscribeWithRetry attempts SUBSCRIBE with SUBACK (< 0x80) verification, retrying
// according to retryDelays unless ctx is canceled or isConnectionClosed returns true.
// When retryDelays is nil, DefaultSubscribeRetryDelays is used; pass an empty non-nil
// slice to disable retries.
func SubscribeWithRetry(
	ctx context.Context,
	subscriber Subscriber,
	sub *paho.Subscribe,
	attemptTimeout time.Duration,
	retryDelays []time.Duration,
	isConnectionClosed func() bool,
) error {
	if subscriber == nil {
		return errors.New("mqtt subscriber is nil")
	}
	if sub == nil || len(sub.Subscriptions) == 0 {
		return nil
	}
	if attemptTimeout <= 0 {
		attemptTimeout = DefaultSubscribeAttemptTimeout
	}
	if retryDelays == nil {
		retryDelays = DefaultSubscribeRetryDelays
	}

	var lastErr error
	totalAttempts := len(retryDelays) + 1
	for attempt := 0; attempt < totalAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if isConnectionClosed != nil && isConnectionClosed() {
			if lastErr != nil {
				return lastErr
			}
			return errors.New("mqtt connection closed before subscribe")
		}

		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		suback, err := subscriber.Subscribe(attemptCtx, sub)
		cancel()
		if err == nil {
			err = CheckSubackReasons(suback, len(sub.Subscriptions))
		}
		if err == nil {
			return nil
		}
		lastErr = err

		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if isConnectionClosed != nil && isConnectionClosed() {
			return lastErr
		}
		if attempt >= len(retryDelays) {
			break
		}

		delay := retryDelays[attempt]
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return lastErr
}
