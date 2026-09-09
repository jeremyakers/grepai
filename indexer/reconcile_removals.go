package indexer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/yoanbernabeu/grepai/store"
)

func (idx *Indexer) removeCandidatesWithRevalidation(ctx context.Context, candidates map[string]store.DocumentMetadata, exclusions, caseRenames map[string]string) (int, []FileInfo, error) {
	if err := checkScanRoot(idx.root); err != nil {
		return 0, nil, fmt.Errorf("scan root unavailable while reconciling removals: %w", err)
	}
	removed := 0
	var reeligible []FileInfo
	for path := range candidates {
		if err := ctx.Err(); err != nil {
			return removed, reeligible, err
		}
		_, excluded := exclusions[path]
		_, caseRenamed := caseRenames[path]
		_, statErr := os.Lstat(filepath.Join(idx.root, path))
		if statErr == nil && excluded && !caseRenamed {
			file, reason, err := idx.scanner.InspectExistingPath(path)
			if err != nil {
				log.Printf("Warning: cannot revalidate %s (%v); keeping its index entry", path, err)
				continue
			}
			if reason == "" && file != nil {
				reeligible = append(reeligible, *file)
				continue
			}
			excluded = reason != ""
		}
		if statErr == nil && !excluded && !caseRenamed {
			continue
		}
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			log.Printf("Warning: cannot verify %s (%v); keeping its index entry", path, statErr)
			continue
		}
		if err := checkScanRoot(idx.root); err != nil {
			return removed, reeligible, fmt.Errorf("scan root lost before removing %s: %w", path, err)
		}
		if err := idx.RemoveFile(ctx, path); err != nil {
			log.Printf("Failed to remove %s: %v", path, err)
			continue
		}
		removed++
	}
	return removed, reeligible, nil
}
