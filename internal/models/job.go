package models

import (
	"time"

	"github.com/google/uuid"
)

// Priority represents job execution priority
type Priority int

const (
	PriorityLow    Priority = 0
	PriorityNormal Priority = 1
	PriorityHigh   Priority = 2
)

// Job represents a scheduled task instance for a specific user
type Job struct {
	ID           string    `json:"id"`
	TaskTypeID   string    `json:"task_type_id"`
	UserID       string    `json:"user_id"`
	ScheduledAt  time.Time `json:"scheduled_at"`   // When it was scheduled
	ExecuteAfter time.Time `json:"execute_after"`  // When it should be executed (after distribution)
	Attempt      int       `json:"attempt"`        // Current attempt number
	Priority     Priority  `json:"priority"`       // Execution priority
	Payload      []byte    `json:"payload"`        // Task-specific data
	CreatedAt    time.Time `json:"created_at"`     // When the job was created
}

// NewJob creates a new job instance
func NewJob(taskTypeID, userID string, executeAfter time.Time) *Job {
	now := time.Now()
	return &Job{
		ID:           uuid.New().String(),
		TaskTypeID:   taskTypeID,
		UserID:       userID,
		ScheduledAt:  now,
		ExecuteAfter: executeAfter,
		Attempt:      1,
		Priority:     PriorityNormal,
		CreatedAt:    now,
	}
}

// Status represents the execution status of a job
type Status string

const (
	StatusPending  Status = "pending"
	StatusRunning  Status = "running"
	StatusSuccess  Status = "success"
	StatusFailed   Status = "failed"
	StatusRetrying Status = "retrying"
)

// ExecutionResult represents the outcome of a job execution
type ExecutionResult struct {
	JobID       string        `json:"job_id"`
	Status      Status        `json:"status"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at"`
	Duration    time.Duration `json:"duration"`
	Error       *ErrorDetail  `json:"error,omitempty"`
	Response    []byte        `json:"response,omitempty"`
	NextAttempt *time.Time    `json:"next_attempt,omitempty"`
}

// ErrorDetail contains error information for failed executions
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Retry   bool   `json:"retry"` // Whether the job should be retried
}

// ShouldRetry determines if the job should be retried based on the error
func (er *ExecutionResult) ShouldRetry(maxAttempts int) bool {
	if er.Status != StatusFailed || er.Error == nil {
		return false
	}
	return er.Error.Retry
}

// QueueMessage represents the message format for the message queue
type QueueMessage struct {
	Version  string            `json:"version"`
	Job      *Job              `json:"job"`
	Metadata QueueMessageMeta  `json:"metadata"`
}

// QueueMessageMeta contains metadata for queue messages
type QueueMessageMeta struct {
	CorrelationID    string `json:"correlation_id"`
	Source           string `json:"source"`
	DistributionSlot int    `json:"distribution_slot"`
}

// NewQueueMessage creates a new queue message
func NewQueueMessage(job *Job, source string, slot int) *QueueMessage {
	return &QueueMessage{
		Version: "1.0",
		Job:     job,
		Metadata: QueueMessageMeta{
			CorrelationID:    uuid.New().String(),
			Source:           source,
			DistributionSlot: slot,
		},
	}
}