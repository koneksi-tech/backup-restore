package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

type FileChange struct {
	Path      string
	Operation string
	Timestamp time.Time
	Size      int64
	IsDir     bool
}

type Watcher struct {
	watcher    *fsnotify.Watcher
	logger     *zap.Logger
	changes    chan FileChange
	errors     chan error
	excludes   []string
	mu         sync.RWMutex
	watched    map[string]bool
}

func NewWatcher(logger *zap.Logger, excludePatterns []string) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	return &Watcher{
		watcher:  watcher,
		logger:   logger,
		changes:  make(chan FileChange, 1000),
		errors:   make(chan error, 100),
		excludes: excludePatterns,
		watched:  make(map[string]bool),
	}, nil
}

func (w *Watcher) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-w.watcher.Events:
				if !ok {
					return
				}
				w.handleEvent(event)
			case err, ok := <-w.watcher.Errors:
				if !ok {
					return
				}
				w.logger.Error("watcher error", zap.Error(err))
				w.errors <- err
			}
		}
	}()
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	if w.shouldExclude(event.Name) {
		return
	}

	info, err := os.Stat(event.Name)
	if err != nil && !os.IsNotExist(err) {
		w.logger.Error("failed to stat file", zap.String("path", event.Name), zap.Error(err))
		return
	}

	change := FileChange{
		Path:      event.Name,
		Timestamp: time.Now(),
	}

	if info != nil {
		change.Size = info.Size()
		change.IsDir = info.IsDir()
	}

	switch {
	case event.Op&fsnotify.Create == fsnotify.Create:
		change.Operation = "create"
		if info != nil && info.IsDir() {
			w.AddDirectory(event.Name)
		}
	case event.Op&fsnotify.Write == fsnotify.Write:
		change.Operation = "modify"
	case event.Op&fsnotify.Remove == fsnotify.Remove:
		change.Operation = "delete"
		w.mu.Lock()
		delete(w.watched, event.Name)
		w.mu.Unlock()
	case event.Op&fsnotify.Rename == fsnotify.Rename:
		change.Operation = "rename"
		w.mu.Lock()
		delete(w.watched, event.Name)
		w.mu.Unlock()
	case event.Op&fsnotify.Chmod == fsnotify.Chmod:
		change.Operation = "chmod"
	}

	w.logger.Debug("file change detected",
		zap.String("path", change.Path),
		zap.String("operation", change.Operation),
		zap.Int64("size", change.Size),
	)

	select {
	case w.changes <- change:
	default:
		w.logger.Warn("changes channel full, dropping event", zap.String("path", event.Name))
	}
}

func (w *Watcher) AddDirectory(path string) error {
	return filepath.Walk(path, func(walkPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if w.shouldExclude(walkPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			w.mu.Lock()
			if _, exists := w.watched[walkPath]; !exists {
				if err := w.watcher.Add(walkPath); err != nil {
					w.mu.Unlock()
					return fmt.Errorf("failed to add directory %s: %w", walkPath, err)
				}
				w.watched[walkPath] = true
				w.logger.Info("watching directory", zap.String("path", walkPath))
			}
			w.mu.Unlock()
		}

		return nil
	})
}

func (w *Watcher) RemoveDirectory(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for watched := range w.watched {
		if filepath.HasPrefix(watched, path) {
			if err := w.watcher.Remove(watched); err != nil {
				w.logger.Error("failed to remove watch", zap.String("path", watched), zap.Error(err))
			}
			delete(w.watched, watched)
		}
	}

	return nil
}

func (w *Watcher) shouldExclude(path string) bool {
	for _, pattern := range w.excludes {
		matched, err := filepath.Match(pattern, filepath.Base(path))
		if err == nil && matched {
			return true
		}
		
		if filepath.HasPrefix(path, pattern) {
			return true
		}
	}
	return false
}

func (w *Watcher) Changes() <-chan FileChange {
	return w.changes
}

func (w *Watcher) Errors() <-chan error {
	return w.errors
}

func (w *Watcher) Close() error {
	close(w.changes)
	close(w.errors)
	return w.watcher.Close()
}