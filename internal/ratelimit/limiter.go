package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config represents the configuration for the rate limiter
type Config struct {
	RedisClient *redis.Client
	KeyPrefix   string        `yaml:"key_prefix"`
	Limit       int           `yaml:"limit"`          // Requests per window
	Window      time.Duration `yaml:"window"`         // Time window duration
	BurstSize   int           `yaml:"burst_size"`     // Burst capacity
}

// Limiter provides distributed rate limiting using Redis
type Limiter struct {
	client *redis.Client
	config *Config
}

// NewLimiter creates a new distributed rate limiter
func NewLimiter(config *Config) (*Limiter, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if config.RedisClient == nil {
		return nil, fmt.Errorf("redis client cannot be nil")
	}

	// Set defaults
	if config.KeyPrefix == "" {
		config.KeyPrefix = "taskqueue:ratelimit"
	}
	if config.Limit <= 0 {
		config.Limit = 100 // Default 100 requests per window
	}
	if config.Window <= 0 {
		config.Window = time.Second // Default 1 second window
	}
	if config.BurstSize <= 0 {
		config.BurstSize = config.Limit + 50 // Default burst is limit + 50%
	}

	return &Limiter{
		client: config.RedisClient,
		config: config,
	}, nil
}

// Allow checks if a request should be allowed based on the rate limit
// Returns true if allowed, false if rate limited
func (l *Limiter) Allow(ctx context.Context) (bool, error) {
	return l.AllowN(ctx, 1)
}

// AllowN checks if N requests should be allowed based on the rate limit
func (l *Limiter) AllowN(ctx context.Context, n int) (bool, error) {
	if n <= 0 {
		return true, nil
	}

	key := l.config.KeyPrefix + ":global"
	now := time.Now().Unix()
	window := int64(l.config.Window.Seconds())
	
	// Use Lua script for atomic token bucket operations
	result, err := l.client.Eval(ctx, tokenBucketScript, []string{key}, 
		now, window, l.config.Limit, l.config.BurstSize, n).Result()
	
	if err != nil {
		return false, fmt.Errorf("failed to execute rate limit check: %w", err)
	}

	allowed, ok := result.(int64)
	if !ok {
		return false, fmt.Errorf("unexpected result type from Lua script")
	}

	return allowed == 1, nil
}

// Reset resets the rate limiter state
func (l *Limiter) Reset(ctx context.Context) error {
	key := l.config.KeyPrefix + ":global"
	return l.client.Del(ctx, key).Err()
}

// Stats returns current rate limiter statistics
func (l *Limiter) Stats(ctx context.Context) (*Stats, error) {
	key := l.config.KeyPrefix + ":global"
	
	result, err := l.client.HMGet(ctx, key, "tokens", "last_refill").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get rate limiter stats: %w", err)
	}

	stats := &Stats{
		Limit:     l.config.Limit,
		BurstSize: l.config.BurstSize,
		Window:    l.config.Window,
	}

	if len(result) >= 2 {
		if tokens, ok := result[0].(string); ok && tokens != "" {
			if t, err := strconv.ParseInt(tokens, 10, 64); err == nil {
				stats.AvailableTokens = int(t)
			}
		}
		
		if lastRefill, ok := result[1].(string); ok && lastRefill != "" {
			if t, err := strconv.ParseInt(lastRefill, 10, 64); err == nil {
				stats.LastRefill = time.Unix(t, 0)
			}
		}
	}

	return stats, nil
}

// Close closes the Redis connection
func (l *Limiter) Close() error {
	if l.client != nil {
		return l.client.Close()
	}
	return nil
}

// Stats represents rate limiter statistics
type Stats struct {
	Limit           int           `json:"limit"`
	BurstSize       int           `json:"burst_size"`
	Window          time.Duration `json:"window"`
	AvailableTokens int           `json:"available_tokens"`
	LastRefill      time.Time     `json:"last_refill"`
}

// tokenBucketScript implements a token bucket algorithm using Lua
// This ensures atomic operations across distributed systems
const tokenBucketScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local burst_size = tonumber(ARGV[4])
local requested = tonumber(ARGV[5])

-- Get current state
local current = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(current[1]) or burst_size
local last_refill = tonumber(current[2]) or now

-- Calculate tokens to add based on time elapsed
local elapsed = now - last_refill
local tokens_to_add = math.floor(elapsed * limit / window)

-- Add tokens but don't exceed burst size
tokens = math.min(burst_size, tokens + tokens_to_add)

-- Check if we can serve the request
if tokens >= requested then
    -- Consume tokens
    tokens = tokens - requested
    
    -- Update state
    redis.call('HMSET', key, 
        'tokens', tokens,
        'last_refill', now
    )
    
    -- Set expiration to prevent memory leaks
    redis.call('EXPIRE', key, window * 2)
    
    return 1  -- Allow
else
    -- Update last_refill time even if we can't serve
    redis.call('HMSET', key,
        'tokens', tokens,
        'last_refill', now
    )
    
    -- Set expiration to prevent memory leaks
    redis.call('EXPIRE', key, window * 2)
    
    return 0  -- Deny
end
`