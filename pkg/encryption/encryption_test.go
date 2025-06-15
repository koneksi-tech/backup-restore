package encryption

import (
	"bytes"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	// Create temporary directory
	tempDir := t.TempDir()

	// Test cases
	tests := []struct {
		name     string
		content  []byte
		password string
	}{
		{
			name:     "small file",
			content:  []byte("Hello, World!"),
			password: "test-password-123",
		},
		{
			name:     "empty file",
			content:  []byte{},
			password: "test-password-456",
		},
		{
			name:     "large file",
			content:  generateRandomData(10 * 1024 * 1024), // 10MB
			password: "test-password-789",
		},
		{
			name:     "binary data",
			content:  generateRandomData(4096),
			password: "test-password-abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file
			inputPath := filepath.Join(tempDir, "input.txt")
			if err := os.WriteFile(inputPath, tt.content, 0644); err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}

			// Encrypt file
			encryptor := NewEncryptor(tt.password)
			encryptedPath := filepath.Join(tempDir, "encrypted.enc")
			if err := encryptor.EncryptFile(inputPath, encryptedPath); err != nil {
				t.Fatalf("failed to encrypt file: %v", err)
			}

			// Verify encrypted file exists and is different from original
			encryptedData, err := os.ReadFile(encryptedPath)
			if err != nil {
				t.Fatalf("failed to read encrypted file: %v", err)
			}
			if len(tt.content) > 0 && bytes.Equal(encryptedData, tt.content) {
				t.Error("encrypted data should be different from original")
			}

			// Decrypt file
			decryptedPath := filepath.Join(tempDir, "decrypted.txt")
			if err := encryptor.DecryptFile(encryptedPath, decryptedPath); err != nil {
				t.Fatalf("failed to decrypt file: %v", err)
			}

			// Verify decrypted content matches original
			decryptedData, err := os.ReadFile(decryptedPath)
			if err != nil {
				t.Fatalf("failed to read decrypted file: %v", err)
			}
			if !bytes.Equal(decryptedData, tt.content) {
				t.Errorf("decrypted data does not match original\noriginal: %v\ndecrypted: %v", 
					tt.content[:min(100, len(tt.content))], 
					decryptedData[:min(100, len(decryptedData))])
			}

			// Clean up
			os.Remove(inputPath)
			os.Remove(encryptedPath)
			os.Remove(decryptedPath)
		})
	}
}

func TestEncryptWithWrongPassword(t *testing.T) {
	tempDir := t.TempDir()

	// Create test file
	content := []byte("Secret data that should not be readable")
	inputPath := filepath.Join(tempDir, "secret.txt")
	if err := os.WriteFile(inputPath, content, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Encrypt with one password
	encryptor1 := NewEncryptor("correct-password")
	encryptedPath := filepath.Join(tempDir, "secret.enc")
	if err := encryptor1.EncryptFile(inputPath, encryptedPath); err != nil {
		t.Fatalf("failed to encrypt file: %v", err)
	}

	// Try to decrypt with wrong password
	encryptor2 := NewEncryptor("wrong-password")
	decryptedPath := filepath.Join(tempDir, "decrypted.txt")
	err := encryptor2.DecryptFile(encryptedPath, decryptedPath)
	if err == nil {
		t.Error("decryption with wrong password should fail")
	}
}

func TestGetEncryptedFileName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"file.txt", "file.txt.enc"},
		{"/path/to/file.pdf", "/path/to/file.pdf.enc"},
		{"file", "file.enc"},
		{"file.tar.gz", "file.tar.gz.enc"},
	}

	for _, tt := range tests {
		result := GetEncryptedFileName(tt.input)
		if result != tt.expected {
			t.Errorf("GetEncryptedFileName(%s) = %s, want %s", tt.input, result, tt.expected)
		}
	}
}

func TestGetDecryptedFileName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"file.txt.enc", "file.txt"},
		{"/path/to/file.pdf.enc", "/path/to/file.pdf"},
		{"file.enc", "file"},
		{"file.txt", "file.txt.dec"}, // No .enc extension
	}

	for _, tt := range tests {
		result := GetDecryptedFileName(tt.input)
		if result != tt.expected {
			t.Errorf("GetDecryptedFileName(%s) = %s, want %s", tt.input, result, tt.expected)
		}
	}
}

func generateRandomData(size int) []byte {
	data := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		panic(err)
	}
	return data
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}