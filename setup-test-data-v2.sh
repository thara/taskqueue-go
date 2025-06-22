#!/bin/bash

# Script to setup test data for demo with correct task names
# This creates test users with enabled tasks that match the actual task IDs

REDIS_CMD="docker exec taskqueue-redis redis-cli"

echo "Setting up test users and tasks (v2 - matching actual task names)..."

# Clear existing data first
echo "Clearing existing test data..."
$REDIS_CMD DEL "taskqueue:users:user1:tasks"
$REDIS_CMD DEL "taskqueue:users:user2:tasks" 
$REDIS_CMD DEL "taskqueue:users:user3:tasks"
$REDIS_CMD DEL "taskqueue:users:user4:tasks"
$REDIS_CMD DEL "taskqueue:users:user1:meta"
$REDIS_CMD DEL "taskqueue:users:user2:meta"
$REDIS_CMD DEL "taskqueue:users:user3:meta"
$REDIS_CMD DEL "taskqueue:users:user4:meta"
$REDIS_CMD DEL "taskqueue:users:all"

# User 1: Enable hourly tasks
echo "Creating user1 with hourly tasks..."
$REDIS_CMD HSET "taskqueue:users:user1:tasks" "hourly_data_sync" "1"
$REDIS_CMD HSET "taskqueue:users:user1:tasks" "hourly_cache_refresh" "1"
$REDIS_CMD HSET "taskqueue:users:user1:tasks" "bi_hourly_health_check" "1"
$REDIS_CMD HSET "taskqueue:users:user1:meta" "last_modified" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
$REDIS_CMD SADD "taskqueue:users:all" "user1"
$REDIS_CMD SADD "taskqueue:tasks:hourly_data_sync:users" "user1"
$REDIS_CMD SADD "taskqueue:tasks:hourly_cache_refresh:users" "user1"
$REDIS_CMD SADD "taskqueue:tasks:bi_hourly_health_check:users" "user1"

# User 2: Enable daily tasks
echo "Creating user2 with daily tasks..."
$REDIS_CMD HSET "taskqueue:users:user2:tasks" "daily_user_report" "1"
$REDIS_CMD HSET "taskqueue:users:user2:tasks" "daily_system_metrics" "1"
$REDIS_CMD HSET "taskqueue:users:user2:meta" "last_modified" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
$REDIS_CMD SADD "taskqueue:users:all" "user2"
$REDIS_CMD SADD "taskqueue:tasks:daily_user_report:users" "user2"
$REDIS_CMD SADD "taskqueue:tasks:daily_system_metrics:users" "user2"

# User 3: Enable weekly tasks
echo "Creating user3 with weekly tasks..."
$REDIS_CMD HSET "taskqueue:users:user3:tasks" "weekly_backup" "1"
$REDIS_CMD HSET "taskqueue:users:user3:tasks" "weekly_analytics" "1"
$REDIS_CMD HSET "taskqueue:users:user3:tasks" "bi_weekly_report" "1"
$REDIS_CMD HSET "taskqueue:users:user3:meta" "last_modified" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
$REDIS_CMD SADD "taskqueue:users:all" "user3"
$REDIS_CMD SADD "taskqueue:tasks:weekly_backup:users" "user3"
$REDIS_CMD SADD "taskqueue:tasks:weekly_analytics:users" "user3"
$REDIS_CMD SADD "taskqueue:tasks:bi_weekly_report:users" "user3"

# User 4: Enable monthly tasks for testing
echo "Creating user4 with monthly tasks..."
$REDIS_CMD HSET "taskqueue:users:user4:tasks" "monthly_billing" "1"
$REDIS_CMD HSET "taskqueue:users:user4:tasks" "monthly_cleanup" "1"
$REDIS_CMD HSET "taskqueue:users:user4:meta" "last_modified" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
$REDIS_CMD SADD "taskqueue:users:all" "user4"
$REDIS_CMD SADD "taskqueue:tasks:monthly_billing:users" "user4"
$REDIS_CMD SADD "taskqueue:tasks:monthly_cleanup:users" "user4"

echo "Test data setup complete (v2)!"
echo "Users created: user1, user2, user3, user4"
echo ""
echo "Enabled tasks:"
echo "- user1: hourly_data_sync, hourly_cache_refresh, bi_hourly_health_check"
echo "- user2: daily_user_report, daily_system_metrics"
echo "- user3: weekly_backup, weekly_analytics, bi_weekly_report"
echo "- user4: monthly_billing, monthly_cleanup"
echo ""
echo "You should see jobs being scheduled in the next 10 seconds..."