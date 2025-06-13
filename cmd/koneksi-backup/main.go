package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/koneksi/backup-cli/internal/api"
	"github.com/koneksi/backup-cli/internal/auth"
	"github.com/koneksi/backup-cli/internal/backup"
	"github.com/koneksi/backup-cli/internal/config"
	"github.com/koneksi/backup-cli/internal/monitor"
	"github.com/koneksi/backup-cli/internal/report"
	"github.com/koneksi/backup-cli/pkg/archive"
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

var (
	compressDir bool
	autoExtract bool
)

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

// Directory management commands
var dirCmd = &cobra.Command{
	Use:   "dir",
	Short: "Manage backup directories",
	Long:  `Create, list, update, and remove backup directories in Koneksi storage.`,
}

var dirListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all directories",
	Long:  `List all backup directories in your Koneksi account.`,
	RunE:  listDirectories,
}

var dirCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new directory",
	Long:  `Create a new backup directory with the specified name.`,
	Args:  cobra.ExactArgs(1),
	RunE:  createDirectory,
}

var dirRemoveCmd = &cobra.Command{
	Use:   "remove [directory-id]",
	Short: "Remove a directory",
	Long:  `Remove a backup directory and all its contents.`,
	Args:  cobra.ExactArgs(1),
	RunE:  removeDirectory,
}

var (
	dirDescription string
	dirForceRemove bool
)

// Auth management commands
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication and API keys",
	Long:  `Register, login, and manage API keys for Koneksi storage.`,
}

var authRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a new user account",
	Long:  `Register a new user account with Koneksi storage service.`,
	RunE:  authRegister,
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to get access token",
	Long:  `Login with email and password to receive an access token.`,
	RunE:  authLogin,
}

var authCreateKeyCmd = &cobra.Command{
	Use:   "create-key [name]",
	Short: "Create a new API key",
	Long:  `Create a new API key (service account) for programmatic access.`,
	Args:  cobra.ExactArgs(1),
	RunE:  authCreateKey,
}

var authRevokeKeyCmd = &cobra.Command{
	Use:   "revoke-key [client-id]",
	Short: "Revoke an API key",
	Long:  `Revoke an existing API key by its client ID.`,
	Args:  cobra.ExactArgs(1),
	RunE:  authRevokeKey,
}

var authVerifyCmd = &cobra.Command{
	Use:   "verify [verification-code]",
	Short: "Verify your account",
	Long:  `Verify your account using the verification code sent to your email after registration.`,
	Args:  cobra.ExactArgs(1),
	RunE:  authVerify,
}

var (
	// Registration flags
	firstName  string
	middleName string
	lastName   string
	suffix     string
	email      string
	password   string

	// Auth token for API key operations
	authToken string

	// Base URL for auth operations
	authBaseURL string = "https://staging.koneksi.co.kr"
)

