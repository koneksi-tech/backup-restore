package backup

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/koneksi/backup-cli/internal/api"
	"github.com/koneksi/backup-cli/internal/config"
	"github.com/koneksi/backup-cli/internal/monitor"
	"github.com/koneksi/backup-cli/internal/report"
	"github.com/koneksi/backup-cli/pkg/compression"
	"github.com/koneksi/backup-cli/pkg/database"
	"go.uber.org/zap"
)

// TestLargeFileBackup tests backing up a 1GB file
func TestLargeFileBackup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large file test in short mode")
	}

	// Create temporary directory
	tempDir := t.TempDir()
	
	// Create a large test file (10MB for demo, change to 1024 for 1GB)
	largeFilePath := filepath.Join(tempDir, "large_test_file.bin")
	fileSize := int64(10 * 1024 * 1024) // 10MB for demo (use 1024 * 1024 * 1024 for 1GB)
	
	t.Logf("Creating %dMB test file at %s", fileSize/(1024*1024), largeFilePath)
	if err := createLargeFile(largeFilePath, fileSize); err != nil {
		t.Fatalf("failed to create large file: %v", err)
	}
	
	// Verify file size
	info, err := os.Stat(largeFilePath)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	if info.Size() != fileSize {
		t.Fatalf("file size mismatch: expected %d, got %d", fileSize, info.Size())
	}
	
	// Setup test environment
	logger := zap.NewNop()
	reportDir := filepath.Join(tempDir, "reports")
	reporter, err := report.NewReporter(logger, reportDir, "json", 10)
	if err != nil {
		t.Fatalf("failed to create reporter: %v", err)
	}
	
	// Create test config
	cfg := &config.Config{}
	cfg.Backup.MaxFileSize = 2 * 1024 * 1024 * 1024 // 2GB limit
	cfg.Backup.Concurrent = 1 // Single worker for predictable test
	cfg.Backup.Compression.Enabled = true // Test with compression
	cfg.Backup.Compression.Level = 1 // Fast compression
	cfg.Backup.Compression.Format = "gzip"
	
	// Create test database
	dbPath := filepath.Join(tempDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()
	
	// Create mock API client
	mockClient := &mockLargeFileAPIClient{
		uploadedData: make(map[string]int64),
	}
	
	// Create service with mock client
	service := &Service{
		client:       mockClient,
		logger:       logger,
		reporter:     reporter,
		db:           db,
		config:       cfg,
		backupQueue:  make(chan BackupTask, 100),
		workerDone:   make(chan struct{}),
		backupState:  make(map[string]FileState),
		maxFileSize:  cfg.Backup.MaxFileSize,
		concurrent:   cfg.Backup.Concurrent,
		compression:  cfg.Backup.Compression.Enabled,
		exclusions:   []string{},
		ctx:          context.Background(),
	}
	
	// Initialize compressor
	if service.compression {
		compressor, err := compression.NewCompressor(cfg.Backup.Compression.Format, cfg.Backup.Compression.Level)
		if err != nil {
			t.Fatalf("failed to create compressor: %v", err)
		}
		service.compressor = compressor
	}
	
	// Start the service
	ctx := context.Background()
	service.Start(ctx)
	defer service.Stop()
	
	// Create file change event
	change := monitor.FileChange{
		Path:      largeFilePath,
		Operation: "create",
		Timestamp: time.Now(),
		Size:      fileSize,
		IsDir:     false,
	}
	
	// Process the large file
	t.Logf("Starting backup of %dMB file", fileSize/(1024*1024))
	startTime := time.Now()
	
	service.ProcessChange(change)
	
	// Wait for backup to complete (with timeout)
	timeout := time.After(5 * time.Minute) // 5-minute timeout for 1GB file
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	var backupCompleted bool
	for !backupCompleted {
		select {
		case <-timeout:
			t.Fatal("backup timed out after 5 minutes")
		case <-ticker.C:
			// Check if backup is complete
			service.mu.RLock()
			state, exists := service.backupState[largeFilePath]
			service.mu.RUnlock()
			
			if exists && state.Status == "success" {
				backupCompleted = true
				duration := time.Since(startTime)
				t.Logf("Backup completed in %v", duration)
				
				// Calculate throughput
				throughputMBps := float64(fileSize) / (1024 * 1024) / duration.Seconds()
				t.Logf("Throughput: %.2f MB/s", throughputMBps)
			} else if exists && state.Status == "failed" {
				t.Fatal("backup failed")
			}
		}
	}
	
	// Verify backup in mock client
	if len(mockClient.uploadedData) == 0 {
		t.Fatal("no files were uploaded")
	}
	
	// Check uploaded size (may be compressed)
	for fileID, size := range mockClient.uploadedData {
		t.Logf("Uploaded file %s with size %d bytes", fileID, size)
		if service.compression {
			// Compressed size should be less than original
			if size >= fileSize {
				t.Errorf("compressed size (%d) should be less than original (%d)", size, fileSize)
			}
		} else {
			// Uncompressed size should match
			if size != fileSize {
				t.Errorf("uploaded size (%d) doesn't match original (%d)", size, fileSize)
			}
		}
	}
	
	// Get backup stats
	stats := service.GetBackupStats()
	t.Logf("Backup stats: %+v", stats)
}

