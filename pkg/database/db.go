package database

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
}

type BackupRecord struct {
	ID             int64
	FilePath       string
	FileID         string
	Checksum       string
	OriginalSize   int64
	CompressedSize int64
	IsCompressed   bool
	BackupTime     time.Time
	Status         string
	ErrorMessage   string
	Operation      string
}

type FileState struct {
	FilePath     string
	LastChecksum string
	LastBackup   time.Time
	BackupCount  int
	Status       string
}

func New(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.initialize(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return db, nil
}

func (db *DB) initialize() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS backup_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL,
			file_id TEXT,
			checksum TEXT NOT NULL,
			original_size INTEGER NOT NULL,
			compressed_size INTEGER,
			is_compressed BOOLEAN DEFAULT FALSE,
			backup_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			status TEXT NOT NULL,
			error_message TEXT,
			operation TEXT,
			UNIQUE(file_path, checksum)
		)`,
		`CREATE TABLE IF NOT EXISTS file_states (
			file_path TEXT PRIMARY KEY,
			last_checksum TEXT,
			last_backup TIMESTAMP,
			backup_count INTEGER DEFAULT 0,
			status TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_records_file_path ON backup_records(file_path)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_records_status ON backup_records(status)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_records_backup_time ON backup_records(backup_time)`,
	}

	for _, query := range queries {
		if _, err := db.conn.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query: %w", err)
		}
	}

	return nil
}

// InsertBackupRecord inserts a new backup record
func (db *DB) InsertBackupRecord(record BackupRecord) (int64, error) {
	query := `
		INSERT INTO backup_records 
		(file_path, file_id, checksum, original_size, compressed_size, is_compressed, 
		 backup_time, status, error_message, operation)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := db.conn.Exec(query,
		record.FilePath, record.FileID, record.Checksum,
		record.OriginalSize, record.CompressedSize, record.IsCompressed,
		record.BackupTime, record.Status, record.ErrorMessage, record.Operation,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert backup record: %w", err)
	}

	return result.LastInsertId()
}

// UpdateFileState updates or inserts file state
func (db *DB) UpdateFileState(state FileState) error {
	query := `
		INSERT OR REPLACE INTO file_states 
		(file_path, last_checksum, last_backup, backup_count, status)
		VALUES (?, ?, ?, ?, ?)
	`

	_, err := db.conn.Exec(query,
		state.FilePath, state.LastChecksum, state.LastBackup,
		state.BackupCount, state.Status,
	)
	if err != nil {
		return fmt.Errorf("failed to update file state: %w", err)
	}

	return nil
}

// GetFileState retrieves the state of a file
func (db *DB) GetFileState(filePath string) (*FileState, error) {
	query := `
		SELECT file_path, last_checksum, last_backup, backup_count, status
		FROM file_states
		WHERE file_path = ?
	`

	var state FileState
	err := db.conn.QueryRow(query, filePath).Scan(
		&state.FilePath, &state.LastChecksum, &state.LastBackup,
		&state.BackupCount, &state.Status,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get file state: %w", err)
	}

	return &state, nil
}

// GetBackupHistory retrieves backup history for a file
func (db *DB) GetBackupHistory(filePath string, limit int) ([]BackupRecord, error) {
	query := `
		SELECT id, file_path, file_id, checksum, original_size, compressed_size,
		       is_compressed, backup_time, status, error_message, operation
		FROM backup_records
		WHERE file_path = ?
		ORDER BY backup_time DESC
		LIMIT ?
	`

	rows, err := db.conn.Query(query, filePath, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query backup history: %w", err)
	}
	defer rows.Close()

	var records []BackupRecord
	for rows.Next() {
		var r BackupRecord
		err := rows.Scan(
			&r.ID, &r.FilePath, &r.FileID, &r.Checksum,
			&r.OriginalSize, &r.CompressedSize, &r.IsCompressed,
			&r.BackupTime, &r.Status, &r.ErrorMessage, &r.Operation,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan record: %w", err)
		}
		records = append(records, r)
	}

	return records, nil
}

// GetBackupStats retrieves backup statistics
func (db *DB) GetBackupStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Total files backed up
	var totalFiles int
	err := db.conn.QueryRow(`SELECT COUNT(DISTINCT file_path) FROM file_states`).Scan(&totalFiles)
	if err != nil {
		return nil, err
	}
	stats["total_files"] = totalFiles

	// Files by status
	statusQuery := `
		SELECT status, COUNT(*) 
		FROM file_states 
		GROUP BY status
	`
	rows, err := db.conn.Query(statusQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	statusCounts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		statusCounts[status] = count
	}
	stats["status_counts"] = statusCounts

	// Total backup size
	var totalOriginalSize, totalCompressedSize sql.NullInt64
	sizeQuery := `
		SELECT SUM(original_size), SUM(compressed_size)
		FROM backup_records
		WHERE status = 'success'
	`
	err = db.conn.QueryRow(sizeQuery).Scan(&totalOriginalSize, &totalCompressedSize)
	if err != nil {
		return nil, err
	}
	stats["total_original_size"] = totalOriginalSize.Int64
	stats["total_compressed_size"] = totalCompressedSize.Int64

	// Recent backups
	var recentCount int
	recentQuery := `
		SELECT COUNT(*) 
		FROM backup_records 
		WHERE backup_time > datetime('now', '-24 hours')
	`
	err = db.conn.QueryRow(recentQuery).Scan(&recentCount)
	if err != nil {
		return nil, err
	}
	stats["recent_backups_24h"] = recentCount

	return stats, nil
}

// SearchBackups searches for backups based on criteria
func (db *DB) SearchBackups(criteria SearchCriteria) ([]BackupRecord, error) {
	query := `
		SELECT id, file_path, file_id, checksum, original_size, compressed_size,
		       is_compressed, backup_time, status, error_message, operation
		FROM backup_records
		WHERE 1=1
	`
	args := []interface{}{}

	if criteria.FilePath != "" {
		query += " AND file_path LIKE ?"
		args = append(args, "%"+criteria.FilePath+"%")
	}

	if criteria.Status != "" {
		query += " AND status = ?"
		args = append(args, criteria.Status)
	}

	if !criteria.StartTime.IsZero() {
		query += " AND backup_time >= ?"
		args = append(args, criteria.StartTime)
	}

	if !criteria.EndTime.IsZero() {
		query += " AND backup_time <= ?"
		args = append(args, criteria.EndTime)
	}

	query += " ORDER BY backup_time DESC LIMIT ?"
	args = append(args, criteria.Limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search backups: %w", err)
	}
	defer rows.Close()

	var records []BackupRecord
	for rows.Next() {
		var r BackupRecord
		err := rows.Scan(
			&r.ID, &r.FilePath, &r.FileID, &r.Checksum,
			&r.OriginalSize, &r.CompressedSize, &r.IsCompressed,
			&r.BackupTime, &r.Status, &r.ErrorMessage, &r.Operation,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan record: %w", err)
		}
		records = append(records, r)
	}

	return records, nil
}

type SearchCriteria struct {
	FilePath  string
	Status    string
	StartTime time.Time
	EndTime   time.Time
	Limit     int
}

// CleanupOldRecords removes old backup records
func (db *DB) CleanupOldRecords(days int) error {
	query := `
		DELETE FROM backup_records 
		WHERE backup_time < datetime('now', '-' || ? || ' days')
		AND status = 'success'
	`

	result, err := db.conn.Exec(query, days)
	if err != nil {
		return fmt.Errorf("failed to cleanup old records: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected > 0 {
		// Vacuum to reclaim space
		_, _ = db.conn.Exec("VACUUM")
	}

	return nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}