package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"

	"github.com/thara/taskqueue-go/internal/queue"
	"github.com/thara/taskqueue-go/internal/scheduler"
	"github.com/thara/taskqueue-go/internal/storage"
)

type Config struct {
	Server struct {
		Port           int           `yaml:"port"`
		ReadTimeout    time.Duration `yaml:"read_timeout"`
		WriteTimeout   time.Duration `yaml:"write_timeout"`
		ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	} `yaml:"server"`

	Redis struct {
		Addr        string        `yaml:"addr"`
		Password    string        `yaml:"password"`
		DB          int           `yaml:"db"`
		MaxRetries  int           `yaml:"max_retries"`
		DialTimeout time.Duration `yaml:"dial_timeout"`
		ReadTimeout time.Duration `yaml:"read_timeout"`
		WriteTimeout time.Duration `yaml:"write_timeout"`
	} `yaml:"redis"`

	Queue struct {
		RedisAddr         string        `yaml:"redis_addr"`
		RedisPassword     string        `yaml:"redis_password"`
		RedisDB           int           `yaml:"redis_db"`
		QueueName         string        `yaml:"queue_name"`
		MaxRetries        int           `yaml:"max_retries"`
		DefaultVisibility time.Duration `yaml:"default_visibility"`
		DeadLetterQueue   string        `yaml:"dead_letter_queue"`
	} `yaml:"queue"`

	Scheduler struct {
		CheckInterval        time.Duration `yaml:"check_interval"`
		BatchSize            int           `yaml:"batch_size"`
		DistributionWindow   time.Duration `yaml:"distribution_window"`
		LeaderElectionTTL    time.Duration `yaml:"leader_election_ttl"`
		EnableLeaderElection bool          `yaml:"enable_leader_election"`
	} `yaml:"scheduler"`

	Logging struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
		Output string `yaml:"output"`
	} `yaml:"logging"`

	Metrics struct {
		Enabled bool   `yaml:"enabled"`
		Port    int    `yaml:"port"`
		Path    string `yaml:"path"`
	} `yaml:"metrics"`

	Health struct {
		Port int    `yaml:"port"`
		Path string `yaml:"path"`
	} `yaml:"health"`
}

func main() {
	var configFile string
	flag.StringVar(&configFile, "config", "config/scheduler.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := loadConfig(configFile)
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Setup logging
	setupLogging(cfg.Logging)

	slog.Info("Starting scheduler service", "config", configFile)

	// Setup Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		MaxRetries:   cfg.Redis.MaxRetries,
		DialTimeout:  cfg.Redis.DialTimeout,
		ReadTimeout:  cfg.Redis.ReadTimeout,
		WriteTimeout: cfg.Redis.WriteTimeout,
	})

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		slog.Error("Failed to connect to Redis", "error", err)
		os.Exit(1)
	}
	slog.Info("Connected to Redis", "addr", cfg.Redis.Addr)

	// Setup queue client
	queueSvc, err := queue.NewClient(&queue.Config{
		RedisAddr:         cfg.Queue.RedisAddr,
		RedisPassword:     cfg.Queue.RedisPassword,
		RedisDB:           cfg.Queue.RedisDB,
		QueueName:         cfg.Queue.QueueName,
		MaxRetries:        cfg.Queue.MaxRetries,
		DefaultVisibility: cfg.Queue.DefaultVisibility,
		DeadLetterQueue:   cfg.Queue.DeadLetterQueue,
	})
	if err != nil {
		slog.Error("Failed to create queue client", "error", err)
		os.Exit(1)
	}

	// Load task registry
	registry := storage.NewTaskRegistry("config/tasks.yaml", slog.Default())
	if err := registry.LoadFromFile(); err != nil {
		slog.Error("Failed to load task registry", "error", err)
		os.Exit(1)
	}

	// Setup user store
	userStore := storage.NewUserStore(redisClient, slog.Default())

	// Create scheduler service
	schedulerSvc := scheduler.New(scheduler.Config{
		CheckInterval:        cfg.Scheduler.CheckInterval,
		BatchSize:            cfg.Scheduler.BatchSize,
		DistributionWindow:   cfg.Scheduler.DistributionWindow,
		LeaderElectionTTL:    cfg.Scheduler.LeaderElectionTTL,
		EnableLeaderElection: cfg.Scheduler.EnableLeaderElection,
	}, redisClient, queueSvc, registry, userStore)

	// Setup health check server
	healthMux := http.NewServeMux()
	healthMux.HandleFunc(cfg.Health.Path, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	healthServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Health.Port),
		Handler: healthMux,
	}

	// Setup metrics server (if enabled)
	var metricsServer *http.Server
	if cfg.Metrics.Enabled {
		metricsMux := http.NewServeMux()
		metricsMux.HandleFunc(cfg.Metrics.Path, func(w http.ResponseWriter, r *http.Request) {
			// TODO: Implement Prometheus metrics
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("# Metrics not implemented yet\n"))
		})

		metricsServer = &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.Metrics.Port),
			Handler: metricsMux,
		}
	}

	// Start servers
	go func() {
		slog.Info("Starting health check server", "port", cfg.Health.Port)
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Health server error", "error", err)
		}
	}()

	if metricsServer != nil {
		go func() {
			slog.Info("Starting metrics server", "port", cfg.Metrics.Port)
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("Metrics server error", "error", err)
			}
		}()
	}

	// Start scheduler
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	go func() {
		if err := schedulerSvc.Start(schedulerCtx); err != nil {
			slog.Error("Scheduler error", "error", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("Shutting down scheduler service...")

	// Shutdown scheduler
	schedulerCancel()

	// Shutdown servers
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("Error shutting down health server", "error", err)
	}

	if metricsServer != nil {
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("Error shutting down metrics server", "error", err)
		}
	}

	// Close Redis connections
	if err := redisClient.Close(); err != nil {
		slog.Error("Error closing Redis client", "error", err)
	}
	if err := queueSvc.Close(); err != nil {
		slog.Error("Error closing queue client", "error", err)
	}

	slog.Info("Scheduler service stopped")
}

func loadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &cfg, nil
}

func setupLogging(cfg struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}) {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		})
	}

	slog.SetDefault(slog.New(handler))
}