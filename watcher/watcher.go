package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// skipPrefixes are directory/file names to skip during watching.
// Mirrors scanner.skipDirs: show dot entries except VCS / deps / app store.
var skipPrefixes = map[string]bool{
	".git": true, ".svn": true,
	"node_modules": true, "vendor": true, "__pycache__": true,
	".warp-snapshots": true,
}

// Callback is called when files change (debounced).
type Callback func(events []string)

// Watcher wraps fsnotify for workspace file watching.
type Watcher struct {
	fsw       *fsnotify.Watcher
	workspace string
	cb        Callback
	events    map[string]struct{}
	mu        sync.Mutex
	done      chan struct{}
}

// New creates a new filesystem watcher for the workspace.
func New(workspace string, cb Callback) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		fsw:       fsw,
		workspace: workspace,
		cb:        cb,
		events:    make(map[string]struct{}),
		done:      make(chan struct{}),
	}
	// Recursively watch all subdirectories
	if err := w.addDirs(workspace); err != nil {
		return nil, err
	}
	go w.loop()
	return w, nil
}

// Close stops the watcher.
func (w *Watcher) Close() {
	close(w.done)
	w.fsw.Close()
}

func (w *Watcher) addDirs(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if skipPrefixes[name] {
				return filepath.SkipDir
			}
			return w.fsw.Add(path)
		}
		return nil
	})
}

func (w *Watcher) loop() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case evt, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			rel, err := filepath.Rel(w.workspace, evt.Name)
			if err != nil {
				continue
			}
			if shouldSkip(rel, evt.Name) {
				continue
			}
			// If a new directory is created, watch it
			if evt.Has(fsnotify.Create) {
				if info, err := os.Stat(evt.Name); err == nil && info.IsDir() {
					w.addDirs(evt.Name)
				}
			}
			w.mu.Lock()
			w.events[rel] = struct{}{}
			w.mu.Unlock()
		case <-ticker.C:
			w.mu.Lock()
			if len(w.events) > 0 {
				evts := make([]string, 0, len(w.events))
				for e := range w.events {
					evts = append(evts, e)
				}
				w.events = make(map[string]struct{})
				w.mu.Unlock()
				w.cb(evts)
			} else {
				w.mu.Unlock()
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			_ = err // silently ignore watcher errors
		}
	}
}

func shouldSkip(relPath, absPath string) bool {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, p := range parts {
		if skipPrefixes[p] {
			return true
		}
	}
	return false
}

