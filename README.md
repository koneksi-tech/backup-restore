# Koneksi Backup and Restore CLI

A powerful command-line tool for automated directory backup with real-time change detection, built for the Koneksi Secure Digital Storage Solution.

## Features

- **Real-time Monitoring**: Automatically detects changes in specified directories
- **Single File Backup**: Backup individual files on demand
- **Directory Compression**: Compress entire directories into tar.gz archives before backup
- **Concurrent Backups**: Efficiently backs up multiple files in parallel
- **Smart Detection**: Only backs up files that have actually changed (using checksums)
- **Compression Support**: Optional gzip/zlib compression to save storage space
- **Large File Support**: Handle files up to 2GB with automatic compression recommendations
- **Database Tracking**: SQLite database tracks all backup history and metadata
- **Comprehensive Reporting**: Generates detailed JSON reports for each backup session
- **Full Restore Capability**: Restore backed up files from manifest files
- **Auto-extraction**: Automatically extract tar.gz archives after restore
- **Configurable**: Flexible configuration for directories, exclusions, and performance

## Installation

### Using Make (Recommended)

```bash
# Clone the repository
git clone https://github.com/koneksi-tech/koneksi-backup-tool.git
cd koneksi-backup-cli

# Build the CLI using make
make build

# Or build for all platforms
make build-all

# Install to $GOPATH/bin
make install
```

### Manual Build

```bash
# Build the CLI manually
go build -o koneksi-backup cmd/koneksi-backup/main.go

# Optional: Install globally
go install ./cmd/koneksi-backup
```

### Available Make Targets

```bash
make              # Run tests and build (default)
make build        # Build for current platform
make build-all    # Build for Linux, Windows, and macOS
make test         # Run unit tests
make test-coverage # Run tests with coverage report
make clean        # Clean build artifacts
make deps         # Download dependencies
make fmt          # Format code
make lint         # Run linter
make package      # Create distribution packages
make docker       # Build Docker image
make help         # Show all available targets
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

### Directory Compression

Compress entire directories into a single archive before backup:

```bash
# Backup a directory as a compressed tar.gz archive
koneksi-backup backup ./my-project --compress-dir

# This will:
# 1. Compress the directory to a temporary tar.gz file
# 2. Upload the compressed archive (much faster for many files)
# 3. Clean up the temporary file

# Example output:
# Starting backup of: ./my-project
# Compressing directory before backup...
# Directory compressed to /tmp/backup-123456.tar.gz (size: 2.3MB)
# Backup completed successfully
```

### Restore Operations

```bash
# Create a manifest from a backup report
koneksi-backup manifest ./reports/backup-20240111-143022.json restore-manifest.json

# Restore files from manifest
koneksi-backup restore restore-manifest.json /path/to/restore/directory

# Restore with automatic extraction of tar.gz archives
koneksi-backup restore restore-manifest.json /path/to/restore --auto-extract

# Restore a single file by ID
koneksi-backup restore-file <file-id> /path/to/restored/file.txt
```

### Large File Backup Example

For files larger than 100MB, compression is recommended:

```bash
# Example: Backing up a 1GB database dump
# First, compress the file
gzip large-database.sql  # Creates large-database.sql.gz

# Then backup the compressed file
koneksi-backup backup large-database.sql.gz

# Or use directory compression for multiple large files
koneksi-backup backup ./database-dumps --compress-dir
```

### Real-World Examples

```bash
# Backup a development project (excluding build artifacts)
koneksi-backup backup ./my-app --compress-dir

# Backup important documents
koneksi-backup backup ~/Documents/important

# Backup and restore a website
# 1. Backup
koneksi-backup backup /var/www/html --compress-dir
# 2. Create manifest from report
koneksi-backup manifest reports/backup-20250612-*.json website-manifest.json
# 3. Restore to new server
koneksi-backup restore website-manifest.json /var/www/html --auto-extract

