package main

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/thara/taskqueue-go/internal/storage"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Get config file path
	configPath := filepath.Join("config", "tasks.yaml")
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	// Create and load registry
	registry := storage.NewTaskRegistry(configPath, logger)
	
	if err := registry.LoadFromFile(); err != nil {
		logger.Error("Failed to load task registry", "error", err)
		os.Exit(1)
	}

	// List all tasks
	logger.Info("Task registry loaded successfully")
	logger.Info("Available tasks", "count", registry.Count())

	tasks := registry.GetAll()
	for id, task := range tasks {
		logger.Info("Task loaded",
			"id", id,
			"name", task.Name,
			"interval_type", task.Interval.Type,
			"interval_value", task.Interval.Value,
			"timeout", task.Timeout,
			"endpoint", task.Config.Endpoint,
		)
	}
}