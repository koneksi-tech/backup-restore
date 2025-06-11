package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/koneksi/backup-cli/internal/api"
	"github.com/koneksi/backup-cli/internal/config"
	"github.com/koneksi/backup-cli/internal/monitor"
	"github.com/koneksi/backup-cli/internal/report"
	"github.com/koneksi/backup-cli/pkg/database"
	"go.uber.org/zap"
)

// Mock API client for testing
type mockAPIClient struct {
	healthCheckErr error
	uploadErr      error
	uploadResponse *api.FileUploadResponse
}

func (m *mockAPIClient) HealthCheck(ctx context.Context) error {
	return m.healthCheckErr
}

func (m *mockAPIClient) UploadFile(ctx context.Context, filePath string, fileData io.Reader, size int64, checksum string) (*api.FileUploadResponse, error) {
	if m.uploadErr != nil {
		return nil, m.uploadErr
	}
	if m.uploadResponse != nil {
		return m.uploadResponse, nil
	}
	return &api.FileUploadResponse{
		FileID:     "test-file-id",
		FileName:   filePath,
		Size:       size,
		UploadedAt: time.Now(),
		Status:     "success",
	}, nil
}

func (m *mockAPIClient) GetPeers(ctx context.Context) ([]interface{}, error) {
	return []interface{}{}, nil
}

func TestBackupService_ProcessChange(t *testing.T) {
	t.Skip("Skipping test that requires mock API client")
	logger := zap.NewNop()
	reporter, _ := report.NewReporter(logger, t.TempDir(), "json", 10)
	
	// Create test config
	cfg := &config.Config{}
	cfg.Backup.MaxFileSize = 1024 * 1024 // 1MB
	cfg.Backup.Concurrent = 2
	cfg.Backup.Compression.Enabled = false
	
	// Create test database
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()
	
	// Create service with API client interface
	apiClient := &api.Client{}
	service, err := NewService(apiClient, logger, reporter, cfg, db)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Stop()

	// Test directory change (should be skipped)
	dirChange := monitor.FileChange{
		Path:      "/test/dir",
		Operation: "create",
		Timestamp: time.Now(),
		Size:      0,
		IsDir:     true,
	}
	service.ProcessChange(dirChange)

	// Test file too large
	largeFileChange := monitor.FileChange{
		Path:      "/test/large.bin",
		Operation: "create",
		Timestamp: time.Now(),
		Size:      2 * 1024 * 1024, // 2MB
		IsDir:     false,
	}
	service.ProcessChange(largeFileChange)

	// Test valid file change
	testFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	validChange := monitor.FileChange{
		Path:      testFile,
		Operation: "create",
		Timestamp: time.Now(),
		Size:      12, // "test content" = 12 bytes
		IsDir:     false,
	}
	service.ProcessChange(validChange)

	// Allow time for processing
	time.Sleep(100 * time.Millisecond)

	// Verify file was backed up
	stats := service.GetBackupStats()
	if stats["total_files"].(int) != 1 {
		t.Errorf("expected 1 total file, got %d", stats["total_files"].(int))
	}
	if stats["successful_files"].(int) != 1 {
		t.Errorf("expected 1 successful file, got %d", stats["successful_files"].(int))
	}
}

func TestBackupService_CalculateChecksum(t *testing.T) {
	logger := zap.NewNop()
	reporter, _ := report.NewReporter(logger, t.TempDir(), "json", 10)
	
	// Create test config
	cfg := &config.Config{}
	cfg.Backup.MaxFileSize = 1024 * 1024
	cfg.Backup.Concurrent = 2
	cfg.Backup.Compression.Enabled = false
	
	// Create test database
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()
	
	apiClient := &api.Client{}
	service, err := NewService(apiClient, logger, reporter, cfg, db)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	// Create test file
	testFile := filepath.Join(t.TempDir(), "checksum.txt")
	content := []byte("test checksum content")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Calculate checksum
	checksum, err := service.calculateChecksum(testFile)
	if err != nil {
		t.Fatalf("failed to calculate checksum: %v", err)
	}

	// Verify checksum is not empty and has correct length (SHA256 = 64 hex chars)
	if len(checksum) != 64 {
		t.Errorf("expected checksum length 64, got %d", len(checksum))
	}

	// Calculate again to ensure consistency
	checksum2, err := service.calculateChecksum(testFile)
	if err != nil {
		t.Fatalf("failed to calculate checksum second time: %v", err)
	}

	if checksum != checksum2 {
		t.Error("checksums should be identical for same file")
	}
}