func init() {
	cobra.OnInitialize(initializeLogger)

	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "config file (default is $HOME/.koneksi-backup/config.yaml)")

	// Add flags for backup command
	backupCmd.Flags().BoolVar(&compressDir, "compress-dir", false, "compress directory into a single tar.gz file before backup")

	// Add flags for restore command
	restoreCmd.Flags().BoolVar(&autoExtract, "auto-extract", false, "automatically extract tar.gz files after restore")

	// Add flags for directory commands
	dirCreateCmd.Flags().StringVarP(&dirDescription, "description", "d", "", "Directory description")
	dirRemoveCmd.Flags().BoolVarP(&dirForceRemove, "force", "f", false, "Force remove without confirmation")

	// Add directory subcommands
	dirCmd.AddCommand(dirListCmd)
	dirCmd.AddCommand(dirCreateCmd)
	dirCmd.AddCommand(dirRemoveCmd)

	// Add flags for auth commands
	authRegisterCmd.Flags().StringVar(&firstName, "first-name", "", "First name (required)")
	authRegisterCmd.Flags().StringVar(&lastName, "last-name", "", "Last name (required)")
	authRegisterCmd.Flags().StringVar(&middleName, "middle-name", "", "Middle name")
	authRegisterCmd.Flags().StringVar(&suffix, "suffix", "", "Suffix")
	authRegisterCmd.Flags().StringVarP(&email, "email", "e", "", "Email address (required)")
	authRegisterCmd.Flags().StringVarP(&password, "password", "p", "", "Password (required)")
	authRegisterCmd.MarkFlagRequired("first-name")
	authRegisterCmd.MarkFlagRequired("last-name")
	authRegisterCmd.MarkFlagRequired("email")
	authRegisterCmd.MarkFlagRequired("password")

	authLoginCmd.Flags().StringVarP(&email, "email", "e", "", "Email address (required)")
	authLoginCmd.Flags().StringVarP(&password, "password", "p", "", "Password (required)")
	authLoginCmd.MarkFlagRequired("email")
	authLoginCmd.MarkFlagRequired("password")

	authCreateKeyCmd.Flags().StringVarP(&authToken, "token", "t", "", "Bearer token from login")
	authRevokeKeyCmd.Flags().StringVarP(&authToken, "token", "t", "", "Bearer token from login")
	authVerifyCmd.Flags().StringVarP(&authToken, "token", "t", "", "Bearer token from login (required)")
	authVerifyCmd.MarkFlagRequired("token")

	// Add auth subcommands
	authCmd.AddCommand(authRegisterCmd)
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authCreateKeyCmd)
	authCmd.AddCommand(authRevokeKeyCmd)
	authCmd.AddCommand(authVerifyCmd)

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(manifestCmd)
	rootCmd.AddCommand(dirCmd)
	rootCmd.AddCommand(authCmd)
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
		fmt.Printf("DEBUG: Failed to load config: %v\n", err)
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

	fmt.Printf("DEBUG: ClientID = %s, HasSecret = %v, DirectoryID = %s\n", cfg.API.ClientID, cfg.API.ClientSecret != "", cfg.API.DirectoryID)

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

	if info.IsDir() && compressDir {
		// Compress directory and backup as single file
		fmt.Println("Compressing directory before backup...")
		archivePath, err := archive.CreateTempArchive(targetPath)
		if err != nil {
			return fmt.Errorf("failed to compress directory: %w", err)
		}
		defer os.Remove(archivePath) // Clean up temp file

		// Get archive info
		archiveInfo, err := os.Stat(archivePath)
		if err != nil {
			return fmt.Errorf("failed to stat archive: %w", err)
		}

		fmt.Printf("Directory compressed to %s (size: %d bytes)\n", archivePath, archiveInfo.Size())

		// Backup the archive as a single file
		err = backupSingleFile(ctx, backupService, archivePath, archiveInfo)
	} else if info.IsDir() {
		// Backup directory normally
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

	// Auto-extract tar.gz files if flag is set
	if autoExtract {
		fmt.Println("\nChecking for tar.gz files to extract...")
		extractCount := 0

		err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}

			if !info.IsDir() && filepath.Ext(path) == ".gz" {
				// Check if it's a tar.gz file
				if len(path) > 7 && path[len(path)-7:] == ".tar.gz" {
					fmt.Printf("Extracting %s...\n", path)

					// Extract to the same directory
					extractDir := filepath.Dir(path)
					if err := archive.DecompressArchive(path, extractDir); err != nil {
						fmt.Printf("Failed to extract %s: %v\n", path, err)
					} else {
						extractCount++
						// Remove the archive after successful extraction
						os.Remove(path)
						fmt.Printf("Extracted and removed %s\n", path)
					}
				}
			}
			return nil
		})

		if err != nil {
			fmt.Printf("Warning: error during extraction walk: %v\n", err)
		}

		if extractCount > 0 {
			fmt.Printf("\nExtracted %d archive(s)\n", extractCount)
		} else {
			fmt.Println("No tar.gz files found to extract")
		}
	}

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

