// Package ratelimit provides rate limiting via Redis.
package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter enforces rate limits on API key usage.
type Limiter interface {
	Allow(ctx context.Context, keyID string, rpm, tpm int32) (bool, time.Duration, error)
	RecordTokens(ctx context.Context, keyID string, tokens int32) error
}

// RedisLimiter implements Limiter using a Redis sliding window counter.
type RedisLimiter struct {
	client *redis.Client
}

// NewRedisLimiter creates a new Redis-backed rate limiter.
func NewRedisLimiter(addr string) (Limiter, error) {
	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	// Test the connection.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}

	return &RedisLimiter{client: client}, nil
}

// luaScript implements sliding-window rate limiting for both RPM and TPM.
// Returns [0] = 1 if allowed, 0 if denied, [1] = milliseconds until oldest request expires.
const luaScript = `
local rpmKey = KEYS[1]
local tpmKey = KEYS[2]
local now = tonumber(ARGV[1])
local rpmLimit = tonumber(ARGV[2])
local tpmLimit = tonumber(ARGV[3])
local window = 60000  -- 60 seconds in milliseconds

-- Clean up old entries (older than the window).
redis.call('ZREMRANGEBYSCORE', rpmKey, '-inf', now - window)
redis.call('ZREMRANGEBYSCORE', tpmKey, '-inf', now - window)

-- Count requests in current RPM window.
local rpmCount = redis.call('ZCARD', rpmKey)

-- Check RPM limit.
if rpmCount >= rpmLimit then
	local oldestRPM = redis.call('ZRANGE', rpmKey, 0, 0, 'WITHSCORES')
	if #oldestRPM > 0 then
		local oldestTs = tonumber(oldestRPM[2])
		local retryMs = (oldestTs + window) - now
		return {0, retryMs}
	end
	return {0, window}
end

-- Sum actual token counts from members (format: "<ts>:<tokens>:<nonce>").
local tpmMembers = redis.call('ZRANGE', tpmKey, 0, -1)
local tpmTotal = 0
for _, member in ipairs(tpmMembers) do
	local first = string.find(member, ':')
	if first then
		local second = string.find(member, ':', first + 1)
		if second then
			local tokenStr = string.sub(member, first + 1, second - 1)
			tpmTotal = tpmTotal + (tonumber(tokenStr) or 0)
		end
	end
end

-- Check TPM limit.
if tpmTotal >= tpmLimit then
	local oldestTPM = redis.call('ZRANGE', tpmKey, 0, 0, 'WITHSCORES')
	if #oldestTPM > 0 then
		local oldestTs = tonumber(oldestTPM[2])
		local retryMs = (oldestTs + window) - now
		return {0, retryMs}
	end
	return {0, window}
end

-- Add current request to RPM window.
redis.call('ZADD', rpmKey, now, now)

-- Set expiry to 65 seconds (window + 5 second buffer).
redis.call('EXPIRE', rpmKey, 65)
redis.call('EXPIRE', tpmKey, 65)

return {1, 0}
`

var script = redis.NewScript(luaScript)

// Allow checks if the given key is within its rate limits.
// Returns (allowed, retryAfter, error).
// NOTE: RPM is checked against request count (correct).
// TPM is checked against actual token totals recorded via RecordTokens.
func (l *RedisLimiter) Allow(ctx context.Context, keyID string, rpm, tpm int32) (bool, time.Duration, error) {
	rpmKey := fmt.Sprintf("rl:rpm:%s", keyID)
	tpmKey := fmt.Sprintf("rl:tpm:%s", keyID)
	now := time.Now().UnixMilli()

	// Execute the Lua script.
	result, err := script.Run(ctx, l.client, []string{rpmKey, tpmKey}, now, rpm, tpm).Result()
	if err != nil {
		return false, 0, fmt.Errorf("rate limiter error: %w", err)
	}

	// Parse the result: [allowed (0 or 1), retryMs]
	res, ok := result.([]interface{})
	if !ok || len(res) < 2 {
		return false, 0, errors.New("unexpected rate limiter response format")
	}

	allowed, ok := res[0].(int64)
	if !ok {
		return false, 0, errors.New("invalid allowed flag from rate limiter")
	}

	retryMs, ok := res[1].(int64)
	if !ok {
		return false, 0, errors.New("invalid retry time from rate limiter")
	}

	if allowed == 1 {
		return true, 0, nil
	}

	return false, time.Duration(retryMs) * time.Millisecond, nil
}

// RecordTokens records actual token consumption for a key after the request completes.
// Tokens are stored in a sorted set with member format: "<timestamp_ms>:<tokens>:<nonce>".
// Score is timestamp for ZREMRANGEBYSCORE cleanup. Nonce prevents collisions at the same ms.
func (l *RedisLimiter) RecordTokens(ctx context.Context, keyID string, tokens int32) error {
	if tokens <= 0 {
		return nil
	}
	tpmKey := fmt.Sprintf("rl:tpm:%s", keyID)
	now := time.Now().UnixMilli()
	var nonceBuf [4]byte
	rand.Read(nonceBuf[:])
	nonce := binary.LittleEndian.Uint32(nonceBuf[:])
	member := fmt.Sprintf("%d:%d:%d", now, tokens, nonce)
	pipe := l.client.Pipeline()
	pipe.ZAdd(ctx, tpmKey, redis.Z{Score: float64(now), Member: member})
	pipe.Expire(ctx, tpmKey, 65*time.Second)
	_, err := pipe.Exec(ctx)
	return err
}
