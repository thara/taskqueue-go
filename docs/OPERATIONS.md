# Operations Guide

## Deployment

### Prerequisites

- Go 1.21+
- Redis 7.0+
- Message Queue (compatible with github.com/thara/message-queue-go)
- Docker & Docker Compose (for local development)

### Building

```bash
# Build all binaries
make build

# Build specific component
make build-scheduler
make build-worker

# Build with specific version
VERSION=1.0.0 make build
```

### Running Locally

```bash
# Start dependencies
docker-compose up -d

# Run scheduler
./bin/scheduler -config config/scheduler.yaml

# Run worker(s)
./bin/worker -config config/worker.yaml

# Run multiple workers
for i in {1..5}; do
  ./bin/worker -config config/worker.yaml &
done
```

### Production Deployment

#### Systemd Service Files

```ini
# /etc/systemd/system/taskqueue-scheduler.service
[Unit]
Description=TaskQueue Scheduler
After=network.target redis.service

[Service]
Type=simple
User=taskqueue
Group=taskqueue
ExecStart=/opt/taskqueue/bin/scheduler -config /etc/taskqueue/scheduler.yaml
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=taskqueue-scheduler

[Install]
WantedBy=multi-user.target
```

```ini
# /etc/systemd/system/taskqueue-worker@.service
[Unit]
Description=TaskQueue Worker %i
After=network.target redis.service

[Service]
Type=simple
User=taskqueue
Group=taskqueue
ExecStart=/opt/taskqueue/bin/worker -config /etc/taskqueue/worker.yaml
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=taskqueue-worker-%i
Environment="WORKER_ID=%i"

[Install]
WantedBy=multi-user.target
```

#### Starting Services

```bash
# Enable services
sudo systemctl enable taskqueue-scheduler
sudo systemctl enable taskqueue-worker@{1..10}

# Start services
sudo systemctl start taskqueue-scheduler
sudo systemctl start taskqueue-worker@{1..10}

# Check status
sudo systemctl status taskqueue-scheduler
sudo systemctl status taskqueue-worker@*
```

## Monitoring

### Key Metrics to Monitor

#### Scheduler Metrics
- **schedule_check_duration**: Time to check all task intervals
- **tasks_scheduled_total**: Total tasks scheduled per interval
- **schedule_errors_total**: Scheduling failures
- **last_execution_lag**: Time since last successful execution

#### Worker Metrics
- **jobs_processed_total**: Total jobs processed
- **jobs_failed_total**: Total job failures
- **job_duration_seconds**: Job execution time histogram
- **rate_limit_wait_seconds**: Time spent waiting for rate limit
- **worker_pool_size**: Current number of active workers

#### System Metrics
- **redis_connection_errors**: Redis connection failures
- **queue_depth**: Number of pending jobs
- **queue_connection_errors**: Message queue failures
- **memory_usage_bytes**: Process memory usage

### Prometheus Configuration

```yaml
# prometheus.yml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'taskqueue-scheduler'
    static_configs:
      - targets: ['localhost:9090']
        labels:
          component: 'scheduler'

  - job_name: 'taskqueue-workers'
    static_configs:
      - targets: ['localhost:9091', 'localhost:9092', 'localhost:9093']
        labels:
          component: 'worker'
```

### Grafana Dashboard

Key panels to include:
1. Task Scheduling Rate (tasks/minute)
2. Job Processing Rate (jobs/second)
3. Error Rate by Task Type
4. Rate Limiter Utilization
5. Queue Depth Over Time
6. P50/P95/P99 Job Duration
7. Worker Pool Utilization
8. Redis Operations/Second

### Alerting Rules

```yaml
# alerts.yml
groups:
  - name: taskqueue
    rules:
      - alert: SchedulerDown
        expr: up{component="scheduler"} == 0
        for: 5m
        annotations:
          summary: "Scheduler is down"
          
      - alert: HighQueueDepth
        expr: taskqueue_queue_depth > 10000
        for: 10m
        annotations:
          summary: "Queue depth is high: {{ $value }}"
          
      - alert: HighErrorRate
        expr: rate(taskqueue_jobs_failed_total[5m]) > 10
        for: 5m
        annotations:
          summary: "High job failure rate: {{ $value }}/sec"
          
      - alert: RateLimiterSaturated
        expr: taskqueue_rate_limit_utilization > 0.9
        for: 5m
        annotations:
          summary: "Rate limiter at {{ $value }}% capacity"
```

## Troubleshooting

### Common Issues

#### 1. Scheduler Not Triggering Tasks

**Symptoms**: Tasks not being scheduled at expected intervals