// Directory management functions
func listDirectories(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load(configFile)
	if err != nil {
		// Create minimal config
		cfg = &config.Config{}
		cfg.API.BaseURL = "https://koneksi-tyk-gateway-3rvca.ondigitalocean.app"
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
		cfg.API.Timeout = 30
		cfg.API.RetryCount = 3
	}

	// Use credentials from environment if not set
	if cfg.API.ClientID == "" {
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
	}
	if cfg.API.ClientSecret == "" {
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
	}

	if cfg.API.ClientID == "" || cfg.API.ClientSecret == "" {
		return fmt.Errorf("API credentials not set. Please set KONEKSI_API_CLIENT_ID and KONEKSI_API_CLIENT_SECRET environment variables")
	}

	// Initialize logger if needed
	if logger == nil {
		initializeLogger()
	}

	// Create API client
	apiClient := api.NewClient(
		cfg.API.BaseURL,
		cfg.API.ClientID,
		cfg.API.ClientSecret,
		"", // No default directory for directory management
		time.Duration(cfg.API.Timeout)*time.Second,
		cfg.API.RetryCount,
		logger,
	)

	ctx := context.Background()

	// Test API connection
	if err := apiClient.HealthCheck(ctx); err != nil {
		return fmt.Errorf("API health check failed: %w", err)
	}

	// List directories
	directories, err := apiClient.ListDirectories(ctx)
	if err != nil {
		return fmt.Errorf("failed to list directories: %w", err)
	}

	if len(directories) == 0 {
		fmt.Println("No directories found.")
		return nil
	}

	// Print directories
	fmt.Printf("%-30s %-30s %-20s %-10s %-10s\n", "ID", "Name", "Created", "Files", "Size")
	fmt.Printf("%-30s %-30s %-20s %-10s %-10s\n", strings.Repeat("-", 30), strings.Repeat("-", 30), strings.Repeat("-", 20), strings.Repeat("-", 10), strings.Repeat("-", 10))

	for _, dir := range directories {
		createdStr := dir.CreatedAt.Format("2006-01-02 15:04")
		sizeStr := formatBytes(dir.TotalSize)
		fmt.Printf("%-30s %-30s %-20s %-10d %-10s\n",
			dir.ID, dir.Name, createdStr, dir.FileCount, sizeStr)
	}

	return nil
}

func createDirectory(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Load configuration
	cfg, err := config.Load(configFile)
	if err != nil {
		// Create minimal config
		cfg = &config.Config{}
		cfg.API.BaseURL = "https://koneksi-tyk-gateway-3rvca.ondigitalocean.app"
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
		cfg.API.Timeout = 30
		cfg.API.RetryCount = 3
	}

	// Use credentials from environment if not set
	if cfg.API.ClientID == "" {
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
	}
	if cfg.API.ClientSecret == "" {
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
	}

	// Initialize logger if needed
	if logger == nil {
		initializeLogger()
	}

	// Create API client
	apiClient := api.NewClient(
		cfg.API.BaseURL,
		cfg.API.ClientID,
		cfg.API.ClientSecret,
		"",
		time.Duration(cfg.API.Timeout)*time.Second,
		cfg.API.RetryCount,
		logger,
	)

	ctx := context.Background()

	// Test API connection
	if err := apiClient.HealthCheck(ctx); err != nil {
		return fmt.Errorf("API health check failed: %w", err)
	}

	// Create directory
	fmt.Printf("Creating directory: %s\n", name)

	if dirDescription == "" {
		dirDescription = fmt.Sprintf("Backup directory created by Koneksi CLI at %s", time.Now().Format("2006-01-02 15:04:05"))
	}

	dirResp, err := apiClient.CreateDirectory(ctx, name, dirDescription)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	fmt.Printf("Directory created successfully!\n")
	fmt.Printf("ID: %s\n", dirResp.DirectoryID)
	fmt.Printf("Name: %s\n", dirResp.Name)
	fmt.Printf("Description: %s\n", dirResp.Description)

	return nil
}

