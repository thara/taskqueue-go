# Development Guide

## Getting Started

### Prerequisites

```bash
# Install Go
brew install go

# Install Redis
brew install redis

# Install development tools
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/swaggo/swag/cmd/swag@latest
```

### Local Setup

```bash
# Clone repository
git clone https://github.com/thara/taskqueue-go
cd taskqueue-go

# Install dependencies
go mod download

# Start local services
docker-compose up -d

# Run tests
make test

# Run with hot reload
air -c .air.toml
```

## Code Examples

### Adding a New Task Type

```go
// internal/models/tasks/email_digest.go
package tasks

import (
    "time"
    "github.com/thara/taskqueue-go/internal/models"
)

var EmailDigestTask = models.TaskType{
    ID:          "email_digest",
    Name:        "Email Digest",
    Description: "Sends daily email digest to users",
    Interval: models.TaskInterval{
        Type:     models.IntervalTypeDaily,
        Value:    1,
        Timezone: "UTC",
        AtTime:   &time.Time{Hour: 9}, // 9 AM UTC
    },
    Timeout: 5 * time.Minute,
    Retries: 3,
    Config: models.TaskConfig{
        Endpoint: "https://api.example.com/email/digest",
        Method:   "POST",
        Headers: map[string]string{
            "Content-Type": "application/json",
        },
        MaxDuration: 4 * time.Minute,
    },
}

// Register in task registry
func init() {
    registry.Register(EmailDigestTask)
}
```

### Implementing a Custom Executor

```go
// internal/worker/executors/custom_executor.go
package executors

import (
    "context"
    "encoding/json"
    "fmt"
    "github.com/thara/taskqueue-go/internal/models"
)

type CustomExecutor struct {
    client HTTPClient
}

func NewCustomExecutor(client HTTPClient) *CustomExecutor {
    return &CustomExecutor{client: client}
}

func (e *CustomExecutor) Execute(ctx context.Context, job *models.Job) (*models.ExecutionResult, error) {
    result := &models.ExecutionResult{
        JobID:     job.ID,
        StartedAt: time.Now(),
    }

    // Parse job payload
    var payload map[string]interface{}
    if err := json.Unmarshal(job.Payload, &payload); err != nil {
        result.Status = models.StatusFailed
        result.Error = &models.ErrorDetail{
            Code:    "INVALID_PAYLOAD",
            Message: err.Error(),
            Retry:   false,
        }
        return result, nil
    }

    // Get task configuration
    taskType, err := registry.Get(job.TaskTypeID)
    if err != nil {
        result.Status = models.StatusFailed
        result.Error = &models.ErrorDetail{
            Code:    "UNKNOWN_TASK",
            Message: err.Error(),
            Retry:   false,
        }
        return result, nil
    }

    // Execute with timeout
    execCtx, cancel := context.WithTimeout(ctx, taskType.Timeout)
    defer cancel()

    // Make HTTP request
    resp, err := e.client.PostJSON(execCtx, taskType.Config.Endpoint, payload)
    if err != nil {
        result.Status = models.StatusFailed
        result.Error = &models.ErrorDetail{
            Code:    "HTTP_ERROR",
            Message: err.Error(),
            Retry:   true,
        }
        return result, err
    }

    // Check response
    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        result.Status = models.StatusSuccess
        result.Response = resp.Body
    } else {
        result.Status = models.StatusFailed
        result.Error = &models.ErrorDetail{
            Code:    fmt.Sprintf("HTTP_%d", resp.StatusCode),
            Message: string(resp.Body),
            Retry:   resp.StatusCode >= 500, // Retry on server errors
        }
    }

    result.CompletedAt = time.Now()
    result.Duration = result.CompletedAt.Sub(result.StartedAt)

    return result, nil
}
```

### Custom Rate Limiter Implementation

