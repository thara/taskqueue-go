package storage

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thara/taskqueue-go/internal/models"
)

func TestTaskRegistry_LoadFromFile(t *testing.T) {
	// Create a temporary YAML file
	yamlContent := `
tasks:
  - id: daily_report
    name: "Daily Report Generation"
    description: "Generates daily activity reports for users"
    interval:
      type: daily
      value: 1
      timezone: UTC
      at_time:
        hour: 9
        minute: 0
    timeout: 5m
    retries: 3
    config:
      endpoint: "https://api.example.com/reports/daily"
      method: POST
      headers:
        Content-Type: application/json
      max_duration: 4m

  - id: hourly_sync
    name: "Hourly Data Sync"
    description: "Syncs user data every hour"
    interval:
      type: hourly
      value: 2
      timezone: UTC
    timeout: 2m
    retries: 2
    config:
      endpoint: "https://api.example.com/sync"
      method: GET
      max_duration: 90s
`

	tmpFile := createTempFile(t, yamlContent)
	defer os.Remove(tmpFile)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	registry := NewTaskRegistry(tmpFile, logger)

	// Load tasks
	err := registry.LoadFromFile()
	require.NoError(t, err)

	// Verify count
	assert.Equal(t, 2, registry.Count())

	// Verify daily_report task
	dailyTask, err := registry.Get("daily_report")
	require.NoError(t, err)
	assert.Equal(t, "daily_report", dailyTask.ID)
	assert.Equal(t, "Daily Report Generation", dailyTask.Name)
	assert.Equal(t, models.IntervalTypeDaily, dailyTask.Interval.Type)
	assert.Equal(t, 1, dailyTask.Interval.Value)
	assert.Equal(t, "UTC", dailyTask.Interval.Timezone)
	assert.NotNil(t, dailyTask.Interval.AtTime)
	assert.Equal(t, 9, dailyTask.Interval.AtTime.Hour)
	assert.Equal(t, 0, dailyTask.Interval.AtTime.Minute)
	assert.Equal(t, 5*time.Minute, dailyTask.Timeout)
	assert.Equal(t, 3, dailyTask.Retries)
	assert.Equal(t, "https://api.example.com/reports/daily", dailyTask.Config.Endpoint)
	assert.Equal(t, "POST", dailyTask.Config.Method)
	assert.Equal(t, "application/json", dailyTask.Config.Headers["Content-Type"])
	assert.Equal(t, 4*time.Minute, dailyTask.Config.MaxDuration)

	// Verify hourly_sync task
	hourlyTask, err := registry.Get("hourly_sync")
	require.NoError(t, err)
	assert.Equal(t, "hourly_sync", hourlyTask.ID)
	assert.Equal(t, "Hourly Data Sync", hourlyTask.Name)
	assert.Equal(t, models.IntervalTypeHourly, hourlyTask.Interval.Type)
	assert.Equal(t, 2, hourlyTask.Interval.Value)
	assert.Nil(t, hourlyTask.Interval.AtTime)
	assert.Equal(t, 2*time.Minute, hourlyTask.Timeout)
	assert.Equal(t, 2, hourlyTask.Retries)
	assert.Equal(t, "https://api.example.com/sync", hourlyTask.Config.Endpoint)
	assert.Equal(t, "GET", hourlyTask.Config.Method)
	assert.Equal(t, 90*time.Second, hourlyTask.Config.MaxDuration)
}

func TestTaskRegistry_GetNonExistent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	registry := NewTaskRegistry("nonexistent.yaml", logger)

	_, err := registry.Get("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task not found")
}

func TestTaskRegistry_GetAll(t *testing.T) {
	yamlContent := `
tasks:
  - id: task1
    name: "Task 1"
    description: "Test task 1"
    interval:
      type: daily
      value: 1
    timeout: 1m
    retries: 1
    config:
      endpoint: "https://api.example.com/task1"
      method: POST

  - id: task2
    name: "Task 2"
    description: "Test task 2"
    interval:
      type: hourly
      value: 1
    timeout: 2m
    retries: 2
    config:
      endpoint: "https://api.example.com/task2"
      method: GET
`

	tmpFile := createTempFile(t, yamlContent)
	defer os.Remove(tmpFile)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	registry := NewTaskRegistry(tmpFile, logger)

	err := registry.LoadFromFile()
	require.NoError(t, err)

	tasks := registry.GetAll()
	assert.Len(t, tasks, 2)
	assert.Contains(t, tasks, "task1")
	assert.Contains(t, tasks, "task2")

	// Verify tasks are copies (modification doesn't affect registry)
	tasks["task1"].Name = "Modified"
	originalTask, _ := registry.Get("task1")
	assert.Equal(t, "Task 1", originalTask.Name)
}

