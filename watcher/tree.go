package watcher

import (
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
)

// addRecursive walks the tree rooted at root and registers an fsnotify watch
// on every directory that cannot be skipped. It uses filepath.WalkDir so large
// repositories do not incur an extra Lstat syscall per file during startup.
func (w *Watcher) addRecursive(root string) error {
	return w.addRecursiveWithFiles(root, false)
}

func (w *Watcher) addRecursiveWithFiles(root string, emitFiles bool) error {
	if emitFiles {
		// A moved-in tree may bring nested ignore files. Load all of them before
		// considering descendants because ScanFile does not reapply ignores.
		relRoot, err := filepath.Rel(w.root, root)
		if err != nil {
			return fmt.Errorf("resolve ignore refresh subtree: %w", err)
		}
		if err := w.ignore.RefreshSubtree(relRoot); err != nil {
			return fmt.Errorf("refresh ignore files: %w", err)
		}
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip inaccessible paths.
		}

		relPath, err := filepath.Rel(w.root, path)
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if w.ignore.ShouldSkipDir(relPath) {
				return filepath.SkipDir
			}
			// An ignored directory may contain a negated child, so every directory
			// that must be traversed also needs a watch.
			if err := w.addWatch(path); err != nil {
				log.Printf("Failed to watch %s: %v", path, err)
			} else {
				w.directoriesMu.Lock()
				w.directories[path] = struct{}{}
				w.directoriesMu.Unlock()
			}
			return nil
		}

		if w.ignore.ShouldIgnore(relPath) {
			return nil
		}
		if emitFiles && w.supportsFile(path) {
			w.debounceEvent(FileEvent{Type: EventCreate, Path: relPath})
		}
		return nil
	})
}
