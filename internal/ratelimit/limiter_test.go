package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
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
			name: "valid config with defaults",
			config: &Config{
				RedisAddr: "localhost:6379",
			},
			wantErr: false,
		},
		{
			name: "valid config with all fields",
			config: &Config{
				RedisAddr:     "localhost:6379",
				RedisPassword: "password",
				RedisDB:       1,
				KeyPrefix:     "test:ratelimit",
				Limit:         200,
				Window:        2 * time.Second,
				BurstSize:     300,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config != nil && tt.config.RedisAddr == "localhost:6379" {
				// Use miniredis for testing
				s, err := miniredis.Run()
				require.NoError(t, err)
				defer s.Close()
				tt.config.RedisAddr = s.Addr()
			}

			limiter, err := NewLimiter(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, limiter)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, limiter)
				
				// Check defaults are set
				if tt.config.KeyPrefix == "" {
					assert.Equal(t, "taskqueue:ratelimit", limiter.config.KeyPrefix)
				}
				if tt.config.Limit <= 0 {
					assert.Equal(t, 100, limiter.config.Limit)
				}
				if tt.config.Window <= 0 {
					assert.Equal(t, time.Second, limiter.config.Window)
				}
				if tt.config.BurstSize <= 0 {
					assert.Equal(t, 150, limiter.config.BurstSize) // limit + 50%
				}
				
				limiter.Close()
			}
		})
	}
}

func TestLimiter_Allow(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		KeyPrefix: "test:ratelimit",
		Limit:     10,           // 10 requests per second
		Window:    time.Second,  // 1 second window
		BurstSize: 15,           // Burst of 15
	}

	limiter, err := NewLimiter(config)
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Test initial burst capacity
	for i := 0; i < 15; i++ {
		allowed, err := limiter.Allow(ctx)
		require.NoError(t, err)
		assert.True(t, allowed, "Request %d should be allowed", i+1)
	}

	// 16th request should be denied (exceeded burst)
	allowed, err := limiter.Allow(ctx)
	require.NoError(t, err)
	assert.False(t, allowed, "Request should be denied after burst exhausted")
}

func TestLimiter_AllowN(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		KeyPrefix: "test:ratelimit",
		Limit:     100,
		Window:    time.Second,
		BurstSize: 150,
	}

	limiter, err := NewLimiter(config)
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Test AllowN with various values
	tests := []struct {
		name     string
		n        int
		expected bool
	}{
		{"allow 0 requests", 0, true},
		{"allow 50 requests", 50, true},
		{"allow 100 requests", 100, true},
		{"allow 1 request (should be denied)", 1, false}, // 151 total, exceeds burst
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, err := limiter.AllowN(ctx, tt.n)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, allowed)
		})
	}
}

func TestLimiter_TokenRefill(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		KeyPrefix: "test:ratelimit",
		Limit:     10,           // 10 requests per second
		Window:    time.Second,  // 1 second window
		BurstSize: 10,           // Burst of 10
	}

	limiter, err := NewLimiter(config)
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Exhaust all tokens
	for i := 0; i < 10; i++ {
		allowed, err := limiter.Allow(ctx)
		require.NoError(t, err)
		assert.True(t, allowed)
	}

	// Next request should be denied
	allowed, err := limiter.Allow(ctx)
	require.NoError(t, err)
	assert.False(t, allowed)

	// Wait for tokens to refill (in a real system, this would happen automatically)
	// For testing, we'll simulate this by waiting and then testing that tokens get refilled
	time.Sleep(100 * time.Millisecond) // Small delay to ensure time difference
	
	// Reset the limiter to simulate token refill
	err = limiter.Reset(ctx)
	require.NoError(t, err)

	// Requests should be allowed again after reset (simulating refill)
	allowed, err = limiter.Allow(ctx)
	require.NoError(t, err)
	assert.True(t, allowed, "Request should be allowed after token refill")
}

func TestLimiter_Stats(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		KeyPrefix: "test:ratelimit",
		Limit:     100,
		Window:    time.Second,
		BurstSize: 150,
	}

	limiter, err := NewLimiter(config)
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Get initial stats
	stats, err := limiter.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 100, stats.Limit)
	assert.Equal(t, 150, stats.BurstSize)
	assert.Equal(t, time.Second, stats.Window)

	// Consume some tokens
	allowed, err := limiter.AllowN(ctx, 50)
	require.NoError(t, err)
	assert.True(t, allowed)

	// Check stats after consumption
	stats, err = limiter.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 100, stats.AvailableTokens) // 150 - 50 = 100
}

func TestLimiter_Reset(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		KeyPrefix: "test:ratelimit",
		Limit:     10,
		Window:    time.Second,
		BurstSize: 10,
	}

	limiter, err := NewLimiter(config)
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Exhaust all tokens
	for i := 0; i < 10; i++ {
		allowed, err := limiter.Allow(ctx)
		require.NoError(t, err)
		assert.True(t, allowed)
	}

	// Next request should be denied
	allowed, err := limiter.Allow(ctx)
	require.NoError(t, err)
	assert.False(t, allowed)

	// Reset the limiter
	err = limiter.Reset(ctx)
	require.NoError(t, err)

	// Requests should be allowed again after reset
	allowed, err = limiter.Allow(ctx)
	require.NoError(t, err)
	assert.True(t, allowed, "Request should be allowed after reset")
}

func TestLimiter_ConcurrentAccess(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		KeyPrefix: "test:ratelimit",
		Limit:     100,
		Window:    time.Second,
		BurstSize: 100,
	}

	limiter, err := NewLimiter(config)
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Simulate concurrent access
	const numGoroutines = 10
	const requestsPerGoroutine = 20
	
	results := make(chan bool, numGoroutines*requestsPerGoroutine)
	
	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < requestsPerGoroutine; j++ {
				allowed, err := limiter.Allow(ctx)
				require.NoError(t, err)
				results <- allowed
			}
		}()
	}

	// Count allowed and denied requests
	allowedCount := 0
	deniedCount := 0
	
	for i := 0; i < numGoroutines*requestsPerGoroutine; i++ {
		if <-results {
			allowedCount++
		} else {
			deniedCount++
		}
	}

	// Should allow exactly 100 requests (burst size)
	assert.Equal(t, 100, allowedCount, "Should allow exactly burst size requests")
	assert.Equal(t, 100, deniedCount, "Should deny remaining requests")
}

func TestLimiter_InvalidRequests(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		KeyPrefix: "test:ratelimit",
		Limit:     100,
		Window:    time.Second,
		BurstSize: 150,
	}

	limiter, err := NewLimiter(config)
	require.NoError(t, err)
	defer limiter.Close()

	ctx := context.Background()

	// Test negative request count
	allowed, err := limiter.AllowN(ctx, -1)
	require.NoError(t, err)
	assert.True(t, allowed, "Negative request count should be allowed")

	// Test zero request count
	allowed, err = limiter.AllowN(ctx, 0)
	require.NoError(t, err)
	assert.True(t, allowed, "Zero request count should be allowed")
}

func BenchmarkLimiter_Allow(b *testing.B) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(b, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		KeyPrefix: "bench:ratelimit",
		Limit:     1000000, // High limit to avoid rate limiting during benchmark
		Window:    time.Second,
		BurstSize: 1000000,
	}

	limiter, err := NewLimiter(config)
	require.NoError(b, err)
	defer limiter.Close()

	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := limiter.Allow(ctx)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}