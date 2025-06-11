package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/koneksi/backup-cli/internal/api"
	"github.com/koneksi/backup-cli/internal/backup"
	"github.com/koneksi/backup-cli/internal/config"
	"github.com/koneksi/backup-cli/internal/monitor"
	"github.com/koneksi/backup-cli/internal/report"
	"github.com/koneksi/backup-cli/pkg/database"
)

var (
	configFile string
	logger     *zap.Logger
)

var rootCmd = &cobra.Command{
	Use:   "koneksi-backup",
	Short: "Koneksi Backup CLI - Automated directory backup with change detection",
	Long: `Koneksi Backup CLI is a tool that monitors directories for changes and 
automatically backs them up to Koneksi Secure Digital Storage Solution.`,
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the backup service in daemon mode",
	Long:  `Start the backup service that monitors directories and automatically backs up changes.`,
	RunE:  runBackupService,
}

var backupCmd = &cobra.Command{
	Use:   "backup [path]",
	Short: "Perform a one-time backup of a file or directory",
	Long:  `Backup a single file or entire directory immediately without starting the monitoring service.`,
	Args:  cobra.ExactArgs(1),
	RunE:  performBackup,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the status of the backup service",
	RunE:  showStatus,
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Show the latest backup report",
	RunE:  showReport,
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the configuration file",
	RunE:  initConfig,
}

var restoreCmd = &cobra.Command{
	Use:   "restore [manifest-file] [target-directory]",
	Short: "Restore files from a backup manifest",
	Long:  `Restore files from a backup using a manifest file that contains file IDs and metadata.`,
	Args:  cobra.ExactArgs(2),
	RunE:  restoreBackup,
}

var manifestCmd = &cobra.Command{
	Use:   "manifest [report-file] [output-file]",
	Short: "Create a restore manifest from a backup report",
	Long:  `Generate a restore manifest file from a backup report that can be used to restore files.`,
	Args:  cobra.ExactArgs(2),
	RunE:  createManifest,
}

func init() {
	cobra.OnInitialize(initializeLogger)

	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "config file (default is $HOME/.koneksi-backup/config.yaml)")
	
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(manifestCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func initializeLogger() {
	config := zap.NewProductionConfig()
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	
	var err error
	logger, err = config.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
}

func runBackupService(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Use credentials from config if not set
	if cfg.API.ClientID == "" {
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
	}
	if cfg.API.ClientSecret == "" {
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
	}
	
	logger.Debug("API credentials",
		zap.String("clientID", cfg.API.ClientID),
		zap.Bool("hasSecret", cfg.API.ClientSecret != ""),
	)

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Configure logger based on config
	if cfg.Log.Level != "" {
		level, err := zapcore.ParseLevel(cfg.Log.Level)
		if err == nil {
			logger = logger.WithOptions(zap.IncreaseLevel(level))
		}
	}

	logger.Info("starting Koneksi Backup Service",
		zap.String("version", "1.0.0"),
		zap.Int("directories", len(cfg.Backup.Directories)),
	)

	// Create API client
	apiClient := api.NewClient(
		cfg.API.BaseURL,
		cfg.API.ClientID,
		cfg.API.ClientSecret,
		cfg.API.DirectoryID,
		time.Duration(cfg.API.Timeout)*time.Second,
		cfg.API.RetryCount,
		logger,
	)

	// Test API connection
	ctx := context.Background()
	if err := apiClient.HealthCheck(ctx); err != nil {
		return fmt.Errorf("API health check failed: %w", err)
	}
	
	// Create backup directory if not specified
	if cfg.API.DirectoryID == "" {
		logger.Info("creating new backup directory")
		dirName := fmt.Sprintf("koneksi-backup-%s", time.Now().Format("20060102-150405"))
		dirResp, err := apiClient.CreateDirectory(ctx, dirName, "Automated backup directory created by Koneksi Backup CLI")
		if err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
		cfg.API.DirectoryID = dirResp.DirectoryID
		apiClient.DirectoryID = dirResp.DirectoryID
		logger.Info("created backup directory", zap.String("directoryID", dirResp.DirectoryID), zap.String("name", dirResp.Name))
	}

	// Create database
	db, err := database.New(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}
	defer db.Close()

	// Create reporter
	reporter, err := report.NewReporter(
		logger,
		cfg.Report.Directory,
		cfg.Report.Format,
		cfg.Report.Retention,
	)
	if err != nil {
		return fmt.Errorf("failed to create reporter: %w", err)
	}

	// Start new report
	reporter.StartNewReport()

	// Create file watcher
	watcher, err := monitor.NewWatcher(logger, cfg.Backup.ExcludePatterns)
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	// Create backup service
	backupService, err := backup.NewService(
		apiClient,
		logger,
		reporter,
		cfg,
		db,
	)
	if err != nil {
		return fmt.Errorf("failed to create backup service: %w", err)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start services
	watcher.Start(ctx)
	backupService.Start(ctx)
	
	// Start database cleanup routine
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := db.CleanupOldRecords(cfg.Database.Retention); err != nil {
					logger.Error("failed to cleanup old database records", zap.Error(err))
				}
			}
		}
	}()

	// Add directories to watch
	for _, dir := range cfg.Backup.Directories {
		absPath, err := filepath.Abs(dir)
		if err != nil {
			logger.Error("failed to resolve directory path", zap.String("dir", dir), zap.Error(err))
			continue
		}

		if err := watcher.AddDirectory(absPath); err != nil {
			logger.Error("failed to add directory to watcher", zap.String("dir", absPath), zap.Error(err))
			continue
		}

		logger.Info("watching directory", zap.String("path", absPath))
	}

	// Main event loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case change := <-watcher.Changes():
				backupService.ProcessChange(change)
			case err := <-watcher.Errors():
				logger.Error("watcher error", zap.Error(err))
			}
		}
	}()

	// Periodic status logging
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	logger.Info("backup service started, monitoring directories...")

	// Wait for shutdown signal
	select {
	case <-sigChan:
		logger.Info("shutdown signal received")
	case <-ctx.Done():
		logger.Info("context cancelled")
	}

	// Graceful shutdown
	logger.Info("shutting down backup service...")
	
	// Stop services
	cancel()
	backupService.Stop()

	// Finish report
	stats := backupService.GetBackupStats()
	if err := reporter.FinishReport(stats); err != nil {
		logger.Error("failed to finish report", zap.Error(err))
	}

	// Print final summary
	fmt.Println(reporter.GenerateSummary())

	logger.Info("backup service stopped")
	return nil
}