# Backup large log files with compression
tar -czf logs-archive.tar.gz /var/log/myapp/
koneksi-backup backup logs-archive.tar.gz
```

## Configuration

The configuration file (`~/.koneksi-backup/config.yaml`) supports the following options:

```yaml
api:
  base_url: "https://koneksi-tyk-gateway-3rvca.ondigitalocean.app"
  client_id: "your-client-id"
  client_secret: "your-client-secret"
  directory_id: "your-directory-id"
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

### Best Practices for Large Files

1. **Use Compression**: Always compress large files before backup
   ```bash
   # For single large files
   gzip myfile.sql                          # Creates myfile.sql.gz
   koneksi-backup backup myfile.sql.gz
   
   # For directories with large files
   koneksi-backup backup ./large-dir --compress-dir
   ```

2. **Compression Ratios**: Typical compression results
   - Text files: 80-95% reduction
   - Log files: 90-98% reduction  
   - Database dumps: 70-90% reduction
   - Already compressed files (zip, jpg, mp4): minimal reduction

3. **Size Recommendations**:
   - Files < 10MB: Direct backup without compression
   - Files 10MB-100MB: Consider compression based on file type
   - Files > 100MB: Always use compression
   - Directories with many files: Use `--compress-dir` flag

4. **Example: 1GB File Backup**
   ```bash
   # Original file: 1GB text file
   ls -lh large-file.txt
   # -rw-r--r-- 1 user user 1.0G Jun 12 10:00 large-file.txt
   
   # Compress (reduces to ~50MB for text)
   gzip large-file.txt
   
   # Backup compressed file
   koneksi-backup backup large-file.txt.gz
   # Upload time: ~30 seconds vs 10+ minutes uncompressed
   ```

### Logging

Enable debug logging for troubleshooting:

```yaml
log:
  level: "debug"
  file: "/var/log/koneksi-backup.log"
```

## API Credentials and Authentication

### User Registration and API Key Management

The CLI includes built-in authentication commands to register users and manage API keys:

```bash
# 1. Register a new account
koneksi-backup auth register \
    --first-name "John" \
    --last-name "Doe" \
    --email "john@example.com" \
    --password "StrongPass123!"

# 2. Login to get access token
koneksi-backup auth login \
    --email "john@example.com" \
    --password "StrongPass123!"

# 3. Verify your account (check email for verification code)
koneksi-backup auth verify "123456" -t <access-token>

# 4. Create API key (requires verified account)
koneksi-backup auth create-key "My Backup Key" -t <access-token>

# 5. Revoke API key when no longer needed
koneksi-backup auth revoke-key <client-id> -t <access-token>
```

### Using API Credentials

Once you have your API credentials, configure them:

#### Option 1: Configuration File

Add to your `~/.koneksi-backup/config.yaml`:
```yaml
api:
  client_id: "your-client-id"
  client_secret: "your-client-secret"
```

#### Option 2: Environment Variables

```bash
export KONEKSI_API_CLIENT_ID="your-client-id"
export KONEKSI_API_CLIENT_SECRET="your-client-secret"
export KONEKSI_API_DIRECTORY_ID="your-directory-id"

# Run backup
koneksi-backup backup /path/to/file.txt
```

### Directory Management

Manage your backup directories:

```bash
# List all directories
koneksi-backup dir list

# Create a new directory
koneksi-backup dir create "my-backups" -d "Description here"

# Remove a directory (with confirmation)
koneksi-backup dir remove <directory-id>

# Force remove without confirmation
koneksi-backup dir remove <directory-id> -f
```

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
- Authentication logic is separated in `internal/auth` package for better security isolation

## Project Structure

```
koneksi-backup-cli/
├── cmd/koneksi-backup/     # CLI entry point
│   └── main.go
├── internal/               # Internal packages
│   ├── api/               # API client for backup operations
│   ├── auth/              # Authentication client (register, login, verify, API keys)
│   ├── backup/            # Backup and restore logic
│   ├── config/            # Configuration management
│   ├── monitor/           # File system monitoring
│   └── report/            # Backup reporting
├── pkg/                   # Reusable packages
│   ├── archive/           # Archive compression/decompression
│   └── database/          # SQLite database for tracking
└── Makefile              # Build automation