```go
// internal/ratelimit/adaptive_limiter.go
package ratelimit

import (
    "context"
    "time"
    "github.com/redis/go-redis/v9"
)

type AdaptiveLimiter struct {
    redis       *redis.Client
    baseRate    int
    minRate     int
    maxRate     int
    adjustEvery time.Duration
}

func NewAdaptiveLimiter(redis *redis.Client, baseRate int) *AdaptiveLimiter {
    return &AdaptiveLimiter{
        redis:       redis,
        baseRate:    baseRate,
        minRate:     baseRate / 2,
        maxRate:     baseRate * 2,
        adjustEvery: 1 * time.Minute,
    }
}

func (l *AdaptiveLimiter) Allow(ctx context.Context) (bool, error) {
    // Get current rate based on system load
    currentRate := l.getCurrentRate(ctx)
    
    // Try to acquire token
    script := `
        local key = KEYS[1]
        local rate = tonumber(ARGV[1])
        local now = tonumber(ARGV[2])
        local window = 1 -- 1 second window
        
        local current = redis.call('GET', key)
        if current == false then
            redis.call('SET', key, 1, 'EX', window)
            return 1
        elseif tonumber(current) < rate then
            redis.call('INCR', key)
            return 1
        else
            return 0
        end
    `
    
    allowed, err := l.redis.Eval(ctx, script, 
        []string{"ratelimit:adaptive"},
        currentRate,
        time.Now().Unix(),
    ).Bool()
    
    return allowed, err
}

func (l *AdaptiveLimiter) getCurrentRate(ctx context.Context) int {
    // Check error rate
    errorRate, _ := l.getErrorRate(ctx)
    
    // Adjust rate based on error rate
    if errorRate > 0.1 { // More than 10% errors
        return l.minRate
    } else if errorRate < 0.01 { // Less than 1% errors
        return l.maxRate
    }
    
    return l.baseRate
}

func (l *AdaptiveLimiter) getErrorRate(ctx context.Context) (float64, error) {
    // Get error metrics from Redis
    pipe := l.redis.Pipeline()
    totalCmd := pipe.Get(ctx, "metrics:total")
    errorsCmd := pipe.Get(ctx, "metrics:errors")
    _, err := pipe.Exec(ctx)
    
    if err != nil {
        return 0, err
    }
    
    total, _ := totalCmd.Float64()
    errors, _ := errorsCmd.Float64()
    
    if total == 0 {
        return 0, nil
    }
    
    return errors / total, nil
}
```

### Writing Tests

```go
// internal/scheduler/scheduler_test.go
package scheduler

import (
    "context"
    "testing"
    "time"
    
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
    "github.com/alicebob/miniredis/v2"
    "github.com/redis/go-redis/v9"
)

func TestScheduler_CheckAndTrigger(t *testing.T) {
    // Setup
    mr := miniredis.RunT(t)
    redis := redis.NewClient(&redis.Options{
        Addr: mr.Addr(),
    })
    
    mockQueue := &MockQueue{}
    mockDistributor := &MockDistributor{}
    
    scheduler := NewScheduler(redis, mockQueue, mockDistributor)
    
    // Test data
    taskType := &models.TaskType{
        ID: "test_task",
        Interval: models.TaskInterval{
            Type:  models.IntervalTypeHourly,
            Value: 1,
        },
    }
    
    // Set last execution to 2 hours ago
    lastExec := time.Now().Add(-2 * time.Hour)
    redis.Set(context.Background(), 
        "taskqueue:schedule:test_task", 
        lastExec.Unix(), 
        0,
    )
    
    // Mock expectations
    mockDistributor.On("Distribute", 
        mock.Anything,
        taskType,
        mock.AnythingOfType("[]string"),
    ).Return(nil)
    
    // Execute
    err := scheduler.CheckAndTrigger(context.Background(), taskType)
    
    // Assert
    assert.NoError(t, err)
    mockDistributor.AssertExpectations(t)
    
    // Verify last execution updated
    newLastExec, err := redis.Get(context.Background(), 
        "taskqueue:schedule:test_task",
    ).Int64()
    assert.NoError(t, err)
    assert.Greater(t, newLastExec, lastExec.Unix())
}

func TestScheduler_ConcurrentExecution(t *testing.T) {
    // Test that only one scheduler can execute at a time
    mr := miniredis.RunT(t)
    redis := redis.NewClient(&redis.Options{
        Addr: mr.Addr(),
    })
    
    scheduler1 := NewScheduler(redis, nil, nil)
    scheduler2 := NewScheduler(redis, nil, nil)
    
    // First scheduler acquires lock
    lock1, err := scheduler1.acquireLock(context.Background(), "test_task")
    assert.NoError(t, err)
    assert.True(t, lock1)
    
    // Second scheduler should fail
    lock2, err := scheduler2.acquireLock(context.Background(), "test_task")
    assert.NoError(t, err)
    assert.False(t, lock2)
    
    // Release lock
    scheduler1.releaseLock(context.Background(), "test_task")
    
    // Now second scheduler can acquire
    lock2, err = scheduler2.acquireLock(context.Background(), "test_task")
    assert.NoError(t, err)
    assert.True(t, lock2)
}
```

