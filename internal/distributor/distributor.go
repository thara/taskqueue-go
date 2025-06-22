package distributor

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/thara/taskqueue-go/internal/models"
	"github.com/thara/taskqueue-go/internal/queue"
)

// TaskToSchedule represents a task that needs to be scheduled
type TaskToSchedule interface {
	GetTaskType() *models.TaskType
	GetUserID() string
}

// Distributor handles distributing tasks across time windows to prevent API overload
type Distributor struct {
	distributionWindow time.Duration
	queueClient        *queue.Client
}

// New creates a new task distributor
func New(distributionWindow time.Duration, queueClient *queue.Client) *Distributor {
	return &Distributor{
		distributionWindow: distributionWindow,
		queueClient:        queueClient,
	}
}

// DistributeTasks distributes tasks across the distribution window to prevent bursts
func (d *Distributor) DistributeTasks(ctx context.Context, tasks []TaskToSchedule, baseTime time.Time) error {
	if len(tasks) == 0 {
		return nil
	}

	slog.Debug("Distributing tasks", "count", len(tasks), "window", d.distributionWindow)

	// Calculate time slots for distribution
	timeSlots := d.calculateTimeSlots(len(tasks))
	
	// Shuffle tasks to avoid bias
	shuffledTasks := make([]TaskToSchedule, len(tasks))
	copy(shuffledTasks, tasks)
	rand.Shuffle(len(shuffledTasks), func(i, j int) {
		shuffledTasks[i], shuffledTasks[j] = shuffledTasks[j], shuffledTasks[i]
	})

	// Create jobs for each task
	jobs := make([]*models.Job, len(shuffledTasks))
	for i, task := range shuffledTasks {
		executeAfter := baseTime.Add(timeSlots[i])
		job := models.NewJob(task.GetTaskType().ID, task.GetUserID(), executeAfter)
		jobs[i] = job
	}

	// Push jobs to queue
	for _, job := range jobs {
		if err := d.queueClient.Push(ctx, job); err != nil {
			slog.Error("Failed to push job to queue", 
				"job_id", job.ID,
				"task_type", job.TaskTypeID,
				"user_id", job.UserID,
				"execute_after", job.ExecuteAfter,
				"error", err,
			)
			// Continue with other jobs even if one fails
			continue
		}

		slog.Debug("Job queued successfully",
			"job_id", job.ID,
			"task_type", job.TaskTypeID,
			"user_id", job.UserID,
			"scheduled_at", job.ScheduledAt,
			"execute_after", job.ExecuteAfter,
		)
	}

	slog.Info("Tasks distributed successfully", 
		"total_tasks", len(tasks),
		"window", d.distributionWindow,
	)

	return nil
}

// calculateTimeSlots calculates evenly distributed time slots within the distribution window
func (d *Distributor) calculateTimeSlots(taskCount int) []time.Duration {
	if taskCount == 0 {
		return []time.Duration{}
	}

	if taskCount == 1 {
		// Single task, schedule immediately
		return []time.Duration{0}
	}

	// Calculate interval between tasks
	interval := d.distributionWindow / time.Duration(taskCount)
	
	// Ensure minimum interval of 1 second to avoid overwhelming the system
	minInterval := time.Second
	if interval < minInterval {
		interval = minInterval
	}

	// Generate time slots
	slots := make([]time.Duration, taskCount)
	for i := 0; i < taskCount; i++ {
		slots[i] = time.Duration(i) * interval
	}

	// Add some jitter to prevent exact synchronization
	jitterRange := interval / 4 // Up to 25% jitter
	if jitterRange > 0 {
		for i := range slots {
			jitter := time.Duration(rand.Int63n(int64(jitterRange)))
			slots[i] += jitter
		}
	}

	return slots
}

// GetDistributionWindow returns the current distribution window
func (d *Distributor) GetDistributionWindow() time.Duration {
	return d.distributionWindow
}

// SetDistributionWindow updates the distribution window
func (d *Distributor) SetDistributionWindow(window time.Duration) {
	d.distributionWindow = window
}

// EstimateDistributionTime estimates how long it will take to distribute all tasks
func (d *Distributor) EstimateDistributionTime(taskCount int) time.Duration {
	if taskCount <= 1 {
		return 0
	}

	// Use the same logic as calculateTimeSlots
	interval := d.distributionWindow / time.Duration(taskCount)
	minInterval := time.Second
	if interval < minInterval {
		interval = minInterval
		// If we hit the minimum interval, actual distribution time will be longer
		return time.Duration(taskCount) * interval
	}

	return d.distributionWindow
}