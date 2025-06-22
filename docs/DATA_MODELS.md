# Data Models & API Reference

## Core Data Structures

### Task Definition

```go
type TaskType struct {
    ID          string        `json:"id"`
    Name        string        `json:"name"`
    Description string        `json:"description"`
    Interval    TaskInterval  `json:"interval"`
    Timeout     time.Duration `json:"timeout"`
    Retries     int          `json:"retries"`
    Config      TaskConfig   `json:"config"`
}

type TaskInterval struct {
    Type     IntervalType `json:"type"`     // "hourly", "daily", "weekly", "monthly"
    Value    int          `json:"value"`    // e.g., 2 for "every 2 hours"
    Timezone string       `json:"timezone"` // e.g., "UTC", "America/New_York"
    AtTime   *time.Time   `json:"at_time"`  // For daily tasks, specific time
}

type TaskConfig struct {
    Endpoint    string            `json:"endpoint"`
    Method      string            `json:"method"`
    Headers     map[string]string `json:"headers"`
    MaxDuration time.Duration     `json:"max_duration"`
}
```

### Job (Queued Task Instance)

```go
type Job struct {
    ID            string    `json:"id"`
    TaskTypeID    string    `json:"task_type_id"`
    UserID        string    `json:"user_id"`
    ScheduledAt   time.Time `json:"scheduled_at"`
    ExecuteAfter  time.Time `json:"execute_after"`
    Attempt       int       `json:"attempt"`
    Priority      Priority  `json:"priority"`
    Payload       []byte    `json:"payload"`
    CreatedAt     time.Time `json:"created_at"`
}

type Priority int
const (
    PriorityLow Priority = iota
    PriorityNormal
    PriorityHigh
)
```

### User Preferences

```go
type UserPreference struct {
    UserID         string              `json:"user_id"`
    EnabledTasks   map[string]bool     `json:"enabled_tasks"`
    TaskSettings   map[string]Settings `json:"task_settings"`
    LastModified   time.Time           `json:"last_modified"`
}

type Settings struct {
    CustomInterval *TaskInterval     `json:"custom_interval,omitempty"`
    Parameters     map[string]string `json:"parameters,omitempty"`
}
```

### Execution Result

```go
type ExecutionResult struct {
    JobID        string        `json:"job_id"`
    Status       Status        `json:"status"`
    StartedAt    time.Time     `json:"started_at"`
    CompletedAt  time.Time     `json:"completed_at"`
    Duration     time.Duration `json:"duration"`
    Error        *ErrorDetail  `json:"error,omitempty"`
    Response     []byte        `json:"response,omitempty"`
    NextAttempt  *time.Time    `json:"next_attempt,omitempty"`
}

type Status string
const (
    StatusPending   Status = "pending"
    StatusRunning   Status = "running"
    StatusSuccess   Status = "success"
    StatusFailed    Status = "failed"
    StatusRetrying  Status = "retrying"
)

type ErrorDetail struct {
    Code    string `json:"code"`
    Message string `json:"message"`
    Retry   bool   `json:"retry"`
}
```

## Redis Schema

### Key Patterns

```
# Scheduling
taskqueue:schedule:{task_type_id}              # STRING: last execution timestamp
taskqueue:schedule:lock:{task_type_id}         # STRING: distributed lock for scheduler

# User Preferences  
taskqueue:users:{user_id}:tasks                # HASH: task_id -> enabled (0/1)
taskqueue:users:{user_id}:settings:{task_id}   # HASH: custom settings

# Rate Limiting
taskqueue:ratelimit:global                     # STRING: token bucket state (Lua script)
taskqueue:ratelimit:task:{task_type_id}        # STRING: task-specific limits

# Metrics
taskqueue:metrics:executions:{date}            # HASH: task_id -> count
taskqueue:metrics:failures:{date}              # HASH: task_id -> count
taskqueue:metrics:duration:{task_id}           # ZSET: timestamp -> duration_ms

# Distribution
taskqueue:distribution:{task_type_id}:{slot}   # LIST: user_ids assigned to slot
taskqueue:distribution:lock                    # STRING: distribution process lock
```

### Redis Operations

