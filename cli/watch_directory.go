package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/yoanbernabeu/grepai/store"
	"github.com/yoanbernabeu/grepai/trace"
	"github.com/yoanbernabeu/grepai/watcher"
)

type watchPathStat func(string) (fs.FileInfo, error)

type indexedFileLister interface {
	ListIndexedFiles(context.Context) ([]string, error)
}

var errWatchPathReplaced = errors.New("watched path has a non-regular replacement")

func watchPathAbsent(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR)
}

func validateWatchProjectRoot(projectRoot string, stat watchPathStat) error {
	info, err := stat(projectRoot)
	if err != nil {
		return fmt.Errorf("stat watch project root %s: %w", projectRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("watch project root %s is not a directory", projectRoot)
	}
	return nil
}

func requalifyRemovedFile(projectRoot string, event watcher.FileEvent, stat watchPathStat) (watcher.EventType, error) {
	info, err := stat(filepath.Join(projectRoot, event.Path))
	if err == nil && info.Mode().IsRegular() {
		return watcher.EventModify, nil
	}
	if err == nil && info.IsDir() {
		return event.Type, nil
	}
	if err == nil {
		return event.Type, errWatchPathReplaced
	}
	if !watchPathAbsent(err) {
		return event.Type, err
	}
	return event.Type, nil
}

func reconcileDeletedDirectory(ctx context.Context, projectRoot, directory string, vectorStore store.VectorStore, symbolStore trace.SymbolStore, dispatch func(watcher.FileEvent)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory = filepath.Clean(directory)
	if directory == "." {
		return nil
	}
	if err := validateWatchProjectRoot(projectRoot, os.Lstat); err != nil {
		return err
	}

	rootInfo, rootErr := os.Lstat(filepath.Join(projectRoot, directory))
	if rootErr != nil && !watchPathAbsent(rootErr) {
		return fmt.Errorf("stat directory event path %s: %w", directory, rootErr)
	}
	if rootErr == nil && !rootInfo.IsDir() && !rootInfo.Mode().IsRegular() {
		return nil
	}

	paths, err := indexedPathsUnderDirectory(ctx, directory, vectorStore, symbolStore)
	if err != nil {
		// The indexed descendants are unknown, but a confirmed regular replacement
		// is independent new state and must not wait for a watcher restart.
		if rootErr == nil && rootInfo.Mode().IsRegular() && ctx.Err() == nil {
			dispatch(watcher.FileEvent{Type: watcher.EventModify, Path: directory})
		}
		return err
	}
	events, err := planDeletedDirectory(ctx, projectRoot, directory, paths, os.Lstat)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		dispatch(event)
	}
	return ctx.Err()
}

func indexedPathsUnderDirectory(ctx context.Context, directory string, vectorStore store.VectorStore, symbolStore trace.SymbolStore) ([]string, error) {
	candidates := make(map[string]struct{})
	if vectorStore != nil {
		documents, err := vectorStore.ListDocuments(ctx)
		if err != nil {
			return nil, fmt.Errorf("list indexed documents: %w", err)
		}
		addIndexedPaths(candidates, documents, directory)
	}
	if lister, ok := symbolStore.(indexedFileLister); ok {
		files, err := lister.ListIndexedFiles(ctx)
		if err != nil {
			return nil, fmt.Errorf("list indexed symbol files: %w", err)
		}
		addIndexedPaths(candidates, files, directory)
	}
	paths := make([]string, 0, len(candidates))
	for path := range candidates {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func addIndexedPaths(candidates map[string]struct{}, paths []string, directory string) {
	for _, path := range paths {
		if pathInDirectory(path, directory) {
			candidates[filepath.Clean(path)] = struct{}{}
		}
	}
}

func pathInDirectory(path, directory string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	directory = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(directory)), "/")
	return path == directory || strings.HasPrefix(path, directory+"/")
}

func planDeletedDirectory(ctx context.Context, projectRoot, directory string, paths []string, stat watchPathStat) ([]watcher.FileEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory = filepath.Clean(directory)
	if directory == "." {
		return nil, nil
	}
	if err := validateWatchProjectRoot(projectRoot, stat); err != nil {
		return nil, err
	}

	info, err := stat(filepath.Join(projectRoot, directory))
	switch {
	case err == nil && info.Mode().IsRegular():
		events := make([]watcher.FileEvent, 0, len(paths)+1)
		for _, path := range paths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if filepath.Clean(path) != directory {
				events = append(events, watcher.FileEvent{Type: watcher.EventDelete, Path: path})
			}
		}
		return append(events, watcher.FileEvent{Type: watcher.EventModify, Path: directory}), nil
	case err == nil && !info.IsDir():
		return nil, nil
	case err != nil && !watchPathAbsent(err):
		return nil, fmt.Errorf("stat directory event path %s: %w", directory, err)
	case watchPathAbsent(err):
		events := make([]watcher.FileEvent, 0, len(paths))
		for _, path := range paths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			events = append(events, watcher.FileEvent{Type: watcher.EventDelete, Path: path})
		}
		return events, nil
	}

	events := make([]watcher.FileEvent, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := stat(filepath.Join(projectRoot, path))
		switch {
		case err == nil && info.Mode().IsRegular():
			events = append(events, watcher.FileEvent{Type: watcher.EventModify, Path: path})
		case err == nil:
			// A replacement exists at this path. Leave its indexed state alone.
		case watchPathAbsent(err):
			events = append(events, watcher.FileEvent{Type: watcher.EventDelete, Path: path})
		default:
			return nil, fmt.Errorf("stat indexed path %s: %w", path, err)
		}
	}
	return events, nil
}
