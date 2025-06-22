package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/thara/taskqueue-go/internal/distributor"
	"github.com/thara/taskqueue-go/internal/models"
	"github.com/thara/taskqueue-go/internal/queue"
	"github.com/thara/taskqueue-go/internal/storage"
)

type Config struct {
	CheckInterval        time.Duration
	BatchSize            int
	DistributionWindow   time.Duration
	LeaderElectionTTL    time.Duration
	EnableLeaderElection bool
}

type Scheduler struct {
	config       Config
	redisClient  *redis.Client
	queueClient  *queue.Client
	registry     *storage.TaskRegistry
	userStore    *storage.UserStore
	distributor  *distributor.Distributor
	leaderKey    string
}

func New(
	config Config,
	redisClient *redis.Client,
	queueClient *queue.Client,
	registry *storage.TaskRegistry,
	userStore *storage.UserStore,
) *Scheduler {
	dist := distributor.New(config.DistributionWindow, queueClient)

	return &Scheduler{
		config:      config,
		redisClient: redisClient,
		queueClient: queueClient,
		registry:    registry,
		userStore:   userStore,
		distributor: dist,
		leaderKey:   "taskqueue:scheduler:leader",
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	slog.Info("Starting scheduler", "check_interval", s.config.CheckInterval)

	ticker := time.NewTicker(s.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Scheduler context cancelled, stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := s.checkAndScheduleTasks(ctx); err != nil {
				slog.Error("Error checking and scheduling tasks", "error", err)
			}
		}
	}
}

func (s *Scheduler) checkAndScheduleTasks(ctx context.Context) error {
	// Check if we should run (leader election)
	if s.config.EnableLeaderElection {
		isLeader, err := s.acquireLeadership(ctx)
		if err != nil {
			return fmt.Errorf("leader election failed: %w", err)
		}
		if !isLeader {
			slog.Debug("Not the leader, skipping this cycle")
			return nil
		}
	}

	slog.Debug("Checking for tasks to schedule")

	// Get all task types from registry
	taskTypes := s.registry.GetAll()
	if len(taskTypes) == 0 {
		slog.Debug("No task types registered")
		return nil
	}

	now := time.Now()
	tasksToSchedule := make([]taskToSchedule, 0)

	// Check each task type
	for _, taskType := range taskTypes {
		dueTasks, err := s.findDueTasks(ctx, taskType, now)
		if err != nil {
			slog.Error("Error finding due tasks", "task_type", taskType.ID, "error", err)
			continue
		}
		tasksToSchedule = append(tasksToSchedule, dueTasks...)
	}

	if len(tasksToSchedule) == 0 {
		slog.Debug("No tasks due for scheduling")
		return nil
	}

	slog.Info("Found tasks to schedule", "count", len(tasksToSchedule))

	// Convert to interface slice for distributor
	taskInterfaces := make([]distributor.TaskToSchedule, len(tasksToSchedule))
	for i, task := range tasksToSchedule {
		taskInterfaces[i] = task
	}

	// Distribute tasks across time windows to prevent bursts
	if err := s.distributor.DistributeTasks(ctx, taskInterfaces, now); err != nil {
		return fmt.Errorf("failed to distribute tasks: %w", err)
	}

	// Update last execution timestamps
	if err := s.updateLastExecutions(ctx, tasksToSchedule, now); err != nil {
		slog.Error("Failed to update last execution timestamps", "error", err)
	}

	return nil
}

type taskToSchedule struct {
	TaskType *models.TaskType
	UserID   string
}

func (t taskToSchedule) GetTaskType() *models.TaskType {
	return t.TaskType
}

func (t taskToSchedule) GetUserID() string {
	return t.UserID
}

func (s *Scheduler) findDueTasks(ctx context.Context, taskType *models.TaskType, now time.Time) ([]taskToSchedule, error) {
	// Get last execution time for this task type
	lastExecKey := fmt.Sprintf("taskqueue:schedule:%s", taskType.ID)
	lastExecStr, err := s.redisClient.Get(ctx, lastExecKey).Result()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("failed to get last execution time: %w", err)
	}

	var lastExecution time.Time
	if lastExecStr != "" {
		if lastExecution, err = time.Parse(time.RFC3339, lastExecStr); err != nil {
			slog.Warn("Failed to parse last execution time, using zero time", "task_type", taskType.ID, "value", lastExecStr)
			lastExecution = time.Time{}
		}
	}

	// Check if task is due
	if !taskType.Interval.IsDue(lastExecution, now) {
		return nil, nil
	}

	slog.Debug("Task type is due", "task_type", taskType.ID, "last_execution", lastExecution)

	// Get all users who have this task enabled
	users, err := s.userStore.GetUsersWithEnabledTask(ctx, taskType.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get users with task: %w", err)
	}

	// Create tasks to schedule
	tasks := make([]taskToSchedule, len(users))
	for i, userID := range users {
		tasks[i] = taskToSchedule{
			TaskType: taskType,
			UserID:   userID,
		}
	}

	slog.Debug("Found users for task", "task_type", taskType.ID, "user_count", len(users))

	return tasks, nil
}

func (s *Scheduler) updateLastExecutions(ctx context.Context, tasks []taskToSchedule, scheduledAt time.Time) error {
	// Group tasks by task type to minimize Redis operations
	taskTypesSeen := make(map[string]bool)

	for _, task := range tasks {
		if taskTypesSeen[task.TaskType.ID] {
			continue
		}
		taskTypesSeen[task.TaskType.ID] = true

		lastExecKey := fmt.Sprintf("taskqueue:schedule:%s", task.TaskType.ID)
		if err := s.redisClient.Set(ctx, lastExecKey, scheduledAt.Format(time.RFC3339), 0).Err(); err != nil {
			slog.Error("Failed to update last execution time", "task_type", task.TaskType.ID, "error", err)
		}
	}

	return nil
}

func (s *Scheduler) acquireLeadership(ctx context.Context) (bool, error) {
	// Try to acquire leadership lock
	result, err := s.redisClient.SetNX(ctx, s.leaderKey, "leader", s.config.LeaderElectionTTL).Result()
	if err != nil {
		return false, err
	}

	if result {
		// We got the lock, we are the leader
		slog.Debug("Acquired leadership")
		return true, nil
	}

	// Check if we already hold the lock
	val, err := s.redisClient.Get(ctx, s.leaderKey).Result()
	if err != nil && err != redis.Nil {
		return false, err
	}

	// Extend our lease if we already hold it
	if val == "leader" {
		if err := s.redisClient.Expire(ctx, s.leaderKey, s.config.LeaderElectionTTL).Err(); err != nil {
			slog.Warn("Failed to extend leadership lease", "error", err)
			return false, nil
		}
		return true, nil
	}

	return false, nil
}