### Integration Testing

```go
// test/integration/end_to_end_test.go
package integration

import (
    "context"
    "testing"
    "time"
    
    "github.com/stretchr/testify/assert"
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/modules/redis"
)

func TestEndToEnd(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }
    
    ctx := context.Background()
    
    // Start Redis container
    redisContainer, err := redis.RunContainer(ctx,
        testcontainers.WithImage("redis:7-alpine"),
    )
    assert.NoError(t, err)
    defer redisContainer.Terminate(ctx)
    
    redisURL, err := redisContainer.ConnectionString(ctx)
    assert.NoError(t, err)
    
    // Start message queue container
    // ... setup queue container
    
    // Initialize components
    scheduler := setupScheduler(redisURL, queueURL)
    worker := setupWorker(redisURL, queueURL)
    
    // Create test task
    testTask := &models.TaskType{
        ID: "integration_test",
        Interval: models.TaskInterval{
            Type:  models.IntervalTypeHourly,
            Value: 1,
        },
    }
    
    // Enable for test user
    err = storage.EnableTaskForUser(ctx, "test_user", testTask.ID)
    assert.NoError(t, err)
    
    // Trigger scheduling
    err = scheduler.TriggerTask(ctx, testTask)
    assert.NoError(t, err)
    
    // Start worker
    go worker.Start(ctx)
    
    // Wait for execution
    timeout := time.After(30 * time.Second)
    success := false
    
    for !success {
        select {
        case <-timeout:
            t.Fatal("Timeout waiting for task execution")
        case <-time.After(100 * time.Millisecond):
            // Check if task executed
            result, err := storage.GetExecutionResult(ctx, "test_user", testTask.ID)
            if err == nil && result.Status == models.StatusSuccess {
                success = true
            }
        }
    }
    
    assert.True(t, success)
}
```

## Best Practices

### Error Handling

```go
// DO: Wrap errors with context
if err != nil {
    return fmt.Errorf("failed to execute task %s: %w", taskID, err)
}

// DO: Use structured errors
type TaskError struct {
    TaskID string
    Code   string
    Err    error
}

func (e *TaskError) Error() string {
    return fmt.Sprintf("task %s failed with code %s: %v", 
        e.TaskID, e.Code, e.Err)
}

// DON'T: Ignore errors
result, _ := executeTask(task) // Bad!
```

### Logging

```go
// Use structured logging
import "github.com/rs/zerolog/log"

log.Info().
    Str("task_id", task.ID).
    Str("user_id", job.UserID).
    Dur("duration", duration).
    Msg("Task executed successfully")

// Add request ID for tracing
ctx := context.WithValue(ctx, "request_id", uuid.New().String())

log.Ctx(ctx).Error().
    Err(err).
    Str("task_id", task.ID).
    Msg("Task execution failed")
```

### Testing

