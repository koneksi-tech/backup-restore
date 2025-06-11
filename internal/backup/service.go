package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/koneksi/backup-cli/internal/api"
	"github.com/koneksi/backup-cli/internal/config"
	"github.com/koneksi/backup-cli/internal/monitor"
	"github.com/koneksi/backup-cli/internal/report"
	"github.com/koneksi/backup-cli/pkg/compression"
	"github.com/koneksi/backup-cli/pkg/database"
	"go.uber.org/zap"
)

type Service struct {
	client       *api.Client
	logger       *zap.Logger
	reporter     *report.Reporter
	maxFileSize  int64
	concurrent   int
	backupQueue  chan BackupTask
	wg           sync.WaitGroup
	mu           sync.RWMutex
	backupState  map[string]*FileBackupState
	compressor   compression.Compressor
	compression  bool
	db           *database.DB
}

type BackupTask struct {
	FilePath  string
	Operation string
	Timestamp time.Time
	Size      int64
	IsDir     bool
}

type FileBackupState struct {
	LastBackup   time.Time
	LastChecksum string
	BackupCount  int
	Status       string
}

type BackupResult struct {
	FilePath       string
	FileID         string
	Operation      string
	Success        bool
	Error          error
	StartTime      time.Time
	EndTime        time.Time
	Size           int64
	CompressedSize int64
	Checksum       string
	Compressed     bool
}

func NewService(client *api.Client, logger *zap.Logger, reporter *report.Reporter, cfg *config.Config, db *database.DB) (*Service, error) {
	var compressor compression.Compressor
	var err error
	
	if cfg.Backup.Compression.Enabled {
		compressor, err = compression.NewCompressor(cfg.Backup.Compression.Format, cfg.Backup.Compression.Level)
		if err != nil {
			return nil, fmt.Errorf("failed to create compressor: %w", err)
		}
	} else {
		compressor, _ = compression.NewCompressor("none", 0)
	}

	service := &Service{
		client:      client,
		logger:      logger,
		reporter:    reporter,
		maxFileSize: cfg.Backup.MaxFileSize,
		concurrent:  cfg.Backup.Concurrent,
		backupQueue: make(chan BackupTask, 1000),
		backupState: make(map[string]*FileBackupState),
		compressor:  compressor,
		compression: cfg.Backup.Compression.Enabled,
		db:          db,
	}

	// Load existing file states from database
	if err := service.loadFileStatesFromDB(); err != nil {
		logger.Warn("failed to load file states from database", zap.Error(err))
	}

	return service, nil
}

func (s *Service) Start(ctx context.Context) {
	// Start worker pool
	for i := 0; i < s.concurrent; i++ {
		s.wg.Add(1)
		go s.worker(ctx, i)
	}

	// Start periodic state cleanup
	go s.cleanupRoutine(ctx)
}

func (s *Service) ProcessChange(change monitor.FileChange) {
	// Skip directories for backup
	if change.IsDir {
		s.logger.Debug("skipping directory", zap.String("path", change.Path))
		return
	}

	// Skip files that are too large
	if change.Size > s.maxFileSize {
		s.logger.Warn("file too large for backup",
			zap.String("path", change.Path),
			zap.Int64("size", change.Size),
			zap.Int64("maxSize", s.maxFileSize),
		)
		return
	}

	// Check if file needs backup
	if !s.needsBackup(change.Path, change.Operation) {
		s.logger.Debug("file does not need backup", zap.String("path", change.Path))
		return
	}

	task := BackupTask{
		FilePath:  change.Path,
		Operation: change.Operation,
		Timestamp: change.Timestamp,
		Size:      change.Size,
		IsDir:     change.IsDir,
	}

	s.logger.Info("queuing backup task", 
		zap.String("path", task.FilePath),
		zap.String("operation", task.Operation),
		zap.Int64("size", task.Size),
	)

	select {
	case s.backupQueue <- task:
		s.logger.Debug("queued backup task", zap.String("path", task.FilePath))
	default:
		s.logger.Warn("backup queue full, dropping task", zap.String("path", task.FilePath))
	}
}

