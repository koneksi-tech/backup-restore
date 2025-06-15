package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/pbkdf2"
)

const (
	// SaltSize is the size of the salt in bytes
	SaltSize = 32
	// NonceSize is the size of the nonce in bytes for AES-GCM
	NonceSize = 12
	// KeySize is the size of the AES key in bytes (AES-256)
	KeySize = 32
	// IterationCount for PBKDF2
	IterationCount = 100000
)

// Encryptor handles file encryption operations
type Encryptor struct {
	password string
}

// NewEncryptor creates a new encryptor with the given password
func NewEncryptor(password string) *Encryptor {
	return &Encryptor{
		password: password,
	}
}

// EncryptFile encrypts a file and returns the path to the encrypted file
func (e *Encryptor) EncryptFile(inputPath string, outputPath string) error {
	// Open input file
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer inputFile.Close()

	// Create output file
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	// Generate random salt
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive key from password using PBKDF2
	key := pbkdf2.Key([]byte(e.password), salt, IterationCount, KeySize, sha256.New)

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Write salt and nonce to output file
	if _, err := outputFile.Write(salt); err != nil {
		return fmt.Errorf("failed to write salt: %w", err)
	}
	if _, err := outputFile.Write(nonce); err != nil {
		return fmt.Errorf("failed to write nonce: %w", err)
	}

	// Read and encrypt file in chunks
	chunkSize := 4096
	buffer := make([]byte, chunkSize)

	for {
		n, err := inputFile.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read input file: %w", err)
		}
		if n == 0 {
			break
		}

		// Encrypt chunk
		encrypted := gcm.Seal(nil, nonce, buffer[:n], nil)
		
		// Write encrypted chunk size (4 bytes) and data
		chunkSizeBytes := make([]byte, 4)
		chunkSizeBytes[0] = byte(len(encrypted) >> 24)
		chunkSizeBytes[1] = byte(len(encrypted) >> 16)
		chunkSizeBytes[2] = byte(len(encrypted) >> 8)
		chunkSizeBytes[3] = byte(len(encrypted))
		
		if _, err := outputFile.Write(chunkSizeBytes); err != nil {
			return fmt.Errorf("failed to write chunk size: %w", err)
		}
		if _, err := outputFile.Write(encrypted); err != nil {
			return fmt.Errorf("failed to write encrypted data: %w", err)
		}

		// Increment nonce for next chunk
		incrementNonce(nonce)
	}

	return nil
}

// DecryptFile decrypts a file and returns the path to the decrypted file
func (e *Encryptor) DecryptFile(inputPath string, outputPath string) error {
	// Open input file
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer inputFile.Close()

	// Create output file
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	// Read salt
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(inputFile, salt); err != nil {
		return fmt.Errorf("failed to read salt: %w", err)
	}

	// Derive key from password using PBKDF2
	key := pbkdf2.Key([]byte(e.password), salt, IterationCount, KeySize, sha256.New)

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Read nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(inputFile, nonce); err != nil {
		return fmt.Errorf("failed to read nonce: %w", err)
	}

	// Read and decrypt file in chunks
	for {
		// Read chunk size
		chunkSizeBytes := make([]byte, 4)
		n, err := io.ReadFull(inputFile, chunkSizeBytes)
		if err == io.EOF || n == 0 {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read chunk size: %w", err)
		}

		chunkSize := int(chunkSizeBytes[0])<<24 | int(chunkSizeBytes[1])<<16 | 
			int(chunkSizeBytes[2])<<8 | int(chunkSizeBytes[3])

		// Read encrypted chunk
		encryptedChunk := make([]byte, chunkSize)
		if _, err := io.ReadFull(inputFile, encryptedChunk); err != nil {
			return fmt.Errorf("failed to read encrypted chunk: %w", err)
		}

		// Decrypt chunk
		decrypted, err := gcm.Open(nil, nonce, encryptedChunk, nil)
		if err != nil {
			return fmt.Errorf("failed to decrypt chunk: %w", err)
		}

		// Write decrypted data
		if _, err := outputFile.Write(decrypted); err != nil {
			return fmt.Errorf("failed to write decrypted data: %w", err)
		}

		// Increment nonce for next chunk
		incrementNonce(nonce)
	}

	return nil
}

// incrementNonce increments the nonce for the next chunk
func incrementNonce(nonce []byte) {
	for i := len(nonce) - 1; i >= 0; i-- {
		nonce[i]++
		if nonce[i] != 0 {
			break
		}
	}
}

// GetEncryptedFileName returns the encrypted file name with .enc extension
func GetEncryptedFileName(originalPath string) string {
	return originalPath + ".enc"
}

// GetDecryptedFileName removes the .enc extension from the file name
func GetDecryptedFileName(encryptedPath string) string {
	if len(encryptedPath) > 4 && encryptedPath[len(encryptedPath)-4:] == ".enc" {
		return encryptedPath[:len(encryptedPath)-4]
	}
	return encryptedPath + ".dec"
}