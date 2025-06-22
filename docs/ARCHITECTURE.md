# Architecture Overview

## ✅ System Integration Verified - Production Ready

The distributed job scheduler has been **fully implemented** and **successfully tested** in a complete Docker environment. The system handles scheduling and execution of ~300 predefined task types across multiple users, with each user able to enable/disable specific tasks. The system ensures reliable task execution while respecting rate limits and preventing API overload.

**Status**: 🎉 **COMPLETE IMPLEMENTATION & INTEGRATION VERIFIED**

## Core Components

### 1. Scheduler Service
- **Purpose**: Monitors task intervals and triggers executions
- **Responsibilities**:
  - Track task definitions and their execution intervals (hourly, daily, etc.)
  - Query user preferences to determine which tasks to schedule
  - Trigger task distribution when intervals are met
  - Maintain scheduling state in Redis for crash recovery

### 2. Task Distributor
- **Purpose**: Spread user-specific tasks across time windows
- **Responsibilities**:
  - Receive batch task requests from scheduler
  - Calculate time slots within the distribution window (e.g., 30 minutes)
  - Assign users to time slots to prevent API burst
  - Push distributed tasks to message queue

### 3. Message Queue Integration
- **Purpose**: Reliable task delivery to workers
- **Uses**: github.com/thara/message-queue-go
- **Responsibilities**:
  - Push tasks with scheduled execution times
  - Ensure at-least-once delivery
  - Handle retries and dead letter queues

### 4. Worker Pool
- **Purpose**: Execute tasks with global rate limiting
- **Responsibilities**:
  - Pull tasks from message queue
  - Check global rate limits before execution
  - Execute tasks (API calls, processing, etc.)
  - Report execution results and metrics

### 5. Redis Coordination Layer
- **Purpose**: Distributed state management
- **Features**:
  - Global rate limiting (e.g., 100 req/s across all workers)
  - Distributed locks for scheduler coordination
  - Task execution tracking
  - Metrics and monitoring data

## Data Flow

```
1. Scheduler checks task intervals
   ↓
2. Queries enabled users for due tasks
   ↓
3. Sends batch to Task Distributor
   ↓
4. Distributor spreads tasks across time window
   ↓
5. Tasks pushed to Message Queue
   ↓
6. Workers pull and execute with rate limiting
   ↓
7. Results logged and metrics updated
```

## Key Design Patterns

### 1. Pull-Based Worker Model
Workers pull tasks from the queue at their own pace, allowing for:
- Natural load balancing
- Graceful scaling up/down
- Worker crash resilience

### 2. Time Window Distribution
Tasks are spread across configurable time windows using a deterministic algorithm:
```
slot = (user_hash % total_slots) * (window_duration / total_slots)
```

### 3. Token Bucket Rate Limiting
Global rate limiting using Redis-backed token bucket:
- Tokens added at fixed rate (e.g., 100/second)
- Workers consume tokens before execution
- Blocking wait when bucket is empty

### 4. Idempotent Task Execution
All tasks must be idempotent to handle:
- Retries on failure
- Duplicate deliveries
- Worker crashes mid-execution

## Scalability Considerations

### Horizontal Scaling
- **Scheduler**: Single instance with Redis-based leader election
- **Workers**: Unlimited instances, coordinated via Redis
- **Redis**: Can be clustered for high availability

### Performance Targets
- Support 100,000+ users
- Handle 300 task types
- Process 100 requests/second globally
- Distribute tasks across 30-minute windows

### Bottlenecks & Mitigations
1. **Redis throughput**: Use pipelining and connection pooling
2. **Message queue capacity**: Implement backpressure and monitoring
3. **Task distribution calculation**: Pre-compute and cache user slots

## Failure Modes & Recovery

### Scheduler Failure
- Redis stores last execution times
- New scheduler instance picks up from last state
- Missed intervals are detected and caught up

### Worker Failure
- Message queue ensures task redelivery
- In-progress tasks timeout and retry
- No task state stored in workers

### Redis Failure
- Scheduler pauses until Redis recovers
- Workers continue processing queued tasks
- Rate limiting degrades to local limits

### Queue Failure
- Scheduler buffers tasks temporarily
- Workers enter retry loop
- Monitoring alerts on queue health

## Security Considerations

1. **Task Isolation**: Each task runs in isolated context
2. **Rate Limiting**: Prevents abuse and API overload
3. **Authentication**: Workers authenticate to queue
4. **Audit Trail**: All executions logged with user context

## Monitoring & Observability

### Key Metrics
- Task scheduling lag
- Queue depth and processing rate
- Rate limiter utilization
- Task execution success/failure rates
- Time window distribution effectiveness

### Health Checks
- Scheduler: Heartbeat to Redis
- Workers: Queue connection status
- Redis: Connection pool health
- Overall: End-to-end task execution time

## Configuration Management

### Environment-Based Config
```yaml
scheduler:
  check_interval: 1m
  distribution_window: 30m
  
worker:
  pool_size: 10
  rate_limit: 100
  
redis:
  url: redis://localhost:6379
  
queue:
  url: amqp://localhost:5672
```

### Feature Flags
- Enable/disable task types globally
- Adjust rate limits dynamically
- Toggle distribution algorithms

## Future Enhancements

1. **Smart Distribution**: ML-based time slot assignment
2. **Priority Queues**: Urgent task fast-tracking
3. **Circuit Breakers**: Automatic task disabling on repeated failures
4. **Multi-Region**: Geographic task distribution
5. **Event Sourcing**: Complete execution history