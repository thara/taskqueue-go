package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/thara/taskqueue-go/internal/models"
)

const (
	// Redis key patterns
	userTasksKey     = "taskqueue:users:%s:tasks"     // Hash: task_id -> enabled (0/1)
	userSettingsKey  = "taskqueue:users:%s:settings"  // Hash: task_id -> settings JSON
	userMetaKey      = "taskqueue:users:%s:meta"      // Hash: metadata like last_modified
	usersSetKey      = "taskqueue:users:all"          // Set: all user IDs
	taskUsersKey     = "taskqueue:tasks:%s:users"     // Set: user IDs who have this task enabled
)

// UserStore manages user preferences in Redis
type UserStore struct {
	client *redis.Client
	logger *slog.Logger
}

// NewUserStore creates a new user preference store
func NewUserStore(client *redis.Client, logger *slog.Logger) *UserStore {
	return &UserStore{
		client: client,
		logger: logger,
	}
}

// GetUserPreference retrieves user preferences for a specific user
func (s *UserStore) GetUserPreference(ctx context.Context, userID string) (*models.UserPreference, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID cannot be empty")
	}

	// Use pipeline for efficiency
	pipe := s.client.Pipeline()
	tasksCmd := pipe.HGetAll(ctx, fmt.Sprintf(userTasksKey, userID))
	settingsCmd := pipe.HGetAll(ctx, fmt.Sprintf(userSettingsKey, userID))
	metaCmd := pipe.HGetAll(ctx, fmt.Sprintf(userMetaKey, userID))
	
	_, err := pipe.Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get user preference: %w", err)
	}

	// Parse tasks (enabled/disabled)
	enabledTasks := make(map[string]bool)
	tasksData := tasksCmd.Val()
	for taskID, enabled := range tasksData {
		enabledTasks[taskID] = enabled == "1"
	}

	// Parse task settings
	taskSettings := make(map[string]*models.TaskSettings)
	settingsData := settingsCmd.Val()
	for taskID, settingsJSON := range settingsData {
		var settings models.TaskSettings
		if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
			s.logger.Warn("Failed to parse task settings",
				"user_id", userID,
				"task_id", taskID,
				"error", err,
			)
			continue
		}
		taskSettings[taskID] = &settings
	}

	// Parse metadata
	metaData := metaCmd.Val()
	lastModified := time.Now()
	if lastModifiedStr, exists := metaData["last_modified"]; exists {
		if parsed, err := time.Parse(time.RFC3339, lastModifiedStr); err == nil {
			lastModified = parsed
		}
	}

	return &models.UserPreference{
		UserID:       userID,
		EnabledTasks: enabledTasks,
		TaskSettings: taskSettings,
		LastModified: lastModified,
	}, nil
}

// SaveUserPreference saves user preferences to Redis
func (s *UserStore) SaveUserPreference(ctx context.Context, pref *models.UserPreference) error {
	if pref.UserID == "" {
		return fmt.Errorf("user ID cannot be empty")
	}

	// Update last modified
	pref.LastModified = time.Now()

	// Use transaction for consistency
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		pipe := tx.Pipeline()

		// Clear existing data
		tasksKey := fmt.Sprintf(userTasksKey, pref.UserID)
		settingsKey := fmt.Sprintf(userSettingsKey, pref.UserID)
		metaKey := fmt.Sprintf(userMetaKey, pref.UserID)
		
		pipe.Del(ctx, tasksKey, settingsKey)

		// Save enabled tasks
		if len(pref.EnabledTasks) > 0 {
			tasksData := make(map[string]interface{})
			for taskID, enabled := range pref.EnabledTasks {
				if enabled {
					tasksData[taskID] = "1"
				} else {
					tasksData[taskID] = "0"
				}
			}
			pipe.HMSet(ctx, tasksKey, tasksData)
		}

		// Save task settings
		if len(pref.TaskSettings) > 0 {
			settingsData := make(map[string]interface{})
			for taskID, settings := range pref.TaskSettings {
				if settings != nil {
					settingsJSON, err := json.Marshal(settings)
					if err != nil {
						return fmt.Errorf("failed to marshal settings for task %s: %w", taskID, err)
					}
					settingsData[taskID] = string(settingsJSON)
				}
			}
			if len(settingsData) > 0 {
				pipe.HMSet(ctx, settingsKey, settingsData)
			}
		}

		// Save metadata
		pipe.HMSet(ctx, metaKey, map[string]interface{}{
			"last_modified": pref.LastModified.Format(time.RFC3339),
		})

		// Add user to users set
		pipe.SAdd(ctx, usersSetKey, pref.UserID)

		// Update task-to-users index
		for taskID, enabled := range pref.EnabledTasks {
			taskUsersSetKey := fmt.Sprintf(taskUsersKey, taskID)
			if enabled {
				pipe.SAdd(ctx, taskUsersSetKey, pref.UserID)
			} else {
				pipe.SRem(ctx, taskUsersSetKey, pref.UserID)
			}
		}

		_, err := pipe.Exec(ctx)
		return err
	})

	if err != nil {
		return fmt.Errorf("failed to save user preference: %w", err)
	}

	s.logger.Debug("Saved user preference",
		"user_id", pref.UserID,
		"enabled_tasks", len(pref.EnabledTasks),
		"task_settings", len(pref.TaskSettings),
	)

	return nil
}

