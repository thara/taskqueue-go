package queue

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thara/taskqueue-go/internal/models"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "valid config with defaults",
			config: &Config{
				RedisAddr: "localhost:6379",
			},
			wantErr: false,
		},
		{
			name: "valid config with all fields",
			config: &Config{
				RedisAddr:         "localhost:6379",
				RedisPassword:     "password",
				RedisDB:           1,
				QueueName:         "test-queue",
				MaxRetries:        5,
				DefaultVisibility: 60 * time.Second,
				DeadLetterQueue:   "dlq",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config != nil && tt.config.RedisAddr == "localhost:6379" {
				// Use miniredis for testing
				s, err := miniredis.Run()
				require.NoError(t, err)
				defer s.Close()
				tt.config.RedisAddr = s.Addr()
			}

			client, err := NewClient(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, client)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, client)
				
				// Check defaults are set
				if tt.config.MaxRetries == 0 {
					assert.Equal(t, 3, client.cfg.MaxRetries)
				}
				if tt.config.DefaultVisibility == 0 {
					assert.Equal(t, 30*time.Second, client.cfg.DefaultVisibility)
				}
				if tt.config.QueueName == "" {
					assert.Equal(t, "taskqueue:jobs", client.queueName)
				}
				
				client.Close()
			}
		})
	}
}

func TestClient_PushAndPull(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr:         s.Addr(),
		QueueName:         "test-queue",
		DefaultVisibility: 10 * time.Second,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Create a test job
	job := models.NewJob("test-task", "user-123", time.Now().Add(time.Minute))
	job.Payload = []byte(`{"test": "data"}`)

	// Test Push
	err = client.Push(ctx, job)
	require.NoError(t, err)

	// Test Pull
	pulled, err := client.Pull(ctx)
	require.NoError(t, err)
	require.NotNil(t, pulled)

	assert.Equal(t, job.ID, pulled.QueueMessage.Job.ID)
	assert.Equal(t, job.TaskTypeID, pulled.QueueMessage.Job.TaskTypeID)
	assert.Equal(t, job.UserID, pulled.QueueMessage.Job.UserID)
	assert.Equal(t, "scheduler", pulled.QueueMessage.Metadata.Source)

	// Test Ack
	err = client.Ack(ctx, pulled.MessageID, pulled.Receipt)
	require.NoError(t, err)

	// Pull again should return nil (no more messages)
	pulled2, err := client.Pull(ctx)
	require.NoError(t, err)
	assert.Nil(t, pulled2)
}

func TestClient_PushBatch(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		QueueName: "test-queue",
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Create test jobs
	jobs := []*models.Job{
		models.NewJob("task-1", "user-1", time.Now().Add(time.Minute)),
		models.NewJob("task-2", "user-2", time.Now().Add(time.Minute)),
		models.NewJob("task-3", "user-3", time.Now().Add(time.Minute)),
	}

	// Test PushBatch
	err = client.PushBatch(ctx, jobs)
	require.NoError(t, err)

	// Pull all jobs
	pulledJobs := make([]*PulledMessage, 0, len(jobs))
	for i := 0; i < len(jobs); i++ {
		pulled, err := client.Pull(ctx)
		require.NoError(t, err)
		require.NotNil(t, pulled)
		pulledJobs = append(pulledJobs, pulled)
	}

	assert.Len(t, pulledJobs, len(jobs))

	// Verify job IDs (order might be different)
	pulledIDs := make(map[string]bool)
	for _, pulled := range pulledJobs {
		pulledIDs[pulled.QueueMessage.Job.ID] = true
	}

	for _, job := range jobs {
		assert.True(t, pulledIDs[job.ID], "Job %s not found in pulled messages", job.ID)
	}
}

func TestClient_PushNilJob(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		QueueName: "test-queue",
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Test Push with nil job
	err = client.Push(ctx, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "job cannot be nil")
}

func TestClient_PushBatchWithNilJobs(t *testing.T) {
	// Setup miniredis
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	config := &Config{
		RedisAddr: s.Addr(),
		QueueName: "test-queue",
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Test PushBatch with empty slice
	err = client.PushBatch(ctx, []*models.Job{})
	require.NoError(t, err)

	// Test PushBatch with nil jobs (should skip them)
	jobs := []*models.Job{
		models.NewJob("task-1", "user-1", time.Now().Add(time.Minute)),
		nil,
		models.NewJob("task-2", "user-2", time.Now().Add(time.Minute)),
	}

	err = client.PushBatch(ctx, jobs)
	require.NoError(t, err)

	// Should only pull 2 jobs (nil job was skipped)
	count := 0
	for {
		pulled, err := client.Pull(ctx)
		require.NoError(t, err)
		if pulled == nil {
			break
		}
		count++
	}
	assert.Equal(t, 2, count)
}