func TestTaskRegistry_List(t *testing.T) {
	yamlContent := `
tasks:
  - id: alpha
    name: "Alpha Task"
    description: "Test task"
    interval:
      type: daily
      value: 1
    timeout: 1m
    retries: 1
    config:
      endpoint: "https://api.example.com/alpha"

  - id: beta
    name: "Beta Task"
    description: "Test task"
    interval:
      type: hourly
      value: 1
    timeout: 1m
    retries: 1
    config:
      endpoint: "https://api.example.com/beta"
`

	tmpFile := createTempFile(t, yamlContent)
	defer os.Remove(tmpFile)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	registry := NewTaskRegistry(tmpFile, logger)

	err := registry.LoadFromFile()
	require.NoError(t, err)

	ids := registry.List()
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, "alpha")
	assert.Contains(t, ids, "beta")
}

func TestTaskRegistry_Validation(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent string
		expectError string
	}{
		{
			name: "missing ID",
			yamlContent: `
tasks:
  - name: "Test Task"
    interval:
      type: daily
      value: 1
    timeout: 1m
    config:
      endpoint: "https://api.example.com"
`,
			expectError: "task ID is required",
		},
		{
			name: "missing name",
			yamlContent: `
tasks:
  - id: test
    interval:
      type: daily
      value: 1
    timeout: 1m
    config:
      endpoint: "https://api.example.com"
`,
			expectError: "task name is required",
		},
		{
			name: "invalid timeout",
			yamlContent: `
tasks:
  - id: test
    name: "Test Task"
    interval:
      type: daily
      value: 1
    timeout: -1s
    config:
      endpoint: "https://api.example.com"
`,
			expectError: "task timeout must be positive",
		},
		{
			name: "negative retries",
			yamlContent: `
tasks:
  - id: test
    name: "Test Task"
    interval:
      type: daily
      value: 1
    timeout: 1m
    retries: -1
    config:
      endpoint: "https://api.example.com"
`,
			expectError: "task retries cannot be negative",
		},
		{
			name: "invalid interval type",
			yamlContent: `
tasks:
  - id: test
    name: "Test Task"
    interval:
      type: invalid
      value: 1
    timeout: 1m
    config:
      endpoint: "https://api.example.com"
`,
			expectError: "invalid interval type",
		},
		{
			name: "zero interval value",
			yamlContent: `
tasks:
  - id: test
    name: "Test Task"
    interval:
      type: daily
      value: 0
    timeout: 1m
    config:
      endpoint: "https://api.example.com"
`,
			expectError: "interval value must be positive",
		},
		{
			name: "missing endpoint",
			yamlContent: `
tasks:
  - id: test
    name: "Test Task"
    interval:
      type: daily
      value: 1
    timeout: 1m
    config:
      method: POST
`,
			expectError: "task endpoint is required",
		},
		{
			name: "invalid hour",
			yamlContent: `
tasks:
  - id: test
    name: "Test Task"
    interval:
      type: daily
      value: 1
      at_time:
        hour: 25
        minute: 0
    timeout: 1m
    config:
      endpoint: "https://api.example.com"
`,
			expectError: "invalid hour: 25",
		},
		{
			name: "invalid minute",
			yamlContent: `
tasks:
  - id: test
    name: "Test Task"
    interval:
      type: daily
      value: 1
      at_time:
        hour: 12
        minute: 70
    timeout: 1m
    config:
      endpoint: "https://api.example.com"
`,
			expectError: "invalid minute: 70",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := createTempFile(t, tt.yamlContent)
			defer os.Remove(tmpFile)

			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			registry := NewTaskRegistry(tmpFile, logger)

			err := registry.LoadFromFile()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

func TestTaskRegistry_DefaultValues(t *testing.T) {
	yamlContent := `
tasks:
  - id: test
    name: "Test Task"
    description: "Test task with defaults"
    interval:
      type: daily
      value: 1
    timeout: 1m
    config:
      endpoint: "https://api.example.com"
`

	tmpFile := createTempFile(t, yamlContent)
	defer os.Remove(tmpFile)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	registry := NewTaskRegistry(tmpFile, logger)

	err := registry.LoadFromFile()
	require.NoError(t, err)

	task, err := registry.Get("test")
	require.NoError(t, err)

	// Check defaults
	assert.Equal(t, "UTC", task.Interval.Timezone) // Default timezone
	assert.Equal(t, "POST", task.Config.Method)    // Default method
	assert.Equal(t, task.Timeout, task.Config.MaxDuration) // Default max duration
	assert.Equal(t, 0, task.Retries) // Default retries
}

// Helper function to create a temporary file with content
func createTempFile(t *testing.T, content string) string {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "tasks.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)
	return tmpFile
}