```go
// Use table-driven tests
func TestTaskInterval_NextExecution(t *testing.T) {
    tests := []struct {
        name     string
        interval TaskInterval
        from     time.Time
        want     time.Time
    }{
        {
            name: "hourly",
            interval: TaskInterval{
                Type:  IntervalTypeHourly,
                Value: 1,
            },
            from: time.Date(2024, 1, 1, 10, 30, 0, 0, time.UTC),
            want: time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC),
        },
        {
            name: "daily at specific time",
            interval: TaskInterval{
                Type:   IntervalTypeDaily,
                Value:  1,
                AtTime: &time.Time{Hour: 9},
            },
            from: time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC),
            want: time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC),
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := tt.interval.NextExecution(tt.from)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

### Performance

```go
// Use sync.Pool for frequently allocated objects
var jobPool = sync.Pool{
    New: func() interface{} {
        return &models.Job{}
    },
}

func getJob() *models.Job {
    return jobPool.Get().(*models.Job)
}

func putJob(job *models.Job) {
    job.Reset() // Clear fields
    jobPool.Put(job)
}

// Batch Redis operations
func updateMetrics(ctx context.Context, results []ExecutionResult) error {
    pipe := redis.Pipeline()
    
    for _, result := range results {
        key := fmt.Sprintf("metrics:%s", result.TaskID)
        pipe.HIncrBy(ctx, key, "total", 1)
        if result.Status == StatusFailed {
            pipe.HIncrBy(ctx, key, "failed", 1)
        }
    }
    
    _, err := pipe.Exec(ctx)
    return err
}
```

### Security

```go
// Validate all inputs
func validateTaskID(id string) error {
    if len(id) == 0 || len(id) > 100 {
        return fmt.Errorf("invalid task ID length")
    }
    
    if !taskIDRegex.MatchString(id) {
        return fmt.Errorf("invalid task ID format")
    }
    
    return nil
}

// Use context for cancellation
func (w *Worker) Execute(ctx context.Context, job *Job) error {
    // Create timeout context
    ctx, cancel := context.WithTimeout(ctx, job.Timeout)
    defer cancel()
    
    // Check context regularly
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        // Continue execution
    }
}

// Sanitize error messages
func sanitizeError(err error) string {
    // Don't expose internal details
    switch {
    case errors.Is(err, ErrDatabase):
        return "Internal database error"
    case errors.Is(err, ErrNetwork):
        return "Network error occurred"
    default:
        return "An error occurred"
    }
}
```

## Debugging

### Enable Debug Logging

```bash
# Set log level
export LOG_LEVEL=debug

# Enable specific debug logs
export DEBUG_SCHEDULER=true
export DEBUG_WORKER=true
export DEBUG_REDIS=true
```

### Using Delve

```bash
# Debug scheduler
dlv debug ./cmd/scheduler -- -config config/scheduler.yaml

# Attach to running process
dlv attach $(pgrep scheduler)

# Common commands
(dlv) break scheduler.go:45
(dlv) continue
(dlv) print job
(dlv) stack
```

### Profiling

```go
// Add profiling endpoints
import _ "net/http/pprof"

func main() {
    go func() {
        log.Println(http.ListenAndServe("localhost:6060", nil))
    }()
    
    // ... rest of application
}
```

```bash
# CPU profile
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# Memory profile
go tool pprof http://localhost:6060/debug/pprof/heap

# Goroutine profile
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

### Tracing with OpenTelemetry

```go
// Initialize tracer
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/trace"
)

func initTracer() (trace.Tracer, error) {
    // Setup OTLP exporter
    exporter, err := otlptrace.New(
        context.Background(),
        otlptracegrpc.NewClient(
            otlptracegrpc.WithEndpoint("localhost:4317"),
        ),
    )
    
    // Create tracer provider
    tp := trace.NewTracerProvider(
        trace.WithBatcher(exporter),
        trace.WithResource(resource.NewWithAttributes(
            semconv.ServiceNameKey.String("taskqueue"),
        )),
    )
    
    otel.SetTracerProvider(tp)
    return tp.Tracer("taskqueue"), nil
}

// Use in code
ctx, span := tracer.Start(ctx, "execute_task",
    trace.WithAttributes(
        attribute.String("task.id", task.ID),
        attribute.String("user.id", user.ID),
    ),
)
defer span.End()
```