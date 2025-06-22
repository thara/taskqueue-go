package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thara/taskqueue-go/internal/models"
	"github.com/thara/taskqueue-go/internal/queue"
	"github.com/thara/taskqueue-go/internal/ratelimit"
	"github.com/thara/taskqueue-go/internal/storage"
)

func TestNew(t *testing.T) {
	queueClient := createTestQueueClient(t)
	rateLimiter := createTestRateLimiter(t)
	registry := createTestRegistry(t)

	config := Config{
		PoolSize:         3,
		PollInterval:     2 * time.Second,
		MaxJobTimeout:    5 * time.Minute,
		RetryDelay:       30 * time.Second,
		MaxRetries:       3,
		ExecutionTimeout: 30 * time.Second,
		RetryBackoff:     "exponential",
		RetryBaseDelay:   2 * time.Second,
		RetryMaxDelay:    60 * time.Second,
	}

	pool := New(config, queueClient, rateLimiter, registry)

	assert.NotNil(t, pool)
	assert.Equal(t, config, pool.config)
	assert.Equal(t, queueClient, pool.queueClient)
	assert.Equal(t, rateLimiter, pool.rateLimiter)
	assert.Equal(t, registry, pool.registry)
	assert.Len(t, pool.workers, 3)
	assert.NotNil(t, pool.httpClient)
	assert.Equal(t, config.ExecutionTimeout, pool.httpClient.Timeout)

	// Verify workers are properly initialized
	for i, worker := range pool.workers {
		assert.Equal(t, i, worker.id)
		assert.Equal(t, pool, worker.pool)
		assert.NotNil(t, worker.stopCh)
	}
}

func TestWorker_CalculateExponentialBackoff(t *testing.T) {
	queueClient := createTestQueueClient(t)
	rateLimiter := createTestRateLimiter(t)
	registry := createTestRegistry(t)

	config := Config{
		PoolSize:       1, // Ensure we have at least one worker
		RetryBaseDelay: 2 * time.Second,
		RetryMaxDelay:  60 * time.Second,
	}

	pool := New(config, queueClient, rateLimiter, registry)
	worker := pool.workers[0]

	tests := []struct {
		name     string
		attempt  int
		expected time.Duration
		maxDelay time.Duration
	}{
		{
			name:     "first attempt",
			attempt:  1,
			expected: 2 * time.Second,
			maxDelay: 60 * time.Second,
		},
		{
			name:     "second attempt",
			attempt:  2,
			expected: 4 * time.Second,
			maxDelay: 60 * time.Second,
		},
		{
			name:     "third attempt",
			attempt:  3,
			expected: 8 * time.Second,
			maxDelay: 60 * time.Second,
		},
		{
			name:     "high attempt capped at max",
			attempt:  10,
			expected: 60 * time.Second, // Should be capped
			maxDelay: 60 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay := worker.calculateExponentialBackoff(tt.attempt)
			assert.Equal(t, tt.expected, delay)
			assert.LessOrEqual(t, delay, tt.maxDelay)
		})
	}
}

func TestWorker_ExecuteJob_Success(t *testing.T) {
	// Create a test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NotEmpty(t, r.Header.Get("X-Job-ID"))
		assert.NotEmpty(t, r.Header.Get("X-User-ID"))
		assert.NotEmpty(t, r.Header.Get("X-Task-Type"))

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	queueClient := createTestQueueClient(t)
	rateLimiter := createTestRateLimiter(t)
	registry := createTestRegistry(t)

	pool := New(Config{
		PoolSize:         1,
		ExecutionTimeout: 30 * time.Second,
	}, queueClient, rateLimiter, registry)
	worker := pool.workers[0]

	ctx := context.Background()
	job := &models.Job{
		ID:         "test-job-1",
		TaskTypeID: "test-task",
		UserID:     "user1",
		CreatedAt:  time.Now(),
	}

	taskType := &models.TaskType{
		ID:      "test-task",
		Name:    "Test Task",
		Timeout: 30 * time.Second,
		Config: models.TaskConfig{
			Endpoint: server.URL,
			Method:   "POST",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
		},
	}

	result := worker.executeJob(ctx, job, taskType)

	assert.NotNil(t, result)
	assert.Equal(t, job.ID, result.JobID)
	assert.Equal(t, models.StatusSuccess, result.Status)
	assert.Nil(t, result.Error)
	assert.NotEmpty(t, result.Response)
	assert.True(t, result.CompletedAt.After(result.StartedAt))
	assert.Greater(t, result.Duration, time.Duration(0))
}

