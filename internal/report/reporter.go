package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

type Reporter struct {
	logger        *zap.Logger
	reportDir     string
	format        string
	retention     int
	mu            sync.RWMutex
	currentReport *BackupReport
	results       []BackupResult
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
	Results     []BackupResult         `json:"results"`
	Statistics  map[string]interface{} `json:"statistics"`
}

type BackupResult struct {
	FilePath       string        `json:"file_path"`
	FileID         string        `json:"file_id,omitempty"`
	Operation      string        `json:"operation"`
	Success        bool          `json:"success"`
	Error          error         `json:"error,omitempty"`
	ErrorMsg       string        `json:"error_message,omitempty"`
	StartTime      time.Time     `json:"start_time"`
	EndTime        time.Time     `json:"end_time"`
	Duration       time.Duration `json:"duration"`
	Size           int64         `json:"size"`
	CompressedSize int64         `json:"compressed_size,omitempty"`
	Checksum       string        `json:"checksum,omitempty"`
	Compressed     bool          `json:"compressed"`
}

func NewReporter(logger *zap.Logger, reportDir, format string, retention int) (*Reporter, error) {
	// Create report directory if it doesn't exist
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create report directory: %w", err)
	}

	return &Reporter{
		logger:    logger,
		reportDir: reportDir,
		format:    format,
		retention: retention,
		results:   make([]BackupResult, 0),
	}, nil
}

func (r *Reporter) StartNewReport() {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Save previous report if exists
	if r.currentReport != nil {
		r.saveReport()
	}

	r.currentReport = &BackupReport{
		ID:         fmt.Sprintf("backup-%s", time.Now().Format("20060102-150405")),
		StartTime:  time.Now(),
		Results:    make([]BackupResult, 0),
		Statistics: make(map[string]interface{}),
	}
	r.results = make([]BackupResult, 0)

	r.logger.Info("started new backup report", zap.String("reportID", r.currentReport.ID))
}

func (r *Reporter) AddResult(result BackupResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Ensure we have a current report
	if r.currentReport == nil {
		r.currentReport = &BackupReport{
			ID:         fmt.Sprintf("backup-%s", time.Now().Format("20060102-150405")),
			StartTime:  time.Now(),
			Results:    make([]BackupResult, 0),
			Statistics: make(map[string]interface{}),
		}
	}

	// Calculate duration
	result.Duration = result.EndTime.Sub(result.StartTime)

	// Convert error to string for JSON serialization
	if result.Error != nil {
		result.ErrorMsg = result.Error.Error()
		result.Error = nil
	}

	r.results = append(r.results, result)

	// Update statistics
	r.currentReport.TotalFiles++
	if result.Success {
		r.currentReport.Successful++
		r.currentReport.TotalSize += result.Size
	} else {
		r.currentReport.Failed++
	}

	// Auto-save every 100 results
	if len(r.results) >= 100 {
		r.saveReport()
	}
}

func (r *Reporter) FinishReport(stats map[string]interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentReport == nil {
		return fmt.Errorf("no active report to finish")
	}

	r.currentReport.EndTime = time.Now()
	r.currentReport.Duration = r.currentReport.EndTime.Sub(r.currentReport.StartTime)
	r.currentReport.Results = r.results
	r.currentReport.Statistics = stats

	// Add additional statistics
	if r.currentReport.TotalFiles > 0 {
		r.currentReport.Statistics["success_rate"] = float64(r.currentReport.Successful) / float64(r.currentReport.TotalFiles) * 100
		if r.currentReport.Successful > 0 {
			r.currentReport.Statistics["average_size"] = r.currentReport.TotalSize / int64(r.currentReport.Successful)
		} else {
			r.currentReport.Statistics["average_size"] = 0
		}
		r.currentReport.Statistics["files_per_second"] = float64(r.currentReport.TotalFiles) / r.currentReport.Duration.Seconds()
	}

	return r.saveReport()
}

func (r *Reporter) saveReport() error {
	if r.currentReport == nil {
		return nil
	}

	filename := fmt.Sprintf("%s-%s.%s", 
		r.currentReport.ID,
		r.currentReport.StartTime.Format("20060102-150405"),
		r.format,
	)
	filepath := filepath.Join(r.reportDir, filename)

	var data []byte
	var err error

	switch r.format {
	case "json":
		data, err = json.MarshalIndent(r.currentReport, "", "  ")
	default:
		return fmt.Errorf("unsupported report format: %s", r.format)
	}

	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	if err := os.WriteFile(filepath, data, 0644); err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}

	r.logger.Info("saved backup report",
		zap.String("file", filepath),
		zap.Int("totalFiles", r.currentReport.TotalFiles),
		zap.Int("successful", r.currentReport.Successful),
		zap.Int("failed", r.currentReport.Failed),
	)

	// Clean up old reports
	go r.cleanupOldReports()

	return nil
}

func (r *Reporter) cleanupOldReports() {
	files, err := os.ReadDir(r.reportDir)
	if err != nil {
		r.logger.Error("failed to read report directory", zap.Error(err))
		return
	}

	// Get all report files
	var reports []os.DirEntry
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == "."+r.format {
			reports = append(reports, file)
		}
	}

	// Skip if within retention limit
	if len(reports) <= r.retention {
		return
	}

	// Remove oldest reports
	toRemove := len(reports) - r.retention
	for i := 0; i < toRemove; i++ {
		path := filepath.Join(r.reportDir, reports[i].Name())
		if err := os.Remove(path); err != nil {
			r.logger.Error("failed to remove old report", zap.String("file", path), zap.Error(err))
		} else {
			r.logger.Info("removed old report", zap.String("file", path))
		}
	}
}

func (r *Reporter) GetLatestReport() (*BackupReport, error) {
	files, err := os.ReadDir(r.reportDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read report directory: %w", err)
	}

	var latestFile string
	var latestTime time.Time

	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == "."+r.format {
			info, err := file.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(latestTime) {
				latestTime = info.ModTime()
				latestFile = file.Name()
			}
		}
	}

	if latestFile == "" {
		return nil, fmt.Errorf("no reports found")
	}

	data, err := os.ReadFile(filepath.Join(r.reportDir, latestFile))
	if err != nil {
		return nil, fmt.Errorf("failed to read report: %w", err)
	}

	var report BackupReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("failed to parse report: %w", err)
	}

	return &report, nil
}

func (r *Reporter) GenerateSummary() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.currentReport == nil {
		return "No active backup report"
	}

	summary := fmt.Sprintf(`
Backup Report Summary
====================
Report ID: %s
Start Time: %s
Total Files: %d
Successful: %d
Failed: %d
Total Size: %s
Success Rate: %.2f%%
`,
		r.currentReport.ID,
		r.currentReport.StartTime.Format("2006-01-02 15:04:05"),
		r.currentReport.TotalFiles,
		r.currentReport.Successful,
		r.currentReport.Failed,
		formatSize(r.currentReport.TotalSize),
		float64(r.currentReport.Successful)/float64(r.currentReport.TotalFiles)*100,
	)

	if r.currentReport.Failed > 0 {
		summary += "\nFailed Files:\n"
		for _, result := range r.results {
			if !result.Success {
				summary += fmt.Sprintf("- %s: %s\n", result.FilePath, result.ErrorMsg)
			}
		}
	}

	return summary
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}