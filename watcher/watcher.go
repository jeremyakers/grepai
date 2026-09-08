package watcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
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
	addWatch      func(string) error
	removeWatch   func(string) error
	ignore        *indexer.IgnoreMatcher
	debounceMs    int
	events        chan FileEvent
	done          chan struct{}
	directories   map[string]struct{}
	directoriesMu sync.Mutex

	// Debouncing state
	pending   map[string]FileEvent
	pendingMu sync.Mutex
	timer     *time.Timer
}

func NewWatcher(root string, ignore *indexer.IgnoreMatcher, debounceMs int) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		root:        root,
		watcher:     fsw,
		ignore:      ignore,
		debounceMs:  debounceMs,
		events:      make(chan FileEvent, 100),
		done:        make(chan struct{}),
		pending:     make(map[string]FileEvent),
		directories: make(map[string]struct{}),
	}
	w.addWatch = fsw.Add
	w.removeWatch = fsw.Remove
	return w, nil
}

func (w *Watcher) Start(ctx context.Context) error {
	// Add root directory and all subdirectories
	if err := w.addRecursive(w.root); err != nil {
		return err
	}

	// Start event processing
	go w.processEvents(ctx)

	return nil
}

func (w *Watcher) Events() <-chan FileEvent {
	return w.events
}

func (w *Watcher) Close() error {
	close(w.done)
	return w.watcher.Close()
}

// addRecursive walks the tree rooted at root and registers an fsnotify watch
// on every directory that isn't ignored. It uses filepath.WalkDir (not
// filepath.Walk) so that directory entries are read directly from the
// readdir results instead of an extra Lstat syscall per file -- on repos with
// 100k+ files this roughly halves the syscall count of the initial/restart
// tree walk, which matters because watch startup blocks on this before it
// starts serving fsnotify events.
func (w *Watcher) addRecursive(root string) error {
	return w.addRecursiveWithFiles(root, false)
}

func (w *Watcher) addRecursiveWithFiles(root string, emitFiles bool) error {
	var files []FileEvent
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip inaccessible paths
		}

		relPath, err := filepath.Rel(w.root, path)
		if err != nil {
			return nil
		}

		// Handle directories: use ShouldSkipDir to respect .grepaiignore negations
		if d.IsDir() {
			if w.ignore.ShouldSkipDir(relPath) {
				return filepath.SkipDir
			}
			// Directory is not skipped; watch it if not individually ignored
			if !w.ignore.ShouldIgnore(relPath) {
				if err := w.addWatch(path); err != nil {
					log.Printf("Failed to watch %s: %v", path, err)
				} else {
					w.directoriesMu.Lock()
					w.directories[path] = struct{}{}
					w.directoriesMu.Unlock()
				}
			}
			return nil
		}

		// Skip ignored files
		if w.ignore.ShouldIgnore(relPath) {
			return nil
		}
		if emitFiles && indexer.SupportedExtensions[strings.ToLower(filepath.Ext(path))] {
			files = append(files, FileEvent{Type: EventCreate, Path: relPath})
		}

		return nil
	})
	if err != nil {
		return err
	}
	for _, event := range files {
		w.debounceEvent(event)
	}
	return nil
}

func (w *Watcher) processEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if err := w.handleEvent(event); err != nil {
				log.Printf("Watcher error: %v", err)
			}
		case err, ok := <-w.watcher.Errors:
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

	// Hidden paths are never watched or indexed.
	if strings.HasPrefix(filepath.Base(relPath), ".") {
		return nil
	}

	if event.Has(fsnotify.Create) {
		info, err := os.Stat(event.Name)
		if err == nil && info.IsDir() {
			if w.ignore.ShouldSkipDir(relPath) {
				return nil
			}
			if err := w.addRecursiveWithFiles(event.Name, true); err != nil {
				log.Printf("Failed to add new directory %s: %v", event.Name, err)
			}
			return nil
		}
	}

	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		isDir, err := w.releaseDirectory(event.Name)
		if err != nil {
			return err
		}
		if isDir {
			evType := EventDelete
			if event.Has(fsnotify.Rename) {
				evType = EventRename
			}
			w.debounceEvent(FileEvent{Type: evType, Path: relPath, IsDir: true})
			return nil
		}
	}

	if w.ignore.ShouldIgnore(relPath) {
		return nil
	}

	if !indexer.SupportedExtensions[strings.ToLower(filepath.Ext(event.Name))] {
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

func (w *Watcher) releaseDirectory(path string) (bool, error) {
	w.directoriesMu.Lock()
	if _, ok := w.directories[path]; !ok {
		w.directoriesMu.Unlock()
		return false, nil
	}
	separator := string(filepath.Separator)
	trackedPaths := make([]string, 0)
	for tracked := range w.directories {
		if tracked == path || strings.HasPrefix(tracked, path+separator) {
			trackedPaths = append(trackedPaths, tracked)
		}
	}
	w.directoriesMu.Unlock()

	sort.Slice(trackedPaths, func(i, j int) bool { return len(trackedPaths[i]) > len(trackedPaths[j]) })
	for _, tracked := range trackedPaths {
		if err := w.removeWatch(tracked); err != nil && !w.benignRemoveWatchError(tracked, err) {
			return true, fmt.Errorf("remove directory watch %s: %w", tracked, err)
		}
	}

	w.directoriesMu.Lock()
	for _, tracked := range trackedPaths {
		delete(w.directories, tracked)
	}
	w.directoriesMu.Unlock()
	return true, nil
}

func (w *Watcher) benignRemoveWatchError(path string, err error) bool {
	if errors.Is(err, fsnotify.ErrNonExistentWatch) {
		return true
	}
	if runtime.GOOS != "linux" || !errors.Is(err, syscall.EINVAL) {
		return false
	}
	path = filepath.Clean(path)
	for _, watched := range w.watcher.WatchList() {
		if filepath.Clean(watched) == path {
			return false
		}
	}
	return true
}

func (w *Watcher) debounceEvent(event FileEvent) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()

	// Merge events: delete > create/modify
	existing, exists := w.pending[event.Path]
	if exists && existing.Type == EventDelete && event.Type != EventDelete {
		// Keep delete if file was deleted then recreated quickly
		// This will be handled as delete + create
	} else {
		w.pending[event.Path] = event
	}

	// Reset timer
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(time.Duration(w.debounceMs)*time.Millisecond, w.flush)
}

func (w *Watcher) flush() {
	w.pendingMu.Lock()
	events := make([]FileEvent, 0, len(w.pending))
	for _, event := range w.pending {
		events = append(events, event)
	}
	w.pending = make(map[string]FileEvent)
	w.pendingMu.Unlock()

	for _, event := range events {
		select {
		case w.events <- event:
		default:
			log.Printf("Event channel full, dropping event for %s", event.Path)
		}
	}
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
