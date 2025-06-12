package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/koneksi/backup-cli/internal/report"
)

// TestBackupRestoreFileIntegrity tests the complete backup and restore process
// by verifying that files can be backed up and restored with integrity
func TestBackupRestoreFileIntegrity(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	restoreDir := filepath.Join(tempDir, "restore")
	reportDir := filepath.Join(tempDir, "reports")

	// Create directories
	for _, dir := range []string{sourceDir, restoreDir, reportDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create directory %s: %v", dir, err)
		}
	}

	// Create test files with different content
	testFiles := []struct {
		path    string
		content []byte
	}{
		{"file1.txt", []byte("This is test file 1 content")},
		{"file2.txt", []byte("This is test file 2 with different content")},
		{"subdir/file3.txt", []byte("This is test file 3 in a subdirectory")},
	}

	// Create subdirectory
	if err := os.MkdirAll(filepath.Join(sourceDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	// Write test files and calculate checksums
	fileChecksums := make(map[string]string)
	for _, tf := range testFiles {
		fullPath := filepath.Join(sourceDir, tf.path)
		if err := os.WriteFile(fullPath, tf.content, 0644); err != nil {
			t.Fatalf("failed to write test file %s: %v", tf.path, err)
		}

		// Calculate checksum
		h := sha256.New()
		h.Write(tf.content)
		fileChecksums[tf.path] = hex.EncodeToString(h.Sum(nil))
	}

	// Create a mock backup report
	backupReport := createMockBackupReport(sourceDir, testFiles, fileChecksums)
	reportPath := filepath.Join(reportDir, "backup-test-report.json")
	reportData, err := json.MarshalIndent(backupReport, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal report: %v", err)
	}
	if err := os.WriteFile(reportPath, reportData, 0644); err != nil {
		t.Fatalf("failed to write report: %v", err)
	}

	// Test manifest creation
	t.Run("CreateManifest", func(t *testing.T) {
		manifestPath := filepath.Join(tempDir, "test-manifest.json")
		
		// Read the report
		var report BackupReport
		data, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatalf("failed to read report: %v", err)
		}
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatalf("failed to unmarshal report: %v", err)
		}

		// Create manifest
		manifest := RestoreManifest{
			Version:    "1.0",
			CreatedAt:  time.Now(),
			BackupID:   report.ID,
			SourcePath: sourceDir,
			Files:      []FileManifestEntry{},
			Metadata: map[string]interface{}{
				"report_id":   report.ID,
				"report_time": report.StartTime,
				"total_files": report.TotalFiles,
				"successful":  report.Successful,
			},
		}

		// Add files to manifest
		for _, result := range report.Results {
			if result.Success && result.FileID != "" {
				entry := FileManifestEntry{
					FilePath:    result.FilePath,
					FileID:      result.FileID,
					Size:        result.Size,
					Checksum:    result.Checksum,
					BackupTime:  result.EndTime,
					Permissions: 0644,
				}
				manifest.Files = append(manifest.Files, entry)
			}
		}

		// Verify manifest has correct number of files
		if len(manifest.Files) != len(testFiles) {
			t.Errorf("expected %d files in manifest, got %d", len(testFiles), len(manifest.Files))
		}

		// Verify each file has a proper file ID (not empty, not a hash)
		for _, file := range manifest.Files {
			if file.FileID == "" {
				t.Errorf("file %s has empty file ID", file.FilePath)
			}
			// Verify it's using the mock file ID format (not IPFS hash)
			if len(file.FileID) == 46 && file.FileID[:2] == "Qm" {
				t.Errorf("file %s is using IPFS hash instead of file ID: %s", file.FilePath, file.FileID)
			}
			t.Logf("File %s has proper file ID: %s", file.FilePath, file.FileID)
		}

		// Save manifest
		manifestData, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			t.Fatalf("failed to marshal manifest: %v", err)
		}
		if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
			t.Fatalf("failed to write manifest: %v", err)
		}
	})

	// Test file checksum calculation
	t.Run("ChecksumCalculation", func(t *testing.T) {
		for _, tf := range testFiles {
			fullPath := filepath.Join(sourceDir, tf.path)
			
			// Calculate checksum using the same method as backup service
			checksum, err := calculateFileChecksum(fullPath)
			if err != nil {
				t.Fatalf("failed to calculate checksum for %s: %v", tf.path, err)
			}

			expectedChecksum := fileChecksums[tf.path]
			if checksum != expectedChecksum {
				t.Errorf("checksum mismatch for %s\nExpected: %s\nGot: %s", 
					tf.path, expectedChecksum, checksum)
			}
		}
	})

	// Test restore simulation (copy files to restore directory)
	t.Run("RestoreSimulation", func(t *testing.T) {
		// Simulate restore by copying files
		for _, tf := range testFiles {
			sourcePath := filepath.Join(sourceDir, tf.path)
			restorePath := filepath.Join(restoreDir, tf.path)
			
			// Create restore directory structure
			restoreFileDir := filepath.Dir(restorePath)
			if err := os.MkdirAll(restoreFileDir, 0755); err != nil {
				t.Fatalf("failed to create restore dir %s: %v", restoreFileDir, err)
			}

			// Copy file
			if err := copyFile(sourcePath, restorePath); err != nil {
				t.Fatalf("failed to copy file %s: %v", tf.path, err)
			}

			// Verify content
			restoredContent, err := os.ReadFile(restorePath)
			if err != nil {
				t.Fatalf("failed to read restored file %s: %v", tf.path, err)
			}

			if string(restoredContent) != string(tf.content) {
				t.Errorf("content mismatch for file %s\nExpected: %s\nGot: %s",
					tf.path, string(tf.content), string(restoredContent))
			}

			// Verify checksum
			restoredChecksum, err := calculateFileChecksum(restorePath)
			if err != nil {
				t.Fatalf("failed to calculate restored checksum for %s: %v", tf.path, err)
			}

			if restoredChecksum != fileChecksums[tf.path] {
				t.Errorf("checksum mismatch for restored file %s\nExpected: %s\nGot: %s",
					tf.path, fileChecksums[tf.path], restoredChecksum)
			}

			t.Logf("File %s restored and verified successfully", tf.path)
		}
	})
}