func removeDirectory(cmd *cobra.Command, args []string) error {
	dirID := args[0]

	// Load configuration
	cfg, err := config.Load(configFile)
	if err != nil {
		// Create minimal config
		cfg = &config.Config{}
		cfg.API.BaseURL = "https://koneksi-tyk-gateway-3rvca.ondigitalocean.app"
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
		cfg.API.Timeout = 30
		cfg.API.RetryCount = 3
	}

	// Use credentials from environment if not set
	if cfg.API.ClientID == "" {
		cfg.API.ClientID = os.Getenv("KONEKSI_API_CLIENT_ID")
	}
	if cfg.API.ClientSecret == "" {
		cfg.API.ClientSecret = os.Getenv("KONEKSI_API_CLIENT_SECRET")
	}

	// Initialize logger if needed
	if logger == nil {
		initializeLogger()
	}

	// Create API client
	apiClient := api.NewClient(
		cfg.API.BaseURL,
		cfg.API.ClientID,
		cfg.API.ClientSecret,
		"",
		time.Duration(cfg.API.Timeout)*time.Second,
		cfg.API.RetryCount,
		logger,
	)

	ctx := context.Background()

	// Test API connection
	if err := apiClient.HealthCheck(ctx); err != nil {
		return fmt.Errorf("API health check failed: %w", err)
	}

	// Get directory info first
	directories, err := apiClient.ListDirectories(ctx)
	if err != nil {
		return fmt.Errorf("failed to list directories: %w", err)
	}

	var targetDir *api.DirectoryInfo
	for _, dir := range directories {
		if dir.ID == dirID {
			targetDir = &dir
			break
		}
	}

	if targetDir == nil {
		return fmt.Errorf("directory not found: %s", dirID)
	}

	// Confirm removal if not forced
	if !dirForceRemove {
		fmt.Printf("Are you sure you want to remove directory '%s' (ID: %s)?\n", targetDir.Name, dirID)
		fmt.Printf("This directory contains %d files totaling %s.\n", targetDir.FileCount, formatBytes(targetDir.TotalSize))
		fmt.Print("This action cannot be undone. Type 'yes' to confirm: ")

		var response string
		fmt.Scanln(&response)
		if response != "yes" {
			fmt.Println("Directory removal cancelled.")
			return nil
		}
	}

	// Remove directory
	fmt.Printf("Removing directory %s...\n", dirID)

	// Note: Actual deletion would require API support
	fmt.Printf("Directory removal functionality not yet implemented in API.\n")
	fmt.Printf("Would remove directory: %s (%s)\n", targetDir.Name, dirID)

	return nil
}

func formatBytes(bytes int64) string {
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

// Auth management functions
func authRegister(cmd *cobra.Command, args []string) error {
	authClient := auth.NewClient(authBaseURL)
	
	req := auth.RegisterRequest{
		FirstName:       firstName,
		LastName:        lastName,
		Email:           email,
		Password:        password,
		ConfirmPassword: password,
	}

	// Set optional fields
	if middleName != "" {
		req.MiddleName = &middleName
	}
	if suffix != "" {
		req.Suffix = &suffix
	}

	return authClient.Register(req)
}

func authLogin(cmd *cobra.Command, args []string) error {
	authClient := auth.NewClient(authBaseURL)
	
	req := auth.LoginRequest{
		Email:    email,
		Password: password,
	}

	return authClient.Login(req)
}

func authCreateKey(cmd *cobra.Command, args []string) error {
	authClient := auth.NewClient(authBaseURL)
	
	req := auth.CreateKeyRequest{
		Name: args[0],
	}

	return authClient.CreateKey(req, authToken)
}

func authRevokeKey(cmd *cobra.Command, args []string) error {
	authClient := auth.NewClient(authBaseURL)
	
	req := auth.RevokeKeyRequest{
		ClientID: args[0],
	}

	return authClient.RevokeKey(req, authToken)
}

func authVerify(cmd *cobra.Command, args []string) error {
	authClient := auth.NewClient(authBaseURL)
	
	req := auth.VerifyRequest{
		VerificationCode: args[0],
	}

	return authClient.Verify(req, authToken)
}
