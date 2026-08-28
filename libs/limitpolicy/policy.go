// Package limitpolicy implements versioned, per-tenant/per-entity rate limits.
// The taxonomy is adopted from ThingsBoard's LimitedApi model (resource class
// x granularity), but the implementation is not: limits run against a swappable
// Counter backend so a single-node deployment can start local and move to a
// shared backend without changing call sites. A counter that cannot be
// evaluated fails closed or open per-limit, never silently.
package limitpolicy

import "time"

// Dimension is the resource class being limited.
type Dimension string

const (
	DimensionRest              Dimension = "rest"
	DimensionTelemetryIngest   Dimension = "telemetry-ingest"
	DimensionCommandWrite      Dimension = "command-write"
	DimensionRealtimeSubscribe Dimension = "realtime-subscription"
	DimensionLoginToken        Dimension = "login-token"
	DimensionExpensiveQuery    Dimension = "expensive-query"
	DimensionNotification      Dimension = "notification"
	DimensionOperationsAgent   Dimension = "operations-agent"
)

// Limit is the budget for one dimension. Window and Burst define a token
// bucket: at most Burst requests per Window, refilled continuously.
type Limit struct {
	Dimension Dimension
	Window    time.Duration
	Burst     int
	// FailClosed means a request is rejected when the counter backend cannot be
	// evaluated. High-risk dimensions (control writes, identity, secrets) must
	// set this; low-risk reads may leave it false to fail open on backend loss.
	FailClosed bool
}

func (limit Limit) valid() bool {
	return limit.Dimension != "" && limit.Window > 0 && limit.Burst > 0
}

// Policy is a versioned set of limits. Version changes allow callers to detect
// that a policy was replaced and, for example, invalidate local state.
type Policy struct {
	Version int
	Limits  []Limit
}

func (policy *Policy) limitFor(dimension Dimension) (Limit, bool) {
	for _, limit := range policy.Limits {
		if limit.Dimension == dimension {
			return limit, true
		}
	}
	return Limit{}, false
}

// Decision is the outcome of an Allow check.
type Decision struct {
	Allowed bool
	// Reason is one of "allowed", "unlimited", "no-policy", "limited",
	// "counter-unavailable", or "counter-degraded".
	Reason     string
	RetryAfter time.Duration
}
