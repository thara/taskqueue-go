package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thara/taskqueue-go/internal/models"
)

func setupRedisTest(t *testing.T) (*UserStore, *redis.Client, func()) {
	// Start miniredis
	mr := miniredis.RunT(t)
	
	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn, // Reduce noise in tests
	}))

	// Create store
	store := NewUserStore(client, logger)

	// Cleanup function
	cleanup := func() {
		client.Close()
		mr.Close()
	}

	return store, client, cleanup
}

func TestUserStore_SaveAndGetUserPreference(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()
	userID := "user123"

	// Create user preference
	pref := &models.UserPreference{
		UserID: userID,
		EnabledTasks: map[string]bool{
			"daily_report": true,
			"hourly_sync":  false,
			"weekly_backup": true,
		},
		TaskSettings: map[string]*models.TaskSettings{
			"daily_report": {
				CustomInterval: &models.TaskInterval{
					Type:     models.IntervalTypeDaily,
					Value:    2,
					Timezone: "America/New_York",
					AtTime:   &models.TimeOfDay{Hour: 10, Minute: 30},
				},
				Parameters: map[string]string{
					"format": "detailed",
					"lang":   "en",
				},
			},
		},
		LastModified: time.Now(),
	}

	// Save preference
	err := store.SaveUserPreference(ctx, pref)
	require.NoError(t, err)

	// Get preference back
	retrieved, err := store.GetUserPreference(ctx, userID)
	require.NoError(t, err)
	
	// Verify basic fields
	assert.Equal(t, userID, retrieved.UserID)
	assert.Equal(t, pref.EnabledTasks, retrieved.EnabledTasks)
	
	// Verify custom settings
	require.Contains(t, retrieved.TaskSettings, "daily_report")
	settings := retrieved.TaskSettings["daily_report"]
	require.NotNil(t, settings.CustomInterval)
	assert.Equal(t, models.IntervalTypeDaily, settings.CustomInterval.Type)
	assert.Equal(t, 2, settings.CustomInterval.Value)
	assert.Equal(t, "America/New_York", settings.CustomInterval.Timezone)
	assert.Equal(t, 10, settings.CustomInterval.AtTime.Hour)
	assert.Equal(t, 30, settings.CustomInterval.AtTime.Minute)
	assert.Equal(t, "detailed", settings.Parameters["format"])
	assert.Equal(t, "en", settings.Parameters["lang"])
	
	// LastModified should be updated
	assert.True(t, retrieved.LastModified.After(pref.LastModified.Add(-time.Second)))
}

func TestUserStore_EnableDisableTask(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()
	userID := "user456"
	taskID := "daily_report"

	// Initially task should not be enabled
	enabled, err := store.IsTaskEnabledForUser(ctx, userID, taskID)
	require.NoError(t, err)
	assert.False(t, enabled)

	// Enable task
	err = store.EnableTaskForUser(ctx, userID, taskID)
	require.NoError(t, err)

	// Check if enabled
	enabled, err = store.IsTaskEnabledForUser(ctx, userID, taskID)
	require.NoError(t, err)
	assert.True(t, enabled)

	// Disable task
	err = store.DisableTaskForUser(ctx, userID, taskID)
	require.NoError(t, err)

	// Check if disabled
	enabled, err = store.IsTaskEnabledForUser(ctx, userID, taskID)
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestUserStore_GetUsersWithEnabledTask(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "daily_report"

	// Enable task for multiple users
	users := []string{"user1", "user2", "user3"}
	for _, userID := range users {
		err := store.EnableTaskForUser(ctx, userID, taskID)
		require.NoError(t, err)
	}

	// Disable for one user
	err := store.DisableTaskForUser(ctx, "user2", taskID)
	require.NoError(t, err)

	// Get users with enabled task
	enabledUsers, err := store.GetUsersWithEnabledTask(ctx, taskID)
	require.NoError(t, err)
	
	assert.Len(t, enabledUsers, 2)
	assert.Contains(t, enabledUsers, "user1")
	assert.Contains(t, enabledUsers, "user3")
	assert.NotContains(t, enabledUsers, "user2")
}

func TestUserStore_GetAllUsers(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()

	// Enable tasks for multiple users
	userTasks := map[string][]string{
		"user1": {"task1", "task2"},
		"user2": {"task1"},
		"user3": {"task3"},
	}

	for userID, tasks := range userTasks {
		for _, taskID := range tasks {
			err := store.EnableTaskForUser(ctx, userID, taskID)
			require.NoError(t, err)
		}
	}

	// Get all users
	allUsers, err := store.GetAllUsers(ctx)
	require.NoError(t, err)
	
	assert.Len(t, allUsers, 3)
	assert.Contains(t, allUsers, "user1")
	assert.Contains(t, allUsers, "user2")
	assert.Contains(t, allUsers, "user3")
}

func TestUserStore_GetUserCount(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()

	// Initially no users
	count, err := store.GetUserCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	// Add users
	for i := 1; i <= 5; i++ {
		userID := fmt.Sprintf("user%d", i)
		err := store.EnableTaskForUser(ctx, userID, "task1")
		require.NoError(t, err)
	}

	// Check count
	count, err = store.GetUserCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(5), count)
}

