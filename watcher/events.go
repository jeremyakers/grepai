package watcher

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yoanbernabeu/grepai/indexer"
)

func (w *Watcher) processEvents(ctx context.Context) {
	defer close(w.processingDone)
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
				w.publishFatal(err)
				return
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.publishFatal(&FatalError{Operation: "process filesystem events", Path: w.root, Cause: err})
			return
		}
	}
}

func (w *Watcher) publishFatal(err error) {
	select {
	case w.errors <- err:
	default:
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) error {
	relPath, err := filepath.Rel(w.root, event.Name)
	if err != nil {
		return nil
	}

	if strings.HasPrefix(filepath.Base(relPath), ".") || w.ignore.ShouldIgnore(relPath) {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(event.Name))
	if !indexer.SupportedExtensions[ext] {
		info, err := os.Stat(event.Name)
		if err != nil || !info.IsDir() {
			return nil
		}
		if event.Has(fsnotify.Create) {
			if err := w.addRecursive(event.Name, false); err != nil {
				return &FatalError{Operation: "register new directory", Path: event.Name, Cause: err}
			}
		}
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
	w.debounceEvent(FileEvent{Type: evType, Path: relPath})
	return nil
}

func (w *Watcher) debounceEvent(event FileEvent) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	existing, exists := w.pending[event.Path]
	if !exists || existing.Type != EventDelete || event.Type == EventDelete {
		w.pending[event.Path] = event
	}
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