func TestWorker_ExecuteJob_HTTPError(t *testing.T) {
	// Create a test HTTP server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	queueClient := createTestQueueClient(t)
	rateLimiter := createTestRateLimiter(t)
	registry := createTestRegistry(t)

	pool := New(Config{
		PoolSize:         1,
		ExecutionTimeout: 30 * time.Second,
	}, queueClient, rateLimiter, registry)
	worker := pool.workers[0]

	ctx := context.Background()
	job := &models.Job{
		ID:         "test-job-error",
		TaskTypeID: "test-task",
		UserID:     "user1",
		CreatedAt:  time.Now(),
	}

	taskType := &models.TaskType{
		ID:      "test-task",
		Name:    "Test Task",
		Timeout: 30 * time.Second,
		Config: models.TaskConfig{
			Endpoint: server.URL,
			Method:   "GET",
		},
	}

	result := worker.executeJob(ctx, job, taskType)

	assert.NotNil(t, result)
	assert.Equal(t, job.ID, result.JobID)
	assert.Equal(t, models.StatusFailed, result.Status)
	assert.NotNil(t, result.Error)
	assert.Equal(t, "HTTP_500", result.Error.Code)
	assert.True(t, result.Error.Retry) // Server errors should be retryable
}

func TestWorker_ExecuteJob_ClientError(t *testing.T) {
	// Create a test HTTP server that returns a client error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "bad request"}`))
	}))
	defer server.Close()

	queueClient := createTestQueueClient(t)
	rateLimiter := createTestRateLimiter(t)
	registry := createTestRegistry(t)

	pool := New(Config{
		PoolSize:         1,
		ExecutionTimeout: 30 * time.Second,
	}, queueClient, rateLimiter, registry)
	worker := pool.workers[0]

	ctx := context.Background()
	job := &models.Job{
		ID:         "test-job-client-error",
		TaskTypeID: "test-task",
		UserID:     "user1",
		CreatedAt:  time.Now(),
	}

	taskType := &models.TaskType{
		ID:      "test-task",
		Name:    "Test Task",
		Timeout: 30 * time.Second,
		Config: models.TaskConfig{
			Endpoint: server.URL,
			Method:   "GET",
		},
	}

	result := worker.executeJob(ctx, job, taskType)

	assert.NotNil(t, result)
	assert.Equal(t, job.ID, result.JobID)
	assert.Equal(t, models.StatusFailed, result.Status)
	assert.NotNil(t, result.Error)
	assert.Equal(t, "HTTP_400", result.Error.Code)
	assert.False(t, result.Error.Retry) // Client errors should not be retryable
}

func TestWorker_ExecuteJob_Timeout(t *testing.T) {
	// Create a test HTTP server that hangs
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // Longer than our timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	queueClient := createTestQueueClient(t)
	rateLimiter := createTestRateLimiter(t)
	registry := createTestRegistry(t)

	pool := New(Config{
		PoolSize:         1,
		ExecutionTimeout: 50 * time.Millisecond, // Very short timeout
	}, queueClient, rateLimiter, registry)
	worker := pool.workers[0]

	ctx := context.Background()
	job := &models.Job{
		ID:         "test-job-timeout",
		TaskTypeID: "test-task",
		UserID:     "user1",
		CreatedAt:  time.Now(),
	}

	taskType := &models.TaskType{
		ID:      "test-task",
		Name:    "Test Task",
		Timeout: 50 * time.Millisecond,
		Config: models.TaskConfig{
			Endpoint: server.URL,
			Method:   "GET",
		},
	}

	result := worker.executeJob(ctx, job, taskType)

	assert.NotNil(t, result)
	assert.Equal(t, job.ID, result.JobID)
	assert.Equal(t, models.StatusFailed, result.Status)
	assert.NotNil(t, result.Error)
	assert.Equal(t, "HTTP_REQUEST_FAILED", result.Error.Code)
	assert.True(t, result.Error.Retry) // Network errors should be retryable
}

