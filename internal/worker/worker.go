package worker

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/thara/taskqueue-go/internal/models"
	"github.com/thara/taskqueue-go/internal/queue"
	"github.com/thara/taskqueue-go/internal/ratelimit"
	"github.com/thara/taskqueue-go/internal/storage"
)

type Config struct {
	PoolSize         int           `yaml:"pool_size"`
	PollInterval     time.Duration `yaml:"poll_interval"`
	MaxJobTimeout    time.Duration `yaml:"max_job_timeout"`
	RetryDelay       time.Duration `yaml:"retry_delay"`
	MaxRetries       int           `yaml:"max_retries"`
	ExecutionTimeout time.Duration `yaml:"execution_timeout"`
	RetryBackoff     string        `yaml:"retry_backoff"`
	RetryBaseDelay   time.Duration `yaml:"retry_base_delay"`
	RetryMaxDelay    time.Duration `yaml:"retry_max_delay"`
}

type Pool struct {
	config      Config
	queueClient *queue.Client
	rateLimiter *ratelimit.Limiter
	registry    *storage.TaskRegistry
	workers     []*Worker
	wg          sync.WaitGroup
	stopCh      chan struct{}
	httpClient  *http.Client
	logger      *slog.Logger
}

type Worker struct {
	id     int
	pool   *Pool
	stopCh chan struct{}
}

func New(
	config Config,
	queueClient *queue.Client,
	rateLimiter *ratelimit.Limiter,
	registry *storage.TaskRegistry,
) *Pool {
	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: config.ExecutionTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	pool := &Pool{
		config:      config,
		queueClient: queueClient,
		rateLimiter: rateLimiter,
		registry:    registry,
		stopCh:      make(chan struct{}),
		httpClient:  httpClient,
		logger:      slog.Default(),
	}

	// Create workers
	pool.workers = make([]*Worker, config.PoolSize)
	for i := 0; i < config.PoolSize; i++ {
		pool.workers[i] = &Worker{
			id:     i,
			pool:   pool,
			stopCh: make(chan struct{}),
		}
	}

	return pool
}

func (p *Pool) Start(ctx context.Context) error {
	p.logger.Info("Starting worker pool", "pool_size", p.config.PoolSize)

	// Start all workers
	for _, worker := range p.workers {
		p.wg.Add(1)
		go func(w *Worker) {
			defer p.wg.Done()
			w.run(ctx)
		}(worker)
	}

	// Wait for context cancellation or stop signal
	select {
	case <-ctx.Done():
		p.logger.Info("Worker pool context cancelled")
	case <-p.stopCh:
		p.logger.Info("Worker pool stop requested")
	}

	return nil
}

func (p *Pool) Stop() error {
	p.logger.Info("Stopping worker pool")

	// Signal all workers to stop
	close(p.stopCh)
	for _, worker := range p.workers {
		close(worker.stopCh)
	}

	// Wait for all workers to finish
	p.wg.Wait()

	p.logger.Info("Worker pool stopped")
	return nil
}

func (w *Worker) run(ctx context.Context) {
	w.pool.logger.Debug("Worker started", "worker_id", w.id)
	defer w.pool.logger.Debug("Worker stopped", "worker_id", w.id)

	ticker := time.NewTicker(w.pool.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-w.pool.stopCh:
			return
		case <-ticker.C:
			if err := w.processJob(ctx); err != nil {
				w.pool.logger.Error("Error processing job", "worker_id", w.id, "error", err)
			}
		}
	}
}

func (w *Worker) processJob(ctx context.Context) error {
	// Pull job from queue
	msg, err := w.pool.queueClient.Pull(ctx)
	if err != nil {
		return fmt.Errorf("failed to pull job: %w", err)
	}

	if msg == nil {
		// No job available
		return nil
	}

	job := msg.QueueMessage.Job
	w.pool.logger.Info("Processing job",
		"worker_id", w.id,
		"job_id", job.ID,
		"task_type", job.TaskTypeID,
		"user_id", job.UserID,
	)

	// Check if job should be executed now
	if time.Now().Before(job.ExecuteAfter) {
		// Job not ready yet, put it back in queue
		// TODO: Implement delayed re-queue
		w.pool.logger.Debug("Job not ready for execution yet",
			"job_id", job.ID,
			"execute_after", job.ExecuteAfter,
		)
		// For now, just acknowledge to prevent reprocessing
		return w.pool.queueClient.Ack(ctx, msg.MessageID, msg.Receipt)
	}

	// Check rate limit
	allowed, err := w.pool.rateLimiter.Allow(ctx)
	if err != nil {
		w.pool.logger.Error("Failed to check rate limit", "job_id", job.ID, "error", err)
		// Don't ack on rate limit errors, let it retry
		return nil
	}

	if !allowed {
		w.pool.logger.Debug("Rate limit exceeded, skipping job", "job_id", job.ID)
		// Don't ack, let it retry later
		return nil
	}

	// Get task definition
	taskType, err := w.pool.registry.Get(job.TaskTypeID)
	if err != nil {
		w.pool.logger.Error("Failed to get task definition",
			"job_id", job.ID,
			"task_type", job.TaskTypeID,
			"error", err,
		)
		// Ack invalid jobs to prevent infinite retry
		return w.pool.queueClient.Ack(ctx, msg.MessageID, msg.Receipt)
	}

	// Execute job
	result := w.executeJob(ctx, job, taskType)

	// Log result
	w.pool.logger.Info("Job execution completed",
		"worker_id", w.id,
		"job_id", job.ID,
		"status", result.Status,
		"duration", result.Duration,
	)

	// Always acknowledge the message after processing
	ackErr := w.pool.queueClient.Ack(ctx, msg.MessageID, msg.Receipt)
	if ackErr != nil {
		w.pool.logger.Error("Failed to acknowledge message",
			"job_id", job.ID,
			"error", ackErr,
		)
	}

	// Handle retry logic if needed
	if result.Status == models.StatusFailed && result.ShouldRetry(w.pool.config.MaxRetries) {
		if err := w.scheduleRetry(ctx, job, result); err != nil {
			w.pool.logger.Error("Failed to schedule retry", "job_id", job.ID, "error", err)
		}
	}

	return nil
}