func performBackup(cmd *cobra.Command, args []string) error {
	targetPath := args[0]
	
	// Load configuration
	cfg, err := config.Load(configFile)
	if err != nil {
		// Create minimal config for one-time backup
		cfg = &config.Config{}
		cfg.API.BaseURL = "https://koneksi-tyk-gateway-3rvca.ondigitalocean.app"
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
		cfg.API.Timeout = 30
		cfg.API.RetryCount = 3
		cfg.Backup.MaxFileSize = 1073741824 // 1GB
		cfg.Backup.Concurrent = 5
		cfg.Backup.Compression.Enabled = false
		cfg.Report.Directory = "./reports"
		cfg.Report.Format = "json"
		cfg.Database.Path = "./backup.db"
		cfg.Log.Level = "debug"
	}

	// Use credentials from environment if not set
	if cfg.API.ClientID == "" {
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
	}
	if cfg.API.ClientSecret == "" {
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
	}
	
	fmt.Printf("DEBUG: ClientID = %s, HasSecret = %v\n", cfg.API.ClientID, cfg.API.ClientSecret != "")

	// Configure logger
	if logger == nil {
		initializeLogger()
	}
	
	// Configure logger based on config
	if cfg.Log.Level != "" {
		level, err := zapcore.ParseLevel(cfg.Log.Level)
		if err == nil {
			logger = logger.WithOptions(zap.IncreaseLevel(level))
		}
	}

	// Create API client
	apiClient := api.NewClient(
		cfg.API.BaseURL,
		cfg.API.ClientID,
		cfg.API.ClientSecret,
		cfg.API.DirectoryID,
		time.Duration(cfg.API.Timeout)*time.Second,
		cfg.API.RetryCount,
		logger,
	)

	// Test API connection
	ctx := context.Background()
	if err := apiClient.HealthCheck(ctx); err != nil {
		return fmt.Errorf("API health check failed: %w", err)
	}
	
	// Create backup directory if not specified
	if cfg.API.DirectoryID == "" {
		logger.Info("creating new backup directory")
		dirName := fmt.Sprintf("koneksi-backup-%s", time.Now().Format("20060102-150405"))
		dirResp, err := apiClient.CreateDirectory(ctx, dirName, "One-time backup directory created by Koneksi Backup CLI")
		if err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
		cfg.API.DirectoryID = dirResp.DirectoryID
		apiClient.DirectoryID = dirResp.DirectoryID
		logger.Info("created backup directory", zap.String("directoryID", dirResp.DirectoryID), zap.String("name", dirResp.Name))
	}

	// Create database
	db, err := database.New(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}
	defer db.Close()

	// Create reporter
	reporter, err := report.NewReporter(
		logger,
		cfg.Report.Directory,
		cfg.Report.Format,
		cfg.Report.Retention,
	)
	if err != nil {
		return fmt.Errorf("failed to create reporter: %w", err)
	}

	// Start new report
	reporter.StartNewReport()

	// Create backup service
	backupService, err := backup.NewService(
		apiClient,
		logger,
		reporter,
		cfg,
		db,
	)
	if err != nil {
		return fmt.Errorf("failed to create backup service: %w", err)
	}

	// Check if path exists
	info, err := os.Stat(targetPath)
	if err != nil {
		return fmt.Errorf("failed to access path %s: %w", targetPath, err)
	}

	// Start the backup service
	ctx = context.Background()
	backupService.Start(ctx)

	// Perform backup
	fmt.Printf("Starting backup of: %s\n", targetPath)
	
	if info.IsDir() {
		// Backup directory
		err = backupDirectory(ctx, backupService, targetPath, cfg.Backup.ExcludePatterns)
	} else {
		// Backup single file
		err = backupSingleFile(ctx, backupService, targetPath, info)
	}

	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Stop the service and wait for completion
	backupService.Stop()

	// Finish report
	stats := backupService.GetBackupStats()
	if err := reporter.FinishReport(stats); err != nil {
		logger.Error("failed to finish report", zap.Error(err))
	}

	// Print summary
	fmt.Println(reporter.GenerateSummary())
	
	return nil
}

