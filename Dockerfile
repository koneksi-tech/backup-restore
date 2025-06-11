# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make gcc musl-dev sqlite-dev

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=1 GOOS=linux go build -mod=mod -a -installsuffix cgo -ldflags="-w -s" -o koneksi-backup cmd/koneksi-backup/main.go

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache ca-certificates sqlite-libs

# Create non-root user
RUN adduser -D -g '' appuser

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/koneksi-backup /app/

# Create necessary directories
RUN mkdir -p /app/reports /app/data && \
    chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Volume for persistent data
VOLUME ["/app/reports", "/app/data"]

# Environment variables
ENV KONEKSI_API_CLIENT_ID=""
ENV KONEKSI_API_CLIENT_SECRET=""
ENV KONEKSI_API_DIRECTORY_ID=""

# Default config path
ENV CONFIG_PATH="/app/data/config.yaml"

# Expose health check port (if needed in future)
# EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ./koneksi-backup status || exit 1

# Default command
ENTRYPOINT ["./koneksi-backup"]
CMD ["run", "-c", "/app/data/config.yaml"]