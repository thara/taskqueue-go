package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewJob(t *testing.T) {
	taskTypeID := "test_task"
	userID := "user123"
	executeAfter := time.Now().Add(5 * time.Minute)

	job := NewJob(taskTypeID, userID, executeAfter)

	assert.NotEmpty(t, job.ID)
	assert.Equal(t, taskTypeID, job.TaskTypeID)
	assert.Equal(t, userID, job.UserID)
	assert.Equal(t, executeAfter, job.ExecuteAfter)
	assert.Equal(t, 1, job.Attempt)
	assert.Equal(t, PriorityNormal, job.Priority)
	assert.NotZero(t, job.CreatedAt)
	assert.NotZero(t, job.ScheduledAt)
}

func TestExecutionResult_ShouldRetry(t *testing.T) {
	tests := []struct {
		name        string
		result      ExecutionResult
		maxAttempts int
		want        bool
	}{
		{
			name: "success - no retry",
			result: ExecutionResult{
				Status: StatusSuccess,
			},
			maxAttempts: 3,
			want:        false,
		},
		{
			name: "failed with retry true",
			result: ExecutionResult{
				Status: StatusFailed,
				Error: &ErrorDetail{
					Code:    "TIMEOUT",
					Message: "Request timeout",
					Retry:   true,
				},
			},
			maxAttempts: 3,
			want:        true,
		},
		{
			name: "failed with retry false",
			result: ExecutionResult{
				Status: StatusFailed,
				Error: &ErrorDetail{
					Code:    "INVALID_PAYLOAD",
					Message: "Invalid request payload",
					Retry:   false,
				},
			},
			maxAttempts: 3,
			want:        false,
		},
		{
			name: "failed with no error detail",
			result: ExecutionResult{
				Status: StatusFailed,
			},
			maxAttempts: 3,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.ShouldRetry(tt.maxAttempts)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewQueueMessage(t *testing.T) {
	job := NewJob("test_task", "user123", time.Now())
	source := "scheduler"
	slot := 42

	msg := NewQueueMessage(job, source, slot)

	assert.Equal(t, "1.0", msg.Version)
	assert.Equal(t, job, msg.Job)
	assert.NotEmpty(t, msg.Metadata.CorrelationID)
	assert.Equal(t, source, msg.Metadata.Source)
	assert.Equal(t, slot, msg.Metadata.DistributionSlot)
}