// EnableTaskForUser enables a specific task for a user
func (s *UserStore) EnableTaskForUser(ctx context.Context, userID, taskID string) error {
	if userID == "" || taskID == "" {
		return fmt.Errorf("user ID and task ID cannot be empty")
	}

	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		pipe := tx.Pipeline()

		// Enable task
		tasksKey := fmt.Sprintf(userTasksKey, userID)
		pipe.HSet(ctx, tasksKey, taskID, "1")

		// Update metadata
		metaKey := fmt.Sprintf(userMetaKey, userID)
		pipe.HSet(ctx, metaKey, "last_modified", time.Now().Format(time.RFC3339))

		// Add user to users set
		pipe.SAdd(ctx, usersSetKey, userID)

		// Add user to task-users index
		taskUsersSetKey := fmt.Sprintf(taskUsersKey, taskID)
		pipe.SAdd(ctx, taskUsersSetKey, userID)

		_, err := pipe.Exec(ctx)
		return err
	})

	if err != nil {
		return fmt.Errorf("failed to enable task for user: %w", err)
	}

	s.logger.Debug("Enabled task for user",
		"user_id", userID,
		"task_id", taskID,
	)

	return nil
}

// DisableTaskForUser disables a specific task for a user
func (s *UserStore) DisableTaskForUser(ctx context.Context, userID, taskID string) error {
	if userID == "" || taskID == "" {
		return fmt.Errorf("user ID and task ID cannot be empty")
	}

	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		pipe := tx.Pipeline()

		// Disable task
		tasksKey := fmt.Sprintf(userTasksKey, userID)
		pipe.HSet(ctx, tasksKey, taskID, "0")

		// Update metadata
		metaKey := fmt.Sprintf(userMetaKey, userID)
		pipe.HSet(ctx, metaKey, "last_modified", time.Now().Format(time.RFC3339))

		// Remove user from task-users index
		taskUsersSetKey := fmt.Sprintf(taskUsersKey, taskID)
		pipe.SRem(ctx, taskUsersSetKey, userID)

		_, err := pipe.Exec(ctx)
		return err
	})

	if err != nil {
		return fmt.Errorf("failed to disable task for user: %w", err)
	}

	s.logger.Debug("Disabled task for user",
		"user_id", userID,
		"task_id", taskID,
	)

	return nil
}

// GetUsersWithEnabledTask returns all user IDs who have a specific task enabled
func (s *UserStore) GetUsersWithEnabledTask(ctx context.Context, taskID string) ([]string, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task ID cannot be empty")
	}

	taskUsersSetKey := fmt.Sprintf(taskUsersKey, taskID)
	userIDs, err := s.client.SMembers(ctx, taskUsersSetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get users with enabled task: %w", err)
	}

	return userIDs, nil
}

