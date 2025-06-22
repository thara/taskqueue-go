package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewUserPreference(t *testing.T) {
	userID := "user123"
	pref := NewUserPreference(userID)

	assert.Equal(t, userID, pref.UserID)
	assert.NotNil(t, pref.EnabledTasks)
	assert.NotNil(t, pref.TaskSettings)
	assert.NotZero(t, pref.LastModified)
}

func TestUserPreference_TaskOperations(t *testing.T) {
	pref := NewUserPreference("user123")
	taskID := "daily_report"

	// Initially disabled
	assert.False(t, pref.IsTaskEnabled(taskID))

	// Enable task
	oldModified := pref.LastModified
	time.Sleep(1 * time.Millisecond) // Ensure time difference
	pref.EnableTask(taskID)
	assert.True(t, pref.IsTaskEnabled(taskID))
	assert.True(t, pref.LastModified.After(oldModified))

	// Disable task
	oldModified = pref.LastModified
	time.Sleep(1 * time.Millisecond)
	pref.DisableTask(taskID)
	assert.False(t, pref.IsTaskEnabled(taskID))
	assert.True(t, pref.LastModified.After(oldModified))
}

func TestUserPreference_CustomSettings(t *testing.T) {
	pref := NewUserPreference("user123")
	taskID := "daily_report"

	// Default interval
	defaultInterval := TaskInterval{
		Type:     IntervalTypeDaily,
		Value:    1,
		Timezone: "UTC",
	}

	// No custom settings initially
	interval := pref.GetTaskInterval(taskID, defaultInterval)
	assert.Equal(t, defaultInterval, interval)

	// Set custom settings
	customInterval := TaskInterval{
		Type:     IntervalTypeDaily,
		Value:    2,
		Timezone: "America/New_York",
		AtTime:   &TimeOfDay{Hour: 10, Minute: 0},
	}
	
	settings := &TaskSettings{
		CustomInterval: &customInterval,
		Parameters: map[string]string{
			"format": "detailed",
			"lang":   "en",
		},
	}

	oldModified := pref.LastModified
	time.Sleep(1 * time.Millisecond)
	pref.SetTaskSettings(taskID, settings)

	// Check custom interval is used
	interval = pref.GetTaskInterval(taskID, defaultInterval)
	assert.Equal(t, customInterval, interval)
	assert.True(t, pref.LastModified.After(oldModified))

	// Check settings are stored
	storedSettings, exists := pref.TaskSettings[taskID]
	assert.True(t, exists)
	assert.Equal(t, "detailed", storedSettings.Parameters["format"])
	assert.Equal(t, "en", storedSettings.Parameters["lang"])
}