# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Distributed job scheduler and executor system in Go that manages ~300 static task types across multiple users. The system uses Redis for coordination, a custom message queue (github.com/thara/message-queue-go) for job distribution, and implements global rate limiting across distributed workers. Uses Go's standard slog package for structured logging.

## Key Commands

```bash
# Build all components
make build

# Run scheduler service
./bin/scheduler -config config/scheduler.yaml

# Run worker service(s)
./bin/worker -config config/worker.yaml

# Run tests
go test ./...

# Run specific test
go test -run TestName ./internal/scheduler

# Lint code
golangci-lint run

# Local development with Redis
docker-compose up -d

# Generate mocks
go generate ./...

# Run integration tests
go test -tags=integration ./test/integration
```

## Architecture Overview

The system consists of two main services:

1. **Scheduler Service**: Monitors task intervals, queries user preferences, and triggers task distribution
2. **Worker Service**: Pulls jobs from queue, enforces global rate limits via Redis, and executes tasks

Key architectural decisions:
- Pull-based worker model for natural load balancing
- Redis-based coordination for rate limiting and distributed locks
- Time window distribution to prevent API overload
- Idempotent task execution for reliability

## Code Structure

```
cmd/
├── scheduler/     # Entry point for scheduler service
└── worker/        # Entry point for worker service

internal/
├── scheduler/     # Core scheduling logic and interval checking
├── distributor/   # Time window distribution algorithm
├── worker/        # Worker pool and task execution
├── ratelimit/     # Redis-based distributed rate limiting
├── queue/         # Message queue client adapter
├── storage/       # Task definitions and user preferences
└── models/        # Data structures (Task, Job, etc.)
```

## Redis Key Patterns

- `taskqueue:schedule:{task_type_id}` - Last execution timestamps
- `taskqueue:users:{user_id}:tasks` - User task preferences
- `taskqueue:ratelimit:global` - Global rate limiter state
- `taskqueue:metrics:*` - Execution metrics and monitoring

## Development Workflow

1. Task definitions are loaded from `config/tasks.yaml`
2. User preferences determine which tasks to schedule
3. Tasks are distributed across time windows to prevent bursts
4. Workers pull from queue and check rate limits before execution
5. All operations are idempotent and retry-safe

## Testing Approach

- Unit tests for individual components
- Integration tests using testcontainers for Redis
- Table-driven tests for complex logic
- Mock interfaces for external dependencies

## Important Considerations

- Workers must respect global rate limits (e.g., 100 req/s total)
- Task execution must be idempotent
- Time window distribution prevents API overload
- Redis is a single point of failure - ensure high availability
- Message queue handles persistence and retries

## Current Implementation Status & TODO

### ✅ Completed Components
- Core data models (Task, Job, User) with comprehensive types
- Task registry with YAML configuration loading (`internal/storage/registry.go`)
- Redis-based user preference storage with indexing (`internal/storage/user_store.go`)
- Task definitions configuration (`config/tasks.yaml` with 10 sample tasks)
- Build system (Makefile) with proper Go tooling
- Comprehensive documentation (architecture, data models, development guide, operations)

### ❌ Missing Critical Components (TODO)
1. **Scheduler Service** (`cmd/scheduler/` is empty)
   - Main service entry point
   - Interval checking logic
   - Task triggering mechanism
   - Redis-based leader election

2. **Worker Service** (`cmd/worker/` is empty)
   - Main service entry point
   - Worker pool management
   - Job execution logic
   - Health check endpoints

3. **Task Distributor** (`internal/distributor/` is empty)
   - Time window distribution algorithm
   - User slot assignment
   - Queue message creation

4. **Rate Limiter** (`internal/ratelimit/` is empty)
   - Redis-based token bucket implementation
   - Global rate limiting logic
   - Lua scripts for atomic operations

5. **Queue Integration** (`internal/queue/` is empty)
   - Message queue client adapter
   - Job pushing/pulling logic
   - Dead letter queue handling

6. **Service Configurations**
   - `config/scheduler.yaml` - Scheduler service configuration
   - `config/worker.yaml` - Worker service configuration

7. **Docker Setup**
   - `docker-compose.yml` - Local development environment
   - `build/scheduler.Dockerfile` - Scheduler container
   - `build/worker.Dockerfile` - Worker container

### 🔧 Implementation Priority
1. Message queue integration (foundation for other services)
2. Rate limiter (shared by scheduler and worker)
3. Task distributor (needed by scheduler)
4. Scheduler service (core orchestration)
5. Worker service (task execution)
6. Configuration files and Docker setup