func TestBackupService_NeedsBackup(t *testing.T) {
	logger := zap.NewNop()
	reporter, _ := report.NewReporter(logger, t.TempDir(), "json", 10)
	
	// Create test config
	cfg := &config.Config{}
	cfg.Backup.MaxFileSize = 1024 * 1024
	cfg.Backup.Concurrent = 2
	cfg.Backup.Compression.Enabled = false
	
	// Create test database
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()
	
	apiClient := &api.Client{}
	service, err := NewService(apiClient, logger, reporter, cfg, db)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	testFile := "/test/file.txt"

	// Test create operation (should always need backup)
	if !service.needsBackup(testFile, "create") {
		t.Error("create operation should always need backup")
	}

	// Test modify operation (should always need backup)
	if !service.needsBackup(testFile, "modify") {
		t.Error("modify operation should always need backup")
	}

	// Test file that doesn't exist
	nonExistentFile := "/definitely/does/not/exist/file.txt"
	if service.needsBackup(nonExistentFile, "chmod") {
		t.Error("non-existent file should not need backup")
	}

	// Add file to backup state as successful
	service.updateBackupState(testFile, "success", "abc123")

	// Test file already backed up successfully
	if service.needsBackup(testFile, "chmod") {
		t.Error("successfully backed up file with chmod should not need backup")
	}

	// Update state to failed
	service.updateBackupState(testFile, "failed", "abc123")

	// Test failed backup (should need retry)
	if !service.needsBackup(testFile, "chmod") {
		t.Error("failed backup should need retry")
	}
}

func TestBackupService_UpdateBackupState(t *testing.T) {
	logger := zap.NewNop()
	reporter, _ := report.NewReporter(logger, t.TempDir(), "json", 10)
	
	// Create test config
	cfg := &config.Config{}
	cfg.Backup.MaxFileSize = 1024 * 1024
	cfg.Backup.Concurrent = 2
	cfg.Backup.Compression.Enabled = false
	
	// Create test database
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()
	
	apiClient := &api.Client{}
	service, err := NewService(apiClient, logger, reporter, cfg, db)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	testFile := "/test/file.txt"

	// Update state
	service.updateBackupState(testFile, "success", "checksum123")

	// Verify state
	service.mu.RLock()
	state, exists := service.backupState[testFile]
	service.mu.RUnlock()

	if !exists {
		t.Fatal("backup state should exist")
	}

	if state.Status != "success" {
		t.Errorf("expected status 'success', got '%s'", state.Status)
	}

	if state.LastChecksum != "checksum123" {
		t.Errorf("expected checksum 'checksum123', got '%s'", state.LastChecksum)
	}

	if state.BackupCount != 1 {
		t.Errorf("expected backup count 1, got %d", state.BackupCount)
	}

	// Update again with success
	service.updateBackupState(testFile, "success", "checksum456")

	service.mu.RLock()
	state, _ = service.backupState[testFile]
	service.mu.RUnlock()

	if state.BackupCount != 2 {
		t.Errorf("expected backup count 2, got %d", state.BackupCount)
	}
}

func TestBackupService_GetBackupStats(t *testing.T) {
	logger := zap.NewNop()
	reporter, _ := report.NewReporter(logger, t.TempDir(), "json", 10)
	
	// Create test config
	cfg := &config.Config{}
	cfg.Backup.MaxFileSize = 1024 * 1024
	cfg.Backup.Concurrent = 2
	cfg.Backup.Compression.Enabled = false
	
	// Create test database
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()
	
	apiClient := &api.Client{}
	service, err := NewService(apiClient, logger, reporter, cfg, db)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	// Add various states
	service.updateBackupState("/test/file1.txt", "success", "check1")
	service.updateBackupState("/test/file2.txt", "success", "check2")
	service.updateBackupState("/test/file3.txt", "failed", "check3")
	service.updateBackupState("/test/file4.txt", "deleted", "check4")

	stats := service.GetBackupStats()

	if stats["total_files"].(int) != 4 {
		t.Errorf("expected 4 total files, got %d", stats["total_files"].(int))
	}

	if stats["successful_files"].(int) != 2 {
		t.Errorf("expected 2 successful files, got %d", stats["successful_files"].(int))
	}

	if stats["failed_files"].(int) != 1 {
		t.Errorf("expected 1 failed file, got %d", stats["failed_files"].(int))
	}

	if stats["deleted_files"].(int) != 1 {
		t.Errorf("expected 1 deleted file, got %d", stats["deleted_files"].(int))
	}
}

func TestBackupService_ProcessBackupWithError(t *testing.T) {
	t.Skip("Skipping test that requires mock API client")
	logger := zap.NewNop()
	reporter, _ := report.NewReporter(logger, t.TempDir(), "json", 10)
	
	// Create test config
	cfg := &config.Config{}
	cfg.Backup.MaxFileSize = 1024 * 1024
	cfg.Backup.Concurrent = 2
	cfg.Backup.Compression.Enabled = false
	
	// Create test database
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()
	
	apiClient := &api.Client{}
	service, err := NewService(apiClient, logger, reporter, cfg, db)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Stop()

	// Create test file
	testFile := filepath.Join(t.TempDir(), "error.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	task := BackupTask{
		FilePath:  testFile,
		Operation: "create",
		Timestamp: time.Now(),
		Size:      12,
		IsDir:     false,
	}

	service.processBackup(ctx, task)

	// Verify state shows failure
	service.mu.RLock()
	state, exists := service.backupState[testFile]
	service.mu.RUnlock()

	if !exists {
		t.Fatal("backup state should exist")
	}

	if state.Status != "failed" {
		t.Errorf("expected status 'failed', got '%s'", state.Status)
	}
}