func backupSingleFile(ctx context.Context, service *backup.Service, filePath string, info os.FileInfo) error {
	fmt.Printf("Backing up file: %s (size: %d bytes)\n", filePath, info.Size())
	
	change := monitor.FileChange{
		Path:      filePath,
		Operation: "manual",
		Timestamp: time.Now(),
		Size:      info.Size(),
		IsDir:     false,
	}
	
	service.ProcessChange(change)
	
	// Wait for processing to complete
	time.Sleep(5 * time.Second)
	
	return nil
}

func backupDirectory(ctx context.Context, service *backup.Service, dirPath string, excludePatterns []string) error {
	fileCount := 0
	
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			logger.Warn("error accessing path", zap.String("path", path), zap.Error(err))
			return nil
		}
		
		// Skip directories
		if info.IsDir() {
			return nil
		}
		
		// Check exclude patterns
		for _, pattern := range excludePatterns {
			matched, err := filepath.Match(pattern, filepath.Base(path))
			if err == nil && matched {
				fmt.Printf("Skipping excluded file: %s\n", path)
				return nil
			}
		}
		
		// Process file
		change := monitor.FileChange{
			Path:      path,
			Operation: "manual",
			Timestamp: time.Now(),
			Size:      info.Size(),
			IsDir:     false,
		}
		
		service.ProcessChange(change)
		fileCount++
		
		if fileCount%10 == 0 {
			fmt.Printf("Processed %d files...\n", fileCount)
		}
		
		return nil
	})
	
	if err != nil {
		return err
	}
	
	fmt.Printf("Queued %d files for backup\n", fileCount)
	
	// Wait for processing to complete
	waitTime := time.Duration(fileCount/10+5) * time.Second
	fmt.Printf("Waiting %v for backups to complete...\n", waitTime)
	time.Sleep(waitTime)
	
	return nil
}

