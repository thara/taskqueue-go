package distributor

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thara/taskqueue-go/internal/models"
	"github.com/thara/taskqueue-go/internal/queue"
)

// Mock task to schedule for testing
type mockTaskToSchedule struct {
	taskType *models.TaskType
	userID   string
}

func (m *mockTaskToSchedule) GetTaskType() *models.TaskType {
	return m.taskType
}

func (m *mockTaskToSchedule) GetUserID() string {
	return m.userID
}

func TestNew(t *testing.T) {
	queueClient := createTestQueueClient(t)
	distributionWindow := 5 * time.Minute

	distributor := New(distributionWindow, queueClient)

	assert.NotNil(t, distributor)
	assert.Equal(t, distributionWindow, distributor.distributionWindow)
	assert.Equal(t, queueClient, distributor.queueClient)
}

func TestDistributor_GetDistributionWindow(t *testing.T) {
	queueClient := createTestQueueClient(t)
	window := 10 * time.Minute

	distributor := New(window, queueClient)

	assert.Equal(t, window, distributor.GetDistributionWindow())
}

func TestDistributor_SetDistributionWindow(t *testing.T) {
	queueClient := createTestQueueClient(t)
	initialWindow := 5 * time.Minute
	newWindow := 15 * time.Minute

	distributor := New(initialWindow, queueClient)
	assert.Equal(t, initialWindow, distributor.GetDistributionWindow())

	distributor.SetDistributionWindow(newWindow)
	assert.Equal(t, newWindow, distributor.GetDistributionWindow())
}

func TestDistributor_CalculateTimeSlots(t *testing.T) {
	queueClient := createTestQueueClient(t)
	distributor := New(60*time.Second, queueClient)

	tests := []struct {
		name               string
		taskCount          int
		expectedSlotCount  int
		shouldHaveJitter   bool
	}{
		{
			name:              "no tasks",
			taskCount:         0,
			expectedSlotCount: 0,
			shouldHaveJitter:  false,
		},
		{
			name:              "single task",
			taskCount:         1,
			expectedSlotCount: 1,
			shouldHaveJitter:  false,
		},
		{
			name:              "multiple tasks",
			taskCount:         4,
			expectedSlotCount: 4,
			shouldHaveJitter:  true,
		},
		{
			name:              "many tasks",
			taskCount:         60,
			expectedSlotCount: 60,
			shouldHaveJitter:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slots := distributor.calculateTimeSlots(tt.taskCount)

			assert.Len(t, slots, tt.expectedSlotCount)

			if tt.taskCount == 0 {
				return
			}

			if tt.taskCount == 1 {
				assert.Equal(t, time.Duration(0), slots[0])
				return
			}

			// Verify slots are in ascending order
			for i := 1; i < len(slots); i++ {
				assert.True(t, slots[i] >= slots[i-1], "slots should be in ascending order")
			}

			// Verify all slots are within the distribution window
			for _, slot := range slots {
				assert.True(t, slot >= 0, "slot should be non-negative")
				// Allow some jitter beyond the window
				assert.True(t, slot <= distributor.distributionWindow+15*time.Second, "slot should not exceed window + jitter")
			}

			// For multiple tasks, verify there's some spacing
			if tt.taskCount > 1 {
				maxSlot := slots[len(slots)-1]
				assert.True(t, maxSlot > 0, "should have some distribution across time")
			}
		})
	}
}

func TestDistributor_EstimateDistributionTime(t *testing.T) {
	queueClient := createTestQueueClient(t)
	window := 60 * time.Second
	distributor := New(window, queueClient)

	tests := []struct {
		name      string
		taskCount int
		expected  time.Duration
	}{
		{
			name:      "no tasks",
			taskCount: 0,
			expected:  0,
		},
		{
			name:      "single task",
			taskCount: 1,
			expected:  0,
		},
		{
			name:      "normal load",
			taskCount: 10,
			expected:  window,
		},
		{
			name:      "high load requiring min interval",
			taskCount: 120, // More than window seconds
			expected:  120 * time.Second, // Should use min interval
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimated := distributor.EstimateDistributionTime(tt.taskCount)
			assert.Equal(t, tt.expected, estimated)
		})
	}
}

