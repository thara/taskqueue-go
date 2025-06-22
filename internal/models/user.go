package models

import (
	"time"
)

// UserPreference represents a user's task preferences
type UserPreference struct {
	UserID       string                    `json:"user_id"`
	EnabledTasks map[string]bool           `json:"enabled_tasks"`    // task_id -> enabled
	TaskSettings map[string]*TaskSettings  `json:"task_settings"`    // task_id -> custom settings
	LastModified time.Time                 `json:"last_modified"`
}

// TaskSettings contains user-specific task settings
type TaskSettings struct {
	CustomInterval *TaskInterval     `json:"custom_interval,omitempty"` // Override default interval
	Parameters     map[string]string `json:"parameters,omitempty"`      // Task-specific parameters
}

// NewUserPreference creates a new user preference instance
func NewUserPreference(userID string) *UserPreference {
	return &UserPreference{
		UserID:       userID,
		EnabledTasks: make(map[string]bool),
		TaskSettings: make(map[string]*TaskSettings),
		LastModified: time.Now(),
	}
}

// IsTaskEnabled checks if a specific task is enabled for the user
func (up *UserPreference) IsTaskEnabled(taskID string) bool {
	enabled, exists := up.EnabledTasks[taskID]
	return exists && enabled
}

// EnableTask enables a task for the user
func (up *UserPreference) EnableTask(taskID string) {
	up.EnabledTasks[taskID] = true
	up.LastModified = time.Now()
}

// DisableTask disables a task for the user
func (up *UserPreference) DisableTask(taskID string) {
	up.EnabledTasks[taskID] = false
	up.LastModified = time.Now()
}

// SetTaskSettings sets custom settings for a task
func (up *UserPreference) SetTaskSettings(taskID string, settings *TaskSettings) {
	up.TaskSettings[taskID] = settings
	up.LastModified = time.Now()
}

// GetTaskInterval returns the task interval (custom or default)
func (up *UserPreference) GetTaskInterval(taskID string, defaultInterval TaskInterval) TaskInterval {
	if settings, exists := up.TaskSettings[taskID]; exists && settings.CustomInterval != nil {
		return *settings.CustomInterval
	}
	return defaultInterval
}