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

type removalReconciliation struct {
	reeligible     []FileInfo
	retiredAliases []RetiredAlias
}

func (idx *Indexer) applyRemovalReconciliation(ctx context.Context, stats *IndexStats, result removalReconciliation) error {
	for _, alias := range result.retiredAliases {
		stats.ScannedFiles = removeFileMetaPath(stats.ScannedFiles, alias.Path)
		stats.ExcludedFiles = removeStringPath(stats.ExcludedFiles, alias.Path)
		stats.RetiredAliases = append(stats.RetiredAliases, alias)
	}
	for _, file := range result.reeligible {
		chunks, err := idx.IndexFile(ctx, file)
		if err != nil {
			return fmt.Errorf("index re-eligible file %s: %w", file.Path, err)
		}
		stats.FilesIndexed++
		stats.ChunksCreated += chunks
		stats.ScannedFiles = append(stats.ScannedFiles, FileMeta{Path: file.Path, Size: file.Size, ModTime: file.ModTime, ObservedModTime: file.ObservedModTime})
		stats.ExcludedFiles = removeStringPath(stats.ExcludedFiles, file.Path)
	}
	return nil
}

func removeFileMetaPath(files []FileMeta, target string) []FileMeta {
	filtered := files[:0]
	for _, file := range files {
		if file.Path != target {
			filtered = append(filtered, file)
		}
	}
	return filtered
}

func removeStringPath(paths []string, target string) []string {
	filtered := paths[:0]
	for _, path := range paths {
		if path != target {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

func (idx *Indexer) removeCandidatesWithRevalidation(ctx context.Context, candidates map[string]store.DocumentMetadata, exclusions, caseRenames map[string]string) (int, removalReconciliation, error) {
	if err := checkScanRoot(idx.root); err != nil {
		return 0, removalReconciliation{}, fmt.Errorf("scan root unavailable while reconciling removals: %w", err)
	}
	removed := 0
	result := removalReconciliation{}
	for path := range candidates {
		if err := ctx.Err(); err != nil {
			return removed, result, err
		}
		_, excluded := exclusions[path]
		caseWitness, caseRenamed := caseRenames[path]
		_, statErr := os.Lstat(filepath.Join(idx.root, path))
		if caseRenamed {
			current, err := RevalidateCaseRenameWitness(idx.root, path, caseWitness)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return removed, result, fmt.Errorf("revalidate case rename %s to %s: %w", path, caseWitness, err)
			}
			if !current {
				file, reason, inspectErr := idx.scanner.InspectExistingPath(path)
				if inspectErr != nil && !errors.Is(inspectErr, fs.ErrNotExist) {
					return removed, result, fmt.Errorf("inspect reversed case rename %s: %w", path, inspectErr)
				}
				caseRenamed = false
				if file != nil && reason == "" {
					retireAlias, retireErr := CanRetireCaseAlias(idx.root, path, caseWitness)
					if retireErr != nil {
						return removed, result, fmt.Errorf("verify temporary case alias %s: %w", caseWitness, retireErr)
					}
					if retireAlias {
						if err := checkScanRoot(idx.root); err != nil {
							return removed, result, fmt.Errorf("scan root lost before reconciling reversed case rename %s: %w", path, err)
						}
						if err := idx.RemoveFile(ctx, caseWitness); err != nil {
							return removed, result, fmt.Errorf("remove temporary case alias %s: %w", caseWitness, err)
						}
						removed++
						result.retiredAliases = append(result.retiredAliases, RetiredAlias{Path: caseWitness, CanonicalPath: path})
					}
					result.reeligible = append(result.reeligible, *file)
					continue
				}
				excluded = reason != ""
				if excluded {
					retireAlias, retireErr := CanRetireCaseAlias(idx.root, path, caseWitness)
					if retireErr != nil {
						return removed, result, fmt.Errorf("verify temporary excluded case alias %s: %w", caseWitness, retireErr)
					}
					if retireAlias {
						if err := checkScanRoot(idx.root); err != nil {
							return removed, result, fmt.Errorf("scan root lost before removing excluded case alias %s: %w", caseWitness, err)
						}
						if err := idx.RemoveFile(ctx, caseWitness); err != nil {
							return removed, result, fmt.Errorf("remove temporary excluded case alias %s: %w", caseWitness, err)
						}
						removed++
						result.retiredAliases = append(result.retiredAliases, RetiredAlias{Path: caseWitness, CanonicalPath: path})
					}
				}
			}
		}
		if statErr == nil && excluded && !caseRenamed {
			file, reason, err := idx.scanner.InspectExistingPath(path)
			if err != nil {
				log.Printf("Warning: cannot revalidate %s (%v); keeping its index entry", path, err)
				continue
			}
			if reason == "" && file != nil {
				result.reeligible = append(result.reeligible, *file)
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
			return removed, result, fmt.Errorf("scan root lost before removing %s: %w", path, err)
		}
		if err := idx.RemoveFile(ctx, path); err != nil {
			log.Printf("Failed to remove %s: %v", path, err)
			continue
		}
		removed++
	}
	return removed, result, nil
}