// createLargeFile creates a file with random data of specified size
func createLargeFile(path string, size int64) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()
	
	// Write random data in chunks
	chunkSize := int64(1024 * 1024) // 1MB chunks
	buffer := make([]byte, chunkSize)
	written := int64(0)
	
	for written < size {
		// Calculate remaining bytes
		remaining := size - written
		if remaining < chunkSize {
			buffer = buffer[:remaining]
		}
		
		// Generate random data
		if _, err := rand.Read(buffer); err != nil {
			return fmt.Errorf("failed to generate random data: %w", err)
		}
		
		// Write to file
		n, err := file.Write(buffer)
		if err != nil {
			return fmt.Errorf("failed to write to file: %w", err)
		}
		
		written += int64(n)
		
		// Print progress every 100MB
		if written%(100*1024*1024) == 0 {
			fmt.Printf("Created %d MB of %d MB\n", written/(1024*1024), size/(1024*1024))
		}
	}
	
	return nil
}

// mockLargeFileAPIClient is a mock API client for testing large file uploads
type mockLargeFileAPIClient struct {
	uploadedData map[string]int64 // fileID -> size
}

func (m *mockLargeFileAPIClient) HealthCheck(ctx context.Context) error {
	return nil
}

func (m *mockLargeFileAPIClient) UploadFile(ctx context.Context, filePath string, fileData io.Reader, size int64, checksum string) (*api.FileUploadResponse, error) {
	// Simulate reading the entire file
	data, err := io.ReadAll(fileData)
	if err != nil {
		return nil, err
	}
	
	fileID := fmt.Sprintf("large_file_%s_%d", checksum[:8], time.Now().UnixNano())
	m.uploadedData[fileID] = int64(len(data))
	
	// Simulate network delay based on size
	uploadTime := time.Duration(len(data)/1024/1024) * time.Millisecond // 1ms per MB
	time.Sleep(uploadTime)
	
	return &api.FileUploadResponse{
		FileID:     fileID,
		FileName:   filepath.Base(filePath),
		Size:       size,
		UploadedAt: time.Now(),
		Status:     "success",
	}, nil
}

func (m *mockLargeFileAPIClient) GetPeers(ctx context.Context) ([]interface{}, error) {
	return []interface{}{}, nil
}

func (m *mockLargeFileAPIClient) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("download not implemented in mock")
}

// Benchmark for large file backup
func BenchmarkLargeFileBackup(b *testing.B) {
	// Create temporary directory
	tempDir := b.TempDir()
	
	// Create test files of different sizes
	sizes := []int64{
		10 * 1024 * 1024,   // 10MB
		100 * 1024 * 1024,  // 100MB
		500 * 1024 * 1024,  // 500MB
	}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("%dMB", size/(1024*1024)), func(b *testing.B) {
			// Create test file
			filePath := filepath.Join(tempDir, fmt.Sprintf("bench_%d.bin", size))
			if err := createLargeFile(filePath, size); err != nil {
				b.Fatalf("failed to create file: %v", err)
			}
			
			// Setup minimal test environment
			logger := zap.NewNop()
			cfg := &config.Config{}
			cfg.Backup.MaxFileSize = 2 * 1024 * 1024 * 1024
			cfg.Backup.Compression.Enabled = true
			cfg.Backup.Compression.Level = 1
			cfg.Backup.Compression.Format = "gzip"
			
			b.ResetTimer()
			
			for i := 0; i < b.N; i++ {
				// Simulate file compression
				file, err := os.Open(filePath)
				if err != nil {
					b.Fatal(err)
				}
				
				// Create compressor
				compressor, _ := compression.NewCompressor(cfg.Backup.Compression.Format, cfg.Backup.Compression.Level)
				
				// Compress to memory
				compressed, err := compressor.Compress(file)
				if err != nil {
					b.Fatal(err)
				}
				
				file.Close()
				compressed.Close()
			}
			
			b.SetBytes(size)
		})
	}
}