package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/trace"
)

func removeFileMissingDuringSymbolScan(ctx context.Context, idx *indexer.Indexer, scanner *indexer.Scanner, symbolStore trace.SymbolStore, path string) (bool, error) {
	_, err := os.Lstat(filepath.Join(scanner.Root(), path))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err := verifyInitialScanRoot(scanner.Root()); err != nil {
		return false, fmt.Errorf("symbol scan root unavailable before removing %s: %w", path, err)
	}
	if err := idx.RemoveFile(ctx, path); err != nil {
		return false, fmt.Errorf("remove missing vector file %s: %w", path, err)
	}
	if err := symbolStore.DeleteFile(ctx, path); err != nil {
		return false, fmt.Errorf("remove missing symbol file %s: %w", path, err)
	}
	return true, nil
}

func verifyInitialScanRoot(root string) error {
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	_, err = dir.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func withoutFilePaths(files []indexer.FileMeta, removed []string) []indexer.FileMeta {
	if len(removed) == 0 {
		return files
	}
	removedSet := make(map[string]struct{}, len(removed))
	for _, path := range removed {
		removedSet[path] = struct{}{}
	}
	filtered := files[:0]
	for _, file := range files {
		if _, ok := removedSet[file.Path]; !ok {
			filtered = append(filtered, file)
		}
	}
	return filtered
}