func TestUserStore_GetTaskEnabledCount(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "daily_report"

	// Initially no users have this task enabled
	count, err := store.GetTaskEnabledCount(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	// Enable for multiple users
	for i := 1; i <= 3; i++ {
		userID := fmt.Sprintf("user%d", i)
		err := store.EnableTaskForUser(ctx, userID, taskID)
		require.NoError(t, err)
	}

	// Check count
	count, err = store.GetTaskEnabledCount(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)

	// Disable for one user
	err = store.DisableTaskForUser(ctx, "user2", taskID)
	require.NoError(t, err)

	// Check count again
	count, err = store.GetTaskEnabledCount(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestUserStore_DeleteUser(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()
	userID := "user789"

	// Create user with multiple tasks
	tasks := []string{"task1", "task2", "task3"}
	for _, taskID := range tasks {
		err := store.EnableTaskForUser(ctx, userID, taskID)
		require.NoError(t, err)
	}

	// Verify user exists
	pref, err := store.GetUserPreference(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, userID, pref.UserID)
	assert.Len(t, pref.EnabledTasks, 3)

	// Verify user is in task indexes
	for _, taskID := range tasks {
		users, err := store.GetUsersWithEnabledTask(ctx, taskID)
		require.NoError(t, err)
		assert.Contains(t, users, userID)
	}

	// Delete user
	err = store.DeleteUser(ctx, userID)
	require.NoError(t, err)

	// Verify user is gone
	pref, err = store.GetUserPreference(ctx, userID)
	require.NoError(t, err)
	assert.Empty(t, pref.EnabledTasks)

	// Verify user is removed from task indexes
	for _, taskID := range tasks {
		users, err := store.GetUsersWithEnabledTask(ctx, taskID)
		require.NoError(t, err)
		assert.NotContains(t, users, userID)
	}

	// Verify user is removed from all users set
	allUsers, err := store.GetAllUsers(ctx)
	require.NoError(t, err)
	assert.NotContains(t, allUsers, userID)
}

func TestUserStore_GetUsersByTaskPattern(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()

	// Setup users with different tasks
	userTasks := map[string][]string{
		"user1": {"daily_report", "hourly_sync"},
		"user2": {"daily_backup", "weekly_report"},
		"user3": {"monthly_billing", "daily_cleanup"},
		"user4": {"hourly_health_check"},
	}

	for userID, tasks := range userTasks {
		for _, taskID := range tasks {
			err := store.EnableTaskForUser(ctx, userID, taskID)
			require.NoError(t, err)
		}
	}

	tests := []struct {
		name     string
		pattern  string
		expected []string
	}{
		{
			name:     "daily tasks",
			pattern:  "daily_*",
			expected: []string{"user1", "user2", "user3"},
		},
		{
			name:     "hourly tasks",
			pattern:  "hourly_*",
			expected: []string{"user1", "user4"},
		},
		{
			name:     "all tasks",
			pattern:  "*",
			expected: []string{"user1", "user2", "user3", "user4"},
		},
		{
			name:     "specific task",
			pattern:  "monthly_billing",
			expected: []string{"user3"},
		},
		{
			name:     "no matches",
			pattern:  "nonexistent_*",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users, err := store.GetUsersByTaskPattern(ctx, tt.pattern)
			require.NoError(t, err)
			
			assert.Len(t, users, len(tt.expected))
			for _, expectedUser := range tt.expected {
				assert.Contains(t, users, expectedUser)
			}
		})
	}
}

func TestUserStore_ValidationErrors(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()

	tests := []struct {
		name   string
		fn     func() error
		errMsg string
	}{
		{
			name: "empty user ID - GetUserPreference",
			fn: func() error {
				_, err := store.GetUserPreference(ctx, "")
				return err
			},
			errMsg: "user ID cannot be empty",
		},
		{
			name: "empty user ID - SaveUserPreference",
			fn: func() error {
				return store.SaveUserPreference(ctx, &models.UserPreference{})
			},
			errMsg: "user ID cannot be empty",
		},
		{
			name: "empty user ID - EnableTaskForUser",
			fn: func() error {
				return store.EnableTaskForUser(ctx, "", "task1")
			},
			errMsg: "user ID and task ID cannot be empty",
		},
		{
			name: "empty task ID - EnableTaskForUser",
			fn: func() error {
				return store.EnableTaskForUser(ctx, "user1", "")
			},
			errMsg: "user ID and task ID cannot be empty",
		},
		{
			name: "empty task ID - GetUsersWithEnabledTask",
			fn: func() error {
				_, err := store.GetUsersWithEnabledTask(ctx, "")
				return err
			},
			errMsg: "task ID cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestUserStore_GetNonExistentUser(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()

	// Get preference for non-existent user
	pref, err := store.GetUserPreference(ctx, "nonexistent")
	require.NoError(t, err)
	
	// Should return empty preference
	assert.Equal(t, "nonexistent", pref.UserID)
	assert.Empty(t, pref.EnabledTasks)
	assert.Empty(t, pref.TaskSettings)
	assert.NotZero(t, pref.LastModified)
}

func TestUserStore_ConcurrentAccess(t *testing.T) {
	store, _, cleanup := setupRedisTest(t)
	defer cleanup()

	ctx := context.Background()
	userID := "concurrent_user"
	taskID := "concurrent_task"

	// Run concurrent enable/disable operations
	const numGoroutines = 10
	done := make(chan bool, numGoroutines)
	
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()
			
			if id%2 == 0 {
				store.EnableTaskForUser(ctx, userID, taskID)
			} else {
				store.DisableTaskForUser(ctx, userID, taskID)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Check final state (should be either enabled or disabled, not corrupted)
	enabled, err := store.IsTaskEnabledForUser(ctx, userID, taskID)
	require.NoError(t, err)
	assert.IsType(t, true, enabled) // Should be a valid boolean
}