package compression

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
)

type Compressor interface {
	Compress(data []byte) ([]byte, error)
	Decompress(data []byte) ([]byte, error)
	Extension() string
}

type GzipCompressor struct {
	level int
}

type ZlibCompressor struct {
	level int
}

type NoOpCompressor struct{}

func NewCompressor(format string, level int) (Compressor, error) {
	switch format {
	case "gzip":
		if level < gzip.DefaultCompression || level > gzip.BestCompression {
			level = gzip.DefaultCompression
		}
		return &GzipCompressor{level: level}, nil
	case "zlib":
		if level < zlib.DefaultCompression || level > zlib.BestCompression {
			level = zlib.DefaultCompression
		}
		return &ZlibCompressor{level: level}, nil
	case "none", "":
		return &NoOpCompressor{}, nil
	default:
		return nil, fmt.Errorf("unsupported compression format: %s", format)
	}
}

// GzipCompressor implementation
func (g *GzipCompressor) Compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buf, g.level)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip writer: %w", err)
	}
	
	if _, err := writer.Write(data); err != nil {
		writer.Close()
		return nil, fmt.Errorf("failed to write gzip data: %w", err)
	}
	
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}
	
	return buf.Bytes(), nil
}

func (g *GzipCompressor) Decompress(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer reader.Close()
	
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read gzip data: %w", err)
	}
	
	return decompressed, nil
}

func (g *GzipCompressor) Extension() string {
	return ".gz"
}

// ZlibCompressor implementation
func (z *ZlibCompressor) Compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer, err := zlib.NewWriterLevel(&buf, z.level)
	if err != nil {
		return nil, fmt.Errorf("failed to create zlib writer: %w", err)
	}
	
	if _, err := writer.Write(data); err != nil {
		writer.Close()
		return nil, fmt.Errorf("failed to write zlib data: %w", err)
	}
	
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close zlib writer: %w", err)
	}
	
	return buf.Bytes(), nil
}

func (z *ZlibCompressor) Decompress(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create zlib reader: %w", err)
	}
	defer reader.Close()
	
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read zlib data: %w", err)
	}
	
	return decompressed, nil
}

func (z *ZlibCompressor) Extension() string {
	return ".zlib"
}

// NoOpCompressor implementation (no compression)
func (n *NoOpCompressor) Compress(data []byte) ([]byte, error) {
	return data, nil
}

func (n *NoOpCompressor) Decompress(data []byte) ([]byte, error) {
	return data, nil
}

func (n *NoOpCompressor) Extension() string {
	return ""
}

// Helper functions for file compression
func CompressFile(reader io.Reader, compressor Compressor) ([]byte, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	
	compressed, err := compressor.Compress(data)
	if err != nil {
		return nil, fmt.Errorf("failed to compress file: %w", err)
	}
	
	return compressed, nil
}

func DecompressFile(reader io.Reader, compressor Compressor) ([]byte, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read compressed file: %w", err)
	}
	
	decompressed, err := compressor.Decompress(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress file: %w", err)
	}
	
	return decompressed, nil
}

// Calculate compression ratio
func CompressionRatio(originalSize, compressedSize int64) float64 {
	if originalSize == 0 {
		return 0
	}
	return float64(originalSize-compressedSize) / float64(originalSize) * 100
}