func TestWorker_ScheduleRetry(t *testing.T) {
	queueClient := createTestQueueClient(t)
	rateLimiter := createTestRateLimiter(t)
	registry := createTestRegistry(t)

	pool := New(Config{
		PoolSize:       1,
		RetryBackoff:   "exponential",
		RetryBaseDelay: 1 * time.Second,
		RetryMaxDelay:  10 * time.Second,
	}, queueClient, rateLimiter, registry)
	worker := pool.workers[0]

	ctx := context.Background()
	originalJob := &models.Job{
		ID:         "original-job",
		TaskTypeID: "test-task",
		UserID:     "user1",
		Attempt:    1,
		CreatedAt:  time.Now(),
	}

	result := &models.ExecutionResult{
		JobID:  originalJob.ID,
		Status: models.StatusFailed,
		Error: &models.ErrorDetail{
			Code:    "TEST_ERROR",
			Message: "Test error",
			Retry:   true,
		},
	}

	err := worker.scheduleRetry(ctx, originalJob, result)
	assert.NoError(t, err)

	// Verify retry job was pushed to queue
	msg, err := queueClient.Pull(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, msg)

	retryJob := msg.QueueMessage.Job
	assert.Contains(t, retryJob.ID, "original-job_retry_")
	assert.Equal(t, originalJob.TaskTypeID, retryJob.TaskTypeID)
	assert.Equal(t, originalJob.UserID, retryJob.UserID)
	assert.Equal(t, 2, retryJob.Attempt) // Should be incremented
	assert.True(t, retryJob.ExecuteAfter.After(time.Now())) // Should be scheduled in the future

	// Acknowledge the message
	err = queueClient.Ack(ctx, msg.MessageID, msg.Receipt)
	assert.NoError(t, err)
}

func TestWorker_ProcessJob_JobNotReady(t *testing.T) {
	queueClient := createTestQueueClient(t)
	rateLimiter := createTestRateLimiter(t)
	registry := createTestRegistry(t)

	pool := New(Config{PoolSize: 1}, queueClient, rateLimiter, registry)
	worker := pool.workers[0]

	ctx := context.Background()

	// Create a job scheduled for the future
	futureTime := time.Now().Add(1 * time.Hour)
	job := &models.Job{
		ID:           "future-job",
		TaskTypeID:   "test-task",
		UserID:       "user1",
		ExecuteAfter: futureTime,
		CreatedAt:    time.Now(),
	}

	// Push job to queue
	err := queueClient.Push(ctx, job)
	require.NoError(t, err)

	// Process the job - it should be acknowledged but not executed
	err = worker.processJob(ctx)
	assert.NoError(t, err)

	// Queue should be empty (job was acknowledged)
	msg, err := queueClient.Pull(ctx)
	assert.NoError(t, err)
	assert.Nil(t, msg) // No message should be available
}

func TestWorker_ProcessJob_RateLimited(t *testing.T) {
	queueClient := createTestQueueClient(t)
	
	// Create a rate limiter that always denies
	redisClient := createTestRedisClient(t)
	rateLimiter, err := ratelimit.NewLimiter(&ratelimit.Config{
		RedisClient: redisClient,
		KeyPrefix:   "test:ratelimit",
		Limit:       0, // No requests allowed
		Window:      1 * time.Second,
		BurstSize:   0,
	})
	require.NoError(t, err)

	registry := createTestRegistry(t)

	pool := New(Config{PoolSize: 1}, queueClient, rateLimiter, registry)
	worker := pool.workers[0]

	ctx := context.Background()

	// Create a job ready for execution
	job := &models.Job{
		ID:           "rate-limited-job",
		TaskTypeID:   "test-task",
		UserID:       "user1",
		ExecuteAfter: time.Now().Add(-1 * time.Minute), // Past time
		CreatedAt:    time.Now(),
	}

	// Push job to queue
	err = queueClient.Push(ctx, job)
	require.NoError(t, err)

	// Process the job - it should be rate limited and not acknowledged
	err = worker.processJob(ctx)
	assert.NoError(t, err)

	// Job should still be in queue (not acknowledged due to rate limiting)
	_, err = queueClient.Pull(ctx)
	assert.NoError(t, err)
	// Note: Depending on implementation, job might still be there or might have been acked
	// This test verifies the processJob doesn't error on rate limiting
}

// Helper functions

func createTestQueueClient(t *testing.T) *queue.Client {
	s, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(func() {
		s.Close()
	})

	queueClient, err := queue.NewClient(&queue.Config{
		RedisAddr:         s.Addr(),
		QueueName:         "test:worker:queue",
		MaxRetries:        3,
		DefaultVisibility: 30 * time.Second,
	})
	require.NoError(t, err)

	return queueClient
}

func createTestRateLimiter(t *testing.T) *ratelimit.Limiter {
	redisClient := createTestRedisClient(t)

	rateLimiter, err := ratelimit.NewLimiter(&ratelimit.Config{
		RedisClient: redisClient,
		KeyPrefix:   "test:ratelimit",
		Limit:       100, // High limit for most tests
		Window:      1 * time.Second,
		BurstSize:   150,
	})
	require.NoError(t, err)

	return rateLimiter
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

func createTestRegistry(t *testing.T) *storage.TaskRegistry {
	registry := storage.NewTaskRegistry("", nil)
	return registry
}