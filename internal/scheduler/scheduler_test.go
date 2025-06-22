package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thara/taskqueue-go/internal/models"
	"github.com/thara/taskqueue-go/internal/queue"
	"github.com/thara/taskqueue-go/internal/storage"
)

func TestNew(t *testing.T) {
	redisClient := createTestRedisClient(t)
	queueClient := createTestQueueClient(t)
	registry := createTestRegistry(t)
	userStore := storage.NewUserStore(redisClient, nil)

	config := Config{
		CheckInterval:        10 * time.Second,
		BatchSize:            100,
		DistributionWindow:   5 * time.Minute,
		LeaderElectionTTL:    30 * time.Second,
		EnableLeaderElection: true,
	}

	scheduler := New(config, redisClient, queueClient, registry, userStore)

	assert.NotNil(t, scheduler)
	assert.Equal(t, config, scheduler.config)
	assert.Equal(t, redisClient, scheduler.redisClient)
	assert.Equal(t, queueClient, scheduler.queueClient)
	assert.Equal(t, registry, scheduler.registry)
	assert.Equal(t, userStore, scheduler.userStore)
	assert.NotNil(t, scheduler.distributor)
	assert.Equal(t, "taskqueue:scheduler:leader", scheduler.leaderKey)
}

func TestScheduler_AcquireLeadership(t *testing.T) {
	tests := []struct {
		name              string
		existingLeader    bool
		expectedLeader    bool
		expectedError     bool
	}{
		{
			name:           "acquire leadership when none exists",
			existingLeader: false,
			expectedLeader: true,
			expectedError:  false,
		},
		{
			name:           "fail to acquire when leader exists",
			existingLeader: true,
			expectedLeader: false,
			expectedError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisClient := createTestRedisClient(t)
			queueClient := createTestQueueClient(t)
			registry := createTestRegistry(t)
			userStore := storage.NewUserStore(redisClient, nil)

			scheduler := New(Config{
				LeaderElectionTTL:    1 * time.Second,
				EnableLeaderElection: true,
			}, redisClient, queueClient, registry, userStore)

			ctx := context.Background()

			// Set up existing leader if needed
			if tt.existingLeader {
				err := redisClient.Set(ctx, scheduler.leaderKey, "other-leader", time.Minute).Err()
				require.NoError(t, err)
			}

			isLeader, err := scheduler.acquireLeadership(ctx)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedLeader, isLeader)
			}
		})
	}
}

func TestScheduler_FindDueTasks(t *testing.T) {
	redisClient := createTestRedisClient(t)
	queueClient := createTestQueueClient(t)
	registry := createTestRegistry(t)
	userStore := storage.NewUserStore(redisClient, nil)

	scheduler := New(Config{}, redisClient, queueClient, registry, userStore)

	ctx := context.Background()

	// Create a test task type
	taskType := &models.TaskType{
		ID:   "test_task",
		Name: "Test Task",
		Interval: models.TaskInterval{
			Type:  models.IntervalTypeHourly,
			Value: 1,
		},
	}

	// Add users for this task
	err := userStore.EnableTaskForUser(ctx, "user1", "test_task")
	require.NoError(t, err)
	err = userStore.EnableTaskForUser(ctx, "user2", "test_task")
	require.NoError(t, err)

	now := time.Now()

	tests := []struct {
		name              string
		lastExecution     time.Time
		currentTime       time.Time
		expectedTaskCount int
	}{
		{
			name:              "task is due - never executed",
			lastExecution:     time.Time{},
			currentTime:       now,
			expectedTaskCount: 2, // 2 users
		},
		{
			name:              "task is due - last executed over an hour ago",
			lastExecution:     now.Add(-2 * time.Hour),
			currentTime:       now,
			expectedTaskCount: 2,
		},
		{
			name:              "task not due - executed recently",
			lastExecution:     now.Add(-30 * time.Minute),
			currentTime:       now,
			expectedTaskCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set last execution time if provided
			if !tt.lastExecution.IsZero() {
				lastExecKey := "taskqueue:schedule:" + taskType.ID
				err := redisClient.Set(ctx, lastExecKey, tt.lastExecution.Format(time.RFC3339), 0).Err()
				require.NoError(t, err)
			}

			tasks, err := scheduler.findDueTasks(ctx, taskType, tt.currentTime)
			assert.NoError(t, err)
			assert.Len(t, tasks, tt.expectedTaskCount)

			// Verify task details if any tasks were found
			for _, task := range tasks {
				assert.Equal(t, taskType, task.TaskType)
				assert.Contains(t, []string{"user1", "user2"}, task.UserID)
			}
		})
	}
}