func TestDistributor_DistributeTasks(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		taskCount int
		shouldErr bool
	}{
		{
			name:      "no tasks",
			taskCount: 0,
			shouldErr: false,
		},
		{
			name:      "single task",
			taskCount: 1,
			shouldErr: false,
		},
		{
			name:      "multiple tasks",
			taskCount: 5,
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queueClient := createTestQueueClient(t)
			distributor := New(60*time.Second, queueClient)

			// Create mock tasks
			tasks := make([]TaskToSchedule, tt.taskCount)
			for i := 0; i < tt.taskCount; i++ {
				tasks[i] = &mockTaskToSchedule{
					taskType: &models.TaskType{
						ID:      "test_task",
						Name:    "Test Task",
						Timeout: 30 * time.Second,
						Retries: 3,
						Config: models.TaskConfig{
							Endpoint: "http://example.com/test",
							Method:   "POST",
						},
					},
					userID: "user1",
				}
			}

			baseTime := time.Now()
			err := distributor.DistributeTasks(ctx, tasks, baseTime)

			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// For non-zero task counts, verify jobs were created
				if tt.taskCount > 0 {
					// Pull a job from the queue to verify it was created
					msg, err := queueClient.Pull(ctx)
					assert.NoError(t, err)
					
					if msg != nil {
						assert.NotNil(t, msg.QueueMessage)
						assert.NotNil(t, msg.QueueMessage.Job)
						assert.Equal(t, "test_task", msg.QueueMessage.Job.TaskTypeID)
						assert.Equal(t, "user1", msg.QueueMessage.Job.UserID)
						
						// Acknowledge the message
						err = queueClient.Ack(ctx, msg.MessageID, msg.Receipt)
						assert.NoError(t, err)
					}
				}
			}
		})
	}
}

func TestDistributor_DistributeTasks_ExecuteAfterTiming(t *testing.T) {
	ctx := context.Background()
	queueClient := createTestQueueClient(t)
	distributor := New(60*time.Second, queueClient)

	// Create a single task
	task := &mockTaskToSchedule{
		taskType: &models.TaskType{
			ID:      "timing_test",
			Name:    "Timing Test Task",
			Timeout: 30 * time.Second,
			Retries: 1,
			Config: models.TaskConfig{
				Endpoint: "http://example.com/test",
				Method:   "GET",
			},
		},
		userID: "user1",
	}

	baseTime := time.Now()
	err := distributor.DistributeTasks(ctx, []TaskToSchedule{task}, baseTime)
	require.NoError(t, err)

	// Pull the job and verify timing
	msg, err := queueClient.Pull(ctx)
	require.NoError(t, err)
	require.NotNil(t, msg)

	job := msg.QueueMessage.Job
	
	// ExecuteAfter should be at or after baseTime
	assert.True(t, job.ExecuteAfter.After(baseTime) || job.ExecuteAfter.Equal(baseTime))
	
	// ExecuteAfter should be within reasonable bounds (baseTime + distribution window + jitter)
	maxExpectedTime := baseTime.Add(distributor.distributionWindow + 15*time.Second)
	assert.True(t, job.ExecuteAfter.Before(maxExpectedTime))

	// Acknowledge the message
	err = queueClient.Ack(ctx, msg.MessageID, msg.Receipt)
	assert.NoError(t, err)
}

func TestDistributor_DistributeTasks_TaskShuffle(t *testing.T) {
	ctx := context.Background()
	queueClient := createTestQueueClient(t)
	distributor := New(60*time.Second, queueClient)

	// Create multiple tasks with different user IDs
	tasks := make([]TaskToSchedule, 3)
	for i := 0; i < 3; i++ {
		tasks[i] = &mockTaskToSchedule{
			taskType: &models.TaskType{
				ID:      "shuffle_test",
				Name:    "Shuffle Test Task",
				Timeout: 30 * time.Second,
				Retries: 1,
				Config: models.TaskConfig{
					Endpoint: "http://example.com/test",
					Method:   "GET",
				},
			},
			userID: "user" + string(rune('1'+i)),
		}
	}

	baseTime := time.Now()
	err := distributor.DistributeTasks(ctx, tasks, baseTime)
	require.NoError(t, err)

	// Pull all jobs and verify they were created
	userIDs := make(map[string]bool)
	for i := 0; i < 3; i++ {
		msg, err := queueClient.Pull(ctx)
		require.NoError(t, err)
		require.NotNil(t, msg)

		userIDs[msg.QueueMessage.Job.UserID] = true

		// Acknowledge the message
		err = queueClient.Ack(ctx, msg.MessageID, msg.Receipt)
		require.NoError(t, err)
	}

	// Verify all users were processed
	assert.Len(t, userIDs, 3)
	assert.True(t, userIDs["user1"])
	assert.True(t, userIDs["user2"])
	assert.True(t, userIDs["user3"])
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
		QueueName:         "test:distributor:queue",
		MaxRetries:        3,
		DefaultVisibility: 30 * time.Second,
	})
	require.NoError(t, err)

	return queueClient
}