**Check**:
```bash
# Check scheduler logs
journalctl -u taskqueue-scheduler -f

# Verify Redis connectivity
redis-cli ping

# Check last execution times
redis-cli --scan --pattern "taskqueue:schedule:*"

# Verify task definitions loaded
curl http://localhost:8080/schedule/status/daily_report
```

**Solutions**:
- Ensure Redis is accessible
- Verify task definitions are loaded correctly
- Check for scheduler lock conflicts
- Review timezone settings

#### 2. Workers Not Processing Jobs

**Symptoms**: Queue depth increasing, jobs not being processed

**Check**:
```bash
# Check worker logs
journalctl -u taskqueue-worker@* -f

# Verify queue connectivity
# Check queue management UI

# Check rate limiter state
redis-cli get taskqueue:ratelimit:global

# Worker health check
curl http://localhost:8081/health
```

**Solutions**:
- Restart stuck workers
- Check rate limit configuration
- Verify queue permissions
- Increase worker pool size

#### 3. High Job Failure Rate

**Symptoms**: Many jobs failing, high retry rate

**Check**:
```bash
# Check failed job details
# Review dead letter queue

# Check specific task type errors
redis-cli zrange taskqueue:metrics:failures:$(date +%Y%m%d) 0 -1 WITHSCORES

# Review worker error logs
grep ERROR /var/log/taskqueue/worker.log | tail -100
```

**Solutions**:
- Review API endpoint availability
- Check task timeout settings
- Verify payload formats
- Implement circuit breakers

#### 4. Uneven Task Distribution

**Symptoms**: Some time slots overloaded, others empty

**Check**:
```bash
# Check distribution slots
redis-cli --scan --pattern "taskqueue:distribution:*"

# Analyze user distribution
./bin/taskqueue-admin analyze-distribution --task daily_report
```

**Solutions**:
- Review distribution algorithm
- Adjust time window size
- Implement adaptive distribution

### Performance Tuning

#### Redis Optimization

```bash
# Enable Redis persistence for reliability
redis-cli CONFIG SET save "900 1 300 10 60 10000"

# Increase max clients
redis-cli CONFIG SET maxclients 10000

# Enable TCP keepalive
redis-cli CONFIG SET tcp-keepalive 60
```

#### Worker Tuning

```yaml
# Optimal worker configuration
worker:
  pool_size: 20              # 2x CPU cores
  prefetch: 10               # Batch job fetching
  connection_pool: 50        # Redis connections
  
execution:
  timeout: 5m                # Reasonable timeout
  concurrent_requests: 100   # Per worker limit
```

#### Queue Optimization

- Use separate queues for different priority levels
- Implement queue depth monitoring
- Set appropriate TTLs for messages
- Configure dead letter queues

### Backup and Recovery

#### Backup Strategy

```bash
# Backup Redis data
redis-cli BGSAVE

# Backup task definitions
cp /etc/taskqueue/tasks.yaml /backup/tasks.yaml.$(date +%Y%m%d)

# Backup user preferences (scheduled)
0 2 * * * redis-cli --rdb /backup/redis-$(date +\%Y\%m\%d).rdb
```

#### Recovery Procedures

1. **Redis Recovery**:
```bash
# Stop services
systemctl stop taskqueue-scheduler taskqueue-worker@*

# Restore Redis backup
cp /backup/redis-20240115.rdb /var/lib/redis/dump.rdb
systemctl restart redis

# Start services
systemctl start taskqueue-scheduler taskqueue-worker@*
```

2. **Missed Execution Recovery**:
```bash
# Run catch-up for missed executions
./bin/taskqueue-admin catchup \
  --from "2024-01-15T00:00:00Z" \
  --to "2024-01-15T12:00:00Z" \
  --task-type daily_report
```

### Security Best Practices

1. **Redis Security**:
   - Enable authentication
   - Use TLS for connections
   - Restrict network access
   - Regular security updates

2. **API Security**:
   - Use HTTPS for all endpoints
   - Implement request signing
   - Rate limit API calls
   - Audit all executions

3. **Operational Security**:
   - Separate environments
   - Least privilege access
   - Regular security audits
   - Encrypted backups

## Maintenance Tasks

### Daily
- Review error logs
- Check queue depths
- Monitor rate limiter usage
- Verify all workers healthy

### Weekly
- Review performance metrics
- Analyze failure patterns
- Update task configurations
- Test backup procedures

### Monthly
- Performance optimization review
- Capacity planning
- Security audit
- Documentation updates

### Quarterly
- Disaster recovery drill
- Load testing
- Architecture review
- Dependency updates