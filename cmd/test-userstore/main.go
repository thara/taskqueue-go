package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/thara/taskqueue-go/internal/models"
	"github.com/thara/taskqueue-go/internal/storage"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Connect to Redis (use Docker or local Redis)
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	
	ctx := context.Background()
	
	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		logger.Error("Failed to connect to Redis", "error", err)
		logger.Info("Make sure Redis is running: docker run -p 6379:6379 redis:7-alpine")
		os.Exit(1)
	}

	// Create user store
	store := storage.NewUserStore(client, logger)

	// Demo user preferences
	logger.Info("=== User Store Demo ===")

	// Create some users with different preferences
	users := []struct {
		id    string
		tasks []string
	}{
		{"user1", []string{"daily_user_report", "hourly_data_sync"}},
		{"user2", []string{"daily_user_report", "weekly_backup"}},
		{"user3", []string{"hourly_data_sync", "monthly_billing"}},
		{"user4", []string{"daily_system_metrics", "bi_hourly_health_check"}},
	}

	// Enable tasks for users
	logger.Info("Setting up user preferences...")
	for _, user := range users {
		for _, taskID := range user.tasks {
			if err := store.EnableTaskForUser(ctx, user.id, taskID); err != nil {
				logger.Error("Failed to enable task", "user", user.id, "task", taskID, "error", err)
				continue
			}
		}
		logger.Info("Enabled tasks for user", "user_id", user.id, "tasks", user.tasks)
	}

	// Create a user with custom settings
	logger.Info("Creating user with custom settings...")
	customUser := &models.UserPreference{
		UserID: "premium_user",
		EnabledTasks: map[string]bool{
			"daily_user_report":   true,
			"weekly_analytics":    true,
			"monthly_billing":     false,
		},
		TaskSettings: map[string]*models.TaskSettings{
			"daily_user_report": {
				CustomInterval: &models.TaskInterval{
					Type:     models.IntervalTypeDaily,
					Value:    1,
					Timezone: "America/New_York",
					AtTime:   &models.TimeOfDay{Hour: 8, Minute: 30},
				},
				Parameters: map[string]string{
					"format":   "premium",
					"language": "en",
					"timezone": "America/New_York",
				},
			},
		},
	}

	if err := store.SaveUserPreference(ctx, customUser); err != nil {
		logger.Error("Failed to save custom user preference", "error", err)
	} else {
		logger.Info("Saved custom user preference", "user_id", customUser.UserID)
	}

	// Query examples
	logger.Info("=== Query Examples ===")

	// Get total user count
	totalUsers, err := store.GetUserCount(ctx)
	if err != nil {
		logger.Error("Failed to get user count", "error", err)
	} else {
		logger.Info("Total users in system", "count", totalUsers)
	}

	// Get users with specific task enabled
	taskToCheck := "daily_user_report"
	usersWithTask, err := store.GetUsersWithEnabledTask(ctx, taskToCheck)
	if err != nil {
		logger.Error("Failed to get users with task", "task", taskToCheck, "error", err)
	} else {
		logger.Info("Users with task enabled", "task_id", taskToCheck, "users", usersWithTask)
	}

	// Get count for specific task
	taskCount, err := store.GetTaskEnabledCount(ctx, taskToCheck)
	if err != nil {
		logger.Error("Failed to get task count", "task", taskToCheck, "error", err)
	} else {
		logger.Info("Task enabled count", "task_id", taskToCheck, "count", taskCount)
	}

	// Pattern search examples
	patterns := []string{"daily_*", "hourly_*", "*billing", "*"}
	for _, pattern := range patterns {
		users, err := store.GetUsersByTaskPattern(ctx, pattern)
		if err != nil {
			logger.Error("Failed to search by pattern", "pattern", pattern, "error", err)
			continue
		}
		logger.Info("Users with task pattern", "pattern", pattern, "users", users)
	}

	// Get detailed user preference
	logger.Info("=== User Details ===")
	userToInspect := "premium_user"
	pref, err := store.GetUserPreference(ctx, userToInspect)
	if err != nil {
		logger.Error("Failed to get user preference", "user", userToInspect, "error", err)
	} else {
		logger.Info("User preference details",
			"user_id", pref.UserID,
			"enabled_tasks", len(pref.EnabledTasks),
			"custom_settings", len(pref.TaskSettings),
			"last_modified", pref.LastModified.Format(time.RFC3339),
		)

		for taskID, enabled := range pref.EnabledTasks {
			logger.Info("Task status", "user_id", userToInspect, "task_id", taskID, "enabled", enabled)
		}

		if settings, exists := pref.TaskSettings["daily_user_report"]; exists {
			if settings.CustomInterval != nil {
				logger.Info("Custom interval",
					"user_id", userToInspect,
					"task_id", "daily_user_report",
					"type", settings.CustomInterval.Type,
					"value", settings.CustomInterval.Value,
					"timezone", settings.CustomInterval.Timezone,
					"at_time", settings.CustomInterval.AtTime,
				)
			}
			logger.Info("Custom parameters",
				"user_id", userToInspect,
				"task_id", "daily_user_report",
				"parameters", settings.Parameters,
			)
		}
	}

	logger.Info("Demo completed successfully!")
}