```lua
-- Rate Limiting Script (rate_limit.lua)
local key = KEYS[1]
local max_tokens = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local requested = tonumber(ARGV[3])
local now = tonumber(ARGV[4])

local bucket = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(bucket[1]) or max_tokens
local last_refill = tonumber(bucket[2]) or now

-- Calculate tokens to add
local elapsed = now - last_refill
local tokens_to_add = elapsed * refill_rate
tokens = math.min(max_tokens, tokens + tokens_to_add)

-- Check if we can fulfill request
if tokens >= requested then
    tokens = tokens - requested
    redis.call('HMSET', key, 'tokens', tokens, 'last_refill', now)
    redis.call('EXPIRE', key, 3600)
    return 1
else
    return 0
end
```

## Message Queue Schema

### Queue Names

```
taskqueue.jobs.high      # High priority jobs
taskqueue.jobs.normal    # Normal priority jobs
taskqueue.jobs.low       # Low priority jobs
taskqueue.jobs.delayed   # Jobs with future execution time
taskqueue.jobs.failed    # Dead letter queue
```

### Message Format

```json
{
  "version": "1.0",
  "job": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "task_type_id": "daily_report",
    "user_id": "user_12345",
    "scheduled_at": "2024-01-15T10:00:00Z",
    "execute_after": "2024-01-15T10:15:30Z",
    "attempt": 1,
    "priority": 1,
    "payload": "eyJ1c2VyX2lkIjoiMTIzNDUifQ==",
    "created_at": "2024-01-15T09:00:00Z"
  },
  "metadata": {
    "correlation_id": "req_789",
    "source": "scheduler",
    "distribution_slot": 93
  }
}
```

## API Endpoints (Internal)

### Scheduler Service

```yaml
# Health Check
GET /health
Response: 
  200 OK
  {
    "status": "healthy",
    "version": "1.0.0",
    "uptime": 3600
  }

# Trigger Manual Schedule
POST /schedule/trigger
Body:
  {
    "task_type_id": "daily_report",
    "user_ids": ["user1", "user2"],
    "execute_immediately": false
  }
Response:
  202 Accepted
  {
    "jobs_created": 2,
    "distribution_window": "30m"
  }

# Get Schedule Status
GET /schedule/status/{task_type_id}
Response:
  200 OK
  {
    "task_type_id": "daily_report",
    "last_execution": "2024-01-15T00:00:00Z",
    "next_execution": "2024-01-16T00:00:00Z",
    "enabled_users": 1523,
    "interval": {
      "type": "daily",
      "value": 1,
      "timezone": "UTC"
    }
  }
```

### Worker Service

```yaml
# Health Check  
GET /health
Response:
  200 OK
  {
    "status": "healthy",
    "workers": 10,
    "active_jobs": 3,
    "rate_limit": {
      "current": 45,
      "max": 100
    }
  }

# Get Worker Stats
GET /stats
Response:
  200 OK
  {
    "processed": 15234,
    "failed": 23,
    "avg_duration_ms": 234,
    "current_rate": 45.2,
    "uptime": 7200
  }
```

## Configuration Schema

### Scheduler Config

```yaml
scheduler:
  check_interval: 1m
  distribution_window: 30m
  batch_size: 1000
  
redis:
  addresses:
    - localhost:6379
  password: ""
  db: 0
  max_retries: 3
  pool_size: 10

queue:
  url: "amqp://guest:guest@localhost:5672/"
  exchange: "taskqueue"
  durable: true
  prefetch: 10

tasks:
  config_file: "tasks.yaml"
  reload_interval: 5m
```

### Worker Config

```yaml
worker:
  pool_size: 10
  shutdown_timeout: 30s
  
rate_limit:
  global: 100
  per_task:
    api_heavy: 10
    report_generation: 50
    
execution:
  timeout: 5m
  max_retries: 3
  retry_delay: 1m
  
monitoring:
  metrics_interval: 10s
  log_level: "info"
```

### Task Definitions (tasks.yaml)

```yaml
tasks:
  - id: daily_report
    name: "Daily Report Generation"
    description: "Generates daily activity reports for users"
    interval:
      type: daily
      value: 1
      timezone: UTC
      at_time: "00:00"
    timeout: 5m
    retries: 3
    config:
      endpoint: "https://api.example.com/reports/daily"
      method: POST
      headers:
        Content-Type: application/json
      max_duration: 4m

  - id: hourly_sync
    name: "Hourly Data Sync"
    description: "Syncs user data every hour"
    interval:
      type: hourly
      value: 1
    timeout: 2m
    retries: 2
    config:
      endpoint: "https://api.example.com/sync"
      method: GET
```