package storage

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thara/taskqueue-go/internal/models"
)

// TaskRegistry manages task type definitions
type TaskRegistry struct {
	tasks    map[string]*models.TaskType
	mu       sync.RWMutex
	filePath string
	logger   *slog.Logger
}

// NewTaskRegistry creates a new task registry
func NewTaskRegistry(filePath string, logger *slog.Logger) *TaskRegistry {
	return &TaskRegistry{
		tasks:    make(map[string]*models.TaskType),
		filePath: filePath,
		logger:   logger,
	}
}

// LoadFromFile loads task definitions from a YAML file
func (r *TaskRegistry) LoadFromFile() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.filePath)
	if err != nil {
		return fmt.Errorf("failed to read task definitions file: %w", err)
	}

	var config TaskConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse task definitions: %w", err)
	}

	// Validate and store tasks
	tasks := make(map[string]*models.TaskType)
	for _, taskDef := range config.Tasks {
		// Convert from config format to model format
		task := &models.TaskType{
			ID:          taskDef.ID,
			Name:        taskDef.Name,
			Description: taskDef.Description,
			Timeout:     taskDef.Timeout,
			Retries:     taskDef.Retries,
			Interval: models.TaskInterval{
				Type:     models.IntervalType(taskDef.Interval.Type),
				Value:    taskDef.Interval.Value,
				Timezone: taskDef.Interval.Timezone,
			},
			Config: models.TaskConfig{
				Endpoint:    taskDef.Config.Endpoint,
				Method:      taskDef.Config.Method,
				Headers:     taskDef.Config.Headers,
				MaxDuration: taskDef.Config.MaxDuration,
			},
		}

		// Convert AtTime if present
		if taskDef.Interval.AtTime != nil {
			task.Interval.AtTime = &models.TimeOfDay{
				Hour:   taskDef.Interval.AtTime.Hour,
				Minute: taskDef.Interval.AtTime.Minute,
			}
		}

		// Validate task
		if err := r.validateTask(task); err != nil {
			return fmt.Errorf("invalid task %s: %w", task.ID, err)
		}

		tasks[task.ID] = task
	}

	// Replace current tasks
	r.tasks = tasks

	r.logger.Info("Loaded task definitions",
		"count", len(tasks),
		"file", r.filePath,
	)

	return nil
}

// Get retrieves a task by ID
func (r *TaskRegistry) Get(id string) (*models.TaskType, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	task, exists := r.tasks[id]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", id)
	}

	// Return a copy to prevent external modification
	taskCopy := *task
	return &taskCopy, nil
}

// GetAll returns all task definitions
func (r *TaskRegistry) GetAll() map[string]*models.TaskType {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return copies to prevent external modification
	result := make(map[string]*models.TaskType)
	for id, task := range r.tasks {
		taskCopy := *task
		result[id] = &taskCopy
	}

	return result
}

// List returns all task IDs
func (r *TaskRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.tasks))
	for id := range r.tasks {
		ids = append(ids, id)
	}

	return ids
}

// Count returns the number of registered tasks
func (r *TaskRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.tasks)
}

// validateTask validates a task definition
func (r *TaskRegistry) validateTask(task *models.TaskType) error {
	if task.ID == "" {
		return fmt.Errorf("task ID is required")
	}

	if task.Name == "" {
		return fmt.Errorf("task name is required")
	}

	if task.Timeout <= 0 {
		return fmt.Errorf("task timeout must be positive")
	}

	if task.Retries < 0 {
		return fmt.Errorf("task retries cannot be negative")
	}

	// Validate interval
	if err := r.validateInterval(&task.Interval); err != nil {
		return fmt.Errorf("invalid interval: %w", err)
	}

	// Validate config
	if task.Config.Endpoint == "" {
		return fmt.Errorf("task endpoint is required")
	}

	if task.Config.Method == "" {
		task.Config.Method = "POST" // Default to POST
	}

	if task.Config.MaxDuration <= 0 {
		task.Config.MaxDuration = task.Timeout // Default to task timeout
	}

	return nil
}

// validateInterval validates a task interval
func (r *TaskRegistry) validateInterval(interval *models.TaskInterval) error {
	switch interval.Type {
	case models.IntervalTypeHourly, models.IntervalTypeDaily, 
		 models.IntervalTypeWeekly, models.IntervalTypeMonthly:
		// Valid types
	default:
		return fmt.Errorf("invalid interval type: %s", interval.Type)
	}

	if interval.Value <= 0 {
		return fmt.Errorf("interval value must be positive")
	}

	if interval.Timezone == "" {
		interval.Timezone = "UTC" // Default to UTC
	}

	// Validate AtTime if present
	if interval.AtTime != nil {
		if interval.AtTime.Hour < 0 || interval.AtTime.Hour > 23 {
			return fmt.Errorf("invalid hour: %d", interval.AtTime.Hour)
		}
		if interval.AtTime.Minute < 0 || interval.AtTime.Minute > 59 {
			return fmt.Errorf("invalid minute: %d", interval.AtTime.Minute)
		}
	}

	return nil
}

// TaskConfig represents the YAML configuration structure
type TaskConfig struct {
	Tasks []TaskDefinition `yaml:"tasks"`
}

// TaskDefinition represents a task definition in YAML format
type TaskDefinition struct {
	ID          string             `yaml:"id"`
	Name        string             `yaml:"name"`
	Description string             `yaml:"description"`
	Interval    IntervalDefinition `yaml:"interval"`
	Timeout     time.Duration      `yaml:"timeout"`
	Retries     int                `yaml:"retries"`
	Config      ConfigDefinition   `yaml:"config"`
}

// IntervalDefinition represents an interval definition in YAML format
type IntervalDefinition struct {
	Type     string       `yaml:"type"`
	Value    int          `yaml:"value"`
	Timezone string       `yaml:"timezone"`
	AtTime   *TimeOfDay   `yaml:"at_time,omitempty"`
}

// TimeOfDay represents a time of day in YAML format
type TimeOfDay struct {
	Hour   int `yaml:"hour"`
	Minute int `yaml:"minute"`
}

// ConfigDefinition represents task configuration in YAML format
type ConfigDefinition struct {
	Endpoint    string            `yaml:"endpoint"`
	Method      string            `yaml:"method"`
	Headers     map[string]string `yaml:"headers"`
	MaxDuration time.Duration     `yaml:"max_duration"`
}