package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type OperationsRateLimiter interface {
	Allow(context.Context, string) (bool, error)
}

type operationsLimitPolicyFile struct {
	SchemaVersion int                      `json:"schemaVersion"`
	PolicyID      string                   `json:"policyId"`
	Revision      int64                    `json:"revision"`
	Limits        operationsLimitPolicySet `json:"limits"`
}

type operationsLimitPolicySet struct {
	OperationsAgent operationsLimitPolicy `json:"operationsAgent"`
}

type operationsLimitPolicy struct {
	Backend       string `json:"backend"`
	FailureMode   string `json:"failureMode"`
	Scope         string `json:"scope"`
	WindowSeconds int64  `json:"windowSeconds"`
	MaxRequests   int64  `json:"maxRequests"`
	KeyPrefix     string `json:"keyPrefix"`
}

type RedisOperationsRateLimiter struct {
	client      *redis.Client
	policyID    string
	revision    int64
	window      time.Duration
	maxRequests int64
	keyPrefix   string
}

const operationsRateLimitScript = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return count
`

func OpenRedisOperationsRateLimiter(ctx context.Context, policyPath, redisURL string) (*RedisOperationsRateLimiter, error) {
	policy, err := loadOperationsLimitPolicy(policyPath)
	if err != nil {
		return nil, err
	}
	urlValue := strings.TrimSpace(redisURL)
	if urlValue == "" {
		return nil, errors.New("Operations limit Redis URL is required")
	}
	options, err := redis.ParseURL(urlValue)
	if err != nil {
		return nil, fmt.Errorf("parse Operations limit Redis URL: %w", err)
	}
	options.ClientName = "platform-gateway-operations-limit"
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping Operations limit Redis: %w", err)
	}
	return &RedisOperationsRateLimiter{
		client:      client,
		policyID:    policy.PolicyID,
		revision:    policy.Revision,
		window:      time.Duration(policy.Limits.OperationsAgent.WindowSeconds) * time.Second,
		maxRequests: policy.Limits.OperationsAgent.MaxRequests,
		keyPrefix:   policy.Limits.OperationsAgent.KeyPrefix,
	}, nil
}

func loadOperationsLimitPolicy(path string) (operationsLimitPolicyFile, error) {
	policyPath := strings.TrimSpace(path)
	if policyPath == "" {
		return operationsLimitPolicyFile{}, errors.New("Operations limit policy file is required")
	}
	file, err := os.Open(policyPath)
	if err != nil {
		return operationsLimitPolicyFile{}, fmt.Errorf("open Operations limit policy: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var policy operationsLimitPolicyFile
	if err := decoder.Decode(&policy); err != nil {
		return operationsLimitPolicyFile{}, fmt.Errorf("decode Operations limit policy: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return operationsLimitPolicyFile{}, err
	}
	limit := policy.Limits.OperationsAgent
	if policy.SchemaVersion != 1 || strings.TrimSpace(policy.PolicyID) == "" || policy.Revision < 1 {
		return operationsLimitPolicyFile{}, errors.New("Operations limit policy identity is invalid")
	}
	if limit.Backend != "redis" || limit.FailureMode != "fail-closed" || limit.Scope != "session" {
		return operationsLimitPolicyFile{}, errors.New("Operations limit policy mode is invalid")
	}
	if limit.WindowSeconds < 1 || limit.MaxRequests < 1 || strings.TrimSpace(limit.KeyPrefix) == "" {
		return operationsLimitPolicyFile{}, errors.New("Operations limit policy bounds are invalid")
	}
	return policy, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("Operations limit policy contains multiple JSON values")
		}
		return fmt.Errorf("decode Operations limit policy trailing data: %w", err)
	}
	return nil
}

func (limiter *RedisOperationsRateLimiter) Allow(ctx context.Context, sessionID string) (bool, error) {
	if limiter == nil || limiter.client == nil {
		return false, errors.New("Operations limit backend is unavailable")
	}
	session := strings.TrimSpace(sessionID)
	if session == "" {
		return false, errors.New("Operations limit session is missing")
	}
	digest := sha256.Sum256([]byte(session))
	key := fmt.Sprintf("%s:v%d:%s", strings.TrimSuffix(limiter.keyPrefix, ":"), limiter.revision, hex.EncodeToString(digest[:]))
	count, err := limiter.client.Eval(ctx, operationsRateLimitScript, []string{key}, limiter.window.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("reserve Operations limit policy %s revision %d: %w", limiter.policyID, limiter.revision, err)
	}
	return count <= limiter.maxRequests, nil
}

func (limiter *RedisOperationsRateLimiter) Ping(ctx context.Context) error {
	if limiter == nil || limiter.client == nil {
		return errors.New("Operations limit backend is unavailable")
	}
	return limiter.client.Ping(ctx).Err()
}

func (limiter *RedisOperationsRateLimiter) Close() error {
	if limiter == nil || limiter.client == nil {
		return nil
	}
	return limiter.client.Close()
}
