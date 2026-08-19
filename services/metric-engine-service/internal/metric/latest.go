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

func NewRedisLatestStore(address, password string, database int, ttl time.Duration) (*RedisLatestStore, error) {
	if strings.TrimSpace(address) == "" {
		return nil, errors.New("metric Redis address is required")
	}
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	client := redis.NewClient(&redis.Options{Addr: strings.TrimSpace(address), Password: password, DB: database})
	return &RedisLatestStore{client: client, ttl: ttl}, nil
}

var putMetricLatestScript = redis.NewScript(`
local current = redis.call('HGET', KEYS[1], 'periodEndMs')
if current and tonumber(current) > tonumber(ARGV[1]) then
  return 0
end
redis.call('HSET', KEYS[1],
  'periodEndMs', ARGV[1],
  'calculatedAtMs', ARGV[2],
  'resultId', ARGV[3],
  'metricVersionId', ARGV[4],
  'bindingId', ARGV[5],
  'value', ARGV[6],
  'unit', ARGV[7],
  'quality', ARGV[8],
  'completeness', ARGV[9])
redis.call('PEXPIRE', KEYS[1], ARGV[10])
return 1
`)

func (store *RedisLatestStore) PutMetric(ctx context.Context, result Result) error {
	if store == nil || store.client == nil {
		return errors.New("metric Redis Latest store is unavailable")
	}
	key := fmt.Sprintf("metric:latest:%s:%s:%s:%s:%s", result.Binding.TenantID, result.Binding.SiteID, result.Binding.MetricID, result.Binding.SubjectType, result.Binding.SubjectID)
	_, err := putMetricLatestScript.Run(ctx, store.client, []string{key},
		result.PeriodEnd.UTC().UnixMilli(), result.CalculatedAt.UTC().UnixMilli(), result.ResultID,
		result.Binding.MetricVersionID, result.Binding.BindingID, strconv.FormatFloat(result.Value, 'g', -1, 64),
		result.Binding.Unit, result.Quality, strconv.FormatFloat(result.Completeness, 'g', -1, 64), store.ttl.Milliseconds(),
	).Result()
	return err
}
