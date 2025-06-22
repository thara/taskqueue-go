package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/thara/message-queue-go/pkg/queue"
	"github.com/thara/taskqueue-go/internal/models"
)

// Config represents the configuration for queue client
type Config struct {
	RedisAddr         string        `yaml:"redis_addr"`
	RedisPassword     string        `yaml:"redis_password"`
	RedisDB           int           `yaml:"redis_db"`
	QueueName         string        `yaml:"queue_name"`
	MaxRetries        int           `yaml:"max_retries"`
	DefaultVisibility time.Duration `yaml:"default_visibility"`
	DeadLetterQueue   string        `yaml:"dead_letter_queue"`
}

// Client wraps the message queue client for job distribution
type Client struct {
	queue     queue.Queue
	cfg       *Config
	queueName string
}

// NewClient creates a new queue client
func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Set defaults
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.DefaultVisibility == 0 {
		cfg.DefaultVisibility = 30 * time.Second
	}
	if cfg.QueueName == "" {
		cfg.QueueName = "taskqueue:jobs"
	}

	queueCfg := &queue.Config{
		RedisAddr:         cfg.RedisAddr,
		RedisPassword:     cfg.RedisPassword,
		RedisDB:           cfg.RedisDB,
		MaxRetries:        cfg.MaxRetries,
		DefaultVisibility: cfg.DefaultVisibility,
		DeadLetterQueue:   cfg.DeadLetterQueue,
	}

	q, err := queue.NewRedisQueue(queueCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create message queue: %w", err)
	}

	return &Client{
		queue:     q,
		cfg:       cfg,
		queueName: cfg.QueueName,
	}, nil
}

// Push pushes a job to the queue
func (c *Client) Push(ctx context.Context, job *models.Job) error {
	if job == nil {
		return fmt.Errorf("job cannot be nil")
	}

	// Create queue message with metadata
	queueMsg := models.NewQueueMessage(job, "scheduler", 0)
	
	// Serialize the message
	data, err := json.Marshal(queueMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal queue message: %w", err)
	}

	return c.queue.PushMessage(ctx, c.queueName, data)
}

// PushBatch pushes multiple jobs to the queue
func (c *Client) PushBatch(ctx context.Context, jobs []*models.Job) error {
	if len(jobs) == 0 {
		return nil
	}

	// Push each job individually (Redis queue doesn't have batch push)
	for _, job := range jobs {
		if job == nil {
			continue
		}
		
		if err := c.Push(ctx, job); err != nil {
			return fmt.Errorf("failed to push job %s: %w", job.ID, err)
		}
	}

	return nil
}

// Pull pulls a job from the queue
func (c *Client) Pull(ctx context.Context) (*PulledMessage, error) {
	msg, err := c.queue.PullMessage(ctx, c.queueName, c.cfg.DefaultVisibility)
	if err != nil {
		if errors.Is(err, queue.ErrQueueEmpty) {
			return nil, nil // No message available
		}
		return nil, err
	}

	if msg == nil {
		return nil, nil // No message available
	}

	// Deserialize the queue message
	var queueMsg models.QueueMessage
	if err := json.Unmarshal(msg.Payload, &queueMsg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal queue message: %w", err)
	}

	return &PulledMessage{
		MessageID:    msg.ID,
		Receipt:      msg.Receipt,
		QueueMessage: &queueMsg,
	}, nil
}

// Ack acknowledges successful processing of a message
func (c *Client) Ack(ctx context.Context, messageID, receipt string) error {
	return c.queue.Ack(ctx, messageID, receipt)
}

// Close closes the queue client
func (c *Client) Close() error {
	if c.queue != nil {
		return c.queue.Close()
	}
	return nil
}

// PulledMessage represents a message pulled from the queue with receipt
type PulledMessage struct {
	MessageID    string
	Receipt      string
	QueueMessage *models.QueueMessage
}