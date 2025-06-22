# Scheduler Service Dockerfile
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata wget

# Set working directory
WORKDIR /app

# Copy go modules files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the scheduler binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s" \
    -o scheduler \
    ./cmd/scheduler

# Final stage - minimal runtime image
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates wget tzdata

# Create non-root user
RUN addgroup -g 1001 -S taskqueue && \
    adduser -u 1001 -S taskqueue -G taskqueue

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/scheduler /app/scheduler

# Copy configuration files
COPY --from=builder /app/config /app/config

# Replace tasks.yaml with Docker version for demo
RUN rm /app/config/tasks.yaml && mv /app/config/tasks.docker.yaml /app/config/tasks.yaml

# Set ownership
RUN chown -R taskqueue:taskqueue /app

# Switch to non-root user
USER taskqueue

# Expose ports
EXPOSE 8080 8081 9090

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:8081/health || exit 1

# Run the scheduler
ENTRYPOINT ["/app/scheduler"]
CMD ["-config", "/app/config/scheduler.docker.yaml"]