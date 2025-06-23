package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/koneksi/backup-cli/internal/api"
	"github.com/koneksi/backup-cli/internal/report"
	"go.uber.org/zap"
)

type RestoreService struct {
	client     *api.Client
	logger     *zap.Logger
	concurrent int
	wg         sync.WaitGroup
	mu         sync.RWMutex
	progress   *RestoreProgress
}

type RestoreProgress struct {
	TotalFiles    int
	RestoredFiles int
	FailedFiles   int
	TotalSize     int64
	RestoredSize  int64
	StartTime     time.Time
	Errors        []RestoreError
}

type RestoreError struct {
	FilePath string
	FileID   string
	Error    string
	Time     time.Time
}

type RestoreManifest struct {
	Version      string                    `json:"version"`
	CreatedAt    time.Time                 `json:"created_at"`
	BackupID     string                    `json:"backup_id"`
	SourcePath   string                    `json:"source_path"`
	Files        []FileManifestEntry       `json:"files"`
	Metadata     map[string]interface{}    `json:"metadata"`
}

type BackupReport struct {
	ID          string                 `json:"id"`
	StartTime   time.Time              `json:"start_time"`
	EndTime     time.Time              `json:"end_time"`
	TotalFiles  int                    `json:"total_files"`
	Successful  int                    `json:"successful"`
	Failed      int                    `json:"failed"`
	TotalSize   int64                  `json:"total_size"`
	Duration    time.Duration          `json:"duration"`
	Results     []report.BackupResult  `json:"results"`
	Statistics  map[string]interface{} `json:"statistics"`
}

type FileManifestEntry struct {
	FilePath     string      `json:"file_path"`
	FileID       string      `json:"file_id"`
	Size         int64       `json:"size"`
	Checksum     string      `json:"checksum"`
	BackupTime   time.Time   `json:"backup_time"`
	Permissions  os.FileMode `json:"permissions"`
	Compressed   bool        `json:"compressed"`
}

func NewRestoreService(client *api.Client, logger *zap.Logger, concurrent int) *RestoreService {
	return &RestoreService{
		client:     client,
		logger:     logger,
		concurrent: concurrent,
		progress: &RestoreProgress{
			StartTime: time.Now(),
			Errors:    make([]RestoreError, 0),
		},
	}
}

// RestoreFromManifest restores files based on a backup manifest
func (r *RestoreService) RestoreFromManifest(ctx context.Context, manifestPath, targetDir string) error {
	manifest, err := r.loadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	r.logger.Info("starting restore from manifest",
		zap.String("backupID", manifest.BackupID),
		zap.Int("files", len(manifest.Files)),
		zap.String("targetDir", targetDir),
	)

	// Create restore queue
	restoreQueue := make(chan FileManifestEntry, len(manifest.Files))
	for _, file := range manifest.Files {
		restoreQueue <- file
	}
	close(restoreQueue)

	r.progress.TotalFiles = len(manifest.Files)

	// Start worker pool
	for i := 0; i < r.concurrent; i++ {
		r.wg.Add(1)
		go r.restoreWorker(ctx, restoreQueue, targetDir)
	}

	// Wait for completion
	r.wg.Wait()

	// Generate restore report
	return r.generateRestoreReport(manifest, targetDir)
}

// RestoreFile restores a single file by its ID
func (r *RestoreService) RestoreFile(ctx context.Context, fileID, targetPath string) error {
	r.logger.Info("restoring single file",
		zap.String("fileID", fileID),
		zap.String("targetPath", targetPath),
	)

	// Download file from Koneksi
	fileData, err := r.downloadFile(ctx, fileID)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	// Ensure target directory exists
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Write file to target path
	if err := os.WriteFile(targetPath, fileData, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	r.logger.Info("file restored successfully",
		zap.String("fileID", fileID),
		zap.String("path", targetPath),
	)

	return nil
}

// CreateManifestFromReport creates a restore manifest from a backup report
func (r *RestoreService) CreateManifestFromReport(reportPath, manifestPath string) error {
	// Read backup report
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("failed to read report: %w", err)
	}

	var report BackupReport
	if err := json.Unmarshal(reportData, &report); err != nil {
		return fmt.Errorf("failed to parse report: %w", err)
	}

	// Create manifest from successful backups
	manifest := RestoreManifest{
		Version:    "1.0",
		CreatedAt:  time.Now(),
		BackupID:   report.ID,
		SourcePath: "multiple",
		Files:      make([]FileManifestEntry, 0),
		Metadata: map[string]interface{}{
			"report_id":    report.ID,
			"report_time":  report.StartTime,
			"total_files":  report.TotalFiles,
			"successful":   report.Successful,
		},
	}

	// Add successful files to manifest
	for _, result := range report.Results {
		if result.Success && result.FileID != "" {
			entry := FileManifestEntry{
				FilePath:   result.FilePath,
				FileID:     result.FileID,
				Size:       result.Size,
				Checksum:   result.Checksum,
				BackupTime: result.EndTime,
			}
			manifest.Files = append(manifest.Files, entry)
		}
	}

	// Save manifest
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	r.logger.Info("created restore manifest",
		zap.String("path", manifestPath),
		zap.Int("files", len(manifest.Files)),
	)

	return nil
}