func showStatus(cmd *cobra.Command, args []string) error {
	// Load latest report
	cfg, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	reporter, err := report.NewReporter(
		logger,
		cfg.Report.Directory,
		cfg.Report.Format,
		cfg.Report.Retention,
	)
	if err != nil {
		return fmt.Errorf("failed to create reporter: %w", err)
	}

	report, err := reporter.GetLatestReport()
	if err != nil {
		return fmt.Errorf("failed to get latest report: %w", err)
	}

	fmt.Printf("Latest Backup Report\n")
	fmt.Printf("===================\n")
	fmt.Printf("Report ID: %s\n", report.ID)
	fmt.Printf("Start Time: %s\n", report.StartTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("End Time: %s\n", report.EndTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("Duration: %s\n", report.Duration)
	fmt.Printf("Total Files: %d\n", report.TotalFiles)
	fmt.Printf("Successful: %d\n", report.Successful)
	fmt.Printf("Failed: %d\n", report.Failed)
	fmt.Printf("Total Size: %d bytes\n", report.TotalSize)

	return nil
}

func showReport(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	reporter, err := report.NewReporter(
		logger,
		cfg.Report.Directory,
		cfg.Report.Format,
		cfg.Report.Retention,
	)
	if err != nil {
		return fmt.Errorf("failed to create reporter: %w", err)
	}

	summary := reporter.GenerateSummary()
	fmt.Println(summary)

	return nil
}

func initConfig(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configDir := filepath.Join(home, ".koneksi-backup")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	
	defaultConfig := `# Koneksi Backup Configuration
api:
  base_url: "https://koneksi-tyk-gateway-3rvca.ondigitalocean.app"
  client_id: ""  # Set your client ID here or use KONEKSI_API_CLIENT_ID env var
  client_secret: ""  # Set your client secret here or use KONEKSI_API_CLIENT_SECRET env var
  directory_id: ""  # Leave empty to create a new directory on startup
  timeout: 30
  retry_count: 3

backup:
  directories:
    - "/path/to/backup/directory1"
    - "/path/to/backup/directory2"
  exclude_patterns:
    - "*.tmp"
    - "*.log"
    - ".git"
    - "node_modules"
    - "__pycache__"
  check_interval: 300  # seconds
  max_file_size: 1073741824  # 1GB in bytes
  concurrent: 5
  compression:
    enabled: false  # Enable compression for backups
    level: 6       # Compression level (1-9)
    format: "gzip" # Compression format (gzip or zlib)

report:
  directory: "./reports"
  format: "json"
  retention: 30  # days

log:
  level: "info"
  file: ""
  format: "json"

database:
  path: "./backup.db"
  retention: 90  # days
`

	if err := os.WriteFile(configPath, []byte(defaultConfig), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Printf("Configuration file created at: %s\n", configPath)
	fmt.Println("Please edit this file to add your backup directories.")
	
	return nil
}

func restoreBackup(cmd *cobra.Command, args []string) error {
	manifestFile := args[0]
	targetDir := args[1]

	// Load configuration
	cfg, err := config.Load(configFile)
	if err != nil {
		// Create minimal config for restore
		cfg = &config.Config{}
		cfg.API.BaseURL = "https://koneksi-tyk-gateway-3rvca.ondigitalocean.app"
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
		cfg.API.DirectoryID = "6839deb70fe80fe0747654b2"
		cfg.API.Timeout = 30
		cfg.API.RetryCount = 3
		cfg.Backup.Concurrent = 5
	}
	
	// Use credentials from environment if not set
	if cfg.API.ClientID == "" {
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
	}
	if cfg.API.ClientSecret == "" {
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
	}

	// Create API client
	apiClient := api.NewClient(
		cfg.API.BaseURL,
		cfg.API.ClientID,
		cfg.API.ClientSecret,
		cfg.API.DirectoryID,
		time.Duration(cfg.API.Timeout)*time.Second,
		cfg.API.RetryCount,
		logger,
	)

	// Test API connection
	ctx := context.Background()
	if err := apiClient.HealthCheck(ctx); err != nil {
		return fmt.Errorf("API health check failed: %w", err)
	}

	// Create restore service
	restoreService := backup.NewRestoreService(apiClient, logger, cfg.Backup.Concurrent)

	fmt.Printf("Starting restore from manifest: %s\n", manifestFile)
	fmt.Printf("Target directory: %s\n", targetDir)

	// Perform restore
	if err := restoreService.RestoreFromManifest(ctx, manifestFile, targetDir); err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}

	// Get final progress
	progress := restoreService.GetProgress()
	fmt.Printf("\nRestore completed:\n")
	fmt.Printf("- Total files: %d\n", progress.TotalFiles)
	fmt.Printf("- Restored: %d\n", progress.RestoredFiles)
	fmt.Printf("- Failed: %d\n", progress.FailedFiles)
	fmt.Printf("- Duration: %s\n", time.Since(progress.StartTime))

	if len(progress.Errors) > 0 {
		fmt.Printf("\nErrors:\n")
		for _, err := range progress.Errors {
			fmt.Printf("- %s: %s\n", err.FilePath, err.Error)
		}
	}

	return nil
}

func createManifest(cmd *cobra.Command, args []string) error {
	reportFile := args[0]
	outputFile := args[1]

	// Load configuration for API client
	cfg, err := config.Load(configFile)
	if err != nil {
		cfg = &config.Config{}
		cfg.API.BaseURL = "https://koneksi-tyk-gateway-3rvca.ondigitalocean.app"
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
		cfg.API.DirectoryID = "6839deb70fe80fe0747654b2"
		cfg.API.Timeout = 30
		cfg.API.RetryCount = 3
	}

	// Create API client
	apiClient := api.NewClient(
		cfg.API.BaseURL,
		cfg.API.ClientID,
		cfg.API.ClientSecret,
		cfg.API.DirectoryID,
		time.Duration(cfg.API.Timeout)*time.Second,
		cfg.API.RetryCount,
		logger,
	)

	// Create restore service
	restoreService := backup.NewRestoreService(apiClient, logger, 1)

	fmt.Printf("Creating manifest from report: %s\n", reportFile)

	// Create manifest
	if err := restoreService.CreateManifestFromReport(reportFile, outputFile); err != nil {
		return fmt.Errorf("failed to create manifest: %w", err)
	}

	fmt.Printf("Manifest created successfully: %s\n", outputFile)
	fmt.Println("\nYou can use this manifest to restore files with:")
	fmt.Printf("  koneksi-backup restore %s /path/to/restore\n", outputFile)

	return nil
}