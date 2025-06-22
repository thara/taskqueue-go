package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLimiter(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "nil redis client",
			config: &Config{
				RedisClient: nil,
			},
			wantErr: true,
		},
		{
			name: "valid config with defaults",
			config: &Config{
				RedisClient: createTestRedisClient(t),
			},
			wantErr: false,
		},
		{
			name: "valid config with all fields",
			config: &Config{
				RedisClient: createTestRedisClient(t),
				KeyPrefix:   "test:ratelimit",
				Limit:       200,
				Window:      2 * time.Second,
				BurstSize:   300,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter, err := NewLimiter(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, limiter)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, limiter)
				if limiter != nil {
					assert.NoError(t, limiter.Close())
				}
			}
		})
	}
}

func createTestRedisClient(t *testing.T) *redis.Client {
	s, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(func() {
		s.Close()
	})
	
	return redis.NewClient(&redis.Options{
		Addr: s.Addr(),
	})
}

func TestLimiter_Allow(t *testing.T) {
	client := createTestRedisClient(t)
	limiter, err := NewLimiter(&Config{
		RedisClient: client,
		KeyPrefix:   "test:ratelimit",
		Limit:       10, // 10 requests per second
		Window:      time.Second,
		BurstSize:   15, // Allow bursts up to 15
	})
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Test initial requests (should all be allowed due to burst)
	for i := 0; i < 15; i++ {
		allowed, err := limiter.Allow(ctx)
		assert.NoError(t, err)
		assert.True(t, allowed, "request %d should be allowed", i+1)
	}

	// Next request should be denied (burst exhausted)
	allowed, err := limiter.Allow(ctx)
	assert.NoError(t, err)
	assert.False(t, allowed, "request 16 should be denied")
}

func TestLimiter_AllowN(t *testing.T) {
	client := createTestRedisClient(t)
	limiter, err := NewLimiter(&Config{
		RedisClient: client,
		KeyPrefix:   "test:ratelimit",
		Limit:       10,
		Window:      time.Second,
		BurstSize:   15,
	})
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Test allowing multiple requests at once
	allowed, err := limiter.AllowN(ctx, 10)
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Should still have 5 tokens left
	allowed, err = limiter.AllowN(ctx, 5)
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Now bucket should be empty
	allowed, err = limiter.AllowN(ctx, 1)
	assert.NoError(t, err)
	assert.False(t, allowed)
}

func TestLimiter_Reset(t *testing.T) {
	client := createTestRedisClient(t)
	limiter, err := NewLimiter(&Config{
		RedisClient: client,
		KeyPrefix:   "test:ratelimit",
		Limit:       5,
		Window:      time.Second,
		BurstSize:   5,
	})
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Exhaust the rate limit
	for i := 0; i < 5; i++ {
		allowed, err := limiter.Allow(ctx)
		assert.NoError(t, err)
		assert.True(t, allowed)
	}

	// Should be denied now
	allowed, err := limiter.Allow(ctx)
	assert.NoError(t, err)
	assert.False(t, allowed)

	// Reset the limiter
	err = limiter.Reset(ctx)
	assert.NoError(t, err)

	// Should be allowed again
	allowed, err = limiter.Allow(ctx)
	assert.NoError(t, err)
	assert.True(t, allowed)
}

func TestLimiter_Stats(t *testing.T) {
	client := createTestRedisClient(t)
	limiter, err := NewLimiter(&Config{
		RedisClient: client,
		KeyPrefix:   "test:ratelimit",
		Limit:       10,
		Window:      time.Second,
		BurstSize:   15,
	})
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Get initial stats
	stats, err := limiter.Stats(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Equal(t, 10, stats.Limit)
	assert.Equal(t, 15, stats.BurstSize)
	assert.Equal(t, time.Second, stats.Window)

	// Use some tokens
	allowed, err := limiter.AllowN(ctx, 5)
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Check stats again
	stats, err = limiter.Stats(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 10, stats.AvailableTokens) // Should be 10 (15 - 5)
}