package scheduler

import (
	"context"
	"time"
)

type QueueStats struct {
	Ready                 int
	RetryWait             int
	Running               int
	OldestReadyAgeSeconds float64
}

func (s *Store) QueueStats(ctx context.Context, now time.Time) (QueueStats, error) {
	var stats QueueStats
	err := s.pool.QueryRow(ctx, `SELECT
COUNT(*) FILTER (WHERE state='READY')::int,
COUNT(*) FILTER (WHERE state='RETRY_WAIT')::int,
COUNT(*) FILTER (WHERE state IN ('CLAIMED','RUNNING'))::int,
COALESCE(EXTRACT(EPOCH FROM ($1-MIN(scheduled_for) FILTER (WHERE state='READY'))),0)::float8
FROM core_registry.job_instances`, now.UTC()).Scan(&stats.Ready, &stats.RetryWait, &stats.Running, &stats.OldestReadyAgeSeconds)
	if stats.OldestReadyAgeSeconds < 0 {
		stats.OldestReadyAgeSeconds = 0
	}
	return stats, err
}
