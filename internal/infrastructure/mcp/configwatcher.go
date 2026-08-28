// Package mcp provides MCP server management with config hot-reload via fsnotify.
package mcp

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ConfigChangeHandler is called when the MCP config file changes.
type ConfigChangeHandler func(ctx context.Context, newPath string) error

// ConfigWatcher watches an MCP config file for changes and triggers reloads.
type ConfigWatcher struct {
	mu       sync.RWMutex
	path     string
	interval time.Duration
	cancel   context.CancelFunc
	handler  ConfigChangeHandler
}

// WatcherOption configures the ConfigWatcher.
type WatcherOption func(*ConfigWatcher)

// WithPollInterval sets the fallback poll interval (default: 5s).
func WithPollInterval(d time.Duration) WatcherOption {
	return func(w *ConfigWatcher) {
		w.interval = d
	}
}

// NewConfigWatcher creates a config file watcher using fsnotify.
func NewConfigWatcher(path string, handler ConfigChangeHandler, opts ...WatcherOption) *ConfigWatcher {
	w := &ConfigWatcher{
		path:     path,
		interval: 5 * time.Second,
		handler:  handler,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Start begins watching the config file. Non-blocking.
func (w *ConfigWatcher) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)
	go w.watch(ctx)
	slog.Default().Info("MCP config watcher started",
		"path", w.path,
		"interval", w.interval,
	)
}

// Stop stops the watcher.
func (w *ConfigWatcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	slog.Default().Info("MCP config watcher stopped", "path", w.path)
}

func (w *ConfigWatcher) watch(ctx context.Context) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Default().Error("failed to create fsnotify watcher, falling back to poll",
			"error", err)
		w.watchPoll(ctx)
		return
	}
	defer watcher.Close()

	// Watch the directory (not the file directly, because fsnotify can't
	// detect creates on non-existent files).
	dir := dirOf(w.path)
	if dir == "" {
		dir = "."
	}
	if err := watcher.Add(dir); err != nil {
		slog.Default().Error("failed to watch directory, falling back to poll",
			"dir", dir, "error", err)
		w.watchPoll(ctx)
		return
	}

	// Debounce: batch rapid changes
	var debounce *time.Timer

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Only care about writes and creates to our config file
			if event.Name != w.path {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}

			// Debounce: wait 500ms for more changes
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(500*time.Millisecond, func() {
				slog.Default().Info("MCP config changed, reloading",
					"path", w.path,
					"op", event.Op.String(),
				)
				if err := w.handler(ctx, w.path); err != nil {
					slog.Default().Error("MCP config reload failed",
						"path", w.path,
						"error", err,
					)
				}
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Default().Error("fsnotify error", "error", err)
		}
	}
}

// watchPoll is a fallback polling-based watcher for platforms without fsnotify support.
func (w *ConfigWatcher) watchPoll(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	var lastModTime time.Time
	if info, err := fileStat(w.path); err == nil {
		lastModTime = info
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			modTime, err := fileStat(w.path)
			if err != nil {
				continue
			}
			if modTime.After(lastModTime) {
				lastModTime = modTime
				slog.Default().Info("MCP config changed (poll), reloading",
					"path", w.path,
				)
				if err := w.handler(ctx, w.path); err != nil {
					slog.Default().Error("MCP config reload failed",
						"path", w.path,
						"error", err,
					)
				}
			}
		}
	}
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return ""
}