func (s *Service) worker(ctx context.Context, id int) {
	defer s.wg.Done()
	s.logger.Info("backup worker started", zap.Int("worker_id", id))

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("backup worker stopping", zap.Int("worker_id", id))
			return
		case task, ok := <-s.backupQueue:
			if !ok {
				s.logger.Info("backup queue closed, worker stopping", zap.Int("worker_id", id))
				return
			}
			s.logger.Info("worker processing backup task", 
				zap.Int("worker_id", id),
				zap.String("path", task.FilePath),
			)
			s.processBackup(ctx, task)
		}
	}
}

func (s *Service) processBackup(ctx context.Context, task BackupTask) {
	result := BackupResult{
		FilePath:   task.FilePath,
		Operation:  task.Operation,
		StartTime:  time.Now(),
		Size:       task.Size,
		Compressed: s.compression,
	}

	// Handle delete operations
	if task.Operation == "delete" {
		result.Success = true
		result.EndTime = time.Now()
		s.updateBackupState(task.FilePath, "deleted", "")
		s.reporter.AddResult(report.BackupResult{
		FilePath:       result.FilePath,
		FileID:         result.FileID,
		Operation:      result.Operation,
		Success:        result.Success,
		Error:          result.Error,
		StartTime:      result.StartTime,
		EndTime:        result.EndTime,
		Size:           result.Size,
		CompressedSize: result.CompressedSize,
		Checksum:       result.Checksum,
		Compressed:     result.Compressed,
	})
		return
	}

	// Calculate file checksum
	checksum, err := s.calculateChecksum(task.FilePath)
	if err != nil {
		result.Error = fmt.Errorf("failed to calculate checksum: %w", err)
		result.EndTime = time.Now()
		s.reporter.AddResult(s.convertToReportResult(result))
		return
	}
	result.Checksum = checksum

	// Check if file has changed
	s.mu.RLock()
	state, exists := s.backupState[task.FilePath]
	s.mu.RUnlock()

	if exists && state.LastChecksum == checksum {
		s.logger.Debug("file unchanged, skipping backup", zap.String("path", task.FilePath))
		return
	}

	// Open file for reading
	file, err := os.Open(task.FilePath)
	if err != nil {
		result.Error = fmt.Errorf("failed to open file: %w", err)
		result.EndTime = time.Now()
		s.reporter.AddResult(s.convertToReportResult(result))
		return
	}
	defer file.Close()

	var uploadData io.Reader = file
	var uploadSize int64 = task.Size

	// Compress if enabled
	if s.compression {
		compressedData, err := compression.CompressFile(file, s.compressor)
		if err != nil {
			result.Error = fmt.Errorf("failed to compress file: %w", err)
			result.EndTime = time.Now()
			s.reporter.AddResult(s.convertToReportResult(result))
			return
		}
		
		uploadData = bytes.NewReader(compressedData)
		uploadSize = int64(len(compressedData))
		result.CompressedSize = uploadSize
		
		compressionRatio := compression.CompressionRatio(task.Size, uploadSize)
		s.logger.Debug("file compressed",
			zap.String("path", task.FilePath),
			zap.Int64("originalSize", task.Size),
			zap.Int64("compressedSize", uploadSize),
			zap.Float64("compressionRatio", compressionRatio),
		)
	}

	// Upload file to Koneksi
	uploadResp, err := s.client.UploadFile(ctx, task.FilePath, uploadData, uploadSize, checksum)
	if err != nil {
		result.Error = fmt.Errorf("failed to upload file: %w", err)
		result.EndTime = time.Now()
		s.updateBackupState(task.FilePath, "failed", checksum)
		s.reporter.AddResult(s.convertToReportResult(result))
		return
	}

	result.FileID = uploadResp.FileID
	result.Success = true
	result.EndTime = time.Now()

	s.updateBackupState(task.FilePath, "success", checksum)
	s.reporter.AddResult(s.convertToReportResult(result))

	// Save to database
	if s.db != nil {
		dbRecord := database.BackupRecord{
			FilePath:       task.FilePath,
			FileID:         uploadResp.FileID,
			Checksum:       checksum,
			OriginalSize:   task.Size,
			CompressedSize: uploadSize,
			IsCompressed:   s.compression,
			BackupTime:     time.Now(),
			Status:         "success",
			Operation:      task.Operation,
		}
		if _, err := s.db.InsertBackupRecord(dbRecord); err != nil {
			s.logger.Error("failed to save backup record to database", zap.Error(err))
		}
	}

	s.logger.Info("file backed up successfully",
		zap.String("path", task.FilePath),
		zap.String("fileID", uploadResp.FileID),
		zap.Duration("duration", result.EndTime.Sub(result.StartTime)),
		zap.Bool("compressed", s.compression),
	)
}