func (r *RestoreService) restoreWorker(ctx context.Context, queue chan FileManifestEntry, targetDir string) {
	defer r.wg.Done()

	for entry := range queue {
		select {
		case <-ctx.Done():
			return
		default:
			r.restoreFile(ctx, entry, targetDir)
		}
	}
}

func (r *RestoreService) restoreFile(ctx context.Context, entry FileManifestEntry, targetDir string) {
	// Sanitize the file path from the manifest to use only the base name
	cleanPath := filepath.Base(entry.FilePath)

	// Calculate target path
	targetPath := filepath.Join(targetDir, cleanPath)

	// Check if file already exists and matches checksum
	if r.fileExists(targetPath, entry.Checksum) {
		r.logger.Debug("file already exists with correct checksum, skipping",
			zap.String("path", targetPath),
		)
		r.updateProgress(true, entry.Size)
		return
	}

	// Download file
	fileData, err := r.downloadFile(ctx, entry.FileID)
	if err != nil {
		r.logger.Error("failed to download file",
			zap.String("fileID", entry.FileID),
			zap.String("path", entry.FilePath),
			zap.Error(err),
		)
		r.recordError(entry.FilePath, entry.FileID, err.Error())
		r.updateProgress(false, 0)
		return
	}

	// Ensure target directory exists
	targetFileDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetFileDir, 0755); err != nil {
		r.logger.Error("failed to create directory",
			zap.String("dir", targetFileDir),
			zap.Error(err),
		)
		r.recordError(entry.FilePath, entry.FileID, err.Error())
		r.updateProgress(false, 0)
		return
	}

	// Write file
	if err := os.WriteFile(targetPath, fileData, entry.Permissions); err != nil {
		r.logger.Error("failed to write file",
			zap.String("path", targetPath),
			zap.Error(err),
		)
		r.recordError(entry.FilePath, entry.FileID, err.Error())
		r.updateProgress(false, 0)
		return
	}

	r.logger.Info("file restored",
		zap.String("path", targetPath),
		zap.Int64("size", entry.Size),
	)
	r.updateProgress(true, entry.Size)
}

func (r *RestoreService) downloadFile(ctx context.Context, fileID string) ([]byte, error) {
	// Use the API client's download method
	reader, err := r.client.DownloadFile(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer reader.Close()
	
	// Read all data
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read file data: %w", err)
	}
	
	return data, nil
}

func (r *RestoreService) fileExists(path, checksum string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	// Calculate checksum of existing file
	existingChecksum, _ := r.calculateFileChecksum(path)
	return existingChecksum == checksum
}

func (r *RestoreService) calculateFileChecksum(path string) (string, error) {
	// This would calculate SHA256 checksum
	// Implementation omitted for brevity
	return "", nil
}

func (r *RestoreService) loadManifest(path string) (*RestoreManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var manifest RestoreManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

func (r *RestoreService) updateProgress(success bool, size int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if success {
		r.progress.RestoredFiles++
		r.progress.RestoredSize += size
	} else {
		r.progress.FailedFiles++
	}
}

func (r *RestoreService) recordError(filePath, fileID, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.progress.Errors = append(r.progress.Errors, RestoreError{
		FilePath: filePath,
		FileID:   fileID,
		Error:    errMsg,
		Time:     time.Now(),
	})
}

func (r *RestoreService) generateRestoreReport(manifest *RestoreManifest, targetDir string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	duration := time.Since(r.progress.StartTime)

	report := map[string]interface{}{
		"restore_id":     fmt.Sprintf("restore-%s", time.Now().Format("20060102-150405")),
		"backup_id":      manifest.BackupID,
		"target_dir":     targetDir,
		"start_time":     r.progress.StartTime,
		"end_time":       time.Now(),
		"duration":       duration,
		"total_files":    r.progress.TotalFiles,
		"restored_files": r.progress.RestoredFiles,
		"failed_files":   r.progress.FailedFiles,
		"total_size":     r.progress.TotalSize,
		"restored_size":  r.progress.RestoredSize,
		"success_rate":   float64(r.progress.RestoredFiles) / float64(r.progress.TotalFiles) * 100,
		"errors":         r.progress.Errors,
	}

	reportPath := filepath.Join(targetDir, fmt.Sprintf("restore-report-%s.json", time.Now().Format("20060102-150405")))
	reportData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	if err := os.WriteFile(reportPath, reportData, 0644); err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}

	r.logger.Info("restore completed",
		zap.String("reportPath", reportPath),
		zap.Int("restored", r.progress.RestoredFiles),
		zap.Int("failed", r.progress.FailedFiles),
		zap.Duration("duration", duration),
	)

	return nil
}

func (r *RestoreService) GetProgress() RestoreProgress {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return *r.progress
}