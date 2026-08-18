package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrLoginStateNotFound = errors.New("OIDC login state not found")
	ErrLoginStateConflict = errors.New("OIDC login state already exists")
)

const defaultLoginStateKeyPrefix = "hvac:v1:oidc:state"

type LoginStateStore interface {
	Put(context.Context, string, loginState, time.Duration) error
	Consume(context.Context, string) (loginState, error)
}

type RedisLoginStateStoreConfig struct {
	URL       string
	KeyPrefix string
}

type RedisLoginStateStore struct {
	client    *redis.Client
	keyPrefix string
}

func OpenRedisLoginStateStore(ctx context.Context, config RedisLoginStateStoreConfig) (*RedisLoginStateStore, error) {
	urlValue := strings.TrimSpace(config.URL)
	if urlValue == "" {
		return nil, errors.New("OIDC state Redis URL is required")
	}
	options, err := redis.ParseURL(urlValue)
	if err != nil {
		return nil, fmt.Errorf("parse OIDC state Redis URL: %w", err)
	}
	options.ClientName = "platform-gateway-oidc-state"
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping OIDC state Redis: %w", err)
	}
	prefix := strings.TrimSpace(config.KeyPrefix)
	if prefix == "" {
		prefix = defaultLoginStateKeyPrefix
	}
	return &RedisLoginStateStore{client: client, keyPrefix: strings.TrimSuffix(prefix, ":")}, nil
}

func (store *RedisLoginStateStore) Close() error {
	if store == nil || store.client == nil {
		return nil
	}
	return store.client.Close()
}

func (store *RedisLoginStateStore) Ping(ctx context.Context) error {
	if store == nil || store.client == nil {
		return errors.New("OIDC state Redis is unavailable")
	}
	return store.client.Ping(ctx).Err()
}

func (store *RedisLoginStateStore) Put(ctx context.Context, state string, value loginState, ttl time.Duration) error {
	if store == nil || store.client == nil || strings.TrimSpace(state) == "" || ttl <= 0 {
		return errors.New("OIDC login state store input is invalid")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode OIDC login state: %w", err)
	}
	stored, err := store.client.SetNX(ctx, store.key(state), encoded, ttl).Result()
	if err != nil {
		return fmt.Errorf("persist OIDC login state: %w", err)
	}
	if !stored {
		return ErrLoginStateConflict
	}
	return nil
}

func (store *RedisLoginStateStore) Consume(ctx context.Context, state string) (loginState, error) {
	if store == nil || store.client == nil || strings.TrimSpace(state) == "" {
		return loginState{}, ErrLoginStateNotFound
	}
	encoded, err := store.client.GetDel(ctx, store.key(state)).Bytes()
	if errors.Is(err, redis.Nil) {
		return loginState{}, ErrLoginStateNotFound
	}
	if err != nil {
		return loginState{}, fmt.Errorf("consume OIDC login state: %w", err)
	}
	var value loginState
	if err := json.Unmarshal(encoded, &value); err != nil {
		return loginState{}, fmt.Errorf("decode OIDC login state: %w", err)
	}
	return value, nil
}

func (store *RedisLoginStateStore) key(state string) string {
	return store.keyPrefix + ":" + state
}
