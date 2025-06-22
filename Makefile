.PHONY: build build-scheduler build-worker test lint clean run-scheduler run-worker docker-up docker-down

# Variables
SCHEDULER_BINARY=bin/scheduler
WORKER_BINARY=bin/worker
GO=go
GOFLAGS=-v
VERSION?=$(shell git describe --tags --always --dirty)
LDFLAGS=-ldflags "-X main.Version=$(VERSION)"

# Build all binaries
build: build-scheduler build-worker

# Build scheduler
build-scheduler:
	@echo "Building scheduler..."
	@mkdir -p bin
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(SCHEDULER_BINARY) ./cmd/scheduler

# Build worker
build-worker:
	@echo "Building worker..."
	@mkdir -p bin
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(WORKER_BINARY) ./cmd/worker

# Run tests
test:
	@echo "Running tests..."
	$(GO) test -v -race -coverprofile=coverage.out ./...

# Run specific test
test-one:
	@echo "Running test: $(TEST)"
	$(GO) test -v -race -run $(TEST) ./...

# Run integration tests
test-integration:
	@echo "Running integration tests..."
	$(GO) test -v -tags=integration ./test/integration

# Lint code
lint:
	@echo "Running linter..."
	golangci-lint run

# Format code
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

# Generate mocks
generate:
	@echo "Generating mocks..."
	$(GO) generate ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf bin/ coverage.out

# Run scheduler locally
run-scheduler: build-scheduler
	$(SCHEDULER_BINARY) -config config/scheduler.yaml

# Run worker locally
run-worker: build-worker
	$(WORKER_BINARY) -config config/worker.yaml

# Start local development environment
docker-up:
	docker-compose up -d

# Stop local development environment
docker-down:
	docker-compose down

# Install dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

# Install development tools
install-tools:
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/swaggo/swag/cmd/swag@latest
	go install github.com/golang/mock/mockgen@latest

# Run local development with hot reload (requires air)
dev-scheduler:
	air -c .air.scheduler.toml

dev-worker:
	air -c .air.worker.toml

# View coverage report
coverage: test
	$(GO) tool cover -html=coverage.out

# Check for security vulnerabilities
security:
	@echo "Running security scan..."
	govulncheck ./...

# Build Docker images
docker-build:
	docker build -f build/scheduler.Dockerfile -t taskqueue-scheduler:$(VERSION) .
	docker build -f build/worker.Dockerfile -t taskqueue-worker:$(VERSION) .