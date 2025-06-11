# Koneksi Backup CLI

A powerful command-line tool for automated directory backup with real-time change detection, built for the Koneksi Secure Digital Storage Solution.

## Features

- **Real-time Monitoring**: Automatically detects changes in specified directories
- **Single File Backup**: Backup individual files on demand
- **Concurrent Backups**: Efficiently backs up multiple files in parallel
- **Smart Detection**: Only backs up files that have actually changed (using checksums)
- **Compression Support**: Optional gzip/zlib compression to save storage space
- **Database Tracking**: SQLite database tracks all backup history and metadata
- **Comprehensive Reporting**: Generates detailed JSON reports for each backup session
- **Full Restore Capability**: Restore backed up files from manifest files with automatic decompression
- **Configurable**: Flexible configuration for directories, exclusions, and performance

## Installation

```bash
# Clone the repository
git clone https://github.com/koneksi/backup-cli.git
cd koneksi-backup-cli

# Build the CLI
go build -o koneksi-backup cmd/koneksi-backup/main.go

# Optional: Install globally
go install ./cmd/koneksi-backup
```

## Quick Start

### 1. Initialize Configuration

```bash
koneksi-backup init
```

This creates a configuration file at `~/.koneksi-backup/config.yaml`. Edit this file to:
- Add directories you want to backup
- Configure exclusion patterns
- Adjust performance settings

### 2. Run the Backup Service

```bash
# Run in daemon mode (continuous monitoring)
koneksi-backup run

# Or specify a custom config file
koneksi-backup run -c /path/to/config.yaml
```

## Usage Examples

### Basic Commands

```bash
# Initialize configuration
koneksi-backup init

# Run backup service (daemon mode)
koneksi-backup run

# Perform one-time backup of a file or directory
koneksi-backup backup /path/to/file.txt
koneksi-backup backup /path/to/directory

# Check backup status
koneksi-backup status

# View latest backup report
koneksi-backup report
```

### Restore Operations

```bash
# Create a manifest from a backup report
koneksi-backup manifest ./reports/backup-20240111-143022.json restore-manifest.json

# Restore files from manifest
koneksi-backup restore restore-manifest.json /path/to/restore/directory

# Restore a single file by ID
koneksi-backup restore-file <file-id> /path/to/restored/file.txt
```

## Configuration

The configuration file (`~/.koneksi-backup/config.yaml`) supports the following options:

```yaml
api:
  base_url: "https://api.koneksi.io"
  client_id: "your-client-id"
  client_secret: "your-client-secret"
  timeout: 30
  retry_count: 3

backup:
  directories:
    - "/home/user/documents"
    - "/home/user/projects"
  exclude_patterns:
    - "*.tmp"
    - "*.log"
    - ".git"
    - "node_modules"
    - "__pycache__"
  check_interval: 300  # seconds
  max_file_size: 1073741824  # 1GB in bytes
  concurrent: 5  # number of concurrent uploads
  compression:
    enabled: false  # Enable to compress files before backup
    level: 6       # Compression level (1-9, where 9 is highest)
    format: "gzip" # Compression format (gzip or zlib)

report:
  directory: "./reports"
  format: "json"
  retention: 30  # keep reports for 30 days

log:
  level: "info"  # debug, info, warn, error
  file: ""  # optional log file path
  format: "json"

database:
  path: "./backup.db"  # SQLite database for tracking backups
  retention: 90  # days to keep backup records
```

## Backup Workflow

### 1. Continuous Monitoring Mode

```bash
# Start the backup service
koneksi-backup run

# The service will:
# - Monitor all configured directories
# - Detect file changes in real-time
# - Queue changes for backup
# - Upload files concurrently
# - Generate reports automatically
```

### 2. Backup Reports

Reports are automatically generated and saved in the configured report directory. Each report includes:

- Total files processed
- Successful/failed backups
- File sizes and checksums
- Duration and performance metrics
- Detailed error information

### 3. Restore Process

```bash
# Step 1: Create a manifest from a backup report
koneksi-backup manifest ./reports/backup-20240111-143022.json my-manifest.json

# Step 2: Review the manifest (optional)
cat my-manifest.json

# Step 3: Restore files
koneksi-backup restore my-manifest.json /home/user/restored-files

# The restore will:
# - Download files from Koneksi storage
# - Verify checksums
# - Recreate directory structure
# - Skip existing files with matching checksums
# - Generate a restore report
```

## Advanced Usage

### Exclude Patterns

Configure exclusion patterns in your config file to skip certain files:

```yaml
exclude_patterns:
  - "*.tmp"          # Skip all .tmp files
  - "*.log"          # Skip all .log files
  - ".git"           # Skip .git directories
  - "node_modules"   # Skip node_modules directories
  - "/path/to/skip"  # Skip specific paths
```

### Performance Tuning

Adjust these settings for optimal performance:

```yaml
backup:
  concurrent: 10       # Increase for faster uploads
  max_file_size: 5368709120  # 5GB max file size
  check_interval: 60   # Check for changes every minute
```

### Logging

Enable debug logging for troubleshooting:

```yaml
log:
  level: "debug"
  file: "/var/log/koneksi-backup.log"
```

## API Credentials

The CLI requires valid Koneksi API credentials. You can obtain these from:

1. Create an account on the Koneksi platform
2. Navigate to the API Keys page
3. Generate a new API key with appropriate permissions
4. Add the credentials to your config file

## Monitoring and Reports

### View Real-time Status

```bash
# Check current backup status
koneksi-backup status

# View detailed report
koneksi-backup report
```

### Report Format

Reports include:
- Backup ID and timestamp
- File statistics (total, successful, failed)
- Performance metrics
- Detailed file-level results
- Error information

## Troubleshooting

### Common Issues

1. **Authentication Errors**
   ```bash
   # Verify API credentials
   koneksi-backup test-connection
   ```

2. **Permission Denied**
   ```bash
   # Ensure read permissions on backup directories
   ls -la /path/to/backup/directory
   ```

3. **Large Files Failing**
   ```yaml
   # Increase max file size in config
   backup:
     max_file_size: 10737418240  # 10GB
   ```

### Debug Mode

Run with debug logging:
```bash
koneksi-backup run --log-level debug
```

## Security

- API credentials are stored locally in the config file
- All file transfers are encrypted in transit
- Checksums ensure data integrity
- Sensitive files can be excluded via patterns