func (w *Worker) executeJob(ctx context.Context, job *models.Job, taskType *models.TaskType) *models.ExecutionResult {
	startTime := time.Now()
	
	result := &models.ExecutionResult{
		JobID:     job.ID,
		StartedAt: startTime,
		Status:    models.StatusRunning,
	}

	// Create request context with timeout
	execCtx, cancel := context.WithTimeout(ctx, taskType.Timeout)
	defer cancel()

	// Prepare HTTP request
	req, err := http.NewRequestWithContext(execCtx, taskType.Config.Method, taskType.Config.Endpoint, nil)
	if err != nil {
		result.Status = models.StatusFailed
		result.Error = &models.ErrorDetail{
			Code:    "REQUEST_CREATION_FAILED",
			Message: fmt.Sprintf("Failed to create HTTP request: %v", err),
			Retry:   false,
		}
		result.CompletedAt = time.Now()
		result.Duration = result.CompletedAt.Sub(startTime)
		return result
	}

	// Add headers
	for key, value := range taskType.Config.Headers {
		req.Header.Set(key, value)
	}

	// Add job-specific headers
	req.Header.Set("X-Job-ID", job.ID)
	req.Header.Set("X-User-ID", job.UserID)
	req.Header.Set("X-Task-Type", job.TaskTypeID)

	// Execute request
	resp, err := w.pool.httpClient.Do(req)
	if err != nil {
		result.Status = models.StatusFailed
		result.Error = &models.ErrorDetail{
			Code:    "HTTP_REQUEST_FAILED",
			Message: fmt.Sprintf("HTTP request failed: %v", err),
			Retry:   true, // Network errors are usually retryable
		}
		result.CompletedAt = time.Now()
		result.Duration = result.CompletedAt.Sub(startTime)
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	body := make([]byte, 0, 1024) // Pre-allocate 1KB
	buf := make([]byte, 512)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
		// Limit response size to prevent memory issues
		if len(body) > 64*1024 { // 64KB limit
			break
		}
	}

	result.Response = body
	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(startTime)

	// Determine status based on HTTP response
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Status = models.StatusSuccess
	} else {
		result.Status = models.StatusFailed
		result.Error = &models.ErrorDetail{
			Code:    fmt.Sprintf("HTTP_%d", resp.StatusCode),
			Message: fmt.Sprintf("HTTP request failed with status %d", resp.StatusCode),
			Retry:   resp.StatusCode >= 500, // Retry server errors
		}
	}

	return result
}

func (w *Worker) scheduleRetry(ctx context.Context, job *models.Job, result *models.ExecutionResult) error {
	// Calculate retry delay based on backoff strategy
	var delay time.Duration
	switch w.pool.config.RetryBackoff {
	case "exponential":
		delay = w.calculateExponentialBackoff(job.Attempt)
	default:
		delay = w.pool.config.RetryDelay
	}

	// Create retry job
	retryJob := &models.Job{
		ID:           job.ID + "_retry_" + fmt.Sprintf("%d", job.Attempt+1),
		TaskTypeID:   job.TaskTypeID,
		UserID:       job.UserID,
		ScheduledAt:  time.Now(),
		ExecuteAfter: time.Now().Add(delay),
		Attempt:      job.Attempt + 1,
		Priority:     job.Priority,
		Payload:      job.Payload,
		CreatedAt:    job.CreatedAt,
	}

	// Push retry job to queue
	if err := w.pool.queueClient.Push(ctx, retryJob); err != nil {
		return fmt.Errorf("failed to push retry job: %w", err)
	}

	w.pool.logger.Info("Scheduled job retry",
		"job_id", job.ID,
		"retry_job_id", retryJob.ID,
		"attempt", retryJob.Attempt,
		"delay", delay,
	)

	return nil
}

func (w *Worker) calculateExponentialBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return w.pool.config.RetryBaseDelay
	}

	// Calculate exponential backoff: base_delay * 2^(attempt-1)
	multiplier := 1 << (attempt - 1) // 2^(attempt-1)
	delay := time.Duration(multiplier) * w.pool.config.RetryBaseDelay

	// Cap at maximum delay
	if delay > w.pool.config.RetryMaxDelay {
		delay = w.pool.config.RetryMaxDelay
	}

	return delay
}