func TestScheduler_UpdateLastExecutions(t *testing.T) {
	redisClient := createTestRedisClient(t)
	queueClient := createTestQueueClient(t)
	registry := createTestRegistry(t)
	userStore := storage.NewUserStore(redisClient, nil)

	scheduler := New(Config{}, redisClient, queueClient, registry, userStore)

	ctx := context.Background()
	scheduledAt := time.Now()

	taskType1 := &models.TaskType{ID: "task1"}
	taskType2 := &models.TaskType{ID: "task2"}

	tasks := []taskToSchedule{
		{TaskType: taskType1, UserID: "user1"},
		{TaskType: taskType1, UserID: "user2"}, // Same task type
		{TaskType: taskType2, UserID: "user1"},
	}

	err := scheduler.updateLastExecutions(ctx, tasks, scheduledAt)
	assert.NoError(t, err)

	// Verify last execution times were set
	lastExec1, err := redisClient.Get(ctx, "taskqueue:schedule:task1").Result()
	assert.NoError(t, err)
	assert.Equal(t, scheduledAt.Format(time.RFC3339), lastExec1)

	lastExec2, err := redisClient.Get(ctx, "taskqueue:schedule:task2").Result()
	assert.NoError(t, err)
	assert.Equal(t, scheduledAt.Format(time.RFC3339), lastExec2)
}

func TestScheduler_CheckAndScheduleTasks_NoLeaderElection(t *testing.T) {
	redisClient := createTestRedisClient(t)
	queueClient := createTestQueueClient(t)
	registry := createTestRegistry(t)
	userStore := storage.NewUserStore(redisClient, nil)

	scheduler := New(Config{
		EnableLeaderElection: false,
		DistributionWindow:   1 * time.Minute,
	}, redisClient, queueClient, registry, userStore)

	ctx := context.Background()

	// Add a user with an enabled task
	err := userStore.EnableTaskForUser(ctx, "user1", "test_task")
	require.NoError(t, err)

	// This should not error even with no due tasks
	err = scheduler.checkAndScheduleTasks(ctx)
	assert.NoError(t, err)
}

func TestScheduler_CheckAndScheduleTasks_WithLeaderElection(t *testing.T) {
	redisClient := createTestRedisClient(t)
	queueClient := createTestQueueClient(t)
	registry := createTestRegistry(t)
	userStore := storage.NewUserStore(redisClient, nil)

	scheduler := New(Config{
		EnableLeaderElection: true,
		LeaderElectionTTL:    1 * time.Second,
		DistributionWindow:   1 * time.Minute,
	}, redisClient, queueClient, registry, userStore)

	ctx := context.Background()

	// Should acquire leadership and run
	err := scheduler.checkAndScheduleTasks(ctx)
	assert.NoError(t, err)

	// Verify leadership was acquired
	leader, err := redisClient.Get(ctx, scheduler.leaderKey).Result()
	assert.NoError(t, err)
	assert.Equal(t, "leader", leader)
}

func TestTaskToSchedule_Interface(t *testing.T) {
	taskType := &models.TaskType{
		ID:   "test_task",
		Name: "Test Task",
	}

	task := taskToSchedule{
		TaskType: taskType,
		UserID:   "user123",
	}

	// Test interface methods
	assert.Equal(t, taskType, task.GetTaskType())
	assert.Equal(t, "user123", task.GetUserID())
}

// Helper functions

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

func createTestQueueClient(t *testing.T) *queue.Client {
	redisClient := createTestRedisClient(t)
	
	queueClient, err := queue.NewClient(&queue.Config{
		RedisAddr:         redisClient.Options().Addr,
		QueueName:         "test:queue",
		MaxRetries:        3,
		DefaultVisibility: 30 * time.Second,
	})
	require.NoError(t, err)
	
	return queueClient
}

func createTestRegistry(t *testing.T) *storage.TaskRegistry {
	registry := storage.NewTaskRegistry("", nil)
	
	// Add a test task type directly to the registry for testing
	// We'll use reflection or create a method to add tasks for testing
	return registry
}