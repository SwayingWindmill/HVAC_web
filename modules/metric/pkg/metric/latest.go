package metric

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type LatestStore interface {
	PutMetric(context.Context, Result) error
}

type RedisLatestStore struct {
	client *redis.Client
	ttl    time.Duration
}

func (store *RedisLatestStore) Close() error {
	if store == nil || store.client == nil {
		return nil
	}
	return store.client.Close()
}

func (store *RedisLatestStore) Ping(ctx context.Context) error {
	return store.client.Ping(ctx).Err()
}

func NewRedisLatestStore(rawURL string, ttl time.Duration) (*RedisLatestStore, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("metric Redis URL is required")
	}
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	options, err := redis.ParseURL(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse metric Redis URL: %w", err)
	}
	client := redis.NewClient(options)
	return &RedisLatestStore{client: client, ttl: ttl}, nil
}

var putMetricLatestScript = redis.NewScript(`
local currentPeriodEnd = redis.call('HGET', KEYS[1], 'periodEndMs')
local currentRevision = redis.call('HGET', KEYS[1], 'revision')
if currentPeriodEnd and tonumber(currentPeriodEnd) > tonumber(ARGV[1]) then
  return 0
end
if currentPeriodEnd and tonumber(currentPeriodEnd) == tonumber(ARGV[1]) and currentRevision and tonumber(currentRevision) >= tonumber(ARGV[2]) then
  return 0
end
redis.call('HSET', KEYS[1],
  'periodEndMs', ARGV[1],
  'revision', ARGV[2],
  'calculatedAtMs', ARGV[3],
  'resultId', ARGV[4],
  'metricVersionId', ARGV[5],
  'bindingId', ARGV[6],
  'value', ARGV[7],
  'unit', ARGV[8],
  'quality', ARGV[9],
  'completeness', ARGV[10])
redis.call('PEXPIRE', KEYS[1], ARGV[11])
return 1
`)

func (store *RedisLatestStore) PutMetric(ctx context.Context, result Result) error {
	if store == nil || store.client == nil {
		return errors.New("metric Redis Latest store is unavailable")
	}
	key := fmt.Sprintf("metric:latest:%s:%s:%s:%s:%s", result.Binding.TenantID, result.Binding.SiteID, result.Binding.MetricID, result.Binding.SubjectType, result.Binding.SubjectID)
	_, err := putMetricLatestScript.Run(ctx, store.client, []string{key},
		result.PeriodEnd.UTC().UnixMilli(), result.Revision, result.CalculatedAt.UTC().UnixMilli(), result.ResultID,
		result.Binding.MetricVersionID, result.Binding.BindingID, strconv.FormatFloat(result.Value, 'g', -1, 64),
		result.Binding.Unit, result.Quality, strconv.FormatFloat(result.Completeness, 'g', -1, 64), store.ttl.Milliseconds(),
	).Result()
	return err
}