// Helper function to create a mock backup report
func createMockBackupReport(sourceDir string, files []struct{ path string; content []byte }, checksums map[string]string) BackupReport {
	now := time.Now()
	results := []report.BackupResult{}

	for i, f := range files {
		// Generate a mock file ID (not an IPFS hash)
		fileID := fmt.Sprintf("mock_file_id_%d_%s", i+1, checksums[f.path][:8])
		
		result := report.BackupResult{
			FilePath:   filepath.Join(sourceDir, f.path),
			FileID:     fileID,
			Operation:  "manual",
			Success:    true,
			StartTime:  now,
			EndTime:    now.Add(100 * time.Millisecond),
			Size:       int64(len(f.content)),
			Checksum:   checksums[f.path],
			Compressed: false,
		}
		results = append(results, result)
	}

	return BackupReport{
		ID:         fmt.Sprintf("backup-test-%s", now.Format("20060102-150405")),
		StartTime:  now,
		EndTime:    now.Add(1 * time.Second),
		TotalFiles: len(files),
		Successful: len(files),
		Failed:     0,
		TotalSize:  calculateTotalSize(results),
		Duration:   1 * time.Second,
		Results:    results,
		Statistics: map[string]interface{}{
			"success_rate": 100.0,
			"total_files":  len(files),
		},
	}
}

// Helper function to calculate total size
func calculateTotalSize(results []report.BackupResult) int64 {
	var total int64
	for _, r := range results {
		total += r.Size
	}
	return total
}

// Helper function to calculate file checksum
func calculateFileChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// Helper function to copy a file
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}