func (s *Service) needsBackup(filePath, operation string) bool {
	// Always backup on create or modify
	if operation == "create" || operation == "modify" {
		return true
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return false
	}

	// Check last backup time
	s.mu.RLock()
	state, exists := s.backupState[filePath]
	s.mu.RUnlock()

	if !exists {
		return true
	}

	// Re-backup if last backup failed
	if state.Status == "failed" {
		return true
	}

	return false
}

func (s *Service) calculateChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Service) updateBackupState(filePath, status, checksum string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, exists := s.backupState[filePath]
	if !exists {
		state = &FileBackupState{}
		s.backupState[filePath] = state
	}

	state.Status = status
	state.LastBackup = time.Now()
	if checksum != "" {
		state.LastChecksum = checksum
	}
	if status == "success" {
		state.BackupCount++
	}

	// Update database
	if s.db != nil {
		dbState := database.FileState{
			FilePath:     filePath,
			LastChecksum: state.LastChecksum,
			LastBackup:   state.LastBackup,
			BackupCount:  state.BackupCount,
			Status:       state.Status,
		}
		if err := s.db.UpdateFileState(dbState); err != nil {
			s.logger.Error("failed to update file state in database", zap.Error(err))
		}
	}
}

func (s *Service) cleanupRoutine(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupDeletedFiles()
		}
	}
}

func (s *Service) cleanupDeletedFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for path, state := range s.backupState {
		if state.Status == "deleted" {
			// Keep deleted file state for 24 hours
			if time.Since(state.LastBackup) > 24*time.Hour {
				delete(s.backupState, path)
			}
		}
	}
}

func (s *Service) Stop() {
	close(s.backupQueue)
	s.wg.Wait()
}

func (s *Service) convertToReportResult(result BackupResult) report.BackupResult {
	return report.BackupResult{
		FilePath:       result.FilePath,
		FileID:         result.FileID,
		Operation:      result.Operation,
		Success:        result.Success,
		Error:          result.Error,
		StartTime:      result.StartTime,
		EndTime:        result.EndTime,
		Size:           result.Size,
		CompressedSize: result.CompressedSize,
		Checksum:       result.Checksum,
		Compressed:     result.Compressed,
	}
}

func (s *Service) GetBackupStats() map[string]interface{} {
	// Try to get stats from database first
	if s.db != nil {
		dbStats, err := s.db.GetBackupStats()
		if err == nil {
			return dbStats
		}
		s.logger.Warn("failed to get stats from database, using in-memory stats", zap.Error(err))
	}

	// Fallback to in-memory stats
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[string]interface{}{
		"total_files":      len(s.backupState),
		"successful_files": 0,
		"failed_files":     0,
		"deleted_files":    0,
	}

	for _, state := range s.backupState {
		switch state.Status {
		case "success":
			stats["successful_files"] = stats["successful_files"].(int) + 1
		case "failed":
			stats["failed_files"] = stats["failed_files"].(int) + 1
		case "deleted":
			stats["deleted_files"] = stats["deleted_files"].(int) + 1
		}
	}

	return stats
}

func (s *Service) loadFileStatesFromDB() error {
	if s.db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Query all file states from database
	criteria := database.SearchCriteria{
		Limit: 10000, // Load up to 10k files
	}
	
	records, err := s.db.SearchBackups(criteria)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Populate in-memory state from database
	for _, record := range records {
		state := &FileBackupState{
			LastBackup:   record.BackupTime,
			LastChecksum: record.Checksum,
			Status:       record.Status,
		}
		
		// Get backup count from file state
		dbState, err := s.db.GetFileState(record.FilePath)
		if err == nil && dbState != nil {
			state.BackupCount = dbState.BackupCount
		}
		
		s.backupState[record.FilePath] = state
	}

	s.logger.Info("loaded file states from database", zap.Int("count", len(s.backupState)))
	return nil
}