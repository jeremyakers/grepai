package watcher

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yoanbernabeu/grepai/indexer"
)

type EventType int

const (
	EventCreate EventType = iota
	EventModify
	EventDelete
	EventRename
)

type FileEvent struct {
	Type EventType
	Path string
	// IsDir records directory identity before a removed path becomes unstatable.
	IsDir bool
}

type Watcher struct {
	root          string
	watcher       *fsnotify.Watcher
	backendEvents <-chan fsnotify.Event
	backendErrors <-chan error
	addWatch      func(string) error
	removeWatch   func(string) error
	ignore        *indexer.IgnoreMatcher
	supportsFile  func(string) bool
	debounceMs    int
	events        chan FileEvent
	done          chan struct{}
	directories   map[string]struct{}
	registered    map[string]struct{}
	directoriesMu sync.Mutex

	// Debouncing state
	pending    map[string]FileEvent
	pendingMu  sync.Mutex
	timer      *time.Timer
	flushReady chan struct{}
	closeOnce  sync.Once
	workers    sync.WaitGroup
}

// Option customizes watcher file selection.
type Option func(*Watcher)

// WithFileFilter sets the same path filter used by the scanner.
func WithFileFilter(filter func(string) bool) Option {
	return func(w *Watcher) {
		if filter != nil {
			w.supportsFile = filter
		}
	}
}

func NewWatcher(root string, ignore *indexer.IgnoreMatcher, debounceMs int, opts ...Option) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		root:    root,
		watcher: fsw,
		ignore:  ignore,
		supportsFile: func(path string) bool {
			return indexer.SupportedExtensions[strings.ToLower(filepath.Ext(path))]
		},
		debounceMs:  debounceMs,
		events:      make(chan FileEvent, 100),
		done:        make(chan struct{}),
		pending:     make(map[string]FileEvent),
		directories: make(map[string]struct{}),
		registered:  make(map[string]struct{}),
		flushReady:  make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(w)
	}
	w.addWatch = fsw.Add
	w.removeWatch = fsw.Remove
	w.backendEvents = fsw.Events
	w.backendErrors = fsw.Errors
	return w, nil
}

func (w *Watcher) Start(ctx context.Context) error {
	// Add root directory and all subdirectories
	if err := w.addRecursive(w.root); err != nil {
		return err
	}

	// Keep backend draining independent from potentially backpressured delivery.
	w.workers.Add(2)
	go func() {
		defer w.workers.Done()
		w.processEvents(ctx)
	}()
	go func() {
		defer w.workers.Done()
		w.processDelivery(ctx)
	}()

	return nil
}

func (w *Watcher) Events() <-chan FileEvent {
	return w.events
}

func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		w.pendingMu.Lock()
		if w.timer != nil {
			w.timer.Stop()
		}
		w.pendingMu.Unlock()
		close(w.done)
		err = w.watcher.Close()
		w.workers.Wait()
	})
	return err
}

func (w *Watcher) processEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case event, ok := <-w.backendEvents:
			if !ok {
				return
			}
			if err := w.handleEvent(event); err != nil {
				log.Printf("Watcher error: %v", err)
			}
		case err, ok := <-w.backendErrors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) error {
	relPath, err := filepath.Rel(w.root, event.Name)
	if err != nil {
		return nil
	}

	// Ignore files affect later siblings and imported descendants. Reload them
	// as data before applying the hidden-path filter.
	base := filepath.Base(relPath)
	if base == ".gitignore" || base == ".grepaiignore" {
		scope := filepath.Dir(relPath)
		if scope == "." {
			return w.ignore.Refresh()
		}
		return w.ignore.RefreshSubtree(scope)
	}

	if event.Has(fsnotify.Create) {
		info, err := os.Stat(event.Name)
		if err == nil && info.IsDir() {
			if err := w.addRecursiveWithFiles(event.Name, true); err != nil {
				log.Printf("Failed to add new directory %s: %v", event.Name, err)
			}
			return nil
		}
	}

	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		isDir, releaseErr := w.releaseDirectory(event.Name)
		if isDir {
			evType := EventDelete
			if event.Has(fsnotify.Rename) {
				evType = EventRename
			}
			w.debounceEvent(FileEvent{Type: evType, Path: relPath, IsDir: true})
			return releaseErr
		}
		if releaseErr != nil {
			return releaseErr
		}
	}

	// Hidden files are not indexed, but indexable dot-directories must reach the
	// directory lifecycle above. Configured metadata directories are rejected by
	// ShouldSkipDir during recursive registration.
	if strings.HasPrefix(filepath.Base(relPath), ".") {
		return nil
	}

	if w.ignore.ShouldIgnore(relPath) {
		return nil
	}

	if !w.supportsFile(event.Name) {
		return nil
	}

	var evType EventType
	switch {
	case event.Has(fsnotify.Create):
		evType = EventCreate
	case event.Has(fsnotify.Write):
		evType = EventModify
	case event.Has(fsnotify.Remove):
		evType = EventDelete
	case event.Has(fsnotify.Rename):
		evType = EventRename
	default:
		return nil
	}

	w.debounceEvent(FileEvent{
		Type: evType,
		Path: relPath,
	})
	return nil
}

func (e EventType) String() string {
	switch e {
	case EventCreate:
		return "CREATE"
	case EventModify:
		return "MODIFY"
	case EventDelete:
		return "DELETE"
	case EventRename:
		return "RENAME"
	default:
		return "UNKNOWN"
	}
}