// GetAllUsers returns all user IDs in the system
func (s *UserStore) GetAllUsers(ctx context.Context) ([]string, error) {
	userIDs, err := s.client.SMembers(ctx, usersSetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get all users: %w", err)
	}

	return userIDs, nil
}

// IsTaskEnabledForUser checks if a specific task is enabled for a user
func (s *UserStore) IsTaskEnabledForUser(ctx context.Context, userID, taskID string) (bool, error) {
	if userID == "" || taskID == "" {
		return false, fmt.Errorf("user ID and task ID cannot be empty")
	}

	tasksKey := fmt.Sprintf(userTasksKey, userID)
	enabled, err := s.client.HGet(ctx, tasksKey, taskID).Result()
	if err != nil {
		if err == redis.Nil {
			return false, nil // Task not set means disabled
		}
		return false, fmt.Errorf("failed to check if task is enabled: %w", err)
	}

	return enabled == "1", nil
}

// GetUserCount returns the total number of users
func (s *UserStore) GetUserCount(ctx context.Context) (int64, error) {
	count, err := s.client.SCard(ctx, usersSetKey).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get user count: %w", err)
	}

	return count, nil
}

// GetTaskEnabledCount returns the number of users who have a specific task enabled
func (s *UserStore) GetTaskEnabledCount(ctx context.Context, taskID string) (int64, error) {
	if taskID == "" {
		return 0, fmt.Errorf("task ID cannot be empty")
	}

	taskUsersSetKey := fmt.Sprintf(taskUsersKey, taskID)
	count, err := s.client.SCard(ctx, taskUsersSetKey).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get task enabled count: %w", err)
	}

	return count, nil
}

// DeleteUser removes all data for a user
func (s *UserStore) DeleteUser(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("user ID cannot be empty")
	}

	// Get user's enabled tasks first to clean up indexes
	pref, err := s.GetUserPreference(ctx, userID)
	if err != nil {
		// If user doesn't exist, that's fine
		return nil
	}

	err = s.client.Watch(ctx, func(tx *redis.Tx) error {
		pipe := tx.Pipeline()

		// Remove user data
		tasksKey := fmt.Sprintf(userTasksKey, userID)
		settingsKey := fmt.Sprintf(userSettingsKey, userID)
		metaKey := fmt.Sprintf(userMetaKey, userID)
		
		pipe.Del(ctx, tasksKey, settingsKey, metaKey)

		// Remove from users set
		pipe.SRem(ctx, usersSetKey, userID)

		// Remove from task-users indexes
		for taskID := range pref.EnabledTasks {
			taskUsersSetKey := fmt.Sprintf(taskUsersKey, taskID)
			pipe.SRem(ctx, taskUsersSetKey, userID)
		}

		_, err := pipe.Exec(ctx)
		return err
	})

	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	s.logger.Debug("Deleted user", "user_id", userID)
	return nil
}

// GetUsersByTaskPattern returns users who have tasks matching a pattern enabled
func (s *UserStore) GetUsersByTaskPattern(ctx context.Context, pattern string) ([]string, error) {
	// Get all users first
	allUsers, err := s.GetAllUsers(ctx)
	if err != nil {
		return nil, err
	}

	var matchingUsers []string
	for _, userID := range allUsers {
		pref, err := s.GetUserPreference(ctx, userID)
		if err != nil {
			continue
		}

		// Check if any enabled task matches the pattern
		for taskID, enabled := range pref.EnabledTasks {
			if enabled && matchesPattern(taskID, pattern) {
				matchingUsers = append(matchingUsers, userID)
				break
			}
		}
	}

	return matchingUsers, nil
}

// matchesPattern checks if a string matches a simple pattern (supports * wildcard)
func matchesPattern(str, pattern string) bool {
	if pattern == "*" {
		return true
	}
	
	if !strings.Contains(pattern, "*") {
		return str == pattern
	}

	// Simple wildcard matching
	parts := strings.Split(pattern, "*")
	if len(parts) == 2 {
		prefix, suffix := parts[0], parts[1]
		return strings.HasPrefix(str, prefix) && strings.HasSuffix(str, suffix)
	}

	return false
}