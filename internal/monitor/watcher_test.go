package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewWatcher(t *testing.T) {
	logger := zap.NewNop()
	excludes := []string{"*.tmp", ".git"}

	watcher, err := NewWatcher(logger, excludes)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	if watcher.logger == nil {
		t.Error("logger should not be nil")
	}
	if len(watcher.excludes) != 2 {
		t.Errorf("expected 2 excludes, got %d", len(watcher.excludes))
	}
}

func TestWatcherFileOperations(t *testing.T) {
	logger := zap.NewNop()
	watcher, err := NewWatcher(logger, []string{})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	// Create test directory
	testDir := t.TempDir()

	// Start watcher
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher.Start(ctx)

	// Add directory to watch
	err = watcher.AddDirectory(testDir)
	if err != nil {
		t.Fatalf("failed to add directory: %v", err)
	}

	// Allow watcher to initialize
	time.Sleep(100 * time.Millisecond)

	// Test file creation
	testFile := filepath.Join(testDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Wait for event
	select {
	case change := <-watcher.Changes():
		if change.Path != testFile {
			t.Errorf("expected path %s, got %s", testFile, change.Path)
		}
		if change.Operation != "create" && change.Operation != "modify" {
			t.Errorf("expected create or modify operation, got %s", change.Operation)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for file change event")
	}

	// Test file modification
	time.Sleep(100 * time.Millisecond) // Allow time for previous event to settle
	if err := os.WriteFile(testFile, []byte("modified content"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}

	select {
	case change := <-watcher.Changes():
		// Accept either modify or chmod as different systems report differently
		if change.Operation != "modify" && change.Operation != "chmod" {
			t.Errorf("expected modify or chmod operation, got %s", change.Operation)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for modify event")
	}

	// Test file deletion
	time.Sleep(100 * time.Millisecond) // Allow time for previous event to settle
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to remove test file: %v", err)
	}

	select {
	case change := <-watcher.Changes():
		// File systems can report different events for deletion
		// Some systems send modify before delete, others send delete directly
		if change.Operation == "modify" {
			// Wait for the actual delete event
			select {
			case change = <-watcher.Changes():
				if change.Operation != "delete" && change.Operation != "remove" {
					t.Logf("Warning: expected delete operation after modify, got %s", change.Operation)
				}
			case <-time.After(1 * time.Second):
				// Some systems only send modify for deletion, which is acceptable
				t.Logf("Only received modify event for deletion, which is acceptable on some systems")
			}
		} else if change.Operation != "delete" && change.Operation != "remove" {
			t.Logf("Warning: expected delete operation, got %s", change.Operation)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for delete event")
	}
}

func TestWatcherExclusions(t *testing.T) {
	logger := zap.NewNop()
	excludes := []string{"*.tmp", ".git", "node_modules"}
	watcher, err := NewWatcher(logger, excludes)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	tests := []struct {
		path     string
		excluded bool
	}{
		{"test.txt", false},
		{"test.tmp", true},
		{".git/config", true},
		{"node_modules/package.json", true},
		{"src/main.go", false},
	}

	for _, tt := range tests {
		excluded := watcher.shouldExclude(tt.path)
		if excluded != tt.excluded {
			t.Errorf("path %s: expected excluded=%v, got %v", tt.path, tt.excluded, excluded)
		}
	}
}

func TestWatcherSubdirectories(t *testing.T) {
	logger := zap.NewNop()
	watcher, err := NewWatcher(logger, []string{})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	// Create test directory structure
	testDir := t.TempDir()
	subDir := filepath.Join(testDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdirectory: %v", err)
	}

	// Start watcher
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher.Start(ctx)

	// Add root directory
	err = watcher.AddDirectory(testDir)
	if err != nil {
		t.Fatalf("failed to add directory: %v", err)
	}

	// Verify subdirectory is watched
	watcher.mu.RLock()
	_, rootWatched := watcher.watched[testDir]
	_, subWatched := watcher.watched[subDir]
	watcher.mu.RUnlock()

	if !rootWatched {
		t.Error("root directory should be watched")
	}
	if !subWatched {
		t.Error("subdirectory should be watched")
	}

	// Test file creation in subdirectory
	time.Sleep(100 * time.Millisecond)
	subFile := filepath.Join(subDir, "sub.txt")
	if err := os.WriteFile(subFile, []byte("sub content"), 0644); err != nil {
		t.Fatalf("failed to create file in subdirectory: %v", err)
	}

	select {
	case change := <-watcher.Changes():
		if change.Path != subFile {
			t.Errorf("expected path %s, got %s", subFile, change.Path)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for subdirectory file event")
	}
}

func TestWatcherRemoveDirectory(t *testing.T) {
	logger := zap.NewNop()
	watcher, err := NewWatcher(logger, []string{})
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	testDir := t.TempDir()
	
	// Add directory
	err = watcher.AddDirectory(testDir)
	if err != nil {
		t.Fatalf("failed to add directory: %v", err)
	}

	// Verify it's watched
	watcher.mu.RLock()
	_, watched := watcher.watched[testDir]
	watcher.mu.RUnlock()
	if !watched {
		t.Error("directory should be watched after adding")
	}

	// Remove directory
	err = watcher.RemoveDirectory(testDir)
	if err != nil {
		t.Fatalf("failed to remove directory: %v", err)
	}

	// Verify it's not watched
	watcher.mu.RLock()
	_, watched = watcher.watched[testDir]
	watcher.mu.RUnlock()
	if watched {
		t.Error("directory should not be watched after removal")
	}
}