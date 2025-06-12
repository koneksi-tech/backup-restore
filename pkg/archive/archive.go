package archive

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CompressDirectory creates a tar.gz archive from a directory
func CompressDirectory(sourcePath string, targetPath string) error {
	// Create target file
	file, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}
	defer file.Close()

	// Create gzip writer
	gzWriter := gzip.NewWriter(file)
	defer gzWriter.Close()

	// Create tar writer
	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	// Walk directory and add files to archive
	return filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("failed to create tar header: %w", err)
		}

		// Update header name to be relative to source directory
		relPath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}
		
		// Use forward slashes for tar compatibility
		header.Name = strings.ReplaceAll(relPath, string(filepath.Separator), "/")
		
		// Skip the root directory itself
		if header.Name == "." {
			return nil
		}

		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header: %w", err)
		}

		// If it's a file, write its contents
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file: %w", err)
			}
			defer file.Close()

			if _, err := io.Copy(tarWriter, file); err != nil {
				return fmt.Errorf("failed to write file to archive: %w", err)
			}
		}

		return nil
	})
}

// DecompressArchive extracts a tar.gz archive to a directory
func DecompressArchive(archivePath string, targetPath string) error {
	// Open archive file
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive file: %w", err)
	}
	defer file.Close()

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzReader)

	// Extract files
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Create full path
		fullPath := filepath.Join(targetPath, header.Name)

		// Ensure the path is safe (doesn't escape target directory)
		if !strings.HasPrefix(filepath.Clean(fullPath), filepath.Clean(targetPath)) {
			return fmt.Errorf("unsafe path in archive: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			// Create directory
			if err := os.MkdirAll(fullPath, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		case tar.TypeReg:
			// Create directory for file if needed
			dir := filepath.Dir(fullPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create directory for file: %w", err)
			}

			// Create file
			file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}

			// Copy file contents
			if _, err := io.Copy(file, tarReader); err != nil {
				file.Close()
				return fmt.Errorf("failed to extract file: %w", err)
			}
			file.Close()

			// Set modification time
			if err := os.Chtimes(fullPath, header.AccessTime, header.ModTime); err != nil {
				// Log warning but don't fail
				fmt.Printf("Warning: failed to set file times for %s: %v\n", fullPath, err)
			}
		}
	}

	return nil
}

// IsDirectory checks if a path is a directory
func IsDirectory(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// CreateTempArchive creates a temporary tar.gz file for a directory
func CreateTempArchive(dirPath string) (string, error) {
	// Create temp file
	tempFile, err := os.CreateTemp("", "backup-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	tempFile.Close()

	// Compress directory to temp file
	if err := CompressDirectory(dirPath, tempPath); err != nil {
		os.Remove(tempPath)
		return "", err
	}

	return tempPath, nil
}