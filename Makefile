# Koneksi Backup CLI Makefile

# Variables
BINARY_NAME=koneksi-backup
VERSION?=1.0.0
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
COMMIT_HASH=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS=-ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.CommitHash=${COMMIT_HASH}"

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOWORK=GOWORK=off

# Directories
CMD_DIR=./cmd/koneksi-backup
DIST_DIR=./dist

.PHONY: all build clean test deps run install docker help

# Default target
all: test build

# Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@$(GOWORK) $(GOBUILD) $(LDFLAGS) -mod=mod -o $(BINARY_NAME) $(CMD_DIR)/main.go

# Build for multiple platforms
build-all: build-linux build-windows build-darwin

build-linux:
	@echo "Building for Linux..."
	@GOOS=linux GOARCH=amd64 $(GOWORK) $(GOBUILD) $(LDFLAGS) -mod=mod -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 $(CMD_DIR)/main.go
	@GOOS=linux GOARCH=arm64 $(GOWORK) $(GOBUILD) $(LDFLAGS) -mod=mod -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 $(CMD_DIR)/main.go

build-windows:
	@echo "Building for Windows..."
	@GOOS=windows GOARCH=amd64 $(GOWORK) $(GOBUILD) $(LDFLAGS) -mod=mod -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe $(CMD_DIR)/main.go
	@GOOS=windows GOARCH=arm64 $(GOWORK) $(GOBUILD) $(LDFLAGS) -mod=mod -o $(DIST_DIR)/$(BINARY_NAME)-windows-arm64.exe $(CMD_DIR)/main.go

build-darwin:
	@echo "Building for macOS..."
	@GOOS=darwin GOARCH=amd64 $(GOWORK) $(GOBUILD) $(LDFLAGS) -mod=mod -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 $(CMD_DIR)/main.go
	@GOOS=darwin GOARCH=arm64 $(GOWORK) $(GOBUILD) $(LDFLAGS) -mod=mod -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 $(CMD_DIR)/main.go

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@$(GOCLEAN)
	@rm -f $(BINARY_NAME)
	@rm -rf $(DIST_DIR)
	@rm -rf reports/
	@rm -f backup.db
	@rm -f restore-manifest.json
	@rm -f test-file.txt new-test-file.txt

# Run tests
test:
	@echo "Running tests..."
	@$(GOWORK) $(GOTEST) -v -mod=mod ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	@$(GOWORK) $(GOTEST) -v -mod=mod -coverprofile=coverage.out ./...
	@$(GOCMD) tool cover -html=coverage.out -o coverage.html

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@$(GOMOD) download
	@$(GOMOD) tidy

# Run the application
run: build
	@echo "Running $(BINARY_NAME)..."
	@./$(BINARY_NAME)

# Install binary to $GOPATH/bin
install: build
	@echo "Installing $(BINARY_NAME)..."
	@$(GOCMD) install $(CMD_DIR)

# Build Docker image
docker:
	@echo "Building Docker image..."
	@docker build -t koneksi-backup:$(VERSION) .
	@docker tag koneksi-backup:$(VERSION) koneksi-backup:latest

# Run Docker container
docker-run:
	@echo "Running Docker container..."
	@docker run -it --rm \
		-e KONEKSI_API_CLIENT_ID \
		-e KONEKSI_API_CLIENT_SECRET \
		-v $(PWD)/reports:/app/reports \
		-v $(PWD)/backup.db:/app/backup.db \
		koneksi-backup:latest

# Format code
fmt:
	@echo "Formatting code..."
	@$(GOCMD) fmt ./...

# Lint code
lint:
	@echo "Linting code..."
	@golangci-lint run

# Create distribution directory
dist-dir:
	@mkdir -p $(DIST_DIR)

# Package for distribution
package: dist-dir build-all
	@echo "Creating distribution packages..."
	@cd $(DIST_DIR) && tar -czf $(BINARY_NAME)-linux-amd64.tar.gz $(BINARY_NAME)-linux-amd64
	@cd $(DIST_DIR) && tar -czf $(BINARY_NAME)-linux-arm64.tar.gz $(BINARY_NAME)-linux-arm64
	@cd $(DIST_DIR) && zip $(BINARY_NAME)-windows-amd64.zip $(BINARY_NAME)-windows-amd64.exe
	@cd $(DIST_DIR) && zip $(BINARY_NAME)-windows-arm64.zip $(BINARY_NAME)-windows-arm64.exe
	@cd $(DIST_DIR) && tar -czf $(BINARY_NAME)-darwin-amd64.tar.gz $(BINARY_NAME)-darwin-amd64
	@cd $(DIST_DIR) && tar -czf $(BINARY_NAME)-darwin-arm64.tar.gz $(BINARY_NAME)-darwin-arm64

# Show help
help:
	@echo "Koneksi Backup CLI Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all           Run tests and build the binary (default)"
	@echo "  build         Build the binary for current platform"
	@echo "  build-all     Build binaries for all platforms"
	@echo "  clean         Remove build artifacts"
	@echo "  test          Run tests"
	@echo "  test-coverage Run tests with coverage report"
	@echo "  deps          Download and tidy dependencies"
	@echo "  run           Build and run the application"
	@echo "  install       Install binary to \$$GOPATH/bin"
	@echo "  docker        Build Docker image"
	@echo "  docker-run    Run Docker container"
	@echo "  fmt           Format code"
	@echo "  lint          Run linter"
	@echo "  package       Create distribution packages"
	@echo "  help          Show this help message"